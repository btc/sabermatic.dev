package backend_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/backendtest"
	"github.com/btc/drill/internal/db"
)

// ---------------------------------------------------------------------------
// ArchiveSessions
// ---------------------------------------------------------------------------

func TestArchiveSessions_Archive(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	userID := backendtest.SeedUser(t, b)
	questionID := seedQuestion(t, b)
	// Use seedSession (bypasses concurrency/balance checks) to create 2 sessions
	// for the same user without hitting the concurrent-session limit.
	s1ID := seedSession(t, b, userID, questionID)
	s2ID := seedSession(t, b, userID, questionID)

	err := b.ArchiveSessions(ctx, userID, []uuid.UUID{s1ID, s2ID}, true)
	require.NoError(t, err)

	// Verify archived_at is set on both sessions.
	q := db.New(b.Pool())
	row1, err := q.GetSessionByID(ctx, s1ID)
	require.NoError(t, err)
	assert.True(t, row1.ArchivedAt.Valid, "s1 should have archived_at set")

	row2, err := q.GetSessionByID(ctx, s2ID)
	require.NoError(t, err)
	assert.True(t, row2.ArchivedAt.Valid, "s2 should have archived_at set")
}

func TestArchiveSessions_Unarchive(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	userID := backendtest.SeedUser(t, b)
	questionID := seedQuestion(t, b)
	session := createTestSession(t, b, userID, questionID)

	// Archive first.
	err := b.ArchiveSessions(ctx, userID, []uuid.UUID{session.ID}, true)
	require.NoError(t, err)

	// Unarchive.
	err = b.ArchiveSessions(ctx, userID, []uuid.UUID{session.ID}, false)
	require.NoError(t, err)

	// Verify archived_at is cleared.
	q := db.New(b.Pool())
	row, err := q.GetSessionByID(ctx, session.ID)
	require.NoError(t, err)
	assert.False(t, row.ArchivedAt.Valid, "archived_at should be cleared after unarchive")
}

func TestArchiveSessions_OwnershipFilter(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	ownerID := backendtest.SeedUser(t, b)
	otherID := backendtest.SeedUser(t, b)
	questionID := seedQuestion(t, b)
	session := createTestSession(t, b, ownerID, questionID)

	// otherID tries to archive ownerID's session — no error, silently skipped.
	err := b.ArchiveSessions(ctx, otherID, []uuid.UUID{session.ID}, true)
	require.NoError(t, err)

	// The session should remain unarchived.
	q := db.New(b.Pool())
	row, err := q.GetSessionByID(ctx, session.ID)
	require.NoError(t, err)
	assert.False(t, row.ArchivedAt.Valid, "session should not be archived when wrong user calls")
}

func TestArchiveSessions_EmptyList(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	userID := backendtest.SeedUser(t, b)

	err := b.ArchiveSessions(ctx, userID, []uuid.UUID{}, true)
	require.NoError(t, err)
}
