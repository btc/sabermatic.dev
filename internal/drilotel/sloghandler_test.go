package drilotel_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/btc/drill/internal/drilotel"
)

func newTestTracer(t *testing.T) (*tracetest.InMemoryExporter, *sdktrace.TracerProvider) {
	t.Helper()
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	t.Cleanup(func() { tp.Shutdown(context.Background()) }) //nolint:errcheck // test cleanup
	return exp, tp
}

func TestTraceHandler_LocalFormat(t *testing.T) {
	_, tp := newTestTracer(t)
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, nil)
	handler := drilotel.NewTraceHandler(inner, "")

	tracer := tp.Tracer("test")
	ctx, span := tracer.Start(context.Background(), "test-span")
	defer span.End()

	logger := slog.New(handler)
	logger.InfoContext(ctx, "hello")

	var out map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &out))
	assert.Contains(t, out, "trace_id")
	assert.Contains(t, out, "span_id")
	assert.NotEmpty(t, out["trace_id"])
	assert.NotEmpty(t, out["span_id"])
	assert.NotContains(t, out, "logging.googleapis.com/trace")
}

func TestTraceHandler_GCPFormat(t *testing.T) {
	_, tp := newTestTracer(t)
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, nil)
	handler := drilotel.NewTraceHandler(inner, "my-project")

	tracer := tp.Tracer("test")
	ctx, span := tracer.Start(context.Background(), "test-span")
	traceID := span.SpanContext().TraceID().String()
	spanID := span.SpanContext().SpanID().String()
	defer span.End()

	logger := slog.New(handler)
	logger.InfoContext(ctx, "hello")

	var out map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &out))
	assert.Equal(t, "projects/my-project/traces/"+traceID, out["logging.googleapis.com/trace"])
	assert.Equal(t, spanID, out["logging.googleapis.com/spanId"])
	assert.NotContains(t, out, "trace_id")
}

func TestTraceHandler_NoSpanContext(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, nil)
	handler := drilotel.NewTraceHandler(inner, "")

	logger := slog.New(handler)
	logger.Info("no context")

	var out map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &out))
	assert.NotContains(t, out, "trace_id")
	assert.NotContains(t, out, "span_id")
}

func TestTraceHandler_WithAttrs(t *testing.T) {
	_, tp := newTestTracer(t)
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, nil)
	handler := drilotel.NewTraceHandler(inner, "")

	tracer := tp.Tracer("test")
	ctx, span := tracer.Start(context.Background(), "test-span")
	defer span.End()

	logger := slog.New(handler).With("custom_key", "custom_val")
	logger.InfoContext(ctx, "with attrs")

	var out map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &out))
	assert.Contains(t, out, "trace_id")
	assert.Equal(t, "custom_val", out["custom_key"])
}
