package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/urfave/cli/v3"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/billing"
	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/migrate"
)

func main() {
	cmd := &cli.Command{
		Name:  "drillctl",
		Usage: "Drill administration CLI",
		Commands: []*cli.Command{
			seedCmd(),
		},
	}

	if err := cmd.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %s\n", err)
		os.Exit(1)
	}
}

func seedCmd() *cli.Command {
	return &cli.Command{
		Name:  "seed",
		Usage: "Create dev user with free trial grant",
		Action: func(ctx context.Context, _ *cli.Command) error {
			return runSeed(ctx)
		},
	}
}

// runSeed creates a dev user by directly calling db + billing packages,
// bypassing Backend.Signup() to avoid requiring full config (OAuth, jobs, OTel).
// If Signup's provisioning logic changes, update this function to match.
func runSeed(ctx context.Context) error {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}

	if err := migrate.Run(databaseURL); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer pool.Close()

	const (
		email       = "dev@drill.dev"
		password    = "devdevdev123"
		displayName = "Dev User"
		bcryptCost  = 10
	)

	hash, err := auth.HashPassword(password, bcryptCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	queries := db.New(tx)
	user, err := queries.CreateUser(ctx, db.CreateUserParams{
		Email:        email,
		PasswordHash: pgtype.Text{String: hash, Valid: true},
		DisplayName:  displayName,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			slog.Info("dev user already exists, skipping", "email", email)
			return nil
		}
		return fmt.Errorf("create user: %w", err)
	}

	err = queries.EnsureFreeGrant(ctx, db.EnsureFreeGrantParams{
		UserID:         user.ID,
		InitialMinutes: int32(billing.FreeTrialMinutes()),
		ExpiresAt:      pgtype.Timestamptz{Time: billing.FreeGrantExpiry(), Valid: true},
	})
	if err != nil {
		return fmt.Errorf("provision free grant: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	slog.Info("dev user created",
		"email", email,
		"password", password,
		"display_name", displayName,
		"free_minutes", billing.FreeTrialMinutes(),
	)
	return nil
}
