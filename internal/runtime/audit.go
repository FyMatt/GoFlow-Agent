package runtime

import (
	"context"
	"sync"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

// AuditLogger records runtime audit events.
type AuditLogger struct {
	mu      sync.Mutex
	enabled bool
	redact  bool
	entries []schema.AuditEntry
}

func NewAuditLogger(enabled, redact bool) *AuditLogger {
	return &AuditLogger{enabled: enabled, redact: redact}
}

func (l *AuditLogger) Record(entry schema.AuditEntry) {
	if l == nil || !l.enabled {
		return
	}
	if l.redact && entry.Detail != "" {
		entry.Detail = "[redacted]"
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, entry)
}

func (l *AuditLogger) Entries() []schema.AuditEntry {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]schema.AuditEntry, len(l.entries))
	copy(out, l.entries)
	return out
}

func (l *AuditLogger) Clear() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = nil
}

// HealthReporter captures component health snapshots.
type HealthReporter interface {
	HealthStatus(ctx context.Context) map[string]string
}
