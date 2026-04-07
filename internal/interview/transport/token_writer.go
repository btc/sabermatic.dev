package transport

import "github.com/google/uuid"

type TokenWriter struct {
	client    Client
	messageID uuid.UUID
}

func NewTokenWriter(client Client, messageID uuid.UUID) *TokenWriter {
	return &TokenWriter{client: client, messageID: messageID}
}

func (w *TokenWriter) OnToken(token string) {
	w.client.InterviewerToken(token)
}

func (w *TokenWriter) OnDone(_ string) {
	w.client.InterviewerDone(w.messageID)
}

func (w *TokenWriter) OnError(err error) {
	w.client.Error(ClientError{Code: "llm_stream_error", Message: err.Error()})
}
