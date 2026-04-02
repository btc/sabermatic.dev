# Drill v1 — Go Rewrite System Design

**Date**: 2026-04-02
**Status**: Approved
**Purpose**: Complete system design for rebuilding Drill as a multi-tenant SaaS in Go, based on the functional requirements (docs/functional-requirements-2026-04-01.md) and lessons learned from the Python prototype (v0/).

---

## 1. Architecture Overview

The system is a Go monolith deployed to Cloud Run, with background work processed by River (Postgres-backed job queue) running in the same binary. There is one Cloud SQL (Postgres) database, one GCS bucket for audio, the Anthropic API as the LLM provider, and Stripe for billing.

```
Clients (React SPA, Vite + shadcn/ui)
    │ HTTPS/WSS
    ▼
Cloud Run Service
  ┌──────────────────────────────────────────┐
  │  Go Binary (single process)              │
  │                                          │
  │  HTTP/WS Server  ·  River Workers        │
  │       │                  │               │
  │  ┌────┴──────────────────┴────────┐      │
  │  │        Service Layer           │      │
  │  │  Sessions · Eval · Edu · Coach │      │
  │  └────┬──────────┬──────────┬─────┘      │
  │       │          │          │             │
  │    sqlc/pgx   Anthropic    GCS           │
  └───────┼──────────┼──────────┼────────────┘
          │          │          │
    Cloud SQL    Anthropic   GCS
    Postgres      API       (audio)
```

### Why a Monolith

The four AI roles (interviewer, evaluator, educator, coach) are different prompt configurations calling the same API. They share the same database, user model, and session model. Splitting into services would mean distributed transactions, service discovery, inter-service auth, and separate deploy pipelines for zero benefit at this scale (70-350 peak concurrent sessions). The actual compute is offloaded to Anthropic.

River workers run inside the same Go process. Jobs are enqueued transactionally with the business logic that creates them. No separate worker deployment, no message broker.

### Key Technology Choices

| Component | Technology | Rationale |
|---|---|---|
| Runtime | Go on Cloud Run | Goroutine model handles thousands of concurrent WebSockets with minimal memory. I/O-bound workload. |
| Database | Cloud SQL (PostgreSQL 16) | Single database for everything: app data, River job queue, auth sessions, rate limiting. |
| Database access | sqlc + pgx/v5 | Type-safe Go from SQL. No ORM. go-sqlbuilder for the few dynamic queries where optional filters make static SQL impractical (e.g., question list filtered by optional difficulty + tags). sqlc is the default; go-sqlbuilder is the escape hatch, not a second query layer. |
| Migrations | golang-migrate | Runs at startup with advisory lock. No JVM dependency. Migration files double as sqlc schema source. |
| Job queue | River | Postgres-backed. Transactional enqueue. Retry with backoff. Built-in UI. Same binary, same connection pool. |
| LLM | Anthropic Go SDK | All four AI roles. Streaming for interviewer, blocking for eval/educator/coach. |
| Object storage | GCS | Audio only. Native Cloud Run IAM integration. Negligible egress at this scale. |
| Frontend | React + Vite + shadcn/ui | SPA with copy-pasted ownable components (Radix UI + Tailwind). TanStack Query for server state. |
| Payments | Stripe | Checkout, Customer Portal, webhooks processed as River jobs. |
| Auth | Self-managed in Postgres | golang.org/x/oauth2 for OAuth, bcrypt for passwords, opaque session tokens as HttpOnly cookies. |
| Observability | OpenTelemetry → Cloud Trace/Monitoring/Logging | End-to-end tracing from frontend through backend. |
| Config | sethvargo/go-envconfig | Nested structs, environment variables only. |

---

## 2. Data Model

### Auth Tables

```sql
CREATE TABLE users (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email           TEXT UNIQUE NOT NULL,
    email_verified  BOOLEAN NOT NULL DEFAULT FALSE,
    password_hash   TEXT,
    display_name    TEXT NOT NULL,
    role            TEXT NOT NULL DEFAULT 'candidate'
                    CHECK (role IN ('candidate', 'admin')),
    stripe_customer_id TEXT,
    plan            TEXT NOT NULL DEFAULT 'free',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at      TIMESTAMPTZ
);

CREATE TABLE oauth_accounts (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id),
    provider    TEXT NOT NULL,
    provider_id TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (provider, provider_id)
);

CREATE TABLE auth_sessions (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      UUID NOT NULL REFERENCES users(id),
    token_hash   TEXT UNIQUE NOT NULL,
    expires_at   TIMESTAMPTZ NOT NULL,
    last_active  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ip_address   INET,
    user_agent   TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

### Core Tables

```sql
CREATE TABLE questions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID REFERENCES users(id),
    title           TEXT NOT NULL,
    prompt          TEXT NOT NULL,
    difficulty      TEXT NOT NULL CHECK (difficulty IN ('medium', 'hard')),
    tags            TEXT[] NOT NULL DEFAULT '{}',
    hints           TEXT,
    source          TEXT NOT NULL CHECK (source IN ('seed', 'custom', 'coach_generated')),
    coach_rationale TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE interview_sessions (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id                 UUID NOT NULL REFERENCES users(id),
    question_id             UUID NOT NULL REFERENCES questions(id),
    status                  TEXT NOT NULL DEFAULT 'active'
                            CHECK (status IN (
                                'active','completed','evaluating',
                                'reviewed','evaluation_failed'
                            )),
    config_duration_minutes INT NOT NULL,
    config_tts_enabled      BOOLEAN NOT NULL DEFAULT FALSE,
    config_coach_briefing   BOOLEAN NOT NULL DEFAULT FALSE,
    started_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ended_at                TIMESTAMPTZ,
    turn_count              INT NOT NULL DEFAULT 0,
    archived                BOOLEAN NOT NULL DEFAULT FALSE,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE messages (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id   UUID NOT NULL REFERENCES interview_sessions(id),
    seq          INT NOT NULL,
    role         TEXT NOT NULL CHECK (role IN ('interviewer', 'candidate')),
    content      TEXT NOT NULL,
    input_method TEXT,
    audio_url    TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (session_id, seq)
);

CREATE TABLE evaluations (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id          UUID UNIQUE NOT NULL REFERENCES interview_sessions(id),
    score_requirements  INT NOT NULL CHECK (score_requirements BETWEEN 1 AND 5),
    score_architecture  INT NOT NULL CHECK (score_architecture BETWEEN 1 AND 5),
    score_deep_dive     INT NOT NULL CHECK (score_deep_dive BETWEEN 1 AND 5),
    score_scalability   INT NOT NULL CHECK (score_scalability BETWEEN 1 AND 5),
    score_communication INT NOT NULL CHECK (score_communication BETWEEN 1 AND 5),
    score_overall       INT NOT NULL CHECK (score_overall BETWEEN 1 AND 5),
    strengths           JSONB NOT NULL,
    gaps                JSONB NOT NULL,
    advice              TEXT NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE annotations (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    evaluation_id   UUID NOT NULL REFERENCES evaluations(id),
    message_id      UUID NOT NULL REFERENCES messages(id),
    annotation_type TEXT NOT NULL
                    CHECK (annotation_type IN (
                        'strength','gap','missed_opportunity','note'
                    )),
    content         TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE educator_analyses (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id      UUID UNIQUE NOT NULL REFERENCES interview_sessions(id),
    status          TEXT NOT NULL DEFAULT 'generating'
                    CHECK (status IN ('generating', 'completed')),
    model_answer    TEXT,
    gap_deep_dives  TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE coach_analyses (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id               UUID NOT NULL REFERENCES users(id),
    narrative             TEXT NOT NULL,
    weakest_dimension     TEXT,
    improving_dimensions  TEXT[],
    topic_gaps            TEXT[],
    suggested_question_id UUID REFERENCES questions(id),
    sessions_analyzed     UUID[] NOT NULL,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

### Observability & Billing Tables

```sql
CREATE TABLE llm_calls (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id     UUID REFERENCES interview_sessions(id),
    user_id        UUID NOT NULL REFERENCES users(id),
    role           TEXT NOT NULL,
    model          TEXT NOT NULL,
    input_tokens   INT NOT NULL,
    output_tokens  INT NOT NULL,
    estimated_cost NUMERIC(10,6) NOT NULL,
    latency_ms     INT NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE llm_call_content (
    llm_call_id UUID PRIMARY KEY REFERENCES llm_calls(id),
    prompt      JSONB NOT NULL,
    response    JSONB NOT NULL
);

CREATE TABLE user_events (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL REFERENCES users(id),
    session_id UUID REFERENCES interview_sessions(id),
    event_type TEXT NOT NULL,
    metadata   JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE usage_periods (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID NOT NULL REFERENCES users(id),
    period_start  TIMESTAMPTZ NOT NULL,
    period_end    TIMESTAMPTZ NOT NULL,
    sessions_used INT NOT NULL DEFAULT 0,
    UNIQUE (user_id, period_start)
);
```

### Indexes

```sql
CREATE INDEX idx_sessions_user_status ON interview_sessions(user_id, status)
    WHERE archived = FALSE;
CREATE INDEX idx_sessions_user_archived ON interview_sessions(user_id, archived);
CREATE INDEX idx_sessions_question ON interview_sessions(question_id);
CREATE INDEX idx_messages_session_seq ON messages(session_id, seq);
CREATE INDEX idx_annotations_evaluation ON annotations(evaluation_id);
CREATE INDEX idx_annotations_message ON annotations(message_id);
CREATE INDEX idx_llm_calls_session ON llm_calls(session_id);
CREATE INDEX idx_llm_calls_user_created ON llm_calls(user_id, created_at);
CREATE INDEX idx_user_events_user_created ON user_events(user_id, created_at);
CREATE INDEX idx_questions_user ON questions(user_id) WHERE user_id IS NOT NULL;
CREATE INDEX idx_questions_seed ON questions(id) WHERE source = 'seed';
CREATE INDEX idx_usage_periods_user ON usage_periods(user_id, period_start);
CREATE INDEX idx_coach_analyses_user ON coach_analyses(user_id, created_at DESC);
```

---

## 3. API Design

### REST Endpoints

```
Auth:
  POST   /api/auth/signup              Email/password registration
  POST   /api/auth/login               Email/password login
  POST   /api/auth/logout              Invalidate session
  GET    /api/auth/oauth/:provider     Start OAuth flow
  GET    /api/auth/oauth/:provider/cb  OAuth callback
  POST   /api/auth/forgot-password     Send reset email
  POST   /api/auth/reset-password      Reset with token
  POST   /api/auth/verify-email        Verify email token
  DELETE /api/auth/account             Account deletion (GDPR)

User:
  GET    /api/me                       Current user profile + plan
  GET    /api/me/usage                 Current period usage
  GET    /api/me/export                GDPR data export (enqueues job)

Questions:
  GET    /api/questions                List (filterable by difficulty, tags)
  POST   /api/questions                Create custom question
  GET    /api/questions/:id            Detail + user stats
  PUT    /api/questions/:id            Update question (admin: seed; user: own custom)
  DELETE /api/questions/:id            Delete question (admin: seed; user: own custom)

Sessions:
  POST   /api/sessions                 Start new session (entitlement check)
  GET    /api/sessions                 List (filterable, sortable)
  GET    /api/sessions/:id             Session detail
  PATCH  /api/sessions/:id/archive     Archive/unarchive single session
  POST   /api/sessions/archive-bulk    Archive/unarchive multiple sessions
  POST   /api/sessions/:id/evaluate    Retry failed evaluation
  GET    /api/sessions/:id/transcript  Full transcript with annotations
  GET    /api/sessions/:id/evaluation  Evaluation results
  POST   /api/sessions/:id/educator    Request educator analysis
  GET    /api/sessions/:id/educator    Get educator content

Coach:
  POST   /api/coach/analyze            Trigger coach analysis
  GET    /api/coach/latest             Latest coach analysis

Billing:
  POST   /api/billing/checkout         Create Stripe Checkout session
  POST   /api/billing/portal           Create Stripe Portal session
  POST   /api/webhooks/stripe          Stripe webhook receiver

Admin:
  GET    /admin/dashboard              System health metrics
  GET    /admin/users                  User list with usage
  GET    /admin/costs                  Cost breakdown
  GET    /admin/jobs/*                 River UI (embedded)
```

### WebSocket Protocol

Endpoint: `WSS /api/sessions/:id/ws`

**Conductor States:**

| State | Description |
|---|---|
| `InterviewerSpeaking` | LLM streaming tokens through observer fan-out |
| `WaitingForInput` | Interviewer done, candidate's turn |
| `Transcribing` | STT in progress on audio input |
| `ProcessingInput` | Preparing LLM call with candidate message |
| `Ending` | Teardown in progress |
| `Ended` | Terminal |

**State Transitions:**

```
InterviewerSpeaking → WaitingForInput, Ending
WaitingForInput     → Transcribing, ProcessingInput, Ending
Transcribing        → ProcessingInput, Ending
ProcessingInput     → InterviewerSpeaking, Ending
Ending              → Ended
Ended               → (terminal)
```

**Client → Server:**

| Message | Payload | Queue |
|---|---|---|
| `session_init` | `{ last_seq: null \| N }` | Through queue |
| `end_turn` | `{ content, input_method: "text" }` or `{ audio, input_method: "voice" }` | Through queue |
| `cancel_tts` | (none) | **Bypasses queue** → `observer.Interrupt()` |
| `end_session` | (none) | Through queue |
| `ping` | (none) | Through queue |

**Server → Client:**

| Message | Payload |
|---|---|
| `session_loaded` | `{ session_id, started_at, duration_minutes, tts_enabled }` |
| `reconnect_state` | `{ last_seq, messages: [...] }` |
| `interviewer_token` | `{ token }` |
| `interviewer_done` | `{ message_id }` |
| `tts_chunk` | `{ data, message_id, seq }` |
| `tts_done` | `{ message_id }` |
| `transcription_result` | `{ text, message_id }` |
| `timer_warning` | `{ minutes_remaining: N }` — N = `clamp(2, 5, round(duration / 9))` |
| `timer_overtime` | (none) — session auto-ends 2 minutes after overtime |
| `session_ended` | `{ reason: "candidate" \| "interviewer" \| "timeout" }` |
| `reconnect_please` | (none) |
| `error` | `{ code, message }` |
| `pong` | (none) |

Audio handling: client records segments in-memory, concatenates, encodes to base64, sends one `end_turn` message. No server-side segment management. Proven approach from v0.

`cancel_tts` bypasses the conductor's sequential message channel and calls `observer.Interrupt()` directly from the read goroutine. This is critical: during `InterviewerSpeaking`, the main loop is blocked streaming LLM tokens. If `cancel_tts` went through the channel, it would queue behind the in-progress turn.

`reconnect_please` is sent during graceful shutdown (Cloud Run deploy) and at the 55-minute mark for sessions exceeding Cloud Run's 60-minute request timeout (preemptive reconnection). The client closes the WebSocket and immediately reconnects without backoff.

---

## 4. Interview Conductor

The conductor is a single goroutine that owns all mutable state for an active interview session. Every WebSocket message is funneled through one channel (`msgCh`). The conductor processes messages sequentially, one at a time. This eliminates race conditions: user sends `end_turn` while the previous turn is still processing, user sends `end_session` while TTS is streaming, user sends text while transcription is in flight.

### State Machine

A separate struct with no I/O. Pure logic, independently testable. Transition rules as a map (GoF state transition table):

```go
type StateMachine struct {
    state     ConductorState
    turnCount int
    startedAt time.Time
}

func (sm *StateMachine) Transition(next ConductorState) error {
    allowed := transitions[sm.state]
    if !allowed[next] {
        return fmt.Errorf("invalid transition: %s → %s", sm.state, next)
    }
    if next == StateWaitingForInput {
        sm.turnCount++
    }
    sm.state = next
    return nil
}
```

### Initialization

On `session_init`, the conductor checks DB state to determine behavior:
- **No messages in DB** → new session. Send `session_loaded`, stream interviewer's opening message.
- **Messages exist in DB** → reconnect. Send `reconnect_state` with messages since client's `last_seq`.

The client does not need to distinguish new vs. reconnect. `last_seq: null` means "I have nothing, send me everything." The conductor checks the DB, not the client's claim.

### Main Loop

```go
func (c *Conductor) Run(ctx context.Context) {
    c.streamInterviewerResponse(ctx) // opening message

    for {
        select {
        case msg, ok := <-c.msgCh:
            if !ok {
                c.handleDisconnect(ctx)
                return
            }
            c.handleMessage(ctx, msg)
        case <-c.timerWarningCh:
            c.ws.Send(TimerWarning{MinutesRemaining: 5})
        case <-c.timerOvertimeCh:
            c.ws.Send(TimerOvertime{})
        case <-c.reconnectTimerCh:
            c.handlePreemptiveReconnect(ctx)
        case <-ctx.Done():
            c.handleShutdown(ctx)
            return
        }
    }
}
```

The `reconnectTimerCh` fires at the 55-minute mark, but the conductor does not reconnect immediately. Instead it sets `reconnectPending = true`. After the current turn completes and transitions to `WaitingForInput`, the conductor checks the flag and sends `reconnect_please`. This guarantees reconnection happens between turns — no mid-stream interruption, no partial state to recover. The client reconnects immediately, gets a fresh 60-minute Cloud Run window. For a 180-minute session, this happens ~3 times. The user sees at most a brief "Reconnecting..." banner.

### Turn Processing Sequence

```
WaitingForInput
  → if voice input:
      validate audio (WebM header + container parse for audio track + duration check)
      sm.Transition(Transcribing)
      call STT, send transcription_result (read-only, no edit window)
      write input audio to GCS inline
  → sm.Transition(ProcessingInput)
    persist candidate message to DB (pre-generated UUID for message_id)
    build prompt (via Builder)
    create observer fan-out (wsWriter + ttsAccumulator + messageAccumulator)
    sm.Transition(InterviewerSpeaking)
    stream LLM response through observers
    persist interviewer message to DB
    TTS accumulator writes concatenated output audio to GCS in OnDone
    sm.Transition(WaitingForInput)
    check reconnectPending flag → if set, send reconnect_please
    check overtime + 2 min → if exceeded, auto-end session
```

**Audio validation:** Before STT, the conductor parses the WebM container header to verify it contains an audio track and the duration is reasonable. Rejects non-audio or oversized payloads. WebSocket max message size set to 10MB.

**Message IDs:** The conductor pre-generates a UUID for each message before persistence. This UUID is sent in `transcription_result` and used when persisting to the `messages` table. UUIDs do not depend on the database.

### Message Queue Bypass

The WebSocket read goroutine sends most messages into `msgCh`. The one exception: `cancel_tts` calls `c.observer.Interrupt()` directly. This cancels in-flight TTS API calls without waiting for the current turn to finish processing.

### Persistence

Every completed message (candidate and interviewer) is persisted to the `messages` table immediately. If the instance dies between turns, no data is lost. If it dies mid-stream, the accumulated text is persisted by `handleShutdown` (triggered by context cancellation on SIGTERM).

### Reconnection

**Network blip (instance alive):** Client disconnects. `readLoop` closes `msgCh`. Conductor detects disconnect. If mid-stream, tokens accumulate in the message accumulator but are not sent to the WebSocket. On reconnect, a new `readLoop` starts. The conductor sends `reconnect_state` with all messages since the client's last acknowledged seq.

**Instance death:** Session state is in Postgres. Client reconnects, hits a new instance, which creates a fresh conductor and loads state from DB. Any partial interviewer response from the crash is lost (a few seconds of text). The conductor detects an unanswered candidate turn and re-triggers the LLM call.

**Graceful deploy:** SIGTERM → conductor waits for current turn to complete (within Cloud Run's termination grace period, configured to 30s) → sends `reconnect_please` → client reconnects to new revision immediately, no backoff. If the grace period expires before the turn completes, force-save partial state and close.

**Preemptive reconnect (55-min timer):** Timer sets `reconnectPending` flag. The conductor checks this flag after each turn completes (in `WaitingForInput`). Reconnection always happens between turns — no mid-stream interruption, no partial state. Enables sessions up to 180 minutes within Cloud Run's 60-minute request timeout.

---

## 5. Observer Pattern: Token Stream Fan-Out

```go
type TokenObserver interface {
    OnToken(token string)
    OnDone(fullMessage string)
    OnError(err error)
    Interrupt()
}
```

Composite fan-out distributes to all observers. The streaming loop sees one observer and doesn't know what's behind it.

**Observers:**
- **WSWriter** — sends `interviewer_token` messages to the client. `Interrupt()` is a no-op.
- **TTSAccumulator** — buffers text, fires TTS API on sentence boundaries in a separate goroutine. Sends `tts_chunk` messages to client as chunks arrive. In `OnDone`, writes concatenated audio to GCS as a single MP3 file per interviewer message. `Interrupt()` cancels its internal context, aborting all in-flight and future TTS calls.
- **MessageAccumulator** — builds the complete response for DB persistence. `Interrupt()` is a no-op.

Adding a new consumer (e.g., content moderation) means implementing `TokenObserver` and passing it to `NewTokenFanOut`. Zero changes to the streaming loop.

If TTS is disabled for a session, the fan-out simply doesn't include a TTS accumulator, and `Interrupt()` is a no-op across the board.

---

## 6. Builder Pattern: Prompt Construction

Each `With` method is independently testable. Nil-safe (e.g., `WithCoachBriefing(nil)` is a no-op).

```go
func NewInterviewerPrompt() *PromptBuilder { ... }

func (b *PromptBuilder) WithSystemInstructions() *PromptBuilder { ... }
func (b *PromptBuilder) WithQuestion(q db.Question) *PromptBuilder { ... }
func (b *PromptBuilder) WithTranscript(msgs []db.Message) *PromptBuilder { ... }
func (b *PromptBuilder) WithTimeContext(elapsed, remaining time.Duration) *PromptBuilder { ... }
func (b *PromptBuilder) WithCoachBriefing(ca *db.CoachAnalysis) *PromptBuilder { ... }
func (b *PromptBuilder) Build() (string, []anthropic.MessageParam) { ... }
```

Evaluator, educator, and coach each have their own builder with different components. The prompt text is the core product.

---

## 7. LLM Client

The shared step across all AI roles (LLM call + logging) lives on the LLM client. Two methods for two execution models:

**`CallAndLog`** — blocking call for River jobs (evaluator, educator, coach). Makes the API call, extracts token counts, computes cost, inserts `llm_calls` + `llm_call_content` rows within the provided transaction.

**`StreamAndLog`** — streaming call for the conductor (interviewer). Returns a token iterator. Writes `llm_calls`/`llm_call_content` rows on stream completion.

No Strategy interface across roles. Each River job worker calls `CallAndLog`, then does its own parsing, validation, and persistence. Three straightforward sequential functions.

### Rate Limit Handling

Two layers:
1. **River queue cap** (10 workers on `ai` queue) — coarse control preventing background jobs from competing with live interviews for Anthropic rate limit budget.
2. **Anthropic SDK retry on 429** — automatic retry with backoff, covering both conductor calls and River worker calls.

---

## 8. Background Jobs (River)

| Job | Queue | Timeout | Max Attempts | Uniqueness |
|---|---|---|---|---|
| `EvaluateSession` | `ai` | 10 min | 4 | unique per `session_id` |
| `GenerateEducatorContent` | `ai` | 15 min | 5 | unique per `session_id` |
| `RunCoachAnalysis` | `ai` | 15 min | 3 | unique per `user_id` |
| `SendEmail` | `notifications` | 1 min | 3 | — |
| `TrackUsageMetrics` | `telemetry` | 30 sec | 3 | — |
| `CleanupAbandonedSessions` | `maintenance` | 1 min | 1 | periodic, every 3 min |

Key properties:
- **Transactional enqueue** — evaluation job created in same transaction as session status update to `completed`. No orphaned jobs.
- **Unique jobs as idempotency keys** — `EvaluateSession` unique on `session_id`, `RunCoachAnalysis` unique on `user_id`. Duplicate enqueues return the existing job. Safe to call retry endpoints multiple times.
- **Exponential backoff** — configured per job type, handled by River.
- **River UI** at `/admin/jobs` behind admin auth for queue visibility.
- **Abandoned session cleanup** — `CleanupAbandonedSessions` runs every 3 minutes. Finds `active` sessions where `started_at + config_duration_minutes + 5 minutes < NOW()`, sets status to `completed`, enqueues `EvaluateSession` for each.

### Evaluation Flow

1. River picks up `EvaluateSession`. Worker loads transcript, constructs prompt via evaluator Builder.
2. Calls Anthropic API via `CallAndLog` (non-streaming, tool_use).
3. Semantic validation: no all-identical scores, non-empty strengths/gaps, valid annotation message_seqs.
4. Validation failure → River retries with exponential backoff.
5. Success → evaluation + annotations + status update to `reviewed` + email notification enqueue. All in one transaction.
6. Retries exhausted → status to `evaluation_failed`. Candidate can retry via `POST /api/sessions/:id/evaluate`.

---

## 9. Authentication

Self-managed auth in Postgres. No external auth providers.

- **OAuth2** for Google and GitHub via `golang.org/x/oauth2`
- **bcrypt** for email/password
- **Opaque session tokens** in Postgres as HttpOnly/Secure/SameSite cookies
- **No JWTs** — single-service architecture means DB lookup for session validation is cheap, and revocation is free
- **Double-submit cookie** for CSRF
- **Per-IP and per-account rate limits** on auth endpoints

Email verification via signed time-limited token sent by `SendEmail` River job. Password reset uses the same pattern.

---

## 10. Billing

Stripe Checkout for signup, Customer Portal for self-service. Webhooks processed synchronously in the endpoint handler (verify signature, look up user, update plan, return 200). No River job needed — user always exists before Stripe interaction, and the update is a single DB query. Stripe retries on failure.

```go
var Plans = map[string]Plan{
    "free": {
        SessionsPerMonth:   3,
        MaxDurationMinutes: 30,
        EducatorAccess:     Preview,
        CoachAccess:        false,
        ConcurrentSessions: 1,
    },
    "pro": {
        SessionsPerMonth:   50,
        MaxDurationMinutes: 180,
        EducatorAccess:     Full,
        CoachAccess:        true,
        ConcurrentSessions: 2,
        StripePriceID:      "price_xxx",
    },
}
```

Plans defined as a Go map. Code deploy to change, no architectural changes (FR-007).

**Entitlement enforcement:**
- **Session count**: Atomic `UPDATE usage_periods SET sessions_used = sessions_used + 1 WHERE sessions_used < $limit RETURNING sessions_used`. Zero rows returned = limit reached. No read-then-check race.
- **Concurrent sessions**: `COUNT(active)` vs plan limit, checked in same transaction as session creation.
- **Duration**: `config_duration_minutes` validated against `MaxDurationMinutes`.
- **Coach access**: `POST /api/coach/analyze` and `GET /api/coach/latest` check `CoachAccess` entitlement. Returns 403 with upgrade message for free-tier users.
- **Educator access**: Full content for paid users. Free-tier users see a server-side truncated preview (approximately first 2000 characters of `model_answer` plus the first gap deep-dive entry). Full content never sent to free-tier clients.

---

## 11. Observability

### Tracing

OpenTelemetry Go SDK → Cloud Trace. Frontend OTel JS SDK generates `traceparent` headers on HTTP requests. For WebSocket, the client sends trace context in `session_init`, and the conductor uses it as the parent span. End-to-end trace for a single turn: client spacebar press → WS send → STT → LLM (time-to-first-token + total) → TTS → WS receive → client audio playback.

### Metrics

Cloud Monitoring: `llm_call_duration_seconds`, `llm_call_tokens_total`, `llm_call_cost_dollars`, `active_sessions_total`, `river_job_duration_seconds`, `river_job_queue_depth`.

### Logs

Structured JSON to stdout → Cloud Logging. Every line includes trace_id, span_id, session_id, user_id.

### LLM Audit

`llm_calls` table for metrics (fast aggregation). `llm_call_content` companion table for full prompts/responses (joined only for debugging/reconstruction).

### Session Reconstruction (FR-082)

`messages` (transcript) + `llm_calls`/`llm_call_content` (every prompt/response) + `user_events` (actions) + Cloud Logging (system events) = complete audit trail. All queryable by session_id.

### Admin Dashboard

`/admin/dashboard`: system health, per-user usage, cost trends, error rates, latency distributions, River UI for queue health.

### Alerts

Error rate > 1%, P95 latency > 5s on interview endpoints, River `ai` queue depth > 50, Cloud SQL CPU > 80% sustained 10 min, active sessions > 500.

### Retention

Indefinite on all tables and Cloud Logging.

---

## 12. GDPR Compliance

- **Right of access / data portability**: `GET /api/me/export` enqueues River job → assembles ZIP → GCS signed URL → email link. Export includes: user profile, all sessions (metadata + full transcripts + evaluations + annotations), educator analyses, coach analyses, custom and coach-generated questions, and all audio files (input and output) from GCS. Excludes internal system data (`llm_calls`/`llm_call_content`, `user_events`).
- **Right to erasure**: `DELETE /api/auth/account` soft-deletes (sets `deleted_at`). Daily River job processes accounts soft-deleted > 30 days: anonymize user record, delete audio from GCS, delete `llm_call_content` rows, retain anonymized scores for aggregate analytics.
- **Consent**: Cookie consent banner. Session cookies are essential (no consent required). Analytics cookies require opt-in.
- **Lawful basis**: Contract (user signed up). Legitimate interest (LLM call logging for service improvement). Audio recording disclosed at session start.

---

## 13. Security & Abuse Prevention

- **Entitlements**: Checked at session creation (atomic increment), educator request, and coach endpoints. See Section 10.
- **Rate limiting**: Per-user token bucket in Postgres (atomic row update) for API endpoints. In-memory per-connection rate limit for WebSocket messages. The in-memory state is ephemeral and dies with the connection; Postgres-based per-user limit covers cross-connection abuse.
- **Content moderation**: Interviewer system prompt detects jailbreak attempts. Pre-LLM scan for injection patterns. Flagged messages logged, interview continues.
- **Account abuse**: Email verification required. Account creation rate-limited per IP. Browser fingerprint flags suspicious patterns for admin review.

---

## 14. Deployment & CI/CD

- **Dockerfile**: Multi-stage build. Go binary + embedded React SPA static assets via `embed.FS`.
- **CI/CD**: GitHub Actions. `sqlc generate` (fail if diff) → `go vet` / `staticcheck` / `go test` → `docker build` → push to Artifact Registry → `gcloud run deploy` with canary (5% for 10 min, then 100%).
- **Migrations**: golang-migrate at startup with Postgres advisory lock. Safe across multiple Cloud Run instances.
- **Graceful deploys**: SIGTERM → conductors persist partial state → send `reconnect_please` → clients reconnect to new revision.

### Initial Configuration (Launch Day)

| Component | Config | Monthly Cost |
|---|---|---|
| Cloud Run | 1 vCPU, 512MB, min 0, max 2, concurrency 100, timeout 3600s | $2-10 |
| Cloud SQL | db-f1-micro, 10GB SSD, backups on, HA off | $8 |
| GCS | Single bucket, standard storage | $0.02 |
| Anthropic API | Testing usage | $10-50 |
| **Total** | | **~$20-70** |

### Scale-Up Triggers

| Trigger | Action |
|---|---|
| First paying customer | Min instances = 1, Cloud SQL → db-g1-small |
| 50+ paying customers | Cloud SQL HA failover, → db-custom-2-8192 |
| 500+ concurrent sessions | Cloud Run 2 vCPU, 2GB, max 10 instances |

---

## 15. Go Project Structure

```
drill/
├── cmd/drill/main.go               # HTTP server + River client startup
├── internal/
│   ├── auth/                       # OAuth, password, sessions
│   ├── billing/                    # Stripe, entitlements
│   ├── config/                     # Env-based config (go-envconfig)
│   ├── db/                         # sqlc generated (do not edit)
│   ├── handler/                    # HTTP + WebSocket handlers
│   │   ├── routes.go               # Centralized route registration
│   │   ├── session_ws.go           # Interview WebSocket
│   │   └── admin.go
│   ├── interview/                  # Interview orchestration
│   │   ├── conductor.go            # Conductor + state machine
│   │   ├── observer.go             # TokenObserver, fan-out, TTS accumulator
│   │   ├── prompt.go               # Interviewer prompt builder
│   │   └── stt.go                  # Speech-to-text
│   ├── evaluation/                 # Eval prompt + parsing + validation
│   ├── educator/                   # Educator content generation
│   ├── coach/                      # Coach analysis + question gen
│   ├── jobs/                       # River workers
│   │   ├── evaluate.go
│   │   ├── educator.go
│   │   ├── coach.go
│   │   ├── email.go
│   │   └── workers.go              # Registration
│   ├── llm/                        # Anthropic wrapper, CallAndLog/StreamAndLog
│   ├── storage/                    # GCS wrapper
│   └── telemetry/                  # OTel setup
├── sql/
│   ├── migrations/                 # golang-migrate SQL files
│   └── queries/                    # sqlc query files
├── web/                            # React SPA (Vite + shadcn/ui)
│   └── src/
│       ├── components/
│       ├── pages/
│       └── hooks/
│           ├── useWebSocket.ts
│           └── useAudioRecorder.ts
├── sqlc.yaml
├── Dockerfile
└── go.mod
```

### Go Conventions

- **Handlers**: Free functions taking `*Backend`, returning `http.HandlerFunc`. Centralized route registration in `routes.go`.
- **Database**: sqlc for all static queries. go-sqlbuilder for dynamic filters (question list). sqlc is default; go-sqlbuilder is the escape hatch.
- **Config**: Nested structs with `sethvargo/go-envconfig`. Environment variables only.
- **Testing**: Table-driven tests. testify/require for assertions. httptest for handlers. testcontainers-go for integration tests against real Postgres.
- **Errors**: Sentinel errors for domain conditions. `%w` wrapping. `errors.Is`/`errors.As` for matching. Early return, never nested else.
- **Comments**: Godoc-style on all exports. `NB:` prefix for important implementation notes.

---

## 16. Build Order

Frontend built incrementally with shadcn/ui alongside each backend sub-project.

| Phase | Scope | Frontend |
|---|---|---|
| 1. Foundation | Go scaffolding, DB schema, sqlc, config, health check | None |
| 2. Auth | Users, OAuth, session cookies, middleware | Login/signup pages |
| 3. Deployment | Dockerfile, Cloud Run, CI/CD, GCS bucket | None |
| 4. Conductor | State machine, WebSocket, STT/TTS, LLM streaming | Interview page |
| 5. Evaluation | Evaluator River job, annotations, validation | Results + SessionReview pages |
| 6. Educator + Coach | Both AI roles, question generation | Learn page, Home coach card |
| 7. Billing | Stripe, entitlements, usage tracking | Plan selector, upgrade prompts |
| 8. Polish + UI | History, Home stats, score trends, bulk archive | Remaining UX |
| 9. Observability + Admin | OTel, Cloud Trace, admin dashboard, alerts | Admin pages |

Each phase gets its own implementation plan. Phase 1 is the starting point.

---

## Appendix A: Functional Requirements Traceability

Every FR from `docs/functional-requirements-2026-04-01.md`, mapped to the design decision that addresses it.

### 2. User Management & Authentication

| FR | Requirement | Design |
|---|---|---|
| FR-001 | Google, email/password, GitHub login | `golang.org/x/oauth2` for Google/GitHub, bcrypt for passwords. `users` + `oauth_accounts` tables. See Section 9. |
| FR-002 | Account lifecycle (signup, verify, login, reset, logout) | `internal/auth/`. Email verification via signed token + `SendEmail` River job. Logout invalidates `auth_sessions` row. |
| FR-003 | Account deletion (GDPR-compliant) | `DELETE /api/auth/account` sets `deleted_at`. Daily River job anonymizes after 30 days, deletes audio from GCS, deletes `llm_call_content` rows. See Section 12. |
| FR-004 | Candidate and Administrator roles | `users.role` column, CHECK constraint. Middleware on admin endpoints checks role. |
| FR-005 | Admin manages users, config, questions, monitoring | `/admin/*` endpoints. Question CRUD via `/api/questions` (admin creates seed questions with `user_id = NULL`). River UI at `/admin/jobs`. Dashboard at `/admin/dashboard`. |
| FR-006 | All actions tied to user identity | Session cookie → `auth_sessions` → `user_id`. `user_events` and `llm_calls` tables include `user_id`. Cloud Logging includes user_id. |

### 3. Billing & Access Control

| FR | Requirement | Design |
|---|---|---|
| FR-007 | Plan structures modifiable without architectural changes | Plans are a Go map compiled into the binary. Code deploy to change, no schema/infrastructure changes. See Section 10. |
| FR-008 | Plans gate sessions/month, duration, features, concurrency | Atomic `UPDATE usage_periods ... WHERE sessions_used < $limit` for session count. `config_duration_minutes` validated against plan limit. Feature access (educator, coach) checked at API layer. Concurrent sessions via `COUNT(active)` in same transaction. |
| FR-009 | Free tier same quality as paid | Same AI model, same prompts, same voice I/O for all tiers. Only usage limits differ. |
| FR-010 | Educator: full for paid, preview for free | `GET /api/sessions/:id/educator` checks plan. Preview returns first ~2000 chars of model_answer + first gap deep-dive entry. Full content never sent to free-tier clients. |
| FR-011 | Real-time entitlement enforcement | `POST /api/sessions` uses atomic `UPDATE ... WHERE sessions_used < $limit`. Returns 403 with upgrade message if zero rows returned. |
| FR-012 | Payment processor integration | Stripe Checkout + Customer Portal + webhooks processed synchronously. See Section 10. |

### 4. Interview Experience

| FR | Requirement | Design |
|---|---|---|
| FR-013 | Real-time bidirectional connection | WebSocket at `WSS /api/sessions/:id/ws`. Cloud Run with 3600s timeout. See Section 3. |
| FR-014 | Pre-session config (duration 1-180 min, TTS toggle) | `POST /api/sessions` accepts `duration_minutes` (1-180) and `tts_enabled`. For sessions >55 min, preemptive reconnection at 55-min intervals. See Section 4. |
| FR-015 | Voice input (push-to-talk) and text input, both always available | `end_turn` accepts `input_method: "text"` or `"voice"`. Both always available in frontend. |
| FR-016 | Multi-segment voice recording | Client-side only. MediaRecorder buffers segments, concatenates on submit, sends single base64 `end_turn`. No server-side segment management. |
| FR-017 | STT transcription | Conductor transitions: `WaitingForInput` → `Transcribing` → `ProcessingInput`. Server sends `transcription_result` (read-only, no edit window). Transcription goes straight to the LLM for maximum fluidity. |
| FR-018 | Token-by-token streaming | Anthropic SDK `Messages.Stream()`. Each delta sent as `interviewer_token` via WebSocket. |
| FR-019 | TTS audio streamed concurrently with text | TTS accumulator observer fires TTS on sentence boundaries. `tts_chunk` messages sent concurrently with text tokens. See Section 5. |
| FR-020 | Timer with warning and overtime | Warning at `clamp(2, 5, round(duration / 9))` minutes remaining. Conductor sends `timer_warning` and `timer_overtime` via dedicated channels. Session auto-ends 2 minutes after overtime. Timer state recalculated from `started_at` on reconnect. |
| FR-021 | Session ends on candidate action | Client sends `end_session`. Conductor transitions to `Ending`. The interviewer prompts wrap-up via time-aware instructions but does not end the session; only the candidate or the 2-minute overtime cutoff ends it. |
| FR-022 | Connection drop: state preserved, can reconnect and resume | Messages persisted on every turn. Session stays `active` on disconnect. `session_init` with `last_seq` triggers `reconnect_state`. See Section 4, Reconnection. |
| FR-023 | No pause/resume, single sitting | No pause endpoint. Timer runs continuously from `started_at`. |
| FR-024 | Chrome and Safari minimum | Standard WebSocket API, MediaRecorder API (Opus/WebM). Supported in Chrome and Safari. |
| FR-025 | Mobile not required | Desktop-first SPA. No mobile-specific UI. |

### 5. Interviewer Behavior

| FR | Requirement | Design |
|---|---|---|
| FR-026 | Behave like senior staff engineer | System prompt in `internal/interview/prompt.go`. All FR-026 through FR-038 are product requirements encoded as prompt engineering. |
| FR-027 | Open with deliberately vague problem statement | Opening instruction in system prompt. |
| FR-028 | Stay silent when candidate should drive | Silence instruction in system prompt. |
| FR-029 | Answer clarifying questions collaboratively | Behavioral rules in system prompt. |
| FR-030 | Probe with "why" | Probing instruction in system prompt. |
| FR-031 | Introduce constraints at midpoint | Elapsed/remaining time injected into prompt each turn. Midpoint instruction activates based on time. |
| FR-032 | Track coverage across 7 areas | Coverage areas listed in system prompt. The LLM tracks coverage from the full transcript each turn. No server-side coverage state. |
| FR-033 | Push past hand-waving | Anti-hand-waving instruction in prompt. |
| FR-034 | Never validate design | Neutrality instruction in prompt. |
| FR-035 | Keep responses to 2-4 sentences | Length constraint in prompt. |
| FR-036 | Time-aware pacing | Elapsed/remaining time in prompt. Behavioral instructions change by phase. Builder's `WithTimeContext()`. |
| FR-037 | Never break character | Character instruction in prompt. |
| FR-038 | Accept coach briefing on weak areas | If `config_coach_briefing = true`, latest `coach_analyses` appended to system prompt via Builder's `WithCoachBriefing()`. |

### 6. Evaluation

| FR | Requirement | Design |
|---|---|---|
| FR-039 | Post-session structured assessment | `EvaluateSession` River job, transactionally enqueued when session completes. See Section 8. |
| FR-040 | 5 dimensions + overall, 1-5 scale | `evaluations` table with 6 score columns, all `CHECK BETWEEN 1 AND 5`. Rubric in evaluator prompt. |
| FR-041 | Score calibration (3 = borderline, 5 = rare) | Calibration guidance in evaluator system prompt with examples per score level. |
| FR-042 | Strengths, gaps with evidence, actionable advice | Evaluator prompt requests structured output. Stored in `evaluations.strengths` (JSONB), `.gaps`, `.advice`. |
| FR-043 | Per-message annotations | `annotations` table with FK to `evaluations` and `messages`. Types: strength, gap, missed_opportunity, note. |
| FR-044 | Semantic validation with retry | Worker validates: score variance, non-empty lists, valid annotation refs. Failure → River retries with backoff. |
| FR-045 | evaluation_failed status with manual retry | After max attempts → `status = 'evaluation_failed'`. `POST /api/sessions/:id/evaluate` re-enqueues. |
| FR-046 | Raw LLM response stored | `llm_call_content` table, written in same transaction as evaluation. |

### 7. Educator

| FR | Requirement | Design |
|---|---|---|
| FR-047 | Candidate requests deep analysis after evaluation | `POST /api/sessions/:id/educator` enqueues `GenerateEducatorContent` River job. Only available when status is `reviewed`. |
| FR-048 | Model Answer + Gap Deep-Dives | Stored as markdown in `educator_analyses.model_answer` and `.gap_deep_dives`. |
| FR-049 | Fresh, personalized per session | No caching. Educator prompt includes full transcript + evaluation for this specific session. |
| FR-050 | Rendered as formatted markdown | Frontend renders with react-markdown + remark-gfm. |
| FR-051 | Free tier sees preview only | API truncates content for free users. See FR-010. |
| FR-052 | Retry on failure | River job with `MaxAttempts: 5`, exponential backoff. Manual retry re-enqueues. |
| FR-053 | Raw LLM response stored | `llm_call_content`, same pattern as FR-046. |

### 8. Coach

| FR | Requirement | Design |
|---|---|---|
| FR-054 | Analyze complete history of reviewed, non-archived sessions | Coach prompt built from all `status = 'reviewed' AND archived = FALSE` sessions for the user. |
| FR-055 | Narrative + gap analysis + optional custom question | Parsed into `coach_analyses`: `narrative` (includes thinking patterns and metacognitive coaching as unstructured text), `weakest_dimension`, `improving_dimensions`, `topic_gaps`, optional `suggested_question_id`. |
| FR-056 | Coach-generated questions in personal question bank | Inserted into `questions` with `source = 'coach_generated'`, `user_id` set, `coach_rationale` populated. |
| FR-057 | Coach briefs interviewer | When `config_coach_briefing = true`, latest `coach_analyses` appended to interviewer prompt. See FR-038. |
| FR-058 | Debounced (no re-run if no new sessions) | `POST /api/coach/analyze` compares `sessions_analyzed` array against current reviewed sessions. Force via `?force=true`. |
| FR-059 | One concurrent analysis per user | River unique job constraint on `user_id`. |
| FR-060 | Raw LLM response stored | `llm_call_content`, same pattern. |

### 9. Question Bank

| FR | Requirement | Design |
|---|---|---|
| FR-061 | Global question bank, admin-managed | `questions` with `user_id = NULL` and `source = 'seed'`. |
| FR-062 | Question fields (title, prompt, difficulty, tags, hints) | All columns on `questions` table. `difficulty` CHECK to medium/hard. `tags` as TEXT[]. |
| FR-063 | Three sources: seed, custom, coach_generated | `source` CHECK constraint. |
| FR-064 | Custom and coach-generated are per-user | API query: `WHERE user_id IS NULL OR user_id = $current_user`. |
| FR-065 | Per-candidate stats (attempts, best score) | JOIN `questions` → `interview_sessions` → `evaluations`, grouped by question. |
| FR-066 | Coach's suggested question visually distinguished | `GET /api/coach/latest` returns `suggested_question_id`. Frontend marks it. |
| FR-067 | Filterable by difficulty and tags | API accepts `?difficulty=hard&tags=caching,databases`. sqlc with `sqlc.narg` for nullable filters; go-sqlbuilder for tag array overlap. |

### 10. Session Lifecycle & History

| FR | Requirement | Design |
|---|---|---|
| FR-068 | Status: active → completed → evaluating → reviewed / evaluation_failed | `interview_sessions.status` CHECK constraint. Transitions enforced in application code. |
| FR-069 | Archivable, soft-hidden, excluded from coach, restorable | `archived` boolean. Default views filter `WHERE archived = FALSE`. |
| FR-070 | Archive/unarchive individually or bulk | `PATCH /api/sessions/:id/archive` for single. `POST /api/sessions/archive-bulk` with `{ session_ids: [...] }` for bulk. |
| FR-071 | Full transcript preserved | `messages` table. Every message persisted immediately on creation. |
| FR-072 | Session metadata (duration, turns, timestamps, config) | All columns on `interview_sessions`. |
| FR-073 | History view with scores and metadata | `GET /api/sessions` joins `interview_sessions` + `evaluations` + `questions`. |
| FR-074 | Score trend visualization | Frontend chart plotting `score_overall` over `started_at`. |
| FR-075 | Sortable, filterable (active/archived/all) | API accepts `?archived=false&sort=created_at&order=desc`. |
| FR-076 | Permanent deletion designed for GDPR | No per-session deletion in UI. Deletion only through account deletion flow (FR-003) or admin action. |

### 11. Observability & Auditability

| FR | Requirement | Design |
|---|---|---|
| FR-077 | Every LLM request logged (role, prompt, response, model, tokens, cost, latency) | `llm_calls` for metrics, `llm_call_content` for full prompt/response. Written in same transaction. See Section 11. |
| FR-078 | Every user action logged | `user_events` table with `event_type` (session_start, turn_submitted, session_ended, evaluation_triggered, educator_requested, coach_triggered) and `metadata` JSONB. Page views excluded — low signal relative to other events. |
| FR-079 | Every system event logged | Structured JSON to stdout → Cloud Logging. All include trace_id, session_id, user_id. |
| FR-080 | Per-session and per-candidate cost breakdowns | `SELECT role, SUM(estimated_cost) FROM llm_calls WHERE session_id = $1 GROUP BY role`. |
| FR-081 | Latency at each stage | OTel spans: STT, LLM time-to-first-token, total LLM, TTS, end-to-end turn. `llm_calls.latency_ms`. |
| FR-082 | Full session reconstruction from logs | `messages` + `llm_calls`/`llm_call_content` + `user_events` + Cloud Logging = complete audit trail by session_id. |
| FR-083 | Observability data retained indefinitely | No TTL on tables. No expiration on Cloud Logging bucket. No GCS lifecycle rules. |
| FR-084 | Admin monitoring dashboards | `/admin/dashboard` with system health, usage, costs, errors, latency, River UI. |

### 12. Abuse Prevention & Rate Limiting

| FR | Requirement | Design |
|---|---|---|
| FR-085 | Rate limits on API and WebSocket | Per-user token bucket in Postgres. In-memory per-connection limit for WebSocket (ephemeral, dies with connection). See Section 13. |
| FR-086 | Real-time entitlement enforcement | Same as FR-011. Also checked at educator request and coach endpoints for feature gating. |
| FR-087 | Content moderation | Interviewer prompt includes jailbreak detection. Pre-LLM scan for injection patterns. Flagged messages logged to `user_events`. |
| FR-088 | Account abuse prevention | Email verification required. Account creation rate-limited per IP. Browser fingerprint flags suspicious patterns. Credential sharing detection not needed — billing model naturally disincentivizes it. |

### 13. Data Retention & Privacy

| FR | Requirement | Design |
|---|---|---|
| FR-089 | GDPR compliance | Lawful basis: contract + legitimate interest. Data export, erasure, consent implemented. See Section 12. |
| FR-090 | Indefinite storage unless GDPR deletion | No automatic expiration on any table or GCS object. |
| FR-091 | Audio recordings preserved | GCS at `audio/{session_id}/input/{message_id}.webm` (written inline by conductor after STT) and `output/{message_id}.mp3` (written by TTS accumulator in `OnDone`). Referenced via `messages.audio_url`. |
| FR-092 | All raw LLM responses preserved | `llm_call_content` as JSONB. Queryable via SQL joins. |
| FR-093 | Complete data export on request | `GET /api/me/export` → River job → ZIP → GCS signed URL → email link. Includes: profile, sessions, transcripts, evaluations, annotations, educator analyses, coach analyses, custom + coach-generated questions, audio files. Excludes: `llm_calls`/`llm_call_content`, `user_events`. |
| FR-094 | Deletion/anonymization for GDPR + audit integrity | Soft delete, 30-day grace, anonymize PII, retain anonymized aggregates. Cloud Logging not deleted (no PII after anonymization). |

### 14. Notifications

| FR | Requirement | Design |
|---|---|---|
| FR-095 | Email when evaluation complete, coach has new insights | `SendEmail` River job enqueued by evaluation and coach workers. Transactional email provider (Resend/Postmark/SES). |

### 15. Error Handling & Graceful Degradation

| FR | Requirement | Design |
|---|---|---|
| FR-096 | STT failure: retry, then text fallback | Retry with exponential backoff (3 attempts). On exhaustion, send `error` with code `stt_unavailable`. Client prompts text input. |
| FR-097 | TTS failure: retry, then text-only | Retry (3 attempts). On exhaustion, send `tts_unavailable`. Text responses continue normally. |
| FR-098 | Interviewer LLM failure: generous retry | Exponential backoff, initial 5s, up to 90s, 5 attempts. Partial response preserved. Client shows "thinking..." during retries. |
| FR-099 | Evaluation failure: retry, then evaluation_failed | River job `MaxAttempts: 4`, backoff starting 30s. On exhaustion → `evaluation_failed`. Manual retry via API. |
| FR-100 | Educator failure: retry, then manual retry | River job `MaxAttempts: 5`, backoff starting 30s. Manual retry re-enqueues. |
| FR-101 | WebSocket reconnection with generous backoff | Client: exponential backoff (1s initial, 30s max, with jitter). Server: state in Postgres, `reconnect_state` on reconnect. Preemptive `reconnect_please` at 55 min and on SIGTERM. |
| FR-102 | All errors logged, no silent failures, no inconsistent state | Structured logging with full context. Database transactions ensure atomicity. River jobs either succeed or retry; never partial state. |
