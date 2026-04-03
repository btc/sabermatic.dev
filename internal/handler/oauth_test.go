package handler_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/csrf"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/backend"
	"github.com/btc/drill/internal/db"
)

func TestOAuthLogin_NewUser(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := newTestBackend(t)
	ctx := context.Background()

	result, err := b.OAuthLogin(ctx, backend.OAuthLoginParams{
		Provider:    "google",
		ProviderID:  "google-123",
		Email:       "newuser@example.com",
		DisplayName: "New User",
		IP:          "192.168.1.1:12345",
		UserAgent:   "TestBrowser/1.0",
	})
	require.NoError(t, err)
	require.NotEqual(t, result.UserID.String(), "00000000-0000-0000-0000-000000000000")
	require.Equal(t, "newuser@example.com", result.Email)
	require.NotEmpty(t, result.Token)
	require.False(t, result.NeedsProfile)

	// Verify user was created with email_verified=true.
	queries := db.New(b.Pool())
	user, err := queries.GetUserByEmail(ctx, "newuser@example.com")
	require.NoError(t, err)
	require.True(t, user.EmailVerified)
	require.Equal(t, "New User", user.DisplayName)
	require.False(t, user.PasswordHash.Valid, "OAuth user should have no password")

	// Verify oauth_accounts row was created.
	oauthAccts, err := queries.GetOAuthAccountsByUser(ctx, user.ID)
	require.NoError(t, err)
	require.Len(t, oauthAccts, 1)
	require.Equal(t, "google", oauthAccts[0].Provider)
	require.Equal(t, "google-123", oauthAccts[0].ProviderID)
}

func TestOAuthLogin_NewUser_EmptyDisplayName(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := newTestBackend(t)
	ctx := context.Background()

	result, err := b.OAuthLogin(ctx, backend.OAuthLoginParams{
		Provider:    "google",
		ProviderID:  "google-456",
		Email:       "noname@example.com",
		DisplayName: "",
		IP:          "10.0.0.1:8080",
		UserAgent:   "TestBrowser/1.0",
	})
	require.NoError(t, err)
	require.True(t, result.NeedsProfile, "NeedsProfile should be true when display_name is empty")
	require.Equal(t, "noname@example.com", result.Email)
}

func TestOAuthLogin_ExistingOAuthAccount(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := newTestBackend(t)
	ctx := context.Background()

	// First login creates the user.
	first, err := b.OAuthLogin(ctx, backend.OAuthLoginParams{
		Provider:    "google",
		ProviderID:  "google-existing",
		Email:       "existing@example.com",
		DisplayName: "Existing User",
		IP:          "192.168.1.1:12345",
		UserAgent:   "TestBrowser/1.0",
	})
	require.NoError(t, err)

	// Second login with same provider+provider_id returns same user.
	second, err := b.OAuthLogin(ctx, backend.OAuthLoginParams{
		Provider:    "google",
		ProviderID:  "google-existing",
		Email:       "existing@example.com",
		DisplayName: "Existing User",
		IP:          "192.168.1.1:12345",
		UserAgent:   "TestBrowser/1.0",
	})
	require.NoError(t, err)
	require.Equal(t, first.UserID, second.UserID, "second login should return the same user")
	require.NotEqual(t, first.Token, second.Token, "each login should get a new session token")
}

func TestOAuthLogin_LinkToExistingPasswordUser(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := newTestBackend(t)
	ctx := context.Background()

	// Create a user via Signup (password-based, email_verified=false).
	signupResult, err := b.Signup(ctx, backend.SignupParams{
		Email:       "passworduser@example.com",
		Password:    "securepass123",
		DisplayName: "Password User",
	})
	require.NoError(t, err)

	// Verify email_verified is false initially.
	queries := db.New(b.Pool())
	user, err := queries.GetUserByEmail(ctx, "passworduser@example.com")
	require.NoError(t, err)
	require.False(t, user.EmailVerified)

	// OAuth login with same email should link, not create a new user.
	oauthResult, err := b.OAuthLogin(ctx, backend.OAuthLoginParams{
		Provider:    "github",
		ProviderID:  "gh-link-123",
		Email:       "passworduser@example.com",
		DisplayName: "Password User",
		IP:          "10.0.0.1:8080",
		UserAgent:   "TestBrowser/1.0",
	})
	require.NoError(t, err)
	require.Equal(t, signupResult.UserID, oauthResult.UserID, "should link to existing user, not create new")

	// Verify email is now verified.
	user, err = queries.GetUserByEmail(ctx, "passworduser@example.com")
	require.NoError(t, err)
	require.True(t, user.EmailVerified, "email should be verified after OAuth link")

	// Verify oauth_accounts row was created.
	oauthAccts, err := queries.GetOAuthAccountsByUser(ctx, user.ID)
	require.NoError(t, err)
	require.Len(t, oauthAccts, 1)
	require.Equal(t, "github", oauthAccts[0].Provider)
}

func TestOAuthLogin_SecondProvider(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := newTestBackend(t)
	ctx := context.Background()

	// First login via Google.
	first, err := b.OAuthLogin(ctx, backend.OAuthLoginParams{
		Provider:    "google",
		ProviderID:  "google-multi",
		Email:       "multi@example.com",
		DisplayName: "Multi User",
		IP:          "192.168.1.1:12345",
		UserAgent:   "TestBrowser/1.0",
	})
	require.NoError(t, err)

	// Second login via GitHub with the same email.
	second, err := b.OAuthLogin(ctx, backend.OAuthLoginParams{
		Provider:    "github",
		ProviderID:  "gh-multi",
		Email:       "multi@example.com",
		DisplayName: "Multi User",
		IP:          "192.168.1.1:12345",
		UserAgent:   "TestBrowser/1.0",
	})
	require.NoError(t, err)
	require.Equal(t, first.UserID, second.UserID, "same email should resolve to same user")

	// Verify 2 oauth_accounts rows.
	queries := db.New(b.Pool())
	oauthAccts, err := queries.GetOAuthAccountsByUser(ctx, first.UserID)
	require.NoError(t, err)
	require.Len(t, oauthAccts, 2)

	providers := map[string]bool{}
	for _, acct := range oauthAccts {
		providers[acct.Provider] = true
	}
	require.True(t, providers["google"])
	require.True(t, providers["github"])
}

func TestOAuthLogin_ReactivateDeletedUser(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := newTestBackend(t)
	ctx := context.Background()
	queries := db.New(b.Pool())

	// Create a user via Signup, then soft-delete.
	signupResult, err := b.Signup(ctx, backend.SignupParams{
		Email:       "deleted@example.com",
		Password:    "securepass123",
		DisplayName: "Deleted User",
	})
	require.NoError(t, err)

	err = queries.SoftDeleteUser(ctx, signupResult.UserID)
	require.NoError(t, err)

	// Verify user is soft-deleted.
	deletedUser, err := queries.GetUserByEmailIncludingDeleted(ctx, "deleted@example.com")
	require.NoError(t, err)
	require.True(t, deletedUser.DeletedAt.Valid, "user should be soft-deleted")

	// OAuth login should reactivate.
	oauthResult, err := b.OAuthLogin(ctx, backend.OAuthLoginParams{
		Provider:    "google",
		ProviderID:  "google-reactivate",
		Email:       "deleted@example.com",
		DisplayName: "Deleted User",
		IP:          "10.0.0.1:8080",
		UserAgent:   "TestBrowser/1.0",
	})
	require.NoError(t, err)
	require.Equal(t, signupResult.UserID, oauthResult.UserID, "should reactivate the same user")

	// Verify user is no longer deleted and email is verified.
	reactivated, err := queries.GetUserByEmail(ctx, "deleted@example.com")
	require.NoError(t, err)
	require.Equal(t, pgtype.Timestamptz{}, reactivated.DeletedAt, "deleted_at should be NULL")
	require.True(t, reactivated.EmailVerified, "email should be verified after reactivation")

	// Verify oauth_accounts row.
	oauthAccts, err := queries.GetOAuthAccountsByUser(ctx, reactivated.ID)
	require.NoError(t, err)
	require.Len(t, oauthAccts, 1)
}

func TestCSRF_RejectsPostWithoutToken(t *testing.T) {
	cfg := loadTestConfig(t, "postgres://unused")
	csrfKey := auth.DeriveKey(cfg.Auth.TokenSecret, "csrf")
	csrfMiddleware := csrf.Protect(
		csrfKey,
		csrf.Secure(false),
		csrf.HttpOnly(false),
		csrf.CookieName("drill_csrf"),
		csrf.Path("/"),
	)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	protected := csrfMiddleware(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/test", nil)
	w := httptest.NewRecorder()
	protected.ServeHTTP(w, req)
	require.Equal(t, http.StatusForbidden, w.Code)
}

func TestCSRF_AllowsGetRequests(t *testing.T) {
	cfg := loadTestConfig(t, "postgres://unused")
	csrfKey := auth.DeriveKey(cfg.Auth.TokenSecret, "csrf")
	csrfMiddleware := csrf.Protect(
		csrfKey,
		csrf.Secure(false),
		csrf.HttpOnly(false),
		csrf.CookieName("drill_csrf"),
		csrf.Path("/"),
	)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	protected := csrfMiddleware(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	w := httptest.NewRecorder()
	protected.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
}
