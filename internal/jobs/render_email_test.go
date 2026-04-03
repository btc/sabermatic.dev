package jobs

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/btc/drill/internal/evaluation"
)

func TestRenderEvaluationEmail_EscapesHTML(t *testing.T) {
	result := &evaluation.EvaluationResult{
		ScoreRequirements: 3, ScoreArchitecture: 3, ScoreDeepDive: 3,
		ScoreScalability: 3, ScoreCommunication: 3, ScoreOverall: 3,
		Strengths: []string{"<script>alert('xss')</script>"},
		Gaps:      []string{"normal gap"},
		Advice:    "<img onerror=alert(1) src=x>",
	}
	html := renderEvaluationEmail("<b>evil title</b>", result)

	assert.NotContains(t, html, "<script>")
	assert.NotContains(t, html, "<img onerror")
	assert.NotContains(t, html, "<b>evil")
	assert.Contains(t, html, "&lt;script&gt;")
	assert.Contains(t, html, "&lt;b&gt;evil")
}
