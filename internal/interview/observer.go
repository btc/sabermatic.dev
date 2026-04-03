package interview

import "strings"

// TokenObserver receives streaming LLM tokens.
type TokenObserver interface {
	OnToken(token string)
	OnDone(fullMessage string)
	OnError(err error)
	Interrupt()
}

// TokenFanOut distributes to all child observers.
type TokenFanOut struct {
	observers []TokenObserver
}

func NewTokenFanOut(observers ...TokenObserver) *TokenFanOut {
	return &TokenFanOut{observers: observers}
}

func (f *TokenFanOut) OnToken(token string) {
	for _, o := range f.observers {
		o.OnToken(token)
	}
}

func (f *TokenFanOut) OnDone(fullMessage string) {
	for _, o := range f.observers {
		o.OnDone(fullMessage)
	}
}

func (f *TokenFanOut) OnError(err error) {
	for _, o := range f.observers {
		o.OnError(err)
	}
}

func (f *TokenFanOut) Interrupt() {
	for _, o := range f.observers {
		o.Interrupt()
	}
}

// MessageAccumulator builds the complete response for DB persistence.
type MessageAccumulator struct {
	buf strings.Builder
}

func NewMessageAccumulator() *MessageAccumulator {
	return &MessageAccumulator{}
}

func (a *MessageAccumulator) OnToken(token string) { a.buf.WriteString(token) }
func (a *MessageAccumulator) OnDone(string)         {}
func (a *MessageAccumulator) OnError(error)          {}
func (a *MessageAccumulator) Interrupt()             {}
func (a *MessageAccumulator) Text() string           { return a.buf.String() }
