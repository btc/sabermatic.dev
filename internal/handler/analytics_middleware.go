package handler

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/events"
)

// VisitorCookieName is the name of the first-party visitor identification
// cookie set on every public request. Used for stitching anonymous funnel
// activity to a single visitor across requests.
const VisitorCookieName = "sabermatic_visitor"

// AnalyticsContextMiddleware reads or sets the visitor cookie, captures the
// Referer header and UTM query params, and stashes them in the request
// context for downstream events.Emit calls. Runs on every request so that
// ConnectRPC handlers see populated values.
//
// secureCookies should be true on HTTPS deployments; pass the same flag the
// session cookie uses.
func AnalyticsContextMiddleware(secureCookies bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Read or mint visitor_id.
			var visitorID string
			if c, err := r.Cookie(VisitorCookieName); err == nil && c.Value != "" {
				visitorID = c.Value
			} else {
				visitorID = uuid.NewString()
				http.SetCookie(w, &http.Cookie{
					Name:     VisitorCookieName,
					Value:    visitorID,
					Path:     "/",
					HttpOnly: true,
					Secure:   secureCookies,
					SameSite: http.SameSiteLaxMode,
					MaxAge:   60 * 60 * 24 * 365, // 1 year
				})
			}

			// Capture referer and UTM params.
			referer := r.Header.Get("Referer")
			q := r.URL.Query()
			utmSource := q.Get("utm_source")
			utmMedium := q.Get("utm_medium")
			utmCampaign := q.Get("utm_campaign")

			ctx := r.Context()
			ctx = events.WithVisitorID(ctx, visitorID)
			ctx = events.WithReferer(ctx, referer)
			ctx = events.WithUTM(ctx, utmSource, utmMedium, utmCampaign)
			ctx = events.WithRequestPath(ctx, r.URL.Path)
			ctx = events.WithUserAgent(ctx, r.Header.Get("User-Agent"))

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// Compile-time assertion that auth package's session cookie name doesn't
// collide with our visitor cookie name. (Belt and suspenders against future
// renames.)
var _ = auth.SessionCookieName
