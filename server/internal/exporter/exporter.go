// Package exporter assembles the self-contained .boxasset / .boxassets
// / .boxmap zip files produced by the design tools' "Export" buttons.
//
// Three entry points map 1:1 to the three UI surfaces:
//
//   - ExportAsset    — single asset (Asset Manager detail page)
//   - ExportAllAssets — every asset in the project (Asset Manager list)
//   - ExportMap       — one map + every entity_type + asset it touches,
//                       with blob bytes embedded (Mapmaker page)
//
// The companion `importer` package round-trips every file produced
// here. New fields land on the wire structs as additive json fields
// (zero-value on old files) — readers MUST tolerate unknown keys.
//
// File-format invariants (also enforced by the importer):
//
//   - manifest.json is always a member of the zip and lists `kind`,
//     `format_version`, `boxland_version`, `exported_at`, `exported_by`.
//   - kinds are stable strings: "boxasset", "boxassets", "boxmap".
//   - blob bytes live under `blobs/<content_addressed_path>` exactly
//     once, deduped by path. Re-importing a blob that already exists
//     is a no-op write because the path is content-addressed.
//   - JSON files are pretty-printed (2-space indent) so designers can
//     diff exports in source control.
//
// Performance notes:
//
//   - All multi-row fetches go through ListByIDs / per-map indexed
//     queries — no per-row N+1 loops.
//   - Blobs are streamed straight from object storage into the zip
//     writer; we never buffer the whole zip in memory beyond what the
//     stdlib zip writer holds.
package exporter

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"

	"boxland/server/internal/assets"
	"boxland/server/internal/entities"
	"boxland/server/internal/folders"
	mapsservice "boxland/server/internal/maps"
	"boxland/server/internal/persistence"
)

// FormatVersion is the wire-format major version stamped into every
// manifest. Bump when a backwards-incompatible change ships.
const FormatVersion = 1

// Manifest kinds.
const (
	KindAsset     = "boxasset"
	KindAllAssets = "boxassets"
	KindMap       = "boxmap"
)

// Manifest is the first member of every export zip. Readers must
// validate `Kind` + `FormatVersion` before trusting any other member.
type Manifest struct {
	Kind           string    `json:"kind"`
	FormatVersion  int       `json:"format_version"`
	BoxlandVersion string    `json:"boxland_version,omitempty"`
	ExportedAt     time.Time `json:"exported_at"`
	ExportedBy     int64     `json:"exported_by,omitempty"`
}

// AssetEnvelope is the wire payload for one asset (asset.json or one
// entry inside the all-assets bundle's assets.json). Carries the row
// itself plus its persisted animation tags so a re-import recreates
// both with one transaction.
//
// FolderPath is the slash-joined ancestor chain of the asset's folder
// at export time, e.g. "forest/trees". Empty means "lives in the kind
// root." The importer feeds this to folders.EnsurePath so the
// hierarchy is recreated automatically; old exports without this field
// import flat (zero-value default).
type AssetEnvelope struct {
	Asset      assets.Asset          `json:"asset"`
	Animations []assets.AnimationRow `json:"animations,omitempty"`
	FolderPath string                `json:"folder_path,omitempty"`
}

// AllAssetsPayload is the assets.json shape for the all-assets bundle.
type AllAssetsPayload struct {
	Assets []AssetEnvelope `json:"assets"`
}

// EntityTypeEnvelope is one entity_type plus its component rows.
// Animations live with the asset (not the type) so they aren't
// duplicated here; the importer reattaches via name lookup.
type EntityTypeEnvelope struct {
	EntityType entities.EntityType     `json:"entity_type"`
	Components []entities.ComponentRow `json:"components,omitempty"`
}

// MapPayload is map.json. Tile / lighting / lock rows carry the
// original entity_type ids; the importer remaps those during apply.
type MapPayload struct {
	Map           mapsservice.Map             `json:"map"`
	Layers        []mapsservice.Layer         `json:"layers"`
	Tiles         []mapsservice.Tile          `json:"tiles"`
	LightingCells []mapsservice.LightingCell  `json:"lighting_cells,omitempty"`
	LockedCells   []mapsservice.LockedCell    `json:"locked_cells,omitempty"`
	SamplePatch   *mapsservice.SamplePatch    `json:"sample_patch,omitempty"`
	Constraints   []mapsservice.MapConstraint `json:"constraints,omitempty"`
	HUDLayoutJSON json.RawMessage             `json:"hud_layout,omitempty"`
}

// MapBundle is what the .boxmap.zip carries (split across map.json,
// entity_types.json, assets.json, blobs/). Modeled as a single struct
// so callers can also render the same content in tests / debug dumps.
type MapBundle struct {
	Map         MapPayload
	EntityTypes []EntityTypeEnvelope
	Assets      []AssetEnvelope
}

// Deps bundles every service the exporter needs. Constructed once at
// boot in cmd/boxland; one instance shared across HTTP handlers (the
// methods are stateless).
type Deps struct {
	Assets      *assets.Service
	Entities    *entities.Service
	Folders     *folders.Service
	Maps        *mapsservice.Service
	ObjectStore *persistence.ObjectStore
	// BoxlandVersion is the running server build (e.g. git sha) and is
	// stamped into every manifest. Empty string is fine; the field is
	// informational.
	BoxlandVersion string
}

// Service is the public exporter facade.
type Service struct {
	d Deps
}

// New constructs a Service.
func New(d Deps) *Service { return &Service{d: d} }

// ---- Asset (single) ---------------------------------------------------

// ExportAsset returns one asset packaged as a .boxasset.zip.
//
// Layout:
//
//   manifest.json
//   asset.json     — AssetEnvelope (asset row + animations)
//   blobs/<path>   — raw PNG / WAV / OGG bytes
//
// Returns the zip bytes; the caller is responsible for setting
// Content-Disposition / Content-Type on the HTTP response.
func (s *Service) ExportAsset(ctx context.Context, id int64, designerID int64) ([]byte, error) {
	a, err := s.d.Assets.FindByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("export asset %d: %w", id, err)
	}
	anims, err := s.d.Assets.ListAnimations(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("export asset %d animations: %w", id, err)
	}
	path, err := s.folderPathFor(ctx, a.FolderID)
	if err != nil {
		return nil, fmt.Errorf("export asset %d folder path: %w", id, err)
	}
	env := AssetEnvelope{Asset: *a, Animations: anims, FolderPath: path}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	if err := writeManifest(zw, KindAsset, designerID, s.d.BoxlandVersion); err != nil {
		return nil, err
	}
	if err := writeJSONFile(zw, "asset.json", env); err != nil {
		return nil, err
	}
	if err := s.copyBlob(ctx, zw, a.ContentAddressedPath); err != nil {
		return nil, fmt.Errorf("copy blob %q: %w", a.ContentAddressedPath, err)
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ---- Assets (all) -----------------------------------------------------

// ExportAllAssets returns every asset packaged as a .boxassets.zip.
//
// Layout:
//
//   manifest.json
//   assets.json    — AllAssetsPayload (every asset + animation row)
//   blobs/<path>   — one entry per unique content_addressed_path
//
// One pass over the assets table; one batched fetch of every animation
// row keyed by asset_id (no N+1).
func (s *Service) ExportAllAssets(ctx context.Context, designerID int64) ([]byte, error) {
	all, err := s.d.Assets.List(ctx, assets.ListOpts{})
	if err != nil {
		return nil, fmt.Errorf("export all assets: list: %w", err)
	}

	ids := make([]int64, 0, len(all))
	for _, a := range all {
		ids = append(ids, a.ID)
	}
	animsByAsset, err := s.d.Assets.ListAnimationsByAssetIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("export all assets: animations: %w", err)
	}

	pathByFolder, err := s.folderPathsByID(ctx, distinctFolderIDs(all))
	if err != nil {
		return nil, fmt.Errorf("export all assets: folder paths: %w", err)
	}
	envs := make([]AssetEnvelope, 0, len(all))
	for _, a := range all {
		envs = append(envs, AssetEnvelope{
			Asset:      a,
			Animations: animsByAsset[a.ID],
			FolderPath: folderPathFromMap(pathByFolder, a.FolderID),
		})
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if err := writeManifest(zw, KindAllAssets, designerID, s.d.BoxlandVersion); err != nil {
		return nil, err
	}
	if err := writeJSONFile(zw, "assets.json", AllAssetsPayload{Assets: envs}); err != nil {
		return nil, err
	}
	// Dedup blobs by content path.
	seen := make(map[string]struct{}, len(all))
	for _, a := range all {
		if _, ok := seen[a.ContentAddressedPath]; ok {
			continue
		}
		seen[a.ContentAddressedPath] = struct{}{}
		if err := s.copyBlob(ctx, zw, a.ContentAddressedPath); err != nil {
			return nil, fmt.Errorf("copy blob %q: %w", a.ContentAddressedPath, err)
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ---- Map --------------------------------------------------------------

// ExportMap returns one map packaged as a .boxmap.zip.
//
// Layout:
//
//   manifest.json
//   map.json          — MapPayload (map row + layers + tiles + lighting +
//                       locks + sample patch + constraints + HUD)
//   entity_types.json — every entity_type the map's tiles or locks reference
//   assets.json       — every asset those entity_types reference (incl.
//                       animation rows)
//   blobs/<path>      — bytes for every referenced asset
//
// Self-contained: re-importing on a fresh database produces a working
// map without any pre-existing rows.
//
// Reference-walking is N+1-safe:
//   - one ChunkTiles call covers the whole map (single indexed query),
//   - one ListByIDs covers every referenced asset,
//   - one batched ListAnimationsByAssetIDs covers every animation row.
//
// (Per-entity_type FindByID + Components is unavoidable in v1: the
// entities service exposes those as primary keys; sets here are bounded
// by the count of *distinct* paintable types on the map, which is small
// in practice.)
func (s *Service) ExportMap(ctx context.Context, mapID int64, designerID int64) ([]byte, error) {
	m, err := s.d.Maps.FindByID(ctx, mapID)
	if err != nil {
		return nil, fmt.Errorf("export map %d: %w", mapID, err)
	}
	bundle, err := s.assembleMapBundle(ctx, m)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if err := writeManifest(zw, KindMap, designerID, s.d.BoxlandVersion); err != nil {
		return nil, err
	}
	if err := writeJSONFile(zw, "map.json", bundle.Map); err != nil {
		return nil, err
	}
	if err := writeJSONFile(zw, "entity_types.json", bundle.EntityTypes); err != nil {
		return nil, err
	}
	if err := writeJSONFile(zw, "assets.json", AllAssetsPayload{Assets: bundle.Assets}); err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(bundle.Assets))
	for _, ae := range bundle.Assets {
		p := ae.Asset.ContentAddressedPath
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		if err := s.copyBlob(ctx, zw, p); err != nil {
			return nil, fmt.Errorf("copy blob %q: %w", p, err)
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// assembleMapBundle pulls every row referenced by the map. Public-ish
// shape so importer tests can build the same struct off a different
// source if needed.
func (s *Service) assembleMapBundle(ctx context.Context, m *mapsservice.Map) (*MapBundle, error) {
	layers, err := s.d.Maps.Layers(ctx, m.ID)
	if err != nil {
		return nil, fmt.Errorf("layers: %w", err)
	}
	// One indexed query for every tile on this map.
	tiles, err := s.d.Maps.ChunkTiles(ctx, m.ID, 0, 0, m.Width-1, m.Height-1)
	if err != nil {
		return nil, fmt.Errorf("chunk tiles: %w", err)
	}
	locked, err := s.d.Maps.LockedCells(ctx, m.ID)
	if err != nil {
		return nil, fmt.Errorf("locked cells: %w", err)
	}
	lighting, err := s.fetchLightingCells(ctx, m.ID)
	if err != nil {
		return nil, fmt.Errorf("lighting cells: %w", err)
	}
	constraints, err := s.d.Maps.MapConstraints(ctx, m.ID)
	if err != nil {
		return nil, fmt.Errorf("constraints: %w", err)
	}
	patch, err := s.d.Maps.SamplePatchByMap(ctx, m.ID)
	if err != nil && err != mapsservice.ErrNoSamplePatch {
		return nil, fmt.Errorf("sample patch: %w", err)
	}
	hud, err := s.fetchHUDLayout(ctx, m.ID)
	if err != nil {
		return nil, fmt.Errorf("hud layout: %w", err)
	}

	mp := MapPayload{
		Map:           *m,
		Layers:        layers,
		Tiles:         tiles,
		LightingCells: lighting,
		LockedCells:   locked,
		SamplePatch:   patch,
		Constraints:   constraints,
		HUDLayoutJSON: hud,
	}

	// Collect distinct entity_type ids referenced by tiles + locks.
	etypeIDSet := make(map[int64]struct{}, len(tiles)+len(locked))
	for _, t := range tiles {
		etypeIDSet[t.EntityTypeID] = struct{}{}
	}
	for _, c := range locked {
		etypeIDSet[c.EntityTypeID] = struct{}{}
	}
	etypeIDs := mapKeysSorted(etypeIDSet)

	etypes, err := s.fetchEntityTypes(ctx, etypeIDs)
	if err != nil {
		return nil, err
	}

	// Distinct asset ids referenced by those entity_types.
	assetIDSet := make(map[int64]struct{}, len(etypes))
	for _, ee := range etypes {
		if ee.EntityType.SpriteAssetID != nil {
			assetIDSet[*ee.EntityType.SpriteAssetID] = struct{}{}
		}
	}
	assetIDs := mapKeysSorted(assetIDSet)

	assetEnvs, err := s.fetchAssetsWithAnimations(ctx, assetIDs)
	if err != nil {
		return nil, err
	}

	return &MapBundle{Map: mp, EntityTypes: etypes, Assets: assetEnvs}, nil
}

// fetchEntityTypes pulls each requested entity_type plus its components.
// Sets here are bounded by the count of *distinct paintable types* on
// the map (typically dozens, not thousands).
func (s *Service) fetchEntityTypes(ctx context.Context, ids []int64) ([]EntityTypeEnvelope, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	out := make([]EntityTypeEnvelope, 0, len(ids))
	for _, id := range ids {
		et, err := s.d.Entities.FindByID(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("entity_type %d: %w", id, err)
		}
		comps, err := s.d.Entities.Components(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("entity_type %d components: %w", id, err)
		}
		out = append(out, EntityTypeEnvelope{EntityType: *et, Components: comps})
	}
	return out, nil
}

// fetchAssetsWithAnimations is one ListByIDs + one batched
// ListAnimationsByAssetIDs + one batched folder-path lookup.
func (s *Service) fetchAssetsWithAnimations(ctx context.Context, ids []int64) ([]AssetEnvelope, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := s.d.Assets.ListByIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("assets list: %w", err)
	}
	animMap, err := s.d.Assets.ListAnimationsByAssetIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("animations list: %w", err)
	}
	pathByFolder, err := s.folderPathsByID(ctx, distinctFolderIDs(rows))
	if err != nil {
		return nil, fmt.Errorf("folder paths: %w", err)
	}
	out := make([]AssetEnvelope, 0, len(rows))
	for _, a := range rows {
		out = append(out, AssetEnvelope{
			Asset:      a,
			Animations: animMap[a.ID],
			FolderPath: folderPathFromMap(pathByFolder, a.FolderID),
		})
	}
	// Stable order: by id ASC. Helps deterministic round-trip tests
	// and produces nice diffs in source control.
	sort.Slice(out, func(i, j int) bool { return out[i].Asset.ID < out[j].Asset.ID })
	return out, nil
}

// folderPathFor resolves a single asset's folder pointer to a slash-
// joined path. Returns "" when the pointer is nil (kind-root asset)
// or the folders service is missing (test fixtures).
func (s *Service) folderPathFor(ctx context.Context, folderID *int64) (string, error) {
	if folderID == nil || *folderID == 0 || s.d.Folders == nil {
		return "", nil
	}
	return s.d.Folders.Path(ctx, *folderID)
}

// folderPathsByID is the bulk version: one CTE for every distinct
// non-nil folder id. Returns id→path. Missing folders simply absent
// from the map.
func (s *Service) folderPathsByID(ctx context.Context, ids []int64) (map[int64]string, error) {
	if len(ids) == 0 || s.d.Folders == nil {
		return nil, nil
	}
	return s.d.Folders.PathsByID(ctx, ids)
}

// distinctFolderIDs collects the set of non-nil folder_ids from a slice
// of assets so the exporter can batch the path lookup.
func distinctFolderIDs(rows []assets.Asset) []int64 {
	seen := make(map[int64]struct{}, len(rows))
	var out []int64
	for _, a := range rows {
		if a.FolderID != nil && *a.FolderID > 0 {
			if _, dup := seen[*a.FolderID]; dup {
				continue
			}
			seen[*a.FolderID] = struct{}{}
			out = append(out, *a.FolderID)
		}
	}
	return out
}

// folderPathFromMap is the dereference helper used after a bulk path
// lookup. Returns "" when the pointer is nil or absent from the map.
func folderPathFromMap(m map[int64]string, folderID *int64) string {
	if folderID == nil || *folderID == 0 || m == nil {
		return ""
	}
	return m[*folderID]
}

// fetchLightingCells reads every map_lighting_cells row for `mapID` in
// one query. Kept here (rather than in the maps service) because the
// runtime path is chunk-scoped; only the exporter needs a whole-map
// read.
func (s *Service) fetchLightingCells(ctx context.Context, mapID int64) ([]mapsservice.LightingCell, error) {
	rows, err := s.d.Maps.Pool.Query(ctx, `
		SELECT map_id, layer_id, x, y, color, intensity
		FROM map_lighting_cells
		WHERE map_id = $1
		ORDER BY layer_id, y, x
	`, mapID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []mapsservice.LightingCell
	for rows.Next() {
		var c mapsservice.LightingCell
		if err := rows.Scan(&c.MapID, &c.LayerID, &c.X, &c.Y, &c.Color, &c.Intensity); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// fetchHUDLayout reads the per-map HUD layout JSON. Returns the raw
// bytes (not decoded) because the importer simply writes it back —
// re-decoding would be lossy if the schema evolves between versions.
func (s *Service) fetchHUDLayout(ctx context.Context, mapID int64) (json.RawMessage, error) {
	var raw json.RawMessage
	err := s.d.Maps.Pool.QueryRow(ctx,
		`SELECT hud_layout_json FROM maps WHERE id = $1`, mapID,
	).Scan(&raw)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

// ---- helpers ----------------------------------------------------------

func writeManifest(zw *zip.Writer, kind string, designerID int64, version string) error {
	return writeJSONFile(zw, "manifest.json", Manifest{
		Kind:           kind,
		FormatVersion:  FormatVersion,
		BoxlandVersion: version,
		ExportedAt:     time.Now().UTC(),
		ExportedBy:     designerID,
	})
}

func writeJSONFile(zw *zip.Writer, name string, v any) error {
	w, err := zw.Create(name)
	if err != nil {
		return fmt.Errorf("create %s: %w", name, err)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("encode %s: %w", name, err)
	}
	return nil
}

// copyBlob streams a blob from object storage into the zip at
// blobs/<content_addressed_path>. The path is pre-namespaced
// (`assets/aa/bb/<sha256>`) which is fine — keeping the original key
// makes round-trip trivial: import re-uses the same key on Put().
func (s *Service) copyBlob(ctx context.Context, zw *zip.Writer, path string) error {
	if s.d.ObjectStore == nil {
		// Test fixtures may pass nil. Skip blobs in that mode; the
		// asset row alone is enough for round-trip in unit tests.
		return nil
	}
	rc, err := s.d.ObjectStore.Get(ctx, path)
	if err != nil {
		return err
	}
	defer rc.Close()
	w, err := zw.Create("blobs/" + path)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, rc); err != nil {
		return err
	}
	return nil
}

func mapKeysSorted(m map[int64]struct{}) []int64 {
	out := make([]int64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// FilenameFor returns a sane default download filename for the given
// kind + slug. Handlers use this so the browser saves a clearly-named
// file by default.
func FilenameFor(kind, slug string, now time.Time) string {
	switch kind {
	case KindAsset:
		return safeSlug(slug) + ".boxasset.zip"
	case KindAllAssets:
		return "boxland-assets-" + now.UTC().Format("2006-01-02") + ".boxassets.zip"
	case KindMap:
		return safeSlug(slug) + ".boxmap.zip"
	default:
		return safeSlug(slug) + ".zip"
	}
}

// safeSlug strips characters the OS file dialog would mangle. Conservative:
// alnum + dash + underscore; everything else collapses to '-'. Never
// returns the empty string (falls back to "export").
func safeSlug(s string) string {
	if s == "" {
		return "export"
	}
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
			out = append(out, c)
		case c == ' ':
			out = append(out, '-')
		default:
			// Replace anything weird (slashes, unicode, etc.) with '-'.
			if len(out) > 0 && out[len(out)-1] != '-' {
				out = append(out, '-')
			}
		}
	}
	// Trim leading + trailing dashes.
	for len(out) > 0 && out[len(out)-1] == '-' {
		out = out[:len(out)-1]
	}
	for len(out) > 0 && out[0] == '-' {
		out = out[1:]
	}
	if len(out) == 0 {
		return "export"
	}
	return string(out)
}
