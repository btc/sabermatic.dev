# Coverage Risk Report

**Date:** 2026-04-04
**Total cross-package coverage:** 63.8%
**Method:** `make cover` (cross-package `-coverpkg=./internal/...`)

## Top Risks

Ranked by likelihood of a silent bug (the cancel-session pattern: wrong value in a string literal or SQL, no test asserts the result, bug ships undetected).

### 1. Stripe webhook handlers — 0% coverage

**Files:** `backend/billing.go:235-420`, `handler/billing.go:93` (4.3%)

| Function | Coverage |
|----------|----------|
| `HandleStripeWebhook` | 0% |
| `handleCheckoutCompleted` | 0% |
| `handleInvoicePaid` | 0% |
| `handleSubscriptionDeleted` | 0% |
| `handleSubscriptionUpdated` | 0% |
| `PostStripeWebhook` (handler) | 4.3% |

**Why this is #1:** These functions handle real money. `handleCheckoutCompleted` parses metadata from Stripe events and creates purchase grants. `handleInvoicePaid` creates subscription grants. `handleSubscriptionDeleted` downgrades a user's plan. Any of these could have the same class of bug we just fixed — a wrong string literal, a swapped field, a missing nil check — and we'd never know until a customer reports it.

**Concrete risk examples:**
- `handleCheckoutCompleted` reads `metadata.pack_minutes` and calls `CreatePurchaseGrant`. If the metadata key name changes or the minutes are parsed wrong, users pay but get no minutes.
- `handleSubscriptionDeleted` sets `plan = "free"`. If it set `plan = "pro"` (same class of bug as cancel-session), deleted subscriptions would stay pro forever.
- `handleInvoicePaid` uses `CreateSubscriptionGrant` with idempotency check. If the idempotency logic is wrong, users could get double-granted on retry.

**Testability:** High. These functions take a `stripe.Event` struct — no live Stripe connection needed. Build the event, call the function, assert DB state. Same pattern as our `TestCancelSession_SetsStatusCancelled`.

---

### 2. Coach and educator backend — 0% coverage

**Files:** `backend/coach.go`, `backend/educator.go`, `handler/coach.go`, `handler/educator.go`

| Function | Coverage |
|----------|----------|
| `GetLatestCoachAnalysis` | 0% |
| `RequestCoachAnalysis` | 0% |
| `GetEducatorAnalysis` | 0% |
| `RequestEducatorAnalysis` | 0% |
| Handler: `GetCoachAnalysis` | 8.3% |
| Handler: `RequestCoachAnalysis` | 9.1% |
| Handler: `GetEducatorAnalysis` | 6.7% |
| Handler: `RequestEducatorAnalysis` | 5.9% |

**Why this is #2:** These are user-facing features. `RequestCoachAnalysis` enqueues a job and has deduplication logic (`uuidSlicesEqual`, also 0%). `RequestEducatorAnalysis` has entitlement gating (free vs pro access levels). If the entitlement check is wrong, free users could access pro features or vice versa.

**Concrete risk examples:**
- `RequestCoachAnalysis` checks if reviewed session IDs changed before re-running. If `uuidSlicesEqual` has a bug, coach analysis either never refreshes or re-runs every time.
- `RequestEducatorAnalysis` calls `EducatorAccessLevel` to determine free/pro access. The entitlement function is tested, but the integration (backend reading from DB, checking level, gating response) is not.

---

### 3. Job workers — 0% coverage

**Files:** `jobs/coach.go`, `jobs/educator.go`

| Function | Coverage |
|----------|----------|
| `RunCoachAnalysisWorker.Work` | 0% |
| `GenerateEducatorContentWorker.Work` | 0% |

**Why this is #3:** These are the async workers that actually generate coach and educator content. They orchestrate LLM calls, parse results, and write to DB. The evaluate worker (`jobs/evaluate.go`) has partial coverage via integration tests, but coach and educator workers have none. A bug here means the feature silently fails after the user clicks "generate."

Note: The evaluate worker's `Work` method has coverage through the WS integration tests (cross-package coverage captures this), but `coach.Work` and `educator.Work` are completely dark.

---

### 4. Conductor.cancelSession — 0% (just fixed blind)

**File:** `interview/conductor.go:538`

We just fixed a bug in the cancel path and added backend-level tests. But the conductor-level `cancelSession` method still has 0% coverage in the WS integration tests. No test sends `cancel_session` over the WebSocket. This means:
- State machine transitions during cancel aren't tested end-to-end
- The `msgSessionEnded("cancelled")` message sent to the client isn't verified
- A regression in the conductor wiring (e.g., someone changes the message type dispatch) won't be caught

Compare: `endSession` has 66.7% coverage because the WS tests exercise it.

---

### 5. Billing handler/checkout flow — 4-9% coverage

**Files:** `handler/billing.go`

| Function | Coverage |
|----------|----------|
| `PostCheckout` | 8.3% |
| `PostPortal` | 9.1% |
| `GetUsage` | 62.5% |

The checkout handler parses a JSON body, validates pack sizes, and calls `CreateCheckoutSession`. The only coverage comes from route registration (the handler function reference is touched but never called with a real request). If the request parsing, validation, or Stripe session creation has a bug, no test catches it.

---

### 6. Password reset flow — 41.7% handler coverage

**Files:** `handler/auth.go:174` (`ResetPassword`), `handler/auth.go:129` (`VerifyEmail` at 45.5%)

The backend methods are well-covered (83-90%), but the HTTP handlers that parse tokens from query params, validate them, and call the backend are under 50%. A bug in token extraction or error response formatting would go unnoticed.

---

## Coverage by package (cross-package)

| Package | Coverage | Notes |
|---------|----------|-------|
| `jobs` | 9.3% | Evaluate worker covered; coach + educator workers dark |
| `cmd/drill` | 6.6% | Main entrypoint, mostly startup |
| `interview` | 2.2% | Conductor.Run 65% (WS tests), but per-package is low |
| `handler` | (mixed) | Session/auth handlers 65-93%; billing/coach/educator 4-9% |
| `backend` | (mixed) | Auth/session 78-90%; billing/coach/educator 0% |
| `ai` | 3.2% | STT/TTS 0%; LLM client 80-100% via mocks |
| `auth` | 2.1% | Middleware 100%; passwords/sessions 75-100% |
| `billing` | 0.6% | Entitlement checks 100%; plan helpers 100%; getters 0% |
| `config` | 0.6% | Load 100%; Stripe config helpers 0% |

## Pattern

The cancel-session bug was a **string literal in SQL** with no test asserting the resulting state. The same pattern exists in:

1. **Stripe webhook handlers** — string matching on event types, metadata keys, plan names
2. **Subscription management** — `plan = "free"` / `plan = "pro"` in SQL updates
3. **Job workers** — status transitions written to DB without assertion

These are all "set a value, trust it's right" code paths with zero verification.

## Recommendation

Priority order for new tests:

1. **Stripe webhooks** — highest blast radius (money), easiest to test (struct in, DB assert out)
2. **WS cancel_session** — we fixed the bug but the integration path is still dark
3. **Coach/educator request + job workers** — user-facing features with entitlement gating
4. **Password reset/verify handlers** — security-sensitive, partially covered
