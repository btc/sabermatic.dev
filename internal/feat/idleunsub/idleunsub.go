// Package idleunsub owns the idle-auto-cancel logic: evaluate the cancel
// trigger on Stripe's invoice.upcoming webhook, sign keep-tokens, and
// auto-reverse a pending cancel when activity resumes.
package idleunsub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	stripe "github.com/stripe/stripe-go/v82"

	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/email"
)

// StripeClient is the surface idleunsub needs from Stripe. Production wires
// this to wrappers around stripe-go's package-level functions; tests inject
// an in-memory fake.
type StripeClient interface {
	GetSubscription(ctx context.Context, id string) (*stripe.Subscription, error)
	UpdateSubscriptionCancel(ctx context.Context, id string, cancelAtPeriodEnd bool, idempotencyKey string) (*stripe.Subscription, error)
}

// Service owns the cancel/keep/auto-reverse logic.
type Service struct {
	pool   *pgxpool.Pool
	stripe StripeClient
	mailer email.Sender
	signer *TokenSigner
	now    func() time.Time
	log    *slog.Logger
}

// NewService constructs a Service. The signer may be nil for tests that
// only exercise HandleInvoiceUpcoming (Task 7); Tasks 8/9 wire it in.
func NewService(pool *pgxpool.Pool, sc StripeClient, m email.Sender, sn *TokenSigner, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{pool: pool, stripe: sc, mailer: m, signer: sn, now: time.Now, log: log}
}

// HandleInvoiceUpcoming evaluates the trigger rule. Idempotent: safe to call
// multiple times for the same Stripe event.
func (s *Service) HandleInvoiceUpcoming(ctx context.Context, event stripe.Event) error {
	// Extract subscription ID from the invoice.upcoming event payload.
	subID := event.GetObjectValue("subscription")
	if subID == "" {
		s.log.Warn("invoice.upcoming missing subscription", "event_id", event.ID)
		return nil
	}

	// 1. Fetch the subscription (authoritative source for current period and status).
	sub, err := s.stripe.GetSubscription(ctx, subID)
	if err != nil {
		mCancelError(ctx, "stripe_get_failed")
		return fmt.Errorf("get subscription %s: %w", subID, err)
	}

	// 2. Status / state early returns.
	if sub.Status != stripe.SubscriptionStatusActive {
		s.log.Debug("skip non_active_status", "sub_id", subID, "status", sub.Status)
		mCancelSkipped(ctx, "non_active_status")
		return nil
	}
	if sub.CancelAtPeriodEnd {
		s.log.Debug("skip already_canceled", "sub_id", subID)
		mCancelSkipped(ctx, "already_canceled")
		return nil
	}
	if sub.Items == nil || len(sub.Items.Data) == 0 {
		s.log.Warn("subscription has no items", "sub_id", subID)
		return nil
	}
	item := sub.Items.Data[0]
	if item.Price == nil || item.Price.Recurring == nil {
		s.log.Warn("subscription item missing price.recurring", "sub_id", subID)
		return nil
	}
	periodStart := time.Unix(item.CurrentPeriodStart, 0).UTC()
	periodEnd := time.Unix(item.CurrentPeriodEnd, 0).UTC()
	threshold := subtractInterval(periodStart, item.Price.Recurring.Interval)

	// 3. Look up our user by stripe customer ID.
	if sub.Customer == nil || sub.Customer.ID == "" {
		s.log.Warn("subscription missing customer", "sub_id", subID)
		return nil
	}
	custID := sub.Customer.ID
	q := db.New(s.pool)
	user, err := q.GetUserByStripeCustomer(ctx, pgxText(custID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			s.log.Warn("no user for stripe customer", "customer_id", custID, "sub_id", subID)
			return nil
		}
		return fmt.Errorf("get user by stripe customer %s: %w", custID, err)
	}

	// 4. Begin the cancel transaction.
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		mCancelError(ctx, "db_begin_failed")
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	qtx := db.New(tx)

	// 4a. Per-user mutex.
	if _, err := qtx.LockUserForSubDecision(ctx, user.ID); err != nil {
		mCancelError(ctx, "db_lock_failed")
		return fmt.Errorf("lock user: %w", err)
	}

	// 4b. Webhook event dedup (retry-storm protection).
	if _, err := qtx.TryClaimWebhookEvent(ctx, db.TryClaimWebhookEventParams{
		EventID: event.ID, EventType: string(event.Type),
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			s.log.Debug("skip duplicate_event", "event_id", event.ID)
			mCancelSkipped(ctx, "duplicate_event")
			return nil
		}
		mCancelError(ctx, "db_dedup_failed")
		return fmt.Errorf("claim webhook event: %w", err)
	}

	// 4c. Period-keyed dedup queries.
	mostRecent, err := qtx.GetMostRecentKeptOrCanceledForPeriod(ctx,
		db.GetMostRecentKeptOrCanceledForPeriodParams{
			UserID:             user.ID,
			SubscriptionID:     subID,
			CurrentPeriodStart: periodStart,
		})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		mCancelError(ctx, "db_dedup_query_failed")
		return fmt.Errorf("most recent decision: %w", err)
	}
	if mostRecent == "subscription_kept" {
		s.log.Debug("skip already_kept_this_period", "user_id", user.ID, "sub_id", subID)
		mCancelSkipped(ctx, "already_kept_this_period")
		return nil
	}
	hasCanceled, err := qtx.HasAutoCanceledThisPeriod(ctx,
		db.HasAutoCanceledThisPeriodParams{
			UserID:             user.ID,
			SubscriptionID:     subID,
			CurrentPeriodStart: periodStart,
		})
	if err != nil {
		mCancelError(ctx, "db_dedup_query_failed")
		return fmt.Errorf("has auto canceled: %w", err)
	}
	if hasCanceled {
		s.log.Debug("skip already_canceled_this_period", "user_id", user.ID, "sub_id", subID)
		mCancelSkipped(ctx, "already_canceled_this_period")
		return nil
	}

	// 4d. Read state and compute trigger.
	lastActive, err := qtx.GetUserLastActive(ctx, user.ID)
	if err != nil {
		return fmt.Errorf("get last active: %w", err)
	}
	if !lastActive.Valid {
		s.log.Debug("skip no_activity_history", "user_id", user.ID)
		mCancelSkipped(ctx, "no_activity_history")
		return nil
	}
	if !lastActive.Time.Before(threshold) {
		s.log.Debug("skip active_in_window",
			"user_id", user.ID, "last_active", lastActive.Time, "threshold", threshold)
		mCancelSkipped(ctx, "active_in_window")
		return nil
	}
	if periodStart.Before(user.IdleEligibleAfter) {
		s.log.Debug("skip grandfathered",
			"user_id", user.ID,
			"period_start", periodStart, "idle_eligible_after", user.IdleEligibleAfter)
		mCancelSkipped(ctx, "grandfathered")
		return nil
	}

	// 5. Trigger fires: insert audit row + cache update inside the TX.
	mdJSON, err := marshalCancelMetadata(subID, event.ID, periodStart, periodEnd)
	if err != nil {
		return fmt.Errorf("marshal cancel metadata: %w", err)
	}
	if err := qtx.InsertSubscriptionAutoCanceledEvent(ctx,
		db.InsertSubscriptionAutoCanceledEventParams{
			UserID: user.ID, Metadata: mdJSON,
		}); err != nil {
		return fmt.Errorf("insert event row: %w", err)
	}
	if err := qtx.SetUserAutoCancelState(ctx, db.SetUserAutoCancelStateParams{
		ID:                    user.ID,
		SubCurrentPeriodStart: pgxTime(periodStart),
	}); err != nil {
		return fmt.Errorf("set cache: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		mCancelError(ctx, "db_commit_failed")
		return fmt.Errorf("commit: %w", err)
	}

	// 6. Stripe call OUTSIDE the transaction. Idempotency key keeps retries safe.
	if _, err := s.stripe.UpdateSubscriptionCancel(ctx, subID, true, event.ID); err != nil {
		mCancelError(ctx, "stripe_update_failed")
		s.log.Error("stripe update failed after commit",
			"sub_id", subID, "event_id", event.ID, "err", err)
		// Cache is now ahead of Stripe. AutoReverse re-checks Stripe state,
		// so this drift will self-heal on the user's next authed request.
		return fmt.Errorf("stripe update: %w", err)
	}

	mCancelFired(ctx, subID)

	// 7. Enqueue cancel email.
	if err := s.enqueueCancelEmail(ctx, user, subID, periodEnd); err != nil {
		mEmailEnqueue(ctx, "cancel", "error")
		s.log.Error("cancel email enqueue failed", "user_id", user.ID, "err", err)
	} else {
		mEmailEnqueue(ctx, "cancel", "ok")
	}
	return nil
}

// subtractInterval returns t minus one Stripe billing interval.
func subtractInterval(t time.Time, interval stripe.PriceRecurringInterval) time.Time {
	switch interval {
	case stripe.PriceRecurringIntervalDay:
		return t.AddDate(0, 0, -1)
	case stripe.PriceRecurringIntervalWeek:
		return t.AddDate(0, 0, -7)
	case stripe.PriceRecurringIntervalMonth:
		return t.AddDate(0, -1, 0)
	case stripe.PriceRecurringIntervalYear:
		return t.AddDate(-1, 0, 0)
	default:
		return t.AddDate(0, -1, 0) // safe default
	}
}

func marshalCancelMetadata(subID, eventID string, start, end time.Time) ([]byte, error) {
	return json.Marshal(cancelMetadata{
		SubscriptionID:     subID,
		StripeEventID:      eventID,
		CurrentPeriodStart: start.UTC(),
		CurrentPeriodEnd:   end.UTC(),
	})
}

// enqueueCancelEmail composes and enqueues the cancel email.
// Implementation lands in Task 9 alongside the templates; for now this is a
// stub so the cancel path compiles. Task 9 replaces it with the real impl.
func (s *Service) enqueueCancelEmail(ctx context.Context, user db.User, subID string, periodEnd time.Time) error {
	_ = ctx
	_ = user
	_ = subID
	_ = periodEnd
	return nil // placeholder; replaced in Task 9
}

// pgxText / pgxTime are local pgtype constructors. Inlined here because the
// project does not yet have a shared helper for these wrappers; if/when it
// does, swap to that and delete these.
func pgxText(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: true}
}

func pgxTime(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}
