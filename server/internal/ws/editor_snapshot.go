package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	flatbuffers "github.com/google/flatbuffers/go"

	"boxland/server/internal/levels"
	mapsservice "boxland/server/internal/maps"
	"boxland/server/internal/proto"
	"boxland/server/internal/sim/editor"
)

// editor_snapshot.go — FlatBuffers encoders for the EditorSnapshot
// + EditorDiff envelopes the WS gateway pushes to designers.
//
// Wire shape: see schemas/design.fbs. Snapshot is a one-shot
// envelope sent on EditorJoin; diffs flow continuously via the
// session's broadcast channel + the conn's per-conn pump.

// buildEditorSnapshot returns a serialized EditorSnapshot
// FlatBuffer for the conn's joined target. Theme + palette +
// surface-specific body all populated.
func buildEditorSnapshot(
	ctx context.Context,
	deps EditorAuthoringDeps,
	kind editor.Kind,
	targetID int64,
) ([]byte, error) {
	if deps.Sessions == nil {
		return nil, fmt.Errorf("snapshot: sessions manager required")
	}
	sess := deps.Sessions.GetOrCreate(editor.SessionKey{Kind: kind, TargetID: targetID})
	undoDepth, redoDepth := sess.HistoryDepths()

	b := flatbuffers.NewBuilder(4096)

	// Theme. Populated from the role -> entity_type binding the
	// editor uses to skin chrome widgets. Soft-fails: a missing
	// service or a broken row logs + ships an empty theme so the
	// snapshot still hands the client something usable.
	themeEntries, err := BuildEditorTheme(ctx, deps.Entities, deps.Assets)
	if err != nil {
		slog.Warn("snapshot: build theme", "err", err, "kind", kind, "target_id", targetID)
		themeEntries = nil
	}
	themeOffsets := make([]flatbuffers.UOffsetT, 0, len(themeEntries))
	for _, e := range themeEntries {
		themeOffsets = append(themeOffsets, encodeEditorTheme(b, e))
	}
	proto.EditorSnapshotStartThemeVector(b, len(themeOffsets))
	for i := len(themeOffsets) - 1; i >= 0; i-- {
		b.PrependUOffsetT(themeOffsets[i])
	}
	themeVec := b.EndVector(len(themeOffsets))

	// Palette. Per-kind class filter: mapmaker -> tiles only;
	// level editor -> npc/pc/logic. The bulk-loaded entries carry
	// sprite URLs + atlas grid info so the client can draw thumbs
	// + atlas-slice tiles directly from the snapshot.
	paletteRows := buildEditorPalette(ctx, deps.Entities, deps.Assets, classesForKind(kind))
	paletteOffsets := make([]flatbuffers.UOffsetT, 0, len(paletteRows))
	for _, e := range paletteRows {
		paletteOffsets = append(paletteOffsets, encodeEditorPalette(b, e))
	}
	proto.EditorSnapshotStartPaletteVector(b, len(paletteOffsets))
	for i := len(paletteOffsets) - 1; i >= 0; i-- {
		b.PrependUOffsetT(paletteOffsets[i])
	}
	paletteVec := b.EndVector(len(paletteOffsets))

	// Surface-specific body.
	var (
		mmBodyOff flatbuffers.UOffsetT
		leBodyOff flatbuffers.UOffsetT
	)
	switch kind {
	case editor.KindLevelEditor:
		body, err := buildLevelEditorBody(ctx, b, deps, targetID)
		if err != nil {
			return nil, err
		}
		leBodyOff = body
	case editor.KindMapmaker:
		body, err := buildMapmakerBody(ctx, b, deps, targetID)
		if err != nil {
			return nil, err
		}
		mmBodyOff = body
	}

	titleOff := b.CreateString(snapshotTitle(kind, targetID))

	proto.EditorSnapshotStart(b)
	proto.EditorSnapshotAddKind(b, snapshotKind(kind))
	proto.EditorSnapshotAddTitle(b, titleOff)
	proto.EditorSnapshotAddTheme(b, themeVec)
	proto.EditorSnapshotAddPalette(b, paletteVec)
	if mmBodyOff != 0 {
		proto.EditorSnapshotAddMapmakerBody(b, mmBodyOff)
	}
	if leBodyOff != 0 {
		proto.EditorSnapshotAddLevelEditorBody(b, leBodyOff)
	}
	proto.EditorSnapshotAddUndoDepth(b, undoDepth)
	proto.EditorSnapshotAddRedoDepth(b, redoDepth)
	root := proto.EditorSnapshotEnd(b)
	proto.FinishEditorSnapshotBuffer(b, root)
	return b.FinishedBytes(), nil
}

func snapshotKind(k editor.Kind) proto.EditorKind {
	switch k {
	case editor.KindMapmaker:
		return proto.EditorKindMapmaker
	case editor.KindLevelEditor:
		return proto.EditorKindLevelEditor
	}
	return proto.EditorKindMapmaker
}

func snapshotTitle(kind editor.Kind, targetID int64) string {
	switch kind {
	case editor.KindMapmaker:
		return fmt.Sprintf("Mapmaker · %d", targetID)
	case editor.KindLevelEditor:
		return fmt.Sprintf("Level · %d", targetID)
	}
	return "Editor"
}

func buildLevelEditorBody(ctx context.Context, b *flatbuffers.Builder, deps EditorAuthoringDeps, levelID int64) (flatbuffers.UOffsetT, error) {
	if deps.Levels == nil {
		return 0, fmt.Errorf("snapshot: Levels service required")
	}
	lv, err := deps.Levels.FindByID(ctx, levelID)
	if err != nil {
		return 0, fmt.Errorf("snapshot: find level: %w", err)
	}
	// Map dims for the editor's coordinate space.
	var mapWidth, mapHeight int32
	if deps.Maps != nil {
		if m, err := deps.Maps.FindByID(ctx, lv.MapID); err == nil && m != nil {
			mapWidth = m.Width
			mapHeight = m.Height
		}
	}
	placements, err := deps.Levels.ListEntities(ctx, levelID)
	if err != nil {
		return 0, fmt.Errorf("snapshot: list placements: %w", err)
	}
	placementOffsets := make([]flatbuffers.UOffsetT, 0, len(placements))
	for _, p := range placements {
		placementOffsets = append(placementOffsets, encodeLevelPlacement(b, p))
	}
	proto.EditorLevelEditorBodyStartPlacementsVector(b, len(placementOffsets))
	for i := len(placementOffsets) - 1; i >= 0; i-- {
		b.PrependUOffsetT(placementOffsets[i])
	}
	placementsVec := b.EndVector(len(placementOffsets))

	nameOff := b.CreateString(lv.Name)

	proto.EditorLevelEditorBodyStart(b)
	proto.EditorLevelEditorBodyAddLevelId(b, uint64(lv.ID))
	proto.EditorLevelEditorBodyAddLevelName(b, nameOff)
	proto.EditorLevelEditorBodyAddMapId(b, uint64(lv.MapID))
	proto.EditorLevelEditorBodyAddMapWidth(b, mapWidth)
	proto.EditorLevelEditorBodyAddMapHeight(b, mapHeight)
	proto.EditorLevelEditorBodyAddPlacements(b, placementsVec)
	return proto.EditorLevelEditorBodyEnd(b), nil
}

// buildMapmakerBody encodes the mapmaker's full editable surface
// (layers + tiles + locks). One ChunkTiles call covering the
// entire map ([0..w-1] × [0..h-1]) avoids N+1; locks are a
// separate small table the designer paints by hand. Soft-fails
// when the underlying service is missing — the snapshot still
// goes out with a partial body so the editor renders an empty
// canvas the designer can paint into.
func buildMapmakerBody(ctx context.Context, b *flatbuffers.Builder, deps EditorAuthoringDeps, mapID int64) (flatbuffers.UOffsetT, error) {
	if deps.Maps == nil {
		// No maps service -> empty body.
		proto.EditorMapmakerBodyStart(b)
		return proto.EditorMapmakerBodyEnd(b), nil
	}
	m, err := deps.Maps.FindByID(ctx, mapID)
	if err != nil {
		return 0, fmt.Errorf("snapshot: find map: %w", err)
	}

	// Layers.
	layers, err := deps.Maps.Layers(ctx, mapID)
	if err != nil {
		slog.Warn("snapshot: list layers", "err", err, "map_id", mapID)
		layers = nil
	}
	layerOffsets := make([]flatbuffers.UOffsetT, 0, len(layers))
	for _, l := range layers {
		layerOffsets = append(layerOffsets, encodeEditorMapLayer(b, l))
	}
	proto.EditorMapmakerBodyStartLayersVector(b, len(layerOffsets))
	for i := len(layerOffsets) - 1; i >= 0; i-- {
		b.PrependUOffsetT(layerOffsets[i])
	}
	layersVec := b.EndVector(len(layerOffsets))

	// Tiles. A single ChunkTiles call covers the whole map.
	tiles, err := deps.Maps.ChunkTiles(ctx, mapID, 0, 0, m.Width-1, m.Height-1)
	if err != nil {
		slog.Warn("snapshot: chunk tiles", "err", err, "map_id", mapID)
		tiles = nil
	}
	tileOffsets := make([]flatbuffers.UOffsetT, 0, len(tiles))
	for _, t := range tiles {
		tileOffsets = append(tileOffsets, encodeEditorMapTile(b, t.LayerID, t.X, t.Y, t.EntityTypeID, t.RotationDegrees))
	}
	proto.EditorMapmakerBodyStartTilesVector(b, len(tileOffsets))
	for i := len(tileOffsets) - 1; i >= 0; i-- {
		b.PrependUOffsetT(tileOffsets[i])
	}
	tilesVec := b.EndVector(len(tileOffsets))

	// Locks. Same shape as tiles.
	locks, err := deps.Maps.LockedCells(ctx, mapID)
	if err != nil {
		slog.Warn("snapshot: locked cells", "err", err, "map_id", mapID)
		locks = nil
	}
	lockOffsets := make([]flatbuffers.UOffsetT, 0, len(locks))
	for _, lc := range locks {
		lockOffsets = append(lockOffsets, encodeEditorMapTile(b, lc.LayerID, lc.X, lc.Y, lc.EntityTypeID, lc.RotationDegrees))
	}
	proto.EditorMapmakerBodyStartLocksVector(b, len(lockOffsets))
	for i := len(lockOffsets) - 1; i >= 0; i-- {
		b.PrependUOffsetT(lockOffsets[i])
	}
	locksVec := b.EndVector(len(lockOffsets))

	nameOff := b.CreateString(m.Name)

	proto.EditorMapmakerBodyStart(b)
	proto.EditorMapmakerBodyAddMapId(b, uint64(m.ID))
	proto.EditorMapmakerBodyAddMapName(b, nameOff)
	proto.EditorMapmakerBodyAddWidth(b, m.Width)
	proto.EditorMapmakerBodyAddHeight(b, m.Height)
	proto.EditorMapmakerBodyAddLayers(b, layersVec)
	proto.EditorMapmakerBodyAddTiles(b, tilesVec)
	proto.EditorMapmakerBodyAddLocks(b, locksVec)
	return proto.EditorMapmakerBodyEnd(b), nil
}

func encodeEditorMapLayer(b *flatbuffers.Builder, l mapsservice.Layer) flatbuffers.UOffsetT {
	nameOff := b.CreateString(l.Name)
	kindOff := b.CreateString(l.Kind)
	proto.EditorMapLayerStart(b)
	proto.EditorMapLayerAddLayerId(b, uint32(l.ID))
	proto.EditorMapLayerAddName(b, nameOff)
	proto.EditorMapLayerAddKind(b, kindOff)
	proto.EditorMapLayerAddOrd(b, l.Ord)
	proto.EditorMapLayerAddYSortEntities(b, l.YSortEntities)
	return proto.EditorMapLayerEnd(b)
}

func encodeEditorMapTile(b *flatbuffers.Builder, layerID int64, x, y int32, entityTypeID int64, rotationDegrees int16) flatbuffers.UOffsetT {
	proto.EditorMapTileStart(b)
	proto.EditorMapTileAddLayerId(b, uint32(layerID))
	proto.EditorMapTileAddX(b, x)
	proto.EditorMapTileAddY(b, y)
	proto.EditorMapTileAddEntityTypeId(b, uint64(entityTypeID))
	proto.EditorMapTileAddRotationDegrees(b, rotationDegrees)
	return proto.EditorMapTileEnd(b)
}

// encodeEditorTheme serializes one role binding into the snapshot's
// theme[] vector.
func encodeEditorTheme(b *flatbuffers.Builder, e EditorThemeEntry) flatbuffers.UOffsetT {
	roleOff := b.CreateString(e.Role)
	urlOff := b.CreateString(e.AssetURL)
	proto.EditorThemeEntryStart(b)
	proto.EditorThemeEntryAddRole(b, roleOff)
	proto.EditorThemeEntryAddEntityTypeId(b, uint64(e.EntityTypeID))
	proto.EditorThemeEntryAddAssetUrl(b, urlOff)
	proto.EditorThemeEntryAddNineSliceLeft(b, e.NineSlice.Left)
	proto.EditorThemeEntryAddNineSliceTop(b, e.NineSlice.Top)
	proto.EditorThemeEntryAddNineSliceRight(b, e.NineSlice.Right)
	proto.EditorThemeEntryAddNineSliceBottom(b, e.NineSlice.Bottom)
	proto.EditorThemeEntryAddWidth(b, e.Width)
	proto.EditorThemeEntryAddHeight(b, e.Height)
	return proto.EditorThemeEntryEnd(b)
}

// encodeEditorPalette serializes one palette row into the snapshot's
// palette[] vector.
func encodeEditorPalette(b *flatbuffers.Builder, e EditorPaletteEntry) flatbuffers.UOffsetT {
	nameOff := b.CreateString(e.Name)
	classOff := b.CreateString(e.Class)
	urlOff := b.CreateString(e.SpriteURL)
	proto.EditorPaletteEntryStart(b)
	proto.EditorPaletteEntryAddEntityTypeId(b, uint64(e.EntityTypeID))
	proto.EditorPaletteEntryAddName(b, nameOff)
	proto.EditorPaletteEntryAddClass(b, classOff)
	proto.EditorPaletteEntryAddSpriteUrl(b, urlOff)
	proto.EditorPaletteEntryAddAtlasIndex(b, e.AtlasIndex)
	proto.EditorPaletteEntryAddAtlasCols(b, e.AtlasCols)
	proto.EditorPaletteEntryAddTileSize(b, e.TileSize)
	proto.EditorPaletteEntryAddFolderId(b, e.FolderID)
	proto.EditorPaletteEntryAddProceduralInclude(b, e.ProceduralInclude)
	return proto.EditorPaletteEntryEnd(b)
}

func encodeLevelPlacement(b *flatbuffers.Builder, p levels.LevelEntity) flatbuffers.UOffsetT {
	overrides := canonicalizeJSON(p.InstanceOverridesJSON)
	overridesOff := b.CreateString(overrides)

	// Tags.
	tagOffsets := make([]flatbuffers.UOffsetT, 0, len(p.Tags))
	for _, t := range p.Tags {
		tagOffsets = append(tagOffsets, b.CreateString(t))
	}
	proto.EditorLevelPlacementStartTagsVector(b, len(tagOffsets))
	for i := len(tagOffsets) - 1; i >= 0; i-- {
		b.PrependUOffsetT(tagOffsets[i])
	}
	tagsVec := b.EndVector(len(tagOffsets))

	proto.EditorLevelPlacementStart(b)
	proto.EditorLevelPlacementAddPlacementId(b, uint64(p.ID))
	proto.EditorLevelPlacementAddEntityTypeId(b, uint64(p.EntityTypeID))
	proto.EditorLevelPlacementAddX(b, p.X)
	proto.EditorLevelPlacementAddY(b, p.Y)
	proto.EditorLevelPlacementAddRotationDegrees(b, p.RotationDegrees)
	proto.EditorLevelPlacementAddInstanceOverridesJson(b, overridesOff)
	proto.EditorLevelPlacementAddTags(b, tagsVec)
	return proto.EditorLevelPlacementEnd(b)
}

// canonicalizeJSON returns the JSON-canonical string form of a
// raw JSON message, falling back to "{}" for empty / invalid
// input. Keeps the wire shape compact and deterministic.
func canonicalizeJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return "{}"
	}
	out, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(out)
}

// encodeEditorDiff serializes one editor.Diff into an EditorDiff
// FlatBuffer envelope. The body is encoded as opaque bytes per the
// schema; clients decode by Kind.
func encodeEditorDiff(d editor.Diff) ([]byte, error) {
	bodyBytes, err := encodeEditorDiffBody(d)
	if err != nil {
		return nil, err
	}
	b := flatbuffers.NewBuilder(256)
	bodyVec := b.CreateByteVector(bodyBytes)
	proto.EditorDiffStart(b)
	proto.EditorDiffAddKind(b, diffKind(d.Kind))
	proto.EditorDiffAddBody(b, bodyVec)
	proto.EditorDiffAddUndoDepth(b, d.UndoDepth)
	proto.EditorDiffAddRedoDepth(b, d.RedoDepth)
	root := proto.EditorDiffEnd(b)
	b.FinishWithFileIdentifier(root, []byte(proto.EditorSnapshotIdentifier))
	return b.FinishedBytes(), nil
}

func diffKind(k editor.DiffKind) proto.EditorDiffKind {
	switch k {
	case editor.DiffTilePlaced:
		return proto.EditorDiffKindTilePlaced
	case editor.DiffTileErased:
		return proto.EditorDiffKindTileErased
	case editor.DiffLockAdded:
		return proto.EditorDiffKindLockAdded
	case editor.DiffLockRemoved:
		return proto.EditorDiffKindLockRemoved
	case editor.DiffPlacementAdded:
		return proto.EditorDiffKindPlacementAdded
	case editor.DiffPlacementMoved:
		return proto.EditorDiffKindPlacementMoved
	case editor.DiffPlacementRemoved:
		return proto.EditorDiffKindPlacementRemoved
	case editor.DiffOverridesChanged:
		return proto.EditorDiffKindOverridesChanged
	case editor.DiffHistoryChanged:
		return proto.EditorDiffKindHistoryChanged
	}
	return proto.EditorDiffKindNone
}

// encodeEditorDiffBody picks the right per-Kind sub-FlatBuffer.
// Returns its raw bytes (already finished) so the outer EditorDiff
// can wrap it. Empty body for Kind=HistoryChanged (the depths in
// the outer envelope carry the news).
func encodeEditorDiffBody(d editor.Diff) ([]byte, error) {
	switch d.Kind {
	case editor.DiffPlacementAdded:
		le, ok := d.Body.(*levels.LevelEntity)
		if !ok {
			return nil, fmt.Errorf("PlacementAdded body type %T", d.Body)
		}
		b := flatbuffers.NewBuilder(256)
		off := encodeLevelPlacement(b, *le)
		b.Finish(off)
		return b.FinishedBytes(), nil
	case editor.DiffPlacementMoved:
		le, ok := d.Body.(*levels.LevelEntity)
		if !ok {
			return nil, fmt.Errorf("PlacementMoved body type %T", d.Body)
		}
		b := flatbuffers.NewBuilder(64)
		proto.EditorLevelPlacementMoveStart(b)
		proto.EditorLevelPlacementMoveAddPlacementId(b, uint64(le.ID))
		proto.EditorLevelPlacementMoveAddX(b, le.X)
		proto.EditorLevelPlacementMoveAddY(b, le.Y)
		proto.EditorLevelPlacementMoveAddRotationDegrees(b, le.RotationDegrees)
		off := proto.EditorLevelPlacementMoveEnd(b)
		b.Finish(off)
		return b.FinishedBytes(), nil
	case editor.DiffPlacementRemoved:
		id, ok := d.Body.(int64)
		if !ok {
			return nil, fmt.Errorf("PlacementRemoved body type %T", d.Body)
		}
		b := flatbuffers.NewBuilder(32)
		proto.EditorPlacementIDStart(b)
		proto.EditorPlacementIDAddPlacementId(b, uint64(id))
		off := proto.EditorPlacementIDEnd(b)
		b.Finish(off)
		return b.FinishedBytes(), nil
	case editor.DiffOverridesChanged:
		body, ok := d.Body.(struct {
			PlacementID int64
			Overrides   json.RawMessage
		})
		if !ok {
			return nil, fmt.Errorf("OverridesChanged body type %T", d.Body)
		}
		b := flatbuffers.NewBuilder(128)
		ovOff := b.CreateString(canonicalizeJSON(body.Overrides))
		proto.EditorPlacementOverridesStart(b)
		proto.EditorPlacementOverridesAddPlacementId(b, uint64(body.PlacementID))
		proto.EditorPlacementOverridesAddInstanceOverridesJson(b, ovOff)
		off := proto.EditorPlacementOverridesEnd(b)
		b.Finish(off)
		return b.FinishedBytes(), nil
	case editor.DiffHistoryChanged:
		// Empty body; outer envelope's depth fields carry the news.
		return nil, nil

	case editor.DiffTilePlaced:
		t, ok := d.Body.(*mapsservice.Tile)
		if !ok {
			return nil, fmt.Errorf("TilePlaced body type %T", d.Body)
		}
		b := flatbuffers.NewBuilder(48)
		off := encodeEditorMapTile(b, t.LayerID, t.X, t.Y, t.EntityTypeID, t.RotationDegrees)
		b.Finish(off)
		return b.FinishedBytes(), nil
	case editor.DiffLockAdded:
		c, ok := d.Body.(*mapsservice.LockedCell)
		if !ok {
			return nil, fmt.Errorf("LockAdded body type %T", d.Body)
		}
		b := flatbuffers.NewBuilder(48)
		off := encodeEditorMapTile(b, c.LayerID, c.X, c.Y, c.EntityTypeID, c.RotationDegrees)
		b.Finish(off)
		return b.FinishedBytes(), nil
	case editor.DiffTileErased, editor.DiffLockRemoved:
		body, ok := d.Body.(editor.TileErasedBody)
		if !ok {
			return nil, fmt.Errorf("%v body type %T", d.Kind, d.Body)
		}
		b := flatbuffers.NewBuilder(32)
		proto.EditorMapTilePointStart(b)
		proto.EditorMapTilePointAddLayerId(b, uint32(body.LayerID))
		proto.EditorMapTilePointAddX(b, body.X)
		proto.EditorMapTilePointAddY(b, body.Y)
		off := proto.EditorMapTilePointEnd(b)
		b.Finish(off)
		return b.FinishedBytes(), nil
	}
	return nil, nil
}
