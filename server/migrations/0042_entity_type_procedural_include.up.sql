-- 0042_entity_type_procedural_include.up.sql
--
-- Per-entity-type "include in procedural generation" flag. Designers
-- get a small eye-icon toggle on each tile in the procedural-mode
-- palette; clicking it flips this flag. Excluded tiles are still
-- paintable by hand — they just won't appear in random fill.
--
-- Default true so existing tiles keep showing up after the migration.

ALTER TABLE entity_types
    ADD COLUMN procedural_include BOOLEAN NOT NULL DEFAULT true;
