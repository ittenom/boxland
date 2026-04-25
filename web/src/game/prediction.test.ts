import { describe, it, expect } from "vitest";

import {
	freshLocalState,
	predictStep,
	reconcile,
	resolveHost,
	HOST_SPEED_SUB_PER_MS,
	RECONCILE_HARD_SNAP_SUB,
} from "./prediction";
import { mailboxAsWorld } from "./world";
import { buildWorld, SUB_PER_PX, EDGE_E, EDGE_W, type World } from "@collision";
import type { CachedEntity } from "@net";

function emptyWorld(): World {
	return buildWorld([]);
}

function withTile(gx: number, gy: number, edges: number): World {
	return buildWorld([{
		gx, gy,
		edge_collisions: edges,
		collision_layer_mask: 0xff,
	}]);
}

function host(): CachedEntity {
	return {
		id: 7n, typeId: 1,
		x: 100 * SUB_PER_PX, y: 50 * SUB_PER_PX,
		facing: 0, animId: 0, animFrame: 0,
		variantId: 0, tint: 0, nameplate: "", hpPct: 255,
	};
}

describe("predictStep", () => {
	it("no-ops when hostId is unset", () => {
		const s = freshLocalState();
		s.intentVx = 1000;
		const next = predictStep(s, 100, emptyWorld());
		expect(next).toBe(s);
	});

	it("no-ops with zero intent", () => {
		const s = { ...freshLocalState(), hostId: 1n };
		const next = predictStep(s, 100, emptyWorld());
		expect(next).toBe(s);
	});

	it("moves the host by intent * speed * dt in open space", () => {
		const s = { ...freshLocalState(), hostId: 1n, hostX: 0, hostY: 0, intentVx: 1000, intentVy: 0 };
		const next = predictStep(s, 100, emptyWorld());
		const expected = ((1000 * HOST_SPEED_SUB_PER_MS * 100) / 1000) | 0;
		expect(next.hostX).toBe(expected);
		expect(next.hostY).toBe(0);
	});

	it("respects diagonal intent on both axes", () => {
		const s = { ...freshLocalState(), hostId: 1n, hostX: 0, hostY: 0, intentVx: 500, intentVy: -500 };
		const next = predictStep(s, 100, emptyWorld());
		expect(next.hostX).toBeGreaterThan(0);
		expect(next.hostY).toBeLessThan(0);
	});

	it("collides with a tile that has the facing edge", () => {
		// Host at world (0,0), 14px half-extents. Tile at (1,0) with EDGE_W blocks east.
		const s = {
			...freshLocalState(),
			hostId: 1n,
			hostX: 16 * SUB_PER_PX, // 16 px so right side is at 30 px (just shy of tile at 32)
			hostY: 16 * SUB_PER_PX,
			intentVx: 1000,
			intentVy: 0,
		};
		const w = withTile(1, 0, EDGE_W);
		const next = predictStep(s, 1000, w); // big dt to ensure we'd cross
		// Right side capped at gx=1 left edge = 32 px = 32*256.
		expect(next.hostX + 14 * SUB_PER_PX).toBeLessThanOrEqual(32 * SUB_PER_PX);
	});

	it("never mutates the input LocalState (purity check)", () => {
		const s = { ...freshLocalState(), hostId: 1n, hostX: 0, hostY: 0, intentVx: 1000, intentVy: 0 };
		const before = { ...s };
		predictStep(s, 100, emptyWorld());
		expect(s).toEqual(before);
	});
});

describe("reconcile", () => {
	it("soft-blends small drifts (halves the gap each tick)", () => {
		const s = { ...freshLocalState(), hostId: 7n, hostX: 100 * SUB_PER_PX, hostY: 50 * SUB_PER_PX };
		const server = { ...host(), x: 102 * SUB_PER_PX, y: 50 * SUB_PER_PX };
		const out = reconcile(s, server);
		expect(out.result.hardSnap).toBe(false);
		expect(out.state.hostX).toBe(101 * SUB_PER_PX); // halfway
		expect(out.state.hostY).toBe(50 * SUB_PER_PX);
	});

	it("hard-snaps when drift exceeds the rubber-band threshold", () => {
		const s = { ...freshLocalState(), hostId: 7n, hostX: 0, hostY: 0 };
		const server = { ...host(), x: RECONCILE_HARD_SNAP_SUB + 100, y: 0 };
		const out = reconcile(s, server);
		expect(out.result.hardSnap).toBe(true);
		expect(out.state.hostX).toBe(server.x);
	});

	it("zero drift is a no-op (soft path)", () => {
		const s = { ...freshLocalState(), hostId: 7n, hostX: 100, hostY: 100 };
		const server = { ...host(), x: 100, y: 100 };
		const out = reconcile(s, server);
		expect(out.result).toEqual({ deltaX: 0, deltaY: 0, hardSnap: false });
		expect(out.state.hostX).toBe(100);
		expect(out.state.hostY).toBe(100);
	});
});

describe("resolveHost", () => {
	it("returns undefined when hostId is 0", () => {
		const s = freshLocalState();
		expect(resolveHost(s, () => undefined)).toBeUndefined();
	});

	it("looks up the entity by hostId", () => {
		const s = { ...freshLocalState(), hostId: 7n };
		const e = host();
		const got = resolveHost(s, (id) => (id === 7n ? e : undefined));
		expect(got).toBe(e);
	});
});

describe("mailboxAsWorld", () => {
	it("ORs edges across overlapping layers", () => {
		const w = mailboxAsWorld({
			values: () => [
				{ layerId: 0, gx: 1, gy: 1, assetId: 0, frame: 0,
				  collisionShape: 0, edgeCollisions: EDGE_W, collisionLayerMask: 0x01 },
				{ layerId: 1, gx: 1, gy: 1, assetId: 0, frame: 0,
				  collisionShape: 0, edgeCollisions: EDGE_E, collisionLayerMask: 0x02 },
			][Symbol.iterator](),
		});
		const t = w.get(1, 1)!;
		expect(t.edge_collisions & EDGE_W).toBe(EDGE_W);
		expect(t.edge_collisions & EDGE_E).toBe(EDGE_E);
		expect(t.collision_layer_mask & 0x03).toBe(0x03);
	});

	it("returns undefined for empty cells", () => {
		const w = mailboxAsWorld({ values: () => ([] as never[])[Symbol.iterator]() });
		expect(w.get(0, 0)).toBeUndefined();
	});
});
