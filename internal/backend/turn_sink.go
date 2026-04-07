package backend

import drillv1 "github.com/btc/drill/internal/pb/drill/v1"

// TurnEventSink receives events during turn execution and delivers them
// to the client. Implementations must silently drop writes that fail
// due to client disconnect — the pipeline must not abort on sink errors.
type TurnEventSink interface {
	TranscriptionResult(e *drillv1.TranscriptionResult)
	InterviewerToken(e *drillv1.InterviewerToken)
	InterviewerDone(e *drillv1.InterviewerDone)
	TtsChunk(e *drillv1.TtsChunk)
	TtsDone(e *drillv1.TtsDone)
	Error(e *drillv1.TurnError)
}
