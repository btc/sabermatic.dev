package jobs

import (
	"context"
	"fmt"

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

type SendEmailWorker struct {
	river.WorkerDefaults[SendEmailArgs]
	Sender email.Sender
}

func (w *SendEmailWorker) Work(ctx context.Context, job *river.Job[SendEmailArgs]) error {
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
