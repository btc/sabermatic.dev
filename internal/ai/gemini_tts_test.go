package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/genai"
)

// fakeTTSTransport answers every request with one candidate whose parts are
// the given inline audio blobs, and records the request path and JSON body.
func fakeTTSTransport(t *testing.T, gotPath *string, gotBody *map[string]any, blobs ...genai.Blob) roundTripFunc {
	t.Helper()
	return func(r *http.Request) (*http.Response, error) {
		*gotPath = r.URL.Path
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, gotBody); err != nil {
			return nil, err
		}
		parts := make([]string, 0, len(blobs))
		for _, b := range blobs {
			parts = append(parts, `{"inlineData":{"mimeType":"`+b.MIMEType+`","data":"`+
				base64.StdEncoding.EncodeToString(b.Data)+`"}}`)
		}
		resp := `{"candidates":[{"content":{"role":"model","parts":[` + strings.Join(parts, ",") + `]}}]}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewBufferString(resp)),
			Request:    r,
		}, nil
	}
}

func readAllClose(t *testing.T, rc io.ReadCloser) []byte {
	t.Helper()
	data, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.NoError(t, rc.Close())
	return data
}

// TestGeminiSynthesizer_Request checks the Vertex AI request: model path,
// AUDIO modality, voice name, the text as the only content, and the style
// sent as speechMetadata beside the text rather than inside it.
func TestGeminiSynthesizer_Request(t *testing.T) {
	wav := pcmToWAV([]byte{1, 0, 2, 0}, 24000, 1)
	var gotPath string
	var gotBody map[string]any
	transport := fakeTTSTransport(t, &gotPath, &gotBody, genai.Blob{MIMEType: "audio/wav", Data: wav})

	g := NewTestGeminiClient("gemini-3.8-flash-lite-tts", &http.Client{Transport: transport})
	s := NewGeminiSynthesizer(g, "Charon", "calm, measured")
	rc, err := s.Synthesize(context.Background(), "Walk me through the write path.")
	require.NoError(t, err)
	require.Equal(t, wav, readAllClose(t, rc))

	require.True(t, strings.HasSuffix(gotPath, "/publishers/google/models/gemini-3.8-flash-lite-tts:generateContent"), gotPath)

	gen, ok := gotBody["generationConfig"].(map[string]any)
	require.True(t, ok, "generationConfig missing: %v", gotBody)
	require.Equal(t, []any{"AUDIO"}, gen["responseModalities"])
	require.Equal(t, map[string]any{"voiceConfig": map[string]any{"voice": "Charon"}}, gen["speechConfig"])

	contents, ok := gotBody["contents"].([]any)
	require.True(t, ok)
	require.Len(t, contents, 1)
	content := contents[0].(map[string]any)
	require.Equal(t, "user", content["role"])
	require.Equal(t, []any{map[string]any{
		"text":           "Walk me through the write path.",
		"speechMetadata": map[string]any{"style": "calm, measured"},
	}}, content["parts"])
	require.NotContains(t, gotBody, "systemInstruction", "TTS models do not accept system instructions")
}

func TestGeminiSynthesizer_NoStyle(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	transport := fakeTTSTransport(t, &gotPath, &gotBody,
		genai.Blob{MIMEType: "audio/wav", Data: pcmToWAV([]byte{0, 0}, 24000, 1)})

	s := NewGeminiSynthesizer(NewTestGeminiClient("m", &http.Client{Transport: transport}), "Orus", "")
	rc, err := s.Synthesize(context.Background(), "Hello.")
	require.NoError(t, err)
	readAllClose(t, rc)

	part := gotBody["contents"].([]any)[0].(map[string]any)["parts"].([]any)[0].(map[string]any)
	require.Equal(t, map[string]any{"text": "Hello."}, part)
}

// TestGeminiSynthesizer_RawPCM checks that headerless PCM parts are joined
// in order and returned as one WAV file at the rate the MIME type names.
func TestGeminiSynthesizer_RawPCM(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	transport := fakeTTSTransport(t, &gotPath, &gotBody,
		genai.Blob{MIMEType: "audio/L16;codec=pcm;rate=16000", Data: []byte{1, 0, 2, 0}},
		genai.Blob{MIMEType: "audio/L16;codec=pcm;rate=16000", Data: []byte{3, 0}},
	)

	s := NewGeminiSynthesizer(NewTestGeminiClient("m", &http.Client{Transport: transport}), "Charon", "")
	rc, err := s.Synthesize(context.Background(), "Hello.")
	require.NoError(t, err)
	got := readAllClose(t, rc)
	require.Equal(t, pcmToWAV([]byte{1, 0, 2, 0, 3, 0}, 16000, 1), got)
}

func TestGeminiSynthesizer_Errors(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		blobs   []genai.Blob
		wantErr string
	}{
		{name: "empty text", text: "  ", wantErr: "empty text"},
		{name: "no audio", text: "Hi.", wantErr: "no audio data"},
		{name: "unsupported format", text: "Hi.", blobs: []genai.Blob{{MIMEType: "audio/mpeg", Data: []byte{1}}}, wantErr: "unsupported audio MIME type"},
		{name: "bad rate", text: "Hi.", blobs: []genai.Blob{{MIMEType: "audio/l16;rate=abc", Data: []byte{1, 0}}}, wantErr: "rate"},
		{name: "mixed rates", text: "Hi.", blobs: []genai.Blob{
			{MIMEType: "audio/l16;rate=24000", Data: []byte{1, 0}},
			{MIMEType: "audio/l16;rate=16000", Data: []byte{1, 0}},
		}, wantErr: "different sample rates"},
		{name: "wav plus more", text: "Hi.", blobs: []genai.Blob{
			{MIMEType: "audio/wav", Data: []byte("RIFF")},
			{MIMEType: "audio/l16", Data: []byte{1, 0}},
		}, wantErr: "expected one"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPath string
			var gotBody map[string]any
			transport := fakeTTSTransport(t, &gotPath, &gotBody, tt.blobs...)
			s := NewGeminiSynthesizer(NewTestGeminiClient("m", &http.Client{Transport: transport}), "Charon", "")
			_, err := s.Synthesize(context.Background(), tt.text)
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

// TestPCMToWAV checks every field of the 44-byte header against the WAV
// (RIFF) format for 16-bit PCM.
func TestPCMToWAV(t *testing.T) {
	pcm := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	w := pcmToWAV(pcm, 24000, 2)
	le := binary.LittleEndian

	require.Len(t, w, 44+len(pcm))
	require.Equal(t, "RIFF", string(w[0:4]))
	require.Equal(t, uint32(36+len(pcm)), le.Uint32(w[4:8]))
	require.Equal(t, "WAVE", string(w[8:12]))
	require.Equal(t, "fmt ", string(w[12:16]))
	require.Equal(t, uint32(16), le.Uint32(w[16:20]))
	require.Equal(t, uint16(1), le.Uint16(w[20:22]))
	require.Equal(t, uint16(2), le.Uint16(w[22:24]))
	require.Equal(t, uint32(24000), le.Uint32(w[24:28]))
	require.Equal(t, uint32(24000*4), le.Uint32(w[28:32]))
	require.Equal(t, uint16(4), le.Uint16(w[32:34]))
	require.Equal(t, uint16(16), le.Uint16(w[34:36]))
	require.Equal(t, "data", string(w[36:40]))
	require.Equal(t, uint32(len(pcm)), le.Uint32(w[40:44]))
	require.Equal(t, pcm, w[44:])
}

// fakeTTSStreamTransport answers a streamGenerateContent request with one
// server-sent event per blob, and records the request path and query.
func fakeTTSStreamTransport(t *testing.T, gotURL *string, blobs ...genai.Blob) roundTripFunc {
	t.Helper()
	return func(r *http.Request) (*http.Response, error) {
		*gotURL = r.URL.Path + "?" + r.URL.RawQuery
		var sse strings.Builder
		for _, b := range blobs {
			sse.WriteString(`data: {"candidates":[{"content":{"role":"model","parts":[{"inlineData":{"mimeType":"` +
				b.MIMEType + `","data":"` + base64.StdEncoding.EncodeToString(b.Data) + `"}}]}}]}` + "\n\n")
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(sse.String())),
			Request:    r,
		}, nil
	}
}

type emitted struct {
	data     []byte
	mimeType string
}

func collectStream(t *testing.T, s *GeminiSynthesizer, text string) ([]emitted, error) {
	t.Helper()
	var got []emitted
	err := s.SynthesizeStream(context.Background(), text, func(pcm []byte, mimeType string) error {
		got = append(got, emitted{append([]byte(nil), pcm...), mimeType})
		return nil
	})
	return got, err
}

// TestGeminiSynthesizer_Stream checks the streaming endpoint is used, that
// pieces are forwarded in order with a normalized MIME type, and that a
// piece ending mid-sample carries its odd byte into the next piece.
func TestGeminiSynthesizer_Stream(t *testing.T) {
	var gotURL string
	transport := fakeTTSStreamTransport(t, &gotURL,
		genai.Blob{MIMEType: "audio/l16; rate=24000; channels=1", Data: []byte{1, 0, 2}},
		genai.Blob{MIMEType: "audio/l16; rate=24000; channels=1", Data: []byte{0, 3, 0}},
		genai.Blob{MIMEType: "audio/l16; rate=24000; channels=1", Data: []byte{4, 0, 9}}, // trailing partial sample dropped
	)
	s := NewGeminiSynthesizer(NewTestGeminiClient("gemini-3.8-flash-lite-tts", &http.Client{Transport: transport}), "Charon", "")

	got, err := collectStream(t, s, "Hello there.")
	require.NoError(t, err)
	require.Contains(t, gotURL, "/publishers/google/models/gemini-3.8-flash-lite-tts:streamGenerateContent")
	require.Contains(t, gotURL, "alt=sse")
	require.Equal(t, []emitted{
		{[]byte{1, 0}, "audio/L16;rate=24000;channels=1"},
		{[]byte{2, 0, 3, 0}, "audio/L16;rate=24000;channels=1"},
		{[]byte{4, 0}, "audio/L16;rate=24000;channels=1"},
	}, got)
}

func TestGeminiSynthesizer_StreamErrors(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		blobs   []genai.Blob
		wantErr string
	}{
		{name: "empty text", text: " ", wantErr: "empty text"},
		{name: "no audio", text: "Hi.", wantErr: "no audio data"},
		{name: "wav not accepted", text: "Hi.", blobs: []genai.Blob{{MIMEType: "audio/wav", Data: []byte("RIFF")}}, wantErr: "unsupported streaming audio MIME type"},
		{name: "format change", text: "Hi.", blobs: []genai.Blob{
			{MIMEType: "audio/l16;rate=24000", Data: []byte{1, 0}},
			{MIMEType: "audio/l16;rate=16000", Data: []byte{1, 0}},
		}, wantErr: "audio format changed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotURL string
			s := NewGeminiSynthesizer(NewTestGeminiClient("m", &http.Client{Transport: fakeTTSStreamTransport(t, &gotURL, tt.blobs...)}), "Charon", "")
			_, err := collectStream(t, s, tt.text)
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestGeminiSynthesizer_StreamEmitErrorStops(t *testing.T) {
	var gotURL string
	transport := fakeTTSStreamTransport(t, &gotURL,
		genai.Blob{MIMEType: "audio/l16;rate=24000", Data: []byte{1, 0}},
		genai.Blob{MIMEType: "audio/l16;rate=24000", Data: []byte{2, 0}},
	)
	s := NewGeminiSynthesizer(NewTestGeminiClient("m", &http.Client{Transport: transport}), "Charon", "")
	stop := errors.New("stop")
	calls := 0
	err := s.SynthesizeStream(context.Background(), "Hi.", func([]byte, string) error {
		calls++
		return stop
	})
	require.ErrorIs(t, err, stop)
	require.Equal(t, 1, calls)
}

var _ StreamingSynthesizer = (*GeminiSynthesizer)(nil)
