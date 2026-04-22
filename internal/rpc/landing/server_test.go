package landing_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	drillv1 "github.com/btc/drill/internal/pb/drill/v1"
	"github.com/btc/drill/internal/pb/drill/v1/drillv1connect"
	landingsvc "github.com/btc/drill/internal/rpc/landing"
)

// setup starts a LandingService over httptest.Server with no interceptors —
// the real production posture for a public RPC (see register.go's publicOpts).
func setup(t *testing.T) drillv1connect.LandingServiceClient {
	t.Helper()
	b := pg.NewBackend(t)

	mux := http.NewServeMux()
	mux.Handle(drillv1connect.NewLandingServiceHandler(landingsvc.NewServer(b)))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return drillv1connect.NewLandingServiceClient(srv.Client(), srv.URL)
}

func TestListFeaturedQuestions_HappyPath(t *testing.T) {
	t.Parallel()
	client := setup(t)

	resp, err := client.ListFeaturedQuestions(context.Background(),
		connect.NewRequest(&drillv1.ListFeaturedQuestionsRequest{}))
	require.NoError(t, err)

	require.Len(t, resp.Msg.Questions, 6)
	require.Equal(t, "Video Streaming", resp.Msg.Questions[0].Title)
	require.Equal(t, "News Feed", resp.Msg.Questions[1].Title)
	require.Equal(t, "Ride Sharing", resp.Msg.Questions[2].Title)
	require.Equal(t, "Chat System", resp.Msg.Questions[3].Title)
	require.Equal(t, "Search Autocomplete", resp.Msg.Questions[4].Title)
	require.Equal(t, "Social Graph", resp.Msg.Questions[5].Title)

	require.Equal(t, int32(18), resp.Msg.TotalCount)
}

func TestListFeaturedQuestions_PublicAccess(t *testing.T) {
	t.Parallel()
	client := setup(t)

	// No session cookie on the underlying transport — public RPC must
	// still respond 200 with data.
	resp, err := client.ListFeaturedQuestions(context.Background(),
		connect.NewRequest(&drillv1.ListFeaturedQuestionsRequest{}))
	require.NoError(t, err)
	require.NotNil(t, resp.Msg)
	require.NotEmpty(t, resp.Msg.Questions)
}
