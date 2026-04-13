# CreateQuestion RPC & REST Cleanup

**Issue:** btc/drill#152
**Date:** 2026-04-12

## Problem

`useCreateQuestion` in `web/src/api/queries.ts` calls `apiClient.post("/api/questions", data)` — a REST endpoint that doesn't exist. No handler is registered. The hook silently fails when invoked from the custom question creation dialog in `home.tsx`.

The SQL layer has `InsertQuestion` (used by the coach job), but there's no user-facing RPC or REST handler.

This is also the last `apiClient` usage in `queries.ts`. Fixing it eliminates all app-level REST code.

## Design

### 1. Proto changes (`pb/drill/v1/question.proto`)

Add `CreateQuestion` RPC following AIP-133 (Standard Create). Adopt AIP-203 field behavior annotations on the `Question` message.

```proto
import "google/api/field_behavior.proto";

service QuestionService {
  rpc ListQuestions(ListQuestionsRequest) returns (ListQuestionsResponse);
  rpc CreateQuestion(CreateQuestionRequest) returns (Question);
}

message CreateQuestionRequest {
  Question question = 1 [(google.api.field_behavior) = REQUIRED];
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

- Client sends `Question` with `title`, `prompt`, `difficulty`, and `tags` populated.
- Server ignores `id`, `user_id`, `source`, `create_time`, `image_url` (all OUTPUT_ONLY).
- `hints` is not settable at creation time (`InsertQuestion` SQL doesn't include it). The field remains unannotated for potential future use via `UpdateQuestion`.
- Server assigns `source = QUESTION_SOURCE_CUSTOM`, `user_id` from auth context.
- Returns the fully-populated `Question` (bare resource, no response wrapper per AIP-133).

### 2. Backend implementation

**`internal/backend/question.go`** — add `CreateQuestion` method:

- Accepts `ctx`, `userID uuid.UUID`, and writable fields (`title`, `prompt`, `difficulty`, `tags`).
- Calls `db.InsertQuestion` with `source = "custom"`, `user_id` from the caller, `coach_rationale` null.
- Returns the full question via `db.GetQuestion` so the response includes all server-assigned fields.
- OTel span matching existing `ListQuestions` pattern.

No new SQL — `InsertQuestion` and `GetQuestion` already exist.

**`internal/rpc/question/server.go`** — add `CreateQuestion` handler:

- Auth guard (same pattern as `ListQuestions`).
- Validate REQUIRED fields: `request.Msg.Question` non-nil, `title` and `prompt` non-empty. Returns `connect.CodeInvalidArgument` on failure.
- Validate `difficulty`: if unspecified, default to `DIFFICULTY_MEDIUM`. If set, must be `MEDIUM` or `HARD`. Returns `CodeInvalidArgument` otherwise.
- Calls `b.CreateQuestion(ctx, userID, ...)`.
- Converts via a new `fullQuestionToProto` converter taking `db.Question` (the full row type). Existing `questionToProto` stays for list rows — two converters, not one, since the list query intentionally returns fewer columns.
- Returns the `Question` directly.

### 3. Frontend changes

**`web/src/api/queries.ts`** — replace `useCreateQuestion`:

- Remove `apiClient` and `types.ts` imports.
- Replace REST-based `useTanStackMutation` with `useConnectMutation` from `@connectrpc/connect-query`.
- Import `createQuestion` from generated `question-QuestionService_connectquery`.
- Invalidate `listQuestions` query key on success via `createConnectQueryKey`.

**`web/src/pages/home.tsx`** — update `CreateQuestionDialog`:

- `mutate` call sends `{ question: { title, prompt, difficulty, tags } }` with `difficulty` mapped to proto enum value (`DIFFICULTY_MEDIUM = 1`, `DIFFICULTY_HARD = 2`).

**`web/src/main.tsx`** — remove CSRF token priming (`fetch("/api/health", ...)` block). Only consumer was `apiClient`.

### 4. Dead code removal

**Delete files:**
- `web/src/api/client.ts` — REST client wrapper, no remaining consumers.
- `web/src/api/types.ts` — `Question` interface, replaced by proto-generated types.
- `web/src/api/__tests__/client.test.ts` — tests for the dead REST client.
- `internal/handler/auth.go` — `writeJSON` and `writePaidBalanceRequired`, unused.

**Clean up:**
- `web/src/api/queries.ts` — remove `useTanStackMutation` import from `@tanstack/react-query` if unused after switch.
- `internal/handler/billing.go` — remove migration comment.
- `CLAUDE.md` — remove AIP-203 skip directive.

### 5. Testing

**`internal/rpc/question/server_test.go`** — add tests for `CreateQuestion`:

- Happy path: valid title + prompt + difficulty + tags returns full `Question` with `source = QUESTION_SOURCE_CUSTOM`, `user_id` set, server-assigned `id` and `create_time`.
- Missing question (nil) returns `CodeInvalidArgument`.
- Empty title returns `CodeInvalidArgument`.
- Empty prompt returns `CodeInvalidArgument`.
- Invalid difficulty returns `CodeInvalidArgument`.
- Unauthenticated returns `CodeUnauthenticated`.
- Created question appears in subsequent `ListQuestions` call.

Uses `backendtest.SeedUser`, real database, matching existing test patterns.

No new frontend tests — the ConnectRPC hook is generated code plus a one-liner wrapper.

## What stays as REST

- `GET /api/health` — health check.
- `GET /api/auth/oauth/{provider}` + callback — browser-redirect OAuth flows.
- `POST /api/webhooks/stripe` — external webhook with signature verification.

These are inherently HTTP-level concerns, not resource RPCs.
