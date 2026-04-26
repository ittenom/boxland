-- 0029_map_flags.up.sql
--
-- Per-realm scratch space for the no-code event system: switches
-- (booleans) and variables (ints) shared across automations on a map.
-- This is the "single feature most responsible for the engine's
-- accessibility" per the indie-RPG research (§P1 #9). Without this,
-- designers can't build "talk to NPC twice to unlock door" puzzles
-- without inventing custom components.
--
-- Tenant isolation: every row is scoped to a single map. Cross-realm
-- reads are forbidden at the repository layer; the caller must always
-- supply map_id as the first parameter. Because maps.created_by ->
-- designers.id, a flag belongs to exactly one tenant transitively.
--
-- value_json keeps the column polymorphic without a UNION table:
--   * kind = 'bool' -> value_json is JSON true / false
--   * kind = 'int'  -> value_json is a JSON number (int32 range
--                       enforced by the application layer; we keep it
--                       JSONB so future kinds -- 'float', 'string' --
--                       slot in without a migration churn)
--
-- updated_at lets the in-memory cache (server/internal/flags) detect
-- staleness on publish without scanning the whole table.

CREATE TABLE map_flags (
    map_id     BIGINT      NOT NULL REFERENCES maps(id) ON DELETE CASCADE,
    key        TEXT        NOT NULL CHECK (length(key) BETWEEN 1 AND 64),
    kind       TEXT        NOT NULL CHECK (kind IN ('bool', 'int')),
    value_json JSONB       NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (map_id, key)
);

-- The composite PK already covers (map_id, key) lookups; the index
-- below speeds up "all flags for a map" loads (sim startup, designer
-- inspector) without a sort.
CREATE INDEX map_flags_map_idx ON map_flags (map_id);
