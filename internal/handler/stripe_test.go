package handler_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stripe/stripe-go/v82/webhook"

	"github.com/btc/drill/internal/handler"
)

const testWebhookSecret = "whsec_test_secret_for_unit_tests_only_must_be_at_least_some_length"

func TestStripeWebhook_ValidSignature(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	cfg := b.Config()
	cfg.Stripe.WebhookSecret = testWebhookSecret
	b.SetConfig(cfg)

	// A minimal Stripe event payload. The handler routes by event type;
	// "ping" falls through HandleStripeWebhook's default branch (see
	// internal/backend/billing.go), which logs "unhandled stripe event" and
	// returns nil. Any not-handled event type would work; "ping" is chosen
	// because it's clearly synthetic.
	payload := []byte(`{"id":"evt_test_1","type":"ping","data":{"object":{}}}`)
	signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{
		Payload:   payload,
		Secret:    testWebhookSecret,
		Timestamp: time.Now(),
	})

	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/stripe", bytes.NewReader(signed.Payload))
	req.Header.Set("Stripe-Signature", signed.Header)

	w := httptest.NewRecorder()
	handler.PostStripeWebhook(b)(w, req)

	require.Equal(t, http.StatusOK, w.Code)
}

func TestStripeWebhook_InvalidSignature(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	cfg := b.Config()
	cfg.Stripe.WebhookSecret = testWebhookSecret
	b.SetConfig(cfg)

	payload := []byte(`{"id":"evt_test_2","type":"ping","data":{"object":{}}}`)

	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/stripe", bytes.NewReader(payload))
	req.Header.Set("Stripe-Signature", "t=0,v1=invalid_signature")

	w := httptest.NewRecorder()
	handler.PostStripeWebhook(b)(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestStripeWebhook_MissingSecret(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	cfg := b.Config()
	cfg.Stripe.WebhookSecret = "" // explicitly unset
	b.SetConfig(cfg)

	payload := []byte(`{"id":"evt_test_3","type":"ping","data":{"object":{}}}`)

	// No Stripe-Signature header — the handler short-circuits on empty secret
	// before reading the header, so its presence is irrelevant.
	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/stripe", bytes.NewReader(payload))

	w := httptest.NewRecorder()
	handler.PostStripeWebhook(b)(w, req)

	// Deliberate: returns 200 to prevent Stripe from retrying when the secret
	// is misconfigured. The misconfiguration is logged for ops to detect.
	require.Equal(t, http.StatusOK, w.Code)
}
