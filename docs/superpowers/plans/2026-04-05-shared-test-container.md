# Shared Test Container Infrastructure — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Eliminate per-test Postgres container overhead project-wide by sharing one container per package, with per-test database isolation and parallel execution.

**Architecture:** A `testutil.PG` factory type owns a shared Postgres container started in `TestMain`. Each test gets its own database via `CREATE DATABASE`, enabling `t.Parallel()`. Config is built via `envconfig.MapLookuper` (no `t.Setenv`), keeping config inert and parallel-safe.

**Tech Stack:** Go testcontainers, envconfig MapLookuper, pgxpool, golang-migrate

**Spec:** `docs/superpowers/specs/2026-04-05-shared-test-container-design.md`

---

## File Structure

### New files
- `internal/testutil/pg.go` — `PG` factory type, `SharedPostgres()`, `NewDatabase()`, container lifecycle
- `internal/testutil/config.go` — `Config()`, `ConfigWithOverrides()` using MapLookuper
- `internal/testutil/backend.go` — `NewBackend(t, cfg)`, PG method wrappers
- `internal/handler/main_test.go` — `TestMain` for handler package
- `internal/backend/main_test.go` — `TestMain` for backend package
- `internal/rpc/question/main_test.go` — `TestMain` for rpc/question package
- `internal/jobs/main_test.go` — `TestMain` for jobs package

### Modified files
- `internal/config/config.go` — delete `Database.NewPool()` method
- `internal/backend/backend.go` — inline pool setup in `New()`
- `internal/testutil/testutil.go` — delete old `StartPostgres`, `LoadTestConfig`, `NewTestBackend`, `SignupAndLogin`
- `internal/backend/testutil_test.go` — delete entirely
- `internal/backend/*_test.go` (7 files) — change `package backend` → `package backend_test`, replace `b.pool` → `b.Pool()`
- `internal/handler/*_test.go` (10 files) — replace `testutil.NewTestBackend(t)` → `pg.NewBackend(t)`, add `t.Parallel()`
- `internal/rpc/question/server_test.go` — same migration
- `internal/jobs/evaluate_test.go` — delete inline `startTestPostgres`, use `pg.NewDatabase(t)`
- `internal/jobs/integration_test.go` — delete inline container setup, use `pg.NewDatabase(t)`
- `internal/jobs/error_handler_test.go` — replace `startTestPostgres(t)` → pool from `pg.NewDatabase(t)`
- `internal/jobs/cleanup_test.go` — same

---

## Task 1: Create `testutil.Config` with MapLookuper

Build the parallel-safe config constructor. This is a standalone function with no container dependency.

**Files:**
- Create: `internal/testutil/config.go`

- [ ] **Step 1: Write `testutil.Config` and `testutil.ConfigWithOverrides`**

```go
// internal/testutil/config.go
package testutil

import (
	"context"
	"testing"

	"github.com/sethvargo/go-envconfig"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/config"
)

// baseEnv returns the minimum env vars needed to satisfy config.Load's
// required fields, with sensible test defaults.
func baseEnv(databaseURL string) map[string]string {
	return map[string]string{
		"DATABASE_URL":      databaseURL,
		"ANTHROPIC_API_KEY": "sk-ant-test",
		"OPENAI_API_KEY":    "sk-test",
		"AUTH_TOKEN_SECRET":  "test-secret-at-least-32-bytes-long",
		"AUTH_BCRYPT_COST":   "4",
	}
}

// Config returns a *config.Config populated with test defaults via
// MapLookuper. No environment variables are read or set, making this
// safe for use with t.Parallel(). The DATABASE_URL is set to a dummy
// value — no real database is created.
func Config(t *testing.T) *config.Config {
	t.Helper()
	return ConfigWithOverrides(t, "postgres://unused", nil)
}

// ConfigWithOverrides returns a *config.Config with the given database URL
// and any additional env-key overrides merged on top of the test defaults.
func ConfigWithOverrides(t *testing.T, databaseURL string, overrides map[string]string) *config.Config {
	t.Helper()
	env := baseEnv(databaseURL)
	for k, v := range overrides {
		env[k] = v
	}
	var cfg config.Config
	err := envconfig.ProcessWith(context.Background(), &envconfig.Config{
		Target:   &cfg,
		Lookuper: envconfig.MapLookuper(env),
	})
	require.NoError(t, err)
	return &cfg
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./internal/testutil/...`
Expected: clean build, no errors

- [ ] **Step 3: Commit**

```bash
git add internal/testutil/config.go
git commit -m "feat(testutil): add Config and ConfigWithOverrides using MapLookuper"
```

---

## Task 2: Create `testutil.PG` factory type

Build the shared container factory with `NewDatabase` for per-test isolation.

**Files:**
- Create: `internal/testutil/pg.go`

- [ ] **Step 1: Write the PG type**

```go
// internal/testutil/pg.go
package testutil

import (
	"context"
	"crypto/rand"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	migrations "github.com/btc/drill/sql/migrations"
)

// PG holds a shared Postgres container. Create one per package via
// SharedPostgres in TestMain.
type PG struct {
	connStr   string                   // connection string to the default database
	container *postgres.PostgresContainer
}

// SharedPostgres starts a single Postgres 16 container and returns a PG
// factory. Call Cleanup in TestMain after m.Run().
func SharedPostgres() PG {
	ctx := context.Background()

	container, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("drill_test"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second),
		),
	)
	if err != nil {
		panic(fmt.Sprintf("testutil.SharedPostgres: start container: %v", err))
	}

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		container.Terminate(ctx)
		panic(fmt.Sprintf("testutil.SharedPostgres: connection string: %v", err))
	}

	return PG{connStr: connStr, container: container}
}

// Cleanup terminates the shared container. Call in TestMain after m.Run().
func (pg PG) Cleanup() {
	if pg.container != nil {
		pg.container.Terminate(context.Background())
	}
}

var unsafeChars = regexp.MustCompile(`[^a-z0-9_]`)

// dbName returns a Postgres-safe database name derived from the test name.
func dbName(t *testing.T) string {
	name := strings.ToLower(t.Name())
	name = unsafeChars.ReplaceAllString(name, "_")
	// Truncate to leave room for suffix; Postgres limit is 63 bytes.
	if len(name) > 50 {
		name = name[:50]
	}
	b := make([]byte, 4)
	rand.Read(b)
	return fmt.Sprintf("test_%s_%x", name, b)
}

// NewDatabase creates a fresh database on the shared container, runs app
// migrations, and returns the connection string. The database is dropped
// on test cleanup.
func (pg PG) NewDatabase(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	name := dbName(t)

	// Connect to the default database to create the test database.
	conn, err := pgx.Connect(ctx, pg.connStr)
	require.NoError(t, err)
	_, err = conn.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s", name))
	require.NoError(t, err)
	conn.Close(ctx)

	// Build connection string for the new database by replacing the dbname.
	// The container connStr ends with /drill_test?sslmode=disable (or similar).
	testConnStr := replaceDBName(pg.connStr, name)

	// Run app migrations.
	d, err := iofs.New(migrations.FS, ".")
	require.NoError(t, err)
	trimmed := strings.TrimPrefix(testConnStr, "postgresql://")
	trimmed = strings.TrimPrefix(trimmed, "postgres://")
	pgxURL := "pgx5://" + trimmed
	m, err := migrate.NewWithSourceInstance("iofs", d, pgxURL)
	require.NoError(t, err)
	err = m.Up()
	if err != nil && err != migrate.ErrNoChange {
		t.Fatalf("migrate up: %v", err)
	}
	srcErr, dbErr := m.Close()
	require.NoError(t, srcErr)
	require.NoError(t, dbErr)

	t.Cleanup(func() {
		// Drop the test database.
		conn, err := pgx.Connect(context.Background(), pg.connStr)
		if err != nil {
			return // container may already be gone
		}
		// Terminate existing connections before dropping.
		conn.Exec(context.Background(), fmt.Sprintf(
			"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '%s'", name))
		conn.Exec(context.Background(), fmt.Sprintf("DROP DATABASE IF EXISTS %s", name))
		conn.Close(context.Background())
	})

	return testConnStr
}

// replaceDBName swaps the database name in a Postgres connection string.
// Handles both postgresql:// and postgres:// schemes.
func replaceDBName(connStr, newDB string) string {
	// connStr looks like: postgres://user:pass@host:port/olddb?params
	// Find the last / before ? and replace the dbname.
	qIdx := strings.Index(connStr, "?")
	var base, query string
	if qIdx >= 0 {
		base = connStr[:qIdx]
		query = connStr[qIdx:]
	} else {
		base = connStr
		query = ""
	}
	slashIdx := strings.LastIndex(base, "/")
	if slashIdx < 0 {
		return connStr // shouldn't happen
	}
	return base[:slashIdx+1] + newDB + query
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./internal/testutil/...`
Expected: clean build

- [ ] **Step 3: Commit**

```bash
git add internal/testutil/pg.go
git commit -m "feat(testutil): add PG factory with SharedPostgres and NewDatabase"
```

---

## Task 3: Add `PG.NewBackend`, `PG.Config`, `PG.ConfigWithOverrides`, and `testutil.NewBackend`

Wire the factory to produce backends and configs.

**Files:**
- Create: `internal/testutil/backend.go`

- [ ] **Step 1: Write the backend helpers**

```go
// internal/testutil/backend.go
package testutil

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/backend"
	"github.com/btc/drill/internal/config"
)

// NewBackend creates a *backend.Backend from a caller-provided config and
// registers cleanup. Use this when you need to customize config before
// creating the backend (e.g. setting Auth.BaseURL for OAuth tests).
func NewBackend(t *testing.T, cfg *config.Config) *backend.Backend {
	t.Helper()
	b, err := backend.New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { b.Close() })
	return b
}

// NewBackend creates a database on the shared container and returns a
// fully-initialized *backend.Backend. This is the common one-liner for
// most integration tests.
func (pg PG) NewBackend(t *testing.T) *backend.Backend {
	t.Helper()
	cfg := pg.Config(t)
	return NewBackend(t, cfg)
}

// Config creates a database on the shared container and returns a
// *config.Config with the real database URL. Use this when you need
// to customize the config before creating a backend.
func (pg PG) Config(t *testing.T) *config.Config {
	t.Helper()
	return pg.ConfigWithOverrides(t, nil)
}

// ConfigWithOverrides creates a database and returns a *config.Config with
// the real database URL and any additional env-key overrides merged in.
func (pg PG) ConfigWithOverrides(t *testing.T, overrides map[string]string) *config.Config {
	t.Helper()
	dbURL := pg.NewDatabase(t)
	return ConfigWithOverrides(t, dbURL, overrides)
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./internal/testutil/...`
Expected: clean build

- [ ] **Step 3: Commit**

```bash
git add internal/testutil/backend.go
git commit -m "feat(testutil): add PG.NewBackend, PG.Config, PG.ConfigWithOverrides, NewBackend"
```

---

## Task 4: Move `Database.NewPool()` into `backend.New`

Config should be inert data. Move pool creation into the one call site.

**Files:**
- Modify: `internal/config/config.go` — delete `NewPool` method and its `pgx`/`pgxpool` imports
- Modify: `internal/backend/backend.go` — inline pool setup

- [ ] **Step 1: Inline pool setup in `backend.New`**

In `internal/backend/backend.go`, replace line 65:

```go
pool, err := cfg.Database.NewPool(context.Background(), otelpgx.NewTracer())
```

with the inlined logic:

```go
poolCfg, err := pgxpool.ParseConfig(cfg.Database.URL)
if err != nil {
	return nil, fmt.Errorf("parse database url: %w", err)
}
poolCfg.MaxConns = cfg.Database.MaxPoolConns
poolCfg.ConnConfig.Tracer = otelpgx.NewTracer()
pool, err := pgxpool.NewWithConfig(context.Background(), poolCfg)
if err != nil {
	return nil, fmt.Errorf("create pool: %w", err)
}
if err := pool.Ping(context.Background()); err != nil {
	pool.Close()
	return nil, fmt.Errorf("ping database: %w", err)
}
slog.Info("database connected")
```

Ensure `pgxpool` is in the import block for `backend.go`. It likely already is since `Backend` holds a `*pgxpool.Pool`.

- [ ] **Step 2: Delete `Database.NewPool` from config.go**

Remove the `NewPool` method (lines 48-71 of `internal/config/config.go`) and the now-unused imports `pgx` and `pgxpool` from the config package imports. Keep the `Database` struct with its `URL` and `MaxPoolConns` fields.

- [ ] **Step 3: Verify existing tests still pass**

Run: `go test ./internal/backend/ -short -count=1`
Run: `go test ./internal/handler/ -short -count=1`
Expected: all tests pass (short mode skips integration tests, but compilation must succeed)

Run: `go build ./...`
Expected: clean build, no references to `Database.NewPool` remain

- [ ] **Step 4: Commit**

```bash
git add internal/config/config.go internal/backend/backend.go
git commit -m "refactor: move Database.NewPool into backend.New, config stays inert"
```

---

## Task 5: Migrate `internal/handler/` tests

Add `TestMain`, replace all container-per-test calls, add `t.Parallel()`.

**Files:**
- Create: `internal/handler/main_test.go`
- Modify: `internal/handler/auth_test.go`
- Modify: `internal/handler/billing_test.go`
- Modify: `internal/handler/evaluation_test.go`
- Modify: `internal/handler/health_test.go`
- Modify: `internal/handler/oauth_flow_test.go`
- Modify: `internal/handler/oauth_test.go`
- Modify: `internal/handler/seed_test.go`
- Modify: `internal/handler/session_rest_test.go`
- Modify: `internal/handler/session_ws_test.go`
- Modify: `internal/handler/user_test.go`

- [ ] **Step 1: Create `main_test.go` with TestMain**

```go
// internal/handler/main_test.go
package handler_test

import (
	"os"
	"testing"

	"github.com/btc/drill/internal/testutil"
)

var pg testutil.PG

func TestMain(m *testing.M) {
	pg = testutil.SharedPostgres()
	code := m.Run()
	pg.Cleanup()
	os.Exit(code)
}
```

- [ ] **Step 2: Migrate `auth_test.go`**

In every test function (TestSignup_Success, TestSignup_DuplicateEmail, TestLogin_Success, TestLogin_WrongPassword, TestForgotAndResetPassword, TestVerifyEmail, TestLogout — 7 functions):

1. Remove the `testing.Short()` skip guard
2. Add `t.Parallel()` as the first line
3. Replace `testutil.NewTestBackend(t)` with `pg.NewBackend(t)`

Example for each function — before:
```go
func TestSignup_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := testutil.NewTestBackend(t)
```
After:
```go
func TestSignup_Success(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
```

Apply this pattern to all 7 functions in `auth_test.go`.

- [ ] **Step 3: Migrate `billing_test.go`**

Same pattern for TestGetUsage_WithFreeGrant and TestGetUsage_ExpiredGrant (2 functions):
- Remove `testing.Short()` skip
- Add `t.Parallel()`
- Replace `testutil.NewTestBackend(t)` → `pg.NewBackend(t)`

- [ ] **Step 4: Migrate `evaluation_test.go`**

Same pattern for TestGetEvaluation_Returns404WhenNotReady, TestGetEvaluation_Returns403ForWrongUser, TestGetEvaluation_Returns200WithJSON, TestRetryEvaluation_Returns202, TestRetryEvaluation_Returns409WhenNotFailed, TestListQuestions_Success (functions that call `testutil.NewTestBackend`):
- Remove `testing.Short()` skip
- Add `t.Parallel()`
- Replace `testutil.NewTestBackend(t)` → `pg.NewBackend(t)`

- [ ] **Step 5: Migrate `health_test.go`**

TestHealthCheck_Healthy (1 function):
- Remove `testing.Short()` skip
- Add `t.Parallel()`
- Replace `testutil.NewTestBackend(t)` → `pg.NewBackend(t)`

- [ ] **Step 6: Migrate `oauth_flow_test.go`**

**These tests must NOT use `t.Parallel()`** — they modify global goth provider state.

For TestOAuthStart_UnknownProvider, TestOAuthCallback_NewUser, TestOAuthCallback_ExistingUser, TestOAuthCallback_NickNameFallback, TestOAuthCallback_UnknownProvider (5 functions):
- Remove `testing.Short()` skip
- Replace `testutil.NewTestBackend(t)` → `pg.NewBackend(t)`
- Do NOT add `t.Parallel()`

For the 3 functions that call `t.Setenv("BASE_URL", ...)` (TestOAuthCallback_NewUser, TestOAuthCallback_ExistingUser, TestOAuthCallback_NickNameFallback), replace:

Before:
```go
t.Setenv("BASE_URL", "http://localhost:3000")
b := testutil.NewTestBackend(t)
```
After:
```go
cfg := pg.ConfigWithOverrides(t, map[string]string{
	"BASE_URL": "http://localhost:3000",
})
b := testutil.NewBackend(t, cfg)
```

- [ ] **Step 7: Migrate `oauth_test.go`**

TestCSRF_RejectsPostWithoutToken, TestCSRF_PostWithValidToken, TestCSRF_AllowsGetRequests (3 functions):

These use `testutil.LoadTestConfig(t, "postgres://unused")` — they don't need a database. Replace with `testutil.Config(t)`:

Before:
```go
cfg := testutil.LoadTestConfig(t, "postgres://unused")
```
After:
```go
cfg := testutil.Config(t)
```

Add `t.Parallel()` to each. These have no `testing.Short()` guard.

- [ ] **Step 8: Migrate `security_test.go`**

TestSecurityHeaders_AlwaysPresent, TestSecurityHeaders_HSTSWhenSecure (2 functions):

These are pure unit tests — no backend, no container. Just add `t.Parallel()`.

- [ ] **Step 9: Migrate `seed_test.go`**

TestSeedQuestions (1 function):

Before:
```go
connStr := testutil.StartPostgres(t)
```
After:
```go
t.Parallel()
connStr := pg.NewDatabase(t)
```

Remove the `testing.Short()` skip guard.

- [ ] **Step 10: Migrate `session_rest_test.go`**

All 13 test functions that call `testutil.NewTestBackend(t)`:
- Remove `testing.Short()` skip
- Add `t.Parallel()`
- Replace `testutil.NewTestBackend(t)` → `pg.NewBackend(t)`

For subtests using `t.Run` (TestCreateSession_InvalidDuration, TestCreateSession_BoundaryDurations), the subtests share the parent's backend — this is fine, the parent test is parallel and the subtests run sequentially within it.

- [ ] **Step 11: Migrate `session_ws_test.go`**

The `newWSTestBackend` helper at line 260 calls `testutil.NewTestBackend(t)`. Replace:

Before:
```go
func newWSTestBackend(t *testing.T, anthropicURL string) *backend.Backend {
	t.Helper()
	b := testutil.NewTestBackend(t)
```
After:
```go
func newWSTestBackend(t *testing.T, anthropicURL string) *backend.Backend {
	t.Helper()
	b := pg.NewBackend(t)
```

For all 24 `TestWS_*` functions:
- Remove `testing.Short()` skip
- Add `t.Parallel()`

For `TestWS_AbandonedCleanup` (line 907) which calls `testutil.NewTestBackend(t)` directly instead of `newWSTestBackend`:
- Replace `testutil.NewTestBackend(t)` → `pg.NewBackend(t)`

- [ ] **Step 12: Migrate `user_test.go`**

TestGetMe_Authenticated, TestGetMe_Unauthenticated (2 functions):
- Remove `testing.Short()` skip
- Add `t.Parallel()`
- Replace `testutil.NewTestBackend(t)` → `pg.NewBackend(t)`

- [ ] **Step 13: Run handler tests and verify**

Run: `go test ./internal/handler/ -count=1 -v 2>&1 | tail -5`
Expected: all tests pass, total time significantly reduced from ~81s

- [ ] **Step 14: Commit**

```bash
git add internal/handler/
git commit -m "feat(handler): shared test container with parallel execution"
```

---

## Task 6: Move `internal/backend/` tests to `package backend_test` and migrate

**Files:**
- Create: `internal/backend/main_test.go`
- Create: `internal/backend/lifecycle_test.go` (from TestNewAndClose in unit_test.go)
- Delete: `internal/backend/testutil_test.go`
- Rename: `internal/backend/unit_test.go` → `internal/backend/internal_test.go` (stays `package backend`)
- Modify: `internal/backend/auth_test.go`
- Modify: `internal/backend/billing_test.go`
- Modify: `internal/backend/coach_test.go`
- Modify: `internal/backend/educator_test.go`
- Modify: `internal/backend/evaluation_test.go`
- Modify: `internal/backend/oauth_test.go`

- [ ] **Step 1: Create `main_test.go` with TestMain**

```go
// internal/backend/main_test.go
package backend_test

import (
	"os"
	"testing"

	"github.com/btc/drill/internal/testutil"
)

var pg testutil.PG

func TestMain(m *testing.M) {
	pg = testutil.SharedPostgres()
	code := m.Run()
	pg.Cleanup()
	os.Exit(code)
}
```

- [ ] **Step 2: Delete `testutil_test.go`**

Delete `internal/backend/testutil_test.go` entirely. Its helpers (`startPostgres`, `newTestBackend`, `loadTestConfig`, `seedUser`) are replaced by `testutil.PG` and `backendtest.SeedUser`.

- [ ] **Step 3: Migrate all 7 test files**

For each file (`auth_test.go`, `billing_test.go`, `coach_test.go`, `educator_test.go`, `evaluation_test.go`, `oauth_test.go`, `unit_test.go`):

1. Change `package backend` → `package backend_test`
2. Add imports:
   ```go
   "github.com/btc/drill/internal/backend"
   "github.com/btc/drill/internal/backendtest"
   "github.com/btc/drill/internal/testutil"
   ```
3. Replace all `b.pool` → `b.Pool()`
4. Replace all `newTestBackend(t)` → `pg.NewBackend(t)`
5. Replace all `seedUser(t, b)` → `backendtest.SeedUser(t, b)`
6. Remove all `testing.Short()` skip guards
7. Add `t.Parallel()` to each test function

Special case for `unit_test.go` — split into two files:

**A. `internal/backend/lifecycle_test.go`** (`package backend_test`) — the integration test:

```go
package backend_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewAndClose(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	require.NotNil(t, b.Pool())
	require.NotNil(t, b.Config())

	err := b.Ping(context.Background())
	require.NoError(t, err)

	err = b.Close()
	require.NoError(t, err)
}
```

**B. Rename `unit_test.go` → `internal_test.go`** (`package backend`) — keeps the three pure unit tests for unexported functions (`parseClientIP`, `isDuplicateKeyError`, `truncateRunes`). These stay in `package backend` since they test unexported functions and don't need a database. Remove `TestNewAndClose` from this file. Add `t.Parallel()` to each test function.

- [ ] **Step 4: Verify backend tests pass**

Run: `go test ./internal/backend/ -count=1 -v 2>&1 | tail -5`
Expected: all tests pass

- [ ] **Step 5: Commit**

```bash
git add internal/backend/
git commit -m "feat(backend): shared test container, move tests to package backend_test"
```

---

## Task 7: Migrate `internal/rpc/question/` tests

**Files:**
- Create: `internal/rpc/question/main_test.go`
- Modify: `internal/rpc/question/server_test.go`

- [ ] **Step 1: Create `main_test.go`**

```go
// internal/rpc/question/main_test.go
package question_test

import (
	"os"
	"testing"

	"github.com/btc/drill/internal/testutil"
)

var pg testutil.PG

func TestMain(m *testing.M) {
	pg = testutil.SharedPostgres()
	code := m.Run()
	pg.Cleanup()
	os.Exit(code)
}
```

- [ ] **Step 2: Migrate `server_test.go`**

For all test functions (5 functions calling `testutil.NewTestBackend(t)`):
- Remove `testing.Short()` skip guard
- Add `t.Parallel()`
- Replace `testutil.NewTestBackend(t)` → `pg.NewBackend(t)`

- [ ] **Step 3: Run and verify**

Run: `go test ./internal/rpc/question/ -count=1 -v 2>&1 | tail -5`
Expected: all pass

- [ ] **Step 4: Commit**

```bash
git add internal/rpc/question/
git commit -m "feat(rpc/question): shared test container with parallel execution"
```

---

## Task 8: Migrate `internal/jobs/` tests

The jobs tests work with raw pools and River clients — they don't use `backend.Backend`. They need `pg.NewDatabase(t)` for a connection string, then create their own pool.

**Files:**
- Create: `internal/jobs/main_test.go`
- Modify: `internal/jobs/evaluate_test.go`
- Modify: `internal/jobs/integration_test.go`
- Modify: `internal/jobs/error_handler_test.go`
- Modify: `internal/jobs/cleanup_test.go`

- [ ] **Step 1: Create `main_test.go`**

```go
// internal/jobs/main_test.go
package jobs_test

import (
	"os"
	"testing"

	"github.com/btc/drill/internal/testutil"
)

var pg testutil.PG

func TestMain(m *testing.M) {
	pg = testutil.SharedPostgres()
	code := m.Run()
	pg.Cleanup()
	os.Exit(code)
}
```

- [ ] **Step 2: Replace `startTestPostgres` in `evaluate_test.go`**

Delete the `startTestPostgres` function (lines 38-88). Replace it with a helper that uses the shared container:

```go
// newTestPool creates a database on the shared container, connects a pool,
// and runs River migrations. Returns the pool for direct use by job tests.
func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	connStr := pg.NewDatabase(t) // app migrations already run

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, connStr)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	// Run River migrations (River needs its own internal tables).
	migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	require.NoError(t, err)
	_, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil)
	require.NoError(t, err)

	return pool
}
```

In every test function that calls `startTestPostgres(t)`, replace with `newTestPool(t)`. Remove the `testing.Short()` skip guard (it was inside `startTestPostgres`). Add `t.Parallel()`.

Remove testcontainers imports (`testcontainers-go`, `postgres`, `wait`) and migration imports (`golang-migrate`, `iofs`, `os`) that are no longer needed.

- [ ] **Step 3: Migrate `integration_test.go`**

`TestSendEmail_Integration` has inline container setup. Replace:

Before (lines 28-61):
```go
// Start Postgres
pgContainer, err := postgres.Run(ctx, ...)
// ... container setup ...
pool, err := pgxpool.New(ctx, connStr)
// ... River migrations ...
t.Setenv("DATABASE_URL", connStr)
// ... more t.Setenv ...
cfg, err := config.Load()
```

After:
```go
t.Parallel()
connStr := pg.NewDatabase(t)
pool, err := pgxpool.New(ctx, connStr)
require.NoError(t, err)
t.Cleanup(pool.Close)

// Run River migrations.
migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
require.NoError(t, err)
_, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil)
require.NoError(t, err)

cfg := testutil.ConfigWithOverrides(t, connStr, nil)
```

Remove all `t.Setenv` calls, testcontainers imports, and the `testing.Short()` skip guard.

- [ ] **Step 4: Migrate `error_handler_test.go` and `cleanup_test.go`**

Both call `startTestPostgres(t)` (which is defined in `evaluate_test.go`). After step 2, they call `newTestPool(t)` instead. For each test function:
- Remove `testing.Short()` skip (was inside old `startTestPostgres`)
- Add `t.Parallel()`
- Replace `startTestPostgres(t)` → `newTestPool(t)`

- [ ] **Step 5: Run and verify**

Run: `go test ./internal/jobs/ -count=1 -v 2>&1 | tail -5`
Expected: all pass

- [ ] **Step 6: Commit**

```bash
git add internal/jobs/
git commit -m "feat(jobs): shared test container with parallel execution"
```

---

## Task 9: Clean up deprecated `testutil` functions

Remove the old API now that all callers are migrated.

**Files:**
- Modify: `internal/testutil/testutil.go` — delete `StartPostgres`, `LoadTestConfig`, old `NewTestBackend`, `SignupAndLogin`

- [ ] **Step 1: Verify no callers remain**

Run: `grep -r 'testutil\.StartPostgres\|testutil\.LoadTestConfig\|testutil\.NewTestBackend\b' internal/ --include='*.go'`
Expected: no matches (all callers migrated in tasks 5-8)

Run: `grep -r 'testutil\.SignupAndLogin' internal/ --include='*.go'`
Expected: check if `SignupAndLogin` is still used. If it is, keep it. If not, delete it.

- [ ] **Step 2: Delete old functions from `testutil.go`**

Delete the following functions from `internal/testutil/testutil.go`:
- `StartPostgres` (lines 31-81)
- `LoadTestConfig` (lines 83-94)
- `NewTestBackend` (lines 98-108) — the old zero-arg version

If `SignupAndLogin` has no remaining callers, delete it too (lines 112-138). If it does, keep it — it doesn't conflict with the new API.

Remove unused imports (testcontainers, postgres, wait, etc.) from the file. If all functions are deleted, delete the entire file.

- [ ] **Step 3: Verify clean build**

Run: `go build ./...`
Expected: clean build, no errors

Run: `go test ./internal/... -count=1 2>&1 | tail -10`
Expected: all tests pass

- [ ] **Step 4: Commit**

```bash
git add internal/testutil/
git commit -m "cleanup: remove deprecated testutil functions"
```
