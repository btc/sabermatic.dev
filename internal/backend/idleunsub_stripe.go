package backend

import (
	"context"

	stripe "github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/subscription"

	"github.com/btc/drill/internal/feat/idleunsub"
)

// TODO: stripe-go v82's subscription package functions are not context-aware
// (Get/Update don't accept a ctx). Tracing and cancellation cannot propagate
// into the Stripe HTTP call. Revisit when the library exposes a contextual API.

// realStripeClient adapts stripe-go's package-level subscription API to
// idleunsub.StripeClient. Used in production wiring; tests inject a fake.
type realStripeClient struct{}

// GetSubscription fetches a subscription by ID via Stripe's REST API.
// The stripe-go package consults the package-level stripe.Key for auth,
// configured at startup by config.Stripe.Init().
func (realStripeClient) GetSubscription(_ context.Context, id string) (*stripe.Subscription, error) {
	return subscription.Get(id, nil)
}

// UpdateSubscriptionCancel toggles cancel_at_period_end on the subscription.
// idempotencyKey is sent in the Idempotency-Key header; pass "" to omit.
func (realStripeClient) UpdateSubscriptionCancel(_ context.Context, id string, cancelAtPeriodEnd bool, idempotencyKey string) (*stripe.Subscription, error) {
	params := &stripe.SubscriptionParams{
		CancelAtPeriodEnd: stripe.Bool(cancelAtPeriodEnd),
	}
	if idempotencyKey != "" {
		params.SetIdempotencyKey(idempotencyKey)
	}
	return subscription.Update(id, params)
}

var _ idleunsub.StripeClient = realStripeClient{}
