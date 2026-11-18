package api

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/session"
)

func (s *Server) handleRunCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		snapshot := s.sessionSnapshotWithApprovalRisk()
		writeJSON(w, s.runCollectionResponseFor(snapshot.AgentRuns, snapshot.WorkflowRuns, runCollectionQueryFromRequest(r)))
	case http.MethodPost:
		// V3 uses one task collection for creation and durable history.
		s.handleRun(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

type runCollectionResponse struct {
	Runs    []TaskRecord          `json:"runs"`
	Filters runCollectionQuery    `json:"filters,omitempty"`
	Counts  runCollectionRunCount `json:"counts"`
	Facets  runCollectionFacets   `json:"facets,omitempty"`
}

// TaskRecord is the stable task-facing contract layered over durable Agent and
// workflow run snapshots. The underlying snapshots remain the source of truth.
type TaskRecord struct {
	ID            string                `json:"id"`
	Type          string                `json:"type"`
	Kind          string                `json:"kind"`
	Title         string                `json:"title"`
	Name          string                `json:"name,omitempty"`
	Status        string                `json:"status,omitempty"`
	Request       string                `json:"request,omitempty"`
	Summary       string                `json:"summary,omitempty"`
	AgentID       string                `json:"agent_id,omitempty"`
	Mode          string                `json:"mode,omitempty"`
	Workflow      string                `json:"workflow,omitempty"`
	TargetID      string                `json:"target_id,omitempty"`
	StartedAt     string                `json:"started_at,omitempty"`
	UpdatedAt     string                `json:"updated_at,omitempty"`
	CompletedAt   string                `json:"completed_at,omitempty"`
	RetryOf       string                `json:"retry_of,omitempty"`
	Attempt       int                   `json:"attempt,omitempty"`
	ScheduleID    string                `json:"schedule_id,omitempty"`
	ScheduleJobID string                `json:"schedule_job_id,omitempty"`
	ResourcePins  []session.ResourcePin `json:"resource_pins,omitempty"`

	NeedsAction bool                        `json:"needs_action"`
	Resumable   bool                        `json:"resumable"`
	Recommended string                      `json:"recommended_action,omitempty"`
	HasError    bool                        `json:"has_error"`
	Quality     *workflowRunQualitySummary  `json:"quality,omitempty"`
	Actions     *runCollectionActionSummary `json:"actions_summary,omitempty"`

	DetailPath    string `json:"detail_path,omitempty"`
	EventsPath    string `json:"events_path,omitempty"`
	EventsURL     string `json:"events_url,omitempty"`
	TimelinePath  string `json:"timeline_path,omitempty"`
	ReplayPath    string `json:"replay_path,omitempty"`
	EvidencePath  string `json:"evidence_path,omitempty"`
	ArtifactsPath string `json:"artifacts_path,omitempty"`
	ContextPath   string `json:"context_path,omitempty"`
	ActionsPath   string `json:"actions_path,omitempty"`
	DiffsPath     string `json:"diffs_path,omitempty"`
	DeliveryPath  string `json:"delivery_path,omitempty"`
	ExportPath    string `json:"export_path,omitempty"`
	Source        string `json:"source,omitempty"`
}

// Keep the internal name as an alias while callers migrate to TaskRecord.
type runCollectionItem = TaskRecord

type runCollectionQuery struct {
	Type            string `json:"type,omitempty"`
	AgentID         string `json:"agent_id,omitempty"`
	Mode            string `json:"mode,omitempty"`
	Workflow        string `json:"workflow,omitempty"`
	Status          string `json:"status,omitempty"`
	ToolName        string `json:"tool_name,omitempty"`
	Action          string `json:"action,omitempty"`
	Query           string `json:"query,omitempty"`
	RetryOf         string `json:"retry_of,omitempty"`
	Limit           int    `json:"limit,omitempty"`
	ActiveOnly      bool   `json:"active_only,omitempty"`
	TerminalOnly    bool   `json:"terminal_only,omitempty"`
	NeedsActionOnly bool   `json:"needs_action_only,omitempty"`
	ErrorsOnly      bool   `json:"errors_only,omitempty"`
}

type runCollectionRunCount struct {
	Total       int `json:"total"`
	Matched     int `json:"matched"`
	Returned    int `json:"returned"`
	Agent       int `json:"agent"`
	Workflow    int `json:"workflow"`
	Active      int `json:"active"`
	Terminal    int `json:"terminal"`
	NeedsAction int `json:"needs_action"`
	Errors      int `json:"errors"`
}

type runCollectionFacets struct {
	Types     []runCollectionFacet `json:"types,omitempty"`
	Statuses  []runCollectionFacet `json:"statuses,omitempty"`
	Agents    []runCollectionFacet `json:"agents,omitempty"`
	Modes     []runCollectionFacet `json:"modes,omitempty"`
	Workflows []runCollectionFacet `json:"workflows,omitempty"`
	Tools     []runCollectionFacet `json:"tools,omitempty"`
	Actions   []runCollectionFacet `json:"actions,omitempty"`
}

type runCollectionFacet struct {
	Value string `json:"value"`
	Label string `json:"label,omitempty"`
	Count int    `json:"count"`
}

type runCollectionActionSummary struct {
	Total          int                            `json:"total,omitempty"`
	Available      int                            `json:"available,omitempty"`
	NeedsBody      int                            `json:"needs_body,omitempty"`
	WithRisk       int                            `json:"with_risk,omitempty"`
	Recommended    string                         `json:"recommended,omitempty"`
	AvailableNames []string                       `json:"available_names,omitempty"`
	AvailableItems []runCollectionAvailableAction `json:"available_items,omitempty"`
}

type runCollectionAvailableAction struct {
	Name               string `json:"name"`
	Kind               string `json:"kind,omitempty"`
	Label              string `json:"label,omitempty"`
	Method             string `json:"method,omitempty"`
	Path               string `json:"path,omitempty"`
	StreamPath         string `json:"stream_path,omitempty"`
	EventsPath         string `json:"events_path,omitempty"`
	SupportsBackground bool   `json:"supports_background,omitempty"`
	SupportsStream     bool   `json:"supports_stream,omitempty"`
	AcceptsBody        bool   `json:"accepts_body,omitempty"`
	RequiresBody       bool   `json:"requires_body,omitempty"`
	Destructive        bool   `json:"destructive,omitempty"`
}

func (s *Server) runCollectionResponseFor(agentRuns []session.AgentRunSnapshot, workflowRuns []session.WorkflowRunSnapshot, query runCollectionQuery) runCollectionResponse {
	counts := runCollectionRunCount{
		Total:    len(agentRuns) + len(workflowRuns),
		Agent:    len(agentRuns),
		Workflow: len(workflowRuns),
	}
	facets := s.runCollectionFacetsFor(agentRuns, workflowRuns)
	matched := make([]TaskRecord, 0, len(agentRuns)+len(workflowRuns))
	for _, run := range agentRuns {
		if agentRunCollectionRunActive(run) {
			counts.Active++
		}
		if agentRunCollectionRunTerminal(run) {
			counts.Terminal++
		}
		if agentRunCollectionRunNeedsAction(run) {
			counts.NeedsAction++
		}
		if agentRunCollectionRunHasError(run) {
			counts.Errors++
		}
		if !s.runCollectionAgentRunMatches(run, query) {
			continue
		}
		matched = append(matched, s.runCollectionItemForAgent(run))
	}
	for _, run := range workflowRuns {
		if workflowRunCollectionRunActive(run) {
			counts.Active++
		}
		if workflowRunCollectionRunTerminal(run) {
			counts.Terminal++
		}
		if workflowRunCollectionRunNeedsAction(run) {
			counts.NeedsAction++
		}
		if workflowRunCollectionRunHasError(run) {
			counts.Errors++
		}
		if !s.runCollectionWorkflowRunMatches(run, query) {
			continue
		}
		matched = append(matched, s.runCollectionItemForWorkflow(run))
	}
	sort.SliceStable(matched, func(i, j int) bool {
		return runCollectionItemNewer(matched[i], matched[j])
	})
	counts.Matched = len(matched)
	if query.Limit > 0 && len(matched) > query.Limit {
		matched = append([]TaskRecord(nil), matched[:query.Limit]...)
	}
	counts.Returned = len(matched)
	return runCollectionResponse{Runs: matched, Filters: query, Counts: counts, Facets: facets}
}

func runCollectionQueryFromRequest(r *http.Request) runCollectionQuery {
	if r == nil || r.URL == nil {
		return runCollectionQuery{}
	}
	values := r.URL.Query()
	limit, _ := strconv.Atoi(strings.TrimSpace(values.Get("limit")))
	if limit < 0 {
		limit = 0
	}
	if limit > 500 {
		limit = 500
	}
	return runCollectionQuery{
		Type:            normalizeWorkflowRunQueryToken(firstWorkflowRunQueryValue(values.Get("type"), values.Get("run_type"), values.Get("source"))),
		AgentID:         strings.TrimSpace(firstWorkflowRunQueryValue(values.Get("agent_id"), values.Get("agent"))),
		Mode:            strings.TrimSpace(values.Get("mode")),
		Workflow:        strings.TrimSpace(firstWorkflowRunQueryValue(values.Get("workflow"), values.Get("name"))),
		Status:          normalizeWorkflowRunQueryToken(values.Get("status")),
		ToolName:        strings.TrimSpace(firstWorkflowRunQueryValue(values.Get("tool_name"), values.Get("tool"))),
		Action:          strings.TrimSpace(firstWorkflowRunQueryValue(values.Get("action"), values.Get("action_name"), values.Get("actionName"))),
		Query:           strings.TrimSpace(firstWorkflowRunQueryValue(values.Get("q"), values.Get("query"), values.Get("search"))),
		RetryOf:         strings.TrimSpace(firstWorkflowRunQueryValue(values.Get("retry_of"), values.Get("retryOf"))),
		Limit:           limit,
		ActiveOnly:      workflowRunQueryBool(firstWorkflowRunQueryValue(values.Get("active"), values.Get("running"))),
		TerminalOnly:    workflowRunQueryBool(firstWorkflowRunQueryValue(values.Get("terminal"), values.Get("done"))),
		NeedsActionOnly: workflowRunQueryBool(values.Get("needs_action")) || workflowRunQueryBool(values.Get("pending")),
		ErrorsOnly:      workflowRunQueryBool(values.Get("errors_only")) || workflowRunQueryBool(values.Get("error")),
	}
}

func (s *Server) runCollectionAgentRunMatches(run session.AgentRunSnapshot, query runCollectionQuery) bool {
	if query.Type != "" && query.Type != "agent" && query.Type != "agent_run" {
		return false
	}
	if query.Workflow != "" {
		return false
	}
	if query.Action != "" && !runCollectionAgentRunHasAvailableAction(s.agentRunActions(run), query.Action) {
		return false
	}
	return agentRunCollectionRunMatches(run, agentRunCollectionQuery{
		AgentID:         query.AgentID,
		Mode:            query.Mode,
		Status:          query.Status,
		ToolName:        query.ToolName,
		Query:           query.Query,
		RetryOf:         query.RetryOf,
		ActiveOnly:      query.ActiveOnly,
		TerminalOnly:    query.TerminalOnly,
		NeedsActionOnly: query.NeedsActionOnly,
		ErrorsOnly:      query.ErrorsOnly,
	})
}

func (s *Server) runCollectionWorkflowRunMatches(run session.WorkflowRunSnapshot, query runCollectionQuery) bool {
	if query.Type != "" && query.Type != "workflow" && query.Type != "workflow_run" {
		return false
	}
	if query.Workflow != "" && !strings.EqualFold(strings.TrimSpace(run.Name), query.Workflow) {
		return false
	}
	if query.AgentID != "" && !runCollectionWorkflowRunHasAgent(run, query.AgentID) {
		return false
	}
	if query.Mode != "" && !runCollectionWorkflowRunHasMode(run, query.Mode) {
		return false
	}
	if query.ToolName != "" && !runCollectionWorkflowRunHasTool(run, query.ToolName) {
		return false
	}
	if query.Action != "" && !runCollectionWorkflowRunHasAvailableAction(s.workflowRunActions(run), query.Action) {
		return false
	}
	return workflowRunCollectionRunMatches(run, workflowRunCollectionQuery{
		Status:          query.Status,
		Query:           query.Query,
		RetryOf:         query.RetryOf,
		ActiveOnly:      query.ActiveOnly,
		TerminalOnly:    query.TerminalOnly,
		NeedsActionOnly: query.NeedsActionOnly,
		ErrorsOnly:      query.ErrorsOnly,
	})
}

func runCollectionAgentRunHasAvailableAction(actions []agentRunAction, actionName string) bool {
	actionName = strings.TrimSpace(actionName)
	if actionName == "" {
		return true
	}
	for _, action := range actions {
		if action.Available && strings.EqualFold(strings.TrimSpace(action.Name), actionName) {
			return true
		}
	}
	return false
}

func runCollectionWorkflowRunHasAvailableAction(actions []workflowRunAction, actionName string) bool {
	actionName = strings.TrimSpace(actionName)
	if actionName == "" {
		return true
	}
	for _, action := range actions {
		if action.Available && strings.EqualFold(strings.TrimSpace(action.Name), actionName) {
			return true
		}
	}
	return false
}

func (s *Server) runCollectionItemForAgent(run session.AgentRunSnapshot) TaskRecord {
	agentID := strings.TrimSpace(run.AgentID)
	name := agentID
	if name == "" {
		name = "Agent run"
	}
	base := unifiedRunBasePath(run.ID)
	actions := s.runCollectionAgentActionSummary(run)
	needsAction := agentRunCollectionRunNeedsAction(run)
	return TaskRecord{
		ID:            run.ID,
		Type:          "agent",
		Kind:          "agent",
		Title:         name,
		Name:          name,
		Status:        run.Status,
		Request:       run.Request,
		Summary:       agentRunSnapshotSummary(run),
		AgentID:       agentID,
		Mode:          run.Mode,
		TargetID:      run.TargetID,
		StartedAt:     run.StartedAt,
		UpdatedAt:     run.UpdatedAt,
		CompletedAt:   run.CompletedAt,
		RetryOf:       run.RetryOf,
		Attempt:       run.Attempt,
		ScheduleID:    run.ScheduleID,
		ScheduleJobID: run.ScheduleJobID,
		ResourcePins:  session.CopyResourcePins(run.ResourcePins),
		NeedsAction:   needsAction,
		Resumable:     needsAction && runCollectionActionSummaryResumable(actions),
		Recommended:   runCollectionActionSummaryRecommended(actions),
		HasError:      agentRunCollectionRunHasError(run),
		Actions:       actions,
		DetailPath:    base,
		EventsPath:    base + "/events/stream",
		EventsURL:     base + "/events/stream",
		TimelinePath:  base + "/timeline",
		ReplayPath:    base + "/replay",
		ArtifactsPath: base + "/artifacts",
		ContextPath:   base + "/context",
		ActionsPath:   base + "/actions",
		DiffsPath:     base + "/diffs",
		DeliveryPath:  base + "/delivery",
		ExportPath:    base + "/export",
		Source:        "agent_run",
	}
}

func (s *Server) runCollectionItemForWorkflow(run session.WorkflowRunSnapshot) TaskRecord {
	base := unifiedRunBasePath(run.ID)
	quality := workflowRunQualitySummaryFor(run)
	actions := s.runCollectionWorkflowActionSummary(run)
	needsAction := workflowRunCollectionRunNeedsAction(run)
	title := strings.TrimSpace(run.Name)
	if title == "" {
		title = "Workflow run"
	}
	return TaskRecord{
		ID:            run.ID,
		Type:          "workflow",
		Kind:          "workflow",
		Title:         title,
		Name:          run.Name,
		Status:        effectiveWorkflowRunStatus(run),
		Request:       run.Request,
		Summary:       runCollectionWorkflowSummary(run),
		AgentID:       runCollectionWorkflowPrimaryAgent(run),
		Mode:          runCollectionWorkflowPrimaryMode(run),
		Workflow:      run.Name,
		TargetID:      run.TargetID,
		StartedAt:     run.StartedAt,
		UpdatedAt:     run.UpdatedAt,
		CompletedAt:   run.CompletedAt,
		RetryOf:       run.RetryOf,
		Attempt:       run.Attempt,
		ScheduleID:    run.ScheduleID,
		ScheduleJobID: run.ScheduleJobID,
		ResourcePins:  session.CopyResourcePins(run.ResourcePins),
		NeedsAction:   needsAction,
		Resumable:     needsAction && runCollectionActionSummaryResumable(actions),
		Recommended:   runCollectionActionSummaryRecommended(actions),
		HasError:      workflowRunCollectionRunHasError(run),
		Quality:       &quality,
		Actions:       actions,
		DetailPath:    base,
		EventsPath:    base + "/events/stream",
		EventsURL:     base + "/events/stream",
		TimelinePath:  base + "/timeline",
		ReplayPath:    base + "/replay",
		EvidencePath:  base + "/evidence",
		ArtifactsPath: base + "/artifacts",
		ContextPath:   base + "/context",
		ActionsPath:   base + "/actions",
		DiffsPath:     base + "/diffs",
		DeliveryPath:  base + "/delivery",
		ExportPath:    base + "/export",
		Source:        "workflow_run",
	}
}

func unifiedRunBasePath(runID string) string {
	return "/api/runs/" + strings.TrimSpace(runID)
}

func runCollectionActionSummaryRecommended(summary *runCollectionActionSummary) string {
	if summary == nil {
		return ""
	}
	return strings.TrimSpace(summary.Recommended)
}

func runCollectionActionSummaryResumable(summary *runCollectionActionSummary) bool {
	if summary == nil {
		return false
	}
	for _, action := range summary.AvailableItems {
		if action.Name != "cancel" {
			return true
		}
	}
	return false
}

func (s *Server) runCollectionAgentActionSummary(run session.AgentRunSnapshot) *runCollectionActionSummary {
	actions := s.agentRunActions(run)
	if len(actions) == 0 {
		return nil
	}
	counts := agentRunActionCounts(actions)
	return &runCollectionActionSummary{
		Total:          counts.Total,
		Available:      counts.Available,
		NeedsBody:      counts.NeedsBody,
		WithRisk:       counts.WithRisk,
		Recommended:    recommendedAgentRunAction(actions),
		AvailableNames: runCollectionAgentAvailableActionNames(actions),
		AvailableItems: runCollectionAgentAvailableActions(run.ID, actions),
	}
}

func (s *Server) runCollectionWorkflowActionSummary(run session.WorkflowRunSnapshot) *runCollectionActionSummary {
	actions := s.workflowRunActions(run)
	if len(actions) == 0 {
		return nil
	}
	counts := workflowRunActionCounts(actions)
	return &runCollectionActionSummary{
		Total:          counts.Total,
		Available:      counts.Available,
		NeedsBody:      counts.NeedsBody,
		WithRisk:       counts.WithRisk,
		Recommended:    recommendedWorkflowRunAction(actions),
		AvailableNames: runCollectionWorkflowAvailableActionNames(actions),
		AvailableItems: runCollectionWorkflowAvailableActions(run.ID, actions),
	}
}

func runCollectionAgentAvailableActionNames(actions []agentRunAction) []string {
	names := make([]string, 0, len(actions))
	for _, action := range actions {
		if action.Available {
			names = append(names, action.Name)
		}
	}
	return names
}

func runCollectionWorkflowAvailableActionNames(actions []workflowRunAction) []string {
	names := make([]string, 0, len(actions))
	for _, action := range actions {
		if action.Available {
			names = append(names, action.Name)
		}
	}
	return names
}

func runCollectionAgentAvailableActions(runID string, actions []agentRunAction) []runCollectionAvailableAction {
	items := make([]runCollectionAvailableAction, 0, len(actions))
	for _, action := range actions {
		if !action.Available {
			continue
		}
		items = append(items, runCollectionAvailableAction{
			Name:               action.Name,
			Kind:               action.Kind,
			Label:              action.Label,
			Method:             action.Method,
			Path:               unifiedRunActionPath(runID, action.Name),
			StreamPath:         unifiedRunActionStreamPath(runID, action.Name, action.StreamPath),
			EventsPath:         unifiedRunBasePath(runID) + "/events/stream",
			SupportsBackground: action.SupportsBackground,
			SupportsStream:     action.SupportsStream,
			AcceptsBody:        action.AcceptsBody,
			RequiresBody:       action.RequiresBody,
			Destructive:        action.Destructive,
		})
	}
	return items
}

func runCollectionWorkflowAvailableActions(runID string, actions []workflowRunAction) []runCollectionAvailableAction {
	items := make([]runCollectionAvailableAction, 0, len(actions))
	for _, action := range actions {
		if !action.Available {
			continue
		}
		items = append(items, runCollectionAvailableAction{
			Name:               action.Name,
			Kind:               action.Kind,
			Label:              action.Label,
			Method:             action.Method,
			Path:               unifiedRunActionPath(runID, action.Name),
			StreamPath:         unifiedRunActionStreamPath(runID, action.Name, action.StreamPath),
			EventsPath:         unifiedRunBasePath(runID) + "/events/stream",
			SupportsBackground: action.SupportsBackground,
			SupportsStream:     action.SupportsStream,
			AcceptsBody:        action.AcceptsBody,
			RequiresBody:       action.RequiresBody,
			Destructive:        action.Destructive,
		})
	}
	return items
}

func unifiedRunActionPath(runID, actionName string) string {
	return unifiedRunBasePath(runID) + "/actions/" + strings.TrimSpace(actionName)
}

func unifiedRunActionStreamPath(runID, actionName, sourceStreamPath string) string {
	if strings.TrimSpace(sourceStreamPath) == "" {
		return ""
	}
	return unifiedRunActionPath(runID, actionName) + "/stream"
}

func runCollectionWorkflowSummary(run session.WorkflowRunSnapshot) string {
	return firstWorkflowRunQueryValue(run.Summary, run.ApprovalPrompt, run.NextStage, run.Status)
}

func runCollectionWorkflowPrimaryAgent(run session.WorkflowRunSnapshot) string {
	if strings.TrimSpace(run.PendingAgentID) != "" {
		return strings.TrimSpace(run.PendingAgentID)
	}
	for i := len(run.Events) - 1; i >= 0; i-- {
		if strings.TrimSpace(run.Events[i].AgentID) != "" {
			return strings.TrimSpace(run.Events[i].AgentID)
		}
	}
	for i := len(run.CompletedStages) - 1; i >= 0; i-- {
		if strings.TrimSpace(run.CompletedStages[i].AgentID) != "" {
			return strings.TrimSpace(run.CompletedStages[i].AgentID)
		}
	}
	return ""
}

func runCollectionWorkflowPrimaryMode(run session.WorkflowRunSnapshot) string {
	for i := len(run.Events) - 1; i >= 0; i-- {
		if strings.TrimSpace(run.Events[i].Mode) != "" {
			return strings.TrimSpace(run.Events[i].Mode)
		}
	}
	return ""
}

func runCollectionWorkflowRunHasAgent(run session.WorkflowRunSnapshot, agentID string) bool {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(run.PendingAgentID), agentID) {
		return true
	}
	for _, stage := range run.CompletedStages {
		if strings.EqualFold(strings.TrimSpace(stage.AgentID), agentID) {
			return true
		}
	}
	for _, event := range run.Events {
		if strings.EqualFold(strings.TrimSpace(event.AgentID), agentID) {
			return true
		}
	}
	return false
}

func runCollectionWorkflowRunHasMode(run session.WorkflowRunSnapshot, mode string) bool {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		return true
	}
	for _, event := range run.Events {
		if strings.EqualFold(strings.TrimSpace(event.Mode), mode) {
			return true
		}
	}
	return false
}

func runCollectionWorkflowRunHasTool(run session.WorkflowRunSnapshot, toolName string) bool {
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(run.PendingToolName), toolName) {
		return true
	}
	for _, event := range run.Events {
		if strings.EqualFold(strings.TrimSpace(event.ToolName), toolName) {
			return true
		}
	}
	for _, stage := range run.CompletedStages {
		if strings.EqualFold(strings.TrimSpace(stage.Tool), toolName) {
			return true
		}
		for _, result := range stage.Result.ToolResults {
			if strings.EqualFold(strings.TrimSpace(result.ToolName), toolName) {
				return true
			}
		}
	}
	for _, artifact := range run.Artifacts {
		if strings.EqualFold(strings.TrimSpace(artifact.ToolName), toolName) {
			return true
		}
	}
	return false
}

func (s *Server) runCollectionFacetsFor(agentRuns []session.AgentRunSnapshot, workflowRuns []session.WorkflowRunSnapshot) runCollectionFacets {
	types := map[string]int{}
	statuses := map[string]int{}
	agents := map[string]int{}
	modes := map[string]int{}
	workflows := map[string]int{}
	tools := map[string]int{}
	actions := map[string]int{}
	for _, run := range agentRuns {
		runTypes := map[string]struct{}{}
		runStatuses := map[string]struct{}{}
		runAgents := map[string]struct{}{}
		runModes := map[string]struct{}{}
		runTools := map[string]struct{}{}
		runActions := map[string]struct{}{}
		addRunCollectionFacetValue(runTypes, "agent")
		addRunCollectionFacetValue(runStatuses, normalizeWorkflowRunQueryToken(run.Status))
		addRunCollectionFacetValue(runAgents, run.AgentID)
		addRunCollectionFacetValue(runModes, run.Mode)
		addRunCollectionFacetValue(runTools, run.PendingTool)
		for _, action := range s.agentRunActions(run) {
			if action.Available {
				addRunCollectionFacetValue(runActions, action.Name)
			}
		}
		for _, event := range run.Events {
			addRunCollectionFacetValue(runAgents, event.AgentID)
			addRunCollectionFacetValue(runModes, event.Mode)
			addRunCollectionFacetValue(runTools, event.ToolName)
		}
		if run.Result != nil {
			addRunCollectionFacetValue(runAgents, run.Result.AgentID)
			addRunCollectionFacetValue(runModes, run.Result.Mode)
			for _, result := range run.Result.ToolResults {
				addRunCollectionFacetValue(runTools, result.ToolName)
			}
		}
		incrementRunCollectionFacets(types, runTypes)
		incrementRunCollectionFacets(statuses, runStatuses)
		incrementRunCollectionFacets(agents, runAgents)
		incrementRunCollectionFacets(modes, runModes)
		incrementRunCollectionFacets(tools, runTools)
		incrementRunCollectionFacets(actions, runActions)
	}
	for _, run := range workflowRuns {
		runTypes := map[string]struct{}{}
		runStatuses := map[string]struct{}{}
		runAgents := map[string]struct{}{}
		runModes := map[string]struct{}{}
		runWorkflows := map[string]struct{}{}
		runTools := map[string]struct{}{}
		runActions := map[string]struct{}{}
		addRunCollectionFacetValue(runTypes, "workflow")
		addRunCollectionFacetValue(runStatuses, normalizeWorkflowRunQueryToken(run.Status))
		addRunCollectionFacetValue(runWorkflows, run.Name)
		addRunCollectionFacetValue(runAgents, run.PendingAgentID)
		addRunCollectionFacetValue(runTools, run.PendingToolName)
		for _, action := range s.workflowRunActions(run) {
			if action.Available {
				addRunCollectionFacetValue(runActions, action.Name)
			}
		}
		for _, stage := range run.CompletedStages {
			addRunCollectionFacetValue(runAgents, stage.AgentID)
			addRunCollectionFacetValue(runTools, stage.Tool)
			for _, result := range stage.Result.ToolResults {
				addRunCollectionFacetValue(runTools, result.ToolName)
			}
		}
		for _, event := range run.Events {
			addRunCollectionFacetValue(runAgents, event.AgentID)
			addRunCollectionFacetValue(runModes, event.Mode)
			addRunCollectionFacetValue(runTools, event.ToolName)
		}
		for _, artifact := range run.Artifacts {
			addRunCollectionFacetValue(runTools, artifact.ToolName)
		}
		incrementRunCollectionFacets(types, runTypes)
		incrementRunCollectionFacets(statuses, runStatuses)
		incrementRunCollectionFacets(agents, runAgents)
		incrementRunCollectionFacets(modes, runModes)
		incrementRunCollectionFacets(workflows, runWorkflows)
		incrementRunCollectionFacets(tools, runTools)
		incrementRunCollectionFacets(actions, runActions)
	}
	return runCollectionFacets{
		Types:     runCollectionFacetRows(types),
		Statuses:  runCollectionFacetRows(statuses),
		Agents:    runCollectionFacetRows(agents),
		Modes:     runCollectionFacetRows(modes),
		Workflows: runCollectionFacetRows(workflows),
		Tools:     runCollectionFacetRows(tools),
		Actions:   runCollectionFacetRows(actions),
	}
}

func addRunCollectionFacetValue(values map[string]struct{}, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	values[value] = struct{}{}
}

func incrementRunCollectionFacets(counts map[string]int, values map[string]struct{}) {
	for value := range values {
		counts[value]++
	}
}

func runCollectionFacetRows(values map[string]int) []runCollectionFacet {
	if len(values) == 0 {
		return nil
	}
	rows := make([]runCollectionFacet, 0, len(values))
	for value, count := range values {
		rows = append(rows, runCollectionFacet{
			Value: value,
			Label: runCollectionFacetLabel(value),
			Count: count,
		})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Count != rows[j].Count {
			return rows[i].Count > rows[j].Count
		}
		return strings.ToLower(rows[i].Value) < strings.ToLower(rows[j].Value)
	})
	return rows
}

func runCollectionFacetLabel(value string) string {
	value = strings.TrimSpace(value)
	switch value {
	case "agent":
		return "Agent"
	case "workflow":
		return "Workflow"
	case "awaiting_tool_approval":
		return "Awaiting tool approval"
	case "awaiting_input":
		return "Awaiting input"
	case "awaiting_approval":
		return "Awaiting approval"
	case "awaiting_sub_workflow":
		return "Awaiting sub-workflow"
	default:
		return value
	}
}

func runCollectionItemNewer(left, right runCollectionItem) bool {
	leftTime, leftRaw := runCollectionItemSortValue(left)
	rightTime, rightRaw := runCollectionItemSortValue(right)
	if !leftTime.IsZero() && !rightTime.IsZero() && !leftTime.Equal(rightTime) {
		return leftTime.After(rightTime)
	}
	if leftRaw != rightRaw {
		return leftRaw > rightRaw
	}
	if left.Type != right.Type {
		return left.Type < right.Type
	}
	return left.ID > right.ID
}

func runCollectionItemSortValue(item runCollectionItem) (time.Time, string) {
	raw := firstWorkflowRunQueryValue(item.UpdatedAt, item.CompletedAt, item.StartedAt)
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, raw
	}
	return parsed, raw
}
