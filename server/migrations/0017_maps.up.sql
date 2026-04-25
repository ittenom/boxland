-- 0017_maps.up.sql
--
-- Authored + procedural map definitions. mode discriminates the editing
-- surface (PLAN.md §4f vs §4g); seed is non-null for procedural maps.
-- reset_rules_json drives the per-map reset engine (PLAN.md §4k).
--
-- instancing_mode and persistence_mode mirror the design-tool radio
-- buttons (PLAN.md §1 "Multiplayer scope" / "Map persistence").

CREATE TABLE maps (
    id                       BIGSERIAL    PRIMARY KEY,
    name                     TEXT         NOT NULL UNIQUE,
    width                    INTEGER      NOT NULL CHECK (width  >= 1),
    height                   INTEGER      NOT NULL CHECK (height >= 1),
    public                   BOOLEAN      NOT NULL DEFAULT false,
    instancing_mode          TEXT         NOT NULL DEFAULT 'shared'
                                          CHECK (instancing_mode IN ('shared', 'per_user', 'per_party')),
    persistence_mode         TEXT         NOT NULL DEFAULT 'persistent'
                                          CHECK (persistence_mode IN ('persistent', 'transient')),
    refresh_window_seconds   INTEGER,
    reset_rules_json         JSONB        NOT NULL DEFAULT '{}'::jsonb,
    mode                     TEXT         NOT NULL DEFAULT 'authored'
                                          CHECK (mode IN ('authored', 'procedural')),
    seed                     BIGINT,
    spectator_policy         TEXT         NOT NULL DEFAULT 'public'
                                          CHECK (spectator_policy IN ('public', 'private', 'invite')),
    created_by               BIGINT       NOT NULL REFERENCES designers(id),
    created_at               TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at               TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX maps_created_by_idx ON maps (created_by);
