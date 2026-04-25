-- 0016_players.down.sql
DROP TABLE IF EXISTS player_email_verifications;
DROP TABLE IF EXISTS player_sessions;
DROP TABLE IF EXISTS player_oauth_links;
DROP FUNCTION IF EXISTS players_require_credential() CASCADE;
DROP TABLE IF EXISTS players;
