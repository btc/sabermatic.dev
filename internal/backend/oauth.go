package backend

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/db"
)

// OAuthLoginParams holds the parameters for OAuthLogin.
type OAuthLoginParams struct {
	Provider    string // "google" or "github"
	ProviderID  string // unique ID from the OAuth provider
	Email       string
	DisplayName string
	IP          string
	UserAgent   string
}

// OAuthLoginResult is returned by OAuthLogin on success.
type OAuthLoginResult struct {
	UserID       uuid.UUID
	Email        string
	Token        string
	NeedsProfile bool // true when display_name is empty
}

// OAuthLogin finds or creates a user from an OAuth provider callback.
// All database operations are performed within a single transaction.
// On unique-violation race conditions, the method retries once.
func (b *Backend) OAuthLogin(ctx context.Context, p OAuthLoginParams) (*OAuthLoginResult, error) {
	return b.oauthLoginWithRetry(ctx, p, false)
}

func (b *Backend) oauthLoginWithRetry(ctx context.Context, p OAuthLoginParams, isRetry bool) (*OAuthLoginResult, error) {
	p.Email = strings.ToLower(strings.TrimSpace(p.Email))

	tx, err := b.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("oauth login: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	queries := db.New(tx)

	// Step 1: Look up by (provider, provider_id).
	oauthAcct, err := queries.GetOAuthAccount(ctx, db.GetOAuthAccountParams{
		Provider:   p.Provider,
		ProviderID: p.ProviderID,
	})
	if err == nil {
		// Found existing OAuth account — load user (including soft-deleted).
		user, err := queries.GetUserByIDIncludingDeleted(ctx, oauthAcct.UserID)
		if err != nil {
			return nil, fmt.Errorf("oauth login: get user by id: %w", err)
		}
		path := "existing_oauth"
		if user.DeletedAt.Valid {
			if err := queries.ReactivateUser(ctx, user.ID); err != nil {
				return nil, fmt.Errorf("oauth login: reactivate user: %w", err)
			}
			path = "reactivated"
		}
		result, err := b.createSessionInTx(ctx, tx, queries, user, p)
		if err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("oauth login: commit: %w", err)
		}
		slog.Info("oauth login", "provider", p.Provider, "email", user.Email, "path", path, "user_id", user.ID)
		return result, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("oauth login: get oauth account: %w", err)
	}

	// Step 2: Look up user by email (including soft-deleted).
	user, err := queries.GetUserByEmailIncludingDeleted(ctx, p.Email)
	if err == nil {
		path := "linked_existing"

		// Reactivate if soft-deleted.
		if user.DeletedAt.Valid {
			if err := queries.ReactivateUser(ctx, user.ID); err != nil {
				return nil, fmt.Errorf("oauth login: reactivate user: %w", err)
			}
			path = "reactivated"
		} else if !user.EmailVerified {
			// Verify email if not already verified.
			if err := queries.VerifyUserEmail(ctx, user.ID); err != nil {
				return nil, fmt.Errorf("oauth login: verify email: %w", err)
			}
		}

		// Link OAuth account.
		_, err := queries.CreateOAuthAccount(ctx, db.CreateOAuthAccountParams{
			UserID:     user.ID,
			Provider:   p.Provider,
			ProviderID: p.ProviderID,
		})
		if err != nil {
			if isDuplicateKeyError(err) {
				// Race condition: another request linked this provider_id first.
				// Rollback and retry — will hit step 1 on retry.
				tx.Rollback(ctx) //nolint:errcheck
				if isRetry {
					return nil, fmt.Errorf("oauth login: duplicate key after retry")
				}
				return b.oauthLoginWithRetry(ctx, p, true)
			}
			return nil, fmt.Errorf("oauth login: create oauth account: %w", err)
		}

		result, err := b.createSessionInTx(ctx, tx, queries, user, p)
		if err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("oauth login: commit: %w", err)
		}
		slog.Info("oauth login", "provider", p.Provider, "email", user.Email, "path", path, "user_id", user.ID)
		return result, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("oauth login: get user by email: %w", err)
	}

	// Step 3: Neither found — create new user + OAuth account.
	user, err = queries.CreateOAuthUser(ctx, db.CreateOAuthUserParams{
		Email:       p.Email,
		DisplayName: p.DisplayName,
	})
	if err != nil {
		if isDuplicateKeyError(err) {
			// Race condition: another request created this email first.
			// Rollback and retry — will hit step 2 on retry.
			tx.Rollback(ctx) //nolint:errcheck
			return b.OAuthLogin(ctx, p)
		}
		return nil, fmt.Errorf("oauth login: create oauth user: %w", err)
	}

	_, err = queries.CreateOAuthAccount(ctx, db.CreateOAuthAccountParams{
		UserID:     user.ID,
		Provider:   p.Provider,
		ProviderID: p.ProviderID,
	})
	if err != nil {
		if isDuplicateKeyError(err) {
			// Race condition on provider_id.
			tx.Rollback(ctx) //nolint:errcheck
			return b.OAuthLogin(ctx, p)
		}
		return nil, fmt.Errorf("oauth login: create oauth account: %w", err)
	}

	result, err := b.createSessionInTx(ctx, tx, queries, user, p)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("oauth login: commit: %w", err)
	}
	slog.Info("oauth login", "provider", p.Provider, "email", user.Email, "path", "new_user", "user_id", user.ID)
	return result, nil
}

// createSessionInTx generates a session token, creates an auth session within
// the given transaction, and returns the OAuthLoginResult.
func (b *Backend) createSessionInTx(ctx context.Context, tx pgx.Tx, queries *db.Queries, user db.User, p OAuthLoginParams) (*OAuthLoginResult, error) {
	rawToken, tokenHash, err := auth.GenerateSessionToken()
	if err != nil {
		return nil, fmt.Errorf("oauth login: generate session token: %w", err)
	}

	ipAddr := parseClientIP(p.IP)

	_, err = queries.CreateAuthSession(ctx, db.CreateAuthSessionParams{
		UserID:    user.ID,
		TokenHash: tokenHash,
		ExpiresAt: time.Now().Add(b.cfg.Auth.SessionTTL),
		IpAddress: ipAddr,
		UserAgent: pgtype.Text{String: p.UserAgent, Valid: p.UserAgent != ""},
	})
	if err != nil {
		return nil, fmt.Errorf("oauth login: create auth session: %w", err)
	}

	return &OAuthLoginResult{
		UserID:       user.ID,
		Email:        user.Email,
		Token:        rawToken,
		NeedsProfile: user.DisplayName == "",
	}, nil
}
