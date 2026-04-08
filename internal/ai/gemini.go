package ai

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/genai"

	"github.com/btc/drill/internal/drilotel"
)

var geminiTracer = drilotel.Tracer("gemini")

// GeminiClient wraps the Google Gen AI SDK for image generation.
type GeminiClient struct {
	client *genai.Client
	model  string
}

// NewGeminiClient creates a GeminiClient using Vertex AI with ADC.
func NewGeminiClient(ctx context.Context, project, location, model string) (*GeminiClient, error) {
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		Project:  project,
		Location: location,
		Backend:  genai.BackendVertexAI,
	})
	if err != nil {
		return nil, fmt.Errorf("create gemini client: %w", err)
	}
	return &GeminiClient{client: client, model: model}, nil
}

// GenerateImage generates an image from a text prompt using Gemini's native
// image generation API (GenerateContent with IMAGE response modality).
// Returns image bytes and MIME type. Uses 4:3 aspect ratio for card images.
func (g *GeminiClient) GenerateImage(ctx context.Context, prompt string) (_ []byte, _ string, err error) {
	ctx, span := geminiTracer.Start(ctx, "GeminiClient.GenerateImage")
	defer func() { drilotel.End(span, err) }()

	if prompt == "" {
		return nil, "", errors.New("empty prompt")
	}
	if g.client == nil {
		return nil, "", errors.New("gemini client not initialized")
	}

	result, err := g.client.Models.GenerateContent(ctx, g.model,
		genai.Text(prompt),
		&genai.GenerateContentConfig{
			ResponseModalities: []string{"IMAGE"},
			ImageConfig: &genai.ImageConfig{
				AspectRatio: "4:3",
			},
		},
	)
	if err != nil {
		return nil, "", fmt.Errorf("gemini generate: %w", err)
	}

	if len(result.Candidates) == 0 || result.Candidates[0].Content == nil {
		return nil, "", errors.New("gemini: no candidates returned")
	}

	for _, part := range result.Candidates[0].Content.Parts {
		if part.InlineData != nil && len(part.InlineData.Data) > 0 {
			return part.InlineData.Data, part.InlineData.MIMEType, nil
		}
	}

	return nil, "", errors.New("gemini: no image data in response")
}
