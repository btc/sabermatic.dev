package jobs_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/ai"
	"github.com/btc/drill/internal/backend"
	"github.com/btc/drill/internal/config"
	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/jobs"
	"github.com/btc/drill/internal/testutil"
)

// ---------------- coach test helpers ----------------

// seedCoachData creates a user with 3 reviewed sessions, each with evaluations
// and messages. Returns the user ID and the session IDs.
type coachSeedResult struct {
	UserID     uuid.UUID
	SessionIDs []uuid.UUID
}

func seedCoachData(t *testing.T, ctx context.Context, b *backend.Backend) coachSeedResult {
	t.Helper()
	pool := b.Pool()
	q := db.New(pool)

	userID := testutil.Signup(t, b, "Coach Test User")

	var sessionIDs []uuid.UUID
	for i := 0; i < 3; i++ {
		questionID := seedQuestion(t, ctx, pool)

		session, err := q.CreateSession(ctx, db.CreateSessionParams{
			UserID:                userID,
			QuestionID:            questionID,
			ConfigDurationMinutes: 45,
			ConfigTtsEnabled:      false,
		})
		require.NoError(t, err)

		// Mark session as reviewed (the coach worker expects reviewed sessions).
		err = q.UpdateSessionStatus(ctx, db.UpdateSessionStatusParams{
			ID:        session.ID,
			Status:    "reviewed",
			TurnCount: 4,
		})
		require.NoError(t, err)

		// Insert messages.
		msgs := []struct {
			role    string
			content string
		}{
			{"interviewer", "Welcome! Let's design a system."},
			{"candidate", "Sure, let me start with requirements."},
			{"interviewer", "Good. What about scalability?"},
			{"candidate", "I'd use sharding and caching."},
		}
		for j, m := range msgs {
			_, err := q.InsertMessage(ctx, db.InsertMessageParams{
				ID:        uuid.New(),
				SessionID: session.ID,
				Seq:       int32(j + 1),
				Role:      m.role,
				Content:   m.content,
			})
			require.NoError(t, err)
		}

		// Insert evaluation for this session.
		_, err = q.InsertEvaluation(ctx, db.InsertEvaluationParams{
			SessionID:          session.ID,
			ScoreRequirements:  int32(2 + i),
			ScoreArchitecture:  int32(3),
			ScoreDeepDive:      int32(2),
			ScoreScalability:   int32(2 + i),
			ScoreCommunication: int32(4),
			ScoreOverall:       int32(3),
			Strengths:          []byte(`["Good communication"]`),
			Gaps:               []byte(`["Missing deep dive"]`),
			Advice:             "Focus on going deeper.",
		})
		require.NoError(t, err)

		sessionIDs = append(sessionIDs, session.ID)
	}

	return coachSeedResult{
		UserID:     userID,
		SessionIDs: sessionIDs,
	}
}

// newFakeCoachServer returns an httptest.Server that mimics the Anthropic messages
// endpoint, returning a valid tool_use response with coach analysis fields.
func newFakeCoachServer(t *testing.T) *httptest.Server {
	t.Helper()

	coachInput := map[string]any{
		"narrative":            "The candidate shows strong communication but consistently struggles with deep dives. Focus on going deeper into specific components.",
		"weakest_dimension":    "deep_dive",
		"improving_dimensions": []string{"requirements", "scalability"},
		"topic_gaps":           []string{"load-balancing", "message-queues"},
		"generated_question": map[string]any{
			"title":      "Design a Message Queue",
			"prompt":     "Design a distributed message queue like Kafka or RabbitMQ.",
			"difficulty": "hard",
			"tags":       []string{"message-queues", "distributed-systems"},
		},
	}
	inputJSON, err := json.Marshal(coachInput)
	require.NoError(t, err)

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{
			"id": "msg_coach", "type": "message", "role": "assistant",
			"content": [{"type": "tool_use", "id": "toolu_coach", "name": "submit_analysis", "input": %s}],
			"model": "claude-sonnet-4-20250514", "stop_reason": "tool_use",
			"usage": {"input_tokens": 800, "output_tokens": 400}
		}`, inputJSON)
	}))
}

// newFakeCoachServerNoQuestion returns a fake server that returns coach analysis
// without a generated question.
func newFakeCoachServerNoQuestion(t *testing.T) *httptest.Server {
	t.Helper()

	coachInput := map[string]any{
		"narrative":            "The candidate is improving across all dimensions.",
		"weakest_dimension":    "scalability",
		"improving_dimensions": []string{"communication"},
		"topic_gaps":           []string{},
		"generated_question":   nil,
	}
	inputJSON, err := json.Marshal(coachInput)
	require.NoError(t, err)

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{
			"id": "msg_coach2", "type": "message", "role": "assistant",
			"content": [{"type": "tool_use", "id": "toolu_coach2", "name": "submit_analysis", "input": %s}],
			"model": "claude-sonnet-4-20250514", "stop_reason": "tool_use",
			"usage": {"input_tokens": 600, "output_tokens": 300}
		}`, inputJSON)
	}))
}

func newCoachWorker(t *testing.T, pool *pgxpool.Pool, srvURL string) *jobs.RunCoachAnalysisWorker {
	t.Helper()
	return &jobs.RunCoachAnalysisWorker{
		Pool: pool,
		LLM:  ai.NewTestClient(srvURL, pool),
		Cfg: &config.LLM{
			CoachModel:     "claude-sonnet-4-20250514",
			CoachMaxTokens: 4096,
		},
	}
}

// ---------------- tests ----------------

func TestRunCoachAnalysisWorker_HappyPath(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	b := testutil.NewTestBackend(t)
	pool := b.Pool()
	srv := newFakeCoachServer(t)
	t.Cleanup(srv.Close)

	seed := seedCoachData(t, ctx, b)
	worker := newCoachWorker(t, pool, srv.URL)

	// Run the worker directly.
	err := worker.Work(ctx, &river.Job[jobs.RunCoachAnalysisArgs]{
		Args: jobs.RunCoachAnalysisArgs{UserID: seed.UserID},
	})
	require.NoError(t, err)

	// Assert: coach_analyses row exists with expected fields.
	q := db.New(pool)
	analysis, err := q.GetLatestCoachAnalysis(ctx, seed.UserID)
	require.NoError(t, err)
	require.Contains(t, analysis.Narrative, "strong communication")
	require.Equal(t, "deep_dive", analysis.WeakestDimension.String)
	require.True(t, analysis.WeakestDimension.Valid)
	require.Equal(t, []string{"requirements", "scalability"}, analysis.ImprovingDimensions)
	require.Equal(t, []string{"load-balancing", "message-queues"}, analysis.TopicGaps)
	require.Len(t, analysis.SessionsAnalyzed, 3)

	// Assert: generated question was inserted.
	require.True(t, analysis.SuggestedQuestionID.Valid)
	var genQuestion db.Question
	err = pool.QueryRow(ctx,
		`SELECT id, title, prompt, difficulty, tags, source, coach_rationale
		 FROM questions WHERE id = $1`,
		analysis.SuggestedQuestionID.Bytes,
	).Scan(
		&genQuestion.ID, &genQuestion.Title, &genQuestion.Prompt,
		&genQuestion.Difficulty, &genQuestion.Tags, &genQuestion.Source,
		&genQuestion.CoachRationale,
	)
	require.NoError(t, err)
	require.Equal(t, "Design a Message Queue", genQuestion.Title)
	require.Equal(t, "hard", genQuestion.Difficulty)
	require.Equal(t, "coach_generated", genQuestion.Source)
	require.Equal(t, []string{"message-queues", "distributed-systems"}, genQuestion.Tags)
	require.NotEmpty(t, genQuestion.Prompt, "generated question prompt should be populated")
	require.True(t, genQuestion.CoachRationale.Valid, "generated question coach_rationale should be valid")
	require.NotEmpty(t, genQuestion.CoachRationale.String, "generated question coach_rationale should be populated")

	// Assert: LLM call logged with role=coach.
	var callCount int
	err = pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM llm_calls WHERE user_id = $1 AND role = 'coach'`,
		seed.UserID,
	).Scan(&callCount)
	require.NoError(t, err)
	require.Equal(t, 1, callCount, "expected exactly one coach LLM call")
}

func TestRunCoachAnalysisWorker_NoGeneratedQuestion(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	b := testutil.NewTestBackend(t)
	pool := b.Pool()
	srv := newFakeCoachServerNoQuestion(t)
	t.Cleanup(srv.Close)

	seed := seedCoachData(t, ctx, b)
	worker := newCoachWorker(t, pool, srv.URL)

	err := worker.Work(ctx, &river.Job[jobs.RunCoachAnalysisArgs]{
		Args: jobs.RunCoachAnalysisArgs{UserID: seed.UserID},
	})
	require.NoError(t, err)

	q := db.New(pool)
	analysis, err := q.GetLatestCoachAnalysis(ctx, seed.UserID)
	require.NoError(t, err)
	require.Contains(t, analysis.Narrative, "improving across all dimensions")
	require.Equal(t, "scalability", analysis.WeakestDimension.String)
	require.False(t, analysis.SuggestedQuestionID.Valid, "expected no suggested question")
}

func TestRunCoachAnalysisWorker_NoReviewedSessions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	b := testutil.NewTestBackend(t)
	pool := b.Pool()
	srv := newFakeCoachServer(t)
	t.Cleanup(srv.Close)

	// Create a user with no sessions at all.
	userID := testutil.Signup(t, b, "Empty User")

	worker := newCoachWorker(t, pool, srv.URL)

	// Work should return nil (skip, no reviewed sessions).
	err := worker.Work(ctx, &river.Job[jobs.RunCoachAnalysisArgs]{
		Args: jobs.RunCoachAnalysisArgs{UserID: userID},
	})
	require.NoError(t, err)

	// Assert: no coach analysis row was created.
	var count int
	err = pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM coach_analyses WHERE user_id = $1`,
		userID,
	).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 0, count)
}

func TestRunCoachAnalysisWorker_EmptyResponse(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	b := testutil.NewTestBackend(t)
	pool := b.Pool()

	// Fake server returns a text content block only — no tool_use block.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"id": "msg_empty", "type": "message", "role": "assistant",
			"content": [{"type": "text", "text": "I cannot analyze this."}],
			"model": "claude-sonnet-4-20250514", "stop_reason": "end_turn",
			"usage": {"input_tokens": 100, "output_tokens": 20}
		}`)
	}))
	t.Cleanup(srv.Close)

	seed := seedCoachData(t, ctx, b)
	worker := newCoachWorker(t, pool, srv.URL)

	// Work should return an error because there is no tool_use block.
	err := worker.Work(ctx, &river.Job[jobs.RunCoachAnalysisArgs]{
		Args: jobs.RunCoachAnalysisArgs{UserID: seed.UserID},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "call coach LLM")
}

func TestRunCoachAnalysisWorker_MalformedResponse(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	b := testutil.NewTestBackend(t)
	pool := b.Pool()

	// Fake server returns tool_use with semantically invalid content (empty narrative).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"id": "msg_bad", "type": "message", "role": "assistant",
			"content": [{"type": "tool_use", "id": "toolu_bad", "name": "submit_analysis", "input": {"narrative": ""}}],
			"model": "claude-sonnet-4-20250514", "stop_reason": "tool_use",
			"usage": {"input_tokens": 100, "output_tokens": 50}
		}`)
	}))
	t.Cleanup(srv.Close)

	seed := seedCoachData(t, ctx, b)
	worker := newCoachWorker(t, pool, srv.URL)

	// Work should return an error because narrative is empty.
	err := worker.Work(ctx, &river.Job[jobs.RunCoachAnalysisArgs]{
		Args: jobs.RunCoachAnalysisArgs{UserID: seed.UserID},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "parse coach")
}
