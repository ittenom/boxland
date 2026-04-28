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

	/**
	 * Optional foot-position y in *world sub-pixels*. When set, the scene
	 * sorts entities sharing the same `layer` by ascending footY -- the
	 * "walk-behind" illusion (Stardew, Undertale). Leave undefined to fall
	 * back to layer-only ordering.
	 *
	 * The producer (catalog + cached entity adapter) decides which entities
	 * opt in -- gated server-side by entity_type.y_sort_anchor +
	 * map_layer.y_sort_entities (migrations 0027 / 0028). See
	 * docs/indie-rpg-research-todo.md §P1 #8.
	 */
	footY?: number;

	/**
	 * Optional "always above the player layer" flag. When true, the
	 * sprite sorts above any non-flagged sibling on the same layer
	 * regardless of footY. Wire-side gating: entity_type.draw_above_player.
	 */
	drawAbove?: boolean;

	/** Optional w/h hint for a sprite that needs tile-grid snapping (tiles). */
	gridSnap?: boolean;

	/**
	 * Optional quarter-turn rotation in degrees. Only 0/90/180/270 are
	 * accepted — those four are the rotations the rest of the stack
	 * (server-side `rotation_degrees` columns on map_tiles +
	 * level_entities, collision rotation helpers, FlatBuffers wire
	 * format) supports. Sprite rotation pivots around its center, so
	 * the cell footprint stays the same; this is the right behaviour
	 * for grid-snapped tiles and entity placements alike.
	 *
	 * Undefined / 0 = no rotation. The renderer falls through to the
	 * fast path (no transform set) when this is omitted.
	 */
	rotation?: 0 | 90 | 180 | 270;

	/** Optional bag for debug overlays (collision boxes etc). */
	debug?: { aabb?: { w: number; h: number } };

	/** Optional nameplate text rendered above the sprite. Empty/undefined
	 *  hides the nameplate entirely (PLAN.md §6h "nameplates"). */
	nameplate?: string;

	/** HP percent in [0..100], or 255 for "no HP bar" (matches the
	 *  EntityState.hp_pct sentinel). PLAN.md §1 EntityState shape. */
	hpPct?: number;
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
	/** Optional clock-friendly animation lookup. When supplied, the
	 *  renderer's frame clock advances `anim_frame` from wall-clock
	 *  time using (frame_from, frame_to, fps, direction). Catalogs
	 *  without this (e.g. PlaceholderCatalog) simply leave the
	 *  server-supplied frame index alone. */
	animationByID?(asset_id: AssetId, anim_id: AnimId): AnimationLookup | undefined;
}

/** Minimum animation metadata the frame clock needs. */
export interface AnimationLookup {
	frame_from: number;
	frame_to: number;
	fps: number;
	direction: "forward" | "reverse" | "pingpong";
}

/** Camera intent: where in world sub-pixels is the viewport centered? */
export interface Camera {
	cx: number;
	cy: number;
}
