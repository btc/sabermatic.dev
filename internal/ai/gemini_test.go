package ai_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/ai"
)

func TestGeminiClient_GenerateImage_EmptyPrompt(t *testing.T) {
	client := &ai.GeminiClient{}
	_, _, err := client.GenerateImage(context.Background(), "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty prompt")
}

func TestGeminiClient_GenerateImage_NilClient(t *testing.T) {
	client := &ai.GeminiClient{}
	_, _, err := client.GenerateImage(context.Background(), "test prompt")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not initialized")
}
