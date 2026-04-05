package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	drill "github.com/btc/drill"
	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/backend"
	"github.com/btc/drill/internal/config"
	"github.com/btc/drill/internal/drilotel"
	"github.com/btc/drill/internal/handler"
	"github.com/btc/drill/sql/migrations"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %s\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	return runWithContext(ctx)
}

// runWithContext is separated from run() so integration tests can pass a cancellable context.
// The ctx controls the shutdown signal only — long-lived resources are managed by Backend.
func runWithContext(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Structured logger with trace correlation (must be after config load for GCPProjectID).
	logLevel := parseLogLevel(cfg.Log.Level)
	logWriter, logCloser := buildLogWriter(cfg.Log.File)
	defer logCloser()

	jsonHandler := slog.NewJSONHandler(logWriter, &slog.HandlerOptions{
		Level: logLevel,
	})
	logger := slog.New(drilotel.NewTraceHandler(jsonHandler, cfg.Otel.GCPProjectID))
	slog.SetDefault(logger)

	if err := runMigrations(cfg.Database.URL); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	// OTel providers (must be before Backend so pool tracer is active).
	providers, err := drilotel.Init(&cfg.Otel)
	if err != nil {
		return fmt.Errorf("init otel: %w", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := providers.Shutdown(shutdownCtx); err != nil {
			slog.Warn("otel shutdown error", "error", err)
		}
	}()

	b, err := backend.New(cfg)
	if err != nil {
		return fmt.Errorf("create backend: %w", err)
	}
	defer b.Close()

	oauthStateKey := auth.DeriveKey(cfg.Auth.TokenSecret, "oauth-state")
	auth.SetupGothProviders(&cfg.OAuth, cfg.Auth.BaseURL, oauthStateKey)

	csrfKey := auth.DeriveKey(cfg.Auth.TokenSecret, "csrf")
	h, closers, err := handler.NewHandler(b, drill.WebFS, csrfKey, cfg.Auth.SecureCookies())
	if err != nil {
		return fmt.Errorf("create handler: %w", err)
	}
	defer func() {
		for _, c := range closers {
			c.Close()
		}
	}()
	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Server.Port),
		Handler: h,
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
		shutdownTimeout := time.Duration(cfg.Server.ShutdownTimeoutSec) * time.Second
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer shutdownCancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			slog.Warn("http shutdown error", "error", err)
		}
		slog.Info("http server stopped")
		return nil
	}
}

func parseLogLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// buildLogWriter returns an io.Writer for slog and a closer func.
// When path is non-empty, logs are written to both stderr and the file.
func buildLogWriter(path string) (io.Writer, func()) {
	if path == "" {
		return os.Stderr, func() {}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "warn: cannot create log dir: %v\n", err)
		return os.Stderr, func() {}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warn: cannot open log file: %v\n", err)
		return os.Stderr, func() {}
	}
	return io.MultiWriter(os.Stderr, f), func() { f.Close() }
}

func runMigrations(databaseURL string) error {
	d, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("create migration source: %w", err)
	}

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
