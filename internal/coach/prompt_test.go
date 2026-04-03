package coach

import (
	"encoding/json"
	"testing"

	"github.com/btc/drill/internal/db"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func TestBuildPrompt_SystemContainsCoachInstructions(t *testing.T) {
	system, userMsgs := BuildPrompt(nil, nil, nil)

	assert.Contains(t, system, "Dimension Analysis")
	assert.Contains(t, system, "Topic Coverage")
	assert.Contains(t, system, "Thinking Pattern Recognition")
	assert.Contains(t, system, "Metacognitive Coaching")
	assert.Contains(t, system, "submit_analysis")

	require.Len(t, userMsgs, 1)
}

func TestBuildPrompt_HistorySummaryIncludesScores(t *testing.T) {
	qID := uuid.New()
	sID := uuid.New()

	questions := []db.Question{
		{ID: qID, Title: "Design URL Shortener", Difficulty: "medium", Tags: []string{"hashing", "storage"}},
	}
	sessions := []db.InterviewSession{
		{ID: sID, QuestionID: qID},
	}
	evaluations := []db.Evaluation{
		{
			SessionID:          sID,
			ScoreRequirements:  3,
			ScoreArchitecture:  4,
			ScoreDeepDive:      2,
			ScoreScalability:   3,
			ScoreCommunication: 4,
			ScoreOverall:       3,
			Strengths:          mustJSON([]string{"Good requirements"}),
			Gaps:               mustJSON([]string{"Missing cache layer"}),
			Advice:             "Practice deep dives.",
		},
	}

	_, userMsgs := BuildPrompt(sessions, evaluations, questions)
	require.Len(t, userMsgs, 1)

	// Extract text content from the user message.
	content := userMsgs[0].Content
	require.NotEmpty(t, content)

	// The message content is a union type — convert back to verify it contains our data.
	system, _ := BuildPrompt(sessions, evaluations, questions)
	_ = system // we're interested in user content

	summary := buildHistorySummary(sessions, evaluations, questions)
	assert.Contains(t, summary, "requirements=3")
	assert.Contains(t, summary, "architecture=4")
	assert.Contains(t, summary, "deep_dive=2")
	assert.Contains(t, summary, "scalability=3")
	assert.Contains(t, summary, "communication=4")
	assert.Contains(t, summary, "overall=3")
	assert.Contains(t, summary, "Good requirements")
	assert.Contains(t, summary, "Missing cache layer")
	assert.Contains(t, summary, "Practice deep dives.")
}

func TestBuildPrompt_TopicCoverage(t *testing.T) {
	qID1 := uuid.New()
	qID2 := uuid.New()
	sID := uuid.New()

	questions := []db.Question{
		{ID: qID1, Title: "Design URL Shortener", Tags: []string{"hashing", "storage"}},
		{ID: qID2, Title: "Design a CDN", Tags: []string{"caching", "networking"}},
	}
	sessions := []db.InterviewSession{
		{ID: sID, QuestionID: qID1},
	}

	summary := buildHistorySummary(sessions, nil, questions)

	// Attempted topics should include hashing and storage.
	assert.Contains(t, summary, "hashing")
	assert.Contains(t, summary, "storage")

	// Uncovered topics should include caching and networking.
	assert.Contains(t, summary, "caching")
	assert.Contains(t, summary, "networking")
}

func TestBuildPrompt_ScoreAverages(t *testing.T) {
	qID := uuid.New()
	sID1 := uuid.New()
	sID2 := uuid.New()

	questions := []db.Question{
		{ID: qID, Title: "Design X", Tags: []string{"storage"}},
	}
	sessions := []db.InterviewSession{
		{ID: sID1, QuestionID: qID},
		{ID: sID2, QuestionID: qID},
	}
	evaluations := []db.Evaluation{
		{
			SessionID:          sID1,
			ScoreRequirements:  2,
			ScoreArchitecture:  3,
			ScoreDeepDive:      2,
			ScoreScalability:   2,
			ScoreCommunication: 3,
			ScoreOverall:       2,
		},
		{
			SessionID:          sID2,
			ScoreRequirements:  4,
			ScoreArchitecture:  3,
			ScoreDeepDive:      4,
			ScoreScalability:   4,
			ScoreCommunication: 3,
			ScoreOverall:       4,
		},
	}

	summary := buildHistorySummary(sessions, evaluations, questions)

	// Average requirements = (2+4)/2 = 3.00
	assert.Contains(t, summary, "requirements: 3.00")
	// Average scalability = (2+4)/2 = 3.00
	assert.Contains(t, summary, "scalability: 3.00")
	// Score Averages section exists
	assert.Contains(t, summary, "Score Averages")
}

func TestBuildPrompt_ZeroSessionsBaseline(t *testing.T) {
	system, userMsgs := BuildPrompt(nil, nil, nil)

	assert.NotEmpty(t, system)
	require.Len(t, userMsgs, 1)

	summary := buildHistorySummary(nil, nil, nil)
	assert.Contains(t, summary, "no interview history")
}

func TestBuildPrompt_EmptySessions(t *testing.T) {
	questions := []db.Question{
		{ID: uuid.New(), Title: "Design X", Tags: []string{"storage"}},
	}

	system, userMsgs := BuildPrompt([]db.InterviewSession{}, nil, questions)

	assert.NotEmpty(t, system)
	require.Len(t, userMsgs, 1)

	summary := buildHistorySummary([]db.InterviewSession{}, nil, questions)
	assert.Contains(t, summary, "no interview history")
}
