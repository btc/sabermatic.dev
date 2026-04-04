package backend

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	stripe "github.com/stripe/stripe-go/v82"
	portalsession "github.com/stripe/stripe-go/v82/billingportal/session"
	checkoutsession "github.com/stripe/stripe-go/v82/checkout/session"
	"github.com/stripe/stripe-go/v82/customer"

	"github.com/btc/drill/internal/billing"
	"github.com/btc/drill/internal/db"
)

// ---------------------------------------------------------------------------
// Params
// ---------------------------------------------------------------------------

// CheckoutParams holds the parameters for CreateCheckoutSession.
type CheckoutParams struct {
	UserID      uuid.UUID
	Email       string
	Type        string // "subscription" or "pack"
	Plan        string // for subscription
	PackMinutes int    // for pack
}

// ---------------------------------------------------------------------------
// Public methods
// ---------------------------------------------------------------------------

// Check returns the user's billing entitlements (single DB query).
func (b *Backend) Check(ctx context.Context, userID uuid.UUID) (billing.Entitlements, error) {
	bs, err := db.New(b.pool).GetBillingSnapshot(ctx, userID)
	if err != nil {
		return billing.Entitlements{}, fmt.Errorf("get billing snapshot: %w", err)
	}
	return billing.Resolve(snapshotFrom(bs)), nil
}

// EnsureFreeGrant creates the current-month free grant for the user if it does
// not already exist. The SQL atomically creates both the grant and its ledger
// entry in a single writable CTE. If the grant already exists (ON CONFLICT),
// both inserts no-op.
func (b *Backend) EnsureFreeGrant(ctx context.Context, userID uuid.UUID, planName string) error {
	plan, ok := billing.PlanByName(planName)
	if !ok {
		return fmt.Errorf("unknown plan %q", planName)
	}
	return db.New(b.pool).EnsureFreeGrant(ctx, db.EnsureFreeGrantParams{
		UserID:         userID,
		InitialMinutes: int32(plan.MinutesPerMonth),
		ExpiresAt:      pgtype.Timestamptz{Time: billing.EndOfMonth(time.Now().UTC()), Valid: true},
	})
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
		TotalBalance:   summary.TotalBalance,
		FreeBalance:    summary.FreeBalance,
		PaidBalance:    summary.PaidBalance,
		Grants:         grants,
		RecentActivity: entries,
	}, nil
}

// CreateCheckoutSession creates a Stripe Checkout session for a subscription or
// one-time minute-pack purchase. Returns the checkout URL.
func (b *Backend) CreateCheckoutSession(ctx context.Context, p CheckoutParams) (string, error) {
	if b.cfg.Stripe.SecretKey == "" {
		return "", fmt.Errorf("stripe not configured")
	}

	q := db.New(b.pool)
	user, err := q.GetUserByID(ctx, p.UserID)
	if err != nil {
		return "", fmt.Errorf("get user: %w", err)
	}

	custID := user.StripeCustomerID.String
	if !user.StripeCustomerID.Valid || custID == "" {
		cust, err := customer.New(&stripe.CustomerParams{
			Email: stripe.String(p.Email),
			Params: stripe.Params{
				Metadata: map[string]string{"user_id": p.UserID.String()},
			},
		})
		if err != nil {
			return "", fmt.Errorf("create stripe customer: %w", err)
		}
		custID = cust.ID
		if err := q.UpdateUserStripeCustomerID(ctx, db.UpdateUserStripeCustomerIDParams{
			ID:               p.UserID,
			StripeCustomerID: pgtype.Text{String: custID, Valid: true},
		}); err != nil {
			return "", fmt.Errorf("save stripe customer id: %w", err)
		}
	}

	var mode string
	var priceID string
	metadata := map[string]string{
		"user_id": p.UserID.String(),
		"type":    p.Type,
	}

	switch p.Type {
	case "subscription":
		mode = string(stripe.CheckoutSessionModeSubscription)
		if _, ok := billing.PlanByName(p.Plan); !ok {
			return "", fmt.Errorf("unknown plan %q", p.Plan)
		}
		priceID = b.cfg.Stripe.PriceIDForPlan(p.Plan)
		if priceID == "" {
			return "", fmt.Errorf("plan %q has no Stripe price configured", p.Plan)
		}
	case "pack":
		mode = string(stripe.CheckoutSessionModePayment)
		if !billing.ValidPackSize(p.PackMinutes) {
			return "", fmt.Errorf("unknown pack size %d", p.PackMinutes)
		}
		priceID = b.cfg.Stripe.PriceIDForPack(p.PackMinutes)
		if priceID == "" {
			return "", fmt.Errorf("pack %d has no Stripe price configured", p.PackMinutes)
		}
		metadata["pack_minutes"] = strconv.Itoa(p.PackMinutes)
	default:
		return "", fmt.Errorf("invalid checkout type %q", p.Type)
	}

	baseURL := b.cfg.Auth.BaseURL
	sess, err := checkoutsession.New(&stripe.CheckoutSessionParams{
		Customer: stripe.String(custID),
		Mode:     stripe.String(mode),
		LineItems: []*stripe.CheckoutSessionLineItemParams{
			{Price: stripe.String(priceID), Quantity: stripe.Int64(1)},
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

// CreatePortalSession creates a Stripe billing portal session for the user.
func (b *Backend) CreatePortalSession(ctx context.Context, userID uuid.UUID) (string, error) {
	if b.cfg.Stripe.SecretKey == "" {
		return "", fmt.Errorf("stripe not configured")
	}

	user, err := db.New(b.pool).GetUserByID(ctx, userID)
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

// HandleStripeWebhook dispatches a Stripe webhook event.
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

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

// UsageSummary holds the balance breakdown, active grants, and recent ledger
// entries for a user.
type UsageSummary struct {
	TotalBalance   int32                         `json:"total_balance"`
	FreeBalance    int32                         `json:"free_balance"`
	PaidBalance    int32                         `json:"paid_balance"`
	Grants         []db.ListActiveGrantsRow      `json:"grants"`
	RecentActivity []db.GetRecentLedgerEntriesRow `json:"recent_activity"`
}

// ---------------------------------------------------------------------------
// Private methods
// ---------------------------------------------------------------------------

// snapshotFrom converts the sqlc-generated row to the billing package's snapshot type.
func snapshotFrom(row db.GetBillingSnapshotRow) billing.BillingSnapshot {
	return billing.BillingSnapshot{
		Plan:                  row.Plan,
		FreeFullEducatorsUsed: int(row.FreeFullEducatorsUsed),
		TotalBalance:          int(row.TotalBalance),
		PaidBalance:           int(row.PaidBalance),
	}
}

// reserveMinutesTx reserves minutes via a single SQL recursive CTE.
// Returns ErrInsufficientBalance if balance < requested.
func (b *Backend) reserveMinutesTx(ctx context.Context, dbtx db.DBTX, userID uuid.UUID, sessionID uuid.UUID, minutes int32) error {
	rows, err := db.New(dbtx).ReserveMinutes(ctx, db.ReserveMinutesParams{
		UserID:    userID,
		SessionID: pgtype.UUID{Bytes: sessionID, Valid: true},
		Minutes:   minutes,
	})
	if err != nil {
		return fmt.Errorf("reserve minutes: %w", err)
	}
	if len(rows) == 0 {
		return ErrInsufficientBalance
	}
	return nil
}

func (b *Backend) handleCheckoutCompleted(ctx context.Context, event stripe.Event) error {
	mode := event.GetObjectValue("mode")
	if mode != "payment" {
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

	if err := db.New(b.pool).CreatePurchaseGrant(ctx, db.CreatePurchaseGrantParams{
		UserID:        userID,
		StripeEventID: pgtype.Text{String: event.ID, Valid: true},
		Minutes:       int32(minutes),
	}); err != nil {
		return fmt.Errorf("create purchase grant: %w", err)
	}

	slog.Info("purchase grant created", "user_id", userID, "minutes", minutes, "event_id", event.ID)
	return nil
}

func (b *Backend) handleInvoicePaid(ctx context.Context, event stripe.Event) error {
	custID := event.GetObjectValue("customer")
	if custID == "" {
		slog.Warn("invoice.paid missing customer", "event_id", event.ID)
		return nil
	}

	plan, ok := billing.PlanByName("pro")
	if !ok {
		return fmt.Errorf("unknown plan %q", "pro")
	}

	result, err := db.New(b.pool).CreateSubscriptionGrant(ctx, db.CreateSubscriptionGrantParams{
		CustID:        pgtype.Text{String: custID, Valid: true},
		StripeEventID: pgtype.Text{String: event.ID, Valid: true},
		Minutes:       int32(plan.MinutesPerMonth),
		Plan:          "pro",
	})
	if err != nil {
		return fmt.Errorf("create subscription grant: %w", err)
	}
	if result.UserFound == 0 {
		slog.Warn("invoice.paid unknown customer", "event_id", event.ID, "customer_id", custID)
		return nil
	}
	if result.GrantsCreated == 0 {
		return nil // idempotent: already processed
	}

	slog.Info("subscription grant created", "customer_id", custID, "plan", "pro", "event_id", event.ID)
	return nil
}

func (b *Backend) handleSubscriptionDeleted(ctx context.Context, event stripe.Event) error {
	custID := event.GetObjectValue("customer")
	if custID == "" {
		slog.Warn("subscription.deleted missing customer", "event_id", event.ID)
		return nil
	}

	n, err := db.New(b.pool).UpdatePlanByStripeCustomer(ctx, db.UpdatePlanByStripeCustomerParams{
		Plan:             "free",
		StripeCustomerID: pgtype.Text{String: custID, Valid: true},
	})
	if err != nil {
		return fmt.Errorf("revert plan to free: %w", err)
	}
	if n == 0 {
		slog.Warn("subscription.deleted unknown customer", "event_id", event.ID, "customer_id", custID)
		return nil
	}

	slog.Info("subscription deleted, plan set to free", "customer_id", custID, "event_id", event.ID)
	return nil
}

func (b *Backend) handleSubscriptionUpdated(ctx context.Context, event stripe.Event) error {
	custID := event.GetObjectValue("customer")
	if custID == "" {
		slog.Warn("subscription.updated missing customer", "event_id", event.ID)
		return nil
	}

	status := event.GetObjectValue("status")
	planName := "pro"
	if status == "canceled" || status == "unpaid" || status == "past_due" {
		planName = "free"
	}

	n, err := db.New(b.pool).UpdatePlanByStripeCustomer(ctx, db.UpdatePlanByStripeCustomerParams{
		Plan:             planName,
		StripeCustomerID: pgtype.Text{String: custID, Valid: true},
	})
	if err != nil {
		return fmt.Errorf("update user plan: %w", err)
	}
	if n == 0 {
		slog.Warn("subscription.updated unknown customer", "event_id", event.ID, "customer_id", custID)
		return nil
	}

	slog.Info("subscription updated", "customer_id", custID, "plan", planName, "status", status, "event_id", event.ID)
	return nil
}
