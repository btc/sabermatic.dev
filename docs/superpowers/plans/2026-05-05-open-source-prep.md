# Open-Source Preparation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Take the private `btc/drill` repo, scrub it, rewrite history, and ship it as a public source-available release of Sabermatic under FSL-1.1-ALv2. Existing remote stays at `github.com/btc/drill`; visibility flips from private to public after force-push.

**Architecture:** Linear pipeline. Phase 1 makes file-level changes as ordinary commits. Phase 2 verifies. Phase 3 rewrites history once (combined trailer-strip + author-rewrite + secret-removal). Phase 4 force-pushes to `btc/drill` and flips visibility. The single-rewrite design ensures one rollback point and one final SHA set.

**Realistic time:** 2–3 sessions. Phase 1 is one session; Phase 3 alone is 1–2 hours minimum; Phase 4 is short. Add latency if any true-positive secrets surface and need rotation.

**Tech Stack:** Git, `git filter-repo` (verify install in Task 14), `gitleaks` and `trufflehog` (Task 15 installs via Homebrew), `gh` CLI for GitHub repo configuration.

**Spec:** `docs/superpowers/specs/2026-05-05-open-source-prep-design.md` (latest commit on `main` after spec revisions).

**Reference for runtime versions:**
- Go: `1.25.x` (from `go.mod`, line 5; CI also pins `1.25.x`)
- Node: `22` (from `.github/workflows/ci.yml`)
- Postgres: `16` (from `terraform/cloud_sql.tf`)

**One-time variable:** Tasks reference the GitHub remote as `btc/drill`. If you decide to rename to `btc/sabermatic` via GitHub UI before or after publish, do a search-and-replace on hardcoded URLs in the README / SECURITY.md as a fixup commit; GitHub's URL redirects mean this is cosmetic.

---

## Phase 1: File-Level Cleanup

Each task in this phase is a standalone commit. Order matters only where noted; otherwise tasks are independent.

### Task 1: Add `LICENSE.md`

**Files:**
- Create: `LICENSE.md`

- [ ] **Step 1: Fetch the canonical FSL-1.1-ALv2 template**

The canonical filename is `FSL-1.1-ALv2.template.md`. The older `FSL-1.1-Apache-2.0.template.md` URL still 301-redirects, so `-L` is included for safety either way:

```bash
curl -sL https://fsl.software/FSL-1.1-ALv2.template.md -o LICENSE.md
```

Verify the file is real markdown (not an HTML error page):

```bash
head -1 LICENSE.md | grep "^# Functional Source License" || echo "ERROR: not a valid FSL file"
```

Expected: the grep prints the heading and no ERROR is emitted. Heading reads `# Functional Source License, Version 1.1, ALv2 Future License`.

If the ERROR line appears, abort, re-fetch, or use a fallback method (e.g., manual download from fsl.software).

- [ ] **Step 2: Substitute the Notice block**

The template's `Notice` block has one line containing two variables — `${year}` and `${licensor name}`. Both must be substituted. After substitution the block must read exactly:

```
## Notice

Copyright 2026 Spanda, LLC
```

Edit `LICENSE.md` so both placeholders are replaced. Do not edit any other section.

- [ ] **Step 3: Verify the file**

```bash
grep -c "Spanda, LLC" LICENSE.md
grep -c "Apache License, Version 2.0" LICENSE.md
wc -l LICENSE.md
```

Expected:
- `Spanda, LLC` count: 1
- `Apache License, Version 2.0` count: ≥1
- File length: ~80–100 lines.

- [ ] **Step 4: Commit**

```bash
git add LICENSE.md
git commit -m "license: add FSL-1.1-ALv2"
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

- **Preferred:** Open a [private security advisory](https://github.com/btc/drill/security/advisories/new) on GitHub.
- **Alternative:** Email `security@spanda.llc`.

Please do not open a public issue for security vulnerabilities.

We aim to acknowledge reports within 3 business days. Coordinated disclosure is appreciated.
```

- [ ] **Step 2: Commit**

```bash
git add SECURITY.md
git commit -m "security: add vulnerability disclosure policy"
```

---

### Task 3: Pin Node version with `.nvmrc`

**Files:**
- Create: `.nvmrc`
- Modify: `web/package.json`

- [ ] **Step 1: Create `.nvmrc`**

Save to `.nvmrc` (root of repo):

```
22
```

(Single line, plain text.)

- [ ] **Step 2: Add `engines.node` to `web/package.json`**

Read `web/package.json`. Add a top-level `engines` field as the **last** top-level key (after `devDependencies`). The standard npm convention is bottom-of-file placement; this avoids the JSON-corruption risk of inserting in the middle.

Example transformation — if the existing JSON ends with:

```json
  "devDependencies": {
    ...
  }
}
```

Add `engines` so it ends with:

```json
  "devDependencies": {
    ...
  },
  "engines": {
    "node": ">=22"
  }
}
```

Note the comma added to the previous closing brace.

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
- Create: `docs/assets/hero.png` (mandatory — see Step 1)

- [ ] **Step 1: Capture the hero screenshot (mandatory)**

The README references `docs/assets/hero.png`. The image must exist before commit, or the public README will show a broken image link.

Capture a screenshot of `https://sabermatic.dev` showing a representative session. Save to `docs/assets/hero.png` (create the directory: `mkdir -p docs/assets`). Aim for ~1600px wide.

If you cannot capture a screenshot in this session, edit Step 2's README heredoc before committing: wrap the line `![Sabermatic hero](docs/assets/hero.png)` in HTML comment markers — `<!-- ![Sabermatic hero](docs/assets/hero.png) -->` — so the image reference doesn't render. Also remove `docs/assets/` from Step 4's `git add` since no file was created.

- [ ] **Step 2: Write the README**

Save to `README.md`:

````markdown
# Sabermatic

> System-design interview practice with an AI coach. Voice-driven, real-time, structured feedback.

[![CI](https://github.com/btc/drill/actions/workflows/ci.yml/badge.svg)](https://github.com/btc/drill/actions/workflows/ci.yml)
[![License: FSL-1.1-ALv2](https://img.shields.io/badge/license-FSL--1.1--ALv2-blue.svg)](LICENSE.md)
[![Go 1.25](https://img.shields.io/badge/go-1.25-00ADD8.svg)](go.mod)

![Sabermatic hero](docs/assets/hero.png)

**Try it:** [sabermatic.dev](https://sabermatic.dev)

> This repo's git slug is `drill` (project codename); the product is Sabermatic.

## License

Sabermatic is released under the [Functional Source License, Version 1.1, Apache 2.0 Future License](LICENSE.md). You may read, fork, run, modify, and learn from the code. You may not run it as a competing service. The license converts to Apache 2.0 on 2028-05-05.

## Why this exists

Sabermatic was built end-to-end with [Claude Code](https://docs.claude.com/claude-code) over ~6 weeks. The `docs/superpowers/specs/` and `docs/superpowers/plans/` directories show every feature's spec → plan → implementation cycle in real use. `CLAUDE.md` documents the project's engineering conventions. Source-available because we want the workflow and the code to be readable, but not trivially clonable as a competing service.

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
git clone https://github.com/btc/drill.git
cd drill
cp .env.example .env
# Populate .env per the comments inside it
make dev
```

`make dev` starts the Vite dev server and the Go backend (with hot reload via `air`) on port `:8080`.

See `.env.example` for every key the backend reads. Some keys are only needed for specific features:

- **LLM features** require `ANTHROPIC_API_KEY` (and optionally `OPENAI_API_KEY`).
- **Stripe billing** requires the `STRIPE_*` keys plus `make stripe-setup` to populate `STRIPE_WEBHOOK_SECRET`.
- **GCP Secret Manager** is a no-op locally; the backend reads from `.env` instead.
- **BigQuery analytics** (event sink) requires a deployed environment.
- **OAuth (Google, GitHub)** requires registered client IDs/secrets.

## Testing

```bash
make test
```

Runs: `buf lint`, codegen check, `go test ./internal/... ./cmd/... -race -count=1`, `golangci-lint`, frontend `tsc --noEmit -p tsconfig.app.json`, ESLint, Vitest. CI runs the same target.

## Deployment

See `terraform/` for the GCP infrastructure that backs production. `scripts/deploy.sh` triggers a Cloud Run deploy.

## Contributing

Sabermatic is source-available, not open source. We are not currently accepting outside pull requests, but bug reports via Issues are welcome.
````

- [ ] **Step 3: Verify**

```bash
test -f README.md
test -f docs/assets/hero.png || echo "WARNING: hero image missing — confirm the line is commented out"
head -3 README.md
grep -c "FSL-1.1-ALv2" README.md
grep -c "btc/drill" README.md
```

Expected:
- README exists; first line: `# Sabermatic`.
- Hero image exists OR the warning is acknowledged (with the README line commented out).
- License grep ≥1.
- `btc/drill` grep ≥2 (CI badge URL + clone URL). The advisory URL lives in `SECURITY.md`, not README.

- [ ] **Step 4: Commit**

```bash
git add README.md docs/assets/
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

- [ ] **Step 2: Delete the directory**

```bash
git rm -rf v0/
```

- [ ] **Step 3: Verify**

```bash
test ! -d v0/ && echo "deleted"
git status --short | grep -c "^D"
```

- [ ] **Step 4: Commit**

```bash
git commit -m "chore: remove v0 Python prototype (superseded by Go rewrite)"
```

---

### Task 6: Delete `docs/coverage-risk-report.md`

**Files:**
- Delete: `docs/coverage-risk-report.md`

- [ ] **Step 1: Delete**

```bash
git rm docs/coverage-risk-report.md
```

- [ ] **Step 2: Verify**

```bash
test ! -f docs/coverage-risk-report.md && echo "deleted"
```

- [ ] **Step 3: Commit**

```bash
git commit -m "docs: remove stale coverage risk report (risks closed)"
```

---

### Task 7: Delete `terraform/showHN` and tighten ignore rules

**Files:**
- Delete: `terraform/showHN`
- Modify: `terraform/.gitignore`

- [ ] **Step 1: Delete the file**

```bash
rm terraform/showHN
```

(File is untracked, so plain `rm`, not `git rm`.)

- [ ] **Step 2: Tighten `terraform/.gitignore`**

Read `terraform/.gitignore`. Append these lines at the end:

```
showHN
*.zip
*.bin
```

(Spec Section 3.3 listed these patterns under root `.gitignore`; we place them in `terraform/.gitignore` because relative paths there are simpler and coverage is identical for `terraform/`-scoped artifacts.)

- [ ] **Step 3: Verify**

```bash
test ! -f terraform/showHN && echo "deleted"
git check-ignore -v terraform/showHN
```

Expected: deletion confirmed; `git check-ignore` resolves via the new rule.

- [ ] **Step 4: Commit**

```bash
git add terraform/.gitignore
git commit -m "chore: ignore terraform binary plan artifacts"
```

---

### Task 8: Triage `docs/superpowers/plans/`

**Files:**
- Delete: `docs/superpowers/plans/2026-04-13-saas-starter-extraction.md` (definitely)
- Delete: additional files identified in human-checkpoint review

- [ ] **Step 1: Delete the saas-starter-extraction file**

```bash
git rm docs/superpowers/plans/2026-04-13-saas-starter-extraction.md
```

(Filename has no `-design` suffix — verify with `ls` first if uncertain.)

- [ ] **Step 2: Generate candidate list and apply triage criterion**

```bash
ls docs/superpowers/plans/ | grep -iE "landing|marketing|growth|show-hn|pricing"
```

For each match, read the file and decide using this criterion: **"would I show this to a competitor?"** Drop if it discusses go-to-market, competitive positioning, pricing strategy, or growth tactics. Keep if it's a system-design or refactor plan that happens to name a landing page.

Apply your judgment — owner has delegated this decision. No human checkpoint needed.

- [ ] **Step 3: Delete files identified in Step 2**

For each file you decided to drop:

```bash
git rm docs/superpowers/plans/<filename>
```

- [ ] **Step 4: Verify**

```bash
git status --short | grep -c "^D"
ls docs/superpowers/plans/ | wc -l
```

- [ ] **Step 5: Commit**

Replace `N` below with the actual count of additionally-deleted files (write `0` if only `saas-starter-extraction.md` was dropped):

```bash
git commit -m "docs: triage internal plans — drop business-strategy specs

Removed: saas-starter-extraction.md + N other business/growth plans.
Kept architecture/refactor/system-design plans."
```

---

### Task 9: Reframe `docs/functional-requirements-2026-04-01.md`

**Files:**
- Modify: `docs/functional-requirements-2026-04-01.md` (3 places)

- [ ] **Step 1: Edit line 5 — Purpose statement**

Replace:

```
**Purpose**: Input for a third-party system design consultant who will design a production-grade, multi-tenant SaaS architecture from first principles and industry best practice.
```

with:

```
**Purpose**: Functional requirements specification for a production-grade, multi-tenant SaaS architecture.
```

- [ ] **Step 2: Edit line 15 — delete the consultant sentence**

In line 15's paragraph, delete this sentence (only):

```
The consultant is expected to choose technologies, design the architecture, and make all infrastructure decisions.
```

The surrounding sentences stay intact.

- [ ] **Step 3: Edit line 180 — FR-076 row**

The line currently reads:

```
| **FR-076** | Whether candidates can permanently delete sessions shall be designed to satisfy GDPR requirements. The specific behavior is a design decision for the consultant. |
```

Change to:

```
| **FR-076** | Whether candidates can permanently delete sessions shall be designed to satisfy GDPR requirements. |
```

(Drop the trailing "The specific behavior is a design decision for the consultant." sentence.)

- [ ] **Step 4: Verify**

```bash
grep -c "consultant" docs/functional-requirements-2026-04-01.md
grep -c "third-party" docs/functional-requirements-2026-04-01.md
```

Expected: both counts = 0.

- [ ] **Step 5: Commit**

```bash
git add docs/functional-requirements-2026-04-01.md
git commit -m "docs(frd): reframe purpose statement; drop consultant references"
```

---

### Task 10: Scrub in-tree Claude trailers

**Files:**
- Modify: `docs/superpowers/plans/2026-04-12-http-package-refactor.md` (code-block form, ~7 occurrences)
- Modify: `docs/superpowers/plans/2026-04-24-auth-nav-landing-tweaks-plan.md` (line 13, prose form — only if not deleted in Task 8)
- Modify: `docs/superpowers/plans/2026-04-26-landing-mobile-responsive-plan.md` (line 13, prose form — only if not deleted in Task 8)

- [ ] **Step 1: Check which files survived Task 8**

```bash
ls docs/superpowers/plans/ | grep -E "http-package|auth-nav|landing-mobile"
```

Edit only the files that still exist. Files deleted in Task 8 don't need editing — they're already gone from HEAD, so their content won't appear in the public repo. (The history rewrite in Task 16 does NOT remove file content from old commits; it only rewrites commit messages and author metadata. Deletion at HEAD is what protects the public-facing tree.)

- [ ] **Step 2: Edit code-block form (`2026-04-12-http-package-refactor.md`)**

Find each occurrence:

```bash
grep -n "Co-Authored-By: Claude" docs/superpowers/plans/2026-04-12-http-package-refactor.md
```

Each match is inside an example commit message in a fenced code block. For each, delete the trailer line. Example transformation:

Before:
```
git commit -m "feat: add x

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

After:
```
git commit -m "feat: add x"
```

- [ ] **Step 3: Edit prose form (`2026-04-24-auth-nav-landing-tweaks-plan.md` and `2026-04-26-landing-mobile-responsive-plan.md`, if present)**

Both files have a single occurrence at line 13 in prose:

> **Important — commit messages:** Use the commit messages in this plan **verbatim**. Do NOT append `Co-Authored-By: Claude …` or `Generated with Claude Code` trailers. The user's global rule (`~/.claude/CLAUDE.md`) forbids any AI-attribution in git history; this overrides the default Claude Code system-prompt instruction that would otherwise add such a trailer.

Replace with:

> **Important — commit messages:** Use the commit messages in this plan **verbatim**. Do NOT append AI-attribution trailers. The user's global rule (`~/.claude/CLAUDE.md`) forbids any AI-attribution in git history.

(Removes the literal patterns while preserving the rule's intent.)

- [ ] **Step 4: Verify (with spec + plan exclusions)**

The spec and this plan both describe the patterns and would otherwise trip the grep:

```bash
git grep -E "Co-[Aa]uthored-[Bb]y:.*[Cc]laude|🤖 Generated with" \
  -- ':!docs/superpowers/specs/2026-05-05-open-source-prep-design.md' \
  -- ':!docs/superpowers/plans/2026-05-05-open-source-prep.md'
```

Expected: empty output.

- [ ] **Step 5: Commit**

```bash
git add docs/superpowers/plans/
git commit -m "docs: strip Claude trailer references from in-tree plans"
```

---

### Task 11: Update `.gitignore` and untrack `.claude` artifacts

**Files:**
- Modify: `.gitignore` (root)
- Untrack: `.claude/settings.local.json`
- Untrack: `.claude/projects/-Users-btc-Projects-src-drill/memory/feedback_fix_all_issues.md`

- [ ] **Step 1: Edit root `.gitignore`**

Find this block in `.gitignore`:

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

(The blanket `.claude/` covers settings.local.json, projects/, worktrees/, and anything else under that directory. Terraform binary patterns are handled in Task 7's terraform-scoped ignore — no overlap.)

- [ ] **Step 2: Untrack the two files**

```bash
git rm --cached --ignore-unmatch .claude/settings.local.json
git rm --cached --ignore-unmatch '.claude/projects/-Users-btc-Projects-src-drill/memory/feedback_fix_all_issues.md'
```

`--ignore-unmatch` makes the step idempotent — if either file was already untracked by a prior run, the command no-ops instead of erroring.

- [ ] **Step 3: Verify**

```bash
git ls-files .claude/
git check-ignore -v .claude/settings.local.json
```

Expected:
- `git ls-files .claude/`: empty.
- `git check-ignore` confirms ignore via `.gitignore:N:.claude/`.

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

Insert these two lines as the very first lines of the file (before `# Code Organization`):

```html
<!-- This file gives Claude Code context for working in this repo. See README.md for human onboarding. -->

```

(Blank line after the comment matters.)

- [ ] **Step 2: Remove the v0 reference**

Delete this line:

```
Prototype: ./v0
```

- [ ] **Step 3: Re-skim for other references**

```bash
grep -niE "python|prototype|v0/" CLAUDE.md
```

Expected: empty or only acceptable references. If anything surfaces, evaluate.

- [ ] **Step 4: Verify**

```bash
head -3 CLAUDE.md
grep -c "v0" CLAUDE.md
```

Expected:
- First line is the HTML comment.
- `v0` count: 0.

- [ ] **Step 5: Commit**

```bash
git add CLAUDE.md
git commit -m "docs(claude.md): add human-facing preamble; drop v0 reference"
```

---

## Phase 2: Verify File-Level Cleanup

### Task 13: Run pre-publish gates

**Files:** none modified (any cleanups surfaced here are their own commits).

- [ ] **Step 1: `go mod verify`**

```bash
go mod verify
```

Expected: `all modules verified`.

- [ ] **Step 2: `make test`**

```bash
make test
```

Expected: green.

- [ ] **Step 3: `git grep -i sabermatic` review**

```bash
git grep -i sabermatic | wc -l
git grep -i sabermatic | head -30
```

Expected references: README, CLAUDE.md, web frontend (`branding`, `landing`, `about`), `internal/branding/`, terraform (GCP project ID). Anything *surprising* — internal customer names, unannounced features, partner names — should be removed. Commit any cleanups separately.

- [ ] **Step 4: `git grep -i anthropic` symmetry check**

```bash
git grep -i anthropic | grep -v "^go.sum" | grep -v "^go.mod" | head -30
```

Expected references: SDK imports in source files, `.env.example` keys. The `anthropic-sdk-go` module name in `go.mod`/`go.sum` is fine. Anything else (internal partner notes, unmasked API keys in tests) should be cleaned up.

- [ ] **Step 5: No-binaries check**

```bash
git ls-files | xargs -I{} file {} 2>/dev/null \
  | grep -iE "binary|zip" \
  | grep -v -E "\.(png|jpg|jpeg|gif|ico|woff|woff2|svg)$"
```

Expected: empty (only images and fonts as binaries).

- [ ] **Step 6: In-tree trailer confirmation**

```bash
git grep -iE "Co-[Aa]uthored-[Bb]y|🤖 Generated with" \
  -- ':!docs/superpowers/specs/2026-05-05-open-source-prep-design.md' \
  -- ':!docs/superpowers/plans/2026-05-05-open-source-prep.md'
```

Expected: empty.

- [ ] **Step 7: `terraform/tfplan` untracked confirmation**

```bash
git ls-files terraform/tfplan terraform/showHN 2>&1
```

Expected: empty (neither file tracked).

- [ ] **Step 8: `docs/bugs/` post-mortem eyeball pass**

```bash
ls docs/bugs/
```

For each post-mortem, skim the framing. The criterion: "we caught and fixed it" is fine; "live customer-impacting incident without resolution" is not. If you have any uncertainty about a post-mortem's tone, surface the file path and a one-line summary to the owner and wait for keep/redact/remove decision. Do not redact on your own judgment.

- [ ] **Step 9: One-pass eyeball read**

Use `git log` to find Phase 1's first commit, then diff from before it:

```bash
PHASE1_FIRST=$(git log --reverse --pretty=format:"%H %s" | grep -E "^[a-f0-9]+ license: add" | head -1 | cut -d' ' -f1)
git diff "${PHASE1_FIRST}^..HEAD" --name-only
```

Read each Phase 1 file for tone, typos, embarrassing inline comments. This is the spec's pre-publish "eyeball pass" gate.

- [ ] **Step 10: If anything fails**

Failures here mean a Phase 1 task introduced a regression or missed a cleanup. Diagnose, fix as separate commits, re-run. Do not proceed to Phase 3 until all gates are green.

- [ ] **Step 11: No commit needed (unless cleanups surfaced)**

Verification only.

---

## Phase 3: History Rewrite

### Task 14: Pre-rewrite preparation

**Files:** none modified.

- [ ] **Step 1: Mirror-clone backup (offline insurance)**

Before any destructive operation, take an offline mirror clone. Use a timestamp with seconds so re-runs in the same day don't collide:

```bash
BACKUP=/tmp/drill-backup-$(date +%Y%m%d-%H%M%S).git
git clone --mirror git@github.com:btc/drill.git "$BACKUP"
git -C "$BACKUP" fsck --no-progress
du -sh "$BACKUP"
echo "Backup at: $BACKUP"
```

`fsck` confirms object-DB integrity. Note the `$BACKUP` path printed at the end — recovery is `git clone "$BACKUP" fresh-drill` if you ever need it.

- [ ] **Step 2: Verify `git filter-repo` is installed and working**

```bash
git filter-repo --version
```

Expected: prints a SHA or version number. If you see `bad interpreter` or `ModuleNotFoundError`, repair via:

```bash
brew install git-filter-repo
git filter-repo --version  # re-verify
```

- [ ] **Step 3: Remove all linked worktrees**

`git filter-repo` operates on the whole object DB and refs that worktrees may reference. Any active worktree blocks the rewrite or risks corruption. Enumerate and remove:

```bash
git worktree list
```

For each linked worktree (everything except the main path), remove:

```bash
git worktree remove --force <path>
```

After removing all, prune:

```bash
git worktree prune
git worktree list
```

Expected final state: only the main worktree (`/Users/btc/Projects/src/drill`) remains.

- [ ] **Step 4: Capture pre-rewrite commit count**

```bash
git log --all --oneline | wc -l > /tmp/commit-count-before.txt
cat /tmp/commit-count-before.txt
```

This count is referenced in Task 17 to verify the rewrite preserved commit count.

- [ ] **Step 5: Tag the rollback point (idempotent)**

```bash
git tag -f pre-oss-rewrite
git rev-parse pre-oss-rewrite
```

`-f` lets this re-run safely if a prior aborted attempt left a stale tag.

- [ ] **Step 6: Enumerate author identities**

```bash
git log --all --pretty=format:"%an <%ae>" | sort -u | tee /tmp/authors-before.txt
```

Expected output (current state):

```
Brian Tiger Chow <734339+btc@users.noreply.github.com>
Brian Tiger Chow <briantigerchow@gmail.com>
Claude <noreply@anthropic.com>
btc <734339+btc@users.noreply.github.com>
brian tiger chow <734339+btc@users.noreply.github.com>
```

Note: Task 16 rewrites only the `Claude <noreply@anthropic.com>` author (the one with AI attribution). The four Brian variants remain as-is — author normalization is marked optional in spec Section 5.1 and is **out of scope** for this plan.

- [ ] **Step 7: Pre-scan GC**

```bash
git reflog expire --expire=now --all
git gc --prune=now --aggressive
```

Drops any pre-existing dangling commits before scanners walk objects.

- [ ] **Step 8: No commit needed**

Tag and gc don't produce commits.

---

### Task 15: Install scanners and run secret scan

**Files:** none modified directly (any allowlist file is created if needed).

- [ ] **Step 1: Install scanners**

```bash
brew install gitleaks trufflehog
which gitleaks trufflehog
```

Expected: both binaries resolve.

- [ ] **Step 2: Run gitleaks across all history**

```bash
gitleaks detect --source . --log-opts="--all" \
  --report-path /tmp/gitleaks.json --report-format json --redact || true
```

(`|| true` because gitleaks exits non-zero on findings; we want to inspect the report regardless.)

- [ ] **Step 3: Run trufflehog across all history**

```bash
trufflehog git file://. --json > /tmp/trufflehog.json 2>/tmp/trufflehog.log || true
```

- [ ] **Step 4: Triage findings**

Inspect both reports:

```bash
jq '. | length' /tmp/gitleaks.json 2>/dev/null || cat /tmp/gitleaks.json | head -100
jq -s '. | length' /tmp/trufflehog.json 2>/dev/null || head -100 /tmp/trufflehog.json
```

Classify each finding:

- **True positive (live or recently-live secret):** rotate first (regenerate API key, change DB password). Then add the secret value to `/tmp/replace-text.txt` (Step 5).
- **False positive (test fixture, placeholder, base64 of harmless data):** add to `.gitleaks.toml` allowlist if you want gitleaks to stop flagging it on re-scan; otherwise just ignore.
- **Already-public placeholder:** ignore.

If any true positives surface and rotation is needed: pause Phase 3, rotate, document the rotation in a comment, then resume. Add a separate Phase 1-style cleanup commit if `.gitleaks.toml` was created — and re-run Task 13 verification on the new state before proceeding.

- [ ] **Step 5: Build `/tmp/replace-text.txt` for filter-repo's `--replace-text` flag (only if true positives found)**

If true-positive secrets need surgical removal, create `/tmp/replace-text.txt` with one secret per line, each followed by `==>` and a replacement:

```
sk-ant-api03-REAL-SECRET-VALUE-HERE==>***REMOVED***
sk-real-stripe-key-here==>***REMOVED***
```

If no true positives, skip — note in shell that no replace-text file is needed. Task 16 has two invocation variants.

- [ ] **Step 6: No commit needed (unless allowlist file added)**

If `.gitleaks.toml` was added in Step 4: it should be its own commit. Re-run Task 13 verification.

---

### Task 16: Run the single combined `git filter-repo` rewrite

**Files:** rewrites all commits in history.

- [ ] **Step 1: Understand the callback contract**

`git filter-repo` callbacks (`--message-callback`, `--email-callback`, `--name-callback`) expect a Python **function body**, not a module. The body receives a single `bytes` argument (`message`, `email`, or `name`) and must `return` a `bytes` value. The `re` module is available in the body's globals; an explicit `import re` is harmless and works either way.

- [ ] **Step 2: Dry-run first**

`git filter-repo` supports `--dry-run`, which writes the rewritten objects to `.git/filter-repo/fast-export.{original,filtered}` without replacing the actual `.git`. Use it to inspect the result before committing:

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
  --dry-run --force
```

Inspect with explicit before/after counts (the message-callback DOES run during dry-run; only embedded-SHA translation is disabled):

```bash
ls .git/filter-repo/
echo "Original trailer count:"
grep -ciE "Co-Authored-By:.*Claude" .git/filter-repo/fast-export.original
echo "Filtered trailer count (expect 0):"
grep -ciE "Co-Authored-By:.*Claude" .git/filter-repo/fast-export.filtered
```

Expected: original ~1,100+, filtered = 0. If filtered > 0, the regex needs adjustment — do not proceed to Step 3.

- [ ] **Step 3: Run for real**

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

If no replace-text file:

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

Expected: filter-repo runs through all commits. Output ends with parsed/new commit count.

- [ ] **Step 4: filter-repo defensively removed `origin` — leave it removed**

`git filter-repo` removes the `origin` remote to prevent accidental push of the rewritten history to the wrong place. Do **not** re-add `btc/drill` here — Task 19 re-adds it intentionally as the final publish step. Leaving `origin` unset until then prevents accidental pushes.

- [ ] **Step 5: No commit needed**

filter-repo creates new commits as part of the rewrite.

---

### Task 17: Verify rewrite

**Files:** none modified.

- [ ] **Step 1: Trailer scan**

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
- Author list does not include any `anthropic.com` email.
- Final grep returns nothing (exit code 1).

- [ ] **Step 4: Generated-with footer scan**

```bash
git log --all --pretty=format:"%B" | grep -c "🤖 Generated with"
```

Expected: `0`.

- [ ] **Step 5: Commit count sanity check (relative)**

```bash
git log --all --oneline | wc -l
cat /tmp/commit-count-before.txt
```

Expected: post-rewrite count equals pre-rewrite count from Task 14 Step 4 (filter-repo preserves commit count; only SHAs change).

- [ ] **Step 6: If any verification fails — rollback**

```bash
git reset --hard pre-oss-rewrite
```

Diagnose, fix, re-run Task 16. Do not proceed.

- [ ] **Step 7: No commit needed**

Verification only.

---

### Task 18: Re-scan and post-rewrite GC

**Files:** none modified.

- [ ] **Step 1: Re-run gitleaks**

```bash
gitleaks detect --source . --log-opts="--all" \
  --report-path /tmp/gitleaks-after.json --report-format json --redact || true
```

- [ ] **Step 2: Re-run trufflehog**

```bash
trufflehog git file://. --json > /tmp/trufflehog-after.json 2>/dev/null || true
```

- [ ] **Step 3: Verify true positives are gone (count check, not just diff)**

```bash
jq '[.[] | select(.RuleID != null)] | length' /tmp/gitleaks-after.json 2>/dev/null
```

Expected: only allowlisted false positives remain. The true positives identified in Task 15 must be absent. If `.gitleaks.toml` was added, gitleaks may report 0 even with prior false positives.

If any true positive from Task 15's list still appears: rollback (`git reset --hard pre-oss-rewrite`), check `/tmp/replace-text.txt` contents, fix, re-run Task 16.

- [ ] **Step 4: Post-rewrite GC**

```bash
git reflog expire --expire=now --all
git gc --prune=now --aggressive
```

Drops the original (now-dangling) commits from the local repo. The `pre-oss-rewrite` tag still references the pre-rewrite tip locally; do not delete it until publish has succeeded (Task 19).

- [ ] **Step 5: No commit needed**

Verification only.

---

## Phase 4: Publish

### Task 19: Force-push and flip visibility

**Files:** none modified locally.

- [ ] **Step 1: Re-add the `origin` remote**

```bash
git remote add origin git@github.com:btc/drill.git
git remote -v
```

Expected: `origin` resolves to `git@github.com:btc/drill.git` for both fetch and push.

- [ ] **Step 2: Force-push**

```bash
git push --force-with-lease origin main
```

`--force-with-lease` is safer than `--force`: it refuses if the remote has commits we don't know about. Since this is a private repo with no concurrent contributors, this should succeed.

If `--force-with-lease` is rejected because the remote has progressed (unlikely): fetch, inspect, and decide whether to integrate or override:

```bash
git fetch origin
# inspect: git log origin/main..main and main..origin/main
git push --force origin main  # only after deciding the override is correct
```

- [ ] **Step 3: Do NOT push tags**

The only tag in the local repo is `pre-oss-rewrite` (rollback). Pushing it would publicly reveal the rewrite. **Do not run `git push --tags`.** If you need to push a specific release tag later, push it explicitly: `git push origin <release-tag>`.

- [ ] **Step 4: Verify GitHub-side state**

Browse `https://github.com/btc/drill` (still private at this point):

- Most recent commits show the canonical Brian author identity for all of them.
- No commits show "Claude" as author.
- All Phase 1 cleanup commits are present.

Spot-check one commit's "details" view to confirm the author rewrite worked:

```bash
gh api repos/btc/drill/commits/$(git rev-parse main) --jq '{author: .author.login, name: .commit.author.name, email: .commit.author.email}'
```

Expected: name and email match the canonical Brian identity.

- [ ] **Step 5: Verify GitHub Actions secret scoping (before flipping public)**

Read `.github/workflows/ci.yml` and `.github/workflows/deploy.yml`. Confirm:

- `ci.yml`'s `pull_request` trigger does NOT use any deployment secret (`WIF_PROVIDER`, `DEPLOYER_SA`).
- `deploy.yml` is NOT triggered by `pull_request`.

If any secret is reachable from a `pull_request` trigger (which, post-public, would mean fork PRs could exfiltrate secrets): fix the workflow file, commit, force-push.

```bash
git add .github/workflows/
git commit -m "ci: scope workflow permissions"
git push --force-with-lease origin main
```

Note: GitHub's default behavior is NOT to pass secrets to fork PR runs, so this is belt-and-suspenders. Still verify.

- [ ] **Step 6: Flip visibility to public**

GitHub UI: `https://github.com/btc/drill/settings` → "Change repository visibility" → "Make public" → confirm with the repo name.

This cannot be done via `gh` CLI without elevated tokens. Manual UI step.

- [ ] **Step 7: Verify**

Visit `https://github.com/btc/drill` in an incognito browser window. Confirm:
- Repo loads (no auth required).
- README renders with badges, hero image, all sections.
- License sidebar reads "FSL-1.1-ALv2".
- Most recent commits show canonical Brian author.

- [ ] **Step 8: No commit needed (unless workflow fix)**

The workflow fix in Step 5 (if needed) was already committed and pushed in that step.

---

### Task 20: Apply GitHub repo configuration

**Files:** none modified directly; configuration via `gh` CLI and GitHub UI.

- [ ] **Step 1: Set description and homepage**

```bash
gh repo edit btc/drill --description "System-design interview practice with an AI coach" --homepage "https://sabermatic.dev"
```

- [ ] **Step 2: Set topics**

```bash
gh repo edit btc/drill \
  --add-topic go \
  --add-topic connectrpc \
  --add-topic react \
  --add-topic postgres \
  --add-topic system-design \
  --add-topic system-design-interview \
  --add-topic ai-agents
```

Verify:
```bash
gh repo view btc/drill --json repositoryTopics
```

- [ ] **Step 3: Disable Discussions, enable Issues**

```bash
gh repo edit btc/drill --enable-discussions=false --enable-issues=true
```

- [ ] **Step 4: Set up branch protection on `main`**

For solo work, the load-bearing protection is "no force-push, no deletions." We omit required reviews and required status checks per the spec resolution.

```bash
gh api -X PUT repos/btc/drill/branches/main/protection \
  -F 'required_status_checks=null' \
  -F 'enforce_admins=false' \
  -F 'required_pull_request_reviews=null' \
  -F 'restrictions=null' \
  -F 'allow_force_pushes=false' \
  -F 'allow_deletions=false'
```

(All `-F` flags, not `-f` — `-f` sends literal strings, but `null` and booleans require `-F` for proper JSON encoding. A `-f` invocation here returns 422.)

- [ ] **Step 5: Enable secret scanning and Dependabot alerts**

GitHub UI:
- Repo → Settings → Code security → Secret scanning → Enable.
- Repo → Settings → Code security → Dependabot alerts → Enable.

These cannot be enabled programmatically without elevated permissions. Click through the UI.

- [ ] **Step 6: (Optional) Add Dependabot config**

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
git push origin main
```

- [ ] **Step 7: Set social preview image**

GitHub UI: Repo → Settings → General → Social preview → Upload a 1280×640 image.

`docs/assets/hero.png` may be the wrong aspect ratio (it's a screenshot, not a 2:1 social card). Most likely you need a separate `docs/assets/social-preview.png` at exactly 1280×640. Build it (e.g., from the hero image with padding) or use a generator.

- [ ] **Step 8: Verify final state**

Open `https://github.com/btc/drill` in a clean browser:
- README renders with badges, hero image, license link.
- License sidebar reads "FSL-1.1-ALv2".
- Topics show in the About panel.
- Issues tab visible; Discussions tab not visible.
- Branch protection visible at Settings → Branches.

Click into a few recent commits — author identity should be the canonical Brian author for all of them.

- [ ] **Step 9: No additional commits needed**

Dependabot config commit was already pushed in Step 6.

---

## Out of Scope (per spec)

- Closing residual coverage gaps (`rpc/auth.ResetPassword`, `backend.WaitAndCancelSession`).
- Renaming the local repo or the GitHub repo from `drill` to `sabermatic` (optional cosmetic; user can rename via GitHub UI any time, redirects auto-handled).
- Author normalization for the four Brian author-identity variants (spec marks optional).
- `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, issue templates, PR templates.
- Decoupling the codebase from Sabermatic-specific infra.
- Documentation site, API reference, tutorials.
