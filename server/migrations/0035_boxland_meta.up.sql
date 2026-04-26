-- 0035_boxland_meta.up.sql
--
-- A tiny key/value table for singleton Boxland-process metadata that
-- has nowhere better to live. Initial use: tracking the last running
-- Boxland version so the updater can detect *which* version this
-- database was last booted under (across the long dev process where
-- arbitrary v0.X jumps will happen).
--
-- Why a table and not a JSON file on disk: the database is the one
-- artifact whose schema must move in lockstep with the code, so
-- recording "this DB was last touched by code at version X" beside
-- the schema itself is the right pairing. A backup carries this
-- value with it; a JSON file on a developer's laptop wouldn't.
--
-- key/value text/text is intentionally boring. We don't need typed
-- columns or migrations per key; this is a bag of operator-facing
-- breadcrumbs, not a feature surface. Add new keys by writing them.
CREATE TABLE boxland_meta (
    key        TEXT         PRIMARY KEY,
    value      TEXT         NOT NULL,
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT now()
);
