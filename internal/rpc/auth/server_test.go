package auth_test

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	iauth "github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/backend"
	"github.com/btc/drill/internal/db"
	drillv1 "github.com/btc/drill/internal/pb/drill/v1"
	"github.com/btc/drill/internal/pb/drill/v1/drillv1connect"
	authsvc "github.com/btc/drill/internal/rpc/auth"
)

// startAuthServer creates the Connect handler WITHOUT auth interceptor
// (public endpoints), starts an httptest.Server, and returns its URL.
func startAuthServer(t *testing.T, b *backend.Backend) string {
	t.Helper()
	_, h := drillv1connect.NewAuthServiceHandler(authsvc.NewServer(b))
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv.URL
}

// publicClient returns a Connect AuthServiceClient with no cookies.
func publicClient(srvURL string) drillv1connect.AuthServiceClient {
	return drillv1connect.NewAuthServiceClient(&http.Client{}, srvURL)
}

// clientWithCookies returns a Connect AuthServiceClient that stores cookies.
func clientWithCookies(t *testing.T, srvURL string) (drillv1connect.AuthServiceClient, *cookiejar.Jar) {
	t.Helper()
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	return drillv1connect.NewAuthServiceClient(
		&http.Client{Jar: jar},
		srvURL,
	), jar
}

func TestSignup_Success(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startAuthServer(t, b)
	client := publicClient(srvURL)

	resp, err := client.Signup(context.Background(), connect.NewRequest(&drillv1.SignupRequest{
		Email:       "alice@example.com",
		Password:    "securepass",
		DisplayName: "Alice",
	}))
	require.NoError(t, err)
	require.Equal(t, "alice@example.com", resp.Msg.Email)
	require.NotEmpty(t, resp.Msg.Id)
}

func TestSignup_DuplicateEmail(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startAuthServer(t, b)
	client := publicClient(srvURL)

	_, err := client.Signup(context.Background(), connect.NewRequest(&drillv1.SignupRequest{
		Email:       "dup@example.com",
		Password:    "securepass",
		DisplayName: "First",
	}))
	require.NoError(t, err)

	_, err = client.Signup(context.Background(), connect.NewRequest(&drillv1.SignupRequest{
		Email:       "dup@example.com",
		Password:    "securepass",
		DisplayName: "Second",
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeAlreadyExists, connect.CodeOf(err))
}

func TestSignup_MissingFields(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startAuthServer(t, b)
	client := publicClient(srvURL)

	_, err := client.Signup(context.Background(), connect.NewRequest(&drillv1.SignupRequest{
		Email:    "no-name@example.com",
		Password: "securepass",
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestLogin_Success(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startAuthServer(t, b)

	// Signup first.
	client := publicClient(srvURL)
	_, err := client.Signup(context.Background(), connect.NewRequest(&drillv1.SignupRequest{
		Email:       "bob@example.com",
		Password:    "securepass",
		DisplayName: "Bob",
	}))
	require.NoError(t, err)

	// Login with cookie jar to capture Set-Cookie.
	cookieClient, jar := clientWithCookies(t, srvURL)
	resp, err := cookieClient.Login(context.Background(), connect.NewRequest(&drillv1.LoginRequest{
		Email:    "bob@example.com",
		Password: "securepass",
	}))
	require.NoError(t, err)
	require.Equal(t, "bob@example.com", resp.Msg.Email)
	require.NotEmpty(t, resp.Msg.Id)

	// Verify session cookie was set.
	u, _ := url.Parse(srvURL)
	cookies := jar.Cookies(u)
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == iauth.SessionCookieName {
			sessionCookie = c
			break
		}
	}
	require.NotNil(t, sessionCookie, "expected session cookie to be set")
	require.NotEmpty(t, sessionCookie.Value)
}

func TestLogin_WrongPassword(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startAuthServer(t, b)
	client := publicClient(srvURL)

	_, err := client.Signup(context.Background(), connect.NewRequest(&drillv1.SignupRequest{
		Email:       "carol@example.com",
		Password:    "securepass",
		DisplayName: "Carol",
	}))
	require.NoError(t, err)

	_, err = client.Login(context.Background(), connect.NewRequest(&drillv1.LoginRequest{
		Email:    "carol@example.com",
		Password: "wrongpassword",
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

func TestLogout_ClearsCookie(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startAuthServer(t, b)
	client := publicClient(srvURL)

	// Signup and login.
	_, err := client.Signup(context.Background(), connect.NewRequest(&drillv1.SignupRequest{
		Email:       "logout@example.com",
		Password:    "securepass123",
		DisplayName: "Logout User",
	}))
	require.NoError(t, err)

	cookieClient, jar := clientWithCookies(t, srvURL)
	_, err = cookieClient.Login(context.Background(), connect.NewRequest(&drillv1.LoginRequest{
		Email:    "logout@example.com",
		Password: "securepass123",
	}))
	require.NoError(t, err)

	// Verify cookie was set.
	u, _ := url.Parse(srvURL)
	cookies := jar.Cookies(u)
	var found bool
	for _, c := range cookies {
		if c.Name == iauth.SessionCookieName {
			found = true
			break
		}
	}
	require.True(t, found, "expected session cookie after login")

	// Logout.
	_, err = cookieClient.Logout(context.Background(), connect.NewRequest(&drillv1.LogoutRequest{}))
	require.NoError(t, err)

	// After logout the cookie jar should have the cleared cookie (MaxAge=-1
	// causes the jar to remove the cookie).
	cookies = jar.Cookies(u)
	for _, c := range cookies {
		if c.Name == iauth.SessionCookieName {
			// If the jar still has it, it should be empty.
			require.Empty(t, c.Value)
		}
	}
}

func TestVerifyEmail(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	cfg := b.Config()
	srvURL := startAuthServer(t, b)
	client := publicClient(srvURL)

	// Signup.
	resp, err := client.Signup(context.Background(), connect.NewRequest(&drillv1.SignupRequest{
		Email:       "verify@example.com",
		Password:    "securepassword123",
		DisplayName: "Verify User",
	}))
	require.NoError(t, err)

	// Generate verification token.
	signer := iauth.NewTokenSigner(iauth.DeriveKey(cfg.Auth.TokenSecret, "hmac-tokens"))
	uid, err := uuid.Parse(resp.Msg.Id)
	require.NoError(t, err)
	token, err := signer.Sign(uid, "verify-email", cfg.Auth.VerifyTokenTTL)
	require.NoError(t, err)

	// Verify email.
	_, err = client.VerifyEmail(context.Background(), connect.NewRequest(&drillv1.VerifyEmailRequest{
		Token: token,
	}))
	require.NoError(t, err)
}

func TestVerifyEmail_InvalidToken(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startAuthServer(t, b)
	client := publicClient(srvURL)

	_, err := client.VerifyEmail(context.Background(), connect.NewRequest(&drillv1.VerifyEmailRequest{
		Token: "bad-token",
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestForgotAndResetPassword(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	cfg := b.Config()
	srvURL := startAuthServer(t, b)
	client := publicClient(srvURL)

	// Signup.
	_, err := client.Signup(context.Background(), connect.NewRequest(&drillv1.SignupRequest{
		Email:       "reset@example.com",
		Password:    "oldpassword123",
		DisplayName: "Reset User",
	}))
	require.NoError(t, err)

	// Forgot password (always succeeds).
	_, err = client.ForgotPassword(context.Background(), connect.NewRequest(&drillv1.ForgotPasswordRequest{
		Email: "reset@example.com",
	}))
	require.NoError(t, err)

	// Forgot password with non-existent email also succeeds.
	_, err = client.ForgotPassword(context.Background(), connect.NewRequest(&drillv1.ForgotPasswordRequest{
		Email: "nonexistent@example.com",
	}))
	require.NoError(t, err)

	// Get user ID for token generation.
	queries := db.New(b.Pool())
	user, err := queries.GetUserByEmail(context.Background(), "reset@example.com")
	require.NoError(t, err)

	// Generate reset token.
	signer := iauth.NewTokenSigner(iauth.DeriveKey(cfg.Auth.TokenSecret, "hmac-tokens"))
	token, err := signer.Sign(user.ID, "reset-password", cfg.Auth.ResetTokenTTL)
	require.NoError(t, err)

	// Reset password.
	_, err = client.ResetPassword(context.Background(), connect.NewRequest(&drillv1.ResetPasswordRequest{
		Token:    token,
		Password: "newpassword456",
	}))
	require.NoError(t, err)

	// Login with new password succeeds.
	_, err = client.Login(context.Background(), connect.NewRequest(&drillv1.LoginRequest{
		Email:    "reset@example.com",
		Password: "newpassword456",
	}))
	require.NoError(t, err)
}

func TestPublicEndpoints_NoAuthRequired(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startAuthServer(t, b)
	client := publicClient(srvURL) // No cookies, no auth.

	// Signup should work without auth.
	_, err := client.Signup(context.Background(), connect.NewRequest(&drillv1.SignupRequest{
		Email:       "public@example.com",
		Password:    "securepass",
		DisplayName: "Public",
	}))
	require.NoError(t, err)

	// ForgotPassword should work without auth.
	_, err = client.ForgotPassword(context.Background(), connect.NewRequest(&drillv1.ForgotPasswordRequest{
		Email: "public@example.com",
	}))
	require.NoError(t, err)

	// Logout without cookie should succeed silently.
	_, err = client.Logout(context.Background(), connect.NewRequest(&drillv1.LogoutRequest{}))
	require.NoError(t, err)
}

func TestDeleteAccount_RequiresAuth(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startAuthServer(t, b)
	client := publicClient(srvURL) // No cookies.

	_, err := client.DeleteAccount(context.Background(), connect.NewRequest(&drillv1.DeleteAccountRequest{}))
	require.Error(t, err)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

func TestDeleteAccount_Success(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startAuthServer(t, b)
	client := publicClient(srvURL)

	// Signup and login.
	_, err := client.Signup(context.Background(), connect.NewRequest(&drillv1.SignupRequest{
		Email:       "delete@example.com",
		Password:    "securepass123",
		DisplayName: "Delete User",
	}))
	require.NoError(t, err)

	cookieClient, jar := clientWithCookies(t, srvURL)
	_, err = cookieClient.Login(context.Background(), connect.NewRequest(&drillv1.LoginRequest{
		Email:    "delete@example.com",
		Password: "securepass123",
	}))
	require.NoError(t, err)

	// Verify cookie was set after login.
	u, _ := url.Parse(srvURL)
	cookies := jar.Cookies(u)
	var found bool
	for _, c := range cookies {
		if c.Name == iauth.SessionCookieName {
			found = true
			break
		}
	}
	require.True(t, found, "expected session cookie after login")

	// Delete account.
	_, err = cookieClient.DeleteAccount(context.Background(), connect.NewRequest(&drillv1.DeleteAccountRequest{}))
	require.NoError(t, err)

	// Cookie should be cleared after deletion (MaxAge=-1 causes jar to remove it).
	cookies = jar.Cookies(u)
	for _, c := range cookies {
		if c.Name == iauth.SessionCookieName {
			require.Empty(t, c.Value, "session cookie should be cleared after account deletion")
		}
	}

	// Login should fail after deletion (user is soft-deleted, sessions cleared).
	_, err = client.Login(context.Background(), connect.NewRequest(&drillv1.LoginRequest{
		Email:    "delete@example.com",
		Password: "securepass123",
	}))
	require.Error(t, err)
}
