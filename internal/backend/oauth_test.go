package backend

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/db"
)

// ---------------------------------------------------------------------------
// OAuthLogin
// ---------------------------------------------------------------------------

func TestOAuthLogin_NewUser(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	res, err := b.OAuthLogin(ctx, OAuthLoginParams{
		Provider:    "google",
		ProviderID:  "google-123",
		Email:       "newuser@example.com",
		DisplayName: "New User",
		IP:          "10.0.0.1:9999",
		UserAgent:   "test-agent",
	})
	require.NoError(t, err)
	require.NotEmpty(t, res.UserID)
	require.Equal(t, "newuser@example.com", res.Email)
	require.NotEmpty(t, res.Token)
	require.False(t, res.NeedsProfile)

	// Verify user exists in DB.
	queries := db.New(b.pool)
	user, err := queries.GetUserByEmail(ctx, "newuser@example.com")
	require.NoError(t, err)
	require.Equal(t, "New User", user.DisplayName)
	require.True(t, user.EmailVerified) // OAuth users are email-verified
	require.False(t, user.PasswordHash.Valid, "OAuth user should have no password")

	// Verify OAuth account was created.
	oauthAcct, err := queries.GetOAuthAccount(ctx, db.GetOAuthAccountParams{
		Provider:   "google",
		ProviderID: "google-123",
	})
	require.NoError(t, err)
	require.Equal(t, user.ID, oauthAcct.UserID)
}

func TestOAuthLogin_NewUserNeedsProfile(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	res, err := b.OAuthLogin(ctx, OAuthLoginParams{
		Provider:    "github",
		ProviderID:  "gh-456",
		Email:       "noprofile@example.com",
		DisplayName: "", // empty — should trigger NeedsProfile
		IP:          "10.0.0.1:9999",
		UserAgent:   "test-agent",
	})
	require.NoError(t, err)
	require.NotEmpty(t, res.UserID)
	require.Equal(t, "noprofile@example.com", res.Email)
	require.NotEmpty(t, res.Token)
	require.True(t, res.NeedsProfile)
}

func TestOAuthLogin_ExistingUserByEmail(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	// Create user via Signup first.
	signupRes := signupUser(t, b, "existing@example.com", "strongpass1", "Existing")

	// Now OAuthLogin with the same email.
	res, err := b.OAuthLogin(ctx, OAuthLoginParams{
		Provider:    "google",
		ProviderID:  "google-existing-789",
		Email:       "existing@example.com",
		DisplayName: "Existing",
		IP:          "10.0.0.1:9999",
		UserAgent:   "test-agent",
	})
	require.NoError(t, err)
	require.Equal(t, signupRes.UserID, res.UserID) // same user, not a new one
	require.Equal(t, "existing@example.com", res.Email)
	require.NotEmpty(t, res.Token)

	// Verify OAuth account was linked to the existing user.
	queries := db.New(b.pool)
	oauthAcct, err := queries.GetOAuthAccount(ctx, db.GetOAuthAccountParams{
		Provider:   "google",
		ProviderID: "google-existing-789",
	})
	require.NoError(t, err)
	require.Equal(t, signupRes.UserID, oauthAcct.UserID)

	// Verify exactly one OAuth account is linked.
	oauthAccts, err := queries.GetOAuthAccountsByUser(ctx, signupRes.UserID)
	require.NoError(t, err)
	require.Len(t, oauthAccts, 1)
	require.Equal(t, "google", oauthAccts[0].Provider)
}

func TestOAuthLogin_ExistingOAuthAccount(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	params := OAuthLoginParams{
		Provider:    "google",
		ProviderID:  "google-repeat-111",
		Email:       "repeat@example.com",
		DisplayName: "Repeat",
		IP:          "10.0.0.1:9999",
		UserAgent:   "test-agent",
	}

	// First call: creates user + OAuth account (Step 3).
	res1, err := b.OAuthLogin(ctx, params)
	require.NoError(t, err)
	require.NotEmpty(t, res1.UserID)

	// Second call: should hit Step 1 (existing OAuth account).
	res2, err := b.OAuthLogin(ctx, params)
	require.NoError(t, err)
	require.Equal(t, res1.UserID, res2.UserID)
	require.NotEmpty(t, res2.Token)
	require.NotEqual(t, res1.Token, res2.Token) // new session each time
}

func TestOAuthLogin_SecondProvider(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	// First login via Google.
	res1, err := b.OAuthLogin(ctx, OAuthLoginParams{
		Provider:    "google",
		ProviderID:  "google-multi",
		Email:       "multi@example.com",
		DisplayName: "Multi User",
		IP:          "10.0.0.1:9999",
		UserAgent:   "test-agent",
	})
	require.NoError(t, err)

	// Second login via GitHub with the same email.
	res2, err := b.OAuthLogin(ctx, OAuthLoginParams{
		Provider:    "github",
		ProviderID:  "gh-multi",
		Email:       "multi@example.com",
		DisplayName: "Multi User",
		IP:          "10.0.0.1:9999",
		UserAgent:   "test-agent",
	})
	require.NoError(t, err)
	require.Equal(t, res1.UserID, res2.UserID, "same email should resolve to same user")

	// Verify 2 oauth_accounts rows with distinct providers.
	queries := db.New(b.pool)
	oauthAccts, err := queries.GetOAuthAccountsByUser(ctx, res1.UserID)
	require.NoError(t, err)
	require.Len(t, oauthAccts, 2)

	providers := map[string]bool{}
	for _, acct := range oauthAccts {
		providers[acct.Provider] = true
	}
	require.True(t, providers["google"])
	require.True(t, providers["github"])
}

func TestOAuthLogin_SoftDeletedUser_ReactivatedViaOAuth(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()
	queries := db.New(b.pool)

	// Create user via OAuthLogin.
	res1, err := b.OAuthLogin(ctx, OAuthLoginParams{
		Provider:    "google",
		ProviderID:  "google-delete-222",
		Email:       "deleted@example.com",
		DisplayName: "Deleted",
		IP:          "10.0.0.1:9999",
		UserAgent:   "test-agent",
	})
	require.NoError(t, err)

	// Soft-delete the user.
	err = queries.SoftDeleteUser(ctx, res1.UserID)
	require.NoError(t, err)

	// Verify user is soft-deleted.
	user, err := queries.GetUserByIDIncludingDeleted(ctx, res1.UserID)
	require.NoError(t, err)
	require.True(t, user.DeletedAt.Valid)

	// OAuthLogin again with same provider+providerID — should reactivate.
	res2, err := b.OAuthLogin(ctx, OAuthLoginParams{
		Provider:    "google",
		ProviderID:  "google-delete-222",
		Email:       "deleted@example.com",
		DisplayName: "Deleted",
		IP:          "10.0.0.1:9999",
		UserAgent:   "test-agent",
	})
	require.NoError(t, err)
	require.Equal(t, res1.UserID, res2.UserID)
	require.NotEmpty(t, res2.Token)

	// Verify user is no longer soft-deleted.
	user, err = queries.GetUserByIDIncludingDeleted(ctx, res1.UserID)
	require.NoError(t, err)
	require.False(t, user.DeletedAt.Valid)
	require.True(t, user.EmailVerified, "email should be verified after reactivation")

	// Verify OAuth account row exists.
	oauthAccts, err := queries.GetOAuthAccountsByUser(ctx, res1.UserID)
	require.NoError(t, err)
	require.Len(t, oauthAccts, 1)
}

func TestOAuthLogin_ExistingUserByEmail_VerifiesEmail(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()
	queries := db.New(b.pool)

	// Signup creates an unverified user.
	signupRes := signupUser(t, b, "unverified@example.com", "strongpass1", "Unverified")

	// Confirm email is not yet verified.
	user, err := queries.GetUserByEmail(ctx, "unverified@example.com")
	require.NoError(t, err)
	require.False(t, user.EmailVerified)

	// OAuthLogin with the same email — should verify the email.
	res, err := b.OAuthLogin(ctx, OAuthLoginParams{
		Provider:    "google",
		ProviderID:  "google-verify-333",
		Email:       "unverified@example.com",
		DisplayName: "Unverified",
		IP:          "10.0.0.1:9999",
		UserAgent:   "test-agent",
	})
	require.NoError(t, err)
	require.Equal(t, signupRes.UserID, res.UserID)

	// Email should now be verified.
	user, err = queries.GetUserByEmail(ctx, "unverified@example.com")
	require.NoError(t, err)
	require.True(t, user.EmailVerified)
}

func TestOAuthLogin_EmailNormalization(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	ctx := context.Background()

	res, err := b.OAuthLogin(ctx, OAuthLoginParams{
		Provider:    "google",
		ProviderID:  "google-norm-444",
		Email:       " USER@EXAMPLE.COM ",
		DisplayName: "Norm",
		IP:          "10.0.0.1:9999",
		UserAgent:   "test-agent",
	})
	require.NoError(t, err)
	require.Equal(t, "user@example.com", res.Email)

	// Verify stored email is normalized.
	queries := db.New(b.pool)
	user, err := queries.GetUserByEmail(ctx, "user@example.com")
	require.NoError(t, err)
	require.Equal(t, "user@example.com", user.Email)
}
