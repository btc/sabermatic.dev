package backend

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// New + Close (full lifecycle with real Postgres and River)
// ---------------------------------------------------------------------------

func TestNewAndClose(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	connStr := startPostgres(t)
	cfg := loadTestConfig(t, connStr)

	b, err := New(cfg)
	require.NoError(t, err)
	require.NotNil(t, b.pool)
	require.NotNil(t, b.jobs)
	require.Equal(t, cfg, b.Config())

	// Ping checks connectivity.
	err = b.Ping(context.Background())
	require.NoError(t, err)

	// Close stops River and closes the pool.
	err = b.Close()
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// parseClientIP
// ---------------------------------------------------------------------------

func TestParseClientIP(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		wantIP string // expected string repr, or "" for nil
	}{
		{
			name:   "IPv4 with port",
			input:  "192.168.1.1:12345",
			wantIP: "192.168.1.1",
		},
		{
			name:   "IPv6 with port",
			input:  "[::1]:12345",
			wantIP: "::1",
		},
		{
			name:   "IPv4 no port",
			input:  "192.168.1.1",
			wantIP: "192.168.1.1",
		},
		{
			name:   "garbage input",
			input:  "garbage",
			wantIP: "",
		},
		{
			name:   "empty string",
			input:  "",
			wantIP: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := parseClientIP(tc.input)
			if tc.wantIP == "" {
				require.Nil(t, result)
			} else {
				require.NotNil(t, result)
				expected := netip.MustParseAddr(tc.wantIP)
				require.Equal(t, expected, *result)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// isDuplicateKeyError
// ---------------------------------------------------------------------------

func TestIsDuplicateKeyError(t *testing.T) {
	t.Run("PgError with code 23505", func(t *testing.T) {
		err := &pgconn.PgError{Code: "23505"}
		require.True(t, isDuplicateKeyError(err))
	})

	t.Run("PgError with different code", func(t *testing.T) {
		err := &pgconn.PgError{Code: "23503"} // foreign key violation
		require.False(t, isDuplicateKeyError(err))
	})

	t.Run("non-PgError", func(t *testing.T) {
		err := errors.New("some random error")
		require.False(t, isDuplicateKeyError(err))
	})

	t.Run("nil error", func(t *testing.T) {
		// isDuplicateKeyError is only called with non-nil errors in production,
		// but verify it does not panic on nil.
		require.False(t, isDuplicateKeyError(nil))
	})

	t.Run("wrapped PgError with code 23505", func(t *testing.T) {
		inner := &pgconn.PgError{Code: "23505"}
		wrapped := errors.Join(errors.New("outer"), inner)
		require.True(t, isDuplicateKeyError(wrapped))
	})
}
