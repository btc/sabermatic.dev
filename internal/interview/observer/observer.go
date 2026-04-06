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

// TTSSink receives synthesized audio from the TTS accumulator.
// Implementations must be safe for concurrent calls — HandleTTSError
// may be called from the conductor goroutine (via OnToken buffer-full)
// while HandleAudio is called from the TTS goroutine.
type TTSSink interface {
	HandleAudio(data []byte)
	HandleTTSDone()
	HandleTTSError()
}
