package idleunsub_test

import (
	"context"
	"errors"

	stripe "github.com/stripe/stripe-go/v82"

	"github.com/btc/drill/internal/email"
	"github.com/btc/drill/internal/feat/idleunsub"
)

// errStripeNotFound is the sentinel returned by the fake when an unknown
// subscription ID is requested. The tests never assert on its identity;
// it just needs to be non-nil so callers see "fetch failed" semantics.
var errStripeNotFound = errors.New("fakeStripe: subscription not found")

// fakeStripe is an in-memory StripeClient for tests.
type fakeStripe struct {
	subs        map[string]*stripe.Subscription
	updateCalls []updateCall
	updateErr   error
}

type updateCall struct {
	ID                string
	CancelAtPeriodEnd bool
	IdempotencyKey    string
}

func (f *fakeStripe) GetSubscription(_ context.Context, id string) (*stripe.Subscription, error) {
	if s, ok := f.subs[id]; ok {
		return s, nil
	}
	return nil, errStripeNotFound
}

func (f *fakeStripe) UpdateSubscriptionCancel(_ context.Context, id string, cancelAtEnd bool, key string) (*stripe.Subscription, error) {
	f.updateCalls = append(f.updateCalls, updateCall{id, cancelAtEnd, key})
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	if s, ok := f.subs[id]; ok {
		s.CancelAtPeriodEnd = cancelAtEnd
		return s, nil
	}
	return nil, errStripeNotFound
}

var _ idleunsub.StripeClient = (*fakeStripe)(nil)

// nullMailer is an email.Sender that drops all sends. Cancel emails are
// fire-and-forget in HandleInvoiceUpcoming; we only need them not to error.
type nullMailer struct{}

func (nullMailer) Send(_ context.Context, _ email.Message) error { return nil }

var _ email.Sender = nullMailer{}
