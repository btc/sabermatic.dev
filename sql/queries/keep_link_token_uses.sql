-- name: TryClaimKeepToken :one
-- Single-use enforcement. Returns the hash on first claim, no row otherwise.
INSERT INTO keep_link_token_uses (token_hash, user_id)
VALUES ($1, $2)
ON CONFLICT (token_hash) DO NOTHING
RETURNING token_hash;
