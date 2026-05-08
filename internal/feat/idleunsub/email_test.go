package idleunsub

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestComposeCancelEmail_Renders(t *testing.T) {
	t.Parallel()
	end := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	msg, err := composeCancelEmail("user@example.com", "Jane",
		"https://sabermatic.dev/sub/keep?t=abc.def", end)
	require.NoError(t, err)
	require.Equal(t, "user@example.com", msg.To)
	require.Equal(t, "We won't charge you for the next period", msg.Subject)
	require.Contains(t, msg.Text, "Hi Jane,")
	require.Contains(t, msg.Text, "March 1, 2026")
	require.Contains(t, msg.Text, "https://sabermatic.dev/sub/keep?t=abc.def")
	require.Contains(t, msg.HTML, "Jane")
}

func TestComposeKeptEmail_Renders(t *testing.T) {
	t.Parallel()
	end := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	msg, err := composeKeptEmail("user@example.com", "Jane", end)
	require.NoError(t, err)
	require.Equal(t, "Your subscription is still active", msg.Subject)
	require.Contains(t, msg.Text, "March 1, 2026")
}
