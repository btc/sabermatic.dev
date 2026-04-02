package handler_test

import (
	"context"
	"os/exec"
	"testing"

	"github.com/btc/drill/internal/db"
	"github.com/stretchr/testify/require"
)

func TestSeedQuestions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupTestDB(t)
	ctx := context.Background()
	queries := db.New(pool)

	// Get connection string for psql
	connStr := pool.Config().ConnString()

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
