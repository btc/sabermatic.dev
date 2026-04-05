-- name: CreatePurchaseGrant :exec
-- Atomically creates a purchase grant + ledger entry. Idempotent via
-- stripe_event_id: duplicate events produce zero CTE rows → no-op.
WITH new_grant AS (
  INSERT INTO grants (user_id, source, stripe_event_id, initial_minutes, remaining_minutes, expires_at)
  VALUES (@user_id, 'purchase', @stripe_event_id, @minutes, @minutes, NULL)
  ON CONFLICT (stripe_event_id) DO NOTHING
  RETURNING id, user_id, initial_minutes
)
INSERT INTO ledger_entries (user_id, grant_id, amount, reason)
SELECT user_id, id, initial_minutes, 'purchase'
FROM new_grant;

-- name: CreateSubscriptionGrant :one
-- Atomically looks up user by stripe_customer_id, creates subscription grant +
-- ledger entry, and updates user plan. Returns user_found=0 for unknown customer,
-- grants_created=0 for duplicate event.
WITH target_user AS (
  SELECT u.id FROM users u
  WHERE u.stripe_customer_id = @cust_id AND u.deleted_at IS NULL
),
new_grant AS (
  INSERT INTO grants (user_id, source, stripe_event_id, initial_minutes, remaining_minutes, expires_at)
  SELECT tu.id, 'subscription', @stripe_event_id, @minutes, @minutes, NULL
  FROM target_user tu
  ON CONFLICT (stripe_event_id) DO NOTHING
  RETURNING id, user_id, initial_minutes
),
new_ledger AS (
  INSERT INTO ledger_entries (user_id, grant_id, amount, reason)
  SELECT ng.user_id, ng.id, ng.initial_minutes, 'subscription_renewal'
  FROM new_grant ng
  RETURNING 1
),
update_plan AS (
  UPDATE users SET plan = @plan, updated_at = NOW()
  FROM new_grant ng2
  WHERE users.id = ng2.user_id
  RETURNING 1
)
SELECT
  (SELECT count(*)::int FROM target_user) AS user_found,
  (SELECT count(*)::int FROM new_grant) AS grants_created;

-- name: ReserveMinutes :many
-- Atomically reserves @minutes from the user's grants in FIFO-by-expiry order.
-- Returns one row per grant debited. Returns zero rows if balance is insufficient
-- (all-or-nothing: no mutations occur when balance < requested).
WITH RECURSIVE
  locked AS (
    SELECT id, remaining_minutes, expires_at
    FROM grants
    WHERE user_id = @user_id
      AND remaining_minutes > 0
      AND (expires_at IS NULL OR expires_at > NOW())
    FOR UPDATE
  ),
  eligible AS (
    SELECT id, remaining_minutes,
           ROW_NUMBER() OVER (ORDER BY expires_at ASC NULLS LAST) AS rn
    FROM locked
  ),
  balance_check AS (
    SELECT COALESCE(SUM(remaining_minutes), 0) AS total
    FROM eligible
  ),
  request AS (
    SELECT @minutes::int AS requested
    FROM balance_check
    WHERE total >= @minutes
  ),
  distributed AS (
    SELECT e.id AS grant_id, e.remaining_minutes,
           LEAST(e.remaining_minutes, r.requested) AS debit,
           r.requested - LEAST(e.remaining_minutes, r.requested) AS remaining,
           e.rn
    FROM eligible e, request r
    WHERE e.rn = 1

    UNION ALL

    SELECT e.id, e.remaining_minutes,
           LEAST(e.remaining_minutes, d.remaining) AS debit,
           d.remaining - LEAST(e.remaining_minutes, d.remaining) AS remaining,
           e.rn
    FROM eligible e
    JOIN distributed d ON e.rn = d.rn + 1
    WHERE d.remaining > 0
  ),
  apply_debits AS (
    UPDATE grants g
    SET remaining_minutes = g.remaining_minutes - d.debit
    FROM distributed d
    WHERE g.id = d.grant_id AND d.debit > 0
    RETURNING g.id AS grant_id, d.debit
  )
INSERT INTO ledger_entries (user_id, grant_id, amount, reason, session_id)
SELECT @user_id, ad.grant_id, -ad.debit, 'session_reserve', @session_id
FROM apply_debits ad
RETURNING grant_id, amount;

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
-- Refunds unused minutes for a completed session based on wall-clock duration.
-- Derives user_id from the session — caller only needs session_id.
-- Idempotent: returns 0 rows if session_refund ledger entries already exist.
WITH RECURSIVE
  sess AS (
    SELECT user_id, reserved_minutes, started_at, ended_at
    FROM interview_sessions
    WHERE id = @session_id AND reserved_minutes IS NOT NULL
      AND NOT EXISTS (
        SELECT 1 FROM ledger_entries
        WHERE session_id = @session_id AND reason = 'session_refund'
      )
  ),
  refund_calc AS (
    SELECT GREATEST(0,
      s.reserved_minutes - GREATEST(1, CEIL(EXTRACT(EPOCH FROM (s.ended_at - s.started_at)) / 60))::int
    ) AS total, s.user_id
    FROM sess s
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
SELECT (SELECT user_id FROM refund_calc), ac.grant_id, ac.credit, 'session_refund', @session_id
FROM apply_credits ac
RETURNING grant_id, amount;

-- name: FullRefundSessionMinutes :many
-- Refunds all reserved minutes for a session. Used by FailSession (platform error).
-- Derives user_id and reserved_minutes from the session — caller only needs session_id.
-- Idempotent: returns 0 rows if session_refund ledger entries already exist.
WITH RECURSIVE
  sess AS (
    SELECT user_id, reserved_minutes
    FROM interview_sessions
    WHERE id = @session_id AND reserved_minutes IS NOT NULL
      AND NOT EXISTS (
        SELECT 1 FROM ledger_entries
        WHERE session_id = @session_id AND reason = 'session_refund'
      )
  ),
  reserves AS (
    SELECT grant_id, -amount AS debit,
           ROW_NUMBER() OVER (ORDER BY created_at DESC) AS rn
    FROM ledger_entries
    WHERE session_id = @session_id AND reason = 'session_reserve'
  ),
  distributed AS (
    SELECT r.grant_id, r.debit,
           LEAST(r.debit, s.reserved_minutes) AS credit,
           s.reserved_minutes - LEAST(r.debit, s.reserved_minutes) AS remaining,
           r.rn
    FROM reserves r, sess s
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
SELECT (SELECT user_id FROM sess), ac.grant_id, ac.credit, @reason, @session_id
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
