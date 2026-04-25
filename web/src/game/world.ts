// Boxland — game/world.ts
//
// Bridge between the net Mailbox tile cache and the collision module's
// World shape. The shared collision algorithm reads tiles via
// `world.get(gx, gy)`; the Mailbox stores them keyed by
// `${layerId}:${gx}:${gy}` because multiple layers can stack on a cell.
//
// For collision we need the union: any layer's edge_collisions OR'd
// together (a wall is a wall regardless of which layer authored it),
// and the highest-priority collision_layer_mask. v1 keeps it simple
// and just picks the first non-empty tile we find at (gx, gy); the
// authored convention is that walls live on the base layer.

import type { World, Tile as CollisionTile } from "@collision";
import type { CachedTile } from "@net";

/**
 * Wrap the Mailbox tile cache as a collision World. The layer id is
 * not exposed to collision; the wrapper picks the first matching tile
 * from any layer for v1.
 *
 * Cheap to recreate per tick (no allocation in get); the orchestrator
 * builds one and reuses it across predict steps within the same frame.
 */
export function mailboxAsWorld(
	tiles: ReadonlyMap<string, CachedTile> | { values(): IterableIterator<CachedTile> },
): World {
	// Build a (gx,gy) -> CollisionTile map up front so per-step lookups
	// stay O(1) even if the host fires multiple predictions per frame.
	const byCell = new Map<string, CollisionTile>();
	const iter = "values" in tiles ? tiles.values() : (tiles as ReadonlyMap<string, CachedTile>).values();
	for (const t of iter as IterableIterator<CachedTile>) {
		const k = key(t.gx, t.gy);
		const existing = byCell.get(k);
		if (existing) {
			// OR edges so walls on either layer block, prefer the wider mask.
			byCell.set(k, {
				gx: t.gx,
				gy: t.gy,
				edge_collisions: existing.edge_collisions | t.edgeCollisions,
				collision_layer_mask: existing.collision_layer_mask | t.collisionLayerMask,
			});
		} else {
			byCell.set(k, {
				gx: t.gx,
				gy: t.gy,
				edge_collisions: t.edgeCollisions,
				collision_layer_mask: t.collisionLayerMask,
			});
		}
	}
	return {
		get(gx: number, gy: number): CollisionTile | undefined {
			return byCell.get(key(gx, gy));
		},
	};
}

function key(gx: number, gy: number): string {
	return `${gx | 0},${gy | 0}`;
}
