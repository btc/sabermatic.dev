package handler_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/handler"
)

func TestSignup_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupTestDB(t)
	b := &handler.Backend{Pool: pool}
	b.SetConfig(loadTestConfig(t))

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux, b)

	body := `{"email":"alice@example.com","password":"securepass","display_name":"Alice"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/signup", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code)

	var resp map[string]any
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	require.Equal(t, "alice@example.com", resp["email"])
	require.NotEmpty(t, resp["id"])
}

func TestSignup_DuplicateEmail(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupTestDB(t)
	b := &handler.Backend{Pool: pool}
	b.SetConfig(loadTestConfig(t))

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux, b)

	body := `{"email":"dup@example.com","password":"securepass","display_name":"First"}`

	// First signup — should succeed.
	req := httptest.NewRequest(http.MethodPost, "/api/auth/signup", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)

	// Second signup with same email — should return 409.
	req = httptest.NewRequest(http.MethodPost, "/api/auth/signup", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(t, http.StatusConflict, w.Code)

	var resp map[string]string
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	require.Contains(t, resp["error"], "email already registered")
}

func TestLogin_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupTestDB(t)
	b := &handler.Backend{Pool: pool}
	b.SetConfig(loadTestConfig(t))

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux, b)

	// Signup first.
	signupBody := `{"email":"bob@example.com","password":"securepass","display_name":"Bob"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/signup", bytes.NewBufferString(signupBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)

	// Login.
	loginBody := `{"email":"bob@example.com","password":"securepass"}`
	req = httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewBufferString(loginBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	require.Equal(t, "bob@example.com", resp["email"])
	require.NotEmpty(t, resp["id"])

	// Verify session cookie is set.
	cookies := w.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == auth.SessionCookieName {
			sessionCookie = c
			break
		}
	}
	require.NotNil(t, sessionCookie, "expected session cookie to be set")
	require.True(t, sessionCookie.HttpOnly, "session cookie should be HttpOnly")
	require.Equal(t, "/", sessionCookie.Path)
	require.NotEmpty(t, sessionCookie.Value)
}

func TestLogin_WrongPassword(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupTestDB(t)
	b := &handler.Backend{Pool: pool}
	b.SetConfig(loadTestConfig(t))

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux, b)

	// Signup first.
	signupBody := `{"email":"carol@example.com","password":"securepass","display_name":"Carol"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/signup", bytes.NewBufferString(signupBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)

	// Login with wrong password.
	loginBody := `{"email":"carol@example.com","password":"wrongpassword"}`
	req = httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewBufferString(loginBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(t, http.StatusUnauthorized, w.Code)

	var resp map[string]string
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	require.Contains(t, resp["error"], "invalid email or password")
}
