package sample

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/btc/drill/internal/pb/drill/v1/drillv1connect"
	"github.com/btc/drill/internal/sample"

	drillv1 "github.com/btc/drill/internal/pb/drill/v1"
)

// SampleService holds pre-parsed proto messages for the sample data endpoints.
// It reads embedded fixture JSON at construction time and serves them directly.
type SampleService struct {
	session    *drillv1.GetSampleSessionResponse
	evaluation *drillv1.GetSampleEvaluationResponse
	educator   *drillv1.GetSampleEducatorResponse
	coach      *drillv1.GetSampleCoachResponse
}

// NewSampleService reads embedded fixture JSON files and unmarshals them into
// proto messages. Returns an error if any fixture is missing or malformed.
func NewSampleService() (*SampleService, error) {
	fs := sample.FixtureFS()

	sessionData, err := fs.ReadFile("fixtures/session.json")
	if err != nil {
		return nil, fmt.Errorf("read session fixture: %w", err)
	}
	evalData, err := fs.ReadFile("fixtures/evaluation.json")
	if err != nil {
		return nil, fmt.Errorf("read evaluation fixture: %w", err)
	}
	educatorData, err := fs.ReadFile("fixtures/educator.json")
	if err != nil {
		return nil, fmt.Errorf("read educator fixture: %w", err)
	}
	coachData, err := fs.ReadFile("fixtures/coach.json")
	if err != nil {
		return nil, fmt.Errorf("read coach fixture: %w", err)
	}

	ss := &SampleService{}

	ss.session = &drillv1.GetSampleSessionResponse{}
	if err := protojson.Unmarshal(sessionData, ss.session); err != nil {
		return nil, fmt.Errorf("unmarshal session fixture: %w", err)
	}

	ss.evaluation = &drillv1.GetSampleEvaluationResponse{}
	if err := protojson.Unmarshal(evalData, ss.evaluation); err != nil {
		return nil, fmt.Errorf("unmarshal evaluation fixture: %w", err)
	}

	ss.educator = &drillv1.GetSampleEducatorResponse{}
	if err := protojson.Unmarshal(educatorData, ss.educator); err != nil {
		return nil, fmt.Errorf("unmarshal educator fixture: %w", err)
	}

	ss.coach = &drillv1.GetSampleCoachResponse{}
	if err := protojson.Unmarshal(coachData, ss.coach); err != nil {
		return nil, fmt.Errorf("unmarshal coach fixture: %w", err)
	}

	return ss, nil
}

// Server implements the ConnectRPC SampleServiceHandler interface.
type Server struct {
	ss *SampleService
}

var _ drillv1connect.SampleServiceHandler = (*Server)(nil)

// NewServer creates a new SampleService handler from a pre-constructed
// SampleService.
func NewServer(ss *SampleService) *Server {
	return &Server{ss: ss}
}

// GetSampleSession returns the pre-parsed sample session and its messages.
func (s *Server) GetSampleSession(
	_ context.Context,
	_ *connect.Request[drillv1.GetSampleSessionRequest],
) (*connect.Response[drillv1.GetSampleSessionResponse], error) {
	return connect.NewResponse(s.ss.session), nil
}

// GetSampleEvaluation returns the pre-parsed sample evaluation.
func (s *Server) GetSampleEvaluation(
	_ context.Context,
	_ *connect.Request[drillv1.GetSampleEvaluationRequest],
) (*connect.Response[drillv1.GetSampleEvaluationResponse], error) {
	return connect.NewResponse(s.ss.evaluation), nil
}

// GetSampleEducator returns the pre-parsed sample educator analysis.
func (s *Server) GetSampleEducator(
	_ context.Context,
	_ *connect.Request[drillv1.GetSampleEducatorRequest],
) (*connect.Response[drillv1.GetSampleEducatorResponse], error) {
	return connect.NewResponse(s.ss.educator), nil
}

// GetSampleCoach returns the pre-parsed sample coach analysis and score trend.
func (s *Server) GetSampleCoach(
	_ context.Context,
	_ *connect.Request[drillv1.GetSampleCoachRequest],
) (*connect.Response[drillv1.GetSampleCoachResponse], error) {
	return connect.NewResponse(s.ss.coach), nil
}
