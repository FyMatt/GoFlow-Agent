package notify

import (
	"bufio"
	"context"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

// startTestSMTPServer accepts one SMTP session and returns the DATA payload.
func startTestSMTPServer(t *testing.T) (addr string, received <-chan string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	payload := make(chan string, 1)
	go func() {
		defer listener.Close()
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		writer := bufio.NewWriter(conn)
		_, _ = writer.WriteString("220 test ESMTP\r\n")
		_ = writer.Flush()
		var data strings.Builder
		inData := false
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				payload <- data.String()
				return
			}
			if inData {
				if line == ".\r\n" {
					inData = false
					_, _ = writer.WriteString("250 OK\r\n")
					_ = writer.Flush()
					continue
				}
				data.WriteString(line)
				continue
			}
			command := strings.ToUpper(strings.TrimSpace(line))
			switch {
			case strings.HasPrefix(command, "EHLO"), strings.HasPrefix(command, "HELO"):
				_, _ = writer.WriteString("250-test\r\n250 OK\r\n")
			case strings.HasPrefix(command, "MAIL FROM"), strings.HasPrefix(command, "RCPT TO"):
				_, _ = writer.WriteString("250 OK\r\n")
			case command == "DATA":
				_, _ = writer.WriteString("354 End data\r\n")
				inData = true
			case command == "QUIT":
				_, _ = writer.WriteString("221 Bye\r\n")
				_ = writer.Flush()
				payload <- data.String()
				return
			default:
				_, _ = writer.WriteString("250 OK\r\n")
			}
			_ = writer.Flush()
		}
	}()
	return listener.Addr().String(), payload
}

func TestSenderDeliversPlainTextEmail(t *testing.T) {
	address, payload := startTestSMTPServer(t)
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatalf("split address: %v", err)
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}
	sender := NewSender(EmailConfig{
		Enabled: true,
		Host:    host,
		Port:    portNumber,
		From:    "goflow@example.com",
		To:      []string{"ops@example.com"},
		TLS:     "none",
	})
	if sender == nil {
		t.Fatal("expected a configured sender")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := sender.Send(ctx, "运行完成", "workflow 已完成，状态 completed。"); err != nil {
		t.Fatalf("send: %v", err)
	}
	select {
	case message := <-payload:
		for _, expected := range []string{"Subject: [GoFlow] 运行完成", "To: ops@example.com", "workflow 已完成"} {
			if !strings.Contains(message, expected) {
				t.Fatalf("message missing %q: %s", expected, message)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("smtp server received no message")
	}
}

func TestNewSenderIsNilWhenDisabled(t *testing.T) {
	if sender := NewSender(EmailConfig{Enabled: false, Host: "smtp.example.com", To: []string{"a@example.com"}}); sender != nil {
		t.Fatal("disabled email must not produce a sender")
	}
	if sender := NewSender(EmailConfig{Enabled: true, Host: "", To: []string{"a@example.com"}}); sender != nil {
		t.Fatal("email without a host must not produce a sender")
	}
	if sender := NewSender(EmailConfig{Enabled: true, Host: "smtp.example.com"}); sender != nil {
		t.Fatal("email without recipients must not produce a sender")
	}
}

func TestComposeSanitizesHeaderInjection(t *testing.T) {
	sender := NewSender(EmailConfig{
		Enabled: true, Host: "smtp.example.com", From: "a@example.com",
		To: []string{"b@example.com"}, SubjectPrefix: "[GoFlow]", TLS: "none",
	})
	message := string(sender.compose("subject\r\nBcc: evil@example.com", "body"))
	for _, line := range strings.Split(message, "\r\n") {
		if strings.HasPrefix(strings.ToLower(line), "bcc:") {
			t.Fatalf("header injection produced a real Bcc header: %s", message)
		}
	}
}
