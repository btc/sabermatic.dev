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
	MinutesPerMonth    int
	MaxDurationMinutes int
	ConcurrentSessions int
	CoachAccess        bool
	EducatorAccess     EducatorAccessLevel
	FreeEducatorLimit  int
}

var plans = map[string]Plan{
	"free": {
		Name:               "Free",
		MinutesPerMonth:    60,
		MaxDurationMinutes: 30,
		ConcurrentSessions: 1,
		CoachAccess:        false,
		EducatorAccess:     Preview,
		FreeEducatorLimit:  1,
	},
	"pro": {
		Name:               "Pro",
		MinutesPerMonth:    600,
		MaxDurationMinutes: 180,
		ConcurrentSessions: 3,
		CoachAccess:        true,
		EducatorAccess:     Full,
		FreeEducatorLimit:  0,
	},
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

func EndOfMonth(t time.Time) time.Time {
	y, m, _ := t.Date()
	return time.Date(y, m+1, 1, 0, 0, 0, 0, time.UTC)
}
