// Boxland — level editor render bridge.
//
// Pure adapter: turn the editor's state (placements, backdrop tiles,
// selection, active-entity ghost) into a Renderable[] the shared
// Pixi renderer (`@render`) consumes. No DOM, no fetch, no Pixi
// imports — purely a data transform.
//
// Why a separate file rather than building Renderables inside the
// state module: the editor's state is a clean mutable model; the
// renderer wants a flat array per frame. Splitting the two means
// state.ts stays small + testable, and render decisions (layer
// stacking, ghost transparency via tint, pending-row dimming) live
// next to the renderer interface they target.

import type { Renderable } from "@render";
import { SUB_PER_PX } from "@render";
import type { Cell, Placement, BackdropTile } from "./types";

/** Cell size in sub-pixels (32 px × 256 sub/px = 8192). The renderer's
 *  coordinate space is sub-pixel; cells convert via this constant. */
const TILE_SUB = 32 * SUB_PER_PX;

/** Layer ordinals. Low draws first (under). Spread out so a future
 *  level-editor overlay can squeeze between groups without renumbering. */
const LAYER = {
	BACKDROP: 0,
	PLACEMENT: 100,
	GHOST: 200,
	SELECTION_HALO: 300,
} as const;

/**
 * Synthetic id-space for the four kinds of Renderable the editor
 * produces. Renderable.id is unique across the scene; placements use
 * their natural ids; backdrop tiles get encoded ids that can't
 * collide with positive placement ids (offset by a large constant).
 *
 * Real placement ids start at 1 and grow into the millions in big
 * projects; offsetting backdrops by 1e12 keeps the spaces disjoint
 * for the lifetime of the editor without touching the wire format.
 */
const ID_BACKDROP_OFFSET = 1_000_000_000_000;
const ID_GHOST = -1;
const ID_SELECTION_HALO = -2;

export interface RenderBridgeState {
	placements: readonly Placement[];
	backdrop: readonly BackdropTile[];
	selection: number | null;
	cursorCell: Cell | null;
	activeEntityID: number | null;
	activeRotation: 0 | 90 | 180 | 270;
	tool: "place" | "select" | "erase";
	mapWidth: number;
	mapHeight: number;
	/** Set of entity_type_ids known to the StaticAssetCatalog. Used
	 *  so the bridge can skip Renderables whose asset_id won't render
	 *  anyway (cleaner than emitting them and watching the Scene
	 *  silently drop the texture lookup). */
	knownAssetIDs: ReadonlySet<number>;
	/** Pending placeholder ids. Drawn dimmer to signal "saving…". */
	pendingPlacementIDs: ReadonlySet<number>;
}

/**
 * Build the Renderable list for one frame. Pure function — same
 * input, same output, no side effects. Called from the entry script
 * whenever state changes; the EditorHarness coalesces flushes per
 * animation frame.
 */
export function buildRenderables(s: RenderBridgeState): Renderable[] {
	const out: Renderable[] = [];

	// 1) Backdrop tiles — read-only, dimmed via tint.
	for (const t of s.backdrop) {
		if (!s.knownAssetIDs.has(t.entityTypeId)) continue;
		out.push(cellRenderable({
			id: ID_BACKDROP_OFFSET + encodeBackdropID(t),
			assetID: t.entityTypeId,
			x: t.x,
			y: t.y,
			rotation: t.rotation,
			layer: LAYER.BACKDROP,
			// Multiply tint at ~55% white = visibly dimmer than the
			// foreground placements. Alpha byte is stripped by Scene
			// upsert; matches the Canvas2D editor's `globalAlpha = 0.55`.
			tint: 0x8c8c8cff,
		}));
	}

	// 2) Placements — full opacity by default; pending ones dimmed.
	for (const p of s.placements) {
		const isPending = s.pendingPlacementIDs.has(p.id);
		out.push(cellRenderable({
			id: p.id,
			assetID: p.entityTypeId,
			x: p.x,
			y: p.y,
			rotation: p.rotation,
			layer: LAYER.PLACEMENT,
			tint: isPending ? 0xb3b3b3ff : 0xffffffff,
			footY: cellSub(p.y) + TILE_SUB, // y-sort by foot for nicer stacking
		}));
	}

	// 3) Selection halo. We synthesize it as a Renderable on a high
	// layer so the existing Scene reconciliation handles its
	// lifecycle uniformly. The renderer renders this at the same
	// asset/atlas as the selected placement; a real halo with corner
	// brackets would need a custom layer (future work — for v1 we
	// just brighten the cell via tint).
	if (s.selection !== null) {
		const sel = s.placements.find((p) => p.id === s.selection);
		if (sel && s.knownAssetIDs.has(sel.entityTypeId)) {
			out.push(cellRenderable({
				id: ID_SELECTION_HALO,
				assetID: sel.entityTypeId,
				x: sel.x,
				y: sel.y,
				rotation: sel.rotation,
				layer: LAYER.SELECTION_HALO,
				// Bright yellow tint — matches --bx-warn token
				// (#ffd84a) — so the selected sprite glows.
				tint: 0xffd84aff,
			}));
		}
	}

	// 4) Place-tool ghost: only when armed, hovering inside the map.
	if (s.tool === "place" && s.activeEntityID !== null && s.cursorCell && inBounds(s.cursorCell, s.mapWidth, s.mapHeight)) {
		if (s.knownAssetIDs.has(s.activeEntityID)) {
			out.push(cellRenderable({
				id: ID_GHOST,
				assetID: s.activeEntityID,
				x: s.cursorCell.x,
				y: s.cursorCell.y,
				rotation: s.activeRotation,
				layer: LAYER.GHOST,
				tint: 0xffffff80, // ~50% opacity via alpha byte (Scene strips it; we use it as a visual hint here for future)
			}));
		}
	}

	return out;
}

interface CellInputs {
	id: number;
	assetID: number;
	x: number;
	y: number;
	rotation: 0 | 90 | 180 | 270;
	layer: number;
	tint?: number;
	footY?: number;
}

function cellRenderable(c: CellInputs): Renderable {
	const r: Renderable = {
		id: c.id,
		asset_id: c.assetID,
		anim_id: 0,
		anim_frame: 0,
		x: cellSub(c.x),
		y: cellSub(c.y),
		layer: c.layer,
		rotation: c.rotation,
		gridSnap: true,
	};
	// Only spread optional fields when defined — exactOptionalPropertyTypes
	// in tsconfig forbids `tint: undefined` in a Renderable literal.
	if (c.tint !== undefined) r.tint = c.tint;
	if (c.footY !== undefined) r.footY = c.footY;
	return r;
}

function cellSub(cell: number): number { return cell * TILE_SUB; }

function inBounds(c: Cell, w: number, h: number): boolean {
	return c.x >= 0 && c.y >= 0 && c.x < w && c.y < h;
}

/** Pack (layerId, x, y) into a single id for backdrop renderables.
 *  Keeps each backdrop tile's id stable across frames so the Scene
 *  reuses the same Sprite (no churn). 16 bits per dim is plenty —
 *  Boxland maps cap at 4096×4096. */
function encodeBackdropID(t: BackdropTile): number {
	return ((t.layerId & 0x3fff) << 28) | ((t.x & 0x3fff) << 14) | (t.y & 0x3fff);
}

/** Camera intent: center the view on the map center (in sub-pixels).
 *  Editor pages start with the whole map framed. */
export function defaultCamera(mapWidth: number, mapHeight: number): { cx: number; cy: number } {
	return {
		cx: (mapWidth * TILE_SUB) / 2,
		cy: (mapHeight * TILE_SUB) / 2,
	};
}

/** Convert a screen-px cell coordinate into sub-pixel for the camera.
 *  Used by pan/zoom controls to set the camera target precisely. */
export function cellToSubpx(cell: Cell): { cx: number; cy: number } {
	return { cx: cellSub(cell.x), cy: cellSub(cell.y) };
}

/** Re-export for external use without forcing a long path. */
export const TILE_SUB_PX = TILE_SUB;
