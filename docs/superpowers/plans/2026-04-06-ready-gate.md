# Ready Gate — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a "Ready" gate to the interview page that initializes AudioContext and mic permission before the interview begins, while the WebSocket connects and buffers the interviewer's opening response in the background.

**Architecture:** Add a `ready` boolean to `InterviewInner`. Before ready, the page shows the question title + a centered Ready button while WS messages accumulate silently in existing state. On click/Enter, `AudioContext.initContext()` and `getUserMedia()` fire from the user gesture, then the full interview UI renders with buffered content. Auto-skip the gate on reconnect (existing messages means session is in progress).

**Tech Stack:** React, existing `AudioPlayer.initContext()`, `navigator.mediaDevices.getUserMedia`

---

## File Map

- **Modify:** `web/src/pages/interview.tsx` — add `ReadyGate` sub-component, `ready` state, `handleReady`, auto-skip on reconnect, remove redundant `onClick={initTtsContext}` from chat area

---

### Task 1: Add ReadyGate to interview page

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
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if (e.code === "Enter") {
        e.preventDefault();
        onReady();
      }
    };
    document.addEventListener("keydown", handler);
    return () => document.removeEventListener("keydown", handler);
  }, [onReady]);

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

- [ ] **Step 2: Add `ready` state and `handleReady` to `InterviewInner`**

After the existing local state declarations (after `const [endDialogOpen, setEndDialogOpen] = useState(false);`, around line 219), add:

```tsx
  const [ready, setReady] = useState(false);
```

After the `initTtsContext` callback (around line 237), add:

```tsx
  // ------ Ready gate ------
  const handleReady = useCallback(() => {
    audioPlayer.initContext();
    // Pre-request mic permission — resolves instantly if already granted,
    // shows prompt if first visit. Fire-and-forget so UI isn't blocked.
    navigator.mediaDevices.getUserMedia({ audio: true }).catch(() => {});
    setReady(true);
  }, [audioPlayer]);

  // Auto-skip ready gate on reconnect (messages already exist).
  useEffect(() => {
    if (messages.length > 0 && !ready) {
      setReady(true);
    }
  }, [messages.length, ready]);
```

- [ ] **Step 3: Add the ReadyGate render gate**

In the render section of `InterviewInner`, after the `isEnded` early return (the `WaitingView` block, around line 316) and before the main `return (` on line 318, add:

```tsx
  if (!ready) {
    return (
      <div className="flex flex-col h-full">
        <ReadyGate
          questionTitle={sessionInfo?.question.title ?? ""}
          questionPrompt={sessionInfo?.question.prompt ?? ""}
          onReady={handleReady}
        />
      </div>
    );
  }
```

- [ ] **Step 4: Remove redundant `onClick={initTtsContext}` from chat area**

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

The AudioContext is now guaranteed to be initialized by `handleReady` before the chat area is ever rendered.

- [ ] **Step 5: Verify in browser**

1. Start the dev server: `cd web && npm run dev`
2. Create a new session and navigate to the interview page
3. Confirm: question title and prompt are shown with a centered "Ready" button
4. Confirm: pressing Enter OR clicking Ready starts the interview
5. Confirm: interviewer's opening message text appears immediately after Ready
6. Confirm: interviewer's opening message audio plays smoothly after Ready
7. Confirm: first voice recording works without cutoff (mic permission was pre-granted)
8. Confirm: refreshing the page mid-interview skips the Ready gate (reconnect)

- [ ] **Step 6: Commit**

```bash
git add web/src/pages/interview.tsx
git commit -m "feat(interview): add Ready gate for AudioContext and mic init

Shows question title + centered Ready button before interview begins.
WebSocket connects immediately and buffers interviewer's opening
response. On Ready (click or Enter), AudioContext and mic permission
are initialized from the user gesture, then buffered content flushes.
Auto-skips on reconnect when messages already exist."
```
