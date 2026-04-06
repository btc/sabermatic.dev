package billing_test

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
	drillv1 "github.com/btc/drill/internal/pb/drill/v1"
	"github.com/btc/drill/internal/pb/drill/v1/drillv1connect"
	"github.com/btc/drill/internal/rpc"
	"github.com/btc/drill/internal/rpc/billing"
	"github.com/btc/drill/internal/testutil"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func authedClient(t *testing.T, srvURL string, rawToken string) drillv1connect.BillingServiceClient {
	t.Helper()
	u, err := url.Parse(srvURL)
	require.NoError(t, err)
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	jar.SetCookies(u, []*http.Cookie{{
		Name:  auth.SessionCookieName,
		Value: rawToken,
	}})
	return drillv1connect.NewBillingServiceClient(
		&http.Client{Jar: jar},
		srvURL,
	)
}

func startBillingServer(t *testing.T, b *backend.Backend) string {
	t.Helper()
	_, h := drillv1connect.NewBillingServiceHandler(
		billing.NewServer(b),
		connect.WithInterceptors(rpc.AuthInterceptor(b)),
	)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv.URL
}

// ---------------------------------------------------------------------------
// Auth tests
// ---------------------------------------------------------------------------

func TestCheckout_Unauthenticated(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := testutil.NewTestBackend(t)
	srvURL := startBillingServer(t, b)

	client := drillv1connect.NewBillingServiceClient(&http.Client{}, srvURL)
	_, err := client.Checkout(context.Background(), connect.NewRequest(&drillv1.CheckoutRequest{
		Type: "subscription",
		Plan: "pro",
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

func TestPortal_Unauthenticated(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := testutil.NewTestBackend(t)
	srvURL := startBillingServer(t, b)

	client := drillv1connect.NewBillingServiceClient(&http.Client{}, srvURL)
	_, err := client.Portal(context.Background(), connect.NewRequest(&drillv1.PortalRequest{}))
	require.Error(t, err)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

// ---------------------------------------------------------------------------
// Authenticated error-path tests (Stripe not configured in test env)
// ---------------------------------------------------------------------------

func TestCheckout_Authenticated_StripeNotConfigured(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := testutil.NewTestBackend(t)
	srvURL := startBillingServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	// Stripe is not configured in the test env, so CreateCheckoutSession
	// will fail. We verify the RPC returns an internal error (not unauth).
	_, err := client.Checkout(context.Background(), connect.NewRequest(&drillv1.CheckoutRequest{
		Type: "subscription",
		Plan: "pro",
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeInternal, connect.CodeOf(err))
}

func TestPortal_Authenticated_StripeNotConfigured(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := testutil.NewTestBackend(t)
	srvURL := startBillingServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	// Stripe is not configured in test env so CreatePortalSession returns
	// a generic error before it can check for ErrNoStripeAccount. Verify
	// the RPC returns an internal error (not unauth).
	_, err := client.Portal(context.Background(), connect.NewRequest(&drillv1.PortalRequest{}))
	require.Error(t, err)
	require.Equal(t, connect.CodeInternal, connect.CodeOf(err))
}
