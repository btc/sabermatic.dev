package ai_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/ai"
	"github.com/btc/drill/internal/config"
)

func TestGeminiClient_GenerateImage_EmptyPrompt(t *testing.T) {
	client := &ai.GeminiClient{}
	_, _, err := client.GenerateImage(context.Background(), "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty prompt")
}

// NewGeminiClient must not need Google credentials: the server has to start
// (and tests have to run) on machines without ADC. Credential errors surface
// from GenerateImage instead, and a failed init is retried on the next call.
func TestGeminiClient_LazyInit_CredentialErrorFromGenerateImage(t *testing.T) {
	// Point ADC at a path that does not exist so credential lookup fails
	// deterministically, whether or not this machine has real ADC. No API
	// call is made: client creation fails before any request.
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", filepath.Join(t.TempDir(), "missing.json"))

	cfg := &config.Config{}
	cfg.GCP.ProjectID = "test-project"
	cfg.Gemini.Location = "global"
	cfg.Gemini.Model = "test-model"

	client := ai.NewGeminiClient(cfg)
	require.NotNil(t, client)

	for range 2 {
		_, _, err := client.GenerateImage(context.Background(), "a prompt")
		require.Error(t, err)
		require.Contains(t, err.Error(), "create gemini client")
	}
}
