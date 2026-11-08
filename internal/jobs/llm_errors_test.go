package jobs_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/ai"
	"github.com/btc/drill/internal/ai/aitest"
	"github.com/btc/drill/internal/config"
	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/jobs"
)

// countLLMCalls returns the number of llm_calls rows with the given role.
func countLLMCalls(t *testing.T, ctx context.Context, pool *pgxpool.Pool, role string) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM llm_calls WHERE role = $1`, role).Scan(&n))
	return n
}

// maxTokensServer answers every request with a max_tokens stop after a
// thinking block and a truncated JSON fragment.
func maxTokensServer(t *testing.T, model string) *aitest.Server {
	t.Helper()
	return aitest.NewServer(t, aitest.Response{
		Model:        model,
		Blocks:       []aitest.Block{aitest.ThinkingBlock(), aitest.TextBlock(`{"scores":`)},
		StopReason:   "max_tokens",
		InputTokens:  500,
		OutputTokens: 32000,
	})
}

func TestEvaluateSessionWorker_RefusalLogsCall(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := newTestPool(t)
	srv := aitest.NewServer(t, aitest.Response{
		Model:           "claude-opus-5-5",
		StopReason:      "refusal",
		RefusalCategory: "bio",
		InputTokens:     500,
	})
	seed := seedSessionWithMessages(t, ctx, pool, 4)
	worker := newEvalWorker(t, pool, srv.URL)

	err := worker.Work(ctx, &river.Job[jobs.EvaluateSessionArgs]{
		Args: jobs.EvaluateSessionArgs{SessionID: seed.SessionID},
	})
	require.True(t, ai.IsRefusal(err))
	require.Equal(t, 1, countLLMCalls(t, ctx, pool, "evaluator"),
		"a refused call is billed and must be logged even though the job's tx rolls back")
}

func TestEvaluateSessionWorker_MaxTokensRetriesOnceThenCancels(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := newTestPool(t)
	srv := maxTokensServer(t, "claude-opus-5-5")
	seed := seedSessionWithMessages(t, ctx, pool, 4)
	worker := newEvalWorker(t, pool, srv.URL)
	q := db.New(pool)

	// First attempt: a plain error, so River retries.
	err := worker.Work(ctx, &river.Job[jobs.EvaluateSessionArgs]{
		JobRow: &rivertype.JobRow{Attempt: 1},
		Args:   jobs.EvaluateSessionArgs{SessionID: seed.SessionID},
	})
	require.ErrorIs(t, err, ai.ErrMaxTokens)
	var cancelErr *rivertype.JobCancelError
	require.NotErrorAs(t, err, &cancelErr, "first max_tokens stop should be retried")
	session, err := q.GetSession(ctx, seed.SessionID)
	require.NoError(t, err)
	require.NotEqual(t, "evaluation_failed", session.Status)

	// Second attempt: cancelled and marked failed.
	err = worker.Work(ctx, &river.Job[jobs.EvaluateSessionArgs]{
		JobRow: &rivertype.JobRow{Attempt: 2},
		Args:   jobs.EvaluateSessionArgs{SessionID: seed.SessionID},
	})
	require.ErrorIs(t, err, ai.ErrMaxTokens)
	require.ErrorAs(t, err, &cancelErr, "second max_tokens stop must cancel the job")
	session, err = q.GetSession(ctx, seed.SessionID)
	require.NoError(t, err)
	require.Equal(t, "evaluation_failed", session.Status)

	require.Equal(t, 2, countLLMCalls(t, ctx, pool, "evaluator"), "both billed attempts are logged")
}

// seedReviewedSession seeds a reviewed session with an evaluation, the state
// the educator job expects.
func seedReviewedSession(t *testing.T, ctx context.Context, pool *pgxpool.Pool) seedResult {
	t.Helper()
	seed := seedSessionWithMessages(t, ctx, pool, 4)
	q := db.New(pool)
	_, err := q.InsertEvaluation(ctx, db.InsertEvaluationParams{
		SessionID:          seed.SessionID,
		ScoreRequirements:  3,
		ScoreArchitecture:  4,
		ScoreDeepDive:      2,
		ScoreScalability:   3,
		ScoreCommunication: 4,
		ScoreOverall:       3,
		Strengths:          []byte(`["Good requirements gathering"]`),
		Gaps:               []byte(`["Missing cache strategy"]`),
		Advice:             "Go deeper.",
	})
	require.NoError(t, err)
	require.NoError(t, q.UpdateSessionStatusOnly(ctx, db.UpdateSessionStatusOnlyParams{
		ID:     seed.SessionID,
		Status: "reviewed",
	}))
	return seed
}

func newEducatorWorker(pool *pgxpool.Pool, srvURL string) *jobs.GenerateEducatorContentWorker {
	return &jobs.GenerateEducatorContentWorker{
		Pool: pool,
		LLM:  ai.NewTestClient(srvURL, pool),
		Cfg: &config.LLM{
			EducatorModel:     "claude-opus-5-5",
			EducatorMaxTokens: 32000,
			EducatorEffort:    "high",
		},
	}
}

func TestGenerateEducatorContentWorker_RefusalCancelsAndMarksFailed(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := newTestPool(t)
	srv := aitest.NewServer(t, aitest.Response{
		Model:           "claude-opus-5-5",
		StopReason:      "refusal",
		RefusalCategory: "cyber",
	})
	seed := seedReviewedSession(t, ctx, pool)
	worker := newEducatorWorker(pool, srv.URL)

	err := worker.Work(ctx, &river.Job[jobs.GenerateEducatorContentArgs]{
		Args: jobs.GenerateEducatorContentArgs{SessionID: seed.SessionID},
	})
	var cancelErr *rivertype.JobCancelError
	require.ErrorAs(t, err, &cancelErr, "refusal must cancel the job, not retry it")
	require.True(t, ai.IsRefusal(err))

	ea, err := db.New(pool).GetEducatorAnalysisBySession(ctx, seed.SessionID)
	require.NoError(t, err)
	require.Equal(t, "failed", ea.Status)
	require.Equal(t, 1, countLLMCalls(t, ctx, pool, "educator"))
}

func TestGenerateEducatorContentWorker_MaxTokensSecondAttemptCancels(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := newTestPool(t)
	srv := maxTokensServer(t, "claude-opus-5-5")
	seed := seedReviewedSession(t, ctx, pool)
	worker := newEducatorWorker(pool, srv.URL)

	err := worker.Work(ctx, &river.Job[jobs.GenerateEducatorContentArgs]{
		JobRow: &rivertype.JobRow{Attempt: 1},
		Args:   jobs.GenerateEducatorContentArgs{SessionID: seed.SessionID},
	})
	var cancelErr *rivertype.JobCancelError
	require.ErrorIs(t, err, ai.ErrMaxTokens)
	require.NotErrorAs(t, err, &cancelErr)

	err = worker.Work(ctx, &river.Job[jobs.GenerateEducatorContentArgs]{
		JobRow: &rivertype.JobRow{Attempt: 2},
		Args:   jobs.GenerateEducatorContentArgs{SessionID: seed.SessionID},
	})
	require.ErrorAs(t, err, &cancelErr)
	ea, err := db.New(pool).GetEducatorAnalysisBySession(ctx, seed.SessionID)
	require.NoError(t, err)
	require.Equal(t, "failed", ea.Status)
}

func TestGenerateQuestionImageWorker_RefusalCancelsAndIsNotRequeued(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := newTestPool(t)
	srv := aitest.NewServer(t, aitest.Response{
		Model:           "claude-sonnet-5-5",
		StopReason:      "refusal",
		RefusalCategory: "general_harms",
		InputTokens:     40,
	})
	questionID := seedQuestion(t, ctx, pool)

	worker := &jobs.GenerateQuestionImageWorker{
		Pool: pool,
		LLM:  ai.NewTestClient(srv.URL, pool),
		Cfg: &config.Config{LLM: config.LLM{
			ImagePromptModel:     "claude-sonnet-5-5",
			ImagePromptMaxTokens: 4096,
			ImagePromptEffort:    "low",
		}},
		// Gemini is checked for readiness but never called, and Store is
		// not reached: the refusal ends the job first.
		Gemini: ai.NewTestGeminiClient("test-model", &http.Client{Transport: failTransport{t}}),
	}

	err := worker.Work(ctx, &river.Job[jobs.GenerateQuestionImageArgs]{
		Args: jobs.GenerateQuestionImageArgs{QuestionID: questionID},
	})
	var cancelErr *rivertype.JobCancelError
	require.ErrorAs(t, err, &cancelErr, "refusal must cancel the job, not retry it")
	require.True(t, ai.IsRefusal(err))
	require.Equal(t, 1, countLLMCalls(t, ctx, pool, "image_prompt_generator"))

	// The sweep inserts with GenerateQuestionImageInsertOpts every minute.
	// Once the question's job is cancelled, that insert must be skipped as a
	// duplicate instead of queueing another paid call.
	riverClient, err := river.NewClient(riverpgxv5.New(pool), &river.Config{})
	require.NoError(t, err)
	first, err := riverClient.Insert(ctx, jobs.GenerateQuestionImageArgs{QuestionID: questionID}, jobs.GenerateQuestionImageInsertOpts())
	require.NoError(t, err)
	_, err = riverClient.JobCancel(ctx, first.Job.ID)
	require.NoError(t, err)

	again, err := riverClient.Insert(ctx, jobs.GenerateQuestionImageArgs{QuestionID: questionID}, jobs.GenerateQuestionImageInsertOpts())
	require.NoError(t, err)
	require.True(t, again.UniqueSkippedAsDuplicate, "sweep must not re-queue a cancelled image job")

	// A different question is still queued normally.
	other, err := riverClient.Insert(ctx, jobs.GenerateQuestionImageArgs{QuestionID: uuid.New()}, jobs.GenerateQuestionImageInsertOpts())
	require.NoError(t, err)
	require.False(t, other.UniqueSkippedAsDuplicate)
}

// failTransport fails the test if any HTTP request is sent through it.
type failTransport struct{ t *testing.T }

func (f failTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f.t.Errorf("unexpected HTTP request to %s", r.URL)
	return nil, errors.New("unexpected HTTP request")
}
