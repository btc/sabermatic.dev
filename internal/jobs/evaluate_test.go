package jobs_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/ai"
	"github.com/btc/drill/internal/ai/aitest"
	"github.com/btc/drill/internal/config"
	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/jobs"
)

// ---------------- test helpers ----------------

// newTestPool creates a database on the shared container, connects a pool,
// and runs River migrations. Returns the pool for direct use by job tests.
func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	connStr := pg.NewDatabase(t) // app migrations already run by NewDatabase

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, connStr)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	// Run River migrations (River needs its own internal tables).
	migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	require.NoError(t, err)
	_, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil)
	require.NoError(t, err)

	return pool
}

// seedQuestion inserts a question directly via SQL (no sqlc CreateQuestion query exists).
func seedQuestion(t *testing.T, ctx context.Context, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO questions (title, prompt, difficulty, tags, source)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		"Design a URL Shortener",
		"Design a URL shortening service like bit.ly.",
		"medium",
		[]string{"system-design", "distributed-systems"},
		"seed",
	).Scan(&id)
	require.NoError(t, err)
	return id
}

// seedSession creates a user, question, and session, then inserts messages.
// Returns the session ID and user ID.
type seedResult struct {
	UserID     uuid.UUID
	QuestionID uuid.UUID
	SessionID  uuid.UUID
}

func seedSessionWithMessages(t *testing.T, ctx context.Context, pool *pgxpool.Pool, numMessages int) seedResult {
	t.Helper()
	q := db.New(pool)

	user, err := q.CreateUser(ctx, db.CreateUserParams{
		Email:        fmt.Sprintf("test-%s@example.com", uuid.New().String()[:8]),
		PasswordHash: pgtype.Text{String: "$2a$04$fakehash", Valid: true},
		DisplayName:  "Test User",
	})
	require.NoError(t, err)

	questionID := seedQuestion(t, ctx, pool)

	session, err := q.CreateSession(ctx, db.CreateSessionParams{
		UserID:                user.ID,
		QuestionID:            questionID,
		ConfigDurationMinutes: 45,
		ConfigTtsEnabled:      false,
	})
	require.NoError(t, err)

	// Mark session as completed (the worker expects completed sessions).
	err = q.UpdateSessionStatus(ctx, db.UpdateSessionStatusParams{
		ID:        session.ID,
		Status:    "completed",
		TurnCount: int32(numMessages),
	})
	require.NoError(t, err)

	// Insert messages with alternating roles.
	messages := []struct {
		role    string
		content string
	}{
		{"interviewer", "Welcome! Let's design a URL shortener. What questions do you have?"},
		{"candidate", "I'd like to understand the scale. How many URLs per day?"},
		{"interviewer", "Great question. Let's say 100M new URLs per day, 10:1 read:write ratio."},
		{"candidate", "I'll use a hash-based approach with Base62 encoding. For storage, I'd use a distributed key-value store with caching."},
	}

	for i := 0; i < numMessages && i < len(messages); i++ {
		_, err := q.InsertMessage(ctx, db.InsertMessageParams{
			ID:        uuid.New(),
			SessionID: session.ID,
			Seq:       int32(i + 1),
			Role:      messages[i].role,
			Content:   messages[i].content,
		})
		require.NoError(t, err)
	}

	return seedResult{
		UserID:     user.ID,
		QuestionID: questionID,
		SessionID:  session.ID,
	}
}

// newFakeEvalServer returns a fake Anthropic server that streams a valid
// structured-output evaluation (preceded by a thinking block, as
// claude-opus-5-5 sends).
func newFakeEvalServer(t *testing.T) *aitest.Server {
	t.Helper()

	evalInput := map[string]any{
		"scores": map[string]any{
			"requirements":  3,
			"architecture":  4,
			"deep_dive":     2,
			"scalability":   3,
			"communication": 4,
			"overall":       3,
		},
		"strengths": []string{"Good requirements gathering"},
		"gaps":      []string{"Missing cache strategy"},
		"advice":    "Focus on deep dive. Ask: what if this component failed?",
		"annotations": []any{
			map[string]any{"message_seq": 2, "type": "strength", "content": "Structured approach."},
			map[string]any{"message_seq": 4, "type": "gap", "content": "Hand-waved caching."},
		},
	}
	outputJSON, err := json.Marshal(evalInput)
	require.NoError(t, err)

	return aitest.NewServer(t, aitest.Response{
		Model:        "claude-opus-5-5",
		Blocks:       []aitest.Block{aitest.ThinkingBlock(), aitest.TextBlock(string(outputJSON))},
		InputTokens:  500,
		OutputTokens: 200,
	})
}

// newEvalWorker constructs an EvaluateSessionWorker with a real River client for testing.
func newEvalWorker(t *testing.T, pool *pgxpool.Pool, srvURL string) *jobs.EvaluateSessionWorker {
	t.Helper()
	worker := &jobs.EvaluateSessionWorker{
		Pool: pool,
		LLM:  ai.NewTestClient(srvURL, pool),
		Cfg: &config.LLM{
			EvaluatorModel:     "claude-opus-5-5",
			EvaluatorMaxTokens: 32000,
			EvaluatorEffort:    "high",
		},
	}

	// Create a River client so InsertTx works (client not started — no workers run).
	riverClient, err := river.NewClient(riverpgxv5.New(pool), &river.Config{})
	require.NoError(t, err)
	worker.Jobs = riverClient
	return worker
}

// ---------------- tests ----------------

func TestEvaluateSessionWorker_HappyPath(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := newTestPool(t)
	srv := newFakeEvalServer(t)
	t.Cleanup(srv.Close)

	seed := seedSessionWithMessages(t, ctx, pool, 4)
	worker := newEvalWorker(t, pool, srv.URL)

	// Run the worker directly.
	err := worker.Work(ctx, &river.Job[jobs.EvaluateSessionArgs]{
		Args: jobs.EvaluateSessionArgs{SessionID: seed.SessionID},
	})
	require.NoError(t, err)

	q := db.New(pool)

	// Assert: evaluation row exists with correct scores.
	eval, err := q.GetEvaluationBySession(ctx, seed.SessionID)
	require.NoError(t, err)
	require.Equal(t, int32(3), eval.ScoreRequirements)
	require.Equal(t, int32(4), eval.ScoreArchitecture)
	require.Equal(t, int32(2), eval.ScoreDeepDive)
	require.Equal(t, int32(3), eval.ScoreScalability)
	require.Equal(t, int32(4), eval.ScoreCommunication)
	require.Equal(t, int32(3), eval.ScoreOverall)
	require.Equal(t, "Focus on deep dive. Ask: what if this component failed?", eval.Advice)

	// Assert: strengths and gaps persisted.
	var strengths []string
	require.NoError(t, json.Unmarshal(eval.Strengths, &strengths))
	require.Equal(t, []string{"Good requirements gathering"}, strengths)

	var gaps []string
	require.NoError(t, json.Unmarshal(eval.Gaps, &gaps))
	require.Equal(t, []string{"Missing cache strategy"}, gaps)

	// Assert: annotations exist.
	annotations, err := q.GetAnnotationsByEvaluation(ctx, eval.ID)
	require.NoError(t, err)
	require.Len(t, annotations, 2)

	// Annotations are ordered by message_seq, annotation_type.
	require.Equal(t, int32(2), annotations[0].MessageSeq)
	require.Equal(t, "strength", annotations[0].AnnotationType)
	require.Equal(t, "Structured approach.", annotations[0].Content)

	require.Equal(t, int32(4), annotations[1].MessageSeq)
	require.Equal(t, "gap", annotations[1].AnnotationType)
	require.Equal(t, "Hand-waved caching.", annotations[1].Content)

	// Assert: session status = "reviewed".
	session, err := q.GetSession(ctx, seed.SessionID)
	require.NoError(t, err)
	require.Equal(t, "reviewed", session.Status)

	// Assert: LLM call logged.
	var callCount int
	err = pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM llm_calls WHERE session_id = $1 AND role = 'evaluator'`,
		seed.SessionID,
	).Scan(&callCount)
	require.NoError(t, err)
	require.Equal(t, 1, callCount, "expected exactly one evaluator LLM call")

	// Assert: the request used structured outputs, not forced tool use.
	reqs := srv.Requests()
	require.Len(t, reqs, 1)
	require.NotContains(t, reqs[0], "tool_choice")
	oc := reqs[0]["output_config"].(map[string]any)
	require.Equal(t, "high", oc["effort"])
	require.Contains(t, oc, "format")
}

func TestEvaluateSessionWorker_RefusalCancelsAndMarksFailed(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := newTestPool(t)
	srv := aitest.NewServer(t, aitest.Response{
		Model:           "claude-opus-5-5",
		StopReason:      "refusal",
		RefusalCategory: "bio",
	})

	seed := seedSessionWithMessages(t, ctx, pool, 4)
	worker := newEvalWorker(t, pool, srv.URL)

	err := worker.Work(ctx, &river.Job[jobs.EvaluateSessionArgs]{
		Args: jobs.EvaluateSessionArgs{SessionID: seed.SessionID},
	})
	require.Error(t, err)
	var cancelErr *rivertype.JobCancelError
	require.ErrorAs(t, err, &cancelErr, "refusal must cancel the job, not retry it")
	require.True(t, ai.IsRefusal(err))

	session, err := db.New(pool).GetSession(ctx, seed.SessionID)
	require.NoError(t, err)
	require.Equal(t, "evaluation_failed", session.Status)
}

func TestEvaluateSessionWorker_Idempotent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := newTestPool(t)
	srv := newFakeEvalServer(t)
	t.Cleanup(srv.Close)

	seed := seedSessionWithMessages(t, ctx, pool, 4)

	// Pre-insert an evaluation to simulate a previous run.
	q := db.New(pool)
	_, err := q.InsertEvaluation(ctx, db.InsertEvaluationParams{
		SessionID:          seed.SessionID,
		ScoreRequirements:  3,
		ScoreArchitecture:  4,
		ScoreDeepDive:      2,
		ScoreScalability:   3,
		ScoreCommunication: 4,
		ScoreOverall:       3,
		Strengths:          []byte(`["Already evaluated"]`),
		Gaps:               []byte(`[]`),
		Advice:             "Previous evaluation.",
	})
	require.NoError(t, err)

	worker := newEvalWorker(t, pool, srv.URL)

	// Run the worker — should return nil without creating a duplicate.
	err = worker.Work(ctx, &river.Job[jobs.EvaluateSessionArgs]{
		Args: jobs.EvaluateSessionArgs{SessionID: seed.SessionID},
	})
	require.NoError(t, err)

	// Assert: still only one evaluation row.
	var evalCount int
	err = pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM evaluations WHERE session_id = $1`,
		seed.SessionID,
	).Scan(&evalCount)
	require.NoError(t, err)
	require.Equal(t, 1, evalCount, "expected exactly one evaluation (idempotent)")
}

func TestEvaluateSessionWorker_EmptyTranscript(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := newTestPool(t)
	srv := newFakeEvalServer(t)
	t.Cleanup(srv.Close)

	// Seed a session with zero messages.
	seed := seedSessionWithMessages(t, ctx, pool, 0)
	worker := newEvalWorker(t, pool, srv.URL)

	// Run the worker.
	err := worker.Work(ctx, &river.Job[jobs.EvaluateSessionArgs]{
		Args: jobs.EvaluateSessionArgs{SessionID: seed.SessionID},
	})
	require.NoError(t, err)

	// Assert: session status = "evaluation_failed".
	q := db.New(pool)
	session, err := q.GetSession(ctx, seed.SessionID)
	require.NoError(t, err)
	require.Equal(t, "evaluation_failed", session.Status)

	// Assert: no evaluation row was created.
	var evalCount int
	err = pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM evaluations WHERE session_id = $1`,
		seed.SessionID,
	).Scan(&evalCount)
	require.NoError(t, err)
	require.Equal(t, 0, evalCount)
}
