-- name: GetOAuthAccount :one
SELECT * FROM oauth_accounts
WHERE provider = $1 AND provider_id = $2;

-- name: CreateOAuthAccount :one
INSERT INTO oauth_accounts (user_id, provider, provider_id)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetOAuthAccountsByUser :many
SELECT * FROM oauth_accounts
WHERE user_id = $1;
