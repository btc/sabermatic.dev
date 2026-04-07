# Transport Client Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extract WebSocket protocol encoding from `conductor.go` into a `transport.Client` interface, making the conductor protocol-agnostic and testable without WS infrastructure.

**Architecture:** New `internal/interview/transport/` package with `Client` interface, `WSClient` implementation, `TokenWriter` observer, `TTSSink` adapter, and `Recorder` for tests. The conductor replaces all `c.send(ctx, msgXxx(...))` calls with typed `c.client.Method(...)` calls. `messages.go` is deleted. The existing WS integration tests validate wire-format compatibility.

**Tech Stack:** Go 1.25, coder/websocket

**Spec:** `docs/superpowers/specs/2026-04-07-transport-client-design.md`

---

## File Map

### New files
- Create: `internal/interview/transport/client.go` — Client interface + domain types
- Create: `internal/interview/transport/ws_client.go` — WSClient implementation
- Create: `internal/interview/transport/ws_client_test.go` — WSClient unit tests
- Create: `internal/interview/transport/token_writer.go` — TokenObserver backed by Client
- Create: `internal/interview/transport/tts_sink.go` — TTSSink adapter backed by Client
- Create: `internal/interview/transport/recorder.go` — Recorder for tests

### Modified files
- Modify: `internal/interview/conductor.go` — replace `ws` field with `client`, rewrite all `c.send` calls, delete `ttsSink` struct, delete `send` helper
- Modify: `internal/handler/session_ws.go` — construct `transport.WSClient`, pass via new `ConductorParams`

### Deleted files
- Delete: `internal/interview/messages.go` — all constructors move into `WSClient`
- Delete: `internal/interview/observer/ws_writer.go` — replaced by `transport.TokenWriter`

---

## Task 1: Create Client interface and domain types

**Files:**
- Create: `internal/interview/transport/client.go`

- [ ] **Step 1: Create the transport package directory**

Run: `mkdir -p internal/interview/transport`

- [ ] **Step 2: Write client.go**

```go
package transport

import (
	"github.com/google/uuid"

	"github.com/btc/drill/internal/db"
)

// Client is the conductor's interface to the connected client.
// The conductor emits domain events; the transport implementation
// handles protocol encoding (WebSocket JSON, test recorder, etc.).
//
// Implementations must be safe for concurrent use. The conductor's
// main goroutine, audio upload goroutine, readLoop goroutine, and
// TTS goroutine all call Client methods.
type Client interface {
	StateChange(state string)
	SessionEnded(reason string)
	TranscriptionResult(text string)
	TimerWarning(minutesRemaining int)
	TimerOvertime()
	ReconnectPlease()
	Pong()
	TTSError()
	TTSDone(messageID uuid.UUID)
	AudioUploadFailed()
	InterviewerToken(token string)
	InterviewerDone(messageID uuid.UUID)

	Error(ClientError)
	SessionLoaded(SessionLoaded)
	ReconnectState(ReconnectState)
	TTSChunk(TTSChunk)
}

// ClientError represents an error sent to the client.
type ClientError struct {
	Code    string
	Message string
}

// SessionLoaded contains the initial session info sent on first connect.
type SessionLoaded struct {
	SessionID   uuid.UUID
	Question    db.Question
	DurationMin int
	TTSEnabled  bool
}

// ReconnectState contains the full message history for reconnection.
// Transports call Missed() to get only the messages the client hasn't seen.
type ReconnectState struct {
	LastSeq  int
	Messages []db.Message
}

// Missed returns only messages with seq > LastSeq.
func (r ReconnectState) Missed() []db.Message {
	missed := make([]db.Message, 0, len(r.Messages))
	for _, m := range r.Messages {
		if int(m.Seq) > r.LastSeq {
			missed = append(missed, m)
		}
	}
	return missed
}

// TTSChunk contains one chunk of synthesized audio.
type TTSChunk struct {
	MessageID uuid.UUID
	Data      []byte
	Seq       int
}
```

- [ ] **Step 3: Verify it compiles**

Run: `go build ./internal/interview/transport/`
Expected: success (no errors)

- [ ] **Step 4: Commit**

```bash
git add internal/interview/transport/client.go
git commit -m "feat(transport): add Client interface and domain types

The conductor's protocol-agnostic event interface. Implementations
handle encoding (WSClient for WebSocket JSON, Recorder for tests)."
```

---

## Task 2: Create WSClient implementation

**Files:**
- Create: `internal/interview/transport/ws_client.go`
- Create: `internal/interview/transport/ws_client_test.go`

- [ ] **Step 1: Write ws_client_test.go with tests for key methods**

```go
package transport_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/interview/transport"

	"github.com/coder/websocket"
)

// mockWSConn captures SendJSON calls for verification.
type mockWSConn struct {
	mu       sync.Mutex
	messages []json.RawMessage
}

func (m *mockWSConn) SendJSON(_ context.Context, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.messages = append(m.messages, data)
	m.mu.Unlock()
	return nil
}

func (m *mockWSConn) Close(_ websocket.StatusCode, _ string) error { return nil }

func (m *mockWSConn) last(t *testing.T) map[string]any {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	require.NotEmpty(t, m.messages, "no messages sent")
	var msg map[string]any
	require.NoError(t, json.Unmarshal(m.messages[len(m.messages)-1], &msg))
	return msg
}

func TestWSClient_StateChange(t *testing.T) {
	mock := &mockWSConn{}
	c := transport.NewWSClient(mock)
	c.StateChange("waiting_for_input")
	msg := mock.last(t)
	assert.Equal(t, "state_change", msg["type"])
	assert.Equal(t, "waiting_for_input", msg["state"])
}

func TestWSClient_Pong(t *testing.T) {
	mock := &mockWSConn{}
	c := transport.NewWSClient(mock)
	c.Pong()
	msg := mock.last(t)
	assert.Equal(t, "pong", msg["type"])
}

func TestWSClient_Error(t *testing.T) {
	mock := &mockWSConn{}
	c := transport.NewWSClient(mock)
	c.Error(transport.ClientError{Code: "turn_failed", Message: "oops"})
	msg := mock.last(t)
	assert.Equal(t, "error", msg["type"])
	assert.Equal(t, "turn_failed", msg["code"])
	assert.Equal(t, "oops", msg["message"])
}

func TestWSClient_InterviewerToken(t *testing.T) {
	mock := &mockWSConn{}
	c := transport.NewWSClient(mock)
	c.InterviewerToken("Hello ")
	msg := mock.last(t)
	assert.Equal(t, "interviewer_token", msg["type"])
	assert.Equal(t, "Hello ", msg["token"])
}

func TestWSClient_InterviewerDone(t *testing.T) {
	mock := &mockWSConn{}
	c := transport.NewWSClient(mock)
	id := uuid.New()
	c.InterviewerDone(id)
	msg := mock.last(t)
	assert.Equal(t, "interviewer_done", msg["type"])
	assert.Equal(t, id.String(), msg["message_id"])
}

func TestWSClient_SessionLoaded(t *testing.T) {
	mock := &mockWSConn{}
	c := transport.NewWSClient(mock)
	id := uuid.New()
	c.SessionLoaded(transport.SessionLoaded{
		SessionID:   id,
		Question:    db.Question{Title: "Design X", Prompt: "Design a system."},
		DurationMin: 45,
		TTSEnabled:  true,
	})
	msg := mock.last(t)
	assert.Equal(t, "session_loaded", msg["type"])
	assert.Equal(t, id.String(), msg["session_id"])
	assert.Equal(t, float64(45), msg["duration"])
	assert.Equal(t, true, msg["tts_enabled"])
	q, ok := msg["question"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "Design X", q["title"])
}

func TestWSClient_ReconnectState(t *testing.T) {
	mock := &mockWSConn{}
	c := transport.NewWSClient(mock)
	msgs := []db.Message{
		{Seq: 1, Role: "interviewer", Content: "Hi"},
		{Seq: 2, Role: "candidate", Content: "Hello"},
		{Seq: 3, Role: "interviewer", Content: "Good"},
	}
	c.ReconnectState(transport.ReconnectState{LastSeq: 1, Messages: msgs})
	msg := mock.last(t)
	assert.Equal(t, "reconnect_state", msg["type"])
	arr, ok := msg["messages"].([]any)
	require.True(t, ok)
	assert.Len(t, arr, 2, "should only include messages with seq > 1")
}

func TestWSClient_TTSChunk(t *testing.T) {
	mock := &mockWSConn{}
	c := transport.NewWSClient(mock)
	id := uuid.New()
	data := []byte("fake-audio")
	c.TTSChunk(transport.TTSChunk{MessageID: id, Data: data, Seq: 3})
	msg := mock.last(t)
	assert.Equal(t, "tts_chunk", msg["type"])
	assert.Equal(t, base64.StdEncoding.EncodeToString(data), msg["data"])
	assert.Equal(t, id.String(), msg["message_id"])
	assert.Equal(t, float64(3), msg["seq"])
}

func TestWSClient_SessionEnded(t *testing.T) {
	mock := &mockWSConn{}
	c := transport.NewWSClient(mock)
	c.SessionEnded("candidate")
	msg := mock.last(t)
	assert.Equal(t, "session_ended", msg["type"])
	assert.Equal(t, "candidate", msg["reason"])
}

func TestWSClient_TTSDone(t *testing.T) {
	mock := &mockWSConn{}
	c := transport.NewWSClient(mock)
	id := uuid.New()
	c.TTSDone(id)
	msg := mock.last(t)
	assert.Equal(t, "tts_done", msg["type"])
	assert.Equal(t, id.String(), msg["message_id"])
}
```

- [ ] **Step 2: Run the tests — they should fail (WSClient doesn't exist)**

Run: `go test ./internal/interview/transport/ -v -count=1`
Expected: compilation error — `transport.NewWSClient` undefined

- [ ] **Step 3: Write ws_client.go**

```go
package transport

import (
	"context"
	"encoding/base64"

	"github.com/google/uuid"

	"github.com/btc/drill/internal/interview/observer"
)

// WSClient implements Client over a coder/websocket connection.
// Thread-safe: coder/websocket serializes writes internally.
type WSClient struct {
	ws observer.WSConn
}

// NewWSClient creates a WSClient wrapping the given WebSocket connection.
func NewWSClient(ws observer.WSConn) *WSClient {
	return &WSClient{ws: ws}
}

// send writes a JSON message to the WebSocket. Errors are ignored — writes
// to a disconnected client are expected failures.
// Uses context.Background() because coder/websocket closes the connection
// when the write context is cancelled.
func (c *WSClient) send(v any) {
	_ = c.ws.SendJSON(context.Background(), v)
}

func (c *WSClient) StateChange(state string) {
	c.send(map[string]string{"type": "state_change", "state": state})
}

func (c *WSClient) SessionEnded(reason string) {
	c.send(map[string]any{"type": "session_ended", "reason": reason})
}

func (c *WSClient) TranscriptionResult(text string) {
	c.send(map[string]string{"type": "transcription_result", "text": text})
}

func (c *WSClient) TimerWarning(minutesRemaining int) {
	c.send(map[string]any{"type": "timer_warning", "minutes_remaining": minutesRemaining})
}

func (c *WSClient) TimerOvertime() {
	c.send(map[string]string{"type": "timer_overtime"})
}

func (c *WSClient) ReconnectPlease() {
	c.send(map[string]string{"type": "reconnect_please"})
}

func (c *WSClient) Pong() {
	c.send(map[string]string{"type": "pong"})
}

func (c *WSClient) TTSError() {
	c.send(map[string]string{"type": "tts_error"})
}

func (c *WSClient) TTSDone(messageID uuid.UUID) {
	c.send(map[string]any{"type": "tts_done", "message_id": messageID.String()})
}

func (c *WSClient) AudioUploadFailed() {
	c.send(map[string]string{"type": "audio_upload_failed"})
}

func (c *WSClient) InterviewerToken(token string) {
	c.send(map[string]string{"type": "interviewer_token", "token": token})
}

func (c *WSClient) InterviewerDone(messageID uuid.UUID) {
	c.send(map[string]any{"type": "interviewer_done", "message_id": messageID.String()})
}

func (c *WSClient) Error(e ClientError) {
	c.send(map[string]string{"type": "error", "code": e.Code, "message": e.Message})
}

func (c *WSClient) SessionLoaded(s SessionLoaded) {
	c.send(map[string]any{
		"type":        "session_loaded",
		"session_id":  s.SessionID.String(),
		"question":    map[string]string{"title": s.Question.Title, "prompt": s.Question.Prompt},
		"duration":    s.DurationMin,
		"tts_enabled": s.TTSEnabled,
	})
}

func (c *WSClient) ReconnectState(r ReconnectState) {
	c.send(map[string]any{"type": "reconnect_state", "messages": r.Missed()})
}

func (c *WSClient) TTSChunk(chunk TTSChunk) {
	c.send(map[string]any{
		"type":       "tts_chunk",
		"data":       base64.StdEncoding.EncodeToString(chunk.Data),
		"message_id": chunk.MessageID.String(),
		"seq":        chunk.Seq,
	})
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/interview/transport/ -v -count=1`
Expected: All pass

- [ ] **Step 5: Commit**

```bash
git add internal/interview/transport/ws_client.go internal/interview/transport/ws_client_test.go
git commit -m "feat(transport): add WSClient with unit tests

Implements transport.Client over coder/websocket. Each method
encodes to the WS JSON wire format. Thread-safe via coder/websocket's
internal write serialization."
```

---

## Task 3: Create TokenWriter and TTSSink adapter

**Files:**
- Create: `internal/interview/transport/token_writer.go`
- Create: `internal/interview/transport/tts_sink.go`

- [ ] **Step 1: Write token_writer.go**

```go
package transport

import "github.com/google/uuid"

// TokenWriter is a TokenObserver that emits tokens through transport.Client.
// Replaces observer.WSWriter.
type TokenWriter struct {
	client    Client
	messageID uuid.UUID
}

// NewTokenWriter creates a TokenWriter for the given message.
func NewTokenWriter(client Client, messageID uuid.UUID) *TokenWriter {
	return &TokenWriter{client: client, messageID: messageID}
}

func (w *TokenWriter) OnToken(token string) {
	w.client.InterviewerToken(token)
}

func (w *TokenWriter) OnDone(_ string) {
	w.client.InterviewerDone(w.messageID)
}

func (w *TokenWriter) OnError(err error) {
	w.client.Error(ClientError{Code: "llm_stream_error", Message: err.Error()})
}

func (w *TokenWriter) Interrupt() {}
```

- [ ] **Step 2: Write tts_sink.go**

```go
package transport

import (
	"github.com/google/uuid"

	"github.com/btc/drill/internal/interview/observer"
)

// ttsSink adapts transport.Client to observer.TTSSink for one turn.
// Created per turn with per-turn messageID and seq counter.
type ttsSink struct {
	client    Client
	messageID uuid.UUID
	seq       int
}

// NewTTSSink creates a TTSSink that sends TTS events through the Client.
func NewTTSSink(client Client, messageID uuid.UUID) observer.TTSSink {
	return &ttsSink{client: client, messageID: messageID}
}

func (s *ttsSink) HandleAudio(data []byte) {
	s.client.TTSChunk(TTSChunk{
		MessageID: s.messageID,
		Data:      data,
		Seq:       s.seq,
	})
	s.seq++
}

func (s *ttsSink) HandleTTSDone() {
	s.client.TTSDone(s.messageID)
}

func (s *ttsSink) HandleTTSError() {
	s.client.TTSError()
}
```

- [ ] **Step 3: Verify it compiles**

Run: `go build ./internal/interview/transport/`
Expected: success

- [ ] **Step 4: Commit**

```bash
git add internal/interview/transport/token_writer.go internal/interview/transport/tts_sink.go
git commit -m "feat(transport): add TokenWriter and TTSSink adapter

TokenWriter replaces observer.WSWriter — emits tokens through
transport.Client instead of directly via WebSocket.
TTSSink wraps transport.Client with per-turn messageID/seq state."
```

---

## Task 4: Create Recorder for tests

**Files:**
- Create: `internal/interview/transport/recorder.go`

- [ ] **Step 1: Write recorder.go**

```go
package transport

import (
	"sync"

	"github.com/google/uuid"
)

// Event types for Recorder.
type (
	StateChangeEvent        struct{ State string }
	SessionEndedEvent       struct{ Reason string }
	TranscriptionResultEvent struct{ Text string }
	TimerWarningEvent       struct{ MinutesRemaining int }
	TimerOvertimeEvent      struct{}
	ReconnectPleaseEvent    struct{}
	PongEvent               struct{}
	TTSErrorEvent           struct{}
	TTSDoneEvent            struct{ MessageID uuid.UUID }
	AudioUploadFailedEvent  struct{}
	InterviewerTokenEvent   struct{ Token string }
	InterviewerDoneEvent    struct{ MessageID uuid.UUID }
	ClientErrorEvent        struct{ ClientError }
	SessionLoadedEvent      struct{ SessionLoaded }
	ReconnectStateEvent     struct{ ReconnectState }
	TTSChunkEvent           struct{ TTSChunk }
)

// Recorder implements Client by appending events to a slice.
// Safe for concurrent use.
type Recorder struct {
	mu     sync.Mutex
	Events []any
}

func (r *Recorder) record(e any) {
	r.mu.Lock()
	r.Events = append(r.Events, e)
	r.mu.Unlock()
}

func (r *Recorder) StateChange(state string)            { r.record(StateChangeEvent{state}) }
func (r *Recorder) SessionEnded(reason string)           { r.record(SessionEndedEvent{reason}) }
func (r *Recorder) TranscriptionResult(text string)      { r.record(TranscriptionResultEvent{text}) }
func (r *Recorder) TimerWarning(minutesRemaining int)    { r.record(TimerWarningEvent{minutesRemaining}) }
func (r *Recorder) TimerOvertime()                       { r.record(TimerOvertimeEvent{}) }
func (r *Recorder) ReconnectPlease()                     { r.record(ReconnectPleaseEvent{}) }
func (r *Recorder) Pong()                                { r.record(PongEvent{}) }
func (r *Recorder) TTSError()                            { r.record(TTSErrorEvent{}) }
func (r *Recorder) TTSDone(messageID uuid.UUID)          { r.record(TTSDoneEvent{messageID}) }
func (r *Recorder) AudioUploadFailed()                   { r.record(AudioUploadFailedEvent{}) }
func (r *Recorder) InterviewerToken(token string)        { r.record(InterviewerTokenEvent{token}) }
func (r *Recorder) InterviewerDone(messageID uuid.UUID)  { r.record(InterviewerDoneEvent{messageID}) }
func (r *Recorder) Error(e ClientError)                  { r.record(ClientErrorEvent{e}) }
func (r *Recorder) SessionLoaded(s SessionLoaded)        { r.record(SessionLoadedEvent{s}) }
func (r *Recorder) ReconnectState(r2 ReconnectState)     { r.record(ReconnectStateEvent{r2}) }
func (r *Recorder) TTSChunk(c TTSChunk)                  { r.record(TTSChunkEvent{c}) }
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./internal/interview/transport/`
Expected: success

- [ ] **Step 3: Commit**

```bash
git add internal/interview/transport/recorder.go
git commit -m "feat(transport): add Recorder for tests

Implements transport.Client by appending typed events to a slice.
Thread-safe via sync.Mutex. Enables conductor unit tests without
WebSocket infrastructure."
```

---

## Task 5: Wire conductor.go to use transport.Client

This is the big task. Replace all `c.send` calls, update `ConductorParams`, delete `ttsSink`, and update `streamInterviewerResponse`.

**Files:**
- Modify: `internal/interview/conductor.go`
- Modify: `internal/handler/session_ws.go`

- [ ] **Step 1: Update ConductorParams and Conductor struct**

In `internal/interview/conductor.go`:

Replace the `ConductorParams` struct:

```go
type ConductorParams struct {
	// Client is the transport layer for sending events to the connected client.
	Client transport.Client

	// RawWS is the raw WebSocket for readLoop reads and connection lifecycle.
	RawWS *websocket.Conn

	// Backend is the service layer for DB, LLM, STT, and TTS operations.
	Backend *backend.Backend

	// SessionID identifies the interview session.
	SessionID uuid.UUID

	// UserID identifies the authenticated user.
	UserID uuid.UUID
}
```

In the `Conductor` struct, replace:
```go
	// WebSocket write interface. Thread-safe (coder/websocket).
	ws observer.WSConn
```
with:
```go
	// Transport layer for sending events to the client.
	client transport.Client
```

Update `NewConductor`:
```go
func NewConductor(p ConductorParams) *Conductor {
	c := &Conductor{
		rawWS:     p.RawWS,
		client:    p.Client,
		backend:   p.Backend,
		sessionID: p.SessionID,
		userID:    p.UserID,
	}
	c.obs.Store(observer.Noop)
	return c
}
```

- [ ] **Step 2: Delete ttsSink struct and its methods (lines 89-125)**

Remove the entire `ttsSink` type, its comment, and its three methods (`HandleAudio`, `HandleTTSDone`, `HandleTTSError`).

- [ ] **Step 3: Delete the `send` helper (lines 791-797)**

Remove:
```go
func (c *Conductor) send(ctx context.Context, v any) {
	if err := c.ws.SendJSON(ctx, v); err != nil {
		slog.Debug("conductor: send failed", "error", err, "session_id", c.sessionID)
	}
}
```

- [ ] **Step 4: Update close() to use rawWS**

Replace:
```go
func (c *Conductor) close() {
	c.ws.Close(websocket.StatusNormalClosure, "session ended")
```
with:
```go
func (c *Conductor) close() {
	c.rawWS.Close(websocket.StatusNormalClosure, "session ended")
```

- [ ] **Step 5: Update early-exit close paths**

Line 180 — replace `c.ws.Close(websocket.StatusInternalError, "lock error")` with `c.rawWS.Close(websocket.StatusInternalError, "lock error")`.

Line 184 — replace `c.ws.Close(websocket.StatusPolicyViolation, "session already in use")` with `c.rawWS.Close(websocket.StatusPolicyViolation, "session already in use")`.

- [ ] **Step 6: Replace all c.send calls in Run (event loop)**

Convert every `c.send(ctx, msgXxx(...))` to `c.client.Xxx(...)`. Here is the complete mapping for `Run`:

| Line | Before | After |
|------|--------|-------|
| 201 | `c.send(serverCtx, msgError("invalid_init", "expected session_init message"))` | `c.client.Error(transport.ClientError{Code: "invalid_init", Message: "expected session_init message"})` |
| 225 | `c.send(readCtx, msgError("malformed_message", err.Error()))` | `c.client.Error(transport.ClientError{Code: "malformed_message", Message: err.Error()})` |
| 251 | `c.send(workCtx, msgError("load_failed", "failed to load session"))` | `c.client.Error(transport.ClientError{Code: "load_failed", Message: "failed to load session"})` |
| 316 | `c.send(workCtx, msgReconnectPlease)` | `c.client.ReconnectPlease()` |
| 326 | `c.send(workCtx, msgError("turn_in_progress", "a turn is already being processed"))` | `c.client.Error(transport.ClientError{Code: "turn_in_progress", Message: "a turn is already being processed"})` |
| 361 | `c.send(workCtx, msgPong)` | `c.client.Pong()` |
| 363 | `c.send(workCtx, msgError("unknown_message_type", "unknown message type: "+msg.Type))` | `c.client.Error(transport.ClientError{Code: "unknown_message_type", Message: "unknown message type: " + msg.Type})` |
| 374 | `c.send(workCtx, msgError("turn_failed", "failed to process turn, please try again"))` | `c.client.Error(transport.ClientError{Code: "turn_failed", Message: "failed to process turn, please try again"})` |
| 394 | `c.send(workCtx, msgReconnectPlease)` | `c.client.ReconnectPlease()` |
| 399 | `c.send(workCtx, msgTimerWarning(warningMinutes(c.duration)))` | `c.client.TimerWarning(warningMinutes(c.duration))` |
| 402 | `c.send(workCtx, msgTimerOvertime)` | `c.client.TimerOvertime()` |
| 420 | `c.send(workCtx, msgReconnectPlease)` | `c.client.ReconnectPlease()` |

- [ ] **Step 7: Replace all c.send calls in sendInitialMessage**

| Line | Before | After |
|------|--------|-------|
| 487 | `c.send(ctx, msgReconnectState(afterSeq, c.messages))` | `c.client.ReconnectState(transport.ReconnectState{LastSeq: afterSeq, Messages: c.messages})` |
| 490 | `c.send(ctx, msgSessionLoaded(c.sessionID, c.question, int(c.duration.Minutes()), c.ttsEnabled))` | `c.client.SessionLoaded(transport.SessionLoaded{SessionID: c.sessionID, Question: c.question, DurationMin: int(c.duration.Minutes()), TTSEnabled: c.ttsEnabled})` |
| 493 | `c.send(ctx, msgReconnectState(0, c.messages))` | `c.client.ReconnectState(transport.ReconnectState{LastSeq: 0, Messages: c.messages})` |
| 494 | `c.send(ctx, msgStateChange(StateWaitingForInput))` | `c.client.StateChange(string(StateWaitingForInput))` |

- [ ] **Step 8: Replace all c.send calls in endTurn**

| Line | Before | After |
|------|--------|-------|
| 516 | `c.send(context.Background(), msgError("audio_validation_failed", "no audio data provided"))` | `c.client.Error(transport.ClientError{Code: "audio_validation_failed", Message: "no audio data provided"})` |
| 524 | `c.send(context.Background(), msgError("invalid_state_transition", err.Error()))` | `c.client.Error(transport.ClientError{Code: "invalid_state_transition", Message: err.Error()})` |
| 527 | `c.send(context.Background(), msgStateChange(StateTranscribing))` | `c.client.StateChange(string(StateTranscribing))` |
| 543 | `c.send(uploadCtx, map[string]string{"type": "audio_upload_failed"})` | `c.client.AudioUploadFailed()` |
| 572 | `c.send(context.Background(), msgTranscriptionResult(text))` | `c.client.TranscriptionResult(text)` |
| 577 | `c.send(context.Background(), msgError("empty_content", "text content cannot be empty"))` | `c.client.Error(transport.ClientError{Code: "empty_content", Message: "text content cannot be empty"})` |
| 585 | `c.send(context.Background(), msgError("invalid_state_transition", err.Error()))` | `c.client.Error(transport.ClientError{Code: "invalid_state_transition", Message: err.Error()})` |
| 588 | `c.send(context.Background(), msgStateChange(StateProcessingInput))` | `c.client.StateChange(string(StateProcessingInput))` |

- [ ] **Step 9: Replace c.send calls and observer construction in streamInterviewerResponse**

| Line | Before | After |
|------|--------|-------|
| 615 | `c.send(context.Background(), msgError("invalid_state_transition", err.Error()))` | `c.client.Error(transport.ClientError{Code: "invalid_state_transition", Message: err.Error()})` |
| 618 | `c.send(context.Background(), msgStateChange(StateInterviewerSpeaking))` | `c.client.StateChange(string(StateInterviewerSpeaking))` |
| 637 | `observer.NewWSWriter(c.ws, messageID)` | `transport.NewTokenWriter(c.client, messageID)` |
| 643 | `sink := &ttsSink{ws: c.ws, messageID: messageID}` | `sink := transport.NewTTSSink(c.client, messageID)` |
| 716 | `c.send(context.Background(), msgStateChange(StateWaitingForInput))` | `c.client.StateChange(string(StateWaitingForInput))` |

- [ ] **Step 10: Replace c.send calls in endSession and cancelSession**

| Line | Before | After |
|------|--------|-------|
| 729 | `c.send(ctx, msgError("invalid_state_transition", err.Error()))` | `c.client.Error(transport.ClientError{Code: "invalid_state_transition", Message: err.Error()})` |
| 741 | `c.send(ctx, msgSessionEnded("candidate"))` | `c.client.SessionEnded("candidate")` |
| 753 | `c.send(ctx, msgError("invalid_state_transition", err.Error()))` | `c.client.Error(transport.ClientError{Code: "invalid_state_transition", Message: err.Error()})` |
| 765 | `c.send(ctx, msgSessionEnded("cancelled"))` | `c.client.SessionEnded("cancelled")` |

- [ ] **Step 11: Update imports in conductor.go**

Remove unused imports: `"encoding/base64"` (moved to WSClient).

Add new import: `"github.com/btc/drill/internal/interview/transport"`.

The `"context"` import stays (used for `context.WithoutCancel`, `context.WithCancel`, `context.Background` in readLoop, workCtx).

Remove `observer` import only if no longer used. It IS still used for `observer.Noop`, `observer.NewTokenFanOut`, `observer.NewMessageAccumulator`, `observer.NewTTSAccumulator`, `observer.TTSAccumulatorParams`. So `observer` import stays.

- [ ] **Step 12: Update session_ws.go handler**

In `internal/handler/session_ws.go`, update the import and the conductor construction:

Add import: `"github.com/btc/drill/internal/interview/transport"`

Replace the conductor construction (lines 61-66):
```go
		conductor := interview.NewConductor(interview.ConductorParams{
			Client:    transport.NewWSClient(&interview.Conn{WS: ws}),
			RawWS:     ws,
			Backend:   b,
			SessionID: sessionID,
			UserID:    user.ID,
		})
```

- [ ] **Step 13: Verify it compiles**

Run: `go build ./internal/...`
Expected: success

- [ ] **Step 14: Run full test suite**

Run: `go test ./internal/handler/ -run TestWS -v -count=1`
Expected: All tests pass (wire format unchanged)

Run: `go test ./internal/interview/transport/ -v -count=1`
Expected: All pass

- [ ] **Step 15: Commit**

```bash
git add internal/interview/conductor.go internal/handler/session_ws.go
git commit -m "refactor(conductor): use transport.Client for all outbound events

Replaces 30 c.send(ctx, msgXxx(...)) calls with typed c.client.Xxx()
calls. Conductor no longer constructs WS JSON payloads or imports
encoding/base64. The ttsSink struct and send() helper are deleted."
```

---

## Task 6: Delete messages.go and observer/ws_writer.go

**Files:**
- Delete: `internal/interview/messages.go`
- Delete: `internal/interview/observer/ws_writer.go`

- [ ] **Step 1: Delete messages.go**

Run: `rm internal/interview/messages.go`

- [ ] **Step 2: Delete observer/ws_writer.go**

Run: `rm internal/interview/observer/ws_writer.go`

- [ ] **Step 3: Verify it compiles**

Run: `go build ./internal/...`
Expected: success — nothing references `msgXxx` constructors or `observer.NewWSWriter` anymore.

If there are compilation errors, it means a reference was missed in Task 5. Fix the reference and re-verify.

- [ ] **Step 4: Run full test suite**

Run: `go test ./internal/handler/ -run TestWS -v -count=1`
Expected: All pass

Run: `go test ./internal/interview/... -v -count=1`
Expected: All pass

- [ ] **Step 5: Commit**

```bash
git add -A internal/interview/messages.go internal/interview/observer/ws_writer.go
git commit -m "refactor: delete messages.go and observer/ws_writer.go

All message construction now lives in transport.WSClient.
Token streaming now goes through transport.TokenWriter.
Both replaced by the transport.Client abstraction."
```

---

## Task 7: Final verification

- [ ] **Step 1: Run the complete test suite**

Run: `go test ./... -count=1 2>&1 | tail -20`
Expected: All packages pass.

- [ ] **Step 2: Verify conductor.go no longer imports encoding/base64**

Run: `grep 'encoding/base64' internal/interview/conductor.go`
Expected: no output

- [ ] **Step 3: Verify conductor.go has no c.send calls**

Run: `grep 'c\.send(' internal/interview/conductor.go`
Expected: no output

- [ ] **Step 4: Verify no msgXxx references remain**

Run: `grep -r 'msgError\|msgPong\|msgState\|msgSession\|msgReconnect\|msgTimer\|msgTranscription' internal/interview/`
Expected: no output

- [ ] **Step 5: Count conductor.go lines**

Run: `wc -l internal/interview/conductor.go`
Expected: ~720 lines (down from 822 — removed ttsSink, send helper, reduced inline message construction)

---

## Task 8: Opus review — up to 2 passes

After all tasks are complete and tests pass, dispatch an Opus subagent to review the full refactor.

**Review scope:** All files in `internal/interview/transport/`, changes to `internal/interview/conductor.go`, changes to `internal/handler/session_ws.go`, deleted files.

**Review criteria:**
- Wire format preserved (no WS protocol changes vs. pre-refactor)
- Interface completeness (every conductor outbound event goes through transport.Client)
- Thread-safety (concurrent callers documented and safe)
- No dead code left behind (unused imports, orphaned references)
- Type consistency between Client interface, WSClient, Recorder, and conductor callsites
- Test coverage (WSClient unit tests cover all 16 methods)

**Process:**
- [ ] **Pass 1:** Dispatch Opus review subagent. Fix ALL findings (HIGH, MEDIUM, LOW, NIT). Commit fixes.
- [ ] **Pass 2 (if needed):** If Pass 1 had findings, dispatch a second Opus review to verify fixes and check for new issues. Fix any new findings. Commit.
- [ ] **Escalate:** If you disagree with a finding, do not silently skip it. Escalate to the user with the finding and your reasoning.
