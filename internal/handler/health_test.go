package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/btc/drill/internal/handler"
	"github.com/stretchr/testify/require"
)

func TestHealthCheck_Healthy(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	pool := setupTestDB(t)
	b := &handler.Backend{Pool: pool}
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux, b)

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var body map[string]any
	err := json.Unmarshal(w.Body.Bytes(), &body)
	require.NoError(t, err)
	require.Equal(t, "ok", body["status"])
	require.Equal(t, true, body["db"])
}
