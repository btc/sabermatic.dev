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
    model_answer    TEXT NOT NULL,
    gap_deep_dives  TEXT NOT NULL,
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
CREATE INDEX idx_messages_session_seq ON messages(session_id, seq);
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

Sessions:
  POST   /api/sessions                 Start new session (entitlement check)
  GET    /api/sessions                 List (filterable, sortable)
  GET    /api/sessions/:id             Session detail
  PATCH  /api/sessions/:id/archive     Archive/unarchive
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
| `EditingTranscript` | Candidate reviewing/editing transcription |
| `ProcessingInput` | Preparing LLM call with candidate message |
| `Ending` | Teardown in progress |
| `Ended` | Terminal |

**State Transitions:**

```
InterviewerSpeaking → WaitingForInput, Ending
WaitingForInput     → Transcribing, ProcessingInput, Ending
Transcribing        → EditingTranscript, Ending
EditingTranscript   → ProcessingInput, Ending
ProcessingInput     → InterviewerSpeaking, Ending
Ending              → Ended
Ended               → (terminal)
```

**Client → Server:**

| Message | Payload | Queue |
|---|---|---|
| `session_init` | `{ last_seq: null \| N }` | Through queue |
| `end_turn` | `{ content, input_method: "text" }` or `{ audio, input_method: "voice" }` | Through queue |
| `transcription_edit` | `{ message_id, content }` | Through queue |
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
| `timer_warning` | `{ minutes_remaining: 5 }` |
| `timer_overtime` | (none) |
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

On `session_init` with `last_seq: null` (new session): the conductor sends `session_loaded` with session metadata, then streams the interviewer's opening message. On `session_init` with `last_seq: N` (reconnect): the conductor sends `reconnect_state` with all messages since seq N.

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

The `reconnectTimerCh` fires at the 55-minute mark. The conductor persists any in-flight partial response, sends `reconnect_please`, and closes the WebSocket. The client reconnects immediately, gets a fresh 60-minute Cloud Run window. For a 180-minute session, this happens ~3 times. The user sees at most a brief "Reconnecting..." banner.

### Turn Processing Sequence

```
WaitingForInput
  → if voice input:
      sm.Transition(Transcribing)
      call STT, send transcription_result
      sm.Transition(EditingTranscript)
      wait for edit timeout or transcription_edit
  → sm.Transition(ProcessingInput)
    persist candidate message to DB
    build prompt (via Builder)
    create observer fan-out (wsWriter + ttsAccumulator + messageAccumulator)
    sm.Transition(InterviewerSpeaking)
    stream LLM response through observers
    persist interviewer message to DB
    sm.Transition(WaitingForInput)
```

### Message Queue Bypass

The WebSocket read goroutine sends most messages into `msgCh`. The one exception: `cancel_tts` calls `c.observer.Interrupt()` directly. This cancels in-flight TTS API calls without waiting for the current turn to finish processing.

### Persistence

Every completed message (candidate and interviewer) is persisted to the `messages` table immediately. If the instance dies between turns, no data is lost. If it dies mid-stream, the accumulated text is persisted by `handleShutdown` (triggered by context cancellation on SIGTERM).

### Reconnection

**Network blip (instance alive):** Client disconnects. `readLoop` closes `msgCh`. Conductor detects disconnect. If mid-stream, tokens accumulate in the message accumulator but are not sent to the WebSocket. On reconnect, a new `readLoop` starts. The conductor sends `reconnect_state` with all messages since the client's last acknowledged seq.

**Instance death:** Session state is in Postgres. Client reconnects, hits a new instance, which creates a fresh conductor and loads state from DB. Any partial interviewer response from the crash is lost (a few seconds of text). The conductor detects an unanswered candidate turn and re-triggers the LLM call.

**Graceful deploy:** SIGTERM → conductor persists partial state → sends `reconnect_please` → client reconnects to new revision immediately, no backoff.

**Preemptive reconnect (55-min timer):** Same as graceful deploy, triggered by timer instead of SIGTERM. Enables sessions up to 180 minutes within Cloud Run's 60-minute request timeout.

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
- **TTSAccumulator** — buffers text, fires TTS API on sentence boundaries in a separate goroutine. `Interrupt()` cancels its internal context, aborting all in-flight and future TTS calls.
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
func (b *PromptBuilder) WithCoverageState(covered map[string]bool) *PromptBuilder { ... }
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

| Job | Queue | Timeout | Max Attempts |
|---|---|---|---|
| `EvaluateSession` | `ai` | 10 min | 4 |
| `GenerateEducatorContent` | `ai` | 15 min | 5 |
| `RunCoachAnalysis` | `ai` | 15 min | unique per user |
| `SendEmail` | `notifications` | 1 min | 3 |
| `TrackUsageMetrics` | `telemetry` | 30 sec | 3 |
| `ProcessAudioUpload` | `media` | 2 min | 3 |

Key properties:
- **Transactional enqueue** — evaluation job created in same transaction as session status update to `completed`. No orphaned jobs.
- **Exponential backoff** — configured per job type, handled by River.
- **Coach uniqueness** — River's built-in unique jobs feature prevents duplicate concurrent analyses per user.
- **River UI** at `/admin/jobs` behind admin auth for queue visibility.

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

Stripe Checkout for signup, Customer Portal for self-service, webhooks processed as River jobs.

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

Entitlements checked synchronously at session creation: `usage_periods.sessions_used` vs plan limit, `COUNT(active sessions)` vs concurrent limit, `config_duration_minutes` vs max duration.

Free-tier educator content: generated identically, but API returns truncated preview. Full content gated at the API layer.

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

- **Right of access / data portability**: `GET /api/me/export` enqueues River job → assembles all user data → ZIP → GCS signed URL → email link.
- **Right to erasure**: `DELETE /api/auth/account` soft-deletes (sets `deleted_at`). Daily River job processes accounts soft-deleted > 30 days: anonymize user record, delete audio from GCS, delete `llm_call_content` rows, retain anonymized scores for aggregate analytics.
- **Consent**: Cookie consent banner. Session cookies are essential (no consent required). Analytics cookies require opt-in.
- **Lawful basis**: Contract (user signed up). Legitimate interest (LLM call logging for service improvement). Audio recording disclosed at session start.

---

## 13. Security & Abuse Prevention

- **Entitlements**: Checked synchronously at session creation and educator request.
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
