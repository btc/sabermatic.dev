package main

import (
	"context"
	"embed"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"

	"github.com/btc/drill/internal/config"
	"github.com/btc/drill/internal/email"
	"github.com/btc/drill/internal/handler"
	"github.com/btc/drill/internal/jobs"
)

//go:embed migrations/*.sql
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
	return runWithContext(ctx)
}

// runWithContext is separated from run() so integration tests can pass a cancellable context.
func runWithContext(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

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
	slog.Info("database connected")

	if err := runMigrations(cfg.Database.URL); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	// Run River migrations (creates River's internal tables)
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

	// Set up River job queue
	workers := jobs.RegisterWorkers(emailSender)
	riverClient, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: 5},
			"notifications":    {MaxWorkers: 5},
			"ai":               {MaxWorkers: 10},
			"maintenance":      {MaxWorkers: 2},
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

	b := &handler.Backend{Pool: pool, River: riverClient}
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
		riverStopCtx, riverStopCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer riverStopCancel()
		if err := riverClient.Stop(riverStopCtx); err != nil {
			slog.Warn("river stop error", "error", err)
		}
		slog.Info("river stopped")
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

	// golang-migrate's pgx5 driver expects pgx5:// scheme.
	// Handle both postgres:// and postgresql:// connection strings.
	trimmed := strings.TrimPrefix(databaseURL, "postgresql://")
	trimmed = strings.TrimPrefix(trimmed, "postgres://")
	pgxURL := "pgx5://" + trimmed
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
