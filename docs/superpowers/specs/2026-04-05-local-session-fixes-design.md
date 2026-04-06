# Local Session Fixes Design

Two fixes to make interview sessions function correctly in local development.

## Fix 1: `cmd/drillctl` Admin CLI + Question Migration

### Problem

`scripts/dev-seed.sh` creates users via raw SQL INSERT, bypassing `Backend.Signup()`. This means `provisionNewUser()` never runs and the dev user gets no free trial grant (0 balance). Sessions require a positive minute balance to create, so the dev user cannot start an interview.

Questions are also seeded via a separate `psql` invocation of `seed/questions.sql`, which is an extra manual step outside the migration pipeline.

### Solution

#### New migration: `008_seed_questions`

Move the idempotent question INSERT from `seed/questions.sql` into `sql/migrations/008_seed_questions.up.sql`. Questions are now provisioned automatically when migrations run (which happens on every server start). The down migration deletes seed questions that have no associated sessions:

```sql
DELETE FROM questions WHERE source = 'seed'
  AND NOT EXISTS (SELECT 1 FROM interview_sessions WHERE question_id = questions.id);
```

#### New binary: `cmd/drillctl`

A CLI tool using `github.com/urfave/cli/v3` at `cmd/drillctl/main.go`.

**`drillctl seed` command:**

The command must NOT use `backend.New()` — that spins up the full server stack (River workers, AI clients, storage, email sender), which is inappropriate for a CLI seed tool. Instead, it operates directly on a database pool:

1. Parse `DATABASE_URL` from the environment (the only required env var).
2. Run migrations. The `runMigrations()` function currently lives in `cmd/drill/main.go` (unexported). Extract it to a shared internal package (e.g., `internal/migrate/migrate.go`) so both `cmd/drill` and `cmd/drillctl` can call it.
3. Open a `pgxpool.Pool` directly.
4. Create the dev user in a transaction:
   a. Hash the password via `auth.HashPassword()`.
   b. Insert the user via `queries.CreateUser()`.
   c. Provision the free trial grant via `queries.EnsureFreeGrant()`.
   d. Commit.
5. On duplicate-email error (unique constraint violation), log "dev user already exists, skipping" and continue (idempotent).
6. On success, log credentials for convenience.
7. Close the pool.

Dev credentials:
- Email: `dev@drill.dev`
- Password: `devdevdev123`
- DisplayName: `Dev User`

This avoids spinning up River, AI clients, or enqueuing email jobs. It replicates the essential steps of `Backend.Signup()` (hash + create user + provision grant) without the side effects.

**Makefile changes:**

The `seed` target sources `.env` into the shell environment (same pattern as `dev-air`), since `config.Load()` / env var access requires this:

```makefile
seed:
	set -a && . ./.env && set +a && go run ./cmd/drillctl seed
```

**Deleted files:**

- `scripts/dev-seed.sh`
- `seed/questions.sql`
- `seed/` directory (if empty after removal)

### Dependencies

- `github.com/urfave/cli/v3` added to `go.mod`. Verify v3 API signatures before writing code (per CLAUDE.md: "Verify library API signatures against installed versions before writing plan code blocks").

## Fix 2: Wire `score_overall` into Session List

### Problem

`ListSessions` returns `SessionSummary` which has no score data. Evaluation scores live in the `evaluations` table, joined by `session_id`. The frontend history page has placeholders (`--/5`) and disabled sort-by-score, and the score trend chart never renders because all scores are hardcoded to 0. The home page `ScoreSparkline` is a stub comment.

### Solution

#### Proto: `session.proto`

Add to `SessionSummary`:

```protobuf
optional int32 score_overall = 13;
```

Optional because not all sessions have evaluations (active, cancelled, failed, evaluating). Field number 13 is the next available (current max is 12).

Run `buf generate` to regenerate Go and TypeScript code. Commit generated files in `internal/pb/` and `web/src/pb/`.

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

The LEFT JOIN is safe because `evaluations.session_id` has a UNIQUE constraint (migration 001), guaranteeing at most one evaluation per session — no aggregation needed.

Run `sqlc generate` to regenerate the Go query types and commit the generated code in `internal/db/`. The new `ListSessionsByUserRow` will have `ScoreOverall` as `pgtype.Int4` (nullable int, because the LEFT JOIN can produce NULL even though `evaluations.score_overall` is NOT NULL).

#### RPC handler: `internal/rpc/session/server.go`

Update `listSessionRowToProto` to map the nullable score:

```go
if row.ScoreOverall.Valid {
    v := row.ScoreOverall.Int32
    s.ScoreOverall = &v
}
```

This follows the nil-slice coercion convention: nullable DB fields map to optional proto fields at the proto conversion layer. No changes needed to `Backend.ListSessions()` — it's a pass-through that returns the sqlc row type.

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

Add a `scoreColor` helper: 1-2 red, 3 yellow, 4-5 green.

**`ScoreTrendChart`:** Replace hardcoded `score: 0` with `score: s.scoreOverall ?? 0`. Keep the `hasScores` guard but update it to work with real data — it correctly suppresses the chart when no sessions have scores yet (e.g., all sessions are still evaluating).

**`applySort`:** Implement `score_high` and `score_low`. Sessions without scores sort to the bottom regardless of sort direction:

```tsx
case "score_high":
  return copy.sort((a, b) => {
    const sa = a.scoreOverall ?? -1;
    const sb = b.scoreOverall ?? -1;
    return sb - sa;
  });
case "score_low":
  return copy.sort((a, b) => {
    const sa = a.scoreOverall ?? Infinity;
    const sb = b.scoreOverall ?? Infinity;
    return sa - sb;
  });
```

#### Frontend: `web/src/pages/home.tsx`

**`ScoreSparkline`:** Implement the component at the location of the existing TODO comment (line 77). It receives the list of reviewed sessions already fetched by the home page (`reviewedSessions` variable, used by the existing stats card). Renders a compact sparkline using recharts `LineChart` (already a dependency) with minimal chrome — no axes, no labels, just the line with dots. Only render when there are 2+ reviewed sessions with non-zero scores. Place it inside the stats section, after the session count.

### TODO cleanup

Remove all related TODO comments (search by content, not line number — lines will shift during implementation):
- `history.tsx` — score_high / score_low sort fallback comment
- `history.tsx` — score_overall not returned by ListSessions comment
- `history.tsx` — placeholder score comment
- `history.tsx` — display actual scores TODO
- `home.tsx` — ScoreSparkline stub comment

## Testing

### Fix 1

- `cmd/drillctl`: Test that `seed` command succeeds against a test database, creates a user with a free grant, and is idempotent on second run (duplicate-email is handled gracefully).
- Migration 008: Covered by existing migration test infrastructure (migrations run on every test DB setup).

### Fix 2

- `internal/rpc/session/server_test.go`: Test that `ListSessions` returns `score_overall` for reviewed sessions (insert an evaluation, then list) and nil/absent for sessions without evaluations.
- Frontend: Verify score display, trend chart rendering, sort ordering, and sparkline via manual browser check after implementation.

## Out of Scope

- Email verification gating (tracked in #91)
- `ExportData` RPC implementation
- WebSocket integration tests
- Additional `drillctl` subcommands (future work)
