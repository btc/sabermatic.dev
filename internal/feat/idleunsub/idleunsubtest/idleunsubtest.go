// Package idleunsubtest provides shared test helpers for idleunsub feature
// tests across packages. Mirrors the httptest convention.
package idleunsubtest

import (
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	stripe "github.com/stripe/stripe-go/v82"

	"github.com/btc/drill/internal/email"
	"github.com/btc/drill/internal/feat/idleunsub"
	"github.com/btc/drill/internal/jobs"
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
// Safe for concurrent use: tests that fire background goroutines (e.g.,
// AutoReverse) can call Send and read Snapshot/Len without racing.
//
// Note: production now enqueues emails via River (see RecordingEnqueuer).
// RecordingMailer remains useful for direct email.Sender wiring (e.g., the
// SendEmailWorker tests), but new idleunsub tests should assert on
// RecordingEnqueuer.Snapshot().
type RecordingMailer struct {
	mu   sync.Mutex
	Msgs []email.Message // legacy direct access; prefer Snapshot for tests
}

// Send implements email.Sender.
func (r *RecordingMailer) Send(_ context.Context, m email.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Msgs = append(r.Msgs, m)
	return nil
}

// Snapshot returns a copy of the captured messages, safe to inspect from any
// goroutine. Use this instead of indexing Msgs directly when there is any
// possibility of a concurrent Send.
func (r *RecordingMailer) Snapshot() []email.Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]email.Message, len(r.Msgs))
	copy(out, r.Msgs)
	return out
}

// Len returns the number of captured messages, safe under concurrent Send.
func (r *RecordingMailer) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.Msgs)
}

// RecordingEnqueuer is an idleunsub.EmailEnqueuer that captures every queued
// SendEmailJob argument. Safe for concurrent use: idleunsub tests that
// trigger AutoReverse from a background goroutine can call EnqueueSendEmail
// and read Snapshot/Len without racing.
//
// Production wires the EmailEnqueuer to a River client wrapper (see
// internal/backend/idleunsub_enqueuer.go); tests use this recorder so they
// can assert on enqueued args without standing up the River worker.
type RecordingEnqueuer struct {
	mu   sync.Mutex
	args []jobs.SendEmailArgs
	Err  error // optional: return this from EnqueueSendEmail
}

// EnqueueSendEmail captures args. Returns r.Err if set (for failure tests).
func (r *RecordingEnqueuer) EnqueueSendEmail(_ context.Context, a jobs.SendEmailArgs) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return r.Err
	}
	r.args = append(r.args, a)
	return nil
}

// Snapshot returns a copy of every queued SendEmailArgs in insertion order.
func (r *RecordingEnqueuer) Snapshot() []jobs.SendEmailArgs {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]jobs.SendEmailArgs, len(r.args))
	copy(out, r.args)
	return out
}

// Len returns the number of queued args, safe under concurrent enqueue.
func (r *RecordingEnqueuer) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.args)
}

// NewServiceWithFake constructs an idleunsub.Service wired to the given fake
// Stripe client and a no-op enqueuer. The token signer uses HMAC-SHA256 with
// a 32-byte random key (sufficient for any keep-link round-trip in tests).
// Returns the Service for use with backend.TestOverrides{Idleunsub: svc}.
//
// Note: Does NOT call ApplyTestOverrides itself — the caller does that, since
// idleunsubtest cannot import the backend package without a circular import.
//
// For tests that need to assert on enqueued emails, construct a
// RecordingEnqueuer and pass it to idleunsub.NewService directly instead.
func NewServiceWithFake(t *testing.T, pool *pgxpool.Pool, fake *FakeStripe) *idleunsub.Service {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("idleunsubtest: read random key: %v", err)
	}
	signer := idleunsub.NewTokenSigner(key)
	return idleunsub.NewService(pool, fake, &RecordingEnqueuer{}, signer, "http://localhost:3000", slog.Default())
}

var (
	_ idleunsub.StripeClient  = (*FakeStripe)(nil)
	_ email.Sender            = NullMailer{}
	_ email.Sender            = (*RecordingMailer)(nil)
	_ idleunsub.EmailEnqueuer = (*RecordingEnqueuer)(nil)
)
