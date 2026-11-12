package agent

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestVerifierPromptPreservesRecentToolEvidence(t *testing.T) {
	content := `{"args":["-c","from app import app; app.run(host=\"127.0.0.1\", port=5003, debug=False, use_reloader=False)"],"cwd":"D:/Projects/test/web","program":".venv/Scripts/python.exe","running":true}`
	prompt := buildVerifierPrompt("Start the existing preview without editing files", schema.AgentResult{
		Output: "Preview started; no project files modified.",
		ToolResults: []schema.ToolResult{
			{ToolName: "start_process", Content: content},
			{ToolName: "browser_snapshot", Content: `{"dom_bytes":3322,"url":"http://127.0.0.1:5003/"}`},
		},
	})
	for _, want := range []string{`"program":".venv/Scripts/python.exe"`, `"running":true`, `"dom_bytes":3322`} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("verifier lost tool-backed evidence %q: %s", want, prompt)
		}
	}
}

func TestVerifierPromptDetailedEvidenceIsBoundedAndKeepsFailures(t *testing.T) {
	results := []schema.ToolResult{{ToolName: "earlier_write", IsError: true, Content: "permission denied"}}
	for i := 0; i < 9; i++ {
		results = append(results, schema.ToolResult{ToolName: "read_file", Content: strings.Repeat("证据", 4000)})
	}
	prompt := buildVerifierPrompt("Check the result", schema.AgentResult{Output: "Done", ToolResults: results})
	if !strings.Contains(prompt, "earlier_write (error): permission denied") {
		t.Fatal("earlier failure was removed from verification history")
	}
	if len(prompt) > 44000 || !utf8.ValidString(prompt) {
		t.Fatalf("detailed evidence must stay bounded and UTF-8 safe: %d bytes", len(prompt))
	}
	if !strings.Contains(prompt, "remaining tool content omitted") {
		t.Fatal("truncated evidence must be explicitly labeled")
	}
}
