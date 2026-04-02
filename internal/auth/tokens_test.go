package auth_test

import (
	"testing"
	"time"

	"github.com/btc/drill/internal/auth"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestTokens_SignAndVerify(t *testing.T) {
	signer := auth.NewTokenSigner("test-secret-at-least-32-bytes!!")
	userID := uuid.New()

	token, err := signer.Sign(userID, "verify-email", 1*time.Hour)
	require.NoError(t, err)
	require.NotEmpty(t, token)

	parsedID, err := signer.Verify(token, "verify-email")
	require.NoError(t, err)
	require.Equal(t, userID, parsedID)
}

func TestTokens_Expired(t *testing.T) {
	signer := auth.NewTokenSigner("test-secret-at-least-32-bytes!!")
	userID := uuid.New()

	token, err := signer.Sign(userID, "verify-email", -1*time.Hour)
	require.NoError(t, err)

	_, err = signer.Verify(token, "verify-email")
	require.Error(t, err)
	require.Contains(t, err.Error(), "expired")
}

func TestTokens_WrongPurpose(t *testing.T) {
	signer := auth.NewTokenSigner("test-secret-at-least-32-bytes!!")
	userID := uuid.New()

	token, err := signer.Sign(userID, "verify-email", 1*time.Hour)
	require.NoError(t, err)

	_, err = signer.Verify(token, "reset-password")
	require.Error(t, err)
	require.Contains(t, err.Error(), "purpose")
}

func TestTokens_Tampered(t *testing.T) {
	signer := auth.NewTokenSigner("test-secret-at-least-32-bytes!!")
	userID := uuid.New()

	token, err := signer.Sign(userID, "verify-email", 1*time.Hour)
	require.NoError(t, err)

	_, err = signer.Verify(token+"tampered", "verify-email")
	require.Error(t, err)
}
