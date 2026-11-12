package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/interfaces"
	"github.com/FyMatt/GoFlow-Agent/internal/runtime"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestRuntimeLoadsRepositoryInstructionsBeforePlanning(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("Run the focused package test before reporting completion.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	llm := &scriptedLLMClient{calls: []scriptedLLMCall{{
		response: schema.ChatResponse{Message: schema.Message{Content: "done"}},
	}}}
	cfg := &config.Config{
		WorkspaceRoot: root,
		Session:       config.SessionConfig{MaxHistory: 8},
		DefaultAgent:  "chat",
		Agents: map[string]config.AgentProfile{
			"chat": {
				Name:             "Chat",
				Provider:         "chat",
				Mode:             "chat",
				Model:            "test-model",
				MaxIterations:    1,
				AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
			},
		},
	}
	runtimeRef, err := NewRuntime(cfg, map[string]interfaces.LLMClient{"chat": llm}, stubSkillManager{}, &stubRuntimeMCP{}, session.New(8), runtime.NewAuditLogger(false, false))
	if err != nil {
		t.Fatal(err)
	}
	var events []schema.StreamEvent
	if _, err := runtimeRef.RunStream(context.Background(), "inspect the package", func(event schema.StreamEvent) error {
		events = append(events, event)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(llm.requests) != 1 || !strings.Contains(llm.requests[0].System, "--- AGENTS.md ---") || !strings.Contains(llm.requests[0].System, "Run the focused package test") {
		t.Fatalf("repository instructions were not loaded into the planning prompt: %#v", llm.requests)
	}
	foundEvidence := false
	for _, event := range events {
		if strings.Contains(event.Content, "repository_instructions=AGENTS.md") {
			foundEvidence = true
			break
		}
	}
	if !foundEvidence {
		t.Fatalf("expected repository instruction evidence event, got %#v", events)
	}
}
