package observer_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/btc/drill/internal/ai"
	"github.com/btc/drill/internal/interview/observer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockWSConn struct {
	sent [][]byte
	err  error
}

func (m *mockWSConn) SendJSON(ctx context.Context, v any) error {
	if m.err != nil {
		return m.err
	}
	data, _ := json.Marshal(v)
	m.sent = append(m.sent, data)
	return nil
}

func (m *mockWSConn) Close(code websocket.StatusCode, reason string) error {
	return nil
}

type spy struct {
	tokens      []string
	doneMsg     string
	errVal      error
	interrupted bool
}

func (s *spy) OnToken(token string)     { s.tokens = append(s.tokens, token) }
func (s *spy) OnDone(fullMessage string) { s.doneMsg = fullMessage }
func (s *spy) OnError(err error)         { s.errVal = err }
func (s *spy) Interrupt()                { s.interrupted = true }

func TestTokenFanOut_DistributesToAll(t *testing.T) {
	a, b := &spy{}, &spy{}
	fan := observer.NewTokenFanOut(a, b)

	fan.OnToken("hello")
	fan.OnToken(" world")
	fan.OnDone("hello world")

	assert.Equal(t, []string{"hello", " world"}, a.tokens)
	assert.Equal(t, []string{"hello", " world"}, b.tokens)
	assert.Equal(t, "hello world", a.doneMsg)
	assert.Equal(t, "hello world", b.doneMsg)
}

func TestTokenFanOut_InterruptPropagates(t *testing.T) {
	a, b := &spy{}, &spy{}
	fan := observer.NewTokenFanOut(a, b)

	fan.Interrupt()

	assert.True(t, a.interrupted)
	assert.True(t, b.interrupted)
}

func TestTokenFanOut_ErrorPropagates(t *testing.T) {
	a, b := &spy{}, &spy{}
	fan := observer.NewTokenFanOut(a, b)

	testErr := assert.AnError
	fan.OnError(testErr)

	assert.Equal(t, testErr, a.errVal)
	assert.Equal(t, testErr, b.errVal)
}

func TestTokenFanOut_Close(t *testing.T) {
	closed := false
	closeable := &closeableSpy{onClose: func() { closed = true }}
	nonCloseable := &spy{}
	fan := observer.NewTokenFanOut(closeable, nonCloseable)

	fan.Close()
	assert.True(t, closed)
}

type closeableSpy struct {
	spy
	onClose func()
}

func (c *closeableSpy) Close() {
	if c.onClose != nil {
		c.onClose()
	}
}

func TestMessageAccumulator(t *testing.T) {
	acc := observer.NewMessageAccumulator()
	acc.OnToken("hello")
	acc.OnToken(" world")
	acc.OnDone("hello world")
	assert.Equal(t, "hello world", acc.Text())
}

func TestMessageAccumulator_EmptyStream(t *testing.T) {
	acc := observer.NewMessageAccumulator()
	acc.OnDone("")
	assert.Equal(t, "", acc.Text())
}

func TestWSWriter_OnToken(t *testing.T) {
	ws := &mockWSConn{}
	writer := observer.NewWSWriter(ws, uuid.New())
	writer.OnToken("hello")
	require.Len(t, ws.sent, 1)
	assert.Contains(t, string(ws.sent[0]), `"type":"interviewer_token"`)
	assert.Contains(t, string(ws.sent[0]), `"token":"hello"`)
}

func TestWSWriter_OnDone(t *testing.T) {
	ws := &mockWSConn{}
	msgID := uuid.New()
	writer := observer.NewWSWriter(ws, msgID)
	writer.OnDone("full message")
	require.Len(t, ws.sent, 1)
	assert.Contains(t, string(ws.sent[0]), `"type":"interviewer_done"`)
	assert.Contains(t, string(ws.sent[0]), msgID.String())
}

func TestWSWriter_IgnoresWriteErrors(t *testing.T) {
	ws := &mockWSConn{err: fmt.Errorf("closed")}
	writer := observer.NewWSWriter(ws, uuid.New())
	writer.OnToken("hello")
	writer.OnDone("hello")
	// Should not panic -- errors are logged and ignored
}

// --- TTSSink mock ---

type mockTTSSink struct {
	mu     sync.Mutex
	audio  [][]byte
	done   bool
	errors int
}

func (m *mockTTSSink) HandleAudio(data []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.audio = append(m.audio, append([]byte(nil), data...))
}

func (m *mockTTSSink) HandleTTSDone() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.done = true
}

func (m *mockTTSSink) HandleTTSError() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.errors++
}

// --- Synth mocks ---

// fakeSynth returns a fixed audio payload immediately.
type fakeSynth struct {
	audio []byte
}

func (f *fakeSynth) Synthesize(_ context.Context, _ string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(f.audio)), nil
}

// blockingSynth blocks until the context is cancelled.
type blockingSynth struct{}

func (b *blockingSynth) Synthesize(ctx context.Context, _ string) (io.ReadCloser, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// errorSynth always returns an error.
type errorSynth struct{}

func (e *errorSynth) Synthesize(_ context.Context, _ string) (io.ReadCloser, error) {
	return nil, fmt.Errorf("synth unavailable")
}

// blockingReaderSynth returns a reader that blocks on Read until context is cancelled.
type blockingReaderSynth struct{}

func (b *blockingReaderSynth) Synthesize(ctx context.Context, _ string) (io.ReadCloser, error) {
	return io.NopCloser(readerFunc(func(p []byte) (int, error) {
		<-ctx.Done()
		return 0, ctx.Err()
	})), nil
}

type readerFunc func(p []byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

// --- Helper ---

func newTestAccumulator(sink *mockTTSSink, synth ai.Synthesizer) *observer.TTSAccumulator {
	return observer.NewTTSAccumulator(observer.TTSAccumulatorParams{
		Sink:            sink,
		Synth:           synth,
		SentenceTimeout: 500 * time.Millisecond,
	})
}

// --- Tests ---

func TestTTSAccumulator_SentenceBoundaries(t *testing.T) {
	sink := &mockTTSSink{}
	audio := bytes.Repeat([]byte("x"), 8192)
	acc := newTestAccumulator(sink, &fakeSynth{audio: audio})

	acc.OnToken("Hello there. ")
	acc.OnToken("How are you? ")
	acc.OnDone("Hello there. How are you? ")
	acc.Close()

	sink.mu.Lock()
	defer sink.mu.Unlock()
	assert.Len(t, sink.audio, 2, "expected one audio per sentence")
	for _, data := range sink.audio {
		assert.Equal(t, audio, data, "audio must be raw bytes, not base64")
	}
	assert.True(t, sink.done, "HandleTTSDone must be called")
	assert.Equal(t, 0, sink.errors, "no errors expected")
}

func TestTTSAccumulator_Interrupt(t *testing.T) {
	sink := &mockTTSSink{}
	acc := newTestAccumulator(sink, &blockingSynth{})

	acc.OnToken("First sentence. Second sentence. ")
	acc.Interrupt()
	acc.OnDone("First sentence. Second sentence. ")
	acc.Close() // must not hang

	sink.mu.Lock()
	defer sink.mu.Unlock()
	assert.False(t, sink.done, "HandleTTSDone must not be called after interrupt")
}

func TestTTSAccumulator_SentenceTimeout(t *testing.T) {
	sink := &mockTTSSink{}
	acc := observer.NewTTSAccumulator(observer.TTSAccumulatorParams{
		Sink:            sink,
		Synth:           &blockingSynth{},
		SentenceTimeout: 50 * time.Millisecond,
	})

	acc.OnToken("Slow sentence. ")
	acc.OnDone("Slow sentence. ")

	start := time.Now()
	acc.Close()
	elapsed := time.Since(start)

	assert.Less(t, elapsed, 500*time.Millisecond, "Close must return within timeout, not hang")
	sink.mu.Lock()
	defer sink.mu.Unlock()
	assert.Equal(t, 1, sink.errors, "HandleTTSError must be called on timeout")
	assert.True(t, sink.done, "HandleTTSDone must still be called after timeout")
}

func TestTTSAccumulator_BlockingReadAllTimeout(t *testing.T) {
	sink := &mockTTSSink{}
	acc := observer.NewTTSAccumulator(observer.TTSAccumulatorParams{
		Sink:            sink,
		Synth:           &blockingReaderSynth{},
		SentenceTimeout: 50 * time.Millisecond,
	})

	acc.OnToken("Stuck read. ")
	acc.OnDone("Stuck read. ")

	start := time.Now()
	acc.Close()
	elapsed := time.Since(start)

	assert.Less(t, elapsed, 500*time.Millisecond, "Close must return within timeout, not hang")
	sink.mu.Lock()
	defer sink.mu.Unlock()
	assert.GreaterOrEqual(t, sink.errors, 1, "HandleTTSError must be called on read timeout")
}

func TestTTSAccumulator_HandleTTSDoneCalledOnce(t *testing.T) {
	sink := &mockTTSSink{}
	acc := newTestAccumulator(sink, &fakeSynth{audio: []byte("x")})

	acc.OnToken("One. Two. Three. ")
	acc.OnDone("One. Two. Three. ")
	acc.Close()

	sink.mu.Lock()
	defer sink.mu.Unlock()
	assert.True(t, sink.done)
	assert.Len(t, sink.audio, 3)
}

func TestTTSAccumulator_HandleTTSDoneNotCalledOnError(t *testing.T) {
	sink := &mockTTSSink{}
	acc := newTestAccumulator(sink, &fakeSynth{audio: []byte("x")})

	acc.OnError(fmt.Errorf("llm failed"))
	acc.OnDone("partial text")
	acc.Close()

	sink.mu.Lock()
	defer sink.mu.Unlock()
	assert.False(t, sink.done, "HandleTTSDone must not be called after OnError")
}

func TestTTSAccumulator_CloseIdempotent(t *testing.T) {
	sink := &mockTTSSink{}
	acc := newTestAccumulator(sink, &fakeSynth{audio: []byte("x")})

	acc.OnDone("")
	acc.Close()
	acc.Close() // must not panic
}

func TestTTSAccumulator_SynthErrorNotifiesUser(t *testing.T) {
	sink := &mockTTSSink{}
	acc := newTestAccumulator(sink, &errorSynth{})

	acc.OnToken("Will fail. ")
	acc.OnDone("Will fail. ")
	acc.Close()

	sink.mu.Lock()
	defer sink.mu.Unlock()
	assert.Equal(t, 1, sink.errors, "HandleTTSError must be called on synth error")
	assert.Len(t, sink.audio, 0, "no audio when synth fails")
	assert.True(t, sink.done)
}

func TestTTSAccumulator_OnDoneAfterOnErrorNoPanic(t *testing.T) {
	sink := &mockTTSSink{}
	acc := newTestAccumulator(sink, &fakeSynth{audio: []byte("x")})

	acc.OnError(fmt.Errorf("boom"))
	acc.OnDone("text")
	acc.Close() // must not panic from double close(sentCh)
}
