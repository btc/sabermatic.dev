package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	pgx "github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/btc/drill/internal/db"
)

// CleanupAbandonedSessionsArgs are the arguments for the CleanupAbandonedSessions job.
type CleanupAbandonedSessionsArgs struct{}

func (CleanupAbandonedSessionsArgs) Kind() string { return "cleanup_abandoned_sessions" }

// CleanupAbandonedSessionsWorker marks timed-out active sessions as completed.
type CleanupAbandonedSessionsWorker struct {
	river.WorkerDefaults[CleanupAbandonedSessionsArgs]
	Pool *pgxpool.Pool
	Jobs *river.Client[pgx.Tx] // set after river.NewClient returns
}

func (w *CleanupAbandonedSessionsWorker) Work(ctx context.Context, job *river.Job[CleanupAbandonedSessionsArgs]) error {
	tx, err := w.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin cleanup tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// Batch-mark all abandoned sessions as completed in a single UPDATE.
	ids, err := db.New(tx).MarkAbandonedSessionsCompleted(ctx)
	if err != nil {
		return fmt.Errorf("mark abandoned sessions: %w", err)
	}

	q := db.New(tx)

	// Refund unused minutes and enqueue evaluation for each.
	for _, id := range ids {
		// Compute and refund unused minutes for abandoned sessions.
		sess, err := q.GetSessionByID(ctx, id)
		if err != nil {
			slog.Warn("cleanup: get session for refund", "session_id", id, "error", err)
		} else if sess.ReservedMinutes.Valid && sess.ReservedMinutes.Int32 > 0 {
			actualMinutes := int32(1)
			if sess.EndedAt.Valid {
				dur := sess.EndedAt.Time.Sub(sess.StartedAt)
				mins := int32((dur + time.Minute - 1) / time.Minute)
				if mins > actualMinutes {
					actualMinutes = mins
				}
			}
			refund := sess.ReservedMinutes.Int32 - actualMinutes
			if refund > 0 {
				refundAbandonedMinutes(ctx, q, sess.UserID, id, refund)
			}
		}

		if _, err := w.Jobs.InsertTx(ctx, tx, EvaluateSessionArgs{SessionID: id}, EvaluateSessionInsertOpts()); err != nil {
			return fmt.Errorf("enqueue evaluation for session %s: %w", id, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit cleanup: %w", err)
	}

	if len(ids) > 0 {
		slog.Info("cleaned up abandoned sessions", "count", len(ids))
	}
	return nil
}

// refundAbandonedMinutes credits back unused reserved minutes for an abandoned
// session. Uses the same FIFO-reverse pattern as backend.refundMinutesTx.
func refundAbandonedMinutes(ctx context.Context, q *db.Queries, userID uuid.UUID, sessionID uuid.UUID, minutes int32) {
	sid := pgtype.UUID{Bytes: sessionID, Valid: true}
	entries, err := q.GetSessionReservationEntries(ctx, sid)
	if err != nil {
		slog.Warn("cleanup: get reservation entries for refund", "session_id", sessionID, "error", err)
		return
	}

	remaining := minutes
	for _, e := range entries {
		if remaining <= 0 {
			break
		}
		maxRefund := -e.Amount
		if maxRefund <= 0 {
			continue
		}
		credit := maxRefund
		if credit > remaining {
			credit = remaining
		}
		if _, err := q.CreditGrant(ctx, db.CreditGrantParams{
			ID: e.GrantID, RemainingMinutes: credit,
		}); err != nil {
			slog.Warn("cleanup: credit grant failed", "grant_id", e.GrantID, "error", err)
			continue
		}
		if _, err := q.InsertLedgerEntry(ctx, db.InsertLedgerEntryParams{
			UserID: userID, GrantID: e.GrantID, Amount: credit,
			Reason: "session_refund", SessionID: sid,
		}); err != nil {
			slog.Warn("cleanup: insert refund ledger entry", "grant_id", e.GrantID, "error", err)
			return
		}
		remaining -= credit
	}
}
