import { describe, expect, it, vi } from "vitest";
import { EditorState } from "./state";
import { handlePointerDown, handlePointerMove, handlePointerUp, rotate } from "./tools";
import type { LevelOps } from "./ops";
import type { PaletteAtlasEntry } from "./types";

const opts = { mapWidth: 10, mapHeight: 10 };

function makeOpsStub(): LevelOps & { calls: Array<{ kind: string; args: unknown[] }> } {
	const calls: Array<{ kind: string; args: unknown[] }> = [];
	const stub = {
		place: vi.fn(async (...args: unknown[]) => { calls.push({ kind: "place", args }); }),
		patch: vi.fn(async (...args: unknown[]) => { calls.push({ kind: "patch", args }); }),
		remove: vi.fn(async (...args: unknown[]) => { calls.push({ kind: "remove", args }); }),
	} as unknown as LevelOps & { calls: typeof calls };
	stub.calls = calls;
	return stub;
}

const palEntry: PaletteAtlasEntry = {
	id: 7, name: "spawn", class: "logic",
	sprite_url: "/x.png", atlas_index: 0, atlas_cols: 1, tile_size: 32,
};

describe("handlePointerDown — place tool", () => {
	it("places when in bounds and an entity is armed", () => {
		const s = new EditorState(opts);
		s.addPaletteEntries([palEntry]);
		s.setActiveEntity(palEntry);
		const ops = makeOpsStub();
		const drag = handlePointerDown(s, ops, { button: 0, cell: { x: 3, y: 4 }, spaceDown: false });
		expect(drag).toBeNull();
		expect(ops.calls).toEqual([{ kind: "place", args: [3, 4] }]);
	});

	it("ignores out-of-bounds clicks", () => {
		const s = new EditorState(opts);
		s.setActiveEntity(palEntry);
		const ops = makeOpsStub();
		const drag = handlePointerDown(s, ops, { button: 0, cell: { x: -1, y: 4 }, spaceDown: false });
		expect(drag).toBeNull();
		expect(ops.calls).toHaveLength(0);
	});

	it("right-click is a quick erase regardless of tool", () => {
		const s = new EditorState(opts);
		s.upsertPlacement({ id: 1, entityTypeId: 7, x: 2, y: 2, rotation: 0, instanceOverrides: {}, tags: [] });
		const ops = makeOpsStub();
		handlePointerDown(s, ops, { button: 2, cell: { x: 2, y: 2 }, spaceDown: false });
		expect(ops.calls).toEqual([{ kind: "remove", args: [1] }]);
	});
});

describe("handlePointerDown — select tool", () => {
	it("selects the top placement at the cell and returns a move-drag handle", () => {
		const s = new EditorState(opts);
		s.upsertPlacement({ id: 1, entityTypeId: 7, x: 2, y: 2, rotation: 0, instanceOverrides: {}, tags: [] });
		s.upsertPlacement({ id: 2, entityTypeId: 7, x: 2, y: 2, rotation: 0, instanceOverrides: {}, tags: [] });
		s.setTool("select");
		const ops = makeOpsStub();
		const drag = handlePointerDown(s, ops, { button: 0, cell: { x: 2, y: 2 }, spaceDown: false });
		expect(drag).not.toBeNull();
		expect(drag?.kind).toBe("move-selection");
		expect(s.selection).toBe(2); // newest first
	});

	it("cycles through stacked placements on repeated clicks", () => {
		const s = new EditorState(opts);
		s.upsertPlacement({ id: 1, entityTypeId: 7, x: 2, y: 2, rotation: 0, instanceOverrides: {}, tags: [] });
		s.upsertPlacement({ id: 2, entityTypeId: 7, x: 2, y: 2, rotation: 0, instanceOverrides: {}, tags: [] });
		s.upsertPlacement({ id: 3, entityTypeId: 7, x: 2, y: 2, rotation: 0, instanceOverrides: {}, tags: [] });
		s.setTool("select");
		const ops = makeOpsStub();
		// Stack newest-first: [3, 2, 1]. First click -> 3, second -> 2, third -> 1.
		handlePointerDown(s, ops, { button: 0, cell: { x: 2, y: 2 }, spaceDown: false });
		expect(s.selection).toBe(3);
		handlePointerDown(s, ops, { button: 0, cell: { x: 2, y: 2 }, spaceDown: false });
		expect(s.selection).toBe(2);
		handlePointerDown(s, ops, { button: 0, cell: { x: 2, y: 2 }, spaceDown: false });
		expect(s.selection).toBe(1);
		handlePointerDown(s, ops, { button: 0, cell: { x: 2, y: 2 }, spaceDown: false });
		expect(s.selection).toBe(3); // cycle
	});

	it("clearing selection by clicking an empty cell", () => {
		const s = new EditorState(opts);
		s.upsertPlacement({ id: 1, entityTypeId: 7, x: 2, y: 2, rotation: 0, instanceOverrides: {}, tags: [] });
		s.setTool("select");
		s.setSelection(1);
		const ops = makeOpsStub();
		handlePointerDown(s, ops, { button: 0, cell: { x: 9, y: 9 }, spaceDown: false });
		expect(s.selection).toBeNull();
	});
});

describe("handlePointerDown — erase tool", () => {
	it("removes the top placement at the cell", () => {
		const s = new EditorState(opts);
		s.upsertPlacement({ id: 1, entityTypeId: 7, x: 5, y: 5, rotation: 0, instanceOverrides: {}, tags: [] });
		s.setTool("erase");
		const ops = makeOpsStub();
		handlePointerDown(s, ops, { button: 0, cell: { x: 5, y: 5 }, spaceDown: false });
		expect(ops.calls).toEqual([{ kind: "remove", args: [1] }]);
	});

	it("no-op on an empty cell", () => {
		const s = new EditorState(opts);
		s.setTool("erase");
		const ops = makeOpsStub();
		handlePointerDown(s, ops, { button: 0, cell: { x: 0, y: 0 }, spaceDown: false });
		expect(ops.calls).toHaveLength(0);
	});
});

describe("handlePointerMove + handlePointerUp", () => {
	it("live-moves the selected placement during drag, then PATCHes once on up", () => {
		const s = new EditorState(opts);
		s.upsertPlacement({ id: 1, entityTypeId: 7, x: 0, y: 0, rotation: 0, instanceOverrides: {}, tags: [] });
		s.setTool("select");
		const ops = makeOpsStub();
		const drag = handlePointerDown(s, ops, { button: 0, cell: { x: 0, y: 0 }, spaceDown: false });
		// Drag through several cells.
		handlePointerMove(s, drag, { x: 1, y: 0 });
		handlePointerMove(s, drag, { x: 2, y: 0 });
		handlePointerMove(s, drag, { x: 3, y: 5 });
		expect(s.placement(1)?.x).toBe(3);
		expect(s.placement(1)?.y).toBe(5);
		// No patch fired during drag.
		expect(ops.calls.filter((c) => c.kind === "patch")).toHaveLength(0);

		handlePointerUp(s, ops, drag);
		// Exactly one PATCH on release.
		const patches = ops.calls.filter((c) => c.kind === "patch");
		expect(patches).toHaveLength(1);
		expect(patches[0]?.args).toEqual([1, { x: 3, y: 5 }]);
	});

	it("doesn't PATCH on release if the placement didn't move", () => {
		const s = new EditorState(opts);
		s.upsertPlacement({ id: 1, entityTypeId: 7, x: 0, y: 0, rotation: 0, instanceOverrides: {}, tags: [] });
		s.setTool("select");
		const ops = makeOpsStub();
		const drag = handlePointerDown(s, ops, { button: 0, cell: { x: 0, y: 0 }, spaceDown: false });
		handlePointerUp(s, ops, drag);
		expect(ops.calls.filter((c) => c.kind === "patch")).toHaveLength(0);
	});
});

describe("rotate", () => {
	it("rotates the selected placement when in select mode", () => {
		const s = new EditorState(opts);
		s.upsertPlacement({ id: 1, entityTypeId: 7, x: 0, y: 0, rotation: 0, instanceOverrides: {}, tags: [] });
		s.setTool("select");
		s.setSelection(1);
		const ops = makeOpsStub();
		rotate(s, ops);
		expect(ops.calls).toEqual([{ kind: "patch", args: [1, { rotation: 90 }] }]);
	});

	it("rotates the active-entity ghost when no placement is selected", () => {
		const s = new EditorState(opts);
		const ops = makeOpsStub();
		rotate(s, ops);
		expect(s.activeRotation).toBe(90);
		rotate(s, ops);
		expect(s.activeRotation).toBe(180);
		rotate(s, ops);
		rotate(s, ops);
		expect(s.activeRotation).toBe(0); // wraps
		expect(ops.calls).toHaveLength(0);
	});
});
