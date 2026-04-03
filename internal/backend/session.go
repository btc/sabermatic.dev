package backend

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/btc/drill/internal/db"
)

// CreateSessionParams holds the parameters for CreateSession.
type CreateSessionParams struct {
	UserID          uuid.UUID
	QuestionID      uuid.UUID
	DurationMinutes int
	TTSEnabled      bool
}

// CreateSession creates a new interview session after validating duration and
// verifying the question exists.
func (b *Backend) CreateSession(ctx context.Context, p CreateSessionParams) (db.InterviewSession, error) {
	if p.DurationMinutes < 1 || p.DurationMinutes > 180 {
		return db.InterviewSession{}, ErrInvalidDuration
	}

	queries := db.New(b.pool)

	if _, err := queries.GetQuestion(ctx, p.QuestionID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.InterviewSession{}, ErrQuestionNotFound
		}
		return db.InterviewSession{}, fmt.Errorf("get question: %w", err)
	}

	session, err := queries.CreateSession(ctx, db.CreateSessionParams{
		UserID:                p.UserID,
		QuestionID:            p.QuestionID,
		ConfigDurationMinutes: int32(p.DurationMinutes),
		ConfigTtsEnabled:      p.TTSEnabled,
	})
	if err != nil {
		return db.InterviewSession{}, fmt.Errorf("create session: %w", err)
	}
	return session, nil
}

// GetSession returns the interview session with the given ID.
// Returns ErrSessionNotFound if no such session exists.
func (b *Backend) GetSession(ctx context.Context, id uuid.UUID) (db.InterviewSession, error) {
	queries := db.New(b.pool)
	session, err := queries.GetSession(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.InterviewSession{}, ErrSessionNotFound
		}
		return db.InterviewSession{}, fmt.Errorf("get session: %w", err)
	}
	return session, nil
}

// GetSessionForUser returns the session only if it belongs to the given user.
// Returns ErrSessionNotFound if the session does not exist, ErrSessionNotOwned
// if it belongs to a different user.
func (b *Backend) GetSessionForUser(ctx context.Context, id, userID uuid.UUID) (db.InterviewSession, error) {
	session, err := b.GetSession(ctx, id)
	if err != nil {
		return db.InterviewSession{}, err
	}
	if session.UserID != userID {
		return db.InterviewSession{}, ErrSessionNotOwned
	}
	return session, nil
}

// ListSessions returns all sessions for the given user, ordered by creation
// time descending.
func (b *Backend) ListSessions(ctx context.Context, userID uuid.UUID) ([]db.ListSessionsByUserRow, error) {
	queries := db.New(b.pool)
	rows, err := queries.ListSessionsByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	return rows, nil
}
