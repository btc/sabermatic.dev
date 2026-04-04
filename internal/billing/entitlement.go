package billing

// Entitlements encapsulates all billing policy decisions for a user.
// Created by Resolve from a BillingSnapshot. Methods encode policy —
// callers never inspect raw balances or thresholds.
type Entitlements struct {
	plan               Plan
	totalBalance       int
	paidBalance        int
	freeEducatorUsed   int
}

// BillingSnapshot holds the billing state fetched by GetBillingSnapshot.
// Defined here (not in db package) so Resolve can accept it directly.
type BillingSnapshot struct {
	Plan                  string
	FreeFullEducatorsUsed int
	TotalBalance          int
	PaidBalance           int
}

// Resolve computes entitlements from a billing snapshot.
// The snapshot should come from GetBillingSnapshot (a single SQL query
// that joins users + grants), fetched inside the caller's transaction.
func Resolve(bs BillingSnapshot) Entitlements {
	p, ok := PlanByName(bs.Plan)
	if !ok {
		p, _ = PlanByName("free")
	}
	return Entitlements{
		plan:             p,
		totalBalance:     bs.TotalBalance,
		paidBalance:      bs.PaidBalance,
		freeEducatorUsed: bs.FreeFullEducatorsUsed,
	}
}

// CanStartSession reports whether the user can start a session of the given duration.
func (e Entitlements) CanStartSession(durationMinutes int) bool {
	return durationMinutes <= e.plan.MaxDurationMinutes && e.totalBalance >= durationMinutes
}

// DurationAllowed reports whether the requested duration is within the plan limit.
func (e Entitlements) DurationAllowed(durationMinutes int) bool {
	return durationMinutes <= e.plan.MaxDurationMinutes
}

// BalanceSufficient reports whether the user has enough minutes for the given duration.
func (e Entitlements) BalanceSufficient(durationMinutes int) bool {
	return e.totalBalance >= durationMinutes
}

// ConcurrentSessionsAllowed reports whether the user can have another active session.
func (e Entitlements) ConcurrentSessionsAllowed(activeCount int) bool {
	return activeCount < e.plan.ConcurrentSessions
}

// CanAccessCoach reports whether the user can use coach analysis.
func (e Entitlements) CanAccessCoach() bool {
	return e.paidBalance > 0
}

// EducatorAccessLevel returns what level of educator content the user sees.
func (e Entitlements) EducatorAccessLevel() EducatorAccessLevel {
	if e.paidBalance > 0 {
		return Full
	}
	if e.freeEducatorUsed < e.plan.FreeEducatorLimit {
		return FreeTaste
	}
	return Preview
}

// MaxDurationMinutes returns the plan's maximum session duration.
func (e Entitlements) MaxDurationMinutes() int {
	return e.plan.MaxDurationMinutes
}

// TotalBalance returns the user's total available minutes (for error messages).
func (e Entitlements) TotalBalance() int {
	return e.totalBalance
}

// FreeEducatorLimit returns the plan's free educator analysis limit (for the atomic increment guard).
func (e Entitlements) FreeEducatorLimit() int {
	return e.plan.FreeEducatorLimit
}
