-- 0027_entity_type_y_sort.up.sql
--
-- Two flags that turn an entity_type into a y-sortable, walk-behind
-- prop (trees, fences, columns) or a permanent overlay (weather FX,
-- HUD-anchored elements that move with the camera).
--
-- y_sort_anchor:
--   true  = sort against other entities on the same render layer by
--           the collider-anchor y-position. The classic Stardew /
--           Undertale "walk behind the tree" illusion.
--   false = use the entity's render layer as the only sort key
--           (current behavior; preserved for tiles, projectiles,
--           etc. whose draw order is deterministic).
--
-- draw_above_player:
--   true  = always draw above the player layer regardless of y-sort.
--           For HUD-style props that should never occlude the player.
--           Wins over y_sort_anchor when both are set.
--   false = participate normally in the comparator.
--
-- Indie-RPG research §P1 #8 ("the single biggest visible polish win
-- in pixel-art top-down RPGs"). Renderer-side; no auth surface change.

ALTER TABLE entity_types
    ADD COLUMN y_sort_anchor    BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN draw_above_player BOOLEAN NOT NULL DEFAULT false;
