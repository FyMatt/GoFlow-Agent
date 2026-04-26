package agent

import (
	"context"
	"encoding/json"
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

func TestWorkflowRunnerRunsCustomWorkflowGraph(t *testing.T) {
	runtimeHome := t.TempDir()
	writeWorkflowGraph(t, runtimeHome, "release-check", `
name: release-check
description: Validate and improve a change.
stages:
  - name: triage
    agent: planner
    skill: execution-plan
    next_strategy: select
    next: [implement, audit]
  - name: implement
    agent: fixer
    skill: code-writing
  - name: audit
    agent: auditor
    skill: code-audit
`)
	skills := workflowGraphTestSkills()
	plannerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Plan ready.\nNext skill: implement"}}}}
	fixerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Implementation done."}}}}
	auditorLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Audit done."}}}}
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, skills, &stubRuntimeMCP{}, map[string]interfaces.LLMClient{
		"chat":    &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}},
		"planner": plannerLLM,
		"fixer":   fixerLLM,
		"auditor": auditorLLM,
	})

	result, err := runtimeRef.WorkflowRunner().Run(context.Background(), "release-check", "ship this feature", true, nil)
	if err != nil {
		t.Fatalf("Run custom workflow graph: %v", err)
	}
	if result.Status != "completed" || len(result.CompletedStages) != 3 {
		t.Fatalf("expected completed custom workflow with three stages, got %#v", result)
	}
	expected := []struct {
		stage string
		agent string
	}{
		{"triage", "planner"},
		{"implement", "fixer"},
		{"audit", "auditor"},
	}
	for i, want := range expected {
		if string(result.CompletedStages[i].Stage) != want.stage || result.CompletedStages[i].Agent != want.agent {
			t.Fatalf("unexpected stage %d: got %#v want %#v", i, result.CompletedStages[i], want)
		}
	}
	if !strings.Contains(plannerLLM.requests[0].System, "Matched skill: execution-plan") {
		t.Fatalf("expected planner stage to use execution-plan skill, got %q", plannerLLM.requests[0].System)
	}
	if !strings.Contains(fixerLLM.requests[0].Messages[len(fixerLLM.requests[0].Messages)-1].Content, "Completed prior stages") {
		t.Fatalf("expected implement prompt to include prior stage context, got %#v", fixerLLM.requests[0].Messages)
	}
	if runtimeRef.ActiveAgent() != "chat" {
		t.Fatalf("expected default agent restored after custom workflow, got %q", runtimeRef.ActiveAgent())
	}
}

func TestWorkflowRunnerCustomWorkflowGraphSelectsBranch(t *testing.T) {
	runtimeHome := t.TempDir()
	writeWorkflowGraph(t, runtimeHome, "release-check", `
name: release-check
stages:
  - name: triage
    agent: planner
    skill: execution-plan
    next_strategy: select
    next: [implement, audit]
  - name: implement
    agent: fixer
    skill: code-writing
  - name: audit
    agent: auditor
    skill: code-audit
`)
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{
		"chat":    &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}},
		"planner": &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Security audit should run next."}}}},
		"fixer":   &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "fix should not run"}}}},
		"auditor": &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Audit done."}}}},
	})

	result, err := runtimeRef.WorkflowRunner().Run(context.Background(), "release-check", "review the login code", true, nil)
	if err != nil {
		t.Fatalf("Run custom workflow graph: %v", err)
	}
	if result.Status != "completed" || len(result.CompletedStages) != 2 {
		t.Fatalf("expected triage plus selected audit stage, got %#v", result)
	}
	if result.CompletedStages[1].Stage != "audit" || result.CompletedStages[1].Agent != "auditor" {
		t.Fatalf("expected selected audit branch, got %#v", result.CompletedStages[1])
	}
}

func TestWorkflowRunnerCustomWorkflowGraphResumesSuspendedStage(t *testing.T) {
	runtimeHome := t.TempDir()
	writeWorkflowGraph(t, runtimeHome, "release-check", `
name: release-check
stages:
  - name: triage
    agent: planner
    skill: execution-plan
    next: [implement]
  - name: implement
    agent: fixer
    skill: code-writing
    next: [audit]
  - name: audit
    agent: auditor
    skill: code-audit
`)
	writeTool := schema.Tool{Name: "write_file", Kind: "write", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`)}
	mcpClient := &stubRuntimeMCP{tools: []schema.Tool{writeTool}}
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), mcpClient, map[string]interfaces.LLMClient{
		"chat":    &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}},
		"planner": &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Plan ready."}}}},
		"fixer": &workflowRecordingLLMClient{responses: []schema.ChatResponse{{
			Message: schema.Message{Content: "Need to write."},
			ToolCalls: []schema.ToolCall{{
				ID:        "call-1",
				Name:      "write_file",
				Arguments: json.RawMessage(`{"path":"a.txt","content":"hello"}`),
			}},
		}, {
			Message: schema.Message{Content: "Implementation done."},
		}}},
		"auditor": &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Audit done."}}}},
	})

	first, err := runtimeRef.WorkflowRunner().Run(context.Background(), "release-check", "add the feature", true, nil)
	if err != nil {
		t.Fatalf("Run custom workflow graph: %v", err)
	}
	if first.Status != "awaiting_tool_approval" || first.NextStage != "implement" {
		t.Fatalf("expected suspended implement stage, got %#v", first)
	}
	if mcpClient.calls != 0 {
		t.Fatalf("expected no tool execution before approval, got %d calls", mcpClient.calls)
	}

	resumed, err := runtimeRef.WorkflowRunner().Resume(context.Background(), "release-check", "call-1", true, nil)
	if err != nil {
		t.Fatalf("Resume custom workflow graph: %v", err)
	}
	if resumed.Status != "completed" || len(resumed.CompletedStages) != 3 {
		t.Fatalf("expected resumed workflow to complete, got %#v", resumed)
	}
	if mcpClient.calls != 1 {
		t.Fatalf("expected approved tool to execute once, got %d calls", mcpClient.calls)
	}
	if resumed.CompletedStages[1].Result.Output != "Implementation done." || resumed.CompletedStages[2].Result.Output != "Audit done." {
		t.Fatalf("unexpected resumed stage outputs: %#v", resumed.CompletedStages)
	}
	if runtimeRef.ActiveAgent() != "chat" {
		t.Fatalf("expected default agent restored after resume, got %q", runtimeRef.ActiveAgent())
	}
}

func TestWorkflowRunnerCustomWorkflowGraphReportsInvalidFile(t *testing.T) {
	tests := []struct {
		name      string
		yaml      string
		wantError string
	}{
		{
			name: "missing-skill",
			yaml: `
name: missing-skill
stages:
  - name: triage
    agent: planner
`,
			wantError: "stage triage missing skill",
		},
		{
			name: "missing-agent",
			yaml: `
name: missing-agent
stages:
  - name: triage
    skill: execution-plan
`,
			wantError: "stage triage missing agent",
		},
		{
			name: "unknown-next",
			yaml: `
name: unknown-next
stages:
  - name: triage
    agent: planner
    skill: execution-plan
    next: [implement]
`,
			wantError: `references unknown next stage "implement"`,
		},
		{
			name: "duplicate-stage",
			yaml: `
name: duplicate-stage
stages:
  - name: triage
    agent: planner
    skill: execution-plan
  - name: triage
    agent: auditor
    skill: code-audit
`,
			wantError: `duplicate stage "triage"`,
		},
		{
			name: "name-mismatch",
			yaml: `
name: other-name
stages:
  - name: triage
    agent: planner
    skill: execution-plan
`,
			wantError: `name "other-name" does not match "name-mismatch"`,
		},
		{
			name: "empty-stages",
			yaml: `
name: empty-stages
stages: []
`,
			wantError: "has no stages",
		},
		{
			name: "missing-name",
			yaml: `
description: Missing workflow name.
stages:
  - name: triage
    agent: planner
    skill: execution-plan
`,
			wantError: "missing name",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtimeHome := t.TempDir()
			writeWorkflowGraph(t, runtimeHome, tt.name, tt.yaml)
			runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, workflowGraphTestClients())

			_, err := runtimeRef.WorkflowRunner().Run(context.Background(), tt.name, "run it", true, nil)
			if err == nil {
				t.Fatal("expected invalid custom workflow graph error")
			}
			if strings.Contains(err.Error(), "unknown workflow") || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("expected validation error containing %q, got %v", tt.wantError, err)
			}
		})
	}
}

func TestWorkflowRunnerCustomWorkflowGraphReportsInvalidInvocationName(t *testing.T) {
	runtimeRef := newWorkflowGraphRuntime(t, t.TempDir(), workflowGraphTestSkills(), &stubRuntimeMCP{}, workflowGraphTestClients())

	_, err := runtimeRef.WorkflowRunner().Run(context.Background(), "../release-check", "run it", true, nil)
	if err == nil {
		t.Fatal("expected invalid workflow name error")
	}
	if strings.Contains(err.Error(), "unknown workflow") || !strings.Contains(err.Error(), "invalid workflow name") {
		t.Fatalf("expected invalid workflow name error, got %v", err)
	}
}

func writeWorkflowGraph(t *testing.T, runtimeHome, name, content string) {
	t.Helper()
	dir := filepath.Join(runtimeHome, "workflows", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll workflow dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "workflow.yaml"), []byte(strings.TrimSpace(content)+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile workflow graph: %v", err)
	}
}

func workflowGraphTestSkills() []schema.Skill {
	return []schema.Skill{
		{Name: "execution-plan", Description: "Plan the work", Mode: "plan", PreferredAgent: "planner", Activation: schema.Activation{Keywords: []string{"plan"}}, Instructions: "Plan."},
		{Name: "code-writing", Description: "Implement code", Mode: "fix", PreferredAgent: "fixer", Activation: schema.Activation{Keywords: []string{"implement", "code"}}, Instructions: "Implement."},
		{Name: "code-audit", Description: "Audit code", Mode: "audit", PreferredAgent: "auditor", Activation: schema.Activation{Keywords: []string{"audit", "security"}}, Instructions: "Audit."},
	}
}

func newWorkflowGraphRuntime(t *testing.T, runtimeHome string, skills []schema.Skill, mcpClient interfaces.MCPClient, clients map[string]interfaces.LLMClient) *Runtime {
	t.Helper()
	cfg := &config.Config{
		Audit:        config.AuditConfig{},
		Session:      config.SessionConfig{MaxHistory: 8},
		RuntimeHome:  runtimeHome,
		DefaultAgent: "chat",
		Agents: map[string]config.AgentProfile{
			"chat":    {Name: "Chat", Provider: "chat", Mode: "chat", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead}, ToolPolicy: config.ToolPolicyAllow},
			"planner": {Name: "Planner", Provider: "planner", Mode: "plan", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead}, ToolPolicy: config.ToolPolicyAllow},
			"fixer":   {Name: "Fixer", Provider: "fixer", Mode: "fix", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead, config.ToolKindWrite}, ToolPolicy: config.ToolPolicyConfirm},
			"auditor": {Name: "Auditor", Provider: "auditor", Mode: "audit", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead}, ToolPolicy: config.ToolPolicyAllow},
		},
	}
	runtimeRef, err := NewRuntime(cfg, clients, workflowSkillManager{skills: skills}, mcpClient, session.New(8), runtime.NewAuditLogger(false, false))
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	return runtimeRef
}

func workflowGraphTestClients() map[string]interfaces.LLMClient {
	return map[string]interfaces.LLMClient{
		"chat":    &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}},
		"planner": &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "plan"}}}},
		"fixer":   &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "fix"}}}},
		"auditor": &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "audit"}}}},
	}
}
