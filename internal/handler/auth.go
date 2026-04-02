package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/backend"
)

// writeJSON encodes body as JSON and writes it with the given HTTP status.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

// Signup returns a handler that creates a new user account.
func Signup(b *backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Email       string `json:"email"`
			Password    string `json:"password"`
			DisplayName string `json:"display_name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
			return
		}

		result, err := b.Signup(r.Context(), req.Email, req.Password, req.DisplayName)
		if err != nil {
			switch {
			case errors.Is(err, backend.ErrDuplicateEmail):
				writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			case errors.Is(err, backend.ErrPasswordLength):
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			default:
				// Check for validation errors (missing fields) — they start with known text.
				if err.Error() == "email, password, and display_name are required" {
					writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				} else {
					writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
				}
			}
			return
		}

		writeJSON(w, http.StatusCreated, map[string]any{
			"id":    result.UserID.String(),
			"email": result.Email,
		})
	}
}

// Login returns a handler that authenticates a user and creates a session.
func Login(b *backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
			return
		}

		result, err := b.Login(r.Context(), req.Email, req.Password, r.RemoteAddr, r.UserAgent())
		if err != nil {
			if errors.Is(err, backend.ErrInvalidCredentials) {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
			} else {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			}
			return
		}

		// Set session cookie (HTTP concern — handler owns cookie construction).
		http.SetCookie(w, &http.Cookie{
			Name:     auth.SessionCookieName,
			Value:    result.Token,
			Path:     "/",
			HttpOnly: true,
			Secure:   b.Config().Auth.SecureCookies(),
			SameSite: http.SameSiteLaxMode,
			MaxAge:   int(b.Config().Auth.SessionTTL.Seconds()),
		})

		writeJSON(w, http.StatusOK, map[string]any{
			"id":    result.UserID.String(),
			"email": result.Email,
		})
	}
}

// Logout returns a handler that invalidates the current session.
func Logout(b *backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(auth.SessionCookieName)
		if err != nil || cookie.Value == "" {
			// No cookie — nothing to do, still return 200.
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
			return
		}

		b.Logout(r.Context(), cookie.Value)

		// Clear cookie regardless (HTTP concern).
		http.SetCookie(w, &http.Cookie{
			Name:     auth.SessionCookieName,
			Value:    "",
			Path:     "/",
			HttpOnly: true,
			Secure:   b.Config().Auth.SecureCookies(),
			SameSite: http.SameSiteLaxMode,
			MaxAge:   -1,
		})

		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// VerifyEmail returns a handler that marks a user's email as verified.
func VerifyEmail(b *backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Token string `json:"token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		if err := b.VerifyEmail(r.Context(), req.Token); err != nil {
			if errors.Is(err, backend.ErrInvalidToken) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			} else {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			}
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"status": "email verified"})
	}
}

// ForgotPassword returns a handler that enqueues a password reset email.
// Always returns 200 to prevent email enumeration.
func ForgotPassword(b *backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Email string `json:"email"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		b.ForgotPassword(r.Context(), req.Email)

		writeJSON(w, http.StatusOK, map[string]string{"status": "if that email exists, a reset link has been sent"})
	}
}

// ResetPassword returns a handler that resets a user's password via a signed token.
func ResetPassword(b *backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Token    string `json:"token"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		if err := b.ResetPassword(r.Context(), req.Token, req.Password); err != nil {
			switch {
			case errors.Is(err, backend.ErrPasswordLength):
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			case errors.Is(err, backend.ErrInvalidToken):
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			default:
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			}
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"status": "password reset"})
	}
}
