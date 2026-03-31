# Session Duration Configuration

## Goal

Allow users to configure session duration (and TTS) before starting an interview, instead of immediately jumping into a hardcoded 45-minute session.

## Current Behavior

Clicking a question in the question bank immediately creates a session with `timer_setting_sec: 2700` (45 min) and `tts_enabled: true` hardcoded in `Home.tsx`, then navigates to the Interview component which starts instantly.

## New Behavior

### Pre-Session Config Screen

After clicking a question, show a lightweight configuration screen:

- **Question title** + difficulty badge (from the selected question)
- **Duration selector**: Preset buttons for 15 / 30 / 45 / 60 minutes, plus a custom input field for arbitrary minute values. Default selection: 45 minutes.
- **TTS toggle**: Checkbox or switch, default on.
- **"Start Interview" button**: Creates the session with chosen settings, navigates to `/sessions/{id}`.

This screen lives at the existing route — no new URL needed. `Home.tsx`'s `handleStartSession` stops creating the session immediately and instead shows this config screen (either inline or as a separate component). The session is only created when the user clicks "Start Interview."

### Interviewer Prompt Update

In `backend/interviewer.py`, update the "Last 5 minutes" behavioral rule to include the session total for context:

Current:
```
- **Last 5 minutes** (around {total_min - 5} minutes onward): Begin wrapping up.
```

New:
```
- **Last 5 minutes** (around {total_min - 5} of {total_min} minutes): Begin wrapping up.
```

### Interview Timer Fix

`Interview.tsx` has `const TIMER_TOTAL = 45 * 60` hardcoded. Change to read from `session.timer_setting_sec` which is already available via the `session` prop.

## Files Changed

- `frontend/src/pages/Home.tsx` — replace immediate session creation with config screen display
- `frontend/src/components/SessionConfig.tsx` (new) — the config screen component
- `frontend/src/pages/Interview.tsx` — use `session.timer_setting_sec` instead of hardcoded constant
- `backend/interviewer.py` — add "of {total_min}" to last-5-minutes prompt line

## What Doesn't Change

- Backend models and API — `SessionCreate` already accepts `timer_setting_sec` and `tts_enabled`
- Database schema — no changes
- Orchestrator — already uses `session.timer_setting_sec` from DB
- WebSocket flow — `session_loaded` already sends `timer_sec` to client
