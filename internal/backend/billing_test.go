package backend

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/db"
)

// ---------------------------------------------------------------------------
// Billing-specific seed helpers
// ---------------------------------------------------------------------------

// seedFreeGrant creates a free grant with the given minutes, expiring at the
// end of the current month.
func seedFreeGrant(t *testing.T, b *Backend, userID uuid.UUID, minutes int32) {
	t.Helper()
	err := db.New(b.pool).EnsureFreeGrant(context.Background(), db.EnsureFreeGrantParams{
		UserID:         userID,
		InitialMinutes: minutes,
		ExpiresAt:      pgtype.Timestamptz{Time: time.Date(2099, 12, 31, 0, 0, 0, 0, time.UTC), Valid: true},
	})
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// CreateSession entitlement enforcement (reservation tested via CreateSession)
// ---------------------------------------------------------------------------

func TestCreateSession_EnforcesBalance(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	userID := seedUser(t, b)
	questionID := seedQuestion(t, b)

	// No grants created (the free grant from EnsureFreeGrant gives 60 min).
	// Request a session longer than the free plan max (30 min) to test
	// ErrDurationExceedsPlan, then request within plan max but with 0 balance.
	// First, exhaust balance by reserving all 60 free minutes via a direct session.
	seedFreeGrant(t, b, userID, 10) // Only 10 minutes available.

	_, err := b.CreateSession(ctx, CreateSessionParams{
		UserID:          userID,
		QuestionID:      questionID,
		DurationMinutes: 30, // Need 30 but only have 10.
		Plan:            "free",
	})
	require.ErrorIs(t, err, ErrInsufficientBalance)
}

func TestCreateSession_WithSufficientBalance(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	userID := seedUser(t, b)
	questionID := seedQuestion(t, b)
	seedFreeGrant(t, b, userID, 60)

	session, err := b.CreateSession(ctx, CreateSessionParams{
		UserID:          userID,
		QuestionID:      questionID,
		DurationMinutes: 30,
		Plan:            "free",
	})
	require.NoError(t, err)
	assert.Equal(t, int32(30), session.ConfigDurationMinutes)

	// Balance should be 60 - 30 = 30.
	bs, err := db.New(b.pool).GetBillingSnapshot(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, int32(30), bs.TotalBalance)
}

func TestCreateSession_EnforcesDurationLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	userID := seedUser(t, b)
	questionID := seedQuestion(t, b)

	// Free plan max is 30 minutes. Request 60.
	_, err := b.CreateSession(ctx, CreateSessionParams{
		UserID:          userID,
		QuestionID:      questionID,
		DurationMinutes: 60,
		Plan:            "free",
	})
	require.ErrorIs(t, err, ErrDurationExceedsPlan)
}

func TestCreateSession_EnforcesConcurrentLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	userID := seedUser(t, b)
	questionID := seedQuestion(t, b)
	seedFreeGrant(t, b, userID, 60)

	// Free plan allows 1 concurrent session. Create one first.
	_, err := b.CreateSession(ctx, CreateSessionParams{
		UserID:          userID,
		QuestionID:      questionID,
		DurationMinutes: 10,
		Plan:            "free",
	})
	require.NoError(t, err)

	// Second session should fail.
	_, err = b.CreateSession(ctx, CreateSessionParams{
		UserID:          userID,
		QuestionID:      questionID,
		DurationMinutes: 10,
		Plan:            "free",
	})
	require.ErrorIs(t, err, ErrConcurrentSessionLimit)
}

// ---------------------------------------------------------------------------
// CompleteSession refund
// ---------------------------------------------------------------------------

func TestCompleteSession_RefundsUnusedMinutes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	userID := seedUser(t, b)
	questionID := seedQuestion(t, b)
	seedFreeGrant(t, b, userID, 60)

	// Create a 30-min session (reserves 30 from the 60-min grant).
	session, err := b.CreateSession(ctx, CreateSessionParams{
		UserID:          userID,
		QuestionID:      questionID,
		DurationMinutes: 30,
		Plan:            "free",
	})
	require.NoError(t, err)

	// Balance after reservation: 60 - 30 = 30.
	bs, err := db.New(b.pool).GetBillingSnapshot(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, int32(30), bs.TotalBalance)

	// Complete immediately (wall-clock ~0 seconds -> actualMinutes = 1).
	// Refund should be 30 - 1 = 29.
	err = b.CompleteSession(ctx, session.ID, 5)
	require.NoError(t, err)

	// Final balance: 30 + 29 = 59 (or 60 - 1 = 59).
	bs, err = db.New(b.pool).GetBillingSnapshot(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, int32(59), bs.TotalBalance)
}

// ---------------------------------------------------------------------------
// SQL query tests: ReserveMinutes, RefundSessionMinutes, FullRefundSessionMinutes
// ---------------------------------------------------------------------------

func TestReserveMinutes_MultiGrant_FIFO(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	userID := seedUser(t, b)
	questionID := seedQuestion(t, b)
	q := db.New(b.pool)

	// Create two grants: one expiring soon (20 min), one never-expiring (100 min).
	soonExpiry := pgtype.Timestamptz{Time: time.Now().Add(24 * time.Hour), Valid: true}
	err := q.EnsureFreeGrant(ctx, db.EnsureFreeGrantParams{
		UserID: userID, InitialMinutes: 20, ExpiresAt: soonExpiry,
	})
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
	session, err := b.CreateSession(ctx, CreateSessionParams{
		UserID: userID, QuestionID: questionID, DurationMinutes: 30, Plan: "free",
	})
	require.NoError(t, err)
	_ = session

	// Check: expiring grant should be at 0, purchase grant at 90.
	grants, err := q.ListActiveGrants(ctx, userID)
	require.NoError(t, err)
	// Only the purchase grant should have remaining (the free grant is drained).
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
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	userID := seedUser(t, b)
	questionID := seedQuestion(t, b)
	seedFreeGrant(t, b, userID, 10)

	_, err := b.CreateSession(ctx, CreateSessionParams{
		UserID: userID, QuestionID: questionID, DurationMinutes: 30, Plan: "free",
	})
	require.ErrorIs(t, err, ErrInsufficientBalance)

	// Balance untouched — all-or-nothing.
	bs, err := db.New(b.pool).GetBillingSnapshot(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, int32(10), bs.TotalBalance)
}

func TestReserveMinutes_ZeroGrants(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	userID := seedUser(t, b)
	questionID := seedQuestion(t, b)
	// No grants at all.

	_, err := b.CreateSession(ctx, CreateSessionParams{
		UserID: userID, QuestionID: questionID, DurationMinutes: 10, Plan: "free",
	})
	require.ErrorIs(t, err, ErrInsufficientBalance)
}

func TestRefundSessionMinutes_WallClock(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	userID := seedUser(t, b)
	questionID := seedQuestion(t, b)
	seedFreeGrant(t, b, userID, 60)

	session, err := b.CreateSession(ctx, CreateSessionParams{
		UserID: userID, QuestionID: questionID, DurationMinutes: 30, Plan: "free",
	})
	require.NoError(t, err)

	// Balance: 60 - 30 = 30.
	bs, err := db.New(b.pool).GetBillingSnapshot(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, int32(30), bs.TotalBalance)

	// Complete immediately → actual ~1 min, refund ~29.
	err = b.CompleteSession(ctx, session.ID, 3)
	require.NoError(t, err)

	bs, err = db.New(b.pool).GetBillingSnapshot(ctx, userID)
	require.NoError(t, err)
	// Should be close to 59 (60 - 1).
	assert.GreaterOrEqual(t, bs.TotalBalance, int32(58))
}

func TestFullRefundSessionMinutes_FailSession(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	userID := seedUser(t, b)
	questionID := seedQuestion(t, b)
	seedFreeGrant(t, b, userID, 60)

	session, err := b.CreateSession(ctx, CreateSessionParams{
		UserID: userID, QuestionID: questionID, DurationMinutes: 30, Plan: "free",
	})
	require.NoError(t, err)

	// Balance: 60 - 30 = 30.
	bs, err := db.New(b.pool).GetBillingSnapshot(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, int32(30), bs.TotalBalance)

	// Fail the session → full refund of all 30 reserved minutes.
	err = b.FailSession(ctx, session.ID)
	require.NoError(t, err)

	// Balance restored to 60.
	bs, err = db.New(b.pool).GetBillingSnapshot(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, int32(60), bs.TotalBalance)
}

func TestEnsureFreeGrant_Idempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	userID := seedUser(t, b)
	q := db.New(b.pool)

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
	assert.Equal(t, "free_monthly", entries[0].Reason)
}
