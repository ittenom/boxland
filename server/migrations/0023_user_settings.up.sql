-- 0023_user_settings.up.sql
--
-- Per-user preferences (font, audio defaults, spectator preferences,
-- control rebindings) for both realms. PLAN.md §5g + §6h: "Settings
-- persisted to localStorage and synced to server".
--
-- Realm-tagged so the same physical user can have distinct designer +
-- player settings (a designer might want a smaller font for dense UI;
-- a player wants chunkier nameplates). subject_id resolves to either
-- designers.id or players.id depending on realm.
--
-- payload_json shape (versioned by `v` field):
--   { "v": 1, "font": "C64esque", "audio": {"master": 80, "music": 70, "sfx": 90},
--     "spectator": {"freeCam": false}, "bindings": {"<combo>": "<command-id>"} }

CREATE TABLE user_settings (
    realm        TEXT         NOT NULL CHECK (realm IN ('designer', 'player')),
    subject_id   BIGINT       NOT NULL,
    payload_json JSONB        NOT NULL DEFAULT '{}'::jsonb,
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    PRIMARY KEY (realm, subject_id)
);
