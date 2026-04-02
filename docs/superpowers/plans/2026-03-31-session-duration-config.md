# Session Duration Configuration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let users configure session duration and TTS before starting an interview, instead of hardcoding 45 minutes.

**Architecture:** New `SessionConfig` component shown after question selection. Home.tsx stores selected question in state instead of immediately creating the session. Interview.tsx reads `session.timer_setting_sec` instead of hardcoded constant. One-line prompt tweak in the backend.

**Tech Stack:** React/TypeScript (frontend), Python (backend interviewer prompt)

---

### Task 1: SessionConfig component

**Files:**
- Create: `frontend/src/components/SessionConfig.tsx`

- [ ] **Step 1: Create the SessionConfig component**

Create `frontend/src/components/SessionConfig.tsx`:

```tsx
import { useState } from "react";
import type { Question } from "../types";

interface SessionConfigProps {
  question: Question;
  onStart: (timerSec: number, ttsEnabled: boolean) => void;
  onCancel: () => void;
}

const DURATION_PRESETS = [15, 30, 45, 60];
const DEFAULT_DURATION = 45;

export default function SessionConfig({ question, onStart, onCancel }: SessionConfigProps) {
  const [durationMin, setDurationMin] = useState(DEFAULT_DURATION);
  const [customInput, setCustomInput] = useState("");
  const [ttsEnabled, setTtsEnabled] = useState(true);

  function handlePreset(min: number) {
    setDurationMin(min);
    setCustomInput("");
  }

  function handleCustomChange(value: string) {
    setCustomInput(value);
    const parsed = parseInt(value, 10);
    if (!isNaN(parsed) && parsed > 0 && parsed <= 180) {
      setDurationMin(parsed);
    }
  }

  function handleStart() {
    onStart(durationMin * 60, ttsEnabled);
  }

  return (
    <div className="session-config card">
      <h2>{question.title}</h2>
      <span className={`tag ${question.difficulty === "hard" ? "badge-red" : "badge-orange"}`}>
        {question.difficulty}
      </span>

      <div className="session-config-section">
        <label className="session-config-label">Duration</label>
        <div className="session-config-durations">
          {DURATION_PRESETS.map((min) => (
            <button
              key={min}
              className={`session-config-duration-btn ${durationMin === min && customInput === "" ? "session-config-duration-btn-active" : ""}`}
              onClick={() => handlePreset(min)}
            >
              {min}m
            </button>
          ))}
          <input
            type="number"
            className="session-config-custom-input"
            placeholder="Custom"
            min={1}
            max={180}
            value={customInput}
            onChange={(e) => handleCustomChange(e.target.value)}
          />
        </div>
      </div>

      <div className="session-config-section">
        <label className="session-config-label">
          <input
            type="checkbox"
            checked={ttsEnabled}
            onChange={(e) => setTtsEnabled(e.target.checked)}
          />
          Voice responses (text-to-speech)
        </label>
      </div>

      <div className="session-config-actions">
        <button className="btn-primary" onClick={handleStart}>
          Start Interview
        </button>
        <button className="btn-secondary" onClick={onCancel}>
          Cancel
        </button>
      </div>
    </div>
  );
}
```

- [ ] **Step 2: Commit**

```bash
git add frontend/src/components/SessionConfig.tsx
git commit -m "feat: add SessionConfig component with duration presets and TTS toggle"
```

---

### Task 2: Wire SessionConfig into Home.tsx

**Files:**
- Modify: `frontend/src/pages/Home.tsx`

- [ ] **Step 1: Add selected question state and config flow**

In `frontend/src/pages/Home.tsx`:

Add import at top:
```tsx
import SessionConfig from "../components/SessionConfig";
```

Add state for the selected question:
```tsx
const [selectedQuestion, setSelectedQuestion] = useState<Question | null>(null);
```

Replace the `handleStartSession` function:
```tsx
function handleSelectQuestion(questionId: number) {
  const q = questions.find((q) => q.id === questionId) ?? null;
  setSelectedQuestion(q);
}

async function handleStartSession(timerSec: number, ttsEnabled: boolean) {
  if (!selectedQuestion) return;
  const session = await api.sessions.create({
    question_id: selectedQuestion.id,
    timer_setting_sec: timerSec,
    tts_enabled: ttsEnabled,
  });
  navigate(`/sessions/${session.id}`);
}
```

In the JSX, if `selectedQuestion` is set, show `SessionConfig` instead of the normal home content. Add this right after the opening `<div className="container">`:

```tsx
{selectedQuestion ? (
  <SessionConfig
    question={selectedQuestion}
    onStart={handleStartSession}
    onCancel={() => setSelectedQuestion(null)}
  />
) : (
  <>
    {/* existing home content: header, coach card, stats, question list */}
  </>
)}
```

Update the two places that call `handleStartSession` to use `handleSelectQuestion` instead:
- `CoachCard`'s `onStartSession` prop: change from `handleStartSession` to `handleSelectQuestion`
- `QuestionList`'s `onSelect` prop: change from `handleStartSession` to `handleSelectQuestion`

- [ ] **Step 2: Verify the app compiles**

Run: `cd frontend && npx tsc --noEmit`

- [ ] **Step 3: Commit**

```bash
git add frontend/src/pages/Home.tsx
git commit -m "feat: show SessionConfig before starting interview"
```

---

### Task 3: Fix hardcoded timer in Interview.tsx and update interviewer prompt

**Files:**
- Modify: `frontend/src/pages/Interview.tsx`
- Modify: `backend/interviewer.py`

- [ ] **Step 1: Use session.timer_setting_sec in Interview.tsx**

In `frontend/src/pages/Interview.tsx`:

Remove the hardcoded constant:
```tsx
// DELETE THIS LINE:
const TIMER_TOTAL = 45 * 60; // 45 minutes default
```

In the `Interview` component body, replace usages of `TIMER_TOTAL` with `session.timer_setting_sec`:

The Timer component in the JSX uses `total={TIMER_TOTAL}`. Change to:
```tsx
<Timer elapsed={seconds} total={session.timer_setting_sec} />
```

Search for any other references to `TIMER_TOTAL` in the file and replace with `session.timer_setting_sec`.

- [ ] **Step 2: Update interviewer prompt**

In `backend/interviewer.py`, find the line (around line 130):
```python
- **Last 5 minutes** (around {total_min - 5} minutes onward): Begin wrapping up. Ask the candidate to summarize trade-offs, discuss what they would monitor in production, or address anything they feel they missed.
```

Replace with:
```python
- **Last 5 minutes** (around {total_min - 5} of {total_min} minutes): Begin wrapping up. Ask the candidate to summarize trade-offs, discuss what they would monitor in production, or address anything they feel they missed.
```

- [ ] **Step 3: Run backend tests**

Run: `pytest tests/ -x -q`
Expected: all pass (prompt change doesn't break any tests)

- [ ] **Step 4: Verify frontend compiles**

Run: `cd frontend && npx tsc --noEmit`

- [ ] **Step 5: Commit**

```bash
git add frontend/src/pages/Interview.tsx backend/interviewer.py
git commit -m "feat: use session duration from DB, add total to interviewer prompt"
```
