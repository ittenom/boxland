// Boxland — unified scene renderer.
//
// One draw path for everything. Tiles and free entities both arrive as
// `Renderable` records; the scene reconciles each tick:
//
//   * pool Pixi sprites by EntityId
//   * for each Renderable, set texture from (asset_id, anim_id, anim_frame),
//     position via worldToScreen, optional tint
//   * remove sprites whose ids dropped out of the new set
//
// The scene does NOT know about server protocol; the Game / Mapmaker /
// Sandbox modules feed it Renderable lists each tick.

import { Container, Sprite } from "pixi.js";

import { DebugOverlay } from "./debug";
import { LightingLayer, type LightingCell } from "./lighting";
import { TextureCache } from "./textures";
import type { AssetCatalog, Camera, EntityId, Renderable } from "./types";
import { computeLayout, worldToScreen, type ViewportLayout } from "./viewport";
import { SUB_PER_PX } from "@collision";

export interface SceneOptions {
	/** World view in *world pixels* (logical resolution before integer scale). */
	worldViewW: number;
	worldViewH: number;
}

export class Scene {
	readonly root = new Container();
	readonly lighting: LightingLayer;
	readonly debug: DebugOverlay;
	private readonly entityRoot = new Container();
	private readonly textures: TextureCache;
	private readonly sprites = new Map<EntityId, Sprite>();
	private layout: ViewportLayout;

	constructor(catalog: AssetCatalog, private readonly opts: SceneOptions) {
		this.textures = new TextureCache(catalog);
		this.lighting = new LightingLayer({
			worldViewW: opts.worldViewW,
			worldViewH: opts.worldViewH,
		});
		this.debug = new DebugOverlay({
			worldViewW: opts.worldViewW,
			worldViewH: opts.worldViewH,
		});
		// Entities, then lighting (multiply blend), then the debug overlay
		// always on top so collision boxes stay visible through lighting.
		this.root.addChild(this.entityRoot);
		this.root.addChild(this.lighting.root);
		this.root.addChild(this.debug.root);
		this.layout = computeLayout({
			canvasW: opts.worldViewW,
			canvasH: opts.worldViewH,
			worldViewW: opts.worldViewW,
			worldViewH: opts.worldViewH,
		});
	}

	/** Recompute layout when the host canvas resizes. */
	resize(canvasW: number, canvasH: number): void {
		this.layout = computeLayout({
			canvasW,
			canvasH,
			worldViewW: this.opts.worldViewW,
			worldViewH: this.opts.worldViewH,
		});
		// Update child sprites' scale to match the new layout in one place.
		this.root.scale.set(this.layout.scale);
		this.root.position.set(this.layout.offsetX, this.layout.offsetY);
	}

	/**
	 * Apply a new set of Renderables. Existing sprites are kept and updated;
	 * sprites whose ids dropped out of `renderables` are removed.
	 */
	async update(renderables: Renderable[], camera: Camera): Promise<void> {
		const seen = new Set<EntityId>();
		for (const r of renderables) {
			seen.add(r.id);
			await this.upsert(r, camera);
		}
		for (const id of [...this.sprites.keys()]) {
			if (!seen.has(id)) {
				const s = this.sprites.get(id);
				s?.destroy();
				this.sprites.delete(id);
			}
		}
		// Sort entities by layer so painter's algorithm respects render order.
		this.entityRoot.children.sort((a, b) => (a.zIndex ?? 0) - (b.zIndex ?? 0));
	}

	private async upsert(r: Renderable, camera: Camera): Promise<void> {
		let sprite = this.sprites.get(r.id);
		if (!sprite) {
			sprite = new Sprite();
			sprite.roundPixels = true;
			this.entityRoot.addChild(sprite);
			this.sprites.set(r.id, sprite);
		}

		const tex = await this.textures.frame(r.asset_id, r.anim_id, r.anim_frame, r.variant_id ?? 0);
		if (tex) sprite.texture = tex;

		// Per-frame tint for runtime effects only (damage flash, freeze, etc).
		// Palette variants use a *different baked PNG* (chosen by variant_id
		// in the texture lookup above) — never the tint path. See PLAN.md §1
		// "Palette swap" and §6b "Variant texture lookup".
		//
		// Pixi 8's built-in sprite.tint is an RGB multiply executed on the GPU;
		// it satisfies the "secondary multiply for runtime effects" spec
		// without bespoke shader code. We strip the alpha byte from the
		// 0xRRGGBBAA wire encoding because Pixi's tint is RGB-only.
		if (r.tint && (r.tint >>> 8) !== 0) {
			sprite.tint = (r.tint >>> 8) & 0xffffff;
		} else {
			sprite.tint = 0xffffff;
		}

		// Pre-divide layout to keep scene.root.scale doing the visible scaling.
		const screen = worldToScreen(
			r.x, r.y,
			camera.cx, camera.cy,
			// Layout pre-applied to the root container, so for child positions
			// we use the pre-scaled coordinate space (raw world pixels).
			{ scale: 1, offsetX: 0, offsetY: 0, scaledW: this.opts.worldViewW, scaledH: this.opts.worldViewH },
			this.opts.worldViewW, this.opts.worldViewH,
			SUB_PER_PX,
		);
		sprite.position.set(screen.x, screen.y);
		sprite.zIndex = r.layer;
	}

	/** Replace the lighting cells (passes through to the LightingLayer). */
	updateLighting(cells: LightingCell[], camera: Camera): void {
		this.lighting.update(cells, camera);
	}

	/** Number of currently mounted sprites — handy for tests. */
	size(): number {
		return this.sprites.size;
	}

	/**
	 * Read-only view of the entity sprite list, in render (z-sorted) order.
	 * Exposed for tests; production code goes through `update()`.
	 */
	entitySprites(): readonly Sprite[] {
		return this.entityRoot.children as unknown as readonly Sprite[];
	}
}
