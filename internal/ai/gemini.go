package ai

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/genai"

	"github.com/btc/drill/internal/config"
	"github.com/btc/drill/internal/drilotel"
)

var geminiTracer = drilotel.Tracer("gemini")

// ErrGeminiUnavailable wraps a failure to create the genai client, which in
// practice means Google Application Default Credentials (ADC) are missing or
// unusable. Callers can treat it as a configuration problem rather than a
// transient API error.
var ErrGeminiUnavailable = errors.New("gemini client unavailable")

// geminiInitTimeout bounds one attempt to create the genai client. Creation
// can query the GCE metadata server (quota project lookup); the bound keeps a
// slow metadata server from holding the init slot indefinitely.
const geminiInitTimeout = 30 * time.Second

// GeminiClient wraps the Google Gen AI SDK for image generation.
//
// The underlying genai client is created on first use (Ready or
// GenerateImage), not at construction. Creating it resolves ADC, and image
// generation is the only feature that needs them, so the server starts
// without ADC and only image generation stops. A failed creation is not
// cached: the next call tries again.
type GeminiClient struct {
	cfg   genai.ClientConfig
	model string

	// newClient creates the genai client. It is genai.NewClient outside tests.
	newClient func(context.Context, *genai.ClientConfig) (*genai.Client, error)

	// initSem is a one-slot semaphore guarding client creation. A channel
	// rather than a sync.Mutex lets waiters give up when their ctx ends.
	initSem chan struct{}
	client  *genai.Client // guarded by initSem
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
		model:     cfg.Gemini.Model,
		newClient: genai.NewClient,
		initSem:   make(chan struct{}, 1),
	}
}

// Ready creates the genai client if it does not exist yet and reports
// whether that succeeded. A creation failure wraps ErrGeminiUnavailable.
// Call it before doing paid work whose output only Gemini can use.
func (g *GeminiClient) Ready(ctx context.Context) error {
	_, err := g.genaiClient(ctx)
	return err
}

// genaiClient returns the genai client, creating it on first use.
func (g *GeminiClient) genaiClient(ctx context.Context) (*genai.Client, error) {
	if g.initSem == nil || g.newClient == nil {
		return nil, fmt.Errorf("%w: GeminiClient not created with NewGeminiClient", ErrGeminiUnavailable)
	}
	select {
	case g.initSem <- struct{}{}:
	case <-ctx.Done():
		return nil, fmt.Errorf("wait for gemini client: %w", ctx.Err())
	}
	defer func() { <-g.initSem }()
	if g.client != nil {
		return g.client, nil
	}
	// genai.NewClient writes resolved values (e.g. Credentials) into the
	// config it is given; pass a copy so a failed attempt leaves g.cfg
	// untouched for the next try. The copy is shallow: in genai v1.53.0
	// NewClient assigns only top-level fields (HTTPOptions is a value field
	// and g.cfg.HTTPClient is nil), so no state shared with g.cfg is
	// modified. Recheck this when bumping the SDK. The SDK uses ctx only
	// during creation (quota project lookup) and does not retain it.
	initCtx, cancel := context.WithTimeout(ctx, geminiInitTimeout)
	defer cancel()
	cc := g.cfg
	client, err := g.newClient(initCtx, &cc)
	if err != nil {
		return nil, fmt.Errorf("%w: create gemini client: %w", ErrGeminiUnavailable, err)
	}
	g.client = client
	return client, nil
}

// GenerateImage generates an image from a text prompt using Gemini's native
// image generation API (GenerateContent with IMAGE response modality).
// Returns image bytes and MIME type. Uses 4:3 aspect ratio for card images.
// Output is pinned to PNG because the image job stores objects under a .png
// key; Vertex accepts ImageConfig.OutputMIMEType (the SDK sends it as
// imageOutputOptions.mimeType), the Gemini Developer API does not.
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
				AspectRatio:    "4:3",
				OutputMIMEType: "image/png",
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
