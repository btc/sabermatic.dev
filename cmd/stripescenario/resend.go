package main

import (
	"context"
	"fmt"

	"github.com/urfave/cli/v3"
)

func resendCmd() *cli.Command {
	return &cli.Command{
		Name:      "resend",
		Usage:     "Resend a previously-delivered Stripe event by ID (verifies handler idempotency)",
		ArgsUsage: "<event_id>",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() != 1 {
				return fmt.Errorf("usage: stripescenario resend <event_id>")
			}
			eventID := cmd.Args().First()
			r, err := newRunner(ctx)
			if err != nil {
				return err
			}
			defer r.Close()
			return r.resend(ctx, eventID)
		},
	}
}

// resend shells out to `stripe events resend`; no SDK equivalent exists.
// Dedup guarantees live in the handler: CreatePurchaseGrant and
// CreateSubscriptionGrant use grants.stripe_event_id UNIQUE. Resending
// customer.subscription.* events is NOT a no-op (known handler gap).
func (r *Runner) resend(ctx context.Context, eventID string) error {
	return r.runStripeCLI(ctx, "events", "resend", eventID)
}
