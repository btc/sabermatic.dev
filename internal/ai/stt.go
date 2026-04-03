package ai

import (
	"bytes"
	"context"
	"fmt"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

// Transcriber converts audio bytes to text.
type Transcriber interface {
	Transcribe(ctx context.Context, audio []byte, format string) (string, error)
}

// OpenAITranscriber calls the OpenAI Whisper transcription API.
type OpenAITranscriber struct {
	client openai.Client
	model  string
}

// NewOpenAITranscriber constructs an OpenAITranscriber with the given API key and model.
func NewOpenAITranscriber(apiKey, model string) *OpenAITranscriber {
	return &OpenAITranscriber{
		client: openai.NewClient(option.WithAPIKey(apiKey)),
		model:  model,
	}
}

// Transcribe sends audio bytes to the Whisper API and returns the transcript text.
// The format parameter is the audio container format (e.g. "webm", "mp3", "wav").
func (t *OpenAITranscriber) Transcribe(ctx context.Context, audio []byte, format string) (string, error) {
	filename := fmt.Sprintf("audio.%s", format)
	reader := newNamedReader(filename, bytes.NewReader(audio))

	resp, err := t.client.Audio.Transcriptions.New(ctx, openai.AudioTranscriptionNewParams{
		File:  reader,
		Model: t.model,
	})
	if err != nil {
		return "", fmt.Errorf("openai transcription: %w", err)
	}

	return resp.Text, nil
}

// namedReader wraps an io.Reader and provides a Name method so that the
// multipart encoder can set the filename in the Content-Disposition header.
type namedReader struct {
	name   string
	reader interface {
		Read(p []byte) (n int, err error)
	}
}

func newNamedReader(name string, r *bytes.Reader) *namedReader {
	return &namedReader{name: name, reader: r}
}

func (n *namedReader) Read(p []byte) (int, error) {
	return n.reader.Read(p)
}

func (n *namedReader) Name() string {
	return n.name
}
