package idleunsub

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrTokenInvalid = errors.New("idleunsub: token invalid")
	ErrTokenExpired = errors.New("idleunsub: token expired")
)

// KeepTokenClaims is the canonical signed payload. Field order is fixed by
// struct definition; encoding/json over a typed struct produces a stable
// serialization (no map iteration order ambiguity).
type KeepTokenClaims struct {
	UserID           uuid.UUID `json:"user_id"`
	SubscriptionID   string    `json:"subscription_id"`
	Action           string    `json:"action"`            // "keep_subscription"
	CurrentPeriodEnd time.Time `json:"current_period_end"`
	IssuedAt         int64     `json:"iat"`
	ExpiresAt        int64     `json:"exp"` // INVARIANT: == CurrentPeriodEnd.Unix()
}

// TokenSigner signs and verifies KeepTokenClaims with HMAC-SHA256.
type TokenSigner struct {
	key []byte
	now func() time.Time
}

// NewTokenSigner constructs a signer with the given HMAC key.
func NewTokenSigner(key []byte) *TokenSigner {
	return &TokenSigner{key: key, now: time.Now}
}

// Sign serializes and HMAC-signs the claims, returning a URL-safe base64
// string of the form "<payload_b64>.<sig_b64>".
func (s *TokenSigner) Sign(c KeepTokenClaims) string {
	body, _ := json.Marshal(c) // typed struct never errors
	bodyB64 := base64.RawURLEncoding.EncodeToString(body)
	sig := hmac.New(sha256.New, s.key)
	sig.Write([]byte(bodyB64))
	sigB64 := base64.RawURLEncoding.EncodeToString(sig.Sum(nil))
	return bodyB64 + "." + sigB64
}

// Verify parses and validates a token. Returns the claims or an error.
//   - ErrTokenInvalid:  bad signature, malformed, or invariant violated
//   - ErrTokenExpired:  signature valid but exp < now
func (s *TokenSigner) Verify(tok string) (KeepTokenClaims, error) {
	var zero KeepTokenClaims
	parts := strings.SplitN(tok, ".", 2)
	if len(parts) != 2 {
		return zero, ErrTokenInvalid
	}
	bodyB64, sigB64 := parts[0], parts[1]

	expectedSig := hmac.New(sha256.New, s.key)
	expectedSig.Write([]byte(bodyB64))
	givenSig, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil || !hmac.Equal(givenSig, expectedSig.Sum(nil)) {
		return zero, ErrTokenInvalid
	}

	body, err := base64.RawURLEncoding.DecodeString(bodyB64)
	if err != nil {
		return zero, ErrTokenInvalid
	}
	var c KeepTokenClaims
	if err := json.Unmarshal(body, &c); err != nil {
		return zero, ErrTokenInvalid
	}

	// Spec invariant: ExpiresAt must equal CurrentPeriodEnd.Unix().
	if c.ExpiresAt != c.CurrentPeriodEnd.Unix() {
		return zero, ErrTokenInvalid
	}

	if s.now().Unix() >= c.ExpiresAt {
		return zero, ErrTokenExpired
	}
	return c, nil
}
