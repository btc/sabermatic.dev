package backend_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/backend"
	"github.com/btc/drill/internal/backendtest"
	"github.com/btc/drill/internal/db"
)

// ---------------------------------------------------------------------------
// UpdateDisplayName
// ---------------------------------------------------------------------------

func TestUpdateDisplayName_Success(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	userID := backendtest.SeedUser(t, b)

	updated, err := b.UpdateDisplayName(ctx, userID, "New Name")
	require.NoError(t, err)
	assert.Equal(t, "New Name", updated.DisplayName)
	assert.Equal(t, userID, updated.ID)

	// Confirm the change persisted.
	stored, err := db.New(b.Pool()).GetUserByID(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, "New Name", stored.DisplayName)
}

func TestUpdateDisplayName_DeletedUser(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	userID := backendtest.SeedUser(t, b)

	// Soft-delete the user first.
	err := b.DeleteAccount(ctx, userID)
	require.NoError(t, err)

	// UpdateDisplayName on a deleted user should return ErrUserNotFound.
	_, err = b.UpdateDisplayName(ctx, userID, "Ghost")
	require.ErrorIs(t, err, backend.ErrUserNotFound)
}
