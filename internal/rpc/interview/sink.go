package interview

import (
	"log/slog"
	"sync"

	"connectrpc.com/connect"

	"github.com/btc/drill/internal/backend"
	drillv1 "github.com/btc/drill/internal/pb/drill/v1"
)

var _ backend.TurnEventSink = (*connectTurnSink)(nil)

// connectTurnSink sends turn events on the SubmitTurn response stream.
// ExecuteTurn calls it from more than one goroutine (interviewer tokens from
// the conductor, audio from the TTS goroutine), and connect.ServerStream.Send
// is not safe for concurrent use, so sends are serialized.
type connectTurnSink struct {
	mu     sync.Mutex
	stream *connect.ServerStream[drillv1.TurnEvent]
}

func (s *connectTurnSink) send(event *drillv1.TurnEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.stream.Send(event); err != nil {
		slog.Debug("interview: sink send failed", "error", err)
	}
}

func (s *connectTurnSink) TranscriptionResult(e *drillv1.TranscriptionResult) {
	s.send(&drillv1.TurnEvent{Event: &drillv1.TurnEvent_TranscriptionResult{TranscriptionResult: e}})
}

func (s *connectTurnSink) InterviewerToken(e *drillv1.InterviewerToken) {
	s.send(&drillv1.TurnEvent{Event: &drillv1.TurnEvent_InterviewerToken{InterviewerToken: e}})
}

func (s *connectTurnSink) InterviewerDone(e *drillv1.InterviewerDone) {
	s.send(&drillv1.TurnEvent{Event: &drillv1.TurnEvent_InterviewerDone{InterviewerDone: e}})
}

func (s *connectTurnSink) TtsChunk(e *drillv1.TtsChunk) {
	s.send(&drillv1.TurnEvent{Event: &drillv1.TurnEvent_TtsChunk{TtsChunk: e}})
}

func (s *connectTurnSink) TtsDone(e *drillv1.TtsDone) {
	s.send(&drillv1.TurnEvent{Event: &drillv1.TurnEvent_TtsDone{TtsDone: e}})
}

func (s *connectTurnSink) Error(e *drillv1.TurnError) {
	s.send(&drillv1.TurnEvent{Event: &drillv1.TurnEvent_Error{Error: e}})
}
