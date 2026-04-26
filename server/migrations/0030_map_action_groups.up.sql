-- 0030_map_action_groups.up.sql
--
-- "Common events": named action groups callable from any automation
-- on the same realm. Solves the indie-RPG genre's classic
-- copy-paste-the-action-list problem: instead of pasting the
-- "award xp + play fanfare + flash screen" chain into every NPC
-- dialog, designers define it once as a group and call it.
--
-- Indie-RPG research §P1 #10. PLAN.md (Automations).
--
-- Tenant isolation: scoped to (map_id, name). The compile-time
-- resolver only ever consults groups in the firing automation's
-- realm; cross-realm calls are not representable in the schema.
--
-- name is the lookup key (not the surrogate id) so a designer can
-- rename a group AT publish time and the call sites pick up the new
-- definition without an FK migration. UNIQUE(map_id, name) keeps
-- references unambiguous.

CREATE TABLE map_action_groups (
    id           BIGSERIAL    PRIMARY KEY,
    map_id       BIGINT       NOT NULL REFERENCES maps(id) ON DELETE CASCADE,
    name         TEXT         NOT NULL CHECK (length(name) BETWEEN 1 AND 64),
    actions_json JSONB        NOT NULL DEFAULT '[]'::jsonb,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    UNIQUE (map_id, name)
);

CREATE INDEX map_action_groups_map_idx ON map_action_groups (map_id);
