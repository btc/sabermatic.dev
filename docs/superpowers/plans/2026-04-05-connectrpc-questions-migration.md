# ConnectRPC QuestionService Migration — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Migrate the Questions REST endpoint to ConnectRPC, establishing the pattern for all subsequent service migrations.

**Architecture:** Define proto schema in `pb/drill/v1/`, generate Go server stubs and TypeScript client hooks, implement the Connect handler in `internal/rpc/question/`, mount alongside existing REST routes, migrate frontend to generated hooks, delete old REST handler.

**Tech Stack:** buf, ConnectRPC (connect-go, connect-es, connect-query), protobuf, TanStack Query v5

**Spec:** `docs/superpowers/specs/2026-04-05-connectrpc-migration-design.md`

---

## File Structure

**Create:**
- `buf.yaml` — buf module config and lint rules
- `buf.gen.yaml` — codegen plugin config
- `pb/drill/v1/question.proto` — QuestionService proto definition
- `internal/rpc/interceptor.go` — shared auth interceptor
- `internal/rpc/register.go` — service registration aggregator
- `internal/rpc/question/server.go` — QuestionServer implementation + converters
- `internal/rpc/question/server_test.go` — integration tests
- `web/src/api/transport.ts` — Connect transport config
- `internal/pb/` — generated Go code (via buf generate)
- `web/src/pb/` — generated TypeScript code (via buf generate)

**Modify:**
- `Makefile` — add `deps` and `generate` targets
- `go.mod` / `go.sum` — new Go dependencies
- `web/package.json` — new npm dependencies
- `internal/handler/routes.go` — add `rpc.Register()` call, CSRF exemption
- `web/src/main.tsx` — add `TransportProvider`
- `web/src/pages/home.tsx` — switch to generated hook
- `web/src/pages/session-config.tsx` — switch to generated hook
- `web/src/api/queries.ts` — remove `useQuestions`, `useCreateQuestion`
- `web/src/api/types.ts` — remove `Question`, `Difficulty`, `QuestionSource`
- `CLAUDE.md` — add ConnectRPC conventions

**Delete:**
- `internal/handler/question.go` — old REST handler

---

### Task 1: Buf tooling setup

**Files:**
- Create: `buf.yaml`
- Create: `buf.gen.yaml`
- Create: `pb/drill/v1/question.proto`

- [ ] **Step 1: Install buf CLI**

Run:
```bash
brew install bufbuild/buf/buf
```
Expected: `buf --version` prints a version.

- [ ] **Step 2: Create `buf.yaml`**

```yaml
# buf.yaml
version: v2
modules:
  - path: pb
    name: buf.build/btc/drill
deps:
  - buf.build/protocolbuffers/wellknowntypes
lint:
  use:
    - DEFAULT
breaking:
  use:
    - FILE
```

- [ ] **Step 3: Create `buf.gen.yaml`**

```yaml
# buf.gen.yaml
version: v2
plugins:
  # Go message types
  - remote: buf.build/protocolbuffers/go
    out: internal/pb
    opt: paths=source_relative
  # Go Connect service stubs
  - remote: buf.build/connectrpc/go
    out: internal/pb
    opt: paths=source_relative
  # TypeScript message types
  - remote: buf.build/bufbuild/es:v2
    out: web/src/pb
  # TypeScript Connect client
  - remote: buf.build/connectrpc/es:v2
    out: web/src/pb
  # TanStack Query hooks
  - remote: buf.build/connectrpc/query:v2
    out: web/src/pb
inputs:
  - directory: pb
```

- [ ] **Step 4: Create `pb/drill/v1/question.proto`**

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
}
```

- [ ] **Step 5: Run `buf dep update` to generate lock file**

Run:
```bash
buf dep update
```
Expected: `buf.lock` created in repo root.

- [ ] **Step 6: Run `buf lint` to verify proto**

Run:
```bash
buf lint
```
Expected: No errors.

- [ ] **Step 7: Run `buf generate` to produce code**

Run:
```bash
buf generate
```
Expected: Files created in `internal/pb/drill/v1/` (Go) and `web/src/pb/drill/v1/` (TypeScript). Verify with:
```bash
ls internal/pb/drill/v1/
ls web/src/pb/drill/v1/
```

- [ ] **Step 8: Commit**

```bash
git add buf.yaml buf.gen.yaml buf.lock pb/ internal/pb/ web/src/pb/
git commit -m "feat: add buf tooling and QuestionService proto definition"
```

---

### Task 2: Go dependencies

**Files:**
- Modify: `go.mod`

- [ ] **Step 1: Add Connect dependencies**

Run:
```bash
go get connectrpc.com/connect@latest
go get connectrpc.com/otelconnect@latest
go get google.golang.org/protobuf@latest
```

- [ ] **Step 2: Verify build**

Run:
```bash
go build ./...
```
Expected: No errors.

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "chore: add connectrpc and otelconnect Go dependencies"
```

---

### Task 3: Auth interceptor

**Files:**
- Create: `internal/rpc/interceptor.go`

- [ ] **Step 1: Write the auth interceptor**

```go
package rpc

import (
	"context"
	"fmt"
	"net/http"

	"connectrpc.com/connect"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/btc/drill/internal/auth"
)

// AuthInterceptor returns a Connect unary interceptor that validates the
// session cookie and injects the authenticated user into the context.
// Reuses auth.SessionAuthenticator — the same interface the HTTP middleware uses.
func AuthInterceptor(sa auth.SessionAuthenticator) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			cookie, err := (&http.Request{Header: req.Header()}).Cookie(auth.SessionCookieName)
			if err != nil || cookie.Value == "" {
				return nil, connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("authentication required"))
			}

			tokenHash := auth.HashSessionToken(cookie.Value)
			user, err := sa.AuthenticateSession(ctx, tokenHash)
			if err != nil {
				return nil, connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("invalid or expired session"))
			}

			span := trace.SpanFromContext(ctx)
			span.SetAttributes(attribute.String("user_id", user.ID.String()))

			ctx = auth.WithUser(ctx, user)
			return next(ctx, req)
		}
	}
}
```

- [ ] **Step 2: Verify build**

Run:
```bash
go build ./internal/rpc/...
```
Expected: No errors.

- [ ] **Step 3: Commit**

```bash
git add internal/rpc/interceptor.go
git commit -m "feat: add ConnectRPC auth interceptor reusing existing auth package"
```

---

### Task 4: QuestionServer implementation

**Files:**
- Create: `internal/rpc/question/server.go`

- [ ] **Step 1: Write the server implementation**

```go
package question

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/backend"
	"github.com/btc/drill/internal/db"
	drillv1 "github.com/btc/drill/internal/pb/drill/v1"
	"github.com/btc/drill/internal/pb/drill/v1/drillv1connect"
)

const (
	defaultPageSize = 50
	maxPageSize     = 100
)

// Server implements the QuestionService Connect handler.
type Server struct {
	b *backend.Backend
}

var _ drillv1connect.QuestionServiceHandler = (*Server)(nil)

// NewServer creates a new QuestionService handler.
func NewServer(b *backend.Backend) *Server {
	return &Server{b: b}
}

// ListQuestions returns questions visible to the authenticated user.
// Follows AIP-132: validates page_size, supports cursor-based pagination.
func (s *Server) ListQuestions(
	ctx context.Context,
	req *connect.Request[drillv1.ListQuestionsRequest],
) (*connect.Response[drillv1.ListQuestionsResponse], error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("authentication required"))
	}

	// AIP-132: validate and apply page size defaults.
	pageSize := int(req.Msg.PageSize)
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}

	// Decode page token (offset-based cursor for simplicity).
	offset := 0
	if req.Msg.PageToken != "" {
		decoded, err := base64.StdEncoding.DecodeString(req.Msg.PageToken)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid page token"))
		}
		offset, err = strconv.Atoi(string(decoded))
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid page token"))
		}
	}

	rows, err := s.b.ListQuestions(ctx, user.ID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("list questions failed"))
	}

	// Apply pagination over the full result set.
	// (Backend returns all rows; paginate in-memory. When the dataset grows,
	// push LIMIT/OFFSET into the SQL query.)
	total := len(rows)
	start := offset
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	page := rows[start:end]

	questions := make([]*drillv1.Question, len(page))
	for i, row := range page {
		questions[i] = questionToProto(row)
	}

	var nextPageToken string
	if end < total {
		nextPageToken = base64.StdEncoding.EncodeToString([]byte(strconv.Itoa(end)))
	}

	return connect.NewResponse(&drillv1.ListQuestionsResponse{
		Questions:     questions,
		NextPageToken: nextPageToken,
	}), nil
}

// questionToProto converts a database row to a proto Question message.
func questionToProto(row db.ListQuestionsForUserRow) *drillv1.Question {
	q := &drillv1.Question{
		Id:         row.ID.String(),
		Title:      row.Title,
		Prompt:     row.Prompt,
		Difficulty: difficultyToProto(row.Difficulty),
		Tags:       row.Tags,
		Source:     sourceToProto(row.Source),
		CreateTime: timestamppb.New(row.CreatedAt),
	}

	if row.Tags == nil {
		q.Tags = []string{}
	}

	if row.UserID.Valid {
		uid := uuid.UUID(row.UserID.Bytes).String()
		q.UserId = &uid
	}

	if row.Hints.Valid {
		q.Hints = &row.Hints.String
	}

	return q
}

func difficultyToProto(s string) drillv1.Difficulty {
	switch s {
	case "medium":
		return drillv1.Difficulty_DIFFICULTY_MEDIUM
	case "hard":
		return drillv1.Difficulty_DIFFICULTY_HARD
	default:
		return drillv1.Difficulty_DIFFICULTY_UNSPECIFIED
	}
}

func sourceToProto(s string) drillv1.QuestionSource {
	switch s {
	case "seed":
		return drillv1.QuestionSource_QUESTION_SOURCE_SEED
	case "custom":
		return drillv1.QuestionSource_QUESTION_SOURCE_CUSTOM
	case "coach_generated":
		return drillv1.QuestionSource_QUESTION_SOURCE_COACH_GENERATED
	default:
		return drillv1.QuestionSource_QUESTION_SOURCE_UNSPECIFIED
	}
}
```

- [ ] **Step 2: Verify build**

Run:
```bash
go build ./internal/rpc/...
```
Expected: No errors. If the generated import paths differ from what's shown, adjust the imports to match the actual generated package paths in `internal/pb/`.

- [ ] **Step 3: Commit**

```bash
git add internal/rpc/question/server.go
git commit -m "feat: implement QuestionServer with AIP-132 pagination"
```

---

### Task 5: Registration and route mounting

**Files:**
- Create: `internal/rpc/register.go`
- Modify: `internal/handler/routes.go`

- [ ] **Step 1: Write the registration aggregator**

```go
package rpc

import (
	"net/http"

	"connectrpc.com/connect"
	"connectrpc.com/otelconnect"

	"github.com/btc/drill/internal/backend"
	"github.com/btc/drill/internal/pb/drill/v1/drillv1connect"
	"github.com/btc/drill/internal/rpc/question"
)

// ConnectPathPrefixes returns all path prefixes used by registered Connect
// services. Used by the CSRF middleware to exempt Connect routes.
func ConnectPathPrefixes() []string {
	return []string{
		drillv1connect.QuestionServiceName,
	}
}

// Register mounts all ConnectRPC services on the given mux.
func Register(mux *http.ServeMux, b *backend.Backend) {
	opts := connect.WithInterceptors(
		otelconnect.NewInterceptor(),
		AuthInterceptor(b),
	)

	mux.Handle(drillv1connect.NewQuestionServiceHandler(question.NewServer(b), opts))
}
```

- [ ] **Step 2: Modify `internal/handler/routes.go` — add rpc.Register call**

In `RegisterRoutes`, add `rpc.Register` at the top of the function (before REST routes) and delete the old Questions route:

Add at the top of `RegisterRoutes`, before `mux.HandleFunc("GET /api/health", ...)`:
```go
	// ConnectRPC services (migrated from REST)
	rpc.Register(mux, b)
```

Delete:
```go
	// Questions
	mux.Handle("GET /api/questions", requireAuth(http.HandlerFunc(ListQuestions(b))))
```

Add `"github.com/btc/drill/internal/rpc"` to the imports.

- [ ] **Step 3: Modify `internal/handler/routes.go` — add CSRF exemption for Connect routes**

In `NewHandler`, update the CSRF exemption check. Replace:
```go
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/webhooks/stripe" && r.Method == http.MethodPost {
			otelHandler.ServeHTTP(w, r)
			return
		}
```

With:
```go
	connectPrefixes := rpc.ConnectPathPrefixes()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/webhooks/stripe" && r.Method == http.MethodPost {
			otelHandler.ServeHTTP(w, r)
			return
		}
		// Connect protocol uses POST with custom Content-Type headers that
		// cannot be sent by simple HTML forms, providing implicit CSRF protection.
		for _, prefix := range connectPrefixes {
			if strings.HasPrefix(r.URL.Path, "/"+prefix+"/") {
				otelHandler.ServeHTTP(w, r)
				return
			}
		}
```

Verify `"strings"` is already imported (it is — used by `SPAHandler`).

- [ ] **Step 4: Delete old handler**

Delete `internal/handler/question.go`.

- [ ] **Step 5: Verify build**

Run:
```bash
go build ./...
```
Expected: No errors.

- [ ] **Step 6: Run existing tests to check nothing broke**

Run:
```bash
go test ./internal/handler/... -short -race -count=1
```
Expected: All existing tests pass. Some may fail if they hit `GET /api/questions` — that's expected since the route is now removed. If so, those tests confirm the old route is gone.

- [ ] **Step 7: Commit**

```bash
git add internal/rpc/register.go internal/handler/routes.go
git rm internal/handler/question.go
git commit -m "feat: mount QuestionService via ConnectRPC, remove REST handler"
```

---

### Task 6: QuestionServer integration tests

**Files:**
- Create: `internal/rpc/question/server_test.go`

The test file lives in `internal/rpc/question/` and reuses the testcontainers pattern from `internal/handler/testutil_test.go`. We duplicate the test helpers rather than extracting to a shared package — this keeps the pattern self-contained for the first migration. (Extraction can happen later if many service packages need it.)

- [ ] **Step 1: Write test utilities**

```go
package question_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/backend"
	"github.com/btc/drill/internal/config"
	"github.com/btc/drill/internal/handler"
	drillv1 "github.com/btc/drill/internal/pb/drill/v1"
	"github.com/btc/drill/internal/pb/drill/v1/drillv1connect"
	"github.com/btc/drill/internal/rpc"
	"github.com/btc/drill/internal/rpc/question"
)

func startPostgres(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("drill_test"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second),
		),
	)
	require.NoError(t, err)
	t.Cleanup(func() { pgContainer.Terminate(ctx) })
	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	d, err := iofs.New(os.DirFS("../../../sql/migrations"), ".")
	require.NoError(t, err)
	trimmed := strings.TrimPrefix(connStr, "postgresql://")
	trimmed = strings.TrimPrefix(trimmed, "postgres://")
	pgxURL := "pgx5://" + trimmed
	m, err := migrate.NewWithSourceInstance("iofs", d, pgxURL)
	require.NoError(t, err)
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("migrate up: %v", err)
	}
	return connStr
}

func newTestBackend(t *testing.T) *backend.Backend {
	t.Helper()
	connStr := startPostgres(t)
	t.Setenv("DATABASE_URL", connStr)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	t.Setenv("OPENAI_API_KEY", "sk-test")
	t.Setenv("AUTH_TOKEN_SECRET", "test-secret-at-least-32-bytes-long")
	t.Setenv("AUTH_BCRYPT_COST", "4")
	cfg, err := config.Load()
	require.NoError(t, err)
	b, err := backend.New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { b.Close() })
	return b
}

// signupAndLogin creates a user via the HTTP signup endpoint and returns
// the raw session cookie token for use in Connect client tests.
func signupAndLogin(t *testing.T, b *backend.Backend) string {
	t.Helper()
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux, b)

	// Signup
	body := `{"email":"testuser@example.com","password":"securepass123","display_name":"Test User"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/signup", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)

	// Login
	loginBody := `{"email":"testuser@example.com","password":"securepass123"}`
	req = httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewBufferString(loginBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	for _, c := range w.Result().Cookies() {
		if c.Name == auth.SessionCookieName {
			return c.Value
		}
	}
	t.Fatal("session cookie not found after login")
	return ""
}

// authedClient creates a Connect QuestionService client with the session cookie set.
func authedClient(t *testing.T, srvURL string, rawToken string) drillv1connect.QuestionServiceClient {
	t.Helper()
	u, err := url.Parse(srvURL)
	require.NoError(t, err)
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	jar.SetCookies(u, []*http.Cookie{{
		Name:  auth.SessionCookieName,
		Value: rawToken,
	}})
	return drillv1connect.NewQuestionServiceClient(
		&http.Client{Jar: jar},
		srvURL,
	)
}
```

- [ ] **Step 2: Write test — unauthenticated returns CodeUnauthenticated**

```go
func TestListQuestions_Unauthenticated(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	_, h := drillv1connect.NewQuestionServiceHandler(
		question.NewServer(b),
		connect.WithInterceptors(rpc.AuthInterceptor(b)),
	)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	client := drillv1connect.NewQuestionServiceClient(http.DefaultClient, srv.URL)
	_, err := client.ListQuestions(context.Background(), connect.NewRequest(&drillv1.ListQuestionsRequest{}))
	require.Error(t, err)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}
```

- [ ] **Step 3: Run test to verify it passes**

Run:
```bash
go test ./internal/rpc/question/ -run TestListQuestions_Unauthenticated -v -count=1 -timeout=120s
```
Expected: PASS.

- [ ] **Step 4: Write test — authenticated list returns seed questions**

```go
func TestListQuestions_Authenticated(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	rawToken := signupAndLogin(t, b)

	_, h := drillv1connect.NewQuestionServiceHandler(
		question.NewServer(b),
		connect.WithInterceptors(rpc.AuthInterceptor(b)),
	)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	client := authedClient(t, srv.URL, rawToken)
	resp, err := client.ListQuestions(context.Background(), connect.NewRequest(&drillv1.ListQuestionsRequest{}))
	require.NoError(t, err)
	// Seed questions should exist (from migrations or seed data).
	// At minimum, the response should be a valid non-nil slice.
	require.NotNil(t, resp.Msg.Questions)
}
```

- [ ] **Step 5: Run test to verify it passes**

Run:
```bash
go test ./internal/rpc/question/ -run TestListQuestions_Authenticated -v -count=1 -timeout=120s
```
Expected: PASS.

- [ ] **Step 6: Write test — pagination**

```go
func TestListQuestions_Pagination(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	rawToken := signupAndLogin(t, b)

	_, h := drillv1connect.NewQuestionServiceHandler(
		question.NewServer(b),
		connect.WithInterceptors(rpc.AuthInterceptor(b)),
	)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	client := authedClient(t, srv.URL, rawToken)

	// Request page size of 1 to exercise pagination.
	resp, err := client.ListQuestions(context.Background(), connect.NewRequest(&drillv1.ListQuestionsRequest{
		PageSize: 1,
	}))
	require.NoError(t, err)
	require.LessOrEqual(t, len(resp.Msg.Questions), 1)

	// If there are more results, next_page_token should be non-empty.
	// Fetch second page if token is present.
	if resp.Msg.NextPageToken != "" {
		resp2, err := client.ListQuestions(context.Background(), connect.NewRequest(&drillv1.ListQuestionsRequest{
			PageSize:  1,
			PageToken: resp.Msg.NextPageToken,
		}))
		require.NoError(t, err)
		require.LessOrEqual(t, len(resp2.Msg.Questions), 1)
		// Pages should not overlap.
		if len(resp.Msg.Questions) > 0 && len(resp2.Msg.Questions) > 0 {
			require.NotEqual(t, resp.Msg.Questions[0].Id, resp2.Msg.Questions[0].Id)
		}
	}
}
```

- [ ] **Step 7: Write test — invalid page token**

```go
func TestListQuestions_InvalidPageToken(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	rawToken := signupAndLogin(t, b)

	_, h := drillv1connect.NewQuestionServiceHandler(
		question.NewServer(b),
		connect.WithInterceptors(rpc.AuthInterceptor(b)),
	)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	client := authedClient(t, srv.URL, rawToken)
	_, err := client.ListQuestions(context.Background(), connect.NewRequest(&drillv1.ListQuestionsRequest{
		PageToken: "not-valid-base64-cursor",
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}
```

- [ ] **Step 8: Write test — enum round-trip**

```go
func TestListQuestions_EnumMapping(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	rawToken := signupAndLogin(t, b)

	_, h := drillv1connect.NewQuestionServiceHandler(
		question.NewServer(b),
		connect.WithInterceptors(rpc.AuthInterceptor(b)),
	)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	client := authedClient(t, srv.URL, rawToken)
	resp, err := client.ListQuestions(context.Background(), connect.NewRequest(&drillv1.ListQuestionsRequest{}))
	require.NoError(t, err)

	for _, q := range resp.Msg.Questions {
		// Every question should have a known difficulty (not UNSPECIFIED).
		require.NotEqual(t, drillv1.Difficulty_DIFFICULTY_UNSPECIFIED, q.Difficulty,
			"question %s has UNSPECIFIED difficulty", q.Id)
		// Every question should have a known source (not UNSPECIFIED).
		require.NotEqual(t, drillv1.QuestionSource_QUESTION_SOURCE_UNSPECIFIED, q.Source,
			"question %s has UNSPECIFIED source", q.Id)
	}
}
```

- [ ] **Step 9: Run all tests**

Run:
```bash
go test ./internal/rpc/... -v -count=1 -timeout=120s
```
Expected: All pass.

- [ ] **Step 10: Commit**

```bash
git add internal/rpc/question/server_test.go
git commit -m "test: add QuestionService integration tests (auth, pagination, enums)"
```

---

### Task 7: npm dependencies

**Files:**
- Modify: `web/package.json`

- [ ] **Step 1: Install Connect npm packages**

Run:
```bash
cd web && npm install @connectrpc/connect @connectrpc/connect-web @connectrpc/connect-query@^2 @bufbuild/protobuf@^2
```

- [ ] **Step 2: Verify frontend still builds**

Run:
```bash
cd web && npm run build
```
Expected: No errors.

- [ ] **Step 3: Commit**

```bash
git add web/package.json web/package-lock.json
git commit -m "chore: add connectrpc and bufbuild npm dependencies"
```

---

### Task 8: Frontend transport and provider

**Files:**
- Create: `web/src/api/transport.ts`
- Modify: `web/src/main.tsx`

- [ ] **Step 1: Create transport config**

```ts
// web/src/api/transport.ts
import { createConnectTransport } from "@connectrpc/connect-web";

export const transport = createConnectTransport({
  baseUrl: "/",
  credentials: "same-origin",
  // No CSRF token needed — Connect's Content-Type header provides implicit
  // CSRF protection. See design spec section 4.
});
```

- [ ] **Step 2: Add TransportProvider to app root**

In `web/src/main.tsx`, add the import:

```ts
import { TransportProvider } from "@connectrpc/connect-query";
import { transport } from "./api/transport";
```

Wrap the app with `TransportProvider` inside `QueryClientProvider`:

Replace:
```tsx
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <App />
      </BrowserRouter>
    </QueryClientProvider>
  </StrictMode>,
```

With:
```tsx
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <TransportProvider transport={transport}>
        <BrowserRouter>
          <App />
        </BrowserRouter>
      </TransportProvider>
    </QueryClientProvider>
  </StrictMode>,
```

- [ ] **Step 3: Verify build**

Run:
```bash
cd web && npm run build
```
Expected: No errors.

- [ ] **Step 4: Commit**

```bash
git add web/src/api/transport.ts web/src/main.tsx
git commit -m "feat: add ConnectRPC transport and TransportProvider"
```

---

### Task 9: Migrate frontend components to generated hooks

**Files:**
- Modify: `web/src/pages/home.tsx`
- Modify: `web/src/pages/session-config.tsx`
- Modify: `web/src/api/queries.ts`
- Modify: `web/src/api/types.ts`

Important: The exact import path for the generated TanStack Query hook depends on what `buf generate` actually produced in Task 1. Before starting this task, check `web/src/pb/drill/v1/` to find the file that exports `listQuestions`. The import path below is illustrative — adjust to match the actual generated file.

- [ ] **Step 1: Check the generated hook export**

Run:
```bash
ls web/src/pb/drill/v1/
```

Find the file containing the `listQuestions` query descriptor (likely named something like `question-QuestionService_connectquery.ts` or `question_connectquery.ts`). Read it to confirm the export name.

- [ ] **Step 2: Update `home.tsx`**

Replace the import (keep `useCreateQuestion` — it's a dead stub but still referenced):
```ts
import {
  useMe, useQuestions, useSessions, useCoachLatest,
  useRequestCoachAnalysis, useCreateQuestion,
} from "@/api/queries";
```

With (adjust generated import path per Step 1):
```ts
import {
  useMe, useSessions, useCoachLatest,
  useRequestCoachAnalysis, useCreateQuestion,
} from "@/api/queries";
import { useQuery } from "@connectrpc/connect-query";
import { listQuestions } from "@/pb/drill/v1/question-QuestionService_connectquery";
```

Replace the hook call:
```ts
const { data: questions = [] } = useQuestions();
```

With:
```ts
const { data: questionsResp } = useQuery(listQuestions, {});
const questions = questionsResp?.questions ?? [];
```

The generated proto `Question` type has different field names and types than the old hand-written `Question` interface. The implementer must update all references in the component. Key differences:

| Old (types.ts) | New (proto) | Migration |
|---|---|---|
| `question.user_id` | `question.userId` | Rename |
| `question.created_at` (string) | `question.createTime` (Timestamp) | Use `.toDate()` for display |
| `question.difficulty` (`"medium" \| "hard"`) | `question.difficulty` (enum number) | Import enum, compare with `Difficulty.MEDIUM` etc. |
| `question.source` (`"seed" \| "custom" \| ...`) | `question.source` (enum number) | Import enum, compare with `QuestionSource.SEED` etc. |
| `question.hints` (`string \| null`) | `question.hints` (`string \| undefined`) | `??` instead of `?? null` if needed |
| `question.coach_rationale` | Not in proto | Field not available (was never returned by list endpoint anyway) |
| `question.attempt_count` | Not in proto | Field not available (was never returned) |
| `question.best_score` | Not in proto | Field not available (was never returned) |

Import the enums for comparisons:
```ts
import { Difficulty, QuestionSource } from "@/pb/drill/v1/question_pb";
import type { Question as ProtoQuestion } from "@/pb/drill/v1/question_pb";
```

Search the component for all references to old field names (`grep -n 'difficulty\|source\|created_at\|user_id\|coach_rationale\|attempt_count\|best_score'`) and update each one. The component should compile with no type errors after the changes.

Remove `import type { Question } from "@/api/types"` — use the generated type instead. If both the old and new `Question` are needed temporarily (e.g., `useCreateQuestion` still uses the old type), alias the proto type: `import type { Question as ProtoQuestion }` or keep the old type import separate.

- [ ] **Step 3: Update `session-config.tsx`**

Same pattern as `home.tsx`:

Replace the import:
```ts
import {
  useQuestions,
  useCoachLatest,
  useCreateSession,
  useMe,
  useUsage,
} from "@/api/queries";
```

With:
```ts
import {
  useCoachLatest,
  useCreateSession,
  useMe,
  useUsage,
} from "@/api/queries";
import { useQuery } from "@connectrpc/connect-query";
import { listQuestions } from "@/pb/drill/v1/question-QuestionService_connectquery";
```

Replace:
```ts
const { data: questions = [] } = useQuestions();
```

With:
```ts
const { data: questionsResp } = useQuery(listQuestions, {});
const questions = questionsResp?.questions ?? [];
```

Apply the same field name, enum, and timestamp changes as described in the migration table in Step 2. Search the component for all references to old field names and update each one.

- [ ] **Step 4: Remove old `useQuestions` hook from queries.ts**

In `web/src/api/queries.ts`, delete only the `useQuestions` function (lines 28-39). Keep `useCreateQuestion` — it's a dead stub (no backend handler, tracked in #49) but still imported in `home.tsx`. It will be replaced when CreateQuestion is implemented as a ConnectRPC service.

Keep the `Question`, `Difficulty`, and `QuestionSource` types in `web/src/api/types.ts` — they're still referenced by `useCreateQuestion`. They'll be removed when that hook is replaced.

- [ ] **Step 5: Verify build**

Run:
```bash
cd web && npm run build
```
Expected: No errors. Fix any remaining type mismatches between the old `Question` interface and the generated proto type.

- [ ] **Step 6: Run frontend tests**

Run:
```bash
cd web && npm test
```
Expected: All pass (or pre-existing failures only).

- [ ] **Step 7: Commit**

```bash
git add web/src/pages/home.tsx web/src/pages/session-config.tsx web/src/api/queries.ts web/src/api/types.ts
git commit -m "feat: migrate frontend to generated ConnectRPC Question hooks"
```

---

### Task 10: Makefile and CLAUDE.md updates

**Files:**
- Modify: `Makefile`
- Modify: `CLAUDE.md`

- [ ] **Step 1: Add Makefile targets**

Append to the end of `Makefile`:

```makefile

# Install project-level dev tools (CLIs, linters, codegen).
# Run once after clone, or when tool versions change.
deps:
	brew install bufbuild/buf/buf

# Regenerate protobuf code from .proto sources.
generate:
	buf generate
```

- [ ] **Step 2: Add CLAUDE.md entries**

Append to `CLAUDE.md`:

```
Proto is the API contract. After editing .proto files, run `buf generate` and commit the generated code in `internal/pb/` and `web/src/pb/`. Never hand-edit generated files.

ConnectRPC services follow Google AIPs where practical. Standard methods (Get, List, Create, Update, Delete) use AIP naming, pagination (AIP-158), and error code conventions (AIP-193). Custom methods (retry, checkout) use AIP-136 naming. Skip resource names (AIP-122) and field behavior annotations (AIP-203) — we use flat UUIDs and defer annotation verbosity.

`repeated` fields in proto responses must map to empty slices, not nil. Same principle as the existing nil-slice rule, extended to proto conversion: always return an initialized slice from db-to-proto converters.

ConnectRPC service handlers live in `internal/rpc/{service}/`. REST handlers in `internal/handler/` are being incrementally migrated. New API endpoints should be implemented as ConnectRPC services, not REST handlers.
```

- [ ] **Step 3: Commit**

```bash
git add Makefile CLAUDE.md
git commit -m "chore: add buf Makefile targets and ConnectRPC CLAUDE.md conventions"
```

---

### Task 11: End-to-end verification

- [ ] **Step 1: Run full Go test suite**

Run:
```bash
make test
```
Expected: All pass.

- [ ] **Step 2: Run full frontend build**

Run:
```bash
cd web && npm run build
```
Expected: No errors.

- [ ] **Step 3: Start dev server and verify in browser**

Run:
```bash
make dev
```

Navigate to `http://localhost:8080`, log in, and verify:
- Home page loads and displays questions
- Session config page loads and displays question picker
- No console errors related to API calls

- [ ] **Step 4: Verify proto lint passes**

Run:
```bash
buf lint
```
Expected: No errors.
