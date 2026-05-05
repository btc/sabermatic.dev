# Open-Source Preparation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Take the private `btc/drill` repo and ship a public, source-available release of Sabermatic under FSL-1.1-Apache-2.0, with all attribution-trailers and tracking secrets stripped from history.

**Architecture:** Linear pipeline. Phase 1 makes file-level changes as ordinary commits. Phase 2 verifies. Phase 3 rewrites history once (combined trailer-strip + author-rewrite + secret-removal). Phase 4 publishes to a fresh public GitHub repo. The single-rewrite design ensures one rollback point and one final SHA set.

**Tech Stack:** Git, `git filter-repo` (already installed), `gitleaks` and `trufflehog` (Phase 3 will install via Homebrew), `gh` CLI for GitHub repo creation and configuration.

**Spec:** `docs/superpowers/specs/2026-05-05-open-source-prep-design.md` (commit `8f948da`).

**Reference for runtime versions:**
- Go: `1.25.x` (from `go.mod`, line 5; CI also pins `1.25.x`)
- Node: `22` (from `.github/workflows/ci.yml`)
- Postgres: `16` (from `terraform/cloud_sql.tf`)

---

## Phase 1: File-Level Cleanup

Each task in this phase is a standalone commit. Order matters only where noted; otherwise tasks are independent and can be reordered if convenient.

### Task 1: Add `LICENSE.md`

**Files:**
- Create: `LICENSE.md`

- [ ] **Step 1: Fetch the canonical FSL-1.1-Apache-2.0 template**

The canonical text lives at `https://fsl.software/FSL-1.1-Apache-2.0.template.md`. Fetch it (e.g., via WebFetch or `curl https://fsl.software/FSL-1.1-Apache-2.0.template.md`).

- [ ] **Step 2: Write the substituted file**

Save the fetched template to `LICENSE.md`. The template has one substitution point: the `Notice` block near the top.

After substitution, the `Notice` line must read exactly:

```
## Notice

Copyright 2026 Spanda, LLC
```

Do not edit any other section. The Permitted Purpose, Patents, Redistribution, Disclaimer, Trademarks, and Grant of Future License sections are fixed.

- [ ] **Step 3: Verify the file**

Run:
```bash
head -5 LICENSE.md
grep -c "Spanda, LLC" LICENSE.md
grep -c "Apache License, Version 2.0" LICENSE.md
```

Expected:
- First line: `# Functional Source License, Version 1.1, Apache 2.0 Future License`
- `Spanda, LLC` count: 1
- `Apache License, Version 2.0` count: ≥1

- [ ] **Step 4: Commit**

```bash
git add LICENSE.md
git commit -m "license: add FSL-1.1-Apache-2.0"
```

---

### Task 2: Add `SECURITY.md`

**Files:**
- Create: `SECURITY.md`

- [ ] **Step 1: Write the file**

Save to `SECURITY.md`:

```markdown
# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in Sabermatic, please report it privately:

- **Preferred:** Open a [private security advisory](https://github.com/Spanda-LLC/sabermatic/security/advisories/new) on GitHub.
- **Alternative:** Email `security@spanda.llc`.

Please do not open a public issue for security vulnerabilities.

We aim to acknowledge reports within 3 business days. Coordinated disclosure is appreciated.
```

If the new repo will live at a path other than `Spanda-LLC/sabermatic`, update the advisory URL accordingly. (Final repo URL is decided in Task 19; you can update this URL post-publish or set it now if known.)

- [ ] **Step 2: Commit**

```bash
git add SECURITY.md
git commit -m "security: add vulnerability disclosure policy"
```

---

### Task 3: Pin Node version with `.nvmrc`

**Files:**
- Create: `.nvmrc`
- Modify: `web/package.json` (add `engines` field)

- [ ] **Step 1: Create `.nvmrc`**

Save to `.nvmrc` (root of repo):

```
22
```

(Single line, no trailing content; `.nvmrc` is plain-text, one version per file.)

- [ ] **Step 2: Add `engines.node` to `web/package.json`**

Read `web/package.json`. Add a top-level `engines` field. Place it after `version`. The field should be:

```json
"engines": {
  "node": ">=22"
}
```

Do not change any other field. Preserve existing formatting.

- [ ] **Step 3: Verify**

```bash
cat .nvmrc
python3 -c "import json; d = json.load(open('web/package.json')); print(d['engines'])"
```

Expected:
- `.nvmrc` outputs `22`
- Python output: `{'node': '>=22'}`

- [ ] **Step 4: Commit**

```bash
git add .nvmrc web/package.json
git commit -m "chore: pin Node version to 22 (.nvmrc + engines)"
```

---

### Task 4: Write `README.md`

**Files:**
- Create: `README.md`

- [ ] **Step 1: Capture or identify a hero screenshot**

Either:
- Capture a screenshot of `https://sabermatic.dev` showing a representative session, save to `docs/assets/hero.png` (create the directory if needed), OR
- Reuse an existing image from `web/public/` if one is suitable.

Note the path you chose; it will be referenced in the README.

- [ ] **Step 2: Write the README**

Save to `README.md`:

````markdown
# Sabermatic

> System-design interview practice with an AI coach. Voice-driven, real-time, structured feedback.

[![CI](https://github.com/Spanda-LLC/sabermatic/actions/workflows/ci.yml/badge.svg)](https://github.com/Spanda-LLC/sabermatic/actions/workflows/ci.yml)
[![License: FSL-1.1-Apache-2.0](https://img.shields.io/badge/license-FSL--1.1--Apache--2.0-blue.svg)](LICENSE.md)
[![Go 1.25](https://img.shields.io/badge/go-1.25-00ADD8.svg)](go.mod)

![Sabermatic hero](docs/assets/hero.png)

**Try it:** [sabermatic.dev](https://sabermatic.dev)

> This repo's git slug is `drill` (project codename); the product is Sabermatic.

## License

Sabermatic is released under the [Functional Source License, Version 1.1, Apache 2.0 Future License](LICENSE.md). You may read, fork, run, modify, and learn from the code. You may not run it as a competing service. The license converts to Apache 2.0 on 2028-05-05.

## Why this exists

Sabermatic was built end-to-end with [Claude Code](https://docs.claude.com/claude-code) over ~6 weeks and ~1,500 commits. The `docs/superpowers/specs/` and `docs/superpowers/plans/` directories show every feature's spec → plan → implementation cycle in real use. `CLAUDE.md` documents the project's engineering conventions. Source-available because we want the workflow and the code to be readable, but not trivially clonable as a competing service.

## Architecture

```mermaid
flowchart LR
  Browser[Browser - React/Vite] -->|ConnectRPC| API[Go API - Cloud Run]
  Browser -.->|WebSocket audio| API
  API --> DB[(Postgres - Cloud SQL)]
  API --> SM[GCP Secret Manager]
  API --> LLM[Anthropic / OpenAI]
  API --> Stripe[Stripe Billing]
  API -->|stderr logs| BQ[(BigQuery analytics)]
```

- **Backend:** Go 1.25, ConnectRPC, sqlc, River jobs, Postgres 16, OpenTelemetry.
- **Frontend:** React 19, Vite, Tailwind, ConnectRPC client.
- **Infra:** GCP Cloud Run, Cloud SQL, Secret Manager, Artifact Registry, BigQuery. Terraform-managed.
- **Billing:** Stripe (subscriptions + minute packs).

## Repository layout

| Path | Purpose |
|---|---|
| `internal/` | Backend Go code (handlers, services, jobs, db, billing). |
| `cmd/` | Binaries (`drill`, `drillctl`, `stripescenario`). |
| `web/` | React frontend. |
| `pb/` | Protobuf source (`.proto`); generated code in `internal/pb/` and `web/src/pb/`. |
| `sql/` | Database migrations and sqlc queries. |
| `terraform/` | GCP infrastructure as code. |
| `docs/` | Engineering specs, plans, and reference docs. |
| `CLAUDE.md` | Conventions for AI-assisted development in this repo. |

## Running locally

### Prerequisites

- Go 1.25 (see `go.mod`)
- Node 22 (see `.nvmrc`)
- Postgres 16
- (Optional) Docker, for ancillary services

### Quickstart

```bash
git clone https://github.com/Spanda-LLC/sabermatic.git
cd sabermatic
cp .env.example .env
# Populate .env — see "Required keys" below
make dev
```

`make dev` starts the Vite dev server and the Go backend (with hot reload via `air`) on port `:8080`.

### Required keys

`.env.example` lists every key the backend reads. Minimum to start the backend without errors:

- `DATABASE_URL` — local Postgres connection string.
- `ANTHROPIC_API_KEY` — required for LLM features.
- `AUTH_TOKEN_SECRET` — any random ≥32-byte string.

Optional:
- `OPENAI_API_KEY` — only if you exercise OpenAI-backed paths.
- `STRIPE_*` — only for billing flows.
- `OAUTH_*` — only for OAuth sign-in.
- `GOOGLE_CLOUD_PROJECT`, etc. — only for GCP deployment paths.

### What won't work without external accounts

- **Stripe webhooks** require a Stripe test account and `make stripe-setup` to populate `STRIPE_WEBHOOK_SECRET`.
- **GCP Secret Manager fetches** are no-ops locally; the backend reads from `.env` instead.
- **BigQuery analytics** (event sink) requires a deployed environment.
- **OAuth (Google, GitHub)** requires registered client IDs/secrets.

## Testing

```bash
make test
```

Runs: `buf lint`, codegen check, `go test ./internal/... ./cmd/... -race -count=1`, `golangci-lint`, frontend `tsc -b`, ESLint, Vitest. CI runs the same target.

## Deployment

See `terraform/` for the GCP infrastructure that backs production. `scripts/deploy.sh` triggers a Cloud Run deploy.

## Contributing

Sabermatic is source-available, not open source. We are not currently accepting outside pull requests, but bug reports via Issues are welcome.
````

If the GitHub repo will not live at `Spanda-LLC/sabermatic`, update the badge URLs and clone URL accordingly.

- [ ] **Step 3: Verify**

```bash
test -f README.md
head -3 README.md
grep -c "FSL-1.1-Apache-2.0" README.md
grep -c "sabermatic.dev" README.md
```

Expected:
- File exists.
- First line: `# Sabermatic`
- Both grep counts ≥1.

- [ ] **Step 4: Commit**

```bash
git add README.md docs/assets/hero.png 2>/dev/null || git add README.md
git commit -m "docs: add public README"
```

---

### Task 5: Delete `v0/`

**Files:**
- Delete: `v0/` (recursive)

- [ ] **Step 1: Confirm contents before deleting**

```bash
ls v0/
```

Note the contents in your shell output for the commit message reference. (Expected: a Python prototype with its own `frontend/`, `pyproject.toml`, etc.)

- [ ] **Step 2: Delete the directory**

```bash
git rm -rf v0/
```

- [ ] **Step 3: Verify**

```bash
ls v0/ 2>&1
git status --short | grep "^D" | wc -l
```

Expected:
- `ls v0/`: error "No such file or directory"
- Status shows multiple deletions staged.

- [ ] **Step 4: Commit**

```bash
git commit -m "chore: remove v0 Python prototype (superseded by Go rewrite)"
```

---

### Task 6: Delete `docs/coverage-risk-report.md`

**Files:**
- Delete: `docs/coverage-risk-report.md`

- [ ] **Step 1: Delete the file**

```bash
git rm docs/coverage-risk-report.md
```

- [ ] **Step 2: Verify**

```bash
test ! -f docs/coverage-risk-report.md && echo "deleted"
```

Expected: `deleted`.

- [ ] **Step 3: Commit**

```bash
git commit -m "docs: remove stale coverage risk report (risks closed)"
```

---

### Task 7: Delete `terraform/showHN`

**Files:**
- Delete: `terraform/showHN` (untracked binary tfplan)
- Modify: `.gitignore`

- [ ] **Step 1: Delete the file**

```bash
rm terraform/showHN
```

(File is untracked, so plain `rm`, not `git rm`.)

- [ ] **Step 2: Tighten `terraform/.gitignore`**

Read `terraform/.gitignore`. It currently has:

```
.terraform/
*.tfstate
*.tfstate.backup
*.tfvars
!terraform.tfvars.example
.terraform.lock.hcl
```

Append these lines:

```
showHN
*.zip
*.bin
```

- [ ] **Step 3: Verify**

```bash
test ! -f terraform/showHN && echo "deleted"
git check-ignore -v terraform/showHN
```

Expected:
- `deleted` printed.
- `git check-ignore` confirms `terraform/.gitignore:N:showHN	terraform/showHN`

- [ ] **Step 4: Commit**

```bash
git add terraform/.gitignore
git commit -m "chore: ignore terraform binary plan artifacts"
```

---

### Task 8: Triage `docs/superpowers/plans/`

**Files:**
- Delete: `docs/superpowers/plans/2026-04-13-saas-starter-extraction-design.md` (definitely)
- Delete: additional files identified in eyeball pass

- [ ] **Step 1: Delete the saas-starter-extraction file**

```bash
git rm docs/superpowers/plans/2026-04-13-saas-starter-extraction-design.md
```

- [ ] **Step 2: Eyeball-pass remaining plans for business/strategy content**

Generate a list of remaining plans:

```bash
ls docs/superpowers/plans/ | sort
```

For each file matching these patterns, read the file and apply the criterion **"would I show this to a competitor?"**:
- Anything with `landing`, `marketing`, `growth`, `show-hn`, `pricing` in the filename.
- Anything that discusses go-to-market, competitive positioning, or revenue.

Keep the rest (system-design, ConnectRPC migration, conductor, auth refactor, jobs/River, billing internals architecture, schema design, etc.).

For each file you decide to drop, run:

```bash
git rm docs/superpowers/plans/<filename>
```

Track decisions in your commit message.

- [ ] **Step 3: Verify**

```bash
git status --short | grep "^D" | wc -l
ls docs/superpowers/plans/ | wc -l
```

Note the counts; they go into the commit message.

- [ ] **Step 4: Commit**

```bash
git commit -m "docs: triage internal plans — drop business-strategy specs

Removed: saas-starter-extraction-design + N other business/growth
plans. Kept architecture/refactor/system-design plans."
```

(Replace `N` with actual count.)

---

### Task 9: Reframe `docs/functional-requirements-2026-04-01.md`

**Files:**
- Modify: `docs/functional-requirements-2026-04-01.md`

- [ ] **Step 1: Edit line 5 — Purpose statement**

Replace this line:

```
**Purpose**: Input for a third-party system design consultant who will design a production-grade, multi-tenant SaaS architecture from first principles and industry best practice.
```

with:

```
**Purpose**: Functional requirements specification for a production-grade, multi-tenant SaaS architecture.
```

- [ ] **Step 2: Edit line 15 — delete the consultant sentence**

In line 15's paragraph, find the sentence:

```
The consultant is expected to choose technologies, design the architecture, and make all infrastructure decisions.
```

Delete it. The surrounding sentences ("The system exists today as a working single-user prototype." and "This document specifies the functional requirements for a production system that is reliable, scalable, and maintainable." and "This document specifies *what* the system must do, not *how*.") stay intact.

- [ ] **Step 3: Verify**

```bash
grep -c "consultant" docs/functional-requirements-2026-04-01.md
grep -c "third-party" docs/functional-requirements-2026-04-01.md
```

Expected: both counts = 0.

- [ ] **Step 4: Commit**

```bash
git add docs/functional-requirements-2026-04-01.md
git commit -m "docs(frd): reframe purpose statement; drop consultant references"
```

---

### Task 10: Scrub in-tree Claude trailers from plan files

**Files:**
- Modify: `docs/superpowers/plans/2026-04-12-http-package-refactor.md`
- Modify: `docs/superpowers/plans/2026-04-24-auth-nav-landing-tweaks-plan.md`
- Modify: `docs/superpowers/plans/2026-04-26-landing-mobile-responsive-plan.md`

(Note: if Task 8 deleted any of these files, skip them here.)

- [ ] **Step 1: Identify trailer locations in each file**

For each file:

```bash
grep -n "Co-Authored-By: Claude\|🤖 Generated with" docs/superpowers/plans/2026-04-12-http-package-refactor.md
grep -n "Co-Authored-By: Claude\|🤖 Generated with" docs/superpowers/plans/2026-04-24-auth-nav-landing-tweaks-plan.md
grep -n "Co-Authored-By: Claude\|🤖 Generated with" docs/superpowers/plans/2026-04-26-landing-mobile-responsive-plan.md
```

These trailers appear inside example commit messages embedded in the plan documents (e.g., inside ```` ``` ```` code blocks showing what `git commit` should be run with).

- [ ] **Step 2: Edit each file**

For each occurrence, delete the trailer line. If the deletion creates a stranded blank line at the end of an example commit message body, also delete that blank line. The surrounding example commit message subject and body stay intact — only the trailer goes.

Example transformation:

```
git commit -m "feat: add x

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

becomes:

```
git commit -m "feat: add x"
```

(If the example included a body before the trailer, keep the body; just remove the trailer line.)

- [ ] **Step 3: Verify**

```bash
git grep -E "Co-[Aa]uthored-[Bb]y:.*[Cc]laude|🤖 Generated with" \
  -- ':!docs/superpowers/specs/2026-05-05-open-source-prep-design.md'
```

Expected: empty output. (The spec is excluded because it describes the patterns.)

- [ ] **Step 4: Commit**

```bash
git add docs/superpowers/plans/
git commit -m "docs: strip Claude trailers from in-tree plan examples"
```

---

### Task 11: Update `.gitignore` and untrack `.claude` artifacts

**Files:**
- Modify: `.gitignore`
- Untrack: `.claude/settings.local.json`
- Untrack: `.claude/projects/-Users-btc-Projects-src-drill/memory/feedback_fix_all_issues.md`

- [ ] **Step 1: Edit `.gitignore`**

Read `.gitignore`. Find the `# Claude Code / Superpowers` block:

```
# Claude Code / Superpowers
.superpowers/
.worktrees/
.claude/worktrees/
.playwright-mcp/
```

Replace with:

```
# Claude Code / Superpowers
.superpowers/
.worktrees/
.claude/
.playwright-mcp/
```

(The `.claude/` blanket replaces `.claude/worktrees/`; it covers settings.local.json, projects/, worktrees/, and anything else under `.claude/`.)

- [ ] **Step 2: Untrack the two files**

```bash
git rm --cached .claude/settings.local.json
git rm --cached '.claude/projects/-Users-btc-Projects-src-drill/memory/feedback_fix_all_issues.md'
```

- [ ] **Step 3: Verify**

```bash
git ls-files .claude/
git check-ignore -v .claude/settings.local.json
git check-ignore -v .claude/projects/foo
```

Expected:
- `git ls-files .claude/`: empty.
- `git check-ignore` confirms ignore for both paths via the new `.claude/` rule.

- [ ] **Step 4: Commit**

```bash
git add .gitignore
git commit -m "chore: ignore .claude/ blanket; untrack personal artifacts"
```

---

### Task 12: Fix `CLAUDE.md`

**Files:**
- Modify: `CLAUDE.md`

- [ ] **Step 1: Add preamble at the top**

Insert these two lines as the very first lines of the file (before the existing `# Code Organization` header):

```html
<!-- This file gives Claude Code context for working in this repo. See README.md for human onboarding. -->

```

(The blank line after the comment matters — it separates from the H1 below.)

- [ ] **Step 2: Remove the v0 reference**

Find this line in `CLAUDE.md`:

```
Prototype: ./v0
```

Delete the entire line (it's around line 10 in the current file, inside the `# Code Organization` block).

- [ ] **Step 3: Re-skim for other v0/Python references**

```bash
grep -niE "python|prototype|v0/" CLAUDE.md
```

Expected: empty or only acceptable references (e.g., none in current file). If anything surfaces, evaluate and trim.

- [ ] **Step 4: Verify**

```bash
head -3 CLAUDE.md
grep -c "v0" CLAUDE.md
```

Expected:
- First line: `<!-- This file gives Claude Code context for working in this repo. See README.md for human onboarding. -->`
- `v0` count: 0.

- [ ] **Step 5: Commit**

```bash
git add CLAUDE.md
git commit -m "docs(claude.md): add human-facing preamble; drop v0 reference"
```

---

## Phase 2: Verify File-Level Cleanup

### Task 13: Run `make test` and `go mod verify`

**Files:** none modified.

- [ ] **Step 1: Run `go mod verify`**

```bash
go mod verify
```

Expected: `all modules verified`.

- [ ] **Step 2: Run `make test`**

```bash
make test
```

Expected: green. The full pipeline runs: `buf lint`, codegen check, `go test ./internal/... ./cmd/... -race -count=1 -timeout=300s`, `golangci-lint run ./...`, `cd web && tsc -b && eslint && vitest`.

- [ ] **Step 3: `git grep -i sabermatic` review**

```bash
git grep -i sabermatic | wc -l
git grep -i sabermatic | head -30
```

Skim the full output. Expected: references to the product name in CLAUDE.md, README.md, web frontend (`branding`, `landing`, `about` pages), `internal/branding/`, and infrastructure (`terraform/`, GCP project ID). Anything *surprising or revealing* — internal customer names, unannounced features, partner names — should be removed before publish.

- [ ] **Step 4: No-binaries check**

```bash
git ls-files | xargs -I{} file {} 2>/dev/null \
  | grep -iE "binary|zip" \
  | grep -v -E "\.(png|jpg|jpeg|gif|ico|woff|woff2|svg)$"
```

Expected: empty. Allowed binaries are images and font files; anything else (`.zip`, `.bin`, executable, archive) is a problem.

- [ ] **Step 5: In-tree trailer confirmation**

```bash
git grep -iE "Co-[Aa]uthored-[Bb]y|🤖 Generated with" \
  -- ':!docs/superpowers/specs/2026-05-05-open-source-prep-design.md'
```

Expected: empty. (The spec is excluded because it describes the patterns.) Confirms Task 10's content scrub stuck.

- [ ] **Step 6: If anything fails**

Failures here mean a Phase 1 task introduced a regression or a content-scrub gap. Diagnose, fix, commit, re-run. Do not proceed to Phase 3 until all gates are green.

- [ ] **Step 7: No commit needed (unless Step 3 surfaced cleanups)**

Verification step. Any cleanup from Step 3 should be its own commit.

---

## Phase 3: History Rewrite

### Task 14: Tag rollback point and pre-scan GC

**Files:** none modified.

- [ ] **Step 1: Tag the current HEAD**

```bash
git tag pre-oss-rewrite
git rev-parse pre-oss-rewrite
```

Expected: prints the SHA of the current HEAD.

- [ ] **Step 2: Enumerate author identities**

```bash
git log --all --pretty=format:"%an <%ae>" | sort -u
```

Expected output (current state, will be used to build the rewrite map in Task 16):

```
Brian Tiger Chow <734339+btc@users.noreply.github.com>
Brian Tiger Chow <briantigerchow@gmail.com>
Claude <noreply@anthropic.com>
btc <734339+btc@users.noreply.github.com>
brian tiger chow <734339+btc@users.noreply.github.com>
```

Save this list to a temporary file for Task 16 reference:

```bash
git log --all --pretty=format:"%an <%ae>" | sort -u > /tmp/authors-before.txt
```

- [ ] **Step 3: Pre-scan GC (drop unreachable history)**

```bash
git reflog expire --expire=now --all
git gc --prune=now --aggressive
```

This drops any pre-existing dangling commits so the secret scanners don't waste effort on objects we don't care about.

- [ ] **Step 4: No commit needed**

Tag and gc don't produce commits.

---

### Task 15: Install scanners and run secret scan

**Files:** none modified.

- [ ] **Step 1: Install gitleaks and trufflehog**

```bash
brew install gitleaks trufflehog
which gitleaks trufflehog
```

Expected: both binaries resolve.

- [ ] **Step 2: Run gitleaks across all history**

```bash
gitleaks detect --source . --log-opts="--all" --report-path /tmp/gitleaks.json --report-format json --redact || true
```

(`|| true` because gitleaks exits non-zero on findings; we want to inspect the report regardless.)

Expected: report written to `/tmp/gitleaks.json`. Exit code may be non-zero; that's fine.

- [ ] **Step 3: Run trufflehog across all history**

```bash
trufflehog git file://. --json > /tmp/trufflehog.json 2>/tmp/trufflehog.log || true
```

Expected: report written to `/tmp/trufflehog.json`.

- [ ] **Step 4: Triage findings**

Inspect both reports:

```bash
jq '. | length' /tmp/gitleaks.json 2>/dev/null || cat /tmp/gitleaks.json | head -50
jq -s '. | length' /tmp/trufflehog.json 2>/dev/null || head -50 /tmp/trufflehog.json
```

For each finding, classify:

- **True positive (live or recently-live secret):** rotate the secret first (e.g., regenerate API key, change DB password). Then add the secret value to `/tmp/replace-text.txt` (created in Step 5) for surgical removal in Task 16.
- **False positive (test fixture, placeholder, base64 of harmless data):** add to `.gitleaks.toml` allowlist (commit before proceeding) or simply note and ignore.
- **Already in `.env.example` only as a placeholder:** ignore.

If any true positives surface and rotation is needed: pause Phase 3, rotate, document the rotation in a comment on the rollback tag, then resume.

- [ ] **Step 5: Build `patterns.txt` for filter-repo (only if true positives found)**

If true-positive secrets need surgical removal, create `/tmp/replace-text.txt` with one secret per line, each followed by `==>` and a replacement. Example format:

```
sk-ant-api03-REAL-SECRET-VALUE-HERE==>***REMOVED***
sk-real-stripe-key-here==>***REMOVED***
```

If no true positives, skip this step. Note in your shell that no patterns file is needed.

- [ ] **Step 6: No commit needed (yet)**

Reports live in /tmp; allowlist (if any) is committed separately if needed. The rewrite in Task 16 consumes `/tmp/replace-text.txt`.

---

### Task 16: Run the single combined `git filter-repo` rewrite

**Files:** rewrites all commits in history.

- [ ] **Step 1: Understand the callback contract**

`git filter-repo` callbacks (`--message-callback`, `--email-callback`, `--name-callback`) expect a Python **function body**, not a module. The callback receives a single `bytes` argument named `message`, `email`, or `name` respectively, and must `return` a `bytes` value. The body executes in a context where `re` and standard library modules are auto-imported as needed.

We pass the bodies as inline strings to `git filter-repo`. For readability, this plan shows them with explicit indentation.

- [ ] **Step 2: Run `git filter-repo`**

If `/tmp/replace-text.txt` exists from Task 15:

```bash
git filter-repo \
  --message-callback '
import re
TRAILER_RE = re.compile(rb"(?im)^Co-Authored-By:.*(claude|anthropic).*$\n?")
GENERATED_RE = re.compile(rb"(?im)^.*Generated with \[Claude Code\].*$\n?")
out = TRAILER_RE.sub(b"", message)
out = GENERATED_RE.sub(b"", out)
return out.rstrip() + b"\n"
' \
  --email-callback '
if email == b"noreply@anthropic.com":
    return b"briantigerchow@gmail.com"
return email
' \
  --name-callback '
if name == b"Claude":
    return b"Brian Tiger Chow"
return name
' \
  --replace-text /tmp/replace-text.txt \
  --force
```

If no `/tmp/replace-text.txt` (no true-positive secrets to surgically remove):

```bash
git filter-repo \
  --message-callback '
import re
TRAILER_RE = re.compile(rb"(?im)^Co-Authored-By:.*(claude|anthropic).*$\n?")
GENERATED_RE = re.compile(rb"(?im)^.*Generated with \[Claude Code\].*$\n?")
out = TRAILER_RE.sub(b"", message)
out = GENERATED_RE.sub(b"", out)
return out.rstrip() + b"\n"
' \
  --email-callback '
if email == b"noreply@anthropic.com":
    return b"briantigerchow@gmail.com"
return email
' \
  --name-callback '
if name == b"Claude":
    return b"Brian Tiger Chow"
return name
' \
  --force
```

(`--force` is required because the working repo has commits and a remote; filter-repo's safety check refuses otherwise. We have the `pre-oss-rewrite` tag and the original remote as backups.)

The `GENERATED_RE` line is defensive; the spec verified zero `Generated with` matches in the current repo, but if any creep in during file-level cleanup, this catches them.

Expected: filter-repo runs through ~1,452 commits. Output ends with parsed/new commit count.

- [ ] **Step 3: filter-repo removes the remote — re-add it**

`git filter-repo` defensively removes the `origin` remote to prevent accidental push. Re-add it (we'll change to a different remote in Task 19 anyway, but for verification commands that operate on local refs, having `origin` set helps):

```bash
git remote add origin git@github.com:btc/drill.git
```

(Do NOT push to this remote yet. Task 19 decides where to push.)

- [ ] **Step 4: No commit needed**

filter-repo creates new commits as part of the rewrite; no manual commit required.

---

### Task 17: Verify rewrite

**Files:** none modified.

- [ ] **Step 1: Trailer scan in commit messages**

```bash
git log --all --pretty=format:"%B" | grep -ciE "^Co-(Authored|authored)-[Bb]y:.*claude"
```

Expected: `0`.

- [ ] **Step 2: Anthropic trailer scan**

```bash
git log --all --pretty=format:"%B" | grep -ciE "^Co-(Authored|authored)-[Bb]y:.*anthropic"
```

Expected: `0`.

- [ ] **Step 3: Author identity scan**

```bash
git log --all --pretty=format:"%an <%ae>" | sort -u > /tmp/authors-after.txt
cat /tmp/authors-after.txt
grep -i "anthropic.com" /tmp/authors-after.txt
```

Expected:
- The list does not include any `anthropic.com` email.
- Final grep returns nothing (exit code 1).

- [ ] **Step 4: Generated-with footer scan**

```bash
git log --all --pretty=format:"%B" | grep -c "🤖 Generated with"
```

Expected: `0`.

- [ ] **Step 5: Commit count sanity check**

```bash
git log --all --oneline | wc -l
```

Expected: ~1,452 (matches pre-rewrite count). Filter-repo preserves commit count; only SHAs change.

- [ ] **Step 6: If any verification fails**

Roll back:

```bash
git update-ref refs/heads/main refs/tags/pre-oss-rewrite
git reset --hard pre-oss-rewrite
```

Diagnose the regex or callback issue, fix, re-run Task 16. Do not proceed.

- [ ] **Step 7: No commit needed**

Verification only.

---

### Task 18: Re-scan and post-rewrite GC

**Files:** none modified.

- [ ] **Step 1: Re-run gitleaks**

```bash
gitleaks detect --source . --log-opts="--all" --report-path /tmp/gitleaks-after.json --report-format json --redact || true
```

- [ ] **Step 2: Re-run trufflehog**

```bash
trufflehog git file://. --json > /tmp/trufflehog-after.json 2>/dev/null || true
```

- [ ] **Step 3: Compare to pre-rewrite findings**

```bash
diff <(jq -S . /tmp/gitleaks.json 2>/dev/null) <(jq -S . /tmp/gitleaks-after.json 2>/dev/null) | head -50
```

Expected: any true-positive findings from Task 15 are gone; allowlisted false positives may still appear.

- [ ] **Step 4: Post-rewrite GC**

```bash
git reflog expire --expire=now --all
git gc --prune=now --aggressive
```

This drops the original (pre-rewrite, now-dangling) commits from the local repo. The `pre-oss-rewrite` tag still references the pre-rewrite tip; if you also want to delete the rollback tag (and lose the option to recover): `git tag -d pre-oss-rewrite` then re-run gc. Recommended: keep the tag locally for now; it is local-only and won't be pushed.

- [ ] **Step 5: No commit needed**

Verification only.

---

## Phase 4: Publish

### Task 19: Push to public location

**Files:** none modified.

- [ ] **Step 1: Decide push target**

Recommended path: **fresh public repo** (Section 5.3 of the spec). This sidesteps GitHub-side persistence of pre-rewrite SHAs in PR review threads, Actions logs, and API caches.

Alternative: force-push to existing `btc/drill` after closing all open PRs and deleting old workflow runs. Only choose this if you have a reason to keep the existing repo URL.

- [ ] **Step 2: Create the fresh public repo via `gh`**

If creating fresh under the personal account:

```bash
gh repo create btc/sabermatic --public --description "System-design interview practice with an AI coach" --homepage "https://sabermatic.dev"
```

If creating under a Spanda-LLC GitHub organization (recommended for licensor alignment):

```bash
gh repo create Spanda-LLC/sabermatic --public --description "System-design interview practice with an AI coach" --homepage "https://sabermatic.dev"
```

(If the `Spanda-LLC` GitHub org doesn't exist yet, create it via `https://github.com/organizations/new` first. Free tier is fine for public repos.)

- [ ] **Step 3: Switch the local remote**

```bash
git remote remove origin
git remote add origin git@github.com:Spanda-LLC/sabermatic.git
# Or btc/sabermatic, depending on Step 2 choice
```

- [ ] **Step 4: Push**

```bash
git push -u origin main
git push --tags  # only if you want tags published; pre-oss-rewrite tag should NOT be pushed
```

If you accidentally push `pre-oss-rewrite`: `git push origin :refs/tags/pre-oss-rewrite` to delete the remote tag.

Expected: push succeeds. Browse `https://github.com/<owner>/sabermatic` and verify:
- README renders.
- LICENSE.md is detected by GitHub (sidebar shows "FSL-1.1-Apache-2.0" or similar).
- No commits show "Claude" as author.
- Most recent commits show the canonical Brian author identity.

- [ ] **Step 5: If any push verification fails**

Most likely: a commit was missed by the rewrite. Check via:

```bash
git log --all --pretty=format:"%H %an <%ae> %s" | grep -iE "claude|anthropic" | head -5
```

If results, rollback (`git reset --hard pre-oss-rewrite`), diagnose, re-run Task 16. The remote is fresh, so nothing public has been broken.

- [ ] **Step 6: No commit needed**

Push only.

---

### Task 20: Apply GitHub repo configuration

**Files:** none modified directly; configuration via `gh` CLI and GitHub UI.

- [ ] **Step 1: Set topics**

```bash
gh repo edit --add-topic go --add-topic connectrpc --add-topic react --add-topic postgres --add-topic system-design --add-topic system-design-interview --add-topic ai-agents
```

(Adjust if `gh repo edit` requires the repo path argument: `gh repo edit Spanda-LLC/sabermatic --add-topic ...`.)

Verify:
```bash
gh repo view --json repositoryTopics
```

- [ ] **Step 2: Disable Discussions, enable Issues**

```bash
gh repo edit --enable-discussions=false --enable-issues=true
```

- [ ] **Step 3: Set up branch protection on `main`**

```bash
gh api -X PUT repos/Spanda-LLC/sabermatic/branches/main/protection \
  -f 'required_status_checks=null' \
  -f 'enforce_admins=false' \
  -F 'required_pull_request_reviews[required_approving_review_count]=0' \
  -f 'restrictions=null' \
  -F 'allow_force_pushes=false' \
  -F 'allow_deletions=false'
```

(Adjust the path to match your owner/repo. The minimum useful protection: no force-pushes, no deletions. PR reviews and required status checks are nice-to-have for solo work.)

- [ ] **Step 4: Enable secret scanning and Dependabot alerts**

In the GitHub UI:
- Repo → Settings → Code security → Secret scanning → Enable.
- Repo → Settings → Code security → Dependabot alerts → Enable.

These cannot be enabled programmatically without elevated org permissions. Click through the UI.

- [ ] **Step 5: (Optional) Add Dependabot config**

Save to `.github/dependabot.yml`:

```yaml
version: 2
updates:
  - package-ecosystem: "gomod"
    directory: "/"
    schedule:
      interval: "weekly"
  - package-ecosystem: "npm"
    directory: "/web"
    schedule:
      interval: "weekly"
  - package-ecosystem: "github-actions"
    directory: "/"
    schedule:
      interval: "weekly"
```

Commit and push:

```bash
git add .github/dependabot.yml
git commit -m "ci: add Dependabot config for weekly updates"
git push
```

- [ ] **Step 6: Set social preview image**

In the GitHub UI:
- Repo → Settings → General → Social preview → Upload a 1280×640 image.

Use `docs/assets/hero.png` from Task 4 if it's appropriately sized, or generate a separate preview.

- [ ] **Step 7: Verify GitHub Actions secret scoping**

Read `.github/workflows/ci.yml` and `.github/workflows/deploy.yml`. Confirm:

- `ci.yml`'s `pull_request` trigger does NOT run any step that uses `secrets.WIF_PROVIDER`, `secrets.DEPLOYER_SA`, or any other deployment secret. (Spec Section 7.2: GitHub's default behavior is to NOT pass secrets to fork PR runs, but verify explicitly that no path leaks.)
- `deploy.yml` uses `push` to `main` (or manual dispatch) only — never `pull_request`.
- Top-level `permissions:` blocks scope to least privilege. If absent, consider adding `permissions: read-all` at the workflow root and granting write per-job.

If any leak risk exists, fix the workflow file, commit, push:

```bash
git add .github/workflows/
git commit -m "ci: scope workflow permissions"
git push
```

- [ ] **Step 8: Verify final state**

Open `https://github.com/<owner>/sabermatic` in a browser:
- README renders with badges, hero image, license link, all sections.
- License sidebar reads "FSL-1.1-Apache-2.0".
- Topics show in the About panel.
- Issues tab visible; Discussions tab not visible.
- Star count: 0 (initially, fine).

Click into a few recent commits — author identity should be the canonical Brian author for all of them.

- [ ] **Step 9: Final commit (only if Dependabot config added)**

Dependabot config commit was already pushed in Step 5. No additional commits.

---

## Out of Scope (per spec)

- Closing residual coverage gaps (`rpc/auth.ResetPassword`, `backend.WaitAndCancelSession`).
- Renaming the local repo from `drill` to `sabermatic` (the public repo is named `sabermatic`; local is unchanged).
- `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, issue templates, PR templates.
- Decoupling the codebase from Sabermatic-specific infra.
- Documentation site, API reference, tutorials.
