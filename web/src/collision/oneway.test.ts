// Web-side mirror of server/internal/sim/collision/oneway_test.go.
// Both runtimes must produce byte-identical resolved deltas for the
// one-way platform's foot-position rule.

import { describe, it, expect } from "vitest";

import { move } from "./move";
import {
	CollisionShape,
	EDGE_N,
	SUB_PER_PX,
	TILE_PX,
	TILE_SIZE_SUB,
	edgesForShape,
	type Entity,
	type Tile,
	type World,
} from "./types";

function oneWayWorldAt(gx: number, gy: number): World {
	const tile: Tile = {
		gx, gy,
		edge_collisions: edgesForShape(CollisionShape.OneWayN),
		collision_layer_mask: 1,
		shape: CollisionShape.OneWayN,
	};
	return {
		get(x, y) {
			return x === gx && y === gy ? tile : undefined;
		},
	};
}

function entityAt(px: number, py: number): Entity {
	return {
		aabb: {
			left: px,
			top: py,
			right: px + TILE_PX * SUB_PER_PX,
			bottom: py + TILE_PX * SUB_PER_PX,
		},
		mask: 1,
	};
}

describe("collision: one-way platforms", () => {
	it("blocks downward motion when foot starts at or above the tile top", () => {
		const world = oneWayWorldAt(0, 1);
		const tileTop = TILE_SIZE_SUB;
		const e = entityAt(0, 0); // bottom = TILE_SIZE_SUB = tileTop
		const r = move(e, 0, 200, world);
		expect(r.resolvedDy).toBe(0);
		expect(e.aabb.bottom).toBe(tileTop);
	});

	it("passes through when jumping up from below", () => {
		const world = oneWayWorldAt(0, 1);
		const e = entityAt(0, 2 * TILE_SIZE_SUB);
		const r = move(e, 0, -1000, world);
		expect(r.resolvedDy).toBe(-1000);
	});

	it("passes through when foot already overlaps the tile (entered from the side)", () => {
		const world = oneWayWorldAt(0, 1);
		const tileTop = TILE_SIZE_SUB;
		// Place foot 100 sub-px below the tile top.
		const e = entityAt(0, tileTop - TILE_PX * SUB_PER_PX + 100);
		const r = move(e, 0, 200, world);
		expect(r.resolvedDy).toBe(200);
	});

	it("does not block sideways motion through the same row", () => {
		const world = oneWayWorldAt(2, 1);
		const e = entityAt(0, TILE_SIZE_SUB);
		const r = move(e, 5000, 0, world);
		expect(r.resolvedDx).toBe(5000);
	});
});

describe("edgesForShape", () => {
	it("OneWayN expands to EDGE_N only", () => {
		expect(edgesForShape(CollisionShape.OneWayN)).toBe(EDGE_N);
	});
});
