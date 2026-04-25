// Boxland — shared collision module public surface.
// See schemas/collision.md for the canonical algorithm.

export { move } from "./move";
export { buildWorld } from "./world";
export type { AABB, Entity, MoveResult, Tile, World } from "./types";
export {
	EDGE_E, EDGE_N, EDGE_S, EDGE_W,
	SUB_PER_PX, TILE_PX, TILE_SIZE_SUB,
} from "./types";
