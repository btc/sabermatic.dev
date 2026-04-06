package backend

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/drilotel"
	"github.com/btc/drill/internal/jobs"
)

// CoachResponse is the API response for coach analysis.
type CoachResponse struct {
	ID                  string    `json:"id"`
	UserID              string    `json:"user_id"`
	Narrative           string    `json:"narrative"`
	WeakestDimension    string    `json:"weakest_dimension,omitempty"`
	ImprovingDimensions []string  `json:"improving_dimensions,omitempty"`
	TopicGaps           []string  `json:"topic_gaps,omitempty"`
	SuggestedQuestionID *string   `json:"suggested_question_id,omitempty"`
	SessionsAnalyzed    []string  `json:"sessions_analyzed"`
	CreatedAt           time.Time `json:"created_at"`
}

// GetLatestCoachAnalysis returns the most recent coach analysis for a user.
// Returns nil with no error if no analysis exists.
func (b *Backend) GetLatestCoachAnalysis(ctx context.Context, userID uuid.UUID) (_ *CoachResponse, err error) {
	ctx, span := tracer.Start(ctx, "Backend.GetLatestCoachAnalysis")
	defer func() { drilotel.End(span, err) }()

	if ent, err := b.Check(ctx, userID); err != nil {
		return nil, err
	} else if !ent.CanAccessCoach() {
		return nil, ErrNoPaidBalance
	}

	q := db.New(b.pool)
	ca, err := q.GetLatestCoachAnalysis(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get latest coach analysis: %w", err)
	}

	sessionsAnalyzed := make([]string, len(ca.SessionsAnalyzed))
	for i, sid := range ca.SessionsAnalyzed {
		sessionsAnalyzed[i] = sid.String()
	}

	resp := &CoachResponse{
		ID:                  ca.ID.String(),
		UserID:              ca.UserID.String(),
		Narrative:           ca.Narrative,
		WeakestDimension:    ca.WeakestDimension.String,
		ImprovingDimensions: ca.ImprovingDimensions,
		TopicGaps:           ca.TopicGaps,
		SessionsAnalyzed:    sessionsAnalyzed,
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
// force skips the "no new sessions" check — used by admin or retry flows.
func (b *Backend) RequestCoachAnalysis(ctx context.Context, userID uuid.UUID, force bool) (err error) {
	ctx, span := tracer.Start(ctx, "Backend.RequestCoachAnalysis")
	defer func() { drilotel.End(span, err) }()

	ent, err := b.Check(ctx, userID)
	if err != nil {
		return err
	}
	if !ent.CanAccessCoach() {
		return ErrNoPaidBalance
	}

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

	coachOpts := jobs.RunCoachAnalysisInsertOpts()
	drilotel.SetTraceMetadata(ctx, coachOpts)
	_, err = b.jobs.Insert(ctx, jobs.RunCoachAnalysisArgs{UserID: userID}, coachOpts)
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
