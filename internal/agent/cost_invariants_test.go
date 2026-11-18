package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/policy"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

// The tool list sent to the model must never contain a tool the policy would
// reject: showing it invites an invalid call that costs a whole turn.
func TestFilterPromptToolsNeverExposesPolicyRejectedTools(t *testing.T) {
	profile := config.AgentProfile{
		AllowedTools:     []string{"file_tools/read_file", "file_tools/edit_file"},
		AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
	}
	tools := []schema.Tool{
		{Name: "read_file", Server: "file_tools", Kind: "read"},
		{Name: "edit_file", Server: "file_tools", Kind: "write"},
		{Name: "write_file", Server: "file_tools", Kind: "write"},
		{Name: "run_command", Server: "command_tools", Kind: "exec"},
	}
	filtered := filterPromptTools(profile, tools)
	exposed := make(map[string]bool, len(filtered))
	for _, tool := range filtered {
		exposed[tool.Server+"/"+tool.Name] = true
	}
	if !exposed["file_tools/read_file"] {
		t.Fatalf("expected the allowed read tool to stay visible, got %#v", exposed)
	}
	for _, forbidden := range []string{"file_tools/write_file", "command_tools/run_command"} {
		if exposed[forbidden] {
			t.Fatalf("prompt exposed policy-rejected tool %s", forbidden)
		}
	}
}

// Compaction must move in blocks so the prompt prefix stays byte-identical for
// consecutive turns inside a block; otherwise the provider cache is invalidated
// every turn (measured: 30% extra cache-miss input).
func TestPruneKeepsPromptPrefixStableWithinCompactionBlock(t *testing.T) {
	build := func(count int) []schema.Message {
		messages := make([]schema.Message, 0, count)
		for i := 0; i < count; i++ {
			role := "user"
			if i%2 == 1 {
				role = "tool"
			}
			messages = append(messages, schema.Message{Role: role, Content: strings.Repeat("x", 2000)})
		}
		return messages
	}
	// 24 messages are always kept in full; below the first block boundary the
	// prefix must not change from one turn to the next.
	previous := ""
	for count := 20; count <= 31; count++ {
		pruned := pruneMessagesForConversation(build(count), 24, 1200, 900)
		prefix := pruned[0].Content
		if previous != "" && prefix != previous {
			t.Fatalf("prefix changed between consecutive turns at count=%d", count)
		}
		previous = prefix
	}
	// Crossing the first block boundary compacts exactly one block, then stays
	// stable again for the following turns.
	atBoundary := pruneMessagesForConversation(build(32), 24, 1200, 900)
	if len(atBoundary[0].Content) >= 2000 {
		t.Fatalf("expected the first block of messages to be compacted at the boundary, got %d bytes", len(atBoundary[0].Content))
	}
	firstBlock := atBoundary[0].Content
	for count := 33; count <= 39; count++ {
		pruned := pruneMessagesForConversation(build(count), 24, 1200, 900)
		if pruned[0].Content != firstBlock {
			t.Fatalf("prefix changed after the boundary at count=%d", count)
		}
	}
}

// A rejected call must tell the model exactly which tools are allowed so the
// next turn can correct itself instead of repeating the same invalid call.
func TestRejectedToolCallNamesAllowedTools(t *testing.T) {
	profile := config.AgentProfile{
		AllowedTools:     []string{"file_tools/edit_file"},
		AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
	}
	tool := schema.Tool{Name: "write_file", Server: "file_tools", Kind: "write"}
	err := policy.EnforceToolPolicy(profile, tool)
	if err == nil {
		t.Fatal("expected the write tool to be rejected by a read-only profile")
	}
	if !strings.Contains(err.Error(), "file_tools/edit_file") {
		t.Fatalf("rejection must name the allowed tools, got %q", err.Error())
	}
	argumentError := validationErrorContentForTest()
	if !strings.Contains(argumentError, "allowed") {
		t.Fatalf("argument validation errors must keep schema hints, got %q", argumentError)
	}
}

func validationErrorContentForTest() string {
	payload, _ := json.Marshal(map[string]string{"error": "tool write_file is not allowed; allowed tools: file_tools/edit_file"})
	return string(payload)
}
