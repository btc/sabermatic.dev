package handler

import (
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"strings"

	"github.com/gorilla/csrf"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/backend"
	"github.com/btc/drill/internal/drilotel"
)

// NewHandler builds the full HTTP handler chain: routes, CSRF, OTel tracing,
// and webhook CSRF exemption. Returns a ready-to-use http.Handler.
func NewHandler(b *backend.Backend, spaFS embed.FS, csrfKey []byte, secureCookies bool) http.Handler {
	mux := http.NewServeMux()
	RegisterRoutes(mux, b)
	mux.Handle("/", SPAHandler(spaFS))

	csrfProtect := csrf.Protect(
		csrfKey,
		csrf.Secure(secureCookies),
		csrf.HttpOnly(false),
		csrf.CookieName("drill_csrf"),
		csrf.Path("/"),
		csrf.SameSite(csrf.SameSiteLaxMode),
	)

	otelHandler := otelhttp.NewMiddleware(drilotel.AppName)(mux)

	// Expose the masked CSRF token via response header so the SPA can read it.
	csrfProtected := csrfProtect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-CSRF-Token", csrf.Token(r))
		otelHandler.ServeHTTP(w, r)
	}))

	// Exempt the Stripe webhook from CSRF — it uses Stripe signature verification.
	// For plaintext HTTP (local dev), mark requests so gorilla/csrf skips
	// HTTPS-only referer/origin checks.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/webhooks/stripe" && r.Method == http.MethodPost {
			otelHandler.ServeHTTP(w, r)
			return
		}
		if !secureCookies {
			r = csrf.PlaintextHTTPRequest(r)
		}
		csrfProtected.ServeHTTP(w, r)
	})
}

// RegisterRoutes sets up all HTTP routes on the given mux.
// Used by NewHandler for production and directly by tests.
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

	// Billing
	mux.Handle("POST /api/billing/checkout", requireAuth(http.HandlerFunc(PostCheckout(b))))
	mux.Handle("POST /api/billing/portal", requireAuth(http.HandlerFunc(PostPortal(b))))
	mux.Handle("GET /api/me/usage", requireAuth(http.HandlerFunc(GetUsage(b))))

	// Stripe webhook — no auth, signature verified.
	// Must be exempt from CSRF middleware. Registered here before any
	// CSRF wrapping, or add to CSRF exemption filter.
	mux.HandleFunc("POST /api/webhooks/stripe", PostStripeWebhook(b))
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
				// Hashed asset files are immutable and can be cached forever
				if strings.Contains(path, "/assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		// Fall back to index.html for client-side routing
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}
