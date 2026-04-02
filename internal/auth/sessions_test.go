package auth_test

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
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

func TestSessionCookie_Login(t *testing.T) {
	c := auth.SessionCookie("my-token", 3600, true)
	require.Equal(t, auth.SessionCookieName, c.Name)
	require.Equal(t, "my-token", c.Value)
	require.Equal(t, "/", c.Path)
	require.True(t, c.HttpOnly)
	require.True(t, c.Secure)
	require.Equal(t, http.SameSiteLaxMode, c.SameSite)
	require.Equal(t, 3600, c.MaxAge)
}

func TestSessionCookie_Logout(t *testing.T) {
	c := auth.SessionCookie("", -1, false)
	require.Equal(t, auth.SessionCookieName, c.Name)
	require.Equal(t, "", c.Value)
	require.Equal(t, -1, c.MaxAge)
	require.False(t, c.Secure)
}
