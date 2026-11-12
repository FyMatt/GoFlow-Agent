package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/nativesandbox"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

type stubApprovalMCP struct {
	result schema.ToolResult
	calls  int
	name   string
	args   []byte
}

func rememberExecutionApproval(t *testing.T, execCtx *ExecutionContext, call schema.ToolCall, tool schema.Tool) session.ApprovalScope {
	t.Helper()
	if execCtx.Session == nil {
		execCtx.Session = session.New(4)
	}
	now := time.Now().UTC()
	scope, err := execCtx.approvalScope(call, tool, now, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("approvalScope: %v", err)
	}
	execCtx.Session.RememberApprovalScope(scope)
	return scope
}

func (s *stubApprovalMCP) ListTools(context.Context) ([]schema.Tool, error) {
	return nil, nil
}

func (s *stubApprovalMCP) CallTool(_ context.Context, name string, arguments []byte) (schema.ToolResult, error) {
	s.calls++
	s.name = name
	s.args = append([]byte(nil), arguments...)
	return s.result, nil
}

func (s *stubApprovalMCP) RefreshTools(context.Context) ([]schema.Tool, error) {
	return nil, nil
}

func (s *stubApprovalMCP) HealthStatus(context.Context) map[string]string {
	return map[string]string{"stub": "ready"}
}

func (s *stubApprovalMCP) ToolNames() []string {
	return nil
}

func TestExecutorQueuesApprovalInsteadOfExecutingImmediately(t *testing.T) {
	mcp := &stubApprovalMCP{}
	executor := NewExecutor(mcp)
	runtimeRef := &Runtime{approvals: newApprovalStore()}
	execCtx := newExecutionContext("fixer", config.AgentProfile{
		ToolPolicy:       config.ToolPolicyConfirm,
		AllowedToolKinds: []config.ToolKind{config.ToolKindWrite},
	}, []schema.Tool{{
		Name:        "write_file",
		Kind:        "write",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`),
	}}, nil, "")

	results, err := executor.RunToolCalls(context.Background(), execCtx, []schema.ToolCall{{
		ID:        "call-1",
		Name:      "write_file",
		Arguments: json.RawMessage(`{"path":"a.txt","content":"hello"}`),
	}}, runtimeRef, nil)
	if err != nil {
		t.Fatalf("RunToolCalls: %v", err)
	}
	if mcp.calls != 0 {
		t.Fatalf("expected no immediate MCP execution, got %d", mcp.calls)
	}
	if len(results) != 1 || !results[0].Suspended {
		t.Fatalf("expected pending approval result, got %#v", results)
	}
	pending := runtimeRef.PendingApprovals()
	if len(pending) != 1 || pending[0].ID != "call-1" {
		t.Fatalf("unexpected pending approvals: %#v", pending)
	}
}

func TestExecutorExecutesReadToolsImmediatelyUnderConfirmPolicy(t *testing.T) {
	mcp := &stubApprovalMCP{result: schema.ToolResult{Content: "contents"}}
	executor := NewExecutor(mcp)
	runtimeRef := &Runtime{approvals: newApprovalStore()}
	execCtx := newExecutionContext("fixer", config.AgentProfile{
		ToolPolicy:       config.ToolPolicyConfirm,
		AllowedToolKinds: []config.ToolKind{config.ToolKindRead, config.ToolKindWrite},
	}, []schema.Tool{{
		Name:        "list_dir",
		Kind:        "read",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`),
	}}, nil, "")

	results, err := executor.RunToolCalls(context.Background(), execCtx, []schema.ToolCall{{
		ID:        "call-read",
		Name:      "list_dir",
		Arguments: json.RawMessage(`{"path":"."}`),
	}}, runtimeRef, nil)
	if err != nil {
		t.Fatalf("RunToolCalls: %v", err)
	}
	if mcp.calls != 1 {
		t.Fatalf("expected immediate MCP execution for read tool, got %d", mcp.calls)
	}
	if len(results) != 1 || results[0].Denied || results[0].IsError {
		t.Fatalf("expected successful read result, got %#v", results)
	}
	if len(runtimeRef.PendingApprovals()) != 0 {
		t.Fatalf("expected no pending approvals for read tool")
	}
}

func TestExecutorRejectsUnknownLocalCommandWithoutStrongNativeSandbox(t *testing.T) {
	mcp := &stubApprovalMCP{result: schema.ToolResult{Content: "should not run"}}
	tool := commandApprovalTool()
	report := nativesandbox.Report{StrongReady: false, RefusalReason: "test sandbox is incomplete"}
	execCtx := newExecutionContext("fixer", config.AgentProfile{
		ToolPolicy:       config.ToolPolicyAllow,
		AllowedToolKinds: []config.ToolKind{config.ToolKindExec},
	}, []schema.Tool{tool}, nil, t.TempDir())
	execCtx.NativeSandboxReport = &report

	results, err := NewExecutor(mcp).RunToolCalls(context.Background(), execCtx, []schema.ToolCall{
		commandToolCall(t, "custom-tool", []string{"run"}),
	}, &Runtime{approvals: newApprovalStore()}, nil)
	if err != nil {
		t.Fatalf("RunToolCalls: %v", err)
	}
	if mcp.calls != 0 {
		t.Fatalf("unknown command must not reach MCP without a strong native sandbox, got %d calls", mcp.calls)
	}
	if len(results) != 1 || !results[0].Denied || !results[0].IsError || results[0].Suspended {
		t.Fatalf("expected a terminal policy denial, got %#v", results)
	}
	if !strings.Contains(results[0].Content, "原生执行策略拒绝") {
		t.Fatalf("expected native sandbox refusal detail, got %q", results[0].Content)
	}
}

func TestExecutorAllowsClassifiedVerificationWithoutStrongNativeSandbox(t *testing.T) {
	mcp := &stubApprovalMCP{result: schema.ToolResult{Content: "ok"}}
	tool := commandApprovalTool()
	report := nativesandbox.Report{StrongReady: false, RefusalReason: "test sandbox is incomplete"}
	execCtx := newExecutionContext("fixer", config.AgentProfile{
		ToolPolicy:       config.ToolPolicyAllow,
		AllowedToolKinds: []config.ToolKind{config.ToolKindExec},
	}, []schema.Tool{tool}, nil, t.TempDir())
	execCtx.NativeSandboxReport = &report

	results, err := NewExecutor(mcp).RunToolCalls(context.Background(), execCtx, []schema.ToolCall{
		commandToolCall(t, "go", []string{"test", "./internal/..."}),
	}, &Runtime{approvals: newApprovalStore()}, nil)
	if err != nil {
		t.Fatalf("RunToolCalls: %v", err)
	}
	if mcp.calls != 1 {
		t.Fatalf("classified verification command should execute, got %d calls", mcp.calls)
	}
	if len(results) != 1 || results[0].Denied || results[0].IsError || results[0].Suspended {
		t.Fatalf("expected successful verification result, got %#v", results)
	}
}

func TestApprovedUnknownCommandRemainsBlockedWithoutStrongNativeSandbox(t *testing.T) {
	mcp := &stubApprovalMCP{result: schema.ToolResult{Content: "should not run"}}
	tool := commandApprovalTool()
	call := commandToolCall(t, "custom-tool", []string{"run"})
	runtimeRef := &Runtime{
		cfg:       &config.Config{WorkspaceRoot: t.TempDir()},
		mcp:       mcp,
		session:   session.New(4),
		approvals: newApprovalStore(),
		nativeSandboxProbe: func() nativesandbox.Report {
			return nativesandbox.Report{StrongReady: false, RefusalReason: "test sandbox is incomplete"}
		},
	}
	runtimeRef.queueApproval(call, tool, "fixer")

	if ok, reason := runtimeRef.CanApprovePendingToolApproval(call.ID); ok || !strings.Contains(reason, "原生执行策略拒绝") {
		t.Fatalf("expected approval UI to report the native sandbox refusal, ok=%v reason=%q", ok, reason)
	}
	if _, err := runtimeRef.ApproveToolCall(context.Background(), call.ID); err == nil || !strings.Contains(err.Error(), "原生执行策略拒绝") {
		t.Fatalf("expected approved unknown command to remain blocked, got %v", err)
	}
	if mcp.calls != 0 {
		t.Fatalf("approval must not bypass the native sandbox policy, got %d MCP calls", mcp.calls)
	}
	if _, ok := runtimeRef.PendingApproval(call.ID); !ok {
		t.Fatal("blocked approval should remain queued so the task can recover after policy or environment changes")
	}
}

func commandApprovalTool() schema.Tool {
	return schema.Tool{
		Name:        "run_command",
		Server:      "command_tools",
		Kind:        string(config.ToolKindExec),
		InputSchema: json.RawMessage(`{"type":"object","properties":{"program":{"type":"string"},"args":{"type":"array","items":{"type":"string"}}},"required":["program"],"additionalProperties":false}`),
	}
}

func TestExecutorSkipsRepeatedApprovalForRememberedWorkspaceTool(t *testing.T) {
	mcp := &stubApprovalMCP{result: schema.ToolResult{Content: "ok"}}
	executor := NewExecutor(mcp)
	runtimeRef := &Runtime{approvals: newApprovalStore()}
	execCtx := newExecutionContext("fixer", config.AgentProfile{
		ToolPolicy:       config.ToolPolicyConfirm,
		AllowedToolKinds: []config.ToolKind{config.ToolKindWrite},
	}, []schema.Tool{{
		Name:        "write_file",
		Kind:        "write",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`),
	}}, nil, "C:/repo")
	call := schema.ToolCall{
		ID:        "call-remembered",
		Name:      "write_file",
		Arguments: json.RawMessage(`{"path":"a.txt","content":"hello again"}`),
	}
	rememberExecutionApproval(t, &execCtx, call, execCtx.Tools[0])

	results, err := executor.RunToolCalls(context.Background(), execCtx, []schema.ToolCall{call}, runtimeRef, nil)
	if err != nil {
		t.Fatalf("RunToolCalls: %v", err)
	}
	if mcp.calls != 1 {
		t.Fatalf("expected remembered approval to execute immediately, got %d calls", mcp.calls)
	}
	if len(results) != 1 || results[0].Suspended {
		t.Fatalf("expected non-suspended result, got %#v", results)
	}
	if len(runtimeRef.PendingApprovals()) != 0 {
		t.Fatalf("expected no pending approvals for remembered tool, got %#v", runtimeRef.PendingApprovals())
	}
}

func TestManagedHostChangesAlwaysRequireOneTimeApproval(t *testing.T) {
	mcp := &stubApprovalMCP{result: schema.ToolResult{Content: "ok"}}
	executor := NewExecutor(mcp)
	runtimeRef := &Runtime{approvals: newApprovalStore()}
	tool := schema.Tool{
		Name: "set_managed_json_value", Server: "host_tools", Kind: "write",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`),
	}
	execCtx := newExecutionContext("operations-specialist", config.AgentProfile{
		ToolPolicy: config.ToolPolicyAllow, AllowedToolKinds: []config.ToolKind{config.ToolKindWrite},
	}, []schema.Tool{tool}, nil, "C:/repo")
	call := schema.ToolCall{ID: "managed-change", Name: "host_tools/set_managed_json_value", Arguments: json.RawMessage(`{"path":"C:/managed/app.json"}`)}
	rememberExecutionApproval(t, &execCtx, call, tool)
	if execCtx.canRememberCallApproval(call, tool) {
		t.Fatal("managed host change approval must not be rememberable")
	}

	results, err := executor.RunToolCalls(context.Background(), execCtx, []schema.ToolCall{call}, runtimeRef, nil)
	if err != nil {
		t.Fatalf("RunToolCalls: %v", err)
	}
	if mcp.calls != 0 || len(results) != 1 || !results[0].Suspended {
		t.Fatalf("remembered scope bypassed one-time managed change approval: calls=%d results=%#v", mcp.calls, results)
	}
	if len(runtimeRef.PendingApprovals()) != 1 {
		t.Fatalf("expected one pending managed change approval, got %#v", runtimeRef.PendingApprovals())
	}
	restoreTool := schema.Tool{Name: "restore_managed_json_change", Server: "host_tools", Kind: "write"}
	restoreCall := schema.ToolCall{ID: "managed-restore", Name: "host_tools/restore_managed_json_change", Arguments: json.RawMessage(`{"change_id":"0123456789abcdef01234567"}`)}
	if !execCtx.requiresApproval(restoreCall, restoreTool) || execCtx.canRememberCallApproval(restoreCall, restoreTool) {
		t.Fatal("managed recovery must require a non-rememberable one-time approval")
	}
}

func TestExecutorRejectsToolWhenKindAllowedButToolNotInAllowlist(t *testing.T) {
	mcp := &stubApprovalMCP{result: schema.ToolResult{Content: "ok"}}
	executor := NewExecutor(mcp)
	runtimeRef := &Runtime{approvals: newApprovalStore()}
	execCtx := newExecutionContext("fixer", config.AgentProfile{
		ToolPolicy:       config.ToolPolicyConfirm,
		AllowedToolKinds: []config.ToolKind{config.ToolKindWrite},
		AllowedTools:     []string{"edit_file"},
	}, []schema.Tool{{
		Name:        "write_file",
		Kind:        "write",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`),
	}}, nil, "C:/repo")

	results, err := executor.RunToolCalls(context.Background(), execCtx, []schema.ToolCall{{
		ID:        "call-allowlist-miss",
		Name:      "write_file",
		Arguments: json.RawMessage(`{"path":"a.txt","content":"hello"}`),
	}}, runtimeRef, nil)
	if err != nil {
		t.Fatalf("expected allowlist rejection as tool result, got %v", err)
	}
	if mcp.calls != 0 {
		t.Fatalf("expected no MCP call, got %d", mcp.calls)
	}
	if len(results) != 1 || !results[0].Denied || !results[0].IsError {
		t.Fatalf("expected denied validation result, got %#v", results)
	}
	if !strings.Contains(results[0].Content, "tool write_file is not allowed; allowed tools: edit_file") {
		t.Fatalf("unexpected validation result: %#v", results[0])
	}
}

func TestExecutorAllowsToolWhenKindAndAllowlistBothMatch(t *testing.T) {
	mcp := &stubApprovalMCP{result: schema.ToolResult{Content: "ok"}}
	executor := NewExecutor(mcp)
	runtimeRef := &Runtime{approvals: newApprovalStore()}
	execCtx := newExecutionContext("fixer", config.AgentProfile{
		ToolPolicy:       config.ToolPolicyAllow,
		AllowedToolKinds: []config.ToolKind{config.ToolKindWrite},
		AllowedTools:     []string{"write_file"},
	}, []schema.Tool{{
		Name:        "write_file",
		Kind:        "write",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`),
	}}, nil, "C:/repo")

	results, err := executor.RunToolCalls(context.Background(), execCtx, []schema.ToolCall{{
		ID:        "call-allowlist-hit",
		Name:      "write_file",
		Arguments: json.RawMessage(`{"path":"a.txt","content":"hello"}`),
	}}, runtimeRef, nil)
	if err != nil {
		t.Fatalf("RunToolCalls: %v", err)
	}
	if mcp.calls != 1 {
		t.Fatalf("expected immediate MCP call, got %d", mcp.calls)
	}
	if len(results) != 1 || results[0].Denied || results[0].IsError {
		t.Fatalf("expected successful result, got %#v", results)
	}
}

func TestRiskPolicyRequiresApprovalForUnsandboxedRiskyToolUnderAllowPolicy(t *testing.T) {
	mcp := &stubApprovalMCP{result: schema.ToolResult{Content: "ok"}}
	executor := NewExecutor(mcp)
	runtimeRef := &Runtime{approvals: newApprovalStore()}
	execCtx := newExecutionContext("fixer", config.AgentProfile{
		ToolPolicy:       config.ToolPolicyAllow,
		AllowedToolKinds: []config.ToolKind{config.ToolKindWrite},
	}, []schema.Tool{{
		Name:        "write_file",
		Server:      "stub",
		Kind:        "write",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`),
	}}, nil, "C:/repo")
	execCtx.RiskPolicy = config.ToolRiskPolicyConfig{RequireApprovalForUnsandboxedRiskyTools: true}
	execCtx.MCPServers = []config.MCPServerRef{{Name: "stub", Isolation: "process_group"}}

	results, err := executor.RunToolCalls(context.Background(), execCtx, []schema.ToolCall{{
		ID:        "call-risk",
		Name:      "write_file",
		Arguments: json.RawMessage(`{"path":"a.txt","content":"hello"}`),
	}}, runtimeRef, nil)
	if err != nil {
		t.Fatalf("RunToolCalls: %v", err)
	}
	if mcp.calls != 0 {
		t.Fatalf("expected no immediate MCP call for unsandboxed risky tool, got %d", mcp.calls)
	}
	if len(results) != 1 || !results[0].Suspended {
		t.Fatalf("expected suspended approval result, got %#v", results)
	}
	pending := runtimeRef.PendingApprovals()
	if len(pending) != 1 || pending[0].ID != "call-risk" {
		t.Fatalf("expected pending approval for risky tool, got %#v", pending)
	}
}

func TestRiskPolicyRejectsUnsandboxedRiskyToolBeforeApproval(t *testing.T) {
	mcp := &stubApprovalMCP{result: schema.ToolResult{Content: "ok"}}
	executor := NewExecutor(mcp)
	runtimeRef := &Runtime{approvals: newApprovalStore()}
	execCtx := newExecutionContext("fixer", config.AgentProfile{
		ToolPolicy:       config.ToolPolicyAllow,
		AllowedToolKinds: []config.ToolKind{config.ToolKindWrite},
	}, []schema.Tool{{
		Name:        "write_file",
		Server:      "stub",
		Kind:        "write",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`),
	}}, nil, "C:/repo")
	execCtx.RiskPolicy = config.ToolRiskPolicyConfig{RejectUnsandboxedRiskyTools: true}
	execCtx.MCPServers = []config.MCPServerRef{{Name: "stub", Isolation: "process_group"}}

	results, err := executor.RunToolCalls(context.Background(), execCtx, []schema.ToolCall{{
		ID:        "call-risk-reject",
		Name:      "write_file",
		Arguments: json.RawMessage(`{"path":"a.txt","content":"hello"}`),
	}}, runtimeRef, nil)
	if err != nil {
		t.Fatalf("RunToolCalls: %v", err)
	}
	if mcp.calls != 0 {
		t.Fatalf("expected rejected risky tool not to call MCP, got %d", mcp.calls)
	}
	if len(results) != 1 || !results[0].Denied || !results[0].IsError || results[0].Suspended {
		t.Fatalf("expected denied non-suspended risk-policy result, got %#v", results)
	}
	if !strings.Contains(results[0].Content, "reject_unsandboxed_risky_tools") {
		t.Fatalf("expected risk-policy rejection detail, got %#v", results[0])
	}
	if len(runtimeRef.PendingApprovals()) != 0 {
		t.Fatalf("expected no pending approval for rejected tool, got %#v", runtimeRef.PendingApprovals())
	}
}

func TestRiskPolicyDoesNotForceApprovalForNativeStrongRiskyTool(t *testing.T) {
	mcp := &stubApprovalMCP{result: schema.ToolResult{Content: "ok"}}
	executor := NewExecutor(mcp)
	runtimeRef := &Runtime{approvals: newApprovalStore()}
	execCtx := newExecutionContext("fixer", config.AgentProfile{
		ToolPolicy:       config.ToolPolicyAllow,
		AllowedToolKinds: []config.ToolKind{config.ToolKindWrite},
	}, []schema.Tool{{
		Name:        "write_file",
		Server:      "stub",
		Kind:        "write",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`),
	}}, nil, "C:/repo")
	execCtx.RiskPolicy = config.ToolRiskPolicyConfig{RequireApprovalForUnsandboxedRiskyTools: true}
	execCtx.MCPServers = []config.MCPServerRef{{Name: "stub", Isolation: "native_strong"}}

	results, err := executor.RunToolCalls(context.Background(), execCtx, []schema.ToolCall{{
		ID:        "call-native-strong",
		Name:      "write_file",
		Arguments: json.RawMessage(`{"path":"a.txt","content":"hello"}`),
	}}, runtimeRef, nil)
	if err != nil {
		t.Fatalf("RunToolCalls: %v", err)
	}
	if mcp.calls != 1 {
		t.Fatalf("expected immediate MCP call for native-strong risky tool under allow policy, got %d", mcp.calls)
	}
	if len(results) != 1 || results[0].Suspended || results[0].IsError {
		t.Fatalf("expected successful direct result, got %#v", results)
	}
	if len(runtimeRef.PendingApprovals()) != 0 {
		t.Fatalf("expected no pending approval, got %#v", runtimeRef.PendingApprovals())
	}
}

func TestRiskPolicyDoesNotRejectNativeStrongRiskyTool(t *testing.T) {
	mcp := &stubApprovalMCP{result: schema.ToolResult{Content: "ok"}}
	executor := NewExecutor(mcp)
	runtimeRef := &Runtime{approvals: newApprovalStore()}
	execCtx := newExecutionContext("fixer", config.AgentProfile{
		ToolPolicy:       config.ToolPolicyAllow,
		AllowedToolKinds: []config.ToolKind{config.ToolKindWrite},
	}, []schema.Tool{{
		Name:        "write_file",
		Server:      "stub",
		Kind:        "write",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`),
	}}, nil, "C:/repo")
	execCtx.RiskPolicy = config.ToolRiskPolicyConfig{RejectUnsandboxedRiskyTools: true}
	execCtx.MCPServers = []config.MCPServerRef{{Name: "stub", Isolation: "native_strong"}}

	results, err := executor.RunToolCalls(context.Background(), execCtx, []schema.ToolCall{{
		ID:        "call-native-strong-risk",
		Name:      "write_file",
		Arguments: json.RawMessage(`{"path":"a.txt","content":"hello"}`),
	}}, runtimeRef, nil)
	if err != nil {
		t.Fatalf("RunToolCalls: %v", err)
	}
	if mcp.calls != 1 {
		t.Fatalf("expected native-strong risky tool to execute, got %d calls", mcp.calls)
	}
	if len(results) != 1 || results[0].Denied || results[0].IsError || results[0].Suspended {
		t.Fatalf("expected successful direct result, got %#v", results)
	}
}

func TestRiskPolicyAllowsLinuxNetworkNamespaceForNetworkToolOnly(t *testing.T) {
	mcp := &stubApprovalMCP{result: schema.ToolResult{Content: "ok"}}
	executor := NewExecutor(mcp)
	runtimeRef := &Runtime{approvals: newApprovalStore()}
	execCtx := newExecutionContext("auditor", config.AgentProfile{
		ToolPolicy:       config.ToolPolicyAllow,
		AllowedToolKinds: []config.ToolKind{config.ToolKindNetwork, config.ToolKindWrite},
	}, []schema.Tool{
		{
			Name:        "web_search",
			Server:      "stub",
			Kind:        "network",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}`),
		},
		{
			Name:        "write_file",
			Server:      "stub",
			Kind:        "write",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`),
		},
	}, nil, "C:/repo")
	execCtx.RiskPolicy = config.ToolRiskPolicyConfig{RejectUnsandboxedRiskyTools: true}
	execCtx.MCPServers = []config.MCPServerRef{{Name: "stub", Isolation: "linux_netns"}}

	results, err := executor.RunToolCalls(context.Background(), execCtx, []schema.ToolCall{{
		ID:        "call-netns-network",
		Name:      "web_search",
		Arguments: json.RawMessage(`{"query":"goflow"}`),
	}}, runtimeRef, nil)
	if err != nil {
		t.Fatalf("RunToolCalls network: %v", err)
	}
	if mcp.calls != 1 || len(results) != 1 || results[0].Denied || results[0].IsError {
		t.Fatalf("expected linux_netns network tool to execute, calls=%d results=%#v", mcp.calls, results)
	}

	results, err = executor.RunToolCalls(context.Background(), execCtx, []schema.ToolCall{{
		ID:        "call-netns-write",
		Name:      "write_file",
		Arguments: json.RawMessage(`{"path":"a.txt","content":"hello"}`),
	}}, runtimeRef, nil)
	if err != nil {
		t.Fatalf("RunToolCalls write: %v", err)
	}
	if mcp.calls != 1 {
		t.Fatalf("expected linux_netns write tool to be rejected before MCP call, got calls=%d", mcp.calls)
	}
	if len(results) != 1 || !results[0].Denied || !results[0].IsError || !strings.Contains(results[0].Content, "reject_unsandboxed_risky_tools") {
		t.Fatalf("expected linux_netns write tool risk rejection, got %#v", results)
	}
}

func TestExecutorRememberedApprovalDoesNotBypassToolAllowlist(t *testing.T) {
	mcp := &stubApprovalMCP{result: schema.ToolResult{Content: "ok"}}
	executor := NewExecutor(mcp)
	runtimeRef := &Runtime{approvals: newApprovalStore()}
	execCtx := newExecutionContext("fixer", config.AgentProfile{
		ToolPolicy:       config.ToolPolicyConfirm,
		AllowedToolKinds: []config.ToolKind{config.ToolKindWrite},
		AllowedTools:     []string{"edit_file"},
	}, []schema.Tool{{
		Name:        "write_file",
		Kind:        "write",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`),
	}}, nil, "C:/repo")
	rememberExecutionApproval(t, &execCtx, schema.ToolCall{Name: "write_file", Arguments: json.RawMessage(`{"path":"a.txt","content":"hello"}`)}, execCtx.Tools[0])

	results, err := executor.RunToolCalls(context.Background(), execCtx, []schema.ToolCall{{
		ID:        "call-remembered-denied",
		Name:      "write_file",
		Arguments: json.RawMessage(`{"path":"a.txt","content":"hello"}`),
	}}, runtimeRef, nil)
	if err != nil {
		t.Fatalf("expected allowlist rejection as tool result, got %v", err)
	}
	if mcp.calls != 0 {
		t.Fatalf("expected no MCP call, got %d", mcp.calls)
	}
	if len(results) != 1 || !results[0].Denied || !results[0].IsError {
		t.Fatalf("expected denied validation result, got %#v", results)
	}
	if !strings.Contains(results[0].Content, "tool write_file is not allowed; allowed tools: edit_file") {
		t.Fatalf("unexpected validation result: %#v", results[0])
	}
}

func TestExecutorRememberedApprovalDoesNotBypassToolKindDenial(t *testing.T) {
	mcp := &stubApprovalMCP{result: schema.ToolResult{Content: "ok"}}
	executor := NewExecutor(mcp)
	runtimeRef := &Runtime{approvals: newApprovalStore()}
	execCtx := newExecutionContext("planner", config.AgentProfile{
		ToolPolicy:       config.ToolPolicyConfirm,
		AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
	}, []schema.Tool{{
		Name:        "write_file",
		Kind:        "write",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`),
	}}, nil, "C:/repo")
	rememberExecutionApproval(t, &execCtx, schema.ToolCall{Name: "write_file", Arguments: json.RawMessage(`{"path":"a.txt","content":"hello"}`)}, execCtx.Tools[0])

	results, err := executor.RunToolCalls(context.Background(), execCtx, []schema.ToolCall{{
		ID:        "call-kind-denied",
		Name:      "write_file",
		Arguments: json.RawMessage(`{"path":"a.txt","content":"hello"}`),
	}}, runtimeRef, nil)
	if err != nil {
		t.Fatalf("expected kind rejection as tool result, got %v", err)
	}
	if mcp.calls != 0 {
		t.Fatalf("expected no MCP call, got %d", mcp.calls)
	}
	if len(results) != 1 || !results[0].Denied || !results[0].IsError {
		t.Fatalf("expected denied validation result, got %#v", results)
	}
	if !strings.Contains(results[0].Content, "tool kind write is not allowed; allowed kinds: read") {
		t.Fatalf("unexpected validation result: %#v", results[0])
	}
}

func TestRuntimeApproveToolCallExecutesQueuedCall(t *testing.T) {
	mcp := &stubApprovalMCP{result: schema.ToolResult{Content: "ok"}}
	runtimeRef := &Runtime{mcp: mcp, approvals: newApprovalStore()}
	runtimeRef.queueApproval(schema.ToolCall{
		ID:        "call-1",
		Name:      "read_file",
		Arguments: json.RawMessage(`{"path":"a.txt"}`),
	}, schema.Tool{Name: "read_file"}, "fixer")

	result, err := runtimeRef.ApproveToolCall(context.Background(), "call-1")
	if err != nil {
		t.Fatalf("ApproveToolCall: %v", err)
	}
	if mcp.calls != 1 {
		t.Fatalf("expected MCP call after approval, got %d", mcp.calls)
	}
	if result.CallID != "call-1" || result.Content != "ok" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if len(runtimeRef.PendingApprovals()) != 0 {
		t.Fatalf("expected pending approvals to be cleared")
	}
}

func TestRuntimeApproveToolCallDoesNotRememberToolForFutureRequests(t *testing.T) {
	mcp := &stubApprovalMCP{result: schema.ToolResult{Content: "ok"}}
	state := session.New(4)
	runtimeRef := &Runtime{
		cfg:       &config.Config{WorkspaceRoot: "C:/repo"},
		mcp:       mcp,
		session:   state,
		approvals: newApprovalStore(),
	}
	runtimeRef.queueApproval(schema.ToolCall{
		ID:        "call-1",
		Name:      "write_file",
		Arguments: json.RawMessage(`{"path":"a.txt","content":"hello"}`),
	}, schema.Tool{Name: "write_file"}, "fixer")

	result, err := runtimeRef.ApproveToolCall(context.Background(), "call-1")
	if err != nil {
		t.Fatalf("ApproveToolCall: %v", err)
	}
	if mcp.calls != 1 {
		t.Fatalf("expected MCP call after approval, got %d", mcp.calls)
	}
	if result.CallID != "call-1" || result.Content != "ok" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if state.HasApprovedTool("C:/repo", "write_file") {
		t.Fatal("single-call approval must not be remembered for future write_file calls")
	}
	if state.HasApprovedToolScope("C:/repo", "write", "write_file") {
		t.Fatal("single-call approval must not be remembered as a scoped future approval")
	}
}

func TestRuntimeApproveToolCallAndRememberStoresScopedToolApproval(t *testing.T) {
	mcp := &stubApprovalMCP{result: schema.ToolResult{Content: "ok"}}
	state := session.New(4)
	runtimeRef := &Runtime{
		cfg:       &config.Config{WorkspaceRoot: "C:/repo"},
		mcp:       mcp,
		session:   state,
		approvals: newApprovalStore(),
	}
	runtimeRef.queueApproval(schema.ToolCall{
		ID:        "call-1",
		Name:      "write_file",
		Arguments: json.RawMessage(`{"path":"a.txt","content":"hello"}`),
	}, schema.Tool{Name: "write_file", Kind: "write"}, "fixer")

	result, err := runtimeRef.ApproveToolCallAndRemember(context.Background(), "call-1")
	if err != nil {
		t.Fatalf("ApproveToolCallAndRemember: %v", err)
	}
	if mcp.calls != 1 {
		t.Fatalf("expected MCP call after approval, got %d", mcp.calls)
	}
	if result.CallID != "call-1" || result.Content != "ok" {
		t.Fatalf("unexpected result: %#v", result)
	}
	scopes := state.ApprovalScopes(time.Now().UTC())
	if len(scopes) != 1 || scopes[0].ToolKind != "write" || scopes[0].ToolName != "write_file" || scopes[0].Workspace != "C:/repo" {
		t.Fatalf("expected exact write_file approval to be remembered, got %#v", scopes)
	}
}

func TestRiskPolicyRejectsRememberForUnsandboxedRiskyTool(t *testing.T) {
	mcp := &stubApprovalMCP{result: schema.ToolResult{Content: "ok"}}
	state := session.New(4)
	runtimeRef := &Runtime{
		cfg: &config.Config{
			WorkspaceRoot: "C:/repo",
			ToolRiskPolicy: config.ToolRiskPolicyConfig{
				DisableRememberForUnsandboxedRiskyTools: true,
			},
			MCP: []config.MCPServerRef{{Name: "stub", Isolation: "process_group"}},
		},
		mcp:       mcp,
		session:   state,
		approvals: newApprovalStore(),
	}
	runtimeRef.queueApproval(schema.ToolCall{
		ID:        "call-risk-remember",
		Name:      "write_file",
		Arguments: json.RawMessage(`{"path":"a.txt","content":"hello"}`),
	}, schema.Tool{Name: "write_file", Server: "stub", Kind: "write"}, "fixer")

	if _, err := runtimeRef.ApproveToolCallAndRemember(context.Background(), "call-risk-remember"); err == nil || !strings.Contains(err.Error(), "tool_risk_policy") {
		t.Fatalf("expected tool_risk_policy remember rejection, got %v", err)
	}
	if mcp.calls != 0 {
		t.Fatalf("expected rejected remember attempt not to call MCP, got %d calls", mcp.calls)
	}
	if len(runtimeRef.PendingApprovals()) != 1 {
		t.Fatalf("expected pending approval to remain queued, got %#v", runtimeRef.PendingApprovals())
	}
	if len(state.Snapshot().PendingApprovals) != 1 {
		t.Fatalf("expected session pending approval to remain queued, got %#v", state.Snapshot().PendingApprovals)
	}

	result, err := runtimeRef.ApproveToolCall(context.Background(), "call-risk-remember")
	if err != nil {
		t.Fatalf("ApproveToolCall: %v", err)
	}
	if result.CallID != "call-risk-remember" || mcp.calls != 1 {
		t.Fatalf("expected approve-once to execute after remember rejection, result=%#v calls=%d", result, mcp.calls)
	}
	if state.HasApprovedToolScope("C:/repo", "write", "write_file") {
		t.Fatal("expected risky unsandboxed tool not to be remembered")
	}
}

func TestRiskPolicyRejectsApproveForQueuedUnsandboxedRiskyTool(t *testing.T) {
	mcp := &stubApprovalMCP{result: schema.ToolResult{Content: "ok"}}
	state := session.New(4)
	runtimeRef := &Runtime{
		cfg: &config.Config{
			WorkspaceRoot: "C:/repo",
			ToolRiskPolicy: config.ToolRiskPolicyConfig{
				RejectUnsandboxedRiskyTools: true,
			},
			MCP: []config.MCPServerRef{{Name: "stub", Isolation: "process_group"}},
		},
		mcp:       mcp,
		session:   state,
		approvals: newApprovalStore(),
	}
	runtimeRef.queueApproval(schema.ToolCall{
		ID:        "call-risk-approve",
		Name:      "write_file",
		Arguments: json.RawMessage(`{"path":"a.txt","content":"hello"}`),
	}, schema.Tool{Name: "write_file", Server: "stub", Kind: "write"}, "fixer")

	if ok, reason := runtimeRef.CanApprovePendingToolApproval("call-risk-approve"); ok || !strings.Contains(reason, "reject_unsandboxed_risky_tools") {
		t.Fatalf("expected approve action unavailable by risk policy, ok=%v reason=%q", ok, reason)
	}
	if _, err := runtimeRef.ApproveToolCall(context.Background(), "call-risk-approve"); err == nil || !strings.Contains(err.Error(), "reject_unsandboxed_risky_tools") {
		t.Fatalf("expected risk-policy approve rejection, got %v", err)
	}
	if mcp.calls != 0 {
		t.Fatalf("expected rejected approval not to call MCP, got %d calls", mcp.calls)
	}
	if len(runtimeRef.PendingApprovals()) != 1 {
		t.Fatalf("expected pending approval to remain queued for deny/cancel, got %#v", runtimeRef.PendingApprovals())
	}
	denied, err := runtimeRef.DenyToolCall("call-risk-approve")
	if err != nil {
		t.Fatalf("DenyToolCall: %v", err)
	}
	if !denied.Denied || !denied.IsError {
		t.Fatalf("expected deny to remain available, got %#v", denied)
	}
}

func TestRunStreamIgnoresPersistedApprovedToolsForConfirmPolicy(t *testing.T) {
	state := session.New(4)
	state.RememberApprovedTool("C:/repo", "write_file")
	llm := &scriptedLLMClient{calls: []scriptedLLMCall{{
		response: schema.ChatResponse{ToolCalls: []schema.ToolCall{{
			ID:        "call-1",
			Name:      "write_file",
			Arguments: json.RawMessage(`{"path":"a.txt","content":"hello"}`),
		}}},
	}}}
	mcp := &stubRuntimeMCP{tools: []schema.Tool{{
		Name:        "write_file",
		Kind:        "write",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`),
	}}}
	runtimeRef := &Runtime{
		cfg:       &config.Config{WorkspaceRoot: "C:/repo"},
		session:   state,
		approvals: newApprovalStore(),
	}
	runner := &AgentRunner{id: "fixer", profile: config.AgentProfile{
		Name:             "Fixer",
		Mode:             "fix",
		Model:            "test-model",
		MaxIterations:    2,
		AllowedToolKinds: []config.ToolKind{config.ToolKindWrite},
		ToolPolicy:       config.ToolPolicyConfirm,
	}, llm: llm, executor: NewExecutor(mcp)}

	result, err := runner.RunStream(context.Background(), "write a file", stubSkillManager{}, mcp, state, nil, runtimeRef, nil)
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	if mcp.calls != 0 {
		t.Fatalf("expected persisted approval memory not to execute write directly, got %d calls", mcp.calls)
	}
	if len(result.ToolResults) != 1 || !result.ToolResults[0].Suspended {
		t.Fatalf("expected write to suspend for fresh approval, got %#v", result.ToolResults)
	}
	if len(runtimeRef.PendingApprovals()) != 1 {
		t.Fatalf("expected pending approval for write_file, got %#v", runtimeRef.PendingApprovals())
	}
}

func TestRunStreamUsesSessionApprovedToolScopeForCurrentSession(t *testing.T) {
	state := session.New(4)
	rememberedCall := schema.ToolCall{
		ID:        "call-1",
		Name:      "write_file",
		Arguments: json.RawMessage(`{"path":"a.txt","content":"hello"}`),
	}
	rememberedTool := schema.Tool{
		Name:        "write_file",
		Kind:        "write",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`),
	}
	approvalContext := newExecutionContext("fixer", config.AgentProfile{}, []schema.Tool{rememberedTool}, nil, "C:/repo")
	approvalContext.Session = state
	rememberExecutionApproval(t, &approvalContext, rememberedCall, rememberedTool)
	llm := &scriptedLLMClient{calls: []scriptedLLMCall{{
		response: schema.ChatResponse{ToolCalls: []schema.ToolCall{{
			ID:        rememberedCall.ID,
			Name:      rememberedCall.Name,
			Arguments: rememberedCall.Arguments,
		}}},
	}}}
	mcp := &stubRuntimeMCP{tools: []schema.Tool{rememberedTool}}
	runtimeRef := &Runtime{
		cfg:       &config.Config{WorkspaceRoot: "C:/repo"},
		session:   state,
		approvals: newApprovalStore(),
	}
	runner := &AgentRunner{id: "fixer", profile: config.AgentProfile{
		Name:             "Fixer",
		Mode:             "fix",
		Model:            "test-model",
		MaxIterations:    2,
		AllowedToolKinds: []config.ToolKind{config.ToolKindWrite},
		ToolPolicy:       config.ToolPolicyConfirm,
	}, llm: llm, executor: NewExecutor(mcp)}

	result, err := runner.RunStream(context.Background(), "write a file", stubSkillManager{}, mcp, state, nil, runtimeRef, nil)
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	if mcp.calls != 1 {
		t.Fatalf("expected scoped approval memory to execute write directly, got %d calls", mcp.calls)
	}
	if len(result.ToolResults) != 1 || result.ToolResults[0].Suspended {
		t.Fatalf("expected direct tool result, got %#v", result.ToolResults)
	}
	if len(runtimeRef.PendingApprovals()) != 0 {
		t.Fatalf("expected no pending approval for remembered write_file, got %#v", runtimeRef.PendingApprovals())
	}
}

func TestRuntimeDenyToolCallRejectsQueuedCall(t *testing.T) {
	runtimeRef := &Runtime{approvals: newApprovalStore()}
	runtimeRef.queueApproval(schema.ToolCall{ID: "call-1", Name: "read_file"}, schema.Tool{Name: "read_file"}, "fixer")

	result, err := runtimeRef.DenyToolCall("call-1")
	if err != nil {
		t.Fatalf("DenyToolCall: %v", err)
	}
	if !result.Denied || !result.IsError {
		t.Fatalf("expected denied result, got %#v", result)
	}
	if len(runtimeRef.PendingApprovals()) != 0 {
		t.Fatalf("expected pending approvals to be cleared")
	}
}

func TestRuntimeApproveAllPendingToolCallsExecutesQueuedCalls(t *testing.T) {
	mcp := &stubApprovalMCP{result: schema.ToolResult{Content: "ok"}}
	runtimeRef := &Runtime{mcp: mcp, approvals: newApprovalStore()}
	runtimeRef.queueApproval(schema.ToolCall{
		ID:        "call-1",
		Name:      "write_file",
		Arguments: json.RawMessage(`{"path":"a.txt","content":"hello"}`),
	}, schema.Tool{Name: "write_file"}, "fixer")
	runtimeRef.queueApproval(schema.ToolCall{
		ID:        "call-2",
		Name:      "write_file",
		Arguments: json.RawMessage(`{"path":"b.txt","content":"world"}`),
	}, schema.Tool{Name: "write_file"}, "fixer")

	results, err := runtimeRef.ApproveAllPendingToolCalls(context.Background())
	if err != nil {
		t.Fatalf("ApproveAllPendingToolCalls: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if mcp.calls != 2 {
		t.Fatalf("expected 2 MCP calls, got %d", mcp.calls)
	}
	if len(runtimeRef.PendingApprovals()) != 0 {
		t.Fatalf("expected pending approvals to be cleared")
	}
}

func TestRuntimeApprovePendingToolCallsForWorkflowExecutesOnlyMatchingQueuedCalls(t *testing.T) {
	mcp := &stubApprovalMCP{result: schema.ToolResult{Content: "ok"}}
	runtimeRef := &Runtime{cfg: &config.Config{WorkspaceRoot: "C:/repo"}, mcp: mcp, session: session.New(4), approvals: newApprovalStore()}
	runtimeRef.approvals.Add(pendingApproval{
		call:     schema.ToolCall{ID: "call-1", Name: "write_file", Arguments: json.RawMessage(`{"path":"a.txt","content":"hello"}`)},
		tool:     schema.Tool{Name: "write_file"},
		agent:    "fixer",
		workflow: workflowNamePlanFixAudit,
		stage:    WorkflowStageFix,
		request:  "build flask hello world",
	})
	runtimeRef.approvals.Add(pendingApproval{
		call:     schema.ToolCall{ID: "call-2", Name: "write_file", Arguments: json.RawMessage(`{"path":"b.txt","content":"world"}`)},
		tool:     schema.Tool{Name: "write_file"},
		agent:    "fixer",
		workflow: "other-workflow",
		stage:    WorkflowStageFix,
		request:  "do something else",
	})

	results, err := runtimeRef.ApprovePendingToolCallsForWorkflow(context.Background(), workflowNamePlanFixAudit)
	if err != nil {
		t.Fatalf("ApprovePendingToolCallsForWorkflow: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 workflow-scoped result, got %d", len(results))
	}
	if results[0].CallID != "call-1" {
		t.Fatalf("expected call-1 to be approved, got %#v", results)
	}
	if mcp.calls != 1 {
		t.Fatalf("expected 1 MCP call, got %d", mcp.calls)
	}
	pending := runtimeRef.PendingApprovals()
	if len(pending) != 1 || pending[0].ID != "call-2" {
		t.Fatalf("expected unrelated approval to remain pending, got %#v", pending)
	}
}

func TestRiskPolicyRejectsWorkflowRememberForUnsandboxedRiskyTool(t *testing.T) {
	mcp := &stubApprovalMCP{result: schema.ToolResult{Content: "ok"}}
	runtimeRef := &Runtime{
		cfg: &config.Config{
			ToolRiskPolicy: config.ToolRiskPolicyConfig{
				DisableRememberForUnsandboxedRiskyTools: true,
			},
			MCP: []config.MCPServerRef{{Name: "stub", Isolation: "process_group"}},
		},
		mcp:       mcp,
		approvals: newApprovalStore(),
	}
	runtimeRef.approvals.Add(pendingApproval{
		call:     schema.ToolCall{ID: "call-workflow-risk", Name: "write_file", Arguments: json.RawMessage(`{"path":"a.txt","content":"hello"}`)},
		tool:     schema.Tool{Name: "write_file", Server: "stub", Kind: "write"},
		agent:    "fixer",
		workflow: workflowNamePlanFixAudit,
		stage:    WorkflowStageFix,
		request:  "build flask hello world",
	})

	if err := runtimeRef.RememberPendingWorkflowToolApproval("call-workflow-risk"); err == nil || !strings.Contains(err.Error(), "tool_risk_policy") {
		t.Fatalf("expected workflow remember rejection, got %v", err)
	}
	if _, err := runtimeRef.ApprovePendingToolCallsForWorkflow(context.Background(), workflowNamePlanFixAudit); err == nil || !strings.Contains(err.Error(), "tool_risk_policy") {
		t.Fatalf("expected approve-tools remember rejection before execution, got %v", err)
	}
	if mcp.calls != 0 {
		t.Fatalf("expected workflow approve-tools rejection not to execute MCP, got %d calls", mcp.calls)
	}
	if len(runtimeRef.PendingApprovals()) != 1 {
		t.Fatalf("expected workflow pending approval to remain queued, got %#v", runtimeRef.PendingApprovals())
	}
}

func TestRuntimeWorkflowAutoApprovalTracksMultipleToolsWithinSameStage(t *testing.T) {
	runtimeRef := &Runtime{cfg: &config.Config{WorkspaceRoot: "C:/repo"}, session: session.New(4)}
	writeCall := pendingApproval{
		workflow: workflowNamePlanFixAudit,
		stage:    WorkflowStageFix,
		call:     schema.ToolCall{Name: "write_file", Arguments: json.RawMessage(`{"path":"a.txt","content":"hello"}`)},
		tool:     schema.Tool{Name: "write_file", Kind: "write"},
	}
	commandCall := pendingApproval{
		workflow: workflowNamePlanFixAudit,
		stage:    WorkflowStageFix,
		call:     commandToolCall(t, "git", []string{"status", "--short"}),
		tool:     schema.Tool{Name: "run_command", Kind: "exec"},
	}
	if err := runtimeRef.enableWorkflowAutoApproval(writeCall); err != nil {
		t.Fatal(err)
	}
	if err := runtimeRef.enableWorkflowAutoApproval(commandCall); err != nil {
		t.Fatal(err)
	}

	if !runtimeRef.shouldAutoApproveWorkflowCall(writeCall) {
		t.Fatal("expected write_file to remain auto-approved after enabling another tool in same workflow stage")
	}
	if !runtimeRef.shouldAutoApproveWorkflowCall(commandCall) {
		t.Fatal("expected run_command to be auto-approved in same workflow stage")
	}
	changedCommand := commandCall
	changedCommand.call = commandToolCall(t, "git", []string{"clean", "-fd"})
	if runtimeRef.shouldAutoApproveWorkflowCall(changedCommand) {
		t.Fatal("exact git status approval must not authorize another command")
	}
}

func TestRuntimeWorkflowAutoApprovalRemainsStageScopedWithinWorkflow(t *testing.T) {
	runtimeRef := &Runtime{cfg: &config.Config{WorkspaceRoot: "C:/repo"}, session: session.New(4)}
	fixCall := pendingApproval{
		workflow: workflowNamePlanFixAudit,
		stage:    WorkflowStageFix,
		call:     schema.ToolCall{Name: "write_file", Arguments: json.RawMessage(`{"path":"a.txt","content":"hello"}`)},
		tool:     schema.Tool{Name: "write_file", Kind: "write"},
	}
	auditCall := pendingApproval{
		workflow: workflowNamePlanFixAudit,
		stage:    WorkflowStageAudit,
		call:     schema.ToolCall{Name: "read_file", Arguments: json.RawMessage(`{"path":"a.txt"}`)},
		tool:     schema.Tool{Name: "read_file", Kind: "read"},
	}
	if err := runtimeRef.enableWorkflowAutoApproval(fixCall); err != nil {
		t.Fatal(err)
	}
	if err := runtimeRef.enableWorkflowAutoApproval(auditCall); err != nil {
		t.Fatal(err)
	}

	if !runtimeRef.shouldAutoApproveWorkflowCall(fixCall) {
		t.Fatal("expected fix-stage write_file to stay auto-approved")
	}
	if !runtimeRef.shouldAutoApproveWorkflowCall(auditCall) {
		t.Fatal("expected audit-stage read_file to stay auto-approved")
	}
	wrongStage := fixCall
	wrongStage.stage = WorkflowStageAudit
	if runtimeRef.shouldAutoApproveWorkflowCall(wrongStage) {
		t.Fatal("expected auto-approval to reject same tool name in different stage when not enabled there")
	}
}

func TestRuntimeDisableWorkflowAutoApprovalClearsAllScopesForWorkflow(t *testing.T) {
	runtimeRef := &Runtime{cfg: &config.Config{WorkspaceRoot: "C:/repo"}, session: session.New(4)}
	fixCall := pendingApproval{
		workflow: workflowNamePlanFixAudit,
		stage:    WorkflowStageFix,
		call:     schema.ToolCall{Name: "write_file", Arguments: json.RawMessage(`{"path":"a.txt","content":"hello"}`)},
		tool:     schema.Tool{Name: "write_file", Kind: "write"},
	}
	auditCall := pendingApproval{
		workflow: workflowNamePlanFixAudit,
		stage:    WorkflowStageAudit,
		call:     schema.ToolCall{Name: "read_file", Arguments: json.RawMessage(`{"path":"a.txt"}`)},
		tool:     schema.Tool{Name: "read_file", Kind: "read"},
	}
	if err := runtimeRef.enableWorkflowAutoApproval(fixCall); err != nil {
		t.Fatal(err)
	}
	if err := runtimeRef.enableWorkflowAutoApproval(auditCall); err != nil {
		t.Fatal(err)
	}
	runtimeRef.DisableWorkflowAutoApproval(workflowNamePlanFixAudit)

	if runtimeRef.shouldAutoApproveWorkflowCall(fixCall) {
		t.Fatal("expected workflow auto-approval to be cleared for fix stage")
	}
	if runtimeRef.shouldAutoApproveWorkflowCall(auditCall) {
		t.Fatal("expected workflow auto-approval to be cleared for audit stage")
	}
}

func TestResumePlanFixAuditContinuesAfterApproveAllExecution(t *testing.T) {
	ctx := context.Background()
	runtimeRef := newWorkflowTestRuntimeWithSuspendedFixTool()

	first, err := runtimeRef.WorkflowRunner().Run(ctx, workflowNamePlanFixAudit, "build flask hello world", true, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if first.Status != "awaiting_tool_approval" {
		t.Fatalf("expected awaiting_tool_approval, got %q", first.Status)
	}
	if len(first.CompletedStages) != 1 {
		t.Fatalf("expected only planner stage before approval, got %d", len(first.CompletedStages))
	}

	pending := runtimeRef.SessionSnapshot().PendingApprovals
	if len(pending) != 1 {
		t.Fatalf("expected one pending approval summary, got %d", len(pending))
	}
	if pending[0].CallID == "" {
		t.Fatal("expected session snapshot to preserve approval call id")
	}
	callID := pending[0].CallID

	results, err := runtimeRef.ApproveAllPendingToolCalls(ctx)
	if err != nil {
		t.Fatalf("ApproveAllPendingToolCalls: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected one approved tool result, got %d", len(results))
	}
	if len(runtimeRef.PendingApprovals()) != 0 {
		t.Fatalf("expected pending approvals to be cleared")
	}

	resumed, err := runtimeRef.WorkflowRunner().Resume(ctx, workflowNamePlanFixAudit, callID, true, nil)
	if err != nil {
		t.Fatalf("Resume after approve-all: %v", err)
	}
	if resumed.Status != "completed" {
		t.Fatalf("expected completed after resume, got %q", resumed.Status)
	}
	if len(resumed.CompletedStages) != 3 {
		t.Fatalf("expected planner, fixer, and auditor stages after resume, got %d", len(resumed.CompletedStages))
	}
	if runtimeRef.ActiveAgent() != workflowAgentPlanner {
		t.Fatalf("expected runtime to restore default planner agent after resume, got %q", runtimeRef.ActiveAgent())
	}
	if snapshot := runtimeRef.SessionSnapshot(); snapshot.ActiveAgent != workflowAgentPlanner || snapshot.Mode != "plan" {
		t.Fatalf("expected session to restore planner/plan mode after resume, got %#v", snapshot)
	}
}

func TestWorkflowResumeDoesNotReachIntoApprovalStoreWithoutLock(t *testing.T) {
	runtimeRef := &Runtime{approvals: newApprovalStore()}
	runtimeRef.approvals.Add(pendingApproval{
		call:  schema.ToolCall{ID: "call-1", Name: "write_file", Arguments: json.RawMessage(`{"path":"a.txt","content":"hello"}`)},
		tool:  schema.Tool{Name: "write_file"},
		agent: "fixer",
	})
	workflow := &WorkflowRunner{runtime: runtimeRef}

	runtimeRef.approvals.mu.Lock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		workflow.captureApprovalContext(workflowNamePlanFixAudit, WorkflowStageFix, "build flask hello world", "fixer requested write_file", schema.Message{}, []WorkflowStageResult{{
			Stage:  WorkflowStagePlan,
			Agent:  workflowAgentPlanner,
			Result: schema.AgentResult{Output: "plan complete"},
		}}, []schema.ToolResult{{
			CallID:    "call-1",
			ToolName:  "write_file",
			Suspended: true,
		}})
	}()

	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		pending := runtimeRef.approvals.pending["call-1"]
		if pending.workflow != "" || pending.stage != "" || pending.request != "" || len(pending.completed) != 0 {
			runtimeRef.approvals.mu.Unlock()
			<-done
			t.Fatalf("captureApprovalContext mutated approvalStore while mutex was held: %#v", pending)
		}
		time.Sleep(5 * time.Millisecond)
	}

	select {
	case <-done:
		t.Fatal("captureApprovalContext completed while approvalStore mutex was held")
	default:
	}

	runtimeRef.approvals.mu.Unlock()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("captureApprovalContext did not finish after approvalStore mutex was released")
	}

	pending, ok := runtimeRef.PendingApproval("call-1")
	if !ok {
		t.Fatal("expected pending approval to remain queued")
	}
	if pending.workflow != workflowNamePlanFixAudit {
		t.Fatalf("expected workflow name to be recorded, got %q", pending.workflow)
	}
	if pending.stage != WorkflowStageFix {
		t.Fatalf("expected workflow stage to be recorded, got %q", pending.stage)
	}
	if pending.request != "build flask hello world" {
		t.Fatalf("expected workflow request to be recorded, got %q", pending.request)
	}
	if pending.responseContent != "fixer requested write_file" {
		t.Fatalf("expected workflow response content to be recorded, got %q", pending.responseContent)
	}
	if pending.responseMessage.Content != "fixer requested write_file" {
		t.Fatalf("expected workflow response message content to be recorded, got %#v", pending.responseMessage)
	}
	if len(pending.completed) != 1 || pending.completed[0].Stage != WorkflowStagePlan {
		t.Fatalf("expected completed workflow context to be copied, got %#v", pending.completed)
	}
}

func TestWorkflowRunnerResumeReportsMissingApprovalAfterApproveAllClearsQueue(t *testing.T) {
	t.Skip("superseded by approve-all resume regression coverage")
}

func TestRegisteredWorkflowPreservesApprovalSuspension(t *testing.T) {
	ctx := context.Background()
	runtimeRef := newWorkflowTestRuntimeWithSuspendedFixTool()

	result, err := runtimeRef.WorkflowRunner().Run(ctx, workflowNamePlanFixAudit, "build flask hello world", true, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(result.CompletedStages) > 0 {
		t.Logf("completed stages before assertion: %#v", result.CompletedStages)
	}
	if len(runtimeRef.PendingApprovals()) > 0 {
		t.Logf("pending approvals before assertion: %#v", runtimeRef.PendingApprovals())
	}
	if result.Status != "awaiting_tool_approval" {
		t.Fatalf("expected awaiting_tool_approval, got %q", result.Status)
	}
	if len(result.CompletedStages) != 1 {
		t.Fatalf("expected only completed plan stage before suspension, got %#v", result.CompletedStages)
	}

	snapshot := runtimeRef.SessionSnapshot()
	if snapshot.Workflow.Name != workflowNamePlanFixAudit {
		t.Fatalf("expected workflow name %q, got %#v", workflowNamePlanFixAudit, snapshot.Workflow)
	}
	if snapshot.Workflow.Status != "awaiting_tool_approval" {
		t.Fatalf("expected persisted workflow status, got %#v", snapshot.Workflow)
	}
	if snapshot.Workflow.NextStage != string(WorkflowStageFix) {
		t.Fatalf("expected next stage fix, got %#v", snapshot.Workflow)
	}
	if len(snapshot.PendingApprovals) != 1 || snapshot.PendingApprovals[0].CallID != "call-1" {
		t.Fatalf("expected persisted pending approval, got %#v", snapshot.PendingApprovals)
	}
}

func TestResumePlanFixAuditClearsPersistedWorkflowStateAfterApproval(t *testing.T) {
	ctx := context.Background()
	runtimeRef := newWorkflowTestRuntimeWithSuspendedFixToolAndDefaultAgent("chat")

	_, err := runtimeRef.WorkflowRunner().RunPlanFixAudit(ctx, "build flask hello world", true, nil)
	if err != nil {
		t.Fatalf("RunPlanFixAudit: %v", err)
	}
	_, err = runtimeRef.WorkflowRunner().ResumePlanFixAudit(ctx, "call-1", true, nil)
	if err != nil {
		t.Fatalf("ResumePlanFixAudit: %v", err)
	}

	snapshot := runtimeRef.SessionSnapshot()
	if snapshot.Workflow.Status != "completed" {
		t.Fatalf("expected completed workflow status, got %#v", snapshot.Workflow)
	}
	if len(snapshot.PendingApprovals) != 0 {
		t.Fatalf("expected approvals to be cleared, got %#v", snapshot.PendingApprovals)
	}
	if runtimeRef.ActiveAgent() != "chat" {
		t.Fatalf("expected active agent restored to chat, got %q", runtimeRef.ActiveAgent())
	}
	if snapshot.ActiveAgent != "chat" {
		t.Fatalf("expected session active agent restored to chat, got %#v", snapshot)
	}
	if snapshot.Mode != "chat" {
		t.Fatalf("expected session mode restored to chat, got %#v", snapshot)
	}
}

func TestResumePlanFixAuditPersistsDeniedWorkflowState(t *testing.T) {
	ctx := context.Background()
	runtimeRef := newWorkflowTestRuntimeWithSuspendedFixToolAndDefaultAgent("chat")

	_, err := runtimeRef.WorkflowRunner().RunPlanFixAudit(ctx, "build flask hello world", true, nil)
	if err != nil {
		t.Fatalf("RunPlanFixAudit: %v", err)
	}
	_, err = runtimeRef.WorkflowRunner().ResumePlanFixAudit(ctx, "call-1", false, nil)
	if err != nil {
		t.Fatalf("ResumePlanFixAudit: %v", err)
	}

	snapshot := runtimeRef.SessionSnapshot()
	if snapshot.Workflow.Status != "denied" {
		t.Fatalf("expected denied workflow status, got %#v", snapshot.Workflow)
	}
	if snapshot.Workflow.NextStage != string(WorkflowStageFix) {
		t.Fatalf("expected next stage fix after deny, got %#v", snapshot.Workflow)
	}
	if len(snapshot.PendingApprovals) != 0 {
		t.Fatalf("expected approvals to be cleared, got %#v", snapshot.PendingApprovals)
	}
	if runtimeRef.ActiveAgent() != "chat" {
		t.Fatalf("expected active agent restored to chat after denial, got %q", runtimeRef.ActiveAgent())
	}
	if snapshot.ActiveAgent != "chat" {
		t.Fatalf("expected session active agent restored to chat after denial, got %#v", snapshot)
	}
	if snapshot.Mode != "chat" {
		t.Fatalf("expected session mode restored to chat after denial, got %#v", snapshot)
	}
}

func TestResumePlanFixAuditContinuesAfterApproval(t *testing.T) {
	ctx := context.Background()
	runtimeRef := newWorkflowTestRuntimeWithSuspendedFixTool()

	first, err := runtimeRef.WorkflowRunner().RunPlanFixAudit(ctx, "build flask hello world", true, nil)
	if err != nil {
		t.Fatalf("RunPlanFixAudit: %v", err)
	}
	if first.Status != "awaiting_tool_approval" {
		t.Fatalf("expected awaiting_tool_approval, got %q", first.Status)
	}

	second, err := runtimeRef.WorkflowRunner().ResumePlanFixAudit(ctx, "call-1", true, nil)
	if err != nil {
		t.Fatalf("ResumePlanFixAudit: %v", err)
	}
	if second.Status != "completed" {
		t.Fatalf("expected completed, got %q", second.Status)
	}
	if len(second.CompletedStages) != 3 {
		t.Fatalf("expected 3 completed stages, got %d", len(second.CompletedStages))
	}
}

func TestResumePlanFixAuditDoesNotAdvanceAfterApprovalWithoutResume(t *testing.T) {
	ctx := context.Background()
	runtimeRef := newWorkflowTestRuntimeWithSuspendedFixTool()

	first, err := runtimeRef.WorkflowRunner().RunPlanFixAudit(ctx, "build flask hello world", true, nil)
	if err != nil {
		t.Fatalf("RunPlanFixAudit: %v", err)
	}
	if first.Status != "awaiting_tool_approval" {
		t.Fatalf("expected awaiting_tool_approval, got %q", first.Status)
	}

	result, err := runtimeRef.ApproveToolCall(ctx, "call-1")
	if err != nil {
		t.Fatalf("ApproveToolCall: %v", err)
	}
	if result.CallID != "call-1" {
		t.Fatalf("expected approved call-1 result, got %#v", result)
	}

	snapshot := runtimeRef.SessionSnapshot()
	if snapshot.Workflow.Status != "awaiting_tool_approval" {
		t.Fatalf("expected workflow to stay awaiting_tool_approval until resume, got %#v", snapshot.Workflow)
	}
	if snapshot.Workflow.NextStage != string(WorkflowStageFix) {
		t.Fatalf("expected next stage fix before resume, got %#v", snapshot.Workflow)
	}
	if len(snapshot.PendingApprovals) != 0 {
		t.Fatalf("expected approved call to clear pending approvals, got %#v", snapshot.PendingApprovals)
	}
}

func TestResumePlanFixAuditStopsWhenFixerSuspendsAgainAfterApproval(t *testing.T) {
	ctx := context.Background()
	capture := newWorkflowTestRuntimeWithRepeatedSuspendedFixTools()

	first, err := capture.runtime.WorkflowRunner().Run(ctx, workflowNamePlanFixAudit, "build flask hello world", true, nil)
	if err != nil {
		t.Fatalf("RunPlanFixAudit: %v", err)
	}
	if first.Status != "awaiting_tool_approval" {
		t.Fatalf("expected awaiting_tool_approval, got %q", first.Status)
	}

	second, err := capture.runtime.WorkflowRunner().ResumePlanFixAudit(ctx, "call-1", true, nil)
	if err != nil {
		t.Fatalf("ResumePlanFixAudit: %v", err)
	}
	if second.Status != "awaiting_tool_approval" {
		t.Fatalf("expected workflow to re-suspend, got %q", second.Status)
	}
	if second.NextStage != WorkflowStageFix {
		t.Fatalf("expected next stage fix, got %q", second.NextStage)
	}
	if len(second.CompletedStages) != 1 {
		t.Fatalf("expected only planner stage to remain completed, got %d", len(second.CompletedStages))
	}
	if len(capture.auditor.requests) != 0 {
		t.Fatalf("expected auditor not to run before second approval, got %d requests", len(capture.auditor.requests))
	}

	snapshot := capture.runtime.SessionSnapshot()
	if snapshot.Workflow.Status != "awaiting_tool_approval" {
		t.Fatalf("expected persisted awaiting_tool_approval, got %#v", snapshot.Workflow)
	}
	if snapshot.Workflow.NextStage != string(WorkflowStageFix) {
		t.Fatalf("expected persisted next stage fix, got %#v", snapshot.Workflow)
	}
	if snapshot.Workflow.PendingCallID != "call-2" {
		t.Fatalf("expected persisted second pending call, got %#v", snapshot.Workflow)
	}
	if len(snapshot.PendingApprovals) != 1 {
		t.Fatalf("expected one pending approval after re-suspension, got %#v", snapshot.PendingApprovals)
	}
	if snapshot.PendingApprovals[0].CallID != "call-2" {
		t.Fatalf("expected pending approval call-2, got %#v", snapshot.PendingApprovals)
	}
	if !strings.Contains(snapshot.PendingApprovals[0].Arguments, "test_calculator.py") {
		t.Fatalf("expected second pending approval args to mention test_calculator.py, got %#v", snapshot.PendingApprovals[0])
	}
}

func TestResumePlanFixAuditRequiresFreshApprovalForChangedArgumentsAfterWorkflowApproveAll(t *testing.T) {
	ctx := context.Background()
	capture := newWorkflowTestRuntimeWithRepeatedSuspendedFixTools()

	first, err := capture.runtime.WorkflowRunner().Run(ctx, workflowNamePlanFixAudit, "build flask hello world", true, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if first.Status != "awaiting_tool_approval" {
		t.Fatalf("expected awaiting_tool_approval, got %q", first.Status)
	}

	results, err := capture.runtime.ApprovePendingToolCallsForWorkflow(ctx, workflowNamePlanFixAudit)
	if err != nil {
		t.Fatalf("ApprovePendingToolCallsForWorkflow: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected first queued workflow approval to execute, got %d", len(results))
	}

	second, err := capture.runtime.WorkflowRunner().ResumePlanFixAudit(ctx, "call-1", true, nil)
	if err != nil {
		t.Fatalf("ResumePlanFixAudit: %v", err)
	}
	if second.Status != "awaiting_tool_approval" {
		t.Fatalf("expected changed write arguments to require fresh approval, got %q", second.Status)
	}
	if len(capture.auditor.requests) != 0 {
		t.Fatalf("expected auditor to wait for the changed write approval, got %d requests", len(capture.auditor.requests))
	}

	snapshot := capture.runtime.SessionSnapshot()
	if snapshot.Workflow.Status != "awaiting_tool_approval" {
		t.Fatalf("expected persisted approval wait, got %#v", snapshot.Workflow)
	}
	if snapshot.Workflow.PendingCallID != "call-2" {
		t.Fatalf("expected changed call-2 to remain pending, got %#v", snapshot.Workflow)
	}
	if len(snapshot.PendingApprovals) != 1 || snapshot.PendingApprovals[0].CallID != "call-2" {
		t.Fatalf("expected only changed call-2 to require approval, got %#v", snapshot.PendingApprovals)
	}
}

func TestResumePlanFixAuditUsesSuspendedFixerToolCallMessageOnResume(t *testing.T) {
	ctx := context.Background()
	capture := newWorkflowTestRuntimeWithRepeatedSuspendedFixTools()

	_, err := capture.runtime.WorkflowRunner().RunPlanFixAudit(ctx, "build flask hello world", true, nil)
	if err != nil {
		t.Fatalf("RunPlanFixAudit: %v", err)
	}
	pending, ok := capture.runtime.PendingApproval("call-1")
	if !ok {
		t.Fatal("expected pending approval for first suspended fixer tool call")
	}
	if pending.responseContent != "need to write first file" {
		t.Fatalf("expected pending response content to preserve suspended fixer output, got %q", pending.responseContent)
	}

	_, err = capture.runtime.WorkflowRunner().ResumePlanFixAudit(ctx, "call-1", true, nil)
	if err != nil {
		t.Fatalf("ResumePlanFixAudit: %v", err)
	}

	if len(capture.fixer.requests) < 2 {
		t.Fatalf("expected fixer to be called twice, got %d requests", len(capture.fixer.requests))
	}
	resumeRequest := capture.fixer.requests[1]
	if len(resumeRequest.Messages) < 3 {
		t.Fatalf("expected resumed fixer request to include prior assistant/tool context, got %#v", resumeRequest.Messages)
	}
	assistant := resumeRequest.Messages[len(resumeRequest.Messages)-2]
	if assistant.Content != "need to write first file" {
		t.Fatalf("expected resumed assistant message to preserve suspended fixer output, got %#v", assistant)
	}
	if len(assistant.ToolCalls) != 1 || assistant.ToolCalls[0].ID != "call-1" {
		t.Fatalf("expected resumed assistant message to replay call-1, got %#v", assistant)
	}
}

func TestResumePlanFixAuditPreservesSuspendedProviderFields(t *testing.T) {
	ctx := context.Background()
	capture := newWorkflowTestRuntimeWithRepeatedSuspendedFixTools()
	capture.fixer.responses[0].Message.ProviderFields = map[string]json.RawMessage{
		"reasoning_content": json.RawMessage(`"private workflow reasoning"`),
	}

	_, err := capture.runtime.WorkflowRunner().RunPlanFixAudit(ctx, "build flask hello world", true, nil)
	if err != nil {
		t.Fatalf("RunPlanFixAudit: %v", err)
	}
	pending, ok := capture.runtime.PendingApproval("call-1")
	if !ok {
		t.Fatal("expected pending approval for first suspended fixer tool call")
	}
	if got := string(pending.responseMessage.ProviderFields["reasoning_content"]); got != `"private workflow reasoning"` {
		t.Fatalf("expected pending response message provider field, got %#v", pending.responseMessage.ProviderFields)
	}

	_, err = capture.runtime.WorkflowRunner().ResumePlanFixAudit(ctx, "call-1", true, nil)
	if err != nil {
		t.Fatalf("ResumePlanFixAudit: %v", err)
	}
	if len(capture.fixer.requests) < 2 {
		t.Fatalf("expected fixer to be called twice, got %d requests", len(capture.fixer.requests))
	}
	resumeRequest := capture.fixer.requests[1]
	assistant := resumeRequest.Messages[len(resumeRequest.Messages)-2]
	if got := string(assistant.ProviderFields["reasoning_content"]); got != `"private workflow reasoning"` {
		t.Fatalf("expected resumed assistant provider field, got %#v", assistant.ProviderFields)
	}
}

func TestWorkflowToolApprovalRebuildPreservesProviderFields(t *testing.T) {
	ctx := context.Background()
	capture := newWorkflowTestRuntimeWithRepeatedSuspendedFixTools()
	capture.fixer.responses[0].Message.ProviderFields = map[string]json.RawMessage{
		"reasoning_content": json.RawMessage(`"private workflow reasoning"`),
	}

	first, err := capture.runtime.WorkflowRunner().Run(ctx, workflowNamePlanFixAudit, "build flask hello world", true, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	run, ok := capture.runtime.session.WorkflowRun(first.RunID)
	if !ok {
		t.Fatalf("expected workflow run %s", first.RunID)
	}
	capture.runtime.approvals = newApprovalStore()
	capture.runtime.approvedWorkflowResumes = nil

	pending, err := capture.runtime.WorkflowRunner().pendingToolApprovalFromRun(ctx, run)
	if err != nil {
		t.Fatalf("pendingToolApprovalFromRun: %v", err)
	}
	if got := string(pending.responseMessage.ProviderFields["reasoning_content"]); got != `"private workflow reasoning"` {
		t.Fatalf("expected rebuilt pending provider field, got %#v", pending.responseMessage.ProviderFields)
	}
}

func TestResumePlanFixAuditReinjectsApprovedToolResultIntoFixerConversation(t *testing.T) {
	ctx := context.Background()
	capture := newWorkflowTestRuntimeWithRepeatedSuspendedFixTools()

	_, err := capture.runtime.WorkflowRunner().RunPlanFixAudit(ctx, "build flask hello world", true, nil)
	if err != nil {
		t.Fatalf("RunPlanFixAudit: %v", err)
	}
	persisted := capture.runtime.SessionSnapshot().PendingApprovals
	if len(persisted) != 1 || len(persisted[0].WorkflowMessages) == 0 || persisted[0].WorkflowSystemPrompt == "" {
		t.Fatalf("expected durable workflow conversation context before approval, got %#v", persisted)
	}
	_, err = capture.runtime.WorkflowRunner().ResumePlanFixAudit(ctx, "call-1", true, nil)
	if err != nil {
		t.Fatalf("ResumePlanFixAudit: %v", err)
	}

	if len(capture.fixer.requests) < 2 {
		t.Fatalf("expected fixer to be called twice, got %d requests", len(capture.fixer.requests))
	}
	resumeRequest := capture.fixer.requests[1]
	if len(resumeRequest.Messages) < 3 {
		t.Fatalf("expected resumed fixer request to include prior assistant/tool context, got %#v", resumeRequest.Messages)
	}
	assistant := resumeRequest.Messages[len(resumeRequest.Messages)-2]
	if assistant.Role != "assistant" {
		t.Fatalf("expected assistant tool-call message before tool result, got %#v", assistant)
	}
	if len(assistant.ToolCalls) != 1 || assistant.ToolCalls[0].ID != "call-1" {
		t.Fatalf("expected assistant message to replay call-1, got %#v", assistant)
	}
	tool := resumeRequest.Messages[len(resumeRequest.Messages)-1]
	if tool.Role != "tool" || tool.ToolCallID != "call-1" {
		t.Fatalf("expected tool result message for call-1, got %#v", tool)
	}
	if !strings.Contains(tool.Content, "done") {
		t.Fatalf("expected resumed tool message to contain approved tool output, got %#v", tool)
	}
}

func TestResumePlanFixAuditPreservesFullConversationAcrossRepeatedApprovals(t *testing.T) {
	ctx := context.Background()
	capture := newWorkflowTestRuntimeWithRepeatedSuspendedFixTools()

	_, err := capture.runtime.WorkflowRunner().RunPlanFixAudit(ctx, "build flask hello world", true, nil)
	if err != nil {
		t.Fatalf("RunPlanFixAudit: %v", err)
	}
	_, err = capture.runtime.WorkflowRunner().ResumePlanFixAudit(ctx, "call-1", true, nil)
	if err != nil {
		t.Fatalf("first ResumePlanFixAudit: %v", err)
	}
	_, err = capture.runtime.WorkflowRunner().ResumePlanFixAudit(ctx, "call-2", true, nil)
	if err != nil {
		t.Fatalf("second ResumePlanFixAudit: %v", err)
	}

	if len(capture.fixer.requests) < 3 {
		t.Fatalf("expected three fixer requests, got %d", len(capture.fixer.requests))
	}
	first := capture.fixer.requests[0]
	resumed := capture.fixer.requests[2]
	if resumed.System != first.System {
		t.Fatal("repeated approval resume must preserve the original system prompt for provider cache stability")
	}
	toolResults := map[string]bool{}
	assistantCalls := map[string]bool{}
	for _, message := range resumed.Messages {
		if message.Role == "tool" {
			toolResults[message.ToolCallID] = true
		}
		if message.Role == "assistant" {
			for _, call := range message.ToolCalls {
				assistantCalls[call.ID] = true
			}
		}
	}
	for _, callID := range []string{"call-1", "call-2"} {
		if !assistantCalls[callID] || !toolResults[callID] {
			t.Fatalf("expected resumed conversation to preserve assistant/tool pair %s, messages=%#v", callID, resumed.Messages)
		}
	}
}

func TestResumePlanFixAuditStopsWhenApprovalDenied(t *testing.T) {
	ctx := context.Background()
	runtimeRef := newWorkflowTestRuntimeWithSuspendedFixTool()

	first, err := runtimeRef.WorkflowRunner().RunPlanFixAudit(ctx, "build flask hello world", true, nil)
	if err != nil {
		t.Fatalf("RunPlanFixAudit: %v", err)
	}
	if first.Status != "awaiting_tool_approval" {
		t.Fatalf("expected awaiting_tool_approval, got %q", first.Status)
	}

	second, err := runtimeRef.WorkflowRunner().ResumePlanFixAudit(ctx, "call-1", false, nil)
	if err != nil {
		t.Fatalf("ResumePlanFixAudit: %v", err)
	}
	if second.Status != "denied" {
		t.Fatalf("expected denied, got %q", second.Status)
	}
	if len(second.CompletedStages) != 2 {
		t.Fatalf("expected planner stage and denied fix stage, got %d", len(second.CompletedStages))
	}
}

func TestAuditStageRejectsWriteToolCalls(t *testing.T) {
	ctx := context.Background()
	runtimeRef := newWorkflowTestRuntimeWithAuditWriteAttempt()

	result, err := runtimeRef.WorkflowRunner().Run(ctx, workflowNamePlanFixAudit, "build flask hello world", true, nil)
	if err != nil {
		t.Fatalf("expected audit-stage write rejection to be reported as tool result, got %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("expected completed workflow with denied audit tool result, got %#v", result)
	}
	auditStage := result.CompletedStages[len(result.CompletedStages)-1].Result
	if len(auditStage.ToolResults) != 1 || !auditStage.ToolResults[0].Denied || !auditStage.ToolResults[0].IsError {
		t.Fatalf("expected denied audit tool result, got %#v", auditStage.ToolResults)
	}
	if !strings.Contains(auditStage.ToolResults[0].Content, "tool kind write is not allowed; allowed kinds: read") {
		t.Fatalf("unexpected audit tool result: %#v", auditStage.ToolResults[0])
	}
}

func TestResumePlanFixAuditAuditPromptUsesFixerSummaryAfterResume(t *testing.T) {
	ctx := context.Background()
	capture := newWorkflowTestRuntimeWithAuditPromptCapture()

	_, err := capture.runtime.WorkflowRunner().RunPlanFixAudit(ctx, "build flask hello world", true, nil)
	if err != nil {
		t.Fatalf("RunPlanFixAudit: %v", err)
	}
	result, err := capture.runtime.WorkflowRunner().ResumePlanFixAudit(ctx, "call-1", true, nil)
	if err != nil {
		t.Fatalf("ResumePlanFixAudit: %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("expected completed workflow, got %q", result.Status)
	}
	if len(result.CompletedStages) != 3 {
		t.Fatalf("expected 4 completed stages, got %d", len(result.CompletedStages))
	}
	if len(capture.auditor.requests) != 1 {
		t.Fatalf("expected one auditor request, got %d", len(capture.auditor.requests))
	}
	auditPrompt := capture.auditor.requests[0].Messages[len(capture.auditor.requests[0].Messages)-1].Content
	if !strings.Contains(auditPrompt, "Implementation summary:\nfix completed") {
		t.Fatalf("expected audit prompt to include fixer summary, got %q", auditPrompt)
	}
}

func TestCaptureOrdinaryApprovalContextDoesNotReopenTerminalRun(t *testing.T) {
	state := session.New(4)
	runID := state.StartAgentRunWithOptions("write report", session.AgentRunStartOptions{AgentID: "chat"})
	runtimeRef := &Runtime{session: state, approvals: newApprovalStore()}
	call := schema.ToolCall{ID: "call-delayed", Name: "write_file", Arguments: json.RawMessage(`{"path":"report.md","content":"done"}`)}
	tool := schema.Tool{Name: "write_file", Server: "file_tools", Kind: string(config.ToolKindWrite)}
	runtimeRef.queueApprovalForAgentRun(call, tool, "chat", runID)

	if _, ok := state.CompleteAgentRun(runID, "completed", schema.AgentResult{Output: "done", AgentID: "chat", Mode: "fix"}); !ok {
		t.Fatal("expected run completion")
	}
	runtimeRef.CaptureOrdinaryApprovalContext(
		runID,
		"chat",
		config.AgentProfile{},
		nil,
		"system",
		"fix",
		[]schema.Message{{Role: "assistant", ToolCalls: []schema.ToolCall{call}}},
		[]schema.ToolCall{call},
		nil,
	)

	run, ok := state.AgentRun(runID)
	if !ok || run.Status != "completed" || run.ResumeContext != nil || len(run.PendingApprovals) != 0 {
		t.Fatalf("delayed capture must not reopen terminal run, got %#v ok=%t", run, ok)
	}
	if pending := runtimeRef.PendingApprovals(); len(pending) != 0 {
		t.Fatalf("delayed capture must discard orphaned approvals, got %#v", pending)
	}
	if pending := state.Snapshot().PendingApprovals; len(pending) != 0 {
		t.Fatalf("compact session must not retain orphaned approvals, got %#v", pending)
	}
}
