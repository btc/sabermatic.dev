# Shared Test Container Infrastructure

## Problem

Every integration test function creates its own Postgres testcontainer (~1.2s overhead each). Across the project there are 160+ container starts. Handler tests alone take 81s for near-instant test logic. Tests run sequentially because `t.Setenv` (used for config injection) panics under `t.Parallel()`.

## Solution

One Postgres container per test package via `TestMain`. Each test gets its own database on that container via `CREATE DATABASE`. Tests run in parallel with `t.Parallel()`. Config is built via `envconfig.MapLookuper` instead of environment variables.

## API

### `testutil.PG` factory type

Owns the shared container. Created once in `TestMain`, used by every test.

```go
var pg testutil.PG

func TestMain(m *testing.M) {
    pg = testutil.SharedPostgres()
    code := m.Run()
    pg.Cleanup()
    os.Exit(code)
}
```

Methods:

- `pg.NewBackend(t *testing.T) *backend.Backend` — the common one-liner. Creates a database (name derived from sanitized `t.Name()` + random suffix), runs migrations, builds config, creates backend. Registers cleanup (backend close + database drop).
- `pg.NewDatabase(t *testing.T) string` — creates a database, runs migrations, returns connection string. For tests needing raw DB access (e.g. `seed_test.go`). Drops database on cleanup.
- `pg.Config(t *testing.T) *config.Config` — creates a database and returns a config with its URL set. For tests that need to customize config before creating a backend.
- `pg.ConfigWithOverrides(t *testing.T, overrides map[string]string) *config.Config` — same as `Config` but merges extra env overrides into the `MapLookuper` (e.g. `BASE_URL` for OAuth tests).

### `testutil.Config(t *testing.T) *config.Config`

Standalone config construction, no container needed. For tests that need a config but no real database (CSRF, security header tests). Uses `envconfig.ProcessWith` + `MapLookuper` with dummy required values (`DATABASE_URL=postgres://unused`, dummy API keys). Struct tag defaults populate everything else.

Note the distinction: `pg.Config(t)` creates a real database and returns a config pointing to it. `testutil.Config(t)` returns a config with a dummy database URL — no database is created.

### `testutil.NewBackend(t *testing.T, cfg *config.Config) *backend.Backend`

Creates a backend from a caller-provided config. For the override path where a test needs to customize config between construction and backend creation:

```go
cfg := pg.ConfigWithOverrides(t, map[string]string{
    "BASE_URL": "http://localhost:3000",
})
b := testutil.NewBackend(t, cfg)
```

## Config construction

`testutil.Config` and all `pg.Config*` methods use `envconfig.ProcessWith` with `MapLookuper` instead of reading from the OS environment. This avoids `t.Setenv` entirely, enabling `t.Parallel()`.

```go
func Config(t *testing.T) *config.Config {
    t.Helper()
    lookuper := envconfig.MapLookuper(map[string]string{
        "DATABASE_URL":      "postgres://unused",
        "ANTHROPIC_API_KEY": "sk-ant-test",
        "OPENAI_API_KEY":    "sk-test",
        "AUTH_TOKEN_SECRET":  "test-secret-at-least-32-bytes-long",
        "AUTH_BCRYPT_COST":   "4",
    })
    var cfg config.Config
    err := envconfig.ProcessWith(context.Background(), &envconfig.Config{
        Target:   &cfg,
        Lookuper: lookuper,
    })
    require.NoError(t, err)
    return &cfg
}
```

The `pg.Config(t)` and `pg.ConfigWithOverrides(t, overrides)` methods do the same but substitute the real database URL from `NewDatabase` and merge any caller-provided overrides.

## Database isolation

Each test gets its own database on the shared container:

1. Connect to the container's default database
2. `CREATE DATABASE test_<sanitized_t_name>_<rand>`
3. Run app migrations on the new database
4. Return connection string pointing to the new database
5. On cleanup: close connections, `DROP DATABASE`

Database names use sanitized `t.Name()` (replace `/` and special chars with `_`) plus a short random suffix for debuggability.

## Move `Database.NewPool()` into `backend.New`

`config.Database.NewPool()` is a factory method on a config struct. Config should be inert data. Move the `pgxpool` setup logic into `backend.New` where the pool is actually used. Delete `Database.NewPool()`. Only one call site exists (`backend.go:65`).

## Consolidation

### Delete `internal/backend/testutil_test.go` private helpers

The private `startPostgres`, `newTestBackend`, `loadTestConfig`, and `seedUser` functions duplicate `testutil`. Delete them.

### Move backend tests to `package backend_test`

Backend tests currently use `package backend` (white-box) to access `b.pool` and `b.jobs`. All `b.pool` usage can use the existing public `b.Pool()` accessor. The single `b.jobs` usage (`require.NotNil(t, b.jobs)` in `unit_test.go`) is a lifecycle assertion that can be tested via `backend.New` succeeding + `b.Close` succeeding.

Moving to `package backend_test` breaks the circular dependency (`testutil` -> `backend` -> `testutil`) and lets backend tests import `testutil.PG`.

### Consolidate `internal/jobs/` inline containers

`internal/jobs/evaluate_test.go` and `integration_test.go` have their own inline `startTestPostgres` functions. Replace with `testutil.PG` via `TestMain`.

## Packages requiring `TestMain`

| Package | Container starts today | Notes |
|---|---|---|
| `internal/handler/` | 34 | Largest win |
| `internal/backend/` | 104 | Most test functions |
| `internal/rpc/question/` | 5 | |
| `internal/jobs/` | 2+ (inline) | Own startTestPostgres |

## Tests that stay sequential

### OAuth flow tests (`internal/handler/oauth_flow_test.go`)

`setupGothForTest` modifies a global `goth` provider registry. These tests must NOT use `t.Parallel()`. Already documented in existing comments.

### OTel tests (`internal/drilotel/drilotel_test.go`)

`TestInit_StdoutExporter` and `TestInit_FullRoundtrip` set the global OTel tracer provider. Must stay sequential. Already documented in existing comments.

### Config loading tests (`internal/config/config_test.go`)

These test `config.Load()` itself, which reads from the real OS environment. They must use `t.Setenv` to exercise the production code path. They stay sequential and don't need a container.

## Tests that need no changes

Pure unit tests (no database, no container) are unaffected:

- `internal/ai/`, `internal/auth/`, `internal/billing/`, `internal/coach/`
- `internal/educator/`, `internal/email/`, `internal/evaluation/`
- `internal/interview/` (unit tests), `internal/storage/`

These can optionally add `t.Parallel()` for free speedup but it's not required.

## Expected results

- Handler tests: ~81s -> ~3-5s
- Backend tests: proportional improvement (104 container starts eliminated)
- Total project-wide test time: significant reduction, bottleneck shifts from container overhead to actual test logic
