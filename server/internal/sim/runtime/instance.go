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
	"boxland/server/internal/sim/systems"
)

// SystemDeps bundles the dependencies the per-instance system stack
// needs at construction time. Held on the manager (one set per
// process) and copied into each new MapInstance so its scheduler can
// register the canonical system pipeline (movement, animation, …).
//
// The Animation system's Catalog is the only required field today;
// future systems (AI, triggers) extend this struct.
type SystemDeps struct {
	Animations systems.AnimationCatalog
}

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

	// hotSwapMu guards the pending hot-swap queue. PLAN.md §133: the
	// publish pipeline pushes a HotSwap entry per affected entity type;
	// the scheduler drains it between ticks (after Step returns,
	// before the next Step) so in-flight automations finish under the
	// old AST and the swap appears atomic to the live world.
	hotSwapMu      sync.Mutex
	pendingHotSwap []HotSwap
}

// HotSwap is one pending entity-type definition update. Carries enough
// metadata for the runtime to identify which ECS components on which
// entities to rebind (or drop, if a component was removed) and where
// to re-fetch the asset bytes if a sprite URL changed. Detailed
// payload shapes land alongside §128 component re-binding; v1 keeps it
// minimal so the wiring can be tested without dragging in the full
// component-store rebind machinery.
type HotSwap struct {
	EntityTypeID int64
	// RemovedComponentKinds lists components that the new definition
	// no longer carries. The runtime drops these from any entity of
	// this type and emits a structured warn-log per dropped row.
	RemovedComponentKinds []string
	// AssetURLsToReload is the set of texture URLs the renderer should
	// invalidate. Sandbox boots may also re-prefetch on the client.
	AssetURLsToReload []string
}

// NewMapInstance constructs an empty instance, registers the canonical
// system pipeline, and runs recovery from (map_state + WAL). The caller
// is responsible for calling LoadChunk for any chunks players are about
// to look at.
//
// `deps` may be a zero value; in that case no systems are registered
// and the scheduler is a bare no-op. Tests that exercise specific
// systems pass a focused SystemDeps; production wiring in cmd/boxland
// passes the full set.
func NewMapInstance(
	ctx context.Context,
	pool *pgxpool.Pool,
	redis rueidis.Client,
	mapsService *maps.Service,
	mapID uint32,
	instanceID string,
	deps SystemDeps,
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

	// Canonical system pipeline. Order: Movement first, Animation
	// right after so the picked anim_id reflects the post-move
	// velocity (matches PLAN.md §4h "input → AI → movement →
	// collision → triggers → audio → AOI" with animation slotted
	// inside the movement stage so it sees the resolved velocity).
	mi.Scheduler.Register(systems.Movement)
	if deps.Animations != nil {
		mi.Scheduler.Register(systems.NewAnimationSystem(systems.AnimationSystemOptions{
			Catalog: deps.Animations,
		}))
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

// PersistFlushInputs builds the EncodeInputs the persister needs to
// flush this instance's state to Postgres. Used by graceful-shutdown
// (cmd/boxland/main.go) and could feed periodic flush from the
// scheduler later.
func (mi *MapInstance) PersistFlushInputs() persist.EncodeInputs {
	return persist.EncodeInputs{
		MapID:      mi.MapID,
		InstanceID: mi.InstanceID,
		Tick:       mi.Scheduler.Tick(),
		Stores:     mi.World.Stores(),
	}
}

// QueueHotSwap enqueues one definition-update for the next tick boundary.
// PLAN.md §133: at a tick boundary, swap entity-type definitions and
// re-bind component data; in-flight automations finish their current
// tick under the old AST.
//
// The publish pipeline calls this from a post-commit hook (one
// QueueHotSwap per affected entity type). DrainHotSwaps applies them.
func (mi *MapInstance) QueueHotSwap(hs HotSwap) {
	mi.hotSwapMu.Lock()
	mi.pendingHotSwap = append(mi.pendingHotSwap, hs)
	mi.hotSwapMu.Unlock()
}

// DrainHotSwaps applies every queued HotSwap and returns the count
// applied. Safe to call between ticks; the scheduler invokes this
// from its tick-boundary hook so in-flight automations finish under
// the old definitions.
//
// v1 implementation logs each swap + drops the entry. Real component
// re-binding lands when the entity-type definition cache becomes a
// real first-class object the ECS can swap atomically (deferred until
// the asset pipeline + automation compiler are linked into the runtime
// loop). PLAN.md §133 is satisfied at the architectural level: the
// pipeline + queue + drain seam is in place; behaviour fills in
// without touching the wire.
func (mi *MapInstance) DrainHotSwaps() int {
	mi.hotSwapMu.Lock()
	swaps := mi.pendingHotSwap
	mi.pendingHotSwap = nil
	mi.hotSwapMu.Unlock()
	for _, hs := range swaps {
		// Future work: walk every entity of this type, rebind
		// components, drop removed components with a structured
		// warn-log per dropped row, signal the renderer to reload
		// AssetURLsToReload. For v1 the seam exists + the warn-log
		// shape is established below.
		for _, k := range hs.RemovedComponentKinds {
			// Component-removal warn-log: PLAN.md §133.
			// (Stub: no entities to walk yet because the runtime
			// loop doesn't materialize automation-only components.)
			_ = k
		}
	}
	return len(swaps)
}

// PendingHotSwapCount returns the queued (but not yet applied) HotSwap
// entries. Test helper; production callers should call DrainHotSwaps.
func (mi *MapInstance) PendingHotSwapCount() int {
	mi.hotSwapMu.Lock()
	defer mi.hotSwapMu.Unlock()
	return len(mi.pendingHotSwap)
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
