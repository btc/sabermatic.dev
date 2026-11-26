package observer

// TokenObserver receives streaming LLM tokens.
type TokenObserver interface {
	OnToken(token string)
	OnDone(fullMessage string)
	OnError(err error)
}

// Closeable is an optional interface for observers that need cleanup.
type Closeable interface {
	Close()
}

// TTSSink receives synthesized audio from the TTS accumulator.
// Implementations must be safe for concurrent calls — HandleTTSError
// may be called from the conductor goroutine (via OnToken buffer-full)
// while HandleAudio is called from the TTS goroutine.
//
// HandleAudio receives one piece of audio. An empty mimeType means data is a
// complete encoded file (MP3 or WAV); otherwise mimeType is a PCM type from
// ai.PCMMIMEType and data is the next piece of a stream.
type TTSSink interface {
	HandleAudio(data []byte, mimeType string)
	HandleTTSDone()
	HandleTTSError()
}
