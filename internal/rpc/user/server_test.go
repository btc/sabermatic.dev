package user_test

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
	"github.com/btc/drill/internal/rpc/user"
	"github.com/btc/drill/internal/testutil"
)

func authedClient(t *testing.T, srvURL string, rawToken string) drillv1connect.UserServiceClient {
	t.Helper()
	u, err := url.Parse(srvURL)
	require.NoError(t, err)
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	jar.SetCookies(u, []*http.Cookie{{
		Name:  auth.SessionCookieName,
		Value: rawToken,
	}})
	return drillv1connect.NewUserServiceClient(
		&http.Client{Jar: jar},
		srvURL,
	)
}

func startUserServer(t *testing.T, b *backend.Backend) string {
	t.Helper()
	_, h := drillv1connect.NewUserServiceHandler(
		user.NewServer(b),
		connect.WithInterceptors(rpc.AuthInterceptor(b)),
	)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestGetMe_Unauthenticated(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := testutil.NewTestBackend(t)
	srvURL := startUserServer(t, b)

	client := drillv1connect.NewUserServiceClient(&http.Client{}, srvURL)
	_, err := client.GetMe(context.Background(), connect.NewRequest(&drillv1.GetMeRequest{}))
	require.Error(t, err)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

func TestGetMe_Authenticated(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := testutil.NewTestBackend(t)
	srvURL := startUserServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	resp, err := client.GetMe(context.Background(), connect.NewRequest(&drillv1.GetMeRequest{}))
	require.NoError(t, err)
	require.NotNil(t, resp.Msg.User)
	require.Equal(t, "testuser@example.com", resp.Msg.User.Email)
	require.Equal(t, "Test User", resp.Msg.User.DisplayName)
	require.Equal(t, drillv1.UserRole_USER_ROLE_CANDIDATE, resp.Msg.User.Role)
	require.Equal(t, drillv1.UserPlan_USER_PLAN_FREE, resp.Msg.User.Plan)
	require.NotEmpty(t, resp.Msg.User.Id)
	require.NotNil(t, resp.Msg.User.CreateTime)
}

func TestGetUsage_Unauthenticated(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := testutil.NewTestBackend(t)
	srvURL := startUserServer(t, b)

	client := drillv1connect.NewUserServiceClient(&http.Client{}, srvURL)
	_, err := client.GetUsage(context.Background(), connect.NewRequest(&drillv1.GetUsageRequest{}))
	require.Error(t, err)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

func TestGetUsage_Authenticated(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := testutil.NewTestBackend(t)
	srvURL := startUserServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	resp, err := client.GetUsage(context.Background(), connect.NewRequest(&drillv1.GetUsageRequest{}))
	require.NoError(t, err)
	// EnsureFreeGrantTx creates a free grant for new users, so balance > 0.
	require.Greater(t, resp.Msg.TotalBalance, int32(0))
	require.Len(t, resp.Msg.Grants, 1)
	require.NotNil(t, resp.Msg.RecentActivity)
}
