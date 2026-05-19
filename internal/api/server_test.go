package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/agent"
	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/interfaces"
	"github.com/FyMatt/GoFlow-Agent/internal/memory"
	"github.com/FyMatt/GoFlow-Agent/internal/runtime"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/internal/version"
	"github.com/FyMatt/GoFlow-Agent/internal/workspace"
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

type apiBlockingLLM struct {
	started chan struct{}
}

func (s *apiBlockingLLM) Chat(ctx context.Context, _ schema.ChatRequest) (schema.ChatResponse, error) {
	return s.StreamChat(ctx, schema.ChatRequest{}, nil)
}

func (s *apiBlockingLLM) StreamChat(ctx context.Context, _ schema.ChatRequest, _ interfaces.StreamHandler) (schema.ChatResponse, error) {
	select {
	case <-s.started:
	default:
		close(s.started)
	}
	<-ctx.Done()
	return schema.ChatResponse{}, ctx.Err()
}

func (s *apiBlockingLLM) Capabilities() []string { return []string{"chat", "stream"} }

type apiGateLLM struct {
	started   chan struct{}
	release   chan struct{}
	responses []schema.ChatResponse
	calls     int
}

func (s *apiGateLLM) Chat(ctx context.Context, req schema.ChatRequest) (schema.ChatResponse, error) {
	return s.StreamChat(ctx, req, nil)
}

func (s *apiGateLLM) StreamChat(ctx context.Context, _ schema.ChatRequest, _ interfaces.StreamHandler) (schema.ChatResponse, error) {
	if s.calls == 0 {
		select {
		case <-s.started:
		default:
			close(s.started)
		}
		select {
		case <-ctx.Done():
			return schema.ChatResponse{}, ctx.Err()
		case <-s.release:
		}
	}
	if s.calls >= len(s.responses) {
		s.calls++
		return schema.ChatResponse{Message: schema.Message{Content: "done"}}, nil
	}
	resp := s.responses[s.calls]
	s.calls++
	return resp, nil
}

func (s *apiGateLLM) Capabilities() []string { return []string{"chat", "stream"} }

type apiTestMCP struct {
	result    schema.ToolResult
	calls     int
	toolNames []string
	metrics   map[string]interfaces.MCPServerCallMetrics
}

func (m *apiTestMCP) ListTools(context.Context) ([]schema.Tool, error) {
	return []schema.Tool{{
		Name:        "write_file",
		Server:      "stub",
		Kind:        "write",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}`),
	}}, nil
}
func (m *apiTestMCP) RefreshTools(context.Context) ([]schema.Tool, error) {
	return m.ListTools(context.Background())
}
func (m *apiTestMCP) CallTool(context.Context, string, []byte) (schema.ToolResult, error) {
	m.calls++
	return m.result, nil
}
func (m *apiTestMCP) HealthStatus(context.Context) map[string]string {
	return map[string]string{"stub": "ready"}
}
func (m *apiTestMCP) MCPCallMetrics() map[string]interfaces.MCPServerCallMetrics {
	if len(m.metrics) == 0 {
		return nil
	}
	out := make(map[string]interfaces.MCPServerCallMetrics, len(m.metrics))
	for name, metric := range m.metrics {
		out[name] = metric
	}
	return out
}
func (m *apiTestMCP) SetMetrics(metrics map[string]interfaces.MCPServerCallMetrics) {
	m.metrics = metrics
}
func (m *apiTestMCP) ToolNames() []string {
	if len(m.toolNames) > 0 {
		return append([]string(nil), m.toolNames...)
	}
	return []string{"write_file"}
}

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
	return newAPITestRuntimeWithStateAndResponses(t, session.New(8), []schema.ChatResponse{
		{Message: schema.Message{Content: "plan ready"}, Usage: schema.TokenUsage{PromptTokens: 11, OutputTokens: 3, CachedTokens: 2}},
		{Message: schema.Message{Content: "fix completed"}, Usage: schema.TokenUsage{PromptTokens: 13, OutputTokens: 4}},
		{Message: schema.Message{Content: "audit done"}, Usage: schema.TokenUsage{PromptTokens: 17, OutputTokens: 5}},
	})
}

func newAPITestRuntimeWithStateAndResponses(t *testing.T, state *session.State, responses []schema.ChatResponse) *agent.Runtime {
	t.Helper()
	return newAPITestRuntimeWithStateHomeAndResponses(t, state, "", responses)
}

func newAPITestRuntimeWithStateHomeAndResponses(t *testing.T, state *session.State, runtimeHome string, responses []schema.ChatResponse) *agent.Runtime {
	t.Helper()
	return newAPITestRuntimeWithStateHomeResponsesAndMCP(t, state, runtimeHome, responses, nil)
}

func newAPITestRuntimeWithStateHomeResponsesAndMCP(t *testing.T, state *session.State, runtimeHome string, responses []schema.ChatResponse, mcpRefs []config.MCPServerRef) *agent.Runtime {
	t.Helper()
	return newAPITestRuntimeWithStateHomeResponsesMCPAndCostControl(t, state, runtimeHome, responses, mcpRefs, config.CostControlConfig{})
}

func newAPITestRuntimeWithStateHomeResponsesMCPAndCostControl(t *testing.T, state *session.State, runtimeHome string, responses []schema.ChatResponse, mcpRefs []config.MCPServerRef, costControl config.CostControlConfig) *agent.Runtime {
	t.Helper()
	return newAPITestRuntimeWithStateHomeResponsesMCPCostAndRiskPolicy(t, state, runtimeHome, responses, mcpRefs, costControl, config.ToolRiskPolicyConfig{})
}

func newAPITestRuntimeWithStateHomeResponsesMCPCostAndRiskPolicy(t *testing.T, state *session.State, runtimeHome string, responses []schema.ChatResponse, mcpRefs []config.MCPServerRef, costControl config.CostControlConfig, riskPolicy config.ToolRiskPolicyConfig) *agent.Runtime {
	t.Helper()
	return newAPITestRuntimeWithStateHomeResponsesMCPCostRiskPolicyAndExtraAgents(t, state, runtimeHome, responses, mcpRefs, costControl, riskPolicy, nil)
}

func newAPITestRuntimeWithDomainResources(t *testing.T) *agent.Runtime {
	t.Helper()
	return newAPITestRuntimeWithStateHomeResponsesMCPCostRiskPolicyAndExtraAgents(t, session.New(8), "", []schema.ChatResponse{
		{Message: schema.Message{Content: "plan ready"}, Usage: schema.TokenUsage{PromptTokens: 11, OutputTokens: 3, CachedTokens: 2}},
		{Message: schema.Message{Content: "fix completed"}, Usage: schema.TokenUsage{PromptTokens: 13, OutputTokens: 4}},
		{Message: schema.Message{Content: "audit done"}, Usage: schema.TokenUsage{PromptTokens: 17, OutputTokens: 5}},
	}, nil, config.CostControlConfig{}, config.ToolRiskPolicyConfig{}, apiTestDomainAgentProfiles())
}

func newAPITestRuntimeWithStateHomeResponsesMCPCostRiskPolicyAndExtraAgents(t *testing.T, state *session.State, runtimeHome string, responses []schema.ChatResponse, mcpRefs []config.MCPServerRef, costControl config.CostControlConfig, riskPolicy config.ToolRiskPolicyConfig, extraAgents map[string]config.AgentProfile) *agent.Runtime {
	t.Helper()
	if state == nil {
		state = session.New(8)
	}
	if strings.TrimSpace(runtimeHome) == "" {
		runtimeHome = t.TempDir()
	}
	workspaceRoot := t.TempDir()
	cfg := &config.Config{
		RuntimeHome:    runtimeHome,
		WorkspaceRoot:  workspaceRoot,
		ConfigPath:     filepath.Join(runtimeHome, "configs", "goflow.yaml"),
		Audit:          config.AuditConfig{},
		CostControl:    costControl,
		ToolRiskPolicy: riskPolicy,
		Session:        config.SessionConfig{MaxHistory: 8},
		DefaultAgent:   "chat",
		Providers: map[string]config.LLMConfig{
			"primary": {Provider: "openai-compatible", BaseURL: "http://localhost:9999/v1", Model: "test-model"},
		},
		Agents: map[string]config.AgentProfile{
			"chat":    {Name: "Chat", Provider: "primary", Mode: "chat", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead, config.ToolKindWrite, config.ToolKindExec}, ToolPolicy: config.ToolPolicyConfirm},
			"planner": {Name: "Planner", Provider: "primary", Mode: "plan", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead}, ToolPolicy: config.ToolPolicyAllow},
			"fixer":   {Name: "Fixer", Provider: "primary", Mode: "fix", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindWrite}, ToolPolicy: config.ToolPolicyConfirm},
			"auditor": {Name: "Auditor", Provider: "primary", Mode: "audit", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead}, ToolPolicy: config.ToolPolicyAllow},
		},
		MCP: append([]config.MCPServerRef(nil), mcpRefs...),
	}
	for name, profile := range extraAgents {
		cfg.Agents[name] = profile
	}
	mcpClient := &apiTestMCP{
		result: schema.ToolResult{CallID: "call-1", ToolName: "write_file", Content: "ok"},
		toolNames: []string{
			"write_file",
			"file_tools/read_file",
			"file_tools/search_files",
			"file_tools/write_file",
			"web_tools/web_search",
			"web_tools/fetch_url",
			"web_tools/fetch_page_assets",
			"python_notes/binary_file_info",
			"python_notes/binary_strings",
			"python_notes/hex_preview",
		},
	}
	clients := map[string]interfaces.LLMClient{
		"primary": &apiTestLLM{responses: responses},
	}
	runtimeRef, err := agent.NewRuntime(cfg, clients, apiTestSkillManager{}, mcpClient, state, runtime.NewAuditLogger(false, false))
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	return runtimeRef
}

func apiTestDomainAgentProfiles() map[string]config.AgentProfile {
	return map[string]config.AgentProfile{
		"software-engineer":             {Name: "Software Engineer", Provider: "primary", Mode: "fix", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead, config.ToolKindWrite}, ToolPolicy: config.ToolPolicyConfirm},
		"security-researcher":           {Name: "Security Researcher", Provider: "primary", Mode: "audit", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead, config.ToolKindNetwork}, ToolPolicy: config.ToolPolicyConfirm},
		"web-security-researcher":       {Name: "Web Security Researcher", Provider: "primary", Mode: "audit", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead, config.ToolKindNetwork}, ToolPolicy: config.ToolPolicyConfirm},
		"binary-analyst":                {Name: "Binary Analyst", Provider: "primary", Mode: "audit", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead, config.ToolKindUnknown}, ToolPolicy: config.ToolPolicyConfirm},
		"documentation-specialist":      {Name: "Documentation Specialist", Provider: "primary", Mode: "fix", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead, config.ToolKindWrite}, ToolPolicy: config.ToolPolicyConfirm},
		"operations-specialist":         {Name: "Operations Specialist", Provider: "primary", Mode: "plan", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead, config.ToolKindWrite}, ToolPolicy: config.ToolPolicyConfirm},
		"support-specialist":            {Name: "Support Specialist", Provider: "primary", Mode: "chat", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead, config.ToolKindNetwork}, ToolPolicy: config.ToolPolicyConfirm},
		"framework-extension-architect": {Name: "Framework Extension Architect", Provider: "primary", Mode: "plan", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead, config.ToolKindWrite}, ToolPolicy: config.ToolPolicyConfirm},
	}
}

func newAPICancellableWorkflowRuntime(t *testing.T) (*agent.Runtime, *apiBlockingLLM) {
	t.Helper()
	cfg := &config.Config{
		RuntimeHome:  t.TempDir(),
		Audit:        config.AuditConfig{},
		Session:      config.SessionConfig{MaxHistory: 8},
		DefaultAgent: "chat",
		Providers: map[string]config.LLMConfig{
			"primary": {Provider: "openai-compatible", BaseURL: "http://localhost:9999/v1", Model: "test-model"},
		},
		Agents: map[string]config.AgentProfile{
			"chat":    {Name: "Chat", Provider: "primary", Mode: "chat", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead, config.ToolKindWrite, config.ToolKindExec}, ToolPolicy: config.ToolPolicyConfirm},
			"planner": {Name: "Planner", Provider: "primary", Mode: "plan", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead}, ToolPolicy: config.ToolPolicyAllow},
			"fixer":   {Name: "Fixer", Provider: "primary", Mode: "fix", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindWrite}, ToolPolicy: config.ToolPolicyConfirm},
			"auditor": {Name: "Auditor", Provider: "primary", Mode: "audit", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead}, ToolPolicy: config.ToolPolicyAllow},
		},
	}
	llm := &apiBlockingLLM{started: make(chan struct{})}
	clients := map[string]interfaces.LLMClient{"primary": llm}
	mcpClient := &apiTestMCP{result: schema.ToolResult{CallID: "call-1", ToolName: "write_file", Content: "ok"}}
	runtimeRef, err := agent.NewRuntime(cfg, clients, apiTestSkillManager{}, mcpClient, session.New(8), runtime.NewAuditLogger(false, false))
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	return runtimeRef, llm
}

func newAPIBackgroundWorkflowRuntime(t *testing.T) (*agent.Runtime, *apiGateLLM) {
	t.Helper()
	cfg := &config.Config{
		RuntimeHome:  t.TempDir(),
		Audit:        config.AuditConfig{},
		Session:      config.SessionConfig{MaxHistory: 8},
		DefaultAgent: "chat",
		Providers: map[string]config.LLMConfig{
			"primary": {Provider: "openai-compatible", BaseURL: "http://localhost:9999/v1", Model: "test-model"},
		},
		Agents: map[string]config.AgentProfile{
			"chat":    {Name: "Chat", Provider: "primary", Mode: "chat", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead, config.ToolKindWrite, config.ToolKindExec}, ToolPolicy: config.ToolPolicyConfirm},
			"planner": {Name: "Planner", Provider: "primary", Mode: "plan", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead}, ToolPolicy: config.ToolPolicyAllow},
			"fixer":   {Name: "Fixer", Provider: "primary", Mode: "fix", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindWrite}, ToolPolicy: config.ToolPolicyConfirm},
			"auditor": {Name: "Auditor", Provider: "primary", Mode: "audit", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead}, ToolPolicy: config.ToolPolicyAllow},
		},
	}
	llm := &apiGateLLM{
		started: make(chan struct{}),
		release: make(chan struct{}),
		responses: []schema.ChatResponse{
			{Message: schema.Message{Content: "background plan"}, Usage: schema.TokenUsage{PromptTokens: 3, OutputTokens: 1}},
			{Message: schema.Message{Content: "background fix"}, Usage: schema.TokenUsage{PromptTokens: 5, OutputTokens: 2}},
			{Message: schema.Message{Content: "background audit"}, Usage: schema.TokenUsage{PromptTokens: 7, OutputTokens: 3}},
		},
	}
	clients := map[string]interfaces.LLMClient{"primary": llm}
	mcpClient := &apiTestMCP{result: schema.ToolResult{CallID: "call-1", ToolName: "write_file", Content: "ok"}}
	runtimeRef, err := agent.NewRuntime(cfg, clients, apiTestSkillManager{}, mcpClient, session.New(8), runtime.NewAuditLogger(false, false))
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	return runtimeRef, llm
}

func newAPIWorkflowPendingApprovalRuntime(t *testing.T) *agent.Runtime {
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
			{Message: schema.Message{Content: "plan ready"}},
			{ToolCalls: []schema.ToolCall{{ID: "call-1", Name: "write_file", Arguments: json.RawMessage(`{"path":"demo.txt","content":"hello"}`)}}},
		}},
	}
	runtimeRef, err := agent.NewRuntime(cfg, clients, apiTestSkillManager{}, mcpClient, session.New(8), runtime.NewAuditLogger(false, false))
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	return runtimeRef
}

func newAPIWorkflowRepeatedToolApprovalRuntime(t *testing.T) (*agent.Runtime, *apiTestMCP) {
	t.Helper()
	cfg := &config.Config{
		RuntimeHome:  t.TempDir(),
		Audit:        config.AuditConfig{},
		Session:      config.SessionConfig{MaxHistory: 8},
		DefaultAgent: "chat",
		Agents: map[string]config.AgentProfile{
			"chat":    {Name: "Chat", Provider: "primary", Mode: "chat", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead, config.ToolKindWrite, config.ToolKindExec}, ToolPolicy: config.ToolPolicyConfirm},
			"planner": {Name: "Planner", Provider: "primary", Mode: "plan", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead}, ToolPolicy: config.ToolPolicyAllow},
			"fixer":   {Name: "Fixer", Provider: "primary", Mode: "fix", Model: "test-model", MaxIterations: 4, AllowedToolKinds: []config.ToolKind{config.ToolKindWrite}, ToolPolicy: config.ToolPolicyConfirm},
			"auditor": {Name: "Auditor", Provider: "primary", Mode: "audit", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead}, ToolPolicy: config.ToolPolicyAllow},
		},
	}
	mcpClient := &apiTestMCP{result: schema.ToolResult{ToolName: "write_file", Content: "ok"}}
	clients := map[string]interfaces.LLMClient{
		"primary": &apiTestLLM{responses: []schema.ChatResponse{
			{Message: schema.Message{Content: "plan ready"}},
			{ToolCalls: []schema.ToolCall{{ID: "call-1", Name: "write_file", Arguments: json.RawMessage(`{"path":"demo.txt","content":"hello"}`)}}},
			{ToolCalls: []schema.ToolCall{{ID: "call-2", Name: "write_file", Arguments: json.RawMessage(`{"path":"demo_test.txt","content":"test"}`)}}},
			{Message: schema.Message{Content: "fix done"}},
			{Message: schema.Message{Content: "audit done"}},
		}},
	}
	runtimeRef, err := agent.NewRuntime(cfg, clients, apiTestSkillManager{}, mcpClient, session.New(8), runtime.NewAuditLogger(false, false))
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	return runtimeRef, mcpClient
}

func newAPIOrdinaryMultiApprovalRuntime(t *testing.T) (*agent.Runtime, *apiTestMCP) {
	t.Helper()
	cfg := &config.Config{
		RuntimeHome:  t.TempDir(),
		Audit:        config.AuditConfig{},
		Session:      config.SessionConfig{MaxHistory: 8},
		DefaultAgent: "chat",
		Agents: map[string]config.AgentProfile{
			"chat": {Name: "Chat", Provider: "primary", Mode: "chat", Model: "test-model", MaxIterations: 3, AllowedToolKinds: []config.ToolKind{config.ToolKindRead, config.ToolKindWrite}, ToolPolicy: config.ToolPolicyConfirm},
		},
	}
	mcpClient := &apiTestMCP{result: schema.ToolResult{ToolName: "write_file", Content: "created"}}
	clients := map[string]interfaces.LLMClient{
		"primary": &apiTestLLM{responses: []schema.ChatResponse{
			{ToolCalls: []schema.ToolCall{
				{ID: "call-1", Name: "write_file", Arguments: json.RawMessage(`{"path":"one.txt","content":"one"}`)},
				{ID: "call-2", Name: "write_file", Arguments: json.RawMessage(`{"path":"two.txt","content":"two"}`)},
			}},
			{Message: schema.Message{Content: "both files done"}, Usage: schema.TokenUsage{PromptTokens: 7, OutputTokens: 3}},
		}},
	}
	runtimeRef, err := agent.NewRuntime(cfg, clients, apiTestSkillManager{}, mcpClient, session.New(8), runtime.NewAuditLogger(false, false))
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	return runtimeRef, mcpClient
}

func newAPIRiskPolicyOrdinaryApprovalRuntime(t *testing.T) *agent.Runtime {
	t.Helper()
	cfg := &config.Config{
		RuntimeHome:  t.TempDir(),
		Audit:        config.AuditConfig{},
		Session:      config.SessionConfig{MaxHistory: 8},
		DefaultAgent: "chat",
		ToolRiskPolicy: config.ToolRiskPolicyConfig{
			DisableRememberForUnsandboxedRiskyTools: true,
		},
		MCP: []config.MCPServerRef{{Name: "stub", Isolation: "process_group"}},
		Agents: map[string]config.AgentProfile{
			"chat": {Name: "Chat", Provider: "primary", Mode: "chat", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead, config.ToolKindWrite}, ToolPolicy: config.ToolPolicyConfirm},
		},
	}
	mcpClient := &apiTestMCP{result: schema.ToolResult{ToolName: "write_file", Content: "created"}}
	clients := map[string]interfaces.LLMClient{
		"primary": &apiTestLLM{responses: []schema.ChatResponse{
			{ToolCalls: []schema.ToolCall{{ID: "call-1", Name: "write_file", Arguments: json.RawMessage(`{"path":"one.txt","content":"one"}`)}}},
			{Message: schema.Message{Content: "file done"}},
		}},
	}
	runtimeRef, err := agent.NewRuntime(cfg, clients, apiTestSkillManager{}, mcpClient, session.New(8), runtime.NewAuditLogger(false, false))
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	return runtimeRef
}

func newAPIRiskPolicyOrdinaryMultiApprovalRuntime(t *testing.T) *agent.Runtime {
	t.Helper()
	cfg := &config.Config{
		RuntimeHome:  t.TempDir(),
		Audit:        config.AuditConfig{},
		Session:      config.SessionConfig{MaxHistory: 8},
		DefaultAgent: "chat",
		ToolRiskPolicy: config.ToolRiskPolicyConfig{
			DisableRememberForUnsandboxedRiskyTools: true,
		},
		MCP: []config.MCPServerRef{{Name: "stub", Isolation: "process_group"}},
		Agents: map[string]config.AgentProfile{
			"chat": {Name: "Chat", Provider: "primary", Mode: "chat", Model: "test-model", MaxIterations: 3, AllowedToolKinds: []config.ToolKind{config.ToolKindRead, config.ToolKindWrite}, ToolPolicy: config.ToolPolicyConfirm},
		},
	}
	mcpClient := &apiTestMCP{result: schema.ToolResult{ToolName: "write_file", Content: "created"}}
	clients := map[string]interfaces.LLMClient{
		"primary": &apiTestLLM{responses: []schema.ChatResponse{
			{ToolCalls: []schema.ToolCall{
				{ID: "call-1", Name: "write_file", Arguments: json.RawMessage(`{"path":"one.txt","content":"one"}`)},
				{ID: "call-2", Name: "write_file", Arguments: json.RawMessage(`{"path":"two.txt","content":"two"}`)},
			}},
			{Message: schema.Message{Content: "both files done"}},
		}},
	}
	runtimeRef, err := agent.NewRuntime(cfg, clients, apiTestSkillManager{}, mcpClient, session.New(8), runtime.NewAuditLogger(false, false))
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	return runtimeRef
}

func newAPIRiskPolicyRejectOrdinaryApprovalRuntime(t *testing.T) *agent.Runtime {
	t.Helper()
	cfg := &config.Config{
		RuntimeHome:  t.TempDir(),
		Audit:        config.AuditConfig{},
		Session:      config.SessionConfig{MaxHistory: 8},
		DefaultAgent: "chat",
		ToolRiskPolicy: config.ToolRiskPolicyConfig{
			RejectUnsandboxedRiskyTools: true,
		},
		MCP: []config.MCPServerRef{{Name: "stub", Isolation: "process_group"}},
		Agents: map[string]config.AgentProfile{
			"chat": {Name: "Chat", Provider: "primary", Mode: "chat", Model: "test-model", MaxIterations: 3, AllowedToolKinds: []config.ToolKind{config.ToolKindRead, config.ToolKindWrite}, ToolPolicy: config.ToolPolicyConfirm},
		},
	}
	mcpClient := &apiTestMCP{result: schema.ToolResult{ToolName: "write_file", Content: "created"}}
	clients := map[string]interfaces.LLMClient{
		"primary": &apiTestLLM{responses: []schema.ChatResponse{
			{ToolCalls: []schema.ToolCall{{ID: "call-1", Name: "write_file", Arguments: json.RawMessage(`{"path":"one.txt","content":"one"}`)}}},
			{Message: schema.Message{Content: "file done"}},
		}},
	}
	runtimeRef, err := agent.NewRuntime(cfg, clients, apiTestSkillManager{}, mcpClient, session.New(8), runtime.NewAuditLogger(false, false))
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	return runtimeRef
}

func newAPIRiskPolicyWorkflowApprovalRuntime(t *testing.T) *agent.Runtime {
	t.Helper()
	cfg := &config.Config{
		RuntimeHome:  t.TempDir(),
		Audit:        config.AuditConfig{},
		Session:      config.SessionConfig{MaxHistory: 8},
		DefaultAgent: "chat",
		ToolRiskPolicy: config.ToolRiskPolicyConfig{
			DisableRememberForUnsandboxedRiskyTools: true,
		},
		MCP: []config.MCPServerRef{{Name: "stub", Isolation: "process_group"}},
		Agents: map[string]config.AgentProfile{
			"chat":    {Name: "Chat", Provider: "primary", Mode: "chat", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead, config.ToolKindWrite, config.ToolKindExec}, ToolPolicy: config.ToolPolicyConfirm},
			"planner": {Name: "Planner", Provider: "primary", Mode: "plan", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead}, ToolPolicy: config.ToolPolicyAllow},
			"fixer":   {Name: "Fixer", Provider: "primary", Mode: "fix", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindWrite}, ToolPolicy: config.ToolPolicyConfirm},
			"auditor": {Name: "Auditor", Provider: "primary", Mode: "audit", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead}, ToolPolicy: config.ToolPolicyAllow},
		},
	}
	mcpClient := &apiTestMCP{result: schema.ToolResult{ToolName: "write_file", Content: "ok"}}
	clients := map[string]interfaces.LLMClient{
		"primary": &apiTestLLM{responses: []schema.ChatResponse{
			{Message: schema.Message{Content: "plan ready"}},
			{ToolCalls: []schema.ToolCall{{ID: "call-1", Name: "write_file", Arguments: json.RawMessage(`{"path":"demo.txt","content":"hello"}`)}}},
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
			{"name":"start","node_type":"start","next":["plan"],"position":{"x":20,"y":120}},
			{"name":"plan","node_type":"agent","agent":"planner","skill":"execution-plan","params":{"depth":"focused"},"next":["implement"],"position":{"x":80,"y":120}},
			{"name":"implement","node_type":"tool","agent":"fixer","skill":"code-writing","tool":"file_tools/write_file","approval":true,"next":["audit"],"position":{"x":320,"y":120}},
			{"name":"audit","node_type":"skill","agent":"auditor","skill":"code-audit","next":["end"],"position":{"x":560,"y":120}},
			{"name":"end","node_type":"end","position":{"x":780,"y":120}}
		]
	}`)
	request := httptest.NewRequest(http.MethodPut, "/api/workflow-graphs/release-check", body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected save 200, got %d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"name":"release-check"`) || !strings.Contains(response.Body.String(), `"node_type":"tool"`) || !strings.Contains(response.Body.String(), `"tool":"file_tools/write_file"`) {
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

	validateRequest := httptest.NewRequest(http.MethodPost, "/api/workflow-graphs/release-check/validate", strings.NewReader(`{
		"name":"release-check",
		"stages":[
			{"name":"plan","node_type":"agent","agent":"planner","skill":"execution-plan","next":["missing"]}
		]
	}`))
	validateRequest.Header.Set("Content-Type", "application/json")
	validateResponse := httptest.NewRecorder()
	server.ServeHTTP(validateResponse, validateRequest)
	if validateResponse.Code != http.StatusOK {
		t.Fatalf("expected validate 200, got %d body=%s", validateResponse.Code, validateResponse.Body.String())
	}
	if !strings.Contains(validateResponse.Body.String(), `"valid":false`) || !strings.Contains(validateResponse.Body.String(), `"stage":"plan"`) || !strings.Contains(validateResponse.Body.String(), "missing") {
		t.Fatalf("expected structured validation issue, got %s", validateResponse.Body.String())
	}

	validateParallelRequest := httptest.NewRequest(http.MethodPost, "/api/workflow-graphs/validate", strings.NewReader(`{
		"name":"parallel-check",
		"stages":[
			{"name":"split","node_type":"parallel","params":{"concurrent":"true"},"next":["research","audit"]},
			{"name":"research","agent":"planner","skill":"execution-plan","next":["join"]},
			{"name":"audit","agent":"auditor","skill":"code-audit","next":["join"]},
			{"name":"join","node_type":"join","params":{"wait_for":"research,audit"}}
		]
	}`))
	validateParallelRequest.Header.Set("Content-Type", "application/json")
	validateParallelResponse := httptest.NewRecorder()
	server.ServeHTTP(validateParallelResponse, validateParallelRequest)
	if validateParallelResponse.Code != http.StatusOK {
		t.Fatalf("expected parallel validate 200, got %d body=%s", validateParallelResponse.Code, validateParallelResponse.Body.String())
	}
	if !strings.Contains(validateParallelResponse.Body.String(), `"parallel"`) || !strings.Contains(validateParallelResponse.Body.String(), `"eligible":true`) || !strings.Contains(validateParallelResponse.Body.String(), `"join":"join"`) {
		t.Fatalf("expected parallel concurrency diagnostics, got %s", validateParallelResponse.Body.String())
	}

	expressionRequest := httptest.NewRequest(http.MethodPost, "/api/workflow-graphs/validate-expression", strings.NewReader(`{
		"mode":"condition",
		"expression":"len(stages.collect.outputs.tags) >= 2",
		"workflow":{
			"name":"expression-flow",
			"stages":[
				{"name":"collect","node_type":"input_gate","params":{"manual":"true","fields_json":"{\"fields\":[{\"name\":\"tags\",\"type\":\"select\",\"options\":[\"api\",\"auth\"],\"multiple\":true},{\"name\":\"payload\",\"type\":\"object\"}]}"},"next":["plan"]},
				{"name":"plan","node_type":"agent","agent":"planner","skill":"execution-plan"}
			]
		}
	}`))
	expressionRequest.Header.Set("Content-Type", "application/json")
	expressionResponse := httptest.NewRecorder()
	server.ServeHTTP(expressionResponse, expressionRequest)
	if expressionResponse.Code != http.StatusOK {
		t.Fatalf("expected expression validation 200, got %d body=%s", expressionResponse.Code, expressionResponse.Body.String())
	}
	var expressionResult agent.WorkflowExpressionValidationResult
	if err := json.Unmarshal(expressionResponse.Body.Bytes(), &expressionResult); err != nil {
		t.Fatalf("decode expression validation: %v", err)
	}
	if !expressionResult.Valid || expressionResult.ValueType != "boolean" || len(expressionResult.References) != 1 || expressionResult.References[0].Type != "array" {
		t.Fatalf("expected valid expression validation result, got %#v", expressionResult)
	}
	if !strings.Contains(expressionResponse.Body.String(), "stages.collect.outputs.payload") {
		t.Fatalf("expected expression suggestions in response, got %s", expressionResponse.Body.String())
	}

	importRequest := httptest.NewRequest(http.MethodPost, "/api/workflow-graphs/import", strings.NewReader(`
name: imported-flow
description: Imported from yaml.
stages:
  - name: plan
    node_type: agent
    agent: planner
    skill: execution-plan
`))
	importRequest.Header.Set("Content-Type", "application/yaml")
	importResponse := httptest.NewRecorder()
	server.ServeHTTP(importResponse, importRequest)
	if importResponse.Code != http.StatusCreated {
		t.Fatalf("expected import 201, got %d body=%s", importResponse.Code, importResponse.Body.String())
	}
	if !strings.Contains(importResponse.Body.String(), `"name":"imported-flow"`) {
		t.Fatalf("expected imported graph response, got %s", importResponse.Body.String())
	}

	exportRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-graphs/imported-flow/export?format=yaml", nil)
	exportResponse := httptest.NewRecorder()
	server.ServeHTTP(exportResponse, exportRequest)
	if exportResponse.Code != http.StatusOK {
		t.Fatalf("expected export 200, got %d body=%s", exportResponse.Code, exportResponse.Body.String())
	}
	if !strings.Contains(exportResponse.Header().Get("Content-Type"), "yaml") || !strings.Contains(exportResponse.Body.String(), "name: imported-flow") {
		t.Fatalf("expected yaml export, content-type=%s body=%s", exportResponse.Header().Get("Content-Type"), exportResponse.Body.String())
	}
}

func TestServerWorkflowExpressionValidationUsesRunSnapshotHints(t *testing.T) {
	state := session.New(8)
	runID := state.StartWorkflowRun("expression-flow", "inspect target")
	state.CompleteWorkflowRun(runID, "completed", "done", "", "", nil, []session.WorkflowRunStageSnapshot{{
		Stage: "collect",
		OutputValues: map[string]any{
			"payload": map[string]any{"risk": "high", "score": 9},
			"tags":    []any{"api", "auth"},
		},
	}})
	runtimeRef := newAPITestRuntimeWithStateAndResponses(t, state, nil)
	server := NewServer(runtimeRef)
	request := httptest.NewRequest(http.MethodPost, "/api/workflow-graphs/validate-expression", strings.NewReader(`{
		"mode":"condition",
		"run_id":"`+runID+`",
		"expression":"stages.collect.outputs.payload.risk == \"high\"",
		"workflow":{
			"name":"expression-flow",
			"stages":[
				{"name":"collect","node_type":"input_gate","outputs":{"payload":"object","tags":"array"}},
				{"name":"plan","node_type":"agent","agent":"planner","skill":"execution-plan"}
			]
		}
	}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected expression validation 200, got %d body=%s", response.Code, response.Body.String())
	}
	var result agent.WorkflowExpressionValidationResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode expression validation: %v", err)
	}
	if !result.Valid || len(result.References) != 1 || result.References[0].Type != "string" || result.References[0].Path != "risk" {
		t.Fatalf("expected run snapshot nested reference inference, got %#v", result)
	}
	if !strings.Contains(response.Body.String(), `"reference":"stages.collect.outputs.payload.risk"`) || !strings.Contains(response.Body.String(), `"source":"run_output"`) {
		t.Fatalf("expected run-output nested suggestions, got %s", response.Body.String())
	}

	catalogRequest := httptest.NewRequest(http.MethodPost, "/api/workflow-graphs/validate-expression", strings.NewReader(`{
		"mode":"condition",
		"expression":"stages.collect.outputs.payload.risk == \"high\"",
		"workflow":{
			"name":"expression-flow",
			"stages":[
				{"name":"collect","node_type":"input_gate","outputs":{"payload":"object","tags":"array"}},
				{"name":"plan","node_type":"agent","agent":"planner","skill":"execution-plan"}
			]
		}
	}`))
	catalogRequest.Header.Set("Content-Type", "application/json")
	catalogResponse := httptest.NewRecorder()
	server.ServeHTTP(catalogResponse, catalogRequest)
	if catalogResponse.Code != http.StatusOK {
		t.Fatalf("expected catalog-backed expression validation 200, got %d body=%s", catalogResponse.Code, catalogResponse.Body.String())
	}
	if !strings.Contains(catalogResponse.Body.String(), `"reference":"stages.collect.outputs.payload.risk"`) || !strings.Contains(catalogResponse.Body.String(), `"source":"schema_catalog"`) {
		t.Fatalf("expected schema-catalog nested suggestions, got %s", catalogResponse.Body.String())
	}

	listRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-schemas", nil)
	listResponse := httptest.NewRecorder()
	server.ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK || !strings.Contains(listResponse.Body.String(), `"workflow":"expression-flow"`) {
		t.Fatalf("expected workflow schema list, got %d body=%s", listResponse.Code, listResponse.Body.String())
	}
	getRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-schemas/expression-flow", nil)
	getResponse := httptest.NewRecorder()
	server.ServeHTTP(getResponse, getRequest)
	if getResponse.Code != http.StatusOK || !strings.Contains(getResponse.Body.String(), `"risk"`) {
		t.Fatalf("expected workflow schema detail, got %d body=%s", getResponse.Code, getResponse.Body.String())
	}
	captureResourceRequest := httptest.NewRequest(http.MethodPost, "/api/resources/workflow-schemas/expression-flow/capture", strings.NewReader(`{"description":"Shared expression schema"}`))
	captureResourceResponse := httptest.NewRecorder()
	server.ServeHTTP(captureResourceResponse, captureResourceRequest)
	if captureResourceResponse.Code != http.StatusCreated || !strings.Contains(captureResourceResponse.Body.String(), `"description":"Shared expression schema"`) {
		t.Fatalf("expected workflow schema resource capture, got %d body=%s", captureResourceResponse.Code, captureResourceResponse.Body.String())
	}
	resourceListRequest := httptest.NewRequest(http.MethodGet, "/api/resources/workflow-schemas", nil)
	resourceListResponse := httptest.NewRecorder()
	server.ServeHTTP(resourceListResponse, resourceListRequest)
	if resourceListResponse.Code != http.StatusOK || !strings.Contains(resourceListResponse.Body.String(), `"name":"expression-flow"`) || !strings.Contains(resourceListResponse.Body.String(), `"version":2`) || !strings.Contains(resourceListResponse.Body.String(), `"outputs":2`) {
		t.Fatalf("expected workflow schema resource list, got %d body=%s", resourceListResponse.Code, resourceListResponse.Body.String())
	}
	resourceGetRequest := httptest.NewRequest(http.MethodGet, "/api/resources/workflow-schemas/expression-flow", nil)
	resourceGetResponse := httptest.NewRecorder()
	server.ServeHTTP(resourceGetResponse, resourceGetRequest)
	if resourceGetResponse.Code != http.StatusOK || !strings.Contains(resourceGetResponse.Body.String(), `"kind":"goflow.workflow_schema_resource"`) || !strings.Contains(resourceGetResponse.Body.String(), `"version":2`) || !strings.Contains(resourceGetResponse.Body.String(), `"schema"`) || !strings.Contains(resourceGetResponse.Body.String(), `"payload"`) {
		t.Fatalf("expected workflow schema resource detail, got %d body=%s", resourceGetResponse.Code, resourceGetResponse.Body.String())
	}
	exportRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-schemas/expression-flow/export", nil)
	exportResponse := httptest.NewRecorder()
	server.ServeHTTP(exportResponse, exportRequest)
	if exportResponse.Code != http.StatusOK || !strings.Contains(exportResponse.Body.String(), `"kind":"goflow.workflow_schemas"`) || !strings.Contains(exportResponse.Body.String(), `"version":2`) || !strings.Contains(exportResponse.Body.String(), `"min_supported_version":1`) || !strings.Contains(exportResponse.Body.String(), `"schemas"`) {
		t.Fatalf("expected workflow schema export bundle, got %d body=%s", exportResponse.Code, exportResponse.Body.String())
	}
	schemaDryRunRequest := httptest.NewRequest(http.MethodPost, "/api/resources/workflow-schemas/draft-schema-flow/validate", strings.NewReader(`{
		"name": "draft-schema-flow",
		"schema": {
			"workflow": "draft-schema-flow",
			"stages": {
				"collect": {
					"outputs": {
						"payload": {
							"type": "object",
							"fields": {
								"risk": {"type": "string"}
							}
						}
					}
				}
			}
		}
	}`))
	schemaDryRunRequest.Header.Set("Content-Type", "application/json")
	schemaDryRunResponse := httptest.NewRecorder()
	server.ServeHTTP(schemaDryRunResponse, schemaDryRunRequest)
	if schemaDryRunResponse.Code != http.StatusOK || !strings.Contains(schemaDryRunResponse.Body.String(), `"valid":true`) || !strings.Contains(schemaDryRunResponse.Body.String(), `"resource":"workflow_schema"`) || !strings.Contains(schemaDryRunResponse.Body.String(), `"normalized"`) {
		t.Fatalf("expected workflow schema dry-run validation, got %d body=%s", schemaDryRunResponse.Code, schemaDryRunResponse.Body.String())
	}
	if _, err := os.Stat(filepath.Join(runtimeRef.RuntimeHome(), "schemas", "workflows", "draft-schema-flow.json")); !os.IsNotExist(err) {
		t.Fatalf("expected workflow schema dry-run not to write resource, stat err=%v", err)
	}
	invalidSchemaDryRunRequest := httptest.NewRequest(http.MethodPost, "/api/resources/workflow-schemas/validate", strings.NewReader(`{
		"schema": {"workflow": "missing-stages"}
	}`))
	invalidSchemaDryRunRequest.Header.Set("Content-Type", "application/json")
	invalidSchemaDryRunResponse := httptest.NewRecorder()
	server.ServeHTTP(invalidSchemaDryRunResponse, invalidSchemaDryRunRequest)
	if invalidSchemaDryRunResponse.Code != http.StatusOK || !strings.Contains(invalidSchemaDryRunResponse.Body.String(), `"valid":false`) || !strings.Contains(invalidSchemaDryRunResponse.Body.String(), `"field":"schema.stages"`) {
		t.Fatalf("expected invalid workflow schema dry-run validation, got %d body=%s", invalidSchemaDryRunResponse.Code, invalidSchemaDryRunResponse.Body.String())
	}
	legacyResourceRequest := httptest.NewRequest(http.MethodPut, "/api/resources/workflow-schemas/legacy-schema-flow", strings.NewReader(`{
		"workflow": "legacy-schema-flow",
		"run_ids": ["legacy-run"],
		"stages": {
			"collect": {
				"outputs": {
					"payload": {
						"type": "object",
						"fields": {
							"risk": {"type": "string"}
						}
					}
				}
			}
		}
	}`))
	legacyResourceRequest.Header.Set("Content-Type", "application/json")
	legacyResourceResponse := httptest.NewRecorder()
	server.ServeHTTP(legacyResourceResponse, legacyResourceRequest)
	if legacyResourceResponse.Code != http.StatusOK || !strings.Contains(legacyResourceResponse.Body.String(), `"kind":"goflow.workflow_schema_resource"`) || !strings.Contains(legacyResourceResponse.Body.String(), `"version":2`) || !strings.Contains(legacyResourceResponse.Body.String(), `"migrated_from_version":1`) {
		t.Fatalf("expected legacy workflow schema resource migration, got %d body=%s", legacyResourceResponse.Code, legacyResourceResponse.Body.String())
	}
	futureResourceRequest := httptest.NewRequest(http.MethodPut, "/api/resources/workflow-schemas/future-schema-flow", strings.NewReader(`{
		"kind": "goflow.workflow_schema_resource",
		"version": 99,
		"schema": {"workflow":"future-schema-flow"}
	}`))
	futureResourceRequest.Header.Set("Content-Type", "application/json")
	futureResourceResponse := httptest.NewRecorder()
	server.ServeHTTP(futureResourceResponse, futureResourceRequest)
	if futureResourceResponse.Code != http.StatusBadRequest || !strings.Contains(futureResourceResponse.Body.String(), "newer than supported") {
		t.Fatalf("expected future workflow schema resource rejection, got %d body=%s", futureResourceResponse.Code, futureResourceResponse.Body.String())
	}
	deleteRequest := httptest.NewRequest(http.MethodDelete, "/api/workflow-schemas/expression-flow", nil)
	deleteResponse := httptest.NewRecorder()
	server.ServeHTTP(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("expected workflow schema delete 204, got %d body=%s", deleteResponse.Code, deleteResponse.Body.String())
	}
	activateResourceRequest := httptest.NewRequest(http.MethodPost, "/api/resources/workflow-schemas/expression-flow/activate?merge=false", nil)
	activateResourceResponse := httptest.NewRecorder()
	server.ServeHTTP(activateResourceResponse, activateResourceRequest)
	if activateResourceResponse.Code != http.StatusOK || !strings.Contains(activateResourceResponse.Body.String(), `"workflow":"expression-flow"`) || !strings.Contains(activateResourceResponse.Body.String(), `"risk"`) {
		t.Fatalf("expected workflow schema resource activation, got %d body=%s", activateResourceResponse.Code, activateResourceResponse.Body.String())
	}
	importRequest := httptest.NewRequest(http.MethodPost, "/api/workflow-schemas/import", strings.NewReader(`{
		"merge": true,
		"schemas": [{
			"workflow": "imported-schema-flow",
			"run_ids": ["external-run"],
			"stages": {
				"collect": {
					"outputs": {
						"payload": {
							"type": "object",
							"fields": {
								"risk": {"type": "string"},
								"score": {"type": "number"}
							}
						}
					}
				}
			}
		}]
	}`))
	importRequest.Header.Set("Content-Type", "application/json")
	importResponse := httptest.NewRecorder()
	server.ServeHTTP(importResponse, importRequest)
	if importResponse.Code != http.StatusCreated || !strings.Contains(importResponse.Body.String(), `"imported":1`) || !strings.Contains(importResponse.Body.String(), `"imported-schema-flow"`) {
		t.Fatalf("expected workflow schema import, got %d body=%s", importResponse.Code, importResponse.Body.String())
	}
	futureImportRequest := httptest.NewRequest(http.MethodPost, "/api/workflow-schemas/import", strings.NewReader(`{
		"kind": "goflow.workflow_schemas",
		"version": 99,
		"schemas": [{"workflow":"future-import-flow"}]
	}`))
	futureImportRequest.Header.Set("Content-Type", "application/json")
	futureImportResponse := httptest.NewRecorder()
	server.ServeHTTP(futureImportResponse, futureImportRequest)
	if futureImportResponse.Code != http.StatusBadRequest || !strings.Contains(futureImportResponse.Body.String(), "newer than supported") {
		t.Fatalf("expected future workflow schema import rejection, got %d body=%s", futureImportResponse.Code, futureImportResponse.Body.String())
	}
	importedGetRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-schemas/imported-schema-flow", nil)
	importedGetResponse := httptest.NewRecorder()
	server.ServeHTTP(importedGetResponse, importedGetRequest)
	if importedGetResponse.Code != http.StatusOK || !strings.Contains(importedGetResponse.Body.String(), `"score"`) {
		t.Fatalf("expected imported workflow schema detail, got %d body=%s", importedGetResponse.Code, importedGetResponse.Body.String())
	}
	resourceDeleteRequest := httptest.NewRequest(http.MethodDelete, "/api/resources/workflow-schemas/expression-flow", nil)
	resourceDeleteResponse := httptest.NewRecorder()
	server.ServeHTTP(resourceDeleteResponse, resourceDeleteRequest)
	if resourceDeleteResponse.Code != http.StatusNoContent {
		t.Fatalf("expected workflow schema resource delete, got %d body=%s", resourceDeleteResponse.Code, resourceDeleteResponse.Body.String())
	}
	rebuildRequest := httptest.NewRequest(http.MethodPost, "/api/workflow-schemas/rebuild", nil)
	rebuildResponse := httptest.NewRecorder()
	server.ServeHTTP(rebuildResponse, rebuildRequest)
	if rebuildResponse.Code != http.StatusOK || !strings.Contains(rebuildResponse.Body.String(), `"workflow":"expression-flow"`) {
		t.Fatalf("expected workflow schema rebuild, got %d body=%s", rebuildResponse.Code, rebuildResponse.Body.String())
	}
	clearRequest := httptest.NewRequest(http.MethodDelete, "/api/workflow-schemas", nil)
	clearResponse := httptest.NewRecorder()
	server.ServeHTTP(clearResponse, clearRequest)
	if clearResponse.Code != http.StatusOK || !strings.Contains(clearResponse.Body.String(), `"cleared":1`) {
		t.Fatalf("expected workflow schema clear all, got %d body=%s", clearResponse.Code, clearResponse.Body.String())
	}
}

func TestServerRuntimeStatusExposesCostDiagnostics(t *testing.T) {
	server := NewServer(newAPITestRuntime(t))
	runReq := httptest.NewRequest(http.MethodPost, "/api/run", strings.NewReader(`{"input":"hello"}`))
	runReq.Header.Set("Content-Type", "application/json")
	runResp := httptest.NewRecorder()
	server.ServeHTTP(runResp, runReq)
	if runResp.Code != http.StatusOK {
		t.Fatalf("expected run 200, got %d body=%s", runResp.Code, runResp.Body.String())
	}

	statusReq := httptest.NewRequest(http.MethodGet, "/api/runtime", nil)
	statusResp := httptest.NewRecorder()
	server.ServeHTTP(statusResp, statusReq)
	if statusResp.Code != http.StatusOK {
		t.Fatalf("expected runtime 200, got %d body=%s", statusResp.Code, statusResp.Body.String())
	}
	var response runtimeStatusResponse
	if err := json.Unmarshal(statusResp.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode runtime status: %v", err)
	}
	if response.Cost.Samples == 0 || response.Cost.Latest == nil {
		t.Fatalf("expected cost diagnostics, got %#v", response.Cost)
	}
	if response.Cost.Latest.PromptPrefixHash == "" || response.Cost.Latest.SystemHash == "" {
		t.Fatalf("expected prompt cache hashes in cost diagnostics, got %#v", response.Cost.Latest)
	}
	if response.Cost.AverageEstimatedPromptTokens == 0 || response.Cost.UniquePromptPrefixes == 0 {
		t.Fatalf("expected aggregate cost diagnostics, got %#v", response.Cost)
	}
	if response.Cost.TokenUsageSamples == 0 || len(response.Cost.TokenUsageHistory) == 0 {
		t.Fatalf("expected token usage history in cost diagnostics, got %#v", response.Cost)
	}
	if response.Cost.TotalPromptTokens == 0 || response.Cost.TotalOutputTokens == 0 || response.Cost.TotalTokens == 0 {
		t.Fatalf("expected top-level token totals in cost diagnostics, got %#v", response.Cost)
	}
	if response.Cost.ProviderCacheHitRate <= 0 {
		t.Fatalf("expected provider cache hit rate from reported cached tokens, got %#v", response.Cost)
	}
	if len(response.Cost.ByAgent) == 0 || response.Cost.ByAgent[0].TotalPromptTokens == 0 {
		t.Fatalf("expected by-agent cost trend, got %#v", response.Cost.ByAgent)
	}
	if response.Cost.ByAgent[0].ProviderCacheHitRate <= 0 {
		t.Fatalf("expected by-agent cache hit rate, got %#v", response.Cost.ByAgent)
	}
	if len(response.Cost.ByMode) == 0 || response.Cost.ByMode[0].AverageNonCacheableTokens == 0 {
		t.Fatalf("expected by-mode cost trend, got %#v", response.Cost.ByMode)
	}
	if len(response.Cost.ByStage) == 0 || response.Cost.ByStage[0].TaskStage == "" {
		t.Fatalf("expected by-stage cost trend, got %#v", response.Cost.ByStage)
	}
	if !costFeatureExists(response.Cost.Features, "prompt_budget_events", "observed") ||
		!costFeatureExists(response.Cost.Features, "provider_prompt_cache_signals", "observed") ||
		!costFeatureExists(response.Cost.Features, "auxiliary_router", "disabled") ||
		!costFeatureExists(response.Cost.Features, "auxiliary_summarizer", "disabled") {
		t.Fatalf("expected cost-control feature catalog, got %#v", response.Cost.Features)
	}
	sessionHistoryFeature := costFeatureByCode(response.Cost.Features, "session_history_compaction")
	if sessionHistoryFeature == nil || sessionHistoryFeature.Category != "prompt_reduction" || !sessionHistoryFeature.Enabled || sessionHistoryFeature.Measurement == "" {
		t.Fatalf("expected session history compaction feature metadata, got %#v", sessionHistoryFeature)
	}
	toolFeature := costFeatureByCode(response.Cost.Features, "tool_schema_minimization")
	if toolFeature == nil || toolFeature.Category != "prompt_reduction" || toolFeature.Enabled != true || toolFeature.Measurement == "" {
		t.Fatalf("expected tool-schema minimization feature metadata, got %#v", toolFeature)
	}

	costReq := httptest.NewRequest(http.MethodGet, "/api/runtime/cost", nil)
	costResp := httptest.NewRecorder()
	server.ServeHTTP(costResp, costReq)
	if costResp.Code != http.StatusOK {
		t.Fatalf("expected runtime cost 200, got %d body=%s", costResp.Code, costResp.Body.String())
	}
	var costOnly costDiagnostics
	if err := json.Unmarshal(costResp.Body.Bytes(), &costOnly); err != nil {
		t.Fatalf("decode runtime cost: %v", err)
	}
	if costOnly.Samples != response.Cost.Samples || costOnly.Latest == nil || costOnly.Latest.PromptPrefixHash == "" {
		t.Fatalf("expected standalone cost endpoint to match runtime diagnostics, got %#v", costOnly)
	}
	if !costFeatureExists(costOnly.Features, "prompt_budget_events", "observed") ||
		!costFeatureExists(costOnly.Features, "auxiliary_router", "disabled") {
		t.Fatalf("expected standalone cost endpoint feature catalog, got %#v", costOnly.Features)
	}
}

func TestBuildCostRecommendationsIncludesMeasuredAuxiliaryCandidates(t *testing.T) {
	diagnostics := costDiagnostics{
		Samples:                      4,
		AverageEstimatedPromptTokens: 2200,
		AuxiliaryRoutes: []auxiliarySummary{
			{Kind: "router", Enabled: false},
			{Kind: "summarizer", Enabled: false},
		},
		Latest: &schema.PromptBudget{
			AgentID:               "chat",
			Mode:                  "chat",
			TaskStage:             "inspect",
			EstimatedPromptTokens: 3200,
			MessageTokens:         2100,
			ToolSchemaTokens:      1200,
			FilteredToolCount:     3,
		},
		ByStage: []costTrend{{
			Key:                          "summarize",
			TaskStage:                    "summarize",
			PromptBudgetSamples:          2,
			AverageEstimatedPromptTokens: 950,
		}},
	}

	recommendations := buildCostRecommendations(diagnostics)
	router := costRecommendationByCode(recommendations, "router_auxiliary_candidate")
	if router == nil || !router.RequiresConfig || router.Measurement == "" || router.Action == "" || router.ExpectedSavingsKind != "auxiliary_model_route" {
		t.Fatalf("expected measured router recommendation, got %#v", router)
	}
	summarizer := costRecommendationByCode(recommendations, "summarizer_auxiliary_candidate")
	if summarizer == nil || !summarizer.RequiresConfig || summarizer.Measurement == "" || summarizer.Action == "" || summarizer.ExpectedSavingsKind != "auxiliary_model_route" {
		t.Fatalf("expected measured summarizer recommendation, got %#v", summarizer)
	}
	toolSchema := costRecommendationByCode(recommendations, "tool_schema_high")
	if toolSchema == nil || !toolSchema.RequiresConfig || toolSchema.Measurement == "" || toolSchema.Action == "" {
		t.Fatalf("expected measured tool schema recommendation, got %#v", toolSchema)
	}
}

func costRecommendationByCode(items []costRecommendation, code string) *costRecommendation {
	for i := range items {
		if items[i].Code == code {
			return &items[i]
		}
	}
	return nil
}

func TestServerRuntimeStatusExposesToolRiskPolicy(t *testing.T) {
	server := NewServer(newAPITestRuntime(t))
	statusReq := httptest.NewRequest(http.MethodGet, "/api/runtime", nil)
	statusResp := httptest.NewRecorder()
	server.ServeHTTP(statusResp, statusReq)
	if statusResp.Code != http.StatusOK {
		t.Fatalf("expected runtime 200, got %d body=%s", statusResp.Code, statusResp.Body.String())
	}
	var response runtimeStatusResponse
	if err := json.Unmarshal(statusResp.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode runtime status: %v", err)
	}
	if response.ToolRiskPolicy.Enabled ||
		response.ToolRiskPolicy.RequireApprovalForUnsandboxedRiskyTools ||
		response.ToolRiskPolicy.DisableRememberForUnsandboxedRiskyTools ||
		response.ToolRiskPolicy.RejectUnsandboxedRiskyTools {
		t.Fatalf("expected default tool risk policy disabled, got %#v", response.ToolRiskPolicy)
	}
	if !configDiagnosticsStringSliceContains(response.ToolRiskPolicy.AppliesToKinds, "write") ||
		!strings.Contains(response.ToolRiskPolicy.UnsandboxedDefinition, "isolation: container") ||
		!strings.Contains(response.ToolRiskPolicy.UnsandboxedDefinition, "isolation: linux_netns") ||
		!strings.Contains(response.ToolRiskPolicy.UnsandboxedDefinition, "network tools") {
		t.Fatalf("expected tool risk policy metadata, got %#v", response.ToolRiskPolicy)
	}
	if !toolRiskPolicyBoundaryExists(response.ToolRiskPolicy.RecognizedSandboxBoundaries, "container", "broad", "write") ||
		!toolRiskPolicyBoundaryExists(response.ToolRiskPolicy.RecognizedSandboxBoundaries, "linux_netns", "capability_specific", "network") ||
		toolRiskPolicyBoundaryExists(response.ToolRiskPolicy.RecognizedSandboxBoundaries, "linux_netns", "capability_specific", "write") {
		t.Fatalf("expected structured tool risk sandbox boundary metadata, got %#v", response.ToolRiskPolicy.RecognizedSandboxBoundaries)
	}

	runtimeRef := newAPITestRuntimeWithStateHomeResponsesMCPCostAndRiskPolicy(t, session.New(8), "", nil, nil, config.CostControlConfig{}, config.ToolRiskPolicyConfig{
		RequireApprovalForUnsandboxedRiskyTools: true,
		DisableRememberForUnsandboxedRiskyTools: true,
		RejectUnsandboxedRiskyTools:             true,
	})
	server = NewServer(runtimeRef)
	statusResp = httptest.NewRecorder()
	server.ServeHTTP(statusResp, statusReq)
	if statusResp.Code != http.StatusOK {
		t.Fatalf("expected runtime 200, got %d body=%s", statusResp.Code, statusResp.Body.String())
	}
	if err := json.Unmarshal(statusResp.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode runtime status with policy: %v", err)
	}
	if !response.ToolRiskPolicy.Enabled ||
		!response.ToolRiskPolicy.RequireApprovalForUnsandboxedRiskyTools ||
		!response.ToolRiskPolicy.DisableRememberForUnsandboxedRiskyTools ||
		!response.ToolRiskPolicy.RejectUnsandboxedRiskyTools ||
		!strings.Contains(response.ToolRiskPolicy.ApprovalBehavior, "tool_policy is allow") ||
		!strings.Contains(response.ToolRiskPolicy.RememberBehavior, "approve-and-remember") ||
		!strings.Contains(response.ToolRiskPolicy.RejectionBehavior, "rejected before execution") ||
		!strings.Contains(response.ToolRiskPolicy.RejectionBehavior, "linux_netns") {
		t.Fatalf("expected enabled tool risk policy metadata, got %#v", response.ToolRiskPolicy)
	}
}

func TestPromptCostDiagnosticsBuildsRecommendations(t *testing.T) {
	diagnostics := promptCostDiagnostics(session.Snapshot{
		PromptBudget: &schema.PromptBudget{
			AgentID:                        "fixer",
			Mode:                           "fix",
			TaskStage:                      "modify",
			WorkflowName:                   "code-flow",
			EstimatedPromptTokens:          9000,
			MessageTokens:                  3200,
			ToolSchemaTokens:               1400,
			CacheablePrefixTokens:          2000,
			ExposedToolCount:               2,
			FilteredToolCount:              3,
			ToolSchemaDiagnosticCount:      4,
			ToolSchemaDiagnosticOmitted:    1,
			HistoryEstimatedSavedTokens:    40,
			HistoryPromptDeduplicatedItems: 1,
			HistoryToolCompactedOlderItems: 2,
			MemoryBlockCount:               2,
			MemoryEstimatedSavedTokens:     60,
			ArtifactRefCount:               1,
			CompactedToolResultCount:       1,
			ArtifactOmittedTokens:          700,
			SkillName:                      "large-skill",
			SkillOmittedTokens:             650,
			OmittedContext:                 []string{"memory omitted", "skill omitted", "artifact omitted"},
			PromptPrefixHash:               "latest",
		},
		PromptBudgets: []schema.PromptBudget{
			{AgentID: "fixer", Mode: "fix", EstimatedPromptTokens: 9000, MessageTokens: 3200, ToolSchemaTokens: 1400, CacheablePrefixTokens: 2000, ExposedToolCount: 2, FilteredToolCount: 3, ToolSchemaDiagnosticCount: 4, ToolSchemaDiagnosticOmitted: 1, HistoryEstimatedSavedTokens: 40, HistoryPromptDeduplicatedItems: 1, HistoryToolCompactedOlderItems: 2, MemoryBlockCount: 2, MemoryEstimatedSavedTokens: 60, ArtifactRefCount: 1, CompactedToolResultCount: 1, ArtifactOmittedTokens: 700, SkillName: "large-skill", SkillOmittedTokens: 650, OmittedContext: []string{"memory omitted", "skill omitted", "artifact omitted"}, PromptPrefixHash: "a"},
			{AgentID: "fixer", Mode: "fix", EstimatedPromptTokens: 8000, CacheablePrefixTokens: 2000, PromptPrefixHash: "b"},
			{AgentID: "fixer", Mode: "fix", EstimatedPromptTokens: 7000, CacheablePrefixTokens: 2000, PromptPrefixHash: "c"},
			{AgentID: "fixer", Mode: "fix", EstimatedPromptTokens: 6000, CacheablePrefixTokens: 2000, PromptPrefixHash: "d"},
		},
	})
	codes := map[string]bool{}
	for _, item := range diagnostics.Recommendations {
		codes[item.Code] = true
	}
	for _, code := range []string{"message_context_high", "tool_schema_high", "non_cacheable_context_high", "skill_summary_saving", "prompt_prefix_churn", "agent_prompt_high"} {
		if !codes[code] {
			t.Fatalf("expected recommendation %s, got %#v", code, diagnostics.Recommendations)
		}
	}
	if diagnostics.HistoryEstimatedSavedTokens != 40 || diagnostics.HistoryDeduplicatedItems != 1 || diagnostics.HistoryCompactedOlderItems != 2 {
		t.Fatalf("expected history compaction diagnostics, got %#v", diagnostics)
	}
	if len(diagnostics.ByAgent) == 0 || diagnostics.ByAgent[0].HistoryEstimatedSavedTokens != 40 || diagnostics.ByAgent[0].HistoryDeduplicatedItems != 1 {
		t.Fatalf("expected trend history compaction diagnostics, got %#v", diagnostics.ByAgent)
	}
	if diagnostics.ToolSchemaDiagnosticSamples != 4 || diagnostics.ToolSchemaDiagnosticOmitted != 1 || diagnostics.MemoryBlockSamples != 2 || diagnostics.MemoryEstimatedSavedTokens != 60 || diagnostics.ArtifactRefSamples != 1 || diagnostics.ArtifactOmittedTokens != 700 || diagnostics.SkillOmittedTokens != 650 || diagnostics.OmittedContextCount != 3 {
		t.Fatalf("expected context diagnostics, got %#v", diagnostics)
	}
	if !costFeatureExists(diagnostics.Features, "retrieval_memory_blocks", "observed") ||
		!costFeatureExists(diagnostics.Features, "skill_schema_slimming", "observed") ||
		!costFeatureExists(diagnostics.Features, "session_artifact_refs", "observed") {
		t.Fatalf("expected context feature diagnostics, got %#v", diagnostics.Features)
	}
}

func TestPromptCostDiagnosticsComputesCacheReuseSignals(t *testing.T) {
	diagnostics := promptCostDiagnostics(session.Snapshot{
		PromptBudgets: []schema.PromptBudget{
			{AgentID: "planner", Mode: "plan", EstimatedPromptTokens: 3000, CacheablePrefixTokens: 1800, PromptPrefixHash: "stable"},
			{AgentID: "planner", Mode: "plan", EstimatedPromptTokens: 3200, CacheablePrefixTokens: 1800, PromptPrefixHash: "stable"},
			{AgentID: "planner", Mode: "plan", EstimatedPromptTokens: 3400, CacheablePrefixTokens: 1800, PromptPrefixHash: "variant"},
		},
		TokenUsages: []schema.TokenUsageSample{
			{AgentID: "planner", Mode: "plan", PromptTokens: 1000, OutputTokens: 100, CachedTokens: 250},
			{AgentID: "planner", Mode: "plan", PromptTokens: 1200, OutputTokens: 120, CachedTokens: 300},
		},
	})
	if diagnostics.TotalPromptTokens != 2200 || diagnostics.TotalOutputTokens != 220 || diagnostics.TotalCachedTokens != 550 || diagnostics.TotalTokens != 2420 {
		t.Fatalf("unexpected top-level token totals: %#v", diagnostics)
	}
	if diagnostics.UniquePromptPrefixes != 2 || diagnostics.PromptPrefixReuseSamples != 1 {
		t.Fatalf("unexpected prefix reuse diagnostics: %#v", diagnostics)
	}
	if diagnostics.ProviderCacheHitRate != 0.25 {
		t.Fatalf("expected provider cache hit rate 0.25, got %#v", diagnostics.ProviderCacheHitRate)
	}
	if len(diagnostics.ByAgent) != 1 || diagnostics.ByAgent[0].PromptPrefixReuseSamples != 1 || diagnostics.ByAgent[0].ProviderCacheHitRate != 0.25 {
		t.Fatalf("expected trend cache diagnostics, got %#v", diagnostics.ByAgent)
	}
}

func TestPromptCostDiagnosticsWarnsWhenCacheNotObserved(t *testing.T) {
	diagnostics := promptCostDiagnostics(session.Snapshot{
		PromptBudgets: []schema.PromptBudget{
			{AgentID: "planner", Mode: "plan", EstimatedPromptTokens: 3000, CacheablePrefixTokens: 1800, PromptPrefixHash: "stable"},
			{AgentID: "planner", Mode: "plan", EstimatedPromptTokens: 3200, CacheablePrefixTokens: 1800, PromptPrefixHash: "stable"},
			{AgentID: "planner", Mode: "plan", EstimatedPromptTokens: 3400, CacheablePrefixTokens: 1800, PromptPrefixHash: "stable"},
		},
		TokenUsages: []schema.TokenUsageSample{
			{AgentID: "planner", Mode: "plan", PromptTokens: 1000, OutputTokens: 100},
			{AgentID: "planner", Mode: "plan", PromptTokens: 1200, OutputTokens: 120},
			{AgentID: "planner", Mode: "plan", PromptTokens: 1400, OutputTokens: 140},
		},
	})
	for _, item := range diagnostics.Recommendations {
		if item.Code == "provider_cache_not_observed" {
			return
		}
	}
	t.Fatalf("expected provider_cache_not_observed recommendation, got %#v", diagnostics.Recommendations)
}

func TestRuntimeCostDiagnosticsExposesConfiguredAuxiliaryRoutes(t *testing.T) {
	runtimeRef := newAPITestRuntimeWithStateHomeResponsesMCPAndCostControl(t, session.New(8), "", nil, nil, config.CostControlConfig{
		Router: config.AuxiliaryModelConfig{Enabled: true, Provider: "primary", Model: "cheap-router", MaxTokens: 64},
	})
	server := NewServer(runtimeRef)
	diagnostics := server.runtimeCostDiagnostics(session.Snapshot{
		PromptBudgets: []schema.PromptBudget{{AgentID: "chat", Mode: "chat", EstimatedPromptTokens: 100, CacheablePrefixTokens: 40, PromptPrefixHash: "stable"}},
	})
	if !costFeatureExists(diagnostics.Features, "auxiliary_router", "configured") {
		t.Fatalf("expected configured auxiliary router feature, got %#v", diagnostics.Features)
	}
	for _, feature := range diagnostics.Features {
		if feature.Code == "auxiliary_router" {
			if !feature.ExtraModelCall || !feature.RequiresConfig || feature.Provider != "primary" || feature.Model != "cheap-router" {
				t.Fatalf("expected auxiliary router metadata, got %#v", feature)
			}
			return
		}
	}
	t.Fatalf("expected auxiliary router feature, got %#v", diagnostics.Features)
}

func TestRuntimeCostDiagnosticsExposesMeasuredRouteTuning(t *testing.T) {
	state := session.New(8)
	state.SetPromptBudget(schema.PromptBudget{AgentID: "router", Mode: "route", EstimatedPromptTokens: 120, CacheablePrefixTokens: 40, PromptPrefixHash: "router"})
	state.AddTokenUsage(schema.TokenUsageSample{AgentID: "router", Mode: "route", PromptTokens: 45, OutputTokens: 3, TotalTokens: 48})
	state.SetTaskStage(session.TaskStageSnapshot{AgentID: "fixer", Mode: "fix", Stage: "summarize", Detail: "iteration budget reached"})
	state.SetPromptBudget(schema.PromptBudget{AgentID: "fixer", Mode: "fix", TaskStage: "summarize", EstimatedPromptTokens: 900, CacheablePrefixTokens: 300, PromptPrefixHash: "summary"})
	state.AddTokenUsage(schema.TokenUsageSample{AgentID: "fixer", Mode: "fix", TaskStage: "summarize", PromptTokens: 380, OutputTokens: 90, TotalTokens: 470})
	state.SetTaskStage(session.TaskStageSnapshot{})
	state.SetPromptBudget(schema.PromptBudget{AgentID: "chat", Mode: "chat", EstimatedPromptTokens: 2600, CacheablePrefixTokens: 1200, PromptPrefixHash: "main"})

	runtimeRef := newAPITestRuntimeWithStateHomeResponsesMCPAndCostControl(t, state, "", nil, nil, config.CostControlConfig{
		Router:     config.AuxiliaryModelConfig{Enabled: true, Provider: "primary", Model: "cheap-router", MaxTokens: 64},
		Summarizer: config.AuxiliaryModelConfig{Enabled: true, Provider: "primary", Model: "cheap-summary", MaxTokens: 512},
	})
	server := NewServer(runtimeRef)
	diagnostics := server.runtimeCostDiagnostics(state.Snapshot())
	router := costRouteTuningByKind(diagnostics.Tuning, "router")
	if router == nil || router.State == "" || router.ObservedSamples == 0 || router.ObservedTotalTokens != 48 || router.Provider != "primary" || router.Model != "cheap-router" || len(router.QualityChecklist) == 0 {
		t.Fatalf("expected observed router tuning metadata, got %#v", diagnostics.Tuning)
	}
	summarizer := costRouteTuningByKind(diagnostics.Tuning, "summarizer")
	if summarizer == nil || summarizer.ObservedSamples == 0 || summarizer.ObservedTotalTokens != 470 || summarizer.EstimatedExtraCallTokens == 0 || summarizer.Action == "" {
		t.Fatalf("expected observed summarizer tuning metadata, got %#v", diagnostics.Tuning)
	}
}

func TestBuildCostRouteTuningIdentifiesDisabledCandidates(t *testing.T) {
	snapshot := session.Snapshot{PromptBudgets: []schema.PromptBudget{
		{AgentID: "chat", Mode: "chat", EstimatedPromptTokens: 2100, CacheablePrefixTokens: 900, PromptPrefixHash: "a"},
		{AgentID: "chat", Mode: "chat", EstimatedPromptTokens: 2300, CacheablePrefixTokens: 900, PromptPrefixHash: "b"},
		{AgentID: "planner", Mode: "plan", EstimatedPromptTokens: 2200, CacheablePrefixTokens: 900, PromptPrefixHash: "c"},
	}}
	diagnostics := promptCostDiagnostics(snapshot)
	tuning := buildCostRouteTuning(diagnostics, snapshot, []auxiliarySummary{{Kind: "router"}, {Kind: "summarizer"}})
	router := costRouteTuningByKind(tuning, "router")
	if router == nil || router.State != "candidate" || router.CandidateSamples != 3 || router.CandidateAveragePromptTokens == 0 || router.Action == "" {
		t.Fatalf("expected router candidate tuning, got %#v", tuning)
	}
	summarizer := costRouteTuningByKind(tuning, "summarizer")
	if summarizer == nil || summarizer.State != "not_enough_data" {
		t.Fatalf("expected summarizer to wait for measured summary samples, got %#v", tuning)
	}
}

func costRouteTuningByKind(items []costRouteTuning, kind string) *costRouteTuning {
	for i := range items {
		if items[i].Kind == kind {
			return &items[i]
		}
	}
	return nil
}

func costFeatureExists(items []costControlFeature, code, state string) bool {
	for _, item := range items {
		if item.Code == code && item.State == state {
			return true
		}
	}
	return false
}

func costFeatureByCode(items []costControlFeature, code string) *costControlFeature {
	for i := range items {
		if items[i].Code == code {
			return &items[i]
		}
	}
	return nil
}

func TestServerSessionArtifactEndpointsAndExpansion(t *testing.T) {
	runtimeHome := t.TempDir()
	workspaceRoot := t.TempDir()
	state := session.New(8)
	artifact := state.AddArtifact(session.SessionArtifactSnapshot{Kind: "tool_result", ToolName: "fetch_url", Summary: "summary only", Content: "full artifact body"})
	cfg := &config.Config{
		RuntimeHome:   runtimeHome,
		WorkspaceRoot: workspaceRoot,
		ConfigPath:    filepath.Join(runtimeHome, "configs", "goflow.yaml"),
		Session:       config.SessionConfig{MaxHistory: 8},
		DefaultAgent:  "chat",
		Agents: map[string]config.AgentProfile{
			"chat": {Name: "Chat", Provider: "primary", Mode: "chat", Model: "test-model", MaxIterations: 1, AllowedToolKinds: []config.ToolKind{config.ToolKindRead}, ToolPolicy: config.ToolPolicyAllow},
		},
	}
	runtimeRef, err := agent.NewRuntime(cfg, map[string]interfaces.LLMClient{"primary": &apiTestLLM{}}, apiTestSkillManager{}, &apiTestMCP{}, state, runtime.NewAuditLogger(false, false))
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	server := NewServer(runtimeRef)

	listReq := httptest.NewRequest(http.MethodGet, "/api/session-artifacts", nil)
	listResp := httptest.NewRecorder()
	server.ServeHTTP(listResp, listReq)
	if listResp.Code != http.StatusOK {
		t.Fatalf("expected artifact list 200, got %d body=%s", listResp.Code, listResp.Body.String())
	}
	if strings.Contains(listResp.Body.String(), "full artifact body") {
		t.Fatalf("artifact list should omit content by default, got %s", listResp.Body.String())
	}

	detailReq := httptest.NewRequest(http.MethodGet, "/api/session-artifacts/"+artifact.ID, nil)
	detailResp := httptest.NewRecorder()
	server.ServeHTTP(detailResp, detailReq)
	if detailResp.Code != http.StatusOK || !strings.Contains(detailResp.Body.String(), "full artifact body") {
		t.Fatalf("expected artifact detail with content, got %d body=%s", detailResp.Code, detailResp.Body.String())
	}

	expanded, err := server.expandAtReferences(context.Background(), "summarize "+artifact.Ref, nil)
	if err != nil {
		t.Fatalf("expand artifact ref: %v", err)
	}
	if !strings.Contains(expanded, "Referenced GoFlow artifacts") || !strings.Contains(expanded, "full artifact body") {
		t.Fatalf("expected artifact content in expanded prompt, got %q", expanded)
	}
}

func TestServerMemoryEndpoints(t *testing.T) {
	root := t.TempDir()
	runtimeRef := newAPITestRuntime(t)
	store := memory.NewStore(root)
	if err := store.Ensure(); err != nil {
		t.Fatalf("Ensure memory store: %v", err)
	}
	runtimeRef.SetMemoryStore(store)
	if _, err := store.RecordTask(memory.TaskSummary{
		UserGoal:        "fix login validation",
		KeyDecisions:    []string{"validate email before saving"},
		ModifiedFiles:   []string{"internal/auth/login.go"},
		TestResults:     []string{"go test ./internal/auth passed"},
		ReusableLessons: []string{"store validation decisions as reusable solution memory"},
	}); err != nil {
		t.Fatalf("RecordTask: %v", err)
	}
	solutions, err := store.Solutions()
	if err != nil {
		t.Fatalf("Solutions: %v", err)
	}
	if len(solutions.Solutions) == 0 {
		t.Fatalf("expected learned solution")
	}
	solutionID := solutions.Solutions[0].ID
	server := NewServer(runtimeRef)

	updateReq := httptest.NewRequest(http.MethodPut, "/api/memory/project", strings.NewReader(`{"content":"# Project Memory\n\n## Project Goal\n- Ship login flow\n"}`))
	updateReq.Header.Set("Content-Type", "application/json")
	updateResp := httptest.NewRecorder()
	server.ServeHTTP(updateResp, updateReq)
	if updateResp.Code != http.StatusOK || !strings.Contains(updateResp.Body.String(), "Ship login flow") {
		t.Fatalf("expected project update, got %d body=%s", updateResp.Code, updateResp.Body.String())
	}

	searchReq := httptest.NewRequest(http.MethodGet, "/api/memory/search?q=login", nil)
	searchResp := httptest.NewRecorder()
	server.ServeHTTP(searchResp, searchReq)
	if searchResp.Code != http.StatusOK || !strings.Contains(searchResp.Body.String(), `"kind":"task"`) {
		t.Fatalf("expected memory search hit, got %d body=%s", searchResp.Code, searchResp.Body.String())
	}

	dashboardReq := httptest.NewRequest(http.MethodGet, "/api/memory", nil)
	dashboardResp := httptest.NewRecorder()
	server.ServeHTTP(dashboardResp, dashboardReq)
	if dashboardResp.Code != http.StatusOK || !strings.Contains(dashboardResp.Body.String(), `"project"`) || !strings.Contains(dashboardResp.Body.String(), `"file_index"`) {
		t.Fatalf("expected memory dashboard, got %d body=%s", dashboardResp.Code, dashboardResp.Body.String())
	}

	retireReq := httptest.NewRequest(http.MethodPost, "/api/memory/solutions/"+solutionID+"/retire", strings.NewReader(`{"reason":"schema changed","superseded_by":"sol-next"}`))
	retireReq.Header.Set("Content-Type", "application/json")
	retireResp := httptest.NewRecorder()
	server.ServeHTTP(retireResp, retireReq)
	if retireResp.Code != http.StatusOK || !strings.Contains(retireResp.Body.String(), `"retired":true`) || !strings.Contains(retireResp.Body.String(), `"superseded_by":"sol-next"`) {
		t.Fatalf("expected retired solution response, got %d body=%s", retireResp.Code, retireResp.Body.String())
	}

	restoreReq := httptest.NewRequest(http.MethodPost, "/api/memory/solutions/"+solutionID+"/restore", nil)
	restoreResp := httptest.NewRecorder()
	server.ServeHTTP(restoreResp, restoreReq)
	if restoreResp.Code != http.StatusOK || strings.Contains(restoreResp.Body.String(), `"retired":true`) {
		t.Fatalf("expected restored solution response, got %d body=%s", restoreResp.Code, restoreResp.Body.String())
	}
}

func TestServerSessionCompactEndpoint(t *testing.T) {
	root := t.TempDir()
	state := session.New(8)
	state.AddPrompt("reduce token cost by compacting current context")
	state.SetPromptBudget(schema.PromptBudget{
		AgentID:                    "chat",
		Mode:                       "chat",
		EstimatedPromptTokens:      13000,
		MemoryEstimatedSavedTokens: 900,
		ArtifactRefCount:           1,
		ArtifactRefs:               []string{"sha256:abc"},
	})
	runtimeRef := newAPITestRuntimeWithStateAndResponses(t, state, nil)
	store := memory.NewStore(root)
	if err := store.Ensure(); err != nil {
		t.Fatalf("Ensure memory store: %v", err)
	}
	runtimeRef.SetMemoryStore(store)
	server := NewServer(runtimeRef)

	req := httptest.NewRequest(http.MethodPost, "/api/session/compact", strings.NewReader(`{"reason":"manual test"}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected compact 200, got %d body=%s", resp.Code, resp.Body.String())
	}
	var summary memory.ContextSummary
	if err := json.NewDecoder(resp.Body).Decode(&summary); err != nil {
		t.Fatalf("decode compact summary: %v", err)
	}
	if summary.ID == "" || !strings.Contains(summary.Reason, "manual test") || summary.PromptBudget.EstimatedPromptTokens != 13000 {
		t.Fatalf("unexpected compact summary: %#v", summary)
	}
	dashboard, err := store.Dashboard(5)
	if err != nil {
		t.Fatalf("Dashboard: %v", err)
	}
	if dashboard.Context.ID != summary.ID {
		t.Fatalf("expected dashboard context %q, got %#v", summary.ID, dashboard.Context)
	}
}

func TestServerMemoryDashboardBackfillsWorkflowTaskMemory(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# GoFlow Agent\n\nWorkflow memory dashboard.\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	state := session.New(8)
	completedID := state.StartWorkflowRun("audit-flow", "review auth module")
	state.CompleteWorkflowRun(completedID, "completed", "audit complete", "", "", nil, []session.WorkflowRunStageSnapshot{{
		Stage:   "review",
		AgentID: "auditor",
		Status:  "completed",
		Summary: "reviewed auth changes",
		Result: schema.AgentResult{
			Output: "auth review complete",
			Changes: []schema.Change{{
				Summary: "updated login validation",
				Files:   []string{"internal/auth/login.go"},
			}},
			Verification: []schema.Verification{{
				Kind:   "test",
				Status: "passed",
				Detail: "go test ./internal/auth passed",
			}},
		},
	}})
	failedID := state.StartWorkflowRun("deploy-flow", "ship release")
	state.CompleteWorkflowRun(failedID, "failed", "deployment failed", "", "", nil, []session.WorkflowRunStageSnapshot{{
		Stage:   "deploy",
		AgentID: "fixer",
		Status:  "failed",
		Summary: "deploy error",
		Result: schema.AgentResult{
			Output: "release failed",
			Findings: []schema.Finding{{
				Summary: "missing environment guard",
				Files:   []string{"internal/deploy/release.go"},
			}},
			Verification: []schema.Verification{{
				Kind:   "test",
				Status: "failed",
				Detail: "go test ./internal/deploy failed",
			}},
		},
	}})

	runtimeRef := newAPITestRuntimeWithStateAndResponses(t, state, nil)
	store := memory.NewStore(root)
	if err := store.Ensure(); err != nil {
		t.Fatalf("Ensure memory store: %v", err)
	}
	runtimeRef.SetMemoryStore(store)
	server := NewServer(runtimeRef)

	req := httptest.NewRequest(http.MethodGet, "/api/memory?tasks=10", nil)
	resp := httptest.NewRecorder()
	server.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected memory dashboard 200, got %d body=%s", resp.Code, resp.Body.String())
	}
	var dashboard memory.Dashboard
	if err := json.NewDecoder(resp.Body).Decode(&dashboard); err != nil {
		t.Fatalf("decode dashboard: %v", err)
	}
	if len(dashboard.Tasks) < 2 {
		t.Fatalf("expected workflow task backfill, got %#v", dashboard.Tasks)
	}
	taskIDs := make([]string, 0, len(dashboard.Tasks))
	for _, task := range dashboard.Tasks {
		taskIDs = append(taskIDs, task.ID)
	}
	if !testStringSliceContains(taskIDs, "workflow-"+completedID) || !testStringSliceContains(taskIDs, "workflow-"+failedID) {
		t.Fatalf("expected workflow task IDs in backfilled tasks, got %#v", taskIDs)
	}
	if len(dashboard.Errors.Errors) == 0 {
		t.Fatalf("expected failed workflow to populate error knowledge, got %#v", dashboard.Errors)
	}
	if !strings.Contains(dashboard.Project.Content, "GoFlow Agent") {
		t.Fatalf("expected seeded project profile in dashboard, got %q", dashboard.Project.Content)
	}
	if dashboard.FileIndex.TotalFiles == 0 {
		t.Fatalf("expected project profile generation to refresh file index, got %#v", dashboard.FileIndex)
	}
}

func TestServerArtifactObjectEndpoint(t *testing.T) {
	root := t.TempDir()
	state := session.New(8)
	store := session.NewArtifactObjectStore(root)
	if err := store.Ensure(); err != nil {
		t.Fatalf("Ensure artifact store: %v", err)
	}
	state.SetArtifactObjectStore(store)
	artifact := state.AddArtifact(session.SessionArtifactSnapshot{Kind: "tool_result", ToolName: "fetch_url", Summary: "summary only", Content: "full artifact body"})
	runtimeRef := newAPITestRuntimeWithStateAndResponses(t, state, nil)
	server := NewServer(runtimeRef)

	req := httptest.NewRequest(http.MethodGet, "/api/artifacts/"+artifact.Hash, nil)
	resp := httptest.NewRecorder()
	server.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected artifact metadata 200, got %d body=%s", resp.Code, resp.Body.String())
	}
	if strings.Contains(resp.Body.String(), "full artifact body") {
		t.Fatalf("metadata endpoint should omit content by default, got %s", resp.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/artifacts?limit=1", nil)
	listResp := httptest.NewRecorder()
	server.ServeHTTP(listResp, listReq)
	if listResp.Code != http.StatusOK {
		t.Fatalf("expected artifact list 200, got %d body=%s", listResp.Code, listResp.Body.String())
	}
	if !strings.Contains(listResp.Body.String(), artifact.Hash) || strings.Contains(listResp.Body.String(), "full artifact body") {
		t.Fatalf("expected artifact list metadata only, got %s", listResp.Body.String())
	}

	detailReq := httptest.NewRequest(http.MethodGet, "/api/artifacts/"+artifact.Hash+"?content=1", nil)
	detailResp := httptest.NewRecorder()
	server.ServeHTTP(detailResp, detailReq)
	if detailResp.Code != http.StatusOK || !strings.Contains(detailResp.Body.String(), "full artifact body") {
		t.Fatalf("expected artifact object content, got %d body=%s", detailResp.Code, detailResp.Body.String())
	}
}

func TestServerExpandAtFileReferencesUsesWorkspaceSafety(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("http file context\n"), 0o644); err != nil {
		t.Fatalf("write notes: %v", err)
	}
	runtimeRef := newAPITestRuntime(t)
	store := memory.NewStore(root)
	if _, err := store.RebuildFiles(context.Background()); err != nil {
		t.Fatalf("RebuildFiles: %v", err)
	}
	runtimeRef.SetMemoryStore(store)
	server := NewServerWithWorkspace(runtimeRef, workspace.New(root, true))
	expanded, err := server.expandAtReferences(context.Background(), "summarize @notes.txt", nil)
	if err != nil {
		t.Fatalf("expand http @file: %v", err)
	}
	if !strings.Contains(expanded, "Referenced workspace files") || !strings.Contains(expanded, "http file context") {
		t.Fatalf("expected HTTP @file content in prompt, got %q", expanded)
	}
	index, err := store.FileIndex()
	if err != nil {
		t.Fatalf("FileIndex: %v", err)
	}
	if len(index.Files) != 1 || index.Files[0].LastReadAt == "" {
		t.Fatalf("expected HTTP @file expansion to mark file usage, got %#v", index.Files)
	}

	if _, err := server.expandAtReferences(context.Background(), "summarize @../outside.txt", nil); err == nil || !strings.Contains(err.Error(), "escapes workspace root") {
		t.Fatalf("expected HTTP @file traversal rejection, got %v", err)
	}
}

func TestServerExpandAtLargeFileReferenceUsesSummary(t *testing.T) {
	root := t.TempDir()
	largeContent := strings.Repeat("large http file context\n", 4200) + "SECRET_FULL_CONTENT_SHOULD_NOT_APPEAR\n"
	if err := os.WriteFile(filepath.Join(root, "large.txt"), []byte(largeContent), 0o644); err != nil {
		t.Fatalf("write large file: %v", err)
	}
	runtimeRef := newAPITestRuntime(t)
	store := memory.NewStore(root)
	if _, err := store.RebuildFiles(context.Background()); err != nil {
		t.Fatalf("RebuildFiles: %v", err)
	}
	runtimeRef.SetMemoryStore(store)
	server := NewServerWithWorkspace(runtimeRef, workspace.New(root, true))

	expanded, err := server.expandAtReferences(context.Background(), "summarize @large.txt", nil)
	if err != nil {
		t.Fatalf("expand large http @file: %v", err)
	}
	if !strings.Contains(expanded, "summary-only") || !strings.Contains(expanded, "hash=") || !strings.Contains(expanded, "summary:") {
		t.Fatalf("expected summary-only large @file expansion, got %q", expanded)
	}
	if strings.Contains(expanded, "SECRET_FULL_CONTENT_SHOULD_NOT_APPEAR") || strings.Contains(expanded, strings.Repeat("large http file context\n", 20)) {
		t.Fatalf("expected large @file expansion to omit full content, got %q", expanded)
	}
	index, err := store.FileIndex()
	if err != nil {
		t.Fatalf("FileIndex: %v", err)
	}
	if len(index.Files) != 1 || index.Files[0].LastReadAt == "" {
		t.Fatalf("expected large HTTP @file expansion to mark file usage, got %#v", index.Files)
	}
}

func TestServerExpandAtFileReferencesRejectsNonUTF8Text(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "binary.bin"), []byte{0xff, 0xfe, 0xfd}, 0o644); err != nil {
		t.Fatalf("write binary file: %v", err)
	}
	server := NewServerWithWorkspace(newAPITestRuntime(t), workspace.New(root, true))
	if _, err := server.expandAtReferences(context.Background(), "summarize @binary.bin", nil); err == nil || !strings.Contains(err.Error(), "valid UTF-8") {
		t.Fatalf("expected HTTP @file UTF-8 rejection, got %v", err)
	}
}

func TestServerExpandAtFileReferencesRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "outside-link.txt")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	server := NewServerWithWorkspace(newAPITestRuntime(t), workspace.New(root, true))
	_, err := server.expandAtReferences(context.Background(), "summarize @outside-link.txt", nil)
	if err == nil || (!strings.Contains(err.Error(), "resolve real path") && !strings.Contains(err.Error(), "resolves outside workspace root")) {
		t.Fatalf("expected HTTP @file symlink escape rejection, got %v", err)
	}
}

func TestServerWorkflowEditorPageAndOptions(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	server := NewServer(runtimeRef)

	pageRequest := httptest.NewRequest(http.MethodGet, "/workflows", nil)
	pageResponse := httptest.NewRecorder()
	server.ServeHTTP(pageResponse, pageRequest)
	if pageResponse.Code != http.StatusOK {
		t.Fatalf("expected editor 200, got %d", pageResponse.Code)
	}
	if !strings.Contains(pageResponse.Body.String(), "GoFlow Console") || !strings.Contains(pageResponse.Body.String(), "data-app-shell") ||
		!strings.Contains(pageResponse.Body.String(), `data-view="workflows"`) || !strings.Contains(pageResponse.Body.String(), "/assets/app.js") {
		t.Fatalf("expected GoFlow Studio HTML, got %s", pageResponse.Body.String())
	}

	optionsRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-options", nil)
	optionsResponse := httptest.NewRecorder()
	server.ServeHTTP(optionsResponse, optionsRequest)
	if optionsResponse.Code != http.StatusOK {
		t.Fatalf("expected options 200, got %d", optionsResponse.Code)
	}
	if !strings.Contains(optionsResponse.Body.String(), `"planner"`) || !strings.Contains(optionsResponse.Body.String(), `"execution-plan"`) ||
		!strings.Contains(optionsResponse.Body.String(), `"node_types"`) || !strings.Contains(optionsResponse.Body.String(), `"input_gate"`) || !strings.Contains(optionsResponse.Body.String(), `"type":"team"`) ||
		!strings.Contains(optionsResponse.Body.String(), `"policy_rules"`) || !strings.Contains(optionsResponse.Body.String(), `"risk_at_least"`) ||
		!strings.Contains(optionsResponse.Body.String(), `"expression_functions"`) || !strings.Contains(optionsResponse.Body.String(), `"risk_rank"`) ||
		!strings.Contains(optionsResponse.Body.String(), `"templates"`) || !strings.Contains(optionsResponse.Body.String(), `"plan-fix-audit"`) ||
		!strings.Contains(optionsResponse.Body.String(), `"team_templates"`) || !strings.Contains(optionsResponse.Body.String(), `"software-task-team"`) ||
		!strings.Contains(optionsResponse.Body.String(), `"workflow_executors"`) || !strings.Contains(optionsResponse.Body.String(), `"source":"legacy_executor"`) ||
		!strings.Contains(optionsResponse.Body.String(), `"compatibility":true`) || !strings.Contains(optionsResponse.Body.String(), `"skill-chain"`) {
		t.Fatalf("expected workflow options, got %s", optionsResponse.Body.String())
	}

	nodeMetadataValidateRequest := httptest.NewRequest(http.MethodPost, "/api/resources/workflow-node-metadata/policy_guard/validate", strings.NewReader(`{
		"type": "policy_guard",
		"label": "Review Gate",
		"description": "Custom review gate copy.",
		"hints": ["Use approval_preset for reusable team quorum."],
		"fields": [{"name":"params.approval_preset","label":"Approval preset","type":"text"}]
	}`))
	nodeMetadataValidateRequest.Header.Set("Content-Type", "application/json")
	nodeMetadataValidateResponse := httptest.NewRecorder()
	server.ServeHTTP(nodeMetadataValidateResponse, nodeMetadataValidateRequest)
	if nodeMetadataValidateResponse.Code != http.StatusOK || !strings.Contains(nodeMetadataValidateResponse.Body.String(), `"valid":true`) || !strings.Contains(nodeMetadataValidateResponse.Body.String(), `"resource":"workflow_node_metadata"`) || !strings.Contains(nodeMetadataValidateResponse.Body.String(), `"normalized"`) {
		t.Fatalf("expected valid workflow node metadata dry-run, got %d body=%s", nodeMetadataValidateResponse.Code, nodeMetadataValidateResponse.Body.String())
	}
	if _, ok := runtimeRef.WorkflowNodeMetadataResource("policy_guard"); ok {
		t.Fatalf("expected workflow node metadata dry-run not to write resource")
	}
	invalidNodeMetadataValidateRequest := httptest.NewRequest(http.MethodPost, "/api/resources/workflow-node-metadata/unknown_node/validate", strings.NewReader(`{
		"type": "unknown_node",
		"label": "Unknown"
	}`))
	invalidNodeMetadataValidateRequest.Header.Set("Content-Type", "application/json")
	invalidNodeMetadataValidateResponse := httptest.NewRecorder()
	server.ServeHTTP(invalidNodeMetadataValidateResponse, invalidNodeMetadataValidateRequest)
	if invalidNodeMetadataValidateResponse.Code != http.StatusOK || !strings.Contains(invalidNodeMetadataValidateResponse.Body.String(), `"valid":false`) || !strings.Contains(invalidNodeMetadataValidateResponse.Body.String(), `"field":"type"`) || !strings.Contains(invalidNodeMetadataValidateResponse.Body.String(), `"code":"unsupported_value"`) || !strings.Contains(invalidNodeMetadataValidateResponse.Body.String(), `"recommendation"`) {
		t.Fatalf("expected invalid workflow node metadata dry-run, got %d body=%s", invalidNodeMetadataValidateResponse.Code, invalidNodeMetadataValidateResponse.Body.String())
	}

	nodeMetadataRequest := httptest.NewRequest(http.MethodPut, "/api/resources/workflow-node-metadata/policy_guard", strings.NewReader(`{
		"type": "policy_guard",
		"label": "Review Gate",
		"description": "Custom review gate copy.",
		"hints": ["Use approval_preset for reusable team quorum."],
		"fields": [{"name":"params.approval_preset","label":"Approval preset","type":"text"}]
	}`))
	nodeMetadataRequest.Header.Set("Content-Type", "application/json")
	nodeMetadataResponse := httptest.NewRecorder()
	server.ServeHTTP(nodeMetadataResponse, nodeMetadataRequest)
	if nodeMetadataResponse.Code != http.StatusOK || !strings.Contains(nodeMetadataResponse.Body.String(), `"kind":"goflow.workflow_node_metadata"`) || !strings.Contains(nodeMetadataResponse.Body.String(), `"custom":true`) {
		t.Fatalf("expected workflow node metadata save, got %d body=%s", nodeMetadataResponse.Code, nodeMetadataResponse.Body.String())
	}
	expressionMetadataValidateRequest := httptest.NewRequest(http.MethodPost, "/api/resources/expression-helpers/risk_rank/validate", strings.NewReader(`{
		"name": "risk_rank",
		"label": "Severity Rank",
		"description": "Custom severity helper copy.",
		"hints": ["Use risk_rank(ref) >= 4 for high-or-above routing."]
	}`))
	expressionMetadataValidateRequest.Header.Set("Content-Type", "application/json")
	expressionMetadataValidateResponse := httptest.NewRecorder()
	server.ServeHTTP(expressionMetadataValidateResponse, expressionMetadataValidateRequest)
	if expressionMetadataValidateResponse.Code != http.StatusOK || !strings.Contains(expressionMetadataValidateResponse.Body.String(), `"valid":true`) || !strings.Contains(expressionMetadataValidateResponse.Body.String(), `"resource":"expression_helper"`) || !strings.Contains(expressionMetadataValidateResponse.Body.String(), `"normalized"`) {
		t.Fatalf("expected valid expression helper metadata dry-run, got %d body=%s", expressionMetadataValidateResponse.Code, expressionMetadataValidateResponse.Body.String())
	}
	if _, ok := runtimeRef.WorkflowExpressionMetadataResource("risk_rank"); ok {
		t.Fatalf("expected expression helper metadata dry-run not to write resource")
	}
	invalidExpressionMetadataValidateRequest := httptest.NewRequest(http.MethodPost, "/api/resources/expression-helpers/unknown_helper/validate", strings.NewReader(`{
		"name": "unknown_helper",
		"label": "Unknown"
	}`))
	invalidExpressionMetadataValidateRequest.Header.Set("Content-Type", "application/json")
	invalidExpressionMetadataValidateResponse := httptest.NewRecorder()
	server.ServeHTTP(invalidExpressionMetadataValidateResponse, invalidExpressionMetadataValidateRequest)
	if invalidExpressionMetadataValidateResponse.Code != http.StatusOK || !strings.Contains(invalidExpressionMetadataValidateResponse.Body.String(), `"valid":false`) || !strings.Contains(invalidExpressionMetadataValidateResponse.Body.String(), `"field":"name"`) || !strings.Contains(invalidExpressionMetadataValidateResponse.Body.String(), `"code":"unsupported_value"`) || !strings.Contains(invalidExpressionMetadataValidateResponse.Body.String(), `"recommendation"`) {
		t.Fatalf("expected invalid expression helper metadata dry-run, got %d body=%s", invalidExpressionMetadataValidateResponse.Code, invalidExpressionMetadataValidateResponse.Body.String())
	}

	expressionMetadataRequest := httptest.NewRequest(http.MethodPut, "/api/resources/expression-helpers/risk_rank", strings.NewReader(`{
		"name": "risk_rank",
		"label": "Severity Rank",
		"description": "Custom severity helper copy.",
		"hints": ["Use risk_rank(ref) >= 4 for high-or-above routing."]
	}`))
	expressionMetadataRequest.Header.Set("Content-Type", "application/json")
	expressionMetadataResponse := httptest.NewRecorder()
	server.ServeHTTP(expressionMetadataResponse, expressionMetadataRequest)
	if expressionMetadataResponse.Code != http.StatusOK || !strings.Contains(expressionMetadataResponse.Body.String(), `"kind":"goflow.workflow_expression_function"`) || !strings.Contains(expressionMetadataResponse.Body.String(), `"custom":true`) {
		t.Fatalf("expected expression helper metadata save, got %d body=%s", expressionMetadataResponse.Code, expressionMetadataResponse.Body.String())
	}
	metadataOptionsRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-options", nil)
	metadataOptionsResponse := httptest.NewRecorder()
	server.ServeHTTP(metadataOptionsResponse, metadataOptionsRequest)
	if metadataOptionsResponse.Code != http.StatusOK || !strings.Contains(metadataOptionsResponse.Body.String(), `"label":"Review Gate"`) || !strings.Contains(metadataOptionsResponse.Body.String(), `"label":"Severity Rank"`) || !strings.Contains(metadataOptionsResponse.Body.String(), `"source":"built_in+custom_metadata"`) {
		t.Fatalf("expected metadata-enriched workflow options, got %d body=%s", metadataOptionsResponse.Code, metadataOptionsResponse.Body.String())
	}
	nodeMetadataListRequest := httptest.NewRequest(http.MethodGet, "/api/resources/workflow-node-metadata", nil)
	nodeMetadataListResponse := httptest.NewRecorder()
	server.ServeHTTP(nodeMetadataListResponse, nodeMetadataListRequest)
	if nodeMetadataListResponse.Code != http.StatusOK || !strings.Contains(nodeMetadataListResponse.Body.String(), `"name":"policy_guard"`) {
		t.Fatalf("expected workflow node metadata resource list, got %d body=%s", nodeMetadataListResponse.Code, nodeMetadataListResponse.Body.String())
	}
	expressionMetadataListRequest := httptest.NewRequest(http.MethodGet, "/api/resources/expression-helpers", nil)
	expressionMetadataListResponse := httptest.NewRecorder()
	server.ServeHTTP(expressionMetadataListResponse, expressionMetadataListRequest)
	if expressionMetadataListResponse.Code != http.StatusOK || !strings.Contains(expressionMetadataListResponse.Body.String(), `"name":"risk_rank"`) {
		t.Fatalf("expected expression helper metadata resource list, got %d body=%s", expressionMetadataListResponse.Code, expressionMetadataListResponse.Body.String())
	}

	functionRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-expression-functions?mode=condition&node_type=policy_guard", nil)
	functionResponse := httptest.NewRecorder()
	server.ServeHTTP(functionResponse, functionRequest)
	if functionResponse.Code != http.StatusOK {
		t.Fatalf("expected expression function catalog 200, got %d", functionResponse.Code)
	}
	if !strings.Contains(functionResponse.Body.String(), `"signature":"risk_rank(value)"`) ||
		!strings.Contains(functionResponse.Body.String(), `"signature":"matches(value, pattern)"`) ||
		!strings.Contains(functionResponse.Body.String(), `"min_args":1`) {
		t.Fatalf("expected expression function metadata, got %s", functionResponse.Body.String())
	}

	templateListRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-templates", nil)
	templateListResponse := httptest.NewRecorder()
	server.ServeHTTP(templateListResponse, templateListRequest)
	if templateListResponse.Code != http.StatusOK {
		t.Fatalf("expected template list 200, got %d", templateListResponse.Code)
	}
	if !strings.Contains(templateListResponse.Body.String(), `"web-research-risk"`) ||
		!strings.Contains(templateListResponse.Body.String(), `"human-input-security-review"`) ||
		!strings.Contains(templateListResponse.Body.String(), `"software-quality-gate"`) ||
		!strings.Contains(templateListResponse.Body.String(), `"engineering-parallel-delivery"`) ||
		!strings.Contains(templateListResponse.Body.String(), `"security-audit-evidence-gate"`) ||
		!strings.Contains(templateListResponse.Body.String(), `"task-decomposition-plan"`) ||
		!strings.Contains(templateListResponse.Body.String(), `"multi-domain-intake-router"`) {
		t.Fatalf("expected reusable workflow templates, got %s", templateListResponse.Body.String())
	}
	if !strings.Contains(templateListResponse.Body.String(), `"node_types"`) ||
		!strings.Contains(templateListResponse.Body.String(), `"team_templates"`) ||
		!strings.Contains(templateListResponse.Body.String(), `"policy_rules"`) ||
		!strings.Contains(templateListResponse.Body.String(), `"has_control_flow":true`) ||
		!strings.Contains(templateListResponse.Body.String(), `"has_data_flow":true`) {
		t.Fatalf("expected workflow template composition metadata, got %s", templateListResponse.Body.String())
	}
	workflowOptionsRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-options", nil)
	workflowOptionsResponse := httptest.NewRecorder()
	server.ServeHTTP(workflowOptionsResponse, workflowOptionsRequest)
	if workflowOptionsResponse.Code != http.StatusOK || !strings.Contains(workflowOptionsResponse.Body.String(), `"providers"`) || !strings.Contains(workflowOptionsResponse.Body.String(), `"name":"primary"`) {
		t.Fatalf("expected workflow options to include provider choices, got %d body=%s", workflowOptionsResponse.Code, workflowOptionsResponse.Body.String())
	}

	templateRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-templates/human-input-security-review", nil)
	templateResponse := httptest.NewRecorder()
	server.ServeHTTP(templateResponse, templateRequest)
	if templateResponse.Code != http.StatusOK {
		t.Fatalf("expected template 200, got %d", templateResponse.Code)
	}
	if !strings.Contains(templateResponse.Body.String(), `"graph"`) || !strings.Contains(templateResponse.Body.String(), `"input_gate"`) || !strings.Contains(templateResponse.Body.String(), `"condition"`) {
		t.Fatalf("expected detailed workflow template graph, got %s", templateResponse.Body.String())
	}
	qualityTemplateRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-templates/software-quality-gate", nil)
	qualityTemplateResponse := httptest.NewRecorder()
	server.ServeHTTP(qualityTemplateResponse, qualityTemplateRequest)
	if qualityTemplateResponse.Code != http.StatusOK || !strings.Contains(qualityTemplateResponse.Body.String(), `"quality_gate"`) || !strings.Contains(qualityTemplateResponse.Body.String(), `"acceptance_criteria"`) || !strings.Contains(qualityTemplateResponse.Body.String(), `"artifacts"`) {
		t.Fatalf("expected quality-gated workflow template graph, got %d body=%s", qualityTemplateResponse.Code, qualityTemplateResponse.Body.String())
	}
	decompositionTemplateRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-templates/task-decomposition-plan", nil)
	decompositionTemplateResponse := httptest.NewRecorder()
	server.ServeHTTP(decompositionTemplateResponse, decompositionTemplateRequest)
	if decompositionTemplateResponse.Code != http.StatusOK ||
		!strings.Contains(decompositionTemplateResponse.Body.String(), `"category":"planning"`) ||
		!strings.Contains(decompositionTemplateResponse.Body.String(), `"workflow_draft"`) ||
		!strings.Contains(decompositionTemplateResponse.Body.String(), `"quality_gate"`) ||
		!strings.Contains(decompositionTemplateResponse.Body.String(), `"fields_json"`) {
		t.Fatalf("expected task decomposition template graph, got %d body=%s", decompositionTemplateResponse.Code, decompositionTemplateResponse.Body.String())
	}
	multiDomainTemplateRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-templates/multi-domain-intake-router", nil)
	multiDomainTemplateResponse := httptest.NewRecorder()
	server.ServeHTTP(multiDomainTemplateResponse, multiDomainTemplateRequest)
	if multiDomainTemplateResponse.Code != http.StatusOK ||
		!strings.Contains(multiDomainTemplateResponse.Body.String(), `"category":"starter"`) ||
		!strings.Contains(multiDomainTemplateResponse.Body.String(), `"decompose"`) ||
		!strings.Contains(multiDomainTemplateResponse.Body.String(), `"active_branches_ref"`) ||
		!strings.Contains(multiDomainTemplateResponse.Body.String(), `"wait_for_ref"`) ||
		!strings.Contains(multiDomainTemplateResponse.Body.String(), `"software-worker"`) ||
		!strings.Contains(multiDomainTemplateResponse.Body.String(), `"ops-worker"`) ||
		!strings.Contains(multiDomainTemplateResponse.Body.String(), `"quality_gate"`) {
		t.Fatalf("expected multi-domain starter template graph, got %d body=%s", multiDomainTemplateResponse.Code, multiDomainTemplateResponse.Body.String())
	}
	if !strings.Contains(multiDomainTemplateResponse.Body.String(), `"team_templates":["`) ||
		!strings.Contains(multiDomainTemplateResponse.Body.String(), `"agents":["`) ||
		!strings.Contains(multiDomainTemplateResponse.Body.String(), `"software-engineer"`) ||
		!strings.Contains(multiDomainTemplateResponse.Body.String(), `"web-security-researcher"`) ||
		!strings.Contains(multiDomainTemplateResponse.Body.String(), `"node_type":"parallel"`) ||
		!strings.Contains(multiDomainTemplateResponse.Body.String(), `"node_type":"join"`) ||
		!strings.Contains(multiDomainTemplateResponse.Body.String(), `"has_quality_gate":true`) {
		t.Fatalf("expected multi-domain starter template detail metadata, got %d body=%s", multiDomainTemplateResponse.Code, multiDomainTemplateResponse.Body.String())
	}
	parallelDeliveryTemplateRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-templates/engineering-parallel-delivery", nil)
	parallelDeliveryTemplateResponse := httptest.NewRecorder()
	server.ServeHTTP(parallelDeliveryTemplateResponse, parallelDeliveryTemplateRequest)
	if parallelDeliveryTemplateResponse.Code != http.StatusOK ||
		!strings.Contains(parallelDeliveryTemplateResponse.Body.String(), `"model":{"provider":"primary"`) ||
		!strings.Contains(parallelDeliveryTemplateResponse.Body.String(), `"model":{"provider":"backup"`) ||
		!strings.Contains(parallelDeliveryTemplateResponse.Body.String(), `"node_type":"parallel"`) ||
		!strings.Contains(parallelDeliveryTemplateResponse.Body.String(), `"node_type":"join"`) ||
		!strings.Contains(parallelDeliveryTemplateResponse.Body.String(), `"node_type":"quality_gate"`) ||
		!strings.Contains(parallelDeliveryTemplateResponse.Body.String(), `"quality-resolution-plan"`) {
		t.Fatalf("expected parallel engineering delivery template with model routes, got %d body=%s", parallelDeliveryTemplateResponse.Code, parallelDeliveryTemplateResponse.Body.String())
	}

	forkTemplateRequest := httptest.NewRequest(http.MethodPost, "/api/resources/workflow-templates/forked-security-review/fork", strings.NewReader(`{
		"source": "human-input-security-review",
		"title": "Forked Security Review",
		"description": "Editable copy of the built-in security review template."
	}`))
	forkTemplateRequest.Header.Set("Content-Type", "application/json")
	forkTemplateResponse := httptest.NewRecorder()
	server.ServeHTTP(forkTemplateResponse, forkTemplateRequest)
	if forkTemplateResponse.Code != http.StatusCreated || !strings.Contains(forkTemplateResponse.Body.String(), `"name":"forked-security-review"`) || !strings.Contains(forkTemplateResponse.Body.String(), `"source":"custom"`) || !strings.Contains(forkTemplateResponse.Body.String(), `"input_gate"`) {
		t.Fatalf("expected forked workflow template resource, got %d body=%s", forkTemplateResponse.Code, forkTemplateResponse.Body.String())
	}
	forkTemplatePath := filepath.Join(runtimeRef.RuntimeHome(), "templates", "workflows", "forked-security-review.yaml")
	if _, err := os.Stat(forkTemplatePath); err != nil {
		t.Fatalf("expected forked workflow template file at %s: %v", forkTemplatePath, err)
	}

	templateDryRunRequest := httptest.NewRequest(http.MethodPost, "/api/resources/workflow-templates/draft-template/validate", strings.NewReader(`{
		"name": "draft-template",
		"title": "Draft Template",
		"graph": {
			"name": "draft-template",
			"stages": [
				{"name":"plan","node_type":"agent","agent":"planner","skill":"execution-plan"}
			]
		}
	}`))
	templateDryRunRequest.Header.Set("Content-Type", "application/json")
	templateDryRunResponse := httptest.NewRecorder()
	server.ServeHTTP(templateDryRunResponse, templateDryRunRequest)
	if templateDryRunResponse.Code != http.StatusOK || !strings.Contains(templateDryRunResponse.Body.String(), `"valid":true`) || !strings.Contains(templateDryRunResponse.Body.String(), `"resource":"workflow_template"`) || !strings.Contains(templateDryRunResponse.Body.String(), `"normalized"`) {
		t.Fatalf("expected workflow template dry-run validation, got %d body=%s", templateDryRunResponse.Code, templateDryRunResponse.Body.String())
	}
	if _, err := os.Stat(filepath.Join(runtimeRef.RuntimeHome(), "templates", "workflows", "draft-template.yaml")); !os.IsNotExist(err) {
		t.Fatalf("expected workflow template dry-run not to write resource, stat err=%v", err)
	}
	invalidTemplateDryRunRequest := httptest.NewRequest(http.MethodPost, "/api/resources/workflow-templates/validate", strings.NewReader(`{
		"name": "bad-template",
		"graph": {
			"name": "bad-template",
			"stages": [
				{"name":"plan","node_type":"agent","agent":"planner","skill":"execution-plan","next":["missing-stage"]}
			]
		}
	}`))
	invalidTemplateDryRunRequest.Header.Set("Content-Type", "application/json")
	invalidTemplateDryRunResponse := httptest.NewRecorder()
	server.ServeHTTP(invalidTemplateDryRunResponse, invalidTemplateDryRunRequest)
	if invalidTemplateDryRunResponse.Code != http.StatusOK || !strings.Contains(invalidTemplateDryRunResponse.Body.String(), `"valid":false`) || !strings.Contains(invalidTemplateDryRunResponse.Body.String(), `"field":"graph.stages"`) {
		t.Fatalf("expected invalid workflow template dry-run validation, got %d body=%s", invalidTemplateDryRunResponse.Code, invalidTemplateDryRunResponse.Body.String())
	}

	customTemplateRequest := httptest.NewRequest(http.MethodPut, "/api/resources/workflow-templates/custom-template", strings.NewReader(`{
		"name": "custom-template",
		"title": "Custom Template",
		"description": "Team-owned reusable template.",
		"category": "custom",
		"tags": ["team"],
		"graph": {
			"name": "custom-template",
			"description": "Team-owned reusable template.",
			"stages": [
				{"name":"plan","node_type":"agent","agent":"planner","skill":"execution-plan"}
			]
		}
	}`))
	customTemplateRequest.Header.Set("Content-Type", "application/json")
	customTemplateResponse := httptest.NewRecorder()
	server.ServeHTTP(customTemplateResponse, customTemplateRequest)
	if customTemplateResponse.Code != http.StatusOK || !strings.Contains(customTemplateResponse.Body.String(), `"kind":"goflow.workflow_template_resource"`) || !strings.Contains(customTemplateResponse.Body.String(), `"version":2`) || !strings.Contains(customTemplateResponse.Body.String(), `"custom":true`) {
		t.Fatalf("expected custom workflow template save, got %d body=%s", customTemplateResponse.Code, customTemplateResponse.Body.String())
	}
	customTemplateListRequest := httptest.NewRequest(http.MethodGet, "/api/resources/workflow-templates", nil)
	customTemplateListResponse := httptest.NewRecorder()
	server.ServeHTTP(customTemplateListResponse, customTemplateListRequest)
	if customTemplateListResponse.Code != http.StatusOK || !strings.Contains(customTemplateListResponse.Body.String(), `"name":"custom-template"`) || !strings.Contains(customTemplateListResponse.Body.String(), `"name":"forked-security-review"`) || !strings.Contains(customTemplateListResponse.Body.String(), `"version":2`) {
		t.Fatalf("expected custom workflow template resource list, got %d body=%s", customTemplateListResponse.Code, customTemplateListResponse.Body.String())
	}
	customTemplateGetRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-templates/custom-template", nil)
	customTemplateGetResponse := httptest.NewRecorder()
	server.ServeHTTP(customTemplateGetResponse, customTemplateGetRequest)
	if customTemplateGetResponse.Code != http.StatusOK || !strings.Contains(customTemplateGetResponse.Body.String(), `"kind":"goflow.workflow_template_resource"`) || !strings.Contains(customTemplateGetResponse.Body.String(), `"source":"custom"`) || !strings.Contains(customTemplateGetResponse.Body.String(), `"graph"`) {
		t.Fatalf("expected custom workflow template detail, got %d body=%s", customTemplateGetResponse.Code, customTemplateGetResponse.Body.String())
	}
	futureTemplateRequest := httptest.NewRequest(http.MethodPut, "/api/resources/workflow-templates/future-template", strings.NewReader(`{
		"kind": "goflow.workflow_template_resource",
		"version": 99,
		"name": "future-template",
		"graph": {"name":"future-template","stages":[{"name":"plan","agent":"planner","skill":"execution-plan"}]}
	}`))
	futureTemplateRequest.Header.Set("Content-Type", "application/json")
	futureTemplateResponse := httptest.NewRecorder()
	server.ServeHTTP(futureTemplateResponse, futureTemplateRequest)
	if futureTemplateResponse.Code != http.StatusBadRequest || !strings.Contains(futureTemplateResponse.Body.String(), "newer than supported") {
		t.Fatalf("expected future workflow template resource rejection, got %d body=%s", futureTemplateResponse.Code, futureTemplateResponse.Body.String())
	}
	legacyTemplateDir := filepath.Join(runtimeRef.RuntimeHome(), "templates", "workflows")
	if err := os.MkdirAll(legacyTemplateDir, 0o755); err != nil {
		t.Fatalf("mkdir legacy workflow template dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacyTemplateDir, "legacy-template.yaml"), []byte(`
name: legacy-template
description: Legacy bare graph template.
stages:
  - name: plan
    agent: planner
    skill: execution-plan
`), 0o644); err != nil {
		t.Fatalf("write legacy workflow template: %v", err)
	}
	legacyTemplateRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-templates/legacy-template", nil)
	legacyTemplateResponse := httptest.NewRecorder()
	server.ServeHTTP(legacyTemplateResponse, legacyTemplateRequest)
	if legacyTemplateResponse.Code != http.StatusOK || !strings.Contains(legacyTemplateResponse.Body.String(), `"kind":"goflow.workflow_template_resource"`) || !strings.Contains(legacyTemplateResponse.Body.String(), `"version":2`) || !strings.Contains(legacyTemplateResponse.Body.String(), `"migrated_from_version":1`) {
		t.Fatalf("expected legacy workflow template migration, got %d body=%s", legacyTemplateResponse.Code, legacyTemplateResponse.Body.String())
	}
	customTemplateDeleteRequest := httptest.NewRequest(http.MethodDelete, "/api/resources/workflow-templates/custom-template", nil)
	customTemplateDeleteResponse := httptest.NewRecorder()
	server.ServeHTTP(customTemplateDeleteResponse, customTemplateDeleteRequest)
	if customTemplateDeleteResponse.Code != http.StatusNoContent {
		t.Fatalf("expected custom workflow template delete, got %d body=%s", customTemplateDeleteResponse.Code, customTemplateDeleteResponse.Body.String())
	}
}

func TestServerTeamTemplateEndpoints(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	server := NewServer(runtimeRef)

	listRequest := httptest.NewRequest(http.MethodGet, "/api/team-templates", nil)
	listResponse := httptest.NewRecorder()
	server.ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("expected team template list 200, got %d", listResponse.Code)
	}
	if !strings.Contains(listResponse.Body.String(), `"software-task-team"`) ||
		!strings.Contains(listResponse.Body.String(), `"audit-security-team"`) ||
		!strings.Contains(listResponse.Body.String(), `"operations-runbook-team"`) ||
		!strings.Contains(listResponse.Body.String(), `"customer-support-team"`) {
		t.Fatalf("expected built-in team template summaries, got %s", listResponse.Body.String())
	}

	detailRequest := httptest.NewRequest(http.MethodGet, "/api/team-templates/web-research-team", nil)
	detailResponse := httptest.NewRecorder()
	server.ServeHTTP(detailResponse, detailRequest)
	if detailResponse.Code != http.StatusOK {
		t.Fatalf("expected team template detail 200, got %d", detailResponse.Code)
	}
	body := detailResponse.Body.String()
	if !strings.Contains(body, `"role_templates"`) ||
		!strings.Contains(body, `"asset-collector"`) ||
		!strings.Contains(body, `"handoffs"`) ||
		!strings.Contains(body, `"blackboard_templates"`) ||
		!strings.Contains(body, `"recommended_workflow":"web-research-risk"`) {
		t.Fatalf("expected detailed web research team template, got %s", body)
	}

	missingRequest := httptest.NewRequest(http.MethodGet, "/api/team-templates/missing-team", nil)
	missingResponse := httptest.NewRecorder()
	server.ServeHTTP(missingResponse, missingRequest)
	if missingResponse.Code != http.StatusNotFound {
		t.Fatalf("expected missing team template 404, got %d", missingResponse.Code)
	}

	customTeamRequest := httptest.NewRequest(http.MethodPut, "/api/resources/team-templates/custom-review-team", strings.NewReader(`{
		"name": "custom-review-team",
		"title": "Custom Review Team",
		"description": "A file-backed custom team template.",
		"category": "custom",
		"tags": ["review"],
		"recommended_workflow": "plan-fix-audit",
		"recommended_entry_agent": "planner",
		"role_templates": [
			{"name":"planner","label":"Planner","agent":"planner","skill":"execution-plan","responsibilities":["plan work"],"produces":["plan"]},
			{"name":"reviewer","label":"Reviewer","agent":"auditor","skill":"code-audit","responsibilities":["review work"],"consumes":["plan"],"produces":["findings"]}
		],
		"handoffs": [
			{"from":"planner","to":"reviewer","kind":"review_request","subject":"Ready for review"}
		],
		"blackboard_templates": [
			{"kind":"decision","title":"Decisions","owner_role":"planner","status":"open"}
		],
		"quorum_presets": [
			{"name":"review-quorum","title":"Reviewer quorum","required":1,"roles":["reviewer"],"reject_blocks":true,"default":true}
		],
		"output_contract": ["findings"]
	}`))
	customTeamRequest.Header.Set("Content-Type", "application/json")
	customTeamResponse := httptest.NewRecorder()
	server.ServeHTTP(customTeamResponse, customTeamRequest)
	if customTeamResponse.Code != http.StatusOK || !strings.Contains(customTeamResponse.Body.String(), `"kind":"goflow.team_template_resource"`) || !strings.Contains(customTeamResponse.Body.String(), `"version":2`) || !strings.Contains(customTeamResponse.Body.String(), `"custom":true`) {
		t.Fatalf("expected custom team template save, got %d body=%s", customTeamResponse.Code, customTeamResponse.Body.String())
	}
	customTeamPath := filepath.Join(runtimeRef.RuntimeHome(), "templates", "teams", "custom-review-team.yaml")
	if _, err := os.Stat(customTeamPath); err != nil {
		t.Fatalf("expected custom team template file at %s: %v", customTeamPath, err)
	}
	customResourceListRequest := httptest.NewRequest(http.MethodGet, "/api/resources/team-templates", nil)
	customResourceListResponse := httptest.NewRecorder()
	server.ServeHTTP(customResourceListResponse, customResourceListRequest)
	if customResourceListResponse.Code != http.StatusOK || !strings.Contains(customResourceListResponse.Body.String(), `"name":"custom-review-team"`) || !strings.Contains(customResourceListResponse.Body.String(), `"version":2`) || !strings.Contains(customResourceListResponse.Body.String(), `"quorum_presets":1`) {
		t.Fatalf("expected custom team template resource list, got %d body=%s", customResourceListResponse.Code, customResourceListResponse.Body.String())
	}
	customTeamGetRequest := httptest.NewRequest(http.MethodGet, "/api/team-templates/custom-review-team", nil)
	customTeamGetResponse := httptest.NewRecorder()
	server.ServeHTTP(customTeamGetResponse, customTeamGetRequest)
	if customTeamGetResponse.Code != http.StatusOK || !strings.Contains(customTeamGetResponse.Body.String(), `"source":"custom"`) || !strings.Contains(customTeamGetResponse.Body.String(), `"planner"`) || !strings.Contains(customTeamGetResponse.Body.String(), `"handoffs"`) || !strings.Contains(customTeamGetResponse.Body.String(), `"review-quorum"`) {
		t.Fatalf("expected custom team template detail, got %d body=%s", customTeamGetResponse.Code, customTeamGetResponse.Body.String())
	}

	validateTeamRequest := httptest.NewRequest(http.MethodPost, "/api/resources/team-templates/dry-run-team/validate", strings.NewReader(`{
		"title": "Dry Run Team",
		"role_templates": [
			{"name":"planner","agent":"planner","skill":"execution-plan"}
		]
	}`))
	validateTeamRequest.Header.Set("Content-Type", "application/json")
	validateTeamResponse := httptest.NewRecorder()
	server.ServeHTTP(validateTeamResponse, validateTeamRequest)
	if validateTeamResponse.Code != http.StatusOK || !strings.Contains(validateTeamResponse.Body.String(), `"valid":true`) || !strings.Contains(validateTeamResponse.Body.String(), `"name":"dry-run-team"`) || !strings.Contains(validateTeamResponse.Body.String(), `"normalized"`) || !strings.Contains(validateTeamResponse.Body.String(), `"kind":"goflow.team_template_resource"`) {
		t.Fatalf("expected valid dry-run team template response, got %d body=%s", validateTeamResponse.Code, validateTeamResponse.Body.String())
	}
	dryRunPath := filepath.Join(runtimeRef.RuntimeHome(), "templates", "teams", "dry-run-team.yaml")
	if _, err := os.Stat(dryRunPath); !os.IsNotExist(err) {
		t.Fatalf("expected team template validate to avoid writing %s, stat err=%v", dryRunPath, err)
	}

	validateBodyNameRequest := httptest.NewRequest(http.MethodPost, "/api/resources/team-templates/validate", strings.NewReader(`{
		"name": "body-named-team",
		"title": "Body Named Team",
		"role_templates": [
			{"name":"planner","agent":"planner","skill":"execution-plan"}
		]
	}`))
	validateBodyNameRequest.Header.Set("Content-Type", "application/json")
	validateBodyNameResponse := httptest.NewRecorder()
	server.ServeHTTP(validateBodyNameResponse, validateBodyNameRequest)
	if validateBodyNameResponse.Code != http.StatusOK || !strings.Contains(validateBodyNameResponse.Body.String(), `"valid":true`) || !strings.Contains(validateBodyNameResponse.Body.String(), `"name":"body-named-team"`) {
		t.Fatalf("expected body-name team template validation, got %d body=%s", validateBodyNameResponse.Code, validateBodyNameResponse.Body.String())
	}

	invalidTeamRequest := httptest.NewRequest(http.MethodPost, "/api/resources/team-templates/validate", strings.NewReader(`{
		"name": "invalid-review-team",
		"title": "Invalid Review Team",
		"role_templates": [
			{"name":"planner","agent":"planner"}
		],
		"handoffs": [
			{"from":"planner","to":"reviewer"}
		]
	}`))
	invalidTeamRequest.Header.Set("Content-Type", "application/json")
	invalidTeamResponse := httptest.NewRecorder()
	server.ServeHTTP(invalidTeamResponse, invalidTeamRequest)
	if invalidTeamResponse.Code != http.StatusOK || !strings.Contains(invalidTeamResponse.Body.String(), `"valid":false`) || !strings.Contains(invalidTeamResponse.Body.String(), `"field":"handoffs"`) || !strings.Contains(invalidTeamResponse.Body.String(), `"code":"invalid_handoffs"`) || !strings.Contains(invalidTeamResponse.Body.String(), `"recommendation"`) || !strings.Contains(invalidTeamResponse.Body.String(), "unknown to role") {
		t.Fatalf("expected structured invalid team template validation, got %d body=%s", invalidTeamResponse.Code, invalidTeamResponse.Body.String())
	}

	scaffoldListRequest := httptest.NewRequest(http.MethodGet, "/api/resources/team-templates/scaffolds", nil)
	scaffoldListResponse := httptest.NewRecorder()
	server.ServeHTTP(scaffoldListResponse, scaffoldListRequest)
	if scaffoldListResponse.Code != http.StatusOK || !strings.Contains(scaffoldListResponse.Body.String(), `"software-review"`) || !strings.Contains(scaffoldListResponse.Body.String(), `"security-review"`) || !strings.Contains(scaffoldListResponse.Body.String(), `"customer-support"`) {
		t.Fatalf("expected team template scaffold list, got %d body=%s", scaffoldListResponse.Code, scaffoldListResponse.Body.String())
	}
	scaffoldDetailRequest := httptest.NewRequest(http.MethodGet, "/api/resources/team-templates/scaffolds/software-review?name=scaffold-review-team", nil)
	scaffoldDetailResponse := httptest.NewRecorder()
	server.ServeHTTP(scaffoldDetailResponse, scaffoldDetailRequest)
	if scaffoldDetailResponse.Code != http.StatusOK || !strings.Contains(scaffoldDetailResponse.Body.String(), `"document"`) || !strings.Contains(scaffoldDetailResponse.Body.String(), `"scaffold-review-team"`) || !strings.Contains(scaffoldDetailResponse.Body.String(), `"software-review"`) {
		t.Fatalf("expected team scaffold detail with preview document, got %d body=%s", scaffoldDetailResponse.Code, scaffoldDetailResponse.Body.String())
	}
	scaffoldCreateRequest := httptest.NewRequest(http.MethodPost, "/api/resources/team-templates/scaffolds/software-review", strings.NewReader(`{
		"name": "scaffold-review-team",
		"title": "Scaffold Review Team",
		"tags": ["scaffold", "review"]
	}`))
	scaffoldCreateRequest.Header.Set("Content-Type", "application/json")
	scaffoldCreateResponse := httptest.NewRecorder()
	server.ServeHTTP(scaffoldCreateResponse, scaffoldCreateRequest)
	if scaffoldCreateResponse.Code != http.StatusCreated {
		t.Fatalf("expected team scaffold create 201, got %d body=%s", scaffoldCreateResponse.Code, scaffoldCreateResponse.Body.String())
	}
	var scaffoldCreated teamTemplateScaffoldResponse
	if err := json.NewDecoder(scaffoldCreateResponse.Body).Decode(&scaffoldCreated); err != nil {
		t.Fatalf("decode team scaffold response: %v", err)
	}
	if !scaffoldCreated.Created || scaffoldCreated.Team.Name != "scaffold-review-team" || !scaffoldCreated.Team.Custom || len(scaffoldCreated.Team.RoleTemplates) == 0 || len(scaffoldCreated.Team.QuorumPresets) == 0 {
		t.Fatalf("expected created custom team template scaffold, got %#v", scaffoldCreated)
	}
	scaffoldPath := filepath.Join(runtimeRef.RuntimeHome(), "templates", "teams", "scaffold-review-team.yaml")
	if _, err := os.Stat(scaffoldPath); err != nil {
		t.Fatalf("expected scaffolded team template file at %s: %v", scaffoldPath, err)
	}
	scaffoldConflictRequest := httptest.NewRequest(http.MethodPost, "/api/resources/team-templates/scaffolds/software-review", strings.NewReader(`{"name":"scaffold-review-team"}`))
	scaffoldConflictResponse := httptest.NewRecorder()
	server.ServeHTTP(scaffoldConflictResponse, scaffoldConflictRequest)
	if scaffoldConflictResponse.Code != http.StatusConflict {
		t.Fatalf("expected team scaffold conflict 409, got %d body=%s", scaffoldConflictResponse.Code, scaffoldConflictResponse.Body.String())
	}
	scaffoldOverwriteRequest := httptest.NewRequest(http.MethodPost, "/api/resources/team-templates/scaffolds/software-review?overwrite=1", strings.NewReader(`{
		"name": "scaffold-review-team",
		"title": "Updated Scaffold Review Team"
	}`))
	scaffoldOverwriteResponse := httptest.NewRecorder()
	server.ServeHTTP(scaffoldOverwriteResponse, scaffoldOverwriteRequest)
	if scaffoldOverwriteResponse.Code != http.StatusOK {
		t.Fatalf("expected team scaffold overwrite 200, got %d body=%s", scaffoldOverwriteResponse.Code, scaffoldOverwriteResponse.Body.String())
	}
	var scaffoldUpdated teamTemplateScaffoldResponse
	if err := json.NewDecoder(scaffoldOverwriteResponse.Body).Decode(&scaffoldUpdated); err != nil {
		t.Fatalf("decode team scaffold overwrite response: %v", err)
	}
	if !scaffoldUpdated.Updated || scaffoldUpdated.Team.Title != "Updated Scaffold Review Team" {
		t.Fatalf("expected updated scaffold team template, got %#v", scaffoldUpdated)
	}

	optionsRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-options", nil)
	optionsResponse := httptest.NewRecorder()
	server.ServeHTTP(optionsResponse, optionsRequest)
	if optionsResponse.Code != http.StatusOK || !strings.Contains(optionsResponse.Body.String(), `"custom-review-team"`) || !strings.Contains(optionsResponse.Body.String(), `"source":"custom"`) {
		t.Fatalf("expected custom team template in workflow options, got %d body=%s", optionsResponse.Code, optionsResponse.Body.String())
	}
	validateGraphRequest := httptest.NewRequest(http.MethodPost, "/api/workflow-graphs/validate", strings.NewReader(`{
		"name": "custom-team-flow",
		"stages": [
			{"name":"team","node_type":"team","params":{"team":"custom-review-team"}}
		]
	}`))
	validateGraphRequest.Header.Set("Content-Type", "application/json")
	validateGraphResponse := httptest.NewRecorder()
	server.ServeHTTP(validateGraphResponse, validateGraphRequest)
	if validateGraphResponse.Code != http.StatusOK || !strings.Contains(validateGraphResponse.Body.String(), `"valid":true`) {
		t.Fatalf("expected custom team workflow validation, got %d body=%s", validateGraphResponse.Code, validateGraphResponse.Body.String())
	}
	futureTeamRequest := httptest.NewRequest(http.MethodPut, "/api/resources/team-templates/future-team", strings.NewReader(`{
		"kind": "goflow.team_template_resource",
		"version": 99,
		"name": "future-team",
		"title": "Future Team",
		"role_templates": [{"name":"planner","agent":"planner"}]
	}`))
	futureTeamRequest.Header.Set("Content-Type", "application/json")
	futureTeamResponse := httptest.NewRecorder()
	server.ServeHTTP(futureTeamResponse, futureTeamRequest)
	if futureTeamResponse.Code != http.StatusBadRequest || !strings.Contains(futureTeamResponse.Body.String(), "newer than supported") {
		t.Fatalf("expected future team template resource rejection, got %d body=%s", futureTeamResponse.Code, futureTeamResponse.Body.String())
	}
	deleteTeamRequest := httptest.NewRequest(http.MethodDelete, "/api/resources/team-templates/custom-review-team", nil)
	deleteTeamResponse := httptest.NewRecorder()
	server.ServeHTTP(deleteTeamResponse, deleteTeamRequest)
	if deleteTeamResponse.Code != http.StatusNoContent {
		t.Fatalf("expected custom team template delete, got %d body=%s", deleteTeamResponse.Code, deleteTeamResponse.Body.String())
	}
}

func TestServerTeamTemplateScaffoldPresetsLoadRuntimeOverrides(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	presetDir := filepath.Join(runtimeRef.RuntimeHome(), "templates", "teams", "scaffolds")
	if err := os.MkdirAll(presetDir, 0o755); err != nil {
		t.Fatalf("mkdir team scaffold preset dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(presetDir, "custom.yaml"), []byte(`
presets:
  - name: software-review
    default_name: custom-platform-review-team
    title: Platform Review Team
    description: Runtime override for software review.
    category: platform
    base_template: software-task-team
    recommended_workflow: plan-fix-audit
    recommended_entry_agent: planner
  - name: finance-review
    default_name: finance-review-team
    title: Finance Review Team
    description: Finance review collaboration preset.
    category: finance
    base_template: documentation-team
    recommended_workflow: docs-review-publish
    recommended_entry_agent: planner
`), 0o644); err != nil {
		t.Fatalf("write custom team scaffold preset: %v", err)
	}

	server := NewServer(runtimeRef)
	request := httptest.NewRequest(http.MethodGet, "/api/resources/team-templates/scaffolds", nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	body := response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(body, `"finance-review"`) || !strings.Contains(body, `"Platform Review Team"`) {
		t.Fatalf("expected runtime team scaffold presets, got %d body=%s", response.Code, body)
	}
}

func TestServerTeamStateEndpoint(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	runtimeRef.AddCollaborationMessage(session.CollaborationMessageSnapshot{
		RunID:     "wf-1",
		Stage:     "team",
		FromAgent: "auditor",
		ToAgent:   "planner",
		Kind:      "team_handoff",
		Subject:   "Risk report handoff",
		Content:   "handoff",
		Metadata:  map[string]string{"team": "web-research-team"},
	})
	runtimeRef.UpsertBlackboardEntry(session.BlackboardEntrySnapshot{
		Scope:    "workflow",
		RunID:    "wf-1",
		Stage:    "team",
		AgentID:  "planner",
		Kind:     "decision",
		Title:    "Verify high risk finding",
		Content:  "needs operator decision",
		Status:   "open",
		Tags:     []string{"team", "web-research-team"},
		Metadata: map[string]string{"team": "web-research-team", "entry_agent": "auditor"},
	})
	server := NewServer(runtimeRef)

	request := httptest.NewRequest(http.MethodGet, "/api/team-state?run_id=wf-1&team=web-research-team", nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected team state 200, got %d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, `"team":"web-research-team"`) ||
		!strings.Contains(body, `"active_owner":"auditor"`) ||
		!strings.Contains(body, `"handoffs"`) ||
		!strings.Contains(body, `"unresolved_items"`) ||
		!strings.Contains(body, `"Verify high risk finding"`) {
		t.Fatalf("expected derived team state response, got %s", body)
	}
}

func TestServerKitResourceEndpoints(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	server := NewServer(runtimeRef)

	saveRequest := httptest.NewRequest(http.MethodPut, "/api/resources/kits/software-kit", strings.NewReader(`{
		"name": "software-kit",
		"title": "Software Kit",
		"description": "Reusable software engineering kit.",
		"category": "software",
		"tags": ["coding", "review"],
		"providers": ["primary"],
		"agents": ["chat", "fixer"],
		"skills": ["execution-plan"],
		"tools": ["write_file"],
		"workflows": ["plan-fix-audit"],
		"workflow_templates": ["human-input-security-review"],
		"team_templates": ["software-task-team"],
		"policy_rules": ["expression"],
		"required_env": ["GOFLOW_TEST_REQUIRED_KIT_ENV"],
		"examples": [{"title":"Improve code","request":"optimize this project","workflow":"plan-fix-audit","agent":"fixer"}]
	}`))
	saveRequest.Header.Set("Content-Type", "application/json")
	saveResponse := httptest.NewRecorder()
	server.ServeHTTP(saveResponse, saveRequest)
	if saveResponse.Code != http.StatusOK {
		t.Fatalf("expected kit save 200, got %d body=%s", saveResponse.Code, saveResponse.Body.String())
	}
	var saved kitResourceDocument
	if err := json.NewDecoder(saveResponse.Body).Decode(&saved); err != nil {
		t.Fatalf("decode saved kit: %v", err)
	}
	if saved.Kind != "goflow.kit" || saved.Version != 1 || saved.Name != "software-kit" || !saved.Validation.Valid {
		t.Fatalf("expected normalized valid kit resource, got %#v", saved)
	}
	if len(saved.Validation.Issues) == 0 || saved.Validation.Issues[0].Code != "env_missing" || saved.Validation.Issues[0].Field != "required_env" || saved.Validation.Issues[0].Recommendation == "" {
		t.Fatalf("expected missing env warning in kit validation, got %#v", saved.Validation)
	}
	if _, err := os.Stat(filepath.Join(runtimeRef.RuntimeHome(), "kits", "software-kit", "kit.yaml")); err != nil {
		t.Fatalf("expected kit manifest on disk: %v", err)
	}

	listRequest := httptest.NewRequest(http.MethodGet, "/api/kits", nil)
	listResponse := httptest.NewRecorder()
	server.ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK || !strings.Contains(listResponse.Body.String(), `"software-kit"`) || !strings.Contains(listResponse.Body.String(), `"valid":true`) {
		t.Fatalf("expected kit list with saved kit, got %d body=%s", listResponse.Code, listResponse.Body.String())
	}
	if !strings.Contains(listResponse.Body.String(), `"agent_refs":["chat","fixer"]`) ||
		!strings.Contains(listResponse.Body.String(), `"workflow_template_refs":["human-input-security-review"]`) ||
		!strings.Contains(listResponse.Body.String(), `"policy_rule_refs":["expression"]`) {
		t.Fatalf("expected kit list to expose linked resource references, got %s", listResponse.Body.String())
	}

	detailRequest := httptest.NewRequest(http.MethodGet, "/api/kits/software-kit", nil)
	detailResponse := httptest.NewRecorder()
	server.ServeHTTP(detailResponse, detailRequest)
	if detailResponse.Code != http.StatusOK || !strings.Contains(detailResponse.Body.String(), `"examples"`) || !strings.Contains(detailResponse.Body.String(), `"software-kit"`) {
		t.Fatalf("expected kit detail, got %d body=%s", detailResponse.Code, detailResponse.Body.String())
	}

	validateRequest := httptest.NewRequest(http.MethodGet, "/api/resources/kits/software-kit/validate", nil)
	validateResponse := httptest.NewRecorder()
	server.ServeHTTP(validateResponse, validateRequest)
	if validateResponse.Code != http.StatusOK || !strings.Contains(validateResponse.Body.String(), `"valid":true`) || !strings.Contains(validateResponse.Body.String(), `"env_missing"`) {
		t.Fatalf("expected kit validation response, got %d body=%s", validateResponse.Code, validateResponse.Body.String())
	}

	dryRunRequest := httptest.NewRequest(http.MethodPost, "/api/resources/kits/draft-kit/validate", strings.NewReader(`{
		"name": "draft-kit",
		"title": "Draft Kit",
		"agents": ["chat"],
		"skills": ["execution-plan"],
		"tools": ["missing-tool"]
	}`))
	dryRunRequest.Header.Set("Content-Type", "application/json")
	dryRunResponse := httptest.NewRecorder()
	server.ServeHTTP(dryRunResponse, dryRunRequest)
	if dryRunResponse.Code != http.StatusOK || !strings.Contains(dryRunResponse.Body.String(), `"valid":true`) || !strings.Contains(dryRunResponse.Body.String(), `"normalized"`) || !strings.Contains(dryRunResponse.Body.String(), `"tool_unseen"`) || !strings.Contains(dryRunResponse.Body.String(), `"field":"tools"`) || !strings.Contains(dryRunResponse.Body.String(), `"recommendation"`) {
		t.Fatalf("expected kit dry-run validation response, got %d body=%s", dryRunResponse.Code, dryRunResponse.Body.String())
	}
	if _, err := os.Stat(filepath.Join(runtimeRef.RuntimeHome(), "kits", "draft-kit", "kit.yaml")); !os.IsNotExist(err) {
		t.Fatalf("expected kit dry-run not to write manifest, stat err=%v", err)
	}

	invalidDryRunRequest := httptest.NewRequest(http.MethodPost, "/api/resources/kits/validate", strings.NewReader(`{
		"name": ""
	}`))
	invalidDryRunRequest.Header.Set("Content-Type", "application/json")
	invalidDryRunResponse := httptest.NewRecorder()
	server.ServeHTTP(invalidDryRunResponse, invalidDryRunRequest)
	if invalidDryRunResponse.Code != http.StatusOK || !strings.Contains(invalidDryRunResponse.Body.String(), `"valid":false`) || !strings.Contains(invalidDryRunResponse.Body.String(), `"invalid_name"`) || !strings.Contains(invalidDryRunResponse.Body.String(), `"field":"name"`) || !strings.Contains(invalidDryRunResponse.Body.String(), `"recommendation"`) {
		t.Fatalf("expected invalid kit dry-run validation response, got %d body=%s", invalidDryRunResponse.Code, invalidDryRunResponse.Body.String())
	}

	badRequest := httptest.NewRequest(http.MethodPut, "/api/resources/kits/bad-kit", strings.NewReader(`{
		"name": "bad-kit",
		"agents": ["missing-agent"],
		"workflows": ["missing-flow"]
	}`))
	badRequest.Header.Set("Content-Type", "application/json")
	badResponse := httptest.NewRecorder()
	server.ServeHTTP(badResponse, badRequest)
	if badResponse.Code != http.StatusOK {
		t.Fatalf("expected bad kit save for validation workflow, got %d body=%s", badResponse.Code, badResponse.Body.String())
	}
	badBody := badResponse.Body.String()
	var bad kitResourceDocument
	if err := json.NewDecoder(strings.NewReader(badBody)).Decode(&bad); err != nil {
		t.Fatalf("decode bad kit: %v", err)
	}
	if bad.Validation.Valid || !strings.Contains(badBody, "agent_missing") || !strings.Contains(badBody, "workflow_missing") {
		t.Fatalf("expected invalid compatibility validation, got %#v", bad.Validation)
	}

	diagnosticsRequest := httptest.NewRequest(http.MethodGet, "/api/config/diagnostics", nil)
	diagnosticsResponse := httptest.NewRecorder()
	server.ServeHTTP(diagnosticsResponse, diagnosticsRequest)
	if diagnosticsResponse.Code != http.StatusOK || !strings.Contains(diagnosticsResponse.Body.String(), `"kits"`) || !strings.Contains(diagnosticsResponse.Body.String(), "kit.yaml") {
		t.Fatalf("expected config diagnostics to include kit module, got %d body=%s", diagnosticsResponse.Code, diagnosticsResponse.Body.String())
	}

	deleteRequest := httptest.NewRequest(http.MethodDelete, "/api/resources/kits/software-kit", nil)
	deleteResponse := httptest.NewRecorder()
	server.ServeHTTP(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("expected kit delete 204, got %d body=%s", deleteResponse.Code, deleteResponse.Body.String())
	}
}

func TestServerKitBundleExportImport(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	server := NewServer(runtimeRef)

	if _, err := server.saveProviderResource("bundle-provider", providerResourceDocument{
		ID:       "bundle-provider",
		Provider: "openai-compatible",
		BaseURL:  "http://localhost:9999/v1",
		APIKey:   "secret-token",
		Model:    "bundle-model",
	}); err != nil {
		t.Fatalf("save provider resource: %v", err)
	}
	if _, err := server.saveAgentResource("bundle-agent", agentResourceDocument{
		ID:               "bundle-agent",
		Name:             "Bundle Agent",
		Description:      "Agent bundled with a vertical kit.",
		Provider:         "bundle-provider",
		Model:            "bundle-model",
		MaxIterations:    2,
		AllowedToolKinds: []string{"read"},
		ToolPolicy:       "allow",
		Mode:             "chat",
	}); err != nil {
		t.Fatalf("save agent resource: %v", err)
	}
	if _, err := server.saveSkillResource("bundle-skill", skillResourceDocument{
		Name:             "bundle-skill",
		Description:      "Skill bundled with a vertical kit.",
		Version:          "1.0.0",
		Author:           "GoFlow Test",
		Mode:             "chat",
		PreferredAgent:   "chat",
		AllowedToolKinds: []string{"read"},
		OutputKind:       "summary",
		Activation:       schema.Activation{Keywords: []string{"bundle-skill"}},
		Instructions:     "Inspect the request and produce a concise answer.",
	}); err != nil {
		t.Fatalf("save skill resource: %v", err)
	}
	if _, err := server.saveToolResource("bundle-tool", toolResourceDocument{
		Name:        "bundle-tool",
		Language:    "python",
		Description: "Bundled Python MCP tool.",
		Command:     "python",
		Args:        []string{"./mcp_servers/bundle-tool.py"},
		Enabled:     true,
		Timeout:     "30s",
		WorkDir:     ".",
		Code:        "#!/usr/bin/env python3\nprint('ok')\n",
	}); err != nil {
		t.Fatalf("save tool resource: %v", err)
	}
	graph := agent.WorkflowGraphDocument{
		Name:        "bundle-flow",
		Description: "Workflow bundled with a vertical kit.",
		Stages: []agent.WorkflowGraphStageDocument{{
			Name:  "plan",
			Agent: "chat",
			Skill: "execution-plan",
		}},
	}
	if err := runtimeRef.WorkflowRunner().SaveWorkflowGraphDocument("bundle-flow", graph); err != nil {
		t.Fatalf("save workflow graph: %v", err)
	}
	templateGraph := graph
	templateGraph.Name = "bundle-template"
	if _, err := runtimeRef.SaveWorkflowTemplateResource("bundle-template", agent.WorkflowTemplate{
		WorkflowTemplateSummary: agent.WorkflowTemplateSummary{
			Name:        "bundle-template",
			Title:       "Bundle Template",
			Description: "Reusable workflow template from a kit.",
			Category:    "custom",
		},
		Graph: templateGraph,
	}); err != nil {
		t.Fatalf("save workflow template: %v", err)
	}
	if _, err := server.saveKitResource("bundle-kit", kitResourceDocument{
		Name:              "bundle-kit",
		Title:             "Bundle Kit",
		Description:       "Complete reusable kit for export/import.",
		Providers:         []string{"bundle-provider"},
		Agents:            []string{"bundle-agent"},
		Skills:            []string{"bundle-skill"},
		Tools:             []string{"bundle-tool"},
		Workflows:         []string{"bundle-flow"},
		WorkflowTemplates: []string{"bundle-template"},
	}); err != nil {
		t.Fatalf("save kit resource: %v", err)
	}

	exportRequest := httptest.NewRequest(http.MethodGet, "/api/resources/kits/bundle-kit/export?format=json", nil)
	exportResponse := httptest.NewRecorder()
	server.ServeHTTP(exportResponse, exportRequest)
	if exportResponse.Code != http.StatusOK {
		t.Fatalf("expected bundle export 200, got %d body=%s", exportResponse.Code, exportResponse.Body.String())
	}
	if strings.Contains(exportResponse.Body.String(), "secret-token") {
		t.Fatalf("expected default kit bundle export to redact provider api keys, got %s", exportResponse.Body.String())
	}
	var bundle kitBundleDocument
	if err := json.NewDecoder(exportResponse.Body).Decode(&bundle); err != nil {
		t.Fatalf("decode exported bundle: %v", err)
	}
	if bundle.Kind != kitBundleKind || bundle.Kit.Name != "bundle-kit" || len(bundle.Providers) != 1 || len(bundle.Agents) != 1 || len(bundle.Skills) != 1 || len(bundle.Workflows) != 1 {
		t.Fatalf("expected populated kit bundle, got %#v", bundle)
	}
	if bundle.Providers[0].ID != "bundle-provider" || bundle.Agents[0].ID != "bundle-agent" {
		t.Fatalf("expected provider and agent ids in bundle, got provider=%#v agent=%#v", bundle.Providers[0], bundle.Agents[0])
	}

	secretRequest := httptest.NewRequest(http.MethodGet, "/api/resources/kits/bundle-kit/export?include_secrets=1", nil)
	secretResponse := httptest.NewRecorder()
	server.ServeHTTP(secretResponse, secretRequest)
	if secretResponse.Code != http.StatusOK || !strings.Contains(secretResponse.Body.String(), "secret-token") {
		t.Fatalf("expected include_secrets export to contain api key, got %d body=%s", secretResponse.Code, secretResponse.Body.String())
	}

	importRuntime := newAPITestRuntime(t)
	importServer := NewServer(importRuntime)
	importBody, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("marshal bundle: %v", err)
	}
	importRequest := httptest.NewRequest(http.MethodPost, "/api/resources/kits/import", strings.NewReader(string(importBody)))
	importRequest.Header.Set("Content-Type", "application/json")
	importResponse := httptest.NewRecorder()
	importServer.ServeHTTP(importResponse, importRequest)
	if importResponse.Code != http.StatusCreated {
		t.Fatalf("expected bundle import 201, got %d body=%s", importResponse.Code, importResponse.Body.String())
	}
	var imported kitBundleImportResult
	if err := json.NewDecoder(importResponse.Body).Decode(&imported); err != nil {
		t.Fatalf("decode import result: %v", err)
	}
	if len(imported.Errors) != 0 || imported.Kit.Name != "bundle-kit" || !imported.Validation.Valid || !imported.Restart {
		t.Fatalf("expected successful import with restart hint, got %#v", imported)
	}
	for _, path := range []string{
		filepath.Join(importRuntime.RuntimeHome(), "kits", "bundle-kit", "kit.yaml"),
		filepath.Join(importRuntime.RuntimeHome(), "configs", "providers", "bundle-provider.yaml"),
		filepath.Join(importRuntime.RuntimeHome(), "configs", "agents", "bundle-agent.yaml"),
		filepath.Join(importRuntime.SkillRoot(), "bundle-skill", "SKILL.md"),
		filepath.Join(importRuntime.RuntimeHome(), "mcp_servers", "bundle-tool.py"),
		filepath.Join(importRuntime.RuntimeHome(), "workflows", "bundle-flow", "workflow.yaml"),
		filepath.Join(importRuntime.RuntimeHome(), "templates", "workflows", "bundle-template.yaml"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected imported resource at %s: %v", path, err)
		}
	}
}

func TestServerKitScaffoldEndpoints(t *testing.T) {
	runtimeRef := newAPITestRuntimeWithDomainResources(t)
	customPresetDir := filepath.Join(runtimeRef.RuntimeHome(), "templates", "kits", "scaffolds")
	if err := os.MkdirAll(customPresetDir, 0o755); err != nil {
		t.Fatalf("mkdir custom preset dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(customPresetDir, "observability.yaml"), []byte(`
name: observability
title: Observability Kit
description: Custom runtime kit scaffold preset.
category: operations
agents: [operations-specialist]
skills: [execution-plan]
workflow_templates: [operations-runbook]
team_templates: [operations-runbook-team]
examples:
  - title: Build an observability plan
    request: Create an observability rollout checklist.
    workflow: operations-runbook
    agent: operations-specialist
`), 0o644); err != nil {
		t.Fatalf("write custom preset: %v", err)
	}
	server := NewServer(runtimeRef)

	listRequest := httptest.NewRequest(http.MethodGet, "/api/resources/kits/scaffolds", nil)
	listResponse := httptest.NewRecorder()
	server.ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK || !strings.Contains(listResponse.Body.String(), `"software-engineering"`) || !strings.Contains(listResponse.Body.String(), `"web-security"`) || !strings.Contains(listResponse.Body.String(), `"multi-domain-agent"`) {
		t.Fatalf("expected scaffold list, got %d body=%s", listResponse.Code, listResponse.Body.String())
	}
	if !strings.Contains(listResponse.Body.String(), `"observability"`) {
		t.Fatalf("expected custom runtime kit scaffold preset, got %s", listResponse.Body.String())
	}
	if !strings.Contains(listResponse.Body.String(), `"operations-runbook"`) ||
		!strings.Contains(listResponse.Body.String(), `"customer-support"`) ||
		!strings.Contains(listResponse.Body.String(), `"customer-support-team"`) ||
		!strings.Contains(listResponse.Body.String(), `"customer-support-triage"`) ||
		!strings.Contains(listResponse.Body.String(), `"agent-framework"`) ||
		!strings.Contains(listResponse.Body.String(), `"agent-framework-extension"`) ||
		!strings.Contains(listResponse.Body.String(), `"framework-extension-team"`) ||
		!strings.Contains(listResponse.Body.String(), `"multi-domain-intake-router"`) {
		t.Fatalf("expected vertical kit scaffold presets to include operations, support, and framework resources, got %s", listResponse.Body.String())
	}

	detailRequest := httptest.NewRequest(http.MethodGet, "/api/resources/kits/scaffolds/software-engineering", nil)
	detailResponse := httptest.NewRecorder()
	server.ServeHTTP(detailResponse, detailRequest)
	if detailResponse.Code != http.StatusOK || !strings.Contains(detailResponse.Body.String(), `"recommended_workflow":"plan-fix-audit"`) {
		t.Fatalf("expected scaffold detail, got %d body=%s", detailResponse.Code, detailResponse.Body.String())
	}
	frameworkDetailRequest := httptest.NewRequest(http.MethodGet, "/api/resources/kits/scaffolds/agent-framework", nil)
	frameworkDetailResponse := httptest.NewRecorder()
	server.ServeHTTP(frameworkDetailResponse, frameworkDetailRequest)
	if frameworkDetailResponse.Code != http.StatusOK ||
		!strings.Contains(frameworkDetailResponse.Body.String(), `"recommended_workflow":"agent-framework-extension"`) ||
		!strings.Contains(frameworkDetailResponse.Body.String(), `"framework-extension-team"`) {
		t.Fatalf("expected framework scaffold detail, got %d body=%s", frameworkDetailResponse.Code, frameworkDetailResponse.Body.String())
	}
	multiDomainDetailRequest := httptest.NewRequest(http.MethodGet, "/api/resources/kits/scaffolds/multi-domain-agent", nil)
	multiDomainDetailResponse := httptest.NewRecorder()
	server.ServeHTTP(multiDomainDetailResponse, multiDomainDetailRequest)
	if multiDomainDetailResponse.Code != http.StatusOK ||
		!strings.Contains(multiDomainDetailResponse.Body.String(), `"recommended_workflow":"multi-domain-intake-router"`) ||
		!strings.Contains(multiDomainDetailResponse.Body.String(), `"web-research-team"`) ||
		!strings.Contains(multiDomainDetailResponse.Body.String(), `"binary-triage"`) {
		t.Fatalf("expected multi-domain scaffold detail, got %d body=%s", multiDomainDetailResponse.Code, multiDomainDetailResponse.Body.String())
	}

	createRequest := httptest.NewRequest(http.MethodPost, "/api/resources/kits/scaffolds/software-engineering", strings.NewReader(`{
		"name": "acme-kit",
		"title": "ACME Engineering Kit",
		"metadata": {"owner": "platform"}
	}`))
	createRequest.Header.Set("Content-Type", "application/json")
	createResponse := httptest.NewRecorder()
	server.ServeHTTP(createResponse, createRequest)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("expected scaffold create 201, got %d body=%s", createResponse.Code, createResponse.Body.String())
	}
	var created kitScaffoldResponse
	if err := json.NewDecoder(createResponse.Body).Decode(&created); err != nil {
		t.Fatalf("decode scaffold create response: %v", err)
	}
	if !created.Created || created.Updated || created.Kit.Name != "acme-kit" || !created.Kit.Validation.Valid {
		t.Fatalf("expected created valid kit, got %#v", created)
	}
	if created.Kit.Metadata["owner"] != "platform" || created.Kit.Metadata["scaffold_preset"] != "software-engineering" {
		t.Fatalf("expected merged scaffold metadata, got %#v", created.Kit.Metadata)
	}
	if _, err := os.Stat(filepath.Join(runtimeRef.RuntimeHome(), "kits", "acme-kit", "kit.yaml")); err != nil {
		t.Fatalf("expected scaffolded kit on disk: %v", err)
	}

	conflictRequest := httptest.NewRequest(http.MethodPost, "/api/resources/kits/scaffolds/software-engineering", strings.NewReader(`{"name":"acme-kit"}`))
	conflictResponse := httptest.NewRecorder()
	server.ServeHTTP(conflictResponse, conflictRequest)
	if conflictResponse.Code != http.StatusConflict {
		t.Fatalf("expected scaffold conflict 409, got %d body=%s", conflictResponse.Code, conflictResponse.Body.String())
	}

	overwriteRequest := httptest.NewRequest(http.MethodPost, "/api/resources/kits/scaffolds/software-engineering?overwrite=1", strings.NewReader(`{
		"name": "acme-kit",
		"title": "Updated ACME Kit",
		"agents": ["chat"],
		"skills": ["execution-plan"]
	}`))
	overwriteResponse := httptest.NewRecorder()
	server.ServeHTTP(overwriteResponse, overwriteRequest)
	if overwriteResponse.Code != http.StatusOK {
		t.Fatalf("expected scaffold overwrite 200, got %d body=%s", overwriteResponse.Code, overwriteResponse.Body.String())
	}
	var updated kitScaffoldResponse
	if err := json.NewDecoder(overwriteResponse.Body).Decode(&updated); err != nil {
		t.Fatalf("decode scaffold overwrite response: %v", err)
	}
	if !updated.Updated || updated.Kit.Title != "Updated ACME Kit" || len(updated.Kit.Agents) != 1 || updated.Kit.Agents[0] != "chat" {
		t.Fatalf("expected updated scaffold kit, got %#v", updated)
	}

	materializeRequest := httptest.NewRequest(http.MethodPost, "/api/resources/kits/scaffolds/software-engineering?materialize=1", strings.NewReader(`{
		"name": "acme-linked",
		"title": "ACME Linked Starter"
	}`))
	materializeRequest.Header.Set("Content-Type", "application/json")
	materializeResponse := httptest.NewRecorder()
	server.ServeHTTP(materializeResponse, materializeRequest)
	if materializeResponse.Code != http.StatusCreated {
		t.Fatalf("expected materialized scaffold create 201, got %d body=%s", materializeResponse.Code, materializeResponse.Body.String())
	}
	var materialized kitScaffoldResponse
	if err := json.NewDecoder(materializeResponse.Body).Decode(&materialized); err != nil {
		t.Fatalf("decode materialized scaffold response: %v", err)
	}
	if !materialized.Materialized || !materialized.RestartRequired || materialized.Kit.Name != "acme-linked" || !materialized.Kit.Validation.Valid {
		t.Fatalf("expected valid materialized kit response, got %#v", materialized)
	}
	if materialized.Kit.Metadata["recommended_agent"] != "acme-linked-agent" ||
		materialized.Kit.Metadata["recommended_workflow"] != "acme-linked-workflow" ||
		materialized.Kit.Metadata["recommended_team"] != "acme-linked-team" {
		t.Fatalf("expected generated recommended metadata, got %#v", materialized.Kit.Metadata)
	}
	if !containsString(materialized.Kit.Tools, "acme-linked-helper") || containsString(materialized.Kit.Tools, "network_tools/device_discovery_plan") {
		t.Fatalf("expected software materialized kit to keep only software-relevant tools plus helper, got %#v", materialized.Kit.Tools)
	}
	for _, want := range []string{"kit", "agent", "skill", "tool", "workflow", "workflow_template", "team_template", "policy_rule"} {
		if !kitBundleSavedResourceKindExists(materialized.Resources, want) {
			t.Fatalf("expected materialized resources to include %s, got %#v", want, materialized.Resources)
		}
	}
	for _, path := range []string{
		filepath.Join(runtimeRef.RuntimeHome(), "kits", "acme-linked", "kit.yaml"),
		filepath.Join(runtimeRef.RuntimeHome(), "configs", "agents", "acme-linked-agent.yaml"),
		filepath.Join(runtimeRef.RuntimeHome(), "skills", "acme-linked-skill", "SKILL.md"),
		filepath.Join(runtimeRef.RuntimeHome(), "mcp_servers", "acme-linked-helper.py"),
		filepath.Join(runtimeRef.RuntimeHome(), "configs", "mcp_servers", "acme-linked-helper.yaml"),
		filepath.Join(runtimeRef.RuntimeHome(), "workflows", "acme-linked-workflow", "workflow.yaml"),
		filepath.Join(runtimeRef.RuntimeHome(), "templates", "workflows", "acme-linked-template.yaml"),
		filepath.Join(runtimeRef.RuntimeHome(), "templates", "teams", "acme-linked-team.yaml"),
		filepath.Join(runtimeRef.RuntimeHome(), "policies", "workflow_rules", "acme-linked-gate.yaml"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected materialized resource at %s: %v", path, err)
		}
	}
	assertFileContains(t, filepath.Join(runtimeRef.RuntimeHome(), "configs", "agents", "acme-linked-agent.yaml"), "acme-linked-helper/read_text")

	binaryRequest := httptest.NewRequest(http.MethodPost, "/api/resources/kits/scaffolds/binary-analysis?materialize=1", strings.NewReader(`{
		"name": "acme-binary",
		"title": "ACME Binary Analysis Kit"
	}`))
	binaryRequest.Header.Set("Content-Type", "application/json")
	binaryResponse := httptest.NewRecorder()
	server.ServeHTTP(binaryResponse, binaryRequest)
	if binaryResponse.Code != http.StatusCreated {
		t.Fatalf("expected binary materialized scaffold create 201, got %d body=%s", binaryResponse.Code, binaryResponse.Body.String())
	}
	var binaryMaterialized kitScaffoldResponse
	if err := json.NewDecoder(binaryResponse.Body).Decode(&binaryMaterialized); err != nil {
		t.Fatalf("decode binary materialized scaffold response: %v", err)
	}
	if !binaryMaterialized.Materialized || !binaryMaterialized.RestartRequired || binaryMaterialized.Kit.Name != "acme-binary" || !binaryMaterialized.Kit.Validation.Valid {
		t.Fatalf("expected valid binary materialized kit response, got %#v", binaryMaterialized)
	}
	assertFileContains(t, filepath.Join(runtimeRef.RuntimeHome(), "kits", "acme-binary", "kit.yaml"), "recommended_agent: acme-binary-agent")
	assertFileContains(t, filepath.Join(runtimeRef.RuntimeHome(), "configs", "agents", "acme-binary-agent.yaml"), "acme-binary-helper/binary_file_info")
	assertFileContains(t, filepath.Join(runtimeRef.RuntimeHome(), "configs", "agents", "acme-binary-agent.yaml"), "acme-binary-helper/binary_strings")
	assertFileContains(t, filepath.Join(runtimeRef.RuntimeHome(), "configs", "agents", "acme-binary-agent.yaml"), "acme-binary-helper/hex_preview")
	assertFileContains(t, filepath.Join(runtimeRef.RuntimeHome(), "skills", "acme-binary-skill", "SKILL.md"), "binary_file_info")
	assertFileContains(t, filepath.Join(runtimeRef.RuntimeHome(), "skills", "acme-binary-skill", "SKILL.md"), "binary_strings")
	assertFileContains(t, filepath.Join(runtimeRef.RuntimeHome(), "mcp_servers", "acme-binary-helper.py"), "\"name\": \"binary_file_info\"")
	assertFileContains(t, filepath.Join(runtimeRef.RuntimeHome(), "mcp_servers", "acme-binary-helper.py"), "\"name\": \"binary_strings\"")
	assertFileContains(t, filepath.Join(runtimeRef.RuntimeHome(), "mcp_servers", "acme-binary-helper.py"), "\"name\": \"hex_preview\"")
	assertFileContains(t, filepath.Join(runtimeRef.RuntimeHome(), "templates", "teams", "acme-binary-team.yaml"), "acme-binary-helper/binary_file_info")
	assertFileContains(t, filepath.Join(runtimeRef.RuntimeHome(), "templates", "teams", "acme-binary-team.yaml"), "acme-binary-helper/hex_preview")

	operationsRequest := httptest.NewRequest(http.MethodPost, "/api/resources/kits/scaffolds/operations-runbook?materialize=1", strings.NewReader(`{
		"name": "acme-ops",
		"title": "ACME Operations Kit"
	}`))
	operationsRequest.Header.Set("Content-Type", "application/json")
	operationsResponse := httptest.NewRecorder()
	server.ServeHTTP(operationsResponse, operationsRequest)
	if operationsResponse.Code != http.StatusCreated {
		t.Fatalf("expected operations materialized scaffold create 201, got %d body=%s", operationsResponse.Code, operationsResponse.Body.String())
	}
	var operationsMaterialized kitScaffoldResponse
	if err := json.NewDecoder(operationsResponse.Body).Decode(&operationsMaterialized); err != nil {
		t.Fatalf("decode operations materialized scaffold response: %v", err)
	}
	for _, want := range []string{"acme-ops-helper", "network_tools/device_discovery_plan", "network_tools/device_command_plan", "network_tools/device_config_dry_run"} {
		if !containsString(operationsMaterialized.Kit.Tools, want) {
			t.Fatalf("expected operations materialized kit tools to include %q, got %#v", want, operationsMaterialized.Kit.Tools)
		}
	}
	assertFileContains(t, filepath.Join(runtimeRef.RuntimeHome(), "configs", "agents", "acme-ops-agent.yaml"), "network_tools/device_discovery_plan")
	assertFileContains(t, filepath.Join(runtimeRef.RuntimeHome(), "skills", "acme-ops-skill", "SKILL.md"), "network_tools/device_config_dry_run")
	assertFileContains(t, filepath.Join(runtimeRef.RuntimeHome(), "templates", "workflows", "acme-ops-template.yaml"), "domain_tool_boundaries")
}

func kitBundleSavedResourceKindExists(items []kitBundleSavedResource, kind string) bool {
	for _, item := range items {
		if item.Kind == kind {
			return true
		}
	}
	return false
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestServerConsolePageAndAssets(t *testing.T) {
	server := NewServer(newAPITestRuntime(t))

	pageRequest := httptest.NewRequest(http.MethodGet, "/console", nil)
	pageResponse := httptest.NewRecorder()
	server.ServeHTTP(pageResponse, pageRequest)
	if pageResponse.Code != http.StatusOK {
		t.Fatalf("expected console 200, got %d", pageResponse.Code)
	}
	if !strings.Contains(pageResponse.Body.String(), "GoFlow Console") || !strings.Contains(pageResponse.Body.String(), "data-app-shell") ||
		!strings.Contains(pageResponse.Body.String(), `data-view="workflows"`) || !strings.Contains(pageResponse.Body.String(), `data-view="memory"`) {
		t.Fatalf("expected modern Studio shell, got %s", pageResponse.Body.String())
	}

	assetRequest := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	assetResponse := httptest.NewRecorder()
	server.ServeHTTP(assetResponse, assetRequest)
	if assetResponse.Code != http.StatusOK {
		t.Fatalf("expected app asset 200, got %d", assetResponse.Code)
	}
	if !strings.Contains(assetResponse.Body.String(), "renderWorkflows") {
		t.Fatalf("expected modular app asset, got %s", assetResponse.Body.String())
	}

	catalogRequest := httptest.NewRequest(http.MethodGet, "/assets/views/catalog.js", nil)
	catalogResponse := httptest.NewRecorder()
	server.ServeHTTP(catalogResponse, catalogRequest)
	if catalogResponse.Code != http.StatusOK {
		t.Fatalf("expected catalog asset 200, got %d", catalogResponse.Code)
	}
	if strings.Contains(catalogResponse.Body.String(), "skills/<name>/SKILL.md") || strings.Contains(catalogResponse.Body.String(), "<code>skills/") {
		t.Fatalf("expected catalog asset to hide low-level skill storage paths from the primary resource UI, got %s", catalogResponse.Body.String())
	}

	workflowsAssetRequest := httptest.NewRequest(http.MethodGet, "/assets/views/workflows.js", nil)
	workflowsAssetResponse := httptest.NewRecorder()
	server.ServeHTTP(workflowsAssetResponse, workflowsAssetRequest)
	if workflowsAssetResponse.Code != http.StatusOK {
		t.Fatalf("expected workflows asset 200, got %d", workflowsAssetResponse.Code)
	}
	workflowsAsset := workflowsAssetResponse.Body.String()
	for _, want := range []string{
		"data-node-type-action",
		"data-advanced-guide-example",
		"data-artifact-guide-action",
		"data-acceptance-guide-action",
		"nodeTypeMetadataSourcePath",
		"shortWorkflowResourcePath",
		"expressionFunctionSourcePath",
	} {
		if !strings.Contains(workflowsAsset, want) {
			t.Fatalf("expected workflow Studio asset to contain %q", want)
		}
	}
	if strings.Contains(workflowsAsset, `<code>${escapeHTML(option.path)}</code>`) {
		t.Fatalf("expected workflow Studio asset to avoid rendering raw node metadata paths in the main inspector")
	}

	i18nAssetRequest := httptest.NewRequest(http.MethodGet, "/assets/i18n.js", nil)
	i18nAssetResponse := httptest.NewRecorder()
	server.ServeHTTP(i18nAssetResponse, i18nAssetRequest)
	if i18nAssetResponse.Code != http.StatusOK {
		t.Fatalf("expected i18n asset 200, got %d", i18nAssetResponse.Code)
	}
	i18nAsset := i18nAssetResponse.Body.String()
	for _, want := range []string{
		"workflow.nodeTypeApplyExample",
		"workflow.advancedGuideUseExample",
		"workflow.artifactGuideAddReport",
		"workflow.acceptanceGuideAddContains",
		"workflow.nodeTypeOverridePath",
		"workflow.expressionFunctionOverridePath",
	} {
		if !strings.Contains(i18nAsset, want) {
			t.Fatalf("expected i18n asset to contain %q", want)
		}
	}

	faviconRequest := httptest.NewRequest(http.MethodGet, "/favicon.ico", nil)
	faviconResponse := httptest.NewRecorder()
	server.ServeHTTP(faviconResponse, faviconRequest)
	if faviconResponse.Code != http.StatusNoContent {
		t.Fatalf("expected favicon 204, got %d", faviconResponse.Code)
	}
}

func TestServerRuntimeStatusAndUpdatePolicy(t *testing.T) {
	server := NewServer(newAPITestRuntime(t))

	statusRequest := httptest.NewRequest(http.MethodGet, "/api/runtime", nil)
	statusResponse := httptest.NewRecorder()
	server.ServeHTTP(statusResponse, statusRequest)
	if statusResponse.Code != http.StatusOK {
		t.Fatalf("expected runtime status 200, got %d", statusResponse.Code)
	}
	if !strings.Contains(statusResponse.Body.String(), `"agents"`) || !strings.Contains(statusResponse.Body.String(), `"tools"`) {
		t.Fatalf("expected runtime inventory, got %s", statusResponse.Body.String())
	}
	if !strings.Contains(statusResponse.Body.String(), `"mcp_servers"`) ||
		!strings.Contains(statusResponse.Body.String(), `"isolation_level"`) ||
		!strings.Contains(statusResponse.Body.String(), `"sandboxed":false`) ||
		!strings.Contains(statusResponse.Body.String(), `"can_access_host_outside_workspace":true`) {
		t.Fatalf("expected MCP isolation risk profile in runtime status, got %s", statusResponse.Body.String())
	}
	if !strings.Contains(statusResponse.Body.String(), `"mcp_isolation_modes"`) ||
		!strings.Contains(statusResponse.Body.String(), `"name":"container"`) ||
		strings.Contains(statusResponse.Body.String(), `"name":"windows_appcontainer"`) ||
		strings.Contains(statusResponse.Body.String(), `"future":true`) {
		t.Fatalf("expected MCP isolation mode capability catalog in runtime status, got %s", statusResponse.Body.String())
	}

	updateRequest := httptest.NewRequest(http.MethodGet, "/api/update-policy", nil)
	updateResponse := httptest.NewRecorder()
	server.ServeHTTP(updateResponse, updateRequest)
	if updateResponse.Code != http.StatusOK {
		t.Fatalf("expected update policy 200, got %d", updateResponse.Code)
	}
	if !strings.Contains(updateResponse.Body.String(), `"release-archive"`) || !strings.Contains(updateResponse.Body.String(), `"docker"`) {
		t.Fatalf("expected update strategies, got %s", updateResponse.Body.String())
	}
	if !strings.Contains(updateResponse.Body.String(), `"/api/update-policy/check"`) || !strings.Contains(updateResponse.Body.String(), `"network_opt_in":true`) {
		t.Fatalf("expected opt-in update check metadata, got %s", updateResponse.Body.String())
	}

	oldVersion := version.Version
	version.Version = "v0.1.0"
	t.Cleanup(func() { version.Version = oldVersion })
	feedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("expected release feed GET, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		archive := fmt.Sprintf("goflow-agent_v0.1.1_%s_%s.%s", goruntime.GOOS, goruntime.GOARCH, map[bool]string{true: "zip", false: "tar.gz"}[goruntime.GOOS == "windows"])
		_, _ = w.Write([]byte(`{
			"tag_name":"v0.1.1",
			"name":"GoFlow Agent v0.1.1",
			"html_url":"https://github.com/FyMatt/GoFlow-Agent/releases/tag/v0.1.1",
			"body":"Bug fixes and release notes.",
			"assets":[
				{"name":"` + archive + `","size":1234,"browser_download_url":"https://example.invalid/download.zip"},
				{"name":"SHA256SUMS","size":256,"browser_download_url":"https://example.invalid/SHA256SUMS"},
				{"name":"SBOM.spdx.json","size":2048,"browser_download_url":"https://example.invalid/SBOM.spdx.json"},
				{"name":"` + archive + `.sigstore.json","size":512,"browser_download_url":"https://example.invalid/archive.sigstore.json"},
				{"name":"SHA256SUMS.sigstore.json","size":512,"browser_download_url":"https://example.invalid/SHA256SUMS.sigstore.json"},
				{"name":"SBOM.spdx.json.sigstore.json","size":512,"browser_download_url":"https://example.invalid/SBOM.spdx.json.sigstore.json"}
			]
		}`))
	}))
	defer feedServer.Close()
	t.Setenv("GOFLOW_RELEASE_FEED", feedServer.URL)
	checkRequest := httptest.NewRequest(http.MethodPost, "/api/update-policy/check", nil)
	checkResponse := httptest.NewRecorder()
	server.ServeHTTP(checkResponse, checkRequest)
	if checkResponse.Code != http.StatusOK {
		t.Fatalf("expected update check 200, got %d body=%s", checkResponse.Code, checkResponse.Body.String())
	}
	for _, want := range []string{`"latest_version":"v0.1.1"`, `"update_available":true`, `"asset_summary"`, `"verification_ready":true`, `"has_checksums":true`, `"has_sbom":true`, `"verify_steps"`, "cosign verify-blob"} {
		if !strings.Contains(checkResponse.Body.String(), want) {
			t.Fatalf("expected update check response to contain %s, got %s", want, checkResponse.Body.String())
		}
	}

	t.Setenv("GOFLOW_DISABLE_UPDATE_CHECKS", "true")
	disabledPolicyRequest := httptest.NewRequest(http.MethodGet, "/api/update-policy", nil)
	disabledPolicyResponse := httptest.NewRecorder()
	server.ServeHTTP(disabledPolicyResponse, disabledPolicyRequest)
	if disabledPolicyResponse.Code != http.StatusOK {
		t.Fatalf("expected disabled update policy 200, got %d", disabledPolicyResponse.Code)
	}
	if !strings.Contains(disabledPolicyResponse.Body.String(), `"check_enabled":false`) ||
		!strings.Contains(disabledPolicyResponse.Body.String(), `"disabled_reason"`) {
		t.Fatalf("expected disabled update check metadata, got %s", disabledPolicyResponse.Body.String())
	}
	disabledCheckRequest := httptest.NewRequest(http.MethodPost, "/api/update-policy/check", nil)
	disabledCheckResponse := httptest.NewRecorder()
	server.ServeHTTP(disabledCheckResponse, disabledCheckRequest)
	if disabledCheckResponse.Code != http.StatusForbidden {
		t.Fatalf("expected disabled update check 403, got %d body=%s", disabledCheckResponse.Code, disabledCheckResponse.Body.String())
	}
}

func TestServerRuntimeStatusMarksSensitiveMCPEnvAllowlist(t *testing.T) {
	t.Setenv("MCP_API_KEY", "super-secret-value")
	runtimeRef := newAPITestRuntimeWithStateHomeResponsesAndMCP(t, session.New(8), "", nil, []config.MCPServerRef{{
		Name:         "secrets",
		Command:      "python",
		Enabled:      true,
		Isolation:    "process_group",
		EnvAllowlist: []string{"PATH", "MCP_API_KEY", "MCP_API_KEY"},
	}})
	server := NewServer(runtimeRef)

	statusRequest := httptest.NewRequest(http.MethodGet, "/api/runtime", nil)
	statusResponse := httptest.NewRecorder()
	server.ServeHTTP(statusResponse, statusRequest)
	if statusResponse.Code != http.StatusOK {
		t.Fatalf("expected runtime status 200, got %d body=%s", statusResponse.Code, statusResponse.Body.String())
	}
	body := statusResponse.Body.String()
	for _, expected := range []string{
		`"name":"secrets"`,
		`"env_allowlist":["MCP_API_KEY","PATH"]`,
		`"sensitive_env":["MCP_API_KEY"]`,
		`env_allowlist includes sensitive variable names`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("expected runtime MCP env risk response to contain %s, got %s", expected, body)
		}
	}
	if strings.Contains(body, "super-secret-value") {
		t.Fatalf("expected runtime status to omit secret env values, got %s", body)
	}
}

func TestServerRuntimeStatusReportsContainerIsolationProperties(t *testing.T) {
	runtimeRef := newAPITestRuntimeWithStateHomeResponsesAndMCP(t, session.New(8), "", nil, []config.MCPServerRef{{
		Name:            "stub",
		Command:         "/app/bin/file_tools",
		Args:            []string{"--stdio"},
		Enabled:         true,
		NetworkDisabled: true,
		Isolation:       "container",
		IsolationOptions: map[string]string{
			"image":             "goflow/mcp-tools@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			"network":           "disabled",
			"ipc":               "none",
			"userns":            "auto",
			"workspace_mount":   "ro",
			"memory":            "256m",
			"memory_swap":       "256m",
			"cpus":              "0.5",
			"pids_limit":        "64",
			"no_new_privileges": "true",
			"cap_drop":          "all",
			"user":              "65532:65532",
		},
	}})
	server := NewServer(runtimeRef)
	request := httptest.NewRequest(http.MethodGet, "/api/runtime", nil)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected runtime status 200, got %d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, want := range []string{
		`"name":"stub"`,
		`"isolation":"container"`,
		`"isolation_level":"container_configured"`,
		`"risk_level":"low"`,
		`"sandboxed":true`,
		`"network_enforced":true`,
		`"filesystem_sandboxed":true`,
		`"privilege_sandboxed":true`,
		`"resource_limited":true`,
		`"sandbox_features":["container_runtime","filesystem_mount_policy","privilege_reduction","network_policy","resource_limits","no_new_privileges","cap_drop_all","ipc_namespace_none","user_namespace","non_root_user"]`,
		`"can_access_host_outside_workspace":false`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected runtime status to contain %s, got %s", want, body)
		}
	}

	toolsRequest := httptest.NewRequest(http.MethodGet, "/api/resources/tools", nil)
	toolsResponse := httptest.NewRecorder()
	server.ServeHTTP(toolsResponse, toolsRequest)
	if toolsResponse.Code != http.StatusOK {
		t.Fatalf("expected tool list 200, got %d body=%s", toolsResponse.Code, toolsResponse.Body.String())
	}
	if !strings.Contains(toolsResponse.Body.String(), `"sandbox_features":["container_runtime","filesystem_mount_policy","privilege_reduction","network_policy","resource_limits","no_new_privileges","cap_drop_all","ipc_namespace_none","user_namespace","non_root_user"]`) {
		t.Fatalf("expected tool risk profile to include non_root_user, got %s", toolsResponse.Body.String())
	}
}

func TestServerRuntimeStatusRecommendsContainerNonRootUserForReadOnlyMounts(t *testing.T) {
	runtimeRef := newAPITestRuntimeWithStateHomeResponsesAndMCP(t, session.New(8), "", nil, []config.MCPServerRef{{
		Name:            "reader_tools",
		Command:         "python",
		Args:            []string{"server.py"},
		Enabled:         true,
		NetworkDisabled: true,
		Isolation:       "container",
		IsolationOptions: map[string]string{
			"image":           "goflow/mcp-reader:1.0.0",
			"network":         "disabled",
			"workspace_mount": "ro",
			"memory":          "256m",
			"memory_swap":     "256m",
			"cpus":            "0.5",
			"pids_limit":      "64",
		},
	}})
	server := NewServer(runtimeRef)
	request := httptest.NewRequest(http.MethodGet, "/api/runtime", nil)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected runtime status 200, got %d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, `"risk_level":"medium"`) {
		t.Fatalf("expected read-only container without explicit non-root user to remain medium risk, got %s", body)
	}
	if !strings.Contains(body, `set isolation_options.user to a non-root UID:GID`) {
		t.Fatalf("expected runtime status to recommend non-root user for read-only container, got %s", body)
	}
	if strings.Contains(body, `"non_root_user"`) {
		t.Fatalf("expected runtime status not to claim non_root_user when user is absent, got %s", body)
	}
}

func TestServerRuntimeStatusRequiresCompleteContainerResourceLimits(t *testing.T) {
	runtimeRef := newAPITestRuntimeWithStateHomeResponsesAndMCP(t, session.New(8), "", nil, []config.MCPServerRef{{
		Name:            "partial_limit_tools",
		Command:         "python",
		Args:            []string{"server.py"},
		Enabled:         true,
		NetworkDisabled: true,
		Isolation:       "container",
		IsolationOptions: map[string]string{
			"image":           "goflow/mcp-partial:1.0.0",
			"network":         "disabled",
			"workspace_mount": "ro",
			"memory":          "256m",
			"user":            "65532:65532",
		},
	}})
	server := NewServer(runtimeRef)
	request := httptest.NewRequest(http.MethodGet, "/api/runtime", nil)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected runtime status 200, got %d body=%s", response.Code, response.Body.String())
	}
	var decoded runtimeStatusResponse
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode runtime status: %v", err)
	}
	summary := mcpServerSummaryByName(decoded.MCPServers, "partial_limit_tools")
	if summary == nil {
		t.Fatalf("expected partial_limit_tools summary, got %#v", decoded.MCPServers)
	}
	for _, want := range []string{
		`container CPU, memory, and process-count resource limits are incomplete`,
	} {
		if !configDiagnosticsStringSliceContains(summary.Warnings, want) {
			t.Fatalf("expected runtime status warning %s, got %#v", want, summary.Warnings)
		}
	}
	if summary.RiskLevel != "medium" || summary.ResourceLimited || !configDiagnosticsStringSliceContains(summary.MissingSandboxFeatures, "resource_limits") ||
		configDiagnosticsStringSliceContains(summary.SandboxFeatures, "resource_limits") {
		t.Fatalf("expected partial resource limits not to claim low risk or resource_limits feature, got %#v", summary)
	}
}

func TestServerRuntimeStatusExposesContainerImageProvenance(t *testing.T) {
	runtimeRef := newAPITestRuntimeWithStateHomeResponsesAndMCP(t, session.New(8), "", nil, []config.MCPServerRef{{
		Name:             "prod_tools",
		Command:          "python",
		Args:             []string{"server.py"},
		Enabled:          true,
		NetworkDisabled:  true,
		Isolation:        "container",
		IsolationProfile: "production",
		IsolationOptions: map[string]string{
			"image":             "goflow/mcp-prod:1.2.3",
			"network":           "disabled",
			"workspace_mount":   "ro",
			"memory":            "256m",
			"memory_swap":       "256m",
			"cpus":              "0.5",
			"pids_limit":        "64",
			"pull_policy":       "missing",
			"no_new_privileges": "true",
			"cap_drop":          "all",
			"readonly_rootfs":   "true",
			"user":              "65532:65532",
			"tmpfs":             "/tmp:rw,noexec,nosuid,size=64m",
			"init":              "true",
			"ipc":               "none",
			"userns":            "auto",
		},
	}})
	server := NewServer(runtimeRef)
	request := httptest.NewRequest(http.MethodGet, "/api/runtime", nil)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected runtime status 200, got %d body=%s", response.Code, response.Body.String())
	}
	var decoded runtimeStatusResponse
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode runtime status: %v", err)
	}
	summary := mcpServerSummaryByName(decoded.MCPServers, "prod_tools")
	if summary == nil {
		t.Fatalf("expected prod_tools summary, got %#v", decoded.MCPServers)
	}
	if summary.ContainerImage != "goflow/mcp-prod:1.2.3" ||
		summary.ContainerImageReferenceType != "version_tag" ||
		summary.ContainerPullPolicy != "missing" ||
		summary.ContainerImageDigestPinned == nil ||
		*summary.ContainerImageDigestPinned ||
		summary.ContainerImageProductionReady == nil ||
		*summary.ContainerImageProductionReady {
		t.Fatalf("expected structured container image provenance, got %#v", summary)
	}
	if !configDiagnosticsStringSliceContains(summary.Warnings, "production container profile requires an immutable image@sha256:... reference") ||
		!configDiagnosticsStringSliceContains(summary.Warnings, "production container profile should use pull_policy=never with pre-pulled trusted images") {
		t.Fatalf("expected production image provenance warnings, got %#v", summary.Warnings)
	}
}

func TestServerRuntimeStatusWarnsContainerRootUser(t *testing.T) {
	runtimeRef := newAPITestRuntimeWithStateHomeResponsesAndMCP(t, session.New(8), "", nil, []config.MCPServerRef{{
		Name:            "root_tools",
		Command:         "python",
		Args:            []string{"server.py"},
		Enabled:         true,
		NetworkDisabled: true,
		Isolation:       "container",
		IsolationOptions: map[string]string{
			"image":           "goflow/mcp-root:1.0.0",
			"network":         "disabled",
			"workspace_mount": "ro",
			"memory":          "256m",
			"memory_swap":     "256m",
			"cpus":            "0.5",
			"pids_limit":      "64",
			"user":            "root:1000",
		},
	}})
	server := NewServer(runtimeRef)
	request := httptest.NewRequest(http.MethodGet, "/api/runtime", nil)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected runtime status 200, got %d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, `"risk_level":"medium"`) {
		t.Fatalf("expected root user container to remain medium risk, got %s", body)
	}
	if !strings.Contains(body, `container explicitly runs as root`) ||
		!strings.Contains(body, `set isolation_options.user to a non-root UID:GID`) {
		t.Fatalf("expected runtime status to warn and recommend non-root user, got %s", body)
	}
	if strings.Contains(body, `"non_root_user"`) {
		t.Fatalf("expected runtime status not to claim non_root_user for root user, got %s", body)
	}
}

func TestServerRuntimeStatusExposesMCPCallMetrics(t *testing.T) {
	runtimeRef := newAPITestRuntimeWithStateHomeResponsesAndMCP(t, session.New(8), "", nil, []config.MCPServerRef{{
		Name:               "file_tools",
		Command:            "file_tools",
		Enabled:            true,
		MaxConcurrentCalls: 3,
	}})
	mcpClient, ok := runtimeRef.MCPForTesting().(*apiTestMCP)
	if !ok {
		t.Fatalf("expected apiTestMCP, got %T", runtimeRef.MCPForTesting())
	}
	mcpClient.SetMetrics(map[string]interfaces.MCPServerCallMetrics{
		"file_tools": {
			MaxConcurrentCalls: 3,
			ActiveCalls:        1,
			QueuedCalls:        2,
			AvailableCallSlots: 2,
		},
	})
	server := NewServer(runtimeRef)
	request := httptest.NewRequest(http.MethodGet, "/api/runtime", nil)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected runtime status 200, got %d body=%s", response.Code, response.Body.String())
	}
	var decoded runtimeStatusResponse
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode runtime status: %v", err)
	}
	summary := mcpServerSummaryByName(decoded.MCPServers, "file_tools")
	if summary == nil {
		t.Fatalf("expected file_tools summary, got %#v", decoded.MCPServers)
	}
	if summary.MaxConcurrentCalls != 3 || summary.ActiveCalls != 1 || summary.QueuedCalls != 2 || summary.AvailableCallSlots != 2 {
		t.Fatalf("expected MCP call metrics in runtime status, got %#v", summary)
	}
}

func TestServerRuntimeStatusRequiresExactCapDropAll(t *testing.T) {
	runtimeRef := newAPITestRuntimeWithStateHomeResponsesAndMCP(t, session.New(8), "", nil, []config.MCPServerRef{{
		Name:            "cap_tools",
		Command:         "python",
		Args:            []string{"server.py"},
		Enabled:         true,
		NetworkDisabled: true,
		Isolation:       "container",
		IsolationOptions: map[string]string{
			"image":             "goflow/mcp-cap:1.0.0",
			"network":           "disabled",
			"workspace_mount":   "ro",
			"memory":            "256m",
			"memory_swap":       "256m",
			"cpus":              "0.5",
			"pids_limit":        "64",
			"user":              "65532:65532",
			"no_new_privileges": "true",
			"cap_drop":          "small",
		},
	}})
	server := NewServer(runtimeRef)
	request := httptest.NewRequest(http.MethodGet, "/api/runtime", nil)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected runtime status 200, got %d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if strings.Contains(body, `"cap_drop_all"`) {
		t.Fatalf("expected cap_drop=small not to claim cap_drop_all, got %s", body)
	}
	if !strings.Contains(body, `set isolation_options.cap_drop=all`) {
		t.Fatalf("expected runtime status to recommend cap_drop=all, got %s", body)
	}
}

func TestServerRuntimeStatusReportsLinuxNetworkNamespaceIsolationProperties(t *testing.T) {
	runtimeRef := newAPITestRuntimeWithStateHomeResponsesAndMCP(t, session.New(8), "", nil, []config.MCPServerRef{{
		Name:            "netns_tools",
		Command:         "python",
		Args:            []string{"server.py"},
		Enabled:         true,
		NetworkDisabled: true,
		Isolation:       "linux_netns",
		AllowedCommands: []string{"python"},
	}})
	server := NewServer(runtimeRef)
	request := httptest.NewRequest(http.MethodGet, "/api/runtime", nil)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected runtime status 200, got %d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, want := range []string{
		`"name":"netns_tools"`,
		`"isolation":"linux_netns"`,
		`"isolation_level":"network_namespace"`,
		`"sandboxed":true`,
		`"network_enforced":true`,
		`"filesystem_sandboxed":false`,
		`"privilege_sandboxed":false`,
		`"can_access_host_outside_workspace":true`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected runtime status to contain %s, got %s", want, body)
		}
	}
	if strings.Contains(body, "network_disabled is advisory") {
		t.Fatalf("expected linux_netns not to report network_disabled as advisory, got %s", body)
	}
}

func TestServerRuntimeStatusReportsWindowsJobLifecycleOnlyProperties(t *testing.T) {
	runtimeRef := newAPITestRuntimeWithStateHomeResponsesAndMCP(t, session.New(8), "", nil, []config.MCPServerRef{{
		Name:            "stub",
		Command:         "python",
		Args:            []string{"server.py"},
		Enabled:         true,
		NetworkDisabled: true,
		Isolation:       "windows_job",
		AllowedCommands: []string{"python"},
	}})
	server := NewServer(runtimeRef)
	request := httptest.NewRequest(http.MethodGet, "/api/runtime", nil)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected runtime status 200, got %d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, want := range []string{
		`"name":"stub"`,
		`"isolation":"windows_job"`,
		`"isolation_level":"lifecycle"`,
		`"sandboxed":false`,
		`"network_enforced":false`,
		`"filesystem_sandboxed":false`,
		`"privilege_sandboxed":false`,
		`"sandbox_features":["windows_job_object_lifecycle"]`,
		`"missing_sandbox_features":["windows_restricted_token","filesystem_policy","network_policy","privilege_reduction"]`,
		`"windows_isolation":{"job_object":true,"restricted_token":false,"app_container":false,"lifecycle_only":true}`,
		`not a restricted-token sandbox`,
		`network_disabled is advisory`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected runtime status to contain %s, got %s", want, body)
		}
	}

	toolsRequest := httptest.NewRequest(http.MethodGet, "/api/resources/tools", nil)
	toolsResponse := httptest.NewRecorder()
	server.ServeHTTP(toolsResponse, toolsRequest)
	if toolsResponse.Code != http.StatusOK {
		t.Fatalf("expected tool list 200, got %d body=%s", toolsResponse.Code, toolsResponse.Body.String())
	}
	toolsBody := toolsResponse.Body.String()
	for _, want := range []string{
		`"name":"stub/write_file"`,
		`"sandbox_features":["windows_job_object_lifecycle"]`,
		`"missing_sandbox_features":["windows_restricted_token","filesystem_policy","network_policy","privilege_reduction"]`,
		`"windows_isolation":{"job_object":true,"restricted_token":false,"app_container":false,"lifecycle_only":true}`,
	} {
		if !strings.Contains(toolsBody, want) {
			t.Fatalf("expected tool risk profile to contain %s, got %s", want, toolsBody)
		}
	}
}

func TestServerRuntimeStatusReportsWindowsRestrictedTokenIsolationProperties(t *testing.T) {
	runtimeRef := newAPITestRuntimeWithStateHomeResponsesAndMCP(t, session.New(8), "", nil, []config.MCPServerRef{{
		Name:            "restricted",
		Command:         "python",
		Args:            []string{"server.py"},
		Enabled:         true,
		NetworkDisabled: true,
		Isolation:       "windows_restricted_token",
		AllowedCommands: []string{"python"},
	}})
	server := NewServer(runtimeRef)
	request := httptest.NewRequest(http.MethodGet, "/api/runtime", nil)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected runtime status 200, got %d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, want := range []string{
		`"name":"restricted"`,
		`"isolation":"windows_restricted_token"`,
		`"isolation_level":"windows_restricted_token"`,
		`"sandboxed":true`,
		`"network_enforced":false`,
		`"filesystem_sandboxed":false`,
		`"privilege_sandboxed":true`,
		`"sandbox_features":["windows_restricted_token","windows_low_integrity","windows_job_object_lifecycle","process_group_lifecycle"]`,
		`"missing_sandbox_features":["filesystem_policy","network_policy"]`,
		`"windows_isolation":{"job_object":true,"restricted_token":true,"app_container":false,"lifecycle_only":false}`,
		`windows_restricted_token does not restrict filesystem paths or network egress by itself`,
		`network_disabled is advisory`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected runtime status to contain %s, got %s", want, body)
		}
	}
}

func TestServerHelpEndpointExposesHTTPParityMap(t *testing.T) {
	server := NewServer(newAPITestRuntime(t))
	request := httptest.NewRequest(http.MethodGet, "/api/help", nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected help 200, got %d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	var decoded helpResponse
	if err := json.NewDecoder(strings.NewReader(body)).Decode(&decoded); err != nil {
		t.Fatalf("decode help response: %v", err)
	}
	if decoded.Meta.Schema != apiDiscoverySchema ||
		decoded.Meta.SchemaVersion != apiDiscoverySchemaVersion ||
		decoded.Meta.MinSupportedSchemaVersion != 1 ||
		decoded.Meta.RuntimeVersion == "" ||
		!strings.Contains(decoded.Meta.CacheKey, apiDiscoverySchema) {
		t.Fatalf("expected help discovery metadata, got %#v", decoded.Meta)
	}
	for _, want := range []string{
		`"schema":"goflow.api.discovery"`,
		`"schema_version":1`,
		`"runtime_version"`,
		`"cache_key"`,
		`"/trace on|off"`,
		`"/api/runtime/trace"`,
		`"background_workflow_runs":true`,
		`"durable_run_sse_heartbeats":true`,
		`"durable_run_sse_retry_directive":true`,
		`"run_action_protocol_metadata":true`,
		`"unified_run_action_filter":true`,
		`"unified_run_action_facets":true`,
		`"workspace_requirement_preflight":true`,
		`"runtime_workspace_action_metadata":true`,
		`"workspace_switch_blocker_details":true`,
		`"workspace_switch_guidance":true`,
		`"cost_control_feature_catalog":true`,
		`"runtime_tool_risk_policy_status":true`,
		`"tool_risk_policy_sandbox_boundaries":true`,
		`"config_diagnostics_field_targets":true`,
		`"config_diagnostics_severity_counts":true`,
		`"config_diagnostics_recommendations":true`,
		`"config_diagnostics_mcp_hardening":true`,
		`"config_diagnostics_tool_risk_policy":true`,
		`"config_diagnostics_tool_risk_policy_mcp_boundaries":true`,
		`"config_diagnostics_apply_summary":true`,
		`"config_load_failure_field_targets":true`,
		`"mcp_isolation_mode_catalog":true`,
		`"mcp_isolation_modes"`,
		`"name":"container"`,
		`"requires_options":["image"]`,
		`"resource_capability_metadata":true`,
		`"client_contract_metadata":true`,
		`"client_contracts"`,
		`"area":"ordinary_agent_runs"`,
		`"area":"cost_control"`,
		`"area":"configuration_apply"`,
		`read /api/runtime.tool_risk_policy`,
		`recognized_sandbox_boundaries`,
		`"reconnect_strategy"`,
		`"restart_behavior"`,
		`"resource_capabilities"`,
		`"apply_state_on_save":"restart_required"`,
		`"restart_required_on_save":true`,
		`"storage_root":"configs/agents"`,
		`"actions"`,
		`"name":"export_bundle"`,
		`"path":"/api/resources/kits/{name}/export?format=json|yaml"`,
		`"name":"activate"`,
		`"path":"/api/resources/workflow-schemas/{name}/activate"`,
		`"/config-diagnostics [--json]"`,
		`"/workspace clear"`,
		`"/workspace use \u003cpath\u003e"`,
		`"/workspace choose"`,
		`"/api/workspace/pick-folder"`,
		`"/api/workspace/requirement"`,
		`"http_cli_parity_matrix":true`,
		`"policy_rule_scaffold_presets":true`,
		`"/policy-rules [name]"`,
		`"/new-policy-rule \u003cpreset\u003e \u003cname\u003e"`,
		`"/teams [name]"`,
		`"/new-team \u003cpreset\u003e \u003cname\u003e"`,
		`"/workflow-templates [name]"`,
		`"/workflow-node-metadata [type]"`,
		`"/expression-helpers [name]"`,
		`"/workflow-schemas [name] [--rebuild|--json|--export|--import \u003cpath\u003e|--clear]"`,
		`"/new-workflow-template \u003csource\u003e \u003cname\u003e"`,
		`"/kits [name] [--export] | /kits --import \u003cpath\u003e"`,
		`"/kits --import \u003cpath\u003e"`,
		`"/new-kit \u003cpreset\u003e \u003cname\u003e"`,
		`"/api/resources/policy-rules"`,
		`"/api/resources/policy-rules/scaffolds"`,
		`"/api/resources/workflow-schemas"`,
		`"/api/resources/workflow-node-metadata"`,
		`"/api/resources/expression-helpers"`,
		`"/api/resources/team-templates/scaffolds/{preset}"`,
		`"/api/resources/workflow-templates/{name}/fork"`,
		`"/api/resources/kits/scaffolds/{preset}"`,
		`"/api/resources/kits/{name}/export"`,
		`"/api/resources/kits/import"`,
		`workspace_capabilities and workspace_actions`,
		`switch_blocker_details`,
		`capabilities.guidance`,
		`"capability":"Unified Agent and workflow run history"`,
		`"/api/workflow-runs/{id}/events/stream?since=0"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected help response to contain %s, got %s", want, body)
		}
	}
	if decoded.Coverage.Total == 0 || decoded.Coverage.Implemented != decoded.Coverage.Total {
		t.Fatalf("expected complete help parity coverage, got %#v", decoded.Coverage)
	}
	if decoded.Capabilities["mcp_isolation_mode_catalog"] != true {
		t.Fatalf("expected MCP isolation mode catalog capability flag, got %#v", decoded.Capabilities)
	}
	if decoded.Capabilities["mcp_isolation_mode_recommendations"] != true {
		t.Fatalf("expected MCP isolation mode recommendation capability flag, got %#v", decoded.Capabilities)
	}
	if decoded.Capabilities["tool_risk_policy_sandbox_boundaries"] != true {
		t.Fatalf("expected tool risk policy sandbox boundary capability flag, got %#v", decoded.Capabilities)
	}
	if decoded.Capabilities["container_tool_scaffold_hardened_defaults"] != true {
		t.Fatalf("expected container tool scaffold hardened defaults capability flag, got %#v", decoded.Capabilities)
	}
	if decoded.Capabilities["container_isolation_profiles"] != true {
		t.Fatalf("expected container isolation profiles capability flag, got %#v", decoded.Capabilities)
	}
	if decoded.Capabilities["container_tool_scaffold_versioned_image_default"] != true {
		t.Fatalf("expected container tool scaffold versioned image default capability flag, got %#v", decoded.Capabilities)
	}
	if decoded.Capabilities["tool_scaffold_default_option_metadata"] != true {
		t.Fatalf("expected tool scaffold default option metadata capability flag, got %#v", decoded.Capabilities)
	}
	containerMode := helpMCPIsolationModeByName(decoded.MCPIsolationModes, "container")
	if containerMode == nil ||
		!containerMode.ConfigSupported ||
		!containerMode.Implemented ||
		!containerMode.Recommended ||
		!containerMode.Sandboxed ||
		!containerMode.FilesystemSandboxed ||
		!configDiagnosticsStringSliceContains(containerMode.RecommendedFor, "third_party_tools") ||
		!configDiagnosticsStringSliceContains(containerMode.RecommendedFor, "exec_tools") ||
		!configDiagnosticsStringSliceContains(containerMode.RequiresOptions, "image") ||
		!mcpIsolationModeOptionExists(containerMode.Options, "image", "string", true) ||
		!mcpIsolationModeOptionExists(containerMode.Options, "workspace_mount", "enum", false) ||
		!mcpIsolationModeOptionExists(containerMode.Options, "ipc", "enum", false) ||
		!mcpIsolationModeOptionExists(containerMode.Options, "userns", "enum", false) ||
		!mcpIsolationModeOptionExists(containerMode.Options, "readonly_rootfs", "boolean_string", false) ||
		!mcpIsolationModeOptionExists(containerMode.Options, "tool_mount", "enum", false) {
		t.Fatalf("expected container MCP isolation mode metadata, got %#v", containerMode)
	}
	if readonlyProfile := mcpIsolationProfileByName(containerMode.Profiles, "readonly"); readonlyProfile == nil ||
		readonlyProfile.DefaultOptions["workspace_mount"] != "ro" ||
		readonlyProfile.DefaultOptions["network"] != "disabled" ||
		readonlyProfile.DefaultOptions["user"] != "65532:65532" ||
		!configDiagnosticsStringSliceContains(readonlyProfile.RecommendedFor, "untrusted_helpers") {
		t.Fatalf("expected readonly container profile metadata, got %#v", containerMode.Profiles)
	}
	if productionProfile := mcpIsolationProfileByName(containerMode.Profiles, "production"); productionProfile == nil ||
		!productionProfile.RequiresDigest ||
		productionProfile.DefaultOptions["pull_policy"] != "never" {
		t.Fatalf("expected production container profile metadata, got %#v", containerMode.Profiles)
	}
	linuxCgroupMode := helpMCPIsolationModeByName(decoded.MCPIsolationModes, "linux_cgroup")
	if linuxCgroupMode == nil ||
		!linuxCgroupMode.Fallback ||
		linuxCgroupMode.FallbackKind != "linux_resource_control" ||
		!mcpIsolationModeOptionExists(linuxCgroupMode.Options, "memory_max", "size_or_max", false) ||
		!mcpIsolationModeOptionExists(linuxCgroupMode.Options, "cpu_max", "cpu_quota_period_or_max", false) {
		t.Fatalf("expected linux_cgroup option metadata, got %#v", linuxCgroupMode)
	}
	linuxNetnsMode := helpMCPIsolationModeByName(decoded.MCPIsolationModes, "linux_netns")
	if linuxNetnsMode == nil ||
		!mcpIsolationModeOptionExists(linuxNetnsMode.Options, "unshare_command", "command", false) ||
		!mcpIsolationModeOptionExists(linuxNetnsMode.Options, "map_root_user", "boolean_string", false) {
		t.Fatalf("expected linux_netns option metadata, got %#v", linuxNetnsMode)
	}
	if appContainerMode := helpMCPIsolationModeByName(decoded.MCPIsolationModes, "windows_appcontainer"); appContainerMode != nil {
		t.Fatalf("expected AppContainer not to be exposed as an isolation mode, got %#v", appContainerMode)
	}
	agentRunContract := helpClientContractByArea(decoded.ClientContracts, "ordinary_agent_runs")
	if agentRunContract == nil ||
		!configDiagnosticsStringSliceContains(agentRunContract.PrimaryEndpoints, "POST /api/run") ||
		!configDiagnosticsStringSliceContains(agentRunContract.StreamEndpoints, "GET /api/agent-runs/{id}/events/stream?since=<seq>") ||
		!strings.Contains(agentRunContract.ReconnectStrategy, "since=<last_seq>") ||
		!strings.Contains(agentRunContract.ReconnectStrategy, "retry directives") ||
		!strings.Contains(agentRunContract.ReconnectStrategy, "heartbeat") {
		t.Fatalf("expected ordinary agent durable-run client contract, got %#v", agentRunContract)
	}
	configApplyContract := helpClientContractByArea(decoded.ClientContracts, "configuration_apply")
	if configApplyContract == nil ||
		!strings.Contains(configApplyContract.RestartBehavior, "runtime_rebootstrap_supported") ||
		!configDiagnosticsStringSliceContains(configApplyContract.PrimaryEndpoints, "GET /api/config/diagnostics") ||
		!configDiagnosticsStringSliceContains(configApplyContract.RequiredBehavior, "render tool_risk_policy_* diagnostics as global safety-policy guidance, including the warning state where approval is required but approve-and-remember remains allowed") ||
		!configDiagnosticsStringSliceContains(configApplyContract.RequiredBehavior, "render tool_risk_policy_mcp_* diagnostics on each MCP server row to explain broad sandbox, network-only sandbox, or unsandboxed risk-policy treatment") {
		t.Fatalf("expected configuration apply client contract, got %#v", configApplyContract)
	}
	workspaceContract := helpClientContractByArea(decoded.ClientContracts, "workspace")
	if workspaceContract == nil ||
		!strings.Contains(workspaceContract.WorkspaceBehavior, "dynamic_rebind") ||
		!strings.Contains(workspaceContract.WorkspaceBehavior, "restart_required") ||
		!strings.Contains(workspaceContract.WorkspaceBehavior, "guidance steps") ||
		!configDiagnosticsStringSliceContains(workspaceContract.RequiredBehavior, "prefer /api/workspace capabilities.guidance for preflight, select, conflict, refresh, and restart UI sequencing") ||
		!configDiagnosticsStringSliceContains(workspaceContract.PrimaryEndpoints, "GET /api/workspace-files") {
		t.Fatalf("expected workspace client contract, got %#v", workspaceContract)
	}
	costContract := helpClientContractByArea(decoded.ClientContracts, "cost_control")
	if costContract == nil ||
		!configDiagnosticsStringSliceContains(costContract.PrimaryEndpoints, "GET /api/runtime/cost") ||
		!configDiagnosticsStringSliceContains(costContract.RequiredBehavior, "use cost.features to render enabled/observed/configured cost-control capabilities instead of parsing recommendation text") {
		t.Fatalf("expected cost-control client contract, got %#v", costContract)
	}
	approvalContract := helpClientContractByArea(decoded.ClientContracts, "approvals")
	if approvalContract == nil ||
		!configDiagnosticsStringSliceContains(approvalContract.RequiredBehavior, "use /api/runtime.tool_risk_policy.recognized_sandbox_boundaries to render policy-recognized sandbox modes by tool kind instead of parsing explanation text") {
		t.Fatalf("expected approvals client contract to advertise structured risk-policy sandbox boundaries, got %#v", approvalContract)
	}
	agentCapability := helpResourceCapabilityByKind(decoded.ResourceCapabilities, "agent")
	if agentCapability == nil ||
		!agentCapability.CanCreate ||
		!agentCapability.CanValidate ||
		!agentCapability.RestartRequiredOnSave ||
		agentCapability.ApplyStateOnSave != "restart_required" ||
		agentCapability.DiagnosticsPath != "/api/config/diagnostics" ||
		agentCapability.StorageRoot != "configs/agents" {
		t.Fatalf("expected restart-required agent resource capability, got %#v", agentCapability)
	}
	toolCapability := helpResourceCapabilityByKind(decoded.ResourceCapabilities, "tool")
	if toolCapability == nil ||
		!toolCapability.CanScaffold ||
		!toolCapability.RestartRequiredOnSave ||
		toolCapability.ScaffoldPath != "/api/resources/tools/scaffolds" ||
		!configDiagnosticsStringSliceContains(toolCapability.RelatedCapabilities, "container_tool_scaffold_hardened_defaults") ||
		!configDiagnosticsStringSliceContains(toolCapability.RelatedCapabilities, "container_tool_scaffold_versioned_image_default") ||
		!configDiagnosticsStringSliceContains(toolCapability.RelatedCapabilities, "tool_scaffold_default_option_metadata") ||
		!configDiagnosticsStringSliceContains(toolCapability.RelatedCapabilities, "config_diagnostics_mcp_hardening") {
		t.Fatalf("expected scaffoldable restart-required tool resource capability, got %#v", toolCapability)
	}
	skillCapability := helpResourceCapabilityByKind(decoded.ResourceCapabilities, "skill")
	if skillCapability == nil ||
		!skillCapability.HotReloadSupported ||
		skillCapability.RestartRequiredOnSave ||
		skillCapability.ApplyStateOnSave != "hot_reload_when_supported" {
		t.Fatalf("expected conditionally hot-reloadable skill resource capability, got %#v", skillCapability)
	}
	workflowCapability := helpResourceCapabilityByKind(decoded.ResourceCapabilities, "workflow_template")
	if workflowCapability == nil ||
		!workflowCapability.CanScaffold ||
		workflowCapability.RestartRequiredOnSave ||
		workflowCapability.ApplyStateOnSave != "active" ||
		!helpResourceActionExists(workflowCapability.Actions, "fork", "/api/resources/workflow-templates/{name}/fork") ||
		!helpResourceActionExists(workflowCapability.Actions, "capture", "/api/resources/workflow-templates/{name}/capture") {
		t.Fatalf("expected active workflow template resource capability, got %#v", workflowCapability)
	}
	schemaCapability := helpResourceCapabilityByKind(decoded.ResourceCapabilities, "workflow_schema")
	if schemaCapability == nil ||
		!helpResourceActionExists(schemaCapability.Actions, "activate", "/api/resources/workflow-schemas/{name}/activate") ||
		!helpResourceActionExists(schemaCapability.Actions, "capture", "/api/resources/workflow-schemas/{name}/capture") ||
		!helpResourceActionExists(schemaCapability.Actions, "import_catalog", "/api/workflow-schemas/import") {
		t.Fatalf("expected workflow schema resource actions, got %#v", schemaCapability)
	}
	kitCapability := helpResourceCapabilityByKind(decoded.ResourceCapabilities, "kit")
	if kitCapability == nil ||
		!kitCapability.CanScaffold ||
		!helpResourceActionExists(kitCapability.Actions, "export_bundle", "/api/resources/kits/{name}/export?format=json|yaml") ||
		!helpResourceActionExists(kitCapability.Actions, "import_bundle", "/api/resources/kits/import") ||
		!helpResourceActionExists(kitCapability.Actions, "validate_saved", "/api/resources/kits/{name}/validate") {
		t.Fatalf("expected kit bundle resource actions, got %#v", kitCapability)
	}
	if !helpParityContains(decoded.Parity, "history", "Unified Agent and workflow run history", "/api/runs") {
		t.Fatalf("expected unified run history parity entry, got %#v", decoded.Parity)
	}
	if !helpParityContains(decoded.Parity, "workspace", "Workspace selection, confirmation, and @file references", "/api/workspace-files") {
		t.Fatalf("expected workspace/@file parity entry, got %#v", decoded.Parity)
	}
	if !helpParityContains(decoded.Parity, "workspace", "Workspace selection, confirmation, and @file references", "/api/workspace/requirement") {
		t.Fatalf("expected workspace requirement parity entry, got %#v", decoded.Parity)
	}
	if !helpParityContains(decoded.Parity, "workflow", "Workflow authoring, validation, templates, metadata, and schemas", "/api/resources/workflow-templates/{name}/fork") {
		t.Fatalf("expected workflow template fork parity entry, got %#v", decoded.Parity)
	}
	if !helpParityContains(decoded.Parity, "workflow", "Workflow authoring, validation, templates, metadata, and schemas", "/api/workflow-expression-functions") {
		t.Fatalf("expected workflow expression functions parity entry, got %#v", decoded.Parity)
	}
	if !helpParityContains(decoded.Parity, "resources", "Edit agents, providers, tools, skills, workflow resources, team templates, and kits", "/api/resources/policy-rules") {
		t.Fatalf("expected policy rule resource parity entry, got %#v", decoded.Parity)
	}
	if !helpParityContains(decoded.Parity, "resources", "Edit agents, providers, tools, skills, workflow resources, team templates, and kits", "/api/resources/workflow-node-metadata") {
		t.Fatalf("expected workflow node metadata parity entry, got %#v", decoded.Parity)
	}
	if !helpParityContains(decoded.Parity, "resources", "Edit agents, providers, tools, skills, workflow resources, team templates, and kits", "/api/resources/expression-helpers") {
		t.Fatalf("expected expression helper metadata parity entry, got %#v", decoded.Parity)
	}
	if !helpParityContains(decoded.Parity, "resources", "Edit agents, providers, tools, skills, workflow resources, team templates, and kits", "/api/kits") {
		t.Fatalf("expected kit catalog parity entry, got %#v", decoded.Parity)
	}
	if !helpParityContains(decoded.Parity, "resources", "Edit agents, providers, tools, skills, workflow resources, team templates, and kits", "/api/resources/kits/scaffolds/{preset}") {
		t.Fatalf("expected kit scaffold parity entry, got %#v", decoded.Parity)
	}
	if !helpParityContains(decoded.Parity, "resources", "Edit agents, providers, tools, skills, workflow resources, team templates, and kits", "/api/resources/kits/import") {
		t.Fatalf("expected kit import parity entry, got %#v", decoded.Parity)
	}
	if !helpParityContains(decoded.Parity, "collaboration", "Team templates, team state, collaboration messages, and blackboard lifecycle", "/api/resources/team-templates/scaffolds/{preset}") {
		t.Fatalf("expected team template scaffold parity entry, got %#v", decoded.Parity)
	}
}

func TestServerCapabilitiesEndpointExposesCompactClientMetadata(t *testing.T) {
	server := NewServer(newAPITestRuntime(t))
	request := httptest.NewRequest(http.MethodGet, "/api/capabilities", nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected capabilities 200, got %d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	var decoded capabilityResponse
	if err := json.NewDecoder(strings.NewReader(body)).Decode(&decoded); err != nil {
		t.Fatalf("decode capabilities response: %v", err)
	}
	if decoded.Meta.Schema != apiDiscoverySchema ||
		decoded.Meta.SchemaVersion != apiDiscoverySchemaVersion ||
		decoded.Meta.MinSupportedSchemaVersion != 1 ||
		decoded.Meta.RuntimeVersion == "" ||
		!strings.Contains(decoded.Meta.CacheKey, apiDiscoverySchema) {
		t.Fatalf("expected capabilities discovery metadata, got %#v", decoded.Meta)
	}
	if decoded.ResourceCatalog.Schema != resourceCatalogSchema ||
		decoded.ResourceCatalog.Path != "/api/resources" ||
		decoded.ResourceCatalog.CacheKey == "" {
		t.Fatalf("expected resource catalog discovery metadata, got %#v", decoded.ResourceCatalog)
	}
	if decoded.Capabilities["http_cli_parity_matrix"] != true ||
		decoded.Capabilities["client_contract_metadata"] != true ||
		decoded.Capabilities["resource_capability_metadata"] != true ||
		decoded.Capabilities["resource_catalog_endpoint"] != true ||
		decoded.Capabilities["run_action_protocol_metadata"] != true ||
		decoded.Capabilities["durable_run_sse_heartbeats"] != true ||
		decoded.Capabilities["durable_run_sse_retry_directive"] != true ||
		decoded.Capabilities["workspace_switch_guidance"] != true ||
		decoded.Capabilities["cost_control_feature_catalog"] != true ||
		decoded.Capabilities["runtime_tool_risk_policy_status"] != true ||
		decoded.Capabilities["container_tool_scaffold_hardened_defaults"] != true ||
		decoded.Capabilities["container_tool_scaffold_versioned_image_default"] != true ||
		decoded.Capabilities["tool_scaffold_default_option_metadata"] != true ||
		decoded.Capabilities["mcp_isolation_mode_catalog"] != true ||
		decoded.Capabilities["mcp_isolation_mode_recommendations"] != true {
		t.Fatalf("expected compact capability flags, got %#v", decoded.Capabilities)
	}
	if helpMCPIsolationModeByName(decoded.MCPIsolationModes, "container") == nil ||
		helpMCPIsolationModeByName(decoded.MCPIsolationModes, "windows_appcontainer") != nil {
		t.Fatalf("expected compact MCP isolation mode catalog, got %#v", decoded.MCPIsolationModes)
	}
	containerMode := helpMCPIsolationModeByName(decoded.MCPIsolationModes, "container")
	if containerMode == nil ||
		!containerMode.Recommended ||
		!configDiagnosticsStringSliceContains(containerMode.RecommendedFor, "generated_tools") ||
		!mcpIsolationModeOptionExists(containerMode.Options, "image", "string", true) {
		t.Fatalf("expected compact container isolation option metadata, got %#v", containerMode)
	}
	if helpClientContractByArea(decoded.ClientContracts, "workflow_runs") == nil ||
		helpClientContractByArea(decoded.ClientContracts, "cost_control") == nil ||
		helpClientContractByArea(decoded.ClientContracts, "configuration_apply") == nil {
		t.Fatalf("expected compact client contracts, got %#v", decoded.ClientContracts)
	}
	kitCapability := helpResourceCapabilityByKind(decoded.ResourceCapabilities, "kit")
	if kitCapability == nil ||
		!helpResourceActionExists(kitCapability.Actions, "export_bundle", "/api/resources/kits/{name}/export?format=json|yaml") {
		t.Fatalf("expected compact resource actions, got %#v", kitCapability)
	}
	if strings.Contains(body, `"commands"`) ||
		strings.Contains(body, `"parity"`) ||
		strings.Contains(body, `"coverage"`) {
		t.Fatalf("expected compact capabilities response without full help envelope, got %s", body)
	}
}

func TestServerResourceCatalogEndpointSummarizesFamilies(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	server := NewServer(runtimeRef)

	if _, err := server.saveAgentResource("researcher", agentResourceDocument{
		Name:             "Researcher",
		Description:      "Research role",
		Provider:         "primary",
		MaxIterations:    2,
		AllowedToolKinds: []string{"read", "network"},
		ToolPolicy:       "confirm",
		Mode:             "chat",
	}); err != nil {
		t.Fatalf("save agent resource: %v", err)
	}
	if _, err := server.saveProviderResource("deepseek", providerResourceDocument{
		Provider: "openai-compatible",
		BaseURL:  "https://api.deepseek.com/v1",
		APIKey:   "test-key",
		Model:    "deepseek-chat",
	}); err != nil {
		t.Fatalf("save provider resource: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/resources", nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected resources catalog 200, got %d body=%s", response.Code, response.Body.String())
	}
	var decoded resourceCatalogResponse
	if err := json.NewDecoder(strings.NewReader(response.Body.String())).Decode(&decoded); err != nil {
		t.Fatalf("decode resources catalog: %v", err)
	}
	if decoded.Meta.Schema != resourceCatalogSchema ||
		decoded.Meta.SchemaVersion != resourceCatalogSchemaVersion ||
		decoded.Meta.RuntimeVersion == "" ||
		decoded.Meta.CacheKey == "" {
		t.Fatalf("expected resource catalog metadata, got %#v", decoded.Meta)
	}
	if decoded.Counts.Families == 0 ||
		decoded.Counts.TotalResources == 0 ||
		decoded.Counts.RestartRequired == 0 ||
		decoded.Counts.WithValidation == 0 ||
		decoded.Counts.Scaffoldable == 0 ||
		!decoded.Summary.RestartRequired ||
		!configDiagnosticsStringSliceContains(decoded.Summary.ApplyStates, "restart_required") {
		t.Fatalf("expected populated resource catalog counts, got counts=%#v summary=%#v", decoded.Counts, decoded.Summary)
	}
	agentFamily := resourceFamilySummaryByKind(decoded.Families, "agent")
	if agentFamily == nil ||
		agentFamily.Count == 0 ||
		agentFamily.CollectionPath != "/api/resources/agents" ||
		agentFamily.ApplyStateOnSave != "restart_required" ||
		!agentFamily.RestartRequiredOnSave ||
		agentFamily.DiagnosticsPath != "/api/config/diagnostics" {
		t.Fatalf("expected agent resource family summary, got %#v", agentFamily)
	}
	skillFamily := resourceFamilySummaryByKind(decoded.Families, "skill")
	if skillFamily == nil ||
		!skillFamily.HotReloadSupported ||
		skillFamily.RestartRequiredOnSave ||
		skillFamily.ApplyStateOnSave != "hot_reload_when_supported" {
		t.Fatalf("expected hot-reload skill family summary, got %#v", skillFamily)
	}
	kitFamily := resourceFamilySummaryByKind(decoded.Families, "kit")
	if kitFamily == nil ||
		!kitFamily.CanScaffold ||
		!helpResourceActionExists(kitFamily.Actions, "export_bundle", "/api/resources/kits/{name}/export?format=json|yaml") {
		t.Fatalf("expected kit family actions, got %#v", kitFamily)
	}

	filterRequest := httptest.NewRequest(http.MethodGet, "/api/resources?kind=agent,provider", nil)
	filterResponse := httptest.NewRecorder()
	server.ServeHTTP(filterResponse, filterRequest)
	if filterResponse.Code != http.StatusOK {
		t.Fatalf("expected filtered resources catalog 200, got %d body=%s", filterResponse.Code, filterResponse.Body.String())
	}
	var filtered resourceCatalogResponse
	if err := json.NewDecoder(strings.NewReader(filterResponse.Body.String())).Decode(&filtered); err != nil {
		t.Fatalf("decode filtered resources catalog: %v", err)
	}
	if filtered.Counts.Families != 2 ||
		resourceFamilySummaryByKind(filtered.Families, "agent") == nil ||
		resourceFamilySummaryByKind(filtered.Families, "provider") == nil ||
		resourceFamilySummaryByKind(filtered.Families, "skill") != nil {
		t.Fatalf("expected filtered resource families, got counts=%#v families=%#v", filtered.Counts, filtered.Families)
	}

	searchRequest := httptest.NewRequest(http.MethodGet, "/api/resources?q=workflow", nil)
	searchResponse := httptest.NewRecorder()
	server.ServeHTTP(searchResponse, searchRequest)
	if searchResponse.Code != http.StatusOK {
		t.Fatalf("expected searched resources catalog 200, got %d body=%s", searchResponse.Code, searchResponse.Body.String())
	}
	var searched resourceCatalogResponse
	if err := json.NewDecoder(strings.NewReader(searchResponse.Body.String())).Decode(&searched); err != nil {
		t.Fatalf("decode searched resources catalog: %v", err)
	}
	if searched.Counts.Families == 0 ||
		resourceFamilySummaryByKind(searched.Families, "agent") != nil ||
		resourceFamilySummaryByKind(searched.Families, "workflow") == nil {
		t.Fatalf("expected searched resource families, got counts=%#v families=%#v", searched.Counts, searched.Families)
	}
}

func helpClientContractByArea(items []helpClientContract, area string) *helpClientContract {
	for i := range items {
		if items[i].Area == area {
			return &items[i]
		}
	}
	return nil
}

func helpResourceCapabilityByKind(items []helpResourceCapability, kind string) *helpResourceCapability {
	for i := range items {
		if items[i].Kind == kind {
			return &items[i]
		}
	}
	return nil
}

func helpMCPIsolationModeByName(items []mcpIsolationMode, name string) *mcpIsolationMode {
	for i := range items {
		if items[i].Name == name {
			return &items[i]
		}
	}
	return nil
}

func toolScaffoldPresetByName(items []toolScaffoldPreset, name string) *toolScaffoldPreset {
	for i := range items {
		if items[i].Name == name {
			return &items[i]
		}
	}
	return nil
}

func mcpServerSummaryByName(items []mcpServerSummary, name string) *mcpServerSummary {
	for i := range items {
		if items[i].Name == name {
			return &items[i]
		}
	}
	return nil
}

func mcpIsolationProfileByName(items []mcpIsolationProfile, name string) *mcpIsolationProfile {
	for i := range items {
		if items[i].Name == name {
			return &items[i]
		}
	}
	return nil
}

func mcpIsolationModeOptionExists(items []mcpIsolationModeOption, name, typ string, required bool) bool {
	for _, item := range items {
		if item.Name == name && item.Type == typ && item.Required == required && item.Description != "" {
			return true
		}
	}
	return false
}

func resourceFamilySummaryByKind(items []resourceFamilySummary, kind string) *resourceFamilySummary {
	for i := range items {
		if items[i].Kind == kind {
			return &items[i]
		}
	}
	return nil
}

func helpResourceActionExists(items []helpResourceAction, name, path string) bool {
	for _, item := range items {
		if item.Name == name && item.Path == path {
			return true
		}
	}
	return false
}

func helpParityContains(items []helpParityItem, category, capability, path string) bool {
	for _, item := range items {
		if item.Category != category || item.Capability != capability {
			continue
		}
		for _, endpoint := range item.HTTP {
			if endpoint.Path == path || endpoint.StreamPath == path {
				return true
			}
		}
	}
	return false
}

func runFacetContains(items []runCollectionFacet, value string, count int) bool {
	for _, item := range items {
		if item.Value == value && item.Count == count {
			return true
		}
	}
	return false
}

func runCollectionItemByID(items []runCollectionItem, id string) *runCollectionItem {
	for i := range items {
		if items[i].ID == id {
			return &items[i]
		}
	}
	return nil
}

func TestServerRuntimeAgentSelection(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	server := NewServer(runtimeRef)

	request := httptest.NewRequest(http.MethodPost, "/api/runtime/agent", strings.NewReader(`{"agent":"auditor"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected agent selection 200, got %d body=%s", response.Code, response.Body.String())
	}
	if runtimeRef.ActiveAgent() != "auditor" || runtimeRef.Mode() != "audit" {
		t.Fatalf("expected auditor active, got agent=%s mode=%s", runtimeRef.ActiveAgent(), runtimeRef.Mode())
	}
	if !strings.Contains(response.Body.String(), `"active_agent":"auditor"`) {
		t.Fatalf("expected runtime status response, got %s", response.Body.String())
	}
}

func TestServerRuntimeTraceToggle(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	server := NewServer(runtimeRef)

	request := httptest.NewRequest(http.MethodPost, "/api/runtime/trace", strings.NewReader(`{"trace":true}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected trace toggle 200, got %d body=%s", response.Code, response.Body.String())
	}
	if !runtimeRef.TraceEnabled() || !strings.Contains(response.Body.String(), `"trace":true`) {
		t.Fatalf("expected trace enabled runtime status, trace=%t body=%s", runtimeRef.TraceEnabled(), response.Body.String())
	}

	disableRequest := httptest.NewRequest(http.MethodPost, "/api/runtime/trace", strings.NewReader(`{"enabled":false}`))
	disableRequest.Header.Set("Content-Type", "application/json")
	disableResponse := httptest.NewRecorder()
	server.ServeHTTP(disableResponse, disableRequest)
	if disableResponse.Code != http.StatusOK || runtimeRef.TraceEnabled() {
		t.Fatalf("expected trace disabled, code=%d trace=%t body=%s", disableResponse.Code, runtimeRef.TraceEnabled(), disableResponse.Body.String())
	}
}

func TestServerSkillResourceSaveValidatesAndWritesSkill(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	server := NewServer(runtimeRef)

	validateRequest := httptest.NewRequest(http.MethodPost, "/api/resources/skills/custom-review/validate", strings.NewReader(`{
		"description":"Review custom project changes.",
		"version":"1.0.0",
		"author":"GoFlow Studio",
		"mode":"audit",
		"preferred_agent":"auditor",
		"allowed_tool_kinds":["read"],
		"output_kind":"findings",
		"activation":{"keywords":["custom-review","review"]},
		"tools":[{"name":"file_tools/read_file","required":true}],
		"instructions":"## Role\n\nReview the requested files and report findings."
	}`))
	validateRequest.Header.Set("Content-Type", "application/json")
	validateResponse := httptest.NewRecorder()
	server.ServeHTTP(validateResponse, validateRequest)
	if validateResponse.Code != http.StatusOK || !strings.Contains(validateResponse.Body.String(), `"valid":true`) || !strings.Contains(validateResponse.Body.String(), `"resource":"skill"`) || !strings.Contains(validateResponse.Body.String(), `"name":"custom-review"`) || !strings.Contains(validateResponse.Body.String(), `"normalized"`) || !strings.Contains(validateResponse.Body.String(), `"instructions"`) {
		t.Fatalf("expected valid skill dry-run response, got %d body=%s", validateResponse.Code, validateResponse.Body.String())
	}
	if _, err := os.Stat(filepath.Join(runtimeRef.RuntimeHome(), "skills", "custom-review", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatalf("expected skill validate not to write SKILL.md, stat err=%v", err)
	}

	invalidValidateRequest := httptest.NewRequest(http.MethodPost, "/api/resources/skills/validate", strings.NewReader(`{
		"name":"bad-skill",
		"description":"Bad skill",
		"format":"goflow",
		"version":"1.0.0",
		"author":"GoFlow Studio",
		"mode":"audit",
		"preferred_agent":"auditor",
		"allowed_tool_kinds":["read"],
		"output_kind":"findings",
		"activation":{"keywords":["bad-skill"]},
		"tools":[{"name":"","required":true}],
		"instructions":"Review."
	}`))
	invalidValidateRequest.Header.Set("Content-Type", "application/json")
	invalidValidateResponse := httptest.NewRecorder()
	server.ServeHTTP(invalidValidateResponse, invalidValidateRequest)
	if invalidValidateResponse.Code != http.StatusOK || !strings.Contains(invalidValidateResponse.Body.String(), `"valid":false`) || !strings.Contains(invalidValidateResponse.Body.String(), `"field":"tools"`) || !strings.Contains(invalidValidateResponse.Body.String(), `"code":"invalid_tools"`) || !strings.Contains(invalidValidateResponse.Body.String(), `"recommendation"`) || !strings.Contains(invalidValidateResponse.Body.String(), "empty name") {
		t.Fatalf("expected invalid skill dry-run response, got %d body=%s", invalidValidateResponse.Code, invalidValidateResponse.Body.String())
	}

	invalidSaveRequest := httptest.NewRequest(http.MethodPut, "/api/resources/skills/bad-skill", strings.NewReader(`{
		"name":"bad-skill",
		"description":"Bad skill",
		"format":"goflow",
		"version":"1.0.0",
		"author":"GoFlow Studio",
		"mode":"audit",
		"preferred_agent":"auditor",
		"allowed_tool_kinds":["read"],
		"output_kind":"findings",
		"activation":{"keywords":["bad-skill"]},
		"tools":[{"name":"","required":true}],
		"instructions":"Review."
	}`))
	invalidSaveRequest.Header.Set("Content-Type", "application/json")
	invalidSaveResponse := httptest.NewRecorder()
	server.ServeHTTP(invalidSaveResponse, invalidSaveRequest)
	if invalidSaveResponse.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid skill save 400, got %d body=%s", invalidSaveResponse.Code, invalidSaveResponse.Body.String())
	}
	var invalidSave resourceValidationResult
	if err := json.NewDecoder(invalidSaveResponse.Body).Decode(&invalidSave); err != nil {
		t.Fatalf("decode invalid skill save response: %v", err)
	}
	if invalidSave.Valid || invalidSave.Resource != "skill" || invalidSave.Name != "bad-skill" || len(invalidSave.Issues) != 1 || invalidSave.Issues[0].Field != "tools" || invalidSave.Issues[0].Code != "invalid_tools" {
		t.Fatalf("unexpected invalid skill save response: %#v", invalidSave)
	}

	body := strings.NewReader(`{
		"name":"custom-review",
		"description":"Review custom project changes.",
		"version":"1.0.0",
		"author":"GoFlow Studio",
		"mode":"audit",
		"preferred_agent":"auditor",
		"allowed_tool_kinds":["read"],
		"output_kind":"findings",
		"activation":{"keywords":["custom-review","review"]},
		"tools":[{"name":"file_tools/read_file","required":true}],
		"instructions":"## Role\n\nReview the requested files and report findings."
	}`)
	request := httptest.NewRequest(http.MethodPut, "/api/resources/skills/custom-review", body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected skill save 200, got %d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"name":"custom-review"`) {
		t.Fatalf("expected saved skill response, got %s", response.Body.String())
	}
	path := filepath.Join(runtimeRef.RuntimeHome(), "skills", "custom-review", "SKILL.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected skill file written: %v", err)
	}
	if !strings.Contains(string(data), "Review the requested files") || strings.Contains(string(data), "instructions:") {
		t.Fatalf("unexpected skill file content:\n%s", string(data))
	}
}

func TestServerSkillResourcePreservesDeclaredScripts(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	server := NewServer(runtimeRef)

	validateRequest := httptest.NewRequest(http.MethodPost, "/api/resources/skills/web-review/validate", strings.NewReader(`{
		"name": "web-review",
		"description": "Review web assets.",
		"version": "1.0.0",
		"author": "GoFlow",
		"activation": {"keywords": ["web review"]},
		"scripts": [
			{
				"name": "collect-assets",
				"description": "Collect page assets.",
				"path": "scripts/collect.py",
				"runtime": "python",
				"output": "json",
				"timeout": "30s",
				"isolation": "container",
				"workspace_mount": "ro",
				"network": "disabled",
				"approval": "required",
				"args_schema": {
					"type": "object",
					"additionalProperties": false,
					"properties": {
						"url": {"type": "string"}
					},
					"required": ["url"]
				}
			}
		],
		"instructions": "## Workflow\n\nUse the declared helper."
	}`))
	validateRequest.Header.Set("Content-Type", "application/json")
	validateRecorder := httptest.NewRecorder()
	server.ServeHTTP(validateRecorder, validateRequest)
	if validateRecorder.Code != http.StatusOK {
		t.Fatalf("validate returned %d: %s", validateRecorder.Code, validateRecorder.Body.String())
	}
	var validateEnvelope resourceValidationResult
	if err := json.Unmarshal(validateRecorder.Body.Bytes(), &validateEnvelope); err != nil {
		t.Fatalf("decode validate response: %v", err)
	}
	normalized, ok := validateEnvelope.Normalized.(map[string]any)
	if !ok {
		t.Fatalf("expected normalized map, got %#v", validateEnvelope.Normalized)
	}
	scripts, ok := normalized["scripts"].([]any)
	if !ok || len(scripts) != 1 {
		t.Fatalf("expected one script in normalized response, got %#v", normalized["scripts"])
	}

	putRequest := httptest.NewRequest(http.MethodPut, "/api/resources/skills/web-review", strings.NewReader(`{
		"name": "web-review",
		"description": "Review web assets.",
		"version": "1.0.0",
		"author": "GoFlow",
		"activation": {"keywords": ["web review"]},
		"scripts": [
			{
				"name": "collect-assets",
				"description": "Collect page assets.",
				"path": "scripts/collect.py",
				"runtime": "python",
				"output": "json",
				"timeout": "30s",
				"isolation": "container",
				"workspace_mount": "ro",
				"network": "disabled",
				"approval": "required",
				"args_schema": {
					"type": "object",
					"additionalProperties": false,
					"properties": {
						"url": {"type": "string"}
					},
					"required": ["url"]
				}
			}
		],
		"instructions": "## Workflow\n\nUse the declared helper."
	}`))
	putRequest.Header.Set("Content-Type", "application/json")
	putRecorder := httptest.NewRecorder()
	server.ServeHTTP(putRecorder, putRequest)
	if putRecorder.Code != http.StatusOK {
		t.Fatalf("save returned %d: %s", putRecorder.Code, putRecorder.Body.String())
	}
	var saved skillResourceDocument
	if err := json.Unmarshal(putRecorder.Body.Bytes(), &saved); err != nil {
		t.Fatalf("decode save response: %v", err)
	}
	if len(saved.Scripts) != 1 || saved.Scripts[0].Name != "collect-assets" {
		t.Fatalf("unexpected saved scripts: %#v", saved.Scripts)
	}
	if saved.Scripts[0].Path != "scripts/collect.py" || saved.Scripts[0].Approval != "required" {
		t.Fatalf("unexpected saved script metadata: %#v", saved.Scripts[0])
	}
}

func TestServerSkillFileResourceEndpointsManageBundledFiles(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	server := NewServer(runtimeRef)
	saveSkillForResourceTest(t, server)

	writeRequest := httptest.NewRequest(http.MethodPut, "/api/resources/skills/custom-review/files/references/policy.md", strings.NewReader(`{"content":"# Review Policy\n\nCheck high-risk changes."}`))
	writeRequest.Header.Set("Content-Type", "application/json")
	writeResponse := httptest.NewRecorder()
	server.ServeHTTP(writeResponse, writeRequest)
	if writeResponse.Code != http.StatusOK {
		t.Fatalf("expected resource write 200, got %d body=%s", writeResponse.Code, writeResponse.Body.String())
	}
	if !strings.Contains(writeResponse.Body.String(), `"path":"references/policy.md"`) || !strings.Contains(writeResponse.Body.String(), `"kind":"references"`) || !strings.Contains(writeResponse.Body.String(), "Review Policy") {
		t.Fatalf("unexpected resource write response: %s", writeResponse.Body.String())
	}

	listRequest := httptest.NewRequest(http.MethodGet, "/api/resources/skills/custom-review/files", nil)
	listResponse := httptest.NewRecorder()
	server.ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("expected resource list 200, got %d body=%s", listResponse.Code, listResponse.Body.String())
	}
	if !strings.Contains(listResponse.Body.String(), `"path":"references/policy.md"`) {
		t.Fatalf("expected resource in list, got %s", listResponse.Body.String())
	}

	readRequest := httptest.NewRequest(http.MethodGet, "/api/resources/skills/custom-review/files/references/policy.md", nil)
	readResponse := httptest.NewRecorder()
	server.ServeHTTP(readResponse, readRequest)
	if readResponse.Code != http.StatusOK {
		t.Fatalf("expected resource read 200, got %d body=%s", readResponse.Code, readResponse.Body.String())
	}
	if !strings.Contains(readResponse.Body.String(), "Check high-risk changes") {
		t.Fatalf("expected resource content, got %s", readResponse.Body.String())
	}
	path := filepath.Join(runtimeRef.RuntimeHome(), "skills", "custom-review", "references", "policy.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected resource file written: %v", err)
	}
	if !strings.Contains(string(data), "Check high-risk changes") {
		t.Fatalf("unexpected resource file content: %s", string(data))
	}

	deleteRequest := httptest.NewRequest(http.MethodDelete, "/api/resources/skills/custom-review/files/references/policy.md", nil)
	deleteResponse := httptest.NewRecorder()
	server.ServeHTTP(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("expected resource delete 204, got %d body=%s", deleteResponse.Code, deleteResponse.Body.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected resource file deleted, got %v", err)
	}
}

func TestServerSkillFileResourceRejectsUnsafePaths(t *testing.T) {
	server := NewServer(newAPITestRuntime(t))
	saveSkillForResourceTest(t, server)

	cases := []string{
		"/api/resources/skills/custom-review/files/SKILL.md",
		"/api/resources/skills/custom-review/files/notes.md",
		"/api/resources/skills/custom-review/files/references%5Cpolicy.md",
		"/api/resources/skills/custom-review/files/references/%2e%2e/outside.md",
	}
	for _, target := range cases {
		request := httptest.NewRequest(http.MethodPut, target, strings.NewReader(`{"content":"unsafe"}`))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("expected unsafe path %s to be rejected with 400, got %d body=%s", target, response.Code, response.Body.String())
		}
	}
}

func saveSkillForResourceTest(t *testing.T, server *Server) {
	t.Helper()
	body := strings.NewReader(`{
		"name":"custom-review",
		"description":"Review custom project changes.",
		"version":"1.0.0",
		"author":"GoFlow Studio",
		"mode":"audit",
		"preferred_agent":"auditor",
		"allowed_tool_kinds":["read"],
		"output_kind":"findings",
		"activation":{"keywords":["custom-review","review"]},
		"tools":[{"name":"file_tools/read_file","required":true}],
		"instructions":"## Role\n\nReview the requested files and report findings."
	}`)
	request := httptest.NewRequest(http.MethodPut, "/api/resources/skills/custom-review", body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected skill save 200, got %d body=%s", response.Code, response.Body.String())
	}
}

func TestServerAgentResourceSaveWritesConfigSnippet(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	server := NewServer(runtimeRef)
	body := strings.NewReader(`{
		"id":"researcher",
		"name":"Researcher",
		"description":"Focused read and network research agent.",
		"system_prompt":"Use concise evidence.",
		"provider":"primary",
		"model":"test-model",
		"temperature":0.1,
		"max_tokens":1024,
		"max_iterations":5,
		"allowed_tool_kinds":["read","network"],
		"allowed_tools":["web_tools/web_search"],
		"tool_policy":"confirm",
		"mode":"audit"
	}`)
	request := httptest.NewRequest(http.MethodPut, "/api/resources/agents/researcher", body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected agent save 200, got %d body=%s", response.Code, response.Body.String())
	}
	path := filepath.Join(runtimeRef.RuntimeHome(), "configs", "agents", "researcher.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected agent snippet written: %v", err)
	}
	if !strings.Contains(string(data), "researcher:") || !strings.Contains(string(data), "allowed_tools:") || !strings.Contains(response.Body.String(), `"path"`) || !strings.Contains(response.Body.String(), `"restart_required":true`) || !strings.Contains(response.Body.String(), `"apply_state":"restart_required"`) {
		t.Fatalf("unexpected agent config response=%s file=\n%s", response.Body.String(), string(data))
	}
	if strings.Contains(string(data), "restart_required") || strings.Contains(string(data), "apply_state") {
		t.Fatalf("expected restart hints to stay out of agent yaml, got\n%s", string(data))
	}

	detailRequest := httptest.NewRequest(http.MethodGet, "/api/resources/agents/researcher", nil)
	detailResponse := httptest.NewRecorder()
	server.ServeHTTP(detailResponse, detailRequest)
	if detailResponse.Code != http.StatusOK || !strings.Contains(detailResponse.Body.String(), `"restart_required":true`) || !strings.Contains(detailResponse.Body.String(), `"apply_state":"restart_required"`) {
		t.Fatalf("expected saved agent detail to keep restart hint, got %d body=%s", detailResponse.Code, detailResponse.Body.String())
	}
	listRequest := httptest.NewRequest(http.MethodGet, "/api/resources/agents", nil)
	listResponse := httptest.NewRecorder()
	server.ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK || !strings.Contains(listResponse.Body.String(), `"id":"researcher"`) || !strings.Contains(listResponse.Body.String(), `"restart_required":true`) {
		t.Fatalf("expected saved agent list item to keep restart hint, got %d body=%s", listResponse.Code, listResponse.Body.String())
	}
}

func TestServerAgentProviderToolResourceValidateEndpoints(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	server := NewServer(runtimeRef)

	agentRequest := httptest.NewRequest(http.MethodPost, "/api/resources/agents/researcher/validate", strings.NewReader(`{
		"name":"Researcher",
		"provider":"primary",
		"mode":"audit",
		"tool_policy":"confirm",
		"allowed_tool_kinds":["read","network"],
		"max_iterations":5
	}`))
	agentRequest.Header.Set("Content-Type", "application/json")
	agentResponse := httptest.NewRecorder()
	server.ServeHTTP(agentResponse, agentRequest)
	if agentResponse.Code != http.StatusOK || !strings.Contains(agentResponse.Body.String(), `"valid":true`) || !strings.Contains(agentResponse.Body.String(), `"resource":"agent"`) || !strings.Contains(agentResponse.Body.String(), `"name":"researcher"`) || !strings.Contains(agentResponse.Body.String(), `"normalized"`) || !strings.Contains(agentResponse.Body.String(), `"restart_required":true`) {
		t.Fatalf("expected valid agent dry-run response, got %d body=%s", agentResponse.Code, agentResponse.Body.String())
	}
	if _, err := os.Stat(filepath.Join(runtimeRef.RuntimeHome(), "configs", "agents", "researcher.yaml")); !os.IsNotExist(err) {
		t.Fatalf("expected agent validate not to write module, stat err=%v", err)
	}

	invalidAgentRequest := httptest.NewRequest(http.MethodPost, "/api/resources/agents/validate", strings.NewReader(`{
		"id":"bad-agent",
		"provider":"missing-provider",
		"mode":"audit",
		"tool_policy":"confirm",
		"allowed_tool_kinds":["read"],
		"max_iterations":5
	}`))
	invalidAgentRequest.Header.Set("Content-Type", "application/json")
	invalidAgentResponse := httptest.NewRecorder()
	server.ServeHTTP(invalidAgentResponse, invalidAgentRequest)
	if invalidAgentResponse.Code != http.StatusOK || !strings.Contains(invalidAgentResponse.Body.String(), `"valid":false`) || !strings.Contains(invalidAgentResponse.Body.String(), `"field":"provider"`) || !strings.Contains(invalidAgentResponse.Body.String(), `"code":"unknown_provider_reference"`) || !strings.Contains(invalidAgentResponse.Body.String(), `"recommendation"`) || !strings.Contains(invalidAgentResponse.Body.String(), "unknown provider") {
		t.Fatalf("expected structured invalid agent validation, got %d body=%s", invalidAgentResponse.Code, invalidAgentResponse.Body.String())
	}

	providerRequest := httptest.NewRequest(http.MethodPost, "/api/resources/providers/deepseek/validate", strings.NewReader(`{
		"provider":"openai-compatible",
		"base_url":"https://api.deepseek.com/v1",
		"api_key":"${DEEPSEEK_API_KEY}",
		"model":"deepseek-chat",
		"fallback_provider":"primary",
		"timeout":"45s"
	}`))
	providerRequest.Header.Set("Content-Type", "application/json")
	providerResponse := httptest.NewRecorder()
	server.ServeHTTP(providerResponse, providerRequest)
	if providerResponse.Code != http.StatusOK || !strings.Contains(providerResponse.Body.String(), `"valid":true`) || !strings.Contains(providerResponse.Body.String(), `"resource":"provider"`) || !strings.Contains(providerResponse.Body.String(), `"api_key_set":true`) || !strings.Contains(providerResponse.Body.String(), `"restart_required":true`) {
		t.Fatalf("expected valid provider dry-run response, got %d body=%s", providerResponse.Code, providerResponse.Body.String())
	}
	if _, err := os.Stat(filepath.Join(runtimeRef.RuntimeHome(), "configs", "providers", "deepseek.yaml")); !os.IsNotExist(err) {
		t.Fatalf("expected provider validate not to write module, stat err=%v", err)
	}

	invalidProviderRequest := httptest.NewRequest(http.MethodPost, "/api/resources/providers/validate", strings.NewReader(`{
		"id":"bad-provider",
		"provider":"openai-compatible",
		"base_url":"https://api.example.com/v1",
		"model":"demo",
		"fallback_provider":"missing-provider"
	}`))
	invalidProviderRequest.Header.Set("Content-Type", "application/json")
	invalidProviderResponse := httptest.NewRecorder()
	server.ServeHTTP(invalidProviderResponse, invalidProviderRequest)
	if invalidProviderResponse.Code != http.StatusOK || !strings.Contains(invalidProviderResponse.Body.String(), `"valid":false`) || !strings.Contains(invalidProviderResponse.Body.String(), `"field":"fallback_provider"`) || !strings.Contains(invalidProviderResponse.Body.String(), `"code":"unknown_provider_reference"`) || !strings.Contains(invalidProviderResponse.Body.String(), `"recommendation"`) || !strings.Contains(invalidProviderResponse.Body.String(), "unknown fallback_provider") {
		t.Fatalf("expected structured invalid provider validation, got %d body=%s", invalidProviderResponse.Code, invalidProviderResponse.Body.String())
	}

	toolRequest := httptest.NewRequest(http.MethodPost, "/api/resources/tools/notes-helper/validate", strings.NewReader(`{
		"language":"python",
		"command":"python",
		"args":["./mcp_servers/notes-helper.py"],
		"enabled":true,
		"timeout":"30s",
		"workdir":".",
		"allowed_commands":["python"],
		"code":"#!/usr/bin/env python3\nprint('ok')\n"
	}`))
	toolRequest.Header.Set("Content-Type", "application/json")
	toolResponse := httptest.NewRecorder()
	server.ServeHTTP(toolResponse, toolRequest)
	if toolResponse.Code != http.StatusOK || !strings.Contains(toolResponse.Body.String(), `"valid":true`) || !strings.Contains(toolResponse.Body.String(), `"resource":"tool"`) || !strings.Contains(toolResponse.Body.String(), `"name":"notes-helper"`) || !strings.Contains(toolResponse.Body.String(), `"restart_required":true`) || !strings.Contains(toolResponse.Body.String(), `"risk"`) {
		t.Fatalf("expected valid tool dry-run response, got %d body=%s", toolResponse.Code, toolResponse.Body.String())
	}
	if _, err := os.Stat(filepath.Join(runtimeRef.RuntimeHome(), "mcp_servers", "notes-helper.py")); !os.IsNotExist(err) {
		t.Fatalf("expected tool validate not to write code, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(runtimeRef.RuntimeHome(), "configs", "mcp_servers", "notes-helper.yaml")); !os.IsNotExist(err) {
		t.Fatalf("expected tool validate not to write config, stat err=%v", err)
	}

	invalidToolRequest := httptest.NewRequest(http.MethodPost, "/api/resources/tools/validate", strings.NewReader(`{
		"name":"bad-tool",
		"command":"python"
	}`))
	invalidToolRequest.Header.Set("Content-Type", "application/json")
	invalidToolResponse := httptest.NewRecorder()
	server.ServeHTTP(invalidToolResponse, invalidToolRequest)
	if invalidToolResponse.Code != http.StatusOK || !strings.Contains(invalidToolResponse.Body.String(), `"valid":false`) || !strings.Contains(invalidToolResponse.Body.String(), `"field":"code"`) || !strings.Contains(invalidToolResponse.Body.String(), `"code":"required_code"`) || !strings.Contains(invalidToolResponse.Body.String(), `"recommendation"`) || !strings.Contains(invalidToolResponse.Body.String(), "tool code is required") {
		t.Fatalf("expected structured invalid tool validation, got %d body=%s", invalidToolResponse.Code, invalidToolResponse.Body.String())
	}

	appContainerToolRequest := httptest.NewRequest(http.MethodPost, "/api/resources/tools/appcontainer-helper/validate", strings.NewReader(`{
		"language":"python",
		"command":"python",
		"args":["./mcp_servers/appcontainer-helper.py"],
		"enabled":true,
		"timeout":"30s",
		"workdir":".",
		"allowed_commands":["python"],
		"isolation":"windows_appcontainer",
		"code":"#!/usr/bin/env python3\nprint('ok')\n"
	}`))
	appContainerToolRequest.Header.Set("Content-Type", "application/json")
	appContainerToolResponse := httptest.NewRecorder()
	server.ServeHTTP(appContainerToolResponse, appContainerToolRequest)
	if appContainerToolResponse.Code != http.StatusOK ||
		!strings.Contains(appContainerToolResponse.Body.String(), `"valid":false`) ||
		!strings.Contains(appContainerToolResponse.Body.String(), `"field":"isolation"`) ||
		!strings.Contains(appContainerToolResponse.Body.String(), `"code":"mcp_windows_appcontainer_unimplemented"`) ||
		!strings.Contains(appContainerToolResponse.Body.String(), "use isolation: container") {
		t.Fatalf("expected tool dry-run to reject unimplemented AppContainer isolation, got %d body=%s", appContainerToolResponse.Code, appContainerToolResponse.Body.String())
	}
	if _, err := os.Stat(filepath.Join(runtimeRef.RuntimeHome(), "mcp_servers", "appcontainer-helper.py")); !os.IsNotExist(err) {
		t.Fatalf("expected invalid tool validate not to write code, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(runtimeRef.RuntimeHome(), "configs", "mcp_servers", "appcontainer-helper.yaml")); !os.IsNotExist(err) {
		t.Fatalf("expected invalid tool validate not to write config, stat err=%v", err)
	}

	missingAllowlistToolRequest := httptest.NewRequest(http.MethodPost, "/api/resources/tools/no-allowlist/validate", strings.NewReader(`{
		"language":"python",
		"command":"python",
		"args":["./mcp_servers/no-allowlist.py"],
		"enabled":true,
		"timeout":"30s",
		"workdir":".",
		"code":"#!/usr/bin/env python3\nprint('ok')\n"
	}`))
	missingAllowlistToolRequest.Header.Set("Content-Type", "application/json")
	missingAllowlistToolResponse := httptest.NewRecorder()
	server.ServeHTTP(missingAllowlistToolResponse, missingAllowlistToolRequest)
	if missingAllowlistToolResponse.Code != http.StatusOK ||
		!strings.Contains(missingAllowlistToolResponse.Body.String(), `"valid":true`) {
		t.Fatalf("expected default allowlist to be applied in tool validate, got %d body=%s", missingAllowlistToolResponse.Code, missingAllowlistToolResponse.Body.String())
	}
}

func TestServerProviderResourceTestEndpoint(t *testing.T) {
	var captured map[string]any
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected provider path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("unexpected authorization header: %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode provider request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"finish_reason": "stop",
				"message": map[string]any{
					"role":    "assistant",
					"content": "pong",
				},
			}},
		})
	}))
	defer modelServer.Close()

	runtimeRef := newAPITestRuntime(t)
	server := NewServer(runtimeRef)
	body := fmt.Sprintf(`{
		"provider":"openai-compatible",
		"base_url":%q,
		"api_key":"test-key",
		"model":"test-model",
		"timeout":"5s"
	}`, modelServer.URL)
	request := httptest.NewRequest(http.MethodPost, "/api/resources/providers/mock/test", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", response.Code, response.Body.String())
	}
	var result providerTestResult
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !result.Valid || result.Status != "ok" || result.Name != "mock" || result.LatencyMS < 0 {
		t.Fatalf("unexpected test result: %#v", result)
	}
	if captured["model"] != "test-model" {
		t.Fatalf("expected model test-model, got %#v", captured["model"])
	}
	if _, err := os.Stat(filepath.Join(runtimeRef.RuntimeHome(), "configs", "providers", "mock.yaml")); !os.IsNotExist(err) {
		t.Fatalf("expected provider test not to write module, stat err=%v", err)
	}

	invalidRequest := httptest.NewRequest(http.MethodPost, "/api/resources/providers/mock/test", strings.NewReader(`{
		"provider":"openai-compatible",
		"base_url":"https://api.example.com/v1",
		"model":"test-model"
	}`))
	invalidRequest.Header.Set("Content-Type", "application/json")
	invalidResponse := httptest.NewRecorder()
	server.ServeHTTP(invalidResponse, invalidRequest)
	if invalidResponse.Code != http.StatusOK {
		t.Fatalf("expected invalid test status 200, got %d body=%s", invalidResponse.Code, invalidResponse.Body.String())
	}
	var invalid providerTestResult
	if err := json.NewDecoder(invalidResponse.Body).Decode(&invalid); err != nil {
		t.Fatalf("decode invalid response: %v", err)
	}
	if invalid.Valid || invalid.Status != "error" || len(invalid.Issues) == 0 || invalid.Issues[0].Field != "api_key" {
		t.Fatalf("expected structured api_key failure, got %#v", invalid)
	}
}

func TestServerAgentResourceGetMarksMatchingModuleActive(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	server := NewServer(runtimeRef)
	dir := filepath.Join(runtimeRef.RuntimeHome(), "configs", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create agent module dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "chat.yaml"), []byte(`chat:
  name: Chat
  provider: primary
  mode: chat
  model: test-model
  max_iterations: 2
  allowed_tool_kinds: [read, write, exec]
  tool_policy: confirm
`), 0o644); err != nil {
		t.Fatalf("write matching agent module: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/resources/agents/chat", nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected agent detail 200, got %d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), `"restart_required":true`) || !strings.Contains(response.Body.String(), `"apply_state":"active"`) {
		t.Fatalf("expected matching saved agent module to be marked active, got %s", response.Body.String())
	}
}

func TestServerAgentResourceSaveRejectsUnknownProvider(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	server := NewServer(runtimeRef)
	body := strings.NewReader(`{
		"id":"researcher",
		"name":"Researcher",
		"provider":"missing-provider",
		"mode":"audit",
		"tool_policy":"confirm",
		"allowed_tool_kinds":["read"],
		"max_iterations":5
	}`)
	request := httptest.NewRequest(http.MethodPut, "/api/resources/agents/researcher", body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected agent save 400, got %d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("expected structured JSON error, content-type=%s body=%s", response.Header().Get("Content-Type"), response.Body.String())
	}
	var result resourceValidationResult
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatalf("decode agent validation error: %v body=%s", err, response.Body.String())
	}
	if result.Valid || result.Resource != "agent" || result.Name != "researcher" || len(result.Issues) != 1 {
		t.Fatalf("unexpected agent validation result: %#v", result)
	}
	if result.Issues[0].Field != "provider" || result.Issues[0].Code != "unknown_provider_reference" || !strings.Contains(result.Issues[0].Message, "unknown provider") || result.Issues[0].Recommendation == "" {
		t.Fatalf("unexpected agent validation issue: %#v", result.Issues[0])
	}
}

func TestServerAgentResourceDeleteRemovesConfigModule(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	server := NewServer(runtimeRef)
	body := strings.NewReader(`{
		"id":"researcher",
		"name":"Researcher",
		"provider":"primary",
		"mode":"audit",
		"tool_policy":"confirm",
		"allowed_tool_kinds":["read"],
		"max_iterations":5
	}`)
	saveRequest := httptest.NewRequest(http.MethodPut, "/api/resources/agents/researcher", body)
	saveRequest.Header.Set("Content-Type", "application/json")
	saveResponse := httptest.NewRecorder()
	server.ServeHTTP(saveResponse, saveRequest)
	if saveResponse.Code != http.StatusOK {
		t.Fatalf("expected agent save 200, got %d body=%s", saveResponse.Code, saveResponse.Body.String())
	}

	deleteRequest := httptest.NewRequest(http.MethodDelete, "/api/resources/agents/researcher", nil)
	deleteResponse := httptest.NewRecorder()
	server.ServeHTTP(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("expected agent delete 204, got %d body=%s", deleteResponse.Code, deleteResponse.Body.String())
	}
	path := filepath.Join(runtimeRef.RuntimeHome(), "configs", "agents", "researcher.yaml")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected agent module deleted, got %v", err)
	}
}

func TestServerAgentResourceDeleteRejectsMainConfigAgent(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	server := NewServer(runtimeRef)
	request := httptest.NewRequest(http.MethodDelete, "/api/resources/agents/chat", nil)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected agent delete 400, got %d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "main config") {
		t.Fatalf("expected main config error, got %s", response.Body.String())
	}
}

func TestServerProviderResourceSaveWritesConfigModule(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	server := NewServer(runtimeRef)
	body := strings.NewReader(`{
		"id":"deepseek",
		"provider":"openai-compatible",
		"base_url":"https://api.deepseek.com/v1",
		"api_key":"${DEEPSEEK_API_KEY}",
		"model":"deepseek-chat",
		"fallback_provider":"primary",
		"timeout":"45s",
		"temperature":0.2,
		"max_tokens":4096,
		"retry_count":2,
		"retry_backoff":"2s"
	}`)
	request := httptest.NewRequest(http.MethodPut, "/api/resources/providers/deepseek", body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected provider save 200, got %d body=%s", response.Code, response.Body.String())
	}
	path := filepath.Join(runtimeRef.RuntimeHome(), "configs", "providers", "deepseek.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected provider module written: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "deepseek:") || !strings.Contains(content, "base_url: https://api.deepseek.com/v1") || !strings.Contains(response.Body.String(), `"api_key_set":true`) || !strings.Contains(response.Body.String(), `"restart_required":true`) || !strings.Contains(response.Body.String(), `"apply_state":"restart_required"`) {
		t.Fatalf("unexpected provider config response=%s file=\n%s", response.Body.String(), content)
	}
	if strings.Contains(content, "restart_required") || strings.Contains(content, "apply_state") {
		t.Fatalf("expected restart hints to stay out of provider yaml, got\n%s", content)
	}

	detailRequest := httptest.NewRequest(http.MethodGet, "/api/resources/providers/deepseek", nil)
	detailResponse := httptest.NewRecorder()
	server.ServeHTTP(detailResponse, detailRequest)
	if detailResponse.Code != http.StatusOK || !strings.Contains(detailResponse.Body.String(), `"restart_required":true`) || !strings.Contains(detailResponse.Body.String(), `"apply_state":"restart_required"`) {
		t.Fatalf("expected saved provider detail to keep restart hint, got %d body=%s", detailResponse.Code, detailResponse.Body.String())
	}
	listRequest := httptest.NewRequest(http.MethodGet, "/api/resources/providers", nil)
	listResponse := httptest.NewRecorder()
	server.ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK || !strings.Contains(listResponse.Body.String(), `"id":"deepseek"`) || !strings.Contains(listResponse.Body.String(), `"restart_required":true`) {
		t.Fatalf("expected saved provider list item to keep restart hint, got %d body=%s", listResponse.Code, listResponse.Body.String())
	}
}

func TestServerProviderResourceDeleteRemovesConfigModule(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	server := NewServer(runtimeRef)
	body := strings.NewReader(`{
		"id":"deepseek",
		"provider":"openai-compatible",
		"base_url":"https://api.deepseek.com/v1",
		"api_key":"${DEEPSEEK_API_KEY}",
		"model":"deepseek-chat",
		"timeout":"45s"
	}`)
	saveRequest := httptest.NewRequest(http.MethodPut, "/api/resources/providers/deepseek", body)
	saveRequest.Header.Set("Content-Type", "application/json")
	saveResponse := httptest.NewRecorder()
	server.ServeHTTP(saveResponse, saveRequest)
	if saveResponse.Code != http.StatusOK {
		t.Fatalf("expected provider save 200, got %d body=%s", saveResponse.Code, saveResponse.Body.String())
	}

	deleteRequest := httptest.NewRequest(http.MethodDelete, "/api/resources/providers/deepseek", nil)
	deleteResponse := httptest.NewRecorder()
	server.ServeHTTP(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("expected provider delete 204, got %d body=%s", deleteResponse.Code, deleteResponse.Body.String())
	}
	path := filepath.Join(runtimeRef.RuntimeHome(), "configs", "providers", "deepseek.yaml")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected provider module deleted, got %v", err)
	}
}

func TestServerProviderResourceSaveRejectsInvalidFallback(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	server := NewServer(runtimeRef)
	body := strings.NewReader(`{
		"id":"deepseek",
		"provider":"openai-compatible",
		"base_url":"https://api.deepseek.com/v1",
		"api_key":"${DEEPSEEK_API_KEY}",
		"model":"deepseek-chat",
		"fallback_provider":"missing-provider",
		"timeout":"45s"
	}`)
	request := httptest.NewRequest(http.MethodPut, "/api/resources/providers/deepseek", body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected provider save 400, got %d body=%s", response.Code, response.Body.String())
	}
	var result resourceValidationResult
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatalf("decode provider validation error: %v", err)
	}
	if result.Valid || result.Resource != "provider" || result.Name != "deepseek" || len(result.Issues) != 1 {
		t.Fatalf("unexpected provider validation result: %#v", result)
	}
	if result.Issues[0].Field != "fallback_provider" || result.Issues[0].Code != "unknown_provider_reference" || !strings.Contains(result.Issues[0].Message, "unknown fallback_provider") {
		t.Fatalf("unexpected provider validation issue: %#v", result.Issues[0])
	}
}

func TestServerProviderResourceDeleteRejectsMainConfigProvider(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	server := NewServer(runtimeRef)
	request := httptest.NewRequest(http.MethodDelete, "/api/resources/providers/primary", nil)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected provider delete 400, got %d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "main config") {
		t.Fatalf("expected main config error, got %s", response.Body.String())
	}
}

func TestServerToolResourceSaveWritesCodeAndConfigSnippet(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	server := NewServer(runtimeRef)
	body := strings.NewReader(`{
		"name":"notes-helper",
		"language":"python",
		"command":"python",
		"args":["./mcp_servers/notes-helper.py"],
		"enabled":true,
		"timeout":"30s",
		"workdir":".",
		"env_allowlist":["PATH"],
		"isolation":"process_group",
		"restart_limit":3,
		"cooldown":"10s",
		"max_concurrent_calls":1,
		"allowed_commands":["python"],
		"max_request_bytes":65536,
		"max_response_bytes":2097152,
		"code":"#!/usr/bin/env python3\nprint('ok')\n"
	}`)
	request := httptest.NewRequest(http.MethodPut, "/api/resources/tools/notes-helper", body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected tool save 200, got %d body=%s", response.Code, response.Body.String())
	}
	codePath := filepath.Join(runtimeRef.RuntimeHome(), "mcp_servers", "notes-helper.py")
	configPath := filepath.Join(runtimeRef.RuntimeHome(), "configs", "mcp_servers", "notes-helper.yaml")
	code, err := os.ReadFile(codePath)
	if err != nil {
		t.Fatalf("expected tool code written: %v", err)
	}
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("expected tool config written: %v", err)
	}
	if !strings.Contains(string(code), "print('ok')") || !strings.Contains(string(config), "mcp_servers:") || !strings.Contains(string(config), "notes-helper") {
		t.Fatalf("unexpected tool files response=%s code=\n%s config=\n%s", response.Body.String(), string(code), string(config))
	}
	if !strings.Contains(response.Body.String(), `"restart_required":true`) || !strings.Contains(response.Body.String(), `"apply_state":"restart_required"`) {
		t.Fatalf("expected tool save restart hint, got %s", response.Body.String())
	}
	if strings.Contains(string(config), "restart_required") || strings.Contains(string(config), "apply_state") {
		t.Fatalf("expected restart hints to stay out of tool yaml, got\n%s", string(config))
	}
	if strings.Contains(string(config), "Merge this block") {
		t.Fatalf("expected modular mcp config guidance, got\n%s", string(config))
	}

	detailRequest := httptest.NewRequest(http.MethodGet, "/api/resources/tools/notes-helper", nil)
	detailResponse := httptest.NewRecorder()
	server.ServeHTTP(detailResponse, detailRequest)
	if detailResponse.Code != http.StatusOK || !strings.Contains(detailResponse.Body.String(), `"restart_required":true`) || !strings.Contains(detailResponse.Body.String(), `"apply_state":"restart_required"`) {
		t.Fatalf("expected saved tool detail to keep restart hint, got %d body=%s", detailResponse.Code, detailResponse.Body.String())
	}
	listRequest := httptest.NewRequest(http.MethodGet, "/api/resources/tools", nil)
	listResponse := httptest.NewRecorder()
	server.ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK || !strings.Contains(listResponse.Body.String(), `"name":"notes-helper"`) || !strings.Contains(listResponse.Body.String(), `"restart_required":true`) {
		t.Fatalf("expected saved tool list item to keep restart hint, got %d body=%s", listResponse.Code, listResponse.Body.String())
	}
}

func TestServerToolResourceSaveRejectsInvalidMCPConfig(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	server := NewServer(runtimeRef)
	body := strings.NewReader(`{
		"name":"appcontainer-helper",
		"language":"python",
		"command":"python",
		"args":["./mcp_servers/appcontainer-helper.py"],
		"enabled":true,
		"timeout":"30s",
		"workdir":".",
		"allowed_commands":["python"],
		"isolation":"windows_appcontainer",
		"code":"#!/usr/bin/env python3\nprint('ok')\n"
	}`)
	request := httptest.NewRequest(http.MethodPut, "/api/resources/tools/appcontainer-helper", body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected tool save 400, got %d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"valid":false`) ||
		!strings.Contains(response.Body.String(), `"resource":"tool"`) ||
		!strings.Contains(response.Body.String(), `"field":"isolation"`) ||
		!strings.Contains(response.Body.String(), `"code":"mcp_windows_appcontainer_unimplemented"`) ||
		!strings.Contains(response.Body.String(), "use isolation: container") {
		t.Fatalf("expected structured AppContainer validation error, got %s", response.Body.String())
	}
	for _, path := range []string{
		filepath.Join(runtimeRef.RuntimeHome(), "mcp_servers", "appcontainer-helper.py"),
		filepath.Join(runtimeRef.RuntimeHome(), "configs", "mcp_servers", "appcontainer-helper.yaml"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("expected invalid tool save not to write %s, got %v", path, err)
		}
	}
}

func TestServerToolResourceValidateReportsSpecificMCPIsolationFields(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	server := NewServer(runtimeRef)
	request := httptest.NewRequest(http.MethodPost, "/api/resources/tools/bad-isolation/validate", strings.NewReader(`{
		"language":"python",
		"command":"python",
		"args":["./mcp_servers/bad-isolation.py"],
		"enabled":true,
		"timeout":"30s",
		"workdir":".",
		"allowed_commands":["python"],
		"network_disabled":true,
		"isolation":"container",
		"isolation_options":{"network":"host","image":"ghcr.io/example/tool:1.0"},
		"code":"#!/usr/bin/env python3\nprint('ok')\n"
	}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), `"valid":false`) ||
		!strings.Contains(response.Body.String(), `"field":"isolation_options.network"`) ||
		!strings.Contains(response.Body.String(), `"code":"mcp_container_network_conflict"`) {
		t.Fatalf("expected specific container network validation error, got %d body=%s", response.Code, response.Body.String())
	}
}

func TestServerToolScaffoldPresetsCreateContainerTool(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	server := NewServer(runtimeRef)

	listRequest := httptest.NewRequest(http.MethodGet, "/api/resources/tools/scaffolds", nil)
	listResponse := httptest.NewRecorder()
	server.ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("expected tool scaffold list 200, got %d body=%s", listResponse.Code, listResponse.Body.String())
	}
	var scaffoldList []toolScaffoldPreset
	if err := json.Unmarshal(listResponse.Body.Bytes(), &scaffoldList); err != nil {
		t.Fatalf("decode scaffold list: %v", err)
	}
	readonlyPreset := toolScaffoldPresetByName(scaffoldList, "python-container-readonly")
	if readonlyPreset == nil {
		t.Fatalf("expected readonly scaffold preset, got %#v", scaffoldList)
	}
	if readonlyPreset.IsolationProfile != "readonly" ||
		readonlyPreset.DefaultOptions["workspace_mount"] != "ro" ||
		readonlyPreset.DefaultOptions["network"] != "disabled" ||
		readonlyPreset.DefaultOptions["user"] != "65532:65532" {
		t.Fatalf("expected readonly scaffold profile defaults, got %#v", readonlyPreset)
	}
	productionPreset := toolScaffoldPresetByName(scaffoldList, "python-container-production")
	if productionPreset == nil {
		t.Fatalf("expected production scaffold preset, got %#v", scaffoldList)
	}
	if productionPreset.IsolationProfile != "production" ||
		productionPreset.DefaultOptions["workspace_mount"] != "ro" ||
		productionPreset.DefaultOptions["network"] != "disabled" ||
		productionPreset.DefaultOptions["pull_policy"] != "never" ||
		!configDiagnosticsStringSliceContains(productionPreset.Recommendations, "replace the default image tag with an immutable digest before deployment") {
		t.Fatalf("expected production scaffold profile defaults and provenance guidance, got %#v", productionPreset)
	}
	if !strings.Contains(listResponse.Body.String(), "python-container-readonly") {
		t.Fatalf("expected container tool scaffold in list, got %s", listResponse.Body.String())
	}
	if !strings.Contains(listResponse.Body.String(), `"safety_guards"`) ||
		!strings.Contains(listResponse.Body.String(), `"capabilities"`) ||
		!strings.Contains(listResponse.Body.String(), `"generated_paths"`) ||
		!strings.Contains(listResponse.Body.String(), `"activation_steps"`) ||
		!strings.Contains(listResponse.Body.String(), "symlink escape rejection") {
		t.Fatalf("expected tool scaffold list to include Studio-facing guidance fields, got %s", listResponse.Body.String())
	}
	if !strings.Contains(listResponse.Body.String(), `"default_image":"ghcr.io/fymatt/goflow-agent-mcp-python:latest"`) ||
		!strings.Contains(listResponse.Body.String(), `"default_isolation_options"`) ||
		!strings.Contains(listResponse.Body.String(), `"ipc":"none"`) ||
		!strings.Contains(listResponse.Body.String(), `"userns":"auto"`) ||
		!strings.Contains(listResponse.Body.String(), `"isolation_profile":"readonly"`) ||
		!strings.Contains(listResponse.Body.String(), `"memory_swap":"256m"`) ||
		!strings.Contains(listResponse.Body.String(), `"pull_policy":"missing"`) ||
		!strings.Contains(listResponse.Body.String(), `"tool_mount":"ro"`) {
		t.Fatalf("expected container scaffold list to expose default image and isolation options, got %s", listResponse.Body.String())
	}

	detailRequest := httptest.NewRequest(http.MethodGet, "/api/resources/tools/scaffolds/python-container-readonly?name=sandboxed-reader-test", nil)
	detailResponse := httptest.NewRecorder()
	server.ServeHTTP(detailResponse, detailRequest)
	if detailResponse.Code != http.StatusOK {
		t.Fatalf("expected tool scaffold detail 200, got %d body=%s", detailResponse.Code, detailResponse.Body.String())
	}
	var detailPreset toolScaffoldPreset
	if err := json.Unmarshal(detailResponse.Body.Bytes(), &detailPreset); err != nil {
		t.Fatalf("decode scaffold detail: %v", err)
	}
	if detailPreset.IsolationProfile != "readonly" ||
		detailPreset.Document == nil ||
		detailPreset.Document.IsolationProfile != "readonly" ||
		detailPreset.Document.IsolationOptions["workspace_mount"] != "ro" ||
		detailPreset.Document.IsolationOptions["user"] != "65532:65532" {
		t.Fatalf("expected scaffold detail to expose readonly profile document, got %#v", detailPreset)
	}
	if !strings.Contains(detailResponse.Body.String(), `"isolation":"container"`) ||
		!strings.Contains(detailResponse.Body.String(), `"runtime":"docker"`) ||
		!strings.Contains(detailResponse.Body.String(), "read-only workspace mount") {
		t.Fatalf("expected container scaffold document detail, got %s", detailResponse.Body.String())
	}

	productionDetailRequest := httptest.NewRequest(http.MethodGet, "/api/resources/tools/scaffolds/python-container-production?name=production-reader-test", nil)
	productionDetailResponse := httptest.NewRecorder()
	server.ServeHTTP(productionDetailResponse, productionDetailRequest)
	if productionDetailResponse.Code != http.StatusOK {
		t.Fatalf("expected production scaffold detail 200, got %d body=%s", productionDetailResponse.Code, productionDetailResponse.Body.String())
	}
	var productionDetail toolScaffoldPreset
	if err := json.Unmarshal(productionDetailResponse.Body.Bytes(), &productionDetail); err != nil {
		t.Fatalf("decode production scaffold detail: %v", err)
	}
	if productionDetail.Document == nil ||
		productionDetail.Document.IsolationProfile != "production" ||
		productionDetail.Document.IsolationOptions["pull_policy"] != "never" ||
		productionDetail.Document.IsolationOptions["workspace_mount"] != "ro" ||
		productionDetail.Document.IsolationOptions["network"] != "disabled" {
		t.Fatalf("expected production scaffold document to use production profile defaults, got %#v", productionDetail)
	}

	createRequest := httptest.NewRequest(http.MethodPost, "/api/resources/tools/scaffolds/python-container-readonly", strings.NewReader(`{
		"name":"sandboxed-reader-test",
		"image":"python:3.13-slim",
		"runtime":"docker"
	}`))
	createResponse := httptest.NewRecorder()
	server.ServeHTTP(createResponse, createRequest)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("expected tool scaffold create 201, got %d body=%s", createResponse.Code, createResponse.Body.String())
	}
	var created toolScaffoldResponse
	if err := json.Unmarshal(createResponse.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode scaffold response: %v", err)
	}
	if !created.Created || created.Tool.Name != "sandboxed-reader-test" || created.Tool.Isolation != "container" {
		t.Fatalf("expected created container tool, got %#v", created)
	}
	if created.Tool.IsolationProfile != "readonly" ||
		created.Tool.IsolationOptions["image"] != "python:3.13-slim" ||
		created.Tool.IsolationOptions["runtime"] != "docker" ||
		created.Tool.IsolationOptions["tool_target"] != "/goflow-tools/sandboxed-reader-test.py" {
		t.Fatalf("expected created container tool to use readonly profile with explicit image/runtime/tool mount, got %#v", created.Tool)
	}
	codePath := filepath.Join(runtimeRef.RuntimeHome(), "mcp_servers", "sandboxed-reader-test.py")
	configPath := filepath.Join(runtimeRef.RuntimeHome(), "configs", "mcp_servers", "sandboxed-reader-test.yaml")
	if _, err := os.Stat(codePath); err != nil {
		t.Fatalf("expected scaffold code file: %v", err)
	}
	codeData, err := os.ReadFile(codePath)
	if err != nil {
		t.Fatalf("expected scaffold code readable: %v", err)
	}
	codeText := string(codeData)
	for _, want := range []string{
		"GOFLOW_WORKSPACE_ROOT",
		"resolve_path",
		"relative_path",
		"is_error",
		"escapes workspace root",
		"resolves outside workspace root",
		"additionalProperties",
	} {
		if !strings.Contains(codeText, want) {
			t.Fatalf("expected scaffold code to contain %q, got\n%s", want, codeText)
		}
	}
	configData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("expected scaffold config file: %v", err)
	}
	configText := string(configData)
	for _, want := range []string{
		"isolation: container",
		"isolation_profile: readonly",
		"image: python:3.13-slim",
		"runtime: docker",
		"tool_source: " + codePath,
		"tool_target: /goflow-tools/sandboxed-reader-test.py",
	} {
		if !strings.Contains(configText, want) {
			t.Fatalf("expected scaffold config to contain %q, got\n%s", want, configText)
		}
	}

	conflictRequest := httptest.NewRequest(http.MethodPost, "/api/resources/tools/scaffolds/python-container-readonly", strings.NewReader(`{"name":"sandboxed-reader-test"}`))
	conflictResponse := httptest.NewRecorder()
	server.ServeHTTP(conflictResponse, conflictRequest)
	if conflictResponse.Code != http.StatusConflict {
		t.Fatalf("expected scaffold conflict 409, got %d body=%s", conflictResponse.Code, conflictResponse.Body.String())
	}
}

func TestServerToolScaffoldContainerImageDefaultsToReleaseVersion(t *testing.T) {
	previous := version.Version
	version.Version = "v9.8.7"
	t.Cleanup(func() { version.Version = previous })

	runtimeRef := newAPITestRuntime(t)
	server := NewServer(runtimeRef)
	request := httptest.NewRequest(http.MethodGet, "/api/resources/tools/scaffolds/python-container-readonly?name=release-reader", nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected tool scaffold detail 200, got %d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"image":"ghcr.io/fymatt/goflow-agent-mcp-python:v9.8.7"`) ||
		strings.Contains(response.Body.String(), `"image":"ghcr.io/fymatt/goflow-agent-mcp-python:latest"`) {
		t.Fatalf("expected release-versioned default MCP Python image, got %s", response.Body.String())
	}
}

func TestServerToolScaffoldPresetsLoadRuntimeOverrides(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	dir := filepath.Join(runtimeRef.RuntimeHome(), "templates", "tools", "scaffolds")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create custom tool scaffold dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "custom.yaml"), []byte(`
name: python-notes-local
default_name: notes-helper
title: Python Notes Helper
description: Runtime-defined local helper preset.
category: local
language: python
isolation: process_group
requires_restart: true
capabilities: [stdio-json-rpc]
`), 0o644); err != nil {
		t.Fatalf("write custom tool scaffold preset: %v", err)
	}

	server := NewServer(runtimeRef)
	request := httptest.NewRequest(http.MethodGet, "/api/resources/tools/scaffolds", nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected tool scaffold list 200, got %d body=%s", response.Code, response.Body.String())
	}
	var presets []toolScaffoldPreset
	if err := json.Unmarshal(response.Body.Bytes(), &presets); err != nil {
		t.Fatalf("decode tool scaffold presets: %v", err)
	}
	custom := toolScaffoldPresetByName(presets, "python-notes-local")
	if custom == nil || custom.Title != "Python Notes Helper" || custom.Language != "python" {
		t.Fatalf("expected runtime-defined tool preset, got %#v", presets)
	}
}

func TestServerToolResourcesIncludeRiskProfiles(t *testing.T) {
	t.Setenv("MCP_API_KEY", "super-secret-value")
	runtimeRef := newAPITestRuntimeWithStateHomeResponsesAndMCP(t, session.New(8), "", nil, []config.MCPServerRef{{
		Name:            "stub",
		Command:         "stub",
		Enabled:         true,
		Isolation:       "process_group",
		EnvAllowlist:    []string{"PATH", "MCP_API_KEY"},
		AllowedCommands: []string{"stub"},
	}})
	server := NewServer(runtimeRef)
	listRequest := httptest.NewRequest(http.MethodGet, "/api/resources/tools", nil)
	listResponse := httptest.NewRecorder()
	server.ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("expected tool list 200, got %d body=%s", listResponse.Code, listResponse.Body.String())
	}
	body := listResponse.Body.String()
	for _, expected := range []string{
		`"name":"stub/write_file"`,
		`"risk"`,
		`"qualified_name":"stub/write_file"`,
		`"kind":"write"`,
		`"risk_level":"high"`,
		`"requires_approval":true`,
		`"workspace_scoped_inputs":true`,
		`"workspace_scope_enforced":false`,
		`"destructive":true`,
		`"isolation_level":"lifecycle"`,
		`"sandboxed":false`,
		`GOFLOW_WORKSPACE_ROOT`,
		`"env_allowlist":["MCP_API_KEY","PATH"]`,
		`"sensitive_env":["MCP_API_KEY"]`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("expected tool risk profile to contain %s, got %s", expected, body)
		}
	}
	if strings.Contains(body, "super-secret-value") {
		t.Fatalf("expected tool risk profile to omit secret env values, got %s", body)
	}

	runtimeDetailRequest := httptest.NewRequest(http.MethodGet, "/api/resources/tools/stub/write_file", nil)
	runtimeDetailResponse := httptest.NewRecorder()
	server.ServeHTTP(runtimeDetailResponse, runtimeDetailRequest)
	if runtimeDetailResponse.Code != http.StatusOK ||
		!strings.Contains(runtimeDetailResponse.Body.String(), `"name":"stub/write_file"`) ||
		!strings.Contains(runtimeDetailResponse.Body.String(), `"qualified_name":"stub/write_file"`) ||
		!strings.Contains(runtimeDetailResponse.Body.String(), `"isolation_level":"lifecycle"`) {
		t.Fatalf("expected qualified runtime tool detail to include risk profile, got %d body=%s", runtimeDetailResponse.Code, runtimeDetailResponse.Body.String())
	}

	detailRequest := httptest.NewRequest(http.MethodGet, "/api/resources/tools/unknown-helper", nil)
	detailResponse := httptest.NewRecorder()
	server.ServeHTTP(detailResponse, detailRequest)
	if detailResponse.Code != http.StatusOK ||
		!strings.Contains(detailResponse.Body.String(), `"risk"`) ||
		!strings.Contains(detailResponse.Body.String(), `"kind":"unknown"`) ||
		!strings.Contains(detailResponse.Body.String(), `"isolation_level":"unknown"`) {
		t.Fatalf("expected default custom tool detail to include conservative risk, got %d body=%s", detailResponse.Code, detailResponse.Body.String())
	}
}

func TestToolRiskProfileMarksKnownWorkspaceScopedTools(t *testing.T) {
	server := NewServer(newAPITestRuntime(t))
	profile := server.toolRiskProfileForTool(schema.Tool{
		Name:   "read_file",
		Server: "file_tools",
		Kind:   "read",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{"path":{"type":"string"}},
			"required":["path"],
			"additionalProperties":false
		}`),
	})
	if profile == nil || !profile.WorkspaceScopedInputs || !profile.WorkspaceScopeEnforced {
		t.Fatalf("expected known file_tools path input to be marked workspace scoped/enforced, got %#v", profile)
	}
}

func TestToolRiskProfileDetectsNestedWorkspacePathInputs(t *testing.T) {
	server := NewServer(newAPITestRuntime(t))
	profile := server.toolRiskProfileForTool(schema.Tool{
		Name:   "batch_upload",
		Server: "third_party",
		Kind:   "write",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"items":{
					"type":"array",
					"items":{
						"type":"object",
						"properties":{
							"source":{"type":"string","description":"workspace file path to upload"},
							"metadata":{"type":"object","properties":{"label":{"type":"string"}}}
						}
					}
				}
			},
			"additionalProperties":false
		}`),
	})
	if profile == nil || !profile.WorkspaceScopedInputs || profile.WorkspaceScopeEnforced {
		t.Fatalf("expected nested path-like input to be detected as workspace-scoped but not proven enforced, got %#v", profile)
	}
	if !configDiagnosticsStringSliceContains(profile.Capabilities, "workspace_path_input") {
		t.Fatalf("expected workspace_path_input capability, got %#v", profile.Capabilities)
	}
	if !configDiagnosticsStringSliceContains(profile.Warnings, "tool accepts file or directory path inputs; GoFlow can pass GOFLOW_WORKSPACE_ROOT but cannot prove the tool enforces workspace scoping") {
		t.Fatalf("expected workspace-scope warning, got %#v", profile.Warnings)
	}
}

type apiSnapshotProvider struct {
	snapshot session.Snapshot
}

func (p apiSnapshotProvider) SessionSnapshot() session.Snapshot {
	return p.snapshot
}

func TestServerAnnotatesPendingApprovalRiskProfiles(t *testing.T) {
	runtimeRef := newAPITestRuntimeWithStateHomeResponsesAndMCP(t, session.New(8), "", nil, []config.MCPServerRef{{
		Name:            "stub",
		Command:         "stub",
		Enabled:         true,
		Isolation:       "process_group",
		AllowedCommands: []string{"stub"},
	}})
	server := NewServer(runtimeRef)
	snapshot := server.snapshotWithApprovalRisk(apiSnapshotProvider{snapshot: session.Snapshot{
		PendingApprovals: []session.PendingApprovalSnapshot{{
			CallID:       "call-1",
			ToolName:     "write_file",
			AgentID:      "fixer",
			AgentRunID:   "agent-run-1",
			WorkflowName: "plan-fix-audit",
			Stage:        "fix",
		}},
		AgentRuns: []session.AgentRunSnapshot{{
			ID:            "agent-run-1",
			Status:        "awaiting_tool_approval",
			PendingCallID: "call-1",
			PendingTool:   "write_file",
			PendingApprovals: []session.PendingApprovalSnapshot{{
				CallID:     "call-1",
				ToolName:   "write_file",
				AgentID:    "fixer",
				AgentRunID: "agent-run-1",
			}},
		}},
		Workflow: session.WorkflowSnapshot{
			RunID:           "workflow-run-1",
			Name:            "plan-fix-audit",
			Status:          "awaiting_tool_approval",
			NextStage:       "fix",
			PendingCallID:   "call-1",
			PendingToolName: "write_file",
		},
		WorkflowRuns: []session.WorkflowRunSnapshot{{
			ID:              "workflow-run-1",
			Name:            "plan-fix-audit",
			Status:          "awaiting_tool_approval",
			NextStage:       "fix",
			PendingCallID:   "call-1",
			PendingToolName: "write_file",
		}},
	}})

	if got := snapshot.PendingApprovals[0].Risk; got == nil || got.RiskLevel != "high" || got.Kind != "write" || !got.RequiresApproval || !got.Destructive {
		t.Fatalf("expected global pending approval risk, got %#v", got)
	}
	if got := snapshot.AgentRuns[0].PendingApprovals[0].Risk; got == nil || got.RiskLevel != "high" || got.IsolationLevel != "lifecycle" {
		t.Fatalf("expected agent run pending approval risk, got %#v", got)
	}
	if got := snapshot.Workflow.PendingToolRisk; got == nil || got.RiskLevel != "high" || got.IsolationLevel != "lifecycle" {
		t.Fatalf("expected workflow pending tool risk, got %#v", got)
	}
	if got := snapshot.WorkflowRuns[0].PendingToolRisk; got == nil || got.RiskLevel != "high" || got.IsolationLevel != "lifecycle" {
		t.Fatalf("expected workflow run pending tool risk, got %#v", got)
	}
}

func TestServerRunStreamApprovalEventsIncludeRiskProfile(t *testing.T) {
	runtimeRef := newAPITestRuntimeWithStateHomeResponsesAndMCP(t, session.New(8), "", []schema.ChatResponse{{
		ToolCalls: []schema.ToolCall{{
			ID:        "call-1",
			Name:      "write_file",
			Arguments: json.RawMessage(`{"path":"note.txt","content":"hello"}`),
		}},
	}}, []config.MCPServerRef{{
		Name:            "stub",
		Command:         "stub",
		Enabled:         true,
		Isolation:       "process_group",
		AllowedCommands: []string{"stub"},
	}})
	server := NewServer(runtimeRef)

	request := httptest.NewRequest(http.MethodPost, "/api/run/stream", strings.NewReader(`{"input":"write a note"}`))
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	body := response.Body.String()
	if response.Code != http.StatusOK {
		t.Fatalf("expected run stream 200, got %d body=%s", response.Code, body)
	}
	for _, want := range []string{
		"event: approval",
		`"risk"`,
		`"risk_level":"high"`,
		`"kind":"write"`,
		`"isolation_level":"lifecycle"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected stream approval event to include %s, got %s", want, body)
		}
	}
}

func TestServerToolResourceDeleteRemovesCodeAndConfig(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	server := NewServer(runtimeRef)
	body := strings.NewReader(`{
		"name":"notes-helper",
		"language":"python",
		"command":"python",
		"args":["./mcp_servers/notes-helper.py"],
		"enabled":true,
		"timeout":"30s",
		"workdir":".",
		"code":"#!/usr/bin/env python3\nprint('ok')\n"
	}`)
	saveRequest := httptest.NewRequest(http.MethodPut, "/api/resources/tools/notes-helper", body)
	saveRequest.Header.Set("Content-Type", "application/json")
	saveResponse := httptest.NewRecorder()
	server.ServeHTTP(saveResponse, saveRequest)
	if saveResponse.Code != http.StatusOK {
		t.Fatalf("expected tool save 200, got %d body=%s", saveResponse.Code, saveResponse.Body.String())
	}

	deleteRequest := httptest.NewRequest(http.MethodDelete, "/api/resources/tools/notes-helper", nil)
	deleteResponse := httptest.NewRecorder()
	server.ServeHTTP(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("expected tool delete 204, got %d body=%s", deleteResponse.Code, deleteResponse.Body.String())
	}
	for _, path := range []string{
		filepath.Join(runtimeRef.RuntimeHome(), "mcp_servers", "notes-helper.py"),
		filepath.Join(runtimeRef.RuntimeHome(), "configs", "mcp_servers", "notes-helper.yaml"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("expected tool module path deleted %s, got %v", path, err)
		}
	}
}

func TestServerToolResourceDeleteRejectsDiscoveredRuntimeTool(t *testing.T) {
	server := NewServer(newAPITestRuntime(t))
	request := httptest.NewRequest(http.MethodDelete, "/api/resources/tools/write_file", nil)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected tool delete 400, got %d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "active MCP runtime") {
		t.Fatalf("expected active MCP runtime delete error, got %s", response.Body.String())
	}
}

func TestServerPolicyRuleResourceSaveListsAndDeletesRule(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	server := NewServer(runtimeRef)

	scaffoldListRequest := httptest.NewRequest(http.MethodGet, "/api/resources/policy-rules/scaffolds", nil)
	scaffoldListResponse := httptest.NewRecorder()
	server.ServeHTTP(scaffoldListResponse, scaffoldListRequest)
	if scaffoldListResponse.Code != http.StatusOK || !strings.Contains(scaffoldListResponse.Body.String(), `"risk-threshold"`) || !strings.Contains(scaffoldListResponse.Body.String(), `"team-review-quorum"`) {
		t.Fatalf("expected policy rule scaffold list, got %d body=%s", scaffoldListResponse.Code, scaffoldListResponse.Body.String())
	}
	scaffoldDetailRequest := httptest.NewRequest(http.MethodGet, "/api/resources/policy-rules/scaffolds/risk-threshold?name=scaffold-risk-gate", nil)
	scaffoldDetailResponse := httptest.NewRecorder()
	server.ServeHTTP(scaffoldDetailResponse, scaffoldDetailRequest)
	if scaffoldDetailResponse.Code != http.StatusOK || !strings.Contains(scaffoldDetailResponse.Body.String(), `"document"`) || !strings.Contains(scaffoldDetailResponse.Body.String(), `"scaffold-risk-gate"`) || !strings.Contains(scaffoldDetailResponse.Body.String(), `"risk_at_least"`) {
		t.Fatalf("expected policy rule scaffold detail document, got %d body=%s", scaffoldDetailResponse.Code, scaffoldDetailResponse.Body.String())
	}
	scaffoldCreateRequest := httptest.NewRequest(http.MethodPost, "/api/resources/policy-rules/scaffolds/risk-threshold", strings.NewReader(`{
		"name":"scaffold-risk-gate",
		"label":"Scaffold Risk Gate",
		"defaults":{"minimum":"critical"}
	}`))
	scaffoldCreateRequest.Header.Set("Content-Type", "application/json")
	scaffoldCreateResponse := httptest.NewRecorder()
	server.ServeHTTP(scaffoldCreateResponse, scaffoldCreateRequest)
	if scaffoldCreateResponse.Code != http.StatusCreated || !strings.Contains(scaffoldCreateResponse.Body.String(), `"created":true`) || !strings.Contains(scaffoldCreateResponse.Body.String(), `"scaffold-risk-gate"`) || !strings.Contains(scaffoldCreateResponse.Body.String(), `"critical"`) {
		t.Fatalf("expected policy rule scaffold create, got %d body=%s", scaffoldCreateResponse.Code, scaffoldCreateResponse.Body.String())
	}
	scaffoldPath := filepath.Join(runtimeRef.RuntimeHome(), "policies", "workflow_rules", "scaffold-risk-gate.yaml")
	if data, err := os.ReadFile(scaffoldPath); err != nil || !strings.Contains(string(data), "goflow.workflow_policy_rule") || !strings.Contains(string(data), "risk_at_least") {
		t.Fatalf("expected scaffold policy rule file, err=%v data=%s", err, string(data))
	}
	scaffoldConflictRequest := httptest.NewRequest(http.MethodPost, "/api/resources/policy-rules/scaffolds/risk-threshold", strings.NewReader(`{"name":"scaffold-risk-gate"}`))
	scaffoldConflictRequest.Header.Set("Content-Type", "application/json")
	scaffoldConflictResponse := httptest.NewRecorder()
	server.ServeHTTP(scaffoldConflictResponse, scaffoldConflictRequest)
	if scaffoldConflictResponse.Code != http.StatusConflict {
		t.Fatalf("expected policy rule scaffold conflict, got %d body=%s", scaffoldConflictResponse.Code, scaffoldConflictResponse.Body.String())
	}
	scaffoldOverwriteRequest := httptest.NewRequest(http.MethodPost, "/api/resources/policy-rules/scaffolds/risk-threshold?overwrite=1", strings.NewReader(`{"name":"scaffold-risk-gate","defaults":{"minimum":"high"}}`))
	scaffoldOverwriteRequest.Header.Set("Content-Type", "application/json")
	scaffoldOverwriteResponse := httptest.NewRecorder()
	server.ServeHTTP(scaffoldOverwriteResponse, scaffoldOverwriteRequest)
	if scaffoldOverwriteResponse.Code != http.StatusOK || !strings.Contains(scaffoldOverwriteResponse.Body.String(), `"updated":true`) || !strings.Contains(scaffoldOverwriteResponse.Body.String(), `"high"`) {
		t.Fatalf("expected policy rule scaffold overwrite, got %d body=%s", scaffoldOverwriteResponse.Code, scaffoldOverwriteResponse.Body.String())
	}

	validateRequest := httptest.NewRequest(http.MethodPost, "/api/resources/policy-rules/critical-finding/validate", strings.NewReader(`{
		"label":"Critical Finding",
		"operator":"risk_at_least",
		"defaults":{
			"ref":"stages.audit.outputs.risk",
			"minimum":"high"
		},
		"reason":"referenced evidence did not meet high severity"
	}`))
	validateRequest.Header.Set("Content-Type", "application/json")
	validateResponse := httptest.NewRecorder()
	server.ServeHTTP(validateResponse, validateRequest)
	if validateResponse.Code != http.StatusOK || !strings.Contains(validateResponse.Body.String(), `"valid":true`) || !strings.Contains(validateResponse.Body.String(), `"name":"critical-finding"`) || !strings.Contains(validateResponse.Body.String(), `"normalized"`) || !strings.Contains(validateResponse.Body.String(), `"kind":"goflow.workflow_policy_rule"`) || !strings.Contains(validateResponse.Body.String(), `"version":2`) {
		t.Fatalf("expected valid policy rule dry-run response, got %d body=%s", validateResponse.Code, validateResponse.Body.String())
	}
	if _, err := os.Stat(filepath.Join(runtimeRef.RuntimeHome(), "policies", "workflow_rules", "critical-finding.yaml")); !os.IsNotExist(err) {
		t.Fatalf("expected policy rule validate not to write file, stat err=%v", err)
	}

	invalidValidateRequest := httptest.NewRequest(http.MethodPost, "/api/resources/policy-rules/validate", strings.NewReader(`{
		"name":"bad-rule",
		"operator":"not-real"
	}`))
	invalidValidateRequest.Header.Set("Content-Type", "application/json")
	invalidValidateResponse := httptest.NewRecorder()
	server.ServeHTTP(invalidValidateResponse, invalidValidateRequest)
	if invalidValidateResponse.Code != http.StatusOK || !strings.Contains(invalidValidateResponse.Body.String(), `"valid":false`) || !strings.Contains(invalidValidateResponse.Body.String(), `"field":"operator"`) || !strings.Contains(invalidValidateResponse.Body.String(), "unsupported") {
		t.Fatalf("expected invalid policy rule dry-run response, got %d body=%s", invalidValidateResponse.Code, invalidValidateResponse.Body.String())
	}

	body := strings.NewReader(`{
		"name":"critical-finding",
		"label":"Critical Finding",
		"description":"Pass when audit evidence is at least high severity.",
		"operator":"risk_at_least",
		"defaults":{
			"ref":"stages.audit.outputs.risk",
			"minimum":"high"
		},
		"reason":"referenced evidence did not meet high severity",
		"params":[
			{"name":"params.ref","label":"Evidence reference","type":"reference"},
			{"name":"params.minimum","label":"Minimum severity","type":"select","options":["low","medium","high","critical"]}
		]
	}`)
	saveRequest := httptest.NewRequest(http.MethodPut, "/api/resources/policy-rules/critical-finding", body)
	saveRequest.Header.Set("Content-Type", "application/json")
	saveResponse := httptest.NewRecorder()

	server.ServeHTTP(saveResponse, saveRequest)

	if saveResponse.Code != http.StatusOK {
		t.Fatalf("expected policy rule save 200, got %d body=%s", saveResponse.Code, saveResponse.Body.String())
	}
	path := filepath.Join(runtimeRef.RuntimeHome(), "policies", "workflow_rules", "critical-finding.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected policy rule written: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "goflow.workflow_policy_rule") || !strings.Contains(content, "version: 2") || !strings.Contains(content, "critical-finding") || !strings.Contains(content, "risk_at_least") ||
		!strings.Contains(saveResponse.Body.String(), `"kind":"goflow.workflow_policy_rule"`) || !strings.Contains(saveResponse.Body.String(), `"version":2`) || !strings.Contains(saveResponse.Body.String(), `"path"`) {
		t.Fatalf("unexpected policy rule response=%s file=\n%s", saveResponse.Body.String(), content)
	}

	listRequest := httptest.NewRequest(http.MethodGet, "/api/resources/policy-rules", nil)
	listResponse := httptest.NewRecorder()
	server.ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK || !strings.Contains(listResponse.Body.String(), `"critical-finding"`) {
		t.Fatalf("expected policy rule list to include custom rule, got %d body=%s", listResponse.Code, listResponse.Body.String())
	}

	optionsRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-options", nil)
	optionsResponse := httptest.NewRecorder()
	server.ServeHTTP(optionsResponse, optionsRequest)
	if optionsResponse.Code != http.StatusOK || !strings.Contains(optionsResponse.Body.String(), `"critical-finding"`) || !strings.Contains(optionsResponse.Body.String(), `"custom":true`) {
		t.Fatalf("expected workflow options to include custom policy rule, got %d body=%s", optionsResponse.Code, optionsResponse.Body.String())
	}

	getRequest := httptest.NewRequest(http.MethodGet, "/api/resources/policy-rules/critical-finding", nil)
	getResponse := httptest.NewRecorder()
	server.ServeHTTP(getResponse, getRequest)
	if getResponse.Code != http.StatusOK || !strings.Contains(getResponse.Body.String(), `"operator":"risk_at_least"`) {
		t.Fatalf("expected policy rule get 200, got %d body=%s", getResponse.Code, getResponse.Body.String())
	}
	futureRequest := httptest.NewRequest(http.MethodPut, "/api/resources/policy-rules/future-rule", strings.NewReader(`{
		"kind":"goflow.workflow_policy_rule",
		"version":99,
		"name":"future-rule",
		"operator":"expression",
		"expression":"true"
	}`))
	futureRequest.Header.Set("Content-Type", "application/json")
	futureResponse := httptest.NewRecorder()
	server.ServeHTTP(futureResponse, futureRequest)
	if futureResponse.Code != http.StatusBadRequest || !strings.Contains(futureResponse.Body.String(), "newer than supported") {
		t.Fatalf("expected future policy rule rejection, got %d body=%s", futureResponse.Code, futureResponse.Body.String())
	}
	var futureValidation policyRuleValidationResult
	if err := json.NewDecoder(futureResponse.Body).Decode(&futureValidation); err != nil {
		t.Fatalf("decode future policy rule validation response: %v", err)
	}
	if futureValidation.Valid || futureValidation.Name != "future-rule" || len(futureValidation.Issues) != 1 || futureValidation.Issues[0].Field != "version" || futureValidation.Issues[0].Code != "unsupported_version" {
		t.Fatalf("unexpected future policy rule validation response: %#v", futureValidation)
	}

	deleteRequest := httptest.NewRequest(http.MethodDelete, "/api/resources/policy-rules/critical-finding", nil)
	deleteResponse := httptest.NewRecorder()
	server.ServeHTTP(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("expected policy rule delete 204, got %d body=%s", deleteResponse.Code, deleteResponse.Body.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected policy rule deleted, got %v", err)
	}
	scaffoldDeleteRequest := httptest.NewRequest(http.MethodDelete, "/api/resources/policy-rules/scaffold-risk-gate", nil)
	scaffoldDeleteResponse := httptest.NewRecorder()
	server.ServeHTTP(scaffoldDeleteResponse, scaffoldDeleteRequest)
	if scaffoldDeleteResponse.Code != http.StatusNoContent {
		t.Fatalf("expected scaffold policy rule delete 204, got %d body=%s", scaffoldDeleteResponse.Code, scaffoldDeleteResponse.Body.String())
	}
}

func TestServerPolicyRuleResourceRejectsInvalidRule(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	server := NewServer(runtimeRef)
	body := strings.NewReader(`{
		"name":"bad-rule",
		"operator":"expression"
	}`)
	request := httptest.NewRequest(http.MethodPut, "/api/resources/policy-rules/bad-rule", body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid policy rule 400, got %d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "expression is required") {
		t.Fatalf("expected validation error, got %s", response.Body.String())
	}
	var result policyRuleValidationResult
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatalf("decode policy rule validation response: %v", err)
	}
	if result.Valid || result.Name != "bad-rule" || len(result.Issues) != 1 || result.Issues[0].Field != "expression" || result.Issues[0].Code != "required_expression" {
		t.Fatalf("unexpected policy rule validation result: %#v", result)
	}
}

func TestServerPolicyRuleScaffoldPresetsLoadRuntimeOverrides(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	presetDir := filepath.Join(runtimeRef.RuntimeHome(), "templates", "policies", "scaffolds")
	if err := os.MkdirAll(presetDir, 0o755); err != nil {
		t.Fatalf("mkdir policy scaffold preset dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(presetDir, "custom.yaml"), []byte(`
presets:
  - name: risk-threshold
    default_name: custom-risk-gate
    title: Custom Risk Gate
    description: Runtime override for risk threshold.
    category: custom
    operator: risk_at_least
    defaults:
      ref: stages.security.outputs.risk
      minimum: critical
  - name: release-window
    default_name: release-window-gate
    title: Release Window Gate
    description: Require an operations release-window signal.
    category: operations
    operator: ref_truthy
    defaults:
      ref: stages.ops.outputs.release_window_open
`), 0o644); err != nil {
		t.Fatalf("write custom policy scaffold preset: %v", err)
	}

	server := NewServer(runtimeRef)
	request := httptest.NewRequest(http.MethodGet, "/api/resources/policy-rules/scaffolds", nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	body := response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(body, `"release-window"`) || !strings.Contains(body, `"Custom Risk Gate"`) || !strings.Contains(body, `"critical"`) {
		t.Fatalf("expected runtime policy scaffold presets, got %d body=%s", response.Code, body)
	}
}

func TestServerConfigDiagnosticsEndpointReportsConfigState(t *testing.T) {
	t.Setenv("HELPER_API_KEY", "super-secret-value")
	runtimeRef := newAPITestRuntime(t)
	configDir := filepath.Join(runtimeRef.RuntimeHome(), "configs")
	if err := os.MkdirAll(filepath.Join(configDir, "mcp_servers"), 0o755); err != nil {
		t.Fatalf("mkdir config modules: %v", err)
	}
	policyDir := filepath.Join(runtimeRef.RuntimeHome(), "policies", "workflow_rules")
	if err := os.MkdirAll(policyDir, 0o755); err != nil {
		t.Fatalf("mkdir policy rules: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "goflow.yaml"), []byte(`agent:
  name: GoFlow Agent
  max_iterations: 8
default_agent: chat
providers:
  primary:
    provider: openai-compatible
    base_url: http://localhost:9999/v1
    model: test-model
agents:
  chat:
    name: Chat
    provider: primary
    mode: chat
    max_iterations: 2
    allowed_tool_kinds: [read, write, exec]
    tool_policy: confirm
skill:
  directory: ./skills
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "mcp_servers", "helper.yaml"), []byte(`mcp_servers:
  - name: helper
    command: python
    args: [./mcp_servers/helper.py]
    enabled: true
    allowed_commands: [python]
    env_allowlist: [PATH, HELPER_API_KEY]
    network_disabled: true
    isolation: process_group
`), 0o644); err != nil {
		t.Fatalf("write mcp module: %v", err)
	}
	if err := os.WriteFile(filepath.Join(policyDir, "critical-finding.yaml"), []byte(`name: critical-finding
operator: risk_at_least
defaults:
  ref: stages.audit.outputs.risk
  minimum: high
`), 0o644); err != nil {
		t.Fatalf("write policy rule module: %v", err)
	}
	server := NewServer(runtimeRef)
	request := httptest.NewRequest(http.MethodGet, "/api/config/diagnostics", nil)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected diagnostics 200, got %d body=%s", response.Code, response.Body.String())
	}
	var diagnostics configDiagnosticsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &diagnostics); err != nil {
		t.Fatalf("decode diagnostics response: %v", err)
	}
	if diagnostics.Diagnostics.Total != len(diagnostics.Items) || diagnostics.Diagnostics.Warnings == 0 || diagnostics.Diagnostics.Info == 0 {
		t.Fatalf("expected diagnostic severity counts to match items, got counts=%#v items=%#v", diagnostics.Diagnostics, diagnostics.Items)
	}
	assertOptionalModuleDirDiagnostic(t, diagnostics.Items, "workflow_templates")
	assertOptionalModuleDirDiagnostic(t, diagnostics.Items, "workflow_schemas")
	assertOptionalModuleDirDiagnostic(t, diagnostics.Items, "workflow_node_metadata")
	if !configDiagnosticRecommendationContains(diagnostics.Items, "mcp_env_allowlist_sensitive", "remove API keys") ||
		!configDiagnosticRecommendationContains(diagnostics.Items, "mcp_network_disabled_advisory", "isolation: container") ||
		!configDiagnosticRecommendationContains(diagnostics.Items, "default_agent_broad_permissions", "specialist fixer agent") ||
		!configDiagnosticRecommendationContains(diagnostics.Items, "provider_api_key_missing", "environment variable") {
		t.Fatalf("expected actionable config diagnostic recommendations, got %#v", diagnostics.Items)
	}
	body := response.Body.String()
	if !strings.Contains(body, `"mcp_servers"`) ||
		!strings.Contains(body, `"diagnostics"`) ||
		!strings.Contains(body, `helper.yaml`) ||
		!strings.Contains(body, `"policy_rules"`) ||
		!strings.Contains(body, `critical-finding.yaml`) ||
		!strings.Contains(body, `"config_load_ok"`) ||
		!strings.Contains(body, `"mcp_network_disabled_advisory"`) ||
		!strings.Contains(body, `"mcp_isolation_lifecycle_only"`) ||
		!strings.Contains(body, `"mcp_env_allowlist_sensitive"`) ||
		!strings.Contains(body, `"recommendation"`) ||
		!strings.Contains(body, `"target_kind":"mcp_server"`) ||
		!strings.Contains(body, `"target_name":"helper"`) ||
		!strings.Contains(body, `"field":"env_allowlist"`) ||
		!strings.Contains(body, `"field":"network_disabled"`) ||
		!strings.Contains(body, `"field":"isolation"`) ||
		!strings.Contains(body, `HELPER_API_KEY`) {
		t.Fatalf("expected diagnostics to include modular mcp config, got %s", body)
	}
	if strings.Contains(body, "super-secret-value") {
		t.Fatalf("expected diagnostics to omit secret env values, got %s", body)
	}
}

func TestServerConfigDiagnosticsCanHideOptionalNonActionableItems(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	server := NewServer(runtimeRef)
	request := httptest.NewRequest(http.MethodGet, "/api/config/diagnostics?include_optional=0", nil)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected diagnostics 200, got %d body=%s", response.Code, response.Body.String())
	}
	var diagnostics configDiagnosticsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &diagnostics); err != nil {
		t.Fatalf("decode diagnostics response: %v", err)
	}
	for _, item := range diagnostics.Items {
		if item.Code == "module_dir_empty" && item.Optional {
			t.Fatalf("expected optional module_dir_empty items to be hidden, got %#v", item)
		}
	}
	if diagnostics.Diagnostics.Total != len(diagnostics.Items) {
		t.Fatalf("expected filtered severity counts to match filtered items, got counts=%#v items=%#v", diagnostics.Diagnostics, diagnostics.Items)
	}

	verboseRequest := httptest.NewRequest(http.MethodGet, "/api/config/diagnostics?verbose=0", nil)
	verboseResponse := httptest.NewRecorder()
	server.ServeHTTP(verboseResponse, verboseRequest)
	if strings.Contains(verboseResponse.Body.String(), `"code":"module_dir_empty"`) {
		t.Fatalf("expected verbose=0 to hide optional module_dir_empty items, got %s", verboseResponse.Body.String())
	}
}

func TestServerConfigDiagnosticsTreatsCommandLikeWorkflowToolAsOptionalHint(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	writeAPITestConfig(t, runtimeRef.RuntimeHome())
	writeAPIWorkflowGraph(t, runtimeRef.RuntimeHome(), "command-hint-flow", `
name: command-hint-flow
stages:
  - name: start
    node_type: start
    next: [verify]
  - name: verify
    node_type: skill
    agent: auditor
    skill: code-audit
    tool: go test ./...
    next: [end]
  - name: end
    node_type: end
`)
	writeAPIWorkflowGraph(t, runtimeRef.RuntimeHome(), "unknown-tool-flow", `
name: unknown-tool-flow
stages:
  - name: start
    node_type: start
    next: [verify]
  - name: verify
    node_type: tool
    agent: auditor
    skill: code-audit
    tool: missing_tool_metadata
    next: [end]
  - name: end
    node_type: end
`)
	server := NewServer(runtimeRef)
	request := httptest.NewRequest(http.MethodGet, "/api/config/diagnostics?include_optional=0", nil)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected diagnostics 200, got %d body=%s", response.Code, response.Body.String())
	}
	var diagnostics configDiagnosticsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &diagnostics); err != nil {
		t.Fatalf("decode diagnostics response: %v", err)
	}
	if configDiagnosticCodeForTargetExists(diagnostics.Items, "workflow_tool_unseen", "command-hint-flow") ||
		configDiagnosticCodeForTargetExists(diagnostics.Items, "workflow_tool_command_hint", "command-hint-flow") {
		t.Fatalf("expected command-like workflow tool hint to be hidden from include_optional=0 diagnostics, got %#v", diagnostics.Items)
	}
	assertConfigDiagnosticTarget(t, diagnostics.Items, "workflow_tool_unseen", "workflow", "unknown-tool-flow", "tool", "discovered Tool metadata id")

	verboseRequest := httptest.NewRequest(http.MethodGet, "/api/config/diagnostics", nil)
	verboseResponse := httptest.NewRecorder()
	server.ServeHTTP(verboseResponse, verboseRequest)
	var verboseDiagnostics configDiagnosticsResponse
	if err := json.Unmarshal(verboseResponse.Body.Bytes(), &verboseDiagnostics); err != nil {
		t.Fatalf("decode verbose diagnostics response: %v", err)
	}
	hint := configDiagnosticByCodeAndTarget(verboseDiagnostics.Items, "workflow_tool_command_hint", "command-hint-flow")
	if hint == nil || !hint.Optional || hint.Actionable == nil || *hint.Actionable || hint.Category != "optional" {
		t.Fatalf("expected command-like workflow tool to be optional non-actionable in verbose diagnostics, got %#v", verboseDiagnostics.Items)
	}
}

func TestServerConfigDiagnosticsReportsToolRiskPolicyState(t *testing.T) {
	cases := []struct {
		name      string
		policyYML string
		want      []struct {
			code           string
			field          string
			recommendation string
		}
	}{
		{
			name:      "disabled",
			policyYML: "",
			want: []struct {
				code           string
				field          string
				recommendation string
			}{
				{code: "tool_risk_policy_disabled", field: "tool_risk_policy", recommendation: "untrusted MCP servers"},
			},
		},
		{
			name: "require approval but remember still allowed",
			policyYML: `tool_risk_policy:
  require_approval_for_unsandboxed_risky_tools: true
`,
			want: []struct {
				code           string
				field          string
				recommendation string
			}{
				{code: "tool_risk_policy_require_approval", field: "require_approval_for_unsandboxed_risky_tools", recommendation: "recognized_sandbox_boundaries"},
				{code: "tool_risk_policy_remember_allowed", field: "disable_remember_for_unsandboxed_risky_tools", recommendation: "approve-and-remember"},
			},
		},
		{
			name: "strict",
			policyYML: `tool_risk_policy:
  require_approval_for_unsandboxed_risky_tools: true
  disable_remember_for_unsandboxed_risky_tools: true
  reject_unsandboxed_risky_tools: true
`,
			want: []struct {
				code           string
				field          string
				recommendation string
			}{
				{code: "tool_risk_policy_require_approval", field: "require_approval_for_unsandboxed_risky_tools", recommendation: "recognized_sandbox_boundaries"},
				{code: "tool_risk_policy_disable_remember", field: "disable_remember_for_unsandboxed_risky_tools", recommendation: "remembered approvals"},
				{code: "tool_risk_policy_reject_unsandboxed", field: "reject_unsandboxed_risky_tools", recommendation: "linux_netns"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runtimeRef := newAPITestRuntime(t)
			configDir := filepath.Join(runtimeRef.RuntimeHome(), "configs")
			if err := os.MkdirAll(configDir, 0o755); err != nil {
				t.Fatalf("mkdir config dir: %v", err)
			}
			content := `agent:
  name: GoFlow Agent
  max_iterations: 8
default_agent: chat
providers:
  primary:
    provider: openai-compatible
    base_url: http://localhost:9999/v1
    model: test-model
agents:
  chat:
    name: Chat
    provider: primary
    mode: chat
    max_iterations: 2
    allowed_tool_kinds: [read]
    tool_policy: confirm
skill:
  directory: ./skills
` + tc.policyYML
			if err := os.WriteFile(filepath.Join(configDir, "goflow.yaml"), []byte(content), 0o644); err != nil {
				t.Fatalf("write config: %v", err)
			}
			server := NewServer(runtimeRef)
			request := httptest.NewRequest(http.MethodGet, "/api/config/diagnostics?include_optional=0", nil)
			response := httptest.NewRecorder()

			server.ServeHTTP(response, request)

			if response.Code != http.StatusOK {
				t.Fatalf("expected diagnostics 200, got %d body=%s", response.Code, response.Body.String())
			}
			var diagnostics configDiagnosticsResponse
			if err := json.Unmarshal(response.Body.Bytes(), &diagnostics); err != nil {
				t.Fatalf("decode diagnostics response: %v", err)
			}
			for _, want := range tc.want {
				assertConfigDiagnosticTarget(t, diagnostics.Items, want.code, "tool_risk_policy", "tool_risk_policy", want.field, want.recommendation)
			}
		})
	}
}

func TestServerConfigDiagnosticsReportsToolRiskPolicyMCPBoundaryImpact(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	configDir := filepath.Join(runtimeRef.RuntimeHome(), "configs")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	networkServerConfig := ""
	if goruntime.GOOS == "linux" {
		networkServerConfig = `  - name: network-helper
    command: python
    args: [./mcp_servers/network.py]
    enabled: true
    allowed_commands: [python]
    isolation: linux_netns
`
	}
	if err := os.WriteFile(filepath.Join(configDir, "goflow.yaml"), []byte(`agent:
  name: GoFlow Agent
  max_iterations: 8
default_agent: chat
providers:
  primary:
    provider: openai-compatible
    base_url: http://localhost:9999/v1
    model: test-model
agents:
  chat:
    name: Chat
    provider: primary
    mode: chat
    max_iterations: 2
    allowed_tool_kinds: [read, network]
    tool_policy: confirm
skill:
  directory: ./skills
tool_risk_policy:
  require_approval_for_unsandboxed_risky_tools: true
  disable_remember_for_unsandboxed_risky_tools: true
  reject_unsandboxed_risky_tools: true
mcp_servers:
  - name: host-helper
    command: python
    args: [./mcp_servers/host.py]
    enabled: true
    allowed_commands: [python]
    isolation: process_group
`+networkServerConfig+`
  - name: container-helper
    command: python
    args: [./mcp_servers/container.py]
    enabled: true
    allowed_commands: [python]
    isolation: container
    network_disabled: true
    isolation_options:
      image: python:3.13-slim
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	server := NewServer(runtimeRef)
	request := httptest.NewRequest(http.MethodGet, "/api/config/diagnostics?include_optional=0", nil)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected diagnostics 200, got %d body=%s", response.Code, response.Body.String())
	}
	var diagnostics configDiagnosticsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &diagnostics); err != nil {
		t.Fatalf("decode diagnostics response: %v", err)
	}
	assertConfigDiagnosticTarget(t, diagnostics.Items, "tool_risk_policy_mcp_unsandboxed", "mcp_server", "host-helper", "isolation", "reject_unsandboxed_risky_tools")
	if goruntime.GOOS == "linux" {
		assertConfigDiagnosticTarget(t, diagnostics.Items, "tool_risk_policy_mcp_network_sandbox", "mcp_server", "network-helper", "isolation", "network-kind tools only")
	}
	assertConfigDiagnosticTarget(t, diagnostics.Items, "tool_risk_policy_mcp_broad_sandbox", "mcp_server", "container-helper", "isolation", "container boundary")
}

func TestServerConfigDiagnosticsReportsProviderAgentQualityIssues(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	configDir := filepath.Join(runtimeRef.RuntimeHome(), "configs")
	if err := os.MkdirAll(filepath.Join(configDir, "mcp_servers"), 0o755); err != nil {
		t.Fatalf("mkdir config modules: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "goflow.yaml"), []byte(`agent:
  name: GoFlow Agent
  max_iterations: 8
default_agent: chat
providers:
  primary:
    provider: openai-compatible
    base_url: http://localhost:9999/v1
    model: test-model
    fallback_provider: backup
  backup:
    provider: openai-compatible
    base_url: http://localhost:9998/v1
    model: backup-model
    fallback_provider: primary
agents:
  chat:
    name: Chat
    provider: primary
    mode: chat
    max_iterations: 2
    allowed_tool_kinds: [read, network]
    allowed_tools: [missing_tools/fetch_url]
    tool_policy: allow
skill:
  directory: ./skills
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "mcp_servers", "web-tools.yaml"), []byte(`mcp_servers:
  - name: web_tools
    command: python
    args: [./mcp_servers/web.py]
    enabled: true
    allowed_commands: [python]
    isolation: none
`), 0o644); err != nil {
		t.Fatalf("write mcp module: %v", err)
	}
	server := NewServer(runtimeRef)
	request := httptest.NewRequest(http.MethodGet, "/api/config/diagnostics", nil)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected diagnostics 200, got %d body=%s", response.Code, response.Body.String())
	}
	var diagnostics configDiagnosticsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &diagnostics); err != nil {
		t.Fatalf("decode diagnostics response: %v", err)
	}
	assertConfigDiagnosticTarget(t, diagnostics.Items, "provider_fallback_cycle", "provider", "backup", "fallback_provider", "break the fallback cycle")
	assertConfigDiagnosticTarget(t, diagnostics.Items, "agent_risky_tool_policy_allow", "agent", "chat", "tool_policy", "tool_policy: confirm")
	assertConfigDiagnosticTarget(t, diagnostics.Items, "agent_allowed_tool_unseen", "agent", "chat", "allowed_tools", "qualified server/tool")
}

func TestServerConfigDiagnosticsTargetsCommonConfigLoadFailures(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	configDir := filepath.Join(runtimeRef.RuntimeHome(), "configs")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "goflow.yaml"), []byte(`agent:
  name: GoFlow Agent
  max_iterations: 8
default_agent: missing-chat
providers:
  primary:
    provider: openai-compatible
    base_url: http://localhost:9999/v1
    model: test-model
agents:
  chat:
    name: Chat
    provider: primary
    mode: chat
    max_iterations: 2
    allowed_tool_kinds: [read]
    tool_policy: confirm
skill:
  directory: ./skills
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	server := NewServer(runtimeRef)
	request := httptest.NewRequest(http.MethodGet, "/api/config/diagnostics", nil)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected diagnostics 200, got %d body=%s", response.Code, response.Body.String())
	}
	var diagnostics configDiagnosticsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &diagnostics); err != nil {
		t.Fatalf("decode diagnostics response: %v", err)
	}
	if diagnostics.Status != "error" || diagnostics.Diagnostics.Errors == 0 {
		t.Fatalf("expected error diagnostics, got %#v", diagnostics)
	}
	assertConfigDiagnosticTarget(t, diagnostics.Items, "default_agent_missing", "agent", "missing-chat", "default_agent", "referenced agent")
}

func TestServerConfigDiagnosticsTargetsMCPContainerLoadFailures(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	configDir := filepath.Join(runtimeRef.RuntimeHome(), "configs")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "goflow.yaml"), []byte(`agent:
  name: GoFlow Agent
  max_iterations: 8
default_agent: chat
providers:
  primary:
    provider: openai-compatible
    base_url: http://localhost:9999/v1
    model: test-model
agents:
  chat:
    name: Chat
    provider: primary
    mode: chat
    max_iterations: 2
    allowed_tool_kinds: [read]
    tool_policy: confirm
skill:
  directory: ./skills
mcp_servers:
  - name: broken-container
    command: python
    args: [./mcp_servers/broken.py]
    enabled: true
    allowed_commands: [python]
    isolation: container
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	server := NewServer(runtimeRef)
	request := httptest.NewRequest(http.MethodGet, "/api/config/diagnostics", nil)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected diagnostics 200, got %d body=%s", response.Code, response.Body.String())
	}
	var diagnostics configDiagnosticsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &diagnostics); err != nil {
		t.Fatalf("decode diagnostics response: %v", err)
	}
	assertConfigDiagnosticTarget(t, diagnostics.Items, "mcp_container_image_missing", "mcp_server", "broken-container", "isolation_options.image", "trusted")
}

func TestServerConfigDiagnosticsTargetsAdditionalConfigLoadFailures(t *testing.T) {
	cases := []struct {
		name           string
		config         string
		code           string
		status         string
		targetKind     string
		targetName     string
		field          string
		recommendation string
	}{
		{
			name: "provider model required",
			config: `agent:
  name: GoFlow Agent
  max_iterations: 8
default_agent: chat
providers:
  primary:
    provider: openai-compatible
    base_url: http://localhost:9999/v1
agents:
  chat:
    name: Chat
    provider: primary
    mode: chat
    max_iterations: 2
    allowed_tool_kinds: [read]
    tool_policy: confirm
skill:
  directory: ./skills
`,
			code:           "provider_model_required",
			status:         "warning",
			targetKind:     "provider",
			targetName:     "primary",
			field:          "model",
			recommendation: "model id",
		},
		{
			name: "provider base url required",
			config: `agent:
  name: GoFlow Agent
  max_iterations: 8
default_agent: chat
providers:
  primary:
    provider: openai-compatible
    model: test-model
agents:
  chat:
    name: Chat
    provider: primary
    mode: chat
    max_iterations: 2
    allowed_tool_kinds: [read]
    tool_policy: confirm
skill:
  directory: ./skills
`,
			code:           "provider_base_url_required",
			status:         "warning",
			targetKind:     "provider",
			targetName:     "primary",
			field:          "base_url",
			recommendation: "base_url",
		},
		{
			name: "agent provider missing",
			config: `agent:
  name: GoFlow Agent
  max_iterations: 8
default_agent: chat
providers:
  primary:
    provider: openai-compatible
    base_url: http://localhost:9999/v1
    model: test-model
agents:
  chat:
    name: Chat
    provider: missing-provider
    mode: chat
    max_iterations: 2
    allowed_tool_kinds: [read]
    tool_policy: confirm
skill:
  directory: ./skills
`,
			code:           "agent_provider_missing",
			targetKind:     "agent",
			targetName:     "chat",
			field:          "provider",
			recommendation: "provider module",
		},
		{
			name: "verifier provider missing",
			config: `agent:
  name: GoFlow Agent
  max_iterations: 8
default_agent: chat
providers:
  primary:
    provider: openai-compatible
    base_url: http://localhost:9999/v1
    model: test-model
agents:
  chat:
    name: Chat
    provider: primary
    mode: chat
    max_iterations: 2
    allowed_tool_kinds: [read]
    tool_policy: confirm
skill:
  directory: ./skills
verifier:
  enabled: true
  agent: chat
  provider: missing-provider
  modes: [fix]
`,
			code:           "verifier_provider_missing",
			targetKind:     "verifier",
			targetName:     "missing-provider",
			field:          "provider",
			recommendation: "verifier provider",
		},
		{
			name: "cost router provider missing",
			config: `agent:
  name: GoFlow Agent
  max_iterations: 8
default_agent: chat
providers:
  primary:
    provider: openai-compatible
    base_url: http://localhost:9999/v1
    model: test-model
agents:
  chat:
    name: Chat
    provider: primary
    mode: chat
    max_iterations: 2
    allowed_tool_kinds: [read]
    tool_policy: confirm
skill:
  directory: ./skills
cost_control:
  router:
    enabled: true
    provider: missing-cheap
`,
			code:           "auxiliary_provider_missing",
			targetKind:     "cost_control",
			targetName:     "cost_control.router",
			field:          "provider",
			recommendation: "low-cost provider",
		},
		{
			name: "mcp command allowlist missing",
			config: `agent:
  name: GoFlow Agent
  max_iterations: 8
default_agent: chat
providers:
  primary:
    provider: openai-compatible
    base_url: http://localhost:9999/v1
    model: test-model
agents:
  chat:
    name: Chat
    provider: primary
    mode: chat
    max_iterations: 2
    allowed_tool_kinds: [read]
    tool_policy: confirm
skill:
  directory: ./skills
mcp_servers:
  - name: unsafe-tool
    command: python
    args: [./mcp_servers/tool.py]
    enabled: true
`,
			code:           "mcp_command_allowlist_missing",
			targetKind:     "mcp_server",
			targetName:     "unsafe-tool",
			field:          "allowed_commands",
			recommendation: "allowed_commands",
		},
		{
			name: "mcp command not allowed",
			config: `agent:
  name: GoFlow Agent
  max_iterations: 8
default_agent: chat
providers:
  primary:
    provider: openai-compatible
    base_url: http://localhost:9999/v1
    model: test-model
agents:
  chat:
    name: Chat
    provider: primary
    mode: chat
    max_iterations: 2
    allowed_tool_kinds: [read]
    tool_policy: confirm
skill:
  directory: ./skills
mcp_servers:
  - name: blocked-tool
    command: python
    args: [./mcp_servers/tool.py]
    enabled: true
    allowed_commands: [node]
`,
			code:           "mcp_command_not_allowed",
			targetKind:     "mcp_server",
			targetName:     "blocked-tool",
			field:          "allowed_commands",
			recommendation: "exact command",
		},
		{
			name: "mcp container network conflict",
			config: `agent:
  name: GoFlow Agent
  max_iterations: 8
default_agent: chat
providers:
  primary:
    provider: openai-compatible
    base_url: http://localhost:9999/v1
    model: test-model
agents:
  chat:
    name: Chat
    provider: primary
    mode: chat
    max_iterations: 2
    allowed_tool_kinds: [read]
    tool_policy: confirm
skill:
  directory: ./skills
mcp_servers:
  - name: networked-container
    command: python
    args: [./mcp_servers/tool.py]
    enabled: true
    allowed_commands: [python]
    network_disabled: true
    isolation: container
    isolation_options:
      image: python:3.13-slim
      network: host
`,
			code:           "mcp_container_network_conflict",
			targetKind:     "mcp_server",
			targetName:     "networked-container",
			field:          "isolation_options.network",
			recommendation: "disabled/none",
		},
		{
			name: "mcp container option invalid",
			config: `agent:
  name: GoFlow Agent
  max_iterations: 8
default_agent: chat
providers:
  primary:
    provider: openai-compatible
    base_url: http://localhost:9999/v1
    model: test-model
agents:
  chat:
    name: Chat
    provider: primary
    mode: chat
    max_iterations: 2
    allowed_tool_kinds: [read]
    tool_policy: confirm
skill:
  directory: ./skills
mcp_servers:
  - name: bad-container
    command: python
    args: [./mcp_servers/tool.py]
    enabled: true
    allowed_commands: [python]
    isolation: container
    isolation_options:
      image: python:3.13-slim
      workspace_mount: maybe
`,
			code:           "mcp_container_option_invalid",
			targetKind:     "mcp_server",
			targetName:     "bad-container",
			field:          "isolation_options.workspace_mount",
			recommendation: "isolation_options",
		},
		{
			name: "mcp container memory invalid",
			config: `agent:
  name: GoFlow Agent
  max_iterations: 8
default_agent: chat
providers:
  primary:
    provider: openai-compatible
    base_url: http://localhost:9999/v1
    model: test-model
agents:
  chat:
    name: Chat
    provider: primary
    mode: chat
    max_iterations: 2
    allowed_tool_kinds: [read]
    tool_policy: confirm
skill:
  directory: ./skills
mcp_servers:
  - name: bad-container
    command: python
    args: [./mcp_servers/tool.py]
    enabled: true
    allowed_commands: [python]
    isolation: container
    isolation_options:
      image: python:3.13-slim
      memory: many
`,
			code:           "mcp_container_option_invalid",
			targetKind:     "mcp_server",
			targetName:     "bad-container",
			field:          "isolation_options.memory",
			recommendation: "isolation_options",
		},
		{
			name: "mcp container user invalid",
			config: `agent:
  name: GoFlow Agent
  max_iterations: 8
default_agent: chat
providers:
  primary:
    provider: openai-compatible
    base_url: http://localhost:9999/v1
    model: test-model
agents:
  chat:
    name: Chat
    provider: primary
    mode: chat
    max_iterations: 2
    allowed_tool_kinds: [read]
    tool_policy: confirm
skill:
  directory: ./skills
mcp_servers:
  - name: bad-container
    command: python
    args: [./mcp_servers/tool.py]
    enabled: true
    allowed_commands: [python]
    isolation: container
    isolation_options:
      image: python:3.13-slim
      user: "app user"
`,
			code:           "mcp_container_option_invalid",
			targetKind:     "mcp_server",
			targetName:     "bad-container",
			field:          "isolation_options.user",
			recommendation: "isolation_options",
		},
		{
			name: "mcp container cap drop invalid",
			config: `agent:
  name: GoFlow Agent
  max_iterations: 8
default_agent: chat
providers:
  primary:
    provider: openai-compatible
    base_url: http://localhost:9999/v1
    model: test-model
agents:
  chat:
    name: Chat
    provider: primary
    mode: chat
    max_iterations: 2
    allowed_tool_kinds: [read]
    tool_policy: confirm
skill:
  directory: ./skills
mcp_servers:
  - name: bad-container
    command: python
    args: [./mcp_servers/tool.py]
    enabled: true
    allowed_commands: [python]
    isolation: container
    isolation_options:
      image: python:3.13-slim
      cap_drop: NET/ADMIN
`,
			code:           "mcp_container_option_invalid",
			targetKind:     "mcp_server",
			targetName:     "bad-container",
			field:          "isolation_options.cap_drop",
			recommendation: "isolation_options",
		},
		{
			name: "mcp isolation invalid",
			config: `agent:
  name: GoFlow Agent
  max_iterations: 8
default_agent: chat
providers:
  primary:
    provider: openai-compatible
    base_url: http://localhost:9999/v1
    model: test-model
agents:
  chat:
    name: Chat
    provider: primary
    mode: chat
    max_iterations: 2
    allowed_tool_kinds: [read]
    tool_policy: confirm
skill:
  directory: ./skills
mcp_servers:
  - name: odd-isolation
    command: python
    args: [./mcp_servers/tool.py]
    enabled: true
    allowed_commands: [python]
    isolation: vm
`,
			code:           "mcp_isolation_invalid",
			targetKind:     "mcp_server",
			targetName:     "odd-isolation",
			field:          "isolation",
			recommendation: "container",
		},
		{
			name: "mcp windows appcontainer unimplemented",
			config: `agent:
  name: GoFlow Agent
  max_iterations: 8
default_agent: chat
providers:
  primary:
    provider: openai-compatible
    base_url: http://localhost:9999/v1
    model: test-model
agents:
  chat:
    name: Chat
    provider: primary
    mode: chat
    max_iterations: 2
    allowed_tool_kinds: [read]
    tool_policy: confirm
skill:
  directory: ./skills
mcp_servers:
  - name: future-win-sandbox
    command: python
    args: [./mcp_servers/tool.py]
    enabled: true
    allowed_commands: [python]
    isolation: windows_appcontainer
`,
			code:           "mcp_windows_appcontainer_unimplemented",
			targetKind:     "mcp_server",
			targetName:     "future-win-sandbox",
			field:          "isolation",
			recommendation: "AppContainer-like values are intentionally rejected",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runtimeRef := newAPITestRuntime(t)
			configDir := filepath.Join(runtimeRef.RuntimeHome(), "configs")
			if err := os.MkdirAll(configDir, 0o755); err != nil {
				t.Fatalf("mkdir config dir: %v", err)
			}
			if err := os.WriteFile(filepath.Join(configDir, "goflow.yaml"), []byte(tc.config), 0o644); err != nil {
				t.Fatalf("write config: %v", err)
			}
			server := NewServer(runtimeRef)
			request := httptest.NewRequest(http.MethodGet, "/api/config/diagnostics", nil)
			response := httptest.NewRecorder()

			server.ServeHTTP(response, request)

			if response.Code != http.StatusOK {
				t.Fatalf("expected diagnostics 200, got %d body=%s", response.Code, response.Body.String())
			}
			var diagnostics configDiagnosticsResponse
			if err := json.Unmarshal(response.Body.Bytes(), &diagnostics); err != nil {
				t.Fatalf("decode diagnostics response: %v", err)
			}
			expectedStatus := tc.status
			if expectedStatus == "" {
				expectedStatus = "error"
			}
			if diagnostics.Status != expectedStatus {
				t.Fatalf("expected %s diagnostics, got %#v", expectedStatus, diagnostics)
			}
			if expectedStatus == "error" && diagnostics.Diagnostics.Errors == 0 {
				t.Fatalf("expected error diagnostics, got %#v", diagnostics)
			}
			if expectedStatus == "warning" && diagnostics.Diagnostics.Warnings == 0 {
				t.Fatalf("expected warning diagnostics, got %#v", diagnostics)
			}
			assertConfigDiagnosticTarget(t, diagnostics.Items, tc.code, tc.targetKind, tc.targetName, tc.field, tc.recommendation)
		})
	}
}

func TestServerConfigDiagnosticsWarnsForIncompleteProviderSetup(t *testing.T) {
	runtimeHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(runtimeHome, "configs"), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	configPath := filepath.Join(runtimeHome, "configs", "goflow.yaml")
	if err := os.WriteFile(configPath, []byte(`agent:
  name: GoFlow Agent
  max_iterations: 8
default_agent: chat
providers:
  primary:
    provider: openai-compatible
agents:
  chat:
    name: Chat
    provider: primary
    mode: chat
    max_iterations: 2
    allowed_tool_kinds: [read]
    tool_policy: confirm
skill:
  directory: ./skills
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	runtimeRef, err := agent.NewRuntime(&config.Config{
		RuntimeHome:   runtimeHome,
		WorkspaceRoot: t.TempDir(),
		ConfigPath:    configPath,
		Session:       config.SessionConfig{MaxHistory: 8},
		DefaultAgent:  "chat",
		Providers:     map[string]config.LLMConfig{"primary": {Provider: "openai-compatible"}},
		Agents:        map[string]config.AgentProfile{"chat": {Name: "Chat", Provider: "primary", Mode: "chat", Model: "", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead}, ToolPolicy: config.ToolPolicyConfirm}},
	}, map[string]interfaces.LLMClient{"primary": &apiTestLLM{}}, apiTestSkillManager{}, &apiTestMCP{}, session.New(8), runtime.NewAuditLogger(false, false))
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	server := NewServer(runtimeRef)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/config/diagnostics", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("expected diagnostics 200, got %d body=%s", response.Code, response.Body.String())
	}
	var diagnostics configDiagnosticsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &diagnostics); err != nil {
		t.Fatalf("decode diagnostics: %v", err)
	}
	if diagnostics.Status != "warning" || diagnostics.Diagnostics.Warnings < 3 {
		t.Fatalf("expected provider setup warnings, got %#v", diagnostics)
	}
	assertConfigDiagnosticTarget(t, diagnostics.Items, "provider_base_url_required", "provider", "primary", "base_url", "OpenAI-compatible")
	assertConfigDiagnosticTarget(t, diagnostics.Items, "provider_model_required", "provider", "primary", "model", "model id")
	assertConfigDiagnosticTarget(t, diagnostics.Items, "provider_api_key_missing", "provider", "primary", "api_key", "Web Studio")
}

func configDiagnosticRecommendationContains(items []configDiagnosticItem, code, want string) bool {
	for _, item := range items {
		if item.Code == code && strings.Contains(item.Recommendation, want) {
			return true
		}
	}
	return false
}

func configDiagnosticsStringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func writeAPITestConfig(t *testing.T, runtimeHome string) {
	t.Helper()
	configDir := filepath.Join(runtimeHome, "configs")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "goflow.yaml"), []byte(`agent:
  name: GoFlow Agent
  max_iterations: 8
default_agent: chat
providers:
  primary:
    provider: openai-compatible
    base_url: http://localhost:9999/v1
    model: test-model
agents:
  chat:
    name: Chat
    provider: primary
    mode: chat
    max_iterations: 2
    allowed_tool_kinds: [read]
    tool_policy: allow
  planner:
    name: Planner
    provider: primary
    mode: plan
    max_iterations: 2
    allowed_tool_kinds: [read]
    tool_policy: allow
  fixer:
    name: Fixer
    provider: primary
    mode: fix
    max_iterations: 2
    allowed_tool_kinds: [read, write]
    tool_policy: confirm
  auditor:
    name: Auditor
    provider: primary
    mode: audit
    max_iterations: 2
    allowed_tool_kinds: [read]
    tool_policy: allow
skill:
  directory: ./skills
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func writeAPIWorkflowGraph(t *testing.T, runtimeHome, name, content string) {
	t.Helper()
	dir := filepath.Join(runtimeHome, "workflows", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir workflow graph: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "workflow.yaml"), []byte(strings.TrimSpace(content)+"\n"), 0o644); err != nil {
		t.Fatalf("write workflow graph: %v", err)
	}
}

func toolRiskPolicyBoundaryExists(items []toolRiskPolicySandboxBoundary, isolation, scope, kind string) bool {
	for _, item := range items {
		if item.Isolation != isolation || item.Scope != scope {
			continue
		}
		if kind == "" || configDiagnosticsStringSliceContains(item.AppliesToKinds, kind) {
			return true
		}
	}
	return false
}

func assertOptionalModuleDirDiagnostic(t *testing.T, items []configDiagnosticItem, moduleKind string) {
	t.Helper()
	for _, item := range items {
		if item.Code != "module_dir_empty" || item.TargetName != moduleKind {
			continue
		}
		if item.Category != "optional" || !item.Optional || item.Actionable == nil || *item.Actionable {
			t.Fatalf("expected optional non-actionable module_dir_empty for %s, got %#v", moduleKind, item)
		}
		if !strings.Contains(item.Recommendation, "no action is required") {
			t.Fatalf("expected optional module_dir_empty recommendation, got %#v", item)
		}
		return
	}
	t.Fatalf("expected optional module_dir_empty diagnostic for %s, got %#v", moduleKind, items)
}

func TestServerConfigDiagnosticsReportsImplicitMinimalMCPEnv(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	configDir := filepath.Join(runtimeRef.RuntimeHome(), "configs")
	if err := os.MkdirAll(filepath.Join(configDir, "mcp_servers"), 0o755); err != nil {
		t.Fatalf("mkdir config modules: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "goflow.yaml"), []byte(`agent:
  name: GoFlow Agent
  max_iterations: 8
default_agent: chat
providers:
  primary:
    provider: openai-compatible
    base_url: http://localhost:9999/v1
    model: test-model
agents:
  chat:
    name: Chat
    provider: primary
    mode: chat
    max_iterations: 2
    allowed_tool_kinds: [read]
    tool_policy: allow
skill:
  directory: ./skills
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "mcp_servers", "minimal-env.yaml"), []byte(`mcp_servers:
  - name: minimal-env
    command: python
    args: [./mcp_servers/minimal.py]
    enabled: true
    allowed_commands: [python]
    isolation: none
`), 0o644); err != nil {
		t.Fatalf("write mcp module: %v", err)
	}
	server := NewServer(runtimeRef)
	request := httptest.NewRequest(http.MethodGet, "/api/config/diagnostics", nil)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected diagnostics 200, got %d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, `"code":"mcp_env_allowlist_implicit_minimal"`) ||
		!strings.Contains(body, `"target_kind":"mcp_server"`) ||
		!strings.Contains(body, `"target_name":"minimal-env"`) ||
		!strings.Contains(body, `"field":"env_allowlist"`) ||
		!strings.Contains(body, `"recommendation"`) ||
		!strings.Contains(body, `GOFLOW_WORKSPACE_ROOT`) {
		t.Fatalf("expected implicit minimal env diagnostic, got %s", body)
	}
}

func TestConfigDiagnosticsReportsWindowsJobLifecycleOnly(t *testing.T) {
	var diagnostics configDiagnosticsResponse
	diagnostics.checkMCPDiagnostics(&config.Config{MCP: []config.MCPServerRef{{
		Name:            "win-tools",
		Command:         "python",
		Args:            []string{"server.py"},
		Enabled:         true,
		NetworkDisabled: true,
		Isolation:       "windows_job",
		AllowedCommands: []string{"python"},
	}}})
	diagnostics.finalize()

	assertConfigDiagnostic(t, diagnostics.Items, "mcp_isolation_lifecycle_only", "win-tools", "isolation", "container")
	assertConfigDiagnostic(t, diagnostics.Items, "mcp_windows_job_lifecycle_only", "win-tools", "isolation", "container")
	assertConfigDiagnostic(t, diagnostics.Items, "mcp_network_disabled_advisory", "win-tools", "network_disabled", "linux_netns")
}

func TestConfigDiagnosticsReportsContainerRootUser(t *testing.T) {
	var diagnostics configDiagnosticsResponse
	diagnostics.checkMCPDiagnostics(&config.Config{MCP: []config.MCPServerRef{{
		Name:      "root-container",
		Command:   "python",
		Args:      []string{"server.py"},
		Enabled:   true,
		Isolation: "container",
		IsolationOptions: map[string]string{
			"image":           "python:3.13-slim",
			"workspace_mount": "ro",
			"network":         "disabled",
			"memory":          "256m",
			"memory_swap":     "256m",
			"cpus":            "0.5",
			"pids_limit":      "64",
			"user":            "root:root",
		},
		AllowedCommands: []string{"python"},
	}}})
	diagnostics.finalize()

	assertConfigDiagnostic(t, diagnostics.Items, "mcp_container_user_root", "root-container", "isolation_options.user", "non-root")
	if configDiagnosticCodeForTargetExists(diagnostics.Items, "mcp_container_user_missing", "root-container") {
		t.Fatalf("expected explicit root user to report root-user diagnostic, not missing-user diagnostic: %#v", diagnostics.Items)
	}
}

func TestContainerUserLooksNonRoot(t *testing.T) {
	tests := []struct {
		name string
		user string
		want bool
	}{
		{name: "empty", user: "", want: false},
		{name: "root name", user: "root", want: false},
		{name: "root name with group", user: "root:root", want: false},
		{name: "root name with numeric group", user: "root:1000", want: false},
		{name: "zero uid", user: "0", want: false},
		{name: "zero uid group", user: "0:0", want: false},
		{name: "padded zero uid", user: "00:1000", want: false},
		{name: "numeric nonroot uid", user: "65532:65532", want: true},
		{name: "named nonroot user", user: "nobody", want: true},
		{name: "nonroot uid with root group", user: "1000:0", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := containerUserLooksNonRoot(tt.user); got != tt.want {
				t.Fatalf("containerUserLooksNonRoot(%q) = %v, want %v", tt.user, got, tt.want)
			}
		})
	}
}

func TestContainerResourceLimitsComplete(t *testing.T) {
	tests := []struct {
		name    string
		options map[string]string
		want    bool
	}{
		{name: "none", options: nil, want: false},
		{name: "memory only", options: map[string]string{"memory": "256m"}, want: false},
		{name: "memory and cpus", options: map[string]string{"memory": "256m", "memory_swap": "256m", "cpus": "0.5"}, want: false},
		{name: "complete", options: map[string]string{"memory": "256m", "memory_swap": "256m", "cpus": "0.5", "pids_limit": "64"}, want: true},
		{name: "blank value", options: map[string]string{"memory": "256m", "memory_swap": "256m", "cpus": " ", "pids_limit": "64"}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := containerResourceLimitsComplete(tt.options); got != tt.want {
				t.Fatalf("containerResourceLimitsComplete(%#v) = %v, want %v", tt.options, got, tt.want)
			}
		})
	}
}

func TestContainerCapDropIncludesAll(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "empty", value: "", want: false},
		{name: "all lowercase", value: "all", want: true},
		{name: "all uppercase", value: "ALL", want: true},
		{name: "csv contains all", value: "NET_ADMIN,all", want: true},
		{name: "semicolon contains all", value: "NET_ADMIN;ALL", want: true},
		{name: "substring small", value: "small", want: false},
		{name: "substring allow", value: "allow", want: false},
		{name: "cap name", value: "NET_ADMIN", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := containerCapDropIncludesAll(tt.value); got != tt.want {
				t.Fatalf("containerCapDropIncludesAll(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

func TestServerConfigDiagnosticsReportsContainerHardeningGaps(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	configDir := filepath.Join(runtimeRef.RuntimeHome(), "configs")
	if err := os.MkdirAll(filepath.Join(configDir, "mcp_servers"), 0o755); err != nil {
		t.Fatalf("mkdir config modules: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "goflow.yaml"), []byte(`agent:
  name: GoFlow Agent
  max_iterations: 8
default_agent: chat
providers:
  primary:
    provider: openai-compatible
    base_url: http://localhost:9999/v1
    model: test-model
agents:
  chat:
    name: Chat
    provider: primary
    mode: chat
    max_iterations: 2
    allowed_tool_kinds: [read]
    tool_policy: allow
skill:
  directory: ./skills
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	toolSource := filepath.ToSlash(filepath.Join(runtimeRef.RuntimeHome(), "mcp_servers", "loose.py"))
	if err := os.WriteFile(filepath.Join(configDir, "mcp_servers", "loose-container.yaml"), []byte(fmt.Sprintf(`mcp_servers:
  - name: loose-container
    command: bash
    args: ["-lc", "python ./mcp_servers/loose.py"]
    enabled: true
    allowed_commands: [bash]
    isolation: container
    isolation_options:
      image: python:latest
      network: host
      workspace_mount: rw
      tool_source: "%s"
      tool_mount: rw
`, toolSource)), 0o644); err != nil {
		t.Fatalf("write mcp module: %v", err)
	}
	server := NewServer(runtimeRef)
	request := httptest.NewRequest(http.MethodGet, "/api/config/diagnostics", nil)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected diagnostics 200, got %d body=%s", response.Code, response.Body.String())
	}
	var diagnostics configDiagnosticsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &diagnostics); err != nil {
		t.Fatalf("decode diagnostics response: %v", err)
	}
	assertConfigDiagnostic(t, diagnostics.Items, "mcp_allowed_command_shell", "loose-container", "allowed_commands", "dedicated executable")
	assertConfigDiagnostic(t, diagnostics.Items, "mcp_isolation_container_network_open", "loose-container", "isolation_options.network", "network")
	assertConfigDiagnostic(t, diagnostics.Items, "mcp_container_image_floating_tag", "loose-container", "isolation_options.image", "versioned tag")
	assertConfigDiagnostic(t, diagnostics.Items, "mcp_container_host_network", "loose-container", "isolation_options.network", "network: host")
	assertConfigDiagnostic(t, diagnostics.Items, "mcp_container_workspace_rw", "loose-container", "isolation_options.workspace_mount", "workspace_mount: ro")
	assertConfigDiagnostic(t, diagnostics.Items, "mcp_container_resource_limits_missing", "loose-container", "isolation_options", "memory")
	assertConfigDiagnostic(t, diagnostics.Items, "mcp_container_pull_policy_missing", "loose-container", "isolation_options.pull_policy", "pull_policy")
	assertConfigDiagnostic(t, diagnostics.Items, "mcp_container_no_new_privileges_missing", "loose-container", "isolation_options.no_new_privileges", "no_new_privileges")
	assertConfigDiagnostic(t, diagnostics.Items, "mcp_container_cap_drop_missing", "loose-container", "isolation_options.cap_drop", "cap_drop")
	assertConfigDiagnostic(t, diagnostics.Items, "mcp_container_readonly_rootfs_missing", "loose-container", "isolation_options.readonly_rootfs", "readonly_rootfs")
	assertConfigDiagnostic(t, diagnostics.Items, "mcp_container_ipc_not_isolated", "loose-container", "isolation_options.ipc", "ipc: none")
	assertConfigDiagnostic(t, diagnostics.Items, "mcp_container_userns_missing", "loose-container", "isolation_options.userns", "userns")
	assertConfigDiagnostic(t, diagnostics.Items, "mcp_container_tool_mount_incomplete", "loose-container", "isolation_options.tool_source", "tool_target")
	assertConfigDiagnostic(t, diagnostics.Items, "mcp_container_tool_mount_rw", "loose-container", "isolation_options.tool_mount", "tool_mount: ro")
}

func TestServerConfigDiagnosticsWarnsOnContainerImageWithoutDigestPin(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	configDir := filepath.Join(runtimeRef.RuntimeHome(), "configs")
	if err := os.MkdirAll(filepath.Join(configDir, "mcp_servers"), 0o755); err != nil {
		t.Fatalf("mkdir config modules: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "goflow.yaml"), []byte(`agent:
  name: GoFlow Agent
  max_iterations: 8
default_agent: chat
providers:
  primary:
    provider: openai-compatible
    base_url: http://localhost:9999/v1
    model: test-model
agents:
  chat:
    name: Chat
    provider: primary
    mode: chat
    max_iterations: 2
    allowed_tool_kinds: [read]
    tool_policy: allow
skill:
  directory: ./skills
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "mcp_servers", "versioned-container.yaml"), []byte(`mcp_servers:
  - name: versioned-container
    command: python
    args: [./mcp_servers/versioned.py]
    enabled: true
    allowed_commands: [python]
    isolation: container
    isolation_options:
      image: python:3.13-slim
      workspace_mount: ro
      network: disabled
      ipc: none
      userns: auto
      memory: 256m
      memory_swap: 256m
      cpus: "0.5"
      pids_limit: "64"
`), 0o644); err != nil {
		t.Fatalf("write mcp module: %v", err)
	}
	server := NewServer(runtimeRef)
	request := httptest.NewRequest(http.MethodGet, "/api/config/diagnostics", nil)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected diagnostics 200, got %d body=%s", response.Code, response.Body.String())
	}
	var diagnostics configDiagnosticsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &diagnostics); err != nil {
		t.Fatalf("decode diagnostics response: %v", err)
	}
	assertConfigDiagnostic(t, diagnostics.Items, "mcp_container_image_not_digest_pinned", "versioned-container", "isolation_options.image", "immutable digest")
	if configDiagnosticCodeForTargetExists(diagnostics.Items, "mcp_container_image_floating_tag", "versioned-container") {
		t.Fatalf("expected versioned container to avoid floating-tag warning, got %#v", diagnostics.Items)
	}
}

func TestServerConfigDiagnosticsReportsProductionContainerImageProvenance(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	configDir := filepath.Join(runtimeRef.RuntimeHome(), "configs")
	if err := os.MkdirAll(filepath.Join(configDir, "mcp_servers"), 0o755); err != nil {
		t.Fatalf("mkdir config modules: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "goflow.yaml"), []byte(`agent:
  name: GoFlow Agent
  max_iterations: 8
default_agent: chat
providers:
  primary:
    provider: openai-compatible
    base_url: http://localhost:9999/v1
    model: test-model
agents:
  chat:
    name: Chat
    provider: primary
    mode: chat
    max_iterations: 2
    allowed_tool_kinds: [read]
    tool_policy: allow
skill:
  directory: ./skills
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "mcp_servers", "prod-container.yaml"), []byte(`mcp_servers:
  - name: prod-container
    command: python
    args: [./mcp_servers/prod.py]
    enabled: true
    allowed_commands: [python]
    isolation: container
    isolation_profile: production
    isolation_options:
      image: goflow/mcp-prod:1.2.3
      pull_policy: missing
`), 0o644); err != nil {
		t.Fatalf("write mcp module: %v", err)
	}
	server := NewServer(runtimeRef)
	request := httptest.NewRequest(http.MethodGet, "/api/config/diagnostics", nil)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected diagnostics 200, got %d body=%s", response.Code, response.Body.String())
	}
	var diagnostics configDiagnosticsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &diagnostics); err != nil {
		t.Fatalf("decode diagnostics response: %v", err)
	}
	assertConfigDiagnostic(t, diagnostics.Items, "mcp_container_image_not_digest_pinned", "prod-container", "isolation_options.image", "immutable digest")
	assertConfigDiagnostic(t, diagnostics.Items, "mcp_container_production_image_not_digest_pinned", "prod-container", "isolation_options.image", "production-profile")
	assertConfigDiagnostic(t, diagnostics.Items, "mcp_container_production_pull_policy_not_never", "prod-container", "isolation_options.pull_policy", "pull_policy: never")
	item := configDiagnosticByCodeAndTarget(diagnostics.Items, "mcp_container_production_image_not_digest_pinned", "prod-container")
	if item == nil ||
		item.Details["image_reference_type"] != "version_tag" ||
		item.Details["digest_pinned"] != "false" ||
		item.Details["image"] != "goflow/mcp-prod:1.2.3" {
		t.Fatalf("expected production image provenance details, got %#v", item)
	}
}

func TestServerConfigDiagnosticsAcceptsHardenedContainerMCP(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	configDir := filepath.Join(runtimeRef.RuntimeHome(), "configs")
	if err := os.MkdirAll(filepath.Join(configDir, "mcp_servers"), 0o755); err != nil {
		t.Fatalf("mkdir config modules: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "goflow.yaml"), []byte(`agent:
  name: GoFlow Agent
  max_iterations: 8
default_agent: chat
providers:
  primary:
    provider: openai-compatible
    base_url: http://localhost:9999/v1
    model: test-model
agents:
  chat:
    name: Chat
    provider: primary
    mode: chat
    max_iterations: 2
    allowed_tool_kinds: [read]
    tool_policy: allow
skill:
  directory: ./skills
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "mcp_servers", "hardened-container.yaml"), []byte(`mcp_servers:
  - name: hardened-container
    command: python
    args: [./mcp_servers/hardened.py]
    enabled: true
    allowed_commands: [python]
    network_disabled: true
    isolation: container
    isolation_options:
      image: python@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
      workspace_mount: ro
      network: disabled
      ipc: none
      userns: auto
      memory: 256m
      memory_swap: 256m
      cpus: "0.5"
      pids_limit: "64"
      pull_policy: missing
      no_new_privileges: "true"
      cap_drop: all
      readonly_rootfs: "true"
      user: "65532:65532"
      tmpfs: /tmp:rw,noexec,nosuid,size=64m
      security_opt: seccomp=/etc/goflow/seccomp.json;apparmor=goflow-mcp
      init: "true"
`), 0o644); err != nil {
		t.Fatalf("write mcp module: %v", err)
	}
	server := NewServer(runtimeRef)
	request := httptest.NewRequest(http.MethodGet, "/api/config/diagnostics", nil)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected diagnostics 200, got %d body=%s", response.Code, response.Body.String())
	}
	var diagnostics configDiagnosticsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &diagnostics); err != nil {
		t.Fatalf("decode diagnostics response: %v", err)
	}
	for _, code := range []string{
		"mcp_isolation_container_network_open",
		"mcp_container_workspace_rw",
		"mcp_container_resource_limits_missing",
		"mcp_container_no_new_privileges_missing",
		"mcp_container_cap_drop_missing",
		"mcp_container_readonly_rootfs_missing",
		"mcp_container_image_floating_tag",
		"mcp_container_image_not_digest_pinned",
		"mcp_container_host_network",
		"mcp_container_tool_mount_incomplete",
		"mcp_container_tool_mount_rw",
		"mcp_container_tmpfs_missing",
		"mcp_container_init_missing",
		"mcp_container_ipc_not_isolated",
		"mcp_container_userns_missing",
		"mcp_container_pull_policy_missing",
	} {
		if configDiagnosticCodeForTargetExists(diagnostics.Items, code, "hardened-container") {
			t.Fatalf("expected hardened container to avoid %s, got %#v", code, diagnostics.Items)
		}
	}
	assertConfigDiagnostic(t, diagnostics.Items, "mcp_isolation_container", "hardened-container", "isolation", "image provenance")
}

func assertConfigDiagnostic(t *testing.T, items []configDiagnosticItem, code, targetName, field, recommendation string) {
	t.Helper()
	assertConfigDiagnosticTarget(t, items, code, "mcp_server", targetName, field, recommendation)
}

func assertConfigDiagnosticTarget(t *testing.T, items []configDiagnosticItem, code, targetKind, targetName, field, recommendation string) {
	t.Helper()
	for _, item := range items {
		if item.Code != code || item.TargetName != targetName {
			continue
		}
		if item.TargetKind != targetKind {
			t.Fatalf("expected %s target kind %s, got %#v", code, targetKind, item)
		}
		if item.Field != field {
			t.Fatalf("expected %s field %q, got %#v", code, field, item)
		}
		if recommendation != "" && !strings.Contains(item.Recommendation, recommendation) {
			t.Fatalf("expected %s recommendation to contain %q, got %#v", code, recommendation, item)
		}
		return
	}
	t.Fatalf("expected diagnostic %s for target %s, got %#v", code, targetName, items)
}

func configDiagnosticCodeForTargetExists(items []configDiagnosticItem, code, targetName string) bool {
	for _, item := range items {
		if item.Code == code && item.TargetName == targetName {
			return true
		}
	}
	return false
}

func configDiagnosticByCodeAndTarget(items []configDiagnosticItem, code, targetName string) *configDiagnosticItem {
	for i := range items {
		if items[i].Code == code && items[i].TargetName == targetName {
			return &items[i]
		}
	}
	return nil
}

func TestServerConfigDiagnosticsReportsLinuxNetworkNamespaceIsolation(t *testing.T) {
	if goruntime.GOOS != "linux" {
		t.Skip("linux_netns isolation is Linux-only")
	}
	runtimeRef := newAPITestRuntime(t)
	configDir := filepath.Join(runtimeRef.RuntimeHome(), "configs")
	if err := os.MkdirAll(filepath.Join(configDir, "mcp_servers"), 0o755); err != nil {
		t.Fatalf("mkdir config modules: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "goflow.yaml"), []byte(`agent:
  name: GoFlow Agent
  max_iterations: 8
default_agent: chat
providers:
  primary:
    provider: openai-compatible
    base_url: http://localhost:9999/v1
    model: test-model
agents:
  chat:
    name: Chat
    provider: primary
    mode: chat
    max_iterations: 2
skill:
  directory: ./skills
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "mcp_servers", "netns.yaml"), []byte(`mcp_servers:
  - name: netns
    command: python
    args: [./mcp_servers/netns.py]
    enabled: true
    allowed_commands: [python]
    network_disabled: true
    isolation: linux_netns
`), 0o644); err != nil {
		t.Fatalf("write mcp module: %v", err)
	}
	server := NewServer(runtimeRef)
	request := httptest.NewRequest(http.MethodGet, "/api/config/diagnostics", nil)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected diagnostics 200, got %d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, `"mcp_isolation_network_only"`) {
		t.Fatalf("expected diagnostics to include linux_netns isolation, got %s", body)
	}
	if strings.Contains(body, `"mcp_network_disabled_advisory"`) {
		t.Fatalf("expected linux_netns not to report network_disabled advisory, got %s", body)
	}
}

func TestServerConfigDiagnosticsIncludesResourceizedWorkflowModules(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	server := NewServer(runtimeRef)
	if _, err := runtimeRef.SaveWorkflowNodeMetadataResource("policy_guard", agent.WorkflowNodeTypeOption{
		Type:        "policy_guard",
		Label:       "Review Gate",
		Description: "Custom review gate copy.",
	}); err != nil {
		t.Fatalf("save workflow node metadata: %v", err)
	}
	if _, err := runtimeRef.SaveWorkflowExpressionMetadataResource("risk_rank", agent.WorkflowExpressionFunctionOption{
		Name:        "risk_rank",
		Label:       "Severity Rank",
		Description: "Custom severity helper copy.",
	}); err != nil {
		t.Fatalf("save expression helper metadata: %v", err)
	}
	if _, err := runtimeRef.SaveTeamTemplateResource("review-team", agent.TeamTemplate{
		TeamTemplateSummary: agent.TeamTemplateSummary{
			Name:        "review-team",
			Title:       "Review Team",
			Description: "Custom review team.",
			Category:    "software",
		},
		RoleTemplates: []agent.TeamRoleTemplate{{
			Name:  "reviewer",
			Agent: "auditor",
			Skill: "code-audit",
		}},
	}); err != nil {
		t.Fatalf("save team template: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/config/diagnostics", nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected diagnostics 200, got %d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, want := range []string{
		`"workflow_node_metadata"`,
		`policy_guard.yaml`,
		`"expression_helpers"`,
		`risk_rank.yaml`,
		`"team_templates"`,
		`review-team.yaml`,
		`"workflow_schemas"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected diagnostics to include %s, got %s", want, body)
		}
	}
}

func TestServerConfigDiagnosticsReportsRestartRequiredForSavedAgentModule(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	configDir := filepath.Join(runtimeRef.RuntimeHome(), "configs")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "goflow.yaml"), []byte(`agent:
  name: GoFlow Agent
  max_iterations: 8
default_agent: chat
providers:
  primary:
    provider: openai-compatible
    base_url: http://localhost:9999/v1
    model: test-model
agents:
  chat:
    name: Chat
    provider: primary
    mode: chat
    model: test-model
    max_iterations: 2
    allowed_tool_kinds: [read, write, exec]
    tool_policy: confirm
  planner:
    name: Planner
    provider: primary
    mode: plan
    model: test-model
    max_iterations: 2
    allowed_tool_kinds: [read]
    tool_policy: allow
  fixer:
    name: Fixer
    provider: primary
    mode: fix
    model: test-model
    max_iterations: 2
    allowed_tool_kinds: [write]
    tool_policy: confirm
  auditor:
    name: Auditor
    provider: primary
    mode: audit
    model: test-model
    max_iterations: 2
    allowed_tool_kinds: [read]
    tool_policy: allow
skill:
  directory: ./skills
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	server := NewServer(runtimeRef)

	baselineRequest := httptest.NewRequest(http.MethodGet, "/api/config/diagnostics", nil)
	baselineResponse := httptest.NewRecorder()
	server.ServeHTTP(baselineResponse, baselineRequest)
	if baselineResponse.Code != http.StatusOK {
		t.Fatalf("expected baseline diagnostics 200, got %d body=%s", baselineResponse.Code, baselineResponse.Body.String())
	}
	if strings.Contains(baselineResponse.Body.String(), `"restart_required":true`) {
		t.Fatalf("expected matching saved config to avoid restart_required, got %s", baselineResponse.Body.String())
	}
	var baselineDiagnostics configDiagnosticsResponse
	if err := json.Unmarshal(baselineResponse.Body.Bytes(), &baselineDiagnostics); err != nil {
		t.Fatalf("decode baseline diagnostics response: %v", err)
	}
	if baselineDiagnostics.Apply.State != "active" || baselineDiagnostics.Apply.RestartRequired {
		t.Fatalf("expected active apply state for matching saved config, got %#v", baselineDiagnostics.Apply)
	}

	body := strings.NewReader(`{
		"id":"researcher",
		"name":"Researcher",
		"provider":"primary",
		"mode":"audit",
		"model":"test-model",
		"tool_policy":"allow",
		"allowed_tool_kinds":["read","network"],
		"max_iterations":2
	}`)
	saveRequest := httptest.NewRequest(http.MethodPut, "/api/resources/agents/researcher", body)
	saveRequest.Header.Set("Content-Type", "application/json")
	saveResponse := httptest.NewRecorder()
	server.ServeHTTP(saveResponse, saveRequest)
	if saveResponse.Code != http.StatusOK {
		t.Fatalf("expected agent save 200, got %d body=%s", saveResponse.Code, saveResponse.Body.String())
	}

	diagnosticsRequest := httptest.NewRequest(http.MethodGet, "/api/config/diagnostics", nil)
	diagnosticsResponse := httptest.NewRecorder()
	server.ServeHTTP(diagnosticsResponse, diagnosticsRequest)
	if diagnosticsResponse.Code != http.StatusOK {
		t.Fatalf("expected diagnostics 200, got %d body=%s", diagnosticsResponse.Code, diagnosticsResponse.Body.String())
	}
	diagnosticsBody := diagnosticsResponse.Body.String()
	if !strings.Contains(diagnosticsBody, `"restart_required":true`) ||
		!strings.Contains(diagnosticsBody, `"code":"restart_required"`) ||
		!strings.Contains(diagnosticsBody, `researcher.yaml`) {
		t.Fatalf("expected restart-required diagnostics for saved agent module, got %s", diagnosticsBody)
	}
	var diagnostics configDiagnosticsResponse
	if err := json.Unmarshal(diagnosticsResponse.Body.Bytes(), &diagnostics); err != nil {
		t.Fatalf("decode diagnostics response: %v", err)
	}
	if diagnostics.Apply.State != "restart_required" ||
		!diagnostics.Apply.RestartRequired ||
		diagnostics.Apply.RuntimeRebootstrapSupported ||
		diagnostics.Apply.HotReloadSupported ||
		!configDiagnosticsStringSliceContains(diagnostics.Apply.AffectedKinds, "agent") ||
		!configDiagnosticsStringSliceContains(diagnostics.Apply.RebootstrapBlockers, "agents_bound_to_runtime_profiles") ||
		!configDiagnosticsStringSliceContains(diagnostics.Apply.RebootstrapBlockers, "session_approvals_and_tool_policy_depend_on_runtime_catalog") ||
		!configDiagnosticsStringSliceContains(diagnostics.Apply.RestartCommandHint, "--config") {
		t.Fatalf("expected structured restart-required apply summary for saved agent module, got %#v", diagnostics.Apply)
	}
}

func TestServerConfigDiagnosticsReportsRestartRequiredForSavedToolModule(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	configDir := filepath.Join(runtimeRef.RuntimeHome(), "configs")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "goflow.yaml"), []byte(`agent:
  name: GoFlow Agent
  max_iterations: 8
default_agent: chat
providers:
  primary:
    provider: openai-compatible
    base_url: http://localhost:9999/v1
    model: test-model
agents:
  chat:
    name: Chat
    provider: primary
    mode: chat
    model: test-model
    max_iterations: 2
    allowed_tool_kinds: [read, write, exec]
    tool_policy: confirm
  planner:
    name: Planner
    provider: primary
    mode: plan
    model: test-model
    max_iterations: 2
    allowed_tool_kinds: [read]
    tool_policy: allow
  fixer:
    name: Fixer
    provider: primary
    mode: fix
    model: test-model
    max_iterations: 2
    allowed_tool_kinds: [write]
    tool_policy: confirm
  auditor:
    name: Auditor
    provider: primary
    mode: audit
    model: test-model
    max_iterations: 2
    allowed_tool_kinds: [read]
    tool_policy: allow
skill:
  directory: ./skills
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	server := NewServer(runtimeRef)

	baselineRequest := httptest.NewRequest(http.MethodGet, "/api/config/diagnostics", nil)
	baselineResponse := httptest.NewRecorder()
	server.ServeHTTP(baselineResponse, baselineRequest)
	if baselineResponse.Code != http.StatusOK {
		t.Fatalf("expected baseline diagnostics 200, got %d body=%s", baselineResponse.Code, baselineResponse.Body.String())
	}
	if strings.Contains(baselineResponse.Body.String(), `"restart_required":true`) {
		t.Fatalf("expected matching saved config to avoid restart_required, got %s", baselineResponse.Body.String())
	}
	var baselineDiagnostics configDiagnosticsResponse
	if err := json.Unmarshal(baselineResponse.Body.Bytes(), &baselineDiagnostics); err != nil {
		t.Fatalf("decode baseline diagnostics response: %v", err)
	}
	if baselineDiagnostics.Apply.State != "active" || baselineDiagnostics.Apply.RestartRequired {
		t.Fatalf("expected active apply state for matching saved config, got %#v", baselineDiagnostics.Apply)
	}

	if _, err := server.saveToolResource("notes-helper", toolResourceDocument{
		Name:        "notes-helper",
		Language:    "python",
		Description: "Notes helper.",
		Command:     "python",
		Args:        []string{"./mcp_servers/notes-helper.py"},
		Enabled:     true,
		Timeout:     "30s",
		WorkDir:     ".",
		Code:        "#!/usr/bin/env python3\nprint('ok')\n",
	}); err != nil {
		t.Fatalf("save tool resource: %v", err)
	}

	diagnosticsRequest := httptest.NewRequest(http.MethodGet, "/api/config/diagnostics", nil)
	diagnosticsResponse := httptest.NewRecorder()
	server.ServeHTTP(diagnosticsResponse, diagnosticsRequest)
	if diagnosticsResponse.Code != http.StatusOK {
		t.Fatalf("expected diagnostics 200, got %d body=%s", diagnosticsResponse.Code, diagnosticsResponse.Body.String())
	}
	diagnosticsBody := diagnosticsResponse.Body.String()
	if !strings.Contains(diagnosticsBody, `"restart_required":true`) ||
		!strings.Contains(diagnosticsBody, `"code":"restart_required"`) ||
		!strings.Contains(diagnosticsBody, `notes-helper.yaml`) {
		t.Fatalf("expected restart-required diagnostics for saved tool module, got %s", diagnosticsBody)
	}
	var diagnostics configDiagnosticsResponse
	if err := json.Unmarshal(diagnosticsResponse.Body.Bytes(), &diagnostics); err != nil {
		t.Fatalf("decode diagnostics response: %v", err)
	}
	if diagnostics.Apply.State != "restart_required" ||
		!diagnostics.Apply.RestartRequired ||
		diagnostics.Apply.RuntimeRebootstrapSupported ||
		diagnostics.Apply.HotReloadSupported ||
		!configDiagnosticsStringSliceContains(diagnostics.Apply.AffectedKinds, "mcp_server") ||
		!configDiagnosticsStringSliceContains(diagnostics.Apply.RebootstrapBlockers, "mcp_servers_bound_to_child_processes") ||
		!configDiagnosticsStringSliceContains(diagnostics.Apply.RebootstrapBlockers, "session_approvals_and_tool_policy_depend_on_runtime_catalog") ||
		!configDiagnosticsStringSliceContains(diagnostics.Apply.RestartCommandHint, "--config") {
		t.Fatalf("expected structured restart-required apply summary for saved tool module, got %#v", diagnostics.Apply)
	}
}

func TestServerWorkspaceFilesListsConfirmedWorkspaceFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "app", "models"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "app", "models", "user.py"), []byte("class User: pass"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".goflow"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".goflow", "session.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".venv", "Lib", "site-packages"), 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3050; i++ {
		path := filepath.Join(root, ".venv", "Lib", "site-packages", fmt.Sprintf("ignored_%04d.py", i))
		if err := os.WriteFile(path, []byte("ignored"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "node_modules", "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "node_modules", "pkg", "index.js"), []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.py"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Symlink(outside, filepath.Join(root, "outside-link"))
	server := NewServerWithWorkspace(newAPITestRuntime(t), workspace.New(root, true))

	request := httptest.NewRequest(http.MethodGet, "/api/workspace-files?prefix=REA", nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected workspace files 200, got %d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"README.md"`) {
		t.Fatalf("expected README suggestion, got %s", response.Body.String())
	}
	var body workspaceFilesResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode workspace files response: %v", err)
	}
	if body.SearchMode != "prefix_or_contains" ||
		body.TraversalLimit != workspaceReferenceTraversalLimit ||
		body.Visited == 0 ||
		body.TraversalTruncated {
		t.Fatalf("expected prefix metadata without traversal truncation, got %#v", body)
	}
	if !configDiagnosticsStringSliceContains(body.IgnoredDirs, ".goflow/") ||
		!configDiagnosticsStringSliceContains(body.IgnoredDirs, ".venv/") ||
		!configDiagnosticsStringSliceContains(body.IgnoredDirs, "node_modules/") {
		t.Fatalf("expected ignored directory metadata for hidden directories, got %#v", body.IgnoredDirs)
	}
	if workspaceFilesEntryPathContains(body.Entries, ".goflow") ||
		workspaceFilesEntryPathContains(body.Entries, ".venv") ||
		workspaceFilesEntryPathContains(body.Entries, "node_modules") {
		t.Fatalf("expected ignored directories to be hidden from entries, got %#v", body.Entries)
	}
	if workspaceFilesEntryPathContains(body.Entries, "outside-link") ||
		workspaceFilesEntryPathContains(body.Entries, "secret.py") {
		t.Fatalf("expected symlink escape paths to be hidden from entries, got %#v", body.Entries)
	}

	containsRequest := httptest.NewRequest(http.MethodGet, "/api/workspace-files?prefix=user.py&limit=20", nil)
	containsResponse := httptest.NewRecorder()
	server.ServeHTTP(containsResponse, containsRequest)
	if containsResponse.Code != http.StatusOK {
		t.Fatalf("expected workspace contains search 200, got %d body=%s", containsResponse.Code, containsResponse.Body.String())
	}
	if !strings.Contains(containsResponse.Body.String(), `"app/models/user.py"`) {
		t.Fatalf("expected contains search to find nested user.py, got %s", containsResponse.Body.String())
	}
	var containsBody workspaceFilesResponse
	if err := json.Unmarshal(containsResponse.Body.Bytes(), &containsBody); err != nil {
		t.Fatalf("decode contains workspace files response: %v", err)
	}
	if containsBody.SearchMode != "prefix_or_contains" {
		t.Fatalf("expected plain prefix to use prefix_or_contains mode, got %#v", containsBody)
	}

	queryRequest := httptest.NewRequest(http.MethodGet, "/api/workspace-files?q=models&limit=20", nil)
	queryResponse := httptest.NewRecorder()
	server.ServeHTTP(queryResponse, queryRequest)
	if queryResponse.Code != http.StatusOK {
		t.Fatalf("expected workspace query search 200, got %d body=%s", queryResponse.Code, queryResponse.Body.String())
	}
	if !strings.Contains(queryResponse.Body.String(), `"app/models/"`) || !strings.Contains(queryResponse.Body.String(), `"query":"models"`) {
		t.Fatalf("expected explicit query search metadata and result, got %s", queryResponse.Body.String())
	}
	var queryBody workspaceFilesResponse
	if err := json.Unmarshal(queryResponse.Body.Bytes(), &queryBody); err != nil {
		t.Fatalf("decode query workspace files response: %v", err)
	}
	if queryBody.SearchMode != "contains" {
		t.Fatalf("expected explicit query to use contains mode, got %#v", queryBody)
	}
}

func TestServerWorkspaceFilesReportsTraversalTruncation(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < workspaceReferenceTraversalLimit+20; i++ {
		path := filepath.Join(root, fmt.Sprintf("file_%04d.txt", i))
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	server := NewServerWithWorkspace(newAPITestRuntime(t), workspace.New(root, true))

	request := httptest.NewRequest(http.MethodGet, "/api/workspace-files?limit=200", nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected workspace files 200, got %d body=%s", response.Code, response.Body.String())
	}
	var body workspaceFilesResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode workspace files response: %v", err)
	}
	if body.SearchMode != "all" ||
		body.TraversalLimit != workspaceReferenceTraversalLimit ||
		body.Visited != workspaceReferenceTraversalLimit ||
		!body.TraversalTruncated ||
		!body.Truncated {
		t.Fatalf("expected traversal and result truncation metadata, got %#v", body)
	}
	if len(body.Entries) != 200 {
		t.Fatalf("expected response entries capped by limit, got %d", len(body.Entries))
	}
}

func workspaceFilesEntryPathContains(entries []workspaceFileEntry, want string) bool {
	for _, entry := range entries {
		if strings.Contains(entry.Path, want) {
			return true
		}
	}
	return false
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

func TestWorkspaceAPIConfirmClearAndSelect(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	demoRoot := filepath.Join(t.TempDir(), "demo")
	otherRoot := filepath.Join(t.TempDir(), "other")
	workspaceState := workspace.New(demoRoot, false)
	runtimeRef.SetWorkspaceConfirmed(workspaceState.Confirmed())
	server := NewServerWithWorkspace(runtimeRef, workspaceState)

	statusRequest := httptest.NewRequest(http.MethodGet, "/api/workspace", nil)
	statusResponse := httptest.NewRecorder()
	server.ServeHTTP(statusResponse, statusRequest)
	if statusResponse.Code != http.StatusOK {
		t.Fatalf("expected workspace status 200, got %d", statusResponse.Code)
	}
	if !strings.Contains(statusResponse.Body.String(), `"confirmed":false`) {
		t.Fatalf("expected unconfirmed workspace status, got %s", statusResponse.Body.String())
	}
	if !strings.Contains(statusResponse.Body.String(), `"capabilities"`) ||
		!strings.Contains(statusResponse.Body.String(), `"pure_chat_without_workspace":true`) ||
		!strings.Contains(statusResponse.Body.String(), `"switch_requires_restart":true`) ||
		!strings.Contains(statusResponse.Body.String(), `"requires_runtime_restart":true`) ||
		!strings.Contains(statusResponse.Body.String(), `"switch_mode":"restart_required"`) ||
		!strings.Contains(statusResponse.Body.String(), `"folder_picker_mode":"client_or_text"`) ||
		!strings.Contains(statusResponse.Body.String(), `"folder_picker_opt_in_env":"GOFLOW_ENABLE_HOST_FOLDER_PICKER"`) ||
		!strings.Contains(statusResponse.Body.String(), `"name":"choose_folder"`) ||
		!strings.Contains(statusResponse.Body.String(), `"name":"confirm"`) {
		t.Fatalf("expected workspace status capabilities/actions, got %s", statusResponse.Body.String())
	}
	var statusBody workspaceResponse
	if err := json.NewDecoder(statusResponse.Body).Decode(&statusBody); err != nil {
		t.Fatalf("decode workspace status response: %v", err)
	}
	if statusBody.Capabilities.RuntimeRebindSupported ||
		!statusBody.Capabilities.RequiresRuntimeRestart ||
		!workflowStringSliceContains(statusBody.Capabilities.SwitchBlockers, "mcp_servers_bound_to_workspace_root") ||
		!workspaceSwitchBlockerDetailExists(statusBody.Capabilities.SwitchBlockerDetails, "mcp_servers_bound_to_workspace_root", "runtime_binding", false) ||
		!statusBody.Capabilities.SwitchPlan.RequiresMCPRebootstrap ||
		!statusBody.Capabilities.SwitchPlan.RequiresApprovalScopeReset ||
		!workspaceSwitchBlockerDetailExists(statusBody.Capabilities.SwitchPlan.BlockerDetails, "session_state_and_approvals_are_workspace_scoped", "session_scope", false) ||
		statusBody.Capabilities.SwitchPlan.Strategy != "restart" ||
		len(statusBody.Capabilities.RestartCommandHint) != 3 ||
		statusBody.Capabilities.Guidance.Schema != "goflow.workspace.guidance.v1" ||
		statusBody.Capabilities.Guidance.Mode != "restart_required" ||
		statusBody.Capabilities.Guidance.SelectPath != "/api/workspace/select" ||
		!configDiagnosticsStringSliceContains(statusBody.Capabilities.Guidance.RefreshPaths, "/api/runtime") ||
		!configDiagnosticsStringSliceContains(statusBody.Capabilities.Guidance.ConflictFields, "restart_command_hint") ||
		!workspaceGuidanceStepExists(statusBody.Capabilities.Guidance.Steps, "preflight", "/api/workspace/requirement") ||
		!workspaceGuidanceStepExists(statusBody.Capabilities.Guidance.Steps, "select_workspace", "/api/workspace/select") ||
		!workspacePreflightOperationExists(statusBody.Capabilities.Guidance.PreflightOperations, "workflow", true) ||
		!workspaceGuidanceResultStateExists(statusBody.Capabilities.Guidance.ResultStates, "restart_required", http.StatusConflict) ||
		statusBody.Capabilities.RestartCommandHint[2] != "<path>" {
		t.Fatalf("expected machine-readable workspace switch blockers, got %#v", statusBody.Capabilities)
	}
	if !statusBody.SwitchPlan.RequiresRuntimeRestart ||
		!statusBody.SwitchPlan.RequiresSessionScopeReset ||
		statusBody.SwitchPlan.NormalizedTargetPath != "<path>" ||
		len(statusBody.SwitchPlan.RestartCommandHint) != 3 {
		t.Fatalf("expected top-level workspace switch plan, got %#v", statusBody.SwitchPlan)
	}
	runtimeStatusRequest := httptest.NewRequest(http.MethodGet, "/api/runtime", nil)
	runtimeStatusRecorder := httptest.NewRecorder()
	server.ServeHTTP(runtimeStatusRecorder, runtimeStatusRequest)
	if runtimeStatusRecorder.Code != http.StatusOK {
		t.Fatalf("expected runtime status 200, got %d body=%s", runtimeStatusRecorder.Code, runtimeStatusRecorder.Body.String())
	}
	var runtimeBody runtimeStatusResponse
	if err := json.NewDecoder(runtimeStatusRecorder.Body).Decode(&runtimeBody); err != nil {
		t.Fatalf("decode runtime status response: %v", err)
	}
	if !runtimeBody.WorkspaceCapabilities.PureChatWithoutWorkspace ||
		!runtimeBody.WorkspaceCapabilities.SwitchRequiresRestart ||
		!runtimeBody.WorkspaceCapabilities.RequiresRuntimeRestart ||
		runtimeBody.WorkspaceCapabilities.RuntimeRebindSupported ||
		runtimeBody.WorkspaceCapabilities.SwitchMode != "restart_required" ||
		!workflowStringSliceContains(runtimeBody.WorkspaceCapabilities.SwitchBlockers, "tool_policy_is_evaluated_against_startup_workspace") ||
		!workspaceSwitchBlockerDetailExists(runtimeBody.WorkspaceCapabilities.SwitchBlockerDetails, "tool_policy_is_evaluated_against_startup_workspace", "tool_policy", false) ||
		!runtimeBody.WorkspaceCapabilities.SwitchPlan.RequiresToolPolicyRebuild ||
		runtimeBody.WorkspaceCapabilities.SwitchPlan.Mode != "restart_required" ||
		runtimeBody.WorkspaceCapabilities.FolderPickerMode != "client_or_text" ||
		runtimeBody.WorkspaceCapabilities.FolderPickerOptInEnv != "GOFLOW_ENABLE_HOST_FOLDER_PICKER" ||
		runtimeBody.WorkspaceCapabilities.Guidance.Mode != "restart_required" ||
		!configDiagnosticsStringSliceContains(runtimeBody.WorkspaceCapabilities.Guidance.OperatorSteps, "when the response is 409 restart_required, show restart_command_hint or suggested_args") ||
		runtimeBody.WorkspaceCapabilities.CanOpenHostFolderPicker ||
		!workspaceRuntimeStatusHasAction(runtimeBody, "choose_folder", false) ||
		!workspaceRuntimeStatusHasAction(runtimeBody, "confirm", false) {
		t.Fatalf("expected runtime workspace capabilities/actions, got %#v", runtimeBody)
	}

	pickRequest := httptest.NewRequest(http.MethodPost, "/api/workspace/pick-folder", strings.NewReader(`{}`))
	pickResponse := httptest.NewRecorder()
	server.ServeHTTP(pickResponse, pickRequest)
	if pickResponse.Code != http.StatusConflict {
		t.Fatalf("expected disabled host folder picker conflict, got %d body=%s", pickResponse.Code, pickResponse.Body.String())
	}
	if !strings.Contains(pickResponse.Body.String(), `"requires_explicit_opt_in":true`) ||
		!strings.Contains(pickResponse.Body.String(), `"opt_in_env":"GOFLOW_ENABLE_HOST_FOLDER_PICKER"`) ||
		!strings.Contains(pickResponse.Body.String(), `"folder_picker_mode":"client_or_text"`) {
		t.Fatalf("expected folder picker opt-in guidance, got %s", pickResponse.Body.String())
	}

	confirmRequest := httptest.NewRequest(http.MethodPost, "/api/workspace/confirm", nil)
	confirmResponse := httptest.NewRecorder()
	server.ServeHTTP(confirmResponse, confirmRequest)
	if confirmResponse.Code != http.StatusOK || !runtimeRef.WorkspaceConfirmed() {
		t.Fatalf("expected confirmed workspace, code=%d body=%s", confirmResponse.Code, confirmResponse.Body.String())
	}

	clearRequest := httptest.NewRequest(http.MethodPost, "/api/workspace/clear", nil)
	clearResponse := httptest.NewRecorder()
	server.ServeHTTP(clearResponse, clearRequest)
	if clearResponse.Code != http.StatusOK || runtimeRef.WorkspaceConfirmed() {
		t.Fatalf("expected cleared workspace confirmation, code=%d body=%s", clearResponse.Code, clearResponse.Body.String())
	}

	selectRequest := httptest.NewRequest(http.MethodPost, "/api/workspace/select", strings.NewReader(fmt.Sprintf(`{"path":%q}`, otherRoot)))
	selectRequest.Header.Set("Content-Type", "application/json")
	selectResponse := httptest.NewRecorder()
	server.ServeHTTP(selectResponse, selectRequest)
	if selectResponse.Code != http.StatusConflict {
		t.Fatalf("expected restart-required conflict, got %d body=%s", selectResponse.Code, selectResponse.Body.String())
	}
	if !strings.Contains(selectResponse.Body.String(), `"restart_required":true`) {
		t.Fatalf("expected restart required response, got %s", selectResponse.Body.String())
	}
	var selectBody workspaceResponse
	if err := json.NewDecoder(selectResponse.Body).Decode(&selectBody); err != nil {
		t.Fatalf("decode workspace select response: %v", err)
	}
	if !selectBody.RestartRequired ||
		!selectBody.RequiresRuntimeRestart ||
		selectBody.NormalizedSelectedPath != otherRoot ||
		!workflowStringSliceContains(selectBody.SwitchBlockers, "session_state_and_approvals_are_workspace_scoped") ||
		!workspaceSwitchBlockerDetailExists(selectBody.SwitchBlockerDetails, "session_state_and_approvals_are_workspace_scoped", "session_scope", false) ||
		!selectBody.SwitchPlan.RequiresMCPRebootstrap ||
		!selectBody.SwitchPlan.RequiresApprovalScopeReset ||
		!selectBody.SwitchPlan.RequiresToolPolicyRebuild ||
		!workspaceSwitchBlockerDetailExists(selectBody.SwitchPlan.BlockerDetails, "tool_policy_is_evaluated_against_startup_workspace", "tool_policy", false) ||
		selectBody.SwitchPlan.TargetPath != otherRoot ||
		selectBody.SwitchPlan.NormalizedTargetPath != otherRoot ||
		selectBody.SwitchPlan.Strategy != "restart" ||
		len(selectBody.SwitchPlan.SuggestedArgs) != 2 ||
		selectBody.SwitchPlan.SuggestedArgs[1] != otherRoot ||
		len(selectBody.RestartCommandHint) != 3 ||
		selectBody.RestartCommandHint[2] != otherRoot ||
		!workspaceResponseHasAction(selectBody, "switch", true) ||
		len(selectBody.SuggestedArgs) != 2 ||
		selectBody.SuggestedArgs[1] != otherRoot {
		t.Fatalf("expected workspace switch action guidance, got %#v", selectBody)
	}

	sameSelectRequest := httptest.NewRequest(http.MethodPost, "/api/workspace/select", strings.NewReader(fmt.Sprintf(`{"path":%q}`, demoRoot)))
	sameSelectRequest.Header.Set("Content-Type", "application/json")
	sameSelectResponse := httptest.NewRecorder()
	server.ServeHTTP(sameSelectResponse, sameSelectRequest)
	if sameSelectResponse.Code != http.StatusOK || !runtimeRef.WorkspaceConfirmed() {
		t.Fatalf("expected same workspace selection to confirm, code=%d body=%s", sameSelectResponse.Code, sameSelectResponse.Body.String())
	}
	var sameSelectBody workspaceResponse
	if err := json.NewDecoder(sameSelectResponse.Body).Decode(&sameSelectBody); err != nil {
		t.Fatalf("decode same workspace select response: %v", err)
	}
	if sameSelectBody.RestartRequired ||
		sameSelectBody.RequiresRuntimeRestart ||
		sameSelectBody.NormalizedSelectedPath != demoRoot ||
		!workspaceResponseHasAction(sameSelectBody, "clear", false) ||
		!workspaceResponseHasAction(sameSelectBody, "select_same", false) {
		t.Fatalf("expected same workspace selection without restart requirement, got %#v", sameSelectBody)
	}
}

func TestWorkspaceAPISelectRebindsWhenSupported(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	demoRoot := filepath.Join(t.TempDir(), "demo")
	otherRoot := filepath.Join(t.TempDir(), "other")
	workspaceState := workspace.New(demoRoot, true)
	runtimeRef.SetWorkspaceConfirmed(workspaceState.Confirmed())
	reboundRuntime := newAPITestRuntime(t)
	var reboundPath string
	server := NewServerWithWorkspaceRebinder(runtimeRef, workspaceState, func(ctx context.Context, path string) (*agent.Runtime, *workspace.State, error) {
		reboundPath = path
		return reboundRuntime, workspace.New(path, true), nil
	})

	statusRequest := httptest.NewRequest(http.MethodGet, "/api/workspace", nil)
	statusResponse := httptest.NewRecorder()
	server.ServeHTTP(statusResponse, statusRequest)
	var statusBody workspaceResponse
	if err := json.NewDecoder(statusResponse.Body).Decode(&statusBody); err != nil {
		t.Fatalf("decode workspace status response: %v", err)
	}
	if !statusBody.Capabilities.RuntimeRebindSupported ||
		statusBody.Capabilities.RequiresRuntimeRestart ||
		statusBody.Capabilities.SwitchMode != "dynamic_rebind" ||
		statusBody.Capabilities.SwitchPlan.Strategy != "runtime_rebind" ||
		statusBody.Capabilities.Guidance.Mode != "dynamic_rebind" ||
		!configDiagnosticsStringSliceContains(statusBody.Capabilities.Guidance.ConflictFields, "switch_plan") ||
		!configDiagnosticsStringSliceContains(statusBody.Capabilities.Guidance.RefreshPaths, "/api/workspace-files") ||
		!workspaceGuidanceStepExists(statusBody.Capabilities.Guidance.Steps, "handle_result", "") ||
		!workspaceGuidanceResultStateExists(statusBody.Capabilities.Guidance.ResultStates, "rebound", http.StatusOK) {
		t.Fatalf("expected dynamic rebind capabilities, got %#v", statusBody.Capabilities)
	}

	selectRequest := httptest.NewRequest(http.MethodPost, "/api/workspace/select", strings.NewReader(fmt.Sprintf(`{"path":%q}`, otherRoot)))
	selectRequest.Header.Set("Content-Type", "application/json")
	selectResponse := httptest.NewRecorder()
	server.ServeHTTP(selectResponse, selectRequest)
	if selectResponse.Code != http.StatusOK {
		t.Fatalf("expected dynamic rebind 200, got %d body=%s", selectResponse.Code, selectResponse.Body.String())
	}
	if reboundPath != otherRoot {
		t.Fatalf("expected rebinder path, got %q", reboundPath)
	}
	var selectBody workspaceResponse
	if err := json.NewDecoder(selectResponse.Body).Decode(&selectBody); err != nil {
		t.Fatalf("decode workspace select response: %v", err)
	}
	if selectBody.RestartRequired ||
		selectBody.RequiresRuntimeRestart ||
		selectBody.Root != otherRoot ||
		!selectBody.Confirmed ||
		selectBody.SwitchPlan.Mode != "dynamic_rebind" ||
		selectBody.SwitchPlan.RequiresRuntimeRestart ||
		!selectBody.SwitchPlan.RequiresMCPRebootstrap ||
		!workspaceResponseHasAction(selectBody, "switch", false) {
		t.Fatalf("expected dynamic workspace rebind response, got %#v", selectBody)
	}

	runtimeStatusRequest := httptest.NewRequest(http.MethodGet, "/api/runtime", nil)
	runtimeStatusRecorder := httptest.NewRecorder()
	server.ServeHTTP(runtimeStatusRecorder, runtimeStatusRequest)
	var runtimeBody runtimeStatusResponse
	if err := json.NewDecoder(runtimeStatusRecorder.Body).Decode(&runtimeBody); err != nil {
		t.Fatalf("decode runtime status response: %v", err)
	}
	if runtimeBody.Workspace.Root != otherRoot ||
		!runtimeBody.WorkspaceCapabilities.RuntimeRebindSupported ||
		runtimeBody.WorkspaceCapabilities.RequiresRuntimeRestart {
		t.Fatalf("expected rebound runtime status, got %#v", runtimeBody.WorkspaceCapabilities)
	}
}

func workspaceResponseHasAction(response workspaceResponse, name string, restartRequired bool) bool {
	for _, action := range response.Actions {
		if action.Name == name && action.RestartRequired == restartRequired {
			return true
		}
	}
	return false
}

func workspaceRuntimeStatusHasAction(response runtimeStatusResponse, name string, restartRequired bool) bool {
	for _, action := range response.WorkspaceActions {
		if action.Name == name && action.RestartRequired == restartRequired {
			return true
		}
	}
	return false
}

func workspaceSwitchBlockerDetailExists(items []workspaceSwitchBlocker, code, category string, transient bool) bool {
	for _, item := range items {
		if item.Code == code && item.Category == category && item.Transient == transient && item.Message != "" && item.Recommendation != "" {
			return true
		}
	}
	return false
}

func workspaceGuidanceStepExists(items []workspaceGuidanceStep, id, path string) bool {
	for _, item := range items {
		if item.ID != id {
			continue
		}
		return path == "" || item.Path == path
	}
	return false
}

func workspacePreflightOperationExists(items []workspacePreflightOperation, operation string, requires bool) bool {
	for _, item := range items {
		if item.Operation == operation && item.Requires == requires {
			return true
		}
	}
	return false
}

func workspaceGuidanceResultStateExists(items []workspaceGuidanceResultState, state string, status int) bool {
	for _, item := range items {
		if item.State == state && item.Status == status {
			return true
		}
	}
	return false
}

func TestWorkspaceRequirementEndpointPreflightsRequests(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	workspaceState := workspace.New(`D:\Projects\demo`, false)
	runtimeRef.SetWorkspaceConfirmed(workspaceState.Confirmed())
	server := NewServerWithWorkspace(runtimeRef, workspaceState)

	chatRequest := httptest.NewRequest(http.MethodPost, "/api/workspace/requirement", strings.NewReader(`{"input":"聊聊这个框架能做什么"}`))
	chatRequest.Header.Set("Content-Type", "application/json")
	chatResponse := httptest.NewRecorder()
	server.ServeHTTP(chatResponse, chatRequest)
	if chatResponse.Code != http.StatusOK {
		t.Fatalf("expected chat preflight 200, got %d body=%s", chatResponse.Code, chatResponse.Body.String())
	}
	var chat workspaceRequirementResponse
	if err := json.NewDecoder(chatResponse.Body).Decode(&chat); err != nil {
		t.Fatalf("decode chat requirement: %v", err)
	}
	if chat.Required || chat.Blocked || chat.Confirmed {
		t.Fatalf("expected pure chat not to require workspace while workspace is unconfirmed, got %#v", chat)
	}

	writeRequest := httptest.NewRequest(http.MethodPost, "/api/workspace/requirement", strings.NewReader(`{"input":"帮我写个 Python 计算器项目"}`))
	writeRequest.Header.Set("Content-Type", "application/json")
	writeResponse := httptest.NewRecorder()
	server.ServeHTTP(writeResponse, writeRequest)
	if writeResponse.Code != http.StatusOK {
		t.Fatalf("expected write preflight 200, got %d body=%s", writeResponse.Code, writeResponse.Body.String())
	}
	var write workspaceRequirementResponse
	if err := json.NewDecoder(writeResponse.Body).Decode(&write); err != nil {
		t.Fatalf("decode write requirement: %v", err)
	}
	if !write.Required || !write.Blocked || write.Confirmed || write.Action != "confirm_workspace" || !strings.Contains(write.Message, "workspace confirmation required") {
		t.Fatalf("expected workspace-required write preflight, got %#v", write)
	}

	workflowRequest := httptest.NewRequest(http.MethodGet, "/api/workspace/requirement?operation=workflow", nil)
	workflowResponse := httptest.NewRecorder()
	server.ServeHTTP(workflowResponse, workflowRequest)
	if workflowResponse.Code != http.StatusOK {
		t.Fatalf("expected workflow preflight 200, got %d body=%s", workflowResponse.Code, workflowResponse.Body.String())
	}
	var workflowReq workspaceRequirementResponse
	if err := json.NewDecoder(workflowResponse.Body).Decode(&workflowReq); err != nil {
		t.Fatalf("decode workflow requirement: %v", err)
	}
	if !workflowReq.Required || !workflowReq.Blocked || !strings.Contains(workflowReq.Reason, "workflow execution") {
		t.Fatalf("expected workspace-required workflow preflight, got %#v", workflowReq)
	}
}

func TestServerRunEndpointRequiresWorkspaceConfirmationForWorkspaceTasks(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	workspaceState := workspace.New(`D:\Projects\demo`, false)
	runtimeRef.SetWorkspaceConfirmed(workspaceState.Confirmed())
	server := NewServerWithWorkspace(runtimeRef, workspaceState)

	request := httptest.NewRequest(http.MethodPost, "/api/run", strings.NewReader(`{"input":"帮我写个 Python 计算器项目"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusConflict {
		t.Fatalf("expected workspace-required conflict, got %d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"workspace_required"`) {
		t.Fatalf("expected workspace required response, got %s", response.Body.String())
	}
}

func TestServerWorkspacePageRendersStatus(t *testing.T) {
	server := NewServerWithWorkspace(newAPITestRuntime(t), workspace.New(`D:\Projects\demo`, false))
	request := httptest.NewRequest(http.MethodGet, "/workspace", nil)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected workspace page 200, got %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), "GoFlow Workspace") || !strings.Contains(response.Body.String(), "Confirm Workspace") {
		t.Fatalf("expected workspace page HTML, got %s", response.Body.String())
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

func TestSessionSnapshotForAPIStripsHeavyRunDetailsByDefault(t *testing.T) {
	large := strings.Repeat("x", sessionSummaryTextBytes+200)
	snapshot := session.Snapshot{
		ActiveAgent: "fixer",
		Workflow: session.WorkflowSnapshot{
			RunID:                   "wf-1",
			Name:                    "plan-fix-audit",
			Status:                  "awaiting_tool_approval",
			PendingCallID:           "call-3",
			PendingToolName:         "write_file",
			PendingArguments:        large,
			PendingArgumentsSummary: large,
		},
		AgentRuns: []session.AgentRunSnapshot{{
			ID:     "agent-1",
			Status: "completed",
			Output: large,
			Events: []session.AgentRunEventSnapshot{{Seq: 1, Content: large}},
			Result: &schema.AgentResult{Output: large, ToolResults: []schema.ToolResult{{CallID: "call-1", ToolName: "read_file", Content: large}}, ResponseMessage: schema.Message{
				Role:           "assistant",
				ProviderFields: map[string]json.RawMessage{"reasoning_content": json.RawMessage(`"private agent reasoning"`)},
			}},
			Mode:    "fix",
			AgentID: "fixer",
			ResumeContext: &session.AgentRunResumeContextSnapshot{Messages: []schema.Message{{
				Role:           "assistant",
				ProviderFields: map[string]json.RawMessage{"reasoning_content": json.RawMessage(`"private resume reasoning"`)},
			}}},
		}},
		WorkflowRuns: []session.WorkflowRunSnapshot{{
			ID:              "wf-1",
			Name:            "plan-fix-audit",
			Status:          "completed",
			Summary:         large,
			PendingCallID:   "call-4",
			PendingToolName: "write_file",
			PendingArgs:     large,
			PendingResponseMessage: schema.Message{
				Role:           "assistant",
				ProviderFields: map[string]json.RawMessage{"reasoning_content": json.RawMessage(`"private workflow reasoning"`)},
			},
			CompletedStages: []session.WorkflowRunStageSnapshot{{
				Stage: "plan",
				Result: schema.AgentResult{Output: large, ToolResults: []schema.ToolResult{{CallID: "call-2", ToolName: "read_file", Content: large}}, ResponseMessage: schema.Message{
					Role:           "assistant",
					ProviderFields: map[string]json.RawMessage{"reasoning_content": json.RawMessage(`"private stage reasoning"`)},
				}},
			}},
			Artifacts: []session.WorkflowRunArtifact{{ID: "artifact-1", Content: large}},
			Events:    []session.WorkflowRunEventSnapshot{{Seq: 3, Content: large}},
		}},
		PendingApprovals: []session.PendingApprovalSnapshot{{
			CallID:           "call-5",
			ToolName:         "write_file",
			Arguments:        large,
			ArgumentsSummary: large,
		}},
		Artifacts: []session.SessionArtifactSnapshot{{ID: "artifact-2", Content: large, Summary: large}},
	}

	summary := sessionSnapshotForAPI(snapshot, false)
	if len(summary.AgentRuns[0].Events) != 0 || summary.AgentRuns[0].Result != nil {
		t.Fatalf("expected agent run details stripped, got %#v", summary.AgentRuns[0])
	}
	if summary.AgentRuns[0].EventsCount != 1 || !strings.HasSuffix(summary.AgentRuns[0].Output, "...") {
		t.Fatalf("expected agent summary counts and truncated output, got %#v", summary.AgentRuns[0])
	}
	if len(summary.WorkflowRuns[0].CompletedStages) != 0 || len(summary.WorkflowRuns[0].Artifacts) != 0 || len(summary.WorkflowRuns[0].Events) != 0 {
		t.Fatalf("expected workflow run details stripped, got %#v", summary.WorkflowRuns[0])
	}
	if summary.WorkflowRuns[0].PendingArgs != "" || !strings.HasSuffix(summary.WorkflowRuns[0].PendingArgsSummary, "...") {
		t.Fatalf("expected workflow pending args summarized, got %#v", summary.WorkflowRuns[0])
	}
	if summary.WorkflowRuns[0].StagesCount != 1 || summary.WorkflowRuns[0].ArtifactsCount != 1 || summary.WorkflowRuns[0].EventsCount != 1 {
		t.Fatalf("expected workflow summary counts, got %#v", summary.WorkflowRuns[0])
	}
	if summary.Artifacts[0].Content != "" || !strings.HasSuffix(summary.Artifacts[0].Summary, "...") {
		t.Fatalf("expected session artifact summary only, got %#v", summary.Artifacts[0])
	}
	if summary.Workflow.PendingArguments != "" || !strings.HasSuffix(summary.Workflow.PendingArgumentsSummary, "...") {
		t.Fatalf("expected top-level workflow pending args summarized, got %#v", summary.Workflow)
	}
	if len(summary.PendingApprovals) != 1 || summary.PendingApprovals[0].Arguments != "" || !strings.HasSuffix(summary.PendingApprovals[0].ArgumentsSummary, "...") {
		t.Fatalf("expected pending approvals summarized, got %#v", summary.PendingApprovals)
	}

	full := sessionSnapshotForAPI(snapshot, true)
	if len(full.AgentRuns[0].Events) != 1 || full.AgentRuns[0].Result == nil || len(full.WorkflowRuns[0].CompletedStages) != 1 {
		t.Fatalf("expected full session details when requested, got %#v", full)
	}
	if full.Artifacts[0].Content != "" {
		t.Fatalf("expected top-level session artifact content hidden even in full runtime snapshot")
	}
	if len(full.AgentRuns[0].Result.ResponseMessage.ProviderFields) != 0 || len(full.AgentRuns[0].ResumeContext.Messages[0].ProviderFields) != 0 {
		t.Fatalf("expected agent provider metadata hidden from API snapshot, got %#v", full.AgentRuns[0])
	}
	if len(full.WorkflowRuns[0].PendingResponseMessage.ProviderFields) != 0 || len(full.WorkflowRuns[0].CompletedStages[0].Result.ResponseMessage.ProviderFields) != 0 {
		t.Fatalf("expected workflow provider metadata hidden from API snapshot, got %#v", full.WorkflowRuns[0])
	}
}

func TestRequestWantsFullSessionSupportsFullQuery(t *testing.T) {
	if !requestWantsFullSession(httptest.NewRequest(http.MethodGet, "/api/session?full=1", nil)) {
		t.Fatal("expected full=1 to request full session details")
	}
	if requestWantsFullSession(httptest.NewRequest(http.MethodGet, "/api/session?summary=1", nil)) {
		t.Fatal("expected summary=1 to keep compact session details")
	}
}

func TestHandleSessionSummaryIsSmallerThanFullAndOmitsLargeDetails(t *testing.T) {
	state := session.New(8)
	large := strings.Repeat("summary first payload ", 1200)
	runID := state.StartAgentRun("inspect summary-first session")
	state.AppendAgentRunEvent(runID, session.AgentRunEventSnapshot{
		Seq:      1,
		Type:     "tool_result",
		ToolName: "read_file",
		Content:  large,
	})
	state.CompleteAgentRun(runID, "completed", schema.AgentResult{
		Output: large,
		ToolResults: []schema.ToolResult{{
			CallID:   "call-large",
			ToolName: "read_file",
			Content:  large,
		}},
	})
	state.SetWorkflow(session.WorkflowSnapshot{
		RunID:                   "wf-large",
		Name:                    "plan-fix-audit",
		Status:                  "awaiting_tool_approval",
		NextStage:               "fix",
		PendingCallID:           "call-workflow-large",
		PendingToolName:         "write_file",
		PendingArguments:        large,
		PendingArgumentsSummary: large,
	})
	state.SetPendingApprovals([]session.PendingApprovalSnapshot{{
		CallID:           "call-pending-large",
		ToolName:         "write_file",
		Arguments:        large,
		ArgumentsSummary: large,
		AgentRunID:       runID,
	}})
	state.AddArtifact(session.SessionArtifactSnapshot{
		Kind:     "tool_result",
		ToolName: "read_file",
		Content:  large,
		Summary:  large,
	})
	server := NewServer(newAPITestRuntimeWithStateAndResponses(t, state, nil))

	summaryResponse := httptest.NewRecorder()
	server.ServeHTTP(summaryResponse, httptest.NewRequest(http.MethodGet, "/api/session?summary=1", nil))
	if summaryResponse.Code != http.StatusOK {
		t.Fatalf("expected summary session 200, got %d body=%s", summaryResponse.Code, summaryResponse.Body.String())
	}
	fullResponse := httptest.NewRecorder()
	server.ServeHTTP(fullResponse, httptest.NewRequest(http.MethodGet, "/api/session?full=1", nil))
	if fullResponse.Code != http.StatusOK {
		t.Fatalf("expected full session 200, got %d body=%s", fullResponse.Code, fullResponse.Body.String())
	}
	summaryBody := summaryResponse.Body.String()
	fullBody := fullResponse.Body.String()
	if strings.Contains(summaryBody, large) {
		t.Fatalf("summary session should omit full large payload")
	}
	if !strings.Contains(fullBody, large) {
		t.Fatalf("full session should hydrate large payload")
	}
	if len(summaryBody) >= len(fullBody)/2 {
		t.Fatalf("summary session should be substantially smaller, summary=%d full=%d", len(summaryBody), len(fullBody))
	}
	var summary session.Snapshot
	if err := json.NewDecoder(strings.NewReader(summaryBody)).Decode(&summary); err != nil {
		t.Fatalf("decode summary session: %v", err)
	}
	if len(summary.AgentRuns) != 1 || len(summary.AgentRuns[0].Events) != 0 || summary.AgentRuns[0].Result != nil {
		t.Fatalf("summary session should omit agent run details, got %#v", summary.AgentRuns)
	}
	if len(summary.PendingApprovals) != 1 || summary.PendingApprovals[0].Arguments != "" || summary.PendingApprovals[0].ArgumentsSummary == "" {
		t.Fatalf("summary session should keep pending approval metadata without raw arguments, got %#v", summary.PendingApprovals)
	}
}

func TestServerCollaborationMessageEndpoints(t *testing.T) {
	server := NewServer(newAPITestRuntime(t))
	body := strings.NewReader(`{
		"run_id":"wf-1",
		"stage":"plan",
		"from_agent":"planner",
		"to_agent":"auditor",
		"kind":"handoff",
		"subject":"Review plan",
		"content":"Please review the plan."
	}`)
	request := httptest.NewRequest(http.MethodPost, "/api/collaboration/messages", body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected message save 200, got %d body=%s", response.Code, response.Body.String())
	}
	var saved session.CollaborationMessageSnapshot
	if err := json.NewDecoder(response.Body).Decode(&saved); err != nil {
		t.Fatalf("decode message: %v", err)
	}
	if saved.ID == "" || saved.Kind != "handoff" {
		t.Fatalf("unexpected message: %#v", saved)
	}

	listRequest := httptest.NewRequest(http.MethodGet, "/api/collaboration/messages?run_id=wf-1&agent=auditor", nil)
	listResponse := httptest.NewRecorder()
	server.ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("expected message list 200, got %d body=%s", listResponse.Code, listResponse.Body.String())
	}
	var messages []session.CollaborationMessageSnapshot
	if err := json.NewDecoder(listResponse.Body).Decode(&messages); err != nil {
		t.Fatalf("decode messages: %v", err)
	}
	if len(messages) != 1 || messages[0].ID != saved.ID {
		t.Fatalf("unexpected messages: %#v", messages)
	}
}

func TestServerBlackboardEndpoints(t *testing.T) {
	server := NewServer(newAPITestRuntime(t))
	body := strings.NewReader(`{
		"scope":"workflow",
		"run_id":"wf-1",
		"stage":"plan",
		"agent_id":"planner",
		"kind":"decision",
		"title":"Implementation path",
		"content":"Implement backend first.",
		"tags":["plan","backend"]
	}`)
	request := httptest.NewRequest(http.MethodPost, "/api/collaboration/blackboard", body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected blackboard save 200, got %d body=%s", response.Code, response.Body.String())
	}
	var saved session.BlackboardEntrySnapshot
	if err := json.NewDecoder(response.Body).Decode(&saved); err != nil {
		t.Fatalf("decode blackboard entry: %v", err)
	}
	if saved.ID == "" || saved.Kind != "decision" || saved.Scope != "workflow" {
		t.Fatalf("unexpected blackboard entry: %#v", saved)
	}

	update := strings.NewReader(`{"title":"Implementation path","content":"Backend API first.","kind":"decision","status":"resolved"}`)
	updateRequest := httptest.NewRequest(http.MethodPut, "/api/collaboration/blackboard/"+saved.ID, update)
	updateRequest.Header.Set("Content-Type", "application/json")
	updateResponse := httptest.NewRecorder()
	server.ServeHTTP(updateResponse, updateRequest)
	if updateResponse.Code != http.StatusOK {
		t.Fatalf("expected blackboard update 200, got %d body=%s", updateResponse.Code, updateResponse.Body.String())
	}

	listRequest := httptest.NewRequest(http.MethodGet, "/api/collaboration/blackboard?run_id=wf-1&kind=decision&status=resolved", nil)
	listResponse := httptest.NewRecorder()
	server.ServeHTTP(listResponse, listRequest)
	var entries []session.BlackboardEntrySnapshot
	if err := json.NewDecoder(listResponse.Body).Decode(&entries); err != nil {
		t.Fatalf("decode blackboard entries: %v", err)
	}
	if len(entries) != 1 || entries[0].ID != saved.ID || entries[0].Content != "Backend API first." {
		t.Fatalf("unexpected blackboard entries: %#v", entries)
	}

	deleteRequest := httptest.NewRequest(http.MethodDelete, "/api/collaboration/blackboard/"+saved.ID, nil)
	deleteResponse := httptest.NewRecorder()
	server.ServeHTTP(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("expected blackboard delete 204, got %d body=%s", deleteResponse.Code, deleteResponse.Body.String())
	}
}

func TestServerCollaborationEndpointsAreSummaryFirstWithLazyContent(t *testing.T) {
	root := t.TempDir()
	store := session.NewArtifactObjectStore(root)
	if err := store.Ensure(); err != nil {
		t.Fatalf("Ensure artifact store: %v", err)
	}
	state := session.New(8)
	state.SetArtifactObjectStore(store)
	runtimeRef := newAPITestRuntimeWithStateAndResponses(t, state, nil)
	server := NewServer(runtimeRef)
	largeMessage := strings.Repeat("collaboration endpoint payload ", 480)
	largeEntry := strings.Repeat("blackboard endpoint payload ", 480)

	savedMessage := runtimeRef.AddCollaborationMessage(session.CollaborationMessageSnapshot{
		RunID:     "wf-1",
		Stage:     "plan",
		FromAgent: "planner",
		ToAgent:   "auditor",
		Kind:      "handoff",
		Subject:   "Review large payload",
		Content:   largeMessage,
	})
	if savedMessage.ContentArtifactRef == "" || savedMessage.Content == largeMessage {
		t.Fatalf("expected externalized saved message, got %#v", savedMessage)
	}
	savedEntry := runtimeRef.UpsertBlackboardEntry(session.BlackboardEntrySnapshot{
		Scope:   "workflow",
		RunID:   "wf-1",
		Stage:   "plan",
		AgentID: "planner",
		Kind:    "decision",
		Title:   "Large decision",
		Content: largeEntry,
		Status:  "open",
	})
	if savedEntry.ContentArtifactRef == "" || savedEntry.Content == largeEntry {
		t.Fatalf("expected externalized saved blackboard entry, got %#v", savedEntry)
	}

	messageListRequest := httptest.NewRequest(http.MethodGet, "/api/collaboration/messages?run_id=wf-1", nil)
	messageListResponse := httptest.NewRecorder()
	server.ServeHTTP(messageListResponse, messageListRequest)
	if messageListResponse.Code != http.StatusOK {
		t.Fatalf("expected message list 200, got %d body=%s", messageListResponse.Code, messageListResponse.Body.String())
	}
	var messages []session.CollaborationMessageSnapshot
	if err := json.NewDecoder(messageListResponse.Body).Decode(&messages); err != nil {
		t.Fatalf("decode messages: %v", err)
	}
	if len(messages) != 1 || messages[0].Content == largeMessage || messages[0].ContentArtifactRef == "" {
		t.Fatalf("expected summary-first message list, got %#v", messages)
	}

	messageFullRequest := httptest.NewRequest(http.MethodGet, "/api/collaboration/messages?run_id=wf-1&include_content=1", nil)
	messageFullResponse := httptest.NewRecorder()
	server.ServeHTTP(messageFullResponse, messageFullRequest)
	if messageFullResponse.Code != http.StatusOK {
		t.Fatalf("expected full message list 200, got %d body=%s", messageFullResponse.Code, messageFullResponse.Body.String())
	}
	var fullMessages []session.CollaborationMessageSnapshot
	if err := json.NewDecoder(messageFullResponse.Body).Decode(&fullMessages); err != nil {
		t.Fatalf("decode full messages: %v", err)
	}
	if len(fullMessages) != 1 || fullMessages[0].Content != largeMessage {
		t.Fatalf("expected hydrated message content, got %#v", fullMessages)
	}

	entryListRequest := httptest.NewRequest(http.MethodGet, "/api/collaboration/blackboard?run_id=wf-1", nil)
	entryListResponse := httptest.NewRecorder()
	server.ServeHTTP(entryListResponse, entryListRequest)
	if entryListResponse.Code != http.StatusOK {
		t.Fatalf("expected blackboard list 200, got %d body=%s", entryListResponse.Code, entryListResponse.Body.String())
	}
	var entries []session.BlackboardEntrySnapshot
	if err := json.NewDecoder(entryListResponse.Body).Decode(&entries); err != nil {
		t.Fatalf("decode blackboard entries: %v", err)
	}
	if len(entries) != 1 || entries[0].Content == largeEntry || entries[0].ContentArtifactRef == "" {
		t.Fatalf("expected summary-first blackboard list, got %#v", entries)
	}

	entryFullRequest := httptest.NewRequest(http.MethodGet, "/api/collaboration/blackboard?run_id=wf-1&include_content=1", nil)
	entryFullResponse := httptest.NewRecorder()
	server.ServeHTTP(entryFullResponse, entryFullRequest)
	if entryFullResponse.Code != http.StatusOK {
		t.Fatalf("expected full blackboard list 200, got %d body=%s", entryFullResponse.Code, entryFullResponse.Body.String())
	}
	var fullEntries []session.BlackboardEntrySnapshot
	if err := json.NewDecoder(entryFullResponse.Body).Decode(&fullEntries); err != nil {
		t.Fatalf("decode full blackboard entries: %v", err)
	}
	if len(fullEntries) != 1 || fullEntries[0].Content != largeEntry {
		t.Fatalf("expected hydrated blackboard content, got %#v", fullEntries)
	}
}

func TestServerBlackboardLifecycleEndpoints(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	server := NewServer(runtimeRef)
	saved := runtimeRef.UpsertBlackboardEntry(session.BlackboardEntrySnapshot{
		Scope:   "workflow",
		RunID:   "wf-1",
		Stage:   "audit",
		AgentID: "reviewer",
		Kind:    "team_unresolved_question",
		Title:   "Rollout question",
		Content: "Who approves rollout?",
		Status:  "open",
		Tags:    []string{"software-task-team"},
		Metadata: map[string]string{
			"team": "software-task-team",
		},
	})

	resolveBody := strings.NewReader(`{"note":"approved by owner","metadata":{"owner":"lead"}}`)
	resolveRequest := httptest.NewRequest(http.MethodPost, "/api/collaboration/blackboard/"+saved.ID+"/resolve", resolveBody)
	resolveRequest.Header.Set("Content-Type", "application/json")
	resolveResponse := httptest.NewRecorder()
	server.ServeHTTP(resolveResponse, resolveRequest)
	if resolveResponse.Code != http.StatusOK {
		t.Fatalf("expected blackboard resolve 200, got %d body=%s", resolveResponse.Code, resolveResponse.Body.String())
	}
	var resolved session.BlackboardEntrySnapshot
	if err := json.NewDecoder(resolveResponse.Body).Decode(&resolved); err != nil {
		t.Fatalf("decode resolved blackboard entry: %v", err)
	}
	if resolved.Status != "resolved" || resolved.Metadata["team"] != "software-task-team" || resolved.Metadata["owner"] != "lead" || resolved.Metadata["resolve_note"] == "" {
		t.Fatalf("unexpected resolved entry: %#v", resolved)
	}

	stateRequest := httptest.NewRequest(http.MethodGet, "/api/team-state?run_id=wf-1&team=software-task-team", nil)
	stateResponse := httptest.NewRecorder()
	server.ServeHTTP(stateResponse, stateRequest)
	if stateResponse.Code != http.StatusOK {
		t.Fatalf("expected team-state 200, got %d body=%s", stateResponse.Code, stateResponse.Body.String())
	}
	var resolvedState agent.TeamState
	if err := json.NewDecoder(stateResponse.Body).Decode(&resolvedState); err != nil {
		t.Fatalf("decode resolved team state: %v", err)
	}
	if blackboardEntriesContain(resolvedState.UnresolvedItems, saved.ID) {
		t.Fatalf("resolved entry should not be unresolved: %#v", resolvedState.UnresolvedItems)
	}
	if !blackboardEntriesContain(resolvedState.Questions, saved.ID) {
		t.Fatalf("resolved question should remain in questions: %#v", resolvedState.Questions)
	}

	reopenRequest := httptest.NewRequest(http.MethodPost, "/api/collaboration/blackboard/"+saved.ID+"/reopen", strings.NewReader(`{"note":"needs more review"}`))
	reopenRequest.Header.Set("Content-Type", "application/json")
	reopenResponse := httptest.NewRecorder()
	server.ServeHTTP(reopenResponse, reopenRequest)
	if reopenResponse.Code != http.StatusOK {
		t.Fatalf("expected blackboard reopen 200, got %d body=%s", reopenResponse.Code, reopenResponse.Body.String())
	}
	var reopened session.BlackboardEntrySnapshot
	if err := json.NewDecoder(reopenResponse.Body).Decode(&reopened); err != nil {
		t.Fatalf("decode reopened blackboard entry: %v", err)
	}
	if reopened.Status != "open" || reopened.Metadata["reopen_note"] == "" {
		t.Fatalf("unexpected reopened entry: %#v", reopened)
	}

	reopenedStateRequest := httptest.NewRequest(http.MethodGet, "/api/team-state?run_id=wf-1&team=software-task-team", nil)
	reopenedStateResponse := httptest.NewRecorder()
	server.ServeHTTP(reopenedStateResponse, reopenedStateRequest)
	var reopenedState agent.TeamState
	if err := json.NewDecoder(reopenedStateResponse.Body).Decode(&reopenedState); err != nil {
		t.Fatalf("decode reopened team state: %v", err)
	}
	if !blackboardEntriesContain(reopenedState.UnresolvedItems, saved.ID) {
		t.Fatalf("reopened entry should be unresolved: %#v", reopenedState.UnresolvedItems)
	}

	assignRequest := httptest.NewRequest(http.MethodPost, "/api/collaboration/blackboard/"+saved.ID+"/assign", strings.NewReader(`{"assigned_agent":"auditor","owner_role":"reviewer","note":"review owns this"}`))
	assignRequest.Header.Set("Content-Type", "application/json")
	assignResponse := httptest.NewRecorder()
	server.ServeHTTP(assignResponse, assignRequest)
	if assignResponse.Code != http.StatusOK {
		t.Fatalf("expected blackboard assign 200, got %d body=%s", assignResponse.Code, assignResponse.Body.String())
	}
	var assigned session.BlackboardEntrySnapshot
	if err := json.NewDecoder(assignResponse.Body).Decode(&assigned); err != nil {
		t.Fatalf("decode assigned blackboard entry: %v", err)
	}
	if assigned.Status != "open" || assigned.AgentID != "auditor" || assigned.Metadata["assigned_agent"] != "auditor" || assigned.Metadata["owner_role"] != "reviewer" {
		t.Fatalf("unexpected assigned entry: %#v", assigned)
	}

	assignedStateRequest := httptest.NewRequest(http.MethodGet, "/api/team-state?run_id=wf-1&team=software-task-team", nil)
	assignedStateResponse := httptest.NewRecorder()
	server.ServeHTTP(assignedStateResponse, assignedStateRequest)
	var assignedState agent.TeamState
	if err := json.NewDecoder(assignedStateResponse.Body).Decode(&assignedState); err != nil {
		t.Fatalf("decode assigned team state: %v", err)
	}
	if assignedState.ActiveOwner != "auditor" || !blackboardEntriesContain(assignedState.Assignments, saved.ID) {
		t.Fatalf("expected assignment to drive active owner, got %#v", assignedState)
	}

	escalateRequest := httptest.NewRequest(http.MethodPost, "/api/collaboration/blackboard/"+saved.ID+"/escalate", strings.NewReader(`{"escalate_to":"planner","severity":"high","note":"needs decision"}`))
	escalateRequest.Header.Set("Content-Type", "application/json")
	escalateResponse := httptest.NewRecorder()
	server.ServeHTTP(escalateResponse, escalateRequest)
	if escalateResponse.Code != http.StatusOK {
		t.Fatalf("expected blackboard escalate 200, got %d body=%s", escalateResponse.Code, escalateResponse.Body.String())
	}
	var escalated session.BlackboardEntrySnapshot
	if err := json.NewDecoder(escalateResponse.Body).Decode(&escalated); err != nil {
		t.Fatalf("decode escalated blackboard entry: %v", err)
	}
	if escalated.Status != "escalated" || escalated.AgentID != "planner" || escalated.Metadata["escalated_to"] != "planner" || escalated.Metadata["severity"] != "high" {
		t.Fatalf("unexpected escalated entry: %#v", escalated)
	}

	escalatedStateRequest := httptest.NewRequest(http.MethodGet, "/api/team-state?run_id=wf-1&team=software-task-team", nil)
	escalatedStateResponse := httptest.NewRecorder()
	server.ServeHTTP(escalatedStateResponse, escalatedStateRequest)
	var escalatedState agent.TeamState
	if err := json.NewDecoder(escalatedStateResponse.Body).Decode(&escalatedState); err != nil {
		t.Fatalf("decode escalated team state: %v", err)
	}
	if escalatedState.ActiveOwner != "planner" || !blackboardEntriesContain(escalatedState.Escalations, saved.ID) || !blackboardEntriesContain(escalatedState.UnresolvedItems, saved.ID) {
		t.Fatalf("expected escalation to remain unresolved and drive active owner, got %#v", escalatedState)
	}
}

func blackboardEntriesContain(entries []session.BlackboardEntrySnapshot, id string) bool {
	for _, entry := range entries {
		if entry.ID == id {
			return true
		}
	}
	return false
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
	for _, event := range decodeSSEEvents(t, body) {
		if event.Name != string(event.Payload.Type) {
			t.Fatalf("expected SSE event name to match payload type, got name=%q payload=%#v", event.Name, event.Payload)
		}
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
	if !strings.Contains(body, `"run_id"`) {
		t.Fatalf("expected workflow stream to include run_id, got %q", body)
	}
	events := decodeSSEEvents(t, body)
	for _, event := range events {
		if event.Name != string(event.Payload.Type) {
			t.Fatalf("expected SSE event name to match payload type, got name=%q payload=%#v", event.Name, event.Payload)
		}
	}
	if !sseEventsContain(events, schema.StreamEventWorkflowResult) {
		t.Fatalf("expected decoded workflow_result event, got %#v", events)
	}
}

func TestServerStreamEndpointsEmitWorkspaceRequiredActionEvent(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	workspaceState := workspace.New(`D:\Projects\demo`, false)
	runtimeRef.SetWorkspaceConfirmed(workspaceState.Confirmed())
	server := NewServerWithWorkspace(runtimeRef, workspaceState)

	runRequest := httptest.NewRequest(http.MethodPost, "/api/run/stream", strings.NewReader(`{"input":"write code for a calculator"}`))
	runRequest.Header.Set("Content-Type", "application/json")
	runResponse := httptest.NewRecorder()
	server.ServeHTTP(runResponse, runRequest)
	if runResponse.Code != http.StatusOK {
		t.Fatalf("expected workspace-required stream 200, got %d body=%s", runResponse.Code, runResponse.Body.String())
	}
	runEvents := decodeSSEEvents(t, runResponse.Body.String())
	if len(runEvents) != 1 || runEvents[0].Payload.Type != schema.StreamEventError || !runEvents[0].Payload.IsError || !runEvents[0].Payload.NeedsAction || !strings.Contains(runEvents[0].Payload.Content, "workspace confirmation required") {
		t.Fatalf("expected actionable workspace-required error event, got %#v body=%s", runEvents, runResponse.Body.String())
	}

	workflowRequest := httptest.NewRequest(http.MethodPost, "/api/workflows/plan-fix-audit/stream", strings.NewReader(`{"input":"ship it"}`))
	workflowRequest.Header.Set("Content-Type", "application/json")
	workflowResponse := httptest.NewRecorder()
	server.ServeHTTP(workflowResponse, workflowRequest)
	if workflowResponse.Code != http.StatusOK {
		t.Fatalf("expected workflow workspace-required stream 200, got %d body=%s", workflowResponse.Code, workflowResponse.Body.String())
	}
	workflowEvents := decodeSSEEvents(t, workflowResponse.Body.String())
	if len(workflowEvents) != 1 || workflowEvents[0].Payload.Type != schema.StreamEventError || !workflowEvents[0].Payload.IsError || !workflowEvents[0].Payload.NeedsAction || !strings.Contains(workflowEvents[0].Payload.Content, "workflow execution runs workspace-scoped stages") {
		t.Fatalf("expected actionable workflow workspace-required error event, got %#v body=%s", workflowEvents, workflowResponse.Body.String())
	}
}

func TestServerWorkflowEndpointCanRunWithoutPreApproval(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	dir := filepath.Join(runtimeRef.RuntimeHome(), "workflows", "checkpoint-flow")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir workflow: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "workflow.yaml"), []byte(`name: checkpoint-flow
stages:
  - name: checkpoint
    node_type: checkpoint
    params:
      prompt: review before continuing
    next: [plan]
  - name: plan
    agent: planner
    skill: execution-plan
`), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}
	server := NewServer(runtimeRef)
	request := httptest.NewRequest(http.MethodPost, "/api/workflows/checkpoint-flow", strings.NewReader(`{"input":"ship it","approve":false}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected workflow 200, got %d body=%s", response.Code, response.Body.String())
	}
	var paused agent.WorkflowResult
	if err := json.NewDecoder(response.Body).Decode(&paused); err != nil {
		t.Fatalf("decode paused workflow: %v", err)
	}
	if paused.Status != "awaiting_approval" || paused.NextStage != "checkpoint" || paused.RunID == "" {
		t.Fatalf("expected checkpoint pause, got %#v", paused)
	}
	actionsRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+paused.RunID+"/actions", nil)
	actionsResponse := httptest.NewRecorder()
	server.ServeHTTP(actionsResponse, actionsRequest)
	if actionsResponse.Code != http.StatusOK {
		t.Fatalf("expected workflow actions 200, got %d body=%s", actionsResponse.Code, actionsResponse.Body.String())
	}
	var actions []workflowRunAction
	if err := json.NewDecoder(actionsResponse.Body).Decode(&actions); err != nil {
		t.Fatalf("decode workflow actions: %v", err)
	}
	if !workflowActionAvailable(actions, "approve_stage", true) || !workflowActionAvailable(actions, "cancel", true) {
		t.Fatalf("expected durable approval and cancel actions, got %#v", actions)
	}

	approveRequest := httptest.NewRequest(http.MethodPost, "/api/workflow-runs/"+paused.RunID+"/approve", nil)
	approveResponse := httptest.NewRecorder()
	server.ServeHTTP(approveResponse, approveRequest)
	if approveResponse.Code != http.StatusOK {
		t.Fatalf("expected approval resume 200, got %d body=%s", approveResponse.Code, approveResponse.Body.String())
	}
	var resumed agent.WorkflowResult
	if err := json.NewDecoder(approveResponse.Body).Decode(&resumed); err != nil {
		t.Fatalf("decode resumed workflow: %v", err)
	}
	if resumed.Status != "completed" || resumed.RunID != paused.RunID || len(resumed.CompletedStages) != 2 {
		t.Fatalf("expected approved workflow to complete, got %#v", resumed)
	}
	if resumed.CompletedStages[0].Stage != "checkpoint" || resumed.CompletedStages[1].Stage != "plan" {
		t.Fatalf("expected checkpoint then plan stages, got %#v", resumed.CompletedStages)
	}
}

func TestServerWorkflowRunApprovalResumesBuiltInWorkflowAfterSessionLoad(t *testing.T) {
	firstState := session.New(8)
	firstRuntime := newAPITestRuntimeWithStateAndResponses(t, firstState, []schema.ChatResponse{
		{Message: schema.Message{Content: "durable plan ready"}, Usage: schema.TokenUsage{PromptTokens: 5, OutputTokens: 2}},
	})
	firstServer := NewServer(firstRuntime)
	startRequest := httptest.NewRequest(http.MethodPost, "/api/workflows/plan-fix-audit", strings.NewReader(`{"input":"ship it","approve":false}`))
	startRequest.Header.Set("Content-Type", "application/json")
	startResponse := httptest.NewRecorder()
	firstServer.ServeHTTP(startResponse, startRequest)
	if startResponse.Code != http.StatusOK {
		t.Fatalf("expected workflow start 200, got %d body=%s", startResponse.Code, startResponse.Body.String())
	}
	var paused agent.WorkflowResult
	if err := json.NewDecoder(startResponse.Body).Decode(&paused); err != nil {
		t.Fatalf("decode paused workflow: %v", err)
	}
	if paused.Status != "awaiting_approval" || paused.NextStage != "fix" || paused.RunID == "" || len(paused.CompletedStages) != 1 {
		t.Fatalf("expected built-in workflow awaiting fix approval, got %#v", paused)
	}

	sessionPath := filepath.Join(t.TempDir(), "session.json")
	if err := firstState.Save(sessionPath); err != nil {
		t.Fatalf("save session: %v", err)
	}
	loadedState := session.New(8)
	if err := loadedState.Load(sessionPath); err != nil {
		t.Fatalf("load session: %v", err)
	}
	secondRuntime := newAPITestRuntimeWithStateAndResponses(t, loadedState, []schema.ChatResponse{
		{Message: schema.Message{Content: "fix after restart"}, Usage: schema.TokenUsage{PromptTokens: 7, OutputTokens: 3}},
		{Message: schema.Message{Content: "audit after restart"}, Usage: schema.TokenUsage{PromptTokens: 9, OutputTokens: 4}},
	})
	secondServer := NewServer(secondRuntime)

	actionsRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+paused.RunID+"/actions", nil)
	actionsResponse := httptest.NewRecorder()
	secondServer.ServeHTTP(actionsResponse, actionsRequest)
	if actionsResponse.Code != http.StatusOK {
		t.Fatalf("expected workflow actions 200 after reload, got %d body=%s", actionsResponse.Code, actionsResponse.Body.String())
	}
	var actions []workflowRunAction
	if err := json.NewDecoder(actionsResponse.Body).Decode(&actions); err != nil {
		t.Fatalf("decode workflow actions: %v", err)
	}
	if !workflowActionAvailable(actions, "approve_stage", true) {
		t.Fatalf("expected durable approve_stage after reload, got %#v", actions)
	}

	approveRequest := httptest.NewRequest(http.MethodPost, "/api/workflow-runs/"+paused.RunID+"/approve", nil)
	approveResponse := httptest.NewRecorder()
	secondServer.ServeHTTP(approveResponse, approveRequest)
	if approveResponse.Code != http.StatusOK {
		t.Fatalf("expected built-in approval resume 200 after reload, got %d body=%s", approveResponse.Code, approveResponse.Body.String())
	}
	var resumed agent.WorkflowResult
	if err := json.NewDecoder(approveResponse.Body).Decode(&resumed); err != nil {
		t.Fatalf("decode resumed workflow: %v", err)
	}
	if resumed.Status != "completed" || resumed.RunID != paused.RunID || len(resumed.CompletedStages) != 3 {
		t.Fatalf("expected built-in workflow to complete after reload, got %#v", resumed)
	}
	if resumed.CompletedStages[0].Result.Output != "durable plan ready" ||
		resumed.CompletedStages[1].Result.Output != "fix after restart" ||
		resumed.CompletedStages[2].Result.Output != "audit after restart" {
		t.Fatalf("expected resumed stages to preserve plan and run fix/audit, got %#v", resumed.CompletedStages)
	}
	reloadedRun := workflowRunSnapshotByID(t, secondRuntime, paused.RunID)
	if reloadedRun.Status != "completed" || len(reloadedRun.CompletedStages) != 3 {
		t.Fatalf("expected completed persisted run after reload resume, got %#v", reloadedRun)
	}
}

func TestServerWorkflowRunInputEndpointResumesManualInputGate(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	dir := filepath.Join(runtimeRef.RuntimeHome(), "workflows", "manual-input-flow")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir workflow: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "workflow.yaml"), []byte(`name: manual-input-flow
stages:
  - name: collect
    node_type: input_gate
    params:
      manual: true
      fields: target
      required: target
      prompt: provide target
    next: [plan]
  - name: plan
    agent: planner
    skill: execution-plan
    input:
      target: stages.collect.outputs.target
`), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}
	server := NewServer(runtimeRef)
	startRequest := httptest.NewRequest(http.MethodPost, "/api/workflows/manual-input-flow", strings.NewReader(`{"input":"plan with manual input"}`))
	startRequest.Header.Set("Content-Type", "application/json")
	startResponse := httptest.NewRecorder()
	server.ServeHTTP(startResponse, startRequest)
	if startResponse.Code != http.StatusOK {
		t.Fatalf("expected workflow start 200, got %d body=%s", startResponse.Code, startResponse.Body.String())
	}
	var paused agent.WorkflowResult
	if err := json.NewDecoder(startResponse.Body).Decode(&paused); err != nil {
		t.Fatalf("decode paused workflow: %v", err)
	}
	if paused.Status != "awaiting_input" || !paused.PendingInput || paused.RunID == "" {
		t.Fatalf("expected awaiting input result, got %#v", paused)
	}
	if len(paused.PendingFields) != 1 || paused.PendingFields[0].Name != "target" || !paused.PendingFields[0].Required {
		t.Fatalf("expected pending input field schema, got %#v", paused.PendingFields)
	}
	actionsRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+paused.RunID+"/actions", nil)
	actionsResponse := httptest.NewRecorder()
	server.ServeHTTP(actionsResponse, actionsRequest)
	if actionsResponse.Code != http.StatusOK {
		t.Fatalf("expected workflow actions 200, got %d body=%s", actionsResponse.Code, actionsResponse.Body.String())
	}
	var actions []workflowRunAction
	if err := json.NewDecoder(actionsResponse.Body).Decode(&actions); err != nil {
		t.Fatalf("decode workflow actions: %v", err)
	}
	if !workflowActionAvailable(actions, "submit_input", true) || !workflowActionAvailable(actions, "cancel", true) || !workflowActionAvailable(actions, "retry", true) {
		t.Fatalf("expected durable input/cancel/retry actions, got %#v", actions)
	}
	if action := workflowActionByName(actions, "submit_input"); action == nil || !action.Background || action.EventsPath != "/api/workflow-runs/"+paused.RunID+"/events/stream" {
		t.Fatalf("expected submit_input to advertise background event stream, got %#v", action)
	}
	if action := workflowActionByName(actions, "submit_input"); action == nil ||
		action.Kind != "manual_input" ||
		!action.SupportsBackground ||
		!action.SupportsStream ||
		!action.AcceptsBody ||
		!action.RequiresBody ||
		action.BodySchema == nil {
		t.Fatalf("expected submit_input to advertise machine-readable action metadata, got %#v", action)
	}

	missingRequest := httptest.NewRequest(http.MethodPost, "/api/workflow-runs/"+paused.RunID+"/input", strings.NewReader(`{"inputs":{}}`))
	missingRequest.Header.Set("Content-Type", "application/json")
	missingResponse := httptest.NewRecorder()
	server.ServeHTTP(missingResponse, missingRequest)
	if missingResponse.Code != http.StatusBadRequest || !strings.Contains(missingResponse.Body.String(), "target") {
		t.Fatalf("expected missing required input 400, got %d body=%s", missingResponse.Code, missingResponse.Body.String())
	}

	inputRequest := httptest.NewRequest(http.MethodPost, "/api/workflow-runs/"+paused.RunID+"/input", strings.NewReader(`{"inputs":{"target":"auth"}}`))
	inputRequest.Header.Set("Content-Type", "application/json")
	inputResponse := httptest.NewRecorder()
	server.ServeHTTP(inputResponse, inputRequest)
	if inputResponse.Code != http.StatusOK {
		t.Fatalf("expected input resume 200, got %d body=%s", inputResponse.Code, inputResponse.Body.String())
	}
	var resumed agent.WorkflowResult
	if err := json.NewDecoder(inputResponse.Body).Decode(&resumed); err != nil {
		t.Fatalf("decode resumed workflow: %v", err)
	}
	if resumed.Status != "completed" || resumed.RunID != paused.RunID || len(resumed.CompletedStages) != 2 {
		t.Fatalf("expected resumed workflow completion, got %#v", resumed)
	}
	if got := resumed.CompletedStages[0].Output.Variables["target"]; got != "auth" {
		t.Fatalf("expected submitted input in control stage output, got %#v", resumed.CompletedStages[0].Output)
	}
	run := workflowRunSnapshotByID(t, runtimeRef, paused.RunID)
	if len(run.PendingFields) != 0 {
		t.Fatalf("expected pending fields cleared after completion, got %#v", run.PendingFields)
	}
}

func TestServerWorkflowRunInputEndpointResumesAfterSessionLoad(t *testing.T) {
	firstState := session.New(8)
	firstRuntime := newAPITestRuntimeWithStateAndResponses(t, firstState, nil)
	dir := filepath.Join(firstRuntime.RuntimeHome(), "workflows", "manual-input-reload")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir workflow: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "workflow.yaml"), []byte(`name: manual-input-reload
stages:
  - name: collect
    node_type: input_gate
    params:
      manual: true
      fields_json: |
        {"fields":[
          {"name":"target","type":"text","required":true},
          {"name":"depth","type":"integer","required":true}
        ]}
    next: [plan]
  - name: plan
    agent: planner
    skill: execution-plan
    input:
      target: stages.collect.outputs.target
      depth: stages.collect.outputs.depth
`), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}
	firstServer := NewServer(firstRuntime)
	startRequest := httptest.NewRequest(http.MethodPost, "/api/workflows/manual-input-reload", strings.NewReader(`{"input":"plan with manual input after reload"}`))
	startRequest.Header.Set("Content-Type", "application/json")
	startResponse := httptest.NewRecorder()
	firstServer.ServeHTTP(startResponse, startRequest)
	if startResponse.Code != http.StatusOK {
		t.Fatalf("expected workflow start 200, got %d body=%s", startResponse.Code, startResponse.Body.String())
	}
	var paused agent.WorkflowResult
	if err := json.NewDecoder(startResponse.Body).Decode(&paused); err != nil {
		t.Fatalf("decode paused workflow: %v", err)
	}
	if paused.Status != "awaiting_input" || paused.RunID == "" || len(paused.PendingFields) != 2 {
		t.Fatalf("expected input pause before reload, got %#v", paused)
	}

	sessionPath := filepath.Join(t.TempDir(), "session.json")
	if err := firstState.Save(sessionPath); err != nil {
		t.Fatalf("save session: %v", err)
	}
	loadedState := session.New(8)
	if err := loadedState.Load(sessionPath); err != nil {
		t.Fatalf("load session: %v", err)
	}
	secondRuntime := newAPITestRuntimeWithStateHomeAndResponses(t, loadedState, firstRuntime.RuntimeHome(), []schema.ChatResponse{
		{Message: schema.Message{Content: "manual input resumed after reload"}},
	})
	secondServer := NewServer(secondRuntime)

	actionsRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+paused.RunID+"/actions", nil)
	actionsResponse := httptest.NewRecorder()
	secondServer.ServeHTTP(actionsResponse, actionsRequest)
	if actionsResponse.Code != http.StatusOK {
		t.Fatalf("expected actions 200 after reload, got %d body=%s", actionsResponse.Code, actionsResponse.Body.String())
	}
	var actions []workflowRunAction
	if err := json.NewDecoder(actionsResponse.Body).Decode(&actions); err != nil {
		t.Fatalf("decode actions after reload: %v", err)
	}
	if !workflowActionAvailable(actions, "submit_input", true) || !workflowActionAvailable(actions, "cancel", true) {
		t.Fatalf("expected durable input actions after reload, got %#v", actions)
	}

	inputRequest := httptest.NewRequest(http.MethodPost, "/api/workflow-runs/"+paused.RunID+"/input", strings.NewReader(`{"inputs":{"target":"auth","depth":3}}`))
	inputRequest.Header.Set("Content-Type", "application/json")
	inputResponse := httptest.NewRecorder()
	secondServer.ServeHTTP(inputResponse, inputRequest)
	if inputResponse.Code != http.StatusOK {
		t.Fatalf("expected input resume 200 after reload, got %d body=%s", inputResponse.Code, inputResponse.Body.String())
	}
	var resumed agent.WorkflowResult
	if err := json.NewDecoder(inputResponse.Body).Decode(&resumed); err != nil {
		t.Fatalf("decode resumed workflow: %v", err)
	}
	if resumed.Status != "completed" || resumed.RunID != paused.RunID || len(resumed.CompletedStages) != 2 {
		t.Fatalf("expected reloaded input workflow to complete, got %#v", resumed)
	}
	if resumed.CompletedStages[0].Output.Values["depth"] != float64(3) || resumed.CompletedStages[1].InputValues["depth"] != float64(3) {
		t.Fatalf("expected typed input value to survive reload resume, got %#v", resumed.CompletedStages)
	}
	run := workflowRunSnapshotByID(t, secondRuntime, paused.RunID)
	if run.Status != "completed" || len(run.PendingFields) != 0 {
		t.Fatalf("expected completed run with pending fields cleared after reload resume, got %#v", run)
	}
	var inputEvent bool
	for _, event := range run.Events {
		if event.Type == "workflow_input_submitted" && event.Stage == "collect" {
			inputEvent = true
		}
	}
	if !inputEvent {
		t.Fatalf("expected input submission event after reload resume, got %#v", run.Events)
	}
}

func TestServerWorkflowRunInputEndpointResumesAfterSessionLoadWithExternalizedStageValues(t *testing.T) {
	root := t.TempDir()
	store := session.NewArtifactObjectStore(root)
	if err := store.Ensure(); err != nil {
		t.Fatalf("Ensure artifact store: %v", err)
	}
	firstState := session.New(8)
	firstState.SetArtifactObjectStore(store)
	firstRuntime := newAPITestRuntimeWithStateAndResponses(t, firstState, nil)
	dir := filepath.Join(firstRuntime.RuntimeHome(), "workflows", "manual-input-reload-large")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir workflow: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "workflow.yaml"), []byte(`name: manual-input-reload-large
stages:
  - name: collect
    node_type: input_gate
    params:
      manual: true
      fields_json: |
        {"fields":[
          {"name":"notes","type":"textarea","required":true},
          {"name":"payload","type":"object","required":true}
        ]}
    next: [review]
  - name: review
    node_type: input_gate
    params:
      manual: true
      fields_json: |
        {"fields":[
          {"name":"depth","type":"integer","required":true}
        ]}
    next: [plan]
  - name: plan
    agent: planner
    skill: execution-plan
    input:
      notes: stages.collect.outputs.notes
      payload: stages.collect.outputs.payload
      depth: stages.review.outputs.depth
`), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}
	firstServer := NewServer(firstRuntime)
	startRequest := httptest.NewRequest(http.MethodPost, "/api/workflows/manual-input-reload-large", strings.NewReader(`{"input":"plan with large manual input after reload"}`))
	startRequest.Header.Set("Content-Type", "application/json")
	startResponse := httptest.NewRecorder()
	firstServer.ServeHTTP(startResponse, startRequest)
	if startResponse.Code != http.StatusOK {
		t.Fatalf("expected workflow start 200, got %d body=%s", startResponse.Code, startResponse.Body.String())
	}
	var firstPaused agent.WorkflowResult
	if err := json.NewDecoder(startResponse.Body).Decode(&firstPaused); err != nil {
		t.Fatalf("decode first paused workflow: %v", err)
	}
	if firstPaused.Status != "awaiting_input" || firstPaused.RunID == "" || len(firstPaused.PendingFields) != 2 {
		t.Fatalf("expected initial input pause, got %#v", firstPaused)
	}

	large := strings.Repeat("workflow stage payload ", 640)
	firstBody, err := json.Marshal(map[string]any{
		"inputs": map[string]any{
			"notes":   large,
			"payload": map[string]any{"risk": "low", "scope": "auth"},
		},
	})
	if err != nil {
		t.Fatalf("marshal first input body: %v", err)
	}
	firstInputRequest := httptest.NewRequest(http.MethodPost, "/api/workflow-runs/"+firstPaused.RunID+"/input", strings.NewReader(string(firstBody)))
	firstInputRequest.Header.Set("Content-Type", "application/json")
	firstInputResponse := httptest.NewRecorder()
	firstServer.ServeHTTP(firstInputResponse, firstInputRequest)
	if firstInputResponse.Code != http.StatusOK {
		t.Fatalf("expected first input resume 200, got %d body=%s", firstInputResponse.Code, firstInputResponse.Body.String())
	}
	var secondPaused agent.WorkflowResult
	if err := json.NewDecoder(firstInputResponse.Body).Decode(&secondPaused); err != nil {
		t.Fatalf("decode second paused workflow: %v", err)
	}
	if secondPaused.Status != "awaiting_input" || secondPaused.RunID != firstPaused.RunID || len(secondPaused.CompletedStages) != 1 || secondPaused.NextStage != "review" {
		t.Fatalf("expected review stage pause after first input, got %#v", secondPaused)
	}
	storedBeforeSave := workflowRunSnapshotByID(t, firstRuntime, secondPaused.RunID)
	if len(storedBeforeSave.CompletedStages) != 1 || len(storedBeforeSave.CompletedStages[0].OutputValues) != 0 || storedBeforeSave.CompletedStages[0].OutputValuesArtifactRef == "" || !storedBeforeSave.CompletedStages[0].OutputValuesExternalized {
		t.Fatalf("expected first stage output values to externalize before save, got %#v", storedBeforeSave.CompletedStages)
	}

	sessionPath := filepath.Join(root, "session.json")
	if err := firstState.Save(sessionPath); err != nil {
		t.Fatalf("save session: %v", err)
	}
	loadedState := session.New(8)
	loadedState.SetArtifactObjectStore(store)
	if err := loadedState.Load(sessionPath); err != nil {
		t.Fatalf("load session: %v", err)
	}
	loadedSnapshot := loadedState.Snapshot()
	run, ok := workflowRunByID(loadedSnapshot.WorkflowRuns, secondPaused.RunID)
	if !ok || len(run.CompletedStages) != 1 || len(run.CompletedStages[0].OutputValues) != 0 || run.CompletedStages[0].OutputValuesArtifactRef == "" {
		t.Fatalf("expected loaded session to keep externalized stage value refs, got %#v ok=%t", run, ok)
	}

	secondRuntime := newAPITestRuntimeWithStateHomeAndResponses(t, loadedState, firstRuntime.RuntimeHome(), []schema.ChatResponse{
		{Message: schema.Message{Content: "manual input resumed after reload"}},
	})
	secondServer := NewServer(secondRuntime)
	secondInputRequest := httptest.NewRequest(http.MethodPost, "/api/workflow-runs/"+secondPaused.RunID+"/input", strings.NewReader(`{"inputs":{"depth":3}}`))
	secondInputRequest.Header.Set("Content-Type", "application/json")
	secondInputResponse := httptest.NewRecorder()
	secondServer.ServeHTTP(secondInputResponse, secondInputRequest)
	if secondInputResponse.Code != http.StatusOK {
		t.Fatalf("expected second input resume 200, got %d body=%s", secondInputResponse.Code, secondInputResponse.Body.String())
	}
	var resumed agent.WorkflowResult
	if err := json.NewDecoder(secondInputResponse.Body).Decode(&resumed); err != nil {
		t.Fatalf("decode resumed workflow: %v", err)
	}
	if resumed.Status != "completed" || resumed.RunID != secondPaused.RunID || len(resumed.CompletedStages) != 3 {
		t.Fatalf("expected resumed workflow completion, got %#v", resumed)
	}
	if strings.TrimSpace(fmt.Sprint(resumed.CompletedStages[2].InputValues["notes"])) != strings.TrimSpace(large) || fmt.Sprint(resumed.CompletedStages[2].InputValues["depth"]) != "3" {
		t.Fatalf("expected downstream plan inputs to include hydrated stage values, got %#v", resumed.CompletedStages[2].InputValues)
	}
	payload, ok := resumed.CompletedStages[2].InputValues["payload"].(map[string]any)
	if !ok || payload["risk"] != "low" || payload["scope"] != "auth" {
		t.Fatalf("expected hydrated payload object in downstream plan inputs, got %#v", resumed.CompletedStages[2].InputValues["payload"])
	}
}

func TestServerWorkflowRunInputEndpointPreservesTypedValues(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	dir := filepath.Join(runtimeRef.RuntimeHome(), "workflows", "typed-input-flow")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir workflow: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "workflow.yaml"), []byte(`name: typed-input-flow
stages:
  - name: collect
    node_type: input_gate
    params:
      manual: true
      fields_json: |
        {"fields":[
          {"name":"depth","type":"integer","required":true},
          {"name":"enabled","type":"boolean"},
          {"name":"payload","type":"object","required":true},
          {"name":"tags","type":"select","options":["api","auth"],"multiple":true}
        ]}
    next: [plan]
  - name: plan
    agent: planner
    skill: execution-plan
    input:
      depth: stages.collect.outputs.depth
      enabled: stages.collect.outputs.enabled
      payload: stages.collect.outputs.payload
      tags: stages.collect.outputs.tags
`), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}
	server := NewServer(runtimeRef)
	startRequest := httptest.NewRequest(http.MethodPost, "/api/workflows/typed-input-flow", strings.NewReader(`{"input":"plan typed input"}`))
	startRequest.Header.Set("Content-Type", "application/json")
	startResponse := httptest.NewRecorder()
	server.ServeHTTP(startResponse, startRequest)
	if startResponse.Code != http.StatusOK {
		t.Fatalf("expected workflow start 200, got %d body=%s", startResponse.Code, startResponse.Body.String())
	}
	var paused agent.WorkflowResult
	if err := json.NewDecoder(startResponse.Body).Decode(&paused); err != nil {
		t.Fatalf("decode paused workflow: %v", err)
	}
	if paused.Status != "awaiting_input" || paused.RunID == "" {
		t.Fatalf("expected awaiting input, got %#v", paused)
	}

	inputRequest := httptest.NewRequest(http.MethodPost, "/api/workflow-runs/"+paused.RunID+"/input", strings.NewReader(`{"inputs":{"depth":2,"enabled":true,"payload":{"risk":"low"},"tags":["api","auth"]}}`))
	inputRequest.Header.Set("Content-Type", "application/json")
	inputResponse := httptest.NewRecorder()
	server.ServeHTTP(inputResponse, inputRequest)
	if inputResponse.Code != http.StatusOK {
		t.Fatalf("expected input resume 200, got %d body=%s", inputResponse.Code, inputResponse.Body.String())
	}
	var resumed agent.WorkflowResult
	if err := json.NewDecoder(inputResponse.Body).Decode(&resumed); err != nil {
		t.Fatalf("decode resumed workflow: %v", err)
	}
	if resumed.Status != "completed" || len(resumed.CompletedStages) != 2 {
		t.Fatalf("expected completed typed workflow, got %#v", resumed)
	}
	if got := resumed.CompletedStages[0].Output.Values["depth"]; got != float64(2) {
		t.Fatalf("expected typed depth in API result, got %#v", resumed.CompletedStages[0].Output.Values)
	}
	if got := resumed.CompletedStages[0].Output.Values["enabled"]; got != true {
		t.Fatalf("expected typed boolean in API result, got %#v", resumed.CompletedStages[0].Output.Values)
	}
	payload, ok := resumed.CompletedStages[0].Output.Values["payload"].(map[string]any)
	if !ok || payload["risk"] != "low" {
		t.Fatalf("expected typed payload in API result, got %#v", resumed.CompletedStages[0].Output.Values["payload"])
	}
	run := workflowRunSnapshotByID(t, runtimeRef, paused.RunID)
	if run.CompletedStages[0].OutputValues["depth"] != 2 || run.CompletedStages[1].InputValues["depth"] != 2 {
		t.Fatalf("expected typed values persisted in run snapshot, got %#v", run.CompletedStages)
	}
}

func TestServerWorkflowRunInputEndpointCanResumeInBackground(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	dir := filepath.Join(runtimeRef.RuntimeHome(), "workflows", "manual-input-background")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir workflow: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "workflow.yaml"), []byte(`name: manual-input-background
stages:
  - name: collect
    node_type: input_gate
    params:
      manual: true
      fields: target
      required: target
    next: [plan]
  - name: plan
    agent: planner
    skill: execution-plan
    input:
      target: stages.collect.outputs.target
`), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}
	server := NewServer(runtimeRef)
	startRequest := httptest.NewRequest(http.MethodPost, "/api/workflows/manual-input-background", strings.NewReader(`{"input":"plan with manual input"}`))
	startRequest.Header.Set("Content-Type", "application/json")
	startResponse := httptest.NewRecorder()
	server.ServeHTTP(startResponse, startRequest)
	if startResponse.Code != http.StatusOK {
		t.Fatalf("expected workflow start 200, got %d body=%s", startResponse.Code, startResponse.Body.String())
	}
	var paused agent.WorkflowResult
	if err := json.NewDecoder(startResponse.Body).Decode(&paused); err != nil {
		t.Fatalf("decode paused workflow: %v", err)
	}
	if paused.Status != "awaiting_input" || paused.RunID == "" {
		t.Fatalf("expected paused input workflow, got %#v", paused)
	}

	inputRequest := httptest.NewRequest(http.MethodPost, "/api/workflow-runs/"+paused.RunID+"/input", strings.NewReader(`{"inputs":{"target":"auth"},"background":true}`))
	inputRequest.Header.Set("Content-Type", "application/json")
	inputResponse := httptest.NewRecorder()
	server.ServeHTTP(inputResponse, inputRequest)
	if inputResponse.Code != http.StatusAccepted {
		t.Fatalf("expected background input 202, got %d body=%s", inputResponse.Code, inputResponse.Body.String())
	}
	var accepted workflowRunBackgroundResponse
	if err := json.NewDecoder(inputResponse.Body).Decode(&accepted); err != nil {
		t.Fatalf("decode background input response: %v", err)
	}
	if accepted.RunID != paused.RunID || accepted.Action != "input" {
		t.Fatalf("expected input to resume same run, got %#v", accepted)
	}
	run := waitWorkflowRunStatus(t, runtimeRef, paused.RunID, "completed")
	if run.ID != paused.RunID || len(run.CompletedStages) != 2 {
		t.Fatalf("expected same run to complete in background, got %#v", run)
	}
}

func TestServerWorkflowRunSubWorkflowResumeEndpoint(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	childDir := filepath.Join(runtimeRef.RuntimeHome(), "workflows", "api-child-input")
	if err := os.MkdirAll(childDir, 0o755); err != nil {
		t.Fatalf("mkdir child workflow: %v", err)
	}
	if err := os.WriteFile(filepath.Join(childDir, "workflow.yaml"), []byte(`name: api-child-input
stages:
  - name: collect
    node_type: input_gate
    params:
      manual: true
      fields: target
      required: target
    next: [plan]
  - name: plan
    agent: planner
    skill: execution-plan
    input:
      target: stages.collect.outputs.target
`), 0o644); err != nil {
		t.Fatalf("write child workflow: %v", err)
	}
	parentDir := filepath.Join(runtimeRef.RuntimeHome(), "workflows", "api-parent-sub")
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		t.Fatalf("mkdir parent workflow: %v", err)
	}
	if err := os.WriteFile(filepath.Join(parentDir, "workflow.yaml"), []byte(`name: api-parent-sub
stages:
  - name: child
    node_type: sub_workflow
    params:
      workflow: api-child-input
      request: workflow.input
    next: [report]
  - name: report
    agent: planner
    skill: execution-plan
    input:
      child_summary: stages.child.outputs.summary
      child_run: stages.child.outputs.sub_run_id
`), 0o644); err != nil {
		t.Fatalf("write parent workflow: %v", err)
	}
	server := NewServer(runtimeRef)
	startRequest := httptest.NewRequest(http.MethodPost, "/api/workflows/api-parent-sub", strings.NewReader(`{"input":"run child"}`))
	startRequest.Header.Set("Content-Type", "application/json")
	startResponse := httptest.NewRecorder()
	server.ServeHTTP(startResponse, startRequest)
	if startResponse.Code != http.StatusOK {
		t.Fatalf("expected parent workflow start 200, got %d body=%s", startResponse.Code, startResponse.Body.String())
	}
	var paused agent.WorkflowResult
	if err := json.NewDecoder(startResponse.Body).Decode(&paused); err != nil {
		t.Fatalf("decode paused parent workflow: %v", err)
	}
	if paused.Status != "awaiting_sub_workflow" || !paused.PendingSubWorkflow || paused.RunID == "" || paused.PendingSubWorkflowRunID == "" {
		t.Fatalf("expected parent awaiting sub-workflow, got %#v", paused)
	}
	childRunID := paused.PendingSubWorkflowRunID
	actionsRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+paused.RunID+"/actions", nil)
	actionsResponse := httptest.NewRecorder()
	server.ServeHTTP(actionsResponse, actionsRequest)
	if actionsResponse.Code != http.StatusOK {
		t.Fatalf("expected parent actions 200, got %d body=%s", actionsResponse.Code, actionsResponse.Body.String())
	}
	var actions []workflowRunAction
	if err := json.NewDecoder(actionsResponse.Body).Decode(&actions); err != nil {
		t.Fatalf("decode parent actions: %v", err)
	}
	if workflowActionAvailable(actions, "resume_sub_workflow", true) {
		t.Fatalf("expected parent resume unavailable before child completion, got %#v", actions)
	}
	if !workflowActionAvailable(actions, "cancel", true) {
		t.Fatalf("expected parent cancel action while awaiting sub-workflow, got %#v", actions)
	}

	earlyResumeRequest := httptest.NewRequest(http.MethodPost, "/api/workflow-runs/"+paused.RunID+"/resume-sub-workflow", nil)
	earlyResumeResponse := httptest.NewRecorder()
	server.ServeHTTP(earlyResumeResponse, earlyResumeRequest)
	if earlyResumeResponse.Code != http.StatusConflict {
		t.Fatalf("expected early parent resume conflict, got %d body=%s", earlyResumeResponse.Code, earlyResumeResponse.Body.String())
	}

	childInputRequest := httptest.NewRequest(http.MethodPost, "/api/workflow-runs/"+childRunID+"/input", strings.NewReader(`{"inputs":{"target":"auth"}}`))
	childInputRequest.Header.Set("Content-Type", "application/json")
	childInputResponse := httptest.NewRecorder()
	server.ServeHTTP(childInputResponse, childInputRequest)
	if childInputResponse.Code != http.StatusOK {
		t.Fatalf("expected child input resume 200, got %d body=%s", childInputResponse.Code, childInputResponse.Body.String())
	}

	actionsResponse = httptest.NewRecorder()
	server.ServeHTTP(actionsResponse, actionsRequest)
	if actionsResponse.Code != http.StatusOK {
		t.Fatalf("expected parent actions after child completion 200, got %d body=%s", actionsResponse.Code, actionsResponse.Body.String())
	}
	actions = nil
	if err := json.NewDecoder(actionsResponse.Body).Decode(&actions); err != nil {
		t.Fatalf("decode parent actions after child completion: %v", err)
	}
	if action := workflowActionByName(actions, "resume_sub_workflow"); action == nil || !action.Available || !action.Background || action.Path != "/api/workflow-runs/"+paused.RunID+"/resume-sub-workflow" {
		t.Fatalf("expected available resumable sub-workflow action, got %#v actions=%#v", action, actions)
	}

	resumeRequest := httptest.NewRequest(http.MethodPost, "/api/workflow-runs/"+paused.RunID+"/resume-sub-workflow", nil)
	resumeResponse := httptest.NewRecorder()
	server.ServeHTTP(resumeResponse, resumeRequest)
	if resumeResponse.Code != http.StatusOK {
		t.Fatalf("expected parent resume 200, got %d body=%s", resumeResponse.Code, resumeResponse.Body.String())
	}
	var resumed agent.WorkflowResult
	if err := json.NewDecoder(resumeResponse.Body).Decode(&resumed); err != nil {
		t.Fatalf("decode resumed parent workflow: %v", err)
	}
	if resumed.Status != "completed" || resumed.RunID != paused.RunID || len(resumed.CompletedStages) != 2 {
		t.Fatalf("expected parent workflow completion, got %#v", resumed)
	}
	if resumed.CompletedStages[0].Output.Variables["sub_run_id"] != childRunID {
		t.Fatalf("expected parent sub-workflow stage to reference child run, got %#v", resumed.CompletedStages[0].Output)
	}
	parentRun := workflowRunSnapshotByID(t, runtimeRef, paused.RunID)
	if parentRun.PendingSubWorkflowRunID != "" || parentRun.Status != "completed" {
		t.Fatalf("expected parent pending sub-workflow metadata cleared, got %#v", parentRun)
	}
}

func TestServerWorkflowRunSubWorkflowResumeEndpointAfterSessionLoad(t *testing.T) {
	firstState := session.New(8)
	firstRuntime := newAPITestRuntimeWithStateAndResponses(t, firstState, nil)
	childDir := filepath.Join(firstRuntime.RuntimeHome(), "workflows", "api-child-input-reload")
	if err := os.MkdirAll(childDir, 0o755); err != nil {
		t.Fatalf("mkdir child workflow: %v", err)
	}
	if err := os.WriteFile(filepath.Join(childDir, "workflow.yaml"), []byte(`name: api-child-input-reload
stages:
  - name: collect
    node_type: input_gate
    params:
      manual: true
      fields: target
      required: target
    next: [plan]
  - name: plan
    agent: planner
    skill: execution-plan
    input:
      target: stages.collect.outputs.target
`), 0o644); err != nil {
		t.Fatalf("write child workflow: %v", err)
	}
	parentDir := filepath.Join(firstRuntime.RuntimeHome(), "workflows", "api-parent-sub-reload")
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		t.Fatalf("mkdir parent workflow: %v", err)
	}
	if err := os.WriteFile(filepath.Join(parentDir, "workflow.yaml"), []byte(`name: api-parent-sub-reload
stages:
  - name: child
    node_type: sub_workflow
    params:
      workflow: api-child-input-reload
      request: workflow.input
    next: [report]
  - name: report
    agent: planner
    skill: execution-plan
    input:
      child_summary: stages.child.outputs.summary
      child_run: stages.child.outputs.sub_run_id
`), 0o644); err != nil {
		t.Fatalf("write parent workflow: %v", err)
	}
	firstServer := NewServer(firstRuntime)
	startRequest := httptest.NewRequest(http.MethodPost, "/api/workflows/api-parent-sub-reload", strings.NewReader(`{"input":"run child after reload"}`))
	startRequest.Header.Set("Content-Type", "application/json")
	startResponse := httptest.NewRecorder()
	firstServer.ServeHTTP(startResponse, startRequest)
	if startResponse.Code != http.StatusOK {
		t.Fatalf("expected parent workflow start 200, got %d body=%s", startResponse.Code, startResponse.Body.String())
	}
	var paused agent.WorkflowResult
	if err := json.NewDecoder(startResponse.Body).Decode(&paused); err != nil {
		t.Fatalf("decode paused parent workflow: %v", err)
	}
	if paused.Status != "awaiting_sub_workflow" || paused.RunID == "" || paused.PendingSubWorkflowRunID == "" {
		t.Fatalf("expected parent awaiting sub-workflow before reload, got %#v", paused)
	}
	childRunID := paused.PendingSubWorkflowRunID

	sessionPath := filepath.Join(t.TempDir(), "session.json")
	if err := firstState.Save(sessionPath); err != nil {
		t.Fatalf("save session: %v", err)
	}
	loadedState := session.New(8)
	if err := loadedState.Load(sessionPath); err != nil {
		t.Fatalf("load session: %v", err)
	}
	secondRuntime := newAPITestRuntimeWithStateHomeAndResponses(t, loadedState, firstRuntime.RuntimeHome(), []schema.ChatResponse{
		{Message: schema.Message{Content: "child completed after reload"}},
		{Message: schema.Message{Content: "parent report after reload"}},
	})
	secondServer := NewServer(secondRuntime)

	parentActionsRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+paused.RunID+"/actions", nil)
	parentActionsResponse := httptest.NewRecorder()
	secondServer.ServeHTTP(parentActionsResponse, parentActionsRequest)
	if parentActionsResponse.Code != http.StatusOK {
		t.Fatalf("expected parent actions 200 after reload, got %d body=%s", parentActionsResponse.Code, parentActionsResponse.Body.String())
	}
	var actions []workflowRunAction
	if err := json.NewDecoder(parentActionsResponse.Body).Decode(&actions); err != nil {
		t.Fatalf("decode parent actions after reload: %v", err)
	}
	if workflowActionAvailable(actions, "resume_sub_workflow", true) || !workflowActionAvailable(actions, "cancel", true) {
		t.Fatalf("expected parent to remain blocked on child after reload, got %#v", actions)
	}

	childInputRequest := httptest.NewRequest(http.MethodPost, "/api/workflow-runs/"+childRunID+"/input", strings.NewReader(`{"inputs":{"target":"auth"}}`))
	childInputRequest.Header.Set("Content-Type", "application/json")
	childInputResponse := httptest.NewRecorder()
	secondServer.ServeHTTP(childInputResponse, childInputRequest)
	if childInputResponse.Code != http.StatusOK {
		t.Fatalf("expected child input resume 200 after reload, got %d body=%s", childInputResponse.Code, childInputResponse.Body.String())
	}
	childRun := workflowRunSnapshotByID(t, secondRuntime, childRunID)
	if childRun.Status != "completed" || len(childRun.PendingFields) != 0 {
		t.Fatalf("expected child completed after reload input, got %#v", childRun)
	}

	parentActionsResponse = httptest.NewRecorder()
	secondServer.ServeHTTP(parentActionsResponse, parentActionsRequest)
	if parentActionsResponse.Code != http.StatusOK {
		t.Fatalf("expected parent actions after child completion 200, got %d body=%s", parentActionsResponse.Code, parentActionsResponse.Body.String())
	}
	actions = nil
	if err := json.NewDecoder(parentActionsResponse.Body).Decode(&actions); err != nil {
		t.Fatalf("decode parent actions after child completion: %v", err)
	}
	if !workflowActionAvailable(actions, "resume_sub_workflow", true) {
		t.Fatalf("expected parent resume action after child completed across reload, got %#v", actions)
	}

	resumeRequest := httptest.NewRequest(http.MethodPost, "/api/workflow-runs/"+paused.RunID+"/resume-sub-workflow", nil)
	resumeResponse := httptest.NewRecorder()
	secondServer.ServeHTTP(resumeResponse, resumeRequest)
	if resumeResponse.Code != http.StatusOK {
		t.Fatalf("expected parent resume 200 after reload, got %d body=%s", resumeResponse.Code, resumeResponse.Body.String())
	}
	var resumed agent.WorkflowResult
	if err := json.NewDecoder(resumeResponse.Body).Decode(&resumed); err != nil {
		t.Fatalf("decode resumed parent workflow: %v", err)
	}
	if resumed.Status != "completed" || resumed.RunID != paused.RunID || len(resumed.CompletedStages) != 2 {
		t.Fatalf("expected parent workflow completion after reload, got %#v", resumed)
	}
	if resumed.CompletedStages[0].Output.Variables["sub_run_id"] != childRunID {
		t.Fatalf("expected parent sub-workflow stage to reference child run after reload, got %#v", resumed.CompletedStages[0].Output)
	}
	parentRun := workflowRunSnapshotByID(t, secondRuntime, paused.RunID)
	if parentRun.Status != "completed" || parentRun.PendingSubWorkflowRunID != "" || parentRun.PendingSubWorkflowStatus != "" {
		t.Fatalf("expected parent pending sub-workflow metadata cleared after reload resume, got %#v", parentRun)
	}
}

func TestServerWorkflowRunInputEndpointResumesTeamApprovalGateQuorum(t *testing.T) {
	runtimeRef := newAPITestRuntimeWithStateAndResponses(t, session.New(8), []schema.ChatResponse{
		{Message: schema.Message{Content: "Plan ready.\nApproval: planner approves the plan."}},
		{Message: schema.Message{Content: "Implementation done."}},
		{Message: schema.Message{Content: "Review complete without explicit approval."}},
		{Message: schema.Message{Content: "Reporter summarized pending review."}},
		{Message: schema.Message{Content: "Manual quorum handoff complete."}},
	})
	dir := filepath.Join(runtimeRef.RuntimeHome(), "workflows", "api-team-approval")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir workflow: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "workflow.yaml"), []byte(`name: api-team-approval
stages:
  - name: team
    node_type: team
    agent: planner
    params:
      team: software-task-team
      execute: true
      approval_quorum: 2
      approval_roles: planner,reviewer
    next: [review_gate]
  - name: review_gate
    node_type: policy_guard
    params:
      rule: team_approval_gate
      team: software-task-team
      status: passed
      wait_for_quorum: true
    routes:
      allow: handoff
  - name: handoff
    agent: chat
    skill: execution-plan
    input:
      gate_status: stages.review_gate.outputs.value
`), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}
	server := NewServer(runtimeRef)
	startRequest := httptest.NewRequest(http.MethodPost, "/api/workflows/api-team-approval", strings.NewReader(`{"input":"ship with review quorum"}`))
	startRequest.Header.Set("Content-Type", "application/json")
	startResponse := httptest.NewRecorder()
	server.ServeHTTP(startResponse, startRequest)
	if startResponse.Code != http.StatusOK {
		t.Fatalf("expected workflow start 200, got %d body=%s", startResponse.Code, startResponse.Body.String())
	}
	var paused agent.WorkflowResult
	if err := json.NewDecoder(startResponse.Body).Decode(&paused); err != nil {
		t.Fatalf("decode paused workflow: %v", err)
	}
	if paused.Status != "awaiting_input" || paused.NextStage != "review_gate" || len(paused.PendingFields) != 3 {
		t.Fatalf("expected team approval gate awaiting manual quorum input, got %#v", paused)
	}
	if paused.PendingFields[0].Name != "roles" || !paused.PendingFields[0].Multiple || !workflowStringSliceContains(paused.PendingFields[0].Options, "reviewer") {
		t.Fatalf("expected roles field with reviewer option, got %#v", paused.PendingFields)
	}
	actionsRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+paused.RunID+"/actions", nil)
	actionsResponse := httptest.NewRecorder()
	server.ServeHTTP(actionsResponse, actionsRequest)
	if actionsResponse.Code != http.StatusOK {
		t.Fatalf("expected actions 200, got %d body=%s", actionsResponse.Code, actionsResponse.Body.String())
	}
	var actions []workflowRunAction
	if err := json.NewDecoder(actionsResponse.Body).Decode(&actions); err != nil {
		t.Fatalf("decode actions: %v", err)
	}
	if !workflowActionAvailable(actions, "submit_input", true) {
		t.Fatalf("expected submit_input action for team approval gate, got %#v", actions)
	}

	inputRequest := httptest.NewRequest(http.MethodPost, "/api/workflow-runs/"+paused.RunID+"/input", strings.NewReader(`{"inputs":{"roles":"reviewer","decision":"approve","comment":"manual reviewer approval"}}`))
	inputRequest.Header.Set("Content-Type", "application/json")
	inputResponse := httptest.NewRecorder()
	server.ServeHTTP(inputResponse, inputRequest)
	if inputResponse.Code != http.StatusOK {
		t.Fatalf("expected input resume 200, got %d body=%s", inputResponse.Code, inputResponse.Body.String())
	}
	var resumed agent.WorkflowResult
	if err := json.NewDecoder(inputResponse.Body).Decode(&resumed); err != nil {
		t.Fatalf("decode resumed workflow: %v", err)
	}
	if resumed.Status != "completed" || resumed.RunID != paused.RunID {
		t.Fatalf("expected completed workflow after manual quorum input, got %#v", resumed)
	}
	run := workflowRunSnapshotByID(t, runtimeRef, paused.RunID)
	if run.Status != "completed" || len(run.PendingFields) != 0 {
		t.Fatalf("expected pending fields cleared after team approval resume, got %#v", run)
	}
}

func TestServerWorkflowRunHistoryEndpointsRecordStreamedRun(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	server := NewServer(runtimeRef)
	request := httptest.NewRequest(http.MethodPost, "/api/workflows/plan-fix-audit/stream", strings.NewReader(`{"input":"ship it"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected workflow stream 200, got %d body=%s", response.Code, response.Body.String())
	}
	listRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs", nil)
	listResponse := httptest.NewRecorder()
	server.ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("expected workflow run list 200, got %d body=%s", listResponse.Code, listResponse.Body.String())
	}
	var runs []session.WorkflowRunSnapshot
	if err := json.NewDecoder(listResponse.Body).Decode(&runs); err != nil {
		t.Fatalf("decode workflow runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected one workflow run, got %#v", runs)
	}
	run := runs[0]
	if run.ID == "" || run.Name != "plan-fix-audit" || run.Status != "completed" {
		t.Fatalf("expected completed run with id, got %#v", run)
	}
	if len(run.CompletedStages) != 3 {
		t.Fatalf("expected persisted completed stages, got %#v", run.CompletedStages)
	}
	if len(run.Artifacts) == 0 || len(run.CompletedStages[0].Artifacts) == 0 {
		t.Fatalf("expected run and stage artifacts, got run=%#v stage=%#v", run.Artifacts, run.CompletedStages[0].Artifacts)
	}
	foundPlanArtifact := false
	for _, artifact := range run.CompletedStages[0].Artifacts {
		if artifact.Kind == "output" && strings.Contains(artifact.Content, "plan ready") {
			foundPlanArtifact = true
			break
		}
	}
	if !foundPlanArtifact {
		t.Fatalf("expected plan output artifact, got %#v", run.CompletedStages[0].Artifacts)
	}
	var hasTaskStage, hasTokenUsage, hasWorkflowResult bool
	for _, event := range run.Events {
		switch event.Type {
		case string(schema.StreamEventTaskStage):
			hasTaskStage = true
		case string(schema.StreamEventTokenUsage):
			hasTokenUsage = true
		case string(schema.StreamEventWorkflowResult):
			hasWorkflowResult = true
		}
	}
	if !hasTaskStage || !hasTokenUsage || !hasWorkflowResult {
		t.Fatalf("expected task/token/result events in run history, got %#v", run.Events)
	}

	itemRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+run.ID, nil)
	itemResponse := httptest.NewRecorder()
	server.ServeHTTP(itemResponse, itemRequest)
	if itemResponse.Code != http.StatusOK || !strings.Contains(itemResponse.Body.String(), run.ID) {
		t.Fatalf("expected workflow run item, code=%d body=%s", itemResponse.Code, itemResponse.Body.String())
	}
	if runtimeRef.SessionSnapshot().Workflow.RunID != run.ID {
		t.Fatalf("expected session workflow run id %q, got %#v", run.ID, runtimeRef.SessionSnapshot().Workflow)
	}

	stageRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+run.ID+"/stages/plan", nil)
	stageResponse := httptest.NewRecorder()
	server.ServeHTTP(stageResponse, stageRequest)
	if stageResponse.Code != http.StatusOK {
		t.Fatalf("expected workflow run stage detail 200, got %d body=%s", stageResponse.Code, stageResponse.Body.String())
	}
	var stageDetail workflowRunStageDetail
	if err := json.NewDecoder(stageResponse.Body).Decode(&stageDetail); err != nil {
		t.Fatalf("decode workflow stage detail: %v", err)
	}
	if stageDetail.RunID != run.ID || stageDetail.Stage != "plan" || stageDetail.Snapshot == nil {
		t.Fatalf("expected plan stage detail, got %#v", stageDetail)
	}
	if len(stageDetail.Events) == 0 || len(stageDetail.Artifacts) == 0 {
		t.Fatalf("expected plan stage events and artifacts, got %#v", stageDetail)
	}

	eventsRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+run.ID+"/stages/plan/events", nil)
	eventsResponse := httptest.NewRecorder()
	server.ServeHTTP(eventsResponse, eventsRequest)
	if eventsResponse.Code != http.StatusOK || !strings.Contains(eventsResponse.Body.String(), `"stage":"plan"`) {
		t.Fatalf("expected filtered plan events, code=%d body=%s", eventsResponse.Code, eventsResponse.Body.String())
	}

	filteredEventsRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+run.ID+"/events?stage=plan&type=task_stage&limit=1", nil)
	filteredEventsResponse := httptest.NewRecorder()
	server.ServeHTTP(filteredEventsResponse, filteredEventsRequest)
	if filteredEventsResponse.Code != http.StatusOK {
		t.Fatalf("expected filtered workflow events 200, got %d body=%s", filteredEventsResponse.Code, filteredEventsResponse.Body.String())
	}
	var filteredEvents []session.WorkflowRunEventSnapshot
	if err := json.NewDecoder(filteredEventsResponse.Body).Decode(&filteredEvents); err != nil {
		t.Fatalf("decode filtered workflow events: %v", err)
	}
	if len(filteredEvents) != 1 || filteredEvents[0].Stage != "plan" || filteredEvents[0].Type != string(schema.StreamEventTaskStage) {
		t.Fatalf("expected one filtered plan task-stage event, got %#v", filteredEvents)
	}

	timelineRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+run.ID+"/timeline?stage=plan&kind=event&limit=3", nil)
	timelineResponse := httptest.NewRecorder()
	server.ServeHTTP(timelineResponse, timelineRequest)
	if timelineResponse.Code != http.StatusOK {
		t.Fatalf("expected workflow timeline 200, got %d body=%s", timelineResponse.Code, timelineResponse.Body.String())
	}
	var timeline []workflowRunTimelineItem
	if err := json.NewDecoder(timelineResponse.Body).Decode(&timeline); err != nil {
		t.Fatalf("decode workflow timeline: %v", err)
	}
	if len(timeline) == 0 || len(timeline) > 3 {
		t.Fatalf("expected bounded plan timeline events, got %#v", timeline)
	}
	for _, item := range timeline {
		if item.Kind != "event" || item.Stage != "plan" {
			t.Fatalf("expected event-only plan timeline item, got %#v", item)
		}
	}

	filteredArtifactsRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+run.ID+"/artifacts?stage=plan&kind=output", nil)
	filteredArtifactsResponse := httptest.NewRecorder()
	server.ServeHTTP(filteredArtifactsResponse, filteredArtifactsRequest)
	if filteredArtifactsResponse.Code != http.StatusOK {
		t.Fatalf("expected filtered artifacts 200, got %d body=%s", filteredArtifactsResponse.Code, filteredArtifactsResponse.Body.String())
	}
	var filteredArtifacts []session.WorkflowRunArtifact
	if err := json.NewDecoder(filteredArtifactsResponse.Body).Decode(&filteredArtifacts); err != nil {
		t.Fatalf("decode filtered artifacts: %v", err)
	}
	if len(filteredArtifacts) == 0 {
		t.Fatalf("expected plan output artifacts")
	}
	for _, artifact := range filteredArtifacts {
		if artifact.Stage != "plan" || artifact.Kind != "output" {
			t.Fatalf("expected plan output artifact from shorthand kind filter, got %#v", artifact)
		}
	}

	replayRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+run.ID+"/replay", nil)
	replayResponse := httptest.NewRecorder()
	server.ServeHTTP(replayResponse, replayRequest)
	if replayResponse.Code != http.StatusOK {
		t.Fatalf("expected workflow run replay 200, got %d body=%s", replayResponse.Code, replayResponse.Body.String())
	}
	replayBody := replayResponse.Body.String()
	if !strings.Contains(replayBody, `"run"`) ||
		!strings.Contains(replayBody, `"stages"`) ||
		!strings.Contains(replayBody, `"events"`) ||
		!strings.Contains(replayBody, `"artifacts"`) ||
		!strings.Contains(replayBody, `"collaboration"`) ||
		!strings.Contains(replayBody, `"blackboard"`) {
		t.Fatalf("expected replay bundle with run data and collaboration state, got %s", replayBody)
	}

	filteredReplayRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+run.ID+"/replay?stage=plan&kind=artifact&artifact_kind=output&limit=2", nil)
	filteredReplayResponse := httptest.NewRecorder()
	server.ServeHTTP(filteredReplayResponse, filteredReplayRequest)
	if filteredReplayResponse.Code != http.StatusOK {
		t.Fatalf("expected filtered replay 200, got %d body=%s", filteredReplayResponse.Code, filteredReplayResponse.Body.String())
	}
	var filteredReplay workflowRunReplay
	if err := json.NewDecoder(filteredReplayResponse.Body).Decode(&filteredReplay); err != nil {
		t.Fatalf("decode filtered replay: %v", err)
	}
	if filteredReplay.Filters.Stage != "plan" || filteredReplay.Filters.ItemKind != "artifact" || filteredReplay.Filters.ArtifactKind != "output" {
		t.Fatalf("expected replay filters to echo request, got %#v", filteredReplay.Filters)
	}
	if len(filteredReplay.Stages) != 0 || len(filteredReplay.Events) != 0 || len(filteredReplay.Artifacts) == 0 || len(filteredReplay.Artifacts) > 2 {
		t.Fatalf("expected artifact-only filtered replay, got stages=%#v events=%#v artifacts=%#v", filteredReplay.Stages, filteredReplay.Events, filteredReplay.Artifacts)
	}
	for _, artifact := range filteredReplay.Artifacts {
		if artifact.Stage != "plan" || artifact.Kind != "output" {
			t.Fatalf("expected plan output artifact, got %#v", artifact)
		}
	}

	exportJSONRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+run.ID+"/export?format=json&stage=plan&kind=artifact&artifact_kind=output", nil)
	exportJSONResponse := httptest.NewRecorder()
	server.ServeHTTP(exportJSONResponse, exportJSONRequest)
	if exportJSONResponse.Code != http.StatusOK || !strings.Contains(exportJSONResponse.Header().Get("Content-Type"), "application/json") || !strings.Contains(exportJSONResponse.Header().Get("Content-Disposition"), ".json") {
		t.Fatalf("expected workflow run json export, code=%d headers=%#v body=%s", exportJSONResponse.Code, exportJSONResponse.Header(), exportJSONResponse.Body.String())
	}
	var exportedReplay workflowRunReplay
	if err := json.NewDecoder(exportJSONResponse.Body).Decode(&exportedReplay); err != nil {
		t.Fatalf("decode exported workflow run replay: %v", err)
	}
	if exportedReplay.Run.ID != run.ID || len(exportedReplay.Events) != 0 || len(exportedReplay.Stages) != 0 || len(exportedReplay.Artifacts) == 0 {
		t.Fatalf("expected filtered json export with artifact-only replay, got %#v", exportedReplay)
	}
	for _, artifact := range exportedReplay.Artifacts {
		if artifact.Stage != "plan" || artifact.Kind != "output" {
			t.Fatalf("expected plan output artifact in json export, got %#v", artifact)
		}
	}

	exportMarkdownRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+run.ID+"/export?format=md&include_content=1", nil)
	exportMarkdownResponse := httptest.NewRecorder()
	server.ServeHTTP(exportMarkdownResponse, exportMarkdownRequest)
	if exportMarkdownResponse.Code != http.StatusOK || !strings.Contains(exportMarkdownResponse.Header().Get("Content-Type"), "text/markdown") || !strings.Contains(exportMarkdownResponse.Header().Get("Content-Disposition"), ".md") {
		t.Fatalf("expected workflow run markdown export, code=%d headers=%#v body=%s", exportMarkdownResponse.Code, exportMarkdownResponse.Header(), exportMarkdownResponse.Body.String())
	}
	markdownBody := exportMarkdownResponse.Body.String()
	if !strings.Contains(markdownBody, "# Workflow Run Report") || !strings.Contains(markdownBody, "## Stages") || !strings.Contains(markdownBody, "## Artifacts") || !strings.Contains(markdownBody, "plan ready") {
		t.Fatalf("expected markdown workflow run report with content, got %s", markdownBody)
	}
}

func TestServerWorkflowRunCollectionSupportsFilteredEnvelope(t *testing.T) {
	state := session.New(8)
	completedID := state.StartWorkflowRun("audit-flow", "review auth module")
	state.CompleteWorkflowRun(completedID, "completed", "audit complete", "", "", nil, []session.WorkflowRunStageSnapshot{{
		Stage:   "audit",
		AgentID: "auditor",
		Status:  "completed",
		Summary: "reviewed auth",
		Result:  schema.AgentResult{Output: "auth review complete"},
	}})
	failedID := state.StartWorkflowRun("deploy-flow", "ship release")
	state.CompleteWorkflowRun(failedID, "failed", "deployment failed", "", "", nil, []session.WorkflowRunStageSnapshot{{
		Stage:   "deploy",
		AgentID: "fixer",
		Status:  "failed",
		Summary: "deploy error",
		Result:  schema.AgentResult{Output: "release failed"},
	}})
	pendingID := state.StartWorkflowRun("manual-flow", "approval required")
	state.CompleteWorkflowRun(pendingID, "awaiting_input", "waiting for approval target", "collect", "provide target", []schema.WorkflowInputField{{
		Name:     "target",
		Label:    "Target",
		Required: true,
	}}, nil)

	runtimeRef := newAPITestRuntimeWithStateAndResponses(t, state, nil)
	server := NewServer(runtimeRef)

	completedRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs?envelope=1&workflow=audit-flow&status=completed", nil)
	completedResponse := httptest.NewRecorder()
	server.ServeHTTP(completedResponse, completedRequest)
	if completedResponse.Code != http.StatusOK {
		t.Fatalf("expected filtered workflow runs 200, got %d body=%s", completedResponse.Code, completedResponse.Body.String())
	}
	var completed workflowRunCollectionResponse
	if err := json.NewDecoder(completedResponse.Body).Decode(&completed); err != nil {
		t.Fatalf("decode completed workflow run collection: %v", err)
	}
	if completed.Counts.Total != 3 || completed.Counts.Matched != 1 || completed.Counts.Returned != 1 || completed.Counts.Active != 1 || completed.Counts.Terminal != 2 || completed.Counts.Errors != 1 || completed.Counts.NeedsAction != 1 {
		t.Fatalf("unexpected completed collection counts: %#v", completed.Counts)
	}
	if len(completed.Runs) != 1 || completed.Runs[0].ID != completedID {
		t.Fatalf("expected only completed audit run, got %#v", completed.Runs)
	}
	if completed.Filters.Workflow != "audit-flow" || completed.Filters.Status != "completed" {
		t.Fatalf("expected echoed filters, got %#v", completed.Filters)
	}

	activeRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs?active=1&q=approval", nil)
	activeResponse := httptest.NewRecorder()
	server.ServeHTTP(activeResponse, activeRequest)
	if activeResponse.Code != http.StatusOK {
		t.Fatalf("expected active workflow runs 200, got %d body=%s", activeResponse.Code, activeResponse.Body.String())
	}
	var active workflowRunCollectionResponse
	if err := json.NewDecoder(activeResponse.Body).Decode(&active); err != nil {
		t.Fatalf("decode active workflow run collection: %v", err)
	}
	if len(active.Runs) != 1 || active.Runs[0].ID != pendingID || !active.Filters.ActiveOnly || active.Filters.Query != "approval" {
		t.Fatalf("expected active pending approval run, got filters=%#v runs=%#v", active.Filters, active.Runs)
	}

	limitedRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs?terminal=1&limit=1", nil)
	limitedResponse := httptest.NewRecorder()
	server.ServeHTTP(limitedResponse, limitedRequest)
	if limitedResponse.Code != http.StatusOK {
		t.Fatalf("expected limited workflow runs 200, got %d body=%s", limitedResponse.Code, limitedResponse.Body.String())
	}
	var limited workflowRunCollectionResponse
	if err := json.NewDecoder(limitedResponse.Body).Decode(&limited); err != nil {
		t.Fatalf("decode limited workflow run collection: %v", err)
	}
	if limited.Counts.Matched != 2 || limited.Counts.Returned != 1 || len(limited.Runs) != 1 {
		t.Fatalf("expected terminal filter to match two and return one, got counts=%#v runs=%#v", limited.Counts, limited.Runs)
	}
}

func TestServerWorkflowRunReplayAndRunsIncludeQualitySummary(t *testing.T) {
	state := session.New(8)
	runID := state.StartWorkflowRun("quality-flow", "verify release evidence")
	state.CompleteWorkflowRun(runID, "completed", "quality gate complete", "", "", nil, []session.WorkflowRunStageSnapshot{{
		Stage:   "verify",
		AgentID: "auditor",
		Status:  "completed",
		Result: schema.AgentResult{
			Output: "release fixed but missing risk note",
			Verification: []schema.Verification{
				{Kind: "tests", Status: "passed", Detail: "unit tests passed"},
				{Kind: "risk-review", Status: "failed", Detail: "risk note missing"},
			},
		},
		Acceptance: []session.WorkflowRunAcceptanceSnapshot{
			{Name: "fix evidence", Ref: "result.output", Expected: "fixed", Actual: "release fixed", Status: "passed"},
			{Name: "risk evidence", Ref: "result.output", Expected: "risk", Actual: "release fixed", Status: "failed", Reason: "risk evidence missing"},
		},
	}})
	server := NewServer(newAPITestRuntimeWithStateAndResponses(t, state, nil))

	replayRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+runID+"/replay", nil)
	replayResponse := httptest.NewRecorder()
	server.ServeHTTP(replayResponse, replayRequest)
	if replayResponse.Code != http.StatusOK {
		t.Fatalf("expected workflow replay 200, got %d body=%s", replayResponse.Code, replayResponse.Body.String())
	}
	var replay workflowRunReplay
	if err := json.NewDecoder(replayResponse.Body).Decode(&replay); err != nil {
		t.Fatalf("decode workflow replay: %v", err)
	}
	if replay.Quality.Status != "failed" || replay.Quality.AcceptanceTotal != 2 || replay.Quality.AcceptanceFailed != 1 || replay.Quality.VerificationTotal != 2 || replay.Quality.VerificationFailed != 1 {
		t.Fatalf("expected failed quality summary with one failed acceptance and validation, got %#v", replay.Quality)
	}
	if replay.Quality.Score >= 100 || len(replay.Quality.UnmetCriteria) != 1 || len(replay.Quality.FailedValidations) != 1 || replay.Quality.EvidenceArtifacts == 0 {
		t.Fatalf("expected quality evidence and issue details, got %#v", replay.Quality)
	}

	evidenceRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+runID+"/evidence", nil)
	evidenceResponse := httptest.NewRecorder()
	server.ServeHTTP(evidenceResponse, evidenceRequest)
	if evidenceResponse.Code != http.StatusOK {
		t.Fatalf("expected workflow evidence 200, got %d body=%s", evidenceResponse.Code, evidenceResponse.Body.String())
	}
	var evidence workflowRunEvidenceResponse
	if err := json.NewDecoder(evidenceResponse.Body).Decode(&evidence); err != nil {
		t.Fatalf("decode workflow evidence: %v", err)
	}
	if evidence.RunID != runID || evidence.Quality.AcceptanceFailed != 1 || evidence.Counts.Stages != 1 || evidence.Counts.Artifacts == 0 || evidence.Counts.Checks != 4 || evidence.Counts.FailedChecks != 2 {
		t.Fatalf("expected quality evidence graph counts, got %#v", evidence)
	}
	if len(evidence.Edges) == 0 || len(evidence.Issues) != 2 {
		t.Fatalf("expected evidence edges and quality issues, got edges=%#v issues=%#v", evidence.Edges, evidence.Issues)
	}

	filteredEvidenceRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+runID+"/evidence?stage=verify&kind=check&status=failed", nil)
	filteredEvidenceResponse := httptest.NewRecorder()
	server.ServeHTTP(filteredEvidenceResponse, filteredEvidenceRequest)
	if filteredEvidenceResponse.Code != http.StatusOK {
		t.Fatalf("expected filtered workflow evidence 200, got %d body=%s", filteredEvidenceResponse.Code, filteredEvidenceResponse.Body.String())
	}
	var filteredEvidence workflowRunEvidenceResponse
	if err := json.NewDecoder(filteredEvidenceResponse.Body).Decode(&filteredEvidence); err != nil {
		t.Fatalf("decode filtered workflow evidence: %v", err)
	}
	if filteredEvidence.Filters.Stage != "verify" || filteredEvidence.Filters.ItemKind != "check" || filteredEvidence.Filters.Status != "failed" || filteredEvidence.Counts.Checks != 2 || filteredEvidence.Counts.Artifacts != 0 {
		t.Fatalf("expected failed check-only evidence filter, got %#v", filteredEvidence)
	}

	runsRequest := httptest.NewRequest(http.MethodGet, "/api/runs?type=workflow", nil)
	runsResponse := httptest.NewRecorder()
	server.ServeHTTP(runsResponse, runsRequest)
	if runsResponse.Code != http.StatusOK {
		t.Fatalf("expected unified runs 200, got %d body=%s", runsResponse.Code, runsResponse.Body.String())
	}
	var runs runCollectionResponse
	if err := json.NewDecoder(runsResponse.Body).Decode(&runs); err != nil {
		t.Fatalf("decode unified runs: %v", err)
	}
	if len(runs.Runs) != 1 || runs.Runs[0].Quality == nil || runs.Runs[0].Quality.AcceptanceFailed != 1 || runs.Runs[0].EvidencePath != "/api/workflow-runs/"+runID+"/evidence" {
		t.Fatalf("expected workflow run item quality summary, got %#v", runs.Runs)
	}

	exportRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+runID+"/export?format=md", nil)
	exportResponse := httptest.NewRecorder()
	server.ServeHTTP(exportResponse, exportRequest)
	if exportResponse.Code != http.StatusOK {
		t.Fatalf("expected markdown export 200, got %d body=%s", exportResponse.Code, exportResponse.Body.String())
	}
	markdown := exportResponse.Body.String()
	if !strings.Contains(markdown, "## Quality") || !strings.Contains(markdown, "### Unmet Criteria") || !strings.Contains(markdown, "risk evidence") {
		t.Fatalf("expected markdown quality section, got %s", markdown)
	}
}

func TestServerWorkflowBackgroundRunReturnsRunIDAndSurvivesRequestCancel(t *testing.T) {
	runtimeRef, gateLLM := newAPIBackgroundWorkflowRuntime(t)
	server := NewServer(runtimeRef)
	ctx, cancelRequest := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodPost, "/api/workflows/plan-fix-audit", strings.NewReader(`{"input":"ship it","background":true}`)).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("expected background workflow 202, got %d body=%s", response.Code, response.Body.String())
	}
	var accepted workflowRunBackgroundResponse
	if err := json.NewDecoder(response.Body).Decode(&accepted); err != nil {
		t.Fatalf("decode accepted workflow: %v", err)
	}
	if accepted.RunID == "" || accepted.EventsURL != "/api/workflow-runs/"+accepted.RunID+"/events/stream" {
		t.Fatalf("expected accepted run id and events url, got %#v", accepted)
	}

	select {
	case <-gateLLM.started:
	case <-time.After(2 * time.Second):
		t.Fatal("expected background workflow LLM call to start")
	}
	cancelRequest()
	close(gateLLM.release)

	run := waitWorkflowRunStatus(t, runtimeRef, accepted.RunID, "completed")
	if run.CompletedAt == "" || len(run.CompletedStages) != 3 {
		t.Fatalf("expected background workflow to complete after request cancel, got %#v", run)
	}
}

func TestServerAgentBackgroundRunReturnsRunIDAndSurvivesRequestCancel(t *testing.T) {
	runtimeRef, gateLLM := newAPIBackgroundWorkflowRuntime(t)
	server := NewServer(runtimeRef)
	ctx, cancelRequest := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodPost, "/api/run", strings.NewReader(`{"input":"hello","background":true}`)).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("expected background agent 202, got %d body=%s", response.Code, response.Body.String())
	}
	var accepted agentRunBackgroundResponse
	if err := json.NewDecoder(response.Body).Decode(&accepted); err != nil {
		t.Fatalf("decode accepted agent run: %v", err)
	}
	if accepted.RunID == "" || accepted.EventsURL != "/api/agent-runs/"+accepted.RunID+"/events/stream" {
		t.Fatalf("expected accepted run id and events url, got %#v", accepted)
	}
	if accepted.TimelineURL != "/api/agent-runs/"+accepted.RunID+"/timeline" ||
		accepted.ReplayURL != "/api/agent-runs/"+accepted.RunID+"/replay" ||
		accepted.ActionsURL != "/api/agent-runs/"+accepted.RunID+"/actions" ||
		accepted.DiffsURL != "/api/agent-runs/"+accepted.RunID+"/diffs" ||
		accepted.CancelURL != "/api/agent-runs/"+accepted.RunID+"/cancel" {
		t.Fatalf("expected accepted agent run navigation urls, got %#v", accepted)
	}

	select {
	case <-gateLLM.started:
	case <-time.After(2 * time.Second):
		t.Fatal("expected background agent LLM call to start")
	}
	cancelRequest()
	close(gateLLM.release)

	run := waitAgentRunStatus(t, runtimeRef, accepted.RunID, "completed")
	if run.CompletedAt == "" || run.Output == "" || run.AgentID == "" {
		t.Fatalf("expected background agent to complete after request cancel, got %#v", run)
	}
}

func TestServerRunStreamBackgroundReturnsDurableRunIDAndSurvivesRequestCancel(t *testing.T) {
	runtimeRef, gateLLM := newAPIBackgroundWorkflowRuntime(t)
	server := NewServer(runtimeRef)
	ctx, cancelRequest := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodPost, "/api/run/stream", strings.NewReader(`{"input":"hello","background":true}`)).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("expected background agent stream 202, got %d body=%s", response.Code, response.Body.String())
	}
	var accepted agentRunBackgroundResponse
	if err := json.NewDecoder(response.Body).Decode(&accepted); err != nil {
		t.Fatalf("decode accepted agent stream run: %v", err)
	}
	if accepted.RunID == "" || accepted.EventsURL != "/api/agent-runs/"+accepted.RunID+"/events/stream" {
		t.Fatalf("expected accepted run id and events url, got %#v", accepted)
	}
	if accepted.TimelineURL != "/api/agent-runs/"+accepted.RunID+"/timeline" ||
		accepted.ReplayURL != "/api/agent-runs/"+accepted.RunID+"/replay" ||
		accepted.ActionsURL != "/api/agent-runs/"+accepted.RunID+"/actions" ||
		accepted.DiffsURL != "/api/agent-runs/"+accepted.RunID+"/diffs" ||
		accepted.CancelURL != "/api/agent-runs/"+accepted.RunID+"/cancel" {
		t.Fatalf("expected accepted agent stream navigation urls, got %#v", accepted)
	}

	select {
	case <-gateLLM.started:
	case <-time.After(2 * time.Second):
		t.Fatal("expected background agent stream LLM call to start")
	}
	cancelRequest()
	close(gateLLM.release)

	run := waitAgentRunStatus(t, runtimeRef, accepted.RunID, "completed")
	if run.CompletedAt == "" || run.Output == "" || run.AgentID == "" {
		t.Fatalf("expected background agent stream to complete after request cancel, got %#v", run)
	}
}

func TestServerAgentRunCollectionSupportsFilteredEnvelope(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	completedID := runtimeRef.StartAgentRun("write docs")
	runtimeRef.AppendAgentRunEvent(completedID, session.AgentRunEventSnapshot{
		Type:    string(schema.StreamEventText),
		Content: "docs drafted",
		AgentID: "chat",
		Mode:    "chat",
	})
	runtimeRef.CompleteAgentRun(completedID, "completed", schema.AgentResult{
		Output:  "docs complete",
		AgentID: "chat",
		Mode:    "chat",
	})
	failedID := runtimeRef.StartAgentRun("fix release")
	runtimeRef.AppendAgentRunEvent(failedID, session.AgentRunEventSnapshot{
		Type:    string(schema.StreamEventTaskStage),
		Content: "fix failed",
		AgentID: "fixer",
		Mode:    "fix",
	})
	runtimeRef.FailAgentRun(failedID, "release failed")
	pendingID := runtimeRef.StartAgentRunWithOptions("write protected file", session.AgentRunStartOptions{RetryOf: completedID})
	runtimeRef.AppendAgentRunEvent(pendingID, session.AgentRunEventSnapshot{
		Type:        string(schema.StreamEventApproval),
		Content:     "approve write",
		ToolName:    "write_file",
		ToolCallID:  "call-pending",
		AgentID:     "fixer",
		Mode:        "fix",
		NeedsAction: true,
		Suspended:   true,
	})
	server := NewServer(runtimeRef)

	legacyRequest := httptest.NewRequest(http.MethodGet, "/api/agent-runs", nil)
	legacyResponse := httptest.NewRecorder()
	server.ServeHTTP(legacyResponse, legacyRequest)
	if legacyResponse.Code != http.StatusOK {
		t.Fatalf("expected legacy agent run list 200, got %d body=%s", legacyResponse.Code, legacyResponse.Body.String())
	}
	var legacy []session.AgentRunSnapshot
	if err := json.NewDecoder(legacyResponse.Body).Decode(&legacy); err != nil {
		t.Fatalf("decode legacy agent run list: %v", err)
	}
	if len(legacy) != 3 {
		t.Fatalf("expected legacy array response with three runs, got %#v", legacy)
	}

	completedRequest := httptest.NewRequest(http.MethodGet, "/api/agent-runs?envelope=1&agent=chat&status=completed", nil)
	completedResponse := httptest.NewRecorder()
	server.ServeHTTP(completedResponse, completedRequest)
	if completedResponse.Code != http.StatusOK {
		t.Fatalf("expected filtered agent runs 200, got %d body=%s", completedResponse.Code, completedResponse.Body.String())
	}
	var completed agentRunCollectionResponse
	if err := json.NewDecoder(completedResponse.Body).Decode(&completed); err != nil {
		t.Fatalf("decode completed agent run collection: %v", err)
	}
	if completed.Counts.Total != 3 || completed.Counts.Matched != 1 || completed.Counts.Returned != 1 || completed.Counts.Active != 1 || completed.Counts.Terminal != 2 || completed.Counts.Errors != 1 || completed.Counts.NeedsAction != 1 {
		t.Fatalf("unexpected completed collection counts: %#v", completed.Counts)
	}
	if len(completed.Runs) != 1 || completed.Runs[0].ID != completedID || completed.Filters.AgentID != "chat" || completed.Filters.Status != "completed" {
		t.Fatalf("expected only completed chat run with echoed filters, filters=%#v runs=%#v", completed.Filters, completed.Runs)
	}

	activeRequest := httptest.NewRequest(http.MethodGet, "/api/agent-runs?active=1&tool_name=write_file&q=protected", nil)
	activeResponse := httptest.NewRecorder()
	server.ServeHTTP(activeResponse, activeRequest)
	if activeResponse.Code != http.StatusOK {
		t.Fatalf("expected active agent runs 200, got %d body=%s", activeResponse.Code, activeResponse.Body.String())
	}
	var active agentRunCollectionResponse
	if err := json.NewDecoder(activeResponse.Body).Decode(&active); err != nil {
		t.Fatalf("decode active agent run collection: %v", err)
	}
	if len(active.Runs) != 1 || active.Runs[0].ID != pendingID || active.Runs[0].RetryOf != completedID || !active.Filters.ActiveOnly || active.Filters.ToolName != "write_file" {
		t.Fatalf("expected active pending write run, filters=%#v runs=%#v", active.Filters, active.Runs)
	}

	limitedRequest := httptest.NewRequest(http.MethodGet, "/api/agent-runs?terminal=1&limit=1", nil)
	limitedResponse := httptest.NewRecorder()
	server.ServeHTTP(limitedResponse, limitedRequest)
	if limitedResponse.Code != http.StatusOK {
		t.Fatalf("expected limited agent runs 200, got %d body=%s", limitedResponse.Code, limitedResponse.Body.String())
	}
	var limited agentRunCollectionResponse
	if err := json.NewDecoder(limitedResponse.Body).Decode(&limited); err != nil {
		t.Fatalf("decode limited agent run collection: %v", err)
	}
	if limited.Counts.Matched != 2 || limited.Counts.Returned != 1 || len(limited.Runs) != 1 {
		t.Fatalf("expected terminal filter to match two and return one, got counts=%#v runs=%#v", limited.Counts, limited.Runs)
	}
}

func TestServerUnifiedRunCollectionSupportsMixedHistoryFilters(t *testing.T) {
	state := session.New(8)
	workflowCompletedID := state.StartWorkflowRun("audit-flow", "review auth module")
	state.CompleteWorkflowRun(workflowCompletedID, "completed", "audit complete", "", "", nil, []session.WorkflowRunStageSnapshot{{
		Stage:   "audit",
		AgentID: "auditor",
		Tool:    "read_file",
		Status:  "completed",
		Summary: "reviewed auth",
		Result:  schema.AgentResult{Output: "auth review complete"},
	}})
	workflowPendingID := state.StartWorkflowRun("manual-flow", "approval required")
	state.CompleteWorkflowRun(workflowPendingID, "awaiting_input", "waiting for approval target", "collect", "provide target", []schema.WorkflowInputField{{
		Name:     "target",
		Label:    "Target",
		Required: true,
	}}, nil)
	runtimeRef := newAPITestRuntimeWithStateAndResponses(t, state, nil)
	agentCompletedID := runtimeRef.StartAgentRun("write docs")
	runtimeRef.AppendAgentRunEvent(agentCompletedID, session.AgentRunEventSnapshot{
		Type:    string(schema.StreamEventText),
		Content: "docs drafted",
		AgentID: "chat",
		Mode:    "chat",
	})
	runtimeRef.CompleteAgentRun(agentCompletedID, "completed", schema.AgentResult{
		Output:  "docs complete",
		AgentID: "chat",
		Mode:    "chat",
	})
	agentFailedID := runtimeRef.StartAgentRun("fix release")
	runtimeRef.AppendAgentRunEvent(agentFailedID, session.AgentRunEventSnapshot{
		Type:     string(schema.StreamEventToolCall),
		Content:  "release write failed",
		ToolName: "write_file",
		AgentID:  "fixer",
		Mode:     "fix",
	})
	runtimeRef.FailAgentRun(agentFailedID, "release failed")
	server := NewServer(runtimeRef)

	allRequest := httptest.NewRequest(http.MethodGet, "/api/runs", nil)
	allResponse := httptest.NewRecorder()
	server.ServeHTTP(allResponse, allRequest)
	if allResponse.Code != http.StatusOK {
		t.Fatalf("expected unified runs 200, got %d body=%s", allResponse.Code, allResponse.Body.String())
	}
	var all runCollectionResponse
	if err := json.NewDecoder(allResponse.Body).Decode(&all); err != nil {
		t.Fatalf("decode unified run collection: %v", err)
	}
	if all.Counts.Total != 4 || all.Counts.Agent != 2 || all.Counts.Workflow != 2 || all.Counts.Matched != 4 || all.Counts.Returned != 4 || all.Counts.Active != 1 || all.Counts.Terminal != 3 || all.Counts.NeedsAction != 1 || all.Counts.Errors != 1 {
		t.Fatalf("unexpected unified run counts: %#v", all.Counts)
	}
	if !runFacetContains(all.Facets.Types, "agent", 2) || !runFacetContains(all.Facets.Types, "workflow", 2) {
		t.Fatalf("expected type facets for agent/workflow runs, got %#v", all.Facets.Types)
	}
	if !runFacetContains(all.Facets.Statuses, "completed", 2) || !runFacetContains(all.Facets.Statuses, "failed", 1) || !runFacetContains(all.Facets.Statuses, "awaiting_input", 1) {
		t.Fatalf("expected status facets, got %#v", all.Facets.Statuses)
	}
	if !runFacetContains(all.Facets.Agents, "chat", 1) || !runFacetContains(all.Facets.Agents, "auditor", 1) || !runFacetContains(all.Facets.Tools, "write_file", 1) {
		t.Fatalf("expected agent/tool facets, got agents=%#v tools=%#v", all.Facets.Agents, all.Facets.Tools)
	}
	if !runFacetContains(all.Facets.Actions, "retry", 4) || !runFacetContains(all.Facets.Actions, "submit_input", 1) {
		t.Fatalf("expected action facets, got %#v", all.Facets.Actions)
	}
	if len(all.Runs) != 4 {
		t.Fatalf("expected four unified runs, got %#v", all.Runs)
	}
	for _, run := range all.Runs {
		if run.EventsURL == "" || run.ReplayPath == "" || run.ContextPath == "" || run.ActionsPath == "" || run.DiffsPath == "" {
			t.Fatalf("expected run paths for %#v", run)
		}
	}
	if pendingItem := runCollectionItemByID(all.Runs, workflowPendingID); pendingItem == nil ||
		pendingItem.Actions == nil ||
		pendingItem.Actions.Recommended != "submit_input" ||
		pendingItem.Actions.NeedsBody != 1 ||
		!workflowStringSliceContains(pendingItem.Actions.AvailableNames, "submit_input") ||
		!runCollectionAvailableActionExists(pendingItem.Actions.AvailableItems, "submit_input", "/api/workflow-runs/"+workflowPendingID+"/input", true) {
		t.Fatalf("expected pending workflow action summary, got %#v", pendingItem)
	}
	if failedItem := runCollectionItemByID(all.Runs, agentFailedID); failedItem == nil ||
		failedItem.Actions == nil ||
		failedItem.Actions.Recommended != "retry" ||
		!workflowStringSliceContains(failedItem.Actions.AvailableNames, "retry") ||
		!runCollectionAvailableActionExists(failedItem.Actions.AvailableItems, "retry", "/api/agent-runs/"+agentFailedID+"/retry", false) {
		t.Fatalf("expected failed agent retry action summary, got %#v", failedItem)
	}

	agentRequest := httptest.NewRequest(http.MethodGet, "/api/runs?type=agent&agent=chat&status=completed", nil)
	agentResponse := httptest.NewRecorder()
	server.ServeHTTP(agentResponse, agentRequest)
	if agentResponse.Code != http.StatusOK {
		t.Fatalf("expected filtered agent unified runs 200, got %d body=%s", agentResponse.Code, agentResponse.Body.String())
	}
	var agentRuns runCollectionResponse
	if err := json.NewDecoder(agentResponse.Body).Decode(&agentRuns); err != nil {
		t.Fatalf("decode filtered agent unified runs: %v", err)
	}
	if len(agentRuns.Runs) != 1 || agentRuns.Runs[0].ID != agentCompletedID || agentRuns.Runs[0].Type != "agent" || agentRuns.Filters.Type != "agent" || agentRuns.Filters.AgentID != "chat" {
		t.Fatalf("expected completed chat agent run, filters=%#v runs=%#v", agentRuns.Filters, agentRuns.Runs)
	}

	workflowRequest := httptest.NewRequest(http.MethodGet, "/api/runs?type=workflow&needs_action=1&q=approval", nil)
	workflowResponse := httptest.NewRecorder()
	server.ServeHTTP(workflowResponse, workflowRequest)
	if workflowResponse.Code != http.StatusOK {
		t.Fatalf("expected filtered workflow unified runs 200, got %d body=%s", workflowResponse.Code, workflowResponse.Body.String())
	}
	var workflowRuns runCollectionResponse
	if err := json.NewDecoder(workflowResponse.Body).Decode(&workflowRuns); err != nil {
		t.Fatalf("decode filtered workflow unified runs: %v", err)
	}
	if len(workflowRuns.Runs) != 1 || workflowRuns.Runs[0].ID != workflowPendingID || workflowRuns.Runs[0].Type != "workflow" || !workflowRuns.Runs[0].NeedsAction {
		t.Fatalf("expected pending workflow run, filters=%#v runs=%#v", workflowRuns.Filters, workflowRuns.Runs)
	}

	errorRequest := httptest.NewRequest(http.MethodGet, "/api/runs?errors_only=1&tool=write_file&q=release", nil)
	errorResponse := httptest.NewRecorder()
	server.ServeHTTP(errorResponse, errorRequest)
	if errorResponse.Code != http.StatusOK {
		t.Fatalf("expected error unified runs 200, got %d body=%s", errorResponse.Code, errorResponse.Body.String())
	}
	var errorRuns runCollectionResponse
	if err := json.NewDecoder(errorResponse.Body).Decode(&errorRuns); err != nil {
		t.Fatalf("decode error unified runs: %v", err)
	}
	if len(errorRuns.Runs) != 1 || errorRuns.Runs[0].ID != agentFailedID || !errorRuns.Runs[0].HasError {
		t.Fatalf("expected failed write_file agent run, filters=%#v runs=%#v", errorRuns.Filters, errorRuns.Runs)
	}

	retryActionRequest := httptest.NewRequest(http.MethodGet, "/api/runs?action=retry", nil)
	retryActionResponse := httptest.NewRecorder()
	server.ServeHTTP(retryActionResponse, retryActionRequest)
	if retryActionResponse.Code != http.StatusOK {
		t.Fatalf("expected retry action unified runs 200, got %d body=%s", retryActionResponse.Code, retryActionResponse.Body.String())
	}
	var retryActionRuns runCollectionResponse
	if err := json.NewDecoder(retryActionResponse.Body).Decode(&retryActionRuns); err != nil {
		t.Fatalf("decode retry action unified runs: %v", err)
	}
	if retryActionRuns.Filters.Action != "retry" || retryActionRuns.Counts.Matched == 0 ||
		runCollectionItemByID(retryActionRuns.Runs, agentFailedID) == nil ||
		runCollectionItemByID(retryActionRuns.Runs, workflowCompletedID) == nil {
		t.Fatalf("expected retry action to match retryable runs, filters=%#v counts=%#v runs=%#v", retryActionRuns.Filters, retryActionRuns.Counts, retryActionRuns.Runs)
	}
	for _, run := range retryActionRuns.Runs {
		if run.Actions == nil || !workflowStringSliceContains(run.Actions.AvailableNames, "retry") {
			t.Fatalf("expected retry-filtered run to expose retry action, got %#v", run)
		}
	}

	inputActionRequest := httptest.NewRequest(http.MethodGet, "/api/runs?action_name=submit_input", nil)
	inputActionResponse := httptest.NewRecorder()
	server.ServeHTTP(inputActionResponse, inputActionRequest)
	if inputActionResponse.Code != http.StatusOK {
		t.Fatalf("expected submit_input action unified runs 200, got %d body=%s", inputActionResponse.Code, inputActionResponse.Body.String())
	}
	var inputActionRuns runCollectionResponse
	if err := json.NewDecoder(inputActionResponse.Body).Decode(&inputActionRuns); err != nil {
		t.Fatalf("decode submit_input action unified runs: %v", err)
	}
	if inputActionRuns.Filters.Action != "submit_input" || len(inputActionRuns.Runs) != 1 || inputActionRuns.Runs[0].ID != workflowPendingID {
		t.Fatalf("expected submit_input action to match pending workflow, filters=%#v runs=%#v", inputActionRuns.Filters, inputActionRuns.Runs)
	}

	missingActionRequest := httptest.NewRequest(http.MethodGet, "/api/runs?action=approve_stage", nil)
	missingActionResponse := httptest.NewRecorder()
	server.ServeHTTP(missingActionResponse, missingActionRequest)
	if missingActionResponse.Code != http.StatusOK {
		t.Fatalf("expected unavailable action unified runs 200, got %d body=%s", missingActionResponse.Code, missingActionResponse.Body.String())
	}
	var missingActionRuns runCollectionResponse
	if err := json.NewDecoder(missingActionResponse.Body).Decode(&missingActionRuns); err != nil {
		t.Fatalf("decode unavailable action unified runs: %v", err)
	}
	if missingActionRuns.Counts.Matched != 0 || len(missingActionRuns.Runs) != 0 {
		t.Fatalf("expected unavailable action to match no runs, counts=%#v runs=%#v", missingActionRuns.Counts, missingActionRuns.Runs)
	}

	limitedRequest := httptest.NewRequest(http.MethodGet, "/api/runs?terminal=1&limit=1", nil)
	limitedResponse := httptest.NewRecorder()
	server.ServeHTTP(limitedResponse, limitedRequest)
	if limitedResponse.Code != http.StatusOK {
		t.Fatalf("expected limited unified runs 200, got %d body=%s", limitedResponse.Code, limitedResponse.Body.String())
	}
	var limited runCollectionResponse
	if err := json.NewDecoder(limitedResponse.Body).Decode(&limited); err != nil {
		t.Fatalf("decode limited unified runs: %v", err)
	}
	if limited.Counts.Matched != 3 || limited.Counts.Returned != 1 || len(limited.Runs) != 1 {
		t.Fatalf("expected terminal filter to match three and return one, got counts=%#v runs=%#v", limited.Counts, limited.Runs)
	}
}

func TestServerAgentRunEventsEndpointSupportsSinceAndStreamReplay(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	server := NewServer(runtimeRef)
	request := httptest.NewRequest(http.MethodPost, "/api/run", strings.NewReader(`{"input":"hello","background":true}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("expected background agent 202, got %d body=%s", response.Code, response.Body.String())
	}
	var accepted agentRunBackgroundResponse
	if err := json.NewDecoder(response.Body).Decode(&accepted); err != nil {
		t.Fatalf("decode accepted agent run: %v", err)
	}
	run := waitAgentRunStatus(t, runtimeRef, accepted.RunID, "completed")
	if len(run.Events) < 2 || run.Events[0].Seq == 0 {
		t.Fatalf("expected sequenced agent events, got %#v", run.Events)
	}

	eventsRequest := httptest.NewRequest(http.MethodGet, "/api/agent-runs/"+run.ID+"/events?since=1", nil)
	eventsResponse := httptest.NewRecorder()
	server.ServeHTTP(eventsResponse, eventsRequest)
	if eventsResponse.Code != http.StatusOK {
		t.Fatalf("expected agent events since 200, got %d body=%s", eventsResponse.Code, eventsResponse.Body.String())
	}
	var events []session.AgentRunEventSnapshot
	if err := json.NewDecoder(eventsResponse.Body).Decode(&events); err != nil {
		t.Fatalf("decode agent since events: %v", err)
	}
	if len(events) == 0 {
		t.Fatalf("expected incremental agent events")
	}
	for _, event := range events {
		if event.Seq <= 1 {
			t.Fatalf("expected event seq > 1, got %#v", event)
		}
	}
	timelineRequest := httptest.NewRequest(http.MethodGet, "/api/agent-runs/"+run.ID+"/timeline?include_content=1", nil)
	timelineResponse := httptest.NewRecorder()
	server.ServeHTTP(timelineResponse, timelineRequest)
	if timelineResponse.Code != http.StatusOK {
		t.Fatalf("expected agent timeline 200, got %d body=%s", timelineResponse.Code, timelineResponse.Body.String())
	}
	var timeline []agentRunTimelineItem
	if err := json.NewDecoder(timelineResponse.Body).Decode(&timeline); err != nil {
		t.Fatalf("decode agent timeline: %v", err)
	}
	if len(timeline) == 0 || !agentRunTimelineContainsKind(timeline, "run") || !agentRunTimelineContainsKind(timeline, "event") {
		t.Fatalf("expected agent timeline to include run and event items, got %#v", timeline)
	}

	replayRequest := httptest.NewRequest(http.MethodGet, "/api/agent-runs/"+run.ID+"/replay?include_content=1", nil)
	replayResponse := httptest.NewRecorder()
	server.ServeHTTP(replayResponse, replayRequest)
	if replayResponse.Code != http.StatusOK {
		t.Fatalf("expected agent replay 200, got %d body=%s", replayResponse.Code, replayResponse.Body.String())
	}
	var replay agentRunReplay
	if err := json.NewDecoder(replayResponse.Body).Decode(&replay); err != nil {
		t.Fatalf("decode agent replay: %v", err)
	}
	if replay.Run.ID != run.ID || len(replay.Events) == 0 || len(replay.Timeline) == 0 || len(replay.Actions) == 0 {
		t.Fatalf("expected agent replay bundle, got %#v", replay)
	}

	exportJSONRequest := httptest.NewRequest(http.MethodGet, "/api/agent-runs/"+run.ID+"/export?format=json&type=agent_run_started&include_content=1", nil)
	exportJSONResponse := httptest.NewRecorder()
	server.ServeHTTP(exportJSONResponse, exportJSONRequest)
	if exportJSONResponse.Code != http.StatusOK || !strings.Contains(exportJSONResponse.Header().Get("Content-Type"), "application/json") || !strings.Contains(exportJSONResponse.Header().Get("Content-Disposition"), ".json") {
		t.Fatalf("expected agent run json export, code=%d headers=%#v body=%s", exportJSONResponse.Code, exportJSONResponse.Header(), exportJSONResponse.Body.String())
	}
	var exportedAgentReplay agentRunReplay
	if err := json.NewDecoder(exportJSONResponse.Body).Decode(&exportedAgentReplay); err != nil {
		t.Fatalf("decode exported agent run replay: %v", err)
	}
	if exportedAgentReplay.Run.ID != run.ID || len(exportedAgentReplay.Events) == 0 || exportedAgentReplay.Filters.EventType != "agent_run_started" {
		t.Fatalf("expected filtered agent json export, got %#v", exportedAgentReplay)
	}

	exportMarkdownRequest := httptest.NewRequest(http.MethodGet, "/api/agent-runs/"+run.ID+"/export?format=md&include_content=1", nil)
	exportMarkdownResponse := httptest.NewRecorder()
	server.ServeHTTP(exportMarkdownResponse, exportMarkdownRequest)
	if exportMarkdownResponse.Code != http.StatusOK || !strings.Contains(exportMarkdownResponse.Header().Get("Content-Type"), "text/markdown") || !strings.Contains(exportMarkdownResponse.Header().Get("Content-Disposition"), ".md") {
		t.Fatalf("expected agent run markdown export, code=%d headers=%#v body=%s", exportMarkdownResponse.Code, exportMarkdownResponse.Header(), exportMarkdownResponse.Body.String())
	}
	markdownBody := exportMarkdownResponse.Body.String()
	if !strings.Contains(markdownBody, "# Agent Run Report") || !strings.Contains(markdownBody, "## Request") || !strings.Contains(markdownBody, "## Output") || !strings.Contains(markdownBody, "hello") {
		t.Fatalf("expected markdown agent run report with content, got %s", markdownBody)
	}

	streamRequest := httptest.NewRequest(http.MethodGet, "/api/agent-runs/"+run.ID+"/events/stream?since=1", nil)
	streamResponse := httptest.NewRecorder()
	server.ServeHTTP(streamResponse, streamRequest)
	if streamResponse.Code != http.StatusOK {
		t.Fatalf("expected agent event stream 200, got %d body=%s", streamResponse.Code, streamResponse.Body.String())
	}
	body := streamResponse.Body.String()
	if !strings.Contains(body, fmt.Sprintf("retry: %d", durableRunSSERetryMillis)) {
		t.Fatalf("expected agent run event stream retry directive, got %q", body)
	}
	if !strings.Contains(body, "event: agent_run_event") || !strings.Contains(body, `"seq":`) || !strings.Contains(body, "event: agent_run_snapshot") {
		t.Fatalf("expected agent run event stream with cursor and final snapshot, got %q", body)
	}
	if strings.Contains(body, "id: 1\n") {
		t.Fatalf("expected stream since=1 to skip first event, got %q", body)
	}

	lastEventRequest := httptest.NewRequest(http.MethodGet, "/api/agent-runs/"+run.ID+"/events/stream", nil)
	lastEventRequest.Header.Set("Last-Event-ID", "1")
	lastEventResponse := httptest.NewRecorder()
	server.ServeHTTP(lastEventResponse, lastEventRequest)
	if lastEventResponse.Code != http.StatusOK {
		t.Fatalf("expected last-event-id agent stream 200, got %d body=%s", lastEventResponse.Code, lastEventResponse.Body.String())
	}
	if strings.Contains(lastEventResponse.Body.String(), "id: 1\n") {
		t.Fatalf("expected Last-Event-ID to skip first event, got %q", lastEventResponse.Body.String())
	}
}

func TestServerRunEventsHydrateExternalizedContentAndDiffsStillWork(t *testing.T) {
	root := t.TempDir()
	store := session.NewArtifactObjectStore(root)
	if err := store.Ensure(); err != nil {
		t.Fatalf("Ensure artifact store: %v", err)
	}
	agentState := session.New(8)
	agentState.SetArtifactObjectStore(store)
	agentRuntime := newAPITestRuntimeWithStateAndResponses(t, agentState, nil)
	agentServer := NewServer(agentRuntime)

	largeDiffPayload := func() string {
		payload := map[string]any{
			"path":           "src/app.py",
			"relative_path":  "src/app.py",
			"status":         "modified",
			"line_summary":   "changed old lines 1-2 -> new lines 1-3 (+2 -1)",
			"old_range":      "1-2",
			"new_range":      "1-3",
			"added_lines":    2,
			"deleted_lines":  1,
			"bytes_written":  42,
			"old_line_count": 2,
			"new_line_count": 3,
			"diff_preview":   strings.Repeat("@@ -1,2 +1,3 @@\n- old\n+ new\n+ more\n", 260),
		}
		data, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal large diff payload: %v", err)
		}
		return string(data)
	}()

	agentRunID := agentState.StartAgentRun("collect agent event content")
	agentState.AppendAgentRunEvent(agentRunID, session.AgentRunEventSnapshot{
		Type:       string(schema.StreamEventToolResult),
		Content:    largeDiffPayload,
		ToolName:   "write_file",
		ToolCallID: "call-agent-diff",
		AgentID:    "fixer",
		Mode:       "fix",
	})
	agentStored := agentRunSnapshotByID(t, agentRuntime, agentRunID)
	agentEvent := agentStored.Events[len(agentStored.Events)-1]
	if agentEvent.ContentArtifactRef == "" || !agentEvent.ContentExternalized || agentEvent.Content == largeDiffPayload {
		t.Fatalf("expected externalized agent event content, got %#v", agentEvent)
	}

	agentEventsRequest := httptest.NewRequest(http.MethodGet, "/api/agent-runs/"+agentRunID+"/events", nil)
	agentEventsResponse := httptest.NewRecorder()
	agentServer.ServeHTTP(agentEventsResponse, agentEventsRequest)
	if agentEventsResponse.Code != http.StatusOK {
		t.Fatalf("expected agent events 200, got %d body=%s", agentEventsResponse.Code, agentEventsResponse.Body.String())
	}
	var agentEvents []session.AgentRunEventSnapshot
	if err := json.NewDecoder(agentEventsResponse.Body).Decode(&agentEvents); err != nil {
		t.Fatalf("decode agent events: %v", err)
	}
	if len(agentEvents) < 2 || agentEvents[len(agentEvents)-1].Content == largeDiffPayload || agentEvents[len(agentEvents)-1].ContentArtifactRef == "" {
		t.Fatalf("expected summarized agent event content by default, got %#v", agentEvents)
	}

	agentFullEventsRequest := httptest.NewRequest(http.MethodGet, "/api/agent-runs/"+agentRunID+"/events?include_content=1", nil)
	agentFullEventsResponse := httptest.NewRecorder()
	agentServer.ServeHTTP(agentFullEventsResponse, agentFullEventsRequest)
	if agentFullEventsResponse.Code != http.StatusOK {
		t.Fatalf("expected full agent events 200, got %d body=%s", agentFullEventsResponse.Code, agentFullEventsResponse.Body.String())
	}
	var agentFullEvents []session.AgentRunEventSnapshot
	if err := json.NewDecoder(agentFullEventsResponse.Body).Decode(&agentFullEvents); err != nil {
		t.Fatalf("decode full agent events: %v", err)
	}
	if len(agentFullEvents) < 2 || agentFullEvents[len(agentFullEvents)-1].Content != largeDiffPayload {
		t.Fatalf("expected hydrated agent event content, got %#v", agentFullEvents)
	}

	agentRunRequest := httptest.NewRequest(http.MethodGet, "/api/agent-runs/"+agentRunID, nil)
	agentRunResponse := httptest.NewRecorder()
	agentServer.ServeHTTP(agentRunResponse, agentRunRequest)
	if agentRunResponse.Code != http.StatusOK {
		t.Fatalf("expected agent run detail 200, got %d body=%s", agentRunResponse.Code, agentRunResponse.Body.String())
	}
	var hydratedAgentRun session.AgentRunSnapshot
	if err := json.NewDecoder(agentRunResponse.Body).Decode(&hydratedAgentRun); err != nil {
		t.Fatalf("decode agent run detail: %v", err)
	}
	if len(hydratedAgentRun.Events) < 2 || hydratedAgentRun.Events[len(hydratedAgentRun.Events)-1].Content != largeDiffPayload {
		t.Fatalf("expected agent run detail to hydrate event content, got %#v", hydratedAgentRun.Events)
	}

	agentDiffRequest := httptest.NewRequest(http.MethodGet, "/api/agent-runs/"+agentRunID+"/diffs", nil)
	agentDiffResponse := httptest.NewRecorder()
	agentServer.ServeHTTP(agentDiffResponse, agentDiffRequest)
	if agentDiffResponse.Code != http.StatusOK {
		t.Fatalf("expected agent diff summary 200, got %d body=%s", agentDiffResponse.Code, agentDiffResponse.Body.String())
	}
	var agentDiffs runDiffResponse
	if err := json.NewDecoder(agentDiffResponse.Body).Decode(&agentDiffs); err != nil {
		t.Fatalf("decode agent diff summary: %v", err)
	}
	if len(agentDiffs.Diffs) != 1 || agentDiffs.Diffs[0].Path != "src/app.py" || agentDiffs.Diffs[0].Patch != "" {
		t.Fatalf("expected summarized agent diff parsed from externalized event content, got %#v", agentDiffs)
	}

	workflowState := session.New(8)
	workflowState.SetArtifactObjectStore(store)
	workflowRuntime := newAPITestRuntimeWithStateAndResponses(t, workflowState, nil)
	workflowServer := NewServer(workflowRuntime)
	workflowRunID := workflowState.StartWorkflowRun("event-flow", "collect workflow event content")
	workflowState.AppendWorkflowRunEvent(workflowRunID, session.WorkflowRunEventSnapshot{
		Type:       string(schema.StreamEventToolResult),
		Stage:      "fix",
		Content:    largeDiffPayload,
		ToolName:   "write_file",
		ToolCallID: "call-workflow-diff",
		AgentID:    "fixer",
		Mode:       "fix",
	})
	workflowStored := workflowRunSnapshotByID(t, workflowRuntime, workflowRunID)
	workflowEvent := workflowStored.Events[len(workflowStored.Events)-1]
	if workflowEvent.ContentArtifactRef == "" || !workflowEvent.ContentExternalized || workflowEvent.Content == largeDiffPayload {
		t.Fatalf("expected externalized workflow event content, got %#v", workflowEvent)
	}

	workflowEventsRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+workflowRunID+"/events", nil)
	workflowEventsResponse := httptest.NewRecorder()
	workflowServer.ServeHTTP(workflowEventsResponse, workflowEventsRequest)
	if workflowEventsResponse.Code != http.StatusOK {
		t.Fatalf("expected workflow events 200, got %d body=%s", workflowEventsResponse.Code, workflowEventsResponse.Body.String())
	}
	var workflowEvents []session.WorkflowRunEventSnapshot
	if err := json.NewDecoder(workflowEventsResponse.Body).Decode(&workflowEvents); err != nil {
		t.Fatalf("decode workflow events: %v", err)
	}
	if len(workflowEvents) < 2 || workflowEvents[len(workflowEvents)-1].Content == largeDiffPayload || workflowEvents[len(workflowEvents)-1].ContentArtifactRef == "" {
		t.Fatalf("expected summarized workflow event content by default, got %#v", workflowEvents)
	}

	workflowFullEventsRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+workflowRunID+"/events?include_content=1", nil)
	workflowFullEventsResponse := httptest.NewRecorder()
	workflowServer.ServeHTTP(workflowFullEventsResponse, workflowFullEventsRequest)
	if workflowFullEventsResponse.Code != http.StatusOK {
		t.Fatalf("expected full workflow events 200, got %d body=%s", workflowFullEventsResponse.Code, workflowFullEventsResponse.Body.String())
	}
	var workflowFullEvents []session.WorkflowRunEventSnapshot
	if err := json.NewDecoder(workflowFullEventsResponse.Body).Decode(&workflowFullEvents); err != nil {
		t.Fatalf("decode full workflow events: %v", err)
	}
	if len(workflowFullEvents) < 2 || workflowFullEvents[len(workflowFullEvents)-1].Content != largeDiffPayload {
		t.Fatalf("expected hydrated workflow event content, got %#v", workflowFullEvents)
	}

	workflowDiffRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+workflowRunID+"/diffs", nil)
	workflowDiffResponse := httptest.NewRecorder()
	workflowServer.ServeHTTP(workflowDiffResponse, workflowDiffRequest)
	if workflowDiffResponse.Code != http.StatusOK {
		t.Fatalf("expected workflow diff summary 200, got %d body=%s", workflowDiffResponse.Code, workflowDiffResponse.Body.String())
	}
	var workflowDiffs runDiffResponse
	if err := json.NewDecoder(workflowDiffResponse.Body).Decode(&workflowDiffs); err != nil {
		t.Fatalf("decode workflow diff summary: %v", err)
	}
	if len(workflowDiffs.Diffs) != 1 || workflowDiffs.Diffs[0].Path != "src/app.py" || workflowDiffs.Diffs[0].Patch != "" {
		t.Fatalf("expected summarized workflow diff parsed from externalized event content, got %#v", workflowDiffs)
	}

	fullSessionRequest := httptest.NewRequest(http.MethodGet, "/api/session?full=1", nil)
	fullSessionResponse := httptest.NewRecorder()
	workflowServer.ServeHTTP(fullSessionResponse, fullSessionRequest)
	if fullSessionResponse.Code != http.StatusOK {
		t.Fatalf("expected full session 200, got %d body=%s", fullSessionResponse.Code, fullSessionResponse.Body.String())
	}
	var fullSnapshot session.Snapshot
	if err := json.NewDecoder(fullSessionResponse.Body).Decode(&fullSnapshot); err != nil {
		t.Fatalf("decode full session: %v", err)
	}
	if len(fullSnapshot.WorkflowRuns) != 1 || len(fullSnapshot.WorkflowRuns[0].Events) < 2 || fullSnapshot.WorkflowRuns[0].Events[len(fullSnapshot.WorkflowRuns[0].Events)-1].Content != largeDiffPayload {
		t.Fatalf("expected full session to hydrate workflow event content, got %#v", fullSnapshot.WorkflowRuns)
	}
}

func TestServerRunContextEndpointsReturnCompactPromptDiagnostics(t *testing.T) {
	state := session.New(8)
	runtimeRef := newAPITestRuntimeWithStateAndResponses(t, state, []schema.ChatResponse{{Message: schema.Message{Content: "unused"}}})
	agentRunID := runtimeRef.StartAgentRun("inspect token budget")
	agentBudget := schema.PromptBudget{
		EstimatedPromptTokens:       120,
		CacheablePrefixTokens:       50,
		AgentID:                     "planner",
		Mode:                        "plan",
		ToolSchemaSelection:         "policy_filtered",
		ExposedToolCount:            1,
		TotalToolCount:              2,
		FilteredToolCount:           1,
		ToolSchemaDiagnosticCount:   2,
		MemoryBlockCount:            1,
		MemoryOmittedCount:          1,
		MemoryEstimatedSavedTokens:  400,
		ArtifactRefCount:            1,
		CompactedToolResultCount:    1,
		ArtifactOmittedTokens:       30,
		SkillOmittedTokens:          50,
		HistoryEstimatedSavedTokens: 60,
		MemoryBlocks: []schema.PromptContextBlock{{
			Kind: "project", Title: "Project memory", Ref: ".goflow/memory/project.md", Tokens: 12, Score: 80,
		}, {
			Kind: "file", Title: "auth.go", Ref: "auth.go", Tokens: 8, Score: 90, Hash: "abcdef1234567890", Language: "go", Size: 2048, ContentMode: "summary", EstimatedSavedTokens: 500,
		}},
		OmittedContext: []string{"tool result compacted; ref=sha256:agent-object"},
		ArtifactRefs:   []string{"sha256:agent-object"},
		InjectedToolSchemas: []schema.PromptToolSchema{{
			Name: "read_file", Status: "injected", SchemaHash: "read-hash", Tokens: 10,
		}},
		FilteredToolSchemas: []schema.PromptToolSchema{{
			Name: "write_file", Status: "filtered", Reason: "read-only policy", SchemaHash: "write-hash", Tokens: 20,
		}},
	}
	runtimeRef.AppendAgentRunEvent(agentRunID, session.AgentRunEventSnapshot{
		Type:         string(schema.StreamEventPromptBudget),
		Content:      "FULL_SYSTEM_PROMPT_SHOULD_NOT_LEAK secret_schema_property",
		AgentID:      "planner",
		Mode:         "plan",
		PromptBudget: &agentBudget,
	})
	runtimeRef.CompleteAgentRun(agentRunID, "completed", schema.AgentResult{Output: "done", AgentID: "planner", Mode: "plan"})

	workflowRunID := state.StartWorkflowRun("review-flow", "audit memory context")
	workflowBudget := schema.PromptBudget{
		EstimatedPromptTokens:      240,
		CacheablePrefixTokens:      90,
		AgentID:                    "auditor",
		Mode:                       "audit",
		WorkflowName:               "review-flow",
		TaskStage:                  "review",
		ToolSchemaSelection:        "all_visible",
		ExposedToolCount:           1,
		TotalToolCount:             1,
		ToolSchemaDiagnosticCount:  1,
		MemoryBlockCount:           1,
		MemoryEstimatedSavedTokens: 220,
		ArtifactRefCount:           1,
		MemoryBlocks: []schema.PromptContextBlock{{
			Kind: "task", Title: "Task summary", Ref: ".goflow/memory/tasks/task-1.json", Tokens: 20, Score: 70,
		}},
		ArtifactRefs: []string{"sha256:workflow-object"},
		InjectedToolSchemas: []schema.PromptToolSchema{{
			Name: "search_files", Status: "injected", SchemaHash: "search-hash", Tokens: 18,
		}},
	}
	state.AppendWorkflowRunEvent(workflowRunID, session.WorkflowRunEventSnapshot{
		Type:         string(schema.StreamEventPromptBudget),
		Stage:        "review",
		Content:      "WORKFLOW_FULL_PROMPT_SHOULD_NOT_LEAK",
		AgentID:      "auditor",
		Mode:         "audit",
		WorkflowName: "review-flow",
		PromptBudget: &workflowBudget,
	})

	server := NewServer(runtimeRef)
	agentRequest := httptest.NewRequest(http.MethodGet, "/api/agent-runs/"+agentRunID+"/context", nil)
	agentResponse := httptest.NewRecorder()
	server.ServeHTTP(agentResponse, agentRequest)
	if agentResponse.Code != http.StatusOK {
		t.Fatalf("expected agent context 200, got %d body=%s", agentResponse.Code, agentResponse.Body.String())
	}
	if strings.Contains(agentResponse.Body.String(), "FULL_SYSTEM_PROMPT_SHOULD_NOT_LEAK") || strings.Contains(agentResponse.Body.String(), "secret_schema_property") {
		t.Fatalf("expected agent context to omit raw event/prompt content, got %s", agentResponse.Body.String())
	}
	var agentContext runContextResponse
	if err := json.NewDecoder(agentResponse.Body).Decode(&agentContext); err != nil {
		t.Fatalf("decode agent context: %v", err)
	}
	if agentContext.RunID != agentRunID || agentContext.RunType != "agent" || agentContext.Latest == nil || agentContext.Latest.Budget.EstimatedPromptTokens != 120 {
		t.Fatalf("expected latest agent budget, got %#v", agentContext)
	}
	if agentContext.Counts.PromptBudgetSamples != 1 || agentContext.Counts.MemoryEstimatedSavedTokens != 400 || agentContext.Counts.ArtifactRefs != 1 {
		t.Fatalf("expected agent context counts, got %#v", agentContext.Counts)
	}
	if len(agentContext.MemoryBlocks) != 2 || agentContext.MemoryBlocks[0].Ref != ".goflow/memory/project.md" || len(agentContext.Artifacts) != 1 {
		t.Fatalf("expected compact memory/artifact diagnostics, got %#v", agentContext)
	}
	if agentContext.MemoryBlocks[1].Ref != "auth.go" || agentContext.MemoryBlocks[1].ContentMode != "summary" || agentContext.MemoryBlocks[1].Hash == "" || agentContext.MemoryBlocks[1].EstimatedSavedTokens != 500 {
		t.Fatalf("expected file summary context diagnostics, got %#v", agentContext.MemoryBlocks[1])
	}
	if agentContext.ToolSchema.Selection != "policy_filtered" || len(agentContext.ToolSchema.Injected) != 1 || len(agentContext.ToolSchema.Filtered) != 1 {
		t.Fatalf("expected agent tool schema visibility, got %#v", agentContext.ToolSchema)
	}

	unifiedRequest := httptest.NewRequest(http.MethodGet, "/api/runs/"+agentRunID+"/context", nil)
	unifiedResponse := httptest.NewRecorder()
	server.ServeHTTP(unifiedResponse, unifiedRequest)
	if unifiedResponse.Code != http.StatusOK {
		t.Fatalf("expected unified agent context 200, got %d body=%s", unifiedResponse.Code, unifiedResponse.Body.String())
	}
	var unifiedContext runContextResponse
	if err := json.NewDecoder(unifiedResponse.Body).Decode(&unifiedContext); err != nil {
		t.Fatalf("decode unified context: %v", err)
	}
	if unifiedContext.RunID != agentRunID || unifiedContext.RunType != "agent" || unifiedContext.Latest == nil {
		t.Fatalf("expected unified context to resolve agent run, got %#v", unifiedContext)
	}

	workflowRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+workflowRunID+"/context", nil)
	workflowResponse := httptest.NewRecorder()
	server.ServeHTTP(workflowResponse, workflowRequest)
	if workflowResponse.Code != http.StatusOK {
		t.Fatalf("expected workflow context 200, got %d body=%s", workflowResponse.Code, workflowResponse.Body.String())
	}
	if strings.Contains(workflowResponse.Body.String(), "WORKFLOW_FULL_PROMPT_SHOULD_NOT_LEAK") {
		t.Fatalf("expected workflow context to omit raw event content, got %s", workflowResponse.Body.String())
	}
	var workflowContext runContextResponse
	if err := json.NewDecoder(workflowResponse.Body).Decode(&workflowContext); err != nil {
		t.Fatalf("decode workflow context: %v", err)
	}
	if workflowContext.RunID != workflowRunID || workflowContext.RunType != "workflow" || workflowContext.WorkflowName != "review-flow" || workflowContext.Latest == nil {
		t.Fatalf("expected workflow context metadata, got %#v", workflowContext)
	}
	if workflowContext.Latest.Stage != "review" || workflowContext.Latest.Budget.EstimatedPromptTokens != 240 || workflowContext.ToolSchema.Selection != "all_visible" {
		t.Fatalf("expected workflow latest budget and tool visibility, got %#v", workflowContext)
	}
}

func TestServerAgentRunSummaryEndpointOmitsReplayContent(t *testing.T) {
	diffContent := testWriteDiffToolContent(t)
	runtimeRef := newAPITestRuntime(t)
	runID := runtimeRef.StartAgentRun(strings.Repeat("inspect api response ", 120))
	runtimeRef.AppendAgentRunEvent(runID, session.AgentRunEventSnapshot{
		Type:       string(schema.StreamEventToolResult),
		Content:    diffContent,
		ToolName:   "write_file",
		ToolCallID: "call-summary",
		AgentID:    "fixer",
		Mode:       "fix",
	})
	runtimeRef.CompleteAgentRun(runID, "completed", schema.AgentResult{
		Output:  strings.Repeat("large final output ", 120),
		AgentID: "fixer",
		Mode:    "fix",
		ToolResults: []schema.ToolResult{{
			CallID:   "call-summary",
			ToolName: "write_file",
			Content:  diffContent,
		}},
	})
	server := NewServer(runtimeRef)

	summaryRequest := httptest.NewRequest(http.MethodGet, "/api/agent-runs/"+runID+"?summary=1", nil)
	summaryResponse := httptest.NewRecorder()
	server.ServeHTTP(summaryResponse, summaryRequest)
	if summaryResponse.Code != http.StatusOK {
		t.Fatalf("expected agent summary 200, got %d body=%s", summaryResponse.Code, summaryResponse.Body.String())
	}
	var summary session.AgentRunSnapshot
	if err := json.NewDecoder(summaryResponse.Body).Decode(&summary); err != nil {
		t.Fatalf("decode agent summary: %v", err)
	}
	if summary.ID != runID || summary.EventsCount == 0 || summary.DiffsCount == 0 {
		t.Fatalf("expected agent summary counts, got %#v", summary)
	}
	if len(summary.Events) != 0 || summary.Result != nil || summary.ResumeContext != nil {
		t.Fatalf("expected agent summary to omit replay content, got events=%d result=%#v resume=%#v", len(summary.Events), summary.Result, summary.ResumeContext)
	}
	if len(summary.Request) > sessionSummaryTextBytes+8 || len(summary.Output) > sessionSummaryTextBytes+8 {
		t.Fatalf("expected agent summary text to be compact, request=%d output=%d", len(summary.Request), len(summary.Output))
	}

	diffSummaryRequest := httptest.NewRequest(http.MethodGet, "/api/agent-runs/"+runID+"/diffs", nil)
	diffSummaryResponse := httptest.NewRecorder()
	server.ServeHTTP(diffSummaryResponse, diffSummaryRequest)
	if diffSummaryResponse.Code != http.StatusOK {
		t.Fatalf("expected agent diff summary 200, got %d body=%s", diffSummaryResponse.Code, diffSummaryResponse.Body.String())
	}
	var diffSummary runDiffResponse
	if err := json.NewDecoder(diffSummaryResponse.Body).Decode(&diffSummary); err != nil {
		t.Fatalf("decode agent diff summary: %v", err)
	}
	if len(diffSummary.Diffs) != 1 || diffSummary.Diffs[0].Path != "src/app.py" {
		t.Fatalf("expected one summarized agent diff, got %#v", diffSummary)
	}
	if diffSummary.Diffs[0].Patch != "" || diffSummary.Diffs[0].DiffPreview != "" {
		t.Fatalf("expected agent diff summary to omit patch content, got %#v", diffSummary.Diffs[0])
	}

	diffFullRequest := httptest.NewRequest(http.MethodGet, "/api/agent-runs/"+runID+"/diffs?include_content=1", nil)
	diffFullResponse := httptest.NewRecorder()
	server.ServeHTTP(diffFullResponse, diffFullRequest)
	if diffFullResponse.Code != http.StatusOK {
		t.Fatalf("expected agent diff full 200, got %d body=%s", diffFullResponse.Code, diffFullResponse.Body.String())
	}
	var diffFull runDiffResponse
	if err := json.NewDecoder(diffFullResponse.Body).Decode(&diffFull); err != nil {
		t.Fatalf("decode agent diff full: %v", err)
	}
	if len(diffFull.Diffs) != 1 || diffFull.Diffs[0].Patch == "" || diffFull.Diffs[0].DiffPreview == "" {
		t.Fatalf("expected include_content agent diff to include patch content, got %#v", diffFull)
	}
}

func TestServerAgentRunArtifactsEndpointIsSummaryFirst(t *testing.T) {
	largeToolResult := strings.Repeat("agent artifact body ", 160)
	runtimeRef := newAPITestRuntime(t)
	runID := runtimeRef.StartAgentRun("collect artifacts")
	runtimeRef.AppendAgentRunEvent(runID, session.AgentRunEventSnapshot{
		Type:    string(schema.StreamEventPromptBudget),
		AgentID: "fixer",
		Mode:    "fix",
		PromptBudget: &schema.PromptBudget{
			ArtifactRefs:     []string{"sha256:agent-ref"},
			ArtifactRefCount: 1,
		},
	})
	runtimeRef.CompleteAgentRun(runID, "completed", schema.AgentResult{
		Output:  strings.Repeat("final answer ", 120),
		AgentID: "fixer",
		Mode:    "fix",
		ToolResults: []schema.ToolResult{{
			CallID:   "call-artifact",
			ToolName: "read_file",
			Content:  largeToolResult,
		}},
		Findings: []schema.Finding{{
			Severity: "high",
			Summary:  "important finding",
			Files:    []string{"src/app.go"},
		}},
	})
	server := NewServer(runtimeRef)

	summaryRequest := httptest.NewRequest(http.MethodGet, "/api/agent-runs/"+runID+"/artifacts", nil)
	summaryResponse := httptest.NewRecorder()
	server.ServeHTTP(summaryResponse, summaryRequest)
	if summaryResponse.Code != http.StatusOK {
		t.Fatalf("expected agent artifacts 200, got %d body=%s", summaryResponse.Code, summaryResponse.Body.String())
	}
	if strings.Contains(summaryResponse.Body.String(), largeToolResult) {
		t.Fatalf("agent artifact summary should omit full content, got %s", summaryResponse.Body.String())
	}
	var summary runArtifactsResponse
	if err := json.NewDecoder(summaryResponse.Body).Decode(&summary); err != nil {
		t.Fatalf("decode agent artifacts summary: %v", err)
	}
	if summary.RunID != runID || summary.RunType != "agent" || summary.Counts.Total < 3 || len(summary.Items) == 0 {
		t.Fatalf("expected agent artifact envelope, got %#v", summary)
	}
	if summary.Counts.ContentOmitted == 0 || summary.Counts.ExternalRefs == 0 || summary.Counts.ToolResults == 0 {
		t.Fatalf("expected summary-first artifact counts, got %#v", summary.Counts)
	}
	for _, item := range summary.Items {
		if item.Content != "" {
			t.Fatalf("expected summarized agent artifact to omit content, got %#v", item)
		}
	}

	fullRequest := httptest.NewRequest(http.MethodGet, "/api/agent-runs/"+runID+"/artifacts?include_content=1&artifact_kind=tool_result", nil)
	fullResponse := httptest.NewRecorder()
	server.ServeHTTP(fullResponse, fullRequest)
	if fullResponse.Code != http.StatusOK {
		t.Fatalf("expected full agent artifacts 200, got %d body=%s", fullResponse.Code, fullResponse.Body.String())
	}
	var full runArtifactsResponse
	if err := json.NewDecoder(fullResponse.Body).Decode(&full); err != nil {
		t.Fatalf("decode full agent artifacts: %v", err)
	}
	if len(full.Items) != 1 || !strings.Contains(full.Items[0].Content, "agent artifact body") {
		t.Fatalf("expected include_content agent artifact to include tool result, got %#v", full)
	}

	unifiedRequest := httptest.NewRequest(http.MethodGet, "/api/runs/"+runID+"/artifacts?artifact_kind=finding", nil)
	unifiedResponse := httptest.NewRecorder()
	server.ServeHTTP(unifiedResponse, unifiedRequest)
	if unifiedResponse.Code != http.StatusOK {
		t.Fatalf("expected unified agent artifacts 200, got %d body=%s", unifiedResponse.Code, unifiedResponse.Body.String())
	}
	var unified runArtifactsResponse
	if err := json.NewDecoder(unifiedResponse.Body).Decode(&unified); err != nil {
		t.Fatalf("decode unified agent artifacts: %v", err)
	}
	if unified.RunType != "agent" || len(unified.Items) != 1 || unified.Items[0].Kind != "finding" || unified.Items[0].Content != "" {
		t.Fatalf("expected unified artifact summary filter, got %#v", unified)
	}
}

func TestServerAgentRunArtifactsHydrateExternalizedContent(t *testing.T) {
	root := t.TempDir()
	state := session.New(8)
	store := session.NewArtifactObjectStore(root)
	if err := store.Ensure(); err != nil {
		t.Fatalf("Ensure artifact store: %v", err)
	}
	state.SetArtifactObjectStore(store)
	largeToolResult := strings.Repeat("externalized agent artifact body ", 120)
	runID := state.StartAgentRun("collect externalized agent artifacts")
	state.CompleteAgentRun(runID, "completed", schema.AgentResult{
		Output:  "done",
		AgentID: "fixer",
		Mode:    "fix",
		ToolResults: []schema.ToolResult{{
			CallID:   "call-external",
			ToolName: "read_file",
			Content:  largeToolResult,
		}},
	})
	runtimeRef := newAPITestRuntimeWithStateAndResponses(t, state, nil)
	server := NewServer(runtimeRef)
	snapshot := state.Snapshot()
	if len(snapshot.AgentRuns) != 1 || len(snapshot.AgentRuns[0].Artifacts) == 0 {
		t.Fatalf("expected persisted agent artifacts, got %#v", snapshot.AgentRuns)
	}
	var stored session.AgentRunArtifactSnapshot
	for _, artifact := range snapshot.AgentRuns[0].Artifacts {
		if artifact.Kind == "tool_result" {
			stored = artifact
			break
		}
	}
	if stored.Content != "" || stored.ArtifactRef == "" || stored.Hash == "" {
		t.Fatalf("expected persisted agent artifact to be externalized, got %#v", stored)
	}

	summaryRequest := httptest.NewRequest(http.MethodGet, "/api/agent-runs/"+runID+"/artifacts?artifact_kind=tool_result", nil)
	summaryResponse := httptest.NewRecorder()
	server.ServeHTTP(summaryResponse, summaryRequest)
	if summaryResponse.Code != http.StatusOK {
		t.Fatalf("expected agent artifacts summary 200, got %d body=%s", summaryResponse.Code, summaryResponse.Body.String())
	}
	var summary runArtifactsResponse
	if err := json.NewDecoder(summaryResponse.Body).Decode(&summary); err != nil {
		t.Fatalf("decode agent artifact summary: %v", err)
	}
	if len(summary.Items) != 1 || summary.Items[0].Content != "" || summary.Items[0].ArtifactRef == "" || !summary.Items[0].Externalized {
		t.Fatalf("expected summary-only externalized agent artifact, got %#v", summary)
	}

	fullRequest := httptest.NewRequest(http.MethodGet, "/api/agent-runs/"+runID+"/artifacts?include_content=1&artifact_kind=tool_result", nil)
	fullResponse := httptest.NewRecorder()
	server.ServeHTTP(fullResponse, fullRequest)
	if fullResponse.Code != http.StatusOK {
		t.Fatalf("expected agent artifacts full 200, got %d body=%s", fullResponse.Code, fullResponse.Body.String())
	}
	var full runArtifactsResponse
	if err := json.NewDecoder(fullResponse.Body).Decode(&full); err != nil {
		t.Fatalf("decode agent artifact full: %v", err)
	}
	if len(full.Items) != 1 || full.Items[0].Content != largeToolResult || full.Items[0].Hash != stored.Hash {
		t.Fatalf("expected hydrated agent artifact content, got %#v", full)
	}

	unifiedRequest := httptest.NewRequest(http.MethodGet, "/api/runs/"+runID+"/artifacts?include_content=1&artifact_kind=tool_result", nil)
	unifiedResponse := httptest.NewRecorder()
	server.ServeHTTP(unifiedResponse, unifiedRequest)
	if unifiedResponse.Code != http.StatusOK {
		t.Fatalf("expected unified agent artifacts full 200, got %d body=%s", unifiedResponse.Code, unifiedResponse.Body.String())
	}
	var unified runArtifactsResponse
	if err := json.NewDecoder(unifiedResponse.Body).Decode(&unified); err != nil {
		t.Fatalf("decode unified agent artifact full: %v", err)
	}
	if len(unified.Items) != 1 || unified.Items[0].Content != largeToolResult || !unified.Items[0].Externalized {
		t.Fatalf("expected unified hydrated agent artifact, got %#v", unified)
	}
}

func TestServerAgentRunDiffsHydrateExternalizedArtifactContent(t *testing.T) {
	root := t.TempDir()
	state := session.New(8)
	store := session.NewArtifactObjectStore(root)
	if err := store.Ensure(); err != nil {
		t.Fatalf("Ensure artifact store: %v", err)
	}
	state.SetArtifactObjectStore(store)
	diffContent := testWriteDiffToolContent(t)
	runID := state.StartAgentRun("collect externalized agent diff")
	state.CompleteAgentRun(runID, "completed", schema.AgentResult{
		Output:  "done",
		AgentID: "fixer",
		Mode:    "fix",
		ToolResults: []schema.ToolResult{{
			CallID:   "call-diff",
			ToolName: "write_file",
			Content:  diffContent,
		}},
	})
	runtimeRef := newAPITestRuntimeWithStateAndResponses(t, state, nil)
	server := NewServer(runtimeRef)

	summaryRequest := httptest.NewRequest(http.MethodGet, "/api/agent-runs/"+runID+"/diffs", nil)
	summaryResponse := httptest.NewRecorder()
	server.ServeHTTP(summaryResponse, summaryRequest)
	if summaryResponse.Code != http.StatusOK {
		t.Fatalf("expected agent diff summary 200, got %d body=%s", summaryResponse.Code, summaryResponse.Body.String())
	}
	var summary runDiffResponse
	if err := json.NewDecoder(summaryResponse.Body).Decode(&summary); err != nil {
		t.Fatalf("decode agent diff summary: %v", err)
	}
	if len(summary.Diffs) != 1 || summary.Diffs[0].Path != "src/app.py" || summary.Diffs[0].Patch != "" || summary.Diffs[0].DiffPreview != "" {
		t.Fatalf("expected summary-only externalized agent diff, got %#v", summary)
	}
	if strings.Contains(summaryResponse.Body.String(), `"diff_preview"`) || strings.Contains(summaryResponse.Body.String(), "new line") {
		t.Fatalf("expected agent diff summary to omit diff body, got %s", summaryResponse.Body.String())
	}

	fullRequest := httptest.NewRequest(http.MethodGet, "/api/agent-runs/"+runID+"/diffs?include_content=1", nil)
	fullResponse := httptest.NewRecorder()
	server.ServeHTTP(fullResponse, fullRequest)
	if fullResponse.Code != http.StatusOK {
		t.Fatalf("expected agent diff full 200, got %d body=%s", fullResponse.Code, fullResponse.Body.String())
	}
	var full runDiffResponse
	if err := json.NewDecoder(fullResponse.Body).Decode(&full); err != nil {
		t.Fatalf("decode agent diff full: %v", err)
	}
	if len(full.Diffs) != 1 || full.Diffs[0].Path != "src/app.py" || !strings.Contains(full.Diffs[0].Patch, "+ new") {
		t.Fatalf("expected hydrated externalized agent diff content, got %#v", full)
	}
}

func TestServerAgentRunRetryWorksAfterSessionLoad(t *testing.T) {
	firstState := session.New(8)
	firstRuntime := newAPITestRuntimeWithStateAndResponses(t, firstState, []schema.ChatResponse{
		{Message: schema.Message{Content: "first durable answer"}},
	})
	firstServer := NewServer(firstRuntime)
	startRequest := httptest.NewRequest(http.MethodPost, "/api/run", strings.NewReader(`{"input":"hello durable agent","background":true}`))
	startRequest.Header.Set("Content-Type", "application/json")
	startResponse := httptest.NewRecorder()
	firstServer.ServeHTTP(startResponse, startRequest)
	if startResponse.Code != http.StatusAccepted {
		t.Fatalf("expected background agent 202, got %d body=%s", startResponse.Code, startResponse.Body.String())
	}
	var accepted agentRunBackgroundResponse
	if err := json.NewDecoder(startResponse.Body).Decode(&accepted); err != nil {
		t.Fatalf("decode accepted run: %v", err)
	}
	firstRun := waitAgentRunStatus(t, firstRuntime, accepted.RunID, "completed")
	if firstRun.Output != "first durable answer" || firstRun.Attempt != 1 {
		t.Fatalf("expected first durable run completion, got %#v", firstRun)
	}

	sessionPath := filepath.Join(t.TempDir(), "session.json")
	if err := firstState.Save(sessionPath); err != nil {
		t.Fatalf("save session: %v", err)
	}
	loadedState := session.New(8)
	if err := loadedState.Load(sessionPath); err != nil {
		t.Fatalf("load session: %v", err)
	}
	secondRuntime := newAPITestRuntimeWithStateAndResponses(t, loadedState, []schema.ChatResponse{
		{Message: schema.Message{Content: "retry durable answer"}},
	})
	secondServer := NewServer(secondRuntime)

	actionsRequest := httptest.NewRequest(http.MethodGet, "/api/agent-runs/"+accepted.RunID+"/actions", nil)
	actionsResponse := httptest.NewRecorder()
	secondServer.ServeHTTP(actionsResponse, actionsRequest)
	if actionsResponse.Code != http.StatusOK {
		t.Fatalf("expected agent actions 200 after reload, got %d body=%s", actionsResponse.Code, actionsResponse.Body.String())
	}
	var actions []agentRunAction
	if err := json.NewDecoder(actionsResponse.Body).Decode(&actions); err != nil {
		t.Fatalf("decode agent actions after reload: %v", err)
	}
	if !agentRunActionAvailable(actions, "retry", true) {
		t.Fatalf("expected durable retry action after reload, got %#v", actions)
	}

	retryRequest := httptest.NewRequest(http.MethodPost, "/api/agent-runs/"+accepted.RunID+"/retry", nil)
	retryResponse := httptest.NewRecorder()
	secondServer.ServeHTTP(retryResponse, retryRequest)
	if retryResponse.Code != http.StatusAccepted {
		t.Fatalf("expected agent retry 202 after reload, got %d body=%s", retryResponse.Code, retryResponse.Body.String())
	}
	var retried agentRunBackgroundResponse
	if err := json.NewDecoder(retryResponse.Body).Decode(&retried); err != nil {
		t.Fatalf("decode retried run: %v", err)
	}
	if retried.RunID == "" || retried.RunID == accepted.RunID || retried.RetryOf != accepted.RunID || retried.EventsURL != "/api/agent-runs/"+retried.RunID+"/events/stream" {
		t.Fatalf("expected retry response to point at linked new run, got %#v", retried)
	}
	retryRun := waitAgentRunStatus(t, secondRuntime, retried.RunID, "completed")
	if retryRun.RetryOf != accepted.RunID || retryRun.Attempt != 2 || retryRun.Output != "retry durable answer" {
		t.Fatalf("expected linked retry run after reload, got %#v", retryRun)
	}
	var retryStarted bool
	for _, event := range retryRun.Events {
		if event.Type == "agent_run_started" && strings.Contains(event.Content, accepted.RunID) {
			retryStarted = true
			break
		}
	}
	if !retryStarted {
		t.Fatalf("expected retry start event to reference original run, got %#v", retryRun.Events)
	}
}

func TestServerRunDiffEndpointsParseAgentAndWorkflowWrites(t *testing.T) {
	diffContent := testWriteDiffToolContent(t)

	agentRuntime := newAPITestRuntime(t)
	agentRunID := agentRuntime.StartAgentRun("change a file")
	agentRuntime.AppendAgentRunEvent(agentRunID, session.AgentRunEventSnapshot{
		Type:         string(schema.StreamEventToolResult),
		ToolName:     "write_file",
		ToolCallID:   "call-diff",
		Content:      diffContent,
		AgentID:      "fixer",
		Mode:         "fix",
		TaskStage:    "modify",
		PromptTokens: 9,
		OutputTokens: 3,
	})
	agentRuntime.CompleteAgentRun(agentRunID, "completed", schema.AgentResult{
		Output:  "changed src/app.py",
		AgentID: "fixer",
		Mode:    "fix",
		ToolResults: []schema.ToolResult{{
			CallID:   "call-diff",
			ToolName: "write_file",
			Content:  diffContent,
		}},
	})
	agentServer := NewServer(agentRuntime)
	agentDiffRequest := httptest.NewRequest(http.MethodGet, "/api/agent-runs/"+agentRunID+"/diffs?q=app.py&include_content=1", nil)
	agentDiffResponse := httptest.NewRecorder()
	agentServer.ServeHTTP(agentDiffResponse, agentDiffRequest)
	if agentDiffResponse.Code != http.StatusOK {
		t.Fatalf("expected agent diffs 200, got %d body=%s", agentDiffResponse.Code, agentDiffResponse.Body.String())
	}
	var agentDiffs runDiffResponse
	if err := json.NewDecoder(agentDiffResponse.Body).Decode(&agentDiffs); err != nil {
		t.Fatalf("decode agent diffs: %v", err)
	}
	if agentDiffs.RunKind != "agent" || agentDiffs.Total != 1 || len(agentDiffs.Diffs) != 1 {
		t.Fatalf("expected one agent diff, got %#v", agentDiffs)
	}
	if diff := agentDiffs.Diffs[0]; diff.Path != "src/app.py" || diff.StatusCode != "M" || diff.AddedLines != 2 || diff.DeletedLines != 1 || !strings.Contains(diff.Patch, "diff --goflow") {
		t.Fatalf("expected parsed agent diff, got %#v", diff)
	}
	agentReplayRequest := httptest.NewRequest(http.MethodGet, "/api/agent-runs/"+agentRunID+"/replay?q=app.py", nil)
	agentReplayResponse := httptest.NewRecorder()
	agentServer.ServeHTTP(agentReplayResponse, agentReplayRequest)
	if agentReplayResponse.Code != http.StatusOK || !strings.Contains(agentReplayResponse.Body.String(), `"diffs"`) || !strings.Contains(agentReplayResponse.Body.String(), `"src/app.py"`) {
		t.Fatalf("expected agent replay to include diffs, got %d body=%s", agentReplayResponse.Code, agentReplayResponse.Body.String())
	}

	workflowState := session.New(8)
	workflowRuntime := newAPITestRuntimeWithStateAndResponses(t, workflowState, nil)
	workflowRunID := workflowState.StartWorkflowRun("diff-flow", "change a file")
	workflowState.AppendWorkflowRunEvent(workflowRunID, session.WorkflowRunEventSnapshot{
		Stage:      "fix",
		Type:       string(schema.StreamEventToolResult),
		ToolName:   "write_file",
		ToolCallID: "call-diff",
		Content:    diffContent,
		AgentID:    "fixer",
		Mode:       "fix",
		TaskStage:  "modify",
	})
	workflowState.CompleteWorkflowRun(workflowRunID, "completed", "done", "", "", nil, []session.WorkflowRunStageSnapshot{{
		Stage:       "fix",
		AgentID:     "fixer",
		NodeType:    "agent",
		Status:      "completed",
		CompletedAt: "2026-05-01T00:00:00Z",
		Result: schema.AgentResult{
			Output:  "changed src/app.py",
			AgentID: "fixer",
			Mode:    "fix",
			ToolResults: []schema.ToolResult{{
				CallID:   "call-diff",
				ToolName: "write_file",
				Content:  diffContent,
			}},
		},
	}})
	workflowServer := NewServer(workflowRuntime)
	workflowDiffRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+workflowRunID+"/diffs?stage=fix&q=app.py&include_content=1", nil)
	workflowDiffResponse := httptest.NewRecorder()
	workflowServer.ServeHTTP(workflowDiffResponse, workflowDiffRequest)
	if workflowDiffResponse.Code != http.StatusOK {
		t.Fatalf("expected workflow diffs 200, got %d body=%s", workflowDiffResponse.Code, workflowDiffResponse.Body.String())
	}
	var workflowDiffs runDiffResponse
	if err := json.NewDecoder(workflowDiffResponse.Body).Decode(&workflowDiffs); err != nil {
		t.Fatalf("decode workflow diffs: %v", err)
	}
	if workflowDiffs.RunKind != "workflow" || workflowDiffs.Total != 1 || len(workflowDiffs.Diffs) != 1 {
		t.Fatalf("expected one workflow diff, got %#v", workflowDiffs)
	}
	if diff := workflowDiffs.Diffs[0]; diff.Stage != "fix" || diff.Path != "src/app.py" || diff.StatusCode != "M" || !strings.Contains(diff.Patch, "diff --goflow") {
		t.Fatalf("expected parsed workflow diff, got %#v", diff)
	}
	workflowReplayRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+workflowRunID+"/replay?kind=diff&stage=fix", nil)
	workflowReplayResponse := httptest.NewRecorder()
	workflowServer.ServeHTTP(workflowReplayResponse, workflowReplayRequest)
	if workflowReplayResponse.Code != http.StatusOK || !strings.Contains(workflowReplayResponse.Body.String(), `"diffs"`) || !strings.Contains(workflowReplayResponse.Body.String(), `"src/app.py"`) {
		t.Fatalf("expected workflow replay to include diffs, got %d body=%s", workflowReplayResponse.Code, workflowReplayResponse.Body.String())
	}
}

func TestServerWorkflowRunSummaryAndChildEndpointsOmitContentByDefault(t *testing.T) {
	diffContent := testWriteDiffToolContent(t)
	state := session.New(8)
	runtimeRef := newAPITestRuntimeWithStateAndResponses(t, state, nil)
	runID := state.StartWorkflowRun("summary-flow", strings.Repeat("inspect workflow api ", 120))
	state.AppendWorkflowRunEvent(runID, session.WorkflowRunEventSnapshot{
		Stage:      "fix",
		Type:       string(schema.StreamEventToolResult),
		Content:    diffContent,
		ToolName:   "write_file",
		ToolCallID: "call-summary",
		AgentID:    "fixer",
		Mode:       "fix",
	})
	state.CompleteWorkflowRun(runID, "completed", strings.Repeat("workflow final summary ", 120), "", "", nil, []session.WorkflowRunStageSnapshot{{
		Stage:    "fix",
		AgentID:  "fixer",
		NodeType: "agent",
		Status:   "completed",
		Summary:  "fixed app",
		Result: schema.AgentResult{
			Output:  strings.Repeat("stage output ", 120),
			AgentID: "fixer",
			Mode:    "fix",
			ToolResults: []schema.ToolResult{{
				CallID:   "call-summary",
				ToolName: "write_file",
				Content:  diffContent,
			}},
		},
		Artifacts: []session.WorkflowRunArtifact{{
			ID:         "artifact-diff",
			Stage:      "fix",
			Kind:       "diff",
			Title:      "src/app.py",
			Summary:    "patched app",
			Content:    diffContent,
			ToolName:   "write_file",
			ToolCallID: "call-summary",
			Metadata:   map[string]string{"path": "src/app.py", "status": "modified"},
		}},
	}})
	server := NewServer(runtimeRef)

	summaryRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+runID+"?summary=1", nil)
	summaryResponse := httptest.NewRecorder()
	server.ServeHTTP(summaryResponse, summaryRequest)
	if summaryResponse.Code != http.StatusOK {
		t.Fatalf("expected workflow summary 200, got %d body=%s", summaryResponse.Code, summaryResponse.Body.String())
	}
	var summary session.WorkflowRunSnapshot
	if err := json.NewDecoder(summaryResponse.Body).Decode(&summary); err != nil {
		t.Fatalf("decode workflow summary: %v", err)
	}
	if summary.ID != runID || summary.StagesCount != 1 || summary.ArtifactsCount == 0 || summary.EventsCount == 0 || summary.DiffsCount == 0 {
		t.Fatalf("expected workflow summary counts, got %#v", summary)
	}
	if len(summary.CompletedStages) != 0 || len(summary.Artifacts) != 0 || len(summary.Events) != 0 {
		t.Fatalf("expected workflow summary to omit child collections, got stages=%d artifacts=%d events=%d", len(summary.CompletedStages), len(summary.Artifacts), len(summary.Events))
	}
	if len(summary.Request) > sessionSummaryTextBytes+8 || len(summary.Summary) > sessionSummaryTextBytes+8 {
		t.Fatalf("expected workflow summary text to be compact, request=%d summary=%d", len(summary.Request), len(summary.Summary))
	}

	artifactsRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+runID+"/artifacts", nil)
	artifactsResponse := httptest.NewRecorder()
	server.ServeHTTP(artifactsResponse, artifactsRequest)
	if artifactsResponse.Code != http.StatusOK {
		t.Fatalf("expected artifact summary 200, got %d body=%s", artifactsResponse.Code, artifactsResponse.Body.String())
	}
	var artifacts []session.WorkflowRunArtifact
	if err := json.NewDecoder(artifactsResponse.Body).Decode(&artifacts); err != nil {
		t.Fatalf("decode artifact summary: %v", err)
	}
	if len(artifacts) == 0 || artifacts[0].ID == "" {
		t.Fatalf("expected one artifact summary, got %#v", artifacts)
	}
	for _, artifact := range artifacts {
		if artifact.Content != "" {
			t.Fatalf("expected artifact summary to omit content, got %#v", artifact)
		}
	}

	artifactFullRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+runID+"/artifacts?include_content=1", nil)
	artifactFullResponse := httptest.NewRecorder()
	server.ServeHTTP(artifactFullResponse, artifactFullRequest)
	if artifactFullResponse.Code != http.StatusOK {
		t.Fatalf("expected artifact full 200, got %d body=%s", artifactFullResponse.Code, artifactFullResponse.Body.String())
	}
	var fullArtifacts []session.WorkflowRunArtifact
	if err := json.NewDecoder(artifactFullResponse.Body).Decode(&fullArtifacts); err != nil {
		t.Fatalf("decode artifact full: %v", err)
	}
	if len(fullArtifacts) == 0 || !workflowRunArtifactsContainContent(fullArtifacts) {
		t.Fatalf("expected include_content artifact to include content, got %#v", fullArtifacts)
	}

	unifiedArtifactsRequest := httptest.NewRequest(http.MethodGet, "/api/runs/"+runID+"/artifacts", nil)
	unifiedArtifactsResponse := httptest.NewRecorder()
	server.ServeHTTP(unifiedArtifactsResponse, unifiedArtifactsRequest)
	if unifiedArtifactsResponse.Code != http.StatusOK {
		t.Fatalf("expected unified workflow artifacts 200, got %d body=%s", unifiedArtifactsResponse.Code, unifiedArtifactsResponse.Body.String())
	}
	var unifiedArtifacts runArtifactsResponse
	if err := json.NewDecoder(unifiedArtifactsResponse.Body).Decode(&unifiedArtifacts); err != nil {
		t.Fatalf("decode unified workflow artifacts: %v", err)
	}
	if unifiedArtifacts.RunID != runID || unifiedArtifacts.RunType != "workflow" || unifiedArtifacts.WorkflowName != "summary-flow" || len(unifiedArtifacts.Items) == 0 {
		t.Fatalf("expected unified workflow artifact envelope, got %#v", unifiedArtifacts)
	}
	if unifiedArtifacts.Items[0].Content != "" || unifiedArtifacts.Counts.ContentOmitted == 0 {
		t.Fatalf("expected unified workflow artifact summary to omit content, got %#v", unifiedArtifacts)
	}

	unifiedFullRequest := httptest.NewRequest(http.MethodGet, "/api/runs/"+runID+"/artifacts?include_content=1", nil)
	unifiedFullResponse := httptest.NewRecorder()
	server.ServeHTTP(unifiedFullResponse, unifiedFullRequest)
	if unifiedFullResponse.Code != http.StatusOK {
		t.Fatalf("expected unified workflow full artifacts 200, got %d body=%s", unifiedFullResponse.Code, unifiedFullResponse.Body.String())
	}
	var unifiedFull runArtifactsResponse
	if err := json.NewDecoder(unifiedFullResponse.Body).Decode(&unifiedFull); err != nil {
		t.Fatalf("decode unified workflow full artifacts: %v", err)
	}
	if len(unifiedFull.Items) == 0 || unifiedFull.Items[0].Content == "" {
		t.Fatalf("expected unified include_content workflow artifact to include content, got %#v", unifiedFull)
	}

	stagesRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+runID+"/stages", nil)
	stagesResponse := httptest.NewRecorder()
	server.ServeHTTP(stagesResponse, stagesRequest)
	if stagesResponse.Code != http.StatusOK {
		t.Fatalf("expected stage summary 200, got %d body=%s", stagesResponse.Code, stagesResponse.Body.String())
	}
	var stages []session.WorkflowRunStageSnapshot
	if err := json.NewDecoder(stagesResponse.Body).Decode(&stages); err != nil {
		t.Fatalf("decode stage summary: %v", err)
	}
	if len(stages) != 1 || stages[0].Stage != "fix" {
		t.Fatalf("expected one stage summary, got %#v", stages)
	}
	if stages[0].Result.Output != "" || len(stages[0].Result.ToolResults) != 0 || len(stages[0].Artifacts) == 0 || workflowRunArtifactsContainContent(stages[0].Artifacts) {
		t.Fatalf("expected stage summary to omit heavy result content, got %#v", stages[0])
	}

	stageFullRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+runID+"/stages?include_content=1", nil)
	stageFullResponse := httptest.NewRecorder()
	server.ServeHTTP(stageFullResponse, stageFullRequest)
	if stageFullResponse.Code != http.StatusOK {
		t.Fatalf("expected stage full 200, got %d body=%s", stageFullResponse.Code, stageFullResponse.Body.String())
	}
	var fullStages []session.WorkflowRunStageSnapshot
	if err := json.NewDecoder(stageFullResponse.Body).Decode(&fullStages); err != nil {
		t.Fatalf("decode stage full: %v", err)
	}
	if len(fullStages) != 1 || fullStages[0].Result.Output == "" || len(fullStages[0].Artifacts) == 0 || !workflowRunArtifactsContainContent(fullStages[0].Artifacts) {
		t.Fatalf("expected include_content stage to include result and artifact content, got %#v", fullStages)
	}

	diffSummaryRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+runID+"/diffs", nil)
	diffSummaryResponse := httptest.NewRecorder()
	server.ServeHTTP(diffSummaryResponse, diffSummaryRequest)
	if diffSummaryResponse.Code != http.StatusOK {
		t.Fatalf("expected workflow diff summary 200, got %d body=%s", diffSummaryResponse.Code, diffSummaryResponse.Body.String())
	}
	var diffSummary runDiffResponse
	if err := json.NewDecoder(diffSummaryResponse.Body).Decode(&diffSummary); err != nil {
		t.Fatalf("decode workflow diff summary: %v", err)
	}
	if len(diffSummary.Diffs) != 1 || diffSummary.Diffs[0].Path != "src/app.py" {
		t.Fatalf("expected one summarized workflow diff, got %#v", diffSummary)
	}
	if diffSummary.Diffs[0].Patch != "" || diffSummary.Diffs[0].DiffPreview != "" {
		t.Fatalf("expected workflow diff summary to omit patch content, got %#v", diffSummary.Diffs[0])
	}

	diffFullRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+runID+"/diffs?include_content=1", nil)
	diffFullResponse := httptest.NewRecorder()
	server.ServeHTTP(diffFullResponse, diffFullRequest)
	if diffFullResponse.Code != http.StatusOK {
		t.Fatalf("expected workflow diff full 200, got %d body=%s", diffFullResponse.Code, diffFullResponse.Body.String())
	}
	var diffFull runDiffResponse
	if err := json.NewDecoder(diffFullResponse.Body).Decode(&diffFull); err != nil {
		t.Fatalf("decode workflow diff full: %v", err)
	}
	if len(diffFull.Diffs) != 1 || diffFull.Diffs[0].Patch == "" || diffFull.Diffs[0].DiffPreview == "" {
		t.Fatalf("expected include_content workflow diff to include patch content, got %#v", diffFull)
	}
}

func TestServerWorkflowRunArtifactsHydrateExternalizedContent(t *testing.T) {
	root := t.TempDir()
	state := session.New(8)
	store := session.NewArtifactObjectStore(root)
	if err := store.Ensure(); err != nil {
		t.Fatalf("Ensure artifact store: %v", err)
	}
	state.SetArtifactObjectStore(store)
	diffContent := testWriteDiffToolContent(t)
	runID := state.StartWorkflowRun("external-artifacts", "collect external artifacts")
	state.CompleteWorkflowRun(runID, "completed", "done", "", "", nil, []session.WorkflowRunStageSnapshot{{
		Stage:  "fix",
		Status: "completed",
		Result: schema.AgentResult{Output: "fixed"},
		Artifacts: []session.WorkflowRunArtifact{{
			ID:         "artifact-diff",
			Stage:      "fix",
			Kind:       "diff",
			Title:      "src/app.py",
			Summary:    "patched app",
			Content:    diffContent,
			ToolName:   "write_file",
			ToolCallID: "call-external",
			Metadata: map[string]string{
				"declared": "true",
				"path":     "src/app.py",
				"status":   "modified",
			},
		}},
	}})
	runtimeRef := newAPITestRuntimeWithStateAndResponses(t, state, nil)
	server := NewServer(runtimeRef)

	snapshot := state.Snapshot()
	if len(snapshot.WorkflowRuns) != 1 || len(snapshot.WorkflowRuns[0].Artifacts) == 0 {
		t.Fatalf("expected workflow artifact snapshot, got %#v", snapshot.WorkflowRuns)
	}
	stored := snapshot.WorkflowRuns[0].Artifacts[0]
	if stored.Content != "" || stored.ArtifactRef == "" || !stored.Externalized {
		t.Fatalf("expected persisted workflow artifact to be externalized, got %#v", stored)
	}

	summaryRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+runID+"/artifacts?artifact_kind=diff", nil)
	summaryResponse := httptest.NewRecorder()
	server.ServeHTTP(summaryResponse, summaryRequest)
	if summaryResponse.Code != http.StatusOK {
		t.Fatalf("expected workflow artifacts summary 200, got %d body=%s", summaryResponse.Code, summaryResponse.Body.String())
	}
	var summary []session.WorkflowRunArtifact
	if err := json.NewDecoder(summaryResponse.Body).Decode(&summary); err != nil {
		t.Fatalf("decode workflow artifact summary: %v", err)
	}
	if len(summary) != 1 || summary[0].Content != "" || summary[0].ArtifactRef == "" {
		t.Fatalf("expected summary-only externalized artifact, got %#v", summary)
	}

	fullRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+runID+"/artifacts?include_content=1&artifact_kind=diff", nil)
	fullResponse := httptest.NewRecorder()
	server.ServeHTTP(fullResponse, fullRequest)
	if fullResponse.Code != http.StatusOK {
		t.Fatalf("expected workflow artifacts full 200, got %d body=%s", fullResponse.Code, fullResponse.Body.String())
	}
	var full []session.WorkflowRunArtifact
	if err := json.NewDecoder(fullResponse.Body).Decode(&full); err != nil {
		t.Fatalf("decode workflow artifact full: %v", err)
	}
	if len(full) != 1 || !strings.Contains(full[0].Content, `"diff_preview"`) || full[0].Hash != stored.Hash {
		t.Fatalf("expected hydrated workflow artifact content, got %#v", full)
	}

	stageRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+runID+"/stages?include_content=1", nil)
	stageResponse := httptest.NewRecorder()
	server.ServeHTTP(stageResponse, stageRequest)
	if stageResponse.Code != http.StatusOK {
		t.Fatalf("expected workflow stages full 200, got %d body=%s", stageResponse.Code, stageResponse.Body.String())
	}
	var stages []session.WorkflowRunStageSnapshot
	if err := json.NewDecoder(stageResponse.Body).Decode(&stages); err != nil {
		t.Fatalf("decode workflow stages full: %v", err)
	}
	if len(stages) != 1 || len(stages[0].Artifacts) == 0 || !workflowRunArtifactsContainContent(stages[0].Artifacts) {
		t.Fatalf("expected hydrated stage artifact content, got %#v", stages)
	}

	unifiedRequest := httptest.NewRequest(http.MethodGet, "/api/runs/"+runID+"/artifacts?include_content=1&artifact_kind=diff", nil)
	unifiedResponse := httptest.NewRecorder()
	server.ServeHTTP(unifiedResponse, unifiedRequest)
	if unifiedResponse.Code != http.StatusOK {
		t.Fatalf("expected unified workflow artifacts full 200, got %d body=%s", unifiedResponse.Code, unifiedResponse.Body.String())
	}
	var unified runArtifactsResponse
	if err := json.NewDecoder(unifiedResponse.Body).Decode(&unified); err != nil {
		t.Fatalf("decode unified workflow artifacts full: %v", err)
	}
	if len(unified.Items) != 1 || !strings.Contains(unified.Items[0].Content, `"diff_preview"`) || !unified.Items[0].Externalized || unified.Items[0].Hash != stored.Hash {
		t.Fatalf("expected unified hydrated externalized artifact, got %#v", unified)
	}

	diffRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+runID+"/diffs?include_content=1", nil)
	diffResponse := httptest.NewRecorder()
	server.ServeHTTP(diffResponse, diffRequest)
	if diffResponse.Code != http.StatusOK {
		t.Fatalf("expected workflow diffs full 200, got %d body=%s", diffResponse.Code, diffResponse.Body.String())
	}
	var diffs runDiffResponse
	if err := json.NewDecoder(diffResponse.Body).Decode(&diffs); err != nil {
		t.Fatalf("decode workflow diffs full: %v", err)
	}
	if len(diffs.Diffs) != 1 || !strings.Contains(diffs.Diffs[0].Patch, `"diff_preview"`) {
		t.Fatalf("expected hydrated workflow artifact diff content, got %#v", diffs)
	}
}

func TestServerWorkflowRunToolResultArtifactsHydrateExternalizedContent(t *testing.T) {
	root := t.TempDir()
	state := session.New(8)
	store := session.NewArtifactObjectStore(root)
	if err := store.Ensure(); err != nil {
		t.Fatalf("Ensure artifact store: %v", err)
	}
	state.SetArtifactObjectStore(store)
	content := strings.Repeat("fetched page body ", 900)
	runID := state.StartWorkflowRun("external-tool-results", "collect external tool results")
	state.CompleteWorkflowRun(runID, "completed", "done", "", "", nil, []session.WorkflowRunStageSnapshot{{
		Stage:  "research",
		Status: "completed",
		Result: schema.AgentResult{
			ToolResults: []schema.ToolResult{{
				CallID:   "call-fetch-1",
				ToolName: "fetch_url",
				Content:  content,
			}},
		},
	}})
	runtimeRef := newAPITestRuntimeWithStateAndResponses(t, state, nil)
	server := NewServer(runtimeRef)

	snapshot := state.Snapshot()
	if len(snapshot.WorkflowRuns) != 1 || len(snapshot.WorkflowRuns[0].Artifacts) == 0 {
		t.Fatalf("expected workflow tool artifact snapshot, got %#v", snapshot.WorkflowRuns)
	}
	var stored session.WorkflowRunArtifact
	for _, artifact := range snapshot.WorkflowRuns[0].Artifacts {
		if artifact.Kind == "tool_result" {
			stored = artifact
			break
		}
	}
	if stored.Content != "" || stored.ArtifactRef == "" || !stored.Externalized || stored.ContentBytes != len([]byte(content)) {
		t.Fatalf("expected persisted workflow tool artifact to be externalized, got %#v", stored)
	}

	summaryRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+runID+"/artifacts?artifact_kind=tool_result", nil)
	summaryResponse := httptest.NewRecorder()
	server.ServeHTTP(summaryResponse, summaryRequest)
	if summaryResponse.Code != http.StatusOK {
		t.Fatalf("expected workflow tool artifact summary 200, got %d body=%s", summaryResponse.Code, summaryResponse.Body.String())
	}
	var summary []session.WorkflowRunArtifact
	if err := json.NewDecoder(summaryResponse.Body).Decode(&summary); err != nil {
		t.Fatalf("decode workflow tool artifact summary: %v", err)
	}
	if len(summary) != 1 || summary[0].Content != "" || summary[0].ArtifactRef == "" {
		t.Fatalf("expected summary-only workflow tool artifact, got %#v", summary)
	}

	fullRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+runID+"/artifacts?include_content=1&artifact_kind=tool_result", nil)
	fullResponse := httptest.NewRecorder()
	server.ServeHTTP(fullResponse, fullRequest)
	if fullResponse.Code != http.StatusOK {
		t.Fatalf("expected workflow tool artifact full 200, got %d body=%s", fullResponse.Code, fullResponse.Body.String())
	}
	var full []session.WorkflowRunArtifact
	if err := json.NewDecoder(fullResponse.Body).Decode(&full); err != nil {
		t.Fatalf("decode workflow tool artifact full: %v", err)
	}
	if len(full) != 1 || full[0].Content != content || full[0].Hash != stored.Hash || !full[0].Externalized {
		t.Fatalf("expected hydrated workflow tool artifact content, got %#v", full)
	}
}

func TestServerWorkflowRunStagesHydrateExternalizedTypedValues(t *testing.T) {
	root := t.TempDir()
	store := session.NewArtifactObjectStore(root)
	if err := store.Ensure(); err != nil {
		t.Fatalf("Ensure artifact store: %v", err)
	}
	state := session.New(8)
	state.SetArtifactObjectStore(store)
	large := strings.Repeat("workflow stage payload ", 640)
	runID := state.StartWorkflowRun("external-stage-values", "collect large stage payloads")
	state.CompleteWorkflowRun(runID, "completed", "done", "", "", nil, []session.WorkflowRunStageSnapshot{{
		Stage:  "collect",
		Status: "completed",
		InputValues: map[string]any{
			"target": "auth",
		},
		OutputValues: map[string]any{
			"report":  large,
			"payload": map[string]any{"risk": "low", "scope": "auth"},
		},
		Result: schema.AgentResult{Output: "collect complete"},
	}, {
		Stage:  "plan",
		Status: "completed",
		InputValues: map[string]any{
			"report":  large,
			"payload": map[string]any{"risk": "low", "scope": "auth"},
		},
		Result: schema.AgentResult{Output: "plan complete"},
	}})
	runtimeRef := newAPITestRuntimeWithStateAndResponses(t, state, nil)
	server := NewServer(runtimeRef)

	stored := workflowRunSnapshotByID(t, runtimeRef, runID)
	if len(stored.CompletedStages) != 2 || len(stored.CompletedStages[0].OutputValues) != 0 || stored.CompletedStages[0].OutputValuesArtifactRef == "" || len(stored.CompletedStages[1].InputValues) != 0 || stored.CompletedStages[1].InputValuesArtifactRef == "" {
		t.Fatalf("expected persisted stage values to be externalized, got %#v", stored.CompletedStages)
	}

	runRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+runID, nil)
	runResponse := httptest.NewRecorder()
	server.ServeHTTP(runResponse, runRequest)
	if runResponse.Code != http.StatusOK {
		t.Fatalf("expected workflow run detail 200, got %d body=%s", runResponse.Code, runResponse.Body.String())
	}
	var hydratedRun session.WorkflowRunSnapshot
	if err := json.NewDecoder(runResponse.Body).Decode(&hydratedRun); err != nil {
		t.Fatalf("decode workflow run detail: %v", err)
	}
	if len(hydratedRun.CompletedStages) != 2 || hydratedRun.CompletedStages[0].OutputValues["report"] != large || hydratedRun.CompletedStages[1].InputValues["report"] != large {
		t.Fatalf("expected workflow run detail to hydrate stage values, got %#v", hydratedRun.CompletedStages)
	}

	summaryRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+runID+"/stages", nil)
	summaryResponse := httptest.NewRecorder()
	server.ServeHTTP(summaryResponse, summaryRequest)
	if summaryResponse.Code != http.StatusOK {
		t.Fatalf("expected workflow stages summary 200, got %d body=%s", summaryResponse.Code, summaryResponse.Body.String())
	}
	var summary []session.WorkflowRunStageSnapshot
	if err := json.NewDecoder(summaryResponse.Body).Decode(&summary); err != nil {
		t.Fatalf("decode workflow stages summary: %v", err)
	}
	if len(summary) != 2 || len(summary[0].OutputValues) != 0 || summary[0].OutputValuesArtifactRef == "" || len(summary[1].InputValues) != 0 || summary[1].InputValuesArtifactRef == "" {
		t.Fatalf("expected workflow stages summary to keep refs without hydrating, got %#v", summary)
	}

	fullRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+runID+"/stages?include_content=1", nil)
	fullResponse := httptest.NewRecorder()
	server.ServeHTTP(fullResponse, fullRequest)
	if fullResponse.Code != http.StatusOK {
		t.Fatalf("expected workflow stages full 200, got %d body=%s", fullResponse.Code, fullResponse.Body.String())
	}
	var full []session.WorkflowRunStageSnapshot
	if err := json.NewDecoder(fullResponse.Body).Decode(&full); err != nil {
		t.Fatalf("decode workflow stages full: %v", err)
	}
	if len(full) != 2 || full[0].OutputValues["report"] != large || full[1].InputValues["report"] != large {
		t.Fatalf("expected workflow stages include_content to hydrate typed values, got %#v", full)
	}

	replayRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+runID+"/replay?include_content=1", nil)
	replayResponse := httptest.NewRecorder()
	server.ServeHTTP(replayResponse, replayRequest)
	if replayResponse.Code != http.StatusOK {
		t.Fatalf("expected workflow replay 200, got %d body=%s", replayResponse.Code, replayResponse.Body.String())
	}
	var replay workflowRunReplay
	if err := json.NewDecoder(replayResponse.Body).Decode(&replay); err != nil {
		t.Fatalf("decode workflow replay: %v", err)
	}
	if len(replay.Stages) != 2 || replay.Stages[0].OutputValues["report"] != large || replay.Stages[1].InputValues["report"] != large {
		t.Fatalf("expected workflow replay stages to hydrate typed values, got %#v", replay.Stages)
	}
}

func workflowRunArtifactsContainContent(artifacts []session.WorkflowRunArtifact) bool {
	for _, artifact := range artifacts {
		if strings.TrimSpace(artifact.Content) != "" {
			return true
		}
	}
	return false
}

func TestServerWorkflowRunReplayIncludesNavigationAndCounts(t *testing.T) {
	diffContent := testWriteDiffToolContent(t)
	state := session.New(8)
	runtimeRef := newAPITestRuntimeWithStateAndResponses(t, state, nil)
	runID := state.StartWorkflowRun("nav-flow", "ship it")
	state.AppendWorkflowRunEvent(runID, session.WorkflowRunEventSnapshot{
		Stage:   "plan",
		Type:    string(schema.StreamEventTaskStage),
		Content: "plan ready",
		AgentID: "planner",
	})
	state.AppendWorkflowRunEvent(runID, session.WorkflowRunEventSnapshot{
		Stage:      "fix",
		Type:       string(schema.StreamEventToolResult),
		Content:    diffContent,
		ToolName:   "write_file",
		ToolCallID: "call-diff",
		AgentID:    "fixer",
		IsError:    true,
	})
	state.CompleteWorkflowRun(runID, "awaiting_input", "needs review", "review", "review required", []schema.WorkflowInputField{{
		Name:     "decision",
		Type:     "select",
		Required: true,
		Options:  []string{"approve", "deny"},
	}}, []session.WorkflowRunStageSnapshot{{
		Stage:    "plan",
		AgentID:  "planner",
		NodeType: "agent",
		Status:   "completed",
		Result: schema.AgentResult{
			Output:  "plan ready",
			AgentID: "planner",
		},
	}, {
		Stage:    "fix",
		AgentID:  "fixer",
		NodeType: "agent",
		Status:   "completed",
		Result: schema.AgentResult{
			Output:  "changed src/app.py",
			AgentID: "fixer",
			ToolResults: []schema.ToolResult{{
				CallID:   "call-diff",
				ToolName: "write_file",
				Content:  diffContent,
				IsError:  true,
			}},
		},
	}})

	server := NewServer(runtimeRef)
	request := httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+runID+"/replay?stage=fix", nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected replay 200, got %d body=%s", response.Code, response.Body.String())
	}
	var replay workflowRunReplay
	if err := json.NewDecoder(response.Body).Decode(&replay); err != nil {
		t.Fatalf("decode replay: %v", err)
	}
	if replay.Navigation.StageCount != 3 || replay.Navigation.CurrentStage != "fix" || replay.Navigation.PreviousStage != "plan" || replay.Navigation.NextStage != "review" {
		t.Fatalf("expected plan/fix/review navigation focused on fix, got %#v", replay.Navigation)
	}
	fix := workflowNavigationItemByStage(replay.Navigation.Stages, "fix")
	if fix == nil || !fix.Current || !fix.Completed || fix.EventCount != 1 || fix.DiffCount != 1 || !fix.HasError || fix.DetailPath == "" || fix.ReplayPath == "" {
		t.Fatalf("expected fix navigation badges and paths, got %#v", fix)
	}
	review := workflowNavigationItemByStage(replay.Navigation.Stages, "review")
	if review == nil || !review.Pending || !review.NeedsAction || review.Status != "awaiting_input" {
		t.Fatalf("expected pending review navigation item, got %#v", review)
	}
	if !replay.Counts.Filtered || replay.Counts.StagesTotal != 2 || replay.Counts.StagesFiltered != 1 || replay.Counts.EventsTotal < 2 || replay.Counts.EventsFiltered != 1 || replay.Counts.DiffsTotal != 1 || replay.Counts.DiffsFiltered != 1 {
		t.Fatalf("expected total and filtered replay counts, got %#v", replay.Counts)
	}

	navRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+runID+"/navigation?anchor=stage:review", nil)
	navResponse := httptest.NewRecorder()
	server.ServeHTTP(navResponse, navRequest)
	if navResponse.Code != http.StatusOK {
		t.Fatalf("expected navigation 200, got %d body=%s", navResponse.Code, navResponse.Body.String())
	}
	var nav workflowRunReplayNavigation
	if err := json.NewDecoder(navResponse.Body).Decode(&nav); err != nil {
		t.Fatalf("decode navigation: %v", err)
	}
	if nav.CurrentStage != "review" || nav.CurrentAnchor != "stage-review" || nav.PreviousStage != "fix" {
		t.Fatalf("expected anchor-focused review navigation, got %#v", nav)
	}
}

func TestServerAgentRunCancelStopsActiveBackgroundRun(t *testing.T) {
	runtimeRef, blockingLLM := newAPICancellableWorkflowRuntime(t)
	server := NewServer(runtimeRef)
	request := httptest.NewRequest(http.MethodPost, "/api/run", strings.NewReader(`{"input":"hello","background":true}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("expected background agent 202, got %d body=%s", response.Code, response.Body.String())
	}
	var accepted agentRunBackgroundResponse
	if err := json.NewDecoder(response.Body).Decode(&accepted); err != nil {
		t.Fatalf("decode accepted agent run: %v", err)
	}
	select {
	case <-blockingLLM.started:
	case <-time.After(2 * time.Second):
		t.Fatal("expected agent LLM call to start")
	}

	cancelRequest := httptest.NewRequest(http.MethodPost, "/api/agent-runs/"+accepted.RunID+"/cancel", nil)
	cancelResponse := httptest.NewRecorder()
	server.ServeHTTP(cancelResponse, cancelRequest)
	if cancelResponse.Code != http.StatusOK {
		t.Fatalf("expected cancel 200, got %d body=%s", cancelResponse.Code, cancelResponse.Body.String())
	}

	run := waitAgentRunStatus(t, runtimeRef, accepted.RunID, "cancelled")
	if run.CancelledAt == "" {
		t.Fatalf("expected cancelled agent run, got %#v", run)
	}
	var requested, cancelled bool
	for _, event := range run.Events {
		if event.Type == "agent_run_cancel_requested" {
			requested = true
		}
		if event.Type == "agent_run_cancelled" {
			cancelled = true
		}
	}
	if !requested || !cancelled {
		t.Fatalf("expected cancellation events, got %#v", run.Events)
	}
}

func TestServerAgentRunTracksAndApprovesMultiplePendingTools(t *testing.T) {
	runtimeRef, mcpClient := newAPIOrdinaryMultiApprovalRuntime(t)
	server := NewServer(runtimeRef)
	request := httptest.NewRequest(http.MethodPost, "/api/run", strings.NewReader(`{"input":"write two files","background":true}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("expected background agent 202, got %d body=%s", response.Code, response.Body.String())
	}
	var accepted agentRunBackgroundResponse
	if err := json.NewDecoder(response.Body).Decode(&accepted); err != nil {
		t.Fatalf("decode accepted agent run: %v", err)
	}
	run := waitAgentRunStatus(t, runtimeRef, accepted.RunID, "awaiting_tool_approval")
	if len(run.PendingApprovals) != 2 {
		t.Fatalf("expected two run-scoped pending approvals, got %#v", run.PendingApprovals)
	}
	for _, pending := range runtimeRef.SessionSnapshot().PendingApprovals {
		if pending.AgentRunID != accepted.RunID {
			t.Fatalf("expected global pending approval to reference agent run %s, got %#v", accepted.RunID, pending)
		}
	}
	run = waitAgentRunActionAvailable(t, server, runtimeRef, accepted.RunID, "approve_all_tools", true)
	actions := server.agentRunActions(run)
	if !agentRunActionAvailable(actions, "approve_all_tools", true) || !agentRunActionAvailable(actions, "deny_all_tools", true) {
		t.Fatalf("expected run-scoped approve/deny all actions, got %#v", actions)
	}
	approveAll := agentRunActionByName(actions, "approve_all_tools")
	if approveAll == nil ||
		approveAll.Kind != "tool_approval" ||
		!approveAll.SupportsBackground ||
		!approveAll.SupportsStream ||
		!approveAll.AcceptsBody ||
		approveAll.RequiresBody ||
		approveAll.Path != "/api/agent-runs/"+accepted.RunID+"/approve-tools" ||
		approveAll.StreamPath != "/api/agent-runs/"+accepted.RunID+"/approve-tools/stream" ||
		approveAll.BodySchema == nil ||
		approveAll.Risk == nil {
		t.Fatalf("expected agent approve_all_tools to advertise action protocol and risk metadata, got %#v", approveAll)
	}
	envelopeRequest := httptest.NewRequest(http.MethodGet, "/api/agent-runs/"+accepted.RunID+"/actions?envelope=1", nil)
	envelopeResponse := httptest.NewRecorder()
	server.ServeHTTP(envelopeResponse, envelopeRequest)
	if envelopeResponse.Code != http.StatusOK {
		t.Fatalf("expected agent action envelope 200, got %d body=%s", envelopeResponse.Code, envelopeResponse.Body.String())
	}
	var envelope agentRunActionDiscovery
	if err := json.NewDecoder(envelopeResponse.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode agent action envelope: %v", err)
	}
	if envelope.Meta.Schema != "goflow.run_actions" ||
		envelope.RunID != accepted.RunID ||
		!envelope.NeedsAction ||
		envelope.Recommended == "" ||
		envelope.Counts.Available == 0 ||
		envelope.Counts.WithRisk == 0 ||
		len(envelope.Actions) == 0 ||
		envelope.EventsPath != "/api/agent-runs/"+accepted.RunID+"/events/stream" {
		t.Fatalf("expected agent action discovery envelope metadata, got %#v", envelope)
	}

	approveRequest := httptest.NewRequest(http.MethodPost, "/api/agent-runs/"+accepted.RunID+"/approve_all_tools", strings.NewReader(`{"background":true}`))
	approveRequest.Header.Set("Content-Type", "application/json")
	approveResponse := httptest.NewRecorder()
	server.ServeHTTP(approveResponse, approveRequest)
	if approveResponse.Code != http.StatusAccepted {
		t.Fatalf("expected approve-all background 202, got %d body=%s", approveResponse.Code, approveResponse.Body.String())
	}
	completed := waitAgentRunStatus(t, runtimeRef, accepted.RunID, "completed")
	if len(completed.PendingApprovals) != 0 || len(runtimeRef.SessionSnapshot().PendingApprovals) != 0 {
		t.Fatalf("expected approvals cleared, run=%#v session=%#v", completed, runtimeRef.SessionSnapshot().PendingApprovals)
	}
	if mcpClient.calls != 2 {
		t.Fatalf("expected two tool calls approved, got %d", mcpClient.calls)
	}
}

func TestServerAgentRunActionsDisableRememberForUnsandboxedRiskyTool(t *testing.T) {
	runtimeRef := newAPIRiskPolicyOrdinaryApprovalRuntime(t)
	server := NewServer(runtimeRef)
	request := httptest.NewRequest(http.MethodPost, "/api/run", strings.NewReader(`{"input":"write one file","background":true}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("expected background agent 202, got %d body=%s", response.Code, response.Body.String())
	}
	var accepted agentRunBackgroundResponse
	if err := json.NewDecoder(response.Body).Decode(&accepted); err != nil {
		t.Fatalf("decode accepted agent run: %v", err)
	}
	run := waitAgentRunStatus(t, runtimeRef, accepted.RunID, "awaiting_tool_approval")
	actions := server.agentRunActions(run)
	if !agentRunActionAvailable(actions, "approve_tool", true) || !agentRunActionAvailable(actions, "deny_tool", true) {
		t.Fatalf("expected approve-once and deny actions to stay available, got %#v", actions)
	}
	remember := agentRunActionByName(actions, "approve_remember_tool")
	if remember == nil {
		t.Fatalf("expected approve_remember_tool action, got %#v", actions)
	}
	if remember.Available || remember.Durable {
		t.Fatalf("expected remember action disabled by tool_risk_policy, got %#v", remember)
	}
	if !strings.Contains(remember.Reason, "tool_risk_policy") {
		t.Fatalf("expected tool_risk_policy reason, got %#v", remember)
	}
}

func TestServerAgentRunActionsDisableRememberAllForUnsandboxedRiskyTools(t *testing.T) {
	runtimeRef := newAPIRiskPolicyOrdinaryMultiApprovalRuntime(t)
	server := NewServer(runtimeRef)
	request := httptest.NewRequest(http.MethodPost, "/api/run", strings.NewReader(`{"input":"write two files","background":true}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("expected background agent 202, got %d body=%s", response.Code, response.Body.String())
	}
	var accepted agentRunBackgroundResponse
	if err := json.NewDecoder(response.Body).Decode(&accepted); err != nil {
		t.Fatalf("decode accepted agent run: %v", err)
	}
	run := waitAgentRunStatus(t, runtimeRef, accepted.RunID, "awaiting_tool_approval")
	actions := server.agentRunActions(run)
	if !agentRunActionAvailable(actions, "approve_all_tools", true) || !agentRunActionAvailable(actions, "deny_all_tools", true) {
		t.Fatalf("expected approve-all once and deny-all actions to stay available, got %#v", actions)
	}
	rememberAll := agentRunActionByName(actions, "approve_remember_all_tools")
	if rememberAll == nil {
		t.Fatalf("expected approve_remember_all_tools action, got %#v", actions)
	}
	if rememberAll.Available || rememberAll.Durable {
		t.Fatalf("expected remember-all action disabled by tool_risk_policy, got %#v", rememberAll)
	}
	if !strings.Contains(rememberAll.Reason, "tool_risk_policy") {
		t.Fatalf("expected tool_risk_policy reason, got %#v", rememberAll)
	}
}

func TestServerAgentRunRejectsUnsandboxedRiskyToolBeforeApproval(t *testing.T) {
	runtimeRef := newAPIRiskPolicyRejectOrdinaryApprovalRuntime(t)
	server := NewServer(runtimeRef)
	accepted, err := server.startAgentBackground("write a file", "run")
	if err != nil {
		t.Fatalf("startAgentBackground: %v", err)
	}
	run := waitAgentRunStatus(t, runtimeRef, accepted.RunID, "denied")
	if len(run.PendingApprovals) != 0 || run.PendingCallID != "" {
		t.Fatalf("expected strict risk policy to reject before approval is queued, got %#v", run)
	}
	if run.Result == nil || len(run.Result.ToolResults) != 1 {
		t.Fatalf("expected denied tool result, got %#v", run)
	}
	result := run.Result.ToolResults[0]
	if !result.Denied || !result.IsError || result.Suspended {
		t.Fatalf("expected denied non-suspended tool result, got %#v", result)
	}
	if !strings.Contains(result.Content, "reject_unsandboxed_risky_tools") {
		t.Fatalf("expected reject_unsandboxed_risky_tools detail, got %#v", result)
	}
	actions := server.agentRunActions(run)
	if agentRunActionByName(actions, "approve_tool") != nil || agentRunActionByName(actions, "deny_tool") != nil {
		t.Fatalf("expected no approval actions after pre-approval rejection, got %#v", actions)
	}
	if !agentRunActionAvailable(actions, "retry", true) {
		t.Fatalf("expected retry to remain available after denied run, got %#v", actions)
	}
}

func TestServerAgentRunToolApprovalResumesAfterSessionLoad(t *testing.T) {
	firstState := session.New(8)
	firstRuntime := newAPITestRuntimeWithStateAndResponses(t, firstState, []schema.ChatResponse{
		{ToolCalls: []schema.ToolCall{{ID: "call-1", Name: "write_file", Arguments: json.RawMessage(`{"path":"note.txt","content":"hello"}`)}}},
	})
	firstServer := NewServer(firstRuntime)
	startRequest := httptest.NewRequest(http.MethodPost, "/api/run", strings.NewReader(`{"input":"write a note file","background":true}`))
	startRequest.Header.Set("Content-Type", "application/json")
	startResponse := httptest.NewRecorder()
	firstServer.ServeHTTP(startResponse, startRequest)
	if startResponse.Code != http.StatusAccepted {
		t.Fatalf("expected background agent 202, got %d body=%s", startResponse.Code, startResponse.Body.String())
	}
	var accepted agentRunBackgroundResponse
	if err := json.NewDecoder(startResponse.Body).Decode(&accepted); err != nil {
		t.Fatalf("decode accepted agent run: %v", err)
	}
	paused := waitAgentRunActionAvailable(t, firstServer, firstRuntime, accepted.RunID, "approve_tool", true)
	if paused.ResumeContext == nil || len(paused.ResumeContext.SuspendedCalls) != 1 {
		t.Fatalf("expected durable ordinary resume context, got %#v", paused.ResumeContext)
	}

	sessionPath := filepath.Join(t.TempDir(), "session.json")
	if err := firstState.Save(sessionPath); err != nil {
		t.Fatalf("save session: %v", err)
	}
	loadedState := session.New(8)
	if err := loadedState.Load(sessionPath); err != nil {
		t.Fatalf("load session: %v", err)
	}
	secondRuntime := newAPITestRuntimeWithStateAndResponses(t, loadedState, []schema.ChatResponse{
		{Message: schema.Message{Content: "done after durable ordinary approval"}, Usage: schema.TokenUsage{PromptTokens: 5, OutputTokens: 4}},
	})
	secondServer := NewServer(secondRuntime)
	actionsRequest := httptest.NewRequest(http.MethodGet, "/api/agent-runs/"+accepted.RunID+"/actions", nil)
	actionsResponse := httptest.NewRecorder()
	secondServer.ServeHTTP(actionsResponse, actionsRequest)
	if actionsResponse.Code != http.StatusOK {
		t.Fatalf("expected actions 200 after reload, got %d body=%s", actionsResponse.Code, actionsResponse.Body.String())
	}
	var actions []agentRunAction
	if err := json.NewDecoder(actionsResponse.Body).Decode(&actions); err != nil {
		t.Fatalf("decode actions: %v", err)
	}
	if !agentRunActionAvailable(actions, "approve_tool", true) || !agentRunActionAvailable(actions, "deny_tool", true) {
		t.Fatalf("expected durable ordinary approval actions after reload, got %#v", actions)
	}

	approveRequest := httptest.NewRequest(http.MethodPost, "/api/agent-runs/"+accepted.RunID+"/approve_tool", nil)
	approveResponse := httptest.NewRecorder()
	secondServer.ServeHTTP(approveResponse, approveRequest)
	if approveResponse.Code != http.StatusOK {
		t.Fatalf("expected ordinary approval resume 200, got %d body=%s", approveResponse.Code, approveResponse.Body.String())
	}
	var completed session.AgentRunSnapshot
	if err := json.NewDecoder(approveResponse.Body).Decode(&completed); err != nil {
		t.Fatalf("decode completed run: %v", err)
	}
	if completed.Status != "completed" || !strings.Contains(completed.Output, "durable ordinary approval") {
		t.Fatalf("expected completed ordinary run after reload, got %#v", completed)
	}
	snapshot := secondRuntime.SessionSnapshot()
	if len(snapshot.PendingApprovals) != 0 {
		t.Fatalf("expected pending approvals cleared after ordinary durable resume, got %#v", snapshot.PendingApprovals)
	}
	run := agentRunSnapshotByID(t, secondRuntime, accepted.RunID)
	if run.ResumeContext != nil || len(run.PendingApprovals) != 0 {
		t.Fatalf("expected resume context and approvals cleared after completion, got %#v", run)
	}
}

func TestServerAgentRunToolApprovalResumesAfterSessionLoadWithExternalizedLargeArguments(t *testing.T) {
	root := t.TempDir()
	store := session.NewArtifactObjectStore(root)
	if err := store.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	content := strings.Repeat("externalized ordinary approval payload ", 420)
	args := `{"path":"note.txt","content":"` + content + `"}`

	firstState := session.New(8)
	firstState.SetArtifactObjectStore(store)
	firstRuntime := newAPITestRuntimeWithStateAndResponses(t, firstState, []schema.ChatResponse{
		{ToolCalls: []schema.ToolCall{{ID: "call-1", Name: "write_file", Arguments: json.RawMessage(args)}}},
	})
	firstServer := NewServer(firstRuntime)
	startRequest := httptest.NewRequest(http.MethodPost, "/api/run", strings.NewReader(`{"input":"write a large note file","background":true}`))
	startRequest.Header.Set("Content-Type", "application/json")
	startResponse := httptest.NewRecorder()
	firstServer.ServeHTTP(startResponse, startRequest)
	if startResponse.Code != http.StatusAccepted {
		t.Fatalf("expected background agent 202, got %d body=%s", startResponse.Code, startResponse.Body.String())
	}
	var accepted agentRunBackgroundResponse
	if err := json.NewDecoder(startResponse.Body).Decode(&accepted); err != nil {
		t.Fatalf("decode accepted agent run: %v", err)
	}
	paused := waitAgentRunActionAvailable(t, firstServer, firstRuntime, accepted.RunID, "approve_tool", true)
	if len(paused.PendingApprovals) != 1 {
		t.Fatalf("expected one pending approval, got %#v", paused.PendingApprovals)
	}
	pending := firstState.Snapshot().PendingApprovals[0]
	if pending.Arguments != "" || pending.ArgumentsArtifactRef == "" || pending.ArgumentsHash == "" || !pending.ArgumentsExternalized {
		t.Fatalf("expected externalized pending approval arguments, got %#v", pending)
	}
	runBeforeSave := agentRunSnapshotByID(t, firstRuntime, accepted.RunID)
	if len(runBeforeSave.PendingApprovals) != 1 || runBeforeSave.PendingApprovals[0].Arguments != "" || runBeforeSave.PendingApprovals[0].ArgumentsArtifactRef == "" {
		t.Fatalf("expected agent run pending approval to store ref metadata, got %#v", runBeforeSave.PendingApprovals)
	}
	if runBeforeSave.ResumeContext == nil || len(runBeforeSave.ResumeContext.SuspendedCalls) != 0 || runBeforeSave.ResumeContext.SuspendedCallsArtifactRef == "" || !runBeforeSave.ResumeContext.SuspendedCallsExternalized {
		t.Fatalf("expected resume context suspended calls to externalize, got %#v", runBeforeSave.ResumeContext)
	}

	sessionPath := filepath.Join(root, "session.json")
	if err := firstState.Save(sessionPath); err != nil {
		t.Fatalf("save session: %v", err)
	}
	loadedState := session.New(8)
	loadedState.SetArtifactObjectStore(store)
	if err := loadedState.Load(sessionPath); err != nil {
		t.Fatalf("load session: %v", err)
	}
	loadedPending := loadedState.Snapshot().PendingApprovals
	if len(loadedPending) != 1 || loadedPending[0].Arguments != "" || loadedPending[0].ArgumentsArtifactRef == "" {
		t.Fatalf("expected loaded session to keep externalized pending approval metadata, got %#v", loadedPending)
	}
	loadedRun, ok := agentRunByID(loadedState.Snapshot().AgentRuns, accepted.RunID)
	if !ok || loadedRun.ResumeContext == nil || len(loadedRun.ResumeContext.SuspendedCalls) != 0 || loadedRun.ResumeContext.SuspendedCallsArtifactRef == "" {
		t.Fatalf("expected loaded run to keep externalized resume context metadata, got %#v ok=%t", loadedRun, ok)
	}

	secondRuntime := newAPITestRuntimeWithStateAndResponses(t, loadedState, []schema.ChatResponse{
		{Message: schema.Message{Content: "done after large durable ordinary approval"}, Usage: schema.TokenUsage{PromptTokens: 5, OutputTokens: 4}},
	})
	secondServer := NewServer(secondRuntime)
	approveRequest := httptest.NewRequest(http.MethodPost, "/api/agent-runs/"+accepted.RunID+"/approve_tool", nil)
	approveResponse := httptest.NewRecorder()
	secondServer.ServeHTTP(approveResponse, approveRequest)
	if approveResponse.Code != http.StatusOK {
		t.Fatalf("expected ordinary approval resume 200, got %d body=%s", approveResponse.Code, approveResponse.Body.String())
	}
	var completed session.AgentRunSnapshot
	if err := json.NewDecoder(approveResponse.Body).Decode(&completed); err != nil {
		t.Fatalf("decode completed run: %v", err)
	}
	if completed.Status != "completed" || !strings.Contains(completed.Output, "large durable ordinary approval") {
		t.Fatalf("expected completed ordinary run after reload, got %#v", completed)
	}
	if len(secondRuntime.SessionSnapshot().PendingApprovals) != 0 {
		t.Fatalf("expected pending approvals cleared after ordinary durable resume, got %#v", secondRuntime.SessionSnapshot().PendingApprovals)
	}
}

func TestServerAgentRunToolApprovalAcceptsHyphenatedActionPaths(t *testing.T) {
	firstState := session.New(8)
	firstRuntime := newAPITestRuntimeWithStateAndResponses(t, firstState, []schema.ChatResponse{
		{ToolCalls: []schema.ToolCall{{ID: "call-1", Name: "write_file", Arguments: json.RawMessage(`{"path":"demo.txt","content":"hello"}`)}}},
	})
	firstServer := NewServer(firstRuntime)
	startRequest := httptest.NewRequest(http.MethodPost, "/api/run", strings.NewReader(`{"input":"write a file","background":true}`))
	startRequest.Header.Set("Content-Type", "application/json")
	startResponse := httptest.NewRecorder()
	firstServer.ServeHTTP(startResponse, startRequest)
	if startResponse.Code != http.StatusAccepted {
		t.Fatalf("expected background agent 202, got %d body=%s", startResponse.Code, startResponse.Body.String())
	}
	var accepted agentRunBackgroundResponse
	if err := json.NewDecoder(startResponse.Body).Decode(&accepted); err != nil {
		t.Fatalf("decode accepted agent run: %v", err)
	}
	paused := waitAgentRunActionAvailable(t, firstServer, firstRuntime, accepted.RunID, "approve_tool", true)
	approve := agentRunActionByName(firstServer.agentRunActions(paused), "approve_tool")
	if approve == nil || approve.Path != "/api/agent-runs/"+accepted.RunID+"/approve-tool" || approve.StreamPath != "/api/agent-runs/"+accepted.RunID+"/approve-tool/stream" {
		t.Fatalf("expected hyphenated approve action paths, got %#v", approve)
	}

	sessionPath := filepath.Join(t.TempDir(), "session.json")
	if err := firstState.Save(sessionPath); err != nil {
		t.Fatalf("save session: %v", err)
	}
	loadedState := session.New(8)
	if err := loadedState.Load(sessionPath); err != nil {
		t.Fatalf("load session: %v", err)
	}
	secondRuntime := newAPITestRuntimeWithStateAndResponses(t, loadedState, []schema.ChatResponse{
		{Message: schema.Message{Content: "done after hyphenated approval"}},
	})
	secondServer := NewServer(secondRuntime)
	approveRequest := httptest.NewRequest(http.MethodPost, "/api/agent-runs/"+accepted.RunID+"/approve-tool", nil)
	approveResponse := httptest.NewRecorder()
	secondServer.ServeHTTP(approveResponse, approveRequest)
	if approveResponse.Code != http.StatusOK {
		t.Fatalf("expected hyphenated approve-tool 200, got %d body=%s", approveResponse.Code, approveResponse.Body.String())
	}
	completed := waitAgentRunStatus(t, secondRuntime, accepted.RunID, "completed")
	if !strings.Contains(completed.Output, "hyphenated approval") {
		t.Fatalf("expected run to complete through hyphenated approval path, got %#v", completed)
	}
}

func TestServerLegacyApprovalStreamResumesDurableAgentRun(t *testing.T) {
	firstState := session.New(8)
	firstRuntime := newAPITestRuntimeWithStateAndResponses(t, firstState, []schema.ChatResponse{
		{ToolCalls: []schema.ToolCall{{ID: "call-1", Name: "write_file", Arguments: json.RawMessage(`{"path":"demo.txt","content":"hello"}`)}}},
	})
	firstServer := NewServer(firstRuntime)
	startRequest := httptest.NewRequest(http.MethodPost, "/api/run", strings.NewReader(`{"input":"write a file","background":true}`))
	startRequest.Header.Set("Content-Type", "application/json")
	startResponse := httptest.NewRecorder()
	firstServer.ServeHTTP(startResponse, startRequest)
	if startResponse.Code != http.StatusAccepted {
		t.Fatalf("expected background agent 202, got %d body=%s", startResponse.Code, startResponse.Body.String())
	}
	var accepted agentRunBackgroundResponse
	if err := json.NewDecoder(startResponse.Body).Decode(&accepted); err != nil {
		t.Fatalf("decode accepted agent run: %v", err)
	}
	paused := waitAgentRunActionAvailable(t, firstServer, firstRuntime, accepted.RunID, "approve_tool", true)
	if len(paused.PendingApprovals) != 1 || paused.PendingApprovals[0].AgentRunID != accepted.RunID {
		t.Fatalf("expected pending approval to reference durable run, got %#v", paused.PendingApprovals)
	}

	sessionPath := filepath.Join(t.TempDir(), "session.json")
	if err := firstState.Save(sessionPath); err != nil {
		t.Fatalf("save session: %v", err)
	}
	loadedState := session.New(8)
	if err := loadedState.Load(sessionPath); err != nil {
		t.Fatalf("load session: %v", err)
	}
	secondRuntime := newAPITestRuntimeWithStateAndResponses(t, loadedState, []schema.ChatResponse{
		{Message: schema.Message{Content: "done through legacy approval stream"}},
	})
	secondServer := NewServer(secondRuntime)
	approveRequest := httptest.NewRequest(http.MethodPost, "/api/approvals/call-1/approve/stream", nil)
	approveResponse := httptest.NewRecorder()
	secondServer.ServeHTTP(approveResponse, approveRequest)
	if approveResponse.Code != http.StatusOK {
		t.Fatalf("expected legacy approval stream 200, got %d body=%s", approveResponse.Code, approveResponse.Body.String())
	}
	if body := approveResponse.Body.String(); !strings.Contains(body, "done through legacy approval stream") {
		t.Fatalf("expected legacy approval stream to include resumed final output, got %s", body)
	}
	completed := waitAgentRunStatus(t, secondRuntime, accepted.RunID, "completed")
	if !strings.Contains(completed.Output, "legacy approval stream") {
		t.Fatalf("expected run to complete through legacy approval stream, got %#v", completed)
	}
	if len(secondRuntime.SessionSnapshot().PendingApprovals) != 0 {
		t.Fatalf("expected pending approvals cleared after legacy approval stream, got %#v", secondRuntime.SessionSnapshot().PendingApprovals)
	}
}

func TestServerWorkflowRunEventsEndpointSupportsSinceAndStreamReplay(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	server := NewServer(runtimeRef)
	request := httptest.NewRequest(http.MethodPost, "/api/workflows/plan-fix-audit", strings.NewReader(`{"input":"ship it"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected workflow 200, got %d body=%s", response.Code, response.Body.String())
	}
	var result agent.WorkflowResult
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatalf("decode workflow result: %v", err)
	}
	run := workflowRunSnapshotByID(t, runtimeRef, result.RunID)
	if len(run.Events) < 2 || run.Events[0].Seq == 0 {
		t.Fatalf("expected sequenced workflow events, got %#v", run.Events)
	}

	eventsRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+run.ID+"/events?since=1", nil)
	eventsResponse := httptest.NewRecorder()
	server.ServeHTTP(eventsResponse, eventsRequest)
	if eventsResponse.Code != http.StatusOK {
		t.Fatalf("expected events since 200, got %d body=%s", eventsResponse.Code, eventsResponse.Body.String())
	}
	var events []session.WorkflowRunEventSnapshot
	if err := json.NewDecoder(eventsResponse.Body).Decode(&events); err != nil {
		t.Fatalf("decode since events: %v", err)
	}
	if len(events) == 0 {
		t.Fatalf("expected incremental events")
	}
	for _, event := range events {
		if event.Seq <= 1 {
			t.Fatalf("expected event seq > 1, got %#v", event)
		}
	}

	streamRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+run.ID+"/events/stream?since=1", nil)
	streamResponse := httptest.NewRecorder()
	server.ServeHTTP(streamResponse, streamRequest)
	if streamResponse.Code != http.StatusOK {
		t.Fatalf("expected event stream 200, got %d body=%s", streamResponse.Code, streamResponse.Body.String())
	}
	body := streamResponse.Body.String()
	if !strings.Contains(body, fmt.Sprintf("retry: %d", durableRunSSERetryMillis)) {
		t.Fatalf("expected workflow run event stream retry directive, got %q", body)
	}
	if !strings.Contains(body, "event: workflow_run_event") || !strings.Contains(body, `"seq":`) || !strings.Contains(body, "event: workflow_run_snapshot") {
		t.Fatalf("expected workflow run event stream with cursor and final snapshot, got %q", body)
	}
	if strings.Contains(body, "id: 1\n") {
		t.Fatalf("expected stream since=1 to skip first event, got %q", body)
	}

	lastEventRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+run.ID+"/events/stream", nil)
	lastEventRequest.Header.Set("Last-Event-ID", "1")
	lastEventResponse := httptest.NewRecorder()
	server.ServeHTTP(lastEventResponse, lastEventRequest)
	if lastEventResponse.Code != http.StatusOK {
		t.Fatalf("expected last-event-id stream 200, got %d body=%s", lastEventResponse.Code, lastEventResponse.Body.String())
	}
	if strings.Contains(lastEventResponse.Body.String(), "id: 1\n") {
		t.Fatalf("expected Last-Event-ID to skip first event, got %q", lastEventResponse.Body.String())
	}
}

func TestSSEWriterHeartbeatUsesCommentFrame(t *testing.T) {
	response := httptest.NewRecorder()
	writer, ok := newSSEWriter(response)
	if !ok {
		t.Fatal("expected recorder to support SSE")
	}
	if err := writer.writeComment("goflow-heartbeat"); err != nil {
		t.Fatalf("write heartbeat: %v", err)
	}
	body := response.Body.String()
	if body != ": goflow-heartbeat\n\n" {
		t.Fatalf("expected SSE comment heartbeat, got %q", body)
	}
	if strings.Contains(body, "event:") || strings.Contains(body, "data:") {
		t.Fatalf("heartbeat should not be a business event frame, got %q", body)
	}
}

func TestSSEWriterRetryUsesRetryDirective(t *testing.T) {
	response := httptest.NewRecorder()
	writer, ok := newSSEWriter(response)
	if !ok {
		t.Fatal("expected recorder to support SSE")
	}
	if err := writer.writeRetry(2500); err != nil {
		t.Fatalf("write retry: %v", err)
	}
	body := response.Body.String()
	if body != "retry: 2500\n\n" {
		t.Fatalf("expected SSE retry directive, got %q", body)
	}
	if strings.Contains(body, "event:") || strings.Contains(body, "data:") {
		t.Fatalf("retry should not be a business event frame, got %q", body)
	}
}

func TestServerWorkflowRunCancelStopsActiveStreamedRun(t *testing.T) {
	runtimeRef, blockingLLM := newAPICancellableWorkflowRuntime(t)
	server := NewServer(runtimeRef)
	request := httptest.NewRequest(http.MethodPost, "/api/workflows/plan-fix-audit/stream", strings.NewReader(`{"input":"ship it"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		server.ServeHTTP(response, request)
		close(done)
	}()

	select {
	case <-blockingLLM.started:
	case <-time.After(2 * time.Second):
		t.Fatal("expected workflow LLM call to start")
	}
	runID := waitWorkflowRunID(t, runtimeRef)
	cancelRequest := httptest.NewRequest(http.MethodPost, "/api/workflow-runs/"+runID+"/cancel", nil)
	cancelResponse := httptest.NewRecorder()
	server.ServeHTTP(cancelResponse, cancelRequest)
	if cancelResponse.Code != http.StatusOK {
		t.Fatalf("expected cancel 200, got %d body=%s", cancelResponse.Code, cancelResponse.Body.String())
	}
	if !strings.Contains(cancelResponse.Body.String(), `"status":"cancelling"`) {
		t.Fatalf("expected cancelling response, got %s", cancelResponse.Body.String())
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("expected workflow stream to finish after cancellation")
	}
	run := workflowRunSnapshotByID(t, runtimeRef, runID)
	if run.Status != "cancelled" || run.CancelledAt == "" {
		t.Fatalf("expected cancelled workflow run, got %#v", run)
	}
	var requested, cancelled bool
	for _, event := range run.Events {
		if event.Type == "workflow_cancel_requested" {
			requested = true
		}
		if event.Type == "workflow_cancelled" {
			cancelled = true
		}
	}
	if !requested || !cancelled {
		t.Fatalf("expected cancellation events, got %#v", run.Events)
	}
}

func TestServerWorkflowRunCancelClearsPausedToolApproval(t *testing.T) {
	runtimeRef := newAPIWorkflowPendingApprovalRuntime(t)
	server := NewServer(runtimeRef)
	request := httptest.NewRequest(http.MethodPost, "/api/workflows/plan-fix-audit", strings.NewReader(`{"input":"write a demo file"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected workflow 200, got %d body=%s", response.Code, response.Body.String())
	}
	var first agent.WorkflowResult
	if err := json.NewDecoder(response.Body).Decode(&first); err != nil {
		t.Fatalf("decode first workflow result: %v", err)
	}
	if first.Status != "awaiting_tool_approval" || first.RunID == "" {
		t.Fatalf("expected awaiting tool approval with run id, got %#v", first)
	}
	if len(runtimeRef.SessionSnapshot().PendingApprovals) != 1 {
		t.Fatalf("expected pending approval before cancel, got %#v", runtimeRef.SessionSnapshot().PendingApprovals)
	}
	actionsRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+first.RunID+"/actions", nil)
	actionsResponse := httptest.NewRecorder()
	server.ServeHTTP(actionsResponse, actionsRequest)
	if actionsResponse.Code != http.StatusOK {
		t.Fatalf("expected workflow actions 200, got %d body=%s", actionsResponse.Code, actionsResponse.Body.String())
	}
	var actions []workflowRunAction
	if err := json.NewDecoder(actionsResponse.Body).Decode(&actions); err != nil {
		t.Fatalf("decode workflow actions: %v", err)
	}
	if !workflowActionAvailable(actions, "approve_tool", true) || !workflowActionAvailable(actions, "deny_tool", true) || !workflowActionAvailable(actions, "approve_all_tools", true) || !workflowActionAvailable(actions, "retry", true) || !workflowActionAvailable(actions, "cancel", true) {
		t.Fatalf("expected tool approval actions plus durable retry/cancel, got %#v", actions)
	}
	if action := workflowActionByName(actions, "approve_tool"); action == nil || action.Path != "/api/workflow-runs/"+first.RunID+"/approve-tool" || action.StreamPath != "/api/workflow-runs/"+first.RunID+"/approve-tool/stream" {
		t.Fatalf("expected approve action paths, got %#v", action)
	}
	if action := workflowActionByName(actions, "approve_tool"); action == nil ||
		action.Kind != "tool_approval" ||
		!action.SupportsBackground ||
		!action.SupportsStream ||
		!action.AcceptsBody ||
		action.RequiresBody ||
		action.BodySchema == nil ||
		action.Risk == nil {
		t.Fatalf("expected workflow approve_tool to advertise action protocol and risk metadata, got %#v", action)
	}
	if action := workflowActionByName(actions, "approve_all_tools"); action == nil || action.Path != "/api/workflow-runs/"+first.RunID+"/approve-tools" || action.StreamPath != "/api/workflow-runs/"+first.RunID+"/approve-tools/stream" {
		t.Fatalf("expected workflow approve-tools action paths, got %#v", action)
	}
	envelopeRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+first.RunID+"/actions?envelope=1", nil)
	envelopeResponse := httptest.NewRecorder()
	server.ServeHTTP(envelopeResponse, envelopeRequest)
	if envelopeResponse.Code != http.StatusOK {
		t.Fatalf("expected workflow action envelope 200, got %d body=%s", envelopeResponse.Code, envelopeResponse.Body.String())
	}
	var envelope workflowRunActionDiscovery
	if err := json.NewDecoder(envelopeResponse.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode workflow action envelope: %v", err)
	}
	if envelope.Meta.Schema != "goflow.run_actions" ||
		envelope.RunID != first.RunID ||
		!envelope.NeedsAction ||
		envelope.Recommended == "" ||
		envelope.Counts.Available == 0 ||
		envelope.Counts.WithRisk == 0 ||
		len(envelope.Actions) == 0 ||
		envelope.EventsPath != "/api/workflow-runs/"+first.RunID+"/events/stream" {
		t.Fatalf("expected workflow action discovery envelope metadata, got %#v", envelope)
	}

	cancelRequest := httptest.NewRequest(http.MethodPost, "/api/workflow-runs/"+first.RunID+"/cancel", nil)
	cancelResponse := httptest.NewRecorder()
	server.ServeHTTP(cancelResponse, cancelRequest)

	if cancelResponse.Code != http.StatusOK {
		t.Fatalf("expected cancel 200, got %d body=%s", cancelResponse.Code, cancelResponse.Body.String())
	}
	var cancelled session.WorkflowRunSnapshot
	if err := json.NewDecoder(cancelResponse.Body).Decode(&cancelled); err != nil {
		t.Fatalf("decode cancelled run: %v", err)
	}
	if cancelled.Status != "cancelled" || cancelled.CancelledAt == "" {
		t.Fatalf("expected cancelled paused run, got %#v", cancelled)
	}
	if cancelled.PendingCallID != "" || cancelled.PendingToolName != "" {
		t.Fatalf("expected pending fields cleared, got %#v", cancelled)
	}
	snapshot := runtimeRef.SessionSnapshot()
	if len(snapshot.PendingApprovals) != 0 {
		t.Fatalf("expected pending approvals cleared, got %#v", snapshot.PendingApprovals)
	}
	if snapshot.Workflow.Status != "cancelled" || snapshot.Workflow.PendingCallID != "" {
		t.Fatalf("expected cancelled workflow snapshot without pending call, got %#v", snapshot.Workflow)
	}
	approvalRequest := httptest.NewRequest(http.MethodPost, "/api/approvals/call-1/approve", nil)
	approvalResponse := httptest.NewRecorder()
	server.ServeHTTP(approvalResponse, approvalRequest)
	if approvalResponse.Code != http.StatusBadRequest || !strings.Contains(approvalResponse.Body.String(), "pending approval not found") {
		t.Fatalf("expected cleared approval to be unavailable, code=%d body=%s", approvalResponse.Code, approvalResponse.Body.String())
	}
}

func TestServerWorkflowRunActionsDisableApproveToolsForUnsandboxedRiskyTool(t *testing.T) {
	runtimeRef := newAPIRiskPolicyWorkflowApprovalRuntime(t)
	server := NewServer(runtimeRef)
	request := httptest.NewRequest(http.MethodPost, "/api/workflows/plan-fix-audit", strings.NewReader(`{"input":"write a demo file"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected workflow 200, got %d body=%s", response.Code, response.Body.String())
	}
	var first agent.WorkflowResult
	if err := json.NewDecoder(response.Body).Decode(&first); err != nil {
		t.Fatalf("decode first workflow result: %v", err)
	}
	if first.Status != "awaiting_tool_approval" || first.RunID == "" {
		t.Fatalf("expected awaiting tool approval with run id, got %#v", first)
	}
	run, ok := workflowRunByID(runtimeRef.SessionSnapshot().WorkflowRuns, first.RunID)
	if !ok {
		t.Fatalf("expected workflow run %s", first.RunID)
	}
	actions := server.workflowRunActions(run)
	if !workflowActionAvailable(actions, "approve_tool", true) || !workflowActionAvailable(actions, "deny_tool", true) {
		t.Fatalf("expected approve-once and deny actions to stay available, got %#v", actions)
	}
	approveTools := workflowActionByName(actions, "approve_all_tools")
	if approveTools == nil {
		t.Fatalf("expected approve_all_tools action, got %#v", actions)
	}
	if approveTools.Available || approveTools.Durable || approveTools.Background {
		t.Fatalf("expected approve_all_tools disabled by tool_risk_policy, got %#v", approveTools)
	}
	if !strings.Contains(approveTools.Reason, "tool_risk_policy") {
		t.Fatalf("expected tool_risk_policy reason, got %#v", approveTools)
	}
}

func TestServerWorkflowRunApproveToolsResumesAndScopesAutoApproval(t *testing.T) {
	runtimeRef, mcpClient := newAPIWorkflowRepeatedToolApprovalRuntime(t)
	server := NewServer(runtimeRef)
	request := httptest.NewRequest(http.MethodPost, "/api/workflows/plan-fix-audit", strings.NewReader(`{"input":"write a demo file"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected workflow 200, got %d body=%s", response.Code, response.Body.String())
	}
	var first agent.WorkflowResult
	if err := json.NewDecoder(response.Body).Decode(&first); err != nil {
		t.Fatalf("decode first workflow result: %v", err)
	}
	if first.Status != "awaiting_tool_approval" || first.RunID == "" {
		t.Fatalf("expected awaiting tool approval with run id, got %#v", first)
	}

	approveRequest := httptest.NewRequest(http.MethodPost, "/api/workflow-runs/"+first.RunID+"/approve-tools", nil)
	approveResponse := httptest.NewRecorder()
	server.ServeHTTP(approveResponse, approveRequest)

	if approveResponse.Code != http.StatusOK {
		t.Fatalf("expected approve-tools 200, got %d body=%s", approveResponse.Code, approveResponse.Body.String())
	}
	var resumed agent.WorkflowResult
	if err := json.NewDecoder(approveResponse.Body).Decode(&resumed); err != nil {
		t.Fatalf("decode resumed workflow result: %v", err)
	}
	if resumed.Status != "completed" {
		t.Fatalf("expected workflow completion after workflow-scoped approve tools, got %#v", resumed)
	}
	if mcpClient.calls != 2 {
		t.Fatalf("expected current and later matching write calls to execute once each, got %d", mcpClient.calls)
	}
	snapshot := runtimeRef.SessionSnapshot()
	if len(snapshot.PendingApprovals) != 0 {
		t.Fatalf("expected no pending approvals after scoped auto approval, got %#v", snapshot.PendingApprovals)
	}
	if snapshot.Workflow.Status != "completed" || snapshot.Workflow.PendingCallID != "" {
		t.Fatalf("expected completed workflow snapshot without pending call, got %#v", snapshot.Workflow)
	}
}

func TestServerWorkflowRunToolApprovalResumesAfterSessionLoad(t *testing.T) {
	firstState := session.New(8)
	firstRuntime := newAPITestRuntimeWithStateAndResponses(t, firstState, []schema.ChatResponse{
		{Message: schema.Message{Content: "durable plan ready"}},
		{ToolCalls: []schema.ToolCall{{ID: "call-1", Name: "write_file", Arguments: json.RawMessage(`{"path":"demo.txt","content":"hello"}`)}}},
	})
	firstServer := NewServer(firstRuntime)
	startRequest := httptest.NewRequest(http.MethodPost, "/api/workflows/plan-fix-audit", strings.NewReader(`{"input":"write a demo file"}`))
	startRequest.Header.Set("Content-Type", "application/json")
	startResponse := httptest.NewRecorder()
	firstServer.ServeHTTP(startResponse, startRequest)
	if startResponse.Code != http.StatusOK {
		t.Fatalf("expected workflow start 200, got %d body=%s", startResponse.Code, startResponse.Body.String())
	}
	var paused agent.WorkflowResult
	if err := json.NewDecoder(startResponse.Body).Decode(&paused); err != nil {
		t.Fatalf("decode paused workflow: %v", err)
	}
	if paused.Status != "awaiting_tool_approval" || paused.RunID == "" {
		t.Fatalf("expected workflow awaiting tool approval, got %#v", paused)
	}

	sessionPath := filepath.Join(t.TempDir(), "session.json")
	if err := firstState.Save(sessionPath); err != nil {
		t.Fatalf("save session: %v", err)
	}
	loadedState := session.New(8)
	if err := loadedState.Load(sessionPath); err != nil {
		t.Fatalf("load session: %v", err)
	}
	secondRuntime := newAPITestRuntimeWithStateAndResponses(t, loadedState, []schema.ChatResponse{
		{Message: schema.Message{Content: "fix after durable tool approval"}},
		{Message: schema.Message{Content: "audit after durable tool approval"}},
	})
	secondServer := NewServer(secondRuntime)

	actionsRequest := httptest.NewRequest(http.MethodGet, "/api/workflow-runs/"+paused.RunID+"/actions", nil)
	actionsResponse := httptest.NewRecorder()
	secondServer.ServeHTTP(actionsResponse, actionsRequest)
	if actionsResponse.Code != http.StatusOK {
		t.Fatalf("expected workflow actions 200 after reload, got %d body=%s", actionsResponse.Code, actionsResponse.Body.String())
	}
	var actions []workflowRunAction
	if err := json.NewDecoder(actionsResponse.Body).Decode(&actions); err != nil {
		t.Fatalf("decode workflow actions: %v", err)
	}
	if !workflowActionAvailable(actions, "approve_tool", true) || !workflowActionAvailable(actions, "deny_tool", true) || !workflowActionAvailable(actions, "approve_all_tools", true) {
		t.Fatalf("expected durable tool approval actions after reload, got %#v", actions)
	}

	approveRequest := httptest.NewRequest(http.MethodPost, "/api/workflow-runs/"+paused.RunID+"/approve-tool", nil)
	approveResponse := httptest.NewRecorder()
	secondServer.ServeHTTP(approveResponse, approveRequest)
	if approveResponse.Code != http.StatusOK {
		t.Fatalf("expected durable tool approval resume 200, got %d body=%s", approveResponse.Code, approveResponse.Body.String())
	}
	var resumed agent.WorkflowResult
	if err := json.NewDecoder(approveResponse.Body).Decode(&resumed); err != nil {
		t.Fatalf("decode resumed workflow: %v", err)
	}
	if resumed.Status != "completed" || resumed.RunID != paused.RunID || len(resumed.CompletedStages) != 3 {
		t.Fatalf("expected workflow to complete after durable tool approval, got %#v", resumed)
	}
	snapshot := secondRuntime.SessionSnapshot()
	if snapshot.Workflow.Status != "completed" || snapshot.Workflow.PendingCallID != "" || len(snapshot.PendingApprovals) != 0 {
		t.Fatalf("expected pending approval cleared after durable resume, got %#v", snapshot)
	}
}

func TestServerWorkflowRunToolApprovalResumesAfterSessionLoadWithExternalizedLargeArguments(t *testing.T) {
	root := t.TempDir()
	store := session.NewArtifactObjectStore(root)
	if err := store.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	content := strings.Repeat("externalized workflow approval payload ", 420)
	args := `{"path":"demo.txt","content":"` + content + `"}`

	firstState := session.New(8)
	firstState.SetArtifactObjectStore(store)
	firstRuntime := newAPITestRuntimeWithStateAndResponses(t, firstState, []schema.ChatResponse{
		{Message: schema.Message{Content: "durable plan ready"}},
		{ToolCalls: []schema.ToolCall{{ID: "call-1", Name: "write_file", Arguments: json.RawMessage(args)}}},
	})
	firstServer := NewServer(firstRuntime)
	startRequest := httptest.NewRequest(http.MethodPost, "/api/workflows/plan-fix-audit", strings.NewReader(`{"input":"write a large demo file"}`))
	startRequest.Header.Set("Content-Type", "application/json")
	startResponse := httptest.NewRecorder()
	firstServer.ServeHTTP(startResponse, startRequest)
	if startResponse.Code != http.StatusOK {
		t.Fatalf("expected workflow start 200, got %d body=%s", startResponse.Code, startResponse.Body.String())
	}
	var paused agent.WorkflowResult
	if err := json.NewDecoder(startResponse.Body).Decode(&paused); err != nil {
		t.Fatalf("decode paused workflow: %v", err)
	}
	if paused.Status != "awaiting_tool_approval" || paused.RunID == "" {
		t.Fatalf("expected workflow awaiting tool approval, got %#v", paused)
	}
	workflowBeforeSave := workflowRunSnapshotByID(t, firstRuntime, paused.RunID)
	if workflowBeforeSave.PendingArgs != "" || workflowBeforeSave.PendingArgsArtifactRef == "" || workflowBeforeSave.PendingArgsHash == "" || !workflowBeforeSave.PendingArgsExternalized {
		t.Fatalf("expected workflow run pending args to externalize before save, got %#v", workflowBeforeSave)
	}
	if firstState.Snapshot().Workflow.PendingArguments != "" || firstState.Snapshot().Workflow.PendingArgumentsArtifactRef == "" {
		t.Fatalf("expected top-level workflow pending args to externalize, got %#v", firstState.Snapshot().Workflow)
	}

	sessionPath := filepath.Join(root, "session.json")
	if err := firstState.Save(sessionPath); err != nil {
		t.Fatalf("save session: %v", err)
	}
	loadedState := session.New(8)
	loadedState.SetArtifactObjectStore(store)
	if err := loadedState.Load(sessionPath); err != nil {
		t.Fatalf("load session: %v", err)
	}
	loadedRun, ok := workflowRunByID(loadedState.Snapshot().WorkflowRuns, paused.RunID)
	if !ok || loadedRun.PendingArgs != "" || loadedRun.PendingArgsArtifactRef == "" {
		t.Fatalf("expected loaded workflow run to keep externalized pending args metadata, got %#v ok=%t", loadedRun, ok)
	}

	secondRuntime := newAPITestRuntimeWithStateAndResponses(t, loadedState, []schema.ChatResponse{
		{Message: schema.Message{Content: "fix after large durable tool approval"}},
		{Message: schema.Message{Content: "audit after large durable tool approval"}},
	})
	secondServer := NewServer(secondRuntime)
	approveRequest := httptest.NewRequest(http.MethodPost, "/api/workflow-runs/"+paused.RunID+"/approve-tool", nil)
	approveResponse := httptest.NewRecorder()
	secondServer.ServeHTTP(approveResponse, approveRequest)
	if approveResponse.Code != http.StatusOK {
		t.Fatalf("expected durable workflow tool approval resume 200, got %d body=%s", approveResponse.Code, approveResponse.Body.String())
	}
	var resumed agent.WorkflowResult
	if err := json.NewDecoder(approveResponse.Body).Decode(&resumed); err != nil {
		t.Fatalf("decode resumed workflow: %v", err)
	}
	if resumed.Status != "completed" || resumed.RunID != paused.RunID || len(resumed.CompletedStages) != 3 {
		t.Fatalf("expected workflow to complete after large durable tool approval, got %#v", resumed)
	}
	snapshot := secondRuntime.SessionSnapshot()
	if snapshot.Workflow.Status != "completed" || snapshot.Workflow.PendingCallID != "" || len(snapshot.PendingApprovals) != 0 {
		t.Fatalf("expected pending approval cleared after large durable resume, got %#v", snapshot)
	}
}

func TestServerWorkflowRunRetryCreatesLinkedAttempt(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	server := NewServer(runtimeRef)
	request := httptest.NewRequest(http.MethodPost, "/api/workflows/plan-fix-audit", strings.NewReader(`{"input":"ship it"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected workflow 200, got %d body=%s", response.Code, response.Body.String())
	}
	var first agent.WorkflowResult
	if err := json.NewDecoder(response.Body).Decode(&first); err != nil {
		t.Fatalf("decode first workflow result: %v", err)
	}
	if first.RunID == "" {
		t.Fatalf("expected first run id, got %#v", first)
	}

	retryRequest := httptest.NewRequest(http.MethodPost, "/api/workflow-runs/"+first.RunID+"/retry", nil)
	retryResponse := httptest.NewRecorder()
	server.ServeHTTP(retryResponse, retryRequest)
	if retryResponse.Code != http.StatusOK {
		t.Fatalf("expected retry 200, got %d body=%s", retryResponse.Code, retryResponse.Body.String())
	}
	var retried agent.WorkflowResult
	if err := json.NewDecoder(retryResponse.Body).Decode(&retried); err != nil {
		t.Fatalf("decode retry workflow result: %v", err)
	}
	if retried.RunID == "" || retried.RunID == first.RunID {
		t.Fatalf("expected new retry run id, first=%#v retry=%#v", first, retried)
	}
	run := workflowRunSnapshotByID(t, runtimeRef, retried.RunID)
	if run.RetryOf != first.RunID || run.Attempt != 2 || run.Status != "completed" {
		t.Fatalf("expected linked retry attempt, got %#v", run)
	}
	var retryStarted bool
	for _, event := range run.Events {
		if event.Type == "workflow_started" && strings.Contains(event.Content, first.RunID) {
			retryStarted = true
			break
		}
	}
	if !retryStarted {
		t.Fatalf("expected retry start event to reference first run, got %#v", run.Events)
	}
}

func TestServerWorkflowRunActionsMarkMissingToolApprovalContext(t *testing.T) {
	server := NewServer(newAPITestRuntime(t))
	actions := server.workflowRunActions(session.WorkflowRunSnapshot{
		ID:            "wf-demo",
		Name:          "demo-flow",
		Status:        "awaiting_tool_approval",
		Request:       "write a file",
		PendingCallID: "call-missing",
	})
	approve := workflowActionByName(actions, "approve_tool")
	if approve == nil || approve.Available || approve.Durable || !strings.Contains(approve.Reason, "not available") {
		t.Fatalf("expected unavailable non-durable approve action, got %#v", approve)
	}
	if !workflowActionAvailable(actions, "retry", true) || !workflowActionAvailable(actions, "cancel", true) {
		t.Fatalf("expected durable retry and cancel actions, got %#v", actions)
	}
}

func waitWorkflowRunID(t *testing.T, runtimeRef *agent.Runtime) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		runID := runtimeRef.SessionSnapshot().Workflow.RunID
		if strings.TrimSpace(runID) != "" {
			return runID
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expected workflow run id")
	return ""
}

func workflowRunSnapshotByID(t *testing.T, runtimeRef *agent.Runtime, runID string) session.WorkflowRunSnapshot {
	t.Helper()
	for _, run := range runtimeRef.SessionSnapshot().WorkflowRuns {
		if run.ID == runID {
			return run
		}
	}
	t.Fatalf("workflow run %s not found in %#v", runID, runtimeRef.SessionSnapshot().WorkflowRuns)
	return session.WorkflowRunSnapshot{}
}

func agentRunSnapshotByID(t *testing.T, runtimeRef *agent.Runtime, runID string) session.AgentRunSnapshot {
	t.Helper()
	for _, run := range runtimeRef.SessionSnapshot().AgentRuns {
		if run.ID == runID {
			return run
		}
	}
	t.Fatalf("agent run %s not found in %#v", runID, runtimeRef.SessionSnapshot().AgentRuns)
	return session.AgentRunSnapshot{}
}

func waitWorkflowRunStatus(t *testing.T, runtimeRef *agent.Runtime, runID, status string) session.WorkflowRunSnapshot {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		run := workflowRunSnapshotByID(t, runtimeRef, runID)
		if strings.EqualFold(run.Status, status) {
			return run
		}
		time.Sleep(10 * time.Millisecond)
	}
	run := workflowRunSnapshotByID(t, runtimeRef, runID)
	t.Fatalf("expected workflow run %s status %q, got %#v", runID, status, run)
	return session.WorkflowRunSnapshot{}
}

func waitAgentRunStatus(t *testing.T, runtimeRef *agent.Runtime, runID, status string) session.AgentRunSnapshot {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		run := agentRunSnapshotByID(t, runtimeRef, runID)
		if strings.EqualFold(run.Status, status) {
			return run
		}
		time.Sleep(10 * time.Millisecond)
	}
	run := agentRunSnapshotByID(t, runtimeRef, runID)
	t.Fatalf("expected agent run %s status %q, got %#v", runID, status, run)
	return session.AgentRunSnapshot{}
}

func waitAgentRunActionAvailable(t *testing.T, server *Server, runtimeRef *agent.Runtime, runID, action string, durable bool) session.AgentRunSnapshot {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		run := agentRunSnapshotByID(t, runtimeRef, runID)
		if agentRunActionAvailable(server.agentRunActions(run), action, durable) {
			return run
		}
		time.Sleep(10 * time.Millisecond)
	}
	run := agentRunSnapshotByID(t, runtimeRef, runID)
	t.Fatalf("expected agent run %s action %q to be available, run=%#v actions=%#v", runID, action, run, server.agentRunActions(run))
	return session.AgentRunSnapshot{}
}

func workflowActionByName(actions []workflowRunAction, name string) *workflowRunAction {
	for i := range actions {
		if actions[i].Name == name {
			return &actions[i]
		}
	}
	return nil
}

func workflowActionAvailable(actions []workflowRunAction, name string, durable bool) bool {
	action := workflowActionByName(actions, name)
	return action != nil && action.Available && action.Durable == durable
}

func workflowNavigationItemByStage(items []workflowRunStageNavigationItem, stage string) *workflowRunStageNavigationItem {
	for i := range items {
		if workflowRunStageEqual(items[i].Stage, stage) {
			return &items[i]
		}
	}
	return nil
}

func agentRunTimelineContainsKind(items []agentRunTimelineItem, kind string) bool {
	for _, item := range items {
		if item.Kind == kind {
			return true
		}
	}
	return false
}

func agentRunActionAvailable(actions []agentRunAction, name string, durable bool) bool {
	for _, action := range actions {
		if action.Name == name {
			return action.Available && action.Durable == durable
		}
	}
	return false
}

func agentRunActionByName(actions []agentRunAction, name string) *agentRunAction {
	for i := range actions {
		if actions[i].Name == name {
			return &actions[i]
		}
	}
	return nil
}

func workflowStringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func assertFileContains(t *testing.T, path, needle string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !strings.Contains(string(data), needle) {
		t.Fatalf("expected %s to contain %q; got:\n%s", path, needle, string(data))
	}
}

func runCollectionAvailableActionExists(items []runCollectionAvailableAction, name, path string, requiresBody bool) bool {
	for _, item := range items {
		if item.Name == name && item.Path == path && item.Method == http.MethodPost && item.RequiresBody == requiresBody {
			return true
		}
	}
	return false
}

func testWriteDiffToolContent(t *testing.T) string {
	t.Helper()
	payload := map[string]any{
		"path":           "src/app.py",
		"relative_path":  "src/app.py",
		"status":         "modified",
		"line_summary":   "changed old lines 1-2 -> new lines 1-3 (+2 -1)",
		"old_range":      "1-2",
		"new_range":      "1-3",
		"added_lines":    2,
		"deleted_lines":  1,
		"bytes_written":  42,
		"old_line_count": 2,
		"new_line_count": 3,
		"diff_preview":   "@@ -1,2 +1,3 @@\n- old\n+ new\n+ more",
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal test write diff payload: %v", err)
	}
	return string(data)
}

type sseTestEvent struct {
	Name    string
	Payload schema.StreamEvent
}

func decodeSSEEvents(t *testing.T, body string) []sseTestEvent {
	t.Helper()
	blocks := strings.Split(strings.TrimSpace(body), "\n\n")
	events := make([]sseTestEvent, 0, len(blocks))
	for _, block := range blocks {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		var name, data string
		for _, line := range strings.Split(block, "\n") {
			line = strings.TrimRight(line, "\r")
			switch {
			case strings.HasPrefix(line, "event:"):
				name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				data = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			}
		}
		if name == "" || data == "" {
			t.Fatalf("invalid SSE block %q in body %q", block, body)
		}
		var payload schema.StreamEvent
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			t.Fatalf("decode SSE payload %q: %v", data, err)
		}
		events = append(events, sseTestEvent{Name: name, Payload: payload})
	}
	if len(events) == 0 {
		t.Fatalf("expected SSE events, got body %q", body)
	}
	return events
}

func sseEventsContain(events []sseTestEvent, eventType schema.StreamEventType) bool {
	for _, event := range events {
		if event.Payload.Type == eventType {
			return true
		}
	}
	return false
}

func testStringSliceContains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
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
