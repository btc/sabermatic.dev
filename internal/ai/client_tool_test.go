package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCallToolAndLog_NilTx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		//nolint:errcheck
		fmt.Fprint(w, `{
			"id": "msg_test",
			"type": "message",
			"role": "assistant",
			"content": [{"type": "tool_use", "id": "toolu_test", "name": "submit_evaluation", "input": {"scores": {"requirements": 3}}}],
			"model": "claude-opus-4-20250514",
			"stop_reason": "tool_use",
			"usage": {"input_tokens": 100, "output_tokens": 50}
		}`)

	}))
	defer srv.Close()

	client := NewTestClient(srv.URL, nil)

	toolInput, err := client.CallToolAndLog(context.Background(), nil, CallToolParams{
		Model:  "claude-opus-4-20250514",
		System: "You are an evaluator.",
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock("Evaluate this session.")),
		},
		MaxTokens: 1024,
		UserID:    uuid.New(),
		Role:      "evaluator",
		SessionID: uuid.New(),
		Tools: []anthropic.ToolUnionParam{
			{OfTool: &anthropic.ToolParam{
				Name:        "submit_evaluation",
				Description: anthropic.String("description"),
				InputSchema: anthropic.ToolInputSchemaParam{
					Properties: map[string]any{
						"scores": map[string]any{"type": "object"},
					},
				},
			}},
		},
		ToolChoice: anthropic.ToolChoiceParamOfTool("submit_evaluation"),
	})
	require.NoError(t, err)
	require.NotNil(t, toolInput)

	// Verify the returned JSON contains the expected tool input data.
	var got map[string]any
	require.NoError(t, json.Unmarshal(toolInput, &got))

	scores, ok := got["scores"].(map[string]any)
	require.True(t, ok, "expected scores key in tool input")
	assert.Equal(t, float64(3), scores["requirements"])
}

func TestCallToolAndLog_NoToolUseBlock(t *testing.T) {
	// Server returns a text response instead of tool_use — should error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		//nolint:errcheck
		fmt.Fprint(w, `{
			"id": "msg_test",
			"type": "message",
			"role": "assistant",
			"content": [{"type": "text", "text": "Here is my evaluation."}],
			"model": "claude-opus-4-20250514",
			"stop_reason": "end_turn",
			"usage": {"input_tokens": 50, "output_tokens": 20}
		}`)
	}))
	defer srv.Close()

	client := NewTestClient(srv.URL, nil)

	_, err := client.CallToolAndLog(context.Background(), nil, CallToolParams{
		Model: "claude-opus-4-20250514",
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock("Evaluate this.")),
		},
		UserID: uuid.New(),
		Role:   "evaluator",
		Tools: []anthropic.ToolUnionParam{
			{OfTool: &anthropic.ToolParam{
				Name:        "submit_evaluation",
				Description: anthropic.String("description"),
				InputSchema: anthropic.ToolInputSchemaParam{},
			}},
		},
		ToolChoice: anthropic.ToolChoiceParamOfTool("submit_evaluation"),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no tool_use block in response")
}
