package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// TokenSigner creates and verifies HMAC-signed tokens for email verification
// and password reset. Not a JWT — just a simple signed payload.
type TokenSigner struct {
	secret []byte
}

// NewTokenSigner creates a signer using the given key (should be an
// HKDF-derived key, not a raw secret).
func NewTokenSigner(key []byte) *TokenSigner {
	return &TokenSigner{secret: key}
}

// Sign creates a token encoding the user ID, purpose, and expiry.
// Format: base64(userID|purpose|expiryUnix) + "." + hex(hmac-sha256)
func (s *TokenSigner) Sign(userID uuid.UUID, purpose string, ttl time.Duration) (string, error) {
	expiry := time.Now().Add(ttl).Unix()
	payload := fmt.Sprintf("%s|%s|%d", userID.String(), purpose, expiry)
	encoded := base64.RawURLEncoding.EncodeToString([]byte(payload))

	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(encoded))
	sig := hex.EncodeToString(mac.Sum(nil))

	return encoded + "." + sig, nil
}

// Verify checks the token signature, purpose, and expiry. Returns the user ID.
func (s *TokenSigner) Verify(token string, expectedPurpose string) (uuid.UUID, error) {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return uuid.Nil, fmt.Errorf("invalid token format")
	}
	encoded, sig := parts[0], parts[1]

	// Verify HMAC
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(encoded))
	expectedSig := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(sig), []byte(expectedSig)) {
		return uuid.Nil, fmt.Errorf("invalid token signature")
	}

	// Decode payload
	payloadBytes, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return uuid.Nil, fmt.Errorf("invalid token encoding: %w", err)
	}

	payload := string(payloadBytes)
	parts = strings.SplitN(payload, "|", 3)
	if len(parts) != 3 {
		return uuid.Nil, fmt.Errorf("invalid token payload")
	}

	userID, err := uuid.Parse(parts[0])
	if err != nil {
		return uuid.Nil, fmt.Errorf("invalid user ID in token: %w", err)
	}

	purpose := parts[1]
	if purpose != expectedPurpose {
		return uuid.Nil, fmt.Errorf("token purpose mismatch: got %q, expected %q", purpose, expectedPurpose)
	}

	var expiry int64
	if _, err := fmt.Sscanf(parts[2], "%d", &expiry); err != nil {
		return uuid.Nil, fmt.Errorf("invalid expiry in token: %w", err)
	}
	if time.Now().Unix() > expiry {
		return uuid.Nil, fmt.Errorf("token expired")
	}

	return userID, nil
}
