-- 0001_init.up.sql — placeholder migration so the runner has something to apply.
-- Real schema lands in later tasks (auth → assets → entities → maps → ...).

-- This file intentionally creates nothing. golang-migrate will record it in
-- schema_migrations and treat the database as initialized.
SELECT 1;
