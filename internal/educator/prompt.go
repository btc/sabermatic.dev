package educator

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/btc/drill/internal/db"
)

const systemPromptBase = `You are an expert system design educator with deep production experience at top tech companies. You have designed and operated large-scale distributed systems.

You are given:
1. A system design interview question
2. The full interview transcript
3. The evaluator's assessment (scores, gaps, strengths)

Your job is to TEACH, not judge. The evaluator already judged. You provide the knowledge the candidate needs.

## Model Answer

Write what a strong answer to THIS SPECIFIC problem looks like. Not a generic textbook answer — a concrete, production-aware design tailored to the exact question as framed in the interview.

Include:
- Concrete architecture with specific technology choices and WHY each was chosen
- Data model with actual schemas, key structures, and access patterns
- Key algorithms, protocols, or techniques with enough detail to implement
- Explicit tradeoffs: what you're giving up and what you're gaining
- What separates a good answer from an exceptional one at each phase

Write as if explaining to a strong engineer who needs to build this. Be specific enough that they could start implementing.

## Gap Deep-Dives

For each gap identified by the evaluator, provide a detailed technical education:

- What the candidate should have known, explained clearly
- How this works in practice at real companies (name companies and systems where relevant)
- Concrete implementation details — not "go research cache invalidation" but "here are the three main approaches: write-through (used by DynamoDB), write-behind (used by most ORMs with batch flush), and TTL-based expiration (Redis default). For this problem, write-through is best because..."
- Code snippets, schema examples, or algorithm pseudocode where helpful
- Common mistakes and how to avoid them

Be the senior engineer who sits down with the candidate after the interview and says "here's what you need to know."

Format everything in Markdown. Use headers, code blocks, and tables where they aid clarity.`

// BuildPrompt constructs the system prompt and user messages for the educator LLM call.
func BuildPrompt(question *db.Question, messages []db.Message, eval *db.Evaluation) (string, []anthropic.BetaMessageParam) {
	system := buildSystemPrompt(question)
	userContent := buildTranscript(question, messages) + "\n\n" + buildEvaluationSummary(eval)
	return system, []anthropic.BetaMessageParam{
		anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(userContent)),
	}
}

// buildSystemPrompt appends the interview context section to the base system prompt.
func buildSystemPrompt(question *db.Question) string {
	var sb strings.Builder
	sb.WriteString(systemPromptBase)
	sb.WriteString("\n\n## Interview Context\n\n")
	fmt.Fprintf(&sb, "**Question:** %s\n\n", question.Title)
	sb.WriteString(question.Prompt)
	return sb.String()
}

// buildTranscript formats the interview messages as a numbered transcript.
func buildTranscript(question *db.Question, messages []db.Message) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "## Interview Transcript\n\n**Question:** %s\n\n", question.Title)
	for i, msg := range messages {
		fmt.Fprintf(&sb, "[%d] %s: %s\n\n", i+1, roleLabel(msg.Role), msg.Content)
	}
	return strings.TrimRight(sb.String(), "\n")
}

// buildEvaluationSummary formats the evaluation scores, strengths, and gaps.
func buildEvaluationSummary(eval *db.Evaluation) string {
	var sb strings.Builder
	sb.WriteString("## Evaluation Summary\n\n")
	fmt.Fprintf(&sb, "- Requirements: %d/5\n", eval.ScoreRequirements)
	fmt.Fprintf(&sb, "- Architecture: %d/5\n", eval.ScoreArchitecture)
	fmt.Fprintf(&sb, "- Deep Dive: %d/5\n", eval.ScoreDeepDive)
	fmt.Fprintf(&sb, "- Scalability: %d/5\n", eval.ScoreScalability)
	fmt.Fprintf(&sb, "- Communication: %d/5\n", eval.ScoreCommunication)
	fmt.Fprintf(&sb, "- Overall: %d/5\n", eval.ScoreOverall)

	var strengths []string
	if len(eval.Strengths) > 0 {
		_ = json.Unmarshal(eval.Strengths, &strengths)
	}
	if len(strengths) > 0 {
		sb.WriteString("\n**Strengths:**\n")
		for _, s := range strengths {
			fmt.Fprintf(&sb, "- %s\n", s)
		}
	}

	var gaps []string
	if len(eval.Gaps) > 0 {
		_ = json.Unmarshal(eval.Gaps, &gaps)
	}
	if len(gaps) > 0 {
		sb.WriteString("\n**Gaps:**\n")
		for _, g := range gaps {
			fmt.Fprintf(&sb, "- %s\n", g)
		}
	}

	if eval.Advice != "" {
		fmt.Fprintf(&sb, "\n**Advice:** %s\n", eval.Advice)
	}

	return strings.TrimRight(sb.String(), "\n")
}

// roleLabel converts a db message role to a display label.
func roleLabel(role string) string {
	switch role {
	case "interviewer":
		return "Interviewer"
	case "candidate":
		return "Candidate"
	default:
		if len(role) == 0 {
			return role
		}
		return strings.ToUpper(role[:1]) + role[1:]
	}
}
