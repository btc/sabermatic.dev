package observer_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"testing"

	"github.com/coder/websocket"
	"github.com/google/uuid"

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

func TestTTSAccumulator_SentenceBoundaries(t *testing.T) {
	ws := &mockWSConn{}
	synth := &fakeSynth{audio: bytes.Repeat([]byte("x"), 8192)}
	ctx := context.Background()

	acc := observer.NewTTSAccumulator(ctx, ws, synth, uuid.New())
	acc.OnToken("Hello there. ")
	acc.OnToken("How are you? ")
	acc.OnDone("Hello there. How are you? ")
	acc.Close() // wait for TTS goroutine

	// Count tts_chunk messages.
	var chunks int
	for _, msg := range ws.sent {
		if bytes.Contains(msg, []byte(`"tts_chunk"`)) {
			chunks++
		}
	}
	assert.Equal(t, 2, chunks, "expected exactly one tts_chunk per sentence")

	// Verify each chunk's data round-trips to the original audio.
	for _, msg := range ws.sent {
		var m map[string]any
		require.NoError(t, json.Unmarshal(msg, &m))
		if m["type"] != "tts_chunk" {
			continue
		}
		b64, ok := m["data"].(string)
		require.True(t, ok)
		decoded, err := base64.StdEncoding.DecodeString(b64)
		require.NoError(t, err)
		assert.Equal(t, synth.audio, decoded, "tts_chunk data must match full synthesized audio")
	}

	// Should have tts_done at the end.
	require.NotEmpty(t, ws.sent)
	lastMsg := ws.sent[len(ws.sent)-1]
	assert.Contains(t, string(lastMsg), `"tts_done"`)
}

func TestTTSAccumulator_Interrupt(t *testing.T) {
	ws := &mockWSConn{}
	synth := &blockingSynth{}
	ctx := context.Background()

	acc := observer.NewTTSAccumulator(ctx, ws, synth, uuid.New())
	acc.OnToken("First sentence. Second sentence. ")
	acc.Interrupt()
	acc.OnDone("First sentence. Second sentence. ")
	acc.Close() // should not hang -- interrupt cancels context
}
