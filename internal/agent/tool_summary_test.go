package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestChineseSummaryTruncationPreservesUTF8(t *testing.T) {
	for _, prefix := range []string{"", "a", "ab"} {
		value := prefix + strings.Repeat("中文路径", 180)
		for _, summary := range []string{workflowPromptSummary(value), truncateToolSummaryValue(value)} {
			if !utf8.ValidString(summary) || strings.ContainsRune(summary, '\ufffd') {
				t.Fatalf("summary split a Chinese character: %q", summary)
			}
		}
	}
}

func TestSummarizeToolArgumentsPrioritizesCommandProgramAndCWD(t *testing.T) {
	arguments := json.RawMessage(`{"program":"go","args":["test","./...","-run","AReallyLongTestSelectorThatShouldNotHideTheProgramOrWorkingDirectory"],"cwd":"services/api","timeout_seconds":120}`)
	summary := summarizeToolArguments(arguments)
	if !strings.HasPrefix(summary, "program=go cwd=services/api") {
		t.Fatalf("expected command identity first in approval summary, got %q", summary)
	}
	if len(summary) > 120 {
		t.Fatalf("expected bounded summary, got %d chars", len(summary))
	}
}
