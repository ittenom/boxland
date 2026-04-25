-- 0004_designers.up.sql
--
-- Designer realm: people who author content via the design tools. Strictly
-- separated from the player realm; designer sessions cannot authenticate
-- WebSocket connections as players. See PLAN.md §10 "Tenant isolation"
-- and §1 "WS auth realms".

CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE designers (
    id            BIGSERIAL   PRIMARY KEY,
    email         CITEXT      UNIQUE NOT NULL,
    password_hash TEXT        NOT NULL,
    role          TEXT        NOT NULL CHECK (role IN ('owner', 'editor', 'viewer')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE designer_sessions (
    token_hash  BYTEA       PRIMARY KEY,             -- sha256 of the cookie token; raw token never stored
    designer_id BIGINT      NOT NULL REFERENCES designers(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL,
    user_agent  TEXT,
    ip          INET
);

CREATE INDEX designer_sessions_designer_idx ON designer_sessions (designer_id);
CREATE INDEX designer_sessions_expires_idx  ON designer_sessions (expires_at);
