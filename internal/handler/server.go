package handler

import (
	"fmt"
	"io/fs"
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/btc/drill/internal/backend"
	"github.com/btc/drill/internal/drilotel"
	"github.com/btc/drill/internal/rpc"
)

// NewHandler builds the full HTTP handler chain: routes, OTel tracing, and
// security headers. Returns a ready-to-use http.Handler.
//
// OTel tracing: otelhttp wraps the entire mux and produces the HTTP-level
// span. Connect routes also produce a child Connect-level span via the
// otelconnect interceptor in rpc.Register. drilotel uses
// ParentBased(TraceIDRatioBased) so the child inherits the parent's
// sampling decision — unsampled traces cost nothing.
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
