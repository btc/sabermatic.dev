package handler_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/handler"
)

func TestGetMe_Authenticated(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := newTestBackend(t)
	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))

	// Sign up
	signupBody, _ := json.Marshal(map[string]string{
		"email":        "me@example.com",
		"password":     "securepassword123",
		"display_name": "Me User",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/signup", bytes.NewReader(signupBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)

	// Login to get session cookie
	loginBody, _ := json.Marshal(map[string]string{
		"email":    "me@example.com",
		"password": "securepassword123",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(loginBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	// Extract session cookie
	var sessionCookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "drill_session" {
			sessionCookie = c
			break
		}
	}
	require.NotNil(t, sessionCookie)

	// GET /api/me with session cookie
	req = httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.AddCookie(sessionCookie)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, "me@example.com", body["email"])
	require.Equal(t, "Me User", body["display_name"])
	require.Equal(t, "candidate", body["role"])
	require.Equal(t, "free", body["plan"])
}

func TestGetMe_Unauthenticated(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := newTestBackend(t)
	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))

	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnauthorized, w.Code)
}
