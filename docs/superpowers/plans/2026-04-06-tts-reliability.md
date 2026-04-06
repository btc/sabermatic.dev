# TTS Reliability Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prevent TTS from deadlocking interview sessions by adding per-sentence timeouts, decoupling the accumulator from WS protocol via a TTSSink interface, and notifying users when audio degrades.

**Architecture:** The TTSAccumulator is refactored to own its internal context, use a WaitGroup for goroutine lifecycle, accept a TTSSink interface instead of WSConn, and bound every Synthesize call with a configurable timeout. A new `tts_error` wire message lets the frontend toast when audio is unavailable. End/Cancel Session now mute TTS client-side.

**Tech Stack:** Go 1.25, React/TypeScript, sonner (toasts), coder/websocket

**Spec:** `docs/superpowers/specs/2026-04-06-tts-reliability-design.md`

---

## File Map

| Action | File | Responsibility |
|--------|------|----------------|
| Modify | `internal/config/config.go` | Add `TTSSentenceTimeout` field |
| Modify | `internal/config/config_test.go` | Assert new default |
| Modify | `internal/interview/observer/observer.go` | Add `TTSSink` interface |
| Modify | `internal/interview/observer/tts_accumulator.go` | Full rewrite: params struct, WaitGroup, TTSSink, per-sentence timeout, non-blocking enqueue |
| Modify | `internal/interview/observer/observer_test.go` | Update existing tests + add new test cases |
| Modify | `internal/interview/conductor.go` | Add `ttsSink` type, update call site |
| Modify | `web/src/ws/protocol.ts` | Add `tts_error` to `ServerMessage` |
| Modify | `web/src/ws/hooks.ts` | Handle `tts_error` with toast |
| Modify | `web/src/pages/interview.tsx` | Mute audio on End/Cancel Session |

---

### Task 1: Add TTSSentenceTimeout config field

**Files:**
- Modify: `internal/config/config.go:57-62`
- Modify: `internal/config/config_test.go:11-32`

- [ ] **Step 1: Add the field to Speech struct**

In `internal/config/config.go`, add `TTSSentenceTimeout` to the `Speech` struct:

```go
type Speech struct {
	OpenAIAPIKey       string        `env:"OPENAI_API_KEY,required"`
	TTSVoice           string        `env:"TTS_VOICE,default=onyx"`
	TTSModel           string        `env:"TTS_MODEL,default=tts-1"`
	WhisperModel       string        `env:"WHISPER_MODEL,default=whisper-1"`
	TTSSentenceTimeout time.Duration `env:"TTS_SENTENCE_TIMEOUT,default=10s"`
}
```

- [ ] **Step 2: Add assertion to config defaults test**

In `internal/config/config_test.go`, add this line at the end of `TestLoadConfig_Defaults`:

```go
require.Equal(t, 10*time.Second, cfg.Speech.TTSSentenceTimeout)
```

- [ ] **Step 3: Run config tests**

Run: `go test ./internal/config/ -v -run TestLoadConfig`
Expected: PASS — default loads as 10s

- [ ] **Step 4: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): add TTSSentenceTimeout with 10s default"
```

---

### Task 2: Add TTSSink interface to observer package

**Files:**
- Modify: `internal/interview/observer/observer.go`

- [ ] **Step 1: Add TTSSink interface**

In `internal/interview/observer/observer.go`, add after the `Closeable` interface:

```go
// TTSSink receives synthesized audio from the TTS accumulator.
// Implementations must be safe for concurrent calls — HandleTTSError
// may be called from the conductor goroutine (via OnToken buffer-full)
// while HandleAudio is called from the TTS goroutine.
type TTSSink interface {
	HandleAudio(data []byte)
	HandleTTSDone()
	HandleTTSError()
}
```

- [ ] **Step 2: Verify compilation**

Run: `go build ./internal/interview/observer/`
Expected: compiles with no errors

- [ ] **Step 3: Commit**

```bash
git add internal/interview/observer/observer.go
git commit -m "feat(observer): add TTSSink interface"
```

---

### Task 3: Rewrite TTSAccumulator

This is the core task. Rewrite `tts_accumulator.go` to use TTSSink, params struct, WaitGroup, per-sentence timeout, and non-blocking enqueue.

**Files:**
- Modify: `internal/interview/observer/tts_accumulator.go`

- [ ] **Step 1: Replace the entire file**

Replace `internal/interview/observer/tts_accumulator.go` with:

```go
package observer

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"

	"github.com/btc/drill/internal/ai"
	"time"
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
					slog.Debug("tts: sentence synthesis failed",
						"error", err,
						"sentence_len", len(sentence))
				}
				a.sink.HandleTTSError()
				synthCancel()
				continue
			}
			data, err := io.ReadAll(rc)
			rc.Close()
			synthCancel()
			if err != nil || len(data) == 0 {
				if err != nil {
					slog.Debug("tts: audio read failed",
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
```

- [ ] **Step 2: Verify compilation**

Run: `go build ./internal/interview/observer/`
Expected: FAIL — observer_test.go still uses old constructor. That's expected; we fix tests in Task 4.

Run: `go build ./internal/interview/...`
Expected: FAIL — conductor.go still uses old constructor. That's expected; we fix it in Task 5.

- [ ] **Step 3: Commit (compiles in isolation, tests updated next)**

```bash
git add internal/interview/observer/tts_accumulator.go
git commit -m "refactor(observer): rewrite TTSAccumulator with TTSSink, WaitGroup, per-sentence timeout"
```

---

### Task 4: Update observer tests

**Files:**
- Modify: `internal/interview/observer/observer_test.go`

- [ ] **Step 1: Replace test infrastructure and all TTS tests**

In `observer_test.go`, replace the `fakeSynth`, `blockingSynth`, and all `TestTTSAccumulator_*` functions with the following. Keep everything above `// fakeSynth` unchanged (mockWSConn, spy, fan-out tests, WSWriter tests, MessageAccumulator tests).

Replace from `// fakeSynth returns a fixed audio payload immediately.` (line 147) through end of file with:

```go
// --- TTSSink mock ---

type mockTTSSink struct {
	mu     sync.Mutex
	audio  [][]byte
	done   bool
	errors int
}

func (m *mockTTSSink) HandleAudio(data []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.audio = append(m.audio, append([]byte(nil), data...))
}

func (m *mockTTSSink) HandleTTSDone() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.done = true
}

func (m *mockTTSSink) HandleTTSError() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.errors++
}

// --- Synth mocks ---

// fakeSynth returns a fixed audio payload immediately.
type fakeSynth struct {
	audio []byte
}

func (f *fakeSynth) Synthesize(_ context.Context, _ string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(f.audio)), nil
}

// blockingSynth blocks until the context is cancelled.
type blockingSynth struct{}

func (b *blockingSynth) Synthesize(ctx context.Context, _ string) (io.ReadCloser, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// errorSynth always returns an error.
type errorSynth struct{}

func (e *errorSynth) Synthesize(_ context.Context, _ string) (io.ReadCloser, error) {
	return nil, fmt.Errorf("synth unavailable")
}

// blockingReaderSynth returns a reader that blocks on Read until context is cancelled.
type blockingReaderSynth struct{}

func (b *blockingReaderSynth) Synthesize(ctx context.Context, _ string) (io.ReadCloser, error) {
	return io.NopCloser(readerFunc(func(p []byte) (int, error) {
		<-ctx.Done()
		return 0, ctx.Err()
	})), nil
}

type readerFunc func(p []byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

// --- Helper ---

func newTestAccumulator(sink *mockTTSSink, synth ai.Synthesizer) *observer.TTSAccumulator {
	return observer.NewTTSAccumulator(observer.TTSAccumulatorParams{
		Sink:            sink,
		Synth:           synth,
		SentenceTimeout: 500 * time.Millisecond,
	})
}

// --- Tests ---

func TestTTSAccumulator_SentenceBoundaries(t *testing.T) {
	sink := &mockTTSSink{}
	audio := bytes.Repeat([]byte("x"), 8192)
	acc := newTestAccumulator(sink, &fakeSynth{audio: audio})

	acc.OnToken("Hello there. ")
	acc.OnToken("How are you? ")
	acc.OnDone("Hello there. How are you? ")
	acc.Close()

	sink.mu.Lock()
	defer sink.mu.Unlock()
	assert.Len(t, sink.audio, 2, "expected one audio per sentence")
	for _, data := range sink.audio {
		assert.Equal(t, audio, data, "audio must be raw bytes, not base64")
	}
	assert.True(t, sink.done, "HandleTTSDone must be called")
	assert.Equal(t, 0, sink.errors, "no errors expected")
}

func TestTTSAccumulator_Interrupt(t *testing.T) {
	sink := &mockTTSSink{}
	acc := newTestAccumulator(sink, &blockingSynth{})

	acc.OnToken("First sentence. Second sentence. ")
	acc.Interrupt()
	acc.OnDone("First sentence. Second sentence. ")
	acc.Close() // must not hang

	sink.mu.Lock()
	defer sink.mu.Unlock()
	assert.False(t, sink.done, "HandleTTSDone must not be called after interrupt")
}

func TestTTSAccumulator_SentenceTimeout(t *testing.T) {
	sink := &mockTTSSink{}
	acc := observer.NewTTSAccumulator(observer.TTSAccumulatorParams{
		Sink:            sink,
		Synth:           &blockingSynth{},
		SentenceTimeout: 50 * time.Millisecond,
	})

	acc.OnToken("Slow sentence. ")
	acc.OnDone("Slow sentence. ")

	start := time.Now()
	acc.Close()
	elapsed := time.Since(start)

	assert.Less(t, elapsed, 500*time.Millisecond, "Close must return within timeout, not hang")
	sink.mu.Lock()
	defer sink.mu.Unlock()
	assert.Equal(t, 1, sink.errors, "HandleTTSError must be called on timeout")
	assert.True(t, sink.done, "HandleTTSDone must still be called after timeout")
}

func TestTTSAccumulator_BlockingReadAllTimeout(t *testing.T) {
	sink := &mockTTSSink{}
	acc := observer.NewTTSAccumulator(observer.TTSAccumulatorParams{
		Sink:            sink,
		Synth:           &blockingReaderSynth{},
		SentenceTimeout: 50 * time.Millisecond,
	})

	acc.OnToken("Stuck read. ")
	acc.OnDone("Stuck read. ")

	start := time.Now()
	acc.Close()
	elapsed := time.Since(start)

	assert.Less(t, elapsed, 500*time.Millisecond, "Close must return within timeout, not hang")
	sink.mu.Lock()
	defer sink.mu.Unlock()
	assert.GreaterOrEqual(t, sink.errors, 1, "HandleTTSError must be called on read timeout")
}

func TestTTSAccumulator_HandleTTSDoneCalledOnce(t *testing.T) {
	sink := &mockTTSSink{}
	acc := newTestAccumulator(sink, &fakeSynth{audio: []byte("x")})

	acc.OnToken("One. Two. Three. ")
	acc.OnDone("One. Two. Three. ")
	acc.Close()

	sink.mu.Lock()
	defer sink.mu.Unlock()
	assert.True(t, sink.done)
	assert.Len(t, sink.audio, 3)
}

func TestTTSAccumulator_HandleTTSDoneNotCalledOnError(t *testing.T) {
	sink := &mockTTSSink{}
	acc := newTestAccumulator(sink, &fakeSynth{audio: []byte("x")})

	acc.OnError(fmt.Errorf("llm failed"))
	acc.OnDone("partial text")
	acc.Close()

	sink.mu.Lock()
	defer sink.mu.Unlock()
	assert.False(t, sink.done, "HandleTTSDone must not be called after OnError")
}

func TestTTSAccumulator_CloseIdempotent(t *testing.T) {
	sink := &mockTTSSink{}
	acc := newTestAccumulator(sink, &fakeSynth{audio: []byte("x")})

	acc.OnDone("")
	acc.Close()
	acc.Close() // must not panic
}

func TestTTSAccumulator_SynthErrorNotifiesUser(t *testing.T) {
	sink := &mockTTSSink{}
	acc := newTestAccumulator(sink, &errorSynth{})

	acc.OnToken("Will fail. ")
	acc.OnDone("Will fail. ")
	acc.Close()

	sink.mu.Lock()
	defer sink.mu.Unlock()
	assert.Equal(t, 1, sink.errors, "HandleTTSError must be called on synth error")
	assert.Len(t, sink.audio, 0, "no audio when synth fails")
	assert.True(t, sink.done)
}

func TestTTSAccumulator_OnDoneAfterOnErrorNoPanic(t *testing.T) {
	sink := &mockTTSSink{}
	acc := newTestAccumulator(sink, &fakeSynth{audio: []byte("x")})

	acc.OnError(fmt.Errorf("boom"))
	acc.OnDone("text")
	acc.Close() // must not panic from double close(sentCh)
}
```

Add `"sync"` and `"time"` to the imports at the top of the file if not already present.

- [ ] **Step 2: Run all observer tests**

Run: `go test ./internal/interview/observer/ -v -count=1`
Expected: ALL PASS

- [ ] **Step 3: Run with race detector**

Run: `go test ./internal/interview/observer/ -race -count=1`
Expected: No races detected

- [ ] **Step 4: Commit**

```bash
git add internal/interview/observer/observer_test.go
git commit -m "test(observer): update TTS tests for TTSSink, add timeout and error notification tests"
```

---

### Task 5: Add ttsSink to conductor and update call site

**Files:**
- Modify: `internal/interview/conductor.go:1-25` (imports), `447-452` (call site)

- [ ] **Step 1: Add `encoding/base64` to conductor imports**

In `internal/interview/conductor.go`, add `"encoding/base64"` to the import block.

- [ ] **Step 2: Add ttsSink type**

Add the following after the `Conductor` struct definition (after line 84):

```go
// ttsSink is created per turn, so seq and messageID are scoped to one
// interviewer response. HandleTTSError may be called concurrently from
// the conductor goroutine (OnToken buffer-full) and the TTS goroutine;
// SendJSON is thread-safe, so no additional synchronization is needed.
// seq is only incremented by HandleAudio (TTS goroutine), never by
// HandleTTSError, so there is no data race on the counter.
type ttsSink struct {
	ws        observer.WSConn
	messageID uuid.UUID
	seq       int
}

func (s *ttsSink) HandleAudio(data []byte) {
	_ = s.ws.SendJSON(context.Background(), map[string]any{
		"type":       "tts_chunk",
		"data":       base64.StdEncoding.EncodeToString(data),
		"message_id": s.messageID.String(),
		"seq":        s.seq,
	})
	s.seq++
}

func (s *ttsSink) HandleTTSDone() {
	_ = s.ws.SendJSON(context.Background(), map[string]any{
		"type":       "tts_done",
		"message_id": s.messageID.String(),
	})
}

func (s *ttsSink) HandleTTSError() {
	_ = s.ws.SendJSON(context.Background(), map[string]any{
		"type": "tts_error",
	})
}
```

- [ ] **Step 3: Update the call site in streamInterviewerResponse**

Replace the TTS block (lines 447-452):

```go
	if c.ttsEnabled {
		synth, err := c.backend.Synthesizer()
		if err == nil && synth != nil {
			observers = append(observers, observer.NewTTSAccumulator(ctx, c.ws, synth, messageID))
		}
	}
```

With:

```go
	if c.ttsEnabled {
		synth, err := c.backend.Synthesizer()
		if err == nil && synth != nil {
			sink := &ttsSink{ws: c.ws, messageID: messageID}
			observers = append(observers, observer.NewTTSAccumulator(observer.TTSAccumulatorParams{
				Sink:            sink,
				Synth:           synth,
				SentenceTimeout: c.backend.Config().Speech.TTSSentenceTimeout,
			}))
		}
	}
```

- [ ] **Step 4: Verify full build**

Run: `go build ./...`
Expected: compiles with no errors

- [ ] **Step 5: Run all tests**

Run: `go test ./internal/interview/... -v -count=1`
Expected: ALL PASS

- [ ] **Step 6: Commit**

```bash
git add internal/interview/conductor.go
git commit -m "feat(interview): add ttsSink, wire TTSAccumulator with per-sentence timeout from config"
```

---

### Task 6: Add tts_error to frontend protocol and hook

**Files:**
- Modify: `web/src/ws/protocol.ts:19-33`
- Modify: `web/src/ws/hooks.ts:1,129-137`

- [ ] **Step 1: Add tts_error to ServerMessage type**

In `web/src/ws/protocol.ts`, add this line after the `tts_done` entry (line 27):

```typescript
  | { type: "tts_error" }
```

- [ ] **Step 2: Add toast import to hooks.ts**

In `web/src/ws/hooks.ts`, add to the imports at the top:

```typescript
import { toast } from "sonner";
```

- [ ] **Step 3: Add tts_error handler in handleMessage switch**

In `web/src/ws/hooks.ts`, add this case before the `case "error":` line (before line 129):

```typescript
      case "tts_error":
        toast.info("Audio temporarily unavailable");
        break;
```

- [ ] **Step 4: Run frontend type check**

Run: `cd web && npx tsc --noEmit`
Expected: no type errors

- [ ] **Step 5: Commit**

```bash
git add web/src/ws/protocol.ts web/src/ws/hooks.ts
git commit -m "feat(frontend): handle tts_error with informational toast"
```

---

### Task 7: Mute TTS on End/Cancel Session

**Files:**
- Modify: `web/src/pages/interview.tsx:493-498,519-523`

- [ ] **Step 1: Add audioPlayer.cancel() to Cancel Session button**

In `web/src/pages/interview.tsx`, replace the Cancel Session button onClick (lines 495-498):

```typescript
              onClick={() => {
                setCancelDialogOpen(false);
                cancelSession();
              }}
```

With:

```typescript
              onClick={() => {
                setCancelDialogOpen(false);
                audioPlayer.cancel();
                cancelSession();
              }}
```

- [ ] **Step 2: Add audioPlayer.cancel() to End Session button**

Replace the End Session button onClick (lines 520-523):

```typescript
              onClick={() => {
                setEndDialogOpen(false);
                endSession();
              }}
```

With:

```typescript
              onClick={() => {
                setEndDialogOpen(false);
                audioPlayer.cancel();
                endSession();
              }}
```

- [ ] **Step 3: Run frontend type check**

Run: `cd web && npx tsc --noEmit`
Expected: no type errors

- [ ] **Step 4: Commit**

```bash
git add web/src/pages/interview.tsx
git commit -m "fix(interview): mute TTS playback on End/Cancel Session"
```

---

### Task 8: Manual verification

- [ ] **Step 1: Start the app**

Run: `make dev` (or however the app starts locally)

- [ ] **Step 2: Verify TTS plays smoothly**

Start an interview with TTS enabled. Confirm audio plays continuously through a full interviewer response.

- [ ] **Step 3: Verify End Session mutes TTS**

During an interviewer response with audio playing, click End Session → confirm. Audio should stop immediately.

- [ ] **Step 4: Verify Cancel Session mutes TTS**

Start another session. During an interviewer response, click Cancel → Cancel Session. Audio should stop immediately.

- [ ] **Step 5: Run full test suite**

Run: `go test ./... -race -count=1`
Expected: ALL PASS, no races
