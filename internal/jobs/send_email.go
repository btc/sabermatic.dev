package jobs

import (
	"context"
	"fmt"
	"time"

	"github.com/btc/drill/internal/config"
	"github.com/btc/drill/internal/drilotel"
	"github.com/btc/drill/internal/email"
	"github.com/riverqueue/river"
)

// SendEmailArgs are the arguments for the SendEmail job.
type SendEmailArgs struct {
	To      string `json:"to"`
	Subject string `json:"subject"`
	Text    string `json:"text"`
	HTML    string `json:"html,omitempty"`
}

func (args SendEmailArgs) Kind() string {
	return "send_email"
}

// SendEmailInsertOpts returns the River insert options for email jobs.
func SendEmailInsertOpts(cfg *config.Email) *river.InsertOpts {
	return &river.InsertOpts{
		Queue:       QueueNotifications,
		MaxAttempts: cfg.MaxSendAttempts,
	}
}

// SendEmailWorker processes SendEmail jobs.
type SendEmailWorker struct {
	river.WorkerDefaults[SendEmailArgs]
	Sender email.Sender
	cfg    *config.Email
}

func NewSendEmailWorker(cfg *config.Email, sender email.Sender) *SendEmailWorker {
	return &SendEmailWorker{Sender: sender, cfg: cfg}
}

func (w *SendEmailWorker) Timeout(job *river.Job[SendEmailArgs]) time.Duration {
	return w.cfg.SendTimeout
}

func (w *SendEmailWorker) Work(ctx context.Context, job *river.Job[SendEmailArgs]) (err error) {
	ctx, span := tracer.Start(ctx, "SendEmailWorker.Work")
	defer func() { drilotel.End(span, err) }()

	if job.Args.To == "" {
		return fmt.Errorf("send_email: missing recipient")
	}
	err = w.Sender.Send(ctx, email.Message{
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
