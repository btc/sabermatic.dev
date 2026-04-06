package handler

import (
	"log/slog"
	"net/http"

	"github.com/markbates/goth"
	"github.com/markbates/goth/gothic"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/backend"
)

// OAuthStart returns a handler that begins the OAuth flow for the given provider.
// If the provider is not configured, returns 404.
func OAuthStart(b *backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		provider := r.PathValue("provider")
		if _, err := goth.GetProvider(provider); err != nil {
			http.NotFound(w, r)
			return
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

		displayName := resolveDisplayName(&gothUser)

		result, err := b.OAuthLogin(r.Context(), &backend.OAuthLoginParams{
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

		redirectURL := b.Config().Auth.BaseURL + "/dashboard"
		if result.NeedsProfile {
			redirectURL = b.Config().Auth.BaseURL + "/complete-profile"
		}
		http.Redirect(w, r, redirectURL, http.StatusFound)
	}
}

// resolveDisplayName picks the best display name from Goth's user data.
// Google always has Name. GitHub may have empty Name, so fall back to NickName.
func resolveDisplayName(u *goth.User) string {
	if u.Name != "" {
		return u.Name
	}
	return u.NickName
}
