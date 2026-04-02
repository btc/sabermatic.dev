# Phase 2: River Job Queue — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add River (Postgres-backed job queue) to the Go binary with a working `SendEmail` job using Mailgun, periodic job support, and River UI for admin visibility.

**Architecture:** River client runs inside the same Go process as the HTTP server, sharing the pgxpool. River's own database tables are created via `rivermigrate` at startup (after golang-migrate runs the application schema). The `SendEmail` worker sends transactional emails via Mailgun. River UI is mounted at `/admin/jobs` (no auth middleware yet — that's Phase 3).

**Tech Stack:** River (github.com/riverqueue/river), riverpgxv5 driver, rivermigrate, Mailgun Go SDK v5, testcontainers-go

---

## File Structure

```
drill/
├── cmd/drill/main.go                     # Modify: add River client init, start, stop
├── internal/
│   ├── config/config.go                  # Modify: add EmailConfig (Mailgun)
│   ├── email/
│   │   └── sender.go                     # Create: Mailgun email sender
│   ├── jobs/
│   │   ├── workers.go                    # Create: worker registration
│   │   └── send_email.go                 # Create: SendEmail job args + worker
│   └── handler/
│       └── routes.go                     # Modify: add River UI mount, add River to Backend
├── go.mod                                # Modify: add river, mailgun deps
└── .env.example                          # Modify: add Mailgun vars
```

---

### Task 1: Add River + Mailgun Dependencies

**Files:**
- Modify: `go.mod`

- [ ] **Step 1: Add dependencies**

```bash
cd /Users/btc/Projects/src/drill
go get github.com/riverqueue/river
go get github.com/riverqueue/river/riverdriver/riverpgxv5
go get github.com/riverqueue/river/rivermigrate
go get github.com/mailgun/mailgun-go/v5
```

- [ ] **Step 2: Tidy**

```bash
go mod tidy
```

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "feat(phase2): add River and Mailgun dependencies"
```

---

### Task 2: Email Config + Sender

**Files:**
- Modify: `internal/config/config.go`
- Create: `internal/email/sender.go`
- Create: `internal/email/sender_test.go`

- [ ] **Step 1: Add EmailConfig to config.go**

Add to `internal/config/config.go` — add `Email` field to `Config` struct and a new `EmailConfig` struct:

```go
type Config struct {
	Server   ServerConfig
	Database DatabaseConfig
	LLM      LLMConfig
	Speech   SpeechConfig
	Email    EmailConfig
}

type EmailConfig struct {
	MailgunAPIKey string `env:"MAILGUN_API_KEY,default=test-key"`
	MailgunDomain string `env:"MAILGUN_DOMAIN,default=localhost"`
	FromAddress   string `env:"EMAIL_FROM,default=noreply@drill.dev"`
}
```

Note: Mailgun fields default to test values so the app starts without Mailgun configured. Email sending will log warnings when using test defaults.

- [ ] **Step 2: Write sender test**

Create `internal/email/sender_test.go`:

```go
package email_test

import (
	"context"
	"testing"

	"github.com/btc/drill/internal/email"
	"github.com/stretchr/testify/require"
)

func TestLogSender_Send(t *testing.T) {
	sender := email.NewLogSender()

	err := sender.Send(context.Background(), email.Message{
		To:      "test@example.com",
		Subject: "Test Subject",
		Text:    "Test body",
		HTML:    "<p>Test body</p>",
	})
	require.NoError(t, err)
	require.Equal(t, 1, sender.Count())
	require.Equal(t, "test@example.com", sender.Last().To)
}

func TestMailgunSender_Implements_Sender(t *testing.T) {
	// Compile-time check that MailgunSender implements Sender
	var _ email.Sender = (*email.MailgunSender)(nil)
}
```

- [ ] **Step 3: Run test to verify it fails**

```bash
go test ./internal/email/ -v
```

Expected: compilation error, package doesn't exist.

- [ ] **Step 4: Write sender implementation**

Create `internal/email/sender.go`:

```go
package email

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/mailgun/mailgun-go/v5"
)

// Message represents an email to send.
type Message struct {
	To      string
	Subject string
	Text    string
	HTML    string
}

// Sender sends emails. Implementations: MailgunSender (production), LogSender (testing).
type Sender interface {
	Send(ctx context.Context, msg Message) error
}

// MailgunSender sends emails via the Mailgun API.
type MailgunSender struct {
	mg     *mailgun.MailgunImpl
	domain string
	from   string
}

// NewMailgunSender creates a Sender backed by Mailgun.
func NewMailgunSender(apiKey, domain, from string) *MailgunSender {
	mg := mailgun.NewMailgun(domain, apiKey)
	return &MailgunSender{mg: mg, domain: domain, from: from}
}

func (s *MailgunSender) Send(ctx context.Context, msg Message) error {
	m := s.mg.NewMessage(s.domain, s.from, msg.Subject, msg.Text, msg.To)
	if msg.HTML != "" {
		m.SetHTML(msg.HTML)
	}
	resp, err := s.mg.Send(ctx, m)
	if err != nil {
		return fmt.Errorf("mailgun send to %s: %w", msg.To, err)
	}
	slog.Info("email sent", "to", msg.To, "subject", msg.Subject, "id", resp.ID)
	return nil
}

// LogSender logs emails instead of sending them. Used in tests and development.
type LogSender struct {
	mu       sync.Mutex
	messages []Message
}

func NewLogSender() *LogSender {
	return &LogSender{}
}

func (s *LogSender) Send(_ context.Context, msg Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	slog.Info("email logged (not sent)", "to", msg.To, "subject", msg.Subject)
	s.messages = append(s.messages, msg)
	return nil
}

// Count returns the number of emails logged.
func (s *LogSender) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.messages)
}

// Last returns the most recently logged email.
func (s *LogSender) Last() Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.messages[len(s.messages)-1]
}
```

- [ ] **Step 5: Run tests**

```bash
go test ./internal/email/ -v
```

Expected: both tests PASS.

- [ ] **Step 6: Update .env.example**

Add to `.env.example`:

```
# Email (Mailgun)
MAILGUN_API_KEY=your-mailgun-api-key
MAILGUN_DOMAIN=your-domain.mailgun.org
EMAIL_FROM=noreply@drill.dev
```

- [ ] **Step 7: Commit**

```bash
git add internal/email/ internal/config/config.go .env.example
git commit -m "feat(phase2): add email sender with Mailgun and LogSender for tests"
```

---

### Task 3: SendEmail Job

**Files:**
- Create: `internal/jobs/send_email.go`
- Create: `internal/jobs/workers.go`
- Create: `internal/jobs/send_email_test.go`

- [ ] **Step 1: Write SendEmail test**

Create `internal/jobs/send_email_test.go`:

```go
package jobs_test

import (
	"context"
	"testing"

	"github.com/btc/drill/internal/email"
	"github.com/btc/drill/internal/jobs"
	"github.com/riverqueue/river"
	"github.com/stretchr/testify/require"
)

func TestSendEmailArgs_Kind(t *testing.T) {
	args := jobs.SendEmailArgs{
		To:      "test@example.com",
		Subject: "Hello",
		Text:    "World",
	}
	require.Equal(t, "send_email", args.Kind())
}

func TestSendEmailWorker_Work(t *testing.T) {
	sender := email.NewLogSender()
	worker := &jobs.SendEmailWorker{Sender: sender}

	job := &river.Job[jobs.SendEmailArgs]{
		Args: jobs.SendEmailArgs{
			To:      "user@example.com",
			Subject: "Verify your email",
			Text:    "Click here to verify",
			HTML:    "<a href='#'>Click here</a>",
		},
	}

	err := worker.Work(context.Background(), job)
	require.NoError(t, err)
	require.Equal(t, 1, sender.Count())
	require.Equal(t, "user@example.com", sender.Last().To)
	require.Equal(t, "Verify your email", sender.Last().Subject)
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/jobs/ -v
```

Expected: compilation error.

- [ ] **Step 3: Write SendEmail job**

Create `internal/jobs/send_email.go`:

```go
package jobs

import (
	"context"
	"fmt"

	"github.com/btc/drill/internal/email"
	"github.com/riverqueue/river"
)

// SendEmailArgs are the arguments for the SendEmail job.
type SendEmailArgs struct {
	To      string `json:"to"`
	Subject string `json:"subject"`
	Text    string `json:"text"`
	HTML    string `json:"html,omitempty"`
}

func (args SendEmailArgs) Kind() string {
	return "send_email"
}

// SendEmailWorker processes SendEmail jobs.
type SendEmailWorker struct {
	river.WorkerDefaults[SendEmailArgs]
	Sender email.Sender
}

func (w *SendEmailWorker) Work(ctx context.Context, job *river.Job[SendEmailArgs]) error {
	err := w.Sender.Send(ctx, email.Message{
		To:      job.Args.To,
		Subject: job.Args.Subject,
		Text:    job.Args.Text,
		HTML:    job.Args.HTML,
	})
	if err != nil {
		return fmt.Errorf("send email to %s: %w", job.Args.To, err)
	}
	return nil
}
```

- [ ] **Step 4: Write worker registration**

Create `internal/jobs/workers.go`:

```go
package jobs

import (
	"github.com/btc/drill/internal/email"
	"github.com/riverqueue/river"
)

// RegisterWorkers creates a Workers bundle with all job workers registered.
func RegisterWorkers(sender email.Sender) *river.Workers {
	workers := river.NewWorkers()
	river.AddWorker(workers, &SendEmailWorker{Sender: sender})
	return workers
}
```

- [ ] **Step 5: Run tests**

```bash
go test ./internal/jobs/ -v
```

Expected: both tests PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/jobs/
git commit -m "feat(phase2): add SendEmail River job with Mailgun sender"
```

---

### Task 4: River Client in main.go

**Files:**
- Modify: `cmd/drill/main.go`
- Modify: `internal/handler/routes.go`

- [ ] **Step 1: Add River to Backend struct**

Update `internal/handler/routes.go`:

```go
package handler

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

// Backend holds shared dependencies for all handlers.
type Backend struct {
	Pool  *pgxpool.Pool
	River *river.Client[*river.InsertTx]
}

// RegisterRoutes sets up all HTTP routes on the given mux.
func RegisterRoutes(mux *http.ServeMux, b *Backend) {
	mux.HandleFunc("GET /api/health", Health(b))
}
```

Wait — the River client generic type is tied to the driver. With riverpgxv5 and pgx transactions, the client type is `*river.Client[pgx.Tx]`. But handler code doesn't need to know the transaction type. Let me check...

Actually, for the Backend struct, we only need the River client for enqueueing jobs. The simplest approach: store the pool and use `riverClient.Insert()` or `riverClient.InsertTx()` directly. The Backend doesn't need to hold the River client generically — specific handlers that need to enqueue jobs can receive it.

Simpler: just add `River` as an `any` field or use a concrete type. River's `Client` is generic on the transaction type. With riverpgxv5, the concrete type is `*river.Client[pgx.Tx]`. Let's use that.

Update `internal/handler/routes.go`:

```go
package handler

import (
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

// Backend holds shared dependencies for all handlers.
type Backend struct {
	Pool  *pgxpool.Pool
	River *river.Client[pgx.Tx]
}

// RegisterRoutes sets up all HTTP routes on the given mux.
func RegisterRoutes(mux *http.ServeMux, b *Backend) {
	mux.HandleFunc("GET /api/health", Health(b))
}
```

- [ ] **Step 2: Wire River into main.go**

Update `cmd/drill/main.go` — add River client creation, migration, start, and stop to `runWithContext`. The key changes:

After `runMigrations(cfg.Database.URL)`, add:

```go
// Run River migrations
riverMigrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
if err != nil {
    return fmt.Errorf("create river migrator: %w", err)
}
riverRes, err := riverMigrator.Migrate(ctx, rivermigrate.DirectionUp, nil)
if err != nil {
    return fmt.Errorf("river migrate: %w", err)
}
for _, v := range riverRes.Versions {
    slog.Info("river migration applied", "version", v.Version)
}

// Set up email sender
var emailSender email.Sender
if cfg.Email.MailgunAPIKey == "test-key" {
    slog.Warn("using log email sender (MAILGUN_API_KEY not configured)")
    emailSender = email.NewLogSender()
} else {
    emailSender = email.NewMailgunSender(cfg.Email.MailgunAPIKey, cfg.Email.MailgunDomain, cfg.Email.FromAddress)
}

// Set up River
workers := jobs.RegisterWorkers(emailSender)
riverClient, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
    Queues: map[string]river.QueueConfig{
        river.QueueDefault:  {MaxWorkers: 5},
        "notifications":     {MaxWorkers: 5},
        "ai":                {MaxWorkers: 10},
        "maintenance":       {MaxWorkers: 2},
    },
    Workers: workers,
})
if err != nil {
    return fmt.Errorf("create river client: %w", err)
}
if err := riverClient.Start(ctx); err != nil {
    return fmt.Errorf("start river: %w", err)
}
slog.Info("river started")
```

Before the `select` block, update the Backend:

```go
b := &handler.Backend{Pool: pool, River: riverClient}
```

In the shutdown path (the `ctx.Done()` case), add River stop before server shutdown:

```go
case <-ctx.Done():
    slog.Info("shutting down")
    
    // Stop River first (finish in-flight jobs)
    riverStopCtx, riverStopCancel := context.WithTimeout(context.Background(), 15*time.Second)
    defer riverStopCancel()
    riverClient.Stop(riverStopCtx)
    slog.Info("river stopped")
    
    shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer shutdownCancel()
    return srv.Shutdown(shutdownCtx)
```

Add new imports to main.go:

```go
"github.com/btc/drill/internal/email"
"github.com/btc/drill/internal/jobs"
"github.com/riverqueue/river"
"github.com/riverqueue/river/riverdriver/riverpgxv5"
"github.com/riverqueue/river/rivermigrate"
```

- [ ] **Step 3: Verify it compiles**

```bash
go build ./cmd/drill/
```

- [ ] **Step 4: Update the main_test.go**

The existing `TestFullStartup` test needs to work with the new River initialization. Since River migrations run automatically and the test uses a real Postgres, it should just work. But we need to set the Mailgun env vars (they have defaults, so this should be fine).

Run the test:

```bash
go test ./cmd/drill/ -run TestFullStartup -v -count=1 -timeout 60s
```

Expected: PASS (River starts with log sender, migrations run).

- [ ] **Step 5: Commit**

```bash
git add cmd/drill/main.go internal/handler/routes.go
git commit -m "feat(phase2): wire River client into main.go with email sender"
```

---

### Task 5: River Integration Test — Enqueue and Process

**Files:**
- Create: `internal/jobs/integration_test.go`

- [ ] **Step 1: Write integration test**

Create `internal/jobs/integration_test.go`:

```go
package jobs_test

import (
	"context"
	"testing"
	"time"

	"github.com/btc/drill/internal/email"
	"github.com/btc/drill/internal/jobs"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestSendEmail_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()

	// Start Postgres
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

	pool, err := pgxpool.New(ctx, connStr)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	// Run River migrations
	migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	require.NoError(t, err)
	_, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil)
	require.NoError(t, err)

	// Set up workers with log sender
	logSender := email.NewLogSender()
	workers := jobs.RegisterWorkers(logSender)

	// Create and start River client
	riverClient, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: 5},
			"notifications":   {MaxWorkers: 5},
		},
		Workers: workers,
	})
	require.NoError(t, err)

	err = riverClient.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		riverClient.Stop(stopCtx)
	})

	// Enqueue a SendEmail job
	_, err = riverClient.Insert(ctx, jobs.SendEmailArgs{
		To:      "user@example.com",
		Subject: "Welcome to Drill",
		Text:    "Welcome!",
		HTML:    "<h1>Welcome!</h1>",
	}, nil)
	require.NoError(t, err)

	// Wait for the job to be processed
	require.Eventually(t, func() bool {
		return logSender.Count() >= 1
	}, 10*time.Second, 100*time.Millisecond, "email job was not processed")

	// Verify the email was "sent"
	msg := logSender.Last()
	require.Equal(t, "user@example.com", msg.To)
	require.Equal(t, "Welcome to Drill", msg.Subject)
}
```

- [ ] **Step 2: Run integration test**

```bash
go test ./internal/jobs/ -run TestSendEmail_Integration -v -count=1 -timeout 60s
```

Expected: PASS — River picks up the job and the LogSender records the email.

- [ ] **Step 3: Commit**

```bash
git add internal/jobs/integration_test.go
git commit -m "test(phase2): add River SendEmail integration test"
```

---

### Task 6: River UI Mount

**Files:**
- Modify: `internal/handler/routes.go`
- Modify: `cmd/drill/main.go` (if needed for River UI setup)

River UI is a separate binary/Docker container. For now, we'll add a placeholder admin route that documents where River UI will be mounted. The actual River UI setup (Docker sidecar or embedded) is a Phase 4 (Deployment) concern.

- [ ] **Step 1: Add admin placeholder route**

Add to `internal/handler/routes.go` in `RegisterRoutes`:

```go
mux.HandleFunc("GET /admin/jobs", AdminJobsPlaceholder(b))
```

Create `internal/handler/admin.go`:

```go
package handler

import (
	"encoding/json"
	"net/http"
)

// AdminJobsPlaceholder returns info about where River UI will be available.
// River UI is a separate service mounted in Phase 4 (Deployment).
func AdminJobsPlaceholder(b *Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status":  "placeholder",
			"message": "River UI will be available here after deployment setup",
		})
	}
}
```

- [ ] **Step 2: Commit**

```bash
git add internal/handler/admin.go internal/handler/routes.go
git commit -m "feat(phase2): add admin jobs placeholder route for River UI"
```

---

### Task 7: Final Verification

- [ ] **Step 1: Run all tests**

```bash
go test ./... -count=1 -timeout 120s 2>&1 | grep -E "^(ok|FAIL|---)"
```

Expected: all PASS across cmd/drill, internal/config, internal/email, internal/handler, internal/jobs.

- [ ] **Step 2: Run go vet**

```bash
go vet ./...
```

Expected: no issues.

- [ ] **Step 3: Verify build**

```bash
go build ./cmd/drill/
```

Expected: clean build.

- [ ] **Step 4: Commit if needed**

```bash
git add -A
git status
# Only commit if there are changes
```

---

## Phase 2 Complete

At this point you have:
- River client running inside the Go binary, sharing the pgxpool
- River migrations run at startup (after app migrations)
- 4 queues configured: default, notifications, ai, maintenance
- `SendEmail` job worker backed by Mailgun (or LogSender in dev/test)
- `RegisterWorkers()` function for centralized worker registration
- Email `Sender` interface with Mailgun and Log implementations
- Integration test proving end-to-end: enqueue → River processes → email sent
- Admin jobs placeholder route
- Graceful shutdown: River stops before HTTP server

**Next:** Phase 3 (Auth) — users, OAuth, session cookies, middleware, login/signup UI.
