package evaluation

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/db"
)

func TestBuildPrompt_SystemContainsRubric(t *testing.T) {
	q := db.Question{Title: "Design a URL Shortener", Prompt: "Design a URL shortening service."}
	msgs := []db.Message{
		{Seq: 1, Role: "interviewer", Content: "Design a URL shortening service."},
		{Seq: 2, Role: "candidate", Content: "Let me start by gathering requirements."},
	}

	system, userMsgs := BuildPrompt(q, msgs)

	assert.Contains(t, system, "Requirements Gathering & Scoping")
	assert.Contains(t, system, "High-Level Architecture")
	assert.Contains(t, system, "Deep Dive")
	assert.Contains(t, system, "Scalability & Trade-offs")
	assert.Contains(t, system, "Communication")
	assert.Contains(t, system, "Calibration Guidance")
	assert.Contains(t, system, "submit_evaluation")

	require.Len(t, userMsgs, 1)
}

func TestBuildPrompt_TranscriptFormatsCorrectly(t *testing.T) {
	q := db.Question{Title: "Design Chat", Prompt: "Design a chat system."}
	msgs := []db.Message{
		{Seq: 1, Role: "interviewer", Content: "Design a chat system."},
		{Seq: 2, Role: "candidate", Content: "Sure, let me start."},
		{Seq: 3, Role: "interviewer", Content: "Go ahead."},
	}

	_, userMsgs := BuildPrompt(q, msgs)
	require.Len(t, userMsgs, 1)
}

func TestBuildPrompt_EmptyMessages(t *testing.T) {
	q := db.Question{Title: "Design X", Prompt: "Design X."}
	system, userMsgs := BuildPrompt(q, nil)

	assert.Contains(t, system, "Requirements Gathering")
	require.Len(t, userMsgs, 1)
}
