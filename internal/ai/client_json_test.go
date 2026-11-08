package ai

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/ai/aitest"
)

func testSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"answer": map[string]any{"type": "string"},
		},
		"required":             []string{"answer"},
		"additionalProperties": false,
	}
}

func TestCallJSONAndLog_RequestBody(t *testing.T) {
	srv := aitest.NewServer(t, aitest.Response{
		Model:  "claude-opus-5-5",
		Blocks: []aitest.Block{aitest.ThinkingBlock(), aitest.TextBlock(`{"answer":`, `"42"}`)},
	})
	client := NewTestClient(srv.URL, nil)

	// A nil tx skips persistence.
	out, err := client.CallJSONAndLog(context.Background(), nil, JSONParams{
		Model:     "claude-opus-5-5",
		System:    "sys",
		Messages:  userMsgs("Evaluate."),
		MaxTokens: 32000,
		Effort:    "high",
		Schema:    testSchema(),
		Role:      "evaluator",
	})
	require.NoError(t, err)
	assert.JSONEq(t, `{"answer":"42"}`, string(out))

	reqs := srv.Requests()
	require.Len(t, reqs, 1)
	body := reqs[0]
	assert.NotContains(t, body, "tool_choice")
	assert.NotContains(t, body, "tools")
	assert.NotContains(t, body, "thinking")
	assert.Equal(t, float64(32000), body["max_tokens"])
	assert.Equal(t, true, body["stream"])

	oc, ok := body["output_config"].(map[string]any)
	require.True(t, ok, "output_config missing: %v", body)
	assert.Equal(t, "high", oc["effort"])
	format, ok := oc["format"].(map[string]any)
	require.True(t, ok, "output_config.format missing: %v", oc)
	assert.Equal(t, "json_schema", format["type"])
	schema, ok := format["schema"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, false, schema["additionalProperties"])
	assert.Equal(t, "default", body["fallbacks"])
}

// TestCallJSONAndLog_MidStreamFallback pins the documented mid-stream
// fallback shape: the declined model's partial text block stays in content, a
// fallback block marks the switch, and the fallback model continues from the
// partial. The JSON is the concatenation of both text blocks, and the served
// model is the fallback target.
func TestCallJSONAndLog_MidStreamFallback(t *testing.T) {
	srv := aitest.NewServer(t, aitest.Response{
		Model: "claude-opus-5-5",
		Blocks: []aitest.Block{
			aitest.ThinkingBlock(),
			aitest.TextBlock(`{"answer":"a`),
			aitest.FallbackBlock("claude-opus-5-5", "claude-opus-4-8"),
			aitest.TextBlock(`b"}`),
		},
	})
	client := NewTestClient(srv.URL, nil)

	p := JSONParams{
		Model:    "claude-opus-5-5",
		Messages: userMsgs("Evaluate."),
		Schema:   testSchema(),
	}
	params := client.newParams(request{model: p.Model, messages: p.Messages, schema: p.Schema})
	resp, _, err := client.complete(context.Background(), params)
	require.NoError(t, err)
	model := servedModel(resp, p.Model)
	assert.Equal(t, "claude-opus-4-8", model)

	out, err := jsonOutput(resp, model, params.MaxTokens)
	require.NoError(t, err)
	assert.JSONEq(t, `{"answer":"ab"}`, string(out))

	// The public path returns the same JSON.
	out, err = client.CallJSONAndLog(context.Background(), nil, p)
	require.NoError(t, err)
	assert.JSONEq(t, `{"answer":"ab"}`, string(out))
}

func TestCallJSONAndLog_Refusal(t *testing.T) {
	srv := aitest.NewServer(t, aitest.Response{
		Model:           "claude-opus-5-5",
		StopReason:      "refusal",
		RefusalCategory: "bio",
	})
	client := NewTestClient(srv.URL, nil)

	_, err := client.CallJSONAndLog(context.Background(), nil, JSONParams{
		Model:    "claude-opus-5-5",
		Messages: userMsgs("Evaluate."),
		Schema:   testSchema(),
	})
	var refusal *RefusalError
	require.ErrorAs(t, err, &refusal)
	assert.Equal(t, "bio", refusal.Category)
	assert.Equal(t, "claude-opus-5-5", refusal.Model)
}

func TestCallJSONAndLog_MaxTokens(t *testing.T) {
	srv := aitest.NewServer(t, aitest.Response{
		Model:      "claude-opus-5-5",
		Blocks:     []aitest.Block{aitest.ThinkingBlock(), aitest.TextBlock(`{"answer":"tru`)},
		StopReason: "max_tokens",
	})
	client := NewTestClient(srv.URL, nil)

	_, err := client.CallJSONAndLog(context.Background(), nil, JSONParams{
		Model:     "claude-opus-5-5",
		Messages:  userMsgs("Evaluate."),
		MaxTokens: 32000,
		Schema:    testSchema(),
	})
	require.ErrorIs(t, err, ErrMaxTokens)
	assert.Contains(t, err.Error(), "32000")
}

func TestCallJSONAndLog_NoText(t *testing.T) {
	srv := aitest.NewServer(t, aitest.Response{
		Model:  "claude-opus-5-5",
		Blocks: []aitest.Block{aitest.ThinkingBlock()},
	})
	client := NewTestClient(srv.URL, nil)

	_, err := client.CallJSONAndLog(context.Background(), nil, JSONParams{
		Model:    "claude-opus-5-5",
		Messages: userMsgs("Evaluate."),
		Schema:   testSchema(),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no text content")
}

func TestCallJSONAndLog_RejectsUnsupportedSchema(t *testing.T) {
	srv := aitest.NewServer(t, aitest.Response{Model: "claude-opus-5-5"})
	client := NewTestClient(srv.URL, nil)

	schema := testSchema()
	schema["properties"].(map[string]any)["score"] = map[string]any{"type": "integer", "minimum": 1}
	schema["required"] = []string{"answer", "score"}

	_, err := client.CallJSONAndLog(context.Background(), nil, JSONParams{
		Model:    "claude-opus-5-5",
		Messages: userMsgs("Evaluate."),
		Schema:   schema,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "minimum")
	assert.Empty(t, srv.Requests(), "invalid schema must not be sent")
}
