package handler

import (
	"net/http"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/backend"
)

// ListQuestions returns a handler that lists all questions visible to the authenticated user.
func ListQuestions(b *backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := auth.UserFromContext(r.Context())
		questions, err := b.ListQuestions(r.Context(), user.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		writeJSON(w, http.StatusOK, questions)
	}
}
