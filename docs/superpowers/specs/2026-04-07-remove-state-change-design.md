# Remove state_change WS Messages — Derive State from Data Messages

**Issue:** btc/drill#126  
**Date:** 2026-04-07

## Summary

Remove the `state_change` message type from the WebSocket protocol entirely. All frontend `InterviewState` transitions are derivable from the data messages the client already receives. This eliminates redundant messages, removes a stuck-state bug on error paths, and simplifies the conductor.

## Background

Every `state_change` the conductor sends is redundant:

| State | Already derivable from |
|-------|----------------------|
| `transcribing` | Client submitted voice audio (client-side) |
| `processing` | Client submitted text (client-side) or `transcription_result` received |
| `streaming` | First `interviewer_token` received |
| `waiting` | `interviewer_done` received (after reorder — see below) |

Additionally, the current error path has a latent bug: when the LLM fails mid-stream, `InterviewerDone` is never sent, the `state_change "waiting_for_input"` that follows it in the success path is never sent, and the client is permanently stuck in `"streaming"` state. The `error` message is sent but the frontend handler only shows a toast — it does not reset state.

## Protocol Changes

### Removed

- `state_change` message type removed entirely from the WS protocol.

### `interviewer_done` — semantics change (wire format unchanged)

Currently sent when the LLM stream ends, before DB persist. After this change, sent after DB persist and after the conductor transitions to `WaitingForInput`. It becomes the authoritative "ready for input" signal in the happy path.

### `reconnect_state` — gains `state` field

```
{ type: "reconnect_state", messages: [...], state: "waiting" | "processing" | "streaming" }
```

The conductor populates `state` from its current `ConductorState` when the client connects. This replaces the `state_change "waiting_for_input"` that currently follows `ReconnectState` in `sendInitialMessage()`.

Mapping from conductor state to reconnect state:

| ConductorState | reconnect_state.state |
|---|---|
| `StateWaitingForInput` | `"waiting"` |
| `StateTranscribing`, `StateProcessingInput` | `"processing"` |
| `StateInterviewerSpeaking` | `"streaming"` |

### Full state derivation table

| Signal | Frontend state | Source |
|--------|---------------|--------|
| Voice audio submitted | `transcribing` | client-side |
| Text submitted | `processing` | client-side |
| `transcription_result` received | `processing` | server message |
| First `interviewer_token` received | `streaming` | server message |
| `interviewer_done` received | `waiting` | server message |
| `error` received | `waiting` + toast | server message |
| `reconnect_state` received | `msg.state` | server message |

## Backend Changes

### `internal/interview/transport/client.go`

- Remove `StateChange(state string)` from the `Client` interface.
- Add `State string` field to `ReconnectState` struct.

### `internal/interview/transport/ws_client.go`

- Remove `StateChange` method implementation.
- Add `"state": r.State` to the `reconnect_state` JSON payload.

### `internal/interview/transport/token_writer.go`

- Remove `c.client.InterviewerDone(messageID)` from `OnDone()`. The conductor calls it after DB persist instead.

### `internal/interview/conductor.go`

Five `StateChange` call sites removed:

| Location | Current call | Action |
|----------|-------------|--------|
| `sendInitialMessage()` | `StateChange("waiting_for_input")` | Remove; state now in `ReconnectState.State` |
| `endTurn()` | `StateChange("transcribing")` | Remove; client sets this on voice submit |
| `endTurn()` | `StateChange("processing_input")` | Remove; client sets this on transcription_result |
| `streamInterviewerResponse()` | `StateChange("interviewer_speaking")` | Remove; client sets this on first token |
| `streamInterviewerResponse()` | `StateChange("waiting_for_input")` | Remove; replaced by reordered `InterviewerDone` |

Additional changes:

- `streamInterviewerResponse()`: after DB persist and `sm.Transition(StateWaitingForInput)`, call `c.client.InterviewerDone(messageID)` (moved from token observer).
- `sendInitialMessage()`: populate `ReconnectState.State` from current conductor state using the mapping above.
- Error path (L297): no change — `c.client.Error(...)` is already sent; frontend now resets to `"waiting"` on any `error` message.

### `internal/interview/state_machine.go`

- Remove `StateEnding` and `StateEnded` constants (already unused by the client since the ack protocol).

## Frontend Changes

### `web/src/ws/protocol.ts`

- Remove `state_change` from `ServerMessage` union type.
- Add `state: "waiting" | "processing" | "streaming"` to the `reconnect_state` message type.

### `web/src/ws/hooks.ts`

Message handler updates:

| Message | Change |
|---------|--------|
| `state_change` | Remove case entirely |
| `reconnect_state` | `setState(msg.state)` instead of hardcoded `setState("waiting")` |
| `transcription_result` | Add `setState("processing")` |
| `interviewer_token` | Add `setState("streaming")` (idempotent on repeated calls) |
| `interviewer_done` | Add `setState("waiting")` |
| `error` | Add `setState("waiting")` alongside existing `setLastError` |

Submit path updates (client-side state, no server message):

- `sendAudio`: add `setState("transcribing")` before sending.
- `sendText`: add `setState("processing")` before sending.

## Testing

### Backend

- Remove `StateChange` from the `transport.Client` mock and all test mock implementations.
- Conductor unit tests: remove `StateChange` assertions; add assertion that `InterviewerDone` is sent after DB persist (timing regression guard).
- Error path test: after `turnResultCh` returns an error, assert no `InterviewerDone` is sent and conductor is in `StateWaitingForInput`.
- Integration tests: remove `state_change` message expectations from all message streams; add `reconnect_state.state` field assertions.
- Review `disconnect-with-pending-cancel` and `pipeline-completes-on-cancel` tests for any `state_change` expectations.
- Run full integration test suite after implementation.
- Run cross-package code coverage after implementation.

### Frontend

- Remove `state_change` cases from any message-handling tests.
- Add: `error` message drives state to `"waiting"` (regression for stuck-state bug).
- Add: `interviewer_done` drives state to `"waiting"`.
- Add: `transcription_result` drives state to `"processing"`.
- Add: `reconnect_state` with `state: "streaming"` leaves client in `"streaming"` until `interviewer_done` arrives.
