package backend_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/backend"
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

func TestAcquireSessionLock_DifferentIDsDoNotContend(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	lock1, acquired1, err := b.AcquireSessionLock(ctx, uuid.New())
	require.NoError(t, err)
	require.True(t, acquired1)

	lock2, acquired2, err := b.AcquireSessionLock(ctx, uuid.New())
	require.NoError(t, err)
	assert.True(t, acquired2)

	require.NoError(t, lock1.Release())
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
