package backend_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
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
	"github.com/btc/drill/internal/email"
	"github.com/btc/drill/internal/feat/idleunsub"
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

	// Balance: 60 - 30 = 30.
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

	// Balance: 60 - 30 = 30.
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

	// Balance: 60 - 30 = 30.
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
// that directly. Data.Raw is also populated by JSON-encoding the object map
// so handlers that unmarshal the typed object (e.g. handleSubscriptionUpdated)
// see a faithful payload — production webhooks have both Object and Raw set.
func makeEvent(eventType, eventID string, object map[string]interface{}) stripe.Event {
	raw, err := json.Marshal(object)
	if err != nil {
		panic(fmt.Sprintf("makeEvent: marshal object: %v", err))
	}
	return stripe.Event{
		ID:   eventID,
		Type: stripe.EventType(eventType),
		Data: &stripe.EventData{
			Object: object,
			Raw:    raw,
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

	err := b.HandleStripeWebhook(ctx, event)
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

	err := b.HandleStripeWebhook(ctx, event)
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
	require.NoError(t, b.HandleStripeWebhook(ctx, event))
	require.NoError(t, b.HandleStripeWebhook(ctx, event))

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

	err := b.HandleStripeWebhook(ctx, event)
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
	err := b.HandleStripeWebhook(ctx, event)
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

	require.NoError(t, b.HandleStripeWebhook(ctx, event))
	require.NoError(t, b.HandleStripeWebhook(ctx, event))

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

	err = b.HandleStripeWebhook(ctx, event)
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

	err := b.HandleStripeWebhook(ctx, event)
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

	err = b.HandleStripeWebhook(ctx, event)
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

	err = b.HandleStripeWebhook(ctx, event)
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

	err = b.HandleStripeWebhook(ctx, event)
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

	err = b.HandleStripeWebhook(ctx, event)
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

	err := b.HandleStripeWebhook(ctx, event)
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// Idle auto-cancel webhook integration tests (Task 10).
//
// These tests exercise the full Backend.HandleStripeWebhook dispatch:
//   - invoice.upcoming routes to idleunsub.Service.HandleInvoiceUpcoming.
//   - customer.subscription.created routes to handleSubscriptionUpdated
//     (cache seed on first event).
//   - customer.subscription.updated extends to call SyncSubStateFromWebhook
//     after the plan update.
//   - customer.subscription.deleted now uses ClearSubStateOnDeletion (clears
//     stripe_subscription_id, sub_cancel_at_period_end, sub_cancel_is_auto,
//     sub_current_period_start, and sets plan='free' in one query).
// ---------------------------------------------------------------------------

// fakeBillingStripe is a local in-memory StripeClient for backend tests.
// idleunsub_test has its own fakeStripe but it lives in the idleunsub_test
// package and is unexported. We reproduce a minimal version here so this
// _test.go file stays self-contained.
type fakeBillingStripe struct {
	subs        map[string]*stripe.Subscription
	updateCalls []fakeBillingUpdateCall
}

type fakeBillingUpdateCall struct {
	ID                string
	CancelAtPeriodEnd bool
	IdempotencyKey    string
}

var errFakeStripeNotFound = errors.New("fakeBillingStripe: subscription not found")

func (f *fakeBillingStripe) GetSubscription(_ context.Context, id string) (*stripe.Subscription, error) {
	if s, ok := f.subs[id]; ok {
		return s, nil
	}
	return nil, errFakeStripeNotFound
}

func (f *fakeBillingStripe) UpdateSubscriptionCancel(_ context.Context, id string, cancelAtEnd bool, key string) (*stripe.Subscription, error) {
	f.updateCalls = append(f.updateCalls, fakeBillingUpdateCall{id, cancelAtEnd, key})
	if s, ok := f.subs[id]; ok {
		s.CancelAtPeriodEnd = cancelAtEnd
		return s, nil
	}
	return nil, errFakeStripeNotFound
}

var _ idleunsub.StripeClient = (*fakeBillingStripe)(nil)

// nullBillingMailer drops all sends. The cancel-decision flow is best-effort
// on email; tests assert on DB state and Stripe-call effects, not delivery.
type nullBillingMailer struct{}

func (nullBillingMailer) Send(_ context.Context, _ email.Message) error { return nil }

var _ email.Sender = nullBillingMailer{}

// makeIdleunsubOverride builds an idleunsub.Service backed by a caller-supplied
// fake StripeClient and applies it via ApplyTestOverrides. Returns the fake so
// callers can assert on calls made into Stripe.
func makeIdleunsubOverride(t *testing.T, b *backend.Backend, fake *fakeBillingStripe) {
	t.Helper()
	var key [32]byte
	_, err := rand.Read(key[:])
	require.NoError(t, err)
	signer := idleunsub.NewTokenSigner(key[:])
	svc := idleunsub.NewService(
		b.Pool(),
		fake,
		nullBillingMailer{},
		signer,
		"http://localhost:3000",
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	b.ApplyTestOverrides(backend.TestOverrides{Idleunsub: svc})
}

// makeSubscriptionEvent constructs a Stripe webhook event whose Data.Raw is
// the JSON encoding of the *stripe.Subscription. Object is also populated
// (just the customer key) so handlers that read GetObjectValue still work.
// Mirrors how stripe-go decodes a real webhook payload.
func makeSubscriptionEvent(eventType, eventID string, sub *stripe.Subscription, status string) stripe.Event {
	raw, err := json.Marshal(sub)
	if err != nil {
		panic(fmt.Sprintf("makeSubscriptionEvent: marshal: %v", err))
	}
	custID := ""
	if sub.Customer != nil {
		custID = sub.Customer.ID
	}
	return stripe.Event{
		ID:   eventID,
		Type: stripe.EventType(eventType),
		Data: &stripe.EventData{
			Object: map[string]interface{}{
				"customer": custID,
				"status":   status,
			},
			Raw: raw,
		},
	}
}

// TestWebhookSwitch_InvoiceUpcoming_RoutesToIdleunsub asserts that the
// dispatch in Backend.HandleStripeWebhook actually reaches idleunsub for
// invoice.upcoming events. We inject a fake-Stripe-backed Service via
// TestOverrides and fire an event matching the fake's seeded subscription.
func TestWebhookSwitch_InvoiceUpcoming_RoutesToIdleunsub(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	// Seed an idle Pro user wired to a Stripe customer + subscription.
	userID, custID := seedUserWithStripeCustomer(t, b)
	subID := "sub_test_" + uuid.NewString()[:8]
	_, err := b.Pool().Exec(ctx, `
		UPDATE users
		SET plan                = 'pro',
		    idle_eligible_after = NOW() - INTERVAL '6 months'
		WHERE id = $1`, userID)
	require.NoError(t, err)
	_, err = b.Pool().Exec(ctx, `
		INSERT INTO auth_sessions (user_id, token_hash, expires_at, last_active)
		VALUES ($1, $2, NOW() + INTERVAL '30 days', NOW() - INTERVAL '3 months')`,
		userID, "tok_test_"+uuid.NewString())
	require.NoError(t, err)

	// Build the fake Stripe subscription. Period started 23 days ago,
	// monthly billing — threshold (= periodStart - 1 month) sits ~53 days
	// ago, so a 3-months-ago last_active is "before" it: cancel fires.
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
	fake := &fakeBillingStripe{subs: map[string]*stripe.Subscription{subID: sub}}
	makeIdleunsubOverride(t, b, fake)

	eventID := "evt_invoice_upcoming_" + uuid.NewString()[:8]
	event := stripe.Event{
		ID:   eventID,
		Type: "invoice.upcoming",
		Data: &stripe.EventData{
			Object: map[string]interface{}{"subscription": subID},
		},
	}

	require.NoError(t, b.HandleStripeWebhook(ctx, event))

	// idleunsub fired the cancel through the fake Stripe client.
	require.Len(t, fake.updateCalls, 1, "expected exactly one Stripe update call")
	assert.Equal(t, subID, fake.updateCalls[0].ID)
	assert.True(t, fake.updateCalls[0].CancelAtPeriodEnd)
	assert.Equal(t, eventID, fake.updateCalls[0].IdempotencyKey)

	// Audit row landed in user_events.
	var count int
	err = b.Pool().QueryRow(ctx, `
		SELECT COUNT(*) FROM user_events
		WHERE user_id = $1 AND event_type = 'subscription_auto_canceled'`,
		userID).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

// TestHandleSubscriptionUpdated_SyncsSubStateCache verifies that, after the
// plan-update step, the handler also syncs stripe_subscription_id,
// sub_cancel_at_period_end, and sub_current_period_start from the webhook
// payload (Spec §6: this is the single sole population path).
func TestHandleSubscriptionUpdated_SyncsSubStateCache(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	userID, custID := seedUserWithStripeCustomer(t, b)

	subID := "sub_sync_" + uuid.NewString()[:8]
	periodStart := time.Now().Add(-24 * time.Hour).UTC().Truncate(time.Second)
	sub := &stripe.Subscription{
		ID:                subID,
		Status:            stripe.SubscriptionStatusActive,
		CancelAtPeriodEnd: true,
		Customer:          &stripe.Customer{ID: custID},
		Items: &stripe.SubscriptionItemList{Data: []*stripe.SubscriptionItem{{
			CurrentPeriodStart: periodStart.Unix(),
		}}},
	}
	event := makeSubscriptionEvent("customer.subscription.updated",
		"evt_sync_"+uuid.NewString()[:8], sub, "active")

	require.NoError(t, b.HandleStripeWebhook(ctx, event))

	var (
		gotSubID            pgtype.Text
		gotCancelAtEnd      bool
		gotPeriodStart      pgtype.Timestamptz
	)
	err := b.Pool().QueryRow(ctx, `
		SELECT stripe_subscription_id, sub_cancel_at_period_end, sub_current_period_start
		FROM users WHERE id = $1`, userID).Scan(&gotSubID, &gotCancelAtEnd, &gotPeriodStart)
	require.NoError(t, err)
	require.True(t, gotSubID.Valid, "stripe_subscription_id should be populated")
	assert.Equal(t, subID, gotSubID.String)
	assert.True(t, gotCancelAtEnd, "sub_cancel_at_period_end should be synced from event")
	require.True(t, gotPeriodStart.Valid)
	assert.WithinDuration(t, periodStart, gotPeriodStart.Time, time.Second)
}

// TestHandleSubscriptionUpdated_DoesNotOverwriteSubCancelIsAuto guards spec
// §6's invariant: SyncSubStateFromWebhook must NOT touch sub_cancel_is_auto.
// Only idleunsub.HandleInvoiceUpcoming sets that flag, and a webhook race
// must not flip it back to false.
func TestHandleSubscriptionUpdated_DoesNotOverwriteSubCancelIsAuto(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	userID, custID := seedUserWithStripeCustomer(t, b)

	// Pretend an idle auto-cancel just landed: sub_cancel_is_auto = true.
	_, err := b.Pool().Exec(ctx, `
		UPDATE users
		SET sub_cancel_at_period_end = TRUE,
		    sub_cancel_is_auto       = TRUE
		WHERE id = $1`, userID)
	require.NoError(t, err)

	subID := "sub_isauto_" + uuid.NewString()[:8]
	periodStart := time.Now().Add(-24 * time.Hour).UTC().Truncate(time.Second)
	sub := &stripe.Subscription{
		ID:                subID,
		Customer:          &stripe.Customer{ID: custID},
		CancelAtPeriodEnd: true,
		Items: &stripe.SubscriptionItemList{Data: []*stripe.SubscriptionItem{{
			CurrentPeriodStart: periodStart.Unix(),
		}}},
	}
	event := makeSubscriptionEvent("customer.subscription.updated",
		"evt_isauto_"+uuid.NewString()[:8], sub, "active")

	require.NoError(t, b.HandleStripeWebhook(ctx, event))

	var isAuto bool
	err = b.Pool().QueryRow(ctx,
		`SELECT sub_cancel_is_auto FROM users WHERE id = $1`, userID).Scan(&isAuto)
	require.NoError(t, err)
	assert.True(t, isAuto, "sub_cancel_is_auto must not be overwritten by webhook sync")
}

// TestHandleSubscriptionCreated_SeedsCache verifies the new dispatch case:
// a customer.subscription.created event reuses handleSubscriptionUpdated and
// seeds the cache columns on the very first event (rather than waiting for a
// later update).
func TestHandleSubscriptionCreated_SeedsCache(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	userID, custID := seedUserWithStripeCustomer(t, b)

	subID := "sub_create_" + uuid.NewString()[:8]
	periodStart := time.Now().Add(-1 * time.Minute).UTC().Truncate(time.Second)
	sub := &stripe.Subscription{
		ID:                subID,
		Status:            stripe.SubscriptionStatusActive,
		CancelAtPeriodEnd: false,
		Customer:          &stripe.Customer{ID: custID},
		Items: &stripe.SubscriptionItemList{Data: []*stripe.SubscriptionItem{{
			CurrentPeriodStart: periodStart.Unix(),
		}}},
	}
	event := makeSubscriptionEvent("customer.subscription.created",
		"evt_create_"+uuid.NewString()[:8], sub, "active")

	require.NoError(t, b.HandleStripeWebhook(ctx, event))

	user, err := db.New(b.Pool()).GetUserByID(ctx, userID)
	require.NoError(t, err)
	require.True(t, user.StripeSubscriptionID.Valid)
	assert.Equal(t, subID, user.StripeSubscriptionID.String)
	assert.False(t, user.SubCancelAtPeriodEnd)
	assert.Equal(t, "pro", user.Plan, "subscription.created with active status upgrades to pro")
}

// TestHandleSubscriptionDeleted_ClearsSubStateAndPlan replaces the original
// "sets plan to free" behavior with the new ClearSubStateOnDeletion call,
// which clears all sub-state cache columns AND downgrades the plan in a
// single statement.
func TestHandleSubscriptionDeleted_ClearsSubStateAndPlan(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	userID, custID := seedUserWithStripeCustomer(t, b)

	// Seed a fully-populated sub state to simulate an active Pro subscriber
	// who is then deleted.
	_, err := b.Pool().Exec(ctx, `
		UPDATE users
		SET plan                     = 'pro',
		    stripe_subscription_id   = $1,
		    sub_cancel_at_period_end = TRUE,
		    sub_cancel_is_auto       = TRUE,
		    sub_current_period_start = NOW() - INTERVAL '1 day'
		WHERE id = $2`, "sub_to_delete_"+uuid.NewString()[:8], userID)
	require.NoError(t, err)

	event := makeEvent("customer.subscription.deleted",
		"evt_sub_deleted_clear_"+uuid.NewString()[:8],
		map[string]interface{}{"customer": custID})

	require.NoError(t, b.HandleStripeWebhook(ctx, event))

	user, err := db.New(b.Pool()).GetUserByID(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, "free", user.Plan)
	assert.False(t, user.StripeSubscriptionID.Valid, "stripe_subscription_id should be cleared")
	assert.False(t, user.SubCancelAtPeriodEnd, "sub_cancel_at_period_end should be cleared")
	assert.False(t, user.SubCancelIsAuto, "sub_cancel_is_auto should be cleared")
	assert.False(t, user.SubCurrentPeriodStart.Valid, "sub_current_period_start should be cleared")
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
