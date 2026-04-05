package auth

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// AuthUser is the authenticated user extracted from the session cookie.
type AuthUser struct {
	ID            uuid.UUID
	Email         string
	DisplayName   string
	Role          string
	Plan          string
	EmailVerified bool
}

type contextKey string

const userContextKey contextKey = "auth_user"

// WithUser stores the authenticated user in the context.
func WithUser(ctx context.Context, user *AuthUser) context.Context {
	return context.WithValue(ctx, userContextKey, user)
}

// UserFromContext extracts the authenticated user from the context.
// Returns nil if no user is present.
func UserFromContext(ctx context.Context) *AuthUser {
	user, _ := ctx.Value(userContextKey).(*AuthUser)
	return user
}

const SessionCookieName = "drill_session"

// SessionAuthenticator validates a hashed session token and returns the
// authenticated user. Implemented by *backend.Backend.
type SessionAuthenticator interface {
	AuthenticateSession(ctx context.Context, tokenHash string) (*AuthUser, error)
}

// RequireAuth returns middleware that validates the session cookie and
// injects the authenticated user into the request context.
func RequireAuth(sa SessionAuthenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(SessionCookieName)
			if err != nil || cookie.Value == "" {
				writeAuthError(w, http.StatusUnauthorized, "authentication required")
				return
			}

			tokenHash := HashSessionToken(cookie.Value)
			user, err := sa.AuthenticateSession(r.Context(), tokenHash)
			if err != nil {
				writeAuthError(w, http.StatusUnauthorized, "invalid or expired session")
				return
			}

			span := trace.SpanFromContext(r.Context())
			span.SetAttributes(attribute.String("user_id", user.ID.String()))

			next.ServeHTTP(w, r.WithContext(WithUser(r.Context(), user)))
		})
	}
}

// RequireAdmin returns middleware that checks the authenticated user has
// the admin role. Must be applied after RequireAuth.
func RequireAdmin() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := UserFromContext(r.Context())
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
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}
