package jobs

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/btc/drill/internal/config"
	"github.com/btc/drill/internal/email"
)

// RegisterWorkers creates a Workers bundle with all job workers registered.
func RegisterWorkers(cfg *config.Config, sender email.Sender, pool *pgxpool.Pool) *river.Workers {
	workers := river.NewWorkers()
	river.AddWorker(workers, NewSendEmailWorker(&cfg.Email, sender))
	river.AddWorker(workers, &EvaluateSessionWorker{})
	river.AddWorker(workers, &CleanupAbandonedSessionsWorker{Pool: pool})
	return workers
}
