package session

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestWorkflowArtifactTitlePreservesUTF8(t *testing.T) {
	for _, value := range []string{strings.Repeat("中文审计报告", 12), "a" + strings.Repeat("📝", 22), strings.Repeat("a", 90)} {
		title := fallbackWorkflowArtifactTitle(value, "阶段结果")
		if !utf8.ValidString(title) || len(title) > 80 || !strings.HasPrefix(value, title) || title == "" {
			t.Fatalf("invalid truncated title %q", title)
		}
		if utf8.RuneCountInString(title) < utf8.RuneCountInString(value) {
			next, size := utf8.DecodeRuneInString(value[len(title):])
			if next != utf8.RuneError && len(title)+size <= 80 {
				t.Fatalf("title dropped a complete character unnecessarily: %q", title)
			}
		}
	}
	if title := fallbackWorkflowArtifactTitle(" ", "阶段结果"); title != "阶段结果" {
		t.Fatalf("unexpected fallback %q", title)
	}
}
