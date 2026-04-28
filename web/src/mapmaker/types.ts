// Boxland — mapmaker type contracts.
//
// Wire shapes (snake_case) match the Go handlers in
// internal/designer/handlers.go: getMapTiles / postMapTiles /
// deleteMapTiles / get/postMapLocks. Client-side state is camelCase;
// adapters in `wire.ts` bridge the two.

/** One tile placement on a map. */
export interface MapTile {
	layerId: number;
	x: number;
	y: number;
	entityTypeId: number;
	rotation: 0 | 90 | 180 | 270;
}

/** Wire shape from `GET /design/maps/{id}/tiles`. */
export interface MapTileWire {
	layer_id: number;
	x: number;
	y: number;
	entity_type_id: number;
	rotation_degrees: number;
}

/** Lock cell — same shape as a tile but stored in a parallel set. */
export type LockedCell = MapTile;

/** One layer in the multi-layer stack. */
export interface MapLayer {
	id: number;
	name: string;
	kind: "tile" | "lighting";
	yShift: number;     // server-supplied draw-order ordinal
	ySort: boolean;     // entity y-sort enable for this layer (informational)
}

/** Tool ids — matches docs/hotkeys.md. */
export type Tool =
	| "brush"
	| "rect"
	| "fill"
	| "eyedrop"
	| "eraser"
	| "lock"
	| "sample";

/** Boot config read off the host element. */
export interface MapmakerBoot {
	mapId: number;
	mapWidth: number;
	mapHeight: number;
	mode: "authored" | "procedural";
	defaultLayerId: number;
}

/** Cell coordinates. */
export interface Cell { x: number; y: number; }

/** Stamp result — what the active tool's pointer-down/move actions did
 *  to local state. The wire diff (POST/DELETE batches) is built from
 *  these arrays at stroke end. */
export interface StampResult {
	placed: MapTile[];
	erased: MapTile[];   // we keep the full record to know rotation for undo
	locked: LockedCell[];
	unlocked: LockedCell[];
}

/** Pre-image snapshot for a single cell, captured on first touch
 *  during a stroke and used to build the inverse op for undo. */
export interface PreImage {
	tile: MapTile | null;
	lock: LockedCell | null;
}

/** Procedural sample rectangle in cell coords. */
export interface SampleRect {
	x: number;
	y: number;
	width: number;
	height: number;
}

export function emptyStamp(): StampResult {
	return { placed: [], erased: [], locked: [], unlocked: [] };
}

export function tileKey(t: { layerId: number; x: number; y: number }): string {
	return `${t.layerId}:${t.x}:${t.y}`;
}

export function nextRotation(r: 0 | 90 | 180 | 270): 0 | 90 | 180 | 270 {
	return ((r + 90) % 360) as 0 | 90 | 180 | 270;
}

export function tileFromWire(w: MapTileWire): MapTile {
	return {
		layerId: w.layer_id,
		x: w.x,
		y: w.y,
		entityTypeId: w.entity_type_id,
		rotation: normalizeRotation(w.rotation_degrees),
	};
}

export function tileToWire(t: MapTile): MapTileWire {
	return {
		layer_id: t.layerId,
		x: t.x,
		y: t.y,
		entity_type_id: t.entityTypeId,
		rotation_degrees: t.rotation,
	};
}

function normalizeRotation(r: number): 0 | 90 | 180 | 270 {
	switch (r) {
		case 90: return 90;
		case 180: return 180;
		case 270: return 270;
		default: return 0;
	}
}

export function normalizeRect(a: Cell, b: Cell): { x0: number; y0: number; x1: number; y1: number } {
	return {
		x0: Math.min(a.x, b.x),
		y0: Math.min(a.y, b.y),
		x1: Math.max(a.x, b.x),
		y1: Math.max(a.y, b.y),
	};
}
