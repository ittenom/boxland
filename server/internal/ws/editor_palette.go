package ws

import (
	"context"
	"log/slog"
	"strconv"

	"boxland/server/internal/assets"
	"boxland/server/internal/entities"
	"boxland/server/internal/sim/editor"
)

// editor_palette.go — server-side palette assembler for the editor
// snapshot. Mirrors `designer.BuildPaletteAtlas` shape so the
// snapshot's `palette[]` vector carries the same data the legacy
// templ + JSON catalog endpoint shipped, just routed through the
// FlatBuffers wire instead.
//
// Why a parallel copy: the `designer` package depends on `ws` (HTTP
// handlers register WS-aware routes), so importing `designer` from
// `ws` would create an import cycle. The function is small + the
// shape is wire-stable, so we duplicate it here. Any future change
// to atlas metadata needs to land on both copies; flagged with the
// matching comment in `designer/palette_helper.go`.

// EditorPaletteEntry is the wire shape one palette row takes when
// emitted from the editor snapshot. Fields match the FlatBuffers
// `EditorPaletteEntry` table 1:1.
type EditorPaletteEntry struct {
	EntityTypeID      int64
	Name              string
	Class             string // "tile" | "npc" | "pc" | "logic" | "ui"
	SpriteURL         string
	AtlasIndex        int32
	AtlasCols         int32
	TileSize          int32
	FolderID          int64
	ProceduralInclude bool
}

// buildEditorPalette returns one entry per entity_type in `classes`,
// with sprite-asset URLs + atlas grid info bulk-loaded.
//
// N+1-safe: at most len(classes) ListByClass round-trips + exactly
// one ListByIDs for every distinct sprite asset, regardless of
// entity_type count. Failures in a single class log + skip it so
// the rest of the palette still ships.
//
// Returns an empty slice (never nil) so the snapshot encoder can
// safely range over it.
func buildEditorPalette(
	ctx context.Context,
	es *entities.Service,
	as *assets.Service,
	classes []entities.Class,
) []EditorPaletteEntry {
	if es == nil || len(classes) == 0 {
		return []EditorPaletteEntry{}
	}
	var ets []entities.EntityType
	for _, c := range classes {
		got, err := es.ListByClass(ctx, c, entities.ListOpts{Limit: 4096})
		if err != nil {
			slog.Warn("editor_palette ListByClass", "err", err, "class", c)
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
	if as != nil && len(assetIDs) > 0 {
		if rows, err := as.ListByIDs(ctx, assetIDs); err == nil {
			for _, a := range rows {
				assetByID[a.ID] = a
			}
		} else {
			slog.Warn("editor_palette assets bulk lookup", "err", err)
		}
	}

	out := make([]EditorPaletteEntry, 0, len(ets))
	for _, et := range ets {
		entry := EditorPaletteEntry{
			EntityTypeID:      et.ID,
			Name:              et.Name,
			Class:             string(et.EntityClass),
			AtlasIndex:        et.AtlasIndex,
			AtlasCols:         1,
			TileSize:          assets.TileSize,
			ProceduralInclude: et.ProceduralInclude,
		}
		if et.SpriteAssetID != nil {
			if a, ok := assetByID[*et.SpriteAssetID]; ok {
				entry.SpriteURL = "/design/assets/blob/" + strconv.FormatInt(a.ID, 10)
				if md, derr := assets.DecodeTileSheetMetadata(a.MetadataJSON); derr == nil && md.Cols > 0 {
					entry.AtlasCols = int32(md.Cols)
					entry.TileSize = int32(md.TileSize)
				}
				if a.FolderID != nil {
					entry.FolderID = *a.FolderID
				}
			}
		}
		out = append(out, entry)
	}
	return out
}

// classesForKind picks the entity classes the surface's palette
// should include. Mapmaker = tiles only; level editor = the three
// "live actor" classes.
func classesForKind(k editor.Kind) []entities.Class {
	switch k {
	case editor.KindMapmaker:
		return []entities.Class{entities.ClassTile}
	case editor.KindLevelEditor:
		return []entities.Class{entities.ClassNPC, entities.ClassPC, entities.ClassLogic}
	}
	return nil
}
