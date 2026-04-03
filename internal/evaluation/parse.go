package evaluation

import (
	"encoding/json"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/google/uuid"
)

// EvaluationResult holds the parsed evaluation data ready for persistence.
type EvaluationResult struct {
	ScoreRequirements  int32
	ScoreArchitecture  int32
	ScoreDeepDive      int32
	ScoreScalability   int32
	ScoreCommunication int32
	ScoreOverall       int32
	Strengths          []string
	Gaps               []string
	Advice             string
	Annotations        []AnnotationResult
}

// AnnotationResult holds a single parsed annotation.
type AnnotationResult struct {
	MessageSeq int32
	Type       string
	Content    string
}

// ToolSchema returns the anthropic.ToolParam for the submit_evaluation tool
// with a forced tool_choice schema.
func ToolSchema() anthropic.ToolParam {
	return anthropic.ToolParam{
		Name:        "submit_evaluation",
		Description: anthropic.String("Submit a structured evaluation of the candidate's system design interview performance."),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"scores": map[string]any{
					"type":        "object",
					"description": "Scores for each evaluation dimension.",
					"properties": map[string]any{
						"requirements": map[string]any{
							"type":        "integer",
							"description": "Score for requirements gathering (1-5).",
							"minimum":     1,
							"maximum":     5,
						},
						"architecture": map[string]any{
							"type":        "integer",
							"description": "Score for system architecture (1-5).",
							"minimum":     1,
							"maximum":     5,
						},
						"deep_dive": map[string]any{
							"type":        "integer",
							"description": "Score for component deep dive (1-5).",
							"minimum":     1,
							"maximum":     5,
						},
						"scalability": map[string]any{
							"type":        "integer",
							"description": "Score for scalability considerations (1-5).",
							"minimum":     1,
							"maximum":     5,
						},
						"communication": map[string]any{
							"type":        "integer",
							"description": "Score for communication clarity (1-5).",
							"minimum":     1,
							"maximum":     5,
						},
						"overall": map[string]any{
							"type":        "integer",
							"description": "Overall score (1-5).",
							"minimum":     1,
							"maximum":     5,
						},
					},
					"required": []string{"requirements", "architecture", "deep_dive", "scalability", "communication", "overall"},
				},
				"strengths": map[string]any{
					"type":        "array",
					"description": "List of candidate strengths observed during the interview.",
					"items":       map[string]any{"type": "string"},
				},
				"gaps": map[string]any{
					"type":        "array",
					"description": "List of gaps or weaknesses identified during the interview.",
					"items":       map[string]any{"type": "string"},
				},
				"advice": map[string]any{
					"type":        "string",
					"description": "Actionable advice for the candidate to improve.",
				},
				"annotations": map[string]any{
					"type":        "array",
					"description": "Per-message annotations referencing specific moments in the interview.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"message_seq": map[string]any{
								"type":        "integer",
								"description": "The sequence number of the message being annotated.",
							},
							"type": map[string]any{
								"type":        "string",
								"enum":        []string{"strength", "gap", "missed_opportunity", "note"},
								"description": "The category of this annotation.",
							},
							"content": map[string]any{
								"type":        "string",
								"description": "The annotation text.",
							},
						},
						"required": []string{"message_seq", "type", "content"},
					},
				},
			},
			Required: []string{"scores", "strengths", "gaps", "advice", "annotations"},
		},
	}
}

// rawToolInput is an intermediate struct for JSON unmarshaling.
// JSON numbers decode as float64; we convert to int32 after.
type rawToolInput struct {
	Scores      *rawScores       `json:"scores"`
	Strengths   []string         `json:"strengths"`
	Gaps        []string         `json:"gaps"`
	Advice      string           `json:"advice"`
	Annotations []rawAnnotation  `json:"annotations"`
}

type rawScores struct {
	Requirements  float64 `json:"requirements"`
	Architecture  float64 `json:"architecture"`
	DeepDive      float64 `json:"deep_dive"`
	Scalability   float64 `json:"scalability"`
	Communication float64 `json:"communication"`
	Overall       float64 `json:"overall"`
}

type rawAnnotation struct {
	MessageSeq float64 `json:"message_seq"`
	Type       string  `json:"type"`
	Content    string  `json:"content"`
}

// Parse unmarshals tool input JSON into an EvaluationResult.
// Returns an error if the scores field is missing.
func Parse(raw json.RawMessage) (*EvaluationResult, error) {
	var input rawToolInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, fmt.Errorf("evaluation: unmarshal tool input: %w", err)
	}

	if input.Scores == nil {
		return nil, fmt.Errorf("evaluation: missing required field: scores")
	}

	annotations := make([]AnnotationResult, len(input.Annotations))
	for i, a := range input.Annotations {
		annotations[i] = AnnotationResult{
			MessageSeq: int32(a.MessageSeq),
			Type:       a.Type,
			Content:    a.Content,
		}
	}

	strengths := input.Strengths
	if strengths == nil {
		strengths = []string{}
	}

	gaps := input.Gaps
	if gaps == nil {
		gaps = []string{}
	}

	return &EvaluationResult{
		ScoreRequirements:  int32(input.Scores.Requirements),
		ScoreArchitecture:  int32(input.Scores.Architecture),
		ScoreDeepDive:      int32(input.Scores.DeepDive),
		ScoreScalability:   int32(input.Scores.Scalability),
		ScoreCommunication: int32(input.Scores.Communication),
		ScoreOverall:       int32(input.Scores.Overall),
		Strengths:          strengths,
		Gaps:               gaps,
		Advice:             input.Advice,
		Annotations:        annotations,
	}, nil
}

// Validate performs structural validation on a parsed EvaluationResult.
// All 6 scores must be in [1, 5]. Each annotation's MessageSeq must exist in seqMap.
// Empty strengths, gaps, and annotations are valid.
func Validate(result *EvaluationResult, seqMap map[int32]uuid.UUID) error {
	scores := []struct {
		name  string
		value int32
	}{
		{"requirements", result.ScoreRequirements},
		{"architecture", result.ScoreArchitecture},
		{"deep_dive", result.ScoreDeepDive},
		{"scalability", result.ScoreScalability},
		{"communication", result.ScoreCommunication},
		{"overall", result.ScoreOverall},
	}

	for _, s := range scores {
		if s.value < 1 || s.value > 5 {
			return fmt.Errorf("evaluation: score %q out of range [1,5]: %d", s.name, s.value)
		}
	}

	for _, a := range result.Annotations {
		if _, ok := seqMap[a.MessageSeq]; !ok {
			return fmt.Errorf("evaluation: annotation references unknown message_seq: %d", a.MessageSeq)
		}
	}

	return nil
}
