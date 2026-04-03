package interview

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/btc/drill/internal/ai"
	"github.com/btc/drill/internal/backend"
	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/interview/observer"
)

// ConductorParams contains everything needed to construct a Conductor.
// The handler creates these after auth, validation, WebSocket upgrade,
// advisory lock acquisition, and reading the session_init message.
type ConductorParams struct {
	// WS is the upgraded WebSocket connection. The conductor wraps it
	// in a WSConn for writes and uses rawWS directly for readLoop reads.
	WS *websocket.Conn

	// Backend is the service layer for DB, LLM, STT, and TTS operations.
	Backend *backend.Backend

	// LockConn is the dedicated connection holding the advisory lock.
	// The conductor releases it in close().
	LockConn *pgxpool.Conn

	// SessionID identifies the interview session.
	SessionID uuid.UUID

	// UserID identifies the authenticated user.
	UserID uuid.UUID

	// InitMsg is the parsed session_init message from the client.
	InitMsg WSMessage

	// Model is the LLM model name (e.g., "claude-sonnet-4-20250514").
	Model string

	// Duration is the configured interview duration. Zero means use the DB value.
	Duration time.Duration
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
	lockConn *pgxpool.Conn

	// Current per-turn observer fan-out. Atomic for cross-goroutine access
	// (readLoop reads for cancel_tts, conductor goroutine writes).
	// Always non-nil -- observer.Noop between turns.
	obs atomic.Pointer[observer.TokenFanOut]

	// Session state loaded from DB.
	sessionID  uuid.UUID
	userID     uuid.UUID
	question   db.Question
	messages   []db.Message // in-memory transcript, appended after each persist
	sequence   int          // last message seq number
	ttsEnabled bool
	duration   time.Duration
	model      string

	// The client's session_init message (contains LastSeq for reconnect detection).
	initMsg WSMessage
}

// NewConductor constructs a Conductor from the given params.
func NewConductor(p ConductorParams) *Conductor {
	c := &Conductor{
		rawWS:     p.WS,
		ws:        &Conn{WS: p.WS},
		backend:   p.Backend,
		lockConn:  p.LockConn,
		model:     p.Model,
		sessionID: p.SessionID,
		userID:    p.UserID,
		duration:  p.Duration,
		initMsg:   p.InitMsg,
	}
	c.obs.Store(observer.Noop)
	return c
}

// isReconnect returns true if the client sent a last_seq in session_init.
func (c *Conductor) isReconnect() bool {
	return c.initMsg.LastSeq != nil
}

// Run is the single entry point for the conductor. It owns the readLoop
// lifecycle, loads session state, sends initial messages, sets timers, and
// runs the main event loop. Run blocks until the session ends.
func (c *Conductor) Run(serverCtx context.Context) {
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
		readCancel()
		wg.Wait()
		c.close()
	}()

	// Load session state from DB.
	if err := c.loadSession(serverCtx); err != nil {
		slog.Error("conductor: load session", "error", err, "session_id", c.sessionID)
		c.send(serverCtx, msgError("load_failed", "failed to load session"))
		return
	}

	// Timers -- all set declaratively, unconditionally. Timers that fire
	// after the session ends are harmless (no one reads the channel).
	elapsed := time.Since(c.sm.StartedAt())
	warningTimer := time.After(max(0, c.duration-time.Duration(warningMinutes(c.duration))*time.Minute-elapsed))
	overtimeTimer := time.After(max(0, c.duration-elapsed))
	autoEndTimer := time.After(max(0, c.duration+2*time.Minute-elapsed))
	reconnectTimer := time.After(55 * time.Minute)

	// Initial messages to client.
	if c.isReconnect() {
		c.sendReconnectState(serverCtx)
	} else {
		c.sendSessionLoaded(serverCtx)
		if err := c.streamInterviewerResponse(serverCtx); err != nil {
			slog.Error("conductor: opening stream", "error", err, "session_id", c.sessionID)
			return
		}
	}

	// Main loop -- all control flow visible here.
	reconnectPending := false
	for {
		select {
		case msg, ok := <-msgCh:
			if !ok {
				return // client disconnected
			}
			switch msg.Type {
			case "end_turn":
				if err := c.handleEndTurn(serverCtx, msg); err != nil {
					slog.Error("conductor: end_turn", "error", err, "session_id", c.sessionID)
					c.sm.ForceState(StateWaitingForInput)
					c.send(serverCtx, msgError("turn_failed", "failed to process turn, please try again"))
					continue
				}
				if reconnectPending {
					c.send(serverCtx, msgReconnectPlease)
					return
				}
			case "end_session":
				if err := c.endSession(serverCtx); err != nil {
					slog.Error("conductor: end_session", "error", err, "session_id", c.sessionID)
				}
				return
			case "ping":
				c.send(serverCtx, msgPong)
			default:
				c.send(serverCtx, msgError("unknown_message_type", "unknown message type: "+msg.Type))
			}

		case <-warningTimer:
			c.send(serverCtx, msgTimerWarning(int(warningMinutes(c.duration))))

		case <-overtimeTimer:
			c.send(serverCtx, msgTimerOvertime)

		case <-autoEndTimer:
			slog.Info("conductor: auto-ending session", "session_id", c.sessionID)
			if err := c.endSession(serverCtx); err != nil {
				slog.Error("conductor: auto-end", "error", err, "session_id", c.sessionID)
			}
			return

		case <-reconnectTimer:
			reconnectPending = true

		case <-serverCtx.Done():
			c.send(context.Background(), msgReconnectPlease)
			return
		}
	}
}

// loadSession loads the session, question, and existing messages from DB.
func (c *Conductor) loadSession(ctx context.Context) error {
	session, question, msgs, err := c.backend.LoadSessionForConductor(ctx, c.sessionID)
	if err != nil {
		return err
	}

	c.question = question
	c.messages = msgs
	c.ttsEnabled = session.ConfigTtsEnabled
	if c.duration == 0 {
		c.duration = time.Duration(session.ConfigDurationMinutes) * time.Minute
	}
	c.sm = NewStateMachine(StateWaitingForInput)
	c.sm.SetStartedAt(session.StartedAt)

	if len(msgs) > 0 {
		c.sequence = int(msgs[len(msgs)-1].Seq)
	}

	return nil
}

// sendSessionLoaded sends the session_loaded event to the client.
func (c *Conductor) sendSessionLoaded(ctx context.Context) {
	c.send(ctx, msgSessionLoaded(c.sessionID, c.question, int(c.duration.Minutes()), c.ttsEnabled))
}

// sendReconnectState sends all messages after the client's last known seq.
func (c *Conductor) sendReconnectState(ctx context.Context) {
	lastSeq := 0
	if c.initMsg.LastSeq != nil {
		lastSeq = *c.initMsg.LastSeq
	}
	c.send(ctx, msgReconnectState(lastSeq, c.messages))
}

// handleEndTurn processes a candidate's turn (text or voice).
func (c *Conductor) handleEndTurn(ctx context.Context, msg WSMessage) error {
	var candidateContent string

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

		// STT.
		text, err := c.backend.Transcribe(ctx, msg.Audio, "webm")
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
	candidateMsg, err := c.persistMessage(ctx, "candidate", candidateContent, msg.InputMethod)
	if err != nil {
		slog.Error("conductor: persist candidate message", "error", err, "session_id", c.sessionID)
		return fmt.Errorf("persist candidate message: %w", err)
	}
	c.messages = append(c.messages, candidateMsg)

	// Stream interviewer response.
	return c.streamInterviewerResponse(ctx)
}

// streamInterviewerResponse builds a prompt, streams the LLM, and persists the result.
func (c *Conductor) streamInterviewerResponse(ctx context.Context) error {
	// Transition to InterviewerSpeaking.
	if err := c.sm.Transition(StateInterviewerSpeaking); err != nil {
		c.send(ctx, msgError("invalid_state_transition", err.Error()))
		return nil
	}
	c.send(ctx, msgStateChange(StateInterviewerSpeaking))

	messageID := uuid.New()

	// Build prompt.
	now := time.Now()
	elapsed := now.Sub(c.sm.StartedAt())
	remaining := c.duration - elapsed
	if remaining < 0 {
		remaining = 0
	}

	system, promptMsgs := NewInterviewerPrompt().
		WithSystemInstructions().
		WithQuestion(c.question).
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
			observers = append(observers, observer.NewTTSAccumulator(ctx, c.ws, synth, messageID))
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
	c.obs.Store(observer.Noop)

	// Persist interviewer message and LLM call atomically.
	c.sequence++
	interviewerMsg, err := c.backend.PersistInterviewerTurn(ctx, stream, backend.PersistMessageParams{
		MessageID:   messageID,
		SessionID:   c.sessionID,
		Seq:         c.sequence,
		Role:        "interviewer",
		Content:     fullText,
		InputMethod: "",
	})
	if err != nil {
		c.sequence-- // rollback sequence on insert failure
		fanOut.Close()
		return fmt.Errorf("persist interviewer turn: %w", err)
	}

	// Close fan-out after persistence: waits for TTS goroutine to finish.
	fanOut.Close()

	c.messages = append(c.messages, interviewerMsg)

	// Transition to WaitingForInput.
	if err := c.sm.Transition(StateWaitingForInput); err != nil {
		return fmt.Errorf("transition to waiting: %w", err)
	}
	c.send(ctx, msgStateChange(StateWaitingForInput))

	return nil
}

// endSession transitions to Ending, persists status + enqueues evaluation atomically, then Ended.
func (c *Conductor) endSession(ctx context.Context) error {
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

// persistMessage inserts a message into the DB and returns it.
func (c *Conductor) persistMessage(ctx context.Context, role, content, inputMethod string) (db.Message, error) {
	c.sequence++

	msg, err := c.backend.PersistMessage(ctx, backend.PersistMessageParams{
		MessageID:   uuid.New(),
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

// close closes the WebSocket and releases the advisory lock connection.
func (c *Conductor) close() {
	c.ws.Close(websocket.StatusNormalClosure, "session ended")
	if c.lockConn != nil {
		c.lockConn.Release()
		c.lockConn = nil
	}
}
