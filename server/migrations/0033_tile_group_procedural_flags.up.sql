-- 0033_tile_group_procedural_flags.up.sql
--
-- Per-group procedural generation controls:
--   * exclude member tile entity types from the single-tile candidate pool
--   * use the tile group itself as an atomic procedural chunk

ALTER TABLE tile_groups
    ADD COLUMN exclude_members_from_procedural BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN use_group_in_procedural         BOOLEAN NOT NULL DEFAULT true;
