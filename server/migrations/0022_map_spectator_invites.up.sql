-- 0022_map_spectator_invites.up.sql
--
-- Per-map spectator allowlist used when maps.spectator_policy = 'invite'
-- (PLAN.md §4m). Public maps ignore this table; private maps reject all
-- player-realm spectators outright.
--
-- Granted-by is the designer who issued the invite; recorded for audit
-- and for future revocation UI. The (map_id, player_id) primary key
-- guarantees one invite per (map, player); re-inviting an existing
-- player is an idempotent UPSERT in the maps service.

CREATE TABLE map_spectator_invites (
    map_id      BIGINT       NOT NULL REFERENCES maps(id)    ON DELETE CASCADE,
    player_id   BIGINT       NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    granted_by  BIGINT       NOT NULL REFERENCES designers(id),
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    PRIMARY KEY (map_id, player_id)
);

CREATE INDEX map_spectator_invites_player_idx ON map_spectator_invites (player_id);
