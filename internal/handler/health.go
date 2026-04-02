package handler

import (
	"encoding/json"
	"net/http"

	"github.com/btc/drill/internal/backend"
)

// Health returns a handler that checks database connectivity.
func Health(b *backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		dbOK := true
		if err := b.Pool.Ping(ctx); err != nil {
			dbOK = false
		}

		status := "ok"
		httpStatus := http.StatusOK
		if !dbOK {
			status = "degraded"
			httpStatus = http.StatusServiceUnavailable
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(httpStatus)
		json.NewEncoder(w).Encode(map[string]any{
			"status": status,
			"db":     dbOK,
		})
	}
}
