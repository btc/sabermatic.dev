package backend_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	stripe "github.com/stripe/stripe-go/v82"

	"github.com/btc/drill/internal/backend"
	"github.com/btc/drill/internal/backendtest"
	"github.com/btc/drill/internal/db"
)

// ---------------------------------------------------------------------------
// CreateSession entitlement enforcement (reservation tested via CreateSession)
// ---------------------------------------------------------------------------

func TestCreateSession_EnforcesBalance(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	userID := backendtest.SeedUser(t, b) // gets 60-min free trial
	questionID := seedQuestion(t, b)

	// Expire the free trial grant so we can control balance precisely.
	_, err := b.Pool().Exec(ctx,
		`UPDATE grants SET expires_at = NOW() - INTERVAL '1 hour' WHERE user_id = $1`, userID)
	require.NoError(t, err)

	// Seed a small purchase grant (10 min) to test insufficient balance.
	seedPurchaseGrant(t, b, userID, 10)

	_, err = b.CreateSession(ctx, backend.CreateSessionParams{
		UserID:          userID,
		QuestionID:      questionID,
		DurationMinutes: 30, // Need 30 but only have 10.
		Plan:            "free",
	})
	require.ErrorIs(t, err, backend.ErrInsufficientBalance)
}

func TestCreateSession_WithSufficientBalance(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	userID := backendtest.SeedUser(t, b)
	questionID := seedQuestion(t, b)

	session, err := b.CreateSession(ctx, backend.CreateSessionParams{
		UserID:          userID,
		QuestionID:      questionID,
		DurationMinutes: 30,
		Plan:            "free",
	})
	require.NoError(t, err)
	assert.Equal(t, int32(30), session.ConfigDurationMinutes)

	// Balance should be 60 - 30 = 30.
	bs, err := db.New(b.Pool()).GetBillingSnapshot(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, int32(30), bs.TotalBalance)
}

func TestCreateSession_EnforcesDurationLimit(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	userID := backendtest.SeedUser(t, b)
	questionID := seedQuestion(t, b)

	// Free plan max is 30 minutes. Request 60.
	_, err := b.CreateSession(ctx, backend.CreateSessionParams{
		UserID:          userID,
		QuestionID:      questionID,
		DurationMinutes: 60,
		Plan:            "free",
	})
	require.ErrorIs(t, err, backend.ErrDurationExceedsPlan)
}

func TestCreateSession_EnforcesConcurrentLimit(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	userID := backendtest.SeedUser(t, b)
	questionID := seedQuestion(t, b)

	// Free plan allows 1 concurrent session. Create one first.
	_, err := b.CreateSession(ctx, backend.CreateSessionParams{
		UserID:          userID,
		QuestionID:      questionID,
		DurationMinutes: 10,
		Plan:            "free",
	})
	require.NoError(t, err)

	// Second session should fail.
	_, err = b.CreateSession(ctx, backend.CreateSessionParams{
		UserID:          userID,
		QuestionID:      questionID,
		DurationMinutes: 10,
		Plan:            "free",
	})
	require.ErrorIs(t, err, backend.ErrConcurrentSessionLimit)
}

// ---------------------------------------------------------------------------
// CompleteSession refund
// ---------------------------------------------------------------------------

func TestCompleteSession_RefundsUnusedMinutes(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	userID := backendtest.SeedUser(t, b)
	questionID := seedQuestion(t, b)

	// Create a 30-min session (reserves 30 from the 60-min grant).
	session, err := b.CreateSession(ctx, backend.CreateSessionParams{
		UserID:          userID,
		QuestionID:      questionID,
		DurationMinutes: 30,
		Plan:            "free",
	})
	require.NoError(t, err)

	// Balance after reservation: 60 - 30 = 30.
	bs, err := db.New(b.Pool()).GetBillingSnapshot(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, int32(30), bs.TotalBalance)

	// Complete immediately (wall-clock ~0 seconds -> actualMinutes = 1).
	// Refund should be 30 - 1 = 29.
	err = b.CompleteSession(ctx, session.ID, 5)
	require.NoError(t, err)

	// Final balance: 30 + 29 = 59 (or 60 - 1 = 59).
	bs, err = db.New(b.Pool()).GetBillingSnapshot(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, int32(59), bs.TotalBalance)
}

// ---------------------------------------------------------------------------
// CancelSession: status, refund, and no eval job
// ---------------------------------------------------------------------------

func TestCancelSession_SetsStatusCancelled(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	userID := backendtest.SeedUser(t, b)
	questionID := seedQuestion(t, b)

	session, err := b.CreateSession(ctx, backend.CreateSessionParams{
		UserID:          userID,
		QuestionID:      questionID,
		DurationMinutes: 30,
		Plan:            "free",
	})
	require.NoError(t, err)

	err = b.CancelSession(ctx, session.ID, 2)
	require.NoError(t, err)

	// Status must be "cancelled", not "completed".
	got, err := db.New(b.Pool()).GetSessionByID(ctx, session.ID)
	require.NoError(t, err)
	assert.Equal(t, "cancelled", got.Status, "CancelSession must set status to 'cancelled'")
	assert.True(t, got.ArchivedAt.Valid, "CancelSession must set archived_at")
	assert.True(t, got.EndedAt.Valid, "CancelSession must set ended_at")
}

func TestCancelSession_RefundsFullReservation(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	userID := backendtest.SeedUser(t, b)
	questionID := seedQuestion(t, b)

	session, err := b.CreateSession(ctx, backend.CreateSessionParams{
		UserID:          userID,
		QuestionID:      questionID,
		DurationMinutes: 30,
		Plan:            "free",
	})
	require.NoError(t, err)

	// Balance after reservation: 60 - 30 = 30.
	bs, err := db.New(b.Pool()).GetBillingSnapshot(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, int32(30), bs.TotalBalance)

	// Cancel immediately — should refund nearly all reserved minutes.
	err = b.CancelSession(ctx, session.ID, 0)
	require.NoError(t, err)

	// Refund should restore balance (30 reserved - 1 min floor = 29 refunded → 59 total).
	bs, err = db.New(b.Pool()).GetBillingSnapshot(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, int32(59), bs.TotalBalance)
}

// ---------------------------------------------------------------------------
// SQL query tests: ReserveMinutes, RefundSessionMinutes, FullRefundSessionMinutes
// ---------------------------------------------------------------------------

func TestReserveMinutes_MultiGrant_FIFO(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	userID := backendtest.SeedUser(t, b) // gets 60-min free trial
	questionID := seedQuestion(t, b)
	q := db.New(b.Pool())

	// Expire the auto-provisioned free trial grant so we control the test scenario.
	_, err := b.Pool().Exec(ctx,
		`UPDATE grants SET expires_at = NOW() - INTERVAL '1 hour' WHERE user_id = $1`, userID)
	require.NoError(t, err)

	// Create two grants: one expiring soon (20 min), one never-expiring (100 min).
	// Use a subscription grant (which has its own source) for the soon-expiring one.
	soonExpiry := pgtype.Timestamptz{Time: time.Now().Add(24 * time.Hour), Valid: true}
	_, err = b.Pool().Exec(ctx,
		`INSERT INTO grants (user_id, source, initial_minutes, remaining_minutes, expires_at)
		 VALUES ($1, 'subscription', 20, 20, $2)`,
		userID, soonExpiry.Time)
	require.NoError(t, err)

	// Purchase grant (never expires).
	err = q.CreatePurchaseGrant(ctx, db.CreatePurchaseGrantParams{
		UserID:        userID,
		StripeEventID: pgtype.Text{String: "evt_test_fifo_" + uuid.New().String(), Valid: true},
		Minutes:       100,
	})
	require.NoError(t, err)
	bs, err := q.GetBillingSnapshot(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, int32(120), bs.TotalBalance) // 20 + 100

	// Reserve 30 — should take all 20 from expiring grant, then 10 from purchase.
	session, err := b.CreateSession(ctx, backend.CreateSessionParams{
		UserID: userID, QuestionID: questionID, DurationMinutes: 30, Plan: "free",
	})
	require.NoError(t, err)
	_ = session

	// Check: purchase grant should have 90 remaining.
	grants, err := q.ListActiveGrants(ctx, userID)
	require.NoError(t, err)
	var purchaseRemaining int32
	for _, g := range grants {
		if g.Source == "purchase" {
			purchaseRemaining = g.RemainingMinutes
		}
	}
	assert.Equal(t, int32(90), purchaseRemaining)

	// Total balance: 120 - 30 = 90.
	bs, err = q.GetBillingSnapshot(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, int32(90), bs.TotalBalance)
}

func TestReserveMinutes_InsufficientBalance(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	userID := backendtest.SeedUser(t, b) // gets 60-min free trial
	questionID := seedQuestion(t, b)

	// Expire the free trial grant so we can control balance precisely.
	_, err := b.Pool().Exec(ctx,
		`UPDATE grants SET expires_at = NOW() - INTERVAL '1 hour' WHERE user_id = $1`, userID)
	require.NoError(t, err)

	// Seed a small purchase grant (10 min) to test insufficient balance.
	seedPurchaseGrant(t, b, userID, 10)

	_, err = b.CreateSession(ctx, backend.CreateSessionParams{
		UserID: userID, QuestionID: questionID, DurationMinutes: 30, Plan: "free",
	})
	require.ErrorIs(t, err, backend.ErrInsufficientBalance)

	// Balance untouched — all-or-nothing.
	bs, err := db.New(b.Pool()).GetBillingSnapshot(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, int32(10), bs.TotalBalance)
}

func TestReserveMinutes_ExpiredGrant(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	userID := backendtest.SeedUser(t, b) // gets 60-min free trial

	// Expire the grant to simulate trial ended.
	_, err := b.Pool().Exec(ctx,
		`UPDATE grants SET expires_at = NOW() - INTERVAL '1 hour' WHERE user_id = $1`, userID)
	require.NoError(t, err)

	questionID := seedQuestion(t, b)

	_, err = b.CreateSession(ctx, backend.CreateSessionParams{
		UserID: userID, QuestionID: questionID, DurationMinutes: 10, Plan: "free",
	})
	require.ErrorIs(t, err, backend.ErrInsufficientBalance)
}

func TestRefundSessionMinutes_WallClock(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	userID := backendtest.SeedUser(t, b)
	questionID := seedQuestion(t, b)

	session, err := b.CreateSession(ctx, backend.CreateSessionParams{
		UserID: userID, QuestionID: questionID, DurationMinutes: 30, Plan: "free",
	})
	require.NoError(t, err)

	// Expected balance is 30 (started with 60, reserved 30).
	bs, err := db.New(b.Pool()).GetBillingSnapshot(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, int32(30), bs.TotalBalance)

	// Complete immediately → actual ~1 min, refund ~29.
	err = b.CompleteSession(ctx, session.ID, 3)
	require.NoError(t, err)

	bs, err = db.New(b.Pool()).GetBillingSnapshot(ctx, userID)
	require.NoError(t, err)
	// Should be close to 59 (60 - 1).
	assert.GreaterOrEqual(t, bs.TotalBalance, int32(58))
}

func TestFullRefundSessionMinutes_FailSession(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	userID := backendtest.SeedUser(t, b)
	questionID := seedQuestion(t, b)

	session, err := b.CreateSession(ctx, backend.CreateSessionParams{
		UserID: userID, QuestionID: questionID, DurationMinutes: 30, Plan: "free",
	})
	require.NoError(t, err)

	// Expected balance is 30 (started with 60, reserved 30).
	bs, err := db.New(b.Pool()).GetBillingSnapshot(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, int32(30), bs.TotalBalance)

	// Fail the session → full refund of all 30 reserved minutes.
	err = b.FailSession(ctx, session.ID)
	require.NoError(t, err)

	// Balance restored to 60.
	bs, err = db.New(b.Pool()).GetBillingSnapshot(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, int32(60), bs.TotalBalance)
}

// ---------------------------------------------------------------------------
// Idempotency tests for refund queries
// ---------------------------------------------------------------------------

func TestRefundSessionMinutes_Idempotent(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	userID := backendtest.SeedUser(t, b)
	questionID := seedQuestion(t, b)

	// Create a 30-min session (reserves 30 from 60-min grant).
	session, err := b.CreateSession(ctx, backend.CreateSessionParams{
		UserID: userID, QuestionID: questionID, DurationMinutes: 30, Plan: "free",
	})
	require.NoError(t, err)

	// Complete immediately → actual ~1 min, refund ~29.
	err = b.CompleteSession(ctx, session.ID, 3)
	require.NoError(t, err)

	// Balance after first refund: should be ~59.
	bs, err := db.New(b.Pool()).GetBillingSnapshot(ctx, userID)
	require.NoError(t, err)
	balanceAfterFirstRefund := bs.TotalBalance
	assert.GreaterOrEqual(t, balanceAfterFirstRefund, int32(58))

	// Artificially reduce the grant's remaining_minutes so there is room for a
	// double-refund to slip through (defeats the remaining <= initial guard).
	_, err = b.Pool().Exec(ctx,
		`UPDATE grants SET remaining_minutes = remaining_minutes - 29 WHERE user_id = $1`, userID)
	require.NoError(t, err)

	// Call RefundSessionMinutes again on the same session — should be a no-op (0 rows).
	rows, err := db.New(b.Pool()).RefundSessionMinutes(ctx, pgtype.UUID{Bytes: session.ID, Valid: true})
	require.NoError(t, err)
	assert.Empty(t, rows, "second call to RefundSessionMinutes should return 0 rows")

	// Balance should be (balanceAfterFirstRefund - 29) — only the manual deduction.
	bs, err = db.New(b.Pool()).GetBillingSnapshot(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, balanceAfterFirstRefund-29, bs.TotalBalance, "balance should not change on duplicate refund")
}

func TestFullRefundSessionMinutes_Idempotent(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	userID := backendtest.SeedUser(t, b)
	questionID := seedQuestion(t, b)

	// Create a 30-min session (reserves 30 from 60-min grant).
	session, err := b.CreateSession(ctx, backend.CreateSessionParams{
		UserID: userID, QuestionID: questionID, DurationMinutes: 30, Plan: "free",
	})
	require.NoError(t, err)

	// Balance after reservation: 60 - 30 = 30.
	bs, err := db.New(b.Pool()).GetBillingSnapshot(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, int32(30), bs.TotalBalance)

	// Full refund — returns all 30 reserved minutes.
	rows, err := db.New(b.Pool()).FullRefundSessionMinutes(ctx, db.FullRefundSessionMinutesParams{
		Reason:    "session_refund",
		SessionID: pgtype.UUID{Bytes: session.ID, Valid: true},
	})
	require.NoError(t, err)
	assert.NotEmpty(t, rows, "first call should return refund rows")

	// Balance restored to 60.
	bs, err = db.New(b.Pool()).GetBillingSnapshot(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, int32(60), bs.TotalBalance)

	// Artificially reduce the grant's remaining_minutes so there is room for a
	// double-refund to slip through (defeats the remaining <= initial guard).
	_, err = b.Pool().Exec(ctx,
		`UPDATE grants SET remaining_minutes = remaining_minutes - 30 WHERE user_id = $1`, userID)
	require.NoError(t, err)

	// Expected balance is 30 (started with 60, reserved 30).
	bs, err = db.New(b.Pool()).GetBillingSnapshot(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, int32(30), bs.TotalBalance)

	// Call FullRefundSessionMinutes again on first session — should be a no-op (0 rows).
	rows, err = db.New(b.Pool()).FullRefundSessionMinutes(ctx, db.FullRefundSessionMinutesParams{
		Reason:    "session_refund",
		SessionID: pgtype.UUID{Bytes: session.ID, Valid: true},
	})
	require.NoError(t, err)
	assert.Empty(t, rows, "second call to FullRefundSessionMinutes should return 0 rows")

	// Balance unchanged at 30 (no double refund).
	bs, err = db.New(b.Pool()).GetBillingSnapshot(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, int32(30), bs.TotalBalance, "balance should not change on duplicate refund")
}

// ---------------------------------------------------------------------------
// Stripe webhook handler integration tests
// ---------------------------------------------------------------------------

// makeEvent constructs a stripe.Event with the given type, ID, and object data.
// GetObjectValue reads from Data.Object (a map[string]interface{}), so we set
// that directly without going through JSON round-tripping.
func makeEvent(eventType, eventID string, object map[string]interface{}) stripe.Event {
	return stripe.Event{
		ID:   eventID,
		Type: stripe.EventType(eventType),
		Data: &stripe.EventData{
			Object: object,
		},
	}
}

// seedUserWithStripeCustomer creates a user, assigns a stripe_customer_id, and
// returns the userID and the customer ID string.
func seedUserWithStripeCustomer(t *testing.T, b *backend.Backend) (uuid.UUID, string) {
	t.Helper()
	userID := backendtest.SeedUser(t, b)
	custID := "cus_test_" + uuid.NewString()[:8]
	err := db.New(b.Pool()).UpdateUserStripeCustomerID(context.Background(), db.UpdateUserStripeCustomerIDParams{
		ID:               userID,
		StripeCustomerID: pgtype.Text{String: custID, Valid: true},
	})
	require.NoError(t, err)
	return userID, custID
}

func TestHandleCheckoutCompleted_CreatesPurchaseGrant(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	userID := backendtest.SeedUser(t, b)
	eventID := "evt_checkout_" + uuid.NewString()[:8]

	event := makeEvent("checkout.session.completed", eventID, map[string]interface{}{
		"mode": "payment",
		"metadata": map[string]interface{}{
			"user_id":      userID.String(),
			"pack_minutes": "120",
		},
	})

	err := b.HandleStripeWebhook(ctx, &event)
	require.NoError(t, err)

	grants, err := db.New(b.Pool()).ListActiveGrants(ctx, userID)
	require.NoError(t, err)
	// User has free trial (from SeedUser) + purchase grant = 2.
	require.Len(t, grants, 2)
	// Find the purchase grant and verify it.
	var purchaseGrant db.ListActiveGrantsRow
	for _, g := range grants {
		if g.Source == "purchase" {
			purchaseGrant = g
		}
	}
	assert.Equal(t, "purchase", purchaseGrant.Source)
	assert.Equal(t, int32(120), purchaseGrant.RemainingMinutes)
}

func TestHandleCheckoutCompleted_IgnoresSubscriptionMode(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	userID := backendtest.SeedUser(t, b)
	eventID := "evt_checkout_sub_" + uuid.NewString()[:8]

	event := makeEvent("checkout.session.completed", eventID, map[string]interface{}{
		"mode": "subscription",
		"metadata": map[string]interface{}{
			"user_id": userID.String(),
		},
	})

	err := b.HandleStripeWebhook(ctx, &event)
	require.NoError(t, err)

	// No additional grants should be created for subscription-mode checkouts.
	// Only the free trial grant from SeedUser should exist.
	grants, err := db.New(b.Pool()).ListActiveGrants(ctx, userID)
	require.NoError(t, err)
	require.Len(t, grants, 1)
	assert.Equal(t, "free_grant", grants[0].Source)
}

func TestHandleCheckoutCompleted_IdempotentOnDuplicateEventID(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	userID := backendtest.SeedUser(t, b)
	eventID := "evt_checkout_idem_" + uuid.NewString()[:8]

	event := makeEvent("checkout.session.completed", eventID, map[string]interface{}{
		"mode": "payment",
		"metadata": map[string]interface{}{
			"user_id":      userID.String(),
			"pack_minutes": "60",
		},
	})

	// Call twice with the same event ID — should be idempotent.
	require.NoError(t, b.HandleStripeWebhook(ctx, &event))
	require.NoError(t, b.HandleStripeWebhook(ctx, &event))

	grants, err := db.New(b.Pool()).ListActiveGrants(ctx, userID)
	require.NoError(t, err)
	// Free trial (from SeedUser) + one purchase grant = 2. Duplicate event should not create a third.
	assert.Len(t, grants, 2, "duplicate stripe event should not create a second purchase grant")
}

func TestHandleInvoicePaid_CreatesSubscriptionGrantAndSetsPlan(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	userID, custID := seedUserWithStripeCustomer(t, b)
	eventID := "evt_invoice_" + uuid.NewString()[:8]

	event := makeEvent("invoice.paid", eventID, map[string]interface{}{
		"customer": custID,
	})

	err := b.HandleStripeWebhook(ctx, &event)
	require.NoError(t, err)

	// Free trial (from SeedUser) + subscription grant = 2.
	grants, err := db.New(b.Pool()).ListActiveGrants(ctx, userID)
	require.NoError(t, err)
	require.Len(t, grants, 2)
	// Find the subscription grant.
	var subGrant db.ListActiveGrantsRow
	for _, g := range grants {
		if g.Source == "subscription" {
			subGrant = g
		}
	}
	assert.Equal(t, "subscription", subGrant.Source)

	// Plan should have been upgraded to "pro".
	user, err := db.New(b.Pool()).GetUserByID(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, "pro", user.Plan)
}

func TestHandleInvoicePaid_UnknownCustomerIsNoOp(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	eventID := "evt_invoice_unknown_" + uuid.NewString()[:8]

	event := makeEvent("invoice.paid", eventID, map[string]interface{}{
		"customer": "cus_doesnotexist",
	})

	// Should return nil (not an error) for an unknown customer.
	err := b.HandleStripeWebhook(ctx, &event)
	require.NoError(t, err)
}

func TestHandleInvoicePaid_IdempotentOnDuplicateEventID(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	userID, custID := seedUserWithStripeCustomer(t, b)
	eventID := "evt_invoice_idem_" + uuid.NewString()[:8]

	event := makeEvent("invoice.paid", eventID, map[string]interface{}{
		"customer": custID,
	})

	require.NoError(t, b.HandleStripeWebhook(ctx, &event))
	require.NoError(t, b.HandleStripeWebhook(ctx, &event))

	grants, err := db.New(b.Pool()).ListActiveGrants(ctx, userID)
	require.NoError(t, err)
	// Free trial + one subscription grant = 2. Duplicate event should not create a third.
	assert.Len(t, grants, 2, "duplicate invoice.paid should not create a second subscription grant")
}

func TestHandleSubscriptionDeleted_SetsPlanToFree(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	userID, custID := seedUserWithStripeCustomer(t, b)

	// Promote the user to "pro" first by setting the plan directly.
	_, err := b.Pool().Exec(ctx,
		`UPDATE users SET plan = 'pro' WHERE id = $1`, userID)
	require.NoError(t, err)

	// Confirm setup.
	user, err := db.New(b.Pool()).GetUserByID(ctx, userID)
	require.NoError(t, err)
	require.Equal(t, "pro", user.Plan)

	eventID := "evt_sub_deleted_" + uuid.NewString()[:8]
	event := makeEvent("customer.subscription.deleted", eventID, map[string]interface{}{
		"customer": custID,
	})

	err = b.HandleStripeWebhook(ctx, &event)
	require.NoError(t, err)

	// Plan should now be "free".
	user, err = db.New(b.Pool()).GetUserByID(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, "free", user.Plan)
}

func TestHandleSubscriptionDeleted_UnknownCustomerIsNoOp(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	eventID := "evt_sub_deleted_unknown_" + uuid.NewString()[:8]
	event := makeEvent("customer.subscription.deleted", eventID, map[string]interface{}{
		"customer": "cus_doesnotexist",
	})

	err := b.HandleStripeWebhook(ctx, &event)
	require.NoError(t, err)
}

func TestHandleSubscriptionUpdated_ActiveStatusKeepsPro(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	userID, custID := seedUserWithStripeCustomer(t, b)

	// Start on free plan.
	user, err := db.New(b.Pool()).GetUserByID(ctx, userID)
	require.NoError(t, err)
	require.Equal(t, "free", user.Plan)

	eventID := "evt_sub_updated_active_" + uuid.NewString()[:8]
	event := makeEvent("customer.subscription.updated", eventID, map[string]interface{}{
		"customer": custID,
		"status":   "active",
	})

	err = b.HandleStripeWebhook(ctx, &event)
	require.NoError(t, err)

	// "active" status → plan should be "pro".
	user, err = db.New(b.Pool()).GetUserByID(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, "pro", user.Plan)
}

func TestHandleSubscriptionUpdated_CanceledStatusDowngradesToFree(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	userID, custID := seedUserWithStripeCustomer(t, b)

	// First upgrade to pro.
	_, err := b.Pool().Exec(ctx, `UPDATE users SET plan = 'pro' WHERE id = $1`, userID)
	require.NoError(t, err)

	eventID := "evt_sub_updated_canceled_" + uuid.NewString()[:8]
	event := makeEvent("customer.subscription.updated", eventID, map[string]interface{}{
		"customer": custID,
		"status":   "canceled",
	})

	err = b.HandleStripeWebhook(ctx, &event)
	require.NoError(t, err)

	user, err := db.New(b.Pool()).GetUserByID(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, "free", user.Plan)
}

func TestHandleSubscriptionUpdated_UnpaidStatusDowngradesToFree(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	userID, custID := seedUserWithStripeCustomer(t, b)

	_, err := b.Pool().Exec(ctx, `UPDATE users SET plan = 'pro' WHERE id = $1`, userID)
	require.NoError(t, err)

	eventID := "evt_sub_updated_unpaid_" + uuid.NewString()[:8]
	event := makeEvent("customer.subscription.updated", eventID, map[string]interface{}{
		"customer": custID,
		"status":   "unpaid",
	})

	err = b.HandleStripeWebhook(ctx, &event)
	require.NoError(t, err)

	user, err := db.New(b.Pool()).GetUserByID(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, "free", user.Plan)
}

func TestHandleSubscriptionUpdated_PastDueStatusDowngradesToFree(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	userID, custID := seedUserWithStripeCustomer(t, b)

	_, err := b.Pool().Exec(ctx, `UPDATE users SET plan = 'pro' WHERE id = $1`, userID)
	require.NoError(t, err)

	eventID := "evt_sub_updated_pastdue_" + uuid.NewString()[:8]
	event := makeEvent("customer.subscription.updated", eventID, map[string]interface{}{
		"customer": custID,
		"status":   "past_due",
	})

	err = b.HandleStripeWebhook(ctx, &event)
	require.NoError(t, err)

	user, err := db.New(b.Pool()).GetUserByID(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, "free", user.Plan)
}

func TestHandleSubscriptionUpdated_UnknownCustomerIsNoOp(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	eventID := "evt_sub_updated_unknown_" + uuid.NewString()[:8]
	event := makeEvent("customer.subscription.updated", eventID, map[string]interface{}{
		"customer": "cus_doesnotexist",
		"status":   "active",
	})

	err := b.HandleStripeWebhook(ctx, &event)
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// Free grant expiry tests
// ---------------------------------------------------------------------------

func TestFreeGrant_ExpiredExcludedFromBalance(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()
	userID := backendtest.SeedUser(t, b) // gets 60-min free trial

	// Expire the grant to simulate trial ended.
	_, err := b.Pool().Exec(ctx,
		`UPDATE grants SET expires_at = NOW() - INTERVAL '1 hour' WHERE user_id = $1`, userID)
	require.NoError(t, err)

	// Balance should be 0 — expired grant doesn't count.
	bs, err := db.New(b.Pool()).GetBillingSnapshot(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, int32(0), bs.TotalBalance)
}

func TestFreeGrant_ExpiredBlocksSessionCreation(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()
	userID := backendtest.SeedUser(t, b) // gets 60-min free trial
	questionID := seedQuestion(t, b)

	// Expire the grant to simulate trial ended.
	_, err := b.Pool().Exec(ctx,
		`UPDATE grants SET expires_at = NOW() - INTERVAL '1 hour' WHERE user_id = $1`, userID)
	require.NoError(t, err)

	// Session creation should fail — no valid balance.
	_, err = b.CreateSession(ctx, backend.CreateSessionParams{
		UserID: userID, QuestionID: questionID, DurationMinutes: 10, Plan: "free",
	})
	require.ErrorIs(t, err, backend.ErrInsufficientBalance)
}

// ---------------------------------------------------------------------------
// One-time grant uniqueness across months
// ---------------------------------------------------------------------------

func TestEnsureFreeGrant_UniquePerUser_AcrossMonths(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()
	userID := backendtest.SeedUser(t, b)

	// Explicit call is a no-op — SeedUser already provisioned the free trial grant.
	err := b.EnsureFreeGrant(ctx, userID)
	require.NoError(t, err)

	bs, err := db.New(b.Pool()).GetBillingSnapshot(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, int32(60), bs.TotalBalance)

	// Manually update the grant's created_at to a previous month to simulate
	// a cross-month scenario. The old monthly index would allow a second grant;
	// the new per-user index should not.
	_, err = b.Pool().Exec(ctx,
		`UPDATE grants SET created_at = created_at - INTERVAL '2 months' WHERE user_id = $1`,
		userID,
	)
	require.NoError(t, err)

	// Second call: should be a no-op (ON CONFLICT DO NOTHING).
	err = b.EnsureFreeGrant(ctx, userID)
	require.NoError(t, err)

	// Balance should still be 60, not 120.
	bs, err = db.New(b.Pool()).GetBillingSnapshot(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, int32(60), bs.TotalBalance, "second EnsureFreeGrant should not create a duplicate grant")
}

// ---------------------------------------------------------------------------
// End-to-end signup → free trial → session creation
// ---------------------------------------------------------------------------

func TestSignupGrantsFreeTrial_EnablesSessionCreation(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	// Sign up a new user (this should auto-create the free trial grant).
	result, err := b.Signup(ctx, backend.SignupParams{
		Email:       "test-e2e@example.com",
		Password:    "securepassword123",
		DisplayName: "E2E Test User",
	})
	require.NoError(t, err)

	// Verify: user has 60-minute free trial balance.
	bs, err := db.New(b.Pool()).GetBillingSnapshot(ctx, result.UserID)
	require.NoError(t, err)
	assert.Equal(t, int32(60), bs.TotalBalance, "signup should provision free trial grant")

	// Verify: user can create a session using their free trial minutes.
	questionID := seedQuestion(t, b)
	session, err := b.CreateSession(ctx, backend.CreateSessionParams{
		UserID: result.UserID, QuestionID: questionID, DurationMinutes: 10, Plan: "free",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, session.ID)

	// Verify: balance reduced by reservation.
	bs, err = db.New(b.Pool()).GetBillingSnapshot(ctx, result.UserID)
	require.NoError(t, err)
	assert.Equal(t, int32(50), bs.TotalBalance, "balance should be reduced by session reservation")
}

func TestEnsureFreeGrant_Idempotent(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	userID := backendtest.SeedUser(t, b)
	q := db.New(b.Pool())

	// Call twice — second should be a no-op.
	err := b.EnsureFreeGrant(ctx, userID)
	require.NoError(t, err)

	err = b.EnsureFreeGrant(ctx, userID)
	require.NoError(t, err)

	// Only one grant should exist.
	grants, err := q.ListActiveGrants(ctx, userID)
	require.NoError(t, err)
	assert.Len(t, grants, 1)

	// Only one ledger entry.
	entries, err := q.GetRecentLedgerEntries(ctx, db.GetRecentLedgerEntriesParams{
		UserID: userID, Limit: 10,
	})
	require.NoError(t, err)
	assert.Len(t, entries, 1)
	assert.Equal(t, "free_trial", entries[0].Reason)
}
