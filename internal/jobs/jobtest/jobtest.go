package jobtest

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// AssertJobEnqueued checks that exactly `want` jobs of the given kind exist in river_job.
func AssertJobEnqueued(t *testing.T, pool *pgxpool.Pool, kind string, want int) {
	t.Helper()
	var count int
	err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM river_job WHERE kind = $1`, kind,
	).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, want, count, "expected %d %s jobs, got %d", want, kind, count)
}
