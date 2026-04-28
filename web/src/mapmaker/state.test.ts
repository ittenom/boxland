import { describe, expect, it, vi } from "vitest";
import { MapmakerState, newStrokeCtx } from "./state";
import { tileKey, type MapTile } from "./types";

const opts = { mapWidth: 10, mapHeight: 10, defaultLayerId: 1 };

function tile(layer: number, x: number, y: number, ent = 7, rot: 0|90|180|270 = 0): MapTile {
	return { layerId: layer, x, y, entityTypeId: ent, rotation: rot };
}

describe("MapmakerState basics", () => {
	it("starts with the default layer active", () => {
		const s = new MapmakerState(opts);
		expect(s.activeLayer).toBe(1);
	});

	it("upsert + delete + lookup roundtrip", () => {
		const s = new MapmakerState(opts);
		s.upsertTile(tile(1, 3, 4));
		expect(s.tileAt(1, 3, 4)?.entityTypeId).toBe(7);
		s.deleteTile(1, 3, 4);
		expect(s.tileAt(1, 3, 4)).toBeNull();
	});

	it("upsert lock parallel to tile (locks are a separate set)", () => {
		const s = new MapmakerState(opts);
		s.upsertTile(tile(1, 3, 4));
		s.upsertLock(tile(1, 3, 4));
		expect(s.tileCount()).toBe(1);
		expect(s.lockCount()).toBe(1);
		s.deleteTile(1, 3, 4);
		// Lock survives until explicitly deleted.
		expect(s.lockCount()).toBe(1);
	});

	it("inBounds rejects out-of-map cells", () => {
		const s = new MapmakerState(opts);
		expect(s.inBounds({ x: -1, y: 0 })).toBe(false);
		expect(s.inBounds({ x: 0, y: 10 })).toBe(false);
		expect(s.inBounds({ x: 5, y: 5 })).toBe(true);
	});

	it("notifies subscribers on each mutation", () => {
		const s = new MapmakerState(opts);
		const cb = vi.fn();
		s.subscribe(cb);
		s.upsertTile(tile(1, 0, 0));
		s.setTool("rect");
		s.setActiveEntity(99);
		expect(cb.mock.calls.length).toBeGreaterThanOrEqual(3);
	});

	it("setLayers preserves activeLayer when it still exists", () => {
		const s = new MapmakerState(opts);
		s.setActiveLayer(2);
		s.setLayers([
			{ id: 1, name: "base", kind: "tile", yShift: 0, ySort: false },
			{ id: 2, name: "deco", kind: "tile", yShift: 1, ySort: false },
		]);
		expect(s.activeLayer).toBe(2);
	});

	it("setLayers picks a tile layer when active is wiped", () => {
		const s = new MapmakerState(opts);
		s.setActiveLayer(99);
		s.setLayers([
			{ id: 1, name: "base", kind: "tile", yShift: 0, ySort: false },
		]);
		expect(s.activeLayer).toBe(1);
	});
});

describe("MapmakerState stroke + history", () => {
	it("captures pre-image once per cell within a stroke", () => {
		const s = new MapmakerState(opts);
		const ctx = newStrokeCtx();
		s.upsertTile(tile(1, 3, 4, 7));
		s.capturePreImage(ctx, 1, 3, 4);
		s.upsertTile(tile(1, 3, 4, 99));
		s.capturePreImage(ctx, 1, 3, 4); // second touch — must not overwrite
		const k = tileKey({ layerId: 1, x: 3, y: 4 });
		expect(ctx.prevTiles.get(k)?.entityTypeId).toBe(7);
	});

	it("buildHistoryEntry diffs the current state against the captured pre-image", () => {
		const s = new MapmakerState(opts);
		const ctx = newStrokeCtx();
		s.upsertTile(tile(1, 3, 4, 7));
		s.capturePreImage(ctx, 1, 3, 4);
		s.upsertTile(tile(1, 3, 4, 99));
		const entry = s.buildHistoryEntry("brush", ctx);
		const k = tileKey({ layerId: 1, x: 3, y: 4 });
		expect(entry.beforeTiles.get(k)?.entityTypeId).toBe(7);
		expect(entry.afterTiles.get(k)?.entityTypeId).toBe(99);
	});

	it("undo/redo entry lifecycle", () => {
		const s = new MapmakerState(opts);
		const e = s.buildHistoryEntry("brush", newStrokeCtx());
		s.pushHistory(e);
		expect(s.canUndo()).toBe(true);
		expect(s.popUndoEntry()).toBe(e);
		expect(s.canRedo()).toBe(true);
		expect(s.popRedoEntry()).toBe(e);
		expect(s.canUndo()).toBe(true);
	});

	it("clearHistory empties both stacks", () => {
		const s = new MapmakerState(opts);
		s.pushHistory(s.buildHistoryEntry("a", newStrokeCtx()));
		s.pushHistory(s.buildHistoryEntry("b", newStrokeCtx()));
		s.clearHistory();
		expect(s.canUndo()).toBe(false);
		expect(s.canRedo()).toBe(false);
	});
});
