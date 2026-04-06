package session

import (
	"context"
	"encoding/base64"
	"errors"
	"strconv"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/backend"
	"github.com/btc/drill/internal/db"
	drillv1 "github.com/btc/drill/internal/pb/drill/v1"
	"github.com/btc/drill/internal/pb/drill/v1/drillv1connect"
)

const (
	defaultPageSize = 50
	maxPageSize     = 100
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
		Session: getSessionRowToProto(&row),
	}), nil
}

// ListSessions returns sessions for the authenticated user.
// Follows AIP-132: validates page_size, supports cursor-based pagination.
func (s *Server) ListSessions(
	ctx context.Context,
	req *connect.Request[drillv1.ListSessionsRequest],
) (*connect.Response[drillv1.ListSessionsResponse], error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	// AIP-132: validate and apply page size defaults.
	pageSize := int(req.Msg.PageSize)
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}

	// Decode page token (offset-based cursor for simplicity).
	offset := 0
	if req.Msg.PageToken != "" {
		decoded, err := base64.StdEncoding.DecodeString(req.Msg.PageToken)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid page token"))
		}
		offset, err = strconv.Atoi(string(decoded))
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid page token"))
		}
	}

	rows, err := s.b.ListSessions(ctx, user.ID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("list sessions failed"))
	}

	// Apply pagination over the full result set.
	total := len(rows)
	start := offset
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	page := rows[start:end]

	sessions := make([]*drillv1.SessionSummary, len(page))
	for i := range page {
		sessions[i] = listSessionRowToProto(&page[i])
	}

	var nextPageToken string
	if end < total {
		nextPageToken = base64.StdEncoding.EncodeToString([]byte(strconv.Itoa(end)))
	}

	return connect.NewResponse(&drillv1.ListSessionsResponse{
		Sessions:      sessions,
		NextPageToken: nextPageToken,
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
		Session: interviewSessionToProto(&session),
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

	ids := make([]uuid.UUID, len(req.Msg.SessionIds))
	for i, rawID := range req.Msg.SessionIds {
		id, err := uuid.Parse(rawID)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid session_id"))
		}
		ids[i] = id
	}

	count, err := s.b.ArchiveSessions(ctx, user.ID, ids, req.Msg.Archive)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("archive sessions failed"))
	}

	return connect.NewResponse(&drillv1.ArchiveSessionsResponse{
		UpdatedCount: int32(count),
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

	messages := make([]*drillv1.Message, len(msgs))
	for i := range msgs {
		messages[i] = messageToProto(&msgs[i])
	}

	return connect.NewResponse(&drillv1.GetTranscriptResponse{
		Messages: messages,
	}), nil
}

// ---------------------------------------------------------------------------
// Conversion helpers
// ---------------------------------------------------------------------------

func backendToConnectError(err error) *connect.Error {
	switch {
	case errors.Is(err, backend.ErrSessionNotFound):
		return connect.NewError(connect.CodeNotFound, errors.New("session not found"))
	case errors.Is(err, backend.ErrSessionNotOwned):
		return connect.NewError(connect.CodeNotFound, errors.New("session not found"))
	case errors.Is(err, backend.ErrInvalidDuration):
		return connect.NewError(connect.CodeInvalidArgument, errors.New("invalid duration"))
	case errors.Is(err, backend.ErrDurationExceedsPlan):
		return connect.NewError(connect.CodeInvalidArgument, errors.New("duration exceeds plan limit"))
	case errors.Is(err, backend.ErrQuestionNotFound):
		return connect.NewError(connect.CodeNotFound, errors.New("question not found"))
	case errors.Is(err, backend.ErrConcurrentSessionLimit):
		return connect.NewError(connect.CodeResourceExhausted, errors.New("concurrent session limit reached"))
	case errors.Is(err, backend.ErrInsufficientBalance):
		return connect.NewError(connect.CodeResourceExhausted, errors.New("insufficient minute balance"))
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
func getSessionRowToProto(row *db.GetSessionRow) *drillv1.Session {
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
func interviewSessionToProto(row *db.InterviewSession) *drillv1.Session {
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

// messageToProto converts a db.Message to its proto representation.
// nullable InputMethod and AudioUrl are mapped to optional *string fields.
func messageToProto(m *db.Message) *drillv1.Message {
	msg := &drillv1.Message{
		Id:         m.ID.String(),
		SessionId:  m.SessionID.String(),
		Seq:        m.Seq,
		Role:       m.Role,
		Content:    m.Content,
		CreateTime: timestamppb.New(m.CreatedAt),
	}
	if m.InputMethod.Valid {
		msg.InputMethod = &m.InputMethod.String
	}
	if m.AudioUrl.Valid {
		msg.AudioUrl = &m.AudioUrl.String
	}
	return msg
}

// listSessionRowToProto converts the list summary row to proto.
func listSessionRowToProto(row *db.ListSessionsByUserRow) *drillv1.SessionSummary {
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

