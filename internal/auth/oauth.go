package auth

import (
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
func SetupGothProviders(cfg *config.OAuth, baseURL string, storeKey []byte) {
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

	gothic.Store = sessions.NewCookieStore(storeKey)
}
