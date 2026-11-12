package api

import (
	"sort"
	"strconv"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/session"
)

// TaskTimelineItem is the stable timeline contract shared by Agent and
// Workflow tasks. Typed replay APIs remain available for compatibility.
type TaskTimelineItem struct {
	RunID      string         `json:"run_id"`
	RunType    string         `json:"run_type"`
	Seq        int            `json:"seq,omitempty"`
	At         string         `json:"at,omitempty"`
	Kind       string         `json:"kind"`
	Type       string         `json:"type,omitempty"`
	Stage      string         `json:"stage,omitempty"`
	Status     string         `json:"status,omitempty"`
	AgentID    string         `json:"agent_id,omitempty"`
	Mode       string         `json:"mode,omitempty"`
	ToolName   string         `json:"tool_name,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	ArtifactID string         `json:"artifact_id,omitempty"`
	Title      string         `json:"title,omitempty"`
	Summary    string         `json:"summary,omitempty"`
	Content    string         `json:"content,omitempty"`
	ToolResult map[string]any `json:"tool_result,omitempty"`
	Reason     string         `json:"reason,omitempty"`
	Severity   string         `json:"severity,omitempty"`

	WorkflowStatus string `json:"workflow_status,omitempty"`
	NextStage      string `json:"next_stage,omitempty"`
	GroupKind      string `json:"group_kind,omitempty"`
	GroupID        string `json:"group_id,omitempty"`
	ParentID       string `json:"parent_id,omitempty"`
	Attempt        int    `json:"attempt,omitempty"`
	Current        bool   `json:"current,omitempty"`
	Action         string `json:"action,omitempty"`

	BudgetScope               string  `json:"budget_scope,omitempty"`
	BudgetReason              string  `json:"budget_reason,omitempty"`
	BudgetMetric              string  `json:"budget_metric,omitempty"`
	BudgetUsed                int     `json:"budget_used,omitempty"`
	BudgetSoftLimit           int     `json:"budget_soft_limit,omitempty"`
	BudgetHardLimit           int     `json:"budget_hard_limit,omitempty"`
	BudgetRemaining           int     `json:"budget_remaining,omitempty"`
	BudgetTotalTokens         int     `json:"budget_total_tokens,omitempty"`
	BudgetLLMCalls            int     `json:"budget_llm_calls,omitempty"`
	BudgetEstimatedInputCost  float64 `json:"budget_estimated_input_cost,omitempty"`
	BudgetEstimatedOutputCost float64 `json:"budget_estimated_output_cost,omitempty"`
	BudgetEstimatedTotalCost  float64 `json:"budget_estimated_total_cost,omitempty"`
	BudgetCostCurrency        string  `json:"budget_cost_currency,omitempty"`
	BudgetPricingSource       string  `json:"budget_pricing_source,omitempty"`
	ContractCheck             string  `json:"contract_check,omitempty"`
	SourceRef                 string  `json:"source_ref,omitempty"`
	StopReason                string  `json:"stop_reason,omitempty"`
	ContinuationCount         int     `json:"continuation_count,omitempty"`
	Incomplete                bool    `json:"incomplete,omitempty"`
	PromptTokens              int     `json:"prompt_tokens,omitempty"`
	OutputTokens              int     `json:"output_tokens,omitempty"`
	CachedTokens              int     `json:"cached_tokens,omitempty"`
	IsError                   bool    `json:"is_error,omitempty"`
	NeedsAction               bool    `json:"needs_action,omitempty"`
	Suspended                 bool    `json:"suspended,omitempty"`

	Plan []TaskTimelinePlanStep `json:"plan,omitempty"`
}

type TaskTimelinePlanStep struct {
	Name     string `json:"name"`
	NodeType string `json:"node_type,omitempty"`
	Agent    string `json:"agent,omitempty"`
	Skill    string `json:"skill,omitempty"`
	Current  bool   `json:"current,omitempty"`
}

func (s *Server) taskTimelineForAgent(run session.AgentRunSnapshot, query agentRunQuery) []TaskTimelineItem {
	if query.IncludeContent {
		run = s.runtime.HydrateAgentRun(run)
	}
	withoutLimit := query
	withoutLimit.Limit = 0
	events := s.agentRunEventsForQuery(run.Events, withoutLimit)
	items := make([]TaskTimelineItem, 0, len(events)+1)
	for _, event := range events {
		items = append(items, TaskTimelineItem{
			RunID:             run.ID,
			RunType:           "agent",
			Seq:               event.Seq,
			At:                event.At,
			Kind:              "event",
			Type:              event.Type,
			Stage:             event.TaskStage,
			AgentID:           firstWorkflowRunQueryValue(event.AgentID, run.AgentID),
			Mode:              firstWorkflowRunQueryValue(event.Mode, run.Mode),
			ToolName:          event.ToolName,
			ToolCallID:        event.ToolCallID,
			Summary:           agentRunTimelineEventSummary(event),
			Content:           agentRunTimelineContent(event.Content, query),
			ToolResult:        timelineToolMetadata(event.Type, event.Content),
			Reason:            event.Reason,
			Severity:          event.Severity,
			BudgetScope:       event.BudgetScope,
			ContractCheck:     event.ContractCheck,
			SourceRef:         event.SourceRef,
			StopReason:        event.StopReason,
			ContinuationCount: event.ContinuationCount,
			Incomplete:        event.Incomplete,
			PromptTokens:      event.PromptTokens,
			OutputTokens:      event.OutputTokens,
			CachedTokens:      event.CachedTokens,
			IsError:           event.IsError,
			NeedsAction:       event.NeedsAction || event.Suspended || event.Incomplete,
			Suspended:         event.Suspended,
			Action:            taskTimelineAgentAction(event),
		})
	}
	items = append(items, TaskTimelineItem{
		RunID:       run.ID,
		RunType:     "agent",
		At:          firstWorkflowRunQueryValue(run.CompletedAt, run.UpdatedAt, run.StartedAt),
		Kind:        "run",
		Type:        "agent_run",
		Status:      run.Status,
		AgentID:     run.AgentID,
		Mode:        run.Mode,
		Title:       run.ID,
		Summary:     agentRunSnapshotSummary(run),
		Content:     agentRunTimelineContent(run.Output, query),
		Attempt:     run.Attempt,
		Current:     agentRunCollectionRunActive(run),
		IsError:     strings.EqualFold(run.Status, "failed"),
		NeedsAction: agentRunCollectionRunNeedsAction(run),
	})
	return limitAndSortTaskTimeline(items, query.Limit)
}

func (s *Server) taskTimelineForWorkflow(run session.WorkflowRunSnapshot, query workflowRunQuery) []TaskTimelineItem {
	withoutLimit := query
	withoutLimit.Limit = 0
	typed := s.workflowRunTimeline(run, withoutLimit)
	items := make([]TaskTimelineItem, 0, len(typed)+2)
	if plan, ok := s.taskWorkflowPlanTimelineItem(run); ok {
		items = append(items, plan)
	}
	for _, item := range typed {
		next := taskTimelineItemFromWorkflow(run, item)
		switch item.Kind {
		case "event":
			if event, ok := workflowTimelineEventForItem(run.Events, item); ok {
				next.Seq = event.Seq
				next.ToolResult = timelineToolMetadata(event.Type, event.Content)
				next.Mode = event.Mode
				next.WorkflowStatus = event.WorkflowStatus
				next.NextStage = event.NextStage
				next.Action = taskTimelineWorkflowAction(event.Type, event.WorkflowStatus, event.Reason, event.NeedsAction || event.PendingApproval || event.Suspended || event.Incomplete)
				applyTaskTimelineEventGroup(&next, event)
			}
		case "stage":
			if stage, ok := workflowTimelineStageForItem(run.CompletedStages, item); ok {
				next.Attempt = stage.Attempts
				applyTaskTimelineStageGroup(&next, stage)
			}
		}
		next.Current = next.Current || strings.EqualFold(strings.TrimSpace(next.Stage), strings.TrimSpace(run.NextStage)) && workflowRunCollectionRunActive(run)
		items = append(items, next)
	}
	items = append(items, TaskTimelineItem{
		RunID:          run.ID,
		RunType:        "workflow",
		At:             firstWorkflowRunQueryValue(run.CompletedAt, run.UpdatedAt, run.StartedAt),
		Kind:           "run",
		Type:           "workflow_run",
		Stage:          run.NextStage,
		Status:         run.Status,
		WorkflowStatus: run.Status,
		NextStage:      run.NextStage,
		Title:          run.Name,
		Summary:        runCollectionWorkflowSummary(run),
		Attempt:        run.Attempt,
		Current:        workflowRunCollectionRunActive(run),
		Action:         taskTimelineWorkflowAction("workflow_state", run.Status, run.BudgetReason, workflowRunCollectionRunNeedsAction(run)),
		NeedsAction:    workflowRunCollectionRunNeedsAction(run),
		IsError:        workflowRunCollectionRunHasError(run),
		GroupKind:      taskTimelineRunGroupKind(run),
		GroupID:        firstWorkflowRunQueryValue(run.PendingSubWorkflowRunID, run.PendingSubWorkflowName, run.NextStage),
	})
	return limitAndSortTaskTimeline(items, query.Limit)
}

func taskTimelineItemFromWorkflow(run session.WorkflowRunSnapshot, item workflowRunTimelineItem) TaskTimelineItem {
	return TaskTimelineItem{
		RunID:                     run.ID,
		RunType:                   "workflow",
		At:                        item.At,
		Kind:                      item.Kind,
		Type:                      item.Type,
		Stage:                     item.Stage,
		Status:                    item.Status,
		AgentID:                   item.AgentID,
		ToolName:                  item.ToolName,
		ToolCallID:                item.ToolCallID,
		ArtifactID:                item.ArtifactID,
		Title:                     item.Title,
		Summary:                   item.Summary,
		Content:                   item.Content,
		Reason:                    item.Reason,
		Severity:                  item.Severity,
		BudgetScope:               item.BudgetScope,
		BudgetReason:              item.BudgetReason,
		BudgetMetric:              item.BudgetMetric,
		BudgetUsed:                item.BudgetUsed,
		BudgetSoftLimit:           item.BudgetSoftLimit,
		BudgetHardLimit:           item.BudgetHardLimit,
		BudgetRemaining:           item.BudgetRemaining,
		BudgetTotalTokens:         item.BudgetTotalTokens,
		BudgetLLMCalls:            item.BudgetLLMCalls,
		BudgetEstimatedInputCost:  item.BudgetEstimatedInputCost,
		BudgetEstimatedOutputCost: item.BudgetEstimatedOutputCost,
		BudgetEstimatedTotalCost:  item.BudgetEstimatedTotalCost,
		BudgetCostCurrency:        item.BudgetCostCurrency,
		BudgetPricingSource:       item.BudgetPricingSource,
		ContractCheck:             item.ContractCheck,
		SourceRef:                 item.SourceRef,
		StopReason:                item.StopReason,
		ContinuationCount:         item.ContinuationCount,
		Incomplete:                item.Incomplete,
		PromptTokens:              item.PromptTokens,
		OutputTokens:              item.OutputTokens,
		CachedTokens:              item.CachedTokens,
		IsError:                   item.IsError,
		NeedsAction:               item.NeedsAction,
		Suspended:                 item.Suspended,
	}
}

func (s *Server) taskWorkflowPlanTimelineItem(run session.WorkflowRunSnapshot) (TaskTimelineItem, bool) {
	doc, err := s.runtime.WorkflowRunner().LoadRunnableWorkflowGraphDocument(run.Name)
	if err != nil {
		return TaskTimelineItem{}, false
	}
	wanted := taskTimelineResourcePin(run.ResourcePins, "workflow", run.Name)
	current := session.NewResourcePin("workflow", run.Name, doc)
	if wanted == nil || wanted.Version != current.Version {
		return TaskTimelineItem{}, false
	}
	steps := make([]TaskTimelinePlanStep, 0, len(doc.Stages))
	names := make([]string, 0, len(doc.Stages))
	for _, stage := range doc.Stages {
		name := strings.TrimSpace(stage.Name)
		if name == "" {
			continue
		}
		names = append(names, name)
		steps = append(steps, TaskTimelinePlanStep{Name: name, NodeType: stage.NodeType, Agent: stage.Agent, Skill: stage.Skill, Current: strings.EqualFold(name, run.NextStage)})
	}
	if len(steps) == 0 {
		return TaskTimelineItem{}, false
	}
	return TaskTimelineItem{RunID: run.ID, RunType: "workflow", At: run.StartedAt, Kind: "plan", Type: "workflow_plan", Title: run.Name, Summary: strings.Join(names, " -> "), Plan: steps}, true
}

func taskTimelineResourcePin(pins []session.ResourcePin, kind, name string) *session.ResourcePin {
	for i := range pins {
		if strings.EqualFold(pins[i].Kind, kind) && strings.EqualFold(pins[i].Name, name) {
			pin := pins[i]
			return &pin
		}
	}
	return nil
}

func workflowTimelineEventForItem(events []session.WorkflowRunEventSnapshot, item workflowRunTimelineItem) (session.WorkflowRunEventSnapshot, bool) {
	for _, event := range events {
		if event.At == item.At && event.Type == item.Type && event.Stage == item.Stage && event.ToolCallID == item.ToolCallID {
			return event, true
		}
	}
	return session.WorkflowRunEventSnapshot{}, false
}

func workflowTimelineStageForItem(stages []session.WorkflowRunStageSnapshot, item workflowRunTimelineItem) (session.WorkflowRunStageSnapshot, bool) {
	for _, stage := range stages {
		if stage.Stage == item.Stage && firstWorkflowRunQueryValue(stage.CompletedAt, stage.StartedAt) == item.At {
			return stage, true
		}
	}
	return session.WorkflowRunStageSnapshot{}, false
}

func applyTaskTimelineEventGroup(item *TaskTimelineItem, event session.WorkflowRunEventSnapshot) {
	typeName := strings.ToLower(strings.TrimSpace(event.Type))
	reason := strings.ToLower(strings.TrimSpace(event.Reason))
	switch {
	case reason == "parallel_branch" || strings.Contains(typeName, "parallel"):
		item.GroupKind = "parallel"
		item.GroupID = firstWorkflowRunQueryValue(event.SourceRef, event.Stage, event.TaskStage)
		item.ParentID = event.SourceRef
	case reason == "stage_retry" || strings.Contains(typeName, "retry"):
		item.GroupKind = "retry"
		item.GroupID = event.Stage
		item.Attempt = taskTimelineAttempt(event.SourceRef, event.ArgumentsSummary)
	case strings.Contains(typeName, "sub_workflow"):
		item.GroupKind = "sub_workflow"
		item.GroupID = firstWorkflowRunQueryValue(event.NextStage, event.Stage)
	case strings.Contains(typeName, "approval") || strings.Contains(typeName, "input") || strings.Contains(typeName, "budget"):
		item.GroupKind = "gate"
		item.GroupID = firstWorkflowRunQueryValue(event.Stage, event.NextStage, event.Type)
	}
}

func applyTaskTimelineStageGroup(item *TaskTimelineItem, stage session.WorkflowRunStageSnapshot) {
	if strings.EqualFold(strings.TrimSpace(stage.Metadata["parallel_branch"]), "true") {
		item.GroupKind = "parallel"
		item.GroupID = firstWorkflowRunQueryValue(stage.Metadata["parallel_parent"], stage.Metadata["parallel_branch_owner"], stage.Stage)
		item.ParentID = stage.Metadata["parallel_parent"]
	} else if strings.Contains(strings.ToLower(stage.NodeType), "sub_workflow") {
		item.GroupKind = "sub_workflow"
		item.GroupID = firstWorkflowRunQueryValue(stage.Metadata["sub_workflow_run_id"], stage.Metadata["sub_workflow"], stage.Stage)
	} else if stage.Attempts > 1 {
		item.GroupKind = "retry"
		item.GroupID = stage.Stage
	}
}

func taskTimelineAgentAction(event session.AgentRunEventSnapshot) string {
	if event.NeedsAction || event.Suspended {
		return "approval"
	}
	if event.Incomplete {
		return "continue"
	}
	return ""
}

func taskTimelineWorkflowAction(eventType, status, reason string, needsAction bool) string {
	combined := strings.ToLower(strings.Join([]string{eventType, status, reason}, " "))
	switch {
	case strings.Contains(combined, "awaiting_input") || strings.Contains(combined, "input_required"):
		return "input"
	case strings.Contains(combined, "sub_workflow"):
		return "sub_workflow"
	case strings.Contains(combined, "budget") && needsAction:
		return "budget"
	case strings.Contains(combined, "interrupted") || strings.Contains(combined, "process_restart"):
		return "retry"
	case strings.Contains(combined, "approval") || needsAction:
		return "approval"
	case strings.Contains(combined, "retry"):
		return "retry"
	default:
		return ""
	}
}

func taskTimelineRunGroupKind(run session.WorkflowRunSnapshot) string {
	status := strings.ToLower(strings.TrimSpace(run.Status))
	switch {
	case status == "awaiting_sub_workflow":
		return "sub_workflow"
	case strings.Contains(status, "awaiting") || strings.Contains(status, "approval"):
		return "gate"
	default:
		return ""
	}
}

func taskTimelineAttempt(values ...string) int {
	for _, value := range values {
		for _, part := range strings.FieldsFunc(value, func(r rune) bool { return r < '0' || r > '9' }) {
			if attempt, err := strconv.Atoi(part); err == nil && attempt > 0 {
				return attempt
			}
		}
	}
	return 0
}

func limitAndSortTaskTimeline(items []TaskTimelineItem, limit int) []TaskTimelineItem {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].At == "" && items[j].At != "" {
			return false
		}
		if items[i].At != "" && items[j].At == "" {
			return true
		}
		if items[i].At == items[j].At {
			if items[i].Seq != items[j].Seq {
				return items[i].Seq < items[j].Seq
			}
			return taskTimelineKindRank(items[i].Kind) < taskTimelineKindRank(items[j].Kind)
		}
		return items[i].At < items[j].At
	})
	if limit > 0 && len(items) > limit {
		return append([]TaskTimelineItem(nil), items[len(items)-limit:]...)
	}
	return items
}

func taskTimelineKindRank(kind string) int {
	switch kind {
	case "event":
		return 0
	case "plan":
		return 1
	case "stage":
		return 2
	case "artifact":
		return 3
	case "run":
		return 4
	default:
		return 5
	}
}
