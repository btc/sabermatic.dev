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

-- name: UpdateUserStripeCustomerID :exec
UPDATE users SET stripe_customer_id = $2, updated_at = NOW() WHERE id = $1;

-- name: UpdatePlanByStripeCustomer :execrows
UPDATE users SET plan = @plan, updated_at = NOW()
WHERE stripe_customer_id = @stripe_customer_id AND deleted_at IS NULL;

-- name: IncrementFreeEducatorUsed :one
UPDATE users
SET free_full_educators_used = free_full_educators_used + 1
WHERE id = $1 AND free_full_educators_used < $2
RETURNING free_full_educators_used;

-- name: DeleteAccount :exec
-- Soft-deletes the user and hard-deletes all auth sessions in one round-trip
-- (data minimization / GDPR). Logout uses RevokeAuthSession / RevokeUserAuthSessions
-- in auth_sessions.sql instead. Idempotent: re-calling on a deleted user is a
-- no-op on the user row.
WITH soft_delete AS (
    UPDATE users SET deleted_at = NOW(), updated_at = NOW()
    WHERE id = @id AND deleted_at IS NULL
)
DELETE FROM auth_sessions WHERE user_id = @id;

-- name: UpdateUserDisplayName :one
UPDATE users SET display_name = @display_name, updated_at = NOW()
WHERE id = @id AND deleted_at IS NULL
RETURNING *;

-- name: GetUserByEmailForUpdate :one
SELECT * FROM users
WHERE email = @email
FOR UPDATE;

-- name: CreateOAuthUserOrNoop :one
INSERT INTO users (email, email_verified, display_name)
VALUES (@email, TRUE, @display_name)
ON CONFLICT (email) DO NOTHING
RETURNING *;
