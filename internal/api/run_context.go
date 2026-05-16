package api

import (
	"net/http"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

const runContextDiagnosticLimit = 24

type runContextResponse struct {
	RunID        string                         `json:"run_id"`
	RunType      string                         `json:"run_type"`
	Status       string                         `json:"status,omitempty"`
	AgentID      string                         `json:"agent_id,omitempty"`
	Mode         string                         `json:"mode,omitempty"`
	WorkflowName string                         `json:"workflow_name,omitempty"`
	Latest       *runContextBudgetSample        `json:"latest,omitempty"`
	History      []runContextBudgetSample       `json:"history,omitempty"`
	MemoryBlocks []schema.PromptContextBlock    `json:"memory_blocks,omitempty"`
	Omitted      []string                       `json:"omitted_context,omitempty"`
	Artifacts    []string                       `json:"artifact_refs,omitempty"`
	ToolSchema   runContextToolSchemaVisibility `json:"tool_schema"`
	Counts       runContextCounts               `json:"counts"`
}

type runContextBudgetSample struct {
	Seq          int                 `json:"seq,omitempty"`
	At           string              `json:"at,omitempty"`
	EventType    string              `json:"event_type,omitempty"`
	Stage        string              `json:"stage,omitempty"`
	AgentID      string              `json:"agent_id,omitempty"`
	Mode         string              `json:"mode,omitempty"`
	WorkflowName string              `json:"workflow_name,omitempty"`
	TaskStage    string              `json:"task_stage,omitempty"`
	Budget       schema.PromptBudget `json:"budget"`
}

type runContextToolSchemaVisibility struct {
	Selection         string                    `json:"selection,omitempty"`
	ExposedToolCount  int                       `json:"exposed_tool_count,omitempty"`
	TotalToolCount    int                       `json:"total_tool_count,omitempty"`
	FilteredToolCount int                       `json:"filtered_tool_count,omitempty"`
	DiagnosticCount   int                       `json:"diagnostic_count,omitempty"`
	DiagnosticOmitted int                       `json:"diagnostic_omitted,omitempty"`
	Injected          []schema.PromptToolSchema `json:"injected,omitempty"`
	Filtered          []schema.PromptToolSchema `json:"filtered,omitempty"`
}

type runContextCounts struct {
	PromptBudgetSamples          int `json:"prompt_budget_samples"`
	TotalEstimatedPromptTokens   int `json:"total_estimated_prompt_tokens,omitempty"`
	AverageEstimatedPromptTokens int `json:"average_estimated_prompt_tokens,omitempty"`
	MaxEstimatedPromptTokens     int `json:"max_estimated_prompt_tokens,omitempty"`
	LatestEstimatedPromptTokens  int `json:"latest_estimated_prompt_tokens,omitempty"`
	CacheablePrefixTokens        int `json:"cacheable_prefix_tokens,omitempty"`
	MemoryBlocks                 int `json:"memory_blocks,omitempty"`
	MemoryOmitted                int `json:"memory_omitted,omitempty"`
	MemoryEstimatedSavedTokens   int `json:"memory_estimated_saved_tokens,omitempty"`
	ArtifactRefs                 int `json:"artifact_refs,omitempty"`
	CompactedToolResults         int `json:"compacted_tool_results,omitempty"`
	ArtifactOmittedTokens        int `json:"artifact_omitted_tokens,omitempty"`
	SkillOmittedTokens           int `json:"skill_omitted_tokens,omitempty"`
	OmittedContext               int `json:"omitted_context,omitempty"`
	InjectedToolSchemas          int `json:"injected_tool_schemas,omitempty"`
	FilteredToolSchemas          int `json:"filtered_tool_schemas,omitempty"`
	ToolSchemaDiagnosticOmitted  int `json:"tool_schema_diagnostic_omitted,omitempty"`
	HistoryEstimatedSavedTokens  int `json:"history_estimated_saved_tokens,omitempty"`
}

func (s *Server) handleRunItem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/runs/"), "/"), "/")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" {
		http.NotFound(w, r)
		return
	}
	snapshot := s.sessionSnapshotWithApprovalRisk()
	switch parts[1] {
	case "context":
		if run, ok := agentRunByID(snapshot.AgentRuns, parts[0]); ok {
			writeJSON(w, agentRunContext(run))
			return
		}
		if run, ok := workflowRunByID(snapshot.WorkflowRuns, parts[0]); ok {
			writeJSON(w, workflowRunContext(run))
			return
		}
	case "artifacts":
		query := workflowRunArtifactQueryFromRequest(r)
		if run, ok := agentRunByID(snapshot.AgentRuns, parts[0]); ok {
			writeJSON(w, s.agentRunArtifactsResponse(run, query))
			return
		}
		if run, ok := workflowRunByID(snapshot.WorkflowRuns, parts[0]); ok {
			writeJSON(w, s.workflowRunArtifactsResponse(run, query))
			return
		}
	}
	http.NotFound(w, r)
}

func agentRunContext(run session.AgentRunSnapshot) runContextResponse {
	samples := make([]runContextBudgetSample, 0)
	for _, event := range run.Events {
		if event.PromptBudget == nil {
			continue
		}
		budget := copyPromptBudgetForRunContext(*event.PromptBudget)
		if strings.TrimSpace(budget.AgentID) == "" {
			budget.AgentID = firstWorkflowRunQueryValue(event.AgentID, run.AgentID)
		}
		if strings.TrimSpace(budget.Mode) == "" {
			budget.Mode = firstWorkflowRunQueryValue(event.Mode, run.Mode)
		}
		samples = append(samples, runContextBudgetSample{
			Seq:       event.Seq,
			At:        event.At,
			EventType: event.Type,
			AgentID:   firstWorkflowRunQueryValue(event.AgentID, budget.AgentID, run.AgentID),
			Mode:      firstWorkflowRunQueryValue(event.Mode, budget.Mode, run.Mode),
			TaskStage: firstWorkflowRunQueryValue(event.TaskStage, budget.TaskStage),
			Budget:    budget,
		})
	}
	return runContextResponseFromSamples(run.ID, "agent", run.Status, run.AgentID, run.Mode, "", samples)
}

func workflowRunContext(run session.WorkflowRunSnapshot) runContextResponse {
	samples := make([]runContextBudgetSample, 0)
	for _, event := range run.Events {
		if event.PromptBudget == nil {
			continue
		}
		budget := copyPromptBudgetForRunContext(*event.PromptBudget)
		if strings.TrimSpace(budget.AgentID) == "" {
			budget.AgentID = event.AgentID
		}
		if strings.TrimSpace(budget.Mode) == "" {
			budget.Mode = event.Mode
		}
		if strings.TrimSpace(budget.WorkflowName) == "" {
			budget.WorkflowName = firstWorkflowRunQueryValue(event.WorkflowName, run.Name)
		}
		if strings.TrimSpace(budget.TaskStage) == "" {
			budget.TaskStage = event.TaskStage
		}
		samples = append(samples, runContextBudgetSample{
			Seq:          event.Seq,
			At:           event.At,
			EventType:    event.Type,
			Stage:        event.Stage,
			AgentID:      firstWorkflowRunQueryValue(event.AgentID, budget.AgentID),
			Mode:         firstWorkflowRunQueryValue(event.Mode, budget.Mode),
			WorkflowName: firstWorkflowRunQueryValue(event.WorkflowName, budget.WorkflowName, run.Name),
			TaskStage:    firstWorkflowRunQueryValue(event.TaskStage, budget.TaskStage),
			Budget:       budget,
		})
	}
	return runContextResponseFromSamples(run.ID, "workflow", run.Status, runCollectionWorkflowPrimaryAgent(run), runCollectionWorkflowPrimaryMode(run), run.Name, samples)
}

func runContextResponseFromSamples(runID, runType, status, agentID, mode, workflowName string, samples []runContextBudgetSample) runContextResponse {
	resp := runContextResponse{
		RunID:        strings.TrimSpace(runID),
		RunType:      strings.TrimSpace(runType),
		Status:       strings.TrimSpace(status),
		AgentID:      strings.TrimSpace(agentID),
		Mode:         strings.TrimSpace(mode),
		WorkflowName: strings.TrimSpace(workflowName),
		History:      append([]runContextBudgetSample(nil), samples...),
	}
	resp.Counts.PromptBudgetSamples = len(samples)
	if len(samples) == 0 {
		return resp
	}
	latest := samples[len(samples)-1]
	resp.Latest = &latest
	resp.MemoryBlocks = append([]schema.PromptContextBlock(nil), latest.Budget.MemoryBlocks...)
	resp.Omitted = append([]string(nil), latest.Budget.OmittedContext...)
	resp.Artifacts = append([]string(nil), latest.Budget.ArtifactRefs...)
	resp.ToolSchema = runContextToolSchemaVisibilityFor(latest.Budget)
	resp.Counts.LatestEstimatedPromptTokens = latest.Budget.EstimatedPromptTokens
	resp.Counts.CacheablePrefixTokens = latest.Budget.CacheablePrefixTokens
	resp.Counts.MemoryBlocks = latest.Budget.MemoryBlockCount
	resp.Counts.MemoryOmitted = latest.Budget.MemoryOmittedCount
	resp.Counts.MemoryEstimatedSavedTokens = latest.Budget.MemoryEstimatedSavedTokens
	resp.Counts.ArtifactRefs = latest.Budget.ArtifactRefCount
	resp.Counts.CompactedToolResults = latest.Budget.CompactedToolResultCount
	resp.Counts.ArtifactOmittedTokens = latest.Budget.ArtifactOmittedTokens
	resp.Counts.SkillOmittedTokens = latest.Budget.SkillOmittedTokens
	resp.Counts.OmittedContext = len(latest.Budget.OmittedContext)
	resp.Counts.InjectedToolSchemas = len(latest.Budget.InjectedToolSchemas)
	resp.Counts.FilteredToolSchemas = len(latest.Budget.FilteredToolSchemas)
	resp.Counts.ToolSchemaDiagnosticOmitted = latest.Budget.ToolSchemaDiagnosticOmitted
	for _, sample := range samples {
		budget := sample.Budget
		resp.Counts.TotalEstimatedPromptTokens += budget.EstimatedPromptTokens
		resp.Counts.HistoryEstimatedSavedTokens += budget.HistoryEstimatedSavedTokens
		if budget.EstimatedPromptTokens > resp.Counts.MaxEstimatedPromptTokens {
			resp.Counts.MaxEstimatedPromptTokens = budget.EstimatedPromptTokens
		}
	}
	if len(samples) > 0 {
		resp.Counts.AverageEstimatedPromptTokens = resp.Counts.TotalEstimatedPromptTokens / len(samples)
	}
	return resp
}

func runContextToolSchemaVisibilityFor(budget schema.PromptBudget) runContextToolSchemaVisibility {
	return runContextToolSchemaVisibility{
		Selection:         budget.ToolSchemaSelection,
		ExposedToolCount:  budget.ExposedToolCount,
		TotalToolCount:    budget.TotalToolCount,
		FilteredToolCount: budget.FilteredToolCount,
		DiagnosticCount:   budget.ToolSchemaDiagnosticCount,
		DiagnosticOmitted: budget.ToolSchemaDiagnosticOmitted,
		Injected:          append([]schema.PromptToolSchema(nil), budget.InjectedToolSchemas...),
		Filtered:          append([]schema.PromptToolSchema(nil), budget.FilteredToolSchemas...),
	}
}

func copyPromptBudgetForRunContext(budget schema.PromptBudget) schema.PromptBudget {
	budget.MemoryBlocks = limitPromptContextBlocksForRunContext(budget.MemoryBlocks)
	budget.OmittedContext = limitRunContextStrings(budget.OmittedContext)
	budget.ArtifactRefs = limitRunContextStrings(budget.ArtifactRefs)
	budget.InjectedToolSchemas = limitPromptToolSchemasForRunContext(budget.InjectedToolSchemas)
	budget.FilteredToolSchemas = limitPromptToolSchemasForRunContext(budget.FilteredToolSchemas)
	return budget
}

func limitPromptContextBlocksForRunContext(values []schema.PromptContextBlock) []schema.PromptContextBlock {
	if len(values) == 0 {
		return nil
	}
	if len(values) > runContextDiagnosticLimit {
		values = values[:runContextDiagnosticLimit]
	}
	return append([]schema.PromptContextBlock(nil), values...)
}

func limitPromptToolSchemasForRunContext(values []schema.PromptToolSchema) []schema.PromptToolSchema {
	if len(values) == 0 {
		return nil
	}
	if len(values) > runContextDiagnosticLimit {
		values = values[:runContextDiagnosticLimit]
	}
	return append([]schema.PromptToolSchema(nil), values...)
}

func limitRunContextStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out = append(out, value)
		if len(out) >= runContextDiagnosticLimit {
			break
		}
	}
	return out
}
