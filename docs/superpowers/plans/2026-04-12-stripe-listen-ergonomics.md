# Stripe Listen Ergonomics Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make local Stripe webhook testing frictionless — `stripe listen` runs under `make dev`, the signing secret auto-pins, and three scenarios (`pack-buy`, `sub-start`, `sub-cancel`) plus a `resend` helper exercise the already-handled webhook branches (and verify idempotency) in one command.

**Architecture:** Additive. Three surfaces: (1) a new line in `Procfile.dev`; (2) a `scripts/stripe-setup.sh` shell script + `make stripe-setup` target that pins `STRIPE_WEBHOOK_SECRET` in `.env`; (3) a `cmd/stripescenario/` Go binary built with `urfave/cli/v3` (matching `cmd/drillctl`) that either shells out to `stripe trigger` (for `pack-buy`, `resend`) or drives the Stripe SDK directly (for `sub-start`, `sub-cancel`, where CLI fixtures pre-create their own customers and can't be re-pointed to ours). No schema changes. No handler changes.

**Tech Stack:** Go 1.x, `github.com/urfave/cli/v3`, `github.com/stripe/stripe-go/v82` (customer, paymentmethod, subscription packages), `github.com/jackc/pgx/v5/pgxpool`, `github.com/google/uuid`, existing `internal/db` sqlc queries, `internal/billing`. Shell: bash. Stripe CLI ≥ 1.32.

**Spec:** `docs/superpowers/specs/2026-04-12-stripe-listen-ergonomics-design.md`

---

## File Structure

**New files:**
- `scripts/stripe-setup.sh` — one-time secret-pinning helper.
- `cmd/stripescenario/main.go` — `urfave/cli/v3` root + subcommand registration, `.env` load, usage.
- `cmd/stripescenario/runner.go` — `Runner` struct, `newRunner` constructor (test-mode assert, pool, `stripe.Key`), `Close`, and the `runStripeCLI` helper.
- `cmd/stripescenario/runner_test.go` — unit tests for `newRunner` safety assertion.
- `cmd/stripescenario/packbuy.go` — `pack-buy` subcommand + `(*Runner).packBuy`.
- `cmd/stripescenario/packbuy_test.go` — unit test for `ValidPackSize` gating.
- `cmd/stripescenario/substart.go` — `sub-start` subcommand + `(*Runner).subStart`.
- `cmd/stripescenario/subcancel.go` — `sub-cancel` subcommand + `(*Runner).subCancel`.
- `cmd/stripescenario/resend.go` — `resend` subcommand + `(*Runner).resend`.

**Modified files:**
- `Procfile.dev` — add `stripe:` entry.
- `.env.example` — comment pointing to `make stripe-setup` under the `STRIPE_WEBHOOK_SECRET` line.
- `Makefile` — add `stripe-setup`, `stripe-pack-buy`, `stripe-sub-start`, `stripe-sub-cancel`, `stripe-resend`, `stripe-trigger` targets.

Each subcommand file owns one behavior; `runner.go` owns shared dependencies; `main.go` owns dispatch. Files remain small and focused.

---

### Task 1: Procfile.dev + `.env.example` pointer

**Files:**
- Modify: `Procfile.dev`
- Modify: `.env.example`

- [ ] **Step 1: Read current Procfile.dev and .env.example**

Run:
```bash
cat Procfile.dev
cat .env.example
```

- [ ] **Step 2: Add `stripe` line to Procfile.dev**

Final `Procfile.dev`:
```
watch: cd web && npx vite build --watch
server: set -a && . ./.env && set +a && air
stripe: stripe listen --forward-to localhost:8080/api/webhooks/stripe
```

- [ ] **Step 3: Annotate `.env.example`**

Change the `STRIPE_WEBHOOK_SECRET` line so it reads:
```
STRIPE_WEBHOOK_SECRET=whsec_...  # Populated by `make stripe-setup` — do not edit by hand.
```

- [ ] **Step 4: Smoke-test**

Run:
```bash
test -f Procfile.dev && grep -q '^stripe: ' Procfile.dev && echo OK
grep -q 'make stripe-setup' .env.example && echo OK
```
Expected: two lines of `OK`.

- [ ] **Step 5: Commit**

```bash
git add Procfile.dev .env.example
git commit -m "feat(dev): run stripe listen under make dev"
```

---

### Task 2: `scripts/stripe-setup.sh`

**Files:**
- Create: `scripts/stripe-setup.sh`

- [ ] **Step 1: Verify script directory exists**

Run:
```bash
ls scripts/
```
Expected: includes `cloud_bootstrap.py`, `deploy.sh`, etc.

- [ ] **Step 2: Write the script**

Create `scripts/stripe-setup.sh`:
```bash
#!/usr/bin/env bash
# Upsert STRIPE_WEBHOOK_SECRET into .env using the Stripe CLI's device-local
# session secret. Idempotent: re-runnable after `stripe logout && stripe login`.
set -euo pipefail

if ! command -v stripe >/dev/null 2>&1; then
    echo "error: stripe CLI not installed (brew install stripe/stripe-cli/stripe)" >&2
    exit 1
fi

if ! stripe config --list >/dev/null 2>&1; then
    echo "stripe CLI not configured; launching login..."
    stripe login
fi

secret="$(stripe listen --print-secret)"
if [[ -z "$secret" ]]; then
    echo "error: stripe listen --print-secret returned empty" >&2
    exit 1
fi

if [[ ! -f .env ]]; then
    echo "error: .env not found; copy .env.example first" >&2
    exit 1
fi

# Ensure .env ends with a newline before any append.
if [[ -s .env ]] && [[ "$(tail -c 1 .env)" != $'\n' ]]; then
    printf '\n' >> .env
fi

if grep -q '^STRIPE_WEBHOOK_SECRET=' .env; then
    # `-i.bak` form works on both BSD (macOS) and GNU sed.
    sed -i.bak "s|^STRIPE_WEBHOOK_SECRET=.*|STRIPE_WEBHOOK_SECRET=${secret}|" .env
    rm -f .env.bak
else
    echo "STRIPE_WEBHOOK_SECRET=${secret}" >> .env
fi

echo "STRIPE_WEBHOOK_SECRET written to .env"
```

- [ ] **Step 3: Make executable**

Run:
```bash
chmod +x scripts/stripe-setup.sh
```

- [ ] **Step 4: Static check**

Run:
```bash
bash -n scripts/stripe-setup.sh && echo OK
```
Expected: `OK`.

- [ ] **Step 5: Manual smoke test (if logged in to stripe)**

Run:
```bash
./scripts/stripe-setup.sh
grep '^STRIPE_WEBHOOK_SECRET=' .env
```
Expected: a `whsec_...` value present. If the user is not logged in, they get a `stripe login` prompt; document this behavior, don't fail the task.

- [ ] **Step 6: Commit**

```bash
git add scripts/stripe-setup.sh
git commit -m "feat(dev): add stripe-setup.sh to pin webhook secret"
```

---

### Task 3: Scenario runner scaffold — `main.go`, `runner.go`, safety-assert test

**Files:**
- Create: `cmd/stripescenario/main.go`
- Create: `cmd/stripescenario/runner.go`
- Create: `cmd/stripescenario/runner_test.go`

- [ ] **Step 1: Write the failing test**

Create `cmd/stripescenario/runner_test.go`:
```go
package main

import (
	"context"
	"strings"
	"testing"
)

func TestNewRunnerRejectsLiveKey(t *testing.T) {
	t.Setenv("STRIPE_SECRET_KEY", "sk_live_ABC")
	t.Setenv("DATABASE_URL", "postgres://example/db")
	_, err := newRunner(context.Background())
	if err == nil {
		t.Fatal("expected error for live key, got nil")
	}
	if !strings.Contains(err.Error(), "not a test key") {
		t.Fatalf("expected 'not a test key' in error, got %q", err.Error())
	}
}

func TestNewRunnerRejectsMissingKey(t *testing.T) {
	t.Setenv("STRIPE_SECRET_KEY", "")
	t.Setenv("DATABASE_URL", "postgres://example/db")
	_, err := newRunner(context.Background())
	if err == nil {
		t.Fatal("expected error for missing key, got nil")
	}
}

func TestNewRunnerRejectsMissingDatabaseURL(t *testing.T) {
	t.Setenv("STRIPE_SECRET_KEY", "sk_test_ABC")
	t.Setenv("DATABASE_URL", "")
	_, err := newRunner(context.Background())
	if err == nil {
		t.Fatal("expected error for missing DATABASE_URL, got nil")
	}
	if !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("expected DATABASE_URL in error, got %q", err.Error())
	}
}
```

- [ ] **Step 2: Run test to confirm it fails (package missing)**

Run:
```bash
go test ./cmd/stripescenario/... -run TestNewRunnerRejectsLiveKey
```
Expected: FAIL — `no Go files in ./cmd/stripescenario` or `newRunner undefined`.

- [ ] **Step 3: Write `runner.go`**

Create `cmd/stripescenario/runner.go`:
```go
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stripe/stripe-go/v82"
)

// Runner holds the shared dependencies for every scenario subcommand.
// It asserts test-mode credentials in its constructor, opens a pool,
// and sets the Stripe SDK's package-level API key.
type Runner struct {
	pool *pgxpool.Pool
}

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

func (r *Runner) Close() {
	if r.pool != nil {
		r.pool.Close()
	}
}

// runStripeCLI shells out to the local `stripe` binary, piping stdio through.
// Takes ctx so Ctrl-C propagates to the spawned process. No callers until
// Task 4 wires packbuy.go — expected dead code at the end of Task 3.
func (r *Runner) runStripeCLI(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "stripe", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("stripe %s: %w", strings.Join(args, " "), err)
	}
	return nil
}
```

- [ ] **Step 4: Write `main.go` (dispatch skeleton; subcommands added in later tasks)**

Create `cmd/stripescenario/main.go`:
```go
// Command stripescenario drives Stripe webhook scenarios against the local
// dev server. Fires real events through the developer's Stripe test account;
// the `stripe listen` process under make dev forwards them to the webhook.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/joho/godotenv"
	"github.com/urfave/cli/v3"
)

func main() {
	// .env is optional here; many CI-ish invocations set env directly.
	_ = godotenv.Load()

	cmd := &cli.Command{
		Name:  "stripescenario",
		Usage: "Drive Stripe webhook scenarios against the local dev server",
		Commands: []*cli.Command{
			// Subcommands wired in subsequent tasks:
			//   packBuyCmd(), subStartCmd(), subCancelCmd(), resendCmd(),
		},
	}

	if err := cmd.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		os.Exit(1)
	}
}
```

- [ ] **Step 5: Run safety-assert tests**

Run:
```bash
go test ./cmd/stripescenario/... -run TestNewRunner
```
Expected: three tests PASS.

- [ ] **Step 6: Verify binary builds**

Run:
```bash
go build -o /dev/null ./cmd/stripescenario
```
Expected: no output, exit 0. (`-o /dev/null` avoids producing a stray `stripescenario` binary at repo root.)

- [ ] **Step 7: Smoke-run `--help`**

Run:
```bash
go run ./cmd/stripescenario --help
```
Expected: usage text listing `stripescenario` as the command name. urfave/cli auto-adds a `help, h` entry under COMMANDS; no user-defined subcommands appear yet.

- [ ] **Step 8: Commit**

```bash
git add cmd/stripescenario/
git commit -m "feat(stripescenario): scaffold runner with test-mode safety assert"
```

---

### Task 4: `pack-buy` subcommand

**Files:**
- Create: `cmd/stripescenario/packbuy.go`
- Create: `cmd/stripescenario/packbuy_test.go`
- Modify: `cmd/stripescenario/main.go` (register the subcommand)

- [ ] **Step 1: Write the failing test for minute validation**

Create `cmd/stripescenario/packbuy_test.go`:
```go
package main

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestPackBuyRejectsInvalidMinutes(t *testing.T) {
	// Construct a Runner without touching the DB — packBuy must reject
	// invalid minute counts before any Stripe or DB interaction.
	r := &Runner{}
	err := r.packBuy(context.Background(), uuid.New(), 999)
	if err == nil {
		t.Fatal("expected error for invalid minute count, got nil")
	}
	if !strings.Contains(err.Error(), "invalid pack size") {
		t.Fatalf("expected 'invalid pack size' in error, got %q", err.Error())
	}
}
```

- [ ] **Step 2: Run test to confirm it fails (packBuy undefined)**

Run:
```bash
go test ./cmd/stripescenario/... -run TestPackBuy
```
Expected: FAIL — `undefined: (*Runner).packBuy`.

- [ ] **Step 3: Implement `packbuy.go`**

Create `cmd/stripescenario/packbuy.go`:
```go
package main

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/urfave/cli/v3"

	"github.com/btc/drill/internal/billing"
)

func packBuyCmd() *cli.Command {
	return &cli.Command{
		Name:  "pack-buy",
		Usage: "Fire checkout.session.completed for a user (creates a PurchaseGrant)",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "user", Required: true, Usage: "target user UUID"},
			&cli.IntFlag{Name: "minutes", Value: 120, Usage: "pack size (120, 300, 600)"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			userID, err := uuid.Parse(cmd.String("user"))
			if err != nil {
				return fmt.Errorf("invalid --user UUID: %w", err)
			}
			r, err := newRunner(ctx)
			if err != nil {
				return err
			}
			defer r.Close()
			return r.packBuy(ctx, userID, cmd.Int("minutes"))
		},
	}
}

// packBuy validates the pack size against production rules, then shells out
// to `stripe trigger` with metadata fields matching what CreateCheckoutSession
// injects in internal/backend/billing.go.
//
// Uses --add (not --override) because the checkout_session fixture does not
// pre-populate metadata.user_id or metadata.pack_minutes; we are adding fields.
func (r *Runner) packBuy(ctx context.Context, userID uuid.UUID, minutes int) error {
	if !billing.ValidPackSize(minutes) {
		return fmt.Errorf("invalid pack size %d (valid: 120, 300, 600)", minutes)
	}
	return r.runStripeCLI(ctx,
		"trigger", "checkout.session.completed",
		"--add", fmt.Sprintf("checkout_session:metadata.user_id=%s", userID),
		"--add", fmt.Sprintf("checkout_session:metadata.pack_minutes=%d", minutes),
	)
}
```

- [ ] **Step 4: Register the subcommand**

Edit `cmd/stripescenario/main.go`, replace the `Commands: []*cli.Command{...}` block with:
```go
		Commands: []*cli.Command{
			packBuyCmd(),
			// subStartCmd(), subCancelCmd(), resendCmd() added in later tasks
		},
```

- [ ] **Step 5: Run the unit test**

Run:
```bash
go test ./cmd/stripescenario/... -run TestPackBuy
```
Expected: PASS.

- [ ] **Step 6: Verify subcommand help**

Run:
```bash
go run ./cmd/stripescenario pack-buy --help
```
Expected: help text showing `--user` required, `--minutes` defaulting to 120.

- [ ] **Step 7: Commit**

```bash
git add cmd/stripescenario/packbuy.go cmd/stripescenario/packbuy_test.go cmd/stripescenario/main.go
git commit -m "feat(stripescenario): add pack-buy subcommand"
```

---

### Task 5: `sub-start` subcommand

**Files:**
- Create: `cmd/stripescenario/substart.go`
- Modify: `cmd/stripescenario/main.go` (register)

- [ ] **Step 1: Verify `STRIPE_PRO_PRICE_ID` is referenced elsewhere**

Run:
```bash
grep -n 'STRIPE_PRO_PRICE_ID\|ProPriceID' internal/config/config.go
```
Expected: at least one match confirming the env var name. The scenario reads it directly via `os.Getenv`.

- [ ] **Step 2: Implement `substart.go`**

Create `cmd/stripescenario/substart.go`:
```go
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/customer"
	"github.com/stripe/stripe-go/v82/subscription"
	"github.com/urfave/cli/v3"

	"github.com/btc/drill/internal/db"
)

func subStartCmd() *cli.Command {
	return &cli.Command{
		Name:  "sub-start",
		Usage: "Create Stripe customer + subscription for a user (fires invoice.paid)",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "user", Required: true, Usage: "target user UUID"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			userID, err := uuid.Parse(cmd.String("user"))
			if err != nil {
				return fmt.Errorf("invalid --user UUID: %w", err)
			}
			r, err := newRunner(ctx)
			if err != nil {
				return err
			}
			defer r.Close()
			return r.subStart(ctx, userID)
		},
	}
}

// subStart drives the real Stripe SDK to create a customer + subscription.
// A CLI-fixture `stripe trigger invoice.paid` would emit an event against
// the fixture's own ephemeral customer, not ours, so handleInvoicePaid's
// customer-lookup would hit n==0. Real SDK calls produce real webhooks.
func (r *Runner) subStart(ctx context.Context, userID uuid.UUID) error {
	priceID := os.Getenv("STRIPE_PRO_PRICE_ID")
	if priceID == "" {
		return fmt.Errorf("STRIPE_PRO_PRICE_ID not set in env")
	}

	q := db.New(r.pool)

	// Existence check: UpdateUserStripeCustomerID is `:exec` and silently
	// succeeds on zero rows affected. Do the check before creating anything
	// in Stripe so we don't orphan resources on a bad user ID.
	if _, err := q.GetUserByID(ctx, userID); err != nil {
		return fmt.Errorf("lookup user %s: %w", userID, err)
	}

	// pm_card_visa is a stable test-mode PaymentMethod fixture; gated by the
	// sk_test_ assertion in newRunner. In live mode Stripe would reject it.
	// Note: existing code in internal/backend/billing.go uses the deprecated
	// stripe.Params.Metadata form; prefer the top-level field in new code.
	// Tech-debt cleanup for billing.go belongs in a separate change.
	cust, err := customer.New(&stripe.CustomerParams{
		PaymentMethod: stripe.String("pm_card_visa"),
		InvoiceSettings: &stripe.CustomerInvoiceSettingsParams{
			DefaultPaymentMethod: stripe.String("pm_card_visa"),
		},
		Metadata: map[string]string{"drill_user_id": userID.String()},
	})
	if err != nil {
		return fmt.Errorf("create stripe customer: %w", err)
	}

	if err := q.UpdateUserStripeCustomerID(ctx, db.UpdateUserStripeCustomerIDParams{
		ID:               userID,
		StripeCustomerID: pgtype.Text{String: cust.ID, Valid: true},
	}); err != nil {
		return fmt.Errorf("persist stripe_customer_id: %w", err)
	}

	// collection_method defaults to charge_automatically; with pm_card_visa
	// attached and set as default, Stripe test mode creates and pays the
	// invoice synchronously, firing invoice.paid.
	s, err := subscription.New(&stripe.SubscriptionParams{
		Customer: stripe.String(cust.ID),
		Items: []*stripe.SubscriptionItemsParams{
			{Price: stripe.String(priceID)},
		},
	})
	if err != nil {
		return fmt.Errorf("create subscription: %w", err)
	}

	fmt.Fprintf(os.Stdout, "subscription %s on customer %s for user %s\n", s.ID, cust.ID, userID)
	return nil
}
```

- [ ] **Step 3: Register the subcommand**

Edit `cmd/stripescenario/main.go`:
```go
		Commands: []*cli.Command{
			packBuyCmd(),
			subStartCmd(),
			// subCancelCmd(), resendCmd() added in later tasks
		},
```

- [ ] **Step 4: Verify build**

Run:
```bash
go build ./cmd/stripescenario
```
Expected: success.

- [ ] **Step 5: Verify help text**

Run:
```bash
go run ./cmd/stripescenario sub-start --help
```
Expected: usage showing `--user` required.

- [ ] **Step 6: Commit**

```bash
git add cmd/stripescenario/substart.go cmd/stripescenario/main.go
git commit -m "feat(stripescenario): add sub-start subcommand (SDK-driven)"
```

---

### Task 6: `sub-cancel` subcommand

**Files:**
- Create: `cmd/stripescenario/subcancel.go`
- Modify: `cmd/stripescenario/main.go` (register)

- [ ] **Step 1: Implement `subcancel.go`**

Create `cmd/stripescenario/subcancel.go`:
```go
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/subscription"
	"github.com/urfave/cli/v3"

	"github.com/btc/drill/internal/db"
)

func subCancelCmd() *cli.Command {
	return &cli.Command{
		Name:  "sub-cancel",
		Usage: "Cancel active subscriptions for a user (fires subscription.deleted)",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "user", Required: true, Usage: "target user UUID"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			userID, err := uuid.Parse(cmd.String("user"))
			if err != nil {
				return fmt.Errorf("invalid --user UUID: %w", err)
			}
			r, err := newRunner(ctx)
			if err != nil {
				return err
			}
			defer r.Close()
			return r.subCancel(ctx, userID)
		},
	}
}

// subCancel reads the user's stripe_customer_id, lists all non-canceled
// subscriptions under that customer (no Status filter — covers active,
// trialing, past_due), and cancels each immediately.
func (r *Runner) subCancel(ctx context.Context, userID uuid.UUID) error {
	q := db.New(r.pool)
	user, err := q.GetUserByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("lookup user %s: %w", userID, err)
	}
	if !user.StripeCustomerID.Valid {
		return fmt.Errorf("user %s has no stripe_customer_id; run sub-start first", userID)
	}

	iter := subscription.List(&stripe.SubscriptionListParams{
		Customer: stripe.String(user.StripeCustomerID.String),
	})

	canceled := 0
	for iter.Next() {
		s := iter.Subscription()
		// Skip already-terminal subs.
		if s.Status == stripe.SubscriptionStatusCanceled ||
			s.Status == stripe.SubscriptionStatusIncompleteExpired {
			continue
		}
		if _, err := subscription.Cancel(s.ID, nil); err != nil {
			return fmt.Errorf("cancel %s: %w", s.ID, err)
		}
		fmt.Fprintf(os.Stdout, "canceled %s\n", s.ID)
		canceled++
	}
	if err := iter.Err(); err != nil {
		return fmt.Errorf("list subscriptions: %w", err)
	}
	if canceled == 0 {
		fmt.Fprintf(os.Stderr, "no active subscriptions for user %s\n", userID)
	}
	return nil
}
```

- [ ] **Step 2: Register the subcommand**

Edit `cmd/stripescenario/main.go`:
```go
		Commands: []*cli.Command{
			packBuyCmd(),
			subStartCmd(),
			subCancelCmd(),
			// resendCmd() added in next task
		},
```

- [ ] **Step 3: Build**

Run:
```bash
go build ./cmd/stripescenario
```
Expected: success.

- [ ] **Step 4: Verify help text**

Run:
```bash
go run ./cmd/stripescenario sub-cancel --help
```
Expected: usage with `--user` required.

- [ ] **Step 5: Commit**

```bash
git add cmd/stripescenario/subcancel.go cmd/stripescenario/main.go
git commit -m "feat(stripescenario): add sub-cancel subcommand"
```

---

### Task 7: `resend` subcommand

**Files:**
- Create: `cmd/stripescenario/resend.go`
- Modify: `cmd/stripescenario/main.go` (register)

- [ ] **Step 1: Implement `resend.go`**

Create `cmd/stripescenario/resend.go`:
```go
package main

import (
	"context"
	"fmt"

	"github.com/urfave/cli/v3"
)

func resendCmd() *cli.Command {
	return &cli.Command{
		Name:      "resend",
		Usage:     "Resend a previously-delivered Stripe event by ID (verifies handler idempotency)",
		ArgsUsage: "<event_id>",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() != 1 {
				return fmt.Errorf("usage: stripescenario resend <event_id>")
			}
			eventID := cmd.Args().First()
			r, err := newRunner(ctx)
			if err != nil {
				return err
			}
			defer r.Close()
			return r.resend(ctx, eventID)
		},
	}
}

// resend shells out to `stripe events resend`; no SDK equivalent exists.
// Dedup guarantees live in the handler: CreatePurchaseGrant and
// CreateSubscriptionGrant use grants.stripe_event_id UNIQUE. Resending
// customer.subscription.* events is NOT a no-op (known handler gap).
func (r *Runner) resend(ctx context.Context, eventID string) error {
	return r.runStripeCLI(ctx, "events", "resend", eventID)
}
```

- [ ] **Step 2: Register the subcommand**

Edit `cmd/stripescenario/main.go`:
```go
		Commands: []*cli.Command{
			packBuyCmd(),
			subStartCmd(),
			subCancelCmd(),
			resendCmd(),
		},
```

- [ ] **Step 3: Build + help smoke**

Run:
```bash
go build ./cmd/stripescenario
go run ./cmd/stripescenario --help
go run ./cmd/stripescenario resend --help
```
Expected: root help lists all four subcommands; `resend --help` shows `<event_id>` ArgsUsage.

- [ ] **Step 4: Commit**

```bash
git add cmd/stripescenario/resend.go cmd/stripescenario/main.go
git commit -m "feat(stripescenario): add resend subcommand"
```

---

### Task 8: Makefile targets

**Files:**
- Modify: `Makefile`

- [ ] **Step 1: Read current Makefile tail**

Run:
```bash
tail -20 Makefile
```
Note: observe formatting conventions (tab indentation, trailing blank lines).

- [ ] **Step 2: Append stripe targets**

Add to the end of `Makefile`:
```makefile

# --- Stripe scenarios ----------------------------------------------------------
# U is the target user's UUID. Chosen over USER to avoid colliding with the
# shell's auto-exported USER env var (which Make inherits as a default).

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

# Passthrough for ad-hoc event triggers (unhandled-event exploration).
stripe-trigger:
	stripe trigger "$(TYPE)"

.PHONY: stripe-setup stripe-pack-buy stripe-sub-start stripe-sub-cancel stripe-resend stripe-trigger
```

- [ ] **Step 3: Parse check**

Run:
```bash
make -n stripe-pack-buy U=00000000-0000-0000-0000-000000000000
```
Expected: prints `go run ./cmd/stripescenario pack-buy --user "00000000-0000-0000-0000-000000000000"` (no execution, -n dry runs).

- [ ] **Step 4: Verify `-n` also handles MINUTES conditional**

Run:
```bash
make -n stripe-pack-buy U=00000000-0000-0000-0000-000000000000 MINUTES=300
```
Expected: command includes `--minutes 300`.

- [ ] **Step 5: Commit**

```bash
git add Makefile
git commit -m "feat(make): add stripe-* targets for scenario runner"
```

---

### Task 9: Full build + lint + repo test pass

**Files:** none modified — verification only.

- [ ] **Step 1: Run full CI**

Run:
```bash
make test
```
Expected: all checks pass (buf lint, codegen, frontend typecheck/lint/tests, backend tests including the new `TestNewRunner*` and `TestPackBuyRejectsInvalidMinutes`).

- [ ] **Step 2: If any failures, fix in place and re-run until green**

Common likely failures and fixes:
- `go mod tidy` needed for `joho/godotenv` if imports drift — it is already a direct dep (see `go.mod`), so no action expected.
- Missing newline at end of files — add one.

No commit here if no fixes needed; otherwise `git commit -m "fix: <specific fix>"`.

---

### Task 10: End-to-end manual verification

**Files:** none modified — verification only. Ensure this is done against a fresh-ish dev DB with a known user UUID.

- [ ] **Step 1: Pin the webhook secret**

Run:
```bash
make stripe-setup
grep '^STRIPE_WEBHOOK_SECRET=' .env
```
Expected: a `whsec_...` value.

- [ ] **Step 2: Start dev**

In one terminal:
```bash
make dev
```
Expected: overmind starts three processes; `stripe` tab shows `Ready! You are using Stripe API Version [...]`.

- [ ] **Step 3: Seed a dev user and capture UUID**

In another terminal:
```bash
go run ./cmd/drillctl seed
psql "$DATABASE_URL" -c "SELECT id FROM users WHERE email = 'dev@drill.dev';"
```
Expected: a UUID, e.g. `9f1a3c4e-...`. Save it as `$U`.

- [ ] **Step 4: Run `pack-buy` and verify grant**

Run (substitute the UUID):
```bash
U=<uuid-from-step-3>
make stripe-pack-buy U=$U MINUTES=120
psql "$DATABASE_URL" -c "SELECT source, initial_minutes, remaining_minutes FROM grants WHERE user_id = '$U' AND source = 'purchase' ORDER BY created_at DESC LIMIT 1;"
```
Expected: a row with `source=purchase, initial_minutes=120, remaining_minutes=120`.

Also check the server tab in overmind for `slog.Info purchase grant created user_id=...`.

- [ ] **Step 5: Run `sub-start` and verify grant + customer link**

Run:
```bash
make stripe-sub-start U=$U
psql "$DATABASE_URL" -c "SELECT stripe_customer_id, plan FROM users WHERE id = '$U';"
psql "$DATABASE_URL" -c "SELECT source, initial_minutes FROM grants WHERE user_id = '$U' AND source = 'subscription' ORDER BY created_at DESC LIMIT 1;"
```
Expected: `stripe_customer_id` populated, `plan='pro'`, a subscription grant row.

- [ ] **Step 6: Run `sub-cancel` and verify plan flip**

Run:
```bash
make stripe-sub-cancel U=$U
psql "$DATABASE_URL" -c "SELECT plan FROM users WHERE id = '$U';"
```
Expected: stdout includes `canceled sub_...`; `plan` flips back to `free`.

- [ ] **Step 7: Verify idempotency via `resend`**

From the server log, copy the most recent `event_id=evt_...` for `checkout.session.completed`. Run:
```bash
EVENT=evt_...
make stripe-resend EVENT=$EVENT
```
Expected: `stripe listen` re-delivers the event; server log shows the handler received it; no new grant row appears (`CreatePurchaseGrant` `ON CONFLICT DO NOTHING`). Re-query grants to confirm count unchanged.

- [ ] **Step 8: Safety-assert spot check**

Temporarily set a bad key:
```bash
STRIPE_SECRET_KEY=sk_live_ABC DATABASE_URL="$DATABASE_URL" go run ./cmd/stripescenario pack-buy --user $U
```
Expected: non-zero exit with `refusing to run: STRIPE_SECRET_KEY missing or not a test key`. No API call is made.

- [ ] **Step 9: Tear down `make dev`**

`Ctrl-C` the overmind session in the terminal from Step 2.

- [ ] **Step 10: (No commit — this task is verification only.)**

---

## Self-Review Notes

- **Spec coverage:** Each of the five spec components maps to tasks — Procfile (T1), setup script (T2), runner scaffold + safety (T3), three scenarios (T4-T6) + resend (T7), Make targets (T8); verification at T9-T10.
- **Known gap acknowledged in-plan:** T7 comments that resend is NOT no-op for `customer.subscription.*` events. Consistent with the spec's narrowed acceptance criterion.
- **Non-goals honored:** No schema migration, no handler changes, no integration tests driven by `stripe listen`. Unit tests are limited to two pure-Go checks (safety assert, minute validation).
- **Intentional deviations from spec:**
  - Task 2 adds a `command -v stripe` preflight check (spec didn't specify). Strict improvement — earlier/clearer error when the CLI isn't installed.
  - Task 6 inlines the customer-lookup logic (`GetUserByID` + `StripeCustomerID.Valid` check) instead of introducing the spec's `(*Runner).lookupStripeCustomer` helper. YAGNI — only one caller today; extract if additional subscription-related scenarios appear.
- **Tech debt flagged by the plan (boy-scout opportunity, not in scope):**
  - `internal/backend/billing.go` uses the deprecated `stripe.Params.Metadata` form in `CreateCheckoutSession` / `CreatePortalSession`. New code in `substart.go` uses the modern top-level `Metadata` field on `CustomerParams`. A separate change should migrate the existing callers.
