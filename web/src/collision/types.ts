// Boxland — collision shared types.
//
// Coordinate convention (from schemas/collision.md):
//   * positions, deltas, AABB extents are int32 fixed-point sub-pixels
//   * 1 px = 256 sub-units (8 fractional bits)
//   * tile grid coords (gx, gy) are int32 tile indices
//   * a 32x32-pixel tile at (gx,gy) covers
//     [gx*TILE_SIZE_SUB, gy*TILE_SIZE_SUB, (gx+1)*TILE_SIZE_SUB, (gy+1)*TILE_SIZE_SUB)

export const SUB_PER_PX = 256;
export const TILE_PX = 32;
export const TILE_SIZE_SUB = TILE_PX * SUB_PER_PX;

/** 4-bit edge-collision mask. N=1 E=2 S=4 W=8 (matches world.fbs). */
export const EDGE_N = 1;
export const EDGE_E = 2;
export const EDGE_S = 4;
export const EDGE_W = 8;

/** AABB in world sub-pixel coordinates. */
export interface AABB {
	left: number;
	top: number;
	right: number;
	bottom: number;
}

/** Tile cell as the collision algorithm sees it (post-shape-resolution).
 *  `shape` is optional and only consulted by move() for shape-specific
 *  rules (the OneWayN platform's foot-position check). Default value
 *  means "treat as the precomputed edges, no special-casing". */
export interface Tile {
	gx: number;
	gy: number;
	edge_collisions: number;     // bitmask of EDGE_N|E|S|W
	collision_layer_mask: number;
	shape?: CollisionShape;
}

/** CollisionShape mirrors world.fbs CollisionShape. Numeric values are
 *  stable; do not renumber. */
export const enum CollisionShape {
	Open       = 0,
	Solid      = 1,
	WallNorth  = 2,
	WallEast   = 3,
	WallSouth  = 4,
	WallWest   = 5,
	DiagNE     = 6,
	DiagNW     = 7,
	DiagSE     = 8,
	DiagSW     = 9,
	HalfNorth  = 10,
	HalfEast   = 11,
	HalfSouth  = 12,
	HalfWest   = 13,
	OneWayN    = 14,
}

/** True when the shape needs the runtime foot-position rule applied at
 *  move-resolution time. See server/internal/sim/collision/shape.go. */
export function isOneWay(s: CollisionShape | undefined): boolean {
	return s === CollisionShape.OneWayN;
}

/** Edge-bit mask for a CollisionShape. Mirror of EdgesForShape in Go.
 *  Used by the loader to expand the authored shape into the bits the
 *  move() resolver consumes. */
export function edgesForShape(s: CollisionShape): number {
	switch (s) {
		case CollisionShape.Open:       return 0;
		case CollisionShape.Solid:      return EDGE_N | EDGE_E | EDGE_S | EDGE_W;
		case CollisionShape.WallNorth:  return EDGE_N;
		case CollisionShape.WallEast:   return EDGE_E;
		case CollisionShape.WallSouth:  return EDGE_S;
		case CollisionShape.WallWest:   return EDGE_W;
		case CollisionShape.DiagNE:     return EDGE_N | EDGE_E;
		case CollisionShape.DiagNW:     return EDGE_N | EDGE_W;
		case CollisionShape.DiagSE:     return EDGE_S | EDGE_E;
		case CollisionShape.DiagSW:     return EDGE_S | EDGE_W;
		case CollisionShape.HalfNorth:  return EDGE_N | EDGE_E | EDGE_W;
		case CollisionShape.HalfEast:   return EDGE_N | EDGE_E | EDGE_S;
		case CollisionShape.HalfSouth:  return EDGE_E | EDGE_S | EDGE_W;
		case CollisionShape.HalfWest:   return EDGE_N | EDGE_S | EDGE_W;
		case CollisionShape.OneWayN:    return EDGE_N;
	}
	return 0;
}

/** Sparse world: tiles indexed by `${gx},${gy}`. Use buildWorld() to load. */
export interface World {
	get(gx: number, gy: number): Tile | undefined;
}

/** Movement input. */
export interface Entity {
	aabb: AABB;
	mask: number; // entity collision-layer mask
}

/** Result of move(): the resolved delta actually applied. */
export interface MoveResult {
	resolvedDx: number;
	resolvedDy: number;
}
