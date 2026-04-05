// Package backendtest provides shared test helpers for seeding backend state.
// It is intended for use by external test packages (e.g. handler_test) that
// need to create users through the real Signup flow.
package backendtest

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/backend"
)

// SeedUser creates a user via the real Signup flow (including free trial grant
// provisioning and verification email enqueue) and returns the user ID.
func SeedUser(t *testing.T, b *backend.Backend) uuid.UUID {
	t.Helper()
	result, err := b.Signup(context.Background(), backend.SignupParams{
		Email:       fmt.Sprintf("test-%s@example.com", uuid.NewString()[:8]),
		Password:    "testpassword123",
		DisplayName: "Test User",
	})
	require.NoError(t, err)
	return result.UserID
}
