package interview

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestWarningMinutes(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		want     int
	}{
		{"45 min session", 45 * time.Minute, 5},  // round(45/9)=5
		{"30 min session", 30 * time.Minute, 3},  // round(30/9)=3
		{"10 min session", 10 * time.Minute, 2},  // round(10/9)=1, clamped to 2
		{"5 min session", 5 * time.Minute, 2},    // round(5/9)=1, clamped to 2
		{"90 min session", 90 * time.Minute, 5},  // round(90/9)=10, clamped to 5
		{"60 min session", 60 * time.Minute, 5},  // round(60/9)=7, clamped to 5
		{"20 min session", 20 * time.Minute, 2},  // round(20/9)=2
		{"18 min session", 18 * time.Minute, 2},  // round(18/9)=2
		{"27 min session", 27 * time.Minute, 3},  // round(27/9)=3
		{"36 min session", 36 * time.Minute, 4},  // round(36/9)=4
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := warningMinutes(tt.duration)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestWarningMinutes_WarningAt(t *testing.T) {
	// Verify when the warning fires relative to the session.
	tests := []struct {
		name              string
		duration          time.Duration
		wantWarningBefore time.Duration // warning fires this many minutes before end
	}{
		{"45 min", 45 * time.Minute, 5 * time.Minute},
		{"30 min", 30 * time.Minute, 3 * time.Minute},
		{"10 min", 10 * time.Minute, 2 * time.Minute},
		{"5 min", 5 * time.Minute, 2 * time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := warningMinutes(tt.duration)
			warningAt := tt.duration - time.Duration(w)*time.Minute
			remaining := tt.duration - warningAt
			assert.Equal(t, tt.wantWarningBefore, remaining)
		})
	}
}

func TestForceState(t *testing.T) {
	sm := NewStateMachine(StateTranscribing)

	// Normal transition to WaitingForInput from Transcribing is invalid.
	err := sm.Transition(StateWaitingForInput)
	assert.ErrorIs(t, err, ErrInvalidTransition)
	assert.Equal(t, StateTranscribing, sm.State())

	// ForceState bypasses validation.
	sm.ForceState(StateWaitingForInput)
	assert.Equal(t, StateWaitingForInput, sm.State())
}

func TestIsReconnect(t *testing.T) {
	c := &Conductor{}

	// No LastSeq -> not a reconnect.
	c.initMsg = WSMessage{Type: "session_init"}
	assert.False(t, c.isReconnect())

	// With LastSeq -> reconnect.
	seq := 3
	c.initMsg = WSMessage{Type: "session_init", LastSeq: &seq}
	assert.True(t, c.isReconnect())
}
