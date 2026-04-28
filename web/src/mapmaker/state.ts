// Boxland — mapmaker state.
//
// One holder for tile placements, locks, layers, palette, and active
// tool/entity/rotation. Pure module — no DOM, no fetch, no Pixi.
//
// Same shape as web/src/level-editor/state.ts. The two surfaces share
// patterns deliberately: tools.ts and entry-mapmaker.ts are the only
// pieces that diverge meaningfully between them, and that's by intent
// (tile painting vs entity placement have genuinely different
// gestures).

import type {
	Cell,
	LockedCell,
	MapLayer,
	MapTile,
	PreImage,
	SampleRect,
	Tool,
} from "./types";
import { emptyStamp, tileKey, type StampResult } from "./types";

export type StateListener = () => void;

export interface HistoryEntry {
	label: string;
	beforeTiles: Map<string, MapTile | null>;
	beforeLocks: Map<string, LockedCell | null>;
	afterTiles:  Map<string, MapTile | null>;
	afterLocks:  Map<string, LockedCell | null>;
}

const HISTORY_LIMIT = 100;

export interface MapmakerStateOpts {
	mapWidth: number;
	mapHeight: number;
	defaultLayerId: number;
}

/**
 * Stroke context — pre-image snapshots for one drag/click. Built
 * fresh per stroke; finishStroke turns it into a HistoryEntry by
 * sampling current state for the "after" side.
 */
export interface StrokeCtx {
	seen: Set<string>;
	prevTiles: Map<string, MapTile | null>;
	prevLocks: Map<string, LockedCell | null>;
}

export function newStrokeCtx(): StrokeCtx {
	return { seen: new Set(), prevTiles: new Map(), prevLocks: new Map() };
}

export class MapmakerState {
	private readonly tiles = new Map<string, MapTile>();
	private readonly locks = new Map<string, LockedCell>();
	private layers: MapLayer[] = [];
	private listeners = new Set<StateListener>();

	tool: Tool = "brush";
	activeLayer: number;
	activeEntity: number = 0;
	activeRotation: 0 | 90 | 180 | 270 = 0;

	cursorCell: Cell | null = null;
	dragRectFrom: Cell | null = null;
	dragRectTo: Cell | null = null;

	procPreview: MapTile[] | null = null;
	sampleRect: SampleRect | null = null;

	pending = 0;

	private undoStack: HistoryEntry[] = [];
	private redoStack: HistoryEntry[] = [];

	constructor(private readonly opts: MapmakerStateOpts) {
		this.activeLayer = opts.defaultLayerId;
	}

	// ---- subscription ----

	subscribe(fn: StateListener): () => void {
		this.listeners.add(fn);
		return () => { this.listeners.delete(fn); };
	}
	notify(): void { for (const fn of this.listeners) fn(); }

	// ---- read ----

	mapWidth(): number { return this.opts.mapWidth; }
	mapHeight(): number { return this.opts.mapHeight; }

	allLayers(): readonly MapLayer[] { return this.layers; }

	allTiles(): readonly MapTile[] { return [...this.tiles.values()]; }
	allLocks(): readonly LockedCell[] { return [...this.locks.values()]; }
	tileAt(layerId: number, x: number, y: number): MapTile | null {
		return this.tiles.get(tileKey({ layerId, x, y })) ?? null;
	}
	lockAt(layerId: number, x: number, y: number): LockedCell | null {
		return this.locks.get(tileKey({ layerId, x, y })) ?? null;
	}

	tilesOnLayer(layerId: number): readonly MapTile[] {
		const out: MapTile[] = [];
		for (const t of this.tiles.values()) if (t.layerId === layerId) out.push(t);
		return out;
	}

	canUndo(): boolean { return this.undoStack.length > 0; }
	canRedo(): boolean { return this.redoStack.length > 0; }

	tileCount(): number { return this.tiles.size; }
	lockCount(): number { return this.locks.size; }

	inBounds(c: Cell): boolean {
		return c.x >= 0 && c.y >= 0 && c.x < this.opts.mapWidth && c.y < this.opts.mapHeight;
	}

	// ---- bulk loads (no history; called on boot or after procedural commit) ----

	setLayers(layers: MapLayer[]): void {
		this.layers = layers;
		// If our active layer was wiped (rare), pick the lowest-ord tile
		// layer so the user always has something to paint on.
		if (!layers.find((l) => l.id === this.activeLayer)) {
			const tileLayer = layers.find((l) => l.kind === "tile");
			if (tileLayer) this.activeLayer = tileLayer.id;
		}
		this.notify();
	}

	setInitialTiles(tiles: MapTile[]): void {
		this.tiles.clear();
		for (const t of tiles) this.tiles.set(tileKey(t), t);
		this.notify();
	}

	setInitialLocks(locks: LockedCell[]): void {
		this.locks.clear();
		for (const c of locks) this.locks.set(tileKey(c), c);
		this.notify();
	}

	clearLocks(): void {
		this.locks.clear();
		this.notify();
	}

	// ---- mutations (touch tiles / locks; pre-image capture is the
	//                stroke ctx's job, not ours) ----

	upsertTile(t: MapTile): void {
		this.tiles.set(tileKey(t), t);
		this.notify();
	}
	deleteTile(layerId: number, x: number, y: number): void {
		this.tiles.delete(tileKey({ layerId, x, y }));
		this.notify();
	}
	upsertLock(c: LockedCell): void {
		this.locks.set(tileKey(c), c);
		this.notify();
	}
	deleteLock(layerId: number, x: number, y: number): void {
		this.locks.delete(tileKey({ layerId, x, y }));
		this.notify();
	}

	// ---- selection + tool + cursor ----

	setTool(t: Tool): void { this.tool = t; this.notify(); }
	setActiveLayer(id: number): void { this.activeLayer = id; this.notify(); }
	setActiveEntity(id: number): void { this.activeEntity = id; this.notify(); }
	setActiveRotation(r: 0 | 90 | 180 | 270): void { this.activeRotation = r; this.notify(); }
	setCursorCell(c: Cell | null): void { this.cursorCell = c; this.notify(); }
	setDragRect(from: Cell | null, to: Cell | null): void {
		this.dragRectFrom = from;
		this.dragRectTo = to;
		this.notify();
	}
	setProcPreview(p: MapTile[] | null): void { this.procPreview = p; this.notify(); }
	setSampleRect(r: SampleRect | null): void { this.sampleRect = r; this.notify(); }

	// ---- pending counter ----

	beginPending(): void { this.pending++; this.notify(); }
	endPending(): void { this.pending = Math.max(0, this.pending - 1); this.notify(); }

	// ---- history ----

	pushHistory(entry: HistoryEntry): void {
		this.undoStack.push(entry);
		while (this.undoStack.length > HISTORY_LIMIT) this.undoStack.shift();
		this.redoStack.length = 0;
		this.notify();
	}

	clearHistory(): void {
		this.undoStack.length = 0;
		this.redoStack.length = 0;
		this.notify();
	}

	popUndoEntry(): HistoryEntry | null {
		const e = this.undoStack.pop();
		if (!e) return null;
		this.redoStack.push(e);
		this.notify();
		return e;
	}

	popRedoEntry(): HistoryEntry | null {
		const e = this.redoStack.pop();
		if (!e) return null;
		this.undoStack.push(e);
		this.notify();
		return e;
	}

	undoLabel(): string | null {
		const e = this.undoStack[this.undoStack.length - 1];
		return e ? e.label : null;
	}
	redoLabel(): string | null {
		const e = this.redoStack[this.redoStack.length - 1];
		return e ? e.label : null;
	}

	/**
	 * Build a HistoryEntry from a finished stroke ctx by sampling the
	 * current "after" state for every cell the stroke touched.
	 */
	buildHistoryEntry(label: string, ctx: StrokeCtx): HistoryEntry {
		const beforeTiles = new Map<string, MapTile | null>();
		const beforeLocks = new Map<string, LockedCell | null>();
		const afterTiles  = new Map<string, MapTile | null>();
		const afterLocks  = new Map<string, LockedCell | null>();
		for (const k of ctx.seen) {
			beforeTiles.set(k, ctx.prevTiles.get(k) ?? null);
			beforeLocks.set(k, ctx.prevLocks.get(k) ?? null);
			const t = this.tiles.get(k);
			const l = this.locks.get(k);
			afterTiles.set(k, t ? { ...t } : null);
			afterLocks.set(k, l ? { ...l } : null);
		}
		return { label, beforeTiles, beforeLocks, afterTiles, afterLocks };
	}

	/** Capture the BEFORE state of a cell on first touch within a stroke. */
	capturePreImage(ctx: StrokeCtx, layerId: number, x: number, y: number): void {
		const k = tileKey({ layerId, x, y });
		if (ctx.seen.has(k)) return;
		ctx.seen.add(k);
		const tile = this.tiles.get(k);
		const lock = this.locks.get(k);
		ctx.prevTiles.set(k, tile ? { ...tile } : null);
		ctx.prevLocks.set(k, lock ? { ...lock } : null);
	}
}

/** Helper for the entry script: build an empty StampResult. */
export { emptyStamp };
export type { PreImage, StampResult };
