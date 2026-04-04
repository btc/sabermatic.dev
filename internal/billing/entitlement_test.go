package billing_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/billing"
)

func bs(plan string, total, paid, freeEdu int) billing.BillingSnapshot {
	return billing.BillingSnapshot{
		Plan: plan, TotalBalance: total, PaidBalance: paid, FreeFullEducatorsUsed: freeEdu,
	}
}

func TestEntitlements_EducatorAccess(t *testing.T) {
	assert.Equal(t, billing.Full, billing.Resolve(bs("pro", 600, 600, 0)).EducatorAccessLevel())
	assert.Equal(t, billing.FreeTaste, billing.Resolve(bs("free", 60, 0, 0)).EducatorAccessLevel())
	assert.Equal(t, billing.Preview, billing.Resolve(bs("free", 60, 0, 1)).EducatorAccessLevel())
}

func TestEntitlements_CanAccessCoach(t *testing.T) {
	assert.True(t, billing.Resolve(bs("pro", 600, 600, 0)).CanAccessCoach())
	assert.False(t, billing.Resolve(bs("free", 60, 0, 0)).CanAccessCoach())
}

func TestEntitlements_CanStartSession(t *testing.T) {
	ent := billing.Resolve(bs("free", 60, 0, 0))
	assert.True(t, ent.CanStartSession(30))
	assert.False(t, ent.CanStartSession(31))  // exceeds free plan max (30)
	assert.False(t, ent.CanStartSession(61))  // exceeds balance

	pro := billing.Resolve(bs("pro", 600, 600, 0))
	assert.True(t, pro.CanStartSession(180))
	assert.False(t, pro.CanStartSession(181)) // exceeds pro plan max
}

func TestEntitlements_DurationAllowed(t *testing.T) {
	assert.True(t, billing.Resolve(bs("free", 60, 0, 0)).DurationAllowed(30))
	assert.False(t, billing.Resolve(bs("free", 60, 0, 0)).DurationAllowed(31))
}

func TestEntitlements_BalanceSufficient(t *testing.T) {
	assert.True(t, billing.Resolve(bs("free", 60, 0, 0)).BalanceSufficient(60))
	assert.False(t, billing.Resolve(bs("free", 60, 0, 0)).BalanceSufficient(61))
}

func TestEntitlements_ConcurrentSessionsAllowed(t *testing.T) {
	free := billing.Resolve(bs("free", 60, 0, 0))
	assert.True(t, free.ConcurrentSessionsAllowed(0))
	assert.False(t, free.ConcurrentSessionsAllowed(1)) // free limit is 1

	pro := billing.Resolve(bs("pro", 600, 600, 0))
	assert.True(t, pro.ConcurrentSessionsAllowed(2))
	assert.False(t, pro.ConcurrentSessionsAllowed(3)) // pro limit is 3
}

func TestPlanByName(t *testing.T) {
	plan, ok := billing.PlanByName("free")
	require.True(t, ok)
	assert.Equal(t, 60, plan.MinutesPerMonth)
	assert.Equal(t, 30, plan.MaxDurationMinutes)

	plan, ok = billing.PlanByName("pro")
	require.True(t, ok)
	assert.Equal(t, 600, plan.MinutesPerMonth)

	_, ok = billing.PlanByName("nonexistent")
	assert.False(t, ok)
}

func TestValidPackSize(t *testing.T) {
	assert.True(t, billing.ValidPackSize(120))
	assert.True(t, billing.ValidPackSize(300))
	assert.True(t, billing.ValidPackSize(600))
	assert.False(t, billing.ValidPackSize(999))
}

func TestEndOfMonth(t *testing.T) {
	d := time.Date(2026, 4, 15, 10, 30, 0, 0, time.UTC)
	eom := billing.EndOfMonth(d)
	assert.Equal(t, time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), eom)

	d = time.Date(2026, 12, 25, 0, 0, 0, 0, time.UTC)
	eom = billing.EndOfMonth(d)
	assert.Equal(t, time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC), eom)
}
