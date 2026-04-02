package backend

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/jobs"
)

// dummyBcryptHash is used for constant-time login responses. When a user is
// not found, we still run bcrypt to prevent timing oracles that reveal whether
// an email is registered. Generated with cost 12.
var dummyBcryptHash = "$2a$12$LKpvXspMO/C6shvqXZwCVOgIw3YklCI48vEoUxpBu3TWm/ZePR.02"

// SignupResult is returned by Signup on success.
type SignupResult struct {
	UserID uuid.UUID
	Email  string
}

// LoginResult is returned by Login on success.
type LoginResult struct {
	UserID uuid.UUID
	Email  string
	Token  string
}

// Signup creates a new user account, hashes the password, and enqueues a
// verification email. Returns ErrPasswordLength or ErrDuplicateEmail on
// validation/constraint failures.
func (b *Backend) Signup(ctx context.Context, email, password, displayName string) (*SignupResult, error) {
	// Normalize.
	email = strings.ToLower(strings.TrimSpace(email))
	displayName = strings.TrimSpace(displayName)

	if email == "" || password == "" || displayName == "" {
		return nil, fmt.Errorf("email, password, and display_name are required")
	}
	if len(password) < 8 || len(password) > 128 {
		return nil, ErrPasswordLength
	}

	// Hash password.
	hash, err := auth.HashPassword(password, b.cfg.Auth.BcryptCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	// Insert user.
	queries := db.New(b.Pool)
	user, err := queries.CreateUser(ctx, db.CreateUserParams{
		Email:        email,
		PasswordHash: pgtype.Text{String: hash, Valid: true},
		DisplayName:  displayName,
	})
	if err != nil {
		if isDuplicateKeyError(err) {
			return nil, ErrDuplicateEmail
		}
		return nil, fmt.Errorf("create user: %w", err)
	}

	// Enqueue verification email (best-effort; River may be nil in tests).
	if b.River != nil {
		signer := auth.NewTokenSigner(b.cfg.Auth.TokenSecret)
		token, err := signer.Sign(user.ID, "verify-email", b.cfg.Auth.VerifyTokenTTL)
		if err != nil {
			slog.Error("sign verification token", "error", err)
		} else {
			verifyURL := fmt.Sprintf("%s/verify-email?token=%s", b.cfg.Auth.BaseURL, token)
			_, err = b.River.Insert(ctx, jobs.SendEmailArgs{
				To:      user.Email,
				Subject: "Verify your Drill account",
				Text:    fmt.Sprintf("Click here to verify your email: %s", verifyURL),
				HTML:    fmt.Sprintf(`<p>Click <a href="%s">here</a> to verify your email.</p>`, verifyURL),
			}, jobs.SendEmailInsertOpts(&b.cfg.Email))
			if err != nil {
				slog.Error("enqueue verification email", "error", err)
			}
		}
	}

	return &SignupResult{
		UserID: user.ID,
		Email:  user.Email,
	}, nil
}

// Login authenticates a user by email/password, creates a session, and returns
// the raw session token. Returns ErrInvalidCredentials on any auth failure.
func (b *Backend) Login(ctx context.Context, email, password, remoteAddr, userAgent string) (*LoginResult, error) {
	email = strings.ToLower(strings.TrimSpace(email))

	// Look up user.
	queries := db.New(b.Pool)
	user, err := queries.GetUserByEmail(ctx, email)
	if err != nil {
		// Constant-time: run dummy bcrypt to prevent timing oracle.
		auth.CheckPassword(dummyBcryptHash, "x")
		return nil, ErrInvalidCredentials
	}

	// Check that user has a password (not OAuth-only).
	if !user.PasswordHash.Valid {
		auth.CheckPassword(dummyBcryptHash, "x")
		return nil, ErrInvalidCredentials
	}

	// Compare password.
	if err := auth.CheckPassword(user.PasswordHash.String, password); err != nil {
		return nil, ErrInvalidCredentials
	}

	// Generate session.
	rawToken, tokenHash, err := auth.GenerateSessionToken()
	if err != nil {
		return nil, fmt.Errorf("generate session token: %w", err)
	}

	// Parse client IP.
	ipAddr := parseClientIP(remoteAddr)

	_, err = queries.CreateAuthSession(ctx, db.CreateAuthSessionParams{
		UserID:    user.ID,
		TokenHash: tokenHash,
		ExpiresAt: time.Now().Add(b.cfg.Auth.SessionTTL),
		IpAddress: ipAddr,
		UserAgent: pgtype.Text{String: userAgent, Valid: userAgent != ""},
	})
	if err != nil {
		return nil, fmt.Errorf("create auth session: %w", err)
	}

	return &LoginResult{
		UserID: user.ID,
		Email:  user.Email,
		Token:  rawToken,
	}, nil
}

// Logout invalidates the session identified by the given raw token.
// Errors are best-effort (always returns nil).
func (b *Backend) Logout(ctx context.Context, sessionToken string) error {
	tokenHash := auth.HashSessionToken(sessionToken)
	queries := db.New(b.Pool)

	session, err := queries.GetAuthSessionByToken(ctx, tokenHash)
	if err == nil {
		if delErr := queries.DeleteAuthSession(ctx, session.ID); delErr != nil {
			slog.Error("delete auth session", "error", delErr)
		}
	}
	return nil
}

// VerifyEmail marks a user's email as verified using the signed token.
// Returns ErrInvalidToken if the token is invalid or expired.
func (b *Backend) VerifyEmail(ctx context.Context, token string) error {
	signer := auth.NewTokenSigner(b.cfg.Auth.TokenSecret)
	userID, err := signer.Verify(token, "verify-email")
	if err != nil {
		return ErrInvalidToken
	}

	queries := db.New(b.Pool)
	if err := queries.VerifyUserEmail(ctx, userID); err != nil {
		return fmt.Errorf("verify email: %w", err)
	}
	return nil
}

// ForgotPassword enqueues a password-reset email if the user exists.
// Always returns nil to prevent email enumeration.
func (b *Backend) ForgotPassword(ctx context.Context, email string) error {
	email = strings.TrimSpace(strings.ToLower(email))

	queries := db.New(b.Pool)
	user, err := queries.GetUserByEmail(ctx, email)
	if err == nil {
		signer := auth.NewTokenSigner(b.cfg.Auth.TokenSecret)
		token, _ := signer.Sign(user.ID, "reset-password", b.cfg.Auth.ResetTokenTTL)
		resetURL := b.cfg.Auth.BaseURL + "/reset-password?token=" + token

		if b.River != nil {
			b.River.Insert(ctx, jobs.SendEmailArgs{
				To:      user.Email,
				Subject: "Reset your Drill password",
				Text:    "Click here to reset your password: " + resetURL,
				HTML:    "<p>Click <a href=\"" + resetURL + "\">here</a> to reset your password.</p>",
			}, jobs.SendEmailInsertOpts(&b.cfg.Email))
		}
	}

	// Always nil to prevent email enumeration.
	return nil
}

// ResetPassword verifies the reset token, updates the password, and deletes
// all sessions for the user. Returns ErrPasswordLength or ErrInvalidToken on
// validation failures.
func (b *Backend) ResetPassword(ctx context.Context, token, newPassword string) error {
	if len(newPassword) < 8 || len(newPassword) > 128 {
		return ErrPasswordLength
	}

	signer := auth.NewTokenSigner(b.cfg.Auth.TokenSecret)
	userID, err := signer.Verify(token, "reset-password")
	if err != nil {
		return ErrInvalidToken
	}

	hash, err := auth.HashPassword(newPassword, b.cfg.Auth.BcryptCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	queries := db.New(b.Pool)
	if err := queries.UpdateUserPassword(ctx, db.UpdateUserPasswordParams{
		ID:           userID,
		PasswordHash: pgtype.Text{String: hash, Valid: true},
	}); err != nil {
		return fmt.Errorf("update password: %w", err)
	}

	// Invalidate all sessions.
	queries.DeleteUserAuthSessions(ctx, userID)
	return nil
}

// parseClientIP extracts a netip.Addr from a RemoteAddr string (which may
// include a port, e.g. "192.168.1.1:12345" or "[::1]:12345").
func parseClientIP(remoteAddr string) *netip.Addr {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		// No port — try parsing the whole thing.
		host = remoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return nil
	}
	return &addr
}

// isDuplicateKeyError returns true if the error is a Postgres unique-violation
// (SQLSTATE 23505).
func isDuplicateKeyError(err error) bool {
	return strings.Contains(err.Error(), "23505") ||
		strings.Contains(err.Error(), "duplicate key")
}
