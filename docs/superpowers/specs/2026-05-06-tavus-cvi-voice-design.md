# Tavus CVI voice conversation integration — design spec

**Status:** spec, round 2 (incorporating round-1 Opus review)
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
  - `conversational_context` (per-session: question prompt + rubric)
  - `custom_greeting` (per-session: opener tied to the question)
  - `callback_url` — `cfg.Auth.BaseURL + "/webhooks/tavus"` (no secret in path; see Webhook section)
  - `properties.max_call_duration` = `config_duration_minutes * 60`
  - `properties.participant_absent_timeout` = `min(config_duration_minutes * 60, 1800)`
  - `properties.enable_recording` = `true`
  Returns `conversation_id`, `conversation_url` (Daily.co URL), `status`.
- `POST /v2/conversations/{id}/end` to terminate.
- `GET /v2/conversations/{id}?verbose=true` for reconciliation (returns authoritative `status`, `shutdown_reason`, transcript).
- Browser joins via the returned `conversation_url`. **We embed it as a plain `<iframe>`**, not via `@daily-co/daily-react`. Reasoning: (a) `@daily-co/daily-react` and `@daily-co/daily-js` peer deps target React 18 in their published version as of this date; the project is on React 19.2.4 (`web/package.json`). (b) Bundle size of `@daily-co/daily-js` exceeds 300KB minified+gzipped versus zero JS for an iframe. (c) The Tavus-hosted iframe UI ships Tavus's default tile layout and controls — fine for v1 since we're not customizing tile arrangement. The cost is losing per-tile control (e.g. picture-in-picture); listed in Out of scope.

### Persona and replica strategy

A persona encapsulates the system prompt + STT/TTS/LLM layer config; a replica is the avatar appearance/voice. Both are created once and reused across all conversations. Per-session context is injected via `conversational_context` at conversation-create time, not by creating a new persona per session.

The persona's system prompt is derived from `internal/interview/prompt/`. To prevent drift between the committed Go source and the Tavus-stored persona, we:

1. Export the persona's system prompt as a `const` from `internal/interview/prompt/persona.go`.
2. `scripts/cloud_bootstrap.py` reads that constant via `go run ./cmd/drillctl tavus-persona-prompt` (a new drillctl subcommand) and creates/updates the Tavus persona via `POST /v2/personas`, writing `TAVUS_PERSONA_ID` and `TAVUS_REPLICA_ID` to Secret Manager.
3. App boot performs a `GET /v2/personas/{id}` and logs a warning (not fatal) if `system_prompt` differs from the constant.

Out of scope for v1: rotating personas per question category, custom replicas (training a Sabermatic-branded avatar), persona A/B testing.

## Why a feature flag, not a replacement

1. The existing pipeline is the only voice path that's been browser-smoked in production; Tavus is unproven for this product.
2. Tavus minute pricing is materially higher than OpenAI TTS+Whisper. Until adoption is measured, the cheap path stays default.
3. The two pipelines persist different things to `messages` — the existing path writes per-turn rows from `turn.go`; Tavus writes via webhook utterance events. Persisting in parallel for one session would be confusing; behind a flag the two never run for the same session.

## Out of scope for v1

- Tavus tool-calling.
- Custom LLM mode (running our own Anthropic prompt and using Tavus only for STT/TTS/replica).
- Replacing the existing pipeline. The flag stays.
- Custom video tile UI (PIP, side-by-side rendering). Iframe shows Tavus default UI.
- Recording playback in the transcript page; we capture `tavus_recording_url` from the `recording_ready` webhook but don't render a player yet.
- Memory persistence across Tavus sessions (`memory_stores`).
- Pricing differentiation — v1 reserves SaberMinutes 1:1 with Tavus minutes (deliberate loss-leader). Revisit after measuring adoption. **Tavus mode is gated to paying users in v1**: free-grant users see the feature disabled. Enforced server-side by checking `user.has_active_paid_grant` in `CreateSession`.
- Copying recordings to GCS (we store the Tavus-hosted URL as-is; v2 will copy to GCS to handle URL expiry).
- UI toggle on session-config (v1 uses a one-click "Enable Tavus" affordance that writes `sessionStorage`; v2 promotes to a persistent UI).

## Design

### Architecture

```
session-config.tsx                    interview.tsx
       │                                    │
       │ CreateSession(mode=TAVUS)         │ GetSession
       ▼                                    │
SessionService.CreateSession                │
  1. validate user paid (free→reject)       │
  2. reserve minutes + insert row (tx#1)    │
  3. call Tavus.CreateConversation           ▼
  4. on success: tx#2 update Tavus cols   if Session.mode==TAVUS:
     on failure: FailSession (refund)       <iframe src=Session.tavus_conversation_url/>
                                          else:
                                            existing UI

                          Tavus servers
                               │
                               │ webhook events (HMAC-verified)
                               ▼
                  POST /webhooks/tavus
                               │
                               ▼
                  HandleTavusEvent (per-session advisory lock):
                    - utterance         → INSERT messages
                    - recording_ready   → UPDATE sessions.tavus_recording_url
                    - shutdown          → conditional UPDATE status, enqueue eval (in tx)

                  Reconciliation worker (River, every 60s):
                    - SELECT sessions WHERE mode='tavus' AND status='active'
                                            AND started_at < now() - max_call_duration
                    - GET Tavus /v2/conversations/{id}?verbose=true
                    - finalize locally if Tavus reports ended
```

User exit paths and how each is handled:

| Exit path | Backend trigger | Tavus call | Status |
|---|---|---|---|
| Click "End interview" | `InterviewService.EndSession` | `/end` best-effort, after local complete | `completed`, eval enqueued in same tx |
| Close tab | iframe `unload`; nothing reaches us | none | Tavus emits `shutdown` → webhook reconciles |
| Cancel | `InterviewService.CancelSession` | `/end` best-effort, after local cancel | `cancelled`, no eval, refund |
| Network blackhole on user side | nothing | none | Tavus eventually emits `shutdown` |
| Webhook never arrives | nothing | nothing | Reconciliation worker (60s tick) finalizes |
| Tavus `/end` 5xx during EndSession | local already-completed | logged | Webhook (when it arrives) hits already-terminal status; no double-eval |

### Data model

Migration `015_tavus_session_fields.up.sql` (and matching `.down.sql`):

| Column on `interview_sessions` | Type | Notes |
|---|---|---|
| `mode` | `text NOT NULL DEFAULT 'standard' CHECK (mode IN ('standard','tavus'))` | enum |
| `tavus_conversation_id` | `text` | NULL unless mode='tavus' |
| `tavus_conversation_url` | `text` | Daily.co room URL; bearer-equivalent (see Open Q 3) |
| `tavus_recording_url` | `text` | Set when `recording_ready` webhook fires |

Plus index: `CREATE INDEX idx_sessions_tavus_conversation_id ON interview_sessions(tavus_conversation_id) WHERE tavus_conversation_id IS NOT NULL;` (used by webhook lookup).

Down migration: drop the index, then the four columns.

`messages.input_method` is unconstrained `text` (`sql/migrations/001_initial.up.sql:82`); we add `'tavus_voice'` as a value with no schema change.

`messages.role` *is* CHECK-constrained to `('interviewer','candidate')` (line 80). We deliberately map Tavus `replica`→`interviewer` and Tavus `user`→`candidate` so that the existing constraint, evaluation prompt formatting, and transcript page all keep working unchanged. Do **not** extend the constraint to add `'replica'`/`'user'` values; the migration is silent about role specifically because no schema change is needed.

No `tavus_events` raw-log table for v1.

### Backend

**New package `internal/tavus/`** (mirrors `internal/billing/`):

- `client.go`: `Client` struct wrapping `http.Client` + injectable `BaseURL`. Methods:
  - `CreateConversation(ctx, CreateConversationRequest) (*Conversation, error)`
  - `EndConversation(ctx, conversationID string) error`
  - `GetConversation(ctx, conversationID string) (*Conversation, error)` — used by reconciler
  All requests sign with `x-api-key`. Surface `ErrUnavailable` (5xx, network) and `ErrBadRequest` (4xx) for caller-side mapping.
- `webhook.go`: `Event` struct with typed sub-payloads per `event_type`; `ParseEvent(body []byte) (Event, error)`; `VerifySignature(body []byte, header string, secret string) error` — HMAC-SHA256 constant-time compare.
- `types.go`: `ConversationMode`, `Conversation`, `Persona`, `Replica`.
- `tavustest/`: helper package with `NewFakeServer(t *testing.T) (*httptest.Server, *Client)` returning a server with default 200 OK + matching Client. Per CLAUDE.md "Shared test helper packages follow the `httptest` convention."

**Webhook signature verification.** The Tavus docs available on this date do not publicly enumerate the signature header. We commit to:
1. **Implementation prerequisite:** before merging, send one test webhook to a request-bin and inspect the headers. If a header named anything like `X-Tavus-Signature`/`Tavus-Signature`/`Webhook-Signature` is present, implement HMAC-SHA256 verification using `cfg.Tavus.WebhookSecret` (configured by registering the secret with Tavus when creating the persona/conversation, if Tavus supports per-account webhook secrets, or as a static value in our env). If absent, **escalate to user** for decision before merging — do not ship the URL-path-secret pattern by default.
2. **The mounted route is `POST /webhooks/tavus` (no path secret).** This avoids logging/span/audit-trail leakage of the secret.
3. The handler reads the body, verifies HMAC, then dispatches. On verify failure: `401`. On parse failure: `400`. On unknown event: `200` (forward-compat). On dispatch failure: `500` so Tavus retries.

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

`Config.Validate()`: when `Enabled=true`, the four secrets must be non-empty; return error from `cmd/drill/main.go` (per CLAUDE.md: never panic at init).

**`SessionService.CreateSession` (`internal/rpc/session/server.go`)** — split-commit approach replacing Open Q 2's earlier rollback proposal:

1. Validate request. If `mode=SESSION_MODE_TAVUS` and `!cfg.Tavus.Enabled`, return `connect.CodeFailedPrecondition`.
2. **Coerce** `SESSION_MODE_UNSPECIFIED → SESSION_MODE_STANDARD` on input.
3. If `mode=tavus`, also validate the user has an active paid grant (subscription or pack purchase). Otherwise return `connect.CodePermissionDenied`.
4. Tx#1: reserve minutes + insert session row with `mode='tavus'`, `tavus_conversation_id=NULL`. Commit.
5. Call `tavus.Client.CreateConversation(ctx, ...)` outside any transaction.
6. On success — Tx#2: `UPDATE interview_sessions SET tavus_conversation_id=$1, tavus_conversation_url=$2 WHERE id=$3 AND status='active'`. Commit. Return `Session` proto.
7. On failure — call existing `Backend.FailSession(ctx, sessionID)` (`internal/backend/session.go:386`), which marks status `failed` and refunds. Emit `tavus_provider_call_failed` event with `{operation:'create', http_status, error}`. Return `connect.CodeUnavailable`.

Rationale: never holds a DB transaction across an external HTTP call (the round-1 review's correct objection to the prior "rollback" plan was that DATABASE_MAX_POOL_SIZE=16 plus a 15s Tavus timeout under concurrent signups would exhaust the pool).

**`SessionService.GetSession`** returns `Session.mode` and `Session.tavus_conversation_url`. Server always returns `SESSION_MODE_STANDARD` (not UNSPECIFIED) for legacy rows where `mode='standard'`.

**`InterviewService.EndSession` and `CancelSession`** (`internal/rpc/interview/server.go`):

End flow (Tavus mode):
1. Conditional `UPDATE interview_sessions SET status='completed' WHERE id=$1 AND status NOT IN (terminal states) RETURNING tavus_conversation_id`.
2. If RowsAffected == 1: enqueue `EvaluateSessionArgs` via `b.jobs.InsertTx(ctx, tx, ...)` in the *same* transaction.
3. Commit, then call `tavus.Client.EndConversation(ctx, conversationID)` best-effort with a 5s timeout. Errors logged (`tavus_provider_call_failed` event with `operation:'end'`); user is not blocked.

Cancel flow mirrors the above but with the existing cancel logic (refund, archive, no eval) and Tavus `/end` best-effort after.

**Webhook handler** (`internal/handler/tavus.go`, mounted on `POST /webhooks/tavus`):

This is the third HTTP-level route in `internal/handler/`. CLAUDE.md currently enumerates "health checks, OAuth flows, and Stripe webhooks." **The Tavus integration must update CLAUDE.md in the same commit** to read "health checks, OAuth flows, and third-party webhooks (Stripe, Tavus)." The boy-scout rule: spell out the carve-out as a category, not a list of providers we'll keep editing.

Handler flow:
1. Read body; on read error: `400`.
2. Lookup `cfg.Tavus.WebhookSecret`; if `Enabled=false` or secret unset: `404`.
3. Verify HMAC (header name resolved at impl time per webhook prereq above); on failure: `401`.
4. `tavus.ParseEvent(body)`; on failure: `400`.
5. Dispatch to `Backend.HandleTavusEvent(ctx, event)`. On error: `500` (Tavus retries). On success: `200`.

`Backend.HandleTavusEvent` (`internal/backend/tavus.go`) per event:

- `application.recording_ready`: `UPDATE interview_sessions SET tavus_recording_url=$1 WHERE tavus_conversation_id=$2`. No advisory lock (single-statement upsert is safe).
- `conversation.utterance` (both `replica` and `user` roles):
  1. Look up session by `tavus_conversation_id`.
  2. Acquire transaction-scoped advisory lock: `SELECT pg_advisory_xact_lock(hashtext('session:' || session_id::text))`. This serializes per-session writes and protects the `UNIQUE(session_id, seq)` constraint on `messages` (`sql/migrations/001:85`) against concurrent webhook deliveries — round-1 review M9.
  3. Compute `seq = MAX(seq)+1` from `messages` (in the same tx).
  4. `INSERT INTO messages (session_id, seq, role, content, input_method)` with role mapped (`replica`→`interviewer`, `user`→`candidate`), `input_method='tavus_voice'`.
  5. Commit.
- `system.shutdown`:
  1. Begin tx.
  2. Conditional `UPDATE interview_sessions SET status=$new_status, ended_at=NOW() WHERE id=$id AND status='active' RETURNING (SELECT COUNT(*) FROM messages WHERE session_id=$id AND role='candidate') AS candidate_count`.
  3. If RowsAffected == 0: tx rollback; the user (or a prior webhook) already terminated this session; nothing to do — no double-eval.
  4. If RowsAffected == 1 and `candidate_count == 0`: status set to `cancelled` (matching `CancelAbandonedEmptySessions` semantics from `internal/jobs/cleanup.go:42`), call `FullRefundSessionMinutes` in the same tx, no eval enqueue. Round-1 review M8.
  5. If RowsAffected == 1 and `candidate_count > 0`: status set to `completed`, enqueue `EvaluateSessionArgs` via `b.jobs.InsertTx` in the same tx.
  6. Commit. Emit `session_ended` (existing event) with attrs `{provider:'tavus', reason:'tavus_shutdown'}`.
- Unknown `event_type`: log at info, return `nil`.

**Reconciliation worker** (`internal/jobs/reconcile_tavus.go`):

`ReconcileTavusSessionsArgs` — periodic (every 60s, via existing River `PeriodicJobBundle`). Selects `interview_sessions` rows where `mode='tavus' AND status='active' AND started_at < now() - (config_duration_minutes * interval '1 minute' + interval '5 minutes')`. For each, calls `tavus.Client.GetConversation`. If Tavus reports `status != 'active'`, simulates the equivalent of a `system.shutdown` event through the same `Backend.HandleTavusEvent` path. Bounded to 50 sessions per tick to limit blast radius.

Also extend `internal/jobs/cleanup.go:CleanupAbandonedSessionsWorker`: for each session it cancels or completes that has a non-NULL `tavus_conversation_id`, call `tavus.Client.EndConversation` after the tx commits. Best-effort — logged failure does not abort the cleanup.

### Proto

Edit `pb/drill/v1/session.proto`:

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
  SessionMode mode        = 4;  // Defaults to STANDARD when unset.
}

message Session {
  // ... existing fields 1..18 unchanged
  SessionMode mode                  = 19;
  string      tavus_conversation_url = 20;  // empty unless mode=TAVUS and creation succeeded
}
```

`SessionSummary` (used by `ListSessions`) gets only `mode = 14` (next free; existing fields 1..13). No `tavus_conversation_url` on the summary because the list page doesn't need to render the iframe.

After editing, `buf generate` and commit `internal/pb/`, `web/src/pb/`. Per CLAUDE.md: never hand-edit generated files.

**New `SystemService` ConnectRPC** for capability flags, replacing the round-1 plan to bolt `tavus_available` onto `User`:

```proto
// pb/drill/v1/system.proto (new file)
service SystemService {
  rpc GetCapabilities(GetCapabilitiesRequest) returns (GetCapabilitiesResponse);
}

message GetCapabilitiesRequest {}

message GetCapabilitiesResponse {
  bool tavus_available = 1;
  // Future capability flags land here.
}
```

Frontend caches the response with React Query (`staleTime: Infinity`) on app boot.

### Frontend

**New module `web/src/tavus/`**:

- `interview-view.tsx`: a `<TavusInterview session={...}>` component. Renders a full-bleed `<iframe src={session.tavus_conversation_url} allow="camera; microphone; autoplay" />`. Plus an "End interview" button that calls the `EndSession` mutation, then navigates to the transcript page. **Lazy-loaded** at the call site via `React.lazy(() => import('@/tavus/interview-view'))` so the (small) module isn't fetched for standard-mode users.
- `__tests__/interview-view.test.tsx`: renders, asserts iframe src, asserts End button calls the mutation and navigates.

**Capability flag**:

- `web/src/api/queries.ts` adds a `useCapabilities()` hook over the new `SystemService.GetCapabilities`.
- `web/src/pages/session-config.tsx`:
  - When `caps.tavus_available && user has paid grant`, render an "Enable Tavus interviewer" toggle that writes `sessionStorage['sabermatic.tavus_mode']='1'`.
  - On submit: if the flag is set, send `mode: SESSION_MODE_TAVUS` to `CreateSession`.
  - Round-1 review L20: `sessionStorage` (not URL query param) so the opt-in doesn't propagate through bookmark/share. Cleared on logout.
- `web/src/pages/interview.tsx`:
  ```tsx
  const TavusInterview = lazy(() => import('@/tavus/interview-view'));
  // ...
  if (session.mode === SessionMode.TAVUS) {
    return <Suspense fallback={<LoadingSpinner/>}><TavusInterview session={session} /></Suspense>;
  }
  // existing UI
  ```

### Telemetry

Reuse existing event names per round-1 review M18 (current convention is domain-noun, not provider-prefix):

- `session_created` already emitted (`internal/backend/session.go:111`); add `slog.String("mode", "tavus")` attr when applicable.
- `session_ended` already emitted (multiple sites); add `slog.String("provider", "tavus")` and existing `reason` attr already covers source.
- New event `tavus_provider_call_failed` (operations event, distinct from the user funnel) with attrs `{operation, http_status, error_class}`. Used for ops dashboards; not a funnel event.

### Infrastructure

- `.env.example`: append `TAVUS_ENABLED`, `TAVUS_API_KEY`, `TAVUS_PERSONA_ID`, `TAVUS_REPLICA_ID`, `TAVUS_WEBHOOK_SECRET`, `TAVUS_BASE_URL` with comments.
- `scripts/cloud_bootstrap.py`: prompt for the four secrets; run `drillctl tavus-bootstrap` to create persona+replica via the Tavus API and persist returned IDs to Secret Manager.
- `terraform/`: Cloud Run env entries for `TAVUS_ENABLED` and `TAVUS_BASE_URL`; Secret Manager references for the four secrets. Pattern matches `STRIPE_*` wiring.
- `docs/tavus-bootstrap.md` (new): step-by-step including how to create the Tavus account, run the bootstrap, configure the webhook secret on the Tavus side (if the Tavus dashboard supports it), and the local-dev tunnel:
  - Local dev requires a public tunnel (ngrok/cloudflared) for Tavus → `localhost:8080/webhooks/tavus`. Cross-references `docs/stripe-local-dev.md` if it exists; otherwise this doc adds the missing how-to for both providers.

### Tests

- `internal/tavus/client_test.go`: exercise create/end/get against `httptest.Server`; assert request shape + auth header + error mapping.
- `internal/tavus/webhook_test.go`: golden-JSON parse for each event type; HMAC verify happy/sad paths.
- `internal/tavus/tavustest/`: helper package per CLAUDE.md "Shared test helper packages follow the `httptest` convention."
- `internal/rpc/session/server_test.go`: extend `TestCreateSession` with cases:
  - `mode=tavus, Enabled=false` → `FailedPrecondition`
  - `mode=tavus, user has no paid grant` → `PermissionDenied`
  - `mode=tavus, Tavus 5xx` → session is `failed` + minutes refunded (FailSession path)
  - `mode=tavus, success` → row has Tavus columns set
- `internal/handler/tavus_test.go`: mirrors `stripe_test.go` — `404` (disabled), `401` (bad HMAC), `400` (bad body), `200` (each event), DB side-effects asserted.
- `internal/backend/tavus_test.go`: covers the dispatcher unit-test-style with a fake `tavus.Client`. Specifically tests:
  - Concurrent `conversation.utterance` events for the same session don't violate `UNIQUE(session_id, seq)` (run 100 parallel inserts, assert all succeed and all `seq` values are distinct).
  - `system.shutdown` for an already-completed session is a no-op.
  - `system.shutdown` with zero candidate messages → `cancelled` + refund.
- `internal/jobs/reconcile_tavus_test.go`: with a fake `tavus.Client`, asserts active+overdue Tavus sessions are queried and finalized.
- `internal/jobs/cleanup_test.go`: extend to cover the Tavus `/end` best-effort call.
- `internal/rpc/interview/server_test.go`: `TestEndSession_TavusMode` and `TestCancelSession_TavusMode`; assert Tavus `/end` is called and that local completion happens regardless of Tavus's response.
- Frontend:
  - `web/src/tavus/__tests__/interview-view.test.tsx`
  - `web/src/pages/__tests__/interview.test.tsx` covers the lazy-loaded mode branch (use `vi.mock` for the lazy import).
  - `web/src/pages/__tests__/session-config.test.tsx` covers the `tavus_available` toggle + sessionStorage write.

## Open questions to resolve during implementation

1. **Webhook signature header.** Per the Webhook section above: a request-bin probe is the first task in C1 and gates whether the integration ships at all. If no HMAC mechanism exists, escalate to user.

2. **Persona system-prompt drift detection — fatal or warning?** Spec says "warning, not fatal." If the prompt has drifted enough to materially change interview behavior, a warning may be too quiet. Resolved during impl by counting how often the prompt actually changes (rare).

3. **`tavus_conversation_url` is bearer-equivalent.** Anyone with the Daily.co URL can join the room. The URL is exposed via `GetSession` to the authenticated owner only (RLS-enforced by `user_id` ownership). Acceptable v1 risk: the URL surface is browser memory + React Query cache + React DevTools — not network logs and not the URL bar. For v2, set `require_auth=true` on the conversation create and have the backend mint per-user join tokens.

4. **Tavus pricing → SaberMinute mapping.** Out of scope per Out of scope section, but flag for a follow-up spec once we have one week of usage data.

5. **`conversational_context` size limit.** Tavus docs don't publish a max. Probe during C1; if our typical question prompt + rubric exceeds the limit, we truncate intelligently (drop few-shot examples first).

## Implementation order

Eight commits, each independently mergeable and reviewable:

1. **C0 — Schema + config + proto.** Migration 015 (up + down), `Tavus` config struct + Validate, `SessionMode` enum + `mode`/`tavus_conversation_url` fields on `Session` + `SessionSummary`, new `SystemService` proto, codegen. CLAUDE.md update for the `internal/handler/` carve-out.
2. **C1 — Tavus client package.** `internal/tavus/{client,webhook,types}.go` + tests + `tavustest/`. Includes the request-bin probe to determine signature header (Open Q 1). Pure library, no callers yet.
3. **C2 — Webhook handler + dispatcher.** `internal/handler/tavus.go`, route mounted, `internal/backend/tavus.go` dispatcher with advisory-lock + conditional-update logic. C2 has a runtime dependency on C0's schema for tests to write — call this out in C2's PR description.
4. **C3 — `SessionService.CreateSession` Tavus branch.** Split-commit logic, paid-grant gate, `tavus_provider_call_failed` event.
5. **C4 — `InterviewService.EndSession` + `CancelSession` Tavus branches.** Conditional UPDATE + best-effort Tavus `/end`.
6. **C5 — Reconciliation worker + cleanup extension.** `ReconcileTavusSessions` River job; `CleanupAbandonedSessionsWorker` extended for Tavus `/end`.
7. **C6 — Frontend.** `web/src/tavus/` module, lazy-loaded; capability hook; session-config toggle (sessionStorage); interview-page branch.
8. **C7 — Bootstrap + docs.** `drillctl tavus-bootstrap` subcommand, `scripts/cloud_bootstrap.py` integration, `docs/tavus-bootstrap.md`, `.env.example`, terraform.

Dependencies: C0 has no deps. C1 has no deps on C0 (pure library). C2 depends on C1 (uses `tavus.ParseEvent`) and at runtime on C0 (tests will fail without the schema). C3 depends on C1 + C0. C4 depends on C3 (shares the conditional-UPDATE pattern). C5 depends on C1 + C2. C6 depends on C0 (proto). C7 depends on all.

C0 and C1 can run in parallel; C5 and C6 can run in parallel after C4. Per CLAUDE.md: tightly-coupled files run sequentially with later agents receiving committed output of earlier ones.

## Success criteria

- [ ] `make test` passes (frontend typecheck + lint + vitest, backend tests with -race, golangci-lint).
- [ ] With `TAVUS_ENABLED=false` (default), the existing flow is bit-for-bit unchanged — proven by existing tests still passing.
- [ ] With `TAVUS_ENABLED=true` and valid Tavus credentials, browse `/sessions/new` as a paid user, click "Enable Tavus interviewer," complete one short interview:
  - Daily room iframe loads, replica avatar visible
  - Conversation transcribed into `messages` with `input_method='tavus_voice'`
  - "End interview" terminates the conversation in the Tavus dashboard
  - `session_ended` event includes `provider=tavus` attribute in BQ
- [ ] With `TAVUS_ENABLED=true` and `mode=standard`, the existing flow still works.
- [ ] Free-grant user attempting `mode=tavus` gets `PermissionDenied`.
- [ ] Reconciliation worker picks up an artificially-stranded Tavus session and finalizes it.
- [ ] Webhook handler returns 200 for unknown event types and 401 for tampered HMAC.
- [ ] Concurrent utterance webhook deliveries for the same session never produce a `UNIQUE` violation (verified by `internal/backend/tavus_test.go`).
- [ ] CLAUDE.md updated with the third-party-webhooks carve-out.
- [ ] `docs/tavus-bootstrap.md` is complete enough that a fresh dev can run the bootstrap end-to-end.

## Risk

Medium-low. The integration is large in surface but each commit lands incrementally with the existing flow as default. The two material risks:

1. **Webhook signature unknown until probe.** If Tavus offers no HMAC, we escalate to user before merging — no silent fallback to URL-path-secret.
2. **Reconciliation worker correctness.** If the GetConversation API doesn't reliably reflect `ended` status, we leak active sessions. Mitigated by `CleanupAbandonedSessionsWorker` as a backstop and by the conditional-UPDATE everywhere preventing double-finalization.

Schema changes are additive (nullable columns + CHECK only on new column + default for backfill of `mode`), no data migration required, fully reversible.
