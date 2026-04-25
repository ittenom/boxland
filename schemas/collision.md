# Canonical swept-AABB collision algorithm

> This document is **the** cross-runtime contract for movement and collision in Boxland. The Go server (`server/internal/sim/collision`), the web client (`web/src/collision`), and at v1.1 the iOS client (`ios/Boxland/Collision`) implement *this exact algorithm*. All three runtimes are gated against `/shared/test-vectors/collision.json` in CI.

If a real game-feel issue surfaces and we change the algorithm, change it **here first**, regenerate test vectors, then update the three implementations. Drift is a bug.

---

## Coordinate system

- All positions, velocities, and AABB extents are `int32` **fixed-point sub-pixel units**: `1 px = 256 sub-units` (8 fractional bits).
- Tile grid coordinates `(gx, gy)` are `int32` tile indices. A 32×32-pixel tile at grid `(gx, gy)` occupies world AABB `[gx*32*256, gy*32*256, (gx+1)*32*256, (gy+1)*32*256)`.
- **No floating point on the hot path**, on any runtime. Determinism across Go, V8, and (at v1.1) Swift requires this.
- Edge-bit convention: `N=1, E=2, S=4, W=8`. An edge bit means "this edge of the tile blocks movement *crossing it*."

## Inputs to a single move

```
move(entity, Δ):
  entity.aabb     : (left, top, right, bottom)   // world sub-pixels
  entity.mask     : uint32                       // entity collision-layer mask
  Δ               : (dx, dy)                     // requested per-tick motion in sub-pixels
  world.tiles     : indexed by (gx, gy) -> Tile  // see world.fbs
  TILE_SIZE_SUB   : 32 * 256 = 8192
```

## Algorithm — axis-separated swept AABB

```
move(entity, Δ):
  for axis in [X, Y]:
    step = Δ[axis]
    if step == 0:
      continue

    // Compute the tile range the AABB touches during this 1-D sweep.
    sweep_aabb = entity.aabb extended by step along this axis
    (gx0, gy0, gx1, gy1) = tile_range_overlapping(sweep_aabb)

    blocked_at = step                                  // furthest distance we may move
    for gy in [gy0 .. gy1]:
      for gx in [gx0 .. gx1]:
        T = world.tiles[gx, gy]
        if T == nil:                                   continue
        if (T.collision_layer_mask & entity.mask) == 0: continue

        // Identify which edge of T faces the *incoming* side.
        // For axis X moving +step (east):  the WEST edge of T blocks us.
        // For axis X moving -step (west):  the EAST edge of T blocks us.
        // For axis Y moving +step (south): the NORTH edge of T blocks us.
        // For axis Y moving -step (north): the SOUTH edge of T blocks us.
        edge_bit = facing_edge_bit(axis, sign(step))

        if (T.edge_collisions & edge_bit) == 0:        continue

        // Distance from entity's leading face to that edge of T:
        contact = distance_to_edge(entity.aabb, T, axis, sign(step))

        // contact may be 0 or negative if we're already touching/overlapping.
        // Clamp so we never advance past the contact.
        if sign(step) > 0:
          if contact < blocked_at:  blocked_at = max(contact, 0)
        else:
          if contact > blocked_at:  blocked_at = min(contact, 0)

    // Apply the (possibly clipped) motion on this axis.
    advance(entity.aabb, axis, blocked_at)
    Δ[axis] = blocked_at        // residual; a higher layer may inspect it
```

### Helper definitions

```
facing_edge_bit(axis, s):
  axis=X, s>0  ->  W (8)        // moving east hits a tile's west edge
  axis=X, s<0  ->  E (2)
  axis=Y, s>0  ->  N (1)        // moving south hits a tile's north edge
  axis=Y, s<0  ->  S (4)

distance_to_edge(aabb, T, axis, s):
  // T's world bounds in sub-pixels
  T_left   = T.gx * TILE_SIZE_SUB
  T_top    = T.gy * TILE_SIZE_SUB
  T_right  = T_left + TILE_SIZE_SUB
  T_bottom = T_top  + TILE_SIZE_SUB
  if axis == X and s > 0:  return T_left   - aabb.right
  if axis == X and s < 0:  return T_right  - aabb.left
  if axis == Y and s > 0:  return T_top    - aabb.bottom
  if axis == Y and s < 0:  return T_bottom - aabb.top
```

### Slide

Sliding falls out automatically: when one axis is blocked, the other axis's sweep on the next iteration of the outer `for` proceeds independently. Walking diagonally into a corner produces motion along whichever axis is unblocked. Walking parallel to a wall jitters not at all because the parallel axis never registers a contact.

## Invariants (verified by `/shared/test-vectors/collision.json`)

1. **Determinism**: identical `(entity, Δ, world)` produces byte-identical resolved AABB across all runtimes.
2. **No tunneling for motion ≤ 1 tile/tick**: an entity moving up to one full tile in a single tick cannot pass through a fully-blocked edge.
3. **Mask short-circuit**: a tile whose `collision_layer_mask & entity.mask == 0` is invisible to that entity.
4. **Idempotent zero-motion**: `move(entity, (0, 0))` is a no-op.
5. **Edge-bit semantics**: only the *facing* edge bit is consulted; the other three edge bits of a tile do not affect this collision direction.
6. **Sliding**: motion `(dx, dy)` blocked on X still applies `dy`; blocked on Y still applies `dx`.
7. **Diagonal preset shapes** (`DiagNE` / `DiagNW` / `DiagSE` / `DiagSW`) expand to two adjacent edge bits at load time. The runtime sees only the resolved edge bits; it does not re-interpret the preset enum during movement.

## Out of scope for v1

- One-way edges (e.g., south-edge-only-when-Δy>0). The `edge_collisions` field has room; the algorithm ignores direction-conditional bits in v1. See PLAN.md §11 #6.
- Per-tile sub-mask collision (e.g., 8×8 sub-cells inside a tile). See PLAN.md §12.
- Entity-vs-entity collision. The current algorithm is entity-vs-tile only; entity-vs-entity is handled at the simulation layer (e.g., soft pushes via the spatial index) without participating in the swept AABB above.

## Test-vector format

`/shared/test-vectors/collision.json` is the authoritative cross-runtime test corpus. Each vector has the shape:

```json
{
  "name": "slide_along_north_wall",
  "world": {
    "tiles": [
      { "gx": 0, "gy": 0, "edge_collisions": 4, "collision_layer_mask": 1 }
    ]
  },
  "entity": {
    "aabb":  [256, 256, 5120, 3328],
    "mask":  1
  },
  "delta": [1024, 1024],
  "expected_resolved_delta": [1024, 0]
}
```

All values are sub-pixel `int32`. Runtime tests run the algorithm and assert `resolved_delta` equals `expected_resolved_delta` exactly.
