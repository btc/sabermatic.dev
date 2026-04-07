package interview

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

// Server implements the InterviewService Connect handler.
type Server struct {
	b *backend.Backend
}

var _ drillv1connect.InterviewServiceHandler = (*Server)(nil)

// NewServer creates a new InterviewService handler.
func NewServer(b *backend.Backend) *Server {
	return &Server{b: b}
}

// SubmitTurn processes a candidate turn and streams the interviewer response.
func (s *Server) SubmitTurn(
	ctx context.Context,
	req *connect.Request[drillv1.SubmitTurnRequest],
	stream *connect.ServerStream[drillv1.TurnEvent],
) error {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	sessionID, err := uuid.Parse(req.Msg.GetSessionId())
	if err != nil {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("invalid session_id"))
	}

	if err := s.b.VerifySessionOwnership(ctx, sessionID, user.ID); err != nil {
		return backendToConnectError(err)
	}

	sink := &connectTurnSink{stream: stream}
	if err := s.b.ExecuteTurn(ctx, req.Msg, sink); err != nil {
		if errors.Is(err, backend.ErrTurnNotAcquired) {
			return connect.NewError(connect.CodeFailedPrecondition, errors.New("session is not active or a turn is already in progress"))
		}
		return connect.NewError(connect.CodeInternal, errors.New("turn execution failed"))
	}

	return nil
}

// GetSessionState returns the current session state including messages.
func (s *Server) GetSessionState(
	ctx context.Context,
	req *connect.Request[drillv1.GetSessionStateRequest],
) (*connect.Response[drillv1.GetSessionStateResponse], error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	sessionID, err := uuid.Parse(req.Msg.GetSessionId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid session_id"))
	}

	if err := s.b.VerifySessionOwnership(ctx, sessionID, user.ID); err != nil {
		return nil, backendToConnectError(err)
	}

	resp, err := s.b.GetSessionState(ctx, sessionID, int(req.Msg.GetKnownMessageCount()))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("get session state failed"))
	}

	return connect.NewResponse(resp), nil
}

// EndSession completes the session and enqueues evaluation.
func (s *Server) EndSession(
	ctx context.Context,
	req *connect.Request[drillv1.EndSessionRequest],
) (*connect.Response[drillv1.EndSessionResponse], error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	sessionID, err := uuid.Parse(req.Msg.GetSessionId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid session_id"))
	}

	if err := s.b.VerifySessionOwnership(ctx, sessionID, user.ID); err != nil {
		return nil, backendToConnectError(err)
	}

	if err := s.b.WaitAndCompleteSession(ctx, sessionID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("end session failed"))
	}

	return connect.NewResponse(&drillv1.EndSessionResponse{}), nil
}

// CancelSession cancels the session and refunds unused minutes.
func (s *Server) CancelSession(
	ctx context.Context,
	req *connect.Request[drillv1.CancelSessionRequest],
) (*connect.Response[drillv1.CancelSessionResponse], error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	sessionID, err := uuid.Parse(req.Msg.GetSessionId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid session_id"))
	}

	if err := s.b.VerifySessionOwnership(ctx, sessionID, user.ID); err != nil {
		return nil, backendToConnectError(err)
	}

	if err := s.b.WaitAndCancelSession(ctx, sessionID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("cancel session failed"))
	}

	return connect.NewResponse(&drillv1.CancelSessionResponse{}), nil
}

// backendToConnectError maps backend sentinel errors to Connect error codes.
func backendToConnectError(err error) *connect.Error {
	switch {
	case errors.Is(err, backend.ErrSessionNotFound):
		return connect.NewError(connect.CodeNotFound, errors.New("session not found"))
	case errors.Is(err, backend.ErrSessionNotOwned):
		// Map to NotFound to avoid leaking session existence.
		return connect.NewError(connect.CodeNotFound, errors.New("session not found"))
	case errors.Is(err, backend.ErrSessionNotActive):
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("session is not active"))
	default:
		return connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
}
