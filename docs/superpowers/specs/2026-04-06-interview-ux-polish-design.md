# Interview UX Polish

Restore v0 UX patterns lost in the Go rewrite and extract the growing input area into its own component.

## Features

### 1. Waveform Visualization

The text input transforms into a waveform display during recording and buffered states.

**Recording state (active):**
- Text input replaced by a red-bordered container with live waveform bars, rec dot, and elapsed timer.
- 24 bars driven by `AnalyserNode.getByteTimeDomainData()` via `requestAnimationFrame`.
- Bar height: `max(2, ((value - 128) / 128) * maxHeight)` where maxHeight fits the input height (~20px).
- Elapsed timer (e.g., "0:03") right-aligned inside the container, monospace, red.

**Pending state (buffered audio):**
- Waveform bars freeze in muted gray — a static snapshot of the last analyser frame before recording stopped.
- Total duration shown right-aligned (e.g., "5.3s"), monospace, gray.
- If the user records another segment (Space again), the waveform goes active again. On stop, duration accumulates and snapshot updates.

**Implementation:**
- Add `AnalyserNode` setup to `AudioRecorder`. The recorder already acquires a persistent `MediaStream`. Create an `AudioContext` + `MediaStreamSource` + `AnalyserNode` once on first `start()` call, reuse on subsequent calls (the stream is already persistent). Expose `analyserNode` getter. This AudioContext is for recording analysis only — completely separate from the TTS playback `AudioPlayer` context.
- `useAudioRecorder` hook exposes `analyserNode: AnalyserNode | null`.
- Duration tracked via `performance.now()` delta: record start time in `start()`, compute elapsed in `stop()`, accumulate across segments. Expose `pendingDuration: number` (seconds) and `elapsedTime: number` (seconds, updated via state during recording).

### 2. Keyboard Hints

Contextual hint text below the input area, changing with state:

| State | Hint |
|-------|------|
| Idle (input not focused) | Hold `Space` to talk |
| Idle (input focused) | *(no hint — standard text input)* |
| Recording | Recording... release `Space` to stop |
| Pending | `Enter` submit · `Space` add more · `Delete` discard |
| Disabled (streaming/ended/reconnecting) | *(no hint)* |

Rendered as small muted text centered below the input row. Uses `<kbd>` elements for key names.

**New keyboard binding:** `Delete` (or `Backspace`) key discards all buffered segments when input is not focused and segments exist. Added as a global keydown listener alongside the existing Enter and Space listeners.

### 3. Question Title in Header

Current header: `[← Cancel] [timer] [End Session]`

New header: `[← Cancel] [Question Title] [timer] [End Session]`

- Title centered via flex, truncated with `text-overflow: ellipsis` and `max-width`.
- Timer and TTS indicator move right, grouped with End Session in a flex container.
- Title sourced from `sessionInfo?.question.title` (already available from `useInterview`).

### 4. Recording Duration

Built into the waveform states above:
- **During recording:** elapsed seconds, updated every animation frame, shown as `M:SS` (e.g., "0:03").
- **When pending:** total accumulated duration across all segments, shown as `N.Ns` (e.g., "5.3s").

## Component Architecture

### `RecordingInput` (new component)

Extracted from `interview.tsx`. Owns the entire input row + hint text.

```
web/src/components/recording-input.tsx
```

**Props:**
```typescript
interface RecordingInputProps {
  // Audio state
  isRecording: boolean;
  segmentCount: number;
  pendingDuration: number;
  analyserNode: AnalyserNode | null;

  // Text state
  textInput: string;
  onTextChange: (value: string) => void;
  inputFocused: boolean;
  onFocusChange: (focused: boolean) => void;

  // Actions
  onSend: () => void;
  onStartRecording: () => void;
  onStopRecording: () => void;
  onDiscard: () => void;

  // Flags
  disabled: boolean;
}
```

**Internal state:**
- `elapsedTime`: updated via `requestAnimationFrame` during recording.
- `frozenWaveform`: snapshot of last analyser data, captured on recording stop.
- `inputRef`: forwarded ref for the text input element.

**Rendering logic:**
- If `isRecording`: mic button (red), waveform (active) + elapsed timer, send button (disabled). Hint: "Recording..."
- Else if `segmentCount > 0`: mic button, delete button, waveform (passive/frozen) + duration, send button (active). Hint: shortcuts.
- Else: mic button, text input, send button. Hint: "Hold Space to talk" (when not focused).

### `Waveform` (new component)

```
web/src/components/waveform.tsx
```

**Props:**
```typescript
interface WaveformProps {
  data: Uint8Array;         // 24+ values from analyser or frozen snapshot
  variant: "active" | "passive";  // red animated vs gray static
  className?: string;
}
```

Renders 24 `<div>` bars. Height computed from byte values. Color from variant. No animation logic — just renders data it's given. The parent (`RecordingInput`) drives updates via `requestAnimationFrame`.

### Changes to existing files

**`web/src/audio/recorder.ts`:**
- Add `AudioContext`, `MediaStreamSource`, `AnalyserNode` setup in `start()`.
- Expose `get analyserNode(): AnalyserNode | null`.
- Track recording start time via `performance.now()`.
- New `get recordingElapsed(): number` (seconds since start, 0 when not recording).
- Accumulate duration across segments. Expose `get totalDuration(): number`.
- Reset duration in `submit()` and `discard()`.
- AudioContext for analyser is separate from the playback AudioContext — recording analysis doesn't conflict with TTS.

**`web/src/audio/hooks.ts` (`useAudioRecorder`):**
- Expose `analyserNode`, `pendingDuration` from recorder.

**`web/src/pages/interview.tsx`:**
- Replace the input area (mic button, segment indicator, text input, send button) with `<RecordingInput>`.
- Move all input-related keyboard listeners (Space, Enter, Escape, Delete) into `RecordingInput`.
- Restructure header: title centered, timer+TTS grouped right.
- Keep `ensureAudioContext` in `interview.tsx` for the TTS `AudioPlayer` — call it inside the `onStartRecording` and `onSend` callbacks passed to `RecordingInput`. The recording AnalyserNode has its own AudioContext managed by `AudioRecorder`.

## Keyboard Bindings Summary

All global (document-level) when text input is NOT focused and not disabled:

| Key | Condition | Action |
|-----|-----------|--------|
| Space (hold) | Not recording | Start recording, stop TTS |
| Space (release) | Recording | Stop recording |
| Enter | Segments pending | Submit audio |
| Enter | Text entered | Submit text |
| Delete / Backspace | Segments pending | Discard segments |
| Escape | Input focused | Blur input |

When text input IS focused: standard text editing. Space types a space. Enter submits text (existing behavior).

## Non-goals

- Waveform playback preview (play back recorded audio before submitting)
- Recording segment list/management (user doesn't see individual segments)
- Mic permission flow (existing lazy-init pattern is sufficient)
- Changes to backend or WebSocket protocol
