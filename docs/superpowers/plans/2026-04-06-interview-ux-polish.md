# Interview UX Polish Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restore v0 UX patterns (waveform, keyboard hints, question title, recording duration) and extract the input area into a dedicated component.

**Architecture:** Add AnalyserNode to AudioRecorder for waveform data + duration tracking. Extract input area from interview.tsx into RecordingInput component with Waveform sub-component. Restructure interview header to show question title.

**Tech Stack:** React, TypeScript, Web Audio API (AnalyserNode), Tailwind CSS, Lucide icons

**Spec:** `docs/superpowers/specs/2026-04-06-interview-ux-polish-design.md`

---

## File Map

| File | Action | Responsibility |
|------|--------|----------------|
| `web/src/audio/recorder.ts` | Modify | Add AnalyserNode, duration tracking |
| `web/src/audio/hooks.ts` | Modify | Expose analyserNode, pendingDuration |
| `web/src/components/waveform.tsx` | Create | Pure rendering of 24 waveform bars |
| `web/src/components/recording-input.tsx` | Create | Input row + keyboard listeners + hint text |
| `web/src/pages/interview.tsx` | Modify | Use RecordingInput, restructure header |

---

### Task 1: Add AnalyserNode and duration tracking to AudioRecorder

**Files:**
- Modify: `web/src/audio/recorder.ts`
- Modify: `web/src/audio/hooks.ts`
- Test: `web/src/audio/__tests__/recorder.test.ts`

- [ ] **Step 1: Add duration tracking tests**

Open `web/src/audio/__tests__/recorder.test.ts` and add these tests after the existing ones:

```typescript
it("tracks duration across segments", async () => {
  const recorder = new AudioRecorder();
  await recorder.start();

  // Simulate 100ms of recording
  await new Promise((r) => setTimeout(r, 100));
  await recorder.stop();

  expect(recorder.totalDuration).toBeGreaterThan(0);
  expect(recorder.totalDuration).toBeLessThan(1);

  // Second segment
  await recorder.start();
  await new Promise((r) => setTimeout(r, 100));
  await recorder.stop();

  // Duration accumulates
  expect(recorder.totalDuration).toBeGreaterThan(0.1);
});

it("resets duration on submit", async () => {
  const recorder = new AudioRecorder();
  await recorder.start();
  await new Promise((r) => setTimeout(r, 50));
  await recorder.stop();
  expect(recorder.totalDuration).toBeGreaterThan(0);

  await recorder.submit();
  expect(recorder.totalDuration).toBe(0);
});

it("resets duration on discard", async () => {
  const recorder = new AudioRecorder();
  await recorder.start();
  await new Promise((r) => setTimeout(r, 50));
  await recorder.stop();

  recorder.discard();
  expect(recorder.totalDuration).toBe(0);
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd web && npx vitest run src/audio/__tests__/recorder.test.ts`
Expected: FAIL — `totalDuration` is not a property.

- [ ] **Step 3: Add AnalyserNode and duration fields to AudioRecorder**

In `web/src/audio/recorder.ts`, add fields after the existing private fields:

```typescript
// After: private _isRecording = false;
private audioCtx: AudioContext | null = null;
private analyser: AnalyserNode | null = null;
private _totalDuration = 0;
private recordingStartTime = 0;
```

Add getters after the existing `get segmentCount()`:

```typescript
get analyserNode(): AnalyserNode | null {
  return this.analyser;
}

get totalDuration(): number {
  return this._totalDuration;
}
```

- [ ] **Step 4: Set up AnalyserNode in start(), track duration in stop()**

In `start()`, after `this.mediaRecorder = new MediaRecorder(this.stream, options);`, add:

```typescript
// Set up AnalyserNode on first start (reuse persistent stream)
if (!this.audioCtx) {
  this.audioCtx = new AudioContext();
  const source = this.audioCtx.createMediaStreamSource(this.stream);
  this.analyser = this.audioCtx.createAnalyser();
  this.analyser.fftSize = 256;
  source.connect(this.analyser);
}
this.recordingStartTime = performance.now();
```

In `stop()`, replace the entire `recorder.onstop` assignment:

```typescript
recorder.onstop = (e) => {
  if (typeof prev === "function") prev.call(recorder, e);
  this._totalDuration += (performance.now() - this.recordingStartTime) / 1000;
  resolve();
};
```

(The blob push already happens in the original `onstop` set during `start()`. The chained `onstop` in `stop()` handles duration + resolve.)

- [ ] **Step 5: Reset duration in submit() and discard()**

In `submit()`, after `this.segments = [];`, add:

```typescript
this._totalDuration = 0;
```

In `discard()`, after `this.segments = [];`, add:

```typescript
this._totalDuration = 0;
```

- [ ] **Step 6: Clean up AnalyserNode AudioContext in destroy()**

In `destroy()`, after the existing cleanup, add:

```typescript
if (this.audioCtx) {
  this.audioCtx.close().catch(() => {});
  this.audioCtx = null;
  this.analyser = null;
}
```

- [ ] **Step 7: Run tests**

Run: `cd web && npx vitest run src/audio/__tests__/recorder.test.ts`
Expected: All tests PASS.

- [ ] **Step 8: Update useAudioRecorder hook to expose new fields**

In `web/src/audio/hooks.ts`, update the `useAudioRecorder` return and add state for `pendingDuration`:

Add state:
```typescript
const [pendingDuration, setPendingDuration] = useState(0);
const [analyserNode, setAnalyserNode] = useState<AnalyserNode | null>(null);
```

Update the `start` callback to expose the analyserNode after first creation:
```typescript
const start = useCallback(async () => {
  await recorderRef.current?.start();
  setIsRecording(true);
  // AnalyserNode is created on first start() — capture it in state for reactivity
  if (!analyserNode && recorderRef.current?.analyserNode) {
    setAnalyserNode(recorderRef.current.analyserNode);
  }
}, [analyserNode]);
```

Update the `stop` callback to also update duration:
```typescript
const stop = useCallback(async () => {
  await recorderRef.current?.stop();
  setIsRecording(false);
  setSegmentCount(recorderRef.current?.segmentCount ?? 0);
  setPendingDuration(recorderRef.current?.totalDuration ?? 0);
}, []);
```

Update `submit` to reset duration:
```typescript
const submit = useCallback(async () => {
  const data = await recorderRef.current?.submit();
  setSegmentCount(0);
  setPendingDuration(0);
  return data ?? "";
}, []);
```

Update `discard` to reset duration:
```typescript
const discard = useCallback(() => {
  recorderRef.current?.discard();
  setSegmentCount(0);
  setPendingDuration(0);
}, []);
```

Update return:
```typescript
return { isRecording, segmentCount, pendingDuration, analyserNode, start, stop, submit, discard };
```

- [ ] **Step 9: Type-check**

Run: `cd web && npx tsc --noEmit`
Expected: Clean.

- [ ] **Step 10: Commit**

```bash
git add web/src/audio/recorder.ts web/src/audio/hooks.ts web/src/audio/__tests__/recorder.test.ts
git commit -m "feat(audio): add AnalyserNode and duration tracking to AudioRecorder"
```

---

### Task 2: Create Waveform component

**Files:**
- Create: `web/src/components/waveform.tsx`

- [ ] **Step 1: Create the Waveform component**

Create `web/src/components/waveform.tsx`:

```tsx
import { cn } from "@/lib/utils";

const BAR_COUNT = 24;

interface WaveformProps {
  data: Uint8Array;
  variant: "active" | "passive";
  className?: string;
}

export function Waveform({ data, variant, className }: WaveformProps) {
  const isActive = variant === "active";
  const barColor = isActive ? "bg-red-500" : "bg-muted-foreground";

  return (
    <div className={cn("flex items-center gap-[2px]", className)}>
      {Array.from({ length: BAR_COUNT }, (_, i) => {
        const value = i < data.length ? data[i]! : 128;
        const normalized = Math.abs(value - 128) / 128;
        const height = Math.max(2, normalized * 20);
        const opacity = isActive ? 0.6 + normalized * 0.4 : 0.4 + normalized * 0.4;
        return (
          <div
            key={i}
            className={cn("w-[2px] rounded-full", barColor)}
            style={{ height: `${height}px`, opacity }}
          />
        );
      })}
    </div>
  );
}
```

- [ ] **Step 2: Type-check**

Run: `cd web && npx tsc --noEmit`
Expected: Clean.

- [ ] **Step 3: Commit**

```bash
git add web/src/components/waveform.tsx
git commit -m "feat: add Waveform component for audio visualization"
```

---

### Task 3: Create RecordingInput component

**Files:**
- Create: `web/src/components/recording-input.tsx`

- [ ] **Step 1: Create the RecordingInput component**

Create `web/src/components/recording-input.tsx`:

```tsx
import { useState, useEffect, useRef } from "react";
import { Mic, Send, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Waveform } from "@/components/waveform";
import { cn } from "@/lib/utils";

interface RecordingInputProps {
  isRecording: boolean;
  segmentCount: number;
  pendingDuration: number;
  analyserNode: AnalyserNode | null;

  textInput: string;
  onTextChange: (value: string) => void;
  inputFocused: boolean;
  onFocusChange: (focused: boolean) => void;

  onSend: () => void;
  onStartRecording: () => void;
  onStopRecording: () => void;
  onDiscard: () => void;

  disabled: boolean;
  dialogOpen: boolean;
}

function formatRecordingTime(seconds: number): string {
  const m = Math.floor(seconds / 60);
  const s = Math.floor(seconds % 60);
  return `${m}:${s.toString().padStart(2, "0")}`;
}

function formatPendingDuration(seconds: number): string {
  return `${seconds.toFixed(1)}s`;
}

export function RecordingInput({
  isRecording,
  segmentCount,
  pendingDuration,
  analyserNode,
  textInput,
  onTextChange,
  inputFocused,
  onFocusChange,
  onSend,
  onStartRecording,
  onStopRecording,
  onDiscard,
  disabled,
  dialogOpen,
}: RecordingInputProps) {
  const inputRef = useRef<HTMLInputElement>(null);

  // -- Waveform animation state --
  const [waveformData, setWaveformData] = useState<Uint8Array>(new Uint8Array(24).fill(128));
  const [frozenWaveform, setFrozenWaveform] = useState<Uint8Array>(new Uint8Array(24).fill(128));
  const [elapsedTime, setElapsedTime] = useState(0);
  const animFrameRef = useRef(0);
  const latestWaveformRef = useRef<Uint8Array>(new Uint8Array(24).fill(128));

  // Drive waveform + elapsed timer via requestAnimationFrame during recording
  useEffect(() => {
    if (!isRecording || !analyserNode) {
      cancelAnimationFrame(animFrameRef.current);
      return;
    }
    const startTime = performance.now();
    const baseOffset = pendingDuration; // include prior segments in elapsed
    const dataArray = new Uint8Array(analyserNode.frequencyBinCount);
    const tick = () => {
      analyserNode.getByteTimeDomainData(dataArray);
      const snapshot = new Uint8Array(dataArray);
      latestWaveformRef.current = snapshot;
      setWaveformData(snapshot);
      setElapsedTime(baseOffset + (performance.now() - startTime) / 1000);
      animFrameRef.current = requestAnimationFrame(tick);
    };
    tick();
    return () => cancelAnimationFrame(animFrameRef.current);
  }, [isRecording, analyserNode, pendingDuration]);

  // Freeze waveform snapshot when recording stops (use ref to avoid stale data)
  const prevRecording = useRef(false);
  useEffect(() => {
    if (prevRecording.current && !isRecording) {
      setFrozenWaveform(new Uint8Array(latestWaveformRef.current));
    }
    prevRecording.current = isRecording;
  }, [isRecording]);

  // -- Consolidated keyboard handlers --
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (inputFocused) {
        if (e.code === "Escape") inputRef.current?.blur();
        return;
      }
      if (disabled) return;
      if (e.code === "Space" && !e.repeat && !isRecording) {
        e.preventDefault();
        onStartRecording();
      } else if (e.code === "Enter" && !dialogOpen) {
        e.preventDefault();
        onSend();
      } else if ((e.code === "Delete" || e.code === "Backspace") && !isRecording && segmentCount > 0) {
        e.preventDefault();
        onDiscard();
      }
    };
    const handleKeyUp = (e: KeyboardEvent) => {
      if (e.code === "Space" && !inputFocused && isRecording) {
        e.preventDefault();
        onStopRecording();
      }
    };
    document.addEventListener("keydown", handleKeyDown);
    document.addEventListener("keyup", handleKeyUp);
    return () => {
      document.removeEventListener("keydown", handleKeyDown);
      document.removeEventListener("keyup", handleKeyUp);
    };
  }, [inputFocused, disabled, isRecording, segmentCount, dialogOpen,
      onStartRecording, onStopRecording, onSend, onDiscard]);

  // -- Input keydown (Enter to submit text) --
  const handleInputKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      onSend();
    }
  };

  // -- Derived --
  const canSend = (textInput.trim().length > 0 || segmentCount > 0) && !disabled;
  const hasPendingAudio = segmentCount > 0 && !isRecording;

  // -- Hint text --
  let hint: React.ReactNode = null;
  if (disabled) {
    hint = null;
  } else if (isRecording) {
    hint = (
      <>Recording... release <Kbd>Space</Kbd> to stop</>
    );
  } else if (hasPendingAudio) {
    hint = (
      <>
        <Kbd>Enter</Kbd> submit · <Kbd>Space</Kbd> add more · <Kbd>Delete</Kbd> discard
      </>
    );
  } else if (!inputFocused) {
    hint = (
      <>Hold <Kbd>Space</Kbd> to talk</>
    );
  }

  return (
    <div className="border-t border-border p-4 shrink-0">
      <div className="max-w-2xl mx-auto flex items-center gap-2">
        {/* Mic button */}
        <Button
          variant="ghost"
          size="icon"
          aria-label={isRecording ? "Stop recording" : "Start recording"}
          disabled={disabled}
          className={cn(isRecording && "ring-2 ring-red-500")}
          onClick={() => {
            if (isRecording) {
              onStopRecording();
            } else {
              onStartRecording();
            }
          }}
        >
          <Mic className={cn("size-4", isRecording && "text-red-500")} />
        </Button>

        {/* Delete button — only when audio is buffered */}
        {hasPendingAudio && (
          <Button
            variant="ghost"
            size="icon"
            aria-label="Discard recording"
            onClick={onDiscard}
          >
            <X className="size-4" />
          </Button>
        )}

        {/* Main input area — switches between text input, active waveform, and passive waveform */}
        {isRecording ? (
          <div className="flex-1 h-9 rounded-lg bg-red-500/5 border border-red-500/30 flex items-center gap-2 px-3 overflow-hidden">
            <div className="size-2 rounded-full bg-red-500 shrink-0 animate-pulse" />
            <Waveform data={waveformData} variant="active" className="flex-1" />
            <span className="text-red-500 text-xs font-mono tabular-nums shrink-0">
              {formatRecordingTime(elapsedTime)}
            </span>
          </div>
        ) : hasPendingAudio ? (
          <div className="flex-1 h-9 rounded-lg bg-muted/30 border border-border flex items-center gap-2 px-3 overflow-hidden">
            <Waveform data={frozenWaveform} variant="passive" className="flex-1" />
            <span className="text-muted-foreground text-xs font-mono tabular-nums shrink-0">
              {formatPendingDuration(pendingDuration)}
            </span>
          </div>
        ) : (
          <Input
            ref={inputRef}
            value={textInput}
            onChange={(e) => onTextChange(e.target.value)}
            onKeyDown={handleInputKeyDown}
            onFocus={() => onFocusChange(true)}
            onBlur={() => onFocusChange(false)}
            placeholder="Type a response..."
            disabled={disabled}
            className="flex-1"
          />
        )}

        {/* Send button */}
        <Button
          variant="default"
          size="icon"
          aria-label="Send message"
          disabled={!canSend}
          onClick={onSend}
        >
          <Send className="size-4" />
        </Button>
      </div>

      {/* Contextual keyboard hint */}
      {hint && (
        <p className="text-center text-[11px] text-muted-foreground mt-2">
          {hint}
        </p>
      )}
    </div>
  );
}

function Kbd({ children }: { children: React.ReactNode }) {
  return (
    <kbd className="px-1 py-0.5 rounded border border-border bg-muted text-[10px] font-mono">
      {children}
    </kbd>
  );
}
```

- [ ] **Step 2: Type-check**

Run: `cd web && npx tsc --noEmit`
Expected: Clean.

- [ ] **Step 3: Commit**

```bash
git add web/src/components/recording-input.tsx
git commit -m "feat: add RecordingInput component with waveform, hints, and keyboard controls"
```

---

### Task 4: Integrate RecordingInput into interview.tsx and restructure header

**Files:**
- Modify: `web/src/pages/interview.tsx`

- [ ] **Step 1: Replace input area with RecordingInput and restructure header**

At the top of `interview.tsx`, update imports. Remove `Mic`, `Send`, `X` from lucide (no longer used in this file). Add `RecordingInput`:

```typescript
import {
  ArrowLeft,
  Volume2,
} from "lucide-react";
```

Add:
```typescript
import { RecordingInput } from "@/components/recording-input";
```

Remove the `Input` import (no longer used in this file):
```typescript
// DELETE: import { Input } from "@/components/ui/input";
```

- [ ] **Step 2: Remove keyboard listeners and handlers that moved to RecordingInput**

In `InterviewInner`, remove all of these (they're now inside RecordingInput):
- The entire spacebar push-to-talk `useEffect` (lines 265–288)
- The entire escape-to-blur `useEffect` (lines 290–299)
- The entire global Enter `useEffect` (lines 338–348)
- `handleKeyDown` function (the React.KeyboardEvent one, lines 329–334)
- `canSend` const (line 336)
- The `inputRef` ref (line 227)

Keep: `handleSendText`, `handleSendAudio`, `handleSend`, `textInput`, `setTextInput`, `inputFocused`, `setInputFocused`, `ensureAudioContext`, `stopTts`, `isProcessing`. These are still used by callbacks passed to RecordingInput or by the chat area rendering.

Also keep the `onClick={ensureAudioContext}` on the chat area `<div>` — it initializes the TTS AudioContext on user interaction (browser autoplay policy).

- [ ] **Step 3: Update the onStartRecording and onStopRecording callbacks**

Destructure stable callbacks from audioRecorder (the return object is unstable — new every render):

```typescript
const { start: recStart, stop: recStop } = audioRecorder;
```

Add these callbacks (async to avoid duration flicker on first render after stop):

```typescript
const handleStartRecording = useCallback(async () => {
  stopTts();
  ensureAudioContext();
  await recStart();
}, [stopTts, ensureAudioContext, recStart]);

const handleStopRecording = useCallback(async () => {
  await recStop();
}, [recStop]);
```

- [ ] **Step 4: Restructure the header**

Replace the header section (the `<header>` element) with:

```tsx
<header className="flex items-center justify-between px-4 h-12 border-b border-border shrink-0">
  {/* Cancel */}
  <Button
    variant="ghost"
    size="sm"
    onClick={() => setCancelDialogOpen(true)}
  >
    <ArrowLeft className="size-4 mr-1" />
    Cancel
  </Button>

  {/* Question title */}
  <span className="text-sm font-medium truncate max-w-[40%] text-center">
    {sessionInfo?.question.title}
  </span>

  {/* Timer + End Session */}
  <div className="flex items-center gap-2">
    {audioPlayer.isPlaying && <TtsIndicator />}
    <span className={cn("text-sm font-mono tabular-nums", timerColor)}>
      {timerDisplay}
    </span>
    <Button
      variant="default"
      size="sm"
      disabled={isEnded}
      onClick={() => setEndDialogOpen(true)}
    >
      End Session
    </Button>
  </div>
</header>
```

- [ ] **Step 5: Replace the input area with RecordingInput**

Replace the entire `{/* ---- Input area ---- */}` section (from `<div className="border-t ...">` through its closing `</div>`, lines 431–496) with:

```tsx
<RecordingInput
  isRecording={audioRecorder.isRecording}
  segmentCount={audioRecorder.segmentCount}
  pendingDuration={audioRecorder.pendingDuration}
  analyserNode={audioRecorder.analyserNode}
  textInput={textInput}
  onTextChange={setTextInput}
  inputFocused={inputFocused}
  onFocusChange={setInputFocused}
  onSend={handleSend}
  onStartRecording={handleStartRecording}
  onStopRecording={handleStopRecording}
  onDiscard={audioRecorder.discard}
  disabled={inputDisabled}
  dialogOpen={cancelDialogOpen || endDialogOpen}
/>
```

- [ ] **Step 6: Type-check**

Run: `cd web && npx tsc --noEmit`
Expected: Clean. Fix any issues from removed imports or references.

- [ ] **Step 7: Visually verify in browser**

Run: `cd web && npm run dev`

Open the interview page. Verify:
1. Header shows question title centered, timer + End Session grouped on right
2. Idle state: text input with "Hold Space to talk" hint
3. Hold Space: waveform appears with red bars and elapsed timer
4. Release Space: passive gray waveform with duration, delete button appears next to mic
5. Press Enter: audio submits
6. Press Delete: audio discards, returns to idle
7. Type text and Enter: text submits normally
8. During streaming: input disabled, no hints

- [ ] **Step 8: Commit**

```bash
git add web/src/pages/interview.tsx
git commit -m "feat: integrate RecordingInput and restructure interview header with question title"
```

---

### Task 5: Clean up and final verification

**Files:**
- Modify: `web/src/pages/interview.tsx` (if needed)

- [ ] **Step 1: Run all frontend tests**

Run: `cd web && npx vitest run`
Expected: All tests PASS.

- [ ] **Step 2: Run type-check**

Run: `cd web && npx tsc --noEmit`
Expected: Clean.

- [ ] **Step 3: Run Go backend tests (no changes expected, sanity check)**

Run: `cd /Users/btc/Projects/src/drill && go test ./internal/handler/ -run "TestWS_" -count=1`
Expected: All PASS.

- [ ] **Step 4: Visual verification in browser**

Open the interview page. Run through a complete flow:
1. Start interview — verify header shows question title
2. Hold Space — verify live waveform with elapsed timer
3. Release Space — verify frozen gray waveform with duration
4. Hold Space again — verify waveform goes active again, duration accumulates on release
5. Press Enter — verify audio submits, candidate message appears after transcription
6. Press Delete (after recording) — verify audio discards
7. Type text, press Enter — verify text submission works
8. During interviewer streaming — verify input disabled, no hints, TTS plays
9. Refresh page — verify messages reload correctly

- [ ] **Step 5: Commit any cleanup**

If any fixes were needed during verification:
```bash
git add -p
git commit -m "fix: interview UX polish cleanup"
```
