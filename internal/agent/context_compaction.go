package agent

import (
	"context"
	"fmt"
	"strings"

	mem "github.com/FyMatt/GoFlow-Agent/internal/memory"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

const (
	autoCompactPromptTokenThreshold = 12000
	autoCompactRunCountThreshold    = 20
	autoCompactHistoryThreshold     = 4
	autoCompactArtifactThreshold    = 8
)

// CompactContext writes a compact, reusable summary of the current runtime
// context to workspace memory. Full session history remains available through
// the session archive and artifact store.
func (r *Runtime) CompactContext(ctx context.Context, reason string) (mem.ContextSummary, error) {
	return r.compactContext(ctx, reason, false)
}

// MaybeAutoCompactContext compacts context when recent runtime diagnostics show
// the default prompt/session payload is becoming expensive.
func (r *Runtime) MaybeAutoCompactContext(ctx context.Context, reason string) (mem.ContextSummary, bool, error) {
	if r == nil || r.memory == nil || r.session == nil {
		return mem.ContextSummary{}, false, nil
	}
	if !runtimeNeedsContextCompaction(r.session.Snapshot()) {
		return mem.ContextSummary{}, false, nil
	}
	summary, err := r.compactContext(ctx, reason, true)
	return summary, err == nil, err
}

func (r *Runtime) compactContext(ctx context.Context, reason string, auto bool) (mem.ContextSummary, error) {
	if r == nil || r.memory == nil {
		return mem.ContextSummary{}, fmt.Errorf("memory store is not configured")
	}
	if r.session == nil {
		return mem.ContextSummary{}, fmt.Errorf("session state is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	snapshot := r.SessionSnapshot()
	summary := buildContextSummary(snapshot, reason, auto)
	if strings.TrimSpace(summary.Summary) == "" {
		summary.Summary = "No active runtime context has been recorded yet."
	}
	return r.memory.RecordContext(summary)
}

func runtimeNeedsContextCompaction(snapshot session.Snapshot) bool {
	if snapshot.PromptBudget != nil {
		budget := snapshot.PromptBudget
		if budget.EstimatedPromptTokens >= autoCompactPromptTokenThreshold {
			return true
		}
		if budget.HistoryPromptCompactedOlderItems+budget.HistoryToolCompactedOlderItems >= autoCompactHistoryThreshold {
			return true
		}
		if budget.ArtifactRefCount >= autoCompactArtifactThreshold || budget.ArtifactOmittedTokens > 0 {
			return true
		}
	}
	return len(snapshot.AgentRuns)+len(snapshot.WorkflowRuns) >= autoCompactRunCountThreshold
}

func buildContextSummary(snapshot session.Snapshot, reason string, auto bool) mem.ContextSummary {
	budget := contextPromptCost(snapshot.PromptBudget)
	recentGoals := compactContextGoals(snapshot)
	decisions := compactContextDecisions(snapshot)
	pending := compactContextPendingActions(snapshot)
	files := compactContextFiles(snapshot)
	artifactRefs := compactContextArtifactRefs(snapshot)
	sourceCounts := map[string]int{
		"recent_prompts":     len(snapshot.RecentPrompts),
		"recent_tools":       len(snapshot.RecentTools),
		"agent_runs":         len(snapshot.AgentRuns),
		"workflow_runs":      len(snapshot.WorkflowRuns),
		"pending_approvals":  len(snapshot.PendingApprovals),
		"artifact_refs":      len(artifactRefs),
		"prompt_budget_rows": len(snapshot.PromptBudgets),
	}
	lines := make([]string, 0, 8)
	if strings.TrimSpace(snapshot.ActiveAgent) != "" || strings.TrimSpace(snapshot.Mode) != "" {
		lines = append(lines, fmt.Sprintf("Active context: agent=%s mode=%s", fallbackText(snapshot.ActiveAgent, "-"), fallbackText(snapshot.Mode, "-")))
	}
	if strings.TrimSpace(snapshot.Workflow.Name) != "" || strings.TrimSpace(snapshot.Workflow.Status) != "" {
		lines = append(lines, fmt.Sprintf("Workflow: %s status=%s next=%s", fallbackText(snapshot.Workflow.Name, "-"), fallbackText(snapshot.Workflow.Status, "-"), fallbackText(snapshot.Workflow.NextStage, "-")))
	}
	if strings.TrimSpace(snapshot.TaskStage.Stage) != "" {
		lines = append(lines, fmt.Sprintf("Task stage: %s %s", snapshot.TaskStage.Stage, truncateSummary(snapshot.TaskStage.Detail)))
	}
	if len(recentGoals) > 0 {
		lines = append(lines, "Recent goal: "+recentGoals[0])
	}
	if len(decisions) > 0 {
		lines = append(lines, "Latest decision: "+decisions[0])
	}
	if len(pending) > 0 {
		lines = append(lines, fmt.Sprintf("Pending actions: %d", len(pending)))
	}
	if budget.EstimatedPromptTokens > 0 {
		lines = append(lines, fmt.Sprintf("Prompt budget: estimated_input=%d saved=%d artifacts=%d", budget.EstimatedPromptTokens, budget.MemoryEstimatedSavedTokens+budget.HistoryEstimatedSavedTokens+budget.ArtifactOmittedTokens, budget.ArtifactRefs))
	}
	estimatedSaved := budget.MemoryEstimatedSavedTokens + budget.HistoryEstimatedSavedTokens + budget.ArtifactOmittedTokens
	if estimatedSaved <= 0 && len(snapshot.RecentPrompts)+len(snapshot.RecentTools) > 0 {
		estimatedSaved = (len(snapshot.RecentPrompts) + len(snapshot.RecentTools)) * 120
	}
	return mem.ContextSummary{
		Summary:              strings.Join(nonEmptyStrings(lines...), "\n"),
		Reason:               strings.TrimSpace(reason),
		Auto:                 auto,
		ActiveAgent:          snapshot.ActiveAgent,
		Mode:                 snapshot.Mode,
		Workflow:             compactContextWorkflow(snapshot.Workflow),
		TaskStage:            compactContextTaskStage(snapshot.TaskStage),
		RecentGoals:          recentGoals,
		Decisions:            decisions,
		PendingActions:       pending,
		RelevantFiles:        files,
		ArtifactRefs:         artifactRefs,
		PromptBudget:         budget,
		EstimatedSavedTokens: estimatedSaved,
		SourceCounts:         sourceCounts,
	}
}

func contextPromptCost(budget *schema.PromptBudget) mem.ContextPromptCost {
	if budget == nil {
		return mem.ContextPromptCost{}
	}
	return mem.ContextPromptCost{
		EstimatedPromptTokens:       budget.EstimatedPromptTokens,
		MemoryBlocks:                budget.MemoryBlockCount,
		MemoryEstimatedSavedTokens:  budget.MemoryEstimatedSavedTokens,
		ArtifactRefs:                budget.ArtifactRefCount,
		ArtifactOmittedTokens:       budget.ArtifactOmittedTokens,
		HistoryEstimatedSavedTokens: budget.HistoryEstimatedSavedTokens,
		CacheablePrefixTokens:       budget.CacheablePrefixTokens,
		OmittedContextCount:         len(budget.OmittedContext),
		PromptPrefixHash:            budget.PromptPrefixHash,
	}
}

func compactContextGoals(snapshot session.Snapshot) []string {
	out := make([]string, 0, 8)
	for i := len(snapshot.RecentPrompts) - 1; i >= 0 && len(out) < 4; i-- {
		out = append(out, truncateSummary(snapshot.RecentPrompts[i]))
	}
	for _, run := range snapshot.AgentRuns {
		if len(out) >= 6 {
			break
		}
		out = append(out, truncateSummary(run.Request))
	}
	for _, run := range snapshot.WorkflowRuns {
		if len(out) >= 8 {
			break
		}
		out = append(out, truncateSummary(firstNonEmptyRuntimeString(run.Request, run.Name)))
	}
	return dedupeContextStrings(out, 8)
}

func compactContextDecisions(snapshot session.Snapshot) []string {
	out := make([]string, 0, 10)
	if strings.TrimSpace(snapshot.PendingHandoff.PlanSummary) != "" {
		out = append(out, "Handoff plan: "+truncateSummary(snapshot.PendingHandoff.PlanSummary))
	}
	if strings.TrimSpace(snapshot.LastRouting.Outcome) != "" || strings.TrimSpace(snapshot.LastRouting.TargetAgent) != "" {
		out = append(out, fmt.Sprintf("Routing: outcome=%s target=%s mode=%s reason=%s", fallbackText(snapshot.LastRouting.Outcome, "-"), fallbackText(snapshot.LastRouting.TargetAgent, "-"), fallbackText(snapshot.LastRouting.TargetMode, "-"), truncateSummary(snapshot.LastRouting.Reason)))
	}
	for _, run := range snapshot.AgentRuns {
		if len(out) >= 10 {
			break
		}
		if strings.TrimSpace(run.Output) != "" {
			out = append(out, fmt.Sprintf("Agent %s: %s", fallbackText(run.AgentID, run.ID), truncateSummary(run.Output)))
		} else if strings.TrimSpace(run.Error) != "" {
			out = append(out, fmt.Sprintf("Agent %s failed: %s", fallbackText(run.AgentID, run.ID), truncateSummary(run.Error)))
		}
	}
	for _, run := range snapshot.WorkflowRuns {
		if len(out) >= 12 {
			break
		}
		if strings.TrimSpace(run.Summary) != "" {
			out = append(out, fmt.Sprintf("Workflow %s: %s", fallbackText(run.Name, run.ID), truncateSummary(run.Summary)))
		}
	}
	return dedupeContextStrings(out, 12)
}

func compactContextPendingActions(snapshot session.Snapshot) []string {
	out := make([]string, 0, len(snapshot.PendingApprovals)+2)
	if strings.TrimSpace(snapshot.PendingHandoff.TargetAgent) != "" || strings.TrimSpace(snapshot.PendingHandoff.ExpectedAction) != "" {
		out = append(out, fmt.Sprintf("Confirm handoff to %s/%s: %s", fallbackText(snapshot.PendingHandoff.TargetAgent, "-"), fallbackText(snapshot.PendingHandoff.TargetMode, "-"), truncateSummary(snapshot.PendingHandoff.ExpectedAction)))
	}
	if strings.TrimSpace(snapshot.Workflow.Status) != "" && !strings.EqualFold(snapshot.Workflow.Status, "completed") {
		out = append(out, fmt.Sprintf("Workflow %s is %s; next=%s", fallbackText(snapshot.Workflow.Name, "-"), snapshot.Workflow.Status, fallbackText(snapshot.Workflow.NextStage, "-")))
	}
	for _, pending := range snapshot.PendingApprovals {
		out = append(out, fmt.Sprintf("Approve tool %s for %s stage=%s args=%s", fallbackText(pending.ToolName, "-"), fallbackText(pending.AgentID, "-"), fallbackText(pending.Stage, "-"), truncateSummary(pending.ArgumentsSummary)))
	}
	return dedupeContextStrings(out, 12)
}

func compactContextFiles(snapshot session.Snapshot) []string {
	out := make([]string, 0)
	for _, run := range snapshot.AgentRuns {
		if run.Result == nil {
			continue
		}
		out = append(out, extractResultFiles(*run.Result)...)
	}
	for _, run := range snapshot.WorkflowRuns {
		for _, stage := range run.CompletedStages {
			if stage.Result.Changes != nil || stage.Result.Findings != nil || len(stage.Result.Structured) > 0 {
				out = append(out, extractResultFiles(stage.Result)...)
			}
			for _, artifact := range stage.Artifacts {
				if strings.TrimSpace(artifact.Metadata["path"]) != "" {
					out = append(out, artifact.Metadata["path"])
				}
			}
		}
	}
	return dedupeContextStrings(out, 24)
}

func compactContextArtifactRefs(snapshot session.Snapshot) []string {
	out := make([]string, 0)
	if snapshot.PromptBudget != nil {
		out = append(out, snapshot.PromptBudget.ArtifactRefs...)
	}
	for _, artifact := range snapshot.Artifacts {
		out = append(out, firstNonEmptyRuntimeString(artifact.ArtifactRef, artifact.Ref, artifact.Hash))
	}
	for _, run := range snapshot.AgentRuns {
		for _, artifact := range run.Artifacts {
			out = append(out, firstNonEmptyRuntimeString(artifact.ArtifactRef, artifact.Ref, artifact.Hash))
		}
		for _, event := range run.Events {
			out = append(out, event.ContentArtifactRef)
		}
	}
	for _, run := range snapshot.WorkflowRuns {
		for _, event := range run.Events {
			out = append(out, event.ContentArtifactRef)
		}
		for _, stage := range run.CompletedStages {
			for _, artifact := range stage.Artifacts {
				out = append(out, firstNonEmptyRuntimeString(artifact.ArtifactRef, artifact.Hash))
			}
		}
	}
	return dedupeContextStrings(out, 24)
}

func compactContextWorkflow(workflow session.WorkflowSnapshot) string {
	parts := []string{workflow.Name, workflow.Status}
	if strings.TrimSpace(workflow.NextStage) != "" {
		parts = append(parts, "next="+workflow.NextStage)
	}
	if strings.TrimSpace(workflow.Summary) != "" {
		parts = append(parts, truncateSummary(workflow.Summary))
	}
	return strings.Join(nonEmptyStrings(parts...), " / ")
}

func compactContextTaskStage(stage session.TaskStageSnapshot) string {
	parts := []string{stage.Stage, stage.AgentID, stage.Mode, truncateSummary(stage.Detail)}
	return strings.Join(nonEmptyStrings(parts...), " / ")
}

func dedupeContextStrings(values []string, limit int) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func nonEmptyStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && value != "-" {
			out = append(out, value)
		}
	}
	return out
}
