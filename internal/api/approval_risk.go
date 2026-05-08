package api

import (
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func (s *Server) sessionSnapshotWithApprovalRisk() session.Snapshot {
	if s == nil || s.runtime == nil {
		return session.Snapshot{}
	}
	return s.snapshotWithApprovalRisk(s.runtime)
}

func (s *Server) snapshotWithApprovalRisk(source sessionSnapshotProvider) session.Snapshot {
	if source == nil {
		return session.Snapshot{}
	}
	snapshot := source.SessionSnapshot()
	s.annotateSessionApprovalRisk(&snapshot)
	return snapshot
}

func (s *Server) annotateSessionApprovalRisk(snapshot *session.Snapshot) {
	if snapshot == nil {
		return
	}
	snapshot.PendingApprovals = s.pendingApprovalsWithRisk(snapshot.PendingApprovals)
	snapshot.AgentRuns = s.agentRunsWithApprovalRisk(snapshot.AgentRuns)
	snapshot.WorkflowRuns = s.workflowRunsWithApprovalRisk(snapshot.WorkflowRuns, snapshot.PendingApprovals)
	snapshot.Workflow = s.workflowSnapshotWithApprovalRisk(snapshot.Workflow, snapshot.PendingApprovals)
}

func (s *Server) agentRunsWithApprovalRisk(runs []session.AgentRunSnapshot) []session.AgentRunSnapshot {
	if len(runs) == 0 {
		return nil
	}
	out := make([]session.AgentRunSnapshot, len(runs))
	for i, run := range runs {
		out[i] = s.agentRunWithApprovalRisk(run)
	}
	return out
}

func (s *Server) agentRunWithApprovalRisk(run session.AgentRunSnapshot) session.AgentRunSnapshot {
	run.Events = s.agentRunEventsWithRisk(run.Events)
	run.PendingApprovals = s.pendingApprovalsWithRisk(run.PendingApprovals)
	return run
}

func (s *Server) workflowRunsWithApprovalRisk(runs []session.WorkflowRunSnapshot, pending []session.PendingApprovalSnapshot) []session.WorkflowRunSnapshot {
	if len(runs) == 0 {
		return nil
	}
	out := make([]session.WorkflowRunSnapshot, len(runs))
	for i, run := range runs {
		out[i] = s.workflowRunWithApprovalRisk(run, pending)
	}
	return out
}

func (s *Server) workflowSnapshotWithApprovalRisk(workflow session.WorkflowSnapshot, pending []session.PendingApprovalSnapshot) session.WorkflowSnapshot {
	if workflow.PendingToolRisk != nil {
		risk := copySchemaToolRiskProfile(*workflow.PendingToolRisk)
		workflow.PendingToolRisk = &risk
	}
	if workflow.PendingToolRisk == nil {
		workflow.PendingToolRisk = s.workflowSnapshotPendingToolRisk(workflow, pending)
	}
	return workflow
}

func (s *Server) workflowRunWithApprovalRisk(run session.WorkflowRunSnapshot, pending []session.PendingApprovalSnapshot) session.WorkflowRunSnapshot {
	run.Events = s.workflowRunEventsWithRisk(run.Events)
	if run.PendingToolRisk != nil {
		risk := copySchemaToolRiskProfile(*run.PendingToolRisk)
		run.PendingToolRisk = &risk
	}
	if run.PendingToolRisk == nil {
		run.PendingToolRisk = s.workflowRunPendingToolRisk(run, pending)
	}
	return run
}

func copySchemaToolRiskProfile(profile schema.ToolRiskProfile) schema.ToolRiskProfile {
	profile.Capabilities = append([]string(nil), profile.Capabilities...)
	profile.SandboxFeatures = append([]string(nil), profile.SandboxFeatures...)
	profile.MissingSandboxFeatures = append([]string(nil), profile.MissingSandboxFeatures...)
	if profile.WindowsIsolation != nil {
		windowsIsolation := *profile.WindowsIsolation
		profile.WindowsIsolation = &windowsIsolation
	}
	profile.Warnings = append([]string(nil), profile.Warnings...)
	profile.Recommendations = append([]string(nil), profile.Recommendations...)
	return profile
}

func (s *Server) workflowSnapshotPendingToolRisk(workflow session.WorkflowSnapshot, pending []session.PendingApprovalSnapshot) *schema.ToolRiskProfile {
	toolName := strings.TrimSpace(workflow.PendingToolName)
	if strings.TrimSpace(workflow.PendingCallID) != "" {
		for _, item := range pending {
			if strings.TrimSpace(item.CallID) == strings.TrimSpace(workflow.PendingCallID) {
				if item.Risk != nil {
					return item.Risk
				}
				if strings.TrimSpace(item.ToolName) != "" {
					toolName = item.ToolName
				}
				break
			}
		}
	}
	if toolName == "" {
		return nil
	}
	return s.toolRiskProfileForName(toolName)
}

func (s *Server) streamHandlerWithApprovalRisk(handler func(schema.StreamEvent) error) func(schema.StreamEvent) error {
	return func(event schema.StreamEvent) error {
		event = s.streamEventWithRisk(event)
		if handler == nil {
			return nil
		}
		return handler(event)
	}
}

func (s *Server) streamEventWithRisk(event schema.StreamEvent) schema.StreamEvent {
	if event.Risk != nil {
		risk := copySchemaToolRiskProfile(*event.Risk)
		event.Risk = &risk
		return event
	}
	if strings.TrimSpace(event.ToolName) == "" {
		return event
	}
	if event.Type != schema.StreamEventApproval && event.Type != schema.StreamEventToolCall && event.Type != schema.StreamEventToolResult {
		return event
	}
	event.Risk = s.toolRiskProfileForName(event.ToolName)
	return event
}

func (s *Server) agentRunEventsWithRisk(events []session.AgentRunEventSnapshot) []session.AgentRunEventSnapshot {
	if len(events) == 0 {
		return nil
	}
	out := make([]session.AgentRunEventSnapshot, len(events))
	for i, event := range events {
		out[i] = event
		if out[i].Risk != nil {
			risk := copySchemaToolRiskProfile(*out[i].Risk)
			out[i].Risk = &risk
			continue
		}
		if isToolRiskEventType(out[i].Type) && strings.TrimSpace(out[i].ToolName) != "" {
			out[i].Risk = s.toolRiskProfileForName(out[i].ToolName)
		}
	}
	return out
}

func (s *Server) workflowRunEventsWithRisk(events []session.WorkflowRunEventSnapshot) []session.WorkflowRunEventSnapshot {
	if len(events) == 0 {
		return nil
	}
	out := make([]session.WorkflowRunEventSnapshot, len(events))
	for i, event := range events {
		out[i] = event
		if out[i].Risk != nil {
			risk := copySchemaToolRiskProfile(*out[i].Risk)
			out[i].Risk = &risk
			continue
		}
		if isToolRiskEventType(out[i].Type) && strings.TrimSpace(out[i].ToolName) != "" {
			out[i].Risk = s.toolRiskProfileForName(out[i].ToolName)
		}
	}
	return out
}

func isToolRiskEventType(eventType string) bool {
	switch strings.TrimSpace(eventType) {
	case string(schema.StreamEventApproval), string(schema.StreamEventToolCall), string(schema.StreamEventToolResult):
		return true
	default:
		return false
	}
}

func (s *Server) pendingApprovalsWithRisk(items []session.PendingApprovalSnapshot) []session.PendingApprovalSnapshot {
	if len(items) == 0 {
		return nil
	}
	out := make([]session.PendingApprovalSnapshot, len(items))
	for i, item := range items {
		out[i] = item
		if out[i].Risk != nil {
			risk := copySchemaToolRiskProfile(*out[i].Risk)
			out[i].Risk = &risk
		}
		if out[i].Risk == nil {
			out[i].Risk = s.toolRiskProfileForName(item.ToolName)
		}
	}
	return out
}

func (s *Server) workflowRunPendingToolRisk(run session.WorkflowRunSnapshot, pending []session.PendingApprovalSnapshot) *schema.ToolRiskProfile {
	toolName := strings.TrimSpace(run.PendingToolName)
	if strings.TrimSpace(run.PendingCallID) != "" {
		for _, item := range pending {
			if strings.TrimSpace(item.CallID) == strings.TrimSpace(run.PendingCallID) {
				if item.Risk != nil {
					return item.Risk
				}
				if strings.TrimSpace(item.ToolName) != "" {
					toolName = item.ToolName
				}
				break
			}
		}
	}
	if toolName == "" {
		return nil
	}
	return s.toolRiskProfileForName(toolName)
}
