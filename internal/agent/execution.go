package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/commandpolicy"
	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/memory"
	"github.com/FyMatt/GoFlow-Agent/internal/nativesandbox"
	"github.com/FyMatt/GoFlow-Agent/internal/policy"
	"github.com/FyMatt/GoFlow-Agent/internal/runtime"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

// ExecutionContext provides execution metadata for one run.
type ExecutionContext struct {
	AgentID                       string
	AgentRunID                    string
	Profile                       config.AgentProfile
	Tools                         []schema.Tool
	Audit                         *runtime.AuditLogger
	WorkspaceRoot                 string
	TargetID                      string
	MemoryStore                   *memory.Store
	MemoryTaskID                  string
	RiskPolicy                    config.ToolRiskPolicyConfig
	MCPServers                    []config.MCPServerRef
	Session                       *session.State
	WorkflowRunID                 string
	WorkflowName                  string
	WorkflowStage                 string
	IdempotentToolCalls           bool
	ApprovedPlanScope             bool
	RunPermissionMode             string
	ChangeSliceEnforcement        bool
	ChangeSlices                  []session.WorkflowChangeSliceSnapshot
	ApprovedTaskScope             bool
	NativeSandboxReport           *nativesandbox.Report
	ToolWorkspaceRoot             string
	ToolWorkspaceScope            schema.TaskWorkspaceScope
	UntrustedInstructionsObserved bool
	readResultCache               *ordinaryAgentReadCache
}

func newExecutionContext(agentID string, profile config.AgentProfile, tools []schema.Tool, audit *runtime.AuditLogger, workspaceRoot string) ExecutionContext {
	if strings.TrimSpace(workspaceRoot) == "" {
		if current, err := os.Getwd(); err == nil {
			workspaceRoot = current
		}
	}
	return ExecutionContext{AgentID: agentID, Profile: profile, Tools: tools, Audit: audit, WorkspaceRoot: workspaceRoot, TargetID: session.LocalTargetID, readResultCache: newOrdinaryAgentReadCache()}
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
	if err := e.validateExecutionTargetCall(call, tool); err != nil {
		return schema.Tool{}, err
	}
	if err := e.validateChangeSliceToolCall(call, tool); err != nil {
		return schema.Tool{}, err
	}
	if err := e.rejectExistingAcceptanceTestRewrite(call, tool); err != nil {
		return schema.Tool{}, err
	}
	return tool, nil
}

func normalizeOptionalToolArguments(call schema.ToolCall) schema.ToolCall {
	name := strings.ToLower(strings.TrimSpace(call.Name))
	if slash := strings.LastIndex(name, "/"); slash >= 0 {
		name = name[slash+1:]
	}
	if name == "run_command" && len(call.Arguments) > 0 {
		call.Arguments = commandpolicy.NormalizeRunCommandArguments(call.Arguments)
		return call
	}
	if name != "write_file" || len(call.Arguments) == 0 {
		return call
	}
	var arguments map[string]json.RawMessage
	if err := json.Unmarshal(call.Arguments, &arguments); err != nil {
		return call
	}
	raw, ok := arguments["expected_sha256"]
	if !ok {
		return call
	}
	var expected string
	if json.Unmarshal(raw, &expected) == nil && validSHA256Hex(expected) {
		return call
	}
	delete(arguments, "expected_sha256")
	if normalized, err := json.Marshal(arguments); err == nil {
		call.Arguments = normalized
	}
	return call
}

func validSHA256Hex(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')) {
			return false
		}
	}
	return true
}

func (e ExecutionContext) validateExecutionTargetCall(call schema.ToolCall, tool schema.Tool) error {
	serverName := strings.TrimSpace(tool.Server)
	if serverName == "" {
		serverName, _, _ = strings.Cut(strings.TrimSpace(call.Name), "/")
	}
	hostScoped := false
	for _, server := range e.MCPServers {
		if strings.EqualFold(strings.TrimSpace(server.Name), serverName) && strings.EqualFold(strings.TrimSpace(server.TargetScope), "host") {
			hostScoped = true
			break
		}
	}
	if !hostScoped {
		return nil
	}
	selected := normalizeExecutionTargetID(e.TargetID)
	var arguments map[string]any
	if len(call.Arguments) > 0 {
		if err := json.Unmarshal(call.Arguments, &arguments); err != nil {
			return fmt.Errorf("invalid host-scoped tool arguments: %w", err)
		}
	}
	value, supplied := arguments["target_id"]
	requested := strings.ToLower(strings.TrimSpace(fmt.Sprint(value)))
	if supplied && requested == "" {
		return fmt.Errorf("host-scoped tool target_id is empty")
	}
	if selected != session.LocalTargetID {
		if !supplied {
			return fmt.Errorf("host-scoped tool %s must pass task target_id %q", call.Name, selected)
		}
		if requested != selected {
			return fmt.Errorf("host-scoped tool target_id %q does not match task Target %q", requested, selected)
		}
		return nil
	}
	if supplied && requested != session.LocalTargetID {
		return fmt.Errorf("host-scoped tool target_id %q does not match task Target %q", requested, session.LocalTargetID)
	}
	return nil
}

func (e ExecutionContext) requiresApproval(call schema.ToolCall, tool schema.Tool) bool {
	if NormalizeRunPermissionMode(e.RunPermissionMode) == RunPermissionFull {
		return false
	}
	if NormalizeRunPermissionMode(e.RunPermissionMode) == RunPermissionWorkspace && e.workspacePermissionAllows(call, tool) {
		return false
	}
	if e.untrustedObservationRequiresApproval(tool) {
		return true
	}
	if managedHostChangeRequiresOneTimeApproval(call, tool) {
		return true
	}
	if e.approvedTaskScopeAllows(call, tool) {
		return false
	}
	if e.approvedPlanScopeAllows(call, tool) {
		return false
	}
	if e.callRequiresExplicitApproval(call, tool) {
		return true
	}
	if e.RiskPolicy.RequireApprovalForUnsandboxedRiskyTools && e.toolRequiresRiskApproval(tool) {
		return true
	}
	if e.Profile.ToolPolicy != config.ToolPolicyConfirm {
		return false
	}
	if policy.KindForTool(tool) == config.ToolKindRead {
		return false
	}
	return !e.hasRememberedApproval(call, tool)
}

func (e ExecutionContext) workspacePermissionAllows(call schema.ToolCall, tool schema.Tool) bool {
	if normalizeExecutionTargetID(e.TargetID) != session.LocalTargetID || strings.TrimSpace(e.WorkspaceRoot) == "" {
		return false
	}
	switch policy.KindForTool(tool) {
	case config.ToolKindRead:
		return true
	case config.ToolKindWrite:
		return taskScopedWorkspaceWrite(call, tool)
	case config.ToolKindExec:
		if !taskScopedCommandTool(call, tool) {
			return false
		}
		switch commandpolicy.ClassifyArguments(call.Arguments).Class {
		case commandpolicy.ClassInspection, commandpolicy.ClassTest, commandpolicy.ClassBuild, commandpolicy.ClassLint, commandpolicy.ClassFormat:
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func (e ExecutionContext) approvedTaskScopeAllows(call schema.ToolCall, tool schema.Tool) bool {
	if !e.ApprovedTaskScope || strings.TrimSpace(e.AgentRunID) == "" || strings.TrimSpace(e.WorkspaceRoot) == "" {
		return false
	}
	if strings.TrimSpace(e.WorkflowName) != "" || strings.TrimSpace(e.WorkflowRunID) != "" {
		return false
	}
	if normalizeExecutionTargetID(e.TargetID) != session.LocalTargetID {
		return false
	}
	switch policy.KindForTool(tool) {
	case config.ToolKindWrite:
		return taskScopedWorkspaceWrite(call, tool)
	case config.ToolKindExec:
		if !taskScopedCommandTool(call, tool) {
			return false
		}
		classification := commandpolicy.ClassifyArguments(call.Arguments)
		switch classification.Class {
		case commandpolicy.ClassInspection,
			commandpolicy.ClassTest,
			commandpolicy.ClassBuild,
			commandpolicy.ClassLint,
			commandpolicy.ClassFormat:
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func taskScopedWorkspaceWrite(call schema.ToolCall, tool schema.Tool) bool {
	if !strings.EqualFold(executionToolServerName(call, tool), "file_tools") {
		return false
	}
	for _, name := range []string{"write_file", "apply_patch", "replace_text", "mkdir"} {
		if toolNameEquivalent(call.Name, name) || toolNameEquivalent(tool.Name, name) {
			return true
		}
	}
	return false
}

func taskScopedCommandTool(call schema.ToolCall, tool schema.Tool) bool {
	return strings.EqualFold(executionToolServerName(call, tool), "command_tools") &&
		(toolNameEquivalent(call.Name, "run_command") || toolNameEquivalent(tool.Name, "run_command"))
}

func executionToolServerName(call schema.ToolCall, tool schema.Tool) string {
	if server := strings.TrimSpace(tool.Server); server != "" {
		return server
	}
	for _, value := range []string{call.Name, tool.Name} {
		server, _, ok := strings.Cut(strings.TrimSpace(value), "/")
		if ok && strings.TrimSpace(server) != "" {
			return strings.TrimSpace(server)
		}
	}
	return ""
}

func (e ExecutionContext) approvedPlanScopeAllows(call schema.ToolCall, tool schema.Tool) bool {
	if !e.ApprovedPlanScope || strings.TrimSpace(e.WorkflowName) == "" || strings.TrimSpace(e.WorkflowStage) == "" || strings.TrimSpace(e.WorkspaceRoot) == "" {
		return false
	}
	if normalizeExecutionTargetID(e.TargetID) != session.LocalTargetID {
		return false
	}
	switch policy.KindForTool(tool) {
	case config.ToolKindWrite:
		return taskScopedWorkspaceWrite(call, tool)
	case config.ToolKindExec:
		if !toolNameEquivalent(call.Name, "run_command") && !toolNameEquivalent(tool.Name, "run_command") {
			return false
		}
		classification := commandpolicy.ClassifyArguments(call.Arguments)
		switch classification.Class {
		case commandpolicy.ClassInspection,
			commandpolicy.ClassTest,
			commandpolicy.ClassBuild,
			commandpolicy.ClassLint,
			commandpolicy.ClassFormat:
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func (e ExecutionContext) callRequiresExplicitApproval(call schema.ToolCall, tool schema.Tool) bool {
	if !toolNameEquivalent(call.Name, "run_command") && !toolNameEquivalent(tool.Name, "run_command") {
		return false
	}
	if len(call.Arguments) == 0 {
		return false
	}
	return commandpolicy.ClassifyArguments(call.Arguments).RequiresExplicitApproval
}

func (e ExecutionContext) canRememberCallApproval(call schema.ToolCall, tool schema.Tool) bool {
	if managedHostChangeRequiresOneTimeApproval(call, tool) {
		return false
	}
	return !e.callRequiresExplicitApproval(call, tool) && e.canRememberApproval(tool)
}

func managedHostChangeRequiresOneTimeApproval(call schema.ToolCall, tool schema.Tool) bool {
	for _, name := range []string{"set_managed_json_value", "restore_managed_json_change"} {
		if toolNameEquivalent(call.Name, name) || toolNameEquivalent(tool.Name, name) {
			return true
		}
	}
	return false
}

func (e ExecutionContext) canRememberApproval(tool schema.Tool) bool {
	if e.untrustedObservationRequiresApproval(tool) {
		return false
	}
	if e.RiskPolicy.DisableRememberForUnsandboxedRiskyTools && e.toolRequiresRiskApproval(tool) {
		return false
	}
	return true
}

func (e ExecutionContext) untrustedObservationRequiresApproval(tool schema.Tool) bool {
	if !e.UntrustedInstructionsObserved {
		return false
	}
	switch policy.KindForTool(tool) {
	case config.ToolKindWrite, config.ToolKindExec, config.ToolKindNetwork, config.ToolKindUnknown:
		return true
	default:
		return false
	}
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
	return fmt.Errorf("tool_risk_policy rejects unsandboxed risky tool %s (kind=%s); use isolation: auto or native_strong, use linux_netns for network-only tools, or disable reject_unsandboxed_risky_tools", name, kind)
}

func (e ExecutionContext) rejectByNativeSandboxPolicy(call schema.ToolCall, tool schema.Tool) error {
	if normalizeExecutionTargetID(e.TargetID) != session.LocalTargetID || !taskScopedCommandTool(call, tool) {
		return nil
	}
	classification := commandpolicy.ClassifyArguments(call.Arguments)
	if classification.Class != commandpolicy.ClassUnknown {
		return nil
	}
	report := nativesandbox.Probe()
	if e.NativeSandboxReport != nil {
		report = *e.NativeSandboxReport
	}
	if report.StrongReady {
		return nil
	}
	reason := strings.TrimSpace(report.RefusalReason)
	if reason == "" {
		reason = "当前系统的原生强沙箱尚未完整启用"
	}
	return fmt.Errorf("原生执行策略拒绝未知命令在真实工作区运行：%s", reason)
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
		if isolation == "native_strong" {
			return true
		}
		if isolation == "linux_netns" && kind == config.ToolKindNetwork {
			return true
		}
	}
	return false
}

func (e ExecutionContext) approvalResult(call schema.ToolCall, tool schema.Tool) schema.ToolResult {
	content := fmt.Sprintf("tool %s requires approval before execution", tool.Name)
	if e.untrustedObservationRequiresApproval(tool) {
		content = fmt.Sprintf("tool %s requires a fresh one-time approval because earlier untrusted tool data contained instruction-like content", tool.Name)
	}
	result := schema.ToolResult{
		CallID:    call.ID,
		ToolName:  tool.Name,
		Content:   content,
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
	if handler == nil || !e.requiresApproval(call, tool) {
		return nil
	}
	content := "approval required"
	reason := "tool_policy"
	if e.untrustedObservationRequiresApproval(tool) {
		content = "检测到不可信工具数据中的疑似指令，必须重新审批本次高风险操作"
		reason = "untrusted_tool_data"
	}
	return handler(schema.StreamEvent{Type: schema.StreamEventApproval, ToolName: tool.Name, ToolCallID: call.ID, ArgumentsSummary: summarizeToolArguments(call.Arguments), ArgumentsHash: toolArgumentsHash(call.Arguments), Content: content, AgentID: e.AgentID, NeedsAction: true, Reason: reason})
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

func (e ExecutionContext) recordFileReadResult(ctx context.Context, call schema.ToolCall, result schema.ToolResult) {
	if e.MemoryStore == nil || result.IsError || result.Denied || result.Suspended {
		return
	}
	if !toolNameEquivalent(call.Name, "read_file") &&
		!toolNameEquivalent(call.Name, "read_file_range") &&
		!toolNameEquivalent(result.ToolName, "read_file") &&
		!toolNameEquivalent(result.ToolName, "read_file_range") {
		return
	}
	path := toolCallStringArgument(call.Arguments, "path")
	if path == "" {
		return
	}
	_ = e.MemoryStore.MarkFilePathsUsedByTask(ctx, []string{path}, e.MemoryTaskID)
}

func toolCallStringArgument(arguments []byte, name string) string {
	name = strings.TrimSpace(name)
	if len(arguments) == 0 || name == "" {
		return ""
	}
	var values map[string]any
	if err := json.Unmarshal(arguments, &values); err != nil {
		return ""
	}
	if value, ok := values[name].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func isPolicyDenial(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "not allowed") ||
		strings.Contains(text, "policy denies") ||
		strings.Contains(text, "原生执行策略拒绝") ||
		strings.Contains(text, "tool_risk_policy rejects") ||
		strings.Contains(text, "reject_unsandboxed_risky_tools")
}
