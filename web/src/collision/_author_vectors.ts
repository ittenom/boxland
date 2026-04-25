// One-shot script that builds the canonical collision test corpus by
// running the web `move` implementation against each scenario, then prints
// the result as JSON. Copy the output into /shared/test-vectors/collision.json.
//
// Run: npx tsx src/collision/_author_vectors.ts (or via just author-collision)
//
// IMPORTANT: only run this when you intentionally want to (re)author the
// corpus. The whole point of the corpus is that it's been audited; future
// runs verify against it.

import { buildWorld, move, EDGE_E, EDGE_N, EDGE_S, EDGE_W, TILE_SIZE_SUB } from "./index";
import type { AABB, Tile } from "./types";

const PX = 256;

function entity(aabb: [number, number, number, number], mask = 1): { aabb: AABB; mask: number } {
	return { aabb: { left: aabb[0], top: aabb[1], right: aabb[2], bottom: aabb[3] }, mask };
}

interface Scenario {
	name: string;
	tiles: Tile[];
	aabb: [number, number, number, number];
	mask: number;
	delta: [number, number];
}

const T = TILE_SIZE_SUB; // 8192

const scenarios: Scenario[] = [
	// --- 1-7: open ground ---
	{
		name: "open_no_motion",
		tiles: [],
		aabb: [4 * PX, 4 * PX, 4 * PX + 5 * PX, 4 * PX + 3 * PX],
		mask: 1,
		delta: [0, 0],
	},
	{
		name: "open_walk_east_one_pixel",
		tiles: [],
		aabb: [4 * PX, 4 * PX, 4 * PX + 5 * PX, 4 * PX + 3 * PX],
		mask: 1,
		delta: [PX, 0],
	},
	{
		name: "open_walk_west_two_pixels",
		tiles: [],
		aabb: [4 * PX, 4 * PX, 4 * PX + 5 * PX, 4 * PX + 3 * PX],
		mask: 1,
		delta: [-2 * PX, 0],
	},
	{
		name: "open_walk_south",
		tiles: [],
		aabb: [4 * PX, 4 * PX, 4 * PX + 5 * PX, 4 * PX + 3 * PX],
		mask: 1,
		delta: [0, 4 * PX],
	},
	{
		name: "open_walk_diagonal_se",
		tiles: [],
		aabb: [4 * PX, 4 * PX, 4 * PX + 5 * PX, 4 * PX + 3 * PX],
		mask: 1,
		delta: [3 * PX, 3 * PX],
	},
	{
		name: "open_walk_diagonal_nw_subpixel",
		tiles: [],
		aabb: [10 * PX, 10 * PX, 15 * PX, 13 * PX],
		mask: 1,
		delta: [-100, -100],
	},
	{
		name: "open_one_subpixel_step",
		tiles: [],
		aabb: [4 * PX, 4 * PX, 4 * PX + 5 * PX, 4 * PX + 3 * PX],
		mask: 1,
		delta: [1, 0],
	},

	// --- 8-13: walk into a wall ---
	{
		// wall to the right (tile (1,0) west edge blocks); entity at left of tile (0,0)
		// AABB right edge = 5 px, target edge of tile (1,0) west = 32 px = 8192 sub.
		// gap = 8192 - 5*PX = 8192 - 1280 = 6912 sub. Step 8 px = 2048 → no contact.
		name: "wall_east_clear_short_step",
		tiles: [{ gx: 1, gy: 0, edge_collisions: EDGE_W, collision_layer_mask: 1 }],
		aabb: [0, PX, 5 * PX, 4 * PX],
		mask: 1,
		delta: [8 * PX, 0],
	},
	{
		// Same wall, step that exactly meets the wall.
		name: "wall_east_exact_meet",
		tiles: [{ gx: 1, gy: 0, edge_collisions: EDGE_W, collision_layer_mask: 1 }],
		aabb: [0, PX, 5 * PX, 4 * PX],
		mask: 1,
		delta: [27 * PX, 0],
	},
	{
		// Step that would overshoot — should clip to gap.
		name: "wall_east_overshoot_clipped",
		tiles: [{ gx: 1, gy: 0, edge_collisions: EDGE_W, collision_layer_mask: 1 }],
		aabb: [0, PX, 5 * PX, 4 * PX],
		mask: 1,
		delta: [40 * PX, 0],
	},
	{
		// West wall: entity east of tile (-1, 0) east edge.
		name: "wall_west_overshoot_clipped",
		tiles: [{ gx: -1, gy: 0, edge_collisions: EDGE_E, collision_layer_mask: 1 }],
		aabb: [10 * PX, PX, 15 * PX, 4 * PX],
		mask: 1,
		delta: [-50 * PX, 0],
	},
	{
		// North wall: entity south of tile (0,-1) south edge.
		name: "wall_north_overshoot_clipped",
		tiles: [{ gx: 0, gy: -1, edge_collisions: EDGE_S, collision_layer_mask: 1 }],
		aabb: [PX, 8 * PX, 6 * PX, 11 * PX],
		mask: 1,
		delta: [0, -30 * PX],
	},
	{
		// South wall: entity north of tile (0,1) north edge.
		name: "wall_south_overshoot_clipped",
		tiles: [{ gx: 0, gy: 1, edge_collisions: EDGE_N, collision_layer_mask: 1 }],
		aabb: [PX, 0, 6 * PX, 4 * PX],
		mask: 1,
		delta: [0, 50 * PX],
	},

	// --- 14-17: slide along a wall ---
	{
		// Entity in tile (0,0), wall on east edge of tile (1,0); push diagonally SE.
		// X is blocked, Y should still apply.
		name: "slide_along_east_wall_southward",
		tiles: [{ gx: 1, gy: 0, edge_collisions: EDGE_W, collision_layer_mask: 1 }],
		aabb: [0, PX, 5 * PX, 4 * PX],
		mask: 1,
		delta: [40 * PX, 5 * PX],
	},
	{
		// Same wall, push diagonally NE (Y negative).
		name: "slide_along_east_wall_northward",
		tiles: [{ gx: 1, gy: 0, edge_collisions: EDGE_W, collision_layer_mask: 1 }],
		aabb: [0, 6 * PX, 5 * PX, 9 * PX],
		mask: 1,
		delta: [40 * PX, -3 * PX],
	},
	{
		// Wall to the south, push SE.
		name: "slide_along_south_wall_eastward",
		tiles: [{ gx: 0, gy: 1, edge_collisions: EDGE_N, collision_layer_mask: 1 }],
		aabb: [PX, 0, 6 * PX, 4 * PX],
		mask: 1,
		delta: [4 * PX, 50 * PX],
	},
	{
		// Walking parallel to a wall (pure-X delta with wall to the south);
		// no contact at all even with lots of motion.
		name: "parallel_to_south_wall_no_contact",
		tiles: [{ gx: 0, gy: 1, edge_collisions: EDGE_N, collision_layer_mask: 1 }],
		aabb: [PX, 0, 6 * PX, 4 * PX],
		mask: 1,
		delta: [4 * PX, 0],
	},

	// --- 18-21: corners ---
	{
		// Two perpendicular walls: east blocker at (1,0) and south blocker at (0,1).
		// Diagonal SE motion: both axes get clipped.
		name: "diagonal_into_corner_se",
		tiles: [
			{ gx: 1, gy: 0, edge_collisions: EDGE_W, collision_layer_mask: 1 },
			{ gx: 0, gy: 1, edge_collisions: EDGE_N, collision_layer_mask: 1 },
		],
		aabb: [0, 0, 5 * PX, 4 * PX],
		mask: 1,
		delta: [50 * PX, 50 * PX],
	},
	{
		// Walking diagonally past an *outside* corner (no walls). Just open.
		name: "outside_corner_no_walls",
		tiles: [],
		aabb: [PX, PX, 6 * PX, 4 * PX],
		mask: 1,
		delta: [10 * PX, 10 * PX],
	},
	{
		// Convex inside corner (NW): two walls, push NW. Both axes clip toward 0.
		name: "diagonal_into_corner_nw",
		tiles: [
			{ gx: -1, gy: 0, edge_collisions: EDGE_E, collision_layer_mask: 1 },
			{ gx: 0, gy: -1, edge_collisions: EDGE_S, collision_layer_mask: 1 },
		],
		aabb: [10 * PX, 10 * PX, 15 * PX, 13 * PX],
		mask: 1,
		delta: [-50 * PX, -50 * PX],
	},
	{
		// Walls on opposite sides; move east → blocked by east wall, but
		// y-axis untouched (no south wall in delta path).
		name: "channel_walks_east_blocked",
		tiles: [
			{ gx: 1, gy: 0, edge_collisions: EDGE_W, collision_layer_mask: 1 },
			{ gx: -1, gy: 0, edge_collisions: EDGE_E, collision_layer_mask: 1 },
		],
		aabb: [0, PX, 5 * PX, 4 * PX],
		mask: 1,
		delta: [10 * PX, 0],
	},

	// --- 22-25: collision masks ---
	{
		// Tile is on layer 2; entity walks on layer 1 → invisible.
		name: "mask_mismatch_passes_through",
		tiles: [{ gx: 1, gy: 0, edge_collisions: EDGE_W, collision_layer_mask: 2 }],
		aabb: [0, PX, 5 * PX, 4 * PX],
		mask: 1,
		delta: [40 * PX, 0],
	},
	{
		// Tile is on layers 1|4=5; entity on layer 4 → blocks.
		name: "mask_overlap_blocks",
		tiles: [{ gx: 1, gy: 0, edge_collisions: EDGE_W, collision_layer_mask: 5 }],
		aabb: [0, PX, 5 * PX, 4 * PX],
		mask: 4,
		delta: [40 * PX, 0],
	},
	{
		// Two stacked tiles, only one in the entity's layer.
		name: "mask_filters_one_of_two",
		tiles: [
			{ gx: 1, gy: 0, edge_collisions: EDGE_W, collision_layer_mask: 1 },
			{ gx: 2, gy: 0, edge_collisions: EDGE_W, collision_layer_mask: 2 },
		],
		aabb: [0, PX, 5 * PX, 4 * PX],
		mask: 2, // first tile invisible to us, second blocks
		delta: [80 * PX, 0],
	},
	{
		// Empty mask = nothing collides.
		name: "ghost_entity_passes_everything",
		tiles: [{ gx: 1, gy: 0, edge_collisions: EDGE_W, collision_layer_mask: 0xff },
			{ gx: 0, gy: 1, edge_collisions: EDGE_N, collision_layer_mask: 0xff }],
		aabb: [0, 0, 5 * PX, 4 * PX],
		mask: 0,
		delta: [50 * PX, 50 * PX],
	},

	// --- 26-29: edge-bit selectivity ---
	{
		// Tile present, but only its EAST edge is set. Walking east approaches
		// the tile's WEST edge — should NOT block.
		name: "edge_bit_selectivity_east_only",
		tiles: [{ gx: 1, gy: 0, edge_collisions: EDGE_E, collision_layer_mask: 1 }],
		aabb: [0, PX, 5 * PX, 4 * PX],
		mask: 1,
		delta: [40 * PX, 0],
	},
	{
		// Tile with all edge bits acts like a fully solid blocker on every side.
		name: "solid_tile_blocks_east_approach",
		tiles: [{ gx: 1, gy: 0, edge_collisions: EDGE_N | EDGE_E | EDGE_S | EDGE_W, collision_layer_mask: 1 }],
		aabb: [0, PX, 5 * PX, 4 * PX],
		mask: 1,
		delta: [40 * PX, 0],
	},
	{
		// Walking south into a tile that only has its NORTH edge bit → blocks.
		name: "north_edge_blocks_southbound",
		tiles: [{ gx: 0, gy: 1, edge_collisions: EDGE_N, collision_layer_mask: 1 }],
		aabb: [PX, 0, 6 * PX, 4 * PX],
		mask: 1,
		delta: [0, 30 * PX],
	},
	{
		// Walking south into a tile that only has its SOUTH edge bit → does NOT
		// block (the FACING edge is N).
		name: "south_only_does_not_block_southbound",
		tiles: [{ gx: 0, gy: 1, edge_collisions: EDGE_S, collision_layer_mask: 1 }],
		aabb: [PX, 0, 6 * PX, 4 * PX],
		mask: 1,
		delta: [0, 30 * PX],
	},

	// --- 30-32: half-blockers and edge cases ---
	{
		// "Half-east" interpretation in v1: per the plan, presets expand to
		// edge bits at load. For the test here we model a half-blocker as a
		// regular tile occupying its lower 16 px region. That's the *server's*
		// job in real play; here we just verify the algorithm handles a tile
		// whose collision shape was already resolved to "Solid" with mask 1
		// at a sub-grid position. (We model by placing the blocker tile at
		// gy=1 — i.e., the "lower half" semantic is encoded by tile placement
		// in v1 authored content.)
		name: "lower_half_blocker_below",
		tiles: [{ gx: 0, gy: 1, edge_collisions: EDGE_N, collision_layer_mask: 1 }],
		aabb: [PX, 4 * PX, 6 * PX, 8 * PX], // sitting flush against the half blocker
		mask: 1,
		delta: [0, 100],                     // try to nudge south by 100 sub-px
	},
	{
		// Idempotent zero-motion under heavy clutter.
		name: "zero_motion_under_clutter",
		tiles: [
			{ gx: 1, gy: 0, edge_collisions: EDGE_W, collision_layer_mask: 1 },
			{ gx: 0, gy: 1, edge_collisions: EDGE_N, collision_layer_mask: 1 },
			{ gx: -1, gy: 0, edge_collisions: EDGE_E, collision_layer_mask: 1 },
		],
		aabb: [PX, PX, 6 * PX, 4 * PX],
		mask: 1,
		delta: [0, 0],
	},
	{
		// Negative-only-axis with X blocked.
		name: "x_blocked_y_negative",
		tiles: [{ gx: -1, gy: 0, edge_collisions: EDGE_E, collision_layer_mask: 1 }],
		aabb: [4 * PX, 5 * PX, 9 * PX, 8 * PX],
		mask: 1,
		delta: [-50 * PX, -2 * PX],
	},
];

const out = {
	$schema_version: 1,
	description:
		"Cross-runtime swept-AABB collision test vectors. Server (Go), web (TS), and at v1.1 iOS (Swift) must produce byte-identical resolved positions for every entry. See schemas/collision.md for the canonical pseudocode. All coordinates are in fixed-point sub-pixel units (1 px = 256 units).",
	vectors: scenarios.map((s) => {
		const e = entity(s.aabb, s.mask);
		const r = move(e, s.delta[0], s.delta[1], buildWorld(s.tiles));
		return {
			name: s.name,
			world: { tiles: s.tiles },
			entity: { aabb: s.aabb, mask: s.mask },
			delta: s.delta,
			expected_resolved_delta: [r.resolvedDx, r.resolvedDy],
		};
	}),
};

console.log(JSON.stringify(out, null, 2));
