// Boxland — level editor optimistic ops.
//
// Wraps every mutation (place / move / rotate / overrides / delete)
// in the standard "apply optimistically, rollback on error" pattern.
// Every op also pushes an undo/redo entry into state history, so a
// successful op is reversible end-to-end (re-creates send a fresh
// POST, getting a new id; we re-bind the id in the redo closure).
//
// Pure module — talks to EditorState + LevelEditorWire. No DOM, no
// Pixi. The caller (entry-level-editor.ts) chooses how to surface
// errors (toast, status bar, etc.).

import type { EditorState } from "./state";
import type { LevelEditorWire, PlaceRequest } from "./wire";
import { placementToPlaceRequest } from "./wire";
import {
	type Placement,
	placementFromWire,
} from "./types";

/**
 * Reporter for one-line error messages. Editors typically wire this
 * to a status-bar slot or a toast component.
 */
export type ErrorReporter = (msg: string) => void;

export interface OpsContext {
	state: EditorState;
	wire: LevelEditorWire;
	onError: ErrorReporter;
}

export class LevelOps {
	constructor(private readonly ctx: OpsContext) {}

	/** Place a new entity at (x, y) using the active palette entry +
	 *  active rotation. No-op if no entity is armed. Pushes an undo
	 *  entry on success. */
	async place(x: number, y: number): Promise<void> {
		const ent = this.ctx.state.activeEntity;
		if (!ent) {
			this.ctx.onError("Pick an entity from the palette first.");
			return;
		}
		const rotation = this.ctx.state.activeRotation;
		// Optimistic placeholder so the cell paints immediately.
		const tempID = -Date.now() - Math.floor(Math.random() * 1000);
		const ghost: Placement = {
			id: tempID,
			entityTypeId: ent.id,
			x, y, rotation,
			instanceOverrides: {},
			tags: [],
		};
		this.ctx.state.upsertPlacement(ghost);
		this.ctx.state.beginPending();
		try {
			const res = await this.ctx.wire.placeEntity({
				entityTypeId: ent.id, x, y, rotation,
			});
			this.ctx.state.removePlacement(tempID);
			const placed = placementFromWire(res.entity);
			this.ctx.state.upsertPlacement(placed);
			this.pushPlaceUndo(placed);
		} catch (err) {
			this.ctx.state.removePlacement(tempID);
			this.ctx.onError(`Couldn't place: ${formatErr(err)}`);
		} finally {
			this.ctx.state.endPending();
		}
	}

	/** Move (or rotate) an existing placement. Caller passes the
	 *  fields actually changing — others are inferred from the
	 *  current row (so a partial PATCH preserves rotation if only
	 *  x/y is supplied). */
	async patch(
		id: number,
		fields: { x?: number; y?: number; rotation?: 0 | 90 | 180 | 270; instanceOverrides?: Record<string, unknown> },
	): Promise<void> {
		const cur = this.ctx.state.placement(id);
		if (!cur) return;
		const before: Partial<Placement> = {
			x: cur.x, y: cur.y, rotation: cur.rotation,
			instanceOverrides: { ...cur.instanceOverrides },
		};
		this.ctx.state.patchPlacement(id, fields);
		this.ctx.state.beginPending();
		try {
			const res = await this.ctx.wire.patchEntity(id, fields);
			const fresh = placementFromWire(res.entity);
			this.ctx.state.upsertPlacement(fresh);
			this.pushPatchUndo(id, before, fields);
		} catch (err) {
			this.ctx.state.patchPlacement(id, before);
			this.ctx.onError(`Couldn't update: ${formatErr(err)}`);
		} finally {
			this.ctx.state.endPending();
		}
	}

	/** Remove a placement. Undo recreates it via a fresh POST (server
	 *  assigns a new id). */
	async remove(id: number): Promise<void> {
		const before = this.ctx.state.placement(id);
		if (!before) return;
		this.ctx.state.removePlacement(id);
		this.ctx.state.beginPending();
		try {
			await this.ctx.wire.deleteEntity(id);
			this.pushDeleteUndo(before);
		} catch (err) {
			this.ctx.state.upsertPlacement(before);
			this.ctx.onError(`Couldn't delete: ${formatErr(err)}`);
		} finally {
			this.ctx.state.endPending();
		}
	}

	// ---- undo helpers --------------------------------------------------

	private pushPlaceUndo(p: Placement): void {
		// Track the latest server-assigned id across redo cycles.
		// `current` starts as the just-placed id and gets re-bound
		// whenever redo re-creates the placement (server assigns a
		// fresh id each time).
		let current = p.id;
		this.ctx.state.pushHistory({
			undo: async () => {
				const target = this.ctx.state.placement(current);
				if (!target) return;
				this.ctx.state.removePlacement(current);
				try {
					await this.ctx.wire.deleteEntity(current);
				} catch (err) {
					this.ctx.state.upsertPlacement(target);
					this.ctx.onError(`Undo failed: ${formatErr(err)}`);
				}
			},
			redo: async () => {
				try {
					const req: PlaceRequest = placementToPlaceRequest(p);
					const res = await this.ctx.wire.placeEntity(req);
					const placed = placementFromWire(res.entity);
					this.ctx.state.upsertPlacement(placed);
					current = placed.id;
				} catch (err) {
					this.ctx.onError(`Redo failed: ${formatErr(err)}`);
				}
			},
		});
	}

	private pushPatchUndo(
		id: number,
		before: Partial<Placement>,
		after: { x?: number; y?: number; rotation?: 0 | 90 | 180 | 270; instanceOverrides?: Record<string, unknown> },
	): void {
		this.ctx.state.pushHistory({
			undo: async () => {
				this.ctx.state.patchPlacement(id, before);
				try { await this.ctx.wire.patchEntity(id, before); }
				catch (err) { this.ctx.onError(`Undo failed: ${formatErr(err)}`); }
			},
			redo: async () => {
				this.ctx.state.patchPlacement(id, after);
				try { await this.ctx.wire.patchEntity(id, after); }
				catch (err) { this.ctx.onError(`Redo failed: ${formatErr(err)}`); }
			},
		});
	}

	private pushDeleteUndo(deleted: Placement): void {
		// Mirror of pushPlaceUndo: the "current" id rebinds across
		// undo/redo cycles since each re-create gets a fresh id.
		let current: number | null = null;
		this.ctx.state.pushHistory({
			undo: async () => {
				try {
					const res = await this.ctx.wire.placeEntity(placementToPlaceRequest(deleted));
					const placed = placementFromWire(res.entity);
					this.ctx.state.upsertPlacement(placed);
					current = placed.id;
				} catch (err) {
					this.ctx.onError(`Undo failed: ${formatErr(err)}`);
				}
			},
			redo: async () => {
				if (current === null) return;
				const target = this.ctx.state.placement(current);
				if (!target) return;
				this.ctx.state.removePlacement(current);
				try { await this.ctx.wire.deleteEntity(current); }
				catch (err) {
					this.ctx.state.upsertPlacement(target);
					this.ctx.onError(`Redo failed: ${formatErr(err)}`);
				}
			},
		});
	}
}

function formatErr(e: unknown): string {
	if (e instanceof Error) return e.message;
	return String(e);
}
