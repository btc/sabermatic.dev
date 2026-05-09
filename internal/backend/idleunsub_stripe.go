package backend

import (
	"context"

	stripe "github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/subscription"
)

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
