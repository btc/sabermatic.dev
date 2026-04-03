# Billing & Entitlements — Design Spec

**Date**: 2026-04-03
**Status**: Draft
**Scope**: Stripe integration, grant-based minute ledger, entitlement enforcement, usage tracking. Supersedes Section 10 of the main system design spec.

---

## 1. Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Billing unit | Minutes | Correlates directly with platform costs (AI tokens, STT/TTS scale with duration). Intuitive for users. |
| Balance model | Grant buckets + append-only ledger | Grants model minute sources (free, subscription, purchase). Ledger provides full audit trail. FIFO consumption by expiry is natural. |
| Payment processor | Stripe (Checkout + Customer Portal + webhooks) | Already set up. Handles subscriptions and one-time payments. We handle entitlements. |
| Webhook processing | Synchronous in handler (verify signature, create grant, return 200) | User always exists before Stripe interaction. Grant creation is a single transaction. Stripe retries on failure. |
| Plan configuration | Go map compiled into binary | Code deploy to change pricing/limits. No schema or infrastructure changes required (FR-007). |
| Minute expiration | Free grants expire monthly. Purchased/subscription grants do not expire. | Industry standard. Prevents free-tier accumulation without penalizing paying customers. |
| Consumption order | FIFO by expiry (earliest-expiring first, never-expiring last) | Preserves purchased minutes. Users always consume the most perishable balance first. |
| Session reservation | Reserve full configured duration upfront, refund unused on completion | Prevents mid-session balance exhaustion. Clean debit/credit ledger entries. |
| Failed session handling | Full refund of reserved minutes | Goodwill-maximizing. Platform errors should be rare; cost is negligible. |
| Educator access (paid) | Full analysis included with session | Educator LLM cost is small relative to session cost. No double-charge UX. |
| Educator access (free) | One free full analysis, then preview | Conversion hook — users experience the best feature before hitting the gate. Preview uses a shorter prompt (not truncated full output), saving tokens. |
| Coach access | Paid balance required | Feature gate: has non-free grant with remaining > 0. |
| Multi-grant debit | Single code path | FIFO walk handles one grant (base case) and multiple grants (general case) identically. No branching. |
| Existing `usage_periods` table | Superseded by grants + ledger | The ledger provides strictly more granularity. Migration drops `usage_periods`. |
| `users.plan` column | Kept, updated by webhooks | Quick read for feature gates without joining grants. Webhook sets to plan name on subscription create/change, resets to `free` on subscription delete. |

---

## 2. Data Model

### New Tables (migration `004_billing.up.sql`)

```sql
CREATE TABLE grants (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id           UUID NOT NULL REFERENCES users(id),
    source            TEXT NOT NULL
                      CHECK (source IN ('free_grant', 'subscription', 'purchase', 'admin')),
    stripe_event_id   TEXT UNIQUE,  -- idempotency key for webhook-created grants
    initial_minutes   INT NOT NULL CHECK (initial_minutes > 0),
    remaining_minutes INT NOT NULL CHECK (remaining_minutes >= 0),
    expires_at        TIMESTAMPTZ,  -- NULL = never expires
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_grants_user_balance ON grants(user_id)
    WHERE remaining_minutes > 0;

CREATE TABLE ledger_entries (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id),
    grant_id    UUID NOT NULL REFERENCES grants(id),
    amount      INT NOT NULL,  -- positive = credit, negative = debit
    reason      TEXT NOT NULL
                CHECK (reason IN (
                    'free_monthly', 'subscription_renewal', 'purchase', 'admin_grant',
                    'session_reserve', 'session_refund', 'session_settle', 'error_refund',
                    'expiry_void'
                )),
    session_id  UUID REFERENCES interview_sessions(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_ledger_user_created ON ledger_entries(user_id, created_at);
CREATE INDEX idx_ledger_session ON ledger_entries(session_id)
    WHERE session_id IS NOT NULL;
```

### Schema Changes to `interview_sessions`

```sql
ALTER TABLE interview_sessions ADD COLUMN reserved_minutes INT;
```

Stores how many minutes were reserved at session creation, used to calculate refund on completion or error. NULL for sessions created before billing was implemented.

### Schema Changes to `users`

```sql
ALTER TABLE users ADD COLUMN free_full_educators_used INT NOT NULL DEFAULT 0;
```

### Dropped Table

```sql
DROP TABLE usage_periods;
```

---

## 3. Grant Lifecycle

### Free Monthly Grant

On the first of each month (or on signup), create a free grant:

```
grants: source=free_grant, initial_minutes=60, remaining_minutes=60, expires_at=end_of_month
ledger: amount=+60, reason=free_monthly
```

Triggered by: a River periodic job that runs monthly, or lazily on first session of the month (check if current-month free grant exists, create if not). Lazy approach is simpler — no cron job, no missed-execution risk.

### Subscription Grant

On `invoice.paid` webhook (includes initial subscription and renewals):

```
grants: source=subscription, initial_minutes=per_plan_config, remaining_minutes=same, expires_at=NULL
ledger: amount=+N, reason=subscription_renewal
```

Idempotent via `stripe_event_id` UNIQUE constraint.

### Purchase Grant

On `checkout.session.completed` webhook (mode=payment):

```
grants: source=purchase, initial_minutes=per_pack_config, remaining_minutes=same, expires_at=NULL
ledger: amount=+N, reason=purchase
```

Idempotent via `stripe_event_id` UNIQUE constraint.

### Admin Grant

Manual via admin endpoint (future phase 11):

```
grants: source=admin, initial_minutes=N, remaining_minutes=N, expires_at=optional
ledger: amount=+N, reason=admin_grant
```

---

## 4. Plan Configuration

```go
type Plan struct {
    Name               string
    MinutesPerMonth    int    // subscription grant size (0 for free)
    MaxDurationMinutes int    // max configurable session length
    ConcurrentSessions int    // max active sessions
    CoachAccess        bool
    EducatorAccess     EducatorAccessLevel  // Full, Preview
    FreeEducatorLimit  int    // number of free full educator analyses (free tier only)
    StripePriceID      string // empty for free
}

type EducatorAccessLevel int
const (
    Preview EducatorAccessLevel = iota
    Full
)

var Plans = map[string]Plan{
    "free": {
        Name:               "Free",
        MinutesPerMonth:    60,
        MaxDurationMinutes: 30,
        ConcurrentSessions: 1,
        CoachAccess:        false,
        EducatorAccess:     Preview,
        FreeEducatorLimit:  1,
    },
    "pro": {
        Name:               "Pro",
        MinutesPerMonth:    600, // subscription grant size per billing cycle
        MaxDurationMinutes: 180,
        ConcurrentSessions: 3,
        CoachAccess:        true,
        EducatorAccess:     Full,
        FreeEducatorLimit:  0,   // not applicable
        StripePriceID:      "price_xxx",
    },
}
```

Minute pack pricing (one-time purchases):

```go
type MinutePack struct {
    Minutes       int
    StripePriceID string
}

var MinutePacks = []MinutePack{
    {Minutes: 120, StripePriceID: "price_pack_120"},
    {Minutes: 300, StripePriceID: "price_pack_300"},
    {Minutes: 600, StripePriceID: "price_pack_600"},
}
```

All price IDs configured via environment or hardcoded — code deploy to change.

---

## 5. Stripe Integration

### Endpoints

| Method | Path | Auth | Purpose |
|---|---|---|---|
| POST | `/api/billing/checkout` | Required | Create Stripe Checkout session (subscription or one-time pack) |
| POST | `/api/billing/portal` | Required | Create Stripe Customer Portal session |
| POST | `/api/webhooks/stripe` | None (signature verified) | Receive Stripe webhook events |
| GET | `/api/me/usage` | Required | Current balance, grant breakdown, recent ledger entries |

### Checkout Flow

1. Client sends `POST /api/billing/checkout` with `{type: "subscription", plan: "pro"}` or `{type: "pack", pack_index: 0}`.
2. Handler looks up Stripe price ID from plan config or pack config.
3. If user has no `stripe_customer_id`, create a Stripe Customer and store it.
4. Create `stripe.Checkout.Session` with:
   - `customer`: user's Stripe customer ID
   - `mode`: `"subscription"` or `"payment"`
   - `line_items`: the price ID
   - `metadata`: `{"user_id": "<uuid>", "type": "subscription|pack", "pack_minutes": "N"}`
   - `success_url` / `cancel_url`: frontend URLs
5. Return `{url: session.URL}` — client redirects to Stripe.

### Webhook Processing

Handler verifies Stripe signature, parses event, dispatches by type:

**`checkout.session.completed`** (mode=payment only — one-time packs):
1. Extract `user_id` and `pack_minutes` from metadata.
2. Create grant + ledger entry. Idempotent via `stripe_event_id`.

**`invoice.paid`** (subscriptions — initial and renewal):
1. Look up user by `stripe_customer_id`.
2. Determine plan from subscription metadata or price ID lookup.
3. Create subscription grant + ledger entry. Idempotent via `stripe_event_id`.
4. Update `users.plan` to the plan name.

**`customer.subscription.deleted`** (cancellation or payment failure after dunning):
1. Look up user by `stripe_customer_id`.
2. Update `users.plan` to `"free"`.
3. Existing grants remain — user keeps purchased/remaining minutes.

**`customer.subscription.updated`** (plan change):
1. Update `users.plan` to new plan name.
2. Future grants (next renewal) will use new plan's minute allocation.

All other events: log and return 200 (ignore gracefully).

### Portal Flow

1. Client sends `POST /api/billing/portal`.
2. Handler creates `stripe.BillingPortal.Session` with user's `stripe_customer_id`.
3. Return `{url: session.URL}` — client redirects.

---

## 6. Enforcement Flow

### Session Creation (`POST /api/sessions`)

```
1. Look up plan config for user's plan.
2. Validate config_duration_minutes <= plan.MaxDurationMinutes.
3. Check concurrent sessions: COUNT(active) < plan.ConcurrentSessions.
4. Get available balance:
   SELECT COALESCE(SUM(remaining_minutes), 0)
   FROM grants
   WHERE user_id = $1
     AND remaining_minutes > 0
     AND (expires_at IS NULL OR expires_at > NOW())
5. If balance < config_duration_minutes → 403 with balance info and upgrade message.
6. Ensure current-month free grant exists (lazy creation).
7. Begin transaction:
   a. SELECT grants FOR UPDATE, ordered by expires_at ASC NULLS LAST, remaining > 0
   b. Walk grants, decrement remaining_minutes until reservation fulfilled
   c. INSERT ledger_entry per grant touched (reason: session_reserve)
   d. INSERT interview_sessions row
   e. Commit
8. Return session.
```

### Session Completion (conductor end-of-session)

```
1. actual_minutes = ceil((end_time - start_time) / 60)
2. refund = reserved_minutes - actual_minutes
3. If refund > 0:
   a. Credit back to same grant(s), reverse order
   b. INSERT ledger_entries (reason: session_refund)
```

Reserved minutes stored on `interview_sessions.reserved_minutes` (added in migration).

**Edge case — refund to expired grant:** If a session reserved minutes from a free grant that expires during the session, the refund credits back to the expired grant row. The balance query's `expires_at > NOW()` filter excludes it, so the refunded minutes are effectively lost. This is correct — you cannot un-expire minutes. The ledger still records the refund for auditability.

### Session Failure (status → failed/error)

```
1. Full refund of reserved_minutes back to original grant(s)
2. INSERT ledger_entries (reason: error_refund)
```

### Educator Access (`GET /api/sessions/:id/educator`)

```
1. Has non-free grant with remaining > 0? → generate full analysis
2. No paid balance + free_full_educators_used < plan.FreeEducatorLimit? → generate full, increment counter
3. Otherwise → generate preview (shorter prompt, teaser output)
```

Preview prompt generates a brief summary highlighting 2-3 key areas, with a note that full deep-dive analysis is available to paid users.

### Coach Access (`POST /api/coach/analyze`, `GET /api/coach/latest`)

```
1. Has non-free grant with remaining > 0? → allow
2. Otherwise → 403 "Coach analysis requires a paid plan or minute balance"
```

---

## 7. Key Queries (sqlc)

### Get Balance

```sql
-- name: GetUserBalance :one
SELECT COALESCE(SUM(remaining_minutes), 0)::int AS balance
FROM grants
WHERE user_id = $1
  AND remaining_minutes > 0
  AND (expires_at IS NULL OR expires_at > NOW());
```

### Get Balance by Type (paid vs free)

```sql
-- name: GetUserPaidBalance :one
SELECT COALESCE(SUM(remaining_minutes), 0)::int AS balance
FROM grants
WHERE user_id = $1
  AND remaining_minutes > 0
  AND source != 'free_grant'
  AND (expires_at IS NULL OR expires_at > NOW());
```

### Select Grants for Reservation (FIFO by expiry)

```sql
-- name: SelectGrantsForReservation :many
SELECT id, remaining_minutes
FROM grants
WHERE user_id = $1
  AND remaining_minutes > 0
  AND (expires_at IS NULL OR expires_at > NOW())
ORDER BY expires_at ASC NULLS LAST
FOR UPDATE;
```

### Debit Grant

```sql
-- name: DebitGrant :one
UPDATE grants
SET remaining_minutes = remaining_minutes - $2
WHERE id = $1
  AND remaining_minutes >= $2
RETURNING remaining_minutes;
```

### Credit Grant (refund)

```sql
-- name: CreditGrant :one
UPDATE grants
SET remaining_minutes = remaining_minutes + $2
WHERE id = $1
RETURNING remaining_minutes;
```

### Insert Ledger Entry

```sql
-- name: InsertLedgerEntry :one
INSERT INTO ledger_entries (user_id, grant_id, amount, reason, session_id)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;
```

### Create Grant (idempotent for Stripe events)

```sql
-- name: CreateGrantFromStripe :one
INSERT INTO grants (user_id, source, stripe_event_id, initial_minutes, remaining_minutes, expires_at)
VALUES ($1, $2, $3, $4, $4, $5)
ON CONFLICT (stripe_event_id) DO NOTHING
RETURNING *;
```

Returns no rows if the `stripe_event_id` already exists (duplicate webhook delivery). Caller checks for empty result and treats as success. Not used for free grants — those use `GetFreeGrantForMonth` as their idempotency check.

### Create Free Grant

```sql
-- name: CreateFreeGrant :one
INSERT INTO grants (user_id, source, initial_minutes, remaining_minutes, expires_at)
VALUES ($1, 'free_grant', $2, $2, $3)
RETURNING *;
```

Called only after `GetFreeGrantForMonth` confirms no grant exists for the current period.

### Check Free Grant Exists for Period

```sql
-- name: GetFreeGrantForMonth :one
SELECT id FROM grants
WHERE user_id = $1
  AND source = 'free_grant'
  AND created_at >= $2
  AND created_at < $3
LIMIT 1;
```

### Get User Usage Summary

```sql
-- name: GetUserUsageSummary :one
SELECT
  COALESCE(SUM(remaining_minutes) FILTER (WHERE expires_at IS NULL OR expires_at > NOW()), 0)::int AS total_balance,
  COALESCE(SUM(remaining_minutes) FILTER (WHERE source = 'free_grant' AND (expires_at IS NULL OR expires_at > NOW())), 0)::int AS free_balance,
  COALESCE(SUM(remaining_minutes) FILTER (WHERE source != 'free_grant' AND (expires_at IS NULL OR expires_at > NOW())), 0)::int AS paid_balance
FROM grants
WHERE user_id = $1
  AND remaining_minutes > 0;
```

---

## 8. Package Structure

```
internal/
  billing/
    plans.go          # Plan map, MinutePack config, EducatorAccessLevel
    entitlement.go    # HasPaidBalance, CanAccessEducator, CanAccessCoach
  backend/
    billing.go        # CreateCheckoutSession, CreatePortalSession,
                      # HandleStripeWebhook, ReserveMinutes, RefundMinutes,
                      # GetBalance, GetUsageSummary, EnsureFreeGrant
  handler/
    billing.go        # PostCheckout, PostPortal, PostStripeWebhook, GetUsage
  db/queries/
    grants.sql        # All grant queries from Section 7
    ledger.sql        # Ledger insert + query by user/session
cmd/drill/
  migrations/
    004_billing.up.sql    # grants, ledger_entries, alter users, drop usage_periods
    004_billing.down.sql
```

### Integration Points (changes to existing code)

- **`internal/backend/session.go`** — `CreateSession` gains entitlement check + minute reservation. `EndSession` gains refund logic.
- **`internal/handler/session.go`** — error responses for insufficient balance.
- **`internal/handler/educator.go`** — educator access check (full vs preview) before generation.
- **`internal/handler/coach.go`** — paid balance check before allowing coach analysis.
- **`internal/jobs/evaluate.go`** — no change (evaluation runs regardless of tier).
- **`internal/auth/middleware.go`** — no change (AuthUser already carries `Plan`).
- **`cmd/drill/main.go`** — register new routes, pass Stripe config to Backend.

---

## 9. API Response Shapes

### `GET /api/me/usage`

```json
{
  "total_balance": 318,
  "free_balance": 18,
  "paid_balance": 300,
  "grants": [
    {
      "id": "uuid",
      "source": "free_grant",
      "initial_minutes": 60,
      "remaining_minutes": 18,
      "expires_at": "2026-05-01T00:00:00Z"
    },
    {
      "id": "uuid",
      "source": "purchase",
      "initial_minutes": 300,
      "remaining_minutes": 300,
      "expires_at": null
    }
  ],
  "recent_activity": [
    {
      "amount": -20,
      "reason": "session_reserve",
      "session_id": "uuid",
      "created_at": "2026-04-07T14:30:00Z"
    }
  ]
}
```

### `POST /api/billing/checkout`

Request: `{"type": "subscription", "plan": "pro"}` or `{"type": "pack", "pack_index": 0}`
Response: `{"url": "https://checkout.stripe.com/..."}`

### `POST /api/billing/portal`

Response: `{"url": "https://billing.stripe.com/..."}`

### Insufficient Balance Error (403)

```json
{
  "error": "insufficient_balance",
  "message": "You need 30 minutes but only have 18 available.",
  "balance": 18,
  "required": 30
}
```

---

## 10. Traceability

| FR | Requirement | Design |
|---|---|---|
| FR-007 | Plan structures modifiable without architectural changes | Plans are a Go map compiled into the binary. Code deploy to change, no schema changes. See Section 4. |
| FR-008 | Plans gate sessions/month, duration, features, concurrency | Minutes-based gating via grant balance. Duration validated against plan max. Coach/educator gated by paid balance. Concurrent sessions via COUNT(active). See Section 6. |
| FR-009 | Free tier same quality as paid | Same AI model, prompts, voice I/O. Only minute allowance and feature access differ. |
| FR-010 | Educator: full for paid, preview for free | Paid balance → full. Free tier gets one full analysis (conversion hook), then preview via shorter prompt. See Section 6. |
| FR-011 | Real-time entitlement enforcement | Reserve minutes atomically at session creation via SELECT FOR UPDATE + decrement. 403 with balance info if insufficient. See Section 6. |
| FR-012 | Payment processor integration | Stripe Checkout (subscription + one-time), Customer Portal, webhooks. See Section 5. |

---

## 11. Configuration

New environment variables:

```
STRIPE_SECRET_KEY       # Stripe API key
STRIPE_WEBHOOK_SECRET   # Webhook endpoint signing secret
STRIPE_PRO_PRICE_ID     # Price ID for pro subscription
STRIPE_PACK_120_PRICE   # Price ID for 120-minute pack
STRIPE_PACK_300_PRICE   # Price ID for 300-minute pack
STRIPE_PACK_600_PRICE   # Price ID for 600-minute pack
```

Added to `internal/config/config.go` as a `Stripe` sub-struct.

---

## 12. What This Spec Does NOT Cover

- **Admin dashboard for billing** — deferred to Phase 11 (Admin).
- **Specific dollar pricing** — business decision, not architecture. Price IDs are configured externally.
- **Proration on plan changes** — Stripe handles this natively via Customer Portal.
- **Dunning and failed payments** — Stripe handles retry logic and sends dunning emails. We react to the terminal `customer.subscription.deleted` event.
- **Frontend billing UI** — covered by the UI phase spec. This spec covers backend only.
- **Tax handling** — Stripe Tax or manual configuration in Stripe Dashboard, not in application code.
