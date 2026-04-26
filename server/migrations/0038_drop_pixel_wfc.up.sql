-- 0038_drop_pixel_wfc.up.sql
--
-- Replace the experimental "pixel_wfc" generator with the new
-- "overlapping" generator (Maxim Gumin's overlapping-model WFC,
-- adapted from github.com/mxgmn/WaveFunctionCollapse — MIT).
--
-- Pixel-WFC produced visually noisy output (per-tile edge similarity
-- carries no NxN context). The overlapping engine learns from a small
-- designer-painted "sample patch" and emits only NxN windows that
-- appeared in that sample, which produces dramatically more coherent
-- maps. See server/internal/maps/wfc/overlapping.go.
--
-- Migration approach: any existing maps stored as 'pixel_wfc' (pre-
-- release test data only — confirmed with carson) get migrated to
-- 'overlapping' so the new CHECK constraint accepts them. The old
-- value cannot be re-inserted after this migration.

-- Step 1: rename existing rows.
UPDATE maps SET gen_algorithm = 'overlapping' WHERE gen_algorithm = 'pixel_wfc';

-- Step 2: swap the CHECK constraint.
ALTER TABLE maps DROP CONSTRAINT IF EXISTS maps_gen_algorithm_check;
ALTER TABLE maps
    ADD CONSTRAINT maps_gen_algorithm_check
    CHECK (gen_algorithm IN ('socket', 'overlapping'));
