package ws

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"boxland/server/internal/maps"
	"boxland/server/internal/proto"
	"boxland/server/internal/sim/runtime"
	"boxland/server/internal/sim/spatial"
)

// AuthoringDeps bundles what the designer authoring verbs need. One
// per-process instance shared across handlers.
//
// Also used by RegisterRuntimeVerbs (below) to give the JoinMap handler
// access to the InstanceManager so it can attach the connection's AOI
// Subscription to the right MapInstance grid.
type AuthoringDeps struct {
	MapsService *maps.Service
	Instances   *runtime.InstanceManager
}

// RegisterAuthoringVerbs wires the designer-only authoring opcodes
// (PlaceTiles / EraseTiles / PlaceLighting) onto the dispatcher AND
// replaces the stub JoinMap handler with one that creates a real AOI
// Subscription against the right MapInstance.
//
// All authoring opcodes ride the existing DesignerCommand verb so the
// realm-violation check the gateway applies to that verb covers them
// without bespoke per-opcode auth. PLAN.md §4j unified-broadcaster: the
// resulting chunk dirties get picked up by the next broadcaster tick
// and pushed to every subscriber on the map -- designers + players
// see the same Diff via one pipeline.
func RegisterAuthoringVerbs(d *Dispatcher, deps AuthoringDeps) {
	// Replace the stub designer-command handler from RegisterDefaultVerbs
	// with one that dispatches by inner opcode. Designer surfaces that
	// also want sandbox opcodes (spawn-any, freeze-tick, etc.) extend the
	// same handler in task #130.
	d.designerHandlers[proto.VerbDesignerCommand] = dispatchDesignerCommand(deps)

	// Replace the stub JoinMap handler with the runtime-aware one.
	d.playerHandlers[proto.VerbJoinMap] = handleJoinMapReal(deps)
}

// dispatchDesignerCommand decodes the outer DesignerCommandPayload and
// routes by opcode. Sub-payload decoding lives in per-opcode handlers.
func dispatchDesignerCommand(deps AuthoringDeps) VerbHandler {
	return func(ctx context.Context, conn *Connection, payload []byte) error {
		if len(payload) < 8 {
			return errors.New("designer_command: short payload")
		}
		dc := proto.GetRootAsDesignerCommandPayload(payload, 0)
		opcode := dc.Opcode()
		data := dc.DataBytes()

		switch opcode {
		case proto.DesignerOpcodePlaceTiles:
			return handlePlaceTiles(ctx, conn, deps, data)
		case proto.DesignerOpcodeEraseTiles:
			return handleEraseTiles(ctx, conn, deps, data)
		case proto.DesignerOpcodePlaceLighting:
			return handlePlaceLighting(ctx, conn, deps, data)

		// Sandbox runtime opcodes (spawn-any, freeze-tick, ...) land in
		// task #130; until then they're explicitly unhandled rather
		// than silently swallowed.
		case proto.DesignerOpcodeSpawnAny,
			proto.DesignerOpcodeSetResource,
			proto.DesignerOpcodeTakeControlEntity,
			proto.DesignerOpcodeReleaseControl,
			proto.DesignerOpcodeTeleport,
			proto.DesignerOpcodeFreezeTick,
			proto.DesignerOpcodeStepTick,
			proto.DesignerOpcodeGodmode:
			slog.Info("designer opcode reserved for sandbox wiring",
				"conn", conn.ID(), "opcode", opcode, "ref", "PLAN.md task #130")
			return nil
		default:
			return fmt.Errorf("designer_command: unknown opcode %d", opcode)
		}
	}
}

// ---- per-opcode handlers ----

func handlePlaceTiles(ctx context.Context, conn *Connection, deps AuthoringDeps, data []byte) error {
	if len(data) < 8 {
		return errors.New("place_tiles: short payload")
	}
	p := proto.GetRootAsPlaceTilesPayload(data, 0)
	mapID := p.MapId()

	// Decode the tile placements into the maps.Tile shape.
	var (
		minX, minY = int32(0x7fffffff), int32(0x7fffffff)
		maxX, maxY = int32(-0x80000000), int32(-0x80000000)
	)
	tiles := make([]maps.Tile, 0, p.TilesLength())
	for i := 0; i < p.TilesLength(); i++ {
		var tp proto.TilePlacement
		if !p.Tiles(&tp, i) {
			continue
		}
		tile := maps.Tile{
			MapID:        int64(mapID),
			LayerID:      int64(tp.LayerId()),
			X:            tp.X(),
			Y:            tp.Y(),
			EntityTypeID: int64(tp.EntityTypeId()),
		}
		if v := tp.AnimOverride(); v >= 0 {
			tile.AnimOverride = &v
		}
		if v := tp.CollisionShapeOverride(); v >= 0 {
			tile.CollisionShapeOverride = &v
		}
		if v := tp.CollisionMaskOverride(); v >= 0 {
			tile.CollisionMaskOverride = &v
		}
		tiles = append(tiles, tile)

		if tile.X < minX {
			minX = tile.X
		}
		if tile.Y < minY {
			minY = tile.Y
		}
		if tile.X > maxX {
			maxX = tile.X
		}
		if tile.Y > maxY {
			maxY = tile.Y
		}
	}
	if len(tiles) == 0 {
		return nil
	}

	if err := deps.MapsService.PlaceTiles(ctx, tiles); err != nil {
		return fmt.Errorf("place_tiles: persist: %w", err)
	}

	// Mark the affected chunks dirty on the live instance so the next
	// broadcaster tick pushes a Diff to every subscriber on this map.
	// We assume the canonical instance id "live:{map}:0" for now;
	// per-user / per-party instancing routes through a separate path
	// (sandbox) that we'll wire in task #131.
	mi := deps.Instances.Get(canonicalInstanceID(mapID))
	if mi != nil {
		mi.MarkChunksDirty(minX, minY, maxX, maxY)
	}
	slog.Info("place_tiles applied",
		"conn", conn.ID(), "map_id", mapID, "count", len(tiles))
	return nil
}

func handleEraseTiles(ctx context.Context, conn *Connection, deps AuthoringDeps, data []byte) error {
	if len(data) < 8 {
		return errors.New("erase_tiles: short payload")
	}
	p := proto.GetRootAsEraseTilesPayload(data, 0)
	mapID := p.MapId()

	// Erase requests can target multiple layers; we group by layer to
	// minimize the number of EraseTiles calls.
	type layerKey struct {
		layerID int64
	}
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
		k := layerKey{layerID: int64(pt.LayerId())}
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
	for k, points := range byLayer {
		if err := deps.MapsService.EraseTiles(ctx, int64(mapID), k.layerID, points); err != nil {
			return fmt.Errorf("erase_tiles layer %d: %w", k.layerID, err)
		}
	}
	if mi := deps.Instances.Get(canonicalInstanceID(mapID)); mi != nil && len(byLayer) > 0 {
		mi.MarkChunksDirty(minX, minY, maxX, maxY)
	}
	slog.Info("erase_tiles applied", "conn", conn.ID(), "map_id", mapID,
		"layers", len(byLayer))
	return nil
}

func handlePlaceLighting(ctx context.Context, conn *Connection, deps AuthoringDeps, data []byte) error {
	if len(data) < 8 {
		return errors.New("place_lighting: short payload")
	}
	p := proto.GetRootAsPlaceLightingPayload(data, 0)
	mapID := p.MapId()

	cells := make([]maps.LightingCell, 0, p.CellsLength())
	var (
		minX, minY = int32(0x7fffffff), int32(0x7fffffff)
		maxX, maxY = int32(-0x80000000), int32(-0x80000000)
	)
	for i := 0; i < p.CellsLength(); i++ {
		var lp proto.LightingCellPayload
		if !p.Cells(&lp, i) {
			continue
		}
		cells = append(cells, maps.LightingCell{
			MapID:     int64(mapID),
			LayerID:   int64(lp.LayerId()),
			X:         lp.X(),
			Y:         lp.Y(),
			Color:     int64(lp.Color()),
			Intensity: int16(lp.Intensity()),
		})
		if lp.X() < minX {
			minX = lp.X()
		}
		if lp.Y() < minY {
			minY = lp.Y()
		}
		if lp.X() > maxX {
			maxX = lp.X()
		}
		if lp.Y() > maxY {
			maxY = lp.Y()
		}
	}
	if len(cells) == 0 {
		return nil
	}
	if err := deps.MapsService.PlaceLightingCells(ctx, cells); err != nil {
		return fmt.Errorf("place_lighting: persist: %w", err)
	}
	if mi := deps.Instances.Get(canonicalInstanceID(mapID)); mi != nil {
		mi.MarkChunksDirty(minX, minY, maxX, maxY)
	}
	slog.Info("place_lighting applied", "conn", conn.ID(), "map_id", mapID,
		"count", len(cells))
	return nil
}

// handleJoinMapReal replaces the stub JoinMap handler. It:
//   1. Finds (or fails-out on missing) the named map.
//   2. Resolves the instance id -- canonical "live:{map}:0" for v1;
//      sandbox / per-user routing slots in here later.
//   3. Calls Instances.GetOrCreate so the MapInstance exists + recovered.
//   4. Builds an aoi.Subscription on the conn pointed at the map's
//      centre chunk with a sensible default radius.
//
// AFTER this handler runs, the broadcaster tick can deliver Diffs to
// the connection.
func handleJoinMapReal(deps AuthoringDeps) VerbHandler {
	return func(ctx context.Context, conn *Connection, payload []byte) error {
		if len(payload) < 8 {
			return errors.New("join_map: short payload")
		}
		jp := proto.GetRootAsJoinMapPayload(payload, 0)
		mapID := jp.MapId()

		m, err := deps.MapsService.FindByID(ctx, int64(mapID))
		if err != nil {
			return fmt.Errorf("join_map: %w", err)
		}

		instanceID := canonicalInstanceID(mapID)
		// instance_hint "sandbox:..." routes to that exact id; v1 only
		// uses it for designer Sandbox surfaces (PLAN.md §4j).
		if hint := string(jp.InstanceHint()); hint != "" {
			instanceID = hint
		}

		mi, err := deps.Instances.GetOrCreate(ctx, mapID, instanceID)
		if err != nil {
			return fmt.Errorf("join_map: get-or-create instance: %w", err)
		}

		// Centre the AOI window on the map's middle. Real spawn-point
		// routing (per-player save-slot, etc.) is a v1.x refinement.
		centreChunk := spatial.MakeChunkID(
			(m.Width/spatial.ChunkTiles)/2,
			(m.Height/spatial.ChunkTiles)/2,
		)

		policy := defaultPolicyForRealm(conn.Realm())
		conn.Subscription = newSubscriptionForConn(conn, policy, centreChunk)

		slog.Info("join_map subscribed",
			"conn", conn.ID(),
			"map_id", mapID,
			"instance_id", instanceID,
			"focus", centreChunk,
		)
		_ = mi
		return nil
	}
}

// canonicalInstanceID returns the live shared-instance id for a map.
// PLAN.md §1 instancing convention: "live:{map_id}:0" is the default
// shared instance. Per-user / per-party routing happens elsewhere.
func canonicalInstanceID(mapID uint32) string {
	return fmt.Sprintf("live:%d:0", mapID)
}
