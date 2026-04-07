# Remove state_change WS Messages Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove the `state_change` WebSocket message type entirely, derive all frontend `InterviewState` transitions from existing data messages, and fix the latent stuck-state bug in the error path.

**Architecture:** The conductor stops broadcasting explicit state transitions. The frontend infers state from `interviewer_token` (→ streaming), `interviewer_done` (→ waiting, reordered to after DB persist), `transcription_result` (→ processing), and `error` (→ waiting). `reconnect_state` gains a `state: "waiting"` field replacing the trailing `state_change` in `sendInitialMessage`. The `TokenWriter.OnDone` no longer sends `InterviewerDone`; the conductor sends it after DB persist and state transition.

**Tech Stack:** Go (backend), TypeScript/React (frontend), coder/websocket, testify, vitest

**Spec:** `docs/superpowers/specs/2026-04-07-remove-state-change-design.md`

---

## File Map

| File | Change |
|------|--------|
| `internal/interview/transport/client.go` | Remove `StateChange` from `Client` interface; add `State string` to `ReconnectState` |
| `internal/interview/transport/ws_client.go` | Remove `StateChange` method; add `state` to `reconnect_state` JSON |
| `internal/interview/transport/recorder.go` | Remove `StateChangeEvent` type and `StateChange` method |
| `internal/interview/transport/token_writer.go` | Empty `OnDone` — conductor now sends `InterviewerDone` |
| `internal/interview/transport/ws_client_test.go` | Delete `TestWSClient_StateChange`; update `TestWSClient_ReconnectState` |
| `internal/interview/conductor.go` | Remove 5 `StateChange` calls; move `InterviewerDone` to after persist; set `ReconnectState.State` |
| `internal/handler/session_ws_test.go` | Remove all `state_change` expectations (~81 references); update sync barriers |
| `web/src/ws/protocol.ts` | Remove `state_change` from `ServerMessage`; add `state` to `reconnect_state` |
| `web/src/ws/hooks.ts` | Update `handleMessage` for derived state transitions |

---

### Task 1: Transport layer — remove StateChange, add State to ReconnectState

**Files:**
- Modify: `internal/interview/transport/client.go`
- Modify: `internal/interview/transport/ws_client.go`
- Modify: `internal/interview/transport/recorder.go`
- Modify: `internal/interview/transport/ws_client_test.go`

- [ ] **Step 1: Update TestWSClient_ReconnectState to assert the new state field**

In `internal/interview/transport/ws_client_test.go`, find `TestWSClient_ReconnectState` (currently at line 110) and add the state assertion:

```go
func TestWSClient_ReconnectState(t *testing.T) {
	ws := &mockWSConn{}
	c := transport.NewWSClient(ws)

	msgs := []db.Message{
		{Seq: 1, Role: "user", Content: "hello"},
		{Seq: 2, Role: "assistant", Content: "hi"},
		{Seq: 3, Role: "user", Content: "thanks"},
	}
	// LastSeq=1 means only seq 2 and 3 are missed
	c.ReconnectState(transport.ReconnectState{LastSeq: 1, Messages: msgs, State: "waiting"})
	msg := last(t, ws)
	assert.Equal(t, "reconnect_state", msg["type"])
	messages, ok := msg["messages"].([]any)
	require.True(t, ok)
	assert.Len(t, messages, 2, "only missed messages should be sent")
	assert.Equal(t, "waiting", msg["state"])
}
```

- [ ] **Step 2: Delete TestWSClient_StateChange**

Remove the entire `TestWSClient_StateChange` function (currently lines 43-50):

```go
func TestWSClient_StateChange(t *testing.T) {
	ws := &mockWSConn{}
	c := transport.NewWSClient(ws)
	c.StateChange("active")
	msg := last(t, ws)
	assert.Equal(t, "state_change", msg["type"])
	assert.Equal(t, "active", msg["state"])
}
```

- [ ] **Step 3: Run tests to confirm they fail**

```bash
cd /Users/btc/Projects/src/drill
go test ./internal/interview/transport/... -run TestWSClient -v 2>&1 | tail -20
```

Expected: FAIL — `State` field not found, `StateChange` method not found on interface.

- [ ] **Step 4: Update client.go — remove StateChange from interface, add State to ReconnectState**

In `internal/interview/transport/client.go`:

```go
// Client is the conductor's interface to the connected client.
// The conductor emits domain events; the transport implementation
// handles protocol encoding (WebSocket JSON, test recorder, etc.).
//
// Implementations must be safe for concurrent use. The conductor's
// main goroutine, audio upload goroutine, readLoop goroutine, and
// TTS goroutine all call Client methods.
type Client interface {
	Ack(action string)
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
```

Update `ReconnectState` struct to add `State`:

```go
type ReconnectState struct {
	LastSeq  int
	Messages []db.Message
	State    string
}
```

- [ ] **Step 5: Update ws_client.go — remove StateChange method, add state to reconnect_state JSON**

Delete the `StateChange` method entirely:

```go
// Remove this method:
// func (c *WSClient) StateChange(state string) {
//     c.send(map[string]string{"type": "state_change", "state": state})
// }
```

Update `ReconnectState` to include the `state` field:

```go
func (c *WSClient) ReconnectState(r ReconnectState) {
	c.send(map[string]any{"type": "reconnect_state", "messages": r.Missed(), "state": r.State})
}
```

- [ ] **Step 6: Update recorder.go — remove StateChangeEvent and StateChange method**

In `internal/interview/transport/recorder.go`, remove `StateChangeEvent` from the type block and remove the `StateChange` method:

```go
package transport

import (
	"sync"

	"github.com/google/uuid"
)

type (
	AckEvent                 struct{ Action string }
	TranscriptionResultEvent struct{ Text string }
	TimerWarningEvent        struct{ MinutesRemaining int }
	TimerOvertimeEvent       struct{}
	ReconnectPleaseEvent     struct{}
	PongEvent                struct{}
	TTSErrorEvent            struct{}
	TTSDoneEvent             struct{ MessageID uuid.UUID }
	AudioUploadFailedEvent   struct{}
	InterviewerTokenEvent    struct{ Token string }
	InterviewerDoneEvent     struct{ MessageID uuid.UUID }
	ClientErrorEvent         struct{ ClientError }
	SessionLoadedEvent       struct{ SessionLoaded }
	ReconnectStateEvent      struct{ ReconnectState }
	TTSChunkEvent            struct{ TTSChunk }
)

type Recorder struct {
	mu     sync.Mutex
	Events []any
}

func (r *Recorder) record(e any) {
	r.mu.Lock()
	r.Events = append(r.Events, e)
	r.mu.Unlock()
}

func (r *Recorder) Ack(action string)                    { r.record(AckEvent{action}) }
func (r *Recorder) TranscriptionResult(text string)     { r.record(TranscriptionResultEvent{text}) }
func (r *Recorder) TimerWarning(minutesRemaining int)   { r.record(TimerWarningEvent{minutesRemaining}) }
func (r *Recorder) TimerOvertime()                      { r.record(TimerOvertimeEvent{}) }
func (r *Recorder) ReconnectPlease()                    { r.record(ReconnectPleaseEvent{}) }
func (r *Recorder) Pong()                               { r.record(PongEvent{}) }
func (r *Recorder) TTSError()                           { r.record(TTSErrorEvent{}) }
func (r *Recorder) TTSDone(messageID uuid.UUID)         { r.record(TTSDoneEvent{messageID}) }
func (r *Recorder) AudioUploadFailed()                  { r.record(AudioUploadFailedEvent{}) }
func (r *Recorder) InterviewerToken(token string)       { r.record(InterviewerTokenEvent{token}) }
func (r *Recorder) InterviewerDone(messageID uuid.UUID) { r.record(InterviewerDoneEvent{messageID}) }
func (r *Recorder) Error(e ClientError)                 { r.record(ClientErrorEvent{e}) }
func (r *Recorder) SessionLoaded(s SessionLoaded)       { r.record(SessionLoadedEvent{s}) }
func (r *Recorder) ReconnectState(rs ReconnectState)    { r.record(ReconnectStateEvent{rs}) }
func (r *Recorder) TTSChunk(c TTSChunk)                 { r.record(TTSChunkEvent{c}) }
```

- [ ] **Step 7: Run transport tests to confirm they pass**

```bash
cd /Users/btc/Projects/src/drill
go test ./internal/interview/transport/... -v 2>&1 | tail -30
```

Expected: All transport tests PASS. Build may still fail on conductor (StateChange calls) — that's fine, fix next.

- [ ] **Step 8: Commit**

```bash
cd /Users/btc/Projects/src/drill
git add internal/interview/transport/
git commit -m "refactor(transport): remove StateChange from Client interface, add State to ReconnectState"
```

---

### Task 2: token_writer.go — remove InterviewerDone from OnDone

**Files:**
- Modify: `internal/interview/transport/token_writer.go`

The conductor will call `InterviewerDone` directly after DB persist and state transition. `OnDone` no longer needs to do so.

- [ ] **Step 1: Empty OnDone in token_writer.go**

Replace the `OnDone` method body:

```go
func (w *TokenWriter) OnDone(_ string) {
	// InterviewerDone is sent by the conductor after DB persist and state
	// transition, not here. See streamInterviewerResponse in conductor.go.
}
```

- [ ] **Step 2: Build to confirm it compiles (conductor still has StateChange calls — expected)**

```bash
cd /Users/btc/Projects/src/drill
go build ./internal/interview/transport/... 2>&1
```

Expected: No errors in the transport package.

- [ ] **Step 3: Commit**

```bash
cd /Users/btc/Projects/src/drill
git add internal/interview/transport/token_writer.go
git commit -m "refactor(transport): remove InterviewerDone from TokenWriter.OnDone — conductor now sends it"
```

---

### Task 3: conductor.go — remove StateChange calls, reorder InterviewerDone, set reconnect state

**Files:**
- Modify: `internal/interview/conductor.go`

Five `StateChange` call sites to remove. One `InterviewerDone` call to add. Two `ReconnectState` calls to update.

- [ ] **Step 1: Remove StateChange from sendInitialMessage and set ReconnectState.State**

In `sendInitialMessage()`, find the two `ReconnectState` calls and update them to include `State: "waiting"`. Remove the trailing `StateChange` call.

Current code (around line 412-427):
```go
if c.isReconnect() {
    afterSeq := *c.initMsg.LastSeq
    c.client.ReconnectState(transport.ReconnectState{LastSeq: afterSeq, Messages: c.messages})
    return nil
}
c.client.SessionLoaded(...)
if len(c.messages) > 0 {
    c.client.ReconnectState(transport.ReconnectState{LastSeq: 0, Messages: c.messages})
    c.client.StateChange(string(StateWaitingForInput))
    return nil
}
```

Replace with:
```go
if c.isReconnect() {
    afterSeq := *c.initMsg.LastSeq
    c.client.ReconnectState(transport.ReconnectState{LastSeq: afterSeq, Messages: c.messages, State: "waiting"})
    return nil
}
c.client.SessionLoaded(transport.SessionLoaded{SessionID: c.sessionID, Question: c.question, DurationMin: int(c.duration.Minutes()), TTSEnabled: c.ttsEnabled})
if len(c.messages) > 0 {
    // Page refresh of existing session — send all messages, skip opening question.
    c.client.ReconnectState(transport.ReconnectState{LastSeq: 0, Messages: c.messages, State: "waiting"})
    return nil
}
// New session: opening question is dispatched by the caller as a
// pipeline goroutine so the event loop is responsive during streaming.
return nil
```

- [ ] **Step 2: Remove StateChange("transcribing") from endTurn**

In `endTurn()`, find the voice path block (around line 450-454):
```go
if err := c.sm.Transition(StateTranscribing); err != nil {
    c.client.Error(transport.ClientError{Code: "invalid_state_transition", Message: err.Error()})
    return nil
}
c.client.StateChange(string(StateTranscribing))  // DELETE THIS LINE
```

- [ ] **Step 3: Remove StateChange("processing_input") from endTurn**

In `endTurn()`, find the processing transition (around line 511-515):
```go
if err := c.sm.Transition(StateProcessingInput); err != nil {
    c.client.Error(transport.ClientError{Code: "invalid_state_transition", Message: err.Error()})
    return nil
}
c.client.StateChange(string(StateProcessingInput))  // DELETE THIS LINE
```

- [ ] **Step 4: Remove StateChange("interviewer_speaking") from streamInterviewerResponse**

In `streamInterviewerResponse()`, find the speaking transition (around line 538-542):
```go
if err := c.sm.Transition(StateInterviewerSpeaking); err != nil {
    c.client.Error(transport.ClientError{Code: "invalid_state_transition", Message: err.Error()})
    return nil
}
c.client.StateChange(string(StateInterviewerSpeaking))  // DELETE THIS LINE
```

- [ ] **Step 5: Replace StateChange("waiting_for_input") with InterviewerDone in streamInterviewerResponse**

At the end of `streamInterviewerResponse()`, find the waiting transition (around line 631-637):
```go
// Transition to WaitingForInput.
if err := c.sm.Transition(StateWaitingForInput); err != nil {
    return fmt.Errorf("transition to waiting: %w", err)
}
c.client.StateChange(string(StateWaitingForInput))  // REPLACE THIS

return nil
```

Replace with:
```go
// Transition to WaitingForInput and notify client. InterviewerDone is sent
// here (after persist + transition) making it the authoritative ready signal.
if err := c.sm.Transition(StateWaitingForInput); err != nil {
    return fmt.Errorf("transition to waiting: %w", err)
}
c.client.InterviewerDone(messageID)

return nil
```

- [ ] **Step 6: Build the entire project to confirm it compiles**

```bash
cd /Users/btc/Projects/src/drill
go build ./... 2>&1
```

Expected: No build errors. The `StateChange` method is gone from the interface, all call sites removed, all implementations updated.

- [ ] **Step 7: Run conductor unit tests**

```bash
cd /Users/btc/Projects/src/drill
go test ./internal/interview/... -v 2>&1 | tail -20
```

Expected: PASS (these are unit tests for state machine logic, not conductor integration).

- [ ] **Step 8: Commit**

```bash
cd /Users/btc/Projects/src/drill
git add internal/interview/conductor.go
git commit -m "refactor(conductor): remove StateChange calls, reorder InterviewerDone to after DB persist"
```

---

### Task 4: Integration tests — update state_change expectations

**Files:**
- Modify: `internal/handler/session_ws_test.go`

There are ~81 `state_change` references. They fall into clear patterns. Apply each pattern mechanically.

**Pattern reference (apply everywhere in the file):**

| Old | Replace with |
|-----|-------------|
| `drainUntilType(t, ws, "state_change") // waiting_for_input` | **Delete the line** — `drainUntilDone` already syncs to the ready point |
| `drainUntilType(t, ws, "state_change") // processing_input` | **Delete the line** — no server signal for this state |
| `drainUntilType(t, ws, "state_change") // interviewer_speaking` | **Delete the line** — if followed by `drainUntilDone`; see cancel tests below |
| `stateX := readMsg(t, ws)` + `assert.Equal(t, "state_change", ...)` | **Delete both lines** |
| `stateX, _ := drainUntilType(t, ws, "state_change")` + `assert.Equal(t, ...)` | **Delete both lines** |

- [ ] **Step 1: Fix TestWS_HappyPath_Text (around lines 310-348)**

Replace the entire opening sequence and turn sequence. Remove all `stateIS`, `stateWait`, `statePI`, `stateIS2`, `stateWait2` variables and assertions. Keep `readMsg` for `session_loaded`, `drainUntilDone` for streaming, and the end_session logic:

```go
loaded := readMsg(t, ws)
assert.Equal(t, "session_loaded", loaded["type"])

// Opening: first interviewer_token signals streaming; interviewer_done signals ready.
openingText, _ := drainUntilDone(t, ws)
assert.Equal(t, "Let's design a URL shortener.", openingText)

// Send candidate text turn.
sendMsg(t, ws, wsMsg{
    "type":         "end_turn",
    "content":      "I would start by defining the requirements.",
    "input_method": "text",
})

// Drain full response — interviewer_done is the ready signal.
responseText, _ := drainUntilDone(t, ws)
assert.Equal(t, "Let's design a URL shortener.", responseText)
```

- [ ] **Step 2: Fix the voice turn test (TestWS_VoiceTurn or similar, around lines 415-460)**

Remove `drainUntilType(t, ws, "state_change") // waiting_for_input` after `drainUntilDone`.
Remove the `stateTranscribing` readMsg + assert block (server no longer sends transcribing).
Remove `statePI`, `stateIS` readMsg + assert blocks.
Remove the trailing `drainUntilType(t, ws, "state_change") // waiting_for_input`.

The voice turn sequence becomes:
```go
drainUntilType(t, ws, "session_loaded")
drainUntilDone(t, ws) // drain opening; interviewer_done = ready

// Send voice turn.
// (send the voice message here)

// Transcription result arrives — no state_change from server.
transcription, _ := drainUntilType(t, ws, "transcription_result")
assert.Equal(t, "transcription_result", transcription["type"])
assert.Equal(t, "I would use a hash-based approach.", transcription["text"])

// Drain interviewer response.
drainUntilDone(t, ws)
```

- [ ] **Step 3: Fix TestWS_PageRefreshReconnect (around lines 1780-1815)**

The first connection setup removes all `drainUntilType(t, ws1, "state_change")` lines:
```go
ws1 := wsConnect(t, srv.URL, session.ID, cookie, nil)
drainUntilType(t, ws1, "session_loaded")
drainUntilDone(t, ws1)          // drain opening

sendMsg(t, ws1, wsMsg{"type": "end_turn", "content": "My answer", "input_method": "text"})
drainUntilDone(t, ws1)          // drain response

ws1.Close(websocket.StatusNormalClosure, "done")
```

For the page-refresh reconnect assertions, `reconnect_state` now includes `state`. Replace the trailing `stateMsg` block:
```go
// Page refresh: reconnect with last_seq=nil.
ws2 := wsConnect(t, srv.URL, session.ID, cookie, nil)
defer ws2.CloseNow()

loaded := readMsg(t, ws2)
assert.Equal(t, "session_loaded", loaded["type"])

reconnectMsg := readMsg(t, ws2)
assert.Equal(t, "reconnect_state", reconnectMsg["type"])
messages, ok := reconnectMsg["messages"].([]any)
assert.True(t, ok, "messages should be an array")
assert.Len(t, messages, 3, "should replay all messages")
assert.Equal(t, "waiting", reconnectMsg["state"], "reconnect_state.state should be 'waiting'")
// No separate state_change message follows.
```

- [ ] **Step 4: Fix TestWS_EndSessionDuringStreaming (around lines 1843-1849)**

The test reads a `state_change` then a token to confirm streaming started. Remove the `state_change` read; the first `interviewer_token` is sufficient:

```go
drainUntilType(t, ws, "session_loaded")
// Read a token to confirm streaming started.
m := readMsg(t, ws)
assert.Equal(t, "interviewer_token", m["type"])
```

- [ ] **Step 5: Fix the abrupt-close-during-streaming test (around lines 1903-1909)**

```go
drainUntilType(t, ws, "session_loaded")
drainUntilType(t, ws, "interviewer_token") // wait for streaming to start
// Close WS abruptly during streaming.
ws.CloseNow()
```

- [ ] **Step 6: Fix re-trigger reconnect test (around lines 2188-2201)**

Remove `retriggerIS` and `stateWait` drainUntilType blocks; `drainUntilDone` is the correct sync:

```go
reconnectMsg := readMsg(t, ws2)
assert.Equal(t, "reconnect_state", reconnectMsg["type"])

// Drain the re-triggered response — interviewer_done is the ready signal.
responseText, _ := drainUntilDone(t, ws2)
assert.Equal(t, "Let's discuss.", responseText)
```

- [ ] **Step 7: Fix TestWS_DisconnectWithPendingCancel and TestWS_CancelDuringPipeline_PipelineCompletes (around lines 2491 and 2536)**

These tests cancel during active streaming. They need a sync point confirming the pipeline has started. Replace `drainUntilType(t, ws, "state_change") // interviewer_speaking` with `drainUntilType(t, ws, "interviewer_token")`:

```go
// Wait for opening to start streaming.
drainUntilType(t, ws, "session_loaded")
drainUntilType(t, ws, "interviewer_token") // first token confirms streaming started
```

- [ ] **Step 8: Fix error-recovery drain loop (around lines 2371-2380)**

Remove the `state_change` branch from the drain loop — only `error` unblocks after a pipeline failure:

```go
// Drain until the pipeline error arrives.
for i := 0; i < 50; i++ {
    m := readMsgTimeout(t, ws, 5*time.Second)
    if m == nil {
        break
    }
    if m["type"] == "error" {
        break
    }
}
```

- [ ] **Step 9: Fix the STT retry test voice turn (around lines 2440-2455)**

Remove `drainUntilType(t, ws, "state_change") // waiting_for_input` (after `drainUntilDone`).
Remove the `stateTranscribing` readMsg and assert block entirely:

```go
drainUntilType(t, ws, "session_loaded")
drainUntilDone(t, ws) // drain opening

// Send voice turn.
// (existing send code unchanged)

// No state_change from server — transcribing is client-side only.
// Transcription result arrives directly.
transcription := readMsg(t, ws)
assert.Equal(t, "transcription_result", transcription["type"])
assert.Equal(t, "I would use a hash-based approach.", transcription["text"])
```

- [ ] **Step 10: Apply patterns to all remaining references**

Grep for any remaining `state_change` in the test file and apply the pattern table from the top of this task:

```bash
grep -n "state_change" /Users/btc/Projects/src/drill/internal/handler/session_ws_test.go
```

Expected: No remaining `state_change` references after applying all patterns. Fix any stragglers.

- [ ] **Step 11: Update comment at line 2068**

Find the loop comment `// turn accepted; got state_change or other non-error` and update:
```go
break // turn accepted; got non-error response
```

- [ ] **Step 12: Run integration tests**

```bash
cd /Users/btc/Projects/src/drill
go test ./internal/handler/... -v -timeout 120s 2>&1 | tail -50
```

Expected: All integration tests PASS.

- [ ] **Step 13: Commit**

```bash
cd /Users/btc/Projects/src/drill
git add internal/handler/session_ws_test.go
git commit -m "test(handler): remove state_change expectations, use interviewer_done as sync barrier"
```

---

### Task 5: Frontend — remove state_change from protocol, add state to reconnect_state

**Files:**
- Modify: `web/src/ws/protocol.ts`

- [ ] **Step 1: Update ServerMessage union type**

In `web/src/ws/protocol.ts`, remove `state_change` and update `reconnect_state`:

```typescript
// --- Server → Client ---

export type ServerMessage =
  | { type: "session_loaded"; session_id: string; question: { title: string; prompt: string }; duration: number; tts_enabled: boolean }
  | { type: "reconnect_state"; messages: ReconnectMessage[]; state: "waiting" }
  | { type: "interviewer_token"; token: string }
  | { type: "interviewer_done"; message_id: string }
  | { type: "tts_chunk"; data: string; message_id: string; seq: number }
  | { type: "tts_done"; message_id: string }
  | { type: "tts_error" }
  | { type: "audio_upload_failed" }
  | { type: "transcription_result"; text: string }
  | { type: "timer_warning"; minutes_remaining: number }
  | { type: "timer_overtime" }
  | { type: "ack"; action: "cancel_session" | "end_session" }
  | { type: "reconnect_please" }
  | { type: "error"; code: string; message: string }
  | { type: "pong" };
```

- [ ] **Step 2: Build to confirm TypeScript compiles**

```bash
cd /Users/btc/Projects/src/drill/web
npm run build 2>&1 | tail -20
```

Expected: Build errors on `hooks.ts` (still references `state_change` case — fix next task). TypeScript may report an error on the `state_change` case in the switch — that's expected.

- [ ] **Step 3: Commit**

```bash
cd /Users/btc/Projects/src/drill
git add web/src/ws/protocol.ts
git commit -m "refactor(frontend): remove state_change from ServerMessage, add state to reconnect_state"
```

---

### Task 6: Frontend — update handleMessage for derived state transitions

**Files:**
- Modify: `web/src/ws/hooks.ts`

- [ ] **Step 1: Update handleMessage in hooks.ts**

Replace the full `handleMessage` switch body. The `state_change` case is removed. The `reconnect_state`, `interviewer_token`, `interviewer_done`, `transcription_result`, and `error` cases gain `setState` calls.

The updated `handleMessage` function (replace from `switch (msg.type) {` to the closing `}` of the switch):

```typescript
  const handleMessage = useCallback((msg: ServerMessage) => {
    switch (msg.type) {
      case "session_loaded":
        setSessionInfo({
          duration: msg.duration,
          tts_enabled: msg.tts_enabled,
          question: msg.question,
        });
        // State will be set by the next message: either interviewer_token
        // (new session) or reconnect_state (page refresh of existing session).
        break;

      case "reconnect_state": {
        const msgs = msg.messages;
        setMessages(
          msgs.map((m) => ({
            id: m.id,
            seq: m.seq,
            role: m.role,
            content: m.content,
          })),
        );
        setState(msg.state);
        setWasReconnected(true);
        break;
      }

      case "interviewer_token":
        streamingTextRef.current += msg.token;
        setStreamingText(streamingTextRef.current);
        setState("streaming");
        break;

      case "interviewer_done": {
        const finalText = streamingTextRef.current;
        streamingTextRef.current = "";
        setStreamingText("");
        setMessages((prev) => {
          const seq = prev.length > 0 ? prev[prev.length - 1]!.seq + 1 : 1;
          return [...prev, { id: msg.message_id, seq, role: "interviewer", content: finalText }];
        });
        setState("waiting");
        // Close the turn span — full round-trip from send to response complete.
        if (turnSpanRef.current) {
          closeTurnSpan(turnSpanRef.current);
          turnSpanRef.current = null;
        }
        break;
      }

      case "transcription_result": {
        setState("processing");
        // Add the candidate's voice message to the chat using transcribed text.
        const text = msg.text;
        setMessages((prev) => {
          const displaySeq = prev.length > 0 ? prev[prev.length - 1]!.seq + 1 : 1;
          return [...prev, { id: `local-${displaySeq}`, seq: displaySeq, role: "candidate", content: text }];
        });
        break;
      }

      case "ack":
        if (msg.action === "cancel_session") {
          setState("cancelled");
        } else if (msg.action === "end_session") {
          setState("ended");
        }
        // Close any outstanding turn span on ack.
        if (turnSpanRef.current) {
          closeTurnSpan(turnSpanRef.current, false);
          turnSpanRef.current = null;
        }
        break;

      case "tts_error":
        toast.info("Audio temporarily unavailable");
        break;

      case "audio_upload_failed":
        toast.info("Audio recording could not be saved. Your response was captured as text.");
        break;

      case "error":
        console.error(`WS error: ${msg.code} — ${msg.message}`);
        setLastError(msg.message);
        setState("waiting");
        break;

      // timer_warning, timer_overtime: handled by useTimer
      // tts_chunk, tts_done: handled by audio player via onRawMessage
      default:
        break;
    }
  }, []); // No dependencies — uses refs for mutable state
```

- [ ] **Step 2: Build frontend to confirm it compiles cleanly**

```bash
cd /Users/btc/Projects/src/drill/web
npm run build 2>&1 | tail -20
```

Expected: Clean build, no TypeScript errors.

- [ ] **Step 3: Run frontend tests**

```bash
cd /Users/btc/Projects/src/drill/web
npm test 2>&1 | tail -20
```

Expected: All frontend tests PASS. (If any test references `state_change` messages, remove those test cases.)

- [ ] **Step 4: Commit**

```bash
cd /Users/btc/Projects/src/drill
git add web/src/ws/hooks.ts
git commit -m "refactor(frontend): derive InterviewState from data messages, remove state_change handler"
```

---

### Task 7: Full verification — integration suite and cross-package coverage

- [ ] **Step 1: Run full backend test suite**

```bash
cd /Users/btc/Projects/src/drill
go test ./... -timeout 180s 2>&1 | tail -30
```

Expected: All tests PASS. No `state_change` references in output.

- [ ] **Step 2: Run cross-package code coverage**

```bash
cd /Users/btc/Projects/src/drill
go test ./... -coverprofile=coverage.out -timeout 180s 2>&1 | tail -10
go tool cover -func=coverage.out | grep -E "transport|conductor|interview" | sort -k3 -rn | head -30
```

Expected: Coverage output shows transport and conductor packages covered. Review any uncovered lines in the changed files and confirm they are intentionally untested (e.g. error paths that are hard to trigger in integration tests).

- [ ] **Step 3: Run frontend test suite**

```bash
cd /Users/btc/Projects/src/drill/web
npm test 2>&1 | tail -20
```

Expected: All frontend tests PASS.

- [ ] **Step 4: Verify no state_change references remain in the codebase**

```bash
grep -r "state_change\|StateChange" /Users/btc/Projects/src/drill/internal /Users/btc/Projects/src/drill/web/src 2>/dev/null
```

Expected: No matches. If any appear, fix them before proceeding.

- [ ] **Step 5: Final commit if any cleanup was needed**

```bash
cd /Users/btc/Projects/src/drill
git add -p   # stage only intentional changes
git commit -m "chore: remove remaining state_change references"
```
