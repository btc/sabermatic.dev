package interview

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
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
	// in a WSConn for writes and uses it directly for readLoop reads.
	WS *websocket.Conn

	// Backend is the service layer for DB, LLM, STT, and TTS operations.
	Backend *backend.Backend

	// LockConn is the dedicated connection holding the advisory lock.
	// The conductor releases it in cleanup().
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
// A single goroutine runs the select loop; the readLoop runs in a separate
// goroutine and communicates via msgCh. Run() blocks until the session ends.
type Conductor struct {
	// sm is the interview state machine (pure logic, no I/O).
	sm *StateMachine

	// msgCh receives parsed WebSocket messages from the readLoop goroutine.
	// Unbuffered -- provides natural backpressure.
	msgCh chan WSMessage

	// cancel cancels the readLoop's context, signaling it to exit.
	// Called in cleanup(). Derived from context.Background() -- independent
	// of the server context so readLoop lifetime is conductor-controlled.
	cancel context.CancelFunc

	// rawWS is the underlying WebSocket connection. Used by readLoop for
	// raw reads. Kept separate from ws because readLoop needs Read(), which
	// is not on the WSConn interface.
	rawWS *websocket.Conn

	// ws is the write-side WebSocket interface. Used by the conductor and
	// observers to send messages to the client. Thread-safe (coder/websocket).
	ws observer.WSConn

	// b is the backend service layer. The conductor calls Backend methods
	// for all DB operations, LLM streaming, and STT/TTS -- never holds raw
	// pool, jobs, or AI client references.
	b *backend.Backend

	// lockConn is the dedicated pgxpool connection holding the Postgres
	// advisory lock for this session. Released in cleanup() when the
	// conductor exits. Prevents concurrent conductors on the same session.
	lockConn *pgxpool.Conn

	// obs holds the current per-turn observer fan-out. Atomic because
	// the readLoop goroutine reads it (for cancel_tts -> Interrupt) while
	// the conductor goroutine writes it (new fan-out each turn).
	// Always non-nil -- set to observer.Noop between turns (Null Object).
	obs atomic.Pointer[observer.TokenFanOut]

	// model is the LLM model name (e.g., "claude-sonnet-4-20250514").
	model string

	// --- Session state (loaded from DB, mutated during the session) ---

	// sessionID is the interview session's UUID.
	sessionID uuid.UUID

	// userID is the authenticated user who owns this session.
	userID uuid.UUID

	// question is the interview question for this session.
	question db.Question

	// messages is the in-memory transcript, appended after each DB persist.
	// Avoids re-querying the full transcript on every turn for prompt building.
	messages []db.Message

	// sequence is the last message sequence number. Incremented before each
	// persist, rolled back on failure.
	sequence int

	// ttsEnabled controls whether TTSAccumulator is included in the fan-out.
	ttsEnabled bool

	// duration is the configured interview duration.
	duration time.Duration

	// initMsg is the client's session_init message, read by the handler
	// before constructing the conductor. Contains LastSeq for reconnect.
	initMsg WSMessage

	// --- Timers ---

	// reconnectPending is set when the 55-minute reconnect timer fires.
	// Checked after each turn completes -- reconnect happens between turns.
	reconnectPending bool

	// timerWarningCh fires when the session approaches its time limit.
	// Formula: duration - clamp(2, 5, round(duration/9)) minutes.
	timerWarningCh <-chan time.Time

	// timerOvertimeCh fires when the session exceeds its configured duration.
	timerOvertimeCh <-chan time.Time

	// reconnectTimerCh fires at 55 minutes into the WebSocket connection
	// (Cloud Run's 60-minute request timeout). Not set for short sessions.
	reconnectTimerCh <-chan time.Time
}

// NewConductor constructs a Conductor from the given params.
func NewConductor(p ConductorParams) *Conductor {
	c := &Conductor{
		msgCh:     make(chan WSMessage),
		rawWS:     p.WS,
		ws:        &Conn{WS: p.WS},
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

// Run is the single entry point for the conductor. It owns the readLoop
// lifecycle, loads session state, streams the opening if needed, then
// processes messages until exit. Run blocks until the session ends.
func (c *Conductor) Run(serverCtx context.Context) {
	// Conductor owns its readLoop lifecycle.
	readCtx, readCancel := context.WithCancel(context.Background())
	c.cancel = readCancel

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		c.readLoop(readCtx)
	}()

	// LIFO order matters: wg.Wait runs last (confirms readLoop exited),
	// cleanup runs first (cancels readCtx, closes WS, releases lock).
	defer wg.Wait()
	defer c.cleanup()

	if err := c.loadSession(serverCtx); err != nil {
		slog.Error("conductor: load session", "error", err, "session_id", c.sessionID)
		_ = c.ws.SendJSON(serverCtx, map[string]string{"type": "error", "code": "load_failed", "message": "failed to load session"})
		return
	}

	isReconnect := len(c.messages) > 0 && c.initMsg.LastSeq != nil

	if isReconnect {
		if err := c.sendReconnectState(serverCtx); err != nil {
			slog.Error("conductor: send reconnect state", "error", err, "session_id", c.sessionID)
			return
		}
	} else {
		if err := c.sendSessionLoaded(serverCtx); err != nil {
			slog.Error("conductor: send session_loaded", "error", err, "session_id", c.sessionID)
			return
		}
		// Stream interviewer opening for new sessions.
		if err := c.streamInterviewerResponse(serverCtx); err != nil {
			slog.Error("conductor: opening stream", "error", err, "session_id", c.sessionID)
			return
		}
	}

	c.initTimers()
	c.selectLoop(serverCtx)
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
func (c *Conductor) initTimers() {
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

// readLoop reads messages from the raw WebSocket and forwards them to msgCh.
// On error (disconnect), it closes msgCh to signal the conductor's selectLoop.
func (c *Conductor) readLoop(ctx context.Context) {
	defer close(c.msgCh)
	for {
		_, data, err := c.rawWS.Read(ctx)
		if err != nil {
			return
		}
		msg, err := ParseWSMessage(data)
		if err != nil {
			errJSON, _ := json.Marshal(map[string]string{
				"type": "error", "code": "malformed_message", "message": err.Error(),
			})
			c.rawWS.Write(ctx, websocket.MessageText, errJSON)
			continue
		}
		if msg.Type == "cancel_tts" {
			c.obs.Load().Interrupt()
			continue
		}
		select {
		case c.msgCh <- msg:
		case <-ctx.Done():
			return
		}
	}
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
