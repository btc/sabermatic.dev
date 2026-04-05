package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/jobs/jobtest"
)

// ---------------------------------------------------------------------------
// Evaluation-specific seed helpers
// ---------------------------------------------------------------------------

// seedUser creates a user via the real production Signup path.
func seedUser(t *testing.T, b *Backend) uuid.UUID {
	t.Helper()
	result, err := b.Signup(context.Background(), SignupParams{
		Email:       fmt.Sprintf("test-%s@example.com", uuid.NewString()[:8]),
		Password:    "testpassword123",
		DisplayName: "Test User",
	})
	require.NoError(t, err)
	return result.UserID
}

// seedQuestion creates a question directly via raw SQL and returns its ID.
func seedQuestion(t *testing.T, b *Backend) uuid.UUID {
	t.Helper()
	qID := uuid.New()
	_, err := b.pool.Exec(context.Background(),
		`INSERT INTO questions (id, title, prompt, difficulty, tags, source)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		qID,
		"Design a URL Shortener",
		"Design a URL shortening service like bit.ly.",
		"medium",
		[]string{"system-design"},
		"seed",
	)
	require.NoError(t, err)
	return qID
}

// seedSession creates an active session and returns its ID.
func seedSession(t *testing.T, b *Backend, userID, questionID uuid.UUID) uuid.UUID {
	t.Helper()
	q := db.New(b.pool)
	s, err := q.CreateSession(context.Background(), db.CreateSessionParams{
		UserID:                userID,
		QuestionID:            questionID,
		ConfigDurationMinutes: 45,
		ConfigTtsEnabled:      false,
	})
	require.NoError(t, err)
	return s.ID
}

// setStatus updates a session's status directly.
func setStatus(t *testing.T, b *Backend, sessionID uuid.UUID, status string) {
	t.Helper()
	err := db.New(b.pool).UpdateSessionStatusOnly(context.Background(), db.UpdateSessionStatusOnlyParams{
		ID:     sessionID,
		Status: status,
	})
	require.NoError(t, err)
}

// seedEval inserts an evaluation row and returns its ID.
func seedEval(t *testing.T, b *Backend, sessionID uuid.UUID) uuid.UUID {
	t.Helper()
	evalID, err := db.New(b.pool).InsertEvaluation(context.Background(), db.InsertEvaluationParams{
		SessionID:          sessionID,
		ScoreRequirements:  3,
		ScoreArchitecture:  4,
		ScoreDeepDive:      2,
		ScoreScalability:   3,
		ScoreCommunication: 4,
		ScoreOverall:       3,
		Strengths:          []byte(`["Good requirements gathering"]`),
		Gaps:               []byte(`["Missing cache layer"]`),
		Advice:             "Focus on depth.",
	})
	require.NoError(t, err)
	return evalID
}

// seedMsg inserts a message and returns its ID.
func seedMsg(t *testing.T, b *Backend, sessionID uuid.UUID, seq int32, role, content string) uuid.UUID {
	t.Helper()
	msg, err := db.New(b.pool).InsertMessage(context.Background(), db.InsertMessageParams{
		ID:          uuid.New(),
		SessionID:   sessionID,
		Seq:         seq,
		Role:        role,
		Content:     content,
		InputMethod: pgtype.Text{String: "text", Valid: true},
	})
	require.NoError(t, err)
	return msg.ID
}

// seedAnnotation inserts an annotation for the given evaluation and message.
func seedAnnotation(t *testing.T, b *Backend, evalID, msgID uuid.UUID, annType, content string) {
	t.Helper()
	err := db.New(b.pool).InsertAnnotation(context.Background(), db.InsertAnnotationParams{
		EvaluationID:   evalID,
		MessageID:      msgID,
		AnnotationType: annType,
		Content:        content,
	})
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// GetEvaluation
// ---------------------------------------------------------------------------

func TestGetEvaluation_Reviewed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	userID := seedUser(t, b)
	questionID := seedQuestion(t, b)
	sessionID := seedSession(t, b, userID, questionID)
	setStatus(t, b, sessionID, "reviewed")

	msg1ID := seedMsg(t, b, sessionID, 1, "interviewer", "Tell me about requirements.")
	msg2ID := seedMsg(t, b, sessionID, 2, "candidate", "We need to handle 10k RPS.")

	evalID := seedEval(t, b, sessionID)
	seedAnnotation(t, b, evalID, msg1ID, "strength", "Good opening question.")
	seedAnnotation(t, b, evalID, msg2ID, "gap", "Did not discuss caching.")

	resp, err := b.GetEvaluation(ctx, sessionID, userID)
	require.NoError(t, err)

	assert.Equal(t, "reviewed", resp.Status)

	require.NotNil(t, resp.Scores)
	assert.Equal(t, int32(3), resp.Scores.Requirements)
	assert.Equal(t, int32(4), resp.Scores.Architecture)
	assert.Equal(t, int32(2), resp.Scores.DeepDive)
	assert.Equal(t, int32(3), resp.Scores.Scalability)
	assert.Equal(t, int32(4), resp.Scores.Communication)
	assert.Equal(t, int32(3), resp.Scores.Overall)

	assert.Equal(t, []string{"Good requirements gathering"}, resp.Strengths)
	assert.Equal(t, []string{"Missing cache layer"}, resp.Gaps)
	assert.Equal(t, "Focus on depth.", resp.Advice)

	require.Len(t, resp.Annotations, 2)
	assert.Equal(t, int32(1), resp.Annotations[0].MessageSeq)
	assert.Equal(t, "strength", resp.Annotations[0].Type)
	assert.Equal(t, "Good opening question.", resp.Annotations[0].Content)
	assert.Equal(t, int32(2), resp.Annotations[1].MessageSeq)
	assert.Equal(t, "gap", resp.Annotations[1].Type)
	assert.Equal(t, "Did not discuss caching.", resp.Annotations[1].Content)
}

func TestGetEvaluation_EvaluationFailed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	userID := seedUser(t, b)
	questionID := seedQuestion(t, b)
	sessionID := seedSession(t, b, userID, questionID)
	setStatus(t, b, sessionID, "evaluation_failed")

	resp, err := b.GetEvaluation(ctx, sessionID, userID)
	require.NoError(t, err)

	assert.Equal(t, "evaluation_failed", resp.Status)
	assert.Nil(t, resp.Scores)
	assert.Empty(t, resp.Strengths)
	assert.Empty(t, resp.Gaps)
	assert.Empty(t, resp.Advice)
	assert.Empty(t, resp.Annotations)
}

func TestGetEvaluation_NotReady(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	userID := seedUser(t, b)
	questionID := seedQuestion(t, b)
	sessionID := seedSession(t, b, userID, questionID) // status=active

	_, err := b.GetEvaluation(ctx, sessionID, userID)
	require.ErrorIs(t, err, ErrEvaluationNotReady)
}

func TestGetEvaluation_WrongUser(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	ownerID := seedUser(t, b)
	otherID := seedUser(t, b)
	questionID := seedQuestion(t, b)
	sessionID := seedSession(t, b, ownerID, questionID)
	setStatus(t, b, sessionID, "reviewed")

	_, err := b.GetEvaluation(ctx, sessionID, otherID)
	require.ErrorIs(t, err, ErrSessionNotOwned)
}

func TestGetEvaluation_ReviewedButMissingEvaluation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	userID := seedUser(t, b)
	questionID := seedQuestion(t, b)
	sessionID := seedSession(t, b, userID, questionID)
	setStatus(t, b, sessionID, "reviewed")
	// No evaluation row inserted.

	_, err := b.GetEvaluation(ctx, sessionID, userID)
	require.ErrorIs(t, err, ErrEvaluationNotReady)
}

// ---------------------------------------------------------------------------
// RetryEvaluation
// ---------------------------------------------------------------------------

func TestRetryEvaluation_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	userID := seedUser(t, b)
	questionID := seedQuestion(t, b)
	sessionID := seedSession(t, b, userID, questionID)
	setStatus(t, b, sessionID, "evaluation_failed")

	err := b.RetryEvaluation(ctx, sessionID, userID)
	require.NoError(t, err)

	// Verify session status updated to "evaluating".
	session, err := db.New(b.pool).GetSession(ctx, sessionID)
	require.NoError(t, err)
	assert.Equal(t, "evaluating", session.Status)

	// Verify river job was enqueued.
	jobtest.AssertJobEnqueued(t, b.pool, "evaluate_session", 1)

	// Verify the job args contain the correct session ID.
	jobs := riverJobs(t, b, "evaluate_session")
	require.Len(t, jobs, 1)
	var args struct {
		SessionID uuid.UUID `json:"session_id"`
	}
	require.NoError(t, json.Unmarshal(jobs[0], &args))
	assert.Equal(t, sessionID, args.SessionID)
}

func TestRetryEvaluation_NotFailed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	userID := seedUser(t, b)
	questionID := seedQuestion(t, b)
	sessionID := seedSession(t, b, userID, questionID)
	setStatus(t, b, sessionID, "reviewed")

	err := b.RetryEvaluation(ctx, sessionID, userID)
	require.ErrorIs(t, err, ErrNotEvaluationFailed)
}

func TestRetryEvaluation_WrongUser(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	ownerID := seedUser(t, b)
	otherID := seedUser(t, b)
	questionID := seedQuestion(t, b)
	sessionID := seedSession(t, b, ownerID, questionID)
	setStatus(t, b, sessionID, "evaluation_failed")

	err := b.RetryEvaluation(ctx, sessionID, otherID)
	require.ErrorIs(t, err, ErrSessionNotOwned)
}
