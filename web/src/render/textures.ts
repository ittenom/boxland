// Boxland — texture cache.
//
// Loads sheet PNGs and slices them into per-frame Pixi textures on demand.
// Cache is keyed by the sheet URL; per-frame textures share a base. The
// cache is per-Pixi-app (it holds GPU handles), so apps own the cache
// lifetime.
//
// Pixi 8 introduced async asset loading (`Assets.load`) and switched to
// the WebGPU/WebGL2 hybrid renderer; these helpers wrap that surface so
// callers don't have to think about it.

import { Rectangle, Texture } from "pixi.js";

import { loadTextureAsset } from "./asset-texture";
import type { AnimationFrame, AssetCatalog, AssetId, AnimId } from "./types";

/**
 * Per-(app, asset, variant) base texture. Returned as a Promise so callers
 * can await the first paint when needed; subsequent calls hit the cache.
 */
export class TextureCache {
	private readonly bases = new Map<string, Promise<Texture>>();
	private readonly frames = new Map<string, Texture>();

	constructor(private readonly catalog: AssetCatalog) {}

	/** Load (or return cached) base texture for an asset+variant. */
	async base(asset_id: AssetId, variant_id = 0): Promise<Texture> {
		const url = this.catalog.urlFor(asset_id, variant_id);
		if (!url) {
			throw new Error(`TextureCache: missing URL for asset ${asset_id}`);
		}
		let p = this.bases.get(url);
		if (!p) {
			p = loadTextureAsset(url).then((t) => {
				// Force nearest-neighbor at the source so every derived texture
				// inherits crisp pixel sampling.
				t.source.scaleMode = "nearest";
				return t;
			}).catch((err) => {
				this.bases.delete(url);
				throw err;
			});
			this.bases.set(url, p);
		}
		return p;
	}

	/** Get (or build) the per-frame Texture using a cached base. */
	async frame(
		asset_id: AssetId,
		anim_id: AnimId,
		frame: number,
		variant_id = 0,
	): Promise<Texture | undefined> {
		const def: AnimationFrame | undefined = this.catalog.frame(asset_id, anim_id, frame);
		if (!def) return undefined;
		if (!this.catalog.urlFor(asset_id, variant_id)) return undefined;
		const key = `${asset_id}:${anim_id}:${frame}:${variant_id}`;
		const cached = this.frames.get(key);
		if (cached) return cached;

		let base: Texture;
		try {
			base = await this.base(asset_id, variant_id);
		} catch {
			return undefined;
		}
		const tex = new Texture({
			source: base.source,
			frame: new Rectangle(def.sx, def.sy, def.sw, def.sh),
		});
		this.frames.set(key, tex);
		return tex;
	}

	/** Drop all cached frames. The Pixi assets cache keeps the bases alive. */
	clearFrames(): void {
		for (const t of this.frames.values()) t.destroy();
		this.frames.clear();
	}
}
