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
	"github.com/btc/drill/internal/ratelimit"
	"github.com/btc/drill/internal/rpc"
)

// RateLimiters holds the rate limiters used by HTTP routes.
// Created externally and passed in -- RegisterRoutes just wires them up.
type RateLimiters struct {
	// Auth limits unauthenticated endpoints by client IP.
	Auth *ratelimit.Limiter
	// User limits authenticated mutation endpoints by user ID.
	User *ratelimit.Limiter
}

// NewHandler builds the full HTTP handler chain: routes, CSRF, OTel tracing,
// and webhook/Connect CSRF exemption. Returns a ready-to-use http.Handler.
func NewHandler(b *backend.Backend, spaFS embed.FS, csrfKey []byte, secureCookies bool, rl *RateLimiters) (http.Handler, error) {
	mux := http.NewServeMux()
	if err := RegisterRoutes(mux, b, rl); err != nil {
		return nil, fmt.Errorf("register routes: %w", err)
	}
	mux.Handle("/", SPAHandler(spaFS))

	otelHandler := otelhttp.NewMiddleware(drilotel.AppName)(mux)

	return csrfMiddleware(otelHandler, csrfKey, secureCookies), nil
}

// RegisterRoutes sets up all HTTP routes on the given mux.
// Used by NewHandler for production and directly by tests.
func RegisterRoutes(mux *http.ServeMux, b *backend.Backend, rl *RateLimiters) error {
	// ConnectRPC services (migrated from REST)
	if err := rpc.Register(mux, b); err != nil {
		return fmt.Errorf("rpc register: %w", err)
	}

	userRL := rl.User.MiddlewareByKey(func(r *http.Request) string {
		if u := auth.UserFromContext(r.Context()); u != nil {
			return u.ID.String()
		}
		return ""
	})

	mux.HandleFunc("GET /api/health", Health(b))
	mux.HandleFunc("GET /admin/jobs", AdminJobsPlaceholder())

	// Auth -- rate-limited by client IP
	mux.Handle("POST /api/auth/signup", rl.Auth.Middleware(Signup(b)))
	mux.Handle("POST /api/auth/login", rl.Auth.Middleware(Login(b)))
	mux.HandleFunc("POST /api/auth/logout", Logout(b))
	mux.Handle("POST /api/auth/verify-email", rl.Auth.Middleware(VerifyEmail(b)))
	mux.Handle("POST /api/auth/forgot-password", rl.Auth.Middleware(ForgotPassword(b)))
	mux.Handle("POST /api/auth/reset-password", rl.Auth.Middleware(ResetPassword(b)))

	// OAuth
	mux.HandleFunc("GET /api/auth/oauth/{provider}", OAuthStart(b))
	mux.HandleFunc("GET /api/auth/oauth/{provider}/callback", OAuthCallback(b))

	// User
	requireAuth := auth.RequireAuth(b)
	mux.Handle("GET /api/me", requireAuth(GetMe(b)))

	// Sessions -- user rate-limited
	mux.Handle("POST /api/sessions", requireAuth(userRL(CreateSession(b))))
	mux.Handle("GET /api/sessions", requireAuth(ListSessions(b)))
	mux.Handle("GET /api/sessions/{id}", requireAuth(GetSession(b)))
	mux.Handle("GET /api/sessions/{id}/ws", requireAuth(userRL(SessionWS(b))))

	// Evaluations
	mux.Handle("GET /api/sessions/{id}/evaluation", requireAuth(GetEvaluation(b)))
	mux.Handle("POST /api/sessions/{id}/evaluate", requireAuth(userRL(RetryEvaluation(b))))

	// Educator -- user rate-limited
	mux.Handle("GET /api/sessions/{id}/educator", requireAuth(GetEducatorAnalysis(b)))
	mux.Handle("POST /api/sessions/{id}/educator", requireAuth(userRL(RequestEducatorAnalysis(b))))

	// Coach -- user rate-limited
	mux.Handle("GET /api/coach/latest", requireAuth(GetCoachAnalysis(b)))
	mux.Handle("POST /api/coach/analyze", requireAuth(userRL(RequestCoachAnalysis(b))))

	// Billing -- user rate-limited
	mux.Handle("POST /api/billing/checkout", requireAuth(userRL(PostCheckout(b))))
	mux.Handle("POST /api/billing/portal", requireAuth(userRL(PostPortal(b))))
	mux.Handle("GET /api/me/usage", requireAuth(GetUsage(b)))

	// Stripe webhook -- no auth, signature verified.
	// Must be exempt from CSRF middleware. Registered here before any
	// CSRF wrapping, or add to CSRF exemption filter.
	mux.HandleFunc("POST /api/webhooks/stripe", PostStripeWebhook(b))

	return nil
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

// csrfMiddleware wraps the given handler with gorilla/csrf protection, exposes
// the CSRF token via response header, and exempts routes that have their own
// protection (Stripe webhooks use signature verification; ConnectRPC uses
// custom Content-Type headers that prevent cross-origin form submissions).
func csrfMiddleware(next http.Handler, csrfKey []byte, secureCookies bool) http.Handler {
	csrfProtect := csrf.Protect(
		csrfKey,
		csrf.Secure(secureCookies),
		csrf.HttpOnly(false),
		csrf.CookieName("drill_csrf"),
		csrf.Path("/"),
		csrf.SameSite(csrf.SameSiteLaxMode),
	)

	// CSRF-protected handler that exposes the masked token via response header.
	csrfProtected := csrfProtect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-CSRF-Token", csrf.Token(r))
		next.ServeHTTP(w, r)
	}))

	connectPrefixes := rpc.ConnectPathPrefixes()

	return SecurityHeaders(secureCookies, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Stripe webhook -- exempt from CSRF; uses Stripe signature verification.
		if r.URL.Path == "/api/webhooks/stripe" && r.Method == http.MethodPost {
			next.ServeHTTP(w, r)
			return
		}
		// ConnectRPC -- exempt from CSRF; POST with custom Content-Type headers
		// cannot be sent by simple HTML forms without CORS preflight.
		for _, prefix := range connectPrefixes {
			if strings.HasPrefix(r.URL.Path, "/"+prefix+"/") {
				next.ServeHTTP(w, r)
				return
			}
		}
		// For plaintext HTTP (local dev), mark requests so gorilla/csrf skips
		// HTTPS-only referer/origin checks.
		if !secureCookies {
			r = csrf.PlaintextHTTPRequest(r)
		}
		csrfProtected.ServeHTTP(w, r)
	}))
}
