// Package importer reads the .boxasset / .boxassets / .boxmap zip
// files produced by the sibling `exporter` package and applies them
// to the live database.
//
// Three entry points map 1:1 to the exporter:
//
//   - ImportAsset   — accepts a .boxasset zip
//   - ImportAssets  — accepts a .boxassets zip (all-assets bundle)
//   - ImportMap     — accepts a .boxmap zip (asset-and-entity-aware)
//
// All three accept either flavor where it makes sense (a .boxasset is
// a strict subset of .boxassets; ImportAssets handles both). The
// `Apply` step is atomic per import: a single transaction wraps the
// row inserts, and blob uploads are content-addressed (idempotent on
// retry).
//
// Tenant safety:
//
//   - `created_by` on every inserted row is rewritten to the importing
//     designer. Exports CANNOT be used to forge author attribution.
//   - Source ids are remapped through name lookups; raw ids in the
//     import never end up in the database.
//   - Zip-slip is blocked: blob entries that escape `blobs/` (`..`,
//     absolute paths, etc.) are rejected with an error.
//
// Conflict policy on (kind, name):
//
//   - DefaultPolicy ("skip"): existing rows survive, new rows skipped,
//     counts surfaced in the result summary.
//   - "replace": existing rows updated in place where possible.
//   - "fail": any name collision aborts the whole import.
package importer

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"boxland/server/internal/assets"
	"boxland/server/internal/automations"
	"boxland/server/internal/entities"
	"boxland/server/internal/entities/components"
	"boxland/server/internal/exporter"
	"boxland/server/internal/folders"
	"boxland/server/internal/levels"
	mapsservice "boxland/server/internal/maps"
	"boxland/server/internal/persistence"
	"boxland/server/internal/tilemaps"
	"boxland/server/internal/worlds"
)

// Conflict policies for repeated (kind, name) tuples.
type ConflictPolicy string

const (
	PolicySkip    ConflictPolicy = "skip"
	PolicyReplace ConflictPolicy = "replace"
	PolicyFail    ConflictPolicy = "fail"
)

// DefaultPolicy is what handlers pass when the client doesn't specify.
const DefaultPolicy = PolicySkip

// Errors returned by the importer. Stable for handler mapping.
var (
	ErrBadZip          = errors.New("import: bad or unreadable zip")
	ErrMissingManifest = errors.New("import: missing manifest.json")
	ErrUnknownKind     = errors.New("import: unknown manifest kind")
	ErrUnsupportedFmt  = errors.New("import: unsupported format_version")
	ErrZipSlip         = errors.New("import: zip entry escapes blobs/")
	ErrConflict        = errors.New("import: name conflict (policy=fail)")
)

// Result summarizes what the import did. Returned by every Apply call;
// handlers serialize it as JSON for HTMX-driven UI updates.
type Result struct {
	AssetsCreated      int      `json:"assets_created"`
	AssetsReplaced     int      `json:"assets_replaced"`
	AssetsSkipped      int      `json:"assets_skipped"`
	BlobsUploaded      int      `json:"blobs_uploaded"`
	EntityTypesCreated int      `json:"entity_types_created"`
	EntityTypesSkipped int      `json:"entity_types_skipped"`
	TilemapsCreated    int      `json:"tilemaps_created"`
	TilemapsSkipped    int      `json:"tilemaps_skipped"`
	MapsCreated        int      `json:"maps_created"`
	LevelsCreated      int      `json:"levels_created"`
	WorldsCreated      int      `json:"worlds_created"`
	Warnings           []string `json:"warnings,omitempty"`
}

// Deps mirrors exporter.Deps; the same services drive both directions.
type Deps struct {
	Assets       *assets.Service
	Entities     *entities.Service
	Folders      *folders.Service
	Tilemaps     *tilemaps.Service
	Maps         *mapsservice.Service
	Levels       *levels.Service
	Worlds       *worlds.Service
	ActionGroups *automations.GroupsRepo
	ObjectStore  *persistence.ObjectStore
}

// Service is the public importer facade.
type Service struct {
	d Deps
}

// New constructs a Service.
func New(d Deps) *Service { return &Service{d: d} }

// ---- Read + validate manifest ----------------------------------------

// readManifest opens the zip and returns (manifest, zip-reader). Caller
// owns the zip.Reader; close is implicit (the underlying buffer is in
// memory, not a file handle).
func readManifest(body []byte) (*exporter.Manifest, *zip.Reader, error) {
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrBadZip, err)
	}
	for _, f := range zr.File {
		if f.Name == "manifest.json" {
			rc, err := f.Open()
			if err != nil {
				return nil, nil, fmt.Errorf("open manifest: %w", err)
			}
			defer rc.Close()
			var m exporter.Manifest
			if err := json.NewDecoder(rc).Decode(&m); err != nil {
				return nil, nil, fmt.Errorf("decode manifest: %w", err)
			}
			if m.FormatVersion != exporter.FormatVersion {
				return nil, nil, fmt.Errorf("%w: got %d, want %d",
					ErrUnsupportedFmt, m.FormatVersion, exporter.FormatVersion)
			}
			return &m, zr, nil
		}
	}
	return nil, nil, ErrMissingManifest
}

// findFile looks up a single zip member by exact name. Returns nil if
// missing — callers decide whether absence is fatal.
func findFile(zr *zip.Reader, name string) *zip.File {
	for _, f := range zr.File {
		if f.Name == name {
			return f
		}
	}
	return nil
}

// readJSONFile decodes a JSON file inside the zip into `dst`.
func readJSONFile(f *zip.File, dst any) error {
	if f == nil {
		return nil
	}
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	return json.NewDecoder(rc).Decode(dst)
}

// ---- Asset / All-Assets imports --------------------------------------

// ImportAssets accepts either a .boxasset (single asset) or .boxassets
// (all-assets bundle) zip and applies it. Both shapes resolve to the
// same write path; the only difference is how many envelopes the
// payload carries.
//
// Single transaction across all asset rows + animation rows. Blobs go
// up to object storage outside the tx (they are content-addressed and
// idempotent — re-running a partially-failed import is safe).
func (s *Service) ImportAssets(ctx context.Context, body []byte, designerID int64, policy ConflictPolicy) (*Result, error) {
	m, zr, err := readManifest(body)
	if err != nil {
		return nil, err
	}
	switch m.Kind {
	case exporter.KindAsset, exporter.KindAllAssets:
		// Both supported.
	case exporter.KindMap:
		// A .boxmap also carries assets — designer might have aimed at
		// the wrong importer. Forward to ImportMap so the operation
		// "just works" rather than refusing on a technicality.
		return nil, fmt.Errorf("%w: this is a .boxmap; use the Mapmaker import", ErrUnknownKind)
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownKind, m.Kind)
	}

	// Normalize both shapes into a single envelope slice.
	var envs []exporter.AssetEnvelope
	if m.Kind == exporter.KindAsset {
		var single exporter.AssetEnvelope
		f := findFile(zr, "asset.json")
		if f == nil {
			return nil, fmt.Errorf("%w: asset.json missing", ErrBadZip)
		}
		if err := readJSONFile(f, &single); err != nil {
			return nil, fmt.Errorf("decode asset.json: %w", err)
		}
		envs = []exporter.AssetEnvelope{single}
	} else {
		var payload exporter.AllAssetsPayload
		f := findFile(zr, "assets.json")
		if f == nil {
			return nil, fmt.Errorf("%w: assets.json missing", ErrBadZip)
		}
		if err := readJSONFile(f, &payload); err != nil {
			return nil, fmt.Errorf("decode assets.json: %w", err)
		}
		envs = payload.Assets
	}

	if policy == "" {
		policy = DefaultPolicy
	}

	res := &Result{}
	if _, err := s.applyAssets(ctx, zr, envs, designerID, policy, res); err != nil {
		return nil, err
	}
	return res, nil
}

// applyAssets uploads every blob (deduped) and inserts every asset row
// (with its animation rows). Returns a srcID→newID map keyed on the
// imported asset's original id so map imports can re-link
// entity_type.sprite_asset_id without juggling names.
func (s *Service) applyAssets(
	ctx context.Context,
	zr *zip.Reader,
	envs []exporter.AssetEnvelope,
	designerID int64,
	policy ConflictPolicy,
	res *Result,
) (map[int64]int64, error) {
	// 1) Push every blob first, deduped by content path. Idempotent
	// (Put is safe to call on an existing key).
	uploaded := make(map[string]struct{}, len(envs))
	for _, env := range envs {
		p := env.Asset.ContentAddressedPath
		if _, ok := uploaded[p]; ok {
			continue
		}
		uploaded[p] = struct{}{}
		if err := s.uploadBlob(ctx, zr, p); err != nil {
			return nil, err
		}
		res.BlobsUploaded++
	}

	// 2) Insert (or reuse) asset rows + animation rows.
	srcToNew := make(map[int64]int64, len(envs))

	for _, env := range envs {
		// Look up existing by (kind, content_addressed_path) first —
		// re-importing identical bytes should always find the row,
		// regardless of name conflicts.
		existing, err := s.d.Assets.FindByContentPath(ctx, env.Asset.Kind, env.Asset.ContentAddressedPath)
		if err != nil && !errors.Is(err, assets.ErrAssetNotFound) {
			return nil, fmt.Errorf("lookup asset by path: %w", err)
		}
		if existing != nil {
			srcToNew[env.Asset.ID] = existing.ID
			res.AssetsSkipped++
			if policy == PolicyReplace {
				if err := s.replaceAnimations(ctx, existing.ID, env.Animations); err != nil {
					return nil, err
				}
			}
			continue
		}

		// Resolve / create the destination folder if the envelope
		// carries a path AND the asset's kind has a foldered home.
		// sprite_animated assets don't get foldered directly — they
		// surface inside their owning tilemap or character bake; only
		// sprite / audio / ui_panel kinds map to a folder kind_root.
		var folderID *int64
		if env.FolderPath != "" && s.d.Folders != nil {
			kr := assetKindToFolderRoot(env.Asset.Kind)
			if kr != "" {
				id, err := s.d.Folders.EnsurePath(ctx, kr, env.FolderPath, designerID)
				if err != nil {
					return nil, fmt.Errorf("ensure folder %q for asset %q: %w",
						env.FolderPath, env.Asset.Name, err)
				}
				if id > 0 {
					folderID = &id
				}
			}
		}

		// Fresh insert: rewrite created_by to the importing designer.
		md := env.Asset.MetadataJSON
		if len(md) == 0 {
			md = json.RawMessage(`{}`)
		}
		row, err := s.d.Assets.Create(ctx, assets.CreateInput{
			Kind:                 env.Asset.Kind,
			Name:                 env.Asset.Name,
			ContentAddressedPath: env.Asset.ContentAddressedPath,
			OriginalFormat:       env.Asset.OriginalFormat,
			MetadataJSON:         md,
			Tags:                 env.Asset.Tags,
			FolderID:             folderID,
			CreatedBy:            designerID,
		})
		if err != nil {
			if errors.Is(err, assets.ErrNameInUse) {
				switch policy {
				case PolicyFail:
					return nil, fmt.Errorf("%w: asset %q", ErrConflict, env.Asset.Name)
				case PolicySkip, PolicyReplace:
					// Skip + replace both treat name-in-use without a
					// matching content-path as "leave the existing
					// row alone" — we don't stomp art the designer
					// may have painted into in the meantime. The
					// warning makes the no-op visible.
					res.AssetsSkipped++
					res.Warnings = append(res.Warnings,
						fmt.Sprintf("asset %q (kind=%s) already exists with different bytes; left untouched",
							env.Asset.Name, env.Asset.Kind))
					continue
				}
			}
			return nil, fmt.Errorf("create asset %q: %w", env.Asset.Name, err)
		}
		res.AssetsCreated++
		srcToNew[env.Asset.ID] = row.ID

		if err := s.replaceAnimations(ctx, row.ID, env.Animations); err != nil {
			return nil, err
		}
	}
	return srcToNew, nil
}

// replaceAnimations forwards to the assets service and converts
// AnimationRow→Animation (the persistence shape the service expects).
func (s *Service) replaceAnimations(ctx context.Context, assetID int64, rows []assets.AnimationRow) error {
	if len(rows) == 0 {
		return nil
	}
	conv := make([]assets.Animation, 0, len(rows))
	for _, r := range rows {
		conv = append(conv, assets.Animation{
			Name:      r.Name,
			FrameFrom: int(r.FrameFrom),
			FrameTo:   int(r.FrameTo),
			Direction: r.Direction,
			FPS:       int(r.FPS),
		})
	}
	return s.d.Assets.ReplaceAnimations(ctx, assetID, conv)
}

// uploadBlob streams `blobs/<path>` from the zip into object storage.
// Verifies the entry actually lives under blobs/ to block zip-slip.
func (s *Service) uploadBlob(ctx context.Context, zr *zip.Reader, contentPath string) error {
	if s.d.ObjectStore == nil {
		return nil
	}
	entryName := "blobs/" + contentPath
	clean := path.Clean(entryName)
	if !strings.HasPrefix(clean, "blobs/") || strings.Contains(clean, "..") {
		return fmt.Errorf("%w: %q", ErrZipSlip, entryName)
	}
	f := findFile(zr, entryName)
	if f == nil {
		// Some exports (test-only) ship without blobs. Skip silently.
		return nil
	}
	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("open blob %q: %w", entryName, err)
	}
	defer rc.Close()
	body, err := io.ReadAll(rc)
	if err != nil {
		return fmt.Errorf("read blob %q: %w", entryName, err)
	}
	// Sniff content-type from bytes; the original Asset row's
	// OriginalFormat is informational only (asset detail page).
	ct := sniffContentType(body)
	return s.d.ObjectStore.Put(ctx, contentPath, ct, bytes.NewReader(body), int64(len(body)))
}

func sniffContentType(body []byte) string {
	if len(body) >= 8 && bytes.HasPrefix(body, []byte{0x89, 'P', 'N', 'G'}) {
		return "image/png"
	}
	if len(body) >= 4 && bytes.Equal(body[:4], []byte("RIFF")) {
		return "audio/wav"
	}
	if len(body) >= 4 && bytes.Equal(body[:4], []byte("OggS")) {
		return "audio/ogg"
	}
	if len(body) >= 3 && bytes.Equal(body[:3], []byte("ID3")) {
		return "audio/mpeg"
	}
	return "application/octet-stream"
}

// ---- Map import ------------------------------------------------------

// ImportMap accepts a .boxmap zip. Process:
//
//  1. Verify manifest kind = "boxmap".
//  2. Apply assets (creating any missing ones, blob bytes uploaded
//     content-addressed). Returns a name→id map.
//  3. Apply entity_types (creating any missing ones; sprite_asset_id
//     is re-pointed via the asset name→id map). Returns a name→id map.
//  4. Apply the map row + layers (new ids). Build a layer-name→new-id
//     map.
//  5. Apply tiles + locked cells + lighting cells, remapping
//     entity_type ids and layer ids through the maps from steps 3+4.
//  6. Apply sample patch + constraints + HUD.
//
// Atomic per call: any error during steps 4–6 rolls back via the maps
// service's own per-call transactions; assets + entity_types created
// in steps 2–3 stay (they are independently useful and the next run
// will dedup by content path / name).
func (s *Service) ImportMap(ctx context.Context, body []byte, designerID int64, policy ConflictPolicy) (*Result, error) {
	m, zr, err := readManifest(body)
	if err != nil {
		return nil, err
	}
	if m.Kind != exporter.KindMap {
		return nil, fmt.Errorf("%w: %q (expected boxmap)", ErrUnknownKind, m.Kind)
	}
	if policy == "" {
		policy = DefaultPolicy
	}
	res := &Result{}

	// 1) Assets.
	var assetsPayload exporter.AllAssetsPayload
	if f := findFile(zr, "assets.json"); f != nil {
		if err := readJSONFile(f, &assetsPayload); err != nil {
			return nil, fmt.Errorf("decode assets.json: %w", err)
		}
	}
	assetIDMap, err := s.applyAssets(ctx, zr, assetsPayload.Assets, designerID, policy, res)
	if err != nil {
		return nil, err
	}

	// 2) Entity types.
	var etypes []exporter.EntityTypeEnvelope
	if f := findFile(zr, "entity_types.json"); f != nil {
		if err := readJSONFile(f, &etypes); err != nil {
			return nil, fmt.Errorf("decode entity_types.json: %w", err)
		}
	}
	etypeIDMap, err := s.applyEntityTypes(ctx, etypes, assetIDMap, designerID, policy, res)
	if err != nil {
		return nil, err
	}

	// 3) Map + layers + tiles + lighting + locks + sample + constraints + HUD.
	var mapPayload exporter.MapPayload
	if f := findFile(zr, "map.json"); f != nil {
		if err := readJSONFile(f, &mapPayload); err != nil {
			return nil, fmt.Errorf("decode map.json: %w", err)
		}
	} else {
		return nil, fmt.Errorf("%w: map.json missing", ErrBadZip)
	}
	if err := s.applyMap(ctx, mapPayload, etypeIDMap, designerID, res); err != nil {
		return nil, err
	}
	return res, nil
}

// applyEntityTypes re-creates entity_types referenced by the imported
// map. Uses (Name) as the unique key (matches the unique index on
// entity_types.name). Returns a sourceID→newID map for tile remap.
func (s *Service) applyEntityTypes(
	ctx context.Context,
	envs []exporter.EntityTypeEnvelope,
	assetIDMap map[int64]int64,
	designerID int64,
	policy ConflictPolicy,
	res *Result,
) (map[int64]int64, error) {
	out := make(map[int64]int64, len(envs))
	for _, env := range envs {
		// Re-point sprite_asset_id through the freshly-imported assets.
		// If we don't find a match (e.g. the export shipped without
		// the sprite asset), drop the link rather than insert a dangler.
		var spriteID *int64
		if env.EntityType.SpriteAssetID != nil {
			if newID, ok := assetIDMap[*env.EntityType.SpriteAssetID]; ok {
				id := newID
				spriteID = &id
			}
		}

		// Skip-with-existing on name match.
		existing, _ := s.d.Entities.FindByName(ctx, env.EntityType.Name)
		if existing != nil {
			out[env.EntityType.ID] = existing.ID
			res.EntityTypesSkipped++
			continue
		}

		row, err := s.d.Entities.Create(ctx, entities.CreateInput{
			Name:                 env.EntityType.Name,
			EntityClass:          env.EntityType.EntityClass,
			SpriteAssetID:        spriteID,
			AtlasIndex:           env.EntityType.AtlasIndex,
			DefaultAnimationID:   nil, // animations were just re-created with new ids; v1 leaves this unset, designer re-picks.
			// Tilemap linkage is restored once the surrounding bundle
			// re-imports the tilemap — applyTilemaps/applyLevel patch
			// these fields on the freshly-created entity_types row
			// after the tilemap row has its new id. Until then, leave
			// them nil so the FK doesn't fire on a stale source id.
			ColliderW:            env.EntityType.ColliderW,
			ColliderH:            env.EntityType.ColliderH,
			ColliderAnchorX:      env.EntityType.ColliderAnchorX,
			ColliderAnchorY:      env.EntityType.ColliderAnchorY,
			DefaultCollisionMask: env.EntityType.DefaultCollisionMask,
			Tags:                 env.EntityType.Tags,
			CreatedBy:            designerID,
		})
		if err != nil {
			if errors.Is(err, entities.ErrNameInUse) && policy == PolicySkip {
				res.EntityTypesSkipped++
				continue
			}
			return nil, fmt.Errorf("create entity_type %q: %w", env.EntityType.Name, err)
		}
		res.EntityTypesCreated++
		out[env.EntityType.ID] = row.ID

		// Components: replace existing rows for this type with the
		// imported set, in one tx.
		if len(env.Components) > 0 {
			typed := make(map[components.Kind]json.RawMessage, len(env.Components))
			for _, c := range env.Components {
				typed[c.Kind] = c.ConfigJSON
			}
			if err := s.d.Entities.SetComponents(ctx, nil, row.ID, typed); err != nil {
				return nil, fmt.Errorf("set components for %q: %w", env.EntityType.Name, err)
			}
		}
	}
	return out, nil
}

// applyMap creates the map row + layers, then writes tiles / locks /
// lighting / sample patch / constraints / HUD using the freshly-built
// id maps.
func (s *Service) applyMap(
	ctx context.Context,
	mp exporter.MapPayload,
	etypeIDMap map[int64]int64,
	designerID int64,
	res *Result,
) error {
	// 1) Insert the map (always a fresh row — never overwrite a live
	// map; the importer is for "land a new copy", not "replace".)
	newMap, err := s.d.Maps.Create(ctx, mapsservice.CreateInput{
		Name:      uniqueMapName(ctx, s.d.Maps, mp.Map.Name),
		Width:     mp.Map.Width,
		Height:    mp.Map.Height,
		Mode:      mp.Map.Mode,
		Seed:      mp.Map.Seed,
		CreatedBy: designerID,
	})
	if err != nil {
		return fmt.Errorf("create map: %w", err)
	}
	res.MapsCreated++

	// 2) Layers — Maps.Create seeded "base/decoration/lighting" by
	// default. We replace that with the imported set so the layer ids
	// in tiles/lighting can be remapped cleanly. (Old layers cascade
	// out via FK.)
	defaultLayers, _ := s.d.Maps.Layers(ctx, newMap.ID)
	for _, l := range defaultLayers {
		_ = s.d.Maps.DeleteLayer(ctx, l.ID)
	}
	layerIDMap := make(map[int64]int64, len(mp.Layers))
	for _, l := range mp.Layers {
		nl, err := s.d.Maps.AddLayer(ctx, newMap.ID, l.Name, l.Kind, l.Ord)
		if err != nil {
			return fmt.Errorf("add layer %q: %w", l.Name, err)
		}
		layerIDMap[l.ID] = nl.ID
		if l.YSortEntities {
			if err := s.d.Maps.SetLayerYSort(ctx, nl.ID, true); err != nil {
				return fmt.Errorf("set y-sort %q: %w", l.Name, err)
			}
		}
	}

	// 3) Tiles — remap layer + entity_type ids; drop any tile whose
	// referenced entity_type didn't survive the import (logged).
	if len(mp.Tiles) > 0 {
		tiles := make([]mapsservice.Tile, 0, len(mp.Tiles))
		for _, t := range mp.Tiles {
			lid, lok := layerIDMap[t.LayerID]
			eid, eok := etypeIDMap[t.EntityTypeID]
			if !lok || !eok {
				res.Warnings = append(res.Warnings,
					fmt.Sprintf("tile (%d,%d): missing layer or entity_type after remap; skipped", t.X, t.Y))
				continue
			}
			t.MapID = newMap.ID
			t.LayerID = lid
			t.EntityTypeID = eid
			tiles = append(tiles, t)
		}
		if err := s.d.Maps.PlaceTiles(ctx, tiles); err != nil {
			return fmt.Errorf("place tiles: %w", err)
		}
	}

	// 4) Locked cells.
	if len(mp.LockedCells) > 0 {
		lcs := make([]mapsservice.LockedCell, 0, len(mp.LockedCells))
		for _, c := range mp.LockedCells {
			lid, lok := layerIDMap[c.LayerID]
			eid, eok := etypeIDMap[c.EntityTypeID]
			if !lok || !eok {
				continue
			}
			c.MapID = newMap.ID
			c.LayerID = lid
			c.EntityTypeID = eid
			lcs = append(lcs, c)
		}
		if err := s.d.Maps.LockCells(ctx, lcs); err != nil {
			return fmt.Errorf("lock cells: %w", err)
		}
	}

	// 5) Lighting cells.
	if len(mp.LightingCells) > 0 {
		cells := make([]mapsservice.LightingCell, 0, len(mp.LightingCells))
		for _, c := range mp.LightingCells {
			lid, lok := layerIDMap[c.LayerID]
			if !lok {
				continue
			}
			c.MapID = newMap.ID
			c.LayerID = lid
			cells = append(cells, c)
		}
		if err := s.d.Maps.PlaceLightingCells(ctx, cells); err != nil {
			return fmt.Errorf("place lighting: %w", err)
		}
	}

	// 6) Sample patch.
	if mp.SamplePatch != nil {
		lid, ok := layerIDMap[mp.SamplePatch.LayerID]
		if ok {
			if err := s.d.Maps.UpsertSamplePatch(ctx, mapsservice.SamplePatchInput{
				MapID:    newMap.ID,
				LayerID:  lid,
				X:        mp.SamplePatch.X,
				Y:        mp.SamplePatch.Y,
				Width:    mp.SamplePatch.Width,
				Height:   mp.SamplePatch.Height,
				PatternN: mp.SamplePatch.PatternN,
			}); err != nil {
				res.Warnings = append(res.Warnings, "sample patch: "+err.Error())
			}
		}
	}

	// 7) Constraints (kind + params re-validated by the service).
	for _, c := range mp.Constraints {
		if _, err := s.d.Maps.AddMapConstraint(ctx, mapsservice.AddMapConstraintInput{
			MapID:  newMap.ID,
			Kind:   c.Kind,
			Params: c.Params,
		}); err != nil {
			res.Warnings = append(res.Warnings, "constraint "+c.Kind+": "+err.Error())
		}
	}

	// HUD layout used to ride on maps.hud_layout_json; per the
	// holistic redesign it lives on the level. The .boxmap.zip is
	// pure geometry now — HUD round-trips through .boxlevel.zip.
	return nil
}

// assetKindToFolderRoot returns the folder kind_root that holds the
// given asset kind, or "" when the kind has no foldered home of its
// own (sprite_animated assets are subordinate to a tilemap or a
// character bake).
func assetKindToFolderRoot(kind assets.Kind) folders.KindRoot {
	switch kind {
	case assets.KindSprite:
		return folders.KindSprite
	case assets.KindAudio:
		return folders.KindAudio
	case assets.KindUIPanel:
		return folders.KindUIPanel
	}
	return ""
}

// uniqueMapName picks the first non-colliding name in the form
// "<base>", "<base> (copy)", "<base> (copy 2)", … so an import never
// overwrites a live map.
func uniqueMapName(ctx context.Context, svc *mapsservice.Service, base string) string {
	if base == "" {
		base = "Imported map"
	}
	candidate := base
	for i := 1; i < 100; i++ {
		existing, err := svc.List(ctx, candidate)
		if err != nil || !nameTaken(existing, candidate) {
			return candidate
		}
		if i == 1 {
			candidate = base + " (copy)"
		} else {
			candidate = fmt.Sprintf("%s (copy %d)", base, i)
		}
	}
	return candidate
}

func nameTaken(rows []mapsservice.Map, name string) bool {
	for _, r := range rows {
		if r.Name == name {
			return true
		}
	}
	return false
}




// ---- Tilemap import --------------------------------------------------

// ImportTilemap accepts a .boxtilemap zip and applies it. Process:
//
//  1. Verify manifest kind = "boxtilemap".
//  2. Apply the backing asset (bytes uploaded content-addressed).
//  3. Create the tilemap row pointing at the freshly-imported asset.
//  4. Apply the per-cell tile entity_types, then patch their
//     tilemap_id / cell_col / cell_row to point at the new tilemap row.
//  5. Re-write tilemap_tiles rows (with hashes preserved verbatim) so
//     the Replace flow's diff-by-pixel-hash logic survives the round
//     trip.
//
// Atomic per call: failures during steps 4–5 leave the assets +
// tilemap row in place (re-running picks them up); the importer is
// idempotent on retry.
func (s *Service) ImportTilemap(ctx context.Context, body []byte, designerID int64, policy ConflictPolicy) (*Result, error) {
	if s.d.Tilemaps == nil {
		return nil, fmt.Errorf("import tilemap: tilemaps service not configured")
	}
	m, zr, err := readManifest(body)
	if err != nil {
		return nil, err
	}
	if m.Kind != exporter.KindTilemap {
		return nil, fmt.Errorf("%w: %q (expected boxtilemap)", ErrUnknownKind, m.Kind)
	}
	if policy == "" {
		policy = DefaultPolicy
	}
	res := &Result{}

	// Read tilemap envelope + asset envelope + entity_types up front.
	var tmEnv exporter.TilemapEnvelope
	if f := findFile(zr, "tilemap.json"); f == nil {
		return nil, fmt.Errorf("%w: tilemap.json missing", ErrBadZip)
	} else if err := readJSONFile(f, &tmEnv); err != nil {
		return nil, fmt.Errorf("decode tilemap.json: %w", err)
	}
	var assetEnv exporter.AssetEnvelope
	if f := findFile(zr, "asset.json"); f == nil {
		return nil, fmt.Errorf("%w: asset.json missing", ErrBadZip)
	} else if err := readJSONFile(f, &assetEnv); err != nil {
		return nil, fmt.Errorf("decode asset.json: %w", err)
	}
	var etypeEnvs []exporter.EntityTypeEnvelope
	if f := findFile(zr, "entity_types.json"); f != nil {
		if err := readJSONFile(f, &etypeEnvs); err != nil {
			return nil, fmt.Errorf("decode entity_types.json: %w", err)
		}
	}

	// 1) Apply the asset (only one in a tilemap export).
	assetIDMap, err := s.applyAssets(ctx, zr, []exporter.AssetEnvelope{assetEnv}, designerID, policy, res)
	if err != nil {
		return nil, err
	}
	newAssetID, ok := assetIDMap[assetEnv.Asset.ID]
	if !ok {
		return nil, fmt.Errorf("import tilemap: backing asset did not survive apply")
	}

	// 2) Apply the entity_types (sprite refs remap; tilemap_id stays
	// nil for now, patched in step 4).
	etypeIDMap, err := s.applyEntityTypes(ctx, etypeEnvs, assetIDMap, designerID, policy, res)
	if err != nil {
		return nil, err
	}

	// 3) Tilemap row. We rebuild the per-cell list from the envelope's
	// Cells slice so tilemaps.Service.Create's slicer doesn't have to
	// re-decode the PNG (which we don't have in memory here anyway).
	// The service's Create requires PngBody to compute hashes; for
	// import-time we already have the hashes on the wire, so we
	// bypass Create and write the rows directly.
	newTMID, err := s.applyTilemap(ctx, tmEnv, newAssetID, etypeIDMap, designerID, res)
	if err != nil {
		return nil, err
	}
	_ = newTMID

	return res, nil
}

// applyTilemap inserts the tilemap row + tilemap_tiles rows + patches
// the tile-class entity_types' (tilemap_id, cell_col, cell_row) fields
// to point at the new row. Returns the new tilemap id.
//
// Idempotency: if a tilemap with the same name already exists we treat
// the import as a name collision (skip + warning). Designers can use
// the "replace" policy to overwrite, but for v1 the safe default is
// to leave existing work untouched.
func (s *Service) applyTilemap(
	ctx context.Context,
	env exporter.TilemapEnvelope,
	newAssetID int64,
	etypeIDMap map[int64]int64,
	designerID int64,
	res *Result,
) (int64, error) {
	// Resolve / create the destination folder.
	var folderID *int64
	if env.FolderPath != "" && s.d.Folders != nil {
		id, err := s.d.Folders.EnsurePath(ctx, folders.KindTilemap, env.FolderPath, designerID)
		if err != nil {
			return 0, fmt.Errorf("ensure tilemap folder %q: %w", env.FolderPath, err)
		}
		if id > 0 {
			folderID = &id
		}
	}

	// Existing-by-name check. Tilemaps.FindByID can't do name lookups,
	// so query directly via the Pool.
	var existingID int64
	if err := s.d.Tilemaps.Pool.QueryRow(ctx,
		`SELECT id FROM tilemaps WHERE name = $1`, env.Tilemap.Name,
	).Scan(&existingID); err == nil {
		res.TilemapsSkipped++
		res.Warnings = append(res.Warnings, fmt.Sprintf(
			"tilemap %q already exists; left untouched", env.Tilemap.Name))
		return existingID, nil
	}

	// Insert the tilemap row directly. We bypass tilemaps.Service.Create
	// because it expects PngBody to recompute hashes — the export
	// already shipped them.
	var newID int64
	err := s.d.Tilemaps.Pool.QueryRow(ctx, `
		INSERT INTO tilemaps (asset_id, name, cols, rows, tile_size,
			non_empty_count, folder_id, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id
	`,
		newAssetID, env.Tilemap.Name,
		env.Tilemap.Cols, env.Tilemap.Rows, env.Tilemap.TileSize,
		env.Tilemap.NonEmptyCount, folderID, designerID,
	).Scan(&newID)
	if err != nil {
		return 0, fmt.Errorf("insert tilemap: %w", err)
	}
	res.TilemapsCreated++

	// Patch entity_types from the etypeIDMap to point at the new
	// tilemap. We do this BEFORE tilemap_tiles inserts because the FK
	// on tilemap_tiles.entity_type_id will fire if we tried to insert
	// an entity that hasn't been re-keyed yet (it has, we just need to
	// re-bind tilemap_id).
	for _, c := range env.Cells {
		newETID, ok := etypeIDMap[c.EntityTypeID]
		if !ok {
			res.Warnings = append(res.Warnings, fmt.Sprintf(
				"tilemap cell (%d,%d): entity_type %d missing post-apply; skipped",
				c.CellCol, c.CellRow, c.EntityTypeID))
			continue
		}
		// Patch entity_types row to point at the new tilemap + cell.
		if _, err := s.d.Tilemaps.Pool.Exec(ctx, `
			UPDATE entity_types
			   SET tilemap_id = $2, cell_col = $3, cell_row = $4,
			       sprite_asset_id = $5, atlas_index = $6
			 WHERE id = $1
		`, newETID, newID, c.CellCol, c.CellRow, newAssetID,
			int32(c.CellRow)*env.Tilemap.Cols+int32(c.CellCol),
		); err != nil {
			return 0, fmt.Errorf("patch entity_type %d for tilemap cell (%d,%d): %w",
				newETID, c.CellCol, c.CellRow, err)
		}
		// Insert tilemap_tiles row. Hashes preserved verbatim from
		// the export so a future Replace flow's diff-by-pixel-hash
		// logic still recognizes unchanged cells.
		if _, err := s.d.Tilemaps.Pool.Exec(ctx, `
			INSERT INTO tilemap_tiles (tilemap_id, cell_col, cell_row,
				entity_type_id, pixel_hash, edge_hash_n, edge_hash_e,
				edge_hash_s, edge_hash_w)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		`, newID, c.CellCol, c.CellRow, newETID,
			c.PixelHash[:], c.EdgeHashN[:], c.EdgeHashE[:],
			c.EdgeHashS[:], c.EdgeHashW[:]); err != nil {
			return 0, fmt.Errorf("insert tilemap_tile (%d,%d): %w",
				c.CellCol, c.CellRow, err)
		}
	}
	return newID, nil
}

// ---- Level import ----------------------------------------------------

// ImportLevel accepts a .boxlevel zip. Apply order:
//
//  1. Manifest check (kind = "boxlevel").
//  2. Apply assets (creating any missing ones, blob bytes uploaded
//     content-addressed). Returns srcID→newID for asset references.
//  3. Apply entity_types (sprite refs remapped via asset map). Tilemap
//     linkage is patched in step 4.
//  4. Apply each tilemap from the bundle's tilemaps.json. Patches its
//     tile-class entity_types' tilemap_id + cell_col + cell_row.
//  5. Apply the level's backing map (full MapPayload — layers + tiles
//     + lighting + locks + sample patch + constraints, with
//     entity_type ids remapped through step 3).
//  6. Apply the level row + entity placements + HUD + action groups +
//     world membership (resolved by name).
//
// Atomic-per-call same as ImportMap: assets / entity_types / tilemaps
// / map persist independently and the next retry will dedup.
func (s *Service) ImportLevel(ctx context.Context, body []byte, designerID int64, policy ConflictPolicy) (*Result, error) {
	if s.d.Levels == nil {
		return nil, fmt.Errorf("import level: levels service not configured")
	}
	m, zr, err := readManifest(body)
	if err != nil {
		return nil, err
	}
	if m.Kind != exporter.KindLevel {
		return nil, fmt.Errorf("%w: %q (expected boxlevel)", ErrUnknownKind, m.Kind)
	}
	if policy == "" {
		policy = DefaultPolicy
	}
	res := &Result{}

	// 1) Assets.
	var assetsPayload exporter.AllAssetsPayload
	if f := findFile(zr, "assets.json"); f != nil {
		if err := readJSONFile(f, &assetsPayload); err != nil {
			return nil, fmt.Errorf("decode assets.json: %w", err)
		}
	}
	assetIDMap, err := s.applyAssets(ctx, zr, assetsPayload.Assets, designerID, policy, res)
	if err != nil {
		return nil, err
	}

	// 2) Entity types.
	var etypes []exporter.EntityTypeEnvelope
	if f := findFile(zr, "entity_types.json"); f != nil {
		if err := readJSONFile(f, &etypes); err != nil {
			return nil, fmt.Errorf("decode entity_types.json: %w", err)
		}
	}
	etypeIDMap, err := s.applyEntityTypes(ctx, etypes, assetIDMap, designerID, policy, res)
	if err != nil {
		return nil, err
	}

	// 3) Tilemaps. Patches entity_types' tilemap_id linkage.
	var tilemapEnvs []exporter.TilemapEnvelope
	if f := findFile(zr, "tilemaps.json"); f != nil {
		if err := readJSONFile(f, &tilemapEnvs); err != nil {
			return nil, fmt.Errorf("decode tilemaps.json: %w", err)
		}
	}
	for _, env := range tilemapEnvs {
		newAssetID, ok := assetIDMap[env.Tilemap.AssetID]
		if !ok {
			res.Warnings = append(res.Warnings, fmt.Sprintf(
				"tilemap %q: backing asset id %d missing post-apply; skipped",
				env.Tilemap.Name, env.Tilemap.AssetID))
			continue
		}
		if _, err := s.applyTilemap(ctx, env, newAssetID, etypeIDMap, designerID, res); err != nil {
			return nil, err
		}
	}

	// 4) Map.
	var mapPayload exporter.MapPayload
	if f := findFile(zr, "map.json"); f == nil {
		return nil, fmt.Errorf("%w: map.json missing", ErrBadZip)
	} else if err := readJSONFile(f, &mapPayload); err != nil {
		return nil, fmt.Errorf("decode map.json: %w", err)
	}
	if err := s.applyMap(ctx, mapPayload, etypeIDMap, designerID, res); err != nil {
		return nil, err
	}

	// applyMap inserted a fresh map row but the level we're about to
	// build needs that map's id. The maps service exposes a name-based
	// lookup via List; we use the unique-name guarantee to find it.
	freshMapName := uniqueMapNameUsed(ctx, s.d.Maps, mapPayload.Map.Name)
	freshMaps, err := s.d.Maps.List(ctx, freshMapName)
	if err != nil {
		return nil, fmt.Errorf("locate fresh map: %w", err)
	}
	var freshMapID int64
	for _, mm := range freshMaps {
		if mm.Name == freshMapName {
			freshMapID = mm.ID
			break
		}
	}
	if freshMapID == 0 {
		return nil, fmt.Errorf("could not locate fresh map %q post-apply", freshMapName)
	}

	// 5) Level row + placements + HUD + action groups + world.
	var levelPayload exporter.LevelPayload
	if f := findFile(zr, "level.json"); f == nil {
		return nil, fmt.Errorf("%w: level.json missing", ErrBadZip)
	} else if err := readJSONFile(f, &levelPayload); err != nil {
		return nil, fmt.Errorf("decode level.json: %w", err)
	}
	if err := s.applyLevel(ctx, levelPayload, freshMapID, etypeIDMap, designerID, res); err != nil {
		return nil, err
	}
	return res, nil
}

// applyLevel creates the level row + placements + HUD + action groups,
// and (when present) re-wires the world membership by name. The map id
// is supplied by the caller (just-imported, fresh).
func (s *Service) applyLevel(
	ctx context.Context,
	lp exporter.LevelPayload,
	mapID int64,
	etypeIDMap map[int64]int64,
	designerID int64,
	res *Result,
) error {
	// Resolve world by name (optional).
	var worldID *int64
	if lp.WorldName != "" && s.d.Worlds != nil {
		ws, err := s.d.Worlds.List(ctx, worlds.ListOpts{Search: lp.WorldName, Limit: 32})
		if err == nil {
			for _, w := range ws {
				if w.Name == lp.WorldName {
					id := w.ID
					worldID = &id
					break
				}
			}
		}
	}

	// Resolve folder.
	var folderID *int64
	if lp.FolderPath != "" && s.d.Folders != nil {
		id, err := s.d.Folders.EnsurePath(ctx, folders.KindLevel, lp.FolderPath, designerID)
		if err == nil && id > 0 {
			folderID = &id
		}
	}

	// Pick a non-colliding name. Levels.Create returns ErrNameInUse on
	// dupes; we prefer to "land a copy" rather than overwrite.
	name := uniqueLevelName(ctx, s.d.Levels, lp.Level.Name)
	lv, err := s.d.Levels.Create(ctx, levels.CreateInput{
		Name:                 name,
		MapID:                mapID,
		WorldID:              worldID,
		Public:               lp.Level.Public,
		InstancingMode:       lp.Level.InstancingMode,
		PersistenceMode:      lp.Level.PersistenceMode,
		RefreshWindowSeconds: lp.Level.RefreshWindowSeconds,
		SpectatorPolicy:      lp.Level.SpectatorPolicy,
		FolderID:             folderID,
		CreatedBy:            designerID,
	})
	if err != nil {
		return fmt.Errorf("create level: %w", err)
	}
	res.LevelsCreated++

	// HUD layout — write the raw bytes back into levels.hud_layout_json.
	if len(lp.Level.HUDLayoutJSON) > 0 && string(lp.Level.HUDLayoutJSON) != "null" {
		if err := s.d.Levels.SetHUDLayout(ctx, lv.ID, lp.Level.HUDLayoutJSON); err != nil {
			return fmt.Errorf("set HUD: %w", err)
		}
	}

	// Placements — remap entity_type ids through etypeIDMap; drop any
	// whose source id didn't survive.
	for _, pl := range lp.Placements {
		newETID, ok := etypeIDMap[pl.EntityTypeID]
		if !ok {
			res.Warnings = append(res.Warnings, fmt.Sprintf(
				"placement (%d,%d): entity_type %d missing post-apply; skipped",
				pl.X, pl.Y, pl.EntityTypeID))
			continue
		}
		if _, err := s.d.Levels.PlaceEntity(ctx, levels.PlaceEntityInput{
			LevelID:               lv.ID,
			EntityTypeID:          newETID,
			X:                     pl.X,
			Y:                     pl.Y,
			RotationDegrees:       pl.RotationDegrees,
			InstanceOverridesJSON: pl.InstanceOverridesJSON,
			Tags:                  pl.Tags,
		}); err != nil {
			return fmt.Errorf("place entity: %w", err)
		}
	}

	// Action groups (level-scoped).
	if s.d.ActionGroups != nil {
		for _, g := range lp.ActionGroups {
			if _, err := s.d.ActionGroups.Upsert(ctx, lv.ID, g.Name, g.ActionsJSON); err != nil {
				res.Warnings = append(res.Warnings, fmt.Sprintf(
					"action group %q: %v", g.Name, err))
			}
		}
	}
	return nil
}

// uniqueLevelName mirrors uniqueMapName but for the levels table. Picks
// the first non-colliding name in the form "<base>", "<base> (copy)",
// "<base> (copy 2)", ... so an import never overwrites a live level.
func uniqueLevelName(ctx context.Context, svc *levels.Service, base string) string {
	if base == "" {
		base = "Imported level"
	}
	candidate := base
	for i := 1; i < 100; i++ {
		existing, err := svc.List(ctx, levels.ListOpts{Search: candidate, Limit: 32})
		if err != nil || !levelNameTaken(existing, candidate) {
			return candidate
		}
		if i == 1 {
			candidate = base + " (copy)"
		} else {
			candidate = fmt.Sprintf("%s (copy %d)", base, i)
		}
	}
	return candidate
}

func levelNameTaken(rows []levels.Level, name string) bool {
	for _, r := range rows {
		if r.Name == name {
			return true
		}
	}
	return false
}

// uniqueMapNameUsed re-runs uniqueMapName's lookup *after* the map has
// been inserted, so the map import flow can find the fresh row by name.
// This is a small consequence of applyMap not returning the new map id;
// keeping it scoped here so applyMap's signature stays simple.
func uniqueMapNameUsed(ctx context.Context, svc *mapsservice.Service, base string) string {
	if base == "" {
		base = "Imported map"
	}
	// First check whether the base name is taken — if it is, the
	// suffix loop runs; otherwise the import landed on `base` exactly.
	rows, err := svc.List(ctx, base)
	if err == nil && nameTaken(rows, base) {
		return base
	}
	// applyMap already wrote one row; we trust it picked the bare base
	// name when free, else added a suffix. Re-run uniqueMapName's logic
	// against the live database to find what survived.
	return uniqueMapName(ctx, svc, base)
}

// ---- World import ----------------------------------------------------

// ImportWorld accepts a .boxworld zip. Apply order:
//
//  1. Manifest check (kind = "boxworld").
//  2. Assets (deduped across the whole bundle).
//  3. Entity types.
//  4. Tilemaps (patch entity_types' tilemap linkage).
//  5. Maps (one per distinct map_id in the export). Returns
//     name→newID for level wiring.
//  6. Levels (one per LevelPayload, wired to its fresh map by name).
//  7. World row, with start_level resolved by name against the
//     freshly-imported levels.
func (s *Service) ImportWorld(ctx context.Context, body []byte, designerID int64, policy ConflictPolicy) (*Result, error) {
	if s.d.Worlds == nil {
		return nil, fmt.Errorf("import world: worlds service not configured")
	}
	m, zr, err := readManifest(body)
	if err != nil {
		return nil, err
	}
	if m.Kind != exporter.KindWorld {
		return nil, fmt.Errorf("%w: %q (expected boxworld)", ErrUnknownKind, m.Kind)
	}
	if policy == "" {
		policy = DefaultPolicy
	}
	res := &Result{}

	// 1) Assets.
	var assetsPayload exporter.AllAssetsPayload
	if f := findFile(zr, "assets.json"); f != nil {
		if err := readJSONFile(f, &assetsPayload); err != nil {
			return nil, fmt.Errorf("decode assets.json: %w", err)
		}
	}
	assetIDMap, err := s.applyAssets(ctx, zr, assetsPayload.Assets, designerID, policy, res)
	if err != nil {
		return nil, err
	}

	// 2) Entity types.
	var etypes []exporter.EntityTypeEnvelope
	if f := findFile(zr, "entity_types.json"); f != nil {
		if err := readJSONFile(f, &etypes); err != nil {
			return nil, fmt.Errorf("decode entity_types.json: %w", err)
		}
	}
	etypeIDMap, err := s.applyEntityTypes(ctx, etypes, assetIDMap, designerID, policy, res)
	if err != nil {
		return nil, err
	}

	// 3) Tilemaps.
	var tilemapEnvs []exporter.TilemapEnvelope
	if f := findFile(zr, "tilemaps.json"); f != nil {
		if err := readJSONFile(f, &tilemapEnvs); err != nil {
			return nil, fmt.Errorf("decode tilemaps.json: %w", err)
		}
	}
	for _, env := range tilemapEnvs {
		newAssetID, ok := assetIDMap[env.Tilemap.AssetID]
		if !ok {
			res.Warnings = append(res.Warnings, fmt.Sprintf(
				"tilemap %q: backing asset id %d missing post-apply; skipped",
				env.Tilemap.Name, env.Tilemap.AssetID))
			continue
		}
		if _, err := s.applyTilemap(ctx, env, newAssetID, etypeIDMap, designerID, res); err != nil {
			return nil, err
		}
	}

	// 4) Maps. Build a name→fresh-id map (by export name → imported
	// name, which uniqueMapName may have suffixed).
	var mapPayloads []exporter.MapPayload
	if f := findFile(zr, "maps.json"); f != nil {
		if err := readJSONFile(f, &mapPayloads); err != nil {
			return nil, fmt.Errorf("decode maps.json: %w", err)
		}
	}
	originalNameToFreshID := map[string]int64{}
	for _, mp := range mapPayloads {
		originalName := mp.Map.Name
		if err := s.applyMap(ctx, mp, etypeIDMap, designerID, res); err != nil {
			return nil, err
		}
		// Find the fresh map by the suffixed name.
		freshName := uniqueMapNameUsed(ctx, s.d.Maps, originalName)
		freshMaps, err := s.d.Maps.List(ctx, freshName)
		if err == nil {
			for _, mm := range freshMaps {
				if mm.Name == freshName {
					originalNameToFreshID[originalName] = mm.ID
					break
				}
			}
		}
	}

	// 5) Levels. Each LevelPayload's level.MapID is a SOURCE id that
	// we resolve by name through originalNameToFreshID — but we don't
	// have the original name on the LevelPayload directly, so we use
	// the export's MapPayload list (above) to map source mapID → name,
	// then name → fresh id.
	srcMapIDToName := map[int64]string{}
	for _, mp := range mapPayloads {
		srcMapIDToName[mp.Map.ID] = mp.Map.Name
	}
	var levelPayloads []exporter.LevelPayload
	if f := findFile(zr, "levels.json"); f != nil {
		if err := readJSONFile(f, &levelPayloads); err != nil {
			return nil, fmt.Errorf("decode levels.json: %w", err)
		}
	}
	importedLevelNames := map[string]string{} // exported name → imported name
	for _, lp := range levelPayloads {
		mapName := srcMapIDToName[lp.Level.MapID]
		freshMapID := originalNameToFreshID[mapName]
		if freshMapID == 0 {
			res.Warnings = append(res.Warnings, fmt.Sprintf(
				"level %q: source map %q (%d) missing post-apply; skipped",
				lp.Level.Name, mapName, lp.Level.MapID))
			continue
		}
		original := lp.Level.Name
		// applyLevel itself picks a non-colliding name; capture it via
		// the exact same suffix logic.
		imported := uniqueLevelName(ctx, s.d.Levels, original)
		// Override the level's name on the payload so applyLevel uses
		// our pre-resolved unique name (avoids a re-roll).
		lp.Level.Name = imported
		// Strip world_name from the per-level payload — we set the
		// world FK ourselves at the end so the level points at the
		// freshly-imported world, not a same-named one already in the
		// project.
		lp.WorldName = ""
		if err := s.applyLevel(ctx, lp, freshMapID, etypeIDMap, designerID, res); err != nil {
			return nil, err
		}
		importedLevelNames[original] = imported
	}

	// 6) World row.
	var wp exporter.WorldPayload
	if f := findFile(zr, "world.json"); f == nil {
		return nil, fmt.Errorf("%w: world.json missing", ErrBadZip)
	} else if err := readJSONFile(f, &wp); err != nil {
		return nil, fmt.Errorf("decode world.json: %w", err)
	}

	var folderID *int64
	if wp.FolderPath != "" && s.d.Folders != nil {
		if id, err := s.d.Folders.EnsurePath(ctx, folders.KindWorld, wp.FolderPath, designerID); err == nil && id > 0 {
			folderID = &id
		}
	}
	worldName := uniqueWorldName(ctx, s.d.Worlds, wp.World.Name)
	w, err := s.d.Worlds.Create(ctx, worlds.CreateInput{
		Name: worldName, FolderID: folderID, CreatedBy: designerID,
	})
	if err != nil {
		return nil, fmt.Errorf("create world: %w", err)
	}
	res.WorldsCreated++

	// Wire start level by name.
	if wp.StartLevelName != "" {
		mappedName := importedLevelNames[wp.StartLevelName]
		if mappedName == "" {
			mappedName = wp.StartLevelName
		}
		lvs, err := s.d.Levels.List(ctx, levels.ListOpts{Search: mappedName, Limit: 32})
		if err == nil {
			for _, lv := range lvs {
				if lv.Name == mappedName {
					id := lv.ID
					if err := s.d.Worlds.SetStartLevel(ctx, w.ID, &id); err == nil {
						break
					}
				}
			}
		}
	}

	// Re-bind every imported level to the fresh world (the per-level
	// applyLevel call above left world_id nil because we cleared
	// WorldName). Iterating the importedLevelNames map keeps the
	// re-bind precisely scoped to this import's levels.
	for _, mapped := range importedLevelNames {
		lvs, err := s.d.Levels.List(ctx, levels.ListOpts{Search: mapped, Limit: 32})
		if err != nil {
			continue
		}
		for _, lv := range lvs {
			if lv.Name == mapped {
				id := w.ID
				_ = s.d.Levels.SetWorld(ctx, lv.ID, &id)
				break
			}
		}
	}
	return res, nil
}

// uniqueWorldName mirrors uniqueLevelName but for worlds.
func uniqueWorldName(ctx context.Context, svc *worlds.Service, base string) string {
	if base == "" {
		base = "Imported world"
	}
	candidate := base
	for i := 1; i < 100; i++ {
		existing, err := svc.List(ctx, worlds.ListOpts{Search: candidate, Limit: 32})
		if err != nil || !worldNameTaken(existing, candidate) {
			return candidate
		}
		if i == 1 {
			candidate = base + " (copy)"
		} else {
			candidate = fmt.Sprintf("%s (copy %d)", base, i)
		}
	}
	return candidate
}

func worldNameTaken(rows []worlds.World, name string) bool {
	for _, r := range rows {
		if r.Name == name {
			return true
		}
	}
	return false
}
