package jobs_test

import (
	"context"
	"testing"
	"time"

	"github.com/btc/drill/internal/config"
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

	// Run River migrations (River needs its own tables)
	migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	require.NoError(t, err)
	_, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil)
	require.NoError(t, err)

	// Load config with defaults (required env vars set for test)
	t.Setenv("DATABASE_URL", connStr)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	t.Setenv("OPENAI_API_KEY", "sk-test")
	t.Setenv("AUTH_TOKEN_SECRET", "test-secret-at-least-32-bytes-long")
	cfg, err := config.Load()
	require.NoError(t, err)

	// Set up workers with log sender
	logSender := email.NewLogSender()
	workers, _ := jobs.RegisterWorkers(cfg, logSender, pool, nil)

	// Create and start River client
	riverClient, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues: map[string]river.QueueConfig{
			river.QueueDefault:      {MaxWorkers: 5},
			jobs.QueueNotifications: {MaxWorkers: 5},
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
	}, jobs.SendEmailInsertOpts(&cfg.Email))
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
