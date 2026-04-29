-- 0002_class_ui.up.sql
--
-- Adds the 'ui' value to the entity_types.entity_class CHECK constraint
-- so UI sprites (button frames, panel backgrounds, sliders, etc.) can be
-- modeled as first-class entity_types alongside tiles, NPCs, PCs, and
-- logic entities.
--
-- Per the holistic redesign, this lets the design tool's chrome and the
-- player-facing in-game HUD draw from the same entity_types catalog —
-- the same widget the level-editor button uses is the one a designer
-- can drop on a HUD anchor in their game.
--
-- Postgres auto-names unnamed CHECK constraints `<table>_<column>_check`.
-- We rely on that convention to drop and recreate cleanly.

ALTER TABLE entity_types
    DROP CONSTRAINT IF EXISTS entity_types_entity_class_check;

ALTER TABLE entity_types
    ADD CONSTRAINT entity_types_entity_class_check
    CHECK (entity_class IN ('tile', 'npc', 'pc', 'logic', 'ui'));
