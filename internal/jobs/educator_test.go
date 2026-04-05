package jobs_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
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

// ---------------- educator test helpers ----------------

// seedEducatorData creates a user, question, reviewed session with messages and
// evaluation. Returns the session ID.
type educatorSeedResult struct {
	UserID    uuid.UUID
	SessionID uuid.UUID
}

func seedEducatorData(t *testing.T, ctx context.Context, b *backend.Backend) educatorSeedResult {
	t.Helper()
	pool := b.Pool()
	q := db.New(pool)

	userID := testutil.Signup(t, b, "Educator Test User")

	questionID := seedQuestion(t, ctx, pool)

	session, err := q.CreateSession(ctx, db.CreateSessionParams{
		UserID:                userID,
		QuestionID:            questionID,
		ConfigDurationMinutes: 45,
		ConfigTtsEnabled:      false,
	})
	require.NoError(t, err)

	// Mark session as reviewed (educator worker requires reviewed status).
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
		{"interviewer", "Welcome! Let's design a URL shortener."},
		{"candidate", "I'd start by understanding the requirements."},
		{"interviewer", "Great. What about the data model?"},
		{"candidate", "I'd use a hash-based approach with Base62 encoding."},
	}
	for i, m := range msgs {
		_, err := q.InsertMessage(ctx, db.InsertMessageParams{
			ID:        uuid.New(),
			SessionID: session.ID,
			Seq:       int32(i + 1),
			Role:      m.role,
			Content:   m.content,
		})
		require.NoError(t, err)
	}

	// Insert evaluation.
	_, err = q.InsertEvaluation(ctx, db.InsertEvaluationParams{
		SessionID:          session.ID,
		ScoreRequirements:  3,
		ScoreArchitecture:  4,
		ScoreDeepDive:      2,
		ScoreScalability:   3,
		ScoreCommunication: 4,
		ScoreOverall:       3,
		Strengths:          []byte(`["Good requirements gathering"]`),
		Gaps:               []byte(`["Missing cache strategy"]`),
		Advice:             "Focus on deep dive.",
	})
	require.NoError(t, err)

	return educatorSeedResult{
		UserID:    userID,
		SessionID: session.ID,
	}
}

// newFakeEducatorServer returns an httptest.Server that mimics the Anthropic messages
// endpoint, returning a valid tool_use response with educator content fields.
func newFakeEducatorServer(t *testing.T) *httptest.Server {
	t.Helper()

	educatorInput := map[string]any{
		"model_answer":   "## Model Answer\n\nA strong URL shortener design starts with...\n\n### Data Model\n\n| Column | Type |\n|--------|------|\n| short_code | VARCHAR(7) |\n| original_url | TEXT |\n\n### Caching Strategy\n\nUse write-through caching with Redis.",
		"gap_deep_dives": "## Cache Invalidation\n\nThe candidate missed cache invalidation strategies. Three main approaches:\n\n1. **Write-through** (used by DynamoDB)\n2. **Write-behind** (common in ORMs)\n3. **TTL-based expiration** (Redis default)\n\nFor this problem, write-through is best because...",
	}
	inputJSON, err := json.Marshal(educatorInput)
	require.NoError(t, err)

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{
			"id": "msg_edu", "type": "message", "role": "assistant",
			"content": [{"type": "tool_use", "id": "toolu_edu", "name": "submit_education", "input": %s}],
			"model": "claude-opus-4-20250514", "stop_reason": "tool_use",
			"usage": {"input_tokens": 1000, "output_tokens": 500}
		}`, inputJSON)
	}))
}

func newEducatorWorker(t *testing.T, pool *pgxpool.Pool, srvURL string) *jobs.GenerateEducatorContentWorker {
	t.Helper()
	return &jobs.GenerateEducatorContentWorker{
		Pool: pool,
		LLM:  ai.NewTestClient(srvURL, pool),
		Cfg: &config.LLM{
			EducatorModel:     "claude-opus-4-20250514",
			EducatorMaxTokens: 8192,
		},
	}
}

// ---------------- tests ----------------

func TestGenerateEducatorContentWorker_HappyPath(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	b := testutil.NewTestBackend(t)
	pool := b.Pool()
	srv := newFakeEducatorServer(t)
	t.Cleanup(srv.Close)

	seed := seedEducatorData(t, ctx, b)
	worker := newEducatorWorker(t, pool, srv.URL)

	// Run the worker directly (educator creates the generating row itself).
	err := worker.Work(ctx, &river.Job[jobs.GenerateEducatorContentArgs]{
		Args: jobs.GenerateEducatorContentArgs{SessionID: seed.SessionID},
	})
	require.NoError(t, err)

	// Assert: educator_analyses row exists with completed status.
	q := db.New(pool)
	ea, err := q.GetEducatorAnalysisBySession(ctx, seed.SessionID)
	require.NoError(t, err)
	require.Equal(t, "completed", ea.Status)
	require.True(t, ea.ModelAnswer.Valid)
	require.Contains(t, ea.ModelAnswer.String, "URL shortener")
	require.True(t, ea.GapDeepDives.Valid)
	require.Contains(t, ea.GapDeepDives.String, "Cache Invalidation")

	// Assert: LLM call logged with role=educator.
	var callCount int
	err = pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM llm_calls WHERE session_id = $1 AND role = 'educator'`,
		seed.SessionID,
	).Scan(&callCount)
	require.NoError(t, err)
	require.Equal(t, 1, callCount, "expected exactly one educator LLM call")
}

func TestGenerateEducatorContentWorker_PreExistingGeneratingRow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	b := testutil.NewTestBackend(t)
	pool := b.Pool()
	srv := newFakeEducatorServer(t)
	t.Cleanup(srv.Close)

	seed := seedEducatorData(t, ctx, b)

	// Pre-insert a generating row to simulate a retry.
	q := db.New(pool)
	analysisID, err := q.InsertEducatorAnalysis(ctx, seed.SessionID)
	require.NoError(t, err)

	worker := newEducatorWorker(t, pool, srv.URL)

	// Work should succeed and update the pre-existing row.
	err = worker.Work(ctx, &river.Job[jobs.GenerateEducatorContentArgs]{
		Args: jobs.GenerateEducatorContentArgs{SessionID: seed.SessionID},
	})
	require.NoError(t, err)

	// Assert: the same row was updated (not a new one created).
	ea, err := q.GetEducatorAnalysisBySession(ctx, seed.SessionID)
	require.NoError(t, err)
	require.Equal(t, analysisID, ea.ID, "expected same analysis row to be updated")
	require.Equal(t, "completed", ea.Status)
	require.True(t, ea.ModelAnswer.Valid)
}

func TestGenerateEducatorContentWorker_IdempotentCompleted(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	b := testutil.NewTestBackend(t)
	pool := b.Pool()
	srv := newFakeEducatorServer(t)
	t.Cleanup(srv.Close)

	seed := seedEducatorData(t, ctx, b)

	// Pre-insert a completed row.
	q := db.New(pool)
	analysisID, err := q.InsertEducatorAnalysis(ctx, seed.SessionID)
	require.NoError(t, err)
	err = q.UpdateEducatorAnalysisContent(ctx, db.UpdateEducatorAnalysisContentParams{
		ID:           analysisID,
		ModelAnswer:  pgtype.Text{String: "Previous answer", Valid: true},
		GapDeepDives: pgtype.Text{String: "Previous dives", Valid: true},
	})
	require.NoError(t, err)

	worker := newEducatorWorker(t, pool, srv.URL)

	// Work should return nil (skip, already completed).
	err = worker.Work(ctx, &river.Job[jobs.GenerateEducatorContentArgs]{
		Args: jobs.GenerateEducatorContentArgs{SessionID: seed.SessionID},
	})
	require.NoError(t, err)

	// Assert: content was NOT overwritten.
	ea, err := q.GetEducatorAnalysisBySession(ctx, seed.SessionID)
	require.NoError(t, err)
	require.Equal(t, "Previous answer", ea.ModelAnswer.String)
}

func TestGenerateEducatorContentWorker_SessionNotReviewed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	b := testutil.NewTestBackend(t)
	pool := b.Pool()
	srv := newFakeEducatorServer(t)
	t.Cleanup(srv.Close)

	// Seed a session that's only "completed", not "reviewed".
	seed := seedSessionWithMessages(t, context.Background(), b, 4)
	worker := newEducatorWorker(t, pool, srv.URL)

	// Work should return nil (skip, not reviewed).
	err := worker.Work(ctx, &river.Job[jobs.GenerateEducatorContentArgs]{
		Args: jobs.GenerateEducatorContentArgs{SessionID: seed.SessionID},
	})
	require.NoError(t, err)

	// Assert: no educator analysis row was created.
	var count int
	err = pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM educator_analyses WHERE session_id = $1`,
		seed.SessionID,
	).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 0, count)
}

func TestGenerateEducatorContentWorker_MalformedResponse(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	b := testutil.NewTestBackend(t)
	pool := b.Pool()

	// Fake server returns a tool_use block with empty model_answer.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"id": "msg_bad", "type": "message", "role": "assistant",
			"content": [{"type": "tool_use", "id": "toolu_bad", "name": "submit_education", "input": {"model_answer": "", "gap_deep_dives": "Some deep dives."}}],
			"model": "claude-opus-4-20250514", "stop_reason": "tool_use",
			"usage": {"input_tokens": 100, "output_tokens": 50}
		}`)
	}))
	t.Cleanup(srv.Close)

	seed := seedEducatorData(t, ctx, b)
	worker := newEducatorWorker(t, pool, srv.URL)

	// Work should return an error because model_answer is empty.
	err := worker.Work(ctx, &river.Job[jobs.GenerateEducatorContentArgs]{
		Args: jobs.GenerateEducatorContentArgs{SessionID: seed.SessionID},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "parse educator")

	// Verify the "generating" row persists (enables retry).
	var status string
	err = pool.QueryRow(ctx, "SELECT status FROM educator_analyses WHERE session_id = $1", seed.SessionID).Scan(&status)
	require.NoError(t, err)
	require.Equal(t, "generating", status)
}

func TestGenerateEducatorContentWorker_EmptyResponse(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	b := testutil.NewTestBackend(t)
	pool := b.Pool()

	// Fake server returns message with no tool_use blocks.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"id": "msg_empty", "type": "message", "role": "assistant",
			"content": [{"type": "text", "text": "I cannot generate content for this."}],
			"model": "claude-opus-4-20250514", "stop_reason": "end_turn",
			"usage": {"input_tokens": 100, "output_tokens": 20}
		}`)
	}))
	t.Cleanup(srv.Close)

	seed := seedEducatorData(t, ctx, b)
	worker := newEducatorWorker(t, pool, srv.URL)

	// Work should return an error because there's no tool_use block.
	err := worker.Work(ctx, &river.Job[jobs.GenerateEducatorContentArgs]{
		Args: jobs.GenerateEducatorContentArgs{SessionID: seed.SessionID},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "call educator LLM")

	// Verify the "generating" row persists (enables retry).
	var status string
	err = pool.QueryRow(ctx, "SELECT status FROM educator_analyses WHERE session_id = $1", seed.SessionID).Scan(&status)
	require.NoError(t, err)
	require.Equal(t, "generating", status)
}
