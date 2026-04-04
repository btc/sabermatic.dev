package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/handler"
)

func TestGetUsage_EmptyBalance(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	b := newTestBackend(t)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux, b)
	pool := b.Pool()
	userID := createTestUser(t, pool)
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
	b := newTestBackend(t)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux, b)
	pool := b.Pool()
	userID := createTestUser(t, pool)
	cookie := createAuthCookie(t, pool, userID)

	// Create a free grant
	err := db.New(pool).EnsureFreeGrant(context.Background(), db.EnsureFreeGrantParams{
		UserID:         userID,
		InitialMinutes: 60,
		ExpiresAt:      pgtype.Timestamptz{Time: time.Now().Add(30 * 24 * time.Hour), Valid: true},
	})
	require.NoError(t, err)

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
