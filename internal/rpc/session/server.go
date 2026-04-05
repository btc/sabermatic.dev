package session

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/backend"
	"github.com/btc/drill/internal/db"
	drillv1 "github.com/btc/drill/internal/pb/drill/v1"
	"github.com/btc/drill/internal/pb/drill/v1/drillv1connect"
)

// Server implements the SessionService Connect handler.
type Server struct {
	b *backend.Backend
}

var _ drillv1connect.SessionServiceHandler = (*Server)(nil)

// NewServer creates a new SessionService handler.
func NewServer(b *backend.Backend) *Server {
	return &Server{b: b}
}

// GetSession returns a single session by ID for the authenticated user.
func (s *Server) GetSession(
	ctx context.Context,
	req *connect.Request[drillv1.GetSessionRequest],
) (*connect.Response[drillv1.GetSessionResponse], error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	id, err := uuid.Parse(req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid session id"))
	}

	row, err := s.b.GetSessionForUser(ctx, id, user.ID)
	if err != nil {
		return nil, backendToConnectError(err)
	}

	return connect.NewResponse(&drillv1.GetSessionResponse{
		Session: getSessionRowToProto(row),
	}), nil
}

// ListSessions returns all sessions for the authenticated user.
func (s *Server) ListSessions(
	ctx context.Context,
	req *connect.Request[drillv1.ListSessionsRequest],
) (*connect.Response[drillv1.ListSessionsResponse], error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	rows, err := s.b.ListSessions(ctx, user.ID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("list sessions failed"))
	}

	sessions := make([]*drillv1.SessionSummary, len(rows))
	for i, row := range rows {
		sessions[i] = listSessionRowToProto(row)
	}

	return connect.NewResponse(&drillv1.ListSessionsResponse{
		Sessions: sessions,
	}), nil
}

// CreateSession creates a new interview session.
func (s *Server) CreateSession(
	ctx context.Context,
	req *connect.Request[drillv1.CreateSessionRequest],
) (*connect.Response[drillv1.CreateSessionResponse], error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	questionID, err := uuid.Parse(req.Msg.QuestionId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid question_id"))
	}

	session, err := s.b.CreateSession(ctx, backend.CreateSessionParams{
		UserID:          user.ID,
		QuestionID:      questionID,
		DurationMinutes: int(req.Msg.DurationMinutes),
		TTSEnabled:      req.Msg.TtsEnabled,
		Plan:            user.Plan,
	})
	if err != nil {
		return nil, backendToConnectError(err)
	}

	// CreateSession returns db.InterviewSession (no joined question fields).
	// Return the minimal Session proto with what we have.
	return connect.NewResponse(&drillv1.CreateSessionResponse{
		Session: interviewSessionToProto(session),
	}), nil
}

// ArchiveSessions bulk archives or unarchives sessions.
func (s *Server) ArchiveSessions(
	ctx context.Context,
	req *connect.Request[drillv1.ArchiveSessionsRequest],
) (*connect.Response[drillv1.ArchiveSessionsResponse], error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	ids := make([]uuid.UUID, 0, len(req.Msg.SessionIds))
	for _, raw := range req.Msg.SessionIds {
		id, err := uuid.Parse(raw)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid session_id: "+raw))
		}
		ids = append(ids, id)
	}

	updated, err := s.b.ArchiveSessions(ctx, user.ID, ids, req.Msg.Archive)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("archive sessions failed"))
	}

	return connect.NewResponse(&drillv1.ArchiveSessionsResponse{
		UpdatedCount: int32(updated),
	}), nil
}

// GetTranscript returns messages for a session.
func (s *Server) GetTranscript(
	ctx context.Context,
	req *connect.Request[drillv1.GetTranscriptRequest],
) (*connect.Response[drillv1.GetTranscriptResponse], error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	sessionID, err := uuid.Parse(req.Msg.SessionId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid session_id"))
	}

	msgs, err := s.b.GetTranscript(ctx, sessionID, user.ID)
	if err != nil {
		return nil, backendToConnectError(err)
	}

	protoMsgs := make([]*drillv1.Message, len(msgs))
	for i, msg := range msgs {
		protoMsgs[i] = messageToProto(msg)
	}

	return connect.NewResponse(&drillv1.GetTranscriptResponse{
		Messages: protoMsgs,
	}), nil
}

// ---------------------------------------------------------------------------
// Conversion helpers
// ---------------------------------------------------------------------------

func backendToConnectError(err error) *connect.Error {
	switch {
	case errors.Is(err, backend.ErrSessionNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, backend.ErrSessionNotOwned):
		return connect.NewError(connect.CodePermissionDenied, err)
	case errors.Is(err, backend.ErrInvalidDuration):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.Is(err, backend.ErrDurationExceedsPlan):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.Is(err, backend.ErrQuestionNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, backend.ErrConcurrentSessionLimit):
		return connect.NewError(connect.CodeResourceExhausted, err)
	case errors.Is(err, backend.ErrInsufficientBalance):
		return connect.NewError(connect.CodeResourceExhausted, err)
	default:
		return connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
}

func statusToProto(s string) drillv1.SessionStatus {
	switch s {
	case "active":
		return drillv1.SessionStatus_SESSION_STATUS_ACTIVE
	case "completed":
		return drillv1.SessionStatus_SESSION_STATUS_COMPLETED
	case "evaluating":
		return drillv1.SessionStatus_SESSION_STATUS_EVALUATING
	case "reviewed":
		return drillv1.SessionStatus_SESSION_STATUS_REVIEWED
	case "evaluation_failed":
		return drillv1.SessionStatus_SESSION_STATUS_EVALUATION_FAILED
	case "failed":
		return drillv1.SessionStatus_SESSION_STATUS_FAILED
	case "cancelled":
		return drillv1.SessionStatus_SESSION_STATUS_CANCELLED
	default:
		return drillv1.SessionStatus_SESSION_STATUS_UNSPECIFIED
	}
}

// getSessionRowToProto converts the full GetSessionRow (with joined question fields) to proto.
func getSessionRowToProto(row db.GetSessionRow) *drillv1.Session {
	s := &drillv1.Session{
		Id:                    row.ID.String(),
		UserId:                row.UserID.String(),
		QuestionId:            row.QuestionID.String(),
		Status:                statusToProto(row.Status),
		ConfigDurationMinutes: row.ConfigDurationMinutes,
		ConfigTtsEnabled:      row.ConfigTtsEnabled,
		ConfigCoachBriefing:   row.ConfigCoachBriefing,
		StartTime:             timestamppb.New(row.StartedAt),
		TurnCount:             row.TurnCount,
		CreateTime:            timestamppb.New(row.CreatedAt),
		UpdateTime:            timestamppb.New(row.UpdatedAt),
		QuestionTitle:         row.QuestionTitle,
		QuestionPrompt:        row.QuestionPrompt,
		QuestionDifficulty:    row.QuestionDifficulty,
	}

	if row.EndedAt.Valid {
		s.EndTime = timestamppb.New(row.EndedAt.Time)
	}
	if row.ArchivedAt.Valid {
		s.ArchiveTime = timestamppb.New(row.ArchivedAt.Time)
	}
	if row.QuestionHints.Valid {
		s.QuestionHints = &row.QuestionHints.String
	}

	return s
}

// interviewSessionToProto converts db.InterviewSession (from CreateSession) to proto.
// This type has no joined question fields.
func interviewSessionToProto(row db.InterviewSession) *drillv1.Session {
	s := &drillv1.Session{
		Id:                    row.ID.String(),
		UserId:                row.UserID.String(),
		QuestionId:            row.QuestionID.String(),
		Status:                statusToProto(row.Status),
		ConfigDurationMinutes: row.ConfigDurationMinutes,
		ConfigTtsEnabled:      row.ConfigTtsEnabled,
		ConfigCoachBriefing:   row.ConfigCoachBriefing,
		StartTime:             timestamppb.New(row.StartedAt),
		TurnCount:             row.TurnCount,
		CreateTime:            timestamppb.New(row.CreatedAt),
		UpdateTime:            timestamppb.New(row.UpdatedAt),
	}

	if row.EndedAt.Valid {
		s.EndTime = timestamppb.New(row.EndedAt.Time)
	}
	if row.ArchivedAt.Valid {
		s.ArchiveTime = timestamppb.New(row.ArchivedAt.Time)
	}

	return s
}

// listSessionRowToProto converts the list summary row to proto.
func listSessionRowToProto(row db.ListSessionsByUserRow) *drillv1.SessionSummary {
	s := &drillv1.SessionSummary{
		Id:                    row.ID.String(),
		UserId:                row.UserID.String(),
		QuestionId:            row.QuestionID.String(),
		Status:                statusToProto(row.Status),
		ConfigDurationMinutes: row.ConfigDurationMinutes,
		ConfigTtsEnabled:      row.ConfigTtsEnabled,
		StartTime:             timestamppb.New(row.StartedAt),
		TurnCount:             row.TurnCount,
		CreateTime:            timestamppb.New(row.CreatedAt),
		QuestionTitle:         row.QuestionTitle,
	}

	if row.EndedAt.Valid {
		s.EndTime = timestamppb.New(row.EndedAt.Time)
	}
	if row.ArchivedAt.Valid {
		s.ArchiveTime = timestamppb.New(row.ArchivedAt.Time)
	}

	return s
}

func messageToProto(msg db.Message) *drillv1.Message {
	m := &drillv1.Message{
		Id:         msg.ID.String(),
		SessionId:  msg.SessionID.String(),
		Seq:        msg.Seq,
		Role:       msg.Role,
		Content:    msg.Content,
		CreateTime: timestamppb.New(msg.CreatedAt),
	}

	if msg.InputMethod.Valid {
		m.InputMethod = &msg.InputMethod.String
	}
	if msg.AudioUrl.Valid {
		m.AudioUrl = &msg.AudioUrl.String
	}

	return m
}
