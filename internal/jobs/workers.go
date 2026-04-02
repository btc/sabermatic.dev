package jobs

import (
	"github.com/btc/drill/internal/email"
	"github.com/riverqueue/river"
)

func RegisterWorkers(sender email.Sender) *river.Workers {
	workers := river.NewWorkers()
	river.AddWorker(workers, &SendEmailWorker{Sender: sender})
	return workers
}
