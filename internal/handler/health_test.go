package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/btc/drill/internal/handler"
	samplesvc "github.com/btc/drill/internal/rpc/sample"
	"github.com/stretchr/testify/require"
)

func TestHealthCheck_Healthy(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	ss, err := samplesvc.NewSampleService()
	require.NoError(t, err)
	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b, ss))

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var body map[string]any
	err = json.Unmarshal(w.Body.Bytes(), &body)
	require.NoError(t, err)
	require.Equal(t, "ok", body["status"])
	require.Equal(t, true, body["db"])
}
