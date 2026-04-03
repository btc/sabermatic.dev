package observer

import (
	"context"

	"github.com/coder/websocket"
)

// WSConn is the write interface for the WebSocket connection.
type WSConn interface {
	SendJSON(ctx context.Context, v any) error
	Close(code websocket.StatusCode, reason string) error
}

// TokenObserver receives streaming LLM tokens.
type TokenObserver interface {
	OnToken(token string)
	OnDone(fullMessage string)
	OnError(err error)
	Interrupt()
}

// Closeable is an optional interface for observers that need cleanup.
type Closeable interface {
	Close()
}
