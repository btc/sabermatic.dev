package jobs

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/btc/drill/internal/ai"
	"github.com/btc/drill/internal/config"
	"github.com/btc/drill/internal/email"
)

// WorkerRefs holds references to workers that need post-creation wiring
// (their Jobs field must be set after the River client is created).
type WorkerRefs struct {
	Evaluate *EvaluateSessionWorker
	Cleanup  *CleanupAbandonedSessionsWorker
	Educator *GenerateEducatorContentWorker
	Coach    *RunCoachAnalysisWorker
}

// RegisterWorkers creates a Workers bundle with all job workers registered.
// Returns WorkerRefs so callers can set the Jobs field after river.NewClient.
func RegisterWorkers(cfg *config.Config, sender email.Sender, pool *pgxpool.Pool, llm *ai.Client) (*river.Workers, WorkerRefs) {
	workers := river.NewWorkers()
	river.AddWorker(workers, NewSendEmailWorker(&cfg.Email, sender))
	eval := &EvaluateSessionWorker{Pool: pool, LLM: llm, Cfg: &cfg.LLM}
	river.AddWorker(workers, eval)
	cleanup := &CleanupAbandonedSessionsWorker{Pool: pool}
	river.AddWorker(workers, cleanup)
	edu := &GenerateEducatorContentWorker{Pool: pool, LLM: llm, Cfg: &cfg.LLM}
	river.AddWorker(workers, edu)
	coachWorker := &RunCoachAnalysisWorker{Pool: pool, LLM: llm, Cfg: &cfg.LLM}
	river.AddWorker(workers, coachWorker)
	return workers, WorkerRefs{Evaluate: eval, Cleanup: cleanup, Educator: edu, Coach: coachWorker}
}
