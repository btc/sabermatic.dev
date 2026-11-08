package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestGeminiClient_GenerateImage_Request checks the Vertex AI request the SDK
// builds from GenerateImage: model path, IMAGE modality and 4:3 aspect ratio
// with no output format option, and that the inline image part and its MIME
// type are returned.
func TestGeminiClient_GenerateImage_Request(t *testing.T) {
	png := []byte("\x89PNG fake")
	var gotPath string
	var gotBody map[string]any

	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotPath = r.URL.Path
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &gotBody); err != nil {
			return nil, err
		}
		resp := `{"candidates":[{"content":{"role":"model","parts":[{"inlineData":{"mimeType":"image/png","data":"` +
			base64.StdEncoding.EncodeToString(png) + `"}}]}}]}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewBufferString(resp)),
			Request:    r,
		}, nil
	})

	g := NewTestGeminiClient("gemini-3.1-flash-image", &http.Client{Transport: transport})
	data, mimeType, err := g.GenerateImage(context.Background(), "a load balancer")
	require.NoError(t, err)
	require.Equal(t, png, data)
	require.Equal(t, "image/png", mimeType)

	require.True(t, strings.HasSuffix(gotPath, "/publishers/google/models/gemini-3.1-flash-image:generateContent"), gotPath)

	gen, ok := gotBody["generationConfig"].(map[string]any)
	require.True(t, ok, "generationConfig missing: %v", gotBody)
	require.Equal(t, []any{"IMAGE"}, gen["responseModalities"])
	img, ok := gen["imageConfig"].(map[string]any)
	require.True(t, ok, "imageConfig missing: %v", gen)
	require.Equal(t, "4:3", img["aspectRatio"])
	require.NotContains(t, img, "imageOutputOptions", "output format is not pinned")
}
