-- name: CreateAuthSession :one
INSERT INTO auth_sessions (user_id, token_hash, expires_at, ip_address, user_agent)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetAuthSessionByToken :one
SELECT s.*, u.email, u.display_name, u.role, u.plan, u.email_verified,
       u.created_at AS user_created_at
FROM auth_sessions s
JOIN users u ON u.id = s.user_id
WHERE s.token_hash = $1
  AND s.expires_at > NOW()
  AND u.deleted_at IS NULL;

-- name: TouchAuthSession :exec
UPDATE auth_sessions SET last_active = NOW()
WHERE id = $1;

-- name: DeleteAuthSession :exec
DELETE FROM auth_sessions WHERE id = $1;

-- name: DeleteUserAuthSessions :exec
DELETE FROM auth_sessions WHERE user_id = $1;
