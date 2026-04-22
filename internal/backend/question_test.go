package backend_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/db"
)

func TestListFeaturedQuestions(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	// Migrations 013 and 014 are part of the embedded migration set
	// (sql/migrations/embed.go), and testutil's SharedPostgres applies
	// all migrations per-database at NewBackend time.

	rows, total, err := b.ListFeaturedQuestions(context.Background())
	require.NoError(t, err)

	require.Len(t, rows, 6)
	require.Equal(t, []string{
		"Video Streaming",
		"News Feed",
		"Ride Sharing",
		"Chat System",
		"Search Autocomplete",
		"Social Graph",
	}, titles(rows))

	// total_count is scoped to seed questions only; 008_seed_questions seeds 18.
	require.Equal(t, int32(18), total)
}

func titles(rows []db.ListFeaturedQuestionsRow) []string {
	out := make([]string, 0, len(rows))
	for i := range rows {
		out = append(out, rows[i].Title)
	}
	return out
}
