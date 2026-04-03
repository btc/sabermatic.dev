package handler

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/stripe/stripe-go/v82/webhook"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/backend"
)

// PostCheckout returns a handler that creates a Stripe Checkout session and
// returns the checkout URL. Accepts JSON: {type, plan, minutes}.
func PostCheckout(b *backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := auth.UserFromContext(r.Context())
		var req struct {
			Type    string `json:"type"`
			Plan    string `json:"plan"`
			Minutes int    `json:"minutes"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
			return
		}

		url, err := b.CreateCheckoutSession(r.Context(), user.ID, user.Email, req.Type, req.Plan, req.Minutes)
		if err != nil {
			slog.Error("create checkout session", "error", err, "user_id", user.ID)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create checkout session"})
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"url": url})
	}
}

// PostPortal returns a handler that creates a Stripe billing portal session
// and returns the portal URL.
func PostPortal(b *backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := auth.UserFromContext(r.Context())

		url, err := b.CreatePortalSession(r.Context(), user.ID)
		if err != nil {
			if errors.Is(err, backend.ErrNoStripeAccount) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			slog.Error("create portal session", "error", err, "user_id", user.ID)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create portal session"})
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"url": url})
	}
}

// GetUsage returns a handler that returns the user's usage summary
// (balance breakdown, active grants, recent ledger entries).
func GetUsage(b *backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := auth.UserFromContext(r.Context())

		summary, err := b.GetUsageSummary(r.Context(), user.ID)
		if err != nil {
			slog.Error("get usage summary", "error", err, "user_id", user.ID)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to get usage"})
			return
		}

		writeJSON(w, http.StatusOK, summary)
	}
}

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

		if err := b.HandleStripeWebhook(r.Context(), event); err != nil {
			slog.Error("handle stripe webhook", "error", err, "event_type", event.Type, "event_id", event.ID)
		}

		// Always 200 — Stripe retries on non-2xx, and we only want retries for
		// transient errors (which we return from HandleStripeWebhook as errors
		// for logging, but still acknowledge the webhook).
		w.WriteHeader(http.StatusOK)
	}
}
