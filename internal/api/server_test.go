package api

import (
	"context"
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

type apiTestLLM struct {
	responses []schema.ChatResponse
	calls     int
}

func (s *apiTestLLM) Chat(context.Context, schema.ChatRequest) (schema.ChatResponse, error) {
	if s.calls >= len(s.responses) {
		return schema.ChatResponse{Message: schema.Message{Content: "done"}}, nil
	}
	resp := s.responses[s.calls]
	s.calls++
	return resp, nil
}

func (s *apiTestLLM) StreamChat(context.Context, schema.ChatRequest, interfaces.StreamHandler) (schema.ChatResponse, error) {
	if s.calls >= len(s.responses) {
		return schema.ChatResponse{Message: schema.Message{Content: "done"}}, nil
	}
	resp := s.responses[s.calls]
	s.calls++
	return resp, nil
}

func (s *apiTestLLM) Capabilities() []string { return []string{"chat", "stream"} }

type apiTestMCP struct {
	result schema.ToolResult
}

func (m *apiTestMCP) ListTools(context.Context) ([]schema.Tool, error) {
	return []schema.Tool{{
		Name:        "write_file",
		Kind:        "write",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}`),
	}}, nil
}
func (m *apiTestMCP) RefreshTools(context.Context) ([]schema.Tool, error) {
	return m.ListTools(context.Background())
}
func (m *apiTestMCP) CallTool(context.Context, string, []byte) (schema.ToolResult, error) {
	return m.result, nil
}
func (m *apiTestMCP) HealthStatus(context.Context) map[string]string {
	return map[string]string{"stub": "ready"}
}
func (m *apiTestMCP) ToolNames() []string { return []string{"write_file"} }

type apiTestSkillManager struct{}

func (apiTestSkillManager) List() []schema.Skill {
	return []schema.Skill{
		{Name: "execution-plan", Description: "Plan work", Mode: "plan", PreferredAgent: "planner"},
		{Name: "code-writing", Description: "Implement work", Mode: "fix", PreferredAgent: "fixer"},
		{Name: "code-audit", Description: "Review work", Mode: "audit", PreferredAgent: "auditor"},
	}
}
func (apiTestSkillManager) Match(string) (*schema.Skill, bool) { return nil, false }
func (apiTestSkillManager) MatchWithDiagnostics(string) (*schema.Skill, schema.SkillMatchDiagnostic, bool) {
	return nil, schema.SkillMatchDiagnostic{}, false
}

func newAPITestRuntime(t *testing.T) *agent.Runtime {
	t.Helper()
	cfg := &config.Config{
		RuntimeHome:  t.TempDir(),
		Audit:        config.AuditConfig{},
		Session:      config.SessionConfig{MaxHistory: 8},
		DefaultAgent: "chat",
		Agents: map[string]config.AgentProfile{
			"chat":    {Name: "Chat", Provider: "primary", Mode: "chat", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead, config.ToolKindWrite, config.ToolKindExec}, ToolPolicy: config.ToolPolicyConfirm},
			"planner": {Name: "Planner", Provider: "primary", Mode: "plan", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead}, ToolPolicy: config.ToolPolicyAllow},
			"fixer":   {Name: "Fixer", Provider: "primary", Mode: "fix", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindWrite}, ToolPolicy: config.ToolPolicyConfirm},
			"auditor": {Name: "Auditor", Provider: "primary", Mode: "audit", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead}, ToolPolicy: config.ToolPolicyAllow},
		},
	}
	mcpClient := &apiTestMCP{result: schema.ToolResult{CallID: "call-1", ToolName: "write_file", Content: "ok"}}
	clients := map[string]interfaces.LLMClient{
		"primary": &apiTestLLM{responses: []schema.ChatResponse{
			{Message: schema.Message{Content: "plan ready"}, Usage: schema.TokenUsage{PromptTokens: 11, OutputTokens: 3, CachedTokens: 2}},
			{Message: schema.Message{Content: "fix completed"}, Usage: schema.TokenUsage{PromptTokens: 13, OutputTokens: 4}},
			{Message: schema.Message{Content: "audit done"}, Usage: schema.TokenUsage{PromptTokens: 17, OutputTokens: 5}},
		}},
	}
	runtimeRef, err := agent.NewRuntime(cfg, clients, apiTestSkillManager{}, mcpClient, session.New(8), runtime.NewAuditLogger(false, false))
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	return runtimeRef
}

func TestServerWorkflowGraphManagementEndpointsPersistCustomGraph(t *testing.T) {
	server := NewServer(newAPITestRuntime(t))
	body := strings.NewReader(`{
		"name":"release-check",
		"description":"Plan, implement, and audit a release change.",
		"stages":[
			{"name":"plan","agent":"planner","skill":"execution-plan","next":["implement"],"position":{"x":80,"y":120}},
			{"name":"implement","agent":"fixer","skill":"code-writing","approval":true,"next":["audit"],"position":{"x":320,"y":120}},
			{"name":"audit","agent":"auditor","skill":"code-audit","position":{"x":560,"y":120}}
		]
	}`)
	request := httptest.NewRequest(http.MethodPut, "/api/workflow-graphs/release-check", body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected save 200, got %d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"name":"release-check"`) || !strings.Contains(response.Body.String(), `"x":80`) {
		t.Fatalf("expected persisted workflow graph response, got %s", response.Body.String())
	}

	listRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-graphs", nil)
	listResponse := httptest.NewRecorder()
	server.ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("expected list 200, got %d", listResponse.Code)
	}
	if !strings.Contains(listResponse.Body.String(), `"release-check"`) || !strings.Contains(listResponse.Body.String(), `"custom"`) {
		t.Fatalf("expected custom workflow in list, got %s", listResponse.Body.String())
	}

	getRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-graphs/release-check", nil)
	getResponse := httptest.NewRecorder()
	server.ServeHTTP(getResponse, getRequest)
	if getResponse.Code != http.StatusOK {
		t.Fatalf("expected get 200, got %d", getResponse.Code)
	}
	if !strings.Contains(getResponse.Body.String(), `"position"`) {
		t.Fatalf("expected visual position metadata, got %s", getResponse.Body.String())
	}
}

func TestServerWorkflowEditorPageAndOptions(t *testing.T) {
	server := NewServer(newAPITestRuntime(t))

	pageRequest := httptest.NewRequest(http.MethodGet, "/workflows", nil)
	pageResponse := httptest.NewRecorder()
	server.ServeHTTP(pageResponse, pageRequest)
	if pageResponse.Code != http.StatusOK {
		t.Fatalf("expected editor 200, got %d", pageResponse.Code)
	}
	if !strings.Contains(pageResponse.Body.String(), "GoFlow Workflow Editor") || !strings.Contains(pageResponse.Body.String(), "/api/workflow-graphs") {
		t.Fatalf("expected workflow editor HTML, got %s", pageResponse.Body.String())
	}

	optionsRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-options", nil)
	optionsResponse := httptest.NewRecorder()
	server.ServeHTTP(optionsResponse, optionsRequest)
	if optionsResponse.Code != http.StatusOK {
		t.Fatalf("expected options 200, got %d", optionsResponse.Code)
	}
	if !strings.Contains(optionsResponse.Body.String(), `"planner"`) || !strings.Contains(optionsResponse.Body.String(), `"execution-plan"`) {
		t.Fatalf("expected workflow options, got %s", optionsResponse.Body.String())
	}
}

func TestServerRunEndpointReturnsAgentResult(t *testing.T) {
	server := NewServer(newAPITestRuntime(t))
	body := strings.NewReader(`{"input":"summarize the workspace"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/run", body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), `"output"`) {
		t.Fatalf("expected agent result body, got %s", response.Body.String())
	}
}

func TestServerWorkflowEndpointReturnsWorkflowResult(t *testing.T) {
	server := NewServer(newAPITestRuntime(t))
	body := strings.NewReader(`{"input":"summarize the workspace"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/workflows/plan-fix-audit", body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), `"name":"plan-fix-audit"`) {
		t.Fatalf("expected workflow response, got %s", response.Body.String())
	}
}

func TestServerSessionEndpointReturnsSnapshot(t *testing.T) {
	server := NewServer(newAPITestRuntime(t))
	request := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), `"active_agent"`) {
		t.Fatalf("expected session snapshot, got %s", response.Body.String())
	}
}

func TestServerRunStreamEndpointEmitsStreamEvents(t *testing.T) {
	server := NewServer(newAPITestRuntime(t))
	request := httptest.NewRequest(http.MethodPost, "/api/run/stream", strings.NewReader(`{"input":"hello"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", response.Code)
	}
	body := response.Body.String()
	if !strings.Contains(body, "event: text") && !strings.Contains(body, "event: done") {
		t.Fatalf("expected SSE events, got %q", body)
	}
	if !strings.Contains(body, "event: task_stage") || !strings.Contains(body, "event: token_usage") || !strings.Contains(body, `"prompt_tokens":11`) {
		t.Fatalf("expected task stage and token usage SSE events, got %q", body)
	}
}

func TestServerWorkflowStreamEndpointEmitsWorkflowResult(t *testing.T) {
	server := NewServer(newAPITestRuntime(t))
	request := httptest.NewRequest(http.MethodPost, "/api/workflows/plan-fix-audit/stream", strings.NewReader(`{"input":"ship it"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, "event: task_stage") || !strings.Contains(body, "event: token_usage") {
		t.Fatalf("expected workflow stream to forward agent task/token events, got %q", body)
	}
	if !strings.Contains(body, "event: workflow_result") || !strings.Contains(body, `"workflow_status":"completed"`) || !strings.Contains(body, `"name":"plan-fix-audit"`) {
		t.Fatalf("expected workflow_result SSE event, got %q", body)
	}
}

func TestServerApprovalStreamResumesOrdinaryToolLoop(t *testing.T) {
	runtimeRef := newAPIApprovalStreamRuntime(t)
	server := NewServer(runtimeRef)
	runRequest := httptest.NewRequest(http.MethodPost, "/api/run/stream", strings.NewReader(`{"input":"write a file"}`))
	runRequest.Header.Set("Content-Type", "application/json")
	runResponse := httptest.NewRecorder()

	server.ServeHTTP(runResponse, runRequest)

	if runResponse.Code != http.StatusOK {
		t.Fatalf("expected initial stream 200, got %d body=%s", runResponse.Code, runResponse.Body.String())
	}
	if !strings.Contains(runResponse.Body.String(), "event: approval") {
		t.Fatalf("expected initial stream to pause for approval, got %q", runResponse.Body.String())
	}

	approvalRequest := httptest.NewRequest(http.MethodPost, "/api/approvals/call-1/approve/stream", nil)
	approvalResponse := httptest.NewRecorder()
	server.ServeHTTP(approvalResponse, approvalRequest)

	if approvalResponse.Code != http.StatusOK {
		t.Fatalf("expected approval stream 200, got %d body=%s", approvalResponse.Code, approvalResponse.Body.String())
	}
	body := approvalResponse.Body.String()
	if !strings.Contains(body, "event: tool_result") || !strings.Contains(body, "event: token_usage") || !strings.Contains(body, "event: final_message") {
		t.Fatalf("expected approval stream to emit tool result, resumed token usage, and final message, got %q", body)
	}
	if !strings.Contains(body, "done after approval") {
		t.Fatalf("expected resumed final content, got %q", body)
	}
}

func newAPIApprovalStreamRuntime(t *testing.T) *agent.Runtime {
	t.Helper()
	cfg := &config.Config{
		Audit:        config.AuditConfig{},
		Session:      config.SessionConfig{MaxHistory: 8},
		DefaultAgent: "chat",
		Agents: map[string]config.AgentProfile{
			"chat": {Name: "Chat", Provider: "primary", Mode: "chat", Model: "test-model", MaxIterations: 3, AllowedToolKinds: []config.ToolKind{config.ToolKindRead, config.ToolKindWrite}, ToolPolicy: config.ToolPolicyConfirm},
		},
	}
	mcpClient := &apiTestMCP{result: schema.ToolResult{ToolName: "write_file", Content: "created file"}}
	clients := map[string]interfaces.LLMClient{
		"primary": &apiTestLLM{responses: []schema.ChatResponse{
			{ToolCalls: []schema.ToolCall{{ID: "call-1", Name: "write_file", Arguments: json.RawMessage(`{"path":"note.txt","content":"hello"}`)}}},
			{Message: schema.Message{Content: "done after approval"}, Usage: schema.TokenUsage{PromptTokens: 7, OutputTokens: 3}},
		}},
	}
	runtimeRef, err := agent.NewRuntime(cfg, clients, apiTestSkillManager{}, mcpClient, session.New(8), runtime.NewAuditLogger(false, false))
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	return runtimeRef
}
