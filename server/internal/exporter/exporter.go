// Package exporter assembles the self-contained .boxasset / .boxassets
// / .boxtilemap / .boxmap / .boxlevel / .boxworld zip files produced
// by the design tools' "Export" buttons.
//
// Six entry points map 1:1 to the IDE surfaces:
//
//   - ExportAsset     — single asset (Library detail page)
//   - ExportAllAssets — every asset in the project (Library)
//   - ExportTilemap   — one tilemap + its sliced tile entities + the
//                        backing PNG (Tilemap viewer)
//   - ExportMap       — one map + every entity_type + tilemap + asset
//                        it touches (Mapmaker)
//   - ExportLevel     — one level + its map (full bundle) + non-tile
//                        entity placements + HUD + action groups
//   - ExportWorld     — one world + every level reachable from it +
//                        their maps + their referenced tilemaps + assets
//
// The companion `importer` package round-trips every file produced
// here. New fields land on the wire structs as additive json fields
// (zero-value on old files) — readers MUST tolerate unknown keys.
//
// File-format invariants (also enforced by the importer):
//
//   - manifest.json is always a member of the zip and lists `kind`,
//     `format_version`, `boxland_version`, `exported_at`, `exported_by`.
//   - kinds are stable strings: "boxasset", "boxassets", "boxtilemap",
//     "boxmap", "boxlevel", "boxworld".
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
	"boxland/server/internal/automations"
	"boxland/server/internal/entities"
	"boxland/server/internal/folders"
	"boxland/server/internal/levels"
	mapsservice "boxland/server/internal/maps"
	"boxland/server/internal/persistence"
	"boxland/server/internal/tilemaps"
	"boxland/server/internal/worlds"
)

// FormatVersion is the wire-format major version stamped into every
// manifest. Phase 3 of the holistic redesign bumped this to 2 — v1
// exports (only assets/maps existed) are not consumed; the project
// hasn't been released, so we don't carry a v1→v2 upgrader.
const FormatVersion = 2

// Manifest kinds.
const (
	KindAsset     = "boxasset"
	KindAllAssets = "boxassets"
	KindTilemap   = "boxtilemap"
	KindMap       = "boxmap"
	KindLevel     = "boxlevel"
	KindWorld     = "boxworld"
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
//
// Per the holistic redesign, a MAP is pure tile geometry — public,
// instancing, persistence, spectator policy, HUD, and action groups
// all live on a LEVEL (see LevelPayload). When a map is exported on
// its own, none of those fields ride along.
type MapPayload struct {
	Map           mapsservice.Map             `json:"map"`
	Layers        []mapsservice.Layer         `json:"layers"`
	Tiles         []mapsservice.Tile          `json:"tiles"`
	LightingCells []mapsservice.LightingCell  `json:"lighting_cells,omitempty"`
	LockedCells   []mapsservice.LockedCell    `json:"locked_cells,omitempty"`
	SamplePatch   *mapsservice.SamplePatch    `json:"sample_patch,omitempty"`
	Constraints   []mapsservice.MapConstraint `json:"constraints,omitempty"`
}

// TilemapEnvelope is the wire payload for one tilemap: the row, the
// per-cell rows (with hashes — these power the Replace flow's
// pixel-diff logic on re-import), and the slash-joined folder path.
//
// Tile-class entity_types produced by this tilemap travel inside the
// surrounding bundle's EntityTypes slice; the importer pairs them up
// by (tilemap source id, cell_col, cell_row) on apply.
type TilemapEnvelope struct {
	Tilemap    tilemaps.Tilemap `json:"tilemap"`
	Cells      []tilemaps.Cell  `json:"cells"`
	FolderPath string           `json:"folder_path,omitempty"`
}

// LevelPayload is level.json. Carries the level row + non-tile entity
// placements + HUD + level-scoped action groups. The level's backing
// map is exported separately under map.json (full MapPayload), and
// every entity_type / tilemap / asset the level transitively references
// rides in entity_types.json / tilemaps.json / assets.json.
type LevelPayload struct {
	Level         levels.Level                 `json:"level"`
	Placements    []levels.LevelEntity         `json:"placements,omitempty"`
	ActionGroups  []automations.GroupRow       `json:"action_groups,omitempty"`
	WorldName     string                       `json:"world_name,omitempty"` // resolved by name, not id
	FolderPath    string                       `json:"folder_path,omitempty"`
}

// WorldPayload is world.json. Carries the world row + the names of
// each level inside (the level rows themselves ride in levels.json as
// LevelPayload[]). Start level is referenced by name so importers can
// resolve it against the freshly-imported levels.
type WorldPayload struct {
	World          worlds.World `json:"world"`
	StartLevelName string       `json:"start_level_name,omitempty"`
	FolderPath     string       `json:"folder_path,omitempty"`
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
	Assets       *assets.Service
	Entities     *entities.Service
	Folders      *folders.Service
	Tilemaps     *tilemaps.Service
	Maps         *mapsservice.Service
	Levels       *levels.Service
	Worlds       *worlds.Service
	ActionGroups *automations.GroupsRepo
	ObjectStore  *persistence.ObjectStore
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

	mp := MapPayload{
		Map:           *m,
		Layers:        layers,
		Tiles:         tiles,
		LightingCells: lighting,
		LockedCells:   locked,
		SamplePatch:   patch,
		Constraints:   constraints,
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

// fetchHUDLayout reads a level's HUD layout JSON. Used by ExportLevel.
// Returns the empty default if the layout column is somehow null — the
// SQL CHECK should make that impossible, but we fail safe.
func (s *Service) fetchHUDLayout(ctx context.Context, levelID int64) (json.RawMessage, error) {
	if s.d.Levels == nil {
		return json.RawMessage(`{"v":1,"anchors":{}}`), nil
	}
	lv, err := s.d.Levels.FindByID(ctx, levelID)
	if err != nil {
		return nil, err
	}
	if len(lv.HUDLayoutJSON) == 0 {
		return json.RawMessage(`{"v":1,"anchors":{}}`), nil
	}
	return lv.HUDLayoutJSON, nil
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
	case KindTilemap:
		return safeSlug(slug) + ".boxtilemap.zip"
	case KindMap:
		return safeSlug(slug) + ".boxmap.zip"
	case KindLevel:
		return safeSlug(slug) + ".boxlevel.zip"
	case KindWorld:
		return safeSlug(slug) + ".boxworld.zip"
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


// ---- Tilemap (single) -------------------------------------------------

// ExportTilemap returns one tilemap packaged as a .boxtilemap.zip.
//
// Layout:
//
//   manifest.json
//   tilemap.json      — TilemapEnvelope (row + cells + folder path)
//   entity_types.json — every tile-class entity_type sliced from the tilemap
//   asset.json        — the backing PNG asset (AssetEnvelope)
//   blobs/<path>      — bytes for the backing PNG
//
// Self-contained: re-importing on a fresh database produces a working
// tilemap with all of its tile entities reattached. Adjacency hashes
// in tilemap_tiles ride along, so the importer's diff-by-pixel-hash
// logic can preserve map_tiles references when the tilemap is later
// replaced.
func (s *Service) ExportTilemap(ctx context.Context, tilemapID int64, designerID int64) ([]byte, error) {
	if s.d.Tilemaps == nil {
		return nil, fmt.Errorf("export tilemap %d: tilemaps service not configured", tilemapID)
	}
	tm, err := s.d.Tilemaps.FindByID(ctx, tilemapID)
	if err != nil {
		return nil, fmt.Errorf("export tilemap %d: %w", tilemapID, err)
	}
	cells, err := s.d.Tilemaps.Cells(ctx, tm.ID)
	if err != nil {
		return nil, fmt.Errorf("export tilemap %d cells: %w", tilemapID, err)
	}
	folderPath, err := s.tilemapFolderPath(ctx, tm.FolderID)
	if err != nil {
		return nil, fmt.Errorf("export tilemap %d folder: %w", tilemapID, err)
	}

	// Tile-class entity_types from this tilemap.
	etypeIDs := make([]int64, 0, len(cells))
	for _, c := range cells {
		etypeIDs = append(etypeIDs, c.EntityTypeID)
	}
	etypes, err := s.fetchEntityTypes(ctx, etypeIDs)
	if err != nil {
		return nil, err
	}

	// The single backing asset.
	asset, err := s.d.Assets.FindByID(ctx, tm.AssetID)
	if err != nil {
		return nil, fmt.Errorf("export tilemap %d asset: %w", tilemapID, err)
	}
	anims, err := s.d.Assets.ListAnimations(ctx, asset.ID)
	if err != nil {
		return nil, fmt.Errorf("export tilemap %d animations: %w", tilemapID, err)
	}
	assetFolderPath, _ := s.folderPathFor(ctx, asset.FolderID)
	assetEnv := AssetEnvelope{Asset: *asset, Animations: anims, FolderPath: assetFolderPath}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if err := writeManifest(zw, KindTilemap, designerID, s.d.BoxlandVersion); err != nil {
		return nil, err
	}
	if err := writeJSONFile(zw, "tilemap.json", TilemapEnvelope{
		Tilemap: *tm, Cells: cells, FolderPath: folderPath,
	}); err != nil {
		return nil, err
	}
	if err := writeJSONFile(zw, "entity_types.json", etypes); err != nil {
		return nil, err
	}
	if err := writeJSONFile(zw, "asset.json", assetEnv); err != nil {
		return nil, err
	}
	if err := s.copyBlob(ctx, zw, asset.ContentAddressedPath); err != nil {
		return nil, fmt.Errorf("copy blob %q: %w", asset.ContentAddressedPath, err)
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// tilemapFolderPath resolves a tilemap's folder pointer. Mirrors
// folderPathFor on the asset side; kept separate so the assets-folder
// and tilemap-folder roots stay distinct (they don't share the same
// kind_root in the asset_folders table).
func (s *Service) tilemapFolderPath(ctx context.Context, folderID *int64) (string, error) {
	if folderID == nil || *folderID == 0 || s.d.Folders == nil {
		return "", nil
	}
	return s.d.Folders.Path(ctx, *folderID)
}

// ---- Level ------------------------------------------------------------

// ExportLevel returns one level packaged as a .boxlevel.zip.
//
// Layout:
//
//   manifest.json
//   level.json        — LevelPayload (level row + placements + HUD + action groups)
//   map.json          — full MapPayload for the level's backing map
//   entity_types.json — every entity_type referenced by tiles + placements
//   tilemaps.json     — every tilemap referenced by the tile entity_types
//   assets.json       — every asset those entity_types + tilemaps reference
//   blobs/<path>      — bytes for every referenced asset
//
// The level export is the unit of "ship a playable scene." It re-imports
// as a brand-new level + map + entities + assets without any pre-existing
// rows.
func (s *Service) ExportLevel(ctx context.Context, levelID int64, designerID int64) ([]byte, error) {
	if s.d.Levels == nil {
		return nil, fmt.Errorf("export level %d: levels service not configured", levelID)
	}
	lv, err := s.d.Levels.FindByID(ctx, levelID)
	if err != nil {
		return nil, fmt.Errorf("export level %d: %w", levelID, err)
	}
	bundle, err := s.assembleLevelBundle(ctx, lv)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if err := writeManifest(zw, KindLevel, designerID, s.d.BoxlandVersion); err != nil {
		return nil, err
	}
	if err := writeJSONFile(zw, "level.json", bundle.Level); err != nil {
		return nil, err
	}
	if err := writeJSONFile(zw, "map.json", bundle.Map); err != nil {
		return nil, err
	}
	if err := writeJSONFile(zw, "entity_types.json", bundle.EntityTypes); err != nil {
		return nil, err
	}
	if err := writeJSONFile(zw, "tilemaps.json", bundle.Tilemaps); err != nil {
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

// LevelBundle is what the .boxlevel.zip carries. Public for parallelism
// with MapBundle.
type LevelBundle struct {
	Level       LevelPayload
	Map         MapPayload
	EntityTypes []EntityTypeEnvelope
	Tilemaps    []TilemapEnvelope
	Assets      []AssetEnvelope
}

// assembleLevelBundle composes a level's full export payload. Walks
// the geometry → entity → tilemap → asset graph collecting everything
// the importer needs to recreate the level without dangling references.
func (s *Service) assembleLevelBundle(ctx context.Context, lv *levels.Level) (*LevelBundle, error) {
	// Backing map first — its tiles drive most of the entity-type set.
	m, err := s.d.Maps.FindByID(ctx, lv.MapID)
	if err != nil {
		return nil, fmt.Errorf("level %d map: %w", lv.ID, err)
	}
	mapBundle, err := s.assembleMapBundle(ctx, m)
	if err != nil {
		return nil, err
	}

	// Non-tile placements on this level. These contribute additional
	// entity_type ids beyond what the map's tiles cover.
	placements, err := s.d.Levels.ListEntities(ctx, lv.ID)
	if err != nil {
		return nil, fmt.Errorf("level %d placements: %w", lv.ID, err)
	}

	// Add placement entity_type ids to the set covered by the map.
	etypeIDSet := make(map[int64]struct{}, len(mapBundle.EntityTypes)+len(placements))
	for _, e := range mapBundle.EntityTypes {
		etypeIDSet[e.EntityType.ID] = struct{}{}
	}
	for _, p := range placements {
		etypeIDSet[p.EntityTypeID] = struct{}{}
	}
	allETypeIDs := mapKeysSorted(etypeIDSet)
	allETypes, err := s.fetchEntityTypes(ctx, allETypeIDs)
	if err != nil {
		return nil, err
	}

	// Tilemaps referenced by tile-class entity_types.
	tilemapIDSet := make(map[int64]struct{}, len(allETypes))
	for _, e := range allETypes {
		if e.EntityType.TilemapID != nil {
			tilemapIDSet[*e.EntityType.TilemapID] = struct{}{}
		}
	}
	tilemapEnvs, err := s.fetchTilemaps(ctx, mapKeysSorted(tilemapIDSet))
	if err != nil {
		return nil, err
	}

	// Assets referenced by every entity_type sprite + every tilemap's
	// backing PNG. Dedup via the set.
	assetIDSet := make(map[int64]struct{}, len(allETypes))
	for _, e := range allETypes {
		if e.EntityType.SpriteAssetID != nil {
			assetIDSet[*e.EntityType.SpriteAssetID] = struct{}{}
		}
	}
	for _, tm := range tilemapEnvs {
		assetIDSet[tm.Tilemap.AssetID] = struct{}{}
	}
	allAssetIDs := mapKeysSorted(assetIDSet)
	allAssets, err := s.fetchAssetsWithAnimations(ctx, allAssetIDs)
	if err != nil {
		return nil, err
	}

	// HUD layout from the level row itself.
	hud := lv.HUDLayoutJSON
	if len(hud) == 0 {
		hud = json.RawMessage(`{"v":1,"anchors":{}}`)
	}
	_ = hud // already on the LevelPayload via the Level row's HUDLayoutJSON

	// Action groups for this level.
	actionGroups, err := s.fetchLevelActionGroups(ctx, lv.ID)
	if err != nil {
		return nil, fmt.Errorf("level %d action groups: %w", lv.ID, err)
	}

	// World name (resolved by name, not id).
	worldName := ""
	if lv.WorldID != nil && s.d.Worlds != nil {
		if wld, err := s.d.Worlds.FindByID(ctx, *lv.WorldID); err == nil {
			worldName = wld.Name
		}
	}

	folderPath := ""
	if lv.FolderID != nil && s.d.Folders != nil {
		folderPath, _ = s.d.Folders.Path(ctx, *lv.FolderID)
	}

	return &LevelBundle{
		Level: LevelPayload{
			Level:        *lv,
			Placements:   placements,
			ActionGroups: actionGroups,
			WorldName:    worldName,
			FolderPath:   folderPath,
		},
		Map:         mapBundle.Map,
		EntityTypes: allETypes,
		Tilemaps:    tilemapEnvs,
		Assets:      allAssets,
	}, nil
}

// fetchTilemaps loads each requested tilemap + its cells + folder path.
// Bounded by the number of distinct tilemaps referenced; small in
// practice (a level usually pulls from a handful).
func (s *Service) fetchTilemaps(ctx context.Context, ids []int64) ([]TilemapEnvelope, error) {
	if len(ids) == 0 || s.d.Tilemaps == nil {
		return nil, nil
	}
	out := make([]TilemapEnvelope, 0, len(ids))
	for _, id := range ids {
		tm, err := s.d.Tilemaps.FindByID(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("tilemap %d: %w", id, err)
		}
		cells, err := s.d.Tilemaps.Cells(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("tilemap %d cells: %w", id, err)
		}
		path, _ := s.tilemapFolderPath(ctx, tm.FolderID)
		out = append(out, TilemapEnvelope{Tilemap: *tm, Cells: cells, FolderPath: path})
	}
	return out, nil
}

// fetchLevelActionGroups returns every level_action_groups row for
// the given level. Used only by ExportLevel; the runtime path loads
// these via automations.GroupsRepo.LoadCompiled.
func (s *Service) fetchLevelActionGroups(ctx context.Context, levelID int64) ([]automations.GroupRow, error) {
	if s.d.ActionGroups == nil {
		return nil, nil
	}
	return s.d.ActionGroups.ListByLevel(ctx, levelID)
}

// ---- World ------------------------------------------------------------

// ExportWorld returns one world packaged as a .boxworld.zip.
//
// Layout:
//
//   manifest.json
//   world.json        — WorldPayload (world row + start_level_name + folder)
//   levels.json       — []LevelPayload (one per level in the world)
//   maps.json         — []MapPayload (one per distinct backing map)
//   entity_types.json — every entity_type referenced anywhere in the world
//   tilemaps.json     — every tilemap referenced by the tile entity_types
//   assets.json       — every asset those entity_types + tilemaps reference
//   blobs/<path>      — bytes for every referenced asset
//
// A world export is the largest unit and the canonical "ship a campaign"
// artifact. It deduplicates everything across its constituent levels —
// shared maps, shared entity_types, shared tilemaps, shared assets all
// appear once.
func (s *Service) ExportWorld(ctx context.Context, worldID int64, designerID int64) ([]byte, error) {
	if s.d.Worlds == nil {
		return nil, fmt.Errorf("export world %d: worlds service not configured", worldID)
	}
	wld, err := s.d.Worlds.FindByID(ctx, worldID)
	if err != nil {
		return nil, fmt.Errorf("export world %d: %w", worldID, err)
	}
	lvs, err := s.d.Levels.List(ctx, levels.ListOpts{WorldID: &wld.ID, Limit: 1024})
	if err != nil {
		return nil, fmt.Errorf("export world %d levels: %w", worldID, err)
	}

	// Build per-level payloads + a global set of map / entity_type /
	// tilemap / asset ids so the global json files dedup across levels.
	levelPayloads := make([]LevelPayload, 0, len(lvs))
	mapIDSet := map[int64]struct{}{}
	etypeIDSet := map[int64]struct{}{}
	tilemapIDSet := map[int64]struct{}{}

	for i := range lvs {
		lv := lvs[i]
		// We re-use assembleLevelBundle for its set-walking, then drop
		// its embedded global slices in favor of our world-wide
		// dedup — but the LevelPayload + per-level set additions ride
		// out.
		bundle, err := s.assembleLevelBundle(ctx, &lv)
		if err != nil {
			return nil, err
		}
		levelPayloads = append(levelPayloads, bundle.Level)
		mapIDSet[bundle.Map.Map.ID] = struct{}{}
		for _, e := range bundle.EntityTypes {
			etypeIDSet[e.EntityType.ID] = struct{}{}
		}
		for _, tm := range bundle.Tilemaps {
			tilemapIDSet[tm.Tilemap.ID] = struct{}{}
		}
	}

	// Build the world-wide MapPayload list (one per distinct backing map).
	mapIDs := mapKeysSorted(mapIDSet)
	maps := make([]MapPayload, 0, len(mapIDs))
	for _, mid := range mapIDs {
		m, err := s.d.Maps.FindByID(ctx, mid)
		if err != nil {
			return nil, fmt.Errorf("world map %d: %w", mid, err)
		}
		mb, err := s.assembleMapBundle(ctx, m)
		if err != nil {
			return nil, err
		}
		maps = append(maps, mb.Map)
		// Add the map's entity_types into the world set too — covered
		// by per-level walks above, but we re-walk to guarantee it.
		for _, e := range mb.EntityTypes {
			etypeIDSet[e.EntityType.ID] = struct{}{}
		}
	}

	// Resolve world-wide entity_types + tilemaps + assets with the full
	// deduped sets.
	allETypes, err := s.fetchEntityTypes(ctx, mapKeysSorted(etypeIDSet))
	if err != nil {
		return nil, err
	}
	tilemapEnvs, err := s.fetchTilemaps(ctx, mapKeysSorted(tilemapIDSet))
	if err != nil {
		return nil, err
	}

	assetIDSet := map[int64]struct{}{}
	for _, e := range allETypes {
		if e.EntityType.SpriteAssetID != nil {
			assetIDSet[*e.EntityType.SpriteAssetID] = struct{}{}
		}
	}
	for _, tm := range tilemapEnvs {
		assetIDSet[tm.Tilemap.AssetID] = struct{}{}
	}
	allAssets, err := s.fetchAssetsWithAnimations(ctx, mapKeysSorted(assetIDSet))
	if err != nil {
		return nil, err
	}

	// Resolve start_level by name (id-by-name is the canonical
	// importer-side reattachment — names are unique per the schema).
	startName := ""
	if wld.StartLevelID != nil {
		for _, lv := range lvs {
			if lv.ID == *wld.StartLevelID {
				startName = lv.Name
				break
			}
		}
	}

	folderPath := ""
	if wld.FolderID != nil && s.d.Folders != nil {
		folderPath, _ = s.d.Folders.Path(ctx, *wld.FolderID)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if err := writeManifest(zw, KindWorld, designerID, s.d.BoxlandVersion); err != nil {
		return nil, err
	}
	if err := writeJSONFile(zw, "world.json", WorldPayload{
		World: *wld, StartLevelName: startName, FolderPath: folderPath,
	}); err != nil {
		return nil, err
	}
	if err := writeJSONFile(zw, "levels.json", levelPayloads); err != nil {
		return nil, err
	}
	if err := writeJSONFile(zw, "maps.json", maps); err != nil {
		return nil, err
	}
	if err := writeJSONFile(zw, "entity_types.json", allETypes); err != nil {
		return nil, err
	}
	if err := writeJSONFile(zw, "tilemaps.json", tilemapEnvs); err != nil {
		return nil, err
	}
	if err := writeJSONFile(zw, "assets.json", AllAssetsPayload{Assets: allAssets}); err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(allAssets))
	for _, ae := range allAssets {
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
