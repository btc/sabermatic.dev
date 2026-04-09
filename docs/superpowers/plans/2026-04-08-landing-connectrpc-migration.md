# Landing Page ConnectRPC Migration

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Migrate the `feature/growth-landing` branch from REST API hooks to ConnectRPC for all endpoints, including a new `SampleService` for public fixture data.

**Architecture:** New `SampleService` proto with 4 RPCs serving embedded JSON fixtures via `protojson.Unmarshal`. Frontend `queries.ts` restored to ConnectRPC hooks. All REST types replaced with proto imports. Fixture JSON rewritten to match protojson field naming.

**Tech Stack:** Protocol Buffers, ConnectRPC (Go + TypeScript), buf generate, TanStack Query, `@connectrpc/connect-query`

**Working directory:** `/Users/btc/Projects/src/drill/.worktrees/growth-landing`

---

### Task 1: Proto — Define SampleService

**Files:**
- Create: `pb/drill/v1/sample.proto`

- [ ] **Step 1: Create the proto file**

```protobuf
syntax = "proto3";
package drill.v1;

option go_package = "github.com/btc/drill/internal/pb/drill/v1;drillv1";

import "drill/v1/session.proto";
import "drill/v1/evaluation.proto";
import "drill/v1/educator.proto";
import "drill/v1/coach.proto";

service SampleService {
  rpc GetSampleSession(GetSampleSessionRequest) returns (GetSampleSessionResponse);
  rpc GetSampleEvaluation(GetSampleEvaluationRequest) returns (GetEvaluationResponse);
  rpc GetSampleEducator(GetSampleEducatorRequest) returns (GetEducatorAnalysisResponse);
  rpc GetSampleCoach(GetSampleCoachRequest) returns (GetSampleCoachResponse);
}

message GetSampleSessionRequest {}

message GetSampleSessionResponse {
  Session session = 1;
  repeated Message messages = 2;
}

message GetSampleEvaluationRequest {}

// Reuses GetEvaluationResponse from evaluation.proto

message GetSampleEducatorRequest {}

// Reuses GetEducatorAnalysisResponse from educator.proto

message GetSampleCoachRequest {}

message GetSampleCoachResponse {
  CoachAnalysis analysis = 1;
  repeated ScoreTrendPoint score_trend = 2;
}

message ScoreTrendPoint {
  string date = 1;
  int32 overall_score = 2;
}
```

- [ ] **Step 2: Run buf generate**

Run: `buf generate`
Expected: Generated files appear in `internal/pb/drill/v1/` (Go) and `web/src/pb/drill/v1/` (TypeScript)

- [ ] **Step 3: Verify generated files exist**

Run: `ls internal/pb/drill/v1/sample* && ls web/src/pb/drill/v1/sample*`
Expected: `sample.pb.go`, `drillv1connect/sample.connect.go`, `sample_pb.ts`, `sample-SampleService_connectquery.ts` (or similar)

- [ ] **Step 4: Commit**

```bash
git add pb/drill/v1/sample.proto internal/pb/ web/src/pb/
git commit -m "feat: add SampleService proto definition"
```

---

### Task 2: Rewrite fixture JSON to protojson format

**Files:**
- Modify: `internal/sample/fixtures/session.json`
- Modify: `internal/sample/fixtures/evaluation.json`
- Modify: `internal/sample/fixtures/educator.json`
- Modify: `internal/sample/fixtures/coach.json`

The current fixtures use REST-style field names that don't match proto. `protojson.Unmarshal` requires proto field names. Key changes per fixture:

**session.json** — currently `{ "session": {...}, "messages": [...] }`. This maps to `GetSampleSessionResponse`. Field changes inside `session`:
- `started_at` → `start_time` (RFC3339 string — protojson accepts this for Timestamp)
- `ended_at` → `end_time`
- `created_at` → `create_time`
- `updated_at` → `update_time`
- `archived` (bool) → remove (use `archive_time` only if archived, omit if not)
- `status: "reviewed"` → `status: "SESSION_STATUS_REVIEWED"`
- `question_title` stays (already matches proto field name)

Inside each message in `messages`:
- `created_at` → `create_time`

**evaluation.json** — currently a flat `EvaluationResult`. Needs wrapping in `{ "evaluation": {...} }` to match `GetEvaluationResponse`. Field changes:
- `status: "reviewed"` → `status: "EVALUATION_STATUS_REVIEWED"`
- Annotation `type: "strength"` → `type: "ANNOTATION_TYPE_STRENGTH"` (etc. for gap, missed_opportunity, note)
- `deep_dive` stays (already matches proto field name)

**educator.json** — currently a flat `EducatorAnalysis`. Needs wrapping in `{ "analysis": {...} }` to match `GetEducatorAnalysisResponse`. Field changes:
- `status: "completed"` → `status: "EDUCATOR_STATUS_COMPLETED"`
- `created_at` → `create_time`

**coach.json** — currently a flat object with `score_trend` mixed in. Needs restructuring to `{ "analysis": {...}, "score_trend": [...] }` to match `GetSampleCoachResponse`. Field changes inside `analysis`:
- `created_at` → `create_time`
- Move `score_trend` to top level (sibling of `analysis`, not inside it)

- [ ] **Step 1: Rewrite session.json**

Read the current file. Apply the field renames listed above to every `session` field and every `message` in the `messages` array. The top-level structure `{ "session": {...}, "messages": [...] }` stays.

- [ ] **Step 2: Rewrite evaluation.json**

Read the current file. Wrap the entire content in `{ "evaluation": {...} }`. Update `status` enum string. Update all annotation `type` values to proto enum strings (`ANNOTATION_TYPE_STRENGTH`, `ANNOTATION_TYPE_GAP`, `ANNOTATION_TYPE_MISSED_OPPORTUNITY`, `ANNOTATION_TYPE_NOTE`).

- [ ] **Step 3: Rewrite educator.json**

Read the current file. Wrap in `{ "analysis": {...} }`. Rename `created_at` → `create_time`. Update `status` → `EDUCATOR_STATUS_COMPLETED`.

- [ ] **Step 4: Rewrite coach.json**

Read the current file. Restructure to `{ "analysis": { <all fields except score_trend> }, "score_trend": [...] }`. Rename `created_at` → `create_time` inside analysis.

- [ ] **Step 5: Commit**

```bash
git add internal/sample/fixtures/
git commit -m "fix: rewrite fixture JSON to protojson format"
```

---

### Task 3: Backend — SampleService handler

**Files:**
- Create: `internal/rpc/sample/server.go`
- Modify: `internal/rpc/register.go`
- Modify: `internal/handler/routes.go`
- Delete: `internal/sample/handler.go`

- [ ] **Step 1: Create `internal/rpc/sample/server.go`**

```go
package sample

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/btc/drill/internal/pb/drill/v1/drillv1connect"
	drillv1 "github.com/btc/drill/internal/pb/drill/v1"
	"github.com/btc/drill/internal/sample"
)

// SampleService holds pre-parsed sample fixture data.
type SampleService struct {
	sessionResp    *drillv1.GetSampleSessionResponse
	evaluationResp *drillv1.GetEvaluationResponse
	educatorResp   *drillv1.GetEducatorAnalysisResponse
	coachResp      *drillv1.GetSampleCoachResponse
}

// NewSampleService reads and unmarshals embedded fixture JSON into proto messages.
func NewSampleService() (*SampleService, error) {
	ss := &SampleService{}

	sessionData, err := sample.FixtureFS().ReadFile("fixtures/session.json")
	if err != nil {
		return nil, fmt.Errorf("read session fixture: %w", err)
	}
	ss.sessionResp = &drillv1.GetSampleSessionResponse{}
	if err := protojson.Unmarshal(sessionData, ss.sessionResp); err != nil {
		return nil, fmt.Errorf("unmarshal session fixture: %w", err)
	}

	evalData, err := sample.FixtureFS().ReadFile("fixtures/evaluation.json")
	if err != nil {
		return nil, fmt.Errorf("read evaluation fixture: %w", err)
	}
	ss.evaluationResp = &drillv1.GetEvaluationResponse{}
	if err := protojson.Unmarshal(evalData, ss.evaluationResp); err != nil {
		return nil, fmt.Errorf("unmarshal evaluation fixture: %w", err)
	}

	educatorData, err := sample.FixtureFS().ReadFile("fixtures/educator.json")
	if err != nil {
		return nil, fmt.Errorf("read educator fixture: %w", err)
	}
	ss.educatorResp = &drillv1.GetEducatorAnalysisResponse{}
	if err := protojson.Unmarshal(educatorData, ss.educatorResp); err != nil {
		return nil, fmt.Errorf("unmarshal educator fixture: %w", err)
	}

	coachData, err := sample.FixtureFS().ReadFile("fixtures/coach.json")
	if err != nil {
		return nil, fmt.Errorf("read coach fixture: %w", err)
	}
	ss.coachResp = &drillv1.GetSampleCoachResponse{}
	if err := protojson.Unmarshal(coachData, ss.coachResp); err != nil {
		return nil, fmt.Errorf("unmarshal coach fixture: %w", err)
	}

	return ss, nil
}

// Server implements the SampleService Connect handler.
type Server struct {
	ss *SampleService
}

var _ drillv1connect.SampleServiceHandler = (*Server)(nil)

// NewServer creates a new SampleService handler.
func NewServer(ss *SampleService) *Server {
	return &Server{ss: ss}
}

func (s *Server) GetSampleSession(
	ctx context.Context,
	req *connect.Request[drillv1.GetSampleSessionRequest],
) (*connect.Response[drillv1.GetSampleSessionResponse], error) {
	return connect.NewResponse(s.ss.sessionResp), nil
}

func (s *Server) GetSampleEvaluation(
	ctx context.Context,
	req *connect.Request[drillv1.GetSampleEvaluationRequest],
) (*connect.Response[drillv1.GetEvaluationResponse], error) {
	return connect.NewResponse(s.ss.evaluationResp), nil
}

func (s *Server) GetSampleEducator(
	ctx context.Context,
	req *connect.Request[drillv1.GetSampleEducatorRequest],
) (*connect.Response[drillv1.GetEducatorAnalysisResponse], error) {
	return connect.NewResponse(s.ss.educatorResp), nil
}

func (s *Server) GetSampleCoach(
	ctx context.Context,
	req *connect.Request[drillv1.GetSampleCoachRequest],
) (*connect.Response[drillv1.GetSampleCoachResponse], error) {
	return connect.NewResponse(s.ss.coachResp), nil
}
```

Note: The `sample` package needs to export its `embed.FS`. Currently it's unexported (`var fixtureFS embed.FS`). Either:
- Export it: rename to `FixtureFS` and add a getter function
- Or move `embed.go` into `internal/rpc/sample/`

Choose whichever is cleaner. If moving, update the embed path and delete `internal/sample/embed.go`.

- [ ] **Step 2: Export the fixture FS from `internal/sample/embed.go` and document provenance**

Replace with an exported accessor and add provenance comment:

```go
package sample

import "embed"

// Fixture data extracted from v0 prototype, session 27 ("Chat System").
// Text-only — no audio files (all audio_url fields are null).
// To regenerate: run a session in v0, export via extract-sample script.
//
//go:embed fixtures/*.json
var fixtureFS embed.FS

// FixtureFS returns the embedded fixture filesystem.
func FixtureFS() embed.FS {
	return fixtureFS
}
```

- [ ] **Step 3: Delete `internal/sample/handler.go`**

Remove the REST handler file entirely.

- [ ] **Step 4: Update `internal/rpc/register.go`**

Add import:
```go
samplesvc "github.com/btc/drill/internal/rpc/sample"
```

Add `SampleService` parameter to `Register`:
```go
func Register(mux *http.ServeMux, b *backend.Backend, ss *samplesvc.SampleService) error {
```

Add registration with `publicOpts` (no auth), after the AuthService line:
```go
mux.Handle(drillv1connect.NewSampleServiceHandler(samplesvc.NewServer(ss), publicOpts))
```

Add to `ConnectPathPrefixes`:
```go
drillv1connect.SampleServiceName,
```

- [ ] **Step 5: Update `internal/handler/routes.go`**

Remove `sample.RegisterRoutes(mux)` call and the `"github.com/btc/drill/internal/sample"` import.

Update `RegisterRoutes` signature to accept the sample service parameter and pass it through to `rpc.Register`. Alternatively, if `Register` already receives it via the caller chain, just remove the `sample.RegisterRoutes` call.

The `RegisterRoutes` caller in `NewHandler` needs updating too — trace the call chain from `cmd/drill/main.go` and thread the `*SampleService` through.

- [ ] **Step 6: Update `cmd/drill/main.go`**

Before creating the handler, construct the `SampleService`:

```go
ss, err := samplesvc.NewSampleService()
if err != nil {
    return fmt.Errorf("create sample service: %w", err)
}
```

Pass `ss` through to where `rpc.Register` is called.

- [ ] **Step 7: Verify Go builds**

Run: `go build ./...`
Expected: Clean build, no errors.

- [ ] **Step 8: Commit**

```bash
git add internal/rpc/sample/ internal/rpc/register.go internal/handler/routes.go internal/sample/ cmd/drill/main.go
git commit -m "feat: add SampleService ConnectRPC handler, remove REST sample endpoints"
```

---

### Task 4: Backend tests

**Files:**
- Modify: `internal/sample/handler_test.go` (rewrite or move to `internal/rpc/sample/server_test.go`)

- [ ] **Step 1: Write ConnectRPC sample service tests**

Create `internal/rpc/sample/server_test.go`. Test that each RPC returns non-nil data with the expected structure:

```go
package sample_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	drillv1 "github.com/btc/drill/internal/pb/drill/v1"
	"github.com/btc/drill/internal/pb/drill/v1/drillv1connect"
	samplesvc "github.com/btc/drill/internal/rpc/sample"
)

func setup(t *testing.T) drillv1connect.SampleServiceClient {
	t.Helper()
	ss, err := samplesvc.NewSampleService()
	require.NoError(t, err)

	mux := http.NewServeMux()
	mux.Handle(drillv1connect.NewSampleServiceHandler(samplesvc.NewServer(ss)))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return drillv1connect.NewSampleServiceClient(
		srv.Client(),
		srv.URL,
	)
}

func TestGetSampleSession(t *testing.T) {
	client := setup(t)
	resp, err := client.GetSampleSession(context.Background(),
		connect.NewRequest(&drillv1.GetSampleSessionRequest{}))
	require.NoError(t, err)
	require.NotNil(t, resp.Msg.Session)
	require.NotEmpty(t, resp.Msg.Messages)
	require.Equal(t, "Chat System", resp.Msg.Session.QuestionTitle)
}

func TestGetSampleEvaluation(t *testing.T) {
	client := setup(t)
	resp, err := client.GetSampleEvaluation(context.Background(),
		connect.NewRequest(&drillv1.GetSampleEvaluationRequest{}))
	require.NoError(t, err)
	require.NotNil(t, resp.Msg.Evaluation)
	require.NotNil(t, resp.Msg.Evaluation.Scores)
	require.NotEmpty(t, resp.Msg.Evaluation.Strengths)
	require.NotEmpty(t, resp.Msg.Evaluation.Annotations)
}

func TestGetSampleEducator(t *testing.T) {
	client := setup(t)
	resp, err := client.GetSampleEducator(context.Background(),
		connect.NewRequest(&drillv1.GetSampleEducatorRequest{}))
	require.NoError(t, err)
	require.NotNil(t, resp.Msg.Analysis)
	require.NotNil(t, resp.Msg.Analysis.ModelAnswer)
}

func TestGetSampleCoach(t *testing.T) {
	client := setup(t)
	resp, err := client.GetSampleCoach(context.Background(),
		connect.NewRequest(&drillv1.GetSampleCoachRequest{}))
	require.NoError(t, err)
	require.NotNil(t, resp.Msg.Analysis)
	require.NotEmpty(t, resp.Msg.ScoreTrend)
}
```

- [ ] **Step 2: Delete old REST test**

Remove `internal/sample/handler_test.go`.

- [ ] **Step 3: Run tests**

Run: `go test ./internal/rpc/sample/...`
Expected: All 4 tests pass.

- [ ] **Step 4: Commit**

```bash
git add internal/rpc/sample/server_test.go
git rm internal/sample/handler_test.go
git commit -m "test: add SampleService ConnectRPC tests, remove REST tests"
```

---

### Task 5: Frontend — Restore `queries.ts` to ConnectRPC

**Files:**
- Modify: `web/src/api/queries.ts`

- [ ] **Step 1: Rewrite `queries.ts`**

Replace the entire file. Restore main's ConnectRPC auth mutations and add ConnectRPC wrappers for session/evaluation/educator/coach queries.

```typescript
import { useQuery, useMutation, createConnectQueryKey } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { getMe } from "@/pb/drill/v1/user-UserService_connectquery";
import {
  login as loginMethod,
  signup as signupMethod,
  logout as logoutMethod,
  forgotPassword as forgotPasswordMethod,
  resetPassword as resetPasswordMethod,
  verifyEmail as verifyEmailMethod,
  deleteAccount as deleteAccountMethod,
} from "@/pb/drill/v1/auth-AuthService_connectquery";
import {
  getSession,
  getTranscript,
} from "@/pb/drill/v1/session-SessionService_connectquery";
import {
  getEvaluation,
  retryEvaluation as retryEvaluationMethod,
} from "@/pb/drill/v1/evaluation-EvaluationService_connectquery";
import {
  getEducatorAnalysis,
  requestEducatorAnalysis as requestEducatorMethod,
} from "@/pb/drill/v1/educator-EducatorService_connectquery";
import { EducatorStatus } from "@/pb/drill/v1/educator_pb";
import { EducatorService } from "@/pb/drill/v1/educator_pb";
import { apiClient } from "./client";
import type { Question } from "./types";

// --- Questions (still REST until QuestionService gets CreateQuestion) ---

export function useCreateQuestion() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: { title: string; prompt: string; difficulty: string; tags: string[] }) =>
      apiClient.post<Question>("/api/questions", data),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["questions"] }),
  });
}

// --- User ---

export function useMe(options?: { enabled?: boolean }) {
  return useQuery(getMe, {}, {
    retry: false,
    enabled: options?.enabled,
  });
}

// --- Session detail queries ---

export function useSession(id: string, options?: { enabled?: boolean }) {
  return useQuery(getSession, { id }, {
    enabled: options?.enabled ?? !!id,
  });
}

export function useTranscript(sessionId: string, enabled?: boolean) {
  return useQuery(getTranscript, { sessionId }, { enabled });
}

export function useEvaluation(sessionId: string, enabled?: boolean) {
  return useQuery(getEvaluation, { sessionId }, { enabled });
}

export function useEducator(sessionId: string, enabled?: boolean) {
  return useQuery(getEducatorAnalysis, { sessionId }, {
    enabled,
    refetchInterval: (query) => {
      const data = query.state.data;
      if (data?.analysis?.status === EducatorStatus.GENERATING) return 3000;
      return false;
    },
  });
}

export function useRetryEvaluation(sessionId: string) {
  const qc = useQueryClient();
  return useMutation(retryEvaluationMethod, {
    onSuccess: () => qc.invalidateQueries({
      queryKey: createConnectQueryKey({ schema: getEvaluation, input: { sessionId }, cardinality: undefined }),
    }),
  });
}

export function useRequestEducator(sessionId: string) {
  const qc = useQueryClient();
  return useMutation(requestEducatorMethod, {
    onSuccess: () => qc.invalidateQueries({
      queryKey: createConnectQueryKey({ schema: getEducatorAnalysis, input: { sessionId }, cardinality: undefined }),
    }),
  });
}

// --- Auth ---

export function useLogin() {
  const qc = useQueryClient();
  return useMutation(loginMethod, {
    onSuccess: () => qc.invalidateQueries({ queryKey: createConnectQueryKey({ schema: getMe, input: {}, cardinality: undefined }) }),
  });
}

export function useSignup() {
  return useMutation(signupMethod);
}

export function useLogout() {
  const qc = useQueryClient();
  return useMutation(logoutMethod, {
    onSuccess: () => qc.clear(),
  });
}

export function useForgotPassword() {
  return useMutation(forgotPasswordMethod);
}

export function useResetPassword() {
  return useMutation(resetPasswordMethod);
}

export function useVerifyEmail() {
  return useMutation(verifyEmailMethod);
}

export function useDeleteAccount() {
  return useMutation(deleteAccountMethod);
}
```

Note: The exact `useMutation` and `useQuery` signatures depend on the `@connectrpc/connect-query` version installed. Verify against the actual installed package API before finalizing. The import paths for connectquery hooks follow the pattern `@/pb/drill/v1/{service}-{ServiceName}_connectquery`.

- [ ] **Step 2: Commit**

```bash
git add web/src/api/queries.ts
git commit -m "fix: restore queries.ts to ConnectRPC hooks"
```

---

### Task 6: Frontend — Rewrite `sample-queries.ts` to ConnectRPC

**Files:**
- Modify: `web/src/api/sample-queries.ts`

- [ ] **Step 1: Rewrite to ConnectRPC**

```typescript
import { useQuery } from "@connectrpc/connect-query";
import {
  getSampleSession,
  getSampleEvaluation,
  getSampleEducator,
  getSampleCoach,
} from "@/pb/drill/v1/sample-SampleService_connectquery";

export interface SampleQueryOptions {
  enabled?: boolean;
}

export function useSampleSession(options?: SampleQueryOptions) {
  return useQuery(getSampleSession, {}, {
    staleTime: Infinity,
    enabled: options?.enabled,
  });
}

export function useSampleEvaluation(options?: SampleQueryOptions) {
  return useQuery(getSampleEvaluation, {}, {
    staleTime: Infinity,
    enabled: options?.enabled,
  });
}

export function useSampleEducator(options?: SampleQueryOptions) {
  return useQuery(getSampleEducator, {}, {
    staleTime: Infinity,
    enabled: options?.enabled,
  });
}

export function useSampleCoach(options?: SampleQueryOptions) {
  return useQuery(getSampleCoach, {}, {
    staleTime: Infinity,
    enabled: options?.enabled,
  });
}
```

Note: Verify the actual generated import path for `sample-SampleService_connectquery` matches what `buf generate` produced in Task 1.

- [ ] **Step 2: Commit**

```bash
git add web/src/api/sample-queries.ts
git commit -m "fix: rewrite sample-queries.ts to ConnectRPC"
```

---

### Task 7: Frontend — Fix `use-auth.ts`

**Files:**
- Modify: `web/src/hooks/use-auth.ts`

- [ ] **Step 1: Rewrite to use ConnectRPC for both hooks**

```typescript
import { useQuery } from "@connectrpc/connect-query";
import { ConnectError, Code } from "@connectrpc/connect";
import { getMe } from "@/pb/drill/v1/user-UserService_connectquery";
import { useNavigate, useLocation } from "react-router-dom";
import { useEffect } from "react";

/**
 * Checks auth state without redirecting. For routes that render
 * different content based on auth (e.g., landing page vs dashboard).
 * Distinguishes unauthenticated from server error.
 */
export function useOptionalAuth() {
  const { data, isLoading, error } = useQuery(getMe, {}, { retry: false });
  const user = data?.user;
  const isAuthError = error instanceof ConnectError && error.code === Code.Unauthenticated;
  return { user, isLoading, isAuthenticated: !!user, isAuthError };
}

export function useRequireAuth() {
  const { data, isLoading, isError } = useQuery(getMe, {});
  const user = data?.user;
  const navigate = useNavigate();
  const location = useLocation();

  useEffect(() => {
    if (!isLoading && isError) {
      navigate(`/login?redirect=${encodeURIComponent(location.pathname)}`, {
        replace: true,
      });
    }
  }, [isLoading, isError, navigate, location.pathname]);

  return { user, isLoading, isAuthenticated: !!user };
}
```

This removes the `ApiError` import and the `useMe` import from `@/api/queries`. Both hooks now use the same ConnectRPC `getMe` query.

- [ ] **Step 2: Commit**

```bash
git add web/src/hooks/use-auth.ts
git commit -m "fix: use ConnectRPC for useOptionalAuth"
```

---

### Task 8: Frontend — Strip REST types from `types.ts`

**Files:**
- Modify: `web/src/api/types.ts`

- [ ] **Step 1: Strip to Question only**

```typescript
// --- Questions ---
export type Difficulty = "medium" | "hard";
export type QuestionSource = "seed" | "custom" | "coach_generated";

export interface Question {
  id: string;
  user_id: string | null;
  title: string;
  prompt: string;
  difficulty: Difficulty;
  tags: string[];
  hints: string | null;
  source: QuestionSource;
  coach_rationale: string | null;
  created_at: string;
  attempt_count?: number;
  best_score?: number | null;
}
```

All other types (User, Session, Message, EvaluationResponse, EvaluationScores, AnnotationType, AnnotationResponse, EducatorAnalysis, CoachAnalysis, Usage, Grant, LedgerEntry) are removed. Consumers will import from proto `*_pb` files instead.

- [ ] **Step 2: Commit**

```bash
git add web/src/api/types.ts
git commit -m "fix: strip REST types from types.ts, keep Question only"
```

---

### Task 9: Frontend — Update session detail pages to proto types

**Files:**
- Modify: `web/src/pages/session/overview.tsx`
- Modify: `web/src/pages/session/transcript.tsx`
- Modify: `web/src/pages/session/deep-dive.tsx`

These pages use the `dataSource` context from `layout.tsx`. When `dataSource === "api"`, they call ConnectRPC hooks from `queries.ts`. When `dataSource === "sample"`, they call ConnectRPC hooks from `sample-queries.ts`. Both now return proto types.

**Key changes across all three files:**

1. **Import proto types** instead of REST types:
   - `EvaluationScores` from `@/pb/drill/v1/evaluation_pb`
   - `Annotation`, `AnnotationType` from `@/pb/drill/v1/evaluation_pb`
   - `Message` from `@/pb/drill/v1/session_pb`
   - `SessionStatus` from `@/pb/drill/v1/session_pb`
   - `EducatorStatus` from `@/pb/drill/v1/educator_pb`
   - `UserPlan` from `@/pb/drill/v1/user_pb`

2. **Unwrap ConnectRPC responses:**
   - `useSession()` → `data?.session` (was `data` directly)
   - `useEvaluation()` → `data?.evaluation` (was `data` directly)
   - `useEducator()` → `data?.analysis` (was `data` directly)
   - `useTranscript()` → `data?.messages` (was `data` directly)
   - `useSampleSession()` → `data?.session` and `data?.messages`
   - `useSampleEvaluation()` → `data?.evaluation`
   - `useSampleEducator()` → `data?.analysis`

3. **Field name changes** (camelCase proto fields):
   - `message_seq` → `messageSeq`
   - `model_answer` → `modelAnswer`
   - `gap_deep_dives` → `gapDeepDives`
   - `deep_dive` → `deepDive` (in EvaluationScores)
   - `started_at` → `startTime` (Timestamp object, not string)
   - `ended_at` → `endTime`
   - `created_at` → `createTime`
   - `question_title` → `questionTitle`
   - `config_duration_minutes` → `configDurationMinutes`

4. **Enum changes:**
   - `"evaluation_failed"` → `SessionStatus.EVALUATION_FAILED` (numeric enum)
   - `"reviewed"` → `SessionStatus.REVIEWED`
   - `"generating"` → `EducatorStatus.GENERATING`
   - `"completed"` → `EducatorStatus.COMPLETED`
   - `"failed"` → `EducatorStatus.FAILED`
   - String annotation types (`"strength"`, `"gap"`, etc.) → `AnnotationType.STRENGTH`, `AnnotationType.GAP`, etc.

5. **Timestamp handling:** Proto timestamps are `Timestamp` objects with `.toDate()` method, not strings. The replay engine and any date parsing needs updating.

6. **Mutation call signatures change:**
   - `useRetryEvaluation`: `retryEvaluation.mutate()` → `retryEvaluation.mutate({ sessionId })`
   - `useRequestEducator`: `requestEducator.mutate()` → `requestEducator.mutate({ sessionId })`

7. **`useMe` response shape change:**
   - `data: me` → `data?.user` (ConnectRPC wraps in `GetMeResponse`)
   - `me?.plan === "pro"` → `me?.plan === UserPlan.PRO`

- [ ] **Step 1: Update `overview.tsx`**

Read the current file. Apply all changes listed above. Key areas:
- Import `SessionStatus` from `@/pb/drill/v1/session_pb`, `EvaluationScores` from `@/pb/drill/v1/evaluation_pb`
- Remove `@/api/types` import
- `OverviewInner`: unwrap responses (`authSession.data?.session`, `authEval.data?.evaluation`, `sampleSession.data?.session`, `sampleEval.data?.evaluation`)
- `session.status === "evaluation_failed"` → `session.status === SessionStatus.EVALUATION_FAILED`
- `session.status !== "reviewed"` → `session.status !== SessionStatus.REVIEWED`
- `retryEvaluation.mutate()` → `retryEvaluation.mutate({ sessionId })`
- `EvaluationScores` type in `DIMENSION_LABELS`: the `Omit` type needs updating since proto `EvaluationScores` uses `deepDive` not `deep_dive`, and has `$typeName`/`$unknown` fields to omit
- `scores[dim.key]` lookups: update `DIMENSIONS` keys to camelCase (`deepDive` not `deep_dive`)
- `InsightCard` `sessionId` prop: no change needed (already a string)
- `question_title` → `questionTitle`, `config_duration_minutes` → `configDurationMinutes`

- [ ] **Step 2: Update `transcript.tsx`**

Read the current file. Key areas:
- Replace `import type { AnnotationType, AnnotationResponse, Message } from "@/api/types"` with proto imports
- `AnnotationType` becomes a numeric enum — `ANNOTATION_LABELS`, `ANNOTATION_BORDER`, `ANNOTATION_TEXT`, `ANNOTATION_BG` records need keys updated from strings to enum values (e.g., `[AnnotationType.STRENGTH]: "strengths"`)
- `ANNOTATION_TYPES` array: update to proto enum values
- `FilterType`: update from `"all" | AnnotationType` (string) to `"all" | AnnotationType` (number)
- `AnnotationResponse` → `Annotation` (proto type)
- `annotation.type.replace("_", " ")` display logic needs updating for numeric enums — use a display name map instead
- `message_seq` → `messageSeq` throughout
- `Message` type comes from `@/pb/drill/v1/session_pb`
- Unwrap responses: `authTranscript.data?.messages`, `authEval.data?.evaluation`, `sampleSession.data?.messages`, `sampleEval.data?.evaluation`
- `evaluation?.annotations` stays (proto field name is the same)
- Timestamp fields: `started_at`/`ended_at` → `startTime`/`endTime` (Timestamp objects)

- [ ] **Step 3: Update `deep-dive.tsx`**

Read the current file. Key areas:
- Replace `useEducator`, `useRequestEducator`, `useMe` imports — these stay from `@/api/queries` (which now returns ConnectRPC data)
- Unwrap responses: `authEducator.data?.analysis`, `sampleEducator.data?.analysis`
- `educator.status === "generating"` → `educator.status === EducatorStatus.GENERATING`
- `educator.status === "failed"` → `educator.status === EducatorStatus.FAILED`
- `educator.model_answer` → `educator.modelAnswer`
- `educator.gap_deep_dives` → `educator.gapDeepDives`
- `me?.plan === "pro"` → `me?.user?.plan === UserPlan.PRO` (or however `useMe` is unwrapped)
- `requestEducator.mutate()` → `requestEducator.mutate({ sessionId })`

- [ ] **Step 4: Verify TypeScript compiles**

Run: `cd web && npx tsc --noEmit`
Expected: No errors. Fix any type mismatches.

- [ ] **Step 5: Commit**

```bash
git add web/src/pages/session/overview.tsx web/src/pages/session/transcript.tsx web/src/pages/session/deep-dive.tsx
git commit -m "fix: update session detail pages to proto types"
```

---

### Task 10: Frontend — Update landing page components to proto types

**Files:**
- Modify: `web/src/pages/landing/scoring.tsx`
- Modify: `web/src/pages/landing/annotations.tsx`
- Modify: `web/src/pages/landing/coaching.tsx`
- Modify: `web/src/pages/landing/deep-dive.tsx`
- Modify: `web/src/pages/landing/strengths-gaps.tsx`
- Modify: `web/src/pages/landing/sample-session.tsx`

These components call `useSample*` hooks from `sample-queries.ts` (now ConnectRPC). Return types change from REST to proto.

- [ ] **Step 1: Update `scoring.tsx`**

- `useSampleEvaluation()` → `data?.evaluation?.scores` (unwrap `GetEvaluationResponse`)
- `useSampleSession()` → `data?.session` (unwrap `GetSampleSessionResponse`)
- `DIMENSIONS` keys: `deep_dive` → `deepDive`
- `scores[dim.key]` lookups work the same (object property access)
- `sessionData?.session.question_title` → `sessionData?.session?.questionTitle`

- [ ] **Step 2: Update `annotations.tsx`**

- Remove `import type { AnnotationType } from "@/api/types"` — import `AnnotationType` from `@/pb/drill/v1/evaluation_pb`
- `useSampleSession()` → unwrap: `data?.messages`, `data?.session`
- `useSampleEvaluation()` → unwrap: `data?.evaluation`
- `ANNOTATION_COLORS` and `ANNOTATION_LABELS` records: keys change from strings (`"strength"`) to proto enum values (`AnnotationType.STRENGTH`, etc.)
- `evaluation.annotations` stays (same field name in proto)
- `a.message_seq` → `a.messageSeq`
- `ann.type` is now a number (proto enum), so `ANNOTATION_COLORS[ann.type]` works if keys are enum values

- [ ] **Step 3: Update `coaching.tsx`**

- `useSampleCoach()` → `data?.analysis` for coach fields, `data?.scoreTrend` for trend data
- `coach.narrative` → `data?.analysis?.narrative`
- `coach.weakest_dimension` → `data?.analysis?.weakestDimension`
- `coach.score_trend` → `data?.scoreTrend`
- Sparkline `data` prop type: `{ date: string; overall_score: number }[]` → update to `ScoreTrendPoint[]` from proto or use `{ date: string; overallScore: number }[]`
- Inside Sparkline: `d.overall_score` → `d.overallScore`

- [ ] **Step 4: Update `deep-dive.tsx` (landing page version)**

- `useSampleEducator()` → unwrap: `data?.analysis`
- `educator.model_answer` → `analysis?.modelAnswer`
- `educator.gap_deep_dives` → `analysis?.gapDeepDives`

- [ ] **Step 5: Update `strengths-gaps.tsx`**

- `useSampleEvaluation()` → unwrap: `data?.evaluation`
- `evaluation.strengths` → `evaluation?.strengths`
- `evaluation.gaps` → `evaluation?.gaps`
- `evaluation.advice` → `evaluation?.advice`
- Fields are arrays/strings — same names in proto, no rename needed.

- [ ] **Step 6: Update `sample-session.tsx`**

- `useSampleSession()` → `data?.session`, `data?.messages`
- `useSampleEvaluation()` → `data?.evaluation`
- `sessionData.session.question_title` → `session?.questionTitle`
- `sessionData.messages?.length` → `messages?.length`
- `sessionData.session.config_duration_minutes` → `session?.configDurationMinutes`
- `evaluation.scores.overall` → `evaluation?.scores?.overall`

- [ ] **Step 7: Verify TypeScript compiles**

Run: `cd web && npx tsc --noEmit`
Expected: No errors.

- [ ] **Step 8: Commit**

```bash
git add web/src/pages/landing/
git commit -m "fix: update landing page components to proto types"
```

---

### Task 11: Frontend — Update replay engine to proto types

**Files:**
- Modify: `web/src/components/replay/engine.ts`

- [ ] **Step 1: Update type imports and field access**

- Replace `import type { Message } from "@/api/types"` with `import type { Message } from "@/pb/drill/v1/session_pb"`
- `ReplayOptions.sessionStartedAt: string` → needs to accept proto `Timestamp` or a converted date string. The simplest approach: keep the interface accepting strings and have the caller convert `session.startTime?.toDate().toISOString()`. Or change to accept `Date | Timestamp`.
- `m.created_at` → `m.createTime` (this is a proto `Timestamp` object, not a string)
- `new Date(m.created_at).getTime()` → `m.createTime!.toDate().getTime()` (Timestamp has `.toDate()`)
- Similarly for `sessionStartedAt`/`sessionEndedAt` — if keeping as strings, callers must convert. If accepting Timestamp, update the parsing.

Decision: Update `ReplayOptions` to accept `Timestamp | undefined` for time fields. Then use `.toDate().getTime()` internally. This avoids string conversion at every call site.

```typescript
import type { Message } from "@/pb/drill/v1/session_pb";
import type { Timestamp } from "@bufbuild/protobuf/wkt";

export interface ReplayOptions {
  messages: Message[];
  sessionStartedAt: Timestamp;
  sessionEndedAt: Timestamp | undefined;
  annotationSeqs: number[];
}
```

Update the `useEffect`:
```typescript
const start = options.sessionStartedAt.toDate().getTime();
const lastMsg = options.messages.length > 0
  ? options.messages[options.messages.length - 1].createTime!.toDate().getTime()
  : start;
const end = options.sessionEndedAt
  ? options.sessionEndedAt.toDate().getTime()
  : lastMsg + 30 * 1000;

messageOffsets.current = options.messages.map(
  (m) => (m.createTime!.toDate().getTime() - start) / 1000,
);
```

- [ ] **Step 2: Update callers**

In `transcript.tsx`, the replay engine call passes `session.started_at` and `session.ended_at`. Update to:

```typescript
const replay = useReplayEngine(
  replayMode && messages && session
    ? {
        messages,
        sessionStartedAt: session.startTime!,
        sessionEndedAt: session.endTime,
        annotationSeqs: (evaluation?.annotations ?? []).map((a) => a.messageSeq),
      }
    : null,
);
```

- [ ] **Step 3: Verify TypeScript compiles**

Run: `cd web && npx tsc --noEmit`
Expected: No errors.

- [ ] **Step 4: Commit**

```bash
git add web/src/components/replay/engine.ts web/src/pages/session/transcript.tsx
git commit -m "fix: update replay engine to proto Timestamp types"
```

---

### Task 12: Final verification

- [ ] **Step 1: Go build**

Run: `go build ./...`
Expected: Clean build.

- [ ] **Step 2: Go tests**

Run: `go test ./internal/rpc/sample/... -v`
Expected: All sample service tests pass.

- [ ] **Step 3: TypeScript check**

Run: `cd web && npx tsc --noEmit`
Expected: No type errors.

- [ ] **Step 4: Verify no remaining REST references**

Run: `grep -r 'apiClient\.\(get\|post\|patch\|delete\)' web/src/api/queries.ts`
Expected: Only the `useCreateQuestion` POST remains (questions endpoint hasn't migrated yet).

Run: `grep -r '/api/sample/' web/src/`
Expected: No results (all sample queries now use ConnectRPC).

Run: `grep -r 'from "@/api/types"' web/src/ --include='*.ts' --include='*.tsx'`
Expected: Only `queries.ts` (for `Question` type) and any files that use `Question`.

- [ ] **Step 5: Final commit if any fixups needed**
