package interview_test

import (
	"testing"

	"github.com/btc/drill/internal/interview"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseWSMessage_SessionInit(t *testing.T) {
	msg, err := interview.ParseWSMessage([]byte(`{"type":"session_init","last_seq":5}`))
	require.NoError(t, err)
	assert.Equal(t, "session_init", msg.Type)
	require.NotNil(t, msg.LastSeq)
	assert.Equal(t, 5, *msg.LastSeq)
}

func TestParseWSMessage_SessionInitNullSeq(t *testing.T) {
	msg, err := interview.ParseWSMessage([]byte(`{"type":"session_init","last_seq":null}`))
	require.NoError(t, err)
	assert.Equal(t, "session_init", msg.Type)
	assert.Nil(t, msg.LastSeq)
}

func TestParseWSMessage_EndTurnText(t *testing.T) {
	msg, err := interview.ParseWSMessage([]byte(`{"type":"end_turn","content":"my answer","input_method":"text"}`))
	require.NoError(t, err)
	assert.Equal(t, "end_turn", msg.Type)
	assert.Equal(t, "text", msg.InputMethod)
	assert.Equal(t, "my answer", msg.Content)
}

func TestParseWSMessage_EndTurnVoice(t *testing.T) {
	// "aGVsbG8=" is base64 for "hello"
	msg, err := interview.ParseWSMessage([]byte(`{"type":"end_turn","audio":"aGVsbG8=","input_method":"voice"}`))
	require.NoError(t, err)
	assert.Equal(t, "end_turn", msg.Type)
	assert.Equal(t, "voice", msg.InputMethod)
	assert.Equal(t, []byte("hello"), msg.Audio)
	assert.Equal(t, "audio/webm", msg.AudioMIME, "should default to audio/webm when not specified")
	assert.Equal(t, "webm", msg.AudioExt())
}

func TestParseWSMessage_EndTurnVoiceWithMIME(t *testing.T) {
	msg, err := interview.ParseWSMessage([]byte(`{"type":"end_turn","audio":"aGVsbG8=","input_method":"voice","audio_mime":"audio/ogg"}`))
	require.NoError(t, err)
	assert.Equal(t, "voice", msg.InputMethod)
	assert.Equal(t, []byte("hello"), msg.Audio)
	assert.Equal(t, "audio/ogg", msg.AudioMIME)
	assert.Equal(t, "ogg", msg.AudioExt())
}

func TestParseWSMessage_CancelTTS(t *testing.T) {
	msg, err := interview.ParseWSMessage([]byte(`{"type":"cancel_tts"}`))
	require.NoError(t, err)
	assert.Equal(t, "cancel_tts", msg.Type)
}

func TestParseWSMessage_InvalidJSON(t *testing.T) {
	_, err := interview.ParseWSMessage([]byte(`not json`))
	require.Error(t, err)
}

func TestParseWSMessage_MissingType(t *testing.T) {
	_, err := interview.ParseWSMessage([]byte(`{"content":"hello"}`))
	require.Error(t, err)
}
