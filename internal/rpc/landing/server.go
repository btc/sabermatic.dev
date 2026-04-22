// Package landing implements the public LandingService ConnectRPC handler.
// This service is registered with publicOpts (no auth interceptor) so the
// landing page can fetch featured-question data without a session.
package landing

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/btc/drill/internal/backend"
	"github.com/btc/drill/internal/db"
	drillv1 "github.com/btc/drill/internal/pb/drill/v1"
	"github.com/btc/drill/internal/pb/drill/v1/drillv1connect"
)

// Server implements LandingServiceHandler. Public; does not require auth.
type Server struct {
	b *backend.Backend
}

var _ drillv1connect.LandingServiceHandler = (*Server)(nil)

// NewServer creates a new LandingService handler.
func NewServer(b *backend.Backend) *Server {
	return &Server{b: b}
}

// ListFeaturedQuestions returns the curated featured questions and total
// seed-question count for the public landing page.
func (s *Server) ListFeaturedQuestions(
	ctx context.Context,
	_ *connect.Request[drillv1.ListFeaturedQuestionsRequest],
) (*connect.Response[drillv1.ListFeaturedQuestionsResponse], error) {
	rows, total, err := s.b.ListFeaturedQuestions(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "landing: list featured questions", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("list featured questions failed"))
	}

	questions := make([]*drillv1.Question, 0, len(rows))
	for _, row := range rows {
		questions = append(questions, rowToProto(row))
	}

	return connect.NewResponse(&drillv1.ListFeaturedQuestionsResponse{
		Questions:  questions,
		TotalCount: total,
	}), nil
}

// rowToProto converts a ListFeaturedQuestionsRow to a proto Question.
// Local to this package because the existing questionToProto in the question
// service binds to db.ListQuestionsForUserRow, a distinct sqlc-generated type.
func rowToProto(row db.ListFeaturedQuestionsRow) *drillv1.Question {
	q := &drillv1.Question{
		Id:         row.ID.String(),
		Title:      row.Title,
		Prompt:     row.Prompt,
		Difficulty: difficultyToProto(row.Difficulty),
		Tags:       row.Tags,
		Source:     sourceToProto(row.Source),
		CreateTime: timestamppb.New(row.CreatedAt),
	}
	if row.Hints.Valid {
		q.Hints = &row.Hints.String
	}
	if row.ImageUrl.Valid {
		q.ImageUrl = &row.ImageUrl.String
	}
	// Featured seed questions always have user_id NULL (see migration 014),
	// so UserId is left unset.
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
