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

-- name: InsertLedgerEntry :one
INSERT INTO ledger_entries (user_id, grant_id, amount, reason, session_id)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: CreateGrantFromStripe :one
INSERT INTO grants (user_id, source, stripe_event_id, initial_minutes, remaining_minutes, expires_at)
VALUES ($1, $2, $3, $4, $4, $5)
ON CONFLICT (stripe_event_id) DO NOTHING
RETURNING *;

-- name: EnsureFreeGrant :exec
-- Creates the monthly free grant + ledger entry atomically. If the grant
-- already exists (ON CONFLICT), both the INSERT and the ledger SELECT
-- produce zero rows — a no-op.
WITH new_grant AS (
  INSERT INTO grants (user_id, source, initial_minutes, remaining_minutes, expires_at)
  VALUES ($1, 'free_grant', $2, $2, $3)
  ON CONFLICT (user_id, date_trunc('month', timezone('UTC', created_at))) WHERE source = 'free_grant'
  DO NOTHING
  RETURNING id, user_id, initial_minutes
)
INSERT INTO ledger_entries (user_id, grant_id, amount, reason)
SELECT user_id, id, initial_minutes, 'free_monthly'
FROM new_grant;

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

-- name: RefundSessionMinutes :many
WITH RECURSIVE
  refund_calc AS (
    SELECT GREATEST(0,
      reserved_minutes - GREATEST(1, CEIL(EXTRACT(EPOCH FROM (ended_at - started_at)) / 60))::int
    ) AS total
    FROM interview_sessions
    WHERE id = @session_id AND reserved_minutes IS NOT NULL
  ),
  reserves AS (
    SELECT grant_id, -amount AS debit,
           ROW_NUMBER() OVER (ORDER BY created_at DESC) AS rn
    FROM ledger_entries
    WHERE session_id = @session_id AND reason = 'session_reserve'
  ),
  distributed AS (
    SELECT r.grant_id, r.debit,
           LEAST(r.debit, rc.total) AS credit,
           rc.total - LEAST(r.debit, rc.total) AS remaining,
           r.rn
    FROM reserves r, refund_calc rc
    WHERE r.rn = 1

    UNION ALL

    SELECT r.grant_id, r.debit,
           LEAST(r.debit, d.remaining) AS credit,
           d.remaining - LEAST(r.debit, d.remaining) AS remaining,
           r.rn
    FROM reserves r
    JOIN distributed d ON r.rn = d.rn + 1
    WHERE d.remaining > 0
  ),
  apply_credits AS (
    UPDATE grants g
    SET remaining_minutes = remaining_minutes + d.credit
    FROM distributed d
    WHERE g.id = d.grant_id
      AND d.credit > 0
      AND g.remaining_minutes + d.credit <= g.initial_minutes
    RETURNING g.id AS grant_id, d.credit
  )
INSERT INTO ledger_entries (user_id, grant_id, amount, reason, session_id)
SELECT @user_id, ac.grant_id, ac.credit, @reason, @session_id
FROM apply_credits ac
RETURNING grant_id, amount;

-- name: FullRefundSessionMinutes :many
-- Refunds exactly @minutes back to the grants that were originally debited
-- for this session. Used by FailSession (platform error → full refund).
WITH RECURSIVE
  refund_amount AS (
    SELECT @minutes::int AS total
  ),
  reserves AS (
    SELECT grant_id, -amount AS debit,
           ROW_NUMBER() OVER (ORDER BY created_at DESC) AS rn
    FROM ledger_entries
    WHERE session_id = @session_id AND reason = 'session_reserve'
  ),
  distributed AS (
    SELECT r.grant_id, r.debit,
           LEAST(r.debit, ra.total) AS credit,
           ra.total - LEAST(r.debit, ra.total) AS remaining,
           r.rn
    FROM reserves r, refund_amount ra
    WHERE r.rn = 1

    UNION ALL

    SELECT r.grant_id, r.debit,
           LEAST(r.debit, d.remaining) AS credit,
           d.remaining - LEAST(r.debit, d.remaining) AS remaining,
           r.rn
    FROM reserves r
    JOIN distributed d ON r.rn = d.rn + 1
    WHERE d.remaining > 0
  ),
  apply_credits AS (
    UPDATE grants g
    SET remaining_minutes = remaining_minutes + d.credit
    FROM distributed d
    WHERE g.id = d.grant_id AND d.credit > 0
      AND g.remaining_minutes + d.credit <= g.initial_minutes
    RETURNING g.id AS grant_id, d.credit
  )
INSERT INTO ledger_entries (user_id, grant_id, amount, reason, session_id)
SELECT @user_id, ac.grant_id, ac.credit, @reason, @session_id
FROM apply_credits ac
RETURNING grant_id, amount;

-- name: GetBillingSnapshot :one
-- Single query to fetch all billing state needed for entitlement checks.
-- Fetch inside the caller's transaction to avoid TOCTOU.
SELECT u.plan, u.free_full_educators_used,
  COALESCE(SUM(g.remaining_minutes) FILTER (
    WHERE g.remaining_minutes > 0 AND (g.expires_at IS NULL OR g.expires_at > NOW())
  ), 0)::int AS total_balance,
  COALESCE(SUM(g.remaining_minutes) FILTER (
    WHERE g.remaining_minutes > 0 AND g.source != 'free_grant'
    AND (g.expires_at IS NULL OR g.expires_at > NOW())
  ), 0)::int AS paid_balance
FROM users u
LEFT JOIN grants g ON g.user_id = u.id
WHERE u.id = $1
GROUP BY u.id, u.plan, u.free_full_educators_used;

-- name: GetUserUsageSummary :one
SELECT
  COALESCE(SUM(remaining_minutes) FILTER (WHERE expires_at IS NULL OR expires_at > NOW()), 0)::int AS total_balance,
  COALESCE(SUM(remaining_minutes) FILTER (WHERE source = 'free_grant' AND (expires_at IS NULL OR expires_at > NOW())), 0)::int AS free_balance,
  COALESCE(SUM(remaining_minutes) FILTER (WHERE source != 'free_grant' AND (expires_at IS NULL OR expires_at > NOW())), 0)::int AS paid_balance
FROM grants
WHERE user_id = $1
  AND remaining_minutes > 0;
