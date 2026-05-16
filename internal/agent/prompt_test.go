package agent

import (
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
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

func TestDynamicContextCompactsSessionHistory(t *testing.T) {
	longPrompt := "recent-long-" + strings.Repeat("x", systemPromptPromptItemBytes+200)
	messages := dynamicContextMessages("", session.Snapshot{
		RecentPrompts: []string{"old-1", "old-2", "recent-1", "recent-2", "recent-3", longPrompt},
		RecentTools:   []string{"tool-1", strings.Repeat("y", systemPromptToolItemBytes+200)},
	}, "", []schema.Message{{Role: "user", Content: "hi"}})
	if len(messages) != 2 {
		t.Fatalf("expected dynamic context and user message, got %#v", messages)
	}
	prompt := messages[0].Content

	if strings.Contains(prompt, "old-1") || strings.Contains(prompt, "old-2") {
		t.Fatalf("expected older prompts to be compacted out, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "[compacted 2 older prompts; latest 4 retained]") {
		t.Fatalf("expected older prompt compaction marker, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "recent-1") || !strings.Contains(prompt, "recent-long-") {
		t.Fatalf("expected recent prompts retained, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "[compacted 200 bytes]") {
		t.Fatalf("expected oversized history item compaction marker, got:\n%s", prompt)
	}
	systemPrompt := BuildSystemPrompt(config.AgentProfile{Name: "Agent", Mode: "chat"}, nil, session.Snapshot{
		RecentPrompts: []string{longPrompt},
		RecentTools:   []string{"tool-1"},
	})
	if strings.Contains(systemPrompt, "Recent prompts") || strings.Contains(systemPrompt, "recent-long-") || strings.Contains(systemPrompt, "tool-1") {
		t.Fatalf("expected session history to stay out of stable system prompt, got:\n%s", systemPrompt)
	}
}

func TestDynamicContextDeduplicatesSessionHistory(t *testing.T) {
	messages := dynamicContextMessages("", session.Snapshot{
		RecentPrompts: []string{"same request", "same   request", "new request", "same request"},
		RecentTools:   []string{"read_file path=a.go", "read_file   path=a.go", "write_file path=a.go"},
	}, "", []schema.Message{{Role: "user", Content: "hi"}})
	if len(messages) != 2 {
		t.Fatalf("expected dynamic context and user message, got %#v", messages)
	}
	prompt := messages[0].Content

	if got := strings.Count(prompt, "same request"); got != 1 {
		t.Fatalf("expected duplicate recent prompt to be retained once, got count=%d prompt:\n%s", got, prompt)
	}
	if got := strings.Count(prompt, "read_file"); got != 1 {
		t.Fatalf("expected duplicate tool summary to be retained once, got count=%d prompt:\n%s", got, prompt)
	}
	if !strings.Contains(prompt, "deduplicated 2 repeated prompts") ||
		!strings.Contains(prompt, "deduplicated 1 repeated tool summaries") {
		t.Fatalf("expected history dedupe markers, got:\n%s", prompt)
	}
}

func TestDynamicContextExcludesCurrentPromptFromSessionHistory(t *testing.T) {
	messages := dynamicContextMessages("", session.Snapshot{
		RecentPrompts: []string{"previous request", "inspect auth.go"},
	}, "inspect   auth.go", []schema.Message{{Role: "user", Content: "inspect auth.go"}})
	if len(messages) != 2 {
		t.Fatalf("expected dynamic context and user message, got %#v", messages)
	}
	if strings.Contains(messages[0].Content, "inspect auth.go") {
		t.Fatalf("expected current prompt to be omitted from session context, got:\n%s", messages[0].Content)
	}
	if !strings.Contains(messages[0].Content, "previous request") {
		t.Fatalf("expected previous prompt to remain in session context, got:\n%s", messages[0].Content)
	}
}

func TestBuildSystemPromptSummarizesLargeSkillInstructions(t *testing.T) {
	skill := &schema.Skill{
		Name:         "large-skill",
		Description:  "A large skill used to verify summary-first prompt injection.",
		Instructions: "## Role\n" + strings.Repeat("- Keep detailed procedure text out of the default prompt.\n", 80),
		Path:         "skills/large-skill/SKILL.md",
	}
	prompt := BuildSystemPrompt(config.AgentProfile{Name: "Agent", Mode: "fix"}, skill, session.Snapshot{})
	if !strings.Contains(prompt, "Matched skill: large-skill") ||
		!strings.Contains(prompt, "Skill instructions summary:") ||
		!strings.Contains(prompt, "Full skill instructions omitted") {
		t.Fatalf("expected large skill summary prompt, got:\n%s", prompt)
	}
	if strings.Contains(prompt, strings.Repeat("- Keep detailed procedure text out of the default prompt.\n", 20)) {
		t.Fatalf("expected repeated full skill instructions to be omitted, got:\n%s", prompt)
	}
}
