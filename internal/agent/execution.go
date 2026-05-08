package agent

import (
	"fmt"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/policy"
	"github.com/FyMatt/GoFlow-Agent/internal/runtime"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

// ExecutionContext provides execution metadata for one run.
type ExecutionContext struct {
	AgentID       string
	AgentRunID    string
	Profile       config.AgentProfile
	Tools         []schema.Tool
	Audit         *runtime.AuditLogger
	WorkspaceRoot string
	RiskPolicy    config.ToolRiskPolicyConfig
	MCPServers    []config.MCPServerRef
	approvedTools map[string]struct{}
}

func newExecutionContext(agentID string, profile config.AgentProfile, tools []schema.Tool, audit *runtime.AuditLogger, workspaceRoot string) ExecutionContext {
	return ExecutionContext{AgentID: agentID, Profile: profile, Tools: tools, Audit: audit, WorkspaceRoot: workspaceRoot, approvedTools: make(map[string]struct{})}
}

func (e ExecutionContext) validateToolCall(call schema.ToolCall) (schema.Tool, error) {
	catalog := policy.NewToolCatalog(e.Tools)
	tool, ok := catalog.Find(call.Name)
	if !ok {
		return schema.Tool{}, fmt.Errorf("tool not found in catalog: %s", call.Name)
	}
	if err := policy.EnforceToolPolicy(e.Profile, tool); err != nil {
		return schema.Tool{}, err
	}
	if err := policy.ValidateToolCall(call, tool); err != nil {
		return schema.Tool{}, err
	}
	return tool, nil
}

func (e ExecutionContext) requiresApproval(tool schema.Tool) bool {
	if e.RiskPolicy.RequireApprovalForUnsandboxedRiskyTools && e.toolRequiresRiskApproval(tool) {
		return true
	}
	if e.Profile.ToolPolicy != config.ToolPolicyConfirm {
		return false
	}
	if policy.KindForTool(tool) == config.ToolKindRead {
		return false
	}
	return !e.hasRememberedApproval(tool)
}

func (e ExecutionContext) canRememberApproval(tool schema.Tool) bool {
	if e.RiskPolicy.DisableRememberForUnsandboxedRiskyTools && e.toolRequiresRiskApproval(tool) {
		return false
	}
	return true
}

func (e ExecutionContext) rejectByRiskPolicy(tool schema.Tool) error {
	if !e.RiskPolicy.RejectUnsandboxedRiskyTools || !e.toolRequiresRiskApproval(tool) {
		return nil
	}
	name := strings.TrimSpace(tool.Name)
	if name == "" {
		name = "tool"
	}
	kind := string(policy.KindForTool(tool))
	if strings.TrimSpace(kind) == "" {
		kind = "unknown"
	}
	return fmt.Errorf("tool_risk_policy rejects unsandboxed risky tool %s (kind=%s); run it with isolation: container, use a capability-specific sandbox such as linux_netns for network-only tools, or disable reject_unsandboxed_risky_tools", name, kind)
}

func (e ExecutionContext) toolRequiresRiskApproval(tool schema.Tool) bool {
	kind := policy.KindForTool(tool)
	if kind == config.ToolKindRead {
		return false
	}
	if e.toolCapabilitySandboxed(tool) {
		return false
	}
	return kind == config.ToolKindWrite || kind == config.ToolKindExec || kind == config.ToolKindNetwork || kind == config.ToolKindUnknown
}

func (e ExecutionContext) toolCapabilitySandboxed(tool schema.Tool) bool {
	kind := policy.KindForTool(tool)
	serverName := strings.TrimSpace(tool.Server)
	if serverName == "" && strings.Contains(tool.Name, "/") {
		serverName = strings.TrimSpace(strings.SplitN(tool.Name, "/", 2)[0])
	}
	for _, server := range e.MCPServers {
		if !strings.EqualFold(strings.TrimSpace(server.Name), serverName) {
			continue
		}
		isolation := strings.ToLower(strings.TrimSpace(server.Isolation))
		if isolation == "container" {
			return true
		}
		if isolation == "linux_netns" && kind == config.ToolKindNetwork {
			return true
		}
	}
	return false
}

func (e *ExecutionContext) RememberApprovedTool(toolName string) {
	if e == nil || toolName == "" {
		return
	}
	if e.approvedTools == nil {
		e.approvedTools = make(map[string]struct{})
	}
	e.approvedTools[approvalScopeKey("*", toolName)] = struct{}{}
}

func (e *ExecutionContext) RememberApprovedToolScope(kind, toolName string) {
	if e == nil || kind == "" || toolName == "" {
		return
	}
	if e.approvedTools == nil {
		e.approvedTools = make(map[string]struct{})
	}
	e.approvedTools[approvalScopeKey(kind, toolName)] = struct{}{}
}

func (e ExecutionContext) hasRememberedApproval(tool schema.Tool) bool {
	if tool.Name == "" || e.WorkspaceRoot == "" {
		return false
	}
	kind := string(policy.KindForTool(tool))
	if _, ok := e.approvedTools[approvalScopeKey(kind, tool.Name)]; ok {
		return true
	}
	_, ok := e.approvedTools[approvalScopeKey("*", tool.Name)]
	return ok
}

func (e ExecutionContext) approvalResult(call schema.ToolCall, tool schema.Tool) schema.ToolResult {
	result := schema.ToolResult{
		CallID:    call.ID,
		ToolName:  tool.Name,
		Content:   fmt.Sprintf("tool %s requires approval before execution", tool.Name),
		Suspended: true,
	}
	e.annotateResult(&result, tool)
	return result
}

func (e ExecutionContext) validationErrorResult(call schema.ToolCall, err error) schema.ToolResult {
	content := fmt.Sprintf("tool call %s was not executed: %s", call.Name, err)
	return schema.ToolResult{
		CallID:   call.ID,
		ToolName: call.Name,
		Content:  content,
		IsError:  true,
		Denied:   isPolicyDenial(err),
	}
}

func (e ExecutionContext) recordAudit(entry schema.AuditEntry) {
	if e.Audit == nil {
		return
	}
	if entry.AgentID == "" {
		entry.AgentID = e.AgentID
	}
	e.Audit.Record(entry)
}

func (e ExecutionContext) emitApproval(handler func(event schema.StreamEvent) error, call schema.ToolCall, tool schema.Tool) error {
	if handler == nil || !e.requiresApproval(tool) {
		return nil
	}
	return handler(schema.StreamEvent{Type: schema.StreamEventApproval, ToolName: tool.Name, ToolCallID: call.ID, ArgumentsSummary: summarizeToolArguments(call.Arguments), Content: "approval required", AgentID: e.AgentID, NeedsAction: true})
}

func (e ExecutionContext) toolKind(tool schema.Tool) string {
	return string(policy.KindForTool(tool))
}

func (e ExecutionContext) annotateResult(result *schema.ToolResult, tool schema.Tool) {
	if result == nil {
		return
	}
	if result.ToolName == "" {
		result.ToolName = tool.Name
	}
}

func approvalScopeKey(kind, toolName string) string {
	return kind + "\x00" + toolName
}

func isPolicyDenial(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "not allowed") ||
		strings.Contains(text, "policy denies") ||
		strings.Contains(text, "tool_risk_policy rejects") ||
		strings.Contains(text, "reject_unsandboxed_risky_tools")
}
