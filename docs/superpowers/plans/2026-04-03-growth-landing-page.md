# Growth, Landing Page & Launch Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the public landing page, sample session page, and session replay feature so Sabermetric can launch on Show HN.

**Architecture:** Static JSON fixtures extracted from v0 session 27 data, served by 4 new public Go endpoints. Landing page is a single-scroll React page with scroll-triggered animations. Session detail components gain a `dataSource` prop to support both authenticated and public data sources. Session replay reconstructs interviews from stored message timestamps and audio files.

**Tech Stack:** Go (backend endpoints, OG meta tags), React 19 + TypeScript (landing page, replay), TanStack Query (data fetching), Tailwind CSS + shadcn/ui (styling), Intersection Observer API (scroll animations)

**Branch:** Create from `ui-phase` (which has the complete frontend). The `ui-phase` branch is code complete and has all session detail components, API client, query hooks, and design system.

**Spec:** `docs/superpowers/specs/2026-04-03-growth-landing-onboarding-design.md`

---

## File Structure

### New Files

```
# Data extraction (one-time tool)
cmd/extract-sample/main.go              # Extracts v0 session 27 → JSON fixtures

# Backend — sample data
internal/sample/handler.go              # HTTP handlers for /api/sample/* endpoints
internal/sample/embed.go                # Embeds fixture JSON files
internal/sample/fixtures/session.json   # Session metadata + transcript
internal/sample/fixtures/evaluation.json # Scores, strengths, gaps, annotations
internal/sample/fixtures/educator.json  # Model answer + gap deep dives
internal/sample/fixtures/coach.json     # Coach analysis + score trend data

# Frontend — landing page
web/src/pages/landing/index.tsx         # Landing page shell (scroll container)
web/src/pages/landing/hero.tsx          # Section 1: wordmark + rotating tagline + CTA
web/src/pages/landing/scoring.tsx       # Section 2: animated score bars
web/src/pages/landing/strengths-gaps.tsx # Section 3: strengths/gaps/advice cards
web/src/pages/landing/annotations.tsx   # Section 4: transcript with annotation callouts
web/src/pages/landing/deep-dive.tsx     # Section 5: educator excerpt
web/src/pages/landing/coaching.tsx      # Section 6: coach card + sparkline
web/src/pages/landing/voice-pipeline.tsx # Section 7: waveform → transcript → analysis
web/src/pages/landing/sample-session.tsx # Section 8: "see a real evaluation" link
web/src/pages/landing/credits.tsx       # Section 9: built-with list
web/src/pages/landing/cta-repeat.tsx    # Section 10: final CTA

# Frontend — infrastructure
web/src/hooks/use-scroll-reveal.ts      # Intersection observer for scroll animations
web/src/api/sample-queries.ts           # TanStack Query hooks for /api/sample/*

# Frontend — sample session page
web/src/pages/sample.tsx                # /sample route — session detail with public data

# Frontend — session replay
web/src/components/replay/engine.ts     # Replay state machine (timing, playback)
web/src/components/replay/controls.tsx  # Play/pause, speed, scrubber UI
web/src/components/replay/timeline.tsx  # Seekable timeline bar
```

### Modified Files

```
internal/handler/routes.go              # Register /api/sample/* routes, OG meta tag injection
web/src/app.tsx                         # Add landing page + /sample routes, conditional auth
web/src/hooks/use-auth.ts               # Add useOptionalAuth() for conditional routes
web/src/api/queries.ts                  # Add dataSource parameter to session/eval/educator hooks
web/src/pages/session/layout.tsx        # Accept dataSource prop for public mode
web/src/pages/session/overview.tsx      # Accept dataSource prop
web/src/pages/session/transcript.tsx    # Accept dataSource prop, add replay button
web/src/pages/session/deep-dive.tsx     # Accept dataSource prop
```

---

## Task 1: Extract v0 Session 27 Data into JSON Fixtures

**Files:**
- Create: `cmd/extract-sample/main.go`
- Create: `internal/sample/fixtures/session.json`
- Create: `internal/sample/fixtures/evaluation.json`
- Create: `internal/sample/fixtures/educator.json`
- Create: `internal/sample/fixtures/coach.json`

This task creates a one-time extraction tool that connects to the v0 database (`drill_v0`), queries session 27 data, transforms it into v1 API response shapes, and writes JSON fixture files.

- [ ] **Step 1: Write the extraction tool**

```go
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
	ID            string `json:"id"`
	SessionID     string `json:"session_id"`
	Status        string `json:"status"`
	ModelAnswer   string `json:"model_answer"`
	GapDeepDives  string `json:"gap_deep_dives"`
	CreatedAt     string `json:"created_at"`
}

type ScoreTrendPoint struct {
	Date         string `json:"date"`
	OverallScore int    `json:"overall_score"`
}

type CoachFixture struct {
	ID                   string           `json:"id"`
	UserID               string           `json:"user_id"`
	Narrative            string           `json:"narrative"`
	WeakestDimension     *string          `json:"weakest_dimension"`
	ImprovingDimensions  []string         `json:"improving_dimensions"`
	TopicGaps            []string         `json:"topic_gaps"`
	SuggestedQuestionID  *string          `json:"suggested_question_id"`
	SessionsAnalyzed     []string         `json:"sessions_analyzed"`
	CreatedAt            string           `json:"created_at"`
	ScoreTrend           []ScoreTrendPoint `json:"score_trend"`
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
		// Audio URLs will be updated after GCS migration
		if audioPath != nil {
			msg.AudioURL = audioPath
		}
		messages = append(messages, msg)
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
		strengths    []byte // JSONB
		gaps         []byte // JSONB
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
	json.Unmarshal(strengths, &strengthList)
	json.Unmarshal(gaps, &gapList)

	// Annotations
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

	// Build score trend from all user sessions
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

	// Map coach_reviews fields → v1 CoachAnalysis shape
	// v0 has recommendation + gap_analysis; v1 has narrative + weakest_dimension etc.
	// The narrative is the recommendation text. weakest_dimension, improving_dimensions,
	// topic_gaps are extracted from gap_analysis JSON.
	var gapAnalysis map[string]any
	json.Unmarshal(gapAnalysisJSON, &gapAnalysis)

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
	// Extract structured fields from gap_analysis if present
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
```

- [ ] **Step 2: Run the extraction tool**

```bash
cd /Users/btc/Projects/src/drill
DATABASE_URL=postgresql://localhost:5432/drill_v0 go run ./cmd/extract-sample/
```

Expected: 4 JSON files written to `internal/sample/fixtures/`. Inspect each file to verify data looks correct — real scores, real transcript text, real annotations.

- [ ] **Step 3: Verify fixture data shapes match v1 TypeScript types**

Open each fixture file and cross-reference field names against `web/src/api/types.ts`:
- `session.json` → `Session` + `Message[]`
- `evaluation.json` → `EvaluationResponse`
- `educator.json` → `EducatorAnalysis`
- `coach.json` → `CoachAnalysis` (plus `score_trend` extension)

Fix any field name mismatches in the extraction tool and re-run if needed.

- [ ] **Step 4: Update audio_url fields with placeholder paths**

After extraction, edit `session.json` to update `audio_url` values from v0 local paths to the target GCS or static paths (e.g., `/static/sample/audio/turn_001_chunk_000.mp3`). This will be finalized when audio migration is done, but the shape should be correct now.

- [ ] **Step 5: Commit**

```bash
git add cmd/extract-sample/ internal/sample/fixtures/
git commit -m "feat: extract v0 session 27 data into sample fixtures"
```

---

## Task 2: Go Backend — Sample Data Endpoints

**Files:**
- Create: `internal/sample/embed.go`
- Create: `internal/sample/handler.go`
- Modify: `internal/handler/routes.go`

- [ ] **Step 1: Write the embed file**

```go
// internal/sample/embed.go
package sample

import "embed"

//go:embed fixtures/*.json
var FixtureFS embed.FS
```

- [ ] **Step 2: Write the handler tests**

```go
// internal/sample/handler_test.go
package sample_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/btc/drill/internal/sample"
)

func TestSampleSessionEndpoint(t *testing.T) {
	mux := http.NewServeMux()
	sample.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/api/sample/session", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Fatalf("expected application/json, got %s", ct)
	}
	if w.Body.Len() == 0 {
		t.Fatal("empty response body")
	}
}

func TestSampleEvaluationEndpoint(t *testing.T) {
	mux := http.NewServeMux()
	sample.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/api/sample/evaluation", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestSampleEducatorEndpoint(t *testing.T) {
	mux := http.NewServeMux()
	sample.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/api/sample/educator", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestSampleCoachEndpoint(t *testing.T) {
	mux := http.NewServeMux()
	sample.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/api/sample/coach", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

```bash
go test ./internal/sample/ -v
```

Expected: compilation error — `sample.RegisterRoutes` does not exist yet.

- [ ] **Step 4: Write the handler implementation**

```go
// internal/sample/handler.go
package sample

import (
	"net/http"
)

// RegisterRoutes adds the public sample data endpoints to the mux.
// These require no authentication.
func RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/sample/session", serveFixture("fixtures/session.json"))
	mux.HandleFunc("GET /api/sample/evaluation", serveFixture("fixtures/evaluation.json"))
	mux.HandleFunc("GET /api/sample/educator", serveFixture("fixtures/educator.json"))
	mux.HandleFunc("GET /api/sample/coach", serveFixture("fixtures/coach.json"))
}

func serveFixture(path string) http.HandlerFunc {
	// Read at init time — embedded files don't change
	data, err := FixtureFS.ReadFile(path)
	if err != nil {
		panic("missing fixture: " + path)
	}

	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Write(data)
	}
}
```

- [ ] **Step 5: Run tests to verify they pass**

```bash
go test ./internal/sample/ -v
```

Expected: all 4 tests pass.

- [ ] **Step 6: Register sample routes in main router**

Add to `internal/handler/routes.go`, at the top of `RegisterRoutes` before auth routes:

```go
// Sample data — public, no auth required
sample.RegisterRoutes(mux)
```

Add import: `"github.com/btc/drill/internal/sample"`

- [ ] **Step 7: Commit**

```bash
git add internal/sample/ internal/handler/routes.go
git commit -m "feat: add public sample data endpoints for landing page"
```

---

## Task 3: Go Backend — OG Meta Tag Injection

**Files:**
- Modify: `internal/handler/routes.go` (SPAHandler function)

- [ ] **Step 1: Write a test for OG tag injection**

```go
// internal/handler/routes_test.go (add to existing or create)
package handler_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/btc/drill/internal/handler"
)

func TestSPAHandlerOGTags(t *testing.T) {
	// Minimal fake embedded FS with an index.html containing a head tag
	indexHTML := `<!DOCTYPE html><html><head><meta charset="utf-8"></head><body></body></html>`
	fsys := fstest.MapFS{
		"web/dist/index.html": &fstest.MapFile{Data: []byte(indexHTML)},
	}

	h := handler.SPAHandler(fsys)

	tests := []struct {
		path     string
		wantTag  string
	}{
		{"/", `og:title" content="Sabermetric"`},
		{"/sample", `og:title" content="Sabermetric — sample evaluation"`},
		{"/login", ""},   // no OG tags for other routes
	}

	for _, tt := range tests {
		req := httptest.NewRequest("GET", tt.path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		body := w.Body.String()
		if tt.wantTag != "" && !strings.Contains(body, tt.wantTag) {
			t.Errorf("path %s: expected OG tag %q in body", tt.path, tt.wantTag)
		}
		if tt.wantTag == "" && strings.Contains(body, "og:title") {
			t.Errorf("path %s: unexpected OG tag in body", tt.path)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/handler/ -run TestSPAHandlerOGTags -v
```

Expected: FAIL — SPAHandler doesn't accept `fstest.MapFS` (it takes `embed.FS`). Refactor the signature.

- [ ] **Step 3: Refactor SPAHandler to accept fs.FS interface**

Update `SPAHandler` in `internal/handler/routes.go` to accept `fs.FS` instead of `embed.FS` so it's testable:

```go
// SPAHandler serves the embedded SPA with OG meta tag injection for public routes.
func SPAHandler(fsys fs.FS) http.Handler {
	sub, err := fs.Sub(fsys, "web/dist")
	if err != nil {
		panic(fmt.Sprintf("embed sub: %v", err))
	}
	fileServer := http.FileServer(http.FS(sub))

	// Read index.html once at init
	indexBytes, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		panic(fmt.Sprintf("read index.html: %v", err))
	}
	indexHTML := string(indexBytes)

	ogTags := map[string]string{
		"/": `<meta property="og:title" content="Sabermetric">` +
			`<meta property="og:description" content="data-driven system design prep">` +
			`<meta property="og:type" content="website">`,
		"/sample": `<meta property="og:title" content="Sabermetric — sample evaluation">` +
			`<meta property="og:description" content="See a real system design interview evaluated across 5 dimensions">` +
			`<meta property="og:type" content="website">`,
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path != "/" {
			if _, err := fs.Stat(sub, strings.TrimPrefix(path, "/")); err == nil {
				if strings.Contains(path, "/assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				fileServer.ServeHTTP(w, r)
				return
			}
		}

		// Serve index.html with optional OG tag injection
		html := indexHTML
		if tags, ok := ogTags[path]; ok {
			html = strings.Replace(html, "</head>", tags+"</head>", 1)
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(html))
	})
}
```

Update the call site in `cmd/drill/main.go` — `SPAHandler` now takes `fs.FS` which `embed.FS` satisfies, so no change needed there.

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/handler/ -run TestSPAHandlerOGTags -v
```

Expected: PASS — all 3 paths return expected OG tags (or lack thereof).

- [ ] **Step 5: Commit**

```bash
git add internal/handler/routes.go internal/handler/routes_test.go
git commit -m "feat: inject OG meta tags for landing page and sample session"
```

---

## Task 4: Frontend — Scroll Animation Hook

**Files:**
- Create: `web/src/hooks/use-scroll-reveal.ts`

- [ ] **Step 1: Write the hook**

```typescript
// web/src/hooks/use-scroll-reveal.ts
import { useEffect, useRef, useState } from "react";

interface ScrollRevealOptions {
  threshold?: number;
  rootMargin?: string;
  /** If true, only triggers once (default: true) */
  once?: boolean;
}

export function useScrollReveal<T extends HTMLElement>(
  options: ScrollRevealOptions = {},
) {
  const { threshold = 0.2, rootMargin = "0px", once = true } = options;
  const ref = useRef<T>(null);
  const [isVisible, setIsVisible] = useState(false);

  useEffect(() => {
    const el = ref.current;
    if (!el) return;

    const observer = new IntersectionObserver(
      ([entry]) => {
        if (entry.isIntersecting) {
          setIsVisible(true);
          if (once) observer.unobserve(el);
        } else if (!once) {
          setIsVisible(false);
        }
      },
      { threshold, rootMargin },
    );

    observer.observe(el);
    return () => observer.disconnect();
  }, [threshold, rootMargin, once]);

  return { ref, isVisible };
}
```

- [ ] **Step 2: Commit**

```bash
git add web/src/hooks/use-scroll-reveal.ts
git commit -m "feat: add useScrollReveal hook for landing page animations"
```

---

## Task 5: Frontend — Sample Data Query Hooks

**Files:**
- Create: `web/src/api/sample-queries.ts`

- [ ] **Step 1: Write the sample query hooks**

```typescript
// web/src/api/sample-queries.ts
import { useQuery } from "@tanstack/react-query";
import { apiClient } from "./client";
import type {
  Session, Message, EvaluationResponse,
  EducatorAnalysis, CoachAnalysis,
} from "./types";

interface SessionFixture {
  session: Session;
  messages: Message[];
}

export interface ScoreTrendPoint {
  date: string;
  overall_score: number;
}

export interface CoachFixture extends CoachAnalysis {
  score_trend: ScoreTrendPoint[];
}

export function useSampleSession() {
  return useQuery({
    queryKey: ["sample", "session"],
    queryFn: () => apiClient.get<SessionFixture>("/api/sample/session"),
    staleTime: Infinity,
  });
}

export function useSampleEvaluation() {
  return useQuery({
    queryKey: ["sample", "evaluation"],
    queryFn: () => apiClient.get<EvaluationResponse>("/api/sample/evaluation"),
    staleTime: Infinity,
  });
}

export function useSampleEducator() {
  return useQuery({
    queryKey: ["sample", "educator"],
    queryFn: () => apiClient.get<EducatorAnalysis>("/api/sample/educator"),
    staleTime: Infinity,
  });
}

export function useSampleCoach() {
  return useQuery({
    queryKey: ["sample", "coach"],
    queryFn: () => apiClient.get<CoachFixture>("/api/sample/coach"),
    staleTime: Infinity,
  });
}
```

- [ ] **Step 2: Commit**

```bash
git add web/src/api/sample-queries.ts
git commit -m "feat: add TanStack Query hooks for sample data endpoints"
```

---

## Task 6: Frontend — Routing Changes

**Files:**
- Modify: `web/src/app.tsx`
- Modify: `web/src/hooks/use-auth.ts`
- Modify: `web/src/layouts/app-layout.tsx`

- [ ] **Step 1: Add useOptionalAuth hook**

Add to `web/src/hooks/use-auth.ts`:

```typescript
/**
 * Checks auth state without redirecting. For routes that render
 * different content based on auth (e.g., landing page vs dashboard).
 */
export function useOptionalAuth() {
  const { data: user, isLoading, isError } = useMe();
  return { user, isLoading, isAuthenticated: !!user && !isError };
}
```

- [ ] **Step 2: Create conditional root route component**

Add to `web/src/app.tsx` — a component that renders landing page for unauthenticated users, or redirects to the app for authenticated users:

```typescript
const Landing = lazy(() => import("@/pages/landing"));
const SampleSession = lazy(() => import("@/pages/sample"));

function RootRoute() {
  const { isAuthenticated, isLoading } = useOptionalAuth();

  if (isLoading) {
    return <Loading />;
  }

  if (isAuthenticated) {
    return (
      <AppLayout>
        <Home />
      </AppLayout>
    );
  }

  return <Landing />;
}
```

Wait — this won't work cleanly with React Router's layout nesting. Instead, restructure the routes:

```typescript
import { useOptionalAuth } from "@/hooks/use-auth";

// ... existing lazy imports ...
const Landing = lazy(() => import("@/pages/landing"));
const SampleSession = lazy(() => import("@/pages/sample"));

function ConditionalHome() {
  const { isAuthenticated, isLoading } = useOptionalAuth();
  if (isLoading) return <Loading />;
  if (isAuthenticated) return <Home />;
  return <Landing />;
}

export function App() {
  return (
    <Suspense fallback={<Loading />}>
      <Routes>
        {/* Public — no layout */}
        <Route path="/login" element={<Login />} />
        <Route path="/signup" element={<Signup />} />
        <Route path="/forgot-password" element={<ForgotPassword />} />
        <Route path="/reset-password" element={<ResetPassword />} />
        <Route path="/verify-email" element={<VerifyEmail />} />
        <Route path="/sample" element={<SampleSession />} />

        {/* Root — conditional: landing (unauth) or app layout (auth) */}
        <Route path="/" element={<ConditionalHome />} />

        {/* App — top bar layout (all require auth) */}
        <Route element={<AppLayout />}>
          <Route path="/sessions/new" element={<SessionConfig />} />
          <Route path="/sessions/:id" element={<SessionLayout />}>
            <Route index element={<Navigate to="overview" replace />} />
            <Route path="overview" element={<Overview />} />
            <Route path="transcript" element={<TranscriptPage />} />
            <Route path="deep-dive" element={<DeepDive />} />
          </Route>
          <Route path="/history" element={<History />} />
          <Route path="/settings" element={<Settings />} />
          <Route path="/settings/billing" element={<Settings />} />
        </Route>

        {/* Interview — immersive layout */}
        <Route element={<ImmersiveLayout />}>
          <Route path="/sessions/:id/interview" element={<Interview />} />
        </Route>
      </Routes>
    </Suspense>
  );
}
```

Note: `ConditionalHome` handles the `/` route. When authenticated, it renders `Home` — but without the `AppLayout` wrapper. To fix this, wrap it conditionally:

```typescript
function ConditionalHome() {
  const { isAuthenticated, isLoading } = useOptionalAuth();
  if (isLoading) return <Loading />;
  if (!isAuthenticated) return <Landing />;
  // Authenticated: render within AppLayout
  return (
    <AppLayout>
      <Home />
    </AppLayout>
  );
}
```

And update `AppLayout` to accept children as an alternative to `<Outlet />`:

- [ ] **Step 3: Update AppLayout to support children**

In `web/src/layouts/app-layout.tsx`, update the return to use `children` when provided, otherwise `<Outlet />`:

```typescript
export function AppLayout({ children }: { children?: React.ReactNode }) {
  // ... existing hook calls ...

  return (
    <div className="min-h-screen bg-background text-foreground">
      <header className="border-b border-border">
        {/* ... existing header ... */}
      </header>
      <main className="mx-auto max-w-5xl px-4 py-6">
        {children ?? <Outlet />}
      </main>
    </div>
  );
}
```

- [ ] **Step 4: Verify the app builds**

```bash
cd web && npm run build
```

Expected: build succeeds (landing page and sample page are lazy-loaded stubs for now).

- [ ] **Step 5: Commit**

```bash
git add web/src/app.tsx web/src/hooks/use-auth.ts web/src/layouts/app-layout.tsx
git commit -m "feat: conditional root route and /sample public route"
```

---

## Task 7: Landing Page — Hero Section

**Files:**
- Create: `web/src/pages/landing/index.tsx`
- Create: `web/src/pages/landing/hero.tsx`

- [ ] **Step 1: Write the hero component**

```tsx
// web/src/pages/landing/hero.tsx
import { useState, useEffect } from "react";
import { Link } from "react-router-dom";

const TAGLINES = [
  "system design, measured.",
  "measure what matters.",
  "practice with precision.",
  "the science of system design prep.",
  "data-driven system design prep.",
];

const CYCLE_MS = 3000;

export function Hero() {
  const [index, setIndex] = useState(0);
  const [visible, setVisible] = useState(true);
  const isLast = index === TAGLINES.length - 1;

  useEffect(() => {
    if (isLast) return; // Hold on final tagline

    const timer = setInterval(() => {
      setVisible(false);
      setTimeout(() => {
        setIndex((i) => i + 1);
        setVisible(true);
      }, 300); // fade out duration
    }, CYCLE_MS);

    return () => clearInterval(timer);
  }, [isLast]);

  return (
    <section className="flex min-h-screen flex-col items-center justify-center gap-8 px-4">
      <h1 className="text-5xl font-light tracking-tight text-foreground sm:text-7xl">
        sabermetric
      </h1>
      <p
        className={`text-lg text-muted-foreground transition-opacity duration-300 sm:text-xl ${
          visible ? "opacity-100" : "opacity-0"
        }`}
      >
        {TAGLINES[index]}
      </p>
      <Link
        to="/signup"
        className="mt-4 rounded-md bg-primary px-8 py-3 text-sm font-medium text-primary-foreground transition-colors hover:bg-primary/90"
      >
        Start practicing
      </Link>
    </section>
  );
}
```

- [ ] **Step 2: Write the landing page shell**

```tsx
// web/src/pages/landing/index.tsx
import { Hero } from "./hero";

export default function Landing() {
  return (
    <div className="min-h-screen bg-background text-foreground">
      <Hero />
      {/* Remaining sections added in subsequent tasks */}
    </div>
  );
}
```

- [ ] **Step 3: Verify the landing page renders**

```bash
cd web && npm run dev
```

Visit `http://localhost:5173/` while logged out. Expected: hero with "sabermetric" wordmark, rotating taglines, and CTA button.

- [ ] **Step 4: Commit**

```bash
git add web/src/pages/landing/
git commit -m "feat: landing page hero with rotating tagline"
```

---

## Task 8: Landing Page — Scoring Section

**Files:**
- Create: `web/src/pages/landing/scoring.tsx`
- Modify: `web/src/pages/landing/index.tsx`

- [ ] **Step 1: Write the scoring component**

```tsx
// web/src/pages/landing/scoring.tsx
import { useEffect, useState } from "react";
import { useScrollReveal } from "@/hooks/use-scroll-reveal";
import { useSampleEvaluation, useSampleSession } from "@/api/sample-queries";

const DIMENSIONS = [
  { key: "requirements", label: "Requirements" },
  { key: "architecture", label: "Architecture" },
  { key: "deep_dive", label: "Deep Dive" },
  { key: "scalability", label: "Scalability" },
  { key: "communication", label: "Communication" },
] as const;

function AnimatedBar({ value, delay, animate }: { value: number; delay: number; animate: boolean }) {
  const [width, setWidth] = useState(0);

  useEffect(() => {
    if (!animate) return;
    const timer = setTimeout(() => setWidth((value / 5) * 100), delay);
    return () => clearTimeout(timer);
  }, [animate, value, delay]);

  return (
    <div className="h-2 w-full rounded-full bg-muted">
      <div
        className="h-2 rounded-full bg-primary transition-all duration-700 ease-out"
        style={{ width: `${width}%` }}
      />
    </div>
  );
}

export function Scoring() {
  const { ref, isVisible } = useScrollReveal<HTMLElement>();
  const { data: evaluation } = useSampleEvaluation();
  const { data: sessionData } = useSampleSession();

  if (!evaluation?.scores) return null;

  return (
    <section ref={ref} className="flex min-h-screen flex-col items-center justify-center gap-12 px-4">
      <div className="max-w-2xl space-y-4 text-center">
        <h2 className="text-3xl font-light text-foreground sm:text-4xl">
          Scored across five dimensions
        </h2>
        {sessionData?.session.question_title && (
          <p className="text-sm text-muted-foreground">
            From a session on: {sessionData.session.question_title}
          </p>
        )}
      </div>
      <div className="w-full max-w-lg space-y-6">
        {DIMENSIONS.map((dim, i) => (
          <div key={dim.key} className="space-y-1.5">
            <div className="flex items-center justify-between text-sm">
              <span className="text-muted-foreground">{dim.label}</span>
              <span className="font-medium text-foreground">
                {isVisible ? evaluation.scores![dim.key] : 0}/5
              </span>
            </div>
            <AnimatedBar
              value={evaluation.scores![dim.key]}
              delay={i * 150}
              animate={isVisible}
            />
          </div>
        ))}
        {/* Overall score — larger, last */}
        <div className="border-t border-border pt-6 space-y-1.5">
          <div className="flex items-center justify-between">
            <span className="text-lg text-foreground font-medium">Overall</span>
            <span className="text-2xl font-semibold text-primary">
              {isVisible ? evaluation.scores!.overall : 0}/5
            </span>
          </div>
          <AnimatedBar
            value={evaluation.scores!.overall}
            delay={DIMENSIONS.length * 150}
            animate={isVisible}
          />
        </div>
      </div>
    </section>
  );
}
```

- [ ] **Step 2: Add to landing page**

In `web/src/pages/landing/index.tsx`, add the import and component:

```tsx
import { Hero } from "./hero";
import { Scoring } from "./scoring";

export default function Landing() {
  return (
    <div className="min-h-screen bg-background text-foreground">
      <Hero />
      <Scoring />
    </div>
  );
}
```

- [ ] **Step 3: Verify it renders**

Visit the landing page, scroll down. Expected: score bars animate from 0 to their values when the section enters the viewport.

- [ ] **Step 4: Commit**

```bash
git add web/src/pages/landing/scoring.tsx web/src/pages/landing/index.tsx
git commit -m "feat: landing page scoring section with animated bars"
```

---

## Task 9: Landing Page — Strengths, Gaps, Advice Section

**Files:**
- Create: `web/src/pages/landing/strengths-gaps.tsx`
- Modify: `web/src/pages/landing/index.tsx`

- [ ] **Step 1: Write the component**

```tsx
// web/src/pages/landing/strengths-gaps.tsx
import { useScrollReveal } from "@/hooks/use-scroll-reveal";
import { useSampleEvaluation } from "@/api/sample-queries";

function FadeInCard({
  children,
  delay,
  animate,
  accent,
}: {
  children: React.ReactNode;
  delay: number;
  animate: boolean;
  accent: string;
}) {
  return (
    <div
      className={`border-l-2 pl-4 py-2 transition-all duration-500 ${
        animate ? "opacity-100 translate-y-0" : "opacity-0 translate-y-4"
      }`}
      style={{
        borderColor: `hsl(var(--${accent}))`,
        transitionDelay: `${delay}ms`,
      }}
    >
      {children}
    </div>
  );
}

export function StrengthsGaps() {
  const { ref, isVisible } = useScrollReveal<HTMLElement>();
  const { data: evaluation } = useSampleEvaluation();

  if (!evaluation?.strengths) return null;

  // Show first 2 of each for the landing page
  const strengths = evaluation.strengths.slice(0, 2);
  const gaps = evaluation.gaps?.slice(0, 2) ?? [];

  return (
    <section ref={ref} className="flex min-h-screen flex-col items-center justify-center gap-12 px-4">
      <div className="max-w-2xl text-center">
        <h2 className="text-3xl font-light text-foreground sm:text-4xl">
          Know exactly where you stand
        </h2>
      </div>
      <div className="w-full max-w-2xl space-y-8">
        {/* Strengths */}
        <div className="space-y-3">
          {strengths.map((s, i) => (
            <FadeInCard key={i} delay={i * 200} animate={isVisible} accent="strength">
              <p className="text-sm text-foreground">{s}</p>
            </FadeInCard>
          ))}
        </div>
        {/* Gaps */}
        <div className="space-y-3">
          {gaps.map((g, i) => (
            <FadeInCard key={i} delay={(strengths.length + i) * 200} animate={isVisible} accent="gap">
              <p className="text-sm text-foreground">{g}</p>
            </FadeInCard>
          ))}
        </div>
        {/* Advice */}
        {evaluation.advice && (
          <FadeInCard
            delay={(strengths.length + gaps.length) * 200}
            animate={isVisible}
            accent="primary"
          >
            <p className="text-sm text-muted-foreground">{evaluation.advice}</p>
          </FadeInCard>
        )}
      </div>
    </section>
  );
}
```

- [ ] **Step 2: Add to landing page index**

Add `import { StrengthsGaps } from "./strengths-gaps";` and `<StrengthsGaps />` after `<Scoring />`.

- [ ] **Step 3: Verify and commit**

```bash
git add web/src/pages/landing/strengths-gaps.tsx web/src/pages/landing/index.tsx
git commit -m "feat: landing page strengths, gaps, advice section"
```

---

## Task 10: Landing Page — Annotations Section

**Files:**
- Create: `web/src/pages/landing/annotations.tsx`
- Modify: `web/src/pages/landing/index.tsx`

- [ ] **Step 1: Write the component**

```tsx
// web/src/pages/landing/annotations.tsx
import { useScrollReveal } from "@/hooks/use-scroll-reveal";
import { useSampleEvaluation, useSampleSession } from "@/api/sample-queries";
import type { AnnotationType } from "@/api/types";
import { cn } from "@/lib/utils";

const ANNOTATION_COLORS: Record<AnnotationType, string> = {
  strength: "border-strength text-strength",
  gap: "border-gap text-gap",
  missed_opportunity: "border-missed text-missed",
  note: "border-note text-note",
};

const ANNOTATION_LABELS: Record<AnnotationType, string> = {
  strength: "Strength",
  gap: "Gap",
  missed_opportunity: "Missed opportunity",
  note: "Note",
};

export function Annotations() {
  const { ref, isVisible } = useScrollReveal<HTMLElement>();
  const { data: sessionData } = useSampleSession();
  const { data: evaluation } = useSampleEvaluation();

  if (!sessionData?.messages || !evaluation?.annotations) return null;

  // Pick a 3-5 message window that has annotations
  const annotatedSeqs = new Set(evaluation.annotations.map((a) => a.message_seq));
  const firstAnnotated = sessionData.messages.find((m) => annotatedSeqs.has(m.seq));
  if (!firstAnnotated) return null;

  const startSeq = Math.max(1, firstAnnotated.seq - 1);
  const window = sessionData.messages.filter(
    (m) => m.seq >= startSeq && m.seq < startSeq + 5,
  );
  const windowAnnotations = evaluation.annotations.filter(
    (a) => a.message_seq >= startSeq && a.message_seq < startSeq + 5,
  );

  return (
    <section ref={ref} className="flex min-h-screen flex-col items-center justify-center gap-12 px-4">
      <div className="max-w-2xl text-center">
        <h2 className="text-3xl font-light text-foreground sm:text-4xl">
          Feedback on what you actually said
        </h2>
      </div>
      <div className="w-full max-w-2xl space-y-4">
        {window.map((msg, msgIdx) => {
          const msgAnnotations = windowAnnotations.filter(
            (a) => a.message_seq === msg.seq,
          );
          return (
            <div key={msg.seq} className="space-y-2">
              <div
                className={cn(
                  "rounded-lg px-4 py-3 text-sm",
                  msg.role === "interviewer"
                    ? "bg-muted text-foreground mr-12"
                    : "bg-primary/10 text-foreground ml-12",
                )}
              >
                <span className="text-xs font-medium text-muted-foreground">
                  {msg.role === "interviewer" ? "Interviewer" : "Candidate"}
                </span>
                <p className="mt-1">{msg.content.slice(0, 200)}{msg.content.length > 200 ? "..." : ""}</p>
              </div>
              {msgAnnotations.map((ann, annIdx) => (
                <div
                  key={annIdx}
                  className={cn(
                    "ml-8 border-l-2 pl-3 py-1 text-xs transition-all duration-500",
                    ANNOTATION_COLORS[ann.type],
                    isVisible
                      ? "opacity-100 translate-x-0"
                      : "opacity-0 -translate-x-4",
                  )}
                  style={{
                    transitionDelay: `${(msgIdx * 300) + (annIdx * 150)}ms`,
                  }}
                >
                  <span className="font-medium">{ANNOTATION_LABELS[ann.type]}:</span>{" "}
                  {ann.content}
                </div>
              ))}
            </div>
          );
        })}
      </div>
    </section>
  );
}
```

- [ ] **Step 2: Add to landing page index**

Add `import { Annotations } from "./annotations";` and `<Annotations />` after `<StrengthsGaps />`.

- [ ] **Step 3: Verify and commit**

```bash
git add web/src/pages/landing/annotations.tsx web/src/pages/landing/index.tsx
git commit -m "feat: landing page annotations section"
```

---

## Task 11: Landing Page — Deep Dive Section

**Files:**
- Create: `web/src/pages/landing/deep-dive.tsx`
- Modify: `web/src/pages/landing/index.tsx`

- [ ] **Step 1: Write the component**

```tsx
// web/src/pages/landing/deep-dive.tsx
import { useScrollReveal } from "@/hooks/use-scroll-reveal";
import { useSampleEducator } from "@/api/sample-queries";

export function DeepDivePreview() {
  const { ref, isVisible } = useScrollReveal<HTMLElement>();
  const { data: educator } = useSampleEducator();

  if (!educator?.model_answer) return null;

  // Show first ~500 chars of model answer and first gap deep dive section
  const modelPreview = educator.model_answer.slice(0, 500);
  const gapPreview = educator.gap_deep_dives?.slice(0, 400) ?? "";

  return (
    <section ref={ref} className="flex min-h-screen flex-col items-center justify-center gap-12 px-4">
      <div className="max-w-2xl text-center">
        <h2 className="text-3xl font-light text-foreground sm:text-4xl">
          Learn what you should have known
        </h2>
      </div>
      <div className="w-full max-w-2xl space-y-8">
        {/* Model answer preview */}
        <div
          className={`rounded-lg border border-border bg-card p-6 transition-all duration-700 ${
            isVisible ? "opacity-100 translate-y-0" : "opacity-0 translate-y-8"
          }`}
        >
          <h3 className="mb-3 text-xs font-medium uppercase tracking-wider text-muted-foreground">
            Model Answer
          </h3>
          <div className="prose prose-sm prose-stone dark:prose-invert max-w-none">
            <p className="text-sm text-foreground leading-relaxed">
              {modelPreview}...
            </p>
          </div>
        </div>
        {/* Gap deep dive preview */}
        {gapPreview && (
          <div
            className={`rounded-lg border border-border bg-card p-6 transition-all duration-700 ${
              isVisible ? "opacity-100 translate-y-0" : "opacity-0 translate-y-8"
            }`}
            style={{ transitionDelay: "300ms" }}
          >
            <h3 className="mb-3 text-xs font-medium uppercase tracking-wider text-muted-foreground">
              Gap Analysis
            </h3>
            <div className="prose prose-sm prose-stone dark:prose-invert max-w-none">
              <p className="text-sm text-foreground leading-relaxed">
                {gapPreview}...
              </p>
            </div>
          </div>
        )}
      </div>
    </section>
  );
}
```

- [ ] **Step 2: Add to landing page index**

Add `import { DeepDivePreview } from "./deep-dive";` and `<DeepDivePreview />` after `<Annotations />`.

- [ ] **Step 3: Verify and commit**

```bash
git add web/src/pages/landing/deep-dive.tsx web/src/pages/landing/index.tsx
git commit -m "feat: landing page deep dive section"
```

---

## Task 12: Landing Page — Coaching Section

**Files:**
- Create: `web/src/pages/landing/coaching.tsx`
- Modify: `web/src/pages/landing/index.tsx`

- [ ] **Step 1: Write the component**

```tsx
// web/src/pages/landing/coaching.tsx
import { useEffect, useState } from "react";
import { useScrollReveal } from "@/hooks/use-scroll-reveal";
import { useSampleCoach } from "@/api/sample-queries";

function Sparkline({ data, animate }: { data: { date: string; overall_score: number }[]; animate: boolean }) {
  if (data.length < 2) return null;

  const width = 280;
  const height = 60;
  const padding = 4;
  const maxScore = 5;

  const points = data.map((d, i) => ({
    x: padding + (i / (data.length - 1)) * (width - padding * 2),
    y: padding + ((maxScore - d.overall_score) / maxScore) * (height - padding * 2),
  }));

  const pathData = points.map((p, i) => `${i === 0 ? "M" : "L"} ${p.x} ${p.y}`).join(" ");

  return (
    <svg
      viewBox={`0 0 ${width} ${height}`}
      className={`w-full max-w-[280px] transition-opacity duration-700 ${
        animate ? "opacity-100" : "opacity-0"
      }`}
    >
      <path
        d={pathData}
        fill="none"
        stroke="hsl(var(--primary))"
        strokeWidth="2"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
      {/* Dots on each point */}
      {points.map((p, i) => (
        <circle
          key={i}
          cx={p.x}
          cy={p.y}
          r="3"
          fill="hsl(var(--primary))"
          className={`transition-opacity duration-300`}
          style={{ transitionDelay: `${i * 100}ms`, opacity: animate ? 1 : 0 }}
        />
      ))}
    </svg>
  );
}

export function Coaching() {
  const { ref, isVisible } = useScrollReveal<HTMLElement>();
  const { data: coach } = useSampleCoach();

  if (!coach) return null;

  return (
    <section ref={ref} className="flex min-h-screen flex-col items-center justify-center gap-12 px-4">
      <div className="max-w-2xl text-center">
        <h2 className="text-3xl font-light text-foreground sm:text-4xl">
          Track your growth across sessions
        </h2>
      </div>
      <div className="w-full max-w-lg space-y-6">
        {/* Score trend sparkline */}
        {coach.score_trend && coach.score_trend.length > 1 && (
          <div
            className={`flex flex-col items-center gap-2 transition-all duration-500 ${
              isVisible ? "opacity-100 translate-y-0" : "opacity-0 translate-y-4"
            }`}
          >
            <Sparkline data={coach.score_trend} animate={isVisible} />
            <span className="text-xs text-muted-foreground">Score trend over sessions</span>
          </div>
        )}

        {/* Weakest dimension badge */}
        {coach.weakest_dimension && (
          <div
            className={`flex items-center justify-center gap-2 transition-all duration-500 ${
              isVisible ? "opacity-100 translate-y-0" : "opacity-0 translate-y-4"
            }`}
            style={{ transitionDelay: "300ms" }}
          >
            <span className="rounded-full bg-gap/10 px-3 py-1 text-xs font-medium text-gap">
              Focus area: {coach.weakest_dimension}
            </span>
          </div>
        )}

        {/* Coach narrative excerpt */}
        <div
          className={`rounded-lg border border-border bg-card p-6 transition-all duration-500 ${
            isVisible ? "opacity-100 translate-y-0" : "opacity-0 translate-y-4"
          }`}
          style={{ transitionDelay: "500ms" }}
        >
          <p className="text-sm text-foreground leading-relaxed">
            {coach.narrative.slice(0, 300)}{coach.narrative.length > 300 ? "..." : ""}
          </p>
        </div>
      </div>
    </section>
  );
}
```

- [ ] **Step 2: Add to landing page index**

Add `import { Coaching } from "./coaching";` and `<Coaching />` after `<DeepDivePreview />`.

- [ ] **Step 3: Verify and commit**

```bash
git add web/src/pages/landing/coaching.tsx web/src/pages/landing/index.tsx
git commit -m "feat: landing page coaching section with sparkline"
```

---

## Task 13: Landing Page — Voice Pipeline, Sample Link, Credits, CTA

**Files:**
- Create: `web/src/pages/landing/voice-pipeline.tsx`
- Create: `web/src/pages/landing/sample-session.tsx`
- Create: `web/src/pages/landing/credits.tsx`
- Create: `web/src/pages/landing/cta-repeat.tsx`
- Modify: `web/src/pages/landing/index.tsx`

- [ ] **Step 1: Write the voice pipeline component**

```tsx
// web/src/pages/landing/voice-pipeline.tsx
import { useEffect, useState } from "react";
import { useScrollReveal } from "@/hooks/use-scroll-reveal";

type Stage = "waveform" | "transcript" | "annotations";

export function VoicePipeline() {
  const { ref, isVisible } = useScrollReveal<HTMLElement>();
  const [stage, setStage] = useState<Stage>("waveform");

  useEffect(() => {
    if (!isVisible) return;
    const t1 = setTimeout(() => setStage("transcript"), 1500);
    const t2 = setTimeout(() => setStage("annotations"), 3000);
    return () => { clearTimeout(t1); clearTimeout(t2); };
  }, [isVisible]);

  return (
    <section ref={ref} className="flex min-h-screen flex-col items-center justify-center gap-12 px-4">
      <div className="max-w-2xl text-center">
        <h2 className="text-3xl font-light text-foreground sm:text-4xl">
          Speak naturally. We handle the rest.
        </h2>
      </div>
      <div className="flex w-full max-w-md flex-col items-center gap-6">
        {/* Waveform stage */}
        <div
          className={`flex items-center gap-1 transition-opacity duration-500 ${
            isVisible ? "opacity-100" : "opacity-0"
          }`}
        >
          {Array.from({ length: 24 }).map((_, i) => (
            <div
              key={i}
              className={`w-1 rounded-full bg-primary ${stage === "waveform" ? "animate-pulse" : ""}`}
              style={{
                height: `${12 + Math.sin(i * 0.5) * 12 + Math.random() * 8}px`,
              }}
            />
          ))}
        </div>

        {/* Transcript stage */}
        <div
          className={`w-full rounded-lg bg-muted px-4 py-3 text-sm text-foreground transition-all duration-500 ${
            stage === "transcript" || stage === "annotations"
              ? "opacity-100 translate-y-0"
              : "opacity-0 translate-y-4"
          }`}
        >
          "I'd start by defining the API contract — the key endpoints for creating and retrieving resources..."
        </div>

        {/* Annotation stage */}
        <div
          className={`ml-8 border-l-2 border-strength pl-3 py-1 text-xs text-strength transition-all duration-500 ${
            stage === "annotations"
              ? "opacity-100 translate-x-0"
              : "opacity-0 -translate-x-4"
          }`}
        >
          <span className="font-medium">Strength:</span> Candidate leads with API design before jumping to infrastructure
        </div>
      </div>
    </section>
  );
}
```

- [ ] **Step 2: Write the sample session link component**

```tsx
// web/src/pages/landing/sample-session.tsx
import { Link } from "react-router-dom";
import { useScrollReveal } from "@/hooks/use-scroll-reveal";
import { useSampleSession, useSampleEvaluation } from "@/api/sample-queries";

export function SampleSessionLink() {
  const { ref, isVisible } = useScrollReveal<HTMLElement>();
  const { data: sessionData } = useSampleSession();
  const { data: evaluation } = useSampleEvaluation();

  return (
    <section ref={ref} className="flex min-h-screen flex-col items-center justify-center gap-8 px-4">
      <div className="max-w-2xl text-center">
        <h2 className="text-3xl font-light text-foreground sm:text-4xl">
          See a real evaluation
        </h2>
      </div>
      <div
        className={`w-full max-w-md rounded-lg border border-border bg-card p-6 transition-all duration-500 ${
          isVisible ? "opacity-100 translate-y-0" : "opacity-0 translate-y-4"
        }`}
      >
        {sessionData?.session && (
          <div className="space-y-2 text-sm text-muted-foreground">
            <p className="text-foreground font-medium">{sessionData.session.question_title}</p>
            <p>{sessionData.messages?.length ?? 0} turns · {sessionData.session.config_duration_minutes} min</p>
            {evaluation?.scores && (
              <p className="text-primary font-medium">Overall score: {evaluation.scores.overall}/5</p>
            )}
          </div>
        )}
        <Link
          to="/sample"
          className="mt-4 inline-block rounded-md bg-muted px-6 py-2 text-sm font-medium text-foreground transition-colors hover:bg-muted/80"
        >
          View full session →
        </Link>
      </div>
    </section>
  );
}
```

- [ ] **Step 3: Write the credits component**

```tsx
// web/src/pages/landing/credits.tsx
import { useScrollReveal } from "@/hooks/use-scroll-reveal";

const CREDITS = [
  { role: "Interviewer", tech: "Claude Sonnet" },
  { role: "Evaluator", tech: "Claude Opus" },
  { role: "Educator", tech: "Claude Opus" },
  { role: "Coach", tech: "Claude Sonnet" },
  { role: "Speech-to-text", tech: "Whisper" },
  { role: "Text-to-speech", tech: "OpenAI TTS" },
  { role: "Backend", tech: "Go on Google Cloud Run" },
  { role: "Database", tech: "PostgreSQL" },
  { role: "Payments", tech: "Stripe" },
];

export function Credits() {
  const { ref, isVisible } = useScrollReveal<HTMLElement>();

  return (
    <section ref={ref} className="py-24 px-4">
      <div className="mx-auto max-w-sm">
        <h3 className="mb-6 text-xs font-medium uppercase tracking-wider text-muted-foreground text-center">
          Built with
        </h3>
        <div className="space-y-2">
          {CREDITS.map((c, i) => (
            <div
              key={c.role}
              className={`flex justify-between text-sm transition-opacity duration-300 ${
                isVisible ? "opacity-100" : "opacity-0"
              }`}
              style={{ transitionDelay: `${i * 50}ms` }}
            >
              <span className="text-muted-foreground">{c.role}</span>
              <span className="text-foreground">{c.tech}</span>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}
```

- [ ] **Step 4: Write the CTA repeat component**

```tsx
// web/src/pages/landing/cta-repeat.tsx
import { Link } from "react-router-dom";

export function CTARepeat() {
  return (
    <section className="flex flex-col items-center justify-center gap-4 py-24 px-4">
      <Link
        to="/signup"
        className="rounded-md bg-primary px-8 py-3 text-sm font-medium text-primary-foreground transition-colors hover:bg-primary/90"
      >
        Start practicing
      </Link>
      <p className="text-xs text-muted-foreground">No credit card required</p>
    </section>
  );
}
```

- [ ] **Step 5: Wire all sections into landing page index**

```tsx
// web/src/pages/landing/index.tsx
import { Hero } from "./hero";
import { Scoring } from "./scoring";
import { StrengthsGaps } from "./strengths-gaps";
import { Annotations } from "./annotations";
import { DeepDivePreview } from "./deep-dive";
import { Coaching } from "./coaching";
import { VoicePipeline } from "./voice-pipeline";
import { SampleSessionLink } from "./sample-session";
import { Credits } from "./credits";
import { CTARepeat } from "./cta-repeat";

export default function Landing() {
  return (
    <div className="min-h-screen bg-background text-foreground">
      <Hero />
      <Scoring />
      <StrengthsGaps />
      <Annotations />
      <DeepDivePreview />
      <Coaching />
      <VoicePipeline />
      <SampleSessionLink />
      <Credits />
      <CTARepeat />
    </div>
  );
}
```

- [ ] **Step 6: Verify full landing page scrolls correctly**

```bash
cd web && npm run dev
```

Visit landing page, scroll through all sections. Verify animations trigger on scroll.

- [ ] **Step 7: Commit**

```bash
git add web/src/pages/landing/
git commit -m "feat: complete landing page with all 10 sections"
```

---

## Task 14: Session Detail — dataSource Refactor

**Files:**
- Modify: `web/src/pages/session/layout.tsx`
- Modify: `web/src/pages/session/overview.tsx`
- Modify: `web/src/pages/session/transcript.tsx`
- Modify: `web/src/pages/session/deep-dive.tsx`

This task makes the session detail components work with both authenticated API data and public sample data.

- [ ] **Step 1: Update session layout to accept dataSource**

In `web/src/pages/session/layout.tsx`, add a context for the data source:

```tsx
import { createContext, useContext } from "react";

type DataSource = "api" | "sample";

interface SessionDetailContext {
  dataSource: DataSource;
  sessionId: string;
}

const SessionDetailCtx = createContext<SessionDetailContext>({
  dataSource: "api",
  sessionId: "",
});

export function useSessionDetail() {
  return useContext(SessionDetailCtx);
}
```

Then wrap the existing `SessionLayoutInner` to support both modes. For `dataSource: "api"`, behavior is unchanged. For `dataSource: "sample"`, use sample query hooks instead.

Update the default export to pass `dataSource: "api"` through context:

```tsx
export default function SessionLayout() {
  const { id } = useParams<{ id: string }>();
  if (!id) return <Navigate to="/" replace />;
  return (
    <SessionDetailCtx.Provider value={{ dataSource: "api", sessionId: id }}>
      <SessionLayoutInner id={id} />
    </SessionDetailCtx.Provider>
  );
}
```

Export the provider for use by the sample page:

```tsx
export { SessionDetailCtx };
```

- [ ] **Step 2: Update overview.tsx to use context**

In `web/src/pages/session/overview.tsx`, add a check: if `dataSource === "sample"`, call `useSampleEvaluation()` and `useSampleSession()` instead of the authenticated hooks. Extract this into a helper:

```tsx
import { useSessionDetail } from "./layout";
import { useSampleEvaluation, useSampleSession } from "@/api/sample-queries";

// At the top of the component:
const { dataSource, sessionId } = useSessionDetail();
const authSession = useSession(sessionId, { enabled: dataSource === "api" });
const authEval = useEvaluation(sessionId, dataSource === "api");
const sampleSession = useSampleSession();
const sampleEval = useSampleEvaluation();

const session = dataSource === "api" ? authSession.data : sampleSession.data?.session;
const evaluation = dataSource === "api" ? authEval.data : sampleEval.data;
```

- [ ] **Step 3: Update transcript.tsx similarly**

Same pattern: check `dataSource`, use sample hooks for transcript messages and annotations.

```tsx
const { dataSource, sessionId } = useSessionDetail();
const authTranscript = useTranscript(sessionId);
const authEval = useEvaluation(sessionId, dataSource === "api");
const sampleSession = useSampleSession();
const sampleEval = useSampleEvaluation();

const messages = dataSource === "api" ? authTranscript.data : sampleSession.data?.messages;
const annotations = dataSource === "api" ? authEval.data?.annotations : sampleEval.data?.annotations;
```

- [ ] **Step 4: Update deep-dive.tsx similarly**

```tsx
const { dataSource, sessionId } = useSessionDetail();
const authEducator = useEducator(sessionId, dataSource === "api");
const sampleEducator = useSampleEducator();

const educator = dataSource === "api" ? authEducator.data : sampleEducator.data;
```

For sample mode, disable the "Generate" and "Retry" buttons since the data is static.

- [ ] **Step 5: Verify authenticated session detail still works**

Navigate to an existing session detail page while logged in. Verify all tabs render correctly with no regressions.

- [ ] **Step 6: Commit**

```bash
git add web/src/pages/session/
git commit -m "feat: session detail dataSource context for public/sample mode"
```

---

## Task 15: Sample Session Page

**Files:**
- Create: `web/src/pages/sample.tsx`

- [ ] **Step 1: Write the sample page**

```tsx
// web/src/pages/sample.tsx
import { Navigate } from "react-router-dom";
import { SessionDetailCtx } from "@/pages/session/layout";
import SessionLayoutInner from "@/pages/session/layout";

// Re-export the inner layout — but we need the tab nav + outlet structure.
// The simplest approach: render the session layout with sample context.

import { Outlet, NavLink, Routes, Route } from "react-router-dom";
import Overview from "@/pages/session/overview";
import TranscriptPage from "@/pages/session/transcript";
import DeepDive from "@/pages/session/deep-dive";
import { cn } from "@/lib/utils";
import { Link } from "react-router-dom";

function TabLink({ to, children }: { to: string; children: React.ReactNode }) {
  return (
    <NavLink
      to={to}
      end
      className={({ isActive }) =>
        cn(
          "px-4 py-2.5 text-sm transition-colors",
          isActive
            ? "border-b-2 border-primary text-foreground font-medium"
            : "text-muted-foreground hover:text-foreground",
        )
      }
    >
      {children}
    </NavLink>
  );
}

export default function SampleSession() {
  return (
    <SessionDetailCtx.Provider value={{ dataSource: "sample", sessionId: "sample" }}>
      <div className="min-h-screen bg-background text-foreground">
        {/* Minimal header */}
        <header className="border-b border-border">
          <div className="mx-auto flex h-12 max-w-5xl items-center justify-between px-4">
            <Link to="/" className="text-sm font-semibold tracking-wider text-muted-foreground">
              DRILL
            </Link>
            <Link
              to="/signup"
              className="rounded-md bg-primary px-4 py-1.5 text-xs font-medium text-primary-foreground"
            >
              Start practicing
            </Link>
          </div>
        </header>

        <main className="mx-auto max-w-5xl px-4 py-6">
          {/* Tab bar */}
          <div className="border-b border-border -mx-4 px-4">
            <nav className="flex items-end max-w-5xl mx-auto -mb-px">
              <TabLink to="/sample">Overview</TabLink>
              <TabLink to="/sample/transcript">Transcript</TabLink>
              <TabLink to="/sample/deep-dive">Deep Dive</TabLink>
            </nav>
          </div>

          <div className="pt-6">
            <Outlet />
          </div>
        </main>
      </div>
    </SessionDetailCtx.Provider>
  );
}
```

- [ ] **Step 2: Update app.tsx routes for /sample**

In `web/src/app.tsx`, update the `/sample` route to support nested tabs:

```tsx
<Route path="/sample" element={<SampleSession />}>
  <Route index element={<Overview />} />
  <Route path="transcript" element={<TranscriptPage />} />
  <Route path="deep-dive" element={<DeepDive />} />
</Route>
```

- [ ] **Step 3: Verify sample session renders**

Visit `/sample`. Verify:
- Overview tab shows session 27 scores, strengths, gaps
- Transcript tab shows messages with annotations
- Deep Dive tab shows educator content
- "Start practicing" CTA in header links to signup

- [ ] **Step 4: Commit**

```bash
git add web/src/pages/sample.tsx web/src/app.tsx
git commit -m "feat: sample session page at /sample with session 27 data"
```

---

## Task 16: Session Replay — Engine

**Files:**
- Create: `web/src/components/replay/engine.ts`

- [ ] **Step 1: Write the replay engine**

```typescript
// web/src/components/replay/engine.ts
import { useState, useRef, useCallback, useEffect } from "react";
import type { Message } from "@/api/types";

export interface ReplayState {
  isPlaying: boolean;
  currentTime: number;    // seconds from session start
  duration: number;       // total session duration in seconds
  speed: number;          // 1, 1.5, or 2
  visibleMessages: number; // number of messages revealed so far
  activeAnnotationSeqs: number[]; // message seqs with visible annotations
}

interface ReplayOptions {
  messages: Message[];
  sessionStartedAt: string;
  sessionEndedAt: string | null;
  annotationSeqs: number[];  // message seqs that have annotations
}

export function useReplayEngine(options: ReplayOptions | null) {
  const [state, setState] = useState<ReplayState>({
    isPlaying: false,
    currentTime: 0,
    duration: 0,
    speed: 1,
    visibleMessages: 0,
    activeAnnotationSeqs: [],
  });

  const animRef = useRef<number>(0);
  const lastTickRef = useRef<number>(0);

  // Compute message timestamps as offsets from session start
  const messageOffsets = useRef<number[]>([]);

  useEffect(() => {
    if (!options) return;
    const start = new Date(options.sessionStartedAt).getTime();
    const end = options.sessionEndedAt
      ? new Date(options.sessionEndedAt).getTime()
      : start + 30 * 60 * 1000; // fallback: 30 min

    messageOffsets.current = options.messages.map(
      (m) => (new Date(m.created_at).getTime() - start) / 1000,
    );

    setState((s) => ({ ...s, duration: (end - start) / 1000 }));
  }, [options]);

  const tick = useCallback(() => {
    const now = performance.now();
    const dt = (now - lastTickRef.current) / 1000;
    lastTickRef.current = now;

    setState((prev) => {
      if (!prev.isPlaying) return prev;

      const newTime = prev.currentTime + dt * prev.speed;
      if (newTime >= prev.duration) {
        return { ...prev, isPlaying: false, currentTime: prev.duration };
      }

      // Count visible messages
      const visible = messageOffsets.current.filter((t) => t <= newTime).length;

      // Active annotation seqs
      const activeAnns = options?.annotationSeqs.filter((seq) => {
        const idx = options.messages.findIndex((m) => m.seq === seq);
        return idx >= 0 && idx < visible;
      }) ?? [];

      return {
        ...prev,
        currentTime: newTime,
        visibleMessages: visible,
        activeAnnotationSeqs: activeAnns,
      };
    });

    animRef.current = requestAnimationFrame(tick);
  }, [options]);

  const play = useCallback(() => {
    lastTickRef.current = performance.now();
    setState((s) => ({ ...s, isPlaying: true }));
    animRef.current = requestAnimationFrame(tick);
  }, [tick]);

  const pause = useCallback(() => {
    cancelAnimationFrame(animRef.current);
    setState((s) => ({ ...s, isPlaying: false }));
  }, []);

  const seek = useCallback((time: number) => {
    setState((prev) => {
      const clamped = Math.max(0, Math.min(time, prev.duration));
      const visible = messageOffsets.current.filter((t) => t <= clamped).length;
      const activeAnns = options?.annotationSeqs.filter((seq) => {
        const idx = options.messages.findIndex((m) => m.seq === seq);
        return idx >= 0 && idx < visible;
      }) ?? [];

      return {
        ...prev,
        currentTime: clamped,
        visibleMessages: visible,
        activeAnnotationSeqs: activeAnns,
      };
    });
  }, [options]);

  const setSpeed = useCallback((speed: number) => {
    setState((s) => ({ ...s, speed }));
  }, []);

  // Cleanup
  useEffect(() => {
    return () => cancelAnimationFrame(animRef.current);
  }, []);

  return { state, play, pause, seek, setSpeed };
}
```

- [ ] **Step 2: Commit**

```bash
git add web/src/components/replay/engine.ts
git commit -m "feat: session replay engine with timing, seek, speed control"
```

---

## Task 17: Session Replay — UI Controls

**Files:**
- Create: `web/src/components/replay/controls.tsx`
- Create: `web/src/components/replay/timeline.tsx`
- Modify: `web/src/pages/session/transcript.tsx`

- [ ] **Step 1: Write the timeline scrubber**

```tsx
// web/src/components/replay/timeline.tsx
interface TimelineProps {
  currentTime: number;
  duration: number;
  onSeek: (time: number) => void;
}

export function Timeline({ currentTime, duration, onSeek }: TimelineProps) {
  const pct = duration > 0 ? (currentTime / duration) * 100 : 0;

  function handleClick(e: React.MouseEvent<HTMLDivElement>) {
    const rect = e.currentTarget.getBoundingClientRect();
    const x = (e.clientX - rect.left) / rect.width;
    onSeek(x * duration);
  }

  return (
    <div
      className="h-1.5 w-full cursor-pointer rounded-full bg-muted"
      onClick={handleClick}
    >
      <div
        className="h-1.5 rounded-full bg-primary transition-[width] duration-100"
        style={{ width: `${pct}%` }}
      />
    </div>
  );
}
```

- [ ] **Step 2: Write the replay controls**

```tsx
// web/src/components/replay/controls.tsx
import { Timeline } from "./timeline";
import type { ReplayState } from "./engine";

function formatTime(seconds: number): string {
  const m = Math.floor(seconds / 60);
  const s = Math.floor(seconds % 60);
  return `${m}:${s.toString().padStart(2, "0")}`;
}

interface ReplayControlsProps {
  state: ReplayState;
  onPlay: () => void;
  onPause: () => void;
  onSeek: (time: number) => void;
  onSetSpeed: (speed: number) => void;
}

const SPEEDS = [1, 1.5, 2] as const;

export function ReplayControls({ state, onPlay, onPause, onSeek, onSetSpeed }: ReplayControlsProps) {
  return (
    <div className="flex flex-col gap-2 rounded-lg border border-border bg-card p-3">
      <Timeline currentTime={state.currentTime} duration={state.duration} onSeek={onSeek} />
      <div className="flex items-center justify-between text-xs text-muted-foreground">
        <div className="flex items-center gap-2">
          <button
            className="rounded px-2 py-1 hover:bg-muted"
            onClick={state.isPlaying ? onPause : onPlay}
          >
            {state.isPlaying ? "⏸" : "▶"}
          </button>
          <span>{formatTime(state.currentTime)} / {formatTime(state.duration)}</span>
        </div>
        <div className="flex items-center gap-1">
          {SPEEDS.map((s) => (
            <button
              key={s}
              className={`rounded px-2 py-1 text-xs ${
                state.speed === s ? "bg-muted font-medium text-foreground" : "hover:bg-muted"
              }`}
              onClick={() => onSetSpeed(s)}
            >
              {s}x
            </button>
          ))}
        </div>
      </div>
    </div>
  );
}
```

- [ ] **Step 3: Integrate replay into transcript page**

In `web/src/pages/session/transcript.tsx`, add a "Replay" button at the top. When clicked, it initializes the replay engine and shows the controls. During replay, only `visibleMessages` are shown, and annotations appear as their messages are revealed.

Add at the top of the component:

```tsx
import { useReplayEngine } from "@/components/replay/engine";
import { ReplayControls } from "@/components/replay/controls";

// Inside the component:
const [replayMode, setReplayMode] = useState(false);

const replay = useReplayEngine(
  replayMode && messages && session
    ? {
        messages,
        sessionStartedAt: session.started_at,
        sessionEndedAt: session.ended_at,
        annotationSeqs: (annotations ?? []).map((a) => a.message_seq),
      }
    : null,
);

// Filter messages during replay
const displayMessages = replayMode
  ? messages?.slice(0, replay.state.visibleMessages)
  : messages;

// Filter annotations during replay
const displayAnnotations = replayMode
  ? annotations?.filter((a) => replay.state.activeAnnotationSeqs.includes(a.message_seq))
  : annotations;
```

Add replay button and controls to the JSX:

```tsx
{/* Before the message list */}
{messages && messages.length > 0 && (
  <div className="mb-4">
    {replayMode ? (
      <ReplayControls
        state={replay.state}
        onPlay={replay.play}
        onPause={replay.pause}
        onSeek={replay.seek}
        onSetSpeed={replay.setSpeed}
      />
    ) : (
      <button
        className="rounded-md bg-muted px-4 py-2 text-sm text-foreground hover:bg-muted/80"
        onClick={() => setReplayMode(true)}
      >
        Replay session
      </button>
    )}
  </div>
)}
```

- [ ] **Step 4: Verify replay works**

Navigate to a session's transcript tab (or `/sample/transcript`). Click "Replay session." Verify:
- Messages appear progressively based on timestamps
- Timeline scrubber tracks progress
- Speed controls work (1x, 1.5x, 2x)
- Seeking via timeline click works
- Annotations appear when their messages are revealed

- [ ] **Step 5: Commit**

```bash
git add web/src/components/replay/ web/src/pages/session/transcript.tsx
git commit -m "feat: session replay with timeline, speed controls, progressive reveal"
```

---

## Summary

| Task | Description | Files |
|------|-------------|-------|
| 1 | Extract v0 session 27 → JSON fixtures | `cmd/extract-sample/`, `internal/sample/fixtures/` |
| 2 | Sample data API endpoints | `internal/sample/handler.go`, `internal/handler/routes.go` |
| 3 | OG meta tag injection | `internal/handler/routes.go` |
| 4 | Scroll animation hook | `web/src/hooks/use-scroll-reveal.ts` |
| 5 | Sample data query hooks | `web/src/api/sample-queries.ts` |
| 6 | Routing changes | `web/src/app.tsx`, `web/src/hooks/use-auth.ts`, `web/src/layouts/app-layout.tsx` |
| 7 | Hero section | `web/src/pages/landing/hero.tsx`, `web/src/pages/landing/index.tsx` |
| 8 | Scoring section | `web/src/pages/landing/scoring.tsx` |
| 9 | Strengths/gaps section | `web/src/pages/landing/strengths-gaps.tsx` |
| 10 | Annotations section | `web/src/pages/landing/annotations.tsx` |
| 11 | Deep dive section | `web/src/pages/landing/deep-dive.tsx` |
| 12 | Coaching section | `web/src/pages/landing/coaching.tsx` |
| 13 | Voice pipeline + credits + CTA | `web/src/pages/landing/voice-pipeline.tsx`, `credits.tsx`, `sample-session.tsx`, `cta-repeat.tsx` |
| 14 | Session detail dataSource refactor | `web/src/pages/session/*.tsx` |
| 15 | Sample session page | `web/src/pages/sample.tsx` |
| 16 | Replay engine | `web/src/components/replay/engine.ts` |
| 17 | Replay UI + integration | `web/src/components/replay/controls.tsx`, `timeline.tsx`, `transcript.tsx` |
