package jobs_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/btc/drill/internal/ai"
	"github.com/btc/drill/internal/config"
	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/jobs"
)

// ---------------- test helpers ----------------

// startTestPostgres starts a Postgres 16 container, runs app migrations, and
// returns a connected pool. Container and pool are cleaned up on test end.
func startTestPostgres(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()

	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("drill_test"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second),
		),
	)
	require.NoError(t, err)
	t.Cleanup(func() { pgContainer.Terminate(ctx) })

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	// Run app migrations.
	d, err := iofs.New(os.DirFS("../../sql/migrations"), ".")
	require.NoError(t, err)

	trimmed := strings.TrimPrefix(connStr, "postgresql://")
	trimmed = strings.TrimPrefix(trimmed, "postgres://")
	pgxURL := "pgx5://" + trimmed
	m, err := migrate.NewWithSourceInstance("iofs", d, pgxURL)
	require.NoError(t, err)
	err = m.Up()
	if err != nil && err != migrate.ErrNoChange {
		t.Fatalf("migrate up: %v", err)
	}

	pool, err := pgxpool.New(ctx, connStr)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

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

// newFakeEvalServer returns an httptest.Server that mimics the Anthropic messages
// endpoint, returning a valid tool_use response with evaluation scores and annotations.
func newFakeEvalServer(t *testing.T) *httptest.Server {
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
		"strengths":   []string{"Good requirements gathering"},
		"gaps":        []string{"Missing cache strategy"},
		"advice":      "Focus on deep dive. Ask: what if this component failed?",
		"annotations": []any{
			map[string]any{"message_seq": 2, "type": "strength", "content": "Structured approach."},
			map[string]any{"message_seq": 4, "type": "gap", "content": "Hand-waved caching."},
		},
	}
	inputJSON, err := json.Marshal(evalInput)
	require.NoError(t, err)

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{
			"id": "msg_eval", "type": "message", "role": "assistant",
			"content": [{"type": "tool_use", "id": "toolu_eval", "name": "submit_evaluation", "input": %s}],
			"model": "claude-opus-4-20250514", "stop_reason": "tool_use",
			"usage": {"input_tokens": 500, "output_tokens": 200}
		}`, inputJSON)
	}))
}

// newEvalWorker constructs an EvaluateSessionWorker for testing.
func newEvalWorker(pool *pgxpool.Pool, srvURL string) *jobs.EvaluateSessionWorker {
	return &jobs.EvaluateSessionWorker{
		Pool: pool,
		LLM:  ai.NewTestClient(srvURL, pool),
		Cfg: &config.LLM{
			EvaluatorModel:     "claude-opus-4-20250514",
			EvaluatorMaxTokens: 4096,
		},
		// Jobs is nil — email enqueue is skipped (nil guard in worker).
	}
}

// ---------------- tests ----------------

func TestEvaluateSessionWorker_HappyPath(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	pool := startTestPostgres(t)
	srv := newFakeEvalServer(t)
	t.Cleanup(srv.Close)

	seed := seedSessionWithMessages(t, ctx, pool, 4)
	worker := newEvalWorker(pool, srv.URL)

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
}

func TestEvaluateSessionWorker_Idempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	pool := startTestPostgres(t)
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

	worker := newEvalWorker(pool, srv.URL)

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
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	pool := startTestPostgres(t)
	srv := newFakeEvalServer(t)
	t.Cleanup(srv.Close)

	// Seed a session with zero messages.
	seed := seedSessionWithMessages(t, ctx, pool, 0)
	worker := newEvalWorker(pool, srv.URL)

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
