package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/testutil"
)

func TestHealthCheck_Healthy(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	h := testutil.NewTestHandler(t, b)

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var body map[string]any
	err := json.Unmarshal(w.Body.Bytes(), &body)
	require.NoError(t, err)
	require.Equal(t, "ok", body["status"])
	require.Equal(t, true, body["db"])
}
