# CreateQuestion RPC & REST Cleanup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add CreateQuestion RPC to QuestionService (AIP-133), wire the frontend to ConnectRPC, and remove all dead app-level REST code.

**Architecture:** Add `CreateQuestion` to the proto with AIP-203 field behavior annotations, implement the backend method and RPC handler, switch the frontend `useCreateQuestion` hook from REST to ConnectRPC, then delete the now-unused REST client, types, CSRF priming, and dead handler code.

**Tech Stack:** protobuf + buf, ConnectRPC (Go + TypeScript), sqlc (existing queries), TanStack Query + connect-query

**Spec:** `docs/superpowers/specs/2026-04-12-create-question-rpc-design.md`

---

### Task 1: Proto & codegen — add CreateQuestion RPC with AIP-203 annotations

**Files:**
- Modify: `buf.yaml` (add googleapis dep)
- Modify: `pb/drill/v1/question.proto`
- Regenerate: `internal/pb/drill/v1/` (Go), `web/src/pb/drill/v1/` (TypeScript)

- [ ] **Step 1: Add googleapis dependency to buf.yaml**

Add `buf.build/googleapis/googleapis` to the `deps` list in `buf.yaml`:

```yaml
deps:
  - buf.build/protocolbuffers/wellknowntypes
  - buf.build/googleapis/googleapis
```

Then update the lock file:

Run: `buf dep update`
Expected: `buf.lock` updated with the new dependency.

- [ ] **Step 2: Update question.proto with CreateQuestion RPC and AIP-203 annotations**

Replace the full contents of `pb/drill/v1/question.proto` with:

```proto
syntax = "proto3";
package drill.v1;

option go_package = "github.com/btc/drill/internal/pb/drill/v1;drillv1";

import "google/api/field_behavior.proto";
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
  rpc CreateQuestion(CreateQuestionRequest) returns (Question);
}

message CreateQuestionRequest {
  Question question = 1 [(google.api.field_behavior) = REQUIRED];
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
  string id = 1 [(google.api.field_behavior) = OUTPUT_ONLY];
  optional string user_id = 2 [(google.api.field_behavior) = OUTPUT_ONLY];
  string title = 3 [(google.api.field_behavior) = REQUIRED];
  string prompt = 4 [(google.api.field_behavior) = REQUIRED];
  Difficulty difficulty = 5;
  repeated string tags = 6;
  optional string hints = 7;
  QuestionSource source = 8 [(google.api.field_behavior) = OUTPUT_ONLY];
  google.protobuf.Timestamp create_time = 9 [(google.api.field_behavior) = OUTPUT_ONLY];
  optional string image_url = 10 [(google.api.field_behavior) = OUTPUT_ONLY];
}
```

- [ ] **Step 3: Run buf generate**

Run: `buf generate`
Expected: No errors. Generated files updated in `internal/pb/drill/v1/` and `web/src/pb/drill/v1/`.

- [ ] **Step 4: Verify generated code includes CreateQuestion**

Run: `grep -r "CreateQuestion" internal/pb/drill/v1/drillv1connect/`
Expected: `CreateQuestion` appears in the generated Connect service interface and handler.

Run: `grep "createQuestion" web/src/pb/drill/v1/question-QuestionService_connectquery.js`
Expected: `export const createQuestion = QuestionService.method.createQuestion;`

- [ ] **Step 5: Verify buf lint passes**

Run: `buf lint`
Expected: No errors.

- [ ] **Step 6: Commit**

```bash
git add buf.yaml buf.lock pb/drill/v1/question.proto internal/pb/ web/src/pb/
git commit -m "proto: add CreateQuestion RPC with AIP-203 field behavior annotations

Follows AIP-133 (Standard Create) with embedded resource. Adds
google/api/field_behavior.proto annotations to Question message:
OUTPUT_ONLY for server-assigned fields, REQUIRED for title/prompt.

Part of #152."
```

---

### Task 2: Backend — add CreateQuestion method to Backend

**Files:**
- Modify: `internal/backend/question.go`

- [ ] **Step 1: Add CreateQuestion method**

Add to `internal/backend/question.go`, below the existing `ListQuestions` method:

```go
// CreateQuestion inserts a user-created custom question and returns the
// fully-populated row. The caller provides only the writable fields; source
// is always "custom" and user_id comes from the authenticated context.
func (b *Backend) CreateQuestion(ctx context.Context, userID uuid.UUID, title, prompt, difficulty string, tags []string) (_ db.Question, err error) {
	ctx, span := tracer.Start(ctx, "Backend.CreateQuestion")
	defer func() { drilotel.End(span, err) }()

	if tags == nil {
		tags = []string{}
	}

	queries := db.New(b.pool)
	id, err := queries.InsertQuestion(ctx, db.InsertQuestionParams{
		UserID:         pgtype.UUID{Bytes: userID, Valid: true},
		Title:          title,
		Prompt:         prompt,
		Difficulty:     difficulty,
		Tags:           tags,
		Source:         "custom",
		CoachRationale: pgtype.Text{}, // NULL — not applicable for custom questions
	})
	if err != nil {
		return db.Question{}, fmt.Errorf("insert question: %w", err)
	}

	row, err := queries.GetQuestion(ctx, id)
	if err != nil {
		return db.Question{}, fmt.Errorf("get question after insert: %w", err)
	}
	return row, nil
}
```

The `pgtype` import is already used in `ListQuestions`. No new imports needed.

- [ ] **Step 2: Verify it compiles**

Run: `go build ./internal/backend/...`
Expected: No errors.

- [ ] **Step 3: Commit**

```bash
git add internal/backend/question.go
git commit -m "backend: add CreateQuestion method

Inserts a custom question and returns the full row via GetQuestion.
Source is always 'custom', user_id from caller, coach_rationale NULL.

Part of #152."
```

---

### Task 3: RPC handler — implement CreateQuestion with validation

**Files:**
- Modify: `internal/rpc/question/server.go`

- [ ] **Step 1: Add fullQuestionToProto converter**

Add to `internal/rpc/question/server.go`, below the existing `questionToProto` function:

```go
// fullQuestionToProto converts a full database Question row to a proto
// Question message. Used by CreateQuestion where GetQuestion returns all
// columns. Separate from questionToProto which handles the list-specific row
// type with fewer columns.
func fullQuestionToProto(row db.Question) *drillv1.Question {
	tags := row.Tags
	if tags == nil {
		tags = []string{}
	}

	q := &drillv1.Question{
		Id:         row.ID.String(),
		Title:      row.Title,
		Prompt:     row.Prompt,
		Difficulty: difficultyToProto(row.Difficulty),
		Tags:       tags,
		Source:     sourceToProto(row.Source),
		CreateTime: timestamppb.New(row.CreatedAt),
	}

	if row.UserID.Valid {
		uid := uuid.UUID(row.UserID.Bytes).String()
		q.UserId = &uid
	}

	if row.Hints.Valid {
		q.Hints = &row.Hints.String
	}

	if row.ImageUrl.Valid {
		q.ImageUrl = &row.ImageUrl.String
	}

	return q
}
```

- [ ] **Step 2: Add difficultyFromProto helper**

Add below `difficultyToProto`:

```go
func difficultyFromProto(d drillv1.Difficulty) string {
	switch d {
	case drillv1.Difficulty_DIFFICULTY_MEDIUM:
		return "medium"
	case drillv1.Difficulty_DIFFICULTY_HARD:
		return "hard"
	default:
		return ""
	}
}
```

- [ ] **Step 3: Add CreateQuestion handler**

Add to `internal/rpc/question/server.go`, below `ListQuestions`:

```go
// CreateQuestion creates a user-owned custom question.
// Follows AIP-133: the request embeds the resource; server assigns
// OUTPUT_ONLY fields (id, user_id, source, create_time, image_url).
func (s *Server) CreateQuestion(
	ctx context.Context,
	req *connect.Request[drillv1.CreateQuestionRequest],
) (*connect.Response[drillv1.Question], error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	q := req.Msg.Question
	if q == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("question is required"))
	}
	if q.Title == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("title is required"))
	}
	if q.Prompt == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("prompt is required"))
	}

	difficulty := q.Difficulty
	if difficulty == drillv1.Difficulty_DIFFICULTY_UNSPECIFIED {
		difficulty = drillv1.Difficulty_DIFFICULTY_MEDIUM
	}
	diffStr := difficultyFromProto(difficulty)
	if diffStr == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid difficulty"))
	}

	row, err := s.b.CreateQuestion(ctx, user.ID, q.Title, q.Prompt, diffStr, q.Tags)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("create question failed"))
	}

	return connect.NewResponse(fullQuestionToProto(row)), nil
}
```

- [ ] **Step 4: Verify it compiles**

Run: `go build ./internal/rpc/...`
Expected: No errors.

- [ ] **Step 5: Commit**

```bash
git add internal/rpc/question/server.go
git commit -m "rpc: implement CreateQuestion handler with AIP-133 validation

Validates REQUIRED fields (title, prompt), defaults DIFFICULTY_UNSPECIFIED
to MEDIUM, assigns source=custom and user_id from auth context. Returns
the full Question resource per AIP-133.

Part of #152."
```

---

### Task 4: Tests — CreateQuestion RPC handler tests

**Files:**
- Modify: `internal/rpc/question/server_test.go`

- [ ] **Step 1: Write the CreateQuestion tests**

Add to the end of `internal/rpc/question/server_test.go`:

```go
func TestCreateQuestion_Success(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startQuestionServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	resp, err := client.CreateQuestion(context.Background(), connect.NewRequest(&drillv1.CreateQuestionRequest{
		Question: &drillv1.Question{
			Title:      "Design a rate limiter",
			Prompt:     "Design a distributed rate limiter for an API gateway.",
			Difficulty: drillv1.Difficulty_DIFFICULTY_HARD,
			Tags:       []string{"distributed-systems", "scaling"},
		},
	}))
	require.NoError(t, err)

	q := resp.Msg
	require.NotEmpty(t, q.Id, "server should assign an ID")
	require.NotNil(t, q.UserId, "server should assign user_id")
	require.Equal(t, "Design a rate limiter", q.Title)
	require.Equal(t, "Design a distributed rate limiter for an API gateway.", q.Prompt)
	require.Equal(t, drillv1.Difficulty_DIFFICULTY_HARD, q.Difficulty)
	require.Equal(t, []string{"distributed-systems", "scaling"}, q.Tags)
	require.Equal(t, drillv1.QuestionSource_QUESTION_SOURCE_CUSTOM, q.Source)
	require.NotNil(t, q.CreateTime, "server should assign create_time")
}

func TestCreateQuestion_DefaultDifficulty(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startQuestionServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	resp, err := client.CreateQuestion(context.Background(), connect.NewRequest(&drillv1.CreateQuestionRequest{
		Question: &drillv1.Question{
			Title:  "Design a cache",
			Prompt: "Design a distributed cache.",
		},
	}))
	require.NoError(t, err)
	require.Equal(t, drillv1.Difficulty_DIFFICULTY_MEDIUM, resp.Msg.Difficulty)
}

func TestCreateQuestion_AppearsInList(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startQuestionServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	baseline, err := client.ListQuestions(context.Background(), connect.NewRequest(&drillv1.ListQuestionsRequest{}))
	require.NoError(t, err)
	beforeCount := len(baseline.Msg.Questions)

	_, err = client.CreateQuestion(context.Background(), connect.NewRequest(&drillv1.CreateQuestionRequest{
		Question: &drillv1.Question{
			Title:  "Design a queue",
			Prompt: "Design a message queue system.",
		},
	}))
	require.NoError(t, err)

	after, err := client.ListQuestions(context.Background(), connect.NewRequest(&drillv1.ListQuestionsRequest{}))
	require.NoError(t, err)
	require.Len(t, after.Msg.Questions, beforeCount+1)
}

func TestCreateQuestion_NilQuestion(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startQuestionServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	_, err := client.CreateQuestion(context.Background(), connect.NewRequest(&drillv1.CreateQuestionRequest{}))
	require.Error(t, err)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestCreateQuestion_EmptyTitle(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startQuestionServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	_, err := client.CreateQuestion(context.Background(), connect.NewRequest(&drillv1.CreateQuestionRequest{
		Question: &drillv1.Question{
			Title:  "",
			Prompt: "Some prompt",
		},
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestCreateQuestion_EmptyPrompt(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startQuestionServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	_, err := client.CreateQuestion(context.Background(), connect.NewRequest(&drillv1.CreateQuestionRequest{
		Question: &drillv1.Question{
			Title:  "Design something",
			Prompt: "",
		},
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestCreateQuestion_Unauthenticated(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startQuestionServer(t, b)

	client := drillv1connect.NewQuestionServiceClient(&http.Client{}, srvURL)
	_, err := client.CreateQuestion(context.Background(), connect.NewRequest(&drillv1.CreateQuestionRequest{
		Question: &drillv1.Question{
			Title:  "Design a cache",
			Prompt: "Design a distributed cache.",
		},
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}
```

- [ ] **Step 2: Run the tests to verify they pass**

Run: `go test ./internal/rpc/question/... -race -count=1 -timeout=300s -v`
Expected: All tests pass, including the new CreateQuestion tests.

- [ ] **Step 3: Commit**

```bash
git add internal/rpc/question/server_test.go
git commit -m "test: add CreateQuestion RPC handler tests

Covers happy path, default difficulty, appears-in-list, nil question,
empty title, empty prompt, and unauthenticated. All use real database
via backendtest pattern.

Part of #152."
```

---

### Task 5: Frontend — switch useCreateQuestion to ConnectRPC

**Files:**
- Modify: `web/src/api/queries.ts`
- Modify: `web/src/pages/home.tsx`

- [ ] **Step 1: Update queries.ts — replace REST hook with ConnectRPC**

In `web/src/api/queries.ts`:

Replace the entire imports section with:

```ts
import {
  createConnectQueryKey,
  useMutation as useConnectMutation,
  useQuery,
} from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";

import {
  deleteAccount as deleteAccountMethod,
  forgotPassword as forgotPasswordMethod,
  login as loginMethod,
  logout as logoutMethod,
  resetPassword as resetPasswordMethod,
  signup as signupMethod,
  verifyEmail as verifyEmailMethod,
} from "@/pb/drill/v1/auth-AuthService_connectquery";
import { EducatorStatus } from "@/pb/drill/v1/educator_pb";
import {
  getEducatorAnalysis,
  requestEducatorAnalysis as requestEducatorMethod,
} from "@/pb/drill/v1/educator-EducatorService_connectquery";
import {
  getEvaluation,
  retryEvaluation as retryEvaluationMethod,
} from "@/pb/drill/v1/evaluation-EvaluationService_connectquery";
import {
  createQuestion as createQuestionMethod,
  listQuestions,
} from "@/pb/drill/v1/question-QuestionService_connectquery";
import {
  getSession,
  getTranscript,
} from "@/pb/drill/v1/session-SessionService_connectquery";
import { getMe } from "@/pb/drill/v1/user-UserService_connectquery";
```

Replace the `useCreateQuestion` function:

```ts
// --- Questions ---

export function useCreateQuestion() {
  const qc = useQueryClient();
  return useConnectMutation(createQuestionMethod, {
    onSuccess: () =>
      qc.invalidateQueries({
        queryKey: createConnectQueryKey({ schema: listQuestions, input: {}, cardinality: undefined }),
      }),
  });
}
```

- [ ] **Step 2: Update home.tsx — map difficulty to proto enum**

In `web/src/pages/home.tsx`, add the Difficulty import near the top with the other proto imports:

```ts
import { Difficulty } from "@/pb/drill/v1/question_pb";
```

Then update the `handleSubmit` function in `CreateQuestionDialog`. Replace:

```ts
    createQuestion.mutate(
      { title, prompt, difficulty, tags },
      {
```

With:

```ts
    const difficultyEnum = difficulty === "hard"
      ? Difficulty.HARD
      : Difficulty.MEDIUM;
    createQuestion.mutate(
      { question: { title, prompt, difficulty: difficultyEnum, tags } },
      {
```

- [ ] **Step 3: Verify frontend typechecks**

Run: `cd web && npx tsc -b`
Expected: No errors.

- [ ] **Step 4: Verify frontend lint passes**

Run: `cd web && npx eslint src/api/queries.ts src/pages/home.tsx`
Expected: No errors (or only pre-existing warnings).

- [ ] **Step 5: Commit**

```bash
git add web/src/api/queries.ts web/src/pages/home.tsx
git commit -m "frontend: switch useCreateQuestion from REST to ConnectRPC

Replaces apiClient.post('/api/questions', ...) with useConnectMutation
using the generated createQuestion method. Maps difficulty string to
proto Difficulty enum. Invalidates listQuestions on success.

Closes #152."
```

---

### Task 6: Dead code removal — REST client, types, CSRF, handler stubs

**Files:**
- Delete: `web/src/api/client.ts`
- Delete: `web/src/api/types.ts`
- Delete: `web/src/api/__tests__/client.test.ts`
- Delete: `internal/handler/auth.go`
- Modify: `web/src/main.tsx`
- Modify: `web/src/vite-env.d.ts`
- Modify: `internal/handler/billing.go`
- Modify: `CLAUDE.md`

- [ ] **Step 1: Delete dead frontend files**

```bash
rm web/src/api/client.ts
rm web/src/api/types.ts
rm -rf web/src/api/__tests__/
```

- [ ] **Step 2: Delete dead backend handler**

```bash
rm internal/handler/auth.go
```

- [ ] **Step 3: Remove CSRF token priming from main.tsx**

In `web/src/main.tsx`, remove these lines:

```ts
// Prime CSRF token — Gorilla CSRF sets the cookie on every response and
// exposes the masked token via the X-CSRF-Token response header. This GET
// ensures the token is available before any POST.
fetch("/api/health", { credentials: "same-origin" }).then((res) => {
  const token = res.headers.get("X-CSRF-Token");
  if (token) window.__csrfToken = token;
});
```

- [ ] **Step 4: Remove __csrfToken type declaration from vite-env.d.ts**

Replace the contents of `web/src/vite-env.d.ts` with:

```ts
/// <reference types="vite/client" />
```

- [ ] **Step 5: Remove migration comment from billing.go**

In `internal/handler/billing.go`, remove the comment on line 13:

```go
// (PostCheckout and PostPortal migrated to ConnectRPC BillingService)
```

- [ ] **Step 6: Update CLAUDE.md — remove AIP-203 skip, update migration status**

In `CLAUDE.md`, replace:

```
ConnectRPC services follow Google AIPs where practical. Standard methods use AIP naming, pagination (AIP-158), error codes (AIP-193). Custom methods use AIP-136. Skip resource names (AIP-122) and field behavior annotations (AIP-203).
```

With:

```
ConnectRPC services follow Google AIPs where practical. Standard methods use AIP naming, pagination (AIP-158), error codes (AIP-193), field behavior annotations (AIP-203). Custom methods use AIP-136. Skip resource names (AIP-122).
```

Also replace:

```
ConnectRPC handlers live in `internal/rpc/{service}/`. REST handlers in `internal/handler/` are being incrementally migrated. New endpoints should be ConnectRPC services.
```

With:

```
ConnectRPC handlers live in `internal/rpc/{service}/`. REST handlers in `internal/handler/` are limited to health checks, OAuth flows, and Stripe webhooks — endpoints that are inherently HTTP-level. All resource RPCs use ConnectRPC.
```

- [ ] **Step 7: Verify frontend typechecks and lint**

Run: `cd web && npx tsc -b`
Expected: No errors.

Run: `cd web && npx eslint src/`
Expected: No new errors.

- [ ] **Step 8: Verify backend compiles**

Run: `go build ./internal/...`
Expected: No errors (auth.go removal doesn't break anything — functions were unused).

- [ ] **Step 9: Commit**

```bash
git add -u web/src/api/client.ts web/src/api/types.ts web/src/api/__tests__/client.test.ts internal/handler/auth.go
git add web/src/main.tsx web/src/vite-env.d.ts internal/handler/billing.go CLAUDE.md
git commit -m "chore: remove dead app-level REST code

Delete apiClient, types.ts, client.test.ts, handler/auth.go (unused
writeJSON/writePaidBalanceRequired). Remove CSRF token priming from
main.tsx and __csrfToken type. Update CLAUDE.md to reflect AIP-203
adoption and completed REST migration.

Part of #152."
```

---

### Task 7: Full verification

- [ ] **Step 1: Run full CI**

Run: `make test`
Expected: All checks pass — buf lint, codegen check, frontend typecheck+lint+tests, backend tests.

- [ ] **Step 2: Verify no remaining apiClient references**

Run: `grep -rn "apiClient" web/src/ --include="*.ts" --include="*.tsx"`
Expected: No output (all references removed).

- [ ] **Step 3: Verify no remaining types.ts references**

Run: `grep -rn "from.*api/types\|from.*./types" web/src/api/ --include="*.ts"`
Expected: No output.

- [ ] **Step 4: Start local dev and verify in browser**

Run: `make dev`

Navigate to the home page. Click the "+ New question" card. Fill in:
- Title: "Test question"
- Prompt: "Test prompt"
- Difficulty: Medium
- Tags: "test, verification"

Click Create. Verify the dialog closes and the new question appears in the grid.

- [ ] **Step 5: Commit any fixups if needed**

If any issues were found, fix and commit with a descriptive message.
