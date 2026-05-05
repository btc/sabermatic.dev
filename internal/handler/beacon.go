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
	"sample_view":    {},
	"signup_started": {},
}

const (
	// beaconMaxBodyBytes caps the JSON request body. Beacon payloads carry
	// short event metadata; an unauth'd public endpoint must not let a single
	// request hold arbitrary memory.
	beaconMaxBodyBytes = 16 * 1024

	// beaconMaxProperties caps custom property keys per event. BigQuery's
	// auto-discovered properties RECORD column would accumulate columns from
	// any caller-supplied key; bound the cardinality at the door so a buggy
	// or hostile client can't bloat the schema.
	beaconMaxProperties = 16

	// beaconMaxFieldLen truncates string-typed fields (UTM, referrer) before
	// emission. Defense against a client sending megabyte-long values.
	beaconMaxFieldLen = 256
)

type beaconRequest struct {
	EventName   string         `json:"event_name"`
	Referrer    string         `json:"referrer"`
	UTMSource   string         `json:"utm_source"`
	UTMMedium   string         `json:"utm_medium"`
	UTMCampaign string         `json:"utm_campaign"`
	Properties  map[string]any `json:"properties,omitempty"`
}

// BeaconHandler returns an http.HandlerFunc for POST /api/beacon. The handler
// validates the event_name against an allowlist, caps body size and property
// cardinality, then emits via the Emitter using body-provided referrer/UTM
// (the frontend SDK is the contract: it always sends those fields, and only
// the browser sees the external document.referrer for a beacon request).
func BeaconHandler(em *events.Emitter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, beaconMaxBodyBytes)
		var req beaconRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid json body", http.StatusBadRequest)
			return
		}
		if _, ok := allowedBeaconEvents[req.EventName]; !ok {
			http.Error(w, "unknown event_name", http.StatusBadRequest)
			return
		}
		if len(req.Properties) > beaconMaxProperties {
			http.Error(w, "too many properties", http.StatusBadRequest)
			return
		}

		// Frontend SDK is the source of truth for referrer/UTM on beacon
		// requests; the middleware-captured Referer header points at the SPA
		// URL, not the external referrer. Always overwrite with body values
		// (including empty strings — the frontend means "no external referrer").
		ctx := r.Context()
		ctx = events.WithReferer(ctx, truncate(req.Referrer))
		ctx = events.WithUTM(ctx,
			truncate(req.UTMSource),
			truncate(req.UTMMedium),
			truncate(req.UTMCampaign),
		)

		// Convert properties map to slog.Attr slice. Truncate string-typed
		// values defensively — beaconMaxBodyBytes caps the request total but
		// individual string values within properties could otherwise consume
		// most of the budget and bloat BQ row sizes.
		var props []slog.Attr
		for k, v := range req.Properties {
			if s, ok := v.(string); ok {
				v = truncate(s)
			}
			props = append(props, slog.Any(k, v))
		}

		em.Emit(ctx, req.EventName, props...)
		w.WriteHeader(http.StatusNoContent)
	}
}

// truncate caps a string at beaconMaxFieldLen characters; defensive against
// caller-supplied UTM/referrer values that bloat log lines or BQ columns.
func truncate(s string) string {
	if len(s) > beaconMaxFieldLen {
		return s[:beaconMaxFieldLen]
	}
	return s
}
