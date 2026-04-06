package backend_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewAndClose(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	require.NotNil(t, b.Pool())
	require.NotNil(t, b.Config())

	err := b.Ping(context.Background())
	require.NoError(t, err)

	err = b.Close()
	require.NoError(t, err)
}
