package auth

import (
	"net/http"

	"github.com/gorilla/sessions"
	"github.com/markbates/goth"
	"github.com/markbates/goth/gothic"
	"github.com/markbates/goth/providers/github"
	"github.com/markbates/goth/providers/google"

	"github.com/btc/drill/internal/config"
)

// SetupGothProviders configures Goth with the enabled OAuth providers and
// sets the gothic session store. Must be called once at startup.
//
// NB: Goth uses package-level global state (goth.UseProviders, gothic.Store).
// This is a library constraint. OAuth handler tests must not use t.Parallel().
//
// secure sets the Secure attribute on gothic's session cookie; pass
// config.Auth.SecureCookies() so it matches the app's other cookies.
func SetupGothProviders(cfg *config.OAuth, baseURL string, storeKey []byte, secure bool) {
	var providers []goth.Provider

	if cfg.GoogleClientID != "" {
		providers = append(providers, google.New(
			cfg.GoogleClientID,
			cfg.GoogleClientSecret,
			baseURL+"/api/auth/oauth/google/callback",
			"email", "profile",
		))
	}

	if cfg.GitHubClientID != "" {
		providers = append(providers, github.New(
			cfg.GitHubClientID,
			cfg.GitHubClientSecret,
			baseURL+"/api/auth/oauth/github/callback",
			"user:email",
		))
	}

	if len(providers) > 0 {
		goth.UseProviders(providers...)
	}

	// Set every cookie attribute explicitly. gorilla/sessions v1.3.0 changed
	// NewCookieStore's defaults to Secure+SameSite=None, which drops the
	// cookie over plain http on non-localhost hosts and sends OAuth state on
	// cross-site subrequests. Lax still flows on the provider's top-level
	// GET redirect back to the callback. Max age keeps the 30-day value the
	// store used before; gothic clears the cookie when the flow completes.
	// store.MaxAge (not Options.MaxAge alone) also sets securecookie's limit,
	// which rejects older cookies on decode.
	store := sessions.NewCookieStore(storeKey)
	store.Options = &sessions.Options{
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	}
	store.MaxAge(gothicSessionMaxAge)
	gothic.Store = store
}

// gothicSessionMaxAge bounds the OAuth state cookie's lifetime in seconds.
const gothicSessionMaxAge = 30 * 24 * 60 * 60
