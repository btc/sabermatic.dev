package main

import (
	"context"
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/subscription"
	"github.com/urfave/cli/v3"

	"github.com/btc/drill/internal/db"
)

func subCancelCmd() *cli.Command {
	return &cli.Command{
		Name:  "sub-cancel",
		Usage: "Cancel active subscriptions for a user (fires subscription.deleted)",
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
			return r.subCancel(ctx, userID)
		},
	}
}

// subCancel reads the user's stripe_customer_id, lists all non-canceled
// subscriptions under that customer (no Status filter — covers active,
// trialing, past_due), and cancels each immediately.
func (r *Runner) subCancel(ctx context.Context, userID uuid.UUID) error {
	q := db.New(r.pool)
	user, err := q.GetUserByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("lookup user %s: %w", userID, err)
	}
	if !user.StripeCustomerID.Valid {
		return fmt.Errorf("user %s has no stripe_customer_id; run sub-start first", userID)
	}

	iter := subscription.List(&stripe.SubscriptionListParams{
		Customer: stripe.String(user.StripeCustomerID.String),
	})

	canceled := 0
	for iter.Next() {
		s := iter.Subscription()
		// Skip already-terminal subs.
		if s.Status == stripe.SubscriptionStatusCanceled ||
			s.Status == stripe.SubscriptionStatusIncompleteExpired {
			continue
		}
		if _, err := subscription.Cancel(s.ID, nil); err != nil {
			return fmt.Errorf("cancel %s: %w", s.ID, err)
		}
		fmt.Fprintf(os.Stdout, "canceled %s\n", s.ID)
		canceled++
	}
	if err := iter.Err(); err != nil {
		return fmt.Errorf("list subscriptions: %w", err)
	}
	if canceled == 0 {
		fmt.Fprintf(os.Stderr, "no active subscriptions for user %s\n", userID)
	}
	return nil
}
