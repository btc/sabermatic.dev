package handler

import (
	"encoding/json"
	"net/http"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/btc/drill/internal/auth"
)

// SecurityHeaders returns a middleware that sets standard security hardening
// headers on every response. HSTS is only set when secureCookies is true
// (production/HTTPS).
func SecurityHeaders(secureCookies bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data: https://storage.googleapis.com; connect-src 'self' wss:; font-src 'self'; frame-ancestors 'none'")

		if secureCookies {
			w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		}

		next.ServeHTTP(w, r)
	})
}

// RequireAuth returns middleware that validates the session cookie and
// injects the authenticated user into the request context.
func RequireAuth(sa auth.SessionAuthenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(auth.SessionCookieName)
			if err != nil || cookie.Value == "" {
				writeAuthError(w, http.StatusUnauthorized, "authentication required")
				return
			}

			tokenHash := auth.HashSessionToken(cookie.Value)
			user, err := sa.AuthenticateSession(r.Context(), tokenHash)
			if err != nil {
				writeAuthError(w, http.StatusUnauthorized, "invalid or expired session")
				return
			}

			span := trace.SpanFromContext(r.Context())
			span.SetAttributes(attribute.String("user_id", user.ID.String()))

			next.ServeHTTP(w, r.WithContext(auth.WithUser(r.Context(), user)))
		})
	}
}

// RequireAdmin returns middleware that checks the authenticated user has
// the admin role. Must be applied after RequireAuth.
func RequireAdmin() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := auth.UserFromContext(r.Context())
			if user == nil || user.Role != "admin" {
				writeAuthError(w, http.StatusForbidden, "admin access required")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func writeAuthError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
