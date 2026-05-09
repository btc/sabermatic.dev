// Package aippatchtest provides a Postgres testcontainer with the fixture
// schema applied. Used by aippatch's runtime tests.
package aippatchtest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

// NewPool spins up a Postgres testcontainer, applies fixture migrations
// from thirdparty/aippatch/internal/fixturepb/migrations/, and returns a
// pool. Container is torn down via t.Cleanup.
func NewPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	c, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("aippatch_test"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		postgres.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = c.Terminate(context.Background())
	})

	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	// Apply migrations.
	migrationsDir := filepath.Join("internal", "fixturepb", "migrations")
	entries, err := os.ReadDir(migrationsDir)
	require.NoError(t, err)
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".up.sql") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(migrationsDir, e.Name()))
		require.NoError(t, err)
		_, err = pool.Exec(ctx, string(body))
		require.NoError(t, err, "migration %s", e.Name())
	}
	return pool
}
