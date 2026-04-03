package backend

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/jobs"
)

// ---------------------------------------------------------------------------
// Signup
// ---------------------------------------------------------------------------

func TestSignup_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	pool := setupTestDB(t)
	b, rj := newTestBackend(t, pool)
	ctx := context.Background()

	res, err := b.Signup(ctx, SignupParams{
		Email:       "alice@example.com",
		Password:    "strongpass1",
		DisplayName: "Alice",
	})
	require.NoError(t, err)
	require.NotEmpty(t, res.UserID)
	require.Equal(t, "alice@example.com", res.Email)

	// Verification email should have been enqueued.
	inserted := rj.Inserted()
	require.Len(t, inserted, 1)
	emailArgs, ok := inserted[0].(jobs.SendEmailArgs)
	require.True(t, ok)
	require.Equal(t, "alice@example.com", emailArgs.To)
	require.Contains(t, emailArgs.Subject, "Verify")
}

func TestSignup_MissingFields(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	pool := setupTestDB(t)
	b, _ := newTestBackend(t, pool)
	ctx := context.Background()

	cases := []struct {
		name string
		p    SignupParams
	}{
		{"no email", SignupParams{Email: "", Password: "strongpass1", DisplayName: "A"}},
		{"no password", SignupParams{Email: "a@b.com", Password: "", DisplayName: "A"}},
		{"no display name", SignupParams{Email: "a@b.com", Password: "strongpass1", DisplayName: ""}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := b.Signup(ctx, tc.p)
			require.ErrorIs(t, err, ErrMissingFields)
		})
	}
}

func TestSignup_ShortPassword(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	pool := setupTestDB(t)
	b, _ := newTestBackend(t, pool)
	ctx := context.Background()

	_, err := b.Signup(ctx, SignupParams{
		Email:       "short@example.com",
		Password:    string(make([]byte, MinPasswordLen-1)), // too short
		DisplayName: "Short",
	})
	require.ErrorIs(t, err, ErrPasswordLength)
}

func TestSignup_LongPassword(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	pool := setupTestDB(t)
	b, _ := newTestBackend(t, pool)
	ctx := context.Background()

	longPass := make([]byte, MaxPasswordLen+1)
	for i := range longPass {
		longPass[i] = 'a'
	}
	_, err := b.Signup(ctx, SignupParams{
		Email:       "long@example.com",
		Password:    string(longPass),
		DisplayName: "Long",
	})
	require.ErrorIs(t, err, ErrPasswordLength)
}

func TestSignup_DuplicateEmail(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	pool := setupTestDB(t)
	b, _ := newTestBackend(t, pool)
	ctx := context.Background()

	_, err := b.Signup(ctx, SignupParams{
		Email:       "dup@example.com",
		Password:    "strongpass1",
		DisplayName: "Dup1",
	})
	require.NoError(t, err)

	_, err = b.Signup(ctx, SignupParams{
		Email:       "dup@example.com",
		Password:    "strongpass2",
		DisplayName: "Dup2",
	})
	require.ErrorIs(t, err, ErrDuplicateEmail)
}

func TestSignup_EmailNormalization(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	pool := setupTestDB(t)
	b, _ := newTestBackend(t, pool)
	ctx := context.Background()

	res, err := b.Signup(ctx, SignupParams{
		Email:       "  UPPER@EXAMPLE.COM  ",
		Password:    "strongpass1",
		DisplayName: "Upper",
	})
	require.NoError(t, err)
	require.Equal(t, "upper@example.com", res.Email)
}

func TestSignup_DisplayNameTrimming(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	pool := setupTestDB(t)
	b, _ := newTestBackend(t, pool)
	ctx := context.Background()

	res, err := b.Signup(ctx, SignupParams{
		Email:       "trim@example.com",
		Password:    "strongpass1",
		DisplayName: "  Trimmed  ",
	})
	require.NoError(t, err)

	// Verify the display name was trimmed by reading back from DB.
	queries := db.New(pool)
	user, err := queries.GetUserByEmail(ctx, res.Email)
	require.NoError(t, err)
	require.Equal(t, "Trimmed", user.DisplayName)
}

// ---------------------------------------------------------------------------
// Login
// ---------------------------------------------------------------------------

// signupUser is a helper that creates a user via Signup and returns the result.
func signupUser(t *testing.T, b *Backend, email, password, name string) *SignupResult {
	t.Helper()
	res, err := b.Signup(context.Background(), SignupParams{
		Email:       email,
		Password:    password,
		DisplayName: name,
	})
	require.NoError(t, err)
	return res
}

func TestLogin_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	pool := setupTestDB(t)
	b, _ := newTestBackend(t, pool)
	ctx := context.Background()

	signupUser(t, b, "login@example.com", "strongpass1", "Login")

	res, err := b.Login(ctx, LoginParams{
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
	queries := db.New(pool)
	session, err := queries.GetAuthSessionByToken(ctx, tokenHash)
	require.NoError(t, err)
	require.Equal(t, res.UserID, session.UserID)
}

func TestLogin_WrongPassword(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	pool := setupTestDB(t)
	b, _ := newTestBackend(t, pool)
	ctx := context.Background()

	signupUser(t, b, "wrong@example.com", "strongpass1", "Wrong")

	_, err := b.Login(ctx, LoginParams{
		Email:    "wrong@example.com",
		Password: "wrongpassword",
	})
	require.ErrorIs(t, err, ErrInvalidCredentials)
}

func TestLogin_NonExistentUser(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	pool := setupTestDB(t)
	b, _ := newTestBackend(t, pool)
	ctx := context.Background()

	_, err := b.Login(ctx, LoginParams{
		Email:    "nonexist@example.com",
		Password: "strongpass1",
	})
	require.ErrorIs(t, err, ErrInvalidCredentials)
}

func TestLogin_OAuthOnlyUser(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	pool := setupTestDB(t)
	b, _ := newTestBackend(t, pool)
	ctx := context.Background()

	// Create an OAuth-only user (no password_hash) directly via DB.
	queries := db.New(pool)
	user, err := queries.CreateOAuthUser(ctx, db.CreateOAuthUserParams{
		Email:       "oauth@example.com",
		DisplayName: "OAuth User",
	})
	require.NoError(t, err)
	require.False(t, user.PasswordHash.Valid)

	_, err = b.Login(ctx, LoginParams{
		Email:    "oauth@example.com",
		Password: "anypassword1",
	})
	require.ErrorIs(t, err, ErrInvalidCredentials)
}

// ---------------------------------------------------------------------------
// Logout
// ---------------------------------------------------------------------------

func TestLogout_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	pool := setupTestDB(t)
	b, _ := newTestBackend(t, pool)
	ctx := context.Background()

	signupUser(t, b, "logout@example.com", "strongpass1", "Logout")
	loginRes, err := b.Login(ctx, LoginParams{
		Email:    "logout@example.com",
		Password: "strongpass1",
		IP:       "127.0.0.1:1234",
	})
	require.NoError(t, err)

	// Logout should succeed.
	err = b.Logout(ctx, loginRes.Token)
	require.NoError(t, err)

	// Session should be deleted -- lookup by hash should fail.
	tokenHash := auth.HashSessionToken(loginRes.Token)
	queries := db.New(pool)
	_, err = queries.GetAuthSessionByToken(ctx, tokenHash)
	require.Error(t, err) // pgx.ErrNoRows
}

func TestLogout_NonExistentToken(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	pool := setupTestDB(t)
	b, _ := newTestBackend(t, pool)
	ctx := context.Background()

	// Logging out with a bogus token should not error.
	err := b.Logout(ctx, "completely-bogus-token")
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// VerifyEmail
// ---------------------------------------------------------------------------

func TestVerifyEmail_ValidToken(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	pool := setupTestDB(t)
	b, _ := newTestBackend(t, pool)
	ctx := context.Background()
	cfg := b.Config()

	res := signupUser(t, b, "verify@example.com", "strongpass1", "Verify")

	// User should not be verified yet.
	queries := db.New(pool)
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
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	pool := setupTestDB(t)
	b, _ := newTestBackend(t, pool)
	ctx := context.Background()

	err := b.VerifyEmail(ctx, "invalid-token")
	require.ErrorIs(t, err, ErrInvalidToken)
}

func TestVerifyEmail_ExpiredToken(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	pool := setupTestDB(t)
	b, _ := newTestBackend(t, pool)
	ctx := context.Background()
	cfg := b.Config()

	res := signupUser(t, b, "expired@example.com", "strongpass1", "Expired")

	// Generate a token that already expired (negative TTL).
	signer := auth.NewTokenSigner(auth.DeriveKey(cfg.Auth.TokenSecret, "hmac-tokens"))
	token, err := signer.Sign(res.UserID, "verify-email", -time.Hour)
	require.NoError(t, err)

	err = b.VerifyEmail(ctx, token)
	require.ErrorIs(t, err, ErrInvalidToken)
}

// ---------------------------------------------------------------------------
// ForgotPassword
// ---------------------------------------------------------------------------

func TestForgotPassword_ExistingUser(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	pool := setupTestDB(t)
	b, rj := newTestBackend(t, pool)
	ctx := context.Background()

	signupUser(t, b, "forgot@example.com", "strongpass1", "Forgot")

	// Clear jobs from signup.
	_ = rj.Inserted()
	rj.mu.Lock()
	rj.inserted = nil
	rj.mu.Unlock()

	err := b.ForgotPassword(ctx, "forgot@example.com")
	require.NoError(t, err)

	inserted := rj.Inserted()
	require.Len(t, inserted, 1)
	emailArgs, ok := inserted[0].(jobs.SendEmailArgs)
	require.True(t, ok)
	require.Equal(t, "forgot@example.com", emailArgs.To)
	require.Contains(t, emailArgs.Subject, "Reset")
}

func TestForgotPassword_NonExistentUser(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	pool := setupTestDB(t)
	b, _ := newTestBackend(t, pool)
	ctx := context.Background()

	err := b.ForgotPassword(ctx, "nobody@example.com")
	require.ErrorIs(t, err, ErrUserNotFound)
}

// ---------------------------------------------------------------------------
// ResetPassword
// ---------------------------------------------------------------------------

func TestResetPassword_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	pool := setupTestDB(t)
	b, _ := newTestBackend(t, pool)
	ctx := context.Background()
	cfg := b.Config()

	signupRes := signupUser(t, b, "reset@example.com", "oldpassword1", "Reset")

	// Login to create a session.
	loginRes, err := b.Login(ctx, LoginParams{
		Email:    "reset@example.com",
		Password: "oldpassword1",
	})
	require.NoError(t, err)

	// Generate a reset token.
	signer := auth.NewTokenSigner(auth.DeriveKey(cfg.Auth.TokenSecret, "hmac-tokens"))
	token, err := signer.Sign(signupRes.UserID, "reset-password", cfg.Auth.ResetTokenTTL)
	require.NoError(t, err)

	// Reset password.
	err = b.ResetPassword(ctx, ResetPasswordParams{
		Token:       token,
		NewPassword: "newpassword1",
	})
	require.NoError(t, err)

	// Old session should be deleted.
	tokenHash := auth.HashSessionToken(loginRes.Token)
	queries := db.New(pool)
	_, err = queries.GetAuthSessionByToken(ctx, tokenHash)
	require.Error(t, err) // session deleted

	// Can login with new password.
	newLoginRes, err := b.Login(ctx, LoginParams{
		Email:    "reset@example.com",
		Password: "newpassword1",
	})
	require.NoError(t, err)
	require.NotEmpty(t, newLoginRes.Token)

	// Cannot login with old password.
	_, err = b.Login(ctx, LoginParams{
		Email:    "reset@example.com",
		Password: "oldpassword1",
	})
	require.ErrorIs(t, err, ErrInvalidCredentials)
}

func TestResetPassword_InvalidToken(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	pool := setupTestDB(t)
	b, _ := newTestBackend(t, pool)
	ctx := context.Background()

	err := b.ResetPassword(ctx, ResetPasswordParams{
		Token:       "bogus-token",
		NewPassword: "newpassword1",
	})
	require.ErrorIs(t, err, ErrInvalidToken)
}

func TestResetPassword_ShortPassword(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	pool := setupTestDB(t)
	b, _ := newTestBackend(t, pool)
	ctx := context.Background()
	cfg := b.Config()

	signupRes := signupUser(t, b, "resetshort@example.com", "strongpass1", "ResetShort")

	signer := auth.NewTokenSigner(auth.DeriveKey(cfg.Auth.TokenSecret, "hmac-tokens"))
	token, err := signer.Sign(signupRes.UserID, "reset-password", cfg.Auth.ResetTokenTTL)
	require.NoError(t, err)

	err = b.ResetPassword(ctx, ResetPasswordParams{
		Token:       token,
		NewPassword: "short", // too short
	})
	require.ErrorIs(t, err, ErrPasswordLength)
}

// ---------------------------------------------------------------------------
// Login with email normalization
// ---------------------------------------------------------------------------

func TestLogin_EmailNormalization(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	pool := setupTestDB(t)
	b, _ := newTestBackend(t, pool)
	ctx := context.Background()

	signupUser(t, b, "norm@example.com", "strongpass1", "Norm")

	// Login with uppercase email.
	res, err := b.Login(ctx, LoginParams{
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
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	pool := setupTestDB(t)
	b, _ := newTestBackend(t, pool)
	ctx := context.Background()

	// Exactly MinPasswordLen chars: should succeed.
	minPass := make([]byte, MinPasswordLen)
	for i := range minPass {
		minPass[i] = 'a'
	}
	_, err := b.Signup(ctx, SignupParams{
		Email:       "bound-min@example.com",
		Password:    string(minPass),
		DisplayName: "BoundMin",
	})
	require.NoError(t, err)

	// Exactly MaxPasswordLen chars (bcrypt max): should succeed.
	maxPass := make([]byte, MaxPasswordLen)
	for i := range maxPass {
		maxPass[i] = 'x'
	}
	_, err = b.Signup(ctx, SignupParams{
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
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	pool := setupTestDB(t)
	b, rj := newTestBackend(t, pool)
	ctx := context.Background()

	signupUser(t, b, "fnorm@example.com", "strongpass1", "FNorm")

	// Clear signup job.
	rj.mu.Lock()
	rj.inserted = nil
	rj.mu.Unlock()

	err := b.ForgotPassword(ctx, "  FNORM@EXAMPLE.COM  ")
	require.NoError(t, err)

	inserted := rj.Inserted()
	require.Len(t, inserted, 1)
	emailArgs, ok := inserted[0].(jobs.SendEmailArgs)
	require.True(t, ok)
	require.Equal(t, "fnorm@example.com", emailArgs.To)
}

// ---------------------------------------------------------------------------
// Create OAuth user then try password login (no password_hash set)
// ---------------------------------------------------------------------------

func TestLogin_OAuthUserNoPassword(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	pool := setupTestDB(t)
	b, _ := newTestBackend(t, pool)
	ctx := context.Background()

	queries := db.New(pool)
	_, err := queries.CreateUser(ctx, db.CreateUserParams{
		Email:        "nopw@example.com",
		PasswordHash: pgtype.Text{Valid: false},
		DisplayName:  "NoPW",
	})
	require.NoError(t, err)

	_, err = b.Login(ctx, LoginParams{
		Email:    "nopw@example.com",
		Password: "anypassword1",
	})
	require.ErrorIs(t, err, ErrInvalidCredentials)
}
