# Stripe Listen Ergonomics — Design

Date: 2026-04-12

## Goal

Make local Stripe webhook testing frictionless:

1. `stripe listen` runs as part of `make dev` — always forwarding, no extra terminal.
2. The signing secret stays in sync with the server's `STRIPE_WEBHOOK_SECRET` by default, pinned once via a setup target.
3. Each already-handled webhook branch that can be driven through the CLI has a one-line scenario that exercises it with real DB state, so the happy path (not the no-op branch) executes.
4. Idempotency is verifiable with one command.

## Non-goals

- Automated handling of `charge.refunded`, disputes, or `invoice.payment_failed`. Deferred until real volume justifies the handler work — a separate spec when the data exists.
- Offline fixture signing. Duplicates `internal/backend/billing_test.go` which already constructs `stripe.Event` values directly.
- Integration tests driven by `stripe listen`. Unit tests at the handler/backend layer are the right coverage; the scenarios in this spec are for interactive local verification.
- Supporting multiple concurrent developers on the same Stripe test account. One dev = one `stripe listen` session. Documented caveat.
- A scenario for `handleSubscriptionUpdated`'s `past_due` branch. That branch produces the same DB mutation (`UpdatePlanByStripeCustomer → 'free'`) as `handleSubscriptionDeleted`, which `sub-cancel` already exercises. If the two branches ever diverge (e.g., `past_due` becomes a grace period instead of an immediate downgrade), add a scenario then — likely using Stripe Test Clocks.

## Components

### 1. Procfile.dev entry

Add a third process:

```
stripe: stripe listen --forward-to localhost:8080/api/webhooks/stripe
```

The webhook endpoint is registered by `PostStripeWebhook` in `internal/handler/routes.go`. The handler verifies signatures using `cfg.Stripe.WebhookSecret`; as long as that matches the CLI's session secret, events flow through. API-version drift between the CLI's fixtures and the stripe-go SDK version is handled by `webhook.ConstructEventWithOptions(..., IgnoreAPIVersionMismatch: true)` in `internal/handler/billing.go`.

**Failure modes are acceptable:**
- CLI not installed / not logged in: the `stripe` process exits non-zero at startup and appears failed in overmind. Other processes keep running.
- Stripe account reauthorization needed: same — visible, loud, doesn't break dev.
- Startup race: `stripe listen` establishes its websocket before `air` finishes rebuilding. A scenario invoked during a rebuild gets a connection-refused delivery; the event stays in Stripe and is recoverable via `make stripe-resend EVENT=<id>`.

No `--skip-verify` needed (localhost HTTP, not TLS).

### 2. One-time secret pinning: `scripts/stripe-setup.sh` + `make stripe-setup`

`stripe listen --print-secret` returns the device-local session `whsec_...`, which is stable per CLI install until `stripe logout`. Fetch once, write to `.env`, forget. Re-run after `stripe logout && stripe login`.

```bash
#!/usr/bin/env bash
# scripts/stripe-setup.sh
set -euo pipefail

if ! stripe config --list >/dev/null 2>&1; then
    stripe login
fi

secret="$(stripe listen --print-secret)"
if [[ -z "$secret" ]]; then
    echo "stripe listen --print-secret returned empty" >&2
    exit 1
fi

# Ensure .env ends with a newline before appending.
if [[ -s .env ]] && [[ "$(tail -c1 .env | od -An -c)" != *"\n"* ]]; then
    printf '\n' >> .env
fi

# Upsert STRIPE_WEBHOOK_SECRET; preserve other keys.
if grep -q '^STRIPE_WEBHOOK_SECRET=' .env 2>/dev/null; then
    # `-i.bak` form works on both BSD (macOS) and GNU sed.
    sed -i.bak "s|^STRIPE_WEBHOOK_SECRET=.*|STRIPE_WEBHOOK_SECRET=${secret}|" .env
    rm -f .env.bak
else
    echo "STRIPE_WEBHOOK_SECRET=${secret}" >> .env
fi

echo "STRIPE_WEBHOOK_SECRET written to .env"
```

Makefile target:
```
stripe-setup:
	./scripts/stripe-setup.sh
```

Documented in `.env.example` with a comment directing new contributors to `make stripe-setup`.

### 3. Scenario runner: `cmd/stripescenario`

A single Go binary with subcommands, using `github.com/urfave/cli/v3` to match `cmd/drillctl` (already a go.mod dependency).

**Layout:**

```
cmd/stripescenario/
    main.go         # urfave/cli root command + subcommand registration
    runner.go       # Runner struct, newRunner (safety assert + deps), shared helpers
    packbuy.go      # (r *Runner) packBuy(ctx, userID, minutes)
    substart.go     # (r *Runner) subStart(ctx, userID)
    subcancel.go    # (r *Runner) subCancel(ctx, userID)
    resend.go       # (r *Runner) resend(ctx, eventID)
```

**`Runner` holds:**
- `*pgxpool.Pool` for DB reads/writes.
- Stripe SDK API key set as a package-level side-effect (`stripe.Key` from `github.com/stripe/stripe-go/v82`).

**Config loading.** Read `STRIPE_SECRET_KEY`, `DATABASE_URL`, and `STRIPE_PRO_PRICE_ID` directly via `os.Getenv` after `godotenv.Load()`. Do **not** call `config.Load()` — it requires unrelated fields (`ANTHROPIC_API_KEY`, `GOOGLE_CLOUD_PROJECT`, `AUTH_TOKEN_SECRET`, etc.) that a standalone dev tool shouldn't need. This matches the `drillctl` pattern (`cmd/drillctl/main.go`).

**Constructor (sketch):**
```go
func newRunner(ctx context.Context) (*Runner, error) {
    key := os.Getenv("STRIPE_SECRET_KEY")
    if !strings.HasPrefix(key, "sk_test_") {
        return nil, fmt.Errorf("refusing to run: STRIPE_SECRET_KEY missing or not a test key")
    }
    dbURL := os.Getenv("DATABASE_URL")
    if dbURL == "" {
        return nil, fmt.Errorf("DATABASE_URL not set")
    }
    pool, err := pgxpool.New(ctx, dbURL)
    if err != nil {
        return nil, fmt.Errorf("db pool: %w", err)
    }
    stripe.Key = key
    return &Runner{pool: pool}, nil
}
```

**Subcommand behaviors:**

| Subcommand | Mechanism | Handler branch exercised |
|---|---|---|
| `pack-buy --user <uuid> [--minutes 120]` | `stripe trigger checkout.session.completed --add checkout_session:metadata.user_id=<uuid> --add checkout_session:metadata.pack_minutes=<n>`. Validate minutes via `billing.ValidPackSize` before triggering to match the production checkout path. | `handleCheckoutCompleted` → `CreatePurchaseGrant` |
| `sub-start --user <uuid>` | **SDK-driven**, not `stripe trigger`. See below. | `handleInvoicePaid` → `CreateSubscriptionGrant` (and `handleSubscriptionUpdated` on activation) |
| `sub-cancel --user <uuid>` | **SDK-driven**, not `stripe trigger`. See below. Requires `sub-start` to have run first. | `handleSubscriptionDeleted` → `UpdatePlanByStripeCustomer("free")` |
| `resend <event_id>` | `stripe events resend "<event_id>"`. Used to verify idempotency. | Whichever handler the original event matched. |

**Why SDK-driven for subscriptions.** `stripe trigger invoice.paid` and `stripe trigger customer.subscription.*` are fixture pipelines that pre-create their own `Customer`/`Subscription` objects. Even with `--override subscription:customer=<cus_id>`, downstream pipeline steps (e.g., `invoiceitem` creation) reference the fixture's internally-created customer, so the emitted event's `customer` does not match the one we wrote into `users.stripe_customer_id`. The `handleInvoicePaid` lookup would hit `n==0`. Real SDK calls produce real webhooks with the correct customer.

**`sub-start` SDK flow** (Stripe SDK packages: `customer`, `paymentmethod`, and `subscription` — the last typically imported as `sub`):
1. `customer.New` with optional email metadata for traceability.
2. Attach `pm_card_visa` — a stable test PaymentMethod ID, usable only in Stripe test mode (gated by the `sk_test_` assertion in `newRunner`). Set it as the customer's default via `customer.Update` with `InvoiceSettings.DefaultPaymentMethod`.
3. Persist the customer link via `db.New(r.pool).UpdateUserStripeCustomerID(ctx, db.UpdateUserStripeCustomerIDParams{ID: userID, StripeCustomerID: pgtype.Text{String: cust.ID, Valid: true}})`. This is the existing sqlc-generated query (`sql/queries/users.sql`), not raw SQL — matches production parity. The query returns no error on zero rows affected, so follow up with a `SELECT 1 FROM users WHERE id = $1` existence check before the UPDATE (or fold the existence check into a new sqlc query if the implementer prefers).
4. `sub.New` with `Customer = cust.ID` and `Items = [{Price: STRIPE_PRO_PRICE_ID}]`. Default `collection_method=charge_automatically` + `pm_card_visa` causes Stripe to create and charge the invoice synchronously in test mode, firing `invoice.paid`.

If the user already has a `stripe_customer_id`, overwrite. The prior customer is orphaned in the Stripe test account — acceptable (test mode, not prod). Any active subscription on the prior customer will keep billing against its own payment method; running `sub-cancel` only cancels the subscription on the current `stripe_customer_id`.

**`sub-cancel` SDK flow:**
1. Read `users.stripe_customer_id`; error if null ("run sub-start first").
2. `sub.List` filtered by `Customer = custID` with no `Status` filter — per stripe-go `SubscriptionListParams.Status`, omitting the filter returns all non-canceled subscriptions, which covers `active`, `trialing`, `past_due`, etc. Filtering on just `"active"` would silently miss the others.
3. If the list is empty, print `no active subscriptions for user <id>` to stderr and exit 0 — the dev isn't left staring at logs for an event that will never fire.
4. Otherwise, for each listed subscription, `sub.Cancel(subID, nil)` with default immediate cancellation. Emits `customer.subscription.deleted`.

**Orphan leak in `sub-start`:** any failure after step 1 (customer already exists in Stripe but the flow didn't complete) leaves an orphaned `Customer`. Step 2 failure → customer without a payment method. Step 3 failure → customer + PM but no DB link (subsequent scenarios can't find them). Step 4 failure → customer + PM + DB link but no subscription; rerunning `sub-start` overwrites cleanly. Acceptable in dev tooling — test mode customers are free and listable via `stripe customers list`. Not something the tool handles.

**Shared helpers in `runner.go`:**
- `(r *Runner) lookupStripeCustomer(ctx, userID) (string, error)` — reads `users.stripe_customer_id`, errors cleanly if null or user missing.
- `(r *Runner) runStripeCLI(args ...string) error` — `exec.Command("stripe", args...)` with stdout/stderr wired to the runner's streams, non-zero exit → Go error.

**Error philosophy:** dev tool. Errors surface with clear context and non-zero exit. No retries. `slog` for diagnostic logs, plain `fmt.Fprintf(os.Stderr, ...)` for user-facing messages.

### 4. Make target surface

```makefile
# U is the target user's UUID. Short name chosen to avoid colliding with
# the shell's auto-exported USER env var (which Make inherits as a default).
stripe-setup:
	./scripts/stripe-setup.sh

stripe-pack-buy:
	go run ./cmd/stripescenario pack-buy --user "$(U)" $(if $(MINUTES),--minutes $(MINUTES),)

stripe-sub-start:
	go run ./cmd/stripescenario sub-start --user "$(U)"

stripe-sub-cancel:
	go run ./cmd/stripescenario sub-cancel --user "$(U)"

stripe-resend:
	go run ./cmd/stripescenario resend "$(EVENT)"

# Ad-hoc passthrough for exploring unhandled event types.
stripe-trigger:
	stripe trigger "$(TYPE)"
```

Usage:
```
make stripe-setup                              # one-time
make stripe-pack-buy U=<uuid> MINUTES=300      # exercises pack purchase
make stripe-sub-start U=<uuid>                 # creates sub, fires invoice.paid
make stripe-sub-cancel U=<uuid>                # cancels active sub, fires subscription.deleted
make stripe-resend EVENT=evt_1ABC...           # idempotency check
make stripe-trigger TYPE=charge.refunded       # ad-hoc event
```

### 5. Safety rails

- **Test-mode assertion** in `newRunner`. Refuses to run unless `STRIPE_SECRET_KEY` starts with `sk_test_`.
- **No prod DB access path.** Runner uses `DATABASE_URL` from `.env`; same file the dev server uses. Pointing at prod is explicit misconfiguration.
- **Narrow DB writes.** Only `sub-start` writes to the DB (`UPDATE users.stripe_customer_id`). `pack-buy`, `sub-cancel`, and `resend` are read-only at the scenario layer — any DB mutations they cause happen through the real webhook handler, same as production.

## Data flow (example: `pack-buy`)

```
$ make stripe-pack-buy U=9f1a... MINUTES=120
  │
  ├─ cmd/stripescenario pack-buy --user 9f1a... --minutes 120
  │    ├─ validate minutes via billing.ValidPackSize
  │    └─ exec: stripe trigger checkout.session.completed
  │              --add checkout_session:metadata.user_id=9f1a...
  │              --add checkout_session:metadata.pack_minutes=120
  │       (--add, not --override: the checkout_session fixture does not
  │        pre-populate metadata, so we're adding, not replacing.)
  │
  ├─ Stripe API receives trigger → emits checkout.session.completed event
  │
  ├─ `stripe listen` (running under overmind) receives event, signs with
  │    session whsec, POSTs to localhost:8080/api/webhooks/stripe
  │
  ├─ PostStripeWebhook → verify signature → b.HandleStripeWebhook
  │    → handleCheckoutCompleted → CreatePurchaseGrant
  │
  └─ DB: new row in grants (source=purchase, 120 min), ledger_entries (purchase, +120)
```

## Out of scope (deferred to future specs)

- **`charge.refunded` handler.** Needs partial-refund semantics, pro-rata math, schema additions (`payment_intent_id`, `initial_charge_amount_cents`, ledger `stripe_event_id`). Spec when refund volume justifies the investment.
- **Dispute handling (`charge.dispute.created`, `charge.dispute.closed`).** Needs ledger-based hold/reversal state machine. Spec alongside refunds.
- **`invoice.payment_failed` handler.** Already covered by `subscription.updated → past_due`. Only worth a dedicated handler for first-failure user notification, which is a product decision.
- **Scenarios for unhandled events.** Principle: a scenario exists only when (a) the handler has a case for the event AND (b) reaching that case's non-trivial path requires setup beyond `stripe trigger`. When refund/dispute handling ships, add `refund-pack`, `dispute-open`, etc. alongside.
- **Known latent handler gap — `UpdatePlanByStripeCustomer` is not dedup-guarded.** `handleSubscriptionDeleted` and `handleSubscriptionUpdated` issue blind `UPDATE users SET plan=...` with no `stripe_event_id` check. Resending a `.deleted` event after a user has re-subscribed would incorrectly flip them back to `free`. This is the reason the `resend` acceptance criterion below is narrowed to purchase/invoice events only. A fix (adding per-user-event dedup — likely via an event log or a `last_applied_event_id` column) belongs in a separate spec.

## Open migration / backfill considerations

None. No schema changes. Pure additive tooling.

## Testing

- Manual verification per scenario: run it, observe DB state and handler logs (`slog.Info("purchase grant created", ...)`).
- `go build ./cmd/stripescenario` runs in `make test` via existing `go build ./...`.
- No new Go unit tests — scenarios are thin wrappers over `stripe trigger` and Stripe SDK calls; tests would stub the one thing the scenarios exist to exercise.

## Acceptance

- `make dev` starts vite + air + `stripe listen` together; webhook events delivered to local server without manual CLI invocation.
- `make stripe-setup` produces a working `.env` entry on a fresh `.env` and on one already containing `STRIPE_WEBHOOK_SECRET`. Works whether or not `.env` ends with a trailing newline. Subsequent `stripe listen` sessions verify signatures cleanly.
- Each of the three scenarios produces the expected DB mutation — new grant row for `pack-buy` and `sub-start`, plan flip to `free` for `sub-cancel` — observable via `psql`.
- `make stripe-resend EVENT=<id>` on a `checkout.session.completed` or `invoice.paid` event is a no-op (dedup via the `grants.stripe_event_id` unique constraint). Resending `customer.subscription.*` events is NOT no-op; documented as the known handler gap above.
- Attempting to run any scenario with `sk_live_...` configured fails loudly before any Stripe API call or DB connection.
