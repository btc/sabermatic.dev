package billing_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/billing"
)

func TestCanAccessEducator_PaidBalance(t *testing.T) {
	level, err := billing.DetermineEducatorAccess(context.Background(), 100, 0, 1)
	require.NoError(t, err)
	assert.Equal(t, billing.Full, level)
}

func TestCanAccessEducator_FreeTasteAvailable(t *testing.T) {
	level, err := billing.DetermineEducatorAccess(context.Background(), 0, 0, 1)
	require.NoError(t, err)
	assert.Equal(t, billing.FreeTaste, level)
}

func TestCanAccessEducator_FreeTasteExhausted(t *testing.T) {
	level, err := billing.DetermineEducatorAccess(context.Background(), 0, 1, 1)
	require.NoError(t, err)
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

func TestPackByMinutes(t *testing.T) {
	pack, ok := billing.PackByMinutes(120)
	require.True(t, ok)
	assert.Equal(t, 120, pack.Minutes)

	_, ok = billing.PackByMinutes(999)
	assert.False(t, ok)
}

func TestEndOfMonth(t *testing.T) {
	d := time.Date(2026, 4, 15, 10, 30, 0, 0, time.UTC)
	eom := billing.EndOfMonth(d)
	assert.Equal(t, time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), eom)

	d = time.Date(2026, 12, 25, 0, 0, 0, 0, time.UTC)
	eom = billing.EndOfMonth(d)
	assert.Equal(t, time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC), eom)
}
