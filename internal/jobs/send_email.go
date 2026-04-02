package jobs

import (
	"context"
	"fmt"
	"time"

	"github.com/btc/drill/internal/email"
	"github.com/riverqueue/river"
)

type SendEmailArgs struct {
	To      string `json:"to"`
	Subject string `json:"subject"`
	Text    string `json:"text"`
	HTML    string `json:"html,omitempty"`
}

func (args SendEmailArgs) Kind() string {
	return "send_email"
}

func (args SendEmailArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		Queue:       "notifications",
		MaxAttempts: 3,
	}
}

type SendEmailWorker struct {
	river.WorkerDefaults[SendEmailArgs]
	Sender email.Sender
}

// Timeout returns the max duration for a single email send attempt.
func (w *SendEmailWorker) Timeout(job *river.Job[SendEmailArgs]) time.Duration {
	return 1 * time.Minute
}

func (w *SendEmailWorker) Work(ctx context.Context, job *river.Job[SendEmailArgs]) error {
	if job.Args.To == "" {
		return fmt.Errorf("send_email: missing recipient")
	}
	err := w.Sender.Send(ctx, email.Message{
		To:      job.Args.To,
		Subject: job.Args.Subject,
		Text:    job.Args.Text,
		HTML:    job.Args.HTML,
	})
	if err != nil {
		return fmt.Errorf("send email to %s: %w", job.Args.To, err)
	}
	return nil
}
