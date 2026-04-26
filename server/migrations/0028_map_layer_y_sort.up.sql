-- 0028_map_layer_y_sort.up.sql
--
-- Per-layer opt-in for foot-position y-sorting. Designers turn this on
-- for the layer the player walks on (so trees + the player z-fight by
-- y-position) and leave it off for terrain, water, ceilings, etc.
-- Default off preserves existing behavior on every existing map.
--
-- Indie-RPG research §P1 #8.

ALTER TABLE map_layers
    ADD COLUMN y_sort_entities BOOLEAN NOT NULL DEFAULT false;
