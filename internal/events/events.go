package events

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
)

// Emitter writes analytics events to a structured slog logger. Emissions are
// fire-and-forget and never return errors or panic. The on-the-wire shape is
// one JSON log line per event, tagged `analytics_event=true` so a Cloud Logging
// sink can route only analytics rows to BigQuery.
type Emitter struct {
	logger *slog.Logger
}

// NewEmitter returns an Emitter that writes to the given slog.Logger. Pass
// the application's main slog logger. In Cloud Run the logger writes to
// stderr (cmd/drill/main.go's buildLogWriter falls back to os.Stderr when
// the configured LOG_FILE path isn't writable); Cloud Run forwards both
// stdout and stderr to Cloud Logging, where the sink filter routes
// analytics_event=true entries on to BigQuery.
//
// The provided handler is wrapped with alwaysEnabledHandler so analytics
// events bypass any LOG_LEVEL filtering on the application logger. Without
// this wrapper, raising LOG_LEVEL=warn (a reasonable cost-control move) would
// silently kill every analytics event without any error or warning.
func NewEmitter(logger *slog.Logger) *Emitter {
	if logger == nil {
		return &Emitter{}
	}
	return &Emitter{logger: slog.New(&alwaysEnabledHandler{inner: logger.Handler()})}
}

// Emit writes a single analytics event. Never blocks the caller, never
// returns an error. Pulls visitor_id/user_id/referer/UTM from ctx (populated
// by middleware) and writes a structured log line.
//
// Properties specific to the event are passed via props as slog.Attrs and
// land under a "properties" subkey for clean read-side flattening into a BQ
// RECORD column.
func (e *Emitter) Emit(ctx context.Context, name string, props ...slog.Attr) {
	if e == nil || e.logger == nil {
		return
	}
	cv := ContextValuesFromContext(ctx)

	attrs := []slog.Attr{
		slog.Bool("analytics_event", true),
		slog.String("event_id", uuid.NewString()),
		slog.String("event_name", name),
		slog.String("visitor_id", cv.VisitorID),
		slog.String("user_id", cv.UserID),
		slog.String("session_id", cv.SessionID),
		slog.String("referer", cv.Referer),
		slog.String("utm_source", cv.UTMSource),
		slog.String("utm_medium", cv.UTMMedium),
		slog.String("utm_campaign", cv.UTMCampaign),
		slog.String("path", cv.Path),
		slog.String("user_agent", cv.UserAgent),
	}
	if len(props) > 0 {
		// Convert []slog.Attr to []any for slog.Group construction.
		propsAny := make([]any, len(props))
		for i, a := range props {
			propsAny[i] = a
		}
		attrs = append(attrs, slog.Group("properties", propsAny...))
	}

	e.logger.LogAttrs(ctx, slog.LevelInfo, "analytics", attrs...)
}

// alwaysEnabledHandler wraps a slog.Handler and reports Enabled=true for
// every level, while delegating Handle/WithAttrs/WithGroup to the inner
// handler. Used by Emitter so analytics events can't be filtered out by
// LOG_LEVEL config — the BQ sink filter (jsonPayload.analytics_event=true)
// is the only routing mechanism we want to gate on.
type alwaysEnabledHandler struct {
	inner slog.Handler
}

func (h *alwaysEnabledHandler) Enabled(_ context.Context, _ slog.Level) bool {
	return true
}

func (h *alwaysEnabledHandler) Handle(ctx context.Context, r slog.Record) error {
	return h.inner.Handle(ctx, r)
}

func (h *alwaysEnabledHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &alwaysEnabledHandler{inner: h.inner.WithAttrs(attrs)}
}

func (h *alwaysEnabledHandler) WithGroup(name string) slog.Handler {
	return &alwaysEnabledHandler{inner: h.inner.WithGroup(name)}
}
