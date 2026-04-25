// Boxland — renderer-facing types.
//
// The renderer is data-driven. Game state arrives via FlatBuffers; the
// renderer's job is to map (asset_id, anim_id, anim_frame, x, y) to a
// pixel-perfect on-screen sprite. This file declares the small surface
// the renderer expects callers to feed it. Per the plan, tiles and
// entities flow through the same pipeline (PLAN.md §1 "Tiles are entities").

import { SUB_PER_PX } from "@collision";
export { SUB_PER_PX };

/** A unique numeric id assigned by the server. Matches FlatBuffers types. */
export type EntityId = number;
export type AssetId = number;
export type AnimId = number;

/**
 * One drawable thing — tile cell, mob, projectile, lighting cell. The
 * renderer treats them all uniformly. Coordinates are world sub-pixels
 * (1 px = 256 sub) so the renderer can snap to integer pixels itself.
 */
export interface Renderable {
	id: EntityId;

	/** Sprite source. */
	asset_id: AssetId;

	/** Animation state, server-authoritative. */
	anim_id: AnimId;
	anim_frame: number;

	/** World position (sub-pixel) of the sprite anchor. */
	x: number;
	y: number;

	/** Optional palette variant; 0 = base art. */
	variant_id?: number;

	/** Optional secondary multiply tint (0xRRGGBBAA), 0 = none. */
	tint?: number;

	/** Render layer. Higher draws on top. */
	layer: number;

	/** Optional w/h hint for a sprite that needs tile-grid snapping (tiles). */
	gridSnap?: boolean;

	/** Optional bag for debug overlays (collision boxes etc). */
	debug?: { aabb?: { w: number; h: number } };
}

/**
 * AnimationFrame describes a single frame within a sprite sheet. The
 * renderer caches a pooled lookup per (asset_id, anim_id, frame).
 */
export interface AnimationFrame {
	asset_id: AssetId;
	anim_id: AnimId;
	frame: number;

	/** Source rect in the sheet, in *texture pixels*. */
	sx: number;
	sy: number;
	sw: number;
	sh: number;

	/** Sprite anchor in *texture pixels* (origin offset from top-left). */
	ax: number;
	ay: number;
}

/**
 * AssetCatalog is everything the renderer needs to know about an asset.
 * Real catalogs come from the server's asset manager; tests pass fixtures.
 */
export interface AssetCatalog {
	/** Full sheet URL the renderer should load. */
	urlFor(asset_id: AssetId, variant_id?: number): string;
	/** Look up the source rect for a frame; returns undefined if unknown. */
	frame(asset_id: AssetId, anim_id: AnimId, frame: number): AnimationFrame | undefined;
}

/** Camera intent: where in world sub-pixels is the viewport centered? */
export interface Camera {
	cx: number;
	cy: number;
}
