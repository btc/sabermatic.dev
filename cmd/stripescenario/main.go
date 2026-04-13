// Command stripescenario drives Stripe webhook scenarios against the local
// dev server. Fires real events through the developer's Stripe test account;
// the `stripe listen` process under make dev forwards them to the webhook.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/joho/godotenv"
	"github.com/urfave/cli/v3"
)

func main() {
	// .env is optional here; many CI-ish invocations set env directly.
	_ = godotenv.Load()

	cmd := &cli.Command{
		Name:  "stripescenario",
		Usage: "Drive Stripe webhook scenarios against the local dev server",
		Commands: []*cli.Command{
			packBuyCmd(),
			// subStartCmd(), subCancelCmd(), resendCmd() added in later tasks
		},
	}

	if err := cmd.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		os.Exit(1)
	}
}
