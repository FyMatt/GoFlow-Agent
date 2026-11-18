package config

import "testing"

func TestValidateNotificationsEmailRequirements(t *testing.T) {
	tests := []struct {
		name    string
		email   EmailNotificationConfig
		wantErr bool
	}{
		{name: "disabled is always valid", email: EmailNotificationConfig{Enabled: false}},
		{name: "enabled needs host", email: EmailNotificationConfig{Enabled: true, To: []string{"a@example.com"}, From: "g@example.com"}, wantErr: true},
		{name: "enabled needs recipient", email: EmailNotificationConfig{Enabled: true, SMTPHost: "smtp.example.com", From: "g@example.com"}, wantErr: true},
		{name: "enabled needs sender", email: EmailNotificationConfig{Enabled: true, SMTPHost: "smtp.example.com", To: []string{"a@example.com"}}, wantErr: true},
		{name: "sender falls back to username", email: EmailNotificationConfig{Enabled: true, SMTPHost: "smtp.example.com", Username: "user@example.com", To: []string{"a@example.com"}}},
		{name: "invalid tls mode", email: EmailNotificationConfig{Enabled: true, SMTPHost: "smtp.example.com", From: "g@example.com", To: []string{"a@example.com"}, TLS: "ssl"}, wantErr: true},
		{name: "complete config", email: EmailNotificationConfig{Enabled: true, SMTPHost: "smtp.example.com", SMTPPort: 465, From: "g@example.com", To: []string{"a@example.com"}, TLS: "tls"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := &Config{Notifications: NotificationsConfig{Email: test.email}}
			err := validateNotifications(cfg)
			if test.wantErr && err == nil {
				t.Fatal("expected a validation error")
			}
			if !test.wantErr && err != nil {
				t.Fatalf("unexpected validation error: %v", err)
			}
		})
	}
}
