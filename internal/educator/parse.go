package educator

import (
	"encoding/json"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
)

// EducatorResult holds the parsed educator content ready for persistence.
type EducatorResult struct {
	ModelAnswer  string
	GapDeepDives string
}

// ToolSchema returns the anthropic.ToolParam for the submit_education tool.
func ToolSchema() anthropic.ToolParam {
	return anthropic.ToolParam{
		Name:        "submit_education",
		Description: anthropic.String("Submit the educational analysis with model answer and gap deep-dives."),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"model_answer": map[string]any{
					"type":        "string",
					"description": "Markdown: what a strong answer to this specific problem looks like.",
				},
				"gap_deep_dives": map[string]any{
					"type":        "string",
					"description": "Markdown: detailed technical explanation for each gap identified by the evaluator.",
				},
			},
			Required: []string{"model_answer", "gap_deep_dives"},
		},
	}
}

type rawToolInput struct {
	ModelAnswer  string `json:"model_answer"`
	GapDeepDives string `json:"gap_deep_dives"`
}

// Parse extracts an EducatorResult from the raw JSON tool input.
func Parse(raw json.RawMessage) (*EducatorResult, error) {
	var input rawToolInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, fmt.Errorf("educator: unmarshal tool input: %w", err)
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
