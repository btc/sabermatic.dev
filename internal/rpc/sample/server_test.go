package sample_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	drillv1 "github.com/btc/drill/internal/pb/drill/v1"
	"github.com/btc/drill/internal/pb/drill/v1/drillv1connect"
	samplesvc "github.com/btc/drill/internal/rpc/sample"
)

func setup(t *testing.T) drillv1connect.SampleServiceClient {
	t.Helper()
	ss, err := samplesvc.NewSampleService()
	require.NoError(t, err)

	mux := http.NewServeMux()
	mux.Handle(drillv1connect.NewSampleServiceHandler(samplesvc.NewServer(ss)))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return drillv1connect.NewSampleServiceClient(srv.Client(), srv.URL)
}

func TestGetSampleSession(t *testing.T) {
	client := setup(t)
	resp, err := client.GetSampleSession(context.Background(),
		connect.NewRequest(&drillv1.GetSampleSessionRequest{}))
	require.NoError(t, err)
	require.NotNil(t, resp.Msg.Session)
	require.NotEmpty(t, resp.Msg.Messages)
	require.Equal(t, "Chat System", resp.Msg.Session.QuestionTitle)
}

func TestGetSampleEvaluation(t *testing.T) {
	client := setup(t)
	resp, err := client.GetSampleEvaluation(context.Background(),
		connect.NewRequest(&drillv1.GetSampleEvaluationRequest{}))
	require.NoError(t, err)
	require.NotNil(t, resp.Msg.Evaluation)
	require.NotNil(t, resp.Msg.Evaluation.Scores)
	require.NotEmpty(t, resp.Msg.Evaluation.Strengths)
	require.NotEmpty(t, resp.Msg.Evaluation.Annotations)
}

func TestGetSampleEducator(t *testing.T) {
	client := setup(t)
	resp, err := client.GetSampleEducator(context.Background(),
		connect.NewRequest(&drillv1.GetSampleEducatorRequest{}))
	require.NoError(t, err)
	require.NotNil(t, resp.Msg.Analysis)
	require.NotNil(t, resp.Msg.Analysis.ModelAnswer)
}

func TestGetSampleCoach(t *testing.T) {
	client := setup(t)
	resp, err := client.GetSampleCoach(context.Background(),
		connect.NewRequest(&drillv1.GetSampleCoachRequest{}))
	require.NoError(t, err)
	require.NotNil(t, resp.Msg.Analysis)
	require.NotEmpty(t, resp.Msg.ScoreTrend)
}
