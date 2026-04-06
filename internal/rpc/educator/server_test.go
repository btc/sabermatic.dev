package educator_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/backend"
	"github.com/btc/drill/internal/db"
	drillv1 "github.com/btc/drill/internal/pb/drill/v1"
	"github.com/btc/drill/internal/pb/drill/v1/drillv1connect"
	"github.com/btc/drill/internal/rpc"
	"github.com/btc/drill/internal/rpc/educator"
	"github.com/btc/drill/internal/testutil"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// authedClient creates a Connect EducatorServiceClient with the session cookie.
func authedClient(t *testing.T, srvURL string, rawToken string) drillv1connect.EducatorServiceClient {
	t.Helper()
	u, err := url.Parse(srvURL)
	require.NoError(t, err)
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	jar.SetCookies(u, []*http.Cookie{{
		Name:  auth.SessionCookieName,
		Value: rawToken,
	}})
	return drillv1connect.NewEducatorServiceClient(
		&http.Client{Jar: jar},
		srvURL,
	)
}

// startEducatorServer creates the Connect handler with auth interceptor,
// starts an httptest.Server, and returns its URL.
func startEducatorServer(t *testing.T, b *backend.Backend) string {
	t.Helper()
	_, h := drillv1connect.NewEducatorServiceHandler(
		educator.NewServer(b),
		connect.WithInterceptors(rpc.AuthInterceptor(b)),
	)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv.URL
}

// userIDFromToken resolves the raw session token to a user ID.
func userIDFromToken(t *testing.T, b *backend.Backend, rawToken string) uuid.UUID {
	t.Helper()
	tokenHash := auth.HashSessionToken(rawToken)
	user, err := b.AuthenticateSession(context.Background(), tokenHash)
	require.NoError(t, err)
	return user.ID
}

// seedQuestion inserts a seed question and returns its ID.
func seedQuestion(t *testing.T, b *backend.Backend) uuid.UUID {
	t.Helper()
	q := db.New(b.Pool())
	id, err := q.InsertQuestion(context.Background(), db.InsertQuestionParams{
		Title:      "Test Question",
		Prompt:     "Describe a distributed system.",
		Difficulty: "medium",
		Tags:       []string{"system-design"},
		Source:     "seed",
	})
	require.NoError(t, err)
	return id
}

// seedReviewedSession creates a session, seeds an evaluation, and sets the
// session status to "reviewed". Returns the session ID.
func seedReviewedSession(t *testing.T, b *backend.Backend, userID, questionID uuid.UUID) uuid.UUID {
	t.Helper()
	q := db.New(b.Pool())
	sess, err := q.CreateSession(context.Background(), db.CreateSessionParams{
		UserID:                userID,
		QuestionID:            questionID,
		ConfigDurationMinutes: 30,
	})
	require.NoError(t, err)

	// Insert a minimal evaluation to allow "reviewed" status.
	// Score CHECK constraint requires values BETWEEN 1 AND 5.
	_, err = q.InsertEvaluation(context.Background(), db.InsertEvaluationParams{
		SessionID:          sess.ID,
		ScoreRequirements:  4,
		ScoreArchitecture:  4,
		ScoreDeepDive:      4,
		ScoreScalability:   4,
		ScoreCommunication: 4,
		ScoreOverall:       4,
		Strengths:          json.RawMessage(`["good"]`),
		Gaps:               json.RawMessage(`["none"]`),
		Advice:             "Keep going.",
	})
	require.NoError(t, err)

	err = q.UpdateSessionStatusOnly(context.Background(), db.UpdateSessionStatusOnlyParams{
		ID:     sess.ID,
		Status: "reviewed",
	})
	require.NoError(t, err)

	return sess.ID
}

// seedEducatorAnalysis inserts an educator_analyses row with the given status.
func seedEducatorAnalysis(t *testing.T, b *backend.Backend, sessionID uuid.UUID, status, modelAnswer, gapDeepDives string) {
	t.Helper()
	q := db.New(b.Pool())
	analysisID, err := q.InsertEducatorAnalysis(context.Background(), sessionID)
	require.NoError(t, err)

	if status == "completed" {
		err = q.UpdateEducatorAnalysisContent(context.Background(), db.UpdateEducatorAnalysisContentParams{
			ID:           analysisID,
			ModelAnswer:  pgtype.Text{String: modelAnswer, Valid: modelAnswer != ""},
			GapDeepDives: pgtype.Text{String: gapDeepDives, Valid: gapDeepDives != ""},
		})
		require.NoError(t, err)
	} else if status != "generating" {
		err = q.UpdateEducatorAnalysisStatus(context.Background(), db.UpdateEducatorAnalysisStatusParams{
			ID:     analysisID,
			Status: status,
		})
		require.NoError(t, err)
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestGetEducatorAnalysis_Unauthenticated(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := testutil.NewTestBackend(t)
	srvURL := startEducatorServer(t, b)

	// No cookie -- plain HTTP client.
	client := drillv1connect.NewEducatorServiceClient(&http.Client{}, srvURL)
	_, err := client.GetEducatorAnalysis(context.Background(), connect.NewRequest(&drillv1.GetEducatorAnalysisRequest{
		SessionId: uuid.New().String(),
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

func TestGetEducatorAnalysis_NotRequested(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := testutil.NewTestBackend(t)
	srvURL := startEducatorServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)
	userID := userIDFromToken(t, b, token)

	questionID := seedQuestion(t, b)
	sessionID := seedReviewedSession(t, b, userID, questionID)

	resp, err := client.GetEducatorAnalysis(context.Background(), connect.NewRequest(&drillv1.GetEducatorAnalysisRequest{
		SessionId: sessionID.String(),
	}))
	require.NoError(t, err)
	require.NotNil(t, resp.Msg.Analysis)
	require.Equal(t, drillv1.EducatorStatus_EDUCATOR_STATUS_NOT_REQUESTED, resp.Msg.Analysis.Status)
}

func TestGetEducatorAnalysis_Completed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := testutil.NewTestBackend(t)
	srvURL := startEducatorServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)
	userID := userIDFromToken(t, b, token)

	questionID := seedQuestion(t, b)
	sessionID := seedReviewedSession(t, b, userID, questionID)
	seedEducatorAnalysis(t, b, sessionID, "completed", "Model answer.", "Gap deep dives.")

	resp, err := client.GetEducatorAnalysis(context.Background(), connect.NewRequest(&drillv1.GetEducatorAnalysisRequest{
		SessionId: sessionID.String(),
	}))
	require.NoError(t, err)
	require.NotNil(t, resp.Msg.Analysis)
	require.Equal(t, drillv1.EducatorStatus_EDUCATOR_STATUS_COMPLETED, resp.Msg.Analysis.Status)
	require.NotNil(t, resp.Msg.Analysis.ModelAnswer)
	require.Equal(t, "Model answer.", *resp.Msg.Analysis.ModelAnswer)
	require.NotNil(t, resp.Msg.Analysis.GapDeepDives)
	require.Equal(t, "Gap deep dives.", *resp.Msg.Analysis.GapDeepDives)
}

// signupAndLoginAs creates a user with the given email+password and returns the raw token.
func signupAndLoginAs(t *testing.T, b *backend.Backend, email, password string) string {
	t.Helper()
	ctx := context.Background()
	_, err := b.Signup(ctx, backend.SignupParams{
		Email:       email,
		Password:    password,
		DisplayName: "Test User",
	})
	require.NoError(t, err)
	res, err := b.Login(ctx, backend.LoginParams{
		Email:     email,
		Password:  password,
		IP:        "127.0.0.1:12345",
		UserAgent: "test-agent",
	})
	require.NoError(t, err)
	return res.Token
}

func TestRequestEducatorAnalysis_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := testutil.NewTestBackend(t)
	srvURL := startEducatorServer(t, b)
	token := signupAndLoginAs(t, b, "educator-req@example.com", "securepass123")
	client := authedClient(t, srvURL, token)
	userID := userIDFromToken(t, b, token)

	questionID := seedQuestion(t, b)
	sessionID := seedReviewedSession(t, b, userID, questionID)

	_, err := client.RequestEducatorAnalysis(context.Background(), connect.NewRequest(&drillv1.RequestEducatorAnalysisRequest{
		SessionId: sessionID.String(),
	}))
	require.NoError(t, err)
}

func TestGetEducatorAnalysis_WrongUser(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := testutil.NewTestBackend(t)
	srvURL := startEducatorServer(t, b)

	// User A creates a session.
	tokenA := signupAndLoginAs(t, b, "user-a@example.com", "securepass123")
	userAID := userIDFromToken(t, b, tokenA)
	questionID := seedQuestion(t, b)
	sessionID := seedReviewedSession(t, b, userAID, questionID)

	// User B tries to get user A's educator analysis.
	tokenB := signupAndLoginAs(t, b, "user-b@example.com", "securepass456")
	clientB := authedClient(t, srvURL, tokenB)

	_, err := clientB.GetEducatorAnalysis(context.Background(), connect.NewRequest(&drillv1.GetEducatorAnalysisRequest{
		SessionId: sessionID.String(),
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestRequestEducatorAnalysis_Unauthenticated(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := testutil.NewTestBackend(t)
	srvURL := startEducatorServer(t, b)

	client := drillv1connect.NewEducatorServiceClient(&http.Client{}, srvURL)
	_, err := client.RequestEducatorAnalysis(context.Background(), connect.NewRequest(&drillv1.RequestEducatorAnalysisRequest{
		SessionId: uuid.New().String(),
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}
