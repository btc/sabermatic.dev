package backend

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/jobs"
)

// CoachResponse is the API response for coach analysis.
type CoachResponse struct {
	Narrative           string    `json:"narrative"`
	WeakestDimension    string    `json:"weakest_dimension,omitempty"`
	ImprovingDimensions []string  `json:"improving_dimensions,omitempty"`
	TopicGaps           []string  `json:"topic_gaps,omitempty"`
	SuggestedQuestionID *string   `json:"suggested_question_id,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
}

// GetLatestCoachAnalysis returns the most recent coach analysis for a user.
// Returns nil with no error if no analysis exists.
func (b *Backend) GetLatestCoachAnalysis(ctx context.Context, userID uuid.UUID) (*CoachResponse, error) {
	q := db.New(b.pool)
	ca, err := q.GetLatestCoachAnalysis(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get latest coach analysis: %w", err)
	}

	resp := &CoachResponse{
		Narrative:           ca.Narrative,
		WeakestDimension:    ca.WeakestDimension.String,
		ImprovingDimensions: ca.ImprovingDimensions,
		TopicGaps:           ca.TopicGaps,
		CreatedAt:           ca.CreatedAt,
	}
	if ca.SuggestedQuestionID.Valid {
		id := uuid.UUID(ca.SuggestedQuestionID.Bytes).String()
		resp.SuggestedQuestionID = &id
	}
	if resp.ImprovingDimensions == nil {
		resp.ImprovingDimensions = []string{}
	}
	if resp.TopicGaps == nil {
		resp.TopicGaps = []string{}
	}

	return resp, nil
}

// RequestCoachAnalysis enqueues a coach analysis job.
// If force is false, checks whether new sessions exist since the last analysis.
func (b *Backend) RequestCoachAnalysis(ctx context.Context, userID uuid.UUID, force bool) error {
	q := db.New(b.pool)

	if !force {
		// Check if there are new reviewed sessions since the last analysis.
		currentIDs, err := q.GetReviewedSessionIDsForUser(ctx, userID)
		if err != nil {
			return fmt.Errorf("get reviewed session ids: %w", err)
		}

		ca, err := q.GetLatestCoachAnalysis(ctx, userID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("get latest coach analysis: %w", err)
		}

		if err == nil && uuidSlicesEqual(currentIDs, ca.SessionsAnalyzed) {
			return ErrNoNewSessions
		}
	}

	_, err := b.jobs.Insert(ctx, jobs.RunCoachAnalysisArgs{UserID: userID}, jobs.RunCoachAnalysisInsertOpts())
	if err != nil {
		return fmt.Errorf("enqueue coach job: %w", err)
	}
	return nil
}

// uuidSlicesEqual compares two UUID slices for set equality (order-sensitive;
// both queries ORDER BY created_at so order is deterministic).
func uuidSlicesEqual(a []uuid.UUID, b []uuid.UUID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
