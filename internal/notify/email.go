package notify

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// EmailConfig carries the SMTP settings used for real delivery.
type EmailConfig struct {
	Enabled       bool
	Host          string
	Port          int
	Username      string
	Password      string
	From          string
	To            []string
	TLS           string // starttls | tls | none
	SubjectPrefix string
	Timeout       time.Duration
}

// Sender delivers plain-text notifications over SMTP.
type Sender struct {
	cfg EmailConfig
}

// NewSender builds a sender. The caller is expected to have validated the
// configuration; an empty or disabled configuration yields a nil sender.
func NewSender(cfg EmailConfig) *Sender {
	if !cfg.Enabled || strings.TrimSpace(cfg.Host) == "" || len(cfg.To) == 0 {
		return nil
	}
	if cfg.Port <= 0 {
		cfg.Port = 587
	}
	if strings.TrimSpace(cfg.From) == "" {
		cfg.From = cfg.Username
	}
	if strings.TrimSpace(cfg.SubjectPrefix) == "" {
		cfg.SubjectPrefix = "[GoFlow]"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 20 * time.Second
	}
	return &Sender{cfg: cfg}
}

// Enabled reports whether the sender can deliver.
func (s *Sender) Enabled() bool { return s != nil }

// Send delivers one message. It is safe to call from a background goroutine.
func (s *Sender) Send(ctx context.Context, subject, body string) error {
	if s == nil {
		return fmt.Errorf("email notification is not configured")
	}
	message := s.compose(subject, body)
	address := net.JoinHostPort(s.cfg.Host, fmt.Sprintf("%d", s.cfg.Port))
	if err := s.deliver(ctx, address, message); err != nil {
		return fmt.Errorf("send email via %s: %w", address, err)
	}
	return nil
}

func (s *Sender) deliver(ctx context.Context, address string, message []byte) error {
	if strings.EqualFold(strings.TrimSpace(s.cfg.TLS), "tls") {
		return s.deliverImplicitTLS(ctx, address, message)
	}
	dialer := &net.Dialer{Timeout: s.cfg.Timeout}
	connection, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return err
	}
	client, err := smtp.NewClient(connection, s.cfg.Host)
	if err != nil {
		_ = connection.Close()
		return err
	}
	defer func() { _ = client.Close() }()
	if strings.EqualFold(strings.TrimSpace(s.cfg.TLS), "starttls") {
		if supported, _ := client.Extension("STARTTLS"); supported {
			if err := client.StartTLS(&tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12}); err != nil {
				return err
			}
		}
	}
	if strings.TrimSpace(s.cfg.Username) != "" {
		if err := client.Auth(smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)); err != nil {
			return err
		}
	}
	if err := client.Mail(s.cfg.From); err != nil {
		return err
	}
	for _, recipient := range s.cfg.To {
		if err := client.Rcpt(recipient); err != nil {
			return err
		}
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := writer.Write(message); err != nil {
		_ = writer.Close()
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}

func (s *Sender) deliverImplicitTLS(ctx context.Context, address string, message []byte) error {
	dialer := &net.Dialer{Timeout: s.cfg.Timeout}
	connection, err := tls.DialWithDialer(dialer, "tcp", address, &tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12})
	if err != nil {
		return err
	}
	client, err := smtp.NewClient(connection, s.cfg.Host)
	if err != nil {
		_ = connection.Close()
		return err
	}
	defer func() { _ = client.Close() }()
	if strings.TrimSpace(s.cfg.Username) != "" {
		if err := client.Auth(smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)); err != nil {
			return err
		}
	}
	if err := client.Mail(s.cfg.From); err != nil {
		return err
	}
	for _, recipient := range s.cfg.To {
		if err := client.Rcpt(recipient); err != nil {
			return err
		}
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := writer.Write(message); err != nil {
		_ = writer.Close()
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}

func (s *Sender) compose(subject, body string) []byte {
	prefix := strings.TrimSpace(s.cfg.SubjectPrefix)
	if prefix != "" && !strings.HasPrefix(subject, prefix) {
		subject = prefix + " " + subject
	}
	subject = sanitizeHeader(subject)
	lines := []string{
		"From: " + s.cfg.From,
		"To: " + strings.Join(s.cfg.To, ", "),
		"Subject: " + subject,
		"Date: " + time.Now().Format(time.RFC1123Z),
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"Content-Transfer-Encoding: 8bit",
		"",
		body,
	}
	return []byte(strings.Join(lines, "\r\n"))
}

func sanitizeHeader(value string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(value)
}
