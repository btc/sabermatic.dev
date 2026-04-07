package interview

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	pgx "github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/codes"

	"github.com/btc/drill/internal/ai"
	"github.com/btc/drill/internal/backend"
	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/drilotel"
	"github.com/btc/drill/internal/interview/observer"
	"github.com/btc/drill/internal/interview/transport"
)

var tracer = drilotel.Tracer("interview")

// ConductorParams contains everything needed to construct a Conductor.
// The handler creates these after auth, validation, and WebSocket upgrade.
// Lock acquisition and session_init reading happen inside Run.
type ConductorParams struct {
	// Client is the transport layer for sending events to the connected client.
	Client transport.Client

	// RawWS is the raw WebSocket for readLoop reads and connection lifecycle.
	RawWS *websocket.Conn

	// Backend is the service layer for DB, LLM, STT, and TTS operations.
	Backend *backend.Backend

	// SessionID identifies the interview session.
	SessionID uuid.UUID

	// UserID identifies the authenticated user.
	UserID uuid.UUID
}

// Conductor owns all mutable state for one active interview session.
// A single goroutine runs the main for/select loop in Run(); the readLoop
// runs as a closure goroutine and communicates via a local msgCh channel.
// Run() blocks until the session ends.
type Conductor struct {
	// State machine (pure logic, no I/O).
	sm *StateMachine

	// Transport layer for sending typed events to the connected client.
	client transport.Client

	// Raw WebSocket for reads (readLoop closure needs Read()).
	rawWS *websocket.Conn

	// Backend service layer for DB, LLM, STT, TTS operations.
	backend *backend.Backend

	// Dedicated connection holding the Postgres advisory lock.
	lock *backend.SessionLock

	// Current per-turn observer fan-out. Atomic for cross-goroutine access.
	obs atomic.Pointer[observer.TokenFanOut]

	// Session state loaded from DB.
	sessionID     uuid.UUID
	userID        uuid.UUID
	question      db.Question
	coachBriefing *db.CoachAnalysis // nil if disabled or no analysis available
	messages      []db.Message      // in-memory transcript, appended after each persist
	sequence      int               // last message seq number
	ttsEnabled    bool
	duration      time.Duration
	model         string
	status        string // DB session status; used to skip recovery for completed/cancelled sessions

	// The client's session_init message (contains LastSeq for reconnect detection).
	initMsg WSMessage
}

// turnResult carries the outcome of a pipeline run.
type turnResult struct {
	err error
}

// drainPipeline waits for the pipeline goroutine to finish, with a timeout
// to prevent shutdown hangs if the pipeline ignores context cancellation.
func drainPipeline(ch <-chan turnResult) {
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		slog.Error("conductor: pipeline did not exit after cancel")
	}
}

// Pending action constants for deferred lifecycle operations.
const (
	actionEnd    = "end"
	actionCancel = "cancel"
)

// NewConductor constructs a Conductor from the given params.
func NewConductor(p ConductorParams) *Conductor {
	c := &Conductor{
		rawWS:     p.RawWS,
		client:    p.Client,
		backend:   p.Backend,
		sessionID: p.SessionID,
		userID:    p.UserID,
	}
	return c
}

// Run is the single entry point for the conductor. It acquires the advisory
// lock, reads session_init, owns the readLoop lifecycle, loads session state,
// sends initial messages, sets timers, and runs the main event loop.
// Run blocks until the session ends.
func (c *Conductor) Run(serverCtx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("conductor: panic recovered",
				"recover", r,
				"session_id", c.sessionID,
				"user_id", c.userID,
				"stack", string(debug.Stack()))
		}
	}()

	// Phase 1: Acquire advisory lock.
	lock, locked, err := c.backend.AcquireSessionLock(serverCtx, c.sessionID)
	if err != nil {
		slog.Error("acquire session lock", "error", err, "session_id", c.sessionID)
		c.rawWS.Close(websocket.StatusInternalError, "lock error")
		return
	}
	if !locked {
		c.rawWS.Close(websocket.StatusPolicyViolation, "session already in use")
		return
	}
	c.lock = lock

	// Phase 2: Read session_init (10s timeout).
	initCtx, initCancel := context.WithTimeout(serverCtx, 10*time.Second)
	_, data, err := c.rawWS.Read(initCtx)
	initCancel()
	if err != nil {
		slog.Error("read session_init", "error", err, "session_id", c.sessionID)
		c.close()
		return
	}
	initMsg, err := ParseWSMessage(data)
	if err != nil || initMsg.Type != "session_init" {
		slog.Error("invalid session_init", "error", err, "session_id", c.sessionID)
		c.client.Error(transport.ClientError{Code: "invalid_init", Message: "expected session_init message"})
		c.close()
		return
	}
	c.initMsg = initMsg

	// Phase 3: Main lifecycle.
	// workCtx is deliberately not derived from serverCtx. Handlers must
	// finish their current work (DB persist, LLM stream) before shutdown;
	// serverCtx.Done() is only checked between turns in the select loop.
	workCtx := context.Background()
	readCtx, readCancel := context.WithCancel(context.Background())

	msgCh := make(chan WSMessage)
	var wg sync.WaitGroup
	wg.Go(func() {
		defer close(msgCh)
		for {
			_, data, err := c.rawWS.Read(readCtx)
			if err != nil {
				return
			}
			msg, err := ParseWSMessage(data)
			if err != nil {
				c.client.Error(transport.ClientError{Code: "malformed_message", Message: err.Error()})
				continue
			}
			if msg.Type == "cancel_tts" {
				continue
			}
			select {
			case msgCh <- msg:
			case <-readCtx.Done():
				return
			}
		}
	})

	// Cleanup in correct order: cancel readLoop -> wait for exit -> close resources.
	defer func() {
		readCancel()
		wg.Wait()
		c.close()
	}()

	// Load session state from DB.
	if err := c.loadSession(workCtx); err != nil {
		slog.Error("conductor: load session", "error", err, "session_id", c.sessionID)
		c.client.Error(transport.ClientError{Code: "load_failed", Message: "failed to load session"})
		return
	}

	// Timers -- all set declaratively, unconditionally. Timers that fire
	// after the session ends are harmless (no one reads the channel).
	elapsed := time.Since(c.sm.StartedAt())
	warningTimer := time.After(warningDelay(c.duration, elapsed))
	overtimeTimer := time.After(overtimeDelay(c.duration, elapsed))
	autoEndTimer := time.After(autoEndDelay(c.duration, elapsed))
	reconnectTimer := time.After(55 * time.Minute)

	// Initial messages to client.
	if err := c.sendInitialMessage(workCtx); err != nil {
		slog.Error("conductor: initial message", "error", err, "session_id", c.sessionID)
		return
	}

	// Pipeline state — at most one pipeline goroutine runs at a time.
	var (
		turnResultCh  <-chan turnResult  // nil when no pipeline running
		cancelTurn    context.CancelFunc // non-nil when pipeline running
		pendingAction string             // "", actionEnd, or actionCancel
	)

	// Dispatch opening question as a pipeline goroutine for new sessions.
	if !c.isReconnect() && len(c.messages) == 0 {
		ch := make(chan turnResult, 1)
		turnResultCh = ch
		turnCtx, cancel := context.WithCancel(workCtx)
		cancelTurn = cancel
		go func() {
			ch <- turnResult{err: c.streamInterviewerResponse(turnCtx)}
		}()
	}

	// Re-trigger interviewer response if the last persisted message is from
	// the candidate (gap from crash or disconnect during streaming).
	if c.needsInterviewerRecovery() {
		ch := make(chan turnResult, 1)
		turnResultCh = ch
		turnCtx, cancel := context.WithCancel(workCtx)
		cancelTurn = cancel
		go func() {
			ch <- turnResult{err: c.streamInterviewerResponse(turnCtx)}
		}()
	}

	// Main loop -- event loop is never blocked by pipeline I/O.
	reconnectPending := false
	for {
		select {
		case msg, ok := <-msgCh:
			if !ok {
				// Client disconnected.
				if cancelTurn != nil {
					cancelTurn()
					drainPipeline(turnResultCh)
				}
				if pendingAction == actionEnd {
					_ = c.endSession(workCtx)
				}
				return
			}
			if serverCtx.Err() != nil {
				c.client.ReconnectPlease()
				if cancelTurn != nil {
					cancelTurn()
					drainPipeline(turnResultCh)
				}
				return
			}
			switch msg.Type {
			case "end_turn":
				if turnResultCh != nil {
					c.client.Error(transport.ClientError{Code: "turn_in_progress", Message: "a turn is already being processed"})
					continue
				}
				ch := make(chan turnResult, 1)
				turnResultCh = ch
				turnCtx, cancel := context.WithCancel(workCtx)
				cancelTurn = cancel
				capturedMsg := msg
				go func() {
					ch <- turnResult{err: c.endTurn(turnCtx, capturedMsg)}
				}()

			case "end_session":
				if turnResultCh != nil {
					pendingAction = actionEnd
					cancelTurn()
					continue
				}
				if err := c.endSession(workCtx); err != nil {
					slog.Error("conductor: end_session", "error", err, "session_id", c.sessionID)
				}
				return

			case "cancel_session":
				if turnResultCh != nil {
					pendingAction = actionCancel
					cancelTurn()
					continue
				}
				if err := c.cancelSession(serverCtx); err != nil {
					slog.Error("conductor: cancel_session", "error", err, "session_id", c.sessionID)
				}
				return

			case "ping":
				c.client.Pong()
			default:
				c.client.Error(transport.ClientError{Code: "unknown_message_type", Message: "unknown message type: " + msg.Type})
			}

		case res := <-turnResultCh:
			// Pipeline completed. Reclaim ownership of shared state.
			turnResultCh = nil
			cancelTurn = nil

			if res.err != nil {
				slog.Error("conductor: end_turn", "error", res.err, "session_id", c.sessionID)
				c.sm.ForceState(StateWaitingForInput)
				c.client.Error(transport.ClientError{Code: "turn_failed", Message: "failed to process turn, please try again"})
			}

			// Handle deferred lifecycle action.
			switch pendingAction {
			case actionEnd:
				pendingAction = ""
				if err := c.endSession(workCtx); err != nil {
					slog.Error("conductor: end_session", "error", err, "session_id", c.sessionID)
				}
				return
			case actionCancel:
				pendingAction = ""
				if err := c.cancelSession(serverCtx); err != nil {
					slog.Error("conductor: cancel_session", "error", err, "session_id", c.sessionID)
				}
				return
			}

			if reconnectPending {
				c.client.ReconnectPlease()
				return
			}

		case <-warningTimer:
			c.client.TimerWarning(warningMinutes(c.duration))

		case <-overtimeTimer:
			c.client.TimerOvertime()

		case <-autoEndTimer:
			slog.Info("conductor: auto-ending session", "session_id", c.sessionID)
			if turnResultCh != nil {
				pendingAction = actionEnd
				cancelTurn()
				continue
			}
			if err := c.endSession(workCtx); err != nil {
				slog.Error("conductor: auto-end", "error", err, "session_id", c.sessionID)
			}
			return

		case <-reconnectTimer:
			reconnectPending = true

		case <-serverCtx.Done():
			c.client.ReconnectPlease()
			if cancelTurn != nil {
				cancelTurn()
				drainPipeline(turnResultCh)
			}
			if pendingAction == actionEnd {
				_ = c.endSession(workCtx)
			}
			return
		}
	}
}

// loadSession loads the session, question, and existing messages from DB.
func (c *Conductor) loadSession(ctx context.Context) (err error) {
	ctx, span := tracer.Start(ctx, "Conductor.loadSession")
	defer func() { drilotel.End(span, err) }()

	row, err := c.backend.GetSession(ctx, c.sessionID)
	if err != nil {
		return err
	}

	msgs, err := c.backend.GetMessagesBySession(ctx, c.sessionID)
	if err != nil {
		return err
	}

	c.question = db.Question{
		Title:  row.QuestionTitle,
		Prompt: row.QuestionPrompt,
	}
	c.messages = msgs
	c.ttsEnabled = row.ConfigTtsEnabled
	c.duration = time.Duration(row.ConfigDurationMinutes) * time.Minute
	c.model = c.backend.Config().LLM.InterviewerModel
	c.status = row.Status
	c.sm = NewStateMachine(StateWaitingForInput)
	c.sm.SetStartedAt(row.StartedAt)

	if len(msgs) > 0 {
		c.sequence = int(msgs[len(msgs)-1].Seq)
	}

	// Load coach briefing if enabled.
	if row.ConfigCoachBriefing {
		q := db.New(c.backend.Pool())
		ca, err := q.GetLatestCoachAnalysis(ctx, row.UserID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("get coach analysis: %w", err)
		}
		if err == nil {
			c.coachBriefing = &ca
		}
	}

	return nil
}

// sendInitialMessage sends the appropriate first message to the client:
// reconnect_state for reconnections, session_loaded + opening LLM stream for new connections.
func (c *Conductor) sendInitialMessage(ctx context.Context) (err error) {
	ctx, span := tracer.Start(ctx, "Conductor.sendInitialMessage")
	defer func() { drilotel.End(span, err) }()

	if c.isReconnect() {
		afterSeq := *c.initMsg.LastSeq // isReconnect already verified non-nil
		c.client.ReconnectState(transport.ReconnectState{LastSeq: afterSeq, Messages: c.messages})
		return nil
	}
	c.client.SessionLoaded(transport.SessionLoaded{SessionID: c.sessionID, Question: c.question, DurationMin: int(c.duration.Minutes()), TTSEnabled: c.ttsEnabled})
	if len(c.messages) > 0 {
		// Page refresh of existing session — send all messages, skip opening question.
		c.client.ReconnectState(transport.ReconnectState{LastSeq: 0, Messages: c.messages})
		c.client.StateChange(string(StateWaitingForInput))
		return nil
	}
	// New session: opening question is dispatched by the caller as a
	// pipeline goroutine so the event loop is responsive during streaming.
	return nil
}

// endTurn processes a candidate's turn (text or voice).
func (c *Conductor) endTurn(ctx context.Context, msg WSMessage) (err error) {
	// Extract client trace context so the server span becomes a child of the
	// browser's turn.submit span, producing one end-to-end flame graph per turn.
	ctx = drilotel.ExtractTraceparent(ctx, msg.Traceparent())
	ctx, span := tracer.Start(ctx, "Conductor.endTurn")
	defer func() { drilotel.End(span, err) }()

	var candidateContent string
	messageID := uuid.New()

	if msg.InputMethod == "voice" {
		// Validate audio.
		if len(msg.Audio) == 0 {
			c.client.Error(transport.ClientError{Code: "audio_validation_failed", Message: "no audio data provided"})
			return nil
		}

		// Transition to Transcribing.
		// WS sends use context.Background() so that turn-context cancellation does
		// not close the underlying WebSocket connection.
		if err := c.sm.Transition(StateTranscribing); err != nil {
			c.client.Error(transport.ClientError{Code: "invalid_state_transition", Message: err.Error()})
			return nil
		}
		c.client.StateChange(string(StateTranscribing))

		// Fire upload goroutine — does not block transcription.
		go func() {
			uploadCtx, uploadSpan := tracer.Start(context.WithoutCancel(ctx), "Conductor.uploadAudio")
			defer uploadSpan.End()

			key := fmt.Sprintf("%s/%s.%s", c.sessionID, messageID, msg.AudioExt())
			url, uploadErr := c.backend.StoreAudio(uploadCtx, key, msg.Audio, msg.AudioMIME)
			if uploadErr != nil {
				uploadSpan.RecordError(uploadErr)
				uploadSpan.SetStatus(codes.Error, "audio upload failed")
				slog.Error("conductor: audio upload failed",
					"error", uploadErr,
					"session_id", c.sessionID,
					"message_id", messageID)
				c.client.AudioUploadFailed()
				return
			}
			if setErr := c.backend.SetAudioURL(uploadCtx, messageID, url); setErr != nil {
				uploadSpan.RecordError(setErr)
				slog.Error("conductor: failed to set audio_url",
					"error", setErr,
					"session_id", c.sessionID,
					"message_id", messageID)
			}
		}()

		// STT — single retry on transient failure.
		text, err := c.backend.Transcribe(ctx, msg.Audio, msg.AudioExt())
		if err != nil {
			slog.Warn("conductor: transcription failed, retrying",
				"error", err, "session_id", c.sessionID)
			time.Sleep(500 * time.Millisecond)
			text, err = c.backend.Transcribe(ctx, msg.Audio, msg.AudioExt())
		}
		if err != nil {
			slog.Error("conductor: transcription failed after retry",
				"error", err, "session_id", c.sessionID)
			return fmt.Errorf("transcription: %w", err)
		}
		if strings.TrimSpace(text) == "" {
			return fmt.Errorf("transcription returned empty text")
		}

		c.client.TranscriptionResult(text)
		candidateContent = text
	} else {
		// Text input -- reject empty content.
		if strings.TrimSpace(msg.Content) == "" {
			c.client.Error(transport.ClientError{Code: "empty_content", Message: "text content cannot be empty"})
			return nil
		}
		candidateContent = msg.Content
	}

	// Transition to ProcessingInput.
	if err := c.sm.Transition(StateProcessingInput); err != nil {
		c.client.Error(transport.ClientError{Code: "invalid_state_transition", Message: err.Error()})
		return nil
	}
	c.client.StateChange(string(StateProcessingInput))

	// Persist candidate message.
	candidateMsg, err := c.persistMessage(context.WithoutCancel(ctx), messageID, "candidate", candidateContent, msg.InputMethod)
	if err != nil {
		slog.Error("conductor: persist candidate message", "error", err, "session_id", c.sessionID)
		return fmt.Errorf("persist candidate message: %w", err)
	}
	c.messages = append(c.messages, candidateMsg)

	// Stream interviewer response.
	return c.streamInterviewerResponse(ctx)
}

// streamInterviewerResponse builds a prompt, streams the LLM, and persists the result.
func (c *Conductor) streamInterviewerResponse(ctx context.Context) (err error) {
	ctx, span := tracer.Start(ctx, "Conductor.streamInterviewerResponse")
	defer func() { drilotel.End(span, err) }()

	// Transition to InterviewerSpeaking.
	// WS sends use context.Background() so that turn-context cancellation does
	// not close the underlying WebSocket connection (coder/websocket registers
	// context.AfterFunc to close the conn when the write ctx is cancelled).
	if err := c.sm.Transition(StateInterviewerSpeaking); err != nil {
		c.client.Error(transport.ClientError{Code: "invalid_state_transition", Message: err.Error()})
		return nil
	}
	c.client.StateChange(string(StateInterviewerSpeaking))

	messageID := uuid.New()

	// Build prompt.
	elapsed := time.Since(c.sm.StartedAt())
	remaining := max(0, c.duration-elapsed)

	system, promptMsgs := NewInterviewerPrompt().
		WithSystemInstructions().
		WithQuestion(c.question).
		WithCoachBriefing(c.coachBriefing).
		WithTimeContext(elapsed, remaining).
		WithTranscript(c.messages).
		Build()

	// Create observer fan-out.
	accumulator := observer.NewMessageAccumulator()
	observers := []observer.TokenObserver{
		transport.NewTokenWriter(c.client, messageID),
		accumulator,
	}
	if c.ttsEnabled {
		synth, err := c.backend.Synthesizer()
		if err == nil && synth != nil {
			sink := transport.NewTTSSink(c.client, messageID)
			observers = append(observers, observer.NewTTSAccumulator(observer.TTSAccumulatorParams{
				Sink:            sink,
				Synth:           synth,
				SentenceTimeout: c.backend.Config().Speech.TTSSentenceTimeout,
			}))
		}
	}
	fanOut := observer.NewTokenFanOut(observers...)
	c.obs.Store(fanOut)

	// Stream LLM.
	stream, err := c.backend.StreamLLM(ctx, ai.StreamParams{
		Model:     c.model,
		System:    system,
		Messages:  promptMsgs,
		UserID:    c.userID,
		Role:      "interviewer",
		SessionID: c.sessionID,
	})
	if err != nil {
		fanOut.OnError(err)
		return fmt.Errorf("start llm stream: %w", err)
	}

	// Iterate tokens through observer.
	for {
		token, err := stream.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			fanOut.OnError(err)
			slog.Error("conductor: stream token", "error", err, "session_id", c.sessionID)
			break
		}
		fanOut.OnToken(token)
	}

	fullText := accumulator.Text()
	fanOut.OnDone(fullText)

	// Clean up the fan-out's TTS context in the background.
	go func() {
		fanOut.Close()
	}()

	// Persist interviewer message and LLM call atomically.
	persistCtx := context.WithoutCancel(ctx)
	c.sequence++
	interviewerMsg, err := c.backend.PersistInterviewerTurn(persistCtx, stream, backend.PersistMessageParams{
		MessageID:   messageID,
		SessionID:   c.sessionID,
		Seq:         c.sequence,
		Role:        "interviewer",
		Content:     fullText,
		InputMethod: "",
	})
	if err != nil {
		c.sequence-- // rollback sequence on insert failure
		return fmt.Errorf("persist interviewer turn: %w", err)
	}

	c.messages = append(c.messages, interviewerMsg)

	// Transition to WaitingForInput.
	if err := c.sm.Transition(StateWaitingForInput); err != nil {
		return fmt.Errorf("transition to waiting: %w", err)
	}
	c.client.StateChange(string(StateWaitingForInput))

	return nil
}

// endSession transitions to Ending, persists status + enqueues evaluation atomically, then Ended.
func (c *Conductor) endSession(ctx context.Context) (err error) {
	ctx, span := tracer.Start(ctx, "Conductor.endSession")
	defer func() { drilotel.End(span, err) }()

	if err := c.sm.Transition(StateEnding); err != nil {
		c.client.Error(transport.ClientError{Code: "invalid_state_transition", Message: err.Error()})
		return nil
	}

	if err := c.backend.CompleteSession(ctx, c.sessionID, c.sm.TurnCount()); err != nil {
		return fmt.Errorf("complete session: %w", err)
	}

	if err := c.sm.Transition(StateEnded); err != nil {
		return fmt.Errorf("transition to ended: %w", err)
	}

	c.client.Ack()
	return nil
}

// cancelSession ends the session early without evaluation. Archived + refunded.
func (c *Conductor) cancelSession(ctx context.Context) (err error) {
	ctx, span := tracer.Start(ctx, "Conductor.cancelSession")
	defer func() { drilotel.End(span, err) }()

	if err := c.sm.Transition(StateEnding); err != nil {
		c.client.Error(transport.ClientError{Code: "invalid_state_transition", Message: err.Error()})
		return nil
	}

	if err := c.backend.CancelSession(ctx, c.sessionID, c.sm.TurnCount()); err != nil {
		return fmt.Errorf("cancel session: %w", err)
	}

	if err := c.sm.Transition(StateEnded); err != nil {
		return fmt.Errorf("transition to ended: %w", err)
	}

	c.client.Ack()
	return nil
}

// persistMessage inserts a message into the DB and returns it.
func (c *Conductor) persistMessage(ctx context.Context, id uuid.UUID, role, content, inputMethod string) (_ db.Message, err error) {
	ctx, span := tracer.Start(ctx, "Conductor.persistMessage")
	defer func() { drilotel.End(span, err) }()

	c.sequence++

	msg, err := c.backend.PersistMessage(ctx, backend.PersistMessageParams{
		MessageID:   id,
		SessionID:   c.sessionID,
		Seq:         c.sequence,
		Role:        role,
		Content:     content,
		InputMethod: inputMethod,
	})
	if err != nil {
		c.sequence-- // rollback sequence on failure
		return db.Message{}, fmt.Errorf("insert message: %w", err)
	}
	return msg, nil
}

// isReconnect returns true if the client sent a last_seq in session_init.
func (c *Conductor) isReconnect() bool {
	return c.initMsg.LastSeq != nil
}

// needsInterviewerRecovery returns true when the last persisted message is
// from the candidate and the session is still active — meaning the
// interviewer's response was lost (crash, disconnect during streaming).
// Fires for both reconnects (last_seq present) and page refreshes (last_seq nil).
func (c *Conductor) needsInterviewerRecovery() bool {
	if len(c.messages) == 0 || c.status != "active" {
		return false
	}
	return c.messages[len(c.messages)-1].Role == "candidate"
}

// close closes the WebSocket and releases the advisory lock.
func (c *Conductor) close() {
	c.rawWS.Close(websocket.StatusNormalClosure, "session ended")
	if err := c.lock.Release(); err != nil {
		slog.Error("conductor: release session lock", "error", err, "session_id", c.sessionID)
	}
}
