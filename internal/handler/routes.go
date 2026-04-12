package handler

import (
	"fmt"
	"html"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/btc/drill/internal/backend"
	"github.com/btc/drill/internal/branding"
	"github.com/btc/drill/internal/drilotel"
	"github.com/btc/drill/internal/rpc"
)

// NewHandler builds the full HTTP handler chain: routes, OTel tracing, and
// security headers. Returns a ready-to-use http.Handler.
func NewHandler(b *backend.Backend, spaFS fs.FS) (http.Handler, error) {
	cfg := b.Config()
	baseURL := cfg.Auth.BaseURL
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
	)(mux)

	return SecurityHeaders(secureCookies, otelHandler), nil
}

// RegisterRoutes sets up all HTTP routes on the given mux.
// Used by NewHandler for production and directly by tests.
//
// Security model: this app does not use anti-CSRF tokens. ConnectRPC's
// Connect-Protocol-Version custom header forces a CORS preflight, which
// the absence of permissive CORS config blocks; the SPA is served
// same-origin; session cookies are SameSite=Lax; the Stripe webhook is
// signature-verified; OAuth callbacks use the state parameter. If a future
// cookie-authenticated REST mutation endpoint is added, wrap it in a
// per-route CSRF helper at registration time.
func RegisterRoutes(mux *http.ServeMux, b *backend.Backend) error {
	if err := rpc.Register(mux, b); err != nil {
		return fmt.Errorf("rpc register: %w", err)
	}

	mux.HandleFunc("GET /api/health", Health(b))

	// Admin — requires both auth and admin role.
	requireAuth := RequireAuth(b)
	requireAdmin := RequireAdmin()
	mux.Handle("/admin/jobs/", requireAuth(requireAdmin(b.RiverUIHandler())))

	// OAuth
	mux.HandleFunc("GET /api/auth/oauth/{provider}", OAuthStart(b))
	mux.HandleFunc("GET /api/auth/oauth/{provider}/callback", OAuthCallback(b))

	// Stripe webhook — signature-verified by the handler.
	mux.HandleFunc("POST /api/webhooks/stripe", PostStripeWebhook(b))

	// Local-dev storage server: serve uploaded files via HTTP so the browser
	// can load them. In production (Storage.Backend == "gcs"), files are
	// served directly from GCS by signed URLs.
	storage := b.Config().Storage
	if storage.Backend == "local" {
		mux.Handle("/storage/", http.StripPrefix("/storage/",
			http.FileServer(http.Dir(storage.LocalDir))))
	}

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

	if !strings.Contains(indexHTML, "</head>") {
		slog.Warn("index.html missing </head> — OG tags will not be injected")
	}

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
			html.EscapeString(baseURL), html.EscapeString(path),
			html.EscapeString(baseURL), html.EscapeString(og.image),
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
