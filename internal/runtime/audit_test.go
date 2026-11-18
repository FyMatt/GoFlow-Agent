package runtime

import (
	"fmt"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestAuditLoggerBoundsInMemoryEntries(t *testing.T) {
	logger := NewAuditLogger(true, false)
	for i := 0; i < maxAuditEntries+25; i++ {
		logger.Record(schema.AuditEntry{Type: "test", Detail: fmt.Sprintf("entry-%d", i)})
	}
	entries := logger.Entries()
	if len(entries) != maxAuditEntries {
		t.Fatalf("expected bounded audit entries, got %d", len(entries))
	}
	if entries[0].Detail != "entry-25" || entries[len(entries)-1].Detail != fmt.Sprintf("entry-%d", maxAuditEntries+24) {
		t.Fatalf("expected newest audit entries to be retained, got first=%q last=%q", entries[0].Detail, entries[len(entries)-1].Detail)
	}
}
