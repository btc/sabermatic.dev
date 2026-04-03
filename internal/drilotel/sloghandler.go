package drilotel

import (
	"context"
	"fmt"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

// TraceHandler wraps a slog.Handler, injecting trace_id and span_id from
// the OTel span in the context. When gcpProjectID is non-empty, it uses
// Cloud Logging field names for automatic trace correlation.
type TraceHandler struct {
	inner        slog.Handler
	gcpProjectID string
}

// NewTraceHandler creates a TraceHandler wrapping inner. Pass an empty
// gcpProjectID for plain trace_id/span_id fields (local dev), or a GCP
// project ID for Cloud Logging-compatible fields.
func NewTraceHandler(inner slog.Handler, gcpProjectID string) *TraceHandler {
	return &TraceHandler{inner: inner, gcpProjectID: gcpProjectID}
}

func (h *TraceHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *TraceHandler) Handle(ctx context.Context, r slog.Record) error {
	sc := trace.SpanFromContext(ctx).SpanContext()
	if sc.IsValid() {
		if h.gcpProjectID != "" {
			r.AddAttrs(
				slog.String("logging.googleapis.com/trace",
					fmt.Sprintf("projects/%s/traces/%s", h.gcpProjectID, sc.TraceID())),
				slog.String("logging.googleapis.com/spanId", sc.SpanID().String()),
			)
		} else {
			r.AddAttrs(
				slog.String("trace_id", sc.TraceID().String()),
				slog.String("span_id", sc.SpanID().String()),
			)
		}
	}
	return h.inner.Handle(ctx, r)
}

func (h *TraceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &TraceHandler{inner: h.inner.WithAttrs(attrs), gcpProjectID: h.gcpProjectID}
}

func (h *TraceHandler) WithGroup(name string) slog.Handler {
	return &TraceHandler{inner: h.inner.WithGroup(name), gcpProjectID: h.gcpProjectID}
}
