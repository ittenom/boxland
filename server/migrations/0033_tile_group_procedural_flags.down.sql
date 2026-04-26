-- 0033_tile_group_procedural_flags.down.sql

ALTER TABLE tile_groups
    DROP COLUMN IF EXISTS use_group_in_procedural,
    DROP COLUMN IF EXISTS exclude_members_from_procedural;
