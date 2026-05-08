package backend_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/backend"
	"github.com/btc/drill/internal/backendtest"
	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/jobs"
)

// riverJobs returns all river_job rows matching the given kind, newest first.
func riverJobs(t *testing.T, b *backend.Backend, kind string) []json.RawMessage {
	t.Helper()
	rows, err := b.Pool().Query(context.Background(),
		"SELECT args FROM river_job WHERE kind = $1 ORDER BY created_at DESC", kind)
	require.NoError(t, err)
	defer rows.Close()

	var out []json.RawMessage
	for rows.Next() {
		var args json.RawMessage
		require.NoError(t, rows.Scan(&args))
		out = append(out, args)
	}
	require.NoError(t, rows.Err())
	return out
}

// ---------------------------------------------------------------------------
// Signup
// ---------------------------------------------------------------------------

func TestSignup_Success(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	res, err := b.Signup(ctx, backend.SignupParams{
		Email:       "alice@example.com",
		Password:    "strongpass1",
		DisplayName: "Alice",
	})
	require.NoError(t, err)
	require.NotEmpty(t, res.UserID)
	require.Equal(t, "alice@example.com", res.Email)

	// Verification email should have been enqueued in River.
	rows := riverJobs(t, b, "send_email")
	require.Len(t, rows, 1)
	var emailArgs jobs.SendEmailArgs
	require.NoError(t, json.Unmarshal(rows[0], &emailArgs))
	require.Equal(t, "alice@example.com", emailArgs.To)
	require.Contains(t, emailArgs.Subject, "Verify")
}

func TestSignup_MissingFields(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	cases := []struct {
		name string
		p    backend.SignupParams
	}{
		{"no email", backend.SignupParams{Email: "", Password: "strongpass1", DisplayName: "A"}},
		{"no password", backend.SignupParams{Email: "a@b.com", Password: "", DisplayName: "A"}},
		{"no display name", backend.SignupParams{Email: "a@b.com", Password: "strongpass1", DisplayName: ""}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := b.Signup(ctx, tc.p)
			require.ErrorIs(t, err, backend.ErrMissingFields)
		})
	}
}

func TestSignup_ShortPassword(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	_, err := b.Signup(ctx, backend.SignupParams{
		Email:       "short@example.com",
		Password:    string(make([]byte, backend.MinPasswordLen-1)), // too short
		DisplayName: "Short",
	})
	require.ErrorIs(t, err, backend.ErrPasswordLength)
}

func TestSignup_LongPassword(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	longPass := make([]byte, backend.MaxPasswordLen+1)
	for i := range longPass {
		longPass[i] = 'a'
	}
	_, err := b.Signup(ctx, backend.SignupParams{
		Email:       "long@example.com",
		Password:    string(longPass),
		DisplayName: "Long",
	})
	require.ErrorIs(t, err, backend.ErrPasswordLength)
}

func TestSignup_DuplicateEmail(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	_, err := b.Signup(ctx, backend.SignupParams{
		Email:       "dup@example.com",
		Password:    "strongpass1",
		DisplayName: "Dup1",
	})
	require.NoError(t, err)

	_, err = b.Signup(ctx, backend.SignupParams{
		Email:       "dup@example.com",
		Password:    "strongpass2",
		DisplayName: "Dup2",
	})
	require.ErrorIs(t, err, backend.ErrDuplicateEmail)
}

func TestSignup_EmailNormalization(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	res, err := b.Signup(ctx, backend.SignupParams{
		Email:       "  UPPER@EXAMPLE.COM  ",
		Password:    "strongpass1",
		DisplayName: "Upper",
	})
	require.NoError(t, err)
	require.Equal(t, "upper@example.com", res.Email)
}

func TestSignup_DisplayNameTrimming(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	res, err := b.Signup(ctx, backend.SignupParams{
		Email:       "trim@example.com",
		Password:    "strongpass1",
		DisplayName: "  Trimmed  ",
	})
	require.NoError(t, err)

	// Verify the display name was trimmed by reading back from DB.
	queries := db.New(b.Pool())
	user, err := queries.GetUserByEmail(ctx, res.Email)
	require.NoError(t, err)
	require.Equal(t, "Trimmed", user.DisplayName)
}

// ---------------------------------------------------------------------------
// Login
// ---------------------------------------------------------------------------

// signupUser is a helper that creates a user via Signup and returns the result.
func signupUser(t *testing.T, b *backend.Backend, email, password, name string) *backend.SignupResult {
	t.Helper()
	res, err := b.Signup(context.Background(), backend.SignupParams{
		Email:       email,
		Password:    password,
		DisplayName: name,
	})
	require.NoError(t, err)
	return res
}

func TestLogin_Success(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	signupUser(t, b, "login@example.com", "strongpass1", "Login")

	res, err := b.Login(ctx, backend.LoginParams{
		Email:     "login@example.com",
		Password:  "strongpass1",
		IP:        "192.168.1.1:12345",
		UserAgent: "test-agent",
	})
	require.NoError(t, err)
	require.NotEmpty(t, res.Token)
	require.Equal(t, "login@example.com", res.Email)
	require.NotEmpty(t, res.UserID)

	// Verify a session was created by looking it up.
	tokenHash := auth.HashSessionToken(res.Token)
	queries := db.New(b.Pool())
	session, err := queries.GetAuthSessionByToken(ctx, tokenHash)
	require.NoError(t, err)
	require.Equal(t, res.UserID, session.UserID)
}

func TestLogin_WrongPassword(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	signupUser(t, b, "wrong@example.com", "strongpass1", "Wrong")

	_, err := b.Login(ctx, backend.LoginParams{
		Email:    "wrong@example.com",
		Password: "wrongpassword",
	})
	require.ErrorIs(t, err, backend.ErrInvalidCredentials)
}

func TestLogin_NonExistentUser(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	_, err := b.Login(ctx, backend.LoginParams{
		Email:    "nonexist@example.com",
		Password: "strongpass1",
	})
	require.ErrorIs(t, err, backend.ErrInvalidCredentials)
}

func TestLogin_OAuthOnlyUser(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	// Create an OAuth-only user (no password_hash) directly via DB.
	queries := db.New(b.Pool())
	user, err := queries.CreateOAuthUser(ctx, db.CreateOAuthUserParams{
		Email:       "oauth@example.com",
		DisplayName: "OAuth User",
	})
	require.NoError(t, err)
	require.False(t, user.PasswordHash.Valid)

	_, err = b.Login(ctx, backend.LoginParams{
		Email:    "oauth@example.com",
		Password: "anypassword1",
	})
	require.ErrorIs(t, err, backend.ErrInvalidCredentials)
}

// ---------------------------------------------------------------------------
// Logout
// ---------------------------------------------------------------------------

func TestLogout_Success(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	signupUser(t, b, "logout@example.com", "strongpass1", "Logout")
	loginRes, err := b.Login(ctx, backend.LoginParams{
		Email:    "logout@example.com",
		Password: "strongpass1",
		IP:       "127.0.0.1:1234",
	})
	require.NoError(t, err)

	// Logout should succeed.
	err = b.Logout(ctx, loginRes.Token)
	require.NoError(t, err)

	// Session should be revoked (soft-deleted) -- lookup by token should fail
	// because GetAuthSessionByToken filters on revoked_at IS NULL.
	tokenHash := auth.HashSessionToken(loginRes.Token)
	queries := db.New(b.Pool())
	_, err = queries.GetAuthSessionByToken(ctx, tokenHash)
	require.Error(t, err) // pgx.ErrNoRows — revoked session is invisible
}

func TestLogout_NonExistentToken(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	// Logging out with a bogus token should not error.
	err := b.Logout(ctx, "completely-bogus-token")
	require.NoError(t, err)
}

func TestLogout_SoftDeletes_PreservesActivityHistory(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	signupUser(t, b, "softlogout@example.com", "strongpass1", "SoftLogout")
	loginRes, err := b.Login(ctx, backend.LoginParams{
		Email:    "softlogout@example.com",
		Password: "strongpass1",
		IP:       "127.0.0.1:1234",
	})
	require.NoError(t, err)

	// Logout should soft-delete the session.
	require.NoError(t, b.Logout(ctx, loginRes.Token))

	// GetAuthSessionByToken should no longer find it (revoked_at IS NULL filter).
	tokenHash := auth.HashSessionToken(loginRes.Token)
	queries := db.New(b.Pool())
	_, err = queries.GetAuthSessionByToken(ctx, tokenHash)
	require.Error(t, err, "revoked session should not be returned by GetAuthSessionByToken")

	// The session row should still exist with revoked_at set (soft delete).
	var revokedAt pgtype.Timestamptz
	err = b.Pool().QueryRow(ctx,
		`SELECT revoked_at FROM auth_sessions WHERE user_id = $1`,
		loginRes.UserID).Scan(&revokedAt)
	require.NoError(t, err)
	require.True(t, revokedAt.Valid, "revoked_at should be set after logout")

	// GetUserLastActive should still return a value (reads across revoked sessions).
	last, err := queries.GetUserLastActive(ctx, loginRes.UserID)
	require.NoError(t, err)
	require.True(t, last.Valid, "last_active should have a value across revoked sessions")
}

func TestGetUserLastActive_NoSessions(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()
	// SeedUser calls Signup only — no auth_sessions row is created, so no
	// DELETE needed to reach the "zero sessions" precondition.
	userID := backendtest.SeedUser(t, b)

	last, err := db.New(b.Pool()).GetUserLastActive(ctx, userID)
	require.NoError(t, err)
	require.False(t, last.Valid, "MAX over zero rows should be NULL")
}

// ---------------------------------------------------------------------------
// VerifyEmail
// ---------------------------------------------------------------------------

func TestVerifyEmail_ValidToken(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()
	cfg := b.Config()

	res := signupUser(t, b, "verify@example.com", "strongpass1", "Verify")

	// User should not be verified yet.
	queries := db.New(b.Pool())
	user, err := queries.GetUserByEmail(ctx, "verify@example.com")
	require.NoError(t, err)
	require.False(t, user.EmailVerified)

	// Generate a valid verify-email token.
	signer := auth.NewTokenSigner(auth.DeriveKey(cfg.Auth.TokenSecret, "hmac-tokens"))
	token, err := signer.Sign(res.UserID, "verify-email", cfg.Auth.VerifyTokenTTL)
	require.NoError(t, err)

	err = b.VerifyEmail(ctx, token)
	require.NoError(t, err)

	// User should now be verified.
	user, err = queries.GetUserByEmail(ctx, "verify@example.com")
	require.NoError(t, err)
	require.True(t, user.EmailVerified)
}

func TestVerifyEmail_InvalidToken(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	err := b.VerifyEmail(ctx, "invalid-token")
	require.ErrorIs(t, err, backend.ErrInvalidToken)
}

func TestVerifyEmail_ExpiredToken(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()
	cfg := b.Config()

	res := signupUser(t, b, "expired@example.com", "strongpass1", "Expired")

	// Generate a token that already expired (negative TTL).
	signer := auth.NewTokenSigner(auth.DeriveKey(cfg.Auth.TokenSecret, "hmac-tokens"))
	token, err := signer.Sign(res.UserID, "verify-email", -time.Hour)
	require.NoError(t, err)

	err = b.VerifyEmail(ctx, token)
	require.ErrorIs(t, err, backend.ErrInvalidToken)
}

// ---------------------------------------------------------------------------
// ForgotPassword
// ---------------------------------------------------------------------------

func TestForgotPassword_ExistingUser(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	signupUser(t, b, "forgot@example.com", "strongpass1", "Forgot")

	err := b.ForgotPassword(ctx, "forgot@example.com")
	require.NoError(t, err)

	// Should have 2 jobs total: 1 verification email from signup + 1 reset email.
	rows := riverJobs(t, b, "send_email")
	require.Len(t, rows, 2)

	// Most recent (first in list) should be the reset email.
	var emailArgs jobs.SendEmailArgs
	require.NoError(t, json.Unmarshal(rows[0], &emailArgs))
	require.Equal(t, "forgot@example.com", emailArgs.To)
	require.Contains(t, emailArgs.Subject, "Reset")
}

func TestForgotPassword_NonExistentUser(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	err := b.ForgotPassword(ctx, "nobody@example.com")
	require.ErrorIs(t, err, backend.ErrUserNotFound)
}

// ---------------------------------------------------------------------------
// ResetPassword
// ---------------------------------------------------------------------------

func TestResetPassword_Success(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()
	cfg := b.Config()

	signupRes := signupUser(t, b, "reset@example.com", "oldpassword1", "Reset")

	// Login to create a session.
	loginRes, err := b.Login(ctx, backend.LoginParams{
		Email:    "reset@example.com",
		Password: "oldpassword1",
	})
	require.NoError(t, err)

	// Generate a reset token.
	signer := auth.NewTokenSigner(auth.DeriveKey(cfg.Auth.TokenSecret, "hmac-tokens"))
	token, err := signer.Sign(signupRes.UserID, "reset-password", cfg.Auth.ResetTokenTTL)
	require.NoError(t, err)

	// Reset password.
	err = b.ResetPassword(ctx, backend.ResetPasswordParams{
		Token:       token,
		NewPassword: "newpassword1",
	})
	require.NoError(t, err)

	// Old session should be deleted.
	tokenHash := auth.HashSessionToken(loginRes.Token)
	queries := db.New(b.Pool())
	_, err = queries.GetAuthSessionByToken(ctx, tokenHash)
	require.Error(t, err) // session revoked (soft-deleted) — invisible to token lookup

	// Can login with new password.
	newLoginRes, err := b.Login(ctx, backend.LoginParams{
		Email:    "reset@example.com",
		Password: "newpassword1",
	})
	require.NoError(t, err)
	require.NotEmpty(t, newLoginRes.Token)

	// Cannot login with old password.
	_, err = b.Login(ctx, backend.LoginParams{
		Email:    "reset@example.com",
		Password: "oldpassword1",
	})
	require.ErrorIs(t, err, backend.ErrInvalidCredentials)
}

// ---------------------------------------------------------------------------
// AuthenticateSession
// ---------------------------------------------------------------------------

func TestAuthenticateSession_ProjectsSubState(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	signupRes := signupUser(t, b, "substate@example.com", "testpassword123", "SubState")

	// Force the cache columns to known values.
	_, err := b.Pool().Exec(ctx, `
		UPDATE users
		SET sub_cancel_at_period_end = TRUE,
		    sub_cancel_is_auto       = TRUE,
		    pending_kept_banner      = TRUE
		WHERE id = $1`, signupRes.UserID)
	require.NoError(t, err)

	loginRes, err := b.Login(ctx, backend.LoginParams{
		Email:    "substate@example.com",
		Password: "testpassword123",
	})
	require.NoError(t, err)

	tokenHash := auth.HashSessionToken(loginRes.Token)

	user, err := b.AuthenticateSession(ctx, tokenHash)
	require.NoError(t, err)
	require.True(t, user.SubCancelAtPeriodEnd)
	require.True(t, user.SubCancelIsAuto)
	require.True(t, user.PendingKeptBanner)
}

func TestResetPassword_InvalidToken(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	err := b.ResetPassword(ctx, backend.ResetPasswordParams{
		Token:       "bogus-token",
		NewPassword: "newpassword1",
	})
	require.ErrorIs(t, err, backend.ErrInvalidToken)
}

func TestResetPassword_ShortPassword(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()
	cfg := b.Config()

	signupRes := signupUser(t, b, "resetshort@example.com", "strongpass1", "ResetShort")

	signer := auth.NewTokenSigner(auth.DeriveKey(cfg.Auth.TokenSecret, "hmac-tokens"))
	token, err := signer.Sign(signupRes.UserID, "reset-password", cfg.Auth.ResetTokenTTL)
	require.NoError(t, err)

	err = b.ResetPassword(ctx, backend.ResetPasswordParams{
		Token:       token,
		NewPassword: "short", // too short
	})
	require.ErrorIs(t, err, backend.ErrPasswordLength)
}

// ---------------------------------------------------------------------------
// Login with email normalization
// ---------------------------------------------------------------------------

func TestLogin_EmailNormalization(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	signupUser(t, b, "norm@example.com", "strongpass1", "Norm")

	// Login with uppercase email.
	res, err := b.Login(ctx, backend.LoginParams{
		Email:    "  NORM@EXAMPLE.COM  ",
		Password: "strongpass1",
	})
	require.NoError(t, err)
	require.Equal(t, "norm@example.com", res.Email)
}

// ---------------------------------------------------------------------------
// Signup exactly MinPasswordLen and MaxPasswordLen char passwords (boundary)
// ---------------------------------------------------------------------------

func TestSignup_PasswordBoundary(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	// Exactly MinPasswordLen chars: should succeed.
	minPass := make([]byte, backend.MinPasswordLen)
	for i := range minPass {
		minPass[i] = 'a'
	}
	_, err := b.Signup(ctx, backend.SignupParams{
		Email:       "bound-min@example.com",
		Password:    string(minPass),
		DisplayName: "BoundMin",
	})
	require.NoError(t, err)

	// Exactly MaxPasswordLen chars (bcrypt max): should succeed.
	maxPass := make([]byte, backend.MaxPasswordLen)
	for i := range maxPass {
		maxPass[i] = 'x'
	}
	_, err = b.Signup(ctx, backend.SignupParams{
		Email:       "bound-max@example.com",
		Password:    string(maxPass),
		DisplayName: "BoundMax",
	})
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// ForgotPassword email normalization
// ---------------------------------------------------------------------------

func TestForgotPassword_EmailNormalization(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	signupUser(t, b, "fnorm@example.com", "strongpass1", "FNorm")

	err := b.ForgotPassword(ctx, "  FNORM@EXAMPLE.COM  ")
	require.NoError(t, err)

	// Most recent job should be the reset email to the normalized address.
	rows := riverJobs(t, b, "send_email")
	require.GreaterOrEqual(t, len(rows), 2) // signup verify + forgot reset
	var emailArgs jobs.SendEmailArgs
	require.NoError(t, json.Unmarshal(rows[0], &emailArgs))
	require.Equal(t, "fnorm@example.com", emailArgs.To)
}

// ---------------------------------------------------------------------------
// Create OAuth user then try password login (no password_hash set)
// ---------------------------------------------------------------------------

func TestLogin_OAuthUserNoPassword(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	queries := db.New(b.Pool())
	_, err := queries.CreateUser(ctx, db.CreateUserParams{
		Email:        "nopw@example.com",
		PasswordHash: pgtype.Text{Valid: false},
		DisplayName:  "NoPW",
	})
	require.NoError(t, err)

	_, err = b.Login(ctx, backend.LoginParams{
		Email:    "nopw@example.com",
		Password: "anypassword1",
	})
	require.ErrorIs(t, err, backend.ErrInvalidCredentials)
}

// ---------------------------------------------------------------------------
// DeleteAccount
// ---------------------------------------------------------------------------

func TestDeleteAccount_Success(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	// Sign up and log in to create a session.
	signupRes := signupUser(t, b, "delete@example.com", "strongpass1", "Delete")
	loginRes, err := b.Login(ctx, backend.LoginParams{
		Email:    "delete@example.com",
		Password: "strongpass1",
		IP:       "127.0.0.1:1234",
	})
	require.NoError(t, err)

	// Delete the account.
	err = b.DeleteAccount(ctx, signupRes.UserID)
	require.NoError(t, err)

	queries := db.New(b.Pool())

	// GetUserByID (excludes deleted) should now fail.
	_, err = queries.GetUserByID(ctx, signupRes.UserID)
	require.Error(t, err, "GetUserByID should fail for a deleted user")

	// GetUserByIDIncludingDeleted should return the user with deleted_at set.
	user, err := queries.GetUserByIDIncludingDeleted(ctx, signupRes.UserID)
	require.NoError(t, err)
	require.True(t, user.DeletedAt.Valid, "deleted_at should be set")

	// Login should fail — auth sessions were wiped.
	tokenHash := auth.HashSessionToken(loginRes.Token)
	_, err = queries.GetAuthSessionByToken(ctx, tokenHash)
	require.Error(t, err, "auth session should have been deleted")

	_, err = b.Login(ctx, backend.LoginParams{
		Email:    "delete@example.com",
		Password: "strongpass1",
	})
	require.ErrorIs(t, err, backend.ErrInvalidCredentials)
}

func TestDeleteAccount_Idempotent(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	signupRes := signupUser(t, b, "delete-idem@example.com", "strongpass1", "DeleteIdem")

	err := b.DeleteAccount(ctx, signupRes.UserID)
	require.NoError(t, err)

	// Calling again should not return an error.
	err = b.DeleteAccount(ctx, signupRes.UserID)
	require.NoError(t, err)
}
