package ecs

import (
	"errors"
	"sync"
)

// World is the per-instance ECS root. Owns the entity registry + every
// registered ComponentStore. Use NewWorld + RegisterStore at boot, then
// Spawn / Despawn through the tick loop.
//
// Concurrency model: not safe for concurrent mutation. The map-instance
// goroutine owns the world; systems run sequentially within one tick. If
// future profiling shows a hot system worth parallelizing, do it via a
// per-system Owners() snapshot + a deferred mutation queue, not by
// mutex-locking the world.
type World struct {
	// Entity registry
	mu          sync.RWMutex
	generations []uint8     // index -> current generation
	freeList    []uint32    // recycled slot indices
	alive       []bool      // index -> is the slot live?
	count       int         // live entity count

	// Component stores. Optional: nil means callers manage lifetimes
	// themselves (used by tests that don't need component cleanup).
	stores *Stores
}

// NewWorld constructs an empty world. The Stores are pre-allocated so the
// runtime can attach components immediately after Spawn.
func NewWorld() *World {
	return &World{stores: NewStores(0)}
}

// Stores returns the shared Stores bundle. Hot-path systems reach for
// world.Stores().Position etc.
func (w *World) Stores() *Stores { return w.stores }

// SetStores swaps in a custom Stores bundle. Used by tests that need to
// pre-size component stores for benchmarking.
func (w *World) SetStores(s *Stores) { w.stores = s }

// Spawn allocates a fresh EntityID. Reuses freed slots when possible to
// keep the entity index space dense.
func (w *World) Spawn() EntityID {
	w.mu.Lock()
	defer w.mu.Unlock()

	var index uint32
	if n := len(w.freeList); n > 0 {
		index = w.freeList[n-1]
		w.freeList = w.freeList[:n-1]
	} else {
		index = uint32(len(w.generations))
		// Start generations at 1 so the very first EntityID (index 0,
		// gen 0 == raw zero) doesn't collide with the invalid sentinel.
		w.generations = append(w.generations, 1)
		w.alive = append(w.alive, false)
	}
	w.alive[index] = true
	w.count++
	return MakeEntityID(index, w.generations[index])
}

// Despawn marks the entity as freed and bumps its generation so any held
// reference becomes invalid. Components belonging to it must be removed
// separately by the caller (typically via the world's component-cleanup
// pass; not built in v1 — explicit removal in tests / sim code keeps the
// dependency graph honest).
func (w *World) Despawn(e EntityID) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	idx := e.Index()
	if int(idx) >= len(w.alive) || !w.alive[idx] || w.generations[idx] != e.Generation() {
		return ErrUnknownEntity
	}
	w.alive[idx] = false
	// Bump generation; skip 0 on uint8 wraparound so the EntityID for slot
	// 0 generation 0 (which equals the raw-zero sentinel) is never produced.
	w.generations[idx]++
	if w.generations[idx] == 0 {
		w.generations[idx] = 1
	}
	w.freeList = append(w.freeList, idx)
	w.count--

	// Drop any components the entity owned. Stores' methods don't take
	// w.mu, so we can call them while still holding it; the world is
	// single-writer per PLAN.md §1 (one map-instance goroutine).
	if w.stores != nil {
		w.stores.RemoveAll(e)
	}
	return nil
}

// Alive reports whether the EntityID still refers to a live entity.
// Returns false for despawned entities (generation mismatch) and for
// EntityIDs that were never spawned.
func (w *World) Alive(e EntityID) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	idx := e.Index()
	if int(idx) >= len(w.alive) {
		return false
	}
	return w.alive[idx] && w.generations[idx] == e.Generation()
}

// Count returns the number of live entities.
func (w *World) Count() int {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.count
}

// ErrUnknownEntity is returned by Despawn when called against an entity
// that's already gone (or never existed).
var ErrUnknownEntity = errors.New("ecs: unknown or already-despawned entity")
