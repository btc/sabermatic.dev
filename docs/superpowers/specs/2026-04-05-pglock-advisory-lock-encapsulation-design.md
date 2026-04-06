# pglock: Advisory Lock Encapsulation

**Date:** 2026-04-05
**Status:** Draft

## Problem

`backend.AcquireSessionLock` returns a raw `*pgxpool.Conn`. The conductor stores it and calls `conn.Release()` on close, which returns the connection to the pool without releasing the Postgres advisory lock. This causes "session already in use" errors on reconnect (#84).

The reverted fix (26e67d3) patched this by duplicating `binary.BigEndian` key derivation inline in `conductor.close()`. That works but spreads the lock protocol across two packages.

## Design

### New package: `internal/pglock/`

A small package that encapsulates the Postgres advisory lock lifecycle.

#### `Lock` struct

```go
type Lock struct {
    conn *pgxpool.Conn
    id   uuid.UUID
}
```

Unexported fields. The UUID is the single source of truth — keys are derived on demand via private helper methods.

#### Private key derivation

```go
func (l *Lock) key1() int32 {
    return int32(binary.BigEndian.Uint32(l.id[:4]))
}

func (l *Lock) key2() int32 {
    return int32(binary.BigEndian.Uint32(l.id[4:8]))
}
```

#### `TryAcquire`

```go
func TryAcquire(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) (*Lock, bool, error)
```

1. Acquires a connection from the pool.
2. Calls `SELECT pg_try_advisory_lock(key1, key2)`.
3. If acquired: returns `(*Lock, true, nil)`.
4. If not acquired: releases connection, returns `(nil, false, nil)`.
5. On error: releases connection, returns `(nil, false, error)`.

#### `Release`

```go
func (l *Lock) Release(ctx context.Context) error
```

1. No-op if receiver is nil or `l.conn` is nil (idempotent).
2. Calls `SELECT pg_advisory_unlock(key1, key2)`.
3. Calls `l.conn.Release()`.
4. Sets `l.conn = nil` to prevent double-release.
5. Returns the unlock error (if any). The connection is always released regardless.

### Changes to `backend/session.go`

`AcquireSessionLock` simplifies to a one-liner delegating to `pglock.TryAcquire`:

```go
func (b *Backend) AcquireSessionLock(ctx context.Context, sessionID uuid.UUID) (*pglock.Lock, error) {
    return pglock.TryAcquire(ctx, b.pool, sessionID)
}
```

The return signature changes from `(*pgxpool.Conn, bool, error)` to `(*pglock.Lock, bool, error)` — same shape, but the conn is now encapsulated.

### Changes to `internal/interview/conductor.go`

- Field: `lockConn *pgxpool.Conn` → `lock *pglock.Lock`
- `Run()`: `lockConn, locked, err := ...` → `lock, locked, err := ...`
- `close()`: replaces `c.lockConn.Release()` / `c.lockConn = nil` with `c.lock.Release(context.Background())`

### Changes to tests

Un-skip `TestWS_Reconnection` and `TestWS_ReconnectAfterMultipleTurns` in `internal/handler/session_ws_test.go`. The advisory lock is now properly released on close, fixing the flaky reconnect behavior.

## Scope

Five files touched:
1. `internal/pglock/pglock.go` — new (~40 lines)
2. `internal/pglock/pglock_test.go` — new (unit test against real Postgres)
3. `internal/backend/session.go` — simplify `AcquireSessionLock`
4. `internal/interview/conductor.go` — use `*pglock.Lock`
5. `internal/handler/session_ws_test.go` — un-skip two tests
