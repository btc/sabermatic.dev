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
