package handler

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"

	"github.com/btc/drill/internal/config"
	"github.com/btc/drill/internal/email"
	"github.com/btc/drill/internal/jobs"
)

// Backend holds shared dependencies for all handlers and owns their lifecycles.
type Backend struct {
	Pool  *pgxpool.Pool
	River *river.Client[pgx.Tx]
	cfg   *config.Config
}

// NewBackend creates a pool, runs River migrations, and starts the River client.
// App migrations must be run before calling this (schema must exist).
func NewBackend(cfg *config.Config) (*Backend, error) {
	// Pool uses background context — must outlive any request or signal context.
	pool, err := cfg.Database.NewPool(context.Background())
	if err != nil {
		return nil, fmt.Errorf("database: %w", err)
	}
	slog.Info("database connected")

	// River migrations (creates River's internal tables)
	riverMigrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("create river migrator: %w", err)
	}
	riverRes, err := riverMigrator.Migrate(context.Background(), rivermigrate.DirectionUp, nil)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("river migrate: %w", err)
	}
	for _, v := range riverRes.Versions {
		slog.Info("river migration applied", "version", v.Version)
	}

	// River client
	emailSender := email.NewSender(&cfg.Email)
	workers := jobs.RegisterWorkers(cfg, emailSender)
	riverClient, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues: map[string]river.QueueConfig{
			river.QueueDefault:       {MaxWorkers: cfg.River.NumDefaultWorkers},
			jobs.QueueNotifications:  {MaxWorkers: cfg.River.NumNotifyWorkers},
			jobs.QueueAI:             {MaxWorkers: cfg.River.NumAIWorkers},
			jobs.QueueMaintenance:    {MaxWorkers: cfg.River.NumMaintWorkers},
		},
		Workers: workers,
	})
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("create river client: %w", err)
	}
	if err := riverClient.Start(context.Background()); err != nil {
		pool.Close()
		return nil, fmt.Errorf("start river: %w", err)
	}
	slog.Info("river started")

	return &Backend{
		Pool:  pool,
		River: riverClient,
		cfg:   cfg,
	}, nil
}

// Close stops River (finishing in-flight jobs) then closes the database pool.
// Implements io.Closer.
func (b *Backend) Close() error {
	timeout := time.Duration(b.cfg.River.ShutdownTimeoutSec) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := b.River.Stop(ctx); err != nil {
		slog.Warn("river stop error", "error", err)
	}
	slog.Info("river stopped")

	b.Pool.Close()
	slog.Info("database pool closed")
	return nil
}
