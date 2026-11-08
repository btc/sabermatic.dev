package educator

import (
	"encoding/json"
	"fmt"
)

// EducatorResult holds the parsed educator content ready for persistence.
type EducatorResult struct {
	ModelAnswer  string
	GapDeepDives string
}

// OutputSchema returns the JSON schema for the educator's structured output
// (output_config.format).
func OutputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"model_answer": map[string]any{
				"type":        "string",
				"description": "Markdown: what a strong answer to this specific problem looks like.",
			},
			"gap_deep_dives": map[string]any{
				"type":        "string",
				"description": "Markdown: detailed technical explanation for each gap identified by the evaluator.",
			},
		},
		"required": []string{"model_answer", "gap_deep_dives"},
	}
}

type rawOutput struct {
	ModelAnswer  string `json:"model_answer"`
	GapDeepDives string `json:"gap_deep_dives"`
}

// Parse extracts an EducatorResult from the structured-output JSON.
func Parse(raw json.RawMessage) (*EducatorResult, error) {
	var input rawOutput
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, fmt.Errorf("educator: unmarshal output: %w", err)
	}
	if input.ModelAnswer == "" {
		return nil, fmt.Errorf("educator: model_answer is empty")
	}
	if input.GapDeepDives == "" {
		return nil, fmt.Errorf("educator: gap_deep_dives is empty")
	}
	return &EducatorResult{
		ModelAnswer:  input.ModelAnswer,
		GapDeepDives: input.GapDeepDives,
	}, nil
}
