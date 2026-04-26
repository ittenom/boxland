-- 0036_map_gen_algorithm.up.sql
--
-- Procedural maps now pick between two generation algorithms:
--
--   * 'socket'    — strict edge-socket WFC with backtracking. The
--                   default. Best for tile-perfect collision/pathing
--                   where mismatches are unacceptable.
--
--   * 'pixel_wfc' — pixel-similarity WFC. Adjacency rules are inferred
--                   from edge-pixel similarity of each tile's sprite
--                   frame. No backtracking, no socket setup required.
--                   Best for flat decorative tiles where small visual
--                   mismatches are fine.
--
-- Only meaningful when mode='procedural'; for mode='authored' the
-- column is ignored (we keep the row's value to avoid losing the
-- designer's intent if they flip back to procedural).

ALTER TABLE maps
    ADD COLUMN gen_algorithm TEXT NOT NULL DEFAULT 'socket'
        CHECK (gen_algorithm IN ('socket', 'pixel_wfc'));
