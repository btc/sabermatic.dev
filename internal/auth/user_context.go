package auth

import (
	"context"
	"time"

	"github.com/google/uuid"
)

const SessionCookieName = "drill_session"

type contextKey string

const userContextKey contextKey = "auth_user"

// AuthUser is the authenticated user extracted from the session cookie.
type AuthUser struct {
	ID            uuid.UUID
	Email         string
	DisplayName   string
	Role          string
	Plan          string
	EmailVerified bool
	CreatedAt     time.Time

	// Auto-cancel state (migration 016).
	SubCancelAtPeriodEnd bool
	SubCancelIsAuto      bool
	PendingKeptBanner    bool
}

// SessionAuthenticator validates a hashed session token and returns the
// authenticated user. Implemented by *backend.Backend.
type SessionAuthenticator interface {
	AuthenticateSession(ctx context.Context, tokenHash string) (*AuthUser, error)
}

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
