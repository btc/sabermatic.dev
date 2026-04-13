package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stripe/stripe-go/v82"
)

// Runner holds the shared dependencies for every scenario subcommand.
// It asserts test-mode credentials in its constructor, opens a pool,
// and sets the Stripe SDK's package-level API key.
type Runner struct {
	pool *pgxpool.Pool
}

func newRunner(ctx context.Context) (*Runner, error) {
	key := os.Getenv("STRIPE_SECRET_KEY")
	if !strings.HasPrefix(key, "sk_test_") {
		return nil, fmt.Errorf("refusing to run: STRIPE_SECRET_KEY missing or not a test key")
	}
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return nil, fmt.Errorf("DATABASE_URL not set")
	}

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return nil, fmt.Errorf("db pool: %w", err)
	}

	stripe.Key = key
	return &Runner{pool: pool}, nil
}

func (r *Runner) Close() {
	if r.pool != nil {
		r.pool.Close()
	}
}

// runStripeCLI shells out to the local `stripe` binary, piping stdio through.
// Takes ctx so Ctrl-C propagates to the spawned process. No callers until
// Task 4 wires packbuy.go — expected dead code at the end of Task 3.
func (r *Runner) runStripeCLI(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "stripe", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("stripe %s: %w", strings.Join(args, " "), err)
	}
	return nil
}
