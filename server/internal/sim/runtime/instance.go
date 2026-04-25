// Package runtime is the live game-instance manager. One MapInstance per
// (map_id, instance_id) owns:
//
//   * an ECS World
//   * a system Scheduler running the canonical pipeline
//   * a spatial Grid (per-chunk version vectors for AOI)
//   * a Persister (Postgres + Redis Streams WAL)
//   * the loaded chunk set (so re-loads are idempotent)
//
// PLAN.md §1 "Tiles ARE entities" + "Same engine code for sandbox and live":
// sandbox instances and live instances are identical at runtime; isolation
// is via the instance id namespace, not branching code.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/rueidis"

	"boxland/server/internal/maps"
	"boxland/server/internal/sim"
	"boxland/server/internal/sim/ecs"
	"boxland/server/internal/sim/persist"
	"boxland/server/internal/sim/spatial"
)

// MapInstance is one live runtime for (mapID, instanceID).
type MapInstance struct {
	MapID      uint32
	InstanceID string

	World       *ecs.World
	Scheduler   *sim.Scheduler
	Grid        *spatial.Grid
	WAL         *persist.WAL
	Persister   *persist.Persister
	MapsService *maps.Service

	// loadedChunks tracks chunk loads so duplicate joins don't
	// double-spawn the same tile entities.
	loadedMu     sync.Mutex
	loadedChunks map[spatial.ChunkID]struct{}
}

// NewMapInstance constructs an empty instance and runs recovery from
// (map_state + WAL). The caller is responsible for calling LoadChunk
// for any chunks players are about to look at.
func NewMapInstance(
	ctx context.Context,
	pool *pgxpool.Pool,
	redis rueidis.Client,
	mapsService *maps.Service,
	mapID uint32,
	instanceID string,
) (*MapInstance, error) {
	world := ecs.NewWorld()
	wal := persist.NewWAL(redis, mapID, instanceID)
	persister := persist.NewPersister(pool, wal, mapID, instanceID)

	mi := &MapInstance{
		MapID:        mapID,
		InstanceID:   instanceID,
		World:        world,
		Scheduler:    sim.NewScheduler(world),
		Grid:         spatial.New(),
		WAL:          wal,
		Persister:    persister,
		MapsService:  mapsService,
		loadedChunks: make(map[spatial.ChunkID]struct{}),
	}

	// Best-effort recovery: a fresh map has no snapshot, which is fine.
	_, err := persist.Recover(ctx, pool, wal, mapID, instanceID, world, nil)
	if err != nil && !errors.Is(err, persist.ErrNoSnapshot) {
		return nil, fmt.Errorf("recover %d/%s: %w", mapID, instanceID, err)
	}
	return mi, nil
}

// LoadChunk materializes the tiles in the named chunk into the world,
// idempotently. Returns the (possibly cached) load result.
func (mi *MapInstance) LoadChunk(ctx context.Context, lookup maps.EntityTypeLookup, chunk spatial.ChunkID) (maps.LoadResult, error) {
	mi.loadedMu.Lock()
	if _, ok := mi.loadedChunks[chunk]; ok {
		mi.loadedMu.Unlock()
		return maps.LoadResult{}, nil
	}
	mi.loadedMu.Unlock()

	cx, cy := chunk.Coords()
	x0 := cx * spatial.ChunkTiles
	y0 := cy * spatial.ChunkTiles
	x1 := x0 + spatial.ChunkTiles - 1
	y1 := y0 + spatial.ChunkTiles - 1

	res, err := mi.MapsService.LoadChunk(ctx, mi.World, lookup, int64(mi.MapID), x0, y0, x1, y1)
	if err != nil {
		return maps.LoadResult{}, fmt.Errorf("load chunk: %w", err)
	}

	// Bump the chunk's grid version so any subscriber whose AOI covers
	// it sees it as dirty next broadcaster tick. Tile entities are
	// Static and never participate in the moving-entity grid, so a bare
	// version bump is the right primitive (vs. adding sentinel entities).
	mi.Grid.BumpVersion(chunk)

	mi.loadedMu.Lock()
	mi.loadedChunks[chunk] = struct{}{}
	mi.loadedMu.Unlock()
	_ = res
	return res, nil
}

// MarkChunksDirty bumps the version vector for every chunk overlapping
// the given tile-grid region. Called by tile/lighting authoring opcodes
// after a successful database mutation so the next broadcast tick picks
// up the change. Tiles are Static and never enter the spatial grid via
// Add(); BumpVersion is the authoritative "this chunk changed" signal.
func (mi *MapInstance) MarkChunksDirty(tileX0, tileY0, tileX1, tileY1 int32) {
	cx0 := floorDivInt32(tileX0, spatial.ChunkTiles)
	cy0 := floorDivInt32(tileY0, spatial.ChunkTiles)
	cx1 := floorDivInt32(tileX1, spatial.ChunkTiles)
	cy1 := floorDivInt32(tileY1, spatial.ChunkTiles)
	for cy := cy0; cy <= cy1; cy++ {
		for cx := cx0; cx <= cx1; cx++ {
			mi.Grid.BumpVersion(spatial.MakeChunkID(cx, cy))
		}
	}
}

// floorDivInt32 mirrors spatial.floorDiv (unexported there). Inlined
// here so the runtime package doesn't need to re-export it from spatial.
func floorDivInt32(a, b int32) int32 {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}
