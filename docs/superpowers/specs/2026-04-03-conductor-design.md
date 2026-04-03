# Conductor Phase — Design Spec

**Date**: 2026-04-03
**Status**: Approved
**Scope**: Backend only. State machine, WebSocket, STT/TTS, LLM streaming, session/question endpoints, rigorous WebSocket integration tests. No frontend.

---

## 1. Decisions

| Decision | Choice | Rationale |
|---|---|---|
| STT/TTS provider | Interface-abstracted, OpenAI initial impl | Swap later without touching conductor logic |
| LLM client scope | Full client (StreamAndLog + CallAndLog) | Shared cost/logging layer; blocking variant is trivial on top of streaming |
| Session creation | No entitlement checks | Phase 9 adds billing gates |
| Phase scope | Backend only, rigorous WS integration tests | WebSocket protocol is fully testable without a browser |
| Abandoned cleanup | Build now, TODO for EvaluateSession enqueue | Data hygiene independent of evaluation |
| Question endpoint | Build GET /api/questions | Conductor needs questions; tests need questions; expose the list |
| EvaluateSession on end | Stub worker, transactional enqueue | Proves correctness of atomic session-end pattern; Phase 7 replaces worker |
| WebSocket library | github.com/coder/websocket | Context-aware Read/Write, concurrent write safety, active maintenance |

---

## 2. Package Structure

```
internal/
  interview/
    conductor.go      # Conductor struct, Run loop, message dispatch
    statemachine.go   # StateMachine, pure logic, no I/O
    observer.go       # TokenObserver interface, fan-out, WSWriter, TTSAccumulator, MessageAccumulator
    prompt.go         # PromptBuilder for interviewer system prompt
  ai/
    client.go         # LLM client: StreamAndLog, CallAndLog, cost/logging persistence
    stt.go            # Transcriber interface + OpenAI implementation
    tts.go            # Synthesizer interface + OpenAI implementation
  handler/
    session.go        # POST /api/sessions, GET /api/sessions, GET /api/sessions/:id
    session_ws.go     # WSS /api/sessions/:id/ws — upgrade, read loop, conductor launch
    question.go       # GET /api/questions (list/filter)
  backend/
    session.go        # Session business logic (create, list, get, end, cleanup)
    question.go       # Question listing/retrieval
  jobs/
    evaluate.go       # Stub EvaluateSession worker (completes immediately)
    cleanup.go        # CleanupAbandonedSessions periodic job
sql/queries/
    sessions.sql      # sqlc queries for interview_sessions
    messages.sql      # sqlc queries for messages
    questions.sql     # (exists, may need additions)
    llm_calls.sql     # sqlc queries for llm_calls + llm_call_content
```

**New dependencies:**
- `github.com/coder/websocket` — WebSocket with context-aware API
- `github.com/openai/openai-go` — STT (Whisper) and TTS (verify exact module path at `go get` time)
- `github.com/anthropics/anthropic-sdk-go` — LLM streaming

**Package naming note:** The parent design spec references `internal/llm/` for the LLM client and places `stt.go` under `internal/interview/`. This spec consolidates all three (LLM, STT, TTS) into `internal/ai/` since they share external API client concerns. This is an intentional deviation — the parent spec's Section 15 project structure is superseded by this spec's Section 2 for these packages.

**Audio storage (GCS) is deferred.** The parent design spec describes writing audio to GCS. GCS bucket setup is a Deployment phase (Phase 5) dependency. In this phase, audio validation and STT happen in-memory, but audio files are not persisted to GCS. The `messages.audio_url` column will be NULL. Phase 5 or a follow-up adds the GCS upload path.

---

## 3. Wiring & Dependency Injection

The `Backend` struct gains new fields for AI dependencies, following the existing pattern where Backend owns all external clients:

```go
// Added to Backend struct
type Backend struct {
    // ... existing fields (pool, jobs, cfg)
    llm  *ai.Client
    stt  ai.Transcriber
    tts  ai.Synthesizer
}
```

Construction in `Backend.New()`:
- `ai.Client` — constructed from `cfg.LLM.APIKey` (Anthropic SDK) + `pool` (for llm_calls persistence)
- `OpenAITranscriber` — constructed from `cfg.Speech.OpenAIAPIKey` + `cfg.Speech.WhisperModel`
- `OpenAISynthesizer` — constructed from `cfg.Speech.OpenAIAPIKey` + `cfg.Speech.TTSModel` + `cfg.Speech.TTSVoice`

Backend exposes accessor methods (`LLM()`, `STT()`, `TTS()`, `Jobs()`, `Pool()`) that the WebSocket handler uses to build the Conductor. `Pool()` already exists; `Jobs()` and the AI accessors are new. This keeps the handler thin — it does not construct AI clients.

**Config:** Uses the existing `config.Speech` struct (already has `OpenAIAPIKey`, `WhisperModel`, `TTSModel`, `TTSVoice`) and `config.LLM` struct (already has `APIKey`, `InterviewerModel`). No new config structs needed.

---

## 4. Sentinel Errors

Following the pattern in `internal/backend/errors.go`:

```go
// backend/errors.go (additions)
var (
    ErrSessionNotFound    = fmt.Errorf("session not found")
    ErrSessionNotActive   = fmt.Errorf("session is not active")
    ErrSessionNotOwned    = fmt.Errorf("session does not belong to user")
    ErrQuestionNotFound   = fmt.Errorf("question not found")
    ErrInvalidDuration    = fmt.Errorf("duration must be between 1 and 180 minutes")
)

// interview/statemachine.go
var ErrInvalidTransition = fmt.Errorf("invalid state transition")

// interview/conductor.go
var (
    ErrAudioValidation = fmt.Errorf("audio validation failed")
    ErrTranscription   = fmt.Errorf("transcription failed")
)
```

Handlers use `errors.Is()` to map these to HTTP status codes (404, 403, 400, 422).

---

## 5. State Machine

Pure struct, no I/O, independently testable.

```go
type ConductorState string

const (
    StateInterviewerSpeaking ConductorState = "interviewer_speaking"
    StateWaitingForInput     ConductorState = "waiting_for_input"
    StateTranscribing        ConductorState = "transcribing"
    StateProcessingInput     ConductorState = "processing_input"
    StateEnding              ConductorState = "ending"
    StateEnded               ConductorState = "ended"
)
```

**Transition table:**

```
InterviewerSpeaking → WaitingForInput, Ending
WaitingForInput     → Transcribing, ProcessingInput, Ending
Transcribing        → ProcessingInput, Ending
ProcessingInput     → InterviewerSpeaking, Ending
Ending              → Ended
Ended               → (terminal)
```

**Fields:** `state ConductorState`, `turnCount int`, `startedAt time.Time`

`Transition(next)` validates against the map, returns error on invalid. Increments `turnCount` when entering `WaitingForInput` (counts completed turns).

---

## 6. Conductor Core

Single goroutine owns all mutable state for one active interview. Messages flow through one channel (`msgCh`), processed sequentially.

**WSConn interface:**

```go
type WSConn interface {
    SendJSON(ctx context.Context, v any) error
    Close(code websocket.StatusCode, reason string) error
}
```

Thin wrapper over `*websocket.Conn`. `SendJSON` marshals to JSON and calls `ws.Write(ctx, websocket.MessageText, data)`. The conductor writes through this interface; the read loop reads from the raw `*websocket.Conn` directly (it needs `ws.Read` which returns raw bytes). Concurrent writes are safe — `coder/websocket` handles internal synchronization.

**WSMessage type:**

```go
type WSMessage struct {
    Type        string          `json:"type"`
    LastSeq     *int            `json:"last_seq,omitempty"`      // session_init
    Content     string          `json:"content,omitempty"`       // end_turn (text)
    Audio       []byte          `json:"-"`                       // end_turn (voice), decoded from base64
    AudioBase64 string          `json:"audio,omitempty"`         // wire format for audio
    InputMethod string          `json:"input_method,omitempty"`  // "text" or "voice"
}
```

`parseWSMessage` unmarshals JSON, then decodes `AudioBase64` into `Audio` bytes if present. `LastSeq` is `*int` — nil means "send me everything" (new session), non-nil means "send messages after this seq" (reconnect).

**Struct:**

```go
type Conductor struct {
    sm          *StateMachine
    msgCh       chan WSMessage
    ws          WSConn
    pool        *pgxpool.Pool        // direct pool access (see Section 11 note)
    lockConn    *pgxpool.Conn        // dedicated conn holding advisory lock, released on exit
    jobs        backend.Jobs         // River client for transactional job enqueue
    llm         *ai.Client
    stt         ai.Transcriber
    tts         ai.Synthesizer       // nil if TTS disabled
    prompter    *PromptBuilder
    observer    *TokenFanOut         // current fan-out, nil between turns

    // Session state
    sessionID   uuid.UUID
    userID      uuid.UUID
    question    db.Question
    messages    []db.Message         // in-memory transcript for prompt building
    sequence    int                  // last message seq
    ttsEnabled  bool
    duration    time.Duration

    // Timers
    reconnectPending bool
    timerWarningCh   <-chan time.Time
    timerOvertimeCh  <-chan time.Time
    reconnectTimerCh <-chan time.Time
}
```

The `jobs` field uses the existing `backend.Jobs` interface (which has `InsertTx`). The WebSocket handler passes `b.Jobs()` when constructing the conductor.

**`Run(ctx context.Context)`:**

1. Load session from DB (or detect reconnect via existing messages)
2. Send `session_loaded` or `reconnect_state`
3. If new session, stream interviewer opening via `streamInterviewerResponse`
4. Select loop:
   - `case msg := <-c.msgCh` — dispatch to handler
   - `case <-c.timerWarningCh` — send `timer_warning`
   - `case <-c.timerOvertimeCh` — send `timer_overtime`, start 2-min auto-end countdown
   - `case <-c.reconnectTimerCh` — set `reconnectPending = true`
   - `case <-ctx.Done()` — `handleShutdown` (persist partial state, send `reconnect_please`)

**Message dispatch:**

- `end_turn` (voice): validate audio → `Transcribing` → STT → `ProcessingInput` → persist candidate message → build prompt → stream LLM → persist interviewer message → `WaitingForInput`
- `end_turn` (text): skip STT → `ProcessingInput` → same as above
- `end_session`: `Ending` → persist status `completed` + enqueue stub `EvaluateSession` in one tx → `Ended`
- `cancel_tts`: **never enters msgCh** — read loop calls `observer.Interrupt()` directly
- `ping`: reply `pong`

**In-memory transcript:** The conductor holds a `messages` slice that grows during the session. Avoids re-querying the full transcript from DB on every turn for prompt building. Messages appended after each persist.

**Timers:** All computed from `session.started_at` at initialization. On reconnect, recalculated from DB timestamps.
- `timerWarningCh` — fires at `duration - clamp(2, 5, round(duration/9))` minutes. The warning time formula matches the parent spec: for a 45-min session, warning at 40 min (5 min remaining); for a 10-min session, warning at 8 min (2 min remaining).
- `timerOvertimeCh` — fires at `duration` minutes. After firing, a 2-minute auto-end countdown begins (a new timer). If the candidate hasn't ended the session by then, the conductor auto-ends it.
- `reconnectTimerCh` — fires at 55 minutes into the WebSocket connection (Cloud Run's 60-minute request timeout). For sessions shorter than 55 minutes, this timer is not set. For sessions longer than 55 minutes, this enables preemptive reconnection — the client gets a fresh 60-minute Cloud Run window.

**Reconnect pending:** Checked after each turn completes (in the `WaitingForInput` transition path), not when the timer fires. Guarantees reconnection happens between turns — no mid-stream interruption, no partial state.

**Disconnect mid-stream:** When the read loop closes `msgCh` (client disconnect), the conductor detects `!ok` on the next channel receive. If the conductor is mid-LLM-stream at that point:
1. The LLM stream **continues to completion** — tokens accumulate in the MessageAccumulator but WSWriter writes fail silently (WebSocket is closed).
2. On stream completion, the conductor persists the full interviewer message to DB via the MessageAccumulator's accumulated text.
3. The conductor explicitly cancels the TTSAccumulator's context after detecting disconnect (via `c.observer.Interrupt()`), aborting in-flight TTS calls — no client to receive audio. Note: the conductor's parent context is NOT cancelled on disconnect (only SIGTERM cancels it), so the TTS abort requires an explicit `Interrupt()` call.
4. The conductor then exits and releases the advisory lock connection.

This means no interviewer text is lost on disconnect. On reconnect, the new conductor loads the persisted message from DB and includes it in `reconnect_state`. This matches the parent spec's "Network blip" behavior (Section 4): "tokens accumulate in the message accumulator but are not sent to the WebSocket."

**Disconnect between turns (in `WaitingForInput`):** The conductor detects `!ok` immediately. No in-flight work to complete. The conductor exits cleanly.

---

## 7. WebSocket Handler & Read Loop

**Handler (`session_ws.go`):**

```go
func SessionWS(b *backend.Backend) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        // 1. Extract session ID from URL path
        // 2. Auth: user from context (RequireAuth middleware)
        // 3. Load session from DB, verify ownership
        // 4. Verify session status is "active"
        // 5. Upgrade via coder/websocket.Accept()
        // 6. Build Conductor with deps from Backend
        // 7. Read client's session_init (with timeout)
        // 8. Launch conductor.Run(ctx) in goroutine
        // 9. Run readLoop (blocking, this goroutine)
    }
}
```

**Read loop:**

```go
func readLoop(ctx context.Context, ws *websocket.Conn, msgCh chan<- WSMessage, observer func() *TokenFanOut) {
    defer close(msgCh)
    for {
        _, data, err := ws.Read(ctx)
        if err != nil {
            return // closing msgCh signals conductor
        }
        msg, err := parseWSMessage(data)
        if err != nil {
            ws.Write(ctx, websocket.MessageText, errorJSON("malformed_message", err))
            continue
        }
        if msg.Type == "cancel_tts" {
            if obs := observer(); obs != nil {
                obs.Interrupt()
            }
            continue
        }
        msgCh <- msg
    }
}
```

**Key points:**
- `ws.Read(ctx)` — context cancellation (SIGTERM) propagates cleanly
- `observer` is a function because the fan-out is recreated each turn and nil between turns
- `cancel_tts` bypass: read loop calls `Interrupt()` directly, never sends to `msgCh`
- `ws.SetReadLimit(10 * 1024 * 1024)` — 10MB for audio payloads
- `session_init` read synchronously before launching conductor goroutine, with a 10-second timeout — no init race, no indefinite block if client never sends
- The `observer` function may return nil if `cancel_tts` arrives before the conductor has started streaming (e.g., between turns). The nil check at the call site is intentional for this race window.

**Reconnection:** Each WebSocket connection gets its own conductor. No pooling. Client disconnects → `close(msgCh)` → conductor detects → state is in DB. Client reconnects → new conductor → loads from DB → sends `reconnect_state`.

**Single-conductor-per-session guard:** After upgrade, the WebSocket handler acquires a dedicated connection from the pool (`pool.Acquire()`) and calls `pg_try_advisory_lock(key1, key2)` using the two 32-bit halves of the session UUID's most significant 64 bits. If the lock fails (another connection already holds it), the handler sends an error message and closes the WebSocket with `StatusPolicyViolation`.

The dedicated connection is held for the entire lifetime of the conductor goroutine — not returned to the pool until the conductor exits. The conductor receives this connection and calls `conn.Release()` in its cleanup path (after `Run` returns). Session-level advisory locks are released when the database session ends, which for pgxpool means when the connection is released back to the pool. This guarantees the lock is held exactly as long as the conductor is alive.

This prevents two conductors from writing to the same session simultaneously — covers both duplicate tabs and reconnect races. The cost is one dedicated connection per active interview, which is acceptable at the expected scale (70-350 peak concurrent sessions vs. the pool size).

---

## 8. Observer Pattern

**Interface:**

```go
type TokenObserver interface {
    OnToken(token string)
    OnDone(fullMessage string)
    OnError(err error)
    Interrupt()
}
```

**TokenFanOut:** Composite observer distributing to children. Each method iterates over all registered observers.

**Three observers, created fresh each turn:**

**WSWriter:**
- `OnToken`: writes `{"type":"interviewer_token","token":"..."}` to WebSocket. Write errors are logged and ignored — on disconnect, the LLM stream must continue to completion so the MessageAccumulator can persist the full response.
- `OnDone`: writes `{"type":"interviewer_done","message_id":"..."}`. Write errors ignored (same reason).
- `OnError`: writes `{"type":"error",...}`. Write errors ignored.
- `Interrupt`: no-op

**MessageAccumulator:**
- `OnToken`: appends to `strings.Builder`
- `OnDone`/`OnError`/`Interrupt`: no-op
- Exposes `Text() string` for conductor to read after streaming

**TTSAccumulator:**
- `OnToken`: appends to sentence buffer; on sentence boundary (`. ` `? ` `! ` or newline), sends sentence to TTS goroutine channel
- `OnDone`: flushes remaining buffer, closes channel; TTS goroutine finishes remaining sentences, then sends `{"type":"tts_done","message_id":"..."}` to WebSocket
- `OnError`: cancels internal context
- `Interrupt`: cancels internal context (what `cancel_tts` triggers). Aborts in-flight and future TTS API calls.
- Only included when `ttsEnabled == true`
- TTS goroutine context derived from conductor context — SIGTERM cancels it

**TTS goroutine detail:** Reads sentences from a channel, calls `Synthesizer.Synthesize()` for each. Reads chunks from the returned `io.ReadCloser` and sends each as `{"type":"tts_chunk","data":"<base64>","message_id":"...","seq":N}` to the WebSocket. After all sentences complete, sends `tts_done`. Audio is not persisted to GCS in this phase (deferred to Phase 5 deployment — see Section 2).

**Per-turn construction:**

```go
observers := []TokenObserver{
    NewWSWriter(ws, messageID),
    NewMessageAccumulator(),
}
if c.ttsEnabled {
    observers = append(observers, NewTTSAccumulator(ctx, ws, c.tts, messageID))
}
c.observer = NewTokenFanOut(observers...)
```

---

## 9. LLM Client

Lives in `internal/ai/client.go`.

**Struct:**

```go
type Client struct {
    anthropic *anthropic.Client
    pool      *pgxpool.Pool
}
```

**`StreamAndLog` params:**

```go
type StreamParams struct {
    Model    string
    System   string
    Messages []anthropic.MessageParam
    UserID   uuid.UUID  // for llm_calls.user_id (NOT NULL)
    Role     string     // for llm_calls.role (e.g., "interviewer")
    SessionID uuid.UUID // for llm_calls.session_id
}
```

**`StreamAndLog(ctx, params) (*TokenStream, error)`** — for the Conductor.
- Creates Anthropic streaming message request
- Returns `TokenStream` iterator
- `CloseWithTx(ctx, tx)` persists `llm_calls` + `llm_call_content` within a caller-provided transaction. The conductor uses this so that `llm_calls` + interviewer message are atomic. If either fails, both roll back — no orphaned data.
- `Close(ctx)` convenience variant that acquires its own connection from the pool. Used when transactionality with other writes is not needed.

**`CallAndLog(ctx, tx, params) (string, error)`** — for River workers.
- Blocking request, same logging, writes within caller's transaction
- Takes the same `UserID`, `Role`, `SessionID` fields (via a `CallParams` struct)

**TokenStream:**

```go
type TokenStream struct {
    stream *anthropic.MessageStream
}

func (s *TokenStream) Next() (string, error)       // next token or io.EOF
func (s *TokenStream) CloseWithTx(ctx, tx) error   // persists llm_calls in caller's tx
func (s *TokenStream) Close(ctx) error              // persists llm_calls (own connection)
func (s *TokenStream) FullMessage() string          // available after Close/CloseWithTx
```

**Cost computation:**

```go
var pricing = map[string]struct{ Input, Output float64 }{
    "claude-sonnet-4-20250514": {Input: 3.0 / 1_000_000, Output: 15.0 / 1_000_000},
}
```

---

## 10. STT/TTS Interfaces

**Interfaces:**

```go
// stt.go
type Transcriber interface {
    Transcribe(ctx context.Context, audio []byte, format string) (string, error)
}

// tts.go
type Synthesizer interface {
    Synthesize(ctx context.Context, text string) (io.ReadCloser, error)
}
```

**OpenAI implementations:**

```go
type OpenAITranscriber struct {
    client *openai.Client
    model  string  // "whisper-1"
}

type OpenAISynthesizer struct {
    client *openai.Client
    model  string  // "tts-1"
    voice  string  // "onyx"
}
```

`Synthesize` returns `io.ReadCloser` — TTS accumulator reads chunks and streams to client. Maps naturally to OpenAI's streaming response body.

**Config:** Uses the existing `config.Speech` struct — no new config needed:

```go
// Already exists in internal/config/config.go
type Speech struct {
    OpenAIAPIKey string `env:"OPENAI_API_KEY,required"`
    TTSVoice     string `env:"TTS_VOICE,default=onyx"`
    TTSModel     string `env:"TTS_MODEL,default=tts-1"`
    WhisperModel string `env:"WHISPER_MODEL,default=whisper-1"`
}
```

**Audio validation:** Before calling `Transcribe`, the conductor parses the WebM container header (audio track + duration check). Standalone function in `interview` package — validation is a conductor concern, not a provider concern.

---

## 11. Session & Question Endpoints

**`POST /api/sessions`** — create interview session.
- Auth required
- Body: `{ "question_id": "uuid", "duration_minutes": int, "tts_enabled": bool }`
- Validates: duration 1-180, question exists
- Inserts `interview_sessions`, returns session ID + metadata
- No entitlement checks

**`GET /api/sessions`** — list user's sessions.
- Auth required, scoped to user
- Optional filters: `?status=active&archived=false`

**`GET /api/sessions/:id`** — session detail with transcript.
- Auth required, ownership check

**`GET /api/questions`** — list available questions.
- Auth required
- Returns seed questions + user's custom questions
- Optional filters: `?difficulty=hard&tags=caching`

All handlers follow existing factory pattern: `func CreateSession(b *backend.Backend) http.HandlerFunc`. `POST /api/sessions` goes through the existing CSRF middleware (double-submit cookie) — no special handling needed. The WebSocket upgrade (`WSS /api/sessions/:id/ws`) also passes through the CSRF middleware, but gorilla/csrf exempts safe methods (GET) by default, so the upgrade request is not blocked. No special CSRF handling needed for WebSocket.

**Note on Conductor's DB access:** The Conductor struct holds `*pgxpool.Pool` directly rather than going through Backend methods. This is intentional — the conductor is a long-lived goroutine, not an HTTP handler. It needs to run transactions spanning multiple operations (persist message + enqueue job) which don't map to the single-operation Backend method pattern. Backend provides the pool via an accessor; the conductor uses it directly.

---

## 12. Jobs

**Stub EvaluateSession:**

```go
type EvaluateSessionArgs struct {
    SessionID uuid.UUID `json:"session_id"`
}

func (EvaluateSessionArgs) Kind() string { return "evaluate_session" }

type EvaluateSessionWorker struct {
    river.WorkerDefaults[EvaluateSessionArgs]
}

func (w *EvaluateSessionWorker) Work(ctx context.Context, job *river.Job[EvaluateSessionArgs]) error {
    return nil // Stub — Phase 7 replaces with evaluation logic
}
```

Enqueued transactionally with session status update on `end_session`:

```go
tx.Exec(ctx, updateSessionStatus, sessionID, "completed", ...)
c.jobs.InsertTx(ctx, tx, EvaluateSessionArgs{SessionID: sessionID}, nil)
```

**CleanupAbandonedSessions:**
- River periodic job, every 3 minutes
- Finds: `active` sessions where `started_at + config_duration_minutes + 5 min < NOW()`
- Updates: status to `completed`, sets `ended_at`
- `// TODO: enqueue EvaluateSession for each abandoned session (Phase 7)`

---

## 13. Testing Strategy

Three tiers.

### Unit Tests (no I/O)

- **statemachine_test.go** — every valid transition, every invalid transition, turn counting, terminal state
- **prompt_test.go** — builder output, nil-safe WithCoachBriefing, time context
- **observer_test.go** — fan-out distribution, MessageAccumulator correctness, Interrupt propagation

### Integration Tests (real WebSocket, real DB)

Test infrastructure:
- `httptest.NewServer` with actual router
- `coder/websocket.Dial` as client
- Real test database (existing test infrastructure from Phase 1)
- Mock STT/TTS via interfaces (canned responses)
- Mock LLM via fake Anthropic server (httptest, returns canned streaming responses)

### WebSocket Integration Test Cases

1. **Happy path (text)** — `session_init` → `session_loaded` → interviewer opening (streamed tokens + `interviewer_done`) → `end_turn` (text) → interviewer response (streamed tokens + `interviewer_done`) → `end_session` → `session_ended` → verify DB: status `completed`, messages persisted, `llm_calls` rows, stub eval job enqueued
2. **Voice input** — `end_turn` with audio → STT called → `transcription_result` → interviewer responds
3. **Cancel TTS** — `cancel_tts` during `InterviewerSpeaking` → TTS accumulator interrupted, response still persisted
4. **Reconnection** — complete a turn, disconnect, reconnect → `session_init` with `last_seq` → `reconnect_state` with correct messages
5. **Invalid transitions** — `end_turn` during `InterviewerSpeaking` → error, session not corrupted
6. **Malformed messages** — garbage JSON → error, connection stays alive
7. **Session ownership** — wrong user → rejected before upgrade
8. **Inactive session** — connect to `completed` session → rejected
9. **Graceful shutdown** — cancel server context mid-stream → partial state persisted, `reconnect_please` sent
10. **Timer overtime** — 1-minute session → `timer_warning` and `timer_overtime` at correct times, auto-end after 2-min grace
11. **Transactional enqueue** — `end_session` → session status and eval job in same transaction
12. **Abandoned cleanup** — create session, don't end it, run cleanup → status `completed`
13. **Preemptive reconnect timer** — create session with 60-min duration, verify `reconnect_please` sent after 55 minutes of WebSocket connection time (use a short duration override in tests), verify it arrives between turns not mid-stream

### LLM Client Tests

- **StreamAndLog** — fake server streams tokens → iterator yields them → `llm_calls` + `llm_call_content` persisted with correct counts and cost
- **CallAndLog** — blocking call → response returned → rows persisted within provided transaction
