package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// GenerateSessionToken creates a random session token and its SHA-256 hash.
// The raw token goes to the client (cookie). The hash is stored in the database.
func GenerateSessionToken() (token string, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("generate session token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	hash = HashSessionToken(token)
	return token, hash, nil
}

// HashSessionToken returns the SHA-256 hash of a session token.
func HashSessionToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
