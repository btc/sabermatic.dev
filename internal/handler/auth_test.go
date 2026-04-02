package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/handler"
)

func TestSignup_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupTestDB(t)
	b := newTestBackend(t, pool)

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
	b := newTestBackend(t, pool)

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
	b := newTestBackend(t, pool)

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
	b := newTestBackend(t, pool)

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

func TestVerifyEmail(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupTestDB(t)
	cfg := loadTestConfig(t)
	b := newTestBackend(t, pool)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux, b)

	// Sign up
	signupBody, _ := json.Marshal(map[string]string{
		"email":        "verify@example.com",
		"password":     "securepassword123",
		"display_name": "Verify User",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/signup", bytes.NewReader(signupBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)

	var signupResp map[string]any
	json.Unmarshal(w.Body.Bytes(), &signupResp)
	userID := signupResp["id"].(string)

	// Generate verification token
	signer := auth.NewTokenSigner(cfg.Auth.TokenSecret)
	uid, _ := uuid.Parse(userID)
	token, _ := signer.Sign(uid, "verify-email", cfg.Auth.VerifyTokenTTL)

	// Verify email
	verifyBody, _ := json.Marshal(map[string]string{"token": token})
	req = httptest.NewRequest(http.MethodPost, "/api/auth/verify-email", bytes.NewReader(verifyBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
}

func TestForgotAndResetPassword(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupTestDB(t)
	cfg := loadTestConfig(t)
	b := newTestBackend(t, pool)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux, b)

	// Sign up
	signupBody, _ := json.Marshal(map[string]string{
		"email":        "reset@example.com",
		"password":     "oldpassword123",
		"display_name": "Reset User",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/signup", bytes.NewReader(signupBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)

	// Forgot password (always 200)
	forgotBody, _ := json.Marshal(map[string]string{"email": "reset@example.com"})
	req = httptest.NewRequest(http.MethodPost, "/api/auth/forgot-password", bytes.NewReader(forgotBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	// Get user ID for token generation
	queries := db.New(pool)
	user, err := queries.GetUserByEmail(context.Background(), "reset@example.com")
	require.NoError(t, err)

	// Generate reset token
	signer := auth.NewTokenSigner(cfg.Auth.TokenSecret)
	token, _ := signer.Sign(user.ID, "reset-password", cfg.Auth.ResetTokenTTL)

	// Reset password
	resetBody, _ := json.Marshal(map[string]string{
		"token":    token,
		"password": "newpassword456",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/auth/reset-password", bytes.NewReader(resetBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	// Login with new password succeeds
	loginBody, _ := json.Marshal(map[string]string{
		"email":    "reset@example.com",
		"password": "newpassword456",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(loginBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
}

func TestLogout(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupTestDB(t)
	b := newTestBackend(t, pool)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux, b)

	// Sign up and login to get a session cookie
	signupBody, _ := json.Marshal(map[string]string{
		"email":        "logout@example.com",
		"password":     "securepassword123",
		"display_name": "Logout User",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/signup", bytes.NewReader(signupBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)

	loginBody, _ := json.Marshal(map[string]string{
		"email":    "logout@example.com",
		"password": "securepassword123",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(loginBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var sessionCookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "drill_session" {
			sessionCookie = c
			break
		}
	}
	require.NotNil(t, sessionCookie)

	// GET /api/me works with session cookie
	req = httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.AddCookie(sessionCookie)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	// Logout
	req = httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	req.AddCookie(sessionCookie)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	// Verify cookie is cleared
	var clearCookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "drill_session" {
			clearCookie = c
			break
		}
	}
	require.NotNil(t, clearCookie)
	require.Equal(t, -1, clearCookie.MaxAge)

	// GET /api/me now returns 401
	req = httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.AddCookie(sessionCookie)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(t, http.StatusUnauthorized, w.Code)
}
