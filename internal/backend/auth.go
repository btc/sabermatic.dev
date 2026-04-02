package backend

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/jobs"
)

// dummyBcryptHash is used for constant-time login responses. When a user is
// not found, we still run bcrypt to prevent timing oracles that reveal whether
// an email is registered. Generated with cost 12.
var dummyBcryptHash = "$2a$12$LKpvXspMO/C6shvqXZwCVOgIw3YklCI48vEoUxpBu3TWm/ZePR.02"

// SignupParams holds the parameters for Signup.
type SignupParams struct {
	Email       string
	Password    string
	DisplayName string
}

// LoginParams holds the parameters for Login.
type LoginParams struct {
	Email     string
	Password  string
	IP        string
	UserAgent string
}

// ResetPasswordParams holds the parameters for ResetPassword.
type ResetPasswordParams struct {
	Token       string
	NewPassword string
}

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
// verification email. User creation and email enqueue are atomic: both
// succeed or both roll back. Returns ErrPasswordLength or ErrDuplicateEmail
// on validation/constraint failures.
func (b *Backend) Signup(ctx context.Context, p SignupParams) (*SignupResult, error) {
	// Normalize.
	p.Email = strings.ToLower(strings.TrimSpace(p.Email))
	p.DisplayName = strings.TrimSpace(p.DisplayName)

	if p.Email == "" || p.Password == "" || p.DisplayName == "" {
		return nil, ErrMissingFields
	}
	if len(p.Password) < 8 || len(p.Password) > 128 {
		return nil, ErrPasswordLength
	}

	// Hash password.
	hash, err := auth.HashPassword(p.Password, b.cfg.Auth.BcryptCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	// Transaction: create user + enqueue verification email atomically.
	tx, err := b.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	queries := db.New(tx)
	user, err := queries.CreateUser(ctx, db.CreateUserParams{
		Email:        p.Email,
		PasswordHash: pgtype.Text{String: hash, Valid: true},
		DisplayName:  p.DisplayName,
	})
	if err != nil {
		if isDuplicateKeyError(err) {
			return nil, ErrDuplicateEmail
		}
		return nil, fmt.Errorf("create user: %w", err)
	}

	// Enqueue verification email within the same transaction.
	signer := auth.NewTokenSigner(auth.DeriveKey(b.cfg.Auth.TokenSecret, "hmac-tokens"))
	token, err := signer.Sign(user.ID, "verify-email", b.cfg.Auth.VerifyTokenTTL)
	if err != nil {
		return nil, fmt.Errorf("sign verification token: %w", err)
	}
	verifyURL := fmt.Sprintf("%s/verify-email?token=%s", b.cfg.Auth.BaseURL, token)
	_, err = b.Jobs.InsertTx(ctx, tx, jobs.SendEmailArgs{
		To:      user.Email,
		Subject: "Verify your Drill account",
		Text:    fmt.Sprintf("Click here to verify your email: %s", verifyURL),
		HTML:    fmt.Sprintf(`<p>Click <a href="%s">here</a> to verify your email.</p>`, verifyURL),
	}, jobs.SendEmailInsertOpts(&b.cfg.Email))
	if err != nil {
		return nil, fmt.Errorf("enqueue verification email: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit signup: %w", err)
	}

	return &SignupResult{
		UserID: user.ID,
		Email:  user.Email,
	}, nil
}

// Login authenticates a user by email/password, creates a session, and returns
// the raw session token. Returns ErrInvalidCredentials on any auth failure.
func (b *Backend) Login(ctx context.Context, p LoginParams) (*LoginResult, error) {
	p.Email = strings.ToLower(strings.TrimSpace(p.Email))

	// Look up user.
	queries := db.New(b.Pool)
	user, err := queries.GetUserByEmail(ctx, p.Email)
	if err != nil {
		// Constant-time: run dummy bcrypt to prevent timing oracle.
		auth.CheckPassword(dummyBcryptHash, "x")
		if !errors.Is(err, pgx.ErrNoRows) {
			slog.Error("login: get user by email", "error", err)
		}
		return nil, ErrInvalidCredentials
	}

	// Check that user has a password (not OAuth-only).
	if !user.PasswordHash.Valid {
		auth.CheckPassword(dummyBcryptHash, "x")
		return nil, ErrInvalidCredentials
	}

	// Compare password.
	if err := auth.CheckPassword(user.PasswordHash.String, p.Password); err != nil {
		return nil, ErrInvalidCredentials
	}

	// Generate session.
	rawToken, tokenHash, err := auth.GenerateSessionToken()
	if err != nil {
		return nil, fmt.Errorf("generate session token: %w", err)
	}

	// Parse client IP.
	ipAddr := parseClientIP(p.IP)

	_, err = queries.CreateAuthSession(ctx, db.CreateAuthSessionParams{
		UserID:    user.ID,
		TokenHash: tokenHash,
		ExpiresAt: time.Now().Add(b.cfg.Auth.SessionTTL),
		IpAddress: ipAddr,
		UserAgent: pgtype.Text{String: p.UserAgent, Valid: p.UserAgent != ""},
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
	signer := auth.NewTokenSigner(auth.DeriveKey(b.cfg.Auth.TokenSecret, "hmac-tokens"))
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
// Returns ErrUserNotFound when the email is not registered (caller decides HTTP policy).
// Returns a wrapped error for any DB or system failure.
func (b *Backend) ForgotPassword(ctx context.Context, email string) error {
	email = strings.TrimSpace(strings.ToLower(email))

	queries := db.New(b.Pool)
	user, err := queries.GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrUserNotFound
		}
		return fmt.Errorf("forgot password: lookup user: %w", err)
	}

	signer := auth.NewTokenSigner(auth.DeriveKey(b.cfg.Auth.TokenSecret, "hmac-tokens"))
	token, err := signer.Sign(user.ID, "reset-password", b.cfg.Auth.ResetTokenTTL)
	if err != nil {
		return fmt.Errorf("forgot password: sign token: %w", err)
	}
	resetURL := b.cfg.Auth.BaseURL + "/reset-password?token=" + token

	_, err = b.Jobs.Insert(ctx, jobs.SendEmailArgs{
		To:      user.Email,
		Subject: "Reset your Drill password",
		Text:    "Click here to reset your password: " + resetURL,
		HTML:    "<p>Click <a href=\"" + resetURL + "\">here</a> to reset your password.</p>",
	}, jobs.SendEmailInsertOpts(&b.cfg.Email))
	if err != nil {
		return fmt.Errorf("forgot password: enqueue email: %w", err)
	}
	return nil
}

// ResetPassword verifies the reset token, updates the password, and deletes
// all sessions for the user. Returns ErrPasswordLength or ErrInvalidToken on
// validation failures.
func (b *Backend) ResetPassword(ctx context.Context, p ResetPasswordParams) error {
	if len(p.NewPassword) < 8 || len(p.NewPassword) > 128 {
		return ErrPasswordLength
	}

	signer := auth.NewTokenSigner(auth.DeriveKey(b.cfg.Auth.TokenSecret, "hmac-tokens"))
	userID, err := signer.Verify(p.Token, "reset-password")
	if err != nil {
		return ErrInvalidToken
	}

	hash, err := auth.HashPassword(p.NewPassword, b.cfg.Auth.BcryptCost)
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
	if err := queries.DeleteUserAuthSessions(ctx, userID); err != nil {
		return fmt.Errorf("delete user sessions: %w", err)
	}
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
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}
