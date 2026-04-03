package backend

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/btc/drill/internal/billing"
	"github.com/btc/drill/internal/db"
)

// ---------------------------------------------------------------------------
// EnsureFreeGrant
// ---------------------------------------------------------------------------

// EnsureFreeGrant creates the current-month free grant for the user if it does
// not already exist. The grant amount and expiry are determined by the user's
// plan. It is safe to call multiple times per month; duplicate creation is a
// no-op thanks to the ON CONFLICT clause in CreateFreeGrant.
func (b *Backend) EnsureFreeGrant(ctx context.Context, userID uuid.UUID, planName string) error {
	plan, ok := billing.PlanByName(planName)
	if !ok {
		return fmt.Errorf("unknown plan %q", planName)
	}

	now := time.Now().UTC()
	expiresAt := billing.EndOfMonth(now)

	q := db.New(b.pool)
	_, err := q.CreateFreeGrant(ctx, db.CreateFreeGrantParams{
		UserID:         userID,
		InitialMinutes: int32(plan.MinutesPerMonth),
		ExpiresAt:      pgtype.Timestamptz{Time: expiresAt, Valid: true},
	})
	if err != nil {
		// ON CONFLICT DO NOTHING returns pgx.ErrNoRows when the grant
		// already exists for this month -- treat as success.
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("create free grant: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// ReserveMinutes
// ---------------------------------------------------------------------------

// ReserveMinutes reserves the given number of minutes from the user's active
// grants. Grants are consumed FIFO by expiry (soonest-expiring first). Each
// debit is recorded as a ledger entry with reason "session_reserve".
//
// Returns ErrInsufficientBalance if the user's total available minutes are
// less than the requested amount.
func (b *Backend) ReserveMinutes(ctx context.Context, userID uuid.UUID, sessionID uuid.UUID, minutes int32) error {
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin reserve tx: %w", err)
	}
	defer tx.Rollback(ctx)

	q := db.New(tx)

	// Lock grants FOR UPDATE to prevent concurrent reservation races.
	grants, err := q.SelectGrantsForReservation(ctx, userID)
	if err != nil {
		return fmt.Errorf("select grants for reservation: %w", err)
	}

	// Sum available minutes.
	var total int32
	for _, g := range grants {
		total += g.RemainingMinutes
	}
	if total < minutes {
		return ErrInsufficientBalance
	}

	remaining := minutes
	sid := pgtype.UUID{Bytes: sessionID, Valid: true}

	for _, g := range grants {
		if remaining <= 0 {
			break
		}

		debit := g.RemainingMinutes
		if debit > remaining {
			debit = remaining
		}

		_, err := q.DebitGrant(ctx, db.DebitGrantParams{
			ID:               g.ID,
			RemainingMinutes: debit,
		})
		if err != nil {
			return fmt.Errorf("debit grant %s: %w", g.ID, err)
		}

		_, err = q.InsertLedgerEntry(ctx, db.InsertLedgerEntryParams{
			UserID:    userID,
			GrantID:   g.ID,
			Amount:    -debit,
			Reason:    "session_reserve",
			SessionID: sid,
		})
		if err != nil {
			return fmt.Errorf("insert ledger entry for grant %s: %w", g.ID, err)
		}

		remaining -= debit
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit reserve tx: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// RefundMinutes
// ---------------------------------------------------------------------------

// RefundMinutes credits back the given number of minutes for a session. It
// reconstructs the per-grant debit amounts from ledger entries and credits
// back in reverse order (most-recently-debited first). If a grant credit
// fails (e.g., the grant has expired), the error is logged and processing
// continues with the next grant.
func (b *Backend) RefundMinutes(ctx context.Context, userID uuid.UUID, sessionID uuid.UUID, minutes int32) error {
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin refund tx: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := b.refundMinutesTx(ctx, tx, userID, sessionID, minutes); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit refund tx: %w", err)
	}
	return nil
}

// refundMinutesTx performs the refund logic within an existing transaction.
// Used by CompleteSession (Task 8) to refund within the completion transaction.
func (b *Backend) refundMinutesTx(ctx context.Context, dbtx db.DBTX, userID uuid.UUID, sessionID uuid.UUID, minutes int32) error {
	q := db.New(dbtx)
	sid := pgtype.UUID{Bytes: sessionID, Valid: true}

	// Get the reservation entries for this session (ordered by created_at DESC,
	// so we process most-recent first for reverse-order crediting).
	entries, err := q.GetSessionReservationEntries(ctx, sid)
	if err != nil {
		return fmt.Errorf("get session reservation entries: %w", err)
	}

	remaining := minutes

	for _, entry := range entries {
		if remaining <= 0 {
			break
		}

		// entry.Amount is negative (debit), so the max we can refund is -Amount.
		maxRefund := -entry.Amount
		if maxRefund <= 0 {
			continue
		}

		credit := maxRefund
		if credit > remaining {
			credit = remaining
		}

		_, err := q.CreditGrant(ctx, db.CreditGrantParams{
			ID:               entry.GrantID,
			RemainingMinutes: credit,
		})
		if err != nil {
			// Grant may have expired or been deleted. Log and continue.
			slog.Warn("credit grant failed during refund",
				"grant_id", entry.GrantID,
				"credit", credit,
				"error", err,
			)
			continue
		}

		_, err = q.InsertLedgerEntry(ctx, db.InsertLedgerEntryParams{
			UserID:    userID,
			GrantID:   entry.GrantID,
			Amount:    credit,
			Reason:    "session_refund",
			SessionID: sid,
		})
		if err != nil {
			return fmt.Errorf("insert refund ledger entry for grant %s: %w", entry.GrantID, err)
		}

		remaining -= credit
	}

	return nil
}

// ---------------------------------------------------------------------------
// GetUsageSummary
// ---------------------------------------------------------------------------

// UsageSummary holds the balance breakdown, active grants, and recent ledger
// entries for a user.
type UsageSummary struct {
	TotalBalance int32                       `json:"total_balance"`
	FreeBalance  int32                       `json:"free_balance"`
	PaidBalance  int32                       `json:"paid_balance"`
	Grants       []db.ListActiveGrantsRow    `json:"grants"`
	LedgerLog    []db.GetRecentLedgerEntriesRow `json:"ledger_log"`
}

// GetUsageSummary returns the user's balance breakdown, active grants, and
// recent ledger entries.
func (b *Backend) GetUsageSummary(ctx context.Context, userID uuid.UUID) (*UsageSummary, error) {
	q := db.New(b.pool)

	summary, err := q.GetUserUsageSummary(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get usage summary: %w", err)
	}

	grants, err := q.ListActiveGrants(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list active grants: %w", err)
	}

	entries, err := q.GetRecentLedgerEntries(ctx, db.GetRecentLedgerEntriesParams{
		UserID: userID,
		Limit:  50,
	})
	if err != nil {
		return nil, fmt.Errorf("get recent ledger entries: %w", err)
	}

	return &UsageSummary{
		TotalBalance: summary.TotalBalance,
		FreeBalance:  summary.FreeBalance,
		PaidBalance:  summary.PaidBalance,
		Grants:       grants,
		LedgerLog:    entries,
	}, nil
}
