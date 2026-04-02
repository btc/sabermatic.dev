package auth

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/btc/drill/internal/db"
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

// RequireAuth returns middleware that validates the session cookie and
// injects the authenticated user into the request context.
// If pool is nil, authentication always fails (for unit testing).
func RequireAuth(pool *pgxpool.Pool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(SessionCookieName)
			if err != nil || cookie.Value == "" {
				writeAuthError(w, http.StatusUnauthorized, "authentication required")
				return
			}

			if pool == nil {
				writeAuthError(w, http.StatusUnauthorized, "authentication required")
				return
			}

			tokenHash := HashSessionToken(cookie.Value)
			queries := db.New(pool)
			row, err := queries.GetAuthSessionByToken(r.Context(), tokenHash)
			if err != nil {
				writeAuthError(w, http.StatusUnauthorized, "invalid or expired session")
				return
			}

			// Touch session last_active (fire-and-forget)
			go func() {
				queries.TouchAuthSession(context.Background(), row.ID)
			}()

			user := &AuthUser{
				ID:            row.UserID,
				Email:         row.Email,
				DisplayName:   row.DisplayName,
				Role:          row.Role,
				Plan:          row.Plan,
				EmailVerified: row.EmailVerified,
			}

			next.ServeHTTP(w, r.WithContext(WithUser(r.Context(), user)))
		})
	}
}

func writeAuthError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}
