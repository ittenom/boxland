// Boxland — level editor page boot (Pixi-rendered).
//
// Replaces the previous templ-shell+canvas hybrid with a single
// fullscreen Pixi scene built on the EditorApp harness. The
// templ page now contains only:
//
//   <body data-surface="level-editor-entities">
//     <main id="bx-editor-host" data-...></main>
//     <meta name="bx-ws-url" content="...">
//     <meta name="bx-ws-ticket" content="...">
//     <script type="module" src="/static/web/level-editor.js" defer></script>
//   </body>
//
// The host element's data-bx-* attributes carry the level/map ids;
// the meta tags carry the WS url + ticket the gateway minted.

import { Container, Graphics, Text } from "pixi.js";

import {
	EditorApp,
	EditorWire,
	Theme,
	PaletteGrid,
	Inspector,
	Statusbar,
	Toolbar,
	StaticAssetCatalog,
	type ThemeEntry,
	type PaletteEntry as HarnessPaletteEntry,
	type StaticCatalogEntry,
	type ToolbarAction,
} from "@render";
import { EditorKind } from "@proto/editor-kind.js";

import { EditorState } from "./state";
import { LevelOps } from "./ops";
import { WSPlacementWire } from "./ws-wire";
import { LevelEditorWire } from "./wire";
import { MapmakerRenderableLayer } from "../mapmaker/render-layer";
import { buildRenderables } from "./render-bridge";
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
	type Placement,
	placementFromWire,
} from "./types";

const CELL_SIZE = 32;
const MAP_ORIGIN_X = 18;
const MAP_ORIGIN_Y = 18;

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

function readMeta(name: string): string {
	const m = document.querySelector(`meta[name="${name}"]`);
	return m?.getAttribute("content") ?? "";
}

function readHostOrMeta(host: HTMLElement, hostAttr: string, metaName: string): string {
	return host.getAttribute(hostAttr) ?? readMeta(metaName);
}

export async function bootLevelEditor(
	host: HTMLElement = document.querySelector("[data-bx-level-editor]") as HTMLElement,
): Promise<EditorApp | null> {
	if (!host) return null;
	const boot = readBoot(host);
	const wsURL = readHostOrMeta(host, "data-bx-ws-url", "bx-ws-url");
	const wsTicket = readHostOrMeta(host, "data-bx-ws-ticket", "bx-ws-ticket");
	if (!wsURL || !wsTicket) {
		console.error("[level-editor] missing bx-ws-url or bx-ws-ticket meta");
		return null;
	}

	// 1) Open the WS + join the level-editor session.
	const wire = await EditorWire.connect({ wsURL, wsTicket });
	const snapshot = await wire.joinLevelEditor(boot.levelId);
	if (snapshot.kind() !== EditorKind.LevelEditor) {
		throw new Error(`level-editor: snapshot kind mismatch (got ${snapshot.kind()})`);
	}

	// 2) Build the theme from the snapshot.
	const themeEntries: ThemeEntry[] = [];
	for (let i = 0; i < snapshot.themeLength(); i++) {
		const e = snapshot.theme(i);
		if (!e) continue;
		themeEntries.push({
			role: e.role() ?? "",
			entityTypeId: Number(e.entityTypeId()),
			assetUrl: e.assetUrl() ?? "",
			nineSlice: {
				left: e.nineSliceLeft(), top: e.nineSliceTop(),
				right: e.nineSliceRight(), bottom: e.nineSliceBottom(),
			},
			width: e.width(), height: e.height(),
		});
	}
	const theme = Theme.fromEntries(themeEntries);

	// 3) Build the EditorApp harness.
	const app = await EditorApp.create({
		host,
		kind: "level-editor",
		theme,
	});

	// 4) Wire local state + ops.
	const state = new EditorState({ mapWidth: boot.mapWidth, mapHeight: boot.mapHeight });
	const placementWire = new WSPlacementWire(wire, boot.levelId);
	const restWire = new LevelEditorWire(boot.levelId, boot.mapId);
	const ops = new LevelOps({
		state,
		wire: placementWire as unknown as import("./wire").LevelEditorWire,
		onError: (m) => console.warn("[level-editor]", m),
	});

	// 5) Hydrate state from snapshot body.
	const body = snapshot.levelEditorBody();
	if (body) {
		const initial: Placement[] = [];
		for (let i = 0; i < body.placementsLength(); i++) {
			const p = body.placements(i);
			if (!p) continue;
			const tags: string[] = [];
			for (let j = 0; j < p.tagsLength(); j++) {
				const t = p.tags(j);
				if (t) tags.push(t);
			}
			initial.push(placementFromWire({
				id: Number(p.placementId()),
				entity_type_id: Number(p.entityTypeId()),
				x: p.x(), y: p.y(),
				rotation_degrees: ((p.rotationDegrees() % 360) | 0) as 0 | 90 | 180 | 270,
				instance_overrides: safeParseJSON(p.instanceOverridesJson() ?? ""),
				tags,
			}));
		}
		state.setInitialPlacements(initial);
	}

	// 6) Build palette from snapshot.palette.
	const paletteEntries: HarnessPaletteEntry[] = [];
	const palByID = new Map<number, PaletteAtlasEntry>();
	const catalogEntries: StaticCatalogEntry[] = [];
	for (let i = 0; i < snapshot.paletteLength(); i++) {
		const e = snapshot.palette(i);
		if (!e) continue;
		const entry: PaletteAtlasEntry = {
			id: Number(e.entityTypeId()),
			name: e.name() ?? "",
			class: (e.class_() ?? "logic") as PaletteAtlasEntry["class"],
			sprite_url: e.spriteUrl() ?? "",
			atlas_index: e.atlasIndex(),
			atlas_cols: e.atlasCols(),
			tile_size: e.tileSize(),
		};
		palByID.set(entry.id, entry);
		paletteEntries.push({
			id: entry.id,
			name: entry.name,
			spriteUrl: entry.sprite_url,
			atlasIndex: entry.atlas_index,
			atlasCols: entry.atlas_cols,
			tileSize: entry.tile_size,
		});
		catalogEntries.push(staticCatalogEntry(entry));
	}
	state.addPaletteEntries([...palByID.values()]);
	if (paletteEntries[0]) state.setActiveEntity(palByID.get(paletteEntries[0].id) ?? null);

	const backdropCatalog = await restWire.loadBackdropCatalog().catch(() => ({ entries: [] as PaletteAtlasEntry[] }));
	for (const entry of backdropCatalog.entries) catalogEntries.push(staticCatalogEntry(entry));
	const backdrop = await restWire.loadBackdropTiles().catch(() => ({ tiles: [] }));
	state.setBackdrop(backdrop.tiles.map((t) => ({
		layerId: t.layer_id,
		x: t.x,
		y: t.y,
		entityTypeId: t.entity_type_id,
		rotation: normalizeRotation(t.rotation_degrees),
	})));

	const palette = new PaletteGrid({
		theme,
		width: 224,
		height: 400,
		onSelect: (e) => {
			const a = state.paletteEntry(e.id);
			if (a) state.setActiveEntity(a);
		},
	});
	palette.setEntries(paletteEntries);
	app.slots.sidebar.addChild(sectionLabel("Entities"));
	app.slots.sidebar.addChild(palette);

	// 7) Toolbar — basic tool selectors + undo/redo.
	const toolbar = new Toolbar({ theme, slot: app.slots.toolbar });
	const actions: ToolbarAction[] = [
		{ id: "place", label: "Place", hotkey: "B", icon: "✎", tooltip: "Place - B", active: state.tool === "place" },
		{ id: "select", label: "Select", hotkey: "V", icon: "⬚", tooltip: "Select - V" },
		{ id: "erase", label: "Erase", hotkey: "E", icon: "🅇︎", tooltip: "Erase - E" },
		{ id: "undo", label: "Undo", hotkey: "Ctrl+Z", icon: "↶", tooltip: "Undo - Ctrl+Z", disabled: !state.canUndo() },
		{ id: "redo", label: "Redo", hotkey: "Shift+Z", icon: "↷", tooltip: "Redo - Shift+Z", disabled: !state.canRedo() },
	];
	const renderToolbar = (): void => toolbar.render(updateActiveAction(actions, state.tool, state));
	renderToolbar();
	toolbar.onAction("place",  () => { state.setTool("place"); renderToolbar(); });
	toolbar.onAction("select", () => { state.setTool("select"); renderToolbar(); });
	toolbar.onAction("erase",  () => { state.setTool("erase"); renderToolbar(); });
	toolbar.onAction("undo",   () => { void state.undo().then(renderToolbar); });
	toolbar.onAction("redo",   () => { void state.redo().then(renderToolbar); });

	// 8) Statusbar — placement count + saving indicator.
	const statusbar = new Statusbar({ theme, slot: app.slots.statusbar });
	const renderStatus = (): void => {
		statusbar.render([
			{ id: "tool", text: `Tool: ${state.tool}` },
			{ id: "count", text: `${state.allPlacements().length} placement(s)` },
			{ id: "saving", text: state.pending > 0 ? "saving…" : "" },
		]);
	};
	state.subscribe(renderStatus);
	renderStatus();

	// 9) Inspector — populated when a placement is selected.
	const inspector = new Inspector({
		theme,
		slot: app.slots.inspector,
		onChange: (key, value) => {
			if (state.selection === null) return;
			void ops.patch(state.selection, { [key]: value } as Parameters<LevelOps["patch"]>[1]);
		},
	});
	state.subscribe(() => {
		if (state.selection === null) {
			inspector.clear();
			inspector.setTitle("No entity selected");
			return;
		}
		const p = state.placement(state.selection);
		if (!p) { inspector.clear(); return; }
		const palEntry = state.paletteEntry(p.entityTypeId);
		inspector.setTitle(palEntry ? palEntry.name : `Placement ${p.id}`);
		inspector.render(
			[
				{ key: "x", label: "X", kind: "int", min: 0, max: state.mapWidth() - 1 },
				{ key: "y", label: "Y", kind: "int", min: 0, max: state.mapHeight() - 1 },
				{
					key: "rotation", label: "Rotation", kind: "enum",
					options: [
						{ value: "0", label: "0°" }, { value: "90", label: "90°" },
						{ value: "180", label: "180°" }, { value: "270", label: "270°" },
					],
				},
			],
			{ x: p.x, y: p.y, rotation: String(p.rotation) },
		);
	});
	inspector.setTitle("No entity selected");

	// 10) In-canvas renderer: read-only map backdrop plus mutable
	// placement layer, both cell-aligned in the shared renderable path.
	const mapSurface = new Container();
	mapSurface.position.set(MAP_ORIGIN_X, MAP_ORIGIN_Y);
	app.slots.canvasWrap.addChild(mapSurface);
	const grid = new Graphics();
	drawMapGrid(grid, state.mapWidth(), state.mapHeight());
	mapSurface.addChild(grid);
	const catalog = new StaticAssetCatalog({ entries: catalogEntries });
	const renderLayer = new MapmakerRenderableLayer(catalog, state.mapWidth(), state.mapHeight());
	mapSurface.addChild(renderLayer);
	const knownAssetIDs = new Set(catalogEntries.map((e) => e.id));
	const refreshCanvas = (): void => {
		void renderLayer.setRenderables(buildRenderables({
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
			pendingPlacementIDs: new Set(),
		}));
	};
	state.subscribe(refreshCanvas);
	refreshCanvas();

	// 11) Pointer handling on the canvas wrap.
	app.slots.canvasWrap.eventMode = "static";
	app.slots.canvasWrap.cursor = "crosshair";
	let drag: DragState | null = null;
	const localCellAt = (e: { global: { x: number; y: number } }): Cell => {
		const localPos = mapSurface.toLocal(e.global);
		return {
			x: Math.max(0, Math.min(state.mapWidth() - 1, Math.floor(localPos.x / CELL_SIZE))),
			y: Math.max(0, Math.min(state.mapHeight() - 1, Math.floor(localPos.y / CELL_SIZE))),
		};
	};
	app.slots.canvasWrap.on("pointerdown", (e) => {
		const cell = localCellAt(e);
		const handle = handlePointerDown(state, ops, { button: e.button, cell, spaceDown: false });
		if (handle) drag = handle as DragState;
	});
	app.slots.canvasWrap.on("pointermove", (e) => {
		const cell = localCellAt(e);
		handlePointerMove(state, drag as Parameters<typeof handlePointerMove>[1], cell);
	});
	const finishDrag = (): void => {
		handlePointerUp(state, ops, drag as Parameters<typeof handlePointerUp>[2]);
		drag = null;
	};
	app.slots.canvasWrap.on("pointerup", finishDrag);
	app.slots.canvasWrap.on("pointerupoutside", finishDrag);

	// 12) Keyboard shortcuts.
	const onKey = (e: KeyboardEvent): void => {
		const isText = (e.target as HTMLElement | null)?.tagName === "INPUT";
		if (isText) return;
		const k = e.key.toLowerCase();
		const mod = e.ctrlKey || e.metaKey;
		if (mod && k === "z" && !e.shiftKey) { void state.undo(); e.preventDefault(); return; }
		if (mod && (k === "y" || (k === "z" && e.shiftKey))) { void state.redo(); e.preventDefault(); return; }
		if (mod) return;
		const tools: Record<string, "place" | "select" | "erase"> = { b: "place", v: "select", e: "erase" };
		if (tools[k]) {
			state.setTool(tools[k]!);
			renderToolbar();
			e.preventDefault();
			return;
		}
		if (k === "t") { rotate(state, ops); e.preventDefault(); return; }
		if (k === "delete" || k === "backspace") {
			if (state.selection !== null) { void ops.remove(state.selection); e.preventDefault(); }
		}
	};
	document.addEventListener("keydown", onKey);

	return app;
}

function updateActiveAction(actions: ToolbarAction[], activeId: string, state: EditorState): ToolbarAction[] {
	return actions.map((a) => {
		const next: ToolbarAction = { ...a, active: a.id === activeId };
		if (a.id === "undo") next.disabled = !state.canUndo();
		else if (a.id === "redo") next.disabled = !state.canRedo();
		else if (a.disabled !== undefined) next.disabled = a.disabled;
		return next;
	});
}

function staticCatalogEntry(entry: PaletteAtlasEntry): StaticCatalogEntry {
	return {
		id: entry.id,
		url: entry.sprite_url,
		atlasIndex: entry.atlas_index,
		atlasCols: entry.atlas_cols,
		tileSize: entry.tile_size,
	};
}

function normalizeRotation(r: number): 0 | 90 | 180 | 270 {
	switch (r) {
		case 90: return 90;
		case 180: return 180;
		case 270: return 270;
		default: return 0;
	}
}

function pixelTextStyle(size: number, fill: number) {
	return {
		fontFamily: "DM Mono, Consolas, monospace",
		fontSize: size,
		fontWeight: "700" as const,
		fill,
		letterSpacing: 0,
	};
}

function sectionLabel(text: string): Text {
	const t = new Text({ text, style: pixelTextStyle(12, 0xffd84a) });
	t.layout = { width: "100%", alignSelf: "flex-start", marginTop: 2, marginBottom: 2 };
	return t;
}

function drawMapGrid(g: Graphics, mapW: number, mapH: number): void {
	const w = mapW * CELL_SIZE;
	const h = mapH * CELL_SIZE;
	g.clear();
	g.rect(-8, -8, w + 16, h + 16).fill(0x0b0f18);
	g.rect(0, 0, w, h).fill(0x111827);
	for (let y = 0; y < mapH; y++) {
		for (let x = 0; x < mapW; x++) {
			if ((x + y) % 2 === 0) g.rect(x * CELL_SIZE, y * CELL_SIZE, CELL_SIZE, CELL_SIZE).fill(0x131d2b);
		}
	}
	for (let x = 0; x <= mapW; x++) {
		const px = x * CELL_SIZE;
		g.moveTo(px, 0).lineTo(px, h).stroke({ color: x % 4 === 0 ? 0x2d3b55 : 0x1e2a3d, width: 1 });
	}
	for (let y = 0; y <= mapH; y++) {
		const py = y * CELL_SIZE;
		g.moveTo(0, py).lineTo(w, py).stroke({ color: y % 4 === 0 ? 0x2d3b55 : 0x1e2a3d, width: 1 });
	}
	g.rect(0, 0, w, h).stroke({ color: 0xffd84a, width: 2, alignment: 1 });
}

function safeParseJSON(s: string): Record<string, unknown> {
	if (!s) return {};
	try { return JSON.parse(s) as Record<string, unknown>; }
	catch { return {}; }
}

// Auto-boot when on the level editor surface.
if (typeof document !== "undefined" && document.body?.dataset.surface === "level-editor-entities") {
	void bootLevelEditor();
}
