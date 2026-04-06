# SessionLock: Advisory Lock Encapsulation

**Date:** 2026-04-05
**Status:** Draft

## Problem

`backend.AcquireSessionLock` returns a raw `*pgxpool.Conn`. The conductor stores it and calls `conn.Release()` on close, which returns the connection to the pool without releasing the Postgres advisory lock. This causes "session already in use" errors on reconnect (#84).

The reverted fix (26e67d3) patched this by duplicating `binary.BigEndian` key derivation inline in `conductor.close()`. That works but spreads the lock protocol across two packages.

## Design

### New type: `backend.SessionLock`

Lives in `internal/backend/session_lock.go`.

#### Struct

```go
type SessionLock struct {
    conn *pgxpool.Conn
    id   uuid.UUID
}
```

Unexported fields. The UUID is the single source of truth — advisory lock keys are derived on demand via private helper methods.

#### Private key derivation

```go
func (l *SessionLock) key1() int32 {
    return int32(binary.BigEndian.Uint32(l.id[:4]))
}

func (l *SessionLock) key2() int32 {
    return int32(binary.BigEndian.Uint32(l.id[4:8]))
}
```

#### `AcquireSessionLock`

```go
func (b *Backend) AcquireSessionLock(ctx context.Context, sessionID uuid.UUID) (*SessionLock, bool, error)
```

1. Acquires a connection from the pool.
2. Derives keys from the UUID.
3. Calls `SELECT pg_try_advisory_lock(key1, key2)`.
4. If acquired: returns `(*SessionLock, true, nil)`.
5. If not acquired: releases connection, returns `(nil, false, nil)`.
6. On error: releases connection, returns `(nil, false, error)`.

Moves from `session.go` to `session_lock.go`.

#### `Release`

```go
func (l *SessionLock) Release() error
```

1. No-op if receiver is nil or `l.conn` is nil (returns nil, idempotent).
2. Calls `SELECT pg_advisory_unlock(key1, key2)` with `context.Background()`.
3. Calls `l.conn.Release()`.
4. Sets `l.conn = nil` to prevent double-release.
5. Returns the unlock error if any. All operations always execute regardless of errors.

No context parameter — unlock must always run, never be cancelled.

### Changes to `internal/interview/conductor.go`

- Field: `lockConn *pgxpool.Conn` → `lock *backend.SessionLock`
- `Run()`: `lockConn, locked, err := ...` → `lock, locked, err := ...`; `c.lockConn = lockConn` → `c.lock = lock`
- `close()`: replaces `c.lockConn.Release()` / `c.lockConn = nil` with `c.lock.Release()`

### Changes to tests

- New `internal/backend/session_lock_test.go` with tests against real Postgres:
  - Acquire succeeds and lock is held
  - Second acquire on same ID returns `(nil, false, nil)` (contention)
  - Release frees the lock, allowing re-acquisition
  - Release is nil-safe and idempotent (double-call)
- Un-skip `TestWS_Reconnection` and `TestWS_ReconnectAfterMultipleTurns` in `internal/handler/session_ws_test.go`

## Scope

Five files touched:
1. `internal/backend/session_lock.go` — new (~50 lines)
2. `internal/backend/session_lock_test.go` — new (tests against real Postgres)
3. `internal/backend/session.go` — remove `AcquireSessionLock` (moved to session_lock.go)
4. `internal/interview/conductor.go` — use `*backend.SessionLock`
5. `internal/handler/session_ws_test.go` — un-skip two tests
