// cmd/extract-sample/main.go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
)

// v1 response types — these must match web/src/api/types.ts exactly

type Session struct {
	ID                    string  `json:"id"`
	UserID                string  `json:"user_id"`
	QuestionID            string  `json:"question_id"`
	Status                string  `json:"status"`
	ConfigDurationMinutes int     `json:"config_duration_minutes"`
	ConfigTTSEnabled      bool    `json:"config_tts_enabled"`
	ConfigCoachBriefing   bool    `json:"config_coach_briefing"`
	StartedAt             string  `json:"started_at"`
	EndedAt               *string `json:"ended_at"`
	TurnCount             int     `json:"turn_count"`
	Archived              bool    `json:"archived"`
	CreatedAt             string  `json:"created_at"`
	UpdatedAt             string  `json:"updated_at"`
	QuestionTitle         string  `json:"question_title,omitempty"`
}

type Message struct {
	ID          string  `json:"id"`
	SessionID   string  `json:"session_id"`
	Seq         int     `json:"seq"`
	Role        string  `json:"role"`
	Content     string  `json:"content"`
	InputMethod *string `json:"input_method"`
	AudioURL    *string `json:"audio_url"`
	CreatedAt   string  `json:"created_at"`
}

type SessionFixture struct {
	Session  Session   `json:"session"`
	Messages []Message `json:"messages"`
}

type EvaluationScores struct {
	Requirements  int `json:"requirements"`
	Architecture  int `json:"architecture"`
	DeepDive      int `json:"deep_dive"`
	Scalability   int `json:"scalability"`
	Communication int `json:"communication"`
	Overall       int `json:"overall"`
}

type Annotation struct {
	MessageSeq int    `json:"message_seq"`
	Type       string `json:"type"`
	Content    string `json:"content"`
}

type EvaluationFixture struct {
	Status      string           `json:"status"`
	Scores      EvaluationScores `json:"scores"`
	Strengths   []string         `json:"strengths"`
	Gaps        []string         `json:"gaps"`
	Advice      string           `json:"advice"`
	Annotations []Annotation     `json:"annotations"`
}

type EducatorFixture struct {
	ID           string `json:"id"`
	SessionID    string `json:"session_id"`
	Status       string `json:"status"`
	ModelAnswer  string `json:"model_answer"`
	GapDeepDives string `json:"gap_deep_dives"`
	CreatedAt    string `json:"created_at"`
}

type ScoreTrendPoint struct {
	Date         string `json:"date"`
	OverallScore int    `json:"overall_score"`
}

type CoachFixture struct {
	ID                  string            `json:"id"`
	UserID              string            `json:"user_id"`
	Narrative           string            `json:"narrative"`
	WeakestDimension    *string           `json:"weakest_dimension"`
	ImprovingDimensions []string          `json:"improving_dimensions"`
	TopicGaps           []string          `json:"topic_gaps"`
	SuggestedQuestionID *string           `json:"suggested_question_id"`
	SessionsAnalyzed    []string          `json:"sessions_analyzed"`
	CreatedAt           string            `json:"created_at"`
	ScoreTrend          []ScoreTrendPoint `json:"score_trend"`
}

func main() {
	ctx := context.Background()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgresql://localhost:5432/drill_v0"
	}

	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close(ctx)

	const sessionID = 27

	// --- Extract session + question title ---
	var (
		questionID    int
		status        string
		timerSec      int
		ttsEnabled    bool
		briefed       bool
		archived      bool
		startedAt     time.Time
		endedAt       *time.Time
		durationSec   *int
		turnCount     *int
		questionTitle string
	)
	err = conn.QueryRow(ctx, `
		SELECT s.question_id, s.status, s.timer_setting_sec, s.tts_enabled,
		       s.interviewer_briefed, s.archived, s.started_at, s.ended_at,
		       s.duration_seconds, s.turn_count, q.title
		FROM sessions s JOIN questions q ON s.question_id = q.id
		WHERE s.id = $1
	`, sessionID).Scan(
		&questionID, &status, &timerSec, &ttsEnabled,
		&briefed, &archived, &startedAt, &endedAt,
		&durationSec, &turnCount, &questionTitle,
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "session query: %v\n", err)
		os.Exit(1)
	}

	sess := Session{
		ID:                    fmt.Sprintf("%d", sessionID),
		UserID:                "sample",
		QuestionID:            fmt.Sprintf("%d", questionID),
		Status:                status,
		ConfigDurationMinutes: timerSec / 60,
		ConfigTTSEnabled:      ttsEnabled,
		ConfigCoachBriefing:   briefed,
		StartedAt:             startedAt.Format(time.RFC3339),
		TurnCount:             derefInt(turnCount),
		Archived:              archived,
		CreatedAt:             startedAt.Format(time.RFC3339),
		UpdatedAt:             startedAt.Format(time.RFC3339),
		QuestionTitle:         questionTitle,
	}
	if endedAt != nil {
		s := endedAt.Format(time.RFC3339)
		sess.EndedAt = &s
	}

	// --- Extract messages ---
	rows, err := conn.Query(ctx, `
		SELECT id, sequence, role, content, audio_path, timestamp
		FROM messages WHERE session_id = $1 ORDER BY sequence
	`, sessionID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "messages query: %v\n", err)
		os.Exit(1)
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var (
			id        int
			seq       int
			role      string
			content   string
			audioPath *string
			ts        time.Time
		)
		if err := rows.Scan(&id, &seq, &role, &content, &audioPath, &ts); err != nil {
			fmt.Fprintf(os.Stderr, "scan message: %v\n", err)
			os.Exit(1)
		}
		msg := Message{
			ID:        fmt.Sprintf("%d", id),
			SessionID: fmt.Sprintf("%d", sessionID),
			Seq:       seq,
			Role:      role,
			Content:   content,
			CreatedAt: ts.Format(time.RFC3339),
		}
		if audioPath != nil {
			msg.AudioURL = audioPath
		}
		messages = append(messages, msg)
	}
	if err := rows.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "iterating messages: %v\n", err)
		os.Exit(1)
	}

	writeJSON("internal/sample/fixtures/session.json", SessionFixture{
		Session:  sess,
		Messages: messages,
	})

	// --- Extract evaluation + annotations ---
	var (
		evalID       int
		scoreReqs    int
		scoreHL      int
		scoreDD      int
		scoreScal    int
		scoreComm    int
		scoreOverall int
		strengths    []byte
		gaps         []byte
		advice       string
	)
	err = conn.QueryRow(ctx, `
		SELECT id, score_requirements, score_highlevel, score_deepdive,
		       score_scalability, score_communication, score_overall,
		       strengths, gaps, advice
		FROM evaluations WHERE session_id = $1
		ORDER BY evaluated_at DESC LIMIT 1
	`, sessionID).Scan(
		&evalID, &scoreReqs, &scoreHL, &scoreDD,
		&scoreScal, &scoreComm, &scoreOverall,
		&strengths, &gaps, &advice,
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "evaluation query: %v\n", err)
		os.Exit(1)
	}

	var strengthList, gapList []string
	if err := json.Unmarshal(strengths, &strengthList); err != nil {
		fmt.Fprintf(os.Stderr, "unmarshal strengths: %v\n", err)
		os.Exit(1)
	}
	if err := json.Unmarshal(gaps, &gapList); err != nil {
		fmt.Fprintf(os.Stderr, "unmarshal gaps: %v\n", err)
		os.Exit(1)
	}

	aRows, err := conn.Query(ctx, `
		SELECT m.sequence, ma.annotation_type, ma.content
		FROM message_annotations ma
		JOIN messages m ON ma.message_id = m.id
		WHERE ma.evaluation_id = $1
		ORDER BY m.sequence, ma.annotation_type
	`, evalID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "annotations query: %v\n", err)
		os.Exit(1)
	}
	defer aRows.Close()

	var annotations []Annotation
	for aRows.Next() {
		var a Annotation
		if err := aRows.Scan(&a.MessageSeq, &a.Type, &a.Content); err != nil {
			fmt.Fprintf(os.Stderr, "scan annotation: %v\n", err)
			os.Exit(1)
		}
		annotations = append(annotations, a)
	}
	if err := aRows.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "iterating annotations: %v\n", err)
		os.Exit(1)
	}

	// Ensure nil slices marshal as [] not null
	if strengthList == nil {
		strengthList = []string{}
	}
	if gapList == nil {
		gapList = []string{}
	}
	if annotations == nil {
		annotations = []Annotation{}
	}

	writeJSON("internal/sample/fixtures/evaluation.json", EvaluationFixture{
		Status: "reviewed",
		Scores: EvaluationScores{
			Requirements:  scoreReqs,
			Architecture:  scoreHL,
			DeepDive:      scoreDD,
			Scalability:   scoreScal,
			Communication: scoreComm,
			Overall:       scoreOverall,
		},
		Strengths:   strengthList,
		Gaps:        gapList,
		Advice:      advice,
		Annotations: annotations,
	})

	// --- Extract educator ---
	var modelAnswer, gapDeepDives *string
	var educatorStatus *string
	err = conn.QueryRow(ctx, `
		SELECT educator_model_answer, educator_gap_deepdives, educator_status
		FROM evaluations WHERE session_id = $1
		ORDER BY evaluated_at DESC LIMIT 1
	`, sessionID).Scan(&modelAnswer, &gapDeepDives, &educatorStatus)
	if err != nil {
		fmt.Fprintf(os.Stderr, "educator query: %v\n", err)
		os.Exit(1)
	}

	writeJSON("internal/sample/fixtures/educator.json", EducatorFixture{
		ID:           fmt.Sprintf("%d", evalID),
		SessionID:    fmt.Sprintf("%d", sessionID),
		Status:       derefStr(educatorStatus),
		ModelAnswer:  derefStr(modelAnswer),
		GapDeepDives: derefStr(gapDeepDives),
		CreatedAt:    startedAt.Format(time.RFC3339),
	})

	// --- Extract coach ---
	var (
		coachID         int
		recommendation  string
		gapAnalysisJSON []byte
		suggestedQID    *int
		sessionsJSON    []int
		coachCreatedAt  time.Time
	)
	err = conn.QueryRow(ctx, `
		SELECT id, recommendation, gap_analysis, suggested_question_id,
		       sessions_analyzed, created_at
		FROM coach_reviews
		WHERE sessions_analyzed @> ARRAY[$1]::INTEGER[]
		ORDER BY created_at DESC LIMIT 1
	`, sessionID).Scan(
		&coachID, &recommendation, &gapAnalysisJSON,
		&suggestedQID, &sessionsJSON, &coachCreatedAt,
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "coach query: %v\n", err)
		os.Exit(1)
	}

	// NOTE: v0 is single-user so no WHERE user_id clause needed.
	trendRows, err := conn.Query(ctx, `
		SELECT s.started_at, e.score_overall
		FROM sessions s
		JOIN evaluations e ON e.session_id = s.id
		WHERE s.status = 'reviewed'
		ORDER BY s.started_at
	`)
	if err != nil {
		fmt.Fprintf(os.Stderr, "trend query: %v\n", err)
		os.Exit(1)
	}
	defer trendRows.Close()

	var trend []ScoreTrendPoint
	for trendRows.Next() {
		var date time.Time
		var score int
		if err := trendRows.Scan(&date, &score); err != nil {
			fmt.Fprintf(os.Stderr, "scan trend: %v\n", err)
			os.Exit(1)
		}
		trend = append(trend, ScoreTrendPoint{
			Date:         date.Format("2006-01-02"),
			OverallScore: score,
		})
	}
	if err := trendRows.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "iterating trend: %v\n", err)
		os.Exit(1)
	}

	if trend == nil {
		trend = []ScoreTrendPoint{}
	}

	var gapAnalysis map[string]any
	if err := json.Unmarshal(gapAnalysisJSON, &gapAnalysis); err != nil {
		fmt.Fprintf(os.Stderr, "unmarshal gap_analysis: %v\n", err)
		os.Exit(1)
	}

	sessionIDs := make([]string, len(sessionsJSON))
	for i, id := range sessionsJSON {
		sessionIDs[i] = fmt.Sprintf("%d", id)
	}

	coach := CoachFixture{
		ID:               fmt.Sprintf("%d", coachID),
		UserID:           "sample",
		Narrative:        recommendation,
		SessionsAnalyzed: sessionIDs,
		CreatedAt:        coachCreatedAt.Format(time.RFC3339),
		ScoreTrend:       trend,
	}
	if wd, ok := gapAnalysis["weakest_dimension"].(string); ok {
		coach.WeakestDimension = &wd
	}
	if imp, ok := gapAnalysis["improving_dimensions"].([]any); ok {
		for _, v := range imp {
			if s, ok := v.(string); ok {
				coach.ImprovingDimensions = append(coach.ImprovingDimensions, s)
			}
		}
	}
	if tg, ok := gapAnalysis["topic_gaps"].([]any); ok {
		for _, v := range tg {
			if s, ok := v.(string); ok {
				coach.TopicGaps = append(coach.TopicGaps, s)
			}
		}
	}
	if sqid, ok := gapAnalysis["suggested_question_id"]; ok && sqid != nil {
		s := fmt.Sprintf("%v", sqid)
		coach.SuggestedQuestionID = &s
	}

	if coach.ImprovingDimensions == nil {
		coach.ImprovingDimensions = []string{}
	}
	if coach.TopicGaps == nil {
		coach.TopicGaps = []string{}
	}

	writeJSON("internal/sample/fixtures/coach.json", coach)

	fmt.Println("Done. Fixtures written to internal/sample/fixtures/")
}

func writeJSON(path string, v any) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal %s: %v\n", path, err)
		os.Exit(1)
	}
	if err := os.MkdirAll("internal/sample/fixtures", 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", path, err)
		os.Exit(1)
	}
	fmt.Printf("Wrote %s (%d bytes)\n", path, len(data))
}

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
