package idleunsub_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/backendtest"
	"github.com/btc/drill/internal/db"
)

// TestClearStaleKeptBanners_MostRecentEventGates verifies that the
// ClearStaleKeptBanners query uses the MOST RECENT subscription_kept event to
// decide whether a user's banner should be cleared.
//
// Scenario:
//   - User A: banner=true, one kept event 30 days ago — should be cleared.
//   - User B: banner=true, one kept event 30 days ago AND one 2 days ago —
//     banner must be retained (recent event is fresh).
//   - User C: banner=true, one kept event 5 days ago — banner must be retained
//     (entirely within the 14-day window).
func TestClearStaleKeptBanners_MostRecentEventGates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b := pg.NewBackend(t)

	userA := backendtest.SeedUser(t, b)
	userB := backendtest.SeedUser(t, b)
	userC := backendtest.SeedUser(t, b)

	// Set all three users to pending_kept_banner=true.
	for _, id := range []uuid.UUID{userA, userB, userC} {
		_, err := b.Pool().Exec(ctx,
			`UPDATE users SET pending_kept_banner = TRUE WHERE id = $1`, id)
		require.NoError(t, err)
	}

	now := time.Now().UTC()

	// User A: one stale event (30 days ago).
	_, err := b.Pool().Exec(ctx, `
		INSERT INTO user_events (user_id, event_type, created_at)
		VALUES ($1, 'subscription_kept', $2)`,
		userA, now.AddDate(0, 0, -30))
	require.NoError(t, err)

	// User B: one stale event (30 days ago) + one recent event (2 days ago).
	_, err = b.Pool().Exec(ctx, `
		INSERT INTO user_events (user_id, event_type, created_at)
		VALUES ($1, 'subscription_kept', $2),
		       ($1, 'subscription_kept', $3)`,
		userB, now.AddDate(0, 0, -30), now.AddDate(0, 0, -2))
	require.NoError(t, err)

	// User C: one recent event (5 days ago).
	_, err = b.Pool().Exec(ctx, `
		INSERT INTO user_events (user_id, event_type, created_at)
		VALUES ($1, 'subscription_kept', $2)`,
		userC, now.AddDate(0, 0, -5))
	require.NoError(t, err)

	require.NoError(t, db.New(b.Pool()).ClearStaleKeptBanners(ctx))

	readBanner := func(id uuid.UUID) bool {
		t.Helper()
		var banner bool
		err := b.Pool().QueryRow(ctx,
			`SELECT pending_kept_banner FROM users WHERE id = $1`, id).Scan(&banner)
		require.NoError(t, err)
		return banner
	}

	// User A: only stale event — banner must be cleared.
	require.False(t, readBanner(userA), "user A: stale-only events must clear the banner")
	// User B: has a recent event — banner must be retained.
	require.True(t, readBanner(userB), "user B: recent event must protect the banner")
	// User C: entirely recent — banner must be retained.
	require.True(t, readBanner(userC), "user C: recent event must protect the banner")
}
