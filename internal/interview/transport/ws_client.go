package transport

import (
	"context"
	"encoding/base64"
	"log/slog"

	"github.com/google/uuid"

	"github.com/btc/drill/internal/interview/observer"
)

// WSClient implements Client over a coder/websocket connection.
// Thread-safe: coder/websocket serializes writes internally.
type WSClient struct {
	ws observer.WSConn
}

func NewWSClient(ws observer.WSConn) *WSClient {
	return &WSClient{ws: ws}
}

func (c *WSClient) send(v any) {
	if err := c.ws.SendJSON(context.Background(), v); err != nil {
		slog.Debug("transport: send failed", "error", err)
	}
}

func (c *WSClient) StateChange(state string) {
	c.send(map[string]string{"type": "state_change", "state": state})
}

func (c *WSClient) Ack(action string) {
	c.send(map[string]string{"type": "ack", "action": action})
}

func (c *WSClient) TranscriptionResult(text string) {
	c.send(map[string]string{"type": "transcription_result", "text": text})
}

func (c *WSClient) TimerWarning(minutesRemaining int) {
	c.send(map[string]any{"type": "timer_warning", "minutes_remaining": minutesRemaining})
}

func (c *WSClient) TimerOvertime() {
	c.send(map[string]string{"type": "timer_overtime"})
}

func (c *WSClient) ReconnectPlease() {
	c.send(map[string]string{"type": "reconnect_please"})
}

func (c *WSClient) Pong() {
	c.send(map[string]string{"type": "pong"})
}

func (c *WSClient) TTSError() {
	c.send(map[string]string{"type": "tts_error"})
}

func (c *WSClient) TTSDone(messageID uuid.UUID) {
	c.send(map[string]any{"type": "tts_done", "message_id": messageID.String()})
}

func (c *WSClient) AudioUploadFailed() {
	c.send(map[string]string{"type": "audio_upload_failed"})
}

func (c *WSClient) InterviewerToken(token string) {
	c.send(map[string]string{"type": "interviewer_token", "token": token})
}

func (c *WSClient) InterviewerDone(messageID uuid.UUID) {
	c.send(map[string]any{"type": "interviewer_done", "message_id": messageID.String()})
}

func (c *WSClient) Error(e ClientError) {
	c.send(map[string]string{"type": "error", "code": e.Code, "message": e.Message})
}

func (c *WSClient) SessionLoaded(s SessionLoaded) {
	c.send(map[string]any{
		"type":        "session_loaded",
		"session_id":  s.SessionID.String(),
		"question":    map[string]string{"title": s.Question.Title, "prompt": s.Question.Prompt},
		"duration":    s.DurationMin,
		"tts_enabled": s.TTSEnabled,
	})
}

func (c *WSClient) ReconnectState(r ReconnectState) {
	c.send(map[string]any{"type": "reconnect_state", "messages": r.Missed()})
}

func (c *WSClient) TTSChunk(chunk TTSChunk) {
	c.send(map[string]any{
		"type":       "tts_chunk",
		"data":       base64.StdEncoding.EncodeToString(chunk.Data),
		"message_id": chunk.MessageID.String(),
		"seq":        chunk.Seq,
	})
}
