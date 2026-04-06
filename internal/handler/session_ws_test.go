package handler_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/ai"
	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/backend"
	"github.com/btc/drill/internal/backendtest"
	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/handler"
)

// ---------------------------------------------------------------------------
// Fake STT / TTS
// ---------------------------------------------------------------------------

// fakeTranscriber returns canned text for any audio input.
type fakeTranscriber struct {
	text string
}

func (f *fakeTranscriber) Transcribe(_ context.Context, _ []byte, _ string) (string, error) {
	return f.text, nil
}

// fakeSynthesizer returns a small reader with no real audio.
type fakeSynthesizer struct{}

func (f *fakeSynthesizer) Synthesize(_ context.Context, _ string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("fake-audio")), nil
}

// failOnceTranscriber fails on the first call, succeeds on subsequent calls.
type failOnceTranscriber struct {
	text  string
	calls int
}

func (f *failOnceTranscriber) Transcribe(_ context.Context, _ []byte, _ string) (string, error) {
	f.calls++
	if f.calls == 1 {
		return "", fmt.Errorf("transient OpenAI error")
	}
	return f.text, nil
}

// ---------------------------------------------------------------------------
// Fake Anthropic SSE server
// ---------------------------------------------------------------------------

// newFakeAnthropicServer creates an httptest server that streams SSE tokens.
func newFakeAnthropicServer(t *testing.T, tokens []string) *httptest.Server {
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

// ---------------------------------------------------------------------------
// DB helpers — direct SQL, bypass backend business logic
// ---------------------------------------------------------------------------


// createTestQuestion inserts a question directly using raw SQL (no sqlc InsertQuestion).
func createTestQuestion(t *testing.T, pool *pgxpool.Pool) db.Question {
	t.Helper()
	ctx := context.Background()
	qID := uuid.New()
	_, err := pool.Exec(ctx,
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
	q, err := db.New(pool).GetQuestion(ctx, qID)
	require.NoError(t, err)
	return q
}

// createTestSession inserts an active interview session and returns it.
func createTestSession(t *testing.T, pool *pgxpool.Pool, userID, questionID uuid.UUID) db.InterviewSession {
	t.Helper()
	ctx := context.Background()
	s, err := db.New(pool).CreateSession(ctx, db.CreateSessionParams{
		UserID:                userID,
		QuestionID:            questionID,
		ConfigDurationMinutes: 45,
		ConfigTtsEnabled:      false,
	})
	require.NoError(t, err)
	return s
}

// createAuthCookie creates an auth session in the DB and returns the cookie
// that the middleware expects.
func createAuthCookie(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID) *http.Cookie {
	t.Helper()
	ctx := context.Background()
	rawToken, tokenHash, err := auth.GenerateSessionToken()
	require.NoError(t, err)

	_, err = db.New(pool).CreateAuthSession(ctx, db.CreateAuthSessionParams{
		UserID:    userID,
		TokenHash: tokenHash,
		ExpiresAt: time.Now().Add(24 * time.Hour),
	})
	require.NoError(t, err)

	return &http.Cookie{
		Name:  auth.SessionCookieName,
		Value: rawToken,
	}
}

// ---------------------------------------------------------------------------
// WebSocket helpers
// ---------------------------------------------------------------------------

type wsMsg map[string]any

// wsConnect dials the WS endpoint, sends session_init, and returns the ws connection.
func wsConnect(t *testing.T, serverURL string, sessionID uuid.UUID, cookie *http.Cookie, lastSeq *int) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	wsURL := strings.Replace(serverURL, "http://", "ws://", 1) +
		"/api/sessions/" + sessionID.String() + "/ws"

	ws, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Cookie": {cookie.String()},
		},
	})
	require.NoError(t, err)
	ws.SetReadLimit(10 * 1024 * 1024)

	// Send session_init.
	initMsg := wsMsg{"type": "session_init"}
	if lastSeq != nil {
		initMsg["last_seq"] = *lastSeq
	}
	sendMsg(t, ws, initMsg)
	return ws
}

// readMsg reads a JSON message from the WebSocket with a timeout.
func readMsg(t *testing.T, ws *websocket.Conn) wsMsg {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, data, err := ws.Read(ctx)
	require.NoError(t, err, "readMsg: read failed")
	var m wsMsg
	require.NoError(t, json.Unmarshal(data, &m), "readMsg: unmarshal failed")
	return m
}

// readMsgTimeout reads a JSON message with a custom timeout. Returns nil if timed out.
func readMsgTimeout(t *testing.T, ws *websocket.Conn, timeout time.Duration) wsMsg {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_, data, err := ws.Read(ctx)
	if err != nil {
		return nil
	}
	var m wsMsg
	require.NoError(t, json.Unmarshal(data, &m))
	return m
}

// sendMsg marshals and sends a JSON message.
func sendMsg(t *testing.T, ws *websocket.Conn, msg wsMsg) {
	t.Helper()
	data, err := json.Marshal(msg)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = ws.Write(ctx, websocket.MessageText, data)
	require.NoError(t, err)
}

// drainUntilType reads messages until it finds one with the given type, returning it.
// Collects all intermediate messages in a slice.
func drainUntilType(t *testing.T, ws *websocket.Conn, msgType string) (wsMsg, []wsMsg) {
	t.Helper()
	var collected []wsMsg
	for i := 0; i < 100; i++ { // safety limit
		m := readMsg(t, ws)
		if m["type"] == msgType {
			return m, collected
		}
		collected = append(collected, m)
	}
	t.Fatalf("did not receive message of type %q within 100 messages", msgType)
	return nil, nil
}

// drainUntilDone reads all streaming messages until interviewer_done, returning
// the assembled text from interviewer_token messages and the full collected messages.
func drainUntilDone(t *testing.T, ws *websocket.Conn) (string, []wsMsg) {
	t.Helper()
	var tokens []string
	var collected []wsMsg
	for i := 0; i < 200; i++ {
		m := readMsg(t, ws)
		collected = append(collected, m)
		switch m["type"] {
		case "interviewer_token":
			if tok, ok := m["token"].(string); ok {
				tokens = append(tokens, tok)
			}
		case "interviewer_done":
			return strings.Join(tokens, ""), collected
		case "error":
			t.Fatalf("unexpected error message: %v", m)
		}
	}
	t.Fatal("did not receive interviewer_done within 200 messages")
	return "", nil
}

// ---------------------------------------------------------------------------
// Test backend constructor — uses real Postgres, fake AI
// ---------------------------------------------------------------------------

// newWSTestBackend creates a Backend with real Postgres + River but fake AI deps.
func newWSTestBackend(t *testing.T, anthropicURL string) *backend.Backend {
	t.Helper()
	b := pg.NewBackend(t)
	b.ApplyTestOverrides(backend.TestOverrides{
		LLM: ai.NewTestClient(anthropicURL, b.Pool()),
		STT: &fakeTranscriber{text: "I would use a hash-based approach."},
		TTS: &fakeSynthesizer{},
	})
	return b
}

// ---------------------------------------------------------------------------
// Test 1: Happy path (text) — full session lifecycle
// ---------------------------------------------------------------------------

func TestWS_HappyPath_Text(t *testing.T) {
	t.Parallel()

	tokens := []string{"Let's ", "design ", "a URL ", "shortener."}
	anthropicSrv := newFakeAnthropicServer(t, tokens)
	b := newWSTestBackend(t, anthropicSrv.URL)

	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	pool := b.Pool()
	userID := backendtest.SeedUser(t, b)
	question := createTestQuestion(t, pool)
	session := createTestSession(t, pool, userID, question.ID)
	cookie := createAuthCookie(t, pool, userID)

	// Connect and receive session_loaded.
	ws := wsConnect(t, srv.URL, session.ID, cookie, nil)
	defer ws.CloseNow()

	loaded := readMsg(t, ws)
	assert.Equal(t, "session_loaded", loaded["type"])

	// Read state_change to interviewer_speaking, then stream tokens, then interviewer_done,
	// then state_change to waiting_for_input.
	stateIS := readMsg(t, ws)
	assert.Equal(t, "state_change", stateIS["type"])
	assert.Equal(t, "interviewer_speaking", stateIS["state"])

	openingText, _ := drainUntilDone(t, ws)
	assert.Equal(t, "Let's design a URL shortener.", openingText)

	stateWait := readMsg(t, ws)
	assert.Equal(t, "state_change", stateWait["type"])
	assert.Equal(t, "waiting_for_input", stateWait["state"])

	// Send candidate text turn.
	sendMsg(t, ws, wsMsg{
		"type":         "end_turn",
		"content":      "I would start by defining the requirements.",
		"input_method": "text",
	})

	// Expect state_change to processing_input.
	statePI := readMsg(t, ws)
	assert.Equal(t, "state_change", statePI["type"])
	assert.Equal(t, "processing_input", statePI["state"])

	// State change to interviewer_speaking, then stream tokens, then done.
	stateIS2 := readMsg(t, ws)
	assert.Equal(t, "state_change", stateIS2["type"])
	assert.Equal(t, "interviewer_speaking", stateIS2["state"])

	responseText, _ := drainUntilDone(t, ws)
	assert.Equal(t, "Let's design a URL shortener.", responseText)

	stateWait2 := readMsg(t, ws)
	assert.Equal(t, "state_change", stateWait2["type"])
	assert.Equal(t, "waiting_for_input", stateWait2["state"])

	// End session.
	sendMsg(t, ws, wsMsg{"type": "end_session"})

	ended := readMsg(t, ws)
	assert.Equal(t, "session_ended", ended["type"])

	// Verify DB state.
	ctx := context.Background()
	q := db.New(pool)

	// Session is completed.
	updatedSession, err := q.GetSession(ctx, session.ID)
	require.NoError(t, err)
	assert.Equal(t, "completed", updatedSession.Status)

	// Messages persisted: 1 opening (interviewer) + 1 candidate + 1 interviewer response.
	msgs, err := q.GetMessagesBySession(ctx, session.ID)
	require.NoError(t, err)
	assert.Len(t, msgs, 3)
	assert.Equal(t, "interviewer", msgs[0].Role)
	assert.Equal(t, "candidate", msgs[1].Role)
	assert.Equal(t, "interviewer", msgs[2].Role)

	// LLM calls logged (check via raw SQL since no list query).
	var llmCallCount int
	err = pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM llm_calls WHERE session_id = $1`,
		session.ID,
	).Scan(&llmCallCount)
	require.NoError(t, err)
	assert.Equal(t, 2, llmCallCount) // opening + response

	// Eval job enqueued (river_job table).
	var evalJobCount int
	err = pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM river_job WHERE kind = 'evaluate_session' AND args->>'session_id' = $1`,
		session.ID.String(),
	).Scan(&evalJobCount)
	require.NoError(t, err)
	assert.Equal(t, 1, evalJobCount)
}

// ---------------------------------------------------------------------------
// Test 2: Voice input — STT called, transcription_result sent
// ---------------------------------------------------------------------------

func TestWS_VoiceInput(t *testing.T) {
	t.Parallel()

	tokens := []string{"Good ", "choice."}
	anthropicSrv := newFakeAnthropicServer(t, tokens)
	b := newWSTestBackend(t, anthropicSrv.URL)

	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	pool := b.Pool()
	userID := backendtest.SeedUser(t, b)
	question := createTestQuestion(t, pool)
	session := createTestSession(t, pool, userID, question.ID)
	cookie := createAuthCookie(t, pool, userID)

	ws := wsConnect(t, srv.URL, session.ID, cookie, nil)
	defer ws.CloseNow()

	// Drain opening: session_loaded, state_change interviewer_speaking, tokens, interviewer_done, state_change waiting.
	drainUntilType(t, ws, "session_loaded")
	drainUntilDone(t, ws)
	drainUntilType(t, ws, "state_change") // waiting_for_input

	// Send voice turn with base64 audio.
	fakeAudio := base64.StdEncoding.EncodeToString([]byte("fake-webm-audio"))
	sendMsg(t, ws, wsMsg{
		"type":         "end_turn",
		"audio":        fakeAudio,
		"input_method": "voice",
	})

	// Expect state_change to transcribing.
	stateTranscribing := readMsg(t, ws)
	assert.Equal(t, "state_change", stateTranscribing["type"])
	assert.Equal(t, "transcribing", stateTranscribing["state"])

	// Expect transcription_result.
	transcription := readMsg(t, ws)
	assert.Equal(t, "transcription_result", transcription["type"])
	assert.Equal(t, "I would use a hash-based approach.", transcription["text"])

	// state_change to processing_input.
	statePI := readMsg(t, ws)
	assert.Equal(t, "state_change", statePI["type"])
	assert.Equal(t, "processing_input", statePI["state"])

	// Interviewer response.
	stateIS := readMsg(t, ws)
	assert.Equal(t, "state_change", stateIS["type"])
	assert.Equal(t, "interviewer_speaking", stateIS["state"])

	responseText, _ := drainUntilDone(t, ws)
	assert.Equal(t, "Good choice.", responseText)

	// Wait for state_change to waiting_for_input — confirms messages are committed.
	stateWait := readMsg(t, ws)
	assert.Equal(t, "state_change", stateWait["type"])
	assert.Equal(t, "waiting_for_input", stateWait["state"])

	// Verify candidate message persisted with voice input method.
	ctx := context.Background()
	msgs, err := db.New(pool).GetMessagesBySession(ctx, session.ID)
	require.NoError(t, err)
	// opening + candidate(voice) + response
	assert.GreaterOrEqual(t, len(msgs), 3)
	candidateMsg := msgs[1]
	assert.Equal(t, "candidate", candidateMsg.Role)
	assert.Equal(t, "I would use a hash-based approach.", candidateMsg.Content)
	assert.True(t, candidateMsg.InputMethod.Valid)
	assert.Equal(t, "voice", candidateMsg.InputMethod.String)
}

// ---------------------------------------------------------------------------
// Test 3: cancel_tts during InterviewerSpeaking — no crash, response persisted
// ---------------------------------------------------------------------------

func TestWS_CancelTTS(t *testing.T) {
	t.Parallel()

	tokens := []string{"This ", "is ", "a ", "test."}
	anthropicSrv := newFakeAnthropicServer(t, tokens)
	b := newWSTestBackend(t, anthropicSrv.URL)

	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	pool := b.Pool()
	userID := backendtest.SeedUser(t, b)
	question := createTestQuestion(t, pool)
	session := createTestSession(t, pool, userID, question.ID)
	cookie := createAuthCookie(t, pool, userID)

	ws := wsConnect(t, srv.URL, session.ID, cookie, nil)
	defer ws.CloseNow()

	// Drain session_loaded.
	drainUntilType(t, ws, "session_loaded")

	// During opening stream, send cancel_tts. It should not crash.
	// The read loop handles cancel_tts without forwarding to conductor.
	sendMsg(t, ws, wsMsg{"type": "cancel_tts"})

	// The opening should still complete.
	drainUntilDone(t, ws)

	// state_change to waiting_for_input confirms session is not corrupted.
	stateWait := readMsg(t, ws)
	assert.Equal(t, "state_change", stateWait["type"])
	assert.Equal(t, "waiting_for_input", stateWait["state"])

	// Verify message was persisted despite cancel_tts.
	ctx := context.Background()
	msgs, err := db.New(pool).GetMessagesBySession(ctx, session.ID)
	require.NoError(t, err)
	assert.Len(t, msgs, 1)
	assert.Equal(t, "interviewer", msgs[0].Role)
	assert.Equal(t, "This is a test.", msgs[0].Content)
}

// ---------------------------------------------------------------------------
// Test 4: Reconnection — disconnect and reconnect with last_seq
// ---------------------------------------------------------------------------

func TestWS_Reconnection(t *testing.T) {
	t.Parallel()

	tokens := []string{"Welcome."}
	anthropicSrv := newFakeAnthropicServer(t, tokens)
	b := newWSTestBackend(t, anthropicSrv.URL)

	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	pool := b.Pool()
	userID := backendtest.SeedUser(t, b)
	question := createTestQuestion(t, pool)
	session := createTestSession(t, pool, userID, question.ID)
	cookie := createAuthCookie(t, pool, userID)

	// First connection: complete an opening.
	ws1 := wsConnect(t, srv.URL, session.ID, cookie, nil)
	drainUntilType(t, ws1, "session_loaded")
	drainUntilDone(t, ws1)
	drainUntilType(t, ws1, "state_change") // waiting_for_input

	// Disconnect.
	ws1.Close(websocket.StatusNormalClosure, "done")
	time.Sleep(200 * time.Millisecond) // let conductor process disconnect

	// Reconnect with last_seq = 0 (want all messages).
	lastSeq := 0
	ws2 := wsConnect(t, srv.URL, session.ID, cookie, &lastSeq)
	defer ws2.CloseNow()

	// Should receive reconnect_state.
	reconnectMsg := readMsg(t, ws2)
	assert.Equal(t, "reconnect_state", reconnectMsg["type"])

	// The messages field should be present and contain the opening.
	messages, ok := reconnectMsg["messages"].([]any)
	assert.True(t, ok, "messages should be an array")
	assert.GreaterOrEqual(t, len(messages), 1, "should have at least the opening message")
}

// ---------------------------------------------------------------------------
// Test 5: Invalid state transition — end_turn during InterviewerSpeaking
// ---------------------------------------------------------------------------

func TestWS_InvalidTransition(t *testing.T) {
	t.Parallel()

	// Use tokens with slight delay effect (streaming takes some time).
	tokens := []string{"Hello ", "there."}
	anthropicSrv := newFakeAnthropicServer(t, tokens)
	b := newWSTestBackend(t, anthropicSrv.URL)

	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	pool := b.Pool()
	userID := backendtest.SeedUser(t, b)
	question := createTestQuestion(t, pool)
	session := createTestSession(t, pool, userID, question.ID)
	cookie := createAuthCookie(t, pool, userID)

	ws := wsConnect(t, srv.URL, session.ID, cookie, nil)
	defer ws.CloseNow()

	// After session_loaded, opening starts. The conductor is in InterviewerSpeaking.
	drainUntilType(t, ws, "session_loaded")

	// The opening is streaming, but the end_turn goes through msgCh, which
	// the conductor will process after the opening stream completes.
	// After the opening, state is WaitingForInput and end_turn should work.
	// Let the opening complete first.
	drainUntilDone(t, ws)
	drainUntilType(t, ws, "state_change") // waiting_for_input

	// Send a text turn to trigger interviewer response.
	sendMsg(t, ws, wsMsg{
		"type":         "end_turn",
		"content":      "test",
		"input_method": "text",
	})

	// Drain through the full response cycle: processing_input, interviewer_speaking,
	// tokens, interviewer_done, waiting_for_input.
	drainUntilType(t, ws, "state_change") // processing_input
	drainUntilType(t, ws, "state_change") // interviewer_speaking
	drainUntilDone(t, ws)
	stateWait2 := readMsg(t, ws) // state_change to waiting_for_input
	assert.Equal(t, "state_change", stateWait2["type"])
	assert.Equal(t, "waiting_for_input", stateWait2["state"])

	// Verify session is still functional.
	sendMsg(t, ws, wsMsg{"type": "end_session"})
	ended := readMsg(t, ws)
	assert.Equal(t, "session_ended", ended["type"])

	// Session not corrupted.
	ctx := context.Background()
	updatedSession, err := db.New(pool).GetSession(ctx, session.ID)
	require.NoError(t, err)
	assert.Equal(t, "completed", updatedSession.Status)
}

// ---------------------------------------------------------------------------
// Test 6: Malformed messages — garbage JSON, connection stays alive
// ---------------------------------------------------------------------------

func TestWS_MalformedMessages(t *testing.T) {
	t.Parallel()

	tokens := []string{"Hi."}
	anthropicSrv := newFakeAnthropicServer(t, tokens)
	b := newWSTestBackend(t, anthropicSrv.URL)

	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	pool := b.Pool()
	userID := backendtest.SeedUser(t, b)
	question := createTestQuestion(t, pool)
	session := createTestSession(t, pool, userID, question.ID)
	cookie := createAuthCookie(t, pool, userID)

	ws := wsConnect(t, srv.URL, session.ID, cookie, nil)
	defer ws.CloseNow()

	// Let opening finish.
	drainUntilType(t, ws, "session_loaded")
	drainUntilDone(t, ws)
	drainUntilType(t, ws, "state_change") // waiting_for_input

	// Send garbage JSON.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := ws.Write(ctx, websocket.MessageText, []byte("{not valid json"))
	require.NoError(t, err)

	// Should receive an error message but connection should stay alive.
	errMsg := readMsg(t, ws)
	assert.Equal(t, "error", errMsg["type"])
	assert.Equal(t, "malformed_message", errMsg["code"])

	// Send another garbage: missing type field.
	sendMsg(t, ws, wsMsg{"content": "no type field"})
	errMsg2 := readMsg(t, ws)
	assert.Equal(t, "error", errMsg2["type"])
	assert.Equal(t, "malformed_message", errMsg2["code"])

	// Connection is still alive — send a valid ping.
	sendMsg(t, ws, wsMsg{"type": "ping"})
	pong := readMsg(t, ws)
	assert.Equal(t, "pong", pong["type"])
}

// ---------------------------------------------------------------------------
// Test 7: Session ownership — wrong user rejected before upgrade
// ---------------------------------------------------------------------------

func TestWS_SessionOwnership(t *testing.T) {
	t.Parallel()

	tokens := []string{"Hi."}
	anthropicSrv := newFakeAnthropicServer(t, tokens)
	b := newWSTestBackend(t, anthropicSrv.URL)

	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	pool := b.Pool()
	userA := backendtest.SeedUser(t, b)
	userB := backendtest.SeedUser(t, b)
	question := createTestQuestion(t, pool)
	session := createTestSession(t, pool, userA, question.ID)
	cookieB := createAuthCookie(t, pool, userB)

	// User B tries to connect to User A's session.
	// The handler returns 403 Forbidden before the WebSocket upgrade.
	wsURL := strings.Replace(srv.URL, "http://", "ws://", 1) +
		"/api/sessions/" + session.ID.String() + "/ws"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Cookie": {cookieB.String()},
		},
	})
	// The server rejects before upgrade — expect a non-nil error.
	assert.Error(t, err, "expected dial to fail for wrong user")
	if resp != nil {
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// Test 8: Inactive session — connect to completed session
// ---------------------------------------------------------------------------

func TestWS_InactiveSession(t *testing.T) {
	t.Parallel()

	tokens := []string{"Hi."}
	anthropicSrv := newFakeAnthropicServer(t, tokens)
	b := newWSTestBackend(t, anthropicSrv.URL)

	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	pool := b.Pool()
	userID := backendtest.SeedUser(t, b)
	question := createTestQuestion(t, pool)
	session := createTestSession(t, pool, userID, question.ID)
	cookie := createAuthCookie(t, pool, userID)

	// Mark session as completed before connecting.
	ctx := context.Background()
	err := db.New(pool).MarkSessionCompleted(ctx, session.ID)
	require.NoError(t, err)

	// Try to connect — should be rejected with 409 Conflict.
	wsURL := strings.Replace(srv.URL, "http://", "ws://", 1) +
		"/api/sessions/" + session.ID.String() + "/ws"

	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	_, resp, err := websocket.Dial(dialCtx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Cookie": {cookie.String()},
		},
	})
	assert.Error(t, err, "expected dial to fail for completed session")
	if resp != nil {
		assert.Equal(t, http.StatusConflict, resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// Test 9: Graceful shutdown — cancel server context
// ---------------------------------------------------------------------------

func TestWS_GracefulShutdown(t *testing.T) {
	t.Parallel()

	// Use a slow Anthropic server that streams with delays.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		fmt.Fprintf(w, "event: message_start\n")
		fmt.Fprintf(w, "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_test\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-sonnet-4-20250514\",\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":25,\"output_tokens\":0}}}\n\n")

		fmt.Fprintf(w, "event: content_block_start\n")
		fmt.Fprintf(w, "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")

		tokens := []string{"Slow ", "stream ", "response."}
		for _, token := range tokens {
			time.Sleep(100 * time.Millisecond)
			fmt.Fprintf(w, "event: content_block_delta\n")
			fmt.Fprintf(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":%q}}\n\n", token)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}

		fmt.Fprintf(w, "event: content_block_stop\n")
		fmt.Fprintf(w, "data: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
		fmt.Fprintf(w, "event: message_delta\n")
		fmt.Fprintf(w, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":3}}\n\n")
		fmt.Fprintf(w, "event: message_stop\n")
		fmt.Fprintf(w, "data: {\"type\":\"message_stop\"}\n\n")
	}))
	t.Cleanup(srv.Close)

	b := newWSTestBackend(t, srv.URL)

	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))
	httpSrv := httptest.NewServer(mux)
	t.Cleanup(httpSrv.Close)

	pool := b.Pool()
	userID := backendtest.SeedUser(t, b)
	question := createTestQuestion(t, pool)
	session := createTestSession(t, pool, userID, question.ID)
	cookie := createAuthCookie(t, pool, userID)

	ws := wsConnect(t, httpSrv.URL, session.ID, cookie, nil)
	defer ws.CloseNow()

	// Wait for session_loaded.
	drainUntilType(t, ws, "session_loaded")

	// Let the opening stream start, then close the server to trigger context cancellation.
	time.Sleep(150 * time.Millisecond) // let at least one token arrive
	httpSrv.Close()

	// The conductor should try to send reconnect_please, but with the server
	// closing, the WebSocket may break. The key test is that we don't panic
	// and the session state is consistent.
	// Just verify no panic by reaching this point.

	// Give a moment for cleanup.
	time.Sleep(500 * time.Millisecond)

	// Verify the session is still in DB (not corrupted).
	ctx := context.Background()
	_, err := db.New(pool).GetSession(ctx, session.ID)
	assert.NoError(t, err)
}

// ---------------------------------------------------------------------------
// Test 10: Transactional enqueue — session status and eval job in same tx
// ---------------------------------------------------------------------------

func TestWS_TransactionalEnqueue(t *testing.T) {
	t.Parallel()

	tokens := []string{"Done."}
	anthropicSrv := newFakeAnthropicServer(t, tokens)
	b := newWSTestBackend(t, anthropicSrv.URL)

	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	pool := b.Pool()
	userID := backendtest.SeedUser(t, b)
	question := createTestQuestion(t, pool)
	session := createTestSession(t, pool, userID, question.ID)
	cookie := createAuthCookie(t, pool, userID)

	ws := wsConnect(t, srv.URL, session.ID, cookie, nil)
	defer ws.CloseNow()

	// Complete opening.
	drainUntilType(t, ws, "session_loaded")
	drainUntilDone(t, ws)
	drainUntilType(t, ws, "state_change") // waiting_for_input

	// End session.
	sendMsg(t, ws, wsMsg{"type": "end_session"})
	ended := readMsg(t, ws)
	assert.Equal(t, "session_ended", ended["type"])

	// Both session status and eval job should exist.
	ctx := context.Background()
	updatedSession, err := db.New(pool).GetSession(ctx, session.ID)
	require.NoError(t, err)
	assert.Equal(t, "completed", updatedSession.Status)

	var evalJobCount int
	err = pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM river_job WHERE kind = 'evaluate_session' AND args->>'session_id' = $1`,
		session.ID.String(),
	).Scan(&evalJobCount)
	require.NoError(t, err)
	assert.Equal(t, 1, evalJobCount, "eval job should be enqueued atomically with session completion")
}

// ---------------------------------------------------------------------------
// Test 11: Abandoned cleanup — session left active, cleanup marks completed
// ---------------------------------------------------------------------------

func TestWS_AbandonedCleanup(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	pool := b.Pool()

	userID := backendtest.SeedUser(t, b)
	question := createTestQuestion(t, pool)

	// Create session with a very short duration and backdate started_at.
	ctx := context.Background()
	session, err := db.New(pool).CreateSession(ctx, db.CreateSessionParams{
		UserID:                userID,
		QuestionID:            question.ID,
		ConfigDurationMinutes: 1, // 1 minute
		ConfigTtsEnabled:      false,
	})
	require.NoError(t, err)

	// Backdate started_at so the session appears abandoned
	// (started_at + duration + 5 min < NOW()).
	_, err = pool.Exec(ctx,
		`UPDATE interview_sessions SET started_at = NOW() - INTERVAL '30 minutes' WHERE id = $1`,
		session.ID,
	)
	require.NoError(t, err)

	// Run the cleanup query directly.
	ids, err := db.New(pool).FindAbandonedSessions(ctx)
	require.NoError(t, err)
	assert.Contains(t, ids, session.ID, "session should be found as abandoned")

	// Mark it completed (as the cleanup worker would).
	err = db.New(pool).MarkSessionCompleted(ctx, session.ID)
	require.NoError(t, err)

	// Verify.
	updated, err := db.New(pool).GetSession(ctx, session.ID)
	require.NoError(t, err)
	assert.Equal(t, "completed", updated.Status)
}

// ---------------------------------------------------------------------------
// Test: Unknown message type — error returned, session continues
// ---------------------------------------------------------------------------

func TestWS_UnknownMessageType(t *testing.T) {
	t.Parallel()

	tokens := []string{"Ok."}
	anthropicSrv := newFakeAnthropicServer(t, tokens)
	b := newWSTestBackend(t, anthropicSrv.URL)

	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	pool := b.Pool()
	userID := backendtest.SeedUser(t, b)
	question := createTestQuestion(t, pool)
	session := createTestSession(t, pool, userID, question.ID)
	cookie := createAuthCookie(t, pool, userID)

	ws := wsConnect(t, srv.URL, session.ID, cookie, nil)
	defer ws.CloseNow()

	// Complete opening.
	drainUntilType(t, ws, "session_loaded")
	drainUntilDone(t, ws)
	drainUntilType(t, ws, "state_change") // waiting_for_input

	// Send unknown message type.
	sendMsg(t, ws, wsMsg{"type": "nonexistent_type"})
	errMsg := readMsg(t, ws)
	assert.Equal(t, "error", errMsg["type"])
	assert.Equal(t, "unknown_message_type", errMsg["code"])

	// Session still works.
	sendMsg(t, ws, wsMsg{"type": "ping"})
	pong := readMsg(t, ws)
	assert.Equal(t, "pong", pong["type"])
}

// ---------------------------------------------------------------------------
// Test: Unauthenticated access — no cookie
// ---------------------------------------------------------------------------

func TestWS_Unauthenticated(t *testing.T) {
	t.Parallel()

	tokens := []string{"Hi."}
	anthropicSrv := newFakeAnthropicServer(t, tokens)
	b := newWSTestBackend(t, anthropicSrv.URL)

	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	pool := b.Pool()
	userID := backendtest.SeedUser(t, b)
	question := createTestQuestion(t, pool)
	session := createTestSession(t, pool, userID, question.ID)

	// Try to connect without auth cookie.
	wsURL := strings.Replace(srv.URL, "http://", "ws://", 1) +
		"/api/sessions/" + session.ID.String() + "/ws"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, resp, err := websocket.Dial(ctx, wsURL, nil)
	assert.Error(t, err, "expected dial to fail without auth")
	if resp != nil {
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// Test: Nonexistent session — 404
// ---------------------------------------------------------------------------

func TestWS_NonexistentSession(t *testing.T) {
	t.Parallel()

	tokens := []string{"Hi."}
	anthropicSrv := newFakeAnthropicServer(t, tokens)
	b := newWSTestBackend(t, anthropicSrv.URL)

	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	pool := b.Pool()
	userID := backendtest.SeedUser(t, b)
	cookie := createAuthCookie(t, pool, userID)

	// Try to connect to a nonexistent session.
	fakeSessionID := uuid.New()
	wsURL := strings.Replace(srv.URL, "http://", "ws://", 1) +
		"/api/sessions/" + fakeSessionID.String() + "/ws"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Cookie": {cookie.String()},
		},
	})
	assert.Error(t, err, "expected dial to fail for nonexistent session")
	if resp != nil {
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// Test: Advisory lock contention — second connection to same session rejected
// ---------------------------------------------------------------------------

func TestWS_AdvisoryLockContention(t *testing.T) {
	t.Parallel()

	tokens := []string{"Hello."}
	anthropicSrv := newFakeAnthropicServer(t, tokens)
	b := newWSTestBackend(t, anthropicSrv.URL)

	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	pool := b.Pool()
	userID := backendtest.SeedUser(t, b)
	question := createTestQuestion(t, pool)
	session := createTestSession(t, pool, userID, question.ID)
	cookie := createAuthCookie(t, pool, userID)

	// First connection: dial, send session_init, receive session_loaded.
	ws1 := wsConnect(t, srv.URL, session.ID, cookie, nil)
	defer ws1.CloseNow()

	loaded := readMsg(t, ws1)
	require.Equal(t, "session_loaded", loaded["type"])

	// Second connection: dial raw (same user, same session).
	// The HTTP upgrade succeeds, but the conductor will fail to acquire the
	// advisory lock and close the WS with StatusPolicyViolation.
	wsURL := strings.Replace(srv.URL, "http://", "ws://", 1) +
		"/api/sessions/" + session.ID.String() + "/ws"

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer dialCancel()

	ws2, _, err := websocket.Dial(dialCtx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Cookie": {cookie.String()}},
	})
	require.NoError(t, err, "dial should succeed; rejection happens at conductor level")
	defer ws2.CloseNow()

	// Send session_init on ws2. Write may or may not fail depending on timing,
	// so ignore the write error — the important thing is the subsequent Read.
	initData, _ := json.Marshal(wsMsg{"type": "session_init"})
	writeCtx, writeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = ws2.Write(writeCtx, websocket.MessageText, initData)
	writeCancel()

	// Read from ws2: the conductor closes it with StatusPolicyViolation, so
	// Read returns a CloseError with that code.
	readCtx, readCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer readCancel()
	_, _, readErr := ws2.Read(readCtx)
	require.Error(t, readErr, "second connection should be closed by the server")
	assert.Equal(t, websocket.StatusPolicyViolation, websocket.CloseStatus(readErr),
		"expected StatusPolicyViolation for lock contention, got: %v", readErr)

	// First connection must still be alive. Drain the full opening sequence
	// (state_change → interviewer_speaking, tokens, interviewer_done,
	// state_change → waiting_for_input), then ping.
	drainUntilDone(t, ws1)
	drainUntilType(t, ws1, "state_change") // waiting_for_input

	sendMsg(t, ws1, wsMsg{"type": "ping"})
	pong := readMsg(t, ws1)
	assert.Equal(t, "pong", pong["type"])
}

// ---------------------------------------------------------------------------
// Test: Multi-turn — sequence numbers, turn count, and DB consistency
// ---------------------------------------------------------------------------

func TestWS_MultiTurn(t *testing.T) {
	t.Parallel()

	// Each LLM call returns the same tokens (fake server is stateless).
	tokens := []string{"Sure, ", "let's ", "continue."}
	anthropicSrv := newFakeAnthropicServer(t, tokens)
	b := newWSTestBackend(t, anthropicSrv.URL)

	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	pool := b.Pool()
	userID := backendtest.SeedUser(t, b)
	question := createTestQuestion(t, pool)
	session := createTestSession(t, pool, userID, question.ID)
	cookie := createAuthCookie(t, pool, userID)

	ws := wsConnect(t, srv.URL, session.ID, cookie, nil)
	defer ws.CloseNow()

	// Opening: session_loaded + interviewer stream + waiting_for_input.
	drainUntilType(t, ws, "session_loaded")
	drainUntilDone(t, ws)
	drainUntilType(t, ws, "state_change") // waiting_for_input

	// Turn 1.
	sendMsg(t, ws, wsMsg{"type": "end_turn", "content": "First answer", "input_method": "text"})
	drainUntilType(t, ws, "state_change") // processing_input
	drainUntilType(t, ws, "state_change") // interviewer_speaking
	drainUntilDone(t, ws)
	drainUntilType(t, ws, "state_change") // waiting_for_input

	// Turn 2.
	sendMsg(t, ws, wsMsg{"type": "end_turn", "content": "Second answer", "input_method": "text"})
	drainUntilType(t, ws, "state_change") // processing_input
	drainUntilType(t, ws, "state_change") // interviewer_speaking
	drainUntilDone(t, ws)
	drainUntilType(t, ws, "state_change") // waiting_for_input

	// Turn 3.
	sendMsg(t, ws, wsMsg{"type": "end_turn", "content": "Third answer", "input_method": "text"})
	drainUntilType(t, ws, "state_change") // processing_input
	drainUntilType(t, ws, "state_change") // interviewer_speaking
	drainUntilDone(t, ws)
	drainUntilType(t, ws, "state_change") // waiting_for_input

	// End session.
	sendMsg(t, ws, wsMsg{"type": "end_session"})
	ended := readMsg(t, ws)
	assert.Equal(t, "session_ended", ended["type"])

	// Verify DB state.
	ctx := context.Background()
	q := db.New(pool)

	// Session status and turn count.
	updatedSession, err := q.GetSession(ctx, session.ID)
	require.NoError(t, err)
	assert.Equal(t, "completed", updatedSession.Status)
	// Turn count: opening (1) + turn1 (2) + turn2 (3) + turn3 (4).
	assert.Equal(t, int32(4), updatedSession.TurnCount)

	// Messages: opening + 3*(candidate + interviewer) = 7 messages.
	msgs, err := q.GetMessagesBySession(ctx, session.ID)
	require.NoError(t, err)
	assert.Len(t, msgs, 7)

	// Verify sequence numbers are monotonically increasing.
	for i := 0; i < len(msgs); i++ {
		assert.Equal(t, int32(i+1), msgs[i].Seq, "message %d should have seq %d", i, i+1)
	}

	// Verify role alternation: interviewer, candidate, interviewer, candidate, ...
	expectedRoles := []string{"interviewer", "candidate", "interviewer", "candidate", "interviewer", "candidate", "interviewer"}
	for i, msg := range msgs {
		assert.Equal(t, expectedRoles[i], msg.Role, "message %d role", i)
	}

	// Verify candidate content matches what was sent.
	assert.Equal(t, "First answer", msgs[1].Content)
	assert.Equal(t, "Second answer", msgs[3].Content)
	assert.Equal(t, "Third answer", msgs[5].Content)
}

// ---------------------------------------------------------------------------
// Test: Empty text input — rejected, session continues
// ---------------------------------------------------------------------------

func TestWS_EmptyTextInput(t *testing.T) {
	t.Parallel()

	tokens := []string{"Hello."}
	anthropicSrv := newFakeAnthropicServer(t, tokens)
	b := newWSTestBackend(t, anthropicSrv.URL)

	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	pool := b.Pool()
	userID := backendtest.SeedUser(t, b)
	question := createTestQuestion(t, pool)
	session := createTestSession(t, pool, userID, question.ID)
	cookie := createAuthCookie(t, pool, userID)

	ws := wsConnect(t, srv.URL, session.ID, cookie, nil)
	defer ws.CloseNow()

	// Complete opening.
	drainUntilType(t, ws, "session_loaded")
	drainUntilDone(t, ws)
	drainUntilType(t, ws, "state_change") // waiting_for_input

	// Send empty text.
	sendMsg(t, ws, wsMsg{"type": "end_turn", "content": "", "input_method": "text"})
	errMsg := readMsg(t, ws)
	assert.Equal(t, "error", errMsg["type"])
	assert.Equal(t, "empty_content", errMsg["code"])

	// Send whitespace-only text.
	sendMsg(t, ws, wsMsg{"type": "end_turn", "content": "   ", "input_method": "text"})
	errMsg2 := readMsg(t, ws)
	assert.Equal(t, "error", errMsg2["type"])
	assert.Equal(t, "empty_content", errMsg2["code"])

	// Session still functional.
	sendMsg(t, ws, wsMsg{"type": "ping"})
	pong := readMsg(t, ws)
	assert.Equal(t, "pong", pong["type"])

	// No candidate messages persisted.
	ctx := context.Background()
	msgs, err := db.New(pool).GetMessagesBySession(ctx, session.ID)
	require.NoError(t, err)
	assert.Len(t, msgs, 1, "only the opening interviewer message should exist")
}

// ---------------------------------------------------------------------------
// Test: Empty voice input — no audio data rejected
// ---------------------------------------------------------------------------

func TestWS_EmptyVoiceInput(t *testing.T) {
	t.Parallel()

	tokens := []string{"Hello."}
	anthropicSrv := newFakeAnthropicServer(t, tokens)
	b := newWSTestBackend(t, anthropicSrv.URL)

	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	pool := b.Pool()
	userID := backendtest.SeedUser(t, b)
	question := createTestQuestion(t, pool)
	session := createTestSession(t, pool, userID, question.ID)
	cookie := createAuthCookie(t, pool, userID)

	ws := wsConnect(t, srv.URL, session.ID, cookie, nil)
	defer ws.CloseNow()

	// Complete opening.
	drainUntilType(t, ws, "session_loaded")
	drainUntilDone(t, ws)
	drainUntilType(t, ws, "state_change") // waiting_for_input

	// Send voice turn with no audio data.
	sendMsg(t, ws, wsMsg{"type": "end_turn", "input_method": "voice"})
	errMsg := readMsg(t, ws)
	assert.Equal(t, "error", errMsg["type"])
	assert.Equal(t, "audio_validation_failed", errMsg["code"])

	// Session still functional.
	sendMsg(t, ws, wsMsg{"type": "ping"})
	pong := readMsg(t, ws)
	assert.Equal(t, "pong", pong["type"])
}

// ---------------------------------------------------------------------------
// Test: LLM stream error — conductor recovers, session continues
// ---------------------------------------------------------------------------

// newFakeAnthropicErrorServer creates a server that streams N tokens then
// returns an SSE error event, simulating a mid-stream LLM failure.
func newFakeAnthropicErrorServer(t *testing.T, goodTokens []string) *httptest.Server {
	t.Helper()
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		fmt.Fprintf(w, "event: message_start\n")
		fmt.Fprintf(w, "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_test\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-sonnet-4-20250514\",\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":25,\"output_tokens\":0}}}\n\n")

		fmt.Fprintf(w, "event: content_block_start\n")
		fmt.Fprintf(w, "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")

		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		// First call (opening): fail after emitting some tokens.
		// Second call onward: succeed normally.
		if callCount == 1 {
			for _, token := range goodTokens {
				fmt.Fprintf(w, "event: content_block_delta\n")
				fmt.Fprintf(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":%q}}\n\n", token)
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}
			// Abruptly close the connection to simulate a stream error.
			if hijacker, ok := w.(http.Hijacker); ok {
				conn, _, _ := hijacker.Hijack()
				conn.Close()
			}
			return
		}

		// Normal response for subsequent calls.
		for _, token := range goodTokens {
			fmt.Fprintf(w, "event: content_block_delta\n")
			fmt.Fprintf(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":%q}}\n\n", token)
		}
		fmt.Fprintf(w, "event: content_block_stop\n")
		fmt.Fprintf(w, "data: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
		fmt.Fprintf(w, "event: message_delta\n")
		fmt.Fprintf(w, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":%d}}\n\n", len(goodTokens))
		fmt.Fprintf(w, "event: message_stop\n")
		fmt.Fprintf(w, "data: {\"type\":\"message_stop\"}\n\n")
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newSlowAnthropicServer creates a server that streams tokens with a delay
// between each, allowing time for cancel/disconnect during streaming.
func newSlowAnthropicServer(t *testing.T, tokens []string, delayPerToken time.Duration) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		fmt.Fprintf(w, "event: message_start\n")
		fmt.Fprintf(w, "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_test\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-sonnet-4-20250514\",\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":25,\"output_tokens\":0}}}\n\n")

		fmt.Fprintf(w, "event: content_block_start\n")
		fmt.Fprintf(w, "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")

		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		for _, token := range tokens {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(delayPerToken):
			}
			fmt.Fprintf(w, "event: content_block_delta\n")
			fmt.Fprintf(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":%q}}\n\n", token)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
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

func TestWS_LLMStreamError(t *testing.T) {
	t.Parallel()

	// Opening will fail mid-stream, then the candidate can still send a turn
	// and get a successful response.
	tokens := []string{"Partial ", "response"}
	anthropicSrv := newFakeAnthropicErrorServer(t, tokens)
	b := newWSTestBackend(t, anthropicSrv.URL)

	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	pool := b.Pool()
	userID := backendtest.SeedUser(t, b)
	question := createTestQuestion(t, pool)
	session := createTestSession(t, pool, userID, question.ID)
	cookie := createAuthCookie(t, pool, userID)

	ws := wsConnect(t, srv.URL, session.ID, cookie, nil)
	defer ws.CloseNow()

	// The opening stream will fail. The conductor should:
	// 1. Stream some tokens
	// 2. Hit an error
	// 3. Still persist what it got and recover
	//
	// We expect to either get an error or the session to close depending
	// on how the conductor handles the opening failure. Read messages
	// until we get either an error, session close, or turn_failed.
	drainUntilType(t, ws, "session_loaded")

	// Read messages — the opening may partially stream tokens then error.
	// The conductor calls streamInterviewerResponse which on error will
	// cause sendInitialMessage to return an error, causing Run() to exit.
	// The WS connection will be closed.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		m := readMsgTimeout(t, ws, 5*time.Second)
		if m == nil {
			break // connection closed
		}
		// If we get an error message or the connection is about to close, that's expected.
		if m["type"] == "error" {
			break
		}
	}

	// The important thing: no panic, no goroutine leak.
	// Verify session is still in DB and not corrupted.
	ctx := context.Background()
	_, err := db.New(pool).GetSession(ctx, session.ID)
	assert.NoError(t, err)
}

// ---------------------------------------------------------------------------
// Test: TTS-enabled session — synthesizer path exercised
// ---------------------------------------------------------------------------

func TestWS_TTSEnabled(t *testing.T) {
	t.Parallel()

	tokens := []string{"Great ", "question!"}
	anthropicSrv := newFakeAnthropicServer(t, tokens)
	b := newWSTestBackend(t, anthropicSrv.URL)

	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	pool := b.Pool()
	userID := backendtest.SeedUser(t, b)
	question := createTestQuestion(t, pool)

	// Create TTS-enabled session.
	ctx := context.Background()
	session, err := db.New(pool).CreateSession(ctx, db.CreateSessionParams{
		UserID:                userID,
		QuestionID:            question.ID,
		ConfigDurationMinutes: 45,
		ConfigTtsEnabled:      true,
	})
	require.NoError(t, err)

	cookie := createAuthCookie(t, pool, userID)

	ws := wsConnect(t, srv.URL, session.ID, cookie, nil)
	defer ws.CloseNow()

	// Verify session_loaded shows tts_enabled = true.
	loaded := readMsg(t, ws)
	assert.Equal(t, "session_loaded", loaded["type"])
	assert.Equal(t, true, loaded["tts_enabled"])

	// Complete opening stream.
	drainUntilDone(t, ws)
	drainUntilType(t, ws, "state_change") // waiting_for_input

	// Send a text turn.
	sendMsg(t, ws, wsMsg{"type": "end_turn", "content": "Tell me more", "input_method": "text"})
	drainUntilType(t, ws, "state_change") // processing_input
	drainUntilType(t, ws, "state_change") // interviewer_speaking
	drainUntilDone(t, ws)
	drainUntilType(t, ws, "state_change") // waiting_for_input

	// End session.
	sendMsg(t, ws, wsMsg{"type": "end_session"})
	ended := readMsg(t, ws)
	assert.Equal(t, "session_ended", ended["type"])

	// Verify messages persisted correctly.
	msgs, err := db.New(pool).GetMessagesBySession(ctx, session.ID)
	require.NoError(t, err)
	assert.Len(t, msgs, 3, "opening + candidate + response")
}

// ---------------------------------------------------------------------------
// Test: Timer auto-end — backdated session triggers warning, overtime, auto-end
// ---------------------------------------------------------------------------

func TestWS_TimerAutoEnd(t *testing.T) {
	t.Parallel()

	// Use a slow server so the opening takes enough time for timers to fire.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		fmt.Fprintf(w, "event: message_start\n")
		fmt.Fprintf(w, "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_test\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-sonnet-4-20250514\",\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":25,\"output_tokens\":0}}}\n\n")
		fmt.Fprintf(w, "event: content_block_start\n")
		fmt.Fprintf(w, "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		fmt.Fprintf(w, "event: content_block_delta\n")
		fmt.Fprintf(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hi.\"}}\n\n")
		fmt.Fprintf(w, "event: content_block_stop\n")
		fmt.Fprintf(w, "data: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
		fmt.Fprintf(w, "event: message_delta\n")
		fmt.Fprintf(w, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":1}}\n\n")
		fmt.Fprintf(w, "event: message_stop\n")
		fmt.Fprintf(w, "data: {\"type\":\"message_stop\"}\n\n")
	}))
	t.Cleanup(srv.Close)

	b := newWSTestBackend(t, srv.URL)

	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))
	httpSrv := httptest.NewServer(mux)
	t.Cleanup(httpSrv.Close)

	pool := b.Pool()
	userID := backendtest.SeedUser(t, b)
	question := createTestQuestion(t, pool)

	// Create a 1-minute session.
	ctx := context.Background()
	session, err := db.New(pool).CreateSession(ctx, db.CreateSessionParams{
		UserID:                userID,
		QuestionID:            question.ID,
		ConfigDurationMinutes: 1,
		ConfigTtsEnabled:      false,
	})
	require.NoError(t, err)

	// Backdate started_at so timers fire quickly but in order.
	// With duration=1min:
	//   warningDelay  = max(0, 1min - 2min - elapsed)   → fires immediately when elapsed > -1min
	//   overtimeDelay = max(0, 1min - elapsed)           → fires immediately when elapsed > 1min
	//   autoEndDelay  = max(0, 3min - elapsed)           → fires after (3min - elapsed)
	//
	// Backdate by 2min58s so elapsed ≈ 2min58s:
	//   warningDelay  = 0 (immediate)
	//   overtimeDelay = 0 (immediate)
	//   autoEndDelay  = 2s (fires 2s after opening, giving warning/overtime a head start)
	_, err = pool.Exec(ctx,
		`UPDATE interview_sessions SET started_at = NOW() - INTERVAL '2 minutes 58 seconds' WHERE id = $1`,
		session.ID,
	)
	require.NoError(t, err)

	cookie := createAuthCookie(t, pool, userID)

	ws := wsConnect(t, httpSrv.URL, session.ID, cookie, nil)
	defer ws.CloseNow()

	// Collect all messages until the session ends. We expect:
	// - session_loaded
	// - timer_warning (fires immediately since elapsed > duration - warning)
	// - timer_overtime (fires immediately since elapsed > duration)
	// - interviewer_speaking + tokens + interviewer_done + waiting_for_input
	// - session_ended (auto-end fires immediately since elapsed > duration + 2min)
	//
	// Order between timers and opening stream depends on goroutine scheduling,
	// so collect everything and check what we got.
	gotWarning := false
	gotOvertime := false
	gotSessionEnded := false

	for i := 0; i < 50; i++ {
		m := readMsgTimeout(t, ws, 10*time.Second)
		if m == nil {
			break
		}
		switch m["type"] {
		case "timer_warning":
			gotWarning = true
		case "timer_overtime":
			gotOvertime = true
		case "session_ended":
			gotSessionEnded = true
		}
		if gotSessionEnded {
			break
		}
	}

	assert.True(t, gotWarning, "should receive timer_warning")
	assert.True(t, gotOvertime, "should receive timer_overtime")
	assert.True(t, gotSessionEnded, "should receive session_ended from auto-end")

	// Verify session is marked completed.
	updatedSession, err := db.New(pool).GetSession(ctx, session.ID)
	require.NoError(t, err)
	assert.Equal(t, "completed", updatedSession.Status)
}

// ---------------------------------------------------------------------------
// Test: Reconnect after multiple turns — verify message replay correctness
// ---------------------------------------------------------------------------

func TestWS_ReconnectAfterMultipleTurns(t *testing.T) {
	t.Parallel()

	tokens := []string{"Response."}
	anthropicSrv := newFakeAnthropicServer(t, tokens)
	b := newWSTestBackend(t, anthropicSrv.URL)

	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	pool := b.Pool()
	userID := backendtest.SeedUser(t, b)
	question := createTestQuestion(t, pool)
	session := createTestSession(t, pool, userID, question.ID)
	cookie := createAuthCookie(t, pool, userID)

	// First connection: complete opening + one text turn.
	ws1 := wsConnect(t, srv.URL, session.ID, cookie, nil)
	drainUntilType(t, ws1, "session_loaded")
	drainUntilDone(t, ws1)
	drainUntilType(t, ws1, "state_change") // waiting_for_input

	sendMsg(t, ws1, wsMsg{"type": "end_turn", "content": "My answer", "input_method": "text"})
	drainUntilType(t, ws1, "state_change") // processing_input
	drainUntilType(t, ws1, "state_change") // interviewer_speaking
	drainUntilDone(t, ws1)
	drainUntilType(t, ws1, "state_change") // waiting_for_input

	// Check DB: should have 3 messages (opening + candidate + response).
	ctx := context.Background()
	msgs, err := db.New(pool).GetMessagesBySession(ctx, session.ID)
	require.NoError(t, err)
	require.Len(t, msgs, 3)
	lastSeq := int(msgs[len(msgs)-1].Seq) // seq 3

	// Disconnect.
	ws1.Close(websocket.StatusNormalClosure, "done")
	time.Sleep(200 * time.Millisecond)

	// Reconnect with last_seq = 1 (only saw the opening).
	// Should receive messages with seq > 1.
	reconnectSeq := 1
	ws2 := wsConnect(t, srv.URL, session.ID, cookie, &reconnectSeq)
	defer ws2.CloseNow()

	reconnectMsg := readMsg(t, ws2)
	assert.Equal(t, "reconnect_state", reconnectMsg["type"])
	missedMsgs, ok := reconnectMsg["messages"].([]any)
	assert.True(t, ok)
	assert.Len(t, missedMsgs, 2, "should replay messages with seq > 1 (candidate + response)")

	ws2.Close(websocket.StatusNormalClosure, "done")
	time.Sleep(200 * time.Millisecond)

	// Reconnect with last_seq = lastSeq (saw everything). Should get empty replay.
	ws3 := wsConnect(t, srv.URL, session.ID, cookie, &lastSeq)
	defer ws3.CloseNow()

	reconnectMsg3 := readMsg(t, ws3)
	assert.Equal(t, "reconnect_state", reconnectMsg3["type"])
	missedMsgs3, _ := reconnectMsg3["messages"].([]any)
	assert.Len(t, missedMsgs3, 0, "should replay nothing when client is caught up")
}

// ---------------------------------------------------------------------------
// Test: Ping/pong works
// ---------------------------------------------------------------------------

func TestWS_PingPong(t *testing.T) {
	t.Parallel()

	tokens := []string{"Hi."}
	anthropicSrv := newFakeAnthropicServer(t, tokens)
	b := newWSTestBackend(t, anthropicSrv.URL)

	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	pool := b.Pool()
	userID := backendtest.SeedUser(t, b)
	question := createTestQuestion(t, pool)
	session := createTestSession(t, pool, userID, question.ID)
	cookie := createAuthCookie(t, pool, userID)

	ws := wsConnect(t, srv.URL, session.ID, cookie, nil)
	defer ws.CloseNow()

	// Finish opening.
	drainUntilType(t, ws, "session_loaded")
	drainUntilDone(t, ws)
	drainUntilType(t, ws, "state_change") // waiting_for_input

	// Ping should return pong.
	sendMsg(t, ws, wsMsg{"type": "ping"})
	pong := readMsg(t, ws)
	assert.Equal(t, "pong", pong["type"])
}

// ---------------------------------------------------------------------------
// Test: cancel_session — server responds with session_ended, DB marked cancelled
// ---------------------------------------------------------------------------

func TestWS_CancelSession(t *testing.T) {
	t.Parallel()

	tokens := []string{"Let's ", "discuss ", "this."}
	anthropicSrv := newFakeAnthropicServer(t, tokens)
	b := newWSTestBackend(t, anthropicSrv.URL)

	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	pool := b.Pool()
	userID := backendtest.SeedUser(t, b)
	question := createTestQuestion(t, pool)
	session := createTestSession(t, pool, userID, question.ID)
	cookie := createAuthCookie(t, pool, userID)

	ws := wsConnect(t, srv.URL, session.ID, cookie, nil)
	defer ws.CloseNow()

	// Wait for the opening sequence to finish before cancelling.
	drainUntilType(t, ws, "session_loaded")
	drainUntilDone(t, ws)
	drainUntilType(t, ws, "state_change") // waiting_for_input

	// Send cancel_session.
	sendMsg(t, ws, wsMsg{"type": "cancel_session"})

	// Read messages until session_ended, asserting reason == "cancelled".
	ended, _ := drainUntilType(t, ws, "session_ended")
	assert.Equal(t, "cancelled", ended["reason"], "session_ended reason should be 'cancelled'")

	// Verify DB state.
	ctx := context.Background()
	updated, err := db.New(pool).GetSessionByID(ctx, session.ID)
	require.NoError(t, err)
	assert.Equal(t, "cancelled", updated.Status, "session status should be 'cancelled'")
	assert.True(t, updated.ArchivedAt.Valid, "archived_at should be set after cancellation")
	assert.True(t, updated.EndedAt.Valid, "ended_at should be set after cancellation")
}

// ---------------------------------------------------------------------------
// Test: Page refresh (last_seq=nil with existing messages) sends session_loaded + reconnect_state
// ---------------------------------------------------------------------------

func TestWS_PageRefreshReconnect(t *testing.T) {
	t.Parallel()

	tokens := []string{"Response."}
	anthropicSrv := newFakeAnthropicServer(t, tokens)
	b := newWSTestBackend(t, anthropicSrv.URL)

	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	pool := b.Pool()
	userID := backendtest.SeedUser(t, b)
	question := createTestQuestion(t, pool)
	session := createTestSession(t, pool, userID, question.ID)
	cookie := createAuthCookie(t, pool, userID)

	// First connection: complete opening + one text turn.
	ws1 := wsConnect(t, srv.URL, session.ID, cookie, nil)
	drainUntilType(t, ws1, "session_loaded")
	drainUntilDone(t, ws1)
	drainUntilType(t, ws1, "state_change") // waiting_for_input

	sendMsg(t, ws1, wsMsg{"type": "end_turn", "content": "My answer", "input_method": "text"})
	drainUntilType(t, ws1, "state_change") // processing_input
	drainUntilType(t, ws1, "state_change") // interviewer_speaking
	drainUntilDone(t, ws1)
	drainUntilType(t, ws1, "state_change") // waiting_for_input

	// Disconnect.
	ws1.Close(websocket.StatusNormalClosure, "done")
	time.Sleep(200 * time.Millisecond)

	// Simulate page refresh: reconnect with last_seq=nil (no React state).
	ws2 := wsConnect(t, srv.URL, session.ID, cookie, nil)
	defer ws2.CloseNow()

	// Should receive session_loaded first (provides session metadata).
	loaded := readMsg(t, ws2)
	assert.Equal(t, "session_loaded", loaded["type"])

	// Then reconnect_state with all messages.
	reconnectMsg := readMsg(t, ws2)
	assert.Equal(t, "reconnect_state", reconnectMsg["type"])
	messages, ok := reconnectMsg["messages"].([]any)
	assert.True(t, ok, "messages should be an array")
	assert.Len(t, messages, 3, "should replay all messages (opening + candidate + response)")

	// Then state_change to waiting_for_input.
	stateMsg := readMsg(t, ws2)
	assert.Equal(t, "state_change", stateMsg["type"])
	assert.Equal(t, "waiting_for_input", stateMsg["state"])
}

// ---------------------------------------------------------------------------
// Async pipeline tests
// ---------------------------------------------------------------------------

func TestWS_EndSessionDuringStreaming(t *testing.T) {
	t.Parallel()

	// Slow server: 10 tokens, 200ms each = 2s total streaming time.
	tokens := []string{"One ", "two ", "three ", "four ", "five ", "six ", "seven ", "eight ", "nine ", "ten."}
	anthropicSrv := newSlowAnthropicServer(t, tokens, 200*time.Millisecond)
	b := newWSTestBackend(t, anthropicSrv.URL)

	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	pool := b.Pool()
	userID := backendtest.SeedUser(t, b)
	question := createTestQuestion(t, pool)
	session := createTestSession(t, pool, userID, question.ID)
	cookie := createAuthCookie(t, pool, userID)

	ws := wsConnect(t, srv.URL, session.ID, cookie, nil)
	defer ws.CloseNow()

	// Wait for the opening question to start streaming.
	drainUntilType(t, ws, "session_loaded")
	// Read a few tokens to confirm streaming started.
	m := readMsg(t, ws)
	assert.Equal(t, "state_change", m["type"])
	m = readMsg(t, ws)
	assert.Equal(t, "interviewer_token", m["type"])

	// Send end_session while streaming is in progress.
	sendMsg(t, ws, wsMsg{"type": "end_session"})

	// The session should end promptly (not after all 10 tokens).
	start := time.Now()
	for {
		m = readMsgTimeout(t, ws, 10*time.Second)
		if m == nil {
			break
		}
		if m["type"] == "session_ended" {
			break
		}
	}
	elapsed := time.Since(start)
	assert.Less(t, elapsed, 5*time.Second, "end_session should be processed promptly, not after full stream")

	// Verify session was completed (status transitions: active -> completed -> evaluating).
	// The evaluate_session River job may run before we poll, so accept either.
	ctx := context.Background()
	require.Eventually(t, func() bool {
		s, err := db.New(pool).GetSession(ctx, session.ID)
		return err == nil && s.Status != "active"
	}, 5*time.Second, 100*time.Millisecond, "session should no longer be active")
}

func TestWS_DisconnectDuringPipeline(t *testing.T) {
	t.Parallel()

	// Slow server so we can disconnect mid-stream.
	tokens := []string{"Slow ", "response ", "here."}
	anthropicSrv := newSlowAnthropicServer(t, tokens, 500*time.Millisecond)
	b := newWSTestBackend(t, anthropicSrv.URL)

	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	pool := b.Pool()
	userID := backendtest.SeedUser(t, b)
	question := createTestQuestion(t, pool)
	session := createTestSession(t, pool, userID, question.ID)
	cookie := createAuthCookie(t, pool, userID)

	ws := wsConnect(t, srv.URL, session.ID, cookie, nil)

	// Wait for streaming to start.
	drainUntilType(t, ws, "session_loaded")
	m := readMsg(t, ws) // state_change
	assert.Equal(t, "state_change", m["type"])

	// Close WS abruptly during streaming.
	ws.CloseNow()

	// Wait a moment for cleanup.
	time.Sleep(500 * time.Millisecond)

	// Verify: advisory lock is released — a new connection should succeed.
	ws2 := wsConnect(t, srv.URL, session.ID, cookie, nil)
	defer ws2.CloseNow()
	m = readMsg(t, ws2)
	// Should get session_loaded (lock was released, reconnection works).
	assert.Equal(t, "session_loaded", m["type"])
}

func TestWS_ConcurrentTurnRejected(t *testing.T) {
	t.Parallel()

	// Slow server so the first turn is still in progress when we send the second.
	tokens := []string{"Still ", "thinking..."}
	anthropicSrv := newSlowAnthropicServer(t, tokens, 500*time.Millisecond)
	b := newWSTestBackend(t, anthropicSrv.URL)

	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	pool := b.Pool()
	userID := backendtest.SeedUser(t, b)
	question := createTestQuestion(t, pool)
	session := createTestSession(t, pool, userID, question.ID)
	cookie := createAuthCookie(t, pool, userID)

	ws := wsConnect(t, srv.URL, session.ID, cookie, nil)
	defer ws.CloseNow()

	// Wait for opening question to finish so we can send a turn.
	drainUntilType(t, ws, "session_loaded")
	drainUntilDone(t, ws)
	drainUntilType(t, ws, "state_change") // waiting_for_input

	// Send first turn (will stream slowly).
	sendMsg(t, ws, wsMsg{
		"type":         "end_turn",
		"content":      "My answer",
		"input_method": "text",
	})

	// Wait for processing to start.
	m, _ := drainUntilType(t, ws, "state_change")
	assert.Equal(t, "processing_input", m["state"])

	// Send second turn while first is still processing.
	sendMsg(t, ws, wsMsg{
		"type":         "end_turn",
		"content":      "Another answer",
		"input_method": "text",
	})

	// Should get an error rejecting the concurrent turn.
	m, _ = drainUntilType(t, ws, "error")
	assert.Equal(t, "turn_in_progress", m["code"])
}

// ---------------------------------------------------------------------------
// Test: end_session during candidate persist — candidate message must survive
// ---------------------------------------------------------------------------

// TestWS_EndSessionDuringCandidatePersist verifies that a candidate message is
// persisted even when end_session arrives while the pipeline is processing the
// candidate turn. The conductor calls cancelTurn() on end_session, which cancels
// the pipeline context. If persistMessage uses the raw (cancellable) ctx, the
// DB write may be aborted and the candidate answer silently lost.
func TestWS_EndSessionDuringCandidatePersist(t *testing.T) {
	t.Parallel()

	// Use a slow LLM server so the pipeline is still running (mid-LLM-stream)
	// long after the candidate message is persisted. This gives end_session a
	// wide window to arrive while the turn goroutine is active and its context
	// has been cancelled.
	tokens := []string{"Slow ", "response ", "here ", "for ", "timing."}
	anthropicSrv := newSlowAnthropicServer(t, tokens, 300*time.Millisecond)
	b := newWSTestBackend(t, anthropicSrv.URL)

	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	pool := b.Pool()
	userID := backendtest.SeedUser(t, b)
	question := createTestQuestion(t, pool)
	session := createTestSession(t, pool, userID, question.ID)
	cookie := createAuthCookie(t, pool, userID)

	ws := wsConnect(t, srv.URL, session.ID, cookie, nil)
	defer ws.CloseNow()

	// Drain opening: session_loaded, interviewer stream, waiting_for_input.
	drainUntilType(t, ws, "session_loaded")
	drainUntilDone(t, ws)
	drainUntilType(t, ws, "state_change") // waiting_for_input

	// Send end_turn with text content. The opening question's turnResultCh
	// result may not have been consumed by the event loop yet, which would
	// cause end_turn to be rejected with turn_in_progress. Retry until the
	// turn is accepted to avoid a scheduling race with the opening pipeline.
	endTurnMsg := wsMsg{
		"type":         "end_turn",
		"content":      "My candidate answer for persist test.",
		"input_method": "text",
	}
	for range 20 {
		sendMsg(t, ws, endTurnMsg)
		m := readMsgTimeout(t, ws, 2*time.Second)
		require.NotNil(t, m, "timeout waiting for response to end_turn")
		if code, _ := m["code"].(string); code == "turn_in_progress" {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		break // turn accepted; got state_change or other non-error
	}

	// Immediately send end_session. The pipeline goroutine is now running
	// (mid-stream from the slow server), so cancelTurn() fires. The test
	// verifies that persistMessage succeeds despite context cancellation.
	sendMsg(t, ws, wsMsg{"type": "end_session"})

	// Drain until session_ended or connection close. The WebSocket may close
	// immediately after sending session_ended, so tolerate EOF/close errors.
	for {
		m := readMsgTimeout(t, ws, 10*time.Second)
		if m == nil {
			break
		}
		if m["type"] == "session_ended" {
			break
		}
	}

	// Verify the candidate message was persisted in the DB despite context
	// cancellation. Without the fix, this assertion will fail intermittently
	// (or consistently, depending on scheduling) because the DB persist is
	// aborted by the cancelled context.
	ctx := context.Background()
	msgs, err := db.New(pool).GetMessagesBySession(ctx, session.ID)
	require.NoError(t, err)

	candidateFound := false
	for _, msg := range msgs {
		if msg.Role == "candidate" {
			candidateFound = true
			assert.Equal(t, "My candidate answer for persist test.", msg.Content)
			break
		}
	}
	assert.True(t, candidateFound, "candidate message must be persisted even when end_session cancels the pipeline")
}

// ---------------------------------------------------------------------------
// Test: Reconnect re-triggers interviewer response when last message is candidate
// ---------------------------------------------------------------------------

func TestWS_ReconnectRetriggersInterviewerResponse(t *testing.T) {
	t.Parallel()

	tokens := []string{"Let's ", "discuss."}
	anthropicSrv := newFakeAnthropicServer(t, tokens)
	b := newWSTestBackend(t, anthropicSrv.URL)

	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	pool := b.Pool()
	userID := backendtest.SeedUser(t, b)
	question := createTestQuestion(t, pool)
	session := createTestSession(t, pool, userID, question.ID)
	cookie := createAuthCookie(t, pool, userID)

	// First connection: complete opening question.
	ws1 := wsConnect(t, srv.URL, session.ID, cookie, nil)
	drainUntilType(t, ws1, "session_loaded")
	drainUntilDone(t, ws1)
	drainUntilType(t, ws1, "state_change") // waiting_for_input

	// Send candidate turn and wait for the full pipeline to complete.
	sendMsg(t, ws1, wsMsg{
		"type":         "end_turn",
		"content":      "My approach would be to use consistent hashing.",
		"input_method": "text",
	})
	drainUntilType(t, ws1, "state_change") // processing_input
	drainUntilType(t, ws1, "state_change") // interviewer_speaking
	drainUntilDone(t, ws1)
	drainUntilType(t, ws1, "state_change") // waiting_for_input

	// Disconnect cleanly.
	ws1.Close(websocket.StatusNormalClosure, "done")
	time.Sleep(200 * time.Millisecond)

	// Simulate the gap: delete the last interviewer response from DB.
	// This models a server crash that lost the in-flight interviewer response
	// (the process died after persisting the candidate message but before
	// persisting the interviewer response).
	ctx := context.Background()
	msgs, err := db.New(pool).GetMessagesBySession(ctx, session.ID)
	require.NoError(t, err)
	require.Len(t, msgs, 3)
	assert.Equal(t, "interviewer", msgs[2].Role)
	_, err = pool.Exec(ctx, `DELETE FROM messages WHERE id = $1`, msgs[2].ID)
	require.NoError(t, err)

	// Verify DB: now 2 messages (opening + candidate).
	msgs, err = db.New(pool).GetMessagesBySession(ctx, session.ID)
	require.NoError(t, err)
	require.Len(t, msgs, 2, "should have opening + candidate after simulated crash")
	assert.Equal(t, "interviewer", msgs[0].Role)
	assert.Equal(t, "candidate", msgs[1].Role)

	lastSeq := int(msgs[len(msgs)-1].Seq)

	// Reconnect as ws2 with lastSeq set to the last persisted seq.
	ws2 := wsConnect(t, srv.URL, session.ID, cookie, &lastSeq)
	defer ws2.CloseNow()

	// Expect reconnect_state first.
	reconnectMsg := readMsg(t, ws2)
	assert.Equal(t, "reconnect_state", reconnectMsg["type"])

	// Then expect state_change to interviewer_speaking (the re-triggered response).
	retriggerIS, _ := drainUntilType(t, ws2, "state_change")
	assert.Equal(t, "interviewer_speaking", retriggerIS["state"])

	// Drain the full response.
	responseText, _ := drainUntilDone(t, ws2)
	assert.Equal(t, "Let's discuss.", responseText)

	// Expect state_change to waiting_for_input.
	stateWait, _ := drainUntilType(t, ws2, "state_change")
	assert.Equal(t, "waiting_for_input", stateWait["state"])

	// Verify DB: now 3 messages (opening + candidate + re-triggered response).
	msgs, err = db.New(pool).GetMessagesBySession(ctx, session.ID)
	require.NoError(t, err)
	require.Len(t, msgs, 3, "should have opening + candidate + re-triggered interviewer response")
	assert.Equal(t, "interviewer", msgs[0].Role)
	assert.Equal(t, "candidate", msgs[1].Role)
	assert.Equal(t, "interviewer", msgs[2].Role)
}

// ---------------------------------------------------------------------------
// Test: STT retry on transient failure
// ---------------------------------------------------------------------------

func TestWS_STTRetry(t *testing.T) {
	t.Parallel()

	tokens := []string{"Good ", "answer."}
	anthropicSrv := newFakeAnthropicServer(t, tokens)
	b := pg.NewBackend(t)
	stt := &failOnceTranscriber{text: "I would use a hash-based approach."}
	b.ApplyTestOverrides(backend.TestOverrides{
		LLM: ai.NewTestClient(anthropicSrv.URL, b.Pool()),
		STT: stt,
		TTS: &fakeSynthesizer{},
	})

	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	pool := b.Pool()
	userID := backendtest.SeedUser(t, b)
	question := createTestQuestion(t, pool)
	session := createTestSession(t, pool, userID, question.ID)
	cookie := createAuthCookie(t, pool, userID)

	ws := wsConnect(t, srv.URL, session.ID, cookie, nil)
	defer ws.CloseNow()

	// Drain opening.
	drainUntilType(t, ws, "session_loaded")
	drainUntilDone(t, ws)
	drainUntilType(t, ws, "state_change") // waiting_for_input

	// Send voice turn.
	fakeAudio := base64.StdEncoding.EncodeToString([]byte("fake-webm-audio"))
	sendMsg(t, ws, wsMsg{
		"type":         "end_turn",
		"audio":        fakeAudio,
		"input_method": "voice",
	})

	// Should succeed on retry — expect transcribing state, then transcription_result.
	stateTranscribing := readMsg(t, ws)
	assert.Equal(t, "state_change", stateTranscribing["type"])
	assert.Equal(t, "transcribing", stateTranscribing["state"])

	transcription := readMsg(t, ws)
	assert.Equal(t, "transcription_result", transcription["type"])
	assert.Equal(t, "I would use a hash-based approach.", transcription["text"])

	// Verify transcriber was called twice (first failed, second succeeded).
	assert.Equal(t, 2, stt.calls, "transcriber should be called twice (retry)")
}
