package observer

// TokenFanOut distributes to all child observers.
type TokenFanOut struct {
	observers []TokenObserver
}

// Noop is an empty fan-out with no observers. Used between turns so the
// observer pointer is never nil — Interrupt() on Noop is a safe no-op.
var Noop = &TokenFanOut{}

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

// Close calls Close() on each observer that implements the Closeable interface.
func (f *TokenFanOut) Close() {
	for _, o := range f.observers {
		if c, ok := o.(Closeable); ok {
			c.Close()
		}
	}
}
