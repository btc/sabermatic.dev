package email

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/mailgun/mailgun-go/v5"
)

// Message represents an email to send.
type Message struct {
	To      string
	Subject string
	Text    string
	HTML    string
}

// Sender sends emails.
type Sender interface {
	Send(ctx context.Context, msg Message) error
}

// MailgunSender sends emails via the Mailgun API.
type MailgunSender struct {
	mg     *mailgun.Client
	domain string
	from   string
}

func NewMailgunSender(apiKey, domain, from string) *MailgunSender {
	mg := mailgun.NewMailgun(apiKey)
	return &MailgunSender{mg: mg, domain: domain, from: from}
}

func (s *MailgunSender) Send(ctx context.Context, msg Message) error {
	m := mailgun.NewMessage(s.domain, s.from, msg.Subject, msg.Text, msg.To)
	if msg.HTML != "" {
		m.SetHTML(msg.HTML)
	}
	resp, err := s.mg.Send(ctx, m)
	if err != nil {
		return fmt.Errorf("mailgun send to %s: %w", msg.To, err)
	}
	slog.Info("email sent", "to", msg.To, "subject", msg.Subject, "id", resp.ID)
	return nil
}

// LogSender logs emails instead of sending them. Used in tests and development.
type LogSender struct {
	mu       sync.Mutex
	messages []Message
}

func NewLogSender() *LogSender {
	return &LogSender{}
}

func (s *LogSender) Send(_ context.Context, msg Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	slog.Info("email logged (not sent)", "to", msg.To, "subject", msg.Subject)
	s.messages = append(s.messages, msg)
	return nil
}

func (s *LogSender) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.messages)
}

func (s *LogSender) Last() Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.messages[len(s.messages)-1]
}
