package jobs_test

import (
	"context"
	"testing"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/jobs"
)

func TestCleanupAbandonedSessions_MarksCompleted(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	pool := startTestPostgres(t)

	// Seed a session with messages (status = "completed" after seedSessionWithMessages).
	seed := seedSessionWithMessages(t, ctx, pool, 2)

	// Set it back to "active" and backdate started_at so it qualifies as abandoned.
	// The FindAbandonedSessions query: started_at + (config_duration_minutes + 5) * '1 minute' < NOW()
	// config_duration_minutes = 45, so we need started_at older than 50 minutes ago.
	_, err := pool.Exec(ctx,
		`UPDATE interview_sessions SET status = 'active', started_at = NOW() - INTERVAL '2 hours' WHERE id = $1`,
		seed.SessionID,
	)
	require.NoError(t, err)

	// Verify the session is now active.
	q := db.New(pool)
	session, err := q.GetSession(ctx, seed.SessionID)
	require.NoError(t, err)
	require.Equal(t, "active", session.Status)

	// Construct worker with a real River client (not started — no workers run).
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

	// Assert: session status is now "completed".
	session, err = q.GetSession(ctx, seed.SessionID)
	require.NoError(t, err)
	assert.Equal(t, "completed", session.Status)
	assert.True(t, session.EndedAt.Valid, "ended_at should be set after MarkSessionCompleted")
}

func TestCleanupAbandonedSessions_RecentSessionNotAffected(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	pool := startTestPostgres(t)

	seed := seedSessionWithMessages(t, ctx, pool, 2)

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
