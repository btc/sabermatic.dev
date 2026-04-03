package backend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/jobs"
)

// EvaluationResponse is the API response for a session's evaluation.
type EvaluationResponse struct {
	Status      string               `json:"status"`
	Scores      *EvaluationScores    `json:"scores,omitempty"`
	Strengths   []string             `json:"strengths,omitempty"`
	Gaps        []string             `json:"gaps,omitempty"`
	Advice      string               `json:"advice,omitempty"`
	Annotations []AnnotationResponse `json:"annotations,omitempty"`
}

// EvaluationScores holds the 6 dimension scores.
type EvaluationScores struct {
	Requirements  int32 `json:"requirements"`
	Architecture  int32 `json:"architecture"`
	DeepDive      int32 `json:"deep_dive"`
	Scalability   int32 `json:"scalability"`
	Communication int32 `json:"communication"`
	Overall       int32 `json:"overall"`
}

// AnnotationResponse is a single annotation in the API response.
type AnnotationResponse struct {
	MessageSeq int32  `json:"message_seq"`
	Type       string `json:"type"`
	Content    string `json:"content"`
}

// GetEvaluation returns the evaluation for a session owned by the given user.
func (b *Backend) GetEvaluation(ctx context.Context, sessionID, userID uuid.UUID) (*EvaluationResponse, error) {
	session, err := b.GetSessionForUser(ctx, sessionID, userID)
	if err != nil {
		return nil, err
	}

	switch session.Status {
	case "evaluation_failed":
		return &EvaluationResponse{Status: "evaluation_failed"}, nil
	case "reviewed":
		// Continue below to load evaluation data.
	default:
		return nil, ErrEvaluationNotReady
	}

	q := db.New(b.pool)

	eval, err := q.GetEvaluationBySession(ctx, sessionID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrEvaluationNotReady
		}
		return nil, fmt.Errorf("get evaluation: %w", err)
	}

	annRows, err := q.GetAnnotationsByEvaluation(ctx, eval.ID)
	if err != nil {
		return nil, fmt.Errorf("get annotations: %w", err)
	}

	var strengths, gaps []string
	json.Unmarshal(eval.Strengths, &strengths)
	json.Unmarshal(eval.Gaps, &gaps)

	annotations := make([]AnnotationResponse, len(annRows))
	for i, a := range annRows {
		annotations[i] = AnnotationResponse{
			MessageSeq: a.MessageSeq,
			Type:       a.AnnotationType,
			Content:    a.Content,
		}
	}

	return &EvaluationResponse{
		Status: "reviewed",
		Scores: &EvaluationScores{
			Requirements:  eval.ScoreRequirements,
			Architecture:  eval.ScoreArchitecture,
			DeepDive:      eval.ScoreDeepDive,
			Scalability:   eval.ScoreScalability,
			Communication: eval.ScoreCommunication,
			Overall:       eval.ScoreOverall,
		},
		Strengths:   strengths,
		Gaps:        gaps,
		Advice:      eval.Advice,
		Annotations: annotations,
	}, nil
}

// RetryEvaluation re-enqueues evaluation for a failed session.
func (b *Backend) RetryEvaluation(ctx context.Context, sessionID, userID uuid.UUID) error {
	session, err := b.GetSessionForUser(ctx, sessionID, userID)
	if err != nil {
		return err
	}
	if session.Status != "evaluation_failed" {
		return ErrNotEvaluationFailed
	}

	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin retry tx: %w", err)
	}
	defer tx.Rollback(ctx)

	err = db.New(tx).UpdateSessionStatusOnly(ctx, db.UpdateSessionStatusOnlyParams{
		ID:     sessionID,
		Status: "evaluating",
	})
	if err != nil {
		return fmt.Errorf("update status to evaluating: %w", err)
	}

	_, err = b.jobs.InsertTx(ctx, tx, jobs.EvaluateSessionArgs{SessionID: sessionID}, jobs.EvaluateSessionInsertOpts())
	if err != nil {
		return fmt.Errorf("enqueue evaluate_session: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit retry: %w", err)
	}
	return nil
}
