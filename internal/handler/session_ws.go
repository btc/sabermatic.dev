package handler

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/backend"
	"github.com/btc/drill/internal/interview"
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

		// Acquire advisory lock (dedicated connection).
		lockConn, err := b.Pool().Acquire(ctx)
		if err != nil {
			slog.Error("acquire lock conn", "error", err, "session_id", sessionID)
			ws.Close(websocket.StatusInternalError, "failed to acquire lock connection")
			return
		}

		// pg_try_advisory_lock using two 32-bit halves of session UUID.
		key1 := int32(binary.BigEndian.Uint32(sessionID[:4]))
		key2 := int32(binary.BigEndian.Uint32(sessionID[4:8]))
		var locked bool
		err = lockConn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1, $2)", key1, key2).Scan(&locked)
		if err != nil {
			slog.Error("advisory lock query", "error", err, "session_id", sessionID)
			lockConn.Release()
			ws.Close(websocket.StatusInternalError, "failed to acquire advisory lock")
			return
		}
		if !locked {
			slog.Warn("session already locked", "session_id", sessionID)
			lockConn.Release()
			errMsg, _ := json.Marshal(map[string]string{
				"type":    "error",
				"code":    "session_locked",
				"message": "another connection is already active for this session",
			})
			ws.Write(ctx, websocket.MessageText, errMsg)
			ws.Close(websocket.StatusPolicyViolation, "session already locked")
			return
		}

		// Read session_init with 10s timeout.
		initCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, data, err := ws.Read(initCtx)
		cancel()
		if err != nil {
			slog.Error("read session_init", "error", err, "session_id", sessionID)
			lockConn.Release()
			ws.Close(websocket.StatusProtocolError, "expected session_init")
			return
		}

		initMsg, err := interview.ParseWSMessage(data)
		if err != nil || initMsg.Type != "session_init" {
			slog.Error("parse session_init", "error", err, "session_id", sessionID)
			lockConn.Release()
			ws.Close(websocket.StatusProtocolError, "expected session_init message")
			return
		}

		// Build Conductor.
		conn := &interview.Conn{WS: ws}

		// Determine TTS synthesizer — nil if disabled.
		var tts = b.TTS()
		if !session.ConfigTtsEnabled {
			tts = nil
		}

		conductor := interview.NewConductor(interview.ConductorParams{
			WS:        conn,
			Pool:      b.Pool(),
			LockConn:  lockConn,
			Jobs:      b.Jobs(),
			LLM:       b.LLM(),
			STT:       b.STT(),
			TTS:       tts,
			SessionID: sessionID,
			UserID:    user.ID,
			InitMsg:   initMsg,
			Model:     b.Config().LLM.InterviewerModel,
			Duration:  time.Duration(session.ConfigDurationMinutes) * time.Minute,
		})

		// Launch conductor.Run in goroutine.
		go conductor.Run(ctx)

		// readLoop blocks this goroutine — closing msgCh signals the conductor.
		readLoop(ctx, ws, conductor.MsgCh(), func() *interview.TokenFanOut {
			return conductor.Observer()
		})
	}
}

// readLoop reads messages from the WebSocket and forwards them to the conductor.
// On error (disconnect), it closes msgCh to signal the conductor.
func readLoop(ctx context.Context, ws *websocket.Conn, msgCh chan<- interview.WSMessage, observer func() *interview.TokenFanOut) {
	defer close(msgCh)
	for {
		_, data, err := ws.Read(ctx)
		if err != nil {
			return
		}
		msg, err := interview.ParseWSMessage(data)
		if err != nil {
			errJSON, _ := json.Marshal(map[string]string{
				"type":    "error",
				"code":    "malformed_message",
				"message": err.Error(),
			})
			ws.Write(ctx, websocket.MessageText, errJSON)
			continue
		}
		if msg.Type == "cancel_tts" {
			if obs := observer(); obs != nil {
				obs.Interrupt()
			}
			continue
		}
		msgCh <- msg
	}
}
