package interview

import (
	"github.com/google/uuid"

	"github.com/btc/drill/internal/db"
)

// Server-to-client message constructors. Zero-arg messages are package-level
// vars; parameterized messages are functions. Keeps conductor.go focused on
// control flow.

// Zero-arg messages.
var (
	msgPong            = map[string]string{"type": "pong"}
	msgTimerOvertime   = map[string]string{"type": "timer_overtime"}
	msgReconnectPlease = map[string]string{"type": "reconnect_please"}
)

// msgSessionLoaded builds the session_loaded event sent on first connect.
func msgSessionLoaded(sessionID uuid.UUID, question *db.Question, durationMin int, ttsEnabled bool) map[string]any {
	return map[string]any{
		"type":       "session_loaded",
		"session_id": sessionID.String(),
		"question":   map[string]string{"title": question.Title, "prompt": question.Prompt},
		"duration":   durationMin,
		"tts_enabled": ttsEnabled,
	}
}

// msgReconnectState builds the reconnect_state event with missed messages.
func msgReconnectState(lastSeq int, messages []db.Message) map[string]any {
	var missed []db.Message
	for _, m := range messages {
		if int(m.Seq) > lastSeq {
			missed = append(missed, m)
		}
	}
	return map[string]any{
		"type":     "reconnect_state",
		"messages": missed,
	}
}

// msgTranscriptionResult builds the transcription_result event.
func msgTranscriptionResult(text string) map[string]string {
	return map[string]string{"type": "transcription_result", "text": text}
}

// msgTimerWarning builds the timer_warning event.
func msgTimerWarning(minutesRemaining int) map[string]any {
	return map[string]any{
		"type":              "timer_warning",
		"minutes_remaining": minutesRemaining,
	}
}

// msgSessionEnded builds the session_ended event with a reason.
func msgSessionEnded(reason string) map[string]any {
	return map[string]any{
		"type":   "session_ended",
		"reason": reason,
	}
}

// msgError builds an error message with code and detail.
func msgError(code, message string) map[string]string {
	return map[string]string{
		"type":    "error",
		"code":    code,
		"message": message,
	}
}

// msgStateChange builds a state_change notification.
func msgStateChange(state ConductorState) map[string]string {
	return map[string]string{"type": "state_change", "state": string(state)}
}
