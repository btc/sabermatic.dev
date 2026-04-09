# Home Page Redesign

**Date:** 2026-04-09
**Status:** Approved

## Problem

The current home page buries the question grid — the most visually compelling part of the product — below a full-fold coach analysis wall-of-text. The tag filter cloud is overwhelming and rarely useful. The session summary strip uses a ticker layout that doesn't read well.

## Goals

- Questions front and center
- Coach info condensed and scannable — not a wall of text
- Remove the filter bar entirely
- Improve the session progress visualization
- Make "Create question" a secondary, discoverable action
- Clean up nav: logo as home link, nav links right-aligned, rename "History" → "Sessions"

---

## Layout Structure (top to bottom)

1. **Nav bar** — see changes below
2. **Session bar chart** — hidden if no reviewed sessions
3. **Coach card** — compact, shown only for active users with a coach analysis
4. **Questions section** — label, hero card, grid

---

## 0. Nav Bar

**Layout:** logo left, all navigation right.

```
DRILL                              Sessions  [avatar]
```

**Changes:**
- `DRILL` logo becomes a `<Link to="/">` — clicking it navigates home. Remove the separate "Home" link.
- Rename the "History" link to **"Sessions"** and update the route label (the `/history` route itself can be renamed `/sessions` or kept as-is — see frontend changes).
- Move remaining nav links flush right, before the avatar.

**Frontend changes:**
- `app-layout.tsx` (or wherever the nav lives): reorder elements, make logo a link, rename History → Sessions.
- Update the page `<title>` on the history page from "History" to "Sessions".
- Update any internal `<Link>` labels that say "History" (e.g. in empty states, coach card copy).

---

## 1. Session Bar Chart

Replaces the current `SummaryStrip` (ticker text + line sparkline).

**Behavior:**
- Hidden entirely when there are zero reviewed (non-archived) sessions
- One bar per reviewed session, ordered chronologically left to right
- Bar height proportional to overall score (0–5 scale)
- Bar color matches score tier: red (<2), orange (2–3.4), green (≥3.5) — consistent with existing `scoreColor` helper
- Latest session bar has a subtle outline to distinguish it
- Each bar is a `<Link to={/sessions/:id/overview}>` — clicking navigates to that session's review
- Hover tooltip shows: session question title + score (e.g. "URL Shortener · 3.8/5")
- Below the bars: a baseline rule, then two items flush left/right:
  - Left: `"N sessions"` — count of reviewed non-archived sessions
  - Right: latest session score colored green/orange/red. Trend arrow: `↑` if latest > previous session, `↓` if latest < previous, omitted if only one session or scores are equal.

**Bug fix:** `reviewedSessions()` currently filters only on `status === REVIEWED`. It must also exclude sessions with `status === ARCHIVED`. Update the filter accordingly.

---

## 2. Coach Card

Replaces the current `CoachCard` (full narrative + refresh button).

**Shown:** when `isActive` is true. Content varies based on whether an analysis exists (see loading/empty states below).

**Layout:**
```
COACH                          (label, small caps, muted)
┌ "Summary sentence here…"     (italic pull quote, left border accent)
↓ Architecture  ↑ Requirements  ↑ Communication    Read full analysis →
```

**Fields used:**
- `summary` — new field (see backend change below), one action-oriented sentence
- `weakestDimension` → rendered as `↓ <dimension>` chip, red background
- `improvingDimensions` → each as `↑ <dimension>` chip, green background
- `suggestedQuestionId` — drives the hero question (unchanged)

**Removed:** refresh button, full narrative inline, `weakestDimension` focus area tag at bottom.

**"Read full analysis →"** opens a modal containing the full `narrative` markdown rendered as plain paragraphs. Modal has an `✕` close button and closes on backdrop click.

```tsx
// TODO: Upgrade modal to dedicated /coach page.
// Add session history, dimension trend charts over time.
```

**Loading / pending state:** unchanged (existing "Analyzing your progress…" pulse).

**No coach yet state:** unchanged (existing "Get strategic coaching" prompt + button).

---

## 3. Questions Section

### Section header
Just the label `"Questions"` — no button.

### Hero question card
Unchanged. Full-width card shown when `coach.suggestedQuestionId` matches a question. Displays "Recommended for you" label, title, difficulty badge, and tags.

### Question grid
3-column responsive grid, unchanged card design (cubist image, title, difficulty badge, tags).

**Ghost tile:** replaces the "Create question" button. Rendered as the last item in the grid — a dashed-border card with a `+` icon and "New question" label. Opens the existing create-question dialog on click. Styled to match grid card dimensions.

### Filter bar
Removed entirely. No difficulty dropdown. No tag cloud. Tags remain visible on cards as read-only labels.

---

## Backend Changes

### New `summary` field on `CoachAnalysis`

Add `summary` to the proto message and LLM tool schema:

**`pb/drill/v1/coach.proto`:**
```proto
message CoachAnalysis {
  // ... existing fields ...
  optional string summary = 10; // One-sentence action-oriented coaching insight
}
```

**`internal/coach/parse.go` — tool schema:**
```go
"summary": map[string]any{
    "type":        "string",
    "description": "One sentence, action-oriented coaching insight. Example: 'Focus on architecture fundamentals — your requirements gathering is improving but needs to drive structural decisions.'",
},
```
`summary` must NOT be added to the `Required` array — it is optional so existing analyses without this field continue to work.

**`internal/coach/parse.go` — `CoachResult`:**
```go
type CoachResult struct {
    Summary             string  // new
    Narrative           string
    // ... rest unchanged
}
```

**`internal/db` / SQL:** add `summary` column to `coach_analyses` table via migration. Nullable — existing rows have no summary. Update `sql/queries/coach_analyses.sql` (`InsertCoachAnalysis` and the select queries) and re-run `sqlc generate` to regenerate `internal/db/`.

**`internal/jobs/coach.go`:** persist `result.Summary` to the new column.

**Proto → frontend:** `summary` field surfaces on `CoachAnalysis` proto message; the frontend reads it directly.

**Fallback:** if `summary` is empty (existing analyses), the coach card omits the pull quote and shows only the dimension chips + "Read full analysis →".

---

## Frontend Changes

All changes are in `web/src/pages/home.tsx` unless noted.

| Component | Change |
|---|---|
| Nav bar (`app-layout.tsx`) | Logo → `<Link to="/">`, remove "Home" link, move links right, rename "History" → "Sessions" |
| History page | Update `<title>` and any self-referential "History" labels to "Sessions" |
| `reviewedSessions()` | Also filter out `status === SessionStatus.ARCHIVED` |
| `SummaryStrip` | Replace with `SessionBarChart` (bar-per-session, linked, color-coded, hidden if empty) |
| `CoachCard` | Condensed layout: summary quote + dimension chips + modal trigger. Remove refresh button. |
| `CoachAnalysisModal` | New component: renders full `narrative` in a modal. Includes `// TODO: /coach page` comment. |
| `QuestionFilters` | Delete entirely |
| `HeroQuestionCard` | Unchanged |
| `QuestionCard` | Unchanged |
| Question grid | Add `GhostTile` as last grid item — opens create-question dialog |
| Create question button | Remove from section header |

---

## Empty / Edge States

| State | Behavior |
|---|---|
| No reviewed sessions | Bar chart hidden; session count hidden; coach card hidden (isActive=false); questions grid shown normally |
| Sessions but no coach analysis yet | Bar chart shown; coach card shows "Get strategic coaching" state |
| Coach analysis with no summary | Pull quote omitted; dimension chips + "Read full analysis →" still shown |
| Coach analysis with no suggested question | Hero card hidden; grid starts immediately |
| Zero questions | Ghost tile shown alone in an otherwise empty grid |
