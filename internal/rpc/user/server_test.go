package user_test

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/backend"
	"github.com/btc/drill/internal/backendtest"
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
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startUserServer(t, b)

	client := drillv1connect.NewUserServiceClient(&http.Client{}, srvURL)
	_, err := client.GetMe(context.Background(), connect.NewRequest(&drillv1.GetMeRequest{}))
	require.Error(t, err)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

func TestGetMe_Authenticated(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
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
	require.True(t, resp.Msg.User.CreateTime.AsTime().After(time.Now().Add(-15*time.Minute)),
		"CreateTime should be recent, got %v", resp.Msg.User.CreateTime.AsTime())
}

func TestGetUsage_Unauthenticated(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startUserServer(t, b)

	client := drillv1connect.NewUserServiceClient(&http.Client{}, srvURL)
	_, err := client.GetUsage(context.Background(), connect.NewRequest(&drillv1.GetUsageRequest{}))
	require.Error(t, err)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

func TestGetUsage_Authenticated(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
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

// ---------------------------------------------------------------------------
// UpdateProfile tests
// ---------------------------------------------------------------------------

func TestUpdateProfile_Success(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startUserServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	resp, err := client.UpdateProfile(context.Background(), connect.NewRequest(&drillv1.UpdateProfileRequest{
		User: &drillv1.User{DisplayName: "New Name"},
		UpdateMask: &fieldmaskpb.FieldMask{
			Paths: []string{"display_name"},
		},
	}))
	require.NoError(t, err)
	require.NotNil(t, resp.Msg.User)
	require.Equal(t, "New Name", resp.Msg.User.DisplayName)
	require.NotEmpty(t, resp.Msg.User.Id)
	require.Equal(t, "testuser@example.com", resp.Msg.User.Email)
}

func TestUpdateProfile_EmptyMask(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startUserServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	_, err := client.UpdateProfile(context.Background(), connect.NewRequest(&drillv1.UpdateProfileRequest{
		User:       &drillv1.User{DisplayName: "New Name"},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{}},
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestUpdateProfile_UnknownField(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startUserServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	_, err := client.UpdateProfile(context.Background(), connect.NewRequest(&drillv1.UpdateProfileRequest{
		User: &drillv1.User{DisplayName: "New Name"},
		UpdateMask: &fieldmaskpb.FieldMask{
			Paths: []string{"bogus"},
		},
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestUpdateProfile_UnsupportedField(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startUserServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	_, err := client.UpdateProfile(context.Background(), connect.NewRequest(&drillv1.UpdateProfileRequest{
		User: &drillv1.User{},
		UpdateMask: &fieldmaskpb.FieldMask{
			Paths: []string{"email"},
		},
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestUpdateProfile_EmptyDisplayName(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startUserServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	_, err := client.UpdateProfile(context.Background(), connect.NewRequest(&drillv1.UpdateProfileRequest{
		User: &drillv1.User{DisplayName: ""},
		UpdateMask: &fieldmaskpb.FieldMask{
			Paths: []string{"display_name"},
		},
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestUpdateProfile_Unauthenticated(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startUserServer(t, b)

	client := drillv1connect.NewUserServiceClient(&http.Client{}, srvURL)
	_, err := client.UpdateProfile(context.Background(), connect.NewRequest(&drillv1.UpdateProfileRequest{
		User: &drillv1.User{DisplayName: "New Name"},
		UpdateMask: &fieldmaskpb.FieldMask{
			Paths: []string{"display_name"},
		},
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

// ---------------------------------------------------------------------------
// AckKeptBanner tests
// ---------------------------------------------------------------------------

func TestAckKeptBanner_ClearsFlag(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	ctx := context.Background()
	userID := backendtest.SeedUser(t, b)

	// Set pending_kept_banner=TRUE directly so we can test clearing it.
	_, err := b.Pool().Exec(ctx,
		`UPDATE users SET pending_kept_banner = TRUE WHERE id = $1`, userID)
	require.NoError(t, err)

	// Call handler with an authenticated context.
	authUser := &auth.AuthUser{ID: userID}
	authCtx := auth.WithUser(ctx, authUser)

	s := user.NewServer(b)
	_, err = s.AckKeptBanner(authCtx, connect.NewRequest(&drillv1.AckKeptBannerRequest{}))
	require.NoError(t, err)

	// Verify the flag is cleared in the database.
	var banner bool
	err = b.Pool().QueryRow(ctx,
		`SELECT pending_kept_banner FROM users WHERE id = $1`, userID).Scan(&banner)
	require.NoError(t, err)
	require.False(t, banner, "banner must be cleared after AckKeptBanner")
}

func TestAckKeptBanner_RequiresAuth(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	s := user.NewServer(b)

	// Call without an auth context.
	_, err := s.AckKeptBanner(context.Background(), connect.NewRequest(&drillv1.AckKeptBannerRequest{}))
	require.Error(t, err)

	var connErr *connect.Error
	require.ErrorAs(t, err, &connErr)
	require.Equal(t, connect.CodeUnauthenticated, connErr.Code())
}
