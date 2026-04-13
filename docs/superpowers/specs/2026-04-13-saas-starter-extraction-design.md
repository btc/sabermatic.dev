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
- Generalize billing from minute-denominated grants to unit-agnostic grants.
- Genericize email templates (strip `internal/branding` coupling).
- Rewrite landing page as generic SaaS landing; replace dashboard with empty placeholder.
- Parameterize `cloud_bootstrap.py` for service name and domain.
- Add `scripts/rename_project.sh` for downstream users to rename the starter when stamping a new project from it.

**Out of scope:**
- OAuth refactor (deferred per user memory).
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
| Telemetry package: `apptel` | `footel` |
| Product name (in email copy, landing): `App` (via `cfg.ProductName`) | `Foo` |
| TF default domain: `example.com` | `foo.dev` |

## Strip manifest

### Delete (top-level junk)
- `v0/` (Python prototype)
- `drill`, `drillctl`, `stripescenario` (stray compiled binaries at repo root; add to `.gitignore`)
- `terraform/2`, `terraform/3`, `terraform/foo`, `terraform/th`, `terraform/terraform.tfvars`, `terraform/tfplan`
- `cloud_bootstrap.log`, `coverage-cross.out`, `data/`, `tmp/`
- `sabermatic-production:us-central1:sabermatic-prod` file
- Stray drill/sabermatic favicons, mark images, OG images in `web/public/`

### Delete (backend packages)
- `internal/coach/`, `internal/educator/`, `internal/evaluation/`, `internal/imagegen/`, `internal/interview/`, `internal/sample/`, `internal/branding/`
- `internal/ai/stt.go`, `internal/ai/tts.go` (+ their tests)
- `internal/rpc/coach/`, `educator/`, `evaluation/`, `interview/`, `question/`, `sample/`

### Delete (backend façade — `internal/backend/`)
- `coach.go`, `educator.go`, `evaluation.go`, `question.go`, `session.go` (interview sessions), `turn.go`, `turn_sink.go`
- Trim `backend.go`: remove `stt`, `tts`, `SampleService` fields; remove their init; remove `Gemini` init if nothing left consumes it (keep if image gen is registered elsewhere).

### Delete (jobs)
- `coach.go`, `educator.go`, `evaluate.go`, `generate_image.go`, `sweep_images.go`
- `cleanup.go` and `cleanup_generating.go` (both drill-specific — operate on `interview_sessions`, call `FullRefundSessionMinutes`, enqueue `EvaluateSession`)
- Surgically edit `workers.go::RegisterWorkers` to drop all references to the deleted workers.

### Delete (protos)
- `pb/drill/v1/coach.proto`, `educator.proto`, `evaluation.proto`, `interview.proto`, `question.proto`, `sample.proto`
- Corresponding generated Go (`internal/pb/drill/v1/`) and TS (`web/src/pb/`)

### Delete (SQL)
- Migration `001_initial.up.sql` tables: `questions`, `interview_sessions`, `messages`, `evaluations`, `annotations`, `educator_analyses`, `coach_analyses` (keep `users`, `oauth_accounts`, `auth_sessions`, `llm_calls`, `llm_call_content`, `user_events`)
- `sql/queries/` files: `questions.sql`, `messages.sql`, `sessions.sql` (interview_sessions), `coach_analyses.sql`, `educator_analyses.sql`, `evaluations.sql`, `interview.sql`

### Delete (frontend)
- Pages: `interview.tsx`, `sample.tsx`, `session-config.tsx`, `session/`, `history.tsx`
- Components: `recording-input.tsx`, `replay/`, `shader-orb.tsx`, `shader-orb-impl.tsx`, `waveform.tsx`
- Generated TS pb for deleted services

## Keep (with targeted modifications)

### Keep as-is
- `internal/auth/` (Goth OAuth + Google + GitHub + passwords + sessions + tokens + keys + user_context)
- `internal/storage/` (GCS + local + mock)
- `internal/drilotel/` (rename package to `apptel`)
- `internal/handler/` (health, middleware, oauth, server, spa, storage, stripe — all platform)
- `internal/testutil/`, `internal/backendtest/`, `internal/migrate/`, `internal/config/`
- `internal/rpc/auth/`, `billing/`, `user/`, `session/` (platform services; `session` here is the auth session, not interview session — verify during implementation)
- `internal/rpc/interceptor.go`, `register.go`
- `cmd/drill/` → rename `cmd/app/`; main.go verified generic.
- `cmd/drillctl/` → rename `cmd/appctl/`; keep `seed` command; generalize `grant <minutes>` → `grant <amount>`.
- `cmd/stripescenario/` — generic Stripe webhook scenario tool.
- `scripts/deploy.sh`, `Procfile.dev`, `web.go`
- `web/src/components/ui/` (shadcn primitives), `auth-layout`, auth pages, `settings.tsx` (trim drill copy), `brand-name.tsx`, `error-fallback.tsx`, `oauth-icons.tsx`, `public-header.tsx`, `theme-switch.tsx`
- `internal/ai/client.go` (LLM chat), `gemini.go` (image gen)
- `internal/ai/` tests for kept files

### Modify — billing generalization (grants/ledger from minutes to units)
Schema + code rename:
- `grants.initial_minutes` → `initial_amount`
- `grants.remaining_minutes` → `remaining_amount`
- All Go function params `minutes int` → `amount int` in `internal/billing/` and `internal/backend/billing.go`
- Update `internal/billing/plans.go` and `entitlement.go` terminology to unit-agnostic (`amount`, `balance`)
- Update tests and SQL queries in `sql/queries/grants.sql`, `ledger_entries.sql`

New apps interpret "1 unit" freely (seats, API calls, generations, tokens, minutes). Stripe webhook, checkout, portal, grant lifecycle, ledger deduction, entitlement middleware all stay.

### Modify — config (`internal/config/`)
- Collapse 5 role-specific LLM env vars (`INTERVIEWER_MODEL`, `EVALUATOR_MODEL`, `COACH_MODEL`, `EDUCATOR_MODEL`, `IMAGE_PROMPT_MODEL`) to one: `LLM_MODEL`.
- Remove `Speech.*` entirely.
- Keep `Gemini.*` (image gen).
- Add `ProductName` field (default `"App"`) to replace `internal/branding` package for email copy.

### Modify — email
- Strip `internal/email/template.go` import of `internal/branding`; use `cfg.ProductName` instead.
- Rewrite `internal/email/templates/wrapper.html` as neutral shell.
- Generalize inline email copy (welcome, verify, reset) — no product-specific phrasing; `{{.ProductName}}` where needed.

### Modify — frontend
- Rewrite landing: hero + value prop + pricing + CTA. Use `public-header.tsx`, shadcn primitives. No drill sections (Annotations, DeepDivePreview, Coaching, VoicePipeline, SampleSessionLink, Scoring, StrengthsGaps).
- Replace `home.tsx` with empty dashboard: "Welcome, {user.displayName}" + link to settings/billing.
- Settings page: trim drill-specific copy; keep account/billing sections.
- Update routing in `app.tsx` to drop deleted routes.

### Modify — Terraform
- Rename TF resource identifiers: `sabermatic_app` → `app`, `cloud_sql.sabermatic` → `cloud_sql.main`, `artifact_registry.sabermatic` → `artifact_registry.app`, etc.
- Replace string literals with `var.service_name` / `var.domain`:
  - `cloud_run.tf`: service name, domain env vars, `FromAddress`, `ProductName`
  - `cloud_sql.tf`: DB name, user name
  - `iam.tf`: service account ID
  - `secrets.tf`: DB connection string
  - `artifact_registry.tf`: repo id
  - `outputs.tf`: resource references
- Add `variables.tf` entries for `service_name`, `domain` (already has `project_id`).
- Ship `terraform.tfvars.example` with clearly placeholder values.

### Modify — bootstrap (`scripts/cloud_bootstrap.py`)
- Add prompts/CLI flags for `service_name`, `domain` (defaults `app`, `example.com`).
- Replace ~12 hardcoded `sabermatic`/`drill` references with variables (see audit at lines 271, 279, 472, 478, 491, 496, 884, 907, 929, 937, 964, 980, 981).
- `--dry-run` exits cleanly.

### Modify — Dockerfile, Makefile
- `Dockerfile`: `go build -o sabermatic ./cmd/drill` → `go build -o app ./cmd/app`; `ENTRYPOINT ["/sabermatic"]` → `["/app"]`
- `Makefile`: `"Starting Sabermatic..."` → `"Starting app..."`

### Add — rename script (`scripts/rename_project.sh`)
One-shot sed pass accepting `--module`, `--name`, `--domain`. Updates Go source, TS source, TF, SQL, Dockerfile, Makefile, scripts. For downstream use — the starter itself is already renamed at commit time.

## Validation

Starter must be continuously verifiable. Before commit:
- `make test` passes (buf lint, codegen check, `tsc -b`, eslint, vitest, `go test -race ./...`)
- `make dev` brings up a working local app: land on marketing page → sign up → verify email (Mailhog or equivalent) → land on empty dashboard → open settings → open Stripe checkout in test mode.
- `terraform plan` runs clean against a fresh GCP project using placeholder `.tfvars`.
- `cloud_bootstrap.py --dry-run` exits cleanly with the new parameterized flags.

README documents: clone → rename → bootstrap → deploy.

## Risks and open items

- **`internal/rpc/session/` naming collision:** there's an auth-session RPC (platform) and an interview-session RPC (app). Verify during implementation that the `rpc/session` path is the auth one; strip or rename if it's the interview one.
- **`Gemini` client init in `backend.New`:** keep if image gen is genuinely reusable; strip along with its env vars if nothing in the starter enqueues image jobs. Decide during implementation.
- **Migration squash:** drill has 4+ migrations that reference now-deleted tables. Either (a) squash into a single `001_initial` migration reflecting the starter's final schema (cleaner), or (b) delete offending migrations and leave the rest (simpler, but leaves dead `004_billing.down.sql` referencing stripped tables). Recommend (a) during implementation.
- **Boy-scout cleanup queue:** OAuth refactor deferred. Flag any other drill tech debt surfaced during stripping; file separately rather than fold into this work.

## Open decisions / notes

- Domain for starter repo: `example.com`.
- All commits and PRs must appear authored by user alone. No Claude/AI attribution in commits or PR bodies.
