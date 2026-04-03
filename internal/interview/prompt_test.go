package interview_test

import (
	"testing"
	"time"

	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/interview"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPromptBuilder_Basic(t *testing.T) {
	system, msgs := interview.NewInterviewerPrompt().
		WithSystemInstructions().
		WithQuestion(db.Question{Title: "URL Shortener", Prompt: "Design a URL shortening service."}).
		WithTimeContext(5*time.Minute, 25*time.Minute).
		Build()

	assert.Contains(t, system, "URL Shortener")
	assert.Contains(t, system, "Design a URL shortening service")
	assert.Contains(t, system, "5 minutes")
	assert.Contains(t, system, "25 minutes")
	assert.Empty(t, msgs)
}

func TestPromptBuilder_WithTranscript(t *testing.T) {
	messages := []db.Message{
		{Seq: 1, Role: "interviewer", Content: "Design a URL shortener."},
		{Seq: 2, Role: "candidate", Content: "I'd start with the API design."},
	}
	_, msgs := interview.NewInterviewerPrompt().
		WithSystemInstructions().
		WithQuestion(db.Question{Title: "URL Shortener", Prompt: "..."}).
		WithTranscript(messages).
		WithTimeContext(10*time.Minute, 20*time.Minute).
		Build()
	require.Len(t, msgs, 2)
}

func TestPromptBuilder_WithCoachBriefingNil(t *testing.T) {
	system, _ := interview.NewInterviewerPrompt().
		WithSystemInstructions().
		WithQuestion(db.Question{Title: "Test", Prompt: "Test"}).
		WithCoachBriefing(nil).
		WithTimeContext(0, 30*time.Minute).
		Build()
	assert.NotContains(t, system, "Coach Briefing")
}

func TestPromptBuilder_WithCoachBriefing(t *testing.T) {
	ca := &db.CoachAnalysis{
		Narrative:           "Candidate struggles with distributed consensus and cache invalidation.",
		WeakestDimension:    pgtype.Text{String: "scalability", Valid: true},
		ImprovingDimensions: []string{"requirements", "communication"},
		TopicGaps:           []string{"consensus", "caching"},
	}
	system, _ := interview.NewInterviewerPrompt().
		WithSystemInstructions().
		WithQuestion(db.Question{Title: "Test", Prompt: "Test"}).
		WithCoachBriefing(ca).
		WithTimeContext(0, 30*time.Minute).
		Build()
	assert.Contains(t, system, "Coach Briefing")
	assert.Contains(t, system, "distributed consensus")
	assert.Contains(t, system, "weakest area is scalability")
	assert.Contains(t, system, "improving in: requirements, communication")
	assert.Contains(t, system, "Topic gaps to probe if relevant: consensus, caching")
}

func TestPromptBuilder_WithCoachBriefingEmptyNarrative(t *testing.T) {
	ca := &db.CoachAnalysis{
		Narrative: "",
	}
	system, _ := interview.NewInterviewerPrompt().
		WithSystemInstructions().
		WithQuestion(db.Question{Title: "Test", Prompt: "Test"}).
		WithCoachBriefing(ca).
		WithTimeContext(0, 30*time.Minute).
		Build()
	assert.NotContains(t, system, "Coach Briefing")
}

func TestPromptBuilder_TimeContextAlerts(t *testing.T) {
	t.Run("nearly up", func(t *testing.T) {
		system, _ := interview.NewInterviewerPrompt().
			WithSystemInstructions().
			WithQuestion(db.Question{Title: "Test", Prompt: "Test"}).
			WithTimeContext(25*time.Minute, 4*time.Minute).
			Build()
		assert.Contains(t, system, "nearly up")
	})

	t.Run("final stretch", func(t *testing.T) {
		system, _ := interview.NewInterviewerPrompt().
			WithSystemInstructions().
			WithQuestion(db.Question{Title: "Test", Prompt: "Test"}).
			WithTimeContext(20*time.Minute, 9*time.Minute).
			Build()
		assert.Contains(t, system, "final stretch")
	})
}

func TestPromptBuilder_TranscriptRoleMapping(t *testing.T) {
	messages := []db.Message{
		{Seq: 1, Role: "interviewer", Content: "Hello, let's begin."},
		{Seq: 2, Role: "candidate", Content: "Sure, I'll start with requirements."},
		{Seq: 3, Role: "interviewer", Content: "Go ahead."},
	}
	_, msgs := interview.NewInterviewerPrompt().
		WithSystemInstructions().
		WithQuestion(db.Question{Title: "Test", Prompt: "Test"}).
		WithTranscript(messages).
		WithTimeContext(5*time.Minute, 25*time.Minute).
		Build()
	require.Len(t, msgs, 3)
	// interviewer → assistant, candidate → user
	assert.Equal(t, "assistant", string(msgs[0].Role))
	assert.Equal(t, "user", string(msgs[1].Role))
	assert.Equal(t, "assistant", string(msgs[2].Role))
}

func TestPromptBuilder_SystemInstructionsContent(t *testing.T) {
	system, _ := interview.NewInterviewerPrompt().
		WithSystemInstructions().
		WithQuestion(db.Question{Title: "Cache Design", Prompt: "Design a distributed cache."}).
		WithTimeContext(0, 45*time.Minute).
		Build()

	// Verify all 10+ behavioral rules are present
	assert.Contains(t, system, "deliberate vagueness")
	assert.Contains(t, system, "WHY")
	assert.Contains(t, system, "hand-waving")
	assert.Contains(t, system, "neutral")
	assert.Contains(t, system, "2-4 sentences")
	assert.Contains(t, system, "coverage")
}
