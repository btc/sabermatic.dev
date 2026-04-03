package observer

import (
	"context"
	"encoding/base64"
	"io"
	"strings"

	"github.com/google/uuid"

	"github.com/btc/drill/internal/ai"
)

// TTSAccumulator buffers tokens, detects sentence boundaries, and fires TTS
// synthesis for each sentence in a dedicated goroutine.
type TTSAccumulator struct {
	ws        WSConn
	synth     ai.Synthesizer
	messageID uuid.UUID
	ctx       context.Context
	cancel    context.CancelFunc
	sentCh    chan string   // sentences to synthesize
	done      chan struct{} // closed when TTS goroutine exits
	buf       strings.Builder
	seq       int
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

// OnDone flushes remaining buffer and closes sentCh.
func (a *TTSAccumulator) OnDone(_ string) {
	if remaining := strings.TrimSpace(a.buf.String()); remaining != "" {
		select {
		case a.sentCh <- remaining:
		case <-a.ctx.Done():
		}
	}
	close(a.sentCh)
}

// OnError cancels the context to abort in-flight TTS.
func (a *TTSAccumulator) OnError(_ error) {
	a.cancel()
}

// Interrupt cancels the context (called by cancel_tts client message).
func (a *TTSAccumulator) Interrupt() {
	a.cancel()
}

// Close waits for the TTS goroutine to finish. Implements the Closeable interface.
func (a *TTSAccumulator) Close() {
	<-a.done
}

// ttsLoop reads sentences from sentCh, synthesizes each one, and streams
// the resulting audio chunks to the WebSocket client.
func (a *TTSAccumulator) ttsLoop() {
	defer close(a.done)

	for sentence := range a.sentCh {
		if a.ctx.Err() != nil {
			continue
		}

		rc, err := a.synth.Synthesize(a.ctx, sentence)
		if err != nil {
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
