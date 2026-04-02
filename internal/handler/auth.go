package handler

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/jobs"
)

// dummyBcryptHash is used for constant-time login responses. When a user is
// not found, we still run bcrypt to prevent timing oracles that reveal whether
// an email is registered. Generated with cost 12.
var dummyBcryptHash = "$2a$12$LKpvXspMO/C6shvqXZwCVOgIw3YklCI48vEoUxpBu3TWm/ZePR.02"

// writeJSON encodes body as JSON and writes it with the given HTTP status.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

// signupRequest is the expected JSON body for POST /api/auth/signup.
type signupRequest struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
}

// Signup returns a handler that creates a new user account.
func Signup(b *Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req signupRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
			return
		}

		// Normalize and validate.
		req.Email = strings.ToLower(strings.TrimSpace(req.Email))
		req.DisplayName = strings.TrimSpace(req.DisplayName)

		if req.Email == "" || req.Password == "" || req.DisplayName == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "email, password, and display_name are required"})
			return
		}
		if len(req.Password) < 8 || len(req.Password) > 128 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "password must be between 8 and 128 characters"})
			return
		}

		// Hash password.
		hash, err := auth.HashPassword(req.Password, b.cfg.Auth.BcryptCost)
		if err != nil {
			slog.Error("hash password", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}

		// Insert user.
		queries := db.New(b.Pool)
		user, err := queries.CreateUser(r.Context(), db.CreateUserParams{
			Email:        req.Email,
			PasswordHash: pgtype.Text{String: hash, Valid: true},
			DisplayName:  req.DisplayName,
		})
		if err != nil {
			if isDuplicateKeyError(err) {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "email already registered"})
				return
			}
			slog.Error("create user", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}

		// Enqueue verification email (best-effort; River may be nil in tests).
		if b.River != nil {
			signer := auth.NewTokenSigner(b.cfg.Auth.TokenSecret)
			token, err := signer.Sign(user.ID, "verify-email", b.cfg.Auth.VerifyTokenTTL)
			if err != nil {
				slog.Error("sign verification token", "error", err)
			} else {
				verifyURL := fmt.Sprintf("%s/verify-email?token=%s", b.cfg.Auth.BaseURL, token)
				_, err = b.River.Insert(r.Context(), jobs.SendEmailArgs{
					To:      user.Email,
					Subject: "Verify your Drill account",
					Text:    fmt.Sprintf("Click here to verify your email: %s", verifyURL),
					HTML:    fmt.Sprintf(`<p>Click <a href="%s">here</a> to verify your email.</p>`, verifyURL),
				}, jobs.SendEmailInsertOpts(&b.cfg.Email))
				if err != nil {
					slog.Error("enqueue verification email", "error", err)
				}
			}
		}

		writeJSON(w, http.StatusCreated, map[string]any{
			"id":    user.ID.String(),
			"email": user.Email,
		})
	}
}

// loginRequest is the expected JSON body for POST /api/auth/login.
type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// Login returns a handler that authenticates a user and creates a session.
func Login(b *Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req loginRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
			return
		}

		req.Email = strings.ToLower(strings.TrimSpace(req.Email))

		// Look up user.
		queries := db.New(b.Pool)
		user, err := queries.GetUserByEmail(r.Context(), req.Email)
		if err != nil {
			// Constant-time: run dummy bcrypt to prevent timing oracle
			auth.CheckPassword(dummyBcryptHash, "x")
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid email or password"})
			return
		}

		// Check that user has a password (not OAuth-only).
		if !user.PasswordHash.Valid {
			auth.CheckPassword(dummyBcryptHash, "x")
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid email or password"})
			return
		}

		// Compare password.
		if err := auth.CheckPassword(user.PasswordHash.String, req.Password); err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid email or password"})
			return
		}

		// Generate session.
		rawToken, tokenHash, err := auth.GenerateSessionToken()
		if err != nil {
			slog.Error("generate session token", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}

		// Parse client IP.
		ipAddr := parseClientIP(r.RemoteAddr)

		_, err = queries.CreateAuthSession(r.Context(), db.CreateAuthSessionParams{
			UserID:    user.ID,
			TokenHash: tokenHash,
			ExpiresAt: time.Now().Add(b.cfg.Auth.SessionTTL),
			IpAddress: ipAddr,
			UserAgent: pgtype.Text{String: r.UserAgent(), Valid: r.UserAgent() != ""},
		})
		if err != nil {
			slog.Error("create auth session", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}

		// Set session cookie.
		http.SetCookie(w, &http.Cookie{
			Name:     auth.SessionCookieName,
			Value:    rawToken,
			Path:     "/",
			HttpOnly: true,
			Secure:   b.cfg.Auth.SecureCookies(),
			SameSite: http.SameSiteLaxMode,
			MaxAge:   int(b.cfg.Auth.SessionTTL.Seconds()),
		})

		writeJSON(w, http.StatusOK, map[string]any{
			"id":    user.ID.String(),
			"email": user.Email,
		})
	}
}

// Logout returns a handler that invalidates the current session.
func Logout(b *Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(auth.SessionCookieName)
		if err != nil || cookie.Value == "" {
			// No cookie — nothing to do, still return 200.
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
			return
		}

		tokenHash := auth.HashSessionToken(cookie.Value)
		queries := db.New(b.Pool)

		session, err := queries.GetAuthSessionByToken(r.Context(), tokenHash)
		if err == nil {
			if delErr := queries.DeleteAuthSession(r.Context(), session.ID); delErr != nil {
				slog.Error("delete auth session", "error", delErr)
			}
		}

		// Clear cookie regardless.
		http.SetCookie(w, &http.Cookie{
			Name:     auth.SessionCookieName,
			Value:    "",
			Path:     "/",
			HttpOnly: true,
			Secure:   b.cfg.Auth.SecureCookies(),
			SameSite: http.SameSiteLaxMode,
			MaxAge:   -1,
		})

		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// parseClientIP extracts a netip.Addr from a RemoteAddr string (which may
// include a port, e.g. "192.168.1.1:12345" or "[::1]:12345").
func parseClientIP(remoteAddr string) *netip.Addr {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		// No port — try parsing the whole thing.
		host = remoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return nil
	}
	return &addr
}

// isDuplicateKeyError returns true if the error is a Postgres unique-violation
// (SQLSTATE 23505).
func isDuplicateKeyError(err error) bool {
	return strings.Contains(err.Error(), "23505") ||
		strings.Contains(err.Error(), "duplicate key")
}

// VerifyEmail returns a handler that marks a user's email as verified.
func VerifyEmail(b *Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Token string `json:"token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		signer := auth.NewTokenSigner(b.cfg.Auth.TokenSecret)
		userID, err := signer.Verify(req.Token, "verify-email")
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid or expired token"})
			return
		}

		queries := db.New(b.Pool)
		if err := queries.VerifyUserEmail(r.Context(), userID); err != nil {
			slog.Error("verify email", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"status": "email verified"})
	}
}

// ForgotPassword returns a handler that enqueues a password reset email.
// Always returns 200 to prevent email enumeration.
func ForgotPassword(b *Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Email string `json:"email"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		req.Email = strings.TrimSpace(strings.ToLower(req.Email))
		queries := db.New(b.Pool)
		user, err := queries.GetUserByEmail(r.Context(), req.Email)
		if err == nil {
			signer := auth.NewTokenSigner(b.cfg.Auth.TokenSecret)
			token, _ := signer.Sign(user.ID, "reset-password", b.cfg.Auth.ResetTokenTTL)
			resetURL := b.cfg.Auth.BaseURL + "/reset-password?token=" + token

			if b.River != nil {
				b.River.Insert(r.Context(), jobs.SendEmailArgs{
					To:      user.Email,
					Subject: "Reset your Drill password",
					Text:    "Click here to reset your password: " + resetURL,
					HTML:    "<p>Click <a href=\"" + resetURL + "\">here</a> to reset your password.</p>",
				}, jobs.SendEmailInsertOpts(&b.cfg.Email))
			}
		}

		// Always 200 to prevent email enumeration.
		writeJSON(w, http.StatusOK, map[string]string{"status": "if that email exists, a reset link has been sent"})
	}
}

// ResetPassword returns a handler that resets a user's password via a signed token.
func ResetPassword(b *Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Token    string `json:"token"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		if len(req.Password) < 8 || len(req.Password) > 128 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "password must be between 8 and 128 characters"})
			return
		}

		signer := auth.NewTokenSigner(b.cfg.Auth.TokenSecret)
		userID, err := signer.Verify(req.Token, "reset-password")
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid or expired token"})
			return
		}

		hash, err := auth.HashPassword(req.Password, b.cfg.Auth.BcryptCost)
		if err != nil {
			slog.Error("hash password", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}

		queries := db.New(b.Pool)
		if err := queries.UpdateUserPassword(r.Context(), db.UpdateUserPasswordParams{
			ID:           userID,
			PasswordHash: pgtype.Text{String: hash, Valid: true},
		}); err != nil {
			slog.Error("update password", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}

		// Invalidate all sessions.
		queries.DeleteUserAuthSessions(r.Context(), userID)

		writeJSON(w, http.StatusOK, map[string]string{"status": "password reset"})
	}
}
