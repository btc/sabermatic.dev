# Landing Page ConnectRPC Migration

Migrate the `feature/growth-landing` branch from REST API hooks to ConnectRPC. The branch was built against an older REST-based frontend; main has since migrated to ConnectRPC. REST endpoints referenced by the branch's `queries.ts` no longer exist on the backend. The sample endpoints (`/api/sample/*`) are REST but should be ConnectRPC for consistency.

## Approach

Dedicated `SampleService` in proto with 4 RPCs serving embedded fixture data. Restore `queries.ts` to ConnectRPC. Update all frontend consumers to use proto types.

## Proto

New file `pb/drill/v1/sample.proto`.

### Service definition

```protobuf
service SampleService {
  rpc GetSampleSession(GetSampleSessionRequest) returns (GetSampleSessionResponse);
  rpc GetSampleEvaluation(GetSampleEvaluationRequest) returns (GetEvaluationResponse);
  rpc GetSampleEducator(GetSampleEducatorRequest) returns (GetEducatorAnalysisResponse);
  rpc GetSampleCoach(GetSampleCoachRequest) returns (GetSampleCoachResponse);
}
```

### Messages

All request messages are empty.

`GetSampleSessionResponse` is new — bundles session + messages:
```protobuf
message GetSampleSessionResponse {
  Session session = 1;
  repeated Message messages = 2;
}
```

`GetSampleCoachResponse` is new — extends coach response with trend data for the landing page sparkline:
```protobuf
message GetSampleCoachResponse {
  CoachAnalysis analysis = 1;
  repeated ScoreTrendPoint score_trend = 2;
}

message ScoreTrendPoint {
  string date = 1;
  int32 overall_score = 2;
}
```

`GetEvaluationResponse` and `GetEducatorAnalysisResponse` are reused from the existing evaluation and educator protos.

After editing, run `buf generate` and commit generated code in `internal/pb/` and `web/src/pb/`.

## Backend

### New: `internal/rpc/sample/server.go`

`SampleService` struct holds pre-parsed proto messages. Constructed at init time by unmarshaling the embedded fixture JSON into proto messages via `protojson.Unmarshal`. Returns error on unmarshal failure — no panics.

```go
type SampleService struct {
    sessionResp   *drillv1.GetSampleSessionResponse
    evaluationResp *drillv1.GetEvaluationResponse
    educatorResp  *drillv1.GetEducatorAnalysisResponse
    coachResp     *drillv1.GetSampleCoachResponse
}

func NewSampleService() (*SampleService, error) { ... }
```

RPC handler wraps `SampleService`:

```go
type Server struct {
    ss *SampleService
}

func NewServer(ss *SampleService) *Server {
    return &Server{ss: ss}
}
```

Each RPC method returns the pre-parsed response. No DB, no auth, no backend dependency.

`SampleService` is constructed in the wiring layer and passed directly to `NewServer(ss)`.

### Registration

In `internal/rpc/register.go`, register with `publicOpts` (no auth interceptor):

```go
mux.Handle(drillv1connect.NewSampleServiceHandler(sample.NewServer(ss), publicOpts))
```

### Removed

- `internal/sample/handler.go` — REST handlers
- `internal/sample/handler_test.go` — REST handler tests
- `sample.RegisterRoutes(mux)` call from `internal/handler/routes.go`

### Kept

- `internal/sample/embed.go` — fixture embedding
- `internal/sample/fixtures/*.json` — fixture data unchanged

The `embed.go` and fixtures stay in `internal/sample/`. The RPC handler in `internal/rpc/sample/` imports the embedded filesystem from `internal/sample`. The fixture JSON files must be rewritten to use proto field names (e.g., `start_time` not `started_at`, `create_time` not `created_at`) and proto enum string representations (e.g., `SESSION_STATUS_REVIEWED` not `"reviewed"`). Response-level fixtures must match the proto response message structure (e.g., evaluation wrapped in `{ "evaluation": {...} }`).

## Frontend

### `web/src/api/queries.ts`

Restore to main's ConnectRPC version. Auth mutations (`useLogin`, `useSignup`, `useLogout`, `useForgotPassword`, `useResetPassword`, `useVerifyEmail`, `useDeleteAccount`) use `useMutation` from `@connectrpc/connect-query` with the generated auth service methods.

Add ConnectRPC query/mutation wrappers for hooks consumed by session detail pages:

| Hook | ConnectRPC call |
|------|----------------|
| `useMe(options?)` | `useQuery(getMe, {}, { enabled: options?.enabled })` — returns `data?.user` |
| `useSession(id, options?)` | `useQuery(getSession, { id }, options)` |
| `useEvaluation(sessionId, enabled?)` | `useQuery(getEvaluation, { sessionId }, { enabled })` |
| `useTranscript(sessionId, enabled?)` | `useQuery(getTranscript, { sessionId }, { enabled })` |
| `useEducator(sessionId, enabled?)` | `useQuery(getEducatorAnalysis, { sessionId }, { enabled, refetchInterval })` |
| `useRequestEducator(sessionId)` | `useMutation(requestEducatorAnalysis, { onSuccess: invalidate })` |
| `useRetryEvaluation(sessionId)` | `useMutation(retryEvaluation, { onSuccess: invalidate })` |

`useCreateQuestion` stays REST (`apiClient.post`) until question endpoints migrate.

### `web/src/api/sample-queries.ts`

Rewrite from REST to ConnectRPC. Same exported hook names, new internals:

```typescript
import { useQuery } from "@connectrpc/connect-query";
import { getSampleSession, getSampleEvaluation, ... } from "@/pb/drill/v1/sample-SampleService_connectquery";

export function useSampleSession(options?) {
  return useQuery(getSampleSession, {}, { staleTime: Infinity, enabled: options?.enabled });
}
// etc.
```

### `web/src/api/types.ts`

Strip all types except `Question` (still used by the REST `useCreateQuestion` hook). All other types (`Session`, `Message`, `EvaluationScores`, `AnnotationType`, `AnnotationResponse`, `EvaluationResponse`, `EducatorAnalysis`, `CoachAnalysis`, `User`, `Usage`, etc.) are replaced by proto imports throughout the codebase.

### `web/src/hooks/use-auth.ts`

`useOptionalAuth` switches from REST `useMe()` to ConnectRPC `useQuery(getMe, {})`. The `ApiError` check becomes a `ConnectError` check with `error.code === Code.Unauthenticated`.

### Session detail pages

**`overview.tsx`**, **`transcript.tsx`**, **`deep-dive.tsx`** — update to proto types:

Field name changes:
- `message_seq` → `messageSeq`
- `model_answer` → `modelAnswer`
- `gap_deep_dives` → `gapDeepDives`
- `deep_dive` → `deepDive`
- `created_at` → `createTime`
- `started_at` → `startTime`
- `ended_at` → `endTime`
- `archived_at` → `archiveTime`

Enum changes:
- `"generating"` → `EducatorStatus.GENERATING`
- `"completed"` → `EducatorStatus.COMPLETED`
- `"failed"` → `EducatorStatus.FAILED`
- `"evaluation_failed"` → `SessionStatus.EVALUATION_FAILED` from proto
- `"strength"` → `AnnotationType.STRENGTH`
- `"gap"` → `AnnotationType.GAP`
- `"missed_opportunity"` → `AnnotationType.MISSED_OPPORTUNITY`
- `"note"` → `AnnotationType.NOTE`

Response unwrapping:
- `useSession` returns `data?.session` (wrapped in `GetSessionResponse`)
- `useEvaluation` returns `data?.evaluation` (wrapped in `GetEvaluationResponse`)
- `useEducator` returns `data?.analysis` (wrapped in `GetEducatorAnalysisResponse`)
- `useSampleSession` returns `data?.session` and `data?.messages`
- `useSampleEvaluation` returns `data?.evaluation`

Type imports change from `@/api/types` to `@/pb/drill/v1/*_pb`:
- `EvaluationScores` from `evaluation_pb`
- `Annotation`, `AnnotationType` from `evaluation_pb`
- `Message` from `session_pb`
- `EducatorStatus` from `educator_pb`
- `SessionStatus` from `session_pb`

### Landing page components

No markup/styling changes. Only import path and field name updates:
- `annotations.tsx` — `AnnotationType` from proto, field name updates
- `deep-dive.tsx` — `modelAnswer` not `model_answer`
- `scoring.tsx` — `EvaluationScores` from proto, `deepDive` not `deep_dive`
- `strengths-gaps.tsx` — field name updates
- `coaching.tsx` — `ScoreTrendPoint` from proto, field name updates
- `sample-session.tsx` — field name updates

### Replay engine

`web/src/components/replay/engine.ts` — import `Message` from `@/pb/drill/v1/session_pb` instead of `@/api/types`. Field access: `createTime` instead of `created_at`.

### Deleted files

- REST types from `web/src/api/types.ts` (all except `Question`)

## Testing

**Backend:** Rewrite `internal/sample/handler_test.go` as ConnectRPC client tests. Verify each RPC returns correct fixture data with correct proto structure. Follow existing ConnectRPC test patterns.

**Frontend:** `tsc --noEmit` (TypeScript strict mode) validates all field name and type changes compile. No new frontend tests needed.

## What doesn't change

- Landing page component markup, styling, animations
- Fixture JSON data content
- OG meta tag injection in `SPAHandler`
- App routing (`app.tsx`)
- `useScrollReveal` hook
- Replay timeline/controls component markup
