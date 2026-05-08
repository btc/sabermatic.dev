package idleunsub

import "time"

// All timestamps are canonicalized to second precision via
// time.Unix(stripeInt64, 0).UTC() before assignment. Stripe period fields
// arrive as int64 Unix seconds; explicit second precision and UTC guard
// against future drift if a code path ever constructs a time.Time from a
// different source. JSON encoding via encoding/json produces RFC3339
// ("...Z") which Postgres ::timestamptz parses reliably.

type cancelMetadata struct {
	SubscriptionID     string    `json:"subscription_id"`
	StripeEventID      string    `json:"stripe_event_id"`
	CurrentPeriodStart time.Time `json:"current_period_start"`
	CurrentPeriodEnd   time.Time `json:"current_period_end"`
}

type keptMetadata struct {
	SubscriptionID     string    `json:"subscription_id"`
	Via                string    `json:"via"`                  // "link" | "auto_activity"
	CurrentPeriodStart time.Time `json:"current_period_start"` // identifies the period kept
}
