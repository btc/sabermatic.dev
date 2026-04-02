# Phase 1: Foundation — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Get a Go binary running that connects to Postgres, runs migrations, seeds questions, generates type-safe DB code via sqlc, loads config from environment, and serves a health check endpoint.

**Architecture:** Single Go binary using `net/http` standard library. pgx/v5 for Postgres, golang-migrate for schema migrations, sqlc for type-safe query generation, sethvargo/go-envconfig for configuration. No frameworks. The binary starts an HTTP server with one endpoint (`GET /api/health`) that checks DB connectivity.

**Tech Stack:** Go 1.23+, pgx/v5, golang-migrate, sqlc, go-envconfig, testcontainers-go, testify

---

## File Structure

```
drill/
├── cmd/drill/main.go                 # Entrypoint: load config, connect DB, migrate, start server
├── internal/
│   ├── config/
│   │   └── config.go                 # Config structs + go-envconfig loading
│   ├── db/                           # sqlc generated code (do not edit)
│   │   ├── db.go
│   │   ├── models.go
│   │   └── queries.sql.go
│   └── handler/
│       ├── routes.go                 # Route registration
│       └── health.go                 # GET /api/health handler
├── sql/
│   ├── migrations/
│   │   ├── 001_initial.up.sql        # Full schema: all tables + indexes
│   │   └── 001_initial.down.sql      # Drop all tables
│   └── queries/
│       └── health.sql                # Health check query
├── sqlc.yaml                         # sqlc configuration
├── go.mod
├── go.sum
├── .env.example                      # Template for environment variables
└── seed/
    └── questions.sql                 # 18 seed questions as INSERT statements
```

---

### Task 1: Go Module + Dependencies

**Files:**
- Create: `go.mod`
- Create: `go.sum` (auto-generated)

- [ ] **Step 1: Initialize Go module**

```bash
cd /Users/btc/Projects/src/drill
go mod init github.com/btc/drill
```

- [ ] **Step 2: Add dependencies**

```bash
go get github.com/jackc/pgx/v5
go get github.com/jackc/pgx/v5/pgxpool
go get github.com/golang-migrate/migrate/v4
go get github.com/golang-migrate/migrate/v4/database/pgx/v5
go get github.com/golang-migrate/migrate/v4/source/iofs
go get github.com/sethvargo/go-envconfig
go get github.com/google/uuid
go get github.com/stretchr/testify
go get github.com/testcontainers/testcontainers-go
go get github.com/testcontainers/testcontainers-go/modules/postgres
```

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "feat(phase1): initialize Go module with dependencies"
```

---

### Task 2: Configuration

**Files:**
- Create: `internal/config/config.go`
- Create: `.env.example`

- [ ] **Step 1: Write config test**

Create `internal/config/config_test.go`:

```go
package config_test

import (
	"testing"

	"github.com/btc/drill/internal/config"
	"github.com/stretchr/testify/require"
)

func TestLoadConfig_Defaults(t *testing.T) {
	// Set only required fields
	t.Setenv("DATABASE_URL", "postgres://localhost:5432/drill")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	t.Setenv("OPENAI_API_KEY", "sk-test")

	cfg, err := config.Load()
	require.NoError(t, err)

	require.Equal(t, "postgres://localhost:5432/drill", cfg.Database.URL)
	require.Equal(t, "sk-ant-test", cfg.LLM.APIKey)
	require.Equal(t, "claude-sonnet-4-20250514", cfg.LLM.InterviewerModel)
	require.Equal(t, "claude-sonnet-4-20250514", cfg.LLM.EvaluatorModel)
	require.Equal(t, "claude-sonnet-4-20250514", cfg.LLM.EducatorModel)
	require.Equal(t, "claude-sonnet-4-20250514", cfg.LLM.CoachModel)
	require.Equal(t, 8080, cfg.Server.Port)
}

func TestLoadConfig_MissingRequired(t *testing.T) {
	// Clear all env vars that might be set
	t.Setenv("DATABASE_URL", "")
	t.Setenv("ANTHROPIC_API_KEY", "")

	_, err := config.Load()
	require.Error(t, err)
}

func TestLoadConfig_Override(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://db:5432/drill")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	t.Setenv("OPENAI_API_KEY", "sk-test")
	t.Setenv("SERVER_PORT", "9090")
	t.Setenv("INTERVIEWER_MODEL", "claude-opus-4-6")

	cfg, err := config.Load()
	require.NoError(t, err)

	require.Equal(t, 9090, cfg.Server.Port)
	require.Equal(t, "claude-opus-4-6", cfg.LLM.InterviewerModel)
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd /Users/btc/Projects/src/drill
go test ./internal/config/ -v
```

Expected: compilation error, `config` package doesn't exist.

- [ ] **Step 3: Write config implementation**

Create `internal/config/config.go`:

```go
package config

import (
	"context"
	"fmt"

	"github.com/sethvargo/go-envconfig"
)

type Config struct {
	Server   ServerConfig
	Database DatabaseConfig
	LLM      LLMConfig
	Speech   SpeechConfig
}

type ServerConfig struct {
	Port int `env:"SERVER_PORT,default=8080"`
}

type DatabaseConfig struct {
	URL         string `env:"DATABASE_URL,required"`
	MaxPoolSize int    `env:"DATABASE_MAX_POOL_SIZE,default=5"`
}

type LLMConfig struct {
	APIKey           string `env:"ANTHROPIC_API_KEY,required"`
	InterviewerModel string `env:"INTERVIEWER_MODEL,default=claude-sonnet-4-20250514"`
	EvaluatorModel   string `env:"EVALUATOR_MODEL,default=claude-sonnet-4-20250514"`
	EducatorModel    string `env:"EDUCATOR_MODEL,default=claude-sonnet-4-20250514"`
	CoachModel       string `env:"COACH_MODEL,default=claude-sonnet-4-20250514"`
}

type SpeechConfig struct {
	OpenAIAPIKey string `env:"OPENAI_API_KEY,required"`
	TTSVoice     string `env:"TTS_VOICE,default=onyx"`
	TTSModel     string `env:"TTS_MODEL,default=tts-1"`
	WhisperModel string `env:"WHISPER_MODEL,default=whisper-1"`
}

func Load() (*Config, error) {
	var cfg Config
	if err := envconfig.Process(context.Background(), &cfg); err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	return &cfg, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/config/ -v
```

Expected: all 3 tests PASS.

- [ ] **Step 5: Create .env.example**

Create `.env.example`:

```
# Required
DATABASE_URL=postgres://localhost:5432/drill
ANTHROPIC_API_KEY=sk-ant-...
OPENAI_API_KEY=sk-...

# Server
SERVER_PORT=8080

# Models (defaults shown)
INTERVIEWER_MODEL=claude-sonnet-4-20250514
EVALUATOR_MODEL=claude-sonnet-4-20250514
EDUCATOR_MODEL=claude-sonnet-4-20250514
COACH_MODEL=claude-sonnet-4-20250514

# Speech (defaults shown)
TTS_VOICE=onyx
TTS_MODEL=tts-1
WHISPER_MODEL=whisper-1

# Database pool
DATABASE_MAX_POOL_SIZE=5
```

- [ ] **Step 6: Commit**

```bash
git add internal/config/ .env.example
git commit -m "feat(phase1): add config loading with go-envconfig"
```

---

### Task 3: Database Migration — Full Schema

**Files:**
- Create: `sql/migrations/001_initial.up.sql`
- Create: `sql/migrations/001_initial.down.sql`

- [ ] **Step 1: Write the up migration**

Create `sql/migrations/001_initial.up.sql`:

```sql
-- 001_initial.up.sql: Full schema for Drill v1

-- Auth tables

CREATE TABLE users (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email           TEXT UNIQUE NOT NULL,
    email_verified  BOOLEAN NOT NULL DEFAULT FALSE,
    password_hash   TEXT,
    display_name    TEXT NOT NULL,
    role            TEXT NOT NULL DEFAULT 'candidate'
                    CHECK (role IN ('candidate', 'admin')),
    stripe_customer_id TEXT,
    plan            TEXT NOT NULL DEFAULT 'free',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at      TIMESTAMPTZ
);

CREATE TABLE oauth_accounts (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id),
    provider    TEXT NOT NULL,
    provider_id TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (provider, provider_id)
);

CREATE TABLE auth_sessions (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      UUID NOT NULL REFERENCES users(id),
    token_hash   TEXT UNIQUE NOT NULL,
    expires_at   TIMESTAMPTZ NOT NULL,
    last_active  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ip_address   INET,
    user_agent   TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Core tables

CREATE TABLE questions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID REFERENCES users(id),
    title           TEXT NOT NULL,
    prompt          TEXT NOT NULL,
    difficulty      TEXT NOT NULL CHECK (difficulty IN ('medium', 'hard')),
    tags            TEXT[] NOT NULL DEFAULT '{}',
    hints           TEXT,
    source          TEXT NOT NULL CHECK (source IN ('seed', 'custom', 'coach_generated')),
    coach_rationale TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE interview_sessions (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id                 UUID NOT NULL REFERENCES users(id),
    question_id             UUID NOT NULL REFERENCES questions(id),
    status                  TEXT NOT NULL DEFAULT 'active'
                            CHECK (status IN (
                                'active','completed','evaluating',
                                'reviewed','evaluation_failed'
                            )),
    config_duration_minutes INT NOT NULL,
    config_tts_enabled      BOOLEAN NOT NULL DEFAULT FALSE,
    config_coach_briefing   BOOLEAN NOT NULL DEFAULT FALSE,
    started_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ended_at                TIMESTAMPTZ,
    turn_count              INT NOT NULL DEFAULT 0,
    archived                BOOLEAN NOT NULL DEFAULT FALSE,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE messages (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id   UUID NOT NULL REFERENCES interview_sessions(id),
    seq          INT NOT NULL,
    role         TEXT NOT NULL CHECK (role IN ('interviewer', 'candidate')),
    content      TEXT NOT NULL,
    input_method TEXT,
    audio_url    TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (session_id, seq)
);

CREATE TABLE evaluations (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id          UUID UNIQUE NOT NULL REFERENCES interview_sessions(id),
    score_requirements  INT NOT NULL CHECK (score_requirements BETWEEN 1 AND 5),
    score_architecture  INT NOT NULL CHECK (score_architecture BETWEEN 1 AND 5),
    score_deep_dive     INT NOT NULL CHECK (score_deep_dive BETWEEN 1 AND 5),
    score_scalability   INT NOT NULL CHECK (score_scalability BETWEEN 1 AND 5),
    score_communication INT NOT NULL CHECK (score_communication BETWEEN 1 AND 5),
    score_overall       INT NOT NULL CHECK (score_overall BETWEEN 1 AND 5),
    strengths           JSONB NOT NULL,
    gaps                JSONB NOT NULL,
    advice              TEXT NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE annotations (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    evaluation_id   UUID NOT NULL REFERENCES evaluations(id),
    message_id      UUID NOT NULL REFERENCES messages(id),
    annotation_type TEXT NOT NULL
                    CHECK (annotation_type IN (
                        'strength','gap','missed_opportunity','note'
                    )),
    content         TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE educator_analyses (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id      UUID UNIQUE NOT NULL REFERENCES interview_sessions(id),
    status          TEXT NOT NULL DEFAULT 'generating'
                    CHECK (status IN ('generating', 'completed')),
    model_answer    TEXT,
    gap_deep_dives  TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE coach_analyses (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id               UUID NOT NULL REFERENCES users(id),
    narrative             TEXT NOT NULL,
    weakest_dimension     TEXT,
    improving_dimensions  TEXT[],
    topic_gaps            TEXT[],
    suggested_question_id UUID REFERENCES questions(id),
    sessions_analyzed     UUID[] NOT NULL,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Observability & billing tables

CREATE TABLE llm_calls (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id     UUID REFERENCES interview_sessions(id),
    user_id        UUID NOT NULL REFERENCES users(id),
    role           TEXT NOT NULL,
    model          TEXT NOT NULL,
    input_tokens   INT NOT NULL,
    output_tokens  INT NOT NULL,
    estimated_cost NUMERIC(10,6) NOT NULL,
    latency_ms     INT NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE llm_call_content (
    llm_call_id UUID PRIMARY KEY REFERENCES llm_calls(id),
    prompt      JSONB NOT NULL,
    response    JSONB NOT NULL
);

CREATE TABLE user_events (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL REFERENCES users(id),
    session_id UUID REFERENCES interview_sessions(id),
    event_type TEXT NOT NULL,
    metadata   JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE usage_periods (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID NOT NULL REFERENCES users(id),
    period_start  TIMESTAMPTZ NOT NULL,
    period_end    TIMESTAMPTZ NOT NULL,
    sessions_used INT NOT NULL DEFAULT 0,
    UNIQUE (user_id, period_start)
);

-- Indexes

CREATE INDEX idx_sessions_user_status ON interview_sessions(user_id, status)
    WHERE archived = FALSE;
CREATE INDEX idx_sessions_user_archived ON interview_sessions(user_id, archived);
CREATE INDEX idx_sessions_question ON interview_sessions(question_id);
CREATE INDEX idx_messages_session_seq ON messages(session_id, seq);
CREATE INDEX idx_annotations_evaluation ON annotations(evaluation_id);
CREATE INDEX idx_annotations_message ON annotations(message_id);
CREATE INDEX idx_llm_calls_session ON llm_calls(session_id);
CREATE INDEX idx_llm_calls_user_created ON llm_calls(user_id, created_at);
CREATE INDEX idx_user_events_user_created ON user_events(user_id, created_at);
CREATE INDEX idx_questions_user ON questions(user_id) WHERE user_id IS NOT NULL;
CREATE INDEX idx_questions_seed ON questions(id) WHERE source = 'seed';
CREATE INDEX idx_usage_periods_user ON usage_periods(user_id, period_start);
CREATE INDEX idx_coach_analyses_user ON coach_analyses(user_id, created_at DESC);
```

- [ ] **Step 2: Write the down migration**

Create `sql/migrations/001_initial.down.sql`:

```sql
-- Drop in reverse dependency order
DROP TABLE IF EXISTS usage_periods;
DROP TABLE IF EXISTS user_events;
DROP TABLE IF EXISTS llm_call_content;
DROP TABLE IF EXISTS llm_calls;
DROP TABLE IF EXISTS coach_analyses;
DROP TABLE IF EXISTS educator_analyses;
DROP TABLE IF EXISTS annotations;
DROP TABLE IF EXISTS evaluations;
DROP TABLE IF EXISTS messages;
DROP TABLE IF EXISTS interview_sessions;
DROP TABLE IF EXISTS questions;
DROP TABLE IF EXISTS auth_sessions;
DROP TABLE IF EXISTS oauth_accounts;
DROP TABLE IF EXISTS users;
```

- [ ] **Step 3: Commit**

```bash
git add sql/
git commit -m "feat(phase1): add initial database migration with full schema"
```

---

### Task 4: sqlc Configuration + Health Query

**Files:**
- Create: `sqlc.yaml`
- Create: `sql/queries/health.sql`

- [ ] **Step 1: Create sqlc.yaml**

```yaml
version: "2"
sql:
  - engine: "postgresql"
    queries: "sql/queries/"
    schema: "sql/migrations/"
    gen:
      go:
        package: "db"
        out: "internal/db"
        sql_package: "pgx/v5"
        emit_json_tags: true
        emit_interface: true
        overrides:
          - db_type: "uuid"
            go_type:
              import: "github.com/google/uuid"
              type: "UUID"
          - db_type: "timestamptz"
            go_type: "time.Time"
          - db_type: "pg_catalog.inet"
            go_type: "string"
```

- [ ] **Step 2: Create health check query**

Create `sql/queries/health.sql`:

```sql
-- name: HealthCheck :one
SELECT 1 AS ok;
```

- [ ] **Step 3: Install sqlc and generate**

```bash
go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
cd /Users/btc/Projects/src/drill
sqlc generate
```

Expected: `internal/db/` directory created with `db.go`, `models.go`, `health.sql.go`.

- [ ] **Step 4: Verify generated code compiles**

```bash
go build ./internal/db/
```

Expected: no errors.

- [ ] **Step 5: Commit**

```bash
git add sqlc.yaml sql/queries/ internal/db/
git commit -m "feat(phase1): add sqlc config and generate type-safe DB code"
```

---

### Task 5: Health Check Handler

**Files:**
- Create: `internal/handler/health.go`
- Create: `internal/handler/routes.go`
- Create: `internal/handler/health_test.go`

- [ ] **Step 1: Write health handler test**

Create `internal/handler/health_test.go`:

```go
package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/btc/drill/internal/handler"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

func TestHealthCheck_Healthy(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupTestDB(t)
	b := &handler.Backend{Pool: pool}
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux, b)

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var body map[string]any
	err := json.Unmarshal(w.Body.Bytes(), &body)
	require.NoError(t, err)
	require.Equal(t, "ok", body["status"])
	require.Equal(t, true, body["db"])
}

// setupTestDB creates a real Postgres connection for integration tests.
// Uses testcontainers if DATABASE_URL is not set.
func setupTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()

	ctx := context.Background()

	// Use testcontainers for a real Postgres
	pgContainer, err := setupPostgresContainer(ctx)
	if err != nil {
		t.Fatalf("failed to start postgres container: %v", err)
	}
	t.Cleanup(func() { pgContainer.Terminate(ctx) })

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("failed to get connection string: %v", err)
	}

	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}
	t.Cleanup(pool.Close)

	// Run migrations
	runMigrations(t, connStr)

	return pool
}
```

Create `internal/handler/testutil_test.go`:

```go
package handler_test

import (
	"context"
	"embed"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"time"
)

//go:embed testdata/migrations/*.sql
var testMigrations embed.FS

func setupPostgresContainer(ctx context.Context) (*postgres.PostgresContainer, error) {
	return postgres.Run(ctx,
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
}

func runMigrations(t *testing.T, connStr string) {
	t.Helper()

	d, err := iofs.New(testMigrations, "testdata/migrations")
	if err != nil {
		t.Fatalf("failed to create migration source: %v", err)
	}

	m, err := migrate.NewWithSourceInstance("iofs", d, "pgx5://"+connStr[len("postgres://"):])
	if err != nil {
		t.Fatalf("failed to create migrate instance: %v", err)
	}

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("failed to run migrations: %v", err)
	}
}
```

- [ ] **Step 2: Symlink migrations for tests**

The test embeds migrations from a local `testdata/` directory. Symlink the real migrations:

```bash
mkdir -p internal/handler/testdata
ln -s ../../../../sql/migrations internal/handler/testdata/migrations
```

- [ ] **Step 3: Run test to verify it fails**

```bash
go test ./internal/handler/ -v -count=1
```

Expected: compilation error, `handler` package doesn't exist.

- [ ] **Step 4: Write Backend struct and routes**

Create `internal/handler/routes.go`:

```go
package handler

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Backend holds shared dependencies for all handlers.
type Backend struct {
	Pool *pgxpool.Pool
}

// RegisterRoutes sets up all HTTP routes on the given mux.
func RegisterRoutes(mux *http.ServeMux, b *Backend) {
	mux.HandleFunc("GET /api/health", Health(b))
}
```

- [ ] **Step 5: Write health handler**

Create `internal/handler/health.go`:

```go
package handler

import (
	"encoding/json"
	"net/http"
)

// Health returns a handler that checks database connectivity.
func Health(b *Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		dbOK := true
		if err := b.Pool.Ping(ctx); err != nil {
			dbOK = false
		}

		status := "ok"
		httpStatus := http.StatusOK
		if !dbOK {
			status = "degraded"
			httpStatus = http.StatusServiceUnavailable
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(httpStatus)
		json.NewEncoder(w).Encode(map[string]any{
			"status": status,
			"db":     dbOK,
		})
	}
}
```

- [ ] **Step 6: Run test to verify it passes**

```bash
go test ./internal/handler/ -v -count=1
```

Expected: `TestHealthCheck_Healthy` PASS. (Requires Docker running for testcontainers.)

- [ ] **Step 7: Commit**

```bash
git add internal/handler/
git commit -m "feat(phase1): add health check handler with integration test"
```

---

### Task 6: Main Entrypoint with Migration Runner

**Files:**
- Create: `cmd/drill/main.go`

- [ ] **Step 1: Write main.go**

Create `cmd/drill/main.go`:

```go
package main

import (
	"context"
	"embed"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/btc/drill/internal/config"
	"github.com/btc/drill/internal/handler"
)

//go:embed migrations
var migrations embed.FS

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	if err := run(); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	// Load config
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Connect to database
	poolCfg, err := pgxpool.ParseConfig(cfg.Database.URL)
	if err != nil {
		return fmt.Errorf("parse database url: %w", err)
	}
	poolCfg.MaxConns = int32(cfg.Database.MaxPoolSize)

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return fmt.Errorf("create pool: %w", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}
	slog.Info("database connected", "url", cfg.Database.URL)

	// Run migrations
	if err := runMigrations(cfg.Database.URL); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	// Set up HTTP server
	b := &handler.Backend{Pool: pool}
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux, b)

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Server.Port),
		Handler: mux,
	}

	// Start server in goroutine
	errCh := make(chan error, 1)
	go func() {
		slog.Info("server starting", "port", cfg.Server.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	// Wait for shutdown signal or server error
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		slog.Info("shutting down")
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer shutdownCancel()
		return srv.Shutdown(shutdownCtx)
	}
}

func runMigrations(databaseURL string) error {
	d, err := iofs.New(migrations, "migrations")
	if err != nil {
		return fmt.Errorf("create migration source: %w", err)
	}

	// golang-migrate expects pgx5:// scheme for pgx/v5 driver
	pgxURL := "pgx5://" + databaseURL[len("postgres://"):]
	m, err := migrate.NewWithSourceInstance("iofs", d, pgxURL)
	if err != nil {
		return fmt.Errorf("create migrate: %w", err)
	}

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("migrate up: %w", err)
	}

	version, dirty, _ := m.Version()
	slog.Info("migrations complete", "version", version, "dirty", dirty)
	return nil
}
```

- [ ] **Step 2: Symlink migrations into cmd/drill for embed**

The `//go:embed` directive requires the files to be in or below the package directory:

```bash
ln -s ../../sql/migrations cmd/drill/migrations
```

- [ ] **Step 3: Verify it compiles**

```bash
go build ./cmd/drill/
```

Expected: produces a `drill` binary (or no output = success).

- [ ] **Step 4: Test locally against a real Postgres**

Start a local Postgres (if not running):

```bash
docker run -d --name drill-pg -e POSTGRES_DB=drill -e POSTGRES_PASSWORD=drill -p 5432:5432 postgres:16-alpine
```

Run the binary:

```bash
DATABASE_URL=postgres://postgres:drill@localhost:5432/drill ANTHROPIC_API_KEY=sk-test OPENAI_API_KEY=sk-test ./drill
```

Expected output:
```
{"level":"INFO","msg":"database connected",...}
{"level":"INFO","msg":"migrations complete","version":1,"dirty":false}
{"level":"INFO","msg":"server starting","port":8080}
```

Test the health endpoint:

```bash
curl http://localhost:8080/api/health
```

Expected: `{"db":true,"status":"ok"}`

Kill the server with Ctrl+C. Clean up the test container:

```bash
docker rm -f drill-pg
```

- [ ] **Step 5: Commit**

```bash
git add cmd/drill/
git commit -m "feat(phase1): add main entrypoint with migration runner and graceful shutdown"
```

---

### Task 7: Seed Questions

**Files:**
- Create: `seed/questions.sql`
- Create: `sql/queries/questions.sql`

- [ ] **Step 1: Create seed questions SQL**

Create `seed/questions.sql` with the 18 questions from the v0 prototype. Each question uses `source = 'seed'` and `user_id = NULL` (global):

```sql
-- Seed questions for system design interview practice.
-- These are global (user_id IS NULL) and source = 'seed'.
-- Run manually or via a seed command: psql $DATABASE_URL -f seed/questions.sql

INSERT INTO questions (title, prompt, difficulty, tags, hints, source) VALUES
('URL Shortener', 'Design a URL shortening service.', 'medium',
 ARRAY['read-heavy','hashing','storage','web'],
 'How do you generate short URLs? Hash collisions? Read vs write ratio and caching strategy. Analytics and click tracking. Custom short URLs and expiration.', 'seed'),
('News Feed', 'Design a social media news feed.', 'hard',
 ARRAY['read-heavy','fanout','caching','ranking','social'],
 'Push vs pull model for feed generation. Ranking and relevance algorithms. Celebrity/influencer problem (fanout). Real-time updates vs polling.', 'seed'),
('Chat System', 'Design a real-time chat application.', 'medium',
 ARRAY['real-time','websocket','messaging','presence'],
 '1:1 vs group chat differences. Online presence and typing indicators. Message delivery guarantees and ordering. Offline message handling.', 'seed'),
('Rate Limiter', 'Design a rate limiting service.', 'medium',
 ARRAY['distributed','algorithms','middleware','reliability'],
 'Token bucket vs sliding window vs fixed window. Distributed rate limiting across multiple servers. Different rate limit tiers. Client identification.', 'seed'),
('Web Crawler', 'Design a web crawler.', 'hard',
 ARRAY['distributed','storage','graph','deduplication'],
 'URL frontier and prioritization. Politeness and robots.txt. Deduplication of content. Distributed crawling coordination.', 'seed'),
('Notification System', 'Design a notification delivery system.', 'medium',
 ARRAY['distributed','messaging','push','email'],
 'Multiple channels (push, email, SMS, in-app). Delivery guarantees and retry. User preferences and throttling. Template management.', 'seed'),
('Video Streaming', 'Design a video streaming platform.', 'hard',
 ARRAY['streaming','cdn','encoding','storage'],
 'Video encoding and adaptive bitrate. CDN and edge caching. Upload pipeline and processing. Live vs on-demand streaming.', 'seed'),
('Search Autocomplete', 'Design a search autocomplete system.', 'medium',
 ARRAY['search','trie','caching','ranking'],
 'Trie vs other data structures. Ranking suggestions by popularity. Personalization. Handling misspellings.', 'seed'),
('Distributed Cache', 'Design a distributed caching system.', 'hard',
 ARRAY['distributed','caching','consistency','partitioning'],
 'Consistent hashing. Cache eviction policies. Cache invalidation strategies. Replication and failover.', 'seed'),
('File Storage', 'Design a cloud file storage service like Dropbox.', 'hard',
 ARRAY['storage','sync','deduplication','distributed'],
 'File chunking and deduplication. Sync conflict resolution. Metadata vs content storage separation. Sharing and permissions.', 'seed'),
('Ride Sharing', 'Design a ride-sharing service like Uber.', 'hard',
 ARRAY['real-time','geospatial','matching','pricing'],
 'Driver-rider matching algorithm. Real-time location tracking. Surge pricing. ETA calculation.', 'seed'),
('Task Queue', 'Design a distributed task queue.', 'medium',
 ARRAY['distributed','queue','reliability','scheduling'],
 'At-least-once vs exactly-once delivery. Priority queues. Dead letter queues. Delayed/scheduled tasks.', 'seed'),
('E-Commerce', 'Design an e-commerce platform.', 'hard',
 ARRAY['transactions','inventory','search','payments'],
 'Inventory management and race conditions. Product search and filtering. Shopping cart. Order processing pipeline.', 'seed'),
('Metrics System', 'Design a metrics collection and alerting system.', 'hard',
 ARRAY['time-series','aggregation','alerting','streaming'],
 'Time-series data storage. Aggregation at different granularities. Alert evaluation engine. High cardinality labels.', 'seed'),
('Key-Value Store', 'Design a distributed key-value store.', 'medium',
 ARRAY['distributed','storage','consistency','replication'],
 'Partitioning strategy. Replication and consistency model. Read/write paths. Failure detection and recovery.', 'seed'),
('Social Graph', 'Design a social graph service.', 'medium',
 ARRAY['graph','social','caching','relationships'],
 'Friend recommendations. Graph traversal for mutual friends. Fan-out for activity feeds. Privacy controls.', 'seed'),
('Payment System', 'Design a payment processing system.', 'hard',
 ARRAY['transactions','reliability','security','ledger'],
 'Idempotency in payment processing. Double-entry bookkeeping. Payment gateway integration. Fraud detection.', 'seed'),
('Content Moderation', 'Design a content moderation system.', 'medium',
 ARRAY['ml','queue','review','policy'],
 'Automated vs human review pipeline. Priority and escalation. Appeals process. Policy versioning.', 'seed')
ON CONFLICT DO NOTHING;
```

- [ ] **Step 2: Create questions query for listing**

Create `sql/queries/questions.sql`:

```sql
-- name: ListSeedQuestions :many
SELECT id, title, prompt, difficulty, tags, hints, source, created_at
FROM questions
WHERE source = 'seed' AND user_id IS NULL
ORDER BY created_at;

-- name: GetQuestion :one
SELECT id, user_id, title, prompt, difficulty, tags, hints, source, coach_rationale, created_at, updated_at
FROM questions
WHERE id = $1;

-- name: CountSeedQuestions :one
SELECT COUNT(*) FROM questions WHERE source = 'seed' AND user_id IS NULL;
```

- [ ] **Step 3: Regenerate sqlc**

```bash
sqlc generate
```

- [ ] **Step 4: Verify generated code compiles**

```bash
go build ./internal/db/
```

- [ ] **Step 5: Write seed integration test**

Create `internal/handler/seed_test.go`:

```go
package handler_test

import (
	"context"
	"os"
	"os/exec"
	"testing"

	"github.com/btc/drill/internal/db"
	"github.com/stretchr/testify/require"
)

func TestSeedQuestions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupTestDB(t)
	ctx := context.Background()

	// Get the connection string for psql
	connStr := pool.Config().ConnString()

	// Run the seed file
	cmd := exec.Command("psql", connStr, "-f", "../../seed/questions.sql")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	require.NoError(t, err)

	// Verify questions were inserted
	queries := db.New(pool)
	count, err := queries.CountSeedQuestions(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(18), count)

	// Verify idempotency (run again, same count)
	cmd = exec.Command("psql", connStr, "-f", "../../seed/questions.sql")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	require.NoError(t, err)

	count, err = queries.CountSeedQuestions(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(18), count)
}
```

- [ ] **Step 6: Run test**

```bash
go test ./internal/handler/ -run TestSeedQuestions -v -count=1
```

Expected: PASS (18 seed questions inserted, idempotent on re-run).

- [ ] **Step 7: Commit**

```bash
git add seed/ sql/queries/questions.sql internal/db/ internal/handler/seed_test.go
git commit -m "feat(phase1): add 18 seed questions and sqlc queries"
```

---

### Task 8: Final Integration — Full Startup Test

**Files:**
- Create: `cmd/drill/main_test.go`

- [ ] **Step 1: Write startup integration test**

Create `cmd/drill/main_test.go`:

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestFullStartup(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()

	// Start Postgres container
	pgContainer, err := postgres.Run(ctx,
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
	require.NoError(t, err)
	t.Cleanup(func() { pgContainer.Terminate(ctx) })

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	// Set env vars for the config
	t.Setenv("DATABASE_URL", connStr)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	t.Setenv("OPENAI_API_KEY", "sk-test")
	t.Setenv("SERVER_PORT", "0") // Will need to pick a free port

	// Use a free port
	port := 18080 + time.Now().UnixNano()%1000
	t.Setenv("SERVER_PORT", fmt.Sprintf("%d", port))

	// Run the server in a goroutine with a cancel context
	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- runWithContext(runCtx)
	}()

	// Wait for server to be ready
	healthURL := fmt.Sprintf("http://localhost:%d/api/health", port)
	require.Eventually(t, func() bool {
		resp, err := http.Get(healthURL)
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 10*time.Second, 100*time.Millisecond, "server did not become healthy")

	// Check health response
	resp, err := http.Get(healthURL)
	require.NoError(t, err)
	defer resp.Body.Close()

	var body map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Equal(t, "ok", body["status"])
	require.Equal(t, true, body["db"])

	// Shutdown
	runCancel()
}
```

- [ ] **Step 2: Refactor main.go to support testable startup**

Add a `runWithContext` function to `cmd/drill/main.go` that accepts a context (so the test can cancel it). Replace the body of `run()` to call `runWithContext`:

Add this function to `cmd/drill/main.go`:

```go
func runWithContext(ctx context.Context) error {
	// Load config
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Connect to database
	poolCfg, err := pgxpool.ParseConfig(cfg.Database.URL)
	if err != nil {
		return fmt.Errorf("parse database url: %w", err)
	}
	poolCfg.MaxConns = int32(cfg.Database.MaxPoolSize)

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return fmt.Errorf("create pool: %w", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}
	slog.Info("database connected", "url", cfg.Database.URL)

	// Run migrations
	if err := runMigrations(cfg.Database.URL); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	// Set up HTTP server
	b := &handler.Backend{Pool: pool}
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux, b)

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Server.Port),
		Handler: mux,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("server starting", "port", cfg.Server.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		slog.Info("shutting down")
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer shutdownCancel()
		return srv.Shutdown(shutdownCtx)
	}
}
```

Update `run()` to use it:

```go
func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	return runWithContext(ctx)
}
```

- [ ] **Step 3: Run the test**

```bash
go test ./cmd/drill/ -run TestFullStartup -v -count=1 -timeout 60s
```

Expected: PASS. Server starts, migrations run, health check returns ok, clean shutdown.

- [ ] **Step 4: Run all tests**

```bash
go test ./... -v -count=1 -timeout 120s
```

Expected: all tests PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/drill/ internal/
git commit -m "feat(phase1): add full startup integration test"
```

---

### Task 9: Clean Up and Final Verification

- [ ] **Step 1: Verify the full project builds**

```bash
cd /Users/btc/Projects/src/drill
go build ./...
```

- [ ] **Step 2: Run go vet**

```bash
go vet ./...
```

Expected: no issues.

- [ ] **Step 3: Run all tests one final time**

```bash
go test ./... -v -count=1 -timeout 120s
```

Expected: all PASS.

- [ ] **Step 4: Verify project structure**

```bash
find . -not -path './v0/*' -not -path './.git/*' -not -path './docs/*' -not -name '*.sum' -type f | sort
```

Expected structure:
```
./.env.example
./cmd/drill/main.go
./cmd/drill/main_test.go
./cmd/drill/migrations -> ../../sql/migrations
./go.mod
./internal/config/config.go
./internal/config/config_test.go
./internal/db/db.go
./internal/db/models.go
./internal/db/queries.sql.go (or similar)
./internal/handler/health.go
./internal/handler/health_test.go
./internal/handler/routes.go
./internal/handler/seed_test.go
./internal/handler/testdata/migrations -> ../../../../sql/migrations
./internal/handler/testutil_test.go
./seed/questions.sql
./sql/migrations/001_initial.down.sql
./sql/migrations/001_initial.up.sql
./sql/queries/health.sql
./sql/queries/questions.sql
./sqlc.yaml
```

- [ ] **Step 5: Final commit**

```bash
git add -A
git commit -m "chore(phase1): clean up and verify Phase 1 Foundation complete"
```

---

## Phase 1 Complete

At this point you have:
- Go binary that starts an HTTP server
- Loads config from environment variables
- Connects to Postgres
- Runs migrations (full schema with all 14 tables + indexes)
- Serves `GET /api/health` checking DB connectivity
- 18 seed questions ready to insert
- Type-safe DB access via sqlc
- Integration tests using testcontainers (real Postgres)
- Graceful shutdown on SIGTERM

**Next:** Phase 2 (Auth) — users, OAuth, session cookies, middleware, login/signup UI.
