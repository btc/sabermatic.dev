// Package handler_test contains integration tests for the HTTP layer.
//
// These tests verify HTTP-specific concerns: status codes, cookies, JSON
// response shape, CSRF protection, and middleware wiring. They intentionally
// do NOT test business logic, validation edge cases, or database state —
// those belong in package backend's tests (internal/backend/*_test.go).
//
// If you're adding a new backend method or business rule, write the test in
// internal/backend/. Only add a handler test if you need to verify something
// specific to the HTTP contract (e.g. a new status code mapping, cookie
// behavior, or middleware interaction).
package handler_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/csrf"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/testutil"
)

func TestCSRF_RejectsPostWithoutToken(t *testing.T) {
	cfg := testutil.Config(t)
	csrfKey := auth.DeriveKey(cfg.Auth.TokenSecret, "csrf")
	csrfMiddleware := csrf.Protect(
		csrfKey,
		csrf.Secure(false),
		csrf.HttpOnly(false),
		csrf.CookieName("sabermatic_csrf"),
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
	cfg := testutil.Config(t)
	csrfKey := auth.DeriveKey(cfg.Auth.TokenSecret, "csrf")
	csrfMiddleware := csrf.Protect(
		csrfKey,
		csrf.Secure(false),
		csrf.HttpOnly(false),
		csrf.CookieName("sabermatic_csrf"),
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

func TestCSRF_PostWithValidToken(t *testing.T) {
	cfg := testutil.Config(t)
	csrfKey := auth.DeriveKey(cfg.Auth.TokenSecret, "csrf")
	csrfMiddleware := csrf.Protect(
		csrfKey,
		csrf.Secure(false),
		csrf.HttpOnly(false),
		csrf.CookieName("sabermatic_csrf"),
		csrf.Path("/"),
	)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/test", func(w http.ResponseWriter, r *http.Request) {
		// Expose the masked token via response header (mirrors production handler).
		w.Header().Set("X-CSRF-Token", csrf.Token(r))
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST /api/test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	protected := csrfMiddleware(mux)

	// Step 1: GET to obtain the CSRF cookie + masked token.
	getReq := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	getReq = csrf.PlaintextHTTPRequest(getReq)
	getW := httptest.NewRecorder()
	protected.ServeHTTP(getW, getReq)
	require.Equal(t, http.StatusOK, getW.Code)

	csrfToken := getW.Header().Get("X-CSRF-Token")
	require.NotEmpty(t, csrfToken, "GET response should include X-CSRF-Token header")

	// Extract the cookie from the GET response.
	var csrfCookie *http.Cookie
	for _, c := range getW.Result().Cookies() {
		if c.Name == "sabermatic_csrf" {
			csrfCookie = c
			break
		}
	}
	require.NotNil(t, csrfCookie, "GET response should set sabermatic_csrf cookie")

	// Step 2: POST with the cookie + masked token in the header.
	postReq := httptest.NewRequest(http.MethodPost, "/api/test", strings.NewReader("{}"))
	postReq = csrf.PlaintextHTTPRequest(postReq)
	postReq.Header.Set("X-CSRF-Token", csrfToken)
	postReq.Header.Set("Content-Type", "application/json")
	postReq.AddCookie(csrfCookie)
	postW := httptest.NewRecorder()
	protected.ServeHTTP(postW, postReq)
	require.Equal(t, http.StatusOK, postW.Code, "POST with valid CSRF token should succeed")
}
