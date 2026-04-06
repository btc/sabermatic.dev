package backend

import (
	"errors"
	"net/netip"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

// ---------------------------------------------------------------------------
// uuidSlicesEqual
// ---------------------------------------------------------------------------

func TestUUIDSlicesEqual(t *testing.T) {
	a := uuid.New()
	b := uuid.New()

	t.Run("both nil", func(t *testing.T) {
		assert.True(t, uuidSlicesEqual(nil, nil))
	})

	t.Run("both empty", func(t *testing.T) {
		assert.True(t, uuidSlicesEqual([]uuid.UUID{}, []uuid.UUID{}))
	})

	t.Run("nil vs empty", func(t *testing.T) {
		// len(nil) == 0 == len([]uuid.UUID{}) → equal
		assert.True(t, uuidSlicesEqual(nil, []uuid.UUID{}))
	})

	t.Run("equal single-element", func(t *testing.T) {
		assert.True(t, uuidSlicesEqual([]uuid.UUID{a}, []uuid.UUID{a}))
	})

	t.Run("equal multi-element", func(t *testing.T) {
		assert.True(t, uuidSlicesEqual([]uuid.UUID{a, b}, []uuid.UUID{a, b}))
	})

	t.Run("different lengths", func(t *testing.T) {
		assert.False(t, uuidSlicesEqual([]uuid.UUID{a}, []uuid.UUID{a, b}))
	})

	t.Run("same length different values", func(t *testing.T) {
		assert.False(t, uuidSlicesEqual([]uuid.UUID{a}, []uuid.UUID{b}))
	})

	t.Run("order matters", func(t *testing.T) {
		// The function is order-sensitive; [a,b] != [b,a]
		assert.False(t, uuidSlicesEqual([]uuid.UUID{a, b}, []uuid.UUID{b, a}))
	})
}

// ---------------------------------------------------------------------------
// truncateRunes
// ---------------------------------------------------------------------------

func TestTruncateRunes(t *testing.T) {
	cases := []struct {
		name string
		s    string
		n    int
		want string
	}{
		{"shorter than limit", "hello", 10, "hello"},
		{"exact length", "hello", 5, "hello"},
		{"truncated", "hello world", 5, "hello"},
		{"empty string", "", 5, ""},
		{"zero limit", "hello", 0, ""},
		{"multibyte runes", "héllo wörld", 5, "héllo"},
		{"emoji", "👋🌍🚀💫✨", 3, "👋🌍🚀"},
		{"cjk", "你好世界测试", 4, "你好世界"},
		{"one rune", "x", 1, "x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, truncateRunes(tc.s, tc.n))
		})
	}
}
