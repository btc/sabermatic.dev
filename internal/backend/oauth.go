package backend

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/drilotel"
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
// One transaction, no retry loop. See design spec for details.
func (b *Backend) OAuthLogin(ctx context.Context, p OAuthLoginParams) (_ *OAuthLoginResult, err error) {
	ctx, span := tracer.Start(ctx, "Backend.OAuthLogin")
	defer func() { drilotel.End(span, err) }()

	const (
		pathExistingOAuth  = "existing_oauth"
		pathLinkedExisting = "linked_existing"
		pathReactivated    = "reactivated"
		pathNewUser        = "new_user"
	)

	p.Email = normalizeEmail(p.Email)

	// Read Committed: FOR UPDATE blocks concurrent access to existing rows,
	// and ON CONFLICT DO NOTHING handles concurrent inserts. Higher isolation
	// levels would convert these into serialization failures requiring
	// full-transaction retries.
	tx, err := b.pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel: pgx.ReadCommitted,
	})
	if err != nil {
		return nil, fmt.Errorf("oauth login: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := db.New(tx)

	var user db.User
	var path string

	// --- Resolve user ---

	// (A) Returning user: they've logged in with this provider before.
	oauthAcct, err := q.GetOAuthAccount(ctx, db.GetOAuthAccountParams{
		Provider:   p.Provider,
		ProviderID: p.ProviderID,
	})
	if err == nil {
		user, err = q.GetUserByIDIncludingDeleted(ctx, oauthAcct.UserID)
		if err != nil {
			return nil, fmt.Errorf("oauth login: get user by id: %w", err)
		}
		if user.DeletedAt.Valid {
			path = pathReactivated
		} else {
			path = pathExistingOAuth
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("oauth login: get oauth account: %w", err)
	}

	// (B) First time with this provider, but we already have an account
	//     for their email (e.g. they signed up with password, now adding
	//     Google). Lock the row so a concurrent login can't race us.
	if path == "" {
		user, err = q.GetUserByEmailForUpdate(ctx, p.Email)
		if err == nil {
			if user.DeletedAt.Valid {
				path = pathReactivated
			} else {
				path = pathLinkedExisting
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("oauth login: get user by email: %w", err)
		}
	}

	// (C) Brand new user — no account exists for this email.
	//     In rare cases, another request for the same email may have
	//     created the account between (B) and now (e.g. the user
	//     double-clicked the OAuth button). CreateOAuthUserOrNoop
	//     safely no-ops in that case, and we re-select their row.
	if path == "" {
		user, err = q.CreateOAuthUserOrNoop(ctx, db.CreateOAuthUserOrNoopParams{
			Email:       p.Email,
			DisplayName: p.DisplayName,
		})
		if err == nil {
			path = pathNewUser
		} else if errors.Is(err, pgx.ErrNoRows) {
			// Another request just created this account. Use theirs.
			user, err = q.GetUserByEmailForUpdate(ctx, p.Email)
			if err != nil {
				return nil, fmt.Errorf("oauth login: get user after race: %w", err)
			}
			if user.DeletedAt.Valid {
				path = pathReactivated
			} else {
				path = pathLinkedExisting
			}
		} else {
			return nil, fmt.Errorf("oauth login: create user: %w", err)
		}
	}

	// --- Side effects ---

	switch path {
	case pathNewUser:
		// Provision free grant.
		if err := b.provisionNewUser(ctx, tx, user.ID); err != nil {
			return nil, fmt.Errorf("oauth login: provision: %w", err)
		}
		// Link OAuth account.
		if _, err := q.LinkOAuthAccount(ctx, db.LinkOAuthAccountParams{
			UserID:     user.ID,
			Provider:   p.Provider,
			ProviderID: p.ProviderID,
		}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("oauth login: link account: %w", err)
		}

	case pathReactivated:
		// Clear deleted_at.
		if err := q.ReactivateUser(ctx, user.ID); err != nil {
			return nil, fmt.Errorf("oauth login: reactivate: %w", err)
		}
		// Link OAuth account.
		if _, err := q.LinkOAuthAccount(ctx, db.LinkOAuthAccountParams{
			UserID:     user.ID,
			Provider:   p.Provider,
			ProviderID: p.ProviderID,
		}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("oauth login: link account: %w", err)
		}

	case pathLinkedExisting:
		// Verify email if unverified.
		if !user.EmailVerified {
			if err := q.VerifyUserEmail(ctx, user.ID); err != nil {
				return nil, fmt.Errorf("oauth login: verify email: %w", err)
			}
		}
		// Link OAuth account.
		if _, err := q.LinkOAuthAccount(ctx, db.LinkOAuthAccountParams{
			UserID:     user.ID,
			Provider:   p.Provider,
			ProviderID: p.ProviderID,
		}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("oauth login: link account: %w", err)
		}

	case pathExistingOAuth:
		// Nothing — user and link already exist.
	}

	// --- Auth session (shared with Login) ---

	sess, err := b.createAuthSession(ctx, tx, AuthSessionParams{
		UserID:    user.ID,
		IP:        p.IP,
		UserAgent: p.UserAgent,
	})
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("oauth login: commit: %w", err)
	}

	slog.Info("oauth login", "provider", p.Provider, "email", user.Email, "path", path, "user_id", user.ID)

	return &OAuthLoginResult{
		UserID:       user.ID,
		Email:        user.Email,
		Token:        sess.Token,
		NeedsProfile: user.DisplayName == "",
	}, nil
}
