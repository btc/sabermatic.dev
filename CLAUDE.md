<!-- This file gives Claude Code context for working in this repo. See README.md for human onboarding. -->

# Code Organization

Docs: ./docs
Protos: ./pb
SQL: ./sql
Backend: ./internal
Frontend: ./web
Local deploys: ./scripts/deploy.sh
Bootstrap: ./scripts/cloud_bootstrap.py

# Code Conventions

REST list endpoints must never return nil slices — `json.Marshal(nil)` produces `null`, not `[]`. Coerce in the backend method. ConnectRPC endpoints do not need this coercion: both binary and JSON protobuf codecs treat nil and empty `repeated` fields identically, and protobuf-es on the client always decodes to `[]`.

Never panic at init time. Propagate errors explicitly up to main.

Test user creation goes through `backendtest.SeedUser` (handler tests) or `seedUser` (backend internal tests), both calling `b.Signup()`. Never create users via raw SQL in tests — test state must match production state. To test unusual states (expired grants, zero balance), start from real state and mutate.

Shared test helper packages follow the `httptest` convention: `internal/jobs/jobtest/`, `internal/backendtest/`. Don't duplicate helpers across packages.

# API & Proto

Proto is the API contract. After editing `.proto` files, run `buf generate` and commit generated code in `internal/pb/` and `web/src/pb/`. Never hand-edit generated files.

ConnectRPC services follow Google AIPs where practical. Standard methods use AIP naming, pagination (AIP-158), error codes (AIP-193), field behavior annotations (AIP-203). Custom methods use AIP-136. Skip resource names (AIP-122).

Update methods use field masks for partial updates (AIP-134).

ConnectRPC handlers live in `internal/rpc/{service}/`. REST handlers in `internal/handler/` are limited to health checks, OAuth flows, and third-party webhooks (e.g. Stripe, Tavus) — endpoints that are inherently HTTP-level (raw body required, status-code-as-protocol, signed by the provider). All resource RPCs use ConnectRPC.

Verify library API signatures against installed versions before writing plan code blocks.

# Build & Verify

Full CI: `make test` (runs buf lint, codegen check, frontend typecheck+lint+tests, backend tests).

Frontend typecheck: `cd web && npx tsc --noEmit -p tsconfig.app.json` or `cd web && npx tsc -b`. Do NOT use bare `npx tsc --noEmit` — the root tsconfig has `files: []` with only project references, so it checks nothing without `-b` or `-p`.

Frontend build: `cd web && npm run build` (runs `tsc -b && vite build`).

Backend build: `go build ./...`

Backend tests: `go test ./internal/... ./cmd/... -race -count=1 -timeout=300s`

Proto codegen: `buf generate` then `sqlc generate` if SQL changed.

Local dev: `make dev` (starts overmind with vite + air on :8080).

# Process

Practice red-green TDD when investigating bugs. Write a failing test first, then fix.

After writing a spec, plan, or implementation, run Opus subagent review — up to 3 rounds. Fix ALL findings (high, medium, low, nit). Escalate disagreements to user.

After every subagent task that modifies code, a separate review agent must read actual files and verify against spec. No exceptions.

For tightly-coupled files, agents run sequentially, with later agents receiving committed output of earlier ones.

After any UI-affecting commit, load the page in the browser and verify before moving on.

Boy-scout rule. When your awareness shines on a piece of tech debt. Address it. Either by pointing it out and leaving a comment. Or integrating the cleanup into your current work scope if it is relevant.
