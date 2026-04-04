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
	return rows, nil
}
