package handler_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/markbates/goth"
	"github.com/markbates/goth/gothic"
	"github.com/markbates/goth/providers/faux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/testutil"
)

// setupGothForTest registers the faux provider and overrides CompleteUserAuth
// to return the given goth.User. Restores original state on cleanup.
// Tests using this MUST NOT use t.Parallel() (gothic uses global state).
func setupGothForTest(t *testing.T, user goth.User) {
	t.Helper()

	// Save originals.
	origComplete := gothic.CompleteUserAuth
	origProviders := goth.GetProviders()

	// Register faux provider.
	goth.ClearProviders()
	goth.UseProviders(&faux.Provider{})

	// Override CompleteUserAuth to skip the real OAuth dance.
	gothic.CompleteUserAuth = func(w http.ResponseWriter, r *http.Request) (goth.User, error) {
		return user, nil
	}

	t.Cleanup(func() {
		gothic.CompleteUserAuth = origComplete
		goth.ClearProviders()
		for _, p := range origProviders {
			goth.UseProviders(p)
		}
	})
}

// ---------------------------------------------------------------------------
// OAuthStart
// ---------------------------------------------------------------------------

func TestOAuthStart_UnknownProvider(t *testing.T) {
	b := pg.NewBackend(t)
	h := testutil.NewTestHandler(t, b)

	// No providers registered → any provider returns 404.
	goth.ClearProviders()
	t.Cleanup(goth.ClearProviders)

	req := httptest.NewRequest(http.MethodGet, "/api/auth/oauth/nonexistent", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ---------------------------------------------------------------------------
// OAuthCallback — full flow
// ---------------------------------------------------------------------------

func TestOAuthCallback_NewUser(t *testing.T) {
	cfg := pg.ConfigWithOverrides(t, map[string]string{
		"BASE_URL": "http://localhost:3000",
	})
	b := testutil.NewBackend(t, cfg)
	h := testutil.NewTestHandler(t, b)

	setupGothForTest(t, goth.User{
		Provider: "faux",
		UserID:   "oauth-user-123",
		Email:    "oauth-new@example.com",
		Name:     "OAuth User",
	})

	// Call the callback endpoint. The provider path value must match a registered provider.
	req := httptest.NewRequest(http.MethodGet, "/api/auth/oauth/faux/callback", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	// Should redirect to home.
	assert.Equal(t, http.StatusFound, w.Code)
	location := w.Header().Get("Location")
	assert.Equal(t, "http://localhost:3000/", location)

	// Should set session cookie.
	var sessionCookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.SessionCookieName {
			sessionCookie = c
			break
		}
	}
	require.NotNil(t, sessionCookie, "session cookie should be set")
	assert.NotEmpty(t, sessionCookie.Value)
}

func TestOAuthCallback_ExistingUser(t *testing.T) {
	cfg := pg.ConfigWithOverrides(t, map[string]string{
		"BASE_URL": "http://localhost:3000",
	})
	b := testutil.NewBackend(t, cfg)
	h := testutil.NewTestHandler(t, b)

	oauthUser := goth.User{
		Provider: "faux",
		UserID:   "oauth-existing-456",
		Email:    "oauth-existing@example.com",
		Name:     "Existing OAuth",
	}
	setupGothForTest(t, oauthUser)

	// First login — creates user.
	req := httptest.NewRequest(http.MethodGet, "/api/auth/oauth/faux/callback", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusFound, w.Code)

	// Second login — same provider + provider_id → finds existing user.
	req2 := httptest.NewRequest(http.MethodGet, "/api/auth/oauth/faux/callback", nil)
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusFound, w2.Code)
	assert.Equal(t, "http://localhost:3000/", w2.Header().Get("Location"))

	// Both should set session cookies.
	for _, rec := range []*httptest.ResponseRecorder{w, w2} {
		found := false
		for _, c := range rec.Result().Cookies() {
			if c.Name == auth.SessionCookieName {
				found = true
				break
			}
		}
		assert.True(t, found, "session cookie should be set")
	}
}

func TestOAuthCallback_NickNameFallback(t *testing.T) {
	cfg := pg.ConfigWithOverrides(t, map[string]string{
		"BASE_URL": "http://localhost:3000",
	})
	b := testutil.NewBackend(t, cfg)
	h := testutil.NewTestHandler(t, b)

	// GitHub sometimes has empty Name but non-empty NickName.
	setupGothForTest(t, goth.User{
		Provider: "faux",
		UserID:   "github-nick-789",
		Email:    "nick@example.com",
		Name:     "", // empty — should fall back to NickName
		NickName: "octocat",
	})

	req := httptest.NewRequest(http.MethodGet, "/api/auth/oauth/faux/callback", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusFound, w.Code)

	// Verify the user was created with NickName as display_name via DB query.
	q := db.New(b.Pool())
	user, err := q.GetUserByEmail(context.Background(), "nick@example.com")
	require.NoError(t, err)
	assert.Equal(t, "octocat", user.DisplayName)
}

func TestOAuthCallback_UnknownProvider(t *testing.T) {
	b := pg.NewBackend(t)
	h := testutil.NewTestHandler(t, b)

	goth.ClearProviders()
	t.Cleanup(goth.ClearProviders)

	req := httptest.NewRequest(http.MethodGet, "/api/auth/oauth/nonexistent/callback", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}
