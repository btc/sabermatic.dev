package idleunsub_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	stripe "github.com/stripe/stripe-go/v82"

	"github.com/btc/drill/internal/backend"
	"github.com/btc/drill/internal/backendtest"
	"github.com/btc/drill/internal/feat/idleunsub"
)

// silentLogger discards log output. Tests assert on DB state, not logs.
// Set IDLEUNSUB_TEST_DEBUG=1 to surface internal log lines while debugging.
func silentLogger() *slog.Logger {
	if os.Getenv("IDLEUNSUB_TEST_DEBUG") == "1" {
		return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fixture bundles the backend + Stripe stub + service for a single test.
// Tests mutate Sub or per-user DB state before calling Svc.HandleInvoiceUpcoming.
type fixture struct {
	B           *backend.Backend
	Ctx         context.Context
	UserID      uuid.UUID
	CustID      string
	SubID       string
	PeriodStart time.Time
	PeriodEnd   time.Time
	Sub         *stripe.Subscription
	Fake        *fakeStripe
	Svc         *idleunsub.Service
	Event       stripe.Event
}

// setupHappyPath spins up a fresh backend, seeds a Pro user with a Stripe
// customer mapping, force-ages their last_active to 3 months ago, and
// constructs a fake Stripe Subscription whose period started 23 days ago
// (= idle for more than one monthly interval).
func setupHappyPath(t *testing.T) *fixture {
	t.Helper()
	b := pg.NewBackend(t)
	ctx := context.Background()
	userID := backendtest.SeedUser(t, b)
	custID := "cus_test_" + uuid.NewString()[:8]
	subID := "sub_test_" + uuid.NewString()[:8]

	// Wire user to the Stripe customer ID and grandfather them in
	// (idle_eligible_after far in the past).
	_, err := b.Pool().Exec(ctx, `
		UPDATE users
		SET stripe_customer_id   = $1,
		    idle_eligible_after  = NOW() - INTERVAL '6 months',
		    plan                 = 'pro'
		WHERE id = $2`, custID, userID)
	require.NoError(t, err)

	// Insert an auth_sessions row whose last_active is 3 months ago.
	// SeedUser does not create one — Signup() only creates the user — so
	// we need to write one explicitly. token_hash must be unique.
	_, err = b.Pool().Exec(ctx, `
		INSERT INTO auth_sessions (user_id, token_hash, expires_at, last_active)
		VALUES ($1, $2, NOW() + INTERVAL '30 days', NOW() - INTERVAL '3 months')`,
		userID, "tok_test_"+uuid.NewString())
	require.NoError(t, err)

	// Build the fake Stripe subscription. Monthly billing; period started
	// 23 days ago, ends ~7 days from now. Threshold (= periodStart - 1mo)
	// sits ~53 days ago, so a 3-months-ago last_active is "before" it.
	now := time.Now().UTC().Truncate(time.Second)
	periodStart := now.AddDate(0, 0, -23)
	periodEnd := periodStart.AddDate(0, 1, 0)
	sub := &stripe.Subscription{
		ID:                subID,
		Status:            stripe.SubscriptionStatusActive,
		CancelAtPeriodEnd: false,
		Customer:          &stripe.Customer{ID: custID},
		Items: &stripe.SubscriptionItemList{Data: []*stripe.SubscriptionItem{{
			CurrentPeriodStart: periodStart.Unix(),
			CurrentPeriodEnd:   periodEnd.Unix(),
			Price: &stripe.Price{Recurring: &stripe.PriceRecurring{
				Interval: stripe.PriceRecurringIntervalMonth,
			}},
		}}},
	}
	fake := &fakeStripe{subs: map[string]*stripe.Subscription{subID: sub}}

	svc := idleunsub.NewService(b.Pool(), fake, nullMailer{}, nil, silentLogger())

	event := stripe.Event{
		ID:   "evt_" + uuid.NewString()[:8],
		Type: "invoice.upcoming",
		Data: &stripe.EventData{Object: map[string]interface{}{
			"subscription": subID,
		}},
	}

	return &fixture{
		B:           b,
		Ctx:         ctx,
		UserID:      userID,
		CustID:      custID,
		SubID:       subID,
		PeriodStart: periodStart,
		PeriodEnd:   periodEnd,
		Sub:         sub,
		Fake:        fake,
		Svc:         svc,
		Event:       event,
	}
}

// requireNoCancel asserts no Stripe update fired and no audit row was
// written. Used by every early-return test below.
func requireNoCancel(t *testing.T, fx *fixture) {
	t.Helper()
	require.Empty(t, fx.Fake.updateCalls, "expected no Stripe update calls")
	var count int
	err := fx.B.Pool().QueryRow(fx.Ctx, `
		SELECT COUNT(*) FROM user_events
		WHERE user_id = $1 AND event_type = 'subscription_auto_canceled'`,
		fx.UserID).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 0, count, "expected zero subscription_auto_canceled events")

	var cancelAtEnd, isAuto bool
	err = fx.B.Pool().QueryRow(fx.Ctx,
		`SELECT sub_cancel_at_period_end, sub_cancel_is_auto FROM users WHERE id = $1`,
		fx.UserID).Scan(&cancelAtEnd, &isAuto)
	require.NoError(t, err)
	require.False(t, cancelAtEnd, "cache flag sub_cancel_at_period_end must remain false")
	require.False(t, isAuto, "cache flag sub_cancel_is_auto must remain false")
}

// ---------------------------------------------------------------------------
// Happy path
// ---------------------------------------------------------------------------

func TestHandleInvoiceUpcoming_FiresCancel_WhenIdleTwoPeriods(t *testing.T) {
	t.Parallel()
	fx := setupHappyPath(t)

	require.NoError(t, fx.Svc.HandleInvoiceUpcoming(fx.Ctx, fx.Event))

	// Stripe Update was called with cancel_at_period_end=true.
	require.Len(t, fx.Fake.updateCalls, 1)
	require.True(t, fx.Fake.updateCalls[0].CancelAtPeriodEnd)
	require.Equal(t, fx.Event.ID, fx.Fake.updateCalls[0].IdempotencyKey)

	// user_events row exists.
	var count int
	err := fx.B.Pool().QueryRow(fx.Ctx, `
		SELECT COUNT(*) FROM user_events
		WHERE user_id = $1 AND event_type = 'subscription_auto_canceled'`,
		fx.UserID).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)

	// Cache flags flipped.
	var cancelAtEnd, isAuto bool
	err = fx.B.Pool().QueryRow(fx.Ctx,
		`SELECT sub_cancel_at_period_end, sub_cancel_is_auto FROM users WHERE id = $1`,
		fx.UserID).Scan(&cancelAtEnd, &isAuto)
	require.NoError(t, err)
	require.True(t, cancelAtEnd)
	require.True(t, isAuto)

	// Webhook dedup row claimed.
	err = fx.B.Pool().QueryRow(fx.Ctx,
		`SELECT COUNT(*) FROM stripe_webhook_dedup WHERE event_id = $1`, fx.Event.ID).
		Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

// ---------------------------------------------------------------------------
// Early-return tests
// ---------------------------------------------------------------------------

func TestHandleInvoiceUpcoming_SkipsTrialing(t *testing.T) {
	t.Parallel()
	fx := setupHappyPath(t)
	fx.Sub.Status = stripe.SubscriptionStatusTrialing

	require.NoError(t, fx.Svc.HandleInvoiceUpcoming(fx.Ctx, fx.Event))
	requireNoCancel(t, fx)
}

func TestHandleInvoiceUpcoming_SkipsAlreadyCanceled(t *testing.T) {
	t.Parallel()
	fx := setupHappyPath(t)
	fx.Sub.CancelAtPeriodEnd = true

	require.NoError(t, fx.Svc.HandleInvoiceUpcoming(fx.Ctx, fx.Event))
	requireNoCancel(t, fx)
}

func TestHandleInvoiceUpcoming_SkipsGrandfathered(t *testing.T) {
	t.Parallel()
	fx := setupHappyPath(t)
	// Override idle_eligible_after to the future: user just signed up under
	// the new policy and has not yet served a full period of grandfather.
	_, err := fx.B.Pool().Exec(fx.Ctx,
		`UPDATE users SET idle_eligible_after = NOW() + INTERVAL '1 month' WHERE id = $1`,
		fx.UserID)
	require.NoError(t, err)

	require.NoError(t, fx.Svc.HandleInvoiceUpcoming(fx.Ctx, fx.Event))
	requireNoCancel(t, fx)
}

func TestHandleInvoiceUpcoming_DedupesRetryStorm(t *testing.T) {
	t.Parallel()
	fx := setupHappyPath(t)

	require.NoError(t, fx.Svc.HandleInvoiceUpcoming(fx.Ctx, fx.Event))
	require.NoError(t, fx.Svc.HandleInvoiceUpcoming(fx.Ctx, fx.Event))

	// Only one update call total — the second invocation hit the webhook dedup.
	require.Len(t, fx.Fake.updateCalls, 1)

	// Exactly one auto-cancel event row.
	var count int
	err := fx.B.Pool().QueryRow(fx.Ctx, `
		SELECT COUNT(*) FROM user_events
		WHERE user_id = $1 AND event_type = 'subscription_auto_canceled'`,
		fx.UserID).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)

	// One webhook dedup row.
	err = fx.B.Pool().QueryRow(fx.Ctx,
		`SELECT COUNT(*) FROM stripe_webhook_dedup WHERE event_id = $1`, fx.Event.ID).
		Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func TestHandleInvoiceUpcoming_DedupesPostKeep(t *testing.T) {
	t.Parallel()
	fx := setupHappyPath(t)

	// Pre-insert a subscription_kept row for THIS sub + period. The user
	// has already kept their sub for this period; we must not re-cancel
	// even when a fresh invoice.upcoming arrives.
	mdJSON := `{` +
		`"subscription_id":"` + fx.SubID + `",` +
		`"via":"link",` +
		`"current_period_start":"` + fx.PeriodStart.UTC().Format(time.RFC3339Nano) + `"` +
		`}`
	_, err := fx.B.Pool().Exec(fx.Ctx, `
		INSERT INTO user_events (user_id, event_type, metadata)
		VALUES ($1, 'subscription_kept', $2::jsonb)`, fx.UserID, mdJSON)
	require.NoError(t, err)

	require.NoError(t, fx.Svc.HandleInvoiceUpcoming(fx.Ctx, fx.Event))

	// No update call.
	require.Empty(t, fx.Fake.updateCalls)
	// No new auto-cancel rows.
	var count int
	err = fx.B.Pool().QueryRow(fx.Ctx, `
		SELECT COUNT(*) FROM user_events
		WHERE user_id = $1 AND event_type = 'subscription_auto_canceled'`,
		fx.UserID).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 0, count)
}

func TestHandleInvoiceUpcoming_NoActivityHistory(t *testing.T) {
	t.Parallel()
	fx := setupHappyPath(t)

	// Wipe auth_sessions for this user — they signed up but never
	// established a session. Defensive path per spec §8.1.
	_, err := fx.B.Pool().Exec(fx.Ctx,
		`DELETE FROM auth_sessions WHERE user_id = $1`, fx.UserID)
	require.NoError(t, err)

	require.NoError(t, fx.Svc.HandleInvoiceUpcoming(fx.Ctx, fx.Event))
	requireNoCancel(t, fx)
}

func TestHandleInvoiceUpcoming_NotIdleStill(t *testing.T) {
	t.Parallel()
	fx := setupHappyPath(t)

	// Move last_active to NOW (well within the current period) — user is
	// active. Threshold (periodStart - 1mo) is in the past, so an active
	// user fails `lastActive < threshold`.
	_, err := fx.B.Pool().Exec(fx.Ctx,
		`UPDATE auth_sessions SET last_active = NOW() WHERE user_id = $1`, fx.UserID)
	require.NoError(t, err)

	require.NoError(t, fx.Svc.HandleInvoiceUpcoming(fx.Ctx, fx.Event))
	requireNoCancel(t, fx)
}

func TestHandleInvoiceUpcoming_FirstPeriodGrace(t *testing.T) {
	t.Parallel()
	fx := setupHappyPath(t)

	// User signed up moments ago: idle_eligible_after = NOW(). The current
	// period started 23 days ago, so periodStart < idle_eligible_after,
	// triggering the grandfather skip ("first-period grace").
	_, err := fx.B.Pool().Exec(fx.Ctx,
		`UPDATE users SET idle_eligible_after = NOW() WHERE id = $1`, fx.UserID)
	require.NoError(t, err)

	require.NoError(t, fx.Svc.HandleInvoiceUpcoming(fx.Ctx, fx.Event))
	requireNoCancel(t, fx)
}
