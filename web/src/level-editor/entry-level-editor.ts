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

import { Container, Sprite, Texture, Rectangle as PixiRectangle, Assets } from "pixi.js";

// Rectangle is referenced once below (texture frame); aliasing
// avoids a name clash with any future local Rect type and keeps
// the import explicit at the call site.
const Rectangle = PixiRectangle;

import {
	EditorApp,
	EditorWire,
	Theme,
	PaletteGrid,
	Inspector,
	Statusbar,
	Toolbar,
	type ThemeEntry,
	type PaletteEntry as HarnessPaletteEntry,
	type ToolbarAction,
} from "@render";
import { EditorKind } from "@proto/editor-kind.js";

import { EditorState } from "./state";
import { LevelOps } from "./ops";
import { WSPlacementWire } from "./ws-wire";
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
	}
	state.addPaletteEntries([...palByID.values()]);

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
	app.slots.sidebar.addChild(palette);

	// 7) Toolbar — basic tool selectors + undo/redo.
	const toolbar = new Toolbar({ theme, slot: app.slots.toolbar });
	const actions: ToolbarAction[] = [
		{ id: "place", label: "Place (B)", active: state.tool === "place" },
		{ id: "select", label: "Select (V)" },
		{ id: "erase", label: "Erase (E)" },
		{ id: "undo", label: "Undo" },
		{ id: "redo", label: "Redo" },
	];
	toolbar.render(actions);
	toolbar.onAction("place",  () => { state.setTool("place");  toolbar.render(updateActiveAction(actions, "place")); });
	toolbar.onAction("select", () => { state.setTool("select"); toolbar.render(updateActiveAction(actions, "select")); });
	toolbar.onAction("erase",  () => { state.setTool("erase");  toolbar.render(updateActiveAction(actions, "erase")); });
	toolbar.onAction("undo",   () => { void state.undo(); });
	toolbar.onAction("redo",   () => { void state.redo(); });

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

	// 10) In-canvas placement renderer. Each placement is a Sprite
	//     atlas-sliced from its entity_type's sheet. We render in
	//     the Scene root (camera-scaled space). A future pass can
	//     use the unified Renderable path.
	const inCanvasContainer = new Container();
	app.slots.canvasWrap.addChild(inCanvasContainer);
	const cellSize = 32;
	const sprites = new Map<number, Sprite>();

	const refreshCanvas = (): void => {
		const present = new Set<number>();
		for (const p of state.allPlacements()) {
			present.add(p.id);
			let sprite = sprites.get(p.id);
			if (!sprite) {
				sprite = new Sprite();
				sprites.set(p.id, sprite);
				inCanvasContainer.addChild(sprite);
				void loadSpriteTextureFor(sprite, p, palByID);
			}
			sprite.position.set(p.x * cellSize, p.y * cellSize);
			sprite.angle = p.rotation;
		}
		for (const [id, sprite] of sprites) {
			if (!present.has(id)) {
				inCanvasContainer.removeChild(sprite);
				sprite.destroy();
				sprites.delete(id);
			}
		}
	};
	state.subscribe(refreshCanvas);
	refreshCanvas();

	// 11) Pointer handling on the canvas wrap.
	app.slots.canvasWrap.eventMode = "static";
	app.slots.canvasWrap.cursor = "crosshair";
	let drag: DragState | null = null;
	const localCellAt = (e: { global: { x: number; y: number } }): Cell => {
		const localPos = app.slots.canvasWrap.toLocal(e.global);
		return {
			x: Math.max(0, Math.min(state.mapWidth() - 1, Math.floor(localPos.x / cellSize))),
			y: Math.max(0, Math.min(state.mapHeight() - 1, Math.floor(localPos.y / cellSize))),
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
			toolbar.render(updateActiveAction(actions, tools[k]!));
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

function updateActiveAction(actions: ToolbarAction[], activeId: string): ToolbarAction[] {
	return actions.map((a) => ({ ...a, active: a.id === activeId }));
}

async function loadSpriteTextureFor(
	sprite: Sprite,
	placement: Placement,
	pal: ReadonlyMap<number, PaletteAtlasEntry>,
): Promise<void> {
	const entry = pal.get(placement.entityTypeId);
	if (!entry || !entry.sprite_url) return;
	try {
		const base = await Assets.load<Texture>(entry.sprite_url);
		if (!base || !base.source) return;
		base.source.scaleMode = "nearest";
		const ts = entry.tile_size || 32;
		const cols = Math.max(1, entry.atlas_cols);
		const sx = (entry.atlas_index % cols) * ts;
		const sy = Math.floor(entry.atlas_index / cols) * ts;
		sprite.texture = new Texture({
			source: base.source,
			frame: new Rectangle(sx, sy, ts, ts),
		});
	} catch { /* placeholder remains empty */ }
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
