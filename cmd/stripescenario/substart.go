package main

import (
	"context"
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/customer"
	"github.com/stripe/stripe-go/v82/subscription"
	"github.com/urfave/cli/v3"

	"github.com/btc/drill/internal/db"
)

func subStartCmd() *cli.Command {
	return &cli.Command{
		Name:  "sub-start",
		Usage: "Create Stripe customer + subscription for a user (fires invoice.paid)",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "user", Required: true, Usage: "target user UUID"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			userID, err := uuid.Parse(cmd.String("user"))
			if err != nil {
				return fmt.Errorf("invalid --user UUID: %w", err)
			}
			r, err := newRunner(ctx)
			if err != nil {
				return err
			}
			defer r.Close()
			return r.subStart(ctx, userID)
		},
	}
}

// subStart drives the real Stripe SDK to create a customer + subscription.
// A CLI-fixture `stripe trigger invoice.paid` would emit an event against
// the fixture's own ephemeral customer, not ours, so handleInvoicePaid's
// customer-lookup would hit n==0. Real SDK calls produce real webhooks.
func (r *Runner) subStart(ctx context.Context, userID uuid.UUID) error {
	priceID := os.Getenv("STRIPE_PRO_PRICE_ID")
	if priceID == "" {
		return fmt.Errorf("STRIPE_PRO_PRICE_ID not set in env")
	}

	q := db.New(r.pool)

	// Existence check: UpdateUserStripeCustomerID is `:exec` and silently
	// succeeds on zero rows affected. Do the check before creating anything
	// in Stripe so we don't orphan resources on a bad user ID.
	if _, err := q.GetUserByID(ctx, userID); err != nil {
		return fmt.Errorf("lookup user %s: %w", userID, err)
	}

	// pm_card_visa is a stable test-mode PaymentMethod fixture; gated by the
	// sk_test_ assertion in newRunner. In live mode Stripe would reject it.
	// Note: existing code in internal/backend/billing.go uses the deprecated
	// stripe.Params.Metadata form; prefer the top-level field in new code.
	// Tech-debt cleanup for billing.go belongs in a separate change.
	cust, err := customer.New(&stripe.CustomerParams{
		PaymentMethod: stripe.String("pm_card_visa"),
		InvoiceSettings: &stripe.CustomerInvoiceSettingsParams{
			DefaultPaymentMethod: stripe.String("pm_card_visa"),
		},
		Metadata: map[string]string{"drill_user_id": userID.String()},
	})
	if err != nil {
		return fmt.Errorf("create stripe customer: %w", err)
	}

	if err := q.UpdateUserStripeCustomerID(ctx, db.UpdateUserStripeCustomerIDParams{
		ID:               userID,
		StripeCustomerID: pgtype.Text{String: cust.ID, Valid: true},
	}); err != nil {
		return fmt.Errorf("persist stripe_customer_id: %w", err)
	}

	// collection_method defaults to charge_automatically; with pm_card_visa
	// attached and set as default, Stripe test mode creates and pays the
	// invoice synchronously, firing invoice.paid.
	s, err := subscription.New(&stripe.SubscriptionParams{
		Customer: stripe.String(cust.ID),
		Items: []*stripe.SubscriptionItemsParams{
			{Price: stripe.String(priceID)},
		},
	})
	if err != nil {
		return fmt.Errorf("create subscription: %w", err)
	}

	fmt.Fprintf(os.Stdout, "subscription %s on customer %s for user %s\n", s.ID, cust.ID, userID)
	return nil
}
