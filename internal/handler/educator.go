package handler

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/backend"
)

// GetEducatorAnalysis returns a handler that fetches educator content for a session.
func GetEducatorAnalysis(b *backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := auth.UserFromContext(r.Context())
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid session id"})
			return
		}

		resp, err := b.GetEducatorAnalysis(r.Context(), id, user.ID)
		if err != nil {
			switch {
			case errors.Is(err, backend.ErrSessionNotFound):
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			case errors.Is(err, backend.ErrSessionNotOwned):
				writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
			case errors.Is(err, backend.ErrEvaluationNotReady):
				writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			default:
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			}
			return
		}

		writeJSON(w, http.StatusOK, resp)
	}
}

// RequestEducatorAnalysis returns a handler that enqueues educator content generation.
func RequestEducatorAnalysis(b *backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := auth.UserFromContext(r.Context())
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid session id"})
			return
		}

		err = b.RequestEducatorAnalysis(r.Context(), id, user.ID)
		if err != nil {
			switch {
			case errors.Is(err, backend.ErrAlreadyExists):
				writeJSON(w, http.StatusOK, map[string]string{"status": "completed"})
			case errors.Is(err, backend.ErrSessionNotFound):
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			case errors.Is(err, backend.ErrSessionNotOwned):
				writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
			case errors.Is(err, backend.ErrEvaluationNotReady):
				writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			case errors.Is(err, backend.ErrNoPaidBalance):
				writeJSON(w, http.StatusForbidden, map[string]string{
					"error":   "paid_balance_required",
					"message": "Full educator analysis requires a paid plan or minute balance.",
				})
			default:
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			}
			return
		}

		writeJSON(w, http.StatusAccepted, map[string]string{"status": "generating"})
	}
}
