package interview

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
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
)

var tracer = drilotel.Tracer("interview")

// ConductorParams contains everything needed to construct a Conductor.
// The handler creates these after auth, validation, and WebSocket upgrade.
// Lock acquisition and session_init reading happen inside Run.
type ConductorParams struct {
	// WS is the upgraded WebSocket connection. The conductor wraps it
	// in a WSConn for writes and uses rawWS directly for readLoop reads.
	WS *websocket.Conn

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

	// WebSocket write interface. Thread-safe (coder/websocket).
	ws observer.WSConn

	// Raw WebSocket for reads (readLoop closure needs Read()).
	rawWS *websocket.Conn

	// Backend service layer for DB, LLM, STT, TTS operations.
	backend *backend.Backend

	// Dedicated connection holding the Postgres advisory lock.
	lock *backend.SessionLock

	// Current per-turn observer fan-out. Atomic for cross-goroutine access
	// (readLoop reads for cancel_tts, conductor goroutine writes).
	// Always non-nil -- observer.Noop between turns.
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

	// The client's session_init message (contains LastSeq for reconnect detection).
	initMsg WSMessage
}

// ttsSink is created per turn, so seq and messageID are scoped to one
// interviewer response. HandleTTSError may be called concurrently from
// the conductor goroutine (OnToken buffer-full) and the TTS goroutine;
// SendJSON is thread-safe, so no additional synchronization is needed.
// seq is only incremented by HandleAudio (TTS goroutine), never by
// HandleTTSError, so there is no data race on the counter.
type ttsSink struct {
	ws        observer.WSConn
	messageID uuid.UUID
	seq       int
}

func (s *ttsSink) HandleAudio(data []byte) {
	_ = s.ws.SendJSON(context.Background(), map[string]any{
		"type":       "tts_chunk",
		"data":       base64.StdEncoding.EncodeToString(data),
		"message_id": s.messageID.String(),
		"seq":        s.seq,
	})
	s.seq++
}

func (s *ttsSink) HandleTTSDone() {
	_ = s.ws.SendJSON(context.Background(), map[string]any{
		"type":       "tts_done",
		"message_id": s.messageID.String(),
	})
}

func (s *ttsSink) HandleTTSError() {
	_ = s.ws.SendJSON(context.Background(), map[string]any{
		"type": "tts_error",
	})
}

// NewConductor constructs a Conductor from the given params.
func NewConductor(p ConductorParams) *Conductor {
	c := &Conductor{
		rawWS:     p.WS,
		ws:        &Conn{WS: p.WS},
		backend:   p.Backend,
		sessionID: p.SessionID,
		userID:    p.UserID,
	}
	c.obs.Store(observer.Noop)
	return c
}

// Run is the single entry point for the conductor. It acquires the advisory
// lock, reads session_init, owns the readLoop lifecycle, loads session state,
// sends initial messages, sets timers, and runs the main event loop.
// Run blocks until the session ends.
func (c *Conductor) Run(serverCtx context.Context) {
	// Phase 1: Acquire advisory lock.
	lock, locked, err := c.backend.AcquireSessionLock(serverCtx, c.sessionID)
	if err != nil {
		slog.Error("acquire session lock", "error", err, "session_id", c.sessionID)
		c.ws.Close(websocket.StatusInternalError, "lock error")
		return
	}
	if !locked {
		c.ws.Close(websocket.StatusPolicyViolation, "session already in use")
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
		c.send(serverCtx, msgError("invalid_init", "expected session_init message"))
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
				c.send(readCtx, msgError("malformed_message", err.Error()))
				continue
			}
			if msg.Type == "cancel_tts" {
				c.obs.Load().Interrupt()
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
		c.obs.Load().Interrupt()
		readCancel()
		wg.Wait()
		c.close()
	}()

	// Load session state from DB.
	if err := c.loadSession(workCtx); err != nil {
		slog.Error("conductor: load session", "error", err, "session_id", c.sessionID)
		c.send(workCtx, msgError("load_failed", "failed to load session"))
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

	// Main loop -- all control flow visible here.
	reconnectPending := false
	for {
		select {
		case msg, ok := <-msgCh:
			if !ok {
				return // client disconnected
			}
			if serverCtx.Err() != nil {
				c.send(workCtx, msgReconnectPlease)
				return
			}
			switch msg.Type {
			case "end_turn":
				if err := c.endTurn(workCtx, msg); err != nil {
					slog.Error("conductor: end_turn", "error", err, "session_id", c.sessionID)
					c.sm.ForceState(StateWaitingForInput)
					c.send(workCtx, msgError("turn_failed", "failed to process turn, please try again"))
					continue
				}
				if reconnectPending {
					c.send(workCtx, msgReconnectPlease)
					return
				}
			case "end_session":
				if err := c.endSession(workCtx); err != nil {
					slog.Error("conductor: end_session", "error", err, "session_id", c.sessionID)
				}
				return
			case "cancel_session":
				if err := c.cancelSession(serverCtx); err != nil {
					slog.Error("conductor: cancel_session", "error", err, "session_id", c.sessionID)
				}
				return
			case "ping":
				c.send(workCtx, msgPong)
			default:
				c.send(workCtx, msgError("unknown_message_type", "unknown message type: "+msg.Type))
			}

		case <-warningTimer:
			c.send(workCtx, msgTimerWarning(warningMinutes(c.duration)))

		case <-overtimeTimer:
			c.send(workCtx, msgTimerOvertime)

		case <-autoEndTimer:
			slog.Info("conductor: auto-ending session", "session_id", c.sessionID)
			if err := c.endSession(workCtx); err != nil {
				slog.Error("conductor: auto-end", "error", err, "session_id", c.sessionID)
			}
			return

		case <-reconnectTimer:
			reconnectPending = true

		case <-serverCtx.Done():
			c.send(workCtx, msgReconnectPlease)
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
		c.send(ctx, msgReconnectState(afterSeq, c.messages))
		return nil
	}
	c.send(ctx, msgSessionLoaded(c.sessionID, c.question, int(c.duration.Minutes()), c.ttsEnabled))
	if len(c.messages) > 0 {
		// Page refresh of existing session — send all messages, skip opening question.
		c.send(ctx, msgReconnectState(0, c.messages))
		c.send(ctx, msgStateChange(StateWaitingForInput))
		return nil
	}
	return c.streamInterviewerResponse(ctx)
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
			c.send(ctx, msgError("audio_validation_failed", "no audio data provided"))
			return nil
		}

		// Transition to Transcribing.
		if err := c.sm.Transition(StateTranscribing); err != nil {
			c.send(ctx, msgError("invalid_state_transition", err.Error()))
			return nil
		}
		c.send(ctx, msgStateChange(StateTranscribing))

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

		// STT.
		text, err := c.backend.Transcribe(ctx, msg.Audio, msg.AudioExt())
		if err != nil {
			slog.Error("conductor: transcription failed", "error", err, "session_id", c.sessionID)
			return fmt.Errorf("transcription: %w", err)
		}
		if strings.TrimSpace(text) == "" {
			return fmt.Errorf("transcription returned empty text")
		}

		c.send(ctx, msgTranscriptionResult(text))
		candidateContent = text
	} else {
		// Text input -- reject empty content.
		if strings.TrimSpace(msg.Content) == "" {
			c.send(ctx, msgError("empty_content", "text content cannot be empty"))
			return nil
		}
		candidateContent = msg.Content
	}

	// Transition to ProcessingInput.
	if err := c.sm.Transition(StateProcessingInput); err != nil {
		c.send(ctx, msgError("invalid_state_transition", err.Error()))
		return nil
	}
	c.send(ctx, msgStateChange(StateProcessingInput))

	// Persist candidate message.
	candidateMsg, err := c.persistMessage(ctx, messageID, "candidate", candidateContent, msg.InputMethod)
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

	// Cancel any lingering TTS from the previous turn.
	c.obs.Load().Interrupt()

	// Transition to InterviewerSpeaking.
	if err := c.sm.Transition(StateInterviewerSpeaking); err != nil {
		c.send(ctx, msgError("invalid_state_transition", err.Error()))
		return nil
	}
	c.send(ctx, msgStateChange(StateInterviewerSpeaking))

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
		observer.NewWSWriter(c.ws, messageID),
		accumulator,
	}
	if c.ttsEnabled {
		synth, err := c.backend.Synthesizer()
		if err == nil && synth != nil {
			sink := &ttsSink{ws: c.ws, messageID: messageID}
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
		fanOut.Interrupt()
		c.obs.Store(observer.Noop)
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
		fanOut.Interrupt()
		return fmt.Errorf("persist interviewer turn: %w", err)
	}

	c.messages = append(c.messages, interviewerMsg)

	// Transition to WaitingForInput.
	if err := c.sm.Transition(StateWaitingForInput); err != nil {
		return fmt.Errorf("transition to waiting: %w", err)
	}
	c.send(ctx, msgStateChange(StateWaitingForInput))

	return nil
}

// endSession transitions to Ending, persists status + enqueues evaluation atomically, then Ended.
func (c *Conductor) endSession(ctx context.Context) (err error) {
	ctx, span := tracer.Start(ctx, "Conductor.endSession")
	defer func() { drilotel.End(span, err) }()

	c.obs.Load().Interrupt()

	if err := c.sm.Transition(StateEnding); err != nil {
		c.send(ctx, msgError("invalid_state_transition", err.Error()))
		return nil
	}

	if err := c.backend.CompleteSession(ctx, c.sessionID, c.sm.TurnCount()); err != nil {
		return fmt.Errorf("complete session: %w", err)
	}

	if err := c.sm.Transition(StateEnded); err != nil {
		return fmt.Errorf("transition to ended: %w", err)
	}

	c.send(ctx, msgSessionEnded("candidate"))
	return nil
}

// cancelSession ends the session early without evaluation. Archived + refunded.
func (c *Conductor) cancelSession(ctx context.Context) (err error) {
	ctx, span := tracer.Start(ctx, "Conductor.cancelSession")
	defer func() { drilotel.End(span, err) }()

	c.obs.Load().Interrupt()

	if err := c.sm.Transition(StateEnding); err != nil {
		c.send(ctx, msgError("invalid_state_transition", err.Error()))
		return nil
	}

	if err := c.backend.CancelSession(ctx, c.sessionID, c.sm.TurnCount()); err != nil {
		return fmt.Errorf("cancel session: %w", err)
	}

	if err := c.sm.Transition(StateEnded); err != nil {
		return fmt.Errorf("transition to ended: %w", err)
	}

	c.send(ctx, msgSessionEnded("cancelled"))
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

// send writes a JSON message to the WebSocket. Errors are logged and ignored
// (writes to a disconnected client are expected failures).
func (c *Conductor) send(ctx context.Context, v any) {
	if err := c.ws.SendJSON(ctx, v); err != nil {
		slog.Debug("conductor: send failed", "error", err, "session_id", c.sessionID)
	}
}

// isReconnect returns true if the client sent a last_seq in session_init.
func (c *Conductor) isReconnect() bool {
	return c.initMsg.LastSeq != nil
}

// close closes the WebSocket and releases the advisory lock.
func (c *Conductor) close() {
	c.ws.Close(websocket.StatusNormalClosure, "session ended")
	if err := c.lock.Release(); err != nil {
		slog.Error("conductor: release session lock", "error", err, "session_id", c.sessionID)
	}
}
