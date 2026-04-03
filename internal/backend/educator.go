package backend

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/btc/drill/internal/billing"
	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/jobs"
)

const previewModelAnswerLen = 200

// EducatorResponse is the API response for educator analysis.
type EducatorResponse struct {
	Status       string `json:"status"`
	ModelAnswer  string `json:"model_answer,omitempty"`
	GapDeepDives string `json:"gap_deep_dives,omitempty"`
}

// GetEducatorAnalysis returns the educator analysis for a session.
func (b *Backend) GetEducatorAnalysis(ctx context.Context, sessionID, userID uuid.UUID) (*EducatorResponse, error) {
	session, err := b.GetSessionForUser(ctx, sessionID, userID)
	if err != nil {
		return nil, err
	}
	if session.Status != "reviewed" {
		return nil, ErrEvaluationNotReady
	}

	// Determine educator access level.
	paidBal, err := db.New(b.pool).GetUserPaidBalance(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get paid balance: %w", err)
	}

	user, err := db.New(b.pool).GetUserByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}

	plan, _ := billing.PlanByName(user.Plan)
	accessLevel := billing.DetermineEducatorAccess(int(paidBal), int(user.FreeFullEducatorsUsed), plan.FreeEducatorLimit)

	q := db.New(b.pool)
	ea, err := q.GetEducatorAnalysisBySession(ctx, sessionID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return &EducatorResponse{Status: "not_requested"}, nil
		}
		return nil, fmt.Errorf("get educator analysis: %w", err)
	}

	switch ea.Status {
	case "generating":
		return &EducatorResponse{Status: "generating"}, nil
	case "failed":
		return &EducatorResponse{Status: "failed"}, nil
	case "completed":
		if accessLevel == billing.Preview {
			return &EducatorResponse{
				Status:      "completed",
				ModelAnswer: truncateRunes(ea.ModelAnswer.String, previewModelAnswerLen),
			}, nil
		}
		// FreeTaste users see full content on GET — the increment happens at
		// request time (POST /educator) to avoid double-counting on page refresh.
		return &EducatorResponse{
			Status:       "completed",
			ModelAnswer:  ea.ModelAnswer.String,
			GapDeepDives: ea.GapDeepDives.String,
		}, nil
	default:
		return &EducatorResponse{Status: ea.Status}, nil
	}
}

// truncateRunes returns s truncated to at most n runes.
func truncateRunes(s string, n int) string {
	count := 0
	for i := range s {
		if count == n {
			return s[:i]
		}
		count++
	}
	return s
}

// RequestEducatorAnalysis enqueues educator content generation for a session.
func (b *Backend) RequestEducatorAnalysis(ctx context.Context, sessionID, userID uuid.UUID) error {
	session, err := b.GetSessionForUser(ctx, sessionID, userID)
	if err != nil {
		return err
	}
	if session.Status != "reviewed" {
		return ErrEvaluationNotReady
	}

	// Determine educator access level.
	paidBal, err := db.New(b.pool).GetUserPaidBalance(ctx, userID)
	if err != nil {
		return fmt.Errorf("get paid balance: %w", err)
	}

	user, err := db.New(b.pool).GetUserByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("get user: %w", err)
	}

	plan, _ := billing.PlanByName(user.Plan)
	accessLevel := billing.DetermineEducatorAccess(int(paidBal), int(user.FreeFullEducatorsUsed), plan.FreeEducatorLimit)

	if accessLevel == billing.Preview {
		return ErrNoPaidBalance
	}

	// Consume the free taste on request (POST), not on read (GET).
	// The atomic increment ensures concurrent requests don't double-consume.
	if accessLevel == billing.FreeTaste {
		if _, err := db.New(b.pool).IncrementFreeEducatorUsed(ctx, db.IncrementFreeEducatorUsedParams{
			ID:                    userID,
			FreeFullEducatorsUsed: int32(plan.FreeEducatorLimit),
		}); err != nil {
			return fmt.Errorf("increment free educator used: %w", err)
		}
	}

	q := db.New(b.pool)
	ea, err := q.GetEducatorAnalysisBySession(ctx, sessionID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("check existing educator analysis: %w", err)
	}
	if err == nil {
		switch ea.Status {
		case "completed":
			return ErrAlreadyExists
		case "generating":
			return nil // idempotent
		case "failed":
			// Retry: reset status and enqueue atomically.
			tx, txErr := b.pool.Begin(ctx)
			if txErr != nil {
				return fmt.Errorf("begin retry tx: %w", txErr)
			}
			defer tx.Rollback(ctx) //nolint:errcheck
			if err := db.New(tx).UpdateEducatorAnalysisStatus(ctx, db.UpdateEducatorAnalysisStatusParams{
				ID:     ea.ID,
				Status: "generating",
			}); err != nil {
				return fmt.Errorf("reset educator status: %w", err)
			}
			if _, err := b.jobs.InsertTx(ctx, tx, jobs.GenerateEducatorContentArgs{SessionID: sessionID}, jobs.GenerateEducatorContentInsertOpts()); err != nil {
				return fmt.Errorf("enqueue educator job: %w", err)
			}
			return tx.Commit(ctx)
		}
	}

	_, err = b.jobs.Insert(ctx, jobs.GenerateEducatorContentArgs{SessionID: sessionID}, jobs.GenerateEducatorContentInsertOpts())
	if err != nil {
		return fmt.Errorf("enqueue educator job: %w", err)
	}
	return nil
}
