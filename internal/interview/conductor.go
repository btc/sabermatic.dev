package interview

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math"
	"strings"
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

var (
	ErrAudioValidation = fmt.Errorf("audio validation failed")
	ErrTranscription   = fmt.Errorf("transcription failed")
)

// ConductorParams holds the dependencies for constructing a Conductor.
type ConductorParams struct {
	WS        observer.WSConn
	Backend   *backend.Backend
	LockConn  *pgxpool.Conn
	SessionID uuid.UUID
	UserID    uuid.UUID
	InitMsg   WSMessage
	Model     string
	Duration  time.Duration
	Cancel    context.CancelFunc
}

// Conductor owns all mutable state for one active interview session.
// A single goroutine runs Run(), processing messages sequentially from msgCh.
type Conductor struct {
	sm     *StateMachine
	msgCh  chan WSMessage
	cancel context.CancelFunc
	ws     observer.WSConn
	b        *backend.Backend
	lockConn *pgxpool.Conn
	obs      atomic.Pointer[observer.TokenFanOut]
	model    string

	sessionID  uuid.UUID
	userID     uuid.UUID
	question   db.Question
	messages   []db.Message
	sequence   int
	ttsEnabled bool
	duration   time.Duration
	initMsg    WSMessage

	reconnectPending bool
	timerWarningCh   <-chan time.Time
	timerOvertimeCh  <-chan time.Time
	reconnectTimerCh <-chan time.Time
}

// NewConductor constructs a Conductor from the given params.
func NewConductor(p ConductorParams) *Conductor {
	c := &Conductor{
		msgCh:  make(chan WSMessage),
		cancel: p.Cancel,
		ws:     p.WS,
		b:         p.Backend,
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

// MsgCh returns the channel that the read loop sends messages to.
func (c *Conductor) MsgCh() chan WSMessage { return c.msgCh }

// Observer returns the current TokenFanOut (never nil; Noop between turns).
func (c *Conductor) Observer() *observer.TokenFanOut { return c.obs.Load() }

// Run is the main loop for the conductor goroutine. It loads session state,
// streams the opening if needed, then processes messages until exit.
func (c *Conductor) Run(ctx context.Context) {
	defer c.cleanup()

	if err := c.loadSession(ctx); err != nil {
		slog.Error("conductor: load session", "error", err, "session_id", c.sessionID)
		_ = c.ws.SendJSON(ctx, map[string]string{"type": "error", "code": "load_failed", "message": "failed to load session"})
		return
	}

	isReconnect := len(c.messages) > 0 && c.initMsg.LastSeq != nil

	if isReconnect {
		if err := c.sendReconnectState(ctx); err != nil {
			slog.Error("conductor: send reconnect state", "error", err, "session_id", c.sessionID)
			return
		}
	} else {
		if err := c.sendSessionLoaded(ctx); err != nil {
			slog.Error("conductor: send session_loaded", "error", err, "session_id", c.sessionID)
			return
		}
		// Stream interviewer opening for new sessions.
		if err := c.streamInterviewerResponse(ctx); err != nil {
			slog.Error("conductor: opening stream", "error", err, "session_id", c.sessionID)
			return
		}
	}

	c.initTimers(ctx)
	c.selectLoop(ctx)
}

// loadSession loads the session, question, and existing messages from DB.
func (c *Conductor) loadSession(ctx context.Context) error {
	session, question, msgs, err := c.b.LoadSessionForConductor(ctx, c.sessionID)
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
func (c *Conductor) sendSessionLoaded(ctx context.Context) error {
	return c.ws.SendJSON(ctx, map[string]any{
		"type":        "session_loaded",
		"session_id":  c.sessionID.String(),
		"question":    map[string]string{"title": c.question.Title, "prompt": c.question.Prompt},
		"duration":    int(c.duration.Minutes()),
		"tts_enabled": c.ttsEnabled,
	})
}

// sendReconnectState sends all messages after the client's last known seq.
func (c *Conductor) sendReconnectState(ctx context.Context) error {
	lastSeq := 0
	if c.initMsg.LastSeq != nil {
		lastSeq = *c.initMsg.LastSeq
	}

	var missed []db.Message
	for _, m := range c.messages {
		if int(m.Seq) > lastSeq {
			missed = append(missed, m)
		}
	}

	return c.ws.SendJSON(ctx, map[string]any{
		"type":     "reconnect_state",
		"messages": missed,
	})
}

// WarningMinutes calculates the number of minutes before session end to fire
// the timer warning. Formula: clamp(2, 5, round(duration_minutes / 9)).
func WarningMinutes(duration time.Duration) float64 {
	w := math.Round(duration.Minutes() / 9)
	return math.Max(2, math.Min(5, w))
}

// initTimers sets up warning, overtime, and reconnect timers based on session start time.
func (c *Conductor) initTimers(ctx context.Context) {
	now := time.Now()
	sessionStart := c.sm.StartedAt()
	elapsed := now.Sub(sessionStart)

	// Warning timer: fires at duration - WarningMinutes.
	warningMinutes := WarningMinutes(c.duration)
	warningAt := c.duration - time.Duration(warningMinutes)*time.Minute
	if remaining := warningAt - elapsed; remaining > 0 {
		c.timerWarningCh = time.After(remaining)
	}

	// Overtime timer: fires at duration.
	if remaining := c.duration - elapsed; remaining > 0 {
		c.timerOvertimeCh = time.After(remaining)
	}

	// Reconnect timer: fires at 55 minutes into the WebSocket connection.
	// Only for sessions longer than 55 minutes.
	const reconnectThreshold = 55 * time.Minute
	if c.duration > reconnectThreshold {
		c.reconnectTimerCh = time.After(reconnectThreshold)
	}
}

// selectLoop is the main event loop.
func (c *Conductor) selectLoop(ctx context.Context) {
	// autoEndCh is set after overtime fires -- 2-minute grace period.
	var autoEndCh <-chan time.Time

	for {
		select {
		case msg, ok := <-c.msgCh:
			if !ok {
				c.handleDisconnect(ctx)
				return
			}
			if err := c.dispatch(ctx, msg); err != nil {
				slog.Error("conductor: dispatch", "error", err, "type", msg.Type, "session_id", c.sessionID)
			}
			if c.sm.State() == StateEnded {
				return
			}

		case <-c.timerWarningCh:
			c.timerWarningCh = nil
			_ = c.ws.SendJSON(ctx, map[string]string{"type": "timer_warning"})

		case <-c.timerOvertimeCh:
			c.timerOvertimeCh = nil
			_ = c.ws.SendJSON(ctx, map[string]string{"type": "timer_overtime"})
			autoEndCh = time.After(2 * time.Minute)

		case <-autoEndCh:
			slog.Info("conductor: auto-ending session", "session_id", c.sessionID)
			if err := c.endSession(ctx); err != nil {
				slog.Error("conductor: auto-end", "error", err, "session_id", c.sessionID)
			}
			return

		case <-c.reconnectTimerCh:
			c.reconnectTimerCh = nil
			c.reconnectPending = true

		case <-ctx.Done():
			c.handleShutdown(ctx)
			return
		}
	}
}

// dispatch routes a client message to the appropriate handler.
func (c *Conductor) dispatch(ctx context.Context, msg WSMessage) error {
	switch msg.Type {
	case "end_turn":
		if err := c.handleEndTurn(ctx, msg); err != nil {
			// Error recovery: force-reset to WaitingForInput so the session
			// remains usable. Matches v0's pattern where the orchestrator
			// force-resets state on InterviewError.
			slog.Error("conductor: end_turn failed, recovering", "error", err, "session_id", c.sessionID)
			c.sm.ForceState(StateWaitingForInput)
			_ = c.ws.SendJSON(ctx, map[string]string{
				"type":    "error",
				"code":    "turn_failed",
				"message": "failed to process turn, please try again",
			})
			return err
		}
		return nil
	case "end_session":
		return c.endSession(ctx)
	case "ping":
		return c.ws.SendJSON(ctx, map[string]string{"type": "pong"})
	default:
		return c.ws.SendJSON(ctx, map[string]string{
			"type":    "error",
			"code":    "unknown_message_type",
			"message": fmt.Sprintf("unknown message type: %s", msg.Type),
		})
	}
}

// handleEndTurn processes a candidate's turn (text or voice).
func (c *Conductor) handleEndTurn(ctx context.Context, msg WSMessage) error {
	var candidateContent string

	if msg.InputMethod == "voice" {
		// Validate audio.
		if len(msg.Audio) == 0 {
			return c.ws.SendJSON(ctx, map[string]string{
				"type":    "error",
				"code":    "audio_validation_failed",
				"message": "no audio data provided",
			})
		}

		// Transition to Transcribing.
		if err := c.sm.Transition(StateTranscribing); err != nil {
			return c.sendStateError(ctx, err)
		}
		_ = c.ws.SendJSON(ctx, map[string]string{"type": "state_change", "state": string(StateTranscribing)})

		// STT.
		text, err := c.b.Transcribe(ctx, msg.Audio, "webm")
		if err != nil {
			slog.Error("conductor: transcription failed", "error", err, "session_id", c.sessionID)
			return fmt.Errorf("transcription: %w", err)
		}
		if strings.TrimSpace(text) == "" {
			return fmt.Errorf("transcription returned empty text")
		}

		_ = c.ws.SendJSON(ctx, map[string]string{"type": "transcription_result", "text": text})
		candidateContent = text
	} else {
		// Text input -- reject empty content.
		if strings.TrimSpace(msg.Content) == "" {
			return c.ws.SendJSON(ctx, map[string]string{
				"type":    "error",
				"code":    "empty_content",
				"message": "text content cannot be empty",
			})
		}
		candidateContent = msg.Content
	}

	// Transition to ProcessingInput.
	if err := c.sm.Transition(StateProcessingInput); err != nil {
		return c.sendStateError(ctx, err)
	}
	_ = c.ws.SendJSON(ctx, map[string]string{"type": "state_change", "state": string(StateProcessingInput)})

	// Persist candidate message.
	candidateMsg, err := c.persistMessage(ctx, "candidate", candidateContent, msg.InputMethod)
	if err != nil {
		slog.Error("conductor: persist candidate message", "error", err, "session_id", c.sessionID)
		return fmt.Errorf("persist candidate message: %w", err)
	}
	c.messages = append(c.messages, candidateMsg)

	// Stream interviewer response.
	if err := c.streamInterviewerResponse(ctx); err != nil {
		return err
	}

	return nil
}

// streamInterviewerResponse builds a prompt, streams the LLM, and persists the result.
func (c *Conductor) streamInterviewerResponse(ctx context.Context) error {
	// Transition to InterviewerSpeaking.
	if err := c.sm.Transition(StateInterviewerSpeaking); err != nil {
		return c.sendStateError(ctx, err)
	}
	_ = c.ws.SendJSON(ctx, map[string]string{"type": "state_change", "state": string(StateInterviewerSpeaking)})

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
		synth, err := c.b.Synthesizer()
		if err == nil && synth != nil {
			observers = append(observers, observer.NewTTSAccumulator(ctx, c.ws, synth, messageID))
		}
	}
	fanOut := observer.NewTokenFanOut(observers...)
	c.obs.Store(fanOut)

	// Stream LLM.
	stream, err := c.b.StreamLLM(ctx, ai.StreamParams{
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
	interviewerMsg, err := c.b.PersistInterviewerTurn(ctx, stream, backend.PersistMessageParams{
		MessageID:   messageID,
		SessionID:   c.sessionID,
		Seq:         c.sequence,
		Role:        "interviewer",
		Content:     fullText,
		InputMethod: "",
	})
	if err != nil {
		c.sequence-- // rollback sequence on insert failure
		// Close fan-out even on error to wait for TTS goroutine.
		fanOut.Close()
		return fmt.Errorf("persist interviewer turn: %w", err)
	}

	// Close fan-out after persistence: waits for TTS goroutine to finish
	// writing all TTS chunks. Persistence must happen first because the
	// conductor needs MessageAccumulator's text for the message content.
	fanOut.Close()

	c.messages = append(c.messages, interviewerMsg)

	// Transition to WaitingForInput.
	if err := c.sm.Transition(StateWaitingForInput); err != nil {
		return fmt.Errorf("transition to waiting: %w", err)
	}
	_ = c.ws.SendJSON(ctx, map[string]string{"type": "state_change", "state": string(StateWaitingForInput)})

	// Check reconnect pending (between turns, not mid-stream).
	if c.reconnectPending {
		_ = c.ws.SendJSON(ctx, map[string]string{"type": "reconnect_please"})
	}

	return nil
}

// endSession transitions to Ending, persists status + enqueues evaluation atomically, then Ended.
func (c *Conductor) endSession(ctx context.Context) error {
	if err := c.sm.Transition(StateEnding); err != nil {
		return c.sendStateError(ctx, err)
	}

	if err := c.b.CompleteSession(ctx, c.sessionID, c.sm.TurnCount()); err != nil {
		return fmt.Errorf("complete session: %w", err)
	}

	if err := c.sm.Transition(StateEnded); err != nil {
		return fmt.Errorf("transition to ended: %w", err)
	}

	_ = c.ws.SendJSON(ctx, map[string]string{"type": "session_ended"})
	return nil
}

// handleDisconnect is called when msgCh is closed (client disconnected).
func (c *Conductor) handleDisconnect(ctx context.Context) {
	slog.Info("conductor: client disconnected", "session_id", c.sessionID, "state", c.sm.State())

	// Interrupt TTS if mid-stream (Noop.Interrupt is a safe no-op between turns).
	c.obs.Load().Interrupt()
	// The LLM stream continues to completion in streamInterviewerResponse;
	// the message is persisted when that method returns. The disconnect is
	// detected on the *next* iteration of the select loop, so by the time
	// we're here, any in-flight stream has already completed.
}

// handleShutdown persists partial state and signals the client to reconnect.
func (c *Conductor) handleShutdown(ctx context.Context) {
	slog.Info("conductor: shutting down", "session_id", c.sessionID, "state", c.sm.State())

	// Best-effort: tell client to reconnect.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = c.ws.SendJSON(shutdownCtx, map[string]string{"type": "reconnect_please"})
}

// persistMessage inserts a message into the DB and returns it.
func (c *Conductor) persistMessage(ctx context.Context, role, content, inputMethod string) (db.Message, error) {
	c.sequence++

	msg, err := c.b.PersistMessage(ctx, backend.PersistMessageParams{
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

// sendStateError sends a state transition error to the client.
func (c *Conductor) sendStateError(ctx context.Context, err error) error {
	return c.ws.SendJSON(ctx, map[string]string{
		"type":    "error",
		"code":    "invalid_state_transition",
		"message": err.Error(),
	})
}

// cleanup closes the WebSocket and releases the advisory lock connection.
// Called via defer from Run(), ensuring all exit paths (endSession,
// handleShutdown, handleDisconnect) close the WS. For disconnect the WS
// is already closed; calling Close on a closed WS is safe.
func (c *Conductor) cleanup() {
	c.cancel()                                                 // unblocks readLoop's ws.Read and select
	c.ws.Close(websocket.StatusNormalClosure, "session ended")
	if c.lockConn != nil {
		c.lockConn.Release()
		c.lockConn = nil
	}
}
