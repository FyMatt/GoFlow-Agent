package agent

import (
	"fmt"
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
	if strings.Contains(prompt, "recent-1") {
		t.Fatalf("expected one more older prompt to be compacted out, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "[compacted 3 older prompts; latest 3 retained]") {
		t.Fatalf("expected older prompt compaction marker, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "recent-2") || !strings.Contains(prompt, "recent-long-") {
		t.Fatalf("expected recent prompts retained, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "[compacted ") || !strings.Contains(prompt, " bytes]") {
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

func TestCompactMessagesForSummaryPromptDropsOlderConversation(t *testing.T) {
	messages := make([]schema.Message, 0, 14)
	for i := 0; i < 14; i++ {
		messages = append(messages, schema.Message{Role: "user", Content: fmt.Sprintf("message-%02d %s", i, strings.Repeat("x", 80))})
	}
	compacted := compactMessagesForSummaryPrompt(messages)
	if len(compacted) != 11 {
		t.Fatalf("expected leading context plus marker plus latest eight messages, got %d: %#v", len(compacted), compacted)
	}
	if !strings.Contains(compacted[2].Content, "compacted 4 earlier conversation messages") {
		t.Fatalf("expected compaction marker, got %#v", compacted[2])
	}
	if compacted[0].Content == "" || compacted[1].Content == "" || !strings.Contains(compacted[0].Content, "message-00") || !strings.Contains(compacted[1].Content, "message-01") {
		t.Fatalf("expected leading context preserved, got %#v", compacted[:2])
	}
	for _, message := range compacted[2:] {
		if strings.Contains(message.Content, "message-02") || strings.Contains(message.Content, "message-05") {
			t.Fatalf("expected older tail messages omitted, got %#v", compacted)
		}
	}
	if !strings.Contains(compacted[len(compacted)-1].Content, "message-13") {
		t.Fatalf("expected newest message retained, got %#v", compacted[len(compacted)-1])
	}
}

func TestCompactMessagesForResumePromptStartsAtAssistantBeforeTool(t *testing.T) {
	messages := []schema.Message{
		{Role: "user", Content: "old-0"},
		{Role: "assistant", Content: "old-1"},
		{Role: "tool", Content: "old-tool"},
	}
	for i := 0; i < 12; i++ {
		messages = append(messages, schema.Message{Role: "user", Content: fmt.Sprintf("new-%02d", i)})
	}
	compacted := compactMessagesForPrompt(messages, 2, 12, 200, 200)
	if len(compacted) < 2 {
		t.Fatalf("expected compacted messages, got %#v", compacted)
	}
	if compacted[1].Role == "tool" {
		t.Fatalf("expected compacted suffix not to start with bare tool message, got %#v", compacted)
	}
}

func TestCompactMessagesForConversationRetainsLeadingContext(t *testing.T) {
	messages := []schema.Message{
		{Role: "system", Content: "stable system"},
		{Role: "user", Content: "original request"},
	}
	for i := 0; i < 18; i++ {
		messages = append(messages, schema.Message{Role: "assistant", Content: fmt.Sprintf("assistant-%02d", i)})
	}
	compacted := compactMessagesForConversation(messages)
	if len(compacted) < 3 {
		t.Fatalf("expected preserved leading context plus compacted tail, got %#v", compacted)
	}
	if compacted[0].Role != "system" || compacted[1].Role != "user" {
		t.Fatalf("expected leading context preserved, got %#v", compacted[:2])
	}
	if !strings.Contains(compacted[2].Content, "compacted") {
		t.Fatalf("expected compacted marker after leading context, got %#v", compacted)
	}
}

func TestCompactMessagesForConversationIsIdempotent(t *testing.T) {
	messages := []schema.Message{
		{Role: "user", Content: "context"},
		{Role: "user", Content: "request"},
	}
	for i := 0; i < 22; i++ {
		messages = append(messages, schema.Message{Role: "assistant", Content: fmt.Sprintf("turn-%02d", i)})
	}
	once := compactMessagesForConversation(messages)
	twice := compactMessagesForConversation(once)
	markers := 0
	latestMarker := ""
	for _, message := range twice {
		if isGoFlowConversationCompactionMarker(message.Content) {
			markers++
			latestMarker = message.Content
		}
	}
	if markers != 1 {
		t.Fatalf("expected one compaction marker after repeated compaction, got %d: %#v", markers, twice)
	}
	if !strings.Contains(latestMarker, "earlier conversation messages") || !strings.Contains(latestMarker, "latest") {
		t.Fatalf("expected readable compaction marker, got %q", latestMarker)
	}
}

func TestToolObservationDigestSummarizesRecentResults(t *testing.T) {
	results := make([]schema.ToolResult, 0, 7)
	for i := 0; i < 7; i++ {
		results = append(results, schema.ToolResult{ToolName: "read_file", Content: fmt.Sprintf("result-%02d %s", i, strings.Repeat("x", 200))})
	}
	digest := toolObservationDigestMessage(results)
	if !strings.Contains(digest, "Tool observation digest:") || !strings.Contains(digest, "1 earlier tool observations omitted") {
		t.Fatalf("expected digest with omitted count, got %q", digest)
	}
	if strings.Contains(digest, "result-00") {
		t.Fatalf("expected oldest result omitted from digest, got %q", digest)
	}
	if !strings.Contains(digest, "result-06") {
		t.Fatalf("expected newest result retained in digest, got %q", digest)
	}
}
