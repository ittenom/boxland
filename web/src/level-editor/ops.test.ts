import { describe, expect, it, vi } from "vitest";
import { EditorState } from "./state";
import { LevelOps } from "./ops";
import type { LevelEditorWire, PlaceRequest, PatchRequest } from "./wire";
import type { PaletteAtlasEntry, PlacementWire } from "./types";

const opts = { mapWidth: 10, mapHeight: 10 };

interface FakeWire {
	wire: LevelEditorWire;
	placed: PlaceRequest[];
	patched: Array<{ eid: number; req: PatchRequest }>;
	deleted: number[];
	rejectNextPlace: boolean;
	rejectNextPatch: boolean;
	rejectNextDelete: boolean;
	nextID: number;
}

function fakeWire(): FakeWire {
	const harness: FakeWire = {
		// `wire` is filled in below; see also the comment at the
		// bottom of this function. Spreading `harness` into the
		// returned object would create *copies* of the boolean +
		// nextID fields, so the test's mutations wouldn't reach the
		// closure the fake wire reads from. Returning the same
		// reference ensures `w.rejectNextPlace = true` actually
		// flips the flag the fake checks.
		wire: undefined as unknown as LevelEditorWire,
		placed: [],
		patched: [],
		deleted: [],
		rejectNextPlace: false,
		rejectNextPatch: false,
		rejectNextDelete: false,
		nextID: 1000,
	};
	harness.wire = {
		placeEntity: vi.fn(async (req: PlaceRequest): Promise<{ entity: PlacementWire }> => {
			if (harness.rejectNextPlace) { harness.rejectNextPlace = false; throw new Error("place failed"); }
			harness.placed.push(req);
			const id = harness.nextID++;
			return {
				entity: {
					id,
					entity_type_id: req.entityTypeId,
					x: req.x, y: req.y,
					rotation_degrees: req.rotation ?? 0,
					instance_overrides: req.instanceOverrides ?? {},
					tags: req.tags ?? [],
				},
			};
		}),
		patchEntity: vi.fn(async (eid: number, req: PatchRequest): Promise<{ entity: PlacementWire }> => {
			if (harness.rejectNextPatch) { harness.rejectNextPatch = false; throw new Error("patch failed"); }
			harness.patched.push({ eid, req });
			return {
				entity: {
					id: eid,
					entity_type_id: 1,
					x: req.x ?? 0,
					y: req.y ?? 0,
					rotation_degrees: req.rotation ?? 0,
					instance_overrides: req.instanceOverrides ?? {},
					tags: [],
				},
			};
		}),
		deleteEntity: vi.fn(async (eid: number): Promise<null> => {
			if (harness.rejectNextDelete) { harness.rejectNextDelete = false; throw new Error("delete failed"); }
			harness.deleted.push(eid);
			return null;
		}),
		listEntities: vi.fn(),
		loadBackdropTiles: vi.fn(),
		loadPlacementCatalog: vi.fn(),
		loadBackdropCatalog: vi.fn(),
	} as unknown as LevelEditorWire;
	return harness;
}

const palEntry: PaletteAtlasEntry = {
	id: 7, name: "spawn", class: "logic",
	sprite_url: "/x.png", atlas_index: 0, atlas_cols: 1, tile_size: 32,
};

function makeCtx() {
	const state = new EditorState(opts);
	state.addPaletteEntries([palEntry]);
	state.setActiveEntity(palEntry);
	const w = fakeWire();
	const errors: string[] = [];
	const ops = new LevelOps({
		state,
		wire: w.wire,
		onError: (m) => errors.push(m),
	});
	return { state, w, ops, errors };
}

describe("LevelOps.place", () => {
	it("places via wire and registers an undo entry", async () => {
		const { state, w, ops } = makeCtx();
		await ops.place(3, 4);
		expect(w.placed).toEqual([{ entityTypeId: 7, x: 3, y: 4, rotation: 0 }]);
		expect(state.allPlacements()).toHaveLength(1);
		expect(state.canUndo()).toBe(true);
	});

	it("rolls back the optimistic placeholder on error and reports", async () => {
		const { state, w, ops, errors } = makeCtx();
		w.rejectNextPlace = true;
		await ops.place(3, 4);
		expect(state.allPlacements()).toHaveLength(0);
		expect(errors[0]).toMatch(/Couldn't place/);
		expect(state.canUndo()).toBe(false);
	});

	it("requires an active entity", async () => {
		const { state, ops, errors, w } = makeCtx();
		state.setActiveEntity(null);
		await ops.place(0, 0);
		expect(w.placed).toHaveLength(0);
		expect(errors[0]).toMatch(/Pick an entity/);
	});

	it("rotates per the active rotation", async () => {
		const { state, w, ops } = makeCtx();
		state.setActiveRotation(180);
		await ops.place(1, 1);
		expect(w.placed[0]?.rotation).toBe(180);
	});
});

describe("LevelOps.patch", () => {
	it("optimistically updates state and PATCHes the server", async () => {
		const { state, w, ops } = makeCtx();
		await ops.place(0, 0);
		const id = state.allPlacements()[0]!.id;
		await ops.patch(id, { x: 5, y: 5 });
		expect(state.placement(id)?.x).toBe(5);
		expect(w.patched.at(-1)).toEqual({ eid: id, req: { x: 5, y: 5 } });
	});

	it("rolls back on PATCH failure", async () => {
		const { state, w, ops, errors } = makeCtx();
		await ops.place(0, 0);
		const id = state.allPlacements()[0]!.id;
		w.rejectNextPatch = true;
		await ops.patch(id, { x: 5, y: 5 });
		// Rolled back to original (0,0).
		expect(state.placement(id)?.x).toBe(0);
		expect(state.placement(id)?.y).toBe(0);
		expect(errors[0]).toMatch(/Couldn't update/);
	});
});

describe("LevelOps.remove + undo cycle", () => {
	it("delete + undo recreates with a new id", async () => {
		const { state, w, ops } = makeCtx();
		await ops.place(2, 2);
		const oldID = state.allPlacements()[0]!.id;
		await ops.remove(oldID);
		expect(state.allPlacements()).toHaveLength(0);
		expect(w.deleted).toEqual([oldID]);

		// Undo the delete -> server POSTs a re-create with a fresh id.
		await state.undo();
		expect(state.allPlacements()).toHaveLength(1);
		// New id, NOT the old one.
		expect(state.allPlacements()[0]!.id).not.toBe(oldID);
		expect(state.allPlacements()[0]!.x).toBe(2);
		expect(state.allPlacements()[0]!.y).toBe(2);
	});

	it("delete + undo + redo deletes the re-created row", async () => {
		const { state, w, ops } = makeCtx();
		await ops.place(2, 2);
		const oldID = state.allPlacements()[0]!.id;
		await ops.remove(oldID);
		await state.undo();
		const newID = state.allPlacements()[0]!.id;
		await state.redo();
		expect(state.allPlacements()).toHaveLength(0);
		expect(w.deleted).toEqual([oldID, newID]);
	});

	it("rolls back the delete on server failure", async () => {
		const { state, w, ops, errors } = makeCtx();
		await ops.place(2, 2);
		const id = state.allPlacements()[0]!.id;
		w.rejectNextDelete = true;
		await ops.remove(id);
		expect(state.allPlacements()).toHaveLength(1);
		expect(errors[0]).toMatch(/Couldn't delete/);
	});
});

describe("LevelOps place + undo cycle", () => {
	it("place + undo deletes the placement", async () => {
		const { state, w, ops } = makeCtx();
		await ops.place(2, 2);
		const id = state.allPlacements()[0]!.id;
		await state.undo();
		expect(state.allPlacements()).toHaveLength(0);
		expect(w.deleted).toContain(id);
	});

	it("place + undo + redo re-creates with a new id", async () => {
		const { state, ops } = makeCtx();
		await ops.place(2, 2);
		const oldID = state.allPlacements()[0]!.id;
		await state.undo();
		await state.redo();
		expect(state.allPlacements()).toHaveLength(1);
		expect(state.allPlacements()[0]!.id).not.toBe(oldID);
	});
});
