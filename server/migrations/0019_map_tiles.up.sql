-- 0019_map_tiles.up.sql
--
-- Per-tile placements. (map_id, layer_id, x, y) uniquely identifies one
-- cell; entity_type_id points at the entity-type the runtime spawns at
-- that grid coord (with Tile + Static + Sprite + Collider components per
-- PLAN.md §1 "Tiles ARE entities").
--
-- The override columns let designers tweak collision behavior per-tile
-- without forking a whole new entity type:
--   collision_shape_override (NULL = inherit type's shape preset)
--   collision_mask_override  (NULL = inherit type's default_collision_mask)
-- Mapmaker's "this tile vs all matching tiles" toggle decides whether the
-- override lands here or propagates back to the type.
--
-- Bulk loading uses the (map_id, layer_id, x, y) primary key for chunk
-- range queries; we additionally index on (map_id, x, y) so the spatial
-- loader can fetch a chunk across all layers in one query.

CREATE TABLE map_tiles (
    map_id                    BIGINT       NOT NULL REFERENCES maps(id) ON DELETE CASCADE,
    layer_id                  BIGINT       NOT NULL REFERENCES map_layers(id) ON DELETE CASCADE,
    x                         INTEGER      NOT NULL,
    y                         INTEGER      NOT NULL,
    entity_type_id            BIGINT       NOT NULL REFERENCES entity_types(id) ON DELETE RESTRICT,
    anim_override             SMALLINT,
    collision_shape_override  SMALLINT,    -- enum int (CollisionShape from world.fbs)
    collision_mask_override   BIGINT,      -- uint32 mask, NULL = inherit
    custom_flags_json         JSONB,
    PRIMARY KEY (map_id, layer_id, x, y)
);

-- Cross-layer chunk loads: fetch every layer's tile at (x, y) in one query.
CREATE INDEX map_tiles_xy_idx ON map_tiles (map_id, x, y);
