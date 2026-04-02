package backend

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"

	"github.com/btc/drill/internal/config"
	"github.com/btc/drill/internal/email"
	"github.com/btc/drill/internal/jobs"
)

// Backend holds shared dependencies and business logic. Handlers call its
// methods; it owns the database pool and River client lifecycle.
type Backend struct {
	Pool *pgxpool.Pool
	Jobs Jobs
	cfg  *config.Config
}

// New creates a pool, runs River migrations, and starts the River client.
// App migrations must be run before calling this (schema must exist).
func New(cfg *config.Config) (*Backend, error) {
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
			river.QueueDefault:      {MaxWorkers: cfg.River.NumDefaultWorkers},
			jobs.QueueNotifications: {MaxWorkers: cfg.River.NumNotifyWorkers},
			jobs.QueueAI:            {MaxWorkers: cfg.River.NumAIWorkers},
			jobs.QueueMaintenance:   {MaxWorkers: cfg.River.NumMaintWorkers},
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
		Pool: pool,
		Jobs: riverClient,
		cfg:  cfg,
	}, nil
}

// SetConfig sets the configuration on a Backend. Useful in tests where
// Backend is constructed manually (without New).
func (b *Backend) SetConfig(cfg *config.Config) { b.cfg = cfg }

// Config returns the Backend's configuration.
func (b *Backend) Config() *config.Config { return b.cfg }

// Close stops River (finishing in-flight jobs) then closes the database pool.
// Implements io.Closer.
func (b *Backend) Close() error {
	timeout := time.Duration(b.cfg.River.ShutdownTimeoutSec) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := b.Jobs.Stop(ctx); err != nil {
		slog.Warn("river stop error", "error", err)
	}
	slog.Info("river stopped")

	b.Pool.Close()
	slog.Info("database pool closed")
	return nil
}
