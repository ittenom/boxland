-- 0041_drop_gen_algorithm.up.sql
--
-- Procedural maps no longer expose a choice of algorithm. There's one
-- engine: chunked WFC that auto-derives its sample from painted tiles
-- (with a socket-adjacency fallback when nothing has been painted).
-- The previous "socket vs overlapping" picker confused designers and
-- hid the actual cause of low-quality output (the broken preview
-- renderer). See server/internal/maps/procedural.go for the new
-- always-chunked path.

ALTER TABLE maps DROP CONSTRAINT IF EXISTS maps_gen_algorithm_check;
ALTER TABLE maps DROP COLUMN IF EXISTS gen_algorithm;
