-- 0038_drop_pixel_wfc.down.sql
--
-- Reverse 0038: re-allow 'pixel_wfc'. Any rows that were migrated up
-- from 'pixel_wfc' to 'overlapping' are NOT moved back — we have no
-- way to know which 'overlapping' rows originated as 'pixel_wfc' and
-- the rollback target is just "make the CHECK permissive again".
ALTER TABLE maps DROP CONSTRAINT IF EXISTS maps_gen_algorithm_check;
ALTER TABLE maps
    ADD CONSTRAINT maps_gen_algorithm_check
    CHECK (gen_algorithm IN ('socket', 'pixel_wfc', 'overlapping'));
