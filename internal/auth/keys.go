package auth

import (
	"crypto/sha256"
	"io"

	"golang.org/x/crypto/hkdf"
)

// DeriveKey derives a 32-byte purpose-specific key from a master secret
// using HKDF-SHA256. Different purpose strings produce different keys.
// This prevents a vulnerability in one subsystem from affecting others
// that share the same master secret.
func DeriveKey(secret, purpose string) []byte {
	r := hkdf.New(sha256.New, []byte(secret), nil, []byte(purpose))
	key := make([]byte, 32)
	if _, err := io.ReadFull(r, key); err != nil {
		panic("hkdf: " + err.Error())
	}
	return key
}
