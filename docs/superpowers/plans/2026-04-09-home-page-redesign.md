# Home Page Redesign — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Redesign the home page so questions are front and center, with a condensed coach card, per-session bar chart, ghost tile for creating questions, and a cleaned-up nav bar.

**Architecture:** Backend adds a `summary` field to coach analysis (DB column + proto + LLM tool schema). Frontend replaces the wall-of-text coach card with a compact version, swaps the sparkline for a per-session bar chart, removes all question filters, and moves the create-question action to a ghost tile in the grid. Nav bar is simplified (logo as home link, "Sessions" replaces "History", right-aligned).

**Tech Stack:** Go, PostgreSQL, sqlc, protobuf/buf, React, TypeScript, Tailwind CSS, ConnectRPC, base-ui Tooltip

---

### Task 1: DB migration — add `summary` column

**Files:**
- Create: `sql/migrations/011_coach_summary.up.sql`
- Create: `sql/migrations/011_coach_summary.down.sql`
- Modify: `sql/queries/coach_analyses.sql`
- Regenerate: `internal/db/coach_analyses.sql.go`, `internal/db/models.go`

- [ ] **Step 1: Create up migration**

```sql
-- sql/migrations/011_coach_summary.up.sql
ALTER TABLE coach_analyses ADD COLUMN summary TEXT;
```

- [ ] **Step 2: Create down migration**

```sql
-- sql/migrations/011_coach_summary.down.sql
ALTER TABLE coach_analyses DROP COLUMN summary;
```

- [ ] **Step 3: Update SQL queries to include `summary`**

Replace `sql/queries/coach_analyses.sql` with:

```sql
-- name: InsertCoachAnalysis :one
INSERT INTO coach_analyses (
    user_id, narrative, summary, weakest_dimension,
    improving_dimensions, topic_gaps,
    suggested_question_id, sessions_analyzed
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING id;

-- name: GetLatestCoachAnalysis :one
SELECT id, user_id, narrative, summary, weakest_dimension,
       improving_dimensions, topic_gaps,
       suggested_question_id, sessions_analyzed, created_at
FROM coach_analyses
WHERE user_id = $1
ORDER BY created_at DESC LIMIT 1;
```

- [ ] **Step 4: Run sqlc generate**

Run: `sqlc generate`
Expected: regenerates `internal/db/coach_analyses.sql.go` and `internal/db/models.go` with new `Summary pgtype.Text` field on the `CoachAnalysis` struct and updated `InsertCoachAnalysisParams`.

- [ ] **Step 5: Run migration locally**

Run: `psql drill_v0 -f sql/migrations/011_coach_summary.up.sql`
Expected: `ALTER TABLE` success.

- [ ] **Step 6: Verify Go compiles**

Run: `go build ./...`
Expected: clean build. The new `Summary` field on `InsertCoachAnalysisParams` defaults to zero value (`pgtype.Text{}` = NULL) where not explicitly set — no code changes needed until Task 4.

- [ ] **Step 7: Commit**

```bash
git add sql/migrations/011_coach_summary.up.sql sql/migrations/011_coach_summary.down.sql sql/queries/coach_analyses.sql internal/db/
git commit -m "feat: add summary column to coach_analyses"
```

---

### Task 2: Proto — add `summary` field to `CoachAnalysis`

**Files:**
- Modify: `pb/drill/v1/coach.proto`
- Regenerate: `internal/pb/drill/v1/coach.pb.go`, `web/src/pb/drill/v1/coach_pb.ts` (and related)

- [ ] **Step 1: Add `summary` field to proto**

In `pb/drill/v1/coach.proto`, add field 10 to the `CoachAnalysis` message, after `create_time`:

```proto
  google.protobuf.Timestamp create_time = 9;
  optional string summary = 10;
```

- [ ] **Step 2: Run buf generate**

Run: `buf generate`
Expected: regenerates Go and TypeScript proto files with the new `summary` field.

- [ ] **Step 3: Commit**

```bash
git add pb/drill/v1/coach.proto internal/pb/ web/src/pb/
git commit -m "feat: add summary field to CoachAnalysis proto"
```

---

### Task 3: Coach parser — add `summary` to tool schema and parsing (TDD)

**Files:**
- Modify: `internal/coach/parse.go`
- Modify: `internal/coach/parse_test.go`

- [ ] **Step 1: Write the failing test — summary is parsed from tool input**

Add to `internal/coach/parse_test.go`:

```go
func TestParse_SummaryField(t *testing.T) {
	input := validToolInput()
	input["summary"] = "Focus on architecture — requirements are improving."
	raw, err := json.Marshal(input)
	require.NoError(t, err)

	result, err := Parse(json.RawMessage(raw))
	require.NoError(t, err)
	assert.Equal(t, "Focus on architecture — requirements are improving.", result.Summary)
}

func TestParse_MissingSummary(t *testing.T) {
	input := validToolInput()
	// no "summary" key
	raw, err := json.Marshal(input)
	require.NoError(t, err)

	result, err := Parse(json.RawMessage(raw))
	require.NoError(t, err)
	assert.Equal(t, "", result.Summary)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/coach/ -run TestParse_Summary -v`
Expected: FAIL — `CoachResult` has no `Summary` field.

- [ ] **Step 3: Add `summary` to `CoachResult`, `rawToolInput`, and `ToolSchema`**

In `internal/coach/parse.go`:

Add `Summary` to `CoachResult`:
```go
type CoachResult struct {
	Summary             string
	Narrative           string
	WeakestDimension    string
	ImprovingDimensions []string
	TopicGaps           []string
	GeneratedQuestion   *GeneratedQuestion
}
```

Add `Summary` to `rawToolInput`:
```go
type rawToolInput struct {
	Summary             string          `json:"summary"`
	Narrative           string          `json:"narrative"`
	WeakestDimension    string          `json:"weakest_dimension"`
	ImprovingDimensions []string        `json:"improving_dimensions"`
	TopicGaps           []string        `json:"topic_gaps"`
	GeneratedQuestion   *rawGenQuestion `json:"generated_question"`
}
```

Add `"summary"` to `ToolSchema()` properties (inside the `Properties` map, alongside the existing fields):
```go
"summary": map[string]any{
	"type":        "string",
	"description": "One sentence, action-oriented coaching insight. Example: 'Focus on architecture fundamentals — your requirements gathering is improving but needs to drive structural decisions.'",
},
```

Do NOT add `"summary"` to the `Required` slice — it must remain optional.

In the `Parse()` function, add `Summary` to the result assignment (after the existing `Narrative` line):
```go
result := &CoachResult{
	Summary:             input.Summary,
	Narrative:           input.Narrative,
	WeakestDimension:    input.WeakestDimension,
	ImprovingDimensions: input.ImprovingDimensions,
	TopicGaps:           input.TopicGaps,
}
```

- [ ] **Step 4: Add test for summary in ToolSchema**

Add to the existing `TestToolSchema_Fields` test:
```go
assert.Contains(t, props, "summary")
```

And verify summary is NOT in required:
```go
assert.NotContains(t, required, "summary")
```

- [ ] **Step 5: Run all coach parser tests**

Run: `go test ./internal/coach/ -v`
Expected: all tests PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/coach/parse.go internal/coach/parse_test.go
git commit -m "feat: add summary field to coach tool schema and parser"
```

---

### Task 4: Wire `summary` through backend, RPC, and job

**Files:**
- Modify: `internal/backend/coach.go`
- Modify: `internal/rpc/coach/server.go`
- Modify: `internal/jobs/coach.go`

- [ ] **Step 1: Add `Summary` to `CoachResponse` in backend**

In `internal/backend/coach.go`, add `Summary` to the `CoachResponse` struct:
```go
type CoachResponse struct {
	ID                  string    `json:"id"`
	UserID              string    `json:"user_id"`
	Summary             string    `json:"summary,omitempty"`
	Narrative           string    `json:"narrative"`
	WeakestDimension    string    `json:"weakest_dimension,omitempty"`
	ImprovingDimensions []string  `json:"improving_dimensions,omitempty"`
	TopicGaps           []string  `json:"topic_gaps,omitempty"`
	SuggestedQuestionID *string   `json:"suggested_question_id,omitempty"`
	SessionsAnalyzed    []string  `json:"sessions_analyzed"`
	CreatedAt           time.Time `json:"created_at"`
}
```

In `GetLatestCoachAnalysis`, add the summary mapping in the response construction (after `Narrative`):
```go
resp := &CoachResponse{
	ID:                  ca.ID.String(),
	UserID:              ca.UserID.String(),
	Summary:             ca.Summary.String,
	Narrative:           ca.Narrative,
	WeakestDimension:    ca.WeakestDimension.String,
	ImprovingDimensions: ca.ImprovingDimensions,
	TopicGaps:           ca.TopicGaps,
	SessionsAnalyzed:    sessionsAnalyzed,
	CreatedAt:           ca.CreatedAt,
}
```

- [ ] **Step 2: Add `Summary` to `coachResponseToProto` in RPC**

In `internal/rpc/coach/server.go`, add the summary mapping in `coachResponseToProto`:
```go
func coachResponseToProto(r *backend.CoachResponse) *drillv1.CoachAnalysis {
	ca := &drillv1.CoachAnalysis{
		Id:                  r.ID,
		UserId:              r.UserID,
		Narrative:           r.Narrative,
		ImprovingDimensions: r.ImprovingDimensions,
		TopicGaps:           r.TopicGaps,
		SessionsAnalyzed:    r.SessionsAnalyzed,
		CreateTime:          timestamppb.New(r.CreatedAt),
	}

	if r.Summary != "" {
		ca.Summary = &r.Summary
	}

	if r.WeakestDimension != "" {
		ca.WeakestDimension = &r.WeakestDimension
	}

	if r.SuggestedQuestionID != nil {
		ca.SuggestedQuestionId = r.SuggestedQuestionID
	}

	return ca
}
```

- [ ] **Step 3: Persist `summary` in the coach job**

In `internal/jobs/coach.go`, update the `InsertCoachAnalysis` call (around line 153) to include `Summary`:

```go
_, err = txq.InsertCoachAnalysis(ctx, db.InsertCoachAnalysisParams{
	UserID:              userID,
	Narrative:           result.Narrative,
	Summary:             pgtype.Text{String: result.Summary, Valid: result.Summary != ""},
	WeakestDimension:    pgtype.Text{String: result.WeakestDimension, Valid: result.WeakestDimension != ""},
	ImprovingDimensions: result.ImprovingDimensions,
	TopicGaps:           result.TopicGaps,
	SuggestedQuestionID: suggestedQuestionID,
	SessionsAnalyzed:    sessionIDs,
})
```

- [ ] **Step 4: Run all backend and RPC tests**

Run: `go test ./internal/backend/ ./internal/rpc/coach/ ./internal/jobs/ -v`
Expected: all tests PASS. The `InsertCoachAnalysis` mock/test calls may need updating if they assert on params — check test output and fix if needed.

- [ ] **Step 5: Verify full build**

Run: `go build ./...`
Expected: clean build, no errors.

- [ ] **Step 6: Commit**

```bash
git add internal/backend/coach.go internal/rpc/coach/server.go internal/jobs/coach.go
git commit -m "feat: wire summary through backend, RPC, and coach job"
```

---

### Task 5: Nav bar — logo as home link, rename History → Sessions, right-align

**Files:**
- Modify: `web/src/layouts/app-layout.tsx`
- Modify: `web/src/pages/history.tsx`

- [ ] **Step 1: Restructure the nav bar in `app-layout.tsx`**

In `web/src/layouts/app-layout.tsx`, replace the entire nav section inside the header `<div>` (lines 43-75). The new structure: logo on the left, nav links + avatar on the right.

Replace:
```tsx
<div className="flex items-center gap-6">
  <Link to="/" className="text-sm font-semibold tracking-wider text-muted-foreground">
    DRILL
  </Link>
  <nav className="flex items-center gap-4 text-sm">
    <Link
      to="/"
      className={location.pathname === "/" ? "text-foreground" : "text-muted-foreground hover:text-foreground"}
    >
      Home
    </Link>
    {hasAnySessions ? (
      <Link
        to="/history"
        className={location.pathname === "/history" ? "text-foreground" : "text-muted-foreground hover:text-foreground"}
      >
        History
      </Link>
    ) : (
      <span className="cursor-default text-muted-foreground/50">History</span>
    )}
    {user?.role === UserRole.ADMIN && (
      <a
        href="/admin/jobs/"
        target="_blank"
        rel="noopener noreferrer"
        className="text-muted-foreground hover:text-foreground"
      >
        Jobs
      </a>
    )}
  </nav>
</div>
```

With:
```tsx
<Link to="/" className="text-sm font-semibold tracking-wider text-muted-foreground hover:text-foreground transition-colors">
  DRILL
</Link>
<div className="flex items-center gap-4">
  <nav className="flex items-center gap-4 text-sm">
    {hasAnySessions ? (
      <Link
        to="/history"
        className={location.pathname === "/history" ? "text-foreground" : "text-muted-foreground hover:text-foreground"}
      >
        Sessions
      </Link>
    ) : (
      <span className="cursor-default text-muted-foreground/50">Sessions</span>
    )}
    {user?.role === UserRole.ADMIN && (
      <a
        href="/admin/jobs/"
        target="_blank"
        rel="noopener noreferrer"
        className="text-muted-foreground hover:text-foreground"
      >
        Jobs
      </a>
    )}
  </nav>
```

Then close the `<div>` to wrap the nav and the existing `<DropdownMenu>` together. The `<DropdownMenu>` (avatar, lines 76-95) moves inside this new `<div>`.

The final structure of the header content should be:
```
<div "mx-auto flex h-12 max-w-5xl items-center justify-between px-4">
  <Link to="/">DRILL</Link>                    ← left
  <div "flex items-center gap-4">              ← right
    <nav>Sessions | Jobs</nav>
    <DropdownMenu>avatar</DropdownMenu>
  </div>
</div>
```

- [ ] **Step 2: Rename heading in history page**

In `web/src/pages/history.tsx`, change the heading on line 471:

Replace:
```tsx
<h1 className="text-base font-medium">History</h1>
```

With:
```tsx
<h1 className="text-base font-medium">Sessions</h1>
```

- [ ] **Step 3: Load the app in the browser and verify**

Open `http://localhost:5173` (or the dev server URL). Verify:
- DRILL logo links to home
- No "Home" link in nav
- "Sessions" link appears (if sessions exist) on the right side
- Avatar dropdown is next to nav links on the right
- Clicking "Sessions" navigates to the history page with "Sessions" heading

- [ ] **Step 4: Commit**

```bash
git add web/src/layouts/app-layout.tsx web/src/pages/history.tsx
git commit -m "feat: nav bar — logo as home link, History renamed to Sessions, right-aligned"
```

---

### Task 6: Home page cleanup — fix archived bug, remove filters and old components

**Files:**
- Modify: `web/src/pages/home.tsx`

This task removes dead code and fixes the archived session count bug. No new components yet.

- [ ] **Step 1: Fix `reviewedSessions()` to exclude archived sessions**

Replace:
```tsx
function reviewedSessions(sessions: SessionSummary[]): SessionSummary[] {
  return sessions.filter((s) => s.status === SessionStatus.REVIEWED);
}
```

With:
```tsx
function reviewedSessions(sessions: SessionSummary[]): SessionSummary[] {
  return sessions.filter((s) => s.status === SessionStatus.REVIEWED && !s.archiveTime);
}
```

- [ ] **Step 2: Delete unused components and functions**

Delete these entire functions/components from `home.tsx`:
- `allTags` function (lines 62-68)
- `ScoreSparkline` component (lines 71-123)
- `SummaryStrip` component (lines 126-139)
- `QuestionFilters` component (lines 281-321)

- [ ] **Step 3: Remove unused imports**

Remove these imports since they are no longer used:
- `useSearchParams` from `react-router-dom`
- `LineChart`, `Line`, `ResponsiveContainer`, `Tooltip as RechartsTooltip` from `recharts`

The import line for react-router-dom becomes:
```tsx
import { Link } from "react-router-dom";
```

Remove the entire recharts import line.

- [ ] **Step 4: Remove filter-related state and logic from `Home` component**

In the `Home` component (starting at line 536), remove:

```tsx
const [searchParams, setSearchParams] = useSearchParams();

const difficulty = searchParams.get("difficulty") ?? "";
const tagsParam = searchParams.get("tags") ?? "";
const selectedTags = useMemo(
  () => new Set(tagsParam ? tagsParam.split(",") : []),
  [tagsParam],
);
```

Remove:
```tsx
const tagList = useMemo(() => allTags(questions), [questions]);
```

Remove:
```tsx
// Map URL param string to proto enum for filtering
const difficultyEnum = useMemo(() => {
  if (difficulty === "medium") return Difficulty.MEDIUM;
  if (difficulty === "hard") return Difficulty.HARD;
  return undefined;
}, [difficulty]);

// Filter questions
const filtered = useMemo(() => {
  let list = questions;
  if (difficultyEnum !== undefined) {
    list = list.filter((q) => q.difficulty === difficultyEnum);
  }
  if (selectedTags.size > 0) {
    list = list.filter((q) => q.tags.some((t) => selectedTags.has(t)));
  }
  return list;
}, [questions, difficultyEnum, selectedTags]);
```

Remove:
```tsx
function setDifficulty(v: string) { ... }
function toggleTag(tag: string) { ... }
```

Update `heroQuestion` to use `questions` instead of `filtered`:
```tsx
const heroQuestion = suggestedId ? questions.find((q) => q.id === suggestedId) : undefined;
```

- [ ] **Step 5: Remove filter and old component rendering from JSX**

Remove the `QuestionFilters` rendering block:
```tsx
{/* Filters */}
{tagList.length > 0 && (
  <QuestionFilters
    allTagList={tagList}
    difficulty={difficulty}
    selectedTags={selectedTags}
    onDifficultyChange={setDifficulty}
    onTagToggle={toggleTag}
  />
)}
```

Replace `SummaryStrip` rendering block:
```tsx
{(isReturning || isActive) && (
  <div className="flex items-center gap-6">
    <SummaryStrip sessions={sessions} />
  </div>
)}
```

With a placeholder comment (replaced in Task 7):
```tsx
{/* Session bar chart — added in Task 7 */}
```

Update the question grid to use `questions` instead of `filtered`:
```tsx
{questions
  .filter((q) => q.id !== suggestedId)
  .map((q) => (
    <QuestionCard
      key={q.id}
      question={q}
      startDisabled={atConcurrentLimit}
    />
  ))}
```

Replace the empty filter message:
```tsx
{filtered.length === 0 && (
  <p className="text-sm text-muted-foreground py-8 text-center col-span-full">
    No questions match your filters.
  </p>
)}
```

With (or remove entirely — the ghost tile in Task 9 will always be present):
Nothing — just delete the block.

- [ ] **Step 6: Remove unused imports that resulted from the cleanup**

Check if `Difficulty` is still imported — it's no longer needed (was only used for filter enum mapping). Remove it:
```tsx
import { QuestionSource } from "@/pb/drill/v1/question_pb";
```

(Remove `Difficulty` from the import.)

- [ ] **Step 7: Verify build**

Run: `cd web && npx tsc --noEmit`
Expected: no type errors.

- [ ] **Step 8: Load in browser and verify**

Open `http://localhost:5173`. Verify:
- No filter bar visible
- No sparkline visible
- Questions grid shows all questions without filtering
- Hero question still appears if coach suggested one
- Session count in the old strip is gone (replaced by placeholder)

- [ ] **Step 9: Commit**

```bash
git add web/src/pages/home.tsx
git commit -m "fix: exclude archived sessions from count; remove filters and sparkline"
```

---

### Task 7: Session bar chart component

**Files:**
- Modify: `web/src/pages/home.tsx`

- [ ] **Step 1: Add Tooltip imports**

Add to the imports in `home.tsx`:
```tsx
import {
  Tooltip, TooltipTrigger, TooltipContent, TooltipProvider,
} from "@/components/ui/tooltip";
```

- [ ] **Step 2: Write the `SessionBarChart` component**

Add this component in `home.tsx` (where `SummaryStrip` was):

```tsx
function SessionBarChart({ sessions }: { sessions: SessionSummary[] }) {
  const reviewed = reviewedSessions(sessions);
  if (reviewed.length === 0) return null;

  const sorted = [...reviewed].sort((a, b) => {
    const ta = a.createTime ? Number(a.createTime.seconds) : 0;
    const tb = b.createTime ? Number(b.createTime.seconds) : 0;
    return ta - tb;
  });

  const latest = sorted[sorted.length - 1]!;
  const previous = sorted.length >= 2 ? sorted[sorted.length - 2]! : undefined;
  const latestScore = latest.scoreOverall ?? 0;
  const trendArrow =
    previous?.scoreOverall != null && latest.scoreOverall != null
      ? latest.scoreOverall > previous.scoreOverall
        ? " \u2191"
        : latest.scoreOverall < previous.scoreOverall
          ? " \u2193"
          : ""
      : "";

  return (
    <div>
      <TooltipProvider>
        <div className="flex items-end gap-1 h-12">
          {sorted.map((s, i) => {
            const score = s.scoreOverall ?? 0;
            const heightPct = Math.max((score / 5) * 100, 4);
            const isLatest = i === sorted.length - 1;
            return (
              <Tooltip key={s.id}>
                <TooltipTrigger
                  render={
                    <Link
                      to={`/sessions/${s.id}/overview`}
                      className="flex-1 rounded-t transition-opacity hover:opacity-75"
                      style={{
                        height: `${heightPct}%`,
                        backgroundColor: scoreColor(score),
                        outline: isLatest ? "2px solid rgba(0,0,0,0.15)" : undefined,
                        outlineOffset: isLatest ? "1px" : undefined,
                      }}
                    />
                  }
                />
                <TooltipContent>
                  {s.questionTitle || "Session"} &middot; {score}/5
                </TooltipContent>
              </Tooltip>
            );
          })}
        </div>
      </TooltipProvider>
      <div className="h-px bg-border mt-0.5 mb-1.5" />
      <div className="flex items-center justify-between text-xs text-muted-foreground">
        <span>{sorted.length} sessions</span>
        <span style={{ color: scoreColor(latestScore) }} className="font-semibold">
          Latest {latestScore}/5{trendArrow}
        </span>
      </div>
    </div>
  );
}
```

- [ ] **Step 3: Wire `SessionBarChart` into the `Home` component**

Replace the placeholder comment from Task 6:
```tsx
{/* Session bar chart — added in Task 7 */}
```

With:
```tsx
<SessionBarChart sessions={sessions} />
```

This renders for both `isReturning` and `isActive` users (the component itself returns null if no reviewed sessions).

- [ ] **Step 4: Verify build**

Run: `cd web && npx tsc --noEmit`
Expected: no type errors.

- [ ] **Step 5: Load in browser and verify**

Open `http://localhost:5173`. Verify:
- Bar chart appears if there are reviewed sessions
- Each bar is colored by score (red/orange/yellow/green)
- Latest bar has a subtle outline
- Hovering a bar shows tooltip with question title and score
- Clicking a bar navigates to that session's overview
- "N sessions" count on the left
- Latest score with trend arrow on the right
- Bar chart is hidden when there are no reviewed sessions

- [ ] **Step 6: Commit**

```bash
git add web/src/pages/home.tsx
git commit -m "feat: session bar chart replacing sparkline"
```

---

### Task 8: Condensed coach card + analysis modal

**Files:**
- Modify: `web/src/pages/home.tsx`

- [ ] **Step 1: Rewrite the `CoachCard` component**

Replace the entire `CoachCard` component (from the `function CoachCard` line through its closing `}`) with:

```tsx
function CoachCard({ coach, isActive }: {
  coach: CoachAnalysis | undefined;
  isActive: boolean;
}) {
  const [modalOpen, setModalOpen] = useState(false);
  const qc = useQueryClient();
  const coachAnalysisKey = createConnectQueryKey({ schema: getCoachAnalysis, input: {}, cardinality: "finite" });
  const requestCoach = useMutation(requestCoachAnalysis, {
    onSuccess: () => {
      toast.success("Coach analysis requested");
      qc.invalidateQueries({ queryKey: coachAnalysisKey });
    },
  });

  if (!isActive) return null;

  if (requestCoach.isPending) {
    return (
      <div className="rounded-xl border border-border bg-card px-5 py-4">
        <p className="text-sm text-muted-foreground animate-pulse">
          Analyzing your progress...
        </p>
      </div>
    );
  }

  if (!coach) {
    return (
      <div className="rounded-xl border border-border bg-card px-5 py-4 flex items-center justify-between">
        <p className="text-sm text-muted-foreground">
          Get strategic coaching based on your sessions.
        </p>
        <Button
          variant="outline"
          size="sm"
          onClick={() => requestCoach.mutate({})}
        >
          Get strategic coaching
        </Button>
      </div>
    );
  }

  return (
    <>
      <div className="rounded-xl border border-amber-300/40 bg-amber-50/30 dark:border-amber-500/20 dark:bg-amber-950/20 px-5 py-4 space-y-3">
        <span className="text-[10px] font-bold tracking-wider uppercase text-muted-foreground">
          Coach
        </span>
        {coach.summary && (
          <p className="text-sm italic text-foreground/80 leading-relaxed border-l-[3px] border-amber-400/60 pl-3">
            &ldquo;{coach.summary}&rdquo;
          </p>
        )}
        <div className="flex items-center gap-2 flex-wrap">
          {coach.weakestDimension && (
            <span className="text-xs font-semibold px-2.5 py-0.5 rounded-full bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-400">
              &darr; {coach.weakestDimension}
            </span>
          )}
          {coach.improvingDimensions.map((dim) => (
            <span
              key={dim}
              className="text-xs font-semibold px-2.5 py-0.5 rounded-full bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-400"
            >
              &uarr; {dim}
            </span>
          ))}
          <button
            type="button"
            onClick={() => setModalOpen(true)}
            className="ml-auto text-xs text-muted-foreground hover:text-foreground transition-colors cursor-pointer"
          >
            Read full analysis &rarr;
          </button>
        </div>
      </div>

      <CoachAnalysisModal
        open={modalOpen}
        onOpenChange={setModalOpen}
        narrative={coach.narrative}
      />
    </>
  );
}
```

- [ ] **Step 2: Add the `CoachAnalysisModal` component**

Add this component right after `CoachCard`:

```tsx
// TODO: Upgrade modal to dedicated /coach page.
// Add session history, dimension trend charts over time.
function CoachAnalysisModal({
  open,
  onOpenChange,
  narrative,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  narrative: string;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-xl max-h-[80vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>Coach Analysis</DialogTitle>
        </DialogHeader>
        <div className="text-sm text-foreground leading-relaxed">
          <ReactMarkdown
            remarkPlugins={[remarkGfm]}
            components={{
              p: ({ children, ...props }) => <p {...props} className="mb-3 last:mb-0">{children}</p>,
              ul: ({ children, ...props }) => <ul {...props} className="list-disc pl-5 mb-3 space-y-1">{children}</ul>,
              ol: ({ children, ...props }) => <ol {...props} className="list-decimal pl-5 mb-3 space-y-1">{children}</ol>,
              strong: ({ children, ...props }) => <strong {...props} className="font-semibold">{children}</strong>,
            }}
          >
            {narrative}
          </ReactMarkdown>
        </div>
      </DialogContent>
    </Dialog>
  );
}
```

- [ ] **Step 3: Clean up unused imports**

After the CoachCard rewrite, check if these are still needed:
- `Card`, `CardContent`, `CardHeader`, `CardTitle` — remove if no longer used anywhere in `home.tsx`. (`Card`/`CardContent` may still be used elsewhere — check.)

- [ ] **Step 4: Verify build**

Run: `cd web && npx tsc --noEmit`
Expected: no type errors.

- [ ] **Step 5: Load in browser and verify**

Open `http://localhost:5173`. Verify:
- Coach card is compact: shows "COACH" label, summary quote (italic with left border), dimension chips (red ↓ / green ↑), "Read full analysis →" link
- No refresh button
- Clicking "Read full analysis →" opens a modal with the full narrative in formatted markdown
- Modal closes on ✕ or clicking outside
- If no coach analysis: "Get strategic coaching" button shows
- If no summary field (older analyses): only dimension chips and "Read full analysis →" shown (no empty quote)

- [ ] **Step 6: Commit**

```bash
git add web/src/pages/home.tsx
git commit -m "feat: condensed coach card with summary quote and analysis modal"
```

---

### Task 9: Ghost tile for create question

**Files:**
- Modify: `web/src/pages/home.tsx`

- [ ] **Step 1: Update `CreateQuestionDialog` to use ghost tile trigger**

Replace the `DialogTrigger` inside `CreateQuestionDialog`:

Replace:
```tsx
<DialogTrigger render={<Button variant="outline" size="sm" />}>
  Create question
</DialogTrigger>
```

With:
```tsx
<DialogTrigger
  render={
    <div className="rounded-[14px] border-[1.5px] border-dashed border-border flex flex-col items-center justify-center gap-2 cursor-pointer text-muted-foreground hover:text-foreground hover:border-foreground/30 transition-colors aspect-[4/3]">
      <span className="text-2xl font-light leading-none">+</span>
      <span className="text-xs">New question</span>
    </div>
  }
/>
```

- [ ] **Step 2: Move `CreateQuestionDialog` from header to grid**

In the `Home` component JSX, remove it from the header:

Replace:
```tsx
<div className="flex items-center justify-between">
  <h2 className="text-base font-medium">Questions</h2>
  <CreateQuestionDialog />
</div>
```

With:
```tsx
<h2 className="text-base font-medium">Questions</h2>
```

Then add `<CreateQuestionDialog />` as the last item inside the question grid `<div>`:

```tsx
<div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-x-6 gap-y-8">
  {heroQuestion && (
    <HeroQuestionCard
      question={heroQuestion}
      startDisabled={atConcurrentLimit}
    />
  )}
  {questions
    .filter((q) => q.id !== suggestedId)
    .map((q) => (
      <QuestionCard
        key={q.id}
        question={q}
        startDisabled={atConcurrentLimit}
      />
    ))}
  <CreateQuestionDialog />
</div>
```

- [ ] **Step 3: Verify build**

Run: `cd web && npx tsc --noEmit`
Expected: no type errors.

- [ ] **Step 4: Load in browser and verify**

Open `http://localhost:5173`. Verify:
- No "Create question" button next to the Questions heading
- Ghost tile appears at the end of the question grid (dashed border, "+" icon, "New question" label)
- Ghost tile matches the height of question cards (aspect-ratio 4/3)
- Clicking the ghost tile opens the create question dialog
- Creating a question works — dialog closes, new question appears in the grid
- When there are zero questions, the ghost tile appears alone in the grid

- [ ] **Step 5: Commit**

```bash
git add web/src/pages/home.tsx
git commit -m "feat: ghost tile replaces create question button in grid"
```
