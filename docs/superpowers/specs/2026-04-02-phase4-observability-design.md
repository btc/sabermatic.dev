# Phase 4: Observability — Design Spec

**Date**: 2026-04-02
**Status**: Draft
**Depends on**: Phase 3b (OAuth + CSRF) — merged to main
**Purpose**: Add OpenTelemetry tracing, structured log correlation, SQL query tracing, River job instrumentation, and configurable exporters so that every subsequent phase is instrumented from day one.

---

## 1. Overview

Phases 1–3b built the application core: database, job queue, auth, OAuth. None of it is observable beyond ad-hoc `slog` calls. Phase 4 lays the observability foundation so that every HTTP request, SQL query, and River job produces correlated traces and logs.

This phase delivers:

1. **OTel SDK initialization** — tracer and meter providers with dual-mode exporters (stdout for dev, Google Cloud for production)
2. **HTTP middleware** — automatic span creation for every request via `otelhttp`, plus `user_id` span enrichment in `RequireAuth`
3. **SQL tracing** — every pgx query appears as a child span via `otelpgx`
4. **Structured log correlation** — custom `slog.Handler` wrapper that injects `trace_id`/`span_id` into every log line
5. **River job tracing** — span-per-job execution via `WorkerMiddleware`, with span links connecting jobs to the traces that enqueued them
6. **Configuration** — `config.Otel` struct controlling enablement, exporter selection, sampling rate, and service name

Systems that don't exist yet (Conductor, LLM client, frontend) are not instrumented here. When those phases are built, they inherit the tracing infrastructure — they just need to propagate `context.Context` (which is already the project convention) and their operations appear in traces automatically.

---

## 2. Dependencies

| Library | Purpose |
|---|---|
| `go.opentelemetry.io/otel/sdk` | TracerProvider and MeterProvider setup |
| `go.opentelemetry.io/otel/sdk/metric` | MeterProvider with PeriodicReader |
| `go.opentelemetry.io/otel/exporters/stdout/stdouttrace` | Dev trace exporter (JSON to stdout) |
| `go.opentelemetry.io/otel/exporters/stdout/stdoutmetric` | Dev metric exporter (JSON to stdout) |
| `github.com/GoogleCloudPlatform/opentelemetry-operations-go/exporter/trace` | Google Cloud Trace exporter |
| `github.com/GoogleCloudPlatform/opentelemetry-operations-go/exporter/metric` | Google Cloud Monitoring exporter |
| `go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp` | HTTP middleware (already indirect dep, promoted to direct) |
| `github.com/exaring/otelpgx` | pgx v5 QueryTracer for SQL span creation |

`otelhttp` is already an indirect dependency (v0.61.0) via testcontainers. The OTel core packages (`otel`, `otel/trace`, `otel/metric`) are also indirect deps. This phase promotes them to direct. The Google Cloud exporter packages are separate modules under `opentelemetry-operations-go` — one for trace (v1.31.0), one for metric (v0.55.0).

**Version alignment:** The current `go.mod` has `otel/sdk/metric v1.39.0` as an indirect dep while `otel v1.41.0` is also indirect. When promoting to direct deps, all OTel SDK packages (`otel/sdk`, `otel/sdk/metric`) must be upgraded to the same minor version to avoid skew. Run `go get go.opentelemetry.io/otel/sdk@latest go.opentelemetry.io/otel/sdk/metric@latest` to align them.

---

## 3. Configuration

New `Otel` struct added to `config.Config`:

```go
type Otel struct {
    Enabled      bool    `env:"OTEL_ENABLED,default=false"`
    Exporter     string  `env:"OTEL_EXPORTER,default=stdout"`     // "stdout" | "google"
    SampleRate   float64 `env:"OTEL_SAMPLE_RATE,default=1.0"`     // 1.0 = trace everything
    ServiceName  string  `env:"OTEL_SERVICE_NAME,default=drill"`
    GCPProjectID string  `env:"GOOGLE_CLOUD_PROJECT"`             // auto-set on Cloud Run
}
```

| Field | Default | Notes |
|---|---|---|
| `Enabled` | `false` | Tests and plain `go run` produce no spans. Production sets `true`. |
| `Exporter` | `stdout` | `"stdout"` for dev (JSON to stderr), `"google"` for Cloud Run. |
| `SampleRate` | `1.0` | At launch scale (< 500 sessions), 100% sampling is fine. Tunable later. |
| `ServiceName` | `drill` | OTel resource attribute `service.name`. |
| `GCPProjectID` | `""` | Cloud Run sets `GOOGLE_CLOUD_PROJECT` automatically. When non-empty, `TraceHandler` outputs Cloud Logging-compatible trace fields (see Section 6). |

Validation: when `Enabled=true` and `Exporter=google`, no additional configuration is needed — the Google Cloud exporter auto-detects project ID and credentials from the Cloud Run environment. When running locally with `Exporter=google`, standard `GOOGLE_APPLICATION_CREDENTIALS` or `gcloud auth application-default login` must be set (standard GCP developer workflow, not our config concern).

Added to `.env.example`:

```
OTEL_ENABLED=false
OTEL_EXPORTER=stdout
# GOOGLE_CLOUD_PROJECT is auto-set on Cloud Run; no need to set locally
```

---

## 4. OTel Initialization & Lifecycle

`internal/drilotel/otel.go` — owns provider creation and shutdown.

### 4.1 Providers Struct

```go
type Providers struct {
    TracerProvider *sdktrace.TracerProvider
    MeterProvider  *sdkmetric.MeterProvider
    Shutdown       func(ctx context.Context) error
}
```

### 4.2 Init Flow

`Init(cfg *config.Otel) (*Providers, error)`:

1. If `!cfg.Enabled`, return a no-op `Providers` with a no-op `Shutdown`. Global providers remain OTel's built-in no-ops. All downstream code (otelhttp, otelpgx, River middleware) reads from the global provider — no-op means zero overhead.

2. Build the OTel `resource.Resource` with `service.name=cfg.ServiceName` and `service.version` (from build info or `"dev"`).

3. Select exporter based on `cfg.Exporter`:
   - `"stdout"`: `stdouttrace.New(stdouttrace.WithWriter(os.Stderr))` and `stdoutmetric.New(stdoutmetric.WithWriter(os.Stderr))` — explicitly write to stderr (the default is stdout, which would mix with application JSON logs).
   - `"google"`: `cloudtrace.New()` from `github.com/GoogleCloudPlatform/opentelemetry-operations-go/exporter/trace` and `cloudmetric.New()` from `.../exporter/metric`. Both auto-detect project ID and credentials from the environment.
   - Anything else: return an error.

4. Create `TracerProvider`:
   - `sdktrace.WithBatcher(traceExporter)` — batches spans before export.
   - `sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.SampleRate)))` — `ParentBased` respects the remote parent's sampled flag when an inbound `traceparent` header is present (preserving distributed trace completeness); delegates to the inner `TraceIDRatioBased` sampler for root spans with no parent.
   - `sdktrace.WithResource(resource)`.

5. Create `MeterProvider`:
   - `sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter, sdkmetric.WithInterval(60*time.Second)))`.
   - `sdkmetric.WithResource(resource)`.

6. Register globals:
   - `otel.SetTracerProvider(tp)`
   - `otel.SetMeterProvider(mp)`
   - `otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))` — enables W3C `traceparent` header propagation for distributed tracing.

7. Return `Providers` with `Shutdown` that calls `tp.Shutdown(ctx)` and `mp.Shutdown(ctx)`, flushing any buffered data.

**Partial failure cleanup:** If any step after exporter creation fails (e.g., `MeterProvider` creation fails after `TracerProvider` was created), `Init` must shut down already-created resources before returning the error. Use a cleanup slice or defer pattern: each successfully created resource registers a cleanup func; on error, run all cleanups in reverse order. This prevents orphaned providers and leaked goroutines (the batcher and periodic reader both start background goroutines).

### 4.3 main.go Integration

```go
// After config load, before backend creation:
providers, err := drilotel.Init(&cfg.Otel)
if err != nil {
    return fmt.Errorf("init otel: %w", err)
}
defer func() {
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    providers.Shutdown(ctx)
}()

// Backend.New(cfg) — pool now has otelpgx tracer active
b, err := backend.New(cfg)
```

Lifecycle order:
1. Config load
2. Migrations
3. **OTel init** ← new
4. Backend (pool + River)
5. HTTP server
6. Shutdown: HTTP → Backend (River + pool) → **OTel flush** ← new

OTel shuts down last so that spans from Backend.Close() (River draining, pool close) are still captured.

---

## 5. SQL Tracing

`otelpgx` implements pgx v5's `pgx.QueryTracer` interface. Adding it to the pool config is a one-line change in `config.Database.NewPool()`:

```go
import "github.com/exaring/otelpgx"

func (d *Database) NewPool(ctx context.Context) (*pgxpool.Pool, error) {
    poolCfg, err := pgxpool.ParseConfig(d.URL)
    if err != nil {
        return nil, fmt.Errorf("parse database url: %w", err)
    }
    poolCfg.MaxConns = d.MaxPoolConns
    poolCfg.ConnConfig.Tracer = otelpgx.NewTracer()
    // ... rest unchanged
}
```

### What appears in traces

- **Span per query**: name `db.query`, attributes `db.statement` (the SQL text), `db.system=postgresql`.
- **Connection events**: `db.connect` when a new connection is established in the pool.
- **Errors**: query failures surface as span status `Error` with the error message as a span event.
- **Nesting**: SQL spans are children of whatever span is active in the `context.Context` passed to the query. An HTTP handler span → sqlc query → `db.query` span — all linked.

### What is excluded

**SQL parameter values are not included.** `otelpgx` supports `otelpgx.WithIncludeQueryParameters()` but this risks leaking PII (emails, tokens, secrets) into Cloud Trace. The SQL statement text is sufficient for debugging. If parameter tracing is ever needed for a specific investigation, it can be enabled temporarily via a config flag — but that's not in scope here.

### No-op behavior

When OTel is disabled, `otelpgx.NewTracer()` reads from the global no-op `TracerProvider`. It creates no-op spans. The overhead is a few no-op function calls per query — negligible. This is safe to add unconditionally.

---

## 6. Structured Logging with Trace Context

`internal/drilotel/sloghandler.go` — a `slog.Handler` wrapper that enriches every log record with trace correlation fields.

### 6.1 TraceHandler

```go
type TraceHandler struct {
    inner        slog.Handler
    gcpProjectID string // when non-empty, use Cloud Logging trace format
}

func NewTraceHandler(inner slog.Handler, gcpProjectID string) *TraceHandler {
    return &TraceHandler{inner: inner, gcpProjectID: gcpProjectID}
}
```

Implements all four `slog.Handler` methods:

- **`Enabled`**: delegates to inner.
- **`Handle`**: extracts `trace.SpanFromContext(ctx).SpanContext()`. If `IsValid()`, adds trace correlation fields (format depends on `gcpProjectID` — see below), then delegates to inner. If not valid (no active span), delegates unchanged.
- **`WithAttrs`**: returns new `TraceHandler` wrapping `inner.WithAttrs(...)`, preserving `gcpProjectID`.
- **`WithGroup`**: returns new `TraceHandler` wrapping `inner.WithGroup(...)`, preserving `gcpProjectID`.

### 6.2 Log Output — Dual Format

Cloud Logging does **not** auto-correlate from a plain `trace_id` field. It requires specific field names and a project-qualified trace resource name. The `TraceHandler` switches format based on whether `gcpProjectID` is set.

**Local dev** (`gcpProjectID` is empty) — plain fields for human readability:

```json
{
  "time": "2026-04-02T14:30:00.000Z",
  "level": "INFO",
  "msg": "oauth login",
  "provider": "google",
  "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736",
  "span_id": "00f067aa0ba902b7"
}
```

**Cloud Run** (`gcpProjectID` is set, e.g. `"my-project"`) — Cloud Logging format for automatic trace correlation:

```json
{
  "time": "2026-04-02T14:30:00.000Z",
  "level": "INFO",
  "msg": "oauth login",
  "provider": "google",
  "logging.googleapis.com/trace": "projects/my-project/traces/4bf92f3577b34da6a3ce929d0e0e4736",
  "logging.googleapis.com/spanId": "00f067aa0ba902b7"
}
```

This enables clicking from a Cloud Trace span to its correlated log entries and vice versa. The `GOOGLE_CLOUD_PROJECT` env var is auto-set on Cloud Run — no manual config needed.

### 6.3 Application-Level Fields

`user_id` and `session_id` are not in the OTel span context — they come from application code. The handler does NOT attempt to inject these. They continue to be passed as explicit `slog` attributes at call sites:

```go
slog.InfoContext(ctx, "signup completed", "user_id", userID)
```

Existing `slog.Info` calls that don't pass a context still work — they just won't have trace correlation. No forced migration of existing call sites; new code should prefer `slog.InfoContext`.

### 6.4 main.go Change

```go
jsonHandler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
    Level: slog.LevelInfo,
})
logger := slog.New(drilotel.NewTraceHandler(jsonHandler, cfg.Otel.GCPProjectID))
slog.SetDefault(logger)
```

Note: JSON logs move to stderr. This separates application logs from stdout trace output (when using stdout exporter in dev). In production on Cloud Run, Cloud Logging captures both stdout and stderr.

**Existing logger relocation:** The current `main()` creates a JSON logger to stdout before calling `run()`. This setup must be moved into `run()` after config load (so `GCPProjectID` is available for the `TraceHandler`). The `main()` function should have no logger setup — if `run()` fails before logger init (e.g., config load error), the error is returned to `main()` and printed via a simple `fmt.Fprintf(os.Stderr, ...)` or a bare `slog.Error` using the default text logger.

### 6.5 No-Op Behavior

When OTel is disabled, `SpanFromContext` returns a no-op span whose `SpanContext().IsValid()` returns false. The handler skips adding trace fields. Logs look identical to today's output.

---

## 7. HTTP Middleware

### 7.1 otelhttp Wrapper

`otelhttp.NewMiddleware("drill")` — the standard OTel HTTP middleware. Creates a server span per request with:

- `http.method`, `http.route`, `http.status_code`, `url.path`
- Request duration
- W3C `traceparent` header extraction (inbound distributed trace propagation)

### 7.2 User Attribute Injection — Inside RequireAuth

A standalone `InjectUser` middleware in the outer chain would run **before** route dispatch, which means **before** `RequireAuth` has authenticated the user. The user would never be in the context at that point.

Instead, `user_id` enrichment is added directly to `auth.RequireAuth`. After successful authentication, before calling `next.ServeHTTP`, it sets the `user_id` attribute on the current span:

```go
// In auth.RequireAuth, after building the AuthUser:
import "go.opentelemetry.io/otel/trace"
import "go.opentelemetry.io/otel/attribute"

span := trace.SpanFromContext(r.Context())
span.SetAttributes(attribute.String("user_id", user.ID.String()))

next.ServeHTTP(w, r.WithContext(WithUser(r.Context(), user)))
```

This works because:
- The `otelhttp` middleware (outer chain) has already created a span and injected it into the request context before the mux dispatches to routes.
- `RequireAuth` runs after mux dispatch, so the span is active.
- `trace.SpanFromContext` and `span.SetAttributes` are from the OTel API package (`go.opentelemetry.io/otel/trace`), which is a lightweight, SDK-independent package. When OTel is disabled, the global provider is no-op, so `SpanFromContext` returns a no-op span and `SetAttributes` is free. No SDK dependency in the auth package.
- Unauthenticated routes (health, login, signup) never call `RequireAuth`, so their spans have no `user_id` — correct behavior.

### 7.3 Middleware Chain in main.go

```go
mux := http.NewServeMux()
handler.RegisterRoutes(mux, b)

otelHandler := otelhttp.NewMiddleware("drill")(mux)
csrfHandler := csrfMiddleware(otelHandler)

srv := &http.Server{
    Addr:    fmt.Sprintf(":%d", cfg.Server.Port),
    Handler: csrfHandler,
}
```

Execution order (outside-in): CSRF → OTel span creation → mux → (RequireAuth on protected routes, which enriches span with user_id) → handler.

- CSRF is outermost: protects everything. CSRF rejections of state-changing requests (POST/PUT/DELETE with invalid tokens) are rejected before a span is created. This means those failures won't appear in traces — an acceptable tradeoff since they're either attacks or misconfigured clients, and the CSRF middleware already logs them. Safe methods (GET) always pass through CSRF and do generate traces.
- OTel is next: creates the span that all downstream code (including `RequireAuth`) reads from context.

### 7.4 No-Op Behavior

When OTel is disabled, `otelhttp.NewMiddleware` wraps the handler but creates no-op spans (reads from global no-op provider). The `trace.SpanFromContext` call in `RequireAuth` returns a no-op span — `SetAttributes` is a no-op. Negligible overhead.

---

## 8. River Job Tracing

`internal/drilotel/riverware.go` — a River `WorkerMiddleware` that creates a span for every job execution and links it to the trace that enqueued the job.

### 8.1 Enqueue Side — Capturing Trace Context

A helper that merges trace metadata into an existing `*river.InsertOpts`, preserving all other fields (queue, max attempts, and any existing metadata):

```go
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
    // Merge into existing metadata JSON object: parse existing, add trace fields, re-marshal.
    var existing map[string]json.RawMessage
    if err := json.Unmarshal(opts.Metadata, &existing); err != nil {
        opts.Metadata = traceJSON // existing metadata is invalid JSON; replace
        return
    }
    existing["trace_id"] = json.RawMessage(fmt.Sprintf(`"%s"`, sc.TraceID()))
    existing["span_id"] = json.RawMessage(fmt.Sprintf(`"%s"`, sc.SpanID()))
    merged, _ := json.Marshal(existing)
    opts.Metadata = merged
}
```

This preserves any metadata that River or other code may have set. River's documentation warns against overwriting metadata it stores. Call sites add one line after building their insert opts. This is opt-in — existing enqueue calls without `SetTraceMetadata` still work; their jobs just won't have a span link. When there's no active span (tests, disabled OTel), it's a no-op.

### 8.2 Execute Side — WorkerMiddleware

```go
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
```

### 8.3 linkFromMetadata

Parses `{"trace_id":"...","span_id":"..."}` from `job.Metadata`, reconstructs a remote `SpanContext`, and returns a `trace.Link`. Returns `false` if metadata is empty, missing the fields, or unparseable — no error, just no link.

### 8.4 Registration

Added to the River client config in `backend.New()`:

```go
riverClient, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
    Queues:     queues,
    Workers:    workers,
    Middleware: []rivertype.Middleware{&drilotel.JobTracer{}},
})
```

`Middleware` is the non-deprecated field (River v0.32). `JobTracer` embeds `river.MiddlewareDefaults` and implements `rivertype.WorkerMiddleware`, which satisfies the `rivertype.Middleware` interface.

### 8.5 What Shows Up in Cloud Trace

- Each job execution is its own trace with root span `river.job/SendEmailArgs`.
- The span has attributes: `river.job_kind`, `river.job_id`, `river.queue`, `river.attempt`.
- If the job was enqueued from an HTTP request that used `JobInsertOpts`, the span has a link to the original request's span — clickable in Cloud Trace.
- SQL queries inside the job appear as child spans (via otelpgx).
- Errors and retries are visible via span status and attempt number.

### 8.6 Existing Enqueue Call Sites

The existing `SendEmail` job enqueue call sites in `backend/auth.go` will be updated to add trace metadata. There are two patterns:

**`Insert` (e.g., `ForgotPassword`):**

```go
// Before:
b.jobs.Insert(ctx, jobs.SendEmailArgs{...}, jobs.SendEmailInsertOpts(&b.cfg.Email))

// After:
opts := jobs.SendEmailInsertOpts(&b.cfg.Email)
drilotel.SetTraceMetadata(ctx, opts)
b.jobs.Insert(ctx, jobs.SendEmailArgs{...}, opts)
```

**`InsertTx` (e.g., `Signup`):**

```go
// Before:
b.jobs.InsertTx(ctx, tx, jobs.SendEmailArgs{...}, jobs.SendEmailInsertOpts(&b.cfg.Email))

// After:
opts := jobs.SendEmailInsertOpts(&b.cfg.Email)
drilotel.SetTraceMetadata(ctx, opts)
b.jobs.InsertTx(ctx, tx, jobs.SendEmailArgs{...}, opts)
```

One extra line per call site. Queue, max attempts, and all other insert opts are preserved. Future enqueue calls follow the same pattern.

---

## 9. File Layout

```
internal/
├── drilotel/
│   ├── drilotel.go          # Init() / Shutdown(), Providers struct, exporter selection
│   ├── drilotel_test.go     # Init roundtrip, disabled mode, stdout mode
│   ├── sloghandler.go       # TraceHandler — slog.Handler wrapper with GCP format support
│   ├── sloghandler_test.go  # Trace context injection, GCP format, no-op when invalid
│   ├── riverware.go         # JobTracer middleware, SetTraceMetadata, linkFromMetadata
│   └── riverware_test.go    # Span creation, span links, error recording
├── auth/
│   └── middleware.go        # Modified: add user_id span enrichment in RequireAuth
├── config/
│   └── config.go            # Modified: add Otel struct
├── backend/
│   ├── backend.go           # Modified: add Middleware to River config
│   └── auth.go              # Modified: add SetTraceMetadata to enqueue call sites
cmd/drill/
└── main.go                  # Modified: OTel init/shutdown, slog handler, middleware chain
```

---

## 10. Testing Strategy

### 10.1 Unit Tests — `internal/drilotel/`

All tests use an in-memory `TracerProvider` with `sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))` where `exporter` is `tracetest.NewInMemoryExporter()`. This captures spans without any network calls.

**sloghandler_test.go:**

| Case | Setup | Expected |
|---|---|---|
| Log with active span (local) | Create span, gcpProjectID="" | Output JSON contains `trace_id` and `span_id` (plain format) |
| Log with active span (GCP) | Create span, gcpProjectID="my-project" | Output JSON contains `logging.googleapis.com/trace` as `projects/my-project/traces/{id}` and `logging.googleapis.com/spanId` |
| Log without span context | Pass background ctx | Output JSON has no trace fields |
| WithAttrs preserves wrapping | Call WithAttrs, then Handle with span | Both custom attrs and trace fields present |
| WithGroup preserves wrapping | Call WithGroup("g"), add attr "x" via WithAttrs, then Handle with span | Trace fields AND custom attr "x" all appear inside group "g" in JSON output. This is correct slog behavior: `WithGroup` causes the inner handler to nest ALL record attrs (including those added by `record.AddAttrs` in the wrapper's `Handle`) inside the group. |

**Note on WithGroup and Cloud Logging:** When `WithGroup` is used with a GCP-configured `TraceHandler`, the `logging.googleapis.com/trace` field will be nested inside the group, which breaks Cloud Logging auto-correlation. This is a known limitation. In practice, `WithGroup` is not used in this codebase — the default logger is a flat JSON logger. If grouped logging is ever needed alongside GCP trace correlation, the handler would need a more complex approach (e.g., pre-handler attr injection). Not in scope.

**riverware_test.go:**

| Case | Setup | Expected |
|---|---|---|
| SetTraceMetadata with active span, no existing metadata | Create span, pass InsertOpts with nil Metadata | Opts.Metadata contains trace_id/span_id JSON, other fields unchanged |
| SetTraceMetadata with active span, existing metadata | Create span, pass InsertOpts with `{"foo":"bar"}` Metadata | Opts.Metadata contains foo, trace_id, and span_id (merged) |
| SetTraceMetadata without span | Background ctx, pass existing InsertOpts | Opts unchanged (Metadata stays nil/empty) |
| SetTraceMetadata preserves InsertOpts fields | InsertOpts with Queue and MaxAttempts set | Queue and MaxAttempts still set after call, Metadata added |
| SetTraceMetadata with invalid existing metadata | Opts.Metadata is `not-json` | Replaced with trace-only JSON (graceful fallback) |
| JobTracer.Work creates span | In-memory exporter, call Work | Exported span named `river.job/{kind}` with correct attributes |
| JobTracer.Work with metadata link | Job metadata has trace_id/span_id | Exported span has link to that span context |
| JobTracer.Work records error | doInner returns error | Span status is Error, error event recorded |
| linkFromMetadata bad JSON | Garbage metadata | Returns false, no panic |

### 10.2 Integration Test — `internal/drilotel/`

**otel_test.go:**

| Case | Setup | Expected |
|---|---|---|
| Init with Enabled=false | Call Init | Returns no-op Providers, Shutdown is no-op |
| Init with stdout exporter | Enabled=true, Exporter=stdout | TracerProvider and MeterProvider created, span export works |
| Init with invalid exporter | Exporter="bogus" | Returns error |
| Full roundtrip | Init → create span → Shutdown | Span appears in exporter output, Shutdown flushes cleanly |

### 10.3 Existing Test Impact

- **testcontainers integration tests**: `otelpgx` is added to the pool unconditionally, but OTel is not initialized in tests (global provider is no-op). Zero spans produced, zero overhead. No test changes needed.
- **Handler tests**: Tests that call `handler.RegisterRoutes` directly don't go through the otelhttp middleware chain. No changes needed.
- **Auth middleware tests**: The `RequireAuth` change adds `trace.SpanFromContext` + `SetAttributes` calls. With no OTel init, these are no-ops. Existing auth tests continue to pass without modification.
- **River worker tests**: The `Middleware` is added to the River config in `backend.New()`. Tests that construct `Backend` manually (without `New`) don't have the middleware. Tests that use `New` get it, but it's no-op without OTel init.

### 10.4 What Is NOT Tested

- Google Cloud exporter connectivity — requires GCP credentials and project. Verified at deployment time.
- Cloud Trace rendering — GCP's responsibility.
- Metric export intervals — testing timers is fragile and low-value.

---

## 11. What This Does NOT Include

- **WebSocket trace propagation** — deferred to Phase 5 (Conductor). The Conductor will read trace context from the `session_init` WebSocket message and use it as the parent span.
- **LLM call instrumentation** — deferred to Phase 5. The Anthropic API client wrapper will record to `llm_calls`/`llm_call_content` tables and emit spans with token counts and latency.
- **Frontend OTel JS SDK** — deferred to Phase 9 (Polish + UI). The frontend will generate `traceparent` headers on HTTP requests for end-to-end tracing.
- **Admin dashboard** — deferred to Phase 10 (Admin). Queries `llm_calls` for cost trends, error rates, latency distributions.
- **Alert thresholds** — deferred to the Deployment phase or later. Cloud Monitoring alerting policies are infrastructure config, not application code.
- **Per-queue River metrics** (queue depth, job duration histograms) — evaluate after seeing River's built-in UI. May add in a later phase if gaps exist.
- **Custom business metrics** (`llm_call_duration_seconds`, `active_sessions_total`, etc.) — deferred to the phases that create the measured systems. The MeterProvider is ready; creating meters is trivial once the systems exist.
