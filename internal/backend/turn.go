package backend

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/codes"

	"github.com/btc/drill/internal/ai"
	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/drilotel"
	"github.com/btc/drill/internal/events"
	"github.com/btc/drill/internal/interview/observer"
	"github.com/btc/drill/internal/interview/prompt"
	drillv1 "github.com/btc/drill/internal/pb/drill/v1"
)

// validAudioMIME is the set of audio MIME types accepted for voice input.
var validAudioMIME = map[string]string{
	"audio/webm": "webm",
	"audio/wav":  "wav",
	"audio/ogg":  "ogg",
	"audio/mpeg": "mp3",
	"audio/mp4":  "m4a",
}

// AcquireGeneratingStatus atomically transitions a session from 'active' to
// 'generating'. Returns true if the status was acquired, false if the session
// is not active or is already generating.
func (b *Backend) AcquireGeneratingStatus(ctx context.Context, sessionID uuid.UUID) (bool, error) {
	_, err := db.New(b.pool).AcquireGeneratingStatus(ctx, sessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("acquire generating status: %w", err)
	}
	return true, nil
}

// ReleaseGeneratingStatus reverts a session from 'generating' back to 'active'.
func (b *Backend) ReleaseGeneratingStatus(ctx context.Context, sessionID uuid.UUID) error {
	return db.New(b.pool).ReleaseGeneratingStatus(ctx, sessionID)
}

// ExecuteTurn processes a single candidate turn: transcription (voice),
// candidate message persistence, LLM response streaming, and interviewer
// message persistence. It is stateless — all state is loaded from the DB
// and the status column guards concurrent access.
//
// The sink receives events as they occur. Sink errors are silently dropped
// so the pipeline runs to completion regardless of client disconnects.
func (b *Backend) ExecuteTurn(ctx context.Context, p *drillv1.SubmitTurnRequest, sink TurnEventSink) (err error) {
	ctx = drilotel.ExtractTraceparent(ctx, p.GetTraceparent())
	ctx, span := tracer.Start(ctx, "Backend.ExecuteTurn")
	defer func() { drilotel.End(span, err) }()

	sessionID, err := uuid.Parse(p.GetSessionId())
	if err != nil {
		return fmt.Errorf("parse session_id: %w", err)
	}

	// Step 1: Acquire generating status (atomic guard).
	acquired, err := b.AcquireGeneratingStatus(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("acquire generating status: %w", err)
	}
	if !acquired {
		return ErrTurnNotAcquired
	}

	// Step 2: Ensure status reverts on ALL exit paths.
	var released atomic.Bool
	defer func() {
		if released.Load() {
			return
		}
		releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if releaseErr := b.ReleaseGeneratingStatus(releaseCtx, sessionID); releaseErr != nil {
			slog.Error("release generating status", "error", releaseErr, "session_id", sessionID)
		}
	}()

	// Step 3: Load session + question + messages.
	q := db.New(b.pool)
	session, err := q.GetSessionForTurn(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("get session for turn: %w", err)
	}

	messages, err := q.GetMessagesBySession(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("get messages: %w", err)
	}

	maxSeq, err := q.GetMaxSeqForSession(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("get max seq: %w", err)
	}
	sequence := int(maxSeq)

	question := db.Question{
		Title:  session.QuestionTitle,
		Prompt: session.QuestionPrompt,
	}

	// Step 4: Load coach briefing if enabled.
	var coachBriefing *db.CoachAnalysis
	if session.ConfigCoachBriefing {
		ca, caErr := q.GetLatestCoachAnalysis(ctx, session.UserID)
		if caErr != nil && !errors.Is(caErr, pgx.ErrNoRows) {
			return fmt.Errorf("get coach analysis: %w", caErr)
		}
		if caErr == nil {
			coachBriefing = &ca
		}
	}

	// Step 5: Determine turn type.
	isOpeningQuestion := len(messages) == 0 && isTextInput(p) && getTextContent(p) == ""
	isCrashRecovery := len(messages) > 0 && messages[len(messages)-1].Role == "candidate"

	if isOpeningQuestion || isCrashRecovery {
		// Skip candidate processing, go directly to interviewer response.
		// Use an uncancellable context so the pipeline survives client disconnects.
		pipelineCtx := context.WithoutCancel(ctx)
		return b.streamInterviewerResponse(pipelineCtx, streamParams{
			sessionID:     sessionID,
			userID:        session.UserID,
			question:      question,
			coachBriefing: coachBriefing,
			messages:      messages,
			sequence:      sequence,
			ttsEnabled:    session.ConfigTtsEnabled,
			startedAt:     session.StartedAt,
			duration:      time.Duration(session.ConfigDurationMinutes) * time.Minute,
			sink:          sink,
			released:      &released,
		})
	}

	// Step 6: Process candidate input.
	var candidateContent string
	messageID := uuid.New()
	inputMethod := "text"

	switch input := p.GetInput().(type) {
	case *drillv1.SubmitTurnRequest_VoiceInput:
		voice := input.VoiceInput
		inputMethod = "voice"

		// Validate MIME type. Strip codec params (e.g. "audio/webm;codecs=opus" → "audio/webm").
		baseMIME, _, _ := strings.Cut(voice.GetAudioMimeType(), ";")
		ext, ok := validAudioMIME[baseMIME]
		if !ok {
			sink.Error(&drillv1.TurnError{Code: "invalid_audio_mime", Message: "unsupported audio MIME type: " + voice.GetAudioMimeType()})
			return nil
		}

		if len(voice.GetAudio()) == 0 {
			sink.Error(&drillv1.TurnError{Code: "audio_validation_failed", Message: "no audio data provided"})
			return nil
		}

		// Fire background audio upload goroutine.
		go func() {
			uploadCtx, uploadSpan := tracer.Start(context.WithoutCancel(ctx), "Backend.ExecuteTurn.uploadAudio")
			defer uploadSpan.End()

			audioData := voice.GetAudio()
			hash := sha256.Sum256(audioData)
			key := path.Join(
				sessionID.String(),
				messageID.String(),
				hex.EncodeToString(hash[:8])+"."+ext,
			)
			url, uploadErr := b.StoreAudio(uploadCtx, key, audioData, voice.GetAudioMimeType())
			if uploadErr != nil {
				uploadSpan.RecordError(uploadErr)
				uploadSpan.SetStatus(codes.Error, "audio upload failed")
				slog.Error("turn: audio upload failed",
					"error", uploadErr,
					"session_id", sessionID,
					"message_id", messageID)
				return
			}
			if setErr := b.SetAudioURL(uploadCtx, messageID, url); setErr != nil {
				uploadSpan.RecordError(setErr)
				slog.Error("turn: failed to set audio_url",
					"error", setErr,
					"session_id", sessionID,
					"message_id", messageID)
			}
		}()

		// STT with single retry.
		text, sttErr := b.Transcribe(ctx, voice.GetAudio(), ext)
		if sttErr != nil {
			slog.Warn("turn: transcription failed, retrying",
				"error", sttErr, "session_id", sessionID)
			time.Sleep(500 * time.Millisecond)
			text, sttErr = b.Transcribe(ctx, voice.GetAudio(), ext)
		}
		if sttErr != nil {
			slog.Error("turn: transcription failed after retry",
				"error", sttErr, "session_id", sessionID)
			sink.Error(&drillv1.TurnError{Code: "transcription_failed", Message: "speech transcription failed"})
			return nil
		}
		if strings.TrimSpace(text) == "" {
			sink.Error(&drillv1.TurnError{Code: "empty_transcription", Message: "No speech detected. Please try again."})
			return nil
		}

		sink.TranscriptionResult(&drillv1.TranscriptionResult{Text: text})
		candidateContent = text

	case *drillv1.SubmitTurnRequest_TextInput:
		content := input.TextInput.GetContent()
		if strings.TrimSpace(content) == "" {
			sink.Error(&drillv1.TurnError{Code: "empty_content", Message: "text content cannot be empty"})
			return nil
		}
		candidateContent = content

	default:
		sink.Error(&drillv1.TurnError{Code: "invalid_input", Message: "no input provided"})
		return nil
	}

	// Step 7: Persist candidate message.
	// Count existing candidate messages before appending to detect first message.
	candidateCount := 0
	for _, m := range messages {
		if m.Role == "candidate" {
			candidateCount++
		}
	}

	sequence++
	persistCtx := context.WithoutCancel(ctx)
	candidateMsg, err := b.PersistMessage(persistCtx, PersistMessageParams{
		MessageID:   messageID,
		SessionID:   sessionID,
		Seq:         sequence,
		Role:        "candidate",
		Content:     candidateContent,
		InputMethod: inputMethod,
	})
	if err != nil {
		sink.Error(&drillv1.TurnError{Code: "persist_failed", Message: "failed to save your response"})
		return nil
	}
	messages = append(messages, candidateMsg)

	if candidateCount == 0 {
		b.events.Emit(
			events.WithSessionID(ctx, sessionID.String()),
			"first_message_sent",
		)
	}

	// Step 8: Stream interviewer response.
	// Use an uncancellable context so the pipeline survives client disconnects.
	// The initial DB operations above (acquire status, load session) respect
	// cancellation, but the pipeline (LLM stream, persist) must not be aborted
	// by a client hangup.
	pipelineCtx := context.WithoutCancel(ctx)
	return b.streamInterviewerResponse(pipelineCtx, streamParams{
		sessionID:     sessionID,
		userID:        session.UserID,
		question:      question,
		coachBriefing: coachBriefing,
		messages:      messages,
		sequence:      sequence,
		ttsEnabled:    session.ConfigTtsEnabled,
		startedAt:     session.StartedAt,
		duration:      time.Duration(session.ConfigDurationMinutes) * time.Minute,
		sink:          sink,
		released:      &released,
	})
}

// streamParams bundles the data needed by streamInterviewerResponse.
type streamParams struct {
	sessionID     uuid.UUID
	userID        uuid.UUID
	question      db.Question
	coachBriefing *db.CoachAnalysis
	messages      []db.Message
	sequence      int
	ttsEnabled    bool
	startedAt     time.Time
	duration      time.Duration
	sink          TurnEventSink
	released      *atomic.Bool // set true after release to prevent double-release in defer
}

// streamInterviewerResponse builds a prompt, streams the LLM response through
// a fan-out of observers, persists the interviewer message, and signals done.
func (b *Backend) streamInterviewerResponse(ctx context.Context, sp streamParams) (err error) {
	ctx, span := tracer.Start(ctx, "Backend.streamInterviewerResponse")
	defer func() { drilotel.End(span, err) }()

	messageID := uuid.New()

	// Build prompt.
	elapsed := time.Since(sp.startedAt)
	remaining := max(0, sp.duration-elapsed)

	system, promptMsgs := prompt.NewInterviewerPrompt().
		WithSystemInstructions().
		WithQuestion(sp.question).
		WithCoachBriefing(sp.coachBriefing).
		WithTimeContext(elapsed, remaining).
		WithTranscript(sp.messages).
		Build()

	// The trailing candidate message, if any: the answer this turn responds
	// to. It is absent on the opening turn.
	var pendingCandidate *db.Message
	if n := len(sp.messages); n > 0 && sp.messages[n-1].Role == "candidate" {
		pendingCandidate = &sp.messages[n-1]
	}

	// Create observer fan-out.
	accumulator := observer.NewMessageAccumulator()
	observers := []observer.TokenObserver{
		newSinkTokenWriter(sp.sink, pendingCandidate == nil),
		accumulator,
	}
	if sp.ttsEnabled {
		synth, synthErr := b.Synthesizer()
		if synthErr == nil && synth != nil {
			ttsSink := newSinkTTSAdapter(sp.sink, messageID)
			observers = append(observers, observer.NewTTSAccumulator(observer.TTSAccumulatorParams{
				Sink:            ttsSink,
				Synth:           synth,
				SentenceTimeout: b.cfg.Speech.TTSSentenceTimeout,
			}))
		}
	}
	fanOut := observer.NewTokenFanOut(observers...)

	// Stream LLM.
	model := b.cfg.LLM.InterviewerModel
	stream, err := b.StreamLLM(ctx, ai.StreamParams{
		Model:     model,
		System:    system,
		Messages:  promptMsgs,
		MaxTokens: b.cfg.LLM.InterviewerMaxTokens,
		Effort:    b.cfg.LLM.InterviewerEffort,
		UserID:    sp.userID,
		Role:      "interviewer",
		SessionID: sp.sessionID,
	})
	if err != nil {
		fanOut.OnError(err)
		fanOut.Close()
		// Client already received TurnError via fanOut.OnError; return nil
		// so the RPC handler closes the stream cleanly without a second error.
		return nil
	}

	// Iterate tokens through observer fan-out.
	var streamErr error
	for {
		token, tokenErr := stream.Next()
		if tokenErr == io.EOF {
			break
		}
		if tokenErr != nil {
			streamErr = tokenErr
			fanOut.OnError(tokenErr)
			slog.Error("turn: stream token", "error", tokenErr, "session_id", sp.sessionID)
			break
		}
		fanOut.OnToken(token)
	}

	// If the LLM stream errored (including a refusal or a max_tokens stop),
	// do NOT persist the partial message or send InterviewerDone. The client
	// already received a TurnError via fan-out. Close synchronously so TTS
	// resources are cleaned up before we return. The LLM call itself is still
	// logged (via the pool, outside any transaction) so its cost is recorded,
	// and closing releases the HTTP response body.
	if streamErr != nil {
		fanOut.Close()
		// ctx is already uncancellable (callers pass pipelineCtx); the
		// WithoutCancel guards against a future caller passing a cancellable
		// context, which would skip logging the billed call.
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if closeErr := stream.Close(closeCtx); closeErr != nil {
			slog.Warn("persist llm_call record after stream error", "error", closeErr, "session_id", sp.sessionID)
		}
		b.dropRejectedCandidate(closeCtx, sp.sessionID, pendingCandidate, streamErr)
		return nil
	}

	fullText := accumulator.Text()
	fanOut.OnDone(fullText)

	// Close the fan-out synchronously. This waits for TTS accumulator to flush
	// remaining sentences — all TTS chunks must be sent before InterviewerDone.
	fanOut.Close()

	// Persist interviewer message and LLM call atomically.
	sp.sequence++
	_, err = b.PersistInterviewerTurn(ctx, stream, PersistMessageParams{
		MessageID:   messageID,
		SessionID:   sp.sessionID,
		Seq:         sp.sequence,
		Role:        "interviewer",
		Content:     fullText,
		InputMethod: "",
	})
	if err != nil {
		sp.sink.Error(&drillv1.TurnError{Code: "persist_failed", Message: "failed to save interviewer response"})
		return nil
	}

	// Release generating status before signaling done, so the client can
	// immediately submit the next turn.
	releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if releaseErr := b.ReleaseGeneratingStatus(releaseCtx, sp.sessionID); releaseErr != nil {
		slog.Error("release generating status after persist", "error", releaseErr, "session_id", sp.sessionID)
	}
	sp.released.Store(true)

	sp.sink.InterviewerDone(&drillv1.InterviewerDone{MessageId: messageID.String()})

	return nil
}

// dropRejectedCandidate deletes the candidate message a refused or
// max_tokens-truncated turn responded to.
//
// The candidate message is saved before the interviewer streams. If it stayed
// as the last message, the next SubmitTurn would take the crash-recovery
// path, ignore the new input, and resend the transcript that was just
// refused. Deleting it lets the next submission go through the normal input
// path, so a rephrased answer replaces the rejected one. For other stream
// errors the message is kept, and crash recovery retries the same transcript.
func (b *Backend) dropRejectedCandidate(ctx context.Context, sessionID uuid.UUID, candidate *db.Message, streamErr error) {
	if candidate == nil || !(ai.IsRefusal(streamErr) || errors.Is(streamErr, ai.ErrMaxTokens)) {
		return
	}
	if err := b.DeleteMessage(ctx, sessionID, candidate.ID); err != nil {
		slog.Error("turn: delete candidate message after refusal or truncation",
			"error", err, "session_id", sessionID, "message_id", candidate.ID)
	}
}

// ---------------------------------------------------------------------------
// Sink adapters: bridge observer interfaces to TurnEventSink proto types
// ---------------------------------------------------------------------------

// sinkTokenWriter implements observer.TokenObserver and routes tokens to the
// TurnEventSink.
type sinkTokenWriter struct {
	sink    TurnEventSink
	opening bool // the turn has no candidate answer (opening question)
}

func newSinkTokenWriter(sink TurnEventSink, opening bool) *sinkTokenWriter {
	return &sinkTokenWriter{sink: sink, opening: opening}
}

func (w *sinkTokenWriter) OnToken(token string) {
	w.sink.InterviewerToken(&drillv1.InterviewerToken{Token: token})
}

func (w *sinkTokenWriter) OnDone(_ string) {
	// InterviewerDone is sent by streamInterviewerResponse after DB persist,
	// not here.
}

func (w *sinkTokenWriter) OnError(err error) {
	w.sink.Error(turnErrorForLLM(err, w.opening))
}

// turnErrorForLLM maps an interviewer stream error to the TurnError the client
// shows. Refusals and max_tokens stops get their own codes and a message
// written for the candidate; other errors keep the raw message. opening is
// true when the turn had no candidate answer. Otherwise the answer has been
// deleted (see dropRejectedCandidate), and the message says so.
func turnErrorForLLM(err error, opening bool) *drillv1.TurnError {
	switch {
	case ai.IsRefusal(err) && opening:
		return &drillv1.TurnError{
			Code:    "llm_refusal",
			Message: "The interviewer couldn't start this question. Try again, or pick a different question.",
		}
	case ai.IsRefusal(err):
		return &drillv1.TurnError{
			Code:    "llm_refusal",
			Message: "The interviewer couldn't respond to that answer, so it wasn't saved. Try rephrasing it and submitting again.",
		}
	case errors.Is(err, ai.ErrMaxTokens) && opening:
		return &drillv1.TurnError{
			Code:    "llm_truncated",
			Message: "The interviewer's response was cut off. Please try again.",
		}
	case errors.Is(err, ai.ErrMaxTokens):
		return &drillv1.TurnError{
			Code:    "llm_truncated",
			Message: "The interviewer's response was cut off, so your answer wasn't saved. Please submit it again.",
		}
	default:
		return &drillv1.TurnError{Code: "llm_stream_error", Message: err.Error()}
	}
}

// sinkTTSAdapter implements observer.TTSSink and routes TTS audio to the
// TurnEventSink.
type sinkTTSAdapter struct {
	sink      TurnEventSink
	messageID uuid.UUID
	seq       atomic.Int32
}

func newSinkTTSAdapter(sink TurnEventSink, messageID uuid.UUID) *sinkTTSAdapter {
	return &sinkTTSAdapter{sink: sink, messageID: messageID}
}

func (a *sinkTTSAdapter) HandleAudio(data []byte) {
	seq := a.seq.Add(1) - 1 // first call returns 0
	a.sink.TtsChunk(&drillv1.TtsChunk{
		Data:      data,
		MessageId: a.messageID.String(),
		Seq:       seq,
	})
}

func (a *sinkTTSAdapter) HandleTTSDone() {
	a.sink.TtsDone(&drillv1.TtsDone{MessageId: a.messageID.String()})
}

func (a *sinkTTSAdapter) HandleTTSError() {
	a.sink.Error(&drillv1.TurnError{Code: "tts_error", Message: "text-to-speech synthesis failed"})
}

// ---------------------------------------------------------------------------
// Input helpers
// ---------------------------------------------------------------------------

// isTextInput returns true if the request contains a text input (including
// the synthetic empty-content input used for opening questions).
func isTextInput(p *drillv1.SubmitTurnRequest) bool {
	_, ok := p.GetInput().(*drillv1.SubmitTurnRequest_TextInput)
	return ok
}

// getTextContent returns the text content, or "" if the input is not text.
func getTextContent(p *drillv1.SubmitTurnRequest) string {
	if ti, ok := p.GetInput().(*drillv1.SubmitTurnRequest_TextInput); ok {
		return ti.TextInput.GetContent()
	}
	return ""
}
