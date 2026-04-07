# Unary SSE Interview Service

Replace the WebSocket-based conductor with a stateless ConnectRPC `InterviewService`. Each RPC is independent — no long-lived server process per session. Session state lives in Postgres. Concurrency control via a status column, not advisory locks.

## Motivation

1. **Operational complexity.** WebSocket connections are hard to route, observe, and debug. Load balancer stickiness, proxy timeouts, and connection lifecycle create production issues.
2. **Code complexity.** The conductor event loop multiplexes 5 concurrent channels, coordinates a read-loop goroutine, manages pending actions, and implements a reconnection protocol. Hard to reason about, hard to evolve.
3. **Advisory lock flakiness.** Tying a Postgres advisory lock to a network connection's lifetime is inherently fragile. Connection drops leave stale locks.

The industry has converged on SSE over HTTP POST for LLM token streaming (Claude.ai, ChatGPT, Gemini). WebSockets add bidirectional capability that token streaming doesn't need, while introducing complexity around proxy traversal, connection management, and load balancer stickiness.

## RPCs

All RPCs require authentication. The handler verifies session ownership via the authenticated user before proceeding. Field naming follows Google AIP conventions.

### SubmitTurn (server-streaming)

```protobuf
rpc SubmitTurn(SubmitTurnRequest) returns (stream TurnEvent);

message SubmitTurnRequest {
  string session_id = 1;
  string traceparent = 2;          // OpenTelemetry traceparent header

  oneof input {
    TextInput text_input = 3;
    VoiceInput voice_input = 4;
  }
}

message TextInput {
  string content = 1;
}

message VoiceInput {
  bytes audio = 1;
  string audio_mime_type = 2;      // e.g. "audio/webm"
}

message TurnEvent {
  oneof event {
    TranscriptionResult transcription_result = 1;
    InterviewerToken interviewer_token = 2;
    InterviewerDone interviewer_done = 3;
    TtsChunk tts_chunk = 4;
    TtsDone tts_done = 5;
    TurnError error = 6;
  }
}
```

Behavior:
- Sets session status to `"generating"` at start via `UPDATE sessions SET status = 'generating', generating_since = now() WHERE id = $1 AND status = 'active' RETURNING id`. If no row returned, reject with error (session not active or already generating).
- Loads session, question, messages, coach briefing from DB in a single query where feasible (full reload each turn, no accumulated state).
- Validates `audio_mime_type` against supported formats (e.g. `audio/webm`, `audio/mp4`) when `voice_input` is set. Rejects with a descriptive error if unsupported.
- Determines turn type:
  - **Opening question**: zero messages and `text_input` with empty content → skip transcription and candidate persistence, proceed directly to interviewer response generation.
  - **Crash recovery**: last message from candidate with no interviewer response → skip transcription and candidate persistence, proceed directly to interviewer response generation.
  - **Normal turn**: proceed with transcription (if voice) and candidate persistence.
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
  string page_token = 2;          // opaque cursor; omit for full state
}

message GetSessionStateResponse {
  SessionInfo session_info = 1;
  repeated Message messages = 2;   // messages after cursor, or all if no cursor
  SessionStatus status = 3;
  string next_page_token = 4;     // cursor representing the last message returned
}

enum SessionStatus {
  SESSION_STATUS_UNSPECIFIED = 0;
  ACTIVE = 1;
  GENERATING = 2;
  COMPLETED = 3;
  CANCELLED = 4;
  FAILED = 5;                      // backend-only (crash/error); client treats as terminal
}
```

Behavior:
- Always returns immediately, never blocks.
- If `page_token` provided, returns only messages after that cursor. The cursor is opaque to the client (server encodes/decodes it internally — initially backed by message count or created_at, but the opacity allows changing the implementation).
- `next_page_token` is set to a cursor representing the last message returned, so the client can pass it back on the next call.
- Client polls with exponential backoff when status is `GENERATING` (see Polling Backoff below).

### EndSession (unary)

```protobuf
rpc EndSession(EndSessionRequest) returns (EndSessionResponse);

message EndSessionRequest {
  string session_id = 1;
}

message EndSessionResponse {}
```

Behavior:
- Blocks until any in-flight turn completes (polls status every 500ms until not `GENERATING`, timeout after 60 seconds).
- If `generating_since` exceeds the crash recovery threshold (5 minutes) during the polling loop, performs inline crash recovery via `UPDATE sessions SET status = 'active', generating_since = NULL WHERE id = $1 AND status = 'generating' AND generating_since < now() - interval '5 minutes'`. The conditional WHERE clause makes this idempotent and race-free across instances.
- Completes the session in a single transaction: counts interviewer messages as turn count, sets status `"completed"`, refunds unused minutes, enqueues `evaluate_session` job. Prefer a combined SQL query over multiple round trips.
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
- Blocks until any in-flight turn completes (polls status every 500ms until not `GENERATING`, timeout after 60 seconds).
- If `generating_since` exceeds the crash recovery threshold (5 minutes) during the polling loop, performs inline crash recovery via the same conditional UPDATE as EndSession (idempotent, race-free).
- Cancels the session in a single transaction: counts interviewer messages as turn count, sets status `"cancelled"` + archived, full refund. No evaluation enqueued.
- Returns success/error.

## Opening Question

The first turn in a new session triggers the opening interviewer question. The ready page fires `SubmitTurn` with `text_input` containing empty content in the background as soon as the page loads. The stream delivers the interviewer's opening message and TTS audio. By the time the user clicks "Ready" (or equivalent), the opening message and audio are already available for instant playback.

This reuses the same pipeline for all turns — no special "start session" RPC. The opening question is just a turn with no candidate input. Normal empty-text validation does not apply to the opening question case (explicitly bypassed when zero messages are detected).

## Backend ExecuteTurn

The conductor's turn execution pipeline moves into the backend. The backend absorbs LLM, STT, and TTS clients into its constructor and lifecycle.

```go
type TurnEventSink interface {
    TranscriptionResult(e *pb.TranscriptionResult)
    InterviewerToken(e *pb.InterviewerToken)
    InterviewerDone(e *pb.InterviewerDone)
    TtsChunk(e *pb.TtsChunk)
    TtsDone(e *pb.TtsDone)
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

The `connectTurnSink` must silently drop writes that fail due to client disconnect (log at debug level, matching current `WSClient.send` behavior). The pipeline must not abort on sink write errors — only on LLM/STT/TTS/DB errors.

Internally, `ExecuteTurn`:
1. Acquires generating status (atomic UPDATE, rejects if not active).
2. Loads session, question, messages, coach briefing from DB. Prefer a single combined query where feasible to reduce round trips.
3. Determines turn type:
   - **Opening question**: zero messages, `text_input` with empty content → skip to step 6.
   - **Crash recovery**: last message from candidate with no interviewer response → skip to step 6.
   - **Normal turn**: proceed to step 4.
4. If voice: validates `audio_mime_type`, transcribes audio (single retry), sinks `TranscriptionResult`.
5. Persists candidate message. Fires background goroutine to upload audio to object storage.
6. Builds LLM prompt from loaded messages + question + coach briefing + remaining duration.
7. Streams LLM response with fan-out to token sink + TTS accumulator (existing observer pattern).
8. Persists interviewer message.
9. Sets status back to `"active"`.
10. On any error: reverts status to `"active"`, sinks error, returns.

The observer/fan-out pattern (`TokenFanOut`, `TTSAccumulator`, `MessageAccumulator`) carries over from the current conductor. The `TokenWriter` and `TTSSink` adapters will be substantially rewritten to emit events via `TurnEventSink` instead of the WebSocket `Client` interface.

## SQL Query Strategy

Prefer new combined sqlc queries over multiple round trips where semantics are unambiguous:

- **Load turn context**: single query joining session + question + coach briefing. Messages loaded separately (they're a collection).
- **Acquire generating status**: single conditional UPDATE returning the row (avoids SELECT + UPDATE race).
- **Complete/cancel session**: single query that counts interviewer messages, updates status, and inserts refund ledger entries. Enqueue evaluation job in the same transaction.
- **Crash recovery cleanup**: single UPDATE with WHERE clause on status + generating_since.

Write new sqlc queries rather than reusing existing ones that are slightly wrong for the new semantics.

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

1. **Periodic cleanup** via a new River periodic job (`cleanup_stale_generating`, every 60 seconds): any session stuck in `"generating"` for >5 minutes gets reset to `"active"`. This handles the case where a Cloud Run instance dies mid-pipeline without setting status back. Separate from the existing `cleanup_abandoned_sessions` worker which runs every 3 minutes.

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
2. For a new session (0 messages): fire `SubmitTurn(text_input: "")` in background on ready page. Stream delivers opening question tokens + TTS. Audio and text are cached so playback is instant when user clicks "Ready."
3. User records/types, submits.
4. `SubmitTurn()` — stream renders tokens, plays TTS chunks as they arrive.
5. Stream closes on `interviewer_done` + `tts_done` — update local messages, ready for next turn.

### Page refresh mid-turn

1. `GetSessionState()` — returns prior messages + status `GENERATING`.
2. Client renders full conversation history + loading indicator.
3. Poll `GetSessionState()` with exponential backoff until status is `ACTIVE`.
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
5. If instance killed before pipeline finishes: pipeline is lost. `generating_since` timeout triggers crash recovery (either periodic or inline via EndSession/CancelSession). Client polls `GetSessionState`, eventually sees `ACTIVE` (with or without the completed message depending on how far the pipeline got).

## Polling Backoff

When `GetSessionState` returns status `GENERATING`, the client polls with exponential backoff tuned for ~4 second average pipeline duration. These are inter-poll delays; actual wall-clock time includes network round-trip and server processing.

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

Inline in the `SubmitTurn` request body via the `voice_input` oneof. Client joins recorded segments locally and includes the audio as a `bytes` field with `audio_mime_type` specifying the format. The server receives the full audio before transcription begins.

Latency sequence: user stops recording → join segments → upload blob → transcribe → LLM → first token.

For typical interview audio (<30 seconds of speech, ~200-500KB), upload is sub-second on any reasonable connection. Progressive segment upload (uploading segments as they're recorded) is a future optimization if upload latency becomes a measurable bottleneck.

After transcription, a background goroutine uploads the raw audio to object storage and sets `audio_url` on the persisted message. This preserves intermediate data without blocking the pipeline.

## Wire Format

ConnectRPC server-streaming with JSON wire format. Provides type-safe generated clients from proto definitions while remaining human-readable in browser devtools and curl.

Proto definitions in `pb/`. Generated code via `buf generate` into `internal/pb/` (Go) and `web/src/pb/` (TypeScript).

## What Gets Deleted

- `internal/handler/session_ws.go` — WebSocket upgrade handler.
- `internal/handler/session_ws_test.go` — WebSocket handler tests.
- `internal/interview/conductor.go` — event loop, read loop, pending actions, reconnection protocol.
- `internal/interview/conductor_test.go` — conductor tests.
- `internal/interview/state_machine.go` — conductor state machine.
- `internal/interview/state_machine_test.go` — state machine tests.
- `internal/interview/transport/ws_client.go` — WebSocket JSON send layer.
- `internal/interview/transport/ws_message.go` — WebSocket message parsing.
- `internal/backend/session_lock.go` — Postgres advisory lock.
- `internal/backend/session_lock_test.go` — advisory lock tests.
- `web/src/ws/connection.ts` — WebSocket connection manager, retry logic, keepalive.
- `web/src/ws/protocol.ts` — WebSocket message type definitions.
- `web/src/ws/hooks.ts` — WebSocket React hooks for interview state.

## What Gets Kept and Moved

- **Observer pattern** (`observer/fan_out.go`, `observer/tts_accumulator.go`, `observer/message_accumulator.go`) — used by `ExecuteTurn`, adapted to sink via `TurnEventSink`.
- **Transport sinks** (`transport/token_writer.go`, `transport/tts_sink.go`) — substantially rewritten to emit events via `TurnEventSink` instead of the WebSocket `Client` interface.
- **Prompt building** (`prompt/`) — unchanged, called from `ExecuteTurn`.
- **LLM streaming, STT, TTS clients** — absorbed into backend constructor and lifecycle.
- **Crash recovery for missing interviewer response** (`needsInterviewerRecovery`) — logic moves into `ExecuteTurn` as a pre-check: if last message is from candidate, skip to interviewer response generation.
