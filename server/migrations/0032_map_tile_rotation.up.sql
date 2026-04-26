-- 0032_map_tile_rotation.up.sql
-- Per-placement quarter-turn tile rotation. 0 preserves existing maps.

ALTER TABLE map_tiles
    ADD COLUMN rotation_degrees SMALLINT NOT NULL DEFAULT 0;

ALTER TABLE map_tiles
    ADD CONSTRAINT map_tiles_rotation_degrees_check
    CHECK (rotation_degrees IN (0, 90, 180, 270));
