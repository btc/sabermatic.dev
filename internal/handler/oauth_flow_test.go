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

// ---------------------------------------------------------------------------
// Redirect preservation across the OAuth round-trip
// ---------------------------------------------------------------------------

func TestOAuthStart_SetsRedirectCookie_WhenSafe(t *testing.T) {
	b := pg.NewBackend(t)
	h := testutil.NewTestHandler(t, b)

	goth.ClearProviders()
	goth.UseProviders(&faux.Provider{})
	t.Cleanup(goth.ClearProviders)

	req := httptest.NewRequest(http.MethodGet, "/api/auth/oauth/faux?redirect=%2Fsessions%2Fnew%3Fquestion%3Dabc", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var rd *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.OAuthRedirectCookieName {
			rd = c
			break
		}
	}
	require.NotNil(t, rd, "OAuth start should stash the return URL")
	assert.Equal(t, "/sessions/new?question=abc", rd.Value)
	assert.True(t, rd.HttpOnly)
	assert.Positive(t, rd.MaxAge)
}

func TestOAuthStart_RejectsUnsafeRedirect(t *testing.T) {
	b := pg.NewBackend(t)
	h := testutil.NewTestHandler(t, b)

	goth.ClearProviders()
	goth.UseProviders(&faux.Provider{})
	t.Cleanup(goth.ClearProviders)

	// Protocol-relative URL would send the user to evil.com — must not be stashed.
	req := httptest.NewRequest(http.MethodGet, "/api/auth/oauth/faux?redirect=%2F%2Fevil.com%2Fpwn", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	for _, c := range w.Result().Cookies() {
		assert.NotEqual(t, auth.OAuthRedirectCookieName, c.Name, "unsafe redirect should not be stashed")
	}
}

func TestOAuthCallback_HonorsRedirectCookie(t *testing.T) {
	cfg := pg.ConfigWithOverrides(t, map[string]string{
		"BASE_URL": "http://localhost:3000",
	})
	b := testutil.NewBackend(t, cfg)
	h := testutil.NewTestHandler(t, b)

	setupGothForTest(t, goth.User{
		Provider: "faux",
		UserID:   "oauth-redir-1",
		Email:    "oauth-redir@example.com",
		Name:     "Redir User",
	})

	req := httptest.NewRequest(http.MethodGet, "/api/auth/oauth/faux/callback", nil)
	req.AddCookie(&http.Cookie{Name: auth.OAuthRedirectCookieName, Value: "/sessions/new?question=abc"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusFound, w.Code)
	assert.Equal(t, "http://localhost:3000/sessions/new?question=abc", w.Header().Get("Location"))

	// The redirect cookie must be cleared (max-age 0 or negative) on consumption.
	var cleared *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.OAuthRedirectCookieName {
			cleared = c
			break
		}
	}
	require.NotNil(t, cleared, "callback should clear the redirect cookie")
	assert.LessOrEqual(t, cleared.MaxAge, 0)
}

func TestOAuthCallback_IgnoresUnsafeRedirectCookie(t *testing.T) {
	cfg := pg.ConfigWithOverrides(t, map[string]string{
		"BASE_URL": "http://localhost:3000",
	})
	b := testutil.NewBackend(t, cfg)
	h := testutil.NewTestHandler(t, b)

	setupGothForTest(t, goth.User{
		Provider: "faux",
		UserID:   "oauth-redir-2",
		Email:    "oauth-redir-2@example.com",
		Name:     "Redir User 2",
	})

	// A tampered cookie with a protocol-relative URL must be ignored —
	// fall back to the default landing rather than honoring the attacker's URL.
	req := httptest.NewRequest(http.MethodGet, "/api/auth/oauth/faux/callback", nil)
	req.AddCookie(&http.Cookie{Name: auth.OAuthRedirectCookieName, Value: "//evil.com/pwn"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusFound, w.Code)
	assert.Equal(t, "http://localhost:3000/", w.Header().Get("Location"))
}
