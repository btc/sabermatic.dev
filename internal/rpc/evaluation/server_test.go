package evaluation_test

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/backend"
	"github.com/btc/drill/internal/db"
	drillv1 "github.com/btc/drill/internal/pb/drill/v1"
	"github.com/btc/drill/internal/pb/drill/v1/drillv1connect"
	"github.com/btc/drill/internal/rpc"
	"github.com/btc/drill/internal/rpc/evaluation"
	"github.com/btc/drill/internal/testutil"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// authedClient creates a Connect EvaluationServiceClient with the session
// cookie set via a cookie jar.
func authedClient(t *testing.T, srvURL string, rawToken string) drillv1connect.EvaluationServiceClient {
	t.Helper()
	u, err := url.Parse(srvURL)
	require.NoError(t, err)
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	jar.SetCookies(u, []*http.Cookie{{
		Name:  auth.SessionCookieName,
		Value: rawToken,
	}})
	return drillv1connect.NewEvaluationServiceClient(
		&http.Client{Jar: jar},
		srvURL,
	)
}

// startEvaluationServer creates the Connect handler with auth interceptor,
// starts an httptest.Server, and returns its URL.
func startEvaluationServer(t *testing.T, b *backend.Backend) string {
	t.Helper()
	_, h := drillv1connect.NewEvaluationServiceHandler(
		evaluation.NewServer(b),
		connect.WithInterceptors(rpc.AuthInterceptor(b)),
	)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv.URL
}

// userIDFromToken resolves the user ID from a raw session token by hashing it
// and looking up the auth session in the database.
func userIDFromToken(t *testing.T, b *backend.Backend, rawToken string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	tokenHash := auth.HashSessionToken(rawToken)
	row, err := db.New(b.Pool()).GetAuthSessionByToken(ctx, tokenHash)
	require.NoError(t, err)
	return row.UserID
}

// seedQuestion inserts a question via sqlc and returns its UUID.
func seedQuestion(t *testing.T, b *backend.Backend) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id, err := db.New(b.Pool()).InsertQuestion(ctx, db.InsertQuestionParams{
		UserID:         pgtype.UUID{},
		Title:          "Design a URL Shortener",
		Prompt:         "Design a URL shortening service like bit.ly.",
		Difficulty:     "medium",
		Tags:           []string{"system-design"},
		Source:         "seed",
		CoachRationale: pgtype.Text{},
	})
	require.NoError(t, err)
	return id
}

// seedSession creates an interview session via the production backend method.
func seedSession(t *testing.T, b *backend.Backend, userID, questionID uuid.UUID) db.InterviewSession {
	t.Helper()
	ctx := context.Background()
	s, err := b.CreateSession(ctx, backend.CreateSessionParams{
		UserID:          userID,
		QuestionID:      questionID,
		DurationMinutes: 15,
		TTSEnabled:      false,
	})
	require.NoError(t, err)
	return s
}

// setSessionStatus updates a session's status directly via sqlc.
func setSessionStatus(t *testing.T, b *backend.Backend, sessionID uuid.UUID, status string) {
	t.Helper()
	err := db.New(b.Pool()).UpdateSessionStatusOnly(context.Background(), db.UpdateSessionStatusOnlyParams{
		ID:     sessionID,
		Status: status,
	})
	require.NoError(t, err)
}

// seedEvaluation inserts an evaluation row and returns its ID.
func seedEvaluation(t *testing.T, b *backend.Backend, sessionID uuid.UUID) uuid.UUID {
	t.Helper()
	evalID, err := db.New(b.Pool()).InsertEvaluation(context.Background(), db.InsertEvaluationParams{
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

// seedMessage inserts a message into a session.
func seedMessage(t *testing.T, b *backend.Backend, sessionID uuid.UUID, seq int32, role, content string) db.Message {
	t.Helper()
	msg, err := db.New(b.Pool()).InsertMessage(context.Background(), db.InsertMessageParams{
		ID:          uuid.New(),
		SessionID:   sessionID,
		Seq:         seq,
		Role:        role,
		Content:     content,
		InputMethod: pgtype.Text{String: "text", Valid: true},
	})
	require.NoError(t, err)
	return msg
}

// seedAnnotation inserts an annotation for the given evaluation and message.
func seedAnnotation(t *testing.T, b *backend.Backend, evalID, msgID uuid.UUID, annType, content string) {
	t.Helper()
	err := db.New(b.Pool()).InsertAnnotation(context.Background(), db.InsertAnnotationParams{
		EvaluationID:   evalID,
		MessageID:      msgID,
		AnnotationType: annType,
		Content:        content,
	})
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestGetEvaluation_Unauthenticated(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := testutil.NewTestBackend(t)
	srvURL := startEvaluationServer(t, b)

	// No cookie — plain HTTP client.
	client := drillv1connect.NewEvaluationServiceClient(&http.Client{}, srvURL)
	_, err := client.GetEvaluation(context.Background(), connect.NewRequest(&drillv1.GetEvaluationRequest{
		SessionId: uuid.New().String(),
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

func TestGetEvaluation_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := testutil.NewTestBackend(t)
	token := testutil.SignupAndLogin(t, b)
	userID := userIDFromToken(t, b, token)
	questionID := seedQuestion(t, b)
	session := seedSession(t, b, userID, questionID)
	setSessionStatus(t, b, session.ID, "reviewed")

	msg1 := seedMessage(t, b, session.ID, 1, "interviewer", "Tell me about requirements.")
	evalID := seedEvaluation(t, b, session.ID)
	seedAnnotation(t, b, evalID, msg1.ID, "strength", "Good opening question.")

	srvURL := startEvaluationServer(t, b)
	client := authedClient(t, srvURL, token)

	resp, err := client.GetEvaluation(context.Background(), connect.NewRequest(&drillv1.GetEvaluationRequest{
		SessionId: session.ID.String(),
	}))
	require.NoError(t, err)
	require.NotNil(t, resp.Msg.Evaluation)

	eval := resp.Msg.Evaluation
	assert.Equal(t, "reviewed", eval.Status)
	assert.NotNil(t, eval.Scores)
	assert.Equal(t, int32(3), eval.Scores.Requirements)
	assert.Equal(t, int32(4), eval.Scores.Architecture)
	assert.Equal(t, int32(2), eval.Scores.DeepDive)
	assert.Equal(t, int32(3), eval.Scores.Scalability)
	assert.Equal(t, int32(4), eval.Scores.Communication)
	assert.Equal(t, int32(3), eval.Scores.Overall)
	assert.Equal(t, []string{"Good requirements gathering"}, eval.Strengths)
	assert.Equal(t, []string{"Missing cache layer"}, eval.Gaps)
	assert.Equal(t, "Focus on depth.", eval.Advice)
	require.Len(t, eval.Annotations, 1)
	assert.Equal(t, int32(1), eval.Annotations[0].MessageSeq)
	assert.Equal(t, drillv1.AnnotationType_ANNOTATION_TYPE_STRENGTH, eval.Annotations[0].Type)
	assert.Equal(t, "Good opening question.", eval.Annotations[0].Content)
}

func TestGetEvaluation_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := testutil.NewTestBackend(t)
	token := testutil.SignupAndLogin(t, b)
	srvURL := startEvaluationServer(t, b)
	client := authedClient(t, srvURL, token)

	_, err := client.GetEvaluation(context.Background(), connect.NewRequest(&drillv1.GetEvaluationRequest{
		SessionId: uuid.New().String(),
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestRetryEvaluation_Unauthenticated(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := testutil.NewTestBackend(t)
	srvURL := startEvaluationServer(t, b)

	// No cookie — plain HTTP client.
	client := drillv1connect.NewEvaluationServiceClient(&http.Client{}, srvURL)
	_, err := client.RetryEvaluation(context.Background(), connect.NewRequest(&drillv1.RetryEvaluationRequest{
		SessionId: uuid.New().String(),
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

func TestRetryEvaluation_FailedPrecondition(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := testutil.NewTestBackend(t)
	token := testutil.SignupAndLogin(t, b)
	userID := userIDFromToken(t, b, token)
	questionID := seedQuestion(t, b)
	session := seedSession(t, b, userID, questionID)
	setSessionStatus(t, b, session.ID, "reviewed") // Not evaluation_failed

	srvURL := startEvaluationServer(t, b)
	client := authedClient(t, srvURL, token)

	_, err := client.RetryEvaluation(context.Background(), connect.NewRequest(&drillv1.RetryEvaluationRequest{
		SessionId: session.ID.String(),
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
}

func TestRetryEvaluation_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := testutil.NewTestBackend(t)
	token := testutil.SignupAndLogin(t, b)
	userID := userIDFromToken(t, b, token)
	questionID := seedQuestion(t, b)
	session := seedSession(t, b, userID, questionID)
	setSessionStatus(t, b, session.ID, "evaluation_failed")

	srvURL := startEvaluationServer(t, b)
	client := authedClient(t, srvURL, token)

	_, err := client.RetryEvaluation(context.Background(), connect.NewRequest(&drillv1.RetryEvaluationRequest{
		SessionId: session.ID.String(),
	}))
	require.NoError(t, err)
}

func TestGetEvaluation_EvaluationNotReady(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := testutil.NewTestBackend(t)
	token := testutil.SignupAndLogin(t, b)
	userID := userIDFromToken(t, b, token)
	questionID := seedQuestion(t, b)
	// Session starts in "active" status — not reviewed or evaluation_failed.
	session := seedSession(t, b, userID, questionID)

	srvURL := startEvaluationServer(t, b)
	client := authedClient(t, srvURL, token)

	_, err := client.GetEvaluation(context.Background(), connect.NewRequest(&drillv1.GetEvaluationRequest{
		SessionId: session.ID.String(),
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}
