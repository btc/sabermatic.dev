# Remove state_change WS Messages — Derive State from Data Messages

**Issue:** btc/drill#126  
**Date:** 2026-04-07

## Summary

Remove the `state_change` message type from the WebSocket protocol entirely. All frontend `InterviewState` transitions are derivable from the data messages the client already receives. This eliminates redundant messages, removes a stuck-state bug on error paths, and simplifies the conductor.

## Background

Every `state_change` the conductor sends is redundant:

| State | Already derivable from |
|-------|----------------------|
| `transcribing` | Client submitted voice audio (already set client-side in `sendAudio`) |
| `processing` | Client submitted text (already set client-side in `sendText`) or `transcription_result` received |
| `streaming` | First `interviewer_token` received |
| `waiting` | `interviewer_done` received (after reorder — see below) |

Additionally, the current error path has a latent bug: when the LLM fails mid-stream, `InterviewerDone` is never sent, the `state_change "waiting_for_input"` that follows it in the success path is never sent, and the client is permanently stuck in `"streaming"` state. The `error` message is sent but the frontend handler only shows a toast — it does not reset state.

## Protocol Changes

### Removed

- `state_change` message type removed entirely from the WS protocol.

### `interviewer_done` — semantics change (wire format unchanged)

Currently sent when the LLM stream ends, before DB persist. After this change, sent after DB persist and after the conductor transitions to `WaitingForInput`. It becomes the authoritative "ready for input" signal in the happy path. It is only emitted on the success path — if DB persist fails, `InterviewerDone` is not sent; the conductor sends `error` instead, which drives the frontend to `"waiting"`.

### `reconnect_state` — gains `state` field

```
{ type: "reconnect_state", messages: [...], state: "waiting" }
```

The conductor always emits `state: "waiting"` because `sendInitialMessage()` only runs at conductor startup, at which point the state machine is always in `StateWaitingForInput`. This replaces the `state_change "waiting_for_input"` that currently follows `ReconnectState` in `sendInitialMessage()`.

The `state` field type is `"waiting"` (literal) on the wire. The frontend `protocol.ts` type is `state: "waiting"` — narrow type, not a general string, since no other value is reachable under current architecture.

### Full state derivation table

| Signal | Frontend state | Source |
|--------|---------------|--------|
| Voice audio submitted | `transcribing` | client-side (`sendAudio` — already implemented) |
| Text submitted | `processing` | client-side (`sendText` — already implemented) |
| `transcription_result` received | `processing` | server message |
| First `interviewer_token` received | `streaming` | server message |
| `interviewer_done` received | `waiting` | server message |
| `error` received | `waiting` + toast | server message |
| `reconnect_state` received | `"waiting"` | server message |

## Deployment

Backend and frontend changes must be deployed atomically. The frontend depends on `interviewer_done` and `error` driving state transitions that are currently driven by `state_change`. Deploying either side alone would leave the UI stuck.

## Backend Changes

### `internal/interview/transport/client.go`

- Remove `StateChange(state string)` from the `Client` interface.
- Add `State string` field to `ReconnectState` struct.

### `internal/interview/transport/ws_client.go`

- Remove `StateChange` method implementation.
- Add `"state": r.State` to the `reconnect_state` JSON payload.

### `internal/interview/transport/recorder.go`

- Remove `StateChangeEvent` struct.
- Remove `StateChange` method and its `StateChangeEvent` append.
- Remove `StateChange` from the `Recorded` struct and any switch/case reading it.

### `internal/interview/transport/token_writer.go`

- Remove `c.client.InterviewerDone(messageID)` from `OnDone()`. The conductor calls it after DB persist instead.
- The existing unconditional `fanOut.OnDone(fullText)` call in `streamInterviewerResponse()` (called even after `fanOut.OnError`) is intentionally left in place — with `InterviewerDone` removed from `token_writer.OnDone`, this call is harmless on the error path.

### `internal/interview/conductor.go`

Five `StateChange` call sites removed:

| Location | Current call | Action |
|----------|-------------|--------|
| `sendInitialMessage()` | `StateChange("waiting_for_input")` | Remove; replaced by `ReconnectState.State = "waiting"` |
| `endTurn()` | `StateChange("transcribing")` | Remove; already set client-side on voice submit |
| `endTurn()` | `StateChange("processing_input")` | Remove; client sets on `transcription_result` |
| `streamInterviewerResponse()` | `StateChange("interviewer_speaking")` | Remove; client sets on first `interviewer_token` |
| `streamInterviewerResponse()` | `StateChange("waiting_for_input")` | Remove; replaced by reordered `InterviewerDone` |

Additional changes:

- `streamInterviewerResponse()`: after DB persist and `sm.Transition(StateWaitingForInput)`, call `c.client.InterviewerDone(messageID)` (moved from token observer). This is the success path only — DB errors return before this call.
- `sendInitialMessage()`: set `ReconnectState.State = "waiting"` (always, since `sendInitialMessage` runs before any pipeline starts).
- Error path (`turnResultCh` error handler): no change — `c.client.Error(...)` is already sent; frontend now resets to `"waiting"` on any `error` message.

### `internal/interview/state_machine.go`

No changes. `StateEnding` and `StateEnded` are used for internal conductor state machine transitions in `endSession`/`cancelSession` — they prevent new turns from being accepted during shutdown and are never broadcast to the client.

## Frontend Changes

### `web/src/ws/protocol.ts`

- Remove `state_change` from `ServerMessage` union type.
- Add `state: "waiting"` (narrow literal type) to the `reconnect_state` message type.

### `web/src/ws/hooks.ts`

Message handler updates only — submit-path `setState` calls already exist:

| Message | Change |
|---------|--------|
| `state_change` | Remove case entirely |
| `reconnect_state` | Replace hardcoded `setState("waiting")` with `setState(msg.state)` |
| `transcription_result` | Add `setState("processing")` |
| `interviewer_token` | Add `setState("streaming")` (idempotent on every token — no first-token guard needed) |
| `interviewer_done` | Add `setState("waiting")` |
| `error` | Add `setState("waiting")` alongside existing `setLastError` |

## Testing

### Backend

- Remove `StateChange` from `transport.Client` mock implementations.
- Remove `TestWSClient_StateChange` from `internal/interview/transport/ws_client_test.go`.
- Conductor unit tests: remove `StateChange` assertions; add assertion that `InterviewerDone` is sent after DB persist (timing regression guard); add test that DB persist failure does not emit `InterviewerDone`.
- Error path test: after `turnResultCh` returns an error, assert no `InterviewerDone` is sent and conductor is in `StateWaitingForInput`.
- Integration tests (`internal/handler/session_ws_test.go`): remove all `state_change` message expectations. Synchronization barriers that currently use `drainUntilType(t, ws, "state_change")` must be replaced with `drainUntilType(t, ws, "interviewer_done")` or the equivalent. `TestWS_PageRefreshReconnect` must assert `reconnect_state.state == "waiting"` instead of a separate `state_change` message.
- Review `TestWS_DisconnectWithPendingCancel` and `TestWS_CancelDuringPipeline_PipelineCompletes` for `state_change` synchronization points.
- Run full integration test suite after implementation.
- Run cross-package code coverage after implementation.

### Frontend

- Remove `state_change` cases from any message-handling tests.
- Add: `error` message drives state to `"waiting"` (regression for stuck-state bug).
- Add: `interviewer_done` drives state to `"waiting"`.
- Add: `transcription_result` drives state to `"processing"`.
