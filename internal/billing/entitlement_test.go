package billing_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/billing"
)

func TestCanAccessEducator_PaidBalance(t *testing.T) {
	level := billing.DetermineEducatorAccess(100, 0, 1)
	assert.Equal(t, billing.Full, level)
}

func TestCanAccessEducator_FreeTasteAvailable(t *testing.T) {
	level := billing.DetermineEducatorAccess(0, 0, 1)
	assert.Equal(t, billing.FreeTaste, level)
}

func TestCanAccessEducator_FreeTasteExhausted(t *testing.T) {
	level := billing.DetermineEducatorAccess(0, 1, 1)
	assert.Equal(t, billing.Preview, level)
}

func TestCanAccessCoach_PaidBalance(t *testing.T) {
	assert.True(t, billing.CanAccessCoach(100))
}

func TestCanAccessCoach_NoPaidBalance(t *testing.T) {
	assert.False(t, billing.CanAccessCoach(0))
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
