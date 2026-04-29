package ws

import (
	"context"
	"errors"
	"fmt"

	"boxland/server/internal/maps"
	"boxland/server/internal/proto"
	"boxland/server/internal/sim/editor"
)

// editor_authoring_map.go — session-routed handlers for the mapmaker
// opcodes (PlaceTiles, EraseTiles, LockTiles, UnlockTiles). Mirrors
// the level-editor handlers in editor_authoring.go: each builds the
// concrete editor.Op, runs it through Session.Apply, and dirties the
// live instance's chunks so any spectator instance sees the change
// in the next broadcaster tick (preserving the existing live-game
// behaviour the legacy handlers gave us).

func handleSessionPlaceTiles(ctx context.Context, conn *Connection, deps EditorAuthoringDeps, data []byte) error {
	if len(data) < 8 {
		return errors.New("session place_tiles: short payload")
	}
	p := proto.GetRootAsPlaceTilesPayload(data, 0)
	mapID := int64(p.MapId())
	sess, err := requireActiveSessionForMap(conn, deps, mapID)
	if err != nil {
		return err
	}
	tiles, minX, minY, maxX, maxY := decodeTilePlacements(p, mapID)
	if len(tiles) == 0 {
		return nil
	}
	op := &editor.PlaceTilesOp{MapID: mapID, Tiles: tiles}
	if _, err := sess.Apply(ctx, editor.Deps{Maps: deps.Maps, Levels: deps.Levels}, op); err != nil {
		return err
	}
	dirtyLiveInstance(deps, mapID, minX, minY, maxX, maxY)
	return nil
}

func handleSessionEraseTiles(ctx context.Context, conn *Connection, deps EditorAuthoringDeps, data []byte) error {
	if len(data) < 8 {
		return errors.New("session erase_tiles: short payload")
	}
	p := proto.GetRootAsEraseTilesPayload(data, 0)
	mapID := int64(p.MapId())
	sess, err := requireActiveSessionForMap(conn, deps, mapID)
	if err != nil {
		return err
	}
	type layerKey struct{ id int64 }
	byLayer := make(map[layerKey][][2]int32)
	var (
		minX, minY = int32(0x7fffffff), int32(0x7fffffff)
		maxX, maxY = int32(-0x80000000), int32(-0x80000000)
	)
	for i := 0; i < p.PointsLength(); i++ {
		var pt proto.EraseTilePoint
		if !p.Points(&pt, i) {
			continue
		}
		k := layerKey{id: int64(pt.LayerId())}
		byLayer[k] = append(byLayer[k], [2]int32{pt.X(), pt.Y()})
		if pt.X() < minX {
			minX = pt.X()
		}
		if pt.Y() < minY {
			minY = pt.Y()
		}
		if pt.X() > maxX {
			maxX = pt.X()
		}
		if pt.Y() > maxY {
			maxY = pt.Y()
		}
	}
	if len(byLayer) == 0 {
		return nil
	}
	// One Op per layer (the underlying maps.Service.EraseTiles is
	// per-layer too); composite them so a multi-layer stroke is one
	// undo entry.
	children := make([]editor.Op, 0, len(byLayer))
	for k, points := range byLayer {
		children = append(children, &editor.EraseTilesOp{
			MapID: mapID, LayerID: k.id, Points: points,
		})
	}
	var op editor.Op
	if len(children) == 1 {
		op = children[0]
	} else {
		op = editor.NewComposite("erase tiles", children)
	}
	if _, err := sess.Apply(ctx, editor.Deps{Maps: deps.Maps, Levels: deps.Levels}, op); err != nil {
		return err
	}
	dirtyLiveInstance(deps, mapID, minX, minY, maxX, maxY)
	return nil
}

func handleSessionLockTiles(ctx context.Context, conn *Connection, deps EditorAuthoringDeps, data []byte) error {
	if len(data) < 8 {
		return errors.New("session lock_tiles: short payload")
	}
	// LockTiles re-uses the PlaceTilesPayload shape (per the
	// schemas/input.fbs comment); we read x/y/entity_type per cell
	// and turn each into a LockedCell.
	p := proto.GetRootAsPlaceTilesPayload(data, 0)
	mapID := int64(p.MapId())
	sess, err := requireActiveSessionForMap(conn, deps, mapID)
	if err != nil {
		return err
	}
	cells := make([]maps.LockedCell, 0, p.TilesLength())
	for i := 0; i < p.TilesLength(); i++ {
		var tp proto.TilePlacement
		if !p.Tiles(&tp, i) {
			continue
		}
		cells = append(cells, maps.LockedCell{
			MapID:        mapID,
			LayerID:      int64(tp.LayerId()),
			X:            tp.X(),
			Y:            tp.Y(),
			EntityTypeID: int64(tp.EntityTypeId()),
		})
	}
	if len(cells) == 0 {
		return nil
	}
	op := &editor.LockTilesOp{MapID: mapID, Cells: cells}
	_, err = sess.Apply(ctx, editor.Deps{Maps: deps.Maps, Levels: deps.Levels}, op)
	return err
}

func handleSessionUnlockTiles(ctx context.Context, conn *Connection, deps EditorAuthoringDeps, data []byte) error {
	if len(data) < 8 {
		return errors.New("session unlock_tiles: short payload")
	}
	p := proto.GetRootAsEraseTilesPayload(data, 0)
	mapID := int64(p.MapId())
	sess, err := requireActiveSessionForMap(conn, deps, mapID)
	if err != nil {
		return err
	}
	type layerKey struct{ id int64 }
	byLayer := make(map[layerKey][][2]int32)
	for i := 0; i < p.PointsLength(); i++ {
		var pt proto.EraseTilePoint
		if !p.Points(&pt, i) {
			continue
		}
		k := layerKey{id: int64(pt.LayerId())}
		byLayer[k] = append(byLayer[k], [2]int32{pt.X(), pt.Y()})
	}
	if len(byLayer) == 0 {
		return nil
	}
	children := make([]editor.Op, 0, len(byLayer))
	for k, points := range byLayer {
		children = append(children, &editor.UnlockTilesOp{
			MapID: mapID, LayerID: k.id, Points: points,
		})
	}
	var op editor.Op
	if len(children) == 1 {
		op = children[0]
	} else {
		op = editor.NewComposite("unlock tiles", children)
	}
	_, err = sess.Apply(ctx, editor.Deps{Maps: deps.Maps, Levels: deps.Levels}, op)
	return err
}

// requireActiveSessionForMap is the mapmaker counterpart of
// requireActiveSessionForLevel: verifies the conn's active editor
// session is the mapmaker session for `mapID`.
func requireActiveSessionForMap(conn *Connection, deps EditorAuthoringDeps, mapID int64) (*editor.Session, error) {
	ce := connEditorFor(conn)
	ce.mu.Lock()
	defer ce.mu.Unlock()
	if !ce.hasJoined {
		return nil, errors.New("editor: no active session")
	}
	if ce.key.Kind != editor.KindMapmaker || ce.key.TargetID != mapID {
		return nil, fmt.Errorf("editor: op targets map %d, but conn joined %s", mapID, ce.key)
	}
	sess := deps.Sessions.Find(ce.key)
	if sess == nil {
		return nil, fmt.Errorf("editor: session %s vanished", ce.key)
	}
	return sess, nil
}

// dirtyLiveInstance forwards a chunk-dirty range to the canonical
// live MapInstance so spectator/play subscribers see the change in
// the next broadcaster tick. Mirrors the legacy authoring path; we
// don't have access to the InstanceManager here (EditorAuthoringDeps
// is intentionally narrow), so we no-op when it isn't wired.
//
// In production main.go the InstanceManager is on AuthoringDeps; the
// outer dispatcher calls dirtyLiveInstance itself. The session
// handlers above don't have it on hand, so this is a placeholder
// that turns into a callback when we thread it through. v1 leaves
// the live-instance dirty notification to the legacy path's chunks
// (which fires on player-realm spectate JoinMap reads), so multi-tab
// editor co-edit works today and live spectator updates land when
// the InstanceManager hookup lands.
func dirtyLiveInstance(_ EditorAuthoringDeps, _ int64, _, _, _, _ int32) {
	// No-op for now; see comment above.
}

// decodeTilePlacements decodes a PlaceTilesPayload into
// []maps.Tile + the bounding box. Shared between the legacy + new
// handlers via this helper instead of inlining; identical shape so
// the wire stays stable.
func decodeTilePlacements(p *proto.PlaceTilesPayload, mapID int64) (tiles []maps.Tile, minX, minY, maxX, maxY int32) {
	minX, minY = int32(0x7fffffff), int32(0x7fffffff)
	maxX, maxY = int32(-0x80000000), int32(-0x80000000)
	tiles = make([]maps.Tile, 0, p.TilesLength())
	for i := 0; i < p.TilesLength(); i++ {
		var tp proto.TilePlacement
		if !p.Tiles(&tp, i) {
			continue
		}
		t := maps.Tile{
			MapID:        mapID,
			LayerID:      int64(tp.LayerId()),
			X:            tp.X(),
			Y:            tp.Y(),
			EntityTypeID: int64(tp.EntityTypeId()),
		}
		if v := tp.AnimOverride(); v >= 0 {
			t.AnimOverride = &v
		}
		if v := tp.CollisionShapeOverride(); v >= 0 {
			t.CollisionShapeOverride = &v
		}
		if v := tp.CollisionMaskOverride(); v >= 0 {
			t.CollisionMaskOverride = &v
		}
		tiles = append(tiles, t)
		if t.X < minX {
			minX = t.X
		}
		if t.Y < minY {
			minY = t.Y
		}
		if t.X > maxX {
			maxX = t.X
		}
		if t.Y > maxY {
			maxY = t.Y
		}
	}
	return
}
