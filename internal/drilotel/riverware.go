package drilotel

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// SetTraceMetadata merges trace context from the current span into opts.Metadata.
// Preserves any existing metadata fields. No-op when there is no active span.
func SetTraceMetadata(ctx context.Context, opts *river.InsertOpts) {
	sc := trace.SpanFromContext(ctx).SpanContext()
	if !sc.IsValid() {
		return
	}

	traceJSON := []byte(fmt.Sprintf(`{"trace_id":"%s","span_id":"%s"}`, sc.TraceID(), sc.SpanID()))

	if len(opts.Metadata) == 0 || string(opts.Metadata) == "null" {
		opts.Metadata = traceJSON
		return
	}

	var existing map[string]json.RawMessage
	if err := json.Unmarshal(opts.Metadata, &existing); err != nil {
		opts.Metadata = traceJSON
		return
	}
	existing["trace_id"] = json.RawMessage(fmt.Sprintf(`"%s"`, sc.TraceID()))
	existing["span_id"] = json.RawMessage(fmt.Sprintf(`"%s"`, sc.SpanID()))
	merged, _ := json.Marshal(existing)
	opts.Metadata = merged
}

// JobTracer is a River WorkerMiddleware that creates an OTel span for every
// job execution. If the job metadata contains trace_id/span_id (set by
// SetTraceMetadata at enqueue time), the span includes a link to the
// originating trace.
type JobTracer struct {
	river.WorkerMiddlewareDefaults
}

func (*JobTracer) Work(ctx context.Context, job *rivertype.JobRow, doInner func(ctx context.Context) error) error {
	tracer := otel.Tracer("drill/river")
	opts := []trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			attribute.String("river.job_kind", job.Kind),
			attribute.Int64("river.job_id", job.ID),
			attribute.String("river.queue", job.Queue),
			attribute.Int("river.attempt", job.Attempt),
		),
	}
	if link, ok := linkFromMetadata(job.Metadata); ok {
		opts = append(opts, trace.WithLinks(link))
	}
	ctx, span := tracer.Start(ctx, "river.job/"+job.Kind, opts...)
	defer span.End()

	if err := doInner(ctx); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	return nil
}

func linkFromMetadata(metadata []byte) (trace.Link, bool) {
	if len(metadata) == 0 {
		return trace.Link{}, false
	}
	var m struct {
		TraceID string `json:"trace_id"`
		SpanID  string `json:"span_id"`
	}
	if err := json.Unmarshal(metadata, &m); err != nil || m.TraceID == "" || m.SpanID == "" {
		return trace.Link{}, false
	}
	tid, err := trace.TraceIDFromHex(m.TraceID)
	if err != nil {
		return trace.Link{}, false
	}
	sid, err := trace.SpanIDFromHex(m.SpanID)
	if err != nil {
		return trace.Link{}, false
	}
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    tid,
		SpanID:     sid,
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})
	return trace.Link{SpanContext: sc}, true
}
