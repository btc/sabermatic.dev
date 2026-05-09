package backend

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/drilotel"
	"github.com/btc/drill/internal/feat/idleunsub"
)

// KeepLinkStatus is the high-level outcome of HandleKeepLink. The handler
// layer maps each value to a rendered HTML page + HTTP status code.
type KeepLinkStatus int

const (
	// KeepLinkInvalid means the token failed signature verification, was
	// malformed, or violated the spec invariants. Render a "bad link" page
	// with HTTP 400.
	KeepLinkInvalid KeepLinkStatus = iota
	// KeepLinkExpired means the signature is valid but the period (and the
	// matching exp) has already passed. Render a "period ended" page with
	// HTTP 200 — the request was authentic, just too late.
	KeepLinkExpired
	// KeepLinkKept means the token was accepted: either the first claim
	// triggered a real reversal, or this is a replay of an already-used
	// token (idempotent — render the same confirmation page).
	KeepLinkKept
)

// KeepLinkOutcome is the data the handler needs to render the response page.
// PeriodEnd is populated for KeepLinkExpired and KeepLinkKept; zero for
// KeepLinkInvalid (no trustworthy claims to expose).
type KeepLinkOutcome struct {
	Status    KeepLinkStatus
	PeriodEnd time.Time
}

// HandleKeepLink verifies a keep-link token, atomically claims single-use,
// and (on first claim) reverses the cancel via idleunsub.KeepSubscription.
// Replays of an already-claimed token return KeepLinkKept idempotently with
// no Stripe side effect.
//
// Returns a non-nil error only on infrastructure failure (DB down, signer
// misconfigured, Stripe failure mid-reversal). Token-level outcomes
// (invalid signature, expired) are returned via Outcome.Status with a nil
// error — the handler renders a page either way.
func (b *Backend) HandleKeepLink(ctx context.Context, token string) (_ KeepLinkOutcome, err error) {
	ctx, span := tracer.Start(ctx, "Backend.HandleKeepLink")
	defer func() { drilotel.End(span, err) }()

	signer := b.idleunsub.Signer()
	if signer == nil {
		// Degraded mode: KEEP_TOKEN_HMAC_KEY was unset at startup, so cancel
		// emails were skipped — there should be no live keep-links pointing
		// here. Treat as invalid so we don't 500 the user.
		return KeepLinkOutcome{Status: KeepLinkInvalid}, nil
	}

	claims, err := signer.Verify(token)
	if err != nil {
		switch {
		case errors.Is(err, idleunsub.ErrTokenInvalid):
			return KeepLinkOutcome{Status: KeepLinkInvalid}, nil
		case errors.Is(err, idleunsub.ErrTokenExpired):
			return KeepLinkOutcome{Status: KeepLinkExpired, PeriodEnd: claims.CurrentPeriodEnd}, nil
		default:
			return KeepLinkOutcome{}, fmt.Errorf("verify keep token: %w", err)
		}
	}

	tokenHash := sha256Of(token)
	if _, err := db.New(b.pool).TryClaimKeepToken(ctx, db.TryClaimKeepTokenParams{
		TokenHash: tokenHash,
		UserID:    claims.UserID,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Replay: the token was already used. Render the same confirmation
			// page idempotently. No Stripe call.
			return KeepLinkOutcome{Status: KeepLinkKept, PeriodEnd: claims.CurrentPeriodEnd}, nil
		}
		return KeepLinkOutcome{}, fmt.Errorf("claim keep token: %w", err)
	}

	// First claim: perform the reversal. KeepSubscription is itself idempotent
	// at the gates layer (manual portal cancels, already-cleared gates) and
	// returns nil in those benign cases — we still render the kept page.
	if err := b.idleunsub.KeepSubscription(ctx, claims); err != nil {
		return KeepLinkOutcome{}, fmt.Errorf("keep subscription: %w", err)
	}
	return KeepLinkOutcome{Status: KeepLinkKept, PeriodEnd: claims.CurrentPeriodEnd}, nil
}

// sha256Of returns the SHA-256 of s as a byte slice. Used to derive the
// keep-link token hash for the single-use claim row.
func sha256Of(s string) []byte {
	sum := sha256.Sum256([]byte(s))
	return sum[:]
}
