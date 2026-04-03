package educator

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/db"
)

func makeQuestion() db.Question {
	return db.Question{
		ID:     uuid.New(),
		Title:  "Design a URL Shortener",
		Prompt: "Design a system like bit.ly that handles 100M daily active users.",
	}
}

func makeMessages() []db.Message {
	return []db.Message{
		{ID: uuid.New(), Seq: 1, Role: "assistant", Content: "Let's start. How would you design a URL shortener?"},
		{ID: uuid.New(), Seq: 2, Role: "user", Content: "I'd use a hash function to generate short codes."},
		{ID: uuid.New(), Seq: 3, Role: "assistant", Content: "How would you handle collisions?"},
	}
}

func makeEval() db.Evaluation {
	strengths, _ := json.Marshal([]string{"Clear requirements gathering", "Solid hash function choice"})
	gaps, _ := json.Marshal([]string{"Cache invalidation not addressed", "No discussion of database sharding"})
	return db.Evaluation{
		ID:                 uuid.New(),
		ScoreRequirements:  3,
		ScoreArchitecture:  2,
		ScoreDeepDive:      2,
		ScoreScalability:   1,
		ScoreCommunication: 3,
		ScoreOverall:       2,
		Strengths:          strengths,
		Gaps:               gaps,
		Advice:             "Focus on scalability patterns.",
	}
}

func TestBuildPrompt_SystemContainsTeachingInstructions(t *testing.T) {
	system, msgs := BuildPrompt(makeQuestion(), makeMessages(), makeEval())

	assert.Contains(t, system, "TEACH, not judge")
	assert.Contains(t, system, "Model Answer")
	assert.Contains(t, system, "Gap Deep-Dives")
	assert.Contains(t, system, "Interview Context")
	assert.Contains(t, system, "Design a URL Shortener")
	assert.Len(t, msgs, 1)
}

func TestBuildPrompt_EvalSummaryIncludesScores(t *testing.T) {
	eval := makeEval()
	summary := buildEvaluationSummary(eval)

	assert.Contains(t, summary, "Requirements: 3/4")
	assert.Contains(t, summary, "Architecture: 2/4")
	assert.Contains(t, summary, "Scalability: 1/4")
	assert.Contains(t, summary, "Overall: 2/4")
	assert.Contains(t, summary, "Clear requirements gathering")
	assert.Contains(t, summary, "Cache invalidation not addressed")
}

func TestBuildTranscript_FormatsCorrectly(t *testing.T) {
	question := makeQuestion()
	messages := makeMessages()
	transcript := buildTranscript(question, messages)

	assert.Contains(t, transcript, "[1] Interviewer:")
	assert.Contains(t, transcript, "[2] Candidate:")
	assert.Contains(t, transcript, "[3] Interviewer:")
	assert.Contains(t, transcript, "Let's start.")
	assert.Contains(t, transcript, "I'd use a hash function")
}

func TestBuildPrompt_EmptyMessages(t *testing.T) {
	system, msgs := BuildPrompt(makeQuestion(), nil, makeEval())

	require.NotEmpty(t, system)
	require.Len(t, msgs, 1)
	assert.Contains(t, system, "Design a URL Shortener")
}
