// Boxland — mapmaker render bridge.
//
// Pure adapter from MapmakerState to Renderable[] for the shared
// Pixi renderer. Tiles + procedural ghost preview + active-stamp
// ghost + lock highlights all flow through one Renderable list,
// layered so the renderer's painter algorithm produces the right
// front-to-back order.

import type { Renderable } from "@render";
import { SUB_PER_PX } from "@render";
import type { Cell, LockedCell, MapTile, SampleRect, Tool } from "./types";

const TILE_SUB = 32 * SUB_PER_PX;

const LAYER = {
	// Layer ordinals from the server: typical map has base=10,
	// decoration=20, lighting=30. We pass them through unchanged so
	// the painter algorithm matches the runtime.
	BACKDROP_OFFSET: 0,
	GHOST: 500,
	LOCK_INDICATOR: 600,
	SAMPLE_RECT: 700,
	RECT_PREVIEW: 800,
} as const;

export interface RenderBridgeState {
	tiles: readonly MapTile[];
	procPreview: readonly MapTile[] | null;
	stampGhost: { entityID: number; rotation: 0 | 90 | 180 | 270 } | null;
	cursorCell: Cell | null;
	dragRectFrom: Cell | null;
	dragRectTo: Cell | null;
	sampleRect: SampleRect | null;
	locks: readonly LockedCell[];
	tool: Tool;
	activeLayer: number;
	mapWidth: number;
	mapHeight: number;
}

const ID_GHOST = -1;
const ID_BACKDROP_OFFSET = 1_000_000_000_000;

export function buildRenderables(s: RenderBridgeState): Renderable[] {
	const out: Renderable[] = [];

	// 1) Painted tiles. layer = the map's layer ordinal so the
	//    renderer's painter algorithm matches the runtime exactly.
	for (const t of s.tiles) {
		out.push({
			id: encodeTileID(t),
			asset_id: t.entityTypeId,
			anim_id: 0,
			anim_frame: 0,
			x: t.x * TILE_SUB,
			y: t.y * TILE_SUB,
			layer: t.layerId,
			rotation: t.rotation,
			gridSnap: true,
		});
	}

	// 2) Procedural ghost preview at low opacity. We tint these
	//    halfway-white as a visual "this isn't permanent yet" cue.
	if (s.procPreview) {
		for (const t of s.procPreview) {
			out.push({
				id: encodeProcID(t),
				asset_id: t.entityTypeId,
				anim_id: 0, anim_frame: 0,
				x: t.x * TILE_SUB, y: t.y * TILE_SUB,
				layer: LAYER.GHOST,
				rotation: t.rotation,
				gridSnap: true,
				tint: 0xa0a0a0ff,
			});
		}
	}

	// 3) Stamp ghost: where the next click will land, rendered at
	//    the active rotation. Brushes / locks / rect-from-corner all
	//    show this; eraser/eyedrop don't.
	if (s.stampGhost && s.cursorCell && inBounds(s.cursorCell, s.mapWidth, s.mapHeight)) {
		const showGhost = s.tool === "brush" || s.tool === "lock" || s.tool === "rect";
		if (showGhost) {
			out.push({
				id: ID_GHOST,
				asset_id: s.stampGhost.entityID,
				anim_id: 0, anim_frame: 0,
				x: s.cursorCell.x * TILE_SUB,
				y: s.cursorCell.y * TILE_SUB,
				layer: LAYER.GHOST + 1,
				rotation: s.stampGhost.rotation,
				gridSnap: true,
				tint: 0xc0c0c0ff,
			});
		}
	}

	// 4) Locked-cell warning overlay. Locked cells already have a
	//    tile drawn (locks live on top of placed cells) — we add a
	//    second Renderable with a yellow tint at higher layer to
	//    visualize the lock corner brackets without a custom layer.
	if (s.locks.length > 0) {
		for (const c of s.locks) {
			out.push({
				id: encodeLockID(c),
				asset_id: c.entityTypeId,
				anim_id: 0, anim_frame: 0,
				x: c.x * TILE_SUB, y: c.y * TILE_SUB,
				layer: LAYER.LOCK_INDICATOR,
				rotation: c.rotation,
				gridSnap: true,
				// Yellow tint, ~50% — matches the docs/hotkeys.md
				// "yellow corner brackets" phrasing.
				tint: 0xffd84a80,
			});
		}
	}

	return out;
}

/** Sample rect + drag-rect preview are NOT Renderables (they're
 *  outline-only graphics). The entry script draws them via a
 *  dedicated overlay container on top of the Pixi stage. We expose
 *  the data here so the entry script can hand it to that overlay. */
export interface OverlayShapes {
	dragRect: { from: Cell; to: Cell } | null;
	sampleRect: SampleRect | null;
}

export function buildOverlayShapes(s: RenderBridgeState): OverlayShapes {
	return {
		dragRect: s.dragRectFrom && s.dragRectTo
			? { from: s.dragRectFrom, to: s.dragRectTo }
			: null,
		sampleRect: s.sampleRect,
	};
}

export function defaultCamera(mapWidth: number, mapHeight: number): { cx: number; cy: number } {
	return {
		cx: (mapWidth * TILE_SUB) / 2,
		cy: (mapHeight * TILE_SUB) / 2,
	};
}

export const TILE_SUB_PX = TILE_SUB;

function inBounds(c: Cell, w: number, h: number): boolean {
	return c.x >= 0 && c.y >= 0 && c.x < w && c.y < h;
}

/** Tile id-space packing: [layerId 14b | x 14b | y 14b] inside the
 *  positive int range. Server layer ids and map dims fit comfortably. */
function encodeTileID(t: MapTile): number {
	return ((t.layerId & 0x3fff) << 28) | ((t.x & 0x3fff) << 14) | (t.y & 0x3fff);
}
function encodeProcID(t: MapTile): number {
	return ID_BACKDROP_OFFSET + encodeTileID(t);
}
function encodeLockID(c: LockedCell): number {
	return -ID_BACKDROP_OFFSET - encodeTileID(c);
}
