package agent

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/policy"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

const rememberedApprovalTTL = 8 * time.Hour

func (e ExecutionContext) approvalScope(call schema.ToolCall, tool schema.Tool, createdAt, expiresAt time.Time) (session.ApprovalScope, error) {
	if strings.TrimSpace(e.WorkspaceRoot) == "" {
		return session.ApprovalScope{}, fmt.Errorf("remembered approval requires a workspace")
	}
	kind := string(policy.KindForTool(tool))
	if strings.TrimSpace(kind) == "" {
		kind = "unknown"
	}
	targetID := strings.ToLower(strings.TrimSpace(e.TargetID))
	if targetID == "" {
		targetID = session.LocalTargetID
	}
	return session.NewApprovalScope(session.ApprovalScopeInput{
		Workspace:     e.WorkspaceRoot,
		Target:        targetID,
		AgentID:       e.AgentID,
		WorkflowName:  e.WorkflowName,
		WorkflowStage: e.WorkflowStage,
		ToolKind:      kind,
		ToolName:      tool.Name,
		ResourcePins:  e.approvalResourcePins(),
		Arguments:     call.Arguments,
	}, createdAt, expiresAt)
}

func (e ExecutionContext) approvalResourcePins() []session.ResourcePin {
	if e.Session == nil {
		return nil
	}
	if runID := strings.TrimSpace(e.WorkflowRunID); runID != "" {
		if run, ok := e.Session.WorkflowRun(runID); ok {
			return session.CopyResourcePins(run.ResourcePins)
		}
	}
	if runID := strings.TrimSpace(e.AgentRunID); runID != "" {
		if run, ok := e.Session.AgentRun(runID); ok {
			return session.CopyResourcePins(run.ResourcePins)
		}
	}
	workflow := e.Session.Snapshot().Workflow
	if strings.TrimSpace(e.WorkflowName) != "" && strings.EqualFold(strings.TrimSpace(workflow.Name), strings.TrimSpace(e.WorkflowName)) {
		return session.CopyResourcePins(workflow.ResourcePins)
	}
	return nil
}

func (e ExecutionContext) hasRememberedApproval(call schema.ToolCall, tool schema.Tool) bool {
	if e.Session == nil {
		return false
	}
	now := time.Now().UTC()
	scope, err := e.approvalScope(call, tool, now, now.Add(rememberedApprovalTTL))
	if err != nil {
		return false
	}
	return e.Session.HasApprovalScope(scope.Fingerprint, now)
}

func pendingApprovalExecutionContext(r *Runtime, pending pendingApproval) ExecutionContext {
	workflowName := strings.TrimSpace(pending.toolOperations.Workflow)
	if workflowName == "" {
		workflowName = strings.TrimSpace(pending.workflow)
	}
	workflowStage := strings.TrimSpace(pending.toolOperations.Stage)
	if workflowStage == "" {
		workflowStage = strings.TrimSpace(string(pending.stage))
	}
	workspace := workspaceRoot(r)
	if strings.TrimSpace(workspace) == "" {
		workspace, _ = os.Getwd()
	}
	targetID := session.LocalTargetID
	if r != nil && r.session != nil {
		if runID := strings.TrimSpace(pending.agentRunID); runID != "" {
			if run, ok := r.session.AgentRun(runID); ok {
				targetID = normalizeExecutionTargetID(run.TargetID)
			}
		} else if runID := strings.TrimSpace(pending.toolOperations.RunID); runID != "" {
			if run, ok := r.session.WorkflowRun(runID); ok {
				targetID = normalizeExecutionTargetID(run.TargetID)
			}
		}
	}
	return ExecutionContext{
		AgentID:             pending.agent,
		AgentRunID:          pending.agentRunID,
		WorkspaceRoot:       workspace,
		TargetID:            targetID,
		Session:             r.session,
		WorkflowRunID:       pending.toolOperations.RunID,
		WorkflowName:        workflowName,
		WorkflowStage:       workflowStage,
		IdempotentToolCalls: pending.toolOperations.Enabled,
	}
}

func normalizeExecutionTargetID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return session.LocalTargetID
	}
	return value
}

func (r *Runtime) approvalScopeForPending(pending pendingApproval, now time.Time) (session.ApprovalScope, error) {
	if r == nil || r.session == nil {
		return session.ApprovalScope{}, fmt.Errorf("remembered approval requires session state")
	}
	return pendingApprovalExecutionContext(r, pending).approvalScope(pending.call, pending.tool, now, now.Add(rememberedApprovalTTL))
}

func (r *Runtime) rememberPendingApprovalScope(pending pendingApproval) (session.ApprovalScope, error) {
	now := time.Now().UTC()
	scope, err := r.approvalScopeForPending(pending, now)
	if err != nil {
		return session.ApprovalScope{}, err
	}
	r.session.RememberApprovalScope(scope)
	return scope, nil
}
