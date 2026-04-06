# TTS Audio Chunking Fix

## Problem

Interviewer audio plays back in choppy fragments during interviews. Only partial audio is heard.

### Root Cause

`tts_accumulator.go` reads each sentence's TTS response in 4096-byte chunks and sends each as an independent `tts_chunk` WebSocket message. The client's `AudioPlayer` calls `decodeAudioData()` on each chunk independently. `decodeAudioData` requires a valid MP3 file (header + complete frames). Only the first chunk of each sentence contains the MP3 header — subsequent chunks fail to decode and are silently skipped by the error callback.

## Fix

Buffer each sentence's full TTS audio server-side before sending. Replace the 4096-byte read loop in `tts_accumulator.ttsLoop()` with `io.ReadAll`.

### Backend Change (`internal/interview/observer/tts_accumulator.go`)

In `ttsLoop()`, replace the 4096-byte chunked read loop:

```go
// Before: sends multiple 4KB fragments per sentence
buf := make([]byte, 4096)
for {
    n, readErr := rc.Read(buf)
    if n > 0 {
        chunk := base64.StdEncoding.EncodeToString(buf[:n])
        _ = a.ws.SendJSON(a.ctx, map[string]any{...})
        a.seq++
    }
    // ...
}
```

With a single full read:

```go
// After: sends one complete MP3 per sentence
data, err := io.ReadAll(rc)
rc.Close()
if err != nil || len(data) == 0 {
    continue
}
chunk := base64.StdEncoding.EncodeToString(data)
_ = a.ws.SendJSON(a.ctx, map[string]any{
    "type":       "tts_chunk",
    "data":       chunk,
    "message_id": a.messageID.String(),
    "seq":        a.seq,
})
a.seq++
```

### What Doesn't Change

- **Protocol**: `tts_chunk`, `tts_done`, `seq` field — all unchanged.
- **Client `AudioPlayer`**: No changes. Each chunk is now a valid MP3 file that `decodeAudioData` handles correctly.
- **`Synthesizer` interface**: Unchanged.
- **Sentence boundary detection**: Unchanged.
- **Cancellation**: Context cancellation still works between sentences. Note: mid-sentence cancellation during `io.ReadAll` relies on Go's `http.Transport` propagating context cancellation to the response body, which closes the underlying connection. This is standard Go behavior — the current code has finer-grained cancellation (between 4KB reads via `select` on `a.ctx.Done()`), but in practice the HTTP transport tears down the body promptly.
- **Sentence boundary detection**: Unchanged. Known limitation: if the LLM produces a long passage without `. `/`? `/`! ` boundaries, the entire passage becomes one sentence. This is pre-existing but slightly more latency-sensitive with buffering since the full MP3 must complete before playback starts.

### Behavioral Differences

- Fewer, larger `tts_chunk` messages (one per sentence instead of many per sentence).
- ~200-500ms added latency on the first sentence (time for OpenAI to stream the full sentence response). Pipeline self-hides after that: sentence N plays while sentence N+1 synthesizes.
- Memory: each sentence's MP3 held briefly (~30-100KB raw, ~40-133KB on the wire after base64 encoding). Well within the 10MB WebSocket read limit.

## Testing

Use red-green TDD: write failing tests first, then implement the fix.

- **Tighten existing assertion**: Change `assert.GreaterOrEqual(t, chunks, 2)` to `assert.Equal(t, 2, chunks)` — this makes the test a precise regression guard for the one-chunk-per-sentence invariant.
- **Use realistic payload size**: The current `fakeSynth` returns 16 bytes (`"fake-audio-bytes"`), which fits in a single 4KB read even before the fix. Use a payload larger than 4096 bytes (e.g., `bytes.Repeat([]byte("x"), 8192)`) so the test actually differentiates pre-fix (multiple chunks per sentence) from post-fix (one chunk per sentence) behavior.
- **Verify round-trip integrity**: Decode the base64 `data` field from each `tts_chunk` message and compare to the `fakeSynth` payload to confirm the complete MP3 is sent.
- Manual verification: play an interview, confirm smooth continuous audio.
