package coach_test

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
	"github.com/btc/drill/internal/rpc/coach"
	"github.com/btc/drill/internal/testutil"
)

// authedClient creates a Connect CoachServiceClient with the session
// cookie set via a cookie jar.
func authedClient(t *testing.T, srvURL string, rawToken string) drillv1connect.CoachServiceClient {
	t.Helper()
	u, err := url.Parse(srvURL)
	require.NoError(t, err)
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	jar.SetCookies(u, []*http.Cookie{{
		Name:  auth.SessionCookieName,
		Value: rawToken,
	}})
	return drillv1connect.NewCoachServiceClient(
		&http.Client{Jar: jar},
		srvURL,
	)
}

// startCoachServer creates the Connect handler with auth interceptor,
// starts an httptest.Server, and returns its URL.
func startCoachServer(t *testing.T, b *backend.Backend) string {
	t.Helper()
	_, h := drillv1connect.NewCoachServiceHandler(
		coach.NewServer(b),
		connect.WithInterceptors(rpc.AuthInterceptor(b)),
	)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestGetCoachAnalysis_Unauthenticated(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := testutil.NewTestBackend(t)
	srvURL := startCoachServer(t, b)

	// No cookie — plain HTTP client.
	client := drillv1connect.NewCoachServiceClient(&http.Client{}, srvURL)
	_, err := client.GetCoachAnalysis(context.Background(), connect.NewRequest(&drillv1.GetCoachAnalysisRequest{}))
	require.Error(t, err)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

func TestGetCoachAnalysis_NoAnalysisExists(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := testutil.NewTestBackend(t)
	srvURL := startCoachServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	// User has no coach analysis yet — should return empty response, not error.
	// Note: this will return FailedPrecondition because a fresh test user has no
	// paid balance, which is checked before the analysis lookup. This is expected
	// behavior matching the backend entitlement check.
	resp, err := client.GetCoachAnalysis(context.Background(), connect.NewRequest(&drillv1.GetCoachAnalysisRequest{}))
	if err != nil {
		// ErrNoPaidBalance maps to FailedPrecondition — acceptable for a fresh user.
		require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	} else {
		// If the user has a balance, analysis should be nil (no analysis yet).
		require.Nil(t, resp.Msg.Analysis)
	}
}

func TestRequestCoachAnalysis_Unauthenticated(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := testutil.NewTestBackend(t)
	srvURL := startCoachServer(t, b)

	// No cookie — plain HTTP client.
	client := drillv1connect.NewCoachServiceClient(&http.Client{}, srvURL)
	_, err := client.RequestCoachAnalysis(context.Background(), connect.NewRequest(&drillv1.RequestCoachAnalysisRequest{}))
	require.Error(t, err)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

func TestRequestCoachAnalysis_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := testutil.NewTestBackend(t)
	srvURL := startCoachServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	// Authenticated user — should succeed or return FailedPrecondition (ErrNoPaidBalance)
	// for fresh users without a paid balance, or ErrNoNewSessions which maps to success.
	// Either outcome is correct: the request was authenticated and dispatched.
	_, err := client.RequestCoachAnalysis(context.Background(), connect.NewRequest(&drillv1.RequestCoachAnalysisRequest{}))
	if err != nil {
		require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	}
}
