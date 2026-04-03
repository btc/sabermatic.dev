package interview_test

import (
	"testing"

	"github.com/btc/drill/internal/interview"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStateMachine_ValidTransitions(t *testing.T) {
	tests := []struct {
		name string
		from interview.ConductorState
		to   interview.ConductorState
	}{
		{"speaking to waiting", interview.StateInterviewerSpeaking, interview.StateWaitingForInput},
		{"speaking to ending", interview.StateInterviewerSpeaking, interview.StateEnding},
		{"waiting to transcribing", interview.StateWaitingForInput, interview.StateTranscribing},
		{"waiting to processing", interview.StateWaitingForInput, interview.StateProcessingInput},
		{"waiting to ending", interview.StateWaitingForInput, interview.StateEnding},
		{"transcribing to processing", interview.StateTranscribing, interview.StateProcessingInput},
		{"transcribing to ending", interview.StateTranscribing, interview.StateEnding},
		{"processing to speaking", interview.StateProcessingInput, interview.StateInterviewerSpeaking},
		{"processing to ending", interview.StateProcessingInput, interview.StateEnding},
		{"ending to ended", interview.StateEnding, interview.StateEnded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sm := interview.NewStateMachine(tt.from)
			err := sm.Transition(tt.to)
			require.NoError(t, err)
			assert.Equal(t, tt.to, sm.State())
		})
	}
}

func TestStateMachine_InvalidTransitions(t *testing.T) {
	tests := []struct {
		name string
		from interview.ConductorState
		to   interview.ConductorState
	}{
		{"ended is terminal", interview.StateEnded, interview.StateWaitingForInput},
		{"waiting cannot go to speaking", interview.StateWaitingForInput, interview.StateInterviewerSpeaking},
		{"transcribing cannot go to waiting", interview.StateTranscribing, interview.StateWaitingForInput},
		{"speaking cannot go to processing", interview.StateInterviewerSpeaking, interview.StateProcessingInput},
		{"ending cannot go to waiting", interview.StateEnding, interview.StateWaitingForInput},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sm := interview.NewStateMachine(tt.from)
			err := sm.Transition(tt.to)
			require.ErrorIs(t, err, interview.ErrInvalidTransition)
			assert.Equal(t, tt.from, sm.State(), "state should not change on invalid transition")
		})
	}
}

func TestStateMachine_TurnCount(t *testing.T) {
	sm := interview.NewStateMachine(interview.StateInterviewerSpeaking)
	assert.Equal(t, 0, sm.TurnCount())

	require.NoError(t, sm.Transition(interview.StateWaitingForInput))
	assert.Equal(t, 1, sm.TurnCount())

	require.NoError(t, sm.Transition(interview.StateProcessingInput))
	require.NoError(t, sm.Transition(interview.StateInterviewerSpeaking))
	require.NoError(t, sm.Transition(interview.StateWaitingForInput))
	assert.Equal(t, 2, sm.TurnCount())
}

func TestStateMachine_SelfTransitionInvalid(t *testing.T) {
	sm := interview.NewStateMachine(interview.StateWaitingForInput)
	err := sm.Transition(interview.StateWaitingForInput)
	require.ErrorIs(t, err, interview.ErrInvalidTransition)
}
