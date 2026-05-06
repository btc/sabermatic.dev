# Tavus CVI voice integration — implementation plan

**Spec:** `docs/superpowers/specs/2026-05-06-tavus-cvi-voice-design.md` (v4, frozen — with two factual corrections from plan-review round 1: webhook URL prefix `/api/webhooks/`, removal of broken `useEffect` cleanup in favor of relying on Tavus's 60s `participant_absent_timeout`).
**Branch:** `claude/integrate-tavus-voice-4dtzy`.
**Goal:** Ship the Tavus CVI integration described in the spec, in nine commits (`C-1` through `C7`), each independently reviewable and tested.

## Required user input before merge

Two probes I cannot run in this environment without a Tavus account:

1. **Webhook signature header (Open Q 1).** I'll implement HMAC-SHA256 verification using a configurable header name (`var SignatureHeader = "X-Tavus-Signature"`) and flag a TODO for user to confirm via request-bin probe before production traffic.
2. **Iframe origin probe (round-3 M4).** Default ships with the more-permissive wildcard-subdomain CSP branch (`frame-src 'self' https://*.daily.co`); user upgrades to host-pinned origin after probe.

Two acceptance gates require live credentials and are deferred to the user:

3. End-to-end browser test of a Tavus interview (CLAUDE.md UI verification).
4. BQ verification of `session_ended` events emitting with `provider=tavus`.

---

## Task C-1: CLAUDE.md handler carve-out broadening

**Files:**
- Modify: `CLAUDE.md`

- [ ] Replace `CLAUDE.md:31` per the verbatim before/after text in the spec ("API & Proto" / Proto section).
- [ ] Commit standalone: `docs(claude): broaden internal/handler/ carve-out to all third-party webhooks`.

---

## Task C0: Schema, config, proto, codegen

**Files:**
- Create: `sql/migrations/015_tavus_session_fields.up.sql`, `015_tavus_session_fields.down.sql`
- Modify: `sql/queries/sessions.sql` (return new columns from existing queries)
- Modify: `sql/queries/grants.sql` (add `HasActivePaidBalance`)
- Regenerate: `internal/db/sessions.sql.go`, `internal/db/grants.sql.go`, `internal/db/models.go`
- Modify: `internal/config/config.go` (add `Tavus` struct + Validate hook)
- Modify: `internal/config/config_test.go`
- Modify: `pb/drill/v1/session.proto` (add `SessionMode` enum + fields, `SESSION_STATUS_PROVISIONING = 9`)
- Create: `pb/drill/v1/system.proto` (new `SystemService.GetSystem` AIP-131)
- Modify: `internal/rpc/register.go` (mount `SystemService` under authed `opts`)
- Create: `internal/rpc/system/server.go` + `_test.go`
- Regenerate: `internal/pb/drill/v1/*.pb.go`, `internal/pb/drill/v1/drillv1connect/*.connect.go`, `web/src/pb/**`

- [ ] **Step 1: Migration up.**

```sql
-- sql/migrations/015_tavus_session_fields.up.sql
ALTER TABLE interview_sessions
  ADD COLUMN mode                     text NOT NULL DEFAULT 'standard'
    CHECK (mode IN ('standard','tavus')),
  ADD COLUMN tavus_conversation_id    text,
  ADD COLUMN tavus_conversation_url   text,
  ADD COLUMN tavus_recording_url      text,
  ADD COLUMN tavus_reconcile_attempts int  NOT NULL DEFAULT 0;

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

CREATE INDEX idx_sessions_tavus_provisioning
  ON interview_sessions(created_at)
  WHERE mode='tavus' AND status='provisioning';
```

- [ ] **Step 2: Migration down** — drops indexes (`idx_sessions_tavus_provisioning`, `idx_sessions_tavus_reconcile`, `idx_sessions_tavus_conversation_id`), drops the new `interview_sessions_status_check` and re-adds it without `'provisioning'`, drops the five columns (the per-column `mode` CHECK drops with the column).

```sql
-- sql/migrations/015_tavus_session_fields.down.sql
DROP INDEX IF EXISTS idx_sessions_tavus_provisioning;
DROP INDEX IF EXISTS idx_sessions_tavus_reconcile;
DROP INDEX IF EXISTS idx_sessions_tavus_conversation_id;

ALTER TABLE interview_sessions DROP CONSTRAINT IF EXISTS interview_sessions_status_check;
ALTER TABLE interview_sessions ADD CONSTRAINT interview_sessions_status_check
  CHECK (status IN ('active','completed','evaluating','reviewed',
                    'evaluation_failed','failed','cancelled','generating'));

ALTER TABLE interview_sessions
  DROP COLUMN tavus_reconcile_attempts,
  DROP COLUMN tavus_recording_url,
  DROP COLUMN tavus_conversation_url,
  DROP COLUMN tavus_conversation_id,
  DROP COLUMN mode;
```

Verify by running `make migrate-down` then `make migrate-up` locally.

- [ ] **Step 3:** Add `HasActivePaidBalance :one` to `sql/queries/grants.sql` per spec.
- [ ] **Step 4:** Update `GetSessionByID`, `ListSessionsByUser`, `CreateSession` etc. in `sql/queries/sessions.sql` to select the new columns.
- [ ] **Step 5:** `sqlc generate`. Verify `internal/db/sessions.sql.go` and `grants.sql.go` regenerate cleanly.
- [ ] **Step 6:** Add `Tavus` struct to `internal/config/config.go` per spec; implement `Tavus.Validate()` and call it from the existing `Config.Validate()`. Test in `config_test.go`: `Enabled=true` with empty `APIKey` returns error.
- [ ] **Step 7:** Edit `pb/drill/v1/session.proto`: add `SessionMode` enum, `mode = 4` on `CreateSessionRequest`, `mode = 19` + `tavus_conversation_url = 20` on `Session`, `mode = 14` on `SessionSummary`, `SESSION_STATUS_PROVISIONING = 9` on `SessionStatus`.
- [ ] **Step 8:** Create `pb/drill/v1/system.proto` per spec (with `string name = 1` on the request).
- [ ] **Step 9:** `buf generate`. Verify `internal/pb/` and `web/src/pb/` updates compile.
- [ ] **Step 10:** Create `internal/rpc/system/server.go::Server.GetSystem` validating `req.Name == "system"` (return `connect.CodeInvalidArgument` otherwise) and returning `&System{TavusAvailable: b.Config().Tavus.Enabled}`.
- [ ] **Step 11:** Mount `SystemService` in `internal/rpc/register.go` under `opts` (authed bucket — capabilities are per-deployment but require the user be logged in to even ask).
- [ ] **Step 12:** `internal/rpc/system/server_test.go`: happy path + invalid-name rejection.
- [ ] **Step 13:** `make test` passes. Commit `feat(tavus): C0 schema, config, proto`.

---

## Task C1: Tavus client package

**Files:**
- Create: `internal/tavus/client.go`, `client_test.go`, `webhook.go`, `webhook_test.go`, `types.go`
- Create: `internal/tavus/tavustest/server.go`, `server_test.go`

- [ ] **Step 1:** `types.go` — `Conversation`, `Persona`, `Replica`, `ConversationMode`, `Event` interface with typed sub-payloads (`RecordingReadyEvent`, `UtteranceEvent` (with `Role replica|user`, `Speech string`), `ShutdownEvent`).
- [ ] **Step 2:** `client.go` — `Client` struct with `BaseURL`, `APIKey`, `httpClient`. Methods `CreateConversation`, `EndConversation`, `GetConversation`. All set `x-api-key` header, decode JSON, classify errors:
  - 429 → `ErrRateLimited` (sentinel via `errors.Is`)
  - 4xx → `ErrBadRequest` wrapped with status + body
  - 5xx / network / timeout → `ErrUnavailable`
- [ ] **Step 3:** `client_test.go` — `httptest.Server` returning canned responses; assert request body (JSON shape), header (`x-api-key`), error class for each method × each error category.
- [ ] **Step 4:** `webhook.go`:
  - `var SignatureHeader = "X-Tavus-Signature"` (TODO comment: confirm via request-bin probe before production).
  - `ParseEvent(body []byte) (Event, error)` switches on the `event_type` field; unknown returns `(UnknownEvent{Type: ...}, nil)` — handler treats as no-op.
  - `VerifySignature(body []byte, header, secret string) error` computes HMAC-SHA256, compares with `hmac.Equal`. Returns `ErrSignatureMismatch` on fail.
- [ ] **Step 5:** `webhook_test.go` — golden JSON for each event type; HMAC verify happy/sad/tampered; rejects wrong secret.
- [ ] **Step 6:** `tavustest/server.go` — `NewFakeClient(t *testing.T, opts ...Option) *Client`. Internally spins `httptest.Server`, registers `t.Cleanup(srv.Close)`. `Option`s let tests override per-method behaviour (return error, return canned body).
- [ ] **Step 7:** `make test` passes. Commit `feat(tavus): C1 tavus client + webhook parser`.

---

## Task C2: Webhook handler + dispatcher + advisory-lock conventions

**Files:**
- Create: `internal/handler/tavus.go`, `tavus_test.go`
- Modify: `internal/handler/server.go` (mount `POST /api/webhooks/tavus`)
- Create: `internal/backend/tavus.go`, `tavus_test.go`
- Create: `docs/superpowers/CONVENTIONS.md`
- Modify: `sql/queries/sessions.sql`
- Create: `internal/backendtest/tavus_session.go` (helper that seeds a Tavus session in `'active'` state for tests — addresses plan-review H6)

- [ ] **Step 1: SQL queries.** All annotations chosen for the dispatcher's RowsAffected dispatch logic (plan-review H3, H4):

```sql
-- name: GetSessionByTavusConversationID :one
SELECT * FROM interview_sessions
WHERE tavus_conversation_id = $1;

-- name: UpdateSessionRecordingURL :exec
UPDATE interview_sessions
   SET tavus_recording_url = $1, updated_at = NOW()
 WHERE tavus_conversation_id = $2;

-- name: InsertTavusUtteranceIfActive :execrows
-- Returns rows-affected (0 = session no longer active; drop the utterance).
INSERT INTO messages (session_id, seq, role, content, input_method)
SELECT $1::uuid,
       COALESCE((SELECT MAX(seq) FROM messages WHERE session_id = $1), 0) + 1,
       $2::text,
       $3::text,
       'tavus_voice'
 WHERE EXISTS (SELECT 1 FROM interview_sessions WHERE id = $1 AND status = 'active');

-- name: MarkSessionCompletedFromActive :one
-- CTE pattern: always returns exactly one row with (updated, candidate_count)
-- so sqlc's :one annotation never produces ErrNoRows.
WITH upd AS (
  UPDATE interview_sessions
     SET status = 'completed', ended_at = NOW(), updated_at = NOW()
   WHERE id = $1 AND status = 'active'
  RETURNING id
)
SELECT
  EXISTS(SELECT 1 FROM upd)::bool AS updated,
  (SELECT COUNT(*)::int FROM messages WHERE session_id = $1 AND role = 'candidate') AS candidate_count;

-- name: MarkSessionCancelledFromActive :one
WITH upd AS (
  UPDATE interview_sessions
     SET status = 'cancelled', ended_at = NOW(), updated_at = NOW()
   WHERE id = $1 AND status = 'active'
  RETURNING id
)
SELECT EXISTS(SELECT 1 FROM upd)::bool AS updated;

-- name: UpdateSessionToActiveWithTavus :execrows
-- (Moved from C3 to C2 because C2's tests require a Tavus session in
-- 'active' state via the backendtest helper, which in turn invokes this
-- query. Keeping the query and the helper co-located.)
UPDATE interview_sessions
   SET status = 'active',
       tavus_conversation_id = $1,
       tavus_conversation_url = $2,
       started_at = COALESCE(started_at, NOW()),
       updated_at = NOW()
 WHERE id = $3 AND status = 'provisioning';
```

The advisory lock is **not** an sqlc query (plan-review M5 resolution). It's invoked via `tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1::int, $2::int)", classid, objid)` directly because it's a control-plane op outside the data layer. This is documented in `CONVENTIONS.md`.

- [ ] **Step 2:** `sqlc generate`. Confirm the generated `MarkSessionCompletedFromActive` returns a row struct with `Updated bool` and `CandidateCount int32`.
- [ ] **Step 3: `internal/backend/tavus.go::Backend.HandleTavusEvent`** dispatch logic. Helper for advisory-lock acquisition:

```go
func acquireTavusSessionLock(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID) error {
  // Two-arg form namespaces the lock by feature classid; see CONVENTIONS.md.
  classid := hashtext("tavus_session")
  objid := hashtext(sessionID.String())
  _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1::int, $2::int)", classid, objid)
  return err
}
// hashtext mirrors Postgres's hashtext() to compute classid in Go for stability.
```

Per-event behavior:
- `RecordingReadyEvent`: call `q.UpdateSessionRecordingURL`. No lock; single-statement; recording is an artifact (spec line ~232).
- `UtteranceEvent`: open tx; `acquireTavusSessionLock`; map role; `q.InsertTavusUtteranceIfActive` (RowsAffected==0 ⇒ session terminated, drop silently per spec H5); commit.
- `ShutdownEvent`: open tx; `acquireTavusSessionLock`; call `MarkSessionCompletedFromActive`; if `Updated==false` rollback (already terminal); if `CandidateCount==0` instead call `MarkSessionCancelledFromActive` + `FullRefundSessionMinutes` in same tx; if `CandidateCount>0` enqueue `EvaluateSessionArgs` via `b.jobs.InsertTx` in same tx; commit. Emit `session_ended` post-commit with attrs:
  ```go
  b.events.Emit(ctx, "session_ended",
    slog.String("provider", "tavus"),
    slog.String("reason", "tavus_shutdown"),
    slog.String("terminal_status", terminalStatus),
  )
  ```
- `UnknownEvent`: log info, return nil.

- [ ] **Step 4: `internal/handler/tavus.go::PostTavusWebhook`** — mirrors `PostStripeWebhook`:
  - body limit 64KB
  - missing `cfg.Tavus.WebhookSecret` → 200 (Stripe parity, prevents retry storms)
  - read `tavus.SignatureHeader` from request; if empty or `tavus.VerifySignature` fails → 400
  - `tavus.ParseEvent(body)` failure → 400
  - `Backend.HandleTavusEvent` failure → 500 (Tavus retries)
  - else → 200 (including unknown event types)
- [ ] **Step 5:** Mount `mux.HandleFunc("POST /api/webhooks/tavus", PostTavusWebhook(b))` in `internal/handler/server.go::registerRoutes`. **Unauthenticated** (signature-verified, like Stripe; plan-review L5).
- [ ] **Step 6: `docs/superpowers/CONVENTIONS.md`** documents the advisory-lock classid registry and the `messages.input_method` value registry:

  ```markdown
  # Codebase Conventions

  ## Advisory-lock classid registry

  All `pg_advisory_xact_lock`/`pg_advisory_lock` callsites use the two-arg
  form `(classid, objid)` to namespace by feature. New callsites must claim
  a unique classid string and add a row here.

  | classid string  | objid                  | Where           | Purpose |
  |-----------------|------------------------|-----------------|---------|
  | `tavus_session` | `hashtext(session_id)` | internal/backend/tavus.go | Serialize utterance INSERT and shutdown UPDATE per Tavus session |

  ## `messages.input_method` value registry

  Stored as nullable `TEXT` (no DB CHECK constraint by design — values
  evolve with input modalities). Known values:

  | Value          | Producer                                     |
  |----------------|----------------------------------------------|
  | `text`         | InterviewService.SubmitTurn (TextInput)      |
  | `voice`        | InterviewService.SubmitTurn (VoiceInput)     |
  | `tavus_voice`  | Backend.HandleTavusEvent (Utterance)         |
  ```

- [ ] **Step 7: `internal/backendtest/tavus_session.go`** helper (resolves plan-review H6):

  ```go
  // SeedTavusSession creates a Tavus-mode session in 'active' state with the
  // given conversation_id, suitable for testing the webhook dispatcher. Uses
  // the same SeedUser path and CreateSession flow as production, then runs
  // UpdateSessionToActiveWithTavus to move past 'provisioning'. Never raw SQL.
  func SeedTavusSession(t *testing.T, b *backend.Backend, ...) (sessionID uuid.UUID, conversationID string) {
      // body uses b.SignUp + b.CreateSession with mode='tavus' (the C3
      // logic is forward-referenced; for C2's standalone tests we instead
      // call a smaller seedTavusSessionDirect helper that inserts via the
      // sqlc queries already added in this commit). See SeedUser pattern in
      // internal/backendtest/.
  }
  ```

  Concretely for C2 (before C3 lands): the helper uses `q.CreateSession` directly with `mode='tavus', status='provisioning'` then `q.UpdateSessionToActiveWithTavus` to transition. Mirrors what C3's CreateSession Tavus branch will do.

- [ ] **Step 8: Tests.** Enumerated explicitly (plan-review L1):
  - `internal/handler/tavus_test.go`:
    1. 200 on missing webhook secret config (Stripe parity)
    2. 400 on missing/invalid HMAC header
    3. 400 on body parse failure
    4. 200 on each event type (RecordingReady, Utterance, Shutdown, Unknown) with DB side-effects asserted
    5. 500 on dispatch error (mock `Backend.HandleTavusEvent` to fail)
  - `internal/backend/tavus_test.go`:
    1. Concurrent utterance test: two sessions, 50 parallel inserts each, no UNIQUE violations, dense `seq` per session.
    2. Negative test (no advisory lock): same scenario fails (red-green proof the lock works).
    3. Utterance↔shutdown race test: N parallel goroutines mixing utterances and shutdowns; assert no inserts after terminal flip, no UNIQUE violations, exactly one evaluation enqueued.
    4. Same race test with the lock removed: assert it fails (proves the lock guards H3 specifically, not just H5).
    5. Shutdown for already-completed session is a no-op (no double-eval, no error).
    6. Shutdown with zero candidate utterances → status `cancelled`, full refund applied.
    7. Late utterance after shutdown is dropped (status guard test, RowsAffected==0).
    8. RecordingReady on already-completed session still updates `tavus_recording_url` (artifact policy).
- [ ] **Step 9:** `make test` passes. Commit `feat(tavus): C2 webhook handler, dispatcher, advisory-lock conventions`.

---

## Task C3: SessionService.CreateSession Tavus branch

**Files:**
- Modify: `internal/rpc/session/server.go`, `server_test.go`
- Modify: `internal/backend/session.go` (split CreateSession for tavus)
- Modify: `internal/backend/tavus.go` (`BuildConversationalContext`)
- Modify: `internal/backend/lifecycle_test.go` (existing CreateSession callers — plan-review L2)
- Modify: `internal/backendtest/tavus_session.go` (replace direct-query helper with one that calls `Backend.CreateSession` end-to-end)

- [ ] **Step 1:** `BuildConversationalContext(question db.Question) (string, error)` — returns `question.Prompt` if `len <= 8000`; else returns `("", ErrContextTooLarge)`. Caller (CreateSession) emits `tavus_context_too_large` event with `{question_id, context_length}` before mapping to `connect.CodeFailedPrecondition`.
- [ ] **Step 2:** Refactor `Backend.CreateSession` to accept `mode` and branch:
  - `mode='standard'`: existing logic, single tx.
  - `mode='tavus'`: validate `cfg.Tavus.Enabled` (else error); call `q.HasActivePaidBalance` (else `ErrNoPaidBalance`); Tx#1 reserve minutes + INSERT with `status='provisioning'`; commit. Call `tavus.Client.CreateConversation` outside tx with body composed via `BuildConversationalContext`. On success: Tx#2 calls `q.UpdateSessionToActiveWithTavus`; if RowsAffected != 1 (someone else mutated the row, e.g. cleanup raced) call `Backend.FailSession`. On Tavus failure: `Backend.FailSession`; emit `tavus_provider_call_failed{operation:"create", http_status, error_class}`; return error.
  - Existing CreateSession callers in `lifecycle_test.go` and `session_archive_test.go` updated to pass the default `SESSION_MODE_STANDARD`.
- [ ] **Step 3:** `internal/rpc/session/server.go::CreateSession` — coerce `SESSION_MODE_UNSPECIFIED → SESSION_MODE_STANDARD`, map any error from `Backend.CreateSession` to ConnectRPC codes per spec (`FailedPrecondition` for Enabled=false, `PermissionDenied` for no paid balance, `Unavailable` for Tavus 5xx). Both backend methods emit `session_created` with `slog.String("mode", mode)` attr.
- [ ] **Step 4:** Test cases:
  - `mode=tavus, Enabled=false` → `FailedPrecondition`
  - `mode=tavus, no paid balance` → `PermissionDenied`
  - `mode=tavus, Tavus 5xx` → session is `failed`, minutes refunded (verify via ledger entry); event emitted
  - `mode=tavus, success` → row has Tavus columns set, status `provisioning → active`
  - `mode=standard` (existing tests) → unchanged
  - `BuildConversationalContext` with a 9000-char prompt returns `ErrContextTooLarge` and emits `tavus_context_too_large`
- [ ] **Step 5:** `make test` passes. Commit `feat(tavus): C3 CreateSession tavus branch`.

---

## Task C4: InterviewService.EndSession + CancelSession

**Files:**
- Modify: `internal/rpc/interview/server.go`, `server_test.go`
- Modify: `internal/backend/session.go` (extend Complete/Cancel to call Tavus `/end` after commit)

- [ ] **Step 1:** Audit `Backend.CompleteSession` and `Backend.CancelSession` — confirm they use `WHERE status='active'` (per spec round-2 M15). If they use a NOT-IN-terminal form, update to `='active'` and add a regression-comment.
- [ ] **Step 2:** After the existing tx commits, look up `tavus_conversation_id` (already on the row from the SELECT used by these methods); if non-NULL, spawn a `tavus.Client.EndConversation` call with a 5s context timeout. Errors logged via `tavus_provider_call_failed{operation:"end"}` event; user is not blocked.
- [ ] **Step 3:** Tests:
  - `TestEndSession_TavusMode_Success` — Tavus `/end` is called, local row terminal.
  - `TestEndSession_TavusMode_Idempotent` — second EndSession after Tavus has already shutdown is a no-op.
  - `TestEndSession_TavusMode_TavusUnavailable` — local completion succeeds even when Tavus 5xxs; event emitted.
  - `TestCancelSession_TavusMode` — Tavus `/end` called, refund applied, no eval enqueued.
- [ ] **Step 4:** `make test` passes. Commit `feat(tavus): C4 End/Cancel session tavus branches`.

---

## Task C5: Reconciliation worker + cleanup extension

**Files:**
- Create: `internal/jobs/reconcile_tavus.go`, `reconcile_tavus_test.go`
- Modify: `internal/jobs/workers.go` (add `Reconcile *ReconcileTavusSessionsWorker` to `WorkerRefs` — plan-review M7)
- Modify: `internal/backend/backend.go` (post-create wiring of `Jobs` field; add to the `PeriodicJobs: []*river.PeriodicJob{...}` slice — plan-review M6 stable anchor)
- Modify: `internal/jobs/cleanup.go`, `cleanup_test.go`
- Modify: `sql/queries/sessions.sql`

- [ ] **Step 1:** Add SQL queries:

```sql
-- name: SelectStrandedTavusProvisioning :many
SELECT id, user_id, config_duration_minutes
  FROM interview_sessions
 WHERE mode = 'tavus' AND status = 'provisioning'
   AND created_at < NOW() - interval '5 minutes'
 ORDER BY created_at ASC
 LIMIT $1
 FOR UPDATE SKIP LOCKED;

-- name: SelectOverdueTavusActive :many
SELECT id, tavus_conversation_id
  FROM interview_sessions
 WHERE mode = 'tavus' AND status = 'active' AND tavus_conversation_id IS NOT NULL
   AND started_at < NOW() - (config_duration_minutes * interval '1 minute' + interval '5 minutes')
   AND tavus_reconcile_attempts < 10
 ORDER BY started_at ASC
 LIMIT $1
 FOR UPDATE SKIP LOCKED;

-- name: IncrementTavusReconcileAttempts :exec
UPDATE interview_sessions
   SET tavus_reconcile_attempts = tavus_reconcile_attempts + 1, updated_at = NOW()
 WHERE id = $1;
```

- [ ] **Step 2:** `sqlc generate`.
- [ ] **Step 3:** `ReconcileTavusSessionsWorker.Work(ctx, job)`:
  - **Pass A:** open tx; `SelectStrandedTavusProvisioning(LIMIT 25)`; for each, call `Backend.FailSession` (refunds via existing path) outside the SELECT tx (or call within if FailSession is tx-aware; pick based on FailSession's signature). Commit.
  - **Pass B:** open tx; `SelectOverdueTavusActive(LIMIT 25)`; release the SELECT lock by committing immediately, then process the rows with `semaphore.NewWeighted(5)` bounding concurrent `tavus.Client.GetConversation` calls. For each:
    - On `ErrRateLimited`: `IncrementTavusReconcileAttempts`, return early (skip remaining rows this tick).
    - On `ErrUnavailable`: `IncrementTavusReconcileAttempts`, continue.
    - On non-`'active'` Tavus status: synthesize a `ShutdownEvent` and dispatch via `Backend.HandleTavusEvent` (which takes the same advisory lock and runs the conditional UPDATE).
  - Log per-tick metrics: `{provisioning_failed, rows_examined, rows_finalized, rate_limited}`.
- [ ] **Step 4:** Register the worker in `internal/jobs/workers.go::WorkerRefs` and create it alongside the others; in `internal/backend/backend.go` wire `Reconcile.Jobs = jobs` after `river.NewClient` returns (plan-review M7); add the periodic job entry to the `PeriodicJobs: []*river.PeriodicJob{...}` slice with a 60s interval.
- [ ] **Step 5:** Extend `CleanupAbandonedSessionsWorker`: capture `tavus_conversation_id` from each cancelled/completed row inside the tx; after `tx.Commit`, iterate the slice and call `tavus.Client.EndConversation` per row with a 5s timeout. Best-effort (logged failure does not abort cleanup).
- [ ] **Step 6:** Tests:
  - Pass A: stranded `'provisioning'` row > 5 min → FailSession path called, refund applied.
  - Pass B: bounded concurrency (assert ≤5 in-flight via instrumented fake client).
  - Pass B: 429 short-circuit (assert no further GetConversation after first 429; attempts incremented for the row that hit 429).
  - `SKIP LOCKED` parallel-worker safety (run two workers concurrently, assert no row finalized twice).
  - Cleanup extension: Tavus `/end` is called for cancelled/completed rows with `tavus_conversation_id` set; non-Tavus rows untouched.
- [ ] **Step 7:** `make test` passes. Commit `feat(tavus): C5 reconciler + cleanup extension`.

---

## Task C6: Frontend — capability hook, session-config toggle, lazy interview surface, CSP

**Files:**
- Create: `web/src/api/system.ts` — `useSystem()` hook
- Modify: `web/src/api/queries.ts` (or wherever existing hooks live) for `useGetUsage()` if it doesn't already exist
- Create: `web/src/tavus/interview-view.tsx`, `__tests__/interview-view.test.tsx`
- Create: `web/src/components/loading.tsx` (extract from `web/src/app.tsx:31`'s local `Loading()` — plan-review M3)
- Modify: `web/src/app.tsx` to use the extracted `Loading`
- Modify: `web/src/pages/session-config.tsx`, add `web/src/pages/__tests__/session-config.test.tsx`
- Modify: `web/src/pages/interview.tsx`, `web/src/pages/__tests__/interview.test.tsx`
- Modify: `internal/handler/middleware.go` (extend `SecurityHeaders` signature to accept `tavusEnabled bool`; add CSP `frame-src` and Permissions-Policy gated on it — plan-review H2)
- Modify: `internal/handler/server.go` (pass `cfg.Tavus.Enabled` to `SecurityHeaders`)
- Modify: `internal/handler/middleware_test.go`

- [ ] **Step 1:** Extract `web/src/components/loading.tsx` from `web/src/app.tsx:31`'s local function. Update `app.tsx` to import it. No behavior change.
- [ ] **Step 2:** `useSystem()` hook with `staleTime: Infinity` over `SystemService.GetSystem({ name: "system" })`.
- [ ] **Step 3:** Confirm/use existing `useGetUsage()` hook (over `UserService.GetUsage`, returns `paid_balance`). If a hook doesn't exist, add one.
- [ ] **Step 4:** Create `web/src/tavus/interview-view.tsx`. Renders the iframe + "End interview" button. **No `useEffect` cleanup** that fires `EndConversation` (plan-review M2 + spec correction). Cost-leak ceiling is 60s of `participant_absent_timeout`.
- [ ] **Step 5:** Edit `session-config.tsx`:
  - `const { data: system } = useSystem()`
  - `const { data: usage } = useGetUsage()`
  - When `system?.tavusAvailable && (usage?.paidBalance ?? 0) > 0`: render an "Enable Tavus interviewer" toggle. Toggle state lives in `sessionStorage['sabermatic.tavus_mode']`; reads via `useState(() => sessionStorage.getItem(...) === '1')`; writes `sessionStorage.setItem(...)` on change.
  - On submit: if toggle on, send `mode: SessionMode.TAVUS` to `CreateSession`.
- [ ] **Step 6:** Edit `interview.tsx`:
  ```tsx
  const TavusInterview = lazy(() => import('@/tavus/interview-view'));
  // inside the component:
  if (session.mode === SessionMode.TAVUS) {
    return <Suspense fallback={<Loading/>}><TavusInterview session={session} /></Suspense>;
  }
  // Existing UI also extends to handle session.status === SESSION_STATUS_PROVISIONING
  // alongside SESSION_STATUS_GENERATING (reuse the existing "preparing" UI).
  ```
- [ ] **Step 7:** Extend `internal/handler/middleware.go::SecurityHeaders` signature to `SecurityHeaders(secureCookies, tavusEnabled bool, next http.Handler) http.Handler`. When `tavusEnabled`:
  - Append ` https://*.daily.co` to the existing CSP `frame-src` (currently absent — add it). Default ships with the wildcard-subdomain branch per spec round-3 M4 default; comment notes the C1 origin probe will potentially tighten this.
  - Do **not** set a document-level `Permissions-Policy` header (wildcards aren't supported there); the iframe `allow=` attribute on the element provides per-iframe delegation.
  Update the call site in `internal/handler/server.go:56` to pass `cfg.Tavus.Enabled`.
- [ ] **Step 8:** Frontend tests:
  - `interview-view.test.tsx`: iframe `src` matches session URL; iframe `allow` has all four tokens; End button calls EndSession mutation. **Assert** unmount does NOT fire any EndConversation mutation (regression guard against re-introducing the broken cleanup).
  - `interview.test.tsx`: lazy branch renders `TavusInterview` when `session.mode === TAVUS`. Use `vi.mock('@/tavus/interview-view', () => ({ default: () => <div data-testid="tavus-interview"/> }))` hoisted at file top; `await waitFor(() => screen.getByTestId('tavus-interview'))`. Standard mode renders existing UI.
  - `session-config.test.tsx`: toggle hidden when `tavus_available=false`; toggle hidden when `paid_balance=0`; toggle visible when both true; clicking writes to `sessionStorage`; submit sends `mode=TAVUS` when toggle on.
  - `middleware_test.go`: `SecurityHeaders(_, tavusEnabled=true, ...)` includes `https://*.daily.co` in `frame-src`; `tavusEnabled=false` does not.
- [ ] **Step 9:** `make test` passes. Commit `feat(tavus): C6 frontend integration`.

---

## Task C7: Bootstrap script + drillctl + docs

**Files:**
- Create: `cmd/drillctl/tavus.go`, `cmd/drillctl/tavus_test.go` (smoke test — plan-review N3)
- Create: `internal/interview/prompt/persona.go` (extract persona system prompt as exported const — plan-review H5)
- Modify: `internal/interview/prompt/prompt.go` (consume the new const in `WithSystemInstructions`)
- Modify: `internal/interview/prompt/prompt_test.go` (snapshot the existing prompt to confirm extraction is byte-equivalent)
- Modify: `scripts/cloud_bootstrap.py` (prompt for Tavus secrets, run `drillctl tavus-bootstrap`)
- Modify: `.env.example`
- Modify: `terraform/cloud_run.tf` (env entries + Secret Manager refs)
- Create: `docs/tavus-bootstrap.md`

- [ ] **Step 1: Persona extraction (plan-review H5).**
  - Read the current persona block from `internal/interview/prompt/prompt.go::WithSystemInstructions`. The block starts at `b.system.WriteString(\`You are a senior staff engineer...\`)`.
  - Extract the literal string into a new `internal/interview/prompt/persona.go`:
    ```go
    package prompt

    // TavusPersonaSystemPrompt is the system prompt used by the Tavus CVI
    // persona. Equivalent to the body written by Builder.WithSystemInstructions
    // in prompt.go; both are derived from this constant.
    const TavusPersonaSystemPrompt = `You are a senior staff engineer ...`
    ```
  - Refactor `WithSystemInstructions` to write `TavusPersonaSystemPrompt` (plus any post-prompt additions that exist today — keep the byte-for-byte output).
  - Add a snapshot test in `prompt_test.go` that asserts `Builder.NewInterviewerPrompt().WithSystemInstructions(...).String()` is unchanged from the pre-extraction value (red-green proof of zero behavioral change).
- [ ] **Step 2: `cmd/drillctl/tavus.go`** — three subcommands (`tavus-bootstrap`, `tavus-persona-sync`, `tavus-orphan-scan`).
  - `tavus-bootstrap`: POST `/v2/personas` with body `{"system_prompt": prompt.TavusPersonaSystemPrompt, "default_replica_id": cfg.Tavus.ReplicaID, "persona_name": "Sabermatic Interviewer"}`. Print returned `persona_id` for operator to paste into Secret Manager. Idempotent? No — if you call twice you get two personas. Add a guard: check env `TAVUS_PERSONA_ID`; if set, refuse with a hint to use `tavus-persona-sync` instead.
  - `tavus-persona-sync`: PATCH `/v2/personas/{TAVUS_PERSONA_ID}` with `{"system_prompt": prompt.TavusPersonaSystemPrompt}`. Idempotent.
  - `tavus-orphan-scan`: list Tavus conversations via `GET /v2/conversations?status=active`; cross-reference with `interview_sessions.tavus_conversation_id`; for any Tavus-side ID not in our DB, call `EndConversation`. Ops-only emergency tool.
- [ ] **Step 3: `cmd/drillctl/tavus_test.go`** — smoke test using `tavustest.NewFakeClient` to verify each subcommand wires the right HTTP method/path/body.
- [ ] **Step 4:** Update `scripts/cloud_bootstrap.py` to prompt for `TAVUS_API_KEY`, `TAVUS_REPLICA_ID`, `TAVUS_WEBHOOK_SECRET` and shell out to `drillctl tavus-bootstrap` to generate `TAVUS_PERSONA_ID`. All four go to Secret Manager. `TAVUS_ENABLED=true` written as a plain Cloud Run env.
- [ ] **Step 5:** Update `.env.example` per spec's Infrastructure section.
- [ ] **Step 6:** Update `terraform/cloud_run.tf` per spec's Infrastructure section (mirror `STRIPE_*` wiring).
- [ ] **Step 7:** Write `docs/tavus-bootstrap.md`:
  - Account creation
  - Secret provisioning order: API key → replica id → webhook secret → bootstrap → persona id
  - `drillctl tavus-bootstrap` invocation
  - `drillctl tavus-persona-sync` for prompt updates after deploy
  - `drillctl tavus-orphan-scan` for emergencies
  - Local-dev tunnel (cloudflared/ngrok) with example command
  - Webhook URL format: `${BASE_URL}/api/webhooks/tavus`
  - Persona-drift warning interpretation
- [ ] **Step 8:** Boot the app and confirm the persona-drift warning fires when prompt deviates (or doesn't fire when matched). This is a local check; production verification is on the user once credentials exist.
- [ ] **Step 9:** `make test` passes. Commit `feat(tavus): C7 bootstrap, drillctl, docs`.

---

## Verification gates

Before marking the implementation complete:

- [ ] All nine commits land on `claude/integrate-tavus-voice-4dtzy`.
- [ ] `make test` green at HEAD.
- [ ] Spec-vs-implementation verification subagent run; fix all findings; re-verify.
- [ ] Push the branch.

Acceptance gates from the spec's "Success criteria" requiring **live Tavus credentials** are explicitly deferred to the user:

- Daily room iframe loads, replica avatar visible.
- Conversation transcribed into `messages` with `input_method='tavus_voice'`.
- "End interview" terminates the conversation in the Tavus dashboard.
- `session_ended` event includes `provider=tavus` attribute in BQ.
- C1 webhook signature probe (Open Q 1) — escalate to user with a request-bin URL.
- C6 iframe origin probe — default ships with wildcard-subdomain CSP.

## Implementation order recap

```
C-1 ──► C0 ──► C1 ──► C2 ──► C3 ──► C4 ──┬─► C5
                                          └─► C6 ──► C7
```

(C5 and C6 may be developed in parallel after C4; C7 depends on C5 and C6 both having landed because it touches `cmd/drillctl/`, `terraform/`, and `scripts/cloud_bootstrap.py` which integrate with the full feature.)
