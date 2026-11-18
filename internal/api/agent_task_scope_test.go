package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/agent"
	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/interfaces"
	"github.com/FyMatt/GoFlow-Agent/internal/runtime"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestServerAgentTaskScopeNeedsOneApprovalForWritesAndValidation(t *testing.T) {
	writeTool := schema.Tool{
		Name:        "write_file",
		Server:      "file_tools",
		Kind:        string(config.ToolKindWrite),
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`),
	}
	commandTool := schema.Tool{
		Name:        "run_command",
		Server:      "command_tools",
		Kind:        string(config.ToolKindExec),
		InputSchema: json.RawMessage(`{"type":"object","properties":{"program":{"type":"string"},"args":{"type":"array","items":{"type":"string"}}},"required":["program"],"additionalProperties":false}`),
	}
	responses := []schema.ChatResponse{
		{ToolCalls: []schema.ToolCall{{ID: "call-write-1", Name: "write_file", Arguments: json.RawMessage(`{"path":"one.txt","content":"one"}`)}}},
		{ToolCalls: []schema.ToolCall{
			{ID: "call-write-2", Name: "write_file", Arguments: json.RawMessage(`{"path":"two.txt","content":"two"}`)},
			{ID: "call-test", Name: "run_command", Arguments: json.RawMessage(`{"program":"go","args":["test","./..."]}`)},
		}},
		{Message: schema.Message{Content: "done"}},
	}
	state := session.New(8)
	cfg := &config.Config{
		RuntimeHome:   t.TempDir(),
		WorkspaceRoot: t.TempDir(),
		Session:       config.SessionConfig{MaxHistory: 8},
		DefaultAgent:  "chat",
		ToolRiskPolicy: config.ToolRiskPolicyConfig{
			RequireApprovalForUnsandboxedRiskyTools: true,
			DisableRememberForUnsandboxedRiskyTools: true,
		},
		Providers: map[string]config.LLMConfig{
			"primary": {Provider: "openai-compatible", BaseURL: "http://localhost:9999/v1", Model: "test-model"},
		},
		Agents: map[string]config.AgentProfile{
			"chat": {
				Name:             "Chat",
				Provider:         "primary",
				Mode:             "fix",
				Model:            "test-model",
				MaxIterations:    4,
				AllowedToolKinds: []config.ToolKind{config.ToolKindRead, config.ToolKindWrite, config.ToolKindExec},
				ToolPolicy:       config.ToolPolicyConfirm,
			},
		},
		MCP: []config.MCPServerRef{
			{Name: "file_tools", Isolation: "process_group"},
			{Name: "command_tools", Isolation: "process_group"},
		},
	}
	mcpClient := &apiTestMCP{result: schema.ToolResult{Content: "ok"}, tools: []schema.Tool{writeTool, commandTool}}
	runtimeRef, err := agent.NewRuntime(
		cfg,
		map[string]interfaces.LLMClient{"primary": &apiTestLLM{responses: responses}},
		apiTestSkillManager{},
		mcpClient,
		state,
		runtime.NewAuditLogger(true, false),
	)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	server := NewServer(runtimeRef)

	startRequest := httptest.NewRequest(http.MethodPost, "/api/runs", strings.NewReader(`{"input":"write and test","agent":"chat","background":true}`))
	startRequest.Header.Set("Content-Type", "application/json")
	startResponse := httptest.NewRecorder()
	server.ServeHTTP(startResponse, startRequest)
	if startResponse.Code != http.StatusAccepted {
		t.Fatalf("expected background agent 202, got %d body=%s", startResponse.Code, startResponse.Body.String())
	}
	var accepted agentRunBackgroundResponse
	if err := json.NewDecoder(startResponse.Body).Decode(&accepted); err != nil {
		t.Fatalf("decode accepted run: %v", err)
	}
	paused := waitAgentRunActionAvailable(t, server, runtimeRef, accepted.RunID, "approve_task_scope", true)
	actions := server.agentRunActions(paused)
	taskAction := agentRunActionByName(actions, "approve_task_scope")
	if taskAction == nil || taskAction.Path != "/api/agent-runs/"+accepted.RunID+"/approve-task" || taskAction.StreamPath == "" {
		t.Fatalf("expected durable task approval action, got %#v", taskAction)
	}
	if recommendedAgentRunAction(actions) != "approve_task_scope" {
		t.Fatalf("expected bounded task scope to be recommended, got %#v", actions)
	}

	approveRequest := httptest.NewRequest(http.MethodPost, taskAction.Path, nil)
	approveResponse := httptest.NewRecorder()
	server.ServeHTTP(approveResponse, approveRequest)
	if approveResponse.Code != http.StatusOK {
		t.Fatalf("expected task approval 200, got %d body=%s", approveResponse.Code, approveResponse.Body.String())
	}
	completed := waitAgentRunStatus(t, runtimeRef, accepted.RunID, "completed")
	if completed.TaskApproval == nil || completed.TaskApproval.GrantedByCallID != "call-write-1" {
		t.Fatalf("expected auditable task approval snapshot, got %#v", completed.TaskApproval)
	}
	if len(completed.PendingApprovals) != 0 || len(runtimeRef.PendingApprovals()) != 0 {
		t.Fatalf("expected no repeated approval after task grant, run=%#v pending=%#v", completed.PendingApprovals, runtimeRef.PendingApprovals())
	}
	if mcpClient.calls != 3 {
		t.Fatalf("expected first write, second write, and test to execute after one approval, got %d calls", mcpClient.calls)
	}
}
