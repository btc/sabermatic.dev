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
	"testing"

	"github.com/gorilla/csrf"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/auth"
)

func TestCSRF_RejectsPostWithoutToken(t *testing.T) {
	cfg := loadTestConfig(t, "postgres://unused")
	csrfKey := auth.DeriveKey(cfg.Auth.TokenSecret, "csrf")
	csrfMiddleware := csrf.Protect(
		csrfKey,
		csrf.Secure(false),
		csrf.HttpOnly(false),
		csrf.CookieName("drill_csrf"),
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
	cfg := loadTestConfig(t, "postgres://unused")
	csrfKey := auth.DeriveKey(cfg.Auth.TokenSecret, "csrf")
	csrfMiddleware := csrf.Protect(
		csrfKey,
		csrf.Secure(false),
		csrf.HttpOnly(false),
		csrf.CookieName("drill_csrf"),
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
