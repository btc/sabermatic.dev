package coach

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/backend"
	drillv1 "github.com/btc/drill/internal/pb/drill/v1"
	"github.com/btc/drill/internal/pb/drill/v1/drillv1connect"
)

// Server implements the CoachService Connect handler.
type Server struct {
	b *backend.Backend
}

var _ drillv1connect.CoachServiceHandler = (*Server)(nil)

// NewServer creates a new CoachService handler.
func NewServer(b *backend.Backend) *Server {
	return &Server{b: b}
}

// GetCoachAnalysis returns the latest coach analysis for the authenticated user.
// Returns an empty response (nil analysis) if no analysis exists yet.
func (s *Server) GetCoachAnalysis(
	ctx context.Context,
	req *connect.Request[drillv1.GetCoachAnalysisRequest],
) (*connect.Response[drillv1.GetCoachAnalysisResponse], error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	resp, err := s.b.GetLatestCoachAnalysis(ctx, user.ID)
	if err != nil {
		if errors.Is(err, backend.ErrNoPaidBalance) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("paid minute balance required"))
		}
		return nil, connect.NewError(connect.CodeInternal, errors.New("get coach analysis failed"))
	}

	// nil means no analysis exists yet — return empty response, not an error.
	if resp == nil {
		return connect.NewResponse(&drillv1.GetCoachAnalysisResponse{}), nil
	}

	return connect.NewResponse(&drillv1.GetCoachAnalysisResponse{
		Analysis: coachResponseToProto(resp),
	}), nil
}

// RequestCoachAnalysis enqueues a new coach analysis job for the authenticated user.
func (s *Server) RequestCoachAnalysis(
	ctx context.Context,
	req *connect.Request[drillv1.RequestCoachAnalysisRequest],
) (*connect.Response[drillv1.RequestCoachAnalysisResponse], error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	err := s.b.RequestCoachAnalysis(ctx, user.ID, req.Msg.Force)
	if err != nil {
		switch {
		case errors.Is(err, backend.ErrNoPaidBalance):
			return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("paid minute balance required"))
		case errors.Is(err, backend.ErrNoNewSessions):
			// Not really an error — analysis is up-to-date. Return success.
			return connect.NewResponse(&drillv1.RequestCoachAnalysisResponse{}), nil
		default:
			return nil, connect.NewError(connect.CodeInternal, errors.New("request coach analysis failed"))
		}
	}

	return connect.NewResponse(&drillv1.RequestCoachAnalysisResponse{}), nil
}

// coachResponseToProto converts a backend CoachResponse to a proto CoachAnalysis message.
func coachResponseToProto(r *backend.CoachResponse) *drillv1.CoachAnalysis {
	ca := &drillv1.CoachAnalysis{
		Id:                  r.ID,
		UserId:              r.UserID,
		Narrative:           r.Narrative,
		ImprovingDimensions: r.ImprovingDimensions,
		TopicGaps:           r.TopicGaps,
		SessionsAnalyzed:    r.SessionsAnalyzed,
		CreateTime:          timestamppb.New(r.CreatedAt),
	}

	if r.Summary != "" {
		ca.Summary = &r.Summary
	}

	if r.WeakestDimension != "" {
		ca.WeakestDimension = &r.WeakestDimension
	}

	if r.SuggestedQuestionID != nil {
		ca.SuggestedQuestionId = r.SuggestedQuestionID
	}

	return ca
}
