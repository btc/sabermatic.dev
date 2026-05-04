# Show HN W1 — DB cleanup implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Right-size the Postgres connection pool from 80 to 16 and remove the dead advisory-lock SQL/code that justified the original sizing.

**Architecture:** No new code — pure deletion plus a config default change. The pool reduction is bounded by Cloud SQL `db-g1-small`'s `max_connections=50` (verified via GCP docs). At pool=16 × 3 instances = 48 < 50 with a 2-conn reserve. AI workers continue to hold transactions across LLM calls (out-of-scope refactor); their per-instance peak of ~10 fits within the new pool. Sqlc is the only build-step needed; the codegen drops the orphaned `internal/db/advisory_locks.sql.go` and removes the two methods from `internal/db/querier.go` automatically once the source SQL is deleted.

**Tech Stack:** Go 1.x, pgx/v5, sqlc, Postgres 16 on Cloud SQL.

**Spec:** `docs/superpowers/specs/2026-05-04-show-hn-prep-design.md` (Workstream 1).

---

## File structure

| File | Change |
|---|---|
| `internal/config/config.go` | Modify line 47–53 (Database struct): default 80 → 16; rewrite comment |
| `sql/queries/advisory_locks.sql` | **Delete** |
| `internal/db/advisory_locks.sql.go` | Removed by `sqlc generate` |
| `internal/db/querier.go` | Two methods removed by `sqlc generate` |

No tests added; this removes dead code. The audit step (Task 1) is the only "test" — confirms zero non-test callers exist before deletion.

---

## Tasks

### Task 1: Pre-flight verification

**Files:** none modified — read-only checks.

- [ ] **Step 1: Verify max_connections on prod Cloud SQL is 50**

Connect to prod Cloud SQL (via `cloud-sql-proxy` or `gcloud sql connect`), run:

```sql
SHOW max_connections;
```

Expected: `50`. If anything else, **STOP** — re-read the spec's W1 sizing math and recompute pool/cap before proceeding. (The spec's `db-g1-small=50` claim is verified per GCP docs, but instances created in older cohorts can differ.)

- [ ] **Step 2: Confirm zero non-test callers of the advisory-lock methods**

Run:

```bash
grep -rn "PGTryAdvisoryLock\|PGAdvisoryUnlock" --include='*.go' . 2>/dev/null | grep -v -E '_test\.go|\.worktrees|\.claude'
```

Expected output (only the generated definitions, no callers):

```
internal/db/advisory_locks.sql.go:12:const pGAdvisoryUnlock = `-- name: PGAdvisoryUnlock :one
internal/db/advisory_locks.sql.go:16:func (q *Queries) PGAdvisoryUnlock(ctx context.Context, key int64) (bool, error) {
internal/db/advisory_locks.sql.go:23:const pGTryAdvisoryLock = `-- name: PGTryAdvisoryLock :one
internal/db/advisory_locks.sql.go:27:func (q *Queries) PGTryAdvisoryLock(ctx context.Context, key int64) (bool, error) {
internal/db/querier.go:115:	PGAdvisoryUnlock(ctx context.Context, key int64) (bool, error)
internal/db/querier.go:116:	PGTryAdvisoryLock(ctx context.Context, key int64) (bool, error)
```

If any other file appears, **STOP** — there's a real caller; re-evaluate the spec's "zero callers" assumption.

---

### Task 2: Update config default and comment

**Files:**
- Modify: `internal/config/config.go` (Database struct, lines ~47–53)

- [ ] **Step 1: Edit `internal/config/config.go`**

Replace the existing Database struct:

```go
type Database struct {
	URL string `env:"DATABASE_URL,required"`
	// 80 leaves ~20 connections for superuser, psql, migrations under the
	// Postgres default of 100 max_connections. Each active session holds a
	// dedicated connection for its advisory lock, so this caps concurrent sessions.
	MaxPoolConns int32 `env:"DATABASE_MAX_POOL_SIZE,default=80"`
}
```

with:

```go
type Database struct {
	URL string `env:"DATABASE_URL,required"`
	// Cloud SQL db-g1-small applies a per-tier max_connections of 50 (Cloud SQL
	// overrides the Postgres default of 100; see cloud.google.com/sql/docs/postgres/flags).
	// 16 conns/instance × max 3 instances = 48, leaves 2 conns reserve under the cap.
	// Sized to accommodate up to 10 concurrent AI workers (each holds a tx across
	// the full LLM call duration in evaluate.go/coach.go/educator.go) plus a small
	// HTTP burst margin. Reducing further requires refactoring AI workers to release
	// the tx before the LLM call (tracked as a separate post-Show-HN spec).
	MaxPoolConns int32 `env:"DATABASE_MAX_POOL_SIZE,default=16"`
}
```

- [ ] **Step 2: Verify build**

Run:

```bash
go build ./...
```

Expected: succeeds with no output.

- [ ] **Step 3: Commit**

```bash
git add internal/config/config.go
git commit -m "config: right-size database pool to 16 against db-g1-small max_connections=50"
```

---

### Task 3: Delete advisory-lock SQL source

**Files:**
- Delete: `sql/queries/advisory_locks.sql`

- [ ] **Step 1: Delete the source file**

```bash
rm sql/queries/advisory_locks.sql
```

- [ ] **Step 2: Confirm deletion**

```bash
ls sql/queries/advisory_locks.sql 2>&1
```

Expected: `ls: sql/queries/advisory_locks.sql: No such file or directory`.

---

### Task 4: Regenerate sqlc; verify generated artifacts removed

**Files:**
- Auto-removed by sqlc: `internal/db/advisory_locks.sql.go`
- Auto-modified by sqlc: `internal/db/querier.go` (two method declarations removed)

- [ ] **Step 1: Run sqlc generate**

```bash
sqlc generate
```

Expected: succeeds with no output (or info messages only). If sqlc complains, the source query file might not have been deleted — confirm with `ls sql/queries/`.

- [ ] **Step 2: Confirm `advisory_locks.sql.go` is gone**

```bash
ls internal/db/advisory_locks.sql.go 2>&1
```

Expected: `ls: internal/db/advisory_locks.sql.go: No such file or directory`.

- [ ] **Step 3: Confirm querier interface no longer mentions the methods**

```bash
grep -n "PGTryAdvisoryLock\|PGAdvisoryUnlock" internal/db/querier.go 2>&1
```

Expected: no output (grep exits 1).

- [ ] **Step 4: Verify build still passes**

```bash
go build ./...
```

Expected: succeeds with no output.

- [ ] **Step 5: Verify sqlc diff is clean**

```bash
sqlc diff
```

Expected: exits 0 with no diff output (the generated files match what sqlc would produce from the current sources).

---

### Task 5: Run full test suite

**Files:** none — verification only.

- [ ] **Step 1: Run backend tests**

```bash
go test ./internal/... ./cmd/... -race -count=1 -timeout=300s
```

Expected: all pass. If any test references `PGTryAdvisoryLock` or `PGAdvisoryUnlock`, it was missed in Task 1's grep — investigate before proceeding (the test should also be deleted, since it covers removed code).

- [ ] **Step 2: Run full CI suite**

```bash
make test
```

Expected: all stages pass (buf lint, codegen check, frontend typecheck/lint/tests, backend tests, go mod tidy, golangci-lint).

---

### Task 6: Commit deletion

**Files:** all already modified or deleted; this commit captures them.

- [ ] **Step 1: Stage and commit**

```bash
git add sql/queries/advisory_locks.sql internal/db/advisory_locks.sql.go internal/db/querier.go
git commit -m "db: remove vestigial advisory-lock queries and regenerate"
```

(The first two paths will be staged as deletions.)

- [ ] **Step 2: Confirm clean tree**

```bash
git status
```

Expected: working tree clean (or only unrelated changes).

---

## Done

W1 is complete. Pool default is 16, advisory-lock code is gone, build and tests pass. **W2 (Cloud Run cap bump) gates on this commit being deployed and the pool change visible in production logs (a single line at startup logs the new `MaxConns` value through pgxpool init).**
