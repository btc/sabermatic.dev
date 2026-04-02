package email

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/btc/drill/internal/config"
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

// NewSender creates the appropriate email sender based on configuration.
// Returns a LogSender if Mailgun is not configured (test-key default).
func NewSender(cfg *config.Email) Sender {
	if cfg.MailgunAPIKey == "test-key" {
		slog.Warn("using log email sender (MAILGUN_API_KEY not configured)")
		return NewLogSender()
	}
	return newMailgunSender(cfg)
}

// MailgunSender sends emails via the Mailgun API.
type MailgunSender struct {
	mg  *mailgun.Client
	cfg *config.Email
}

func newMailgunSender(cfg *config.Email) *MailgunSender {
	mg := mailgun.NewMailgun(cfg.MailgunAPIKey)
	return &MailgunSender{mg: mg, cfg: cfg}
}

func (s *MailgunSender) Send(ctx context.Context, msg Message) error {
	m := mailgun.NewMessage(s.cfg.MailgunDomain, s.cfg.FromAddress, msg.Subject, msg.Text, msg.To)
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
