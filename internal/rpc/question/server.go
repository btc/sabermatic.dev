package question

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

// Server implements the QuestionService Connect handler.
type Server struct {
	b *backend.Backend
}

var _ drillv1connect.QuestionServiceHandler = (*Server)(nil)

// NewServer creates a new QuestionService handler.
func NewServer(b *backend.Backend) *Server {
	return &Server{b: b}
}

// ListQuestions returns questions visible to the authenticated user.
// Follows AIP-132: validates page_size, supports cursor-based pagination.
func (s *Server) ListQuestions(
	ctx context.Context,
	req *connect.Request[drillv1.ListQuestionsRequest],
) (*connect.Response[drillv1.ListQuestionsResponse], error) {
	// Defense-in-depth: AuthInterceptor should always populate the user, but
	// guard here for callers in tests that construct the handler without it.
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

	rows, err := s.b.ListQuestions(ctx, user.ID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("list questions failed"))
	}

	// Apply pagination over the full result set.
	// (Backend returns all rows; paginate in-memory. When the dataset grows,
	// push LIMIT/OFFSET into the SQL query.)
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

	questions := make([]*drillv1.Question, len(page))
	for i := range page {
		questions[i] = questionToProto(&page[i])
	}

	var nextPageToken string
	if end < total {
		nextPageToken = base64.StdEncoding.EncodeToString([]byte(strconv.Itoa(end)))
	}

	return connect.NewResponse(&drillv1.ListQuestionsResponse{
		Questions:     questions,
		NextPageToken: nextPageToken,
	}), nil
}

// questionToProto converts a database row to a proto Question message.
func questionToProto(row *db.ListQuestionsForUserRow) *drillv1.Question {
	tags := row.Tags
	if tags == nil {
		tags = []string{}
	}

	q := &drillv1.Question{
		Id:         row.ID.String(),
		Title:      row.Title,
		Prompt:     row.Prompt,
		Difficulty: difficultyToProto(row.Difficulty),
		Tags:       tags,
		Source:     sourceToProto(row.Source),
		CreateTime: timestamppb.New(row.CreatedAt),
	}

	if row.UserID.Valid {
		uid := uuid.UUID(row.UserID.Bytes).String()
		q.UserId = &uid
	}

	if row.Hints.Valid {
		q.Hints = &row.Hints.String
	}

	return q
}

func difficultyToProto(s string) drillv1.Difficulty {
	switch s {
	case "medium":
		return drillv1.Difficulty_DIFFICULTY_MEDIUM
	case "hard":
		return drillv1.Difficulty_DIFFICULTY_HARD
	default:
		return drillv1.Difficulty_DIFFICULTY_UNSPECIFIED
	}
}

func sourceToProto(s string) drillv1.QuestionSource {
	switch s {
	case "seed":
		return drillv1.QuestionSource_QUESTION_SOURCE_SEED
	case "custom":
		return drillv1.QuestionSource_QUESTION_SOURCE_CUSTOM
	case "coach_generated":
		return drillv1.QuestionSource_QUESTION_SOURCE_COACH_GENERATED
	default:
		return drillv1.QuestionSource_QUESTION_SOURCE_UNSPECIFIED
	}
}
