-- Trivial query so the sqlc-generated package compiles before any real
-- hot-path queries land. Removed in task #82+ once real queries arrive.

-- name: SmokeOne :one
SELECT 1::bigint AS one;
