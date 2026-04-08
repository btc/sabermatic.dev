// Package testutil provides shared test helpers for integration tests that need
// a real Postgres database, backend, and authenticated sessions.
package testutil

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/backend"
	"github.com/btc/drill/internal/handler"
	drillv1 "github.com/btc/drill/internal/pb/drill/v1"
	"github.com/btc/drill/internal/pb/drill/v1/drillv1connect"
	samplesvc "github.com/btc/drill/internal/rpc/sample"
)

// SignupAndLogin creates a user via ConnectRPC AuthService and returns the raw
// session token string.
func SignupAndLogin(t *testing.T, b *backend.Backend) string {
	t.Helper()
	ss, err := samplesvc.NewSampleService()
	require.NoError(t, err)
	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b, ss))

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	// Signup via ConnectRPC.
	pubClient := drillv1connect.NewAuthServiceClient(&http.Client{}, srv.URL)
	_, err = pubClient.Signup(context.Background(), connect.NewRequest(&drillv1.SignupRequest{
		Email:       "testuser@example.com",
		Password:    "securepass123",
		DisplayName: "Test User",
	}))
	require.NoError(t, err)

	// Login via ConnectRPC with a cookie jar to capture Set-Cookie.
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	cookieClient := drillv1connect.NewAuthServiceClient(&http.Client{Jar: jar}, srv.URL)
	_, err = cookieClient.Login(context.Background(), connect.NewRequest(&drillv1.LoginRequest{
		Email:    "testuser@example.com",
		Password: "securepass123",
	}))
	require.NoError(t, err)

	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	for _, c := range jar.Cookies(u) {
		if c.Name == auth.SessionCookieName {
			return c.Value
		}
	}
	t.Fatal("session cookie not found after login")
	return ""
}
