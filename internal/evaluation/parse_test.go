package evaluation

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validToolInput() map[string]any {
	return map[string]any{
		"scores": map[string]any{
			"requirements":  float64(3),
			"architecture":  float64(4),
			"deep_dive":     float64(2),
			"scalability":   float64(3),
			"communication": float64(4),
			"overall":       float64(3),
		},
		"strengths":   []any{"Good requirements gathering"},
		"gaps":        []any{"Missing cache invalidation"},
		"advice":      "Focus on deep dive components next time.",
		"annotations": []any{
			map[string]any{
				"message_seq": float64(2),
				"type":        "strength",
				"content":     "Good structured approach to requirements.",
			},
			map[string]any{
				"message_seq": float64(4),
				"type":        "gap",
				"content":     "Missed cache invalidation strategy.",
			},
		},
	}
}

func TestParse_ValidInput(t *testing.T) {
	input := validToolInput()
	raw, err := json.Marshal(input)
	require.NoError(t, err)

	result, err := Parse(json.RawMessage(raw))
	require.NoError(t, err)

	assert.Equal(t, int32(3), result.ScoreRequirements)
	assert.Equal(t, int32(4), result.ScoreArchitecture)
	assert.Equal(t, int32(2), result.ScoreDeepDive)
	assert.Equal(t, int32(3), result.ScoreScalability)
	assert.Equal(t, int32(4), result.ScoreCommunication)
	assert.Equal(t, int32(3), result.ScoreOverall)
	assert.Len(t, result.Strengths, 1)
	assert.Len(t, result.Gaps, 1)
	assert.Equal(t, "Focus on deep dive components next time.", result.Advice)
	assert.Len(t, result.Annotations, 2)
	assert.Equal(t, int32(2), result.Annotations[0].MessageSeq)
	assert.Equal(t, "strength", result.Annotations[0].Type)
}

func TestParse_EmptyStrengthsAndGaps(t *testing.T) {
	input := validToolInput()
	input["strengths"] = []any{}
	input["gaps"] = []any{}
	raw, err := json.Marshal(input)
	require.NoError(t, err)

	result, err := Parse(json.RawMessage(raw))
	require.NoError(t, err)
	assert.Empty(t, result.Strengths)
	assert.Empty(t, result.Gaps)
}

func TestParse_ZeroAnnotations(t *testing.T) {
	input := validToolInput()
	input["annotations"] = []any{}
	raw, err := json.Marshal(input)
	require.NoError(t, err)

	result, err := Parse(json.RawMessage(raw))
	require.NoError(t, err)
	assert.Empty(t, result.Annotations)
}

func TestParse_MissingScoresField(t *testing.T) {
	input := validToolInput()
	delete(input, "scores")
	raw, err := json.Marshal(input)
	require.NoError(t, err)

	_, err = Parse(json.RawMessage(raw))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scores")
}

func TestValidate_ValidResult(t *testing.T) {
	result := &EvaluationResult{
		ScoreRequirements: 3, ScoreArchitecture: 4, ScoreDeepDive: 2,
		ScoreScalability: 3, ScoreCommunication: 4, ScoreOverall: 3,
		Annotations: []AnnotationResult{
			{MessageSeq: 2, Type: "strength", Content: "Good."},
		},
	}
	seqMap := map[int32]uuid.UUID{1: uuid.New(), 2: uuid.New(), 3: uuid.New()}

	err := Validate(result, seqMap)
	require.NoError(t, err)
}

func TestValidate_InvalidMessageSeq(t *testing.T) {
	result := &EvaluationResult{
		ScoreRequirements: 3, ScoreArchitecture: 4, ScoreDeepDive: 2,
		ScoreScalability: 3, ScoreCommunication: 4, ScoreOverall: 3,
		Annotations: []AnnotationResult{
			{MessageSeq: 99, Type: "gap", Content: "Missing."},
		},
	}
	seqMap := map[int32]uuid.UUID{1: uuid.New(), 2: uuid.New()}

	err := Validate(result, seqMap)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "99")
}

func TestValidate_ScoreOutOfRange(t *testing.T) {
	result := &EvaluationResult{
		ScoreRequirements: 6, ScoreArchitecture: 4, ScoreDeepDive: 2,
		ScoreScalability: 3, ScoreCommunication: 4, ScoreOverall: 3,
	}

	err := Validate(result, map[int32]uuid.UUID{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "out of range")
}

func TestValidate_ScoreZero(t *testing.T) {
	result := &EvaluationResult{
		ScoreRequirements: 0, ScoreArchitecture: 4, ScoreDeepDive: 2,
		ScoreScalability: 3, ScoreCommunication: 4, ScoreOverall: 3,
	}

	err := Validate(result, map[int32]uuid.UUID{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "out of range")
}
