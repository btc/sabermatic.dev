# TTS Audio Chunking Fix — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix choppy interviewer audio by sending one complete MP3 per sentence instead of 4096-byte fragments that fail to decode independently.

**Architecture:** Replace the 4096-byte chunked read loop in `tts_accumulator.ttsLoop()` with `io.ReadAll`. Each `tts_chunk` WebSocket message becomes a complete, decodable MP3 file. No client or protocol changes needed.

**Tech Stack:** Go, Web Audio API (unchanged client)

**Spec:** `docs/superpowers/specs/2026-04-06-tts-chunking-fix-design.md`

---

## File Map

- **Modify:** `internal/interview/observer/tts_accumulator.go:103-156` — replace chunked read loop with `io.ReadAll`
- **Modify:** `internal/interview/observer/observer_test.go:146-199` — update `fakeSynth` payload size, tighten assertions, add round-trip verification

---

### Task 1: Write failing test — one chunk per sentence invariant

**Files:**
- Modify: `internal/interview/observer/observer_test.go:146-187`

- [ ] **Step 1: Update `fakeSynth` payload to exceed 4096 bytes**

The current `fakeSynth` returns 16 bytes, which fits in a single 4KB read even before the fix. Change it to 8192 bytes so the pre-fix code produces multiple chunks per sentence.

In `observer_test.go`, replace:

```go
func TestTTSAccumulator_SentenceBoundaries(t *testing.T) {
	ws := &mockWSConn{}
	synth := &fakeSynth{audio: []byte("fake-audio-bytes")}
	ctx := context.Background()
```

With:

```go
func TestTTSAccumulator_SentenceBoundaries(t *testing.T) {
	ws := &mockWSConn{}
	synth := &fakeSynth{audio: bytes.Repeat([]byte("x"), 8192)}
	ctx := context.Background()
```

- [ ] **Step 2: Tighten chunk count assertion to exact equality**

Replace:

```go
	assert.GreaterOrEqual(t, chunks, 2)
```

With:

```go
	assert.Equal(t, 2, chunks, "expected exactly one tts_chunk per sentence")
```

- [ ] **Step 3: Add round-trip integrity verification**

After the chunk count assertion and before the `tts_done` check, add verification that each chunk's base64 data decodes to the original audio payload:

```go
	// Verify each chunk's data round-trips to the original audio.
	for _, msg := range ws.sent {
		var m map[string]any
		require.NoError(t, json.Unmarshal(msg, &m))
		if m["type"] != "tts_chunk" {
			continue
		}
		b64, ok := m["data"].(string)
		require.True(t, ok)
		decoded, err := base64.StdEncoding.DecodeString(b64)
		require.NoError(t, err)
		assert.Equal(t, synth.audio, decoded, "tts_chunk data must match full synthesized audio")
	}
```

This requires adding `"encoding/base64"` to the import block.

- [ ] **Step 4: Run the test — verify it fails**

Run: `go test ./internal/interview/observer/ -run TestTTSAccumulator_SentenceBoundaries -v`

Expected: FAIL — the current 4096-byte chunking produces more than 2 chunks for 8192-byte audio, and individual chunks won't match the full payload.

---

### Task 2: Implement the fix — buffer full sentence audio

**Files:**
- Modify: `internal/interview/observer/tts_accumulator.go:103-156`

- [ ] **Step 1: Replace the chunked read loop with `io.ReadAll`**

In `tts_accumulator.go`, replace the entire read loop inside the `sentence, ok` case (lines 125-150):

```go
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
```

With:

```go
			rc, err := a.synth.Synthesize(a.ctx, sentence)
			if err != nil {
				continue
			}
			data, err := io.ReadAll(rc)
			rc.Close()
			if err != nil || len(data) == 0 {
				continue
			}
			_ = a.ws.SendJSON(a.ctx, map[string]any{
				"type":       "tts_chunk",
				"data":       base64.StdEncoding.EncodeToString(data),
				"message_id": a.messageID.String(),
				"seq":        a.seq,
			})
			a.seq++
```

- [ ] **Step 2: Run the test — verify it passes**

Run: `go test ./internal/interview/observer/ -run TestTTSAccumulator_SentenceBoundaries -v`

Expected: PASS — exactly 2 chunks (one per sentence), each containing the full 8192-byte payload.

- [ ] **Step 3: Run the full observer test suite**

Run: `go test ./internal/interview/observer/ -v`

Expected: All tests pass, including `TestTTSAccumulator_Interrupt`.

- [ ] **Step 4: Commit**

```bash
git add internal/interview/observer/tts_accumulator.go internal/interview/observer/observer_test.go
git commit -m "fix(tts): buffer full sentence audio before sending

Each tts_chunk now contains a complete MP3 file instead of a 4096-byte
fragment. Fixes choppy interviewer audio caused by decodeAudioData
failing on headerless MP3 fragments."
```

---

### Task 3: Manual verification

- [ ] **Step 1: Start the app and run an interview**

Start the dev server, open an interview session, and confirm:
- Interviewer audio plays smoothly without choppiness
- Audio is complete (no missing words or sentence fragments)
- Interrupting TTS (sending a new message mid-audio) still works
- Text streaming is unaffected

- [ ] **Step 2: Check browser console for errors**

Open DevTools console during playback. Confirm no `decodeAudioData` errors appear (previously these were silently caught but would have shown as warnings in some browsers).
