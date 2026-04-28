// Boxland — level editor server I/O.
//
// One fetch wrapper for the level-entity CRUD + the two catalog
// endpoints. Mirrors the snake_case wire contract from
// server/internal/designer/level_entities_handlers.go and
// palette_handlers.go.
//
// CSRF: all mutating requests carry the `X-CSRF-Token` header read
// from the canonical `<meta name="csrf-token">` tag on the page;
// matches mapmaker.js + every other design-realm fetch site.

import type { PlacementWire, PaletteAtlasEntry, Placement } from "./types";

export interface PlaceRequest {
	entityTypeId: number;
	x: number;
	y: number;
	rotation?: 0 | 90 | 180 | 270;
	instanceOverrides?: Record<string, unknown>;
	tags?: string[];
}

export interface PatchRequest {
	x?: number;
	y?: number;
	rotation?: 0 | 90 | 180 | 270;
	instanceOverrides?: Record<string, unknown>;
}

/** Atlas info for the editor's StaticAssetCatalog. Single shape used
 *  by both `tile-types` (map backdrop) and `entity-types` (placement
 *  palette) endpoints. */
export interface TilesPayload {
	tiles: Array<{
		layer_id: number;
		x: number;
		y: number;
		entity_type_id: number;
		rotation_degrees: 0 | 90 | 180 | 270;
	}>;
	width: number;
	height: number;
}

export interface EntitiesPayload {
	entities: PlacementWire[];
}

export interface PaletteAtlasPayload {
	entries: PaletteAtlasEntry[];
}

export class LevelEditorWire {
	constructor(private readonly levelId: number, private readonly mapId: number) {}

	listEntities(signal?: AbortSignal): Promise<EntitiesPayload> {
		return fetchJSON(`/design/levels/${this.levelId}/entities`, "GET", undefined, signal);
	}

	loadBackdropTiles(signal?: AbortSignal): Promise<TilesPayload> {
		return fetchJSON(`/design/maps/${this.mapId}/tiles`, "GET", undefined, signal);
	}

	loadPlacementCatalog(signal?: AbortSignal): Promise<PaletteAtlasPayload> {
		return fetchJSON(`/design/levels/${this.levelId}/entity-types`, "GET", undefined, signal);
	}

	loadBackdropCatalog(signal?: AbortSignal): Promise<PaletteAtlasPayload> {
		return fetchJSON(`/design/maps/${this.mapId}/tile-types`, "GET", undefined, signal);
	}

	placeEntity(req: PlaceRequest): Promise<{ entity: PlacementWire }> {
		return fetchJSON(`/design/levels/${this.levelId}/entities`, "POST", {
			entity_type_id: req.entityTypeId,
			x: req.x,
			y: req.y,
			rotation_degrees: req.rotation ?? 0,
			instance_overrides: req.instanceOverrides,
			tags: req.tags,
		});
	}

	patchEntity(eid: number, req: PatchRequest): Promise<{ entity: PlacementWire }> {
		const body: Record<string, unknown> = {};
		if (req.x !== undefined) body.x = req.x;
		if (req.y !== undefined) body.y = req.y;
		if (req.rotation !== undefined) body.rotation_degrees = req.rotation;
		if (req.instanceOverrides !== undefined) body.instance_overrides = req.instanceOverrides;
		return fetchJSON(`/design/levels/${this.levelId}/entities/${eid}`, "PATCH", body);
	}

	deleteEntity(eid: number): Promise<null> {
		return fetchJSON(`/design/levels/${this.levelId}/entities/${eid}`, "DELETE");
	}
}

/** Convert a placement record back to wire shape. Used by undo/redo
 *  when re-creating a previously deleted placement. */
export function placementToPlaceRequest(p: Placement): PlaceRequest {
	return {
		entityTypeId: p.entityTypeId,
		x: p.x,
		y: p.y,
		rotation: p.rotation,
		instanceOverrides: { ...p.instanceOverrides },
		tags: [...p.tags],
	};
}

function fetchJSON<T>(url: string, method: string, body?: unknown, signal?: AbortSignal): Promise<T> {
	const csrf = document.querySelector('meta[name="csrf-token"]')?.getAttribute("content") ?? "";
	const init: RequestInit = {
		method,
		headers: {
			"Content-Type": "application/json",
			"X-CSRF-Token": csrf,
		},
		credentials: "same-origin",
		body: body == null ? null : JSON.stringify(body),
	};
	// exactOptionalPropertyTypes: only set `signal` if a real one was
	// passed; the DOM fetch type doesn't accept `signal: undefined`.
	if (signal) init.signal = signal;
	return fetch(url, init).then(async (r) => {
		if (r.status === 204) return null as T;
		if (!r.ok) {
			const text = await r.text().catch(() => "");
			throw new Error(text || `HTTP ${r.status}`);
		}
		return (await r.json()) as T;
	});
}
