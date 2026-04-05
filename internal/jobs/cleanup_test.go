package jobs_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/jobs"
	"github.com/btc/drill/internal/jobs/jobtest"
	"github.com/btc/drill/internal/testutil"
)

func TestCleanupAbandonedSessions_CancelsEmptySessions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	b := testutil.NewTestBackend(t)
	pool := b.Pool()

	// Seed a session with 0 candidate messages.
	seed := seedSessionWithMessages(t, ctx, b, 0)

	q := db.New(pool)

	// Seed a grant (60 min) and reserve 30 min for this session so we can
	// verify the full refund after cancellation.
	err := q.EnsureFreeGrant(ctx, db.EnsureFreeGrantParams{
		UserID:         seed.UserID,
		InitialMinutes: 60,
		ExpiresAt:      pgtype.Timestamptz{Time: time.Date(2099, 12, 31, 0, 0, 0, 0, time.UTC), Valid: true},
	})
	require.NoError(t, err)

	_, err = q.ReserveMinutes(ctx, db.ReserveMinutesParams{
		UserID:    seed.UserID,
		SessionID: pgtype.UUID{Bytes: seed.SessionID, Valid: true},
		Minutes:   30,
	})
	require.NoError(t, err)

	// Set reserved_minutes on the session.
	_, err = pool.Exec(ctx,
		`UPDATE interview_sessions SET reserved_minutes = 30 WHERE id = $1`, seed.SessionID)
	require.NoError(t, err)

	// Balance after reservation: 60 - 30 = 30.
	bs, err := q.GetBillingSnapshot(ctx, seed.UserID)
	require.NoError(t, err)
	require.Equal(t, int32(30), bs.TotalBalance)

	// Set it back to "active" and backdate started_at so it qualifies as abandoned.
	_, err = pool.Exec(ctx,
		`UPDATE interview_sessions SET status = 'active', started_at = NOW() - INTERVAL '2 hours' WHERE id = $1`,
		seed.SessionID,
	)
	require.NoError(t, err)

	// Verify the session is now active.
	session, err := q.GetSession(ctx, seed.SessionID)
	require.NoError(t, err)
	require.Equal(t, "active", session.Status)

	// Construct worker with a real River client (not started -- no workers run).
	riverClient, err := river.NewClient(riverpgxv5.New(pool), &river.Config{})
	require.NoError(t, err)
	worker := &jobs.CleanupAbandonedSessionsWorker{
		Pool: pool,
		Jobs: riverClient,
	}

	err = worker.Work(ctx, &river.Job[jobs.CleanupAbandonedSessionsArgs]{
		Args: jobs.CleanupAbandonedSessionsArgs{},
	})
	require.NoError(t, err)

	// Assert: session is cancelled and archived.
	session, err = q.GetSession(ctx, seed.SessionID)
	require.NoError(t, err)
	assert.Equal(t, "cancelled", session.Status)
	assert.True(t, session.EndedAt.Valid, "ended_at should be set after cancellation")
	assert.True(t, session.ArchivedAt.Valid, "archived_at should be set after cancellation")

	// Assert: minutes fully refunded (balance restored to 60).
	bs, err = q.GetBillingSnapshot(ctx, seed.UserID)
	require.NoError(t, err)
	assert.Equal(t, int32(60), bs.TotalBalance, "cancelled empty session should be fully refunded")

	// Assert: no evaluate_session job was enqueued.
	jobtest.AssertJobEnqueued(t, pool, "evaluate_session", 0)
}

func TestCleanupAbandonedSessions_CompletesSessionsWithMessages(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	b := testutil.NewTestBackend(t)
	pool := b.Pool()

	// Seed a session with candidate messages (seedSessionWithMessages inserts alternating roles).
	seed := seedSessionWithMessages(t, ctx, b, 2)

	q := db.New(pool)

	// Seed a grant (60 min) and reserve 30 min for this session so we can
	// verify that NO refund happens for completed sessions.
	err := q.EnsureFreeGrant(ctx, db.EnsureFreeGrantParams{
		UserID:         seed.UserID,
		InitialMinutes: 60,
		ExpiresAt:      pgtype.Timestamptz{Time: time.Date(2099, 12, 31, 0, 0, 0, 0, time.UTC), Valid: true},
	})
	require.NoError(t, err)

	_, err = q.ReserveMinutes(ctx, db.ReserveMinutesParams{
		UserID:    seed.UserID,
		SessionID: pgtype.UUID{Bytes: seed.SessionID, Valid: true},
		Minutes:   30,
	})
	require.NoError(t, err)

	// Set reserved_minutes on the session.
	_, err = pool.Exec(ctx,
		`UPDATE interview_sessions SET reserved_minutes = 30 WHERE id = $1`, seed.SessionID)
	require.NoError(t, err)

	// Balance after reservation: 60 - 30 = 30.
	bs, err := q.GetBillingSnapshot(ctx, seed.UserID)
	require.NoError(t, err)
	require.Equal(t, int32(30), bs.TotalBalance)

	// Set it back to "active" and backdate started_at so it qualifies as abandoned.
	_, err = pool.Exec(ctx,
		`UPDATE interview_sessions SET status = 'active', started_at = NOW() - INTERVAL '2 hours' WHERE id = $1`,
		seed.SessionID,
	)
	require.NoError(t, err)

	// Verify the session is now active.
	session, err := q.GetSession(ctx, seed.SessionID)
	require.NoError(t, err)
	require.Equal(t, "active", session.Status)

	// Construct worker with a real River client (not started -- no workers run).
	riverClient, err := river.NewClient(riverpgxv5.New(pool), &river.Config{})
	require.NoError(t, err)
	worker := &jobs.CleanupAbandonedSessionsWorker{
		Pool: pool,
		Jobs: riverClient,
	}

	err = worker.Work(ctx, &river.Job[jobs.CleanupAbandonedSessionsArgs]{
		Args: jobs.CleanupAbandonedSessionsArgs{},
	})
	require.NoError(t, err)

	// Assert: session is completed (not cancelled).
	session, err = q.GetSession(ctx, seed.SessionID)
	require.NoError(t, err)
	assert.Equal(t, "completed", session.Status)
	assert.True(t, session.EndedAt.Valid, "ended_at should be set after completion")
	assert.False(t, session.ArchivedAt.Valid, "archived_at should NOT be set for completed sessions")

	// Assert: balance unchanged (no refund for completed sessions).
	bs, err = q.GetBillingSnapshot(ctx, seed.UserID)
	require.NoError(t, err)
	assert.Equal(t, int32(30), bs.TotalBalance, "completed session should NOT be refunded by cleanup worker")

	// Assert: evaluate_session job was enqueued.
	jobtest.AssertJobEnqueued(t, pool, "evaluate_session", 1)
}

func TestCleanupAbandonedSessions_RecentSessionNotAffected(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	b := testutil.NewTestBackend(t)
	pool := b.Pool()

	seed := seedSessionWithMessages(t, ctx, b, 2)

	// Set back to "active" but keep started_at recent (default NOW()).
	q := db.New(pool)
	err := q.UpdateSessionStatusOnly(ctx, db.UpdateSessionStatusOnlyParams{
		ID:     seed.SessionID,
		Status: "active",
	})
	require.NoError(t, err)

	riverClient2, err := river.NewClient(riverpgxv5.New(pool), &river.Config{})
	require.NoError(t, err)
	worker := &jobs.CleanupAbandonedSessionsWorker{
		Pool: pool,
		Jobs: riverClient2,
	}

	err = worker.Work(ctx, &river.Job[jobs.CleanupAbandonedSessionsArgs]{
		Args: jobs.CleanupAbandonedSessionsArgs{},
	})
	require.NoError(t, err)

	// Assert: session is still "active" (not old enough to be abandoned).
	session, err := q.GetSession(ctx, seed.SessionID)
	require.NoError(t, err)
	assert.Equal(t, "active", session.Status)
}
