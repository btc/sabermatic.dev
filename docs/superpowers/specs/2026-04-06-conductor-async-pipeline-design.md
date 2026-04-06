# Conductor Async Pipeline: Non-Blocking Event Loop

## Problem

The conductor's event loop blocks while processing turns. `endTurn()` → `streamInterviewerResponse()` → LLM streaming → `fanOut.Close()` (TTS wait) runs synchronously in the main goroutine. While blocked (5-60+ seconds), the conductor cannot:

- Observe client disconnect (advisory lock held until TTS finishes)
- Process `end_session` or `cancel_session`
- Fire timers (auto-end, warning, overtime)
- Handle server shutdown gracefully

Each fix so far (per-sentence timeout, non-blocking enqueue, WaitGroup lifecycle) is correct in isolation but works around the fundamental issue: **the event loop shouldn't block on I/O**.

## Root Cause

The conductor conflates two roles:

1. **Event dispatch** — observe messages, timers, disconnect, shutdown and react
2. **Pipeline execution** — STT → LLM → TTS → persist, which takes seconds

Both run on the same goroutine. While role 2 is active, role 1 is suspended.

## Design

Two changes, applied together:

### 1. Fire-and-forget TTS

TTS is a **projection** — supplementary audio rendering of content the client already has as text. The conductor should not wait for it.

**Remove `fanOut.Close()` from the critical path.** After `OnDone()` closes the sentence channel, the TTS goroutine self-terminates (bounded by per-sentence timeouts). `Interrupt()` is called on:

- **Next turn start** — the new `streamInterviewerResponse` calls `c.obs.Load().Interrupt()` before creating a new fan-out
- **Session end / cancel** — `endSession` and `cancelSession` call `c.obs.Load().Interrupt()`
- **Disconnect / shutdown** — the `Run()` defer calls `c.obs.Load().Interrupt()`

Every exit path calls `Interrupt()` → `cancel()`, so the TTS context is always cleaned up. No goroutine or context leak.

**Note on observer coupling:** After TTS self-terminates, `Interrupt()` on the stale fan-out is harmless — `TTSAccumulator.Interrupt()` is an idempotent `cancel()`, and `WSWriter.Interrupt()` / `MessageAccumulator.Interrupt()` are no-ops. If a future observer adds meaningful `Interrupt()` behavior, this implicit coupling would need revisiting.

**Specific changes in `streamInterviewerResponse`:**

Remove `fanOut.Close()` on the happy path (current line 548). The TTS goroutine finishes on its own.

Remove `c.obs.Store(observer.Noop)` on the happy path (current line 529). Keep the fan-out in `c.obs` so `cancel_tts` still reaches TTS. The fan-out is replaced when the next turn creates a new one via `c.obs.Store(fanOut)`.

Change `fanOut.Close()` on both error paths (line 508 — `StreamLLM` failure, and line 543 — persist failure) to `fanOut.Interrupt()`. On error, kill TTS immediately.

### 2. Async pipeline with ownership transfer

Move `endTurn` off the event loop into a goroutine. The event loop stays responsive. Shared state ownership transfers via a channel.

**The shared mutable state:**
- `c.sm` (StateMachine) — state, turnCount. Plain struct, not thread-safe.
- `c.messages` ([]db.Message) — appended after each persist.
- `c.sequence` (int) — incremented per message.

**Ownership rule:** The pipeline goroutine owns these fields for the duration of its run. The event loop does not read or write them while `turnResultCh` is non-nil. The channel receive is the happens-before edge that transfers ownership back.

**New type:**

```go
// turnResult carries the outcome of a pipeline run.
type turnResult struct {
    err error
}
```

**New local variables in the select loop:**

```go
const (
    actionEnd    = "end"
    actionCancel = "cancel"
)

var (
    turnResultCh  <-chan turnResult  // nil when no pipeline running
    cancelTurn    context.CancelFunc // non-nil when pipeline running
    pendingAction string             // "", actionEnd, or actionCancel
)
```

**`sendInitialMessage` also dispatches via the pipeline.** The opening question for new sessions calls `streamInterviewerResponse`, which has the same blocking problem. The event loop must start with `turnResultCh` already set:

```go
// Initial messages to client.
if err := c.sendInitialMessage(workCtx); err != nil {
    // sendInitialMessage only returns error for reconnect/page-refresh failures.
    // For new sessions, it dispatches the opening question as a pipeline goroutine.
    slog.Error("conductor: initial message", "error", err, "session_id", c.sessionID)
    return
}
```

Where `sendInitialMessage` is modified to dispatch the opening question:

```go
func (c *Conductor) sendInitialMessage(ctx context.Context) (err error) {
    // ... reconnect and page-refresh paths unchanged ...

    c.send(ctx, msgSessionLoaded(c.sessionID, c.question, int(c.duration.Minutes()), c.ttsEnabled))
    if len(c.messages) > 0 {
        c.send(ctx, msgReconnectState(0, c.messages))
        c.send(ctx, msgStateChange(StateWaitingForInput))
        return nil
    }
    // New session: opening question dispatched by the caller as a pipeline goroutine.
    return nil
}
```

And the caller dispatches the opening question before entering the event loop:

```go
if !c.isReconnect() && len(c.messages) == 0 {
    ch := make(chan turnResult, 1)
    turnResultCh = ch
    turnCtx, cancel := context.WithCancel(workCtx)
    cancelTurn = cancel
    go func() {
        ch <- turnResult{err: c.streamInterviewerResponse(turnCtx)}
    }()
}
```

The event loop immediately starts with `turnResultCh` set, so disconnect/shutdown during the opening question stream is handled identically to any other turn.

**Event loop changes:**

```go
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
```

**Pipeline completion handling (new select case):**

```go
case res := <-turnResultCh:
    turnResultCh = nil
    cancelTurn = nil

    if res.err != nil {
        slog.Error("conductor: end_turn", "error", res.err, "session_id", c.sessionID)
        c.sm.ForceState(StateWaitingForInput)
        c.send(workCtx, msgError("turn_failed", "failed to process turn, please try again"))
    }

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
```

**Disconnect / shutdown handling — check pendingAction after draining:**

```go
case msg, ok := <-msgCh:
    if !ok {
        if cancelTurn != nil {
            cancelTurn()
            drainPipeline(turnResultCh)
        }
        if pendingAction == actionEnd {
            _ = c.endSession(workCtx)
        }
        return
    }
    // ...

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
```

**`drainPipeline` helper — with timeout to prevent shutdown hangs:**

```go
func drainPipeline(ch <-chan turnResult) {
    select {
    case <-ch:
    case <-time.After(5 * time.Second):
        slog.Error("conductor: pipeline did not exit after cancel")
    }
}
```

**Auto-end timer:**

```go
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
```

**Interrupt TTS in `streamInterviewerResponse`:**

Add at the top of `streamInterviewerResponse`, before creating observers:

```go
c.obs.Load().Interrupt()
```

**Interrupt TTS on session end:**

Add at the top of `endSession` and `cancelSession`:

```go
c.obs.Load().Interrupt()
```

**Interrupt TTS in the Run() defer:**

```go
defer func() {
    c.obs.Load().Interrupt()
    readCancel()
    wg.Wait()
    c.close()
}()
```

### 3. Persist with uncancellable context

When `cancelTurn()` fires (e.g., user ends session mid-turn), `turnCtx` is cancelled. But `PersistInterviewerTurn` uses `turnCtx` for its DB transaction. If cancelled, the persist fails and the interviewer's response is lost.

For `cancel_session` this is acceptable (session is being discarded). For `end_session`, losing the last response could affect evaluation quality.

Fix: use `context.WithoutCancel(ctx)` for the persist step in `streamInterviewerResponse`:

```go
persistCtx := context.WithoutCancel(ctx)
c.sequence++
interviewerMsg, err := c.backend.PersistInterviewerTurn(persistCtx, stream, backend.PersistMessageParams{
    // ...
})
```

This ensures the interviewer message is persisted even when the turn context is cancelled. The persist either succeeds or fails on its own merits (DB error), not because the user clicked End Session.

## Race Elimination

### Data race on `sm.state` / `sm.turnCount`
**Before:** Pipeline goroutine writes `sm.state` (transitions) while event loop writes `sm.state` (endSession). Two unsynchronized writers.
**After:** Event loop defers endSession until pipeline completes. Channel receive provides happens-before guarantee. No concurrent writes.

### Data race on `c.sequence` / `c.messages`
**Before:** Pipeline goroutine appends messages while event loop could process a new `end_turn`.
**After:** Event loop rejects `end_turn` while `turnResultCh != nil`. At most one pipeline goroutine exists.

### Logic race on transition legality
**Before:** `endSession` transitions to `StateEnding` while pipeline is about to transition to `StateWaitingForInput`. One gets an illegal transition, turn count is wrong.
**After:** `endSession` cancels pipeline context, waits for completion, then runs. Pipeline's error path does `ForceState(StateWaitingForInput)`, then `endSession` does `Transition(StateEnding)` — a legal transition.

### WebSocket writes from multiple goroutines
Three concurrent writers: event loop (timers, pong, errors), pipeline goroutine (state changes, tokens, transcription results), TTS goroutine (audio chunks, done, errors). `c.send()` wraps `coder/websocket.Write()`, which is documented as goroutine-safe with internal locking. This is already the case today (readLoop + conductor + TTS goroutine). No change needed.

## Known Limitations

### `ForceState` does not increment `turnCount`
When the pipeline fails after persisting the candidate message but before persisting the interviewer message, `ForceState(StateWaitingForInput)` is called. Unlike `Transition(StateWaitingForInput)`, `ForceState` does not increment `turnCount`. The turn count passed to `CompleteSession` may be inaccurate. This is an existing bug, not introduced by this spec. Fixing it is out of scope.

## What Changes

| File | Change |
|------|--------|
| `conductor.go` | Add `turnResult` type and `drainPipeline` helper. Rewrite select loop to dispatch `endTurn` and opening question in goroutines, defer end/cancel during pipeline, handle completion with pending action. Add `Interrupt()` calls at session end, disconnect, and new turn start. Remove `fanOut.Close()` and `c.obs.Store(Noop)` from happy path in `streamInterviewerResponse`. Change error-path `fanOut.Close()` to `fanOut.Interrupt()`. Use `context.WithoutCancel` for persist. Modify `sendInitialMessage` to not call `streamInterviewerResponse` directly. |

## What Doesn't Change

- `endTurn()` — runs exactly as today, just in a goroutine with a cancellable context
- `endSession()` / `cancelSession()` — same, plus `Interrupt()` at top
- `StateMachine` — no mutex, no changes. Ownership serializes access.
- `TTSAccumulator` — no changes. Already designed for fire-and-forget with `Interrupt()`.
- `TokenFanOut` — no changes.
- Observer interfaces — no changes.
- Frontend — no changes.
- Wire protocol — no changes.

## The `workCtx` Situation

Currently `workCtx := context.Background()` with a comment explaining why it's not derived from `serverCtx`. With the async pipeline:

- The pipeline goroutine receives `turnCtx`, derived from `workCtx` via `context.WithCancel`. This `turnCtx` is cancelled by `cancelTurn()` when the event loop needs the pipeline to stop (end_session, cancel_session, disconnect, shutdown).
- Persist uses `context.WithoutCancel(turnCtx)` to survive cancellation.
- `workCtx` remains `context.Background()` for now. A future improvement could derive it from `serverCtx` with a grace period, but that's out of scope.

## Testing

### Existing tests
- `session_ws_test.go` — existing WS handler tests should continue to pass. The change is internal to the conductor; the external behavior (WS messages, state transitions, session lifecycle) is identical.

### New tests
- **End session during LLM streaming** — start a turn with a slow LLM mock, send `end_session` mid-stream. Verify: pipeline is cancelled, interviewer message is persisted (WithoutCancel), session transitions to Ended, lock is released promptly.
- **Cancel session during TTS** — start a turn, let LLM complete, send `cancel_session` while TTS is synthesizing. Verify: TTS is interrupted, session is cancelled.
- **Disconnect during pipeline** — start a turn, close the WS connection mid-stream. Verify: pipeline context is cancelled, lock is released promptly (not after TTS timeout).
- **Reject concurrent end_turn** — send two `end_turn` messages without waiting. Verify: second one receives `turn_in_progress` error.
- **Auto-end timer during pipeline** — start a turn with a slow LLM, let the auto-end timer fire. Verify: pipeline is cancelled, session transitions to Ended.
- **Pipeline error + pending end** — start a turn that will fail (e.g., STT error), send `end_session` before it fails. Verify: pipeline error is handled, then `endSession` runs.
- **Opening question with disconnect** — start a new session, close WS during opening question LLM stream. Verify: pipeline cancelled, lock released promptly.
- **Pipeline timeout on drain** — start a turn with a synth that ignores context cancellation, call `cancelTurn()`. Verify: `drainPipeline` times out after 5s and the conductor exits rather than hanging forever.
