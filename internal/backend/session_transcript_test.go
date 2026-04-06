package backend_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/backend"
	"github.com/btc/drill/internal/backendtest"
	"github.com/btc/drill/internal/db"
)

// createTestSession creates a session via the real CreateSession flow and
// returns the resulting InterviewSession. The seeded user (via backendtest.SeedUser)
// receives a 60-min free trial grant, so a 15-min session is always affordable.
func createTestSession(t *testing.T, b *backend.Backend, userID, questionID uuid.UUID) db.InterviewSession {
	t.Helper()
	session, err := b.CreateSession(context.Background(), backend.CreateSessionParams{
		UserID:          userID,
		QuestionID:      questionID,
		DurationMinutes: 15,
		TTSEnabled:      false,
		Plan:            "free",
	})
	require.NoError(t, err)
	return session
}

// ---------------------------------------------------------------------------
// GetTranscript
// ---------------------------------------------------------------------------

func TestGetTranscript_Success(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	userID := backendtest.SeedUser(t, b)
	questionID := seedQuestion(t, b)
	session := createTestSession(t, b, userID, questionID)

	// Seed two messages in order.
	q := db.New(b.Pool())
	msg1, err := q.InsertMessage(ctx, db.InsertMessageParams{
		ID:        uuid.New(),
		SessionID: session.ID,
		Seq:       1,
		Role:      "interviewer",
		Content:   "Hello candidate",
	})
	require.NoError(t, err)

	msg2, err := q.InsertMessage(ctx, db.InsertMessageParams{
		ID:        uuid.New(),
		SessionID: session.ID,
		Seq:       2,
		Role:      "candidate",
		Content:   "Hello interviewer",
	})
	require.NoError(t, err)

	msgs, err := b.GetTranscript(ctx, session.ID, userID)
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	assert.Equal(t, msg1.ID, msgs[0].ID)
	assert.Equal(t, msg2.ID, msgs[1].ID)
	// Verify ordering by seq.
	assert.Equal(t, int32(1), msgs[0].Seq)
	assert.Equal(t, int32(2), msgs[1].Seq)
}

func TestGetTranscript_EmptyTranscript(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	userID := backendtest.SeedUser(t, b)
	questionID := seedQuestion(t, b)
	session := createTestSession(t, b, userID, questionID)

	msgs, err := b.GetTranscript(ctx, session.ID, userID)
	require.NoError(t, err)
	require.NotNil(t, msgs, "empty transcript must be a non-nil empty slice")
	assert.Len(t, msgs, 0)
}

func TestGetTranscript_NotOwned(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	ownerID := backendtest.SeedUser(t, b)
	otherID := backendtest.SeedUser(t, b)
	questionID := seedQuestion(t, b)
	session := createTestSession(t, b, ownerID, questionID)

	_, err := b.GetTranscript(ctx, session.ID, otherID)
	require.ErrorIs(t, err, backend.ErrSessionNotOwned)
}

func TestGetTranscript_NotFound(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	userID := backendtest.SeedUser(t, b)

	_, err := b.GetTranscript(ctx, uuid.New(), userID)
	require.ErrorIs(t, err, backend.ErrSessionNotFound)
}
