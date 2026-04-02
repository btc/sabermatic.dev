package auth_test

import (
	"testing"

	"github.com/btc/drill/internal/auth"
	"github.com/stretchr/testify/require"
)

func TestDeriveKey_Deterministic(t *testing.T) {
	key1 := auth.DeriveKey("my-secret-at-least-32-bytes-long", "purpose-a")
	key2 := auth.DeriveKey("my-secret-at-least-32-bytes-long", "purpose-a")
	require.Equal(t, key1, key2)
}

func TestDeriveKey_DifferentPurposes(t *testing.T) {
	key1 := auth.DeriveKey("my-secret-at-least-32-bytes-long", "purpose-a")
	key2 := auth.DeriveKey("my-secret-at-least-32-bytes-long", "purpose-b")
	require.NotEqual(t, key1, key2)
}

func TestDeriveKey_Length(t *testing.T) {
	key := auth.DeriveKey("my-secret-at-least-32-bytes-long", "purpose-a")
	require.Len(t, key, 32)
}
