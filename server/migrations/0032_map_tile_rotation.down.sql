-- 0032_map_tile_rotation.down.sql

ALTER TABLE map_tiles
    DROP CONSTRAINT IF EXISTS map_tiles_rotation_degrees_check;

ALTER TABLE map_tiles
    DROP COLUMN IF EXISTS rotation_degrees;
