package idleunsub_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
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

	svc := idleunsub.NewService(b.Pool(), fake, nullMailer{}, nil, "http://localhost:3000", silentLogger())

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

	// SetUserAutoCancelState does NOT write stripe_subscription_id (spec §4.1
	// designates SyncSubStateFromWebhook as the single population path).
	// In this test the webhook hasn't fired, so the column remains NULL.
	var subIDCol pgtype.Text
	err = fx.B.Pool().QueryRow(fx.Ctx,
		`SELECT stripe_subscription_id FROM users WHERE id = $1`,
		fx.UserID).Scan(&subIDCol)
	require.NoError(t, err)
	require.False(t, subIDCol.Valid, "spec invariant: HandleInvoiceUpcoming does not write stripe_subscription_id")
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

	// Step 1: First call with evt_1 → fires cancel (1 Stripe update call).
	require.NoError(t, fx.Svc.HandleInvoiceUpcoming(fx.Ctx, fx.Event))
	require.Len(t, fx.Fake.updateCalls, 1)

	// Step 2: Second call with evt_1 → blocked by webhook dedup (same event
	// ID); still exactly 1 update call.
	require.NoError(t, fx.Svc.HandleInvoiceUpcoming(fx.Ctx, fx.Event))
	require.Len(t, fx.Fake.updateCalls, 1)

	// Step 3: Third call with evt_2 (different event ID, same period) →
	// TryClaimWebhookEvent succeeds (new event), but HasAutoCanceledThisPeriod
	// blocks the cancel inside the TX. Still exactly 1 update call.
	evt2 := fx.Event
	evt2.ID = "evt_" + uuid.NewString()[:8]
	require.NoError(t, fx.Svc.HandleInvoiceUpcoming(fx.Ctx, evt2))
	require.Len(t, fx.Fake.updateCalls, 1)

	// Exactly one auto-cancel user_events row (only step 1 wrote it).
	var count int
	err := fx.B.Pool().QueryRow(fx.Ctx, `
		SELECT COUNT(*) FROM user_events
		WHERE user_id = $1 AND event_type = 'subscription_auto_canceled'`,
		fx.UserID).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count, "only one auto-cancel event row expected")

	// evt_1 dedup row persisted (step 1 committed).
	err = fx.B.Pool().QueryRow(fx.Ctx,
		`SELECT COUNT(*) FROM stripe_webhook_dedup WHERE event_id = $1`, fx.Event.ID).
		Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count, "evt_1 dedup row persisted from step 1")

	// evt_2 dedup row is NOT persisted: TryClaimWebhookEvent runs inside the
	// same TX that gets rolled back (no commit occurs) when
	// HasAutoCanceledThisPeriod returns true. The dedup insert is in-flight
	// only — it rolls back with the rest of the TX.
	err = fx.B.Pool().QueryRow(fx.Ctx,
		`SELECT COUNT(*) FROM stripe_webhook_dedup WHERE event_id = $1`, evt2.ID).
		Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 0, count, "evt_2 dedup row rolled back with the TX")

	// Total dedup rows: only evt_1.
	err = fx.B.Pool().QueryRow(fx.Ctx,
		`SELECT COUNT(*) FROM stripe_webhook_dedup`).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count, "only one dedup row persisted total")
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

// ---------------------------------------------------------------------------
// KeepSubscription / AutoReverse fixtures
// ---------------------------------------------------------------------------

// setupAutoCanceledState seeds a user in the "auto-canceled" cache state. We
// use raw SQL UPDATE per the spec's "start from real state and mutate" rule:
// SeedUser yields a real user row; we then mutate the cache columns to the
// post-cancel shape we'd see after HandleInvoiceUpcoming + the
// SyncSubStateFromWebhook follow-up. This is appropriate for unit-testing
// the reversal paths in isolation.
//
// Returns a fixture ready for KeepSubscription/AutoReverse calls. The fake
// Stripe sub mirrors the cached state (CancelAtPeriodEnd=true) by default;
// individual tests mutate fx.Sub before calling AutoReverse to simulate
// drift.
func setupAutoCanceledState(t *testing.T) *fixture {
	t.Helper()
	fx := setupHappyPath(t)

	// Mutate the user's cache columns to the auto-canceled shape and write
	// the stripe_subscription_id (in production written by
	// SyncSubStateFromWebhook on customer.subscription.updated).
	_, err := fx.B.Pool().Exec(fx.Ctx, `
		UPDATE users
		SET sub_cancel_at_period_end = TRUE,
		    sub_cancel_is_auto       = TRUE,
		    stripe_subscription_id   = $1,
		    sub_current_period_start = $2,
		    pending_kept_banner      = FALSE
		WHERE id = $3`, fx.SubID, fx.PeriodStart, fx.UserID)
	require.NoError(t, err)

	// The fake Stripe sub should match: it's in the canceled state too.
	fx.Sub.CancelAtPeriodEnd = true

	return fx
}

// readUserCache returns the auto-cancel cache columns for a user.
func readUserCache(t *testing.T, fx *fixture) (cancelAtEnd, isAuto, banner bool) {
	t.Helper()
	err := fx.B.Pool().QueryRow(fx.Ctx,
		`SELECT sub_cancel_at_period_end, sub_cancel_is_auto, pending_kept_banner
		 FROM users WHERE id = $1`,
		fx.UserID).Scan(&cancelAtEnd, &isAuto, &banner)
	require.NoError(t, err)
	return
}

// countKeptEvents returns the number of subscription_kept rows for a user.
func countKeptEvents(t *testing.T, fx *fixture) int {
	t.Helper()
	var count int
	err := fx.B.Pool().QueryRow(fx.Ctx, `
		SELECT COUNT(*) FROM user_events
		WHERE user_id = $1 AND event_type = 'subscription_kept'`,
		fx.UserID).Scan(&count)
	require.NoError(t, err)
	return count
}

// keepClaims constructs a KeepTokenClaims for fx. The endpoint constructs
// these from a verified token; here we synthesize directly since
// KeepSubscription's contract is "endpoint already verified the token".
func keepClaims(fx *fixture) idleunsub.KeepTokenClaims {
	return idleunsub.KeepTokenClaims{
		UserID:           fx.UserID,
		SubscriptionID:   fx.SubID,
		Action:           "keep_subscription",
		CurrentPeriodEnd: fx.PeriodEnd,
		IssuedAt:         fx.PeriodStart.Unix(),
		ExpiresAt:        fx.PeriodEnd.Unix(),
	}
}

// ---------------------------------------------------------------------------
// KeepSubscription tests
// ---------------------------------------------------------------------------

func TestKeepSubscription_HappyPath(t *testing.T) {
	t.Parallel()
	fx := setupAutoCanceledState(t)

	require.NoError(t, fx.Svc.KeepSubscription(fx.Ctx, keepClaims(fx)))

	// Stripe Update called once, with cancel_at_period_end=false.
	require.Len(t, fx.Fake.updateCalls, 1)
	require.Equal(t, fx.SubID, fx.Fake.updateCalls[0].ID)
	require.False(t, fx.Fake.updateCalls[0].CancelAtPeriodEnd)
	require.Empty(t, fx.Fake.updateCalls[0].IdempotencyKey,
		"KeepSubscription passes empty idempotency key (Stripe treats as non-idempotent)")

	// Cache flipped, banner set.
	cancelAtEnd, isAuto, banner := readUserCache(t, fx)
	require.False(t, cancelAtEnd)
	require.False(t, isAuto)
	require.True(t, banner, "KeepSubscription is a real reversal: banner must be set")

	// One subscription_kept event row with via='link'.
	require.Equal(t, 1, countKeptEvents(t, fx))
	var via, gotSubID string
	err := fx.B.Pool().QueryRow(fx.Ctx, `
		SELECT metadata->>'via', metadata->>'subscription_id'
		FROM user_events
		WHERE user_id = $1 AND event_type = 'subscription_kept'`,
		fx.UserID).Scan(&via, &gotSubID)
	require.NoError(t, err)
	require.Equal(t, "link", via)
	require.Equal(t, fx.SubID, gotSubID)
}

func TestKeepSubscription_RefusesManualCancel(t *testing.T) {
	t.Parallel()
	fx := setupAutoCanceledState(t)

	// Flip sub_cancel_is_auto OFF: this is now a manual portal cancel that
	// happens to share the cache flag layout. KeepSubscription must refuse.
	_, err := fx.B.Pool().Exec(fx.Ctx,
		`UPDATE users SET sub_cancel_is_auto = FALSE WHERE id = $1`, fx.UserID)
	require.NoError(t, err)

	require.NoError(t, fx.Svc.KeepSubscription(fx.Ctx, keepClaims(fx)))

	// Zero Stripe calls.
	require.Empty(t, fx.Fake.updateCalls, "must not touch Stripe on a manual cancel")
	// Zero new event rows.
	require.Equal(t, 0, countKeptEvents(t, fx))
	// Cache unchanged: still cancel_at_period_end=true, is_auto=false (we set
	// it false above), banner false.
	var cancelAtEnd, isAuto, banner bool
	err = fx.B.Pool().QueryRow(fx.Ctx,
		`SELECT sub_cancel_at_period_end, sub_cancel_is_auto, pending_kept_banner
		 FROM users WHERE id = $1`, fx.UserID).Scan(&cancelAtEnd, &isAuto, &banner)
	require.NoError(t, err)
	require.True(t, cancelAtEnd, "manual-cancel cache flag must be left intact")
	require.False(t, isAuto)
	require.False(t, banner)
}

func TestKeepSubscription_Idempotent(t *testing.T) {
	t.Parallel()
	fx := setupAutoCanceledState(t)

	// First call: real reversal.
	require.NoError(t, fx.Svc.KeepSubscription(fx.Ctx, keepClaims(fx)))
	// Second call: gates already cleared after first → must be a no-op.
	require.NoError(t, fx.Svc.KeepSubscription(fx.Ctx, keepClaims(fx)))

	// Exactly one Stripe call, exactly one event row.
	require.Len(t, fx.Fake.updateCalls, 1, "second call must not re-hit Stripe")
	require.Equal(t, 1, countKeptEvents(t, fx), "second call must not re-insert event")

	// TODO(Task 9): assert mEmailEnqueue counter == 1 once the real mailer wires up.
	// The plan promises "exactly one email enqueued"; with the current stub this
	// can't be verified without inspecting the OTEL counter directly.
}

// ---------------------------------------------------------------------------
// AutoReverse tests
// ---------------------------------------------------------------------------

func TestAutoReverse_GateOff(t *testing.T) {
	t.Parallel()
	// Plain happy-path user: sub_cancel_at_period_end=false. AutoReverse is
	// a no-op.
	fx := setupHappyPath(t)

	require.NoError(t, fx.Svc.AutoReverse(fx.Ctx, fx.UserID))

	require.Empty(t, fx.Fake.updateCalls, "gate off → no Stripe calls")
	// Note: setupHappyPath does not call GetSubscription either; AutoReverse
	// must early-return before any Stripe round-trip.
	require.Equal(t, 0, countKeptEvents(t, fx))
}

func TestAutoReverse_HappyPath(t *testing.T) {
	t.Parallel()
	fx := setupAutoCanceledState(t)

	require.NoError(t, fx.Svc.AutoReverse(fx.Ctx, fx.UserID))

	// Exactly one Stripe Update call, with cancel_at_period_end=false.
	require.Len(t, fx.Fake.updateCalls, 1)
	require.Equal(t, fx.SubID, fx.Fake.updateCalls[0].ID)
	require.False(t, fx.Fake.updateCalls[0].CancelAtPeriodEnd)

	// Cache cleared with banner=true.
	cancelAtEnd, isAuto, banner := readUserCache(t, fx)
	require.False(t, cancelAtEnd)
	require.False(t, isAuto)
	require.True(t, banner, "real reversal must set banner")

	// One subscription_kept row with via='auto_activity'.
	require.Equal(t, 1, countKeptEvents(t, fx))
	var via string
	err := fx.B.Pool().QueryRow(fx.Ctx, `
		SELECT metadata->>'via' FROM user_events
		WHERE user_id = $1 AND event_type = 'subscription_kept'`,
		fx.UserID).Scan(&via)
	require.NoError(t, err)
	require.Equal(t, "auto_activity", via)
}

func TestAutoReverse_CacheDriftCorrected(t *testing.T) {
	t.Parallel()
	fx := setupAutoCanceledState(t)

	// Stripe says NOT canceled (the auto-cancel webhook side never landed,
	// or was reversed out-of-band). Cache says canceled. AutoReverse must
	// silently reconcile without celebrating a reversal.
	fx.Sub.CancelAtPeriodEnd = false

	require.NoError(t, fx.Svc.AutoReverse(fx.Ctx, fx.UserID))

	// ZERO Stripe Update calls — only the GetSubscription read.
	require.Empty(t, fx.Fake.updateCalls, "drift correction must not hit Stripe Update")

	// Cache silently cleared.
	cancelAtEnd, isAuto, banner := readUserCache(t, fx)
	require.False(t, cancelAtEnd)
	require.False(t, isAuto)
	require.False(t, banner, "drift correction must NOT set the banner")

	// No subscription_kept event row (no real reversal happened).
	require.Equal(t, 0, countKeptEvents(t, fx))
}

func TestAutoReverse_RefusesManualCancel(t *testing.T) {
	t.Parallel()
	fx := setupAutoCanceledState(t)

	// Flip sub_cancel_is_auto OFF: this is a manual portal cancel.
	// AutoReverse must early-return without any Stripe calls.
	_, err := fx.B.Pool().Exec(fx.Ctx,
		`UPDATE users SET sub_cancel_is_auto = FALSE WHERE id = $1`, fx.UserID)
	require.NoError(t, err)

	require.NoError(t, fx.Svc.AutoReverse(fx.Ctx, fx.UserID))

	// Zero Stripe calls of any kind.
	require.Empty(t, fx.Fake.updateCalls)
	// Cache unchanged.
	var cancelAtEnd, isAuto, banner bool
	err = fx.B.Pool().QueryRow(fx.Ctx,
		`SELECT sub_cancel_at_period_end, sub_cancel_is_auto, pending_kept_banner
		 FROM users WHERE id = $1`, fx.UserID).Scan(&cancelAtEnd, &isAuto, &banner)
	require.NoError(t, err)
	require.True(t, cancelAtEnd)
	require.False(t, isAuto)
	require.False(t, banner)
	require.Equal(t, 0, countKeptEvents(t, fx))
}

func TestAutoReverse_StripeUpdateFails(t *testing.T) {
	t.Parallel()
	fx := setupAutoCanceledState(t)

	// Configure fakeStripe to fail on the next Update call.
	fx.Fake.updateErr = errors.New("stripe down")

	err := fx.Svc.AutoReverse(fx.Ctx, fx.UserID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "stripe down")

	// Gates must remain ON — the next call retries (self-healing).
	cancelAtEnd, isAuto, banner := readUserCache(t, fx)
	require.True(t, cancelAtEnd, "gates must remain ON after Stripe failure for self-heal")
	require.True(t, isAuto)
	require.False(t, banner, "banner must NOT be set on failure")

	// No subscription_kept rows because we returned before DB writes.
	require.Equal(t, 0, countKeptEvents(t, fx))
}
