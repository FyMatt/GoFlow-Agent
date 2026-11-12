package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

const agentTaskApprovalScopeVersion = 1

// CanApproveAgentTaskScope reports whether one pending ordinary tool call can
// establish the bounded local workspace scope used for the rest of that run.
func (r *Runtime) CanApproveAgentTaskScope(callID string) (bool, string) {
	if r == nil || strings.TrimSpace(callID) == "" {
		return false, "pending approval is not available"
	}
	pending, ok := r.PendingApproval(callID)
	if !ok {
		return false, "pending approval is not available"
	}
	if _, err := r.agentTaskApprovalScopeForPending(pending); err != nil {
		return false, err.Error()
	}
	return true, "Approve workspace writes and validation commands only for this agent run; destructive, network, host, remote, shell, dependency-install, and unknown operations still require separate approval."
}

// ApproveToolCallForAgentTask grants a durable, run-scoped local workspace
// authorization and then executes the currently pending call. It is distinct
// from remembered tool approval: it cannot cross run, workspace, agent, or
// target boundaries and is inactive as soon as the run becomes terminal.
func (r *Runtime) ApproveToolCallForAgentTask(ctx context.Context, id string) (schema.ToolResult, error) {
	if r == nil || r.session == nil {
		return schema.ToolResult{}, fmt.Errorf("agent task approval requires session state")
	}
	pending, ok := r.PendingApproval(id)
	if !ok {
		return schema.ToolResult{}, fmt.Errorf("pending approval not found: %s", id)
	}
	if err := r.rejectToolApprovalByRiskPolicy(pending.tool); err != nil {
		return schema.ToolResult{}, err
	}
	scope, err := r.agentTaskApprovalScopeForPending(pending)
	if err != nil {
		return schema.ToolResult{}, err
	}
	if _, ok := r.session.SetAgentRunTaskApproval(pending.agentRunID, scope); !ok {
		return schema.ToolResult{}, fmt.Errorf("agent run %s is no longer active", pending.agentRunID)
	}
	if err := r.session.PersistConfigured(); err != nil {
		r.session.ClearAgentRunTaskApproval(pending.agentRunID)
		return schema.ToolResult{}, fmt.Errorf("persist agent task approval: %w", err)
	}
	if r.audit != nil {
		r.audit.Record(schema.AuditEntry{
			Type:     "task_approval_scope",
			AgentID:  pending.agent,
			ToolName: pending.tool.Name,
			Outcome:  "approved",
			Detail:   "current agent run may perform bounded workspace writes and validation commands",
		})
	}
	return r.ApproveToolCall(ctx, id)
}

func (r *Runtime) agentTaskApprovalScopeForPending(pending pendingApproval) (session.AgentTaskApprovalScopeSnapshot, error) {
	runID := strings.TrimSpace(pending.agentRunID)
	if runID == "" {
		return session.AgentTaskApprovalScopeSnapshot{}, fmt.Errorf("task-scoped approval requires an ordinary agent run")
	}
	if strings.TrimSpace(pending.workflow) != "" || strings.TrimSpace(pending.toolOperations.Workflow) != "" || strings.TrimSpace(pending.toolOperations.RunID) != "" {
		return session.AgentTaskApprovalScopeSnapshot{}, fmt.Errorf("workflow approvals use their stage scope instead of an agent task scope")
	}
	run, ok := r.session.AgentRun(runID)
	if !ok || agentRunStatusTerminalForTaskApproval(run.Status) {
		return session.AgentTaskApprovalScopeSnapshot{}, fmt.Errorf("agent run %s is no longer active", runID)
	}
	execCtx := pendingApprovalExecutionContext(r, pending)
	execCtx.AgentRunID = runID
	execCtx.ApprovedTaskScope = true
	if normalizeExecutionTargetID(execCtx.TargetID) != session.LocalTargetID {
		return session.AgentTaskApprovalScopeSnapshot{}, fmt.Errorf("task-scoped workspace approval is limited to the local target")
	}
	if !execCtx.approvedTaskScopeAllows(pending.call, pending.tool) {
		return session.AgentTaskApprovalScopeSnapshot{}, fmt.Errorf("this tool call is outside the bounded workspace-write and validation-command task scope")
	}
	workspace, err := canonicalTaskApprovalWorkspace(execCtx.WorkspaceRoot)
	if err != nil {
		return session.AgentTaskApprovalScopeSnapshot{}, err
	}
	return session.AgentTaskApprovalScopeSnapshot{
		Version:                 agentTaskApprovalScopeVersion,
		AgentID:                 strings.TrimSpace(pending.agent),
		WorkspaceRoot:           workspace,
		TargetID:                session.LocalTargetID,
		AllowWorkspaceWrites:    true,
		AllowValidationCommands: true,
		GrantedByCallID:         strings.TrimSpace(pending.call.ID),
		GrantedAt:               time.Now().UTC().Format(time.RFC3339Nano),
	}, nil
}

func (r *Runtime) applyAgentTaskApprovalScope(execCtx *ExecutionContext) {
	if r == nil || r.session == nil || execCtx == nil || strings.TrimSpace(execCtx.AgentRunID) == "" {
		return
	}
	run, ok := r.session.AgentRun(execCtx.AgentRunID)
	if !ok || agentRunStatusTerminalForTaskApproval(run.Status) || run.TaskApproval == nil {
		return
	}
	scope := run.TaskApproval
	if scope.Version != agentTaskApprovalScopeVersion || !scope.AllowWorkspaceWrites || !scope.AllowValidationCommands {
		return
	}
	if !strings.EqualFold(strings.TrimSpace(scope.AgentID), strings.TrimSpace(execCtx.AgentID)) {
		return
	}
	if normalizeExecutionTargetID(scope.TargetID) != session.LocalTargetID || normalizeExecutionTargetID(execCtx.TargetID) != session.LocalTargetID {
		return
	}
	workspace, err := canonicalTaskApprovalWorkspace(execCtx.WorkspaceRoot)
	if err != nil || !sameTaskApprovalWorkspace(scope.WorkspaceRoot, workspace) {
		return
	}
	if strings.TrimSpace(execCtx.WorkflowName) != "" || strings.TrimSpace(execCtx.WorkflowRunID) != "" {
		return
	}
	execCtx.ApprovedTaskScope = true
}

func canonicalTaskApprovalWorkspace(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("task-scoped approval requires a workspace")
	}
	absolute, err := filepath.Abs(filepath.Clean(value))
	if err != nil {
		return "", fmt.Errorf("resolve task approval workspace: %w", err)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(absolute); resolveErr == nil {
		absolute, err = filepath.Abs(filepath.Clean(resolved))
		if err != nil {
			return "", fmt.Errorf("resolve task approval workspace: %w", err)
		}
	}
	return absolute, nil
}

func sameTaskApprovalWorkspace(left, right string) bool {
	left = filepath.Clean(strings.TrimSpace(left))
	right = filepath.Clean(strings.TrimSpace(right))
	if left == right {
		return true
	}
	if filepath.VolumeName(left) != "" || filepath.VolumeName(right) != "" {
		return strings.EqualFold(left, right)
	}
	return false
}

func agentRunStatusTerminalForTaskApproval(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "succeeded", "failed", "cancelled", "canceled", "denied", "blocked", "action_failed":
		return true
	default:
		return false
	}
}
