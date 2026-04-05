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
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := testutil.NewTestBackend(t)
	srvURL := startSessionServer(t, b)

	client := drillv1connect.NewSessionServiceClient(&http.Client{}, srvURL)
	_, err := client.ListSessions(context.Background(), connect.NewRequest(&drillv1.ListSessionsRequest{}))
	require.Error(t, err)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

func TestListSessions_Empty(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := testutil.NewTestBackend(t)
	srvURL := startSessionServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	resp, err := client.ListSessions(context.Background(), connect.NewRequest(&drillv1.ListSessionsRequest{}))
	require.NoError(t, err)
	require.Empty(t, resp.Msg.Sessions, "expected no sessions")
}

func TestCreateSession_And_GetSession(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := testutil.NewTestBackend(t)
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
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := testutil.NewTestBackend(t)
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
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := testutil.NewTestBackend(t)
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
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := testutil.NewTestBackend(t)
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
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := testutil.NewTestBackend(t)
	srvURL := startSessionServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	_, err := client.GetSession(context.Background(), connect.NewRequest(&drillv1.GetSessionRequest{
		Id: "not-valid",
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestGetTranscript_Empty(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := testutil.NewTestBackend(t)
	questionID := seedQuestion(t, b)
	srvURL := startSessionServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	// Create a session
	createResp, err := client.CreateSession(context.Background(), connect.NewRequest(&drillv1.CreateSessionRequest{
		QuestionId:      questionID.String(),
		DurationMinutes: 15,
	}))
	require.NoError(t, err)

	// Transcript should be empty
	resp, err := client.GetTranscript(context.Background(), connect.NewRequest(&drillv1.GetTranscriptRequest{
		SessionId: createResp.Msg.Session.Id,
	}))
	require.NoError(t, err)
	require.Empty(t, resp.Msg.Messages, "expected no messages")
}

func TestGetTranscript_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := testutil.NewTestBackend(t)
	srvURL := startSessionServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	_, err := client.GetTranscript(context.Background(), connect.NewRequest(&drillv1.GetTranscriptRequest{
		SessionId: uuid.New().String(),
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestArchiveSessions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := testutil.NewTestBackend(t)
	questionID := seedQuestion(t, b)
	srvURL := startSessionServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	// Create a session
	createResp, err := client.CreateSession(context.Background(), connect.NewRequest(&drillv1.CreateSessionRequest{
		QuestionId:      questionID.String(),
		DurationMinutes: 15,
	}))
	require.NoError(t, err)
	sessionID := createResp.Msg.Session.Id

	// Archive it
	archResp, err := client.ArchiveSessions(context.Background(), connect.NewRequest(&drillv1.ArchiveSessionsRequest{
		SessionIds: []string{sessionID},
		Archive:    true,
	}))
	require.NoError(t, err)
	require.Equal(t, int32(1), archResp.Msg.UpdatedCount)

	// Verify it's archived via GetSession
	getResp, err := client.GetSession(context.Background(), connect.NewRequest(&drillv1.GetSessionRequest{
		Id: sessionID,
	}))
	require.NoError(t, err)
	require.NotNil(t, getResp.Msg.Session.ArchiveTime, "session should have archive_time set")

	// Unarchive it
	unarchResp, err := client.ArchiveSessions(context.Background(), connect.NewRequest(&drillv1.ArchiveSessionsRequest{
		SessionIds: []string{sessionID},
		Archive:    false,
	}))
	require.NoError(t, err)
	require.Equal(t, int32(1), unarchResp.Msg.UpdatedCount)

	// Verify it's unarchived
	getResp2, err := client.GetSession(context.Background(), connect.NewRequest(&drillv1.GetSessionRequest{
		Id: sessionID,
	}))
	require.NoError(t, err)
	require.Nil(t, getResp2.Msg.Session.ArchiveTime, "session should have no archive_time after unarchive")
}

func TestArchiveSessions_Idempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := testutil.NewTestBackend(t)
	questionID := seedQuestion(t, b)
	srvURL := startSessionServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	// Create and archive
	createResp, err := client.CreateSession(context.Background(), connect.NewRequest(&drillv1.CreateSessionRequest{
		QuestionId:      questionID.String(),
		DurationMinutes: 15,
	}))
	require.NoError(t, err)
	sessionID := createResp.Msg.Session.Id

	_, err = client.ArchiveSessions(context.Background(), connect.NewRequest(&drillv1.ArchiveSessionsRequest{
		SessionIds: []string{sessionID},
		Archive:    true,
	}))
	require.NoError(t, err)

	// Archive again — should return 0 updated (already archived)
	resp, err := client.ArchiveSessions(context.Background(), connect.NewRequest(&drillv1.ArchiveSessionsRequest{
		SessionIds: []string{sessionID},
		Archive:    true,
	}))
	require.NoError(t, err)
	require.Equal(t, int32(0), resp.Msg.UpdatedCount)
}

func TestStatusEnumMapping(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := testutil.NewTestBackend(t)
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
