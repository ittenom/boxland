-- 0002_class_ui.down.sql
--
-- Reverse of 0002_class_ui.up.sql. Strips 'ui' back out of the
-- entity_class CHECK constraint. If any rows with entity_class='ui'
-- exist at down-migration time, the ALTER will fail with a
-- check-constraint violation — that's correct: the operator must
-- drop or reclassify those rows first. We don't auto-delete them
-- because losing entity_types this way would silently break HUDs
-- and editor chrome.

ALTER TABLE entity_types
    DROP CONSTRAINT IF EXISTS entity_types_entity_class_check;

ALTER TABLE entity_types
    ADD CONSTRAINT entity_types_entity_class_check
    CHECK (entity_class IN ('tile', 'npc', 'pc', 'logic'));
