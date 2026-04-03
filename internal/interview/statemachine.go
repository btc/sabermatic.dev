package interview

import (
	"fmt"
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
