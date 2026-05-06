# Tavus CVI voice conversation integration — design spec

**Status:** spec, round 4 — final (incorporating round-1, round-2, round-3 Opus review). Implementation begins after this round per the CLAUDE.md "up to 3 rounds" cap.
**Date:** 2026-05-06
**Goal:** Let an opted-in user run a Sabermatic interview against a Tavus replica avatar (live WebRTC video + audio) instead of the current push-to-talk OpenAI STT → Anthropic LLM → OpenAI TTS pipeline. Ship behind a feature flag; existing pipeline remains the default.

## Context

Today the interview flow is:

1. `web/src/pages/session-config.tsx` collects question, duration, TTS toggle → `SessionService.CreateSession` (`internal/rpc/session/server.go:128`).
2. `web/src/pages/interview.tsx` mounts. The user holds a button to record (`web/src/audio/recorder.ts`); on release the audio bytes are sent through `InterviewService.SubmitTurn` (`internal/rpc/interview/server.go:28`) as a streaming RPC.
3. Server-side: `internal/backend/turn.go` runs OpenAI Whisper STT → Anthropic streaming LLM → `internal/interview/observer/tts_accumulator.go` chunks tokens into sentences → OpenAI TTS → `TtsChunk` events back over the stream.
4. `web/src/audio/player.ts` decodes/plays the chunks.

Tavus CVI replaces steps 2–4 entirely. A Tavus *conversation* is an end-to-end pipeline: the browser joins a Daily.co WebRTC room; Tavus's servers run STT, run an LLM against a *persona* (system prompt + per-session context), drive a *replica* (the avatar) with TTS, and stream audio+video back. From the Sabermatic backend's perspective the live turn loop becomes opaque — we only orchestrate creation/teardown and consume webhook events.

Tavus does not publish (in any docs URL retrievable on this date) an enumeration of webhook signature/HMAC mechanics; this single fact dominates several design decisions below.

### Tavus API surface we'll use

- `POST https://tavusapi.com/v2/conversations` (header `x-api-key`). Body fields we set:
  - `persona_id` (required, pre-created at bootstrap time; not per-session)
  - `replica_id` (explicit so it's grep-able, even though persona has a default)
  - `conversation_name` (`sabermatic-{session_id}`)
  - `conversational_context` (per-session: question prompt + rubric, **bounded to 8000 chars**; see Open Q 5 / size policy)
  - `custom_greeting` (per-session: opener tied to the question)
  - `callback_url` — `cfg.Auth.BaseURL + "/api/webhooks/tavus"` (no secret in path; see Webhook section)
  - `properties.max_call_duration` = `config_duration_minutes * 60`
  - `properties.participant_absent_timeout` = `60` (seconds; tight to limit the cost-leak from in-app navigation; see L16/L18 resolution below)
  - `properties.enable_recording` = `true`
  Returns `conversation_id`, `conversation_url` (Daily.co URL), `status`.
- `POST /v2/conversations/{id}/end` to terminate.
- `GET /v2/conversations/{id}?verbose=true` for reconciliation. The reconciler reads only: `status` (terminal vs `active`), `shutdown_reason` (logged for ops), `events[]` (ignored — we trust webhook-delivered utterances and skip backfill in v1; documented as a known v1 gap).

### Persona and replica strategy

A persona encapsulates the system prompt + STT/TTS/LLM layer config; a replica is the avatar appearance/voice. Both are created once and reused across all conversations. Per-session context is injected via `conversational_context` at conversation-create time, not by creating a new persona per session.

Drift prevention between the committed Go source and the Tavus-stored persona:

1. The persona's system prompt is exported as a `const` from `internal/interview/prompt/persona.go`.
2. `scripts/cloud_bootstrap.py` shells `drillctl tavus-bootstrap` to create the persona via `POST /v2/personas` and persist the returned ID to Secret Manager.
3. App boot performs a `GET /v2/personas/{id}` and logs a warning (not fatal) if `system_prompt` differs from the constant.
4. **`drillctl tavus-persona-sync`** subcommand re-`PATCH`es the persona to match the constant. Operators run it after a deploy that changes the prompt. This is documented in `docs/tavus-bootstrap.md` and on the boot warning's slog line. (Round-2 M12 resolution.)

### Conversational context size

`conversational_context` is bounded server-side to **8000 chars** (resolves round-2 M14). v1 composition (round-3 M3 resolution — there is no rubric or few-shot data anywhere in the codebase today; `questions` rows have only `title`, `prompt`, `difficulty`, `hints`):

`conversational_context = question.prompt`

That is the entire composition for v1. The 8000-char cap is a forward-compat guardrail for when we add rubric/few-shot data; today no question prompt approaches 8000 chars. Validation lives in `internal/backend/tavus.go::BuildConversationalContext(question)`. Any prompt exceeding 8000 chars fails session creation with `connect.CodeFailedPrecondition` referencing the question_id, and an alert event `tavus_context_too_large` is emitted for ops. Multi-layer composition (rubric, few-shots) is explicitly deferred — when we add it, the size policy will need to grow into a real truncation order; we'll revise this section then.

Out of scope for v1: rotating personas per question category, custom replicas, persona A/B testing, backfilling missed utterances from `GET /v2/conversations/{id}.events[]`.

## Why a feature flag, not a replacement

1. The existing pipeline is the only voice path that's been browser-smoked in production; Tavus is unproven for this product.
2. Tavus minute pricing is materially higher than OpenAI TTS+Whisper. Until adoption is measured, the cheap path stays default.
3. The two pipelines persist different things to `messages` — the existing path writes per-turn rows from `turn.go`; Tavus writes via webhook utterance events. Persisting in parallel for one session would be confusing; behind a flag the two never run for the same session.

## Out of scope for v1

- Tavus tool-calling; custom LLM mode; replacing the existing pipeline; custom video tile UI; recording playback in the transcript page; cross-session memory (`memory_stores`); copying recordings to GCS.
- Pricing differentiation — v1 reserves SaberMinutes 1:1 with Tavus minutes (deliberate loss-leader). **Tavus mode is gated to users with a positive paid balance** (see "Paid-balance gating" below).
- UI toggle on session-config promoted to a persistent setting; v1 uses one-click toggle that writes `sessionStorage`.
- Backfilling missed utterances from the `GET /v2/conversations` events array. If a webhook is dropped and its data isn't recovered by re-delivery, that utterance is missing from the transcript. Documented limitation; revisit after measuring webhook reliability.

## Design

### Architecture

```
session-config.tsx                    interview.tsx
       │                                    │
       │ CreateSession(mode=TAVUS)          │ GetSession
       ▼                                    │
SessionService.CreateSession                │
  1. validate paid_balance > 0              │
  2. Tx#1: reserve minutes + insert         │
     row with status='generating',          │
     mode='tavus'                            ▼
  3. (no tx) call Tavus.CreateConversation  if Session.mode==TAVUS:
  4a. on success Tx#2: status='active',       <iframe src=Session.tavus_conversation_url/>
      tavus_conversation_id/url set         else:
  4b. on failure: FailSession (refund)        existing UI
  5. return Session

                          Tavus servers
                               │
                               │ webhook events (HMAC-verified)
                               ▼
                  POST /api/webhooks/tavus
                               │
                               ▼
                  HandleTavusEvent (per-session advisory lock):
                    - utterance         → INSERT msg if status='active'
                    - recording_ready   → UPDATE recording_url unconditionally
                    - shutdown          → conditional UPDATE status='active'->terminal,
                                          enqueue eval (in tx)

                  Reconciliation worker (River, every 60s):
                    SELECT ... FOR UPDATE SKIP LOCKED ORDER BY started_at LIMIT 25
                    For each: GetConversation, finalize if Tavus reports ended
```

User exit paths:

| Exit path | Backend trigger | Tavus call | Status |
|---|---|---|---|
| Click "End interview" | `InterviewService.EndSession` | `/end` best-effort, after local complete | `completed`, eval enqueued (same tx) |
| Close tab | iframe `unload`; nothing reaches us | none | Tavus emits `shutdown` after 60s absent → webhook reconciles |
| In-app navigation away | `useEffect` cleanup in `TavusInterview` | `EndConversation` best-effort | Same shutdown webhook path |
| Cancel | `InterviewService.CancelSession` | `/end` best-effort, after local cancel | `cancelled`, no eval, refund |
| Network blackhole | nothing | none | Tavus emits `shutdown` after 60s absent |
| Webhook never arrives | nothing | nothing | Reconciliation worker (60s tick) finalizes |
| Tavus `/end` 5xx during EndSession | local already-completed | logged | Webhook hits already-terminal status; no double-eval |

### Data model

Migration `015_tavus_session_fields.up.sql` (and matching `.down.sql`):

| Column on `interview_sessions` | Type | Notes |
|---|---|---|
| `mode` | `text NOT NULL DEFAULT 'standard' CHECK (mode IN ('standard','tavus'))` | enum |
| `tavus_conversation_id` | `text` | NULL unless mode='tavus'; populated by Tx#2 |
| `tavus_conversation_url` | `text` | Daily.co URL; bearer-equivalent (Open Q 3) |
| `tavus_recording_url` | `text` | Set by `recording_ready` webhook |
| `tavus_reconcile_attempts` | `int NOT NULL DEFAULT 0` | Round-2 M8/M9: per-row backoff counter |

The migration also extends the `interview_sessions.status` CHECK to add `'provisioning'` (round-3 H1 resolution; see immediately below) and adds two indexes:

- `CREATE INDEX idx_sessions_tavus_conversation_id ON interview_sessions(tavus_conversation_id) WHERE tavus_conversation_id IS NOT NULL;` — webhook lookup.
- `CREATE INDEX idx_sessions_tavus_reconcile ON interview_sessions(started_at) WHERE mode='tavus' AND status='active' AND tavus_reconcile_attempts < 10;` — reconciler hot path (round-3 M1 resolution).

Plus a third partial index (for Pass A of the reconciler — plan-review L4): `CREATE INDEX idx_sessions_tavus_provisioning ON interview_sessions(created_at) WHERE mode='tavus' AND status='provisioning';`.

Down migration drops all three indexes (`idx_sessions_tavus_conversation_id`, `idx_sessions_tavus_reconcile`, `idx_sessions_tavus_provisioning`), drops the new status CHECK constraint and re-adds the original (without `'provisioning'`), then drops the five columns. The migration must explicitly reference the constraint name `interview_sessions_status_check` (created in `001_initial.up.sql`).

`messages.input_method` is unconstrained text; we add `'tavus_voice'` as a value with no schema change.

`messages.role` is CHECK-constrained to `('interviewer','candidate')`. We deliberately map Tavus `replica`→`interviewer` and Tavus `user`→`candidate` so existing constraint, evaluation prompt, and transcript page all keep working unchanged. Do **not** extend the constraint to add `'replica'`/`'user'`.

We **introduce a new status `'provisioning'`** for the brief Tx#1↔Tx#2 window (round-3 H1 resolution). Round-2 v3 had proposed reusing `SESSION_STATUS_GENERATING`, but `'generating'` is actively managed by `internal/jobs/cleanup_generating.go:CleanupStaleGeneratingWorker` and `internal/backend/session.go:586:waitForGeneration`, both of which inline-recover stale `'generating'` rows back to `'active'`. A Tavus row stuck in `'generating'` would be silently flipped to `'active'` with `tavus_conversation_id=NULL`, then Tx#2's `WHERE status='generating'` no-ops, and the row leaks (the reconciler can't recover it because `tavus_conversation_id` is NULL forever).

The new `'provisioning'` status:

- Added to the `interview_sessions.status` CHECK constraint in migration 015.
- Added to the `SessionStatus` proto enum as `SESSION_STATUS_PROVISIONING = 9`.
- Frontend treats it as equivalent to `SESSION_STATUS_GENERATING` for UI purposes (renders "preparing your interview"); the existing generating-state component is reused via `if (status === GENERATING || status === PROVISIONING)`.
- Excluded from `CleanupStaleGenerating` (it has its own cleanup path via the reconciler — see below).
- The reconciler picks up rows stuck in `'provisioning'` for >5 min (round-3 H2 resolution): for each, calls `Backend.FailSession` (refunds via existing path). We do **not** attempt to look up potentially-orphaned Tavus conversations by name — Tavus's `participant_absent_timeout=60s` means orphaned Tavus-side rooms self-terminate within 60s of creation if no participant joins, so the leak surface is bounded to one minute of Tavus billing per crash.

### Backend

**New package `internal/tavus/`** (mirrors `internal/billing/`):

- `client.go`: `Client` struct wrapping `http.Client` + injectable `BaseURL`. Methods:
  - `CreateConversation(ctx, CreateConversationRequest) (*Conversation, error)`
  - `EndConversation(ctx, conversationID string) error`
  - `GetConversation(ctx, conversationID string) (*Conversation, error)` — used by reconciler. The returned `Conversation` value includes `Status`, `ShutdownReason`, `RecordingURL` (typed; `events[]` deliberately omitted — we don't backfill in v1).
  All requests sign with `x-api-key`. Surface `ErrUnavailable` (5xx, network), `ErrBadRequest` (4xx), `ErrRateLimited` (429 specifically; round-2 M8).
- `webhook.go`: `Event` typed sub-payloads per `event_type`; `ParseEvent(body []byte) (Event, error)`; `VerifySignature(body []byte, header string, secret string) error` — HMAC-SHA256 constant-time compare via `hmac.Equal`.
- `types.go`: `ConversationMode`, `Conversation`, `Persona`, `Replica`.
- `tavustest/`: helper package per CLAUDE.md convention. Single export: `NewFakeClient(t *testing.T, opts ...Option) *Client`. The helper internally spins up an `httptest.Server` and registers `t.Cleanup(srv.Close)`. Round-2 N22 resolution.

**Webhook signature verification.** Tavus's signature mechanism is not retrievable from public docs on this date. We commit to:

1. **Implementation prerequisite for C2:** before merging, send one test webhook to a request-bin and inspect the headers. If a header named anything like `X-Tavus-Signature` is present, implement HMAC-SHA256 verification using `cfg.Tavus.WebhookSecret`. If absent, **escalate to user** for decision before merging — do not ship URL-path-secret as a silent fallback.
2. **Mounted route:** `POST /api/webhooks/tavus` (no path secret; round-1 H1).
3. **Status codes (matches `internal/handler/stripe.go` pattern; round-2 M7):**
  - `200` on success.
  - `200` on missing `WebhookSecret` config (avoid retry storms; matches Stripe).
  - `400` on signature verification failure (matches Stripe — caller isn't Tavus).
  - `400` on body parse failure.
  - `500` on dispatch error (Tavus retries — same as Stripe transient handling).
  - Unknown event_type returns `200` (forward-compat). Note this is *intentionally* the same code as success; the dispatcher logs the unknown type for ops visibility.

**Config** (`internal/config/config.go`):

```go
type Tavus struct {
    Enabled        bool          `env:"TAVUS_ENABLED,default=false"`
    APIKey         string        `env:"TAVUS_API_KEY"`
    PersonaID      string        `env:"TAVUS_PERSONA_ID"`
    ReplicaID      string        `env:"TAVUS_REPLICA_ID"`
    WebhookSecret  string        `env:"TAVUS_WEBHOOK_SECRET"`
    BaseURL        string        `env:"TAVUS_BASE_URL,default=https://tavusapi.com"`
    RequestTimeout time.Duration `env:"TAVUS_REQUEST_TIMEOUT,default=15s"`
}
```

`Config.Validate()` with `Enabled=true`: the four secrets must be non-empty; return error from `cmd/drill/main.go` (per CLAUDE.md: never panic at init).

**Paid-balance gating.** New sqlc query in `sql/queries/grants.sql`:

```sql
-- name: HasActivePaidBalance :one
-- A user "has paid balance" iff they have non-zero remaining minutes from a non-free
-- grant (subscription, purchase, or admin) that hasn't expired. This is the v1 gate
-- for Tavus mode access.
SELECT EXISTS (
  SELECT 1 FROM grants
   WHERE user_id = $1
     AND remaining_minutes > 0
     AND source <> 'free_grant'
     AND (expires_at IS NULL OR expires_at > NOW())
) AS has_paid_balance;
```

Source-of-truth choice: paid *balance > 0*, not "ever had a paid grant." This means a user who buys a pack and exhausts it loses Tavus access until next purchase. Documented behavior. Round-2 H1 resolution.

`source='admin'` qualifies as paid (admin grants exist for staff/comp accounts that should get the full feature surface). Documented inline in the query comment.

**`SessionService.CreateSession`** — split-commit (round-1 H4 + round-2 H4 resolutions):

1. Validate request. If `mode=SESSION_MODE_TAVUS && !cfg.Tavus.Enabled`: `connect.CodeFailedPrecondition`.
2. Coerce `SESSION_MODE_UNSPECIFIED → SESSION_MODE_STANDARD` on input.
3. If `mode=tavus`: call `HasActivePaidBalance`; on false return `connect.CodePermissionDenied`.
4. **Tx#1:** reserve minutes + INSERT session row with `mode='tavus', status='provisioning', tavus_conversation_id=NULL`. Commit. (`'provisioning'` is the new status added by migration 015; see Data model.)
5. (No tx open) call `tavus.Client.CreateConversation(ctx, ...)` with body composed via `BuildConversationalContext`.
6. **Tx#2 on success:** `UPDATE interview_sessions SET status='active', tavus_conversation_id=$1, tavus_conversation_url=$2 WHERE id=$3 AND status='provisioning'`. Commit. Emit `session_created` (existing) with `slog.String("mode","tavus")` attr. Return `Session`.
7. **On Tavus failure:** call `Backend.FailSession(ctx, sessionID)` (refunds via existing path). Emit `tavus_provider_call_failed` `{operation:"create", http_status, error_class}`. Return `connect.CodeUnavailable`.

`GetSession` returns the `Session` proto including `mode` and `tavus_conversation_url`. Server always returns `SESSION_MODE_STANDARD` (not UNSPECIFIED) for legacy rows.

**`InterviewService.EndSession`** Tavus path (round-2 M15: use `status='active'` consistently everywhere):

1. **Tx:** `UPDATE interview_sessions SET status='completed', ended_at=NOW() WHERE id=$1 AND status='active' RETURNING tavus_conversation_id`.
2. If RowsAffected == 1: `b.jobs.InsertTx(ctx, tx, EvaluateSessionArgs{...})` in same tx.
3. Commit.
4. If we have a `tavus_conversation_id`, call `tavus.Client.EndConversation` best-effort (5s timeout). Errors logged via `tavus_provider_call_failed` `{operation:"end"}`.
5. If RowsAffected == 0: session is already terminal — return success without re-acting (idempotent EndSession).

**`CancelSession`** Tavus path: same pattern but `status='cancelled'`, refund minutes, no eval enqueue, then Tavus `/end` best-effort.

**Webhook handler** (`internal/handler/tavus.go`, `POST /api/webhooks/tavus`):

1. Read body (limit 64KB).
2. If `cfg.Tavus.WebhookSecret == ""`: log + `200`.
3. Verify HMAC; on failure: `400` (matches Stripe).
4. `tavus.ParseEvent(body)`; on failure: `400`.
5. `Backend.HandleTavusEvent(ctx, event)`; on error: `500`. Else: `200`.

**`Backend.HandleTavusEvent`** (`internal/backend/tavus.go`):

- `application.recording_ready`: `UPDATE interview_sessions SET tavus_recording_url=$1 WHERE tavus_conversation_id=$2`. **No status guard**: recordings can legitimately arrive after eval; the URL is a stored artifact, not a live signal. Documented in code comment.
- `conversation.utterance` (both `replica` and `user` roles):
  1. Look up session by `tavus_conversation_id`.
  2. Open tx; acquire transaction-scoped advisory lock with two-arg form (round-2 M6 resolution): `SELECT pg_advisory_xact_lock(hashtext('tavus_session')::int, hashtext(session_id::text)::int)`. The classid `hashtext('tavus_session')` namespaces this lock so future advisory-lock callers in other features don't collide. **Both utterance and shutdown handlers take this lock** (round-3 H3 resolution); the lock serializes the entire utterance↔shutdown race, not just utterance↔utterance.
  3. **Lock convention:** any new advisory-lock call must claim a unique classid; we document this in `docs/superpowers/CONVENTIONS.md` (a new file).
  4. INSERT message with status guard folded in (round-3 H3 belt-and-braces — even with the lock, the conditional INSERT prevents accidental insert if a future caller forgets the lock):
     ```sql
     INSERT INTO messages (session_id, seq, role, content, input_method)
     SELECT $1, COALESCE((SELECT MAX(seq) FROM messages WHERE session_id=$1), 0)+1, $2, $3, 'tavus_voice'
     WHERE EXISTS (SELECT 1 FROM interview_sessions WHERE id=$1 AND status='active');
     ```
     If RowsAffected == 0: utterance arrived after session terminated; drop silently. Role mapped (replica→interviewer, user→candidate).
  5. Commit.
- `system.shutdown`:
  1. Open tx; acquire the **same** per-session advisory lock as the utterance handler (round-3 H3): `SELECT pg_advisory_xact_lock(hashtext('tavus_session')::int, hashtext(session_id::text)::int)`. This serializes shutdown against in-flight utterance INSERTs.
  2. Conditional `UPDATE interview_sessions SET status=$new_status, ended_at=NOW() WHERE id=$id AND status='active'` (round-2 M15: only `'active'`, not "NOT IN terminal"). Use a CTE to also fetch `candidate_count = (SELECT COUNT(*) FROM messages WHERE session_id=$id AND role='candidate')`.
  3. RowsAffected == 0: tx rollback; user or prior webhook already finalized this session. No double-eval.
  4. RowsAffected == 1 + candidate_count == 0: status set to `cancelled`, call `FullRefundSessionMinutes` in same tx. No eval.
  5. RowsAffected == 1 + candidate_count > 0: status set to `completed`, `b.jobs.InsertTx` enqueue eval.
  6. Commit. Emit `session_ended` with `{provider:"tavus", reason:"tavus_shutdown"}`.
- Unknown `event_type`: log info, return nil.

**Reconciliation worker** (`internal/jobs/reconcile_tavus.go`) — registered into the existing `PeriodicJobs` slice in `internal/backend/backend.go:140` (round-2 L17 wording fix):

`ReconcileTavusSessionsArgs` runs every 60s. Two passes per tick:

**Pass A — stranded provisioning rows** (round-3 H2 resolution): rows where `mode='tavus' AND status='provisioning' AND created_at < NOW() - interval '5 minutes' ORDER BY created_at ASC LIMIT 25 FOR UPDATE SKIP LOCKED`. For each, call `Backend.FailSession` (refunds via existing path). We do **not** try to discover potentially-orphaned Tavus conversations by name — `participant_absent_timeout=60s` bounds the orphan-side leak. Logged warning includes a hint pointing at `drillctl tavus-orphan-scan` (an ops-only subcommand documented in `docs/tavus-bootstrap.md` for manual cleanup if leak rates ever justify it).

**Pass B — overdue active rows:**

1. `SELECT id, tavus_conversation_id FROM interview_sessions WHERE mode='tavus' AND status='active' AND tavus_conversation_id IS NOT NULL AND started_at < NOW() - (config_duration_minutes * interval '1 minute' + interval '5 minutes') AND tavus_reconcile_attempts < 10 ORDER BY started_at ASC LIMIT 25 FOR UPDATE SKIP LOCKED`. The `SKIP LOCKED` clause prevents two concurrent worker invocations from double-finalizing (round-2 M9); the attempts counter prevents permanently-broken rows from being checked forever (round-2 M8). The selection is index-backed by the new `idx_sessions_tavus_reconcile` partial index (round-3 M1).
2. Use `started_at`, not `created_at`, because Tavus rooms only burn time once a participant joins; sessions that never started have already been handled by Pass A or by the existing `CleanupAbandonedSessionsWorker`.
3. Bounded concurrency (`semaphore.NewWeighted(5)`) for the Tavus `GetConversation` calls within the batch (round-2 M8).
4. For each row: call `tavus.Client.GetConversation`. If `ErrRateLimited`, increment `tavus_reconcile_attempts`, commit, exit early (try next tick). If `ErrUnavailable`, increment attempts, continue to next row.
5. If `Status` is non-`'active'`: synthesize a `system.shutdown` event and dispatch through `Backend.HandleTavusEvent` so the same lock + conditional-UPDATE path runs.
6. Commit. Log per-tick metrics: `{provisioning_failed, rows_examined, rows_finalized, rate_limited}`.

Also extend `internal/jobs/cleanup.go:CleanupAbandonedSessionsWorker`: for each row it cancels or completes that has a non-NULL `tavus_conversation_id`, call `tavus.Client.EndConversation` after the tx commits. Best-effort (logged failure does not abort cleanup).

### Proto

**Move CLAUDE.md edit out of C0** into its own micro-commit C-1 (round-2 M13 + round-3 M6 — verbatim text spelled out below). Lands first as a stable contract.

CLAUDE.md line 31 currently reads:

> `ConnectRPC handlers live in `internal/rpc/{service}/`. REST handlers in `internal/handler/` are limited to health checks, OAuth flows, and Stripe webhooks — endpoints that are inherently HTTP-level. All resource RPCs use ConnectRPC.`

C-1 replaces it with:

> `ConnectRPC handlers live in `internal/rpc/{service}/`. REST handlers in `internal/handler/` are limited to health checks, OAuth flows, and third-party webhooks (e.g. Stripe, Tavus) — endpoints that are inherently HTTP-level (raw body required, status-code-as-protocol, signed by the provider). All resource RPCs use ConnectRPC.`

The change rephrases a closed enumeration as an open category and documents the three properties that justify the carve-out. Future webhook handlers (Mailgun events, GitHub webhooks, etc.) can land without further CLAUDE.md edits.

Then in C0, edit `pb/drill/v1/session.proto`:

```proto
enum SessionMode {
  SESSION_MODE_UNSPECIFIED = 0;  // Server coerces to STANDARD on input; never returned on output.
  SESSION_MODE_STANDARD    = 1;
  SESSION_MODE_TAVUS       = 2;
}

message CreateSessionRequest {
  string question_id      = 1;
  int32  duration_minutes = 2;
  bool   tts_enabled      = 3;
  SessionMode mode        = 4;
}

message Session {
  // ... fields 1..18 unchanged
  SessionMode mode                  = 19;
  string      tavus_conversation_url = 20;
}

message SessionSummary {
  // ... fields 1..13 unchanged
  SessionMode mode = 14;
}
```

Field numbers verified next-free against current proto (round-2 N23).

**New `pb/drill/v1/system.proto`** — AIP-131 `Get` shape on a `System` resource (round-2 H3 + round-3 M5 resolution):

```proto
service SystemService {
  // AIP-131: returns the singleton System resource describing server-side capabilities.
  rpc GetSystem(GetSystemRequest) returns (System);
}

message GetSystemRequest {
  // AIP-131 singleton convention: must be the literal string "system".
  // Validated server-side; non-"system" values return InvalidArgument.
  string name = 1;
}

message System {
  // Globally-enabled capabilities. Per-user gating happens at request time
  // (e.g., Tavus mode also checks paid balance in CreateSession).
  bool tavus_available = 1;
}
```

Mount in `internal/rpc/register.go` with `opts` (authed; round-2 H2 — capabilities are not public). The `tavus_available` field is global (mirrors `cfg.Tavus.Enabled`); per-user paid-balance gating is enforced at `CreateSession` time only, so the response is stable per-deploy and `staleTime: Infinity` on the React Query side is safe.

After editing protos: `buf generate` and commit `internal/pb/`, `web/src/pb/`. Per CLAUDE.md: never hand-edit generated files.

### Frontend

**New module `web/src/tavus/`**:

- `interview-view.tsx`: `<TavusInterview session={...}>` renders a full-bleed iframe:
  ```tsx
  <iframe
    src={session.tavus_conversation_url}
    allow="camera; microphone; autoplay; display-capture"
    className="h-full w-full border-0"
    title="Tavus interview"
  />
  ```
  Plus an "End interview" button that calls `EndSession` and navigates to the transcript.
  **No `useEffect` cleanup that fires `EndConversation`** (resolves round-3 M2 + plan-review M2). The earlier `didMountRef` pattern was logically broken: a useRef-based guard cannot reliably distinguish StrictMode's synthetic unmount from a real unmount, since the cleanup runs in both cases with the ref re-set on the second mount. Workable alternatives (`pagehide` listener, route-change blocker) all add complexity and edge cases.

  Instead we **rely solely on Tavus's `participant_absent_timeout=60s`** for navigation-away cleanup. The user's exit paths reduce to:

  - **Click "End interview"**: explicit `EndSession` RPC → backend calls Tavus `/end`. Cleanest path; user is on the transcript page within seconds.
  - **Anything else** (close tab, in-app navigation, force quit, network blackhole): the iframe disconnects from the Daily room; Tavus shuts the room down 60s after the last participant leaves; the `system.shutdown` webhook reaches us; reconciler is the backstop.

  Cost-leak ceiling is 60s of Tavus minutes per non-explicit exit. That's acceptable for v1 and removes a fragile React pattern.
  Lazy-loaded at the call site via `React.lazy(() => import('@/tavus/interview-view'))`.
- `__tests__/interview-view.test.tsx`: renders, asserts iframe src + `allow` attrs, asserts End button calls the `EndSession` mutation. Asserts unmount does **not** fire any `EndConversation` mutation (regression guard against re-introducing the broken cleanup pattern).

**iframe permissions and CSP** (round-2 M10 + round-3 M4 resolution):

The Tavus iframe origin is potentially a per-tenant or per-conversation Daily.co subdomain; **C1 will probe the actual returned `conversation_url` host alongside the webhook signature probe**. Three branches based on what we find:

- **If single fixed origin** (e.g., `tavus.daily.co`): backend serves `Permissions-Policy: camera=(self https://tavus.daily.co), microphone=(self https://tavus.daily.co), display-capture=(self https://tavus.daily.co)` (round-3 M4 — origin tokens are bare URLs, not double-quoted). CSP `frame-src 'self' https://tavus.daily.co`.
- **If wildcard subdomains under `*.daily.co`**: drop the document-level `Permissions-Policy` header (the spec does not support host wildcards in origin allowlists). Rely solely on the iframe's `allow="camera; microphone; autoplay; display-capture"` attribute, which delegates by attribute rather than by origin. CSP `frame-src 'self' https://*.daily.co` (CSP *does* support host wildcards).
- **If something else entirely**: escalate to user before merging C6.

The header logic and CSP additions live in `internal/handler/spa.go`, gated on `cfg.Tavus.Enabled` so non-Tavus deploys are unaffected.

No `sandbox` attribute on the iframe — Daily.co requires same-origin tokens and fails inside a sandboxed iframe in our testing pattern.

**Capability flag**:

- `web/src/api/queries.ts` adds `useSystem()` over the new `SystemService.GetSystem` (`staleTime: Infinity`).
- `web/src/pages/session-config.tsx`:
  - When `system.tavus_available && billing.paid_balance > 0` (read from existing `BillingService` query), render an "Enable Tavus interviewer" toggle that writes `sessionStorage['sabermatic.tavus_mode']='1'` (round-1 L20: `sessionStorage`, not URL param).
  - On submit: if the flag is set, send `mode: SESSION_MODE_TAVUS`. Cleared on logout.
- `web/src/pages/interview.tsx`:
  ```tsx
  const TavusInterview = lazy(() => import('@/tavus/interview-view'));
  if (session.mode === SessionMode.TAVUS) {
    return <Suspense fallback={<LoadingSpinner/>}><TavusInterview session={session} /></Suspense>;
  }
  ```

**Test pattern for the lazy branch** (round-2 M11): `vi.mock('@/tavus/interview-view', () => ({ default: () => <div data-testid="tavus-interview"/> }))` at file top (hoisted). Tests then `await waitFor(() => screen.getByTestId('tavus-interview'))`. Path alias `@/` is configured in `web/vitest.config.ts` (verify before C6).

### Telemetry

Reuse existing event names (round-1 review M18 resolved):

- `session_created` already emitted (`internal/backend/session.go:111`); add `slog.String("mode","tavus")` attr.
- `session_ended` already emitted (multiple sites); add `slog.String("provider","tavus")` attr alongside the existing `reason` attr.
- `tavus_provider_call_failed` (operations event, distinct from user funnel) — `{operation, http_status, error_class}`.
- `tavus_context_too_large` — `{question_id, context_length}` for Open Q 5 / size policy.

### Infrastructure

- `.env.example`: append `TAVUS_ENABLED`, `TAVUS_API_KEY`, `TAVUS_PERSONA_ID`, `TAVUS_REPLICA_ID`, `TAVUS_WEBHOOK_SECRET`, `TAVUS_BASE_URL` with comments.
- `scripts/cloud_bootstrap.py`: prompt for the four secrets; run `drillctl tavus-bootstrap`.
- `terraform/`: Cloud Run env entries for `TAVUS_ENABLED` + `TAVUS_BASE_URL`; Secret Manager refs for the four secrets.
- `docs/tavus-bootstrap.md` (new): account creation; `drillctl tavus-bootstrap`; webhook secret configuration; `drillctl tavus-persona-sync` after prompt changes; local-dev tunnel via cloudflared/ngrok; webhook URL = `${BASE_URL}/api/webhooks/tavus`.
- `docs/superpowers/CONVENTIONS.md` (new): documents the advisory-lock classid registry. Lands in C2 alongside the first user.

### Tests

- `internal/tavus/client_test.go`: create/end/get against `httptest.Server`; rate-limit (429) error mapping.
- `internal/tavus/webhook_test.go`: HMAC verify happy/sad/tampered; golden-JSON parse for each event type.
- `internal/tavus/tavustest/`: helper package per CLAUDE.md.
- `internal/rpc/session/server_test.go`: extend `TestCreateSession` with cases for `Enabled=false` rejection, no-paid-balance rejection, Tavus 5xx → FailSession path with refund verification, success → row has Tavus columns set + status flips `generating`→`active`.
- `internal/handler/tavus_test.go`: mirrors `stripe_test.go` — 200 (missing secret config), 400 (bad HMAC), 400 (bad body), 200 (each event), 500 (dispatch error). DB side-effects asserted.
- `internal/backend/tavus_test.go`:
  - **Concurrent utterance test** (round-2 L20): two distinct sessions, 50 parallel inserts each, assert no `UNIQUE` violations and `seq` is dense per session.
  - **Negative test** (red-green per CLAUDE.md): same scenario *without* the advisory lock — assert it fails. Proves the lock does real work.
  - **Utterance↔shutdown race test** (round-3 H3): N parallel goroutines, half firing `conversation.utterance` and half firing `system.shutdown` for the same session, assert: (a) no inserts land after the shutdown's terminal status flip, (b) no UNIQUE violations on `messages.seq`, (c) at most one evaluation enqueued. Run with the lock removed to confirm the test fails — the test must be sensitive to the H3 race, not just to the H5 race.
  - `system.shutdown` for already-completed session is a no-op.
  - `system.shutdown` with zero candidate utterances → `cancelled` + refund.
  - Late `conversation.utterance` after shutdown is dropped (status guard test, exercises the `WHERE EXISTS (... AND status='active')` clause).
- `internal/jobs/reconcile_tavus_test.go`: bounded concurrency assertion; 429 short-circuit; `SKIP LOCKED` parallel-worker safety; **Pass A** stranded `'provisioning'` row (older than 5 min) is `FailSession`'d and minutes refunded.
- `internal/jobs/cleanup_test.go`: extend to assert Tavus `/end` is called for Tavus-mode rows.
- `internal/rpc/interview/server_test.go`: `TestEndSession_TavusMode_Idempotent` (second EndSession is a no-op), `TestEndSession_TavusMode_TavusUnavailable` (local completion succeeds even when Tavus 5xxs).
- Frontend:
  - `web/src/tavus/__tests__/interview-view.test.tsx` covers iframe attrs + End button + asserts unmount does NOT fire EndConversation.
  - `web/src/pages/__tests__/interview.test.tsx` covers the lazy-loaded branch using `vi.mock` per test pattern above.
  - `web/src/pages/__tests__/session-config.test.tsx` covers toggle + sessionStorage + paid-balance gate.

## Open questions to resolve during implementation

1. **Webhook signature header.** Per the Webhook section: a request-bin probe is the first task in C1. If no HMAC mechanism exists, escalate to user.

2. **Persona system-prompt drift detection — fatal or warning?** v1 ships warning-only; ops uses `drillctl tavus-persona-sync` to remediate. Promotion to fatal requires confirming the prompt isn't routinely tweaked under real ops; revisit after a month.

3. **`tavus_conversation_url` is bearer-equivalent.** Anyone with the Daily.co URL can join the room. v1 acceptable risk: surface is browser memory + React Query cache + DevTools — not network logs. v2 enables `require_auth=true` and mints per-user join tokens.

4. **Tavus pricing → SaberMinute mapping.** Out of scope; revisit after one week of usage data.

5. **`conversational_context` size.** Hard cap 8000 chars; truncation order question-prompt > rubric > few-shots; oversized question_id fails with `tavus_context_too_large` event. Probe Tavus's actual limit during C1 to confirm 8000 is conservative.

## Implementation order

Nine commits, each independently mergeable and reviewable:

- **C-1 — CLAUDE.md edit** (round-2 M13): broaden `internal/handler/` carve-out wording. Lands first as a stable contract.
- **C0 — Schema + config + proto.** Migration 015 (up + down): adds `mode`, `tavus_conversation_id`, `tavus_conversation_url`, `tavus_recording_url`, `tavus_reconcile_attempts` columns; extends `interview_sessions.status` CHECK to add `'provisioning'`; adds `idx_sessions_tavus_conversation_id` and `idx_sessions_tavus_reconcile` indexes. `Tavus` config struct + Validate. `SessionMode` enum + `mode`/`tavus_conversation_url` on `Session`/`SessionSummary`. `SessionStatus` enum extended with `SESSION_STATUS_PROVISIONING = 9`. New `SystemService` proto + handler stub returning `tavus_available` (mounted under authed `opts`). Codegen.
- **C1 — Tavus client package.** `internal/tavus/{client,webhook,types}.go` + tests + `tavustest/`. Includes the request-bin probe (Open Q 1). Pure library.
- **C2 — Webhook handler + dispatcher + conventions doc.** `internal/handler/tavus.go`, route mounted, `internal/backend/tavus.go` dispatcher with advisory-lock + status-guarded utterance INSERT + conditional shutdown UPDATE. `docs/superpowers/CONVENTIONS.md` documents the advisory-lock classid registry (round-2 M6). C2 has runtime dep on C0's schema (called out in the PR description).
- **C3 — `SessionService.CreateSession` Tavus branch.** Split-commit logic, `HasActivePaidBalance` query, `BuildConversationalContext` with size policy.
- **C4 — `InterviewService.EndSession` + `CancelSession`.** Conditional UPDATE, idempotent EndSession, best-effort Tavus `/end`.
- **C5 — Reconciliation worker + cleanup extension.** `ReconcileTavusSessions` River job (`SKIP LOCKED`, attempts counter, bounded concurrency); `CleanupAbandonedSessionsWorker` calls Tavus `/end` for Tavus rows.
- **C6 — Frontend.** `web/src/tavus/` + lazy load + `Suspense` + `useEffect` cleanup; `useSystem()` hook; session-config toggle (sessionStorage + paid-balance check); interview-page branch; SPA `Permissions-Policy` + `frame-src` CSP gated on `Tavus.Enabled`.
- **C7 — Bootstrap + docs.** `drillctl tavus-bootstrap` + `tavus-persona-sync` subcommands, `cloud_bootstrap.py` integration, `docs/tavus-bootstrap.md`, `.env.example`, terraform.

Dependencies:
- C-1 has no deps; trivial doc edit.
- C0 depends on C-1 (just for the policy reference in C2); C0 itself touches schema/proto only.
- C1 has no deps on C0 (pure library).
- C2 depends on C1 + C0 (schema for tests).
- C3 depends on C1 + C0.
- C4 depends on C3.
- C5 depends on C1 + C2.
- C6 depends on C0.
- C7 depends on all previous.

C0 and C1 can run in parallel; C5 and C6 can run in parallel after C4.

## Success criteria

- [ ] `make test` passes.
- [ ] With `TAVUS_ENABLED=false` (default), the existing flow is bit-for-bit unchanged.
- [ ] With `TAVUS_ENABLED=true` and valid credentials, browse `/sessions/new` as a paid user, click "Enable Tavus interviewer," complete a short interview:
  - Daily room iframe loads, replica avatar visible
  - Conversation transcribed into `messages` with `input_method='tavus_voice'`
  - "End interview" terminates the conversation in the Tavus dashboard
  - `session_ended` event includes `provider=tavus` attribute in BQ
- [ ] With `TAVUS_ENABLED=true` and `mode=standard`, the existing flow still works.
- [ ] Free-grant user attempting `mode=tavus` gets `PermissionDenied`.
- [ ] Reconciliation worker picks up an artificially-stranded Tavus session and finalizes it (manual: insert a row with `started_at = NOW() - 2h`, run worker, observe).
- [ ] Webhook handler returns 200 for unknown event types, 400 for tampered HMAC, 200 for missing-secret config.
- [ ] Concurrent utterance webhook deliveries for the same session never produce a `UNIQUE` violation; the negative test (no lock) demonstrates the lock is required.
- [ ] In-app navigation away from the interview page eventually triggers a Tavus `system.shutdown` webhook within ~60s of `participant_absent_timeout` (verified by webhook log + Tavus dashboard).
- [ ] CLAUDE.md updated with third-party-webhooks carve-out.
- [ ] `docs/tavus-bootstrap.md` is complete enough that a fresh dev can run the bootstrap end-to-end.
- [ ] `docs/superpowers/CONVENTIONS.md` documents the advisory-lock classid registry.

## Risk

Medium-low. Each commit lands incrementally with the existing flow as default. Material risks:

1. **Webhook signature unknown until probe** (Open Q 1). Escalate to user if no HMAC.
2. **Reconciliation worker correctness.** If `GetConversation` doesn't reliably reflect `ended` status, sessions leak active. Mitigated by `CleanupAbandonedSessionsWorker` as backstop and the conditional-UPDATE preventing double-finalization.
3. **iframe permissions delegation.** Wrong CSP/Permissions-Policy means mic/camera prompts fail in production. Verify in browser smoke before declaring done.
4. **In-app navigation cleanup.** The `useEffect` cleanup is best-effort and depends on the React unmount running cleanly; a force-quit browser tab still leaks 60s of Tavus minutes (bounded by `participant_absent_timeout`).

Schema changes are additive (nullable columns + CHECK only on new column + default for backfill of `mode`), no data migration required, fully reversible.
