package ai

import (
	"context"
	"fmt"
	"io"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

// Synthesizer converts text to audio.
type Synthesizer interface {
	Synthesize(ctx context.Context, text string) (io.ReadCloser, error)
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
func (s *OpenAISynthesizer) Synthesize(ctx context.Context, text string) (io.ReadCloser, error) {
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
