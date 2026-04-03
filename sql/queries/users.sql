-- name: CreateUser :one
INSERT INTO users (email, password_hash, display_name)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetUserByEmail :one
SELECT * FROM users
WHERE email = $1 AND deleted_at IS NULL;

-- name: GetUserByID :one
SELECT * FROM users
WHERE id = $1 AND deleted_at IS NULL;

-- name: VerifyUserEmail :exec
UPDATE users SET email_verified = TRUE, updated_at = NOW()
WHERE id = $1;

-- name: UpdateUserPassword :exec
UPDATE users SET password_hash = $2, updated_at = NOW()
WHERE id = $1;

-- name: SoftDeleteUser :exec
UPDATE users SET deleted_at = NOW(), updated_at = NOW()
WHERE id = $1;

-- name: CreateOAuthUser :one
INSERT INTO users (email, email_verified, display_name)
VALUES ($1, TRUE, $2)
RETURNING *;

-- name: ReactivateUser :exec
UPDATE users SET deleted_at = NULL, email_verified = TRUE, updated_at = NOW()
WHERE id = $1;

-- name: GetUserByEmailIncludingDeleted :one
SELECT * FROM users
WHERE email = $1;

-- name: GetUserByIDIncludingDeleted :one
SELECT * FROM users
WHERE id = $1;

-- name: GetUserByStripeCustomerID :one
SELECT * FROM users
WHERE stripe_customer_id = $1 AND deleted_at IS NULL;

-- name: UpdateUserPlan :exec
UPDATE users SET plan = $2, updated_at = NOW() WHERE id = $1;

-- name: UpdateUserStripeCustomerID :exec
UPDATE users SET stripe_customer_id = $2, updated_at = NOW() WHERE id = $1;

-- name: IncrementFreeEducatorUsed :one
UPDATE users
SET free_full_educators_used = free_full_educators_used + 1
WHERE id = $1 AND free_full_educators_used < $2
RETURNING free_full_educators_used;
