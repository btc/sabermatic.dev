package backend

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/drilotel"
)

// UpdateDisplayName updates the display name for the given user. Returns
// ErrUserNotFound if the user does not exist or has been soft-deleted.
func (b *Backend) UpdateDisplayName(ctx context.Context, userID uuid.UUID, displayName string) (_ db.User, err error) {
	ctx, span := tracer.Start(ctx, "Backend.UpdateDisplayName")
	defer func() { drilotel.End(span, err) }()

	user, err := db.New(b.pool).UpdateUserDisplayName(ctx, db.UpdateUserDisplayNameParams{
		ID:          userID,
		DisplayName: displayName,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.User{}, ErrUserNotFound
		}
		return db.User{}, fmt.Errorf("update display name: %w", err)
	}
	return user, nil
}

// ClearKeptBanner clears the pending_kept_banner flag for the given user.
// Idempotent: clearing an already-clear flag is a no-op.
func (b *Backend) ClearKeptBanner(ctx context.Context, userID uuid.UUID) (err error) {
	ctx, span := tracer.Start(ctx, "Backend.ClearKeptBanner")
	defer func() { drilotel.End(span, err) }()
	return db.New(b.pool).ClearKeptBanner(ctx, userID)
}
