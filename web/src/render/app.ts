// Boxland — Pixi Application bootstrap.
//
// Creates a pixel-perfect Application sized to its host container, mounts
// a Scene, and subscribes to window resize so the integer-scale layout
// snaps to the new canvas. Surfaces (Mapmaker, Sandbox, game) construct
// one of these and feed it Renderable lists per frame.
//
// This file touches the DOM. Pure-math behavior is in viewport.ts; the
// Pixi pieces here are smoke-tested by surface-level integration tests
// (vitest browser env, future task), not by node-only unit tests.

import { Application } from "pixi.js";

import { Scene, type SceneOptions } from "./scene";
import type { AssetCatalog, Camera, Renderable } from "./types";
import { computeLayout } from "./viewport";

/** Same integer-scale step Scene.resize uses, surfaced so the HUD can
 *  match exactly without duplicating the math. */
function integerScale(canvasW: number, canvasH: number, worldViewW: number, worldViewH: number): number {
	return computeLayout({ canvasW, canvasH, worldViewW, worldViewH }).scale;
}

export interface BoxlandAppOptions extends SceneOptions {
	/** Host element to mount the canvas inside. */
	host: HTMLElement;
	/** Background clear color (0xRRGGBB). Defaults to deep nav background. */
	background?: number;
	/** Asset catalog used for texture lookups. */
	catalog: AssetCatalog;
}

export class BoxlandApp {
	readonly pixi: Application;
	readonly scene: Scene;
	private readonly resizeObserver: ResizeObserver | null = null;

	private constructor(opts: BoxlandAppOptions, pixi: Application, scene: Scene) {
		this.pixi = pixi;
		this.scene = scene;

		this.pixi.stage.addChild(scene.root);
		// HUD lives outside scene.root (which is camera-scaled): it draws
		// in viewport-pixel space at integer scale. Adding it AFTER root
		// makes it draw above every world layer including the debug
		// overlay (Pixi default is back-to-front by add order).
		this.pixi.stage.addChild(scene.hud.root);
		opts.host.appendChild(this.pixi.canvas);

		// Initial layout sync.
		const rect = opts.host.getBoundingClientRect();
		this.pixi.renderer.resize(rect.width, rect.height);
		scene.resize(rect.width, rect.height);
		scene.hud.resize(rect.width, rect.height, integerScale(rect.width, rect.height, opts.worldViewW, opts.worldViewH));

		// Observe host size changes so the layout stays integer-scaled.
		if (typeof ResizeObserver !== "undefined") {
			this.resizeObserver = new ResizeObserver((entries) => {
				const entry = entries[0];
				if (!entry) return;
				const { width, height } = entry.contentRect;
				this.pixi.renderer.resize(width, height);
				scene.resize(width, height);
				scene.hud.resize(width, height, integerScale(width, height, opts.worldViewW, opts.worldViewH));
			});
			this.resizeObserver.observe(opts.host);
		}
	}

	/**
	 * Build the application. Async because Pixi 8's renderer init is async
	 * (must pick WebGL/WebGPU at runtime). Caller awaits before first feed.
	 */
	static async create(opts: BoxlandAppOptions): Promise<BoxlandApp> {
		const pixi = new Application();
		await pixi.init({
			resizeTo: opts.host,
			background: opts.background ?? 0x1a1733,
			antialias: false,
			roundPixels: true,
			autoDensity: true,
			resolution: window.devicePixelRatio || 1,
		});
		// Force nearest neighbor on every texture this Pixi app generates.
		// Sources also set scaleMode in TextureCache; this is the belt to
		// that suspenders.
		pixi.canvas.style.imageRendering = "pixelated";

		const scene = new Scene(opts.catalog, {
			worldViewW: opts.worldViewW,
			worldViewH: opts.worldViewH,
		});

		return new BoxlandApp(opts, pixi, scene);
	}

	/** Feed the renderer this tick's Renderables. */
	async update(renderables: Renderable[], camera: Camera): Promise<void> {
		await this.scene.update(renderables, camera);
	}

	/** Tear down the Pixi app, free GPU resources, stop observing resize. */
	destroy(): void {
		this.resizeObserver?.disconnect();
		this.pixi.destroy(true, { children: true, texture: true });
	}
}
