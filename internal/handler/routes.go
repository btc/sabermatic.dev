package handler

import (
	"embed"
	"fmt"
	"html"
	"io/fs"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/csrf"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/backend"
	"github.com/btc/drill/internal/branding"
	"github.com/btc/drill/internal/drilotel"
	"github.com/btc/drill/internal/rpc"
)

// NewHandler builds the full HTTP handler chain: routes, CSRF, OTel tracing,
// and webhook/Connect CSRF exemption. Returns a ready-to-use http.Handler.
func NewHandler(b *backend.Backend, spaFS embed.FS) (http.Handler, error) {
	cfg := b.Config()
	baseURL := cfg.Auth.BaseURL
	csrfKey := auth.DeriveKey(cfg.Auth.TokenSecret, "csrf")
	secureCookies := cfg.Auth.SecureCookies()

	mux := http.NewServeMux()
	if err := RegisterRoutes(mux, b); err != nil {
		return nil, fmt.Errorf("register routes: %w", err)
	}
	spaHandler, err := SPAHandler(spaFS, baseURL)
	if err != nil {
		return nil, fmt.Errorf("spa handler: %w", err)
	}
	mux.Handle("/", spaHandler)

	otelHandler := otelhttp.NewMiddleware(drilotel.AppName,
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			if r.Pattern != "" {
				return r.Pattern
			}
			return r.Method + " " + r.URL.Path
		}),
		otelhttp.WithFilter(func(r *http.Request) bool {
			for _, prefix := range rpc.ConnectPathPrefixes() {
				if strings.HasPrefix(r.URL.Path, "/"+prefix+"/") {
					return false
				}
			}
			return true
		}),
	)(mux)

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
	mux.Handle("/admin/jobs/", requireAuth(requireAdmin(b.RiverUIHandler())))

	// Auth (ConnectRPC AuthService handles signup/login/logout/etc.)

	// OAuth
	mux.HandleFunc("GET /api/auth/oauth/{provider}", OAuthStart(b))
	mux.HandleFunc("GET /api/auth/oauth/{provider}/callback", OAuthCallback(b))

	// Stripe webhook — no auth, signature verified.
	// Must be exempt from CSRF middleware. Registered here before any
	// CSRF wrapping, or add to CSRF exemption filter.
	mux.HandleFunc("POST /api/webhooks/stripe", PostStripeWebhook(b))

	return nil
}

// ogRoute defines OG meta tag content for a public route.
type ogRoute struct {
	title       string
	description string
	image       string // path relative to base URL
}

var ogRoutes = map[string]ogRoute{
	"/": {
		title:       branding.AppName,
		description: "data-driven system design prep",
		image:       "/og-landing.png",
	},
	"/about": {
		title:       branding.AppName,
		description: "data-driven system design prep",
		image:       "/og-landing.png",
	},
	"/sample": {
		title:       branding.AppName + " — sample evaluation",
		description: "See a real system design interview evaluated across 5 dimensions",
		image:       "/og-sample.png",
	},
}

// SPAHandler serves the embedded SPA. Static assets served directly.
// All other paths return index.html for client-side routing.
// For paths with OG tags defined, the tags are injected before </head>.
// baseURL is the public URL (e.g., "https://sabermatic.dev") used for
// absolute og:url and og:image values. Pass "" for tests.
func SPAHandler(fsys fs.FS, baseURL string) (http.Handler, error) {
	sub, err := fs.Sub(fsys, "web/dist")
	if err != nil {
		return nil, fmt.Errorf("embed sub: %w", err)
	}

	indexBytes, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		return nil, fmt.Errorf("read index.html: %w", err)
	}
	indexHTML := string(indexBytes)

	// Pre-compute OG-injected HTML at init time
	ogPages := make(map[string][]byte, len(ogRoutes))
	for path, og := range ogRoutes {
		tags := fmt.Sprintf(
			`<meta property="og:title" content="%s">`+
				`<meta property="og:description" content="%s">`+
				`<meta property="og:type" content="website">`+
				`<meta property="og:url" content="%s%s">`+
				`<meta property="og:image" content="%s%s">`,
			html.EscapeString(og.title), html.EscapeString(og.description),
			baseURL, path, baseURL, og.image,
		)
		ogPages[path] = []byte(strings.Replace(indexHTML, "</head>", tags+"</head>", 1))
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

		// Serve pre-computed OG-injected HTML if this path has OG tags
		if body, ok := ogPages[path]; ok {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			_, _ = w.Write(body)
			return
		}

		// Fall back to index.html for client-side routing
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	}), nil
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
		// River UI — exempt from CSRF; uses application/json bodies which cannot be
		// submitted cross-origin by a simple HTML form. Unauthenticated requests to
		// this prefix are rejected by requireAuth in the mux before reaching the handler.
		// TODO: flip csrfMiddleware to opt-in model — exempt list is growing.
		if strings.HasPrefix(r.URL.Path, "/admin/jobs/") || r.URL.Path == "/admin/jobs" {
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
