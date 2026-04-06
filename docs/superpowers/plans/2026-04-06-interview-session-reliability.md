# Interview Session Reliability Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix all failure modes that can break, lose data from, or degrade an interview session.

**Architecture:** Four PRs grouped by coupling. PR 1 (backend data integrity) and PR 2 (client resilience) can run in parallel. PR 3 (observability polish) lands after both. PR 4 (test coverage) lands last.

**Tech Stack:** Go 1.25, React/TypeScript, coder/websocket, Postgres advisory locks

**Spec:** `docs/superpowers/specs/2026-04-06-interview-session-reliability-design.md`

---

## File Map

### PR 1: Backend data integrity
- Modify: `internal/interview/conductor.go` (C1: line 553, C2: lines 451-472, L1: line 160, M2: lines 524-532)
- Modify: `internal/interview/conductor.go` (add `status` field to Conductor struct)
- Test: `internal/handler/session_ws_test.go` (new tests for C1, C2, M2)

### PR 2: Client resilience
- Modify: `web/src/ws/connection.ts` (L3: max retries, C4: ping health check)
- Modify: `web/src/ws/hooks.ts` (C4: visibility recovery)
- Modify: `web/src/pages/interview.tsx` (C3: auto-stop recorder on disconnect, C4: resume AudioContext)

### PR 3: Observability + polish
- Modify: `internal/interview/conductor.go` (M5: thread context into ttsSink, M6: deferred Close)
- Modify: `internal/interview/observer/tts_accumulator.go` (M4: log level)
- Modify: `internal/config/config.go` (M3: timeout default)
- Modify: `internal/interview/conductor.go` (M1: upload failure notification)
- Modify: `web/src/ws/hooks.ts` (M1: handle `audio_upload_failed` message)
- Modify: `web/src/ws/protocol.ts` (M1: add message type)

### PR 4: Test coverage
- Modify: `internal/handler/session_ws_test.go` (L2: 5 missing async pipeline tests)

---

## PR 1: Backend Data Integrity

### Task 1: C1 — Candidate message persist with WithoutCancel

**Files:**
- Modify: `internal/interview/conductor.go:553`
- Test: `internal/handler/session_ws_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/handler/session_ws_test.go`:

```go
// ---------------------------------------------------------------------------
// Test: End session immediately after submitting — candidate message persisted
// ---------------------------------------------------------------------------

func TestWS_EndSessionDuringCandidatePersist(t *testing.T) {
	t.Parallel()

	tokens := []string{"Follow ", "up."}
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

	// Drain opening.
	drainUntilType(t, ws, "session_loaded")
	drainUntilDone(t, ws)
	drainUntilType(t, ws, "state_change") // waiting_for_input

	// Send candidate text, then immediately end session.
	sendMsg(t, ws, wsMsg{"type": "end_turn", "content": "My answer", "input_method": "text"})
	sendMsg(t, ws, wsMsg{"type": "end_session"})

	// Drain until session_ended.
	drainUntilType(t, ws, "session_ended")

	// Verify candidate message was persisted despite cancellation.
	ctx := context.Background()
	msgs, err := db.New(pool).GetMessagesBySession(ctx, session.ID)
	require.NoError(t, err)

	// Should have at least: opening (interviewer) + candidate.
	// The interviewer response may or may not be present depending on timing.
	candidateFound := false
	for _, m := range msgs {
		if m.Role == "candidate" && m.Content == "My answer" {
			candidateFound = true
		}
	}
	assert.True(t, candidateFound, "candidate message must be persisted even when end_session cancels the pipeline")
}
```

- [ ] **Step 2: Run the test to verify behavior**

Run: `go test ./internal/handler/ -run TestWS_EndSessionDuringCandidatePersist -v -count=1`

This test may pass intermittently depending on timing. The fix makes it deterministic.

- [ ] **Step 3: Apply the fix**

In `internal/interview/conductor.go`, change line 553 from:

```go
	candidateMsg, err := c.persistMessage(ctx, messageID, "candidate", candidateContent, msg.InputMethod)
```

to:

```go
	candidateMsg, err := c.persistMessage(context.WithoutCancel(ctx), messageID, "candidate", candidateContent, msg.InputMethod)
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/handler/ -run TestWS_EndSessionDuringCandidatePersist -v -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/interview/conductor.go internal/handler/session_ws_test.go
git commit -m "fix(conductor): use WithoutCancel for candidate message persist

Candidate persist at conductor.go:553 used raw ctx. If end_session
arrived mid-persist, the candidate's answer was silently lost.
Matches the interviewer persist which already uses WithoutCancel.

Fixes C1 in #102."
```

---

### Task 2: C2/C5 — Re-trigger interviewer response on reconnect gap

**Files:**
- Modify: `internal/interview/conductor.go` (struct + loadSession + sendInitialMessage + Run)
- Test: `internal/handler/session_ws_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/handler/session_ws_test.go`:

```go
// ---------------------------------------------------------------------------
// Test: Reconnect after crash mid-stream — interviewer response re-triggered
// ---------------------------------------------------------------------------

func TestWS_ReconnectRetriggersInterviewerResponse(t *testing.T) {
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

	// First connection: complete opening + submit candidate text.
	ws1 := wsConnect(t, srv.URL, session.ID, cookie, nil)
	drainUntilType(t, ws1, "session_loaded")
	drainUntilDone(t, ws1)
	drainUntilType(t, ws1, "state_change") // waiting_for_input

	sendMsg(t, ws1, wsMsg{"type": "end_turn", "content": "My answer", "input_method": "text"})
	drainUntilType(t, ws1, "state_change") // processing_input

	// Simulate crash: disconnect DURING the interviewer stream (before it persists).
	// Close with abnormal code so conductor cancels the pipeline.
	ws1.Close(websocket.StatusGoingAway, "simulated crash")
	time.Sleep(500 * time.Millisecond) // let conductor process disconnect + drain

	// Verify DB state: candidate message persisted, but NO interviewer response.
	ctx := context.Background()
	msgs, err := db.New(pool).GetMessagesBySession(ctx, session.ID)
	require.NoError(t, err)
	require.Equal(t, 2, len(msgs), "should have opening + candidate, no interviewer response")
	assert.Equal(t, "interviewer", msgs[0].Role)
	assert.Equal(t, "candidate", msgs[1].Role)

	// Reconnect. Conductor should detect the gap and re-trigger interviewer response.
	lastSeq := int(msgs[len(msgs)-1].Seq)
	ws2 := wsConnect(t, srv.URL, session.ID, cookie, &lastSeq)
	defer ws2.CloseNow()

	// Should receive reconnect_state first.
	reconnectMsg := readMsg(t, ws2)
	assert.Equal(t, "reconnect_state", reconnectMsg["type"])

	// Then the interviewer should start streaming automatically.
	stateIS := readMsg(t, ws2)
	assert.Equal(t, "state_change", stateIS["type"])
	assert.Equal(t, "interviewer_speaking", stateIS["state"])

	// Drain the full response.
	responseText, _ := drainUntilDone(t, ws2)
	assert.NotEmpty(t, responseText)

	stateWait := readMsg(t, ws2)
	assert.Equal(t, "state_change", stateWait["type"])
	assert.Equal(t, "waiting_for_input", stateWait["state"])

	// Verify DB: now 3 messages (opening + candidate + re-triggered response).
	msgs2, err := db.New(pool).GetMessagesBySession(ctx, session.ID)
	require.NoError(t, err)
	assert.Equal(t, 3, len(msgs2))
	assert.Equal(t, "interviewer", msgs2[2].Role)
}
```

- [ ] **Step 2: Run the test to confirm it fails**

Run: `go test ./internal/handler/ -run TestWS_ReconnectRetriggersInterviewerResponse -v -count=1`
Expected: FAIL — after reconnect, no `state_change` to `interviewer_speaking` is sent.

- [ ] **Step 3: Add `status` field to Conductor and populate in loadSession**

In `internal/interview/conductor.go`, add `status` to the struct (after `model string`):

```go
	model         string
	status        string // DB session status (active, completed, cancelled, etc.)
```

In `loadSession`, after `c.model = c.backend.Config().LLM.InterviewerModel` (line 428), add:

```go
	c.status = row.Status
```

- [ ] **Step 4: Add `needsInterviewerRecovery` method**

In `internal/interview/conductor.go`, add after `isReconnect()`:

```go
// needsInterviewerRecovery returns true when the last persisted message is
// from the candidate and the session is still active — meaning the
// interviewer's response was lost (crash, disconnect during streaming).
// The conductor should re-trigger streamInterviewerResponse.
func (c *Conductor) needsInterviewerRecovery() bool {
	if len(c.messages) == 0 || c.status != "active" {
		return false
	}
	return c.messages[len(c.messages)-1].Role == "candidate"
}
```

- [ ] **Step 5: Dispatch recovery pipeline in Run**

In `internal/interview/conductor.go`, after the existing opening-question dispatch block (lines 262-270), add:

```go
	// Re-trigger interviewer response if the last persisted message is from
	// the candidate (gap from crash or disconnect during streaming).
	if c.needsInterviewerRecovery() {
		ch := make(chan turnResult, 1)
		turnResultCh = ch
		turnCtx, cancel := context.WithCancel(workCtx)
		cancelTurn = cancel
		go func() {
			ch <- turnResult{err: c.streamInterviewerResponse(turnCtx)}
		}()
	}
```

- [ ] **Step 6: Run the test**

Run: `go test ./internal/handler/ -run TestWS_ReconnectRetriggersInterviewerResponse -v -count=1`
Expected: PASS

- [ ] **Step 7: Run the full WS test suite to check for regressions**

Run: `go test ./internal/handler/ -run TestWS -v -count=1`
Expected: All tests pass.

- [ ] **Step 8: Commit**

```bash
git add internal/interview/conductor.go internal/handler/session_ws_test.go
git commit -m "feat(conductor): re-trigger interviewer response on reconnect gap

When a client reconnects and the last persisted message is from the
candidate (interviewer response was lost to crash or disconnect during
streaming), the conductor now automatically dispatches a new
streamInterviewerResponse pipeline.

Uses the strict I,C,I,C alternation invariant: last.Role == candidate
AND session status == active means the response was lost.

Fixes C2/C5 in #102."
```

---

### Task 3: L1 — Panic recovery in conductor

**Files:**
- Modify: `internal/interview/conductor.go:160`

- [ ] **Step 1: Add the panic recovery defer**

In `internal/interview/conductor.go`, add `"runtime/debug"` to the imports.

Then, at the very top of `func (c *Conductor) Run(serverCtx context.Context)` (line 160), before the advisory lock acquisition, add:

```go
	defer func() {
		if r := recover(); r != nil {
			slog.Error("conductor: panic recovered",
				"recover", r,
				"session_id", c.sessionID,
				"user_id", c.userID,
				"stack", string(debug.Stack()))
		}
	}()
```

This must be the FIRST defer in Run so it runs LAST (after the existing cleanup defer at line 226).

- [ ] **Step 2: Run existing tests to confirm no regression**

Run: `go test ./internal/handler/ -run TestWS -v -count=1`
Expected: All tests pass.

- [ ] **Step 3: Commit**

```bash
git add internal/interview/conductor.go
git commit -m "fix(conductor): add panic recovery with structured logging

Captures panics in the conductor goroutine with session_id, user_id,
and full stack trace. The existing defer chain (interrupt, readCancel,
wg.Wait, close) still handles cleanup.

Fixes L1 in #102."
```

---

### Task 4: M2 — Single STT retry on transient failure

**Files:**
- Modify: `internal/interview/conductor.go:524-532`
- Test: `internal/handler/session_ws_test.go`

- [ ] **Step 1: Write the failing test**

First, add a failing-then-succeeding transcriber to the test helpers in `internal/handler/session_ws_test.go`:

```go
// failOnceTranscriber fails on the first call, succeeds on subsequent calls.
type failOnceTranscriber struct {
	text    string
	calls   int
}

func (f *failOnceTranscriber) Transcribe(_ context.Context, _ []byte, _ string) (string, error) {
	f.calls++
	if f.calls == 1 {
		return "", fmt.Errorf("transient OpenAI error")
	}
	return f.text, nil
}
```

Then add the test:

```go
// ---------------------------------------------------------------------------
// Test: STT transient failure — single retry succeeds
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

	// Should succeed on retry — expect transcription_result.
	stateTranscribing := readMsg(t, ws)
	assert.Equal(t, "transcribing", stateTranscribing["state"])

	transcription := readMsg(t, ws)
	assert.Equal(t, "transcription_result", transcription["type"])
	assert.Equal(t, "I would use a hash-based approach.", transcription["text"])

	// Verify transcriber was called twice (first failed, second succeeded).
	assert.Equal(t, 2, stt.calls, "transcriber should be called twice (retry)")
}
```

- [ ] **Step 2: Run the test to confirm it fails**

Run: `go test ./internal/handler/ -run TestWS_STTRetry -v -count=1`
Expected: FAIL — first transcription fails, no retry, turn fails.

- [ ] **Step 3: Add retry logic**

In `internal/interview/conductor.go`, replace lines 525-528:

```go
		// STT.
		text, err := c.backend.Transcribe(ctx, msg.Audio, msg.AudioExt())
		if err != nil {
			slog.Error("conductor: transcription failed", "error", err, "session_id", c.sessionID)
			return fmt.Errorf("transcription: %w", err)
		}
```

with:

```go
		// STT — single retry on transient failure.
		text, err := c.backend.Transcribe(ctx, msg.Audio, msg.AudioExt())
		if err != nil {
			slog.Warn("conductor: transcription failed, retrying",
				"error", err, "session_id", c.sessionID)
			time.Sleep(500 * time.Millisecond)
			text, err = c.backend.Transcribe(ctx, msg.Audio, msg.AudioExt())
		}
		if err != nil {
			slog.Error("conductor: transcription failed after retry",
				"error", err, "session_id", c.sessionID)
			return fmt.Errorf("transcription: %w", err)
		}
```

- [ ] **Step 4: Run the test**

Run: `go test ./internal/handler/ -run TestWS_STTRetry -v -count=1`
Expected: PASS

- [ ] **Step 5: Run the full WS test suite**

Run: `go test ./internal/handler/ -run TestWS -v -count=1`
Expected: All tests pass.

- [ ] **Step 6: Commit**

```bash
git add internal/interview/conductor.go internal/handler/session_ws_test.go
git commit -m "feat(conductor): single STT retry on transient failure

Adds one retry with 500ms backoff before failing the turn. Prevents
transient Whisper API errors from forcing the user to re-record.

Fixes M2 in #102."
```

---

## PR 2: Client Resilience

### Task 5: C3 — Auto-stop recording on disconnect

**Files:**
- Modify: `web/src/pages/interview.tsx`

- [ ] **Step 1: Add the useEffect to auto-stop recording on disconnect**

In `web/src/pages/interview.tsx`, after the "Cancel TTS when user starts responding" block (around line 310), add:

```typescript
  // ------ Auto-stop recording on disconnect ------
  // MediaRecorder can't survive a reconnect. Stop it on disconnect so
  // segments are finalized and preserved in the AudioRecorder instance.
  // The user can submit the buffered recording after reconnect.
  useEffect(() => {
    if (connectionState === ConnectionState.Reconnecting && audioRecorder.isRecording) {
      recStop();
    }
  }, [connectionState, audioRecorder.isRecording, recStop]);
```

- [ ] **Step 2: Run the frontend dev server and test manually**

Run: `cd web && npm run dev`
Open the interview page. Start recording, then kill the backend server to simulate disconnect. Verify the recording stops (waveform freezes, segments preserved) and can be submitted after reconnect.

- [ ] **Step 3: Commit**

```bash
git add web/src/pages/interview.tsx
git commit -m "fix(web): auto-stop recording on WebSocket disconnect

When the connection drops to Reconnecting, immediately stop the
MediaRecorder so segments are finalized. The AudioRecorder instance
survives reconnection, so the user can submit buffered audio.

Fixes C3 in #102."
```

---

### Task 6: C4 — Visibility change handler

**Files:**
- Modify: `web/src/ws/connection.ts`
- Modify: `web/src/pages/interview.tsx`

- [ ] **Step 1: Add ping/pong health check to ConnectionManager**

In `web/src/ws/connection.ts`, add a `healthCheck` method and expose it:

```typescript
  private healthCheckTimer: ReturnType<typeof setTimeout> | null = null;

  healthCheck() {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return;
    // Send ping, expect pong within 3s. If no pong, close WS to trigger reconnect.
    let gotPong = false;
    const prevHandler = this.onMessage;
    const pongListener = (msg: ServerMessage) => {
      prevHandler(msg);
      if (msg.type === "pong") {
        gotPong = true;
      }
    };
    this.onMessage = pongListener;
    this.send({ type: "ping" });
    this.healthCheckTimer = setTimeout(() => {
      this.onMessage = prevHandler;
      if (!gotPong && this.ws?.readyState === WebSocket.OPEN) {
        // Server is unresponsive — force reconnect.
        this.ws.close(4000, "health check timeout");
      }
    }, 3000);
  }
```

- [ ] **Step 2: Add visibility recovery hook to interview page**

In `web/src/pages/interview.tsx`, after the auto-stop recording effect, add:

```typescript
  // ------ Visibility change recovery ------
  // When the user returns to the tab, check WS health and resume audio.
  useEffect(() => {
    const handler = () => {
      if (document.visibilityState !== "visible") return;
      // Force WS health check — ping/pong with 3s timeout.
      cmRef.current?.healthCheck();
      // Resume AudioContext if browser suspended it.
      audioPlayer.initContext();
    };
    document.addEventListener("visibilitychange", handler);
    return () => document.removeEventListener("visibilitychange", handler);
  }, [audioPlayer]);
```

Note: `cmRef` is already available in `useInterview`. We need to expose it. Add to the return object of `useInterview` in `web/src/ws/hooks.ts`:

In the return statement of `useInterview`, add `cmRef`:

```typescript
  return {
    messages,
    streamingText,
    state,
    connectionState,
    sessionInfo,
    lastError,
    wasReconnected,
    sendText,
    sendAudio,
    endSession,
    cancelSession,
    cancelTts,
    setRawMessageHandler,
    cmRef,
  };
```

Then in `interview.tsx`, destructure it from `useInterview`:

```typescript
  const {
    messages,
    streamingText,
    state,
    connectionState,
    sessionInfo,
    lastError,
    wasReconnected,
    sendText,
    sendAudio,
    endSession,
    cancelSession,
    cancelTts,
    setRawMessageHandler,
    cmRef,
  } = useInterview(sessionId);
```

- [ ] **Step 3: Test manually**

Open the interview page. Switch to another tab for 30+ seconds, then switch back. Verify:
- WS ping is sent on return
- AudioContext resumes (TTS plays)
- If WS died during tab switch, reconnection triggers within 3s of returning

- [ ] **Step 4: Commit**

```bash
git add web/src/ws/connection.ts web/src/ws/hooks.ts web/src/pages/interview.tsx
git commit -m "feat(web): visibility change handler for tab-switch recovery

Adds a visibilitychange listener that fires a WS health check
(ping with 3s timeout) and resumes AudioContext when the user
returns to the tab. Detects dead connections within 3s instead
of waiting for the browser's onclose event.

Fixes C4 in #102."
```

---

### Task 7: L3 — Bounded reconnect retries

**Files:**
- Modify: `web/src/ws/connection.ts`
- Modify: `web/src/pages/interview.tsx`

- [ ] **Step 1: Add max retry and manual retry to ConnectionManager**

In `web/src/ws/connection.ts`, add a max retry constant and `exhausted` state:

At the top of the class, add:

```typescript
  private static readonly MAX_RETRIES = 20;
```

In the `onclose` handler, before the exponential backoff block, add a check:

Replace this section:

```typescript
      // Unexpected close — reconnect with backoff
      this.onStateChange(ConnectionState.Reconnecting);
      const delay = Math.min(1000 * Math.pow(2, this.retryCount), 30000);
      const jitter = delay * (0.5 + Math.random() * 0.5);
      this.retryCount++;
      this.retryTimer = setTimeout(() => this.open(), jitter);
```

with:

```typescript
      // Unexpected close — reconnect with backoff
      if (this.retryCount >= ConnectionManager.MAX_RETRIES) {
        this.onStateChange(ConnectionState.Disconnected);
        return;
      }
      this.onStateChange(ConnectionState.Reconnecting);
      const delay = Math.min(1000 * Math.pow(2, this.retryCount), 30000);
      const jitter = delay * (0.5 + Math.random() * 0.5);
      this.retryCount++;
      this.retryTimer = setTimeout(() => this.open(), jitter);
```

Add a `retry` method for manual reconnection:

```typescript
  retry() {
    this.retryCount = 0;
    this.open();
  }
```

- [ ] **Step 2: Add manual retry UI to interview page**

In `web/src/pages/interview.tsx`, update the `ReconnectingBanner` to handle the disconnected state too. Replace the existing `ReconnectingBanner` component:

```typescript
function ConnectionBanner({
  state,
  onRetry,
}: {
  state: ConnectionState;
  onRetry: () => void;
}) {
  if (state === ConnectionState.Reconnecting) {
    return (
      <div className="bg-amber-100 dark:bg-amber-900/40 text-amber-800 dark:text-amber-200 text-center text-sm py-1.5 px-4">
        Reconnecting...
      </div>
    );
  }
  if (state === ConnectionState.Disconnected) {
    return (
      <div className="bg-red-100 dark:bg-red-900/40 text-red-800 dark:text-red-200 text-center text-sm py-1.5 px-4 flex items-center justify-center gap-2">
        <span>Session disconnected</span>
        <button
          onClick={onRetry}
          className="underline font-medium hover:no-underline"
        >
          Tap to retry
        </button>
      </div>
    );
  }
  return null;
}
```

Update the banner rendering in the JSX (replace the existing `{connectionState === ConnectionState.Reconnecting && <ReconnectingBanner />}`):

```typescript
      <ConnectionBanner
        state={connectionState}
        onRetry={() => cmRef.current?.retry()}
      />
```

- [ ] **Step 3: Test manually**

Kill the backend server. Watch the client exhaust retries (~5 min). Verify "Session disconnected — Tap to retry" appears. Click retry, restart the server, verify reconnection works.

- [ ] **Step 4: Commit**

```bash
git add web/src/ws/connection.ts web/src/pages/interview.tsx
git commit -m "feat(web): bounded reconnect retries with manual retry UI

Caps automatic reconnect attempts at 20 (~5 min). After exhaustion,
shows 'Session disconnected — Tap to retry' banner instead of
silently retrying forever.

Fixes L3 in #102."
```

---

## PR 3: Observability + Polish

### Task 8: M3 — Reduce TTSSentenceTimeout to 10s

**Files:**
- Modify: `internal/config/config.go:62`

- [ ] **Step 1: Change the default**

In `internal/config/config.go`, line 62, change:

```go
	TTSSentenceTimeout time.Duration `env:"TTS_SENTENCE_TIMEOUT,default=30s"`
```

to:

```go
	TTSSentenceTimeout time.Duration `env:"TTS_SENTENCE_TIMEOUT,default=10s"`
```

- [ ] **Step 2: Run tests**

Run: `go test ./internal/config/ -v -count=1`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/config/config.go
git commit -m "fix(config): reduce TTSSentenceTimeout default from 30s to 10s

30s of silence before error toast is too generous. The timeout covers
both Synthesize + ReadAll since ac01648, so 10s is sufficient.

Fixes M3 in #102."
```

---

### Task 9: M4 — Log TTS synthesis failures at Warn

**Files:**
- Modify: `internal/interview/observer/tts_accumulator.go:158`

- [ ] **Step 1: Change the log level**

In `internal/interview/observer/tts_accumulator.go`, line 158, change:

```go
				slog.Debug("tts: sentence synthesis failed",
```

to:

```go
				slog.Warn("tts: sentence synthesis failed",
```

Also change line 175:

```go
				slog.Debug("tts: audio read failed",
```

to:

```go
				slog.Warn("tts: audio read failed",
```

- [ ] **Step 2: Run tests**

Run: `go test ./internal/interview/observer/ -v -count=1`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/interview/observer/tts_accumulator.go
git commit -m "fix(tts): log synthesis failures at Warn, not Debug

HTTP 500s and read failures from the TTS provider are at least as
alarming as timeouts (already at Warn). Makes them visible in
production logs.

Fixes M4 in #102."
```

---

### Task 10: M5 — Thread context into ttsSink

**Files:**
- Modify: `internal/interview/conductor.go:93-120, 602`

- [ ] **Step 1: Add context field to ttsSink**

In `internal/interview/conductor.go`, change the `ttsSink` struct (lines 93-97):

```go
type ttsSink struct {
	ws        observer.WSConn
	ctx       context.Context
	messageID uuid.UUID
	seq       int
}
```

Update all three methods to use `s.ctx` instead of `context.Background()`:

```go
func (s *ttsSink) HandleAudio(data []byte) {
	_ = s.ws.SendJSON(s.ctx, map[string]any{
		"type":       "tts_chunk",
		"data":       base64.StdEncoding.EncodeToString(data),
		"message_id": s.messageID.String(),
		"seq":        s.seq,
	})
	s.seq++
}

func (s *ttsSink) HandleTTSDone() {
	_ = s.ws.SendJSON(s.ctx, map[string]any{
		"type":       "tts_done",
		"message_id": s.messageID.String(),
	})
}

func (s *ttsSink) HandleTTSError() {
	_ = s.ws.SendJSON(s.ctx, map[string]any{
		"type": "tts_error",
	})
}
```

- [ ] **Step 2: Pass context at construction site**

In `streamInterviewerResponse` (around line 602), change the sink construction:

```go
			sink := &ttsSink{ws: c.ws, messageID: messageID}
```

to:

```go
			sink := &ttsSink{ws: c.ws, ctx: ctx, messageID: messageID}
```

Note: `ctx` here is the turn context, which is cancelled on interrupt — exactly what we want. When the accumulator calls `Interrupt()`, the context is cancelled, and all pending `SendJSON` calls will fail fast instead of hanging.

- [ ] **Step 3: Run tests**

Run: `go test ./internal/handler/ -run TestWS -v -count=1`
Expected: All tests pass.

- [ ] **Step 4: Commit**

```bash
git add internal/interview/conductor.go
git commit -m "fix(conductor): thread turn context into ttsSink

ttsSink methods used context.Background() for WS writes. When the
client disconnects, writes hung until the WS write timeout. Now
uses the turn context, so writes fail fast on Interrupt().

Fixes M5 in #102."
```

---

### Task 11: M6 — Deferred fanOut.Close() after OnDone

**Files:**
- Modify: `internal/interview/conductor.go` (after line 644)

- [ ] **Step 1: Add deferred close**

In `internal/interview/conductor.go`, after line 644 (`fanOut.OnDone(fullText)`), add:

```go
	// Clean up the fan-out's TTS context in the background. In the happy
	// path Close() waits for in-flight synthesis; Interrupt() is the forced path.
	go func() {
		fanOut.Close()
		c.obs.Store(observer.Noop)
	}()
```

- [ ] **Step 2: Run tests**

Run: `go test ./internal/handler/ -run TestWS -v -count=1`
Expected: All tests pass.

- [ ] **Step 3: Commit**

```bash
git add internal/interview/conductor.go
git commit -m "fix(conductor): deferred fanOut.Close() after OnDone

Previously, Close() was never called on the happy path — the
TTSAccumulator's context persisted until the next turn or session end.
Now a background goroutine waits for in-flight synthesis to complete,
then cleans up.

Fixes M6 in #102."
```

---

### Task 12: M1 — Audio upload failure notification

**Files:**
- Modify: `internal/interview/conductor.go:500-522`
- Modify: `web/src/ws/hooks.ts`
- Modify: `web/src/ws/protocol.ts`

- [ ] **Step 1: Add `audio_upload_failed` message type to protocol**

In `web/src/ws/protocol.ts`, add to the `ServerMessage` union type:

```typescript
  | { type: "audio_upload_failed" }
```

- [ ] **Step 2: Add toast handler in hooks**

In `web/src/ws/hooks.ts`, in the `handleMessage` switch, after the `tts_error` case (around line 132), add:

```typescript
      case "audio_upload_failed":
        toast.info("Audio recording could not be saved. Your response was captured as text.");
        break;
```

- [ ] **Step 3: Modify upload goroutine to notify on failure**

In `internal/interview/conductor.go`, replace the upload goroutine (lines 500-522):

```go
		go func() {
			uploadCtx, uploadSpan := tracer.Start(context.WithoutCancel(ctx), "Conductor.uploadAudio")
			defer uploadSpan.End()

			key := fmt.Sprintf("%s/%s.%s", c.sessionID, messageID, msg.AudioExt())
			url, uploadErr := c.backend.StoreAudio(uploadCtx, key, msg.Audio, msg.AudioMIME)
			if uploadErr != nil {
				uploadSpan.RecordError(uploadErr)
				uploadSpan.SetStatus(codes.Error, "audio upload failed")
				slog.Error("conductor: audio upload failed",
					"error", uploadErr,
					"session_id", c.sessionID,
					"message_id", messageID)
				c.send(uploadCtx, map[string]string{"type": "audio_upload_failed"})
				return
			}
			if setErr := c.backend.SetAudioURL(uploadCtx, messageID, url); setErr != nil {
				uploadSpan.RecordError(setErr)
				slog.Error("conductor: failed to set audio_url",
					"error", setErr,
					"session_id", c.sessionID,
					"message_id", messageID)
			}
		}()
```

The only change is adding `c.send(uploadCtx, map[string]string{"type": "audio_upload_failed"})` after logging the upload failure.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/handler/ -run TestWS -v -count=1`
Expected: All tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/interview/conductor.go web/src/ws/hooks.ts web/src/ws/protocol.ts
git commit -m "feat(conductor): notify client on audio upload failure

Previously, audio upload failures were logged server-side but the
user had no indication. Now sends audio_upload_failed message,
displayed as an informational toast.

Fixes M1 in #102."
```

---

## PR 4: Test Coverage

### Task 13: L2 — Missing async pipeline test cases

**Files:**
- Modify: `internal/handler/session_ws_test.go`

These tests should be written after PRs 1-3 land, as they test the final system state.

- [ ] **Step 1: Write test — cancel session during TTS**

```go
// ---------------------------------------------------------------------------
// Test: Cancel session during TTS synthesis
// ---------------------------------------------------------------------------

func TestWS_CancelSessionDuringTTS(t *testing.T) {
	t.Parallel()

	tokens := []string{"Let's ", "discuss ", "the ", "requirements. ", "First, ", "we ", "need."}
	anthropicSrv := newFakeAnthropicServer(t, tokens)
	b := pg.NewBackend(t)
	b.ApplyTestOverrides(backend.TestOverrides{
		LLM: ai.NewTestClient(anthropicSrv.URL, b.Pool()),
		STT: &fakeTranscriber{text: "My answer."},
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

	// Send a candidate turn, wait for interviewer to start streaming.
	sendMsg(t, ws, wsMsg{"type": "end_turn", "content": "My answer", "input_method": "text"})
	drainUntilType(t, ws, "state_change") // processing_input
	drainUntilType(t, ws, "state_change") // interviewer_speaking

	// Cancel session while interviewer is streaming (TTS synthesis active).
	sendMsg(t, ws, wsMsg{"type": "cancel_session"})

	// Should eventually get session_ended with reason "cancelled".
	ended, _ := drainUntilType(t, ws, "session_ended")
	assert.Equal(t, "cancelled", ended["reason"])

	// Verify session status.
	ctx := context.Background()
	updatedSession, err := db.New(pool).GetSessionByID(ctx, session.ID)
	require.NoError(t, err)
	assert.Equal(t, "cancelled", updatedSession.Status)
}
```

- [ ] **Step 2: Run it**

Run: `go test ./internal/handler/ -run TestWS_CancelSessionDuringTTS -v -count=1`
Expected: PASS

- [ ] **Step 3: Write test — auto-end timer during pipeline**

```go
// ---------------------------------------------------------------------------
// Test: Auto-end fires during active pipeline
// ---------------------------------------------------------------------------

func TestWS_AutoEndDuringPipeline(t *testing.T) {
	t.Parallel()

	// Use a slow Anthropic server that delays between tokens.
	slowSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		fmt.Fprintf(w, "event: message_start\n")
		fmt.Fprintf(w, "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_test\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-sonnet-4-20250514\",\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":25,\"output_tokens\":0}}}\n\n")
		fmt.Fprintf(w, "event: content_block_start\n")
		fmt.Fprintf(w, "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")

		// Stream slowly — 200ms per token.
		tokens := []string{"Slow ", "response."}
		for _, token := range tokens {
			time.Sleep(200 * time.Millisecond)
			fmt.Fprintf(w, "event: content_block_delta\n")
			fmt.Fprintf(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":%q}}\n\n", token)
		}

		fmt.Fprintf(w, "event: content_block_stop\n")
		fmt.Fprintf(w, "data: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
		fmt.Fprintf(w, "event: message_delta\n")
		fmt.Fprintf(w, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":2}}\n\n")
		fmt.Fprintf(w, "event: message_stop\n")
		fmt.Fprintf(w, "data: {\"type\":\"message_stop\"}\n\n")
	}))
	t.Cleanup(slowSrv.Close)

	b := pg.NewBackend(t)
	b.ApplyTestOverrides(backend.TestOverrides{
		LLM: ai.NewTestClient(slowSrv.URL, b.Pool()),
		STT: &fakeTranscriber{text: "My answer."},
		TTS: &fakeSynthesizer{},
	})

	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	pool := b.Pool()
	userID := backendtest.SeedUser(t, b)
	question := createTestQuestion(t, pool)
	// Use 0-minute duration so auto-end fires immediately (2 min grace).
	// Actually, create a very short session and backdate it.
	session := createTestSession(t, pool, userID, question.ID)
	cookie := createAuthCookie(t, pool, userID)

	// Backdate started_at so auto-end fires quickly (duration 45 min + 2 min grace).
	// Set started_at to 48 min ago so auto-end fires within seconds.
	ctx := context.Background()
	_, err := pool.Exec(ctx,
		`UPDATE interview_sessions SET started_at = NOW() - interval '48 minutes' WHERE id = $1`,
		session.ID)
	require.NoError(t, err)

	ws := wsConnect(t, srv.URL, session.ID, cookie, nil)
	defer ws.CloseNow()

	// Drain opening.
	drainUntilType(t, ws, "session_loaded")

	// Auto-end should fire during or soon after the opening stream.
	// The conductor should handle it gracefully.
	var gotSessionEnded bool
	for i := 0; i < 50; i++ {
		m := readMsgTimeout(t, ws, 10*time.Second)
		if m == nil {
			break
		}
		if m["type"] == "session_ended" {
			gotSessionEnded = true
			break
		}
	}
	assert.True(t, gotSessionEnded, "auto-end should fire and end the session")
}
```

- [ ] **Step 4: Run it**

Run: `go test ./internal/handler/ -run TestWS_AutoEndDuringPipeline -v -count=1`
Expected: PASS

- [ ] **Step 5: Write test — pipeline error + pending end**

```go
// ---------------------------------------------------------------------------
// Test: Pipeline error with pending end_session
// ---------------------------------------------------------------------------

func TestWS_PipelineErrorWithPendingEnd(t *testing.T) {
	t.Parallel()

	// Anthropic server that returns an error mid-stream.
	errSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		fmt.Fprintf(w, "event: message_start\n")
		fmt.Fprintf(w, "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_test\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-sonnet-4-20250514\",\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":25,\"output_tokens\":0}}}\n\n")
		fmt.Fprintf(w, "event: content_block_start\n")
		fmt.Fprintf(w, "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		fmt.Fprintf(w, "event: content_block_delta\n")
		fmt.Fprintf(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Partial\"}}\n\n")
		// Delay then close — simulates mid-stream error.
		time.Sleep(200 * time.Millisecond)
		// Close connection abruptly (no message_stop).
	}))
	t.Cleanup(errSrv.Close)

	b := pg.NewBackend(t)
	b.ApplyTestOverrides(backend.TestOverrides{
		LLM: ai.NewTestClient(errSrv.URL, b.Pool()),
		STT: &fakeTranscriber{text: "My answer."},
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

	// Complete the opening.
	drainUntilType(t, ws, "session_loaded")
	// Opening will also error since the server always errors. Accept whatever happens.
	// Wait for either turn_failed or interviewer_done.
	for i := 0; i < 50; i++ {
		m := readMsgTimeout(t, ws, 5*time.Second)
		if m == nil {
			break
		}
		if m["type"] == "error" || m["type"] == "state_change" && m["state"] == "waiting_for_input" {
			break
		}
	}

	// Send end_session — should succeed regardless of pipeline state.
	sendMsg(t, ws, wsMsg{"type": "end_session"})

	ended, _ := drainUntilType(t, ws, "session_ended")
	assert.Equal(t, "session_ended", ended["type"])
}
```

- [ ] **Step 6: Run it**

Run: `go test ./internal/handler/ -run TestWS_PipelineErrorWithPendingEnd -v -count=1`
Expected: PASS

- [ ] **Step 7: Commit all L2 tests**

```bash
git add internal/handler/session_ws_test.go
git commit -m "test(conductor): add missing async pipeline test cases

Adds tests for cancel-during-TTS, auto-end-during-pipeline, and
pipeline-error-with-pending-end scenarios. Drain timeout and
buffer-full tests deferred — they require deeper test infrastructure
changes (mock slow synthesizer, mock full channel).

Partially addresses L2 in #102."
```

- [ ] **Step 8: Run the full test suite**

Run: `go test ./internal/handler/ -run TestWS -v -count=1`
Expected: All tests pass.
