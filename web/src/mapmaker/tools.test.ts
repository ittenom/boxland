import { describe, expect, it } from "vitest";
import { MapmakerState, newStrokeCtx } from "./state";
import { stamp, stampRect, floodFill, applyHistorySide, groupByLayer } from "./tools";
import type { MapTile } from "./types";

const opts = { mapWidth: 10, mapHeight: 10, defaultLayerId: 1 };

function tile(layer: number, x: number, y: number, ent = 7, rot: 0|90|180|270 = 0): MapTile {
	return { layerId: layer, x, y, entityTypeId: ent, rotation: rot };
}

describe("stamp — brush", () => {
	it("places the active entity at the cell", () => {
		const s = new MapmakerState(opts);
		s.setActiveEntity(7);
		const ctx = newStrokeCtx();
		const out = stamp(s, ctx, { x: 3, y: 4 }, {});
		expect(out.placed).toEqual([tile(1, 3, 4, 7)]);
		expect(s.tileAt(1, 3, 4)?.entityTypeId).toBe(7);
	});

	it("no-op when no entity is armed", () => {
		const s = new MapmakerState(opts);
		const ctx = newStrokeCtx();
		const out = stamp(s, ctx, { x: 0, y: 0 }, {});
		expect(out.placed).toHaveLength(0);
		expect(s.tileCount()).toBe(0);
	});

	it("respects rotation", () => {
		const s = new MapmakerState(opts);
		s.setActiveEntity(7);
		s.setActiveRotation(180);
		stamp(s, newStrokeCtx(), { x: 0, y: 0 }, {});
		expect(s.tileAt(1, 0, 0)?.rotation).toBe(180);
	});

	it("ignores out-of-bounds cells", () => {
		const s = new MapmakerState(opts);
		s.setActiveEntity(7);
		const out = stamp(s, newStrokeCtx(), { x: -1, y: 0 }, {});
		expect(out.placed).toHaveLength(0);
	});
});

describe("stamp — eraser", () => {
	it("removes the tile at the cell on the active layer", () => {
		const s = new MapmakerState(opts);
		s.upsertTile(tile(1, 2, 2));
		s.setTool("eraser");
		const out = stamp(s, newStrokeCtx(), { x: 2, y: 2 }, {});
		expect(out.erased).toHaveLength(1);
		expect(s.tileAt(1, 2, 2)).toBeNull();
	});

	it("no-op when the cell is empty", () => {
		const s = new MapmakerState(opts);
		s.setTool("eraser");
		const out = stamp(s, newStrokeCtx(), { x: 0, y: 0 }, {});
		expect(out.erased).toHaveLength(0);
	});
});

describe("stamp — eyedrop", () => {
	it("copies the entity_type + rotation into active state", () => {
		const s = new MapmakerState(opts);
		s.upsertTile(tile(1, 2, 2, 99, 90));
		s.setTool("eyedrop");
		const out = stamp(s, newStrokeCtx(), { x: 2, y: 2 }, {});
		expect(out.placed).toHaveLength(0);
		expect(s.activeEntity).toBe(99);
		expect(s.activeRotation).toBe(90);
	});
});

describe("stamp — lock", () => {
	it("paint+lock places tile AND records lock", () => {
		const s = new MapmakerState(opts);
		s.setActiveEntity(7);
		s.setTool("lock");
		const out = stamp(s, newStrokeCtx(), { x: 1, y: 1 }, {});
		expect(out.placed).toHaveLength(1);
		expect(out.locked).toHaveLength(1);
		expect(s.tileCount()).toBe(1);
		expect(s.lockCount()).toBe(1);
	});

	it("shift+lock unlocks an existing lock without erasing the tile", () => {
		const s = new MapmakerState(opts);
		s.upsertTile(tile(1, 1, 1, 7));
		s.upsertLock(tile(1, 1, 1, 7));
		s.setTool("lock");
		const out = stamp(s, newStrokeCtx(), { x: 1, y: 1 }, { shift: true });
		expect(out.unlocked).toHaveLength(1);
		expect(out.erased).toHaveLength(0);
		expect(s.tileCount()).toBe(1);
		expect(s.lockCount()).toBe(0);
	});

	it("alt+lock unlocks AND erases the tile", () => {
		const s = new MapmakerState(opts);
		s.upsertTile(tile(1, 1, 1, 7));
		s.upsertLock(tile(1, 1, 1, 7));
		s.setTool("lock");
		const out = stamp(s, newStrokeCtx(), { x: 1, y: 1 }, { alt: true });
		expect(out.unlocked).toHaveLength(1);
		expect(out.erased).toHaveLength(1);
		expect(s.tileCount()).toBe(0);
		expect(s.lockCount()).toBe(0);
	});

	it("shift on a non-locked cell is a no-op", () => {
		const s = new MapmakerState(opts);
		s.setTool("lock");
		const out = stamp(s, newStrokeCtx(), { x: 1, y: 1 }, { shift: true });
		expect(out.unlocked).toHaveLength(0);
		expect(out.erased).toHaveLength(0);
	});
});

describe("stampRect", () => {
	it("stamps every cell in the rectangle", () => {
		const s = new MapmakerState(opts);
		s.setActiveEntity(7);
		const out = stampRect(s, newStrokeCtx(), { x: 1, y: 1 }, { x: 3, y: 2 });
		// 3x2 = 6 cells.
		expect(out.placed).toHaveLength(6);
		expect(s.tileCount()).toBe(6);
	});

	it("normalizes from/to so rect can be drawn in any direction", () => {
		const s = new MapmakerState(opts);
		s.setActiveEntity(7);
		const out = stampRect(s, newStrokeCtx(), { x: 3, y: 3 }, { x: 1, y: 1 });
		expect(out.placed).toHaveLength(9); // 3x3
	});
});

describe("floodFill", () => {
	it("fills empty cells with the active entity", () => {
		const s = new MapmakerState({ mapWidth: 5, mapHeight: 5, defaultLayerId: 1 });
		s.setActiveEntity(7);
		const out = floodFill(s, newStrokeCtx(), { x: 0, y: 0 });
		// 5x5 = 25 cells.
		expect(out.placed).toHaveLength(25);
	});

	it("only replaces matching cells (continuous region)", () => {
		const s = new MapmakerState({ mapWidth: 5, mapHeight: 5, defaultLayerId: 1 });
		// Wall at column 2.
		for (let y = 0; y < 5; y++) s.upsertTile(tile(1, 2, y, 99));
		s.setActiveEntity(7);
		const out = floodFill(s, newStrokeCtx(), { x: 0, y: 0 });
		// 2x5 = 10 cells filled (left of the wall).
		expect(out.placed).toHaveLength(10);
	});

	it("eraser tool clears matching contiguous tiles", () => {
		const s = new MapmakerState({ mapWidth: 5, mapHeight: 5, defaultLayerId: 1 });
		for (let y = 0; y < 5; y++) for (let x = 0; x < 5; x++) s.upsertTile(tile(1, x, y, 7));
		s.setTool("eraser");
		const out = floodFill(s, newStrokeCtx(), { x: 0, y: 0 });
		expect(out.erased).toHaveLength(25);
		expect(s.tileCount()).toBe(0);
	});

	it("safety-caps at 4096 cells", () => {
		// 100x100 map — flood fill of an empty region would touch
		// 10000 cells but caps at 4096.
		const s = new MapmakerState({ mapWidth: 100, mapHeight: 100, defaultLayerId: 1 });
		s.setActiveEntity(7);
		const out = floodFill(s, newStrokeCtx(), { x: 0, y: 0 });
		expect(out.placed.length).toBeLessThanOrEqual(4096);
	});
});

describe("applyHistorySide", () => {
	it("undo restores deleted tile and reports it for re-POST", () => {
		const s = new MapmakerState(opts);
		const ctx = newStrokeCtx();
		s.upsertTile(tile(1, 1, 1, 7));
		s.capturePreImage(ctx, 1, 1, 1);
		s.deleteTile(1, 1, 1);
		const entry = s.buildHistoryEntry("eraser", ctx);
		const diff = applyHistorySide(s, entry, "before");
		expect(s.tileAt(1, 1, 1)?.entityTypeId).toBe(7);
		expect(diff.placed).toHaveLength(1);
	});

	it("redo re-deletes and reports the erase for DELETE shipping", () => {
		const s = new MapmakerState(opts);
		const ctx = newStrokeCtx();
		s.upsertTile(tile(1, 1, 1, 7));
		s.capturePreImage(ctx, 1, 1, 1);
		s.deleteTile(1, 1, 1);
		const entry = s.buildHistoryEntry("eraser", ctx);
		// Undo first (state now has the tile back).
		applyHistorySide(s, entry, "before");
		// Redo deletes.
		const diff = applyHistorySide(s, entry, "after");
		expect(s.tileAt(1, 1, 1)).toBeNull();
		expect(diff.erased).toHaveLength(1);
	});
});

describe("groupByLayer", () => {
	it("groups erase points by layer for the bulk DELETE wire shape", () => {
		const grouped = groupByLayer([
			tile(1, 0, 0), tile(2, 1, 1), tile(1, 2, 2), tile(2, 3, 3),
		]);
		expect(grouped.get(1)).toHaveLength(2);
		expect(grouped.get(2)).toHaveLength(2);
	});
});
