# Issue Burndown Round 2 — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix 10 issues across 9 independent tracks, each producing a GitHub PR, without conflicting with the in-flight connectRPC migration or Round 1 burndown.

**Architecture:** 9 independent tasks, each self-contained in an isolated git worktree branched from `main`. All tasks are fully parallel. Each task ends with a PR via `gh pr create`.

**Tech Stack:** Go 1.25, sqlc, pgxpool, River (job queue), React 19, TanStack Query v5, shadcn/ui, sonner, vitest, GitHub Actions, golangci-lint

**Spec:** `docs/superpowers/specs/2026-04-05-issue-burndown-round2-design.md`

---

## File Structure

**Task A (Track A — #7 + #33):**
- Create: `internal/ratelimit/limiter.go`
- Create: `internal/ratelimit/limiter_test.go`
- Modify: `internal/config/config.go`
- Modify: `internal/handler/routes.go`
- Modify: `go.mod`, `go.sum`

**Task B (Track B — #41):**
- Modify: `web/package.json`, `web/package-lock.json`
- Modify: `web/src/main.tsx`
- Modify: `web/src/pages/settings.tsx`
- Modify: `web/src/pages/history.tsx`
- Modify: `web/src/pages/session-config.tsx`
- Modify: `web/src/pages/home.tsx`

**Task C (Track C — #44):**
- Create: `.golangci.yml`
- Modify: `.github/workflows/ci.yml`
- Modify: `Makefile`

**Task D (Track D — #12):**
- Create or modify: `internal/jobs/coach_test.go`
- Create or modify: `internal/jobs/educator_test.go`

**Task E (Track E — #22):**
- Modify: `web/src/api/types.ts`
- Modify: `web/src/pages/session/layout.tsx`
- Modify: `web/src/pages/session/transcript.tsx`
- Modify: `web/src/pages/session/deep-dive.tsx`
- Modify: `web/src/pages/session/overview.tsx`
- Modify: `web/src/pages/history.tsx`

**Task F (Track F — #16):**
- Modify: `web/src/pages/home.tsx`

**Task G (Track G — #36):**
- Modify: `internal/auth/middleware.go`
- Create: `internal/auth/middleware_test.go`
- Modify: `internal/handler/routes.go`

**Task H (Track H — #4):**
- Create: `sql/migrations/007_rls.up.sql`
- Create: `sql/migrations/007_rls.down.sql`
- Create: `internal/rls/middleware.go`
- Create: `internal/rls/middleware_test.go`
- Modify: `internal/config/config.go`
- Modify: `cmd/drill/main.go`
- Modify: `internal/backend/*.go`
- Modify: `internal/handler/testutil_test.go`

**Task I (Track I — #17):**
- Create: `scripts/remove-orphan-schema-migrations.sh`

---

### Task A: Rate Limiting (#7 + #33)

**Branch:** `feat/rate-limiting`
**PR:** Closes #7, closes #33

**Files:**
- Create: `internal/ratelimit/limiter.go`
- Create: `internal/ratelimit/limiter_test.go`
- Modify: `internal/config/config.go`
- Modify: `internal/handler/routes.go`
- Modify: `go.mod`, `go.sum`

**Context for the implementer:**

No rate limiting exists in this codebase. Auth endpoints (`POST /api/auth/login`, `/signup`, `/forgot-password`, `/reset-password`) are wide open. WebSocket, billing, and LLM endpoints have no per-user throttle.

Create a new `internal/ratelimit/` package with a `Limiter` type backed by `golang.org/x/time/rate` token buckets. Each unique key (IP or user ID) gets its own bucket. An LRU map with background cleanup prevents unbounded memory growth. The `Limiter` follows the project's DI and explicit lifecycle patterns.

The `golang.org/x/time` module is already an indirect dependency in `go.mod`. Using `rate.Limiter` from it will promote it to a direct dependency.

Read `internal/config/config.go` to see how environment-backed config structs are defined (pattern: struct with `env:` tags, loaded via `go-envconfig`). Read `internal/handler/routes.go` to see how middleware is applied to routes.

- [ ] **Step 1: Write failing tests for `ClientIP`**

Create `internal/ratelimit/limiter_test.go`:

```go
package ratelimit

import (
	"net/http"
	"testing"
)

func TestClientIP_XForwardedFor(t *testing.T) {
	r, _ := http.NewRequest("GET", "/", nil)
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 10.0.0.1")
	r.RemoteAddr = "127.0.0.1:9999"
	if got := ClientIP(r); got != "1.2.3.4" {
		t.Errorf("ClientIP = %q, want 1.2.3.4", got)
	}
}

func TestClientIP_FallbackRemoteAddr(t *testing.T) {
	r, _ := http.NewRequest("GET", "/", nil)
	r.RemoteAddr = "192.168.1.1:8080"
	if got := ClientIP(r); got != "192.168.1.1" {
		t.Errorf("ClientIP = %q, want 192.168.1.1", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/ratelimit/ -run TestClientIP -v`
Expected: FAIL — package does not exist yet.

- [ ] **Step 3: Implement `ClientIP` and the `Limiter` type**

Create `internal/ratelimit/limiter.go`:

```go
package ratelimit

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Config holds rate limiter settings. Populated from environment via config package.
type Config struct {
	Rate       float64       // tokens per second
	Burst      int           // max burst size
	MaxEntries int           // LRU cache size
	CleanupAge time.Duration // evict entries older than this
}

type entry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// Limiter provides per-key rate limiting with LRU eviction.
type Limiter struct {
	mu      sync.Mutex
	entries map[string]*entry
	config  Config
	done    chan struct{}
}

// NewLimiter creates a rate limiter and starts its background cleanup goroutine.
func NewLimiter(cfg Config) *Limiter {
	l := &Limiter{
		entries: make(map[string]*entry),
		config:  cfg,
		done:    make(chan struct{}),
	}
	go l.cleanup()
	return l
}

// Allow reports whether the given key is within its rate limit.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	e, ok := l.entries[key]
	if !ok {
		if len(l.entries) >= l.config.MaxEntries {
			l.evictOldest()
		}
		e = &entry{limiter: rate.NewLimiter(rate.Limit(l.config.Rate), l.config.Burst)}
		l.entries[key] = e
	}
	e.lastSeen = time.Now()
	l.mu.Unlock()
	return e.limiter.Allow()
}

// Middleware returns HTTP middleware that rate-limits by client IP.
// Returns 429 with Retry-After header when the limit is exceeded.
func (l *Limiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.Allow(ClientIP(r)) {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// MiddlewareByKey returns HTTP middleware that rate-limits by a caller-provided key.
func (l *Limiter) MiddlewareByKey(keyFunc func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := keyFunc(r)
			if key == "" {
				next.ServeHTTP(w, r)
				return
			}
			if !l.Allow(key) {
				w.Header().Set("Retry-After", "1")
				http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Close stops the background cleanup goroutine.
func (l *Limiter) Close() {
	close(l.done)
}

// ClientIP extracts the client IP from the request. Uses the leftmost
// IP in X-Forwarded-For (the original client) or falls back to RemoteAddr.
func ClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		ip := strings.TrimSpace(strings.SplitN(xff, ",", 2)[0])
		if ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (l *Limiter) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-l.done:
			return
		case <-ticker.C:
			l.mu.Lock()
			cutoff := time.Now().Add(-l.config.CleanupAge)
			for k, e := range l.entries {
				if e.lastSeen.Before(cutoff) {
					delete(l.entries, k)
				}
			}
			l.mu.Unlock()
		}
	}
}

func (l *Limiter) evictOldest() {
	var oldestKey string
	var oldestTime time.Time
	for k, e := range l.entries {
		if oldestKey == "" || e.lastSeen.Before(oldestTime) {
			oldestKey = k
			oldestTime = e.lastSeen
		}
	}
	if oldestKey != "" {
		delete(l.entries, oldestKey)
	}
}
```

- [ ] **Step 4: Run `ClientIP` tests to verify they pass**

Run: `go test ./internal/ratelimit/ -run TestClientIP -v`
Expected: PASS

- [ ] **Step 5: Write tests for `Allow` and `Middleware`**

Add to `internal/ratelimit/limiter_test.go`:

```go
func TestAllow_UnderLimit(t *testing.T) {
	l := NewLimiter(Config{Rate: 10, Burst: 10, MaxEntries: 100, CleanupAge: time.Minute})
	defer l.Close()
	if !l.Allow("key1") {
		t.Error("expected Allow to return true under limit")
	}
}

func TestAllow_OverLimit(t *testing.T) {
	l := NewLimiter(Config{Rate: 1, Burst: 2, MaxEntries: 100, CleanupAge: time.Minute})
	defer l.Close()
	l.Allow("key1") // 1
	l.Allow("key1") // 2 (burst)
	if l.Allow("key1") {
		t.Error("expected Allow to return false over burst limit")
	}
}

func TestAllow_EvictsOldest(t *testing.T) {
	l := NewLimiter(Config{Rate: 10, Burst: 10, MaxEntries: 2, CleanupAge: time.Minute})
	defer l.Close()
	l.Allow("a")
	l.Allow("b")
	l.Allow("c") // should evict "a"
	l.mu.Lock()
	_, hasA := l.entries["a"]
	_, hasC := l.entries["c"]
	l.mu.Unlock()
	if hasA {
		t.Error("expected 'a' to be evicted")
	}
	if !hasC {
		t.Error("expected 'c' to exist")
	}
}

func TestMiddleware_Returns429(t *testing.T) {
	l := NewLimiter(Config{Rate: 1, Burst: 1, MaxEntries: 100, CleanupAge: time.Minute})
	defer l.Close()

	handler := l.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First request passes
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", nil)
	req.RemoteAddr = "1.2.3.4:1234"
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("first request: got %d, want 200", rec.Code)
	}

	// Second request (over burst) returns 429
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("second request: got %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After header")
	}
}
```

Add `"net/http/httptest"` to the test imports.

- [ ] **Step 6: Run all ratelimit tests**

Run: `go test ./internal/ratelimit/ -v`
Expected: PASS

- [ ] **Step 7: Add `RateLimit` config struct**

In `internal/config/config.go`, add a new struct after `Stripe` and add it to `Config`:

```go
type RateLimit struct {
	AuthRate       float64 `env:"RATE_LIMIT_AUTH_RATE,default=5"`
	AuthBurst      int     `env:"RATE_LIMIT_AUTH_BURST,default=10"`
	UserRate       float64 `env:"RATE_LIMIT_USER_RATE,default=2"`
	UserBurst      int     `env:"RATE_LIMIT_USER_BURST,default=5"`
	MaxEntries     int     `env:"RATE_LIMIT_MAX_ENTRIES,default=100000"`
	CleanupAgeSec  int     `env:"RATE_LIMIT_CLEANUP_AGE_SEC,default=600"`
}
```

Add `RateLimit RateLimit` to the `Config` struct (after `Stripe Stripe`).

- [ ] **Step 8: Wire rate limiters into routes**

In `internal/handler/routes.go`, import `"github.com/btc/drill/internal/ratelimit"` and `"github.com/btc/drill/internal/auth"`.

The `RegisterRoutes` function currently accepts `(mux *http.ServeMux, b *backend.Backend)`. The rate limiter instances need to come from the caller. Add a `Deps` struct or pass the limiters directly. The simplest approach: create the limiters in `RegisterRoutes` using `b.Config().RateLimit` (the `Backend` already holds config).

Read `internal/backend/backend.go` to find how to access config from the Backend. Then wrap the auth endpoint registrations:

```go
// Auth — rate-limited by client IP
authLimiter := ratelimit.NewLimiter(ratelimit.Config{
	Rate:       b.Cfg.RateLimit.AuthRate,
	Burst:      b.Cfg.RateLimit.AuthBurst,
	MaxEntries: b.Cfg.RateLimit.MaxEntries,
	CleanupAge: time.Duration(b.Cfg.RateLimit.CleanupAgeSec) * time.Second,
})
authRL := authLimiter.Middleware

mux.Handle("POST /api/auth/signup", authRL(http.HandlerFunc(Signup(b))))
mux.Handle("POST /api/auth/login", authRL(http.HandlerFunc(Login(b))))
mux.Handle("POST /api/auth/forgot-password", authRL(http.HandlerFunc(ForgotPassword(b))))
mux.Handle("POST /api/auth/reset-password", authRL(http.HandlerFunc(ResetPassword(b))))
```

For Phase 2 (user-keyed), read the authenticated user from context:

```go
userLimiter := ratelimit.NewLimiter(ratelimit.Config{
	Rate:       b.Cfg.RateLimit.UserRate,
	Burst:      b.Cfg.RateLimit.UserBurst,
	MaxEntries: b.Cfg.RateLimit.MaxEntries,
	CleanupAge: time.Duration(b.Cfg.RateLimit.CleanupAgeSec) * time.Second,
})
userRL := userLimiter.MiddlewareByKey(func(r *http.Request) string {
	if u := auth.UserFromContext(r.Context()); u != nil {
		return u.ID.String()
	}
	return ""
})
```

Wrap the expensive authenticated endpoints:

```go
mux.Handle("GET /api/sessions/{id}/ws", requireAuth(userRL(http.HandlerFunc(SessionWS(b)))))
mux.Handle("POST /api/billing/checkout", requireAuth(userRL(http.HandlerFunc(PostCheckout(b)))))
mux.Handle("POST /api/coach/analyze", requireAuth(userRL(http.HandlerFunc(RequestCoachAnalysis(b)))))
mux.Handle("POST /api/sessions/{id}/educator", requireAuth(userRL(http.HandlerFunc(RequestEducatorAnalysis(b)))))
```

**Important:** `RegisterRoutes` currently returns `error`. You need to also return a cleanup function, or store the limiters so `Close()` can be called on shutdown. The simplest approach: return the limiters from `RegisterRoutes` so the caller can `defer limiter.Close()`. Or, modify the function signature to accept limiters created externally.

Read `internal/backend/backend.go` to see the Backend struct and how `Cfg` is accessed — the `Backend` likely has a `Cfg` field of type `*config.Config`. Check and adjust the field path accordingly.

**Note on routes.go conflicts:** This file is modified by Round 1 Track D (security headers) and ConnectRPC. The changes here are to individual route registrations, not the `NewHandler` wrapper. Conflicts will be in different regions of the file and resolvable with a simple rebase.

- [ ] **Step 9: Run full test suite**

Run: `go test ./internal/ratelimit/ ./internal/handler/ -v -count=1 -timeout=300s`
Expected: PASS

- [ ] **Step 10: Commit and create PR**

```bash
git add internal/ratelimit/ internal/config/config.go internal/handler/routes.go go.mod go.sum
git commit -m "feat: add rate limiting to auth, WS, billing, and LLM endpoints

Closes #7, closes #33"
gh pr create --title "feat: rate limiting for auth and expensive endpoints" --body "$(cat <<'EOF'
## Summary
- New `internal/ratelimit/` package with per-key token bucket rate limiting
- Auth endpoints rate-limited by client IP (configurable via env vars)
- WebSocket, billing, coach, educator endpoints rate-limited by user ID
- Background LRU cleanup, explicit lifecycle via Close()

## Test plan
- [ ] Unit tests for Allow, Middleware, ClientIP, LRU eviction
- [ ] Verify 429 + Retry-After returned when limit exceeded
- [ ] Verify rate limit config loads from environment

Closes #7, closes #33

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

---

### Task B: Toast/Notification System (#41)

**Branch:** `feat/toast-notifications`
**PR:** Closes #41

**Files:**
- Modify: `web/package.json`
- Modify: `web/src/main.tsx`
- Modify: `web/src/pages/settings.tsx`
- Modify: `web/src/pages/history.tsx`
- Modify: `web/src/pages/session-config.tsx`
- Modify: `web/src/pages/home.tsx`

**Context for the implementer:**

The app has no global notification system. Errors are shown inline near triggers, inconsistently. Success operations (save profile, archive sessions) give no feedback. You'll add [sonner](https://sonner.emilkowal.dev/) — the standard toast library for shadcn/ui.

Read `web/src/main.tsx` for the app's root component tree. Read the pages listed above to find mutation `onSuccess`/`onError` handlers where toasts should fire.

The frontend uses ConnectRPC hooks (`@connectrpc/connect-query`) for some endpoints and raw `fetch` via `apiClient` for others. Look at each mutation to determine which pattern is used and add toast calls to the existing `onSuccess`/`onError` callbacks.

- [ ] **Step 1: Install sonner**

```bash
cd web && npm install sonner
```

- [ ] **Step 2: Add `<Toaster />` to app root**

In `web/src/main.tsx`, add:

```tsx
import { Toaster } from "sonner";
```

Then add `<Toaster theme="dark" position="bottom-right" />` inside `<StrictMode>`, after `<App />`:

```tsx
<StrictMode>
  <QueryClientProvider client={queryClient}>
    <TransportProvider transport={transport}>
      <BrowserRouter>
        <App />
      </BrowserRouter>
    </TransportProvider>
  </QueryClientProvider>
  <Toaster theme="dark" position="bottom-right" />
</StrictMode>
```

- [ ] **Step 3: Add toasts to settings page**

Read `web/src/pages/settings.tsx`. Find the profile save mutation and add:

```tsx
import { toast } from "sonner";
```

In the `onSuccess` callback of the profile save mutation, add `toast.success("Profile saved")`. In the `onError` callback, replace inline error state with `toast.error("Save failed")`.

For the data export action, add `toast.success("Export requested — check your email")` on success.

For the delete account dialog, add `toast.error(msg)` on error (replacing inline error state).

- [ ] **Step 4: Add toasts to history page**

Read `web/src/pages/history.tsx`. Find the archive mutation. In `onSuccess`, add:

```tsx
toast.success(`${count} session${count === 1 ? "" : "s"} archived`);
```

Replace any inline success/error state with toast calls.

- [ ] **Step 5: Add toasts to session config page**

Read `web/src/pages/session-config.tsx`. Find the create session mutation. In `onError`, add:

```tsx
toast.error("Failed to start session");
```

- [ ] **Step 6: Add toast to home page coach analysis**

Read `web/src/pages/home.tsx`. Find the coach analysis refresh mutation. In `onSuccess`, add:

```tsx
toast.success("Coach analysis updated");
```

- [ ] **Step 7: Verify in browser**

Run: `make dev`
Test: trigger a profile save, archive, and coach refresh. Verify dark toasts appear at bottom-right and auto-dismiss.

- [ ] **Step 8: Commit and create PR**

```bash
git add web/
git commit -m "feat: add global toast notification system via sonner

Closes #41"
gh pr create --title "feat: global toast notifications" --body "$(cat <<'EOF'
## Summary
- Add sonner for toast notifications
- Profile save, data export, archive, session creation, coach refresh all show toasts
- Dark theme, bottom-right position

## Test plan
- [ ] Save profile → "Profile saved" toast
- [ ] Archive sessions → "N sessions archived" toast
- [ ] Coach refresh → "Coach analysis updated" toast
- [ ] Session creation error → error toast

Closes #41

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

---

### Task C: Replace go vet + staticcheck with golangci-lint (#44)

**Branch:** `feat/golangci-lint`
**PR:** Closes #44

**Files:**
- Create: `.golangci.yml`
- Modify: `.github/workflows/ci.yml`
- Modify: `Makefile`

**Context for the implementer:**

Current CI runs `go vet ./...` and `staticcheck ./...` separately (see `.github/workflows/ci.yml`). golangci-lint subsumes both and adds useful linters. Use `--new-from-rev=main` in CI to only lint changed code, preventing this PR from turning into a codebase-wide cleanup.

- [ ] **Step 1: Create `.golangci.yml`**

```yaml
run:
  timeout: 5m

linters:
  enable:
    - govet
    - staticcheck
    - errcheck
    - ineffassign
    - unused
    - gocritic
    - gosimple
    - typecheck
    - misspell
    - revive

linters-settings:
  errcheck:
    check-blank: false
  gocritic:
    enabled-tags:
      - diagnostic
      - performance
  revive:
    rules:
      - name: exported
        disabled: true

issues:
  exclude-dirs:
    - internal/pb
  exclude-files:
    - ".*\\.sql\\.go$"
```

- [ ] **Step 2: Install golangci-lint locally and run**

```bash
brew install golangci-lint
golangci-lint run ./...
```

Review output. For any pre-existing violations in files this PR doesn't change, note them but do NOT fix them (that's a separate PR). If there are violations in files this PR does change, add targeted `//nolint` directives.

- [ ] **Step 3: Update CI workflow**

In `.github/workflows/ci.yml`, remove the "Install tools" step's `staticcheck` line, the "Vet" step, and the "Staticcheck" step. Replace with:

```yaml
      - name: Lint
        uses: golangci/golangci-lint-action@v7
        with:
          version: latest
          args: --new-from-rev=origin/main
```

Keep the `sqlc` install in the "Install tools" step.

- [ ] **Step 4: Add `make lint` target**

In `Makefile`, add after the existing `generate` target:

```makefile
# Run golangci-lint (same config as CI).
lint:
	golangci-lint run ./...
```

Update the `.PHONY` line to include `lint`.

- [ ] **Step 5: Run `make lint` locally**

Run: `make lint`
Expected: Passes (or only pre-existing violations outside this PR's scope).

- [ ] **Step 6: Commit and create PR**

```bash
git add .golangci.yml .github/workflows/ci.yml Makefile
git commit -m "feat: replace go vet + staticcheck with golangci-lint

Closes #44"
gh pr create --title "feat: golangci-lint replaces go vet + staticcheck" --body "$(cat <<'EOF'
## Summary
- Add `.golangci.yml` with curated linter set (govet, staticcheck, errcheck, gocritic, revive, etc.)
- CI uses `golangci-lint-action@v7` with `--new-from-rev` to only lint new code
- Add `make lint` target for local development
- Generated files (internal/pb/, *.sql.go) excluded

## Test plan
- [ ] `make lint` passes locally
- [ ] CI lint step passes on this PR
- [ ] Verify staticcheck and go vet steps are removed from CI

Closes #44

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

---

### Task D: Test Coverage for Job Workers (#12)

**Branch:** `test/job-worker-coverage`
**PR:** Closes #12

**Files:**
- Create or modify: `internal/jobs/coach_test.go`
- Create or modify: `internal/jobs/educator_test.go`

**Context for the implementer:**

The coach and educator job workers (`internal/jobs/coach.go` and `internal/jobs/educator.go`) orchestrate LLM calls, parse tool-use responses, and write structured data to the DB. They're at 0% test coverage. Both workers have a `Work()` method that:

1. Loads data from DB
2. Builds a prompt
3. Calls the Anthropic API
4. Parses a tool-use response
5. Writes results to DB

The pattern for testing LLM-dependent code already exists: `internal/handler/session_ws_test.go` uses `newFakeAnthropicServer()` to create a test HTTP server that returns canned SSE responses. The `ai.NewTestClient(baseURL, pool)` function in `internal/ai/client.go` creates an `ai.Client` pointed at that fake server.

Read `internal/jobs/coach.go` and `internal/jobs/educator.go` fully to understand the `Work()` method, what data it needs seeded, and what tool-use response schema it expects.

Read `internal/handler/session_ws_test.go` to find `newFakeAnthropicServer` and understand how the fake Anthropic SSE server is built — it returns a canned `content_block_start` + `content_block_delta` + `content_block_stop` + `message_stop` SSE stream with a tool-use block.

Read `internal/backend/evaluation_test.go` for test helpers: `seedUser`, `seedQuestion`, `seedSession`, `seedEval`, `seedMsg`, `setStatus`, `seedPurchaseGrant`, `seedReviewedSession`.

Read `internal/coach/schema.go` to see the expected tool-use response schema for the coach worker.
Read `internal/educator/schema.go` to see the expected tool-use response schema for the educator worker.

**Important:** The worker structs have fields `Pool *pgxpool.Pool`, `LLM *ai.Client`, and `Cfg *config.LLM`. Create test instances with the test pool and fake-server-backed client.

- [ ] **Step 1: Write the coach worker test**

Create or add to `internal/jobs/coach_test.go`. You need:
1. A running Postgres test container (use the same `startPostgres` / `newTestBackend` helpers from `internal/handler/testutil_test.go`, or replicate the pattern if in a different package)
2. Seed data: user with paid balance, 3 reviewed sessions with evaluations
3. A fake Anthropic server returning a tool-use response matching the coach schema
4. A `RunCoachAnalysisWorker` instance with the test pool and fake client

```go
func TestRunCoachAnalysisWorker_Work(t *testing.T) {
    // 1. Start test DB, seed user + reviewed sessions + evaluations
    // 2. Start fake Anthropic server returning coach tool-use response
    // 3. Create worker with test pool and fake client
    // 4. Build a river.Job[RunCoachAnalysisArgs] and call Work()
    // 5. Assert: coach_analysis row exists with expected fields
    // 6. Assert: generated question exists if response included one
}
```

The fake server must return a valid SSE stream with a `tool_use` content block whose `input` matches the coach tool schema. Read `internal/coach/schema.go` for the exact field names.

For building a `river.Job[T]` in tests, use `river.Job[RunCoachAnalysisArgs]{Args: RunCoachAnalysisArgs{UserID: userID}}`. Check if there are other required fields on the Job struct.

- [ ] **Step 2: Run coach test**

Run: `go test ./internal/jobs/ -run TestRunCoachAnalysisWorker -v -timeout=120s`
Expected: PASS

- [ ] **Step 3: Write the educator worker test**

Add to `internal/jobs/educator_test.go`:

```go
func TestGenerateEducatorContentWorker_Work(t *testing.T) {
    // 1. Start test DB, seed user + reviewed session + evaluation + messages
    // 2. Insert educator_analyses row with status='generating'
    // 3. Start fake Anthropic server returning educator tool-use response
    // 4. Create worker with test pool and fake client
    // 5. Build a river.Job[GenerateEducatorContentArgs] and call Work()
    // 6. Assert: educator_analyses status updated to 'completed'
    // 7. Assert: model_answer and gap_deep_dives populated
}
```

Read `internal/educator/schema.go` for the expected tool-use response schema.

- [ ] **Step 4: Run educator test**

Run: `go test ./internal/jobs/ -run TestGenerateEducatorContentWorker -v -timeout=120s`
Expected: PASS

- [ ] **Step 5: Write error case tests**

Add tests for malformed LLM responses:

```go
func TestRunCoachAnalysisWorker_MalformedResponse(t *testing.T) {
    // Fake server returns tool_use with invalid JSON in input field
    // Assert: Work() returns an error (or marks job for retry)
}

func TestGenerateEducatorContentWorker_EmptyResponse(t *testing.T) {
    // Fake server returns a message with no tool_use blocks
    // Assert: Work() returns an error
}
```

- [ ] **Step 6: Run all job tests**

Run: `go test ./internal/jobs/ -v -timeout=300s`
Expected: PASS

- [ ] **Step 7: Commit and create PR**

```bash
git add internal/jobs/
git commit -m "test: add integration tests for coach and educator job workers

Closes #12"
gh pr create --title "test: coach and educator job worker coverage" --body "$(cat <<'EOF'
## Summary
- Integration tests for RunCoachAnalysisWorker.Work() and GenerateEducatorContentWorker.Work()
- Uses fake Anthropic SSE server pattern from session_ws_test.go
- Covers happy path and malformed/empty LLM response error cases

## Test plan
- [ ] `go test ./internal/jobs/ -v` passes
- [ ] Coverage for jobs/coach.go and jobs/educator.go above 50%

Closes #12

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

---

### Task E: Session Detail Empty/Cancelled/Failed States (#22)

**Branch:** `fix/session-detail-states`
**PR:** Closes #22

**Files:**
- Modify: `web/src/api/types.ts`
- Modify: `web/src/pages/session/layout.tsx`
- Modify: `web/src/pages/session/transcript.tsx`
- Modify: `web/src/pages/session/deep-dive.tsx`
- Modify: `web/src/pages/session/overview.tsx`
- Modify: `web/src/pages/history.tsx`

**Context for the implementer:**

Session detail pages break when a session is cancelled, failed, or has no messages. The `cancelled` status exists in the backend (DB migration 006, `CancelSession` query) but is missing from the frontend `SessionStatus` type.

Read all files listed above. The session layout component (`layout.tsx`) manages tab navigation and status display. Each sub-page (overview, transcript, deep-dive) renders content based on session data.

The existing `EvaluatingView` in `overview.tsx` shows how intermediate states are handled — it displays a spinner with rotating messages. Use similar patterns for empty states.

- [ ] **Step 1: Add `cancelled` to SessionStatus**

In `web/src/api/types.ts`, find the `SessionStatus` type and add `"cancelled"`:

```ts
export type SessionStatus = "active" | "completed" | "evaluating" | "reviewed" | "evaluation_failed" | "failed" | "cancelled";
```

- [ ] **Step 2: Add cancelled state to session layout**

In `web/src/pages/session/layout.tsx`:

- If `session.status === "cancelled"`, render a centered card with:
  - Heading: "Session Cancelled"
  - Body: "This session was cancelled before completion. No evaluation is available."
  - Link: "Back to Home" → `/`
- If `session.status === "active"`, redirect to `/sessions/${id}/interview`
- Disable the "Overview" and "Deep Dive" tabs for non-reviewed sessions (only enable when status is `reviewed` or `evaluation_failed`)

- [ ] **Step 3: Add empty message state to transcript page**

In `web/src/pages/session/transcript.tsx`:

- If the messages array is empty or undefined, render an empty state: a muted icon (e.g., `MessageSquare` from lucide-react) with text "No messages in this session"
- Hide the annotation summary bar when there are zero annotations

- [ ] **Step 4: Add unavailable state to deep dive page**

In `web/src/pages/session/deep-dive.tsx`:

- If no educator analysis data exists (status is `not_requested` or missing), show: "Deep dive not available — request it from the overview page"
- If status is `evaluation_failed`: "Evaluation failed — deep dive requires a completed evaluation"
- If status is `generating`: show a loading state similar to `EvaluatingView`

- [ ] **Step 5: Improve failed state in overview page**

In `web/src/pages/session/overview.tsx`:

- If `evaluation_failed`: ensure the retry button is prominently displayed (verify the existing retry UI works)
- If no evaluation data and status is not `evaluating`: show "Evaluation not available" with status-appropriate explanation

- [ ] **Step 6: Add cancelled StatusBadge to history page**

In `web/src/pages/history.tsx`, find the `StatusBadge` component or status display logic. Add a case for `"cancelled"` rendering a grey badge with "Cancelled" text.

- [ ] **Step 7: Verify in browser**

Run: `make dev`
Test: navigate to session detail pages for sessions with different statuses. If no cancelled session exists, manually cancel one via the interview page.

- [ ] **Step 8: Commit and create PR**

```bash
git add web/src/
git commit -m "fix: handle empty, cancelled, and failed session states in detail pages

Closes #22"
gh pr create --title "fix: session detail empty/cancelled/failed state handling" --body "$(cat <<'EOF'
## Summary
- Add `cancelled` to frontend SessionStatus type
- Session layout shows dedicated cancelled state card
- Transcript page shows empty state when no messages
- Deep dive page shows unavailable state with explanation
- History page shows grey "Cancelled" badge

## Test plan
- [ ] Navigate to cancelled session → shows "Session Cancelled" card
- [ ] Navigate to session with no messages → transcript shows empty state
- [ ] Navigate to failed evaluation session → deep dive shows explanation
- [ ] History page shows "Cancelled" badge for cancelled sessions

Closes #22

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

---

### Task F: Question Card Disabled State UX (#16)

**Branch:** `fix/question-card-disabled-ux`
**PR:** Closes #16

**Files:**
- Modify: `web/src/pages/home.tsx`

**Context for the implementer:**

When a user has an active session and is at the concurrent limit, question cards become silently disabled via `pointer-events-none` and `opacity-60`. No explanation is shown. The active session banner exists but is visually weak.

Read `web/src/pages/home.tsx` fully. Find:
- `activeSessions()` helper that filters active sessions
- `atConcurrentLimit` variable
- The `ActiveSessionBanner` component
- The question card rendering logic (where `pointer-events-none` and `opacity-60` are applied)

The existing shadcn `tooltip` component is at `web/src/components/ui/tooltip.tsx`.

- [ ] **Step 1: Enhance the active session banner**

In the `ActiveSessionBanner` component (or wherever the banner is rendered):
- Change background to `bg-blue-600` with white text for higher visual weight
- Add a pulsing dot indicator (`animate-pulse` on a small circle)
- Add padding and make it sticky within the question list
- Change the "Resume" link to a primary button style
- Add an "End Session" secondary button that calls the cancel session API

- [ ] **Step 2: Replace silent disable with overlay explanation**

In the question card rendering:
- Remove `pointer-events-none` from disabled cards
- Keep `opacity-60` but add a visible overlay with text: "Resume or end your active session to start a new one"
- Add a click handler on disabled cards that scrolls to the active session banner:

```tsx
onClick={() => {
  document.getElementById("active-session-banner")?.scrollIntoView({ behavior: "smooth" });
}}
```

Add `id="active-session-banner"` to the banner element.

- [ ] **Step 3: Add tooltip on disabled cards**

Wrap each disabled question card in the shadcn `Tooltip` component:

```tsx
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip";

<TooltipProvider>
  <Tooltip>
    <TooltipTrigger asChild>
      <div>{/* card content */}</div>
    </TooltipTrigger>
    <TooltipContent>Active session in progress</TooltipContent>
  </Tooltip>
</TooltipProvider>
```

Only wrap in tooltip when `atConcurrentLimit` is true.

- [ ] **Step 4: Verify in browser**

Run: `make dev`
Test: start a session, navigate to home page. Verify:
- Banner is prominent blue with Resume and End Session buttons
- Question cards show overlay text
- Clicking a disabled card scrolls to the banner
- Hovering shows tooltip

- [ ] **Step 5: Commit and create PR**

```bash
git add web/src/pages/home.tsx
git commit -m "fix: replace silent card disable with visible explanation and enhanced banner

Closes #16"
gh pr create --title "fix: question card disabled state UX" --body "$(cat <<'EOF'
## Summary
- Active session banner upgraded: prominent blue, pulsing dot, Resume + End Session buttons
- Disabled question cards show overlay explanation instead of silent disable
- Clicking disabled cards scrolls to active session banner
- Tooltip on hover explains why cards are disabled

## Test plan
- [ ] Start session → home shows prominent blue banner
- [ ] Question cards show overlay text when at limit
- [ ] Click disabled card → scrolls to banner
- [ ] End Session button on banner cancels active session

Closes #16

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

---

### Task G: Admin Role-Based Endpoint Protection (#36)

**Branch:** `feat/admin-role-protection`
**PR:** Closes #36

**Files:**
- Modify: `internal/auth/middleware.go`
- Create: `internal/auth/middleware_test.go`
- Modify: `internal/handler/routes.go`

**Context for the implementer:**

The `users` table has a `role` column (`candidate` or `admin`). The `AuthUser` struct in `internal/auth/middleware.go` includes `Role string`. But there's no middleware that checks it. `GET /admin/jobs` is registered with bare `HandleFunc` — no auth, no role check. Any anonymous request can access it.

Read `internal/auth/middleware.go` to see the `RequireAuth` pattern (returns `func(http.Handler) http.Handler`). The new `RequireAdmin` follows the same shape but checks `user.Role == "admin"`.

Read `internal/handler/routes.go:78` to see the unprotected admin route.

- [ ] **Step 1: Write failing test for RequireAdmin**

Create `internal/auth/middleware_test.go`:

```go
package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestRequireAdmin_NoUser(t *testing.T) {
	handler := RequireAdmin()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/admin/jobs", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("got %d, want 403", rec.Code)
	}
}

func TestRequireAdmin_CandidateRole(t *testing.T) {
	handler := RequireAdmin()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/admin/jobs", nil)
	req = req.WithContext(WithUser(req.Context(), &AuthUser{
		ID:   uuid.New(),
		Role: "candidate",
	}))
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("got %d, want 403", rec.Code)
	}
}

func TestRequireAdmin_AdminRole(t *testing.T) {
	handler := RequireAdmin()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/admin/jobs", nil)
	req = req.WithContext(WithUser(req.Context(), &AuthUser{
		ID:   uuid.New(),
		Role: "admin",
	}))
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("got %d, want 200", rec.Code)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/auth/ -run TestRequireAdmin -v`
Expected: FAIL — `RequireAdmin` not defined.

- [ ] **Step 3: Implement RequireAdmin**

In `internal/auth/middleware.go`, add:

```go
// RequireAdmin returns middleware that checks the authenticated user has
// the admin role. Must be applied after RequireAuth.
func RequireAdmin() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := UserFromContext(r.Context())
			if user == nil || user.Role != "admin" {
				writeAuthError(w, http.StatusForbidden, "admin access required")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/auth/ -run TestRequireAdmin -v`
Expected: PASS (3/3)

- [ ] **Step 5: Wire RequireAdmin into routes**

In `internal/handler/routes.go`, change line 78 from:

```go
mux.HandleFunc("GET /admin/jobs", AdminJobsPlaceholder())
```

to:

```go
requireAdmin := auth.RequireAdmin()
mux.Handle("GET /admin/jobs", requireAuth(requireAdmin(AdminJobsPlaceholder())))
```

Note: `AdminJobsPlaceholder()` returns `http.HandlerFunc`, which implements `http.Handler`, so no additional wrapping needed.

- [ ] **Step 6: Run handler tests**

Run: `go test ./internal/handler/ -v -count=1 -timeout=300s`
Expected: PASS

- [ ] **Step 7: Commit and create PR**

```bash
git add internal/auth/middleware.go internal/auth/middleware_test.go internal/handler/routes.go
git commit -m "feat: add admin role-based endpoint protection

Closes #36"
gh pr create --title "feat: admin role-based endpoint protection" --body "$(cat <<'EOF'
## Summary
- Add `RequireAdmin` middleware in `internal/auth/`
- `GET /admin/jobs` now requires both authentication AND admin role
- Returns 403 for non-admin users and unauthenticated requests

## Test plan
- [ ] Unit: no user → 403
- [ ] Unit: candidate role → 403
- [ ] Unit: admin role → 200
- [ ] Integration: full handler test suite passes

Closes #36

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

---

### Task H: Row-Level Security (#4)

**Branch:** `feat/row-level-security`
**PR:** Closes #4

**Files:**
- Create: `sql/migrations/007_rls.up.sql`
- Create: `sql/migrations/007_rls.down.sql`
- Create: `internal/rls/middleware.go`
- Create: `internal/rls/middleware_test.go`
- Modify: `internal/config/config.go`
- Modify: `cmd/drill/main.go`
- Modify: `internal/backend/*.go`
- Modify: `internal/handler/testutil_test.go`

**Context for the implementer:**

All data isolation is via `WHERE user_id = $1` in queries. RLS adds defense-in-depth at the Postgres layer. This is the most invasive track — it introduces a dedicated `drill_app` DB role, per-request connection acquire with `SET`/`RESET`, and a Backend refactor to pull connections from context.

Read the design spec at `docs/superpowers/specs/2026-04-05-issue-burndown-round2-design.md`, Track H section, for the full architectural rationale.

Read `internal/config/config.go` to see how `Database` config works. Read `cmd/drill/main.go` to see how migrations run and the Backend is created. Read `internal/backend/backend.go` to see how the Backend struct holds the pool and how `db.New(b.pool)` is called.

Read `sql/migrations/` to confirm the latest migration is 006. Verify no other Round 2 track adds a migration. If one does, adjust the number.

**Scope note:** If the Backend refactor proves too large for one PR, ship the migration + middleware + a few key tables, with a follow-up PR for remaining Backend methods. Prioritize `interview_sessions` and `grants` since they're the most security-sensitive.

- [ ] **Step 1: Create the RLS migration**

Create `sql/migrations/007_rls.up.sql`:

```sql
-- Create a restricted role for the application.
-- Migrations continue to run as the owner (superuser).
DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'drill_app') THEN
        CREATE ROLE drill_app LOGIN PASSWORD 'drill_app_password';
    END IF;
END
$$;

GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO drill_app;
GRANT USAGE ON ALL SEQUENCES IN SCHEMA public TO drill_app;

-- Ensure future tables also get grants.
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO drill_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT USAGE ON SEQUENCES TO drill_app;

-- Enable RLS on direct-user_id tables.
ALTER TABLE interview_sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE coach_analyses ENABLE ROW LEVEL SECURITY;
ALTER TABLE grants ENABLE ROW LEVEL SECURITY;
ALTER TABLE ledger_entries ENABLE ROW LEVEL SECURITY;
ALTER TABLE llm_calls ENABLE ROW LEVEL SECURITY;
ALTER TABLE user_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE questions ENABLE ROW LEVEL SECURITY;

-- Policies: user can only see/modify their own rows.
CREATE POLICY user_isolation ON interview_sessions
    USING (user_id = current_setting('app.current_user_id', true)::uuid)
    WITH CHECK (user_id = current_setting('app.current_user_id', true)::uuid);

CREATE POLICY user_isolation ON coach_analyses
    USING (user_id = current_setting('app.current_user_id', true)::uuid)
    WITH CHECK (user_id = current_setting('app.current_user_id', true)::uuid);

CREATE POLICY user_isolation ON grants
    USING (user_id = current_setting('app.current_user_id', true)::uuid)
    WITH CHECK (user_id = current_setting('app.current_user_id', true)::uuid);

CREATE POLICY user_isolation ON ledger_entries
    USING (user_id = current_setting('app.current_user_id', true)::uuid)
    WITH CHECK (user_id = current_setting('app.current_user_id', true)::uuid);

CREATE POLICY user_isolation ON llm_calls
    USING (user_id = current_setting('app.current_user_id', true)::uuid)
    WITH CHECK (user_id = current_setting('app.current_user_id', true)::uuid);

CREATE POLICY user_isolation ON user_events
    USING (user_id = current_setting('app.current_user_id', true)::uuid)
    WITH CHECK (user_id = current_setting('app.current_user_id', true)::uuid);

-- Questions: seed questions have NULL user_id (visible to all).
CREATE POLICY questions_isolation ON questions
    USING (user_id IS NULL OR user_id = current_setting('app.current_user_id', true)::uuid)
    WITH CHECK (user_id IS NULL OR user_id = current_setting('app.current_user_id', true)::uuid);
```

Create `sql/migrations/007_rls.down.sql`:

```sql
DROP POLICY IF EXISTS user_isolation ON interview_sessions;
DROP POLICY IF EXISTS user_isolation ON coach_analyses;
DROP POLICY IF EXISTS user_isolation ON grants;
DROP POLICY IF EXISTS user_isolation ON ledger_entries;
DROP POLICY IF EXISTS user_isolation ON llm_calls;
DROP POLICY IF EXISTS user_isolation ON user_events;
DROP POLICY IF EXISTS questions_isolation ON questions;

ALTER TABLE interview_sessions DISABLE ROW LEVEL SECURITY;
ALTER TABLE coach_analyses DISABLE ROW LEVEL SECURITY;
ALTER TABLE grants DISABLE ROW LEVEL SECURITY;
ALTER TABLE ledger_entries DISABLE ROW LEVEL SECURITY;
ALTER TABLE llm_calls DISABLE ROW LEVEL SECURITY;
ALTER TABLE user_events DISABLE ROW LEVEL SECURITY;
ALTER TABLE questions DISABLE ROW LEVEL SECURITY;

ALTER DEFAULT PRIVILEGES IN SCHEMA public REVOKE SELECT, INSERT, UPDATE, DELETE ON TABLES FROM drill_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public REVOKE USAGE ON SEQUENCES FROM drill_app;
REVOKE SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public FROM drill_app;
REVOKE USAGE ON ALL SEQUENCES IN SCHEMA public FROM drill_app;

DROP ROLE IF EXISTS drill_app;
```

- [ ] **Step 2: Create the RLS middleware package**

Create `internal/rls/middleware.go`:

```go
package rls

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/db"
)

type ctxKey string

const dbKey ctxKey = "rls_db"

// withDB stores a per-request DB connection in the context.
func withDB(ctx context.Context, conn *pgx.Conn) context.Context {
	return context.WithValue(ctx, dbKey, conn)
}

// DBFromContext returns the per-request RLS-scoped connection if present,
// falling back to the given pool. This is the only function callers need.
func DBFromContext(ctx context.Context, fallback db.DBTX) db.DBTX {
	if conn, ok := ctx.Value(dbKey).(*pgx.Conn); ok {
		return conn
	}
	return fallback
}

// WithRLS returns HTTP middleware that acquires a dedicated pool connection,
// sets app.current_user_id for RLS, and releases it after the request.
func WithRLS(pool *pgxpool.Pool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := auth.UserFromContext(r.Context())
			if user == nil {
				next.ServeHTTP(w, r)
				return
			}

			conn, err := pool.Acquire(r.Context())
			if err != nil {
				slog.Error("rls: acquire connection", "error", err)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				json.NewEncoder(w).Encode(map[string]string{"error": "service unavailable"})
				return
			}
			defer func() {
				conn.Exec(context.Background(), "RESET app.current_user_id")
				conn.Release()
			}()

			_, err = conn.Exec(r.Context(),
				"SELECT set_config('app.current_user_id', $1, false)",
				user.ID.String())
			if err != nil {
				slog.Error("rls: set current_user_id", "error", err)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]string{"error": "internal error"})
				return
			}

			ctx := withDB(r.Context(), conn.Conn())
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// WithUser is for job workers running outside HTTP middleware. It acquires a
// connection, sets the user ID for RLS, calls fn, then resets and releases.
func WithUser(ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID, fn func(ctx context.Context) error) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer func() {
		conn.Exec(context.Background(), "RESET app.current_user_id")
		conn.Release()
	}()

	_, err = conn.Exec(ctx,
		"SELECT set_config('app.current_user_id', $1, false)",
		userID.String())
	if err != nil {
		return err
	}

	return fn(withDB(ctx, conn.Conn()))
}
```

- [ ] **Step 3: Write RLS middleware tests**

Create `internal/rls/middleware_test.go`:

```go
package rls

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/btc/drill/internal/auth"
	"github.com/google/uuid"
)

func TestDBFromContext_Fallback(t *testing.T) {
	// With no RLS connection in context, should return the fallback.
	ctx := context.Background()
	fallback := &fakeDTBX{}
	result := DBFromContext(ctx, fallback)
	if result != fallback {
		t.Error("expected fallback to be returned")
	}
}

type fakeDTBX struct{}
func (f *fakeDTBX) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) { return pgconn.CommandTag{}, nil }
func (f *fakeDTBX) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) { return nil, nil }
func (f *fakeDTBX) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row { return nil }
```

Add imports as needed. The full integration test (two users can't see each other's data) requires a Postgres test container — add it following the pattern from `internal/handler/testutil_test.go`.

- [ ] **Step 4: Add `DatabaseAppURL` to config**

In `internal/config/config.go`, add to the `Database` struct:

```go
AppURL string `env:"DATABASE_APP_URL"`
```

This is optional — when empty, the app uses the main `URL` (no RLS enforcement). This allows gradual rollout.

- [ ] **Step 5: Wire RLS into main.go**

In `cmd/drill/main.go`, after `runMigrations(cfg.Database.URL)` (which uses the owner connection), check if `cfg.Database.AppURL` is set. If so, create a second pool for the app role and use it for the Backend:

```go
appCfg := cfg.Database
if cfg.Database.AppURL != "" {
    appCfg.URL = cfg.Database.AppURL
}
// The existing pool creation uses appCfg
```

The `handler.NewHandler` call should apply `rls.WithRLS(pool)` middleware. Read the `NewHandler` function signature to determine where to insert it.

- [ ] **Step 6: Refactor Backend methods to use `rls.DBFromContext`**

In `internal/backend/*.go`, find all calls to `db.New(b.pool)` (or `db.New(b.Pool)`) and replace with `db.New(rls.DBFromContext(ctx, b.pool))`. This requires that `ctx` is available — it should be, since all Backend methods accept `context.Context` as their first argument.

Start with the most security-sensitive tables:
- `internal/backend/session.go` — interview_sessions
- `internal/backend/billing.go` — grants, ledger_entries
- Then proceed to other files

- [ ] **Step 7: Update test infrastructure**

In `internal/handler/testutil_test.go`, the `startPostgres` function needs to:
1. Create the `drill_app` role after migrations run
2. Grant permissions (same as migration 007)
3. Use the app role URL for the test pool

- [ ] **Step 8: Run full test suite**

Run: `go test ./internal/... ./cmd/... -race -count=1 -timeout=300s`
Expected: PASS

- [ ] **Step 9: Commit and create PR**

```bash
git add sql/migrations/007_rls* internal/rls/ internal/config/config.go cmd/drill/main.go internal/backend/ internal/handler/testutil_test.go
git commit -m "feat: add row-level security for user-scoped tables

Closes #4"
gh pr create --title "feat: row-level security (Postgres RLS)" --body "$(cat <<'EOF'
## Summary
- Create `drill_app` Postgres role with restricted permissions
- Enable RLS on 7 direct-user_id tables with user isolation policies
- Per-request connection acquire middleware with SET/RESET lifecycle
- `rls.DBFromContext` helper for Backend methods
- `rls.WithUser` helper for job workers
- Join-dependent tables (messages, evaluations, etc.) deferred to follow-up

## Test plan
- [ ] Migration applies cleanly
- [ ] User A cannot see User B's sessions/grants
- [ ] Shared questions (NULL user_id) visible to all
- [ ] Unauthenticated queries return no rows (fail-closed)
- [ ] Full test suite passes

Closes #4

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

---

### Task I: Remove Orphan schema_migrations from drill_v0 (#17)

**Branch:** `chore/remove-orphan-schema-migrations`
**PR:** Closes #17

**Files:**
- Create: `scripts/remove-orphan-schema-migrations.sh`

**Context for the implementer:**

The Go app's `DATABASE_URL` was once accidentally pointed at `drill_v0` (the Python prototype's database). golang-migrate created a `schema_migrations` table with a single row (version=1, dirty=false). The URL has since been fixed. This orphan table needs to be removed.

**IMPORTANT:** `drill_v0` is the Python prototype's priceless database. The script MUST verify the table state before dropping. Any deviation from the expected state (exactly 1 row, version=1, dirty=false) must abort.

- [ ] **Step 1: Create the verification-and-drop script**

Create `scripts/remove-orphan-schema-migrations.sh`:

```bash
#!/usr/bin/env bash
# Removes the orphan schema_migrations table from drill_v0.
# This table was created accidentally by golang-migrate when DATABASE_URL
# was briefly misconfigured to point at drill_v0 instead of drill_v1.
#
# Safety: verifies exact expected state before dropping.
set -euo pipefail

DB="${1:-drill_v0}"

echo "=== Removing orphan schema_migrations from $DB ==="
echo ""

echo "Step 1: Verify table exists..."
if ! psql "$DB" -tA -c "SELECT 1 FROM information_schema.tables WHERE table_name = 'schema_migrations'" | grep -q 1; then
    echo "Table schema_migrations does not exist in $DB. Nothing to do."
    exit 0
fi

echo "Step 2: Verify table state..."
ROW_COUNT=$(psql "$DB" -tA -c "SELECT count(*) FROM schema_migrations")
VERSION=$(psql "$DB" -tA -c "SELECT version FROM schema_migrations")
DIRTY=$(psql "$DB" -tA -c "SELECT dirty FROM schema_migrations")

echo "  Rows: $ROW_COUNT (expected: 1)"
echo "  Version: $VERSION (expected: 1)"
echo "  Dirty: $DIRTY (expected: f)"

if [[ "$ROW_COUNT" -ne 1 ]] || [[ "$VERSION" -ne 1 ]] || [[ "$DIRTY" != "f" ]]; then
    echo ""
    echo "ERROR: Table state does not match expected orphan state."
    echo "  Expected: 1 row, version=1, dirty=f"
    echo "  Got: $ROW_COUNT rows, version=$VERSION, dirty=$DIRTY"
    echo ""
    echo "Aborting. Do NOT drop this table without manual investigation."
    exit 1
fi

echo ""
echo "Step 3: Dropping orphan table..."
psql "$DB" -c "DROP TABLE schema_migrations"
echo ""
echo "Done. The orphan schema_migrations table has been removed from $DB."
echo "All other tables in $DB are untouched."
```

- [ ] **Step 2: Make the script executable**

```bash
chmod +x scripts/remove-orphan-schema-migrations.sh
```

- [ ] **Step 3: Commit and create PR**

```bash
git add scripts/remove-orphan-schema-migrations.sh
git commit -m "chore: add script to remove orphan schema_migrations from drill_v0

Closes #17"
gh pr create --title "chore: remove orphan schema_migrations from drill_v0" --body "$(cat <<'EOF'
## Summary
Script to safely remove the orphan `schema_migrations` table from the `drill_v0` database. This table was created accidentally when `DATABASE_URL` was briefly misconfigured.

The script verifies exact expected state (1 row, version=1, dirty=f) before dropping. Any deviation aborts with an error.

**To run:** `./scripts/remove-orphan-schema-migrations.sh drill_v0`

## Test plan
- [ ] Script aborts if table doesn't exist (clean exit)
- [ ] Script aborts if table has unexpected state
- [ ] Script drops table when state matches
- [ ] No other tables in drill_v0 are affected

Closes #17

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```
