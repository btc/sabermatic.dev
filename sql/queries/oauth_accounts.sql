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

-- name: LinkOAuthAccount :one
INSERT INTO oauth_accounts (user_id, provider, provider_id)
VALUES (@user_id, @provider, @provider_id)
ON CONFLICT (provider, provider_id) DO NOTHING
RETURNING *;
