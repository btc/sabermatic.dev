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
	assert.Equal(t, 60, plan.GrantMinutes)
	assert.Equal(t, 30, plan.MaxDurationMinutes)

	plan, ok = billing.PlanByName("pro")
	require.True(t, ok)
	assert.Equal(t, 600, plan.GrantMinutes)

	_, ok = billing.PlanByName("nonexistent")
	assert.False(t, ok)
}

func TestFreeTrialMinutes(t *testing.T) {
	assert.Equal(t, 60, billing.FreeTrialMinutes())
}

func TestFreeGrantExpiry(t *testing.T) {
	expiry := billing.FreeGrantExpiry()
	// Free trial grants expire ~10 years in the future, not at end of month.
	assert.True(t, expiry.After(time.Now().AddDate(9, 0, 0)),
		"free grant expiry should be far in the future (10 years)")
}

func TestValidPackSize(t *testing.T) {
	assert.True(t, billing.ValidPackSize(120))
	assert.True(t, billing.ValidPackSize(300))
	assert.True(t, billing.ValidPackSize(600))
	assert.False(t, billing.ValidPackSize(999))
}
