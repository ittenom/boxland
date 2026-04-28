// Boxland — level editor type contracts.
//
// Wire-side shapes (snake_case) match what the Go handlers return /
// accept; client-side state is camelCase. Adapters in `state.ts`
// bridge the two.

/** One placement on a level. */
export interface Placement {
	id: number;
	entityTypeId: number;
	x: number;
	y: number;
	rotation: 0 | 90 | 180 | 270;
	instanceOverrides: Record<string, unknown>;
	tags: readonly string[];
}

/** Wire shape returned by `GET/POST/PATCH /design/levels/{id}/entities[/{eid}]`. */
export interface PlacementWire {
	id: number;
	entity_type_id: number;
	x: number;
	y: number;
	rotation_degrees: 0 | 90 | 180 | 270;
	instance_overrides: Record<string, unknown>;
	tags: string[];
}

/** Backdrop tile, read-only on this surface. Renders under placements
 *  so the user sees what the underlying map looks like. */
export interface BackdropTile {
	layerId: number;
	x: number;
	y: number;
	entityTypeId: number;
	rotation: 0 | 90 | 180 | 270;
}

/** One palette atlas row from `GET /design/levels/{id}/entity-types`
 *  or `/design/maps/{id}/tile-types`. Drives both the palette UI and
 *  the StaticAssetCatalog. */
export interface PaletteAtlasEntry {
	id: number;
	name: string;
	class: "tile" | "npc" | "pc" | "logic";
	sprite_url: string;
	atlas_index: number;
	atlas_cols: number;
	tile_size: number;
	folder_id?: number;
	procedural_include?: boolean;
}

/** Tool ids — single source of truth shared by hotkey + button bindings. */
export type Tool = "place" | "select" | "erase";

/** Boot config read off the host element's data-bx-* attributes. */
export interface LevelEditorBoot {
	levelId: number;
	mapId: number;
	mapWidth: number;
	mapHeight: number;
}

/** Tile cell coords. */
export interface Cell {
	x: number;
	y: number;
}

/** Quarter-turn rotation set, in editor display order. */
export const ROTATIONS = [0, 90, 180, 270] as const;

/** Bump rotation by 90°. */
export function nextRotation(r: 0 | 90 | 180 | 270): 0 | 90 | 180 | 270 {
	switch (r) {
		case 0: return 90;
		case 90: return 180;
		case 180: return 270;
		default: return 0;
	}
}

export function placementFromWire(w: PlacementWire): Placement {
	return {
		id: w.id,
		entityTypeId: w.entity_type_id,
		x: w.x,
		y: w.y,
		rotation: normalizeRotation(w.rotation_degrees),
		instanceOverrides: w.instance_overrides ?? {},
		tags: w.tags ?? [],
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
