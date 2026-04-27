-- 0041_drop_gen_algorithm.down.sql
ALTER TABLE maps
    ADD COLUMN gen_algorithm TEXT NOT NULL DEFAULT 'socket'
        CHECK (gen_algorithm IN ('socket', 'overlapping'));
