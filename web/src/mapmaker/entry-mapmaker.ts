// Boxland — mapmaker page boot (Pixi-rendered).
//
// Same architecture as the level-editor: minimal templ shell with
// a single host element + WS url/ticket; a Pixi-rendered scene
// inside the host driven by the EditorApp harness.
//
// v1 supports the brush + eraser tools (and the rect / fill / lock
// / sample / procedural-mode tools fall in subsequent passes). The
// pure-logic state / tools / render-bridge modules under this
// directory are unchanged; we just inject a WS-backed wire instead
// of the legacy REST one.

import { Container, Graphics, Text } from "pixi.js";
import * as flatbuffers from "flatbuffers";

import {
	EditorApp,
	EditorWire,
	Theme,
	Toolbar,
	Statusbar,
	PaletteGrid,
	StaticAssetCatalog,
	NineSlice,
	Roles,
	type ThemeEntry,
	type PaletteEntry as HarnessPaletteEntry,
	type StaticCatalogEntry,
	type ToolbarAction,
} from "@render";
import { EditorKind } from "@proto/editor-kind.js";
import { EditorDiffKind } from "@proto/editor-diff-kind.js";
import { EditorMapTile } from "@proto/editor-map-tile.js";
import { EditorMapTilePoint } from "@proto/editor-map-tile-point.js";

import { MapmakerState, newStrokeCtx, type StrokeCtx } from "./state";
import { WSMapmakerWire } from "./ws-wire";
import {
	stamp,
	stampRect,
	floodFill,
} from "./tools";
import {
	type Cell,
	type MapTile,
	type Tool,
	type MapLayer,
	tileFromWire,
} from "./types";
import { buildRenderables } from "./render-bridge";
import { MapmakerRenderableLayer } from "./render-layer";

const CELL_SIZE = 32;
const MAP_ORIGIN_X = 18;
const MAP_ORIGIN_Y = 18;
const MIN_ZOOM = 0.5;
const MAX_ZOOM = 4;

interface MapmakerBoot {
	mapId: number;
	mapWidth: number;
	mapHeight: number;
	defaultLayerId: number;
}

interface LayerUIOptions {
	visible: boolean;
	editable: boolean;
}

function readBoot(host: HTMLElement): MapmakerBoot {
	const need = (k: string): string => {
		const v = host.getAttribute(k);
		if (v == null) throw new Error(`mapmaker boot: missing ${k}`);
		return v;
	};
	return {
		mapId: Number(need("data-map-id")),
		mapWidth: Number(need("data-map-w")),
		mapHeight: Number(need("data-map-h")),
		defaultLayerId: Number(host.getAttribute("data-default-layer-id") ?? "0"),
	};
}

function readDataAttr(host: HTMLElement, name: string): string {
	return host.getAttribute(name) ?? "";
}

export async function bootMapmaker(): Promise<EditorApp | null> {
	const host = document.querySelector("[data-bx-mapmaker-host]") as HTMLElement | null;
	if (!host) return null;
	const boot = readBoot(host);
	const wsURL = readDataAttr(host, "data-bx-ws-url");
	const wsTicket = readDataAttr(host, "data-bx-ws-ticket");
	if (!wsURL || !wsTicket) {
		console.error("[mapmaker] missing data-bx-ws-url or data-bx-ws-ticket");
		return null;
	}

	// 1) Open the WS + join the mapmaker session.
	const wire = await EditorWire.connect({ wsURL, wsTicket });
	const snapshot = await wire.joinMapmaker(boot.mapId);
	if (snapshot.kind() !== EditorKind.Mapmaker) {
		throw new Error(`mapmaker: snapshot kind mismatch (got ${snapshot.kind()})`);
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
		kind: "mapmaker",
		theme,
	});

	// 4) Hydrate state from snapshot body.
	const state = new MapmakerState({
		mapWidth: boot.mapWidth,
		mapHeight: boot.mapHeight,
		defaultLayerId: boot.defaultLayerId,
	});
	const layerOptions = new Map<number, LayerUIOptions>();
	const ensureLayerOptions = (id: number): LayerUIOptions => {
		let opts = layerOptions.get(id);
		if (!opts) {
			opts = { visible: true, editable: true };
			layerOptions.set(id, opts);
		}
		return opts;
	};
	let refreshCanvas = (): void => {};
	const body = snapshot.mapmakerBody();
	if (body) {
		const layers: MapLayer[] = [];
		for (let i = 0; i < body.layersLength(); i++) {
			const l = body.layers(i);
			if (!l) continue;
			layers.push({
				id: l.layerId(),
				name: l.name() ?? "",
				kind: (l.kind() === "lighting" ? "lighting" : "tile"),
				yShift: l.ord(),
				ySort: l.ySortEntities(),
			});
		}
		state.setLayers(layers);
		for (const layer of layers) ensureLayerOptions(layer.id);

		const tiles: MapTile[] = [];
		for (let i = 0; i < body.tilesLength(); i++) {
			const t = body.tiles(i);
			if (!t) continue;
			tiles.push(tileFromWire({
				layer_id: t.layerId(),
				x: t.x(), y: t.y(),
				entity_type_id: Number(t.entityTypeId()),
				rotation_degrees: t.rotationDegrees(),
			}));
		}
		state.setInitialTiles(tiles);

		const locks: MapTile[] = [];
		for (let i = 0; i < body.locksLength(); i++) {
			const t = body.locks(i);
			if (!t) continue;
			locks.push(tileFromWire({
				layer_id: t.layerId(),
				x: t.x(), y: t.y(),
				entity_type_id: Number(t.entityTypeId()),
				rotation_degrees: t.rotationDegrees(),
			}));
		}
		state.setInitialLocks(locks);
	}

	// 5) Wire WS-backed wire.
	const placementWire = new WSMapmakerWire(wire, boot.mapId);
	wire.onDiff((diff) => {
		const bytes = diffBodyBytes(diff);
		switch (diff.kind()) {
			case EditorDiffKind.TilePlaced:
				if (!bytes) return;
				state.upsertTile(mapTileFromEditorBytes(bytes));
				return;
			case EditorDiffKind.TileErased: {
				if (!bytes) return;
				const p = mapTilePointFromEditorBytes(bytes);
				state.deleteTile(p.layerId, p.x, p.y);
				return;
			}
			case EditorDiffKind.LockAdded:
				if (!bytes) return;
				state.upsertLock(mapTileFromEditorBytes(bytes));
				return;
			case EditorDiffKind.LockRemoved: {
				if (!bytes) return;
				const p = mapTilePointFromEditorBytes(bytes);
				state.deleteLock(p.layerId, p.x, p.y);
				return;
			}
			case EditorDiffKind.HistoryChanged:
				renderToolbar();
				return;
		}
	});

	// Snapshot palette. The same records feed the left rail and
	// texture lookup for the map surface.
	const paletteEntries: HarnessPaletteEntry[] = [];
	const catalogEntries: StaticCatalogEntry[] = [];
	for (let i = 0; i < snapshot.paletteLength(); i++) {
		const e = snapshot.palette(i);
		if (!e) continue;
		const entry: HarnessPaletteEntry = {
			id: Number(e.entityTypeId()),
			name: e.name() ?? "",
			spriteUrl: e.spriteUrl() ?? "",
			atlasIndex: e.atlasIndex(),
			atlasCols: e.atlasCols(),
			tileSize: e.tileSize(),
		};
		paletteEntries.push(entry);
		catalogEntries.push({
			id: entry.id,
			url: entry.spriteUrl,
			atlasIndex: entry.atlasIndex,
			atlasCols: entry.atlasCols,
			tileSize: entry.tileSize,
		});
	}
	if (paletteEntries[0]) state.setActiveEntity(paletteEntries[0].id);

	// 6) Toolbar — five tools + undo/redo. v1 wires brush + eraser
	//    end-to-end; rect / fill / eyedrop / lock / sample land in
	//    follow-up passes (the local stamp() function already
	//    handles them; we just need to dispatch the right wire
	//    calls per stroke end).
	const toolbar = new Toolbar({ theme, slot: app.slots.toolbar });
	const tools: Tool[] = ["brush", "rect", "fill", "eyedrop", "eraser", "lock"];
	const renderToolbar = (): void => {
		toolbar.render([
			...tools.map<ToolbarAction>((t) => ({
				id: t,
				label: t.charAt(0).toUpperCase() + t.slice(1),
				hotkey: hotkeyForTool(t),
				active: state.tool === t,
			})),
			{ id: "undo", label: "Undo", hotkey: "Ctrl+Z", disabled: !state.canUndo() },
			{ id: "redo", label: "Redo", hotkey: "Shift+Z", disabled: !state.canRedo() },
		]);
	};
	for (const t of tools) {
		toolbar.onAction(t, () => { state.setTool(t); renderToolbar(); });
	}
	toolbar.onAction("undo", () => {
		// v1: the WS protocol carries EditorUndo at the session
		// layer; clients dispatch by sending a DesignerCommand.
		// For now we no-op locally — full undo wiring lands when
		// we surface the editor history through the WS dispatch
		// helpers (Phase 4 already added the opcode + handler).
	});
	toolbar.onAction("redo", () => { /* same: see undo above */ });
	renderToolbar();

	// 6b) Left/right rails. These are intentionally simple, but
	// already use the Gradient slot/frame primitives through the
	// shared PaletteGrid and framed layout slots.
	app.slots.sidebar.addChild(sectionLabel("Layers"));
	const layerList = new Container();
	layerList.layout = { width: "100%", flexDirection: "column", gap: 4 };
	app.slots.sidebar.addChild(layerList);
	const renderLayers = (): void => renderLayerList({
		root: layerList,
		state,
		theme,
		layerOptions,
		ensureLayerOptions,
		onChange: () => {
			renderToolbar();
			refreshCanvas();
			queueMicrotask(renderLayers);
		},
	});
	state.subscribe(renderLayers);
	renderLayers();

	app.slots.sidebar.addChild(sectionLabel("Palette"));
	const palette = new PaletteGrid({
		theme,
		width: 224,
		height: 680,
		cellSize: 36,
		onSelect: (e) => state.setActiveEntity(e.id),
	});
	palette.setEntries(paletteEntries);
	if (paletteEntries[0]) palette.select(paletteEntries[0].id);
	app.slots.sidebar.addChild(palette);

	app.slots.inspector.addChild(sectionLabel("Mapmaker"));
	const inspectorText = new Text({ text: "", style: pixelTextStyle(11, 0xa9b0c0) });
	inspectorText.layout = { width: "100%", alignSelf: "flex-start" };
	app.slots.inspector.addChild(inspectorText);
	const renderInspector = (): void => {
		const active = paletteEntries.find((p) => p.id === state.activeEntity);
		inspectorText.text = [
			`Map: ${state.mapWidth()} x ${state.mapHeight()}`,
			`Layer: ${state.allLayers().find((l) => l.id === state.activeLayer)?.name ?? "none"}`,
			`Brush: ${active?.name ?? "none"}`,
			`Cell: ${state.cursorCell ? `${state.cursorCell.x}, ${state.cursorCell.y}` : "--"}`,
			`Rotation: ${state.activeRotation} deg`,
			`Tiles: ${state.tileCount()}`,
			`Locks: ${state.lockCount()}`,
		].join("\n");
	};
	state.subscribe(renderInspector);
	renderInspector();

	// 7) Statusbar.
	const statusbar = new Statusbar({ theme, slot: app.slots.statusbar });
	const renderStatus = (): void => {
		const layer = state.allLayers().find((l) => l.id === state.activeLayer);
		statusbar.render([
			{ id: "tool", text: `Tool: ${state.tool}` },
			{ id: "layer", text: `Layer: ${layer?.name ?? state.activeLayer}` },
			{ id: "cell", text: state.cursorCell ? `Cell: ${state.cursorCell.x},${state.cursorCell.y}` : "Cell: --" },
			{ id: "count", text: `${state.tileCount()} tile(s) · ${state.lockCount()} locked` },
			{ id: "saving", text: state.pending > 0 ? "saving…" : "" },
		]);
	};
	state.subscribe(renderStatus);
	renderStatus();

	// 8) In-canvas tile renderer. The Mapmaker state is adapted
	//    through render-bridge.ts into the shared renderer's
	//    Renderable[] contract; this layer paints those renderables
	//    inside the editor viewport's pan/zoom surface.
	const mapSurface = new Container();
	mapSurface.position.set(MAP_ORIGIN_X, MAP_ORIGIN_Y);
	app.slots.canvasWrap.addChild(mapSurface);
	const grid = new Graphics();
	drawMapGrid(grid, boot.mapWidth, boot.mapHeight);
	mapSurface.addChild(grid);
	const renderLayer = new MapmakerRenderableLayer(
		new StaticAssetCatalog({ entries: catalogEntries }),
		state.mapWidth(),
		state.mapHeight(),
	);
	mapSurface.addChild(renderLayer);
	const cursorOverlay = new Graphics();
	mapSurface.addChild(cursorOverlay);
	const renderCursor = (): void => drawCursor(cursorOverlay, state.cursorCell, state.mapWidth(), state.mapHeight());
	state.subscribe(renderCursor);
	renderCursor();
	refreshCanvas = (): void => {
		const visibleLayers = new Set<number>();
		for (const layer of state.allLayers()) {
			if (ensureLayerOptions(layer.id).visible) visibleLayers.add(layer.id);
		}
		void renderLayer.setRenderables(buildRenderables({
			tiles: state.allTiles().filter((t) => visibleLayers.has(t.layerId)),
			procPreview: state.procPreview?.filter((t) => visibleLayers.has(t.layerId)) ?? null,
			stampGhost: state.activeEntity > 0 ? { entityID: state.activeEntity, rotation: state.activeRotation } : null,
			cursorCell: state.cursorCell,
			dragRectFrom: state.dragRectFrom,
			dragRectTo: state.dragRectTo,
			sampleRect: state.sampleRect,
			locks: state.allLocks().filter((t) => visibleLayers.has(t.layerId)),
			tool: state.tool,
			activeLayer: state.activeLayer,
			mapWidth: state.mapWidth(),
			mapHeight: state.mapHeight(),
		}));
	};
	state.subscribe(refreshCanvas);
	refreshCanvas();

	// 9) Pointer handling: brush + eraser strokes. rect / fill /
	//    lock / sample wire up later as the surface matures.
	app.slots.canvasWrap.eventMode = "static";
	app.slots.canvasWrap.cursor = "crosshair";
	let strokeCtx: StrokeCtx | null = null;
	let isStroking = false;
	let rectStart: Cell | null = null;
	let spaceDown = false;
	let panStart: { x: number; y: number; surfaceX: number; surfaceY: number } | null = null;

	const cellAt = (e: { global: { x: number; y: number } }): Cell => {
		const localPos = mapSurface.toLocal(e.global);
		return {
			x: Math.max(0, Math.min(state.mapWidth() - 1, Math.floor(localPos.x / CELL_SIZE))),
			y: Math.max(0, Math.min(state.mapHeight() - 1, Math.floor(localPos.y / CELL_SIZE))),
		};
	};

	const beginStroke = (cell: Cell, mods: { shift?: boolean; alt?: boolean }): void => {
		strokeCtx = newStrokeCtx();
		isStroking = true;
		// Rect tool stages the start cell on pointerdown and only
		// commits on pointerup (one batch). Other tools stamp per
		// cell during the drag.
		if (state.tool === "rect") {
			rectStart = cell;
			return;
		}
		applyStamp(cell, mods);
	};
	const continueStroke = (cell: Cell, mods: { shift?: boolean; alt?: boolean }): void => {
		if (!isStroking) return;
		// Rect previews on pointerup — no per-move work for now;
		// future iteration: draw a marquee outline here.
		if (state.tool === "rect") return;
		applyStamp(cell, mods);
	};
	const endStroke = (cell: Cell | null): void => {
		if (!isStroking) return;
		// Commit the rect stroke as one batch.
		if (state.tool === "rect" && rectStart && cell) {
			const out = stampRect(state, strokeCtx ?? newStrokeCtx(), rectStart, cell);
			shipStamp(out);
		}
		isStroking = false;
		strokeCtx = null;
		rectStart = null;
	};

	// shipStamp dispatches the wire calls implied by a StampResult.
	// Centralized so brush / lock / fill / rect all use the same
	// per-effect routing.
	const shipStamp = (out: ReturnType<typeof stamp>): void => {
		if (out.placed.length) placementWire.placeTiles(out.placed);
		if (out.erased.length) {
			const points = out.erased.map((t) => ({ layerId: t.layerId, x: t.x, y: t.y }));
			placementWire.eraseTiles(points);
		}
		if (out.locked.length) placementWire.lockTiles(out.locked);
		if (out.unlocked.length) {
			const points = out.unlocked.map((t) => ({ layerId: t.layerId, x: t.x, y: t.y }));
			placementWire.unlockTiles(points);
		}
	};

	const applyStamp = (cell: Cell, mods: { shift?: boolean; alt?: boolean }): void => {
		if (!ensureLayerOptions(state.activeLayer).editable) return;
		if (state.tool === "fill") {
			const out = floodFill(state, strokeCtx ?? newStrokeCtx(), cell);
			shipStamp(out);
			return;
		}
		// brush, eraser, eyedrop, lock all flow through stamp().
		// eyedrop self-mutates state with no wire effect; the
		// shipStamp call below is a no-op for it.
		const out = stamp(state, strokeCtx, cell, mods);
		shipStamp(out);
	};

	app.slots.canvasWrap.on("pointerdown", (e) => {
		const cell = cellAt(e);
		state.setCursorCell(cell);
		if (spaceDown || e.button === 1) {
			panStart = {
				x: e.global.x,
				y: e.global.y,
				surfaceX: mapSurface.position.x,
				surfaceY: mapSurface.position.y,
			};
			app.slots.canvasWrap.cursor = "grabbing";
			return;
		}
		const mods = { shift: e.shiftKey, alt: e.altKey };
		beginStroke(cell, mods);
	});
	app.slots.canvasWrap.on("pointermove", (e) => {
		if (panStart) {
			mapSurface.position.set(
				panStart.surfaceX + (e.global.x - panStart.x),
				panStart.surfaceY + (e.global.y - panStart.y),
			);
			return;
		}
		const cell = cellAt(e);
		state.setCursorCell(cell);
		const mods = { shift: e.shiftKey, alt: e.altKey };
		continueStroke(cell, mods);
	});
	app.slots.canvasWrap.on("pointerout", () => state.setCursorCell(null));
	app.slots.canvasWrap.on("pointerup", (e) => {
		if (panStart) {
			panStart = null;
			app.slots.canvasWrap.cursor = spaceDown ? "grab" : "crosshair";
			return;
		}
		endStroke(cellAt(e));
	});
	app.slots.canvasWrap.on("pointerupoutside", (e) => {
		if (panStart) {
			panStart = null;
			app.slots.canvasWrap.cursor = spaceDown ? "grab" : "crosshair";
			return;
		}
		endStroke(cellAt(e));
	});
	app.pixi.pixi.canvas.addEventListener("wheel", (e) => {
		if (!host.contains(e.target as Node)) return;
		const global = { x: e.offsetX, y: e.offsetY };
		const before = mapSurface.toLocal(global);
		const factor = e.deltaY < 0 ? 1.1 : 0.9;
		const next = Math.max(MIN_ZOOM, Math.min(MAX_ZOOM, mapSurface.scale.x * factor));
		if (next === mapSurface.scale.x) return;
		mapSurface.scale.set(next);
		const after = mapSurface.toGlobal(before);
		mapSurface.position.x += global.x - after.x;
		mapSurface.position.y += global.y - after.y;
		e.preventDefault();
	}, { passive: false });

	// 10) Keyboard shortcuts.
	const onKey = (e: KeyboardEvent): void => {
		const isText = (e.target as HTMLElement | null)?.tagName === "INPUT";
		if (isText) return;
		const k = e.key.toLowerCase();
		const mod = e.ctrlKey || e.metaKey;
		if (e.code === "Space") {
			spaceDown = true;
			app.slots.canvasWrap.cursor = "grab";
			e.preventDefault();
			return;
		}
		if (mod) return;
		const map: Record<string, Tool> = { b: "brush", r: "rect", f: "fill", i: "eyedrop", e: "eraser", l: "lock", s: "sample" };
		if (map[k]) {
			state.setTool(map[k]!);
			renderToolbar();
			e.preventDefault();
		}
	};
	document.addEventListener("keydown", onKey);
	document.addEventListener("keyup", (e) => {
		if (e.code !== "Space") return;
		spaceDown = false;
		if (!panStart) app.slots.canvasWrap.cursor = "crosshair";
	});
	app.relayout();

	return app;
}

function hotkeyForTool(t: Tool): string {
	switch (t) {
		case "brush": return "B";
		case "rect": return "R";
		case "fill": return "F";
		case "eyedrop": return "I";
		case "eraser": return "E";
		case "lock": return "L";
		case "sample": return "S";
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

function renderLayerList(opts: {
	root: Container;
	state: MapmakerState;
	theme: Theme;
	layerOptions: Map<number, LayerUIOptions>;
	ensureLayerOptions: (id: number) => LayerUIOptions;
	onChange: () => void;
}): void {
	const { root, state, theme, ensureLayerOptions, onChange } = opts;
	root.removeChildren().forEach((c) => c.destroy());
	for (const layer of state.allLayers()) {
		const row = new Container();
		row.layout = { width: 220, height: 56 };
		row.eventMode = "static";
		row.cursor = "pointer";
		const active = layer.id === state.activeLayer;
		const layerOpts = ensureLayerOptions(layer.id);
		const bg = new NineSlice({
			theme,
			role: active ? Roles.ButtonMdPressA : Roles.FrameLite,
			width: 220,
			height: 56,
			fallbackColor: active ? 0x2f5eaa : 0x151d2c,
		});
		row.addChild(bg);
		const label = new Text({
			text: layer.name,
			style: pixelTextStyle(11, active ? 0xffd84a : 0xe8ecf2),
		});
		label.position.set(8, 7);
		row.addChild(label);
		const meta = new Text({
			text: `${layer.kind}  ord ${layer.yShift}${layer.ySort ? "  y-sort" : ""}`,
			style: pixelTextStyle(9, 0x9aa8bd),
		});
		meta.position.set(8, 24);
		row.addChild(meta);
		const visible = layerToggle({
			theme,
			label: layerOpts.visible ? "VIS" : "HID",
			on: layerOpts.visible,
			x: 132,
			y: 8,
			onTap: () => {
				layerOpts.visible = !layerOpts.visible;
				onChange();
			},
		});
		row.addChild(visible);
		const editable = layerToggle({
			theme,
			label: layerOpts.editable ? "EDIT" : "LOCK",
			on: layerOpts.editable,
			x: 132,
			y: 31,
			onTap: () => {
				layerOpts.editable = !layerOpts.editable;
				onChange();
			},
		});
		row.addChild(editable);
		row.on("pointertap", () => {
			state.setActiveLayer(layer.id);
			onChange();
		});
		root.addChild(row);
	}
}

function layerToggle(opts: {
	theme: Theme;
	label: string;
	on: boolean;
	x: number;
	y: number;
	onTap: () => void;
}): Container {
	const c = new Container();
	c.position.set(opts.x, opts.y);
	c.eventMode = "static";
	c.cursor = "pointer";
	c.addChild(new NineSlice({
		theme: opts.theme,
		role: opts.on ? Roles.ButtonSmPressA : Roles.ButtonSmReleaseA,
		width: 78,
		height: 18,
		fallbackColor: opts.on ? 0x3b66bc : 0x1b2638,
	}));
	const t = new Text({
		text: opts.label,
		style: pixelTextStyle(9, opts.on ? 0xffd84a : 0xaeb8ca),
	});
	t.position.set(8, 3);
	c.addChild(t);
	c.on("pointertap", (ev) => {
		ev.stopPropagation();
		opts.onTap();
	});
	return c;
}

function drawMapGrid(g: Graphics, mapW: number, mapH: number): void {
	const w = mapW * CELL_SIZE;
	const h = mapH * CELL_SIZE;
	g.clear();
	g.rect(-8, -8, w + 16, h + 16).fill(0x0b0f18);
	g.rect(0, 0, w, h).fill(0x111827);
	for (let y = 0; y < mapH; y++) {
		for (let x = 0; x < mapW; x++) {
			if ((x + y) % 2 === 0) {
				g.rect(x * CELL_SIZE, y * CELL_SIZE, CELL_SIZE, CELL_SIZE).fill(0x131d2b);
			}
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

function drawCursor(g: Graphics, cell: Cell | null, mapW: number, mapH: number): void {
	g.clear();
	if (!cell || cell.x < 0 || cell.y < 0 || cell.x >= mapW || cell.y >= mapH) return;
	const x = cell.x * CELL_SIZE;
	const y = cell.y * CELL_SIZE;
	g.rect(x, y, CELL_SIZE, CELL_SIZE)
		.fill({ color: 0xffd84a, alpha: 0.12 })
		.rect(x, y, CELL_SIZE, CELL_SIZE)
		.stroke({ color: 0xffd84a, width: 2, alignment: 1 });
	g.rect(x + 5, y + 5, CELL_SIZE - 10, CELL_SIZE - 10)
		.stroke({ color: 0x10131c, width: 1, alignment: 1 });
}

function diffBodyBytes(diff: import("@proto/editor-diff.js").EditorDiff): Uint8Array | null {
	const len = diff.bodyLength();
	if (len === 0) return null;
	const out = new Uint8Array(len);
	for (let i = 0; i < len; i++) out[i] = diff.body(i)!;
	return out;
}

function mapTileFromEditorBytes(bytes: Uint8Array): MapTile {
	const t = EditorMapTile.getRootAsEditorMapTile(new flatbuffers.ByteBuffer(bytes));
	return tileFromWire({
		layer_id: t.layerId(),
		x: t.x(),
		y: t.y(),
		entity_type_id: Number(t.entityTypeId()),
		rotation_degrees: t.rotationDegrees(),
	});
}

function mapTilePointFromEditorBytes(bytes: Uint8Array): { layerId: number; x: number; y: number } {
	const p = EditorMapTilePoint.getRootAsEditorMapTilePoint(new flatbuffers.ByteBuffer(bytes));
	return { layerId: p.layerId(), x: p.x(), y: p.y() };
}

if (typeof document !== "undefined" && document.body?.dataset.surface === "mapmaker") {
	void bootMapmaker();
}
