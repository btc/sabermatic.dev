package backend

import (
	"context"
	"encoding/binary"
	"fmt"

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
// The connection is always returned to the pool even if the unlock query fails.
func (l *SessionLock) Release() error {
	if l == nil || l.conn == nil {
		return nil
	}
	_, err := l.conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1, $2)", l.key1(), l.key2())
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
