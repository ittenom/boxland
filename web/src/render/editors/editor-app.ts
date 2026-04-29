// Boxland — editor app harness.
//
// Boots a `BoxlandApp` (Pixi renderer), opens a designer-realm
// WebSocket, awaits the editor snapshot, builds the flexbox
// layout, and exposes named slot containers for the surface-
// specific entry script to populate.
//
// This is the "BoxlandApp + EditorHarness + flexbox chrome"
// composition that both Mapmaker and Level Editor sit on top of.
// It is renderer-side: WS protocol details + opcodes live in
// `web/src/render/editors/editor-wire.ts` (Phase 7); this module
// just wires the pieces together.

import "./layout-init";

import { Container } from "pixi.js";

import { BoxlandApp } from "../app";
import { EditorHarness } from "../editor-harness";
import { Scene } from "../scene";
import type { Camera } from "../types";
import { StaticAssetCatalog } from "../static-catalog";
import { Theme } from "../ui";
import {
	buildEditorLayout,
	resizeEditorLayout,
	type EditorSlots,
} from "./editor-layout";
import type { EditorKind } from "./types";

export interface EditorAppOptions {
	/** DOM host the Pixi canvas mounts inside. The container's
	 *  `clientWidth`/`Height` drives the layout via
	 *  ResizeObserver. */
	host: HTMLElement;

	/** Editor kind — picks which join opcode to send + which
	 *  surface-specific snapshot fields to expect. */
	kind: EditorKind;

	/** Theme built from the snapshot's `ui_theme` payload. */
	theme: Theme;

	/** Background color (0xRRGGBB). Default deep navy. */
	background?: number;
}

/** EditorApp owns the Pixi application + the layout + the
 *  EditorHarness scheduler. Surface-specific entry scripts call
 *  EditorApp.create(...) and receive the slot references they
 *  need to populate. */
export class EditorApp {
	readonly pixi: BoxlandApp;
	readonly slots: EditorSlots;
	readonly harness: EditorHarness;

	private readonly host: HTMLElement;
	private resizeObserver: ResizeObserver | null = null;

	private constructor(pixi: BoxlandApp, slots: EditorSlots, harness: EditorHarness, host: HTMLElement) {
		this.pixi = pixi;
		this.slots = slots;
		this.harness = harness;
		this.host = host;
	}

	/** Construct the app + layout. Async because Pixi 8's renderer
	 *  init is async. Caller awaits before populating slots. */
	static async create(opts: EditorAppOptions): Promise<EditorApp> {
		// World view = host's pixel dims. Editors use the renderer
		// at 1:1 logical scale (no integer-scale-up like the game
		// viewport): designers want pixel-precise interaction.
		const rect = opts.host.getBoundingClientRect();
		const w = Math.max(320, Math.floor(rect.width));
		const h = Math.max(240, Math.floor(rect.height));

		const pixi = await BoxlandApp.create({
			host: opts.host,
			worldViewW: w,
			worldViewH: h,
			catalog: new StaticAssetCatalog({ entries: [] }),
			background: opts.background ?? 0x10131c,
		});

		const slots = buildEditorLayout({
			theme: opts.theme,
			width: w,
			height: h,
		});

		// Mount the layout root into pixi.stage (NOT scene.root —
		// scene.root is camera-scaled; the editor chrome must
		// stay in viewport-pixel space). Same pattern HUD uses.
		pixi.pixi.stage.addChild(slots.root);

		// EditorHarness drives the scene — it's used for in-canvas
		// content (tile sprites, placement renderables) which the
		// surface-specific entry script feeds. The chrome layout
		// doesn't go through it; it's static.
		const camera: Camera = { cx: 0, cy: 0 };
		const harness = EditorHarness.create({ app: pixi, camera });

		const app = new EditorApp(pixi, slots, harness, opts.host);
		app.attachResizeObserver();

		// Pre-warm theme textures so chrome panels paint with art
		// from frame 1.
		void opts.theme.prefetchAll();

		return app;
	}

	/** Tear down the app. Disconnects the resize observer + the
	 *  EditorHarness; the Pixi canvas remains mounted (caller
	 *  owns the host element's lifecycle). */
	destroy(): void {
		this.resizeObserver?.disconnect();
		this.resizeObserver = null;
		this.harness.destroy();
		this.pixi.destroy();
	}

	/** Force a layout recalc (e.g. after the surface adds widgets
	 *  that affect intrinsic sizing). The ResizeObserver handles
	 *  viewport resizes automatically. */
	relayout(): void {
		const rect = this.host.getBoundingClientRect();
		const w = Math.max(320, Math.floor(rect.width));
		const h = Math.max(240, Math.floor(rect.height));
		resizeEditorLayout(this.slots, w, h);
	}

	/** The scene root, exposed so surface scripts can add
	 *  in-canvas content (tile sprites, placement renderables)
	 *  alongside what the EditorHarness feeds. */
	scene(): Scene { return this.pixi.scene; }

	/** The canvas-wrap slot is the bounding region for the
	 *  in-canvas viewport. Surface scripts can read its position
	 *  to convert pointer events to world coordinates. */
	canvasWrap(): Container { return this.slots.canvasWrap; }

	private attachResizeObserver(): void {
		if (typeof ResizeObserver === "undefined") return;
		this.resizeObserver = new ResizeObserver(() => this.relayout());
		this.resizeObserver.observe(this.host);
	}
}
