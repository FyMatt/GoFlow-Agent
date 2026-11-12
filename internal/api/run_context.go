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
	TargetID     string                         `json:"target_id,omitempty"`
	Latest       *runContextBudgetSample        `json:"latest,omitempty"`
	History      []runContextBudgetSample       `json:"history,omitempty"`
	MemoryBlocks []schema.PromptContextBlock    `json:"memory_blocks,omitempty"`
	Omitted      []string                       `json:"omitted_context,omitempty"`
	Artifacts    []string                       `json:"artifact_refs,omitempty"`
	ToolSchema   runContextToolSchemaVisibility `json:"tool_schema"`
	Counts       runContextCounts               `json:"counts"`
}

// runWorkbenchResponse is the bounded task snapshot used by the page-first
// workbench. It keeps recovery to one request without embedding replay,
// artifacts, diffs, or other detail that remains available on demand.
type runWorkbenchResponse struct {
	Task        TaskRecord             `json:"task"`
	Run         any                    `json:"run"`
	FinalOutput string                 `json:"final_output,omitempty"`
	Actions     any                    `json:"actions,omitempty"`
	Timeline    any                    `json:"timeline,omitempty"`
	TokenUsage  usageTotals            `json:"token_usage"`
	Execution   *runWorkbenchExecution `json:"execution,omitempty"`
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
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/runs/"), "/"), "/")
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		http.NotFound(w, r)
		return
	}
	snapshot := s.sessionSnapshotWithApprovalRisk()
	agentRun, isAgent := agentRunByID(snapshot.AgentRuns, parts[0])
	workflowRun, isWorkflow := workflowRunByID(snapshot.WorkflowRuns, parts[0])
	if !isAgent && !isWorkflow {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodPost {
		if len(parts) < 3 || parts[1] != "actions" {
			http.NotFound(w, r)
			return
		}
		if runActionNeedsCurrentResources(parts[2]) {
			pins := workflowRun.ResourcePins
			if isAgent {
				pins = agentRun.ResourcePins
			}
			if !s.requireResourcePinsCurrent(w, pins) {
				return
			}
		}
		if isAgent {
			s.handleUnifiedAgentRunAction(w, r, agentRun, parts)
			return
		}
		s.handleUnifiedWorkflowRunAction(w, r, workflowRun, parts)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if len(parts) == 1 {
		if runItemWantsWorkbench(r) {
			if isAgent {
				actions := unifiedAgentRunActions(agentRun.ID, s.agentRunActions(agentRun))
				delivery := s.agentRunDeliveryManifest(agentRun)
				writeJSON(w, runWorkbenchResponse{
					Task:        s.runCollectionItemForAgent(agentRun),
					Run:         summarizeAgentRunForAPI(agentRun),
					FinalOutput: boundedWorkbenchText(agentRun.Output, 64*1024),
					Actions:     actions,
					Timeline:    s.taskTimelineForAgent(agentRun, agentRunQuery{Limit: 40}),
					TokenUsage:  retainedAgentRunUsage(agentRun),
					Execution:   agentRunWorkbenchExecution(agentRun, delivery),
				})
				return
			}
			actions := unifiedWorkflowRunActions(workflowRun.ID, s.workflowRunActions(workflowRun))
			delivery := s.workflowRunDeliveryManifest(workflowRun)
			writeJSON(w, runWorkbenchResponse{
				Task:        s.runCollectionItemForWorkflow(workflowRun),
				Run:         summarizeWorkflowRunForAPI(workflowRun),
				FinalOutput: workflowWorkbenchFinalOutput(workflowRun),
				Actions:     actions,
				Timeline:    s.taskTimelineForWorkflow(workflowRun, workflowRunQuery{Limit: 40}),
				TokenUsage:  retainedWorkflowRunUsage(workflowRun),
				Execution:   workflowRunWorkbenchExecution(workflowRun, delivery),
			})
			return
		}
		if isAgent {
			s.handleAgentRunItem(w, runRequestWithPath(r, "/api/agent-runs/"+agentRun.ID))
			return
		}
		s.handleWorkflowRunItem(w, runRequestWithPath(r, "/api/workflow-runs/"+workflowRun.ID))
		return
	}
	switch parts[1] {
	case "context":
		if isAgent {
			writeJSON(w, agentRunContext(agentRun))
			return
		}
		if isWorkflow {
			writeJSON(w, workflowRunContext(workflowRun))
			return
		}
	case "artifacts":
		query := workflowRunArtifactQueryFromRequest(r)
		if isAgent {
			writeJSON(w, s.agentRunArtifactsResponse(agentRun, query))
			return
		}
		if isWorkflow {
			writeJSON(w, s.workflowRunArtifactsResponse(workflowRun, query))
			return
		}
	case "actions":
		if len(parts) != 2 {
			break
		}
		if isAgent {
			actions := unifiedAgentRunActions(agentRun.ID, s.agentRunActions(agentRun))
			if !runActionsWantsEnvelope(r) {
				writeJSON(w, actions)
				return
			}
			discovery := agentRunActionDiscoveryFor(agentRun, actions)
			applyUnifiedAgentRunDiscoveryPaths(&discovery, agentRun.ID)
			writeJSON(w, discovery)
			return
		}
		actions := unifiedWorkflowRunActions(workflowRun.ID, s.workflowRunActions(workflowRun))
		if !runActionsWantsEnvelope(r) {
			writeJSON(w, actions)
			return
		}
		discovery := workflowRunActionDiscoveryFor(workflowRun, actions)
		applyUnifiedWorkflowRunDiscoveryPaths(&discovery, workflowRun.ID)
		writeJSON(w, discovery)
		return
	case "timeline":
		if isAgent {
			writeJSON(w, s.taskTimelineForAgent(agentRun, agentRunQueryFromRequest(r)))
			return
		}
		writeJSON(w, s.taskTimelineForWorkflow(workflowRun, workflowRunQueryFromRequest(r)))
		return
	case "delivery":
		if len(parts) != 2 {
			break
		}
		if isAgent {
			writeJSON(w, s.agentRunDeliveryManifest(agentRun))
			return
		}
		writeJSON(w, s.workflowRunDeliveryManifest(workflowRun))
		return
	default:
		if isAgent {
			s.handleAgentRunItem(w, runRequestWithPath(r, "/api/agent-runs/"+strings.Join(parts, "/")))
			return
		}
		s.handleWorkflowRunItem(w, runRequestWithPath(r, "/api/workflow-runs/"+strings.Join(parts, "/")))
		return
	}
	http.NotFound(w, r)
}

func runItemWantsWorkbench(r *http.Request) bool {
	if r == nil || r.URL == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("view")), "workbench")
}

func runRequestWithPath(r *http.Request, path string) *http.Request {
	clone := r.Clone(r.Context())
	urlCopy := *r.URL
	urlCopy.Path = path
	clone.URL = &urlCopy
	return clone
}

func unifiedAgentRunActions(runID string, actions []agentRunAction) []agentRunAction {
	out := append([]agentRunAction(nil), actions...)
	base := unifiedRunBasePath(runID)
	for i := range out {
		out[i].Path = unifiedRunActionPath(runID, out[i].Name)
		out[i].StreamPath = unifiedRunActionStreamPath(runID, out[i].Name, out[i].StreamPath)
		out[i].EventsPath = base + "/events/stream"
	}
	return out
}

func unifiedWorkflowRunActions(runID string, actions []workflowRunAction) []workflowRunAction {
	out := append([]workflowRunAction(nil), actions...)
	base := unifiedRunBasePath(runID)
	for i := range out {
		out[i].Path = unifiedRunActionPath(runID, out[i].Name)
		out[i].StreamPath = unifiedRunActionStreamPath(runID, out[i].Name, out[i].StreamPath)
		out[i].EventsPath = base + "/events/stream"
	}
	return out
}

func applyUnifiedAgentRunDiscoveryPaths(discovery *agentRunActionDiscovery, runID string) {
	if discovery == nil {
		return
	}
	base := unifiedRunBasePath(runID)
	discovery.EventsPath = base + "/events/stream"
	discovery.TimelinePath = base + "/timeline"
	discovery.ReplayPath = base + "/replay"
	discovery.ActionsPath = base + "/actions"
}

func applyUnifiedWorkflowRunDiscoveryPaths(discovery *workflowRunActionDiscovery, runID string) {
	if discovery == nil {
		return
	}
	base := unifiedRunBasePath(runID)
	discovery.EventsPath = base + "/events/stream"
	discovery.TimelinePath = base + "/timeline"
	discovery.ReplayPath = base + "/replay"
	discovery.ActionsPath = base + "/actions"
}

func (s *Server) handleUnifiedAgentRunAction(w http.ResponseWriter, r *http.Request, run session.AgentRunSnapshot, parts []string) {
	action, stream := unifiedAgentRunActionForPath(s.agentRunActions(run), parts)
	if action == nil {
		http.NotFound(w, r)
		return
	}
	if !action.Available {
		http.Error(w, firstWorkflowRunQueryValue(action.Reason, "run action is not available"), http.StatusConflict)
		return
	}
	target := action.Path
	if stream {
		target = action.StreamPath
	}
	if strings.TrimSpace(target) == "" {
		http.NotFound(w, r)
		return
	}
	s.handleAgentRunItem(w, runRequestWithPath(r, target))
}

func (s *Server) handleUnifiedWorkflowRunAction(w http.ResponseWriter, r *http.Request, run session.WorkflowRunSnapshot, parts []string) {
	action, stream := unifiedWorkflowRunActionForPath(s.workflowRunActions(run), parts)
	if action == nil {
		http.NotFound(w, r)
		return
	}
	if !action.Available {
		http.Error(w, firstWorkflowRunQueryValue(action.Reason, "run action is not available"), http.StatusConflict)
		return
	}
	target := action.Path
	if stream {
		target = action.StreamPath
	}
	if strings.TrimSpace(target) == "" {
		http.NotFound(w, r)
		return
	}
	s.handleWorkflowRunItem(w, runRequestWithPath(r, target))
}

func unifiedAgentRunActionForPath(actions []agentRunAction, parts []string) (*agentRunAction, bool) {
	if len(parts) != 3 && !(len(parts) == 4 && parts[3] == "stream") {
		return nil, false
	}
	name := normalizeUnifiedRunActionName(parts[2])
	for i := range actions {
		if normalizeUnifiedRunActionName(actions[i].Name) == name {
			return &actions[i], len(parts) == 4
		}
	}
	return nil, false
}

func unifiedWorkflowRunActionForPath(actions []workflowRunAction, parts []string) (*workflowRunAction, bool) {
	if len(parts) != 3 && !(len(parts) == 4 && parts[3] == "stream") {
		return nil, false
	}
	name := normalizeUnifiedRunActionName(parts[2])
	for i := range actions {
		if normalizeUnifiedRunActionName(actions[i].Name) == name {
			return &actions[i], len(parts) == 4
		}
	}
	return nil, false
}

func normalizeUnifiedRunActionName(name string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(name)), "-", "_")
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
	resp := runContextResponseFromSamples(run.ID, "agent", run.Status, run.AgentID, run.Mode, "", samples)
	resp.TargetID = run.TargetID
	return resp
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
	resp := runContextResponseFromSamples(run.ID, "workflow", run.Status, runCollectionWorkflowPrimaryAgent(run), runCollectionWorkflowPrimaryMode(run), run.Name, samples)
	resp.TargetID = run.TargetID
	return resp
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
