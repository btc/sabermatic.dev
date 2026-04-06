# Local Session Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make interview sessions function correctly in local development by fixing the dev seed script (no free grant) and wiring evaluation scores into the session list UI.

**Architecture:** Two independent fixes. Fix 1 adds a `cmd/drillctl` CLI that seeds dev users via the production signup path (pool-direct, no full Backend). Fix 2 adds `score_overall` to `SessionSummary` via a LEFT JOIN on evaluations, then wires it through proto → sqlc → RPC → frontend.

**Tech Stack:** Go, urfave/cli v3, pgx, sqlc, buf/protobuf, ConnectRPC, React, TypeScript, recharts

**Spec:** `docs/superpowers/specs/2026-04-05-local-session-fixes-design.md`

---

## File Map

### Fix 1: `drillctl` + question migration

| Action | File | Responsibility |
|--------|------|---------------|
| Create | `internal/migrate/migrate.go` | Shared migration runner (extracted from `cmd/drill/main.go`) |
| Create | `cmd/drillctl/main.go` | CLI entrypoint with `seed` subcommand |
| Create | `sql/migrations/008_seed_questions.up.sql` | Idempotent question INSERT (moved from `seed/questions.sql`) |
| Create | `sql/migrations/008_seed_questions.down.sql` | Remove seed questions (FK-safe) |
| Modify | `cmd/drill/main.go` | Replace local `runMigrations()` with `migrate.Run()` |
| Modify | `Makefile` | Update `seed` target, add `deps` for urfave/cli |
| Delete | `scripts/dev-seed.sh` | Replaced by `drillctl seed` |
| Delete | `seed/questions.sql` | Moved to migration 008 |

### Fix 2: Score wiring

| Action | File | Responsibility |
|--------|------|---------------|
| Modify | `pb/drill/v1/session.proto` | Add `optional int32 score_overall = 13` to `SessionSummary` |
| Modify | `sql/queries/sessions.sql` | LEFT JOIN evaluations in `ListSessionsByUser` |
| Modify | `internal/rpc/session/server.go` | Map nullable score in `listSessionRowToProto` |
| Modify | `internal/rpc/session/server_test.go` | Test score_overall returned for reviewed sessions |
| Modify | `web/src/pages/history.tsx` | Real scores, trend chart, sort-by-score |
| Modify | `web/src/pages/home.tsx` | Implement `ScoreSparkline` component |
| Regen  | `internal/pb/`, `web/src/pb/` | `buf generate` output |
| Regen  | `internal/db/sessions.sql.go` | `sqlc generate` output |

---

## Task 1: Extract shared migration runner

**Files:**
- Create: `internal/migrate/migrate.go`
- Modify: `cmd/drill/main.go:151-172`

- [ ] **Step 1: Create `internal/migrate/migrate.go`**

```go
package migrate

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/btc/drill/sql/migrations"
)

// Run applies all pending database migrations. Safe to call on every startup.
func Run(databaseURL string) error {
	d, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("create migration source: %w", err)
	}

	trimmed := strings.TrimPrefix(databaseURL, "postgresql://")
	trimmed = strings.TrimPrefix(trimmed, "postgres://")
	pgxURL := "pgx5://" + trimmed
	m, err := migrate.NewWithSourceInstance("iofs", d, pgxURL)
	if err != nil {
		return fmt.Errorf("create migrate: %w", err)
	}

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("migrate up: %w", err)
	}

	version, dirty, _ := m.Version()
	slog.Info("migrations complete", "version", version, "dirty", dirty)
	return nil
}
```

- [ ] **Step 2: Update `cmd/drill/main.go` to use shared runner**

Replace the `runMigrations` function call and definition. In `runWithContext`, change:

```go
if err := runMigrations(cfg.Database.URL); err != nil {
```
to:
```go
if err := migrate.Run(cfg.Database.URL); err != nil {
```

Add import `"github.com/btc/drill/internal/migrate"`.

Delete the entire `runMigrations` function (lines 151-172).

Remove the now-unused imports:
- `"github.com/golang-migrate/migrate/v4"`
- `_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"`
- `"github.com/golang-migrate/migrate/v4/source/iofs"`
- `"github.com/btc/drill/sql/migrations"`

Also remove `"strings"` if it's only used by the deleted function (check — it's not used elsewhere in main.go).

- [ ] **Step 3: Verify it compiles**

Run: `go build ./cmd/drill/...`
Expected: Success, no errors.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/... ./cmd/... -short -race -count=1`
Expected: All pass. Migrations still run via the shared package.

- [ ] **Step 5: Commit**

```bash
git add internal/migrate/migrate.go cmd/drill/main.go
git commit -m "refactor: extract shared migration runner to internal/migrate"
```

---

## Task 2: Add question seed migration

**Files:**
- Create: `sql/migrations/008_seed_questions.up.sql`
- Create: `sql/migrations/008_seed_questions.down.sql`
- Delete: `seed/questions.sql`

- [ ] **Step 1: Create `sql/migrations/008_seed_questions.up.sql`**

Copy the content from `seed/questions.sql` verbatim — it's already idempotent with the `IF NOT EXISTS` guard:

```sql
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM questions WHERE source = 'seed') THEN
        INSERT INTO questions (title, prompt, difficulty, tags, hints, source) VALUES
        ('URL Shortener', 'Design a URL shortening service.', 'medium',
         ARRAY['read-heavy','hashing','storage','web'],
         'How do you generate short URLs? Hash collisions? Read vs write ratio and caching strategy. Analytics and click tracking. Custom short URLs and expiration.', 'seed'),
        ('News Feed', 'Design a social media news feed.', 'hard',
         ARRAY['read-heavy','fanout','caching','ranking','social'],
         'Push vs pull model for feed generation. Ranking and relevance algorithms. Celebrity/influencer problem (fanout). Real-time updates vs polling.', 'seed'),
        ('Chat System', 'Design a real-time chat application.', 'medium',
         ARRAY['real-time','websocket','messaging','presence'],
         '1:1 vs group chat differences. Online presence and typing indicators. Message delivery guarantees and ordering. Offline message handling.', 'seed'),
        ('Rate Limiter', 'Design a rate limiting service.', 'medium',
         ARRAY['distributed','algorithms','middleware','reliability'],
         'Token bucket vs sliding window vs fixed window. Distributed rate limiting across multiple servers. Different rate limit tiers. Client identification.', 'seed'),
        ('Web Crawler', 'Design a web crawler.', 'hard',
         ARRAY['distributed','storage','graph','deduplication'],
         'URL frontier and prioritization. Politeness and robots.txt. Deduplication of content. Distributed crawling coordination.', 'seed'),
        ('Notification System', 'Design a notification delivery system.', 'medium',
         ARRAY['distributed','messaging','push','email'],
         'Multiple channels (push, email, SMS, in-app). Delivery guarantees and retry. User preferences and throttling. Template management.', 'seed'),
        ('Video Streaming', 'Design a video streaming platform.', 'hard',
         ARRAY['streaming','cdn','encoding','storage'],
         'Video encoding and adaptive bitrate. CDN and edge caching. Upload pipeline and processing. Live vs on-demand streaming.', 'seed'),
        ('Search Autocomplete', 'Design a search autocomplete system.', 'medium',
         ARRAY['search','trie','caching','ranking'],
         'Trie vs other data structures. Ranking suggestions by popularity. Personalization. Handling misspellings.', 'seed'),
        ('Distributed Cache', 'Design a distributed caching system.', 'hard',
         ARRAY['distributed','caching','consistency','partitioning'],
         'Consistent hashing. Cache eviction policies. Cache invalidation strategies. Replication and failover.', 'seed'),
        ('File Storage', 'Design a cloud file storage service like Dropbox.', 'hard',
         ARRAY['storage','sync','deduplication','distributed'],
         'File chunking and deduplication. Sync conflict resolution. Metadata vs content storage separation. Sharing and permissions.', 'seed'),
        ('Ride Sharing', 'Design a ride-sharing service like Uber.', 'hard',
         ARRAY['real-time','geospatial','matching','pricing'],
         'Driver-rider matching algorithm. Real-time location tracking. Surge pricing. ETA calculation.', 'seed'),
        ('Task Queue', 'Design a distributed task queue.', 'medium',
         ARRAY['distributed','queue','reliability','scheduling'],
         'At-least-once vs exactly-once delivery. Priority queues. Dead letter queues. Delayed/scheduled tasks.', 'seed'),
        ('E-Commerce', 'Design an e-commerce platform.', 'hard',
         ARRAY['transactions','inventory','search','payments'],
         'Inventory management and race conditions. Product search and filtering. Shopping cart. Order processing pipeline.', 'seed'),
        ('Metrics System', 'Design a metrics collection and alerting system.', 'hard',
         ARRAY['time-series','aggregation','alerting','streaming'],
         'Time-series data storage. Aggregation at different granularities. Alert evaluation engine. High cardinality labels.', 'seed'),
        ('Key-Value Store', 'Design a distributed key-value store.', 'medium',
         ARRAY['distributed','storage','consistency','replication'],
         'Partitioning strategy. Replication and consistency model. Read/write paths. Failure detection and recovery.', 'seed'),
        ('Social Graph', 'Design a social graph service.', 'medium',
         ARRAY['graph','social','caching','relationships'],
         'Friend recommendations. Graph traversal for mutual friends. Fan-out for activity feeds. Privacy controls.', 'seed'),
        ('Payment System', 'Design a payment processing system.', 'hard',
         ARRAY['transactions','reliability','security','ledger'],
         'Idempotency in payment processing. Double-entry bookkeeping. Payment gateway integration. Fraud detection.', 'seed'),
        ('Content Moderation', 'Design a content moderation system.', 'medium',
         ARRAY['ml','queue','review','policy'],
         'Automated vs human review pipeline. Priority and escalation. Appeals process. Policy versioning.', 'seed');
    END IF;
END $$;
```

- [ ] **Step 2: Create `sql/migrations/008_seed_questions.down.sql`**

```sql
DELETE FROM questions
WHERE source = 'seed'
  AND NOT EXISTS (
    SELECT 1 FROM interview_sessions WHERE question_id = questions.id
  );
```

- [ ] **Step 3: Delete `seed/questions.sql`**

```bash
rm seed/questions.sql
rmdir seed  # only if empty
```

- [ ] **Step 4: Verify migrations run**

Run: `go test ./internal/... -short -race -count=1 -run TestMain`
Expected: Tests pass (migrations run on test DB setup).

- [ ] **Step 5: Commit**

```bash
git add sql/migrations/008_seed_questions.up.sql sql/migrations/008_seed_questions.down.sql
git rm seed/questions.sql
git commit -m "feat: move question seeding into migration 008"
```

---

## Task 3: Create `cmd/drillctl` with `seed` command

**Files:**
- Create: `cmd/drillctl/main.go`
- Modify: `Makefile`
- Delete: `scripts/dev-seed.sh`

- [ ] **Step 1: Create `cmd/drillctl/main.go`**

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/urfave/cli/v3"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/billing"
	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/migrate"
)

func main() {
	cmd := &cli.Command{
		Name:  "drillctl",
		Usage: "Drill administration CLI",
		Commands: []*cli.Command{
			seedCmd(),
		},
	}

	if err := cmd.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %s\n", err)
		os.Exit(1)
	}
}

func seedCmd() *cli.Command {
	return &cli.Command{
		Name:  "seed",
		Usage: "Create dev user with free trial grant",
		Action: func(ctx context.Context, _ *cli.Command) error {
			return runSeed(ctx)
		},
	}
}

func runSeed(ctx context.Context) error {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}

	// Run migrations first.
	if err := migrate.Run(databaseURL); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	// Open a pool.
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer pool.Close()

	const (
		email       = "dev@drill.dev"
		password    = "devdevdev123"
		displayName = "Dev User"
		bcryptCost  = 10 // Fast for dev.
	)

	// Hash password.
	hash, err := auth.HashPassword(password, bcryptCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	// Transaction: create user + provision free grant.
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	queries := db.New(tx)
	user, err := queries.CreateUser(ctx, db.CreateUserParams{
		Email:        email,
		PasswordHash: pgtype.Text{String: hash, Valid: true},
		DisplayName:  displayName,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			slog.Info("dev user already exists, skipping", "email", email)
			return nil
		}
		return fmt.Errorf("create user: %w", err)
	}

	err = queries.EnsureFreeGrant(ctx, db.EnsureFreeGrantParams{
		UserID:         user.ID,
		InitialMinutes: int32(billing.FreeTrialMinutes()),
		ExpiresAt:      pgtype.Timestamptz{Time: billing.FreeGrantExpiry(), Valid: true},
	})
	if err != nil {
		return fmt.Errorf("provision free grant: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	slog.Info("dev user created",
		"email", email,
		"password", password,
		"display_name", displayName,
		"free_minutes", billing.FreeTrialMinutes(),
	)
	return nil
}
```

- [ ] **Step 2: Update Makefile `seed` target**

Replace the existing `seed` target:

```makefile
seed:
	set -a && . ./.env && set +a && go run ./cmd/drillctl seed
```

- [ ] **Step 3: Delete `scripts/dev-seed.sh`**

```bash
rm scripts/dev-seed.sh
```

- [ ] **Step 4: Verify it compiles**

Run: `go build ./cmd/drillctl/...`
Expected: Success.

- [ ] **Step 5: Run `go mod tidy`**

Run: `go mod tidy`
Expected: `go.mod` and `go.sum` updated with urfave/cli v3.

- [ ] **Step 6: Commit**

```bash
git add cmd/drillctl/main.go Makefile go.mod go.sum
git rm scripts/dev-seed.sh
git commit -m "feat: add drillctl CLI with seed command

Replaces scripts/dev-seed.sh. Creates dev user via production
signup path (hash + create user + free grant) and handles
duplicate-email gracefully for idempotency."
```

---

## Task 4: Add `score_overall` to proto and regenerate

**Files:**
- Modify: `pb/drill/v1/session.proto`
- Regen: `internal/pb/`, `web/src/pb/`

- [ ] **Step 1: Add field to `SessionSummary`**

In `pb/drill/v1/session.proto`, add after `string question_title = 12;`:

```protobuf
  optional int32 score_overall = 13;
```

- [ ] **Step 2: Run `buf generate`**

Run: `buf generate`
Expected: Regenerated files in `internal/pb/` and `web/src/pb/`.

- [ ] **Step 3: Verify no lint errors**

Run: `buf lint`
Expected: Clean.

- [ ] **Step 4: Commit**

```bash
git add pb/drill/v1/session.proto internal/pb/ web/src/pb/
git commit -m "feat(proto): add score_overall to SessionSummary"
```

---

## Task 5: LEFT JOIN evaluations in SQL + regenerate sqlc

**Files:**
- Modify: `sql/queries/sessions.sql`
- Regen: `internal/db/sessions.sql.go`

- [ ] **Step 1: Update `ListSessionsByUser` query**

Replace the existing `ListSessionsByUser` query in `sql/queries/sessions.sql`:

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

- [ ] **Step 2: Run `sqlc generate`**

Run: `sqlc generate`
Expected: `internal/db/sessions.sql.go` regenerated. `ListSessionsByUserRow` now has `ScoreOverall pgtype.Int4`.

- [ ] **Step 3: Verify it compiles**

Run: `go build ./internal/...`
Expected: Success (the RPC handler still compiles because the new field is zero-valued and not yet read).

- [ ] **Step 4: Commit**

```bash
git add sql/queries/sessions.sql internal/db/sessions.sql.go
git commit -m "feat(sql): LEFT JOIN evaluations in ListSessionsByUser for score_overall"
```

---

## Task 6: Wire score in RPC handler + test

**Files:**
- Modify: `internal/rpc/session/server.go:346-368`
- Modify: `internal/rpc/session/server_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/rpc/session/server_test.go`:

```go
func TestListSessions_ScoreOverall(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	questionID := seedQuestion(t, b)
	srvURL := startSessionServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	// Create a session.
	createResp, err := client.CreateSession(context.Background(), connect.NewRequest(&drillv1.CreateSessionRequest{
		QuestionId:      questionID.String(),
		DurationMinutes: 15,
	}))
	require.NoError(t, err)
	sessionID, err := uuid.Parse(createResp.Msg.Session.Id)
	require.NoError(t, err)

	// Before evaluation: score_overall should be nil.
	listResp, err := client.ListSessions(context.Background(), connect.NewRequest(&drillv1.ListSessionsRequest{}))
	require.NoError(t, err)
	require.Len(t, listResp.Msg.Sessions, 1)
	require.Nil(t, listResp.Msg.Sessions[0].ScoreOverall, "no evaluation yet — score should be nil")

	// Mark session as reviewed and insert an evaluation.
	ctx := context.Background()
	q := db.New(b.Pool())
	err = q.UpdateSessionStatusOnly(ctx, db.UpdateSessionStatusOnlyParams{
		ID: sessionID, Status: "reviewed",
	})
	require.NoError(t, err)

	_, err = q.InsertEvaluation(ctx, db.InsertEvaluationParams{
		SessionID:          sessionID,
		ScoreRequirements:  3,
		ScoreArchitecture:  4,
		ScoreDeepDive:      3,
		ScoreScalability:   4,
		ScoreCommunication: 3,
		ScoreOverall:       4,
		Strengths:          []byte(`["good architecture"]`),
		Gaps:               []byte(`["missed caching"]`),
		Advice:             "Focus on caching strategies",
	})
	require.NoError(t, err)

	// After evaluation: score_overall should be 4.
	listResp2, err := client.ListSessions(context.Background(), connect.NewRequest(&drillv1.ListSessionsRequest{}))
	require.NoError(t, err)
	require.Len(t, listResp2.Msg.Sessions, 1)
	require.NotNil(t, listResp2.Msg.Sessions[0].ScoreOverall, "evaluation exists — score should be present")
	require.Equal(t, int32(4), *listResp2.Msg.Sessions[0].ScoreOverall)
}
```

- [ ] **Step 2: Run the test — verify it fails**

Run: `go test ./internal/rpc/session/ -run TestListSessions_ScoreOverall -v -count=1`
Expected: FAIL — `ScoreOverall` is nil even after inserting the evaluation (handler doesn't map it yet).

- [ ] **Step 3: Update `listSessionRowToProto` in `server.go`**

In `internal/rpc/session/server.go`, in the `listSessionRowToProto` function, add after the `ArchivedAt` block:

```go
	if row.ScoreOverall.Valid {
		v := row.ScoreOverall.Int32
		s.ScoreOverall = &v
	}
```

- [ ] **Step 4: Run the test — verify it passes**

Run: `go test ./internal/rpc/session/ -run TestListSessions_ScoreOverall -v -count=1`
Expected: PASS.

- [ ] **Step 5: Run all session tests**

Run: `go test ./internal/rpc/session/ -v -count=1`
Expected: All pass.

- [ ] **Step 6: Commit**

```bash
git add internal/rpc/session/server.go internal/rpc/session/server_test.go
git commit -m "feat(rpc): wire score_overall into ListSessions response"
```

---

## Task 7: Frontend — history page score display and sorting

**Files:**
- Modify: `web/src/pages/history.tsx`

- [ ] **Step 1: Add `scoreColor` helper**

Add near the top of `history.tsx`, after the existing helpers:

```tsx
function scoreColor(score: number): string {
  if (score >= 4) return "hsl(142 71% 45%)";  // green
  if (score >= 3) return "hsl(48 96% 53%)";   // yellow/amber
  if (score >= 2) return "hsl(25 95% 53%)";   // orange
  return "hsl(0 84% 60%)";                     // red
}
```

- [ ] **Step 2: Update `applySort` for score_high / score_low**

Replace the `score_high` and `score_low` cases:

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

Remove the TODO comment above the cases.

- [ ] **Step 3: Update `ScoreTrendChart` to use real scores**

In the `ScoreTrendChart` component, replace:

```tsx
        // score_overall not yet on Session type — placeholder
        score: 0,
```

with:

```tsx
        score: s.scoreOverall ?? 0,
```

Keep the `hasScores` guard as-is — it correctly suppresses the chart when no sessions have scores. Remove the TODO comments above the component and the `hasScores` line.

- [ ] **Step 4: Update `SessionRow` to show real score**

Replace the score placeholder block:

```tsx
      {/* TODO: display actual scores once ListSessions returns score_overall */}
      {session.status === SessionStatus.REVIEWED && (
        <div className="shrink-0 text-right">
          <span className="text-xs text-muted-foreground">—/5</span>
        </div>
      )}
```

with:

```tsx
      {session.status === SessionStatus.REVIEWED && session.scoreOverall != null && (
        <div className="shrink-0 text-right">
          <span className="text-xs font-medium" style={{ color: scoreColor(session.scoreOverall) }}>
            {session.scoreOverall}/5
          </span>
        </div>
      )}
```

- [ ] **Step 5: Verify frontend builds**

Run: `cd web && npm run build`
Expected: Success, no TypeScript errors.

- [ ] **Step 6: Run frontend lint**

Run: `cd web && npx eslint .`
Expected: Clean (no new warnings).

- [ ] **Step 7: Commit**

```bash
git add web/src/pages/history.tsx
git commit -m "feat(ui): wire real scores into history page"
```

---

## Task 8: Frontend — ScoreSparkline on home page

**Files:**
- Modify: `web/src/pages/home.tsx`

- [ ] **Step 1: Add `scoreColor` helper**

Add near the top of `home.tsx`, after the existing helpers (same function as in history.tsx):

```tsx
function scoreColor(score: number): string {
  if (score >= 4) return "hsl(142 71% 45%)";
  if (score >= 3) return "hsl(48 96% 53%)";
  if (score >= 2) return "hsl(25 95% 53%)";
  return "hsl(0 84% 60%)";
}
```

- [ ] **Step 2: Implement `ScoreSparkline` component**

Replace the TODO comment block at line 77-78 with:

```tsx
import {
  LineChart,
  Line,
  ResponsiveContainer,
  Tooltip as RechartsTooltip,
} from "recharts";
```

Add the recharts imports at the top of the file (merge with existing imports if any recharts imports exist).

Then replace the TODO comment:

```tsx
function ScoreSparkline({ sessions }: { sessions: SessionSummary[] }) {
  const points = useMemo(() => {
    return sessions
      .filter((s) => s.scoreOverall != null)
      .sort((a, b) => {
        const ta = a.createTime ? Number(a.createTime.seconds) : 0;
        const tb = b.createTime ? Number(b.createTime.seconds) : 0;
        return ta - tb;
      })
      .map((s) => ({
        score: s.scoreOverall!,
        label: s.questionTitle || "Session",
      }));
  }, [sessions]);

  if (points.length < 2) return null;

  const lastScore = points[points.length - 1]!.score;

  return (
    <div className="flex items-center gap-3">
      <div className="h-8 w-24">
        <ResponsiveContainer width="100%" height="100%">
          <LineChart data={points}>
            <Line
              type="monotone"
              dataKey="score"
              stroke="hsl(32 95% 44%)"
              strokeWidth={1.5}
              dot={{ r: 2, fill: "hsl(32 95% 44%)", strokeWidth: 0 }}
              isAnimationActive={false}
            />
            <RechartsTooltip
              content={({ active, payload }) => {
                if (!active || !payload?.length) return null;
                const p = payload[0]!.payload as { score: number; label: string };
                return (
                  <div className="rounded border border-border bg-popover px-2 py-1 text-xs shadow">
                    <p className="font-medium">{p.label}</p>
                    <p style={{ color: scoreColor(p.score) }}>{p.score}/5</p>
                  </div>
                );
              }}
            />
          </LineChart>
        </ResponsiveContainer>
      </div>
      <span className="text-sm font-medium" style={{ color: scoreColor(lastScore) }}>
        {lastScore}/5
      </span>
    </div>
  );
}
```

- [ ] **Step 3: Wire `ScoreSparkline` into the page**

In the `SummaryStrip` component, add the sparkline after the session count. Update `SummaryStrip`:

```tsx
function SummaryStrip({ sessions }: { sessions: SessionSummary[] }) {
  const reviewed = reviewedSessions(sessions);
  if (reviewed.length === 0) return null;

  return (
    <div className="flex items-center gap-6 text-sm">
      <div>
        <span className="text-muted-foreground">Sessions completed</span>{" "}
        <span className="font-medium">{reviewed.length}</span>
      </div>
      <ScoreSparkline sessions={reviewed} />
    </div>
  );
}
```

- [ ] **Step 4: Verify frontend builds**

Run: `cd web && npm run build`
Expected: Success, no TypeScript errors.

- [ ] **Step 5: Run frontend lint**

Run: `cd web && npx eslint .`
Expected: Clean.

- [ ] **Step 6: Run frontend tests**

Run: `cd web && npx vitest run`
Expected: All pass.

- [ ] **Step 7: Commit**

```bash
git add web/src/pages/home.tsx
git commit -m "feat(ui): implement ScoreSparkline on home page"
```

---

## Task 9: Final verification

- [ ] **Step 1: Run full backend test suite**

Run: `go test ./internal/... ./cmd/... -race -count=1 -timeout=300s`
Expected: All pass.

- [ ] **Step 2: Run full lint + test suite**

Run: `make test`
Expected: buf lint, buf generate (clean), golangci-lint, frontend lint, frontend tests, backend tests — all pass.

- [ ] **Step 3: Verify the full dev workflow**

This is a manual check. With Postgres running and `.env` configured:

```bash
make seed
make dev
```

1. Open http://localhost:8080
2. Log in with `dev@drill.dev` / `devdevdev123`
3. Verify questions appear on home page
4. Start a session — should succeed (user has 60 free minutes)
5. Check history page — scores display for reviewed sessions, trend chart renders, sort-by-score works
