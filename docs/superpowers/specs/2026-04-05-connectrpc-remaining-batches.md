# ConnectRPC Migration — Remaining Batches (2-8)

**Date:** 2026-04-05
**Prerequisite:** Batch 1 (QuestionService) is merged. See `docs/superpowers/specs/2026-04-05-connectrpc-migration-design.md` for the full design.

## What's Done

Batch 1 established the complete pattern:
- **Buf tooling**: `buf.yaml`, `buf.gen.yaml`, `buf.lock` at repo root. Proto sources in `pb/drill/v1/`. Generated Go in `internal/pb/`, generated TS in `web/src/pb/`.
- **Go server pattern**: `internal/rpc/{service}/server.go` implements `drillv1connect.{Service}ServiceHandler`. Uses `auth.UserFromContext(ctx)` for auth. Returns Connect error codes.
- **Registration**: `internal/rpc/register.go` — one line per service: `mux.Handle(drillv1connect.New{Service}ServiceHandler({service}.NewServer(b), opts))`. Add new services here.
- **Auth interceptor**: `internal/rpc/interceptor.go` — unary only for now. When streaming RPCs are added (batches 4-6), extend to `connect.StreamingHandlerInterceptorFunc`.
- **CSRF exemption**: `csrfMiddleware` in `internal/handler/routes.go` exempts Connect paths via `rpc.ConnectPathPrefixes()`. Add new service names there.
- **Frontend**: `web/src/api/transport.ts` (Connect transport), `TransportProvider` in `main.tsx`. Generated hooks replace hand-written `useQuery` hooks. Import pattern: `import { listQuestions } from "@/pb/drill/v1/question-QuestionService_connectquery"`.
- **Tests**: `internal/rpc/{service}/server_test.go` using `testutil.NewTestBackend(t)` and `testutil.SignupAndLogin(t, b)` from `internal/testutil/`. Real DB via testcontainers.
- **AIP conventions**: Pagination (AIP-158) on all List RPCs. Enums for constrained string fields. `create_time` not `created_at`. Error codes: `CodeUnauthenticated`, `CodeInvalidArgument`, `CodeInternal`, `CodeNotFound`.

## Remaining Batches

Each batch follows the same recipe:
1. Add `.proto` file in `pb/drill/v1/`
2. `buf generate`
3. Create `internal/rpc/{service}/server.go`
4. Add to `rpc.Register()` and `ConnectPathPrefixes()`
5. Write tests in `internal/rpc/{service}/server_test.go`
6. Update frontend: replace hand-written hooks with generated hooks
7. Delete old REST handler from `internal/handler/`
8. Remove old route from `RegisterRoutes()`

### Batch 2: UserService

**Endpoints:** `GetMe`, `GetUsage`
**Proto file:** `pb/drill/v1/user.proto`
**Handler to delete:** `internal/handler/user.go`
**Frontend hooks to replace:** `useMe()`, `useUsage()` in `web/src/api/queries.ts`
**Notes:** `GetMe` returns the `User` message — define it here. `GetUsage` returns balance/usage info. Both are authenticated, read-only.

### Batch 3: SessionService

**Endpoints:** `CreateSession`, `ListSessions`, `GetSession`
**Proto file:** `pb/drill/v1/session.proto`
**Handler to delete:** `internal/handler/session.go` (REST parts only)
**Frontend hooks to replace:** `useCreateSession()`, `useSessions()`, `useSession()` in `queries.ts`
**Notes:** `ListSessions` needs AIP-132 pagination. `CreateSession` is AIP-133. `GetSession` is AIP-131. The WebSocket handler (`session_ws.go`) is NOT migrated. `Session` message will be reused by Evaluation/Educator/Coach services.

### Batch 4: EvaluationService (streaming candidate)

**Endpoints:** `GetEvaluation`, `RetryEvaluation`
**Proto file:** `pb/drill/v1/evaluation.proto`
**Handler to delete:** `internal/handler/evaluation.go`
**Frontend hooks to replace:** `useEvaluation()`, `useRetryEvaluation()` in `queries.ts`
**Notes:** Currently polled every 3s via `useSession(id, { refetchInterval: 3000 })`. Consider server-streaming RPC: client calls `WatchEvaluation`, server holds connection and pushes the result when the River job completes. If streaming is added, extend the auth interceptor for streaming (see comment in `interceptor.go`).

### Batch 5: EducatorService (streaming candidate)

**Endpoints:** `GetEducatorAnalysis`, `RequestEducatorAnalysis`
**Proto file:** `pb/drill/v1/educator.proto`
**Handler to delete:** `internal/handler/educator.go`
**Frontend hooks to replace:** `useEducator()`, `useRequestEducatorAnalysis()` in `queries.ts`
**Notes:** Same polling pattern as evaluation (3s refetch while `status === "generating"`). Same streaming opportunity.

### Batch 6: CoachService (streaming candidate)

**Endpoints:** `GetCoachAnalysis`, `RequestCoachAnalysis`
**Proto file:** `pb/drill/v1/coach.proto`
**Handler to delete:** `internal/handler/coach.go`
**Frontend hooks to replace:** `useCoachLatest()`, `useRequestCoachAnalysis()` in `queries.ts`
**Notes:** Same streaming opportunity. Coach analysis is user-scoped, not session-scoped.

### Batch 7: BillingService

**Endpoints:** `PostCheckout`, `PostPortal`
**Proto file:** `pb/drill/v1/billing.proto`
**Handler to delete:** `internal/handler/billing.go` (checkout/portal only)
**Frontend hooks to replace:** `useCheckout()`, `usePortal()` in `queries.ts`
**Notes:** Stripe webhook (`POST /api/webhooks/stripe`) stays as a raw HTTP handler — it's an inbound webhook with Stripe signature verification, not an RPC. The checkout/portal RPCs return Stripe redirect URLs.

### Batch 8: AuthService

**Endpoints:** `Signup`, `Login`, `Logout`, `VerifyEmail`, `ForgotPassword`, `ResetPassword`
**Proto file:** `pb/drill/v1/auth.proto`
**Handler to delete:** `internal/handler/auth.go`
**Frontend hooks to replace:** auth hooks in `queries.ts`
**Notes:** These are PUBLIC endpoints (no auth). Registration in `rpc.Register()` needs separate `opts` WITHOUT `AuthInterceptor`. OAuth (`GET /api/auth/oauth/{provider}`) stays as HTTP redirect handlers.

## Frontend-Only Stubs (no backend handler exists)

These should be implemented directly as ConnectRPC services when their batch lands:
- `CreateQuestion` (batch 1 scope but deferred — #49)
- `ArchiveBulk` (batch 3)
- `UpdateProfile` / `PATCH /api/me` (batch 2)
- `DeleteAccount` (batch 8)
- `ExportData` / `POST /api/me/export` (batch 2)
- `GetTranscript` (batch 3)

## Gotchas Learned in Batch 1

1. **`otelconnect.NewInterceptor()` returns `(interceptor, error)`** — propagate the error, don't panic. `rpc.Register` returns `error`.
2. **`NewHandler` and `RegisterRoutes` return `error`** — changed in batch 1. All callers check it.
3. **`createConnectTransport` doesn't have a `credentials` option** — use a `fetch` wrapper: `fetch: (input, init) => globalThis.fetch(input, { ...init, credentials: "same-origin" })`.
4. **Proto field names are camelCase in TypeScript** — `user_id` becomes `userId`, `created_at` becomes `createTime`, etc. Every component touching migrated data needs field name updates.
5. **Enums are numbers in TypeScript, not strings** — `question.difficulty === "hard"` becomes `question.difficulty === Difficulty.HARD`. Import from `*_pb`.
6. **`buf generate` remote plugins need exact version pins** — `:v2` shorthand doesn't work. Pin to specific versions (e.g., `buf.build/bufbuild/es:v2.11.0`).
7. **`buf lint` must run from repo root** — CWD matters.
8. **Use `migrations.FS` (embedded) for test migrations** — not relative `os.DirFS` paths which break when test CWD differs.
9. **`git add -A` during rebase will re-add untracked files** — stage specific files only.
10. **Keep old types in `types.ts` if dead stubs still reference them** — clean up when the stub is replaced.

## Execution Approach

Batch 1 took ~30 commits with per-task review. Subsequent batches can be faster since the pattern is established. A single agent can likely handle one batch end-to-end (proto → server → tests → frontend → cleanup) with one review at the end, rather than per-task reviews.

For a batch operation across all remaining batches: consider dispatching parallel agents per batch (2-8), each in a worktree, since the batches are independent (different proto files, different handler files, different frontend hooks). Merge sequentially to avoid conflicts in shared files (`register.go`, `queries.ts`, `types.ts`).
