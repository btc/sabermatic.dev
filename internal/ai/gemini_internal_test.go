package ai

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/genai"

	"github.com/btc/drill/internal/config"
)

// imageTransport answers every request with a generateContent response that
// carries one inline image, so no request leaves the process.
type imageTransport struct {
	mimeType string
	data     []byte
}

func (rt imageTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body := fmt.Sprintf(`{"candidates":[{"content":{"role":"model","parts":[{"inlineData":{"mimeType":%q,"data":%q}}]}}]}`,
		rt.mimeType, base64.StdEncoding.EncodeToString(rt.data))
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}, nil
}

func testGeminiConfig() *config.Config {
	cfg := &config.Config{}
	cfg.GCP.ProjectID = "test-project"
	cfg.Gemini.Location = "global"
	cfg.Gemini.Model = "test-model"
	return cfg
}

// A failed client creation must not be cached: after one failure, the next
// call creates the client and generates the image. A successful client is
// reused without creating another.
func TestGeminiClient_FailedInitIsRetried(t *testing.T) {
	g := NewGeminiClient(testGeminiConfig())

	wantImage := []byte("fake-image-bytes")
	calls := 0
	g.newClient = func(ctx context.Context, cc *genai.ClientConfig) (*genai.Client, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("failed to find default credentials")
		}
		// An explicit HTTPClient makes the SDK skip ADC lookup.
		cc.HTTPClient = &http.Client{Transport: imageTransport{mimeType: "image/jpeg", data: wantImage}}
		return genai.NewClient(ctx, cc)
	}

	_, _, err := g.GenerateImage(context.Background(), "a prompt")
	require.ErrorIs(t, err, ErrGeminiUnavailable)
	require.Equal(t, 1, calls)

	data, mimeType, err := g.GenerateImage(context.Background(), "a prompt")
	require.NoError(t, err)
	require.Equal(t, wantImage, data)
	require.Equal(t, "image/jpeg", mimeType)
	require.Equal(t, 2, calls)

	require.NoError(t, g.Ready(context.Background()))
	require.Equal(t, 2, calls, "a created client must be reused")

	// The failed attempt's writes went to a copy, not g.cfg.
	require.Nil(t, g.cfg.HTTPClient)
	require.Nil(t, g.cfg.Credentials)
}

// A caller waiting for another caller's client creation stops waiting when
// its own ctx ends.
func TestGeminiClient_InitWaitRespectsContext(t *testing.T) {
	g := NewGeminiClient(testGeminiConfig())
	g.initSem <- struct{}{} // another caller is creating the client
	defer func() { <-g.initSem }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := g.Ready(ctx)
	require.ErrorIs(t, err, context.Canceled)
}

func TestGeminiClient_ZeroValueReportsUnavailable(t *testing.T) {
	var g GeminiClient
	require.ErrorIs(t, g.Ready(context.Background()), ErrGeminiUnavailable)
}
