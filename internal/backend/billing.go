package backend

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	stripe "github.com/stripe/stripe-go/v82"
	portalsession "github.com/stripe/stripe-go/v82/billingportal/session"
	checkoutsession "github.com/stripe/stripe-go/v82/checkout/session"
	"github.com/stripe/stripe-go/v82/customer"

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

	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin free grant tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	q := db.New(tx)
	grant, err := q.CreateFreeGrant(ctx, db.CreateFreeGrantParams{
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

	// Record the grant creation in the ledger for auditability.
	if _, err := q.InsertLedgerEntry(ctx, db.InsertLedgerEntryParams{
		UserID:    userID,
		GrantID:   grant.ID,
		Amount:    int32(plan.MinutesPerMonth),
		Reason:    "free_monthly",
		SessionID: pgtype.UUID{},
	}); err != nil {
		return fmt.Errorf("insert free grant ledger entry: %w", err)
	}

	return tx.Commit(ctx)
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

	if err := b.refundMinutesTx(ctx, tx, userID, sessionID, minutes, "session_refund"); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit refund tx: %w", err)
	}
	return nil
}

// refundMinutesTx performs the refund logic within an existing transaction.
// Used by CompleteSession (Task 8) to refund within the completion transaction.
func (b *Backend) refundMinutesTx(ctx context.Context, dbtx db.DBTX, userID uuid.UUID, sessionID uuid.UUID, minutes int32, reason string) error {
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
			Reason:    reason,
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
	RecentActivity []db.GetRecentLedgerEntriesRow `json:"recent_activity"`
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
		RecentActivity: entries,
	}, nil
}

// ---------------------------------------------------------------------------
// CreateCheckoutSession
// ---------------------------------------------------------------------------

// CreateCheckoutSession creates a Stripe Checkout session for a subscription or
// one-time minute-pack purchase. It returns the checkout URL. If the user has
// no Stripe customer yet, one is created and persisted.
func (b *Backend) CreateCheckoutSession(ctx context.Context, userID uuid.UUID, email, checkoutType, planName string, packMinutes int) (string, error) {
	if b.cfg.Stripe.SecretKey == "" {
		return "", fmt.Errorf("stripe not configured")
	}

	q := db.New(b.pool)
	user, err := q.GetUserByID(ctx, userID)
	if err != nil {
		return "", fmt.Errorf("get user: %w", err)
	}

	// Ensure the user has a Stripe customer ID.
	custID := user.StripeCustomerID.String
	if !user.StripeCustomerID.Valid || custID == "" {
		cust, err := customer.New(&stripe.CustomerParams{
			Email: stripe.String(email),
			Params: stripe.Params{
				Metadata: map[string]string{
					"user_id": userID.String(),
				},
			},
		})
		if err != nil {
			return "", fmt.Errorf("create stripe customer: %w", err)
		}
		custID = cust.ID
		if err := q.UpdateUserStripeCustomerID(ctx, db.UpdateUserStripeCustomerIDParams{
			ID:               userID,
			StripeCustomerID: pgtype.Text{String: custID, Valid: true},
		}); err != nil {
			return "", fmt.Errorf("save stripe customer id: %w", err)
		}
	}

	// Determine mode and price ID.
	var mode string
	var priceID string
	metadata := map[string]string{
		"user_id": userID.String(),
		"type":    checkoutType,
	}

	switch checkoutType {
	case "subscription":
		mode = string(stripe.CheckoutSessionModeSubscription)
		plan, ok := billing.PlanByName(planName)
		if !ok {
			return "", fmt.Errorf("unknown plan %q", planName)
		}
		if plan.StripePriceID == "" {
			return "", fmt.Errorf("plan %q has no Stripe price configured", planName)
		}
		priceID = plan.StripePriceID
	case "pack":
		mode = string(stripe.CheckoutSessionModePayment)
		pack, ok := billing.PackByMinutes(packMinutes)
		if !ok {
			return "", fmt.Errorf("unknown pack size %d", packMinutes)
		}
		if pack.StripePriceID == "" {
			return "", fmt.Errorf("pack %d has no Stripe price configured", packMinutes)
		}
		priceID = pack.StripePriceID
		metadata["pack_minutes"] = strconv.Itoa(packMinutes)
	default:
		return "", fmt.Errorf("invalid checkout type %q", checkoutType)
	}

	baseURL := b.cfg.Auth.BaseURL
	sess, err := checkoutsession.New(&stripe.CheckoutSessionParams{
		Customer: stripe.String(custID),
		Mode:     stripe.String(mode),
		LineItems: []*stripe.CheckoutSessionLineItemParams{
			{
				Price:    stripe.String(priceID),
				Quantity: stripe.Int64(1),
			},
		},
		SuccessURL: stripe.String(baseURL + "/settings/billing?success=1"),
		CancelURL:  stripe.String(baseURL + "/settings/billing?canceled=1"),
		Metadata:   metadata,
	})
	if err != nil {
		return "", fmt.Errorf("create checkout session: %w", err)
	}

	return sess.URL, nil
}

// ---------------------------------------------------------------------------
// CreatePortalSession
// ---------------------------------------------------------------------------

// CreatePortalSession creates a Stripe billing portal session for the user.
// Returns the portal URL. Returns ErrNoStripeAccount if the user has no
// Stripe customer on file.
func (b *Backend) CreatePortalSession(ctx context.Context, userID uuid.UUID) (string, error) {
	if b.cfg.Stripe.SecretKey == "" {
		return "", fmt.Errorf("stripe not configured")
	}

	q := db.New(b.pool)
	user, err := q.GetUserByID(ctx, userID)
	if err != nil {
		return "", fmt.Errorf("get user: %w", err)
	}

	if !user.StripeCustomerID.Valid || user.StripeCustomerID.String == "" {
		return "", ErrNoStripeAccount
	}

	baseURL := b.cfg.Auth.BaseURL
	sess, err := portalsession.New(&stripe.BillingPortalSessionParams{
		Customer:  stripe.String(user.StripeCustomerID.String),
		ReturnURL: stripe.String(baseURL + "/settings/billing"),
	})
	if err != nil {
		return "", fmt.Errorf("create portal session: %w", err)
	}

	return sess.URL, nil
}

// ---------------------------------------------------------------------------
// HandleStripeWebhook
// ---------------------------------------------------------------------------

// HandleStripeWebhook dispatches a Stripe webhook event. It handles checkout
// completions (purchase grants), invoice payments (subscription grants),
// subscription deletions, and subscription updates.
func (b *Backend) HandleStripeWebhook(ctx context.Context, event stripe.Event) error {
	switch event.Type {
	case "checkout.session.completed":
		return b.handleCheckoutCompleted(ctx, event)
	case "invoice.paid":
		return b.handleInvoicePaid(ctx, event)
	case "customer.subscription.deleted":
		return b.handleSubscriptionDeleted(ctx, event)
	case "customer.subscription.updated":
		return b.handleSubscriptionUpdated(ctx, event)
	default:
		slog.Info("unhandled stripe event", "type", event.Type, "id", event.ID)
		return nil
	}
}

// handleCheckoutCompleted processes checkout.session.completed events. For
// one-time payments (mode=payment), it creates a purchase grant and ledger
// entry. Subscription checkouts are handled by invoice.paid instead.
func (b *Backend) handleCheckoutCompleted(ctx context.Context, event stripe.Event) error {
	mode := event.GetObjectValue("mode")
	if mode != "payment" {
		// Subscription checkouts are handled via invoice.paid.
		return nil
	}

	userIDStr := event.GetObjectValue("metadata", "user_id")
	if userIDStr == "" {
		slog.Warn("checkout.session.completed missing user_id metadata", "event_id", event.ID)
		return nil
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		slog.Warn("checkout.session.completed invalid user_id", "event_id", event.ID, "user_id", userIDStr)
		return nil
	}

	minutesStr := event.GetObjectValue("metadata", "pack_minutes")
	if minutesStr == "" {
		slog.Warn("checkout.session.completed missing pack_minutes", "event_id", event.ID)
		return nil
	}
	minutes, err := strconv.Atoi(minutesStr)
	if err != nil {
		slog.Warn("checkout.session.completed invalid pack_minutes", "event_id", event.ID, "pack_minutes", minutesStr)
		return nil
	}

	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	q := db.New(tx)
	grant, err := q.CreateGrantFromStripe(ctx, db.CreateGrantFromStripeParams{
		UserID:         userID,
		Source:         "purchase",
		StripeEventID:  pgtype.Text{String: event.ID, Valid: true},
		InitialMinutes: int32(minutes),
		ExpiresAt:      pgtype.Timestamptz{Valid: false}, // never expires
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Idempotent: grant already created for this event.
			return nil
		}
		return fmt.Errorf("create purchase grant: %w", err)
	}

	_, err = q.InsertLedgerEntry(ctx, db.InsertLedgerEntryParams{
		UserID:    userID,
		GrantID:   grant.ID,
		Amount:    int32(minutes),
		Reason:    "purchase",
		SessionID: pgtype.UUID{}, // NULL
	})
	if err != nil {
		return fmt.Errorf("insert purchase ledger entry: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit purchase tx: %w", err)
	}

	slog.Info("purchase grant created", "user_id", userID, "minutes", minutes, "event_id", event.ID)
	return nil
}

// handleInvoicePaid processes invoice.paid events. It looks up the user by
// stripe_customer_id and creates a subscription grant + ledger entry, then
// updates the user's plan.
func (b *Backend) handleInvoicePaid(ctx context.Context, event stripe.Event) error {
	custID := event.GetObjectValue("customer")
	if custID == "" {
		slog.Warn("invoice.paid missing customer", "event_id", event.ID)
		return nil
	}

	q := db.New(b.pool)
	user, err := q.GetUserByStripeCustomerID(ctx, pgtype.Text{String: custID, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			slog.Warn("invoice.paid unknown customer", "event_id", event.ID, "customer_id", custID)
			return nil // unknown customer, don't retry
		}
		return fmt.Errorf("get user by stripe customer: %w", err) // transient error, retry
	}

	// Determine plan from subscription metadata or default to pro.
	planName := "pro"
	plan, ok := billing.PlanByName(planName)
	if !ok {
		return fmt.Errorf("unknown plan %q", planName)
	}

	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	tq := db.New(tx)
	grant, err := tq.CreateGrantFromStripe(ctx, db.CreateGrantFromStripeParams{
		UserID:         user.ID,
		Source:         "subscription",
		StripeEventID:  pgtype.Text{String: event.ID, Valid: true},
		InitialMinutes: int32(plan.MinutesPerMonth),
		ExpiresAt:      pgtype.Timestamptz{Valid: false}, // never expires
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Idempotent: grant already created for this event.
			return nil
		}
		return fmt.Errorf("create subscription grant: %w", err)
	}

	_, err = tq.InsertLedgerEntry(ctx, db.InsertLedgerEntryParams{
		UserID:    user.ID,
		GrantID:   grant.ID,
		Amount:    int32(plan.MinutesPerMonth),
		Reason:    "subscription_renewal",
		SessionID: pgtype.UUID{}, // NULL
	})
	if err != nil {
		return fmt.Errorf("insert subscription ledger entry: %w", err)
	}

	if err := tq.UpdateUserPlan(ctx, db.UpdateUserPlanParams{
		ID:   user.ID,
		Plan: planName,
	}); err != nil {
		return fmt.Errorf("update user plan: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit subscription tx: %w", err)
	}

	slog.Info("subscription grant created", "user_id", user.ID, "plan", planName, "event_id", event.ID)
	return nil
}

// handleSubscriptionDeleted processes customer.subscription.deleted events.
// It reverts the user's plan to "free".
func (b *Backend) handleSubscriptionDeleted(ctx context.Context, event stripe.Event) error {
	custID := event.GetObjectValue("customer")
	if custID == "" {
		slog.Warn("subscription.deleted missing customer", "event_id", event.ID)
		return nil
	}

	q := db.New(b.pool)
	user, err := q.GetUserByStripeCustomerID(ctx, pgtype.Text{String: custID, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			slog.Warn("subscription.deleted unknown customer", "event_id", event.ID, "customer_id", custID)
			return nil
		}
		return fmt.Errorf("get user by stripe customer: %w", err)
	}

	if err := q.UpdateUserPlan(ctx, db.UpdateUserPlanParams{
		ID:   user.ID,
		Plan: "free",
	}); err != nil {
		return fmt.Errorf("update user plan to free: %w", err)
	}

	slog.Info("subscription deleted, plan set to free", "user_id", user.ID, "event_id", event.ID)
	return nil
}

// handleSubscriptionUpdated processes customer.subscription.updated events.
// It syncs the user's plan based on subscription status.
func (b *Backend) handleSubscriptionUpdated(ctx context.Context, event stripe.Event) error {
	custID := event.GetObjectValue("customer")
	if custID == "" {
		slog.Warn("subscription.updated missing customer", "event_id", event.ID)
		return nil
	}

	q := db.New(b.pool)
	user, err := q.GetUserByStripeCustomerID(ctx, pgtype.Text{String: custID, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			slog.Warn("subscription.updated unknown customer", "event_id", event.ID, "customer_id", custID)
			return nil
		}
		return fmt.Errorf("get user by stripe customer: %w", err)
	}

	status := event.GetObjectValue("status")
	planName := "pro"
	if status == "canceled" || status == "unpaid" || status == "past_due" {
		planName = "free"
	}

	if err := q.UpdateUserPlan(ctx, db.UpdateUserPlanParams{
		ID:   user.ID,
		Plan: planName,
	}); err != nil {
		return fmt.Errorf("update user plan: %w", err)
	}

	slog.Info("subscription updated", "user_id", user.ID, "plan", planName, "status", status, "event_id", event.ID)
	return nil
}
