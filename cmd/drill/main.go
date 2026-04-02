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
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"

	"github.com/btc/drill/internal/config"
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
// The ctx controls the shutdown signal only — long-lived resources (pool, River) use
// context.Background() so they remain available during graceful shutdown.
func runWithContext(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Pool uses background context — must outlive signal for graceful shutdown.
	pool, err := cfg.Database.NewPool(context.Background())
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	defer pool.Close()
	slog.Info("database connected")

	if err := runMigrations(cfg.Database.URL); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	// Run River migrations (creates River's internal tables)
	riverMigrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		return fmt.Errorf("create river migrator: %w", err)
	}
	riverRes, err := riverMigrator.Migrate(context.Background(), rivermigrate.DirectionUp, nil)
	if err != nil {
		return fmt.Errorf("river migrate: %w", err)
	}
	for _, v := range riverRes.Versions {
		slog.Info("river migration applied", "version", v.Version)
	}

	// Set up River job queue
	emailSender := cfg.Email.NewSender()
	workers := jobs.RegisterWorkers(emailSender)
	riverClient, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: cfg.River.DefaultWorkers},
			"notifications":   {MaxWorkers: cfg.River.NotifyWorkers},
			"ai":              {MaxWorkers: cfg.River.AIWorkers},
			"maintenance":     {MaxWorkers: cfg.River.MaintWorkers},
		},
		Workers: workers,
	})
	if err != nil {
		return fmt.Errorf("create river client: %w", err)
	}
	// River uses background context — we manage its lifecycle via Stop().
	if err := riverClient.Start(context.Background()); err != nil {
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

	// Wait for shutdown signal or server error.
	// ctx cancellation only triggers the select — it does not cancel pool or River.
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		slog.Info("shutting down")

		riverStopTimeout := time.Duration(cfg.River.ShutdownTimeout) * time.Second
		riverStopCtx, riverStopCancel := context.WithTimeout(context.Background(), riverStopTimeout)
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
