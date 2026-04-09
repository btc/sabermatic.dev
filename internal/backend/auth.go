package backend

import (
	"context"
	"errors"
	"fmt"
	"html/template"
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
	"github.com/btc/drill/internal/branding"
	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/drilotel"
	intemail "github.com/btc/drill/internal/email"
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

// AuthSessionParams holds the parameters for createAuthSession.
type AuthSessionParams struct {
	UserID    uuid.UUID
	IP        string
	UserAgent string
}

// AuthSessionResult is returned by createAuthSession on success.
type AuthSessionResult struct {
	Token string
}

// createAuthSession generates a session token, stores the hashed token in the
// database, and returns the raw token. Works with any db.DBTX (pool or tx).
// Shared by Login and OAuthLogin.
func (b *Backend) createAuthSession(ctx context.Context, dbtx db.DBTX, p AuthSessionParams) (_ *AuthSessionResult, err error) {
	ctx, span := tracer.Start(ctx, "Backend.createAuthSession")
	defer func() { drilotel.End(span, err) }()

	rawToken, tokenHash, err := auth.GenerateSessionToken()
	if err != nil {
		return nil, fmt.Errorf("create auth session: generate token: %w", err)
	}

	_, err = db.New(dbtx).CreateAuthSession(ctx, db.CreateAuthSessionParams{
		UserID:    p.UserID,
		TokenHash: tokenHash,
		ExpiresAt: time.Now().Add(b.cfg.Auth.SessionTTL),
		IpAddress: parseClientIP(p.IP),
		UserAgent: pgtype.Text{String: p.UserAgent, Valid: p.UserAgent != ""},
	})
	if err != nil {
		return nil, fmt.Errorf("create auth session: %w", err)
	}

	return &AuthSessionResult{Token: rawToken}, nil
}

// Signup creates a new user account, hashes the password, and enqueues a
// verification email. User creation and email enqueue are atomic: both
// succeed or both roll back. Returns ErrPasswordLength or ErrDuplicateEmail
// on validation/constraint failures.
func (b *Backend) Signup(ctx context.Context, p SignupParams) (_ *SignupResult, err error) {
	ctx, span := tracer.Start(ctx, "Backend.Signup")
	defer func() { drilotel.End(span, err) }()

	// Normalize.
	p.Email = normalizeEmail(p.Email)
	p.DisplayName = strings.TrimSpace(p.DisplayName)

	if p.Email == "" || p.Password == "" || p.DisplayName == "" {
		return nil, ErrMissingFields
	}
	if err := ValidatePasswordLength(p.Password); err != nil {
		return nil, err
	}

	// Hash password.
	hash, err := auth.HashPassword(p.Password, b.cfg.Auth.BcryptCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	// Transaction: create user + enqueue verification email atomically.
	tx, err := b.pool.Begin(ctx)
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

	// Provision the new user's account (free trial grant, etc.).
	if err := b.provisionNewUser(ctx, tx, user.ID); err != nil {
		return nil, fmt.Errorf("provision new user: %w", err)
	}

	// Enqueue verification email within the same transaction.
	signer := auth.NewTokenSigner(auth.DeriveKey(b.cfg.Auth.TokenSecret, "hmac-tokens"))
	token, err := signer.Sign(user.ID, "verify-email", b.cfg.Auth.VerifyTokenTTL)
	if err != nil {
		return nil, fmt.Errorf("sign verification token: %w", err)
	}
	verifyURL := fmt.Sprintf("%s/verify-email?token=%s", b.cfg.Auth.BaseURL, token)
	verifyBody := template.HTML(fmt.Sprintf(
		`<p>Click the button below to verify your email address.</p>
		<p style="margin:24px 0;"><a href="%s" style="display:inline-block;padding:12px 24px;background:#b45309;color:#fff;text-decoration:none;border-radius:6px;font-weight:500;">Verify email</a></p>
		<p style="font-size:13px;color:#666;">Or copy this link: %s</p>`,
		verifyURL, verifyURL))
	verifyHTML, err := intemail.RenderEmail(verifyBody, fmt.Sprintf("You received this email because you signed up for %s.", branding.AppName))
	if err != nil {
		slog.Error("render verification email template", "error", err)
		verifyHTML = string(verifyBody) // fallback to unwrapped body
	}
	emailOpts := jobs.SendEmailInsertOpts(&b.cfg.Email)
	drilotel.SetTraceMetadata(ctx, emailOpts)
	_, err = b.jobs.InsertTx(ctx, tx, jobs.SendEmailArgs{
		To:      user.Email,
		Subject: fmt.Sprintf("Verify your %s account", branding.AppName),
		Text:    fmt.Sprintf("Click here to verify your email: %s", verifyURL),
		HTML:    verifyHTML,
	}, emailOpts)
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
func (b *Backend) Login(ctx context.Context, p LoginParams) (_ *LoginResult, err error) {
	ctx, span := tracer.Start(ctx, "Backend.Login")
	defer func() { drilotel.End(span, err) }()

	p.Email = normalizeEmail(p.Email)

	// Look up user.
	queries := db.New(b.pool)
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

	sess, err := b.createAuthSession(ctx, b.pool, AuthSessionParams{
		UserID:    user.ID,
		IP:        p.IP,
		UserAgent: p.UserAgent,
	})
	if err != nil {
		return nil, fmt.Errorf("login: %w", err)
	}

	return &LoginResult{
		UserID: user.ID,
		Email:  user.Email,
		Token:  sess.Token,
	}, nil
}

// Logout invalidates the session identified by the given raw token.
// Errors are best-effort (always returns nil).
func (b *Backend) Logout(ctx context.Context, sessionToken string) (err error) {
	ctx, span := tracer.Start(ctx, "Backend.Logout")
	defer func() { drilotel.End(span, err) }()

	tokenHash := auth.HashSessionToken(sessionToken)
	queries := db.New(b.pool)

	session, err := queries.GetAuthSessionByToken(ctx, tokenHash)
	if err == nil {
		if delErr := queries.DeleteAuthSession(ctx, session.ID); delErr != nil {
			slog.Error("delete auth session", "error", delErr)
		}
	}
	return nil
}

// DeleteAccount soft-deletes the user and wipes all auth sessions atomically.
// Idempotent: calling on an already-deleted user is a no-op.
func (b *Backend) DeleteAccount(ctx context.Context, userID uuid.UUID) (err error) {
	ctx, span := tracer.Start(ctx, "Backend.DeleteAccount")
	defer func() { drilotel.End(span, err) }()

	if err := db.New(b.pool).DeleteAccount(ctx, userID); err != nil {
		return fmt.Errorf("delete account: %w", err)
	}
	return nil
}

// VerifyEmail marks a user's email as verified using the signed token.
// Returns ErrInvalidToken if the token is invalid or expired.
func (b *Backend) VerifyEmail(ctx context.Context, token string) (err error) {
	ctx, span := tracer.Start(ctx, "Backend.VerifyEmail")
	defer func() { drilotel.End(span, err) }()

	signer := auth.NewTokenSigner(auth.DeriveKey(b.cfg.Auth.TokenSecret, "hmac-tokens"))
	userID, err := signer.Verify(token, "verify-email")
	if err != nil {
		return ErrInvalidToken
	}

	queries := db.New(b.pool)
	if err := queries.VerifyUserEmail(ctx, userID); err != nil {
		return fmt.Errorf("verify email: %w", err)
	}
	return nil
}

// ForgotPassword enqueues a password-reset email if the user exists.
// Returns ErrUserNotFound when the email is not registered (caller decides HTTP policy).
// Returns a wrapped error for any DB or system failure.
func (b *Backend) ForgotPassword(ctx context.Context, email string) (err error) {
	ctx, span := tracer.Start(ctx, "Backend.ForgotPassword")
	defer func() { drilotel.End(span, err) }()

	email = normalizeEmail(email)

	queries := db.New(b.pool)
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

	ttlStr := intemail.FormatDurationHuman(b.cfg.Auth.ResetTokenTTL)
	resetBody := template.HTML(fmt.Sprintf(
		`<p>Click the button below to reset your password. This link expires in %s.</p>
		<p style="margin:24px 0;"><a href="%s" style="display:inline-block;padding:12px 24px;background:#b45309;color:#fff;text-decoration:none;border-radius:6px;font-weight:500;">Reset password</a></p>
		<p style="font-size:13px;color:#666;">Or copy this link: %s</p>`,
		ttlStr, resetURL, resetURL))
	resetHTML, err := intemail.RenderEmail(resetBody, fmt.Sprintf("You received this email because you requested a password reset for %s.", branding.AppName))
	if err != nil {
		slog.Error("render reset email template", "error", err)
		resetHTML = string(resetBody)
	}

	resetEmailOpts := jobs.SendEmailInsertOpts(&b.cfg.Email)
	drilotel.SetTraceMetadata(ctx, resetEmailOpts)
	_, err = b.jobs.Insert(ctx, jobs.SendEmailArgs{
		To:      user.Email,
		Subject: fmt.Sprintf("Reset your %s password", branding.AppName),
		Text:    "Click here to reset your password: " + resetURL,
		HTML:    resetHTML,
	}, resetEmailOpts)
	if err != nil {
		return fmt.Errorf("forgot password: enqueue email: %w", err)
	}
	return nil
}

// ResetPassword verifies the reset token, updates the password, and deletes
// all sessions for the user. Returns ErrPasswordLength or ErrInvalidToken on
// validation failures.
func (b *Backend) ResetPassword(ctx context.Context, p ResetPasswordParams) (err error) {
	ctx, span := tracer.Start(ctx, "Backend.ResetPassword")
	defer func() { drilotel.End(span, err) }()

	if err := ValidatePasswordLength(p.NewPassword); err != nil {
		return err
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

	queries := db.New(b.pool)
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

// provisionNewUser sets up a newly created user's account within the
// registration transaction. Currently provisions the one-time free trial grant.
func (b *Backend) provisionNewUser(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	return b.EnsureFreeGrantTx(ctx, tx, userID)
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

// normalizeEmail lowercases and trims whitespace from an email address.
func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
