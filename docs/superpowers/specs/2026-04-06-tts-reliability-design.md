# TTS Reliability: Prevent Session Deadlock and Harden Lifecycle

## Problem

A hanging TTS `Synthesize` call can deadlock an entire interview session. The conductor calls `fanOut.Close()` after the LLM stream completes, which blocks on the TTS goroutine's `done` channel. If `Synthesize` hangs (network stall, TTS API timeout, DNS failure), the goroutine never exits, `Close()` blocks forever, the advisory lock is held permanently, and the user cannot reconnect. Only a server restart recovers the session.

Secondary issues in the same code:
- The TTS accumulator directly couples to `WSConn`, marshaling JSON and tracking seq/messageID — protocol concerns that belong in the conductor layer.
- The constructor takes positional arguments (`ctx, ws, synth, messageID`) instead of a params struct.
- The goroutine lifecycle uses a raw `done` channel instead of `sync.WaitGroup`.
- End Session and Cancel Session don't stop TTS playback on the client.

## Scope

Backend changes to `observer/tts_accumulator.go`, `observer/observer.go`, `conductor.go`, and `config/config.go`. Frontend changes to `interview.tsx` (end/cancel mute) and `hooks.ts` (tts_error handler). One new wire message (`tts_error`); `tts_chunk`, `tts_done`, and `seq` are unchanged.

## Design

### 1. Per-sentence timeout prevents unbounded Synthesize calls

Add `TTSSentenceTimeout` to the `Speech` config struct:

```go
// internal/config/config.go
type Speech struct {
    OpenAIAPIKey       string        `env:"OPENAI_API_KEY,required"`
    TTSVoice           string        `env:"TTS_VOICE,default=onyx"`
    TTSModel           string        `env:"TTS_MODEL,default=tts-1"`
    WhisperModel       string        `env:"WHISPER_MODEL,default=whisper-1"`
    TTSSentenceTimeout time.Duration `env:"TTS_SENTENCE_TIMEOUT,default=10s"`
}
```

The TTS goroutine wraps each `Synthesize` call in a per-sentence timeout derived from the accumulator's internal context:

```go
synthCtx, synthCancel := context.WithTimeout(a.ctx, a.sentenceTimeout)
rc, err := a.synth.Synthesize(synthCtx, sentence)
if err != nil {
    a.sink.HandleTTSError()
    synthCancel()
    continue
}
data, err := io.ReadAll(rc)
rc.Close()
synthCancel()
```

The timeout context covers both `Synthesize` and `io.ReadAll` — `synthCancel()` is called after the read completes, not before. This bounds the entire sentence operation. No single sentence can hang indefinitely. All synthesis failures (timeout, HTTP error, auth failure) notify the user via `HandleTTSError()`.

### 2. TTSSink interface decouples accumulator from WebSocket protocol

Define a local interface in the observer package that the accumulator calls. The conductor provides the implementation.

```go
// observer/observer.go
//
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

The accumulator drops `WSConn`, `messageID`, and `seq`. It calls `a.sink.HandleAudio(data)` after each successful synthesis, `a.sink.HandleTTSDone()` when the sentence channel is drained, and `a.sink.HandleTTSError()` when a sentence is dropped or times out.

The conductor owns an unexported `ttsSink` type that implements the interface, handling base64 encoding, seq tracking, messageID, and WS JSON marshaling:

```go
// conductor.go
//
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

A new `ttsSink` is created per turn in `streamInterviewerResponse`, scoping seq and messageID to the turn's lifetime.

### 3. Non-blocking sentence enqueue with error notification

The sentence channel is buffered (cap 32) for normal smoothing. The send is non-blocking — if TTS falls behind and the buffer fills, the sentence is dropped and the user is notified via `HandleTTSError()`:

```go
func (a *TTSAccumulator) OnToken(token string) {
    a.buf.WriteString(token)
    for {
        // ... sentence boundary detection (unchanged) ...
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
```

This guarantees `OnToken` never blocks the fan-out, regardless of TTS API latency. Text streaming continues unimpeded. The user sees a toast acknowledging the audio issue.

`OnDone` uses the same non-blocking pattern for the remaining-buffer flush:

```go
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
```

`close(a.sentCh)` is wrapped in `sync.Once` to prevent a panic if `OnDone` is called after `OnError` (the fan-out calls both in the LLM error path: `OnError` then `OnDone`).

### 4. Constructor takes a params struct

```go
// observer/tts_accumulator.go
type TTSAccumulatorParams struct {
    Sink            TTSSink
    Synth           ai.Synthesizer
    SentenceTimeout time.Duration
}

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
```

The accumulator owns its internal context — no parent context accepted. The lifecycle is managed entirely by `Close()` (graceful) and `Interrupt()` (forced). Per-sentence timeouts bound each operation, so the goroutine always terminates in finite time without external cancellation.

### 5. sync.WaitGroup replaces done channel

Replace the `done chan struct{}` and manual goroutine launch with `sync.WaitGroup` and `wg.Go`:

```go
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
```

`Close()` waits for the goroutine to finish, then cancels the context as cleanup. Safe to call multiple times — both `Wait()` and `cancel()` are idempotent:

```go
func (a *TTSAccumulator) Close() {
    a.wg.Wait()
    a.cancel()
}
```

`Wait()` before `cancel()` is critical: it lets in-flight synthesis complete naturally in the happy path. Per-sentence timeouts (section 1) guarantee `Wait()` is bounded — no single sentence can hang longer than `sentenceTimeout`. Calling `cancel()` first would truncate the last sentence's audio on every turn.

`Interrupt()` cancels without waiting (for `cancel_tts` mid-stream — `Close()` is called later by the fan-out):

```go
func (a *TTSAccumulator) Interrupt() {
    a.cancel()
}
```

In the happy path, `OnDone` closes `sentCh`, the goroutine drains remaining sentences and exits, then `Close()` calls `wg.Wait()` (returns immediately) and `cancel()` (cleanup). In the error/interrupt path, `Interrupt()` or `OnError()` has already called `cancel()`, so the goroutine exits promptly via `<-a.ctx.Done()`, and `Close()`'s `wg.Wait()` returns immediately.

### 6. End Session / Cancel Session mute TTS on the client

Currently clicking End Session or Cancel Session while the interviewer is speaking doesn't stop audio playback. The fix is client-side — call `audioPlayer.cancel()` before sending the WS message:

```typescript
// interview.tsx — Cancel Session button onClick
setCancelDialogOpen(false);
audioPlayer.cancel();
cancelSession();
```

```typescript
// interview.tsx — End Session button onClick
setEndDialogOpen(false);
audioPlayer.cancel();
endSession();
```

We use `audioPlayer.cancel()` directly rather than `stopTts()` (which also sends `cancel_tts` over WS). Sending `cancel_tts` is unnecessary — the session is ending and the server will clean up. It could also race with `end_session` on the wire.

### 7. tts_error handler on the frontend

The `useInterview` hook handles the new `tts_error` message with a toast. The toast is informational, not alarming — audio is supplementary to the text stream:

```typescript
// hooks.ts — inside handleMessage switch
case "tts_error":
    toast.info("Audio temporarily unavailable");
    break;
```

The `toast.info` call is naturally deduplicated by sonner (duplicate messages within a short window are suppressed), so rapid drops or timeouts don't spam the user.

Add the new message type to `web/src/ws/protocol.ts`:

```typescript
  | { type: "tts_error" }
```

### 8. ttsLoop after all changes

For reference, the complete ttsLoop after applying changes 1-5:

```go
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
                // Log timeouts at Warn (the specific failure this spec fixes);
                // other errors (HTTP 500, auth) at Debug.
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

## Call site in conductor

```go
// conductor.go — inside streamInterviewerResponse
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

## What doesn't change

- **Wire protocol**: `tts_chunk`, `tts_done`, `seq` field — all unchanged. One new message type: `tts_error` (no fields).
- **AudioPlayer**: No changes. Each chunk is still a valid MP3 that `decodeAudioData` handles.
- **Sentence boundary detection**: `OnToken` buffering logic is unchanged.
- **OnToken / OnDone / OnError / Interrupt signatures**: Unchanged (part of `TokenObserver` interface).
- **TokenFanOut**: Unchanged. `Close()` on the fan-out still calls `Close()` on each `Closeable` observer.
- **Synthesizer interface**: Unchanged.

## Testing

### Existing tests to update

- `TestTTSAccumulator_SentenceBoundaries` — update constructor call to use params struct, replace `mockWSConn` with a `mockTTSSink`.
- `TestTTSAccumulator_Interrupt` — same constructor update.

### New tests

- **Sentence timeout (blocking Synthesize)**: Use a `blockingSynth` that blocks until context cancellation. Verify `Close()` returns within `sentenceTimeout + margin` rather than hanging.
- **Sentence timeout (blocking ReadAll)**: Use a synth that returns a reader that blocks on `Read()`. Verify the per-sentence timeout covers the full operation, not just the API call.
- **HandleAudio receives raw bytes**: Verify the sink receives unencoded bytes (base64 encoding is the sink's job, not the accumulator's).
- **HandleTTSDone called once**: Verify `HandleTTSDone` is called exactly once after all sentences are processed.
- **HandleTTSDone not called on interrupt**: Verify `HandleTTSDone` is not called when the accumulator is interrupted before completion.
- **HandleTTSDone not called on error**: Verify `HandleTTSDone` is not called when `OnError` is called before `OnDone`.
- **Close is idempotent**: Calling `Close()` twice doesn't panic.
- **Buffer full drops sentence and notifies**: Fill the sentence channel (cap 32), enqueue one more. Verify the sentence is dropped, `HandleTTSError` is called, and `OnToken` returns without blocking.
- **Timeout notifies via HandleTTSError**: Use a `blockingSynth`, verify `HandleTTSError` is called when the sentence timeout fires.
- **Non-timeout Synthesize error notifies via HandleTTSError**: Use a synth that returns an error (e.g., HTTP 500). Verify `HandleTTSError` is called.
- **OnDone after OnError does not double-close sentCh**: Call `OnError` then `OnDone`, verify no panic.

### Manual verification

- Start an interview with TTS enabled, confirm audio plays smoothly.
- Mid-response, click End Session — audio stops immediately.
- Mid-response, click Cancel Session — audio stops immediately.
- Simulate TTS degradation — verify "Audio temporarily unavailable" toast appears.
