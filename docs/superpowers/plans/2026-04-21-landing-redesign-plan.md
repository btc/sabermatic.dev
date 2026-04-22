# Landing Page Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the current landing page with the Claude Design handoff, adapted to the existing React/Vite/Tailwind + shadcn setup; add a public `LandingService` with `ListFeaturedQuestions` + a curated featured-question seed so the new Question Library section shows real data.

**Architecture:** In-place restyles of existing `pages/landing/*.tsx` (one rename, two deletes, one add). New public ConnectRPC `LandingService` (registered with `publicOpts`, auth-exempt). Two forward migrations — `013` adds `is_featured` / `featured_order` columns with a CHECK + partial unique index; `014` flags the six curated titles by `(source='seed', title)`. Existing components keep their live-sample-data hooks (`useSampleEvaluation`, `useSampleCoach`, etc.) — this is a presentation change, not a data change.

**Tech Stack:** Go + ConnectRPC + pgx + sqlc (backend), Postgres (migrations), Buf for proto codegen, React + Vite + Tailwind + shadcn + `@connectrpc/connect-query` (frontend), vitest + testify (tests).

**Reference:** Spec at `docs/superpowers/specs/2026-04-21-landing-redesign-design.md`. Design bundle at `/tmp/design-bundle/www-sabermatic-dev/project/index.html` (1060-line HTML prototype — visual reference only; never commit it).

---

## Task 1: Add CSS tokens to `index.css`

**Files:**
- Modify: `web/src/index.css`

- [ ] **Step 1: Add `--border-strong` and `--primary-soft` tokens to `:root` block**

Open `web/src/index.css`. Inside the `:root` block (currently ending at line 36), add the two tokens immediately before the closing `}`:

```css
  --border-strong: hsl(24 6% 80%);
  --primary-soft: hsl(32 95% 44% / .1);
```

- [ ] **Step 2: Add dark overrides for both tokens in `.dark` block**

Inside the `.dark` block (currently ending at line 65), add before the closing `}`:

```css
  --border-strong: hsl(20 8% 30%);
  --primary-soft: hsl(32 95% 44% / .14);
```

- [ ] **Step 3: Add Inter font-feature-settings to body rule**

Replace the existing `body` rule (lines 67-71) with:

```css
body {
  font-family: "Inter", system-ui, -apple-system, sans-serif;
  background-color: var(--background);
  color: var(--foreground);
  font-feature-settings: "ss01", "cv11";
}
```

- [ ] **Step 4: Map new tokens in `@theme inline` block**

Inside the `@theme inline` block (currently ending at line 103), add before the closing `}`:

```css
  --color-border-strong: var(--border-strong);
  --color-primary-soft: var(--primary-soft);
```

- [ ] **Step 5: Run typecheck + build to verify Tailwind still compiles**

```bash
cd web && npx tsc -b
cd web && npm run build
```

Expected: no errors. Tailwind's JIT will pick up the new tokens and expose `border-border-strong` and `bg-primary-soft` classes.

- [ ] **Step 6: Commit**

```bash
git add web/src/index.css
git commit -m "style: add --border-strong, --primary-soft tokens, Inter ss01/cv11 features"
```

---

## Task 2: Migration 013 — add `is_featured` and `featured_order` columns

**Files:**
- Create: `sql/migrations/013_featured_questions.up.sql`
- Create: `sql/migrations/013_featured_questions.down.sql`

- [ ] **Step 1: Write the up migration**

Create `sql/migrations/013_featured_questions.up.sql`:

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

- [ ] **Step 2: Write the down migration**

Create `sql/migrations/013_featured_questions.down.sql`:

```sql
DROP INDEX IF EXISTS questions_featured_order_unique;
ALTER TABLE questions DROP CONSTRAINT IF EXISTS questions_featured_order_required;
ALTER TABLE questions DROP COLUMN IF EXISTS featured_order;
ALTER TABLE questions DROP COLUMN IF EXISTS is_featured;
```

- [ ] **Step 3: Apply migration locally against dev DB**

```bash
psql "$DATABASE_URL" -f sql/migrations/013_featured_questions.up.sql
```

Expected: no error. Verify columns present:

```bash
psql "$DATABASE_URL" -c "\d questions" | grep -E "is_featured|featured_order"
```

Expected: two rows, `is_featured boolean NOT NULL DEFAULT false` and `featured_order integer`.

- [ ] **Step 4: Verify down migration round-trips cleanly**

```bash
psql "$DATABASE_URL" -f sql/migrations/013_featured_questions.down.sql
psql "$DATABASE_URL" -c "\d questions" | grep -E "is_featured|featured_order" && echo FAIL || echo OK
psql "$DATABASE_URL" -f sql/migrations/013_featured_questions.up.sql
```

Expected: `OK` — columns are gone after down, reinstated after re-up.

- [ ] **Step 5: Commit**

```bash
git add sql/migrations/013_featured_questions.up.sql sql/migrations/013_featured_questions.down.sql
git commit -m "db: add questions.is_featured, questions.featured_order"
```

---

## Task 3: Migration 014 — feature the curated six seed questions

**Files:**
- Create: `sql/migrations/014_feature_seed_questions.up.sql`
- Create: `sql/migrations/014_feature_seed_questions.down.sql`

- [ ] **Step 1: Write the up migration**

Create `sql/migrations/014_feature_seed_questions.up.sql`:

```sql
UPDATE questions SET is_featured = true, featured_order = 1 WHERE source = 'seed' AND user_id IS NULL AND title = 'Video Streaming';
UPDATE questions SET is_featured = true, featured_order = 2 WHERE source = 'seed' AND user_id IS NULL AND title = 'News Feed';
UPDATE questions SET is_featured = true, featured_order = 3 WHERE source = 'seed' AND user_id IS NULL AND title = 'Ride Sharing';
UPDATE questions SET is_featured = true, featured_order = 4 WHERE source = 'seed' AND user_id IS NULL AND title = 'Chat System';
UPDATE questions SET is_featured = true, featured_order = 5 WHERE source = 'seed' AND user_id IS NULL AND title = 'Search Autocomplete';
UPDATE questions SET is_featured = true, featured_order = 6 WHERE source = 'seed' AND user_id IS NULL AND title = 'Social Graph';
```

Each statement is idempotent — re-running sets the same state. The `(source, user_id, title)` triple is unique among seed rows.

- [ ] **Step 2: Write the down migration**

Create `sql/migrations/014_feature_seed_questions.down.sql`:

```sql
UPDATE questions SET is_featured = false, featured_order = NULL
WHERE source = 'seed' AND user_id IS NULL AND title IN (
    'Video Streaming',
    'News Feed',
    'Ride Sharing',
    'Chat System',
    'Search Autocomplete',
    'Social Graph'
);
```

- [ ] **Step 3: Apply and verify**

```bash
psql "$DATABASE_URL" -f sql/migrations/014_feature_seed_questions.up.sql
psql "$DATABASE_URL" -c "SELECT title, featured_order FROM questions WHERE is_featured ORDER BY featured_order;"
```

Expected output (exactly):

```
        title          | featured_order
-----------------------+----------------
 Video Streaming       |              1
 News Feed             |              2
 Ride Sharing          |              3
 Chat System           |              4
 Search Autocomplete   |              5
 Social Graph          |              6
```

- [ ] **Step 4: Verify down reverts cleanly**

```bash
psql "$DATABASE_URL" -f sql/migrations/014_feature_seed_questions.down.sql
psql "$DATABASE_URL" -c "SELECT count(*) FROM questions WHERE is_featured;"
```

Expected: `0`. Then re-apply up:

```bash
psql "$DATABASE_URL" -f sql/migrations/014_feature_seed_questions.up.sql
```

- [ ] **Step 5: Commit**

```bash
git add sql/migrations/014_feature_seed_questions.up.sql sql/migrations/014_feature_seed_questions.down.sql
git commit -m "db: feature curated six seed questions for landing library"
```

---

## Task 4: Add sqlc queries `ListFeaturedQuestions` and `CountSeedQuestions`

**Files:**
- Modify: `sql/queries/questions.sql` (append)
- Regenerate: `internal/db/` (sqlc output)

- [ ] **Step 1: Append new queries to `sql/queries/questions.sql`**

Add to the end of `sql/queries/questions.sql`:

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

- [ ] **Step 2: Regenerate sqlc bindings**

```bash
sqlc generate
```

Expected: no errors. `internal/db/questions.sql.go` now contains `ListFeaturedQuestions` and `CountSeedQuestions` methods plus a `ListFeaturedQuestionsRow` struct.

- [ ] **Step 3: Verify generated code compiles**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add sql/queries/questions.sql internal/db/
git commit -m "sqlc: add ListFeaturedQuestions, CountSeedQuestions"
```

---

## Task 5: Add `landing.proto` and run `buf generate`

**Files:**
- Create: `pb/drill/v1/landing.proto`
- Regenerate: `internal/pb/drill/v1/landing*.go`, `internal/pb/drill/v1/drillv1connect/landing.connect.go`, `web/src/pb/drill/v1/landing*`

- [ ] **Step 1: Create the proto**

Create `pb/drill/v1/landing.proto`:

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
  repeated Question questions = 1;
  int32 total_count = 2;
}
```

- [ ] **Step 2: Run buf lint, then buf generate**

```bash
buf lint
buf generate
```

Expected: no lint errors, no generate errors. Check generated files exist:

```bash
ls internal/pb/drill/v1/landing_pb.go internal/pb/drill/v1/drillv1connect/landing.connect.go
ls web/src/pb/drill/v1/landing_pb.d.ts web/src/pb/drill/v1/landing-LandingService_connectquery.d.ts
```

All four must be present.

- [ ] **Step 3: Verify everything compiles**

```bash
go build ./...
cd web && npx tsc -b
```

Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add pb/drill/v1/landing.proto internal/pb/drill/v1/ web/src/pb/drill/v1/
git commit -m "proto: add LandingService.ListFeaturedQuestions"
```

---

## Task 6: Backend method `ListFeaturedQuestions` on `*backend.Backend`

**Files:**
- Modify: `internal/backend/question.go` (append method)
- Test: `internal/backend/question_test.go` (create or extend)

- [ ] **Step 1: Write the failing test**

Create `internal/backend/question_test.go` (if it already exists, append `TestListFeaturedQuestions` to it — `package backend_test`):

```go
package backend_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/testutil"
)

func TestListFeaturedQuestions(t *testing.T) {
	t.Parallel()

	b := testutil.NewBackend(t, nil)
	// Migrations 013 and 014 are part of the embedded migration set
	// (sql/migrations/embed.go), so the six curated rows are already
	// featured by the time this test runs against the shared test DB.

	rows, total, err := b.ListFeaturedQuestions(context.Background())
	require.NoError(t, err)

	require.Len(t, rows, 6)
	require.Equal(t, []string{
		"Video Streaming",
		"News Feed",
		"Ride Sharing",
		"Chat System",
		"Search Autocomplete",
		"Social Graph",
	}, titles(rows))

	// total_count is scoped to seed questions only; 008_seed_questions seeds 18.
	require.Equal(t, 18, total)
}

func titles(rows []db.ListFeaturedQuestionsRow) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Title)
	}
	return out
}
```

- [ ] **Step 2: Run test — expect compile failure**

```bash
go test ./internal/backend/ -run TestListFeaturedQuestions -count=1
```

Expected: compile error — `b.ListFeaturedQuestions` is undefined.

- [ ] **Step 3: Add the backend method**

Append to `internal/backend/question.go`:

```go
// ListFeaturedQuestions returns the curated list of featured questions for
// the public landing page along with the total count of seed questions.
//
// The featured list is ordered by featured_order. The total_count is scoped
// to seed questions (source='seed' AND user_id IS NULL) so the public
// landing's "N questions" headline reflects the curated library, not private
// user-generated or coach-generated questions.
func (b *Backend) ListFeaturedQuestions(ctx context.Context) (_ []db.ListFeaturedQuestionsRow, _ int, err error) {
	ctx, span := tracer.Start(ctx, "Backend.ListFeaturedQuestions")
	defer func() { drilotel.End(span, err) }()

	queries := db.New(b.pool)

	rows, err := queries.ListFeaturedQuestions(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("list featured questions: %w", err)
	}

	total, err := queries.CountSeedQuestions(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("count seed questions: %w", err)
	}

	return rows, int(total), nil
}
```

- [ ] **Step 4: Run test — expect pass**

```bash
go test ./internal/backend/ -run TestListFeaturedQuestions -count=1 -race
```

Expected: PASS. Migrations are `//go:embed`ed via `sql/migrations/embed.go` and applied by `testutil.NewBackend`, so 013 and 014 are picked up automatically from the files created in Tasks 2–3.

- [ ] **Step 5: Commit**

```bash
git add internal/backend/question.go internal/backend/question_test.go
git commit -m "backend: add ListFeaturedQuestions (rows + seed-scoped total)"
```

---

## Task 7: `LandingService` handler at `internal/rpc/landing/server.go`

**Files:**
- Create: `internal/rpc/landing/server.go`
- Create: `internal/rpc/landing/server_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/rpc/landing/server_test.go`:

```go
package landing_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	drillv1 "github.com/btc/drill/internal/pb/drill/v1"
	"github.com/btc/drill/internal/pb/drill/v1/drillv1connect"
	landingsvc "github.com/btc/drill/internal/rpc/landing"
	"github.com/btc/drill/internal/testutil"
)

// setup starts a LandingService over httptest.Server with no interceptors —
// the real production posture for a public RPC (see register.go's publicOpts).
func setup(t *testing.T) drillv1connect.LandingServiceClient {
	t.Helper()
	b := testutil.NewBackend(t, nil)

	mux := http.NewServeMux()
	mux.Handle(drillv1connect.NewLandingServiceHandler(landingsvc.NewServer(b)))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return drillv1connect.NewLandingServiceClient(srv.Client(), srv.URL)
}

func TestListFeaturedQuestions_HappyPath(t *testing.T) {
	t.Parallel()
	client := setup(t)

	resp, err := client.ListFeaturedQuestions(context.Background(),
		connect.NewRequest(&drillv1.ListFeaturedQuestionsRequest{}))
	require.NoError(t, err)

	require.Len(t, resp.Msg.Questions, 6)
	require.Equal(t, "Video Streaming", resp.Msg.Questions[0].Title)
	require.Equal(t, "News Feed", resp.Msg.Questions[1].Title)
	require.Equal(t, "Ride Sharing", resp.Msg.Questions[2].Title)
	require.Equal(t, "Chat System", resp.Msg.Questions[3].Title)
	require.Equal(t, "Search Autocomplete", resp.Msg.Questions[4].Title)
	require.Equal(t, "Social Graph", resp.Msg.Questions[5].Title)

	require.Equal(t, int32(18), resp.Msg.TotalCount)
}

func TestListFeaturedQuestions_PublicAccess(t *testing.T) {
	t.Parallel()
	client := setup(t)

	// No session cookie on the underlying transport — public RPC must
	// still respond 200 with data.
	resp, err := client.ListFeaturedQuestions(context.Background(),
		connect.NewRequest(&drillv1.ListFeaturedQuestionsRequest{}))
	require.NoError(t, err)
	require.NotNil(t, resp.Msg)
	require.NotEmpty(t, resp.Msg.Questions)
}
```

Note: empty-featured-set test is covered by the handler-level code path (nothing featured ⇒ empty questions, total still equals seed count). If the existing test DB infra can roll back 014 for a single test, add a third test; otherwise skip — happy path plus public access suffices.

- [ ] **Step 2: Run test — expect compile failure**

```bash
go test ./internal/rpc/landing/ -count=1
```

Expected: compile error — package `internal/rpc/landing` doesn't exist yet.

- [ ] **Step 3: Write the handler**

Create `internal/rpc/landing/server.go`:

```go
// Package landing implements the public LandingService ConnectRPC handler.
// This service is registered with publicOpts (no auth interceptor) so the
// landing page can fetch featured-question data without a session.
package landing

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/btc/drill/internal/backend"
	"github.com/btc/drill/internal/db"
	drillv1 "github.com/btc/drill/internal/pb/drill/v1"
	"github.com/btc/drill/internal/pb/drill/v1/drillv1connect"
)

// Server implements LandingServiceHandler. Public; does not require auth.
type Server struct {
	b *backend.Backend
}

var _ drillv1connect.LandingServiceHandler = (*Server)(nil)

// NewServer creates a new LandingService handler.
func NewServer(b *backend.Backend) *Server {
	return &Server{b: b}
}

// ListFeaturedQuestions returns the curated featured questions and total
// seed-question count for the public landing page.
func (s *Server) ListFeaturedQuestions(
	ctx context.Context,
	_ *connect.Request[drillv1.ListFeaturedQuestionsRequest],
) (*connect.Response[drillv1.ListFeaturedQuestionsResponse], error) {
	rows, total, err := s.b.ListFeaturedQuestions(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "landing: list featured questions", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("list featured questions failed"))
	}

	questions := make([]*drillv1.Question, 0, len(rows))
	for _, row := range rows {
		questions = append(questions, rowToProto(row))
	}

	return connect.NewResponse(&drillv1.ListFeaturedQuestionsResponse{
		Questions:  questions,
		TotalCount: int32(total),
	}), nil
}

// rowToProto converts a ListFeaturedQuestionsRow to a proto Question.
// Local to this package because the existing questionToProto in the question
// service binds to db.ListQuestionsForUserRow, a distinct sqlc-generated type.
func rowToProto(row db.ListFeaturedQuestionsRow) *drillv1.Question {
	q := &drillv1.Question{
		Id:         row.ID.String(),
		Title:      row.Title,
		Prompt:     row.Prompt,
		Difficulty: difficultyToProto(row.Difficulty),
		Tags:       row.Tags,
		Source:     sourceToProto(row.Source),
		CreateTime: timestamppb.New(row.CreatedAt),
	}
	if row.Hints.Valid {
		q.Hints = &row.Hints.String
	}
	if row.ImageUrl.Valid {
		q.ImageUrl = &row.ImageUrl.String
	}
	// Featured seed questions always have user_id NULL (see migration 014),
	// so UserId is left unset.
	return q
}

func difficultyToProto(s string) drillv1.Difficulty {
	switch s {
	case "medium":
		return drillv1.Difficulty_DIFFICULTY_MEDIUM
	case "hard":
		return drillv1.Difficulty_DIFFICULTY_HARD
	default:
		return drillv1.Difficulty_DIFFICULTY_UNSPECIFIED
	}
}

func sourceToProto(s string) drillv1.QuestionSource {
	switch s {
	case "seed":
		return drillv1.QuestionSource_QUESTION_SOURCE_SEED
	case "custom":
		return drillv1.QuestionSource_QUESTION_SOURCE_CUSTOM
	case "coach_generated":
		return drillv1.QuestionSource_QUESTION_SOURCE_COACH_GENERATED
	default:
		return drillv1.QuestionSource_QUESTION_SOURCE_UNSPECIFIED
	}
}
```

- [ ] **Step 4: Run test — expect pass**

```bash
go test ./internal/rpc/landing/ -count=1 -race
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/rpc/landing/
git commit -m "rpc: add LandingService with public ListFeaturedQuestions"
```

---

## Task 8: Register `LandingService` in `register.go`

**Files:**
- Modify: `internal/rpc/register.go`

- [ ] **Step 1: Add landing import and registration**

Open `internal/rpc/register.go`. Add the import next to the other `rpc/*` imports:

```go
	"github.com/btc/drill/internal/rpc/landing"
```

Find the block registering public services (around line 38):

```go
	mux.Handle(drillv1connect.NewAuthServiceHandler(authsvc.NewServer(b), publicOpts))
	mux.Handle(drillv1connect.NewSampleServiceHandler(samplerpc.NewServer(b.SampleService), publicOpts))
```

Append a third line in the same public-services block:

```go
	mux.Handle(drillv1connect.NewLandingServiceHandler(landing.NewServer(b), publicOpts))
```

- [ ] **Step 2: Run all backend tests**

```bash
go build ./...
go test ./internal/rpc/... -race -count=1
```

Expected: all tests pass; build clean.

- [ ] **Step 3: Commit**

```bash
git add internal/rpc/register.go
git commit -m "rpc: register public LandingService"
```

---

## Task 9: Restyle `hero.tsx`

**Files:**
- Modify: `web/src/pages/landing/hero.tsx`

Target visual reference: `/tmp/design-bundle/www-sabermatic-dev/project/index.html:521-542`.

- [ ] **Step 1: Rewrite the component body**

Replace the entire contents of `web/src/pages/landing/hero.tsx` with:

```tsx
import { useEffect, useState } from "react";
import { Link } from "react-router-dom";

import { useScrollReveal } from "@/hooks/use-scroll-reveal";

const TAGLINES = [
  "system design, measured.",
  "measure what matters.",
  "practice with precision.",
  "the science of system design prep.",
  "data-driven system design prep.",
];

const CYCLE_MS = 3000;
const FADE_MS = 300;

export function Hero() {
  const [index, setIndex] = useState(0);
  const [visible, setVisible] = useState(true);
  const isLast = index === TAGLINES.length - 1;
  const { ref: barRef, isVisible: barVisible } = useScrollReveal<HTMLDivElement>();

  useEffect(() => {
    if (isLast) return;
    if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) return;

    let fadeTimeout: ReturnType<typeof setTimeout>;
    const timer = setInterval(() => {
      setVisible(false);
      fadeTimeout = setTimeout(() => {
        setIndex((i) => Math.min(i + 1, TAGLINES.length - 1));
        setVisible(true);
      }, FADE_MS);
    }, CYCLE_MS);

    return () => {
      clearInterval(timer);
      clearTimeout(fadeTimeout);
    };
  }, [isLast]);

  return (
    <section aria-labelledby="hero-heading" className="px-10 pt-36 pb-28 text-center sm:px-6">
      <div className="mx-auto max-w-[1120px]">
        <div className="mb-8 inline-flex items-center gap-2.5 rounded-full border border-border bg-card px-3 py-1.5 font-mono text-[11px] uppercase tracking-[0.12em] text-muted-foreground">
          <span className="h-1.5 w-1.5 rounded-full bg-primary" aria-hidden />
          system design prep, measured
        </div>

        <h1
          id="hero-heading"
          className="mb-9 text-[clamp(56px,10vw,140px)] font-extrabold leading-[0.9] tracking-[-0.055em]"
        >
          sabermatic<span className="font-bold tracking-[-0.03em] opacity-40">[.DEV]</span>
        </h1>

        <p
          aria-live="polite"
          className={`h-8 text-[clamp(17px,1.6vw,22px)] text-muted-foreground transition-opacity duration-300 motion-reduce:transition-none ${
            visible ? "opacity-100" : "opacity-0"
          }`}
        >
          {TAGLINES[index]}
        </p>

        <div className="mt-14 flex flex-wrap justify-center gap-3">
          <Link
            to="/signup"
            className="inline-flex items-center gap-2 rounded-lg bg-foreground px-[22px] py-[13px] text-sm font-medium text-background transition-transform hover:-translate-y-px"
          >
            Start practicing
            <span className="rounded bg-foreground/20 px-1.5 py-0.5 font-mono text-[10px]">↵</span>
          </Link>
          <Link
            to="/sample"
            className="inline-flex items-center rounded-lg border border-border-strong bg-card px-[22px] py-[13px] text-sm font-medium hover:bg-muted"
          >
            See sample session
          </Link>
        </div>

        <p className="mt-4 font-mono text-xs uppercase text-muted-foreground">3 sessions free · no card</p>

        <div ref={barRef} className="mx-auto mt-[88px] max-w-[960px]">
          <div className="relative h-[18px] overflow-hidden rounded-sm bg-primary">
            <div
              className={`absolute inset-0 bg-[linear-gradient(90deg,transparent,rgba(255,255,255,0.35),transparent)] ${
                barVisible ? "animate-[sweep_4s_ease-in-out_infinite]" : ""
              } motion-reduce:animate-none`}
            />
          </div>
          <div className="mt-3 flex justify-between font-mono text-[11px] uppercase tracking-[0.08em] text-muted-foreground">
            <span>1 session completed</span>
            <span>
              <b className="font-medium text-primary">Latest 3/5</b>
            </span>
          </div>
        </div>
      </div>
    </section>
  );
}
```

The `animate-[sweep_...]` arbitrary-value animation references a `@keyframes sweep` rule we need to add to `index.css`. Add it in the next step.

- [ ] **Step 2: Add the `sweep` keyframes to `index.css`**

Append to `web/src/index.css` (after the `@layer base` block):

```css
@keyframes sweep {
  0% { transform: translateX(-100%); }
  60%, 100% { transform: translateX(100%); }
}
```

- [ ] **Step 3: Typecheck**

```bash
cd web && npx tsc -b
```

Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add web/src/pages/landing/hero.tsx web/src/index.css
git commit -m "landing: restyle Hero (eyebrow, wordmark, CTAs, progress bar)"
```

---

## Task 10: Restyle `scoring.tsx`

**Files:**
- Modify: `web/src/pages/landing/scoring.tsx`

Target visual reference: `index.html:545-592`. Data sources stay (`useSampleEvaluation`, `useSampleSession`).

- [ ] **Step 1: Rewrite**

Replace `web/src/pages/landing/scoring.tsx` with:

```tsx
import { useEffect, useState } from "react";

import { useSampleEvaluation, useSampleSession } from "@/api/sample-queries";
import { useScrollReveal } from "@/hooks/use-scroll-reveal";
import type { EvaluationScores } from "@/pb/drill/v1/evaluation_pb";

const DIMENSIONS = [
  { key: "requirements", label: "Requirements" },
  { key: "architecture", label: "Architecture" },
  { key: "deepDive", label: "Deep Dive" },
  { key: "scalability", label: "Scalability" },
  { key: "communication", label: "Communication" },
] as const;

function DimRow({
  label,
  value,
  animate,
  delay,
  isOverall = false,
}: {
  label: string;
  value: number;
  animate: boolean;
  delay: number;
  isOverall?: boolean;
}) {
  const [width, setWidth] = useState(0);

  useEffect(() => {
    if (!animate) return;
    const reducedMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    const timer = setTimeout(
      () => setWidth((value / 5) * 100),
      reducedMotion ? 0 : delay,
    );
    return () => clearTimeout(timer);
  }, [animate, value, delay]);

  const barThickness = isOverall ? "h-[10px] rounded-[5px]" : "h-1.5 rounded-sm";
  const fillBg = isOverall ? "bg-primary" : "bg-foreground";
  const valueClass = isOverall ? "text-[22px] text-primary" : "text-[13px] text-foreground";
  const rowBorder = isOverall ? "border-t-2 border-foreground mt-2 pt-[22px]" : "border-b border-border";

  return (
    <div className={`grid grid-cols-[160px_1fr_80px] items-center gap-5 py-[18px] last:border-b-0 ${rowBorder}`}>
      <div className="text-sm font-medium">{label}</div>
      <div className={`relative overflow-hidden bg-muted ${barThickness}`}>
        <div
          className={`absolute inset-y-0 left-0 transition-[width] duration-1000 ease-[cubic-bezier(.2,.7,.2,1)] motion-reduce:transition-none ${fillBg} ${barThickness}`}
          style={{ width: `${width}%` }}
        />
      </div>
      <div className={`text-right font-mono font-medium ${valueClass}`}>
        {isOverall ? value.toFixed(1) : `${value} / 5`}
      </div>
    </div>
  );
}

export function Scoring() {
  const { ref, isVisible } = useScrollReveal<HTMLElement>();
  const { data: evalData } = useSampleEvaluation();
  const { data: sessionData } = useSampleSession();

  if (!evalData?.evaluation?.scores) return null;
  const scores = evalData.evaluation.scores as EvaluationScores;

  const overall =
    (scores.requirements + scores.architecture + scores.deepDive + scores.scalability + scores.communication) / 5;

  const questionTitle = sessionData?.session?.questionTitle ?? "Sample Session";

  return (
    <section ref={ref} id="scoring" className="py-28 px-10 sm:px-6">
      <div className="mx-auto max-w-[1120px]">
        <header className="mb-16">
          <div className="mb-5 flex items-center gap-3 font-mono text-[11px] uppercase tracking-[0.14em] text-muted-foreground">
            <span className="block h-px w-6 bg-border-strong" aria-hidden /> 01 — Evaluation
          </div>
          <h2 className="mb-5 max-w-[20ch] text-[clamp(32px,4.2vw,56px)] font-light leading-[1.05] tracking-[-0.025em]">
            Scored across five dimensions.
          </h2>
          <p className="max-w-[56ch] text-[17px] text-muted-foreground">
            Graded the way an interviewer would. Requirements, architecture, depth, scale, communication. Not vibes.
          </p>
        </header>

        <div className="overflow-hidden rounded-xl border border-border bg-card">
          <div className="flex items-center justify-between border-b border-border px-6 py-[18px] font-mono text-[11px] uppercase tracking-[0.1em] text-muted-foreground">
            <span>Sample session · {questionTitle} · 30 min</span>
            <span>Rubric v2.1</span>
          </div>
          <div className="px-6 py-3">
            {DIMENSIONS.map((dim, i) => (
              <DimRow
                key={dim.key}
                label={dim.label}
                value={scores[dim.key] as number}
                animate={isVisible}
                delay={i * 80}
              />
            ))}
            <DimRow label="Overall" value={overall} animate={isVisible} delay={DIMENSIONS.length * 80} isOverall />
          </div>
        </div>
      </div>
    </section>
  );
}
```

- [ ] **Step 2: Typecheck**

```bash
cd web && npx tsc -b
```

Expected: no errors. If `scores[dim.key]` fails type narrowing, cast via `(scores as unknown as Record<string, number>)[dim.key]`.

- [ ] **Step 3: Commit**

```bash
git add web/src/pages/landing/scoring.tsx
git commit -m "landing: restyle Scoring to stat-panel layout"
```

---

## Task 11: Restyle `strengths-gaps.tsx`

**Files:**
- Modify: `web/src/pages/landing/strengths-gaps.tsx`

Target visual reference: `index.html:595-631`. Data source: `useSampleEvaluation`.

- [ ] **Step 1: Rewrite**

Replace `web/src/pages/landing/strengths-gaps.tsx` with:

```tsx
import { useSampleEvaluation } from "@/api/sample-queries";
import { useScrollReveal } from "@/hooks/use-scroll-reveal";
import { AnnotationType } from "@/pb/drill/v1/evaluation_pb";

const KIND_CLASS: Record<string, string> = {
  strength: "border-strength",
  gap: "border-gap",
  missed: "border-missed",
};
const KIND_LABEL_CLASS: Record<string, string> = {
  strength: "text-strength",
  gap: "text-gap",
  missed: "text-missed",
};
const KIND_HEADING: Record<string, string> = {
  strength: "Strengths",
  gap: "Gaps",
  missed: "Missed opportunities",
};

function group(kind: "strength" | "gap" | "missed") {
  return AnnotationType[kind === "strength" ? "STRENGTH" : kind === "gap" ? "GAP" : "MISSED_OPPORTUNITY"];
}

export function StrengthsGaps() {
  const { ref, isVisible } = useScrollReveal<HTMLElement>();
  const { data: evalData } = useSampleEvaluation();

  const annotations = evalData?.evaluation?.annotations ?? [];
  if (annotations.length === 0) return null;

  const byKind = {
    strength: annotations.filter((a) => a.type === group("strength")),
    gap: annotations.filter((a) => a.type === group("gap")),
    missed: annotations.filter((a) => a.type === group("missed")),
  };

  return (
    <section ref={ref} id="strengths" className="py-28 px-10 sm:px-6">
      <div className="mx-auto grid max-w-[1120px] grid-cols-1 items-start gap-20 md:grid-cols-[5fr_6fr]">
        <header>
          <div className="mb-5 flex items-center gap-3 font-mono text-[11px] uppercase tracking-[0.14em] text-muted-foreground">
            <span className="block h-px w-6 bg-border-strong" aria-hidden /> 02 — Evidence
          </div>
          <h2 className="mb-5 max-w-[20ch] text-[clamp(32px,4.2vw,56px)] font-light leading-[1.05] tracking-[-0.025em]">
            Know exactly where you stand.
          </h2>
          <p className="max-w-[56ch] text-[17px] text-muted-foreground">
            Specific moments from your transcript, not generic advice. Strengths, gaps, and the ones you nearly caught.
          </p>
        </header>

        <div
          className={`flex flex-col gap-1 transition-opacity duration-700 motion-reduce:transition-none ${
            isVisible ? "opacity-100" : "opacity-0"
          }`}
        >
          {(["strength", "gap", "missed"] as const).map((kind) =>
            byKind[kind].length === 0 ? null : (
              <div key={kind}>
                <div className="mt-5 mb-2.5 font-mono text-[11px] uppercase tracking-[0.12em] text-muted-foreground first:mt-0">
                  {KIND_HEADING[kind]}
                </div>
                {byKind[kind].map((a, i) => (
                  <div
                    key={`${kind}-${i}`}
                    className={`border-l-2 py-2.5 pl-4 text-sm leading-relaxed ${KIND_CLASS[kind]}`}
                  >
                    <span
                      className={`mr-2 font-mono text-[11px] font-medium uppercase tracking-[0.08em] ${KIND_LABEL_CLASS[kind]}`}
                    >
                      {KIND_HEADING[kind].replace(/ opportunities$/, "").replace(/s$/, "")}
                    </span>
                    {a.content}
                  </div>
                ))}
              </div>
            ),
          )}
        </div>
      </div>
    </section>
  );
}
```

- [ ] **Step 2: Typecheck**

```bash
cd web && npx tsc -b
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add web/src/pages/landing/strengths-gaps.tsx
git commit -m "landing: restyle StrengthsGaps to two-column grouped annotations"
```

---

## Task 12: Rename `annotations.tsx` → `transcript.tsx` + restyle

**Files:**
- Delete: `web/src/pages/landing/annotations.tsx`
- Create: `web/src/pages/landing/transcript.tsx`

Target visual reference: `index.html:634-670`. Data sources: `useSampleEvaluation`, `useSampleSession`.

- [ ] **Step 1: Create the new file**

Create `web/src/pages/landing/transcript.tsx`:

```tsx
import { useSampleEvaluation, useSampleSession } from "@/api/sample-queries";
import { useScrollReveal } from "@/hooks/use-scroll-reveal";
import { AnnotationType } from "@/pb/drill/v1/evaluation_pb";

const INLINE_CLASS: Record<number, string> = {
  [AnnotationType.STRENGTH]: "border-strength text-strength",
  [AnnotationType.GAP]: "border-gap text-gap",
  [AnnotationType.MISSED_OPPORTUNITY]: "border-missed text-missed",
  [AnnotationType.NOTE]: "border-note text-note",
};

const LABEL: Record<number, string> = {
  [AnnotationType.STRENGTH]: "Strength",
  [AnnotationType.GAP]: "Gap",
  [AnnotationType.MISSED_OPPORTUNITY]: "Missed",
  [AnnotationType.NOTE]: "Note",
};

export function Transcript() {
  const { ref, isVisible } = useScrollReveal<HTMLElement>();
  const { data: sessionData } = useSampleSession();
  const { data: evalData } = useSampleEvaluation();

  const messages = sessionData?.messages ?? [];
  const annotations = evalData?.evaluation?.annotations ?? [];
  if (messages.length === 0) return null;

  // Index annotations by message seq for lookup during render.
  const byMessageSeq = new Map<number, typeof annotations>();
  for (const a of annotations) {
    const existing = byMessageSeq.get(a.messageSeq) ?? [];
    existing.push(a);
    byMessageSeq.set(a.messageSeq, existing);
  }

  return (
    <section ref={ref} id="transcript" className="py-28 px-10 sm:px-6">
      <div className="mx-auto max-w-[1120px]">
        <header className="mb-16">
          <div className="mb-5 flex items-center gap-3 font-mono text-[11px] uppercase tracking-[0.14em] text-muted-foreground">
            <span className="block h-px w-6 bg-border-strong" aria-hidden /> 03 — Transcript
          </div>
          <h2 className="mb-5 max-w-[20ch] text-[clamp(32px,4.2vw,56px)] font-light leading-[1.05] tracking-[-0.025em]">
            Feedback on what you actually said.
          </h2>
          <p className="max-w-[56ch] text-[17px] text-muted-foreground">
            Annotations anchored to the turn. Replay the session and see the moment the call was made — or wasn't.
          </p>
        </header>

        <div
          className={`flex flex-col gap-5 rounded-xl border border-border bg-card p-7 transition-opacity duration-700 motion-reduce:transition-none ${
            isVisible ? "opacity-100" : "opacity-0"
          }`}
        >
          {messages.map((m) => {
            const isInterviewer = m.role === "interviewer";
            const anns = byMessageSeq.get(m.seq) ?? [];
            return (
              <div key={m.id} className={`flex flex-col gap-2 ${isInterviewer ? "" : "items-end"}`}>
                <div
                  className={`font-mono text-[10px] uppercase tracking-[0.1em] text-muted-foreground ${
                    isInterviewer ? "" : "mr-1 text-right"
                  }`}
                >
                  {isInterviewer ? "Interviewer" : "Candidate"}
                </div>
                <div
                  className={`max-w-[85%] rounded-[10px] px-[18px] py-3.5 text-sm leading-relaxed ${
                    isInterviewer ? "mr-[15%] bg-muted" : "ml-[15%] bg-primary-soft text-left"
                  }`}
                >
                  {m.content}
                </div>
                {anns.map((a, i) => (
                  <div
                    key={`${m.id}-ann-${i}`}
                    className={`ml-6 inline-flex max-w-[80%] items-start gap-2.5 border-l-2 px-3 py-2 text-xs leading-snug ${
                      INLINE_CLASS[a.type] ?? "border-muted text-muted-foreground"
                    }`}
                  >
                    <span>
                      <b className="font-mono text-[10px] font-medium uppercase tracking-[0.08em]">
                        {LABEL[a.type] ?? "Note"}
                      </b>
                      <br />
                      {a.content}
                    </span>
                  </div>
                ))}
              </div>
            );
          })}
        </div>
      </div>
    </section>
  );
}
```

- [ ] **Step 2: Delete the old file**

```bash
git rm web/src/pages/landing/annotations.tsx
```

- [ ] **Step 3: Typecheck**

```bash
cd web && npx tsc -b
```

Expected: errors in `index.tsx` (still imports `Annotations`). That's fixed in Task 18. Field names used match the generated proto as of this spec: `Message.id`, `Message.seq`, `Message.role`, `Message.content`; `Annotation.messageSeq`, `Annotation.content`, `Annotation.type`.

- [ ] **Step 4: Commit** (allow broken state — fixed at task 18)

```bash
git add web/src/pages/landing/transcript.tsx web/src/pages/landing/annotations.tsx
git commit -m "landing: rename annotations → transcript, restyle as bubble+inline"
```

---

## Task 13: Restyle `voice-pipeline.tsx`

**Files:**
- Modify: `web/src/pages/landing/voice-pipeline.tsx`

Target visual reference: `index.html:673-712`. No live data; all copy static.

- [ ] **Step 1: Rewrite**

Replace `web/src/pages/landing/voice-pipeline.tsx` with:

```tsx
import { useScrollReveal } from "@/hooks/use-scroll-reveal";

function barHeights(seedShift: number) {
  return Array.from({ length: 28 }, (_, i) => {
    const seed = i + seedShift;
    return 12 + Math.sin(seed * 0.5) * 14 + ((seed * 7 + 3) % 11) * 0.8;
  });
}

function Waveform({ bars }: { bars: number[] }) {
  return (
    <div className="flex h-12 items-center gap-[3px]" aria-hidden>
      {bars.map((h, i) => (
        <span
          key={i}
          className="w-[3px] rounded-sm bg-primary animate-[wav_1.4s_ease-in-out_infinite] motion-reduce:animate-none"
          style={{ height: `${h}px`, animationDelay: `${i * 0.05}s` }}
        />
      ))}
    </div>
  );
}

export function VoicePipeline() {
  const { ref, isVisible } = useScrollReveal<HTMLElement>();

  return (
    <section ref={ref} id="pipeline" className="py-28 px-10 sm:px-6">
      <div className="mx-auto max-w-[1120px]">
        <header className="mb-16">
          <div className="mb-5 flex items-center gap-3 font-mono text-[11px] uppercase tracking-[0.14em] text-muted-foreground">
            <span className="block h-px w-6 bg-border-strong" aria-hidden /> 04 — Conversation
          </div>
          <h2 className="mb-5 max-w-[20ch] text-[clamp(32px,4.2vw,56px)] font-light leading-[1.05] tracking-[-0.025em]">
            You speak. The interviewer speaks back.
          </h2>
          <p className="max-w-[56ch] text-[17px] text-muted-foreground">
            A conversation, not a form. Follow-ups out loud. Pushback when you hand-wave. Silence when you're mid-thought. Built to feel like the real thing.
          </p>
        </header>

        <div
          className={`grid grid-cols-1 overflow-hidden rounded-xl border border-border bg-card md:grid-cols-3 transition-opacity duration-700 motion-reduce:transition-none ${
            isVisible ? "opacity-100" : "opacity-0"
          }`}
        >
          {/* Col 1 — You */}
          <div className="flex min-h-[220px] flex-col gap-4.5 border-b border-border p-8 md:border-b-0 md:border-r">
            <div className="flex justify-between font-mono text-[10px] uppercase tracking-[0.12em] text-muted-foreground">
              <span>01 · You</span>
              <span className="text-primary">Voice in</span>
            </div>
            <div className="text-[15px] font-medium">Think out loud.</div>
            <div className="flex flex-1 flex-col justify-center">
              <Waveform bars={barHeights(0)} />
              <div className="mt-4 font-mono text-xs leading-[1.7] text-muted-foreground">
                "I'd start by defining the API contract — POST /shorten, GET /:slug
                <span className="ml-1 inline-block h-3.5 w-2 animate-[blink_1s_steps(2)_infinite] bg-primary align-middle motion-reduce:animate-none" />
                "
              </div>
            </div>
          </div>

          {/* Col 2 — Interviewer */}
          <div className="flex min-h-[220px] flex-col gap-4.5 border-b border-border p-8 md:border-b-0 md:border-r">
            <div className="flex justify-between font-mono text-[10px] uppercase tracking-[0.12em] text-muted-foreground">
              <span>02 · Interviewer</span>
              <span className="text-primary">Voice back</span>
            </div>
            <div className="text-[15px] font-medium">A follow-up, out loud.</div>
            <div className="flex flex-1 flex-col justify-center">
              <Waveform bars={barHeights(5)} />
              <div className="mt-4 font-mono text-xs leading-[1.7] text-foreground">
                "Good start. What happens when two users generate the same slug at the same time?"
              </div>
            </div>
          </div>

          {/* Col 3 — Afterward */}
          <div className="flex min-h-[220px] flex-col gap-4.5 p-8">
            <div className="flex justify-between font-mono text-[10px] uppercase tracking-[0.12em] text-muted-foreground">
              <span>03 · Afterward</span>
              <span className="text-primary">Annotated</span>
            </div>
            <div className="text-[15px] font-medium">Every moment, graded.</div>
            <div className="flex flex-1 flex-col justify-start gap-2.5 pt-1.5">
              <div className="border-l-2 border-strength px-3 py-2.5 text-xs text-strength leading-snug">
                <b className="mb-1 block font-mono text-[10px] font-medium uppercase tracking-[0.08em]">Strength</b>
                Leads with API contract.
              </div>
              <div className="border-l-2 border-gap px-3 py-2.5 text-xs text-gap leading-snug">
                <b className="mb-1 block font-mono text-[10px] font-medium uppercase tracking-[0.08em]">Gap</b>
                Collision strategy hand-waved.
              </div>
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}
```

- [ ] **Step 2: Add `wav` and `blink` keyframes to `index.css`**

Append to `web/src/index.css`:

```css
@keyframes wav {
  0%, 100% { transform: scaleY(0.3); }
  50% { transform: scaleY(1); }
}
@keyframes blink {
  50% { opacity: 0; }
}
```

- [ ] **Step 3: Typecheck**

```bash
cd web && npx tsc -b
```

Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add web/src/pages/landing/voice-pipeline.tsx web/src/index.css
git commit -m "landing: restyle VoicePipeline to 3-col conversation"
```

---

## Task 14: Restyle `coaching.tsx`

**Files:**
- Modify: `web/src/pages/landing/coaching.tsx`

Target visual reference: `index.html:715-746`. Data source: `useSampleCoach`.

- [ ] **Step 1: Rewrite**

Replace `web/src/pages/landing/coaching.tsx` with:

```tsx
import { useSampleCoach } from "@/api/sample-queries";
import { useScrollReveal } from "@/hooks/use-scroll-reveal";

function Sparkline({ points }: { points: { x: number; y: number }[] }) {
  if (points.length < 2) return null;
  const path = points.map((p, i) => `${i === 0 ? "M" : "L"} ${p.x} ${p.y}`).join(" ");
  return (
    <svg viewBox="0 0 280 72" className="mt-5 block w-full max-w-[320px]" aria-hidden>
      <path d={path} stroke="var(--primary)" strokeWidth={2} fill="none" strokeLinecap="round" strokeLinejoin="round" />
      <g fill="var(--primary)">
        {points.map((p, i) => (
          <circle
            key={i}
            cx={p.x}
            cy={p.y}
            r={i === points.length - 1 ? 4 : 3}
            stroke={i === points.length - 1 ? "var(--background)" : undefined}
            strokeWidth={i === points.length - 1 ? 2 : undefined}
          />
        ))}
      </g>
    </svg>
  );
}

export function Coaching() {
  const { ref, isVisible } = useScrollReveal<HTMLElement>();
  const { data: coachData } = useSampleCoach();

  const trend = coachData?.scoreTrend ?? [];
  if (trend.length === 0) return null;

  const latest = trend[trend.length - 1].overallScore;
  const earliest = trend[0].overallScore;
  const delta = latest - earliest;

  // Map trend to svg coordinates (width=280, height=72, padding=4, scoreMax=5).
  const width = 280;
  const height = 72;
  const padding = 4;
  const maxScore = 5;
  const points = trend.map((d, i) => ({
    x: padding + (i / (trend.length - 1)) * (width - padding * 2),
    y: padding + (1 - d.overallScore / maxScore) * (height - padding * 2),
  }));

  return (
    <section ref={ref} id="coach" className="py-28 px-10 sm:px-6">
      <div className="mx-auto max-w-[1120px]">
        <header className="mb-16">
          <div className="mb-5 flex items-center gap-3 font-mono text-[11px] uppercase tracking-[0.14em] text-muted-foreground">
            <span className="block h-px w-6 bg-border-strong" aria-hidden /> 05 — Coaching
          </div>
          <h2 className="mb-5 max-w-[20ch] text-[clamp(32px,4.2vw,56px)] font-light leading-[1.05] tracking-[-0.025em]">
            Track your growth across sessions.
          </h2>
          <p className="max-w-[56ch] text-[17px] text-muted-foreground">
            A short letter after every few sessions: what's improving, what to drill, when you're ready.
          </p>
        </header>

        <div
          className={`grid grid-cols-1 items-center gap-10 rounded-xl border border-border bg-card p-8 md:grid-cols-2 transition-opacity duration-700 motion-reduce:transition-none ${
            isVisible ? "opacity-100" : "opacity-0"
          }`}
        >
          <div>
            <div className="mb-1 font-mono text-[11px] uppercase tracking-[0.12em] text-muted-foreground">
              Overall · last {trend.length} sessions
            </div>
            <div className="font-mono text-[64px] font-medium leading-none tracking-[-0.03em] text-primary">
              {latest.toFixed(1)}
              <span className="ml-1.5 text-[28px] text-muted-foreground">/ 5</span>
            </div>
            <div className="mt-2 font-mono text-[13px] text-strength">
              {delta > 0 ? "▲" : "▼"} {Math.abs(delta).toFixed(1)} over last {trend.length - 1} sessions
            </div>
            <Sparkline points={points} />
            <div className="mt-5 inline-flex items-center gap-2 rounded-full bg-[hsl(var(--gap)/.1)] px-3 py-1.5 font-mono text-xs font-medium text-gap">
              Focus area: Deep Dive
            </div>
          </div>

          <div className="border-l-2 border-primary pl-5 text-sm leading-[1.7] text-foreground">
            You've gone from surface-level to structured in four sessions. Requirements framing is reliable now — you're anchoring every session in SLOs before touching boxes.
            <br />
            <br />
            The work ahead is <b>depth</b>. You name the right components but stop one question short of the trade-off. Next drill: one subsystem per session. Failure modes and two alternatives before moving on.
          </div>
        </div>
      </div>
    </section>
  );
}
```

- [ ] **Step 2: Typecheck**

```bash
cd web && npx tsc -b
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add web/src/pages/landing/coaching.tsx
git commit -m "landing: restyle Coaching to trend-card with sparkline + narrative"
```

---

## Task 15: Create `library.tsx` (new) + vitest

**Files:**
- Create: `web/src/pages/landing/library.tsx`
- Create: `web/src/pages/landing/__tests__/library.test.tsx`

Target visual reference: `index.html:749-868`. Data source: `useListFeaturedQuestions` (new, from generated connectquery client).

- [ ] **Step 1: Write the failing vitest**

Create `web/src/pages/landing/__tests__/library.test.tsx`:

```tsx
import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";

import { Library } from "../library";

// Mock the connect-query hook to return canned data.
vi.mock("@connectrpc/connect-query", () => ({
  useQuery: vi.fn(),
}));

import { useQuery } from "@connectrpc/connect-query";

const fakeQuestions = [
  { id: "1", title: "Video Streaming", difficulty: 2, tags: ["streaming", "cdn"], imageUrl: undefined },
  { id: "2", title: "News Feed", difficulty: 2, tags: ["social", "fanout"], imageUrl: undefined },
  { id: "3", title: "Ride Sharing", difficulty: 2, tags: ["geospatial"], imageUrl: undefined },
  { id: "4", title: "Chat System", difficulty: 1, tags: ["real-time"], imageUrl: undefined },
  { id: "5", title: "Search Autocomplete", difficulty: 1, tags: ["trie"], imageUrl: undefined },
  { id: "6", title: "Social Graph", difficulty: 1, tags: ["graph"], imageUrl: undefined },
];

describe("Library", () => {
  it("renders 6 cards in order with total count", () => {
    (useQuery as ReturnType<typeof vi.fn>).mockReturnValue({
      data: { questions: fakeQuestions, totalCount: 18 },
      isLoading: false,
      isError: false,
    });

    render(<Library />);

    const titles = screen.getAllByRole("heading", { level: 3 }).map((h) => h.textContent);
    expect(titles).toEqual([
      "Video Streaming",
      "News Feed",
      "Ride Sharing",
      "Chat System",
      "Search Autocomplete",
      "Social Graph",
    ]);
    expect(screen.getByText(/18 questions/)).toBeInTheDocument();
  });

  it("renders skeletons while loading", () => {
    (useQuery as ReturnType<typeof vi.fn>).mockReturnValue({
      data: undefined,
      isLoading: true,
      isError: false,
    });

    render(<Library />);
    expect(screen.getAllByTestId("library-skeleton")).toHaveLength(6);
  });

  it("renders nothing on error", () => {
    (useQuery as ReturnType<typeof vi.fn>).mockReturnValue({
      data: undefined,
      isLoading: false,
      isError: true,
    });

    const { container } = render(<Library />);
    expect(container).toBeEmptyDOMElement();
  });

  it("renders nothing when total_count is 0", () => {
    (useQuery as ReturnType<typeof vi.fn>).mockReturnValue({
      data: { questions: [], totalCount: 0 },
      isLoading: false,
      isError: false,
    });

    const { container } = render(<Library />);
    expect(container).toBeEmptyDOMElement();
  });
});
```

- [ ] **Step 2: Run tests — expect compile/import failure**

```bash
cd web && npx vitest run src/pages/landing/__tests__/library.test.tsx
```

Expected: error — `Library` is not exported from `../library` (file doesn't exist).

- [ ] **Step 3: Create the component**

Create `web/src/pages/landing/library.tsx`:

```tsx
import { useQuery } from "@connectrpc/connect-query";

import { useScrollReveal } from "@/hooks/use-scroll-reveal";
import { listFeaturedQuestions } from "@/pb/drill/v1/landing-LandingService_connectquery";
import { Difficulty } from "@/pb/drill/v1/question_pb";

const DIFFICULTY_CLASS: Record<number, string> = {
  [Difficulty.MEDIUM]: "bg-muted text-foreground",
  [Difficulty.HARD]: "bg-[hsl(0_84%_60%/.15)] text-[hsl(0_75%_45%)]",
};
const DIFFICULTY_LABEL: Record<number, string> = {
  [Difficulty.MEDIUM]: "medium",
  [Difficulty.HARD]: "hard",
};

function PlaceholderSvg() {
  return (
    <svg viewBox="0 0 200 200" preserveAspectRatio="none" className="block h-full w-full" aria-hidden>
      <rect width="200" height="200" fill="hsl(35 60% 82%)" />
      <polygon points="0,0 120,0 70,90 0,140" fill="hsl(22 58% 58%)" />
      <polygon points="120,0 200,0 200,100 140,70" fill="hsl(32 55% 68%)" />
      <polygon points="0,140 70,90 140,130 90,200 0,200" fill="hsl(200 25% 60%)" opacity="0.7" />
      <polygon points="140,70 200,100 200,200 90,200 140,130" fill="hsl(15 55% 48%)" />
      <circle cx="100" cy="100" r="22" fill="hsl(40 80% 75%)" opacity="0.85" />
      <polygon points="100,78 122,100 100,122 78,100" fill="hsl(25 55% 38%)" opacity="0.7" />
    </svg>
  );
}

export function Library() {
  const { ref, isVisible } = useScrollReveal<HTMLElement>();
  const { data, isLoading, isError } = useQuery(listFeaturedQuestions, {});

  if (isError) return null;
  if (!isLoading && (!data || data.totalCount === 0)) return null;

  return (
    <section ref={ref} id="library" className="py-28 px-10 sm:px-6">
      <div className="mx-auto max-w-[1120px]">
        <header className="mb-16">
          <div className="mb-5 flex items-center gap-3 font-mono text-[11px] uppercase tracking-[0.14em] text-muted-foreground">
            <span className="block h-px w-6 bg-border-strong" aria-hidden /> 06 — Library
          </div>
          <h2 className="mb-5 max-w-[20ch] text-[clamp(32px,4.2vw,56px)] font-light leading-[1.05] tracking-[-0.025em]">
            {data?.totalCount ?? "…"} questions. Real prompts. Real rubrics.
          </h2>
          <p className="max-w-[56ch] text-[17px] text-muted-foreground">
            Hand-written by engineers who've sat on both sides of the table. Every question ships with an expert answer and the topics the interviewer is listening for.
          </p>
        </header>

        <div className="mb-4 flex items-baseline justify-between font-mono text-[11px] uppercase tracking-[0.1em] text-muted-foreground">
          <span>Questions · {data?.totalCount ?? "…"} total</span>
          <span>
            <b className="font-medium text-primary">Latest 3 / 5</b>
          </span>
        </div>
        <div
          className={`relative mb-9 h-[18px] overflow-hidden rounded-sm bg-primary ${
            isVisible ? "after:animate-[sweep_4s_ease-in-out_infinite]" : ""
          } motion-reduce:after:animate-none after:absolute after:inset-0 after:bg-[linear-gradient(90deg,transparent,rgba(255,255,255,0.35),transparent)] after:content-['']`}
        />

        <div className="grid grid-cols-1 gap-8 sm:grid-cols-2 md:grid-cols-3">
          {isLoading
            ? Array.from({ length: 6 }).map((_, i) => (
                <div key={i} data-testid="library-skeleton" className="flex flex-col gap-2.5">
                  <div className="aspect-square animate-pulse rounded-md bg-muted" />
                  <div className="h-4 w-3/4 animate-pulse rounded bg-muted" />
                  <div className="h-3 w-1/2 animate-pulse rounded bg-muted" />
                </div>
              ))
            : (data?.questions ?? []).map((q) => (
                <article
                  key={q.id}
                  className="flex flex-col gap-2.5 transition-transform hover:-translate-y-0.5 motion-reduce:transition-none"
                >
                  <div className="relative aspect-square overflow-hidden rounded-md bg-muted">
                    {q.imageUrl ? <img src={q.imageUrl} alt="" className="h-full w-full object-cover" /> : <PlaceholderSvg />}
                  </div>
                  <h3 className="mt-1 text-sm font-medium">{q.title}</h3>
                  <div className="flex flex-wrap items-center gap-x-2.5 gap-y-1.5 font-mono text-[11px] text-muted-foreground">
                    <span className={`rounded-full px-2 py-0.5 text-[10px] tracking-wide ${DIFFICULTY_CLASS[q.difficulty] ?? "bg-muted"}`}>
                      {DIFFICULTY_LABEL[q.difficulty] ?? ""}
                    </span>
                    {q.tags.join(" ")}
                  </div>
                </article>
              ))}
        </div>
      </div>
    </section>
  );
}
```

- [ ] **Step 4: Run tests — expect PASS**

```bash
cd web && npx vitest run src/pages/landing/__tests__/library.test.tsx
```

Expected: all 4 tests pass. If the generated `listFeaturedQuestions` import path differs from `@/pb/drill/v1/landing-LandingService_connectquery`, check `ls web/src/pb/drill/v1/landing*` and use the correct filename.

- [ ] **Step 5: Commit**

```bash
git add web/src/pages/landing/library.tsx web/src/pages/landing/__tests__/library.test.tsx
git commit -m "landing: add Library section (live featured questions + skeletons)"
```

---

## Task 16: Restyle `credits.tsx`

**Files:**
- Modify: `web/src/pages/landing/credits.tsx`

Target visual reference: `index.html:871-889`. Data: static list (9 roles, already correct).

- [ ] **Step 1: Rewrite**

Replace `web/src/pages/landing/credits.tsx` with:

```tsx
import { useScrollReveal } from "@/hooks/use-scroll-reveal";

const CREDITS = [
  { role: "Interviewer", tech: "Claude Sonnet" },
  { role: "Evaluator", tech: "Claude Opus" },
  { role: "Educator", tech: "Claude Opus" },
  { role: "Coach", tech: "Claude Sonnet" },
  { role: "Speech-to-text", tech: "Whisper" },
  { role: "Text-to-speech", tech: "OpenAI TTS" },
  { role: "Backend", tech: "Go on Google Cloud Run" },
  { role: "Database", tech: "PostgreSQL" },
  { role: "Payments", tech: "Stripe" },
];

export function Credits() {
  const { ref, isVisible } = useScrollReveal<HTMLElement>();

  return (
    <section ref={ref} id="stack" className="py-28 px-10 sm:px-6">
      <div className="mx-auto max-w-[1120px]">
        <header className="mb-10 text-center">
          <div className="mb-5 flex items-center justify-center gap-3 font-mono text-[11px] uppercase tracking-[0.14em] text-muted-foreground">
            <span className="block h-px w-6 bg-border-strong" aria-hidden /> 07 — Built with
          </div>
          <h2 className="mx-auto max-w-none text-[clamp(32px,4.2vw,56px)] font-light leading-[1.05] tracking-[-0.025em]">
            No magic. Good tools.
          </h2>
        </header>

        <div
          className={`grid grid-cols-1 border-t border-border sm:grid-cols-3 transition-opacity duration-700 motion-reduce:transition-none ${
            isVisible ? "opacity-100" : "opacity-0"
          }`}
        >
          {CREDITS.map((c, i) => (
            <div
              key={c.role}
              className={`flex items-baseline justify-between border-b border-border px-6 py-5 sm:[&:nth-child(3n)]:border-r-0 sm:border-r`}
              style={{ transitionDelay: `${i * 40}ms` }}
            >
              <span className="font-mono text-[11px] uppercase tracking-[0.08em] text-muted-foreground">{c.role}</span>
              <span className="text-sm font-medium">{c.tech}</span>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}
```

- [ ] **Step 2: Typecheck**

```bash
cd web && npx tsc -b
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add web/src/pages/landing/credits.tsx
git commit -m "landing: restyle Credits as 3-col bordered stack grid"
```

---

## Task 17: Restyle `cta-repeat.tsx`

**Files:**
- Modify: `web/src/pages/landing/cta-repeat.tsx`

Target visual reference: `index.html:892-900`.

- [ ] **Step 1: Rewrite**

Replace `web/src/pages/landing/cta-repeat.tsx` with:

```tsx
import { Link } from "react-router-dom";

export function CTARepeat() {
  return (
    <section
      aria-label="Call to action"
      className="border-t border-border px-10 py-40 text-center sm:px-6"
    >
      <div className="mx-auto max-w-[1120px]">
        <h2 className="mb-10 text-[clamp(40px,6vw,80px)] font-light leading-none tracking-[-0.035em]">
          practice with precision.
        </h2>
        <div className="mt-2 flex flex-wrap justify-center gap-3">
          <Link
            to="/signup"
            className="inline-flex items-center gap-2 rounded-lg bg-foreground px-[22px] py-[13px] text-sm font-medium text-background transition-transform hover:-translate-y-px"
          >
            Start practicing
            <span className="rounded bg-foreground/20 px-1.5 py-0.5 font-mono text-[10px]">↵</span>
          </Link>
        </div>
        <div className="mt-[22px] font-mono text-xs uppercase tracking-[0.08em] text-muted-foreground">
          3 sessions free · no card · cancel any time
        </div>
      </div>
    </section>
  );
}
```

- [ ] **Step 2: Typecheck**

```bash
cd web && npx tsc -b
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add web/src/pages/landing/cta-repeat.tsx
git commit -m "landing: restyle CTARepeat to final-cta hold-line"
```

---

## Task 18: Update `index.tsx` composition; delete stale files

**Files:**
- Modify: `web/src/pages/landing/index.tsx`
- Delete: `web/src/pages/landing/deep-dive.tsx`
- Delete: `web/src/pages/landing/sample-session.tsx`

- [ ] **Step 1: Rewrite `index.tsx`**

Replace `web/src/pages/landing/index.tsx` with:

```tsx
import { Coaching } from "./coaching";
import { Credits } from "./credits";
import { CTARepeat } from "./cta-repeat";
import { Hero } from "./hero";
import { Library } from "./library";
import { Scoring } from "./scoring";
import { StrengthsGaps } from "./strengths-gaps";
import { Transcript } from "./transcript";
import { VoicePipeline } from "./voice-pipeline";

export default function Landing() {
  return (
    <div className="min-h-screen bg-background text-foreground">
      <Hero />
      <Scoring />
      <StrengthsGaps />
      <Transcript />
      <VoicePipeline />
      <Coaching />
      <Library />
      <Credits />
      <CTARepeat />
    </div>
  );
}
```

- [ ] **Step 2: Delete stale files**

```bash
git rm web/src/pages/landing/deep-dive.tsx web/src/pages/landing/sample-session.tsx
```

- [ ] **Step 3: Typecheck + frontend test suite**

```bash
cd web && npx tsc -b
cd web && npx vitest run
```

Expected: no errors, all tests pass.

- [ ] **Step 4: Commit**

```bash
git add web/src/pages/landing/index.tsx web/src/pages/landing/deep-dive.tsx web/src/pages/landing/sample-session.tsx
git commit -m "landing: compose new sections, drop deep-dive and sample-session"
```

---

## Task 19: Update `PublicHeader`

**Files:**
- Modify: `web/src/components/public-header.tsx`

- [ ] **Step 1: Rewrite**

Replace `web/src/components/public-header.tsx` with:

```tsx
import { Link, useLocation } from "react-router-dom";

import { BrandName } from "@/components/brand-name";

const LANDING_NAV = [
  { href: "#library", label: "Questions" },
  { href: "#pipeline", label: "How it works" },
  { href: "#stack", label: "Stack" },
];

const LANDING_PATHS = new Set(["/", "/about"]);

export function PublicHeader() {
  const location = useLocation();
  const showAnchors = LANDING_PATHS.has(location.pathname);

  return (
    <header className="sticky top-0 z-40 border-b border-border bg-background/90 backdrop-blur-md">
      <div className="mx-auto flex h-[60px] max-w-[1120px] items-center justify-between px-10 sm:px-6">
        <Link to="/" className="text-sm font-medium tracking-[-0.01em]">
          <BrandName />
        </Link>
        <nav aria-label="Public navigation" className="flex items-center gap-7 text-[13px] text-muted-foreground">
          {showAnchors &&
            LANDING_NAV.map((a) => (
              <a key={a.href} href={a.href} className="hover:text-foreground transition-colors">
                {a.label}
              </a>
            ))}
          <Link to="/login" className="hover:text-foreground transition-colors">
            Log in
          </Link>
          <Link
            to="/signup"
            className="rounded-md bg-primary px-4 py-1.5 text-xs font-medium text-primary-foreground"
          >
            Sign up
          </Link>
        </nav>
      </div>
    </header>
  );
}
```

- [ ] **Step 2: Typecheck**

```bash
cd web && npx tsc -b
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add web/src/components/public-header.tsx
git commit -m "header: sticky backdrop-blur with conditional landing anchors"
```

---

## Task 20: Run `make test` and fix anything that drifted

**Files:**
- (Any that fail)

- [ ] **Step 1: Full CI run**

```bash
make test
```

Expected: passes end-to-end (buf lint, codegen check, frontend typecheck + lint + vitest, backend go test -race).

- [ ] **Step 2: Fix any failures**

Common suspects if anything drifts:
- `buf lint` complaining about proto comment style — add a doc comment above `service LandingService`.
- TS strict null checks in `library.tsx` on `q.imageUrl` — the proto field is `optional`, so `q.imageUrl` is `string | undefined`; use `q.imageUrl ? ... : ...`.
- Lint rule wanting named function props in `library.tsx` — extract the inline `Array.from(...).map(...)` to a `Skeleton` subcomponent.

Commit each fix as its own commit if the change is non-trivial (`fix: tsc strict null in library image fallback`, etc.); squash if all cosmetic.

- [ ] **Step 3: Commit (only if fixes made)**

```bash
git status
# If changes: git add <files> && git commit -m "fix: <what>"
```

---

## Task 21: Browser verification

**Files:** none (manual verification)

- [ ] **Step 1: Start dev server**

```bash
make dev
```

Wait for overmind to report vite at `:5173` / air backend at `:8080`.

- [ ] **Step 2: Open the landing at http://localhost:5173/ (unauth)**

If you're currently signed in, log out first (the `/` route will redirect to `Home` via `ConditionalHome`).

Checklist — visually confirm:
- [ ] Hero: eyebrow pill with amber dot, wordmark `sabermatic[.DEV]`, tagline rotates every 3s (5 lines, holds on last), `Start practicing` (dark) and `See sample session` (ghost) CTAs, `3 sessions free · no card`, amber progress bar.
- [ ] Sticky header: scroll past hero — header stays pinned with blurred backdrop. Three anchor links visible (`Questions`, `How it works`, `Stack`). Click each — page scrolls to `#library`, `#pipeline`, `#stack`.
- [ ] Scoring: stat panel with 5 dimension bars + overall bar. Bar fills animate in on scroll.
- [ ] Strengths/Gaps: two-column grid, grouped strengths/gaps/missed with colored left borders.
- [ ] Transcript: bubble layout (interviewer left with neutral bg, candidate right with amber-soft bg), inline annotations below turns.
- [ ] Voice Pipeline: three columns; waveforms on cols 1 and 2 animate; typing cursor blinks in col 1.
- [ ] Coaching: big amber number, delta, sparkline, focus pill, narrative.
- [ ] Library: 6 cards in the known order (Video Streaming, News Feed, Ride Sharing, Chat System, Search Autocomplete, Social Graph), placeholder SVG on every card, headline reads `18 questions. Real prompts. Real rubrics.`, amber progress bar animates.
- [ ] Credits: 3-column bordered grid.
- [ ] Final CTA: huge hold-line `practice with precision.`, primary CTA, mono meta tag.

- [ ] **Step 3: Verify `prefers-reduced-motion`**

Chrome DevTools → Rendering tab → set `Emulate CSS media feature prefers-reduced-motion` → `reduce`. Reload.
- [ ] Tagline doesn't rotate.
- [ ] Waveforms static (no bar oscillation).
- [ ] Progress bar sweep stopped.
- [ ] Typing cursor doesn't blink.
- [ ] Scoring bars jump to their final width without animation.

- [ ] **Step 4: Verify `/about`**

Navigate to http://localhost:5173/about — same landing renders. Header anchor nav still visible.

- [ ] **Step 5: Verify authed still bypasses**

Log in. Visit `/`. You should see the authenticated `Home` inside `AppLayout`, not the landing.

- [ ] **Step 6: Verify Library RPC is public**

Open DevTools Network tab while loading `/` as an unauth user. Find the `ListFeaturedQuestions` request. Response: 200 with 6 questions. No session cookie required.

- [ ] **Step 7: No commit** — this task is manual verification. If something's off, fix with a follow-up task.

---

## Done-criteria summary

All tasks ticked, `make test` green, browser verification passes, `/` and `/about` render the new landing. The `LandingService` serves real data to unauth users. No console errors in the browser.
