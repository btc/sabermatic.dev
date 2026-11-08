package coach

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/ai"
)

func validToolInput() map[string]any {
	return map[string]any{
		"narrative":            "The candidate consistently struggles with scalability.",
		"weakest_dimension":    "scalability",
		"improving_dimensions": []any{"requirements", "communication"},
		"topic_gaps":           []any{"distributed caching", "consensus algorithms"},
	}
}

func TestParse_ValidInput(t *testing.T) {
	input := validToolInput()
	raw, err := json.Marshal(input)
	require.NoError(t, err)

	result, err := Parse(json.RawMessage(raw))
	require.NoError(t, err)

	assert.Equal(t, "The candidate consistently struggles with scalability.", result.Narrative)
	assert.Equal(t, "scalability", result.WeakestDimension)
	assert.Equal(t, []string{"requirements", "communication"}, result.ImprovingDimensions)
	assert.Equal(t, []string{"distributed caching", "consensus algorithms"}, result.TopicGaps)
	assert.Nil(t, result.GeneratedQuestion)
}

func TestParse_ValidInputWithQuestion(t *testing.T) {
	input := validToolInput()
	input["generated_question"] = map[string]any{
		"title":      "Design a Distributed Cache",
		"prompt":     "Design a distributed caching system like Redis.",
		"difficulty": "hard",
		"tags":       []any{"caching", "distributed-systems"},
	}
	raw, err := json.Marshal(input)
	require.NoError(t, err)

	result, err := Parse(json.RawMessage(raw))
	require.NoError(t, err)

	require.NotNil(t, result.GeneratedQuestion)
	assert.Equal(t, "Design a Distributed Cache", result.GeneratedQuestion.Title)
	assert.Equal(t, "Design a distributed caching system like Redis.", result.GeneratedQuestion.Prompt)
	assert.Equal(t, "hard", result.GeneratedQuestion.Difficulty)
	assert.Equal(t, []string{"caching", "distributed-systems"}, result.GeneratedQuestion.Tags)
}

func TestParse_ValidInputWithMediumDifficulty(t *testing.T) {
	input := validToolInput()
	input["generated_question"] = map[string]any{
		"title":      "Design a Rate Limiter",
		"prompt":     "Design a rate limiting service.",
		"difficulty": "medium",
		"tags":       []any{"rate-limiting"},
	}
	raw, err := json.Marshal(input)
	require.NoError(t, err)

	result, err := Parse(json.RawMessage(raw))
	require.NoError(t, err)

	require.NotNil(t, result.GeneratedQuestion)
	assert.Equal(t, "medium", result.GeneratedQuestion.Difficulty)
}

func TestParse_EmptyNarrative(t *testing.T) {
	input := validToolInput()
	input["narrative"] = ""
	raw, err := json.Marshal(input)
	require.NoError(t, err)

	_, err = Parse(json.RawMessage(raw))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "narrative")
}

func TestParse_EmptyPromptOnGeneratedQuestion(t *testing.T) {
	input := validToolInput()
	input["generated_question"] = map[string]any{
		"title":      "Some Question",
		"prompt":     "",
		"difficulty": "medium",
		"tags":       []any{},
	}
	raw, err := json.Marshal(input)
	require.NoError(t, err)

	_, err = Parse(json.RawMessage(raw))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "prompt")
}

func TestParse_InvalidDifficulty(t *testing.T) {
	input := validToolInput()
	input["generated_question"] = map[string]any{
		"title":      "Some Question",
		"prompt":     "Some prompt.",
		"difficulty": "easy",
		"tags":       []any{},
	}
	raw, err := json.Marshal(input)
	require.NoError(t, err)

	_, err = Parse(json.RawMessage(raw))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "difficulty")
	assert.Contains(t, err.Error(), "easy")
}

func TestParse_NullArraysBecomesEmpty(t *testing.T) {
	raw := []byte(`{"narrative":"Some narrative.","improving_dimensions":null,"topic_gaps":null}`)

	result, err := Parse(json.RawMessage(raw))
	require.NoError(t, err)

	assert.NotNil(t, result.ImprovingDimensions)
	assert.Empty(t, result.ImprovingDimensions)
	assert.NotNil(t, result.TopicGaps)
	assert.Empty(t, result.TopicGaps)
}

func TestParse_InvalidJSON(t *testing.T) {
	raw := []byte(`{not valid json`)

	_, err := Parse(json.RawMessage(raw))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal")
}

func TestParse_GeneratedQuestionNullTagsBecomesEmpty(t *testing.T) {
	raw := []byte(`{"narrative":"Some narrative.","generated_question":{"title":"Q","prompt":"P","difficulty":"medium","tags":null}}`)

	result, err := Parse(json.RawMessage(raw))
	require.NoError(t, err)

	require.NotNil(t, result.GeneratedQuestion)
	assert.NotNil(t, result.GeneratedQuestion.Tags)
	assert.Empty(t, result.GeneratedQuestion.Tags)
}

func TestParse_SummaryField(t *testing.T) {
	input := validToolInput()
	input["summary"] = "Focus on architecture — requirements are improving."
	raw, err := json.Marshal(input)
	require.NoError(t, err)

	result, err := Parse(json.RawMessage(raw))
	require.NoError(t, err)
	assert.Equal(t, "Focus on architecture — requirements are improving.", result.Summary)
}

func TestParse_MissingSummary(t *testing.T) {
	input := validToolInput()
	// no "summary" key
	raw, err := json.Marshal(input)
	require.NoError(t, err)

	result, err := Parse(json.RawMessage(raw))
	require.NoError(t, err)
	assert.Equal(t, "", result.Summary)
}

func TestOutputSchema_Fields(t *testing.T) {
	schema := OutputSchema()
	require.NoError(t, ai.CheckOutputSchema(schema))

	props := schema["properties"].(map[string]any)
	for _, name := range []string{"narrative", "weakest_dimension", "improving_dimensions", "topic_gaps", "generated_question", "summary"} {
		assert.Contains(t, props, name)
	}

	// generated_question is nullable through anyOf, not a type array.
	gq := props["generated_question"].(map[string]any)
	assert.NotContains(t, gq, "type")
	require.Len(t, gq["anyOf"], 2)
}

func TestParse_NullGeneratedQuestion(t *testing.T) {
	input := validToolInput()
	input["generated_question"] = nil
	raw, err := json.Marshal(input)
	require.NoError(t, err)

	result, err := Parse(json.RawMessage(raw))
	require.NoError(t, err)
	assert.Nil(t, result.GeneratedQuestion)
}
