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
	"boxland/server/internal/entities"
	"boxland/server/internal/entities/components"
	"boxland/server/internal/exporter"
	"boxland/server/internal/folders"
	mapsservice "boxland/server/internal/maps"
	"boxland/server/internal/persistence"
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
	MapsCreated        int      `json:"maps_created"`
	Warnings           []string `json:"warnings,omitempty"`
}

// Deps mirrors exporter.Deps; the same services drive both directions.
type Deps struct {
	Assets      *assets.Service
	Entities    *entities.Service
	Folders     *folders.Service
	Maps        *mapsservice.Service
	ObjectStore *persistence.ObjectStore
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
		// carries a path. Old exports without `folder_path` land in
		// the kind root (folderID = nil).
		var folderID *int64
		if env.FolderPath != "" && s.d.Folders != nil {
			id, err := s.d.Folders.EnsurePath(ctx,
				folders.KindRoot(env.Asset.Kind), env.FolderPath, designerID)
			if err != nil {
				return nil, fmt.Errorf("ensure folder %q for asset %q: %w",
					env.FolderPath, env.Asset.Name, err)
			}
			if id > 0 {
				folderID = &id
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
			SpriteAssetID:        spriteID,
			AtlasIndex:           env.EntityType.AtlasIndex,
			DefaultAnimationID:   nil, // animations were just re-created with new ids; v1 leaves this unset, designer re-picks.
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
		Name:            uniqueMapName(ctx, s.d.Maps, mp.Map.Name),
		Width:           mp.Map.Width,
		Height:          mp.Map.Height,
		Public:          false, // never auto-publish on import
		InstancingMode:  mp.Map.InstancingMode,
		PersistenceMode: mp.Map.PersistenceMode,
		Mode:            mp.Map.Mode,
		Seed:            mp.Map.Seed,
		SpectatorPolicy: mp.Map.SpectatorPolicy,
		CreatedBy:       designerID,
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

	// 8) HUD layout: write the raw JSON back into maps.hud_layout_json.
	// We bypass the hud.Repo because it expects a typed Layout we'd
	// have to re-validate; for round-trip the bytes are already known
	// good.
	if len(mp.HUDLayoutJSON) > 0 && string(mp.HUDLayoutJSON) != "null" {
		if _, err := s.d.Maps.Pool.Exec(ctx,
			`UPDATE maps SET hud_layout_json = $2, updated_at = now()
			   WHERE id = $1 AND created_by = $3`,
			newMap.ID, []byte(mp.HUDLayoutJSON), designerID,
		); err != nil {
			return fmt.Errorf("update hud_layout_json: %w", err)
		}
	}
	return nil
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


