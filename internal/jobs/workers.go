package jobs

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/btc/drill/internal/ai"
	"github.com/btc/drill/internal/config"
	"github.com/btc/drill/internal/email"
	"github.com/btc/drill/internal/storage"
)

// WorkerRefs holds references to workers that need post-creation wiring
// (their Jobs field must be set after the River client is created).
type WorkerRefs struct {
	Evaluate          *EvaluateSessionWorker
	Cleanup           *CleanupAbandonedSessionsWorker
	CleanupGenerating *CleanupStaleGeneratingWorker
	Educator          *GenerateEducatorContentWorker
	Coach             *RunCoachAnalysisWorker
	ImageGen          *GenerateQuestionImageWorker
	SweepImages       *SweepMissingImagesWorker
}

// RegisterWorkers creates a Workers bundle with all job workers registered.
// Returns WorkerRefs so callers can set the Jobs field after river.NewClient.
func RegisterWorkers(cfg *config.Config, sender email.Sender, pool *pgxpool.Pool, llm *ai.Client, gemini *ai.GeminiClient, publicStore storage.ObjectStore) (*river.Workers, WorkerRefs) {
	workers := river.NewWorkers()
	river.AddWorker(workers, NewSendEmailWorker(&cfg.Email, sender))
	eval := &EvaluateSessionWorker{Pool: pool, LLM: llm, Cfg: &cfg.LLM}
	river.AddWorker(workers, eval)
	cleanup := &CleanupAbandonedSessionsWorker{Pool: pool}
	river.AddWorker(workers, cleanup)
	cleanupGenerating := &CleanupStaleGeneratingWorker{Pool: pool}
	river.AddWorker(workers, cleanupGenerating)
	edu := &GenerateEducatorContentWorker{Pool: pool, LLM: llm, Cfg: &cfg.LLM}
	river.AddWorker(workers, edu)
	coachWorker := &RunCoachAnalysisWorker{Pool: pool, LLM: llm, Cfg: &cfg.LLM}
	river.AddWorker(workers, coachWorker)
	imgGen := &GenerateQuestionImageWorker{Pool: pool, LLM: llm, Gemini: gemini, PublicStore: publicStore, Cfg: cfg}
	river.AddWorker(workers, imgGen)
	sweep := &SweepMissingImagesWorker{Pool: pool}
	river.AddWorker(workers, sweep)
	return workers, WorkerRefs{
		Evaluate:          eval,
		Cleanup:           cleanup,
		CleanupGenerating: cleanupGenerating,
		Educator:          edu,
		Coach:             coachWorker,
		ImageGen:          imgGen,
		SweepImages:       sweep,
	}
}
