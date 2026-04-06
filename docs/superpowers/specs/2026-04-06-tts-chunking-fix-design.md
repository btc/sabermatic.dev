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
- **Cancellation**: Context cancellation still works between sentences.

### Behavioral Differences

- Fewer, larger `tts_chunk` messages (one per sentence instead of many per sentence).
- ~0.5-1s added latency on the first sentence (time for full sentence TTS to complete). Pipeline self-hides after that: sentence N plays while sentence N+1 synthesizes.
- Memory: each sentence's MP3 held briefly (~30-100KB). Negligible.

## Testing

- Existing `observer_test.go` covers `tts_chunk`/`tts_done` flow.
- Verify chunk count matches sentence count (not fragment count).
- Manual verification: play an interview, confirm smooth continuous audio.
