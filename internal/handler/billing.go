package handler

import (
	"io"
	"log/slog"
	"net/http"

	"github.com/stripe/stripe-go/v82/webhook"

	"github.com/btc/drill/internal/backend"
)

// (PostCheckout and PostPortal migrated to ConnectRPC BillingService)

// PostStripeWebhook returns a handler that processes incoming Stripe webhook
// events. The request body is verified against the Stripe-Signature header
// using the configured webhook secret. Always returns 200 to avoid Stripe
// retries on application errors that would recur.
func PostStripeWebhook(b *backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const maxBodyBytes = 65536
		body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
		if err != nil {
			slog.Error("read webhook body", "error", err)
			w.WriteHeader(http.StatusOK)
			return
		}

		sigHeader := r.Header.Get("Stripe-Signature")
		webhookSecret := b.Config().Stripe.WebhookSecret
		if webhookSecret == "" {
			slog.Error("stripe webhook secret not configured")
			w.WriteHeader(http.StatusOK)
			return
		}

		event, err := webhook.ConstructEventWithOptions(body, sigHeader, webhookSecret, webhook.ConstructEventOptions{
			IgnoreAPIVersionMismatch: true,
		})
		if err != nil {
			slog.Warn("stripe webhook signature verification failed", "error", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		if err := b.HandleStripeWebhook(r.Context(), &event); err != nil {
			// Return 500 so Stripe retries on transient errors (DB down, etc.).
			// HandleStripeWebhook returns nil for permanent non-errors (unknown
			// customer, duplicate event) so those get 200.
			slog.Error("handle stripe webhook", "error", err, "event_type", event.Type, "event_id", event.ID)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
	}
}
