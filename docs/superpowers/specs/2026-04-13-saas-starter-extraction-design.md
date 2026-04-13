# SaaS Starter Extraction — Design

**Date:** 2026-04-13
**Source repo:** `github.com/btc/drill`
**Target repo:** `github.com/btc/saas-starter` (new)
**Strategy:** Fork-and-strip. One-time extraction. Starter evolves independently from drill.

## Goal

Produce a working SaaS starter repo — auth, billing, tracing, metrics, Stripe, ConnectRPC, GCP, Terraform, email, jobs, storage, and AI — with all drill-specific domain code removed. A fresh clone + rename + bootstrap should yield a deployable app with login, pay, and settings working end-to-end, zero domain filler.

## Scope and boundaries

**In scope:**
- Fork drill to a new repo; delete app-specific packages; rename all drill/sabermatic/drilotel identifiers to neutral defaults; verify `make test`, `make dev`, and `terraform plan` pass; commit.
- Generalize billing from minute-denominated grants to unit-agnostic grants; strip drill-specific reserve/refund-on-session mechanic.
- Genericize email templates; replace `internal/branding` with a `cfg.ProductName` field threaded to all consumers.
- Rewrite landing page as generic SaaS landing; replace dashboard with empty placeholder.
- Parameterize `cloud_bootstrap.py`, `.github/workflows/`, and TF for service name and domain.
- Add `scripts/rename_project.sh` for downstream users to rename the starter when stamping a new project from it.
- Squash migrations 001–012 into a single `001_initial` reflecting the starter's final schema.

**Out of scope:**
- OAuth refactor (deferred per user memory; flagged for follow-up in the starter).
- pgvector/embeddings/RAG (not in drill; net-new work).
- Publishing the starter or setting up a starter CI pipeline (just get it clean locally).
- Drill itself — this is a one-way extraction. No changes flow back into drill.

## Repo shape and naming

Starter is committed with neutral names already applied. It builds green, passes `make test`, and `terraform plan` runs cleanly. Downstream users run a rename script once after forking.

| Starter | Downstream example |
|---|---|
| Go module: `github.com/btc/saas-starter` | `github.com/btc/foo` |
| Service / binary / TF service_name / SQL DB / SQL user / SA / AR repo: `app` | `foo` |
| CLI binary: `appctl` | `fooctl` |
| Telemetry package: `apptel`; `apptel.AppName = "app"` const | `footel`; `AppName = "foo"` |
| Product name (email copy, landing, SPA meta titles) via `cfg.ProductName` default `"App"` | `"Foo"` |
| TF default domain: `example.com` | `foo.dev` |
| Default email FromAddress: `noreply@example.com` | `noreply@foo.dev` |
| Default log file: `data/logs/app.log` | `data/logs/foo.log` |

## Strip manifest

### Delete (top-level junk / build artifacts)
- `v0/` (Python prototype)
- Stray compiled binaries at repo root: `drill`, `drillctl`, `stripescenario` (86MB + 18MB + 19MB; distinct from the source at `cmd/stripescenario/` which is kept)
- `terraform/2`, `terraform/3`, `terraform/foo`, `terraform/th`, `terraform/terraform.tfvars`, `terraform/tfplan`
- `cloud_bootstrap.log`, `coverage-cross.out`, `data/`, `tmp/`
- `sabermatic-production:us-central1:sabermatic-prod` file
- Stray drill/sabermatic favicons, mark images, OG images in `web/public/`
- `docs/superpowers/specs/*`, `docs/superpowers/plans/*` — drill design history; starter gets clean `docs/superpowers/` (retain skill scaffolding only)
- Any `docs/*.pdf` references to drill content
- `.gitignore`: add all of the above (binaries, tfplan, tfvars, cloud_bootstrap.log, coverage-cross.out, data/, tmp/) so future drift stays out

### Delete (backend packages)
- `internal/coach/`, `internal/educator/`, `internal/evaluation/`, `internal/imagegen/`, `internal/interview/`, `internal/sample/`, `internal/branding/`
- `internal/ai/stt.go`, `internal/ai/tts.go` (+ their tests)
- `internal/rpc/coach/`, `educator/`, `evaluation/`, `interview/`, `question/`, `sample/`, **`session/`** (interview-session service; auth-session logic lives in `internal/auth/sessions.go`, not here — reviewer-resolved open item)

### Delete (backend façade — `internal/backend/`)
- `coach.go`, `educator.go`, `evaluation.go`, `question.go`, `session.go` (interview sessions), `turn.go`, `turn_sink.go`
- Trim `backend.go::Backend`: remove `stt`, `tts`, `SampleService`, `llm` (if only used by deleted code — verify), `store` (keep — generic).
- Trim `backend.go::New`:
  - Drop `SampleService` init
  - Drop `Gemini` init unless image gen is genuinely reused in starter (see "Open decisions")
  - Drop `jobs.RegisterWorkers` references to Coach/Educator/Evaluate/GenerateImage/SweepImages/Cleanup* workers
  - Drop `PeriodicJobs` slice entries for `CleanupAbandonedSessions`, `CleanupStaleGenerating`, `SweepMissingImages`
  - Drop `workerRefs.Evaluate/Cleanup/Coach/SweepImages.Jobs = riverClient` back-refs
  - Drop `QueueGemini`, `QueueAI` queue registrations unless kept workers still use them

### Delete (jobs)
- `coach.go`, `educator.go`, `evaluate.go`, `generate_image.go`, `sweep_images.go` (+ tests)
- `cleanup.go`, `cleanup_generating.go` (+ tests) — both interview-session-specific
- `integration_test.go` if it wires deleted workers; otherwise trim
- `internal/jobs/jobtest/` fixtures/helpers that only serve deleted workers — audit and keep only what `send_email_test.go` and `error_handler_test.go` need
- Surgically edit `workers.go::RegisterWorkers` signature + body to drop deleted-worker refs; keep `SendEmailWorker` + `error_handler`

### Delete (protos)
- `pb/drill/v1/coach.proto`, `educator.proto`, `evaluation.proto`, `interview.proto`, `question.proto`, `sample.proto`, **`session.proto`** (interview-session)
- Corresponding generated Go (`internal/pb/drill/v1/*.pb.go` + `drillv1connect/` connect stubs) and TS (`web/src/pb/`) regenerated fresh via `buf generate` after strip

### Delete (SQL — schema)
Migrations 002, 003, 005, 006, 008, 009, 010, 012 all reference stripped tables. Strategy: **squash migrations 001–012 into one `001_initial.up.sql`** reflecting the starter's final schema. Squashed schema MUST contain only:
- `users` (with `role` default changed: `'candidate'` → `'user'`, CHECK `IN ('candidate','admin')` → `IN ('user','admin')`)
- `oauth_accounts`
- `auth_sessions`
- `llm_calls` — **drop `session_id UUID REFERENCES interview_sessions(id)` column entirely** (or retype as nullable opaque UUID with no FK)
- `llm_call_content`
- `user_events` — **drop `session_id` FK column** (same treatment)
- `grants` (renamed columns, see Modify — billing)
- `ledger_entries` — **drop `session_id UUID REFERENCES interview_sessions(id)` column entirely**; if reserve/refund mechanic is kept in generalized form, reintroduce as nullable opaque `subject_id` (see Modify — billing)

Drop `usage_periods` — already superseded in drill migration 004. Drop `questions`, `interview_sessions`, `messages`, `evaluations`, `annotations`, `educator_analyses`, `coach_analyses` — never created in squashed `001`.

### Delete (SQL — queries)
- `sql/queries/`: delete `questions.sql`, `messages.sql`, `sessions.sql` (interview_sessions), `coach_analyses.sql`, `educator_analyses.sql`, `evaluations.sql`, `interview.sql`
- Audit `advisory_locks.sql` — if it keys on `interview_sessions.id`, either delete or generalize to opaque subject key
- Audit `grants.sql` + `ledger_entries.sql` for queries that reference `interview_sessions` (e.g., `ReserveMinutes`, `FullRefundSessionMinutes` — decide fate in Modify — billing)
- Run `sqlc generate` after query edits; starter must not commit orphan generated code

### Delete (frontend)
- Pages: `interview.tsx`, `sample.tsx`, `session-config.tsx`, `session/`, `history.tsx`
- Components: `recording-input.tsx`, `replay/`, `shader-orb.tsx`, `shader-orb-impl.tsx`, `waveform.tsx`
- Generated TS pb for deleted services (`web/src/pb/` regenerate clean)
- Landing page subcomponents (see Modify — frontend landing)
- Any imports in `web/src/app.tsx` and `main.tsx` referring to deleted pages — remove explicit route list (enumerated in Modify — frontend router)

## Keep (with targeted modifications)

### Keep as-is
- `internal/auth/` (Goth OAuth + Google + GitHub + passwords + sessions + tokens + keys + user_context) — the canonical auth-session store
- `internal/storage/` (GCS + local + mock)
- `internal/drilotel/` → rename to `internal/apptel/`; update const `AppName = "drill"` → `"app"`; audit ~15 call sites so metric/span names update consistently
- `internal/handler/` (health, middleware, oauth, server, spa, stripe — all platform) — note: `spa.go` + `spa_test.go` import `internal/branding`, must swap to `cfg.ProductName`
- `internal/testutil/`, `internal/backendtest/`, `internal/migrate/`, `internal/config/`
- `internal/rpc/auth/`, `billing/`, `user/` (platform services; **NOT** `session/` or `sample/`)
- `internal/rpc/interceptor.go`
- `internal/rpc/register.go` — surgically edit to drop deleted service registrations; must compile against the trimmed service list
- `cmd/drill/` → rename `cmd/app/`; update default log file path in `config.go`
- `cmd/drillctl/` → rename `cmd/appctl/`; keep `seed`; change `grant <minutes>` → `grant <amount>` with help text: "amount is your app-defined billing unit; see internal/billing/plans.go"
- `cmd/stripescenario/` — generic Stripe webhook scenario tool (source kept; repo-root binary deleted per strip list)
- `scripts/deploy.sh`, `Procfile.dev`, `.air.toml` (if present) — audit and rename any `drill`/`sabermatic` strings (binary name, log path, overmind process names)
- `web.go`, `web/src/components/ui/` (shadcn), `web/src/layouts/auth-layout`, auth pages under `web/src/pages/auth/`, `settings.tsx` (trim drill copy), `brand-name.tsx` (drive from `cfg.ProductName`), `error-fallback.tsx`, `oauth-icons.tsx`, `public-header.tsx`, `theme-switch.tsx`
- `internal/ai/client.go` (LLM chat), `gemini.go` (image gen) + tests for kept files — see "Open decisions" on whether image gen stays

### Modify — billing generalization (minutes → unit-agnostic amount)
Full list of surface to touch — Go + SQL + proto + generated + Stripe:

- **Schema:** `grants.initial_minutes` → `initial_amount`, `grants.remaining_minutes` → `remaining_amount`. Reissue as single squashed migration.
- **Go:** `internal/billing/plans.go`, `internal/billing/entitlement.go`, `internal/backend/billing.go` — rename `minutes int` → `amount int`, log fields `"minutes"` → `"amount"`. ~29 occurrences across billing package.
- **Stripe integration:** drop fixed 120/300/600 pack scheme. Config fields `STRIPE_PACK_120_PRICE_ID`, `STRIPE_PACK_300_PRICE_ID`, `STRIPE_PACK_600_PRICE_ID` → replaced by a single `STRIPE_PACK_PRICE_IDS` map env var (`"small=price_xxx,med=price_yyy"`) OR an enumerated `Pack` config slice. `PriceIDForPack(minutes int)` → `PriceIDForPack(tier string)`. Stripe checkout metadata key `pack_minutes` → `pack_amount`. Webhook handler in `internal/handler/stripe.go` updated to match.
- **Reserve/refund mechanic:** drill reserves minutes at session start and refunds on cleanup via `reserveMinutesTx`, `FullRefundSessionMinutes`, `ReserveMinutes`. Decision: **strip reserve/refund entirely**. Starter billing is simpler: grant → deduct on usage → check balance. Delete `ledger_entries.session_id` column and all reserve/refund Go+SQL. Downstream apps that need reserve/refund add it back as an opaque `subject_id` generalization. (If you prefer to keep a generalized reserve, flag now — defaults to strip.)
- **Protos:** `pb/drill/v1/billing.proto` and `user.proto` use `minutes` fields. Rename: `int32 minutes` → `int32 amount`; `initial_minutes`/`remaining_minutes` → `initial_amount`/`remaining_amount`. `buf generate`.
- **RPC servers:** `internal/rpc/billing/server.go` and `internal/rpc/user/server.go` — update field accesses to match renamed proto.
- **Plan fields:** `Plan.FreeEducatorLimit`, `Plan.ConcurrentSessions`, `Plan.MaxDurationMinutes`, `Plan.CanStartSession` — drill-specific feature fields. Strip them. Starter `Plan` exposes only `Name`, `FreeTrialAmount` (renamed from `FreeTrialMinutes`), and generic billing fields.
- **Frontend:** TS pb regen via `buf generate` picks up renames automatically; audit any hardcoded `"minutes"` strings in React components (settings billing panel, pricing page).

### Modify — config (`internal/config/config.go`)
- Collapse 5 role-specific LLM env vars (`INTERVIEWER_MODEL`, `EVALUATOR_MODEL`, `COACH_MODEL`, `EDUCATOR_MODEL`, `IMAGE_PROMPT_MODEL`) to one: `LLM_MODEL`.
- Remove `Speech.*` struct entirely (OPENAI_API_KEY, WHISPER_MODEL, TTS_MODEL).
- Relax `config.Load` validator: no longer require `OPENAI_API_KEY` (was required for STT/TTS). `ANTHROPIC_API_KEY` stays required (LLM client).
- Keep `Gemini.*` (image gen) iff image gen stays; otherwise remove (see Open decisions).
- Add `ProductName` field (default `"App"`).
- Rename default `Log.File` from `data/logs/drill.log` → `data/logs/app.log`.
- Rename default `Email.FromAddress` from `noreply@drill.dev` → `noreply@example.com`.
- River config: drop `NumGeminiWorkers`/`NumAIWorkers` fields if those queues are dropped; keep if retained.

### Modify — email
- `internal/email/template.go`: remove import of `internal/branding`; take `productName string` at construction or via config.
- `internal/email/sender.go`: no-op aside from identifier rename.
- `internal/email/templates/wrapper.html`: rewrite as neutral shell using `{{.ProductName}}` template var.
- Inline email copy (welcome, verify, reset): rewrite as generic; template var for product name.

### Modify — handler/spa coupling
- `internal/handler/spa.go`: remove `internal/branding` import; thread `cfg.ProductName` through OG title logic.
- `internal/handler/spa_test.go`: update to use injected product name.

### Modify — frontend landing (explicit)
`web/src/pages/landing/` currently contains: `annotations.tsx`, `coaching.tsx`, `credits.tsx`, `cta-repeat.tsx`, `deep-dive.tsx`, `hero.tsx`, `index.tsx`, `sample-session.tsx`, `scoring.tsx`, `strengths-gaps.tsx`, `voice-pipeline.tsx`. Decisions:

- **Keep + rewrite copy:** `hero.tsx`, `cta-repeat.tsx`, `index.tsx`
- **Keep as-is structurally (generic):** `credits.tsx` — if it's a pricing/credits panel; rewrite copy to refer to generic `amount` units
- **Delete:** `annotations.tsx`, `coaching.tsx`, `deep-dive.tsx`, `sample-session.tsx`, `scoring.tsx`, `strengths-gaps.tsx`, `voice-pipeline.tsx`
- Verify each decision during implementation; if `credits.tsx` or `cta-repeat.tsx` are drill-specific, delete and add placeholder components

### Modify — frontend router (`web/src/app.tsx`)
Remove route entries for deleted pages:
- `/interview`, `/interview/:id`, `/sample`, `/session-config`, `/session/:id`, `/history`

Keep:
- `/` (landing), `/login`, `/signup`, `/forgot-password`, `/reset-password`, `/verify-email`, `/settings`, `/home` (empty dashboard placeholder), `/404`

Audit `main.tsx` for drill-specific imports (document title, analytics keys, feature flags).

### Modify — home.tsx
Replace with empty dashboard: "Welcome, {user.displayName}" + link to settings/billing. No drill content (coach summary cards, question list, session history).

### Modify — settings.tsx
Trim drill-specific sections (interview preferences, voice settings). Keep account display name, email verification status, billing summary, logout.

### Modify — Terraform
- Rename TF resource identifiers: `sabermatic_app` → `app`, `cloud_sql.sabermatic` → `cloud_sql.main`, `artifact_registry.sabermatic` → `artifact_registry.app`, etc.
- Replace string literals with `var.service_name` / `var.domain` in: `cloud_run.tf` (service name, env vars for `BASE_URL`, `FROM_ADDRESS`, `PRODUCT_NAME`), `cloud_sql.tf` (DB name, user name), `iam.tf` (service account ID), `secrets.tf` (DB connection string), `artifact_registry.tf` (repo id), `outputs.tf`.
- Add `variables.tf` entries for `service_name`, `domain`; keep existing `project_id`, `region`, `mailgun_subdomain` (default `mg.example.com`).
- Ship `terraform.tfvars.example` with clearly placeholder values; `.gitignore` `terraform.tfvars`.

### Modify — bootstrap (`scripts/cloud_bootstrap.py`)
- Add prompts/CLI flags for `service_name` (default `app`), `domain` (default `example.com`).
- Replace every `sabermatic`/`drill` literal (grep for the two tokens in this file; ~12 sites) with the variables. Do not commit line numbers — semantic replacement only.
- `--dry-run` exits cleanly with the new flags.

### Modify — Dockerfile, Makefile, Procfile.dev
- `Dockerfile`: `go build -o sabermatic ./cmd/drill` → `go build -o app ./cmd/app`; `ENTRYPOINT ["/sabermatic"]` → `["/app"]`.
- `Makefile`: `"Starting Sabermatic..."` → `"Starting app..."`; update deploy target references.
- `Procfile.dev`, `.air.toml`: grep for `drill`/`sabermatic`; swap to `app`.

### Modify — GitHub Actions (`.github/workflows/`)
- `deploy.yml`: hardcoded `SERVICE_NAME: sabermatic` and AR path `sabermatic/sabermatic` → use repo variables or parameterize to `app`/`app`. Consider gating deploy on a repo variable so a fresh fork doesn't attempt to deploy to a non-existent GCP project.
- `ci.yml`: audit for `drill` module paths, test path filters; update references; ensure CI runs `make test` cleanly.
- Both: ensure no hardcoded GCP project IDs or secrets tied to drill's GitHub repo; secrets should remain repo-configured.

### Add — rename script (`scripts/rename_project.sh`)
One-shot sed pass accepting `--module`, `--name`, `--domain`. Updates Go source, TS source, TF, SQL, Dockerfile, Makefile, Procfile.dev, .air.toml, .github/workflows/, scripts/. Touches:
- `github.com/btc/saas-starter` → `--module`
- `app` (Cloud Run, TF resource names, binary, SQL DB, SA, AR repo) → `--name`
- `appctl` → `--name + ctl`
- `apptel` package imports → `<name>tel`
- `example.com`, `noreply@example.com`, `mg.example.com` → `--domain` variants
- `AppName` constant in telemetry package
- `cfg.ProductName` default

The starter itself is already renamed at commit time. This script is for downstream users.

### Add — starter README
Rewrite (not modify). Content: what the starter is, what it includes, how to fork, how to run the rename script, how to run cloud_bootstrap, how to deploy, what to strip further for a non-AI app, where drill's history lives (reference link only).

## Validation

Starter must be continuously verifiable. Before final commit:

- `buf lint` clean
- `buf generate` clean; no orphan generated code
- `sqlc generate` clean; no queries referencing stripped tables
- `go vet ./...` clean
- `go build ./...` succeeds
- `make test` passes (buf lint, codegen check, `tsc -b`, eslint, vitest, `go test -race ./...`)
- `make dev` brings up a working local app: land on marketing page → sign up → verify email (Mailhog or equivalent) → land on empty dashboard → open settings → open Stripe checkout in test mode.
- `terraform plan` runs clean against a fresh GCP project using placeholder `.tfvars`.
- `cloud_bootstrap.py --dry-run` exits cleanly with the new parameterized flags.
- `scripts/rename_project.sh --module github.com/btc/foo --name foo --domain foo.dev` on a copy produces a repo where `make test` still passes — smoke test the rename script.

## Open decisions

1. **Keep image gen (`internal/ai/gemini.go` + `Gemini` config + `QueueGemini`)?**
   - Yes: keep in starter. No workers use it by default; downstream apps enqueue as needed.
   - No: strip. Downstream apps add image gen back from drill.
   - **Default: keep.** Small surface; valuable for AI-enabled SaaS.

2. **Keep reserve/refund ledger mechanic?**
   - Strip (default): simpler `grant + deduct + check balance` model.
   - Keep generalized: introduce opaque `subject_id` on `ledger_entries`; apps supply their own subject.
   - **Default: strip.** Downstream apps needing reserve/refund add it back.

3. **Stripe pack pricing shape** — single tier, enumerated map, or config-driven slice of packs?
   - **Default: enumerated map** via `STRIPE_PACK_PRICE_IDS="small=price_xxx,med=price_yyy,large=price_zzz"` env var + matching tier labels in `Plan`.

4. **Session cookie name** — drill may hardcode cookie namespace with "drill" string. Audit during implementation; parameterize via `cfg.ProductName` or a dedicated `Auth.CookieName` field.

## Risks

- **Telemetry span/metric name drift:** renaming `AppName = "drill"` to `"app"` changes metric keys. Drill's dashboards/alerts will not apply to the starter, but that's fine (different project). Downstream users rerun rename script and their metrics start fresh.
- **SQL squash vs preserved history:** squashing migrations loses the drill-era migration sequence. Acceptable — the starter is a reset point. Drill's own migrations remain in drill's git history.
- **River job queue state in dev DBs:** existing dev DBs from drill will have River metadata pointing at deleted workers. Not a starter problem (fresh DB on fork), but call out in README.
- **sqlc regeneration race:** `sqlc generate` must run after SQL edits AND the generated `internal/db/` must be committed. Treat as a gate in CI.
- **Env-var contract:** dropping `OPENAI_API_KEY` requirement, renaming LLM_MODEL, adding `PRODUCT_NAME` — document the final env var list in README and `terraform.tfvars.example`.
- **OAuth refactor deferred:** flag as first boy-scout TODO in starter README. Any downstream fork inherits it.

## Attribution

All commits and PRs in the starter repo must appear authored by the user alone. No Claude/AI attribution in commit trailers or PR bodies.
