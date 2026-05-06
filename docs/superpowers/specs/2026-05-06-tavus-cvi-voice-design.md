# Tavus CVI voice conversation integration — design spec

**Status:** spec, awaiting review
**Date:** 2026-05-06
**Branch:** `claude/integrate-tavus-voice-4dtzy`
**Author trigger:** product request to add a video-avatar interviewer (Tavus CVI) alongside the existing browser-recorded voice loop.
**Goal:** Let an opted-in user run a Sabermatic interview against a Tavus replica avatar (live WebRTC video + audio) instead of the current push-to-talk OpenAI STT → Anthropic LLM → OpenAI TTS pipeline. Ship behind a feature flag; existing pipeline remains the default.

## Context

Today the interview flow is:

1. `web/src/pages/session-config.tsx` collects question, duration, TTS toggle → `SessionService.CreateSession` (`internal/rpc/session/server.go:128`).
2. `web/src/pages/interview.tsx` mounts. The user holds a button to record (`web/src/audio/recorder.ts`); on release the audio bytes are sent through `InterviewService.SubmitTurn` (`internal/rpc/interview/server.go:28`) as a streaming RPC.
3. Server-side: `internal/backend/turn.go` runs OpenAI Whisper STT → Anthropic streaming LLM → `internal/interview/observer/tts_accumulator.go` chunks tokens into sentences → OpenAI TTS → `TtsChunk` events back over the stream.
4. `web/src/audio/player.ts` decodes/plays the chunks.

Tavus CVI replaces steps 2–4 entirely. A Tavus *conversation* is an end-to-end pipeline: the browser joins a Daily.co WebRTC room; Tavus's servers run STT, run an LLM against a *persona* (system prompt + optional context), drive a *replica* (the avatar) with TTS, and stream audio+video back. From the Sabermatic backend's perspective the live turn loop becomes opaque — we only orchestrate creation/teardown and consume webhook events.

### Tavus API surface we'll use

- `POST https://tavusapi.com/v2/conversations` (header `x-api-key`). Request body fields we set:
  - `persona_id` (required, pre-created at bootstrap; not per-session)
  - `replica_id` (optional if persona has default; we'll set explicitly so it's grep-able)
  - `conversation_name` (we set to `sabermatic-{session_id}`)
  - `conversational_context` (per-session: question prompt + session metadata)
  - `custom_greeting` (per-session: opener tied to the question)
  - `callback_url` (our webhook handler)
  - `properties.max_call_duration` (set from `sessions.config_duration_minutes * 60`)
  - `properties.participant_absent_timeout` (300s default — fine)
  - `properties.enable_recording` (true so we capture transcript+audio)
  Returns `conversation_id`, `conversation_url` (Daily.co URL), `status`.
- `POST /v2/conversations/{id}/end` to terminate.
- Browser joins via the returned `conversation_url`. We embed using `@daily-co/daily-react` directly (peer dep already required by Tavus's CLI scaffolder; importing it ourselves avoids the `@tavus/cvi-ui` codegen tool, which writes files into our tree we don't want to manage). The `<DailyProvider>` + `<DailyVideo>` components from `@daily-co/daily-react` are a stable npm package. This is documented in the open questions below.

### Persona and replica strategy

A persona encapsulates the system prompt + STT/TTS/LLM layer config; a replica is the avatar appearance/voice. Both are created once and reused across all conversations. We provision them out-of-band (not in app code) and store the IDs in environment variables — same pattern as `STRIPE_PRO_PRICE_ID`. Per-session question context is injected via `conversational_context` at conversation-create time, *not* by creating a new persona per session (that would be slow and wasteful).

The persona's system prompt is the static "you are a senior staff engineer running a system-design interview" framing currently in `internal/interview/prompt/`. The dynamic part — the specific question, candidate's prior turns within a multi-turn session, the rubric — goes in `conversational_context`.

**Out of scope for v1:** rotating personas per question category, custom replicas (training a sabermatic-branded avatar), persona A/B testing.

## Why a feature flag, not a replacement

1. The existing pipeline is the only voice path that's been browser-smoked in production. Tavus is unproven for this product.
2. Tavus has cost-per-minute that's an order of magnitude above OpenAI TTS. Until we know which users want it, we keep the cheap path as default.
3. The two pipelines persist different things to `messages` (the existing path writes per-turn rows; Tavus writes via webhook utterance events). Persisting in parallel would be confusing; behind a flag the two never run for the same session.

## Out of scope for v1

- Tavus tool-calling (would let the avatar call Sabermatic backend functions during a turn — interesting future work, not needed for first ship).
- Custom LLM mode (running our own Anthropic prompt and just using Tavus for STT/TTS/replica — defeats the simplicity win of CVI; revisit only if persona-driven LLM quality is unacceptable).
- Replacing the existing pipeline. The flag stays. We may sunset the legacy path in a later spec if Tavus wins.
- Per-user opt-in via UI affordance (v1 is global flag + URL query param to opt in; full UI toggle on session-config waits for v2 once we've measured retention).
- Recording playback in the transcript page (we capture the recording URL from the `recording_ready` webhook but don't render a player yet).
- Memory persistence across sessions (Tavus has `memory_stores`; we don't need cross-session memory — each interview is independent).

## Design

### Architecture

```
session-config.tsx           interview.tsx
       │                           │
       │ CreateSession             │ GetSession
       │ (mode field)              │
       ▼                           ▼
SessionService.CreateSession   SessionService.GetSession
       │                           │
       │ (if mode=tavus)           │ returns Session{
       ▼                           │   tavus_conversation_url,
TavusClient.CreateConversation    │   tavus_conversation_id, ...
       │                           │ }
       │                           │
       ▼                           ▼
sessions table updated         interview.tsx routes:
with conversation_id +         mode=standard → existing UI
conversation_url               mode=tavus    → <TavusInterview/>
                                              embeds Daily room

                          Tavus servers
                               │
                               │ webhook events
                               ▼
                  POST /webhooks/tavus/{secret}
                               │
                               ▼
                  HandleTavusWebhook persists:
                    - utterances → messages table
                    - recording_ready → sessions.tavus_recording_url
                    - shutdown → sessions.status='completed', refund minutes
```

The user ends a Tavus session either by clicking "End" in the existing UI (which calls `EndSession` and we propagate to Tavus's `/end`) or by leaving the Daily room (Tavus emits `shutdown`, our webhook handler reconciles).

### Data model

New columns on `sessions` (migration `015_tavus_session_fields.up.sql`):

| Column | Type | Notes |
|---|---|---|
| `mode` | `text NOT NULL DEFAULT 'standard'` | enum-validated in code: `'standard'`, `'tavus'`. |
| `tavus_conversation_id` | `text` | Set by `CreateSession` when `mode='tavus'`. NULL otherwise. |
| `tavus_conversation_url` | `text` | Daily.co room URL returned by Tavus. NULL otherwise. |
| `tavus_recording_url` | `text` | Set when `recording_ready` webhook fires. |

No separate `tavus_events` raw-log table for v1 — utterances land in `messages` (with `input_method='tavus_voice'`, `audio_url=NULL`) and the rest of the events we just log + ack. If we need event replay later we add the table then.

`messages.input_method` already exists; we add `'tavus_voice'` as an accepted value (it's stored as text, no DB constraint to update).

### Backend

**New package `internal/tavus/`** (mirrors `internal/billing/` structure):
- `client.go`: `Client` struct wrapping `http.Client`; methods `CreateConversation(ctx, CreateConversationRequest) (*Conversation, error)` and `EndConversation(ctx, conversationID string) error`. All requests sign with `x-api-key`. Use the standard library — no third-party Tavus SDK exists. Surface `ErrTavusUnavailable` (5xx, network) and `ErrTavusBadRequest` (4xx) so callers can map to ConnectRPC codes.
- `client_test.go`: hits an `httptest.Server` to assert the request shape and parse responses.
- `webhook.go`: `Event` struct (typed sub-fields per `event_type`); `ParseEvent(body []byte) (Event, error)`.
- `webhook_test.go`: golden-file parsing of each event_type we handle.
- `types.go`: `ConversationMode` enum (`'standard'`, `'tavus'`), `Conversation`, `Persona`, `Replica` value types.

**Config** (`internal/config/config.go`):

```go
type Tavus struct {
    Enabled              bool          `env:"TAVUS_ENABLED,default=false"`
    APIKey               string        `env:"TAVUS_API_KEY"`
    PersonaID            string        `env:"TAVUS_PERSONA_ID"`
    ReplicaID            string        `env:"TAVUS_REPLICA_ID"`
    WebhookSecret        string        `env:"TAVUS_WEBHOOK_SECRET"`
    BaseURL              string        `env:"TAVUS_BASE_URL,default=https://tavusapi.com"`
    RequestTimeout       time.Duration `env:"TAVUS_REQUEST_TIMEOUT,default=15s"`
}
```

`Enabled=false` means: pretend the integration doesn't exist. RPC validates that mode='tavus' is rejected; webhook handler returns 404. When `Enabled=true`, the other four secrets must be non-empty — `Config.Validate()` enforces this and returns an error from `cmd/drill/main.go` (per CLAUDE.md: never panic at init).

**SessionService.CreateSession** (`internal/rpc/session/server.go`):
- Add `mode` field to `CreateSessionRequest` proto. Accepted values: `SESSION_MODE_STANDARD` (default), `SESSION_MODE_TAVUS`.
- If `mode=tavus` and `!cfg.Tavus.Enabled`: return `connect.CodeFailedPrecondition` "tavus mode is not enabled".
- If `mode=tavus`: insert the session row first (so we have an ID), then call `tavus.Client.CreateConversation` with `conversational_context` derived from the question, `custom_greeting` from the question opener, `properties.max_call_duration = duration_minutes*60`, `callback_url = cfg.Auth.BaseURL + "/webhooks/tavus/" + cfg.Tavus.WebhookSecret`. On success, `UPDATE sessions SET tavus_conversation_id=$1, tavus_conversation_url=$2 WHERE id=$3`. On Tavus failure: rollback the session insert (or mark it `cancelled` and refund) — the spec resolves this in Open Questions.

**SessionService.GetSession**: include the new fields in the `Session` proto so the frontend can route on them.

**InterviewService.EndSession** / **CancelSession**: if the session has `tavus_conversation_id`, call `tavus.Client.EndConversation` *before* the existing logic. Errors logged but not fatal — the webhook `shutdown` will reconcile if Tavus times out the call.

**Webhook handler** (`internal/handler/tavus.go`, mounted on `POST /webhooks/tavus/{secret}`):
- Mirror the Stripe webhook structure (`internal/handler/stripe.go`). Required because: it's an HTTP-level concern (third-party POST, raw body, status-code-as-protocol); CLAUDE.md explicitly carves an exception for "Stripe webhooks — endpoints that are inherently HTTP-level."
- Authenticates by URL path segment matching `cfg.Tavus.WebhookSecret`. Tavus does not (per available docs) sign requests with a header; the unguessable secret in the URL is our authentication. **Open Question 1 below: confirm.**
- Body: parse via `tavus.ParseEvent`. Dispatch on `event_type`:
  - `application.transcription_ready` (full-conversation transcript): no-op for v1 (we have per-utterance events for messages).
  - `application.recording_ready`: `UPDATE sessions SET tavus_recording_url=$1 WHERE tavus_conversation_id=$2`.
  - `conversation.utterance` (per-speech-segment, for both `replica` and `user` roles): insert into `messages` with `role` mapped (`replica`→`interviewer`, `user`→`candidate`), `content`=transcript, `input_method='tavus_voice'`.
  - `system.shutdown` (conversation ended): if session not already in terminal state, mark `completed`, enqueue evaluation job (same job pipeline the standard flow uses today).
  - Unknown event_type: log at info and 200.
- Always 200 unless body parse fails (400) or DB write fails (500). The 200 ack semantics are critical to prevent Tavus retry storms — this is the same lesson encoded in `internal/handler/stripe.go`.

**Backend.HandleTavusEvent**: the dispatcher above lives in `internal/backend/tavus.go`, called from the handler — same shape as `Backend.HandleStripeWebhook`.

### Proto

Add to `pb/drill/v1/session.proto`:

```proto
enum SessionMode {
  SESSION_MODE_UNSPECIFIED = 0;
  SESSION_MODE_STANDARD    = 1;
  SESSION_MODE_TAVUS       = 2;
}

message CreateSessionRequest {
  // ... existing fields
  SessionMode mode = 4;
}

message Session {
  // ... existing fields
  SessionMode mode = N;
  string tavus_conversation_url = N+1;  // empty unless mode=TAVUS and creation succeeded
  // tavus_conversation_id and tavus_recording_url stay backend-only; the frontend doesn't need them.
}
```

After editing, `buf generate` (per CLAUDE.md) and commit `internal/pb/`, `web/src/pb/`.

### Frontend

**New module `web/src/tavus/`**:
- `provider.tsx`: thin wrapper around `@daily-co/daily-react`'s `<DailyProvider>`. We initialize the call object once on mount.
- `interview-view.tsx`: a `<TavusInterview session={...}>` component. Joins the Daily room at `session.tavus_conversation_url`. Renders the replica's video tile and the user's local video tile. Listens for `participant-left` (user closed the tab) and triggers a `EndSession` mutation. Renders an "End interview" button that does the same.
- `__tests__/interview-view.test.tsx`: mounts with mocked Daily call object, asserts the URL is passed in and the leave button calls the mutation.

**Feature flag exposure**:
- Backend exposes `tavus_available` on the existing `UserService.GetCurrentUser` response (or the next nearest authenticated query — pick based on what the session-config page already calls; whichever it is, add the bool to it). Boolean, derived from `cfg.Tavus.Enabled`.
- `web/src/pages/session-config.tsx`: when `tavus_available && URL has ?tavus=1` (URL query is the v1 user opt-in mechanism), pass `mode: SESSION_MODE_TAVUS` to `CreateSession`. Keep the UI for this minimal — small banner at top of session-config saying "Tavus mode" so the user knows they're in it.
- `web/src/pages/interview.tsx`: branch on `session.mode`:
  ```tsx
  if (session.mode === SessionMode.TAVUS) return <TavusInterview session={session} />;
  // existing UI
  ```

**Why query param, not toggle button**: ships faster, easier to revoke (just remove the URL param), defers UX work until we know if it's worth doing. The toggle goes in v2.

### Infrastructure

- `.env.example`: append the five new `TAVUS_*` keys with comments.
- `scripts/cloud_bootstrap.py`: add prompts for `TAVUS_API_KEY`, `TAVUS_PERSONA_ID`, `TAVUS_REPLICA_ID`, `TAVUS_WEBHOOK_SECRET` so production gets them in Secret Manager. `TAVUS_ENABLED` is plain env (boot config, not a secret).
- `terraform/`: add Cloud Run env entries for `TAVUS_ENABLED` and `TAVUS_BASE_URL`; reference Secret Manager secrets for the four sensitive keys. Pattern matches existing `STRIPE_*` wiring.
- Persona+replica creation: not automated. `docs/tavus-bootstrap.md` (new) documents the one-time `curl` to create them and where to paste the IDs. Out of scope for app code.

### Tests

- `internal/tavus/client_test.go`: round-trip create+end against `httptest.Server`; assert request body shape, header, response parsing, error mapping.
- `internal/tavus/webhook_test.go`: parse golden JSON for each event_type; reject malformed.
- `internal/rpc/session/server_test.go`: extend `TestCreateSession` with cases for `mode=tavus, Enabled=true`, `mode=tavus, Enabled=false` (rejected), `mode=tavus, Tavus API returns 5xx` (rolled back).
- `internal/handler/tavus_test.go`: HTTP-level test mirroring `stripe_test.go` — 404 on wrong secret, 400 on bad body, 200 on each event type, asserts DB side-effects.
- `internal/rpc/interview/server_test.go`: extend `TestEndSession` to cover Tavus-mode session (asserts Tavus `/end` is called).
- Frontend: `web/src/tavus/__tests__/interview-view.test.tsx`; `web/src/pages/__tests__/interview.test.tsx` covers the mode branch.

Mock Tavus the same way Anthropic is mocked in `internal/rpc/interview/server_test.go:34` — `httptest.Server` + injection point.

### Telemetry

Emit two new events through the existing `events.Emitter` pattern (which goes to BigQuery):
- `tavus_session_started` (server-side, on successful `CreateConversation`): props `{ session_id, persona_id, replica_id, duration_minutes }`.
- `tavus_session_ended` (server-side, on `shutdown` webhook or `EndSession`): props `{ session_id, ended_via: 'user'|'webhook'|'cancel', duration_seconds }`.

These let us measure adoption, drop-off, and cost without inspecting Tavus's dashboard.

## Open questions to resolve during implementation

1. **Webhook authentication.** The currently-fetchable Tavus docs don't enumerate a signature header. The URL-path-secret approach is what we have to ship without it. Before merging: spend 30 minutes spelunking the actual Tavus webhook docs (the `/sections/event-schemas/` URL paths I tried 404'd on this date — they may be at a different path) to confirm whether there's an HMAC header to verify. If yes, switch to header-verification (cheaper to rotate, doesn't appear in URL logs).

2. **Failed `CreateConversation` — rollback or refund?** Two options:
   - **Rollback (recommended):** wrap the session insert + Tavus call + update in a single repeatable-read transaction. Tavus failure rolls back, user sees `CreateSession` error, no minutes consumed. Tradeoff: holds a DB tx across an external HTTP call (15s timeout); acceptable because `Speech.OpenAIAPIKey` calls in `turn.go` already do similar.
   - **Refund:** insert session, call Tavus, on failure mark session `cancelled` and refund minutes via the existing billing path. Tradeoff: extra row in `sessions`, two paths for cancellation. Pick rollback for v1 simplicity.

3. **Daily.co auth.** The `conversation_url` is a public-knowledge URL — anyone with it can join the room. We don't currently set `require_auth=true` on the conversation create (which would gate joining behind a per-session JWT we'd return to the client). For v1: rely on the unguessability of the URL. The URL is stored on `sessions` (RLS-protected by user_id ownership in `SessionService.GetSession`), so cross-user access requires direct DB exfiltration — not in our threat model. Add a comment to `sessions.tavus_conversation_url`'s migration noting this. For v2 if we get worried: enable `require_auth` and have the backend mint join tokens on demand.

4. **Frontend Daily lib choice.** Spec says `@daily-co/daily-react`. Confirm during plan that this lib's tree-shake size + React 19 compat are acceptable — if it's too big, fall back to a thin iframe embed of `conversation_url` (loses the "render the user's tile separately" UX but cuts ~200KB). Embedding via iframe also sidesteps the Daily React lib's call-object lifecycle complexity.

5. **`conversational_context` size limit.** Tavus docs don't publish a max length. Long question prompts (with rubric + few-shot examples) could exceed it. Plan should measure typical context length against a documented limit — if none documented, send a probe request and observe.

6. **Per-utterance `messages` ordering.** Webhooks may arrive out-of-order under load. Use the event's `created_at` timestamp (Tavus includes one) as a tiebreaker; existing `messages.seq` is `serial`-driven and reflects insert order. This may be acceptable; if not, switch to `(timestamp, insert_order)` ordering on the read path.

## Implementation order

Eight commits, each independently mergeable and reviewable:

1. **C0 — Schema + config + proto.** Migration 015, `Tavus` config struct, proto enum + fields, codegen. No behavior changes. Tests: config validation only.
2. **C1 — Tavus client package.** `internal/tavus/{client,webhook,types}.go` + tests. Pure library, no callers yet.
3. **C2 — Webhook handler.** `internal/handler/tavus.go`, route mounted, dispatcher in `internal/backend/tavus.go`. Tests cover the four event types. Still no UI access.
4. **C3 — SessionService.CreateSession Tavus branch.** Wires the client into session creation behind `Enabled` + `mode=tavus`. RPC test updated.
5. **C4 — InterviewService.EndSession + CancelSession Tavus branch.** Calls `/end`. Test updated.
6. **C5 — Frontend `web/src/tavus/` + provider + view.** Component-level tests with mocked Daily.
7. **C6 — Session-config + interview routing.** Query-param flag, `tavus_available` on user response, branch in interview.tsx.
8. **C7 — Telemetry events + docs.** `tavus-bootstrap.md`, `.env.example`, scripts/cloud_bootstrap.py, terraform.

C0 and C1 can run in parallel by separate sub-agents; C2 depends on C1; C3 depends on C2; C4 on C3; C5 and C6 in parallel after C3; C7 last.

## Success criteria

Before marking done:
- [ ] `make test` passes (frontend typecheck + lint + vitest, backend tests with -race, golangci-lint).
- [ ] With `TAVUS_ENABLED=false` (default), the existing flow is bit-for-bit unchanged (existing tests prove this).
- [ ] With `TAVUS_ENABLED=true` and a valid Tavus API key + persona + replica, browse `/sessions/new?tavus=1`, complete one short interview, verify:
   - Daily room loads, replica avatar visible
   - Conversation transcribed into `messages` table
   - "End" button terminates the conversation in Tavus dashboard
   - `tavus_session_started` and `tavus_session_ended` events appear in BQ
- [ ] With `TAVUS_ENABLED=true` and `mode=standard`, the existing flow still works.
- [ ] No regression in evaluation pipeline — Tavus-mode sessions enqueue evaluation on shutdown the same way standard-mode sessions do on end.
- [ ] Webhook handler returns 200 for unknown event_types (forward-compatibility).
- [ ] Documented in `docs/tavus-bootstrap.md` how to provision persona+replica IDs.

## Risk

Medium. The integration touches session lifecycle, webhook trust boundaries, and a third-party dependency that's central to the user experience when enabled. Mitigations:

- Behind a default-off flag — zero blast radius for existing users.
- Webhook handler ack semantics modeled on the production-tested Stripe handler.
- Schema changes are additive (new nullable columns + new enum value); no data migration.
- Per-commit reviewability: each commit lands working tests against the prior state.

The biggest unknown is webhook authentication (Open Q 1) — if there's no signature mechanism, the URL-path-secret is vulnerable to log leaks (proxy logs, browser history if the URL ever appears client-side). The webhook URL never appears client-side and Cloud Run access logs are not externally accessible, so the v1 risk is low; we should still confirm.
