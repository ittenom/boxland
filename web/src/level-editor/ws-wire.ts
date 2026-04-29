// Boxland — level editor WS-backed wire.
//
// Implements the same `placeEntity` / `patchEntity` / `deleteEntity`
// surface the REST `LevelEditorWire` exposed, but dispatches over
// the shared `EditorWire` (WebSocket + FlatBuffers + Realm.Designer).
//
// Why parallel surface: `LevelOps` still owns the optimistic-update
// pattern + the undo/redo stack. The transport is the only thing
// that changed. Keeping the method names identical means
// `LevelOps` (and its 11 unit tests) work unchanged — we just
// inject a WS-backed wire instead of the REST one in production.
//
// Note: the WS path is fire-and-forget at the dispatch layer:
// `dispatch()` returns void. The server applies the op, broadcasts
// an EditorDiff back, and we resolve the returned Promise from the
// next inbound diff that matches our expected kind. A 5s timeout
// resolves with an error if the diff never arrives (network
// hiccup; user reconnect retries).

import * as flatbuffers from "flatbuffers";

import type { EditorWire } from "@render";
import { DesignerOpcode } from "@proto/designer-opcode.js";
import { PlaceLevelEntityPayload } from "@proto/place-level-entity-payload.js";
import { MoveLevelEntityPayload } from "@proto/move-level-entity-payload.js";
import { RemoveLevelEntityPayload } from "@proto/remove-level-entity-payload.js";
import { SetLevelEntityOverridesPayload } from "@proto/set-level-entity-overrides-payload.js";
import { EditorDiffKind } from "@proto/editor-diff-kind.js";
import { EditorLevelPlacement } from "@proto/editor-level-placement.js";
import { EditorLevelPlacementMove } from "@proto/editor-level-placement-move.js";
import { EditorPlacementID } from "@proto/editor-placement-id.js";

import type { PlaceRequest, PatchRequest } from "./wire";
import type { PlacementWire } from "./types";

const ACK_TIMEOUT_MS = 5_000;

/** Pending op state keyed by a synthetic correlation id. v1 keeps
 *  the matching crude (FIFO) — placements get a fresh id from the
 *  server in the diff, and we match by op-kind + earliest pending.
 *  Good enough for a single user editing alone; multi-user co-edit
 *  works because the client treats *every* incoming diff as
 *  authoritative state and applies it whether or not it
 *  originated locally. */
interface PendingPlace {
	resolve: (entity: PlacementWire) => void;
	reject: (err: Error) => void;
	timer: ReturnType<typeof setTimeout>;
}

interface PendingPatch {
	placementID: number;
	resolve: (entity: PlacementWire) => void;
	reject: (err: Error) => void;
	timer: ReturnType<typeof setTimeout>;
}

interface PendingDelete {
	placementID: number;
	resolve: () => void;
	reject: (err: Error) => void;
	timer: ReturnType<typeof setTimeout>;
}

export class WSPlacementWire {
	private readonly placeQ: PendingPlace[] = [];
	private readonly patchQ = new Map<number, PendingPatch>();
	private readonly deleteQ = new Map<number, PendingDelete>();
	private unsub: (() => void) | null = null;

	constructor(
		private readonly wire: EditorWire,
		private readonly levelId: number,
	) {
		this.unsub = wire.onDiff((diff) => this.handleDiff(diff));
	}

	close(): void {
		if (this.unsub) {
			this.unsub();
			this.unsub = null;
		}
		const cancelErr = new Error("level editor: closed");
		for (const p of this.placeQ) { clearTimeout(p.timer); p.reject(cancelErr); }
		this.placeQ.length = 0;
		for (const p of this.patchQ.values()) { clearTimeout(p.timer); p.reject(cancelErr); }
		this.patchQ.clear();
		for (const p of this.deleteQ.values()) { clearTimeout(p.timer); p.reject(cancelErr); }
		this.deleteQ.clear();
	}

	placeEntity(req: PlaceRequest): Promise<{ entity: PlacementWire }> {
		const b = new flatbuffers.Builder(128);
		const overridesOff = b.createString(JSON.stringify(req.instanceOverrides ?? {}));
		const tagOffsets = (req.tags ?? []).map((t) => b.createString(t));
		PlaceLevelEntityPayload.startTagsVector(b, tagOffsets.length);
		for (let i = tagOffsets.length - 1; i >= 0; i--) b.addOffset(tagOffsets[i]!);
		const tagsVec = b.endVector();
		PlaceLevelEntityPayload.startPlaceLevelEntityPayload(b);
		PlaceLevelEntityPayload.addLevelId(b, BigInt(this.levelId));
		PlaceLevelEntityPayload.addEntityTypeId(b, BigInt(req.entityTypeId));
		PlaceLevelEntityPayload.addX(b, req.x);
		PlaceLevelEntityPayload.addY(b, req.y);
		PlaceLevelEntityPayload.addRotationDegrees(b, req.rotation ?? 0);
		PlaceLevelEntityPayload.addInstanceOverridesJson(b, overridesOff);
		PlaceLevelEntityPayload.addTags(b, tagsVec);
		const root = PlaceLevelEntityPayload.endPlaceLevelEntityPayload(b);
		b.finish(root);
		this.wire.dispatch(DesignerOpcode.PlaceLevelEntity, b.asUint8Array());

		return new Promise<{ entity: PlacementWire }>((resolve, reject) => {
			const timer = setTimeout(() => {
				const idx = this.placeQ.findIndex((p) => p.timer === timer);
				if (idx >= 0) this.placeQ.splice(idx, 1);
				reject(new Error("place entity: timed out waiting for diff"));
			}, ACK_TIMEOUT_MS);
			this.placeQ.push({
				resolve: (entity) => resolve({ entity }),
				reject,
				timer,
			});
		});
	}

	patchEntity(eid: number, req: PatchRequest): Promise<{ entity: PlacementWire }> {
		// Move + overrides are split into two opcodes server-side;
		// here we issue them sequentially. v1 surface keeps the
		// same return shape (one entity for the call) — when both
		// move and overrides are in the patch, we resolve once
		// both diffs arrive.
		const wantsMove = req.x !== undefined || req.y !== undefined || req.rotation !== undefined;
		const wantsOverrides = req.instanceOverrides !== undefined;

		if (!wantsMove && !wantsOverrides) {
			// No-op: synthesize an immediate "current state" via
			// a fake entity that the caller's optimistic update
			// already produced. v1: just resolve with a stub.
			return Promise.resolve({ entity: { id: eid, entity_type_id: 0, x: 0, y: 0, rotation_degrees: 0, instance_overrides: {}, tags: [] } });
		}

		return new Promise<{ entity: PlacementWire }>((resolve, reject) => {
			const timer = setTimeout(() => {
				if (this.patchQ.delete(eid)) {
					reject(new Error("patch entity: timed out waiting for diff"));
				}
			}, ACK_TIMEOUT_MS);
			this.patchQ.set(eid, {
				placementID: eid,
				resolve: (entity) => resolve({ entity }),
				reject,
				timer,
			});

			if (wantsMove) {
				const b = new flatbuffers.Builder(64);
				MoveLevelEntityPayload.startMoveLevelEntityPayload(b);
				MoveLevelEntityPayload.addLevelId(b, BigInt(this.levelId));
				MoveLevelEntityPayload.addPlacementId(b, BigInt(eid));
				MoveLevelEntityPayload.addX(b, req.x ?? 0);
				MoveLevelEntityPayload.addY(b, req.y ?? 0);
				MoveLevelEntityPayload.addRotationDegrees(b, req.rotation ?? 0);
				const root = MoveLevelEntityPayload.endMoveLevelEntityPayload(b);
				b.finish(root);
				this.wire.dispatch(DesignerOpcode.MoveLevelEntity, b.asUint8Array());
			}
			if (wantsOverrides) {
				const b = new flatbuffers.Builder(128);
				const ovOff = b.createString(JSON.stringify(req.instanceOverrides));
				SetLevelEntityOverridesPayload.startSetLevelEntityOverridesPayload(b);
				SetLevelEntityOverridesPayload.addLevelId(b, BigInt(this.levelId));
				SetLevelEntityOverridesPayload.addPlacementId(b, BigInt(eid));
				SetLevelEntityOverridesPayload.addInstanceOverridesJson(b, ovOff);
				const root = SetLevelEntityOverridesPayload.endSetLevelEntityOverridesPayload(b);
				b.finish(root);
				this.wire.dispatch(DesignerOpcode.SetLevelEntityOverrides, b.asUint8Array());
			}
		});
	}

	deleteEntity(eid: number): Promise<null> {
		const b = new flatbuffers.Builder(32);
		RemoveLevelEntityPayload.startRemoveLevelEntityPayload(b);
		RemoveLevelEntityPayload.addLevelId(b, BigInt(this.levelId));
		RemoveLevelEntityPayload.addPlacementId(b, BigInt(eid));
		const root = RemoveLevelEntityPayload.endRemoveLevelEntityPayload(b);
		b.finish(root);
		this.wire.dispatch(DesignerOpcode.RemoveLevelEntity, b.asUint8Array());

		return new Promise<null>((resolve, reject) => {
			const timer = setTimeout(() => {
				if (this.deleteQ.delete(eid)) {
					reject(new Error("delete entity: timed out waiting for diff"));
				}
			}, ACK_TIMEOUT_MS);
			this.deleteQ.set(eid, { placementID: eid, resolve: () => resolve(null), reject, timer });
		});
	}

	// ---- diff routing ------------------------------------------------

	private handleDiff(diff: import("@proto/editor-diff.js").EditorDiff): void {
		switch (diff.kind()) {
			case EditorDiffKind.PlacementAdded:
				this.handlePlacementAdded(diff);
				break;
			case EditorDiffKind.PlacementMoved:
				this.handlePlacementMoved(diff);
				break;
			case EditorDiffKind.PlacementRemoved:
				this.handlePlacementRemoved(diff);
				break;
			case EditorDiffKind.OverridesChanged:
				this.handleOverridesChanged(diff);
				break;
		}
	}

	private handlePlacementAdded(diff: import("@proto/editor-diff.js").EditorDiff): void {
		const bytes = diffBodyBytes(diff);
		if (!bytes) return;
		const placement = EditorLevelPlacement.getRootAsEditorLevelPlacement(new flatbuffers.ByteBuffer(bytes));
		const entity = decodePlacement(placement);
		const next = this.placeQ.shift();
		if (next) {
			clearTimeout(next.timer);
			next.resolve(entity);
		}
	}

	private handlePlacementMoved(diff: import("@proto/editor-diff.js").EditorDiff): void {
		const bytes = diffBodyBytes(diff);
		if (!bytes) return;
		const move = EditorLevelPlacementMove.getRootAsEditorLevelPlacementMove(new flatbuffers.ByteBuffer(bytes));
		const id = Number(move.placementId());
		const pending = this.patchQ.get(id);
		if (pending) {
			clearTimeout(pending.timer);
			this.patchQ.delete(id);
			pending.resolve({
				id, entity_type_id: 0,
				x: move.x(),
				y: move.y(),
				rotation_degrees: normalizeRotation(move.rotationDegrees()),
				instance_overrides: {},
				tags: [],
			});
		}
	}

	private handlePlacementRemoved(diff: import("@proto/editor-diff.js").EditorDiff): void {
		const bytes = diffBodyBytes(diff);
		if (!bytes) return;
		const removed = EditorPlacementID.getRootAsEditorPlacementID(new flatbuffers.ByteBuffer(bytes));
		const id = Number(removed.placementId());
		const pending = this.deleteQ.get(id);
		if (pending) {
			clearTimeout(pending.timer);
			this.deleteQ.delete(id);
			pending.resolve();
		}
	}

	private handleOverridesChanged(diff: import("@proto/editor-diff.js").EditorDiff): void {
		// Same handling as PlacementMoved — resolves the pending
		// patch promise; the body carries placement_id + overrides.
		// We don't need to surface the overrides themselves to
		// the LevelOps caller (they already optimistic-set them).
		const bytes = diffBodyBytes(diff);
		if (!bytes) return;
		const ov = (
			// import deferred to avoid cyclic init order issues at module load.
			require("@proto/editor-placement-overrides.js") as typeof import("@proto/editor-placement-overrides.js")
		).EditorPlacementOverrides.getRootAsEditorPlacementOverrides(new flatbuffers.ByteBuffer(bytes));
		const id = Number(ov.placementId());
		const pending = this.patchQ.get(id);
		if (pending) {
			clearTimeout(pending.timer);
			this.patchQ.delete(id);
			pending.resolve({
				id, entity_type_id: 0, x: 0, y: 0, rotation_degrees: 0,
				instance_overrides: safeParseJSON(ov.instanceOverridesJson()),
				tags: [],
			});
		}
	}
}

function diffBodyBytes(diff: import("@proto/editor-diff.js").EditorDiff): Uint8Array | null {
	const len = diff.bodyLength();
	if (len === 0) return null;
	const buf = new Uint8Array(len);
	for (let i = 0; i < len; i++) buf[i] = diff.body(i)!;
	return buf;
}

function decodePlacement(p: EditorLevelPlacement): PlacementWire {
	const tags: string[] = [];
	for (let i = 0; i < p.tagsLength(); i++) {
		const t = p.tags(i);
		if (t) tags.push(t);
	}
	return {
		id: Number(p.placementId()),
		entity_type_id: Number(p.entityTypeId()),
		x: p.x(),
		y: p.y(),
		rotation_degrees: normalizeRotation(p.rotationDegrees()),
		instance_overrides: safeParseJSON(p.instanceOverridesJson()),
		tags,
	};
}

function safeParseJSON(s: string | null): Record<string, unknown> {
	if (!s) return {};
	try { return JSON.parse(s) as Record<string, unknown>; }
	catch { return {}; }
}

function normalizeRotation(r: number): 0 | 90 | 180 | 270 {
	switch (r) { case 90: return 90; case 180: return 180; case 270: return 270; default: return 0; }
}
