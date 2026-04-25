-- 0005_designer_ws_tickets.up.sql
--
-- One-shot, short-TTL tickets that bridge a designer's cookie session onto
-- a realm-tagged WebSocket connection. Per PLAN.md §1 "WS auth realms":
--   * Designers POST /design/ws-ticket with their session cookie.
--   * Server mints a ticket bound to (designer_id, source IP), TTL ~30s.
--   * The ticket appears in the FlatBuffers Auth message; gateway tags
--     the connection with realm=designer; ticket is consumed (one-shot).
-- Player-realm tokens (JWTs) take a separate path; they never touch this
-- table.

CREATE TABLE designer_ws_tickets (
    ticket_hash  BYTEA       PRIMARY KEY,            -- sha256 of the ticket; raw value never stored
    designer_id  BIGINT      NOT NULL REFERENCES designers(id) ON DELETE CASCADE,
    ip           INET        NOT NULL,
    expires_at   TIMESTAMPTZ NOT NULL,
    consumed_at  TIMESTAMPTZ                          -- NULL until the WS gateway redeems it
);

CREATE INDEX designer_ws_tickets_expires_idx ON designer_ws_tickets (expires_at);
