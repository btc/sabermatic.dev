package backend

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/drilotel"
)

// ListQuestions returns all questions visible to the given user: seed questions
// (shared, no owner) and questions owned by the user.
func (b *Backend) ListQuestions(ctx context.Context, userID uuid.UUID) (_ []db.ListQuestionsForUserRow, err error) {
	ctx, span := tracer.Start(ctx, "Backend.ListQuestions")
	defer func() { drilotel.End(span, err) }()

	queries := db.New(b.pool)
	rows, err := queries.ListQuestionsForUser(ctx, pgtype.UUID{Bytes: userID, Valid: true})
	if err != nil {
		return nil, fmt.Errorf("list questions: %w", err)
	}
	if rows == nil {
		rows = []db.ListQuestionsForUserRow{}
	}
	return rows, nil
}

// ListFeaturedQuestions returns the curated list of featured questions for
// the public landing page along with the total count of seed questions.
//
// The featured list is ordered by featured_order. The total_count is scoped
// to seed questions (source='seed' AND user_id IS NULL) so the public
// landing's "N questions" headline reflects the curated library, not private
// user-generated or coach-generated questions.
func (b *Backend) ListFeaturedQuestions(ctx context.Context) (_ []db.ListFeaturedQuestionsRow, _ int32, err error) {
	ctx, span := tracer.Start(ctx, "Backend.ListFeaturedQuestions")
	defer func() { drilotel.End(span, err) }()

	queries := db.New(b.pool)

	rows, err := queries.ListFeaturedQuestions(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("list featured questions: %w", err)
	}

	total, err := queries.CountSeedQuestions(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("count seed questions: %w", err)
	}

	return rows, total, nil
}

// CreateQuestion inserts a user-created custom question and returns the
// fully-populated row. The caller provides only the writable fields; source
// is always "custom" and user_id comes from the authenticated context.
func (b *Backend) CreateQuestion(ctx context.Context, userID uuid.UUID, title, prompt, difficulty string, tags []string) (_ db.Question, err error) {
	ctx, span := tracer.Start(ctx, "Backend.CreateQuestion")
	defer func() { drilotel.End(span, err) }()

	if tags == nil {
		tags = []string{}
	}

	queries := db.New(b.pool)
	id, err := queries.InsertQuestion(ctx, db.InsertQuestionParams{
		UserID:         pgtype.UUID{Bytes: userID, Valid: true},
		Title:          title,
		Prompt:         prompt,
		Difficulty:     difficulty,
		Tags:           tags,
		Source:         "custom",
		CoachRationale: pgtype.Text{}, // NULL — not applicable for custom questions
	})
	if err != nil {
		return db.Question{}, fmt.Errorf("insert question: %w", err)
	}

	row, err := queries.GetQuestion(ctx, id)
	if err != nil {
		return db.Question{}, fmt.Errorf("get question after insert: %w", err)
	}
	return row, nil
}
