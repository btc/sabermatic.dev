package handler

import (
	"net/http"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/backend"
)

// RegisterRoutes sets up all HTTP routes on the given mux.
func RegisterRoutes(mux *http.ServeMux, b *backend.Backend) {
	mux.HandleFunc("GET /api/health", Health(b))
	mux.HandleFunc("GET /admin/jobs", AdminJobsPlaceholder())

	// Auth
	mux.HandleFunc("POST /api/auth/signup", Signup(b))
	mux.HandleFunc("POST /api/auth/login", Login(b))
	mux.HandleFunc("POST /api/auth/logout", Logout(b))
	mux.HandleFunc("POST /api/auth/verify-email", VerifyEmail(b))
	mux.HandleFunc("POST /api/auth/forgot-password", ForgotPassword(b))
	mux.HandleFunc("POST /api/auth/reset-password", ResetPassword(b))

	// OAuth
	mux.HandleFunc("GET /api/auth/oauth/{provider}", OAuthStart(b))
	mux.HandleFunc("GET /api/auth/oauth/{provider}/callback", OAuthCallback(b))

	// User
	requireAuth := auth.RequireAuth(b.Pool)
	mux.Handle("GET /api/me", requireAuth(http.HandlerFunc(GetMe(b))))
}
