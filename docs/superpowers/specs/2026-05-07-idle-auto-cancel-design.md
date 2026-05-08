# Idle Auto-Cancel — Design Spec

**Date**: 2026-05-07
**Status**: Draft (rounds 1 + 2 + 3 review applied)
**Scope**: Auto-cancel a Pro subscription when the user has been idle for two consecutive billing periods. No settings toggle — this is the default Sabermatic behavior. Establishes a Spanda, LLC product tenet: earn money for ongoing value delivered.

---

## 1. Motivation

A subscription that bills a user month after month for a service they haven't touched generates negative sentiment — remorse, distrust, "why am I still paying for this?" Industry convention is to keep collecting until the customer notices and cancels manually. We reject that convention.

This feature implements the inverse: if a Pro user goes idle for a *full* billing period, and the next period also looks idle as it nears renewal, we set `cancel_at_period_end=true` *before* the next charge fires. The customer is informed, given a frictionless way to keep the subscription, and given access through their already-paid period regardless. They are never billed for a period they're about to skip too.

This is a tenet for all Spanda, LLC products — Sabermatic[.DEV] is the first to implement it.

---

## 2. Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Activity definition | Any authenticated request — touches `auth_sessions.last_active` | Matches user intent: "visiting the site while logged in counts." Drill activity is a strict subset (drill RPCs are authenticated). |
| Activity SQL source | `MAX(last_active)` from `auth_sessions` for the user, **across all rows including soft-revoked ones** | Single-table query. The activity question is *historical* ("when did this user last act?"), not *current* ("is this session valid?"), so it intentionally ignores `revoked_at`. |
| `auth_sessions` retention | Soft delete: add `revoked_at`; logout = `UPDATE`, account-delete = hard `DELETE` | Preserves rows through logout so the activity query can read across history. Audit trail. Consistent with existing `users.deleted_at` pattern. |
| Trigger rule | `MAX(last_active) < (current_period.start − 1 interval)` AND `current_period.start ≥ users.idle_eligible_after` | Two periods of evidence, evaluated near renewal. Second clause grandfathers periods that began before this feature shipped. |
| Evaluation moment | Stripe `invoice.upcoming` webhook (~7 days before next renewal) | Event-driven; no cron; Stripe carries the period boundaries. |
| Period boundaries source | `stripe-go/v82` puts these on `SubscriptionItem`, not `Subscription`. We read `sub.Items.Data[0].CurrentPeriodStart` and `Price.Recurring.Interval` from a fresh `subscription.Get(...)` at evaluation time. | The `invoice.upcoming` event's `Invoice.Lines.Data[0].Period` covers the *upcoming* period (after renewal). To get the period currently ending, we fetch the subscription. |
| JSONB timestamp format for period anchors in `user_events.metadata` | RFC3339 strings, written via `json.Marshal` of a Go `time.Time` field on a typed metadata struct. Read via `(metadata->>'current_period_start')::timestamptz`. | Postgres can cast RFC3339 text to `timestamptz` reliably across versions and timezones. Avoids the int64-Unix vs ISO ambiguity. The Go writer is the only producer; using a typed struct prevents drift. |
| Cancel mechanic | `cancel_at_period_end=true` | User keeps already-paid period. No refund. Reversible. |
| Distinguishing auto vs manual cancel | `users.sub_cancel_is_auto` BOOLEAN | Set true *only* when our handler initiates the cancel. Stripe's `cancel_at_period_end` is shared with manual portal cancels; without this distinction, our auto-reverse path would silently undo user-initiated cancellations. |
| Notification | Single email at decision moment with `[Keep my subscription]` link | One transparent message; the link reverses the cancel via signed token. |
| Reversal triggers | (a) clicking the email link, (b) any authed activity during the cancel window — but only if `sub_cancel_is_auto=true` | Activity in the current period invalidates the trigger condition; auto-reverse is rule-consistent. We never reverse a user's deliberate cancellation. |
| Reversal feedback | Banner on next page load + confirmation email on every reversal | Transparent and communicative. |
| Settings toggle | None | This is the default behavior; making it opt-in dilutes the tenet. |
| Code home | `internal/feat/idleunsub/` | New `internal/feat/` convention for cohesive feature packages. First entry. |
| Idempotency | New `stripe_webhook_dedup` table with `event_id TEXT PRIMARY KEY`. Insert-then-act pattern: ON CONFLICT DO NOTHING; if the insert returns no row, the event was already handled. | Stripe retries webhooks. Same event ID = same logical event. A separate dedup table is cleaner than embedding `stripe_event_id` in `user_events.metadata` (which can't be UNIQUE-constrained without expression indexes). |
| Multi-firing of `invoice.upcoming` for same period | State-aware predicate before acting: skip if Stripe says `cancel_at_period_end=true` already; skip if a `subscription_kept` event exists more recently than the most recent `subscription_auto_canceled` for the same `(user_id, subscription_id, current_period_start)`. | Stripe re-fires `invoice.upcoming` (with a fresh `event.ID`) when the upcoming invoice changes (proration, plan switch, coupon edit). Event-ID dedup alone would re-act on each firing. |
| Skip evaluation when sub is `trialing`, `past_due`, `unpaid`, `incomplete` | Early return | Trial: no prior period to evaluate. Past due / unpaid: Stripe's dunning logic owns the lifecycle; we don't fight it. |
| Period storage | Cache `sub_current_period_start` and `stripe_subscription_id` on `users` for hot-path reads (auto-reverse middleware gate). Authoritative read for cancel decisions = fresh Stripe `subscription.Get`. | Cache supports the cheap "is this user's sub auto-canceled and they just acted?" check. Authoritative reads avoid stale-cache risk on the cancel decision itself. |
| Multiple subscriptions per user | Out of scope | Drill assumes one subscription per user (`users.plan` is single-valued). If/when multi-sub is added, this feature is revisited. |

---

## 3. Trigger Rule

**Rule:** at the moment Stripe fires `invoice.upcoming` for subscription S of user U, evaluate:

```
// 1. Early returns
fetch S = subscription.Get(subID)
if S.Status not in {active}:                              return  // skip trial/past_due/unpaid
if S.CancelAtPeriodEnd is already true:                   return  // already canceled (by us or user)
if NOT TryClaimWebhookEvent(event.ID):                    return  // retry-storm dedup
                                                                  //   (insert-on-conflict-do-nothing;
                                                                  //   no-row return = already handled)
if mostRecentPeriodDecisionWasKeep(U, subID, period):     return  // post-keep multi-firing dedup
if alreadyAutoCanceledThisPeriod(U, subID, period):       return  // pre-keep multi-firing dedup

// 2. Read state
last_active = SELECT MAX(last_active)
              FROM auth_sessions
              WHERE user_id = U.id
              -- (no revoked_at filter; activity is across history)

cur_period_start = S.Items.Data[0].CurrentPeriodStart   // see Data Sources below
interval         = S.Items.Data[0].Price.Recurring.Interval  // e.g., "month"
threshold        = cur_period_start - 1 interval         // start of period N-1

// 3. Trigger
if last_active < threshold AND cur_period_start >= U.idle_eligible_after:
    set cancel_at_period_end = true on S
    persist user_events row, cache update, queue email (see §4.1)
```

### Data Sources

The Stripe Go SDK v82 splits subscription period fields between `Subscription` and `SubscriptionItem`. The fields the rule cares about live here:

- `S.Items.Data[0].CurrentPeriodStart` (`int64` Unix seconds) — start of the period currently ending.
- `S.Items.Data[0].CurrentPeriodEnd` — end of the period currently ending; equals the next renewal moment.
- `S.Items.Data[0].Price.Recurring.Interval` — `"month"`, `"year"`, etc.

The `invoice.upcoming` event payload (`stripe.Invoice`) carries `Invoice.Lines.Data[0].Period.Start` and `.End` for the *upcoming* period (after renewal) — not what we want. We fetch the subscription explicitly at evaluation time.

### Example timeline

User signs up Feb 1 with monthly billing. The trigger fires for each upcoming invoice:

| When | Event | `last_active` | `cur_period_start` | `threshold` | Decision |
|---|---|---|---|---|---|
| Feb 22 | invoice.upcoming for Mar 1 renewal | Feb 8 (signup + early use) | Feb 1 | Jan 1 | last_active ≥ threshold → no cancel |
| Mar 22 | invoice.upcoming for Apr 1 renewal | Feb 8 (no Mar activity) | Mar 1 | Feb 1 | last_active ≥ threshold → no cancel (Feb activity counts) |
| Apr 22 | invoice.upcoming for May 1 renewal | Feb 8 (no Mar, no Apr activity) | Apr 1 | Mar 1 | last_active < threshold → **cancel_at_period_end=true** |

The user pays for **two** unused periods (March and April in this example) before the cancel kicks in — the first while we're establishing the idle baseline, the second while we're confirming sustained idleness. The cancel prevents the *next* charge (May 1) and every charge after.

A user who signs up Feb 1 and never returns has their cancel triggered at the Apr ~22 evaluation and loses access at May 1 — about 90 days after signup, after paying 3 monthly bills (Feb, Mar, Apr) of which two were unused.

This two-billed-but-unused-periods cost is the deliberate price of the two-period rule. The alternative (one-period rule, which would prevent the second charge) was rejected because it can't be evaluated reliably at `invoice.upcoming` (only ~23 of 30 days are observed at that moment) and because a single quiet month is a noisy signal.

### Why two periods, not one

A one-period rule would have to evaluate at `invoice.upcoming` (~T-7 days before period_end), but that means the current period isn't actually fully observed yet — we'd be calling 23 of 30 days "the full period." Inconsistent. The two-period rule resolves it: at evaluation time, period N−1 is provably 100% complete and idle, regardless of what happens in the remaining 7 days of period N.

A one-period rule is also too aggressive: a user who happens to take a single quiet month (vacation, busy quarter, surgery, parental leave) gets canceled. Two periods is meaningful evidence; one is noise.

### Plan changes, pause/resume

Stripe shifts `current_period_start` to the change date on plan changes. The same is true for portal-driven pause + resume (`pause_collection` flag + later resume). We accept both: a user paying enough attention to upgrade or resume is by definition not idle, so resetting the idle clock is correct. Documented as a known property, not a bug.

Note: paused subscriptions retain `Status='active'` (`pause_collection` is a separate field). They pass our status early-return and are evaluated normally. An idle paused sub is still idle from this feature's perspective. If we later decide we want to *exclude* paused subs from evaluation, the gate is `S.PauseCollection != nil`. Out of scope for v1.

---

## 4. Behavior

### 4.1 Cancel path

1. **T-7**: Stripe fires `invoice.upcoming`. Webhook handler in `internal/backend/billing.go` delegates to `idleunsub.HandleInvoiceUpcoming`.
2. Fetch the subscription from Stripe (`subscription.Get(subID, &SubscriptionParams{Expand: ["items"]})`).
3. Run early returns from §3 (status, already-canceled).
4. **Begin DB transaction:**
   a. `SELECT id FROM users WHERE id = $1 FOR UPDATE`. **This is the per-user mutex**: two concurrent `invoice.upcoming` deliveries for the same user cannot both pass the dedup queries below before either has committed. Without this lock, two transactions starting under READ COMMITTED can both see no prior `subscription_auto_canceled` row, both proceed, and both fire Stripe + email. The lock serializes evaluations per user; webhook concurrency is low so contention is negligible.
   b. `TryClaimWebhookEvent(event.ID, 'invoice.upcoming')` — `INSERT ... ON CONFLICT DO NOTHING RETURNING event_id`. If no row returned, ROLLBACK and return (event already handled).
   c. Run period-keyed dedup queries (`mostRecentPeriodDecisionWasKeep`, `alreadyAutoCanceledThisPeriod`). If either signals a prior decision for this period, ROLLBACK and return. Both queries are kept (rather than collapsing to just `alreadyAutoCanceledThisPeriod`) for log/observability clarity — `cancel.skipped{reason=already_kept_this_period}` vs `cancel.skipped{reason=already_canceled_this_period}` distinguish two operationally interesting cases.
   d. Read `last_active`, compute `threshold`. If trigger does not fire, ROLLBACK and return (no point claiming the event — let a future re-fire of `invoice.upcoming` for this same period have a fresh shot if state changes).
   e. **Trigger fires:** insert a `user_events` row with `event_type='subscription_auto_canceled'`, metadata `{subscription_id, stripe_event_id, current_period_start, current_period_end}` (timestamps as RFC3339 strings via the typed metadata struct, canonicalized to second precision via `time.Unix(stripeInt64, 0).UTC()`).
   f. Update `users` cache: `sub_cancel_at_period_end = true`, `sub_cancel_is_auto = true`, `sub_current_period_start = ...`, `stripe_subscription_id = ...`.
   g. **COMMIT.** The lock, the dedup row, the audit row, and the cache update are now durable together. If the transaction rolls back at any point, the next webhook retry sees an unclaimed event and starts fresh.
5. Call Stripe: `subscription.Update(id, cancel_at_period_end=true)` with `Idempotency-Key: <stripe_event_id>`. **Outside the transaction.** Stripe `Update` is naturally idempotent.
6. Queue the cancel email via `SendEmailJob` (River) using the `current_period_end` from step 4d's metadata.
7. **T-0** (period_end): Stripe naturally lets the subscription end. The existing `customer.subscription.deleted` handler resets `users.plan = 'free'` and clears the cache columns.

**Failure-mode reasoning:**
- DB transaction fails before COMMIT → no Stripe call attempted, dedup not claimed, retry will see unclaimed event and start fresh. ✓
- COMMIT succeeds, Stripe `Update` fails → cache says canceled, Stripe says not canceled. **This is the partial-failure window.** It must be handled in two places:
  1. **Server-side**: emit `idleunsub.cancel.error{reason=stripe_update_failed}` counter, page ops. Ops manually verifies Stripe state and either re-issues the cancel via Stripe API or clears the cache flag (`UPDATE users SET sub_cancel_at_period_end=false, sub_cancel_is_auto=false WHERE id=$1`).
  2. **Client-side**: `AutoReverse` (§4.3) verifies Stripe state via `subscription.Get` before calling `subscription.Update`. If Stripe says `cancel_at_period_end` is already `false`, AutoReverse silently clears the cache flag, logs a warning, and skips the email/`subscription_kept` event row. This prevents a spurious "subscription kept" email and audit row when no real reversal happened. Cost: one extra Stripe API call on the cold path that only fires for users whose cache flag is true.
- COMMIT succeeds, Stripe `Update` succeeds, email enqueue fails → cache and Stripe agree; user sees the banner on next visit and Stripe's own renewal-prevention behavior. Logged.
- Concurrent webhook deliveries for the same user → serialized by the `SELECT ... FOR UPDATE` in step 4a; only one transaction proceeds, the other waits and then sees the prior decision via the dedup queries.

### 4.2 Reversal — email link click

1. User receives the cancel email containing a signed token URL: `https://sabermatic.dev/sub/keep?t=<signed_token>`.
2. The token is HMAC-signed JSON serialized via a typed Go struct with explicit field order: `{user_id, subscription_id, action: "keep_subscription", current_period_end, iat, exp: current_period_end}`. The token carries `current_period_end` so the confirmation page (and replay) has the date without re-fetching from Stripe or relying on a possibly-stale cache. Encoded as URL-safe base64.
3. **Single-use enforcement**: the public endpoint `GET /sub/keep?t=...` (in `internal/handler/keep.go`):
   a. Verifies signature.
     - Bad signature / malformed token → **400** with a generic "this link is invalid" page. Distinguished from expiry to avoid leaking whether a given (user, sub, period) tuple ever existed.
     - Valid signature but expired (current time > `exp`) → **200** with a friendly "your subscription period has already ended" page (no Stripe call, no email). The token doesn't grant any access; expiry just means the keep window is over. A bare 400 here is user-hostile per the tenet — distinguish from tampering.
     - Spec also enforces invariant: `claims.ExpiresAt == claims.CurrentPeriodEnd.Unix()`. If they disagree (signer bug or struct drift), reject as malformed.
   b. Valid signature + not expired: computes `token_hash = sha256(token)`.
   c. INSERTs into `keep_link_token_uses (token_hash PRIMARY KEY, used_at)` with `ON CONFLICT DO NOTHING RETURNING token_hash`. If no row returned, the token was already used — render the same confirmation page using `claims.CurrentPeriodEnd` from the (still-valid-signature) token. No Stripe call, no email.
   d. On first use: calls `idleunsub.KeepSubscription(ctx, claims)`, which:
      - Confirms the subscription is still in `cancel_at_period_end=true` and `sub_cancel_is_auto=true` state (refuses to reverse a manual cancel).
      - Calls `subscription.Update(id, cancel_at_period_end=false)`. Captures the returned `subscription` object for its updated period info.
      - Updates `users` cache: `sub_cancel_at_period_end = false`, `sub_cancel_is_auto = false`, `pending_kept_banner = true`, `sub_current_period_start = <returned sub's CurrentPeriodStart>`.
      - Sends the "subscription kept" confirmation email; uses the returned subscription's `CurrentPeriodEnd` for the renewal date in the body.
      - Inserts a `user_events` row with `event_type='subscription_kept'`, metadata `{subscription_id, via: 'link', current_period_start: <RFC3339>}`. The `current_period_start` here matches the period the keep applies to and is what `mostRecentPeriodDecisionWasKeep` queries against.
   e. Renders a confirmation page ("You're all set — your Sabermatic subscription is still active. Next renewal: {claims.CurrentPeriodEnd}.").
4. The endpoint is mounted publicly (no `RequireAuth`); the token IS the authentication.

### 4.3 Reversal — auto-reverse on activity

While `cancel_at_period_end=true` AND `sub_cancel_is_auto=true`, any authenticated request from the user invalidates the trigger condition. The hook lives in `Backend.AuthenticateSession` alongside the existing `TouchAuthSession` fire-and-forget pattern (`internal/backend/backend.go:280-285`):

```go
// After existing TouchAuthSession goroutine, before returning user:
if user.SubCancelAtPeriodEnd && user.SubCancelIsAuto {
    go func() {
        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer cancel()
        _ = idleunsub.AutoReverse(ctx, user.ID)  // best-effort
    }()
}
```

`AutoReverse`:
1. Re-reads `users.sub_cancel_at_period_end` and `users.sub_cancel_is_auto` (defensive: these may have flipped via webhook between the gate read and goroutine execution). If either is false, no-op.
2. **The current request itself is the activity signal.** We do *not* re-read `MAX(last_active)` — that would race against the in-flight `TouchAuthSession` goroutine, producing a stale read. The gate `SubCancelAtPeriodEnd && SubCancelIsAuto` plus the fact that this request authenticated successfully is sufficient evidence that the user is active in the current period.
3. **Verifies Stripe state** via `subscription.Get`. If Stripe says `cancel_at_period_end` is already `false`, the cache is stale (probably from a prior partial failure where COMMIT succeeded but Stripe `Update` did not). Silently clear the cache flags (`sub_cancel_at_period_end=false`, `sub_cancel_is_auto=false`), log a warning, emit `idleunsub.cancel.cache_drift_corrected`, and return without sending email or inserting `subscription_kept`. No real reversal happened.
4. Otherwise, calls `subscription.Update(id, cancel_at_period_end=false)`. Stripe call is idempotent (setting `false` when already `false` is a no-op, but step 3 already filtered that case). Captures the returned subscription for its `CurrentPeriodStart` and `CurrentPeriodEnd`.
5. Updates `users` cache: `sub_cancel_at_period_end = false`, `sub_cancel_is_auto = false`, `pending_kept_banner = true`, `sub_current_period_start = <from response>`.
6. Sends the confirmation email via `SendEmailJob`, using the returned subscription's `CurrentPeriodEnd` for the renewal date.
7. Inserts a `user_events` row with `event_type='subscription_kept'`, metadata `{subscription_id, via: 'auto_activity', current_period_start: <RFC3339, from response>}`. The `current_period_start` is what `mostRecentPeriodDecisionWasKeep` queries against — without it, multi-firing dedup would wrongly re-cancel after a keep.

The TOCTOU window between step 1 (gate read) and step 4 (Stripe `Update`) is benign: setting `cancel_at_period_end=false` when Stripe says it's already `false` is a no-op, and the post-Get verification in step 3 short-circuits anyway.

The cached `sub_cancel_at_period_end` flag is the gate that prevents Stripe API calls on every authed request — it's `false` for almost every user almost all the time. After AutoReverse flips it, subsequent requests skip the entire path. The `sub_cancel_is_auto` second gate prevents this code from reversing a user's manual portal cancellation.

### 4.4 No reversal (cancel completes)

User does nothing. At period_end, Stripe naturally deletes the subscription. The existing `customer.subscription.deleted` webhook handler runs, resets `users.plan = 'free'`, clears the cache columns (`sub_cancel_at_period_end`, `sub_cancel_is_auto`, `sub_current_period_start`, `stripe_subscription_id`). User retains anything they own (purchased grants, free-tier minutes), loses Pro features.

### 4.5 Banner

The `pending_kept_banner` flag on `users` is set during reversal (link or auto). The frontend reads it from the user-info RPC payload. The banner clears on **dismiss** (explicit user action — close button, a small RPC call sets the flag to `false`), not on render. This way, a user who closes the tab before noticing the banner gets it on the next page load. Auto-dismiss after 14 days as a hygiene measure.

---

## 5. Schema Changes

### Migration 015: `auth_sessions` soft delete

```sql
-- 015_auth_sessions_soft_delete.up.sql
ALTER TABLE auth_sessions
  ADD COLUMN revoked_at TIMESTAMPTZ;

CREATE INDEX idx_auth_sessions_user_last_active
  ON auth_sessions(user_id, last_active DESC);
```

```sql
-- 015_auth_sessions_soft_delete.down.sql
DROP INDEX IF EXISTS idx_auth_sessions_user_last_active;
ALTER TABLE auth_sessions DROP COLUMN revoked_at;
```

The new index is `DESC` on `last_active` so `MAX(last_active) WHERE user_id = $1` is a one-row scan. No `WHERE revoked_at IS NULL` filter — the activity query reads across history.

**Write amplification note:** this index is updated on every `TouchAuthSession` (one per authed request). Cost is one additional B-tree update per request. Acceptable; the same row is hot anyway.

**Retention:** rows are never physically deleted on logout under this migration. Account deletion still hard-deletes (§7.2). Rows are tiny (~100 bytes); we accept unbounded growth for v1 and revisit if it becomes a real cost. A future cleanup job could hard-delete `revoked_at < NOW() - 1 year` while preserving each user's most recent revoked row — out of scope here.

### Migration 016: cache subscription state on users

```sql
-- 016_users_sub_state.up.sql
ALTER TABLE users
  ADD COLUMN stripe_subscription_id     TEXT,
  ADD COLUMN sub_cancel_at_period_end   BOOLEAN     NOT NULL DEFAULT FALSE,
  ADD COLUMN sub_cancel_is_auto         BOOLEAN     NOT NULL DEFAULT FALSE,
  ADD COLUMN sub_current_period_start   TIMESTAMPTZ,
  ADD COLUMN pending_kept_banner        BOOLEAN     NOT NULL DEFAULT FALSE,
  ADD COLUMN idle_eligible_after        TIMESTAMPTZ NOT NULL DEFAULT NOW();
```

| Column | Purpose | Populated by |
|---|---|---|
| `stripe_subscription_id` | The sub ID we need to call `subscription.Update` on | `handleSubscriptionUpdated` (Stripe fires this on subscription create as well as update — see §7.2 note). Cleared on `customer.subscription.deleted`. |
| `sub_cancel_at_period_end` | Hot-path gate for auto-reverse middleware | Synced from `customer.subscription.updated` events; written directly by `idleunsub` cancel/keep paths. |
| `sub_cancel_is_auto` | Distinguishes our auto-cancel from a user's manual portal cancel | Set `true` by `idleunsub.HandleInvoiceUpcoming` only. Set `false` by `KeepSubscription`/`AutoReverse`. **Never** set by webhook sync. |
| `sub_current_period_start` | Cached so we can detect "user came back since the cancel decision" without a Stripe round-trip; also populated by reversal paths from the `subscription.Update` response | Populated by `handleSubscriptionUpdated`, the cancel path (from the fetched-at-eval-time subscription), and the keep/auto-reverse paths (from the `subscription.Update` response). |
| `pending_kept_banner` | "Welcome back" banner state | Set by reversal paths; cleared on banner dismiss; auto-cleared after 14 days by a tiny periodic job. |
| `idle_eligible_after` | Period-grandfathering anchor for launch-day wave protection | Defaults to `NOW()` at migration. Migration applies to existing users → their first eligible period_start is whatever begins after deploy. New users get `NOW()` at signup. |

### Migration 017: webhook event idempotency

```sql
-- 017_stripe_webhook_dedup.up.sql
CREATE TABLE stripe_webhook_dedup (
  event_id      TEXT        PRIMARY KEY,
  event_type    TEXT        NOT NULL,
  processed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

Generic table for webhook retry-storm protection. Other webhook handlers can adopt it incrementally; the existing `grants.stripe_event_id UNIQUE` pattern continues to work for grant creation.

### Migration 018: keep-link token single-use

```sql
-- 018_keep_link_token_uses.up.sql
CREATE TABLE keep_link_token_uses (
  token_hash  BYTEA       PRIMARY KEY,
  user_id     UUID        NOT NULL REFERENCES users(id),
  used_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

`token_hash` is `sha256(raw_token)`; storing the hash, not the token, prevents leak risk if the table is ever exposed.

### sqlc query changes (`sql/queries/auth_sessions.sql`)

| Existing | Change |
|---|---|
| `GetAuthSessionByToken` | Add `AND s.revoked_at IS NULL` to WHERE; project the new `users` cache columns into the returned row. |
| `DeleteAuthSession` (single logout — sole call site is `internal/backend/auth.go:261`) | Rename to `RevokeAuthSession`; becomes `UPDATE auth_sessions SET revoked_at = NOW() WHERE id = $1`. |
| `DeleteUserAuthSessions` (logout-everywhere) | Rename to `RevokeUserAuthSessions`; becomes `UPDATE auth_sessions SET revoked_at = NOW() WHERE user_id = $1 AND revoked_at IS NULL`. |
| `users.sql` `DELETE FROM auth_sessions WHERE user_id = @id` (account deletion) | Stays as hard DELETE, renamed for clarity to `HardDeleteUserAuthSessions`. |

### New sqlc queries

```sql
-- name: GetUserLastActive :one
-- Reads across all history (including revoked sessions) — this is a
-- "when did the user last act, ever?" query, not a session-validity check.
SELECT MAX(last_active)::timestamptz
FROM auth_sessions
WHERE user_id = $1;

-- name: SetUserAutoCancelState :exec
UPDATE users
SET sub_cancel_at_period_end = TRUE,
    sub_cancel_is_auto       = TRUE,
    sub_current_period_start = $2
WHERE id = $1;

-- name: ClearUserAutoCancelState :exec
UPDATE users
SET sub_cancel_at_period_end = FALSE,
    sub_cancel_is_auto       = FALSE,
    pending_kept_banner      = TRUE
WHERE id = $1;

-- name: SyncSubStateFromWebhook :exec
-- Used by handleSubscriptionUpdated. Does NOT touch sub_cancel_is_auto:
-- that flag is only set by our handler, never by webhook sync.
UPDATE users
SET stripe_subscription_id   = $2,
    sub_cancel_at_period_end = $3,
    sub_current_period_start = $4
WHERE id = $1;

-- name: ClearSubStateOnDeletion :exec
-- Used by handleSubscriptionDeleted.
UPDATE users
SET stripe_subscription_id   = NULL,
    sub_cancel_at_period_end = FALSE,
    sub_cancel_is_auto       = FALSE,
    sub_current_period_start = NULL,
    plan                     = 'free'
WHERE id = $1;

-- name: ClearKeptBanner :exec
UPDATE users SET pending_kept_banner = FALSE WHERE id = $1;

-- name: TryClaimWebhookEvent :one
-- Returns the event_id on first claim, no row on subsequent claims.
INSERT INTO stripe_webhook_dedup (event_id, event_type)
VALUES ($1, $2)
ON CONFLICT (event_id) DO NOTHING
RETURNING event_id;

-- name: TryClaimKeepToken :one
INSERT INTO keep_link_token_uses (token_hash, user_id)
VALUES ($1, $2)
ON CONFLICT (token_hash) DO NOTHING
RETURNING token_hash;

-- name: HasAutoCanceledThisPeriod :one
-- Returns true if a subscription_auto_canceled event already exists for this period.
-- metadata->>'current_period_start' is RFC3339 text written by the typed metadata struct.
SELECT EXISTS (
  SELECT 1 FROM user_events
  WHERE user_id = $1
    AND event_type = 'subscription_auto_canceled'
    AND (metadata->>'subscription_id') = $2
    AND (metadata->>'current_period_start')::timestamptz = $3
);

-- name: GetMostRecentKeptOrCanceledForPeriod :one
-- For multi-firing dedup: did a 'subscription_kept' event arrive after the
-- most recent 'subscription_auto_canceled' for this period? If yes, the user
-- already kept their sub for this period; don't re-cancel.
-- Both event types include current_period_start in their metadata (RFC3339 string).
SELECT event_type
FROM user_events
WHERE user_id = $1
  AND event_type IN ('subscription_auto_canceled', 'subscription_kept')
  AND (metadata->>'subscription_id') = $2
  AND (metadata->>'current_period_start')::timestamptz = $3
ORDER BY created_at DESC
LIMIT 1;
```

**Metadata schema** (the canonical Go writer struct, ensuring RFC3339 timestamps):

```go
package idleunsub

// All timestamps are canonicalized to second precision via
// time.Unix(stripeInt64, 0).UTC() before assignment. Stripe period fields
// arrive as int64 Unix seconds (no sub-second component); explicit second
// precision and UTC guard against future drift if a code path ever
// constructs a time.Time from a different source. JSON encoding via
// encoding/json produces RFC3339 ("...Z") which Postgres ::timestamptz
// parses reliably.

type cancelMetadata struct {
    SubscriptionID     string    `json:"subscription_id"`
    StripeEventID      string    `json:"stripe_event_id"`
    CurrentPeriodStart time.Time `json:"current_period_start"`
    CurrentPeriodEnd   time.Time `json:"current_period_end"`
}

type keptMetadata struct {
    SubscriptionID     string    `json:"subscription_id"`
    Via                string    `json:"via"`                  // "link" | "auto_activity"
    CurrentPeriodStart time.Time `json:"current_period_start"` // identifies the period kept;
                                                                // load-bearing for mostRecentPeriodDecisionWasKeep dedup
}
```

---

## 6. Emails

Two messages, both sent via the existing `email.Sender` (Mailgun in prod, log-only in test). Templates live in `internal/feat/idleunsub/templates/`. The phrase "the last two billing periods" is composed at send time so it works for non-monthly intervals (annual: "the last two annual billing periods"; weekly: "the last two weeks").

### 6.1 Cancel decision email

```
Subject: We won't charge you for the next period

Hi {first_name},

We noticed you haven't been around Sabermatic in the last two billing
periods, so we've stopped your auto-renewal. You'll keep access through
{current_period_end} — we won't charge you for the next period.

If you'd like to keep your subscription active, one click does it:

  [Keep my subscription]

If you're done for now, no action needed. We'll be here whenever you
want to come back.

— Sabermatic
```

### 6.2 Subscription kept (confirmation)

```
Subject: Your subscription is still active

Hi {first_name},

You're all set — your Sabermatic subscription will renew normally on
{next_renewal_date}.

Welcome back.

— Sabermatic
```

A "period has ended" email is intentionally out of scope. The existing `customer.subscription.deleted` handler does not need a new message under this feature; if we want one, it lives in the existing subscription-deletion path, not here.

---

## 7. Implementation Surface

### 7.1 Feature package — `internal/feat/idleunsub/`

```
internal/feat/idleunsub/
  idleunsub.go         // public API + decision/action logic
  email.go             // compose the cancel + kept emails
  templates/
    cancel.html.tmpl
    cancel.txt.tmpl
    kept.html.tmpl
    kept.txt.tmpl
  token.go             // HMAC sign/verify for the keep-link
  metrics.go           // OTEL counter wrappers (see §9)
  idleunsub_test.go    // domain tests
```

Public API (Go):

```go
package idleunsub

// HandleInvoiceUpcoming evaluates the trigger and, if it fires,
// sets cancel_at_period_end on Stripe and sends the cancel email.
// Idempotent: safe to call multiple times with the same event.
func (s *Service) HandleInvoiceUpcoming(ctx context.Context, event stripe.Event) error

// KeepSubscription reverses cancel_at_period_end. Called from the
// keep-link endpoint after token verification + single-use claim.
// Refuses to reverse if sub_cancel_is_auto is false (manual cancel).
// Takes the verified token claims (which carry CurrentPeriodEnd for the
// confirmation email) so we don't need an extra Stripe round-trip just
// for the date.
func (s *Service) KeepSubscription(ctx context.Context, claims KeepTokenClaims) error

// AutoReverse reverses cancel_at_period_end when activity is detected
// in the current period. Looks up subscription state from cached users row.
// No-op if cache says cancel is off, or if cancel is not the auto kind.
func (s *Service) AutoReverse(ctx context.Context, userID uuid.UUID) error

// SignKeepToken returns a signed token for embedding in the cancel email.
// Token is a Go struct serialized via canonical JSON (typed fields, fixed
// order); HMAC-SHA256; URL-safe base64.
func (s *Service) SignKeepToken(claims KeepTokenClaims) string

// VerifyKeepToken parses and validates a token from the keep-link URL.
// Does NOT consume single-use; that's the endpoint's responsibility.
func (s *Service) VerifyKeepToken(token string) (KeepTokenClaims, error)

type KeepTokenClaims struct {
    UserID             uuid.UUID `json:"user_id"`
    SubscriptionID     string    `json:"subscription_id"`
    Action             string    `json:"action"`              // "keep_subscription"
    CurrentPeriodEnd   time.Time `json:"current_period_end"`  // RFC3339; for confirmation email + replay page
    IssuedAt           int64     `json:"iat"`                 // Unix seconds (JWT-style)
    ExpiresAt          int64     `json:"exp"`                 // Unix seconds; INVARIANT: == CurrentPeriodEnd.Unix()
}

// Invariant enforced by both Sign and Verify:
//   ExpiresAt == CurrentPeriodEnd.Unix()
// SignKeepToken populates ExpiresAt from CurrentPeriodEnd; VerifyKeepToken
// rejects tokens where the two disagree (struct drift / forged token).
```

`Service` is constructed once at startup with: a Stripe client, the database, the email sender, the HMAC signing key (already used elsewhere — see existing tokens.go), a clock, and an `slog.Logger`.

### 7.2 Integration adapters (live in conventional places)

| File | Change |
|---|---|
| `internal/backend/billing.go` `HandleStripeWebhook` | Add `case "invoice.upcoming"` → delegate to `b.idleunsub.HandleInvoiceUpcoming`. Also handle `customer.subscription.created` (currently unhandled) → route to `handleSubscriptionUpdated` (same body works for both create and update — Stripe sends both events on subscription creation, with identical payload shape). |
| `internal/backend/billing.go` `handleSubscriptionUpdated` | **Extends the existing function body** (currently calls only `UpdatePlanByStripeCustomer`). Add a `SyncSubStateFromWebhook` call that reads `id`, `current_period_start`, and `cancel_at_period_end` from the event's Subscription object and writes them to `users`. This becomes the sole population path for `users.stripe_subscription_id` and `sub_current_period_start` — Stripe fires `customer.subscription.updated` (and `.created`) immediately after checkout completion, on every period renewal, on plan changes, on portal cancel/resume, so this single hook covers all cases. **Does not touch `sub_cancel_is_auto`** (only `idleunsub.HandleInvoiceUpcoming` sets it). The existing `handleCheckoutCompleted` is unchanged — its `mode != "payment"` early return still applies; subscription-mode checkouts populate state via `customer.subscription.created`/`.updated` instead. **Round-trip note:** when our cancel path sets both `sub_cancel_at_period_end=true` and `sub_cancel_is_auto=true`, Stripe then fires `customer.subscription.updated` with `cancel_at_period_end=true` in the payload. `SyncSubStateFromWebhook` overwrites our `sub_cancel_at_period_end` (with the same `true` value) but does *not* touch `sub_cancel_is_auto`, which stays `true`. The flag round-trip is correct by construction. |
| `internal/backend/billing.go` `handleSubscriptionDeleted` | Use `ClearSubStateOnDeletion`. |
| `internal/backend/backend.go` | Wire the `idleunsub.Service` into `Backend` at construction. |
| `internal/handler/keep.go` (new) | `GET /sub/keep` endpoint: verify token, claim single-use, call `idleunsub.KeepSubscription`, render confirmation page. Mounted publicly (no `RequireAuth`). |
| `internal/backend/backend.go` `AuthenticateSession` | After the existing `TouchAuthSession` goroutine, add a second fire-and-forget goroutine that calls `idleunsub.AutoReverse(ctx, user.ID)` iff `user.SubCancelAtPeriodEnd && user.SubCancelIsAuto`. Same 5s timeout pattern. |
| `internal/auth/user_context.go` | Extend `AuthUser` struct with `SubCancelAtPeriodEnd bool`, `SubCancelIsAuto bool`, `PendingKeptBanner bool`. |
| `internal/backend/auth.go:261` (Logout) | Switch from `DeleteAuthSession` to `RevokeAuthSession` (the renamed query). Sole caller. |
| `web/src/components/KeptBanner.tsx` (new) | One-time banner. Reads `pending_kept_banner` from the user-info RPC. Cleared on user dismiss via small ack RPC. Auto-dismisses after 14 days server-side. |
| `internal/rpc/user/server.go` | Include `pending_kept_banner` in the user-info RPC response. Add an `AckKeptBanner` RPC method that calls `ClearKeptBanner`. (ConnectRPC handlers live under `internal/rpc/{service}/` per project convention.) |
| `sql/queries/auth_sessions.sql`, `sql/queries/users.sql` | Updates per §5. |
| `sql/migrations/015_*.sql`, `016_*.sql`, `017_*.sql`, `018_*.sql` | New migrations per §5. |

### 7.3 Email link / token

- Token payload: typed `KeepTokenClaims` struct serialized with `encoding/json` over a fixed field order. Canonical because the struct is the only writer.
- Signing: HMAC-SHA256 with a separate `KEEP_TOKEN_HMAC_KEY` env var, derived at startup. Separating from the session-token signing key prevents cross-purpose token forgery.
- Encoding: URL-safe base64.
- Expiry: `exp = current_period_end`. After period end, the cancel either took effect or was reversed; reusing the token has no effect.
- **Replay protection**: server-side single-use via `keep_link_token_uses` (token-hash PRIMARY KEY). The first click "consumes" the token; subsequent clicks render the same confirmation page without side effects.
- **Leak surface**: tokens land in our own server access logs. Mitigation: the token is single-use, so a logged token cannot be replayed for action. The token does not grant any other access (no session, no API auth) — it specifically and only triggers the keep action for the one subscription it was minted for.

---

## 8. Testing

### 8.1 Domain tests (in `idleunsub_test.go`)

- **Trigger rule** — table-driven cases:
  - `last_active` exactly equals threshold → not idle (`<` is strict).
  - `last_active` one second before threshold → idle, cancel.
  - `last_active` is `NULL` (no rows for user — should be unreachable for a Pro user, but defensive) → no cancel; no evidence to act on.
  - First-period evaluation (user signed up in current period) → threshold predates signup → trivially not idle → no cancel.
  - `cur_period_start < idle_eligible_after` → period grandfathered → no cancel even if idle.
  - Sub status `trialing` / `past_due` / `unpaid` / `incomplete` → early return.
  - Sub already `cancel_at_period_end=true` → early return (no duplicate email).
  - Same `event_id` re-delivered → second call is a no-op (dedup table claim fails).
  - Same period, different event_id (multi-firing) → second call sees prior `subscription_auto_canceled` row → no-op.
  - Same period, prior `subscription_kept` more recent than prior `subscription_auto_canceled` → no-op (don't re-cancel after keep).
- **KeepSubscription**:
  - Refuses to reverse when `sub_cancel_is_auto=false` (manual cancel scenario).
  - Idempotent: token already claimed → no Stripe call, no email.
- **AutoReverse**:
  - Gate: `sub_cancel_at_period_end=false` → no-op.
  - Gate: `sub_cancel_is_auto=false` → no-op (don't reverse manual cancels).
  - Both true → reverses, flips cache, sends email, sets banner flag.
  - **Race-safety:** does not depend on `MAX(last_active)` — the request itself is the activity signal.
- **Token**: tampered token → verify fails. Expired token → verify fails. Single-use enforced.

### 8.2 Integration tests (live DB + stub Stripe)

Following the existing `internal/backendtest/` convention (use `backendtest.SeedUser` and the production `Signup` path; never raw SQL inserts).

- **End-to-end happy path** — seed a Pro user with `last_active` in Feb, fire `invoice.upcoming` for May renewal, assert: Stripe `Update` called with `cancel_at_period_end=true`, `users.sub_cancel_is_auto=true` set, email sent, `user_events` row inserted with `event_type='subscription_auto_canceled'`, `stripe_webhook_dedup` row created.
- **End-to-end reversal via link** — given an auto-canceled sub, hit `GET /sub/keep?t=...` with a valid token, assert reversal + flag flips + confirmation email + `subscription_kept` row + `keep_link_token_uses` row. Re-click → idempotent confirmation page, no second email.
- **End-to-end auto-reverse via activity** — given an auto-canceled sub, simulate an authed request, assert reversal + cache flips + confirmation email + banner flag set.
- **Manual cancel guard** — user manually cancels via portal (simulate via webhook); `sub_cancel_at_period_end=true` but `sub_cancel_is_auto=false`; subsequent authed request → AutoReverse is a no-op; user's choice respected.
- **Idempotent webhook re-delivery** — fire same `invoice.upcoming` event twice → one cancel call, one email, one dedup row.
- **Multi-firing of `invoice.upcoming`** — fire two distinct events for same period → second one is no-op via period dedup.
- **Launch wave** — seed an existing Pro user with `idle_eligible_after = NOW() + 1 month`, fire `invoice.upcoming` with `cur_period_start < idle_eligible_after`, assert no cancel.
- **Token tampering** — modified token → 400 with generic "invalid link" page. No DB or Stripe state change.
- **Token expired** — past-period token (valid signature, expired) → 200 with friendly "your subscription period has already ended" page. No DB or Stripe state change. Distinguished from tampering on purpose.
- **Token signer/verifier invariant** — token where `claims.ExpiresAt != claims.CurrentPeriodEnd.Unix()` → rejected as malformed.
- **Token replay** — valid token used twice → first works, second returns the same confirmation page idempotently with no Stripe call.
- **Concurrent webhook race** — two simultaneous `invoice.upcoming` deliveries for the same user → `SELECT FOR UPDATE` serializes them; one runs to completion, the other waits and then sees the prior decision via `alreadyAutoCanceledThisPeriod`.
- **Cache drift correction** — seed a Pro user with `sub_cancel_at_period_end=true` (cache) but Stripe-side `cancel_at_period_end=false` (simulating partial-failure window) → next authed request triggers AutoReverse → AutoReverse's `subscription.Get` detects drift → silently clears cache, emits `idleunsub.cancel.cache_drift_corrected`, no email, no `subscription_kept` row.

---

## 9. Operational Notes

### Observability

Each decision emits an `slog` record AND an OTEL counter:

| Counter | Tags | When |
|---|---|---|
| `idleunsub.cancel.fired` | `sub_id` | Trigger fires, cancel set on Stripe |
| `idleunsub.cancel.skipped` | `reason ∈ {trialing, past_due, already_canceled, dedup, grandfathered}` | Early-return in handler |
| `idleunsub.reverse.link` | `sub_id` | Reversal via email-link click |
| `idleunsub.reverse.activity` | `sub_id` | Reversal via auto-activity middleware |
| `idleunsub.email.enqueue` | `kind ∈ {cancel, kept}, status ∈ {ok, error}` | Email enqueued (or enqueue itself failed). Delivery success/failure is the email worker's concern, not this counter. |
| `idleunsub.cancel.error` | `reason ∈ {stripe_update_failed, db_commit_failed}` | Stripe `Update` returned error after DB commit, or DB transaction failed mid-cancel. Pages ops. |
| `idleunsub.cancel.cache_drift_corrected` | `sub_id` | AutoReverse detected the local cache disagreed with Stripe (cache said canceled, Stripe said not). Cache silently corrected; no email/event row. Should be near-zero in steady state — sustained nonzero indicates a Stripe-failure pattern worth investigating. |

A dashboard panel for `cancel.fired` minus `reverse.{link,activity}` shows net cancellations per period. Sustained large drift in one direction is worth investigating (regression where `last_active` isn't being updated, misconfigured `idle_eligible_after`, etc.).

### Email deliverability

§4.1 sequences: DB transaction (dedup-claim + user_events + cache) → COMMIT → Stripe call → email enqueue. If the Stripe call succeeds but email enqueue fails, the cancel still applies (Stripe and our cache agree). The user discovers via the in-app banner on next visit, or via Stripe's own renewal-prevention behavior. Email failures are logged and counted via `idleunsub.email.enqueue{status=error}`.

### HMAC key rotation

Rotating `KEEP_TOKEN_HMAC_KEY` invalidates all outstanding keep-link tokens (max age = period length, typically ≤ 30 days from email send for monthly subs). Mitigations, in order of preference:

1. **Don't rotate routinely.** This key has a small surface (only signs keep-link tokens) and is not in the request hot path.
2. **Two-key verification window.** During rotation, accept tokens signed with either the old or new key for one period; retire the old key after.
3. **Accept the breakage.** Users with broken links can still use AutoReverse on next login (they just won't have the email link path during the rotation window).

Default policy for v1: option 1 (no scheduled rotation). Document option 2 as the path forward if a security incident requires rotation.

### Backfill

- `auth_sessions.revoked_at` defaults to NULL — no backfill.
- `users.idle_eligible_after` defaults to `NOW()` at migration — every existing Pro user is grandfathered for at least their current period (and for their prior period, since `cur_period_start < idle_eligible_after` until the next period rolls over).
- `users.stripe_subscription_id` will be NULL for existing users until their next `customer.subscription.updated` webhook fires (which Stripe re-fires on any subscription state change). For users who don't change anything, the field stays NULL and AutoReverse is a no-op via its existing gates. Optional one-time backfill: query Stripe for every user with `stripe_customer_id IS NOT NULL` and a current sub, populate the new field. Non-blocking; can ship without.

### Rollback

- Migration down-scripts restore prior state. Dropping `auth_sessions.revoked_at` reverts the table; the only data loss is "knowing when sessions were revoked."
- The feature can be turned off without rolling back schema by removing `case "invoice.upcoming"` from `HandleStripeWebhook`. The package stays in place dormant.
- If a bad cancel slips through, `KeepSubscription` is the per-user remedy; for a wider issue, a one-shot ops script can scan `user_events` for `subscription_auto_canceled` rows in a time window and call `subscription.Update(cancel_at_period_end=false)` for each.

### Launch wave protection

The `idle_eligible_after` column (default `NOW()` at migration) ensures no existing Pro user is canceled on day 1 of deploy. Their first eligible period is the first one whose `current_period_start ≥ idle_eligible_after`. For monthly subs, this means earliest cancel for an existing user is ~60 days post-deploy (if they were already idle and continue to be).

---

## 10. Out of Scope / Open Questions

- **Multiple subscriptions per user.** Drill assumes one. If a user ever has multiple, this feature needs revisiting.
- **Annual subscriptions.** Rule generalizes — "previous period start" is just `current_period_start − 1 interval`. Annual means a user signing up and going idle wouldn't be canceled until ~24 months later. Probably correct (annual buyers are presumably committed); revisit if Sabermatic ever offers annual.
- **Toggle later?** If we ever discover users want to opt *out* of this protection (e.g., enterprise customers with shared seats where idleness doesn't reflect intent), we revisit. Default-on holds until then.
- **Spanda portability.** The *tenet* is portable; the *code* is not — each Spanda product implements its own version against its own data model. A future cross-product library might extract the trigger evaluator if it pays for itself.
- **Cancel-window UI surface beyond the banner.** Should the billing page show "your subscription will end on {date} unless you log in or click here"? Current spec says no; easy add-on if we want it.
- **Package name.** Round 1 review: both Opus and Sonnet flagged `idleunsub` as awkward (reads like newsletter-unsub). Author chose it explicitly. Open: rename to `idlecancel` or `autocancel` later if the awkwardness compounds during implementation.
- **Keep semantics for future periods.** A click on the email keep-link reverses *this* period's cancel but does not update `last_active`. If the user clicks keep but then never logs in, the rule re-fires next period (after another full idle period N+1 passes). v1 treats this as correct: clicking keep is "I want this period," not "I want to keep paying forever even without using." Revisit if real users hit a re-cancel loop.
- **Auto-prune of `auth_sessions`.** Soft-delete leaves rows forever. v1 accepts the bloat; revisit when storage cost becomes real.
- **Auto-prune of `keep_link_token_uses`.** Rows are only created on first click. After `exp = period_end` passes, the token is expired regardless and the row's no-replay protection becomes moot. A periodic `DELETE FROM keep_link_token_uses WHERE used_at < NOW() - INTERVAL '60 days'` keeps the table trim. Boy-scout cleanup; not blocking.
- **Generic webhook dedup adoption.** `stripe_webhook_dedup` is generic; other handlers (`invoice.paid`, `customer.subscription.*`) could adopt it for consistency. Out of scope for this feature.

---

## 11. Glossary

- **Period N** (a.k.a. the *current period*): the billing period the user is currently in (read from `SubscriptionItem.CurrentPeriodStart` / `.CurrentPeriodEnd` in stripe-go v82).
- **Period N−1**: the previous period (`current_period_start − 1 interval` to `current_period_start`).
- **Threshold**: `current_period_start − 1 interval` — the moment before which `last_active` must fall for the trigger to fire.
- **invoice.upcoming**: Stripe webhook fired ~7 days before each renewal (default; configurable per Stripe account).
- **`cancel_at_period_end`**: Stripe subscription flag. When `true`, Stripe completes the current paid period and then deletes the subscription instead of renewing. Reversible until period ends.
- **`sub_cancel_is_auto`**: our boolean distinguishing an auto-cancel set by this feature from a manual cancel set by the user via Stripe portal. Only the former is reversible by AutoReverse.
- **Keep-link**: signed-token URL embedded in the cancel email; clicking it reverses `cancel_at_period_end`. Single-use enforced server-side.
- **`idle_eligible_after`**: per-user timestamp before which `current_period_start` values are grandfathered (no cancel). Used to prevent a wave of cancellations at deploy time.
