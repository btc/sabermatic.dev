package backend

import (
	"context"
	"fmt"
	"hash/fnv"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/drilotel"
)

// SessionLock holds a Postgres advisory lock on a dedicated pooled connection.
// The lock is identified by a UUID; the advisory lock key is an FNV-64 hash.
type SessionLock struct {
	conn *pgxpool.Conn
	id   uuid.UUID
}

func (l *SessionLock) key() int64 {
	h := fnv.New64()
	h.Write(l.id[:]) // fnv.Write never returns an error
	return int64(h.Sum64())
}

// Release unlocks the advisory lock and returns the connection to the pool.
// Safe to call on a nil receiver or after a previous Release (idempotent).
// The connection is always returned to the pool even if the unlock query fails.
func (l *SessionLock) Release() error {
	if l == nil || l.conn == nil {
		return nil
	}
	released, err := db.New(l.conn).PGAdvisoryUnlock(context.Background(), l.key())
	if err == nil && !released {
		slog.Warn("advisory unlock: connection did not hold lock", "id", l.id)
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
	locked, err := db.New(conn).PGTryAdvisoryLock(ctx, l.key())
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
