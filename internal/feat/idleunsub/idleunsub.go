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
	"strings"
	"time"

	"github.com/google/uuid"
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
	pool    *pgxpool.Pool
	stripe  StripeClient
	mailer  email.Sender
	signer  *TokenSigner
	baseURL string
	now     func() time.Time
	log     *slog.Logger
}

// NewService constructs a Service. The signer is required for the cancel email
// flow (buildKeepURL); pass a real TokenSigner in all test fixtures.
// baseURL is the public-facing base URL (e.g. "https://sabermatic.dev") used
// to build keep-links; pass "http://localhost:3000" in tests.
func NewService(pool *pgxpool.Pool, sc StripeClient, m email.Sender, sn *TokenSigner, baseURL string, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{pool: pool, stripe: sc, mailer: m, signer: sn, baseURL: baseURL, now: time.Now, log: log}
}

// Signer returns the configured TokenSigner. Used by Backend to verify
// keep-link tokens at the handler layer without duplicating the signer
// reference. Returns nil if KEEP_TOKEN_HMAC_KEY was unset at startup
// (degraded mode: cancel decisions still fire, but keep-link emails are
// skipped and the /sub/keep endpoint cannot verify tokens).
func (s *Service) Signer() *TokenSigner { return s.signer }

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
	threshold := subtractInterval(periodStart, item.Price.Recurring.Interval, item.Price.Recurring.IntervalCount)

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

// subtractInterval returns t minus count Stripe billing intervals.
func subtractInterval(t time.Time, interval stripe.PriceRecurringInterval, count int64) time.Time {
	if count < 1 {
		count = 1
	}
	n := int(count)
	switch interval {
	case stripe.PriceRecurringIntervalDay:
		return t.AddDate(0, 0, -n)
	case stripe.PriceRecurringIntervalWeek:
		return t.AddDate(0, 0, -7*n)
	case stripe.PriceRecurringIntervalMonth:
		return t.AddDate(0, -n, 0)
	case stripe.PriceRecurringIntervalYear:
		return t.AddDate(-n, 0, 0)
	default:
		return t.AddDate(0, -n, 0) // safe default
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

// enqueueCancelEmail composes the cancel email and sends it via the mailer.
// Called from HandleInvoiceUpcoming after the cancel decision is committed.
func (s *Service) enqueueCancelEmail(ctx context.Context, user db.User, subID string, periodEnd time.Time) error {
	keepURL, err := s.buildKeepURL(user.ID, subID, periodEnd)
	if err != nil {
		return fmt.Errorf("build keep url: %w", err)
	}
	msg, err := composeCancelEmail(user.Email, user.DisplayName, keepURL, periodEnd)
	if err != nil {
		return fmt.Errorf("compose cancel: %w", err)
	}
	return s.mailer.Send(ctx, msg)
}

func (s *Service) buildKeepURL(userID uuid.UUID, subID string, periodEnd time.Time) (string, error) {
	if s.signer == nil {
		return "", fmt.Errorf("token signer not configured")
	}
	periodEnd = periodEnd.UTC().Truncate(time.Second)
	tok := s.signer.Sign(KeepTokenClaims{
		UserID:           userID,
		SubscriptionID:   subID,
		Action:           "keep_subscription",
		CurrentPeriodEnd: periodEnd,
		IssuedAt:         s.now().Unix(),
		ExpiresAt:        periodEnd.Unix(),
	})
	return fmt.Sprintf("%s/sub/keep?t=%s", strings.TrimRight(s.baseURL, "/"), tok), nil
}

// KeepSubscription reverses cancel_at_period_end after a verified, single-use
// keep-link click. The endpoint is responsible for verifying the token AND
// claiming single-use BEFORE invoking this method. Refuses to act if the
// stored cache says the cancel is NOT our auto-cancel (manual portal cancel).
func (s *Service) KeepSubscription(ctx context.Context, claims KeepTokenClaims) error {
	q := db.New(s.pool)
	gates, err := q.GetUserAutoCancelGates(ctx, claims.UserID)
	if err != nil {
		return fmt.Errorf("read gates: %w", err)
	}
	if !gates.SubCancelAtPeriodEnd || !gates.SubCancelIsAuto {
		// Either the cancel was already reversed, or it's a manual portal cancel.
		// Don't touch Stripe; render confirmation page idempotently.
		return nil
	}
	// Empty idempotency key is intentional: KeepSubscription is gated upstream by
	// keep_link_token_uses single-use enforcement; AutoReverse converges via the
	// gate read on the next call. Unlike HandleInvoiceUpcoming (which uses event.ID
	// because Stripe retries webhooks with the same ID), reversal is request-driven
	// and idempotent at the gate-check layer.
	updated, err := s.stripe.UpdateSubscriptionCancel(ctx, claims.SubscriptionID, false, "")
	if err != nil {
		return fmt.Errorf("stripe reverse: %w", err)
	}
	if updated.Items == nil || len(updated.Items.Data) == 0 {
		return fmt.Errorf("stripe reverse: subscription %s missing items", claims.SubscriptionID)
	}
	periodStart := time.Unix(updated.Items.Data[0].CurrentPeriodStart, 0).UTC()

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op
	qtx := db.New(tx)

	if err := qtx.ClearUserAutoCancelState(ctx, db.ClearUserAutoCancelStateParams{
		ID: claims.UserID, SubCurrentPeriodStart: pgxTime(periodStart),
	}); err != nil {
		return fmt.Errorf("clear gates: %w", err)
	}

	mdJSON, err := marshalKeptMetadata(claims.SubscriptionID, "link", periodStart)
	if err != nil {
		return fmt.Errorf("marshal kept metadata: %w", err)
	}
	if err := qtx.InsertSubscriptionKeptEvent(ctx, db.InsertSubscriptionKeptEventParams{
		UserID: claims.UserID, Metadata: mdJSON,
	}); err != nil {
		return fmt.Errorf("insert kept event: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	mReverseLink(ctx, claims.SubscriptionID)

	if err := s.enqueueKeptEmail(ctx, claims.UserID, claims.SubscriptionID, claims.CurrentPeriodEnd); err != nil {
		mEmailEnqueue(ctx, "kept", "error")
		s.log.Error("kept email enqueue failed", "user_id", claims.UserID, "sub_id", claims.SubscriptionID, "err", err)
	} else {
		mEmailEnqueue(ctx, "kept", "ok")
	}
	return nil
}

// AutoReverse is invoked by the auth middleware when an authenticated request
// arrives from a user whose cached gates are SubCancelAtPeriodEnd && SubCancelIsAuto.
// The current request itself is the activity signal; we do NOT re-read
// last_active (would race against TouchAuthSession).
//
// AutoReverse verifies Stripe state before acting. If Stripe says the sub is
// NOT canceled (cache drift from a prior partial failure), AutoReverse silently
// clears the cache and returns without sending email or inserting an event row.
func (s *Service) AutoReverse(ctx context.Context, userID uuid.UUID) error {
	q := db.New(s.pool)
	gates, err := q.GetUserAutoCancelGates(ctx, userID)
	if err != nil {
		return fmt.Errorf("read gates: %w", err)
	}
	// If StripeSubscriptionID is NULL, AutoReverse cannot self-heal here — only
	// the link path (which carries the sub ID in the signed token) or a fresh
	// customer.subscription.updated webhook (via SyncSubStateFromWebhook) can
	// recover this state. This is consistent with the spec's "single sole
	// population path" invariant for stripe_subscription_id.
	if !gates.SubCancelAtPeriodEnd || !gates.SubCancelIsAuto || !gates.StripeSubscriptionID.Valid {
		return nil // cache says off, or no sub — nothing to do
	}
	subID := gates.StripeSubscriptionID.String

	// Verify Stripe state — handles the partial-failure window where our cache
	// says canceled but Stripe never confirmed.
	stripeSub, err := s.stripe.GetSubscription(ctx, subID)
	if err != nil {
		return fmt.Errorf("get sub: %w", err)
	}
	if stripeSub.Items == nil || len(stripeSub.Items.Data) == 0 {
		return fmt.Errorf("subscription %s missing items", subID)
	}
	if !stripeSub.CancelAtPeriodEnd {
		// Cache drift. Silently correct via the no-banner query and return.
		// We don't celebrate a reversal that didn't actually happen here —
		// the user kept their sub via some other channel.
		mCacheDriftCorrected(ctx, subID)
		periodStart := time.Unix(stripeSub.Items.Data[0].CurrentPeriodStart, 0).UTC()
		if err := q.ClearUserAutoCancelStateNoBanner(ctx, db.ClearUserAutoCancelStateNoBannerParams{
			ID: userID, SubCurrentPeriodStart: pgxTime(periodStart),
		}); err != nil {
			return fmt.Errorf("clear cache (drift): %w", err)
		}
		return nil
	}

	// Real reversal.
	// Empty idempotency key is intentional: KeepSubscription is gated upstream by
	// keep_link_token_uses single-use enforcement; AutoReverse converges via the
	// gate read on the next call. Unlike HandleInvoiceUpcoming (which uses event.ID
	// because Stripe retries webhooks with the same ID), reversal is request-driven
	// and idempotent at the gate-check layer.
	updated, err := s.stripe.UpdateSubscriptionCancel(ctx, subID, false, "")
	if err != nil {
		return fmt.Errorf("stripe reverse: %w", err)
	}
	if updated.Items == nil || len(updated.Items.Data) == 0 {
		return fmt.Errorf("stripe reverse: subscription %s missing items", subID)
	}
	periodStart := time.Unix(updated.Items.Data[0].CurrentPeriodStart, 0).UTC()
	periodEnd := time.Unix(updated.Items.Data[0].CurrentPeriodEnd, 0).UTC()

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op
	qtx := db.New(tx)

	if err := qtx.ClearUserAutoCancelState(ctx, db.ClearUserAutoCancelStateParams{
		ID: userID, SubCurrentPeriodStart: pgxTime(periodStart),
	}); err != nil {
		return fmt.Errorf("clear gates: %w", err)
	}

	mdJSON, err := marshalKeptMetadata(subID, "auto_activity", periodStart)
	if err != nil {
		return fmt.Errorf("marshal kept metadata: %w", err)
	}
	if err := qtx.InsertSubscriptionKeptEvent(ctx, db.InsertSubscriptionKeptEventParams{
		UserID: userID, Metadata: mdJSON,
	}); err != nil {
		return fmt.Errorf("insert kept event: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	mReverseActivity(ctx, subID)

	if err := s.enqueueKeptEmail(ctx, userID, subID, periodEnd); err != nil {
		mEmailEnqueue(ctx, "kept", "error")
		s.log.Error("kept email enqueue failed", "user_id", userID, "sub_id", subID, "err", err)
	} else {
		mEmailEnqueue(ctx, "kept", "ok")
	}
	return nil
}

func marshalKeptMetadata(subID, via string, periodStart time.Time) ([]byte, error) {
	return json.Marshal(keptMetadata{
		SubscriptionID:     subID,
		Via:                via,
		CurrentPeriodStart: periodStart.UTC(),
	})
}

// enqueueKeptEmail composes the kept-confirmation email and sends it.
// Called from KeepSubscription and AutoReverse after a successful reversal.
func (s *Service) enqueueKeptEmail(ctx context.Context, userID uuid.UUID, _ string, periodEnd time.Time) error {
	q := db.New(s.pool)
	user, err := q.GetUserByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("get user: %w", err)
	}
	msg, err := composeKeptEmail(user.Email, user.DisplayName, periodEnd)
	if err != nil {
		return fmt.Errorf("compose kept: %w", err)
	}
	return s.mailer.Send(ctx, msg)
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
