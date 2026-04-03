package backend

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/btc/drill/internal/config"
)

// startPostgres starts a Postgres 16 container, runs app migrations, and
// returns the connection string. The container is terminated on test cleanup.
func startPostgres(t *testing.T) string {
	t.Helper()
	ctx := context.Background()

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
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() { pgContainer.Terminate(ctx) })

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("get connection string: %v", err)
	}

	// Run app migrations from canonical sql/migrations/.
	d, err := iofs.New(os.DirFS("../../sql/migrations"), ".")
	if err != nil {
		t.Fatalf("create migration source: %v", err)
	}

	trimmed := strings.TrimPrefix(connStr, "postgresql://")
	trimmed = strings.TrimPrefix(trimmed, "postgres://")
	pgxURL := "pgx5://" + trimmed
	m, err := migrate.NewWithSourceInstance("iofs", d, pgxURL)
	if err != nil {
		t.Fatalf("create migrate: %v", err)
	}
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("migrate up: %v", err)
	}

	return connStr
}

// newTestBackend starts Postgres, creates a real Backend (with pool + River),
// and returns it. The Backend is closed on test cleanup.
func newTestBackend(t *testing.T) *Backend {
	t.Helper()
	connStr := startPostgres(t)
	cfg := loadTestConfig(t, connStr)

	b, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { b.Close() })

	return b
}

// loadTestConfig loads a config.Config suitable for backend integration tests.
func loadTestConfig(t *testing.T, databaseURL string) *config.Config {
	t.Helper()
	t.Setenv("DATABASE_URL", databaseURL)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	t.Setenv("OPENAI_API_KEY", "sk-test")
	t.Setenv("AUTH_TOKEN_SECRET", "test-secret-at-least-32-bytes-long")
	t.Setenv("AUTH_BCRYPT_COST", "4")
	cfg, err := config.Load()
	require.NoError(t, err)
	return cfg
}
