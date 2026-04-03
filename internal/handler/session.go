package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/backend"
)

// CreateSession returns a handler that creates a new interview session.
func CreateSession(b *backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := auth.UserFromContext(r.Context())
		var req struct {
			QuestionID      uuid.UUID `json:"question_id"`
			DurationMinutes int       `json:"duration_minutes"`
			TTSEnabled      bool      `json:"tts_enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
			return
		}

		session, err := b.CreateSession(r.Context(), backend.CreateSessionParams{
			UserID:          user.ID,
			QuestionID:      req.QuestionID,
			DurationMinutes: req.DurationMinutes,
			TTSEnabled:      req.TTSEnabled,
			Plan:            user.Plan,
		})
		if err != nil {
			switch {
			case errors.Is(err, backend.ErrInvalidDuration):
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			case errors.Is(err, backend.ErrDurationExceedsPlan):
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			case errors.Is(err, backend.ErrQuestionNotFound):
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			case errors.Is(err, backend.ErrConcurrentSessionLimit):
				writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
			case errors.Is(err, backend.ErrInsufficientBalance):
				writeJSON(w, http.StatusForbidden, map[string]any{
					"error":   "insufficient_balance",
					"message": fmt.Sprintf("You need %d minutes but don't have enough available.", req.DurationMinutes),
				})
			default:
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			}
			return
		}

		writeJSON(w, http.StatusCreated, session)
	}
}

// ListSessions returns a handler that lists all sessions for the authenticated user.
func ListSessions(b *backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := auth.UserFromContext(r.Context())
		sessions, err := b.ListSessions(r.Context(), user.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		writeJSON(w, http.StatusOK, sessions)
	}
}

// GetSession returns a handler that fetches a single session by ID for the authenticated user.
func GetSession(b *backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := auth.UserFromContext(r.Context())
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid session id"})
			return
		}

		session, err := b.GetSessionForUser(r.Context(), id, user.ID)
		if err != nil {
			switch {
			case errors.Is(err, backend.ErrSessionNotFound):
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			case errors.Is(err, backend.ErrSessionNotOwned):
				writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
			default:
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			}
			return
		}

		writeJSON(w, http.StatusOK, session)
	}
}
