# Conductor Async Pipeline Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the conductor's event loop non-blocking by dispatching the pipeline (STT → LLM → TTS → persist) in a goroutine with ownership transfer via a completion channel.

**Architecture:** The select loop dispatches `endTurn` and the opening question in goroutines. While a pipeline runs, end/cancel/timer requests are deferred as a `pendingAction` and the pipeline context is cancelled. The pipeline result arrives via `<-chan turnResult`, at which point the event loop reclaims shared state ownership and executes any pending action. TTS is fire-and-forget — no `fanOut.Close()` on the happy path.

**Tech Stack:** Go 1.25, coder/websocket, pgx, context

**Spec:** `docs/superpowers/specs/2026-04-06-conductor-async-pipeline-design.md`

---

## File Map

| Action | File | Responsibility |
|--------|------|----------------|
| Modify | `internal/interview/conductor.go` | All changes: turnResult type, drainPipeline helper, async dispatch, fire-and-forget TTS, Interrupt on all exit paths, context.WithoutCancel for persist, sendInitialMessage refactor |
| Modify | `internal/handler/session_ws_test.go` | New tests for mid-pipeline cancel, disconnect, concurrent turn rejection |

---

### Task 1: Fire-and-forget TTS + Interrupt on exit paths

Decouple TTS from the critical path. This is a prerequisite for the async pipeline — once the pipeline runs in a goroutine, we can't have it blocking on `fanOut.Close()`.

**Files:**
- Modify: `internal/interview/conductor.go`

- [ ] **Step 1: Add Interrupt at top of `streamInterviewerResponse`**

In `conductor.go`, add this line at the very beginning of `streamInterviewerResponse`, before the state transition (before `if err := c.sm.Transition(StateInterviewerSpeaking)`):

```go
// Cancel any lingering TTS from the previous turn.
c.obs.Load().Interrupt()
```

- [ ] **Step 2: Add Interrupt at top of `endSession` and `cancelSession`**

In `endSession`, add as the first line of the function body (after the span setup):

```go
c.obs.Load().Interrupt()
```

In `cancelSession`, add the same line at the same position:

```go
c.obs.Load().Interrupt()
```

- [ ] **Step 3: Add Interrupt in Run() defer**

Replace the existing defer block:

```go
defer func() {
    readCancel()
    wg.Wait()
    c.close()
}()
```

With:

```go
defer func() {
    c.obs.Load().Interrupt()
    readCancel()
    wg.Wait()
    c.close()
}()
```

- [ ] **Step 4: Remove `fanOut.Close()` and `c.obs.Store(observer.Noop)` on happy path**

In `streamInterviewerResponse`, remove these two lines from the happy path (after persist succeeds):

Remove line 529: `c.obs.Store(observer.Noop)`

Remove line 548: `fanOut.Close()`

Keep `c.obs.Store(fanOut)` at line 495 — the fan-out stays in `c.obs` so `cancel_tts` still reaches TTS.

- [ ] **Step 5: Change error-path `fanOut.Close()` to `fanOut.Interrupt()`**

In `streamInterviewerResponse`, there are two error paths with `fanOut.Close()`:

Line 508 (StreamLLM failure): Change `fanOut.Close()` to `fanOut.Interrupt()`

Line 543 (persist failure): Change `fanOut.Close()` to `fanOut.Interrupt()`

- [ ] **Step 6: Use context.WithoutCancel for persist**

In `streamInterviewerResponse`, change the persist call from:

```go
c.sequence++
interviewerMsg, err := c.backend.PersistInterviewerTurn(ctx, stream, backend.PersistMessageParams{
```

To:

```go
persistCtx := context.WithoutCancel(ctx)
c.sequence++
interviewerMsg, err := c.backend.PersistInterviewerTurn(persistCtx, stream, backend.PersistMessageParams{
```

- [ ] **Step 7: Run existing tests**

Run: `go test ./internal/handler/ -run TestWS -v -count=1 -timeout 120s`
Expected: ALL PASS — behavior is unchanged, we just stopped waiting for TTS and added Interrupt calls.

Run: `go test ./internal/interview/... -v -count=1`
Expected: ALL PASS

- [ ] **Step 8: Commit**

```bash
git add internal/interview/conductor.go
git commit -m "refactor(conductor): fire-and-forget TTS, Interrupt on all exit paths

TTS is a supplementary projection — the conductor no longer waits
for it via fanOut.Close(). The TTS goroutine self-terminates after
OnDone closes its sentence channel. Interrupt() is called on:
next turn start, session end/cancel, disconnect, and shutdown.

fanOut.Close() on error paths becomes fanOut.Interrupt() for
immediate cleanup. Persist uses context.WithoutCancel so the
interviewer message survives turn cancellation."
```

---

### Task 2: Async pipeline dispatch with ownership transfer

The core change. Move `endTurn` into a goroutine. The event loop defers conflicting operations until the pipeline completes.

**Files:**
- Modify: `internal/interview/conductor.go`

- [ ] **Step 1: Add turnResult type and drainPipeline helper**

Add after the `ttsSink` methods (after `HandleTTSError`), before `NewConductor`:

```go
// turnResult carries the outcome of a pipeline run.
type turnResult struct {
	err error
}

// drainPipeline waits for the pipeline goroutine to finish, with a timeout
// to prevent shutdown hangs if the pipeline ignores context cancellation.
func drainPipeline(ch <-chan turnResult) {
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		slog.Error("conductor: pipeline did not exit after cancel")
	}
}
```

- [ ] **Step 2: Add pending action constants**

Add after the `turnResult` type:

```go
// Pending action constants for deferred lifecycle operations.
const (
	actionEnd    = "end"
	actionCancel = "cancel"
)
```

- [ ] **Step 3: Modify `sendInitialMessage` to not call `streamInterviewerResponse` directly**

Replace the current `sendInitialMessage` function. The key change: for new sessions with no messages, it no longer calls `streamInterviewerResponse` — the caller dispatches it as a pipeline goroutine.

Current code:

```go
func (c *Conductor) sendInitialMessage(ctx context.Context) (err error) {
	ctx, span := tracer.Start(ctx, "Conductor.sendInitialMessage")
	defer func() { drilotel.End(span, err) }()

	if c.isReconnect() {
		afterSeq := *c.initMsg.LastSeq // isReconnect already verified non-nil
		c.send(ctx, msgReconnectState(afterSeq, c.messages))
		return nil
	}
	c.send(ctx, msgSessionLoaded(c.sessionID, c.question, int(c.duration.Minutes()), c.ttsEnabled))
	if len(c.messages) > 0 {
		// Page refresh of existing session — send all messages, skip opening question.
		c.send(ctx, msgReconnectState(0, c.messages))
		c.send(ctx, msgStateChange(StateWaitingForInput))
		return nil
	}
	return c.streamInterviewerResponse(ctx)
}
```

Replace with:

```go
func (c *Conductor) sendInitialMessage(ctx context.Context) (err error) {
	ctx, span := tracer.Start(ctx, "Conductor.sendInitialMessage")
	defer func() { drilotel.End(span, err) }()

	if c.isReconnect() {
		afterSeq := *c.initMsg.LastSeq // isReconnect already verified non-nil
		c.send(ctx, msgReconnectState(afterSeq, c.messages))
		return nil
	}
	c.send(ctx, msgSessionLoaded(c.sessionID, c.question, int(c.duration.Minutes()), c.ttsEnabled))
	if len(c.messages) > 0 {
		// Page refresh of existing session — send all messages, skip opening question.
		c.send(ctx, msgReconnectState(0, c.messages))
		c.send(ctx, msgStateChange(StateWaitingForInput))
		return nil
	}
	// New session: opening question is dispatched by the caller as a
	// pipeline goroutine so the event loop is responsive during streaming.
	return nil
}
```

- [ ] **Step 4: Rewrite the event loop in `Run()`**

Replace everything from `// Initial messages to client.` through the end of the `for/select` loop (lines 226-292) with the following. Keep everything above (lock, session_init, readLoop, defer, loadSession, timers) unchanged.

```go
	// Initial messages to client.
	if err := c.sendInitialMessage(workCtx); err != nil {
		slog.Error("conductor: initial message", "error", err, "session_id", c.sessionID)
		return
	}

	// Pipeline state — at most one pipeline goroutine runs at a time.
	var (
		turnResultCh  <-chan turnResult  // nil when no pipeline running
		cancelTurn    context.CancelFunc // non-nil when pipeline running
		pendingAction string             // "", actionEnd, or actionCancel
	)

	// Dispatch opening question as a pipeline goroutine for new sessions.
	if !c.isReconnect() && len(c.messages) == 0 {
		ch := make(chan turnResult, 1)
		turnResultCh = ch
		turnCtx, cancel := context.WithCancel(workCtx)
		cancelTurn = cancel
		go func() {
			ch <- turnResult{err: c.streamInterviewerResponse(turnCtx)}
		}()
	}

	// Main loop -- event loop is never blocked by pipeline I/O.
	reconnectPending := false
	for {
		select {
		case msg, ok := <-msgCh:
			if !ok {
				// Client disconnected.
				if cancelTurn != nil {
					cancelTurn()
					drainPipeline(turnResultCh)
				}
				if pendingAction == actionEnd {
					_ = c.endSession(workCtx)
				}
				return
			}
			if serverCtx.Err() != nil {
				c.send(workCtx, msgReconnectPlease)
				if cancelTurn != nil {
					cancelTurn()
					drainPipeline(turnResultCh)
				}
				return
			}
			switch msg.Type {
			case "end_turn":
				if turnResultCh != nil {
					c.send(workCtx, msgError("turn_in_progress", "a turn is already being processed"))
					continue
				}
				ch := make(chan turnResult, 1)
				turnResultCh = ch
				turnCtx, cancel := context.WithCancel(workCtx)
				cancelTurn = cancel
				capturedMsg := msg
				go func() {
					ch <- turnResult{err: c.endTurn(turnCtx, capturedMsg)}
				}()

			case "end_session":
				if turnResultCh != nil {
					pendingAction = actionEnd
					cancelTurn()
					continue
				}
				if err := c.endSession(workCtx); err != nil {
					slog.Error("conductor: end_session", "error", err, "session_id", c.sessionID)
				}
				return

			case "cancel_session":
				if turnResultCh != nil {
					pendingAction = actionCancel
					cancelTurn()
					continue
				}
				if err := c.cancelSession(serverCtx); err != nil {
					slog.Error("conductor: cancel_session", "error", err, "session_id", c.sessionID)
				}
				return

			case "ping":
				c.send(workCtx, msgPong)
			default:
				c.send(workCtx, msgError("unknown_message_type", "unknown message type: "+msg.Type))
			}

		case res := <-turnResultCh:
			// Pipeline completed. Reclaim ownership of shared state.
			turnResultCh = nil
			cancelTurn = nil

			if res.err != nil {
				slog.Error("conductor: end_turn", "error", res.err, "session_id", c.sessionID)
				c.sm.ForceState(StateWaitingForInput)
				c.send(workCtx, msgError("turn_failed", "failed to process turn, please try again"))
			}

			// Handle deferred lifecycle action.
			switch pendingAction {
			case actionEnd:
				pendingAction = ""
				if err := c.endSession(workCtx); err != nil {
					slog.Error("conductor: end_session", "error", err, "session_id", c.sessionID)
				}
				return
			case actionCancel:
				pendingAction = ""
				if err := c.cancelSession(serverCtx); err != nil {
					slog.Error("conductor: cancel_session", "error", err, "session_id", c.sessionID)
				}
				return
			}

			if reconnectPending {
				c.send(workCtx, msgReconnectPlease)
				return
			}

		case <-warningTimer:
			c.send(workCtx, msgTimerWarning(warningMinutes(c.duration)))

		case <-overtimeTimer:
			c.send(workCtx, msgTimerOvertime)

		case <-autoEndTimer:
			slog.Info("conductor: auto-ending session", "session_id", c.sessionID)
			if turnResultCh != nil {
				pendingAction = actionEnd
				cancelTurn()
				continue
			}
			if err := c.endSession(workCtx); err != nil {
				slog.Error("conductor: auto-end", "error", err, "session_id", c.sessionID)
			}
			return

		case <-reconnectTimer:
			reconnectPending = true

		case <-serverCtx.Done():
			c.send(workCtx, msgReconnectPlease)
			if cancelTurn != nil {
				cancelTurn()
				drainPipeline(turnResultCh)
			}
			if pendingAction == actionEnd {
				_ = c.endSession(workCtx)
			}
			return
		}
	}
```

- [ ] **Step 5: Run existing tests**

Run: `go test ./internal/handler/ -run TestWS -v -count=1 -timeout 120s`
Expected: ALL PASS

Run: `go test ./internal/interview/... -v -count=1`
Expected: ALL PASS

Run: `go test ./internal/handler/ -run TestWS -race -count=1 -timeout 120s`
Expected: No races

- [ ] **Step 6: Commit**

```bash
git add internal/interview/conductor.go
git commit -m "feat(conductor): async pipeline with ownership transfer

Move endTurn and opening question into goroutines. The event loop
stays responsive to disconnect, end/cancel session, timers, and
shutdown during pipeline execution.

Shared state (sm, messages, sequence) ownership transfers via
<-chan turnResult. The event loop defers conflicting operations
until the pipeline completes. No mutex needed — channel
send/receive provides the happens-before guarantee."
```

---

### Task 3: New tests for async pipeline behavior

Add tests that verify the async pipeline's correctness — mid-pipeline cancel, disconnect, and concurrent turn rejection.

**Files:**
- Modify: `internal/handler/session_ws_test.go`

- [ ] **Step 1: Add a slow Anthropic server helper**

Add after `newFakeAnthropicErrorServer` (around line 1357):

```go
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
```

- [ ] **Step 2: Add test for end session during LLM streaming**

```go
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

	// Verify session is completed in DB.
	ctx := context.Background()
	s, err := db.New(pool).GetSession(ctx, session.ID)
	require.NoError(t, err)
	assert.Equal(t, "completed", s.Status)
}
```

- [ ] **Step 3: Add test for disconnect during pipeline**

```go
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
```

- [ ] **Step 4: Add test for concurrent turn rejection**

```go
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
```

- [ ] **Step 5: Run all tests**

Run: `go test ./internal/handler/ -run TestWS -v -count=1 -timeout 120s`
Expected: ALL PASS including new tests

Run: `go test ./internal/handler/ -run TestWS -race -count=1 -timeout 120s`
Expected: No races

- [ ] **Step 6: Commit**

```bash
git add internal/handler/session_ws_test.go
git commit -m "test(conductor): add async pipeline tests — mid-stream cancel, disconnect, concurrent turn"
```

---

### Task 4: Full test suite + manual verification

- [ ] **Step 1: Run full test suite with race detector**

Run: `go test ./... -race -count=1 -timeout 300s`
Expected: ALL PASS, no races

- [ ] **Step 2: Run frontend type check**

Run: `cd web && npx tsc --noEmit`
Expected: no type errors (no frontend changes in this spec, but verify nothing broke)

- [ ] **Step 3: Manual verification**

Start the app. Open an interview with TTS enabled.

1. **Normal turn:** Send a voice or text message, confirm response streams and TTS plays.
2. **End session mid-stream:** During an interviewer response, click End Session. Confirm audio stops and session ends promptly (not after full response).
3. **Disconnect recovery:** During an interviewer response, close the browser tab. Reopen it. Confirm the session reconnects (advisory lock was released promptly).
4. **Cancel session mid-stream:** Start another session, send a turn, click Cancel during response. Confirm session cancels promptly.
