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

import { Container, Sprite, Texture, Rectangle as PixiRectangle, Assets } from "pixi.js";

import {
	EditorApp,
	EditorWire,
	Theme,
	Toolbar,
	Statusbar,
	type ThemeEntry,
	type ToolbarAction,
} from "@render";
import { EditorKind } from "@proto/editor-kind.js";

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

const Rectangle = PixiRectangle;

interface MapmakerBoot {
	mapId: number;
	mapWidth: number;
	mapHeight: number;
	defaultLayerId: number;
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
				active: state.tool === t,
			})),
			{ id: "undo", label: "Undo" },
			{ id: "redo", label: "Redo" },
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

	// 7) Statusbar.
	const statusbar = new Statusbar({ theme, slot: app.slots.statusbar });
	const renderStatus = (): void => {
		const layer = state.allLayers().find((l) => l.id === state.activeLayer);
		statusbar.render([
			{ id: "tool", text: `Tool: ${state.tool}` },
			{ id: "layer", text: `Layer: ${layer?.name ?? state.activeLayer}` },
			{ id: "count", text: `${state.tileCount()} tile(s) · ${state.lockCount()} locked` },
			{ id: "saving", text: state.pending > 0 ? "saving…" : "" },
		]);
	};
	state.subscribe(renderStatus);
	renderStatus();

	// 8) In-canvas tile renderer. Per-tile Sprite atlas-sliced from
	//    the entity_type's sheet. We keep a sprite per (layer, x, y)
	//    cell and tear/rebuild as state changes.
	const inCanvasContainer = new Container();
	app.slots.canvasWrap.addChild(inCanvasContainer);
	const cellSize = 32;
	const sprites = new Map<string, Sprite>();
	const tileKeyOf = (t: MapTile): string => `${t.layerId}:${t.x}:${t.y}`;

	const refreshCanvas = (): void => {
		const present = new Set<string>();
		for (const t of state.allTiles()) {
			const key = tileKeyOf(t);
			present.add(key);
			let sprite = sprites.get(key);
			if (!sprite) {
				sprite = new Sprite();
				sprites.set(key, sprite);
				inCanvasContainer.addChild(sprite);
				void loadTileTextureFor(sprite, t, snapshot);
			}
			sprite.position.set(t.x * cellSize, t.y * cellSize);
			sprite.angle = t.rotation;
		}
		for (const [key, sprite] of sprites) {
			if (!present.has(key)) {
				inCanvasContainer.removeChild(sprite);
				sprite.destroy();
				sprites.delete(key);
			}
		}
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

	const cellAt = (e: { global: { x: number; y: number } }): Cell => {
		const localPos = app.slots.canvasWrap.toLocal(e.global);
		return {
			x: Math.max(0, Math.min(state.mapWidth() - 1, Math.floor(localPos.x / cellSize))),
			y: Math.max(0, Math.min(state.mapHeight() - 1, Math.floor(localPos.y / cellSize))),
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
		const mods = { shift: e.shiftKey, alt: e.altKey };
		beginStroke(cell, mods);
	});
	app.slots.canvasWrap.on("pointermove", (e) => {
		const cell = cellAt(e);
		const mods = { shift: e.shiftKey, alt: e.altKey };
		continueStroke(cell, mods);
	});
	app.slots.canvasWrap.on("pointerup", (e) => endStroke(cellAt(e)));
	app.slots.canvasWrap.on("pointerupoutside", (e) => endStroke(cellAt(e)));

	// 10) Keyboard shortcuts.
	const onKey = (e: KeyboardEvent): void => {
		const isText = (e.target as HTMLElement | null)?.tagName === "INPUT";
		if (isText) return;
		const k = e.key.toLowerCase();
		const mod = e.ctrlKey || e.metaKey;
		if (mod) return;
		const map: Record<string, Tool> = { b: "brush", r: "rect", f: "fill", i: "eyedrop", e: "eraser", l: "lock", s: "sample" };
		if (map[k]) {
			state.setTool(map[k]!);
			renderToolbar();
			e.preventDefault();
		}
	};
	document.addEventListener("keydown", onKey);

	return app;
}

async function loadTileTextureFor(
	sprite: Sprite,
	tile: MapTile,
	snapshot: import("@proto/editor-snapshot.js").EditorSnapshot,
): Promise<void> {
	// Look up the palette entry for this tile's entity_type from
	// the snapshot's palette.
	for (let i = 0; i < snapshot.paletteLength(); i++) {
		const e = snapshot.palette(i);
		if (!e) continue;
		if (Number(e.entityTypeId()) !== tile.entityTypeId) continue;
		const url = e.spriteUrl() ?? "";
		if (!url) return;
		try {
			const base = await Assets.load<Texture>(url);
			if (!base || !base.source) return;
			base.source.scaleMode = "nearest";
			const ts = e.tileSize() || 32;
			const cols = Math.max(1, e.atlasCols());
			const sx = (e.atlasIndex() % cols) * ts;
			const sy = Math.floor(e.atlasIndex() / cols) * ts;
			sprite.texture = new Texture({
				source: base.source,
				frame: new Rectangle(sx, sy, ts, ts),
			});
		} catch { /* placeholder remains empty */ }
		return;
	}
}

if (typeof document !== "undefined" && document.body?.dataset.surface === "mapmaker") {
	void bootMapmaker();
}
