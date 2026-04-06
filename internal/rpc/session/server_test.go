package session_test

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
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/backend"
	"github.com/btc/drill/internal/db"
	drillv1 "github.com/btc/drill/internal/pb/drill/v1"
	"github.com/btc/drill/internal/pb/drill/v1/drillv1connect"
	"github.com/btc/drill/internal/rpc"
	"github.com/btc/drill/internal/rpc/session"
	"github.com/btc/drill/internal/testutil"
)

// authedClient creates a Connect SessionServiceClient with the session cookie set.
func authedClient(t *testing.T, srvURL string, rawToken string) drillv1connect.SessionServiceClient {
	t.Helper()
	u, err := url.Parse(srvURL)
	require.NoError(t, err)
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	jar.SetCookies(u, []*http.Cookie{{
		Name:  auth.SessionCookieName,
		Value: rawToken,
	}})
	return drillv1connect.NewSessionServiceClient(
		&http.Client{Jar: jar},
		srvURL,
	)
}

// seedQuestion inserts a seed question and returns its ID.
func seedQuestion(t *testing.T, b *backend.Backend) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	queries := db.New(b.Pool())
	id, err := queries.InsertQuestion(ctx, db.InsertQuestionParams{
		UserID:         pgtype.UUID{},
		Title:          "Test Question",
		Prompt:         "Design a test system",
		Difficulty:     "medium",
		Tags:           []string{"testing"},
		Source:         "seed",
		CoachRationale: pgtype.Text{},
	})
	require.NoError(t, err)
	return id
}

// startSessionServer creates the Connect handler with auth interceptor,
// starts an httptest.Server, and returns its URL.
func startSessionServer(t *testing.T, b *backend.Backend) string {
	t.Helper()
	_, h := drillv1connect.NewSessionServiceHandler(
		session.NewServer(b),
		connect.WithInterceptors(rpc.AuthInterceptor(b)),
	)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv.URL
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestListSessions_Unauthenticated(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startSessionServer(t, b)

	client := drillv1connect.NewSessionServiceClient(&http.Client{}, srvURL)
	_, err := client.ListSessions(context.Background(), connect.NewRequest(&drillv1.ListSessionsRequest{}))
	require.Error(t, err)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

func TestListSessions_Empty(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startSessionServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	resp, err := client.ListSessions(context.Background(), connect.NewRequest(&drillv1.ListSessionsRequest{}))
	require.NoError(t, err)
	require.Empty(t, resp.Msg.Sessions, "expected no sessions")
}

func TestCreateSession_And_GetSession(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	questionID := seedQuestion(t, b)
	srvURL := startSessionServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	// Create
	createResp, err := client.CreateSession(context.Background(), connect.NewRequest(&drillv1.CreateSessionRequest{
		QuestionId:      questionID.String(),
		DurationMinutes: 15,
		TtsEnabled:      true,
	}))
	require.NoError(t, err)
	require.NotNil(t, createResp.Msg.Session)
	require.Equal(t, drillv1.SessionStatus_SESSION_STATUS_ACTIVE, createResp.Msg.Session.Status)
	require.Equal(t, int32(15), createResp.Msg.Session.ConfigDurationMinutes)
	require.True(t, createResp.Msg.Session.ConfigTtsEnabled)

	sessionID := createResp.Msg.Session.Id

	// Get — should return the same session with question fields
	getResp, err := client.GetSession(context.Background(), connect.NewRequest(&drillv1.GetSessionRequest{
		Id: sessionID,
	}))
	require.NoError(t, err)
	require.Equal(t, sessionID, getResp.Msg.Session.Id)
	require.Equal(t, "Test Question", getResp.Msg.Session.QuestionTitle)
	require.Equal(t, drillv1.SessionStatus_SESSION_STATUS_ACTIVE, getResp.Msg.Session.Status)

	// List — should contain the session
	listResp, err := client.ListSessions(context.Background(), connect.NewRequest(&drillv1.ListSessionsRequest{}))
	require.NoError(t, err)
	require.Len(t, listResp.Msg.Sessions, 1)
	require.Equal(t, sessionID, listResp.Msg.Sessions[0].Id)
	require.Equal(t, "Test Question", listResp.Msg.Sessions[0].QuestionTitle)
}

func TestCreateSession_InvalidDuration(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	questionID := seedQuestion(t, b)
	srvURL := startSessionServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	_, err := client.CreateSession(context.Background(), connect.NewRequest(&drillv1.CreateSessionRequest{
		QuestionId:      questionID.String(),
		DurationMinutes: 0,
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestCreateSession_InvalidQuestionID(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startSessionServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	_, err := client.CreateSession(context.Background(), connect.NewRequest(&drillv1.CreateSessionRequest{
		QuestionId:      "not-a-uuid",
		DurationMinutes: 15,
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestGetSession_NotFound(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startSessionServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	_, err := client.GetSession(context.Background(), connect.NewRequest(&drillv1.GetSessionRequest{
		Id: uuid.New().String(),
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestGetSession_InvalidID(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startSessionServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	_, err := client.GetSession(context.Background(), connect.NewRequest(&drillv1.GetSessionRequest{
		Id: "not-valid",
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

// ---------------------------------------------------------------------------
// GetTranscript tests
// ---------------------------------------------------------------------------

func TestGetTranscript_Success(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	questionID := seedQuestion(t, b)
	srvURL := startSessionServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	// Create a session.
	createResp, err := client.CreateSession(context.Background(), connect.NewRequest(&drillv1.CreateSessionRequest{
		QuestionId:      questionID.String(),
		DurationMinutes: 15,
	}))
	require.NoError(t, err)
	sessionID, err := uuid.Parse(createResp.Msg.Session.Id)
	require.NoError(t, err)

	// Insert two messages directly.
	ctx := context.Background()
	q := db.New(b.Pool())
	_, err = q.InsertMessage(ctx, db.InsertMessageParams{
		ID:        uuid.New(),
		SessionID: sessionID,
		Seq:       1,
		Role:      "interviewer",
		Content:   "Hello candidate",
	})
	require.NoError(t, err)
	_, err = q.InsertMessage(ctx, db.InsertMessageParams{
		ID:          uuid.New(),
		SessionID:   sessionID,
		Seq:         2,
		Role:        "candidate",
		Content:     "Hello interviewer",
		InputMethod: pgtype.Text{String: "voice", Valid: true},
		AudioUrl:    pgtype.Text{String: "https://example.com/audio.wav", Valid: true},
	})
	require.NoError(t, err)

	resp, err := client.GetTranscript(context.Background(), connect.NewRequest(&drillv1.GetTranscriptRequest{
		SessionId: sessionID.String(),
	}))
	require.NoError(t, err)
	require.Len(t, resp.Msg.Messages, 2)
	require.Equal(t, int32(1), resp.Msg.Messages[0].Seq)
	require.Equal(t, "interviewer", resp.Msg.Messages[0].Role)
	require.Equal(t, "Hello candidate", resp.Msg.Messages[0].Content)
	require.Nil(t, resp.Msg.Messages[0].InputMethod)
	require.Nil(t, resp.Msg.Messages[0].AudioUrl)
	require.Equal(t, int32(2), resp.Msg.Messages[1].Seq)
	require.Equal(t, "candidate", resp.Msg.Messages[1].Role)
	require.NotNil(t, resp.Msg.Messages[1].InputMethod)
	require.Equal(t, "voice", *resp.Msg.Messages[1].InputMethod)
	require.NotNil(t, resp.Msg.Messages[1].AudioUrl)
	require.Equal(t, "https://example.com/audio.wav", *resp.Msg.Messages[1].AudioUrl)
	require.NotNil(t, resp.Msg.Messages[0].CreateTime)
}

func TestGetTranscript_Unauthenticated(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startSessionServer(t, b)
	client := drillv1connect.NewSessionServiceClient(&http.Client{}, srvURL)

	_, err := client.GetTranscript(context.Background(), connect.NewRequest(&drillv1.GetTranscriptRequest{
		SessionId: uuid.New().String(),
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

func TestGetTranscript_InvalidID(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startSessionServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	_, err := client.GetTranscript(context.Background(), connect.NewRequest(&drillv1.GetTranscriptRequest{
		SessionId: "not-a-uuid",
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestGetTranscript_NotOwned(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	questionID := seedQuestion(t, b)
	srvURL := startSessionServer(t, b)

	// Create session as user1.
	token1 := testutil.SignupAndLogin(t, b)
	client1 := authedClient(t, srvURL, token1)
	createResp, err := client1.CreateSession(context.Background(), connect.NewRequest(&drillv1.CreateSessionRequest{
		QuestionId:      questionID.String(),
		DurationMinutes: 15,
	}))
	require.NoError(t, err)
	sessionID := createResp.Msg.Session.Id

	// Create a second user and try to read user1's transcript.
	ctx := context.Background()
	result2, err := b.Signup(ctx, backend.SignupParams{
		Email:       "user2-notowned@example.com",
		Password:    "testpassword123",
		DisplayName: "User 2",
	})
	require.NoError(t, err)
	loginResult2, err := b.Login(ctx, backend.LoginParams{
		Email:    "user2-notowned@example.com",
		Password: "testpassword123",
	})
	require.NoError(t, err)
	_ = result2

	client2 := authedClient(t, srvURL, loginResult2.Token)
	_, err = client2.GetTranscript(context.Background(), connect.NewRequest(&drillv1.GetTranscriptRequest{
		SessionId: sessionID,
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

// ---------------------------------------------------------------------------
// ArchiveSessions tests
// ---------------------------------------------------------------------------

func TestArchiveSessions_Success(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	questionID := seedQuestion(t, b)
	srvURL := startSessionServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	// Create a session.
	createResp, err := client.CreateSession(context.Background(), connect.NewRequest(&drillv1.CreateSessionRequest{
		QuestionId:      questionID.String(),
		DurationMinutes: 15,
	}))
	require.NoError(t, err)
	sessionID := createResp.Msg.Session.Id

	// Archive it.
	_, err = client.ArchiveSessions(context.Background(), connect.NewRequest(&drillv1.ArchiveSessionsRequest{
		SessionIds: []string{sessionID},
		Archive:    true,
	}))
	require.NoError(t, err)
	// GetSession should now show ArchiveTime set.
	getResp, err := client.GetSession(context.Background(), connect.NewRequest(&drillv1.GetSessionRequest{
		Id: sessionID,
	}))
	require.NoError(t, err)
	require.NotNil(t, getResp.Msg.Session.ArchiveTime, "expected archive_time to be set")
}

func TestArchiveSessions_InvalidUUID(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startSessionServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	_, err := client.ArchiveSessions(context.Background(), connect.NewRequest(&drillv1.ArchiveSessionsRequest{
		SessionIds: []string{"not-a-uuid"},
		Archive:    true,
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestArchiveSessions_Unauthenticated(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startSessionServer(t, b)
	client := drillv1connect.NewSessionServiceClient(&http.Client{}, srvURL)

	_, err := client.ArchiveSessions(context.Background(), connect.NewRequest(&drillv1.ArchiveSessionsRequest{
		SessionIds: []string{uuid.New().String()},
		Archive:    true,
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

func TestStatusEnumMapping(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	questionID := seedQuestion(t, b)
	srvURL := startSessionServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	// Create — should be ACTIVE
	createResp, err := client.CreateSession(context.Background(), connect.NewRequest(&drillv1.CreateSessionRequest{
		QuestionId:      questionID.String(),
		DurationMinutes: 15,
	}))
	require.NoError(t, err)
	require.Equal(t, drillv1.SessionStatus_SESSION_STATUS_ACTIVE, createResp.Msg.Session.Status)

	// Verify in list
	listResp, err := client.ListSessions(context.Background(), connect.NewRequest(&drillv1.ListSessionsRequest{}))
	require.NoError(t, err)
	require.Equal(t, drillv1.SessionStatus_SESSION_STATUS_ACTIVE, listResp.Msg.Sessions[0].Status)
}

func TestListSessions_ScoreOverall(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	questionID := seedQuestion(t, b)
	srvURL := startSessionServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	// Create a session.
	createResp, err := client.CreateSession(context.Background(), connect.NewRequest(&drillv1.CreateSessionRequest{
		QuestionId:      questionID.String(),
		DurationMinutes: 15,
	}))
	require.NoError(t, err)
	sessionID, err := uuid.Parse(createResp.Msg.Session.Id)
	require.NoError(t, err)

	// Before evaluation: score_overall should be nil.
	listResp, err := client.ListSessions(context.Background(), connect.NewRequest(&drillv1.ListSessionsRequest{}))
	require.NoError(t, err)
	require.Len(t, listResp.Msg.Sessions, 1)
	require.Nil(t, listResp.Msg.Sessions[0].ScoreOverall, "no evaluation yet — score should be nil")

	// Mark session as reviewed and insert an evaluation.
	ctx := context.Background()
	q := db.New(b.Pool())
	err = q.UpdateSessionStatusOnly(ctx, db.UpdateSessionStatusOnlyParams{
		ID: sessionID, Status: "reviewed",
	})
	require.NoError(t, err)

	_, err = q.InsertEvaluation(ctx, db.InsertEvaluationParams{
		SessionID:          sessionID,
		ScoreRequirements:  3,
		ScoreArchitecture:  4,
		ScoreDeepDive:      3,
		ScoreScalability:   4,
		ScoreCommunication: 3,
		ScoreOverall:       4,
		Strengths:          []byte(`["good architecture"]`),
		Gaps:               []byte(`["missed caching"]`),
		Advice:             "Focus on caching strategies",
	})
	require.NoError(t, err)

	// After evaluation: score_overall should be 4.
	listResp2, err := client.ListSessions(context.Background(), connect.NewRequest(&drillv1.ListSessionsRequest{}))
	require.NoError(t, err)
	require.Len(t, listResp2.Msg.Sessions, 1)
	require.NotNil(t, listResp2.Msg.Sessions[0].ScoreOverall, "evaluation exists — score should be present")
	require.Equal(t, int32(4), *listResp2.Msg.Sessions[0].ScoreOverall)
}
