// Boxland — canonical swept-AABB collision (server port).
//
// THIS IMPLEMENTATION IS A LITERAL PORT of schemas/collision.md and must
// produce byte-identical resolved deltas to web/src/collision/move.ts for
// every vector in /shared/test-vectors/collision.json. The corpus is
// gated against this implementation in collision_corpus_test.go.
//
// Algorithm (axis-separated swept AABB):
//
//	move(entity, Δ):
//	  for axis in [X, Y]:
//	    sweep entity.AABB along Δ[axis]
//	    for each tile T overlapping the sweep:
//	      if (T.collision_layer_mask & entity.mask) == 0: continue
//	      determine which edge of T faces the sweep direction
//	      if that edge bit is unset: continue
//	      clip Δ[axis] to the contact point
//	    apply (clipped) Δ[axis]
package collision

const (
	axisX = 0
	axisY = 1
)

// Move advances entity by (dx, dy) sub-pixels, applying axis-separated
// swept-AABB collision against world. Mutates entity.AABB in place;
// returns the actual delta applied so callers can observe slides.
func Move(entity *Entity, dx, dy int32, world World) MoveResult {
	dxR := sweepAxis(entity, axisX, dx, world)
	dyR := sweepAxis(entity, axisY, dy, world)
	return MoveResult{ResolvedDX: dxR, ResolvedDY: dyR}
}

func sweepAxis(entity *Entity, axis int, step int32, world World) int32 {
	if step == 0 {
		return 0
	}

	sweep := extendAABB(entity.AABB, axis, step)
	gx0, gy0, gx1, gy1 := tileRangeOverlapping(sweep)

	sign := int32(1)
	if step < 0 {
		sign = -1
	}
	edgeBit := facingEdgeBit(axis, sign)

	blockedAt := step

	for gy := gy0; gy <= gy1; gy++ {
		for gx := gx0; gx <= gx1; gx++ {
			t, ok := world.TileAt(gx, gy)
			if !ok {
				continue
			}
			if (t.CollisionLayerMask & entity.Mask) == 0 {
				continue
			}
			if (t.EdgeCollisions & edgeBit) == 0 {
				continue
			}
			// One-way platform rule: a tile authored as ShapeOneWayN
			// blocks downward motion (axis=Y, sign>0) ONLY when the
			// entity's foot is already at or above the tile top at the
			// START of the sweep. Otherwise skip the block — entities
			// crossing in from the side or rising through the bottom
			// pass through cleanly. See shape.go IsOneWay.
			if IsOneWay(t.Shape) {
				if axis != axisY || sign <= 0 {
					continue
				}
				tileTop := gy * TileSizeSub
				if entity.AABB.Bottom > tileTop {
					continue
				}
			}
			contact := distanceToEdge(entity.AABB, gx, gy, axis, sign)
			if sign > 0 {
				clamped := contact
				if clamped < 0 {
					clamped = 0
				}
				if clamped < blockedAt {
					blockedAt = clamped
				}
			} else {
				clamped := contact
				if clamped > 0 {
					clamped = 0
				}
				if clamped > blockedAt {
					blockedAt = clamped
				}
			}
		}
	}

	advance(&entity.AABB, axis, blockedAt)
	return blockedAt
}

// ---- helpers ----

func extendAABB(box AABB, axis int, step int32) AABB {
	if axis == axisX {
		if step >= 0 {
			return AABB{Left: box.Left, Top: box.Top, Right: box.Right + step, Bottom: box.Bottom}
		}
		return AABB{Left: box.Left + step, Top: box.Top, Right: box.Right, Bottom: box.Bottom}
	}
	if step >= 0 {
		return AABB{Left: box.Left, Top: box.Top, Right: box.Right, Bottom: box.Bottom + step}
	}
	return AABB{Left: box.Left, Top: box.Top + step, Right: box.Right, Bottom: box.Bottom}
}

// tileRangeOverlapping returns the (inclusive) tile-grid bounds the AABB
// touches. Right/bottom edges that lie exactly on a tile boundary belong
// to the previous tile, not the next, to avoid touching a tile we don't
// actually overlap.
func tileRangeOverlapping(box AABB) (gx0, gy0, gx1, gy1 int32) {
	gx0 = floorDiv(box.Left, TileSizeSub)
	gy0 = floorDiv(box.Top, TileSizeSub)
	gx1 = floorDiv(box.Right-1, TileSizeSub)
	gy1 = floorDiv(box.Bottom-1, TileSizeSub)
	return
}

func facingEdgeBit(axis int, sign int32) uint8 {
	if axis == axisX {
		if sign > 0 {
			return EdgeW
		}
		return EdgeE
	}
	if sign > 0 {
		return EdgeN
	}
	return EdgeS
}

func distanceToEdge(aabb AABB, gx, gy int32, axis int, sign int32) int32 {
	tLeft := gx * TileSizeSub
	tTop := gy * TileSizeSub
	tRight := tLeft + TileSizeSub
	tBottom := tTop + TileSizeSub
	if axis == axisX {
		if sign > 0 {
			return tLeft - aabb.Right
		}
		return tRight - aabb.Left
	}
	if sign > 0 {
		return tTop - aabb.Bottom
	}
	return tBottom - aabb.Top
}

func advance(aabb *AABB, axis int, by int32) {
	if by == 0 {
		return
	}
	if axis == axisX {
		aabb.Left += by
		aabb.Right += by
		return
	}
	aabb.Top += by
	aabb.Bottom += by
}

// floorDiv is integer division rounded towards negative infinity.
// Stdlib `/` rounds towards zero; we need floor for chunk math.
func floorDiv(a, b int32) int32 {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}
