package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/agent"
	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/interfaces"
	"github.com/FyMatt/GoFlow-Agent/internal/runtime"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

type integrityTestMCP struct {
	*apiTestMCP
	upserts, removals int
}

func (m *integrityTestMCP) UpsertServer(config.MCPServerRef) error { m.upserts++; return nil }
func (m *integrityTestMCP) RemoveServer(string) error              { m.removals++; return nil }

func newIntegrityTestServer(t *testing.T) (*Server, *session.State, *apiGateLLM, *integrityTestMCP) {
	t.Helper()
	state := session.New(8)
	model := &apiGateLLM{started: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(func() {
		select {
		case <-model.release:
		default:
			close(model.release)
		}
	})
	mcp := &integrityTestMCP{apiTestMCP: &apiTestMCP{tools: []schema.Tool{{Name: "read", Server: "guarded", Kind: "read"}}, health: map[string]string{"guarded": "ready"}}}
	profile := config.AgentProfile{Name: "Chat", Provider: "primary", Mode: "chat", Model: "stub", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead}, ToolPolicy: config.ToolPolicyConfirm}
	cfg := &config.Config{RuntimeHome: t.TempDir(), WorkspaceRoot: t.TempDir(), DefaultAgent: "chat", Providers: map[string]config.LLMConfig{"primary": {Provider: "openai-compatible", BaseURL: "http://127.0.0.1:9999/v1", Model: "stub"}}, Agents: map[string]config.AgentProfile{"chat": profile, "planner": profile}, MCP: []config.MCPServerRef{{Name: "guarded", Command: "unused-local-stub", Enabled: true}}}
	rt, err := agent.NewRuntime(cfg, map[string]interfaces.LLMClient{"primary": model}, apiTestSkillManager{}, mcp, state, runtime.NewAuditLogger(false, false))
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(rt)
	if _, err := server.saveMCPResource(mcpResourceDocument{Name: "guarded", Command: "unused-local-stub", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	return server, state, model, mcp
}

func TestResourceIntegrityMCPInUseRejectedBeforeWriteOrClientSwap(t *testing.T) {
	server, _, model, mcp := newIntegrityTestServer(t)
	path, err := server.mcpResourcePath("guarded")
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	response := kitRoutingRequest(t, server, http.MethodPost, "/api/runs", map[string]any{"input": "inspect resources", "agent": "chat", "skill": "execution-plan", "tool": "guarded/read", "background": true})
	if response.Code != http.StatusAccepted {
		t.Fatalf("start run: %d %s", response.Code, response.Body.String())
	}
	var accepted agentRunBackgroundResponse
	if err := json.Unmarshal(response.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	select {
	case <-model.started:
	case <-time.After(3 * time.Second):
		t.Fatal("local model did not start")
	}
	upserts := mcp.upserts
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		response = kitRoutingRequest(t, server, method, "/api/resources/mcp/guarded", map[string]any{"command": "replacement-stub", "enabled": true})
		if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "resource_in_use") {
			t.Fatalf("live MCP mutation: %d %s", response.Code, response.Body.String())
		}
		current, err := os.ReadFile(path)
		if err != nil || string(current) != string(original) {
			t.Fatal("conflict modified MCP configuration")
		}
		if mcp.upserts != upserts || mcp.removals != 0 {
			t.Fatal("conflict replaced/removed the runtime MCP client")
		}
	}
	response = kitRoutingRequest(t, server, http.MethodPut, "/api/resources/mcp/unrelated", map[string]any{"command": "unrelated-stub", "enabled": true})
	if response.Code != http.StatusOK || mcp.upserts != upserts+1 {
		t.Fatalf("unrelated install blocked: %d %s", response.Code, response.Body.String())
	}
	response = kitRoutingRequest(t, server, http.MethodPut, "/api/resources/agents/chat", agentResourceDocument{ID: "chat", Name: "Changed", Provider: "primary", Mode: "chat", Model: "stub"})
	if response.Code != http.StatusConflict {
		t.Fatalf("running Agent update: %d %s", response.Code, response.Body.String())
	}
	response = kitRoutingRequest(t, server, http.MethodPut, "/api/resources/skills/execution-plan", skillResourceDocument{Name: "execution-plan"})
	if response.Code != http.StatusConflict {
		t.Fatalf("running Skill update: %d %s", response.Code, response.Body.String())
	}
	if _, err := os.Stat(filepath.Join(server.runtime.RuntimeHome(), "skills", "execution-plan", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatal("Skill conflict wrote a file")
	}
	close(model.release)
	waitAgentRunStatus(t, server.runtime, accepted.RunID, "completed")
	response = kitRoutingRequest(t, server, http.MethodPut, "/api/resources/mcp/guarded", map[string]any{"command": "replacement-stub", "enabled": true})
	if response.Code != http.StatusOK {
		t.Fatalf("completed run still blocked MCP edit: %d %s", response.Code, response.Body.String())
	}
}

func TestResourceIntegrityPausedRunsAllowEditsButRejectDriftOnResume(t *testing.T) {
	server, state, _, _ := newIntegrityTestServer(t)
	pins := server.agentTaskResourcePins("chat", "execution-plan", "guarded/read")
	var ids []string
	for i := 0; i < 5; i++ {
		id := state.StartAgentRunWithOptions("paused task", session.AgentRunStartOptions{AgentID: "chat", ResourcePins: pins})
		state.CompleteAgentRun(id, "paused_need_more_budget", schema.AgentResult{})
		ids = append(ids, id)
	}
	if err := server.ensureResourcePinsCurrent(pins); err != nil {
		t.Fatalf("unchanged pins rejected: %v", err)
	}
	response := kitRoutingRequest(t, server, http.MethodPut, "/api/resources/mcp/guarded", map[string]any{"command": "changed-stub", "enabled": true})
	if response.Code != http.StatusOK {
		t.Fatalf("old paused runs blocked edit: %d %s", response.Code, response.Body.String())
	}
	for _, action := range []string{"continue_output", "approve_tool", "retry"} {
		response = kitRoutingRequest(t, server, http.MethodPost, "/api/runs/"+ids[0]+"/actions/"+action, nil)
		if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "resource_pin_drift") || !strings.Contains(response.Body.String(), "guarded") {
			t.Fatalf("resume silently used changed resources: %d %s", response.Code, response.Body.String())
		}
	}
	if current, _ := server.runtime.AgentRun(ids[0]); current.Status != "paused_need_more_budget" {
		t.Fatalf("conflict changed paused run state: %#v", current)
	}
	response = kitRoutingRequest(t, server, http.MethodPost, "/api/runs/"+ids[0]+"/actions/cancel", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("drift blocked cancellation: %d %s", response.Code, response.Body.String())
	}
}

func TestResourceIntegrityWorkflowEditsAndPausedResume(t *testing.T) {
	server, state, _, _ := newIntegrityTestServer(t)
	graph := agent.WorkflowGraphDocument{Name: "guarded-flow", Stages: []agent.WorkflowGraphStageDocument{{Name: "inspect", Agent: "chat", Skill: "execution-plan", Params: map[string]string{"tools": "guarded/read"}}}}
	if err := server.saveWorkflowGraphResource(graph.Name, graph); err != nil {
		t.Fatal(err)
	}
	pins := server.workflowTaskResourcePins(graph.Name)
	if resourcePinByKindAndName(pins, "mcp", "guarded") == nil {
		t.Fatal("stage params tool server not pinned")
	}
	id := state.StartWorkflowRunWithOptions(graph.Name, "inspect", session.WorkflowRunStartOptions{ResourcePins: pins})
	path := filepath.Join(server.runtime.RuntimeHome(), "workflows", graph.Name, "workflow.yaml")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		response := kitRoutingRequest(t, server, method, "/api/workflow-graphs/"+graph.Name, graph)
		if response.Code != http.StatusConflict {
			t.Fatalf("running workflow mutation: %d %s", response.Code, response.Body.String())
		}
		current, err := os.ReadFile(path)
		if err != nil || string(current) != string(original) {
			t.Fatal("workflow conflict changed files")
		}
	}
	state.UpdateWorkflowRunState(session.WorkflowSnapshot{RunID: id, Name: graph.Name, Status: "awaiting_approval", ResourcePins: pins})
	graph.Description = "changed while paused"
	response := kitRoutingRequest(t, server, http.MethodPut, "/api/workflow-graphs/"+graph.Name, graph)
	if response.Code != http.StatusOK {
		t.Fatalf("paused workflow blocked edit: %d %s", response.Code, response.Body.String())
	}
	for _, action := range []string{"approve", "input", "continue-output"} {
		response = kitRoutingRequest(t, server, http.MethodPost, "/api/workflow-runs/"+id+"/"+action, nil)
		if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "resource_pin_drift") {
			t.Fatalf("paused workflow drift: %d %s", response.Code, response.Body.String())
		}
	}
}
