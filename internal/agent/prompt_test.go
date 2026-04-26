package agent

import (
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
)

func TestBuildSystemPromptIncludesReasoningLoopContract(t *testing.T) {
	prompt := BuildSystemPrompt(config.AgentProfile{
		Name: "Fixer",
		Mode: "fix",
	}, nil, session.Snapshot{})

	for _, expected := range []string{
		"Reasoning loop:",
		"Plan -> Act -> Observe -> Conclude",
		"Workspace and tool contract:",
		"@path",
		"delete_file",
		"If a tool fails or is denied",
		"do not emit a prose prelude",
		"instead of restating the same plan",
		".goflow/session.json",
	} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("expected prompt to contain %q, got:\n%s", expected, prompt)
		}
	}
}

func TestBuildSystemPromptIncludesModeContract(t *testing.T) {
	tests := []struct {
		mode     string
		expected string
	}{
		{mode: "plan", expected: "Mode contract for plan:"},
		{mode: "fix", expected: "Mode contract for fix:"},
		{mode: "audit", expected: "Mode contract for audit:"},
	}

	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			prompt := BuildSystemPrompt(config.AgentProfile{Name: "Agent", Mode: tt.mode}, nil, session.Snapshot{})
			if !strings.Contains(prompt, tt.expected) {
				t.Fatalf("expected prompt to contain %q, got:\n%s", tt.expected, prompt)
			}
		})
	}
}
