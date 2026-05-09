// Package idleunsubtest provides shared test helpers for idleunsub feature
// tests across packages. Mirrors the httptest convention.
package idleunsubtest

import (
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	stripe "github.com/stripe/stripe-go/v82"

	"github.com/btc/drill/internal/email"
	"github.com/btc/drill/internal/feat/idleunsub"
)

// ErrStripeNotFound is returned when GetSubscription is called with an
// unknown subscription ID.
var ErrStripeNotFound = errors.New("idleunsubtest: subscription not found")

// FakeStripe is an in-memory idleunsub.StripeClient for tests. Pre-populate
// Subs by ID; UpdateCalls records every UpdateSubscriptionCancel invocation.
// Set UpdateErr to make UpdateSubscriptionCancel fail.
//
// Concurrency: FakeStripe is NOT safe for concurrent use. Tests that fire
// background goroutines (e.g., AutoReverse from AuthenticateSession) must
// either:
//   - Wait for the goroutine to complete before reading UpdateCalls, or
//   - Read state via the DB rather than the slice.
//
// If a future test needs to assert call counts mid-flight, add a sync.Mutex
// or use atomic counters here.
type FakeStripe struct {
	Subs        map[string]*stripe.Subscription
	UpdateCalls []UpdateCall
	UpdateErr   error
}

// UpdateCall records a single UpdateSubscriptionCancel invocation.
type UpdateCall struct {
	ID                string
	CancelAtPeriodEnd bool
	IdempotencyKey    string
}

// NewFakeStripe constructs a FakeStripe with an empty Subs map.
func NewFakeStripe() *FakeStripe {
	return &FakeStripe{Subs: make(map[string]*stripe.Subscription)}
}

// GetSubscription returns the subscription for the given ID or ErrStripeNotFound.
func (f *FakeStripe) GetSubscription(_ context.Context, id string) (*stripe.Subscription, error) {
	s, ok := f.Subs[id]
	if !ok {
		return nil, ErrStripeNotFound
	}
	return s, nil
}

// UpdateSubscriptionCancel records the call and applies the mutation to Subs.
func (f *FakeStripe) UpdateSubscriptionCancel(_ context.Context, id string, cancelAtPeriodEnd bool, idempotencyKey string) (*stripe.Subscription, error) {
	f.UpdateCalls = append(f.UpdateCalls, UpdateCall{ID: id, CancelAtPeriodEnd: cancelAtPeriodEnd, IdempotencyKey: idempotencyKey})
	if f.UpdateErr != nil {
		return nil, f.UpdateErr
	}
	s, ok := f.Subs[id]
	if !ok {
		return nil, ErrStripeNotFound
	}
	s.CancelAtPeriodEnd = cancelAtPeriodEnd
	return s, nil
}

// NullMailer is an email.Sender that drops messages.
type NullMailer struct{}

// Send implements email.Sender.
func (NullMailer) Send(_ context.Context, _ email.Message) error { return nil }

// RecordingMailer is an email.Sender that captures all sent messages.
type RecordingMailer struct {
	Msgs []email.Message
}

// Send implements email.Sender.
func (r *RecordingMailer) Send(_ context.Context, m email.Message) error {
	r.Msgs = append(r.Msgs, m)
	return nil
}

// NewServiceWithFake constructs an idleunsub.Service wired to the given fake
// Stripe client and a NullMailer. The token signer uses HMAC-SHA256 with a
// 32-byte random key (sufficient for any keep-link round-trip in tests).
// Returns the Service for use with backend.TestOverrides{Idleunsub: svc}.
//
// Note: Does NOT call ApplyTestOverrides itself — the caller does that, since
// idleunsubtest cannot import the backend package without a circular import.
func NewServiceWithFake(t *testing.T, pool *pgxpool.Pool, fake *FakeStripe) *idleunsub.Service {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("idleunsubtest: read random key: %v", err)
	}
	signer := idleunsub.NewTokenSigner(key)
	return idleunsub.NewService(pool, fake, NullMailer{}, signer, "http://localhost:3000", slog.Default())
}

var (
	_ idleunsub.StripeClient = (*FakeStripe)(nil)
	_ email.Sender           = NullMailer{}
	_ email.Sender           = (*RecordingMailer)(nil)
)
