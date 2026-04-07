package observer

import "strings"

// MessageAccumulator builds the complete response for DB persistence.
type MessageAccumulator struct {
	buf strings.Builder
}

func NewMessageAccumulator() *MessageAccumulator {
	return &MessageAccumulator{}
}

func (a *MessageAccumulator) OnToken(token string) { a.buf.WriteString(token) }
func (a *MessageAccumulator) OnDone(string)        {}
func (a *MessageAccumulator) OnError(error)        {}
func (a *MessageAccumulator) Text() string         { return a.buf.String() }
