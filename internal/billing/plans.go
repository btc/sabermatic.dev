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
	StripePriceID      string
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
		StripePriceID:      "",
	},
}

func PlanByName(name string) (Plan, bool) {
	p, ok := plans[name]
	return p, ok
}

func SetProPriceID(priceID string) {
	p := plans["pro"]
	p.StripePriceID = priceID
	plans["pro"] = p
}

type MinutePack struct {
	Minutes       int
	StripePriceID string
}

var packs = map[int]MinutePack{
	120: {Minutes: 120},
	300: {Minutes: 300},
	600: {Minutes: 600},
}

func PackByMinutes(minutes int) (MinutePack, bool) {
	p, ok := packs[minutes]
	return p, ok
}

func SetPackPriceIDs(pack120, pack300, pack600 string) {
	set := func(mins int, priceID string) {
		p := packs[mins]
		p.StripePriceID = priceID
		packs[mins] = p
	}
	set(120, pack120)
	set(300, pack300)
	set(600, pack600)
}

func AllPacks() []MinutePack {
	return []MinutePack{packs[120], packs[300], packs[600]}
}

func EndOfMonth(t time.Time) time.Time {
	y, m, _ := t.Date()
	return time.Date(y, m+1, 1, 0, 0, 0, 0, time.UTC)
}
