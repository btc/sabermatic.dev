# SessionLock Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Encapsulate the Postgres advisory lock lifecycle in a `backend.SessionLock` type so the conductor never touches raw connections or key derivation.

**Architecture:** New `SessionLock` struct in `internal/backend/session_lock.go` owns the `pgxpool.Conn` and UUID. `AcquireSessionLock` moves there, `Release()` handles unlock+conn-release. Conductor swaps `*pgxpool.Conn` for `*SessionLock`.

**Tech Stack:** Go, pgx/v5, pgxpool, testify, testutil.SharedPostgres

**Spec:** `docs/superpowers/specs/2026-04-05-pglock-advisory-lock-encapsulation-design.md`

---

### Task 1: Create `SessionLock` type with tests (TDD)

**Files:**
- Create: `internal/backend/session_lock.go`
- Create: `internal/backend/session_lock_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/backend/session_lock_test.go`:

```go
package backend_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAcquireSessionLock_Success(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	id := uuid.New()
	lock, acquired, err := b.AcquireSessionLock(ctx, id)
	require.NoError(t, err)
	assert.True(t, acquired)
	assert.NotNil(t, lock)

	// Clean up.
	require.NoError(t, lock.Release())
}

func TestAcquireSessionLock_Contention(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	id := uuid.New()
	lock1, acquired, err := b.AcquireSessionLock(ctx, id)
	require.NoError(t, err)
	require.True(t, acquired)

	// Second acquire on same ID should fail.
	lock2, acquired2, err := b.AcquireSessionLock(ctx, id)
	require.NoError(t, err)
	assert.False(t, acquired2)
	assert.Nil(t, lock2)

	// Clean up.
	require.NoError(t, lock1.Release())
}

func TestAcquireSessionLock_ReleaseAllowsReacquire(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	id := uuid.New()
	lock1, acquired, err := b.AcquireSessionLock(ctx, id)
	require.NoError(t, err)
	require.True(t, acquired)

	// Release the lock.
	require.NoError(t, lock1.Release())

	// Should now be acquirable again.
	lock2, acquired2, err := b.AcquireSessionLock(ctx, id)
	require.NoError(t, err)
	assert.True(t, acquired2)
	assert.NotNil(t, lock2)

	require.NoError(t, lock2.Release())
}

func TestSessionLock_ReleaseNilSafe(t *testing.T) {
	t.Parallel()

	// Nil receiver.
	var lock *backend.SessionLock
	require.NoError(t, lock.Release())
}

func TestSessionLock_ReleaseIdempotent(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	id := uuid.New()
	lock, acquired, err := b.AcquireSessionLock(ctx, id)
	require.NoError(t, err)
	require.True(t, acquired)

	require.NoError(t, lock.Release())
	// Second release should be a no-op.
	require.NoError(t, lock.Release())
}
```

Note: `TestSessionLock_ReleaseNilSafe` needs an import of `backend` package — add:

```go
import (
	...
	"github.com/btc/drill/internal/backend"
)
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/btc/Projects/src/drill && go test ./internal/backend/ -run 'TestAcquireSessionLock|TestSessionLock_Release' -v -count=1`

Expected: Compilation errors — `SessionLock` type doesn't exist yet, `AcquireSessionLock` returns wrong type.

- [ ] **Step 3: Create `session_lock.go` with `SessionLock` type and updated `AcquireSessionLock`**

Create `internal/backend/session_lock.go`:

```go
package backend

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/btc/drill/internal/drilotel"
)

// SessionLock holds a Postgres advisory lock on a dedicated pooled connection.
// The lock is identified by a UUID; advisory lock keys are derived on demand.
type SessionLock struct {
	conn *pgxpool.Conn
	id   uuid.UUID
}

func (l *SessionLock) key1() int32 {
	return int32(binary.BigEndian.Uint32(l.id[:4]))
}

func (l *SessionLock) key2() int32 {
	return int32(binary.BigEndian.Uint32(l.id[4:8]))
}

// Release unlocks the advisory lock and returns the connection to the pool.
// Safe to call on a nil receiver or after a previous Release (idempotent).
// All operations execute regardless of errors.
func (l *SessionLock) Release() error {
	if l == nil || l.conn == nil {
		return nil
	}
	_, err := l.conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1, $2)", l.key1(), l.key2())
	if err != nil {
		slog.Warn("advisory unlock failed", "id", l.id, "error", err)
	}
	l.conn.Release()
	l.conn = nil
	return err
}

// AcquireSessionLock acquires a Postgres advisory lock for the given session.
// Returns the lock and whether it was acquired. If acquired is false, no lock
// is held and lock is nil.
func (b *Backend) AcquireSessionLock(ctx context.Context, sessionID uuid.UUID) (_ *SessionLock, _ bool, err error) {
	ctx, span := tracer.Start(ctx, "Backend.AcquireSessionLock")
	defer func() { drilotel.End(span, err) }()

	conn, err := b.pool.Acquire(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("acquire lock conn: %w", err)
	}

	l := &SessionLock{conn: conn, id: sessionID}
	var locked bool
	err = conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1, $2)", l.key1(), l.key2()).Scan(&locked)
	if err != nil {
		conn.Release()
		return nil, false, fmt.Errorf("advisory lock query: %w", err)
	}
	if !locked {
		conn.Release()
		return nil, false, nil
	}
	return l, true, nil
}
```

- [ ] **Step 4: Remove `AcquireSessionLock` from `session.go`**

Delete the entire `AcquireSessionLock` method (lines 165–190) and the `"encoding/binary"` import from `internal/backend/session.go`. The import list currently includes `"encoding/binary"` — remove it. Also check if the `// Conductor-facing methods` section divider (lines 161–163) should stay or move; it should stay since `GetMessagesBySession` and other conductor-facing methods remain in `session.go`.

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /Users/btc/Projects/src/drill && go test ./internal/backend/ -run 'TestAcquireSessionLock|TestSessionLock_Release' -v -count=1`

Expected: All 5 tests PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/backend/session_lock.go internal/backend/session_lock_test.go internal/backend/session.go
git commit -m "feat(backend): add SessionLock type with encapsulated advisory lock lifecycle"
```

---

### Task 2: Update conductor to use `SessionLock`

**Files:**
- Modify: `internal/interview/conductor.go`

- [ ] **Step 1: Update the `lockConn` field to `lock`**

In `internal/interview/conductor.go`, change the struct field (line 65):

```go
// Before:
lockConn *pgxpool.Conn

// After:
lock *backend.SessionLock
```

- [ ] **Step 2: Update `Run()` to use new return type**

In `Run()` (lines 106–116), change:

```go
// Before:
lockConn, locked, err := c.backend.AcquireSessionLock(serverCtx, c.sessionID)
if err != nil {
    slog.Error("acquire session lock", "error", err, "session_id", c.sessionID)
    c.ws.Close(websocket.StatusInternalError, "lock error")
    return
}
if !locked {
    c.ws.Close(websocket.StatusPolicyViolation, "session already in use")
    return
}
c.lockConn = lockConn

// After:
lock, locked, err := c.backend.AcquireSessionLock(serverCtx, c.sessionID)
if err != nil {
    slog.Error("acquire session lock", "error", err, "session_id", c.sessionID)
    c.ws.Close(websocket.StatusInternalError, "lock error")
    return
}
if !locked {
    c.ws.Close(websocket.StatusPolicyViolation, "session already in use")
    return
}
c.lock = lock
```

- [ ] **Step 3: Update `close()` to use `SessionLock.Release()`**

In `close()` (lines 594–601), change:

```go
// Before:
func (c *Conductor) close() {
	c.ws.Close(websocket.StatusNormalClosure, "session ended")
	if c.lockConn != nil {
		c.lockConn.Release()
		c.lockConn = nil
	}
}

// After:
func (c *Conductor) close() {
	c.ws.Close(websocket.StatusNormalClosure, "session ended")
	if err := c.lock.Release(); err != nil {
		slog.Error("conductor: release session lock", "error", err, "session_id", c.sessionID)
	}
}
```

No nil check needed — `Release()` is nil-safe.

- [ ] **Step 4: Remove unused `pgxpool` import**

Remove `"github.com/jackc/pgx/v5/pgxpool"` from the import block — it's no longer referenced. The `backend` import is already present.

- [ ] **Step 5: Run compilation check and existing tests**

Run: `cd /Users/btc/Projects/src/drill && go build ./internal/interview/ && go test ./internal/backend/ -count=1`

Expected: Build succeeds. All backend tests pass.

- [ ] **Step 6: Commit**

```bash
git add internal/interview/conductor.go
git commit -m "refactor(conductor): use SessionLock instead of raw pgxpool.Conn"
```

---

### Task 3: Un-skip reconnect tests

**Files:**
- Modify: `internal/handler/session_ws_test.go`

- [ ] **Step 1: Remove skip from `TestWS_Reconnection`**

In `internal/handler/session_ws_test.go`, remove line 511:

```go
t.Skip("TODO: flaky under parallel execution — advisory lock release timing. See #84")
```

- [ ] **Step 2: Remove skip from `TestWS_ReconnectAfterMultipleTurns`**

In `internal/handler/session_ws_test.go`, remove line 1593:

```go
t.Skip("TODO: flaky under parallel execution — advisory lock release timing. See #84")
```

- [ ] **Step 3: Run the reconnect tests**

Run: `cd /Users/btc/Projects/src/drill && go test ./internal/handler/ -run 'TestWS_Reconnect' -v -count=1 -timeout 60s`

Expected: Both `TestWS_Reconnection` and `TestWS_ReconnectAfterMultipleTurns` PASS.

- [ ] **Step 4: Run full test suite**

Run: `cd /Users/btc/Projects/src/drill && go test ./... -count=1 -timeout 120s`

Expected: All tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/handler/session_ws_test.go
git commit -m "test: un-skip reconnect tests, advisory lock now properly released

Closes #84"
```
