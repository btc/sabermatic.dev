package evaluation

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/backend"
	drillv1 "github.com/btc/drill/internal/pb/drill/v1"
	"github.com/btc/drill/internal/pb/drill/v1/drillv1connect"
)

// Server implements the EvaluationService Connect handler.
type Server struct {
	b *backend.Backend
}

var _ drillv1connect.EvaluationServiceHandler = (*Server)(nil)

// NewServer creates a new EvaluationService handler.
func NewServer(b *backend.Backend) *Server {
	return &Server{b: b}
}

// GetEvaluation returns the evaluation for a session owned by the authenticated user.
func (s *Server) GetEvaluation(
	ctx context.Context,
	req *connect.Request[drillv1.GetEvaluationRequest],
) (*connect.Response[drillv1.GetEvaluationResponse], error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	sessionID, err := uuid.Parse(req.Msg.SessionId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid session_id"))
	}

	eval, err := s.b.GetEvaluation(ctx, sessionID, user.ID)
	if err != nil {
		return nil, mapEvaluationError(err)
	}

	return connect.NewResponse(&drillv1.GetEvaluationResponse{
		Evaluation: evaluationToProto(eval),
	}), nil
}

// RetryEvaluation re-enqueues evaluation for a failed session.
func (s *Server) RetryEvaluation(
	ctx context.Context,
	req *connect.Request[drillv1.RetryEvaluationRequest],
) (*connect.Response[drillv1.RetryEvaluationResponse], error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	sessionID, err := uuid.Parse(req.Msg.SessionId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid session_id"))
	}

	if err := s.b.RetryEvaluation(ctx, sessionID, user.ID); err != nil {
		return nil, mapRetryError(err)
	}

	return connect.NewResponse(&drillv1.RetryEvaluationResponse{}), nil
}

// mapEvaluationError maps backend errors to Connect error codes for GetEvaluation.
func mapEvaluationError(err error) error {
	switch {
	case errors.Is(err, backend.ErrSessionNotFound):
		return connect.NewError(connect.CodeNotFound, errors.New("session not found"))
	case errors.Is(err, backend.ErrSessionNotOwned):
		// Don't leak existence — return NotFound.
		return connect.NewError(connect.CodeNotFound, errors.New("session not found"))
	case errors.Is(err, backend.ErrEvaluationNotReady):
		return connect.NewError(connect.CodeNotFound, errors.New("evaluation not ready"))
	default:
		return connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
}

// mapRetryError maps backend errors to Connect error codes for RetryEvaluation.
func mapRetryError(err error) error {
	switch {
	case errors.Is(err, backend.ErrSessionNotFound):
		return connect.NewError(connect.CodeNotFound, errors.New("session not found"))
	case errors.Is(err, backend.ErrSessionNotOwned):
		// Don't leak existence — return NotFound.
		return connect.NewError(connect.CodeNotFound, errors.New("session not found"))
	case errors.Is(err, backend.ErrNotEvaluationFailed):
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("session is not in evaluation_failed status"))
	default:
		return connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
}

// evaluationStatusToProto converts a backend evaluation status string to the proto enum.
func evaluationStatusToProto(s string) drillv1.EvaluationStatus {
	switch s {
	case "reviewed":
		return drillv1.EvaluationStatus_EVALUATION_STATUS_REVIEWED
	case "evaluation_failed":
		return drillv1.EvaluationStatus_EVALUATION_STATUS_EVALUATION_FAILED
	default:
		return drillv1.EvaluationStatus_EVALUATION_STATUS_UNSPECIFIED
	}
}

// evaluationToProto converts a backend EvaluationResponse to the proto EvaluationResult.
func evaluationToProto(e *backend.EvaluationResponse) *drillv1.EvaluationResult {
	annotations := make([]*drillv1.Annotation, len(e.Annotations))
	for i, a := range e.Annotations {
		annotations[i] = &drillv1.Annotation{
			MessageSeq: a.MessageSeq,
			Type:       annotationTypeToProto(a.Type),
			Content:    a.Content,
		}
	}

	result := &drillv1.EvaluationResult{
		Status:      evaluationStatusToProto(e.Status),
		Strengths:   e.Strengths,
		Gaps:        e.Gaps,
		Advice:      e.Advice,
		Annotations: annotations,
	}

	if e.Scores != nil {
		result.Scores = &drillv1.EvaluationScores{
			Requirements:  e.Scores.Requirements,
			Architecture:  e.Scores.Architecture,
			DeepDive:      e.Scores.DeepDive,
			Scalability:   e.Scores.Scalability,
			Communication: e.Scores.Communication,
			Overall:       e.Scores.Overall,
		}
	}

	return result
}

// annotationTypeToProto converts a backend annotation type string to the proto enum.
func annotationTypeToProto(s string) drillv1.AnnotationType {
	switch s {
	case "strength":
		return drillv1.AnnotationType_ANNOTATION_TYPE_STRENGTH
	case "gap":
		return drillv1.AnnotationType_ANNOTATION_TYPE_GAP
	case "missed_opportunity":
		return drillv1.AnnotationType_ANNOTATION_TYPE_MISSED_OPPORTUNITY
	case "note":
		return drillv1.AnnotationType_ANNOTATION_TYPE_NOTE
	default:
		return drillv1.AnnotationType_ANNOTATION_TYPE_UNSPECIFIED
	}
}
