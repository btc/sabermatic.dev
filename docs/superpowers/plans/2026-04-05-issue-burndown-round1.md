# Issue Burndown Round 1 — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix 8 high-priority issues across 6 independent tracks, each producing a GitHub PR, without conflicting with the in-flight connectRPC migration.

**Architecture:** 6 independent tasks, each self-contained in an isolated git worktree branched from `main`. Tasks A-D are fully parallel. Task E is internally sequential (#34 then #35). Task F is internally sequential (#24 then #40). Each task ends with a PR via `gh pr create`.

**Tech Stack:** Go 1.25, sqlc, River (job queue), React 19, react-router-dom 7, vitest, react-error-boundary, GitHub Actions

**Spec:** `docs/superpowers/specs/2026-04-05-issue-burndown-round1-design.md`

---

## File Structure

**Task A (Track A — #21):**
- Modify: `sql/queries/sessions.sql`
- Modify: `internal/jobs/cleanup.go`
- Modify: `internal/jobs/cleanup_test.go`
- Regenerate: `internal/db/sessions.sql.go`, `internal/db/querier.go` (via `sqlc generate`)

**Task B (Track B — #19):**
- Investigate + fix: `internal/backend/billing.go`, `internal/backend/billing_test.go`
- Investigate + fix: `internal/handler/billing.go`, `internal/handler/billing_test.go`
- Investigate: `sql/queries/grants.sql`, `internal/db/grants.sql.go`

**Task C (Track C — #43):**
- Modify: `Dockerfile`
- Modify: `.github/workflows/ci.yml`

**Task D (Track D — #8):**
- Create: `internal/handler/security.go`
- Create: `internal/handler/security_test.go`
- Modify: `internal/handler/routes.go`

**Task E (Track E — #34 + #35):**
- Create: `web/src/components/error-fallback.tsx`
- Create: `web/src/pages/not-found.tsx`
- Create: `web/src/__tests__/error-boundary.test.tsx`
- Create: `web/src/__tests__/not-found.test.tsx`
- Modify: `web/src/main.tsx`
- Modify: `web/src/app.tsx`
- Modify: `web/package.json`

**Task F (Track F — #24 + #40):**
- Modify: `.github/workflows/ci.yml`

---

### Task A: Fix cleanup worker — cancel abandoned sessions, don't evaluate (#21)

**Branch:** `fix/empty-session-eval`

**Files:**
- Modify: `sql/queries/sessions.sql:41-48`
- Modify: `internal/jobs/cleanup.go:36-52`
- Modify: `internal/jobs/cleanup_test.go`
- Regenerate: `internal/db/sessions.sql.go`, `internal/db/querier.go`

**Context for the implementer:**

The cleanup worker (`internal/jobs/cleanup.go`) sweeps abandoned sessions. Currently it calls `MarkAbandonedSessionsCompleted` which sets status to `completed` and enqueues evaluation. Empty sessions should be **cancelled**, not completed, and should NOT trigger evaluation. The existing `CancelSession` SQL query (`sql/queries/sessions.sql:68-71`) shows the correct pattern — it sets `status = 'cancelled'`, `ended_at = NOW()`, and `archived_at = NOW()`.

The evaluator (`internal/jobs/evaluate.go:80-90`) already has a zero-message guard — it stays as defense-in-depth.

- [ ] **Step 1: Rename and modify the SQL query**

In `sql/queries/sessions.sql`, find the existing query (lines 41-48):

```sql
-- name: MarkAbandonedSessionsCompleted :many
-- Batch-marks all abandoned sessions as completed and returns their IDs.
-- A session is abandoned if it's active and past its duration + 5 min buffer.
UPDATE interview_sessions
SET status = 'completed', ended_at = NOW(), updated_at = NOW()
WHERE status = 'active'
  AND started_at + (config_duration_minutes + 5) * INTERVAL '1 minute' < NOW()
RETURNING id;
```

Replace it with:

```sql
-- name: MarkAbandonedSessionsCancelled :many
-- Batch-marks all abandoned sessions as cancelled and returns their IDs.
-- A session is abandoned if it's active and past its duration + 5 min buffer.
-- Sets archived_at to keep cancelled sessions out of active lists (consistent
-- with CancelSession).
UPDATE interview_sessions
SET status = 'cancelled', ended_at = NOW(), archived_at = NOW(), updated_at = NOW()
WHERE status = 'active'
  AND started_at + (config_duration_minutes + 5) * INTERVAL '1 minute' < NOW()
RETURNING id;
```

- [ ] **Step 2: Regenerate sqlc code**

Run:
```bash
sqlc generate
```

Expected: `internal/db/sessions.sql.go` and `internal/db/querier.go` are updated. The function `MarkAbandonedSessionsCompleted` is replaced by `MarkAbandonedSessionsCancelled`.

Verify:
```bash
sqlc diff
```
Expected: no diff (generated code matches queries).

- [ ] **Step 3: Update cleanup worker to use new query and skip evaluation**

In `internal/jobs/cleanup.go`, replace the `Work` method body. Current code (lines 28-62):

```go
func (w *CleanupAbandonedSessionsWorker) Work(ctx context.Context, job *river.Job[CleanupAbandonedSessionsArgs]) error {
	tx, err := w.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin cleanup tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// Batch-mark all abandoned sessions as completed in a single UPDATE.
	ids, err := db.New(tx).MarkAbandonedSessionsCompleted(ctx)
	if err != nil {
		return fmt.Errorf("mark abandoned sessions: %w", err)
	}

	q := db.New(tx)

	// Refund unused minutes and enqueue evaluation for each.
	for _, id := range ids {
		if _, err := q.RefundSessionMinutes(ctx, pgtype.UUID{Bytes: id, Valid: true}); err != nil {
			slog.Warn("cleanup: refund failed", "session_id", id, "error", err)
		}

		if _, err := w.Jobs.InsertTx(ctx, tx, EvaluateSessionArgs{SessionID: id}, EvaluateSessionInsertOpts()); err != nil {
			return fmt.Errorf("enqueue evaluation for session %s: %w", id, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit cleanup: %w", err)
	}

	if len(ids) > 0 {
		slog.Info("cleaned up abandoned sessions", "count", len(ids))
	}
	return nil
}
```

Replace with:

```go
func (w *CleanupAbandonedSessionsWorker) Work(ctx context.Context, job *river.Job[CleanupAbandonedSessionsArgs]) error {
	tx, err := w.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin cleanup tx: %w", err)
	}
	defer tx.Rollback(ctx)

	ids, err := db.New(tx).MarkAbandonedSessionsCancelled(ctx)
	if err != nil {
		return fmt.Errorf("mark abandoned sessions cancelled: %w", err)
	}

	q := db.New(tx)

	// Refund unused minutes. Do NOT enqueue evaluation — cancelled sessions
	// with empty transcripts should not be evaluated.
	for _, id := range ids {
		if _, err := q.RefundSessionMinutes(ctx, pgtype.UUID{Bytes: id, Valid: true}); err != nil {
			slog.Warn("cleanup: refund failed", "session_id", id, "error", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit cleanup: %w", err)
	}

	if len(ids) > 0 {
		slog.Info("cancelled abandoned sessions", "count", len(ids))
	}
	return nil
}
```

- [ ] **Step 4: Update existing test to assert cancelled status**

In `internal/jobs/cleanup_test.go`, the existing `TestCleanupAbandonedSessions_MarksCompleted` (line 16) asserts `"completed"` on line 58. Update the test:

Change the function name and assertions:

```go
func TestCleanupAbandonedSessions_CancelsAndArchives(t *testing.T) {
```

Change line 58 from:
```go
assert.Equal(t, "completed", session.Status)
```
to:
```go
assert.Equal(t, "cancelled", session.Status)
```

Change line 59 from:
```go
assert.True(t, session.EndedAt.Valid, "ended_at should be set after MarkSessionCompleted")
```
to:
```go
assert.True(t, session.EndedAt.Valid, "ended_at should be set after cancellation")
assert.True(t, session.ArchivedAt.Valid, "archived_at should be set after cancellation")
```

Note: Check that `GetSessionRow` has an `ArchivedAt` field. If the `GetSession` query doesn't return `archived_at`, query the session directly:
```go
var archivedAt pgtype.Timestamptz
err = pool.QueryRow(ctx, `SELECT archived_at FROM interview_sessions WHERE id = $1`, seed.SessionID).Scan(&archivedAt)
require.NoError(t, err)
assert.True(t, archivedAt.Valid, "archived_at should be set after cancellation")
```

- [ ] **Step 5: Add test verifying no evaluation job is enqueued**

Add a new test in `internal/jobs/cleanup_test.go`:

```go
func TestCleanupAbandonedSessions_DoesNotEnqueueEvaluation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	pool := startTestPostgres(t)

	seed := seedSessionWithMessages(t, ctx, pool, 0) // zero messages

	// Set to active and backdate.
	_, err := pool.Exec(ctx,
		`UPDATE interview_sessions SET status = 'active', started_at = NOW() - INTERVAL '2 hours' WHERE id = $1`,
		seed.SessionID,
	)
	require.NoError(t, err)

	riverClient, err := river.NewClient(riverpgxv5.New(pool), &river.Config{})
	require.NoError(t, err)
	worker := &jobs.CleanupAbandonedSessionsWorker{
		Pool: pool,
		Jobs: riverClient,
	}

	err = worker.Work(ctx, &river.Job[jobs.CleanupAbandonedSessionsArgs]{
		Args: jobs.CleanupAbandonedSessionsArgs{},
	})
	require.NoError(t, err)

	// Verify session is cancelled.
	q := db.New(pool)
	session, err := q.GetSession(ctx, seed.SessionID)
	require.NoError(t, err)
	assert.Equal(t, "cancelled", session.Status)

	// Verify no evaluation job was enqueued.
	var jobCount int
	err = pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM river_job WHERE args->>'session_id' = $1 AND kind = 'evaluate_session'`,
		seed.SessionID.String(),
	).Scan(&jobCount)
	require.NoError(t, err)
	assert.Equal(t, 0, jobCount, "no evaluation job should be enqueued for cancelled sessions")
}
```

Note: If `seedSessionWithMessages` doesn't support 0 messages, you may need to seed a session differently. Check the helper in `internal/jobs/evaluate_test.go:115` — if it requires at least 1 message, create a session without calling the message-seeding part.

- [ ] **Step 6: Run tests**

```bash
go test -run TestCleanupAbandonedSessions ./internal/jobs/ -v -count=1
```

Expected: Both tests pass. The `_CancelsAndArchives` test confirms status is `cancelled` and `archived_at` is set. The `_DoesNotEnqueueEvaluation` test confirms no evaluation job.

Also run the full jobs package:
```bash
go test ./internal/jobs/ -v -count=1
```

Expected: All tests pass including the unchanged `TestCleanupAbandonedSessions_RecentSessionNotAffected`.

- [ ] **Step 7: Commit, push, and create PR**

```bash
git add sql/queries/sessions.sql internal/db/sessions.sql.go internal/db/querier.go internal/jobs/cleanup.go internal/jobs/cleanup_test.go
git commit -m "fix: cleanup worker cancels abandoned sessions instead of completing them

Abandoned sessions with empty transcripts were being marked 'completed'
and triggering evaluation, producing garbage all-1 scores. Now marks
them 'cancelled' with archived_at set, and does not enqueue evaluation.

Closes #21"
git push -u origin fix/empty-session-eval
gh pr create --title "Fix: cleanup worker cancels abandoned sessions, skips evaluation" --body "$(cat <<'EOF'
## Summary
- Changes `MarkAbandonedSessionsCompleted` → `MarkAbandonedSessionsCancelled` to set `status = 'cancelled'` and `archived_at = NOW()`
- Removes evaluation job enqueue from cleanup worker — cancelled sessions should not be evaluated
- Keeps minute refund behavior unchanged
- Evaluator zero-message guard remains as defense-in-depth

Closes #21

## Test plan
- [ ] `TestCleanupAbandonedSessions_CancelsAndArchives` — verifies cancelled status and archived_at
- [ ] `TestCleanupAbandonedSessions_DoesNotEnqueueEvaluation` — verifies no evaluation job enqueued
- [ ] `TestCleanupAbandonedSessions_RecentSessionNotAffected` — existing test still passes
- [ ] `sqlc diff` shows no drift

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

---

### Task B: Fix pre-existing billing test failures (#19)

**Branch:** `fix/preexisting-test-failures`

**Files:**
- Investigate + fix: `internal/backend/billing_test.go:326` (`TestReserveMinutes_ZeroGrants`)
- Investigate + fix: `internal/handler/billing_test.go:19` (`TestGetUsage_EmptyBalance`)
- Investigate: `internal/backend/billing.go` (CreateSession, GetUsageSummary)
- Investigate: `sql/queries/grants.sql` (GetBillingSnapshot, GetUserUsageSummary)
- Investigate: `internal/db/grants.sql.go` (generated query implementations)

**Context for the implementer:**

Two billing integration tests fail consistently. These are pre-existing failures, not caused by recent work. Both tests involve users with **zero grants** (no billing grants at all).

Key code paths:
- `TestReserveMinutes_ZeroGrants` → calls `b.CreateSession()` → internally calls `b.CheckTx()` → calls `GetBillingSnapshot` SQL query
- `TestGetUsage_EmptyBalance` → calls `b.GetUsageSummary()` → calls `EnsureFreeGrantTx()` then `GetUserUsageSummary` SQL query

The `GetBillingSnapshot` query (`sql/queries/grants.sql:231`) uses `LEFT JOIN grants` with `GROUP BY`. With zero grants, the LEFT JOIN returns one row per user with NULLs for grant columns. The COALESCE handles NULLs → 0. This *should* work, but investigate whether `GROUP BY` or the query structure causes issues with zero rows.

The `GetUsageSummary` method calls `EnsureFreeGrantTx` first, which should create a free grant. If `EnsureFreeGrant` has a bug when the user has no existing grants, `GetUserUsageSummary` could fail.

- [ ] **Step 1: Run both tests to capture exact failure output**

```bash
go test -run TestReserveMinutes_ZeroGrants ./internal/backend/ -v -count=1 2>&1 | head -50
```

```bash
go test -run TestGetUsage_EmptyBalance ./internal/handler/ -v -count=1 2>&1 | head -50
```

If either test passes, note it in the PR and skip fixing that one.

- [ ] **Step 2: Read the test code and functions under test**

Read and understand:
1. `internal/backend/billing_test.go:326-341` — the ZeroGrants test
2. `internal/backend/billing.go` — `CreateSession` path (find where it calls `CheckTx`/`GetBillingSnapshot`)
3. `internal/handler/billing_test.go:19-40` — the EmptyBalance test
4. `internal/backend/billing.go:64-109` — `GetUsageSummary` method
5. `sql/queries/grants.sql:231-245` — `GetBillingSnapshot` query
6. `sql/queries/grants.sql:247-254` — `GetUserUsageSummary` query

- [ ] **Step 3: Diagnose root cause**

Common failure patterns to check:
- Does `GetBillingSnapshot` return `pgx.ErrNoRows` when there are no grants? (LEFT JOIN should prevent this, but GROUP BY might collapse to zero rows if the user doesn't exist or has some other issue)
- Does `seedUser(t, b)` in the backend test create a user with the expected `plan` field?
- Does `createTestUser(t, pool)` in the handler test create a user with the same schema expectations?
- Does `EnsureFreeGrant` fail silently when the grants table is empty?
- Is there a foreign key or constraint that prevents the test setup from completing?

- [ ] **Step 4: Fix the root cause**

Apply the fix in the source code (not the tests, unless the tests themselves have incorrect assertions). If the tests are aspirational — testing behavior that was never implemented — implement the missing feature.

- [ ] **Step 5: Verify both tests pass**

```bash
go test -run TestReserveMinutes_ZeroGrants ./internal/backend/ -v -count=1
go test -run TestGetUsage_EmptyBalance ./internal/handler/ -v -count=1
```

Expected: PASS for both.

- [ ] **Step 6: Run full test suites to verify no regressions**

```bash
go test ./internal/backend/ -v -count=1 -timeout=300s
go test ./internal/handler/ -v -count=1 -timeout=300s
```

Expected: All tests pass.

- [ ] **Step 7: Commit, push, and create PR**

```bash
git add <fixed files>
git commit -m "fix: resolve pre-existing billing test failures

<describe the root cause and fix here>

Closes #19"
git push -u origin fix/preexisting-test-failures
gh pr create --title "Fix: pre-existing billing test failures" --body "$(cat <<'EOF'
## Summary
- Fixes `TestReserveMinutes_ZeroGrants` and `TestGetUsage_EmptyBalance`
- Root cause: <describe>
- Fix: <describe>

Closes #19

## Test plan
- [ ] `TestReserveMinutes_ZeroGrants` passes
- [ ] `TestGetUsage_EmptyBalance` passes
- [ ] Full `go test ./internal/backend/` passes
- [ ] Full `go test ./internal/handler/` passes

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

---

### Task C: Fix Dockerfile Go version mismatch (#43)

**Branch:** `fix/dockerfile-go-version`

**Files:**
- Modify: `Dockerfile:10`
- Modify: `.github/workflows/ci.yml`

- [ ] **Step 1: Update Dockerfile Go version**

In `Dockerfile`, change line 10 from:

```dockerfile
FROM golang:1.24-alpine AS backend
```

to:

```dockerfile
FROM golang:1.25-alpine AS backend
```

- [ ] **Step 2: Add Go version consistency check to CI**

In `.github/workflows/ci.yml`, add this step immediately after the `Checkout` step (before `Setup Go`):

```yaml
      - name: Check Go version consistency
        run: |
          MOD=$(grep '^go ' go.mod | awk '{print $2}' | cut -d. -f1,2)
          DOCKER=$(grep 'FROM golang:' Dockerfile | head -1 | sed 's/.*golang:\([0-9]*\.[0-9]*\).*/\1/')
          CI=$(grep 'go-version:' .github/workflows/ci.yml | head -1 | sed 's/.*"\([0-9]*\.[0-9]*\).*/\1/')
          echo "go.mod: $MOD | Dockerfile: $DOCKER | CI: $CI"
          FAIL=0
          if [ "$MOD" != "$DOCKER" ]; then
            echo "::error::go.mod ($MOD) != Dockerfile ($DOCKER)"
            FAIL=1
          fi
          if [ "$MOD" != "$CI" ]; then
            echo "::error::go.mod ($MOD) != CI ($CI)"
            FAIL=1
          fi
          exit $FAIL
```

- [ ] **Step 3: Verify Dockerfile builds**

```bash
docker build -t drill-test .
```

Expected: Build succeeds with `golang:1.25-alpine`.

If Docker is not available locally, verify the version string is correct:
```bash
grep 'FROM golang:' Dockerfile
grep '^go ' go.mod
grep 'go-version:' .github/workflows/ci.yml
```

Expected: All show `1.25` as the major.minor.

- [ ] **Step 4: Commit, push, and create PR**

```bash
git add Dockerfile .github/workflows/ci.yml
git commit -m "fix: align Dockerfile Go version with go.mod (1.24 → 1.25)

Adds CI check to prevent future version drift between go.mod,
Dockerfile, and ci.yml.

Closes #43"
git push -u origin fix/dockerfile-go-version
gh pr create --title "Fix: Dockerfile Go version mismatch (1.24 → 1.25)" --body "$(cat <<'EOF'
## Summary
- Updates Dockerfile from `golang:1.24-alpine` to `golang:1.25-alpine` to match go.mod
- Adds CI step that extracts Go versions from go.mod, Dockerfile, and ci.yml and fails if they diverge

Closes #43

## Test plan
- [ ] `grep 'FROM golang:' Dockerfile` shows 1.25
- [ ] CI version check step passes with all three files aligned
- [ ] Docker build succeeds (if testable)

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

---

### Task D: Add security response headers (#8)

**Branch:** `feat/security-headers`

**Files:**
- Create: `internal/handler/security.go`
- Create: `internal/handler/security_test.go`
- Modify: `internal/handler/routes.go:20-55`

**Context for the implementer:**

`internal/handler/routes.go:NewHandler()` builds the HTTP middleware chain: routes → CSRF → OTel. No security headers are set. Add a middleware that sets standard security headers on every response.

The frontend telemetry (`web/src/telemetry/provider.ts`) sends OTLP traces to `/api/traces` (same origin) in production, so the CSP `connect-src 'self' wss:` is sufficient — no external URLs.

- [ ] **Step 1: Write failing test**

Create `internal/handler/security_test.go`:

```go
package handler_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/btc/drill/internal/handler"
)

func TestSecurityHeaders_AlwaysPresent(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	h := handler.SecurityHeaders(false, mux)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "DENY", w.Header().Get("X-Frame-Options"))
	assert.Equal(t, "strict-origin-when-cross-origin", w.Header().Get("Referrer-Policy"))
	assert.Contains(t, w.Header().Get("Content-Security-Policy"), "default-src 'self'")
	assert.Empty(t, w.Header().Get("Strict-Transport-Security"), "HSTS should not be set for non-secure")
}

func TestSecurityHeaders_HSTSWhenSecure(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	h := handler.SecurityHeaders(true, mux)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, "max-age=63072000; includeSubDomains", w.Header().Get("Strict-Transport-Security"))
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test -run TestSecurityHeaders ./internal/handler/ -v -count=1
```

Expected: FAIL — `SecurityHeaders` function doesn't exist yet.

- [ ] **Step 3: Implement security headers middleware**

Create `internal/handler/security.go`:

```go
package handler

import "net/http"

// SecurityHeaders returns a middleware that sets standard security hardening
// headers on every response. HSTS is only set when secureCookies is true
// (production/HTTPS).
func SecurityHeaders(secureCookies bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data:; connect-src 'self' wss:; font-src 'self'; frame-ancestors 'none'")

		if secureCookies {
			w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		}

		next.ServeHTTP(w, r)
	})
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test -run TestSecurityHeaders ./internal/handler/ -v -count=1
```

Expected: PASS for both `TestSecurityHeaders_AlwaysPresent` and `TestSecurityHeaders_HSTSWhenSecure`.

- [ ] **Step 5: Wire middleware into NewHandler**

In `internal/handler/routes.go`, modify the `NewHandler` function. The current return statement (lines 45-54):

```go
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/webhooks/stripe" && r.Method == http.MethodPost {
			otelHandler.ServeHTTP(w, r)
			return
		}
		if !secureCookies {
			r = csrf.PlaintextHTTPRequest(r)
		}
		csrfProtected.ServeHTTP(w, r)
	})
```

Replace with:

```go
	return SecurityHeaders(secureCookies, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/webhooks/stripe" && r.Method == http.MethodPost {
			otelHandler.ServeHTTP(w, r)
			return
		}
		if !secureCookies {
			r = csrf.PlaintextHTTPRequest(r)
		}
		csrfProtected.ServeHTTP(w, r)
	}))
```

- [ ] **Step 6: Run full handler test suite**

```bash
go test ./internal/handler/ -v -count=1 -timeout=300s
```

Expected: All tests pass including new security header tests.

- [ ] **Step 7: Commit, push, and create PR**

```bash
git add internal/handler/security.go internal/handler/security_test.go internal/handler/routes.go
git commit -m "feat: add security response headers middleware

Adds X-Content-Type-Options, X-Frame-Options, Referrer-Policy, CSP,
and HSTS (production only) to all HTTP responses.

Closes #8"
git push -u origin feat/security-headers
gh pr create --title "Add security response headers (HSTS, CSP, X-Frame-Options)" --body "$(cat <<'EOF'
## Summary
- New `SecurityHeaders` middleware in `internal/handler/security.go`
- Sets X-Content-Type-Options: nosniff, X-Frame-Options: DENY, Referrer-Policy, CSP on all responses
- HSTS only set when secureCookies=true (production)
- CSP allows 'unsafe-inline' for styles (Tailwind), wss: for WebSocket, data: for images

Closes #8

## Test plan
- [ ] `TestSecurityHeaders_AlwaysPresent` — verifies headers on non-secure responses
- [ ] `TestSecurityHeaders_HSTSWhenSecure` — verifies HSTS on secure responses
- [ ] Full handler test suite passes
- [ ] Manual: load app in browser, check response headers in DevTools Network tab, check console for CSP violations

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

---

### Task E: Add React Error Boundary and 404 catch-all route (#34 + #35)

**Branch:** `feat/error-boundary-and-404`

**Files:**
- Create: `web/src/components/error-fallback.tsx`
- Create: `web/src/pages/not-found.tsx`
- Create: `web/src/__tests__/error-boundary.test.tsx`
- Create: `web/src/__tests__/not-found.test.tsx`
- Modify: `web/src/main.tsx`
- Modify: `web/src/app.tsx`
- Modify: `web/package.json` / `web/package-lock.json`

**Context for the implementer:**

`web/src/main.tsx` renders the app tree with no error boundary. `web/src/app.tsx` defines all routes but has no `<Route path="*">` catch-all. The vitest config (`web/vitest.config.ts`) uses jsdom environment and `@testing-library/jest-dom` setup. Existing tests import `{ describe, it, expect, vi, beforeEach }` from `"vitest"`.

Do #34 (ErrorBoundary) first, then #35 (404 route). Commit after each.

#### Part 1: Error Boundary (#34)

- [ ] **Step 1: Install react-error-boundary**

```bash
cd web && npm install react-error-boundary
```

- [ ] **Step 2: Write failing test for error boundary**

Create `web/src/__tests__/error-boundary.test.tsx`:

```tsx
import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { ErrorBoundary } from "react-error-boundary";
import { ErrorFallback } from "@/components/error-fallback";

function ThrowingComponent(): never {
  throw new Error("Test render error");
}

describe("ErrorFallback", () => {
  it("renders fallback UI when a child component throws", () => {
    // Suppress React error boundary console.error noise in test output.
    vi.spyOn(console, "error").mockImplementation(() => {});

    render(
      <ErrorBoundary FallbackComponent={ErrorFallback}>
        <ThrowingComponent />
      </ErrorBoundary>,
    );

    expect(screen.getByText(/something went wrong/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /reload/i })).toBeInTheDocument();
  });
});
```

- [ ] **Step 3: Run test to verify it fails**

```bash
cd web && npm run test -- --reporter=verbose 2>&1 | tail -20
```

Expected: FAIL — `@/components/error-fallback` doesn't exist.

- [ ] **Step 4: Create error fallback component**

Create `web/src/components/error-fallback.tsx`:

```tsx
import type { FallbackProps } from "react-error-boundary";

export function ErrorFallback({ resetErrorBoundary }: FallbackProps) {
  return (
    <div className="flex h-screen flex-col items-center justify-center gap-4 text-center">
      <h1 className="text-2xl font-semibold">Something went wrong</h1>
      <p className="text-muted-foreground">
        An unexpected error occurred. Please try reloading the page.
      </p>
      <button
        onClick={resetErrorBoundary}
        className="rounded-md bg-primary px-4 py-2 text-primary-foreground hover:bg-primary/90"
      >
        Reload page
      </button>
    </div>
  );
}
```

- [ ] **Step 5: Run test to verify it passes**

```bash
cd web && npm run test -- --reporter=verbose 2>&1 | tail -20
```

Expected: PASS for `ErrorFallback > renders fallback UI when a child component throws`.

- [ ] **Step 6: Add ErrorBoundary to main.tsx**

In `web/src/main.tsx`, add the import and wrap the app tree. Current file:

```tsx
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { BrowserRouter } from "react-router-dom";
import { App } from "./app";
import { initTelemetry } from "./telemetry/provider";
import "./index.css";
```

Add imports after the existing ones:

```tsx
import { ErrorBoundary } from "react-error-boundary";
import { ErrorFallback } from "./components/error-fallback";
```

Then change the render tree from:

```tsx
createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <App />
      </BrowserRouter>
    </QueryClientProvider>
  </StrictMode>,
);
```

to:

```tsx
createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <ErrorBoundary
      FallbackComponent={ErrorFallback}
      onReset={() => window.location.reload()}
    >
      <QueryClientProvider client={queryClient}>
        <BrowserRouter>
          <App />
        </BrowserRouter>
      </QueryClientProvider>
    </ErrorBoundary>
  </StrictMode>,
);
```

- [ ] **Step 7: Run all frontend tests**

```bash
cd web && npm run test
```

Expected: All tests pass (existing tests + new error boundary test).

- [ ] **Step 8: Commit error boundary work**

```bash
cd web && git add package.json package-lock.json src/components/error-fallback.tsx src/__tests__/error-boundary.test.tsx src/main.tsx
git commit -m "feat: add React Error Boundary to prevent white screen of death

Wraps the app tree with react-error-boundary. Any unhandled render
error shows a 'Something went wrong' fallback with a reload button
instead of a blank white screen.

Closes #34"
```

#### Part 2: 404 Catch-All Route (#35)

- [ ] **Step 9: Write failing test for 404 page**

Create `web/src/__tests__/not-found.test.tsx`:

```tsx
import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { App } from "@/app";

describe("404 Not Found", () => {
  it("renders not-found page for unknown routes", async () => {
    render(
      <MemoryRouter initialEntries={["/this-does-not-exist"]}>
        <App />
      </MemoryRouter>,
    );

    // App uses React.lazy() for page components, so use findByText (async).
    expect(await screen.findByText(/page not found/i)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /home/i })).toHaveAttribute(
      "href",
      "/",
    );
  });
});
```

- [ ] **Step 10: Run test to verify it fails**

```bash
cd web && npm run test -- --reporter=verbose 2>&1 | tail -20
```

Expected: FAIL — no "page not found" text rendered.

- [ ] **Step 11: Create NotFound page component**

Create `web/src/pages/not-found.tsx`:

```tsx
import { Link } from "react-router-dom";

export default function NotFound() {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-4 py-20 text-center">
      <h1 className="text-4xl font-bold">404</h1>
      <p className="text-lg text-muted-foreground">Page not found</p>
      <Link
        to="/"
        className="text-primary underline underline-offset-4 hover:text-primary/80"
      >
        Go home
      </Link>
    </div>
  );
}
```

- [ ] **Step 12: Add catch-all routes to app.tsx**

In `web/src/app.tsx`, add the lazy import near the other imports:

```tsx
const NotFound = lazy(() => import("@/pages/not-found"));
```

Then add a catch-all route at the end of the `<AppLayout>` group and a top-level catch-all. Current Routes block (lines 28-56):

```tsx
<Routes>
  {/* Auth — standalone layout */}
  <Route path="/login" element={<Login />} />
  <Route path="/signup" element={<Signup />} />
  <Route path="/forgot-password" element={<ForgotPassword />} />
  <Route path="/reset-password" element={<ResetPassword />} />
  <Route path="/verify-email" element={<VerifyEmail />} />

  {/* App — top bar layout */}
  <Route element={<AppLayout />}>
    <Route path="/" element={<Home />} />
    <Route path="/sessions/new" element={<SessionConfig />} />
    <Route path="/sessions/:id" element={<SessionLayout />}>
      <Route index element={<Navigate to="overview" replace />} />
      <Route path="overview" element={<Overview />} />
      <Route path="transcript" element={<TranscriptPage />} />
      <Route path="deep-dive" element={<DeepDive />} />
    </Route>
    <Route path="/history" element={<History />} />
    <Route path="/settings" element={<Settings />} />
    <Route path="/settings/billing" element={<Settings />} />
  </Route>

  {/* Interview — immersive layout */}
  <Route element={<ImmersiveLayout />}>
    <Route path="/sessions/:id/interview" element={<Interview />} />
  </Route>
</Routes>
```

Replace with:

```tsx
<Routes>
  {/* Auth — standalone layout */}
  <Route path="/login" element={<Login />} />
  <Route path="/signup" element={<Signup />} />
  <Route path="/forgot-password" element={<ForgotPassword />} />
  <Route path="/reset-password" element={<ResetPassword />} />
  <Route path="/verify-email" element={<VerifyEmail />} />

  {/* App — top bar layout */}
  <Route element={<AppLayout />}>
    <Route path="/" element={<Home />} />
    <Route path="/sessions/new" element={<SessionConfig />} />
    <Route path="/sessions/:id" element={<SessionLayout />}>
      <Route index element={<Navigate to="overview" replace />} />
      <Route path="overview" element={<Overview />} />
      <Route path="transcript" element={<TranscriptPage />} />
      <Route path="deep-dive" element={<DeepDive />} />
    </Route>
    <Route path="/history" element={<History />} />
    <Route path="/settings" element={<Settings />} />
    <Route path="/settings/billing" element={<Settings />} />
  </Route>

  {/* Interview — immersive layout */}
  <Route element={<ImmersiveLayout />}>
    <Route path="/sessions/:id/interview" element={<Interview />} />
  </Route>

  {/* Catch-all 404 */}
  <Route path="*" element={<NotFound />} />
</Routes>
```

- [ ] **Step 13: Run test to verify it passes**

```bash
cd web && npm run test -- --reporter=verbose 2>&1 | tail -20
```

Expected: PASS for the 404 test. All other tests still pass.

- [ ] **Step 14: Run all frontend tests**

```bash
cd web && npm run test
```

Expected: All tests pass.

- [ ] **Step 15: Commit 404 route work**

```bash
cd web && git add src/pages/not-found.tsx src/__tests__/not-found.test.tsx src/app.tsx
git commit -m "feat: add 404 catch-all route for unknown URLs

Adds a NotFound page and a catch-all route at the end of the Routes
block. Unknown URLs now show a '404 - Page not found' message with a
link home instead of a blank page.

Closes #35"
```

- [ ] **Step 16: Push and create PR**

```bash
git push -u origin feat/error-boundary-and-404
gh pr create --title "Add React Error Boundary and 404 catch-all route" --body "$(cat <<'EOF'
## Summary
- Adds `react-error-boundary` with a fallback UI ("Something went wrong" + reload button) wrapping the entire app tree in `main.tsx`
- Adds a `NotFound` page component and `<Route path="*">` catch-all at the end of the Routes block
- Unknown URLs now show "404 — Page not found" with a link home

Closes #34, closes #35

## Test plan
- [ ] `ErrorFallback` test — component that throws renders fallback UI
- [ ] `404 Not Found` test — unknown route renders not-found page with home link
- [ ] All existing frontend tests pass
- [ ] Manual: navigate to `/nonexistent` in browser, verify 404 page renders

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

---

### Task F: Fix CI — frontend build before Go, add frontend tests (#24 + #40)

**Branch:** `fix/ci-frontend-build-and-tests`

**Files:**
- Modify: `.github/workflows/ci.yml`

**Context for the implementer:**

The current CI workflow (`.github/workflows/ci.yml`) only sets up Go and runs Go tools. It does not build the frontend (`web/dist`), which causes `go vet`, `staticcheck`, and `go test` to fail because `web.go` has `//go:embed web/dist`. It also never runs frontend tests (3 test files in `web/src/`).

The frontend uses npm (see `web/package.json`, `web/package-lock.json`). The test command is `npm run test` (runs `vitest run`). Lint is `npm run lint` (runs `eslint .`).

- [ ] **Step 1: Read the current CI workflow**

Read `.github/workflows/ci.yml` to understand the current structure:

```yaml
name: CI

on:
  pull_request:
    branches: [main]

permissions:
  contents: read

jobs:
  check:
    runs-on: ubuntu-latest

    steps:
      - name: Checkout
        uses: actions/checkout@v4

      - name: Setup Go
        uses: actions/setup-go@v5
        with:
          go-version: "1.25.x"

      - name: Install tools
        run: |
          go install honnef.co/go/tools/cmd/staticcheck@latest
          go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest

      - name: Vet
        run: go vet ./...

      - name: Staticcheck
        run: staticcheck ./...

      - name: Sqlc diff
        run: sqlc diff

      - name: Test
        run: go test ./... -race -count=1 -timeout=300s
```

- [ ] **Step 2: Write the updated CI workflow**

Replace the entire file with:

```yaml
name: CI

on:
  pull_request:
    branches: [main]

permissions:
  contents: read

jobs:
  check:
    runs-on: ubuntu-latest

    steps:
      - name: Checkout
        uses: actions/checkout@v4

      # --- Frontend ---

      - name: Setup Node
        uses: actions/setup-node@v4
        with:
          node-version: "22"
          cache: "npm"
          cache-dependency-path: web/package-lock.json

      - name: Install frontend dependencies
        run: npm ci
        working-directory: web

      - name: Frontend lint
        run: npm run lint
        working-directory: web

      - name: Frontend tests
        run: npm run test
        working-directory: web

      - name: Build frontend
        run: npm run build
        working-directory: web

      # --- Go ---

      - name: Setup Go
        uses: actions/setup-go@v5
        with:
          go-version: "1.25.x"

      - name: Install tools
        run: |
          go install honnef.co/go/tools/cmd/staticcheck@latest
          go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest

      - name: Vet
        run: go vet ./...

      - name: Staticcheck
        run: staticcheck ./...

      - name: Sqlc diff
        run: sqlc diff

      - name: Test
        run: go test ./... -race -count=1 -timeout=300s
```

Key changes:
- Added Node.js 22 setup with npm cache
- Added `npm ci`, `npm run lint`, `npm run test`, `npm run build` in `web/` directory
- All frontend steps come before Go steps so `web/dist` exists for `go vet`/`staticcheck`/`go test`
- Frontend lint and tests run before the build since they don't need `web/dist`

- [ ] **Step 3: Verify frontend steps work locally**

```bash
cd web && npm ci && npm run lint && npm run test && npm run build
```

Expected: All four commands succeed. `web/dist` directory is created after build.

- [ ] **Step 4: Commit, push, and create PR**

```bash
git add .github/workflows/ci.yml
git commit -m "fix: add frontend build and tests to CI pipeline

Frontend was not built before Go compilation, causing embed failures.
Frontend tests were never run in CI. Now CI installs Node, runs
frontend lint/tests, builds web/dist, then runs Go tools.

Closes #24, closes #40"
git push -u origin fix/ci-frontend-build-and-tests
gh pr create --title "Fix CI: build frontend before Go, add frontend tests" --body "$(cat <<'EOF'
## Summary
- Adds Node.js 22 setup and npm cache to CI
- Runs `npm run lint` and `npm run test` in `web/` directory
- Builds frontend (`npm run build`) before Go steps so `//go:embed web/dist` succeeds
- All frontend steps placed before Go steps (vet, staticcheck, test all need web/dist)

Closes #24, closes #40

## Test plan
- [ ] CI pipeline passes on this PR (frontend lint + test + build + Go vet/staticcheck/test)
- [ ] `go vet ./...` no longer fails with "pattern web/dist: no matching files found"
- [ ] Frontend test failures would now block merge

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```
