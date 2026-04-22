package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
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

// SessionCookie builds the standard session cookie. Pass an empty token
// and maxAge -1 to create a deletion cookie (logout).
func SessionCookie(token string, maxAge int, secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   maxAge,
	}
}

// OAuthRedirectCookieName is the cookie that survives the OAuth round-trip
// to remember where to send the user after a successful callback. Separate
// from the session cookie so it can be cleared independently on consumption.
const OAuthRedirectCookieName = "drill_oauth_redirect"

// OAuthRedirectCookie builds a cookie that carries the post-OAuth return
// URL across the provider round-trip. Pass an empty value and maxAge -1 to
// clear it after consumption. SameSite=Lax so it flows through the
// same-origin callback redirect.
func OAuthRedirectCookie(value string, maxAge int, secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     OAuthRedirectCookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   maxAge,
	}
}
