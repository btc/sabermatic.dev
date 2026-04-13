# Stripe Listen Ergonomics — Design

Date: 2026-04-12

## Goal

Make local Stripe webhook testing frictionless:

1. `stripe listen` runs as part of `make dev` — always forwarding, no extra terminal.
2. The signing secret stays in sync with the server's `STRIPE_WEBHOOK_SECRET` by default, pinned once via a setup target.
3. Each already-handled webhook branch has a one-line scenario that exercises it with real DB state, so the happy path (not the no-op branch) executes.
4. Idempotency is verifiable with one command.

## Non-goals

- Automated handling of `charge.refunded`, disputes, or `invoice.payment_failed`. Deferred until real volume justifies the handler work — a separate spec when the data exists.
- Offline fixture signing. Duplicates `internal/backend/billing_test.go` which already constructs `stripe.Event` values directly.
- Integration tests driven by `stripe listen`. Unit tests at the handler/backend layer are the right coverage; the scenarios in this spec are for interactive local verification.
- Supporting multiple concurrent developers on the same Stripe test account. One dev = one `stripe listen` session. Documented caveat.

## Components

### 1. Procfile.dev entry

Add a third process:

```
stripe: stripe listen --forward-to localhost:8080/api/webhooks/stripe
```

The webhook endpoint is `POST /api/webhooks/stripe` (see `internal/handler/routes.go:83`). The handler verifies signatures using `cfg.Stripe.WebhookSecret`; as long as that matches the CLI's session secret, events flow through.

**Failure modes are acceptable:**
- CLI not installed / not logged in: the `stripe` process exits non-zero at startup and appears failed in overmind. Other processes keep running.
- Stripe account reauthorization needed: same — visible, loud, doesn't break dev.

No `--skip-verify` needed (localhost HTTP, not TLS).

### 2. One-time secret pinning: `scripts/stripe-setup.sh` + `make stripe-setup`

`stripe listen --print-secret` returns the device-local session `whsec_...`, which is stable per CLI install until `stripe logout`. Fetch once, write to `.env`, forget.

```bash
# scripts/stripe-setup.sh (sketch)
set -euo pipefail
if ! stripe config --list >/dev/null 2>&1; then
    stripe login
fi
secret="$(stripe listen --print-secret)"
# Upsert STRIPE_WEBHOOK_SECRET in .env, preserving other keys.
if grep -q '^STRIPE_WEBHOOK_SECRET=' .env; then
    sed -i.bak "s|^STRIPE_WEBHOOK_SECRET=.*|STRIPE_WEBHOOK_SECRET=${secret}|" .env
else
    echo "STRIPE_WEBHOOK_SECRET=${secret}" >> .env
fi
rm -f .env.bak
echo "STRIPE_WEBHOOK_SECRET written to .env"
```

Makefile target:
```
stripe-setup:
	./scripts/stripe-setup.sh
```

Documented in `.env.example` with a comment directing new contributors to `make stripe-setup`.

### 3. Scenario runner: `cmd/stripescenario`

A single Go binary with subcommands. Dispatch via `os.Args[1]` + `flag.NewFlagSet` per subcommand (no new deps — codebase doesn't use cobra).

**Layout:**

```
cmd/stripescenario/
    main.go         # subcommand dispatch, usage
    runner.go       # Runner struct, NewRunner (asserts test mode), shared helpers
    packbuy.go      # (r *Runner) PackBuy(ctx, userID, minutes)
    substart.go     # (r *Runner) SubStart(ctx, userID)
    subpastdue.go   # (r *Runner) SubPastDue(ctx, userID)
    subcancel.go    # (r *Runner) SubCancel(ctx, userID)
    resend.go       # (r *Runner) Resend(ctx, eventID)
```

**`Runner` holds:**
- `*config.Config` — loads from `.env` same as the server (reuses `internal/config`).
- `*pgxpool.Pool` — for DB reads/writes in `sub-start` (customer link).
- Stripe SDK package-level `stripe.Key` set via `cfg.Stripe.Init()` — matches server init path.

**Constructor safety assert:**
```go
func NewRunner(ctx context.Context) (*Runner, error) {
    cfg, err := config.Load()
    if err != nil { return nil, err }
    if !strings.HasPrefix(cfg.Stripe.SecretKey, "sk_test_") {
        return nil, fmt.Errorf("refusing to run: STRIPE_SECRET_KEY is not a test key")
    }
    cfg.Stripe.Init()
    // ... pgxpool.New(ctx, cfg.Database.URL)
    return &Runner{cfg: cfg, pool: pool}, nil
}
```

**Subcommand behaviors:**

| Subcommand | Action | Handler branch exercised |
|---|---|---|
| `pack-buy --user <uuid> [--minutes 120]` | Shells `stripe trigger checkout.session.completed --add checkout_session:metadata.user_id=<uuid> --add checkout_session:metadata.pack_minutes=<n>` | `handleCheckoutCompleted` → `CreatePurchaseGrant` |
| `sub-start --user <uuid>` | (1) `stripe.Customer.New` via API. (2) `UPDATE users SET stripe_customer_id=? WHERE id=?`. (3) Shell `stripe trigger invoice.paid --add invoice:customer=<cus_id>`. | `handleInvoicePaid` → `CreateSubscriptionGrant` |
| `sub-past-due --user <uuid>` | Looks up `stripe_customer_id` from users table; errors if null ("run sub-start first"). Shells `stripe trigger customer.subscription.updated --add subscription:customer=<cus_id> --add subscription:status=past_due`. | `handleSubscriptionUpdated` → plan flipped to `free` |
| `sub-cancel --user <uuid>` | Looks up `stripe_customer_id`. Shells `stripe trigger customer.subscription.deleted --add subscription:customer=<cus_id>`. | `handleSubscriptionDeleted` → `UpdatePlanByStripeCustomer("free")` |
| `resend <event_id>` | Shells `stripe events resend <event_id>`. | Whichever handler matches; used to verify `stripe_event_id` unique dedup. |

**Shared helpers in `runner.go`:**
- `(r *Runner) lookupStripeCustomer(ctx, userID) (string, error)` — reads `users.stripe_customer_id`, errors cleanly if null.
- `(r *Runner) runStripe(args ...string) error` — `exec.Command("stripe", args...)` with stdout/stderr piped, error if non-zero.

**Error philosophy:** dev tool. Errors surface clearly with context. No retries.

### 4. Make target surface

```makefile
# U is the target user's UUID. Short name chosen to avoid colliding with
# the shell's auto-exported USER env var (which Make inherits as a default).
stripe-setup:
	./scripts/stripe-setup.sh

stripe-pack-buy:
	go run ./cmd/stripescenario pack-buy --user $(U) $(if $(MINUTES),--minutes $(MINUTES),)

stripe-sub-start:
	go run ./cmd/stripescenario sub-start --user $(U)

stripe-sub-past-due:
	go run ./cmd/stripescenario sub-past-due --user $(U)

stripe-sub-cancel:
	go run ./cmd/stripescenario sub-cancel --user $(U)

stripe-resend:
	go run ./cmd/stripescenario resend $(EVENT)

# Ad-hoc passthrough for exploring unhandled event types.
stripe-trigger:
	stripe trigger $(TYPE)
```

Usage:
```
make stripe-setup                                  # one-time
make stripe-pack-buy U=<uuid> MINUTES=300          # exercises pack purchase
make stripe-sub-start U=<uuid>                     # links customer, fires invoice.paid
make stripe-sub-past-due U=<uuid>                  # downgrades to free
make stripe-sub-cancel U=<uuid>                    # also downgrades to free
make stripe-resend EVENT=evt_1ABC...               # idempotency check
make stripe-trigger TYPE=charge.refunded           # ad-hoc event
```

### 5. Safety rails

- **Test-mode assertion** in `NewRunner` (above). Refuses to run on `sk_live_...`.
- **No prod DB access.** `config.Load` uses the same `.env` as the dev server; pointing it at prod is an explicit misconfiguration, not a silent risk.
- **No auto-destructive DB writes.** Only `sub-start` writes: a single `UPDATE users SET stripe_customer_id`. Existing value is overwritten intentionally (rerunning `sub-start` rotates the customer link).

## Data flow (example: `pack-buy`)

```
$ make stripe-pack-buy USER=9f1a... MINUTES=120
  │
  ├─ cmd/stripescenario pack-buy …
  │    └─ exec: stripe trigger checkout.session.completed
  │              --add checkout_session:metadata.user_id=9f1a...
  │              --add checkout_session:metadata.pack_minutes=120
  │
  ├─ Stripe API receives trigger → emits checkout.session.completed event
  │
  ├─ `stripe listen` (in Procfile.dev) receives event, signs with session whsec,
  │    POSTs to localhost:8080/api/webhooks/stripe
  │
  ├─ handler.PostStripeWebhook → verifies signature → b.HandleStripeWebhook
  │    → handleCheckoutCompleted → CreatePurchaseGrant
  │
  └─ DB: new row in grants (source=purchase, 120 min), ledger entry (purchase, +120)
```

## Out of scope (deferred to future specs)

- **`charge.refunded` handler.** Needs partial-refund semantics, pro-rata math, schema additions (`payment_intent_id`, `initial_charge_amount_cents`, ledger `stripe_event_id`). Spec when refund volume justifies the investment.
- **Dispute handling (`charge.dispute.created`, `charge.dispute.closed`).** Needs ledger-based hold/reversal state machine. Spec alongside refunds.
- **`invoice.payment_failed` handler.** Already covered by `subscription.updated → past_due`. Only worth a dedicated handler for first-failure user notification, which is a product decision.
- **Scenarios for unhandled events.** Principle: a scenario exists only when (a) the handler has a case for the event AND (b) reaching that case's non-trivial path requires setup beyond `stripe trigger`. When refund/dispute handling ships, add `refund-pack`, `dispute-open`, etc. alongside.

## Open migration / backfill considerations

None — no schema changes. Pure additive tooling.

## Testing

- Manual verification per scenario: run it, observe DB state and handler logs.
- `go build ./cmd/stripescenario` in `make test` (via existing `go build ./...`).
- No new Go unit tests — scenarios are thin wrappers over `stripe trigger`; tests would stub the one thing we're trying to exercise (the CLI).

## Acceptance

- `make dev` starts vite + air + `stripe listen` together; webhook events delivered to local server without manual CLI invocation.
- `make stripe-setup` produces a working `.env` entry; subsequent `stripe listen` sessions verify signatures cleanly.
- Each of the four scenarios produces the expected DB mutation (new grant row, plan flip, etc.) observable via `psql`.
- `make stripe-resend EVENT=<id>` applied to a recent event is a no-op (dedup via `stripe_event_id` unique constraint).
- Attempting to run any scenario with `sk_live_...` configured fails loudly before any Stripe API call.
