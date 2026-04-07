package transport

import (
	"github.com/google/uuid"

	"github.com/btc/drill/internal/interview/observer"
)

type ttsSink struct {
	client    Client
	messageID uuid.UUID
	seq       int
}

func NewTTSSink(client Client, messageID uuid.UUID) observer.TTSSink {
	return &ttsSink{client: client, messageID: messageID}
}

func (s *ttsSink) HandleAudio(data []byte) {
	s.client.TTSChunk(TTSChunk{
		MessageID: s.messageID,
		Data:      data,
		Seq:       s.seq,
	})
	s.seq++
}

func (s *ttsSink) HandleTTSDone() {
	s.client.TTSDone(s.messageID)
}

func (s *ttsSink) HandleTTSError() {
	s.client.TTSError()
}
