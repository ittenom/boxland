package systems

import (
	"context"
	"strings"
	"sync"

	"boxland/server/internal/assets"
	"boxland/server/internal/entities/components"
	"boxland/server/internal/sim"
	"boxland/server/internal/sim/ecs"
)

// AnimationCatalog is the lookup the Animation system needs to translate
// a (sprite_asset_id, animation name) pair into the persisted anim_id
// the wire-format carries. The runtime injects an adapter that reads
// from assets.Service.ListAnimations; tests pass an inline map.
//
// Keeping the lookup pull-based (rather than seeded at boot) means new
// asset rows landing via publish are visible without a system restart.
type AnimationCatalog interface {
	// AnimationsFor returns every (name, anim_id) pair persisted for
	// the given sprite asset. Name is lowercased here so the system
	// can do case-insensitive matching against the canonical
	// `walk_north`/`idle` names without per-call allocation.
	AnimationsFor(ctx context.Context, assetID uint32) (map[string]uint16, error)
}

// AnimationSystemOptions configures NewAnimationSystem. The catalog is
// required; everything else is optional.
type AnimationSystemOptions struct {
	Catalog AnimationCatalog
	// CacheTTLTicks bounds how long a per-asset name→id table sticks
	// around before it's re-fetched. Zero = forever (publish-time
	// HotSwap is the only invalidation), which is the normal mode in
	// production. Tests set a low value to exercise refresh paths.
	CacheTTLTicks uint64
}

// NewAnimationSystem returns a SystemEntry that runs immediately after
// movement: read each entity's velocity, derive a facing, look up the
// matching `walk_<facing>` (or `idle`) clip on its sprite asset, and
// stamp the resulting (Facing, AnimID) onto the Sprite component.
//
// Stationary entities (zero velocity) keep their last facing — most
// pixel-art expects "stand still in the direction you were last
// walking" rather than snapping to a default. We swap the running
// `walk_*` clip for `idle` though, so the renderer's frame clock
// stops the cycle.
//
// The system writes ONLY when the resolved (Facing, AnimID) pair
// would actually change, so a static crowd of NPCs costs no Diff
// bandwidth.
func NewAnimationSystem(opts AnimationSystemOptions) sim.SystemEntry {
	if opts.Catalog == nil {
		panic("systems.NewAnimationSystem: Catalog required")
	}
	cache := newAnimCache(opts.Catalog, opts.CacheTTLTicks)
	return sim.SystemEntry{
		Name:  "animation",
		// Runs in the movement stage so it sits right after Movement.
		// Stages are coarse-grained; within a stage entries run in
		// registration order, so production wires Movement first.
		Stage: sim.StageMovement,
		Run: func(ctx context.Context, w *ecs.World) error {
			cache.tick++
			stores := w.Stores()
			stores.Sprite.Each(func(e ecs.EntityID, sp *components.Sprite) {
				if stores.Static.Has(e) {
					// Tile entities don't animate from movement —
					// their anim_id is set by automation actions
					// (ActionSetAnimation) instead.
					return
				}
				v := stores.Velocity.GetPtr(e)
				vx, vy := int32(0), int32(0)
				if v != nil {
					vx, vy = v.VX, v.VY
				}
				newFacing, hasDir := assets.FacingFromVelocity(vx, vy)
				if !hasDir {
					newFacing = sp.Facing // sticky on a zero vector
				}
				// Resolve the desired animation name for this tick.
				want := assets.AnimIdle
				if hasDir {
					want = assets.WalkAnimForFacing(newFacing)
				}
				newAnimID, ok := cache.resolve(ctx, sp.AssetID, want)
				if !ok {
					// No matching clip on the sheet — leave anim_id
					// alone (renderer will use the last known clip).
					// Still update facing because that's a wire field
					// other systems care about (e.g. interaction
					// targeting in the future).
					if sp.Facing != newFacing {
						sp.Facing = newFacing
					}
					return
				}
				if sp.Facing != newFacing {
					sp.Facing = newFacing
				}
				if uint32(newAnimID) != sp.AnimID {
					sp.AnimID = uint32(newAnimID)
				}
			})
			return nil
		},
	}
}

// ---- Per-asset name→id cache --------------------------------------------

type animCache struct {
	catalog AnimationCatalog
	ttl     uint64
	tick    uint64

	mu      sync.RWMutex
	entries map[uint32]*animCacheEntry
}

type animCacheEntry struct {
	names    map[string]uint16
	loadTick uint64
	failed   bool // true when the catalog fetch errored; suppresses thrash
}

func newAnimCache(c AnimationCatalog, ttl uint64) *animCache {
	return &animCache{
		catalog: c,
		ttl:     ttl,
		entries: make(map[uint32]*animCacheEntry, 32),
	}
}

// resolve returns (anim_id, true) for the requested clip on the asset.
// Walks the standard fallback chain when the exact name isn't present
// (matches the client's PickWalkAnim — kept consistent so server and
// renderer never disagree on which clip is authoritative).
//
// Returns (0, false) when the asset has no animations at all (or the
// catalog fetch failed), so the caller can leave the existing anim_id
// in place rather than zeroing it.
func (c *animCache) resolve(ctx context.Context, assetID uint32, want string) (uint16, bool) {
	if assetID == 0 {
		return 0, false
	}
	entry := c.lookup(ctx, assetID)
	if entry == nil || len(entry.names) == 0 {
		return 0, false
	}
	want = strings.ToLower(want)
	if id, ok := entry.names[want]; ok {
		return id, true
	}
	// Fallback chain: walk → idle → first listed.
	if id, ok := entry.names[assets.AnimWalk]; ok {
		return id, true
	}
	if id, ok := entry.names[assets.AnimIdle]; ok {
		return id, true
	}
	for _, id := range entry.names {
		return id, true
	}
	return 0, false
}

func (c *animCache) lookup(ctx context.Context, assetID uint32) *animCacheEntry {
	c.mu.RLock()
	entry, ok := c.entries[assetID]
	c.mu.RUnlock()
	if ok && !c.expired(entry) {
		return entry
	}
	// Fetch (one round trip per asset; the catalog caches in-memory
	// inside the runtime so this is cheap).
	names, err := c.catalog.AnimationsFor(ctx, assetID)
	c.mu.Lock()
	defer c.mu.Unlock()
	// Re-check under the write lock in case a concurrent goroutine
	// populated the cache while we were fetching.
	if existing, ok := c.entries[assetID]; ok && !c.expired(existing) {
		return existing
	}
	e := &animCacheEntry{
		names:    names,
		loadTick: c.tick,
		failed:   err != nil,
	}
	c.entries[assetID] = e
	return e
}

func (c *animCache) expired(e *animCacheEntry) bool {
	if e == nil {
		return true
	}
	if c.ttl == 0 {
		return false
	}
	return c.tick-e.loadTick >= c.ttl
}

// Invalidate drops any cached entry for `assetID`. Called by the
// publish hot-swap pipeline so the next tick picks up the new clips.
func (c *animCache) Invalidate(assetID uint32) {
	c.mu.Lock()
	delete(c.entries, assetID)
	c.mu.Unlock()
}
