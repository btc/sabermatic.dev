package drilotel_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/btc/drill/internal/drilotel"
)

// setupGlobalTracer sets the global OTel tracer provider to an in-memory
// exporter so otel.Tracer("drill/river") in JobTracer returns a real tracer.
func setupGlobalTracer(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		tp.Shutdown(context.Background())
		otel.SetTracerProvider(trace.NewNoopTracerProvider())
	})
	return exp
}

func TestSetTraceMetadata_WithSpan(t *testing.T) {
	_, tp := newTestTracer(t)
	tracer := tp.Tracer("test")
	ctx, span := tracer.Start(context.Background(), "enqueue")
	defer span.End()

	opts := &river.InsertOpts{Queue: "notifications", MaxAttempts: 3}
	drilotel.SetTraceMetadata(ctx, opts)

	assert.Equal(t, "notifications", opts.Queue)
	assert.Equal(t, 3, opts.MaxAttempts)

	var meta map[string]string
	require.NoError(t, json.Unmarshal(opts.Metadata, &meta))
	assert.Equal(t, span.SpanContext().TraceID().String(), meta["trace_id"])
	assert.Equal(t, span.SpanContext().SpanID().String(), meta["span_id"])
}

func TestSetTraceMetadata_MergesExisting(t *testing.T) {
	_, tp := newTestTracer(t)
	tracer := tp.Tracer("test")
	ctx, span := tracer.Start(context.Background(), "enqueue")
	defer span.End()

	opts := &river.InsertOpts{Metadata: []byte(`{"foo":"bar"}`)}
	drilotel.SetTraceMetadata(ctx, opts)

	var meta map[string]string
	require.NoError(t, json.Unmarshal(opts.Metadata, &meta))
	assert.Equal(t, "bar", meta["foo"])
	assert.NotEmpty(t, meta["trace_id"])
	assert.NotEmpty(t, meta["span_id"])
}

func TestSetTraceMetadata_NoSpan(t *testing.T) {
	opts := &river.InsertOpts{Queue: "default"}
	drilotel.SetTraceMetadata(context.Background(), opts)
	assert.Nil(t, opts.Metadata)
	assert.Equal(t, "default", opts.Queue)
}

func TestSetTraceMetadata_InvalidJSON(t *testing.T) {
	_, tp := newTestTracer(t)
	tracer := tp.Tracer("test")
	ctx, span := tracer.Start(context.Background(), "enqueue")
	defer span.End()

	opts := &river.InsertOpts{Metadata: []byte("not-json")}
	drilotel.SetTraceMetadata(ctx, opts)

	var meta map[string]string
	require.NoError(t, json.Unmarshal(opts.Metadata, &meta))
	assert.NotEmpty(t, meta["trace_id"])
}

func TestJobTracer_CreatesSpan(t *testing.T) {
	exp := setupGlobalTracer(t)

	jt := &drilotel.JobTracer{}
	job := &rivertype.JobRow{
		ID:      42,
		Kind:    "send_email",
		Queue:   "notifications",
		Attempt: 1,
	}

	err := jt.Work(context.Background(), job, func(ctx context.Context) error {
		return nil
	})
	require.NoError(t, err)

	spans := exp.GetSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "river.job/send_email", spans[0].Name)

	attrMap := make(map[string]any)
	for _, a := range spans[0].Attributes {
		attrMap[string(a.Key)] = a.Value.AsInterface()
	}
	assert.Equal(t, "send_email", attrMap["river.job_kind"])
	assert.Equal(t, int64(42), attrMap["river.job_id"])
	assert.Equal(t, "notifications", attrMap["river.queue"])
	assert.Equal(t, int64(1), attrMap["river.attempt"])
}

func TestJobTracer_RecordsError(t *testing.T) {
	exp := setupGlobalTracer(t)

	jt := &drilotel.JobTracer{}
	job := &rivertype.JobRow{Kind: "fail_job", Queue: "default", Attempt: 1}

	testErr := errors.New("job failed")
	err := jt.Work(context.Background(), job, func(ctx context.Context) error {
		return testErr
	})
	assert.Equal(t, testErr, err)

	spans := exp.GetSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, codes.Error, spans[0].Status.Code)
}

func TestJobTracer_SpanLink(t *testing.T) {
	exp := setupGlobalTracer(t)

	meta := []byte(`{"trace_id":"4bf92f3577b34da6a3ce929d0e0e4736","span_id":"00f067aa0ba902b7"}`)
	jt := &drilotel.JobTracer{}
	job := &rivertype.JobRow{Kind: "linked_job", Queue: "default", Attempt: 1, Metadata: meta}

	err := jt.Work(context.Background(), job, func(ctx context.Context) error {
		return nil
	})
	require.NoError(t, err)

	spans := exp.GetSpans()
	require.Len(t, spans, 1)
	require.Len(t, spans[0].Links, 1)
	assert.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", spans[0].Links[0].SpanContext.TraceID().String())
	assert.Equal(t, "00f067aa0ba902b7", spans[0].Links[0].SpanContext.SpanID().String())
}

func TestLinkFromMetadata_BadJSON(t *testing.T) {
	exp := setupGlobalTracer(t)

	jt := &drilotel.JobTracer{}
	job := &rivertype.JobRow{Kind: "bad_meta", Queue: "default", Attempt: 1, Metadata: []byte("garbage")}

	err := jt.Work(context.Background(), job, func(ctx context.Context) error {
		return nil
	})
	require.NoError(t, err)

	spans := exp.GetSpans()
	require.Len(t, spans, 1)
	assert.Empty(t, spans[0].Links)
}
