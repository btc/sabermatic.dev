# ConnectRPC Migration Design

**Date:** 2026-04-05
**Related:** [#47](https://github.com/btc/drill/issues/47) (API contract enforcement), [#48](https://github.com/btc/drill/issues/48) (CI verification pipeline)
**Scope:** Migrate REST API to buf ConnectRPC. WebSocket interview implementation excluded.

## Goals

1. **Primary — Contract enforcement.** Protobuf becomes the single source of truth for the API contract. Frontend-backend divergence (the root cause of issues #25-#32, #38-#39) becomes a compile error instead of a silent bug.
2. **Secondary — Streaming.** Replace polling patterns (evaluation, educator, coach) with server-streaming RPCs in later batches.

## Migration Strategy

Incremental, in-place replacement. Migrate one service group at a time. Each group fully replaces its REST handlers before moving to the next. Frontend updates in lockstep.

**First migration:** QuestionService (1 endpoint, smallest useful unit). Inspected closely to establish the pattern. Subsequent batches use it as a concrete guide.

**Service groups and order:**

| # | Service | Endpoints | Notes |
|---|---------|-----------|-------|
| 1 | Questions | ListQuestions | Pattern-setting migration |
| 2 | User | GetMe, GetUsage | Simple read-only, authenticated |
| 3 | Sessions | CreateSession, ListSessions, GetSession | Core CRUD |
| 4 | Evaluation | GetEvaluation, RetryEvaluation | Streaming candidate |
| 5 | Educator | GetEducatorAnalysis, RequestEducatorAnalysis | Streaming candidate |
| 6 | Coach | GetCoachAnalysis, RequestCoachAnalysis | Streaming candidate |
| 7 | Billing | PostCheckout, PostPortal | Stripe integration |
| 8 | Auth | Signup, Login, Logout, VerifyEmail, ForgotPassword, ResetPassword | Public, sets cookies |

**Excluded from ConnectRPC migration:**
- OAuth (`GET /api/auth/oauth/{provider}`, callback) — HTTP redirects, not JSON RPCs
- Stripe webhook (`POST /api/webhooks/stripe`) — inbound webhook with Stripe signature verification
- Health check (`GET /api/health`) — simple HTTP endpoint
- WebSocket interview (`GET /api/sessions/{id}/ws`) — out of scope per requirements

**Frontend-only stubs (no backend handler exists):**

These frontend hooks have no corresponding REST handler. They are not migrations — they need fresh implementation, either as ConnectRPC services or REST handlers first. Tracked separately:
- `POST /api/questions` (CreateQuestion) — [#49](https://github.com/btc/drill/issues/49)
- `POST /api/sessions/archive-bulk` (ArchiveBulk) — needs issue
- `PATCH /api/me` (UpdateProfile) — needs issue
- `DELETE /api/auth/account` (DeleteAccount) — needs issue
- `POST /api/me/export` (ExportData) — needs issue
- `GET /api/sessions/{id}/transcript` (GetTranscript) — needs issue

These should be implemented directly as ConnectRPC services when their respective service group is migrated, rather than implementing a REST handler and immediately replacing it.

## 1. Buf Tooling and Proto Definition

### File layout

```
pb/                          # proto source files
  drill/v1/
    question.proto
    session.proto
    ...
buf.yaml                     # module config + lint rules
buf.gen.yaml                 # codegen targets
internal/pb/                 # generated Go code (checked in)
web/src/pb/                  # generated TypeScript code (checked in)
```

### buf.yaml

- Module name: `buf.build/btc/drill`
- Lint: `DEFAULT` rules enabled
- Dependency: `buf.build/protocolbuffers/wellknowntypes` (for `google/protobuf/timestamp.proto`)
- Run `buf dep update` after creating `buf.yaml` to generate `buf.lock`

### buf.gen.yaml plugins

| Plugin | Output | Purpose |
|--------|--------|---------|
| `protocolbuffers/go` | `internal/pb/` | Go message types |
| `connectrpc/go` | `internal/pb/` | Go service stubs |
| `bufbuild/es` | `web/src/pb/` | TypeScript message types |
| `connectrpc/es` | `web/src/pb/` | TypeScript service client |
| `connectrpc/query` | `web/src/pb/` | TanStack Query hooks |

### Proto definition (QuestionService)

```protobuf
syntax = "proto3";
package drill.v1;

option go_package = "github.com/btc/drill/internal/pb/drill/v1;drillv1";

import "google/protobuf/timestamp.proto";

enum Difficulty {
  DIFFICULTY_UNSPECIFIED = 0;
  DIFFICULTY_MEDIUM = 1;
  DIFFICULTY_HARD = 2;
}

enum QuestionSource {
  QUESTION_SOURCE_UNSPECIFIED = 0;
  QUESTION_SOURCE_SEED = 1;
  QUESTION_SOURCE_CUSTOM = 2;
  QUESTION_SOURCE_COACH_GENERATED = 3;
}

service QuestionService {
  rpc ListQuestions(ListQuestionsRequest) returns (ListQuestionsResponse);
}

message ListQuestionsRequest {
  int32 page_size = 1;
  string page_token = 2;
  // Filters intentionally omitted. The frontend sends difficulty/tags params
  // (issue #50) but the backend has never implemented filtering. Filters will
  // be added to the proto when the backend supports them.
}

message ListQuestionsResponse {
  repeated Question questions = 1;
  string next_page_token = 2;
}

message Question {
  string id = 1;
  optional string user_id = 2;
  string title = 3;
  string prompt = 4;
  Difficulty difficulty = 5;
  repeated string tags = 6;
  optional string hints = 7;
  QuestionSource source = 8;
  google.protobuf.Timestamp create_time = 9;
  // Fields intentionally excluded from list response:
  // - coach_rationale: only in full Question model, not in ListQuestionsForUserRow
  // - attempt_count, best_score: frontend type includes these but backend never returns them
  // These can be added when the backend query supports them. The proto matches
  // what the backend actually returns, not what the frontend type wishes for.
}
```

**AIP compliance:**
- AIP-132/158: List method with pagination fields included from the start
- AIP-126: Enums for difficulty and source instead of bare strings
- AIP-142: `google.protobuf.Timestamp` for time fields, named `create_time`
- Skipped: AIP-122 resource names (flat UUIDs, no hierarchy), AIP-203 field behavior annotations (deferred), AIP-123 resource annotations (no tooling benefit)

## 2. Go Server Implementation

### Package layout

```
internal/rpc/
  interceptor.go              # shared auth interceptor
  register.go                 # Register() mounts all services on mux
  question/
    server.go                 # QuestionServer + db-to-proto converters
  session/
    server.go                 # (future)
  ...
```

Each service gets its own package under `internal/rpc/`. Shared concerns (auth interceptor) live at the `internal/rpc/` level.

### Generated code import paths

The `go_package` option in the proto produces two packages:
- `drillv1 "github.com/btc/drill/internal/pb/drill/v1"` — message types (`Question`, `ListQuestionsRequest`, etc.)
- `"github.com/btc/drill/internal/pb/drill/v1/drillv1connect"` — service definitions (`NewQuestionServiceHandler`, `NewQuestionServiceClient`)

### Registration

`internal/rpc/register.go` aggregates all service registrations:

```go
func Register(mux *http.ServeMux, b *backend.Backend) {
    opts := connect.WithInterceptors(
        otelconnect.NewInterceptor(),
        AuthInterceptor(b),
    )

    mux.Handle(drillv1connect.NewQuestionServiceHandler(question.NewServer(b), opts))
    // one line per service as they migrate
}
```

`internal/handler/routes.go` calls `rpc.Register(mux, b)` — no need to import individual service packages.

### Handler implementation pattern

```go
// internal/rpc/question/server.go
type QuestionServer struct {
    b *backend.Backend
}

func NewServer(b *backend.Backend) *QuestionServer {
    return &QuestionServer{b: b}
}

func (s *QuestionServer) ListQuestions(
    ctx context.Context,
    req *connect.Request[drillv1.ListQuestionsRequest],
) (*connect.Response[drillv1.ListQuestionsResponse], error) {
    user := auth.UserFromContext(ctx)
    // AIP-132: validate page_size, decode page_token, apply defaults
    // call s.b.ListQuestions(ctx, userID)
    // convert db rows to proto messages
    // encode next_page_token if more results
}
```

### DB-to-proto conversion

Each service package owns its converters. For Questions:

```go
func questionToProto(q db.ListQuestionsForUserRow) *drillv1.Question {
    // map fields: pgtype.UUID -> string, pgtype.Text -> *string,
    // time.Time -> timestamppb.New(), string -> enum
}
```

## 3. Frontend Integration

### Transport setup

New file `web/src/api/transport.ts`:

```ts
import { createConnectTransport } from "@connectrpc/connect-web";

export const transport = createConnectTransport({
  baseUrl: "/",
  credentials: "same-origin", // sends session cookie
  // No CSRF token needed — Connect's Content-Type header provides implicit CSRF protection
});
```

### Provider setup

One-time addition to app root:

```tsx
import { TransportProvider } from "@connectrpc/connect-query";

<QueryClientProvider client={queryClient}>
  <TransportProvider transport={transport}>
    ...
  </TransportProvider>
</QueryClientProvider>
```

### Usage in components

Generated hooks replace hand-written ones:

```ts
// Before (hand-written):
import { useQuestions } from "@/api/queries";
const { data } = useQuestions();

// After (generated — exact import path depends on plugin version, verify after first buf generate):
import { listQuestions } from "@/pb/drill/v1/question-QuestionService_connectquery";
import { useQuery } from "@connectrpc/connect-query";
const { data } = useQuery(listQuestions, { pageSize: 50 });
// data is typed as ListQuestionsResponse
```

### Per-endpoint migration

1. Delete hand-written hook from `queries.ts`
2. Delete hand-written type from `types.ts`
3. Update component imports to use generated hook/types
4. Generated types become the source of truth

## 4. Auth and Middleware

### Connect auth interceptor

`internal/rpc/interceptor.go` reuses existing `auth` package — no new auth logic:

- Reads `drill_session` cookie from `req.Header()`
- Calls `auth.HashSessionToken()` + `auth.SessionAuthenticator.AuthenticateSession()` (same interface the HTTP middleware uses; `Backend` implements it)
- Injects user into context via `auth.WithUser()`
- Returns `connect.CodeUnauthenticated` on failure
- Sets OTel span attribute `user_id`

For the initial migration (unary RPCs only), implement `connect.UnaryInterceptorFunc`. When streaming RPCs are added in batches 4-6, the interceptor must also implement `connect.StreamingHandlerInterceptorFunc` since streaming requests access headers via `conn.RequestHeader()` rather than `req.Header()`. The `otelconnect.NewInterceptor()` already handles both.

### CSRF

Connect routes are **exempt from gorilla/csrf**. The Connect protocol provides implicit CSRF protection:

1. Connect requests use `Content-Type: application/json` (or `application/proto`) with a `Connect-Protocol-Version: 1` header
2. Simple HTML forms can only send `application/x-www-form-urlencoded`, `multipart/form-data`, or `text/plain`
3. Cross-origin JavaScript cannot set custom Content-Type headers without CORS preflight
4. We do not serve CORS headers allowing cross-origin access
5. Therefore, Connect requests are inherently CSRF-safe for same-origin cookie auth

Implementation: the CSRF exemption in `NewHandler` (which already exempts `/api/webhooks/stripe`) is extended to exempt Connect service paths. Rather than hard-coding a prefix like `/drill.v1.`, use the path prefixes returned by each `New*ServiceHandler` call (e.g., `/drill.v1.QuestionService/`) to build the exemption list. This keeps the exemption in sync with registered services automatically. No CSRF token handling needed on the frontend Connect transport.

### Observability

Two layers of tracing:
1. **HTTP-level** — existing `otelhttp.NewMiddleware` wrapping the mux (unchanged)
2. **RPC-level** — `otelconnect.NewInterceptor()` in the Connect interceptor chain, providing method name, Connect error codes, and RPC-specific span attributes

## 5. Testing Strategy

### Go server tests

Test the full stack using `httptest.NewServer` with the Connect handler:

```go
func TestListQuestions(t *testing.T) {
    b := newTestBackend(t) // real DB, per CLAUDE.md

    // New helper for RPC tests (distinct from handler's createTestSession):
    // creates a user + auth session, returns the raw token for cookie construction
    user, rawToken := createTestUserSession(t, b)

    _, handler := drillv1connect.NewQuestionServiceHandler(
        question.NewServer(b),
        connect.WithInterceptors(rpc.AuthInterceptor(b)),
    )
    srv := httptest.NewServer(handler)
    t.Cleanup(srv.Close)

    // Authenticated client: set session cookie via cookie jar
    srvURL, err := url.Parse(srv.URL)
    require.NoError(t, err)
    jar, err := cookiejar.New(nil) // net/http/cookiejar
    require.NoError(t, err)
    jar.SetCookies(srvURL, []*http.Cookie{{
        Name: auth.SessionCookieName, Value: rawToken,
    }})
    httpClient := &http.Client{Jar: jar}
    client := drillv1connect.NewQuestionServiceClient(httpClient, srv.URL)
    // ...
}
```

**Test cases for QuestionService:**
- List returns seeded questions for authenticated user
- Pagination: default page size, custom page size, page token produces correct next page
- Empty result returns empty slice (not null)
- Unauthenticated request returns `CodeUnauthenticated`
- Enum mapping: difficulty and source round-trip correctly between DB and proto

**Auth interceptor tests:**
- Missing cookie -> `CodeUnauthenticated`
- Invalid/expired token -> `CodeUnauthenticated`
- Valid cookie -> user injected into context, RPC proceeds

### Frontend tests

Existing vitest setup. Tests verify components render with the new generated hooks. No need to test generated code itself.

## 6. Codegen Workflow and Cleanup

### Developer workflow

1. Edit `.proto` files in `pb/`
2. Run `buf generate` from repo root
3. Commit generated code in `internal/pb/` and `web/src/pb/`

Generated code is checked into git — CI doesn't need buf installed, and code review catches unexpected proto changes.

### New dependencies

**Go:**
- `connectrpc.com/connect` — Connect runtime
- `connectrpc.com/otelconnect` — OTel interceptor
- `google.golang.org/protobuf` — proto runtime (already indirect dep; becomes direct)

**npm** (keep `@connectrpc/*` packages version-aligned, and `@bufbuild/protobuf` at v2.x):
- `@connectrpc/connect` — Connect runtime
- `@connectrpc/connect-web` — browser transport
- `@connectrpc/connect-query` — TanStack Query integration (v2.x)
- `@bufbuild/protobuf` — proto runtime (v2.x, required by bufbuild/es plugin)

**CLI (dev tooling):**
- `buf` — installed via `make deps`

### Makefile target

```makefile
# Install project-level dev tools (CLIs, linters, codegen).
# Run once after clone, or when tool versions change.
deps:
	brew install bufbuild/buf/buf
```

### Cleanup per migration batch

1. Delete old handler file (e.g., `internal/handler/question.go`)
2. Remove route registration line from `routes.go`
3. Delete hand-written hooks from `web/src/api/queries.ts`
4. Delete hand-written types from `web/src/api/types.ts`
5. Update component imports to generated hooks/types

### What stays unchanged

- `internal/backend/` — business logic layer, called by RPC handlers the same way HTTP handlers called it
- `internal/db/` — sqlc layer untouched
- `internal/auth/` — reused via Connect interceptor

## CLAUDE.md Additions

1. **Proto is the API contract.** After editing `.proto` files, run `buf generate` and commit the generated code in `internal/pb/` and `web/src/pb/`. Never hand-edit generated files.

2. **ConnectRPC services follow Google AIPs where practical.** Standard methods (Get, List, Create, Update, Delete) use AIP naming, pagination (AIP-158), and error code conventions (AIP-193). Custom methods (retry, checkout) use AIP-136 naming. Skip resource names (AIP-122) and field behavior annotations (AIP-203) — we use flat UUIDs and defer annotation verbosity.

3. **`repeated` fields in proto responses must map to empty slices, not nil.** Same principle as the existing nil-slice rule, extended to proto conversion: always return an initialized slice from db-to-proto converters.

4. **ConnectRPC service handlers live in `internal/rpc/{service}/`.** REST handlers in `internal/handler/` are being incrementally migrated. New API endpoints should be implemented as ConnectRPC services, not REST handlers.
