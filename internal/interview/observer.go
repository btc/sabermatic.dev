package interview

import (
	"context"
	"encoding/base64"
	"io"
	"strings"

	"github.com/google/uuid"

	"github.com/btc/drill/internal/ai"
)

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

// WSWriter streams LLM tokens to the WebSocket client.
// Write errors are ignored so the LLM stream continues uninterrupted,
// allowing MessageAccumulator to persist the full response on disconnect.
type WSWriter struct {
	ws        WSConn
	messageID uuid.UUID
	ctx       context.Context
}

func NewWSWriter(ws WSConn, messageID uuid.UUID) *WSWriter {
	return &WSWriter{ws: ws, messageID: messageID, ctx: context.Background()}
}

func (w *WSWriter) OnToken(token string) {
	_ = w.ws.SendJSON(w.ctx, map[string]string{
		"type":  "interviewer_token",
		"token": token,
	})
}

func (w *WSWriter) OnDone(fullMessage string) {
	_ = w.ws.SendJSON(w.ctx, map[string]string{
		"type":       "interviewer_done",
		"message_id": w.messageID.String(),
	})
}

func (w *WSWriter) OnError(err error) {
	_ = w.ws.SendJSON(w.ctx, map[string]any{
		"type":    "error",
		"message": err.Error(),
	})
}

func (w *WSWriter) Interrupt() {} // no-op

// TTSAccumulator buffers tokens, detects sentence boundaries, and fires TTS
// synthesis for each sentence in a dedicated goroutine.
type TTSAccumulator struct {
	ws        WSConn
	synth     ai.Synthesizer
	messageID uuid.UUID
	ctx       context.Context
	cancel    context.CancelFunc
	sentCh    chan string      // sentences to synthesize
	done      chan struct{}    // closed when TTS goroutine exits
	buf       strings.Builder // token buffer for sentence detection
	seq       int             // tts_chunk sequence counter
}

// NewTTSAccumulator constructs a TTSAccumulator and starts the TTS goroutine.
func NewTTSAccumulator(ctx context.Context, ws WSConn, synth ai.Synthesizer, messageID uuid.UUID) *TTSAccumulator {
	ctx, cancel := context.WithCancel(ctx)
	acc := &TTSAccumulator{
		ws:        ws,
		synth:     synth,
		messageID: messageID,
		ctx:       ctx,
		cancel:    cancel,
		sentCh:    make(chan string, 32),
		done:      make(chan struct{}),
	}
	go acc.ttsLoop()
	return acc
}

// sentenceBoundaries are the two-character suffixes that end a sentence.
var sentenceBoundaries = []string{". ", "? ", "! ", ".\n", "?\n", "!\n"}

// OnToken appends the token to the buffer and flushes any complete sentences.
func (a *TTSAccumulator) OnToken(token string) {
	a.buf.WriteString(token)
	for {
		s := a.buf.String()
		idx := -1
		boundaryLen := 0
		for _, b := range sentenceBoundaries {
			if i := strings.Index(s, b); i >= 0 {
				if idx == -1 || i < idx {
					idx = i
					boundaryLen = len(b)
				}
			}
		}
		if idx == -1 {
			break
		}
		sentence := s[:idx+boundaryLen]
		rest := s[idx+boundaryLen:]
		a.buf.Reset()
		a.buf.WriteString(rest)
		select {
		case a.sentCh <- sentence:
		case <-a.ctx.Done():
			return
		}
	}
}

// OnDone flushes remaining buffer, closes sentCh, and waits for TTS to finish.
func (a *TTSAccumulator) OnDone(_ string) {
	if remaining := strings.TrimSpace(a.buf.String()); remaining != "" {
		select {
		case a.sentCh <- remaining:
		case <-a.ctx.Done():
		}
	}
	close(a.sentCh)
	a.Wait()
}

// OnError cancels the context to abort in-flight TTS.
func (a *TTSAccumulator) OnError(_ error) {
	a.cancel()
}

// Interrupt cancels the context (called by cancel_tts client message).
func (a *TTSAccumulator) Interrupt() {
	a.cancel()
}

// Wait blocks until the TTS goroutine has finished.
func (a *TTSAccumulator) Wait() {
	<-a.done
}

// ttsLoop reads sentences from sentCh, synthesizes each one, and streams
// the resulting audio chunks to the WebSocket client.
func (a *TTSAccumulator) ttsLoop() {
	defer close(a.done)

	for sentence := range a.sentCh {
		if a.ctx.Err() != nil {
			// Context cancelled; drain remaining sentences without synthesising.
			continue
		}

		rc, err := a.synth.Synthesize(a.ctx, sentence)
		if err != nil {
			// Synthesis failed or was cancelled; skip this sentence.
			continue
		}

		buf := make([]byte, 4096)
		for {
			n, readErr := rc.Read(buf)
			if n > 0 {
				chunk := base64.StdEncoding.EncodeToString(buf[:n])
				_ = a.ws.SendJSON(a.ctx, map[string]any{
					"type":       "tts_chunk",
					"data":       chunk,
					"message_id": a.messageID.String(),
					"seq":        a.seq,
				})
				a.seq++
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				break
			}
		}
		rc.Close()
	}

	_ = a.ws.SendJSON(a.ctx, map[string]any{
		"type":       "tts_done",
		"message_id": a.messageID.String(),
	})
}
