package ai

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"mime"
	"strconv"
	"strings"

	"google.golang.org/genai"

	"github.com/btc/drill/internal/drilotel"
)

// Gemini text-to-speech returns 16-bit mono PCM at 24 kHz. A unary
// generateContent response is documented as audio/wav (with a RIFF header);
// streaming responses are headerless audio/l16. Both are handled so the
// browser always receives a WAV file it can decode with decodeAudioData.
const (
	geminiPCMDefaultRate     = 24000
	geminiPCMDefaultChannels = 1
	pcmBitsPerSample         = 16
)

// GeminiSynthesizer implements Synthesizer with a Gemini text-to-speech model
// on Vertex AI (for example gemini-3.8-flash-lite-tts). Each call synthesizes
// one sentence with a unary generateContent request and returns a WAV file.
type GeminiSynthesizer struct {
	gemini *GeminiClient
	voice  string
	style  string
}

// NewGeminiSynthesizer returns a GeminiSynthesizer that sends requests
// through gemini, whose model must be a Gemini text-to-speech model. voice is
// a prebuilt voice name such as "Charon". style is an optional delivery
// instruction, sent apart from the text, which the model reads verbatim.
func NewGeminiSynthesizer(gemini *GeminiClient, voice, style string) *GeminiSynthesizer {
	return &GeminiSynthesizer{gemini: gemini, voice: voice, style: style}
}

// Synthesize converts text to speech and returns a WAV file. The caller is
// responsible for closing the returned ReadCloser.
func (s *GeminiSynthesizer) Synthesize(ctx context.Context, text string) (_ io.ReadCloser, err error) {
	ctx, span := geminiTracer.Start(ctx, "GeminiSynthesizer.Synthesize")
	defer func() { drilotel.End(span, err) }()

	if strings.TrimSpace(text) == "" {
		return nil, errors.New("gemini tts: empty text")
	}

	client, err := s.gemini.genaiClient(ctx)
	if err != nil {
		return nil, err
	}

	part := &genai.Part{Text: text}
	if s.style != "" {
		part.SpeechMetadata = &genai.SpeechMetadata{Style: s.style}
	}
	result, err := client.Models.GenerateContent(ctx, s.gemini.model,
		[]*genai.Content{{Role: genai.RoleUser, Parts: []*genai.Part{part}}},
		&genai.GenerateContentConfig{
			ResponseModalities: []string{"AUDIO"},
			SpeechConfig: &genai.SpeechConfig{
				VoiceConfig: &genai.VoiceConfig{Voice: s.voice},
			},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("gemini tts: %w", err)
	}

	wav, err := wavFromGeminiAudio(result)
	if err != nil {
		return nil, fmt.Errorf("gemini tts: %w", err)
	}
	return io.NopCloser(bytes.NewReader(wav)), nil
}

// wavFromGeminiAudio extracts the audio from a text-to-speech response and
// returns it as a WAV file. A single audio/wav part is returned unchanged.
// Raw PCM parts (audio/l16 or audio/pcm) are concatenated and given a WAV
// header, using the rate and channels parameters of the MIME type when
// present.
func wavFromGeminiAudio(result *genai.GenerateContentResponse) ([]byte, error) {
	if result == nil || len(result.Candidates) == 0 || result.Candidates[0].Content == nil {
		return nil, errors.New("no candidates returned")
	}

	var parts []*genai.Blob
	for _, p := range result.Candidates[0].Content.Parts {
		if p != nil && p.InlineData != nil && len(p.InlineData.Data) > 0 {
			parts = append(parts, p.InlineData)
		}
	}
	if len(parts) == 0 {
		return nil, errors.New("no audio data in response")
	}

	var pcm bytes.Buffer
	rate, channels := 0, 0
	for _, b := range parts {
		mediaType, params, err := mime.ParseMediaType(b.MIMEType)
		if err != nil {
			return nil, fmt.Errorf("parse audio MIME type %q: %w", b.MIMEType, err)
		}
		switch mediaType {
		case "audio/wav", "audio/x-wav", "audio/wave":
			if len(parts) != 1 {
				return nil, fmt.Errorf("got %d audio parts including a WAV part; expected one", len(parts))
			}
			return b.Data, nil
		case "audio/l16", "audio/pcm":
			r, err := mimeIntParam(params, "rate", geminiPCMDefaultRate)
			if err != nil {
				return nil, err
			}
			c, err := mimeIntParam(params, "channels", geminiPCMDefaultChannels)
			if err != nil {
				return nil, err
			}
			if rate != 0 && (r != rate || c != channels) {
				return nil, errors.New("audio parts have different sample rates or channel counts")
			}
			rate, channels = r, c
			pcm.Write(b.Data)
		default:
			return nil, fmt.Errorf("unsupported audio MIME type %q", b.MIMEType)
		}
	}
	return pcmToWAV(pcm.Bytes(), rate, channels), nil
}

func mimeIntParam(params map[string]string, name string, def int) (int, error) {
	v, ok := params[name]
	if !ok {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid audio MIME parameter %s=%q", name, v)
	}
	return n, nil
}

// pcmToWAV prepends a 44-byte RIFF/WAVE header to 16-bit little-endian PCM.
func pcmToWAV(pcm []byte, rate, channels int) []byte {
	const headerLen = 44
	blockAlign := channels * pcmBitsPerSample / 8
	out := make([]byte, headerLen+len(pcm))
	copy(out[0:4], "RIFF")
	binary.LittleEndian.PutUint32(out[4:8], uint32(36+len(pcm)))
	copy(out[8:12], "WAVE")
	copy(out[12:16], "fmt ")
	binary.LittleEndian.PutUint32(out[16:20], 16) // PCM fmt chunk size
	binary.LittleEndian.PutUint16(out[20:22], 1)  // audio format: PCM
	binary.LittleEndian.PutUint16(out[22:24], uint16(channels))
	binary.LittleEndian.PutUint32(out[24:28], uint32(rate))
	binary.LittleEndian.PutUint32(out[28:32], uint32(rate*blockAlign))
	binary.LittleEndian.PutUint16(out[32:34], uint16(blockAlign))
	binary.LittleEndian.PutUint16(out[34:36], pcmBitsPerSample)
	copy(out[36:40], "data")
	binary.LittleEndian.PutUint32(out[40:44], uint32(len(pcm)))
	copy(out[headerLen:], pcm)
	return out
}
