package handler

import (
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"strings"

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
	requireAuth := auth.RequireAuth(b)
	mux.Handle("GET /api/me", requireAuth(http.HandlerFunc(GetMe(b))))

	// Sessions
	mux.Handle("POST /api/sessions", requireAuth(http.HandlerFunc(CreateSession(b))))
	mux.Handle("GET /api/sessions", requireAuth(http.HandlerFunc(ListSessions(b))))
	mux.Handle("GET /api/sessions/{id}", requireAuth(http.HandlerFunc(GetSession(b))))
	mux.Handle("GET /api/sessions/{id}/ws", requireAuth(http.HandlerFunc(SessionWS(b))))

	// Evaluations
	mux.Handle("GET /api/sessions/{id}/evaluation", requireAuth(http.HandlerFunc(GetEvaluation(b))))
	mux.Handle("POST /api/sessions/{id}/evaluate", requireAuth(http.HandlerFunc(RetryEvaluation(b))))

	// Educator
	mux.Handle("GET /api/sessions/{id}/educator", requireAuth(http.HandlerFunc(GetEducatorAnalysis(b))))
	mux.Handle("POST /api/sessions/{id}/educator", requireAuth(http.HandlerFunc(RequestEducatorAnalysis(b))))

	// Coach
	mux.Handle("GET /api/coach/latest", requireAuth(http.HandlerFunc(GetCoachAnalysis(b))))
	mux.Handle("POST /api/coach/analyze", requireAuth(http.HandlerFunc(RequestCoachAnalysis(b))))

	// Questions
	mux.Handle("GET /api/questions", requireAuth(http.HandlerFunc(ListQuestions(b))))
}

// SPAHandler serves the embedded SPA. Static assets served directly.
// All other paths return index.html for client-side routing.
func SPAHandler(fsys embed.FS) http.Handler {
	sub, err := fs.Sub(fsys, "web/dist")
	if err != nil {
		panic(fmt.Sprintf("embed sub: %v", err))
	}
	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path != "/" {
			if _, err := fs.Stat(sub, strings.TrimPrefix(path, "/")); err == nil {
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		// Fall back to index.html for client-side routing
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}
