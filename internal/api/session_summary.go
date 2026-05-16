package api

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/FyMatt/GoFlow-Agent/internal/session"
)

const (
	sessionSummaryTextBytes = 1200
)

func sessionSnapshotForAPI(snapshot session.Snapshot, details bool) session.Snapshot {
	stripSessionArtifactContent(&snapshot)
	if details {
		return snapshot
	}
	snapshot.Workflow = summarizeWorkflowSnapshotForAPI(snapshot.Workflow)
	snapshot.AgentRuns = summarizeAgentRunsForAPI(snapshot.AgentRuns)
	snapshot.WorkflowRuns = summarizeWorkflowRunsForAPI(snapshot.WorkflowRuns)
	snapshot.Artifacts = summarizeSessionArtifactsForAPI(snapshot.Artifacts)
	snapshot.PendingApprovals = summarizePendingApprovalsForAPI(snapshot.PendingApprovals)
	snapshot.Messages = nil
	snapshot.Blackboard = nil
	return snapshot
}

func requestWantsFullSession(r *http.Request) bool {
	if r == nil || r.URL == nil {
		return false
	}
	values := r.URL.Query()
	for _, value := range []string{
		values.Get("full"),
		values.Get("session"),
		values.Get("view"),
		values.Get("detail"),
		values.Get("details"),
		values.Get("include_session"),
		values.Get("include_details"),
	} {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "full" || value == "detail" || value == "details" || value == "true" || value == "1" || value == "yes" {
			return true
		}
	}
	return false
}

func summarizeAgentRunsForAPI(runs []session.AgentRunSnapshot) []session.AgentRunSnapshot {
	if len(runs) == 0 {
		return nil
	}
	out := make([]session.AgentRunSnapshot, len(runs))
	for i, run := range runs {
		out[i] = summarizeAgentRunForAPI(run)
	}
	return out
}

func summarizeAgentRunForAPI(run session.AgentRunSnapshot) session.AgentRunSnapshot {
	run.EventsCount = len(run.Events)
	run.DiffsCount = len(agentRunDiffs(run, agentRunQuery{}))
	run.ArtifactsCount = agentRunArtifactCount(run)
	run.Request = summaryText(run.Request)
	run.Output = summaryText(run.Output)
	run.Error = summaryText(run.Error)
	run.Artifacts = summarizeAgentArtifactsForAPI(run.Artifacts)
	run.Events = nil
	run.Result = nil
	run.ResumeContext = nil
	run.PendingApprovals = summarizePendingApprovalsForAPI(run.PendingApprovals)
	return run
}

func summarizeAgentArtifactsForAPI(items []session.AgentRunArtifactSnapshot) []session.AgentRunArtifactSnapshot {
	if len(items) == 0 {
		return nil
	}
	out := make([]session.AgentRunArtifactSnapshot, len(items))
	for i, item := range items {
		out[i] = item
		out[i].Content = ""
		out[i].Summary = summaryText(item.Summary)
	}
	return out
}

func summarizeWorkflowRunsForAPI(runs []session.WorkflowRunSnapshot) []session.WorkflowRunSnapshot {
	if len(runs) == 0 {
		return nil
	}
	out := make([]session.WorkflowRunSnapshot, len(runs))
	for i, run := range runs {
		out[i] = summarizeWorkflowRunForAPI(run)
	}
	return out
}

func summarizeWorkflowRunForAPI(run session.WorkflowRunSnapshot) session.WorkflowRunSnapshot {
	run.StagesCount = len(run.CompletedStages)
	run.ArtifactsCount = len(run.Artifacts)
	run.EventsCount = len(run.Events)
	run.DiffsCount = len(workflowRunDiffs(run, workflowRunQuery{}))
	run.Request = summaryText(run.Request)
	run.Summary = summaryText(run.Summary)
	run.ApprovalPrompt = summaryText(run.ApprovalPrompt)
	run.PendingArgsSummary = summaryText(firstSummaryText(run.PendingArgsSummary, run.PendingArgs))
	run.PendingArgs = ""
	run.CompletedStages = nil
	run.Artifacts = nil
	run.Events = nil
	return run
}

func summarizeWorkflowSnapshotForAPI(snapshot session.WorkflowSnapshot) session.WorkflowSnapshot {
	snapshot.Request = summaryText(snapshot.Request)
	snapshot.Summary = summaryText(snapshot.Summary)
	snapshot.LastApproval = summaryText(snapshot.LastApproval)
	snapshot.PendingArgumentsSummary = summaryText(firstSummaryText(snapshot.PendingArgumentsSummary, snapshot.PendingArguments))
	snapshot.PendingArguments = ""
	return snapshot
}

func summarizePendingApprovalsForAPI(items []session.PendingApprovalSnapshot) []session.PendingApprovalSnapshot {
	if len(items) == 0 {
		return nil
	}
	out := make([]session.PendingApprovalSnapshot, len(items))
	for i, item := range items {
		out[i] = item
		out[i].Arguments = ""
		out[i].ArgumentsSummary = summaryText(firstSummaryText(item.ArgumentsSummary, item.Arguments))
		out[i].Request = summaryText(item.Request)
		out[i].CompletedSummary = summaryText(item.CompletedSummary)
	}
	return out
}

func summarizeSessionArtifactsForAPI(items []session.SessionArtifactSnapshot) []session.SessionArtifactSnapshot {
	if len(items) == 0 {
		return nil
	}
	out := make([]session.SessionArtifactSnapshot, len(items))
	for i, item := range items {
		out[i] = item
		out[i].Content = ""
		out[i].Summary = summaryText(item.Summary)
	}
	return out
}

func summaryText(value string) string {
	if len([]byte(value)) <= sessionSummaryTextBytes {
		return value
	}
	trimmed := value[:sessionSummaryTextBytes]
	for !utf8.ValidString(trimmed) && len(trimmed) > 0 {
		trimmed = trimmed[:len(trimmed)-1]
	}
	return strings.TrimRight(trimmed, "\r\n\t ") + "..."
}

func firstSummaryText(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
