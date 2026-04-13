# SaaS Starter Extraction Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extract a working generic SaaS starter from `/Users/btc/Projects/src/drill` into `/Users/btc/Projects/src/saas-starter` by forking, stripping drill-specific code, renaming identifiers, and validating end-to-end.

**Architecture:** Fork-and-strip. One-shot copy of drill tree → 9 sequential transformation phases (delete → compile-fix → schema squash → billing generalize → rename → frontend rewrite → infra parameterize → rename-script + README → validate). Each phase commits independently for bisection.

**Tech Stack:** Go 1.22+, ConnectRPC, buf, sqlc, Postgres, River queue, Stripe SDK, Mailgun, React 18, Vite, TypeScript, Terraform, GCP (Cloud Run + Cloud SQL + GCS + Secret Manager + AR + IAM), OpenTelemetry, Goth OAuth.

**Spec reference:** `docs/superpowers/specs/2026-04-13-saas-starter-extraction-design.md` (drill repo).

**Working directory convention:** All tasks run inside `~/Projects/src/saas-starter/` unless stated otherwise. Source paths without absolute prefix are relative to that directory.

---

## Phase 0: Setup

### Task 1: Copy drill tree into new location

**Files:**
- Create: `/Users/btc/Projects/src/saas-starter/` (entire tree)

- [ ] **Step 1: Create destination; verify it does not exist**

```bash
test ! -e /Users/btc/Projects/src/saas-starter || (echo "target exists" && exit 1)
```

- [ ] **Step 2: Copy drill tree, excluding .git, build artifacts, and stray binaries**

```bash
rsync -a \
  --exclude '.git' \
  --exclude 'node_modules' \
  --exclude 'tmp' \
  --exclude 'data' \
  --exclude 'web/dist' \
  --exclude 'v0' \
  --exclude 'coverage-cross.out' \
  --exclude 'cloud_bootstrap.log' \
  --exclude '/drill' \
  --exclude '/drillctl' \
  --exclude '/stripescenario' \
  --exclude '/sabermatic-production:*' \
  --exclude 'terraform/tfplan' \
  --exclude 'terraform/terraform.tfvars' \
  --exclude 'terraform/2' \
  --exclude 'terraform/3' \
  --exclude 'terraform/foo' \
  --exclude 'terraform/th' \
  --exclude 'docs/superpowers/specs' \
  --exclude 'docs/superpowers/plans' \
  /Users/btc/Projects/src/drill/ /Users/btc/Projects/src/saas-starter/
```

- [ ] **Step 3: Verify tree shape**

```bash
cd /Users/btc/Projects/src/saas-starter
ls -la | head -30
test -d internal && test -d web && test -d terraform && test -f go.mod && echo OK
```

- [ ] **Step 4: Init git, first baseline commit**

```bash
cd /Users/btc/Projects/src/saas-starter
git init -b main
git add -A
git commit -m "import: drill tree baseline for saas-starter extraction"
```

---

### Task 2: Harden .gitignore

**Files:**
- Modify: `.gitignore`

- [ ] **Step 1: Append starter-specific ignores**

Append to `.gitignore`:

```
# build artifacts / generated
/app
/appctl
/stripescenario
coverage-cross.out
cloud_bootstrap.log
tmp/
data/
web/dist/
node_modules/

# terraform
terraform/tfplan
terraform/terraform.tfvars
terraform/.terraform/
terraform/.terraform.lock.hcl

# env
.env
.env.local
```

- [ ] **Step 2: Verify no ignored tracked files**

```bash
git ls-files -i -c --exclude-standard
```

Expected: no output.

- [ ] **Step 3: Commit**

```bash
git add .gitignore
git commit -m "chore: tighten .gitignore for build artifacts and env"
```

---

## Phase 1: Delete-only

Repo does not compile after this phase. That is expected.

### Task 3: Delete app-specific backend packages and internal/ai STT/TTS

**Files:**
- Delete: `internal/coach/`, `internal/educator/`, `internal/evaluation/`, `internal/imagegen/`, `internal/interview/`, `internal/sample/`, `internal/branding/`
- Delete: `internal/ai/stt.go`, `internal/ai/stt_test.go` (if exists), `internal/ai/tts.go`, `internal/ai/tts_test.go` (if exists)

- [ ] **Step 1: Delete packages**

```bash
rm -rf internal/coach internal/educator internal/evaluation internal/imagegen internal/interview internal/sample internal/branding
rm -f internal/ai/stt.go internal/ai/stt_test.go internal/ai/tts.go internal/ai/tts_test.go
```

- [ ] **Step 2: Verify deletion**

```bash
ls internal/ | grep -E '^(coach|educator|evaluation|imagegen|interview|sample|branding)$' && exit 1 || echo OK
ls internal/ai/ | grep -E '^(stt|tts)' && exit 1 || echo OK
```

- [ ] **Step 3: Commit**

```bash
git add -A
git commit -m "rm: app-specific backend packages and ai stt/tts"
```

---

### Task 4: Delete app-specific RPC services

**Files:**
- Delete: `internal/rpc/coach/`, `educator/`, `evaluation/`, `interview/`, `question/`, `sample/`, `session/`

- [ ] **Step 1: Delete services**

```bash
rm -rf internal/rpc/coach internal/rpc/educator internal/rpc/evaluation internal/rpc/interview internal/rpc/question internal/rpc/sample internal/rpc/session
```

- [ ] **Step 2: Confirm surviving services**

```bash
ls internal/rpc/
```

Expected output includes: `auth`, `billing`, `user`, `interceptor.go`, `register.go`. No `coach/educator/evaluation/interview/question/sample/session`.

- [ ] **Step 3: Commit**

```bash
git add -A
git commit -m "rm: app-specific rpc services (coach, educator, evaluation, interview, question, sample, session)"
```

---

### Task 5: Delete app-specific jobs

**Files:**
- Delete: `internal/jobs/coach.go`, `educator.go`, `evaluate.go`, `evaluate_test.go`, `generate_image.go`, `generate_image_test.go`, `sweep_images.go`, `cleanup.go`, `cleanup_test.go`, `cleanup_generating.go`, `integration_test.go`

- [ ] **Step 1: Delete files**

```bash
cd internal/jobs
rm -f coach.go educator.go evaluate.go evaluate_test.go generate_image.go generate_image_test.go sweep_images.go cleanup.go cleanup_test.go cleanup_generating.go integration_test.go
cd ../..
```

- [ ] **Step 2: List survivors**

```bash
ls internal/jobs/
```

Expected: `error_handler.go`, `error_handler_test.go`, `jobtest/`, `main_test.go`, `queues.go`, `render_email_test.go`, `send_email.go`, `send_email_test.go`, `workers.go`.

- [ ] **Step 3: Audit jobtest/ for fixtures that only served deleted workers**

```bash
ls internal/jobs/jobtest/
grep -l -r "Coach\|Educator\|Evaluate\|GenerateImage\|SweepImages\|CleanupAbandoned\|CleanupStale" internal/jobs/jobtest/ || echo "jobtest/ clean"
```

If any file only references deleted worker types, delete it. Keep fixtures used by `send_email_test.go`, `error_handler_test.go`, `render_email_test.go`.

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "rm: app-specific jobs (coach, educator, evaluate, imagegen, sweep, cleanup workers)"
```

---

### Task 6: Delete app-specific backend façade files

**Files:**
- Delete: `internal/backend/coach.go`, `educator.go`, `evaluation.go`, `question.go`, `session.go`, `turn.go`, `turn_sink.go`, plus matching `*_test.go`

- [ ] **Step 1: Delete files**

```bash
cd internal/backend
rm -f coach.go coach_test.go educator.go educator_test.go evaluation.go evaluation_test.go question.go question_test.go session.go session_test.go turn.go turn_test.go turn_sink.go turn_sink_test.go
cd ../..
```

- [ ] **Step 2: List survivors**

```bash
ls internal/backend/
```

Expected: `auth.go` (+ test), `backend.go`, `billing.go` (+ test), `oauth.go` (+ test), `user.go` (+ test).

- [ ] **Step 3: Commit**

```bash
git add -A
git commit -m "rm: app-specific backend facade methods (coach, educator, evaluation, question, session, turn, turn_sink)"
```

---

### Task 7: Delete app-specific protos

**Files:**
- Delete: `pb/drill/v1/coach.proto`, `educator.proto`, `evaluation.proto`, `interview.proto`, `question.proto`, `sample.proto`, `session.proto`
- Delete: matching generated files under `internal/pb/drill/v1/` (for the above protos) and `web/src/pb/` (for the above)

- [ ] **Step 1: Delete protos**

```bash
cd pb/drill/v1
rm -f coach.proto educator.proto evaluation.proto interview.proto question.proto sample.proto session.proto
cd ../../..
```

- [ ] **Step 2: Delete generated Go**

```bash
cd internal/pb/drill/v1
rm -f coach.pb.go coach_vtproto.pb.go educator.pb.go educator_vtproto.pb.go evaluation.pb.go evaluation_vtproto.pb.go interview.pb.go interview_vtproto.pb.go question.pb.go question_vtproto.pb.go sample.pb.go sample_vtproto.pb.go session.pb.go session_vtproto.pb.go
rm -rf drillv1connect/coach* drillv1connect/educator* drillv1connect/evaluation* drillv1connect/interview* drillv1connect/question* drillv1connect/sample* drillv1connect/session*
cd ../../../..
```

Use glob-per-shell as needed; verify with `ls internal/pb/drill/v1/ | grep -E '^(coach|educator|evaluation|interview|question|sample|session)'` — expect empty.

- [ ] **Step 3: Delete generated TypeScript**

```bash
cd web/src/pb
rm -f coach_pb.* educator_pb.* evaluation_pb.* interview_pb.* question_pb.* sample_pb.* session_pb.* *-CoachService_connectquery.* *-EducatorService_connectquery.* *-EvaluationService_connectquery.* *-InterviewService_connectquery.* *-QuestionService_connectquery.* *-SampleService_connectquery.* *-SessionService_connectquery.*
cd ../../..
```

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "rm: app-specific protos and generated code"
```

---

### Task 8: Delete app-specific SQL queries

**Files:**
- Delete: `sql/queries/questions.sql`, `messages.sql`, `sessions.sql`, `coach_analyses.sql`, `educator_analyses.sql`, `evaluations.sql`, `interview.sql`

- [ ] **Step 1: Delete query files**

```bash
cd sql/queries
rm -f questions.sql messages.sql sessions.sql coach_analyses.sql educator_analyses.sql evaluations.sql interview.sql
cd ../..
```

- [ ] **Step 2: List survivors**

```bash
ls sql/queries/
```

Expected: `users.sql`, `oauth_accounts.sql`, `auth_sessions.sql`, `grants.sql`, `ledger_entries.sql`, `llm_calls.sql`, `advisory_locks.sql`, `user_events.sql` (whatever kept queries exist).

- [ ] **Step 3: Commit**

```bash
git add -A
git commit -m "rm: app-specific SQL queries (interview, coach, educator, evaluation, questions, messages)"
```

---

### Task 9: Delete app-specific frontend pages, components, hooks, lib

**Files:**
- Delete: `web/src/pages/interview.tsx`, `sample.tsx`, `session-config.tsx`, `session/`, `history.tsx`
- Delete: `web/src/components/recording-input.tsx`, `replay/`, `shader-orb.tsx`, `shader-orb-impl.tsx`, `waveform.tsx`
- Delete: `web/src/hooks/use-interview.ts`, `web/src/hooks/use-timer.ts`
- Delete: `web/src/lib/constants.ts`, `web/src/lib/score-utils.ts`
- Delete: drill-specific landing subcomponents

- [ ] **Step 1: Delete pages and components**

```bash
cd web/src
rm -f pages/interview.tsx pages/sample.tsx pages/session-config.tsx pages/history.tsx
rm -rf pages/session
rm -f components/recording-input.tsx components/shader-orb.tsx components/shader-orb-impl.tsx components/waveform.tsx
rm -rf components/replay
cd ../..
```

- [ ] **Step 2: Delete hooks and lib**

```bash
cd web/src
rm -f hooks/use-interview.ts hooks/use-timer.ts
rm -f lib/constants.ts lib/score-utils.ts
cd ../..
```

- [ ] **Step 3: Delete drill-specific landing subcomponents**

```bash
cd web/src/pages/landing
rm -f annotations.tsx coaching.tsx deep-dive.tsx sample-session.tsx scoring.tsx strengths-gaps.tsx voice-pipeline.tsx
ls
cd ../../../..
```

Expected survivors in `landing/`: `index.tsx`, `hero.tsx`, `cta-repeat.tsx`, `credits.tsx`.

- [ ] **Step 4: Delete drill/sabermatic web/public assets**

```bash
cd web/public
rm -f mark-1024.png mark-180.png mark-192.png mark-256.png mark-512.png mark.svg
# og- and favicon files will be replaced in Phase 6; leave for now
cd ../..
```

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "rm: app-specific frontend pages, components, hooks, lib, and drill mark assets"
```

---

### Task 10: Delete drill-era documentation

**Files:**
- Delete: any drill-specific `docs/*.pdf` or `docs/*.md`

- [ ] **Step 1: List docs tree and decide**

```bash
ls docs/
```

Any drill-flavored `.md` or `.pdf` (e.g., "How Claude, ChatGPT, and Gemini actually work under the hood.pdf") must be removed. Keep generic topics if any exist (none expected in a fresh starter).

- [ ] **Step 2: Delete all doc files except directory skeleton**

```bash
find docs -type f \( -name '*.md' -o -name '*.pdf' \) ! -path 'docs/superpowers/*' -delete
# Remove empty docs/superpowers subdirs if they came over
rm -rf docs/superpowers
```

The starter ships without design history. The new README (Phase 8) replaces.

- [ ] **Step 3: Commit**

```bash
git add -A
git commit -m "rm: drill-era documentation and superpowers history"
```

---

## Phase 2: Compile-fixing surgery

Now restore compilability. `go build ./...` must pass at end of this phase; many tests will still fail (schema not yet updated) — that's OK.

### Task 11: Trim internal/backend/backend.go

**Files:**
- Modify: `internal/backend/backend.go`

- [ ] **Step 1: Read the file and identify the Backend struct + New() function**

Struct (confirmed from spec): has fields `pool`, `jobs`, `riverUI`, `closeRiverUI`, `cfg`, `llm`, `stt`, `tts`, `store`, `SampleService`.

- [ ] **Step 2: Remove `stt`, `tts`, `SampleService` fields from Backend struct**

The struct becomes:

```go
type Backend struct {
	pool         *pgxpool.Pool
	jobs         Jobs
	riverUI      http.Handler
	closeRiverUI context.CancelFunc
	cfg          *config.Config
	llm          *ai.Client
	store        storage.Store
}
```

- [ ] **Step 3: Remove `samplesvc` import**

Delete the import: `samplesvc "github.com/btc/drill/internal/rpc/sample"`.

- [ ] **Step 4: In `New(cfg *config.Config)`: remove SampleService init block, STT client init, TTS client init**

Delete every block that constructs `ss`, `stt`, `tts`, and the struct-field assignments for `SampleService`, `stt`, `tts`.

- [ ] **Step 5: In `New`: remove all `jobs.RegisterWorkers` arguments for deleted workers**

The call must pass only the workers still registered (SendEmail and any image-gen worker if image-gen is kept). Update `workers.go` signature in Task 13; for now, call `RegisterWorkers(cfg, emailSender, pool, llmClient, geminiClient, store)` → `RegisterWorkers(cfg, emailSender)` (final signature set in Task 13).

- [ ] **Step 6: In `New`: remove `PeriodicJobs` entries for `CleanupAbandonedSessionsArgs`, `CleanupStaleGeneratingArgs`, `SweepMissingImagesArgs`**

The `PeriodicJobs` slice becomes empty; remove the slice entirely if River's `NewClient` tolerates no `PeriodicJobs` key, else pass an empty slice.

- [ ] **Step 7: In `New`: remove worker back-ref assignments**

Delete lines like `workerRefs.Evaluate.Jobs = riverClient`, `.Cleanup.Jobs = ...`, `.Coach.Jobs = ...`, `.SweepImages.Jobs = ...`. Keep only SendEmail back-ref if it exists.

- [ ] **Step 8: In `New`: drop `QueueAI` queue registration; keep `QueueGemini` (image-gen retained per Open Decision #1)**

- [ ] **Step 9: Don't run build yet — other files still broken**

Commit this file's changes alone.

```bash
git add internal/backend/backend.go
git commit -m "backend.New: strip stt/tts/SampleService; trim worker and periodic-job registration"
```

---

### Task 12: Trim internal/rpc/register.go

**Files:**
- Modify: `internal/rpc/register.go`

- [ ] **Step 1: Remove service registrations for deleted services**

Delete all lines registering `coach`, `educator`, `evaluation`, `interview`, `question`, `sample`, `session` ConnectRPC handlers. Delete corresponding imports at the top of the file.

- [ ] **Step 2: Keep only auth, billing, user registrations**

Verify remaining imports: only `authsvc`, `billingsvc`, `usersvc` (or whatever package aliases drill used) plus `drillv1connect`.

- [ ] **Step 3: Commit**

```bash
git add internal/rpc/register.go
git commit -m "rpc/register.go: drop deleted service registrations"
```

---

### Task 13: Trim internal/jobs/workers.go

**Files:**
- Modify: `internal/jobs/workers.go`

- [ ] **Step 1: Reduce `RegisterWorkers` signature**

The final signature:

```go
func RegisterWorkers(cfg *config.Config, sender email.Sender) (*river.Workers, *WorkerRefs) {
    workers := river.NewWorkers()
    river.AddWorker(workers, &SendEmailWorker{Sender: sender})
    // Image-gen: if retained per Open Decision #1, register here with geminiClient + store.
    // Currently deferred until image-gen decision is finalized at deploy time.
    refs := &WorkerRefs{}
    return workers, refs
}
```

- [ ] **Step 2: Reduce `WorkerRefs` struct**

```go
type WorkerRefs struct {
    // Currently unused; kept as a seam for downstream apps to attach job-to-job refs.
}
```

- [ ] **Step 3: Remove all imports no longer needed**

Drop `coach`, `educator`, `imagegen`, `evaluation` imports and related constructor calls.

- [ ] **Step 4: Commit**

```bash
git add internal/jobs/workers.go
git commit -m "jobs/workers.go: reduce RegisterWorkers to SendEmail only"
```

---

### Task 14: Trim internal/jobs/queues.go

**Files:**
- Modify: `internal/jobs/queues.go`

- [ ] **Step 1: Drop `QueueAI` constant and its queue config**

Find lines defining `QueueAI` (e.g., `const QueueAI = "ai"`) and remove. Also remove from any `QueueNames`/`DefaultQueues` map.

- [ ] **Step 2: Keep `QueueDefault`, `QueueEmail`, `QueueGemini`**

- [ ] **Step 3: Commit**

```bash
git add internal/jobs/queues.go
git commit -m "jobs/queues.go: drop QueueAI (no kept worker uses it)"
```

---

### Task 15: Trim email/template.go branding coupling

> **Dependency:** this task's Step 4 call-site update requires `cfg.ProductName` from Task 16. Execute Task 16 first, or combine the two into one commit.


**Files:**
- Modify: `internal/email/template.go`

- [ ] **Step 1: Remove import of `internal/branding`**

```go
// before
import "github.com/btc/drill/internal/branding"
// after
// (removed)
```

- [ ] **Step 2: Replace every `branding.AppName` with a template variable**

In the rendered context map, add `"ProductName": productName` where `productName` is a constructor parameter. Signature change:

```go
// before
func NewRenderer() *Renderer { return &Renderer{} }
// after
func NewRenderer(productName string) *Renderer { return &Renderer{productName: productName} }
```

Every call to `Render` threads `r.productName` into the template context as `.ProductName`.

- [ ] **Step 3: Replace `branding.AppName` references in templates**

Edit `internal/email/templates/wrapper.html` — replace every occurrence of `{{ .AppName }}` or literal "Sabermatic"/"Drill" with `{{ .ProductName }}`.

- [ ] **Step 4: Update constructor call site**

In `internal/backend/backend.go` or wherever `NewRenderer` is called, thread `cfg.ProductName` (added in Task 16).

- [ ] **Step 5: Commit**

```bash
git add internal/email/template.go internal/email/templates/wrapper.html internal/backend/backend.go
git commit -m "email: decouple from internal/branding; accept productName"
```

---

### Task 16: Add ProductName and remove Speech from Config

**Files:**
- Modify: `internal/config/config.go`

- [ ] **Step 1: Remove the `Speech` struct definition and field**

Delete:

```go
type Speech struct {
    OpenAIAPIKey string
    WhisperModel string
    TTSModel     string
}
```

and the `Speech Speech` field on `Config`.

- [ ] **Step 2: Remove the OPENAI_API_KEY validation**

In `config.Load`, find and delete:

```go
if cfg.Speech.OpenAIAPIKey == "" {
    return nil, errors.New("OPENAI_API_KEY is required")
}
```

- [ ] **Step 3: Collapse LLM model env vars**

Replace the 5 fields (`InterviewerModel`, `EvaluatorModel`, `CoachModel`, `EducatorModel`, `ImagePromptModel`) on the LLM struct with a single `Model string`. Env var: `LLM_MODEL` (default `"claude-sonnet-4-5"`).

- [ ] **Step 4: Add `ProductName` field**

On the top-level `Config` struct, add:

```go
ProductName string  // PRODUCT_NAME (default "App")
```

Load via `getenvDefault("PRODUCT_NAME", "App")`.

- [ ] **Step 5: Add `Auth.CookieName` field**

On `Config.Auth` struct:

```go
CookieName string  // AUTH_COOKIE_NAME (default "app_session")
```

- [ ] **Step 6: Update default `Log.File`, `Email.FromAddress`, `Email.MailgunDomain`**

Defaults:

```go
Log.File:             "data/logs/app.log"
Email.FromAddress:    "noreply@example.com"
Email.MailgunDomain:  "mg.example.com"
```

- [ ] **Step 7: Update Stripe pack price ID config**

Replace `STRIPE_PACK_120_PRICE_ID`, `STRIPE_PACK_300_PRICE_ID`, `STRIPE_PACK_600_PRICE_ID` with a single env var `STRIPE_PACK_PRICE_IDS` parsed as comma-separated `tier=priceID` pairs, materialized to `map[string]string` on `Config.Stripe.PackPriceIDs`.

```go
// Stripe.PackPriceIDs keyed by tier label (e.g., "small", "med", "large").
PackPriceIDs map[string]string
```

Parser:

```go
func parsePackPriceIDs(raw string) map[string]string {
    out := map[string]string{}
    for _, pair := range strings.Split(raw, ",") {
        pair = strings.TrimSpace(pair)
        if pair == "" {
            continue
        }
        k, v, ok := strings.Cut(pair, "=")
        if !ok {
            continue
        }
        out[strings.TrimSpace(k)] = strings.TrimSpace(v)
    }
    return out
}
```

- [ ] **Step 8: Commit**

```bash
git add internal/config/config.go
git commit -m "config: add ProductName/CookieName; remove Speech + OpenAI validator; collapse LLM_MODEL; generic Stripe pack map"
```

---

### Task 17: Trim handler/spa.go branding coupling

**Files:**
- Modify: `internal/handler/spa.go`, `internal/handler/spa_test.go`

- [ ] **Step 1: Remove import of `internal/branding`**

In `spa.go`:

```go
// remove
import "github.com/btc/drill/internal/branding"
```

- [ ] **Step 2: Introduce a `productName` field on the handler**

```go
type SPAHandler struct {
    productName string
    // ... existing fields
}

func NewSPAHandler(productName string, /* other deps */) *SPAHandler {
    return &SPAHandler{productName: productName /* ... */}
}
```

- [ ] **Step 3: Replace every `branding.AppName` with `h.productName`**

In OG title logic (was `title: branding.AppName`, etc.) → `title: h.productName`, `title: h.productName + " — sample evaluation"` → update per context but remove the sample evaluation suffix since sample page is deleted; the handler now has only the root OG tag.

- [ ] **Step 4: Update spa_test.go to inject a test product name**

```go
// before
branding.AppName
// after
"TestApp"
```

Pass `"TestApp"` as productName in the handler constructor. Update expected HTML assertions.

- [ ] **Step 5: Update handler registration site**

In `internal/handler/server.go` (or wherever `NewSPAHandler` is called), thread `cfg.ProductName`.

- [ ] **Step 6: Commit**

```bash
git add internal/handler/spa.go internal/handler/spa_test.go internal/handler/server.go
git commit -m "handler/spa: thread productName instead of branding.AppName"
```

---

### Task 18: Strip billing package app-specific Plan fields (preliminary)

**Files:**
- Modify: `internal/billing/plans.go`, `internal/billing/entitlement.go`

- [ ] **Step 1: In `plans.go`: remove drill-specific Plan fields**

Delete `FreeEducatorLimit`, `ConcurrentSessions`, `MaxDurationMinutes`, `CanStartSession` (if method). Keep `Name`, `FreeTrialMinutes` (renamed to `FreeTrialAmount` in Phase 4), and whatever generic fields remain.

- [ ] **Step 2: In `entitlement.go`: remove `CanStartSession` check**

Keep `Check(ctx, userID) (balance, ok)` as generic entitlement check.

- [ ] **Step 3: Delete `entitlement_test.go` sections that exercise session-based rules**

- [ ] **Step 4: Commit (intermediate — Phase 4 finishes the rename)**

```bash
git add internal/billing/
git commit -m "billing: strip drill-specific Plan fields (session rules, educator limit)"
```

---

### Task 19: Strip app-specific internal/backend/billing.go methods

**Files:**
- Modify: `internal/backend/billing.go`, `internal/backend/billing_test.go`

- [ ] **Step 1: Delete `reserveMinutesTx`, `FullRefundSessionMinutes`, and any session-keyed method**

Keep `Check`, `CheckTx`, `EnsureFreeGrant`, `EnsureFreeGrantTx`, `GetUsageSummary`, `CreateCheckoutSession`, `CreatePortalSession`, `HandleStripeWebhook`.

- [ ] **Step 2: In `HandleStripeWebhook`: replace `metadata["pack_minutes"]` key with `metadata["pack_amount"]` (or `metadata["pack_tier"]`)**

Decision: use `pack_tier` — the webhook looks up amount via `cfg.Stripe.PackPriceIDs` reverse map. Implement:

```go
tier := metadata["pack_tier"]
priceID := session.LineItems[0].PriceID
// Validate tier matches configured PackPriceIDs[tier] == priceID; bail if mismatch.
// Amount credited comes from a separate config: PackAmounts map[string]int {"small": 120, ...}
// (Or: store amount as session metadata too, and trust Stripe side.)
```

Simpler: use `metadata["pack_amount"]` — Stripe metadata stores the integer amount directly. This is what Phase 4 codifies.

- [ ] **Step 3: Update billing_test.go accordingly**

- [ ] **Step 4: Commit**

```bash
git add internal/backend/billing.go internal/backend/billing_test.go
git commit -m "backend/billing: strip reserveMinutes/refundSession; pack_minutes → pack_amount metadata key"
```

---

### Task 20: Fix ai/client.go SessionID surface

**Files:**
- Modify: `internal/ai/client.go`, `internal/ai/client_test.go`, `internal/ai/client_tool_test.go`

- [ ] **Step 1: Remove `SessionID uuid.UUID` field from `StreamParams`, `CallParams`, `CallToolParams`, and internal `persistParams`**

- [ ] **Step 2: Delete `pgtype.UUID` branches in `persistCall` that set `session_id`**

The `llm_calls` INSERT no longer binds `session_id` (column removed in Phase 3).

- [ ] **Step 3: Update `Role` field doc comment**

```go
// Role is a caller-defined tag for attributing this call (e.g., "chat", "summarize").
// Empty string is acceptable.
Role string
```

- [ ] **Step 4: Audit call sites**

```bash
grep -rn 'SessionID:' internal/ --include='*.go' | grep -v _test.go
```

Any remaining `SessionID:` in a `StreamParams`/`CallParams` literal must be removed.

- [ ] **Step 5: Update tests — drop SessionID from test params**

- [ ] **Step 6: Commit**

```bash
git add internal/ai/
git commit -m "ai/client: drop SessionID from param structs; rewrite Role comment as generic tag"
```

---

### Task 21: Remove usage_periods migration artifacts

**Files:**
- Modify: existing migration files — done in Phase 3 squash. Skip here.

---

### Task 22: Verify compile (or document remaining breakage)

- [ ] **Step 1: Run build**

```bash
go build ./... 2>&1 | tee /tmp/build-phase2.log | head -80
```

Expected: most packages compile. Remaining failures are expected in:
- `internal/db/` — generated code references tables that still exist in SQL but not in Go (fixed in Phase 3 sqlc regen).
- `cmd/drillctl/main.go` — `grant` command uses old `InitialMinutes` (fixed in Phase 4).

- [ ] **Step 2: If any OTHER failures, fix them now**

Common issue: unused imports in files that no longer reference deleted packages. Remove.

- [ ] **Step 3: Commit any surface cleanup**

```bash
git add -A
git commit -m "chore: clean up unused imports after Phase 2 surgery"
```

---

## Phase 3: Schema squash

Produce one canonical `001_initial` migration. Delete all existing drill migrations. Regenerate sqlc.

### Task 23: Write new 001_initial.up.sql

**Files:**
- Create: `sql/migrations/001_initial.up.sql` (replaces drill's)

- [ ] **Step 1: Remove all existing drill migrations**

```bash
rm -f sql/migrations/001_initial.up.sql sql/migrations/001_initial.down.sql
rm -f sql/migrations/002_* sql/migrations/003_* sql/migrations/004_* sql/migrations/005_* sql/migrations/006_* sql/migrations/007_* sql/migrations/008_* sql/migrations/009_* sql/migrations/010_* sql/migrations/011_* sql/migrations/012_*
ls sql/migrations/
```

- [ ] **Step 2: Write squashed `001_initial.up.sql`**

```sql
-- users: app identity. role is a coarse authorization bucket.
CREATE TABLE users (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email           TEXT NOT NULL UNIQUE,
    display_name    TEXT NOT NULL DEFAULT '',
    password_hash   TEXT,
    email_verified  BOOLEAN NOT NULL DEFAULT FALSE,
    role            TEXT NOT NULL DEFAULT 'user' CHECK (role IN ('user','admin')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- oauth_accounts: external identity providers linked to users.
CREATE TABLE oauth_accounts (
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider    TEXT NOT NULL,
    provider_id TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (provider, provider_id)
);

-- auth_sessions: server-side session records keyed by cookie secret.
CREATE TABLE auth_sessions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash  TEXT NOT NULL UNIQUE,
    expires_at  TIMESTAMPTZ NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_auth_sessions_user ON auth_sessions(user_id);

-- grants: buckets of billable units granted to a user from some source.
CREATE TABLE grants (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id           UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    source            TEXT NOT NULL, -- 'free_trial', 'purchase', 'admin'
    initial_amount    INT NOT NULL CHECK (initial_amount > 0),
    remaining_amount  INT NOT NULL CHECK (remaining_amount >= 0),
    expires_at        TIMESTAMPTZ,
    stripe_event_id   TEXT,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (remaining_amount <= initial_amount)
);
CREATE INDEX idx_grants_user_remaining ON grants(user_id)
    WHERE remaining_amount > 0;
CREATE UNIQUE INDEX idx_grants_stripe_event ON grants(stripe_event_id)
    WHERE stripe_event_id IS NOT NULL;

-- ledger_entries: one row per debit or credit against a grant.
CREATE TABLE ledger_entries (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    grant_id   UUID REFERENCES grants(id) ON DELETE SET NULL,
    amount     INT NOT NULL, -- negative = debit, positive = credit
    reason     TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_ledger_user_created ON ledger_entries(user_id, created_at DESC);

-- user_events: lightweight product analytics.
CREATE TABLE user_events (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID REFERENCES users(id) ON DELETE SET NULL,
    event_type TEXT NOT NULL,
    payload    JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_user_events_user_created ON user_events(user_id, created_at DESC);

-- llm_calls: observability for every LLM request.
CREATE TABLE llm_calls (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        UUID REFERENCES users(id) ON DELETE SET NULL,
    role           TEXT NOT NULL DEFAULT '', -- caller-defined tag
    model          TEXT NOT NULL,
    input_tokens   INT NOT NULL DEFAULT 0,
    output_tokens  INT NOT NULL DEFAULT 0,
    cost_usd       NUMERIC(10,6) NOT NULL DEFAULT 0,
    latency_ms     INT NOT NULL DEFAULT 0,
    error          TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_llm_calls_user_created ON llm_calls(user_id, created_at DESC);

-- llm_call_content: request/response blobs; split for cheaper scans.
CREATE TABLE llm_call_content (
    call_id  UUID PRIMARY KEY REFERENCES llm_calls(id) ON DELETE CASCADE,
    request  JSONB NOT NULL,
    response JSONB
);
```

- [ ] **Step 3: Write `001_initial.down.sql`**

```sql
DROP TABLE IF EXISTS llm_call_content;
DROP TABLE IF EXISTS llm_calls;
DROP TABLE IF EXISTS user_events;
DROP TABLE IF EXISTS ledger_entries;
DROP TABLE IF EXISTS grants;
DROP TABLE IF EXISTS auth_sessions;
DROP TABLE IF EXISTS oauth_accounts;
DROP TABLE IF EXISTS users;
```

- [ ] **Step 4: Commit**

```bash
git add sql/migrations/
git commit -m "sql: squash migrations to single 001_initial (generic schema, amount columns, no session FKs)"
```

---

### Task 24: Audit and update sql/queries

**Files:**
- Modify: `sql/queries/grants.sql`, `sql/queries/ledger_entries.sql`, `sql/queries/llm_calls.sql`, `sql/queries/user_events.sql`, `sql/queries/users.sql`, `sql/queries/auth_sessions.sql`, `sql/queries/oauth_accounts.sql`, `sql/queries/advisory_locks.sql`

- [ ] **Step 1: Rewrite `sql/queries/grants.sql`**

Rename all `initial_minutes`/`remaining_minutes` to `initial_amount`/`remaining_amount`. Delete `ReserveMinutes` and any query that references `interview_sessions`. Keep `EnsureFreeGrant` (rename param to `amount`), `DeductGrantAmount`, `GetRemainingAmount`, `ListGrants`.

Example (full file content must be valid sqlc — write out every query):

```sql
-- name: EnsureFreeGrant :one
-- Insert a free_trial grant if the user has none; idempotent.
INSERT INTO grants (user_id, source, initial_amount, remaining_amount)
SELECT @user_id::uuid, 'free_trial', @amount::int, @amount::int
WHERE NOT EXISTS (
    SELECT 1 FROM grants WHERE user_id = @user_id AND source = 'free_trial'
)
RETURNING id, user_id, source, initial_amount, remaining_amount, created_at;

-- name: ListUserGrants :many
SELECT id, user_id, source, initial_amount, remaining_amount, expires_at, created_at
FROM grants
WHERE user_id = @user_id
ORDER BY created_at DESC;

-- name: GetRemainingAmount :one
SELECT COALESCE(SUM(remaining_amount), 0)::int
FROM grants
WHERE user_id = @user_id
  AND remaining_amount > 0
  AND (expires_at IS NULL OR expires_at > NOW());

-- name: DeductGrantAmount :one
-- Deduct `amount` across oldest grants first; returns rows updated.
-- (Implementation: downstream caller iterates or uses a PL/pgSQL function.)
-- For simplicity, deduct from single oldest grant with sufficient balance.
UPDATE grants
SET remaining_amount = remaining_amount - @amount::int
WHERE id = (
    SELECT id FROM grants
    WHERE user_id = @user_id AND remaining_amount >= @amount::int
    ORDER BY created_at ASC
    LIMIT 1
)
RETURNING id, remaining_amount;

-- name: RecordPurchaseGrant :one
INSERT INTO grants (user_id, source, initial_amount, remaining_amount, stripe_event_id)
VALUES (@user_id, 'purchase', @amount::int, @amount::int, @stripe_event_id)
ON CONFLICT (stripe_event_id) WHERE stripe_event_id IS NOT NULL DO NOTHING
RETURNING id;
```

- [ ] **Step 2: Rewrite `sql/queries/ledger_entries.sql`**

```sql
-- name: RecordLedgerEntry :one
INSERT INTO ledger_entries (user_id, grant_id, amount, reason)
VALUES (@user_id, @grant_id, @amount, @reason)
RETURNING id, user_id, grant_id, amount, reason, created_at;

-- name: ListLedgerEntries :many
SELECT id, user_id, grant_id, amount, reason, created_at
FROM ledger_entries
WHERE user_id = @user_id
ORDER BY created_at DESC
LIMIT @limit_val::int;
```

- [ ] **Step 3: Rewrite `sql/queries/llm_calls.sql`**

Drop any queries filtering by `session_id`. Keep:

```sql
-- name: RecordLLMCall :one
INSERT INTO llm_calls (user_id, role, model, input_tokens, output_tokens, cost_usd, latency_ms, error)
VALUES (@user_id, @role, @model, @input_tokens, @output_tokens, @cost_usd, @latency_ms, @error)
RETURNING id, created_at;

-- name: RecordLLMCallContent :exec
INSERT INTO llm_call_content (call_id, request, response)
VALUES (@call_id, @request, @response);

-- name: GetUserLLMCalls :many
SELECT id, user_id, role, model, input_tokens, output_tokens, cost_usd, latency_ms, error, created_at
FROM llm_calls
WHERE user_id = @user_id
ORDER BY created_at DESC
LIMIT @limit_val::int;
```

- [ ] **Step 4: Audit `users.sql`, `oauth_accounts.sql`, `auth_sessions.sql`, `user_events.sql`, `advisory_locks.sql`**

Read each; rewrite any that reference deleted columns or tables. Expected: `users.sql` has `CreateUser`, `GetUserByEmail`, `GetUserByID`, `UpdateDisplayName`, `MarkEmailVerified`, `UpdatePasswordHash`, `DeleteUser`. `oauth_accounts.sql` has `UpsertOAuthAccount`, `GetByProviderAndID`. `auth_sessions.sql` has `CreateAuthSession`, `GetAuthSession`, `DeleteAuthSession`, `DeleteExpiredAuthSessions`. `user_events.sql` has `RecordUserEvent`. `advisory_locks.sql` has `TryAdvisoryLock`, `ReleaseAdvisoryLock`.

No changes needed for queries that don't reference stripped tables/columns.

- [ ] **Step 5: Commit**

```bash
git add sql/queries/
git commit -m "sql/queries: rename minutes→amount; drop session-keyed and reserve/refund queries"
```

---

### Task 25: Regenerate sqlc

**Files:**
- Regenerate: `internal/db/*.sql.go`

- [ ] **Step 1: Run sqlc**

```bash
sqlc generate
```

- [ ] **Step 2: Verify generated code compiles (in isolation)**

```bash
go build ./internal/db/...
```

Expected: pass.

- [ ] **Step 3: Commit generated code**

```bash
git add internal/db/
git commit -m "sqlc: regenerate from squashed schema"
```

---

### Task 26: Verify go build after schema rebuild

- [ ] **Step 1: Build**

```bash
go build ./... 2>&1 | tee /tmp/build-phase3.log | head -80
```

Many sites will fail with `InitialMinutes` / `RemainingMinutes` → `InitialAmount` / `RemainingAmount`. That's expected; fixed in Phase 4.

- [ ] **Step 2: Record failures for Phase 4**

```bash
grep -E 'Minutes|minutes' /tmp/build-phase3.log | sort -u > /tmp/minutes-sites.txt
head /tmp/minutes-sites.txt
```

Phase 4 will systematically fix every site.

---

## Phase 4: Billing genericization (protos + Go + Stripe)

### Task 27: Rename proto fields

**Files:**
- Modify: `pb/drill/v1/billing.proto`, `pb/drill/v1/user.proto`

- [ ] **Step 1: Edit billing.proto**

Rename `int32 minutes = N` → `int32 amount = N` wherever it appears in request/response messages. Tag numbers unchanged.

Example diff:

```proto
message GetBalanceResponse {
-  int32 minutes = 1;
+  int32 amount = 1;
}

message Pack {
   string tier = 1;
-  int32 minutes = 2;
+  int32 amount = 2;
   string price_id = 3;
}
```

- [ ] **Step 2: Edit user.proto**

Rename `initial_minutes` → `initial_amount`, `remaining_minutes` → `remaining_amount` in `Grant` message (or wherever they appear on the user-facing Grant representation).

- [ ] **Step 3: Commit**

```bash
git add pb/drill/v1/
git commit -m "proto: rename minutes→amount across billing and user"
```

---

### Task 28: Regenerate Go and TS from protos

- [ ] **Step 1: Run buf generate**

```bash
buf generate
```

- [ ] **Step 2: Commit regenerated code**

```bash
git add internal/pb/ web/src/pb/
git commit -m "pb: regenerate after billing rename"
```

---

### Task 29: Update Go consumers for billing rename

**Files:**
- Modify: `internal/billing/plans.go`, `entitlement.go`, `internal/backend/billing.go`, `internal/rpc/billing/server.go`, `internal/rpc/user/server.go`, `cmd/drillctl/main.go`, any `*_test.go` that references minutes.

- [ ] **Step 1: Rename Go identifiers**

In each file, rename function params `minutes int` → `amount int`, struct fields `Minutes` → `Amount`, log keys `"minutes"` → `"amount"`, `InitialMinutes` → `InitialAmount`, `RemainingMinutes` → `RemainingAmount`, `FreeTrialMinutes` → `FreeTrialAmount`, `PriceIDForPack(minutes int)` → `PriceIDForPack(tier string)`, `PackMinutes` → removed (replaced by tier-keyed lookup).

- [ ] **Step 2: Rewrite `PriceIDForPack`**

```go
// billing/plans.go
func (p *Plans) PriceIDForPack(tier string) (string, bool) {
    id, ok := p.cfg.PackPriceIDs[tier]
    return id, ok
}
```

- [ ] **Step 3: In `internal/backend/billing.go::HandleStripeWebhook`**

Read `metadata["pack_amount"]` (an integer string) and `metadata["pack_tier"]` (tier label for audit logs). Insert the purchase grant via `RecordPurchaseGrant` with `amount = parsed`.

- [ ] **Step 4: In `internal/rpc/billing/server.go::CreateCheckoutSession`**

Accept `tier string` in the proto request (update proto if needed; a minimal `CreateCheckoutSessionRequest { string tier = 1; int32 amount = 2; }` is fine). Set Stripe metadata `pack_tier=tier`, `pack_amount=strconv.Itoa(int(amount))`.

- [ ] **Step 5: Update `cmd/drillctl/main.go::grantCmd`**

Rename CLI arg. Help text: `"amount is your app-defined billing unit; see internal/billing/plans.go"`.

```go
func grantCmd() *cli.Command {
    return &cli.Command{
        Name:      "grant",
        Usage:     "Create an admin grant",
        ArgsUsage: "<amount>",
        Description: "amount is your app-defined billing unit; see internal/billing/plans.go",
        Action: func(ctx context.Context, cmd *cli.Command) error {
            amountStr := cmd.Args().First()
            // parse and call EnsureFreeGrant or a new AdminGrant method
            ...
        },
    }
}
```

- [ ] **Step 6: Run build**

```bash
go build ./...
```

Expected: pass.

- [ ] **Step 7: Run go tests (many still broken; record what's left)**

```bash
go test ./... 2>&1 | tee /tmp/test-phase4.log | tail -40
```

Fix compile errors; defer semantic test failures to Phase 9.

- [ ] **Step 8: Commit**

```bash
git add -A
git commit -m "billing: rename minutes→amount across Go call sites and drillctl CLI"
```

---

### Task 30: Update web frontend for billing rename

**Files:**
- Modify: `web/src/pages/settings.tsx` (billing panel), `web/src/pages/landing/credits.tsx`, any other component using `.minutes`

- [ ] **Step 1: Grep and fix**

```bash
cd web
grep -rn 'minutes\|Minutes' src/pages/ src/components/ | grep -v node_modules
```

Replace TS field accesses `.minutes` → `.amount`, labels `"minutes"` → `"credits"` (or whatever the downstream app calls them — neutral default `"credits"` or `"units"` works).

- [ ] **Step 2: Run typecheck**

```bash
npx tsc -b
```

Expected: pass.

- [ ] **Step 3: Commit**

```bash
cd ..
git add web/
git commit -m "web: rename minutes→amount in settings billing panel and landing credits"
```

---

### Task 31: Update cmd/stripescenario for billing rename

**Files:**
- Modify: `cmd/stripescenario/packbuy.go`, `runner.go`, any file using `--minutes`

- [ ] **Step 1: Rename CLI flag `--minutes` → `--amount`**

- [ ] **Step 2: Update Stripe metadata keys to `pack_amount` + `pack_tier`**

- [ ] **Step 3: Build and commit**

```bash
go build ./cmd/stripescenario/...
git add cmd/stripescenario/
git commit -m "stripescenario: rename --minutes flag and metadata keys"
```

---

## Phase 5: Rename drill → app / drilotel → apptel / module path

### Task 32: Rename internal/drilotel → internal/apptel

**Files:**
- Rename: `internal/drilotel/` → `internal/apptel/`
- Modify: all files in the new package; `AppName` const; imports across the repo

- [ ] **Step 1: Rename dir**

```bash
git mv internal/drilotel internal/apptel
```

- [ ] **Step 2: Update package declaration in every file inside**

```bash
cd internal/apptel
sed -i '' 's/^package drilotel$/package apptel/' *.go
cd ../..
```

- [ ] **Step 3: Update `AppName` const**

Find the line `const AppName = "drill"` and change to `const AppName = "app"`.

```bash
grep -rn 'AppName\s*=\s*"drill"' internal/apptel/
# edit file to change value
```

- [ ] **Step 4: Update every import site across the repo**

```bash
git grep -l 'github.com/btc/drill/internal/drilotel' | xargs sed -i '' 's|github.com/btc/drill/internal/drilotel|github.com/btc/drill/internal/apptel|g'
git grep -l 'drilotel\.' | xargs sed -i '' 's|drilotel\.|apptel.|g'
```

- [ ] **Step 5: Verify build**

```bash
go build ./...
```

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "rename: internal/drilotel → internal/apptel; AppName 'drill' → 'app'"
```

---

### Task 33: Rename cmd/drill → cmd/app, cmd/drillctl → cmd/appctl

**Files:**
- Rename: `cmd/drill/` → `cmd/app/`, `cmd/drillctl/` → `cmd/appctl/`

- [ ] **Step 1: Rename**

```bash
git mv cmd/drill cmd/app
git mv cmd/drillctl cmd/appctl
```

- [ ] **Step 2: Update binary-name literals inside**

```bash
grep -rn '"drill"\|"drillctl"\|Drill administration' cmd/
```

In `cmd/appctl/main.go`:

```go
// before
cmd := &cli.Command{
    Name:  "drillctl",
    Usage: "Drill administration CLI",
    ...
}
// after
cmd := &cli.Command{
    Name:  "appctl",
    Usage: "app administration CLI",
    ...
}
```

Any `slog.Info` banner in `cmd/app/main.go` referencing "drill" → "app".

- [ ] **Step 3: Verify build**

```bash
go build ./cmd/...
```

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "rename: cmd/drill→cmd/app, cmd/drillctl→cmd/appctl"
```

---

### Task 34: Rename Go module path

**Files:**
- Modify: `go.mod`, every `import "github.com/btc/drill/..."`

- [ ] **Step 1: Update go.mod**

```bash
sed -i '' 's|^module github.com/btc/drill$|module github.com/btc/saas-starter|' go.mod
```

- [ ] **Step 2: Rewrite all imports**

```bash
git grep -l '"github.com/btc/drill/' | xargs sed -i '' 's|"github.com/btc/drill/|"github.com/btc/saas-starter/|g'
```

- [ ] **Step 3: go mod tidy**

```bash
go mod tidy
```

- [ ] **Step 4: Verify build**

```bash
go build ./...
```

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum
git add -A
git commit -m "rename: go module github.com/btc/drill → github.com/btc/saas-starter"
```

---

### Task 35: Rename proto package path pb/drill/v1 → pb/app/v1

**Files:**
- Rename: `pb/drill/v1/` → `pb/app/v1/`, `internal/pb/drill/` → `internal/pb/app/`, `web/src/pb/drill` (if present as a subdir)
- Modify: every `.proto` file's `package` and `go_package` options, `buf.gen.yaml`, every Go import referencing `internal/pb/drill/v1`, every TS import referencing `web/src/pb/drill`

- [ ] **Step 1: Rename dirs**

```bash
git mv pb/drill pb/app
git mv internal/pb/drill internal/pb/app
# web/src/pb is flat — confirm with ls
ls web/src/pb/
```

- [ ] **Step 2: Edit each proto file**

In every `pb/app/v1/*.proto`:

```proto
// before
package drill.v1;
option go_package = "github.com/btc/drill/internal/pb/drill/v1;drillv1";
// after
package app.v1;
option go_package = "github.com/btc/saas-starter/internal/pb/app/v1;appv1";
```

```bash
cd pb/app/v1
sed -i '' 's|^package drill\.v1;|package app.v1;|' *.proto
sed -i '' 's|github.com/btc/drill/internal/pb/drill/v1;drillv1|github.com/btc/saas-starter/internal/pb/app/v1;appv1|' *.proto
cd ../../..
```

- [ ] **Step 3: Update `buf.gen.yaml` `go_package_prefix`**

```yaml
# before
- name: go
  opt:
    - paths=source_relative
    - module=github.com/btc/drill
# after
- name: go
  opt:
    - paths=source_relative
    - module=github.com/btc/saas-starter
```

- [ ] **Step 4: Regenerate**

```bash
rm -rf internal/pb/app/* web/src/pb/*
buf generate
```

- [ ] **Step 5: Update every Go import**

```bash
git grep -l 'internal/pb/drill/v1\|drillv1' | xargs sed -i '' 's|internal/pb/drill/v1|internal/pb/app/v1|g; s|drillv1\.|appv1.|g; s|drillv1connect|appv1connect|g'
```

- [ ] **Step 6: Update TS imports**

Grep for `"@/pb/drill/` or similar; replace with `"@/pb/app/`. Update `vite.config.ts` alias if needed.

```bash
cd web
git grep -l '@/pb/drill\|drill/v1\|drillv1' src/ | xargs sed -i '' 's|@/pb/drill|@/pb/app|g; s|drill/v1|app/v1|g; s|drillv1|appv1|g' 2>/dev/null || true
cd ..
```

- [ ] **Step 7: Verify**

```bash
go build ./...
cd web && npx tsc -b && cd ..
```

- [ ] **Step 8: Commit**

```bash
git add -A
git commit -m "rename: proto package drill.v1 → app.v1; internal/pb/drill → internal/pb/app; regenerate"
```

---

### Task 36: Session cookie name generalization

**Files:**
- Modify: `internal/auth/user_context.go`, `cmd/app/main.go`

- [ ] **Step 1: In user_context.go: convert const to var**

```go
// before
const SessionCookieName = "drill_session"
// after
var SessionCookieName = "app_session"

func SetSessionCookieName(name string) {
    if name != "" {
        SessionCookieName = name
    }
}
```

- [ ] **Step 2: In cmd/app/main.go: call `auth.SetSessionCookieName(cfg.Auth.CookieName)` after `config.Load`**

```go
cfg, err := config.Load()
if err != nil { ... }
auth.SetSessionCookieName(cfg.Auth.CookieName)
```

- [ ] **Step 3: Verify build + existing tests still pass**

```bash
go build ./...
go test ./internal/auth/... -race -count=1
```

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "auth: session cookie name is now runtime-configurable (Auth.CookieName)"
```

---

### Task 37: Rename remaining 'drill' and 'sabermatic' strings in code

**Files:**
- Various

- [ ] **Step 1: Grep for leftover references**

```bash
git grep -l 'drill\|sabermatic\|Drill\|Sabermatic' -- ':!*.md' ':!go.sum'
```

- [ ] **Step 2: For each file, replace contextually**

Common sites:
- `cmd/app/main.go` startup logs
- `internal/config/config.go` any residual env var names (e.g., `DRILL_*`; rename to `APP_*`)
- Any test fixtures with drill names

```bash
git grep -l 'drill\|Drill' -- '*.go' | xargs sed -i '' 's/\bdrill\b/app/g; s/\bDrill\b/App/g'
```

Audit the resulting diff carefully — global sed can over-replace. Review before committing.

- [ ] **Step 3: Review diff**

```bash
git diff | head -200
```

Revert any unintended replacements (e.g., in vendor-style strings or URLs).

- [ ] **Step 4: Build + test**

```bash
go build ./...
go test ./... -race -count=1
```

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "rename: remaining drill/sabermatic literals in Go code"
```

---

## Phase 6: Frontend landing rewrite + home + router

### Task 38: Rewrite landing page

**Files:**
- Modify: `web/src/pages/landing/index.tsx`, `hero.tsx`, `cta-repeat.tsx`, `credits.tsx`

- [ ] **Step 1: Rewrite `hero.tsx`**

A neutral SaaS hero. Uses existing shadcn `Button`, `Card`.

```tsx
import { Button } from "@/components/ui/button";
import { Link } from "react-router-dom";

export function Hero() {
    return (
        <section className="py-20 px-6 max-w-6xl mx-auto text-center">
            <h1 className="text-5xl md:text-6xl font-bold tracking-tight">
                Build your SaaS faster.
            </h1>
            <p className="mt-6 text-xl text-muted-foreground max-w-2xl mx-auto">
                Auth, billing, tracing, metrics, Stripe, and infra — ready to ship.
                Focus on what makes your app unique.
            </p>
            <div className="mt-10 flex justify-center gap-4">
                <Link to="/signup">
                    <Button size="lg">Get started</Button>
                </Link>
                <Link to="/login">
                    <Button size="lg" variant="outline">Log in</Button>
                </Link>
            </div>
        </section>
    );
}
```

- [ ] **Step 2: Rewrite `credits.tsx` as generic Pricing**

```tsx
import { Card } from "@/components/ui/card";

export function Pricing() {
    return (
        <section className="py-20 px-6 max-w-6xl mx-auto">
            <h2 className="text-3xl font-bold text-center mb-12">Pricing</h2>
            <div className="grid md:grid-cols-3 gap-6">
                <Card className="p-6">
                    <h3 className="text-xl font-semibold">Starter</h3>
                    <p className="text-3xl font-bold mt-2">Free</p>
                    <p className="text-muted-foreground mt-4">Enough to try.</p>
                </Card>
                <Card className="p-6 border-primary">
                    <h3 className="text-xl font-semibold">Pro</h3>
                    <p className="text-3xl font-bold mt-2">$X/mo</p>
                    <p className="text-muted-foreground mt-4">For teams.</p>
                </Card>
                <Card className="p-6">
                    <h3 className="text-xl font-semibold">Enterprise</h3>
                    <p className="text-3xl font-bold mt-2">Contact</p>
                    <p className="text-muted-foreground mt-4">At scale.</p>
                </Card>
            </div>
        </section>
    );
}
```

- [ ] **Step 3: Rewrite `cta-repeat.tsx`**

```tsx
import { Button } from "@/components/ui/button";
import { Link } from "react-router-dom";

export function CTARepeat() {
    return (
        <section className="py-20 px-6 text-center">
            <h2 className="text-3xl font-bold">Ready to start?</h2>
            <Link to="/signup" className="inline-block mt-6">
                <Button size="lg">Sign up — it's free</Button>
            </Link>
        </section>
    );
}
```

- [ ] **Step 4: Rewrite `index.tsx` to compose only the retained sections**

```tsx
import { Hero } from "./hero";
import { Pricing } from "./credits"; // renamed component, same file
import { CTARepeat } from "./cta-repeat";

export function Landing() {
    return (
        <main>
            <Hero />
            <Pricing />
            <CTARepeat />
        </main>
    );
}
```

- [ ] **Step 5: Rename file `credits.tsx` → `pricing.tsx` for clarity**

```bash
git mv web/src/pages/landing/credits.tsx web/src/pages/landing/pricing.tsx
# update import in index.tsx to match
```

- [ ] **Step 6: Typecheck**

```bash
cd web && npx tsc -b && cd ..
```

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "web/landing: rewrite as generic SaaS landing (hero + pricing + CTA)"
```

---

### Task 39: Rewrite home.tsx

**Files:**
- Modify: `web/src/pages/home.tsx`

- [ ] **Step 1: Replace content**

```tsx
import { useAuth } from "@/hooks/use-auth";
import { Link } from "react-router-dom";
import { Button } from "@/components/ui/button";

export function Home() {
    const { user } = useAuth();
    return (
        <main className="py-12 px-6 max-w-4xl mx-auto">
            <h1 className="text-3xl font-bold">Welcome, {user?.displayName || user?.email}</h1>
            <p className="mt-4 text-muted-foreground">
                Your dashboard goes here.
            </p>
            <div className="mt-8 flex gap-4">
                <Link to="/settings"><Button variant="outline">Settings</Button></Link>
                <Link to="/settings/billing"><Button variant="outline">Billing</Button></Link>
            </div>
        </main>
    );
}
```

- [ ] **Step 2: Typecheck and commit**

```bash
cd web && npx tsc -b && cd ..
git add web/src/pages/home.tsx
git commit -m "web/home: empty dashboard placeholder"
```

---

### Task 40: Rewrite app.tsx routes

**Files:**
- Modify: `web/src/app.tsx`

- [ ] **Step 1: Replace routes and imports**

```tsx
import { Route, Routes } from "react-router-dom";
import { Login } from "./pages/auth/login";
import { Signup } from "./pages/auth/signup";
import { ForgotPassword } from "./pages/auth/forgot-password";
import { ResetPassword } from "./pages/auth/reset-password";
import { VerifyEmail } from "./pages/auth/verify-email";
import { Landing } from "./pages/landing";
import { PublicHeader } from "./components/public-header";
import { Home } from "./pages/home";
import { Settings } from "./pages/settings";
import { NotFound } from "./pages/not-found";
import { AppLayout } from "./layouts/app-layout";

export default function App() {
    return (
        <Routes>
            <Route path="/login" element={<Login />} />
            <Route path="/signup" element={<Signup />} />
            <Route path="/forgot-password" element={<ForgotPassword />} />
            <Route path="/reset-password" element={<ResetPassword />} />
            <Route path="/verify-email" element={<VerifyEmail />} />
            <Route path="/about" element={<><PublicHeader /><Landing /></>} />
            <Route path="/" element={<><PublicHeader /><Landing /></>} />

            <Route element={<AppLayout />}>
                <Route path="/home" element={<Home />} />
                <Route path="/settings" element={<Settings />} />
                <Route path="/settings/billing" element={<Settings />} />
            </Route>

            <Route path="*" element={<NotFound />} />
        </Routes>
    );
}
```

Adjust imports if drill's folder/file shapes differ (e.g., default vs named exports).

- [ ] **Step 2: Delete `ConditionalHome`, `ImmersiveLayout` if they existed and are no longer used**

- [ ] **Step 3: Typecheck**

```bash
cd web && npx tsc -b && cd ..
```

- [ ] **Step 4: Commit**

```bash
git add web/src/app.tsx
git commit -m "web/app.tsx: strip session/interview/history routes; restrict AppLayout to home + settings"
```

---

### Task 41: Trim settings.tsx

**Files:**
- Modify: `web/src/pages/settings.tsx`

- [ ] **Step 1: Read the file and delete any drill-specific sections**

Typical drill-specific content: interview preferences panel, voice settings, session history summary. Delete these JSX blocks and their imports.

- [ ] **Step 2: Keep: Account (display name, email, verification status, logout), Billing (balance, pack purchase)**

- [ ] **Step 3: Typecheck**

```bash
cd web && npx tsc -b && cd ..
```

- [ ] **Step 4: Commit**

```bash
git add web/src/pages/settings.tsx
git commit -m "web/settings: trim drill-specific sections; keep account + billing"
```

---

### Task 42: Parameterize frontend telemetry service.name

**Files:**
- Modify: `web/src/telemetry/provider.ts`, `web/vite.config.ts`

- [ ] **Step 1: Add Vite define in vite.config.ts**

```ts
// vite.config.ts
export default defineConfig({
    // ...
    define: {
        __APP_NAME__: JSON.stringify(process.env.VITE_APP_NAME || "app"),
    },
});
```

- [ ] **Step 2: Add TS ambient declaration for __APP_NAME__**

In `web/src/vite-env.d.ts`:

```ts
declare const __APP_NAME__: string;
```

- [ ] **Step 3: Update provider.ts**

```ts
// before
"service.name": "drill-web",
// after
"service.name": `${__APP_NAME__}-web`,
```

- [ ] **Step 4: Typecheck + build**

```bash
cd web && npx tsc -b && npm run build && cd ..
```

- [ ] **Step 5: Commit**

```bash
git add web/
git commit -m "web/telemetry: parameterize service.name via __APP_NAME__ define"
```

---

### Task 43: Replace web/public branding assets

**Files:**
- Create: `web/public/favicon.svg` (neutral placeholder)
- Create: `web/public/og-default.png` (neutral 1200x630)
- Delete: drill-specific OG and favicon variants

- [ ] **Step 1: Write a minimal placeholder `favicon.svg`**

```xml
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32">
  <rect width="32" height="32" rx="6" fill="#0f172a"/>
  <text x="16" y="22" text-anchor="middle" fill="#fff" font-family="monospace" font-size="18" font-weight="bold">A</text>
</svg>
```

- [ ] **Step 2: Replace `og-landing.*`, `og-sample.*` with a single `og-default.png` (or SVG that renders to 1200x630)**

```bash
rm -f web/public/og-landing.* web/public/og-sample.*
# og-default.png can start as a simple text-on-gradient image; implementer can replace with a branded asset post-rename
```

Write a placeholder SVG if no PNG is available:

```xml
<!-- web/public/og-default.svg -->
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1200 630" width="1200" height="630">
  <rect width="1200" height="630" fill="#0f172a"/>
  <text x="600" y="330" text-anchor="middle" fill="#fff" font-family="sans-serif" font-size="72" font-weight="bold">App</text>
</svg>
```

- [ ] **Step 3: Update SPA handler and index.html references to point at the new filenames**

Grep `web/index.html` for `og-landing`, `favicon`; update paths.

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "web/public: replace drill branding assets with neutral placeholders"
```

---

## Phase 7: TF / bootstrap / Make / CI parameterization

### Task 44: Parameterize Terraform

**Files:**
- Modify: `terraform/variables.tf`, `terraform/main.tf`, `cloud_run.tf`, `cloud_sql.tf`, `gcs.tf`, `iam.tf`, `secrets.tf`, `artifact_registry.tf`, `outputs.tf`
- Create: `terraform/terraform.tfvars.example`

- [ ] **Step 1: Add variables**

In `terraform/variables.tf` add (or replace existing):

```hcl
variable "project_id" {
    description = "GCP project ID"
    type        = string
}

variable "region" {
    description = "GCP region"
    type        = string
    default     = "us-central1"
}

variable "service_name" {
    description = "App/service name; used for Cloud Run service, SQL DB, SA, AR repo"
    type        = string
    default     = "app"
}

variable "domain" {
    description = "Primary user-facing domain (e.g., example.com)"
    type        = string
    default     = "example.com"
}

variable "mailgun_subdomain" {
    description = "Mailgun sending subdomain (e.g., mg.example.com)"
    type        = string
    default     = "mg.example.com"
}
```

- [ ] **Step 2: Rewrite resource identifiers**

Across the `.tf` files, rename `sabermatic_app` → `app`, `cloud_sql.sabermatic` → `cloud_sql.main`, `artifact_registry.sabermatic` → `artifact_registry.app`, `google_cloud_run_v2_service.sabermatic` → `google_cloud_run_v2_service.app`, etc.

- [ ] **Step 3: Replace string literals with var references**

In `cloud_run.tf`:

```hcl
# before
name = "sabermatic"
env { name = "BASE_URL" value = "https://sabermatic.dev" }
env { name = "FROM_ADDRESS" value = "noreply@sabermatic.dev" }
# after
name = var.service_name
env { name = "BASE_URL" value = "https://${var.domain}" }
env { name = "FROM_ADDRESS" value = "noreply@${var.domain}" }
env { name = "PRODUCT_NAME" value = var.service_name }
```

`cloud_sql.tf`:

```hcl
resource "google_sql_database" "main" {
    name     = var.service_name
    instance = google_sql_database_instance.main.name
}

resource "google_sql_user" "main" {
    name     = var.service_name
    instance = google_sql_database_instance.main.name
    password = random_password.db_password.result
}
```

`iam.tf`:

```hcl
resource "google_service_account" "app" {
    account_id = "${var.service_name}-app"
    # ...
}
```

`secrets.tf`:

```hcl
secret_data = "postgres://${var.service_name}:${random_password.db_password.result}@/${var.service_name}?host=/cloudsql/${google_sql_database_instance.main.connection_name}"
```

`artifact_registry.tf`:

```hcl
resource "google_artifact_registry_repository" "app" {
    repository_id = var.service_name
    # ...
}
```

- [ ] **Step 4: Update outputs.tf**

```hcl
output "cloud_run_url" {
    value = google_cloud_run_v2_service.app.uri
}

output "cloud_sql_instance" {
    value = google_sql_database_instance.main.connection_name
}

output "artifact_registry_url" {
    value = "${var.region}-docker.pkg.dev/${var.project_id}/${google_artifact_registry_repository.app.repository_id}"
}
```

- [ ] **Step 5: Delete existing `terraform.tfvars.example` if drill-era, write new**

```hcl
# terraform.tfvars.example
# Copy to terraform.tfvars and fill in.
project_id = "your-gcp-project-id"
region     = "us-central1"
service_name = "app"
domain     = "example.com"
mailgun_subdomain = "mg.example.com"
```

- [ ] **Step 6: Run `terraform plan` against a stub project**

```bash
cd terraform
cp terraform.tfvars.example terraform.tfvars
# edit terraform.tfvars: set project_id to an actual GCP project you can run plan against, or skip this step if no test project
terraform init -backend=false
terraform plan -var-file=terraform.tfvars 2>&1 | head -40
cd ..
```

If no test project is available, skip the plan run; `terraform validate` is enough:

```bash
cd terraform && terraform init -backend=false && terraform validate && cd ..
```

- [ ] **Step 7: Commit**

```bash
git add terraform/
git commit -m "terraform: parameterize service_name, domain, mailgun_subdomain; rename resource identifiers"
```

---

### Task 45: Parameterize cloud_bootstrap.py

**Files:**
- Modify: `scripts/cloud_bootstrap.py`

- [ ] **Step 1: Read the file; locate every hardcoded `sabermatic` / `drill` reference**

```bash
grep -n 'sabermatic\|drill' scripts/cloud_bootstrap.py
```

- [ ] **Step 2: Add prompts for service_name and domain**

```python
service_name = prompt_value("Service name (used for Cloud Run, SQL DB, AR repo)", state.get("service_name", "app"))
domain       = prompt_value("Primary domain", state.get("domain", "example.com"))
```

- [ ] **Step 3: Replace every hardcoded literal with the variables**

Search-and-replace `"sabermatic"` → `service_name`, `"sabermatic-production"` → `project_id` where semantically correct, `"sabermatic.dev"` → `domain`, `"mg.sabermatic.dev"` → `f"mg.{domain}"` (or ask in prompt), `"btc/drill"` → prompt for github_repo as before.

- [ ] **Step 4: Test dry-run**

```bash
python3 scripts/cloud_bootstrap.py --dry-run < /dev/null 2>&1 | head -20
```

- [ ] **Step 5: Commit**

```bash
git add scripts/cloud_bootstrap.py
git commit -m "cloud_bootstrap.py: parameterize service_name and domain prompts"
```

---

### Task 46: Update Makefile

**Files:**
- Modify: `Makefile`

- [ ] **Step 1: Replace string literals**

Sites to edit:

```makefile
# before
CLOUDSQL_INSTANCE := sabermatic-production:us-central1:sabermatic-production
# after
# Set via env or terraform.tfvars; no default.
CLOUDSQL_INSTANCE ?= $(error set CLOUDSQL_INSTANCE, e.g. my-project:us-central1:my-instance)
```

```makefile
# before
db-password:
	...grep '://sabermatic:'...
# after
db-password:
	...grep '://app:'...
```

```makefile
# before
grant:
	go run ./cmd/drillctl grant $(MINUTES) --email $(EMAIL)
# after
grant:
	go run ./cmd/appctl grant $(AMOUNT) --email $(EMAIL)
```

```makefile
# before
dev:
	@echo "Starting Sabermatic → http://localhost:8080"
	...tmp/drill...
# after
dev:
	@echo "Starting app → http://localhost:8080"
	...tmp/app...
```

```makefile
# before
stripe-pack-buy:
	go run ./cmd/stripescenario packbuy --minutes $(MINUTES)
# after
stripe-pack-buy:
	go run ./cmd/stripescenario packbuy --amount $(AMOUNT)
```

- [ ] **Step 2: Verify make targets still run for the happy paths**

```bash
make help 2>&1 | head
```

- [ ] **Step 3: Commit**

```bash
git add Makefile
git commit -m "Makefile: rename drill→app; MINUTES→AMOUNT; parameterize CLOUDSQL_INSTANCE"
```

---

### Task 47: Update Procfile.dev, .air.toml, scripts/deploy.sh, Dockerfile

**Files:**
- Modify: `Procfile.dev`, `.air.toml`, `scripts/deploy.sh`, `Dockerfile`

- [ ] **Step 1: Procfile.dev**

```bash
grep -n 'drill\|sabermatic' Procfile.dev
```

Replace `tmp/drill` binary path → `tmp/app`, process names if any.

Mailhog smoke-test support: add a line if not present:

```
mailhog: mailhog
```

This requires `mailhog` installed locally. Document in README.

- [ ] **Step 2: .air.toml**

```bash
grep -n 'drill\|sabermatic' .air.toml
```

Replace binary path, log path.

- [ ] **Step 3: scripts/deploy.sh**

Convert hardcoded variables to env-driven:

```bash
#!/usr/bin/env bash
set -euo pipefail

: "${GCP_PROJECT:?GCP_PROJECT must be set (e.g., my-gcp-project)}"
: "${SERVICE:=app}"
: "${REGION:=us-central1}"
: "${DOMAIN:=example.com}"

# existing logic with PROJECT=sabermatic-production → $GCP_PROJECT
# SERVICE=sabermatic → $SERVICE
...
```

- [ ] **Step 4: Dockerfile**

```dockerfile
# before
RUN go build -o sabermatic ./cmd/drill
...
ENTRYPOINT ["/sabermatic"]
# after
RUN go build -o app ./cmd/app
...
ENTRYPOINT ["/app"]
```

- [ ] **Step 5: Build the container locally to verify**

```bash
docker build -t saas-starter-test .
```

- [ ] **Step 6: Commit**

```bash
git add Procfile.dev .air.toml scripts/deploy.sh Dockerfile
git commit -m "dev infra: rename drill→app across Procfile.dev, .air.toml, deploy.sh, Dockerfile"
```

---

### Task 48: Update GitHub Actions

**Files:**
- Modify: `.github/workflows/deploy.yml`, `.github/workflows/ci.yml`

- [ ] **Step 1: deploy.yml**

Replace hardcoded service names with repo variables:

```yaml
env:
  SERVICE_NAME: ${{ vars.SERVICE_NAME || 'app' }}
  GCP_PROJECT: ${{ vars.GCP_PROJECT }}
  REGION: ${{ vars.GCP_REGION || 'us-central1' }}
```

Add a guard step at the top of the deploy job:

```yaml
- name: Verify deploy variables
  run: |
    test -n "$GCP_PROJECT" || (echo "GCP_PROJECT repo var not set; refusing to deploy" && exit 1)
```

Replace `sabermatic/sabermatic` AR paths with `${SERVICE_NAME}/${SERVICE_NAME}`.

- [ ] **Step 2: ci.yml**

Audit for `drill` module paths. Since the module is renamed to `github.com/btc/saas-starter`, Go actions that reference the module should work as-is (they use `./...`). Adjust any path filters on `paths-ignore:` or matrix inputs.

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/
git commit -m "gh actions: parameterize deploy.yml via repo vars; refuse deploy without GCP_PROJECT"
```

---

### Task 49: Update email template test assertions

**Files:**
- Modify: `internal/email/template_test.go`

- [ ] **Step 1: Change expected HTML**

Every assertion that checks for `"Sabermatic"` or `"Drill"` → `"TestApp"` (the test uses `NewRenderer("TestApp")`).

- [ ] **Step 2: Run tests**

```bash
go test ./internal/email/... -race -count=1
```

- [ ] **Step 3: Commit**

```bash
git add internal/email/template_test.go
git commit -m "email/template_test: assert TestApp product name in rendered HTML"
```

---

## Phase 8: Rename script + README

### Task 50: Write scripts/rename_project.sh

**Files:**
- Create: `scripts/rename_project.sh`

- [ ] **Step 1: Write the script**

```bash
#!/usr/bin/env bash
# rename_project.sh: rename the starter's default neutral identifiers to your own.
# Run ONCE after forking; commit the result.
set -euo pipefail

usage() {
    cat <<EOF
Usage: $0 --module github.com/<owner>/<name> --name <svcname> --domain <domain>

Example:
    $0 --module github.com/acme/widget --name widget --domain widget.dev

Renames:
    github.com/btc/saas-starter → <module>
    app / appctl / apptel       → <svcname> / <svcname>ctl / <svcname>tel
    pb/app/v1                    → pb/<svcname>/v1
    app.v1 (proto package)       → <svcname>.v1
    example.com                  → <domain>
    noreply@example.com          → noreply@<domain>
    mg.example.com               → mg.<domain>
    AppName = "app"              → AppName = "<svcname>"
    CookieName default "app_..."  → "<svcname>_..."
EOF
    exit 1
}

MODULE="" NAME="" DOMAIN=""
while [[ $# -gt 0 ]]; do
    case "$1" in
        --module) MODULE="$2"; shift 2;;
        --name) NAME="$2"; shift 2;;
        --domain) DOMAIN="$2"; shift 2;;
        -h|--help) usage;;
        *) echo "unknown arg: $1"; usage;;
    esac
done

[[ -n "$MODULE" && -n "$NAME" && -n "$DOMAIN" ]] || usage

# macOS/BSD sed in-place vs GNU sed
SED_I=(sed -i '')
if sed --version >/dev/null 2>&1; then
    SED_I=(sed -i)
fi

echo "==> Module: github.com/btc/saas-starter → $MODULE"
git grep -l 'github.com/btc/saas-starter' | xargs "${SED_I[@]}" "s|github.com/btc/saas-starter|$MODULE|g" || true

echo "==> Identifier: apptel → ${NAME}tel"
git mv internal/apptel internal/${NAME}tel || true
git grep -l 'apptel' | xargs "${SED_I[@]}" "s|apptel|${NAME}tel|g" || true

echo "==> Binary: cmd/app → cmd/$NAME; cmd/appctl → cmd/${NAME}ctl"
git mv cmd/app cmd/$NAME || true
git mv cmd/appctl cmd/${NAME}ctl || true

echo "==> Proto package: app.v1 → $NAME.v1; pb/app/v1 → pb/$NAME/v1"
git mv pb/app pb/$NAME || true
git mv internal/pb/app internal/pb/$NAME || true
find pb/$NAME -name '*.proto' -exec "${SED_I[@]}" "s|package app\\.v1;|package $NAME.v1;|g" {} \;
find pb/$NAME -name '*.proto' -exec "${SED_I[@]}" "s|internal/pb/app/v1;appv1|internal/pb/$NAME/v1;${NAME}v1|g" {} \;
git grep -l 'appv1\.' | xargs "${SED_I[@]}" "s|appv1\\.|${NAME}v1.|g" || true
git grep -l 'appv1connect' | xargs "${SED_I[@]}" "s|appv1connect|${NAME}v1connect|g" || true
git grep -l 'internal/pb/app/v1' | xargs "${SED_I[@]}" "s|internal/pb/app/v1|internal/pb/$NAME/v1|g" || true

echo "==> buf.gen.yaml"
"${SED_I[@]}" "s|module=github.com/btc/saas-starter|module=$MODULE|" buf.gen.yaml

echo "==> Binary references: 'app' / 'appctl' literal (selective replace)"
git grep -l 'cmd/app\b' | xargs "${SED_I[@]}" "s|cmd/app\\b|cmd/$NAME|g" || true
git grep -l 'cmd/appctl\b' | xargs "${SED_I[@]}" "s|cmd/appctl\\b|cmd/${NAME}ctl|g" || true

echo "==> AppName const"
git grep -l 'AppName\\s*=\\s*"app"' | xargs "${SED_I[@]}" "s|AppName = \"app\"|AppName = \"$NAME\"|g" || true

echo "==> Vite define VITE_APP_NAME default"
"${SED_I[@]}" "s|VITE_APP_NAME || \"app\"|VITE_APP_NAME || \"$NAME\"|" web/vite.config.ts

echo "==> Cookie name default"
git grep -l '\"app_session\"' | xargs "${SED_I[@]}" "s|\"app_session\"|\"${NAME}_session\"|g" || true

echo "==> Domain strings: example.com → $DOMAIN"
git grep -l 'example\\.com' | xargs "${SED_I[@]}" "s|example\\.com|$DOMAIN|g" || true

echo "==> FromAddress, mg.<domain>, log path"
git grep -l 'noreply@example' | xargs "${SED_I[@]}" "s|noreply@example|noreply@|g" || true  # not used after example.com replace; idempotent
git grep -l 'data/logs/app\\.log' | xargs "${SED_I[@]}" "s|data/logs/app\\.log|data/logs/$NAME.log|g" || true

echo "==> Docker binary"
"${SED_I[@]}" "s|go build -o app ./cmd/app|go build -o $NAME ./cmd/$NAME|" Dockerfile
"${SED_I[@]}" "s|ENTRYPOINT \\[\"/app\"\\]|ENTRYPOINT [\"/$NAME\"]|" Dockerfile

echo "==> Regenerate"
buf generate
sqlc generate
go mod tidy

echo "==> Done. Review: git status; run make test."
```

- [ ] **Step 2: chmod +x**

```bash
chmod +x scripts/rename_project.sh
```

- [ ] **Step 3: Commit**

```bash
git add scripts/rename_project.sh
git commit -m "scripts: add rename_project.sh for downstream forks"
```

---

### Task 51: Write README.md

**Files:**
- Create: `README.md`

- [ ] **Step 1: Write a focused starter README**

```markdown
# SaaS Starter

A fork-and-ship SaaS starter with auth, billing, tracing, metrics, Stripe, ConnectRPC, email, jobs, object storage, LLM/image AI integrations, GCP infrastructure, and Terraform — all wired end-to-end. Rename to your own identifiers and deploy.

## What's inside

- **Backend:** Go + ConnectRPC on `cmd/app`; admin CLI on `cmd/appctl`; background jobs via River; telemetry via OpenTelemetry (`internal/apptel`).
- **Auth:** password + Google/GitHub OAuth via Goth; cookie-based sessions.
- **Billing:** generic amount-based grants + ledger; Stripe checkout, webhook, customer portal.
- **Email:** Mailgun, with configurable product name.
- **Storage:** GCS + local fallback + in-memory mock for tests.
- **Data:** Postgres via sqlc, migrations via `internal/migrate`.
- **AI:** LLM client (`internal/ai/client.go`) and Gemini image gen (`internal/ai/gemini.go`). Drop these if you don't need AI.
- **Frontend:** React + Vite + shadcn/ui; auth pages, empty dashboard, settings, billing, landing.
- **Infra:** Terraform for Cloud Run + Cloud SQL + GCS + Secret Manager + Artifact Registry + IAM; `scripts/cloud_bootstrap.py` sets up a fresh GCP project.
- **CI/CD:** GitHub Actions in `.github/workflows/`.

## Getting started

### 1. Fork

```bash
git clone https://github.com/btc/saas-starter my-app && cd my-app
rm -rf .git && git init -b main && git add -A && git commit -m "initial"
```

### 2. Rename

```bash
./scripts/rename_project.sh \
    --module github.com/me/my-app \
    --name myapp \
    --domain my-app.dev
git add -A && git commit -m "rename project"
```

### 3. Bootstrap GCP (optional, for deployment)

```bash
python3 scripts/cloud_bootstrap.py
```

Follow the prompts to create a GCP project, enable APIs, provision Cloud SQL, etc. Writes `terraform/terraform.tfvars`.

### 4. Run locally

```bash
make dev
```

Starts the Go server on `:8080`, Vite dev server, and Mailhog for local email at `:8025`. Visit `http://localhost:8080`.

### 5. Deploy

```bash
cd terraform && terraform init && terraform apply
make deploy
```

## Environment variables

See `.env.example` for the full list. The required ones:

- `ANTHROPIC_API_KEY` — LLM calls
- `DATABASE_URL` — Postgres connection
- `STRIPE_SECRET_KEY`, `STRIPE_WEBHOOK_SECRET` — billing
- `MAILGUN_API_KEY` — outbound email (use Mailhog in dev)
- `GOOGLE_OAUTH_CLIENT_ID`, `GOOGLE_OAUTH_CLIENT_SECRET` — Google login
- `GITHUB_OAUTH_CLIENT_ID`, `GITHUB_OAUTH_CLIENT_SECRET` — GitHub login
- `SESSION_KEY` — cookie signing secret (generate with `openssl rand -base64 32`)

Optional:
- `PRODUCT_NAME` — display name (default `App`)
- `LLM_MODEL` — (default `claude-sonnet-4-5`)
- `STRIPE_PACK_PRICE_IDS` — `small=price_xxx,med=price_yyy` for billing tiers

## What to strip

Not an AI app? Remove `internal/ai/` and its `Gemini` config. Not paid? Remove `internal/billing/` and associated protos/handlers/SQL tables.

## Known follow-ups

- OAuth flow in `internal/auth/oauth.go` is functional but has accumulated complexity; consider refactoring before major changes.
- `example.com` email defaults require Mailhog locally; mailgun will refuse to relay.

## Attribution

Extracted from an internal SaaS product.
```

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs: starter README"
```

---

### Task 52: Write .env.example

**Files:**
- Create: `.env.example`

- [ ] **Step 1: Write**

```bash
# .env.example — copy to .env and fill in.
PRODUCT_NAME=App
LLM_MODEL=claude-sonnet-4-5
ANTHROPIC_API_KEY=sk-ant-...

DATABASE_URL=postgres://app:password@localhost:5432/app?sslmode=disable
LOG_FILE=data/logs/app.log

# Stripe (test mode keys)
STRIPE_SECRET_KEY=sk_test_...
STRIPE_WEBHOOK_SECRET=whsec_...
STRIPE_PACK_PRICE_IDS=small=price_xxx,med=price_yyy

# Email — use Mailhog locally
MAILGUN_API_KEY=dev
MAILGUN_DOMAIN=mg.example.com
FROM_ADDRESS=noreply@example.com
EMAIL_PROVIDER=mailhog  # or "mailgun" for production

# OAuth
GOOGLE_OAUTH_CLIENT_ID=
GOOGLE_OAUTH_CLIENT_SECRET=
GITHUB_OAUTH_CLIENT_ID=
GITHUB_OAUTH_CLIENT_SECRET=

# Session
SESSION_KEY=change-me-generate-with-openssl-rand-base64-32
AUTH_COOKIE_NAME=app_session

# Storage
STORAGE_BACKEND=local
STORAGE_LOCAL_DIR=data/storage
GCS_BUCKET=
GCS_PUBLIC_BUCKET=

# Telemetry (optional)
OTEL_EXPORTER_OTLP_ENDPOINT=
```

- [ ] **Step 2: Commit**

```bash
git add .env.example
git commit -m "docs: .env.example with all required + optional vars"
```

---

## Phase 9: Validation

### Task 53: Full lint + codegen + test sweep

- [ ] **Step 1: buf lint**

```bash
buf lint
```

Expected: no output (pass).

- [ ] **Step 2: buf generate — no diff**

```bash
buf generate
git diff --exit-code internal/pb/ web/src/pb/
```

Expected: exit 0 (no diff).

- [ ] **Step 3: sqlc generate — no diff**

```bash
sqlc generate
git diff --exit-code internal/db/
```

Expected: exit 0.

- [ ] **Step 4: go vet, go build**

```bash
go vet ./...
go build ./...
```

- [ ] **Step 5: go mod tidy — no diff**

```bash
go mod tidy
git diff --exit-code go.mod go.sum
```

- [ ] **Step 6: Run full test suite**

```bash
go test ./... -race -count=1 -timeout=300s
```

Expected: all tests pass. If failures remain, fix in place (this is the final fixup pass).

- [ ] **Step 7: Frontend checks**

```bash
cd web
npx tsc -b
npm run lint
npm run test -- --run
npm run build
cd ..
```

- [ ] **Step 8: Commit any fixup**

```bash
git add -A
git commit -m "chore: Phase 9 validation fixups"
```

---

### Task 54: make test end-to-end

- [ ] **Step 1: Run make test**

```bash
make test
```

Expected: green. If drill's Makefile target definition diverges, inspect and fix.

- [ ] **Step 2: If failures, triage**

Common sources: residual drill strings in test fixtures, email template assertions, stale generated code not committed.

- [ ] **Step 3: Commit**

```bash
git add -A && git commit -m "chore: make test passes" || echo "no-op"
```

---

### Task 55: make dev smoke test

- [ ] **Step 1: Ensure local Postgres and Mailhog are running**

```bash
# Postgres
psql -U postgres -c "CREATE USER app WITH PASSWORD 'password' SUPERUSER;" || true
psql -U postgres -c "CREATE DATABASE app OWNER app;" || true

# Mailhog (install: brew install mailhog)
# Procfile.dev should include it; otherwise start in another terminal:
mailhog &
```

- [ ] **Step 2: Run migrations**

```bash
go run ./cmd/appctl migrate up
```

- [ ] **Step 3: Start make dev**

```bash
make dev &
MAKE_DEV_PID=$!
sleep 5
```

- [ ] **Step 4: Hit the app**

```bash
curl -s http://localhost:8080/ | head -20
curl -s http://localhost:8080/api/health
```

Expected: landing HTML on `/`; `{"status":"ok"}` on `/api/health`.

- [ ] **Step 5: Sign up a user (manual browser or scripted)**

Browser: visit `http://localhost:8080/signup`, fill form, submit. Verify email arrives in Mailhog at `:8025`. Click the link. Land on `/home`.

Scripted alternative:

```bash
curl -s -X POST http://localhost:8080/api/auth/signup -H 'Content-Type: application/json' -d '{"email":"test@example.com","password":"testpass123","displayName":"Test"}' | head
```

Check Mailhog at `http://localhost:8025`.

- [ ] **Step 6: Stop make dev**

```bash
kill $MAKE_DEV_PID || true
```

- [ ] **Step 7: If any bugs found, fix + commit**

- [ ] **Step 8: Commit**

```bash
git add -A && git commit -m "chore: make dev smoke test fixups" || echo "no-op"
```

---

### Task 56: terraform validate + plan (if GCP project available)

- [ ] **Step 1: terraform validate**

```bash
cd terraform
terraform init -backend=false
terraform validate
cd ..
```

Expected: `Success! The configuration is valid.`

- [ ] **Step 2: Optional — terraform plan**

If a real GCP project is available:

```bash
cd terraform
cp terraform.tfvars.example terraform.tfvars
# edit terraform.tfvars with real project_id
terraform plan -var-file=terraform.tfvars 2>&1 | tail -20
cd ..
```

Expected: plan completes without error. Actions shown should be coherent (create ~15-20 resources).

- [ ] **Step 3: Clean up**

```bash
rm -f terraform/terraform.tfvars  # not committed
```

---

### Task 57: Rename script smoke test

- [ ] **Step 1: Copy current saas-starter to a scratch dir**

```bash
rsync -a --exclude '.git' /Users/btc/Projects/src/saas-starter/ /tmp/rename-smoke/
cd /tmp/rename-smoke
git init -b main
git add -A
git commit -m "baseline"
```

- [ ] **Step 2: Run the rename script**

```bash
./scripts/rename_project.sh --module github.com/btc/testfoo --name testfoo --domain testfoo.dev
```

- [ ] **Step 3: Verify rename covered everything**

```bash
git grep -E 'saas-starter|\\bapp\\b|apptel|appctl' -- ':!*.md' ':!go.sum' ':!scripts/rename_project.sh' | head
```

Expected: no matches (aside from README, which may describe the starter's former state as historical).

- [ ] **Step 4: make test on the renamed copy**

```bash
go mod tidy
make test
```

Expected: green.

- [ ] **Step 5: Clean up**

```bash
cd -
rm -rf /tmp/rename-smoke
```

- [ ] **Step 6: If anything failed, return to Task 50 and fix the rename script; re-smoke.**

---

### Task 58: Final commit and audit

- [ ] **Step 1: Final leftover-string check**

```bash
cd /Users/btc/Projects/src/saas-starter
git grep -E 'drill|sabermatic|drilotel' -- ':!docs/*'
```

Expected: no matches (README may mention "extracted from an internal SaaS product" but not "drill" by name).

If matches appear, inspect and fix.

- [ ] **Step 2: Review commits**

```bash
git log --oneline | head -40
```

Should read as a clear phase-by-phase burndown.

- [ ] **Step 3: No-op final commit if everything clean**

```bash
git log -1 --stat
```

- [ ] **Step 4: Done.**

---

## Post-plan notes

- **Stripe test-mode end-to-end** is not in the smoke test scripts since it requires real (test-mode) Stripe keys. README lists this as an optional deeper validation.
- **OAuth flows** can only be fully exercised with real Google/GitHub credentials; not in the smoke test.
- **Deployment to GCP** requires an actual project; smoke test stops at `terraform validate`.
- **Open decisions** from the spec:
  - Image gen kept in starter per Open Decision #1 default.
  - Reserve/refund stripped per Open Decision #2 default.
  - Stripe packs use tier-keyed env var map per Open Decision #3 default.
  - Cookie name parameterized per Modify — auth; no longer an open item.
- **Boy-scout follow-up:** OAuth refactor (`internal/auth/oauth.go`) is called out in README as a known area for future work, not fixed in this extraction.
