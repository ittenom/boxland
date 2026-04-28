-- 0001_init.down.sql
--
-- Drop everything created by 0001_init.up.sql. We don't expect anyone
-- to ever step down past 0001 (that's "destroy the database"), so this
-- is a brute-force teardown rather than a careful reverse-order
-- DROP TABLE chain. golang-migrate requires the file to exist and to
-- be applyable; this satisfies that contract without false precision.
--
-- The trigger and function are dropped explicitly because DROP SCHEMA
-- CASCADE handles tables/sequences/indexes but not the deferred
-- constraint trigger plumbing on its own.

DROP TRIGGER  IF EXISTS players_credential_check ON players;
DROP FUNCTION IF EXISTS players_require_credential();

-- Wipe the public schema and recreate it. Single-tenant deployment;
-- no other apps share this database.
DROP SCHEMA public CASCADE;
CREATE SCHEMA public;

-- citext is created by 0001 inside the public schema; the DROP SCHEMA
-- above takes the extension with it. Re-creating the schema leaves
-- the database in the empty state golang-migrate expects after a
-- complete reverse migration.
