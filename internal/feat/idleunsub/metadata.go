package idleunsub

import "time"

// cancelMetadata is stored as JSONB in user_events.metadata for
// subscription_auto_canceled rows. All timestamps are canonicalized to
// second precision via time.Unix(stripeInt64, 0).UTC() before assignment.
// Stripe period fields arrive as int64 Unix seconds; explicit second
// precision and UTC guard against future drift if a code path ever
// constructs a time.Time from a different source. JSON encoding via
// encoding/json produces RFC3339 ("...Z") which Postgres ::timestamptz
// parses reliably.
type cancelMetadata struct {
	SubscriptionID     string    `json:"subscription_id"`
	StripeEventID      string    `json:"stripe_event_id"`
	CurrentPeriodStart time.Time `json:"current_period_start"`
	CurrentPeriodEnd   time.Time `json:"current_period_end"`
}

//nolint:unused // wired up by KeepSubscription/AutoReverse in Task 8
type keptMetadata struct {
	SubscriptionID     string    `json:"subscription_id"`
	Via                string    `json:"via"`                  // "link" | "auto_activity"
	CurrentPeriodStart time.Time `json:"current_period_start"` // identifies the period kept
}
