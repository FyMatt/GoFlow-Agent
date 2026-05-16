package session

import (
	"strings"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

const (
	maxPersistedSessionArtifactContentBytes = 16 * 1024
	sessionCompactionSuffix                 = "\n\n[GoFlow: content truncated for session storage]"
)

func compactSnapshotForPersist(snapshot Snapshot) Snapshot {
	snapshot.Workflow = compactWorkflowSnapshotForPersist(snapshot.Workflow)
	snapshot.AgentRuns = compactAgentRunsForPersist(snapshot.AgentRuns)
	snapshot.WorkflowRuns = compactWorkflowRunsForPersist(snapshot.WorkflowRuns)
	snapshot.Artifacts = compactSessionArtifactsForPersist(snapshot.Artifacts)
	snapshot.PendingApprovals = compactPendingApprovalsForPersist(snapshot.PendingApprovals)
	snapshot.Messages = compactCollaborationMessagesForPersist(snapshot.Messages)
	snapshot.Blackboard = compactBlackboardEntriesForPersist(snapshot.Blackboard)
	return snapshot
}

func compactAgentRunsForPersist(runs []AgentRunSnapshot) []AgentRunSnapshot {
	if len(runs) == 0 {
		return nil
	}
	out := make([]AgentRunSnapshot, len(runs))
	for i, run := range runs {
		out[i] = compactAgentRunForPersist(run)
	}
	return out
}

func compactAgentRunForPersist(run AgentRunSnapshot) AgentRunSnapshot {
	run.Request = compactSessionText(run.Request, maxAgentRunText)
	run.Output = compactSessionText(run.Output, maxAgentRunText)
	run.Error = compactSessionText(run.Error, maxAgentRunText)
	if run.Result != nil {
		result := compactAgentResultForPersist(copyAgentResult(*run.Result))
		run.Result = &result
	}
	if run.ResumeContext != nil {
		context := compactAgentRunResumeContextForPersist(copyAgentRunResumeContext(*run.ResumeContext))
		run.ResumeContext = &context
	}
	run.Artifacts = compactAgentArtifactsForPersist(run.Artifacts)
	run.Events = append([]AgentRunEventSnapshot(nil), run.Events...)
	for i := range run.Events {
		run.Events[i].Content = compactSessionText(run.Events[i].Content, maxAgentRunText)
		run.Events[i].ArgumentsSummary = compactSessionText(run.Events[i].ArgumentsSummary, maxAgentRunText)
	}
	run.PendingApprovals = copyPendingApprovalSnapshots(run.PendingApprovals)
	for i := range run.PendingApprovals {
		run.PendingApprovals[i].ArgumentsSummary = compactSessionText(run.PendingApprovals[i].ArgumentsSummary, maxAgentRunText)
		run.PendingApprovals[i].Arguments = compactSessionText(run.PendingApprovals[i].Arguments, maxAgentRunText)
		run.PendingApprovals[i].Request = compactSessionText(run.PendingApprovals[i].Request, maxAgentRunText)
		run.PendingApprovals[i].CompletedSummary = compactSessionText(run.PendingApprovals[i].CompletedSummary, maxAgentRunText)
	}
	return run
}

func compactAgentArtifactsForPersist(artifacts []AgentRunArtifactSnapshot) []AgentRunArtifactSnapshot {
	if len(artifacts) == 0 {
		return nil
	}
	out := make([]AgentRunArtifactSnapshot, len(artifacts))
	for i, artifact := range artifacts {
		out[i] = artifact
		out[i].Summary = compactSessionText(artifact.Summary, maxSessionArtifactSummaryBytes)
		if len([]byte(out[i].Content)) > maxPersistedSessionArtifactContentBytes {
			out[i].Content = compactSessionText(out[i].Content, maxPersistedSessionArtifactContentBytes)
			out[i].Truncated = true
		}
		out[i].StoredBytes = len([]byte(out[i].Content))
	}
	return out
}

func compactAgentRunResumeContextForPersist(context AgentRunResumeContextSnapshot) AgentRunResumeContextSnapshot {
	context.SystemPrompt = compactSessionText(context.SystemPrompt, maxAgentRunText)
	for i := range context.Messages {
		context.Messages[i].Content = compactSessionText(context.Messages[i].Content, maxAgentRunText)
	}
	for i := range context.CollectedResults {
		context.CollectedResults[i].Content = compactSessionText(context.CollectedResults[i].Content, maxAgentRunText)
	}
	return context
}

func compactWorkflowRunsForPersist(runs []WorkflowRunSnapshot) []WorkflowRunSnapshot {
	if len(runs) == 0 {
		return nil
	}
	out := make([]WorkflowRunSnapshot, len(runs))
	for i, run := range runs {
		out[i] = compactWorkflowRunForPersist(run)
	}
	return out
}

func compactWorkflowRunForPersist(run WorkflowRunSnapshot) WorkflowRunSnapshot {
	run.Request = compactSessionText(run.Request, maxWorkflowRunText)
	run.Summary = compactSessionText(run.Summary, maxWorkflowRunText)
	run.ApprovalPrompt = compactSessionText(run.ApprovalPrompt, maxWorkflowRunText)
	run.PendingArgs = compactSessionText(run.PendingArgs, maxWorkflowRunText)
	run.PendingArgsSummary = compactSessionText(run.PendingArgsSummary, maxWorkflowRunText)
	run.PendingFields = copyWorkflowInputFields(run.PendingFields)
	run.CompletedStages = compactWorkflowStagesForPersist(run.CompletedStages)
	run.Artifacts = compactWorkflowArtifactsForPersist(run.Artifacts)
	run.Events = append([]WorkflowRunEventSnapshot(nil), run.Events...)
	for i := range run.Events {
		run.Events[i].Content = compactSessionText(run.Events[i].Content, maxWorkflowRunText)
		run.Events[i].ArgumentsSummary = compactSessionText(run.Events[i].ArgumentsSummary, maxWorkflowRunText)
	}
	return run
}

func compactWorkflowSnapshotForPersist(snapshot WorkflowSnapshot) WorkflowSnapshot {
	snapshot.Request = compactSessionText(snapshot.Request, maxWorkflowRunText)
	snapshot.Summary = compactSessionText(snapshot.Summary, maxWorkflowRunText)
	snapshot.LastApproval = compactSessionText(snapshot.LastApproval, maxWorkflowRunText)
	snapshot.PendingArguments = compactSessionText(snapshot.PendingArguments, maxWorkflowRunText)
	snapshot.PendingArgumentsSummary = compactSessionText(snapshot.PendingArgumentsSummary, maxWorkflowRunText)
	return snapshot
}

func compactPendingApprovalsForPersist(items []PendingApprovalSnapshot) []PendingApprovalSnapshot {
	if len(items) == 0 {
		return nil
	}
	out := copyPendingApprovalSnapshots(items)
	for i := range out {
		out[i].ArgumentsSummary = compactSessionText(out[i].ArgumentsSummary, maxAgentRunText)
		out[i].Arguments = compactSessionText(out[i].Arguments, maxAgentRunText)
		out[i].Request = compactSessionText(out[i].Request, maxAgentRunText)
		out[i].CompletedSummary = compactSessionText(out[i].CompletedSummary, maxAgentRunText)
	}
	return out
}

func compactWorkflowStagesForPersist(stages []WorkflowRunStageSnapshot) []WorkflowRunStageSnapshot {
	if len(stages) == 0 {
		return nil
	}
	out := make([]WorkflowRunStageSnapshot, len(stages))
	for i, stage := range stages {
		out[i] = stage
		out[i].Inputs = copyWorkflowRunStringMap(stage.Inputs)
		out[i].InputValues = compactWorkflowRunAnyMapForPersist(stage.InputValues)
		out[i].Outputs = copyWorkflowRunStringMap(stage.Outputs)
		out[i].OutputValues = compactWorkflowRunAnyMapForPersist(stage.OutputValues)
		out[i].Metadata = copyWorkflowRunStringMap(stage.Metadata)
		out[i].Summary = compactSessionText(stage.Summary, maxWorkflowRunText)
		out[i].Result = compactAgentResultForPersist(copyAgentResult(stage.Result))
		out[i].Artifacts = compactWorkflowArtifactsForPersist(stage.Artifacts)
		out[i].Acceptance = copyWorkflowRunAcceptance(stage.Acceptance)
		for key, value := range out[i].Outputs {
			out[i].Outputs[key] = compactSessionText(value, maxWorkflowRunText)
		}
	}
	return out
}

func compactAgentResultForPersist(result schema.AgentResult) schema.AgentResult {
	result.Output = compactSessionText(result.Output, maxWorkflowRunText)
	for i := range result.ToolResults {
		result.ToolResults[i].Content = compactSessionText(result.ToolResults[i].Content, maxWorkflowRunText)
	}
	for i := range result.Structured {
		result.Structured[i].Summary = compactSessionText(result.Structured[i].Summary, maxWorkflowRunText)
		result.Structured[i].Items = append([]string(nil), result.Structured[i].Items...)
		result.Structured[i].Items = compactStringSliceForPersist(result.Structured[i].Items, maxWorkflowRunText)
	}
	for i := range result.AuditTrail {
		result.AuditTrail[i].Detail = compactSessionText(result.AuditTrail[i].Detail, maxWorkflowRunText)
	}
	for i := range result.Findings {
		result.Findings[i].Summary = compactSessionText(result.Findings[i].Summary, maxWorkflowRunText)
		result.Findings[i].Remediation = compactSessionText(result.Findings[i].Remediation, maxWorkflowRunText)
	}
	for i := range result.Changes {
		result.Changes[i].Summary = compactSessionText(result.Changes[i].Summary, maxWorkflowRunText)
	}
	for i := range result.Verification {
		result.Verification[i].Detail = compactSessionText(result.Verification[i].Detail, maxWorkflowRunText)
	}
	return result
}

func compactWorkflowArtifactsForPersist(artifacts []WorkflowRunArtifact) []WorkflowRunArtifact {
	if len(artifacts) == 0 {
		return nil
	}
	out := make([]WorkflowRunArtifact, len(artifacts))
	for i, artifact := range artifacts {
		out[i] = artifact
		out[i].Summary = compactSessionText(artifact.Summary, maxWorkflowRunText)
		out[i].Content = compactSessionText(artifact.Content, maxWorkflowRunText)
	}
	return out
}

func compactWorkflowRunAnyMapForPersist(values map[string]any) map[string]any {
	if len(values) == 0 {
		return nil
	}
	copied := make(map[string]any, len(values))
	for key, value := range values {
		copied[key] = compactWorkflowRunAnyValueForPersist(value)
	}
	return copied
}

func compactWorkflowRunAnyValueForPersist(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return compactWorkflowRunAnyMapForPersist(typed)
	case []any:
		copied := make([]any, len(typed))
		for i, item := range typed {
			copied[i] = compactWorkflowRunAnyValueForPersist(item)
		}
		return copied
	case []string:
		return compactStringSliceForPersist(typed, maxWorkflowRunText)
	case map[string]string:
		copied := copyWorkflowRunStringMap(typed)
		for key, value := range copied {
			copied[key] = compactSessionText(value, maxWorkflowRunText)
		}
		return copied
	case string:
		return compactSessionText(typed, maxWorkflowRunText)
	default:
		return typed
	}
}

func compactSessionArtifactsForPersist(artifacts []SessionArtifactSnapshot) []SessionArtifactSnapshot {
	if len(artifacts) == 0 {
		return nil
	}
	out := make([]SessionArtifactSnapshot, len(artifacts))
	for i, artifact := range artifacts {
		out[i] = artifact
		out[i].Summary = compactSessionText(artifact.Summary, maxSessionArtifactSummaryBytes)
		if len([]byte(out[i].Content)) > maxPersistedSessionArtifactContentBytes {
			out[i].Content = compactSessionText(out[i].Content, maxPersistedSessionArtifactContentBytes)
			out[i].Truncated = true
		}
		out[i].StoredBytes = len([]byte(out[i].Content))
	}
	return out
}

func compactCollaborationMessagesForPersist(messages []CollaborationMessageSnapshot) []CollaborationMessageSnapshot {
	if len(messages) == 0 {
		return nil
	}
	out := make([]CollaborationMessageSnapshot, len(messages))
	for i, message := range messages {
		out[i] = message
		out[i].Content = compactSessionText(message.Content, maxCollaborationText)
	}
	return out
}

func compactBlackboardEntriesForPersist(entries []BlackboardEntrySnapshot) []BlackboardEntrySnapshot {
	if len(entries) == 0 {
		return nil
	}
	out := make([]BlackboardEntrySnapshot, len(entries))
	for i, entry := range entries {
		out[i] = entry
		out[i].Content = compactSessionText(entry.Content, maxCollaborationText)
	}
	return out
}

func compactStringSliceForPersist(values []string, maxBytes int) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = compactSessionText(value, maxBytes)
	}
	return out
}

func compactSessionText(value string, maxBytes int) string {
	if maxBytes <= 0 || len([]byte(value)) <= maxBytes {
		return value
	}
	suffix := sessionCompactionSuffix
	if maxBytes <= len([]byte(suffix)) {
		return trimSessionArtifactBytes(value, maxBytes)
	}
	trimmed := trimSessionArtifactBytes(value, maxBytes-len([]byte(suffix)))
	return strings.TrimRight(trimmed, "\r\n\t ") + suffix
}
