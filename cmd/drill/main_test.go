package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/testutil"
)

var pg testutil.PG

func TestMain(m *testing.M) {
	pg = testutil.SharedPostgres()
	pg.RunTests(m)
}

func TestFullStartup(t *testing.T) {
	connStr := pg.NewDatabase(t)

	// Find a free port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	// Set env vars — runWithContext calls config.Load which reads from env.
	t.Setenv("DATABASE_URL", connStr)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	t.Setenv("OPENAI_API_KEY", "sk-test")
	t.Setenv("AUTH_TOKEN_SECRET", "test-secret-at-least-32-bytes-long")
	t.Setenv("GOOGLE_CLOUD_PROJECT", "test-project")
	t.Setenv("SERVER_PORT", fmt.Sprintf("%d", port))

	// Start server with cancellable context
	ctx := context.Background()
	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- runWithContext(runCtx)
	}()

	// Wait for server to become healthy
	healthURL := fmt.Sprintf("http://127.0.0.1:%d/api/health", port)
	require.Eventually(t, func() bool {
		resp, err := http.Get(healthURL)
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 15*time.Second, 100*time.Millisecond, "server did not become healthy")

	// Verify health response
	resp, err := http.Get(healthURL)
	require.NoError(t, err)
	defer resp.Body.Close()

	var body map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Equal(t, "ok", body["status"])
	require.Equal(t, true, body["db"])

	// Trigger graceful shutdown
	runCancel()

	// Wait for shutdown to complete
	select {
	case err := <-errCh:
		if err != nil {
			require.ErrorIs(t, err, http.ErrServerClosed)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("server did not shut down within 10 seconds")
	}
}
