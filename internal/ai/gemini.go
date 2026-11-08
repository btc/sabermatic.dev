package ai

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"google.golang.org/genai"

	"github.com/btc/drill/internal/config"
	"github.com/btc/drill/internal/drilotel"
)

var geminiTracer = drilotel.Tracer("gemini")

// GeminiClient wraps the Google Gen AI SDK for image generation.
//
// The underlying genai client is created on the first GenerateImage call, not
// at construction. Creating it resolves Google Application Default Credentials
// (ADC), and image generation is the only feature that needs them, so the
// server starts without ADC and only image-generation jobs fail. A failed
// creation is not cached: the next call (e.g. the River retry) tries again.
type GeminiClient struct {
	cfg   genai.ClientConfig
	model string

	mu     sync.Mutex
	client *genai.Client
}

// NewGeminiClient returns a GeminiClient configured for Vertex AI with ADC.
// It does not contact Google or read credentials; see GeminiClient.
func NewGeminiClient(cfg *config.Config) *GeminiClient {
	return &GeminiClient{
		cfg: genai.ClientConfig{
			Project:  cfg.GCP.ProjectID,
			Location: cfg.Gemini.Location,
			Backend:  genai.BackendVertexAI,
		},
		model: cfg.Gemini.Model,
	}
}

// genaiClient returns the genai client, creating it on first use.
func (g *GeminiClient) genaiClient(ctx context.Context) (*genai.Client, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.client != nil {
		return g.client, nil
	}
	// genai.NewClient writes resolved values (e.g. Credentials) into the config
	// it is given; pass a copy so a failed attempt leaves g.cfg untouched for
	// the next retry. The client outlives this call (ctx is a job context), so
	// detach cancellation in case the SDK holds on to ctx.
	cc := g.cfg
	client, err := genai.NewClient(context.WithoutCancel(ctx), &cc)
	if err != nil {
		return nil, fmt.Errorf("create gemini client: %w", err)
	}
	g.client = client
	return client, nil
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

	client, err := g.genaiClient(ctx)
	if err != nil {
		return nil, "", err
	}

	result, err := client.Models.GenerateContent(ctx, g.model,
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
