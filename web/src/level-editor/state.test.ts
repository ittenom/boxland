import { describe, expect, it, vi } from "vitest";
import { EditorState } from "./state";
import type { PaletteAtlasEntry, Placement } from "./types";

const opts = { mapWidth: 10, mapHeight: 10 };

function p(id: number, x = 0, y = 0): Placement {
	return { id, entityTypeId: 1, x, y, rotation: 0, instanceOverrides: {}, tags: [] };
}

describe("EditorState placements", () => {
	it("upsert adds a new placement and notifies listeners", () => {
		const s = new EditorState(opts);
		const cb = vi.fn();
		s.subscribe(cb);
		s.upsertPlacement(p(1));
		expect(s.allPlacements()).toHaveLength(1);
		expect(cb).toHaveBeenCalled();
	});

	it("upsert replaces an existing placement", () => {
		const s = new EditorState(opts);
		s.upsertPlacement(p(1, 0, 0));
		s.upsertPlacement(p(1, 5, 5));
		expect(s.allPlacements()).toHaveLength(1);
		expect(s.placement(1)?.x).toBe(5);
	});

	it("remove drops the placement and clears selection if it was selected", () => {
		const s = new EditorState(opts);
		s.upsertPlacement(p(1));
		s.upsertPlacement(p(2));
		s.setSelection(1);
		s.removePlacement(1);
		expect(s.placement(1)).toBeNull();
		expect(s.selection).toBeNull();
		expect(s.placement(2)).not.toBeNull();
	});

	it("patch leaves entity_type_id and id immutable", () => {
		const s = new EditorState(opts);
		s.upsertPlacement(p(1));
		s.patchPlacement(1, { x: 4, y: 5, rotation: 90 });
		const cur = s.placement(1);
		expect(cur).toMatchObject({ id: 1, entityTypeId: 1, x: 4, y: 5, rotation: 90 });
	});
});

describe("EditorState stacking", () => {
	it("stackedAt returns every placement at the cell, newest first", () => {
		const s = new EditorState(opts);
		s.upsertPlacement(p(1, 3, 4));
		s.upsertPlacement(p(2, 3, 4));
		s.upsertPlacement(p(3, 3, 4));
		s.upsertPlacement(p(4, 9, 9));
		const stack = s.stackedAt({ x: 3, y: 4 });
		expect(stack.map((q) => q.id)).toEqual([3, 2, 1]);
	});

	it("topPlacementAt returns the selection when it's at this cell", () => {
		const s = new EditorState(opts);
		s.upsertPlacement(p(1, 3, 4));
		s.upsertPlacement(p(2, 3, 4));
		s.upsertPlacement(p(3, 3, 4));
		s.setSelection(1);
		expect(s.topPlacementAt({ x: 3, y: 4 })?.id).toBe(1);
	});

	it("topPlacementAt picks newest when nothing relevant is selected", () => {
		const s = new EditorState(opts);
		s.upsertPlacement(p(1, 3, 4));
		s.upsertPlacement(p(2, 3, 4));
		expect(s.topPlacementAt({ x: 3, y: 4 })?.id).toBe(2);
	});

	it("topPlacementAt returns null on an empty cell", () => {
		const s = new EditorState(opts);
		expect(s.topPlacementAt({ x: 0, y: 0 })).toBeNull();
	});
});

describe("EditorState bounds", () => {
	it("inBounds rejects negative cells", () => {
		const s = new EditorState(opts);
		expect(s.inBounds({ x: -1, y: 0 })).toBe(false);
		expect(s.inBounds({ x: 0, y: -1 })).toBe(false);
	});

	it("inBounds rejects cells past the map dims", () => {
		const s = new EditorState(opts);
		expect(s.inBounds({ x: 10, y: 0 })).toBe(false);
		expect(s.inBounds({ x: 0, y: 10 })).toBe(false);
	});

	it("inBounds accepts cells inside the map", () => {
		const s = new EditorState(opts);
		expect(s.inBounds({ x: 0, y: 0 })).toBe(true);
		expect(s.inBounds({ x: 9, y: 9 })).toBe(true);
	});
});

describe("EditorState undo/redo history", () => {
	it("pushes onto the undo stack and clears redo", async () => {
		const s = new EditorState(opts);
		const undo1 = vi.fn();
		const redo1 = vi.fn();
		s.pushHistory({ undo: undo1, redo: redo1 });
		expect(s.canUndo()).toBe(true);
		expect(s.canRedo()).toBe(false);
		// Pushing a new entry after undo clears redo.
		await s.undo();
		expect(s.canRedo()).toBe(true);
		s.pushHistory({ undo: vi.fn(), redo: vi.fn() });
		expect(s.canRedo()).toBe(false);
	});

	it("undo runs the undo fn exactly once and moves entry to redo stack", async () => {
		const s = new EditorState(opts);
		const undo = vi.fn();
		const redo = vi.fn();
		s.pushHistory({ undo, redo });
		await s.undo();
		expect(undo).toHaveBeenCalledTimes(1);
		expect(redo).not.toHaveBeenCalled();
		expect(s.canUndo()).toBe(false);
		expect(s.canRedo()).toBe(true);
	});

	it("redo runs redo fn and moves the entry back to undo stack", async () => {
		const s = new EditorState(opts);
		const undo = vi.fn();
		const redo = vi.fn();
		s.pushHistory({ undo, redo });
		await s.undo();
		await s.redo();
		expect(redo).toHaveBeenCalledTimes(1);
		expect(s.canUndo()).toBe(true);
		expect(s.canRedo()).toBe(false);
	});

	it("caps history at 100 entries", () => {
		const s = new EditorState(opts);
		for (let i = 0; i < 150; i++) {
			s.pushHistory({ undo: vi.fn(), redo: vi.fn() });
		}
		// We can't peek the stack length directly, but we can drain
		// it via undo() and assert the count.
		let count = 0;
		while (s.canUndo() && count < 200) {
			void s.undo();
			count++;
		}
		expect(count).toBe(100);
	});
});

describe("EditorState pending counter", () => {
	it("clamps at zero", () => {
		const s = new EditorState(opts);
		s.endPending();
		s.endPending();
		expect(s.pending).toBe(0);
		s.beginPending();
		s.beginPending();
		s.endPending();
		expect(s.pending).toBe(1);
	});
});

describe("EditorState palette", () => {
	it("addPaletteEntries upserts by id and is idempotent", () => {
		const s = new EditorState(opts);
		const e: PaletteAtlasEntry = {
			id: 1, name: "spawn", class: "logic",
			sprite_url: "/x.png", atlas_index: 0, atlas_cols: 1, tile_size: 32,
		};
		s.addPaletteEntries([e, e]);
		expect(s.allPaletteEntries()).toHaveLength(1);
		expect(s.paletteEntry(1)?.name).toBe("spawn");
	});
});
