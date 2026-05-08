package idleunsub_test

import (
	"crypto/rand"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/feat/idleunsub"
)

func newSigner(t *testing.T) *idleunsub.TokenSigner {
	t.Helper()
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)
	return idleunsub.NewTokenSigner(key)
}

func TestSignVerify_RoundTrip(t *testing.T) {
	t.Parallel()
	s := newSigner(t)
	now := time.Now().UTC().Truncate(time.Second)
	end := now.Add(7 * 24 * time.Hour)

	claims := idleunsub.KeepTokenClaims{
		UserID:           uuid.New(),
		SubscriptionID:   "sub_123",
		Action:           "keep_subscription",
		CurrentPeriodEnd: end,
		IssuedAt:         now.Unix(),
		ExpiresAt:        end.Unix(),
	}

	tok := s.Sign(claims)
	got, err := s.Verify(tok)
	require.NoError(t, err)
	require.Equal(t, claims.UserID, got.UserID)
	require.Equal(t, claims.SubscriptionID, got.SubscriptionID)
	require.Equal(t, claims.Action, got.Action)
	require.True(t, claims.CurrentPeriodEnd.Equal(got.CurrentPeriodEnd))
	require.Equal(t, claims.ExpiresAt, got.ExpiresAt)
}

func TestVerify_RejectsTampered(t *testing.T) {
	t.Parallel()
	s := newSigner(t)
	tok := s.Sign(idleunsub.KeepTokenClaims{
		UserID: uuid.New(), SubscriptionID: "sub_x", Action: "keep_subscription",
		CurrentPeriodEnd: time.Now().Add(time.Hour).UTC(), ExpiresAt: time.Now().Add(time.Hour).Unix(),
	})
	tampered := tok[:len(tok)-2] + "AA" // mutate last two characters
	_, err := s.Verify(tampered)
	require.ErrorIs(t, err, idleunsub.ErrTokenInvalid)
}

func TestVerify_RejectsExpired(t *testing.T) {
	t.Parallel()
	s := newSigner(t)
	past := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	tok := s.Sign(idleunsub.KeepTokenClaims{
		UserID: uuid.New(), SubscriptionID: "sub_x", Action: "keep_subscription",
		CurrentPeriodEnd: past, IssuedAt: past.Add(-24 * time.Hour).Unix(), ExpiresAt: past.Unix(),
	})
	_, err := s.Verify(tok)
	require.ErrorIs(t, err, idleunsub.ErrTokenExpired)
}

func TestVerify_RejectsExpDriftFromPeriodEnd(t *testing.T) {
	t.Parallel()
	s := newSigner(t)
	end := time.Now().Add(time.Hour).UTC().Truncate(time.Second)

	claims := idleunsub.KeepTokenClaims{
		UserID: uuid.New(), SubscriptionID: "sub_x", Action: "keep_subscription",
		CurrentPeriodEnd: end,
		IssuedAt:         time.Now().Unix(),
		ExpiresAt:        end.Add(24 * time.Hour).Unix(), // drift!
	}
	tok := s.Sign(claims)
	_, err := s.Verify(tok)
	require.ErrorIs(t, err, idleunsub.ErrTokenInvalid)
}
