package ai

import (
	"context"
	"fmt"
	"io"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"

	"github.com/btc/drill/internal/drilotel"
)

// Synthesizer converts text to audio.
type Synthesizer interface {
	Synthesize(ctx context.Context, text string) (io.ReadCloser, error)
}

// StreamingSynthesizer is implemented by synthesizers that can return audio
// while the vendor is still generating it. SynthesizeStream calls emit with
// each piece in order and returns once the whole text is synthesized, emit
// returns an error, or ctx ends. Every piece is a whole number of 16-bit
// little-endian PCM sample frames, and mimeType has the form
// "audio/L16;rate=R;channels=C" (see PCMMIMEType). If emit returns an error,
// SynthesizeStream stops and returns it.
type StreamingSynthesizer interface {
	SynthesizeStream(ctx context.Context, text string, emit func(pcm []byte, mimeType string) error) error
}

// PCMMIMEType returns the MIME type for 16-bit little-endian PCM at rate Hz
// with the given number of interleaved channels. The browser player parses
// this exact form.
func PCMMIMEType(rate, channels int) string {
	return fmt.Sprintf("audio/L16;rate=%d;channels=%d", rate, channels)
}

// OpenAISynthesizer calls the OpenAI TTS API.
type OpenAISynthesizer struct {
	client openai.Client
	model  string
	voice  openai.AudioSpeechNewParamsVoice
}

// NewOpenAISynthesizer constructs an OpenAISynthesizer with the given API key, model, and voice.
func NewOpenAISynthesizer(apiKey, model, voice string) *OpenAISynthesizer {
	return &OpenAISynthesizer{
		client: openai.NewClient(option.WithAPIKey(apiKey)),
		model:  model,
		voice:  openai.AudioSpeechNewParamsVoice(voice),
	}
}

// Synthesize sends text to the OpenAI TTS API and returns the streaming audio response body.
// The caller is responsible for closing the returned ReadCloser.
func (s *OpenAISynthesizer) Synthesize(ctx context.Context, text string) (_ io.ReadCloser, err error) {
	ctx, span := tracer.Start(ctx, "OpenAISynthesizer.Synthesize")
	defer func() { drilotel.End(span, err) }()

	resp, err := s.client.Audio.Speech.New(ctx, openai.AudioSpeechNewParams{
		Input: text,
		Model: s.model,
		Voice: s.voice,
	})
	if err != nil {
		return nil, fmt.Errorf("openai tts: %w", err)
	}

	return resp.Body, nil
}
