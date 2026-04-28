// Boxland — level editor state.
//
// One holder for all the mutable pieces of editor state: placements,
// selection, active palette entry/rotation, current tool, undo/redo
// history. Pure module — no DOM, no fetch, no Pixi. The boot module
// (entry-level-editor.ts) wires this to the wire client + harness.
//
// This module's API surface is what tools.ts and the entry script
// call into. Keeping the surface small and pure is what lets us test
// the place/move/erase + undo/redo loops in node, without standing
// up jsdom.

import type {
	BackdropTile,
	Cell,
	PaletteAtlasEntry,
	Placement,
	Tool,
} from "./types";

/** Listener invoked after any state change. Editors re-render through
 *  the EditorHarness; this is just the trigger. */
export type StateListener = () => void;

/**
 * Undo/redo entry. `apply()` runs the side effect when the user
 * triggers undo (or redo, for the inverse). For mutations that
 * involve a server round-trip (place / patch / delete) the apply()
 * fn talks to the wire client.
 */
export interface HistoryEntry {
	undo: () => Promise<void> | void;
	redo: () => Promise<void> | void;
}

const HISTORY_LIMIT = 100;

export interface EditorStateOptions {
	mapWidth: number;
	mapHeight: number;
}

export class EditorState {
	private placements = new Map<number, Placement>();
	private backdrop: BackdropTile[] = [];
	private paletteByEntity = new Map<number, PaletteAtlasEntry>();
	private listeners = new Set<StateListener>();

	tool: Tool = "place";
	activeEntity: PaletteAtlasEntry | null = null;
	activeRotation: 0 | 90 | 180 | 270 = 0;
	selection: number | null = null;
	cursorCell: Cell | null = null;
	pending = 0;

	private undoStack: HistoryEntry[] = [];
	private redoStack: HistoryEntry[] = [];

	constructor(private readonly opts: EditorStateOptions) {}

	// ---- subscription -------------------------------------------------

	subscribe(fn: StateListener): () => void {
		this.listeners.add(fn);
		return () => { this.listeners.delete(fn); };
	}

	notify(): void {
		for (const fn of this.listeners) fn();
	}

	// ---- read ---------------------------------------------------------

	mapWidth(): number { return this.opts.mapWidth; }
	mapHeight(): number { return this.opts.mapHeight; }

	allPlacements(): readonly Placement[] {
		return [...this.placements.values()];
	}

	placement(id: number): Placement | null {
		return this.placements.get(id) ?? null;
	}

	allBackdrop(): readonly BackdropTile[] { return this.backdrop; }

	paletteEntry(entityTypeId: number): PaletteAtlasEntry | null {
		return this.paletteByEntity.get(entityTypeId) ?? null;
	}

	allPaletteEntries(): readonly PaletteAtlasEntry[] {
		return [...this.paletteByEntity.values()];
	}

	stackedAt(c: Cell): readonly Placement[] {
		const out: Placement[] = [];
		for (const p of this.placements.values()) {
			if (p.x === c.x && p.y === c.y) out.push(p);
		}
		// Most recently created on top of stack.
		out.sort((a, b) => b.id - a.id);
		return out;
	}

	topPlacementAt(c: Cell): Placement | null {
		// "Top" = current selection if it's at this cell (so repeated
		// clicks cycle), otherwise the most-recent placement.
		const stack = this.stackedAt(c);
		if (stack.length === 0) return null;
		if (this.selection !== null) {
			const sel = stack.find((p) => p.id === this.selection);
			if (sel) return sel;
		}
		return stack[0] ?? null;
	}

	canUndo(): boolean { return this.undoStack.length > 0; }
	canRedo(): boolean { return this.redoStack.length > 0; }

	// ---- bulk loads (no history; called once on boot) -----------------

	setBackdrop(tiles: BackdropTile[]): void {
		this.backdrop = tiles;
		this.notify();
	}

	setInitialPlacements(list: Placement[]): void {
		this.placements.clear();
		for (const p of list) this.placements.set(p.id, p);
		this.notify();
	}

	addPaletteEntries(entries: readonly PaletteAtlasEntry[]): void {
		for (const e of entries) this.paletteByEntity.set(e.id, e);
		this.notify();
	}

	// ---- mutations (touch placements + history) -----------------------

	upsertPlacement(p: Placement): void {
		this.placements.set(p.id, p);
		this.notify();
	}

	removePlacement(id: number): void {
		this.placements.delete(id);
		if (this.selection === id) this.selection = null;
		this.notify();
	}

	patchPlacement(id: number, patch: Partial<Omit<Placement, "id" | "entityTypeId">>): void {
		const cur = this.placements.get(id);
		if (!cur) return;
		this.placements.set(id, { ...cur, ...patch });
		this.notify();
	}

	// ---- undo/redo ----------------------------------------------------

	pushHistory(entry: HistoryEntry): void {
		this.undoStack.push(entry);
		if (this.undoStack.length > HISTORY_LIMIT) this.undoStack.shift();
		this.redoStack.length = 0;
		this.notify();
	}

	async undo(): Promise<void> {
		const e = this.undoStack.pop();
		if (!e) return;
		this.redoStack.push(e);
		this.notify();
		await e.undo();
	}

	async redo(): Promise<void> {
		const e = this.redoStack.pop();
		if (!e) return;
		this.undoStack.push(e);
		this.notify();
		await e.redo();
	}

	// ---- selection / tool / cursor -----------------------------------

	setTool(t: Tool): void { this.tool = t; this.notify(); }
	setSelection(id: number | null): void { this.selection = id; this.notify(); }
	setActiveEntity(e: PaletteAtlasEntry | null): void {
		this.activeEntity = e;
		this.notify();
	}
	setActiveRotation(r: 0 | 90 | 180 | 270): void {
		this.activeRotation = r;
		this.notify();
	}
	setCursorCell(c: Cell | null): void {
		this.cursorCell = c;
		this.notify();
	}

	// ---- pending counter (drives "saving…" indicator) ----------------

	beginPending(): void { this.pending++; this.notify(); }
	endPending(): void { this.pending = Math.max(0, this.pending - 1); this.notify(); }

	// ---- bounds ------------------------------------------------------

	inBounds(c: Cell): boolean {
		return c.x >= 0 && c.y >= 0 && c.x < this.opts.mapWidth && c.y < this.opts.mapHeight;
	}
}
