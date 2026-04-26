-- 0027_entity_type_y_sort.down.sql

ALTER TABLE entity_types
    DROP COLUMN draw_above_player,
    DROP COLUMN y_sort_anchor;
