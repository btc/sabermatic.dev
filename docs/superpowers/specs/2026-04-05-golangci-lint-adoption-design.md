# golangci-lint Adoption

## Goal

Enable golangci-lint as a CI gate in `make test`, fixing all 100 existing violations across two PRs.

## Current State

- golangci-lint v2.5.0 installed
- `.golangci.yml` exists with 8 linters: govet, staticcheck, errcheck, ineffassign, unused, gocritic, misspell, revive
- `make test` has `golangci-lint run ./...` commented out
- 100 issues: errcheck (50), gocritic (45), staticcheck (3), unused (2)

## Config

Keep `.golangci.yml` as-is. No threshold changes.

## PR1: Mechanical Fixes (zero behavior change)

### errcheck nolints (~50 issues)

Add `//nolint:errcheck` to all 50 sites. These fall into categories where the error cannot be meaningfully handled:

- **Defer cleanup:** `defer b.Close()`, `defer tx.Rollback(ctx)`, `defer resp.Body.Close()`
- **HTTP response writes:** `json.NewEncoder(w).Encode(...)`, `fmt.Fprint(w, ...)`
- **Test teardown:** `container.Terminate(ctx)`, `tp.Shutdown(...)`, `ws.CloseNow()`
- **Intentional fire-and-forget:** `auth.CheckPassword(dummyBcryptHash, "x")` (timing-attack mitigation), `queries.TouchAuthSession(...)`
- **Best-effort WebSocket close:** `c.ws.Close(...)` in conductor error paths
- **GCS writer cleanup:** `w.Close()` in error path after failed write (line 37, not the real close on line 40)
- **Test helpers:** `rand.Read(b)` (never errors since Go 1.24), `conn.Exec(...)` in test DB cleanup

### Unused code (2 issues) — delete

- `pending` field in `internal/ai/client.go:289`
- `writePaidBalanceRequired` func in `internal/handler/auth.go:15`

### Deprecated API (3 issues) — update

Replace `trace.NewNoopTracerProvider()` with `noop.NewTracerProvider()` in:
- `internal/drilotel/drilotel_test.go:37`
- `internal/drilotel/drilotel_test.go:69`
- `internal/drilotel/riverware_test.go:31`

### Commented-out code (3 issues) — nolint (false positives)

`internal/backend/billing_test.go:337,365,463` — these are `// Balance: 60 - 30 = 30.` comments explaining test assertions. gocritic misidentifies the arithmetic as commented-out code. Add `//nolint:gocritic` to each line.

### sprintfQuotedString (3 issues) — nolint

`internal/drilotel/riverware.go:23,35,36` use `fmt.Sprintf` with `"%s"` inside backtick-quoted raw JSON strings. `%q` would double-quote the values (`"\"abc123\""`) and break the JSON. Add `//nolint:gocritic` to these three lines.

### Makefile

Uncomment `golangci-lint run ./...` in `make test`.

### Files touched (~15)

```
cmd/drill/main.go
cmd/drill/main_test.go
internal/ai/client.go
internal/ai/client_test.go
internal/ai/client_tool_test.go
internal/backend/auth.go
internal/backend/backend.go
internal/backend/billing_test.go
internal/backend/evaluation.go
internal/backend/session.go
internal/drilotel/drilotel_test.go
internal/drilotel/riverware.go
internal/drilotel/riverware_test.go
internal/drilotel/sloghandler_test.go
internal/handler/admin.go
internal/handler/auth.go
internal/handler/health.go
internal/handler/session_ws_test.go
internal/interview/conductor.go
internal/interview/observer/tts_accumulator.go
internal/jobs/cleanup.go
internal/jobs/integration_test.go
internal/storage/gcs.go
internal/testutil/backend.go
internal/testutil/pg.go
Makefile
```

## PR2: Pointer Fixes (signature changes)

### hugeParam — change to pointer params

Grouped by type:

**`db.Question` (224b) → `*db.Question`:**
- `internal/evaluation/prompt.go`: `BuildPrompt`, `buildTranscript`
- `internal/educator/prompt.go`: `BuildPrompt`, `buildSystemPrompt`, `buildTranscript`
- `internal/interview/prompt.go`: `(*PromptBuilder).WithQuestion`
- `internal/interview/messages.go`: `msgSessionLoaded`

**`db.User` (224b) → `*db.User`:**
- `internal/backend/oauth.go`: `createSessionInTx`

**`db.Evaluation` (144b) → `*db.Evaluation`:**
- `internal/educator/prompt.go`: `buildEvaluationSummary`

**`db.ListQuestionsForUserRow` (176b) → `*db.ListQuestionsForUserRow`:**
- `internal/rpc/question/server.go`: `questionToProto`

**`db.ListActiveGrantsRow` (96b) → `*db.ListActiveGrantsRow`:**
- `internal/rpc/user/server.go`: `grantToProto`

**`goth.User` (256b) → `*goth.User`:**
- `internal/handler/oauth.go`: `resolveDisplayName`
- `internal/handler/oauth_flow_test.go`: `setupGothForTest`

**`stripe.Event` (144b) → `*stripe.Event`:**
- `internal/backend/billing.go`: `HandleStripeWebhook`, `handleCheckoutCompleted`, `handleInvoicePaid`

**`WSMessage` (120b) → `*WSMessage` (param only):**
- `internal/interview/conductor.go`: `endTurn`

**`OAuthLoginParams` (96b) → `*OAuthLoginParams`:**
- `internal/backend/oauth.go`: `OAuthLogin`, `oauthLoginWithRetry`

**`PersistMessageParams` (88b) → `*PersistMessageParams`:**
- `internal/backend/session.go`: `persistMessage`, `PersistMessage`, `PersistInterviewerTurn`

**`ai.StreamParams` (112b) → `*ai.StreamParams`:**
- `internal/backend/session.go`: `StreamLLM`

**`ai.CallToolParams` (184b) → `*ai.CallToolParams`:**
- `internal/ai/client.go`: `CallToolAndLog`

**`ai.CallParams` (112b) → `*ai.CallParams`:**
- `internal/ai/client.go`: `CallAndLog`

**`ai.persistParams` (600b) → `*ai.persistParams`:**
- `internal/ai/client.go`: `persistCall`

### rangeValCopy — index into slice (10 sites)

Convert `for _, x := range xs` to `for i := range xs { x := &xs[i] }`:

- `internal/ai/client.go:163` — `resp.Content` (648b)
- `internal/ai/client.go:247` — `resp.Content` (648b)
- `internal/coach/prompt.go:77` — questions (224b)
- `internal/coach/prompt.go:89` — sessions (224b)
- `internal/educator/prompt.go:71` — messages (144b)
- `internal/evaluation/prompt.go:98` — messages (144b)
- `internal/jobs/coach.go:67` — sessions (224b)
- `internal/jobs/evaluate.go:94` — messages (144b)
- `internal/rpc/question/server.go:92` — page rows (176b)

### Nolint (can't/shouldn't fix)

- `internal/drilotel/sloghandler.go:30` — `slog.Handler` interface requires `Handle(ctx, r slog.Record)` by value
- `internal/interview/ws_message.go:86,94` — value receiver methods `Traceparent()`, `AudioExt()` (changing to pointer receiver changes method set)
- `internal/billing/entitlement.go:39,44,49` — value receiver on small immutable struct, idiomatic Go

## Verification

After each PR, `golangci-lint run ./...` must exit 0 and `go test ./internal/... ./cmd/... -race -count=1` must pass.
