-- 0025_entity_type_atlas_index.down.sql
DROP INDEX IF EXISTS entity_types_sprite_atlas_idx;
ALTER TABLE entity_types DROP COLUMN IF EXISTS atlas_index;
