package handler

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/markbates/goth"
	"github.com/markbates/goth/gothic"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/backend"
)

// oauthRedirectMaxAge bounds how long the post-OAuth return URL cookie lives.
// Five minutes covers a reasonable OAuth round-trip (including provider
// consent screens) without leaving a stale redirect hint around.
const oauthRedirectMaxAge = 5 * 60

// isSafeRedirect reports whether target is a same-origin relative URL.
// Must start with "/" and not "//" (protocol-relative), to prevent
// open-redirect abuse where a hostile redirect sends the user off-site
// after a successful login.
func isSafeRedirect(target string) bool {
	return strings.HasPrefix(target, "/") && !strings.HasPrefix(target, "//")
}

// OAuthStart returns a handler that begins the OAuth flow for the given provider.
// If the provider is not configured, returns 404.
func OAuthStart(b *backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		provider := r.PathValue("provider")
		if _, err := goth.GetProvider(provider); err != nil {
			http.NotFound(w, r)
			return
		}

		// Stash the optional return URL in a short-lived cookie so the
		// callback can send the user back where they started after the
		// provider round-trip. Same-origin validated to block open redirects.
		if rd := r.URL.Query().Get("redirect"); rd != "" && isSafeRedirect(rd) {
			http.SetCookie(w, auth.OAuthRedirectCookie(rd, oauthRedirectMaxAge, b.Config().Auth.SecureCookies()))
		}

		// gothic reads "provider" from query params or gorilla/mux vars.
		// With stdlib mux, we need to set it as a query param.
		q := r.URL.Query()
		q.Set("provider", provider)
		r.URL.RawQuery = q.Encode()

		gothic.BeginAuthHandler(w, r)
	}
}

// OAuthCallback returns a handler that completes the OAuth flow, creates or
// links the user account, sets the session cookie, and redirects to the app.
func OAuthCallback(b *backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		provider := r.PathValue("provider")
		if _, err := goth.GetProvider(provider); err != nil {
			http.NotFound(w, r)
			return
		}

		// gothic needs the provider in the query params.
		q := r.URL.Query()
		q.Set("provider", provider)
		r.URL.RawQuery = q.Encode()

		gothUser, err := gothic.CompleteUserAuth(w, r)
		if err != nil {
			slog.Error("oauth complete auth", "provider", provider, "error", err)
			http.Redirect(w, r, b.Config().Auth.BaseURL+"/login?error=oauth_failed", http.StatusFound)
			return
		}

		displayName := resolveDisplayName(gothUser)

		result, err := b.OAuthLogin(r.Context(), backend.OAuthLoginParams{
			Provider:    provider,
			ProviderID:  gothUser.UserID,
			Email:       gothUser.Email,
			DisplayName: displayName,
			IP:          r.RemoteAddr,
			UserAgent:   r.UserAgent(),
		})
		if err != nil {
			slog.Error("oauth login", "provider", provider, "error", err)
			http.Redirect(w, r, b.Config().Auth.BaseURL+"/login?error=internal", http.StatusFound)
			return
		}

		http.SetCookie(w, auth.SessionCookie(
			result.Token,
			int(b.Config().Auth.SessionTTL.Seconds()),
			b.Config().Auth.SecureCookies(),
		))

		// Always clear the return-URL cookie — whether we consume it or
		// not, it's single-use.
		secure := b.Config().Auth.SecureCookies()
		http.SetCookie(w, auth.OAuthRedirectCookie("", -1, secure))

		redirectURL := b.Config().Auth.BaseURL + "/"
		switch {
		case result.NeedsProfile:
			// Profile completion takes precedence — the stashed redirect is
			// dropped in this case rather than bounced through profile setup.
			redirectURL = b.Config().Auth.BaseURL + "/complete-profile"
		default:
			if c, err := r.Cookie(auth.OAuthRedirectCookieName); err == nil && isSafeRedirect(c.Value) {
				redirectURL = b.Config().Auth.BaseURL + c.Value
			}
		}
		http.Redirect(w, r, redirectURL, http.StatusFound)
	}
}

// resolveDisplayName picks the best display name from Goth's user data.
// Google always has Name. GitHub may have empty Name, so fall back to NickName.
func resolveDisplayName(u goth.User) string {
	if u.Name != "" {
		return u.Name
	}
	return u.NickName
}
