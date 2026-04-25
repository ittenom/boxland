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

/** Tile cell as the collision algorithm sees it (post-shape-resolution). */
export interface Tile {
	gx: number;
	gy: number;
	edge_collisions: number;     // bitmask of EDGE_N|E|S|W
	collision_layer_mask: number;
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
