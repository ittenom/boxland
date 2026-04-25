// Boxland — game/catalog.ts
//
// Asset catalog stub. The real catalog (with sheet URLs, animation
// frame tables, palette variants) lands when the player game ships
// against the asset pipeline. For task #116 we provide a placeholder
// catalog that satisfies the renderer's interface so the boot path
// proves end-to-end without coupling the game module to half-built
// asset wiring.
//
// PLAN.md §1 "content-addressed CDN URLs": the catalog returns absolute
// URLs the renderer can pass to TextureCache; here those URLs point at
// a 1x1 magenta placeholder served from /static/ so an unauthored
// asset is visually obvious instead of throwing.

import type { AnimationFrame, AssetCatalog } from "@render";

const MAGENTA_DATA_URL =
	"data:image/png;base64," +
	// 1x1 magenta PNG — visible at any zoom so missing-art shows up.
	"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8/5+hHgAHggJ/PchI7wAAAABJRU5ErkJggg==";

/** Minimal placeholder catalog. Every (asset_id) maps to the same
 *  magenta tile; every frame returns a 32x32 source rect anchored at
 *  the centre. Enough for the renderer's smoke test. */
export class PlaceholderCatalog implements AssetCatalog {
	urlFor(_assetId: number, _variantId?: number): string {
		return MAGENTA_DATA_URL;
	}
	frame(assetId: number, animId: number, frame: number): AnimationFrame | undefined {
		return {
			asset_id: assetId,
			anim_id: animId,
			frame,
			sx: 0,
			sy: 0,
			sw: 1,
			sh: 1,
			ax: 0,
			ay: 0,
		};
	}
}
