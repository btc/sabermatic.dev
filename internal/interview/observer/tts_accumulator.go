package observer

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/btc/drill/internal/ai"
)

// TTSAccumulatorParams contains everything needed to construct a TTSAccumulator.
type TTSAccumulatorParams struct {
	Sink            TTSSink
	Synth           ai.Synthesizer
	SentenceTimeout time.Duration
}

// TTSAccumulator buffers tokens, detects sentence boundaries, and fires TTS
// synthesis for each sentence in a dedicated goroutine.
type TTSAccumulator struct {
	sink            TTSSink
	synth           ai.Synthesizer
	sentenceTimeout time.Duration
	ctx             context.Context
	cancel          context.CancelFunc
	sentCh          chan string
	wg              sync.WaitGroup
	closeOnce       sync.Once
	buf             strings.Builder
}

// NewTTSAccumulator constructs a TTSAccumulator and starts the TTS goroutine.
// The accumulator owns its internal context — no parent context accepted.
// The lifecycle is managed entirely by Close() (graceful) and Interrupt()
// (forced). Per-sentence timeouts bound each operation, so the goroutine
// always terminates in finite time without external cancellation.
func NewTTSAccumulator(p TTSAccumulatorParams) *TTSAccumulator {
	ctx, cancel := context.WithCancel(context.Background())
	acc := &TTSAccumulator{
		sink:            p.Sink,
		synth:           p.Synth,
		sentenceTimeout: p.SentenceTimeout,
		ctx:             ctx,
		cancel:          cancel,
		sentCh:          make(chan string, 32),
	}
	acc.wg.Go(acc.ttsLoop)
	return acc
}

// sentenceBoundaries are the two-character suffixes that end a sentence.
var sentenceBoundaries = []string{". ", "? ", "! ", ".\n", "?\n", "!\n"}

// OnToken appends the token to the buffer and flushes any complete sentences.
// The send to sentCh is non-blocking — if the buffer is full, the sentence
// is dropped and HandleTTSError is called to notify the user.
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
		default:
			slog.Warn("tts: sentence dropped, buffer full",
				"buffer_cap", cap(a.sentCh),
				"sentence_len", len(sentence))
			a.sink.HandleTTSError()
		}
	}
}

// OnDone flushes remaining buffer and closes sentCh.
func (a *TTSAccumulator) OnDone(_ string) {
	if remaining := strings.TrimSpace(a.buf.String()); remaining != "" {
		select {
		case a.sentCh <- remaining:
		case <-a.ctx.Done():
		default:
			slog.Warn("tts: final sentence dropped, buffer full",
				"sentence_len", len(remaining))
			a.sink.HandleTTSError()
		}
	}
	a.closeOnce.Do(func() { close(a.sentCh) })
}

// OnError cancels the context to abort in-flight TTS.
func (a *TTSAccumulator) OnError(_ error) {
	a.cancel()
}

// Interrupt cancels the context (called by cancel_tts client message).
func (a *TTSAccumulator) Interrupt() {
	a.cancel()
}

// Close waits for the TTS goroutine to finish, then cancels the context
// as cleanup. Safe to call multiple times — both Wait() and cancel() are
// idempotent.
//
// Wait() before cancel() is critical: it lets in-flight synthesis complete
// naturally in the happy path. Per-sentence timeouts guarantee Wait() is
// bounded. Calling cancel() first would truncate the last sentence's audio.
func (a *TTSAccumulator) Close() {
	a.wg.Wait()
	a.cancel()
}

// ttsLoop reads sentences from sentCh, synthesizes each one, and sends
// the resulting audio to the sink.
func (a *TTSAccumulator) ttsLoop() {
	for {
		select {
		case sentence, ok := <-a.sentCh:
			if !ok {
				// sentCh closed by OnDone. Send HandleTTSDone only if we
				// weren't interrupted — on interrupt/error the client
				// won't receive tts_done (existing behavior, not a regression).
				if a.ctx.Err() == nil {
					a.sink.HandleTTSDone()
				}
				return
			}
			if a.ctx.Err() != nil {
				continue
			}

			synthCtx, synthCancel := context.WithTimeout(a.ctx, a.sentenceTimeout)
			rc, err := a.synth.Synthesize(synthCtx, sentence)
			if err != nil {
				if synthCtx.Err() == context.DeadlineExceeded {
					slog.Warn("tts: sentence synthesis timed out",
						"timeout", a.sentenceTimeout,
						"sentence_len", len(sentence))
				} else {
					slog.Warn("tts: sentence synthesis failed",
						"error", err,
						"sentence_len", len(sentence))
				}
				a.sink.HandleTTSError()
				synthCancel()
				continue
			}
			// synthCancel after ReadAll: the timeout covers both the API
			// call and reading the response body (which streams from the
			// remote TTS service). Cancelling before ReadAll would kill
			// the response reader since it's tied to the request context.
			data, err := io.ReadAll(rc)
			rc.Close()
			synthCancel()
			if err != nil || len(data) == 0 {
				if err != nil {
					slog.Warn("tts: audio read failed",
						"error", err,
						"sentence_len", len(sentence))
				}
				a.sink.HandleTTSError()
				continue
			}
			a.sink.HandleAudio(data)

		case <-a.ctx.Done():
			return
		}
	}
}
