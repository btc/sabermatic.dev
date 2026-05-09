package idleunsub_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	stripe "github.com/stripe/stripe-go/v82"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/backend"
	"github.com/btc/drill/internal/backendtest"
	"github.com/btc/drill/internal/feat/idleunsub"
	"github.com/btc/drill/internal/feat/idleunsub/idleunsubtest"
	"github.com/btc/drill/internal/handler"
)

const testBaseURL = "http://test.local"

// TestE2E_CancelAndKeepViaLink exercises the full cancel + keep-link round
// trip: a Stripe invoice.upcoming webhook is dispatched through the production
// Backend.HandleStripeWebhook router, the cancel email's keep-link is parsed
// and clicked against a real httptest server mounting handler.GetKeepLink, the
// reversal lands in fake Stripe and DB cache, and a replay of the link is
// idempotent.
func TestE2E_CancelAndKeepViaLink(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()
	userID := backendtest.SeedUser(t, b)
	subID := "sub_e2e"
	custID := "cus_e2e"

	// Set up the user as a Pro subscriber with stale activity (idle > 1 period).
	_, err := b.Pool().Exec(ctx, `
		UPDATE users
		SET stripe_customer_id     = $1,
		    stripe_subscription_id = $2,
		    plan                   = 'pro',
		    idle_eligible_after    = NOW() - INTERVAL '6 months'
		WHERE id = $3`, custID, subID, userID)
	require.NoError(t, err)
	_, err = b.Pool().Exec(ctx, `
		INSERT INTO auth_sessions (user_id, token_hash, expires_at, last_active)
		VALUES ($1, 'e2e-fake-hash', NOW() + INTERVAL '24 hours', NOW() - INTERVAL '3 months')`, userID)
	require.NoError(t, err)

	// Fake Stripe with an active monthly subscription mid-cycle.
	now := time.Now().UTC().Truncate(time.Second)
	periodStart := now.AddDate(0, 0, -23)
	periodEnd := periodStart.AddDate(0, 1, 0)
	fake := idleunsubtest.NewFakeStripe()
	fake.Subs[subID] = &stripe.Subscription{
		ID:                subID,
		Status:            stripe.SubscriptionStatusActive,
		CancelAtPeriodEnd: false,
		Customer:          &stripe.Customer{ID: custID},
		Items: &stripe.SubscriptionItemList{Data: []*stripe.SubscriptionItem{{
			CurrentPeriodStart: periodStart.Unix(),
			CurrentPeriodEnd:   periodEnd.Unix(),
			Price: &stripe.Price{Recurring: &stripe.PriceRecurring{
				Interval:      stripe.PriceRecurringIntervalMonth,
				IntervalCount: 1,
			}},
		}}},
	}

	enqueuer := &idleunsubtest.RecordingEnqueuer{}
	signer := idleunsub.NewTokenSigner([]byte("test-key-32-bytes-padding-aaaaaa"))
	svc := idleunsub.NewService(b.Pool(), fake, enqueuer, signer, testBaseURL, slog.Default())
	b.ApplyTestOverrides(backend.TestOverrides{Idleunsub: svc})

	// httptest server hosting just the keep-link handler. Email body links
	// point at testBaseURL; we rewrite to server.URL when issuing the GET.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /sub/keep", handler.GetKeepLink(b))
	server := httptest.NewServer(mux)
	defer server.Close()

	// 1. Fire invoice.upcoming via the production webhook dispatch path.
	upcomingEvent := makeInvoiceUpcomingEvent("evt_e2e_1", subID, custID)
	require.NoError(t, b.HandleStripeWebhook(ctx, upcomingEvent))

	// 2. Verify Stripe was told to cancel and a cancel email was enqueued.
	require.True(t, fake.Subs[subID].CancelAtPeriodEnd, "Stripe should be canceled at period end")
	msgs := enqueuer.Snapshot()
	require.Len(t, msgs, 1, "expected cancel email enqueued")
	require.Contains(t, msgs[0].Subject, "next period")
	require.Contains(t, msgs[0].Text, testBaseURL+"/sub/keep?t=")

	// 3. Click the keep link against the test server.
	keepURL := extractKeepURL(t, msgs[0].Text, testBaseURL)
	testTargetURL := strings.Replace(keepURL, testBaseURL, server.URL, 1)
	resp, err := http.Get(testTargetURL)
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "body=%s", body)

	// 4. Verify reversal happened in Stripe + a kept email was enqueued.
	require.False(t, fake.Subs[subID].CancelAtPeriodEnd, "Stripe should be reverted")
	msgs = enqueuer.Snapshot()
	require.Len(t, msgs, 2, "expected kept email enqueued after keep-link click")
	require.Contains(t, msgs[1].Subject, "still active")

	// 5. Verify cache state in DB.
	var subCancelAtPeriodEnd, subCancelIsAuto, banner bool
	err = b.Pool().QueryRow(ctx, `
		SELECT sub_cancel_at_period_end, sub_cancel_is_auto, pending_kept_banner
		FROM users WHERE id = $1`, userID).Scan(&subCancelAtPeriodEnd, &subCancelIsAuto, &banner)
	require.NoError(t, err)
	require.False(t, subCancelAtPeriodEnd)
	require.False(t, subCancelIsAuto)
	require.True(t, banner, "pending_kept_banner should be true after a real reversal")

	// 6. Replay the keep link — should be idempotent (same status, no new email).
	resp2, err := http.Get(testTargetURL)
	require.NoError(t, err)
	resp2.Body.Close()
	require.Equal(t, http.StatusOK, resp2.StatusCode)
	require.Equal(t, 2, enqueuer.Len(), "no additional email enqueued on replay")
}

// TestE2E_AutoReverseOnLogin exercises the activity-driven reversal path:
// a user is in the auto-cancel state, logs in, calls AuthenticateSession,
// and the background goroutine reverses the cancel in Stripe + DB.
func TestE2E_AutoReverseOnLogin(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	signupResult, err := b.Signup(ctx, backend.SignupParams{
		Email:       "e2e-auto@example.com",
		Password:    "strongpass1",
		DisplayName: "E2E AutoReverse",
	})
	require.NoError(t, err)
	userID := signupResult.UserID
	subID := "sub_e2e_auto"
	custID := "cus_e2e_auto"

	// Place the user in the auto-canceled state (cache reflects pending cancel).
	_, err = b.Pool().Exec(ctx, `
		UPDATE users
		SET stripe_customer_id       = $1,
		    stripe_subscription_id   = $2,
		    plan                     = 'pro',
		    sub_cancel_at_period_end = TRUE,
		    sub_cancel_is_auto       = TRUE,
		    sub_current_period_start = NOW()
		WHERE id = $3`, custID, subID, userID)
	require.NoError(t, err)

	// Fake Stripe agrees: subscription is canceled at period end.
	now := time.Now().UTC().Truncate(time.Second)
	periodStart := now.AddDate(0, 0, -10)
	periodEnd := periodStart.AddDate(0, 1, 0)
	fake := idleunsubtest.NewFakeStripe()
	fake.Subs[subID] = &stripe.Subscription{
		ID:                subID,
		Status:            stripe.SubscriptionStatusActive,
		CancelAtPeriodEnd: true,
		Items: &stripe.SubscriptionItemList{Data: []*stripe.SubscriptionItem{{
			CurrentPeriodStart: periodStart.Unix(),
			CurrentPeriodEnd:   periodEnd.Unix(),
		}}},
	}

	enqueuer := &idleunsubtest.RecordingEnqueuer{}
	signer := idleunsub.NewTokenSigner([]byte("test-key-32-bytes-padding-aaaaaa"))
	svc := idleunsub.NewService(b.Pool(), fake, enqueuer, signer, testBaseURL, slog.Default())
	b.ApplyTestOverrides(backend.TestOverrides{Idleunsub: svc})

	// Login through the real path to get a real session token.
	loginResult, err := b.Login(ctx, backend.LoginParams{
		Email:    "e2e-auto@example.com",
		Password: "strongpass1",
		IP:       "127.0.0.1:1234",
	})
	require.NoError(t, err)

	// AuthenticateSession kicks the AutoReverse goroutine.
	_, err = b.AuthenticateSession(ctx, auth.HashSessionToken(loginResult.Token))
	require.NoError(t, err)

	// Wait for the background goroutine to land its writes AND enqueue the email.
	// AutoReverse clears the cache and commits BEFORE enqueuing the email, so
	// polling on the cache alone races against the enqueue call.
	require.Eventually(t, func() bool {
		var subCancelAtPeriodEnd bool
		err := b.Pool().QueryRow(ctx,
			`SELECT sub_cancel_at_period_end FROM users WHERE id = $1`,
			userID).Scan(&subCancelAtPeriodEnd)
		return err == nil && !subCancelAtPeriodEnd && enqueuer.Len() >= 1
	}, 5*time.Second, 50*time.Millisecond, "AutoReverse goroutine did not finish")

	// Stripe was reverted (asserted via DB cache, not fake.Subs — the FakeStripe
	// map is mutated by the AutoReverse goroutine and is documented as not
	// safe for concurrent reads).
	msgs := enqueuer.Snapshot()
	require.Len(t, msgs, 1, "expected kept email enqueued")
	require.Contains(t, msgs[0].Subject, "still active")

	// pending_kept_banner is set (activity-driven real reversal).
	var banner bool
	err = b.Pool().QueryRow(ctx,
		`SELECT pending_kept_banner FROM users WHERE id = $1`,
		userID).Scan(&banner)
	require.NoError(t, err)
	require.True(t, banner)

	// subscription_kept event with via=auto_activity.
	var via string
	err = b.Pool().QueryRow(ctx, `
		SELECT metadata->>'via' FROM user_events
		WHERE user_id = $1 AND event_type = 'subscription_kept'`,
		userID).Scan(&via)
	require.NoError(t, err)
	require.Equal(t, "auto_activity", via)
}

// makeInvoiceUpcomingEvent builds a Stripe event suitable for the production
// webhook dispatch. event.GetObjectValue("subscription") reads from Object;
// the production handler extracts the subID from there.
func makeInvoiceUpcomingEvent(eventID, subID, custID string) stripe.Event {
	return stripe.Event{
		ID:   eventID,
		Type: "invoice.upcoming",
		Data: &stripe.EventData{
			Object: map[string]interface{}{
				"subscription": subID,
				"customer":     custID,
			},
		},
	}
}

// extractKeepURL pulls the keep-link URL from the cancel email body.
func extractKeepURL(t *testing.T, body, baseURL string) string {
	t.Helper()
	re := regexp.MustCompile(regexp.QuoteMeta(baseURL) + `/sub/keep\?t=[A-Za-z0-9._-]+`)
	m := re.FindString(body)
	require.NotEmpty(t, m, "keep URL not found in email body")
	return m
}
