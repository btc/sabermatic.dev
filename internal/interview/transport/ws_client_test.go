package transport_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/interview/transport"
)

type mockWSConn struct {
	sent [][]byte
}

func (m *mockWSConn) SendJSON(_ context.Context, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	m.sent = append(m.sent, data)
	return nil
}

func (m *mockWSConn) Close(_ websocket.StatusCode, _ string) error {
	return nil
}

// last returns the last sent message decoded as map[string]any.
func last(t *testing.T, m *mockWSConn) map[string]any {
	t.Helper()
	require.NotEmpty(t, m.sent, "no messages sent")
	var result map[string]any
	require.NoError(t, json.Unmarshal(m.sent[len(m.sent)-1], &result))
	return result
}

func TestWSClient_StateChange(t *testing.T) {
	ws := &mockWSConn{}
	c := transport.NewWSClient(ws)
	c.StateChange("active")
	msg := last(t, ws)
	assert.Equal(t, "state_change", msg["type"])
	assert.Equal(t, "active", msg["state"])
}

func TestWSClient_Pong(t *testing.T) {
	ws := &mockWSConn{}
	c := transport.NewWSClient(ws)
	c.Pong()
	msg := last(t, ws)
	assert.Equal(t, "pong", msg["type"])
}

func TestWSClient_Error(t *testing.T) {
	ws := &mockWSConn{}
	c := transport.NewWSClient(ws)
	c.Error(transport.ClientError{Code: "bad_request", Message: "invalid input"})
	msg := last(t, ws)
	assert.Equal(t, "error", msg["type"])
	assert.Equal(t, "bad_request", msg["code"])
	assert.Equal(t, "invalid input", msg["message"])
}

func TestWSClient_InterviewerToken(t *testing.T) {
	ws := &mockWSConn{}
	c := transport.NewWSClient(ws)
	c.InterviewerToken("hello")
	msg := last(t, ws)
	assert.Equal(t, "interviewer_token", msg["type"])
	assert.Equal(t, "hello", msg["token"])
}

func TestWSClient_InterviewerDone(t *testing.T) {
	ws := &mockWSConn{}
	c := transport.NewWSClient(ws)
	msgID := uuid.New()
	c.InterviewerDone(msgID)
	msg := last(t, ws)
	assert.Equal(t, "interviewer_done", msg["type"])
	assert.Equal(t, msgID.String(), msg["message_id"])
}

func TestWSClient_SessionLoaded(t *testing.T) {
	ws := &mockWSConn{}
	c := transport.NewWSClient(ws)
	sessionID := uuid.New()
	c.SessionLoaded(transport.SessionLoaded{
		SessionID:   sessionID,
		Question:    db.Question{Title: "Two Sum", Prompt: "Find two numbers..."},
		DurationMin: 45,
		TTSEnabled:  true,
	})
	msg := last(t, ws)
	assert.Equal(t, "session_loaded", msg["type"])
	assert.Equal(t, sessionID.String(), msg["session_id"])
	assert.Equal(t, float64(45), msg["duration"])
	assert.Equal(t, true, msg["tts_enabled"])
	q, ok := msg["question"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "Two Sum", q["title"])
	assert.Equal(t, "Find two numbers...", q["prompt"])
}

func TestWSClient_ReconnectState(t *testing.T) {
	ws := &mockWSConn{}
	c := transport.NewWSClient(ws)

	msgs := []db.Message{
		{Seq: 1, Role: "user", Content: "hello"},
		{Seq: 2, Role: "assistant", Content: "hi"},
		{Seq: 3, Role: "user", Content: "thanks"},
	}
	// LastSeq=1 means only seq 2 and 3 are missed
	c.ReconnectState(transport.ReconnectState{LastSeq: 1, Messages: msgs})
	msg := last(t, ws)
	assert.Equal(t, "reconnect_state", msg["type"])
	messages, ok := msg["messages"].([]any)
	require.True(t, ok)
	assert.Len(t, messages, 2, "only missed messages should be sent")
}

func TestWSClient_TTSChunk(t *testing.T) {
	ws := &mockWSConn{}
	c := transport.NewWSClient(ws)
	msgID := uuid.New()
	data := []byte("audio data")
	c.TTSChunk(transport.TTSChunk{
		MessageID: msgID,
		Data:      data,
		Seq:       3,
	})
	msg := last(t, ws)
	assert.Equal(t, "tts_chunk", msg["type"])
	assert.Equal(t, msgID.String(), msg["message_id"])
	assert.Equal(t, float64(3), msg["seq"])
	// data field should be base64-encoded
	assert.NotEmpty(t, msg["data"])
}

func TestWSClient_Ack(t *testing.T) {
	ws := &mockWSConn{}
	c := transport.NewWSClient(ws)
	c.Ack("cancel_session")
	msg := last(t, ws)
	assert.Equal(t, "ack", msg["type"])
	assert.Equal(t, "cancel_session", msg["action"])
}

func TestWSClient_TTSDone(t *testing.T) {
	ws := &mockWSConn{}
	c := transport.NewWSClient(ws)
	msgID := uuid.New()
	c.TTSDone(msgID)
	msg := last(t, ws)
	assert.Equal(t, "tts_done", msg["type"])
	assert.Equal(t, msgID.String(), msg["message_id"])
}

func TestWSClient_TranscriptionResult(t *testing.T) {
	ws := &mockWSConn{}
	c := transport.NewWSClient(ws)
	c.TranscriptionResult("I would use a hash table.")
	msg := last(t, ws)
	assert.Equal(t, "transcription_result", msg["type"])
	assert.Equal(t, "I would use a hash table.", msg["text"])
}

func TestWSClient_TimerWarning(t *testing.T) {
	ws := &mockWSConn{}
	c := transport.NewWSClient(ws)
	c.TimerWarning(5)
	msg := last(t, ws)
	assert.Equal(t, "timer_warning", msg["type"])
	assert.Equal(t, float64(5), msg["minutes_remaining"])
}

func TestWSClient_TimerOvertime(t *testing.T) {
	ws := &mockWSConn{}
	c := transport.NewWSClient(ws)
	c.TimerOvertime()
	msg := last(t, ws)
	assert.Equal(t, "timer_overtime", msg["type"])
}

func TestWSClient_ReconnectPlease(t *testing.T) {
	ws := &mockWSConn{}
	c := transport.NewWSClient(ws)
	c.ReconnectPlease()
	msg := last(t, ws)
	assert.Equal(t, "reconnect_please", msg["type"])
}

func TestWSClient_TTSError(t *testing.T) {
	ws := &mockWSConn{}
	c := transport.NewWSClient(ws)
	c.TTSError()
	msg := last(t, ws)
	assert.Equal(t, "tts_error", msg["type"])
}

func TestWSClient_AudioUploadFailed(t *testing.T) {
	ws := &mockWSConn{}
	c := transport.NewWSClient(ws)
	c.AudioUploadFailed()
	msg := last(t, ws)
	assert.Equal(t, "audio_upload_failed", msg["type"])
}
