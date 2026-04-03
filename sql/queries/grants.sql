-- name: GetUserBalance :one
SELECT COALESCE(SUM(remaining_minutes), 0)::int AS balance
FROM grants
WHERE user_id = $1
  AND remaining_minutes > 0
  AND (expires_at IS NULL OR expires_at > NOW());

-- name: GetUserPaidBalance :one
SELECT COALESCE(SUM(remaining_minutes), 0)::int AS balance
FROM grants
WHERE user_id = $1
  AND remaining_minutes > 0
  AND source != 'free_grant'
  AND (expires_at IS NULL OR expires_at > NOW());

-- name: SelectGrantsForReservation :many
SELECT id, remaining_minutes
FROM grants
WHERE user_id = $1
  AND remaining_minutes > 0
  AND (expires_at IS NULL OR expires_at > NOW())
ORDER BY expires_at ASC NULLS LAST
FOR UPDATE;

-- name: DebitGrant :one
UPDATE grants
SET remaining_minutes = remaining_minutes - $2
WHERE id = $1
  AND remaining_minutes >= $2
RETURNING remaining_minutes;

-- name: CreditGrant :one
UPDATE grants
SET remaining_minutes = remaining_minutes + $2
WHERE id = $1
  AND remaining_minutes + $2 <= initial_minutes
RETURNING remaining_minutes;

-- name: InsertLedgerEntry :one
INSERT INTO ledger_entries (user_id, grant_id, amount, reason, session_id)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: CreateGrantFromStripe :one
INSERT INTO grants (user_id, source, stripe_event_id, initial_minutes, remaining_minutes, expires_at)
VALUES ($1, $2, $3, $4, $4, $5)
ON CONFLICT (stripe_event_id) DO NOTHING
RETURNING *;

-- name: CreateFreeGrant :one
INSERT INTO grants (user_id, source, initial_minutes, remaining_minutes, expires_at)
VALUES ($1, 'free_grant', $2, $2, $3)
ON CONFLICT (user_id, date_trunc('month', timezone('UTC', created_at))) WHERE source = 'free_grant'
DO NOTHING
RETURNING *;

-- name: GetFreeGrantForMonth :one
SELECT id FROM grants
WHERE user_id = $1
  AND source = 'free_grant'
  AND expires_at = $2
LIMIT 1;

-- name: ListActiveGrants :many
SELECT id, source, initial_minutes, remaining_minutes, expires_at, created_at
FROM grants
WHERE user_id = $1
  AND remaining_minutes > 0
  AND (expires_at IS NULL OR expires_at > NOW())
ORDER BY expires_at ASC NULLS LAST;

-- name: GetRecentLedgerEntries :many
SELECT amount, reason, session_id, created_at
FROM ledger_entries
WHERE user_id = $1
ORDER BY created_at DESC
LIMIT $2;

-- name: GetSessionReservationEntries :many
SELECT grant_id, amount
FROM ledger_entries
WHERE session_id = $1 AND reason = 'session_reserve'
ORDER BY created_at DESC;

-- name: GetUserUsageSummary :one
SELECT
  COALESCE(SUM(remaining_minutes) FILTER (WHERE expires_at IS NULL OR expires_at > NOW()), 0)::int AS total_balance,
  COALESCE(SUM(remaining_minutes) FILTER (WHERE source = 'free_grant' AND (expires_at IS NULL OR expires_at > NOW())), 0)::int AS free_balance,
  COALESCE(SUM(remaining_minutes) FILTER (WHERE source != 'free_grant' AND (expires_at IS NULL OR expires_at > NOW())), 0)::int AS paid_balance
FROM grants
WHERE user_id = $1
  AND remaining_minutes > 0;
