package auth_test

import (
	"testing"

	"github.com/btc/drill/internal/auth"
	"github.com/stretchr/testify/require"
)

func TestHashPassword(t *testing.T) {
	hash, err := auth.HashPassword("mypassword", 12)
	require.NoError(t, err)
	require.NotEmpty(t, hash)
	require.NotEqual(t, "mypassword", hash)
}

func TestCheckPassword(t *testing.T) {
	hash, err := auth.HashPassword("mypassword", 12)
	require.NoError(t, err)
	require.NoError(t, auth.CheckPassword(hash, "mypassword"))
	require.Error(t, auth.CheckPassword(hash, "wrongpassword"))
}

func TestHashPassword_DifferentHashes(t *testing.T) {
	h1, _ := auth.HashPassword("same", 4)
	h2, _ := auth.HashPassword("same", 4)
	require.NotEqual(t, h1, h2, "bcrypt should produce different hashes for the same input")
}
