import { describe, expect, it } from "vitest";
import { buildRenderables, defaultCamera, TILE_SUB_PX, buildOverlayShapes } from "./render-bridge";
import type { LockedCell, MapTile } from "./types";

const known = new Set([1, 2, 3, 7]);

type TestRBState = {
	tiles: MapTile[];
	procPreview: MapTile[] | null;
	stampGhost: { entityID: number; rotation: 0|90|180|270 } | null;
	cursorCell: { x: number; y: number } | null;
	dragRectFrom: { x: number; y: number } | null;
	dragRectTo: { x: number; y: number } | null;
	sampleRect: { x: number; y: number; width: number; height: number } | null;
	locks: LockedCell[];
	tool: "brush" | "rect" | "fill" | "eyedrop" | "eraser" | "lock" | "sample";
	activeLayer: number;
	mapWidth: number;
	mapHeight: number;
};

function makeBase(): TestRBState {
	return {
		tiles: [],
		procPreview: null,
		stampGhost: null,
		cursorCell: null,
		dragRectFrom: null,
		dragRectTo: null,
		sampleRect: null,
		locks: [],
		tool: "brush",
		activeLayer: 1,
		mapWidth: 10,
		mapHeight: 10,
	};
}

function tile(layer: number, x: number, y: number, ent = 7, rot: 0|90|180|270 = 0): MapTile {
	return { layerId: layer, x, y, entityTypeId: ent, rotation: rot };
}

describe("buildRenderables — tiles", () => {
	it("emits one Renderable per tile", () => {
		const s = makeBase();
		s.tiles = [tile(1, 1, 1), tile(1, 2, 1), tile(2, 1, 1)];
		const rs = buildRenderables(s);
		expect(rs).toHaveLength(3);
	});

	it("converts cell coords to sub-pixels", () => {
		const s = makeBase();
		s.tiles = [tile(1, 3, 4)];
		const r = buildRenderables(s)[0]!;
		expect(r.x).toBe(3 * TILE_SUB_PX);
		expect(r.y).toBe(4 * TILE_SUB_PX);
	});

	it("preserves rotation and layer", () => {
		const s = makeBase();
		s.tiles = [tile(5, 0, 0, 7, 90)];
		const r = buildRenderables(s)[0]!;
		expect(r.rotation).toBe(90);
		expect(r.layer).toBe(5);
	});

	it("keeps tiles whose entity_type is not in the current palette", () => {
		const s = makeBase();
		s.tiles = [tile(1, 0, 0, 999)]; // 999 not in known set
		expect(buildRenderables(s)).toHaveLength(1);
		expect(buildRenderables(s)[0]?.asset_id).toBe(999);
	});
});

describe("buildRenderables — procedural ghost", () => {
	it("emits ghost tiles when procPreview is set", () => {
		const s = makeBase();
		s.procPreview = [tile(1, 0, 0)];
		const rs = buildRenderables(s);
		expect(rs).toHaveLength(1);
		// Tinted (not undefined) — that's the ghost cue.
		expect(rs[0]?.tint).toBeDefined();
	});

	it("no ghost when procPreview is null", () => {
		const s = makeBase();
		expect(buildRenderables(s)).toHaveLength(0);
	});
});

describe("buildRenderables — stamp ghost", () => {
	it("emits a stamp ghost when cursor is in bounds and tool is brush", () => {
		const s = makeBase();
		s.stampGhost = { entityID: 7, rotation: 0 };
		s.cursorCell = { x: 5, y: 5 };
		s.tool = "brush";
		const rs = buildRenderables(s);
		expect(rs.some((r) => r.id === -1)).toBe(true);
	});

	it("no stamp ghost for eyedrop / eraser tools", () => {
		const s = makeBase();
		s.stampGhost = { entityID: 7, rotation: 0 };
		s.cursorCell = { x: 5, y: 5 };
		s.tool = "eyedrop";
		expect(buildRenderables(s).some((r) => r.id === -1)).toBe(false);
		s.tool = "eraser";
		expect(buildRenderables(s).some((r) => r.id === -1)).toBe(false);
	});

	it("no stamp ghost when cursor is out of bounds", () => {
		const s = makeBase();
		s.stampGhost = { entityID: 7, rotation: 0 };
		s.cursorCell = { x: -1, y: 5 };
		expect(buildRenderables(s).some((r) => r.id === -1)).toBe(false);
	});
});

describe("buildRenderables — locks", () => {
	it("emits a tinted overlay Renderable for each lock", () => {
		const s = makeBase();
		s.locks = [tile(1, 0, 0)];
		const rs = buildRenderables(s);
		expect(rs).toHaveLength(1);
		expect(rs[0]?.tint).toBeDefined();
	});
});

describe("buildOverlayShapes", () => {
	it("returns dragRect when both endpoints are set", () => {
		const s = makeBase();
		s.dragRectFrom = { x: 0, y: 0 };
		s.dragRectTo = { x: 2, y: 2 };
		const shapes = buildOverlayShapes(s);
		expect(shapes.dragRect).toEqual({ from: { x: 0, y: 0 }, to: { x: 2, y: 2 } });
	});

	it("returns null when dragRect is partial", () => {
		const s = makeBase();
		s.dragRectFrom = { x: 0, y: 0 };
		expect(buildOverlayShapes(s).dragRect).toBeNull();
	});

	it("passes sampleRect through", () => {
		const s = makeBase();
		s.sampleRect = { x: 1, y: 1, width: 3, height: 2 };
		expect(buildOverlayShapes(s).sampleRect).toEqual({ x: 1, y: 1, width: 3, height: 2 });
	});
});

describe("defaultCamera", () => {
	it("centers on the map midpoint in sub-pixels", () => {
		const cam = defaultCamera(10, 8);
		expect(cam.cx).toBe((10 * TILE_SUB_PX) / 2);
		expect(cam.cy).toBe((8 * TILE_SUB_PX) / 2);
	});
});
