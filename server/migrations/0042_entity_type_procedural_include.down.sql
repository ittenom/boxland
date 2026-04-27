-- 0042_entity_type_procedural_include.down.sql
ALTER TABLE entity_types DROP COLUMN IF EXISTS procedural_include;
