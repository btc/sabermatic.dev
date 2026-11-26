package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/config"
	"github.com/markbates/goth"
	"github.com/markbates/goth/gothic"
	"github.com/stretchr/testify/require"
)

// The gothic session cookie carries OAuth state across the provider
// round-trip. Its attributes are set explicitly rather than inherited from
// gorilla/sessions defaults, which changed in v1.3.0 to Secure+SameSite=None.
func TestSetupGothProviders_SessionCookieAttributes(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(map[bool]string{false: "http", true: "https"}[secure], func(t *testing.T) {
			goth.ClearProviders()
			t.Cleanup(goth.ClearProviders)

			auth.SetupGothProviders(&config.OAuth{}, "http://localhost:3000", []byte("test-key-32-bytes-long-for-store"), secure)

			req := httptest.NewRequest(http.MethodGet, "/api/auth/oauth/google", nil)
			rec := httptest.NewRecorder()
			require.NoError(t, gothic.StoreInSession("state", "abc", req, rec))

			var c *http.Cookie
			for _, rc := range rec.Result().Cookies() {
				if rc.Name == gothic.SessionName {
					c = rc
				}
			}
			require.NotNil(t, c, "gothic session cookie should be set")
			require.Equal(t, "/", c.Path)
			require.True(t, c.HttpOnly)
			require.Equal(t, http.SameSiteLaxMode, c.SameSite)
			require.Equal(t, secure, c.Secure)
			require.Equal(t, 30*24*60*60, c.MaxAge)
		})
	}
}

func TestSetupGothProviders_BothConfigured(t *testing.T) {
	goth.ClearProviders()
	t.Cleanup(goth.ClearProviders)

	cfg := &config.OAuth{
		GoogleClientID:     "google-id",
		GoogleClientSecret: "google-secret",
		GitHubClientID:     "github-id",
		GitHubClientSecret: "github-secret",
	}
	auth.SetupGothProviders(cfg, "http://localhost:3000", []byte("test-key-32-bytes-long-for-store"), false)

	providers := goth.GetProviders()
	require.Contains(t, providers, "google")
	require.Contains(t, providers, "github")
}

func TestSetupGothProviders_NoneConfigured(t *testing.T) {
	goth.ClearProviders()
	t.Cleanup(goth.ClearProviders)

	cfg := &config.OAuth{}
	auth.SetupGothProviders(cfg, "http://localhost:3000", []byte("test-key-32-bytes-long-for-store"), false)

	providers := goth.GetProviders()
	require.Empty(t, providers)
}

func TestSetupGothProviders_GoogleOnly(t *testing.T) {
	goth.ClearProviders()
	t.Cleanup(goth.ClearProviders)

	cfg := &config.OAuth{
		GoogleClientID:     "google-id",
		GoogleClientSecret: "google-secret",
	}
	auth.SetupGothProviders(cfg, "http://localhost:3000", []byte("test-key-32-bytes-long-for-store"), false)

	providers := goth.GetProviders()
	require.Contains(t, providers, "google")
	require.NotContains(t, providers, "github")
}
