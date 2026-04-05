package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/handler"
	"github.com/btc/drill/internal/testutil"
)

func TestGetUsage_ExpiredGrant(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := testutil.NewTestBackend(t)
	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))
	pool := b.Pool()
	userID := createTestUser(t, b) // gets 60-min free trial

	// Expire the grant to simulate trial ended.
	_, err := pool.Exec(context.Background(),
		`UPDATE grants SET expires_at = NOW() - INTERVAL '1 hour' WHERE user_id = $1`, userID)
	require.NoError(t, err)

	cookie := createAuthCookie(t, pool, userID)

	req := authedRequest(t, http.MethodGet, "/api/me/usage", nil, cookie)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	// total_balance should be 0 (float64 from JSON unmarshaling into any)
	assert.Equal(t, float64(0), body["total_balance"])
}

func TestGetUsage_WithFreeGrant(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := testutil.NewTestBackend(t)
	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))
	pool := b.Pool()
	userID := createTestUser(t, b) // gets 60-min free trial
	cookie := createAuthCookie(t, pool, userID)

	req := authedRequest(t, http.MethodGet, "/api/me/usage", nil, cookie)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, float64(60), body["total_balance"])
	assert.Equal(t, float64(60), body["free_balance"])
	assert.Equal(t, float64(0), body["paid_balance"])
}
