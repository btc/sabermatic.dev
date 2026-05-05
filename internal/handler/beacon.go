package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/btc/drill/internal/events"
)

// allowedBeaconEvents is the set of event_name values that frontend clients
// are permitted to emit via the beacon. Backend events are emitted server-side
// via Backend / RPC handlers, not through the beacon.
var allowedBeaconEvents = map[string]struct{}{
	"landing_view":   {},
	"signup_started": {},
}

type beaconRequest struct {
	EventName   string         `json:"event_name"`
	Referrer    string         `json:"referrer,omitempty"`
	UTMSource   string         `json:"utm_source,omitempty"`
	UTMMedium   string         `json:"utm_medium,omitempty"`
	UTMCampaign string         `json:"utm_campaign,omitempty"`
	Properties  map[string]any `json:"properties,omitempty"`
}

// BeaconHandler returns an http.HandlerFunc for POST /api/beacon. The handler
// validates the event_name against an allowlist, then constructs explicit
// emission attributes from the body (referrer/UTM take precedence over what
// the AnalyticsContextMiddleware captured from headers, since for beacon
// requests the browser-side document.referrer is the only source of truth).
func BeaconHandler(em *events.Emitter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		defer r.Body.Close()
		var req beaconRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid json body", http.StatusBadRequest)
			return
		}
		if _, ok := allowedBeaconEvents[req.EventName]; !ok {
			http.Error(w, "unknown event_name", http.StatusBadRequest)
			return
		}

		// Body-provided referrer/UTM take precedence over middleware-captured
		// header values (which point to the SPA page that fired the beacon,
		// not the external referrer the browser remembers).
		ctx := r.Context()
		if req.Referrer != "" {
			ctx = events.WithReferer(ctx, req.Referrer)
		}
		if req.UTMSource != "" || req.UTMMedium != "" || req.UTMCampaign != "" {
			ctx = events.WithUTM(ctx, req.UTMSource, req.UTMMedium, req.UTMCampaign)
		}

		// Convert properties map to slog.Attr slice.
		var props []slog.Attr
		for k, v := range req.Properties {
			props = append(props, slog.Any(k, v))
		}

		em.Emit(ctx, req.EventName, props...)
		w.WriteHeader(http.StatusNoContent)
	}
}
