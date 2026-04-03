package coach

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/btc/drill/internal/db"
	"github.com/google/uuid"
)

const systemPrompt = `You are an expert system design interview coach. You have access to the candidate's full interview history — past sessions, evaluations, scores, strengths, gaps, and advice. Your job is to analyze this history and produce strategic guidance.

## Your Responsibilities

### 1. Dimension Analysis
Analyze the candidate's scores across all evaluation dimensions (requirements, high-level architecture, deep dive, scalability, communication). Identify:
- Which dimension is consistently weakest
- Which dimensions are improving over time
- Where scores plateau and what might break through

### 2. Topic Coverage
Review which system design topics the candidate has practiced and which they haven't. Identify gaps in their coverage and recommend topics that would round out their preparation.

### 3. Thinking Pattern Recognition
Look across multiple interviews for recurring patterns in how the candidate approaches problems:
- Do they consistently skip requirements gathering?
- Do they go too deep too early?
- Do they struggle with back-of-envelope calculations?
- Do they miss trade-off discussions?
Identify both positive patterns (strengths to maintain) and negative patterns (habits to break).

### 4. Scenario Generation
Based on identified gaps, optionally generate a custom practice question that specifically targets the candidate's weaknesses. The question should:
- Address a topic or dimension they struggle with
- Be at an appropriate difficulty level for their current skill
- Include tags that map to their gap areas

### 5. Progressive Difficulty
Track the candidate's overall trajectory and recommend appropriate difficulty:
- If scores are consistently low (1-2), suggest medium difficulty questions
- If scores are improving (3-4), suggest harder variants or new topics
- If scores are high (4-5), suggest hard questions with complex constraints

### 6. Metacognitive Coaching
Help the candidate develop self-awareness about their interview approach:
- Point out blind spots they may not realize they have
- Suggest reflection exercises or frameworks
- Encourage deliberate practice on specific sub-skills
- Frame feedback in terms of growth and improvement

## Output

You MUST use the submit_analysis tool to submit your structured analysis. Do not produce free-text output — use the tool.

If you identify a specific gap that would benefit from a custom practice question, include it in the generated_question field. Otherwise, set generated_question to null.`

// BuildPrompt returns the coach system prompt and a single user message
// containing the candidate's full interview history summary.
func BuildPrompt(sessions []db.InterviewSession, evaluations []db.Evaluation, questions []db.Question) (string, []anthropic.MessageParam) {
	summary := buildHistorySummary(sessions, evaluations, questions)
	userMsg := anthropic.NewUserMessage(anthropic.NewTextBlock(summary))
	return systemPrompt, []anthropic.MessageParam{userMsg}
}

// buildHistorySummary formats session history, evaluations, and topic coverage
// into a structured markdown summary for the coach.
func buildHistorySummary(sessions []db.InterviewSession, evaluations []db.Evaluation, questions []db.Question) string {
	if len(sessions) == 0 {
		return "The candidate has no interview history yet."
	}

	// Index questions by ID for lookup.
	questionByID := make(map[uuid.UUID]db.Question, len(questions))
	for _, q := range questions {
		questionByID[q.ID] = q
	}

	// Index evaluations by session ID for lookup.
	evalBySessionID := make(map[uuid.UUID]db.Evaluation, len(evaluations))
	for _, e := range evaluations {
		evalBySessionID[e.SessionID] = e
	}

	// Collect attempted tags across all sessions.
	attemptedTags := make(map[string]bool)
	for _, s := range sessions {
		if q, ok := questionByID[s.QuestionID]; ok {
			for _, tag := range q.Tags {
				attemptedTags[tag] = true
			}
		}
	}

	// Collect all available tags from the question pool.
	allTags := make(map[string]bool)
	for _, q := range questions {
		for _, tag := range q.Tags {
			allTags[tag] = true
		}
	}

	var sb strings.Builder

	sb.WriteString("# Candidate Interview History\n")

	// Per-session details.
	fmt.Fprintf(&sb, "\n## Sessions (%d total)\n", len(sessions))
	for i, s := range sessions {
		q, hasQ := questionByID[s.QuestionID]
		title := "(unknown question)"
		difficulty := ""
		var tags []string
		if hasQ {
			title = q.Title
			difficulty = q.Difficulty
			tags = q.Tags
		}

		fmt.Fprintf(&sb, "\n### Session %d: %s\n", i+1, title)
		if difficulty != "" {
			fmt.Fprintf(&sb, "- Difficulty: %s\n", difficulty)
		}
		if len(tags) > 0 {
			fmt.Fprintf(&sb, "- Topics: %s\n", strings.Join(tags, ", "))
		}

		eval, hasEval := evalBySessionID[s.ID]
		if hasEval {
			fmt.Fprintf(&sb, "- Scores: requirements=%d, architecture=%d, deep_dive=%d, scalability=%d, communication=%d, overall=%d\n",
				eval.ScoreRequirements,
				eval.ScoreArchitecture,
				eval.ScoreDeepDive,
				eval.ScoreScalability,
				eval.ScoreCommunication,
				eval.ScoreOverall,
			)

			strengths := unmarshalStringSlice(eval.Strengths)
			if len(strengths) > 0 {
				fmt.Fprintf(&sb, "- Strengths: %s\n", strings.Join(strengths, "; "))
			}

			gaps := unmarshalStringSlice(eval.Gaps)
			if len(gaps) > 0 {
				fmt.Fprintf(&sb, "- Gaps: %s\n", strings.Join(gaps, "; "))
			}

			if eval.Advice != "" {
				fmt.Fprintf(&sb, "- Advice: %s\n", eval.Advice)
			}
		} else {
			sb.WriteString("- No evaluation available\n")
		}
	}

	// Score averages across all evaluations.
	if len(evaluations) > 0 {
		sb.WriteString("\n## Score Averages\n")
		var (
			sumReq, sumArch, sumDive, sumScale, sumComm, sumOverall float64
		)
		for _, e := range evaluations {
			sumReq += float64(e.ScoreRequirements)
			sumArch += float64(e.ScoreArchitecture)
			sumDive += float64(e.ScoreDeepDive)
			sumScale += float64(e.ScoreScalability)
			sumComm += float64(e.ScoreCommunication)
			sumOverall += float64(e.ScoreOverall)
		}
		n := float64(len(evaluations))
		fmt.Fprintf(&sb, "- requirements: %.2f\n", sumReq/n)
		fmt.Fprintf(&sb, "- architecture: %.2f\n", sumArch/n)
		fmt.Fprintf(&sb, "- deep_dive: %.2f\n", sumDive/n)
		fmt.Fprintf(&sb, "- scalability: %.2f\n", sumScale/n)
		fmt.Fprintf(&sb, "- communication: %.2f\n", sumComm/n)
		fmt.Fprintf(&sb, "- overall: %.2f\n", sumOverall/n)
	}

	// Topic coverage.
	sb.WriteString("\n## Topic Coverage\n")

	if len(attemptedTags) > 0 {
		attempted := sortedKeys(attemptedTags)
		fmt.Fprintf(&sb, "- Attempted topics: %s\n", strings.Join(attempted, ", "))
	} else {
		sb.WriteString("- No topics attempted yet\n")
	}

	var uncovered []string
	for tag := range allTags {
		if !attemptedTags[tag] {
			uncovered = append(uncovered, tag)
		}
	}
	if len(uncovered) > 0 {
		slices.Sort(uncovered)
		fmt.Fprintf(&sb, "- Uncovered topics: %s\n", strings.Join(uncovered, ", "))
	} else {
		sb.WriteString("- All available topics have been attempted\n")
	}

	return sb.String()
}

// unmarshalStringSlice decodes a JSONB []byte into a []string.
// Returns nil on empty input or unmarshal failure.
func unmarshalStringSlice(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	var result []string
	if err := json.Unmarshal(data, &result); err != nil {
		return nil
	}
	return result
}

// sortedKeys returns sorted keys from a bool map.
func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
