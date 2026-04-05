# Issue Burndown Round 1 — Design

**Date:** 2026-04-05
**Scope:** 8 high-priority issues that do not conflict with the in-flight connectRPC migration.
**Execution:** 6 parallel tracks using subagents in git worktrees, each producing a GitHub PR.

## Context

The connectRPC migration (issues #47, #48) is actively replacing REST handlers in `internal/handler/`, API hooks in `web/src/api/`, and frontend types in `web/src/api/types.ts`. These 8 issues were selected because they touch layers the migration explicitly leaves unchanged: `internal/backend/`, `internal/jobs/`, `internal/db/`, CI config, Dockerfile, React routing/error handling, and HTTP middleware.

## Conflict Avoidance

**Safe layers (unchanged by connectRPC):**
- `internal/backend/` — business logic
- `internal/jobs/` — worker logic
- `internal/db/` — sqlc queries
- `internal/auth/` — auth package
- `.github/workflows/` — CI config
- `Dockerfile` — build config
- `web/src/app.tsx` — React router structure (connectRPC changes component internals, not the router)
- `web/src/main.tsx` — app entry (connectRPC adds `TransportProvider` but doesn't restructure wrapping)
- HTTP middleware in `internal/handler/routes.go:NewHandler()` — connectRPC adds `rpc.Register()` call in `RegisterRoutes()` but `NewHandler()` middleware chain is structurally stable

**Risky overlap:** Track F (#24, #40) modifies `.github/workflows/ci.yml`. The connectRPC migration may also add CI steps (proto lint, etc. per #48). Low risk — additive changes to different sections of the same file. Easily resolvable if merge conflict arises.

---

## Track A — #21: Empty sessions should not be evaluated

**Branch:** `fix/empty-session-eval`
**PR:** Closes #21

### Problem

`CleanupAbandonedSessionsWorker` (`internal/jobs/cleanup.go`) calls `MarkAbandonedSessionsCompleted` which sets status to `completed`, then enqueues `EvaluateSessionArgs`. When the session has zero candidate messages, the evaluator runs against an empty transcript, producing all-1 scores and wasting LLM credits.

The evaluator (`internal/jobs/evaluate.go:80-90`) already has a guard for zero messages — it marks `evaluation_failed` and returns. But the damage is already partially done: the session is `completed` in the user's history instead of `cancelled`, and the worker round-trips through the job queue unnecessarily.

### Fix

1. **Change cleanup worker to cancel, not complete.** Modify the existing `MarkAbandonedSessionsCompleted` SQL query to set `status = 'cancelled'` instead of `completed`, and rename it to `MarkAbandonedSessionsCancelled`. This query is only called from `cleanup.go`, so the rename is safe. After `sqlc generate`, update the Go call site. Do not enqueue evaluation for cancelled sessions.

2. **Refund minutes.** The cleanup worker already calls `RefundSessionMinutes` — this stays. Cancelled sessions still get refunds.

3. **Do not auto-archive.** Issue #21 suggests auto-archiving, but the `sessions` table has no `archived` column (archiving is frontend-only via `ListSessions` query params). Skip this — out of scope.

4. **Keep evaluator guard.** The zero-message guard in `evaluate.go:80-90` remains as defense-in-depth for sessions that reach the evaluator through other paths.

### Files

- Modify: `sql/queries/sessions.sql` — add `MarkAbandonedSessionsCancelled` query (or rename existing)
- Modify: `internal/db/sessions.sql.go` — regenerate via `sqlc generate`
- Modify: `internal/jobs/cleanup.go` — use cancel query, skip evaluation enqueue
- Create: `internal/jobs/cleanup_test.go` — test that cleanup cancels (not completes) and does not enqueue evaluation
- Modify: `internal/db/querier.go` — regenerated

### Testing

- **Red:** Test that after cleanup runs on an abandoned session with zero messages, session status is `cancelled` (not `completed`) and no evaluation job is enqueued.
- **Green:** Implement the fix.
- Verify existing `TestCancelSession_*` tests still pass.

---

## Track B — #19: Fix pre-existing test failures

**Branch:** `fix/preexisting-test-failures`
**PR:** Closes #19

### Problem

Two billing integration tests fail consistently:
- `TestReserveMinutes_ZeroGrants` (`internal/backend/billing_test.go:326`) — creates a user with no grants, expects `ErrInsufficientBalance`
- `TestGetUsage_EmptyBalance` (`internal/handler/billing_test.go:19`) — hits `GET /api/me/usage` with no grants, expects HTTP 200 with `total_balance: 0`

### Approach

These are investigation tasks. The subagent must:
1. Run each test in isolation to capture the exact failure output
2. Read the test code and the functions under test
3. Diagnose the root cause (likely a missing DB seed, a query that returns an error on zero rows, or a billing snapshot function that doesn't handle the no-grants case)
4. Fix the root cause in the backend/handler code (not the tests, unless the tests have wrong assertions)

### Files

- Investigate: `internal/backend/billing_test.go`, `internal/backend/billing.go`
- Investigate: `internal/handler/billing_test.go`, `internal/handler/billing.go`
- Investigate: relevant sqlc queries in `internal/db/`
- Fix: whichever source file has the bug (backend method or handler)

### Testing

- **Red:** Confirm both tests fail with `go test -run TestReserveMinutes_ZeroGrants ./internal/backend/` and `go test -run TestGetUsage_EmptyBalance ./internal/handler/`
- **Green:** Fix the root cause, confirm both tests pass
- Run full `go test ./internal/backend/... ./internal/handler/...` to verify no regressions

---

## Track C — #43: Dockerfile Go version mismatch

**Branch:** `fix/dockerfile-go-version`
**PR:** Closes #43

### Problem

`Dockerfile` line 10: `FROM golang:1.24-alpine`
`go.mod` line 3: `go 1.25.4`
`.github/workflows/ci.yml` line 20: `go-version: "1.25.x"`

Production Docker builds use Go 1.24; local dev and CI use Go 1.25.

### Fix

1. Update `Dockerfile` line 10 to `FROM golang:1.25-alpine AS backend`
2. Add a CI step that extracts Go versions from all three files and asserts the major.minor match, preventing future drift:
   ```yaml
   - name: Check Go version consistency
     run: |
       MOD=$(grep '^go ' go.mod | awk '{print $2}' | cut -d. -f1,2)
       DOCKER=$(grep 'FROM golang:' Dockerfile | head -1 | sed 's/.*golang:\([0-9]*\.[0-9]*\).*/\1/')
       if [ "$MOD" != "$DOCKER" ]; then
         echo "::error::go.mod ($MOD) != Dockerfile ($DOCKER)"
         exit 1
       fi
   ```

### Files

- Modify: `Dockerfile` — update Go version
- Modify: `.github/workflows/ci.yml` — add version consistency check step

### Testing

- Build Docker image locally: `docker build -t drill-test .`
- Verify the CI check passes with matching versions

---

## Track D — #8: Security response headers

**Branch:** `feat/security-headers`
**PR:** Closes #8

### Problem

`internal/handler/routes.go:NewHandler()` sets up CSRF and OTel middleware but no security hardening headers. Missing: HSTS, CSP, X-Content-Type-Options, X-Frame-Options.

### Fix

Add a `securityHeaders` middleware function in `internal/handler/routes.go` (or a new `internal/handler/security.go` file) that sets headers on every response. Apply it in the `NewHandler` chain.

Headers:
- `X-Content-Type-Options: nosniff` — always
- `X-Frame-Options: DENY` — always
- `Referrer-Policy: strict-origin-when-cross-origin` — always
- `Strict-Transport-Security: max-age=63072000; includeSubDomains` — only when `secureCookies` is true (production/HTTPS)
- `Content-Security-Policy` — `default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self' wss:; font-src 'self'; frame-ancestors 'none'` — this is a starting CSP. The `'unsafe-inline'` for styles is needed because Tailwind/shadcn injects inline styles. The `wss:` in `connect-src` is required for WebSocket interview connections. The agent should verify the CSP doesn't break the app by loading it in the browser.

### Middleware placement

In `NewHandler()`, wrap the outermost handler so headers are set on ALL responses (including CSRF token responses, webhooks, SPA assets):

```go
return securityHeaders(secureCookies, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    // existing CSRF exemption + routing logic
}))
```

### Files

- Create or modify: `internal/handler/security.go` — `securityHeaders` middleware function
- Modify: `internal/handler/routes.go` — apply middleware in `NewHandler()`
- Create: `internal/handler/security_test.go` — test that headers are present on responses

### Testing

- **Red:** Test that a GET /api/health response includes `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, etc.
- **Green:** Implement middleware, verify tests pass
- **Browser verification:** Load the app and check response headers in DevTools Network tab

---

## Track E — #34 + #35: Error Boundary and 404 route

**Branch:** `feat/error-boundary-and-404`
**PR:** Closes #34, closes #35
**Sequencing:** #34 first (ErrorBoundary), then #35 (404 route). Both touch `web/src/app.tsx` and `web/src/main.tsx`.

### #34: React Error Boundary

**Problem:** No `<ErrorBoundary>` in the component tree. Any render error shows a blank white screen.

**Fix:**
1. Install `react-error-boundary` package (`npm install react-error-boundary` in `web/`)
2. Create `web/src/components/error-fallback.tsx` — a minimal "Something went wrong" UI with a "Reload page" button
3. Wrap the app in `web/src/main.tsx`:
   ```tsx
   <ErrorBoundary FallbackComponent={ErrorFallback}>
     <QueryClientProvider ...>
       <BrowserRouter>
         <App />
       </BrowserRouter>
     </QueryClientProvider>
   </ErrorBoundary>
   ```
4. Optionally add a second boundary inside `<App>` around the `<Routes>` block for route-level isolation

**Testing:**
- **Red:** Vitest test renders a component that throws inside the ErrorBoundary, asserts fallback UI appears
- **Green:** Implement ErrorBoundary and fallback component

### #35: 404 catch-all route

**Problem:** `web/src/app.tsx` defines routes but no `<Route path="*">`. Unknown URLs render blank content inside AppLayout.

**Fix:**
1. Create `web/src/pages/not-found.tsx` — minimal page with "Page not found" heading and a link to `/`
2. Add catch-all route at the end of the AppLayout route group in `web/src/app.tsx`:
   ```tsx
   <Route element={<AppLayout />}>
     {/* ...existing routes... */}
     <Route path="*" element={<NotFound />} />
   </Route>
   ```
3. Also add a top-level catch-all after all route groups for URLs that don't match any layout

**Testing:**
- **Red:** Vitest + MemoryRouter test navigates to `/nonexistent`, asserts "Page not found" text is visible and a link to `/` exists
- **Green:** Implement NotFound component and route

### Files

- Create: `web/src/components/error-fallback.tsx`
- Create: `web/src/pages/not-found.tsx`
- Modify: `web/src/main.tsx` — add ErrorBoundary
- Modify: `web/src/app.tsx` — add catch-all route
- Create: `web/src/__tests__/error-boundary.test.tsx`
- Create: `web/src/__tests__/not-found.test.tsx`
- Modify: `web/package.json` — add `react-error-boundary` dependency

---

## Track F — #24 + #40: CI frontend build and tests

**Branch:** `fix/ci-frontend-build-and-tests`
**PR:** Closes #24, closes #40
**Sequencing:** #24 first (frontend build before Go compile), then #40 (add frontend test/lint steps). Both modify `.github/workflows/ci.yml`.

### #24: Build web/dist before Go compile

**Problem:** CI runs `go test ./...` without first building `web/dist`. The `//go:embed web/dist` directive in `web.go` fails because the directory doesn't exist.

**Fix:** Add Node.js setup and frontend build steps before the Go test step:

```yaml
- name: Setup Node
  uses: actions/setup-node@v4
  with:
    node-version: "22"
    cache: "npm"
    cache-dependency-path: web/package-lock.json

- name: Install frontend dependencies
  run: npm ci
  working-directory: web

- name: Build frontend
  run: npm run build
  working-directory: web
```

These steps must come before `go vet`, `go test`, etc.

### #40: Run frontend tests in CI

**Problem:** 3 frontend test files (recorder.test.ts, client.test.ts, connection.test.ts) are never executed in CI.

**Fix:** After the frontend build steps, add test and lint steps:

```yaml
- name: Frontend tests
  run: npm run test
  working-directory: web

- name: Frontend lint
  run: npm run lint
  working-directory: web
```

### Files

- Modify: `.github/workflows/ci.yml` — add Node setup, frontend build, test, lint steps

### Testing

- Verify locally: `cd web && npm ci && npm run build && npm run test && npm run lint`
- Push to a PR and confirm CI passes with the new steps

---

## Execution Model

### Subagent dispatch

Each track runs as one subagent in an isolated git worktree branched from `main`. Tracks E and F are internally sequential (the agent handles both issues in order on the same branch).

| Track | Subagent count | Worktree branch |
|-------|---------------|-----------------|
| A | 1 impl + 1 review | `fix/empty-session-eval` |
| B | 1 impl + 1 review | `fix/preexisting-test-failures` |
| C | 1 impl + 1 review | `fix/dockerfile-go-version` |
| D | 1 impl + 1 review | `feat/security-headers` |
| E | 1 impl + 1 review | `feat/error-boundary-and-404` |
| F | 1 impl + 1 review | `fix/ci-frontend-build-and-tests` |

### Review gate

Per CLAUDE.md: after each impl agent completes, a separate review agent reads the actual files in the worktree and verifies against the task spec. The review agent checks:
1. All spec requirements are met
2. Tests exist and pass (agent runs them)
3. No unintended changes to files outside the spec's scope
4. Code follows project conventions (DI, no globals, methods on types)

### PR creation

After review passes, the impl agent (or a follow-up step):
1. Commits all changes with a descriptive message
2. Pushes the branch
3. Creates a PR via `gh pr create` referencing the issue(s) with `Closes #N`

### Merge order

No inter-track dependencies. PRs can be merged in any order. Two potential merge conflicts, both trivially resolvable:
1. Track C and Track F both touch `.github/workflows/ci.yml` (C adds a version check step, F restructures the workflow to add Node/frontend steps)
2. Track E modifies `web/src/main.tsx` (adds ErrorBoundary), which the connectRPC migration will later modify (adds TransportProvider) — but the connectRPC work hasn't started yet, so no conflict in this round
