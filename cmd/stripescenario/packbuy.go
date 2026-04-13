package main

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/urfave/cli/v3"

	"github.com/btc/drill/internal/billing"
)

func packBuyCmd() *cli.Command {
	return &cli.Command{
		Name:  "pack-buy",
		Usage: "Fire checkout.session.completed for a user (creates a PurchaseGrant)",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "user", Required: true, Usage: "target user UUID"},
			&cli.IntFlag{Name: "minutes", Value: 120, Usage: "pack size (120, 300, 600)"},
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
			return r.packBuy(ctx, userID, cmd.Int("minutes"))
		},
	}
}

// packBuy validates the pack size against production rules, then shells out
// to `stripe trigger` with metadata fields matching what CreateCheckoutSession
// injects in internal/backend/billing.go.
//
// Uses --add (not --override) because the checkout_session fixture does not
// pre-populate metadata.user_id or metadata.pack_minutes; we are adding fields.
func (r *Runner) packBuy(ctx context.Context, userID uuid.UUID, minutes int) error {
	if !billing.ValidPackSize(minutes) {
		return fmt.Errorf("invalid pack size %d (valid: 120, 300, 600)", minutes)
	}
	return r.runStripeCLI(ctx,
		"trigger", "checkout.session.completed",
		"--add", fmt.Sprintf("checkout_session:metadata.user_id=%s", userID),
		"--add", fmt.Sprintf("checkout_session:metadata.pack_minutes=%d", minutes),
	)
}
