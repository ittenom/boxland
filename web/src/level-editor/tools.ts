// Boxland — level editor pointer tools.
//
// Place / Select / Erase gestures translated from raw pointer events
// into editor ops. The viewport math (screen → cell) is owned by the
// caller (entry script) since it depends on Pixi camera + canvas
// rect; this module assumes its caller has already converted to
// cell coordinates.

import type { Cell, Placement } from "./types";
import type { EditorState } from "./state";
import type { LevelOps } from "./ops";

/** Pointer event reduced to what the tools actually use. */
export interface PointerInfo {
	/** 0 = primary (left), 1 = middle, 2 = secondary (right). */
	button: number;
	cell: Cell;
	/** True if Space is held (drives pan, ignored by tools below). */
	spaceDown: boolean;
}

/** Returned by handlePointerDown when the gesture should escalate
 *  into a drag. The entry script tracks this in a `drag` slot and
 *  routes subsequent move/up events back through this module. */
export interface DragHandle {
	kind: "move-selection";
	id: number;
	originX: number;
	originY: number;
	lastCell: Cell;
}

/**
 * Pointer-down dispatch. Returns a DragHandle when the gesture
 * should keep going (e.g. select+drag-to-move); null otherwise.
 *
 * The caller is responsible for ignoring this when the event is a
 * pan gesture (middle mouse / space-drag) — those are camera-level,
 * not tool-level, and they bypass the tool logic.
 */
export function handlePointerDown(
	state: EditorState,
	ops: LevelOps,
	p: PointerInfo,
): DragHandle | null {
	// Right-click is a quick-erase regardless of tool, matching the
	// previous Canvas2D editor's behaviour.
	if (p.button === 2) {
		const hit = state.topPlacementAt(p.cell);
		if (hit) void ops.remove(hit.id);
		return null;
	}

	if (state.tool === "place") {
		if (!state.inBounds(p.cell)) return null;
		void ops.place(p.cell.x, p.cell.y);
		return null;
	}

	if (state.tool === "select") {
		const hit = state.topPlacementAt(p.cell);
		if (!hit) {
			state.setSelection(null);
			return null;
		}
		// Cycle through stacked placements when the user repeatedly
		// clicks the same cell.
		if (state.selection === hit.id) {
			const stack = state.stackedAt(p.cell);
			const idx = stack.findIndex((q) => q.id === hit.id);
			const next = stack[(idx + 1) % stack.length];
			if (next) state.setSelection(next.id);
		} else {
			state.setSelection(hit.id);
		}
		const sel = state.placement(state.selection ?? -1);
		if (!sel) return null;
		return {
			kind: "move-selection",
			id: sel.id,
			originX: sel.x,
			originY: sel.y,
			lastCell: p.cell,
		};
	}

	if (state.tool === "erase") {
		const hit = state.topPlacementAt(p.cell);
		if (hit) void ops.remove(hit.id);
		return null;
	}

	return null;
}

/**
 * Drag-move during select-drag. Updates the placement's x/y in local
 * state for live preview; the actual PATCH fires on pointer-up. */
export function handlePointerMove(
	state: EditorState,
	drag: DragHandle | null,
	cell: Cell,
): void {
	state.setCursorCell(cell);
	if (!drag || drag.kind !== "move-selection") return;
	const cur = state.placement(drag.id);
	if (!cur) return;
	if (cur.x === cell.x && cur.y === cell.y) return;
	state.patchPlacement(drag.id, { x: cell.x, y: cell.y });
	drag.lastCell = cell;
}

/**
 * Drag end. If the placement actually moved, fire a PATCH (which
 * also pushes an undo entry). No-op if the user dropped it back on
 * its original cell.
 */
export function handlePointerUp(
	state: EditorState,
	ops: LevelOps,
	drag: DragHandle | null,
): void {
	if (!drag || drag.kind !== "move-selection") return;
	const cur = state.placement(drag.id);
	if (!cur) return;
	const moved = cur.x !== drag.originX || cur.y !== drag.originY;
	if (!moved) return;
	// Roll the optimistic move back to "before" state and let
	// LevelOps.patch re-apply it cleanly (so the undo entry has the
	// right before/after pair).
	state.patchPlacement(drag.id, { x: drag.originX, y: drag.originY });
	void ops.patch(drag.id, { x: cur.x, y: cur.y });
}

/**
 * Rotation hotkey handler. When a placement is selected and the
 * user is in select mode, rotates the selected one and PATCHes;
 * otherwise rotates the active-entity ghost so the next click
 * places at the new rotation.
 */
export function rotate(state: EditorState, ops: LevelOps): void {
	if (state.tool === "select" && state.selection !== null) {
		const sel: Placement | null = state.placement(state.selection);
		if (!sel) return;
		const next = nextRot(sel.rotation);
		void ops.patch(sel.id, { rotation: next });
	} else {
		state.setActiveRotation(nextRot(state.activeRotation));
	}
}

function nextRot(r: 0 | 90 | 180 | 270): 0 | 90 | 180 | 270 {
	switch (r) {
		case 0: return 90;
		case 90: return 180;
		case 180: return 270;
		default: return 0;
	}
}
