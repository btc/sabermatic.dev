# golangci-lint Adoption Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Enable golangci-lint as a CI gate by fixing all 100 existing violations across two PRs.

**Architecture:** PR1 fixes all mechanical/nolint issues (errcheck, unused, deprecated, false positives). PR2 changes function signatures to pass large structs by pointer. Each PR ends with `golangci-lint run ./...` passing clean.

**Tech Stack:** Go, golangci-lint v2.5.0

---

## PR1: Mechanical Fixes

### Task 1: Add errcheck nolint annotations — main and ai packages

**Files:**
- Modify: `cmd/drill/main.go:81,148`
- Modify: `cmd/drill/main_test.go:31,57,64`
- Modify: `internal/ai/client.go:336,348`
- Modify: `internal/ai/client_test.go:27,28,31,193`
- Modify: `internal/ai/client_tool_test.go:21,75`

- [ ] **Step 1: Add nolint comments to cmd/drill/main.go**

```go
// line 81: change
defer b.Close()
// to
defer b.Close() //nolint:errcheck // best-effort cleanup

// line 148: change
return io.MultiWriter(os.Stderr, f), func() { f.Close() }
// to
return io.MultiWriter(os.Stderr, f), func() { f.Close() } //nolint:errcheck // best-effort cleanup
```

- [ ] **Step 2: Add nolint comments to cmd/drill/main_test.go**

```go
// line 31: change
listener.Close()
// to
listener.Close() //nolint:errcheck // test cleanup

// line 57: change
defer resp.Body.Close()
// to
defer resp.Body.Close() //nolint:errcheck // test cleanup

// line 64: change
defer resp.Body.Close()
// to
defer resp.Body.Close() //nolint:errcheck // test cleanup
```

- [ ] **Step 3: Add nolint comments to internal/ai/client.go**

```go
// line 336: change
defer ts.stream.Close()
// to
defer ts.stream.Close() //nolint:errcheck // best-effort cleanup

// line 348: change
defer ts.stream.Close()
// to
defer ts.stream.Close() //nolint:errcheck // best-effort cleanup
```

- [ ] **Step 4: Add nolint comments to internal/ai/client_test.go**

```go
// lines 27,28,31: add //nolint:errcheck to each fmt.Fprintf(w, ...) line
// line 193: add //nolint:errcheck to fmt.Fprint(w, ...) line
```

- [ ] **Step 5: Add nolint comments to internal/ai/client_tool_test.go**

```go
// lines 21,75: add //nolint:errcheck to each fmt.Fprint(w, ...) line
```

- [ ] **Step 6: Run golangci-lint on touched packages**

Run: `golangci-lint run ./cmd/drill/... ./internal/ai/...`
Expected: No errcheck failures in these packages.

- [ ] **Step 7: Commit**

```bash
git add cmd/drill/main.go cmd/drill/main_test.go internal/ai/client.go internal/ai/client_test.go internal/ai/client_tool_test.go
git commit -m "lint: add errcheck nolint annotations to cmd and ai packages"
```

### Task 2: Add errcheck nolint annotations — backend and handler packages

**Files:**
- Modify: `internal/backend/auth.go:155,164`
- Modify: `internal/backend/backend.go:206`
- Modify: `internal/backend/evaluation.go:129`
- Modify: `internal/backend/session.go:254`
- Modify: `internal/handler/admin.go:12`
- Modify: `internal/handler/auth.go:12`
- Modify: `internal/handler/health.go:29`
- Modify: `internal/handler/session_ws_test.go:111,321,428,507,562,568,1095,1113,1365,1656,1663`

- [ ] **Step 1: Add nolint comments to internal/backend/auth.go**

```go
// line 155: change
auth.CheckPassword(dummyBcryptHash, "x")
// to
auth.CheckPassword(dummyBcryptHash, "x") //nolint:errcheck // constant-time dummy check

// line 164: same change
```

- [ ] **Step 2: Add nolint comments to internal/backend/backend.go**

```go
// line 206: change
queries.TouchAuthSession(touchCtx, row.ID)
// to
queries.TouchAuthSession(touchCtx, row.ID) //nolint:errcheck // fire-and-forget
```

- [ ] **Step 3: Add nolint comments to internal/backend/evaluation.go and session.go**

```go
// evaluation.go line 129: change
defer tx.Rollback(ctx)
// to
defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is no-op

// session.go line 254: same pattern
```

- [ ] **Step 4: Add nolint comments to internal/handler/admin.go, auth.go, health.go**

```go
// Each json.NewEncoder(w).Encode(...) line gets //nolint:errcheck // HTTP response write
```

- [ ] **Step 5: Add nolint comments to internal/handler/session_ws_test.go**

All `defer ws.CloseNow()`, `ws.Close(...)`, `conn.Close()`, and `conn.Exec(...)` lines get `//nolint:errcheck // test cleanup`.

Lines: 111, 321, 428, 507, 562, 568, 1095, 1113, 1365, 1656, 1663.

- [ ] **Step 6: Run golangci-lint on touched packages**

Run: `golangci-lint run ./internal/backend/... ./internal/handler/...`
Expected: No errcheck failures in these packages.

- [ ] **Step 7: Commit**

```bash
git add internal/backend/auth.go internal/backend/backend.go internal/backend/evaluation.go internal/backend/session.go internal/handler/admin.go internal/handler/auth.go internal/handler/health.go internal/handler/session_ws_test.go
git commit -m "lint: add errcheck nolint annotations to backend and handler packages"
```

### Task 3: Add errcheck nolint annotations — remaining packages

**Files:**
- Modify: `internal/drilotel/drilotel_test.go:36`
- Modify: `internal/drilotel/riverware_test.go:30`
- Modify: `internal/drilotel/sloghandler_test.go:22`
- Modify: `internal/interview/conductor.go:109,113,596`
- Modify: `internal/interview/observer/tts_accumulator.go:150`
- Modify: `internal/jobs/cleanup.go:35`
- Modify: `internal/jobs/integration_test.go:56`
- Modify: `internal/storage/gcs.go:37`
- Modify: `internal/testutil/backend.go:19`
- Modify: `internal/testutil/pg.go:59,76,90,109,133,135,136`

- [ ] **Step 1: Add nolint comments to internal/drilotel/ test files**

```go
// drilotel_test.go line 36:
p.Shutdown(context.Background()) //nolint:errcheck // test cleanup

// riverware_test.go line 30:
tp.Shutdown(context.Background()) //nolint:errcheck // test cleanup

// sloghandler_test.go line 22: the t.Cleanup closure
tp.Shutdown(context.Background()) //nolint:errcheck // test cleanup
```

- [ ] **Step 2: Add nolint comments to internal/interview/ files**

```go
// conductor.go lines 109,113,596: each c.ws.Close(...) gets //nolint:errcheck // best-effort WS close

// observer/tts_accumulator.go line 150:
rc.Close() //nolint:errcheck // best-effort cleanup
```

- [ ] **Step 3: Add nolint comments to internal/jobs/ files**

```go
// cleanup.go line 35:
defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is no-op

// integration_test.go line 56:
riverClient.Stop(stopCtx) //nolint:errcheck // test cleanup
```

- [ ] **Step 4: Add nolint comments to internal/storage/gcs.go**

```go
// line 37: change
w.Close()
// to
w.Close() //nolint:errcheck // write already failed, cleanup only
```

- [ ] **Step 5: Add nolint comments to internal/testutil/ files**

```go
// backend.go line 19:
t.Cleanup(func() { b.Close() }) //nolint:errcheck // test cleanup

// pg.go lines 59,76: container.Terminate(...) //nolint:errcheck // test cleanup
// pg.go line 90: rand.Read(b) //nolint:errcheck // crypto/rand.Read never errors
// pg.go line 109: conn.Close(ctx) //nolint:errcheck // test cleanup
// pg.go lines 133,135: conn.Exec(...) //nolint:errcheck // best-effort test DB cleanup
// pg.go line 136: conn.Close(context.Background()) //nolint:errcheck // test cleanup
```

- [ ] **Step 6: Run golangci-lint on touched packages**

Run: `golangci-lint run ./internal/drilotel/... ./internal/interview/... ./internal/jobs/... ./internal/storage/... ./internal/testutil/...`
Expected: No errcheck failures in these packages.

- [ ] **Step 7: Commit**

```bash
git add internal/drilotel/drilotel_test.go internal/drilotel/riverware_test.go internal/drilotel/sloghandler_test.go internal/interview/conductor.go internal/interview/observer/tts_accumulator.go internal/jobs/cleanup.go internal/jobs/integration_test.go internal/storage/gcs.go internal/testutil/backend.go internal/testutil/pg.go
git commit -m "lint: add errcheck nolint annotations to remaining packages"
```

### Task 4: Delete unused code

**Files:**
- Modify: `internal/ai/client.go:289`
- Modify: `internal/handler/auth.go:15-20`

- [ ] **Step 1: Delete the `pending` field from TokenStream**

In `internal/ai/client.go`, remove line 289:

```go
// Remove this line:
pending  string // buffered token from last Next() lookahead
```

- [ ] **Step 2: Delete the `writePaidBalanceRequired` function**

In `internal/handler/auth.go`, remove the entire function (lines 15-20):

```go
// Remove this entire function:
func writePaidBalanceRequired(w http.ResponseWriter) {
	writeJSON(w, http.StatusForbidden, map[string]string{
		"error":   "paid_balance_required",
		"message": "This feature requires a paid plan or minute balance.",
	})
}
```

- [ ] **Step 3: Run tests to verify nothing breaks**

Run: `go build ./internal/ai/... ./internal/handler/...`
Expected: Compiles clean.

- [ ] **Step 4: Commit**

```bash
git add internal/ai/client.go internal/handler/auth.go
git commit -m "lint: delete unused pending field and writePaidBalanceRequired func"
```

### Task 5: Fix deprecated trace.NewNoopTracerProvider

**Files:**
- Modify: `internal/drilotel/drilotel_test.go:10,37,69`
- Modify: `internal/drilotel/riverware_test.go:17,31`

- [ ] **Step 1: Update drilotel_test.go**

Change the import:

```go
// Replace:
"go.opentelemetry.io/otel/trace"
// With:
"go.opentelemetry.io/otel/trace/noop"
```

Change both call sites (lines 37 and 69):

```go
// Replace:
otel.SetTracerProvider(trace.NewNoopTracerProvider())
// With:
otel.SetTracerProvider(noop.NewTracerProvider())
```

- [ ] **Step 2: Update riverware_test.go**

Change the import:

```go
// Replace:
"go.opentelemetry.io/otel/trace"
// With:
"go.opentelemetry.io/otel/trace/noop"
```

Change call site (line 31):

```go
// Replace:
otel.SetTracerProvider(trace.NewNoopTracerProvider())
// With:
otel.SetTracerProvider(noop.NewTracerProvider())
```

- [ ] **Step 3: Run tests**

Run: `go test ./internal/drilotel/... -count=1`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/drilotel/drilotel_test.go internal/drilotel/riverware_test.go
git commit -m "lint: replace deprecated trace.NewNoopTracerProvider with noop.NewTracerProvider"
```

### Task 6: Nolint false-positive commentedOutCode and sprintfQuotedString

**Files:**
- Modify: `internal/backend/billing_test.go:337,365,463`
- Modify: `internal/drilotel/riverware.go:23,35,36`

- [ ] **Step 1: Nolint the billing_test.go false positives**

Lines 337, 365, 463 are all `// Balance: 60 - 30 = 30.` — real comments, not commented-out code. Add nolint:

```go
// Balance: 60 - 30 = 30. //nolint:gocritic // not commented-out code
```

- [ ] **Step 2: Nolint the riverware.go sprintfQuotedString issues**

Lines 23, 35, 36 use `"%s"` in backtick-quoted raw JSON strings. `%q` would double-quote and break JSON. Add nolint:

```go
// line 23: change
traceJSON := []byte(fmt.Sprintf(`{"trace_id":"%s","span_id":"%s"}`, sc.TraceID(), sc.SpanID()))
// to
traceJSON := []byte(fmt.Sprintf(`{"trace_id":"%s","span_id":"%s"}`, sc.TraceID(), sc.SpanID())) //nolint:gocritic // %q would break JSON

// line 35: change
existing["trace_id"] = json.RawMessage(fmt.Sprintf(`"%s"`, sc.TraceID()))
// to
existing["trace_id"] = json.RawMessage(fmt.Sprintf(`"%s"`, sc.TraceID())) //nolint:gocritic // %q would break JSON

// line 36: same pattern for span_id
```

- [ ] **Step 3: Run golangci-lint on touched packages**

Run: `golangci-lint run ./internal/backend/... ./internal/drilotel/...`
Expected: No gocritic failures in these packages.

- [ ] **Step 4: Commit**

```bash
git add internal/backend/billing_test.go internal/drilotel/riverware.go
git commit -m "lint: nolint false-positive commentedOutCode and sprintfQuotedString"
```

### Task 7: Uncomment golangci-lint in Makefile and verify clean

**Files:**
- Modify: `Makefile:12`

- [ ] **Step 1: Uncomment the lint line**

In `Makefile`, change line 12:

```makefile
# Replace:
	# golangci-lint run ./...
# With:
	golangci-lint run ./...
```

- [ ] **Step 2: Run full lint**

Run: `golangci-lint run ./...`
Expected: 0 issues (exit code 0).

- [ ] **Step 3: Run full test suite**

Run: `make test`
Expected: All checks passed.

- [ ] **Step 4: Commit**

```bash
git add Makefile
git commit -m "lint: enable golangci-lint in make test"
```

---

## PR2: Pointer Fixes

### Task 8: Convert ai package params to pointers

**Files:**
- Modify: `internal/ai/client.go:86,134,163,220,247,402`
- Modify: `internal/ai/client_test.go:61,92,120,144,208`
- Modify: `internal/ai/client_tool_test.go:35,89`
- Modify: `internal/jobs/evaluate.go:123`
- Modify: `internal/jobs/coach.go:95`
- Modify: `internal/jobs/educator.go:124`

- [ ] **Step 1: Change StreamAndLog signature**

In `internal/ai/client.go:86`:

```go
// Replace:
func (c *Client) StreamAndLog(ctx context.Context, p StreamParams) (_ *TokenStream, err error) {
// With:
func (c *Client) StreamAndLog(ctx context.Context, p *StreamParams) (_ *TokenStream, err error) {
```

- [ ] **Step 2: Update StreamAndLog callers**

In `internal/ai/client_test.go`, update all 4 call sites (lines 61, 92, 120, 144):

```go
// Replace:
stream, err := client.StreamAndLog(context.Background(), StreamParams{
// With:
stream, err := client.StreamAndLog(context.Background(), &StreamParams{
```

In `internal/backend/session.go:445` — `StreamLLM` still takes `p ai.StreamParams` by value at this point (Task 10 changes it later). Add `&` now:

```go
// Replace:
return b.llm.StreamAndLog(ctx, p)
// With:
return b.llm.StreamAndLog(ctx, &p)
```

- [ ] **Step 3: Change CallToolAndLog signature**

In `internal/ai/client.go:134`:

```go
// Replace:
func (c *Client) CallToolAndLog(ctx context.Context, tx pgx.Tx, p CallToolParams) (_ json.RawMessage, err error) {
// With:
func (c *Client) CallToolAndLog(ctx context.Context, tx pgx.Tx, p *CallToolParams) (_ json.RawMessage, err error) {
```

- [ ] **Step 4: Update CallToolAndLog callers**

In `internal/jobs/evaluate.go:123`:

```go
// Replace:
toolInput, err := w.LLM.CallToolAndLog(ctx, tx, ai.CallToolParams{
// With:
toolInput, err := w.LLM.CallToolAndLog(ctx, tx, &ai.CallToolParams{
```

Same pattern for `internal/jobs/coach.go:95` and `internal/jobs/educator.go:124`.

In `internal/ai/client_tool_test.go:35,89`:

```go
// Replace:
toolInput, err := client.CallToolAndLog(context.Background(), nil, CallToolParams{
// With:
toolInput, err := client.CallToolAndLog(context.Background(), nil, &CallToolParams{
```

- [ ] **Step 5: Change CallAndLog signature**

In `internal/ai/client.go:220`:

```go
// Replace:
func (c *Client) CallAndLog(ctx context.Context, tx pgx.Tx, p CallParams) (_ string, err error) {
// With:
func (c *Client) CallAndLog(ctx context.Context, tx pgx.Tx, p *CallParams) (_ string, err error) {
```

- [ ] **Step 6: Update CallAndLog callers**

In `internal/ai/client_test.go:208`:

```go
// Replace:
text, err := client.CallAndLog(context.Background(), nil, CallParams{
// With:
text, err := client.CallAndLog(context.Background(), nil, &CallParams{
```

- [ ] **Step 7: Change persistCall signature**

In `internal/ai/client.go:402`:

```go
// Replace:
func persistCall(ctx context.Context, q *db.Queries, p persistParams) error {
// With:
func persistCall(ctx context.Context, q *db.Queries, p *persistParams) error {
```

- [ ] **Step 8: Update persistCall callers**

In `internal/ai/client.go:256` (inside CallAndLog):

```go
// Replace:
err = persistCall(ctx, db.New(tx), persistParams{
// With:
err = persistCall(ctx, db.New(tx), &persistParams{
```

Lines 341 and 353 call `ts.persistParams()` which returns `persistParams` by value. Change to take its address:

```go
// line 341: replace
return persistCall(ctx, db.New(ts.pool), ts.persistParams())
// with
p := ts.persistParams()
return persistCall(ctx, db.New(ts.pool), &p)

// line 353: same pattern
p := ts.persistParams()
return persistCall(ctx, db.New(tx), &p)
```

- [ ] **Step 9: Fix rangeValCopy in ai/client.go**

Lines 163 and 247 both iterate `resp.Content` (648 bytes per element):

```go
// Replace (line 163):
for _, block := range resp.Content {
// With:
for i := range resp.Content {
    block := &resp.Content[i]
```

```go
// Replace (line 247):
for _, block := range resp.Content {
// With:
for i := range resp.Content {
    block := &resp.Content[i]
```

- [ ] **Step 10: Run tests**

Run: `go test ./internal/ai/... ./internal/jobs/... -count=1 -short`
Expected: PASS

- [ ] **Step 11: Commit**

```bash
git add internal/ai/client.go internal/ai/client_test.go internal/ai/client_tool_test.go internal/backend/session.go internal/jobs/evaluate.go internal/jobs/coach.go internal/jobs/educator.go
git commit -m "lint: pass ai package params by pointer to fix hugeParam"
```

### Task 9: Convert backend/billing params to pointers

**Files:**
- Modify: `internal/backend/billing.go:238,305,348`
- Modify: `internal/handler/billing.go:46`
- Modify: `internal/backend/billing_test.go` (many call sites)

- [ ] **Step 1: Change HandleStripeWebhook signature**

In `internal/backend/billing.go:238`:

```go
// Replace:
func (b *Backend) HandleStripeWebhook(ctx context.Context, event stripe.Event) (err error) {
// With:
func (b *Backend) HandleStripeWebhook(ctx context.Context, event *stripe.Event) (err error) {
```

- [ ] **Step 2: Change handleCheckoutCompleted and handleInvoicePaid signatures**

```go
// line 305: replace
func (b *Backend) handleCheckoutCompleted(ctx context.Context, event stripe.Event) (err error) {
// with
func (b *Backend) handleCheckoutCompleted(ctx context.Context, event *stripe.Event) (err error) {

// line 348: replace
func (b *Backend) handleInvoicePaid(ctx context.Context, event stripe.Event) (err error) {
// with
func (b *Backend) handleInvoicePaid(ctx context.Context, event *stripe.Event) (err error) {
```

- [ ] **Step 3: Update internal callers in billing.go**

Lines 244 and 246 pass `event` to the sub-handlers. Since `event` is now `*stripe.Event` in the parent, these calls need no change — they already pass the pointer through.

- [ ] **Step 4: Update handler/billing.go caller**

In `internal/handler/billing.go:46`:

```go
// Replace:
if err := b.HandleStripeWebhook(r.Context(), event); err != nil {
// With:
if err := b.HandleStripeWebhook(r.Context(), &event); err != nil {
```

- [ ] **Step 5: Update billing_test.go callers**

Every call to `b.HandleStripeWebhook(ctx, event)` becomes `b.HandleStripeWebhook(ctx, &event)`. There are ~16 call sites. For lines where `event` is already a variable, add `&`. For lines constructing inline, wrap with `&`.

- [ ] **Step 6: Run tests**

Run: `go test ./internal/backend/... ./internal/handler/... -count=1 -short`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/backend/billing.go internal/handler/billing.go internal/backend/billing_test.go
git commit -m "lint: pass stripe.Event by pointer to fix hugeParam"
```

### Task 10: Convert backend/oauth and backend/session params to pointers

**Files:**
- Modify: `internal/backend/oauth.go:41,45,189`
- Modify: `internal/backend/oauth_test.go` (many call sites)
- Modify: `internal/handler/oauth.go:57`
- Modify: `internal/backend/session.go:215,239,246,405`
- Modify: `internal/interview/conductor.go:452,487,566`

- [ ] **Step 1: Change OAuthLogin and oauthLoginWithRetry signatures**

In `internal/backend/oauth.go`:

```go
// line 41: replace
func (b *Backend) OAuthLogin(ctx context.Context, p OAuthLoginParams) (*OAuthLoginResult, error) {
// with
func (b *Backend) OAuthLogin(ctx context.Context, p *OAuthLoginParams) (*OAuthLoginResult, error) {

// line 45: replace
func (b *Backend) oauthLoginWithRetry(ctx context.Context, p OAuthLoginParams, isRetry bool) (_ *OAuthLoginResult, err error) {
// with
func (b *Backend) oauthLoginWithRetry(ctx context.Context, p *OAuthLoginParams, isRetry bool) (_ *OAuthLoginResult, err error) {
```

- [ ] **Step 2: Change createSessionInTx — user param to pointer**

In `internal/backend/oauth.go:189`:

```go
// Replace:
func (b *Backend) createSessionInTx(ctx context.Context, tx pgx.Tx, queries *db.Queries, user db.User, p OAuthLoginParams) (_ *OAuthLoginResult, err error) {
// With:
func (b *Backend) createSessionInTx(ctx context.Context, tx pgx.Tx, queries *db.Queries, user *db.User, p *OAuthLoginParams) (_ *OAuthLoginResult, err error) {
```

- [ ] **Step 3: Update internal callers in oauth.go**

Line 42 (`OAuthLogin` calls `oauthLoginWithRetry`): `p` is already `*OAuthLoginParams`, no change needed.

Lines 77, 128, 176 (`oauthLoginWithRetry` calls `createSessionInTx`): `user` is a local `db.User` variable. Change to `&user`. `p` is already `*OAuthLoginParams`, no change needed.

Lines 123, 152, 171 (recursive calls to `OAuthLogin`/`oauthLoginWithRetry`): `p` is already `*OAuthLoginParams`, no change needed.

- [ ] **Step 4: Update handler/oauth.go caller**

In `internal/handler/oauth.go:57`:

```go
// Replace:
result, err := b.OAuthLogin(r.Context(), backend.OAuthLoginParams{
// With:
result, err := b.OAuthLogin(r.Context(), &backend.OAuthLoginParams{
```

- [ ] **Step 5: Update oauth_test.go callers**

Every `b.OAuthLogin(ctx, backend.OAuthLoginParams{...})` becomes `b.OAuthLogin(ctx, &backend.OAuthLoginParams{...})`. For the one using a `params` variable (line 126, 131): `b.OAuthLogin(ctx, &params)`.

- [ ] **Step 6: Change PersistMessage, persistMessage, PersistInterviewerTurn signatures**

In `internal/backend/session.go`:

```go
// line 215: replace
func (b *Backend) persistMessage(ctx context.Context, dbtx db.DBTX, p PersistMessageParams) (_ db.Message, err error) {
// with
func (b *Backend) persistMessage(ctx context.Context, dbtx db.DBTX, p *PersistMessageParams) (_ db.Message, err error) {

// line 239: replace
func (b *Backend) PersistMessage(ctx context.Context, p PersistMessageParams) (db.Message, error) {
// with
func (b *Backend) PersistMessage(ctx context.Context, p *PersistMessageParams) (db.Message, error) {

// line 246: replace
func (b *Backend) PersistInterviewerTurn(ctx context.Context, stream *ai.TokenStream, p PersistMessageParams) (_ db.Message, err error) {
// with
func (b *Backend) PersistInterviewerTurn(ctx context.Context, stream *ai.TokenStream, p *PersistMessageParams) (_ db.Message, err error) {
```

- [ ] **Step 7: Change StreamLLM signature**

In `internal/backend/session.go:405`:

```go
// Replace:
func (b *Backend) StreamLLM(ctx context.Context, p ai.StreamParams) (_ *ai.TokenStream, err error) {
// With:
func (b *Backend) StreamLLM(ctx context.Context, p *ai.StreamParams) (_ *ai.TokenStream, err error) {
```

- [ ] **Step 8: Update internal callers in session.go**

Line 293 (`PersistInterviewerTurn` calls `persistMessage`): `p` is already `*PersistMessageParams`, no change needed.

Line 445 (`StreamLLM` calls `StreamAndLog`): Task 8 added `&p` here when `p` was a value. Now that `StreamLLM` takes `*ai.StreamParams`, `p` is already a pointer. Remove the `&`:

```go
// Replace (from Task 8):
return b.llm.StreamAndLog(ctx, &p)
// With:
return b.llm.StreamAndLog(ctx, p)
```

- [ ] **Step 9: Update conductor.go callers**

In `internal/interview/conductor.go`:

```go
// line 452: replace
stream, err := c.backend.StreamLLM(ctx, ai.StreamParams{
// with
stream, err := c.backend.StreamLLM(ctx, &ai.StreamParams{

// line 487: replace
interviewerMsg, err := c.backend.PersistInterviewerTurn(ctx, stream, backend.PersistMessageParams{
// with
interviewerMsg, err := c.backend.PersistInterviewerTurn(ctx, stream, &backend.PersistMessageParams{

// line 566: replace
msg, err := c.backend.PersistMessage(ctx, backend.PersistMessageParams{
// with
msg, err := c.backend.PersistMessage(ctx, &backend.PersistMessageParams{
```

- [ ] **Step 10: Run tests**

Run: `go test ./internal/backend/... ./internal/handler/... ./internal/interview/... -count=1 -short`
Expected: PASS

- [ ] **Step 11: Commit**

```bash
git add internal/backend/oauth.go internal/backend/oauth_test.go internal/backend/session.go internal/handler/oauth.go internal/interview/conductor.go
git commit -m "lint: pass backend oauth/session params by pointer to fix hugeParam"
```

### Task 11: Convert prompt-building functions to pointer params

**Files:**
- Modify: `internal/evaluation/prompt.go:83,91,98`
- Modify: `internal/evaluation/prompt_test.go`
- Modify: `internal/educator/prompt.go:49,58,68,71,78`
- Modify: `internal/educator/prompt_test.go`
- Modify: `internal/interview/prompt.go:91`
- Modify: `internal/interview/prompt_test.go`
- Modify: `internal/interview/messages.go:21`
- Modify: `internal/interview/conductor.go:316,430`
- Modify: `internal/jobs/evaluate.go:107`
- Modify: `internal/jobs/educator.go:113`
- Modify: `internal/coach/prompt.go:77,89`
- Modify: `internal/jobs/coach.go:67`

- [ ] **Step 1: Change evaluation/prompt.go signatures**

```go
// line 83: replace
func BuildPrompt(question db.Question, messages []db.Message) (string, []anthropic.MessageParam) {
// with
func BuildPrompt(question *db.Question, messages []db.Message) (string, []anthropic.MessageParam) {

// line 91: replace
func buildTranscript(question db.Question, messages []db.Message) string {
// with
func buildTranscript(question *db.Question, messages []db.Message) string {
```

- [ ] **Step 2: Fix rangeValCopy in evaluation/prompt.go**

Line 98:

```go
// Replace:
for _, m := range messages {
// With:
for i := range messages {
    m := &messages[i]
```

- [ ] **Step 3: Update evaluation callers**

In `internal/jobs/evaluate.go:107`: The code constructs a `db.Question` inline. Change to pass its address:

```go
// Replace:
system, userMsgs := evaluation.BuildPrompt(db.Question{
// With:
system, userMsgs := evaluation.BuildPrompt(&db.Question{
```

In `internal/evaluation/prompt_test.go`: Update all `BuildPrompt(q, msgs)` calls to `BuildPrompt(&q, msgs)`.

- [ ] **Step 4: Change educator/prompt.go signatures**

```go
// line 49: replace
func BuildPrompt(question db.Question, messages []db.Message, eval db.Evaluation) (string, []anthropic.MessageParam) {
// with
func BuildPrompt(question *db.Question, messages []db.Message, eval *db.Evaluation) (string, []anthropic.MessageParam) {

// line 58: replace
func buildSystemPrompt(question db.Question) string {
// with
func buildSystemPrompt(question *db.Question) string {

// line 68: replace
func buildTranscript(question db.Question, messages []db.Message) string {
// with
func buildTranscript(question *db.Question, messages []db.Message) string {

// line 78: replace
func buildEvaluationSummary(eval db.Evaluation) string {
// with
func buildEvaluationSummary(eval *db.Evaluation) string {
```

- [ ] **Step 5: Fix rangeValCopy in educator/prompt.go**

Line 71:

```go
// Replace:
for i, msg := range messages {
// With:
for i := range messages {
    msg := &messages[i]
```

- [ ] **Step 6: Update internal callers in educator/prompt.go**

Lines 50-51 call `buildSystemPrompt(question)`, `buildTranscript(question, messages)`, `buildEvaluationSummary(eval)`. Since `question` and `eval` are now pointers in `BuildPrompt`, these pass through directly — no change needed.

- [ ] **Step 7: Update educator callers**

In `internal/jobs/educator.go:113`:

```go
// Replace:
system, promptMsgs := educator.BuildPrompt(question, messages, eval)
// With:
system, promptMsgs := educator.BuildPrompt(&question, messages, &eval)
```

In `internal/educator/prompt_test.go`: Update `BuildPrompt(makeQuestion(), ...)` calls. Since `makeQuestion()` returns by value, wrap: `BuildPrompt(ptrTo(makeQuestion()), ..., ptrTo(makeEval()))` — or simpler, assign to a variable first and pass `&`.

```go
// Example pattern:
q := makeQuestion()
e := makeEval()
system, msgs := BuildPrompt(&q, makeMessages(), &e)
```

Same for `buildTranscript`, `buildEvaluationSummary` test calls.

- [ ] **Step 8: Change interview/prompt.go and interview/messages.go signatures**

```go
// interview/prompt.go line 91: replace
func (b *PromptBuilder) WithQuestion(q db.Question) *PromptBuilder {
// with
func (b *PromptBuilder) WithQuestion(q *db.Question) *PromptBuilder {

// interview/messages.go line 21: replace
func msgSessionLoaded(sessionID uuid.UUID, question db.Question, durationMin int, ttsEnabled bool) map[string]any {
// with
func msgSessionLoaded(sessionID uuid.UUID, question *db.Question, durationMin int, ttsEnabled bool) map[string]any {
```

- [ ] **Step 9: Update interview callers**

In `internal/interview/conductor.go`:

```go
// line 316: msgSessionLoaded — c.question is db.Question, pass &c.question
c.send(ctx, msgSessionLoaded(c.sessionID, &c.question, int(c.duration.Minutes()), c.ttsEnabled))

// line 430: WithQuestion — pass &c.question
.WithQuestion(&c.question).
```

In `internal/interview/prompt_test.go`: Every `.WithQuestion(db.Question{...})` becomes `.WithQuestion(&db.Question{...})`.

- [ ] **Step 10: Fix rangeValCopy in coach/prompt.go and jobs/**

In `internal/coach/prompt.go`:

```go
// line 77: replace
for _, q := range questions {
// with
for i := range questions {
    q := &questions[i]

// line 89: replace
for _, s := range sessions {
// with
for i := range sessions {
    s := &sessions[i]
```

In `internal/jobs/coach.go:67`:

```go
// Replace:
for i, s := range sessions {
// With:
for i := range sessions {
    s := &sessions[i]
```

In `internal/jobs/evaluate.go:94`:

```go
// Replace:
for _, m := range messages {
// With:
for i := range messages {
    m := &messages[i]
```

- [ ] **Step 11: Run tests**

Run: `go test ./internal/evaluation/... ./internal/educator/... ./internal/interview/... ./internal/coach/... ./internal/jobs/... -count=1 -short`
Expected: PASS

- [ ] **Step 12: Commit**

```bash
git add internal/evaluation/prompt.go internal/evaluation/prompt_test.go internal/educator/prompt.go internal/educator/prompt_test.go internal/interview/prompt.go internal/interview/prompt_test.go internal/interview/messages.go internal/interview/conductor.go internal/jobs/evaluate.go internal/jobs/educator.go internal/jobs/coach.go internal/coach/prompt.go
git commit -m "lint: pass db.Question/Evaluation by pointer in prompt builders"
```

### Task 12: Convert remaining hugeParam sites and add nolints

**Files:**
- Modify: `internal/handler/oauth.go:87`
- Modify: `internal/handler/oauth_flow_test.go:24,82,126,164`
- Modify: `internal/interview/conductor.go:321`
- Modify: `internal/interview/ws_message.go:86,94`
- Modify: `internal/billing/entitlement.go:39,44,49`
- Modify: `internal/drilotel/sloghandler.go:30`
- Modify: `internal/rpc/question/server.go:92,108`
- Modify: `internal/rpc/user/server.go:72,148`

- [ ] **Step 1: Change resolveDisplayName to take *goth.User**

In `internal/handler/oauth.go:87`:

```go
// Replace:
func resolveDisplayName(u goth.User) string {
// With:
func resolveDisplayName(u *goth.User) string {
```

Update caller at `internal/handler/oauth.go:55`:

```go
// Replace:
displayName := resolveDisplayName(gothUser)
// With:
displayName := resolveDisplayName(&gothUser)
```

- [ ] **Step 2: Change setupGothForTest to take *goth.User**

In `internal/handler/oauth_flow_test.go:24`:

```go
// Replace:
func setupGothForTest(t *testing.T, user goth.User) {
// With:
func setupGothForTest(t *testing.T, user *goth.User) {
```

Update callers at lines 82, 164:

```go
// Replace:
setupGothForTest(t, goth.User{
// With:
setupGothForTest(t, &goth.User{
```

Update caller at line 126:

```go
// Replace:
setupGothForTest(t, oauthUser)
// With:
setupGothForTest(t, &oauthUser)
```

Also update the closure inside setupGothForTest that captures `user` — since `user` is now a pointer, the `gothic.CompleteUserAuth` closure on line 36 returns `*user` (dereferenced):

```go
// line 36: replace
gothic.CompleteUserAuth = func(w http.ResponseWriter, r *http.Request) (goth.User, error) {
    return user, nil
}
// with
gothic.CompleteUserAuth = func(w http.ResponseWriter, r *http.Request) (goth.User, error) {
    return *user, nil
}
```

- [ ] **Step 3: Change endTurn to take *WSMessage**

In `internal/interview/conductor.go:321`:

```go
// Replace:
func (c *Conductor) endTurn(ctx context.Context, msg WSMessage) (err error) {
// With:
func (c *Conductor) endTurn(ctx context.Context, msg *WSMessage) (err error) {
```

Update caller at `internal/interview/conductor.go:211`:

```go
// Replace:
if err := c.endTurn(workCtx, msg); err != nil {
// With:
if err := c.endTurn(workCtx, &msg); err != nil {
```

- [ ] **Step 4: Add nolint to WSMessage value receiver methods**

In `internal/interview/ws_message.go`:

```go
// line 86:
func (m WSMessage) Traceparent() string { //nolint:gocritic // value receiver intentional
// line 94:
func (m WSMessage) AudioExt() string { //nolint:gocritic // value receiver intentional
```

- [ ] **Step 5: Add nolint to Entitlements value receiver methods**

In `internal/billing/entitlement.go`:

```go
// line 39:
func (e Entitlements) CanStartSession(durationMinutes int) bool { //nolint:gocritic // small immutable struct
// line 44:
func (e Entitlements) DurationAllowed(durationMinutes int) bool { //nolint:gocritic // small immutable struct
// line 49:
func (e Entitlements) BalanceSufficient(durationMinutes int) bool { //nolint:gocritic // small immutable struct
```

- [ ] **Step 6: Add nolint to slog.Handler interface method**

In `internal/drilotel/sloghandler.go:30`:

```go
// Replace:
func (h *TraceHandler) Handle(ctx context.Context, r slog.Record) error {
// With:
func (h *TraceHandler) Handle(ctx context.Context, r slog.Record) error { //nolint:gocritic // slog.Handler interface
```

- [ ] **Step 7: Change questionToProto to take pointer**

In `internal/rpc/question/server.go:108`:

```go
// Replace:
func questionToProto(row db.ListQuestionsForUserRow) *drillv1.Question {
// With:
func questionToProto(row *db.ListQuestionsForUserRow) *drillv1.Question {
```

Fix rangeValCopy and update caller at line 92:

```go
// Replace:
for i, row := range page {
    questions[i] = questionToProto(row)
}
// With:
for i := range page {
    questions[i] = questionToProto(&page[i])
}
```

- [ ] **Step 8: Change grantToProto to take pointer**

In `internal/rpc/user/server.go:148`:

```go
// Replace:
func grantToProto(g db.ListActiveGrantsRow) *drillv1.Grant {
// With:
func grantToProto(g *db.ListActiveGrantsRow) *drillv1.Grant {
```

Update caller at line 72:

```go
// Replace:
grants[i] = grantToProto(g)
// With — also need to check if this is a range loop:
```

Check: line 72 is inside a range loop. Update to index:

```go
for i := range rows {
    grants[i] = grantToProto(&rows[i])
}
```

- [ ] **Step 9: Run full lint**

Run: `golangci-lint run ./...`
Expected: 0 issues.

- [ ] **Step 10: Run full test suite**

Run: `go test ./internal/... ./cmd/... -race -count=1 -short`
Expected: PASS

- [ ] **Step 11: Commit**

```bash
git add internal/handler/oauth.go internal/handler/oauth_flow_test.go internal/interview/conductor.go internal/interview/ws_message.go internal/billing/entitlement.go internal/drilotel/sloghandler.go internal/rpc/question/server.go internal/rpc/user/server.go
git commit -m "lint: fix remaining hugeParam sites and add nolint for unfixable cases"
```
