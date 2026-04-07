# Conductor Lifecycle Simplification Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Eliminate pipeline context cancellation, replace `session_ended` with immediate `ack`, remove server-side TTS interruption, and simplify the conductor main loop.

**Architecture:** The conductor never cancels pipeline context. Lifecycle messages (`cancel_session`, `end_session`) get an immediate `ack` and set a `pendingAction`. A unified post-select block processes pending actions when the pipeline finishes. The observer `Interrupt()` method is removed entirely. Frontend navigates on ack receipt.

**Tech Stack:** Go (conductor, observer, transport), TypeScript/React (ws hooks, interview page)

**Spec:** `docs/superpowers/specs/2026-04-07-conductor-lifecycle-simplification-design.md`

---

### Task 1: Remove `Interrupt()` from observer interface and implementations

**Files:**
- Modify: `internal/interview/observer/observer.go:16-21`
- Modify: `internal/interview/observer/fan_out.go:10,34-38`
- Modify: `internal/interview/observer/tts_accumulator.go:114-117`
- Modify: `internal/interview/observer/message_accumulator.go:17`
- Modify: `internal/interview/transport/token_writer.go:26`
- Modify: `internal/interview/observer/observer_test.go:43-55,198-210`

- [ ] **Step 1: Remove `Interrupt()` from `TokenObserver` interface**

In `internal/interview/observer/observer.go`, remove `Interrupt()` from the interface:

```go
// TokenObserver receives streaming LLM tokens.
type TokenObserver interface {
	OnToken(token string)
	OnDone(fullMessage string)
	OnError(err error)
}
```

- [ ] **Step 2: Remove `Interrupt()` and `Noop` from `TokenFanOut`**

In `internal/interview/observer/fan_out.go`, remove the `Noop` var and the `Interrupt()` method:

```go
// Delete: var Noop = &TokenFanOut{}

// Delete the entire Interrupt method:
// func (f *TokenFanOut) Interrupt() { ... }
```

The file should contain only `NewTokenFanOut`, `OnToken`, `OnDone`, `OnError`, and `Close`.

- [ ] **Step 3: Remove `Interrupt()` from `TTSAccumulator`**

In `internal/interview/observer/tts_accumulator.go`, delete the `Interrupt()` method (lines 114-117):

```go
// Delete:
// func (a *TTSAccumulator) Interrupt() {
//     a.cancel()
// }
```

`OnError()` still calls `a.cancel()` — that stays.

- [ ] **Step 4: Remove `Interrupt()` from `MessageAccumulator`**

In `internal/interview/observer/message_accumulator.go`, delete the no-op method. The line:

```go
func (a *MessageAccumulator) Interrupt()           {}
```

becomes simply absent.

- [ ] **Step 5: Remove `Interrupt()` from `TokenWriter`**

In `internal/interview/transport/token_writer.go`, delete:

```go
func (w *TokenWriter) Interrupt() {}
```

- [ ] **Step 6: Remove Interrupt tests**

In `internal/interview/observer/observer_test.go`:
- Delete `TestTokenFanOut_InterruptPropagates` (line 43)
- Delete `TestTTSAccumulator_Interrupt` (line 198)

- [ ] **Step 7: Run observer and transport tests**

Run: `go test ./internal/interview/observer/... ./internal/interview/transport/... -v -count=1`
Expected: All remaining tests pass. No compilation errors.

- [ ] **Step 8: Commit**

```bash
git add internal/interview/observer/ internal/interview/transport/token_writer.go
git commit -m "refactor(observer): remove Interrupt() from TokenObserver interface

Pipeline is never cancelled, so server-side TTS interruption is no longer
needed. TTS runs to completion; client handles playback interruption locally."
```

---

### Task 2: Add `Ack()` and remove `SessionEnded()` from transport Client

**Files:**
- Modify: `internal/interview/transport/client.go:18`
- Modify: `internal/interview/transport/ws_client.go:33-35`
- Modify: `internal/interview/transport/recorder.go:11,40`
- Modify: `internal/interview/transport/ws_client_test.go:146-152`

- [ ] **Step 1: Write test for `Ack()`**

In `internal/interview/transport/ws_client_test.go`, replace `TestWSClient_SessionEnded` with:

```go
func TestWSClient_Ack(t *testing.T) {
	ws, msg := setupWSClient(t)
	c := transport.NewWSClient(ws)
	c.Ack()
	got := <-msg
	assert.Equal(t, "ack", got["type"])
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/interview/transport/... -run TestWSClient_Ack -v`
Expected: Compilation error — `Ack()` not defined on Client.

- [ ] **Step 3: Update Client interface**

In `internal/interview/transport/client.go`, replace `SessionEnded(reason string)` with `Ack()`:

```go
type Client interface {
	StateChange(state string)
	Ack()
	TranscriptionResult(text string)
	// ... rest unchanged
}
```

- [ ] **Step 4: Update WSClient**

In `internal/interview/transport/ws_client.go`, replace `SessionEnded` with:

```go
func (c *WSClient) Ack() {
	c.send(map[string]string{"type": "ack"})
}
```

Delete the old `SessionEnded` method.

- [ ] **Step 5: Update Recorder**

In `internal/interview/transport/recorder.go`:

Replace `SessionEndedEvent` with `AckEvent`:
```go
AckEvent struct{}
```

Replace the `SessionEnded` method with:
```go
func (r *Recorder) Ack() { r.record(AckEvent{}) }
```

Delete `SessionEndedEvent` and `func (r *Recorder) SessionEnded(...)`.

- [ ] **Step 6: Run transport tests**

Run: `go test ./internal/interview/transport/... -v -count=1`
Expected: All pass.

- [ ] **Step 7: Commit**

```bash
git add internal/interview/transport/
git commit -m "refactor(transport): replace SessionEnded with Ack

Ack is sent immediately when the server receives a lifecycle message.
The client navigates on ack receipt, not on session completion."
```

---

### Task 3: Rewrite conductor main loop and lifecycle methods

This is the core change. The conductor main loop is rewritten with the new structure: `pendingAction` with post-select processing, `pipelineRunning` bool, no `cancelTurn()`, no observer tracking.

**Files:**
- Modify: `internal/interview/conductor.go`

- [ ] **Step 1: Remove `drainPipeline`, `obs` field, `Noop` reference, `cancelTurn` variable**

In `internal/interview/conductor.go`:

Delete `drainPipeline` function (lines 98-104).

Add `actionReconnect` to constants:
```go
const (
	actionEnd       = "end"
	actionCancel    = "cancel"
	actionReconnect = "reconnect"
)
```

In `NewConductor`, remove `c.obs.Store(observer.Noop)` (line 121).

Remove the `obs` field from the Conductor struct (lines 70-73):
```go
// Delete:
// obs atomic.Pointer[observer.TokenFanOut]
```

Remove the `sync/atomic` import if no longer used.

- [ ] **Step 2: Rewrite the readLoop — remove `cancel_tts` handling**

In the readLoop goroutine (inside `Run`), remove the `cancel_tts` block:

```go
// Delete:
// if msg.Type == "cancel_tts" {
//     c.obs.Load().Interrupt()
//     continue
// }
```

The readLoop now just reads, parses, and sends to `msgCh`.

- [ ] **Step 3: Rewrite the defer**

Replace the existing defer (lines 205-210) with:

```go
defer func() {
	if pipelineRunning {
		<-turnResultCh
	}
	c.rawWS.Close(websocket.StatusNormalClosure, "session ended")
	readCancel()
	wg.Wait()
	if err := c.lock.Release(); err != nil {
		slog.Error("conductor: release session lock", "error", err, "session_id", c.sessionID)
	}
}()
```

Also delete the existing `close()` method (lines 771-777) — its logic is now inline in the defer. Update the early-return paths (lock failure, session_init failure) to close the WS directly via `c.rawWS.Close(...)` and release the lock if acquired, since they return before the defer is installed.

- [ ] **Step 4: Rewrite pipeline state variables**

Replace the existing pipeline state block (lines 234-238) with:

```go
var (
	turnResultCh    <-chan turnResult
	pipelineRunning bool
	pendingAction   string // "", actionEnd, actionCancel, or actionReconnect
)
```

- [ ] **Step 5: Rewrite opening question and recovery dispatches**

Replace the opening question dispatch (lines 241-249) with:

```go
if !c.isReconnect() && len(c.messages) == 0 {
	ch := make(chan turnResult, 1)
	turnResultCh = ch
	pipelineRunning = true
	go func() {
		ch <- turnResult{err: c.streamInterviewerResponse(workCtx)}
	}()
}
```

Replace the recovery dispatch (lines 253-261) with the same pattern (using `workCtx`, no `cancelTurn`):

```go
if c.needsInterviewerRecovery() {
	ch := make(chan turnResult, 1)
	turnResultCh = ch
	pipelineRunning = true
	go func() {
		ch <- turnResult{err: c.streamInterviewerResponse(workCtx)}
	}()
}
```

- [ ] **Step 6: Rewrite the main loop**

Replace the entire `for { select { ... } }` block (lines 264-394) with:

```go
for {
	shouldExit := false

	select {
	case msg, ok := <-msgCh:
		if !ok {
			shouldExit = true
			break
		}
		switch msg.Type {
		case "end_turn":
			if pipelineRunning {
				c.client.Error(transport.ClientError{Code: "turn_in_progress", Message: "a turn is already being processed"})
				continue
			}
			ch := make(chan turnResult, 1)
			turnResultCh = ch
			pipelineRunning = true
			capturedMsg := msg
			go func() {
				ch <- turnResult{err: c.endTurn(workCtx, capturedMsg)}
			}()

		case "end_session":
			c.client.Ack()
			pendingAction = actionEnd

		case "cancel_session":
			c.client.Ack()
			pendingAction = actionCancel

		case "ping":
			c.client.Pong()
		default:
			c.client.Error(transport.ClientError{Code: "unknown_message_type", Message: "unknown message type: " + msg.Type})
		}

	case res := <-turnResultCh:
		pipelineRunning = false
		if res.err != nil {
			slog.Error("conductor: pipeline error", "error", res.err, "session_id", c.sessionID)
			c.sm.ForceState(StateWaitingForInput)
			if pendingAction == "" {
				c.client.Error(transport.ClientError{Code: "turn_failed", Message: "failed to process turn, please try again"})
			}
		}

	case <-warningTimer:
		c.client.TimerWarning(warningMinutes(c.duration))

	case <-overtimeTimer:
		c.client.TimerOvertime()

	case <-autoEndTimer:
		slog.Info("conductor: auto-ending session", "session_id", c.sessionID)
		pendingAction = actionEnd

	case <-reconnectTimer:
		if pendingAction == "" {
			pendingAction = actionReconnect
		}

	case <-serverCtx.Done():
		shouldExit = true
	}

	// Wait for pipeline if exiting.
	if shouldExit && pipelineRunning {
		<-turnResultCh
		pipelineRunning = false
	}

	// Process pending action when pipeline is done.
	if !pipelineRunning && pendingAction != "" {
		action := pendingAction
		pendingAction = ""
		switch action {
		case actionCancel:
			if err := c.cancelSession(workCtx); err != nil {
				slog.Error("conductor: cancel_session", "error", err, "session_id", c.sessionID)
			}
		case actionEnd:
			if err := c.endSession(workCtx); err != nil {
				slog.Error("conductor: end_session", "error", err, "session_id", c.sessionID)
			}
		case actionReconnect:
			c.client.ReconnectPlease()
		}
		return
	}

	if shouldExit {
		return
	}
}
```

- [ ] **Step 7: Simplify `endSession` and `cancelSession`**

Remove observer interrupts and `SessionEnded` calls from both methods.

`endSession` becomes:
```go
func (c *Conductor) endSession(ctx context.Context) (err error) {
	ctx, span := tracer.Start(ctx, "Conductor.endSession")
	defer func() { drilotel.End(span, err) }()

	if err := c.sm.Transition(StateEnding); err != nil {
		return fmt.Errorf("transition to ending: %w", err)
	}

	if err := c.backend.CompleteSession(ctx, c.sessionID, c.sm.TurnCount()); err != nil {
		return fmt.Errorf("complete session: %w", err)
	}

	if err := c.sm.Transition(StateEnded); err != nil {
		return fmt.Errorf("transition to ended: %w", err)
	}

	return nil
}
```

`cancelSession` becomes:
```go
func (c *Conductor) cancelSession(ctx context.Context) (err error) {
	ctx, span := tracer.Start(ctx, "Conductor.cancelSession")
	defer func() { drilotel.End(span, err) }()

	if err := c.sm.Transition(StateEnding); err != nil {
		return fmt.Errorf("transition to ending: %w", err)
	}

	if err := c.backend.CancelSession(ctx, c.sessionID, c.sm.TurnCount()); err != nil {
		return fmt.Errorf("cancel session: %w", err)
	}

	if err := c.sm.Transition(StateEnded); err != nil {
		return fmt.Errorf("transition to ended: %w", err)
	}

	return nil
}
```

Note: the state machine error path no longer sends an error to the client — these are internal methods called after the client has already received the ack and navigated away. Return the error to the caller for logging.

- [ ] **Step 8: Remove `obs` references from `streamInterviewerResponse`**

In `streamInterviewerResponse`:

Delete `c.obs.Load().Interrupt()` (line 572).

Delete `c.obs.Store(fanOut)` (line 616).

Replace the error path after `StreamLLM` fails (lines 628-631):
```go
if err != nil {
	fanOut.OnError(err)
	return fmt.Errorf("start llm stream: %w", err)
}
```

Replace the background cleanup (lines 651-655):
```go
go func() {
	fanOut.Close()
}()
```

Delete `fanOut.Interrupt()` in the persist error path (line 670) — just remove the single line, keep the rest of the error handling.

Delete `c.obs.Store(observer.Noop)` wherever it appears (lines 630, 654).

- [ ] **Step 9: Clean up imports**

Remove unused imports from `conductor.go`:
- `"sync/atomic"` — `obs` field removed
- The `observer` import may no longer be needed if only `observer.NewMessageAccumulator`, `observer.NewTokenFanOut`, `observer.NewTTSAccumulator`, `observer.TTSAccumulatorParams` are used — check and keep if still referenced.

- [ ] **Step 10: Verify compilation**

Run: `go build ./...`
Expected: Clean compilation.

- [ ] **Step 11: Commit**

```bash
git add internal/interview/conductor.go
git commit -m "refactor(conductor): rewrite main loop with unified pendingAction

- Pipeline context is never cancelled
- Immediate ack on cancel_session/end_session
- Post-select pendingAction processing replaces per-case handling
- pipelineRunning bool replaces nil channel checks
- Reconnect consolidated into pendingAction
- Defer closes WS before cancelling read context (fixes AfterFunc race)
- Remove obs field, cancelTurn, drainPipeline, reconnectPending"
```

---

### Task 4: Update integration tests

**Files:**
- Modify: `internal/handler/session_ws_test.go`

- [ ] **Step 1: Update `TestWS_CancelSession`**

The test (line 1753) currently expects `session_ended` with reason `"cancelled"`. Change it to expect `ack`:

Find the assertion:
```go
ended, _ := drainUntilType(t, ws, "session_ended")
assert.Equal(t, "cancelled", ended["reason"], ...)
```

Replace with:
```go
ack, _ := drainUntilType(t, ws, "ack")
assert.Equal(t, "ack", ack["type"])
```

Keep the DB verification assertions unchanged.

- [ ] **Step 2: Update `TestWS_CancelSessionDuringTTS`**

The test (line 2196) expects `session_ended` with reason `"cancelled"` during TTS streaming. Change the assertion loop to look for `ack` instead of `session_ended`:

```go
var gotAck bool
for i := 0; i < 50; i++ {
	m := readMsgTimeout(t, ws, 10*time.Second)
	if m == nil {
		break
	}
	if m["type"] == "ack" {
		gotAck = true
		break
	}
}
assert.True(t, gotAck, "should receive ack for cancel_session")
```

Keep DB verification. Add a wait for the conductor to finish before checking DB state (the pipeline now runs to completion). Use a poll loop on the session status:

```go
assert.Eventually(t, func() bool {
	s, err := db.New(pool).GetSessionByID(ctx, session.ID)
	return err == nil && s.Status == "cancelled"
}, 10*time.Second, 100*time.Millisecond, "session should be cancelled after pipeline completes")
```

- [ ] **Step 3: Update `TestWS_AutoEndDuringPipeline`**

The test (line 2257) expects `session_ended`. Auto-end no longer sends `session_ended` — it's a server-side action. The test should verify DB state instead:

Remove the `session_ended` assertion loop. Replace with polling for completion:

```go
assert.Eventually(t, func() bool {
	s, err := db.New(pool).GetSession(ctx, session.ID)
	return err == nil && s.Status == "completed"
}, 15*time.Second, 100*time.Millisecond, "auto-end should complete the session")
```

- [ ] **Step 4: Update `TestWS_PipelineErrorWithPendingEnd`**

The test (line 2310) uses an Anthropic server that errors mid-stream. The opening pipeline fails, conductor sends `turn_failed`, then the test sends `end_session` and expects `session_ended`.

Replace the `session_ended` assertion loop (lines 2368-2379) with an `ack` check:

```go
// Now send end_session — should succeed regardless of pipeline state.
sendMsg(t, ws, wsMsg{"type": "end_session"})

var gotAck bool
for i := 0; i < 20; i++ {
	m := readMsgTimeout(t, ws, 5*time.Second)
	if m == nil {
		break
	}
	if m["type"] == "ack" {
		gotAck = true
		break
	}
}
assert.True(t, gotAck, "end_session should receive ack after pipeline error")

// Verify DB state.
ctx := context.Background()
assert.Eventually(t, func() bool {
	s, err := db.New(pool).GetSession(ctx, session.ID)
	return err == nil && s.Status == "completed"
}, 10*time.Second, 100*time.Millisecond, "session should be completed")
```

- [ ] **Step 5: Update `TestWS_CancelTTS`**

The test (line 475) sends `cancel_tts` and verifies server-side TTS interruption. Since `cancel_tts` is removed from the protocol, this test should be deleted entirely.

- [ ] **Step 6: Run all WS integration tests**

Run: `go test ./internal/handler/ -run TestWS -v -count=1 -timeout 120s`
Expected: All pass.

- [ ] **Step 7: Run full test suite**

Run: `go test ./... -count=1 -timeout 120s`
Expected: All pass.

- [ ] **Step 8: Commit**

```bash
git add internal/handler/session_ws_test.go
git commit -m "test: update integration tests for ack protocol and removed cancel_tts"
```

---

### Task 5: Frontend protocol and hook changes

**Files:**
- Modify: `web/src/ws/protocol.ts`
- Modify: `web/src/ws/hooks.ts`

- [ ] **Step 1: Update protocol types**

In `web/src/ws/protocol.ts`:

Remove from `ClientMessage`:
```typescript
| { type: "cancel_tts" }
```

Remove from `ServerMessage`:
```typescript
| { type: "session_ended"; reason: "candidate" | "interviewer" | "timeout" | "cancelled" }
```

Add to `ServerMessage`:
```typescript
| { type: "ack" }
```

- [ ] **Step 2: Update `useInterview` hook**

In `web/src/ws/hooks.ts`:

Add a ref to track the pending action:
```typescript
const pendingActionRef = useRef<"cancel" | "end" | null>(null);
```

In `handleMessage`, remove the `session_ended` case and add `ack`:
```typescript
case "ack":
  if (pendingActionRef.current === "cancel") {
    setState("cancelled");
  } else if (pendingActionRef.current === "end") {
    setState("ended");
  }
  pendingActionRef.current = null;
  // Close any outstanding turn span on ack.
  if (turnSpanRef.current) {
    closeTurnSpan(turnSpanRef.current, false);
    turnSpanRef.current = null;
  }
  break;
```

Delete the `session_ended` case entirely.

Update `endSession` to set the ref before sending:
```typescript
const endSession = useCallback(() => {
  pendingActionRef.current = "end";
  cmRef.current?.send({ type: "end_session" });
}, []);
```

Update `cancelSession` similarly:
```typescript
const cancelSession = useCallback(() => {
  pendingActionRef.current = "cancel";
  cmRef.current?.send({ type: "cancel_session" });
}, []);
```

Delete the `cancelTts` callback entirely:
```typescript
// Delete:
// const cancelTts = useCallback(() => {
//   cmRef.current?.send({ type: "cancel_tts" });
// }, []);
```

Remove `cancelTts` from the return object.

- [ ] **Step 3: Verify TypeScript compilation**

Run: `cd web && npx tsc --noEmit`
Expected: Type errors in `interview.tsx` (references to removed `cancelTts`) — these are fixed in Task 6.

- [ ] **Step 4: Commit**

```bash
git add web/src/ws/protocol.ts web/src/ws/hooks.ts
git commit -m "refactor(frontend): replace session_ended with ack protocol

- Add ack handler with pendingActionRef tracking
- Remove cancel_tts client message
- Remove session_ended server message
- Remove cancelTts callback from useInterview"
```

---

### Task 6: Frontend interview page changes

**Files:**
- Modify: `web/src/pages/interview.tsx`

- [ ] **Step 1: Remove `cancelTts` usage**

In `web/src/pages/interview.tsx`:

Remove `cancelTts` from the destructured hooks (line 254):
```typescript
const {
  messages,
  streamingText,
  state,
  connectionState,
  sessionInfo,
  lastError,
  sendText,
  sendAudio,
  endSession,
  cancelSession,
  setRawMessageHandler,
  cmRef,
} = useInterview(sessionId);
```

Replace the `stopTts` callback (lines 332-335) — it previously called `cancelTts()` + `audioPlayer.cancel()`. Now it's just client-side:
```typescript
const stopTts = useCallback(() => {
  audioPlayer.cancel();
}, [audioPlayer]);
```

- [ ] **Step 2: Verify TypeScript compilation**

Run: `cd web && npx tsc --noEmit`
Expected: Clean compilation.

- [ ] **Step 3: Verify frontend builds**

Run: `cd web && npm run build`
Expected: Clean build.

- [ ] **Step 4: Commit**

```bash
git add web/src/pages/interview.tsx
git commit -m "refactor(frontend): remove cancelTts, use client-side audio stop only"
```

---

### Task 7: New integration tests for the simplified lifecycle

**Files:**
- Modify: `internal/handler/session_ws_test.go`

- [ ] **Step 1: Add test for disconnect with pending cancel**

```go
func TestWS_DisconnectWithPendingCancel(t *testing.T) {
	t.Parallel()

	tokens := []string{"Let's ", "discuss ", "the ", "requirements. ", "First, ", "we ", "need."}
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

	// Wait for opening to start streaming.
	drainUntilType(t, ws, "session_loaded")
	drainUntilType(t, ws, "state_change") // interviewer_speaking

	// Send cancel while pipeline is running, then disconnect immediately.
	sendMsg(t, ws, wsMsg{"type": "cancel_session"})
	// Read ack before closing.
	ack, _ := drainUntilType(t, ws, "ack")
	assert.Equal(t, "ack", ack["type"])

	ws.Close(websocket.StatusNormalClosure, "leaving")

	// Conductor should wait for pipeline then cancel.
	ctx := context.Background()
	assert.Eventually(t, func() bool {
		s, err := db.New(pool).GetSessionByID(ctx, session.ID)
		return err == nil && s.Status == "cancelled"
	}, 15*time.Second, 100*time.Millisecond, "session should be cancelled after disconnect")
}
```

- [ ] **Step 2: Add test for cancel during pipeline with no context cancellation**

```go
func TestWS_CancelDuringPipeline_PipelineCompletes(t *testing.T) {
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

	drainUntilType(t, ws, "session_loaded")
	drainUntilType(t, ws, "state_change") // interviewer_speaking

	// Cancel during the opening question pipeline.
	sendMsg(t, ws, wsMsg{"type": "cancel_session"})

	// Should get ack immediately.
	ack, _ := drainUntilType(t, ws, "ack")
	assert.Equal(t, "ack", ack["type"])

	// Pipeline should still complete — verify the interviewer message was persisted.
	ctx := context.Background()
	assert.Eventually(t, func() bool {
		msgs, err := db.New(pool).ListMessagesBySession(ctx, session.ID)
		if err != nil {
			return false
		}
		// Opening question should be persisted despite cancel.
		return len(msgs) >= 1 && msgs[0].Role == "interviewer"
	}, 15*time.Second, 100*time.Millisecond, "interviewer message should be persisted even after cancel")

	// Session should be cancelled.
	assert.Eventually(t, func() bool {
		s, err := db.New(pool).GetSessionByID(ctx, session.ID)
		return err == nil && s.Status == "cancelled"
	}, 15*time.Second, 100*time.Millisecond, "session should be cancelled")
}
```

- [ ] **Step 3: Run new tests**

Run: `go test ./internal/handler/ -run "TestWS_Disconnect|TestWS_CancelDuring" -v -count=1 -timeout 120s`
Expected: Both pass.

- [ ] **Step 4: Run full test suite**

Run: `go test ./... -count=1 -timeout 120s`
Expected: All pass.

- [ ] **Step 5: Commit**

```bash
git add internal/handler/session_ws_test.go
git commit -m "test: add disconnect-with-pending-cancel and pipeline-completes-on-cancel tests"
```
