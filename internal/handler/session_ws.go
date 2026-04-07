package handler

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/backend"
	"github.com/btc/drill/internal/interview"
	"github.com/btc/drill/internal/interview/transport"
)

// SessionWS returns a handler that upgrades to WebSocket and runs the interview conductor.
func SessionWS(b *backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		user := auth.UserFromContext(ctx)
		if user == nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
			return
		}

		// Parse session ID from path.
		sessionID, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid session id"})
			return
		}

		// Load session, verify ownership and active status.
		session, err := b.GetSessionForUser(ctx, sessionID, user.ID)
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
		if session.Status != "active" {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "session is not active"})
			return
		}

		// Upgrade to WebSocket.
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			slog.Error("websocket accept", "error", err, "session_id", sessionID)
			return
		}
		ws.SetReadLimit(10 * 1024 * 1024) // 10MB for audio

		// Hand off to the conductor. Run acquires the lock, reads session_init,
		// and blocks until the session ends.
		conductor := interview.NewConductor(interview.ConductorParams{
			Client:    transport.NewWSClient(&interview.Conn{WS: ws}),
			RawWS:     ws,
			Backend:   b,
			SessionID: sessionID,
			UserID:    user.ID,
		})
		conductor.Run(r.Context())
	}
}
