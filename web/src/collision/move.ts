// Boxland — canonical swept-AABB collision (web port).
//
// THIS IMPLEMENTATION IS A LITERAL PORT of schemas/collision.md. Server
// (Go), web (this), and (at v1.1) iOS (Swift) MUST produce byte-identical
// resolved deltas for every vector in /shared/test-vectors/collision.json.
// CI gates all runtimes against the same corpus.
//
// All math is integer arithmetic on int32 fixed-point sub-pixels (1 px =
// 256 sub-units). No floats anywhere on the hot path.

import {
	EDGE_E,
	EDGE_N,
	EDGE_S,
	EDGE_W,
	TILE_SIZE_SUB,
	isOneWay,
	type AABB,
	type Entity,
	type MoveResult,
	type World,
} from "./types";

// Axis is a tagged number so signature reads are obvious.
const AXIS_X = 0;
const AXIS_Y = 1;
type Axis = typeof AXIS_X | typeof AXIS_Y;

/**
 * Move `entity` by (dx, dy) sub-pixels through `world`, applying axis-
 * separated swept-AABB collision. The entity's aabb is mutated in place to
 * reflect the resolved position. Returns the actual delta applied so
 * callers can observe slides.
 */
export function move(entity: Entity, dx: number, dy: number, world: World): MoveResult {
	const dxResolved = sweepAxis(entity, AXIS_X, dx | 0, world);
	const dyResolved = sweepAxis(entity, AXIS_Y, dy | 0, world);
	return { resolvedDx: dxResolved, resolvedDy: dyResolved };
}

function sweepAxis(entity: Entity, axis: Axis, step: number, world: World): number {
	if (step === 0) return 0;

	const sweepBox = extendAABB(entity.aabb, axis, step);
	const [gx0, gy0, gx1, gy1] = tileRangeOverlapping(sweepBox);

	const sign = step > 0 ? 1 : -1;
	const edgeBit = facingEdgeBit(axis, sign);

	let blockedAt = step;

	for (let gy = gy0; gy <= gy1; gy++) {
		for (let gx = gx0; gx <= gx1; gx++) {
			const T = world.get(gx, gy);
			if (!T) continue;
			if ((T.collision_layer_mask & entity.mask) === 0) continue;
			if ((T.edge_collisions & edgeBit) === 0) continue;

			// One-way platform rule: a tile authored as OneWayN blocks
			// downward motion ONLY when the entity's foot is already at
			// or above the tile top. Otherwise pass through (entry from
			// the side, or rising from below). Mirror of the Go rule
			// in server/internal/sim/collision/move.go.
			if (isOneWay(T.shape)) {
				if (axis !== AXIS_Y || sign <= 0) continue;
				const tileTop = gy * TILE_SIZE_SUB;
				if (entity.aabb.bottom > tileTop) continue;
			}

			const contact = distanceToEdge(entity.aabb, gx, gy, axis, sign);
			if (sign > 0) {
				const clamped = contact < 0 ? 0 : contact;
				if (clamped < blockedAt) blockedAt = clamped;
			} else {
				const clamped = contact > 0 ? 0 : contact;
				if (clamped > blockedAt) blockedAt = clamped;
			}
		}
	}

	advance(entity.aabb, axis, blockedAt);
	return blockedAt;
}

// ---- helpers ----

function extendAABB(box: AABB, axis: Axis, step: number): AABB {
	if (axis === AXIS_X) {
		return step >= 0
			? { left: box.left, top: box.top, right: box.right + step, bottom: box.bottom }
			: { left: box.left + step, top: box.top, right: box.right, bottom: box.bottom };
	}
	return step >= 0
		? { left: box.left, top: box.top, right: box.right, bottom: box.bottom + step }
		: { left: box.left, top: box.top + step, right: box.right, bottom: box.bottom };
}

/**
 * Tile-grid AABB cover. Uses inclusive bounds; the right/bottom edge of a
 * box that sits exactly on a tile boundary belongs to the *previous* tile,
 * not the next, to avoid touching a tile we don't actually overlap.
 */
function tileRangeOverlapping(box: AABB): [number, number, number, number] {
	const gx0 = Math.floor(box.left   / TILE_SIZE_SUB);
	const gy0 = Math.floor(box.top    / TILE_SIZE_SUB);
	const gx1 = Math.floor((box.right  - 1) / TILE_SIZE_SUB);
	const gy1 = Math.floor((box.bottom - 1) / TILE_SIZE_SUB);
	return [gx0, gy0, gx1, gy1];
}

function facingEdgeBit(axis: Axis, sign: number): number {
	if (axis === AXIS_X) return sign > 0 ? EDGE_W : EDGE_E;
	return sign > 0 ? EDGE_N : EDGE_S;
}

function distanceToEdge(aabb: AABB, gx: number, gy: number, axis: Axis, sign: number): number {
	const tLeft   = gx * TILE_SIZE_SUB;
	const tTop    = gy * TILE_SIZE_SUB;
	const tRight  = tLeft + TILE_SIZE_SUB;
	const tBottom = tTop + TILE_SIZE_SUB;
	if (axis === AXIS_X) {
		return sign > 0 ? tLeft - aabb.right : tRight - aabb.left;
	}
	return sign > 0 ? tTop - aabb.bottom : tBottom - aabb.top;
}

function advance(aabb: AABB, axis: Axis, by: number): void {
	if (by === 0) return;
	if (axis === AXIS_X) {
		aabb.left += by;
		aabb.right += by;
	} else {
		aabb.top += by;
		aabb.bottom += by;
	}
}
