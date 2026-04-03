package interview_test

import (
	"testing"

	"github.com/btc/drill/internal/interview"
	"github.com/stretchr/testify/assert"
)

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
	fan := interview.NewTokenFanOut(a, b)

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
	fan := interview.NewTokenFanOut(a, b)

	fan.Interrupt()

	assert.True(t, a.interrupted)
	assert.True(t, b.interrupted)
}

func TestTokenFanOut_ErrorPropagates(t *testing.T) {
	a, b := &spy{}, &spy{}
	fan := interview.NewTokenFanOut(a, b)

	testErr := assert.AnError
	fan.OnError(testErr)

	assert.Equal(t, testErr, a.errVal)
	assert.Equal(t, testErr, b.errVal)
}

func TestMessageAccumulator(t *testing.T) {
	acc := interview.NewMessageAccumulator()
	acc.OnToken("hello")
	acc.OnToken(" world")
	acc.OnDone("hello world")
	assert.Equal(t, "hello world", acc.Text())
}

func TestMessageAccumulator_EmptyStream(t *testing.T) {
	acc := interview.NewMessageAccumulator()
	acc.OnDone("")
	assert.Equal(t, "", acc.Text())
}
