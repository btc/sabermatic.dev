package jobs_test

import (
	"context"
	"testing"

	"github.com/btc/drill/internal/email"
	"github.com/btc/drill/internal/jobs"
	"github.com/riverqueue/river"
	"github.com/stretchr/testify/require"
)

func TestSendEmailArgs_Kind(t *testing.T) {
	args := jobs.SendEmailArgs{
		To:      "test@example.com",
		Subject: "Hello",
		Text:    "World",
	}
	require.Equal(t, "send_email", args.Kind())
}

func TestSendEmailWorker_Work(t *testing.T) {
	sender := email.NewLogSender()
	worker := &jobs.SendEmailWorker{Sender: sender}

	job := &river.Job[jobs.SendEmailArgs]{
		Args: jobs.SendEmailArgs{
			To:      "user@example.com",
			Subject: "Verify your email",
			Text:    "Click here to verify",
			HTML:    "<a href='#'>Click here</a>",
		},
	}

	err := worker.Work(context.Background(), job)
	require.NoError(t, err)
	require.Equal(t, 1, sender.Count())
	require.Equal(t, "user@example.com", sender.Last().To)
	require.Equal(t, "Verify your email", sender.Last().Subject)
}
