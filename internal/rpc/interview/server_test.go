package interview_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/ai"
	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/backend"
	"github.com/btc/drill/internal/db"
	drillv1 "github.com/btc/drill/internal/pb/drill/v1"
	"github.com/btc/drill/internal/pb/drill/v1/drillv1connect"
	"github.com/btc/drill/internal/rpc"
	"github.com/btc/drill/internal/rpc/interview"
	"github.com/btc/drill/internal/testutil"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// fakeAnthropicServer returns an httptest.Server that streams Anthropic-format
// SSE events with the given tokens.
func fakeAnthropicServer(t *testing.T, tokens []string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		fmt.Fprintf(w, "event: message_start\n")
		fmt.Fprintf(w, "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_test\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-sonnet-4-20250514\",\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":25,\"output_tokens\":0}}}\n\n")

		fmt.Fprintf(w, "event: content_block_start\n")
		fmt.Fprintf(w, "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")

		for _, token := range tokens {
			fmt.Fprintf(w, "event: content_block_delta\n")
			fmt.Fprintf(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":%q}}\n\n", token)
		}

		fmt.Fprintf(w, "event: content_block_stop\n")
		fmt.Fprintf(w, "data: {\"type\":\"content_block_stop\",\"index\":0}\n\n")

		fmt.Fprintf(w, "event: message_delta\n")
		fmt.Fprintf(w, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":%d}}\n\n", len(tokens))

		fmt.Fprintf(w, "event: message_stop\n")
		fmt.Fprintf(w, "data: {\"type\":\"message_stop\"}\n\n")
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newBackendWithFakeLLM creates a backend with a real Postgres database and a
// fake Anthropic server that returns the given tokens.
func newBackendWithFakeLLM(t *testing.T, tokens []string) (*backend.Backend, *httptest.Server) {
	t.Helper()
	anthropicSrv := fakeAnthropicServer(t, tokens)
	b := pg.NewBackend(t)
	b.ApplyTestOverrides(backend.TestOverrides{
		LLM: ai.NewTestClient(anthropicSrv.URL, b.Pool()),
	})
	return b, anthropicSrv
}

// startInterviewServer creates the ConnectRPC InterviewService handler with
// auth interceptor, starts an httptest.Server, and returns its URL.
func startInterviewServer(t *testing.T, b *backend.Backend) string {
	t.Helper()
	_, h := drillv1connect.NewInterviewServiceHandler(
		interview.NewServer(b),
		connect.WithInterceptors(rpc.AuthInterceptor(b)),
	)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv.URL
}

// authedClient creates a ConnectRPC InterviewServiceClient with the session
// cookie set.
func authedClient(t *testing.T, srvURL string, rawToken string) drillv1connect.InterviewServiceClient {
	t.Helper()
	u, err := url.Parse(srvURL)
	require.NoError(t, err)
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	jar.SetCookies(u, []*http.Cookie{{
		Name:  auth.SessionCookieName,
		Value: rawToken,
	}})
	return drillv1connect.NewInterviewServiceClient(
		&http.Client{Jar: jar},
		srvURL,
	)
}

// seedQuestion inserts a seed question and returns its ID.
func seedQuestion(t *testing.T, b *backend.Backend) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id, err := db.New(b.Pool()).InsertQuestion(ctx, db.InsertQuestionParams{
		UserID:         pgtype.UUID{},
		Title:          "Test Interview Question",
		Prompt:         "Design a distributed cache system",
		Difficulty:     "medium",
		Tags:           []string{"system-design"},
		Source:         "seed",
		CoachRationale: pgtype.Text{},
	})
	require.NoError(t, err)
	return id
}

// createSession creates a session via the backend and returns its ID.
func createSession(t *testing.T, b *backend.Backend, userID, questionID uuid.UUID) uuid.UUID {
	t.Helper()
	session, err := b.CreateSession(context.Background(), backend.CreateSessionParams{
		UserID:          userID,
		QuestionID:      questionID,
		DurationMinutes: 15,
		TTSEnabled:      false,
	})
	require.NoError(t, err)
	return session.ID
}

// submitOpeningTurn calls SubmitTurn with empty text (opening question) and
// drains the stream. Returns collected events.
func submitOpeningTurn(t *testing.T, client drillv1connect.InterviewServiceClient, sessionID string) []*drillv1.TurnEvent {
	t.Helper()
	return submitTurn(t, client, &drillv1.SubmitTurnRequest{
		SessionId: sessionID,
		Input:     &drillv1.SubmitTurnRequest_TextInput{TextInput: &drillv1.TextInput{Content: ""}},
	})
}

// submitTurn calls SubmitTurn and drains the stream. Returns collected events.
func submitTurn(t *testing.T, client drillv1connect.InterviewServiceClient, req *drillv1.SubmitTurnRequest) []*drillv1.TurnEvent {
	t.Helper()
	stream, err := client.SubmitTurn(context.Background(), connect.NewRequest(req))
	require.NoError(t, err)

	var events []*drillv1.TurnEvent
	for stream.Receive() {
		events = append(events, stream.Msg())
	}
	require.NoError(t, stream.Err())
	return events
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestSubmitTurn_Unauthenticated(t *testing.T) {
	t.Parallel()

	b, _ := newBackendWithFakeLLM(t, []string{"Hello"})
	srvURL := startInterviewServer(t, b)

	client := drillv1connect.NewInterviewServiceClient(&http.Client{}, srvURL)
	stream, err := client.SubmitTurn(context.Background(), connect.NewRequest(&drillv1.SubmitTurnRequest{
		SessionId: uuid.New().String(),
	}))

	// Server-streaming may return the error on the call or on Receive.
	if err != nil {
		require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
		return
	}
	// Drain stream expecting an error.
	for stream.Receive() {
	}
	require.Error(t, stream.Err())
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(stream.Err()))
}

func TestSubmitTurn_OpeningQuestion(t *testing.T) {
	t.Parallel()

	tokens := []string{"Welcome", " to", " your", " interview."}
	b, _ := newBackendWithFakeLLM(t, tokens)
	srvURL := startInterviewServer(t, b)
	rawToken := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, rawToken)

	questionID := seedQuestion(t, b)

	// Resolve user ID from backend.
	ctx := context.Background()
	user, err := db.New(b.Pool()).GetUserByEmail(ctx, "testuser@example.com")
	require.NoError(t, err)

	sessionID := createSession(t, b, user.ID, questionID)

	events := submitOpeningTurn(t, client, sessionID.String())

	// Verify at least one InterviewerToken and one InterviewerDone event.
	var tokenCount, doneCount int
	for _, e := range events {
		switch e.Event.(type) {
		case *drillv1.TurnEvent_InterviewerToken:
			tokenCount++
		case *drillv1.TurnEvent_InterviewerDone:
			doneCount++
		}
	}
	require.Greater(t, tokenCount, 0, "expected at least one InterviewerToken event")
	require.Equal(t, 1, doneCount, "expected exactly one InterviewerDone event")
}

func TestGetSessionState_ReturnsMessages(t *testing.T) {
	t.Parallel()

	tokens := []string{"Tell me about caching."}
	b, _ := newBackendWithFakeLLM(t, tokens)
	srvURL := startInterviewServer(t, b)
	rawToken := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, rawToken)

	questionID := seedQuestion(t, b)
	ctx := context.Background()
	user, err := db.New(b.Pool()).GetUserByEmail(ctx, "testuser@example.com")
	require.NoError(t, err)

	sessionID := createSession(t, b, user.ID, questionID)

	// Submit opening turn.
	submitOpeningTurn(t, client, sessionID.String())

	// GetSessionState should return the interviewer message.
	resp, err := client.GetSessionState(ctx, connect.NewRequest(&drillv1.GetSessionStateRequest{
		SessionId: sessionID.String(),
	}))
	require.NoError(t, err)

	require.NotNil(t, resp.Msg.SessionInfo)
	require.Equal(t, sessionID.String(), resp.Msg.SessionInfo.SessionId)
	require.Equal(t, "Test Interview Question", resp.Msg.SessionInfo.QuestionTitle)
	require.Len(t, resp.Msg.Messages, 1, "expected 1 interviewer message after opening")
	require.Equal(t, "interviewer", resp.Msg.Messages[0].Role)
	require.Equal(t, "Tell me about caching.", resp.Msg.Messages[0].Content)
	require.Equal(t, drillv1.SessionStatus_SESSION_STATUS_ACTIVE, resp.Msg.Status)
}

func TestSubmitTurn_TextTurn(t *testing.T) {
	t.Parallel()

	tokens := []string{"Great", " point."}
	b, _ := newBackendWithFakeLLM(t, tokens)
	srvURL := startInterviewServer(t, b)
	rawToken := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, rawToken)

	questionID := seedQuestion(t, b)
	ctx := context.Background()
	user, err := db.New(b.Pool()).GetUserByEmail(ctx, "testuser@example.com")
	require.NoError(t, err)

	sessionID := createSession(t, b, user.ID, questionID)

	// Opening turn.
	submitOpeningTurn(t, client, sessionID.String())

	// Candidate text turn.
	events := submitTurn(t, client, &drillv1.SubmitTurnRequest{
		SessionId: sessionID.String(),
		Input: &drillv1.SubmitTurnRequest_TextInput{
			TextInput: &drillv1.TextInput{Content: "I would use Redis as the caching layer."},
		},
	})

	var tokenCount, doneCount int
	for _, e := range events {
		switch e.Event.(type) {
		case *drillv1.TurnEvent_InterviewerToken:
			tokenCount++
		case *drillv1.TurnEvent_InterviewerDone:
			doneCount++
		}
	}
	require.Greater(t, tokenCount, 0, "expected at least one InterviewerToken event")
	require.Equal(t, 1, doneCount, "expected exactly one InterviewerDone event")
}

func TestGetSessionState_KnownMessageCount(t *testing.T) {
	t.Parallel()

	tokens := []string{"Good answer."}
	b, _ := newBackendWithFakeLLM(t, tokens)
	srvURL := startInterviewServer(t, b)
	rawToken := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, rawToken)

	questionID := seedQuestion(t, b)
	ctx := context.Background()
	user, err := db.New(b.Pool()).GetUserByEmail(ctx, "testuser@example.com")
	require.NoError(t, err)

	sessionID := createSession(t, b, user.ID, questionID)

	// Opening turn (creates 1 interviewer message).
	submitOpeningTurn(t, client, sessionID.String())

	// Candidate turn (creates 1 candidate message + 1 interviewer message = 3 total).
	submitTurn(t, client, &drillv1.SubmitTurnRequest{
		SessionId: sessionID.String(),
		Input: &drillv1.SubmitTurnRequest_TextInput{
			TextInput: &drillv1.TextInput{Content: "Redis with write-through."},
		},
	})

	// Full state: should return 3 messages.
	fullResp, err := client.GetSessionState(ctx, connect.NewRequest(&drillv1.GetSessionStateRequest{
		SessionId: sessionID.String(),
	}))
	require.NoError(t, err)
	require.Len(t, fullResp.Msg.Messages, 3)

	// Delta: known_message_count=1 should return messages after the first.
	deltaResp, err := client.GetSessionState(ctx, connect.NewRequest(&drillv1.GetSessionStateRequest{
		SessionId:         sessionID.String(),
		KnownMessageCount: 1,
	}))
	require.NoError(t, err)
	require.Len(t, deltaResp.Msg.Messages, 2, "expected 2 new messages when known_message_count=1")
}

func TestEndSession(t *testing.T) {
	t.Parallel()

	tokens := []string{"Let's begin."}
	b, _ := newBackendWithFakeLLM(t, tokens)
	srvURL := startInterviewServer(t, b)
	rawToken := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, rawToken)

	questionID := seedQuestion(t, b)
	ctx := context.Background()
	user, err := db.New(b.Pool()).GetUserByEmail(ctx, "testuser@example.com")
	require.NoError(t, err)

	sessionID := createSession(t, b, user.ID, questionID)

	// Opening turn.
	submitOpeningTurn(t, client, sessionID.String())

	// End session.
	_, err = client.EndSession(ctx, connect.NewRequest(&drillv1.EndSessionRequest{
		SessionId: sessionID.String(),
	}))
	require.NoError(t, err)

	// Verify status is completed via GetSessionState.
	resp, err := client.GetSessionState(ctx, connect.NewRequest(&drillv1.GetSessionStateRequest{
		SessionId: sessionID.String(),
	}))
	require.NoError(t, err)
	require.Equal(t, drillv1.SessionStatus_SESSION_STATUS_COMPLETED, resp.Msg.Status)
}

func TestCancelSession(t *testing.T) {
	t.Parallel()

	tokens := []string{"Let's begin."}
	b, _ := newBackendWithFakeLLM(t, tokens)
	srvURL := startInterviewServer(t, b)
	rawToken := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, rawToken)

	questionID := seedQuestion(t, b)
	ctx := context.Background()
	user, err := db.New(b.Pool()).GetUserByEmail(ctx, "testuser@example.com")
	require.NoError(t, err)

	sessionID := createSession(t, b, user.ID, questionID)

	// Opening turn.
	submitOpeningTurn(t, client, sessionID.String())

	// Cancel session.
	_, err = client.CancelSession(ctx, connect.NewRequest(&drillv1.CancelSessionRequest{
		SessionId: sessionID.String(),
	}))
	require.NoError(t, err)

	// Verify status is cancelled via GetSessionState.
	resp, err := client.GetSessionState(ctx, connect.NewRequest(&drillv1.GetSessionStateRequest{
		SessionId: sessionID.String(),
	}))
	require.NoError(t, err)
	require.Equal(t, drillv1.SessionStatus_SESSION_STATUS_CANCELLED, resp.Msg.Status)
}

func TestSubmitTurn_RejectsConcurrent(t *testing.T) {
	t.Parallel()

	tokens := []string{"Hello"}
	b, _ := newBackendWithFakeLLM(t, tokens)
	srvURL := startInterviewServer(t, b)
	rawToken := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, rawToken)

	questionID := seedQuestion(t, b)
	ctx := context.Background()
	user, err := db.New(b.Pool()).GetUserByEmail(ctx, "testuser@example.com")
	require.NoError(t, err)

	sessionID := createSession(t, b, user.ID, questionID)

	// Manually set status to 'generating' to simulate a concurrent turn.
	_, err = db.New(b.Pool()).AcquireGeneratingStatus(ctx, sessionID)
	require.NoError(t, err)

	// SubmitTurn should fail with FailedPrecondition.
	stream, err := client.SubmitTurn(ctx, connect.NewRequest(&drillv1.SubmitTurnRequest{
		SessionId: sessionID.String(),
		Input:     &drillv1.SubmitTurnRequest_TextInput{TextInput: &drillv1.TextInput{Content: ""}},
	}))
	if err != nil {
		require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
		return
	}
	// Drain stream expecting an error.
	for stream.Receive() {
	}
	require.Error(t, stream.Err())
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(stream.Err()))
}

// ---------------------------------------------------------------------------
// Unary endpoint auth tests
// ---------------------------------------------------------------------------

func TestGetSessionState_Unauthenticated(t *testing.T) {
	t.Parallel()

	b, _ := newBackendWithFakeLLM(t, []string{"Hello"})
	srvURL := startInterviewServer(t, b)

	client := drillv1connect.NewInterviewServiceClient(&http.Client{}, srvURL)
	_, err := client.GetSessionState(context.Background(), connect.NewRequest(&drillv1.GetSessionStateRequest{
		SessionId: uuid.New().String(),
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

func TestEndSession_Unauthenticated(t *testing.T) {
	t.Parallel()

	b, _ := newBackendWithFakeLLM(t, []string{"Hello"})
	srvURL := startInterviewServer(t, b)

	client := drillv1connect.NewInterviewServiceClient(&http.Client{}, srvURL)
	_, err := client.EndSession(context.Background(), connect.NewRequest(&drillv1.EndSessionRequest{
		SessionId: uuid.New().String(),
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

func TestCancelSession_Unauthenticated(t *testing.T) {
	t.Parallel()

	b, _ := newBackendWithFakeLLM(t, []string{"Hello"})
	srvURL := startInterviewServer(t, b)

	client := drillv1connect.NewInterviewServiceClient(&http.Client{}, srvURL)
	_, err := client.CancelSession(context.Background(), connect.NewRequest(&drillv1.CancelSessionRequest{
		SessionId: uuid.New().String(),
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}
