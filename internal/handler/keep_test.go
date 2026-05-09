package handler_test

import (
	"context"
	"crypto/rand"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	stripe "github.com/stripe/stripe-go/v82"

	"github.com/btc/drill/internal/backend"
	"github.com/btc/drill/internal/backendtest"
	"github.com/btc/drill/internal/feat/idleunsub"
	"github.com/btc/drill/internal/feat/idleunsub/idleunsubtest"
	"github.com/btc/drill/internal/handler"
)

// Fakes are provided by idleunsubtest; no local definitions needed.

// ---------------------------------------------------------------------------
// Fixture
// ---------------------------------------------------------------------------

type keepFixture struct {
	B           *backend.Backend
	Ctx         context.Context
	UserID      uuid.UUID
	CustID      string
	SubID       string
	PeriodStart time.Time
	PeriodEnd   time.Time
	Sub         *stripe.Subscription
	Fake        *idleunsubtest.FakeStripe
	Signer      *idleunsub.TokenSigner
}

// setupKeepFixture wires a Pro user with a Stripe customer/subscription, sets
// the cache to the auto-canceled shape (per spec §6 — populated by
// SyncSubStateFromWebhook in production), and overrides the Backend's
// idleunsub.Service with a fake-Stripe-backed instance plus a real signer.
func setupKeepFixture(t *testing.T) *keepFixture {
	t.Helper()
	b := pg.NewBackend(t)
	ctx := context.Background()
	userID := backendtest.SeedUser(t, b)
	custID := "cus_keep_" + uuid.NewString()[:8]
	subID := "sub_keep_" + uuid.NewString()[:8]

	now := time.Now().UTC().Truncate(time.Second)
	periodStart := now.AddDate(0, 0, -23)
	periodEnd := periodStart.AddDate(0, 1, 0)

	// Wire user + cache to auto-canceled state.
	_, err := b.Pool().Exec(ctx, `
		UPDATE users
		SET plan                     = 'pro',
		    stripe_customer_id       = $1,
		    stripe_subscription_id   = $2,
		    sub_cancel_at_period_end = TRUE,
		    sub_cancel_is_auto       = TRUE,
		    sub_current_period_start = $3,
		    pending_kept_banner      = FALSE
		WHERE id = $4`, custID, subID, periodStart, userID)
	require.NoError(t, err)

	sub := &stripe.Subscription{
		ID:                subID,
		Status:            stripe.SubscriptionStatusActive,
		CancelAtPeriodEnd: true, // matches the cached state above
		Customer:          &stripe.Customer{ID: custID},
		Items: &stripe.SubscriptionItemList{Data: []*stripe.SubscriptionItem{{
			CurrentPeriodStart: periodStart.Unix(),
			CurrentPeriodEnd:   periodEnd.Unix(),
			Price: &stripe.Price{Recurring: &stripe.PriceRecurring{
				Interval: stripe.PriceRecurringIntervalMonth,
			}},
		}}},
	}
	fake := &idleunsubtest.FakeStripe{Subs: map[string]*stripe.Subscription{subID: sub}}

	var signerKey [32]byte
	_, err = rand.Read(signerKey[:])
	require.NoError(t, err)
	signer := idleunsub.NewTokenSigner(signerKey[:])

	svc := idleunsub.NewService(
		b.Pool(),
		fake,
		&idleunsubtest.RecordingEnqueuer{},
		signer,
		"http://localhost:3000",
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	b.ApplyTestOverrides(backend.TestOverrides{Idleunsub: svc})

	return &keepFixture{
		B:           b,
		Ctx:         ctx,
		UserID:      userID,
		CustID:      custID,
		SubID:       subID,
		PeriodStart: periodStart,
		PeriodEnd:   periodEnd,
		Sub:         sub,
		Fake:        fake,
		Signer:      signer,
	}
}

// signKeepToken builds a valid (unexpired) keep token for fx.
func (fx *keepFixture) signKeepToken() string {
	return fx.Signer.Sign(idleunsub.KeepTokenClaims{
		UserID:           fx.UserID,
		SubscriptionID:   fx.SubID,
		Action:           "keep_subscription",
		CurrentPeriodEnd: fx.PeriodEnd,
		IssuedAt:         fx.PeriodStart.Unix(),
		ExpiresAt:        fx.PeriodEnd.Unix(),
	})
}

func newKeepRequest(token string) *http.Request {
	return httptest.NewRequest(http.MethodGet, "/sub/keep?t="+token, nil)
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestKeep_HappyPath(t *testing.T) {
	t.Parallel()
	fx := setupKeepFixture(t)

	tok := fx.signKeepToken()
	w := httptest.NewRecorder()
	handler.GetKeepLink(fx.B)(w, newKeepRequest(tok))

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "text/html")
	assert.Contains(t, w.Body.String(), "You're all set")
	assert.Contains(t, w.Body.String(), fx.PeriodEnd.UTC().Format("January 2, 2006"))

	// Exactly one Stripe Update call: cancel_at_period_end=false.
	require.Len(t, fx.Fake.UpdateCalls, 1, "exactly one Stripe call on first claim")
	assert.Equal(t, fx.SubID, fx.Fake.UpdateCalls[0].ID)
	assert.False(t, fx.Fake.UpdateCalls[0].CancelAtPeriodEnd)

	// One subscription_kept event row was written.
	var count int
	err := fx.B.Pool().QueryRow(fx.Ctx, `
		SELECT COUNT(*) FROM user_events
		WHERE user_id = $1 AND event_type = 'subscription_kept'`,
		fx.UserID).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "exactly one subscription_kept row")

	// Cache flipped + banner set.
	var cancelAtEnd, isAuto, banner bool
	err = fx.B.Pool().QueryRow(fx.Ctx, `
		SELECT sub_cancel_at_period_end, sub_cancel_is_auto, pending_kept_banner
		FROM users WHERE id = $1`, fx.UserID).Scan(&cancelAtEnd, &isAuto, &banner)
	require.NoError(t, err)
	assert.False(t, cancelAtEnd)
	assert.False(t, isAuto)
	assert.True(t, banner, "real reversal must set kept-banner")
}

func TestKeep_TamperedToken(t *testing.T) {
	t.Parallel()
	fx := setupKeepFixture(t)

	tok := fx.signKeepToken()
	// Flip a single character in the signature half (after the dot).
	// hmac.Equal sees a mismatch → ErrTokenInvalid.
	tampered := tok[:len(tok)-1]
	if tok[len(tok)-1] == 'A' {
		tampered += "B"
	} else {
		tampered += "A"
	}

	w := httptest.NewRecorder()
	handler.GetKeepLink(fx.B)(w, newKeepRequest(tampered))

	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid")
	assert.Empty(t, fx.Fake.UpdateCalls, "tampered token must not reach Stripe")
}

func TestKeep_ExpiredToken(t *testing.T) {
	t.Parallel()
	fx := setupKeepFixture(t)

	// Build a token whose period has already passed (and exp matches per the
	// spec invariant). Verify will return ErrTokenExpired.
	pastEnd := time.Now().Add(-1 * time.Hour).UTC().Truncate(time.Second)
	tok := fx.Signer.Sign(idleunsub.KeepTokenClaims{
		UserID:           fx.UserID,
		SubscriptionID:   fx.SubID,
		Action:           "keep_subscription",
		CurrentPeriodEnd: pastEnd,
		IssuedAt:         pastEnd.Add(-30 * 24 * time.Hour).Unix(),
		ExpiresAt:        pastEnd.Unix(),
	})

	w := httptest.NewRecorder()
	handler.GetKeepLink(fx.B)(w, newKeepRequest(tok))

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "subscription has ended")
	assert.Contains(t, w.Body.String(), pastEnd.UTC().Format("January 2, 2006"))
	assert.Empty(t, fx.Fake.UpdateCalls, "expired token must not reach Stripe")
	require.Contains(t, w.Header().Get("Content-Type"), "text/html")
}

func TestKeep_Replay(t *testing.T) {
	t.Parallel()
	fx := setupKeepFixture(t)

	tok := fx.signKeepToken()

	// First click — real reversal.
	w1 := httptest.NewRecorder()
	handler.GetKeepLink(fx.B)(w1, newKeepRequest(tok))
	require.Equal(t, http.StatusOK, w1.Code)
	require.Contains(t, w1.Body.String(), "You're all set")
	require.Len(t, fx.Fake.UpdateCalls, 1)

	// Second click with the same token — TryClaimKeepToken returns no rows
	// (already claimed). The handler must render the same confirmation page
	// with NO additional Stripe call.
	w2 := httptest.NewRecorder()
	handler.GetKeepLink(fx.B)(w2, newKeepRequest(tok))
	require.Equal(t, http.StatusOK, w2.Code)
	assert.Contains(t, w2.Body.String(), "You're all set")
	assert.Len(t, fx.Fake.UpdateCalls, 1, "replay must not trigger a second Stripe call")

	// Still exactly one subscription_kept row.
	var count int
	err := fx.B.Pool().QueryRow(fx.Ctx, `
		SELECT COUNT(*) FROM user_events
		WHERE user_id = $1 AND event_type = 'subscription_kept'`,
		fx.UserID).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "replay must not insert a second kept event")
}

func TestKeep_RefusesManualCancel(t *testing.T) {
	t.Parallel()
	fx := setupKeepFixture(t)

	// Flip sub_cancel_is_auto OFF: this is now a manual portal cancel that
	// happens to share the cache flag layout. KeepSubscription's gate read
	// will refuse to touch Stripe; the handler still renders the kept page.
	_, err := fx.B.Pool().Exec(fx.Ctx,
		`UPDATE users SET sub_cancel_is_auto = FALSE WHERE id = $1`, fx.UserID)
	require.NoError(t, err)

	tok := fx.signKeepToken()
	w := httptest.NewRecorder()
	handler.GetKeepLink(fx.B)(w, newKeepRequest(tok))

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "You're all set",
		"manual-cancel refusal still renders the kept page idempotently")

	// Zero Stripe calls — KeepSubscription's gate refused.
	assert.Empty(t, fx.Fake.UpdateCalls, "manual cancel must not reach Stripe")

	// Zero new event rows.
	var count int
	err = fx.B.Pool().QueryRow(fx.Ctx, `
		SELECT COUNT(*) FROM user_events
		WHERE user_id = $1 AND event_type = 'subscription_kept'`,
		fx.UserID).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 0, count, "manual cancel must not insert a kept event")
}

// TestKeep_NoSigner_RendersInvalid verifies the degraded-mode path: when the
// backend has no signer configured (nil TokenSigner), HandleKeepLink returns
// KeepLinkInvalid and the handler renders a 400 "invalid" page without
// touching Stripe or the database.
func TestKeep_NoSigner_RendersInvalid(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)

	// Construct a Service with nil signer to simulate a missing
	// KEEP_TOKEN_HMAC_KEY at startup (degraded mode).
	testSvc := idleunsub.NewService(
		b.Pool(),
		idleunsubtest.NewFakeStripe(),
		&idleunsubtest.RecordingEnqueuer{},
		nil, // no signer
		"http://localhost:3000",
		slog.Default(),
	)
	b.ApplyTestOverrides(backend.TestOverrides{Idleunsub: testSvc})

	w := httptest.NewRecorder()
	handler.GetKeepLink(b)(w, newKeepRequest("anything"))

	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid")
}
