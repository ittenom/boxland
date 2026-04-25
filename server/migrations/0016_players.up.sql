-- 0016_players.up.sql
--
-- Player realm: people who play the game. Strictly separated from the
-- designer realm; player credentials cannot authenticate WebSocket
-- connections as designers (PLAN.md §10 "Tenant isolation").
--
-- password_hash NULL is allowed for OAuth-only accounts; the CHECK
-- constraint requires either a password or at least one OAuth link.
--
-- email_verified gates join-game access. Verification flow sends a
-- short-lived token via SMTP (Mailpit in dev) the client redeems with
-- POST /auth/player/verify.

CREATE TABLE players (
    id              BIGSERIAL    PRIMARY KEY,
    email           CITEXT       UNIQUE NOT NULL,
    password_hash   TEXT,
    email_verified  BOOLEAN      NOT NULL DEFAULT false,
    display_name    TEXT,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE TABLE player_oauth_links (
    player_id          BIGINT NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    provider           TEXT   NOT NULL CHECK (provider IN ('google', 'apple', 'discord')),
    provider_user_id   TEXT   NOT NULL,
    linked_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (provider, provider_user_id)
);

CREATE INDEX player_oauth_links_player_idx ON player_oauth_links (player_id);

-- The CHECK constraint declared in PLAN.md §4c: every player has either
-- a password or at least one OAuth link. We enforce it via a trigger
-- because pure CHECK constraints can't span tables in Postgres.
CREATE OR REPLACE FUNCTION players_require_credential()
RETURNS TRIGGER AS $$
BEGIN
    IF NEW.password_hash IS NULL THEN
        IF NOT EXISTS (
            SELECT 1 FROM player_oauth_links WHERE player_id = NEW.id
        ) THEN
            RAISE EXCEPTION 'players: row id=% has neither password nor OAuth link', NEW.id
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- The trigger is DEFERRED to commit time so a single transaction can
-- INSERT players + INSERT player_oauth_links and have both visible to
-- the check.
CREATE CONSTRAINT TRIGGER players_credential_check
    AFTER INSERT OR UPDATE OF password_hash ON players
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW
    EXECUTE FUNCTION players_require_credential();

CREATE TABLE player_sessions (
    refresh_token_hash  BYTEA       PRIMARY KEY,  -- sha256 of the refresh token
    player_id           BIGINT      NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at          TIMESTAMPTZ NOT NULL,
    user_agent          TEXT,
    ip                  INET
);

CREATE INDEX player_sessions_player_idx  ON player_sessions (player_id);
CREATE INDEX player_sessions_expires_idx ON player_sessions (expires_at);

-- Email verification tokens. Sent on signup; consumed by /auth/player/verify.
-- Short-lived; expired rows get swept by a periodic job.
CREATE TABLE player_email_verifications (
    token_hash   BYTEA       PRIMARY KEY,
    player_id    BIGINT      NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    expires_at   TIMESTAMPTZ NOT NULL,
    consumed_at  TIMESTAMPTZ
);

CREATE INDEX player_email_verifications_player_idx ON player_email_verifications (player_id);
