// Boxland — mapmaker page boot.
//
// Templ-side script tag: <script type="module" src="/static/web/mapmaker.js" defer>.
// The host element carries data-bx-mapmaker-* attributes the legacy
// JS used; we read those plus the layer + palette tree the templ
// already renders. No need for a separate entity-types fetch — the
// palette is already serialized into the page DOM with the right
// data-bx-* attributes.

import { Assets, Container, Graphics } from "pixi.js";
import {
	BoxlandApp,
	EditorHarness,
	StaticAssetCatalog,
	type Camera,
} from "@render";

import { MapmakerState, newStrokeCtx, type StrokeCtx, type HistoryEntry } from "./state";
import { MapmakerWire } from "./wire";
import {
	stamp, stampRect, floodFill, applyHistorySide, groupByLayer,
} from "./tools";
import {
	buildRenderables,
	buildOverlayShapes,
	defaultCamera,
	TILE_SUB_PX,
} from "./render-bridge";
import {
	emptyStamp,
	type Cell,
	type MapLayer,
	type MapTile,
	type StampResult,
	type Tool,
} from "./types";

const TILE_PX = 32;

interface MapmakerBoot {
	mapId: number;
	mapWidth: number;
	mapHeight: number;
	defaultLayerId: number;
}

function readBoot(host: HTMLElement): MapmakerBoot {
	const need = (name: string): string => {
		const v = host.getAttribute(name);
		if (v == null) throw new Error(`mapmaker boot: missing ${name}`);
		return v;
	};
	return {
		mapId: Number(need("data-map-id")),
		mapWidth: Number(need("data-map-w")),
		mapHeight: Number(need("data-map-h")),
		defaultLayerId: Number(host.getAttribute("data-default-layer-id") ?? "0"),
	};
}

interface PaletteEntry {
	id: number;
	name: string;
	url: string;
	atlasIndex: number;
	atlasCols: number;
	tileSize: number;
}

function readPaletteFromDOM(): PaletteEntry[] {
	const out: PaletteEntry[] = [];
	document.querySelectorAll("[data-bx-entity-type-id]").forEach((el) => {
		const node = el as HTMLElement;
		const id = Number(node.dataset.bxEntityTypeId);
		if (!Number.isFinite(id) || id <= 0) return;
		out.push({
			id,
			name: node.dataset.bxPaletteName ?? "tile",
			url: node.dataset.bxSpriteUrl ?? "",
			atlasIndex: Number(node.dataset.bxAtlasIndex ?? 0),
			atlasCols: Math.max(1, Number(node.dataset.bxAtlasCols ?? 1)),
			tileSize: Math.max(1, Number(node.dataset.bxTileSize ?? TILE_PX)),
		});
	});
	return out;
}

function readLayersFromDOM(): MapLayer[] {
	const out: MapLayer[] = [];
	document.querySelectorAll(".bx-mapmaker__layers li[data-bx-layer-id]").forEach((el) => {
		const node = el as HTMLElement;
		const id = Number(node.dataset.bxLayerId ?? 0);
		if (!id) return;
		out.push({
			id,
			name: node.dataset.bxLayerName ?? "layer",
			kind: (node.dataset.bxLayerKind === "lighting" ? "lighting" : "tile"),
			yShift: 0,
			ySort: node.dataset.bxLayerYSort === "true",
		});
	});
	return out;
}

export async function bootMapmaker(): Promise<EditorHarness | null> {
	const host = document.querySelector("[data-bx-mapmaker-canvas]") as HTMLElement | null;
	if (!host) return null;
	const boot = readBoot(host);
	if (!boot.mapId) return null;

	const palette = readPaletteFromDOM();
	const catalog = new StaticAssetCatalog({
		entries: palette.map((e) => ({
			id: e.id,
			url: e.url,
			atlasCols: e.atlasCols,
			tileSize: e.tileSize,
			atlasIndex: e.atlasIndex,
		})),
	});
	const knownAssetIDs = new Set<number>(palette.map((p) => p.id));
	const layers = readLayersFromDOM();

	// The templ ships a <canvas> element; we replace it with a <div>
	// host so Pixi can mount its own canvas. We preserve the host's
	// data-* attributes (and especially `data-bx-mapmaker-canvas`)
	// on the replacement so the sibling procedural overlay script
	// (still Canvas2D-era; it only talks to us via CustomEvents)
	// keeps finding us via its querySelector.
	const wrap = host.parentElement;
	if (!wrap) return null;
	const canvasHost = document.createElement("div");
	canvasHost.className = host.className + " bx-mapmaker__pixi-host";
	canvasHost.setAttribute("data-bx-mapmaker-canvas", "");
	canvasHost.dataset.bxMapmakerCanvasHost = "";
	for (const attr of ["data-map-id", "data-map-w", "data-map-h", "data-default-layer-id"]) {
		const v = host.getAttribute(attr);
		if (v != null) canvasHost.setAttribute(attr, v);
	}
	canvasHost.style.minWidth = `${boot.mapWidth * TILE_PX}px`;
	canvasHost.style.minHeight = `${boot.mapHeight * TILE_PX}px`;
	host.replaceWith(canvasHost);

	const visibleW = Math.min(640, boot.mapWidth * TILE_PX);
	const visibleH = Math.min(400, boot.mapHeight * TILE_PX);

	const app = await BoxlandApp.create({
		host: canvasHost,
		worldViewW: visibleW,
		worldViewH: visibleH,
		catalog,
	});

	// Overlay container for selection rect + sample rect (drawn as
	// outlines, not Renderables). Mounted on app.scene.root so it's
	// camera-scaled along with the tile sprites.
	const overlay = new Container();
	const overlayGfx = new Graphics();
	overlay.addChild(overlayGfx);
	app.scene.root.addChild(overlay);

	const state = new MapmakerState({
		mapWidth: boot.mapWidth,
		mapHeight: boot.mapHeight,
		defaultLayerId: boot.defaultLayerId || (layers[0]?.id ?? 0),
	});
	state.setLayers(layers);

	const wire = new MapmakerWire(boot.mapId);

	// Pre-warm textures + load tiles + load locks in parallel.
	await Promise.allSettled(palette.map((e) => Assets.load(e.url).catch(() => undefined)));
	const [tilesRes, locksRes] = await Promise.allSettled([
		wire.loadTiles(),
		wire.loadLocks(),
	]);
	if (tilesRes.status === "fulfilled") state.setInitialTiles(tilesRes.value);
	if (locksRes.status === "fulfilled") state.setInitialLocks(locksRes.value);

	const camera: Camera = defaultCamera(boot.mapWidth, boot.mapHeight);
	const harness = EditorHarness.create({ app, camera });

	const flush = () => {
		const stampGhost: { entityID: number; rotation: 0 | 90 | 180 | 270 } | null =
			state.activeEntity ? { entityID: state.activeEntity, rotation: state.activeRotation } : null;
		harness.setRenderables(buildRenderables({
			tiles: state.allTiles(),
			procPreview: state.procPreview,
			stampGhost,
			cursorCell: state.cursorCell,
			dragRectFrom: state.dragRectFrom,
			dragRectTo: state.dragRectTo,
			sampleRect: state.sampleRect,
			locks: state.allLocks(),
			tool: state.tool,
			activeLayer: state.activeLayer,
			mapWidth: state.mapWidth(),
			mapHeight: state.mapHeight(),
			knownAssetIDs,
		}));
		harness.setCamera(camera);
		drawOverlay();
	};

	const drawOverlay = () => {
		overlayGfx.clear();
		const shapes = buildOverlayShapes({
			tiles: state.allTiles(),
			procPreview: state.procPreview,
			stampGhost: null,
			cursorCell: state.cursorCell,
			dragRectFrom: state.dragRectFrom,
			dragRectTo: state.dragRectTo,
			sampleRect: state.sampleRect,
			locks: state.allLocks(),
			tool: state.tool,
			activeLayer: state.activeLayer,
			mapWidth: state.mapWidth(),
			mapHeight: state.mapHeight(),
			knownAssetIDs,
		});
		if (shapes.dragRect) {
			const r = shapes.dragRect;
			const x0 = Math.min(r.from.x, r.to.x) * TILE_PX;
			const y0 = Math.min(r.from.y, r.to.y) * TILE_PX;
			const w = (Math.abs(r.from.x - r.to.x) + 1) * TILE_PX;
			const h = (Math.abs(r.from.y - r.to.y) + 1) * TILE_PX;
			overlayGfx.rect(x0, y0, w, h);
			overlayGfx.stroke({ color: 0xffffff, width: 1, alpha: 0.85 });
		}
		if (shapes.sampleRect) {
			const r = shapes.sampleRect;
			overlayGfx.rect(r.x * TILE_PX, r.y * TILE_PX, r.width * TILE_PX, r.height * TILE_PX);
			overlayGfx.stroke({ color: 0xffdd4a, width: 2, alpha: 0.9 });
		}
	};
	state.subscribe(flush);
	flush();

	// ---- Pointer + tool gestures ----

	let dragging = false;
	let dragStart: Cell | null = null;
	let strokeAccum: StampResult = emptyStamp();
	let strokeCtx: StrokeCtx | null = null;
	let strokeLabel: Tool | "stroke" = "stroke";

	const canvas = app.pixi.canvas as HTMLCanvasElement;
	const cellFromEvent = (e: PointerEvent): Cell => {
		const rect = canvas.getBoundingClientRect();
		const sx = (e.clientX - rect.left) * (canvas.width / rect.width) / app.scene.root.scale.x;
		const sy = (e.clientY - rect.top) * (canvas.height / rect.height) / app.scene.root.scale.y;
		const halfW = visibleW / 2;
		const halfH = visibleH / 2;
		const wx = (sx - app.scene.root.position.x / app.scene.root.scale.x) + (camera.cx / 256) - halfW;
		const wy = (sy - app.scene.root.position.y / app.scene.root.scale.y) + (camera.cy / 256) - halfH;
		const cx = Math.max(0, Math.min(state.mapWidth() - 1, Math.floor(wx / TILE_PX)));
		const cy = Math.max(0, Math.min(state.mapHeight() - 1, Math.floor(wy / TILE_PX)));
		return { x: cx, y: cy };
	};

	canvas.addEventListener("pointermove", (e) => {
		const cell = cellFromEvent(e);
		state.setCursorCell(cell);
		if (!dragging) return;
		const mods = { shift: e.shiftKey, alt: e.altKey };
		if (state.tool === "rect" || state.tool === "sample") {
			state.setDragRect(dragStart, cell);
			return;
		}
		if (state.tool === "brush" || state.tool === "eraser" || state.tool === "lock") {
			const out = stamp(state, strokeCtx, cell, mods);
			merge(strokeAccum, out);
		}
	});

	canvas.addEventListener("pointerdown", (e) => {
		if (e.button !== 0) return;
		canvas.setPointerCapture(e.pointerId);
		dragging = true;
		const cell = cellFromEvent(e);
		const mods = { shift: e.shiftKey, alt: e.altKey };

		if (state.tool === "fill") {
			strokeCtx = newStrokeCtx();
			strokeLabel = "fill";
			const out = floodFill(state, strokeCtx, cell);
			merge(strokeAccum, out);
			void finishStroke();
			dragging = false;
			return;
		}
		if (state.tool === "rect" || state.tool === "sample") {
			dragStart = cell;
			state.setDragRect(cell, cell);
			return;
		}
		strokeCtx = newStrokeCtx();
		strokeLabel = state.tool;
		const out = stamp(state, strokeCtx, cell, mods);
		merge(strokeAccum, out);
	});

	const finishStroke = async () => {
		dragging = false;

		if (state.tool === "rect" && dragStart && state.dragRectTo) {
			strokeCtx = newStrokeCtx();
			strokeLabel = "rect";
			const out = stampRect(state, strokeCtx, dragStart, state.dragRectTo);
			merge(strokeAccum, out);
			state.setDragRect(null, null);
			dragStart = null;
		}
		if (state.tool === "sample" && dragStart && state.dragRectTo) {
			const r = {
				x: Math.min(dragStart.x, state.dragRectTo.x),
				y: Math.min(dragStart.y, state.dragRectTo.y),
				width: Math.abs(dragStart.x - state.dragRectTo.x) + 1,
				height: Math.abs(dragStart.y - state.dragRectTo.y) + 1,
			};
			canvas.dispatchEvent(new CustomEvent("bx:procedural-sample-drawn", { bubbles: true, detail: r }));
			state.setDragRect(null, null);
			dragStart = null;
		}

		// Push history if anything happened.
		if (strokeCtx && strokeCtx.seen.size > 0) {
			state.pushHistory(state.buildHistoryEntry(strokeLabel, strokeCtx));
		}
		strokeCtx = null;

		// Ship the wire diff.
		try {
			await shipDiff(strokeAccum);
		} catch (err) {
			flash(`Save failed: ${formatErr(err)}`);
		}
		strokeAccum = emptyStamp();
	};

	canvas.addEventListener("pointerup", () => void finishStroke());
	canvas.addEventListener("pointercancel", () => void finishStroke());
	canvas.addEventListener("pointerleave", () => { if (dragging) void finishStroke(); });

	// ---- Wire shipping ----

	async function shipDiff(diff: StampResult): Promise<void> {
		const tasks: Array<Promise<unknown>> = [];
		if (diff.placed.length > 0) {
			state.beginPending();
			tasks.push(wire.postTiles(diff.placed).finally(() => state.endPending()));
		}
		if (diff.locked.length > 0) {
			state.beginPending();
			tasks.push(wire.postLocks(diff.locked).finally(() => state.endPending()));
		}
		const erasedByLayer = groupByLayer(diff.erased);
		for (const [layerId, points] of erasedByLayer) {
			state.beginPending();
			tasks.push(wire.deleteTiles(layerId, points).finally(() => state.endPending()));
		}
		const unlockedByLayer = groupByLayer(diff.unlocked);
		for (const [layerId, points] of unlockedByLayer) {
			state.beginPending();
			tasks.push(wire.deleteLocks(layerId, points).finally(() => state.endPending()));
		}
		await Promise.all(tasks);
	}

	// ---- Undo / Redo ----

	const undo = async () => {
		const e = state.popUndoEntry();
		if (!e) return;
		const diff = applyHistorySide(state, e, "before");
		await shipDiff(diff).catch((err) => flash(`Undo save failed: ${formatErr(err)}`));
	};
	const redo = async () => {
		const e = state.popRedoEntry();
		if (!e) return;
		const diff = applyHistorySide(state, e, "after");
		await shipDiff(diff).catch((err) => flash(`Redo save failed: ${formatErr(err)}`));
	};

	// ---- Toolbar / palette / layers wiring ----

	document.querySelectorAll<HTMLElement>(".bx-mapmaker__tools [data-bx-tool]").forEach((btn) => {
		btn.addEventListener("click", () => {
			state.setTool(btn.getAttribute("data-bx-tool") as Tool);
			document.querySelectorAll<HTMLElement>(".bx-mapmaker__tools [data-bx-tool]").forEach((b) => {
				b.setAttribute("aria-pressed", b === btn ? "true" : "false");
			});
		});
	});

	document.querySelectorAll<HTMLElement>(".bx-mapmaker__layers li[data-bx-layer-id]").forEach((li) => {
		li.addEventListener("click", () => {
			const id = Number(li.dataset.bxLayerId ?? 0);
			if (!id) return;
			state.setActiveLayer(id);
			document.querySelectorAll<HTMLElement>(".bx-mapmaker__layers li").forEach((x) => x.setAttribute("aria-selected", "false"));
			li.setAttribute("aria-selected", "true");
		});
	});

	document.querySelectorAll<HTMLElement>(".bx-mapmaker__palette li[data-bx-entity-type-id]").forEach((li) => {
		li.addEventListener("click", () => {
			const id = Number(li.dataset.bxEntityTypeId ?? 0);
			if (id) state.setActiveEntity(id);
			document.querySelectorAll<HTMLElement>(".bx-mapmaker__palette li[data-bx-entity-type-id]")
				.forEach((x) => x.setAttribute("aria-selected", x === li ? "true" : "false"));
		});
	});

	const rotateBtn = document.querySelector<HTMLElement>("[data-bx-rotate-tile]");
	if (rotateBtn) rotateBtn.addEventListener("click", () => {
		state.setActiveRotation(((state.activeRotation + 90) % 360) as 0 | 90 | 180 | 270);
		rotateBtn.textContent = `⟳ ${state.activeRotation}°`;
	});

	const undoBtn = document.querySelector<HTMLButtonElement>("[data-bx-history-undo]");
	const redoBtn = document.querySelector<HTMLButtonElement>("[data-bx-history-redo]");
	if (undoBtn) undoBtn.addEventListener("click", () => void undo());
	if (redoBtn) redoBtn.addEventListener("click", () => void redo());

	// ---- Hotkeys ----

	document.addEventListener("keydown", (e) => {
		if (isTextEditingTarget(e.target)) return;
		const k = e.key.toLowerCase();
		const mod = e.ctrlKey || e.metaKey;
		if (mod && k === "z" && !e.shiftKey) { void undo(); e.preventDefault(); return; }
		if ((mod && k === "z" && e.shiftKey) || (mod && k === "y")) { void redo(); e.preventDefault(); return; }
		if (mod) return;
		const map: Record<string, Tool> = { b: "brush", r: "rect", f: "fill", i: "eyedrop", e: "eraser", l: "lock", s: "sample" };
		if (map[k]) { state.setTool(map[k]); e.preventDefault(); return; }
		if (k === "t") {
			state.setActiveRotation(((state.activeRotation + 90) % 360) as 0 | 90 | 180 | 270);
			if (rotateBtn) rotateBtn.textContent = `⟳ ${state.activeRotation}°`;
			e.preventDefault();
			return;
		}
	});

	// ---- Status bar ----

	state.subscribe(() => {
		setStatus("tool", state.tool);
		setStatus("layer", layers.find((l) => l.id === state.activeLayer)?.name ?? "—");
		setStatus("dirty", state.pending > 0 ? "saving…" : "");
		if (undoBtn) undoBtn.disabled = !state.canUndo();
		if (redoBtn) redoBtn.disabled = !state.canRedo();
	});

	// ---- CustomEvent integration with the procedural overlay ----

	canvasHost.addEventListener("bx:procedural-preview", (e: Event) => {
		const ce = e as CustomEvent<{ tiles?: Array<{ layer_id: number; x: number; y: number; entity_type_id: number; rotation_degrees: number }> }>;
		const tiles = ce.detail?.tiles ?? [];
		state.setProcPreview(tiles.map((t) => ({
			layerId: t.layer_id, x: t.x, y: t.y,
			entityTypeId: t.entity_type_id,
			rotation: ((t.rotation_degrees % 360) | 0) as 0 | 90 | 180 | 270,
		})));
	});
	canvasHost.addEventListener("bx:procedural-preview-clear", () => {
		state.setProcPreview(null);
	});
	canvasHost.addEventListener("bx:procedural-sample-set", (e: Event) => {
		const ce = e as CustomEvent<{ x: number; y: number; width: number; height: number }>;
		state.setSampleRect(ce.detail);
	});
	canvasHost.addEventListener("bx:procedural-sample-clear", () => {
		state.setSampleRect(null);
	});
	canvasHost.addEventListener("bx:locks-cleared", () => {
		state.clearLocks();
	});
	canvasHost.addEventListener("bx:mapmaker-history-clear", () => {
		state.clearHistory();
	});
	canvasHost.addEventListener("bx:mapmaker-reload", async () => {
		const fresh = await wire.loadTiles().catch(() => []);
		state.setInitialTiles(fresh);
		state.clearHistory();
	});

	return harness;
}

// ---- helpers ----

function merge(into: StampResult, from: StampResult): void {
	into.placed.push(...from.placed);
	into.erased.push(...from.erased);
	into.locked.push(...from.locked);
	into.unlocked.push(...from.unlocked);
}

function setStatus(key: string, value: string): void {
	const el = document.querySelector(`[data-bx-mapmaker-status="${key}"]`);
	if (el) el.textContent = value;
}

function flash(msg: string): void {
	const el = document.querySelector("[data-bx-status-msg]") as HTMLElement | null;
	if (el) {
		el.textContent = msg;
		setTimeout(() => { if (el.textContent === msg) el.textContent = ""; }, 4000);
	} else {
		console.warn("[mapmaker]", msg);
	}
}

function formatErr(e: unknown): string {
	return e instanceof Error ? e.message : String(e);
}

function isTextEditingTarget(t: EventTarget | null): boolean {
	if (!(t instanceof HTMLElement)) return false;
	const tag = t.tagName;
	return tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT" || t.isContentEditable;
}

if (typeof document !== "undefined" && document.body?.dataset.surface === "mapmaker") {
	void bootMapmaker();
}

export { TILE_SUB_PX };
