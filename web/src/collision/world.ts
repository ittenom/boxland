import type { Tile, World } from "./types";

/**
 * Build a sparse World from a list of tile descriptors. Used by tests and
 * by the renderer when it needs collision queries against authored maps.
 *
 * For the tick loop the server-side spatial index is authoritative; the
 * web build calls into this only for client-side prediction in the
 * shared collision module.
 */
export function buildWorld(tiles: Tile[]): World {
	const map = new Map<string, Tile>();
	for (const t of tiles) {
		map.set(key(t.gx, t.gy), t);
	}
	return {
		get(gx: number, gy: number): Tile | undefined {
			return map.get(key(gx, gy));
		},
	};
}

function key(gx: number, gy: number): string {
	return `${gx | 0},${gy | 0}`;
}
