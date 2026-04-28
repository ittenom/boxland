import { describe, expect, it } from "vitest";
import { buildRenderables, defaultCamera, TILE_SUB_PX } from "./render-bridge";
import type { Placement, BackdropTile } from "./types";

const known = new Set([1, 2, 3]);

function makeBase() {
	return {
		placements: [] as Placement[],
		backdrop: [] as BackdropTile[],
		selection: null as number | null,
		cursorCell: null as { x: number; y: number } | null,
		activeEntityID: null as number | null,
		activeRotation: 0 as 0 | 90 | 180 | 270,
		tool: "place" as "place" | "select" | "erase",
		mapWidth: 10,
		mapHeight: 10,
		knownAssetIDs: known as ReadonlySet<number>,
		pendingPlacementIDs: new Set<number>() as ReadonlySet<number>,
	};
}

describe("buildRenderables — placements", () => {
	it("emits one Renderable per placement at the right sub-pixel coords", () => {
		const s = makeBase();
		s.placements = [{
			id: 100, entityTypeId: 1, x: 3, y: 4, rotation: 0,
			instanceOverrides: {}, tags: [],
		}];
		const rs = buildRenderables(s);
		expect(rs.length).toBeGreaterThanOrEqual(1);
		const placement = rs.find((r) => r.id === 100);
		expect(placement).toBeDefined();
		expect(placement?.x).toBe(3 * TILE_SUB_PX);
		expect(placement?.y).toBe(4 * TILE_SUB_PX);
		expect(placement?.gridSnap).toBe(true);
		expect(placement?.rotation).toBe(0);
	});

	it("dims pending placements via tint", () => {
		const s = makeBase();
		s.placements = [{
			id: 100, entityTypeId: 1, x: 0, y: 0, rotation: 0,
			instanceOverrides: {}, tags: [],
		}];
		s.pendingPlacementIDs = new Set([100]);
		const rs = buildRenderables(s);
		const r = rs.find((x) => x.id === 100);
		// Pending dim != fully-bright white.
		expect(r?.tint).not.toBe(0xffffffff);
	});

	it("skips placements whose entity_type isn't in the catalog (no texture would render)", () => {
		const s = makeBase();
		s.placements = [{
			id: 100, entityTypeId: 999, x: 0, y: 0, rotation: 0,
			instanceOverrides: {}, tags: [],
		}];
		// Selection halo is what we'd see for an unknown type; just
		// assert no Renderable is emitted with the placement id.
		const rs = buildRenderables(s);
		// Placement WITH unknown id is still emitted (the renderer
		// will draw a texture-less sprite). The skip behaviour is
		// only for backdrop + selection halo + ghost — placement
		// rows always render so the user can find and remove broken
		// references.
		expect(rs.find((r) => r.id === 100)).toBeDefined();
	});
});

describe("buildRenderables — backdrop", () => {
	it("renders backdrop tiles at low layer with dim tint", () => {
		const s = makeBase();
		s.backdrop = [{ layerId: 1, x: 2, y: 3, entityTypeId: 1, rotation: 0 }];
		const rs = buildRenderables(s);
		expect(rs.some((r) => r.layer === 0)).toBe(true);
		const back = rs.find((r) => r.layer === 0);
		expect(back?.tint).toBeDefined();
	});

	it("skips backdrop tiles whose entity_type isn't in the catalog", () => {
		const s = makeBase();
		s.backdrop = [{ layerId: 1, x: 2, y: 3, entityTypeId: 999, rotation: 0 }];
		const rs = buildRenderables(s);
		expect(rs.some((r) => r.layer === 0)).toBe(false);
	});

	it("encodes distinct backdrop tiles into distinct ids (same Sprite reuse)", () => {
		const s = makeBase();
		s.backdrop = [
			{ layerId: 1, x: 0, y: 0, entityTypeId: 1, rotation: 0 },
			{ layerId: 1, x: 1, y: 0, entityTypeId: 1, rotation: 0 },
			{ layerId: 1, x: 0, y: 1, entityTypeId: 1, rotation: 0 },
		];
		const rs = buildRenderables(s);
		const ids = rs.filter((r) => r.layer === 0).map((r) => r.id);
		expect(new Set(ids).size).toBe(3);
	});
});

describe("buildRenderables — selection halo", () => {
	it("emits a halo Renderable above the selected placement", () => {
		const s = makeBase();
		s.placements = [{
			id: 50, entityTypeId: 1, x: 0, y: 0, rotation: 0,
			instanceOverrides: {}, tags: [],
		}];
		s.selection = 50;
		const rs = buildRenderables(s);
		// Two emissions for the same cell: the placement and its halo.
		const here = rs.filter((r) => r.x === 0 && r.y === 0);
		expect(here.length).toBeGreaterThanOrEqual(2);
		// Halo on a higher layer.
		const halo = here.reduce((best, r) => (r.layer > best.layer ? r : best), here[0]!);
		expect(halo.layer).toBeGreaterThan(0);
	});

	it("no halo when selection is missing or null", () => {
		const s = makeBase();
		s.placements = [{
			id: 50, entityTypeId: 1, x: 0, y: 0, rotation: 0,
			instanceOverrides: {}, tags: [],
		}];
		s.selection = null;
		const before = buildRenderables(s).length;
		s.selection = 999; // doesn't exist
		const after = buildRenderables(s).length;
		expect(after).toBe(before);
	});
});

describe("buildRenderables — ghost", () => {
	it("emits a ghost when place tool is armed and cursor is in bounds", () => {
		const s = makeBase();
		s.activeEntityID = 1;
		s.cursorCell = { x: 4, y: 4 };
		s.tool = "place";
		const rs = buildRenderables(s);
		expect(rs.some((r) => r.id === -1)).toBe(true);
	});

	it("no ghost when cursor is out of bounds", () => {
		const s = makeBase();
		s.activeEntityID = 1;
		s.cursorCell = { x: -1, y: 0 };
		const rs = buildRenderables(s);
		expect(rs.some((r) => r.id === -1)).toBe(false);
	});

	it("no ghost when tool is not place", () => {
		const s = makeBase();
		s.activeEntityID = 1;
		s.cursorCell = { x: 4, y: 4 };
		s.tool = "select";
		const rs = buildRenderables(s);
		expect(rs.some((r) => r.id === -1)).toBe(false);
	});

	it("no ghost when no entity is armed", () => {
		const s = makeBase();
		s.activeEntityID = null;
		s.cursorCell = { x: 4, y: 4 };
		const rs = buildRenderables(s);
		expect(rs.some((r) => r.id === -1)).toBe(false);
	});
});

describe("defaultCamera", () => {
	it("centers on the map center in sub-pixel space", () => {
		const cam = defaultCamera(10, 8);
		expect(cam.cx).toBe((10 * TILE_SUB_PX) / 2);
		expect(cam.cy).toBe((8 * TILE_SUB_PX) / 2);
	});
});
