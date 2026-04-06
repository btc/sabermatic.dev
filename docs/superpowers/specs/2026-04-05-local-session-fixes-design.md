# Local Session Fixes Design

Two fixes to make interview sessions function correctly in local development.

## Fix 1: `cmd/drillctl` Admin CLI + Question Migration

### Problem

`scripts/dev-seed.sh` creates users via raw SQL INSERT, bypassing `Backend.Signup()`. This means `provisionNewUser()` never runs and the dev user gets no free trial grant (0 balance). Sessions require a positive minute balance to create, so the dev user cannot start an interview.

Questions are also seeded via a separate `psql` invocation of `seed/questions.sql`, which is an extra manual step outside the migration pipeline.

### Solution

#### New migration: `008_seed_questions`

Move the idempotent question INSERT from `seed/questions.sql` into `sql/migrations/008_seed_questions.up.sql`. Questions are now provisioned automatically when migrations run (which happens on every server start). The down migration deletes rows where `source = 'seed'`.

#### New binary: `cmd/drillctl`

A CLI tool using `urfave/cli` (v3) at `cmd/drillctl/main.go`.

**`drillctl seed` command:**

1. Load config via `config.Load()` (reads `.env`)
2. Run migrations. The `runMigrations()` function currently lives in `cmd/drill/main.go` (unexported). Extract it to a shared internal package (e.g., `internal/migrate/migrate.go`) so both `cmd/drill` and `cmd/drillctl` can call it.
3. Create `Backend` via `backend.New(cfg)`
4. Call `Backend.Signup()` with hardcoded dev credentials:
   - Email: `dev@drill.dev`
   - Password: `devdevdev123`
   - DisplayName: `Dev User`
5. On duplicate-email error, log "dev user already exists, skipping" and continue (idempotent)
6. On success, log credentials for convenience
7. `defer b.Close()` for clean shutdown

**Makefile changes:**

- `seed` target becomes: `go run ./cmd/drillctl seed`

**Deleted files:**

- `scripts/dev-seed.sh`
- `seed/questions.sql`
- `seed/` directory (if empty after removal)

### Dependencies

- `github.com/urfave/cli/v3` added to `go.mod`

## Fix 2: Wire `score_overall` into Session List

### Problem

`ListSessions` returns `SessionSummary` which has no score data. Evaluation scores live in the `evaluations` table, joined by `session_id`. The frontend history page has placeholders (`--/5`) and disabled sort-by-score, and the score trend chart never renders because all scores are hardcoded to 0. The home page `ScoreSparkline` is a stub comment.

### Solution

#### Proto: `session.proto`

Add to `SessionSummary`:

```protobuf
optional int32 score_overall = 13;
```

Optional because not all sessions have evaluations (active, cancelled, failed, evaluating).

Run `buf generate` to regenerate Go and TypeScript code.

#### SQL: `sessions.sql`

Modify `ListSessionsByUser` to LEFT JOIN evaluations:

```sql
-- name: ListSessionsByUser :many
SELECT s.id, s.user_id, s.question_id, s.status, s.config_duration_minutes,
       s.config_tts_enabled, s.started_at, s.ended_at, s.turn_count, s.archived_at,
       s.created_at, q.title AS question_title,
       e.score_overall
FROM interview_sessions s
JOIN questions q ON q.id = s.question_id
LEFT JOIN evaluations e ON e.session_id = s.id
WHERE s.user_id = $1
ORDER BY s.created_at DESC;
```

Run `sqlc generate` to regenerate the Go query types. The new `ListSessionsByUserRow` will have `ScoreOverall` as `pgtype.Int4` (nullable int).

#### RPC handler: `internal/rpc/session/server.go`

Update `listSessionRowToProto` to map the nullable score:

```go
if row.ScoreOverall.Valid {
    v := row.ScoreOverall.Int32
    s.ScoreOverall = &v
}
```

This follows the nil-slice coercion convention: nullable DB fields map to optional proto fields at the proto conversion layer.

#### Frontend: `web/src/pages/history.tsx`

**`SessionRow`:** Replace the `--/5` placeholder with the actual score:

```tsx
{session.status === SessionStatus.REVIEWED && session.scoreOverall !== undefined && (
  <div className="shrink-0 text-right">
    <span className="text-xs" style={{ color: scoreColor(session.scoreOverall) }}>
      {session.scoreOverall}/5
    </span>
  </div>
)}
```

Add a `scoreColor` helper (reuse pattern from `v0/` if applicable, or simple thresholds: 1-2 red, 3 yellow, 4-5 green).

**`ScoreTrendChart`:** Replace hardcoded `score: 0` with `score: s.scoreOverall ?? 0` and remove the `hasScores` early-return guard (scores are now real).

**`applySort`:** Implement `score_high` and `score_low`:

```tsx
case "score_high":
  return copy.sort((a, b) => (b.scoreOverall ?? 0) - (a.scoreOverall ?? 0));
case "score_low":
  return copy.sort((a, b) => (a.scoreOverall ?? 0) - (b.scoreOverall ?? 0));
```

#### Frontend: `web/src/pages/home.tsx`

**`ScoreSparkline`:** Implement the component. It receives the list of reviewed sessions (already fetched by the home page) and renders a compact sparkline of `scoreOverall` values over time. Use recharts `LineChart` (already a dependency) with minimal chrome -- no axes, no labels, just the line with dots. Only render when there are 2+ reviewed sessions with scores.

### TODO cleanup

Remove all related TODO comments:
- `history.tsx:70` — score_high / score_low sort
- `history.tsx:117` — score_overall not returned
- `history.tsx:127` — placeholder score
- `history.tsx:248` — display actual scores
- `home.tsx:77` — ScoreSparkline stub

## Testing

### Fix 1

- `cmd/drillctl`: Test that `seed` command succeeds against a test database, creates a user with a free grant, and is idempotent on second run.
- Migration 008: Covered by existing migration test infrastructure (migrations run on every test DB setup).

### Fix 2

- `internal/rpc/session/server_test.go`: Test that `ListSessions` returns `score_overall` for reviewed sessions and nil for sessions without evaluations.
- Frontend: Verify score display, trend chart, and sort ordering via manual browser check after implementation.

## Out of Scope

- Email verification gating (tracked in #91)
- `ExportData` RPC implementation
- WebSocket integration tests
- Additional `drillctl` subcommands (future work)
