package handler

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/btc/drill/internal/backend"
)

// GetKeepLink returns a handler for GET /sub/keep?t=<token>. The route is
// mounted publicly: the signed token IS the auth — there is no session
// requirement. Status codes:
//   - 200 on a valid (or expired) link with a rendered HTML page.
//   - 400 on an invalid/tampered token.
//   - 500 on infrastructure failure (DB, Stripe). Stripe errors mid-reversal
//     bubble up — the user can retry the same link safely (replays are
//     idempotent: a replay after a partial-failure reversal renders the
//     confirmation page either way).
func GetKeepLink(b *backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := r.URL.Query().Get("t")
		outcome, err := b.HandleKeepLink(r.Context(), tok)
		if err != nil {
			slog.Error("keep link handler", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		switch outcome.Status {
		case backend.KeepLinkInvalid:
			renderInvalidLink(w)
		case backend.KeepLinkExpired:
			renderPeriodEnded(w, outcome.PeriodEnd)
		case backend.KeepLinkKept:
			renderKeptConfirmation(w, outcome.PeriodEnd)
		default:
			slog.Error("keep link unknown outcome status", "status", outcome.Status)
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
	}
}

// renderInvalidLink writes a 400 with a minimal "bad link" page. The body
// contains no user-controlled data, so a static string is safe.
//
// Write errors are ignored: by the time we know one occurred, the client has
// already disconnected (TCP RST/closed pipe). There's nothing useful for the
// caller to do, and the response status has already been sent.
func renderInvalidLink(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	_, _ = fmt.Fprint(w, `<!doctype html><html><body>
<h1>This link is invalid</h1>
<p>Try clicking the most recent cancel email link, or sign in to your account directly.</p>
</body></html>`)
}

// renderPeriodEnded writes a 200 with a "subscription has ended" page. The
// only dynamic data (end date) comes from a verified token's claims and is
// formatted via time.Format — no template/HTML escaping concerns.
func renderPeriodEnded(w http.ResponseWriter, end time.Time) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprintf(w, `<!doctype html><html><body>
<h1>Your subscription has ended</h1>
<p>The keep-link expired on %s. To restart, please sign in.</p>
</body></html>`, end.UTC().Format("January 2, 2006"))
}

// renderKeptConfirmation writes a 200 with the "you're all set" page. Same
// safety story as renderPeriodEnded: end-date is a formatted time, no user
// HTML can leak in.
func renderKeptConfirmation(w http.ResponseWriter, end time.Time) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprintf(w, `<!doctype html><html><body>
<h1>You're all set</h1>
<p>Your Sabermatic subscription will renew normally on %s.</p>
<p>Welcome back.</p>
</body></html>`, end.UTC().Format("January 2, 2006"))
}
