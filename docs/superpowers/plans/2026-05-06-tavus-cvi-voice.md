# Tavus CVI voice integration — implementation plan

**Spec:** `docs/superpowers/specs/2026-05-06-tavus-cvi-voice-design.md` (v4, frozen).
**Branch:** `claude/integrate-tavus-voice-4dtzy`.
**Goal:** Ship the Tavus CVI integration described in the spec, in nine commits (`C-1` through `C7`), each independently reviewable and tested.

## Required user input before merge

Two items from the spec's Open Questions are blockers I cannot resolve without external resources:

1. **Webhook signature header (Open Q 1).** The spec mandates a request-bin probe to determine whether Tavus sends an HMAC signature. Requires: a Tavus account, a publicly reachable endpoint (request bin or local cloudflared tunnel), and one test webhook delivery. I will implement HMAC verification *as if* Tavus sends `X-Tavus-Signature` (HMAC-SHA256 of body), make the header name a config variable, and flag a TODO in the handler. **User must run the probe before C2 ships to production**, and either confirm the header or escalate.

2. **Iframe origin probe (round-3 M4).** The spec branches on whether the Daily.co iframe origin is single-host or wildcard subdomain. Requires: one successful `CreateConversation` call to inspect the returned `conversation_url` host. Until probed, C6 ships with the wildcard-subdomain branch as default (CSP `frame-src 'self' https://*.daily.co`, no document-level Permissions-Policy header) — the more permissive branch.

Two items require live Tavus credentials for the success-criteria browser smoke (cannot be done in this environment):

3. End-to-end browser test of a Tavus interview (CLAUDE.md "After any UI-affecting commit, load the page in the browser and verify").
4. BQ verification of `session_ended` events emitting with `provider=tavus`.

I will implement all nine commits and write tests that pass against `httptest`-mocked Tavus, and explicitly note in the final commit message which acceptance gates remain unverified pending live credentials.

---

## Task C-1: CLAUDE.md handler carve-out broadening

**Files:**
- Modify: `CLAUDE.md`

- [ ] **Step 1**: Replace the line at `CLAUDE.md:31` per the verbatim before/after text in the spec ("API & Proto" section). Keep the surrounding paragraph intact.
- [ ] **Step 2**: Commit standalone with message `docs(claude): broaden internal/handler/ carve-out to all third-party webhooks`.

---

## Task C0: Schema, config, proto, codegen

**Files:**
- Create: `sql/migrations/015_tavus_session_fields.up.sql`
- Create: `sql/migrations/015_tavus_session_fields.down.sql`
- Modify: `sql/queries/sessions.sql` (return new columns from `GetSession`/`ListSessions`/`CreateSession`)
- Modify: `sql/queries/grants.sql` (add `HasActivePaidBalance` query)
- Regenerate: `internal/db/sessions.sql.go`, `internal/db/grants.sql.go`, `internal/db/models.go`
- Modify: `internal/config/config.go` (add `Tavus` struct + Validate hook)
- Modify: `internal/config/config_test.go` (cover Validate)
- Modify: `pb/drill/v1/session.proto` (add `SessionMode` enum, `mode`/`tavus_conversation_url` fields, `SESSION_STATUS_PROVISIONING = 9`)
- Create: `pb/drill/v1/system.proto` (new `SystemService.GetSystem` AIP-131)
- Modify: `internal/rpc/register.go` (mount `SystemService` under authed `opts`)
- Create: `internal/rpc/system/server.go` (handler stub returning `tavus_available = cfg.Tavus.Enabled`)
- Create: `internal/rpc/system/server_test.go`
- Regenerate: `internal/pb/drill/v1/*.pb.go`, `internal/pb/drill/v1/drillv1connect/*.connect.go`, `web/src/pb/**`

- [ ] **Step 1: Migration up**

```sql
-- sql/migrations/015_tavus_session_fields.up.sql
ALTER TABLE interview_sessions
  ADD COLUMN mode                     text NOT NULL DEFAULT 'standard'
    CHECK (mode IN ('standard','tavus')),
  ADD COLUMN tavus_conversation_id    text,
  ADD COLUMN tavus_conversation_url   text,
  ADD COLUMN tavus_recording_url      text,
  ADD COLUMN tavus_reconcile_attempts int  NOT NULL DEFAULT 0;

-- Extend status CHECK to admit 'provisioning' (used by the Tx#1<->Tx#2 window
-- of Tavus session creation; see spec). The full enum already includes
-- 'generating' from migration 009; we add 'provisioning' alongside it.
ALTER TABLE interview_sessions DROP CONSTRAINT IF EXISTS interview_sessions_status_check;
ALTER TABLE interview_sessions ADD CONSTRAINT interview_sessions_status_check
  CHECK (status IN ('active','completed','evaluating','reviewed',
                    'evaluation_failed','failed','cancelled','generating',
                    'provisioning'));

CREATE INDEX idx_sessions_tavus_conversation_id
  ON interview_sessions(tavus_conversation_id)
  WHERE tavus_conversation_id IS NOT NULL;

CREATE INDEX idx_sessions_tavus_reconcile
  ON interview_sessions(started_at)
  WHERE mode='tavus' AND status='active' AND tavus_reconcile_attempts < 10;
```

- [ ] **Step 2: Migration down** — drop the indexes, restore the status CHECK without `'provisioning'`, drop the five columns. Verify by running `make migrate-down` then `make migrate-up` locally.
- [ ] **Step 3**: Add `HasActivePaidBalance :one` query to `sql/queries/grants.sql` per spec text.
- [ ] **Step 4**: Update existing session queries to select the new columns.
- [ ] **Step 5**: `sqlc generate`.
- [ ] **Step 6**: Add `Tavus` struct to `internal/config/config.go`. Implement `Tavus.Validate()` returning error if `Enabled=true` and any of `APIKey/PersonaID/ReplicaID/WebhookSecret` is empty. Wire into the existing `Config.Validate()` (or add it if it doesn't exist).
- [ ] **Step 7**: Edit `pb/drill/v1/session.proto`: add `SessionMode` enum, `mode = 4` on `CreateSessionRequest`, `mode = 19` + `tavus_conversation_url = 20` on `Session`, `mode = 14` on `SessionSummary`, `SESSION_STATUS_PROVISIONING = 9` on `SessionStatus`.
- [ ] **Step 8**: Create `pb/drill/v1/system.proto` per spec (with `string name = 1` on the request).
- [ ] **Step 9**: `buf generate`.
- [ ] **Step 10**: Create `internal/rpc/system/server.go` with `Server.GetSystem` validating `req.Name == "system"` (return `connect.CodeInvalidArgument` otherwise) and returning `&System{TavusAvailable: b.cfg.Tavus.Enabled}`.
- [ ] **Step 11**: Mount `SystemService` in `internal/rpc/register.go` under `opts` (authed).
- [ ] **Step 12**: Tests: `internal/rpc/system/server_test.go` covers `name="system"` happy path and invalid-name rejection.
- [ ] **Step 13**: `make test` passes. Commit `feat(tavus): C0 schema, config, proto`.

---

## Task C1: Tavus client package

**Files:**
- Create: `internal/tavus/client.go`
- Create: `internal/tavus/client_test.go`
- Create: `internal/tavus/webhook.go`
- Create: `internal/tavus/webhook_test.go`
- Create: `internal/tavus/types.go`
- Create: `internal/tavus/tavustest/server.go`
- Create: `internal/tavus/tavustest/server_test.go`

- [ ] **Step 1**: `types.go` — `Conversation`, `Persona`, `Replica`, `ConversationMode`, `Event` (typed sub-payloads: `RecordingReady`, `Utterance`, `Shutdown`).
- [ ] **Step 2**: `client.go` — `Client` struct with `BaseURL`, `APIKey`, `httpClient`. Methods `CreateConversation`, `EndConversation`, `GetConversation`. All set `x-api-key` header, decode JSON, classify errors: 429 → `ErrRateLimited`, 4xx → `ErrBadRequest`, 5xx/network → `ErrUnavailable`.
- [ ] **Step 3**: `client_test.go` — `httptest.Server` returning canned responses; assert request shape + header + error mapping for each method + each error class.
- [ ] **Step 4**: `webhook.go` — `ParseEvent(body []byte) (Event, error)` switches on `event_type` field; `VerifySignature(body, header, secret string) error` performs HMAC-SHA256 and `hmac.Equal` constant-time compare. Header name is taken from a package-level `var SignatureHeader = "X-Tavus-Signature"` (TODO comment notes the probe).
- [ ] **Step 5**: `webhook_test.go` — golden-JSON for each event type; HMAC verify happy/sad/tampered; rejects wrong secret.
- [ ] **Step 6**: `tavustest/server.go` — `NewFakeClient(t *testing.T, opts ...Option) *Client`. Default returns 200 OK with sensible canned bodies; `Option`s let tests override per-method behaviour. Internally registers `t.Cleanup(srv.Close)`.
- [ ] **Step 7**: `make test` passes. Commit `feat(tavus): C1 tavus client + webhook parser`.

---

## Task C2: Webhook handler + dispatcher + advisory-lock conventions

**Files:**
- Create: `internal/handler/tavus.go`
- Create: `internal/handler/tavus_test.go`
- Modify: `internal/handler/server.go` (mount `POST /webhooks/tavus`)
- Create: `internal/backend/tavus.go` (`HandleTavusEvent` dispatcher)
- Create: `internal/backend/tavus_test.go`
- Create: `docs/superpowers/CONVENTIONS.md` (advisory-lock classid registry)
- Modify: `sql/queries/sessions.sql` (add `GetSessionByTavusConversationID`, `UpdateSessionRecordingURL`, conditional `MarkSessionCompletedFromActive` / `MarkSessionCancelledFromActive` queries with status guard, conditional utterance INSERT query)

- [ ] **Step 1**: Add SQL queries:
  - `GetSessionByTavusConversationID :one` — select session by conversation_id.
  - `UpdateSessionRecordingURL :exec` — unconditional update.
  - `MarkSessionCompletedFromActiveWithCandidateCount :one` — CTE, returns `(rows_affected, candidate_count)`. The webhook's `system.shutdown` handler dispatches on the result.
  - `MarkSessionCancelledFromActiveWithCandidateCount :one` — same shape for the cancelled branch.
  - `InsertTavusUtteranceIfActive :exec` — INSERT...SELECT with WHERE EXISTS guard per spec.
- [ ] **Step 2**: `sqlc generate`.
- [ ] **Step 3**: `internal/backend/tavus.go::Backend.HandleTavusEvent(ctx, event)` — dispatches per event type:
  - `RecordingReady`: call `UpdateSessionRecordingURL`.
  - `Utterance`: open tx, `pg_advisory_xact_lock(hashtext('tavus_session'), hashtext(session_id::text))`, run `InsertTavusUtteranceIfActive`, commit.
  - `Shutdown`: open tx, **same** advisory lock, run the appropriate `MarkSession*FromActive` query depending on candidate count, on `completed` branch call `b.jobs.InsertTx(...EvaluateSessionArgs{...}...)` in same tx, commit. On `cancelled` branch call `FullRefundSessionMinutes` in same tx.
  - Unknown: return nil.
  - Emit `session_ended` after commit on the `completed`/`cancelled` branches.
- [ ] **Step 4**: `internal/handler/tavus.go::PostTavusWebhook` — body limit 64KB, missing-secret → 200 (Stripe parity), HMAC verify → 400 on fail, parse → 400 on fail, dispatch → 500 on fail else 200.
- [ ] **Step 5**: Mount in `internal/handler/server.go`'s `registerRoutes`.
- [ ] **Step 6**: Create `docs/superpowers/CONVENTIONS.md` documenting the advisory-lock classid registry. Initial entry: `tavus_session = hashtext('tavus_session')`. Convention: any new advisory-lock callsite must claim a unique classid string and add a row to this table.
- [ ] **Step 7**: Tests:
  - `internal/handler/tavus_test.go` mirrors `stripe_test.go`: 200 (missing secret), 400 (bad HMAC), 400 (bad body), 200 (each event type), 500 (dispatch error).
  - `internal/backend/tavus_test.go` covers all the spec's "Tests" bullets including the H3 utterance↔shutdown race test (with and without lock) and the H5 late-utterance status-guard test.
- [ ] **Step 8**: `make test` passes. Commit `feat(tavus): C2 webhook handler, dispatcher, advisory-lock conventions`.

---

## Task C3: SessionService.CreateSession Tavus branch

**Files:**
- Modify: `internal/rpc/session/server.go`
- Modify: `internal/rpc/session/server_test.go`
- Modify: `internal/backend/session.go` (split CreateSession into two-tx flow for tavus; reuse `FailSession` on error)
- Modify: `internal/backend/tavus.go` (add `BuildConversationalContext(question)` with size cap)
- Modify: `sql/queries/sessions.sql` (add `UpdateSessionToActiveWithTavus :execrows` query)
- Modify: `internal/backend/main_test.go` or `backendtest` helpers as needed

- [ ] **Step 1**: Add SQL query `UpdateSessionToActiveWithTavus :execrows` — `UPDATE interview_sessions SET status='active', tavus_conversation_id=$1, tavus_conversation_url=$2 WHERE id=$3 AND status='provisioning'` returning rows affected.
- [ ] **Step 2**: `sqlc generate`.
- [ ] **Step 3**: `BuildConversationalContext(question db.Question) (string, error)` — returns `question.Prompt` if `len <= 8000`; else returns `("", ErrContextTooLarge)`. Emits `tavus_context_too_large` event before returning.
- [ ] **Step 4**: Refactor `Backend.CreateSession` to accept `mode` and branch:
  - For `mode='standard'`: existing logic, single tx.
  - For `mode='tavus'`: validate `cfg.Tavus.Enabled`, call `HasActivePaidBalance`, run Tx#1 inserting with `status='provisioning'`, call `tavus.Client.CreateConversation` outside tx, run Tx#2 `UpdateSessionToActiveWithTavus`. On any error in Tx#2 or in the Tavus call: `Backend.FailSession` + emit `tavus_provider_call_failed` + return error mapped to ConnectRPC code.
- [ ] **Step 5**: `internal/rpc/session/server.go::CreateSession` — coerce `SESSION_MODE_UNSPECIFIED → STANDARD`, call `Backend.CreateSession` with the mode.
- [ ] **Step 6**: Tests cover all four spec'd cases: enabled-false rejection, no-paid-balance rejection, Tavus 5xx → FailSession + refund verified, success → row has Tavus columns set + status `provisioning → active`.
- [ ] **Step 7**: `make test` passes. Commit `feat(tavus): C3 CreateSession tavus branch`.

---

## Task C4: InterviewService.EndSession + CancelSession

**Files:**
- Modify: `internal/rpc/interview/server.go`
- Modify: `internal/rpc/interview/server_test.go`
- Modify: `internal/backend/session.go` (extend `CompleteSession`/`CancelSession` to call Tavus `/end` best-effort after local terminal-state flip)

- [ ] **Step 1**: In `CompleteSession`/`CancelSession`, after the existing tx commits, look up the row's `tavus_conversation_id`; if non-NULL, call `tavus.Client.EndConversation` with a 5s context timeout. Errors logged via `tavus_provider_call_failed{operation:"end"}`.
- [ ] **Step 2**: Verify the existing conditional UPDATE in those functions matches the spec's `WHERE status='active'` form (round-2 M15). If they use `NOT IN (terminal states)`, switch them to `='active'` for consistency. *Caveat*: this might be an existing-code change; if so, leave a brief comment referencing the spec.
- [ ] **Step 3**: Tests: `TestEndSession_TavusMode_Idempotent` (second call no-ops), `TestEndSession_TavusMode_TavusUnavailable` (local completion succeeds even when Tavus 5xxs).
- [ ] **Step 4**: `make test` passes. Commit `feat(tavus): C4 End/Cancel session tavus branches`.

---

## Task C5: Reconciliation worker + cleanup extension

**Files:**
- Create: `internal/jobs/reconcile_tavus.go`
- Create: `internal/jobs/reconcile_tavus_test.go`
- Modify: `internal/jobs/workers.go` (register `ReconcileTavusSessionsWorker`)
- Modify: `internal/backend/backend.go` (add to `PeriodicJobs` slice with 60s schedule)
- Modify: `internal/jobs/cleanup.go` (call Tavus `/end` for tavus rows after commit)
- Modify: `internal/jobs/cleanup_test.go`
- Modify: `sql/queries/sessions.sql` (add `SelectStrandedTavusProvisioning`, `SelectOverdueTavusActive`, `IncrementTavusReconcileAttempts` queries — all with `FOR UPDATE SKIP LOCKED`)

- [ ] **Step 1**: Add the three SQL queries per spec's reconciler section.
- [ ] **Step 2**: `sqlc generate`.
- [ ] **Step 3**: `ReconcileTavusSessionsWorker.Work(ctx, job)` — runs Pass A (provisioning sweep, FailSession) then Pass B (active sweep with `semaphore.NewWeighted(5)` and 429 short-circuit). Uses `FOR UPDATE SKIP LOCKED` selections; commits per pass.
- [ ] **Step 4**: Register the worker in `internal/jobs/workers.go` and the periodic job in `internal/backend/backend.go:140`.
- [ ] **Step 5**: Extend `CleanupAbandonedSessionsWorker`: capture `tavus_conversation_id` from each cancelled/completed row; after tx commit, call `tavus.Client.EndConversation` per row best-effort.
- [ ] **Step 6**: Tests cover bounded concurrency (assert at most 5 in-flight), 429 short-circuit (asserts no further GetConversation calls after first 429), `SKIP LOCKED` parallel-worker safety (run two workers concurrently, assert no row finalized twice), Pass A FailSession path.
- [ ] **Step 7**: `make test` passes. Commit `feat(tavus): C5 reconciler + cleanup extension`.

---

## Task C6: Frontend — capability hook, session-config toggle, lazy interview surface

**Files:**
- Create: `web/src/api/system.ts` (or extend existing queries.ts) — `useSystem()` hook
- Create: `web/src/tavus/interview-view.tsx`
- Create: `web/src/tavus/__tests__/interview-view.test.tsx`
- Modify: `web/src/pages/session-config.tsx` (toggle + sessionStorage write + paid-balance gate)
- Modify: `web/src/pages/session-config.test.tsx` (or create if missing)
- Modify: `web/src/pages/interview.tsx` (lazy-loaded TavusInterview branch + Suspense)
- Modify: `web/src/pages/__tests__/interview.test.tsx`
- Modify: `internal/handler/spa.go` (add CSP `frame-src` + Permissions-Policy gated on `cfg.Tavus.Enabled`)
- Modify: `internal/handler/spa_test.go`

- [ ] **Step 1**: `useSystem()` hook with `staleTime: Infinity` over `SystemService.GetSystem({ name: "system" })`.
- [ ] **Step 2**: Create `web/src/tavus/interview-view.tsx` per spec snippet, including the `useRef` StrictMode guard. Uses existing query mutation client for `EndSession`/`EndConversation`.
- [ ] **Step 3**: Edit `session-config.tsx`: import `useSystem` and existing billing query; conditionally render the "Enable Tavus interviewer" toggle when both gates pass; toggle writes/reads `sessionStorage['sabermatic.tavus_mode']`. On submit, send `mode: SESSION_MODE_TAVUS` if set.
- [ ] **Step 4**: Edit `interview.tsx`: `const TavusInterview = lazy(...)`; branch on `session.mode`; wrap in `<Suspense>`. Reuse the existing generating-state UI when `session.status === SESSION_STATUS_PROVISIONING`.
- [ ] **Step 5**: Edit `internal/handler/spa.go` to add CSP + Permissions-Policy headers. Default to wildcard-subdomain branch (`frame-src 'self' https://*.daily.co`, no document-level Permissions-Policy) per spec's M4 default. Gate on `cfg.Tavus.Enabled`. Add a TODO comment to revisit after the C1 origin probe.
- [ ] **Step 6**: Frontend tests:
  - `interview-view.test.tsx` — iframe attrs, End button mutation call, unmount fires EndConversation, `<StrictMode>`-wrapped render does NOT fire spurious EndConversation.
  - `interview.test.tsx` — lazy branch with `vi.mock('@/tavus/interview-view', ...)` hoisted; awaits Suspense.
  - `session-config.test.tsx` — toggle visibility gated on `tavus_available && paid_balance > 0`; sessionStorage write; mode field on submit payload.
- [ ] **Step 7**: `make test` passes (includes typecheck + lint + vitest). Commit `feat(tavus): C6 frontend integration`.

---

## Task C7: Bootstrap script + drillctl + docs

**Files:**
- Create: `cmd/drillctl/tavus.go` (subcommands: `tavus-bootstrap`, `tavus-persona-sync`, `tavus-orphan-scan`)
- Modify: `scripts/cloud_bootstrap.py` (prompt for Tavus secrets, run `drillctl tavus-bootstrap`)
- Modify: `.env.example` (six new env keys with comments)
- Modify: `terraform/cloud_run.tf` (env entries for `TAVUS_ENABLED` + `TAVUS_BASE_URL`; Secret Manager refs for the four secrets)
- Create: `docs/tavus-bootstrap.md`
- Modify: `internal/interview/prompt/persona.go` (or wherever we add the exported persona constant)

- [ ] **Step 1**: Export persona prompt as a `const TavusPersonaSystemPrompt = "..."` from the `internal/interview/prompt` package, sourced from the existing interviewer-persona text. If the existing text is multi-piece (system + tools), pick the system-only portion.
- [ ] **Step 2**: `drillctl tavus-bootstrap` — POST `/v2/personas` with the prompt + a default replica id (read from env `TAVUS_REPLICA_ID`); print the returned persona_id (operator pastes it into Secret Manager).
- [ ] **Step 3**: `drillctl tavus-persona-sync` — PATCH `/v2/personas/{id}` with the current `TavusPersonaSystemPrompt`. Idempotent.
- [ ] **Step 4**: `drillctl tavus-orphan-scan` — list Tavus conversations, cross-reference with our `interview_sessions.tavus_conversation_id` set, end any Tavus-side conversations with no matching local session (ops-only emergency tool).
- [ ] **Step 5**: Update `scripts/cloud_bootstrap.py` to prompt for the four secrets and shell out to `drillctl tavus-bootstrap`.
- [ ] **Step 6**: Update `.env.example` per spec's Infrastructure section.
- [ ] **Step 7**: Update terraform per spec's Infrastructure section.
- [ ] **Step 8**: Write `docs/tavus-bootstrap.md` covering: account creation, secret provisioning, `tavus-bootstrap` invocation, `tavus-persona-sync` for prompt updates, local-dev tunnel via cloudflared, webhook URL format. Also a "troubleshooting" section pointing at the boot drift warning and `tavus-orphan-scan`.
- [ ] **Step 9**: Boot the app and confirm the persona-drift warning fires when prompt deviates (or doesn't fire when it matches).
- [ ] **Step 10**: `make test` passes. Commit `feat(tavus): C7 bootstrap, drillctl, docs`.

---

## Verification gates

Before marking the implementation complete:

- [ ] All nine commits land on `claude/integrate-tavus-voice-4dtzy`.
- [ ] `make test` green at HEAD.
- [ ] Spec verification subagent run (per CLAUDE.md "After every subagent task that modifies code, a separate review agent must read actual files and verify against spec"). Fix all findings; re-verify.
- [ ] Push the branch.

Acceptance gates from the spec's "Success criteria" requiring **live Tavus credentials** are explicitly deferred to the user once credentials exist:

- Daily room iframe loads, replica avatar visible.
- Conversation transcribed into `messages` with `input_method='tavus_voice'`.
- "End interview" terminates the conversation in the Tavus dashboard.
- `session_ended` event includes `provider=tavus` attribute in BQ.
- Reconciliation worker picks up an artificially-stranded Tavus session and finalizes it (this one *can* be exercised in tests; the *manual* observation is the deferred part).
- C1 webhook signature probe (Open Q 1) — escalate to user with a request-bin URL.
- C6 iframe origin probe — escalate to user; default ships with the more-permissive wildcard-subdomain CSP branch.

## Implementation order recap

```
C-1 ──► C0 ──► C1 ──► C2 ──► C3 ──► C4 ──► C5 ──► C7
                              │              │
                              └──► C6 ───────┘
```

C0 + C1 can be developed in parallel after C-1; everything else is sequential as drawn. C5 + C6 can be parallel after C4. C7 is last.
