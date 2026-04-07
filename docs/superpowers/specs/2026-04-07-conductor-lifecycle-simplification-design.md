# Conductor Lifecycle Simplification

## Problem

The conductor cancels the pipeline context on `cancel_session`, `end_session`, disconnect, and shutdown. This creates race conditions between context cancellation, WS close, and message delivery. The `session_ended` WS message — the only signal the frontend uses to navigate — is unreliable because the WS may close before the message is received. Result: user clicks Cancel and is stuck on the interview page.

Secondary issues found during investigation:
- `pendingAction == actionCancel` is not handled in the disconnect or shutdown exit paths (only `actionEnd` is handled)
- The defer's `readCancel()` triggers coder/websocket's `context.AfterFunc`, which does a raw TCP close before the graceful WebSocket close, causing abnormal closure (code 1006) and client reconnect loops

## Principles

1. **Pipeline is never cancelled.** Not on cancel, end, disconnect, or shutdown. It finishes naturally.
2. **`Run()` waits for pipeline completion before returning.** Always.
3. **Immediate ack.** Server sends `ack` the moment it receives a lifecycle message — before any cleanup.
4. **Ack is the last client-facing signal.** No `session_ended`. Conductor does internal work after the ack.
5. **No server-side TTS interruption.** Remove `Interrupt()` entirely. TTS runs to completion. Client handles playback interruption locally.

## Protocol Changes

### Removed Messages

| Message | Direction | Reason |
|---------|-----------|--------|
| `session_ended` | server → client | Replaced by `ack`. Client navigates on ack, not on session_ended. |
| `cancel_tts` | client → server | TTS interruption becomes client-side only. |

### Added Messages

| Message | Direction | Format | Description |
|---------|-----------|--------|-------------|
| `ack` | server → client | `{ "type": "ack" }` | Immediate acknowledgment that the server received `cancel_session` or `end_session`. The client already knows which it sent. |

## Backend Changes

### Conductor Main Loop

The select cases record what happened. The code after the select decides what to do.

Three distinct concepts govern the loop:
- **`pendingAction`** (end / cancel / reconnect) — what to do after the pipeline finishes, then return.
- **`shouldExit`** (disconnect / shutdown) — the loop's input sources are exhausted and it cannot continue. Wait for pipeline, process any pending action, return.
- **`pipelineRunning`** — bool gate for processing pending actions. Explicit state, not derived from channel nil-ness.

The pipeline result channel is created per-pipeline and read once. After reading, the drained channel is never selected again (no writers), so there is no need to nil it.

```
const (
    actionEnd       = "end"
    actionCancel    = "cancel"
    actionReconnect = "reconnect"
)

var (
    turnResultCh   <-chan turnResult
    pipelineRunning bool
)

for {
    shouldExit := false

    select {
    case msg, ok := <-msgCh:
        if !ok:
            shouldExit = true
            break

        switch msg.Type:
        case "end_turn":
            if pipelineRunning:
                client.Error("turn_in_progress", ...)
                continue
            ch := make(chan turnResult, 1)
            turnResultCh = ch
            pipelineRunning = true
            go func() { ch <- turnResult{err: c.endTurn(workCtx, msg)} }()
        case "end_session":
            client.Ack()
            pendingAction = actionEnd
        case "cancel_session":
            client.Ack()
            pendingAction = actionCancel
        case "ping":
            client.Pong()

    case res := <-turnResultCh:
        pipelineRunning = false
        // handle pipeline error (log, ForceState, send error to client if no pendingAction)

    case <-warningTimer:
        client.TimerWarning(...)
    case <-overtimeTimer:
        client.TimerOvertime()
    case <-autoEndTimer:
        pendingAction = actionEnd
    case <-reconnectTimer:
        if pendingAction == "":
            pendingAction = actionReconnect
    case <-serverCtx.Done():
        shouldExit = true
    }

    // Wait for pipeline if exiting
    if shouldExit && pipelineRunning:
        <-turnResultCh
        pipelineRunning = false

    // Process pending action when pipeline is done
    if !pipelineRunning && pendingAction != "":
        action := pendingAction
        pendingAction = ""
        switch action:
            actionCancel    → cancelSession(workCtx)
            actionEnd       → endSession(workCtx)
            actionReconnect → client.ReconnectPlease()
        return

    if shouldExit:
        return
}
```

### `cancelSession` / `endSession`

These become pure internal methods — DB operations only, no WS messages:

- `cancelSession`: transition state machine → `backend.CancelSession()` (archive + refund) → transition to ended
- `endSession`: transition state machine → `backend.CompleteSession()` (enqueue eval) → transition to ended

Remove all `client.SessionEnded(...)` calls.

### Defer Cleanup

```
defer func() {
    if turnResultCh != nil:
        <-turnResultCh      // wait for pipeline (shouldn't happen — loop above handles it)
    c.rawWS.Close(websocket.StatusNormalClosure, "session ended")
    readCancel()             // clean up context after WS is closed
    wg.Wait()                // read goroutine exits from closed WS, not cancelled context
    c.lock.Release()
}()
```

Key change: close the WS **before** cancelling the read context. The read goroutine exits because the WS is closed, not because its context was cancelled. This eliminates the `context.AfterFunc` race.

### Observer Removals

**Remove `Interrupt()` from `TokenObserver` interface** (`observer/observer.go`):
```go
// Before
type TokenObserver interface {
    OnToken(token string)
    OnDone(fullMessage string)
    OnError(err error)
    Interrupt()
}

// After
type TokenObserver interface {
    OnToken(token string)
    OnDone(fullMessage string)
    OnError(err error)
}
```

**Remove `Interrupt()` implementations:**
- `TokenFanOut.Interrupt()` — remove method
- `TTSAccumulator.Interrupt()` — remove method. `OnError()` still cancels context (LLM stream failure). `Close()` still waits then cancels (graceful shutdown).
- `MessageAccumulator.Interrupt()` — remove (was no-op)
- `TokenWriter.Interrupt()` — remove (was no-op)

**Remove `observer.Noop` sentinel** — no longer needed. Existed only to ensure `c.obs.Load().Interrupt()` never panicked on nil.

**Remove `c.obs` field** from `Conductor` — the atomic pointer to the current fan-out. No longer needed because nothing reads it cross-goroutine. The fan-out is created locally in `streamInterviewerResponse` and cleaned up via `Close()`.

**Remove all `c.obs.Load().Interrupt()` calls** (5 call sites in conductor.go).

**Remove all `c.obs.Store(...)` calls** (4 call sites in conductor.go).

### ReadLoop

Remove `cancel_tts` handling:

```go
// Remove this block from the readLoop:
if msg.Type == "cancel_tts" {
    c.obs.Load().Interrupt()
    continue
}
```

### Transport Client

**Remove:** `SessionEnded(reason string)` from `Client` interface, `WSClient`, and `Recorder`.

**Add:** `Ack()` to `Client` interface, `WSClient`, and `Recorder`.

```go
func (c *WSClient) Ack() {
    c.send(map[string]string{"type": "ack"})
}
```

### Pipeline Error Handling

The pipeline error path (`res.err != nil` in turnResultCh case) no longer needs to handle context cancellation errors — because the context is never cancelled. The only errors are genuine failures (LLM error, DB error, etc.). Keep: log the error and `ForceState(StateWaitingForInput)`. Skip sending the "turn_failed" error to the client when `pendingAction` is set — the client already received the ack and navigated away.

## Frontend Changes

### Protocol Types (`protocol.ts`)

```typescript
// Remove from ClientMessage:
| { type: "cancel_tts" }

// Remove from ServerMessage:
| { type: "session_ended"; reason: "candidate" | "interviewer" | "timeout" | "cancelled" }

// Add to ServerMessage:
| { type: "ack" }
```

### `useInterview` Hook (`ws/hooks.ts`)

**Add `ack` handler:**
```typescript
case "ack":
    // The client knows what it sent — pendingAction ref tracks it.
    if (pendingActionRef.current === "cancel") {
        setState("cancelled");
    } else if (pendingActionRef.current === "end") {
        setState("ended");
    }
    pendingActionRef.current = null;
    break;
```

**Track pending action:** Add a `pendingActionRef = useRef<"cancel" | "end" | null>(null)` to track which lifecycle message was sent. Set it in `cancelSession()` and `endSession()` before sending.

**Remove:** `session_ended` case from `handleMessage`. Remove `cancelTts` callback and export.

### Interview Page (`interview.tsx`)

- `state === "cancelled"` → `navigate("/")` — unchanged (same useEffect, now triggered by ack)
- `state === "ended"` → WaitingView — unchanged
- Remove `cancelTts` from destructured hooks
- TTS interruption on user response: keep `audioPlayer.cancel()`, remove `cancelTts()` call
- Cancel dialog: keep `audioPlayer.cancel()`, remove any `cancelTts()` call

### `InterviewState` Type

Keep `"cancelled"` and `"ended"` states — they're still used for rendering decisions. They're just triggered by `ack` instead of `session_ended`.

## Tests

### Backend Tests to Update

- `TestWS_CancelSession` — expect `ack` instead of `session_ended`. Verify DB state.
- `TestWS_CancelSessionDuringTTS` — expect `ack` immediately, then verify DB state after pipeline completes.
- `TestWS_AutoEndDuringPipeline` — auto-end sets `pendingAction`, waits for pipeline, then completes.
- `TestWS_PipelineErrorWithPendingEnd` — pipeline error + pending action still processes the action.
- Observer tests — remove `TestTokenFanOut_InterruptPropagates` and `TestTTSAccumulator_Interrupt`.
- Transport tests — remove `TestWSClient_SessionEnded`, add `TestWSClient_Ack`.
- Recorder — remove `SessionEndedEvent`, add `AckEvent`.

### New Test Cases

- **Cancel during pipeline (no context cancellation):** Send `cancel_session` during `streamInterviewerResponse`. Verify: ack is received immediately, pipeline finishes naturally (response persisted), session cancelled in DB.
- **Disconnect with pending cancel:** Send `cancel_session` during pipeline, then close the WS. Verify: conductor waits for pipeline, then cancels session in DB.
- **Shutdown with pending cancel:** Send `cancel_session` during pipeline, then cancel serverCtx. Verify: conductor waits for pipeline, then cancels session in DB.

## What Stays the Same

- `cancel_session` / `end_session` client message types
- Pipeline internals (`streamInterviewerResponse`, `endTurn`)
- DB operations (`CancelSession`, `CompleteSession`)
- State machine transitions
- Reconnect flow
- TTS generation (runs to completion, audio data produced for future GCS storage)
- `TTSAccumulator.OnError()` — still cancels TTS on LLM stream failure
- `TTSAccumulator.Close()` — still waits for completion then cleans up
