// Boxland — level editor page boot.
//
// This is what the templ shell calls into via
// <script type="module" src="/static/web/level-editor.js">. Reads the
// host element's data-bx-* attributes, builds a BoxlandApp +
// EditorHarness, fetches the placement + backdrop catalogs, wires
// palette + tools + hotkeys + inspector. The result is a full Pixi-
// rendered editor that shares its render path with the live game.
//
// Per-page entry convention matches sandbox/entry-sandbox.ts: auto-
// run when document.body.dataset.surface matches our id, but exporting
// `bootLevelEditor()` so a future surface dispatcher (or a unit
// integration test in jsdom) can call it explicitly.

import { Assets } from "pixi.js";
import {
	BoxlandApp,
	EditorHarness,
	StaticAssetCatalog,
	type Camera,
} from "@render";

import { EditorState } from "./state";
import { LevelEditorWire } from "./wire";
import { LevelOps } from "./ops";
import {
	buildRenderables,
	defaultCamera,
	TILE_SUB_PX,
} from "./render-bridge";
import {
	handlePointerDown,
	handlePointerMove,
	handlePointerUp,
	rotate,
} from "./tools";
import {
	type Cell,
	type LevelEditorBoot,
	type PaletteAtlasEntry,
	placementFromWire,
} from "./types";

interface DragState {
	kind: "pan" | "move-selection";
	id?: number;
	originX?: number;
	originY?: number;
	lastClientX?: number;
	lastClientY?: number;
	lastCell?: Cell;
}

function readBoot(host: HTMLElement): LevelEditorBoot {
	const ds = host.dataset;
	const need = (k: string): string => {
		const v = ds[k];
		if (v == null) throw new Error(`level-editor boot: missing data-${k}`);
		return v;
	};
	return {
		levelId: Number(need("levelId")),
		mapId: Number(need("mapId")),
		mapWidth: Number(need("mapW")),
		mapHeight: Number(need("mapH")),
	};
}

export async function bootLevelEditor(
	host: HTMLElement = document.querySelector("[data-bx-level-editor]") as HTMLElement,
	canvasHost: HTMLElement = document.querySelector("[data-bx-level-canvas-host]") as HTMLElement,
): Promise<EditorHarness | null> {
	if (!host || !canvasHost) return null;
	const boot = readBoot(host);

	const wire = new LevelEditorWire(boot.levelId, boot.mapId);
	const state = new EditorState({ mapWidth: boot.mapWidth, mapHeight: boot.mapHeight });

	// 1) Fetch the two catalogs in parallel (palette + backdrop tile types).
	//    Then build a single StaticAssetCatalog from their union.
	const [paletteRes, backdropTypesRes] = await Promise.allSettled([
		wire.loadPlacementCatalog(),
		wire.loadBackdropCatalog(),
	]);
	const palette = paletteRes.status === "fulfilled" ? paletteRes.value.entries : [];
	const backdropTypes = backdropTypesRes.status === "fulfilled" ? backdropTypesRes.value.entries : [];
	if (paletteRes.status === "rejected") {
		console.warn("[level-editor] placement catalog load failed", paletteRes.reason);
	}
	if (backdropTypesRes.status === "rejected") {
		console.warn("[level-editor] backdrop tile-types load failed", backdropTypesRes.reason);
	}
	state.addPaletteEntries(palette);

	// Build one StaticAssetCatalog that knows about both palette
	// (placeable) and backdrop (tile) entity types. The Renderer
	// resolves textures by asset_id == entity_type_id (same convention
	// the sandbox uses).
	const catalogEntries = mergeCatalogEntries([...palette, ...backdropTypes]);
	const catalog = new StaticAssetCatalog({
		entries: catalogEntries.map((e) => ({
			id: e.id,
			url: e.sprite_url,
			atlasCols: e.atlas_cols,
			tileSize: e.tile_size,
			atlasIndex: e.atlas_index,
		})),
	});
	const knownAssetIDs = new Set<number>(catalogEntries.map((e) => e.id));

	// 2) Build BoxlandApp. worldViewW/H = the level's pixel size so
	//    integer-scale fits the whole map by default. We set this to
	//    the visible viewport size, not the full map: the camera
	//    centers on the map and pans/zooms, so the world view is the
	//    *visible window*, not the entire level.
	const visibleW = Math.min(640, boot.mapWidth * 32);
	const visibleH = Math.min(400, boot.mapHeight * 32);
	const app = await BoxlandApp.create({
		host: canvasHost,
		worldViewW: visibleW,
		worldViewH: visibleH,
		catalog,
	});

	// Pre-warm the texture cache so the first frame paints with art.
	await Promise.allSettled(catalog.urls().map((u) => Assets.load(u).catch(() => undefined)));

	// 3) Initial data load (backdrop tiles + placements).
	const [tilesRes, entsRes] = await Promise.allSettled([
		wire.loadBackdropTiles(),
		wire.listEntities(),
	]);
	if (tilesRes.status === "fulfilled") {
		state.setBackdrop(tilesRes.value.tiles.map((t) => ({
			layerId: t.layer_id, x: t.x, y: t.y,
			entityTypeId: t.entity_type_id,
			rotation: t.rotation_degrees,
		})));
	}
	if (entsRes.status === "fulfilled") {
		state.setInitialPlacements(entsRes.value.entities.map(placementFromWire));
	}

	// 4) Harness drives redraws on dirty.
	const camera: Camera = defaultCamera(boot.mapWidth, boot.mapHeight);
	const harness = EditorHarness.create({ app, camera });

	const flush = () => {
		harness.setRenderables(buildRenderables({
			placements: state.allPlacements(),
			backdrop: state.allBackdrop(),
			selection: state.selection,
			cursorCell: state.cursorCell,
			activeEntityID: state.activeEntity?.id ?? null,
			activeRotation: state.activeRotation,
			tool: state.tool,
			mapWidth: state.mapWidth(),
			mapHeight: state.mapHeight(),
			knownAssetIDs,
			pendingPlacementIDs: new Set(), // optimistic ghosts use negative ids; fine.
		}));
		harness.setCamera(camera);
	};
	state.subscribe(flush);
	flush();

	// 5) Pointer + keyboard wiring.
	const ops = new LevelOps({
		state, wire,
		onError: (m) => flash(host, m),
	});
	const errorReporter = (m: string) => flash(host, m);
	let drag: DragState | null = null;
	let spaceDown = false;
	const canvas = app.pixi.canvas as HTMLCanvasElement;

	// Cursor → cell (sub-pixel canvas coords → cell index).
	const cellFromEvent = (e: PointerEvent): Cell => {
		const rect = canvas.getBoundingClientRect();
		// The canvas backing-store is integer-scaled; the reverse map
		// is screen-px → world-px → cell (with camera + zoom math
		// living inside Scene). For v1 we use the canvas's natural
		// 1:1 mapping plus the camera offset; this matches how the
		// previous Canvas2D editor sampled clicks.
		const sx = (e.clientX - rect.left) * (canvas.width / rect.width) / app.scene.root.scale.x;
		const sy = (e.clientY - rect.top) * (canvas.height / rect.height) / app.scene.root.scale.y;
		// Camera in sub-pixels; subtract from world position to get
		// screen-relative; we want the inverse.
		const halfW = visibleW / 2;
		const halfH = visibleH / 2;
		const wx = (sx - app.scene.root.position.x / app.scene.root.scale.x) + (camera.cx / 256) - halfW;
		const wy = (sy - app.scene.root.position.y / app.scene.root.scale.y) + (camera.cy / 256) - halfH;
		const cx = Math.max(0, Math.min(state.mapWidth() - 1, Math.floor(wx / 32)));
		const cy = Math.max(0, Math.min(state.mapHeight() - 1, Math.floor(wy / 32)));
		return { x: cx, y: cy };
	};

	canvas.addEventListener("pointerdown", (e) => {
		canvas.setPointerCapture(e.pointerId);
		canvas.focus({ preventScroll: true });
		const cell = cellFromEvent(e);
		// Pan: middle button or Space+drag.
		if (e.button === 1 || (e.button === 0 && spaceDown)) {
			drag = { kind: "pan", lastClientX: e.clientX, lastClientY: e.clientY };
			e.preventDefault();
			return;
		}
		const handle = handlePointerDown(state, ops, { button: e.button, cell, spaceDown });
		if (handle) drag = handle;
	});

	canvas.addEventListener("pointermove", (e) => {
		const cell = cellFromEvent(e);
		if (drag?.kind === "pan") {
			const dx = e.clientX - (drag.lastClientX ?? e.clientX);
			const dy = e.clientY - (drag.lastClientY ?? e.clientY);
			camera.cx -= (dx * 256) / app.scene.root.scale.x;
			camera.cy -= (dy * 256) / app.scene.root.scale.y;
			drag.lastClientX = e.clientX;
			drag.lastClientY = e.clientY;
			harness.setCamera(camera);
			return;
		}
		handlePointerMove(state, drag as { kind: "move-selection"; id: number; originX: number; originY: number; lastCell: Cell } | null, cell);
	});

	canvas.addEventListener("pointerup", () => {
		if (drag?.kind === "move-selection") {
			handlePointerUp(state, ops, { kind: "move-selection", id: drag.id!, originX: drag.originX!, originY: drag.originY!, lastCell: drag.lastCell ?? { x: 0, y: 0 } });
		}
		drag = null;
	});
	canvas.addEventListener("pointercancel", () => { drag = null; });
	canvas.addEventListener("contextmenu", (e) => e.preventDefault());

	canvas.addEventListener("wheel", (e) => {
		e.preventDefault();
		// Pixi viewport is integer-scaled, so the editor's zoom
		// lives in the camera's sub-pixel offsets / viewport size.
		// v1: increment integer scale by resizing the BoxlandApp's
		// host (cheap; renderer recomputes layout on its own
		// ResizeObserver). Future: dedicated zoom API on Scene.
		// For now wheel just nudges the camera in the y direction
		// when shift is held (a workable proxy).
		const speed = 32;
		camera.cy += e.deltaY > 0 ? speed * 256 : -speed * 256;
		harness.setCamera(camera);
	}, { passive: false });

	// Hotkeys.
	const onKey = (e: KeyboardEvent) => {
		if (isTextEditingTarget(e.target)) return;
		if (e.code === "Space") spaceDown = true;
		const key = e.key.toLowerCase();
		if (e.metaKey && !e.ctrlKey && !e.shiftKey && !e.altKey) return; // boot.js Cmd-shortcut path
		if (e.ctrlKey || e.metaKey) {
			if (key === "z" && !e.shiftKey) { e.preventDefault(); void state.undo(); return; }
			if ((key === "z" && e.shiftKey) || key === "y") { e.preventDefault(); void state.redo(); return; }
			return;
		}
		if (key === "b") { e.preventDefault(); state.setTool("place"); return; }
		if (key === "v") { e.preventDefault(); state.setTool("select"); return; }
		if (key === "e") { e.preventDefault(); state.setTool("erase"); return; }
		if (key === "t") { e.preventDefault(); rotate(state, ops); return; }
		if (key === "escape") { state.setSelection(null); return; }
		if (key === "delete" || key === "backspace") {
			if (state.selection !== null) { e.preventDefault(); void ops.remove(state.selection); }
			return;
		}
	};
	const onKeyUp = (e: KeyboardEvent) => { if (e.code === "Space") spaceDown = false; };
	document.addEventListener("keydown", onKey);
	document.addEventListener("keyup", onKeyUp);

	// Palette click → arm entity + switch to place tool.
	host.querySelectorAll("[data-bx-level-palette-entry]").forEach((el) => {
		const node = el as HTMLElement;
		const click = () => {
			const id = Number(node.dataset.bxEntityTypeId ?? 0);
			const entry = state.paletteEntry(id);
			if (entry) {
				state.setActiveEntity(entry);
				state.setTool("place");
				// Visual: highlight the picked one.
				host.querySelectorAll("[data-bx-level-palette-entry]").forEach((other) => {
					other.setAttribute("aria-selected", other === el ? "true" : "false");
				});
			}
		};
		node.addEventListener("click", click);
		node.addEventListener("keydown", (e) => {
			const ke = e as KeyboardEvent;
			if (ke.key === "Enter" || ke.key === " ") { ke.preventDefault(); click(); }
		});
	});

	// Tool buttons.
	host.querySelectorAll("[data-bx-level-tool]").forEach((btn) => {
		const node = btn as HTMLElement;
		node.addEventListener("click", () => state.setTool(node.dataset.bxLevelTool as "place" | "select" | "erase"));
	});
	const rotateBtn = host.querySelector("[data-bx-level-rotate]") as HTMLElement | null;
	if (rotateBtn) rotateBtn.addEventListener("click", () => rotate(state, ops));
	const undoBtn = host.querySelector("[data-bx-level-undo]") as HTMLButtonElement | null;
	const redoBtn = host.querySelector("[data-bx-level-redo]") as HTMLButtonElement | null;
	if (undoBtn) undoBtn.addEventListener("click", () => void state.undo());
	if (redoBtn) redoBtn.addEventListener("click", () => void state.redo());

	// Status-bar updates from state.
	state.subscribe(() => {
		setText(host, "tool", state.tool);
		setText(host, "entity", state.activeEntity ? `${state.activeEntity.name} (${state.activeEntity.class})` : "no entity selected");
		setText(host, "count", placementsLabel(state.allPlacements().length));
		setText(host, "dirty", state.pending > 0 ? "saving…" : "");
		if (undoBtn) undoBtn.disabled = !state.canUndo();
		if (redoBtn) redoBtn.disabled = !state.canRedo();
		// Tab-strip badge reflects live count.
		document.querySelectorAll(".bx-tabstrip__tab").forEach((tab) => {
			if (tab.textContent && tab.textContent.trim().startsWith("Entities")) {
				tab.textContent = `Entities · ${state.allPlacements().length}`;
			}
		});
	});

	void errorReporter; // wired through ops.onError above

	return harness;
}

// ---- helpers --------------------------------------------------------

function mergeCatalogEntries(
	entries: readonly PaletteAtlasEntry[],
): PaletteAtlasEntry[] {
	const byID = new Map<number, PaletteAtlasEntry>();
	for (const e of entries) {
		// Last write wins; the placement catalog and backdrop catalog
		// can overlap on shared entity_types (rare but valid), and
		// since both endpoints derive their atlas info from the same
		// upstream rows the values will be byte-identical.
		byID.set(e.id, e);
	}
	return [...byID.values()];
}

function setText(host: HTMLElement, key: string, value: string) {
	const el = host.querySelector(`[data-bx-status="${key}"]`);
	if (el) el.textContent = value;
}

function flash(host: HTMLElement, msg: string) {
	const el = host.querySelector('[data-bx-status="dirty"]');
	if (el) {
		el.textContent = msg;
		(el as HTMLElement).style.color = "var(--bx-danger, #f55)";
		setTimeout(() => {
			if (el.textContent === msg) {
				el.textContent = "";
				(el as HTMLElement).style.color = "";
			}
		}, 4000);
	} else {
		console.warn("[level-editor]", msg);
	}
}

function placementsLabel(n: number): string {
	return n === 1 ? "1 placement" : `${n} placements`;
}

function isTextEditingTarget(t: EventTarget | null): boolean {
	if (!(t instanceof HTMLElement)) return false;
	const tag = t.tagName;
	return tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT" || t.isContentEditable;
}

// Auto-run on the level editor's entities tab.
if (typeof document !== "undefined" && document.body?.dataset.surface === "level-editor-entities") {
	void bootLevelEditor();
}

// Suppress unused: TILE_SUB_PX is re-exported here for tests / future
// pan/zoom math callers; explicit export keeps tree-shaking happy.
export { TILE_SUB_PX };
