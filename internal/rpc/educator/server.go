package educator

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/backend"
	drillv1 "github.com/btc/drill/internal/pb/drill/v1"
	"github.com/btc/drill/internal/pb/drill/v1/drillv1connect"
)

// Server implements the EducatorService Connect handler.
type Server struct {
	b *backend.Backend
}

var _ drillv1connect.EducatorServiceHandler = (*Server)(nil)

// NewServer creates a new EducatorService handler.
func NewServer(b *backend.Backend) *Server {
	return &Server{b: b}
}

// GetEducatorAnalysis returns the educator analysis for a session.
func (s *Server) GetEducatorAnalysis(
	ctx context.Context,
	req *connect.Request[drillv1.GetEducatorAnalysisRequest],
) (*connect.Response[drillv1.GetEducatorAnalysisResponse], error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	sessionID, err := uuid.Parse(req.Msg.SessionId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid session_id"))
	}

	resp, err := s.b.GetEducatorAnalysis(ctx, sessionID, user.ID)
	if err != nil {
		return nil, mapBackendError(err)
	}

	return connect.NewResponse(&drillv1.GetEducatorAnalysisResponse{
		Analysis: educatorResponseToProto(resp, sessionID.String()),
	}), nil
}

// RequestEducatorAnalysis enqueues educator content generation for a session.
func (s *Server) RequestEducatorAnalysis(
	ctx context.Context,
	req *connect.Request[drillv1.RequestEducatorAnalysisRequest],
) (*connect.Response[drillv1.RequestEducatorAnalysisResponse], error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	sessionID, err := uuid.Parse(req.Msg.SessionId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid session_id"))
	}

	err = s.b.RequestEducatorAnalysis(ctx, sessionID, user.ID)
	if err != nil {
		// ErrAlreadyExists means analysis is already completed — treat as success.
		if errors.Is(err, backend.ErrAlreadyExists) {
			return connect.NewResponse(&drillv1.RequestEducatorAnalysisResponse{}), nil
		}
		return nil, mapBackendError(err)
	}

	return connect.NewResponse(&drillv1.RequestEducatorAnalysisResponse{}), nil
}

// mapBackendError converts backend sentinel errors to Connect error codes.
func mapBackendError(err error) *connect.Error {
	switch {
	case errors.Is(err, backend.ErrSessionNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, backend.ErrSessionNotOwned):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, backend.ErrEvaluationNotReady):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	case errors.Is(err, backend.ErrNoPaidBalance):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	default:
		return connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
}

// educatorResponseToProto converts a backend EducatorResponse to the proto
// EducatorAnalysis message.
func educatorResponseToProto(resp *backend.EducatorResponse, sessionID string) *drillv1.EducatorAnalysis {
	analysis := &drillv1.EducatorAnalysis{
		Id:        resp.ID,
		SessionId: sessionID,
		Status:    statusToProto(resp.Status),
	}
	if !resp.CreatedAt.IsZero() {
		analysis.CreateTime = timestamppb.New(resp.CreatedAt)
	}
	if resp.ModelAnswer != "" {
		analysis.ModelAnswer = &resp.ModelAnswer
	}
	if resp.GapDeepDives != "" {
		analysis.GapDeepDives = &resp.GapDeepDives
	}
	return analysis
}

// statusToProto converts a backend status string to the proto EducatorStatus enum.
func statusToProto(s string) drillv1.EducatorStatus {
	switch s {
	case "not_requested":
		return drillv1.EducatorStatus_EDUCATOR_STATUS_NOT_REQUESTED
	case "generating":
		return drillv1.EducatorStatus_EDUCATOR_STATUS_GENERATING
	case "completed":
		return drillv1.EducatorStatus_EDUCATOR_STATUS_COMPLETED
	case "failed":
		return drillv1.EducatorStatus_EDUCATOR_STATUS_FAILED
	default:
		return drillv1.EducatorStatus_EDUCATOR_STATUS_UNSPECIFIED
	}
}
