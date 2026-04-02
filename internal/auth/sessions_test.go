package auth_test

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/btc/drill/internal/auth"
	"github.com/stretchr/testify/require"
)

func TestGenerateSessionToken(t *testing.T) {
	token, hash, err := auth.GenerateSessionToken()
	require.NoError(t, err)
	require.NotEmpty(t, token)
	require.NotEmpty(t, hash)

	h := sha256.Sum256([]byte(token))
	require.Equal(t, hash, hex.EncodeToString(h[:]))
}

func TestGenerateSessionToken_Unique(t *testing.T) {
	token1, _, _ := auth.GenerateSessionToken()
	token2, _, _ := auth.GenerateSessionToken()
	require.NotEqual(t, token1, token2)
}

func TestHashSessionToken(t *testing.T) {
	hash := auth.HashSessionToken("my-token")
	require.NotEmpty(t, hash)
	require.NotEqual(t, "my-token", hash)
	require.Equal(t, hash, auth.HashSessionToken("my-token"))
}
