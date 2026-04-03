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
func seedFreeGrant(t *testing.T, b *Backend, userID uuid.UUID, minutes int32) db.Grant {
	t.Helper()
	q := db.New(b.pool)
	expiresAt := pgtype.Timestamptz{
		Time:  time.Date(2099, 12, 31, 0, 0, 0, 0, time.UTC),
		Valid: true,
	}
	grant, err := q.CreateFreeGrant(context.Background(), db.CreateFreeGrantParams{
		UserID:         userID,
		InitialMinutes: minutes,
		ExpiresAt:      expiresAt,
	})
	require.NoError(t, err)
	return grant
}

// ---------------------------------------------------------------------------
// ReserveMinutes
// ---------------------------------------------------------------------------

func TestReserveMinutes_SingleGrant(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	userID := seedUser(t, b)
	seedFreeGrant(t, b, userID, 60)

	// Reserve 30 minutes.
	sessionID := seedSession(t, b, userID, seedQuestion(t, b))
	err := b.ReserveMinutes(ctx, userID, sessionID, 30)
	require.NoError(t, err)

	// Verify balance dropped from 60 to 30.
	balance, err := db.New(b.pool).GetUserBalance(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, int32(30), balance)
}

func TestReserveMinutes_InsufficientBalance(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	userID := seedUser(t, b)
	seedFreeGrant(t, b, userID, 10)

	sessionID := seedSession(t, b, userID, seedQuestion(t, b))
	err := b.ReserveMinutes(ctx, userID, sessionID, 30)
	require.ErrorIs(t, err, ErrInsufficientBalance)

	// Balance should be untouched since the tx rolled back.
	balance, err := db.New(b.pool).GetUserBalance(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, int32(10), balance)
}

// ---------------------------------------------------------------------------
// RefundMinutes
// ---------------------------------------------------------------------------

func TestRefundMinutes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	userID := seedUser(t, b)
	seedFreeGrant(t, b, userID, 60)

	sessionID := seedSession(t, b, userID, seedQuestion(t, b))

	// Reserve 30, then refund 10.
	err := b.ReserveMinutes(ctx, userID, sessionID, 30)
	require.NoError(t, err)

	err = b.RefundMinutes(ctx, userID, sessionID, 10)
	require.NoError(t, err)

	// Balance should be 60 - 30 + 10 = 40.
	balance, err := db.New(b.pool).GetUserBalance(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, int32(40), balance)
}

// ---------------------------------------------------------------------------
// CreateSession entitlement enforcement
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
	balance, err := db.New(b.pool).GetUserBalance(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, int32(30), balance)
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
