package billing

import "time"

// EducatorAccessLevel controls what educator content a user sees.
type EducatorAccessLevel int

const (
	Preview   EducatorAccessLevel = iota // Teaser summary only
	Full                                 // Complete deep-dive analysis
	FreeTaste                            // One-time full analysis for free users
)

// Plan defines the limits and features for a billing tier.
type Plan struct {
	Name               string
	GrantMinutes       int // Minutes granted per billing event (one-time for free, per-cycle for subscriptions)
	MaxDurationMinutes int
	ConcurrentSessions int
	CoachAccess        bool
	EducatorAccess     EducatorAccessLevel
	FreeEducatorLimit  int
}

var plans = map[string]Plan{
	"free": {
		Name:               "Free",
		GrantMinutes:       60,
		MaxDurationMinutes: 30,
		ConcurrentSessions: 1,
		CoachAccess:        false,
		EducatorAccess:     Preview,
		FreeEducatorLimit:  1,
	},
	"pro": {
		Name:               "Pro",
		GrantMinutes:       600,
		MaxDurationMinutes: 180,
		ConcurrentSessions: 3,
		CoachAccess:        true,
		EducatorAccess:     Full,
		FreeEducatorLimit:  0,
	},
}

// FreeTrialMinutes returns the one-time free trial minute allocation for new accounts.
func FreeTrialMinutes() int {
	return plans["free"].GrantMinutes
}

func PlanByName(name string) (Plan, bool) {
	p, ok := plans[name]
	return p, ok
}

// MinutePack defines an available minute-pack purchase size.
type MinutePack struct {
	Minutes int
}

// ValidPackSize reports whether the given minute count is a purchasable pack.
func ValidPackSize(minutes int) bool {
	switch minutes {
	case 120, 300, 600:
		return true
	default:
		return false
	}
}

// AllPackSizes returns the available minute-pack sizes in ascending order.
func AllPackSizes() []int {
	return []int{120, 300, 600}
}

// FreeGrantExpiry returns the expiry timestamp for a one-time free trial grant.
// Set far in the future since free trial grants do not expire monthly.
func FreeGrantExpiry() time.Time {
	return time.Now().UTC().AddDate(10, 0, 0)
}
