# Transport Client: Protocol-Agnostic Conductor Events

**Date:** 2026-04-07
**Status:** Approved

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
//
// Implementations must be safe for concurrent use. The conductor's
// main goroutine, audio upload goroutine, readLoop goroutine, and
// TTS goroutine all call Client methods.
type Client interface {
    // Single-arg or zero-arg — bare values when types differ
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

    // Single-arg bare values — token streaming
    InterviewerToken(token string)
    InterviewerDone(messageID uuid.UUID)

    // Multi-arg structs — same-type args or 3+ args
    Error(ClientError)
    SessionLoaded(SessionLoaded)
    ReconnectState(ReconnectState)
    TTSChunk(TTSChunk)
}
```

### Arg convention

- **Zero args:** bare method — `TimerOvertime()`, `ReconnectPlease()`, `Pong()`, `TTSError()`, `AudioUploadFailed()`
- **One arg:** bare value — `StateChange(state string)`, `SessionEnded(reason string)`, `InterviewerToken(token string)`, `InterviewerDone(messageID uuid.UUID)`
- **Two args, same type:** struct — `Error(ClientError)` (both strings)
- **Three+ args:** struct — `SessionLoaded(SessionLoaded)`, `TTSChunk(TTSChunk)`

## Domain Types

```go
type ClientError struct {
    Code    string
    Message string
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
        client.go       — Client interface + domain types
        ws_client.go    — WSClient implementation
        token_writer.go — TokenObserver backed by Client
        tts_sink.go     — TTSSink adapter backed by Client
        recorder.go     — Recorder for tests
    conductor.go        — uses transport.Client, no WS imports
    state_machine.go    — unchanged
    ws_message.go       — inbound WS parsing (unchanged for now)
    observer/           — unchanged (interfaces only)
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

All writes use `context.Background()` because `coder/websocket` closes the connection when the write context is cancelled. The WS's internal write timeout bounds write duration. `SendJSON` is thread-safe (`coder/websocket` serializes writes), so `WSClient` is safe for concurrent use.

Each method encodes to the WS JSON format:

```go
func (c *WSClient) StateChange(state string) {
    c.send(map[string]string{"type": "state_change", "state": state})
}

func (c *WSClient) Pong() {
    c.send(map[string]string{"type": "pong"})
}

func (c *WSClient) InterviewerToken(token string) {
    c.send(map[string]string{"type": "interviewer_token", "token": token})
}

func (c *WSClient) InterviewerDone(messageID uuid.UUID) {
    c.send(map[string]any{"type": "interviewer_done", "message_id": messageID.String()})
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

## TokenWriter — Observer backed by Client

Replaces `observer.NewWSWriter`. Lives in transport package, implements `observer.TokenObserver`.

```go
type TokenWriter struct {
    client    Client
    messageID uuid.UUID
}

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

**Note on OnDone:** The `fullText` parameter is ignored — the current wire protocol does not send full text in `interviewer_done`. The conductor uses `MessageAccumulator.Text()` for persistence, not the observer's `OnDone` parameter.

**Note on OnError:** The current `WSWriter.OnError` sends `{"type": "error", "message": err.Error()}` to the client. `TokenWriter.OnError` preserves this behavior by calling `c.client.Error(...)` with a `"llm_stream_error"` code. This is a minor wire format addition (the `code` field) that improves client-side error handling.

## TTS Sink Adapter

The existing `observer.TTSSink` interface stays unchanged. A thin adapter wraps `transport.Client` with per-turn state (messageID, seq counter):

```go
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

func (r *Recorder) StateChange(state string) {
    r.record(StateChangeEvent{State: state})
}

// ... 16 event types total (one per Client method)
```

Event types: `StateChangeEvent`, `SessionEndedEvent`, `TranscriptionResultEvent`, `TimerWarningEvent`, `TimerOvertimeEvent`, `ReconnectPleaseEvent`, `PongEvent`, `TTSErrorEvent`, `TTSDoneEvent`, `AudioUploadFailedEvent`, `InterviewerTokenEvent`, `InterviewerDoneEvent`, `ClientErrorEvent`, `SessionLoadedEvent`, `ReconnectStateEvent`, `TTSChunkEvent`.

This enables conductor unit tests without WS infrastructure — assert on the event sequence.

## What Changes in conductor.go

| Before | After |
|--------|-------|
| `ws observer.WSConn` field | `client transport.Client` field |
| `send(ctx context.Context, v any)` helper | Deleted |
| `c.send(ctx, msgPong)` | `c.client.Pong()` |
| `c.send(ctx, msgStateChange(StateWaitingForInput))` | `c.client.StateChange(string(StateWaitingForInput))` |
| `c.send(ctx, msgError("code", "msg"))` | `c.client.Error(transport.ClientError{Code: "code", Message: "msg"})` |
| `c.send(ctx, msgSessionLoaded(...))` | `c.client.SessionLoaded(transport.SessionLoaded{...})` |
| `c.send(ctx, msgReconnectState(seq, msgs))` | `c.client.ReconnectState(transport.ReconnectState{LastSeq: seq, Messages: msgs})` |
| `c.send(uploadCtx, map[string]string{"type": "audio_upload_failed"})` | `c.client.AudioUploadFailed()` |
| readLoop `c.send(readCtx, msgError("malformed_message", ...))` | `c.client.Error(transport.ClientError{Code: "malformed_message", Message: ...})` |
| `ttsSink` struct + 3 methods | Deleted — replaced by `transport.NewTTSSink(c.client, messageID)` |
| `messages.go` (msgXxx constructors + package-level vars) | Deleted — logic moved into WSClient methods |
| `observer.NewWSWriter(c.ws, messageID)` | `transport.NewTokenWriter(c.client, messageID)` |

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

The conductor closes the WS connection in several paths:

- **Normal close** (`close()` method): Uses `rawWS.Close(websocket.StatusNormalClosure, "session ended")`. Called at the end of `Run` via defer.
- **Early exit — lock error** (line 180): Uses `rawWS.Close(websocket.StatusInternalError, "lock error")` before `c.lock` is set.
- **Early exit — lock unavailable** (line 184): Uses `rawWS.Close(websocket.StatusPolicyViolation, "session already in use")`.

All close paths use `rawWS` directly. `transport.Client` is for outbound events, not connection lifecycle. The `Conn` wrapper (in `ws_message.go`) that currently implements `observer.WSConn` is only used to construct `WSClient`; the conductor itself no longer references it.

## What Does NOT Change

- **Inbound WS parsing:** `ws_message.go` stays. The `WSMessage` struct, `ParseWSMessage`, and the `msg.Type` switch in `Run` are unchanged. Inbound extraction is a separate refactor.
- **Observer package:** `observer.TokenObserver`, `observer.TTSSink`, `observer.WSConn` interfaces unchanged. `observer.NewWSWriter` becomes unused and can be deleted after `transport.TokenWriter` replaces it.
- **State machine:** Unchanged.
- **`rawWS` field:** The conductor still holds the raw `*websocket.Conn` for the readLoop and close paths. Only outbound messages go through `transport.Client`.

## Testing Strategy

- **WSClient:** Unit test each method — verify JSON output matches expected WS protocol. Use a mock `WSConn` that captures `SendJSON` args.
- **Conductor with Recorder:** Unit test conductor logic by asserting on the sequence of emitted events. No WS infrastructure needed.
- **Existing WS integration tests:** Still pass unchanged — they test the full stack (handler → conductor → WSClient → actual WebSocket). These are the primary correctness tests.
