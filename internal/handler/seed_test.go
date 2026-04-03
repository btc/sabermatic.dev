package handler_test

import (
	"context"
	"os/exec"
	"testing"

	"github.com/btc/drill/internal/db"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

func TestSeedQuestions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	connStr := startPostgres(t)
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, connStr)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	queries := db.New(pool)

	// Run seed file
	cmd := exec.Command("psql", connStr, "-f", "../../seed/questions.sql")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "psql failed: %s", string(out))

	// Verify 18 questions inserted
	count, err := queries.CountSeedQuestions(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(18), count)

	// Run again to verify idempotency
	cmd = exec.Command("psql", connStr, "-f", "../../seed/questions.sql")
	out, err = cmd.CombinedOutput()
	require.NoError(t, err, "psql idempotent run failed: %s", string(out))

	count, err = queries.CountSeedQuestions(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(18), count, "seed should be idempotent")
}
