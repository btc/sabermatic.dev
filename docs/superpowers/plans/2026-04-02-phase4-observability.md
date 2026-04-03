# Phase 4: Observability Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add OpenTelemetry tracing, structured log correlation, SQL tracing, and River job instrumentation so every HTTP request, DB query, and background job is observable from day one.

**Architecture:** New `internal/drilotel` package owns OTel SDK init/shutdown, a slog handler wrapper for trace context injection, and River worker middleware for job tracing with span links. HTTP spans come from `otelhttp`, SQL spans from `otelpgx` (injected via DI into the pool), and user_id enrichment happens inside the existing `RequireAuth` middleware. Dual-mode exporters: stdout for dev, Google Cloud for production.

**Tech Stack:** OpenTelemetry Go SDK, otelhttp, otelpgx, GoogleCloudPlatform/opentelemetry-operations-go, River v0.32 WorkerMiddleware

**Spec:** `docs/superpowers/specs/2026-04-02-phase4-observability-design.md`

---

## File Map

### New files
| File | Responsibility |
|---|---|
| `internal/drilotel/drilotel.go` | `Init()` / `Shutdown()`, `Providers` struct, exporter selection (stdout vs google), global provider registration |
| `internal/drilotel/drilotel_test.go` | Init roundtrip tests: disabled mode, stdout mode, invalid exporter, full lifecycle |
| `internal/drilotel/sloghandler.go` | `TraceHandler` — `slog.Handler` wrapper injecting trace_id/span_id with dual format (plain vs Cloud Logging) |
| `internal/drilotel/sloghandler_test.go` | Trace context injection, GCP format, no-op when invalid, WithAttrs/WithGroup |
| `internal/drilotel/riverware.go` | `JobTracer` (River WorkerMiddleware), `SetTraceMetadata`, `linkFromMetadata` |
| `internal/drilotel/riverware_test.go` | Span creation, span links, metadata merge, error recording |

### Modified files
| File | Change |
|---|---|
| `internal/config/config.go` | Add `Otel` struct to `Config`; add `tracer pgx.QueryTracer` param to `Database.NewPool` |
| `internal/auth/middleware.go` | Add `user_id` span attribute in `RequireAuth` after successful auth |
| `internal/backend/backend.go` | Pass `otelpgx.NewTracer()` to `NewPool`; add `Middleware` to River client config |
| `internal/backend/auth.go` | Add `drilotel.SetTraceMetadata` to both enqueue call sites |
| `cmd/drill/main.go` | Relocate logger to `run()`, wrap with `TraceHandler`, add OTel init/shutdown, wire `otelhttp` middleware |
| `.env.example` | Add `OTEL_ENABLED`, `OTEL_EXPORTER` |
| `go.mod` | New direct deps: otel/sdk, otelhttp, otelpgx, GCP exporters |

---

### Task 1: Add dependencies and config

**Files:**
- Modify: `go.mod`
- Modify: `internal/config/config.go`
- Modify: `.env.example`

- [ ] **Step 1: Add OTel and otelpgx dependencies**

```bash
cd /Users/btc/Projects/src/drill
go get go.opentelemetry.io/otel/sdk@latest \
  go.opentelemetry.io/otel/sdk/metric@latest \
  go.opentelemetry.io/otel/exporters/stdout/stdouttrace@latest \
  go.opentelemetry.io/otel/exporters/stdout/stdoutmetric@latest \
  go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp@latest \
  github.com/GoogleCloudPlatform/opentelemetry-operations-go/exporter/trace@latest \
  github.com/GoogleCloudPlatform/opentelemetry-operations-go/exporter/metric@latest \
  github.com/exaring/otelpgx@latest
```

- [ ] **Step 2: Verify it compiles**

```bash
go build ./...
```

Expected: success with no errors.

- [ ] **Step 3: Add `Otel` struct to config and `tracer` param to `NewPool`**

In `internal/config/config.go`, add the `Otel` struct and field to `Config`:

```go
type Otel struct {
	Enabled      bool    `env:"OTEL_ENABLED,default=false"`
	Exporter     string  `env:"OTEL_EXPORTER,default=stdout"`
	SampleRate   float64 `env:"OTEL_SAMPLE_RATE,default=1.0"`
	ServiceName  string  `env:"OTEL_SERVICE_NAME,default=drill"`
	GCPProjectID string  `env:"GOOGLE_CLOUD_PROJECT"`
}
```

Add `Otel Otel` field to the `Config` struct (after `OAuth`).

Change `NewPool` signature to accept a `pgx.QueryTracer`:

```go
import "github.com/jackc/pgx/v5"

func (d *Database) NewPool(ctx context.Context, tracer pgx.QueryTracer) (*pgxpool.Pool, error) {
```

Inside `NewPool`, after `poolCfg.MaxConns = d.MaxPoolConns`, add:

```go
	if tracer != nil {
		poolCfg.ConnConfig.Tracer = tracer
	}
```

- [ ] **Step 4: Fix the one existing caller of `NewPool` in `backend.go`**

In `internal/backend/backend.go`, change line 33 from:

```go
pool, err := cfg.Database.NewPool(context.Background())
```

to:

```go
pool, err := cfg.Database.NewPool(context.Background(), nil)
```

(We pass `nil` for now — Task 6 will change this to `otelpgx.NewTracer()`.)

- [ ] **Step 5: Update `.env.example`**

Append to `.env.example`:

```
OTEL_ENABLED=false
OTEL_EXPORTER=stdout
# GOOGLE_CLOUD_PROJECT is auto-set on Cloud Run; no need to set locally
```

- [ ] **Step 6: Verify all tests still pass**

```bash
go build ./... && go test ./... -count=1
```

Expected: all tests pass. The `NewPool` signature change is backward-compatible (existing call site updated to pass `nil`).

- [ ] **Step 7: Commit**

```bash
git add -A && git commit -m "feat(phase4): add OTel deps, Otel config struct, tracer param to NewPool"
```

---

### Task 2: OTel initialization and lifecycle (`internal/drilotel/drilotel.go`)

**Files:**
- Create: `internal/drilotel/drilotel.go`
- Create: `internal/drilotel/drilotel_test.go`

- [ ] **Step 1: Write the tests**

Create `internal/drilotel/drilotel_test.go`:

```go
package drilotel_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

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

func TestInit_StdoutExporter(t *testing.T) {
	cfg := &config.Otel{
		Enabled:     true,
		Exporter:    "stdout",
		SampleRate:  1.0,
		ServiceName: "drill-test",
	}
	p, err := drilotel.Init(cfg)
	require.NoError(t, err)
	defer p.Shutdown(context.Background())

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

	// Create a span to verify the pipeline works end-to-end.
	tracer := otel.Tracer("roundtrip")
	ctx, span := tracer.Start(context.Background(), "roundtrip-span")
	_ = ctx
	span.End()

	// Shutdown should flush without error.
	err = p.Shutdown(context.Background())
	require.NoError(t, err)
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/drilotel/... -v -count=1
```

Expected: compilation failure — `drilotel` package does not exist yet.

- [ ] **Step 3: Implement `internal/drilotel/drilotel.go`**

```go
package drilotel

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"

	cloudmetric "github.com/GoogleCloudPlatform/opentelemetry-operations-go/exporter/metric"
	cloudtrace "github.com/GoogleCloudPlatform/opentelemetry-operations-go/exporter/trace"

	"github.com/btc/drill/internal/config"
)

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
		return nil, nil, fmt.Errorf("unknown otel exporter: %q (want \"stdout\" or \"google\")", name)
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/drilotel/... -v -count=1
```

Expected: all 4 tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/drilotel/drilotel.go internal/drilotel/drilotel_test.go && git commit -m "feat(phase4): add OTel init/shutdown with dual-mode exporters"
```

---

### Task 3: TraceHandler slog wrapper (`internal/drilotel/sloghandler.go`)

**Files:**
- Create: `internal/drilotel/sloghandler.go`
- Create: `internal/drilotel/sloghandler_test.go`

- [ ] **Step 1: Write the tests**

Create `internal/drilotel/sloghandler_test.go`:

```go
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
	t.Cleanup(func() { tp.Shutdown(context.Background()) })
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
	// Must NOT contain GCP fields.
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
	// Must NOT contain plain fields.
	assert.NotContains(t, out, "trace_id")
}

func TestTraceHandler_NoSpanContext(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, nil)
	handler := drilotel.NewTraceHandler(inner, "")

	logger := slog.New(handler)
	logger.Info("no context") // no ctx passed

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
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/drilotel/... -run TestTraceHandler -v -count=1
```

Expected: compilation failure — `NewTraceHandler` does not exist yet.

- [ ] **Step 3: Implement `internal/drilotel/sloghandler.go`**

```go
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
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/drilotel/... -run TestTraceHandler -v -count=1
```

Expected: all 4 TraceHandler tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/drilotel/sloghandler.go internal/drilotel/sloghandler_test.go && git commit -m "feat(phase4): add TraceHandler slog wrapper with GCP format support"
```

---

### Task 4: River job tracing middleware (`internal/drilotel/riverware.go`)

**Files:**
- Create: `internal/drilotel/riverware.go`
- Create: `internal/drilotel/riverware_test.go`

- [ ] **Step 1: Write the tests**

Create `internal/drilotel/riverware_test.go`:

```go
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
// exporter, so that otel.Tracer("drill/river") in JobTracer returns a real tracer.
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

	// Simulate metadata from SetTraceMetadata.
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
	jt := &drilotel.JobTracer{}
	job := &rivertype.JobRow{Kind: "bad_meta", Queue: "default", Attempt: 1, Metadata: []byte("garbage")}

	exp := setupGlobalTracer(t)
	err := jt.Work(context.Background(), job, func(ctx context.Context) error {
		return nil
	})
	require.NoError(t, err)

	spans := exp.GetSpans()
	require.Len(t, spans, 1)
	assert.Empty(t, spans[0].Links) // no link, no panic
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/drilotel/... -run "TestSetTrace|TestJobTracer|TestLinkFrom" -v -count=1
```

Expected: compilation failure — `SetTraceMetadata`, `JobTracer` do not exist yet.

- [ ] **Step 3: Implement `internal/drilotel/riverware.go`**

```go
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
	river.MiddlewareDefaults
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
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/drilotel/... -v -count=1
```

Expected: all tests pass (Task 2 + Task 3 + Task 4 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/drilotel/riverware.go internal/drilotel/riverware_test.go && git commit -m "feat(phase4): add River job tracing middleware with span links"
```

---

### Task 5: Wire OTel into main.go (logger, init, middleware chain)

**Files:**
- Modify: `cmd/drill/main.go`

- [ ] **Step 1: Update `main()` — remove logger setup, use fmt.Fprintf for fatal error**

In `cmd/drill/main.go`, replace the `main()` function:

```go
func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %s\n", err)
		os.Exit(1)
	}
}
```

Remove the `slog.New` / `slog.SetDefault` / `slog.Error` from `main()`. Add `"fmt"` to imports, remove the `"log/slog"` import if it's only used in `main()` (it's still used in `runWithContext`, so keep it).

- [ ] **Step 2: Add OTel init, logger setup, and middleware wiring to `runWithContext`**

In `runWithContext`, after config load and migrations, add OTel init and logger setup. Then wire the middleware chain.

The full updated `runWithContext` function:

```go
func runWithContext(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Structured logger with trace correlation (must be after config load for GCPProjectID).
	jsonHandler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	logger := slog.New(drilotel.NewTraceHandler(jsonHandler, cfg.Otel.GCPProjectID))
	slog.SetDefault(logger)

	if err := runMigrations(cfg.Database.URL); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	// OTel providers (must be before Backend so pool tracer is active).
	providers, err := drilotel.Init(&cfg.Otel)
	if err != nil {
		return fmt.Errorf("init otel: %w", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := providers.Shutdown(shutdownCtx); err != nil {
			slog.Warn("otel shutdown error", "error", err)
		}
	}()

	b, err := backend.New(cfg)
	if err != nil {
		return fmt.Errorf("create backend: %w", err)
	}
	defer b.Close()

	oauthStateKey := auth.DeriveKey(cfg.Auth.TokenSecret, "oauth-state")
	auth.SetupGothProviders(&cfg.OAuth, cfg.Auth.BaseURL, oauthStateKey)

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux, b)

	csrfKey := auth.DeriveKey(cfg.Auth.TokenSecret, "csrf")
	csrfMiddleware := csrf.Protect(
		csrfKey,
		csrf.Secure(cfg.Auth.SecureCookies()),
		csrf.HttpOnly(false),
		csrf.CookieName("drill_csrf"),
		csrf.Path("/"),
		csrf.SameSite(csrf.SameSiteLaxMode),
	)

	otelHandler := otelhttp.NewMiddleware("drill")(mux)
	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Server.Port),
		Handler: csrfMiddleware(otelHandler),
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("server starting", "port", cfg.Server.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		slog.Info("shutting down")
		shutdownTimeout := time.Duration(cfg.Server.ShutdownTimeoutSec) * time.Second
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer shutdownCancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			slog.Warn("http shutdown error", "error", err)
		}
		slog.Info("http server stopped")
		return nil
	}
}
```

Add these imports to `cmd/drill/main.go`:

```go
"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

"github.com/btc/drill/internal/drilotel"
```

- [ ] **Step 3: Verify it compiles**

```bash
go build ./cmd/drill/...
```

Expected: success.

- [ ] **Step 4: Run all tests**

```bash
go test ./... -count=1
```

Expected: all tests pass.

- [ ] **Step 5: Commit**

```bash
git add cmd/drill/main.go && git commit -m "feat(phase4): wire OTel init, TraceHandler, and otelhttp middleware in main"
```

---

### Task 6: SQL tracing and River middleware in Backend

**Files:**
- Modify: `internal/backend/backend.go`

- [ ] **Step 1: Add otelpgx tracer to pool creation and JobTracer to River config**

In `internal/backend/backend.go`, add imports:

```go
"github.com/exaring/otelpgx"
"github.com/riverqueue/river/rivertype"

"github.com/btc/drill/internal/drilotel"
```

Change the `NewPool` call (line 33) from:

```go
pool, err := cfg.Database.NewPool(context.Background(), nil)
```

to:

```go
pool, err := cfg.Database.NewPool(context.Background(), otelpgx.NewTracer())
```

Add the `Middleware` field to the River client config. Change the `river.NewClient` call from:

```go
riverClient, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
	Queues: map[string]river.QueueConfig{
		river.QueueDefault:      {MaxWorkers: cfg.River.NumDefaultWorkers},
		jobs.QueueNotifications: {MaxWorkers: cfg.River.NumNotifyWorkers},
		jobs.QueueAI:            {MaxWorkers: cfg.River.NumAIWorkers},
		jobs.QueueMaintenance:   {MaxWorkers: cfg.River.NumMaintWorkers},
	},
	Workers: workers,
})
```

to:

```go
riverClient, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
	Queues: map[string]river.QueueConfig{
		river.QueueDefault:      {MaxWorkers: cfg.River.NumDefaultWorkers},
		jobs.QueueNotifications: {MaxWorkers: cfg.River.NumNotifyWorkers},
		jobs.QueueAI:            {MaxWorkers: cfg.River.NumAIWorkers},
		jobs.QueueMaintenance:   {MaxWorkers: cfg.River.NumMaintWorkers},
	},
	Workers:    workers,
	Middleware: []rivertype.Middleware{&drilotel.JobTracer{}},
})
```

- [ ] **Step 2: Verify it compiles and all tests pass**

```bash
go build ./... && go test ./... -count=1
```

Expected: all tests pass. The otelpgx tracer produces no-op spans in tests (global provider is no-op). The River middleware is no-op without global provider init.

- [ ] **Step 3: Commit**

```bash
git add internal/backend/backend.go && git commit -m "feat(phase4): add SQL tracing (otelpgx) and River job tracing middleware"
```

---

### Task 7: user_id span enrichment in RequireAuth

**Files:**
- Modify: `internal/auth/middleware.go`

- [ ] **Step 1: Add span enrichment after successful auth**

In `internal/auth/middleware.go`, add imports:

```go
"go.opentelemetry.io/otel/attribute"
"go.opentelemetry.io/otel/trace"
```

Remove the unused `"github.com/google/uuid"` import (the `AuthUser` struct uses `uuid.UUID` — check if it's still needed; it is, so keep it).

In the `RequireAuth` function, just before the `next.ServeHTTP(w, r.WithContext(WithUser(r.Context(), user)))` line, add:

```go
			span := trace.SpanFromContext(r.Context())
			span.SetAttributes(attribute.String("user_id", user.ID.String()))
```

- [ ] **Step 2: Run existing auth tests to verify nothing breaks**

```bash
go test ./internal/auth/... -v -count=1
```

Expected: all existing tests pass. The `trace.SpanFromContext` call returns a no-op span when no OTel provider is initialized.

- [ ] **Step 3: Commit**

```bash
git add internal/auth/middleware.go && git commit -m "feat(phase4): add user_id span enrichment in RequireAuth"
```

---

### Task 8: Add SetTraceMetadata to enqueue call sites

**Files:**
- Modify: `internal/backend/auth.go`

- [ ] **Step 1: Add import and update both enqueue call sites**

In `internal/backend/auth.go`, add import:

```go
"github.com/btc/drill/internal/drilotel"
```

In the `Signup` method (around line 111), change from:

```go
	_, err = b.jobs.InsertTx(ctx, tx, jobs.SendEmailArgs{
		To:      user.Email,
		Subject: "Verify your Drill account",
		Text:    fmt.Sprintf("Click here to verify your email: %s", verifyURL),
		HTML:    fmt.Sprintf(`<p>Click <a href="%s">here</a> to verify your email.</p>`, verifyURL),
	}, jobs.SendEmailInsertOpts(&b.cfg.Email))
```

to:

```go
	emailOpts := jobs.SendEmailInsertOpts(&b.cfg.Email)
	drilotel.SetTraceMetadata(ctx, emailOpts)
	_, err = b.jobs.InsertTx(ctx, tx, jobs.SendEmailArgs{
		To:      user.Email,
		Subject: "Verify your Drill account",
		Text:    fmt.Sprintf("Click here to verify your email: %s", verifyURL),
		HTML:    fmt.Sprintf(`<p>Click <a href="%s">here</a> to verify your email.</p>`, verifyURL),
	}, emailOpts)
```

In the `ForgotPassword` method (around line 239), change from:

```go
	_, err = b.jobs.Insert(ctx, jobs.SendEmailArgs{
		To:      user.Email,
		Subject: "Reset your Drill password",
		Text:    "Click here to reset your password: " + resetURL,
		HTML:    "<p>Click <a href=\"" + resetURL + "\">here</a> to reset your password.</p>",
	}, jobs.SendEmailInsertOpts(&b.cfg.Email))
```

to:

```go
	resetEmailOpts := jobs.SendEmailInsertOpts(&b.cfg.Email)
	drilotel.SetTraceMetadata(ctx, resetEmailOpts)
	_, err = b.jobs.Insert(ctx, jobs.SendEmailArgs{
		To:      user.Email,
		Subject: "Reset your Drill password",
		Text:    "Click here to reset your password: " + resetURL,
		HTML:    "<p>Click <a href=\"" + resetURL + "\">here</a> to reset your password.</p>",
	}, resetEmailOpts)
```

- [ ] **Step 2: Run all tests to verify nothing breaks**

```bash
go build ./... && go test ./... -count=1
```

Expected: all tests pass.

- [ ] **Step 3: Commit**

```bash
git add internal/backend/auth.go && git commit -m "feat(phase4): add trace metadata to email job enqueue call sites"
```

---

### Task 9: Final verification

- [ ] **Step 1: Run full test suite**

```bash
go test ./... -count=1 -race
```

Expected: all tests pass with race detector enabled.

- [ ] **Step 2: Build the binary**

```bash
go build -o /dev/null ./cmd/drill/...
```

Expected: clean build.

- [ ] **Step 3: Smoke test with OTel enabled**

```bash
OTEL_ENABLED=true OTEL_EXPORTER=stdout DATABASE_URL=postgres://localhost:5432/drill ANTHROPIC_API_KEY=test OPENAI_API_KEY=test AUTH_TOKEN_SECRET=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa go run ./cmd/drill/ &
sleep 2
curl -s http://localhost:8080/api/health
kill %1
```

Expected: Health check responds 200. Stdout shows OTel trace/metric JSON output. Stderr shows structured JSON log lines. Trace output includes a span for the `GET /api/health` request with a child `db.query` span from the health check's DB ping.

- [ ] **Step 4: Commit plan completion marker**

No code change — this step confirms the phase is complete.
