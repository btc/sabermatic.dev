package auth_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/auth"
)

func TestAuthUser_FromContext(t *testing.T) {
	user := &auth.AuthUser{
		ID:    uuid.New(),
		Email: "test@example.com",
		Role:  "candidate",
	}
	ctx := auth.WithUser(context.Background(), user)

	got := auth.UserFromContext(ctx)
	require.NotNil(t, got)
	require.Equal(t, user.ID, got.ID)
	require.Equal(t, user.Email, got.Email)
}

func TestAuthUser_FromContext_Missing(t *testing.T) {
	got := auth.UserFromContext(context.Background())
	require.Nil(t, got)
}
