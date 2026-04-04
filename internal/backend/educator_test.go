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
// Helpers
// ---------------------------------------------------------------------------

// seedReviewedSession creates a session, seeds an evaluation (required for
// the session to be genuinely "reviewed"), and sets status to "reviewed".
// Returns the session ID.
func seedReviewedSession(t *testing.T, b *Backend, userID, questionID uuid.UUID) uuid.UUID {
	t.Helper()
	sessionID := seedSession(t, b, userID, questionID)
	seedEval(t, b, sessionID)
	setStatus(t, b, sessionID, "reviewed")
	return sessionID
}

// seedEducatorAnalysis inserts an educator_analyses row with the given status
// and optional model/gap content. Returns the analysis ID.
func seedEducatorAnalysis(t *testing.T, b *Backend, sessionID uuid.UUID, status, modelAnswer, gapDeepDives string) uuid.UUID {
	t.Helper()
	q := db.New(b.pool)
	// InsertEducatorAnalysis defaults to status='generating'. We may need to
	// override the status afterwards.
	analysisID, err := q.InsertEducatorAnalysis(context.Background(), sessionID)
	require.NoError(t, err)

	if status != "generating" {
		if status == "completed" {
			err = q.UpdateEducatorAnalysisContent(context.Background(), db.UpdateEducatorAnalysisContentParams{
				ID:           analysisID,
				ModelAnswer:  pgtype.Text{String: modelAnswer, Valid: modelAnswer != ""},
				GapDeepDives: pgtype.Text{String: gapDeepDives, Valid: gapDeepDives != ""},
			})
			require.NoError(t, err)
		} else {
			err = q.UpdateEducatorAnalysisStatus(context.Background(), db.UpdateEducatorAnalysisStatusParams{
				ID:     analysisID,
				Status: status,
			})
			require.NoError(t, err)
		}
	}
	return analysisID
}

// ---------------------------------------------------------------------------
// GetEducatorAnalysis
// ---------------------------------------------------------------------------

func TestGetEducatorAnalysis_SessionNotReviewed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	userID := seedUser(t, b)
	questionID := seedQuestion(t, b)
	sessionID := seedSession(t, b, userID, questionID) // status=active

	_, err := b.GetEducatorAnalysis(ctx, sessionID, userID)
	require.ErrorIs(t, err, ErrEvaluationNotReady)
}

func TestGetEducatorAnalysis_WrongUser(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	ownerID := seedUser(t, b)
	otherID := seedUser(t, b)
	questionID := seedQuestion(t, b)
	sessionID := seedReviewedSession(t, b, ownerID, questionID)

	_, err := b.GetEducatorAnalysis(ctx, sessionID, otherID)
	require.ErrorIs(t, err, ErrSessionNotOwned)
}

func TestGetEducatorAnalysis_NotRequested(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	userID := seedUser(t, b)
	questionID := seedQuestion(t, b)
	// FreeTaste user (free plan, 0 used) can GET; no educator_analyses row yet.
	sessionID := seedReviewedSession(t, b, userID, questionID)

	resp, err := b.GetEducatorAnalysis(ctx, sessionID, userID)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "not_requested", resp.Status)
}

func TestGetEducatorAnalysis_Generating(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	userID := seedUser(t, b)
	questionID := seedQuestion(t, b)
	sessionID := seedReviewedSession(t, b, userID, questionID)
	seedEducatorAnalysis(t, b, sessionID, "generating", "", "")

	resp, err := b.GetEducatorAnalysis(ctx, sessionID, userID)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "generating", resp.Status)
}

func TestGetEducatorAnalysis_Failed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	userID := seedUser(t, b)
	questionID := seedQuestion(t, b)
	sessionID := seedReviewedSession(t, b, userID, questionID)
	seedEducatorAnalysis(t, b, sessionID, "failed", "", "")

	resp, err := b.GetEducatorAnalysis(ctx, sessionID, userID)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "failed", resp.Status)
}

func TestGetEducatorAnalysis_Completed_PaidUser(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	userID := seedUser(t, b)
	questionID := seedQuestion(t, b)
	// Paid user: purchase grant gives paidBalance > 0 → Full access level.
	seedPurchaseGrant(t, b, userID, 120)
	sessionID := seedReviewedSession(t, b, userID, questionID)
	seedEducatorAnalysis(t, b, sessionID, "completed", "Model answer text.", "Gap deep dives text.")

	resp, err := b.GetEducatorAnalysis(ctx, sessionID, userID)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "completed", resp.Status)
	assert.Equal(t, "Model answer text.", resp.ModelAnswer)
	assert.Equal(t, "Gap deep dives text.", resp.GapDeepDives)
}

func TestGetEducatorAnalysis_Completed_PreviewUser(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	userID := seedUser(t, b)
	questionID := seedQuestion(t, b)
	// Preview user: free plan, already used 1 free educator → freeEducatorUsed >= limit(1)
	// Exhaust the free taste by incrementing used count.
	_, err := b.pool.Exec(context.Background(),
		`UPDATE users SET free_full_educators_used = 1 WHERE id = $1`, userID)
	require.NoError(t, err)

	sessionID := seedReviewedSession(t, b, userID, questionID)
	modelAnswer := "This is a very long model answer that exceeds the preview limit by a significant margin and contains many words."
	seedEducatorAnalysis(t, b, sessionID, "completed", modelAnswer, "Gap deep dives text.")

	resp, err := b.GetEducatorAnalysis(ctx, sessionID, userID)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "completed", resp.Status)
	// Preview users see at most previewModelAnswerLen runes; no gap deep dives.
	assert.LessOrEqual(t, len([]rune(resp.ModelAnswer)), previewModelAnswerLen)
	assert.Empty(t, resp.GapDeepDives)
}

func TestGetEducatorAnalysis_Completed_FreeTasteUser(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	userID := seedUser(t, b)
	questionID := seedQuestion(t, b)
	// FreeTaste: free plan with 0 educators used (default for new users).
	// free plan FreeEducatorLimit = 1, so freeEducatorUsed(0) < 1 → FreeTaste.
	sessionID := seedReviewedSession(t, b, userID, questionID)
	seedEducatorAnalysis(t, b, sessionID, "completed", "Full model answer.", "Full gap deep dives.")

	resp, err := b.GetEducatorAnalysis(ctx, sessionID, userID)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "completed", resp.Status)
	// FreeTaste sees full content on GET.
	assert.Equal(t, "Full model answer.", resp.ModelAnswer)
	assert.Equal(t, "Full gap deep dives.", resp.GapDeepDives)
}

// ---------------------------------------------------------------------------
// RequestEducatorAnalysis
// ---------------------------------------------------------------------------

func TestRequestEducatorAnalysis_SessionNotReviewed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	userID := seedUser(t, b)
	questionID := seedQuestion(t, b)
	sessionID := seedSession(t, b, userID, questionID) // status=active

	err := b.RequestEducatorAnalysis(ctx, sessionID, userID)
	require.ErrorIs(t, err, ErrEvaluationNotReady)
}

func TestRequestEducatorAnalysis_WrongUser(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	ownerID := seedUser(t, b)
	otherID := seedUser(t, b)
	questionID := seedQuestion(t, b)
	sessionID := seedReviewedSession(t, b, ownerID, questionID)

	err := b.RequestEducatorAnalysis(ctx, sessionID, otherID)
	require.ErrorIs(t, err, ErrSessionNotOwned)
}

func TestRequestEducatorAnalysis_PreviewUserRejected(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	userID := seedUser(t, b)
	questionID := seedQuestion(t, b)
	// Exhaust free taste → Preview access level.
	_, err := b.pool.Exec(context.Background(),
		`UPDATE users SET free_full_educators_used = 1 WHERE id = $1`, userID)
	require.NoError(t, err)

	sessionID := seedReviewedSession(t, b, userID, questionID)

	err = b.RequestEducatorAnalysis(ctx, sessionID, userID)
	require.ErrorIs(t, err, ErrNoPaidBalance)
}

func TestRequestEducatorAnalysis_FreeTasteEnqueuesJob(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	userID := seedUser(t, b)
	questionID := seedQuestion(t, b)
	// Fresh free-plan user has 0 educators used → FreeTaste access.
	sessionID := seedReviewedSession(t, b, userID, questionID)

	err := b.RequestEducatorAnalysis(ctx, sessionID, userID)
	require.NoError(t, err)

	jobs := riverJobs(t, b, "generate_educator_content")
	require.Len(t, jobs, 1)

	var args struct {
		SessionID uuid.UUID `json:"session_id"`
	}
	require.NoError(t, json.Unmarshal(jobs[0], &args))
	assert.Equal(t, sessionID, args.SessionID)

	// The free-taste count must have been incremented.
	var used int32
	err = b.pool.QueryRow(ctx,
		"SELECT free_full_educators_used FROM users WHERE id = $1", userID).Scan(&used)
	require.NoError(t, err)
	assert.Equal(t, int32(1), used)
}

func TestRequestEducatorAnalysis_PaidUserEnqueuesJob(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	userID := seedUser(t, b)
	questionID := seedQuestion(t, b)
	seedPurchaseGrant(t, b, userID, 120) // Full access.
	sessionID := seedReviewedSession(t, b, userID, questionID)

	err := b.RequestEducatorAnalysis(ctx, sessionID, userID)
	require.NoError(t, err)

	jobs := riverJobs(t, b, "generate_educator_content")
	require.Len(t, jobs, 1)

	var args struct {
		SessionID uuid.UUID `json:"session_id"`
	}
	require.NoError(t, json.Unmarshal(jobs[0], &args))
	assert.Equal(t, sessionID, args.SessionID)
}

func TestRequestEducatorAnalysis_AlreadyCompleted(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	userID := seedUser(t, b)
	questionID := seedQuestion(t, b)
	seedPurchaseGrant(t, b, userID, 120)
	sessionID := seedReviewedSession(t, b, userID, questionID)
	seedEducatorAnalysis(t, b, sessionID, "completed", "answer", "gaps")

	err := b.RequestEducatorAnalysis(ctx, sessionID, userID)
	require.ErrorIs(t, err, ErrAlreadyExists)
}

func TestRequestEducatorAnalysis_AlreadyGenerating_Idempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	userID := seedUser(t, b)
	questionID := seedQuestion(t, b)
	seedPurchaseGrant(t, b, userID, 120)
	sessionID := seedReviewedSession(t, b, userID, questionID)
	seedEducatorAnalysis(t, b, sessionID, "generating", "", "")

	// Should return nil (idempotent) without enqueuing another job.
	err := b.RequestEducatorAnalysis(ctx, sessionID, userID)
	require.NoError(t, err)
}

func TestRequestEducatorAnalysis_FailedRetry(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	userID := seedUser(t, b)
	questionID := seedQuestion(t, b)
	seedPurchaseGrant(t, b, userID, 120)
	sessionID := seedReviewedSession(t, b, userID, questionID)
	analysisID := seedEducatorAnalysis(t, b, sessionID, "failed", "", "")

	err := b.RequestEducatorAnalysis(ctx, sessionID, userID)
	require.NoError(t, err)

	// Status must be reset to "generating".
	ea, err := db.New(b.pool).GetEducatorAnalysisBySession(context.Background(), sessionID)
	require.NoError(t, err)
	assert.Equal(t, "generating", ea.Status)
	assert.Equal(t, analysisID, ea.ID)

	// A job must have been enqueued.
	jobs := riverJobs(t, b, "generate_educator_content")
	require.Len(t, jobs, 1)

	var args struct {
		SessionID uuid.UUID `json:"session_id"`
	}
	require.NoError(t, json.Unmarshal(jobs[0], &args))
	assert.Equal(t, sessionID, args.SessionID)
}
