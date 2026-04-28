package designer

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"boxland/server/internal/assets"
	"boxland/server/internal/entities"
)

// palette_helper.go — shared "build a palette of entity types with
// atlas info" routine.
//
// Three places in the design tool need the same data shape:
//
//   1. Mapmaker page — tile-class entity_types, server-rendered into
//      the palette tree.
//   2. Level editor entities tab — npc/pc/logic entity_types,
//      server-rendered into the placement palette.
//   3. JSON endpoints powering the Pixi-driven editor canvases:
//      `GET /design/maps/{id}/tile-types` and
//      `GET /design/levels/{id}/entity-types`.
//
// The N+1-safe pattern (one ListByClass + one ListByIDs for sprites)
// lives here as a single function so all three callers are identical
// and a future change to atlas metadata only touches one place.

// PaletteAtlasEntry is the wire-stable atlas row the editor canvases
// hand to a StaticAssetCatalog on the JS side. Fields match the
// templ-side data-bx-* attributes 1:1, so the editor JS can hydrate
// a catalog from either source with the same parsing code.
type PaletteAtlasEntry struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Class      string `json:"class"`
	SpriteURL  string `json:"sprite_url"`
	AtlasIndex int32  `json:"atlas_index"`
	AtlasCols  int32  `json:"atlas_cols"`
	TileSize   int32  `json:"tile_size"`
	FolderID   *int64 `json:"folder_id,omitempty"`
	// ProceduralInclude only matters for tile-class entries; the
	// procedural-mode WFC engine reads it as a hint about which tiles
	// to consider. Non-tile classes always serialize false.
	ProceduralInclude bool `json:"procedural_include"`
}

// BuildPaletteAtlas returns one entry per entity_type in `classes`,
// with sprite-asset URLs + atlas grid info bulk-loaded.
//
// Performance: at most 4 ListByClass round-trips (the four enum
// classes — bounded constant) + exactly one ListByIDs for every
// referenced sprite asset, regardless of entity_type count. Failures
// in a single class log + skip that class so the rest of the palette
// still renders.
func BuildPaletteAtlas(
	ctx context.Context,
	d Deps,
	classes []entities.Class,
) []PaletteAtlasEntry {
	if d.Entities == nil {
		return nil
	}
	var ets []entities.EntityType
	for _, c := range classes {
		got, err := d.Entities.ListByClass(ctx, c, entities.ListOpts{Limit: 4096})
		if err != nil {
			slog.Warn("palette ListByClass", "err", err, "class", c)
			continue
		}
		ets = append(ets, got...)
	}

	// Distinct sprite asset ids, single bulk load.
	assetIDs := make([]int64, 0, len(ets))
	seen := make(map[int64]struct{}, len(ets))
	for _, et := range ets {
		if et.SpriteAssetID == nil {
			continue
		}
		if _, ok := seen[*et.SpriteAssetID]; ok {
			continue
		}
		seen[*et.SpriteAssetID] = struct{}{}
		assetIDs = append(assetIDs, *et.SpriteAssetID)
	}
	assetByID := make(map[int64]assets.Asset, len(assetIDs))
	if d.Assets != nil && len(assetIDs) > 0 {
		if rows, err := d.Assets.ListByIDs(ctx, assetIDs); err == nil {
			for _, a := range rows {
				assetByID[a.ID] = a
			}
		} else {
			slog.Warn("palette assets bulk lookup", "err", err)
		}
	}
	urlFn := assetPublicURLFunc(mapsValues(assetByID))

	out := make([]PaletteAtlasEntry, 0, len(ets))
	for _, et := range ets {
		out = append(out, makePaletteEntry(et, assetByID, urlFn))
	}
	return out
}

// makePaletteEntry materializes one entity_type into the wire-stable
// palette row. Centralized so BuildPaletteAtlas + BuildMapTileAtlas
// produce byte-identical output for the same input row.
func makePaletteEntry(
	et entities.EntityType,
	assetByID map[int64]assets.Asset,
	urlFn func(string) string,
) PaletteAtlasEntry {
	entry := PaletteAtlasEntry{
		ID:                et.ID,
		Name:              et.Name,
		Class:             string(et.EntityClass),
		AtlasIndex:        et.AtlasIndex,
		AtlasCols:         1,
		TileSize:          assets.TileSize,
		ProceduralInclude: et.ProceduralInclude,
	}
	if et.SpriteAssetID != nil {
		if a, ok := assetByID[*et.SpriteAssetID]; ok {
			entry.SpriteURL = urlFn(a.ContentAddressedPath)
			if md, derr := assets.DecodeTileSheetMetadata(a.MetadataJSON); derr == nil && md.Cols > 0 {
				entry.AtlasCols = int32(md.Cols)
				entry.TileSize = int32(md.TileSize)
			}
			if a.FolderID != nil {
				fid := *a.FolderID
				entry.FolderID = &fid
			}
		}
	}
	return entry
}

// BuildMapTileAtlas returns palette entries restricted to entity_types
// actually placed on the map's tiles. Used by the level editor's
// backdrop layer: rendering the map geometry under the placement
// canvas needs atlas info for *those* specific tile types, not the
// full project-wide tile catalog (which can be hundreds of unused
// types in larger projects).
//
// Implementation: list every distinct entity_type_id from the map's
// tiles via a single SQL DISTINCT, then bulk-load those entity_types
// + their sprite assets. One DB round-trip per layer (DISTINCT) +
// one bulk entity_type fetch + one bulk asset fetch.
func BuildMapTileAtlas(ctx context.Context, d Deps, mapID int64) ([]PaletteAtlasEntry, error) {
	if d.Maps == nil || d.Entities == nil {
		return nil, nil
	}
	m, err := d.Maps.FindByID(ctx, mapID)
	if err != nil {
		return nil, err
	}
	tiles, err := d.Maps.ChunkTiles(ctx, mapID, 0, 0, m.Width-1, m.Height-1)
	if err != nil {
		return nil, fmt.Errorf("read tiles for tile-types: %w", err)
	}

	// Distinct entity_type_ids referenced by the map.
	distinct := make(map[int64]struct{}, 64)
	for _, t := range tiles {
		distinct[t.EntityTypeID] = struct{}{}
	}
	if len(distinct) == 0 {
		return nil, nil
	}

	// We don't have an entities.ListByIDs helper in scope here; the
	// existing pattern is to FindByID per id. For a map with N
	// distinct tile types this is N round-trips — fine for v0
	// (typical maps reuse 10-50 tile types, well within budget) and
	// flagged as a follow-up below. The asset lookup below remains
	// bulk so the typical case is 1-2 DB round-trips total.
	ets := make([]entities.EntityType, 0, len(distinct))
	for id := range distinct {
		et, err := d.Entities.FindByID(ctx, id)
		if err != nil {
			slog.Warn("palette tile-type FindByID", "err", err, "entity_type_id", id)
			continue
		}
		ets = append(ets, *et)
	}

	// Assets bulk-load.
	assetIDs := make([]int64, 0, len(ets))
	seen := make(map[int64]struct{}, len(ets))
	for _, et := range ets {
		if et.SpriteAssetID == nil {
			continue
		}
		if _, ok := seen[*et.SpriteAssetID]; ok {
			continue
		}
		seen[*et.SpriteAssetID] = struct{}{}
		assetIDs = append(assetIDs, *et.SpriteAssetID)
	}
	assetByID := make(map[int64]assets.Asset, len(assetIDs))
	if d.Assets != nil && len(assetIDs) > 0 {
		if rows, err := d.Assets.ListByIDs(ctx, assetIDs); err == nil {
			for _, a := range rows {
				assetByID[a.ID] = a
			}
		}
	}
	urlFn := assetPublicURLFunc(mapsValues(assetByID))

	out := make([]PaletteAtlasEntry, 0, len(ets))
	for _, et := range ets {
		out = append(out, makePaletteEntry(et, assetByID, urlFn))
	}
	return out, nil
}

// writePaletteJSON is a tiny helper that wraps a slice in the canonical
// envelope the editor JS expects. Kept as a function rather than
// inlined in each handler so a wire-format tweak (e.g. adding a
// version field) lands in one place.
func writePaletteJSON(w jsonResponseWriter, entries []PaletteAtlasEntry) error {
	if entries == nil {
		entries = []PaletteAtlasEntry{}
	}
	return json.NewEncoder(w).Encode(map[string]any{
		"entries": entries,
	})
}

// jsonResponseWriter is the minimal interface palette JSON encoders
// need. Used so test helpers can pass a *bytes.Buffer or a recorder
// without dragging in the full http.ResponseWriter contract.
type jsonResponseWriter interface {
	Write([]byte) (int, error)
}
