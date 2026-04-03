package drilotel_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"

	"github.com/btc/drill/internal/config"
	"github.com/btc/drill/internal/drilotel"
)

func TestInit_Disabled(t *testing.T) {
	cfg := &config.Otel{Enabled: false}
	p, err := drilotel.Init(cfg)
	require.NoError(t, err)
	assert.Nil(t, p.TracerProvider)
	assert.Nil(t, p.MeterProvider)
	require.NoError(t, p.Shutdown(context.Background()))
}

// Init tests set the global OTel provider and must NOT use t.Parallel().
func TestInit_StdoutExporter(t *testing.T) {
	cfg := &config.Otel{
		Enabled:     true,
		Exporter:    "stdout",
		SampleRate:  1.0,
		ServiceName: "drill-test",
	}
	p, err := drilotel.Init(cfg)
	require.NoError(t, err)
	defer func() {
		p.Shutdown(context.Background())
		otel.SetTracerProvider(trace.NewNoopTracerProvider())
	}()

	assert.NotNil(t, p.TracerProvider)
	assert.NotNil(t, p.MeterProvider)

	// Verify the global tracer provider produces real spans.
	tracer := otel.Tracer("test")
	_, span := tracer.Start(context.Background(), "test-span")
	assert.True(t, span.SpanContext().IsValid())
	span.End()
}

func TestInit_InvalidExporter(t *testing.T) {
	cfg := &config.Otel{
		Enabled:  true,
		Exporter: "bogus",
	}
	_, err := drilotel.Init(cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "bogus")
}

func TestInit_FullRoundtrip(t *testing.T) {
	cfg := &config.Otel{
		Enabled:     true,
		Exporter:    "stdout",
		SampleRate:  1.0,
		ServiceName: "drill-roundtrip",
	}
	p, err := drilotel.Init(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { otel.SetTracerProvider(trace.NewNoopTracerProvider()) })

	tracer := otel.Tracer("roundtrip")
	ctx, span := tracer.Start(context.Background(), "roundtrip-span")
	_ = ctx
	span.End()

	err = p.Shutdown(context.Background())
	require.NoError(t, err)
}
