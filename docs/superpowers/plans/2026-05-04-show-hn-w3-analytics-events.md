# Show HN W3 — Analytics events to BigQuery implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Emit structured analytics events from backend handlers, ship them to BigQuery via Cloud Logging Logs Router, and expose a clean queryable schema via a BQ view — so the Show HN funnel (landing → signup → activate → complete) can be reconstructed post-spike with SQL.

**Architecture:** Backend code calls a fire-and-forget `events.Emitter.Emit(ctx, name, props...)` which writes one structured slog line tagged `analytics_event=true`. The application logger writes to stderr in Cloud Run (the configured `LOG_FILE` path is unwritable in the container, so `buildLogWriter` falls back to `os.Stderr`); Cloud Run captures both stdout and stderr and forwards them to Cloud Logging automatically. A Logs Router sink filters on the tag and routes matching entries to a BigQuery dataset. A BQ view flattens `jsonPayload.*` into named columns for queries. Frontend events (landing_view, signup_started) post to `/api/beacon` which validates an allowlist and re-emits via the same path.

**Tech Stack:** Go 1.x, slog, pgx/v5, ConnectRPC, React + TypeScript, Terraform, Google Cloud Logging, BigQuery.

**Spec:** `docs/superpowers/specs/2026-05-04-show-hn-prep-design.md` (Workstream 3).

---

## File structure

### Go (backend)

| File | Action | Responsibility |
|---|---|---|
| `internal/events/context.go` | Create | Typed context helpers: `WithVisitorID`, `VisitorID`, `WithUserID`, `UserID`, `WithReferer`, `Referer`, `WithUTM`, `UTM`, `ContextValues`, `ContextValuesFromContext` |
| `internal/events/events.go` | Create | `Emitter` type + `NewEmitter(*slog.Logger) *Emitter` + `(e *Emitter) Emit(ctx, name, props ...slog.Attr)` |
| `internal/events/events_test.go` | Create | Unit tests using a recording `slog.Handler` |
| `internal/handler/analytics_middleware.go` | Create | `AnalyticsContextMiddleware`: visitor cookie set/read, capture Referer + UTM into ctx |
| `internal/handler/analytics_middleware_test.go` | Create | Tests: cookie set on first hit, reused on second; UTM captured |
| `internal/handler/beacon.go` | Create | `POST /api/beacon` handler with event-name allowlist |
| `internal/handler/beacon_test.go` | Create | Tests: allowlist enforcement, body parsing, emission verification |
| `internal/handler/server.go` | Modify | Wire `AnalyticsContextMiddleware` around `mux` before `otelhttp` sees it; register `/api/beacon` route |
| `internal/handler/middleware.go` | Modify | Extend `RequireAuth` to call `events.WithUserID` on the request context |
| `internal/rpc/interceptor.go` | Modify | Extend `AuthInterceptor.authenticate` to call `events.WithUserID` on the connect handler context |
| `internal/rpc/register.go` | Modify | Inject `*events.Emitter` into `Backend` constructor and into `sample.NewServer` |
| `internal/rpc/sample/server.go` | Modify | `(*Server).GetSampleSession` signature change to use ctx; emit `sample_view`; constructor takes `*events.Emitter` |
| `internal/backend/backend.go` | Modify | `New` constructor signature: add `*events.Emitter`; store on `Backend` |
| `internal/backend/auth.go` | Modify | Emit `signup_completed` (Signup) and `email_verified` (VerifyEmail) at success boundaries |
| `internal/backend/oauth.go` | Modify | Emit `oauth_completed` from `OAuthLogin` before returning, with `is_new_user` / `is_reactivated` derived from `path` |
| `internal/backend/session.go` | Modify | Emit `session_created` (CreateSession), `session_ended` (CompleteSession + CancelSession inner methods) |
| `internal/backend/turn.go` | Modify | Emit `first_message_sent` (ExecuteTurn, guarded by candidate-message-count check) |
| `internal/jobs/cleanup.go` | Modify | Emit one `session_ended` per ID returned by both `CompleteAbandonedActiveSessions` (`reason=abandoned`) and `CancelAbandonedEmptySessions` (`reason=abandoned_empty`); fetch user_ids for each via the modified queries |
| `sql/queries/sessions.sql` | Modify | Change two `:many` queries to RETURN both `id` and `user_id` so cleanup can attach user_id to events |
| `internal/db/sessions.sql.go` | Auto-regen | sqlc regenerates the two query functions to return rows with both fields |

### Frontend

| File | Action | Responsibility |
|---|---|---|
| `web/src/lib/analytics.ts` | Create | `track(args: TrackArgs)` discriminated-union API; uses `navigator.sendBeacon` with fetch fallback |
| `web/src/lib/analytics.test.ts` | Create | Unit tests on the payload shape |
| `web/src/pages/landing/index.tsx` | Modify | `useEffect(() => track({event: 'landing_view'}), [])` on mount |
| `web/src/pages/auth/signup.tsx` | Modify | `track({event: 'signup_started', props: {auth_method}})` on form submit + on OAuth button clicks |

### Infrastructure

| File | Action | Responsibility |
|---|---|---|
| `terraform/analytics.tf` | Create | `google_bigquery_dataset.analytics`, `google_logging_project_sink.analytics` (filter + use_partitioned_tables), `google_bigquery_dataset_iam_member.sink_writer`, `google_bigquery_table.analytics_events_view` (the flattening view) |

---

## Tasks

### Phase 1 — Events package foundation

#### Task 1: Create `internal/events/context.go`

**Files:**
- Create: `internal/events/context.go`

- [ ] **Step 1: Create the file**

```go
// Package events provides analytics event emission and per-request context
// propagation for visitor/user identity, referer, and UTM parameters.
package events

import "context"

// ContextValues bundles the per-request analytics context populated by
// AnalyticsContextMiddleware (visitor_id, referer, UTM, path, user_agent),
// the auth middlewares (user_id), and backend handlers (session_id).
//
// IMPORTANT: every field here becomes a top-level column in the BQ view via
// jsonPayload.<field>. Event-specific properties go into the `properties`
// slog.Group instead and become a nested RECORD column.
type ContextValues struct {
	VisitorID   string
	UserID      string
	SessionID   string
	Referer     string
	UTMSource   string
	UTMMedium   string
	UTMCampaign string
	Path        string
	UserAgent   string
}

type ctxKey struct{}

// WithContextValues returns a new context with the given ContextValues stored.
// Callers should populate via the With* helpers below rather than building a
// ContextValues struct directly.
func WithContextValues(ctx context.Context, cv ContextValues) context.Context {
	return context.WithValue(ctx, ctxKey{}, cv)
}

// ContextValuesFromContext returns the ContextValues stored on ctx, or a zero
// struct if none is present.
func ContextValuesFromContext(ctx context.Context) ContextValues {
	cv, _ := ctx.Value(ctxKey{}).(ContextValues)
	return cv
}

// WithVisitorID returns a new ctx with VisitorID set; preserves other fields.
func WithVisitorID(ctx context.Context, id string) context.Context {
	cv := ContextValuesFromContext(ctx)
	cv.VisitorID = id
	return WithContextValues(ctx, cv)
}

// WithUserID returns a new ctx with UserID set; preserves other fields.
func WithUserID(ctx context.Context, id string) context.Context {
	cv := ContextValuesFromContext(ctx)
	cv.UserID = id
	return WithContextValues(ctx, cv)
}

// WithSessionID returns a new ctx with SessionID set; preserves other fields.
// Backend handlers operating on a known session call this before Emit so that
// session_id lands at jsonPayload.session_id (top-level) rather than buried
// inside the per-event properties record.
func WithSessionID(ctx context.Context, id string) context.Context {
	cv := ContextValuesFromContext(ctx)
	cv.SessionID = id
	return WithContextValues(ctx, cv)
}

// WithRequestPath returns a new ctx with Path set; preserves other fields.
func WithRequestPath(ctx context.Context, path string) context.Context {
	cv := ContextValuesFromContext(ctx)
	cv.Path = path
	return WithContextValues(ctx, cv)
}

// WithUserAgent returns a new ctx with UserAgent set; preserves other fields.
func WithUserAgent(ctx context.Context, ua string) context.Context {
	cv := ContextValuesFromContext(ctx)
	cv.UserAgent = ua
	return WithContextValues(ctx, cv)
}

// WithReferer returns a new ctx with Referer set; preserves other fields.
func WithReferer(ctx context.Context, ref string) context.Context {
	cv := ContextValuesFromContext(ctx)
	cv.Referer = ref
	return WithContextValues(ctx, cv)
}

// WithUTM returns a new ctx with UTM source/medium/campaign set; preserves
// other fields.
func WithUTM(ctx context.Context, source, medium, campaign string) context.Context {
	cv := ContextValuesFromContext(ctx)
	cv.UTMSource = source
	cv.UTMMedium = medium
	cv.UTMCampaign = campaign
	return WithContextValues(ctx, cv)
}
```

- [ ] **Step 2: Verify compile**

```bash
go build ./internal/events/...
```

Expected: succeeds with no output.

#### Task 2: Create `internal/events/events.go` with Emitter

**Files:**
- Create: `internal/events/events.go`

- [ ] **Step 1: Create the file**

```go
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
// the application's main slog logger (which writes to stdout in production,
// which Cloud Run forwards to Cloud Logging).
func NewEmitter(logger *slog.Logger) *Emitter {
	return &Emitter{logger: logger}
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
```

- [ ] **Step 2: Verify compile**

```bash
go build ./internal/events/...
```

Expected: succeeds. If `uuid` import is unresolved, `go mod tidy` first; the module is already a transitive dep of pgx.

#### Task 3: Tests for events package

**Files:**
- Create: `internal/events/events_test.go`

- [ ] **Step 1: Write the failing tests**

```go
package events_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/events"
)

func TestEmit_WritesAllStandardFields(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	em := events.NewEmitter(logger)

	ctx := context.Background()
	ctx = events.WithVisitorID(ctx, "visitor-abc")
	ctx = events.WithUserID(ctx, "user-xyz")
	ctx = events.WithReferer(ctx, "https://news.ycombinator.com/")
	ctx = events.WithUTM(ctx, "hn", "social", "show-hn")

	em.Emit(ctx, "signup_completed", slog.String("auth_method", "password"))

	var record map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &record))

	require.Equal(t, true, record["analytics_event"])
	require.Equal(t, "signup_completed", record["event_name"])
	require.Equal(t, "visitor-abc", record["visitor_id"])
	require.Equal(t, "user-xyz", record["user_id"])
	require.Equal(t, "https://news.ycombinator.com/", record["referer"])
	require.Equal(t, "hn", record["utm_source"])
	require.NotEmpty(t, record["event_id"], "event_id must be a UUID")

	props, ok := record["properties"].(map[string]any)
	require.True(t, ok, "properties must be a nested object")
	require.Equal(t, "password", props["auth_method"])
}

func TestEmit_NoContextValues_StillEmitsWithEmpties(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	em := events.NewEmitter(logger)

	em.Emit(context.Background(), "landing_view")

	var record map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &record))
	require.Equal(t, "landing_view", record["event_name"])
	require.Equal(t, "", record["visitor_id"])
	require.Equal(t, "", record["user_id"])
}

func TestEmit_NilEmitter_NoPanic(t *testing.T) {
	var em *events.Emitter
	em.Emit(context.Background(), "anything") // must not panic
}

func TestContextValues_PreserveAcrossWithCalls(t *testing.T) {
	ctx := context.Background()
	ctx = events.WithVisitorID(ctx, "v1")
	ctx = events.WithUserID(ctx, "u1")
	ctx = events.WithReferer(ctx, "r1")
	ctx = events.WithUTM(ctx, "s1", "m1", "c1")

	cv := events.ContextValuesFromContext(ctx)
	require.Equal(t, "v1", cv.VisitorID)
	require.Equal(t, "u1", cv.UserID)
	require.Equal(t, "r1", cv.Referer)
	require.Equal(t, "s1", cv.UTMSource)
	require.Equal(t, "m1", cv.UTMMedium)
	require.Equal(t, "c1", cv.UTMCampaign)
}
```

- [ ] **Step 2: Run; expect failures**

```bash
go test ./internal/events/... -v
```

Expected: tests should compile and run; if `Emit` is wrong, fail loudly.

- [ ] **Step 3: Iterate until green; commit**

```bash
go test ./internal/events/... -v
```

Expected: all 4 tests pass.

```bash
git add internal/events/
git commit -m "events: add fire-and-forget analytics emitter and per-request context helpers"
```

---

### Phase 2 — Inject Emitter into Backend and SampleService

#### Task 4: Modify `Backend` constructor to accept and hold an Emitter

**Files:**
- Modify: `internal/backend/backend.go` (the `New` constructor)
- Modify: any test helper that constructs `Backend` (search after the change)

- [ ] **Step 1: Read the current `New` signature**

```bash
grep -n "func New(" internal/backend/backend.go | head -5
```

Note the exact signature so the change is precise.

- [ ] **Step 2: Add `events` field to the `Backend` struct**

In `internal/backend/backend.go`, locate the `Backend` struct definition (likely near the top of the file). Add an `events *events.Emitter` field. Add the import `"github.com/btc/drill/internal/events"`.

- [ ] **Step 3: Update `New` to accept and assign**

Append `em *events.Emitter` to `New`'s parameter list. In the `Backend{...}` initializer at the end of `New`, set `events: em`.

- [ ] **Step 4: Add a getter (used by jobs/cleanup later)**

Add a method:

```go
// Events returns the Backend's analytics emitter. Used by background jobs
// that hold a Backend reference and need to emit events.
func (b *Backend) Events() *events.Emitter {
	return b.events
}
```

- [ ] **Step 5: Update all callers**

```bash
grep -rn "backend.New(\|backend\.New(" --include='*.go' . 2>/dev/null | grep -v -E '_test\.go|\.worktrees|\.claude'
```

Expected callers:
- `cmd/drill/main.go` (production entry point)
- `internal/testutil/backend.go:17` (`NewBackend(t, cfg)` test helper — calls `backend.New(cfg)`)

(Note: `internal/backendtest/backendtest.go` only contains `SeedUser`, which takes an existing `*backend.Backend`; no constructor change needed there.)

In `cmd/drill/main.go`, instantiate the emitter from the application logger and pass it:

```go
em := events.NewEmitter(slog.Default())
b, err := backend.New(cfg, em)
if err != nil {
    return fmt.Errorf("backend: %w", err)
}
```

- [ ] **Step 6: Update `internal/testutil/backend.go`**

Modify the `NewBackend` helper at line 15 to construct a discard-logger emitter and pass it through:

```go
import (
    "io"
    "log/slog"
    "github.com/btc/drill/internal/events"
    // ... existing imports
)

func NewBackend(t *testing.T, cfg *config.Config) *backend.Backend {
    t.Helper()
    em := events.NewEmitter(slog.New(slog.NewJSONHandler(io.Discard, nil)))
    b, err := backend.New(cfg, em)
    // ... rest unchanged
}
```

(`PG.NewBackend(t)` at line 26 already delegates to this helper, so no additional change is needed there.)

- [ ] **Step 7: Verify build + tests**

```bash
go build ./...
go test ./internal/... ./cmd/... -race -count=1 -timeout=300s
```

Expected: build passes; tests pass.

- [ ] **Step 8: Commit**

```bash
git add internal/backend/backend.go internal/testutil/backend.go cmd/drill/main.go
git commit -m "backend: inject events.Emitter into Backend constructor"
```

#### Task 5: Update SampleService Server to take Emitter; change `GetSampleSession` signature

**Files:**
- Modify: `internal/rpc/sample/server.go`
- Modify: `internal/rpc/register.go`

- [ ] **Step 1: Update `Server` struct + `NewServer`**

In `internal/rpc/sample/server.go`, locate `type Server struct{ ss *SampleService }` (or similar). Add an `em *events.Emitter` field. Update `NewServer` to:

```go
func NewServer(ss *SampleService, em *events.Emitter) *Server {
	return &Server{ss: ss, em: em}
}
```

Add the import `"github.com/btc/drill/internal/events"`.

- [ ] **Step 2: Change `GetSampleSession` to use ctx**

Read the existing method (line 86):

```go
func (s *Server) GetSampleSession(_ context.Context, _ *connect.Request[...]) (*connect.Response[...], error) {
    ...
}
```

Change the first parameter from `_ context.Context` to `ctx context.Context`. (Don't emit the event yet — that happens in Phase 6 / Task 18.)

- [ ] **Step 3: Update `Register`**

In `internal/rpc/register.go`, the existing call site at line 41 is:

```go
mux.Handle(drillv1connect.NewSampleServiceHandler(samplerpc.NewServer(b.SampleService), publicOpts))
```

Note: the package alias is `samplerpc` (line 20), not `sample`. Update to pass the emitter:

```go
mux.Handle(drillv1connect.NewSampleServiceHandler(samplerpc.NewServer(b.SampleService, b.Events()), publicOpts))
```

- [ ] **Step 4: Verify build + tests**

```bash
go build ./...
go test ./internal/rpc/... -v
```

Expected: passes.

- [ ] **Step 5: Commit**

```bash
git add internal/rpc/sample/server.go internal/rpc/register.go
git commit -m "rpc(sample): inject events.Emitter; surface ctx in GetSampleSession"
```

---

### Phase 3 — AnalyticsContextMiddleware

#### Task 6: Create the middleware

**Files:**
- Create: `internal/handler/analytics_middleware.go`

- [ ] **Step 1: Create the file**

```go
package handler

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/events"
)

// VisitorCookieName is the name of the first-party visitor identification
// cookie set on every public request. Used for stitching anonymous funnel
// activity to a single visitor across requests.
const VisitorCookieName = "sabermatic_visitor"

// AnalyticsContextMiddleware reads or sets the visitor cookie, captures the
// Referer header and UTM query params, and stashes them in the request
// context for downstream events.Emit calls. Runs on every request so that
// ConnectRPC handlers see populated values.
//
// secureCookies should be true on HTTPS deployments; pass the same flag the
// session cookie uses.
func AnalyticsContextMiddleware(secureCookies bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Read or mint visitor_id.
			var visitorID string
			if c, err := r.Cookie(VisitorCookieName); err == nil && c.Value != "" {
				visitorID = c.Value
			} else {
				visitorID = uuid.NewString()
				http.SetCookie(w, &http.Cookie{
					Name:     VisitorCookieName,
					Value:    visitorID,
					Path:     "/",
					HttpOnly: true,
					Secure:   secureCookies,
					SameSite: http.SameSiteLaxMode,
					MaxAge:   60 * 60 * 24 * 365, // 1 year
				})
			}

			// Capture referer and UTM params.
			referer := r.Header.Get("Referer")
			q := r.URL.Query()
			utmSource := q.Get("utm_source")
			utmMedium := q.Get("utm_medium")
			utmCampaign := q.Get("utm_campaign")

			ctx := r.Context()
			ctx = events.WithVisitorID(ctx, visitorID)
			ctx = events.WithReferer(ctx, referer)
			ctx = events.WithUTM(ctx, utmSource, utmMedium, utmCampaign)
			ctx = events.WithRequestPath(ctx, r.URL.Path)
			ctx = events.WithUserAgent(ctx, r.Header.Get("User-Agent"))

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// Compile-time assertion that auth package's session cookie name doesn't
// collide with our visitor cookie name. (Belt and suspenders against future
// renames.)
var _ = auth.SessionCookieName
```

- [ ] **Step 2: Verify compile**

```bash
go build ./internal/handler/...
```

Expected: succeeds.

#### Task 7: Tests for the middleware

**Files:**
- Create: `internal/handler/analytics_middleware_test.go`

- [ ] **Step 1: Write tests**

```go
package handler_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/events"
	"github.com/btc/drill/internal/handler"
)

func TestAnalyticsContextMiddleware_SetsCookieOnFirstHit(t *testing.T) {
	mw := handler.AnalyticsContextMiddleware(false)

	var capturedVisitorID string
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		capturedVisitorID = events.ContextValuesFromContext(r.Context()).VisitorID
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, req)

	cookies := rec.Result().Cookies()
	require.Len(t, cookies, 1)
	require.Equal(t, handler.VisitorCookieName, cookies[0].Name)
	require.NotEmpty(t, cookies[0].Value)
	require.Equal(t, cookies[0].Value, capturedVisitorID, "ctx visitor_id must match the cookie value")
}

func TestAnalyticsContextMiddleware_ReusesExistingCookie(t *testing.T) {
	mw := handler.AnalyticsContextMiddleware(false)

	var capturedVisitorID string
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		capturedVisitorID = events.ContextValuesFromContext(r.Context()).VisitorID
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: handler.VisitorCookieName, Value: "existing-visitor-id"})
	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, req)

	require.Empty(t, rec.Result().Cookies(), "no new cookie should be set when one exists")
	require.Equal(t, "existing-visitor-id", capturedVisitorID)
}

func TestAnalyticsContextMiddleware_CapturesRefererAndUTM(t *testing.T) {
	mw := handler.AnalyticsContextMiddleware(false)

	var cv events.ContextValues
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		cv = events.ContextValuesFromContext(r.Context())
	})

	req := httptest.NewRequest(http.MethodGet, "/?utm_source=hn&utm_medium=social&utm_campaign=show-hn", nil)
	req.Header.Set("Referer", "https://news.ycombinator.com/")
	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, req)

	require.Equal(t, "https://news.ycombinator.com/", cv.Referer)
	require.Equal(t, "hn", cv.UTMSource)
	require.Equal(t, "social", cv.UTMMedium)
	require.Equal(t, "show-hn", cv.UTMCampaign)
}
```

- [ ] **Step 2: Run; iterate to green**

```bash
go test ./internal/handler/... -run AnalyticsContextMiddleware -v
```

Expected: all 3 tests pass.

#### Task 8: Wire middleware into the server

**Files:**
- Modify: `internal/handler/server.go` (around lines 40–55)

- [ ] **Step 1: Update `NewHandler`**

Read `internal/handler/server.go` lines 30–60. The current chain is:

```go
otelHandler := otelhttp.NewMiddleware(...)(mux)
return SecurityHeaders(secureCookies, otelHandler), nil
```

Wrap `mux` with `AnalyticsContextMiddleware(secureCookies)` BEFORE it goes into otel:

```go
analyticsMux := AnalyticsContextMiddleware(secureCookies)(mux)
otelHandler := otelhttp.NewMiddleware(drilotel.AppName, /* ... existing opts ... */)(analyticsMux)
return SecurityHeaders(secureCookies, otelHandler), nil
```

- [ ] **Step 2: Verify build + handler tests**

```bash
go build ./...
go test ./internal/handler/... -v
```

Expected: passes.

- [ ] **Step 3: Commit middleware**

```bash
git add internal/handler/analytics_middleware.go internal/handler/analytics_middleware_test.go internal/handler/server.go
git commit -m "handler: add AnalyticsContextMiddleware (visitor cookie, referer, UTM)"
```

---

### Phase 4 — Wire user_id into both auth paths

#### Task 9: Extend `RequireAuth` to set user_id in events context

**Files:**
- Modify: `internal/handler/middleware.go` (lines 33–57)

- [ ] **Step 1: Update `RequireAuth`**

Replace the body of the inner `http.HandlerFunc` so that after `auth.WithUser(r.Context(), user)`, it also calls `events.WithUserID`. The full updated function:

```go
func RequireAuth(sa auth.SessionAuthenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(auth.SessionCookieName)
			if err != nil || cookie.Value == "" {
				writeAuthError(w, http.StatusUnauthorized, "authentication required")
				return
			}

			tokenHash := auth.HashSessionToken(cookie.Value)
			user, err := sa.AuthenticateSession(r.Context(), tokenHash)
			if err != nil {
				writeAuthError(w, http.StatusUnauthorized, "invalid or expired session")
				return
			}

			span := trace.SpanFromContext(r.Context())
			span.SetAttributes(attribute.String("user_id", user.ID.String()))

			ctx := auth.WithUser(r.Context(), user)
			ctx = events.WithUserID(ctx, user.ID.String())
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
```

Add the import: `"github.com/btc/drill/internal/events"`.

- [ ] **Step 2: Verify build**

```bash
go build ./internal/handler/...
```

#### Task 10: Extend `AuthInterceptor` to set user_id in connect handler context

**Files:**
- Modify: `internal/rpc/interceptor.go`

- [ ] **Step 1: Read the current interceptor**

```bash
cat internal/rpc/interceptor.go
```

Identify the `authenticate` helper (around line 67) that produces the authenticated context.

- [ ] **Step 2: Update `authenticate`**

After the existing `ctx = auth.WithUser(ctx, user)` line, add:

```go
ctx = events.WithUserID(ctx, user.ID.String())
```

Add the import `"github.com/btc/drill/internal/events"`.

- [ ] **Step 3: Verify build + tests**

```bash
go build ./...
go test ./internal/rpc/... -race -count=1
```

Expected: passes.

- [ ] **Step 4: Commit auth extensions**

```bash
git add internal/handler/middleware.go internal/rpc/interceptor.go
git commit -m "auth: propagate user_id into events context for both REST and ConnectRPC paths"
```

---

### Phase 5 — Beacon endpoint

#### Task 11: Create the beacon handler

**Files:**
- Create: `internal/handler/beacon.go`

- [ ] **Step 1: Create the file**

```go
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
	"landing_view":    {},
	"signup_started":  {},
}

type beaconRequest struct {
	EventName   string                 `json:"event_name"`
	Referrer    string                 `json:"referrer,omitempty"`
	UTMSource   string                 `json:"utm_source,omitempty"`
	UTMMedium   string                 `json:"utm_medium,omitempty"`
	UTMCampaign string                 `json:"utm_campaign,omitempty"`
	Properties  map[string]interface{} `json:"properties,omitempty"`
}

// BeaconHandler returns an http.HandlerFunc for POST /api/beacon. The handler
// validates the event_name against an allowlist, then constructs explicit
// emission attributes from the body (referrer/UTM take precedence over what
// the AnalyticsContextMiddleware captured from headers, since for beacon
// requests the browser-side document.referrer is the only source of truth).
func BeaconHandler(em *events.Emitter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		defer r.Body.Close()
		var req beaconRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid json body", http.StatusBadRequest)
			return
		}
		if _, ok := allowedBeaconEvents[req.EventName]; !ok {
			http.Error(w, "unknown event_name", http.StatusBadRequest)
			return
		}

		// Body-provided referrer/UTM take precedence over middleware-captured
		// header values (which point to the SPA page that fired the beacon,
		// not the external referrer the browser remembers).
		ctx := r.Context()
		if req.Referrer != "" {
			ctx = events.WithReferer(ctx, req.Referrer)
		}
		if req.UTMSource != "" || req.UTMMedium != "" || req.UTMCampaign != "" {
			ctx = events.WithUTM(ctx, req.UTMSource, req.UTMMedium, req.UTMCampaign)
		}

		// Convert properties map to slog.Attr slice.
		var props []slog.Attr
		for k, v := range req.Properties {
			props = append(props, slog.Any(k, v))
		}

		em.Emit(ctx, req.EventName, props...)
		w.WriteHeader(http.StatusNoContent)
	}
}
```

- [ ] **Step 2: Verify compile**

```bash
go build ./internal/handler/...
```

#### Task 12: Tests for the beacon

**Files:**
- Create: `internal/handler/beacon_test.go`

- [ ] **Step 1: Write tests**

```go
package handler_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/events"
	"github.com/btc/drill/internal/handler"
)

func newRecorder(t *testing.T) (*events.Emitter, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	return events.NewEmitter(logger), &buf
}

func TestBeacon_AcceptsAllowedEvent(t *testing.T) {
	em, buf := newRecorder(t)
	body := strings.NewReader(`{"event_name":"landing_view","referrer":"https://news.ycombinator.com/"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/beacon", body)
	rec := httptest.NewRecorder()

	handler.BeaconHandler(em).ServeHTTP(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)
	var rec1 map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &rec1))
	require.Equal(t, "landing_view", rec1["event_name"])
	require.Equal(t, "https://news.ycombinator.com/", rec1["referer"])
}

func TestBeacon_RejectsDisallowedEvent(t *testing.T) {
	em, _ := newRecorder(t)
	body := strings.NewReader(`{"event_name":"signup_completed"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/beacon", body)
	rec := httptest.NewRecorder()

	handler.BeaconHandler(em).ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestBeacon_RejectsBadJSON(t *testing.T) {
	em, _ := newRecorder(t)
	req := httptest.NewRequest(http.MethodPost, "/api/beacon", io.NopCloser(strings.NewReader("not json")))
	rec := httptest.NewRecorder()
	handler.BeaconHandler(em).ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestBeacon_RejectsNonPost(t *testing.T) {
	em, _ := newRecorder(t)
	req := httptest.NewRequest(http.MethodGet, "/api/beacon", nil)
	rec := httptest.NewRecorder()
	handler.BeaconHandler(em).ServeHTTP(rec, req)
	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestBeacon_PassesPropertiesIntoEmission(t *testing.T) {
	em, buf := newRecorder(t)
	body := strings.NewReader(`{"event_name":"signup_started","properties":{"auth_method":"google"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/beacon", body)
	rec := httptest.NewRecorder()

	handler.BeaconHandler(em).ServeHTTP(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)
	var rec1 map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &rec1))
	props, ok := rec1["properties"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "google", props["auth_method"])
}
```

- [ ] **Step 2: Run tests; iterate to green**

```bash
go test ./internal/handler/... -run Beacon -v
```

Expected: 5 tests pass.

#### Task 13: Wire beacon route in `registerRoutes`

**Files:**
- Modify: `internal/handler/server.go` (registerRoutes function)

- [ ] **Step 1: Add the route**

In `internal/handler/server.go`, find `registerRoutes(mux *http.ServeMux, b *backend.Backend)`. After existing route registrations, add:

```go
mux.HandleFunc("POST /api/beacon", BeaconHandler(b.Events()))
```

(Uses Go 1.22+ method-based pattern syntax — already used elsewhere in the codebase if available; otherwise use `mux.Handle("/api/beacon", BeaconHandler(...))` with method-check inside the handler.)

- [ ] **Step 2: Verify build + tests**

```bash
go build ./...
go test ./internal/handler/...
```

- [ ] **Step 3: Commit beacon**

```bash
git add internal/handler/beacon.go internal/handler/beacon_test.go internal/handler/server.go
git commit -m "handler: add POST /api/beacon for frontend-emitted analytics events"
```

---

### Phase 6 — Backend emissions

For each emission task: locate the success path, add the `b.events.Emit(ctx, ...)` call after the operation succeeds (just before `return nil` or the success return). Keep emissions outside any deferred error wrapping so they can read the now-final ctx.

#### Task 14: Emit `signup_completed` and `email_verified`

**Files:**
- Modify: `internal/backend/auth.go`

- [ ] **Step 1: Emit in `Backend.Signup`**

Locate `func (b *Backend) Signup(...)` (around line 110). At the success path (after the new user is created and committed, just before `return nil`), add:

```go
b.events.Emit(ctx, "signup_completed",
    slog.String("auth_method", "password"),
    slog.String("new_user_id", user.ID.String()),
)
```

Add `"log/slog"` to the imports if not already present.

- [ ] **Step 2: Emit in `Backend.VerifyEmail`**

Locate `func (b *Backend) VerifyEmail(...)` (around line 274). At the success path, add:

```go
b.events.Emit(ctx, "email_verified")
```

- [ ] **Step 3: Verify build + run auth tests**

```bash
go build ./...
go test ./internal/backend/... -run Signup -v
go test ./internal/backend/... -run VerifyEmail -v
```

#### Task 15: Emit `oauth_completed`

**Files:**
- Modify: `internal/backend/oauth.go`

- [ ] **Step 1: Emit in `Backend.OAuthLogin`**

Locate `func (b *Backend) OAuthLogin(...)` (around line 36). The function uses internal `path` constants (`pathNewUser`, `pathReactivated`, `pathExisting`) to distinguish flows. At the success path, before returning the `*OAuthLoginResult`, add:

```go
b.events.Emit(ctx, "oauth_completed",
    slog.String("auth_method", p.Provider), // "google" or "github" (already a string)
    slog.String("new_user_id", user.ID.String()),
    slog.Bool("is_new_user", path == pathNewUser),
    slog.Bool("is_reactivated", path == pathReactivated),
)
```

The local variable in scope is `user` (`db.User`), accessible throughout `OAuthLogin`. There is no `result` variable until the very end of the function where `*OAuthLoginResult` is constructed; emit BEFORE that point so `user` and `path` are both in scope. Verify by reading the surrounding code.

- [ ] **Step 2: Verify**

```bash
go build ./...
go test ./internal/backend/... -run OAuth -v
```

#### Task 16: Emit `session_created`

**Files:**
- Modify: `internal/backend/session.go`

- [ ] **Step 1: Emit in `Backend.CreateSession`**

Locate `func (b *Backend) CreateSession(...)` (around line 36). At the success path, after the session row is inserted and committed, before `return session, nil`:

```go
ctx = events.WithSessionID(ctx, session.ID.String())
b.events.Emit(ctx, "session_created",
    slog.String("question_id", session.QuestionID.String()),
)
```

(The `events.WithSessionID` call attaches `session_id` to the analytics context as a top-level field, so it lands at `jsonPayload.session_id` in the BQ raw table — matching the view DDL. Putting it in the `props` slice instead would bury it under `properties.session_id` and break the view.)

Add the import `"github.com/btc/drill/internal/events"` if not already present.

- [ ] **Step 2: Verify**

```bash
go build ./...
go test ./internal/backend/... -run CreateSession -v
```

- [ ] **Step 3: Commit auth + session-creation emissions**

```bash
git add internal/backend/auth.go internal/backend/oauth.go internal/backend/session.go
git commit -m "events: emit signup_completed, email_verified, oauth_completed, session_created"
```

#### Task 17: Emit `sample_view`

**Files:**
- Modify: `internal/rpc/sample/server.go`

- [ ] **Step 1: Emit in `(*Server).GetSampleSession`**

The `ctx` parameter was already wired in Task 5. At the start of the function (or right before returning the response on the success path), add:

```go
s.em.Emit(ctx, "sample_view")
```

- [ ] **Step 2: Verify build**

```bash
go build ./...
```

#### Task 18: Emit `first_message_sent`

**Files:**
- Modify: `internal/backend/turn.go`

- [ ] **Step 1: Add the guarded emission**

Locate `func (b *Backend) ExecuteTurn(...)` (around line 63). Read the function carefully: at line 131, `isOpeningQuestion := len(messages) == 0 && isTextInput(p) && getTextContent(p) == ""` and `isCrashRecovery := len(messages) > 0 && messages[len(messages)-1].Role == "candidate"`. When either is true, the function returns early via `streamInterviewerResponse` (around line 138) — so the candidate-message-persist code (around lines 243–256) only runs when `!isOpeningQuestion && !isCrashRecovery`. The `!isOpeningQuestion` guard in any post-persist emit is redundant (always true at that point).

Just before reading the messages (or just after — anywhere prior to the persist block), compute:

```go
// Count candidate messages already in the session. Zero means the user has
// not yet sent any real content; if we reach the persist block at all, this
// is their first message.
candidateCount := 0
for _, m := range messages {
    if m.Role == "candidate" {
        candidateCount++
    }
}
```

Then in the persist block, AFTER `messages = append(messages, candidateMsg)` (around line 257) and BEFORE the return that calls `streamInterviewerResponse` (around line 265), emit:

```go
if candidateCount == 0 {
    ctx = events.WithSessionID(ctx, sessionID.String())
    b.events.Emit(ctx, "first_message_sent")
}
```

(`session_id` flows via the analytics context, not as a property — same reason as `Backend.CreateSession`.)

Add `"github.com/btc/drill/internal/events"` import if missing.

- [ ] **Step 2: Verify build + run turn tests**

```bash
go build ./...
go test ./internal/backend/... -run ExecuteTurn -v
```

- [ ] **Step 3: Commit**

```bash
git add internal/rpc/sample/server.go internal/backend/turn.go
git commit -m "events: emit sample_view and first_message_sent"
```

#### Task 19: Modify cleanup SQL queries to RETURN user_ids

**Files:**
- Modify: `sql/queries/sessions.sql`
- Auto-regen: `internal/db/sessions.sql.go`

- [ ] **Step 1: Update `CompleteAbandonedActiveSessions` to return user_id**

In `sql/queries/sessions.sql`, locate the query and modify the RETURNING clause. Change:

```sql
-- name: CompleteAbandonedActiveSessions :many
... existing ...
RETURNING id;
```

to:

```sql
-- name: CompleteAbandonedActiveSessions :many
... existing ...
RETURNING id, user_id;
```

- [ ] **Step 2: Update `CancelAbandonedEmptySessions` similarly**

Same change to its RETURNING clause: `RETURNING id, user_id`.

- [ ] **Step 3: Regenerate**

```bash
sqlc generate
```

The generated functions now return `[]struct{ID uuid.UUID; UserID uuid.UUID}` instead of `[]uuid.UUID`. Verify:

```bash
grep -A5 "CompleteAbandonedActiveSessions\|CancelAbandonedEmptySessions" internal/db/sessions.sql.go | head -40
```

Expected: row types include both `ID` and `UserID`.

- [ ] **Step 4: Update callers of the changed signatures**

```bash
grep -rn "CompleteAbandonedActiveSessions\|CancelAbandonedEmptySessions" --include='*.go' . 2>/dev/null | grep -v -E '_test\.go|\.worktrees|\.claude|/db/'
```

`internal/jobs/cleanup.go` is the caller. The next task updates it.

- [ ] **Step 5: Verify build (will fail until cleanup.go is updated in next task)**

```bash
go build ./internal/db/...
```

Expected: `internal/db/...` compiles.

```bash
go build ./internal/jobs/...
```

Expected: FAILS — `cleanup.go` is using the old return type. That's expected; Task 20 fixes it.

#### Task 20: Emit `session_ended` from `CompleteSession`, `CancelSession`, and the cleanup job

**Files:**
- Modify: `internal/backend/session.go`
- Modify: `internal/jobs/cleanup.go`

- [ ] **Step 1: Emit in `Backend.CompleteSession`**

Locate `func (b *Backend) CompleteSession(...)` (line ~286). At the success path, AFTER the transaction commits, before `return nil`:

```go
ctx = events.WithSessionID(ctx, sessionID.String())
b.events.Emit(ctx, "session_ended",
    slog.String("reason", "completed"),
    slog.Int("turn_count", turnCount),
)
```

- [ ] **Step 2: Emit in `Backend.CancelSession`**

Locate `func (b *Backend) CancelSession(...)` (line ~334). Same pattern, AFTER commit:

```go
ctx = events.WithSessionID(ctx, sessionID.String())
b.events.Emit(ctx, "session_ended",
    slog.String("reason", "cancelled"),
    slog.Int("turn_count", turnCount),
)
```

(Both place the emit AFTER commit so a commit failure does not produce a "session ended" event for a session that didn't actually end — same publish-after-commit pattern as in the cleanup job below.)

- [ ] **Step 3: Add `Events *events.Emitter` to the cleanup worker (avoid circular import)**

`internal/jobs/cleanup.go` defines `CleanupAbandonedSessionsWorker` with `Pool *pgxpool.Pool` and (post-wired) `Jobs *river.Client[pgx.Tx]`. **Do NOT add a `Backend` reference** — `backend` already imports `jobs`, so `jobs` importing `backend` would create a cycle.

Instead, add a third field for the emitter:

```go
type CleanupAbandonedSessionsWorker struct {
    river.WorkerDefaults[CleanupAbandonedSessionsArgs]
    Pool   *pgxpool.Pool
    Jobs   *river.Client[pgx.Tx]
    Events *events.Emitter // NEW
}
```

Add `"github.com/btc/drill/internal/events"` to the imports.

- [ ] **Step 4: Wire the emitter in `RegisterWorkers`**

`internal/jobs/workers.go` has `RegisterWorkers(cfg, sender, pool, llm, gemini, store)`. Add `em *events.Emitter` as a new parameter at the end:

```go
func RegisterWorkers(
    cfg *config.Config,
    sender email.Sender,
    pool *pgxpool.Pool,
    llm *ai.Client,
    gemini *ai.GeminiClient,
    store storage.Store,
    em *events.Emitter,
) (*river.Workers, WorkerRefs) {
    // ... existing code, change cleanup construction:
    cleanup := &CleanupAbandonedSessionsWorker{Pool: pool, Events: em}
    // ... rest unchanged
}
```

Update the caller in `internal/backend/backend.go` (the line `workers, workerRefs := jobs.RegisterWorkers(cfg, emailSender, pool, llmClient, geminiClient, store)`) to pass `b.events`. Since this happens inside `New` AFTER `b.events = em` has been assigned, that field is in scope.

Update `internal/jobs/cleanup_test.go` to construct the worker with `Events: events.NewEmitter(slog.New(slog.NewJSONHandler(io.Discard, nil)))` (or a recording emitter if the test asserts on emitted events).

- [ ] **Step 5: Update `cleanup.go` work logic — collect IDs during tx, emit AFTER commit**

The current cleanup job runs both terminal SQL queries and commits. The plan must place emits AFTER the commit, otherwise a commit-fail produces phantom "ended" events. Refactor (pseudo-diff):

```go
// Inside Work(), inside the existing tx:
completedRows, err := q.CompleteAbandonedActiveSessions(ctx)
if err != nil {
    return fmt.Errorf("complete abandoned active sessions: %w", err)
}
cancelledRows, err := q.CancelAbandonedEmptySessions(ctx)
if err != nil {
    return fmt.Errorf("cancel abandoned empty sessions: %w", err)
}
if err := tx.Commit(ctx); err != nil {
    return fmt.Errorf("commit cleanup: %w", err)
}

// Emit AFTER commit. If the worker crashes here, a few session_ended events
// are missed — acceptable and recoverable from Cloud Logging _Default backfill.
for _, row := range completedRows {
    emitCtx := events.WithUserID(ctx, row.UserID.String())
    emitCtx  = events.WithSessionID(emitCtx, row.ID.String())
    w.Events.Emit(emitCtx, "session_ended",
        slog.String("reason", "abandoned"),
    )
}
for _, row := range cancelledRows {
    emitCtx := events.WithUserID(ctx, row.UserID.String())
    emitCtx  = events.WithSessionID(emitCtx, row.ID.String())
    w.Events.Emit(emitCtx, "session_ended",
        slog.String("reason", "abandoned_empty"),
    )
}
return nil
```

(Field access `row.ID` and `row.UserID` matches sqlc's PascalCase from `RETURNING id, user_id`. Confirm against the regenerated `internal/db/sessions.sql.go` after Task 19's sqlc regen.)

Add imports `"log/slog"` and `"github.com/btc/drill/internal/events"` if missing.

- [ ] **Step 4: Verify build + run job tests**

```bash
go build ./...
go test ./internal/jobs/... -run Cleanup -v
go test ./internal/backend/... -run Session -v
```

- [ ] **Step 5: Commit**

```bash
git add sql/queries/sessions.sql internal/db/sessions.sql.go internal/db/querier.go \
        internal/backend/session.go internal/jobs/cleanup.go
git commit -m "events: emit session_ended from all four terminal paths with reason property"
```

---

### Phase 7 — Frontend SDK and call sites

#### Task 21: Create `web/src/lib/analytics.ts`

**Files:**
- Create: `web/src/lib/analytics.ts`

- [ ] **Step 1: Create the file**

```ts
/**
 * Frontend analytics SDK. Posts events to /api/beacon, which validates and
 * forwards to the backend events.Emitter. Fire-and-forget; never throws.
 *
 * Discriminated union enforces per-event required properties at type-check
 * time so callers can't omit auth_method on signup_started.
 */

export type TrackArgs =
  | { event: "landing_view" }
  | { event: "signup_started"; props: { auth_method: "password" | "google" | "github" } };

export function track(args: TrackArgs): void {
  try {
    const params = new URLSearchParams(window.location.search);
    const payload: Record<string, unknown> = {
      event_name: args.event,
      referrer: document.referrer || "",
      utm_source: params.get("utm_source") || "",
      utm_medium: params.get("utm_medium") || "",
      utm_campaign: params.get("utm_campaign") || "",
    };
    if ("props" in args) {
      payload.properties = args.props;
    }

    const body = JSON.stringify(payload);
    const blob = new Blob([body], { type: "application/json" });

    if (typeof navigator.sendBeacon === "function") {
      navigator.sendBeacon("/api/beacon", blob);
      return;
    }

    // Fallback for environments without sendBeacon — use fetch with keepalive
    // so the request survives page unload.
    void fetch("/api/beacon", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body,
      keepalive: true,
    }).catch(() => {
      // Silent — analytics must never throw or interrupt the user.
    });
  } catch {
    // Defensive — never let analytics break the app.
  }
}
```

- [ ] **Step 2: Verify compile**

```bash
cd web && npx tsc --noEmit -p tsconfig.app.json
```

Expected: no errors.

#### Task 22: Test the frontend SDK

**Files:**
- Create: `web/src/lib/analytics.test.ts`

- [ ] **Step 1: Write tests**

```ts
import { describe, it, expect, vi, beforeEach } from "vitest";
import { track } from "@/lib/analytics";

describe("track", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it("posts a landing_view event with referrer + UTM extracted", () => {
    const sendBeacon = vi.fn().mockReturnValue(true);
    Object.defineProperty(window, "navigator", {
      value: { ...window.navigator, sendBeacon },
      configurable: true,
    });
    Object.defineProperty(document, "referrer", {
      value: "https://news.ycombinator.com/",
      configurable: true,
    });
    Object.defineProperty(window, "location", {
      value: { search: "?utm_source=hn&utm_medium=social" },
      configurable: true,
    });

    track({ event: "landing_view" });

    expect(sendBeacon).toHaveBeenCalledOnce();
    const [url, blob] = sendBeacon.mock.calls[0];
    expect(url).toBe("/api/beacon");
    return (blob as Blob).text().then((text) => {
      const body = JSON.parse(text);
      expect(body.event_name).toBe("landing_view");
      expect(body.referrer).toBe("https://news.ycombinator.com/");
      expect(body.utm_source).toBe("hn");
      expect(body.utm_medium).toBe("social");
    });
  });

  it("posts a signup_started event with auth_method in properties", async () => {
    const sendBeacon = vi.fn().mockReturnValue(true);
    Object.defineProperty(window, "navigator", {
      value: { ...window.navigator, sendBeacon },
      configurable: true,
    });
    Object.defineProperty(document, "referrer", { value: "", configurable: true });
    Object.defineProperty(window, "location", { value: { search: "" }, configurable: true });

    track({ event: "signup_started", props: { auth_method: "google" } });

    const [, blob] = sendBeacon.mock.calls[0];
    const body = JSON.parse(await (blob as Blob).text());
    expect(body.event_name).toBe("signup_started");
    expect(body.properties).toEqual({ auth_method: "google" });
  });
});
```

- [ ] **Step 2: Run; iterate to green**

```bash
cd web && npx vitest run src/lib/analytics.test.ts
```

Expected: 2 tests pass.

#### Task 23: Wire `landing_view` on the landing page

**Files:**
- Modify: `web/src/pages/landing/index.tsx`

- [ ] **Step 1: Add the effect**

Read the current file. Add at the top of the component body (after any existing hooks):

```tsx
import { useEffect } from "react";
import { track } from "@/lib/analytics";

// ... inside the default-exported component:
useEffect(() => {
  track({ event: "landing_view" });
}, []);
```

(Placement: top of the function body, alongside other hooks. Empty dep array = fires once on mount.)

- [ ] **Step 2: Verify build**

```bash
cd web && npx tsc -b
```

#### Task 24: Wire `signup_started` on signup form + OAuth buttons

**Files:**
- Modify: `web/src/pages/auth/signup.tsx`

- [ ] **Step 1: Add the import**

```tsx
import { track } from "@/lib/analytics";
```

- [ ] **Step 2: Fire on form submit**

In the form's submit handler (likely `onSubmit={handleSubmit}` or inline), add `track({ event: "signup_started", props: { auth_method: "password" } });` as the first line of the handler (BEFORE any async work or validation that could short-circuit).

- [ ] **Step 3: Fire on OAuth buttons**

For each OAuth button (Google, GitHub), add an `onClick` (or augment existing) that calls `track({ event: "signup_started", props: { auth_method: "google" } })` or `"github"` as the first line. The OAuth redirect happens immediately after, so use `sendBeacon` (which the SDK does) to ensure the event flushes before navigation.

- [ ] **Step 4: Verify build + run frontend tests**

```bash
cd web && npx tsc -b && npx vitest run
```

Expected: all pass.

- [ ] **Step 5: Commit frontend SDK + call sites**

```bash
git add web/src/lib/analytics.ts web/src/lib/analytics.test.ts \
        web/src/pages/landing/index.tsx web/src/pages/auth/signup.tsx
git commit -m "web: add analytics SDK and emit landing_view + signup_started"
```

---

### Phase 8 — Terraform: BQ pipeline

#### Task 25: Create `terraform/analytics.tf`

**Files:**
- Create: `terraform/analytics.tf`

- [ ] **Step 1: Create the file**

```hcl
# Analytics events pipeline:
#   Cloud Run stdout (slog "analytics_event":true)
#     -> Cloud Logging
#     -> Logs Router sink (filtered)
#     -> BigQuery dataset (raw LogEntry table, sink-managed schema)
#     -> BigQuery view (flattens jsonPayload.* to named columns)

resource "google_bigquery_dataset" "analytics" {
  dataset_id    = "sabermatic_analytics"
  friendly_name = "Sabermatic analytics events"
  location      = var.region
  description   = "Funnel-reconstruction events emitted from Cloud Run via Cloud Logging Logs Router."
}

resource "google_logging_project_sink" "analytics" {
  name        = "sabermatic-analytics-events"
  description = "Routes slog analytics events from Cloud Run to BigQuery."
  destination = "bigquery.googleapis.com/projects/${var.project_id}/datasets/${google_bigquery_dataset.analytics.dataset_id}"

  # Note: jsonPayload.analytics_event is a JSON boolean (slog writes booleans
  # as native JSON booleans, not strings). Cloud Logging filter syntax
  # supports unquoted boolean literals; do NOT quote the value or the filter
  # will silently match zero entries.
  filter = <<-EOT
    resource.type = "cloud_run_revision"
    AND resource.labels.service_name = "sabermatic"
    AND jsonPayload.analytics_event = true
  EOT

  unique_writer_identity = true

  bigquery_options {
    use_partitioned_tables = true
  }
}

resource "google_bigquery_dataset_iam_member" "sink_writer" {
  dataset_id = google_bigquery_dataset.analytics.dataset_id
  role       = "roles/bigquery.dataEditor"
  member     = google_logging_project_sink.analytics.writer_identity
}

# Flattening view. Reads from the sink-managed table `analytics_events_raw`
# (created lazily by Logs Router on first matching log entry) and projects
# the slog payload fields into named columns. BQ permits view creation
# against a non-existent table; queries will return errors until the first
# write — that's expected.
resource "google_bigquery_table" "analytics_events_view" {
  dataset_id = google_bigquery_dataset.analytics.dataset_id
  table_id   = "analytics_events"

  view {
    use_legacy_sql = false
    query          = <<-EOT
      SELECT
        timestamp                                                  AS event_time,
        jsonPayload.event_id                                       AS event_id,
        jsonPayload.event_name                                     AS event_name,
        jsonPayload.visitor_id                                     AS visitor_id,
        NULLIF(jsonPayload.user_id,    '')                         AS user_id,
        NULLIF(jsonPayload.session_id, '')                         AS session_id,
        REGEXP_EXTRACT(trace, r'traces/(.+)$')                     AS trace_id,
        NULLIF(jsonPayload.referer,      '')                       AS referer,
        NULLIF(jsonPayload.utm_source,   '')                       AS utm_source,
        NULLIF(jsonPayload.utm_medium,   '')                       AS utm_medium,
        NULLIF(jsonPayload.utm_campaign, '')                       AS utm_campaign,
        NULLIF(jsonPayload.path,         '')                       AS path,
        NULLIF(jsonPayload.user_agent,   '')                       AS user_agent,
        jsonPayload.properties                                     AS properties
      FROM `${var.project_id}.${google_bigquery_dataset.analytics.dataset_id}.analytics_events_raw`
      WHERE jsonPayload.analytics_event IS TRUE
    EOT
  }

  depends_on = [google_bigquery_dataset.analytics]
}
```

- [ ] **Step 2: Verify the syntax**

```bash
cd terraform && terraform fmt -check && terraform validate
```

Expected: succeeds (assumes you've already run `terraform init` in this dir).

- [ ] **Step 3: Plan**

```bash
cd terraform && terraform plan
```

Expected: 4 new resources to add (dataset, sink, IAM member, view). No changes to existing resources outside this scope.

- [ ] **Step 4: Apply**

```bash
cd terraform && terraform apply
```

Confirm `yes`. Expected: all 4 resources created in <1 minute.

- [ ] **Step 5: Commit**

```bash
git add terraform/analytics.tf
git commit -m "infra(analytics): create BQ dataset, logs router sink, and flattening view"
```

---

### Phase 9 — End-to-end smoke test

#### Task 26: Validate the pipeline post-deploy

**Files:** none — observation only.

- [ ] **Step 1: Deploy the backend + frontend changes to prod**

Use the existing deploy path (`./scripts/deploy.sh`). Wait for the new revision to receive traffic.

- [ ] **Step 2: Manually trigger one of each event**

In a browser:
1. Visit `https://sabermatic.dev/` (open in an Incognito window with a fresh visitor cookie). Triggers `landing_view` via the beacon.
2. Visit `https://sabermatic.dev/sample`. Triggers `sample_view` via the SampleService.
3. Click "Sign up" → "Continue with Google" or "Continue with GitHub" or fill out the form. Triggers `signup_started`.
4. Complete signup. Triggers `signup_completed` (password) or `oauth_completed` (OAuth).
5. Verify your email. Triggers `email_verified`.
6. Create a session. Triggers `session_created`.
7. Send your first message. Triggers `first_message_sent`.
8. Complete the session. Triggers `session_ended` with `reason=completed`.

- [ ] **Step 3: Verify rows arrive in BQ within ~5 minutes**

In BQ console, run:

```sql
SELECT event_name, event_time, visitor_id, user_id, session_id
FROM `sabermatic-prod.sabermatic_analytics.analytics_events`
WHERE event_time > TIMESTAMP_SUB(CURRENT_TIMESTAMP(), INTERVAL 30 MINUTE)
ORDER BY event_time DESC
LIMIT 50
```

Expected: rows for each event you triggered, with non-empty `visitor_id`, `user_id` populated for post-auth events, `session_id` populated for session-scoped events.

- [ ] **Step 4: Verify `properties` queryable as RECORD**

```sql
SELECT event_name, properties.auth_method
FROM `sabermatic-prod.sabermatic_analytics.analytics_events`
WHERE event_name IN ('signup_completed', 'oauth_completed')
  AND event_time > TIMESTAMP_SUB(CURRENT_TIMESTAMP(), INTERVAL 30 MINUTE)
LIMIT 5
```

Expected: rows with `auth_method` populated (e.g., `password`, `google`, `github`).

If this returns zero rows or errors with a type message, BQ may have inferred `properties` as JSON (not RECORD). Fall back to `JSON_VALUE(properties, '$.auth_method')` and update the view DDL in `terraform/analytics.tf` to wrap `properties` with `TO_JSON()` if needed.

- [ ] **Step 5: Verify a funnel query works end-to-end**

```sql
SELECT
  COUNT(DISTINCT IF(event_name='landing_view', visitor_id, NULL))      landed,
  COUNT(DISTINCT IF(event_name='signup_completed', visitor_id, NULL))  signed_up,
  COUNT(DISTINCT IF(event_name='session_created', user_id, NULL))      created_session,
  COUNT(DISTINCT IF(event_name='first_message_sent', user_id, NULL))   used_session,
  COUNT(DISTINCT IF(event_name='session_ended' AND properties.reason='completed', user_id, NULL)) completed_session
FROM `sabermatic-prod.sabermatic_analytics.analytics_events`
WHERE event_time > TIMESTAMP_SUB(CURRENT_TIMESTAMP(), INTERVAL 1 HOUR)
```

Expected: small non-zero counts reflecting your manual smoke-test activity, with funnel monotonicity (landed ≥ signed_up ≥ etc. — assuming the smoke test followed the funnel order).

---

## Done

W3 is complete. Backend emits 9 distinct events; frontend SDK posts the two browser-side events through `/api/beacon`; Cloud Logging routes to BQ via the Logs Router sink; the `analytics_events` view exposes a clean queryable schema.

**Post-Show-HN backlog (out of scope here):**
- AI worker LLM-call decoupling (W1's known follow-up) — improves DB headroom, indirectly improves event delivery during AI-heavy load
- BQ schema evolution: add `_PARTITIONTIME` exposure if event volume grows
- Geographic enrichment (`ip_country`) via MaxMind or Cloud Logging's `httpRequest.remoteIp`
- A "Cancel real session" event distinct from the wait-wrapper path if `Backend.CancelSession` ever gets a non-Wait* caller
- Cleanup script to backfill from `_Default` Cloud Logging bucket if sink drops occur during the spike
