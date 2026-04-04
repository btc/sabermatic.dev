package interview

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/coder/websocket"

	"github.com/btc/drill/internal/interview/observer"
)

// Preferred file extension for audio MIME types.
// stdlib mime DB either lacks these or returns unexpected first entries
// (e.g. "audio/ogg" -> ".oga" instead of ".ogg").
var audioExt = map[string]string{
	"audio/webm": "webm",
	"audio/wav":  "wav",
	"audio/ogg":  "ogg",
	"audio/mpeg": "mp3",
	"audio/mp4":  "m4a",
}

// Conn wraps a *websocket.Conn to implement observer.WSConn.
type Conn struct {
	WS *websocket.Conn
}

// Verify Conn implements observer.WSConn at compile time.
var _ observer.WSConn = (*Conn)(nil)

func (c *Conn) SendJSON(ctx context.Context, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal ws message: %w", err)
	}
	return c.WS.Write(ctx, websocket.MessageText, data)
}

func (c *Conn) Close(code websocket.StatusCode, reason string) error {
	return c.WS.Close(code, reason)
}

// WSMessage represents a client-to-server WebSocket message.
type WSMessage struct {
	Type         string          `json:"type"`
	LastSeq      *int            `json:"last_seq,omitempty"`
	Content      string          `json:"content,omitempty"`
	Audio        []byte          `json:"-"`
	AudioBase64  string          `json:"audio,omitempty"`
	AudioMIME    string          `json:"audio_mime,omitempty"`
	InputMethod  string          `json:"input_method,omitempty"`
	TraceContext *wsTraceContext `json:"trace_context,omitempty"`
}

// wsTraceContext carries W3C traceparent from the client.
type wsTraceContext struct {
	Traceparent string `json:"traceparent"`
}

// ParseWSMessage parses a raw JSON WebSocket message.
func ParseWSMessage(data []byte) (WSMessage, error) {
	var msg WSMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return WSMessage{}, fmt.Errorf("unmarshal ws message: %w", err)
	}
	if msg.Type == "" {
		return WSMessage{}, fmt.Errorf("missing message type")
	}
	if msg.AudioBase64 != "" {
		audio, err := base64.StdEncoding.DecodeString(msg.AudioBase64)
		if err != nil {
			return WSMessage{}, fmt.Errorf("decode audio base64: %w", err)
		}
		msg.Audio = audio
		if msg.AudioMIME == "" {
			msg.AudioMIME = "audio/webm"
		}
	}
	return msg, nil
}

// traceparent returns the W3C traceparent string, or "" if not present.
func (m WSMessage) traceparent() string {
	if m.TraceContext == nil {
		return ""
	}
	return m.TraceContext.Traceparent
}

// AudioExt returns the file extension derived from AudioMIME (e.g. "audio/webm" -> "webm").
func (m WSMessage) AudioExt() string {
	if ext, ok := audioExt[m.AudioMIME]; ok {
		return ext
	}
	// Fallback: extract subtype from MIME.
	_, ext, _ := strings.Cut(m.AudioMIME, "/")
	if ext == "" {
		return "bin"
	}
	return ext
}
