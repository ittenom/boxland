// Boxland — chunked map loader.
//
// Materializes persisted map_tiles into the live ECS as Tile + Static +
// Sprite + Collider entities. One LoadChunk call per chunk; the runtime
// streams them lazily as players move (PLAN.md §4f "Map chunk model").
//
// The loader needs entity-type metadata (collider dimensions, sprite
// asset id) to fill out the components. We accept an EntityTypeLookup
// interface to avoid coupling this package to the entities CRUD package.

package maps

import (
	"context"
	"fmt"

	"boxland/server/internal/entities/components"
	"boxland/server/internal/sim/ecs"
)

// EntityTypeMeta is the minimal subset of EntityType the loader needs to
// build components from a tile placement. The fields here match the
// shape returned by entities.Service.EntityTypeMeta — the value types
// are distinct (no cross-package dep) but structurally identical, and
// callers convert with a one-liner.
type EntityTypeMeta struct {
	ID                   int64
	SpriteAssetID        *int64
	DefaultAnimationID   *int64
	ColliderW            int32
	ColliderH            int32
	ColliderAnchorX      int32
	ColliderAnchorY      int32
	DefaultCollisionMask int64
}

// EntityTypeLookup is the dependency for translating an entity_type_id
// into its componentry. The runtime injects an adapter that delegates
// to entities.Service.EntityTypeMeta; tests pass canned data.
type EntityTypeLookup interface {
	EntityTypeMeta(ctx context.Context, id int64) (*EntityTypeMeta, error)
}

// LoadResult reports the count of materialized entities.
type LoadResult struct {
	TilesSpawned    int
	LightingSpawned int
}

// LoadChunk reads every tile in (x0, y0)..(x1, y1) for `mapID`, looks up
// each tile's entity-type metadata, and spawns the corresponding entity
// in `world` with the appropriate component set. Idempotent ONLY w.r.t.
// the database read — calling LoadChunk twice on the same world double-
// spawns. The runtime tracks "is this chunk already loaded?" externally.
func (s *Service) LoadChunk(
	ctx context.Context,
	world *ecs.World,
	lookup EntityTypeLookup,
	mapID int64,
	x0, y0, x1, y1 int32,
) (LoadResult, error) {
	tiles, err := s.ChunkTiles(ctx, mapID, x0, y0, x1, y1)
	if err != nil {
		return LoadResult{}, fmt.Errorf("chunk tiles: %w", err)
	}

	// Cache entity-type lookups so a chunk full of identical wall tiles
	// only hits the entities surface once.
	cache := make(map[int64]*EntityTypeMeta, 8)
	stores := world.Stores()

	res := LoadResult{}
	for _, t := range tiles {
		meta, ok := cache[t.EntityTypeID]
		if !ok {
			m, err := lookup.EntityTypeMeta(ctx, t.EntityTypeID)
			if err != nil {
				return res, fmt.Errorf("entity type %d: %w", t.EntityTypeID, err)
			}
			cache[t.EntityTypeID] = m
			meta = m
		}

		e := world.Spawn()
		stores.Tile.Set(e, components.Tile{
			LayerID: uint16(t.LayerID),
			GX:      t.X,
			GY:      t.Y,
		})
		stores.Static.Set(e, components.Static{})

		// Sprite. AssetID is the type's, unless the placement overrides
		// the animation (rare; full sprite override would be a separate
		// per-tile field in a future task).
		var assetID uint32
		if meta.SpriteAssetID != nil {
			assetID = uint32(*meta.SpriteAssetID)
		}
		var animID uint32
		if t.AnimOverride != nil {
			animID = uint32(*t.AnimOverride)
		} else if meta.DefaultAnimationID != nil {
			animID = uint32(*meta.DefaultAnimationID)
		}
		stores.Sprite.Set(e, components.Sprite{
			AssetID: assetID,
			AnimID:  animID,
		})

		// Collider. Mask override (per-tile) wins over the type default.
		mask := uint32(meta.DefaultCollisionMask)
		if t.CollisionMaskOverride != nil {
			mask = uint32(*t.CollisionMaskOverride)
		}
		stores.Collider.Set(e, components.Collider{
			W:       uint16(meta.ColliderW),
			H:       uint16(meta.ColliderH),
			AnchorX: uint16(meta.ColliderAnchorX),
			AnchorY: uint16(meta.ColliderAnchorY),
			Mask:    mask,
		})

		res.TilesSpawned++
	}

	// Lighting cells for the same chunk.
	rows, err := s.Pool.Query(ctx, `
		SELECT layer_id, x, y, color, intensity
		FROM map_lighting_cells
		WHERE map_id = $1 AND x BETWEEN $2 AND $3 AND y BETWEEN $4 AND $5
	`, mapID, x0, x1, y0, y1)
	if err != nil {
		return res, fmt.Errorf("chunk lighting: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var layerID int64
		var x, y int32
		var color int64
		var intensity int16
		if err := rows.Scan(&layerID, &x, &y, &color, &intensity); err != nil {
			return res, err
		}
		// Lighting cells are also entities (Tile + Static), but they
		// don't carry a Collider or Sprite. The renderer's lighting
		// compositor reads them directly via the LightingLayer; the
		// ECS entry is for spatial lookup + WAL replay symmetry.
		e := world.Spawn()
		stores.Tile.Set(e, components.Tile{
			LayerID: uint16(layerID),
			GX:      x,
			GY:      y,
		})
		stores.Static.Set(e, components.Static{})
		res.LightingSpawned++
	}
	return res, rows.Err()
}
