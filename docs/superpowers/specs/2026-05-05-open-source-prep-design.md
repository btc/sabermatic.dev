# Open-Source Preparation Design

**Date:** 2026-05-05
**Repo:** github.com/btc/drill (codename `drill`, product Sabermatic)
**Goal:** Prepare the repo for public release as a marketing / Show-HN play. Source-available with an FSL license that prevents others from running Sabermatic as a competing service. Eyeballs and credibility, not a contributor community.

## Strategy

Two truths shape every decision below:

1. **Source-available, not OSS.** The license carries the anti-compete posture. The repo's job is to be readable, runnable, and not embarrassing — not to court contributors.
2. **The product is Sabermatic.** Anything that makes Sabermatic look more credible stays. Anything that distracts from that — internal artifacts, stale risk audits, dead prototypes — goes.

## 1. License

Adopt **FSL-1.1-Apache-2.0** (Functional Source License, 2-year non-compete, converts to Apache 2.0).

- File: `LICENSE.md` at repo root.
- Source: published FSL text from fsl.software, no edits to the body.
- No per-file headers. FSL doesn't require them and they add noise.
- README must surface the license clearly: one short paragraph near the top stating "FSL 1.1 (Apache 2.0); converts to Apache 2.0 in 2 years; you may not run this as a competing service."

**Substitutions in the FSL template:**

| Placeholder | Value |
|---|---|
| Licensor | `Spanda, LLC` |
| Copyright year | `2026` |
| Change Date | `2028-05-05` (2 years from publication) |
| Permitted Purpose | (fixed in template — no substitution; do not author anything new here) |

Rationale for FSL over BSL/ELv2/SSPL: cleanest text, recognized by sophisticated readers, eventual permissive conversion gives the "we're not pretending" signal that plays well on HN. Apache 2.0 conversion target (over MIT) for the patent grant.

Licensor is **Spanda, LLC** (not Brian personally) to align with the public marketing on `web/src/pages/about.tsx` ("Sabermatic is a product of Spanda, LLC"). This presumes the LLC owns the IP — verify before publish.

## 2. README

Three audiences, top to bottom. Each section gets only what its audience needs.

### 2.1 Top fold — HN reader, 30 seconds

- One-sentence product description: what Sabermatic is and who it's for.
- Hero screenshot or short GIF.
- Three badges: license (FSL-1.1-Apache-2.0), CI status (`![CI](https://github.com/<owner>/<repo>/actions/workflows/ci.yml/badge.svg)`), Go version.
- Link to sabermatic.dev.
- One-paragraph license posture in plain English.
- One disambiguation line: "This repo's git slug is `drill` (project codename); the product is Sabermatic."

### 2.2 "Why this exists" hook — HN reader, 60 seconds

A short section between top fold and middle, picking ONE angle. Candidates:

- **AI-assisted-development workflow.** ~1,500 commits built with Claude Code. The `docs/superpowers/specs/` and `docs/superpowers/plans/` directories show the spec → plan → implementation cycle in real use. `CLAUDE.md` documents the project's conventions. Source-available because we want the workflow to be readable but not trivially clonable as a competing service.
- **Real-time interview platform with WebSocket → ConnectRPC migration.** The `docs/superpowers/specs/2026-04-05-connectrpc-migration-design.md` chain shows the migration mid-project.
- **Source-available licensing case study.** Why FSL over BSL/ELv2/SSPL, and what that decision means for readers.

Pick the angle at write time based on which feels most honest. Default to the AI-workflow angle.

### 2.3 Middle — developer skimming, 5 minutes

- Architecture overview: Go backend, React/Vite frontend, Postgres, ConnectRPC, GCP (Cloud Run + Cloud SQL + Secret Manager + BigQuery analytics), Stripe billing.
- Diagram (mermaid) showing request flow and major components.
- Key directories (`internal/`, `web/`, `pb/`, `terraform/`, `sql/`) with one-line each.
- Pointer to `CLAUDE.md` for engineering conventions and AI-assisted workflow.

### 2.4 Bottom — running locally, 30 minutes

- Prerequisites: Go (version from `go.mod`), Node 22+ (current LTS; CI workflow `.github/workflows/ci.yml` is the source of truth — verify at write time), Postgres 16 (from `terraform/cloud_sql.tf`), Docker (optional).
- Quickstart: clone, copy existing `.env.example` to `.env`, populate keys, `make dev`. Note that `.env.example` already exists in the repo and lists every key; the README references it rather than re-listing.
- Honest "what won't work without external accounts" section — Stripe webhooks, GCP Secret Manager fetches, BigQuery analytics, OAuth flows.
- Pointer to `terraform/` for the cloud deployment.

**Sub-task during README work:** add `.nvmrc` (or `engines.node` to `web/package.json`) so Node version has a single authoritative source. Without it, README pinning has nothing to anchor to.

## 3. Content Scrub

### 3.1 Delete

- `v0/` — Python prototype, predecessor of the Go rewrite. Tells no story we want.
- `docs/coverage-risk-report.md` — dated 2026-04-04; every risk it called out is now closed (Stripe webhooks 0 → 80%+, coach/educator backend 0 → 83-88%, etc.). Document describes a state that no longer exists.
- `terraform/showHN` — 38KB zip-format Terraform plan currently in working tree as untracked. Plans can contain plaintext post-Secret-Manager values; any `git add -A` would commit it. Delete and tighten gitignore.

### 3.2 Untrack (file stays on disk, removed from git index, gitignored going forward)

- `.claude/settings.local.json` — personal-not-shared by convention; slipped into tracking.
- `.claude/projects/-Users-btc-Projects-src-drill/memory/feedback_fix_all_issues.md` — auto-memory, personal user feedback, not project-relevant.

### 3.3 Update `.gitignore`

Add these lines (fail-closed approach — block all of `.claude/`, all binary terraform artifacts):

```
.claude/
terraform/showHN
terraform/*.zip
terraform/*.bin
```

`.claude/worktrees/` is already gitignored individually; the blanket `.claude/` covers it and everything else under that directory. If anything in `.claude/` ever needs to be tracked, add an explicit `!.claude/<path>` re-include rule.

### 3.4 In-tree Claude trailer scrub (must run before history rewrite)

Three tracked files contain `Co-Authored-By: Claude <noreply@anthropic.com>` as **content** (embedded example commit messages, not commit trailers):

- `docs/superpowers/plans/2026-04-12-http-package-refactor.md`
- `docs/superpowers/plans/2026-04-24-auth-nav-landing-tweaks-plan.md`
- `docs/superpowers/plans/2026-04-26-landing-mobile-responsive-plan.md`

These survive any git history rewrite because they're file content. Edit each to remove the trailer lines from quoted/example commit messages, commit as part of the file-level cleanup phase.

Verification: after edits, this should return empty (excluding this spec file, which describes the patterns):
```
git grep -E "Co-[Aa]uthored-[Bb]y:.*[Cc]laude|🤖 Generated with" \
  -- ':!docs/superpowers/specs/2026-05-05-open-source-prep-design.md'
```

### 3.5 Plans triage

`docs/superpowers/plans/` currently contains 59 files spanning 6 weeks. Triage by criterion: **"would I show this to a competitor?"** Keep architecture/design plans; drop business/strategy plans.

**Definitely drop:**
- `2026-04-13-saas-starter-extraction-design.md` — documents the plan to extract a sellable SaaS starter from this codebase. Discloses what's considered generic vs. domain-specific, plus rename strategy. Useful to competitors.

**Eyeball pass at execution time** for these categories (drop if they discuss go-to-market, pricing, growth tactics, or competitive positioning; keep if they're system-design/refactor/architecture plans):
- Landing-redesign and growth-related plans (e.g., `2026-04-21-landing-redesign-design.md`).
- Show-HN prep plans.
- Marketing or pricing strategy plans.

**Definitely keep:** system-design, ConnectRPC migration, conductor, auth refactor, jobs/River, billing internals architecture, schema design plans.

The implementer should produce a delete list, get a quick eyeball check, then proceed.

### 3.6 Functional Requirements Document reframe

Keep `docs/functional-requirements-2026-04-01.md` as architecture reference. Apply two surgical edits:

**Line 5** — replace:
```
**Purpose**: Input for a third-party system design consultant who will design a production-grade, multi-tenant SaaS architecture from first principles and industry best practice.
```
with:
```
**Purpose**: Functional requirements specification for a production-grade, multi-tenant SaaS architecture.
```

**Line 15** — delete the third sentence (`The consultant is expected to choose technologies, design the architecture, and make all infrastructure decisions.`). The surrounding paragraph stands.

GDPR `shall be designed to satisfy` lines (FR-003, FR-076, FR-089, FR-094): leave as-is. Standard requirements-doc forward-tense. Substance unchanged.

### 3.7 Keep (decisions made — listing here so they aren't revisited)

- `docs/superpowers/plans/` — modulo the triage above.
- `docs/superpowers/specs/` — explicit keep. Sibling of plans/. Contains the very spec being implemented; on-brand for AI-assisted-development repo.
- `docs/bugs/` — niche but harmless. Eyeball pass at execution time to confirm post-mortems read as "we caught and fixed it" rather than "we shipped a money bug."
- `docs/functional-requirements-2026-04-01.md` — kept with reframe per 3.6.
- `CLAUDE.md` — kept with fixes per Section 4.
- `cmd/stripescenario/` — verified safe; refuses non-`sk_test_` keys. Reveals billing scenarios but no credentials.
- `terraform/` — verified safe (all real secrets in GCP Secret Manager; no credentials in `.tf` files).

## 4. CLAUDE.md Fixes

- Remove line 10: `Prototype: ./v0` — points to a deleted directory.
- Add a one-line preamble at the top: `<!-- This file gives Claude Code context for working in this repo. See README.md for human onboarding. -->` (HTML comment, invisible in rendered markdown but searchable.)
- Re-skim the file for any remaining v0/Python-prototype context references and trim if found.

No other rewrites. Existing content is tight, project-specific, and best-practice.

## 5. Git History Rewrite

**Verified state (2026-05-05):**

- Total commits across all refs: **1,452**
- Commits with at least one `Co-Authored-By: Claude` trailer: **1,064 (73%)**
- Total trailer lines (some commits carry 2+): 1,128
- Distinct author identities for Brian (case + email variants): 4
- Commits authored by `Claude <noreply@anthropic.com>` directly: **1**

Tool: `git filter-repo` (not `filter-branch` — slower and deprecated by git's own docs).

### 5.1 Single combined rewrite (replaces previous two-step plan)

Run **one** `git filter-repo` invocation that does both trailer stripping and (if needed) secret removal. One rewrite = one rollback point = one set of new SHAs.

Before rewriting:
1. Tag current HEAD as `pre-oss-rewrite` (rollback point).
2. Run `git log --pretty=format:"%an <%ae>" | sort -u` and document every author identity. Decide which need rewriting — at minimum, the `Claude <noreply@anthropic.com>` author. Optionally normalize the four Brian variants to one canonical form.
3. Run `git reflog expire --expire=now --all && git gc --prune=now --aggressive` to drop unreachable original commits before scanning.

The filter-repo invocation must use:

- `--message-callback` with **case-insensitive** regex to strip trailers. Patterns to match:
  - `(?i)^Co-Authored-By:.*Claude.*$` (covers `Co-Authored-By:` and `Co-authored-by:` variants; covers `Claude` in any model variant).
  - Drop the previously-listed `🤖 Generated with [Claude Code]` pattern — it matches **zero** commits in this repo. Verified: `git log --all --pretty=format:"%B" | grep -c "Generated with"` = 0.
  - Strip trailing blank lines orphaned by trailer removal.
- `--email-callback` to rewrite `noreply@anthropic.com` → the canonical author email. Without this, the 1 commit authored directly by Claude remains visible on GitHub even after message rewrite.
- `--name-callback` to rewrite `Claude` → the canonical author name. Same reason.
- `--replace-text <patterns.txt>` if any secrets surfaced in the secret-scan phase (Section 6).

### 5.2 Verification (replaces the previously broken `--grep` command)

The previous spec's verification used `git log --all --grep=...` which OR-matches commit subjects mentioning `CLAUDE.md` (14 such commits) and produces false positives. Replace with these targeted greps that scan commit bodies and author identities directly:

```bash
# Trailer scan in commit messages — must return 0
git log --all --pretty=format:"%B" \
  | grep -ciE "^Co-(Authored|authored)-[Bb]y:.*claude"

# Trailer scan for Anthropic — must return 0
git log --all --pretty=format:"%B" \
  | grep -ciE "^Co-(Authored|authored)-[Bb]y:.*anthropic"

# Author identity scan — must NOT contain anthropic.com
git log --all --pretty=format:"%an <%ae>" | sort -u

# Generated-with footer scan — must return 0
git log --all --pretty=format:"%B" | grep -c "🤖 Generated with"
```

A passing rewrite returns `0` from the three counted checks and shows no `anthropic.com` identity in the author list.

### 5.3 GitHub-side cleanup

Force-push to the existing remote does NOT clean GitHub-side artifacts:

- Open and closed PRs reference old SHAs; PR review threads remain accessible at `https://github.com/<owner>/<repo>/commit/<old-sha>` for ~30-90 days.
- GitHub Actions workflow run logs reference old SHAs and may surface old commit titles in their pages.
- GitHub's REST/GraphQL API caches branch state.

**Recommended path:** publish to a **fresh public repo** (e.g., create new `Spanda-LLC/sabermatic` or `btc/sabermatic`), push the rewritten history there. Leave the existing `btc/drill` private. This sidesteps every GitHub-side persistence concern and gives a clean repo URL to share on HN.

If you instead force-push to the existing remote: close all open PRs first, delete old workflow runs (manually or via API), and accept that some pre-rewrite SHAs may be retrievable for several weeks.

### 5.4 Post-rewrite cleanup

After the rewrite succeeds and verification passes:

```bash
git reflog expire --expire=now --all
git gc --prune=now --aggressive
```

This drops the original (now-dangling) commits from the local repo so they aren't accidentally pushed.

## 6. Secret Scan Across History

Run before the rewrite (so any findings can be folded into the same `filter-repo --replace-text` invocation in Section 5).

### 6.1 Tools

- **gitleaks**: `gitleaks detect --source . --log-opts="--all" --report-path /tmp/gitleaks.json`
- **trufflehog**: `trufflehog git file://. --json > /tmp/trufflehog.json`

Both walk reachable history. Run `git reflog expire --expire=now --all && git gc --prune=now --aggressive` first to drop unreachable history that the scanners would skip anyway.

### 6.2 Triage

- True positive (real key, ever live): rotate the key first; then surgically remove from history.
- False positive (test fixture, placeholder, base64 of harmless data): document in `.gitleaks.toml` allowlist.

### 6.3 Surgical removal

- For specific strings: `git filter-repo --replace-text <patterns.txt>` (combined into the Section 5 rewrite).
- For whole files: `git filter-repo --invert-paths --path <file>`.

### 6.4 Re-scan after rewrite

Run both scanners again on the rewritten history to confirm clean. Then run the reflog/gc one more time to drop the now-dangling original blobs.

## 7. Final Pre-Publish Gates

Before the public push:

- `make test` green (full pipeline; see Makefile for exact targets — at minimum: buf lint, codegen check, frontend tsc + eslint + vitest, backend `go test -race -count=1`, golangci-lint).
- `go mod verify` green.
- `git grep -i sabermatic` reviewed — confirm no internal references are surprising or revealing.
- `git grep -iE "Co-[Aa]uthored-[Bb]y|🤖 Generated with" -- ':!docs/superpowers/specs/2026-05-05-open-source-prep-design.md'` returns empty (in-tree content scrub from Section 3.4 worked; this spec is excluded because it describes the patterns).
- `terraform/showHN` confirmed deleted; `terraform/tfplan` confirmed untracked.
- `git ls-files | xargs -I{} file {} 2>/dev/null | grep -iE "binary|zip" | grep -v -E "\.png$|\.jpg$|\.jpeg$|\.gif$|\.ico$|\.woff2?$"` — no unexpected binaries committed (allow images and fonts).
- One-pass eyeball read of changed/added files for tone, typos, embarrassing inline comments.

### 7.1 Publish-time GitHub repo configuration

- Description: one-line product pitch.
- Topics: `go`, `connectrpc`, `react`, `postgres`, `system-design`, `system-design-interview`, `ai-agents`. Keep ≤10. Drop low-traffic tags.
- Social preview image set (1280×640). This is build work, not a check; budget time.
- Default branch: `main`.
- Issues: enabled (low cost; lets readers report bugs).
- Discussions: disabled (we're not building a community).
- **Branch protection on `main`**: require PR reviews + status checks; **no force-pushes after publish**. Five-minute setup; prevents the repo from being silently rewritten by a stolen credential.
- **Secret scanning + Dependabot alerts**: enable both (free for public repos).
- **`SECURITY.md`**: one paragraph at root: vulnerability disclosure email or "Open a private security advisory."
- **Optional**: `.github/dependabot.yml` for weekly Go module + npm bumps.

### 7.2 GitHub Actions secret scoping verification

Existing `.github/workflows/deploy.yml` references `WIF_PROVIDER` and `DEPLOYER_SA` secrets. Verify before publish:

- `pull_request` triggers in `ci.yml` do NOT have access to deployment secrets (look at workflow `permissions:` block and event types).
- `pull_request` from forks (when public) is appropriately gated. GitHub's default is to NOT pass secrets to fork PR runs, but verify explicitly.
- Consider adding `permissions: read-all` at workflow root and explicitly scoping write permissions where needed.

## 8. Order of Operations

The history rewrite must come **after** all file-level cleanup, so the rewrite operates on the final state once.

1. **File-level cleanup** as a sequence of normal commits:
   - Add `LICENSE.md`.
   - Write `README.md`.
   - Add `.nvmrc` (or `engines.node` in `web/package.json`).
   - Write `SECURITY.md`.
   - Delete `v0/`.
   - Delete `docs/coverage-risk-report.md`.
   - Delete `terraform/showHN`.
   - Triage `docs/superpowers/plans/` (delete the saas-starter-extraction file at minimum; eyeball-pass the rest).
   - Edit `docs/functional-requirements-2026-04-01.md` per Section 3.6.
   - Scrub in-tree Claude trailers from the 3 plan files (Section 3.4).
   - Untrack `.claude/settings.local.json` and the auto-memory file; update `.gitignore` (Section 3.3).
   - Fix `CLAUDE.md` (Section 4).
2. `make test` green; `go mod verify` green.
3. Tag `pre-oss-rewrite` rollback point.
4. `git reflog expire --expire=now --all && git gc --prune=now --aggressive` (drop unreachable history before scanning).
5. Run gitleaks + trufflehog; triage findings; build `patterns.txt` for `--replace-text` if needed.
6. **Single** `git filter-repo` invocation: `--message-callback` (strip trailers), `--email-callback` + `--name-callback` (rewrite Claude author identity), `--replace-text` (if secrets to remove). One rewrite, one new set of SHAs.
7. Verification per Section 5.2 — all four checks must pass.
8. Re-run gitleaks + trufflehog on rewritten history (Section 6.4).
9. `git reflog expire --expire=now --all && git gc --prune=now --aggressive` again (drop dangling original commits).
10. **Push to public location** — recommended: fresh public repo (Section 5.3); fallback: force-push existing remote with GitHub-side cleanup.
11. Apply GitHub repo configuration (Section 7.1).

**Note on commits made during step 1:** if any cleanup commits get authored via Claude Code with trailers (per the user's global CLAUDE.md, this should already be suppressed), the step 6 rewrite strips them as well. No special handling needed.

## Out of Scope

Explicitly **not** addressed in this work:

- Closing the residual coverage gaps (`rpc/auth.ResetPassword` at 33%, `backend.WaitAndCancelSession` at 0%). Not Show-HN blockers.
- Renaming the repo from `drill` to `sabermatic` — codename `drill` stays; README disambiguates. (If the fresh-public-repo path is taken in Section 5.3, the new repo can be named `sabermatic` directly; that's a publish-time decision, not a content decision.)
- `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, issue templates, PR templates. We're not courting contributors.
- Decoupling the codebase from Sabermatic-specific infra (e.g., making it generic enough that anyone could host it). The license discourages that path; the code shouldn't pretend otherwise.
- Documentation site, API reference, tutorials.
