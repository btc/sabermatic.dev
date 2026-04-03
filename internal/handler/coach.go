package handler

import (
	"errors"
	"net/http"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/backend"
)

// GetCoachAnalysis returns a handler that fetches the latest coach analysis.
func GetCoachAnalysis(b *backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := auth.UserFromContext(r.Context())

		resp, err := b.GetLatestCoachAnalysis(r.Context(), user.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		if resp == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no coach analysis yet"})
			return
		}

		writeJSON(w, http.StatusOK, resp)
	}
}

// RequestCoachAnalysis returns a handler that enqueues a coach analysis.
func RequestCoachAnalysis(b *backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := auth.UserFromContext(r.Context())
		force := r.URL.Query().Get("force") == "true"

		err := b.RequestCoachAnalysis(r.Context(), user.ID, force)
		if err != nil {
			switch {
			case errors.Is(err, backend.ErrNoNewSessions):
				writeJSON(w, http.StatusOK, map[string]string{"status": "up_to_date"})
			default:
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			}
			return
		}

		writeJSON(w, http.StatusAccepted, map[string]string{"status": "analyzing"})
	}
}
