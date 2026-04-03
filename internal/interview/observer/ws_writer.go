package observer

import (
	"context"

	"github.com/google/uuid"
)

// WSWriter streams LLM tokens to the WebSocket client.
// Write errors are ignored so the LLM stream continues uninterrupted,
// allowing MessageAccumulator to persist the full response on disconnect.
type WSWriter struct {
	ws        WSConn
	messageID uuid.UUID
	ctx       context.Context
}

// NewWSWriter creates a WSWriter that sends interviewer tokens to the client.
// NB: Uses context.Background() intentionally — writes must continue during
// client disconnect so the LLM stream completes and MessageAccumulator can
// persist the full response. Write errors are logged and ignored.
func NewWSWriter(ws WSConn, messageID uuid.UUID) *WSWriter {
	return &WSWriter{ws: ws, messageID: messageID, ctx: context.Background()}
}

func (w *WSWriter) OnToken(token string) {
	_ = w.ws.SendJSON(w.ctx, map[string]string{
		"type":  "interviewer_token",
		"token": token,
	})
}

func (w *WSWriter) OnDone(fullMessage string) {
	_ = w.ws.SendJSON(w.ctx, map[string]string{
		"type":       "interviewer_done",
		"message_id": w.messageID.String(),
	})
}

func (w *WSWriter) OnError(err error) {
	_ = w.ws.SendJSON(w.ctx, map[string]any{
		"type":    "error",
		"message": err.Error(),
	})
}

func (w *WSWriter) Interrupt() {} // no-op
