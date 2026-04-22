# Landing Page Redesign — Design Spec

_2026-04-21_

## Purpose

Replace the current landing page (`web/src/pages/landing/`) with a redesigned version created in Claude Design. The new design keeps the existing brand language (warm neutrals, amber primary, Inter + JetBrains Mono) but restructures sections, adds a live Question Library section, and updates `PublicHeader` to a sticky blurred-backdrop style.

The user-approved design bundle is the source of truth for visuals. Implementation adapts it to the existing React/Vite/Tailwind + shadcn setup.

Reference bundle: `www-sabermatic-dev/project/index.html` (1060 lines) from the Claude Design handoff.

## Scope

### In scope

- Restyle and recompose the existing 10 section components into the 9 sections of the new design.
- Delete `deep-dive.tsx` and `sample-session.tsx` (absorbed elsewhere).
- Add `library.tsx` — new Question Library section backed by live data.
- Rename `annotations.tsx` → `transcript.tsx`.
- Update `PublicHeader` to sticky + blurred-backdrop with conditional anchor nav on the landing route.
- Add CSS tokens missing from `web/src/index.css`: `--border-strong`, `--primary-soft`, Inter stylistic-set font-feature settings on body.
- Backend: new `LandingService` (public, auth-exempt) with `ListFeaturedQuestions` RPC.
- Schema: new columns `questions.is_featured`, `questions.featured_order`.
- Seed: feature 6 selected seeded questions.
- Tests: handler tests, migration round-trip, frontend unit tests for the new section.

### Out of scope

- Generating real cubist illustrations for question cards. A single inline-SVG placeholder serves all 6 cards until the existing Nano Banana pipeline produces `image_url` values. When images land, no frontend change is needed — the card already reads `image_url`.
- A/B rollout framework or feature flag. Direct replacement.
- Tweaks panel from the design bundle (dev artifact).
- Additional landing-page marketing data (testimonials, pricing snippets). `LandingService` is created but only exposes `ListFeaturedQuestions`; future additions are follow-ups.
- "Easy" difficulty styling — the seed has no easy questions. The difficulty-badge class for easy is not wired.
- `/about` route behavior change. It continues to render the same Landing component as `/` (unauth).

## Architecture

### Route-level wiring (unchanged)

- `/` → `ConditionalHome` → authenticated users get `Home` inside `AppLayout`; unauthenticated users get `<PublicHeader /><Landing />`.
- `/about` → always `<PublicHeader /><Landing />`.
- Authenticated users never see the landing at `/`; this remains true.

### Frontend component map

| Existing file | Action | New file |
|---|---|---|
| `hero.tsx` | Restyle + copy. Eyebrow pill (`system design prep, measured`), wordmark-style heading, existing tagline-rotation logic kept, CTA row with primary `Start practicing` (→ `/signup`) and ghost `See sample session` (→ `/sample`), cta-meta line, signature amber progress bar below CTAs. Note: the design bundle's HTML ghost CTA points to `#library` because the prototype is a single-page file; production uses the real `/sample` route. | same |
| `scoring.tsx` | Restyle to `stat-panel` layout: header row with session label and rubric version, 5 thin amber fill bars for dimensions, thick amber overall bar with larger numeric. | same |
| `strengths-gaps.tsx` | Restyle to two-column: header text left, grouped annotations right (Strengths / Gaps / Missed). Uses `--strength`, `--gap`, `--missed` tokens. The `--note` token stays defined in `index.css` but isn't instantiated on the landing. | same |
| `annotations.tsx` | Rename + restyle as annotated transcript: interviewer/candidate bubbles with inline annotation callouts below each. | → `transcript.tsx` |
| `deep-dive.tsx` | Delete. | — |
| `voice-pipeline.tsx` | Restyle to 3-column Conversation: You / Interviewer / Afterward, each with waveform or annotation chips. | same |
| `coaching.tsx` | Restyle to `trend-card`: big-number overall score, delta, inline SVG sparkline, focus pill, narrative with amber left border. | same |
| `sample-session.tsx` | Delete (hero ghost CTA covers the sample entry point). | — |
| — | New Question Library section: header + signature amber progress bar + 6-card grid, live data via `ListFeaturedQuestions`. | `library.tsx` |
| `credits.tsx` | Restyle from stacked-list to 3-column bordered `stack-grid`. Data unchanged — the 9 roles already match. | same |
| `cta-repeat.tsx` | Restyle to `final-cta`: large hold-line (current last tagline), primary CTA, meta tag. | same |
| `index.tsx` | Update section imports and composition to the ordered list below. | same |

**`index.tsx` composition order** (Hero first, then 8 labeled sections matching the design, then the final CTA):

1. `<Hero />`
2. `<Scoring />` — `id="scoring"`
3. `<StrengthsGaps />` — `id="strengths"`
4. `<Transcript />` — `id="transcript"` (renamed from `annotations.tsx`)
5. `<VoicePipeline />` — `id="pipeline"`
6. `<Coaching />` — `id="coach"`
7. `<Library />` — `id="library"`
8. `<Credits />` — `id="stack"`
9. `<CTARepeat />` — the final CTA section

Each component's outermost `<section>` must set its corresponding `id` so the `PublicHeader` anchor nav (`#library`, `#pipeline`, `#stack`) resolves.

### Styling approach

Inline Tailwind classes with existing CSS custom properties wherever natural. Three additions to `web/src/index.css`:

1. `--border-strong` — stronger border token: `hsl(24 6% 80%)` in `:root`, `hsl(20 8% 30%)` in `.dark`. Mapped in `@theme inline` as `--color-border-strong: hsl(var(--border-strong))` so Tailwind exposes it as `border-border-strong`. Matches the design's light/dark pair.
2. `--primary-soft` — primary at reduced opacity for CTA/bubble backgrounds. `hsl(32 95% 44% / .1)` in `:root`, `hsl(32 95% 44% / .14)` in `.dark` (dark background needs higher opacity to read the same). Mapped in `@theme inline` as `--color-primary-soft` for `bg-primary-soft`.
3. Body `font-feature-settings: "ss01", "cv11"` — Inter stylistic alternates. Add to the existing `body` rule at `index.css:67-71`, not a new rule, so the features inherit to inputs and buttons.

Everything else maps to existing tokens: `bg`, `fg`, `muted`, `primary`, `card`, `border`, `strength`, `gap`, `missed`. (`--note` exists in `index.css` and the design defines a `.note` annotation class, but no landing content instantiates it — left as dormant.)

### Header update

`PublicHeader` updates to match the design:

- Sticky (`sticky top-0 z-40`), backdrop-blur (`backdrop-blur-md`), semi-transparent background.
- Brand left, right-side nav.
- Anchor nav rendered only when `pathname === "/"` or `pathname === "/about"` — three anchors: `#library`, `#pipeline`, `#stack`.
- Log in and Sign up CTAs always present (right edge).
- No avatar chip. The design showed one but authenticated users don't see this header in practice (`ConditionalHome` routes them to `AppLayout`).

### Animations

Retained from the design, all gated by `prefers-reduced-motion`:

- Tagline rotation (existing logic in `Hero`, reused).
- `useScrollReveal` on each section (existing hook).
- Score bar width fill on reveal.
- Waveform bar heights (CSS-animated).
- Progress bar shimmer sweep.
- Inline SVG sparkline is static (no animation).

### Backend

**Migrations**

- `013_featured_questions.up.sql` —
  ```sql
  ALTER TABLE questions
    ADD COLUMN is_featured BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN featured_order INTEGER;
  ALTER TABLE questions
    ADD CONSTRAINT questions_featured_order_required
    CHECK (NOT is_featured OR featured_order IS NOT NULL);
  CREATE UNIQUE INDEX questions_featured_order_unique
    ON questions (featured_order) WHERE is_featured;
  ```
  The CHECK prevents `is_featured = true` with a NULL `featured_order` (which would silently break `ORDER BY`); the partial unique index prevents duplicate orderings among featured rows without constraining NULLs on non-featured rows.
- `013_featured_questions.down.sql` — drop the index, drop the CHECK constraint, drop both columns.
- `014_feature_seed_questions.up.sql` — idempotent: one `UPDATE questions SET is_featured = true, featured_order = N WHERE source = 'seed' AND title = '…'` per featured title. Key is `(source, title)` since seed IDs are UUIDs generated at seed time.
- `014_feature_seed_questions.down.sql` — `UPDATE questions SET is_featured = false, featured_order = NULL WHERE source = 'seed' AND title IN (…)` for the same six titles. (Migrations roll back in reverse order, so `014.down` runs before `013.down` drops the columns.)

**Featured six (in order):**

| order | title |
|---|---|
| 1 | Video Streaming |
| 2 | News Feed |
| 3 | Ride Sharing |
| 4 | Chat System |
| 5 | Search Autocomplete |
| 6 | Social Graph |

**Proto — new service**

`pb/drill/v1/landing.proto`:

```proto
syntax = "proto3";
package drill.v1;

option go_package = "github.com/btc/drill/internal/pb/drill/v1;drillv1";

import "drill/v1/question.proto";

service LandingService {
  rpc ListFeaturedQuestions(ListFeaturedQuestionsRequest) returns (ListFeaturedQuestionsResponse);
}

message ListFeaturedQuestionsRequest {}

message ListFeaturedQuestionsResponse {
  repeated Question questions = 1;  // ordered by featured_order
  int32 total_count = 2;            // full library size, for the "N questions" header
}
```

**Why a new service (not adding to `QuestionService` or `SampleService`)**

- `QuestionService` is authed. Making a single method public requires a procedure-aware allowlist inside the auth interceptor, which scatters auth posture across code. Uniform per-service auth matches existing convention.
- `SampleService` is scoped to sample-session data (`GetSample*`). A featured-question list doesn't fit that naming.
- `LandingService` is a clean home for future public marketing-surface data. Zero auth-interceptor complexity.

**Backend wiring**

- `internal/rpc/landing/server.go` — `Server` struct holding `*backend.Backend`, implements `LandingServiceHandler`, single `ListFeaturedQuestions` method. Owns the db-row → proto conversion for `ListFeaturedQuestions` rows (small local converter, ~10 lines; no shared helper). The existing `questionToProto` in `internal/rpc/question/server.go` binds to `db.ListQuestionsForUserRow`, a different sqlc-generated type, so cross-package sharing would require a generic helper that's not worth the indirection for one reuse.
- `internal/backend/question.go` — new method `ListFeaturedQuestions(ctx context.Context) (rows []db.ListFeaturedQuestionsRow, total int, err error)` calling two sqlc queries: one for featured rows, one for total count. Returns `int` (not `int32`); cast to `int32` happens at the proto layer in the landing server.
- Registration in `internal/rpc/register.go`: `mux.Handle(drillv1connect.NewLandingServiceHandler(landing.NewServer(b), publicOpts))` — same `publicOpts` already used for `AuthService` and `SampleService` at `register.go:36-39`. `publicOpts` omits the auth interceptor, so requests reach the handler without a session.

**Future-proofing note — `DIFFICULTY_EASY`**

The proto `Difficulty` enum (`pb/drill/v1/question.proto:9-13`) only defines `UNSPECIFIED`, `MEDIUM`, `HARD`. The seed has no easy questions, so the featured six won't hit this. If an easy seed is added later, `difficultyToProto` (`internal/rpc/question/server.go:218-227`) must gain a case or easy questions will serialize as `UNSPECIFIED`. Out of scope here, but flagged.

**SQL queries** (`sql/queries/questions.sql`):

```sql
-- name: ListFeaturedQuestions :many
SELECT id, title, prompt, difficulty, tags, hints, source, image_url, created_at
FROM questions
WHERE is_featured = true
ORDER BY featured_order;

-- name: CountSeedQuestions :one
SELECT COUNT(*) FROM questions
WHERE source = 'seed' AND user_id IS NULL;
```

The total count is scoped to seed questions so the landing's "N questions" headline reflects the public, curated library size — not user-generated custom or coach-generated questions, which would leak private account activity into public marketing copy and drift unpredictably.

### Frontend RPC wiring

- `buf generate` regenerates TS clients under `web/src/pb/drill/v1/`.
- `library.tsx` uses `@connectrpc/connect-query`'s `useQuery(listFeaturedQuestions, {})` — same pattern as existing ConnectRPC hooks in the app (precedent: `web/src/api/sample-queries.ts`).
- Loading: 6 skeleton cards matching the grid layout.
- Error: Library section hides itself. Don't block the rest of the page; the marketing page is viewable without it.
- Empty result: same — section hides.
- Per-card: `image_url` when present, otherwise the shared inline-SVG cubist placeholder. **Initial state at launch:** all six cards will use the placeholder. The Nano Banana pipeline backfills `questions.image_url` via `ListQuestionsWithoutImages` / `SetQuestionImageURL` (`sql/queries/questions.sql:28-34`), and featured rows aren't prioritized in that backfill, so real images will appear progressively.
- Library section h2 format: `{total_count} questions. Real prompts. Real rubrics.` — `total_count` is injected from the RPC response into the single headline string, keeping the period-terminated tagline style of the prototype. If `total_count` is 0 (shouldn't happen with seed present) the section hides entirely.

## Error handling and edge cases

- **Featured question deleted** — `is_featured` and `featured_order` removed with the row. `featured_order` gaps are fine; clients sort by the column.
- **Fewer than 6 featured** — UI renders whatever comes back. Headline count is `total_count` from the RPC, not the featured length, so the "N questions" figure stays truthful.
- **More than 6 featured** — UI renders all returned cards; grid wraps.
- **`image_url` missing** — UI renders the shared placeholder SVG.
- **Landing fetch fails** — Library section disappears. Other sections unaffected.
- **`prefers-reduced-motion`** — all animations disabled (tagline rotation, scroll reveals, waveform bars, progress-bar shimmer, score bar fill).

## Testing

### Tests to write

- `internal/rpc/landing/server_test.go` — happy path (seeded DB with 6 featured rows returns them in `featured_order`; `total_count` equals the count of seed rows with `user_id IS NULL`); empty path (no featured rows → empty `questions` slice, `total_count` still equals the seed count); public-access path (end-to-end via `httptest.Server` with the full `Register()` wiring, request sent without session cookie → 200 with data). Follow the pattern in `internal/rpc/sample/server_test.go` which exercises the same public-registration path.
- `internal/backend/question_test.go` — unit test for the new `ListFeaturedQuestions` backend method (rows + count in one call).
- Migration round-trip — run `013` and `014` up then down locally against a dev DB; verify columns, index, CHECK constraint, and seed updates are symmetric. Covered by the existing migration-up test harness if one exists; otherwise manual verification noted in the PR.
- `web/src/pages/landing/library.test.tsx` — vitest: renders the 6 cards from mocked hook data in `featured_order`, renders `total_count` in the h2 headline, renders skeletons during loading, renders nothing on error or empty.

### Tests to verify/update

- Existing `internal/rpc/question/server_test.go` — unchanged (`QuestionService` untouched).
- `make test` must pass end-to-end (buf lint, codegen check, tsc -b, eslint, vitest, go test -race).

## Acceptance criteria

- `/` (unauth) renders the new landing page with the 9 sections in order, visually matching the prototype.
- `/about` renders the same page.
- `PublicHeader` is sticky, blurred backdrop, anchor nav on landing routes.
- Library section shows 6 real featured questions, ordered by `featured_order`, with the total library count in the header read from the RPC.
- Authenticated users at `/` still bypass the landing via `ConditionalHome`.
- `make test` passes.
- Dev server loaded in a browser: scroll reveal, tagline rotation, waveforms, sparkline, score-bar fill are visible; `prefers-reduced-motion` path verified via DevTools emulation.

## Boy-scout items in this scope

- `CLAUDE.md` nil-slice rule tightened to REST-only. ConnectRPC does not need nil→`[]T{}` coercion: both binary and JSON protobuf codecs treat nil and empty `repeated` fields identically, and the TS client always decodes to `[]`. Updated in the same commit as this spec.

## Known tech debt (not in this scope)

- Existing ConnectRPC handlers still contain explicit nil→`[]T{}` coercions that the revised CLAUDE.md makes unnecessary (e.g., `internal/rpc/question/server.go:150-162`, `internal/backend/question.go`). These are inert but stale. Clean up in a follow-up when touching those files.
