package jobs

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/ai"
	"github.com/btc/drill/internal/config"
)

func TestGenerateQuestionImageArgs_Kind(t *testing.T) {
	args := GenerateQuestionImageArgs{QuestionID: uuid.New()}
	require.Equal(t, "generate_question_image", args.Kind())
}

func TestGenerateQuestionImageInsertOpts(t *testing.T) {
	opts := GenerateQuestionImageInsertOpts()
	require.Equal(t, QueueGemini, opts.Queue)
	require.Equal(t, 3, opts.MaxAttempts)
	require.True(t, opts.UniqueOpts.ByArgs)
	// Cancelled and discarded jobs block re-queueing by the sweep; completed
	// does not, so nulling image_url regenerates.
	require.Contains(t, opts.UniqueOpts.ByState, rivertype.JobStateCancelled)
	require.Contains(t, opts.UniqueOpts.ByState, rivertype.JobStateDiscarded)
	require.NotContains(t, opts.UniqueOpts.ByState, rivertype.JobStateCompleted)
}

// Without usable Google credentials the worker must snooze before loading the
// question or making the billed Sonnet call. Pool and LLM are nil: touching
// either would panic.
func TestGenerateQuestionImageWorker_SnoozesWhenGeminiUnavailable(t *testing.T) {
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", filepath.Join(t.TempDir(), "missing.json"))

	cfg := &config.Config{}
	cfg.GCP.ProjectID = "test-project"
	cfg.Gemini.Location = "global"
	cfg.Gemini.Model = "test-model"

	w := &GenerateQuestionImageWorker{Gemini: ai.NewGeminiClient(cfg), Cfg: cfg}
	err := w.Work(context.Background(), &river.Job[GenerateQuestionImageArgs]{
		JobRow: &rivertype.JobRow{},
		Args:   GenerateQuestionImageArgs{QuestionID: uuid.New()},
	})

	var snooze *river.JobSnoozeError
	require.ErrorAs(t, err, &snooze)
	require.Equal(t, geminiUnavailableSnooze, snooze.Duration)
}
