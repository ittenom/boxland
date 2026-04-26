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

import { AnimClock, type AnimResolver } from "./anim-clock";
import { DebugOverlay } from "./debug";
import { Hud } from "./hud";
import { LightingLayer, type LightingCell } from "./lighting";
import { NameplateLayer } from "./nameplates";
import { TextureCache } from "./textures";
import type { AssetCatalog, Camera, EntityId, Renderable } from "./types";
import { computeLayout, worldToScreen, type ViewportLayout } from "./viewport";
import { SUB_PER_PX } from "@collision";

export interface SceneOptions {
	/** World view in *world pixels* (logical resolution before integer scale). */
	worldViewW: number;
	worldViewH: number;
}

/**
 * SortKey is the parallel-stored ordering key for one entity sprite.
 * Kept off the Pixi Sprite (which has no schema for it) so the
 * comparator stays type-safe and fast (one map lookup per compare).
 *
 * Order: primary = layer (low draws first), secondary = drawAbove
 * (true wins), tertiary = footY when defined (low = north = behind),
 * quaternary = entity id (stable tiebreak so sort order doesn't churn
 * frame to frame for stationary entities).
 */
interface SortKey {
	layer: number;
	drawAbove: 0 | 1;
	footY: number;        // 0 when not opted in -- comparator uses ySort below
	ySort: 0 | 1;         // 1 when footY is meaningful for this sprite
	id: number;
}

export class Scene {
	readonly root = new Container();
	readonly lighting: LightingLayer;
	readonly nameplates: NameplateLayer;
	readonly debug: DebugOverlay;
	/** Player-facing HUD. Lives outside `root` (which is camera-scaled)
	 *  so widgets keep crisp viewport coords; mounted on `app.stage` as
	 *  a sibling of `root` -- see web/src/render/app.ts and hud.ts. */
	readonly hud: Hud;
	private readonly entityRoot = new Container();
	private readonly textures: TextureCache;
	private readonly sprites = new Map<EntityId, Sprite>();
	private readonly sortKeys = new Map<Sprite, SortKey>();
	private layout: ViewportLayout;
	private readonly clock = new AnimClock();
	private readonly animResolver: AnimResolver;

	constructor(private readonly catalog: AssetCatalog, private readonly opts: SceneOptions) {
		this.textures = new TextureCache(catalog);
		// Cached resolver: prefer the catalog's clock-friendly lookup
		// when supplied, otherwise no-op (frame index passes through
		// unmodified). Bound once so we don't re-bind per tick.
		const lookup = catalog.animationByID?.bind(catalog);
		this.animResolver = lookup ? (a, id) => lookup(a, id) : () => undefined;
		this.lighting = new LightingLayer({
			worldViewW: opts.worldViewW,
			worldViewH: opts.worldViewH,
		});
		this.nameplates = new NameplateLayer({
			worldViewW: opts.worldViewW,
			worldViewH: opts.worldViewH,
		});
		this.debug = new DebugOverlay({
			worldViewW: opts.worldViewW,
			worldViewH: opts.worldViewH,
		});
		this.hud = new Hud({
			worldViewW: opts.worldViewW,
			worldViewH: opts.worldViewH,
			textures: this.textures,
			urlFor: (id) => catalog.urlFor(id),
		});
		// Entities, then lighting (multiply blend), then nameplates +
		// debug overlay always on top so name/HP/collision boxes stay
		// visible through lighting. The HUD lives OUTSIDE root because
		// root is camera-scaled; the HUD is viewport-pixel space.
		this.root.addChild(this.entityRoot);
		this.root.addChild(this.lighting.root);
		this.root.addChild(this.nameplates.root);
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
	 *
	 * Mutates `renderables[i].anim_frame` in place to reflect the
	 * wall-clock cycle position (forward / reverse / pingpong, fps
	 * from the catalog's animation row). Callers that need the
	 * server-supplied frame for other purposes should snapshot the
	 * field before calling update().
	 */
	async update(renderables: Renderable[], camera: Camera): Promise<void> {
		this.clock.tick(this.now(), renderables, this.animResolver);
		const seen = new Set<EntityId>();
		for (const r of renderables) {
			seen.add(r.id);
			await this.upsert(r, camera);
		}
		for (const id of [...this.sprites.keys()]) {
			if (!seen.has(id)) {
				const s = this.sprites.get(id);
				if (s) this.sortKeys.delete(s);
				s?.destroy();
				this.sprites.delete(id);
			}
		}
		// Multi-key painter's-algorithm sort:
		//   1. layer            -- gross draw order (terrain < entities < FX)
		//   2. drawAbove        -- pinned overlays always over peers
		//   3. footY (if y-sorted) -- the walk-behind illusion
		//   4. id               -- stable tiebreak; prevents per-frame churn
		// See SortKey above + docs/indie-rpg-research-todo.md §P1 #8.
		const keys = this.sortKeys;
		this.entityRoot.children.sort((a, b) => {
			const ka = keys.get(a as Sprite);
			const kb = keys.get(b as Sprite);
			if (!ka || !kb) return (a.zIndex ?? 0) - (b.zIndex ?? 0);
			if (ka.layer !== kb.layer) return ka.layer - kb.layer;
			if (ka.drawAbove !== kb.drawAbove) return ka.drawAbove - kb.drawAbove;
			// Y-sort kicks in only when BOTH siblings opted in. Mixing
			// y-sorted and non-y-sorted on the same layer is rare but
			// the conservative choice ("y-sorted wins") avoids surprising
			// non-opted-in tiles popping above the player.
			if (ka.ySort && kb.ySort && ka.footY !== kb.footY) {
				return ka.footY - kb.footY;
			}
			if (ka.ySort !== kb.ySort) return ka.ySort - kb.ySort;
			return ka.id - kb.id;
		});
		// Nameplates + HP bars track the same Renderable list so they
		// position with the (post-prediction) sprite + tear down when
		// an entity drops out of AOI in the same pass.
		this.nameplates.update(renderables, camera);
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
		// Update the parallel sort key (consumed by the comparator above).
		// Reused on every upsert so the GC pressure of per-frame allocation
		// is bounded by entity count, not by tick count.
		this.sortKeys.set(sprite, {
			layer:     r.layer,
			drawAbove: r.drawAbove ? 1 : 0,
			footY:     r.footY ?? 0,
			ySort:     r.footY === undefined ? 0 : 1,
			id:        r.id,
		});
	}

	/** Replace the lighting cells (passes through to the LightingLayer). */
	updateLighting(cells: LightingCell[], camera: Camera): void {
		this.lighting.update(cells, camera);
	}

	/** Number of currently mounted sprites — handy for tests. */
	size(): number {
		return this.sprites.size;
	}

	/** now() in ms — wraps performance.now / Date.now. Indirected so
	 *  tests can subclass + override to drive the frame clock
	 *  deterministically without standing up a fake performance API. */
	protected now(): number {
		return typeof performance !== "undefined" ? performance.now() : Date.now();
	}

	/**
	 * Read-only view of the entity sprite list, in render (z-sorted) order.
	 * Exposed for tests; production code goes through `update()`.
	 */
	entitySprites(): readonly Sprite[] {
		return this.entityRoot.children as unknown as readonly Sprite[];
	}

	/**
	 * Test-only: returns the EntityIds of sprites in their current
	 * post-sort render order. Exists so the y-sort + draw-above tests
	 * can assert ordering without exposing the SortKey internals.
	 */
	sortedEntityIds(): EntityId[] {
		const reverse = new Map<Sprite, EntityId>();
		for (const [id, sp] of this.sprites) reverse.set(sp, id);
		const out: EntityId[] = [];
		for (const child of this.entityRoot.children) {
			const id = reverse.get(child as Sprite);
			if (id !== undefined) out.push(id);
		}
		return out;
	}
}
