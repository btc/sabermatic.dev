package transport

import (
	"sync"

	"github.com/google/uuid"
)

type (
	StateChangeEvent         struct{ State string }
	AckEvent                 struct{}
	TranscriptionResultEvent struct{ Text string }
	TimerWarningEvent        struct{ MinutesRemaining int }
	TimerOvertimeEvent       struct{}
	ReconnectPleaseEvent     struct{}
	PongEvent                struct{}
	TTSErrorEvent            struct{}
	TTSDoneEvent             struct{ MessageID uuid.UUID }
	AudioUploadFailedEvent   struct{}
	InterviewerTokenEvent    struct{ Token string }
	InterviewerDoneEvent     struct{ MessageID uuid.UUID }
	ClientErrorEvent         struct{ ClientError }
	SessionLoadedEvent       struct{ SessionLoaded }
	ReconnectStateEvent      struct{ ReconnectState }
	TTSChunkEvent            struct{ TTSChunk }
)

type Recorder struct {
	mu     sync.Mutex
	Events []any
}

func (r *Recorder) record(e any) {
	r.mu.Lock()
	r.Events = append(r.Events, e)
	r.mu.Unlock()
}

func (r *Recorder) StateChange(state string)           { r.record(StateChangeEvent{state}) }
func (r *Recorder) Ack()                                { r.record(AckEvent{}) }
func (r *Recorder) TranscriptionResult(text string)     { r.record(TranscriptionResultEvent{text}) }
func (r *Recorder) TimerWarning(minutesRemaining int)   { r.record(TimerWarningEvent{minutesRemaining}) }
func (r *Recorder) TimerOvertime()                      { r.record(TimerOvertimeEvent{}) }
func (r *Recorder) ReconnectPlease()                    { r.record(ReconnectPleaseEvent{}) }
func (r *Recorder) Pong()                               { r.record(PongEvent{}) }
func (r *Recorder) TTSError()                           { r.record(TTSErrorEvent{}) }
func (r *Recorder) TTSDone(messageID uuid.UUID)         { r.record(TTSDoneEvent{messageID}) }
func (r *Recorder) AudioUploadFailed()                  { r.record(AudioUploadFailedEvent{}) }
func (r *Recorder) InterviewerToken(token string)       { r.record(InterviewerTokenEvent{token}) }
func (r *Recorder) InterviewerDone(messageID uuid.UUID) { r.record(InterviewerDoneEvent{messageID}) }
func (r *Recorder) Error(e ClientError)                 { r.record(ClientErrorEvent{e}) }
func (r *Recorder) SessionLoaded(s SessionLoaded)       { r.record(SessionLoadedEvent{s}) }
func (r *Recorder) ReconnectState(rs ReconnectState)    { r.record(ReconnectStateEvent{rs}) }
func (r *Recorder) TTSChunk(c TTSChunk)                 { r.record(TTSChunkEvent{c}) }
