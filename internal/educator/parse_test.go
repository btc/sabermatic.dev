package educator

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/ai"
)

func validOutput() map[string]any {
	return map[string]any{
		"model_answer":   "## Architecture\n\nUse a distributed cache with write-through...",
		"gap_deep_dives": "## Cache Invalidation\n\nThe candidate missed...",
	}
}

func TestParse_ValidInput(t *testing.T) {
	raw, err := json.Marshal(validOutput())
	require.NoError(t, err)
	result, err := Parse(json.RawMessage(raw))
	require.NoError(t, err)
	assert.Contains(t, result.ModelAnswer, "distributed cache")
	assert.Contains(t, result.GapDeepDives, "Cache Invalidation")
}

func TestParse_EmptyModelAnswer(t *testing.T) {
	input := validOutput()
	input["model_answer"] = ""
	raw, _ := json.Marshal(input)
	_, err := Parse(json.RawMessage(raw))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "model_answer")
}

func TestParse_EmptyGapDeepDives(t *testing.T) {
	input := validOutput()
	input["gap_deep_dives"] = ""
	raw, _ := json.Marshal(input)
	_, err := Parse(json.RawMessage(raw))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gap_deep_dives")
}

func TestParse_MissingFields(t *testing.T) {
	_, err := Parse(json.RawMessage([]byte(`{}`)))
	require.Error(t, err)
}

func TestParse_InvalidJSON(t *testing.T) {
	_, err := Parse(json.RawMessage([]byte(`not json`)))
	require.Error(t, err)
}

func TestOutputSchema_HasRequiredFields(t *testing.T) {
	schema := OutputSchema()
	require.NoError(t, ai.CheckOutputSchema(schema))
	props := schema["properties"].(map[string]any)
	assert.Contains(t, props, "model_answer")
	assert.Contains(t, props, "gap_deep_dives")
	assert.Equal(t, []string{"model_answer", "gap_deep_dives"}, schema["required"])
}
