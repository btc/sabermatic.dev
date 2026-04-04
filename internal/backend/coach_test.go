package backend

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/db"
)

// ---------------------------------------------------------------------------
// seedPurchaseGrant creates a purchase grant (paid balance) for the given user.
// CanAccessCoach requires paidBalance > 0, which only purchase grants provide.
// ---------------------------------------------------------------------------

func seedPurchaseGrant(t *testing.T, b *Backend, userID uuid.UUID, minutes int32) {
	t.Helper()
	err := db.New(b.pool).CreatePurchaseGrant(context.Background(), db.CreatePurchaseGrantParams{
		UserID:        userID,
		StripeEventID: pgtype.Text{String: "evt_test_coach_" + uuid.New().String(), Valid: true},
		Minutes:       minutes,
	})
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// uuidSlicesEqual — unit tests (no DB needed)
// ---------------------------------------------------------------------------

func TestUUIDSlicesEqual(t *testing.T) {
	a := uuid.New()
	b := uuid.New()

	t.Run("both nil", func(t *testing.T) {
		assert.True(t, uuidSlicesEqual(nil, nil))
	})

	t.Run("both empty", func(t *testing.T) {
		assert.True(t, uuidSlicesEqual([]uuid.UUID{}, []uuid.UUID{}))
	})

	t.Run("nil vs empty", func(t *testing.T) {
		// len(nil) == 0 == len([]uuid.UUID{}) → equal
		assert.True(t, uuidSlicesEqual(nil, []uuid.UUID{}))
	})

	t.Run("equal single-element", func(t *testing.T) {
		assert.True(t, uuidSlicesEqual([]uuid.UUID{a}, []uuid.UUID{a}))
	})

	t.Run("equal multi-element", func(t *testing.T) {
		assert.True(t, uuidSlicesEqual([]uuid.UUID{a, b}, []uuid.UUID{a, b}))
	})

	t.Run("different lengths", func(t *testing.T) {
		assert.False(t, uuidSlicesEqual([]uuid.UUID{a}, []uuid.UUID{a, b}))
	})

	t.Run("same length different values", func(t *testing.T) {
		assert.False(t, uuidSlicesEqual([]uuid.UUID{a}, []uuid.UUID{b}))
	})

	t.Run("order matters", func(t *testing.T) {
		// The function is order-sensitive; [a,b] != [b,a]
		assert.False(t, uuidSlicesEqual([]uuid.UUID{a, b}, []uuid.UUID{b, a}))
	})
}

// ---------------------------------------------------------------------------
// GetLatestCoachAnalysis
// ---------------------------------------------------------------------------

func TestGetLatestCoachAnalysis_NoPaidBalance(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	userID := seedUser(t, b)
	// No purchase grant → paidBalance == 0 → ErrNoPaidBalance.

	_, err := b.GetLatestCoachAnalysis(ctx, userID)
	require.ErrorIs(t, err, ErrNoPaidBalance)
}

func TestGetLatestCoachAnalysis_NoAnalysisExists(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	userID := seedUser(t, b)
	seedPurchaseGrant(t, b, userID, 120)

	resp, err := b.GetLatestCoachAnalysis(ctx, userID)
	require.NoError(t, err)
	assert.Nil(t, resp, "expected nil when no coach analysis exists")
}

func TestGetLatestCoachAnalysis_ReturnsLatest(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	userID := seedUser(t, b)
	questionID := seedQuestion(t, b)
	seedPurchaseGrant(t, b, userID, 120)

	q := db.New(b.pool)

	// Insert two analyses; the second is newer.
	_, err := q.InsertCoachAnalysis(ctx, db.InsertCoachAnalysisParams{
		UserID:              userID,
		Narrative:           "first analysis",
		WeakestDimension:    pgtype.Text{String: "scalability", Valid: true},
		ImprovingDimensions: []string{"requirements"},
		TopicGaps:           []string{"caching"},
		SuggestedQuestionID: pgtype.UUID{},
		SessionsAnalyzed:    []uuid.UUID{},
	})
	require.NoError(t, err)

	qID := pgtype.UUID{Bytes: questionID, Valid: true}
	_, err = q.InsertCoachAnalysis(ctx, db.InsertCoachAnalysisParams{
		UserID:              userID,
		Narrative:           "second analysis",
		WeakestDimension:    pgtype.Text{String: "communication", Valid: true},
		ImprovingDimensions: []string{"scalability"},
		TopicGaps:           []string{"sharding"},
		SuggestedQuestionID: qID,
		SessionsAnalyzed:    []uuid.UUID{},
	})
	require.NoError(t, err)

	resp, err := b.GetLatestCoachAnalysis(ctx, userID)
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Equal(t, "second analysis", resp.Narrative)
	assert.Equal(t, "communication", resp.WeakestDimension)
	assert.Equal(t, []string{"scalability"}, resp.ImprovingDimensions)
	assert.Equal(t, []string{"sharding"}, resp.TopicGaps)
	require.NotNil(t, resp.SuggestedQuestionID)
	assert.Equal(t, questionID.String(), *resp.SuggestedQuestionID)
}

func TestGetLatestCoachAnalysis_NilSlicesNormalized(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	userID := seedUser(t, b)
	seedPurchaseGrant(t, b, userID, 120)

	q := db.New(b.pool)
	_, err := q.InsertCoachAnalysis(ctx, db.InsertCoachAnalysisParams{
		UserID:              userID,
		Narrative:           "sparse analysis",
		WeakestDimension:    pgtype.Text{},
		ImprovingDimensions: nil,
		TopicGaps:           nil,
		SuggestedQuestionID: pgtype.UUID{},
		SessionsAnalyzed:    []uuid.UUID{},
	})
	require.NoError(t, err)

	resp, err := b.GetLatestCoachAnalysis(ctx, userID)
	require.NoError(t, err)
	require.NotNil(t, resp)

	// Nil slices from DB must be returned as empty slices, not nil.
	assert.NotNil(t, resp.ImprovingDimensions)
	assert.NotNil(t, resp.TopicGaps)
	assert.Empty(t, resp.ImprovingDimensions)
	assert.Empty(t, resp.TopicGaps)
	assert.Nil(t, resp.SuggestedQuestionID)
}

// ---------------------------------------------------------------------------
// RequestCoachAnalysis
// ---------------------------------------------------------------------------

func TestRequestCoachAnalysis_NoPaidBalance(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	userID := seedUser(t, b)

	err := b.RequestCoachAnalysis(ctx, userID, false)
	require.ErrorIs(t, err, ErrNoPaidBalance)
}

func TestRequestCoachAnalysis_NoPriorAnalysisEnqueuesJob(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	userID := seedUser(t, b)
	seedPurchaseGrant(t, b, userID, 120)
	// No prior coach analysis → GetLatestCoachAnalysis returns pgx.ErrNoRows.
	// The `err == nil && uuidSlicesEqual(...)` guard is false, so the job is
	// enqueued regardless of whether there are reviewed sessions.

	err := b.RequestCoachAnalysis(ctx, userID, false)
	require.NoError(t, err)

	jobs := riverJobs(t, b, "run_coach_analysis")
	require.Len(t, jobs, 1)

	var args struct {
		UserID uuid.UUID `json:"user_id"`
	}
	require.NoError(t, json.Unmarshal(jobs[0], &args))
	assert.Equal(t, userID, args.UserID)
}

func TestRequestCoachAnalysis_ReviewedSessionsEnqueuesJob(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	userID := seedUser(t, b)
	questionID := seedQuestion(t, b)
	seedPurchaseGrant(t, b, userID, 120)

	// Create a session and move it to "reviewed" status.
	sessionID := seedSession(t, b, userID, questionID)
	setStatus(t, b, sessionID, "reviewed")

	err := b.RequestCoachAnalysis(ctx, userID, false)
	require.NoError(t, err)

	jobs := riverJobs(t, b, "run_coach_analysis")
	require.Len(t, jobs, 1)

	var args struct {
		UserID uuid.UUID `json:"user_id"`
	}
	require.NoError(t, json.Unmarshal(jobs[0], &args))
	assert.Equal(t, userID, args.UserID)
}

func TestRequestCoachAnalysis_NoNewSessionsReturnsError(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	userID := seedUser(t, b)
	questionID := seedQuestion(t, b)
	seedPurchaseGrant(t, b, userID, 120)

	// Create a reviewed session.
	sessionID := seedSession(t, b, userID, questionID)
	setStatus(t, b, sessionID, "reviewed")

	q := db.New(b.pool)

	// Insert a coach analysis that already covers this session.
	_, err := q.InsertCoachAnalysis(ctx, db.InsertCoachAnalysisParams{
		UserID:              userID,
		Narrative:           "up to date",
		WeakestDimension:    pgtype.Text{},
		ImprovingDimensions: []string{},
		TopicGaps:           []string{},
		SuggestedQuestionID: pgtype.UUID{},
		SessionsAnalyzed:    []uuid.UUID{sessionID},
	})
	require.NoError(t, err)

	// Now the set of reviewed sessions matches the last analysis → ErrNoNewSessions.
	err = b.RequestCoachAnalysis(ctx, userID, false)
	require.ErrorIs(t, err, ErrNoNewSessions)
}

func TestRequestCoachAnalysis_ForceBypassesSessionCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	userID := seedUser(t, b)
	questionID := seedQuestion(t, b)
	seedPurchaseGrant(t, b, userID, 120)

	// Create a reviewed session and a matching analysis so force=false would fail.
	sessionID := seedSession(t, b, userID, questionID)
	setStatus(t, b, sessionID, "reviewed")

	q := db.New(b.pool)
	_, err := q.InsertCoachAnalysis(ctx, db.InsertCoachAnalysisParams{
		UserID:              userID,
		Narrative:           "already done",
		WeakestDimension:    pgtype.Text{},
		ImprovingDimensions: []string{},
		TopicGaps:           []string{},
		SuggestedQuestionID: pgtype.UUID{},
		SessionsAnalyzed:    []uuid.UUID{sessionID},
	})
	require.NoError(t, err)

	// force=true bypasses the session-change check.
	err = b.RequestCoachAnalysis(ctx, userID, true)
	require.NoError(t, err)

	jobs := riverJobs(t, b, "run_coach_analysis")
	require.Len(t, jobs, 1)

	var args struct {
		UserID uuid.UUID `json:"user_id"`
	}
	require.NoError(t, json.Unmarshal(jobs[0], &args))
	assert.Equal(t, userID, args.UserID)
}
