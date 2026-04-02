package email_test

import (
	"context"
	"testing"

	"github.com/btc/drill/internal/email"
	"github.com/stretchr/testify/require"
)

func TestLogSender_Send(t *testing.T) {
	sender := email.NewLogSender()

	err := sender.Send(context.Background(), email.Message{
		To:      "test@example.com",
		Subject: "Test Subject",
		Text:    "Test body",
		HTML:    "<p>Test body</p>",
	})
	require.NoError(t, err)
	require.Equal(t, 1, sender.Count())
	require.Equal(t, "test@example.com", sender.Last().To)
}

func TestMailgunSender_Implements_Sender(t *testing.T) {
	var _ email.Sender = (*email.MailgunSender)(nil)
}
