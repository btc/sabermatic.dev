# Unary SSE Interview Service

Replace the WebSocket-based conductor with a stateless ConnectRPC `InterviewService`. Each RPC is independent — no long-lived server process per session. Session state lives in Postgres. Concurrency control via a status column, not advisory locks.

## Motivation

1. **Operational complexity.** WebSocket connections are hard to route, observe, and debug. Load balancer stickiness, proxy timeouts, and connection lifecycle create production issues.
2. **Code complexity.** The conductor event loop multiplexes 5 concurrent channels, coordinates a read-loop goroutine, manages pending actions, and implements a reconnection protocol. Hard to reason about, hard to evolve.
3. **Advisory lock flakiness.** Tying a Postgres advisory lock to a network connection's lifetime is inherently fragile. Connection drops leave stale locks.

The industry has converged on SSE over HTTP POST for LLM token streaming (Claude.ai, ChatGPT, Gemini). WebSockets add bidirectional capability that token streaming doesn't need, while introducing complexity around proxy traversal, connection management, and load balancer stickiness.

## RPCs

All RPCs require authentication. The handler verifies session ownership via the authenticated user before proceeding.

### SubmitTurn (server-streaming)

```protobuf
rpc SubmitTurn(SubmitTurnRequest) returns (stream TurnEvent);

enum InputMethod {
  INPUT_METHOD_UNSPECIFIED = 0;
  TEXT = 1;
  VOICE = 2;
}

message SubmitTurnRequest {
  string session_id = 1;
  InputMethod input_method = 2;
  string content = 3;              // text content (when input_method = TEXT)
  bytes audio = 4;                 // audio bytes (when input_method = VOICE)
  string traceparent = 5;          // OpenTelemetry traceparent header
  string audio_mime = 6;           // MIME type of audio (e.g. "audio/webm")
}

message TurnEvent {
  oneof event {
    TranscriptionResult transcription_result = 1;
    InterviewerToken token = 2;
    InterviewerDone done = 3;
    TTSChunk tts_chunk = 4;
    TTSDone tts_done = 5;
    TurnError error = 6;
  }
}
```

Behavior:
- Before acquiring generating status, check if the last persisted message is from the candidate (no interviewer response). If so, this is crash recovery — skip candidate persistence and proceed directly to interviewer response generation.
- Sets session status to `"generating"` at start via `UPDATE sessions SET status = 'generating', generating_since = now() WHERE id = $1 AND status = 'active' RETURNING id`. If no row returned, reject with error (session not active or already generating).
- Loads question, messages, coach briefing from DB (full reload each turn, no accumulated state).
- If voice: transcribe with single retry (500ms delay), stream `transcription_result`.
- Persist candidate message.
- Fire background goroutine to upload raw audio to object storage and set `audio_url` on the persisted message. This runs independently of the pipeline and must not block turn progression.
- Build LLM prompt from loaded messages, stream response with fan-out to token sink + TTS accumulator.
- Persist interviewer message.
- Set status back to `"active"`.
- Pipeline runs on `context.Background()` — survives client disconnect, always persists result.
- On failure at any point: status reverts to `"active"`, no partial message persisted, error event streamed if client still connected.

### GetSessionState (unary)

```protobuf
rpc GetSessionState(GetSessionStateRequest) returns (GetSessionStateResponse);

message GetSessionStateRequest {
  string session_id = 1;
  optional int32 after_seq = 2;   // last message seq the client has seen
}

message GetSessionStateResponse {
  SessionInfo session_info = 1;    // question, duration_minutes, tts_enabled, started_at
  repeated Message messages = 2;   // messages after cursor, or all if no cursor
  string status = 3;               // "active", "generating", "completed", "cancelled"
}
```

Behavior:
- Always returns immediately, never blocks.
- If `after_seq` provided, returns only messages with seq greater than the given value.
- Client polls with exponential backoff when status is `"generating"` (see Polling Backoff below).

### EndSession (unary)

```protobuf
rpc EndSession(EndSessionRequest) returns (EndSessionResponse);

message EndSessionRequest {
  string session_id = 1;
}

message EndSessionResponse {}
```

Behavior:
- Blocks until any in-flight turn completes (polls status every 500ms until not `"generating"`, timeout after 60 seconds).
- If `generating_since` exceeds the crash recovery threshold (5 minutes) during the polling loop, performs inline crash recovery (resets status to `"active"`) rather than waiting for the periodic cleanup.
- Derives turn count from `SELECT COUNT(*) FROM messages WHERE session_id = $1 AND role = 'candidate'`.
- Sets status `"completed"`, records turn count, refunds unused minutes, enqueues `evaluate_session` job. All in one transaction.
- Returns success/error.

### CancelSession (unary)

```protobuf
rpc CancelSession(CancelSessionRequest) returns (CancelSessionResponse);

message CancelSessionRequest {
  string session_id = 1;
}

message CancelSessionResponse {}
```

Behavior:
- Blocks until any in-flight turn completes (polls status every 500ms until not `"generating"`, timeout after 60 seconds).
- If `generating_since` exceeds the crash recovery threshold (5 minutes) during the polling loop, performs inline crash recovery (resets status to `"active"`) rather than waiting for the periodic cleanup.
- Derives turn count from `SELECT COUNT(*) FROM messages WHERE session_id = $1 AND role = 'candidate'`.
- Sets status `"cancelled"` + archived, full refund, no evaluation. All in one transaction.
- Returns success/error.

## Opening Question

The first turn in a new session triggers the opening interviewer question. The client calls `SubmitTurn` with `input_method = TEXT` and empty `content`. `ExecuteTurn` detects zero existing messages and skips candidate message persistence, proceeding directly to interviewer response generation (the opening question).

This reuses the same pipeline for all turns — no special "start session" RPC. The opening question is just a turn with no candidate input.

## Backend ExecuteTurn

The conductor's turn execution pipeline moves into the backend. The backend absorbs LLM, STT, and TTS clients into its constructor and lifecycle.

```go
type TurnEventSink interface {
    TranscriptionResult(e *pb.TranscriptionResult)
    InterviewerToken(e *pb.InterviewerToken)
    InterviewerDone(e *pb.InterviewerDone)
    TTSChunk(e *pb.TTSChunk)
    TTSDone(e *pb.TTSDone)
    Error(e *pb.TurnError)
}

func (b *Backend) ExecuteTurn(ctx context.Context, p *pb.SubmitTurnRequest, sink TurnEventSink) error
```

The sink interface uses proto types directly. The backend already imports proto types for db-to-proto conversions, and the sink is purpose-built for serving the RPC. The ConnectRPC handler wraps its `connect.ServerStream` to satisfy the interface:

```go
func (s *InterviewServer) SubmitTurn(
    ctx context.Context,
    req *connect.Request[pb.SubmitTurnRequest],
    stream *connect.ServerStream[pb.TurnEvent],
) error {
    sink := &connectTurnSink{stream: stream}
    return s.backend.ExecuteTurn(ctx, req.Msg, sink)
}
```

The `connectTurnSink` must silently drop writes that fail due to client disconnect. The pipeline must not abort on sink write errors — only on LLM/STT/TTS/DB errors. This matches the current `WSClient.send` pattern which logs and continues on write failure.

Internally, `ExecuteTurn`:
1. Checks for crash recovery (last message from candidate with no interviewer response — skip to step 6).
2. Acquires generating status (atomic UPDATE, rejects if not active).
3. Loads session, question, messages, coach briefing from DB.
4. If voice: transcribes audio (single retry), sinks `TranscriptionResult`.
5. Persists candidate message. Fires background goroutine to upload audio to object storage.
6. Builds LLM prompt from loaded messages + question + coach briefing + remaining duration.
7. Streams LLM response with fan-out to token sink + TTS accumulator (existing observer pattern).
8. Persists interviewer message.
9. Sets status back to `"active"`.
10. On any error: reverts status to `"active"`, sinks error, returns.

The observer/fan-out pattern (`TokenFanOut`, `TTSAccumulator`, `MessageAccumulator`) carries over from the current conductor. The `TokenWriter` and `TTSSink` adapters will be substantially rewritten to satisfy the `TurnEventSink` interface, which uses proto message types rather than plain strings and byte slices.

## Concurrency Control

### Status column

Add to `sessions` table:
- `generating_since TIMESTAMPTZ` — set when entering `"generating"` state.

The `status` column already exists with values `"active"`, `"completed"`, `"cancelled"`. Add `"generating"` as a valid value.

### Turn rejection

`ExecuteTurn` atomically sets status to `"generating"`:

```sql
UPDATE sessions
SET status = 'generating', generating_since = now()
WHERE id = $1 AND status = 'active'
RETURNING id;
```

No row returned = concurrent turn or inactive session. Return an error.

### Crash recovery

Two mechanisms:

1. **Periodic cleanup** (every 60 seconds): any session stuck in `"generating"` for >5 minutes gets reset to `"active"`. This handles the case where a Cloud Run instance dies mid-pipeline without setting status back.

```sql
UPDATE sessions
SET status = 'active', generating_since = NULL
WHERE status = 'generating' AND generating_since < now() - interval '5 minutes';
```

2. **Inline recovery in EndSession/CancelSession**: if the polling loop observes `generating_since` exceeding the 5-minute threshold, it performs the same reset inline rather than waiting for the periodic cleanup. This eliminates the timing gap where a crash at second 1 of the polling window would otherwise require waiting until second 61 for periodic cleanup.

The existing `CleanupAbandonedSessionsWorker` handles sessions stuck in `"active"` for too long (duration + grace period). These two cleanups compose: crash recovery converts stale `"generating"` to `"active"`, and abandoned session cleanup handles stale `"active"` sessions.

## Client Flows

### Normal turn

1. Page load: `GetSessionState()` — render conversation, start client-side timer.
2. For a new session (0 messages): `SubmitTurn(TEXT, "")` — triggers opening interviewer question.
3. User records/types, submits.
4. `SubmitTurn()` — stream renders tokens, plays TTS chunks as they arrive.
5. Stream closes on `interviewer_done` + `tts_done` — update local messages, ready for next turn.

### Page refresh mid-turn

1. `GetSessionState()` — returns prior messages + status `"generating"`.
2. Client renders full conversation history + loading indicator.
3. Poll `GetSessionState()` with exponential backoff until status is `"active"`.
4. New message appears in response, render it.

### End session

1. Client calls `EndSession()`.
2. RPC blocks until in-flight turn completes.
3. Response confirms completion. Client navigates to results.

### Cancel session

1. Client calls `CancelSession()`.
2. RPC blocks until in-flight turn completes.
3. Response confirms cancellation. Client navigates away.

### Server shutdown (deploy)

1. Cloud Run sends SIGTERM.
2. Server stops accepting new requests.
3. In-flight `SubmitTurn` pipelines continue on `context.Background()`.
4. If pipeline completes within graceful shutdown period: stream closes cleanly, status set to `"active"`.
5. If instance killed before pipeline finishes: pipeline is lost. `generating_since` timeout triggers crash recovery (either periodic or inline via EndSession/CancelSession). Client polls `GetSessionState`, eventually sees `"active"` (with or without the completed message depending on how far the pipeline got).

## Polling Backoff

When `GetSessionState` returns status `"generating"`, the client polls with exponential backoff tuned for ~4 second average pipeline duration. These are inter-poll delays; actual wall-clock time includes network round-trip and server processing.

```
delay = min(300 * 2^attempt, 2000) ms
```

| Attempt | Delay | Cumulative |
|---------|-------|------------|
| 0 | 300ms | 300ms |
| 1 | 600ms | 900ms |
| 2 | 1200ms | 2.1s |
| 3 | 2000ms | 4.1s |
| 4+ | 2000ms (cap) | +2s each |

Front-loads checks during the window where completion is most likely. By 2.1 seconds (3 polls), covers the median case. 2-second cap prevents over-polling on outlier long generations.

## Timers

Client-side. `GetSessionState` returns `duration_minutes` and `started_at`. The client computes warning and overtime thresholds locally. No server push needed.

Timer thresholds (same as current conductor logic):
- Warning: 5 minutes remaining (or 2 minutes for sessions <= 10 minutes).
- Overtime: 0 minutes remaining.
- Auto-end: client calls `EndSession` 2 minutes after overtime.

## TTS in the Turn Stream

TTS chunks are interleaved in the `SubmitTurn` response stream alongside LLM tokens. The existing sentence-boundary accumulator buffers tokens until sentence boundaries, synthesizes each sentence, and streams audio chunks via the sink.

Voice is the primary user experience; text is secondary context. Keeping TTS in the turn stream ensures the first audio chunk plays as soon as the first sentence is synthesized, while the LLM is still generating. Decoupling TTS into a separate fetch would add a round trip and regress latency.

TTS errors are conveyed through the `TurnError` event with a specific error code (e.g. `"tts_failed"`). A TTS failure does not abort the turn — text tokens continue streaming. The client falls back to text-only display for that turn.

The stream event sequence for a typical turn:

```
→ transcription_result {text: "The time complexity is O(n log n)..."}
→ interviewer_token {token: "That's"}
→ interviewer_token {token: " correct."}
→ tts_chunk {data: <bytes>, message_id: "...", seq: 1}
→ interviewer_token {token: " The"}
→ interviewer_token {token: " key"}
→ interviewer_token {token: " insight..."}
→ tts_chunk {data: <bytes>, message_id: "...", seq: 2}
→ interviewer_done {message_id: "..."}
→ tts_done {message_id: "..."}
```

## Audio Upload

Inline in the `SubmitTurn` request body. Client joins recorded segments locally and includes the audio as a `bytes` field in the proto message with `audio_mime` specifying the format. The server receives the full audio before transcription begins.

Latency sequence: user stops recording → join segments → upload blob → transcribe → LLM → first token.

For typical interview audio (<30 seconds of speech, ~200-500KB), upload is sub-second on any reasonable connection. Progressive segment upload (uploading segments as they're recorded) is a future optimization if upload latency becomes a measurable bottleneck.

After transcription, a background goroutine uploads the raw audio to object storage and sets `audio_url` on the persisted message. This preserves intermediate data without blocking the pipeline.

## Wire Format

ConnectRPC server-streaming with JSON wire format. Provides type-safe generated clients from proto definitions while remaining human-readable in browser devtools and curl.

Proto definitions in `pb/`. Generated code via `buf generate` into `internal/pb/` (Go) and `web/src/pb/` (TypeScript).

## What Gets Deleted

- `internal/handler/session_ws.go` — WebSocket upgrade handler.
- `internal/interview/conductor.go` — event loop, read loop, pending actions, reconnection protocol.
- `internal/interview/state_machine.go` — conductor state machine.
- `internal/interview/transport/ws_client.go` — WebSocket JSON send layer.
- `internal/backend/session_lock.go` — Postgres advisory lock.
- `web/src/ws/connection.ts` — WebSocket connection manager, retry logic, keepalive.
- `web/src/ws/protocol.ts` — WebSocket message type definitions.
- `web/src/ws/hooks.ts` — WebSocket React hooks for interview state.

## What Gets Kept and Moved

- **Observer pattern** (`observer/fan_out.go`, `observer/tts_accumulator.go`, `observer/message_accumulator.go`) — used by `ExecuteTurn`, adapted to sink via `TurnEventSink`.
- **Transport sinks** (`transport/token_writer.go`, `transport/tts_sink.go`) — substantially rewritten to implement `TurnEventSink` with proto types instead of plain Go types.
- **Prompt building** (`prompt/`) — unchanged, called from `ExecuteTurn`.
- **LLM streaming, STT, TTS clients** — absorbed into backend constructor and lifecycle.
- **Crash recovery for missing interviewer response** (`needsInterviewerRecovery`) — logic moves into `ExecuteTurn` as a pre-check: if last message is from candidate, skip to interviewer response generation.
