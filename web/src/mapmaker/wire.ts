// Boxland — mapmaker server I/O.
//
// Mirrors the wire contract in
// server/internal/designer/handlers.go: tiles + locks + the new
// tile-types catalog endpoint we added in palette_handlers.go.

import type { MapTile, MapTileWire } from "./types";
import { tileFromWire, tileToWire } from "./types";

export interface PaletteAtlasEntry {
	id: number;
	name: string;
	class: string;
	sprite_url: string;
	atlas_index: number;
	atlas_cols: number;
	tile_size: number;
	folder_id?: number;
	procedural_include?: boolean;
}

export interface TilesPayload {
	map_id: number;
	width: number;
	height: number;
	tiles: MapTileWire[];
}

export interface LocksPayload {
	cells: MapTileWire[];
}

export interface PaletteAtlasPayload {
	entries: PaletteAtlasEntry[];
}

export class MapmakerWire {
	constructor(private readonly mapId: number) {}

	loadTiles(signal?: AbortSignal): Promise<MapTile[]> {
		return fetchJSON<TilesPayload>(`/design/maps/${this.mapId}/tiles`, "GET", undefined, signal)
			.then((p) => p.tiles.map(tileFromWire));
	}

	loadLocks(signal?: AbortSignal): Promise<MapTile[]> {
		// 404 on authored-mode maps is expected; surface as empty.
		return fetchJSON<LocksPayload>(`/design/maps/${this.mapId}/locks`, "GET", undefined, signal)
			.then((p) => (p.cells ?? []).map(tileFromWire))
			.catch(() => []);
	}

	loadTileTypes(signal?: AbortSignal): Promise<PaletteAtlasPayload> {
		return fetchJSON<PaletteAtlasPayload>(`/design/maps/${this.mapId}/tile-types`, "GET", undefined, signal);
	}

	loadPaintCatalog(signal?: AbortSignal): Promise<PaletteAtlasPayload> {
		// The mapmaker palette is project-wide tile-class entity_types.
		// We could re-use BuildPaletteAtlas for class=tile via a second
		// route, but the existing handler still serves the templ-rendered
		// palette tree, so we read the catalog from the page DOM instead
		// (see entry-mapmaker.ts).
		void signal;
		return Promise.resolve({ entries: [] });
	}

	postTiles(tiles: MapTile[]): Promise<unknown> {
		if (tiles.length === 0) return Promise.resolve({});
		return fetchJSON(`/design/maps/${this.mapId}/tiles`, "POST", { tiles: tiles.map(tileToWire) });
	}

	deleteTiles(layerId: number, points: ReadonlyArray<readonly [number, number]>): Promise<unknown> {
		if (points.length === 0) return Promise.resolve({});
		return fetchJSON(`/design/maps/${this.mapId}/tiles`, "DELETE", {
			layer_id: layerId,
			points: points.map((p) => [p[0], p[1]] as [number, number]),
		});
	}

	postLocks(cells: MapTile[]): Promise<unknown> {
		if (cells.length === 0) return Promise.resolve({});
		return fetchJSON(`/design/maps/${this.mapId}/locks`, "POST", {
			cells: cells.map(tileToWire),
		});
	}

	deleteLocks(layerId: number, points: ReadonlyArray<readonly [number, number]>): Promise<unknown> {
		if (points.length === 0) return Promise.resolve({});
		return fetchJSON(`/design/maps/${this.mapId}/locks`, "DELETE", {
			layer_id: layerId,
			points: points.map((p) => [p[0], p[1]] as [number, number]),
		});
	}
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
