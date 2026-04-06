# Interview Session Reliability: Comprehensive Failure Mode Analysis

**Date:** 2026-04-06
**Status:** Approved
**Supersedes:** Issues #99, #100, #101

## Purpose

Comprehensive audit of every way an interview session can break, lose data, or degrade. Covers the full lifecycle: connection establishment, recording, transcription, LLM streaming, TTS playback, reconnection, crash recovery, and session completion. Each failure mode is categorized, root-caused, and given a concrete fix.

---

## Architecture Context

A single `Conductor` goroutine owns all mutable state for one interview session. It communicates with the client via WebSocket and with the database via a `Backend` service layer. Key properties:

- **Advisory lock**: Postgres `pg_try_advisory_lock` ensures one conductor per session
- **Async pipeline**: `endTurn` runs in a goroutine; the event loop stays responsive
- **Fire-and-forget TTS**: TTS synthesis runs independently; doesn't block turn completion
- **Message persistence**: Candidate messages persisted before LLM starts; interviewer messages persisted after streaming completes (atomic with LLM call record)
- **Reconnection**: Client sends `session_init { last_seq }`, server replays missed messages from DB

---

## Failure Modes

### C1: Candidate message lost on End Session

**Severity:** Critical — data loss
**File:** `conductor.go:553`

The candidate message persist uses raw `ctx`. The interviewer persist at line 647 correctly uses `context.WithoutCancel(ctx)`. If the user clicks End Session after submitting audio/text but before the candidate message is written to the DB, the persist fails with `context.Canceled` and the candidate's answer is silently lost.

**Impact:** Lost candidate answer degrades evaluation quality. The LLM's follow-up question appears to respond to nothing.

**Fix:** Use `context.WithoutCancel(ctx)` for the candidate persist:
```go
candidateMsg, err := c.persistMessage(context.WithoutCancel(ctx), messageID, "candidate", candidateContent, msg.InputMethod)
```

**Scope:** One-line change + regression test.

---

### C2: Server crash mid-LLM-stream loses interviewer response

**Severity:** Critical — data loss, session disruption
**Files:** `conductor.go:629-661`

If the server process crashes (or Cloud Run restarts the container) while the LLM is streaming, the partial interviewer response is lost. It exists only in the `MessageAccumulator`'s in-memory buffer. The candidate's message is already persisted (C1 fix notwithstanding), so on reconnect the candidate sees their own message but no interviewer reply.

**Current state:**
- State machine resets to `WaitingForInput` on reconnect
- Advisory lock auto-releases when the DB connection dies
- Cleanup job marks the session completed after `duration + 5 min`
- No mechanism to retry the interviewer's turn

**Impact:** The interview has a "phantom gap" — the candidate said something, got no response, and must figure out that they should speak again. With TTS enabled, they may not even realize the interviewer was supposed to respond.

**Fix:** Detect the gap on reconnect and automatically re-trigger the interviewer response.

In `sendInitialMessage`, after sending `reconnect_state`, check if the last persisted message is from the candidate (i.e., the interviewer never responded). If so, dispatch a new `streamInterviewerResponse` pipeline:

```go
if c.isReconnect() && len(c.messages) > 0 {
    last := c.messages[len(c.messages)-1]
    if last.Role == "candidate" {
        // Interviewer response was lost — re-trigger it
        // (dispatched as pipeline goroutine, same as opening question)
    }
}
```

**Scope:** ~15 lines in `sendInitialMessage` + integration test. Must handle the edge case where the last candidate message was the End Session trigger (check session status before re-triggering).

---

### C3: In-progress audio recording lost on disconnect

**Severity:** Critical — data loss, user frustration
**Files:** `web/src/audio/recorder.ts`, `web/src/ws/hooks.ts`

If the WebSocket disconnects while the user is actively recording (holding spacebar), the `MediaRecorder` state is lost. The browser's `MediaRecorder` API cannot survive a reconnect — chunks accumulated in the `ondataavailable` handler are gone. The user has no indication that their recording was lost.

After reconnect, the UI returns to `waiting` state, and the recording input resets. The user must re-record their entire response.

**Impact:** User loses their in-progress answer. Particularly bad for long voice responses (30+ seconds).

**Fix — two layers:**

1. **Defensive: auto-stop and buffer on disconnect.** When `ConnectionState` transitions to `Reconnecting`, immediately stop the active `MediaRecorder` (triggering `onstop`, which finalizes the segment). The segments are preserved in the `AudioRecorder` instance, which lives for the component's lifetime — not the WebSocket's. On reconnect, the buffered segments can be submitted.

2. **UX: show reconnection state during recording.** Display a reconnecting banner that preserves the recording UI rather than resetting to `waiting`. On reconnect success, the user can resume or submit their buffered recording.

**Scope:** ~30 lines in hooks + recording-input component. Needs integration test with simulated disconnect.

---

### C4: No visibility change handling

**Severity:** High — session disruption on mobile
**Files:** `web/src/ws/connection.ts`, `web/src/pages/interview.tsx`

Switching tabs on mobile (or locking the phone screen) can cause:
1. **WebSocket killed by OS** — browser suspends background tabs, TCP connection times out
2. **AudioContext suspended** — browser policy suspends audio in background tabs
3. **MediaRecorder stopped** — some browsers stop background audio capture

There is no `visibilitychange` listener anywhere in the app. The only recovery is the existing exponential backoff in `ConnectionManager.onclose`, which fires only when the browser eventually detects the dead connection — potentially seconds to minutes later.

**Impact:** User returns to the interview and finds it in a broken state — no audio, stale connection, potentially missed interviewer response.

**Fix:**
```typescript
// In useInterview or ConnectionManager:
document.addEventListener("visibilitychange", () => {
  if (document.visibilityState === "visible") {
    // 1. Force WS health check — send ping, expect pong within 3s
    //    If no pong, close WS (triggers reconnect)
    // 2. Resume AudioContext if suspended
    // 3. Check MediaRecorder stream tracks — re-acquire if dead
  }
});
```

**Scope:** ~40 lines. New `useVisibilityRecovery` hook or integrated into `ConnectionManager`.

---

### C5: Reconnect during LLM streaming loses partial response

**Severity:** High — jarring UX
**Files:** `conductor.go:451-472`, `web/src/ws/hooks.ts:58-76`

If the client disconnects and reconnects while the LLM is mid-stream:
1. The pipeline is cancelled (`cancelTurn()`)
2. The partial response is never persisted (accumulator text is discarded)
3. On reconnect, `reconnect_state` contains only previously persisted messages
4. The client sees the conversation as it was before the interrupted turn

The candidate's message was persisted (assuming C1 is fixed), but the interviewer's partial response is lost. This is the same "phantom gap" as C2, but triggered by network instability rather than server crash.

**Impact:** Same as C2 — interview has a missing response.

**Fix:** Same as C2 — detect candidate-last-message gap on reconnect and re-trigger interviewer response. The C2 fix handles both server crash and disconnect-during-streaming.

---

### M1: Audio upload failure is silent

**Severity:** Medium — data loss (audio only, transcript preserved)
**Files:** `conductor.go:500-522`

Audio upload runs in a fire-and-forget goroutine with `context.WithoutCancel`. If `StoreAudio` or `SetAudioURL` fails, it's logged server-side but the user sees no indication. The `audio_url` column is NULL in the DB.

**Impact:** Transcription succeeds (STT runs on the raw bytes, not the stored file), so the interview continues normally. But the audio recording is permanently lost — no playback in review. This matters for evaluation quality and user trust.

**Fix:** Track upload status and notify on failure:
```go
// After upload attempt, send result back to conductor via channel
type audioUploadResult struct {
    messageID uuid.UUID
    err       error
}
```
On failure, send a `tts_error`-style notification to the client: "Audio recording could not be saved. Your response was captured as text."

**Scope:** ~20 lines backend + frontend toast handler. Consider retry (1 attempt) before notifying.

---

### M2: No STT retry on transient failure

**Severity:** Medium — user frustration
**Files:** `conductor.go:524-532`

If `Transcribe` fails (network timeout, OpenAI 500, rate limit), the turn fails immediately. The user sees "failed to process turn, please try again" and must re-record everything.

**Current behavior:**
- Error propagates from `endTurn` → `turnResult{err}` → `ForceState(WaitingForInput)` + error message to client
- Audio bytes are still in memory (the `msg` struct) — but the goroutine exits, so they're gone

**Impact:** Transient Whisper failures (rare but real) force the user to re-record. Particularly frustrating for long voice responses.

**Fix:** Add a single retry with backoff inside `endTurn`, before returning the error:
```go
text, err := c.backend.Transcribe(ctx, msg.Audio, msg.AudioExt())
if err != nil {
    slog.Warn("conductor: transcription failed, retrying", "error", err)
    time.Sleep(500 * time.Millisecond)
    text, err = c.backend.Transcribe(ctx, msg.Audio, msg.AudioExt())
}
if err != nil {
    return fmt.Errorf("transcription: %w", err)
}
```

**Scope:** ~5 lines. Single retry is sufficient — if Whisper is down, a second attempt won't help, and the user can re-record.

---

### M3: TTSSentenceTimeout 30s is too generous

**Severity:** Medium — degraded UX
**File:** `internal/config/config.go:62`

A TTS synthesis that hangs for 30s means 30 seconds of silence before the error toast appears. The user may think the app is broken and leave.

**Fix:** Reduce default to 10s. The original bump from 10s to 30s was to cover both `Synthesize` + `ReadAll`, but `ac01648` already deferred the cancel to cover both — the timeout just needs to be reasonable for a single sentence.

**Scope:** One-line config change. Monitor in production; make it configurable per-environment if needed.

---

### M4: TTS HTTP 500 logged at Debug, not Warn

**Severity:** Medium — observability gap
**File:** `observer/tts_accumulator.go:158`

Synthesis timeouts are logged at `slog.Warn`, but other synthesis failures (HTTP 500, auth errors) are logged at `slog.Debug`. A 500 from the TTS provider is at least as alarming as a timeout.

**Fix:** Log all synthesis errors at `slog.Warn`:
```go
slog.Warn("tts: synthesis failed", "error", err, ...)
```

**Scope:** One-line change.

---

### M5: ttsSink uses context.Background() for WS writes

**Severity:** Low-Medium — slow failure on disconnect
**Files:** `conductor.go:100,110,118`

All three `ttsSink` methods (`HandleAudio`, `HandleTTSDone`, `HandleTTSError`) use `context.Background()` for `SendJSON`. When the client has disconnected, writes hang until the WebSocket's internal write timeout (typically 10s in `coder/websocket`) rather than failing immediately.

**Impact:** TTS goroutine keeps trying to write to a dead connection for up to 10s per chunk. With 20+ TTS chunks queued, that's minutes of wasted work. The goroutine eventually exits when the accumulator's context is cancelled, but it's slow.

**Fix:** Pass the accumulator's context (or a context derived from it) into the ttsSink at construction time:
```go
type ttsSink struct {
    ws        observer.WSConn
    ctx       context.Context // from accumulator, cancelled on Interrupt
    messageID uuid.UUID
    seq       int
}
```

**Scope:** ~10 lines. Thread context through ttsSink constructor.

---

### M6: Stale TTS context between turns

**Severity:** Low — no user impact
**Files:** `conductor.go` (streamInterviewerResponse), `observer/tts_accumulator.go` (Close)

With fire-and-forget TTS, `fanOut.Close()` is never called on the happy path. The TTSAccumulator's `context.WithCancel(context.Background())` persists between turns until `Interrupt()` is called on the next turn or session end.

**Impact:** Lightweight — `context.WithCancel` creates minimal state. No goroutine leak (synthesis goroutines complete naturally).

**Fix:** Call `fanOut.Close()` in a deferred goroutine after `OnDone`:
```go
go func() {
    fanOut.Close()
    c.obs.Store(observer.Noop)
}()
```

**Scope:** ~5 lines. Nice-to-have cleanup.

---

### L1: No panic recovery in conductor

**Severity:** Low — defense in depth
**File:** `conductor.go:160`

If the conductor goroutine panics, the `defer` cleanup runs (lock release, WS close), but:
1. No error is logged with context (session ID, user ID)
2. The session sits in `active` status until the cleanup job finds it (~8 min)
3. The user sees a disconnection with no explanation

**Fix:** Add `defer recover()` at the top of `Run`:
```go
defer func() {
    if r := recover(); r != nil {
        slog.Error("conductor: panic", "recover", r, "session_id", c.sessionID,
            "stack", string(debug.Stack()))
    }
}()
```

**Scope:** ~5 lines. The existing defer chain (interrupt, readCancel, wg.Wait, close) handles cleanup.

---

### L2: Missing async pipeline test cases

**Severity:** Low — coverage gap
**Files:** `internal/handler/session_ws_test.go`

5 of 8 spec'd test cases are not implemented:
1. Cancel session during TTS
2. Auto-end timer during pipeline
3. Pipeline error + pending end
4. Pipeline drain timeout
5. Buffer-full sentence drop

The 3 implemented tests (mid-stream cancel, disconnect during pipeline, concurrent turn rejection) cover the most critical paths.

**Fix:** Implement the remaining 5 tests. Priority order: drain timeout (L2.4, verifies no hangs), pipeline error + pending end (L2.3, verifies error recovery), then the rest.

---

### L3: Reconnect retry has no maximum

**Severity:** Low — battery/resource drain
**File:** `web/src/ws/connection.ts:90-93`

The exponential backoff caps at 30s but never stops retrying. If the server is down for an extended period, the client retries indefinitely.

**Impact:** Battery drain on mobile. Unnecessary network requests.

**Fix:** Add a max retry count (e.g., 20 retries ≈ 5 minutes of attempts). After that, show a "Session disconnected — tap to retry" UI instead of silent retries.

---

## Key Invariant: Strict Turn Alternation

The state machine enforces strict interviewer/candidate alternation. `end_turn` is rejected unless state is `WaitingForInput`, and the pipeline check (`turnResultCh != nil`) blocks concurrent turns. The DB message sequence is always `I, C, I, C, ...` starting with the interviewer's opening question.

This invariant makes C2/C5 gap detection trivial:
```
last message role == "candidate" AND session status == "active"
→ re-trigger interviewer response
```

If the session is `completed` or `cancelled`, the gap was intentional (session ended before the interviewer could respond).

---

## Implementation Grouping

Four PRs, grouped by coupling. PRs 1 and 2 can run in parallel (backend vs frontend, no shared files).

### PR 1: Backend data integrity (C1, C2/C5, L1, M2)
All touch `conductor.go`, tightly related to turn lifecycle. C2/C5 is the meaty one; C1, L1, M2 are small additions while we're in the file.

### PR 2: Client resilience (C3, C4, L3)
All frontend, all connection/lifecycle. Independent of PR 1.

### PR 3: Observability + polish (M1, M3, M4, M5, M6)
Small independent fixes across backend and frontend. Batched because each is <10 lines.

### PR 4: Test coverage (L2)
Tests written against the final state, after PRs 1-3 land.

---

## Priority Matrix

| ID | Severity | Effort | PR | Recommendation |
|----|----------|--------|----|----------------|
| C1 | Critical | XS | 1 | `context.WithoutCancel` for candidate persist |
| C2/C5 | Critical | S | 1 | Re-trigger interviewer response on reconnect gap |
| L1 | Low | XS | 1 | `defer recover()` with structured logging |
| M2 | Medium | XS | 1 | Single STT retry with 500ms backoff |
| C3 | High | S | 2 | Auto-stop MediaRecorder on disconnect, preserve segments |
| C4 | High | S | 2 | `visibilitychange` handler: health check, resume audio |
| L3 | Low | XS | 2 | Max retry count (~20), then manual retry UI |
| M1 | Medium | S | 3 | Track upload result, notify client on failure |
| M3 | Medium | XS | 3 | Reduce TTSSentenceTimeout to 10s |
| M4 | Medium | XS | 3 | Log synthesis failures at Warn |
| M5 | Low-Med | XS | 3 | Thread accumulator context into ttsSink |
| M6 | Low | XS | 3 | Deferred `fanOut.Close()` after OnDone |
| L2 | Low | M | 4 | 5 remaining async pipeline tests |
