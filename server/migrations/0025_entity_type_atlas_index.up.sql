-- 0025_entity_type_atlas_index.up.sql
--
-- entity_types.atlas_index lets one entity_type point at a single 32x32
-- cell within its sprite asset, instead of always meaning "the whole
-- PNG". This is the foundation for tile-sheet uploads: a 256x96 tile
-- sheet (8 cols x 3 rows) becomes 24 entity_types, each with the same
-- sprite_asset_id and atlas_index = 0..23 (row-major, top-left origin,
-- left-to-right per row, top-to-bottom rows -- the MDN atlas
-- convention).
--
-- Default 0 keeps existing single-frame sprite entities working
-- unchanged: index 0 == the only cell of a 32x32 PNG.
--
-- Wire format: Tile.frame in schemas/world.fbs (uint16) already exists
-- and carries this index to the runtime renderer; sim/persist's
-- encoder will start populating it from this column.

ALTER TABLE entity_types
    ADD COLUMN atlas_index INTEGER NOT NULL DEFAULT 0
        CHECK (atlas_index >= 0);

-- Helps the "do we already have an entity for cell N of this sheet?"
-- idempotency lookup that the auto-slice tile-upload pipeline runs
-- before INSERTing each cell.
CREATE INDEX entity_types_sprite_atlas_idx
    ON entity_types (sprite_asset_id, atlas_index)
    WHERE sprite_asset_id IS NOT NULL;
