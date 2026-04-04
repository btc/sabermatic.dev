package drilotel

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.opentelemetry.io/otel/trace"

	cloudmetric "github.com/GoogleCloudPlatform/opentelemetry-operations-go/exporter/metric"
	cloudtrace "github.com/GoogleCloudPlatform/opentelemetry-operations-go/exporter/trace"

	"github.com/btc/drill/internal/config"
)

// AppName is the application name used as the OTel tracer namespace prefix.
const AppName = "drill"

// Tracer returns an OTel tracer namespaced under AppName.
// Usage: var tracer = drilotel.Tracer("backend")
func Tracer(component string) trace.Tracer {
	return otel.Tracer(AppName + "/" + component)
}

// End records err on span (if non-nil) and ends it. Designed for use with
// named return values:
//
//	func Foo(ctx context.Context) (err error) {
//	    ctx, span := tracer.Start(ctx, "Foo")
//	    defer func() { drilotel.End(span, err) }()
//	    ...
//	}
func End(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}

// ExtractTraceparent returns a context carrying the remote span described by
// the W3C traceparent string. If traceparent is empty, ctx is returned as-is.
func ExtractTraceparent(ctx context.Context, traceparent string) context.Context {
	if traceparent == "" {
		return ctx
	}
	return otel.GetTextMapPropagator().Extract(ctx, propagation.MapCarrier{"traceparent": traceparent})
}

// Providers holds the OTel providers created by Init.
// When OTel is disabled, both provider fields are nil and Shutdown is a no-op.
type Providers struct {
	TracerProvider *sdktrace.TracerProvider
	MeterProvider  *sdkmetric.MeterProvider
	Shutdown       func(ctx context.Context) error
}

// Init creates and registers OTel tracer and meter providers.
// When cfg.Enabled is false, returns no-op providers with zero overhead.
func Init(cfg *config.Otel) (*Providers, error) {
	if !cfg.Enabled {
		return &Providers{
			Shutdown: func(context.Context) error { return nil },
		}, nil
	}

	res, err := buildResource(cfg.ServiceName)
	if err != nil {
		return nil, fmt.Errorf("build otel resource: %w", err)
	}

	traceExp, metricExp, err := buildExporters(cfg.Exporter)
	if err != nil {
		return nil, fmt.Errorf("build otel exporters: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExp),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.SampleRate))),
		sdktrace.WithResource(res),
	)

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp, sdkmetric.WithInterval(60*time.Second))),
		sdkmetric.WithResource(res),
	)

	otel.SetTracerProvider(tp)
	otel.SetMeterProvider(mp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	shutdown := func(ctx context.Context) error {
		return errors.Join(tp.Shutdown(ctx), mp.Shutdown(ctx))
	}

	return &Providers{
		TracerProvider: tp,
		MeterProvider:  mp,
		Shutdown:       shutdown,
	}, nil
}

func buildResource(serviceName string) (*resource.Resource, error) {
	version := "dev"
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		version = info.Main.Version
	}
	return resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion(version),
		),
	)
}

func buildExporters(name string) (sdktrace.SpanExporter, sdkmetric.Exporter, error) {
	switch name {
	case "dev":
		// One-line-per-span exporter for local development.
		te := &devTraceExporter{}
		me, err := stdoutmetric.New()
		if err != nil {
			return nil, nil, fmt.Errorf("stdout metric exporter: %w", err)
		}
		return te, me, nil

	case "stdout":
		te, err := stdouttrace.New()
		if err != nil {
			return nil, nil, fmt.Errorf("stdout trace exporter: %w", err)
		}
		me, err := stdoutmetric.New()
		if err != nil {
			return nil, nil, fmt.Errorf("stdout metric exporter: %w", err)
		}
		return te, me, nil

	case "google":
		te, err := cloudtrace.New()
		if err != nil {
			return nil, nil, fmt.Errorf("google trace exporter: %w", err)
		}
		me, err := cloudmetric.New()
		if err != nil {
			return nil, nil, fmt.Errorf("google metric exporter: %w", err)
		}
		return te, me, nil

	default:
		return nil, nil, fmt.Errorf("unknown otel exporter: %q (want \"dev\", \"stdout\", or \"google\")", name)
	}
}

// devTraceExporter logs one slog line per completed span. Only HTTP server
// spans include method/path/status; other spans log name and duration.
type devTraceExporter struct{}

func (e *devTraceExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	for _, s := range spans {
		attrs := make(map[string]any, len(s.Attributes()))
		for _, kv := range s.Attributes() {
			attrs[string(kv.Key)] = kv.Value.AsInterface()
		}

		method, _ := attrs["http.request.method"].(string)
		route, _ := attrs["http.route"].(string)
		status, _ := attrs["http.response.status_code"].(int64)
		dur := s.EndTime().Sub(s.StartTime()).Round(time.Millisecond)

		if method != "" {
			level := slog.LevelInfo
			if status >= 400 {
				level = slog.LevelWarn
			}
			if status >= 500 {
				level = slog.LevelError
			}
			slog.Log(context.Background(), level, "http",
				"method", method,
				"path", route,
				"status", status,
				"dur", dur,
			)
		}
	}
	return nil
}

func (e *devTraceExporter) Shutdown(context.Context) error { return nil }
