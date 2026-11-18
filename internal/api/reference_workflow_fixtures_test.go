package api

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/agent"
	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/interfaces"
	"github.com/FyMatt/GoFlow-Agent/internal/runtime"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
	"gopkg.in/yaml.v3"
)

func TestReferenceWorkflowDryRunFixturesPassRuntimePreflight(t *testing.T) {
	runtimeRef := newReferenceWorkflowFixtureRuntime(t, true)
	server := NewServer(runtimeRef)
	runner := runtimeRef.WorkflowRunner()

	for _, name := range []string{"plan-fix-audit", "complex-project-delivery", "repository-readiness-review"} {
		t.Run(name, func(t *testing.T) {
			fixture := referenceWorkflowDryRunFixture(name)
			if fixture == nil || fixture.Method != "POST" || fixture.Path != "/api/runs/preflight" || fixture.Request.Input == "" {
				t.Fatalf("expected reusable dry-run fixture, got %#v", fixture)
			}
			template, ok := runner.WorkflowTemplate(name)
			if !ok {
				t.Fatalf("expected bundled workflow template %s", name)
			}
			for _, stage := range fixture.ExpectedStages {
				if !referenceWorkflowHasStage(template.Graph, stage) {
					t.Fatalf("fixture expects missing stage %s in %s", stage, name)
				}
			}
			if fixture.ReadOnly && referenceWorkflowHasWriteTool(template.Graph) {
				t.Fatalf("read-only fixture %s exposes a write tool", name)
			}
			if err := runner.SaveWorkflowGraphDocument(name, template.Graph); err != nil {
				t.Fatalf("materialize reference workflow: %v", err)
			}
			result := server.taskPreflight(context.Background(), fixture.Request)
			if !result.Valid {
				t.Fatalf("reference workflow dry-run failed: %#v", result.Issues)
			}
			item := server.workflowCatalogItem(agent.WorkflowGraphSummary{Name: name, Description: template.Description, Valid: true, Stages: len(template.Graph.Stages)})
			if item.DryRunFixture == nil || item.DryRunFixture.Request.Workflow != name || item.Version == "" || item.ValidationStatus != "valid" {
				t.Fatalf("expected catalog dry-run metadata for %s, got %#v", name, item)
			}
		})
	}
}

func TestReferenceWorkflowCatalogExplainsMissingDependencyRepair(t *testing.T) {
	runtimeRef := newReferenceWorkflowFixtureRuntime(t, false)
	server := NewServer(runtimeRef)
	runner := runtimeRef.WorkflowRunner()
	template, ok := runner.WorkflowTemplate("repository-readiness-review")
	if !ok {
		t.Fatal("expected repository readiness template")
	}
	if err := runner.SaveWorkflowGraphDocument(template.Name, template.Graph); err != nil {
		t.Fatalf("materialize repository readiness workflow: %v", err)
	}
	item := server.workflowCatalogItem(agent.WorkflowGraphSummary{Name: template.Name, Description: template.Description, Valid: true, Stages: len(template.Graph.Stages)})
	if item.Health != "error" {
		t.Fatalf("expected missing required tool to make workflow unavailable, got %#v", item)
	}
	var found bool
	for _, issue := range item.Issues {
		if issue.Code == "dependency_missing" && strings.Contains(issue.Field, "command_tools/run_command") && strings.Contains(issue.Recommendation, "Enable the MCP connection") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected actionable command tool repair guidance, got %#v", item.Issues)
	}
}

func TestReferenceWorkflowPreflightWithShippedQualityProfile(t *testing.T) {
	runtimeRef := newReferenceWorkflowFixtureRuntime(t, true)
	data, err := os.ReadFile(filepath.Join("..", "..", "configs", "agents", "quality-engineer.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	profiles := map[string]config.AgentProfile{}
	if err := yaml.Unmarshal(data, &profiles); err != nil {
		t.Fatal(err)
	}
	if err := runtimeRef.UpsertAgentProfile("quality-engineer", profiles["quality-engineer"]); err != nil {
		t.Fatal(err)
	}
	server := NewServer(runtimeRef)
	for _, name := range []string{"complex-project-delivery", "repository-readiness-review"} {
		template, ok := runtimeRef.WorkflowRunner().WorkflowTemplate(name)
		if !ok {
			t.Fatal(name)
		}
		if err := runtimeRef.WorkflowRunner().SaveWorkflowGraphDocument(name, template.Graph); err != nil {
			t.Fatal(err)
		}
		result := server.taskPreflight(t.Context(), referenceWorkflowDryRunFixture(name).Request)
		if !result.Valid {
			t.Fatalf("shipped quality profile cannot run %s: %#v", name, result.Issues)
		}
	}
}

func newReferenceWorkflowFixtureRuntime(t *testing.T, includeCommandTool bool) *agent.Runtime {
	t.Helper()
	runtimeHome := t.TempDir()
	workspaceRoot := t.TempDir()
	allKinds := []config.ToolKind{config.ToolKindRead, config.ToolKindWrite, config.ToolKindExec, config.ToolKindNetwork, config.ToolKindUnknown}
	profiles := map[string]config.AgentProfile{}
	for _, name := range []string{"chat", "planner", "fixer", "auditor", "software-engineer", "quality-engineer"} {
		profiles[name] = config.AgentProfile{Name: name, Provider: "primary", Mode: "chat", Model: "fixture-model", MaxIterations: 4, AllowedToolKinds: append([]config.ToolKind(nil), allKinds...), ToolPolicy: config.ToolPolicyAllow}
	}
	tools := []schema.Tool{
		{Name: "list_tree", Server: "file_tools", Kind: "read"},
		{Name: "read_file", Server: "file_tools", Kind: "read"},
		{Name: "read_file_range", Server: "file_tools", Kind: "read"},
		{Name: "read_files", Server: "file_tools", Kind: "read"},
		{Name: "read_file_outline", Server: "file_tools", Kind: "read"},
		{Name: "search_files", Server: "file_tools", Kind: "read"},
		{Name: "repository_map", Server: "file_tools", Kind: "read"},
		{Name: "apply_patch", Server: "file_tools", Kind: "write"},
		{Name: "apply_edits", Server: "file_tools", Kind: "write"},
		{Name: "write_file", Server: "file_tools", Kind: "write"},
		{Name: "mkdir", Server: "file_tools", Kind: "write"},
		{Name: "web_search", Server: "web_tools", Kind: "network"},
		{Name: "fetch_url", Server: "web_tools", Kind: "network"},
		{Name: "research_snapshot", Server: "web_tools", Kind: "network"},
		{Name: "browser_snapshot", Server: "web_tools", Kind: "network"},
	}
	if includeCommandTool {
		tools = append(tools, schema.Tool{Name: "run_command", Server: "command_tools", Kind: "exec"})
		tools = append(tools, schema.Tool{Name: "start_process", Server: "command_tools", Kind: "exec"}, schema.Tool{Name: "process_status", Server: "command_tools", Kind: "read"}, schema.Tool{Name: "stop_process", Server: "command_tools", Kind: "exec"})
	}
	mcpRefs := []config.MCPServerRef{{Name: "file_tools", Command: "go", Enabled: true}, {Name: "web_tools", Command: "go", Enabled: true}}
	health := map[string]string{"file_tools": "ready", "web_tools": "ready"}
	if includeCommandTool {
		mcpRefs = append(mcpRefs, config.MCPServerRef{Name: "command_tools", Command: "go", Enabled: true})
		health["command_tools"] = "ready"
	}
	mcpClient := &apiTestMCP{tools: tools, health: health}
	cfg := &config.Config{
		RuntimeHome:   runtimeHome,
		WorkspaceRoot: workspaceRoot,
		DefaultAgent:  "chat",
		Session:       config.SessionConfig{MaxHistory: 8},
		Providers: map[string]config.LLMConfig{
			"primary": {Provider: "openai-compatible", BaseURL: "http://127.0.0.1:9999/v1", APIKey: "fixture-key", Model: "fixture-model"},
		},
		Agents: profiles,
		MCP:    mcpRefs,
	}
	runtimeRef, err := agent.NewRuntime(cfg, map[string]interfaces.LLMClient{"primary": &apiTestLLM{}}, apiTestSkillManager{}, mcpClient, session.New(8), runtime.NewAuditLogger(false, false))
	if err != nil {
		t.Fatalf("create reference workflow fixture runtime: %v", err)
	}
	return runtimeRef
}

func referenceWorkflowHasStage(doc agent.WorkflowGraphDocument, name string) bool {
	for _, stage := range doc.Stages {
		if stage.Name == name {
			return true
		}
	}
	return false
}

func referenceWorkflowHasWriteTool(doc agent.WorkflowGraphDocument) bool {
	for _, stage := range doc.Stages {
		for _, tool := range strings.Split(stage.Params["tools"], ",") {
			if strings.Contains(strings.ToLower(strings.TrimSpace(tool)), "apply_patch") || strings.Contains(strings.ToLower(strings.TrimSpace(tool)), "write") {
				return true
			}
		}
	}
	return false
}
