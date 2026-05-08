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

-- name: SetUserAutoCancelState :exec
-- Called by idleunsub.HandleInvoiceUpcoming when our trigger fires.
-- Sets BOTH cache flags so the auto-reverse middleware gate fires for this user.
UPDATE users
SET sub_cancel_at_period_end = TRUE,
    sub_cancel_is_auto       = TRUE,
    sub_current_period_start = $2
WHERE id = $1;

-- name: ClearUserAutoCancelState :exec
-- Called by KeepSubscription / AutoReverse on reversal. Sets banner flag.
UPDATE users
SET sub_cancel_at_period_end = FALSE,
    sub_cancel_is_auto       = FALSE,
    pending_kept_banner      = TRUE,
    sub_current_period_start = $2
WHERE id = $1;

-- name: SyncSubStateFromWebhook :exec
-- Called by handleSubscriptionUpdated. Does NOT touch sub_cancel_is_auto:
-- only our handler sets that flag; webhook sync must not overwrite it.
UPDATE users
SET stripe_subscription_id   = $2,
    sub_cancel_at_period_end = $3,
    sub_current_period_start = $4
WHERE id = $1;

-- name: ClearSubStateOnDeletion :exec
-- Called by handleSubscriptionDeleted. Clears all sub state and downgrades plan.
UPDATE users
SET stripe_subscription_id   = NULL,
    sub_cancel_at_period_end = FALSE,
    sub_cancel_is_auto       = FALSE,
    sub_current_period_start = NULL,
    plan                     = 'free'
WHERE id = $1;

-- name: ClearKeptBanner :exec
UPDATE users SET pending_kept_banner = FALSE WHERE id = $1;

-- name: ClearStaleKeptBanners :exec
-- Periodic hygiene: clear banners that are older than 14 days.
-- (Run from a tiny daily cron; the spec calls this out as low-priority cleanup.)
UPDATE users
SET pending_kept_banner = FALSE
WHERE pending_kept_banner = TRUE
  AND id IN (
    SELECT user_id FROM user_events
    WHERE event_type = 'subscription_kept'
      AND created_at < NOW() - INTERVAL '14 days'
  );

-- name: GetUserAutoCancelGates :one
-- Used by AutoReverse to defensively re-read both gates.
SELECT sub_cancel_at_period_end, sub_cancel_is_auto,
       stripe_subscription_id,   sub_current_period_start
FROM users
WHERE id = $1;

-- name: LockUserForSubDecision :one
-- Per-user mutex for the cancel transaction. Acquires a row-level lock that
-- serializes concurrent invoice.upcoming evaluations for the same user.
-- Must be inside a transaction; releases on COMMIT or ROLLBACK.
SELECT id FROM users WHERE id = $1 FOR UPDATE;
