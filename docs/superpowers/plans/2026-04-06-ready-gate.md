# Ready Gate — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a "Ready" gate to the interview page that initializes AudioContext and mic permission before the interview begins, while the WebSocket connects and buffers the interviewer's opening response in the background.

**Architecture:** Add a `ready` boolean to `InterviewInner`. Before ready, the page shows the question title + a centered Ready button while WS messages accumulate silently in existing state. On click/Enter, `AudioContext.initContext()` (async, awaits `ctx.resume()`) and `getUserMedia()` fire from the user gesture, then the full interview UI renders with buffered content. Auto-skip the gate on reconnect using a `wasReconnected` signal from the hook (not `messages.length`, which would race with the first `interviewer_done`).

**Tech Stack:** React, existing `AudioPlayer.initContext()`, `navigator.mediaDevices.getUserMedia`

---

## File Map

- **Modify:** `web/src/ws/hooks.ts` — expose `wasReconnected` boolean, set in `reconnect_state` handler
- **Modify:** `web/src/audio/player.ts` — make `initContext()` async, await `ctx.resume()` before flushing queue
- **Modify:** `web/src/audio/hooks.ts` — update `initContext` wrapper to return Promise
- **Modify:** `web/src/pages/interview.tsx` — add `ReadyGate` sub-component, `ready` state, async `handleReady`, auto-skip on reconnect via `wasReconnected`, remove `initTtsContext` and all its call sites

---

### Task 1: Expose `wasReconnected` from `useInterview`

**Files:**
- Modify: `web/src/ws/hooks.ts:24-208`

- [ ] **Step 1: Add `wasReconnected` ref and set it in the `reconnect_state` handler**

In `useInterview`, after the existing refs (around line 42), add:

```ts
  const wasReconnectedRef = useRef(false);
```

In the `reconnect_state` case (around line 56), after `setState("waiting");`, add:

```ts
        wasReconnectedRef.current = true;
```

- [ ] **Step 2: Expose `wasReconnected` in the return value**

Change the return statement (around line 194) — add `wasReconnected: wasReconnectedRef.current` to the returned object:

```ts
  return {
    messages,
    streamingText,
    state,
    connectionState,
    sessionInfo,
    lastError,
    wasReconnected: wasReconnectedRef.current,
    sendText,
    sendAudio,
    endSession,
    cancelSession,
    cancelTts,
    setRawMessageHandler,
  };
```

- [ ] **Step 3: Commit**

```bash
git add web/src/ws/hooks.ts
git commit -m "feat(ws): expose wasReconnected signal from useInterview

Set when reconnect_state is received (page refresh of existing session).
Used by Ready gate to auto-skip on reconnect without racing with the
first interviewer_done on fresh sessions."
```

---

### Task 2: Make `AudioPlayer.initContext()` async

**Files:**
- Modify: `web/src/audio/player.ts:22-33`
- Modify: `web/src/audio/hooks.ts:62-64`

- [ ] **Step 1: Make `initContext` async and await `ctx.resume()`**

In `player.ts`, replace the `initContext` method:

```ts
  async initContext(): Promise<void> {
    if (!this.ctx) {
      this.ctx = new AudioContext();
    }
    if (this.ctx.state === "suspended") {
      await this.ctx.resume();
    }
    // Kick off playback if chunks arrived before context was ready.
    if (this.queue.length > 0 && !this._isPlaying) {
      this._isPlaying = true;
      this.playNext();
    }
  }
```

- [ ] **Step 2: Update the hook wrapper in `hooks.ts`**

In `hooks.ts`, update the `initContext` wrapper to return the Promise:

```ts
  const initContext = useCallback(() => {
    return playerRef.current?.initContext();
  }, []);
```

- [ ] **Step 3: Commit**

```bash
git add web/src/audio/player.ts web/src/audio/hooks.ts
git commit -m "fix(audio): await AudioContext.resume() before flushing queue

Calling decodeAudioData while context is transitioning from suspended
to running can fail silently on Safari. Awaiting resume() ensures
the context is fully active before queued chunks are played."
```

---

### Task 3: Add ReadyGate to interview page

**Files:**
- Modify: `web/src/pages/interview.tsx`

- [ ] **Step 1: Add the `ReadyGate` sub-component**

Add this after the existing sub-components (after `ProcessingIndicator`, before the `WaitingView` section around line 96):

```tsx
function ReadyGate({
  questionTitle,
  questionPrompt,
  onReady,
}: {
  questionTitle: string;
  questionPrompt: string;
  onReady: () => void;
}) {
  const onReadyRef = useRef(onReady);
  onReadyRef.current = onReady;

  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if (e.code === "Enter") {
        e.preventDefault();
        onReadyRef.current();
      }
    };
    document.addEventListener("keydown", handler);
    return () => document.removeEventListener("keydown", handler);
  }, []);

  return (
    <div className="flex flex-col items-center justify-center h-full gap-8 px-4">
      <div className="flex flex-col items-center gap-3 text-center max-w-md">
        <h2 className="text-lg font-semibold">{questionTitle}</h2>
        <p className="text-sm text-muted-foreground leading-relaxed">{questionPrompt}</p>
      </div>
      <Button
        size="lg"
        className="h-14 px-12 text-lg font-medium"
        onClick={onReady}
        autoFocus
      >
        Ready
      </Button>
      <p className="text-xs text-muted-foreground">
        Press <kbd className="px-1 py-0.5 rounded border border-border bg-muted text-[10px] font-mono">Enter</kbd> to start
      </p>
    </div>
  );
}
```

Note: uses a ref for the callback to avoid re-registering the keydown listener on every render (the `audioPlayer` object from `useAudioPlayer` is unstable).

- [ ] **Step 2: Add `ready` state and async `handleReady` to `InterviewInner`**

Update the destructured return from `useInterview` to include `wasReconnected`:

```tsx
  const {
    messages,
    streamingText,
    state,
    connectionState,
    sessionInfo,
    lastError,
    wasReconnected,
    sendText,
    sendAudio,
    endSession,
    cancelSession,
    cancelTts,
    setRawMessageHandler,
  } = useInterview(sessionId);
```

After the existing local state declarations (after `const [endDialogOpen, setEndDialogOpen] = useState(false);`, around line 219), add:

```tsx
  const [ready, setReady] = useState(false);
```

Replace the `initTtsContext` callback (around lines 234-237) with the ready gate logic:

```tsx
  // ------ Ready gate ------
  const handleReady = useCallback(async () => {
    if (ready) return;
    await audioPlayer.initContext();
    navigator.mediaDevices.getUserMedia({ audio: true }).catch(() => {
      toast.error("Microphone access denied — you can still use text input");
    });
    setReady(true);
  }, [audioPlayer, ready]);

  // Auto-skip ready gate on reconnect (page refresh of existing session).
  useEffect(() => {
    if (wasReconnected && !ready) {
      setReady(true);
    }
  }, [wasReconnected, ready]);
```

- [ ] **Step 3: Remove all `initTtsContext` references**

Remove the `initTtsContext()` calls from `handleSendText`, `handleSendAudio`, and `handleStartRecording`. The ready gate guarantees AudioContext is initialized before these are ever reachable.

In `handleSendText` (around line 260), remove the `initTtsContext();` line and remove `initTtsContext` from the dependency array:

```tsx
  const handleSendText = useCallback(() => {
    const trimmed = textInput.trim();
    if (!trimmed) return;
    stopTts();
    sendText(trimmed);
    setTextInput("");
  }, [textInput, sendText, stopTts]);
```

In `handleSendAudio` (around line 269), remove the `initTtsContext();` line and remove `initTtsContext` from the dependency array:

```tsx
  const handleSendAudio = useCallback(async () => {
    if (audioRecorder.segmentCount === 0) return;
    stopTts();
    const audioBase64 = await audioRecorder.submit();
    if (audioBase64) {
      sendAudio(audioBase64);
    }
  }, [audioRecorder, sendAudio, stopTts]);
```

In `handleStartRecording` (around line 288), remove the `initTtsContext();` line and remove `initTtsContext` from the dependency array:

```tsx
  const handleStartRecording = useCallback(async () => {
    stopTts();
    await recStart();
  }, [stopTts, recStart]);
```

- [ ] **Step 4: Add the ReadyGate render gates**

In the render section of `InterviewInner`, after the `isEnded` early return (the `WaitingView` block, around line 316) and before the main `return (`, add two gates:

```tsx
  // Loading — WS hasn't delivered session_loaded yet.
  if (!ready && !sessionInfo) {
    return (
      <div className="flex items-center justify-center h-full">
        <div className="size-3 rounded-full bg-primary animate-pulse" />
      </div>
    );
  }

  // Ready gate — show question and wait for user gesture.
  if (!ready) {
    return (
      <div className="flex flex-col h-full">
        <ReadyGate
          questionTitle={sessionInfo!.question.title}
          questionPrompt={sessionInfo!.question.prompt}
          onReady={handleReady}
        />
      </div>
    );
  }
```

- [ ] **Step 5: Remove `onClick={initTtsContext}` from chat area**

In the chat area `<div>` (around line 358-361), remove the `onClick={initTtsContext}` handler:

Replace:
```tsx
      <div
        className="flex-1 overflow-y-auto"
        onClick={initTtsContext}
      >
```

With:
```tsx
      <div className="flex-1 overflow-y-auto">
```

- [ ] **Step 6: Verify in browser**

1. Start the dev server: `cd web && npm run dev`
2. Create a new session and navigate to the interview page
3. Confirm: loading spinner shows briefly, then question title/prompt + centered "Ready" button
4. Confirm: pressing Enter OR clicking Ready starts the interview
5. Confirm: interviewer's opening message text appears immediately after Ready
6. Confirm: interviewer's opening message audio plays smoothly after Ready
7. Confirm: first voice recording works without cutoff (mic permission was pre-granted)
8. Confirm: refreshing the page mid-interview skips the Ready gate (reconnect)
9. Confirm: denying mic permission shows a toast but interview still works (text-only)

- [ ] **Step 7: Commit**

```bash
git add web/src/pages/interview.tsx
git commit -m "feat(interview): add Ready gate for AudioContext and mic init

Shows question title + centered Ready button before interview begins.
WebSocket connects immediately and buffers interviewer's opening
response. On Ready (click or Enter), AudioContext and mic permission
are initialized from the user gesture, then buffered content flushes.
Auto-skips on reconnect via wasReconnected signal from useInterview.
Removes initTtsContext — no longer needed with Ready gate."
```
