package auth_test

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/btc/drill/internal/auth"
	"github.com/stretchr/testify/require"
)

func TestDeriveKey_Deterministic(t *testing.T) {
	key1 := auth.DeriveKey("my-secret-at-least-32-bytes-long", "purpose-a")
	key2 := auth.DeriveKey("my-secret-at-least-32-bytes-long", "purpose-a")
	require.Equal(t, key1, key2)
}

func TestDeriveKey_DifferentPurposes(t *testing.T) {
	key1 := auth.DeriveKey("my-secret-at-least-32-bytes-long", "purpose-a")
	key2 := auth.DeriveKey("my-secret-at-least-32-bytes-long", "purpose-b")
	require.NotEqual(t, key1, key2)
}

func TestDeriveKey_Length(t *testing.T) {
	key := auth.DeriveKey("my-secret-at-least-32-bytes-long", "purpose-a")
	require.Len(t, key, 32)
}

// Known-answer tests pin DeriveKey's output so a change in the HKDF
// implementation (x/crypto/hkdf wraps crypto/hkdf since x/crypto v0.51.0)
// cannot silently rotate the keys that sign tokens and OAuth state.
func TestDeriveKey_RFC5869TestCase3(t *testing.T) {
	// RFC 5869 A.3: SHA-256, 22-byte IKM of 0x0b, zero-length salt and info.
	// DeriveKey passes a nil salt, which HKDF treats the same as empty.
	ikm := strings.Repeat("\x0b", 22)
	want, err := hex.DecodeString("8da4e775a563c18f715f802a063c5a31b8a11f5c5ee1879ec3454e5f3c738d2d")
	require.NoError(t, err)
	require.Equal(t, want, auth.DeriveKey(ikm, ""))
}

func TestDeriveKey_RecordedOutputs(t *testing.T) {
	// Recorded with golang.org/x/crypto v0.50.0.
	for purpose, want := range map[string]string{
		"oauth-state": "80eb8d17080ce344b2b504061d89bddd842dbcbb303add77cff87527c7430ab6",
		"hmac-tokens": "924c5b86f432f22c591c5f08476955b71d23967806320b364e73f7ae251ddbfc",
	} {
		require.Equal(t, want, hex.EncodeToString(auth.DeriveKey("my-secret-at-least-32-bytes-long", purpose)), purpose)
	}
}
