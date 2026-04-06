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
	"github.com/btc/drill/internal/rpc"
)

// NewHandler builds the full HTTP handler chain: routes, CSRF, OTel tracing,
// and webhook/Connect CSRF exemption. Returns a ready-to-use http.Handler.
func NewHandler(b *backend.Backend, spaFS embed.FS, csrfKey []byte, secureCookies bool) (http.Handler, error) {
	mux := http.NewServeMux()
	if err := RegisterRoutes(mux, b); err != nil {
		return nil, fmt.Errorf("register routes: %w", err)
	}
	mux.Handle("/", SPAHandler(spaFS))

	otelHandler := otelhttp.NewMiddleware(drilotel.AppName)(mux)

	return csrfMiddleware(otelHandler, csrfKey, secureCookies), nil
}

// RegisterRoutes sets up all HTTP routes on the given mux.
// Used by NewHandler for production and directly by tests.
func RegisterRoutes(mux *http.ServeMux, b *backend.Backend) error {
	if err := rpc.Register(mux, b); err != nil {
		return fmt.Errorf("rpc register: %w", err)
	}

	mux.HandleFunc("GET /api/health", Health(b))

	// Admin — requires both auth and admin role.
	requireAuth := auth.RequireAuth(b)
	requireAdmin := auth.RequireAdmin()
	mux.Handle("GET /admin/jobs", requireAuth(requireAdmin(AdminJobsPlaceholder())))

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

	mux.Handle("GET /api/sessions/{id}/ws", requireAuth(http.HandlerFunc(SessionWS(b))))

	// Billing
	mux.Handle("POST /api/billing/checkout", requireAuth(http.HandlerFunc(PostCheckout(b))))
	mux.Handle("POST /api/billing/portal", requireAuth(http.HandlerFunc(PostPortal(b))))

	// Stripe webhook — no auth, signature verified.
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
		// Stripe webhook — exempt from CSRF; uses Stripe signature verification.
		if r.URL.Path == "/api/webhooks/stripe" && r.Method == http.MethodPost {
			next.ServeHTTP(w, r)
			return
		}
		// ConnectRPC — exempt from CSRF; POST with custom Content-Type headers
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
