-- name: CreateAuthSession :one
INSERT INTO auth_sessions (user_id, token_hash, expires_at, ip_address, user_agent)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetAuthSessionByToken :one
-- Filters out soft-revoked sessions; only returns valid live sessions.
-- (Activity queries do NOT filter on revoked_at — see GetUserLastActive.)
SELECT s.*, u.email, u.display_name, u.role, u.plan, u.email_verified,
       u.created_at AS user_created_at
FROM auth_sessions s
JOIN users u ON u.id = s.user_id
WHERE s.token_hash = $1
  AND s.expires_at > NOW()
  AND s.revoked_at IS NULL
  AND u.deleted_at IS NULL;

-- name: TouchAuthSession :exec
UPDATE auth_sessions SET last_active = NOW()
WHERE id = $1;

-- name: RevokeAuthSession :exec
-- Soft-delete: marks the row revoked but preserves it for the activity query.
UPDATE auth_sessions SET revoked_at = NOW()
WHERE id = $1 AND revoked_at IS NULL;

-- name: RevokeUserAuthSessions :exec
-- Soft-delete every active session for a user (logout-everywhere).
UPDATE auth_sessions SET revoked_at = NOW()
WHERE user_id = $1 AND revoked_at IS NULL;

-- name: GetUserLastActive :one
-- Reads across all history (including revoked sessions). Returns NULL
-- when the user has no auth_sessions rows.
--
-- The LEFT-JOIN-from-placeholder shape (rather than the simpler MAX()) is
-- a workaround: in sqlc 1.25 with pgx/v5, MAX(timestamptz_not_null) is
-- generated as a non-nullable time.Time even though the SQL semantics are
-- "NULL when zero rows match." LEFT JOIN forces sqlc's nullability
-- inference correctly. If a future sqlc version makes MAX() nullable for
-- this case, simplify back to:
--   SELECT MAX(last_active)::timestamptz FROM auth_sessions WHERE user_id = $1;
SELECT a.last_active
FROM (SELECT 1) AS _placeholder
LEFT JOIN auth_sessions a ON a.user_id = $1
ORDER BY a.last_active DESC
LIMIT 1;
