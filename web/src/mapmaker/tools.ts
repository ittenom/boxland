// Boxland — mapmaker pointer tools.
//
// stamp(state, ctx, cell, mods) takes one cell + the active tool +
// modifiers and mutates state, returning a StampResult the caller
// uses to build the wire diff. Pure module: no DOM, no fetch — the
// caller (entry-mapmaker.ts) is responsible for translating pointer
// events into cells (camera + zoom math) and shipping the diffs.

import type { MapTile, LockedCell, Cell, StampResult } from "./types";
import { emptyStamp, normalizeRect, tileKey } from "./types";
import type { MapmakerState, StrokeCtx } from "./state";

export interface StampMods {
	shift?: boolean;
	alt?: boolean;
}

/**
 * Apply the active tool to one cell. Returns the diff (placed /
 * erased / locked / unlocked) for the caller to ship to the server
 * at stroke end.
 *
 * Tools:
 *   brush    — place activeEntity at cell (replaces existing)
 *   eraser   — remove the tile at this cell on the active layer
 *   eyedrop  — copy the tile's entity_type + rotation into active
 *              state (no diff)
 *   lock     — paint+lock; shift=unlock only; alt=unlock+erase
 *   rect     — handled by `stampRect` (stamp() ignores it)
 *   fill     — handled by `floodFill` (stamp() ignores it)
 *   sample   — handled by entry script (CustomEvent semantics)
 */
export function stamp(
	state: MapmakerState,
	ctx: StrokeCtx | null,
	cell: Cell,
	mods: StampMods,
): StampResult {
	const layerId = state.activeLayer;
	if (!layerId) return emptyStamp();
	if (!state.inBounds(cell)) return emptyStamp();

	const captureCell = () => {
		if (ctx) state.capturePreImage(ctx, layerId, cell.x, cell.y);
	};

	switch (state.tool) {
		case "eraser": {
			if (!state.tileAt(layerId, cell.x, cell.y)) return emptyStamp();
			captureCell();
			state.deleteTile(layerId, cell.x, cell.y);
			return {
				placed: [],
				erased: [{ layerId, x: cell.x, y: cell.y, entityTypeId: 0, rotation: 0 }],
				locked: [], unlocked: [],
			};
		}
		case "eyedrop": {
			const t = state.tileAt(layerId, cell.x, cell.y);
			if (t) {
				state.setActiveEntity(t.entityTypeId);
				state.setActiveRotation(t.rotation);
			}
			return emptyStamp();
		}
		case "lock": {
			if (mods.shift || mods.alt) {
				const had = state.lockAt(layerId, cell.x, cell.y);
				if (!had && !mods.alt) return emptyStamp();
				captureCell();
				state.deleteLock(layerId, cell.x, cell.y);
				const out: StampResult = { placed: [], erased: [], locked: [], unlocked: [] };
				if (had) out.unlocked.push({ ...had });
				if (mods.alt && state.tileAt(layerId, cell.x, cell.y)) {
					const rm = state.tileAt(layerId, cell.x, cell.y);
					if (rm) {
						state.deleteTile(layerId, cell.x, cell.y);
						out.erased.push({ ...rm });
					}
				}
				return out;
			}
			if (!state.activeEntity) return emptyStamp();
			captureCell();
			const t: MapTile = {
				layerId, x: cell.x, y: cell.y,
				entityTypeId: state.activeEntity,
				rotation: state.activeRotation,
			};
			state.upsertTile(t);
			state.upsertLock(t);
			return { placed: [t], erased: [], locked: [t], unlocked: [] };
		}
		case "brush": {
			if (!state.activeEntity) return emptyStamp();
			captureCell();
			const t: MapTile = {
				layerId, x: cell.x, y: cell.y,
				entityTypeId: state.activeEntity,
				rotation: state.activeRotation,
			};
			state.upsertTile(t);
			return { placed: [t], erased: [], locked: [], unlocked: [] };
		}
		default:
			return emptyStamp();
	}
}

/**
 * Stamp every cell in the rectangle [from..to]. Used by the rect
 * tool when the user releases the mouse button. Ctx is fresh per
 * stroke; the caller passes one in so all cells share an undo entry.
 */
export function stampRect(
	state: MapmakerState,
	ctx: StrokeCtx,
	from: Cell,
	to: Cell,
): StampResult {
	const r = normalizeRect(from, to);
	const merged: StampResult = { placed: [], erased: [], locked: [], unlocked: [] };
	if (!state.activeLayer || !state.activeEntity) return merged;
	for (let y = r.y0; y <= r.y1; y++) {
		for (let x = r.x0; x <= r.x1; x++) {
			const cell = { x, y };
			if (!state.inBounds(cell)) continue;
			state.capturePreImage(ctx, state.activeLayer, x, y);
			const t: MapTile = {
				layerId: state.activeLayer,
				x,
				y,
				entityTypeId: state.activeEntity,
				rotation: state.activeRotation,
			};
			state.upsertTile(t);
			merged.placed.push(t);
		}
	}
	return merged;
}

/**
 * Flood fill on the active layer. Replaces every contiguous cell
 * matching the start cell's entity_type with the active entity (or
 * empties them, if the active tool is eraser).
 */
export function floodFill(
	state: MapmakerState,
	ctx: StrokeCtx,
	start: Cell,
): StampResult {
	const layerId = state.activeLayer;
	const w = state.mapWidth();
	const h = state.mapHeight();
	const merged: StampResult = { placed: [], erased: [], locked: [], unlocked: [] };

	const startTile = state.tileAt(layerId, start.x, start.y);
	const startEntity = startTile ? startTile.entityTypeId : 0;
	const target = state.tool === "eraser" ? 0 : state.activeEntity;

	if (target === 0 && state.tool !== "eraser") return merged;
	if (startEntity === target) return merged;

	const visited = new Set<string>();
	const queue: Cell[] = [start];
	const SAFETY_CAP = 4096;
	let safety = 0;

	while (queue.length > 0 && safety < SAFETY_CAP) {
		safety++;
		const cell = queue.shift()!;
		const k = tileKey({ layerId, x: cell.x, y: cell.y });
		if (visited.has(k)) continue;
		visited.add(k);
		if (cell.x < 0 || cell.y < 0 || cell.x >= w || cell.y >= h) continue;
		const cur = state.tileAt(layerId, cell.x, cell.y);
		const curEntity = cur ? cur.entityTypeId : 0;
		if (curEntity !== startEntity) continue;
		state.capturePreImage(ctx, layerId, cell.x, cell.y);
		if (target === 0) {
			if (cur) {
				state.deleteTile(layerId, cell.x, cell.y);
				merged.erased.push({ ...cur });
			}
		} else {
			const t: MapTile = {
				layerId, x: cell.x, y: cell.y,
				entityTypeId: target, rotation: state.activeRotation,
			};
			state.upsertTile(t);
			merged.placed.push(t);
		}
		queue.push({ x: cell.x + 1, y: cell.y }, { x: cell.x - 1, y: cell.y });
		queue.push({ x: cell.x, y: cell.y + 1 }, { x: cell.x, y: cell.y - 1 });
	}

	return merged;
}

/** Flip a tile's rotation by +90°. Used by the T hotkey on the
 *  active stamp; doesn't touch placed cells. */
export function cycleStampRotation(state: MapmakerState): void {
	const next = ((state.activeRotation + 90) % 360) as 0 | 90 | 180 | 270;
	state.setActiveRotation(next);
}

/**
 * History side application — used by undo/redo. Rewrites local state
 * to the "before" or "after" snapshot of an entry and returns the
 * wire diff to ship.
 */
export function applyHistorySide(
	state: MapmakerState,
	entry: import("./state").HistoryEntry,
	side: "before" | "after",
): StampResult {
	const tilesSnap = side === "before" ? entry.beforeTiles : entry.afterTiles;
	const locksSnap = side === "before" ? entry.beforeLocks : entry.afterLocks;
	const merged: StampResult = { placed: [], erased: [], locked: [], unlocked: [] };

	for (const [k, snap] of tilesSnap) {
		const [layerId, xs, ys] = k.split(":").map(Number) as [number, number, number];
		const cur = state.tileAt(layerId, xs, ys);
		if (snap) {
			state.upsertTile(snap);
			if (!cur || cur.entityTypeId !== snap.entityTypeId || cur.rotation !== snap.rotation) {
				merged.placed.push(snap);
			}
		} else if (cur) {
			state.deleteTile(layerId, xs, ys);
			merged.erased.push(cur);
		}
	}
	for (const [k, snap] of locksSnap) {
		const [layerId, xs, ys] = k.split(":").map(Number) as [number, number, number];
		const cur = state.lockAt(layerId, xs, ys);
		if (snap) {
			state.upsertLock(snap);
			if (!cur || cur.entityTypeId !== snap.entityTypeId || cur.rotation !== snap.rotation) {
				merged.locked.push(snap as LockedCell);
			}
		} else if (cur) {
			state.deleteLock(layerId, xs, ys);
			merged.unlocked.push(cur);
		}
	}
	return merged;
}

/** Group erase/unlock points by layerId for the bulk DELETE wire shape. */
export function groupByLayer<T extends { layerId: number; x: number; y: number }>(
	items: readonly T[],
): Map<number, Array<readonly [number, number]>> {
	const out = new Map<number, Array<readonly [number, number]>>();
	for (const it of items) {
		let arr = out.get(it.layerId);
		if (!arr) { arr = []; out.set(it.layerId, arr); }
		arr.push([it.x, it.y]);
	}
	return out;
}
