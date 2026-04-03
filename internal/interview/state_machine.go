package interview

import (
	"fmt"
	"math"
	"time"
)

var ErrInvalidTransition = fmt.Errorf("invalid state transition")

type ConductorState string

const (
	StateInterviewerSpeaking ConductorState = "interviewer_speaking"
	StateWaitingForInput     ConductorState = "waiting_for_input"
	StateTranscribing        ConductorState = "transcribing"
	StateProcessingInput     ConductorState = "processing_input"
	StateEnding              ConductorState = "ending"
	StateEnded               ConductorState = "ended"
)

var transitions = map[ConductorState]map[ConductorState]bool{
	StateInterviewerSpeaking: {StateWaitingForInput: true, StateEnding: true},
	StateWaitingForInput:     {StateInterviewerSpeaking: true, StateTranscribing: true, StateProcessingInput: true, StateEnding: true},
	StateTranscribing:        {StateProcessingInput: true, StateEnding: true},
	StateProcessingInput:     {StateInterviewerSpeaking: true, StateEnding: true},
	StateEnding:              {StateEnded: true},
	StateEnded:               {},
}

type StateMachine struct {
	state     ConductorState
	turnCount int
	startedAt time.Time
}

func NewStateMachine(initial ConductorState) *StateMachine {
	return &StateMachine{state: initial, startedAt: time.Now()}
}

func (sm *StateMachine) Transition(next ConductorState) error {
	allowed := transitions[sm.state]
	if !allowed[next] {
		return fmt.Errorf("%w: %s → %s", ErrInvalidTransition, sm.state, next)
	}
	if next == StateWaitingForInput {
		sm.turnCount++
	}
	sm.state = next
	return nil
}

func (sm *StateMachine) State() ConductorState { return sm.state }
func (sm *StateMachine) TurnCount() int         { return sm.turnCount }
func (sm *StateMachine) StartedAt() time.Time   { return sm.startedAt }

// SetStartedAt records when the session started. Called by the conductor
// after loading session state from the database.
func (sm *StateMachine) SetStartedAt(t time.Time) {
	sm.startedAt = t
}

// ForceState sets the state without validation. Used only for error recovery
// (e.g., STT failure, LLM failure) to reset the session to a usable state.
// Normal transitions must use Transition().
func (sm *StateMachine) ForceState(s ConductorState) {
	sm.state = s
}

// warningMinutes calculates the number of minutes before session end to fire
// the timer warning. Formula: clamp(2, 5, round(duration_minutes / 9)).
func warningMinutes(duration time.Duration) int {
	minutes := duration.Minutes()
	return int(math.Max(2, math.Min(5, math.Round(minutes/9))))
}

// warningDelay returns how long until the warning timer should fire.
// Warning fires at: duration - clamp(2, 5, round(duration_minutes/9)) minutes.
func warningDelay(duration, elapsed time.Duration) time.Duration {
	w := time.Duration(warningMinutes(duration)) * time.Minute
	return max(0, duration-w-elapsed)
}

// overtimeDelay returns how long until the overtime timer should fire.
func overtimeDelay(duration, elapsed time.Duration) time.Duration {
	return max(0, duration-elapsed)
}

// autoEndDelay returns how long until the session auto-ends (2 min after overtime).
func autoEndDelay(duration, elapsed time.Duration) time.Duration {
	return max(0, duration+2*time.Minute-elapsed)
}
