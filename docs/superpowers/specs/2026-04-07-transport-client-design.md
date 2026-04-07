# Transport Client: Protocol-Agnostic Conductor Events

**Date:** 2026-04-07
**Status:** Draft

## Purpose

Extract WebSocket protocol encoding from the conductor into a `transport.Client` interface. The conductor emits domain events through typed methods; the transport implementation handles protocol encoding. This makes the conductor testable without WebSocket infrastructure and consolidates all WS message construction in one place.

## Interface

```go
package transport

import (
    "github.com/google/uuid"

    "github.com/btc/drill/internal/db"
)

// Client is the conductor's interface to the connected client.
// The conductor emits domain events; the transport implementation
// handles protocol encoding (WebSocket JSON, test recorder, etc.).
type Client interface {
    // Single-arg or zero-arg — bare values when types differ
    StateChange(state string)
    SessionEnded(reason string)
    TranscriptionResult(text string)
    TimerWarning(minutesRemaining int)
    TimerOvertime()
    ReconnectPlease()
    TTSError()
    TTSDone(messageID uuid.UUID)
    AudioUploadFailed()

    // Multi-arg structs — same-type args or 3+ args
    Error(ClientError)
    InterviewerToken(InterviewerToken)
    InterviewerDone(InterviewerDone)
    SessionLoaded(SessionLoaded)
    ReconnectState(ReconnectState)
    TTSChunk(TTSChunk)
}
```

### Arg convention

- **Zero args:** bare method — `TimerOvertime()`, `ReconnectPlease()`, `TTSError()`, `AudioUploadFailed()`
- **One arg:** bare value — `StateChange(state string)`, `SessionEnded(reason string)`
- **Two args, different types:** bare values — `TTSDone(messageID uuid.UUID)`
- **Two args, same type:** struct — `Error(ClientError)` (both strings)
- **Three+ args:** struct — `SessionLoaded(SessionLoaded)`, `TTSChunk(TTSChunk)`

## Domain Types

```go
type ClientError struct {
    Code    string
    Message string
}

type InterviewerToken struct {
    MessageID uuid.UUID
    Token     string
}

type InterviewerDone struct {
    MessageID uuid.UUID
    FullText  string
}

type SessionLoaded struct {
    SessionID   uuid.UUID
    Question    db.Question
    DurationMin int
    TTSEnabled  bool
}

type ReconnectState struct {
    LastSeq  int
    Messages []db.Message
}

// Missed returns only messages with seq > LastSeq.
// Transports call this to get the replay set.
func (r ReconnectState) Missed() []db.Message {
    missed := make([]db.Message, 0, len(r.Messages))
    for _, m := range r.Messages {
        if int(m.Seq) > r.LastSeq {
            missed = append(missed, m)
        }
    }
    return missed
}

type TTSChunk struct {
    MessageID uuid.UUID
    Data      []byte
    Seq       int
}
```

## Package Layout

```
internal/interview/
    transport/
        client.go      — Client interface + domain types
        ws_client.go   — WSClient implementation
        recorder.go    — Recorder for tests
    conductor.go       — uses transport.Client, no WS imports
    state_machine.go   — unchanged
    ws_message.go      — inbound WS parsing (unchanged for now)
    observer/          — unchanged
```

## WSClient Implementation

`transport.WSClient` implements `Client` over a `coder/websocket` connection.

```go
type WSClient struct {
    ws observer.WSConn
}

func NewWSClient(ws observer.WSConn) *WSClient {
    return &WSClient{ws: ws}
}
```

All writes use `context.Background()` because `coder/websocket` closes the connection when the write context is cancelled. The WS's internal write timeout bounds write duration.

Each method encodes to the WS JSON format:

```go
func (c *WSClient) StateChange(state string) {
    c.send(map[string]string{"type": "state_change", "state": state})
}

func (c *WSClient) TTSChunk(chunk TTSChunk) {
    c.send(map[string]any{
        "type":       "tts_chunk",
        "data":       base64.StdEncoding.EncodeToString(chunk.Data),
        "message_id": chunk.MessageID.String(),
        "seq":        chunk.Seq,
    })
}

// etc. — one method per event, each self-contained
```

Private helper:

```go
func (c *WSClient) send(v any) {
    _ = c.ws.SendJSON(context.Background(), v)
}
```

## TTS Sink Adapter

The existing `observer.TTSSink` interface stays unchanged. A thin adapter wraps `transport.Client` with per-turn state (messageID, seq counter):

```go
// ttsSink adapts transport.Client to observer.TTSSink for one turn.
type ttsSink struct {
    client    Client
    messageID uuid.UUID
    seq       int
}

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

Lives in the transport package. Created per turn in `streamInterviewerResponse`:

```go
sink := transport.NewTTSSink(c.client, messageID)
```

## Recorder for Tests

```go
// Recorder implements Client by appending events to a slice.
type Recorder struct {
    Events []any
}

func (r *Recorder) StateChange(state string) {
    r.Events = append(r.Events, StateChangeEvent{State: state})
}

// ... one record type per method
```

This enables conductor unit tests without WS infrastructure — assert on the event sequence.

## What Changes in conductor.go

| Before | After |
|--------|-------|
| `ws observer.WSConn` field | `client transport.Client` field |
| `send(ctx context.Context, v any)` helper | Deleted |
| `c.send(ctx, msgStateChange(StateWaitingForInput))` | `c.client.StateChange(string(StateWaitingForInput))` |
| `c.send(ctx, msgError("code", "msg"))` | `c.client.Error(transport.ClientError{Code: "code", Message: "msg"})` |
| `c.send(ctx, msgSessionLoaded(...))` | `c.client.SessionLoaded(transport.SessionLoaded{...})` |
| `c.send(ctx, msgReconnectState(seq, msgs))` | `c.client.ReconnectState(transport.ReconnectState{LastSeq: seq, Messages: msgs})` |
| `ttsSink` struct + 3 methods | Deleted — replaced by `transport.NewTTSSink(c.client, messageID)` |
| `messages.go` (msgXxx constructors) | Deleted — logic moved into WSClient methods |

## What Does NOT Change

- **Inbound WS parsing:** `ws_message.go` stays. The `WSMessage` struct, `ParseWSMessage`, and the `msg.Type` switch in `Run` are unchanged. Inbound extraction is a separate refactor.
- **Observer package:** `observer.TokenObserver`, `observer.TTSSink`, `observer.WSConn` interfaces unchanged. `observer.NewWSWriter` still takes a `WSConn` directly (it's an observer, not a transport concern — it streams tokens to the client during LLM output).
- **State machine:** Unchanged.
- **`rawWS` field:** The conductor still holds the raw `*websocket.Conn` for the readLoop. Only outbound messages go through `transport.Client`.

## Observer WSWriter Consideration

`observer.NewWSWriter` currently takes `observer.WSConn` and sends `interviewer_token` and `interviewer_done` messages directly. These are the same events as `transport.InterviewerToken` and `transport.InterviewerDone`.

Two options:
- **A) Leave it:** WSWriter stays in observer, sends WS messages directly. Two paths for token/done messages (WSWriter for streaming, transport.Client for everything else). Inconsistent but avoids changing the observer package.
- **B) Replace it:** WSWriter becomes a `transport.Client`-backed observer that calls `c.client.InterviewerToken(...)` and `c.client.InterviewerDone(...)`.

**Recommendation: B.** The inconsistency of A is worse than the small change. WSWriter becomes:

```go
// TokenWriter is a TokenObserver that emits tokens through transport.Client.
type TokenWriter struct {
    client    Client
    messageID uuid.UUID
}

func NewTokenWriter(client Client, messageID uuid.UUID) *TokenWriter {
    return &TokenWriter{client: client, messageID: messageID}
}

func (w *TokenWriter) OnToken(token string) {
    w.client.InterviewerToken(InterviewerToken{MessageID: w.messageID, Token: token})
}

func (w *TokenWriter) OnDone(fullText string) {
    w.client.InterviewerDone(InterviewerDone{MessageID: w.messageID, FullText: fullText})
}

func (w *TokenWriter) OnError(_ error) {}
func (w *TokenWriter) Interrupt()      {}
```

Lives in transport package. Implements `observer.TokenObserver`. Replaces `observer.NewWSWriter` in `streamInterviewerResponse`.

## ConductorParams Change

```go
// Before
type ConductorParams struct {
    WS        *websocket.Conn
    Backend   *backend.Backend
    SessionID uuid.UUID
    UserID    uuid.UUID
}

// After
type ConductorParams struct {
    Client    transport.Client
    RawWS     *websocket.Conn    // for readLoop reads only
    Backend   *backend.Backend
    SessionID uuid.UUID
    UserID    uuid.UUID
}
```

The handler constructs `transport.NewWSClient(ws)` and passes it in. The conductor no longer wraps the WS connection itself.

## Close Behavior

The conductor currently calls `c.ws.Close(code, reason)` in `close()`. With the transport.Client refactor, closing the connection is still needed. Options:

- Add `Close(code websocket.StatusCode, reason string)` to `transport.Client` — but this leaks WS-specific types
- Keep the `rawWS` field and close it directly in `close()` — the conductor already holds `rawWS` for reads

**Recommendation:** Close via `rawWS` directly. The transport.Client is for outbound events, not connection lifecycle. The `close()` method becomes:

```go
func (c *Conductor) close() {
    c.rawWS.Close(websocket.StatusNormalClosure, "session ended")
    if err := c.lock.Release(); err != nil {
        slog.Error("conductor: release session lock", "error", err, "session_id", c.sessionID)
    }
}
```

Same as today but uses `rawWS` instead of `c.ws.Close()`.

## Testing Strategy

- **WSClient:** Unit test each method — verify JSON output matches expected WS protocol.
- **Conductor with Recorder:** Unit test conductor logic by asserting on the sequence of emitted events. No WS infrastructure needed.
- **Existing WS integration tests:** Still pass — they test the full stack (handler → conductor → WSClient → actual WebSocket). These are the primary correctness tests.
