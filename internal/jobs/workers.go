package jobs

import (
	"github.com/btc/drill/internal/config"
	"github.com/btc/drill/internal/email"
	"github.com/riverqueue/river"
)

// RegisterWorkers creates a Workers bundle with all job workers registered.
func RegisterWorkers(cfg *config.Config, sender email.Sender) *river.Workers {
	workers := river.NewWorkers()
	river.AddWorker(workers, NewSendEmailWorker(&cfg.Email, sender))
	return workers
}
