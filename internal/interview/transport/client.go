package transport

import (
	"github.com/google/uuid"

	"github.com/btc/drill/internal/db"
)

// Client is the conductor's interface to the connected client.
// The conductor emits domain events; the transport implementation
// handles protocol encoding (WebSocket JSON, test recorder, etc.).
//
// Implementations must be safe for concurrent use. The conductor's
// main goroutine, audio upload goroutine, readLoop goroutine, and
// TTS goroutine all call Client methods.
type Client interface {
	StateChange(state string)
	SessionEnded(reason string)
	TranscriptionResult(text string)
	TimerWarning(minutesRemaining int)
	TimerOvertime()
	ReconnectPlease()
	Pong()
	TTSError()
	TTSDone(messageID uuid.UUID)
	AudioUploadFailed()
	InterviewerToken(token string)
	InterviewerDone(messageID uuid.UUID)

	Error(ClientError)
	SessionLoaded(SessionLoaded)
	ReconnectState(ReconnectState)
	TTSChunk(TTSChunk)
}

type ClientError struct {
	Code    string
	Message string
}

type SessionLoaded struct {
	SessionID   uuid.UUID
	Question    db.Question
	DurationMin int
	TTSEnabled  bool
}

type ReconnectState struct {
	LastSeq  int
	Messages []db.Message
}

// Missed returns only messages with seq > LastSeq.
func (r ReconnectState) Missed() []db.Message {
	missed := make([]db.Message, 0, len(r.Messages))
	for _, m := range r.Messages {
		if int(m.Seq) > r.LastSeq {
			missed = append(missed, m)
		}
	}
	return missed
}

type TTSChunk struct {
	MessageID uuid.UUID
	Data      []byte
	Seq       int
}
