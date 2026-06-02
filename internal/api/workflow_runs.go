package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/agent"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func (s *Server) handleWorkflowRunCollection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	snapshot := s.sessionSnapshotWithApprovalRisk()
	runs := snapshot.WorkflowRuns
	query := workflowRunCollectionQueryFromRequest(r)
	if !workflowRunCollectionWantsEnvelope(r, query) {
		writeJSON(w, runs)
		return
	}
	writeJSON(w, workflowRunCollectionResponseFor(runs, query))
}

func (s *Server) handleWorkflowRunItem(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/workflow-runs/"), "/"), "/")
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		http.NotFound(w, r)
		return
	}
	snapshot := s.sessionSnapshotWithApprovalRisk()
	run, ok := workflowRunByID(snapshot.WorkflowRuns, parts[0])
	if !ok {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodPost {
		s.handleWorkflowRunAction(w, r, run, parts)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	switch {
	case len(parts) == 1:
		if workflowRunQueryBool(firstWorkflowRunQueryValue(r.URL.Query().Get("summary"), r.URL.Query().Get("compact"))) {
			writeJSON(w, summarizeWorkflowRunForAPI(run))
			return
		}
		writeJSON(w, s.runtime.HydrateWorkflowRun(run))
	case len(parts) == 2 && parts[1] == "events":
		writeJSON(w, s.workflowRunEventsForQuery(run.Events, workflowRunQueryFromRequest(r)))
	case len(parts) == 2 && parts[1] == "context":
		writeJSON(w, workflowRunContext(run))
	case len(parts) == 3 && parts[1] == "events" && parts[2] == "stream":
		s.handleWorkflowRunEventsStream(w, r, run.ID)
	case len(parts) == 2 && parts[1] == "artifacts":
		query := workflowRunArtifactQueryFromRequest(r)
		writeJSON(w, s.workflowRunArtifactsForQuery(run.Artifacts, query))
	case len(parts) == 2 && parts[1] == "stages":
		query := workflowRunQueryFromRequest(r)
		writeJSON(w, s.workflowRunStagesForQuery(run.CompletedStages, query))
	case len(parts) == 2 && parts[1] == "replay":
		query := workflowRunQueryFromRequest(r)
		writeJSON(w, s.workflowRunReplay(run, query))
	case len(parts) == 2 && parts[1] == "evidence":
		writeJSON(w, workflowRunEvidenceView(run, workflowRunQueryFromRequest(r)))
	case len(parts) == 2 && parts[1] == "navigation":
		writeJSON(w, workflowRunReplayNavigationFor(run, workflowRunQueryFromRequest(r), s.workflowRunDiffs(run, workflowRunQuery{})))
	case len(parts) == 2 && parts[1] == "diffs":
		writeJSON(w, s.workflowRunDiffResponse(run, workflowRunQueryFromRequest(r)))
	case len(parts) == 2 && parts[1] == "export":
		s.handleWorkflowRunExport(w, r, run)
	case len(parts) == 2 && parts[1] == "timeline":
		writeJSON(w, s.workflowRunTimeline(run, workflowRunQueryFromRequest(r)))
	case len(parts) == 2 && parts[1] == "actions":
		actions := s.workflowRunActions(run)
		if runActionsWantsEnvelope(r) {
			writeJSON(w, workflowRunActionDiscoveryFor(run, actions))
			return
		}
		writeJSON(w, actions)
	case len(parts) == 3 && parts[1] == "stages":
		stageDetail, ok := s.workflowRunStageDetailFor(run, parts[2])
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, stageDetail)
	case len(parts) == 4 && parts[1] == "stages" && parts[3] == "events":
		writeJSON(w, s.workflowRunEventsForQuery(run.Events, workflowRunQueryFromRequest(r).withStage(parts[2])))
	case len(parts) == 4 && parts[1] == "stages" && parts[3] == "artifacts":
		query := workflowRunArtifactQueryFromRequest(r).withStage(parts[2])
		writeJSON(w, s.workflowRunArtifactsForQuery(run.Artifacts, query))
	default:
		http.NotFound(w, r)
	}
}

type workflowRunReplay struct {
	Run           session.WorkflowRunSnapshot            `json:"run"`
	Stages        []session.WorkflowRunStageSnapshot     `json:"stages,omitempty"`
	Events        []session.WorkflowRunEventSnapshot     `json:"events,omitempty"`
	Artifacts     []session.WorkflowRunArtifact          `json:"artifacts,omitempty"`
	Timeline      []workflowRunTimelineItem              `json:"timeline,omitempty"`
	Diffs         []runDiffItem                          `json:"diffs,omitempty"`
	Actions       []workflowRunAction                    `json:"actions,omitempty"`
	Filters       workflowRunQuery                       `json:"filters,omitempty"`
	Navigation    workflowRunReplayNavigation            `json:"navigation"`
	Counts        workflowRunReplayCounts                `json:"counts"`
	Quality       workflowRunQualitySummary              `json:"quality"`
	Collaboration []session.CollaborationMessageSnapshot `json:"collaboration,omitempty"`
	Blackboard    []session.BlackboardEntrySnapshot      `json:"blackboard,omitempty"`
	TeamState     agent.TeamState                        `json:"team_state,omitempty"`
}

type workflowRunQualitySummary struct {
	Status              string                    `json:"status"`
	Score               int                       `json:"score"`
	AcceptanceTotal     int                       `json:"acceptance_total,omitempty"`
	AcceptancePassed    int                       `json:"acceptance_passed,omitempty"`
	AcceptanceFailed    int                       `json:"acceptance_failed,omitempty"`
	VerificationTotal   int                       `json:"verification_total,omitempty"`
	VerificationPassed  int                       `json:"verification_passed,omitempty"`
	VerificationFailed  int                       `json:"verification_failed,omitempty"`
	VerificationUnknown int                       `json:"verification_unknown,omitempty"`
	EvidenceArtifacts   int                       `json:"evidence_artifacts,omitempty"`
	ErrorCount          int                       `json:"error_count,omitempty"`
	NeedsActionCount    int                       `json:"needs_action_count,omitempty"`
	UnmetCriteria       []workflowRunQualityIssue `json:"unmet_criteria,omitempty"`
	FailedValidations   []workflowRunQualityIssue `json:"failed_validations,omitempty"`
	Warnings            []string                  `json:"warnings,omitempty"`
}

type workflowRunQualityIssue struct {
	Stage    string `json:"stage,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Name     string `json:"name,omitempty"`
	Status   string `json:"status,omitempty"`
	Expected string `json:"expected,omitempty"`
	Actual   string `json:"actual,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

type workflowRunReplayNavigation struct {
	RunID         string                           `json:"run_id,omitempty"`
	Workflow      string                           `json:"workflow,omitempty"`
	CurrentStage  string                           `json:"current_stage,omitempty"`
	CurrentAnchor string                           `json:"current_anchor,omitempty"`
	CurrentIndex  int                              `json:"current_index"`
	PreviousStage string                           `json:"previous_stage,omitempty"`
	NextStage     string                           `json:"next_stage,omitempty"`
	PendingStage  string                           `json:"pending_stage,omitempty"`
	StageCount    int                              `json:"stage_count"`
	Stages        []workflowRunStageNavigationItem `json:"stages,omitempty"`
}

type workflowRunStageNavigationItem struct {
	Stage         string `json:"stage"`
	Anchor        string `json:"anchor"`
	Index         int    `json:"index"`
	PreviousStage string `json:"previous_stage,omitempty"`
	NextStage     string `json:"next_stage,omitempty"`
	Status        string `json:"status,omitempty"`
	NodeType      string `json:"node_type,omitempty"`
	AgentID       string `json:"agent_id,omitempty"`
	Skill         string `json:"skill,omitempty"`
	Tool          string `json:"tool,omitempty"`
	EventCount    int    `json:"event_count,omitempty"`
	ArtifactCount int    `json:"artifact_count,omitempty"`
	DiffCount     int    `json:"diff_count,omitempty"`
	ErrorCount    int    `json:"error_count,omitempty"`
	NeedsAction   bool   `json:"needs_action,omitempty"`
	HasError      bool   `json:"has_error,omitempty"`
	Completed     bool   `json:"completed,omitempty"`
	Pending       bool   `json:"pending,omitempty"`
	Current       bool   `json:"current,omitempty"`
	DetailPath    string `json:"detail_path,omitempty"`
	EventsPath    string `json:"events_path,omitempty"`
	ArtifactsPath string `json:"artifacts_path,omitempty"`
	DiffsPath     string `json:"diffs_path,omitempty"`
	ReplayPath    string `json:"replay_path,omitempty"`
}

type workflowRunReplayCounts struct {
	Filtered              bool `json:"filtered"`
	StagesTotal           int  `json:"stages_total"`
	StagesFiltered        int  `json:"stages_filtered"`
	EventsTotal           int  `json:"events_total"`
	EventsFiltered        int  `json:"events_filtered"`
	ArtifactsTotal        int  `json:"artifacts_total"`
	ArtifactsFiltered     int  `json:"artifacts_filtered"`
	TimelineTotal         int  `json:"timeline_total"`
	TimelineFiltered      int  `json:"timeline_filtered"`
	DiffsTotal            int  `json:"diffs_total"`
	DiffsFiltered         int  `json:"diffs_filtered"`
	CollaborationTotal    int  `json:"collaboration_total"`
	CollaborationFiltered int  `json:"collaboration_filtered"`
	BlackboardTotal       int  `json:"blackboard_total"`
	BlackboardFiltered    int  `json:"blackboard_filtered"`
	ErrorsTotal           int  `json:"errors_total,omitempty"`
	NeedsActionTotal      int  `json:"needs_action_total,omitempty"`
}

type workflowRunCollectionResponse struct {
	Runs    []session.WorkflowRunSnapshot  `json:"runs"`
	Filters workflowRunCollectionQuery     `json:"filters,omitempty"`
	Counts  workflowRunCollectionRunCounts `json:"counts"`
}

type workflowRunCollectionQuery struct {
	Workflow        string `json:"workflow,omitempty"`
	Status          string `json:"status,omitempty"`
	Query           string `json:"query,omitempty"`
	RetryOf         string `json:"retry_of,omitempty"`
	Limit           int    `json:"limit,omitempty"`
	ActiveOnly      bool   `json:"active_only,omitempty"`
	TerminalOnly    bool   `json:"terminal_only,omitempty"`
	NeedsActionOnly bool   `json:"needs_action_only,omitempty"`
	ErrorsOnly      bool   `json:"errors_only,omitempty"`
	Summary         bool   `json:"summary,omitempty"`
}

type workflowRunCollectionRunCounts struct {
	Total       int `json:"total"`
	Matched     int `json:"matched"`
	Returned    int `json:"returned"`
	Active      int `json:"active"`
	Terminal    int `json:"terminal"`
	NeedsAction int `json:"needs_action"`
	Errors      int `json:"errors"`
}

type workflowRunAction struct {
	Name               string                  `json:"name"`
	Kind               string                  `json:"kind,omitempty"`
	Label              string                  `json:"label,omitempty"`
	Method             string                  `json:"method,omitempty"`
	Path               string                  `json:"path,omitempty"`
	StreamPath         string                  `json:"stream_path,omitempty"`
	EventsPath         string                  `json:"events_path,omitempty"`
	Available          bool                    `json:"available"`
	Durable            bool                    `json:"durable,omitempty"`
	Background         bool                    `json:"background,omitempty"`
	SupportsBackground bool                    `json:"supports_background,omitempty"`
	SupportsStream     bool                    `json:"supports_stream,omitempty"`
	AcceptsBody        bool                    `json:"accepts_body,omitempty"`
	RequiresBody       bool                    `json:"requires_body,omitempty"`
	BodySchema         map[string]any          `json:"body_schema,omitempty"`
	Destructive        bool                    `json:"destructive,omitempty"`
	Reason             string                  `json:"reason,omitempty"`
	Risk               *schema.ToolRiskProfile `json:"risk,omitempty"`
}

type workflowRunActionDiscovery struct {
	Meta          runActionDiscoveryMeta `json:"meta"`
	RunID         string                 `json:"run_id"`
	Name          string                 `json:"name,omitempty"`
	Status        string                 `json:"status,omitempty"`
	NeedsAction   bool                   `json:"needs_action"`
	NextStage     string                 `json:"next_stage,omitempty"`
	EventsPath    string                 `json:"events_path,omitempty"`
	TimelinePath  string                 `json:"timeline_path,omitempty"`
	ReplayPath    string                 `json:"replay_path,omitempty"`
	ActionsPath   string                 `json:"actions_path,omitempty"`
	Recommended   string                 `json:"recommended_action,omitempty"`
	Counts        runActionCounts        `json:"counts"`
	Actions       []workflowRunAction    `json:"actions"`
	SupportsRetry bool                   `json:"supports_retry"`
}

type workflowRunActionRequest struct {
	Background bool `json:"background,omitempty"`
}

type workflowRunBackgroundResponse struct {
	RunID     string                       `json:"run_id"`
	Name      string                       `json:"name,omitempty"`
	Status    string                       `json:"status,omitempty"`
	Action    string                       `json:"action,omitempty"`
	RetryOf   string                       `json:"retry_of,omitempty"`
	EventsURL string                       `json:"events_url,omitempty"`
	Run       *session.WorkflowRunSnapshot `json:"run,omitempty"`
}

type workflowRunQuery struct {
	Stage           string `json:"stage,omitempty"`
	ItemKind        string `json:"kind,omitempty"`
	EventType       string `json:"event_type,omitempty"`
	ArtifactKind    string `json:"artifact_kind,omitempty"`
	Status          string `json:"status,omitempty"`
	AgentID         string `json:"agent_id,omitempty"`
	ToolName        string `json:"tool_name,omitempty"`
	Query           string `json:"query,omitempty"`
	Limit           int    `json:"limit,omitempty"`
	Since           int    `json:"since,omitempty"`
	ErrorsOnly      bool   `json:"errors_only,omitempty"`
	NeedsActionOnly bool   `json:"needs_action_only,omitempty"`
	SuspendedOnly   bool   `json:"suspended_only,omitempty"`
	IncludeContent  bool   `json:"include_content,omitempty"`
}

type workflowRunTimelineItem struct {
	At                        string  `json:"at,omitempty"`
	Kind                      string  `json:"kind"`
	Type                      string  `json:"type,omitempty"`
	Stage                     string  `json:"stage,omitempty"`
	Status                    string  `json:"status,omitempty"`
	AgentID                   string  `json:"agent_id,omitempty"`
	ToolName                  string  `json:"tool_name,omitempty"`
	ToolCallID                string  `json:"tool_call_id,omitempty"`
	ArtifactID                string  `json:"artifact_id,omitempty"`
	Title                     string  `json:"title,omitempty"`
	Summary                   string  `json:"summary,omitempty"`
	Content                   string  `json:"content,omitempty"`
	Reason                    string  `json:"reason,omitempty"`
	Severity                  string  `json:"severity,omitempty"`
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
}

func (s *Server) workflowRunReplay(run session.WorkflowRunSnapshot, query workflowRunQuery) workflowRunReplay {
	if query.IncludeContent {
		run = s.runtime.HydrateWorkflowRun(run)
	}
	filter := session.CollaborationFilter{RunID: run.ID, Stage: query.Stage, Agent: query.AgentID, Limit: query.Limit}
	stages := s.workflowRunStagesForQuery(run.CompletedStages, query)
	events := s.workflowRunEventsForQuery(run.Events, query)
	artifacts := s.workflowRunArtifactsForQuery(run.Artifacts, query)
	timeline := s.workflowRunTimeline(run, query)
	diffs := s.workflowRunDiffs(run, query)
	collaboration := s.runtime.CollaborationMessages(filter)
	blackboard := s.runtime.BlackboardEntries(filter)
	return workflowRunReplay{
		Run:           run,
		Stages:        stages,
		Events:        events,
		Artifacts:     artifacts,
		Timeline:      timeline,
		Diffs:         diffs,
		Actions:       s.workflowRunActions(run),
		Filters:       query,
		Navigation:    workflowRunReplayNavigationFor(run, query, s.workflowRunDiffs(run, workflowRunQuery{})),
		Counts:        s.workflowRunReplayCounts(run, query, stages, events, artifacts, timeline, diffs, collaboration, blackboard),
		Quality:       workflowRunQualitySummaryFor(run),
		Collaboration: collaboration,
		Blackboard:    blackboard,
		TeamState:     s.runtime.TeamState(run.ID, ""),
	}
}

func (s *Server) workflowRunReplayCounts(run session.WorkflowRunSnapshot, query workflowRunQuery, stages []session.WorkflowRunStageSnapshot, events []session.WorkflowRunEventSnapshot, artifacts []session.WorkflowRunArtifact, timeline []workflowRunTimelineItem, diffs []runDiffItem, collaboration []session.CollaborationMessageSnapshot, blackboard []session.BlackboardEntrySnapshot) workflowRunReplayCounts {
	totalQuery := workflowRunQuery{}
	totalCollaboration := s.runtime.CollaborationMessages(session.CollaborationFilter{RunID: run.ID})
	totalBlackboard := s.runtime.BlackboardEntries(session.CollaborationFilter{RunID: run.ID})
	return workflowRunReplayCounts{
		Filtered:              workflowRunQueryHasFilters(query),
		StagesTotal:           len(s.workflowRunStagesForQuery(run.CompletedStages, totalQuery)),
		StagesFiltered:        len(stages),
		EventsTotal:           len(workflowRunEventsForQuery(run.Events, totalQuery)),
		EventsFiltered:        len(events),
		ArtifactsTotal:        len(s.workflowRunArtifactsForQuery(run.Artifacts, totalQuery)),
		ArtifactsFiltered:     len(artifacts),
		TimelineTotal:         len(s.workflowRunTimeline(run, totalQuery)),
		TimelineFiltered:      len(timeline),
		DiffsTotal:            len(s.workflowRunDiffs(run, totalQuery)),
		DiffsFiltered:         len(diffs),
		CollaborationTotal:    len(totalCollaboration),
		CollaborationFiltered: len(collaboration),
		BlackboardTotal:       len(totalBlackboard),
		BlackboardFiltered:    len(blackboard),
		ErrorsTotal:           workflowRunErrorCount(run, s.workflowRunDiffs(run, totalQuery)),
		NeedsActionTotal:      workflowRunNeedsActionCount(run),
	}
}

func workflowRunCollectionResponseFor(runs []session.WorkflowRunSnapshot, query workflowRunCollectionQuery) workflowRunCollectionResponse {
	counts := workflowRunCollectionRunCounts{Total: len(runs)}
	matched := make([]session.WorkflowRunSnapshot, 0, len(runs))
	for _, run := range runs {
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
		if !workflowRunCollectionRunMatches(run, query) {
			continue
		}
		counts.Matched++
		if query.Limit > 0 && len(matched) >= query.Limit {
			continue
		}
		matched = append(matched, run)
	}
	counts.Returned = len(matched)
	if query.Summary {
		matched = summarizeWorkflowRunsForAPI(matched)
	}
	return workflowRunCollectionResponse{Runs: matched, Filters: query, Counts: counts}
}

func workflowRunCollectionQueryFromRequest(r *http.Request) workflowRunCollectionQuery {
	if r == nil || r.URL == nil {
		return workflowRunCollectionQuery{}
	}
	values := r.URL.Query()
	limit, _ := strconv.Atoi(strings.TrimSpace(values.Get("limit")))
	if limit < 0 {
		limit = 0
	}
	if limit > 500 {
		limit = 500
	}
	return workflowRunCollectionQuery{
		Workflow:        strings.TrimSpace(firstWorkflowRunQueryValue(values.Get("workflow"), values.Get("name"))),
		Status:          normalizeWorkflowRunQueryToken(values.Get("status")),
		Query:           strings.TrimSpace(firstWorkflowRunQueryValue(values.Get("q"), values.Get("query"), values.Get("search"))),
		RetryOf:         strings.TrimSpace(firstWorkflowRunQueryValue(values.Get("retry_of"), values.Get("retryOf"))),
		Limit:           limit,
		ActiveOnly:      workflowRunQueryBool(firstWorkflowRunQueryValue(values.Get("active"), values.Get("running"))),
		TerminalOnly:    workflowRunQueryBool(firstWorkflowRunQueryValue(values.Get("terminal"), values.Get("done"))),
		NeedsActionOnly: workflowRunQueryBool(values.Get("needs_action")) || workflowRunQueryBool(values.Get("pending")),
		ErrorsOnly:      workflowRunQueryBool(values.Get("errors_only")) || workflowRunQueryBool(values.Get("error")),
		Summary:         workflowRunQueryBool(values.Get("summary")),
	}
}

func workflowRunCollectionWantsEnvelope(r *http.Request, query workflowRunCollectionQuery) bool {
	if r == nil || r.URL == nil {
		return false
	}
	values := r.URL.Query()
	if workflowRunQueryBool(firstWorkflowRunQueryValue(values.Get("envelope"), values.Get("summary"), values.Get("counts"))) {
		return true
	}
	query.Summary = false
	return query != (workflowRunCollectionQuery{})
}

func workflowRunCollectionRunMatches(run session.WorkflowRunSnapshot, query workflowRunCollectionQuery) bool {
	if query.Workflow != "" && !strings.EqualFold(strings.TrimSpace(run.Name), query.Workflow) {
		return false
	}
	if query.Status != "" && normalizeWorkflowRunQueryToken(run.Status) != query.Status {
		return false
	}
	if query.RetryOf != "" && !strings.EqualFold(strings.TrimSpace(run.RetryOf), query.RetryOf) {
		return false
	}
	if query.ActiveOnly && !workflowRunCollectionRunActive(run) {
		return false
	}
	if query.TerminalOnly && !workflowRunCollectionRunTerminal(run) {
		return false
	}
	if query.NeedsActionOnly && !workflowRunCollectionRunNeedsAction(run) {
		return false
	}
	if query.ErrorsOnly && !workflowRunCollectionRunHasError(run) {
		return false
	}
	if query.Query != "" && !workflowRunCollectionRunSearchMatch(run, query.Query) {
		return false
	}
	return true
}

func workflowRunCollectionRunActive(run session.WorkflowRunSnapshot) bool {
	switch normalizeWorkflowRunQueryToken(run.Status) {
	case "running", "cancelling", "awaiting_approval", "awaiting_tool_approval", "awaiting_input", "awaiting_sub_workflow", "paused_need_more_budget", "awaiting_budget_approval":
		return true
	default:
		return false
	}
}

func workflowRunCollectionRunTerminal(run session.WorkflowRunSnapshot) bool {
	switch normalizeWorkflowRunQueryToken(run.Status) {
	case "completed", "denied", "failed", "blocked", "cancelled", "canceled":
		return true
	default:
		return false
	}
}

func workflowRunCollectionRunNeedsAction(run session.WorkflowRunSnapshot) bool {
	if workflowRunStatusNeedsAction(run.Status) {
		return true
	}
	for _, event := range run.Events {
		if event.NeedsAction || event.PendingApproval || event.Suspended || event.Incomplete {
			return true
		}
	}
	for _, stage := range run.CompletedStages {
		if workflowRunStageNeedsAction(stage) {
			return true
		}
	}
	return false
}

func workflowRunCollectionRunHasError(run session.WorkflowRunSnapshot) bool {
	switch normalizeWorkflowRunQueryToken(run.Status) {
	case "failed", "blocked", "denied":
		return true
	}
	return workflowRunErrorCount(run, workflowRunDiffs(run, workflowRunQuery{})) > 0
}

func workflowRunCollectionRunSearchMatch(run session.WorkflowRunSnapshot, query string) bool {
	if workflowRunSearchMatch(query, run.ID, run.Name, run.Status, run.Request, run.Summary, run.NextStage, run.PendingToolName, run.PendingSubWorkflowName, run.PendingSubWorkflowRunID) {
		return true
	}
	for _, stage := range run.CompletedStages {
		if workflowRunSearchMatch(query, stage.Stage, stage.AgentID, stage.NodeType, stage.Skill, stage.Tool, stage.Status, stage.Summary, stage.Result.Output) {
			return true
		}
	}
	for _, event := range run.Events {
		if workflowRunSearchMatch(query, event.Stage, event.Type, event.Content, event.ToolName, event.ToolCallID, event.WorkflowStatus, event.NextStage) {
			return true
		}
	}
	for _, artifact := range run.Artifacts {
		if workflowRunSearchMatch(query, artifact.Stage, artifact.Kind, artifact.Title, artifact.Summary, artifact.Content, artifact.ToolName, artifact.ToolCallID) {
			return true
		}
	}
	return false
}

func workflowRunReplayNavigationFor(run session.WorkflowRunSnapshot, query workflowRunQuery, diffs []runDiffItem) workflowRunReplayNavigation {
	eventCounts := make(map[string]int)
	artifactCounts := make(map[string]int)
	diffCounts := make(map[string]int)
	errorCounts := make(map[string]int)
	needsAction := make(map[string]bool)
	for _, event := range run.Events {
		stage := strings.TrimSpace(event.Stage)
		if stage == "" {
			continue
		}
		key := workflowRunStageKey(stage)
		eventCounts[key]++
		if event.IsError {
			errorCounts[key]++
		}
		if event.NeedsAction || event.PendingApproval || event.Suspended {
			needsAction[key] = true
		}
	}
	if len(run.Artifacts) > 0 {
		for _, artifact := range run.Artifacts {
			stage := strings.TrimSpace(artifact.Stage)
			if stage == "" {
				continue
			}
			key := workflowRunStageKey(stage)
			artifactCounts[key]++
			if artifact.IsError {
				errorCounts[key]++
			}
		}
	} else {
		for _, stage := range run.CompletedStages {
			key := workflowRunStageKey(stage.Stage)
			for _, artifact := range stage.Artifacts {
				artifactCounts[key]++
				if artifact.IsError {
					errorCounts[key]++
				}
			}
		}
	}
	for _, diff := range diffs {
		stage := strings.TrimSpace(diff.Stage)
		if stage == "" {
			continue
		}
		key := workflowRunStageKey(stage)
		diffCounts[key]++
		if diff.IsError {
			errorCounts[key]++
		}
		if diff.NeedsAction {
			needsAction[key] = true
		}
	}

	items := make([]workflowRunStageNavigationItem, 0, len(run.CompletedStages)+1)
	stageSeen := make(map[string]struct{})
	anchorSeen := make(map[string]int)
	for _, stage := range run.CompletedStages {
		stageName := strings.TrimSpace(stage.Stage)
		if stageName == "" {
			continue
		}
		key := workflowRunStageKey(stageName)
		stageSeen[key] = struct{}{}
		item := workflowRunNavigationItem(run.ID, stageName, len(items), workflowRunStageAnchor(stageName, anchorSeen))
		item.Status = stage.Status
		item.NodeType = stage.NodeType
		item.AgentID = stage.AgentID
		item.Skill = stage.Skill
		item.Tool = stage.Tool
		item.Completed = true
		item.EventCount = eventCounts[key]
		item.ArtifactCount = artifactCounts[key]
		item.DiffCount = diffCounts[key]
		item.ErrorCount = errorCounts[key]
		if workflowRunStageIsError(stage) {
			item.ErrorCount++
		}
		item.HasError = item.ErrorCount > 0
		item.NeedsAction = needsAction[key]
		items = append(items, item)
	}
	pendingStage := strings.TrimSpace(run.NextStage)
	if pendingStage != "" {
		key := workflowRunStageKey(pendingStage)
		if _, exists := stageSeen[key]; !exists {
			item := workflowRunNavigationItem(run.ID, pendingStage, len(items), workflowRunStageAnchor(pendingStage, anchorSeen))
			item.Status = run.Status
			item.Pending = true
			item.EventCount = eventCounts[key]
			item.ArtifactCount = artifactCounts[key]
			item.DiffCount = diffCounts[key]
			item.ErrorCount = errorCounts[key]
			item.HasError = item.ErrorCount > 0
			item.NeedsAction = workflowRunStatusNeedsAction(run.Status) || needsAction[key]
			items = append(items, item)
		}
	}

	currentStage := strings.TrimSpace(query.Stage)
	if currentStage == "" {
		currentStage = pendingStage
	}
	currentIndex := -1
	for i := range items {
		if i > 0 {
			items[i].PreviousStage = items[i-1].Stage
		}
		if i+1 < len(items) {
			items[i].NextStage = items[i+1].Stage
		}
		if currentStage != "" && workflowRunStageEqual(items[i].Stage, currentStage) {
			items[i].Current = true
			currentIndex = i
		}
	}
	nav := workflowRunReplayNavigation{
		RunID:        run.ID,
		Workflow:     run.Name,
		CurrentStage: currentStage,
		CurrentIndex: currentIndex,
		PendingStage: pendingStage,
		StageCount:   len(items),
		Stages:       items,
	}
	if currentIndex >= 0 {
		current := items[currentIndex]
		nav.CurrentStage = current.Stage
		nav.CurrentAnchor = current.Anchor
		nav.PreviousStage = current.PreviousStage
		nav.NextStage = current.NextStage
	}
	return nav
}

func workflowRunNavigationItem(runID, stage string, index int, anchor string) workflowRunStageNavigationItem {
	escapedRun := url.PathEscape(strings.TrimSpace(runID))
	escapedStage := url.PathEscape(strings.TrimSpace(stage))
	base := "/api/workflow-runs/" + escapedRun
	return workflowRunStageNavigationItem{
		Stage:         stage,
		Anchor:        anchor,
		Index:         index,
		DetailPath:    base + "/stages/" + escapedStage,
		EventsPath:    base + "/stages/" + escapedStage + "/events",
		ArtifactsPath: base + "/stages/" + escapedStage + "/artifacts",
		DiffsPath:     base + "/diffs?stage=" + url.QueryEscape(strings.TrimSpace(stage)),
		ReplayPath:    base + "/replay?stage=" + url.QueryEscape(strings.TrimSpace(stage)),
	}
}

func workflowRunStageAnchor(stage string, seen map[string]int) string {
	base := "stage-" + workflowRunSlug(stage)
	if base == "stage-" {
		base = "stage-unnamed"
	}
	seen[base]++
	if seen[base] == 1 {
		return base
	}
	return base + "-" + strconv.Itoa(seen[base])
}

func workflowRunSlug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	lastDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			builder.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && builder.Len() > 0 {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(builder.String(), "-")
}

func workflowRunStageKey(stage string) string {
	return strings.ToLower(strings.TrimSpace(stage))
}

func workflowRunStatusNeedsAction(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "awaiting_approval", "awaiting_tool_approval", "awaiting_input", "awaiting_sub_workflow", "paused_need_more_budget", "awaiting_budget_approval", "blocked":
		return true
	default:
		return false
	}
}

func workflowRunErrorCount(run session.WorkflowRunSnapshot, diffs []runDiffItem) int {
	count := 0
	for _, stage := range run.CompletedStages {
		if workflowRunStageIsError(stage) {
			count++
		}
	}
	for _, event := range run.Events {
		if event.IsError {
			count++
		}
	}
	for _, artifact := range run.Artifacts {
		if artifact.IsError {
			count++
		}
	}
	for _, diff := range diffs {
		if diff.IsError {
			count++
		}
	}
	return count
}

func workflowRunNeedsActionCount(run session.WorkflowRunSnapshot) int {
	count := 0
	if workflowRunStatusNeedsAction(run.Status) {
		count++
	}
	for _, event := range run.Events {
		if event.NeedsAction || event.PendingApproval || event.Suspended || event.Incomplete {
			count++
		}
	}
	for _, stage := range run.CompletedStages {
		if workflowRunStageNeedsAction(stage) {
			count++
		}
	}
	return count
}

func workflowRunQualitySummaryFor(run session.WorkflowRunSnapshot) workflowRunQualitySummary {
	quality := workflowRunQualitySummary{
		ErrorCount:        workflowRunErrorCount(run, workflowRunDiffs(run, workflowRunQuery{})),
		NeedsActionCount:  workflowRunNeedsActionCount(run),
		EvidenceArtifacts: workflowRunEvidenceArtifactCount(run),
	}
	for _, stage := range run.CompletedStages {
		for _, acceptance := range stage.Acceptance {
			quality.AcceptanceTotal++
			status := normalizeWorkflowRunQualityStatus(acceptance.Status)
			switch {
			case workflowRunQualityStatusPassed(status):
				quality.AcceptancePassed++
			case workflowRunQualityStatusFailed(status):
				quality.AcceptanceFailed++
				quality.UnmetCriteria = append(quality.UnmetCriteria, workflowRunQualityIssue{
					Stage:    stage.Stage,
					Kind:     "acceptance",
					Name:     firstWorkflowRunQueryValue(acceptance.Name, acceptance.Ref, "acceptance"),
					Status:   status,
					Expected: acceptance.Expected,
					Actual:   acceptance.Actual,
					Detail:   firstWorkflowRunQueryValue(acceptance.Reason, acceptance.Description),
				})
			default:
				quality.Warnings = append(quality.Warnings, workflowRunQualityWarning(stage.Stage, "acceptance", acceptance.Name, status))
			}
		}
		for _, verification := range stage.Result.Verification {
			quality.VerificationTotal++
			status := normalizeWorkflowRunQualityStatus(verification.Status)
			switch {
			case workflowRunQualityStatusPassed(status):
				quality.VerificationPassed++
			case workflowRunQualityStatusFailed(status):
				quality.VerificationFailed++
				quality.FailedValidations = append(quality.FailedValidations, workflowRunQualityIssue{
					Stage:  stage.Stage,
					Kind:   firstWorkflowRunQueryValue(verification.Kind, "verification"),
					Name:   firstWorkflowRunQueryValue(verification.Kind, "verification"),
					Status: status,
					Detail: verification.Detail,
				})
			default:
				quality.VerificationUnknown++
				quality.Warnings = append(quality.Warnings, workflowRunQualityWarning(stage.Stage, "verification", verification.Kind, status))
			}
		}
	}
	quality.Status = workflowRunQualityStatusFor(run, quality)
	quality.Score = workflowRunQualityScoreFor(run, quality)
	return quality
}

func workflowRunEvidenceArtifactCount(run session.WorkflowRunSnapshot) int {
	count := 0
	for _, artifact := range run.Artifacts {
		if workflowRunArtifactHasQualityEvidence(artifact) {
			count++
		}
	}
	return count
}

func workflowRunArtifactHasQualityEvidence(artifact session.WorkflowRunArtifact) bool {
	if artifact.IsError {
		return false
	}
	switch normalizeWorkflowRunQueryToken(artifact.Kind) {
	case "acceptance", "verification", "output", "tool_result", "diff", "evidence", "report":
		return strings.TrimSpace(firstWorkflowRunQueryValue(artifact.Content, artifact.Summary, artifact.Title)) != ""
	default:
		return false
	}
}

func normalizeWorkflowRunQualityStatus(status string) string {
	return strings.ToLower(strings.TrimSpace(status))
}

func workflowRunQualityStatusPassed(status string) bool {
	switch normalizeWorkflowRunQualityStatus(status) {
	case "passed", "pass", "success", "ok", "completed", "complete", "verified", "valid":
		return true
	default:
		return false
	}
}

func workflowRunQualityStatusFailed(status string) bool {
	switch normalizeWorkflowRunQualityStatus(status) {
	case "failed", "fail", "failure", "error", "blocked", "denied", "invalid", "rejected":
		return true
	default:
		return false
	}
}

func workflowRunQualityWarning(stage, kind, name, status string) string {
	label := strings.TrimSpace(firstWorkflowRunQueryValue(name, kind, "check"))
	if strings.TrimSpace(stage) != "" {
		label = stage + "/" + label
	}
	if strings.TrimSpace(status) == "" {
		return label + " has no status"
	}
	return label + " has unknown status: " + status
}

func workflowRunQualityStatusFor(run session.WorkflowRunSnapshot, quality workflowRunQualitySummary) string {
	runStatus := normalizeWorkflowRunQualityStatus(run.Status)
	switch runStatus {
	case "cancelled", "canceled":
		return "cancelled"
	}
	if quality.ErrorCount > 0 || quality.AcceptanceFailed > 0 || quality.VerificationFailed > 0 || workflowRunQualityStatusFailed(runStatus) {
		return "failed"
	}
	if quality.NeedsActionCount > 0 {
		return "needs_action"
	}
	if workflowRunStatusActive(run.Status) {
		return "running"
	}
	if quality.AcceptanceTotal == 0 && quality.VerificationTotal == 0 {
		if runStatus == "completed" {
			return "unverified"
		}
		return firstWorkflowRunQueryValue(runStatus, "unknown")
	}
	if quality.VerificationUnknown > 0 || len(quality.Warnings) > 0 {
		return "warning"
	}
	return "passed"
}

func workflowRunQualityScoreFor(run session.WorkflowRunSnapshot, quality workflowRunQualitySummary) int {
	totalChecks := quality.AcceptanceTotal + quality.VerificationTotal
	passedChecks := quality.AcceptancePassed + quality.VerificationPassed
	score := 0
	if totalChecks > 0 {
		score = (passedChecks*100 + totalChecks/2) / totalChecks
	} else {
		switch normalizeWorkflowRunQualityStatus(run.Status) {
		case "completed":
			score = 70
		case "running":
			score = 50
		default:
			score = 0
		}
	}
	score -= workflowRunQualityPenalty(quality.ErrorCount, 15, 60)
	score -= workflowRunQualityPenalty(quality.NeedsActionCount, 10, 40)
	if workflowRunQualityStatusFailed(run.Status) && score > 40 {
		score = 40
	}
	if normalizeWorkflowRunQualityStatus(run.Status) == "cancelled" && score > 50 {
		score = 50
	}
	if score < 0 {
		return 0
	}
	if score > 100 {
		return 100
	}
	return score
}

func workflowRunQualityPenalty(count, each, max int) int {
	if count <= 0 {
		return 0
	}
	penalty := count * each
	if penalty > max {
		return max
	}
	return penalty
}

func (s *Server) handleWorkflowRunExport(w http.ResponseWriter, r *http.Request, run session.WorkflowRunSnapshot) {
	query := workflowRunQueryFromRequest(r)
	replay := s.workflowRunReplay(run, query)
	format := strings.ToLower(strings.TrimSpace(firstWorkflowRunQueryValue(r.URL.Query().Get("format"), r.URL.Query().Get("type"))))
	if format == "" {
		format = "json"
	}
	switch format {
	case "json":
		data, err := json.MarshalIndent(replay, "", "  ")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", workflowRunExportDisposition(run, "json"))
		_, _ = w.Write(append(data, '\n'))
	case "md", "markdown":
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Header().Set("Content-Disposition", workflowRunExportDisposition(run, "md"))
		_, _ = w.Write([]byte(renderWorkflowRunMarkdownExport(replay, query)))
	default:
		http.Error(w, "unsupported workflow run export format: "+format, http.StatusBadRequest)
	}
}

func workflowRunExportDisposition(run session.WorkflowRunSnapshot, ext string) string {
	name := normalizeWorkflowRunExportName(firstWorkflowRunQueryValue(run.Name, "workflow-run"))
	id := normalizeWorkflowRunExportName(run.ID)
	if id != "" {
		name += "-" + id
	}
	return fmt.Sprintf(`attachment; filename="%s.%s"`, name, ext)
}

func normalizeWorkflowRunExportName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
		case r == '-' || r == '_':
			builder.WriteRune(r)
		default:
			if builder.Len() > 0 {
				builder.WriteByte('-')
			}
		}
	}
	out := strings.Trim(builder.String(), "-_")
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	return out
}

func renderWorkflowRunMarkdownExport(replay workflowRunReplay, query workflowRunQuery) string {
	run := replay.Run
	var builder strings.Builder
	builder.WriteString("# Workflow Run Report\n\n")
	workflowRunMarkdownKV(&builder, "Run ID", run.ID)
	workflowRunMarkdownKV(&builder, "Workflow", run.Name)
	workflowRunMarkdownKV(&builder, "Status", run.Status)
	workflowRunMarkdownKV(&builder, "Attempt", strconv.Itoa(run.Attempt))
	workflowRunMarkdownKV(&builder, "Started", run.StartedAt)
	workflowRunMarkdownKV(&builder, "Updated", run.UpdatedAt)
	workflowRunMarkdownKV(&builder, "Completed", run.CompletedAt)
	if strings.TrimSpace(run.RetryOf) != "" {
		workflowRunMarkdownKV(&builder, "Retry Of", run.RetryOf)
	}
	if strings.TrimSpace(run.Request) != "" {
		builder.WriteString("\n## Request\n\n")
		builder.WriteString(workflowRunMarkdownCode(run.Request, "text"))
	}
	if strings.TrimSpace(run.Summary) != "" {
		builder.WriteString("\n## Summary\n\n")
		builder.WriteString(strings.TrimSpace(run.Summary))
		builder.WriteString("\n")
	}
	workflowRunMarkdownQuality(&builder, replay.Quality)
	if len(replay.Stages) > 0 {
		builder.WriteString("\n## Stages\n\n")
		for _, stage := range replay.Stages {
			builder.WriteString("### ")
			builder.WriteString(workflowRunMarkdownInline(firstWorkflowRunQueryValue(stage.Stage, "stage")))
			builder.WriteString("\n\n")
			workflowRunMarkdownKV(&builder, "Status", stage.Status)
			workflowRunMarkdownKV(&builder, "Agent", stage.AgentID)
			workflowRunMarkdownKV(&builder, "Node Type", stage.NodeType)
			workflowRunMarkdownKV(&builder, "Skill", stage.Skill)
			workflowRunMarkdownKV(&builder, "Tool", stage.Tool)
			workflowRunMarkdownKV(&builder, "Attempts", strconv.Itoa(stage.Attempts))
			if strings.TrimSpace(stage.Summary) != "" {
				builder.WriteString("\n")
				builder.WriteString(strings.TrimSpace(stage.Summary))
				builder.WriteString("\n")
			}
			if query.IncludeContent && strings.TrimSpace(stage.Result.Output) != "" {
				builder.WriteString("\n")
				builder.WriteString(workflowRunMarkdownCode(stage.Result.Output, "text"))
			}
		}
	}
	if len(replay.Artifacts) > 0 {
		builder.WriteString("\n## Artifacts\n\n")
		for _, artifact := range replay.Artifacts {
			builder.WriteString("- ")
			builder.WriteString(workflowRunMarkdownInline(firstWorkflowRunQueryValue(artifact.Title, artifact.Kind, artifact.ID)))
			if strings.TrimSpace(artifact.Stage) != "" {
				builder.WriteString(" (stage: `")
				builder.WriteString(workflowRunMarkdownInline(artifact.Stage))
				builder.WriteString("`)")
			}
			if strings.TrimSpace(artifact.Summary) != "" {
				builder.WriteString(": ")
				builder.WriteString(strings.TrimSpace(artifact.Summary))
			}
			builder.WriteString("\n")
			if query.IncludeContent && strings.TrimSpace(artifact.Content) != "" {
				builder.WriteString("\n")
				builder.WriteString(workflowRunMarkdownCode(artifact.Content, "text"))
			}
		}
	}
	if len(replay.Timeline) > 0 {
		builder.WriteString("\n## Timeline\n\n")
		for _, item := range replay.Timeline {
			builder.WriteString("- ")
			if strings.TrimSpace(item.At) != "" {
				builder.WriteString(workflowRunMarkdownInline(item.At))
				builder.WriteString(" ")
			}
			builder.WriteString("`")
			builder.WriteString(workflowRunMarkdownInline(item.Kind))
			builder.WriteString("`")
			if strings.TrimSpace(item.Type) != "" {
				builder.WriteString("/")
				builder.WriteString("`")
				builder.WriteString(workflowRunMarkdownInline(item.Type))
				builder.WriteString("`")
			}
			if strings.TrimSpace(item.Stage) != "" {
				builder.WriteString(" stage `")
				builder.WriteString(workflowRunMarkdownInline(item.Stage))
				builder.WriteString("`")
			}
			if details := workflowRunTimelineMarkdownDetails(item); len(details) > 0 {
				builder.WriteString(" (")
				builder.WriteString(strings.Join(details, ", "))
				builder.WriteString(")")
			}
			if strings.TrimSpace(item.Summary) != "" {
				builder.WriteString(": ")
				builder.WriteString(strings.TrimSpace(item.Summary))
			}
			builder.WriteString("\n")
		}
	}
	if len(replay.Actions) > 0 {
		builder.WriteString("\n## Available Actions\n\n")
		for _, action := range replay.Actions {
			builder.WriteString("- `")
			builder.WriteString(workflowRunMarkdownInline(action.Name))
			builder.WriteString("`")
			if action.Available {
				builder.WriteString(" available")
			} else {
				builder.WriteString(" unavailable")
			}
			if strings.TrimSpace(action.Reason) != "" {
				builder.WriteString(": ")
				builder.WriteString(action.Reason)
			}
			builder.WriteString("\n")
		}
	}
	return builder.String()
}

func workflowRunMarkdownQuality(builder *strings.Builder, quality workflowRunQualitySummary) {
	if strings.TrimSpace(quality.Status) == "" {
		return
	}
	builder.WriteString("\n## Quality\n\n")
	workflowRunMarkdownKV(builder, "Status", quality.Status)
	workflowRunMarkdownKV(builder, "Score", strconv.Itoa(quality.Score))
	workflowRunMarkdownKV(builder, "Acceptance", workflowRunMarkdownRatio(quality.AcceptancePassed, quality.AcceptanceTotal))
	workflowRunMarkdownKV(builder, "Verification", workflowRunMarkdownRatio(quality.VerificationPassed, quality.VerificationTotal))
	workflowRunMarkdownKV(builder, "Evidence Artifacts", strconv.Itoa(quality.EvidenceArtifacts))
	workflowRunMarkdownKV(builder, "Errors", strconv.Itoa(quality.ErrorCount))
	workflowRunMarkdownKV(builder, "Needs Action", strconv.Itoa(quality.NeedsActionCount))
	if len(quality.UnmetCriteria) > 0 {
		builder.WriteString("\n### Unmet Criteria\n\n")
		for _, issue := range quality.UnmetCriteria {
			workflowRunMarkdownQualityIssue(builder, issue)
		}
	}
	if len(quality.FailedValidations) > 0 {
		builder.WriteString("\n### Failed Validations\n\n")
		for _, issue := range quality.FailedValidations {
			workflowRunMarkdownQualityIssue(builder, issue)
		}
	}
	if len(quality.Warnings) > 0 {
		builder.WriteString("\n### Warnings\n\n")
		for _, warning := range quality.Warnings {
			builder.WriteString("- ")
			builder.WriteString(workflowRunMarkdownInline(warning))
			builder.WriteString("\n")
		}
	}
}

func workflowRunMarkdownRatio(passed, total int) string {
	if total <= 0 {
		return ""
	}
	return strconv.Itoa(passed) + "/" + strconv.Itoa(total)
}

func workflowRunMarkdownQualityIssue(builder *strings.Builder, issue workflowRunQualityIssue) {
	builder.WriteString("- ")
	label := firstWorkflowRunQueryValue(issue.Name, issue.Kind, "check")
	if strings.TrimSpace(issue.Stage) != "" {
		builder.WriteString("`")
		builder.WriteString(workflowRunMarkdownInline(issue.Stage))
		builder.WriteString("` ")
	}
	builder.WriteString(workflowRunMarkdownInline(label))
	if strings.TrimSpace(issue.Status) != "" {
		builder.WriteString(" (`")
		builder.WriteString(workflowRunMarkdownInline(issue.Status))
		builder.WriteString("`)")
	}
	if strings.TrimSpace(issue.Detail) != "" {
		builder.WriteString(": ")
		builder.WriteString(workflowRunMarkdownInline(issue.Detail))
	}
	if strings.TrimSpace(issue.Expected) != "" || strings.TrimSpace(issue.Actual) != "" {
		builder.WriteString(" expected `")
		builder.WriteString(workflowRunMarkdownInline(issue.Expected))
		builder.WriteString("`, actual `")
		builder.WriteString(workflowRunMarkdownInline(issue.Actual))
		builder.WriteString("`")
	}
	builder.WriteString("\n")
}

func workflowRunMarkdownKV(builder *strings.Builder, key, value string) {
	value = strings.TrimSpace(value)
	if value == "" || value == "0" {
		return
	}
	builder.WriteString("- **")
	builder.WriteString(key)
	builder.WriteString(":** ")
	builder.WriteString(workflowRunMarkdownInline(value))
	builder.WriteString("\n")
}

func workflowRunMarkdownInline(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "`", "'")
	return value
}

func workflowRunTimelineMarkdownDetails(item workflowRunTimelineItem) []string {
	details := make([]string, 0, 6)
	add := func(label, value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		details = append(details, label+": `"+workflowRunMarkdownInline(value)+"`")
	}
	if item.Incomplete {
		details = append(details, "incomplete")
	}
	add("reason", item.Reason)
	add("stop", item.StopReason)
	add("budget", item.BudgetScope)
	if item.BudgetLLMCalls > 0 {
		details = append(details, "llm_calls: `"+strconv.Itoa(item.BudgetLLMCalls)+"`")
	}
	if item.BudgetTotalTokens > 0 {
		details = append(details, "budget_tokens: `"+strconv.Itoa(item.BudgetTotalTokens)+"`")
	}
	add("contract", item.ContractCheck)
	if item.ContinuationCount > 0 {
		details = append(details, "continuations: `"+strconv.Itoa(item.ContinuationCount)+"`")
	}
	return details
}

func workflowRunMarkdownCode(value, language string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = strings.ReplaceAll(value, "```", "'''")
	if strings.TrimSpace(language) == "" {
		language = "text"
	}
	return "```" + language + "\n" + value + "\n```\n"
}

func (s *Server) workflowRunActions(run session.WorkflowRunSnapshot) []workflowRunAction {
	id := strings.TrimSpace(run.ID)
	if id == "" {
		return nil
	}
	eventsPath := workflowRunEventsStreamPath(id)
	pendingRisk := s.workflowRunPendingToolRisk(run, nil)
	status := strings.ToLower(strings.TrimSpace(run.Status))
	actions := make([]workflowRunAction, 0, 5)
	switch status {
	case "awaiting_input":
		actions = append(actions, workflowRunAction{
			Name:       "submit_input",
			Label:      "Submit input",
			Method:     http.MethodPost,
			Path:       "/api/workflow-runs/" + id + "/input",
			StreamPath: "/api/workflow-runs/" + id + "/input/stream",
			EventsPath: eventsPath,
			Available:  true,
			Durable:    true,
			Background: true,
			Reason:     "manual input can resume from the persisted workflow run snapshot",
		})
	case "awaiting_tool_approval":
		callID, inMemory := s.workflowRunPendingApprovalCallID(run)
		resumable := inMemory || s.workflowRunToolApprovalResumable(run)
		resumeReason := workflowRunToolApprovalActionReason(resumable)
		approveAvailable := resumable
		approveReason := resumeReason
		if approveAvailable {
			approveAvailable, approveReason = s.workflowRunToolApprovalAllowed(run, callID, approveReason)
		}
		rememberAvailable, rememberReason := s.workflowRunRememberToolApprovalAvailable(run, callID, approveAvailable, approveReason)
		action := workflowRunAction{
			Name:       "approve_tool",
			Label:      "Approve tool",
			Method:     http.MethodPost,
			Path:       "/api/workflow-runs/" + id + "/approve-tool",
			StreamPath: "/api/workflow-runs/" + id + "/approve-tool/stream",
			EventsPath: eventsPath,
			Available:  approveAvailable,
			Durable:    approveAvailable,
			Background: approveAvailable,
			Reason:     approveReason,
			Risk:       pendingRisk,
		}
		actions = append(actions, action)
		deny := action
		deny.Name = "deny_tool"
		deny.Label = "Deny tool"
		deny.Destructive = true
		deny.Path = "/api/workflow-runs/" + id + "/deny-tool"
		deny.StreamPath = "/api/workflow-runs/" + id + "/deny-tool/stream"
		deny.Available = resumable
		deny.Durable = resumable
		deny.Background = resumable
		deny.Reason = resumeReason
		actions = append(actions, deny)
		approveAll := workflowRunAction{
			Name:       "approve_all_tools",
			Label:      "Approve matching workflow tools",
			Method:     http.MethodPost,
			Path:       "/api/workflow-runs/" + id + "/approve-tools",
			StreamPath: "/api/workflow-runs/" + id + "/approve-tools/stream",
			EventsPath: eventsPath,
			Available:  rememberAvailable,
			Durable:    rememberAvailable,
			Background: rememberAvailable,
			Reason:     "approves this tool call and auto-approves later matching tool calls within the same workflow stage",
			Risk:       pendingRisk,
		}
		if !rememberAvailable {
			approveAll.Reason = rememberReason
		}
		actions = append(actions, approveAll)
	case "awaiting_approval":
		resumable := s.workflowRunApprovalResumable(run)
		actions = append(actions, workflowRunAction{
			Name:       "approve_stage",
			Label:      "Approve stage",
			Method:     http.MethodPost,
			Path:       "/api/workflow-runs/" + id + "/approve",
			StreamPath: "/api/workflow-runs/" + id + "/approve/stream",
			EventsPath: eventsPath,
			Available:  resumable,
			Durable:    resumable,
			Background: resumable,
			Reason:     workflowRunApprovalActionReason(resumable),
		})
	case "awaiting_sub_workflow":
		resumable := s.workflowRunSubWorkflowResumable(run)
		actions = append(actions, workflowRunAction{
			Name:       "resume_sub_workflow",
			Label:      "Resume parent workflow",
			Method:     http.MethodPost,
			Path:       "/api/workflow-runs/" + id + "/resume-sub-workflow",
			StreamPath: "/api/workflow-runs/" + id + "/resume-sub-workflow/stream",
			EventsPath: eventsPath,
			Available:  resumable,
			Durable:    resumable,
			Background: resumable,
			Reason:     workflowRunSubWorkflowActionReason(run, resumable),
		})
	case "paused_need_more_budget":
		resumable, resumeReason := s.workflowRunContinueOutputResumable(run)
		actions = append(actions, workflowRunAction{
			Name:       "continue_output",
			Label:      "Continue output",
			Method:     http.MethodPost,
			Path:       "/api/workflow-runs/" + id + "/continue-output",
			StreamPath: "/api/workflow-runs/" + id + "/continue-output/stream",
			EventsPath: eventsPath,
			Available:  resumable,
			Durable:    resumable,
			Background: resumable,
			Reason:     resumeReason,
		})
	case "awaiting_budget_approval":
		resumable, resumeReason := s.workflowRunBudgetApprovalResumable(run)
		actions = append(actions, workflowRunAction{
			Name:       "approve_budget",
			Label:      "Approve budget",
			Method:     http.MethodPost,
			Path:       "/api/workflow-runs/" + id + "/approve-budget",
			StreamPath: "/api/workflow-runs/" + id + "/approve-budget/stream",
			EventsPath: eventsPath,
			Available:  resumable,
			Durable:    resumable,
			Background: resumable,
			Reason:     resumeReason,
		})
	case "blocked":
		resumable, resumeReason := s.workflowRunModelEscalationResumable(run)
		actions = append(actions, workflowRunAction{
			Name:       "escalate_model",
			Label:      "Escalate model",
			Method:     http.MethodPost,
			Path:       "/api/workflow-runs/" + id + "/escalate-model",
			StreamPath: "/api/workflow-runs/" + id + "/escalate-model/stream",
			EventsPath: eventsPath,
			Available:  resumable,
			Durable:    resumable,
			Background: resumable,
			Reason:     resumeReason,
		})
	}
	if workflowRunStatusCancellable(status) {
		actions = append(actions, workflowRunAction{
			Name:        "cancel",
			Label:       "Cancel",
			Method:      http.MethodPost,
			Path:        "/api/workflow-runs/" + id + "/cancel",
			EventsPath:  eventsPath,
			Available:   true,
			Durable:     true,
			Destructive: true,
		})
	}
	if workflowRunStatusRetryable(status) && strings.TrimSpace(run.Name) != "" && strings.TrimSpace(run.Request) != "" {
		actions = append(actions, workflowRunAction{
			Name:       "retry",
			Label:      "Retry",
			Method:     http.MethodPost,
			Path:       "/api/workflow-runs/" + id + "/retry",
			StreamPath: "/api/workflow-runs/" + id + "/retry/stream",
			EventsPath: eventsPath,
			Available:  true,
			Durable:    true,
			Background: true,
			Reason:     workflowRunRetryActionReason(status),
		})
	}
	return normalizeWorkflowRunActions(actions)
}

func normalizeWorkflowRunActions(actions []workflowRunAction) []workflowRunAction {
	if len(actions) == 0 {
		return actions
	}
	out := make([]workflowRunAction, len(actions))
	for i, action := range actions {
		out[i] = action
		out[i].Kind = workflowRunActionKind(action.Name)
		out[i].SupportsBackground = action.Background || strings.TrimSpace(action.EventsPath) != ""
		out[i].SupportsStream = strings.TrimSpace(action.StreamPath) != ""
		out[i].AcceptsBody = workflowRunActionAcceptsBody(action.Name)
		out[i].RequiresBody = workflowRunActionRequiresBody(action.Name)
		if out[i].AcceptsBody || out[i].RequiresBody {
			out[i].BodySchema = workflowRunActionBodySchema(action.Name)
		}
		if out[i].Risk != nil {
			risk := copySchemaToolRiskProfile(*out[i].Risk)
			out[i].Risk = &risk
		}
	}
	return out
}

func workflowRunActionKind(name string) string {
	switch strings.TrimSpace(name) {
	case "cancel":
		return "lifecycle"
	case "retry":
		return "retry"
	case "escalate_model":
		return "model_escalation"
	case "continue_output", "approve_budget":
		return "budget_recovery"
	case "submit_input":
		return "manual_input"
	case "approve_stage":
		return "stage_approval"
	case "approve_tool", "approve_all_tools", "deny_tool":
		return "tool_approval"
	case "resume_sub_workflow":
		return "sub_workflow"
	default:
		return "action"
	}
}

func workflowRunActionRequiresBody(name string) bool {
	return strings.TrimSpace(name) == "submit_input"
}

func workflowRunActionAcceptsBody(name string) bool {
	switch strings.TrimSpace(name) {
	case "retry", "escalate_model", "continue_output", "approve_budget", "submit_input", "approve_stage", "approve_tool", "approve_all_tools", "deny_tool", "resume_sub_workflow":
		return true
	default:
		return false
	}
}

func workflowRunActionBodySchema(name string) map[string]any {
	properties := map[string]any{
		"background": map[string]any{
			"type":        "boolean",
			"description": "Run the action in the background and reconnect through events_path.",
			"default":     true,
		},
	}
	required := []string(nil)
	if strings.TrimSpace(name) == "submit_input" {
		properties["inputs"] = map[string]any{
			"type":                 "object",
			"description":          "Manual input values keyed by the pending input field names.",
			"additionalProperties": true,
		}
		required = []string{"inputs"}
	}
	schema := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func workflowRunActionDiscoveryFor(run session.WorkflowRunSnapshot, actions []workflowRunAction) workflowRunActionDiscovery {
	id := strings.TrimSpace(run.ID)
	return workflowRunActionDiscovery{
		Meta:          runActionDiscoveryMetaForActions(),
		RunID:         id,
		Name:          strings.TrimSpace(run.Name),
		Status:        strings.TrimSpace(run.Status),
		NeedsAction:   workflowRunCollectionRunNeedsAction(run),
		NextStage:     strings.TrimSpace(run.NextStage),
		EventsPath:    workflowRunEventsStreamPath(id),
		TimelinePath:  "/api/workflow-runs/" + id + "/timeline",
		ReplayPath:    "/api/workflow-runs/" + id + "/replay",
		ActionsPath:   "/api/workflow-runs/" + id + "/actions",
		Recommended:   recommendedWorkflowRunAction(actions),
		Counts:        workflowRunActionCounts(actions),
		Actions:       actions,
		SupportsRetry: workflowRunActionExists(actions, "retry"),
	}
}

func workflowRunActionExists(actions []workflowRunAction, name string) bool {
	for _, action := range actions {
		if action.Name == name {
			return true
		}
	}
	return false
}

func workflowRunActionCounts(actions []workflowRunAction) runActionCounts {
	counts := runActionCounts{Total: len(actions)}
	for _, action := range actions {
		if action.Available {
			counts.Available++
		} else {
			counts.Unavailable++
		}
		if action.Destructive {
			counts.Destructive++
		}
		if action.RequiresBody {
			counts.NeedsBody++
		}
		if action.Risk != nil {
			counts.WithRisk++
		}
	}
	return counts
}

func recommendedWorkflowRunAction(actions []workflowRunAction) string {
	for _, name := range []string{"submit_input", "approve_budget", "continue_output", "approve_stage", "approve_tool", "approve_all_tools", "resume_sub_workflow", "escalate_model", "deny_tool", "retry", "cancel"} {
		for _, action := range actions {
			if action.Name == name && action.Available {
				return name
			}
		}
	}
	return ""
}

func workflowRunEventsStreamPath(runID string) string {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return ""
	}
	return "/api/workflow-runs/" + runID + "/events/stream"
}

func workflowRunApprovalActionReason(resumable bool) string {
	if resumable {
		return "graph workflow approval can resume from the persisted workflow run snapshot"
	}
	return "this approval type cannot be resumed durably yet; retry or cancel the workflow run"
}

func workflowRunToolApprovalActionReason(resumable bool) string {
	if resumable {
		return "workflow tool approval can resume from the persisted workflow run snapshot"
	}
	return "workflow tool approval context is not available in this process; retry or cancel the workflow run"
}

func workflowRunToolApprovalActionName(approve, remember bool) string {
	if !approve {
		return "deny-tool"
	}
	if remember {
		return "approve-tools"
	}
	return "approve-tool"
}

func workflowRunSubWorkflowActionReason(run session.WorkflowRunSnapshot, resumable bool) string {
	if resumable {
		return "parent workflow can resume because the nested sub-workflow run completed"
	}
	if strings.TrimSpace(run.PendingSubWorkflowRunID) == "" {
		return "parent workflow is missing nested sub-workflow run metadata"
	}
	if strings.TrimSpace(run.PendingSubWorkflowStatus) != "" {
		return "nested sub-workflow is still " + strings.TrimSpace(run.PendingSubWorkflowStatus)
	}
	return "nested sub-workflow is not completed yet"
}

func workflowRunContinueOutputActionReason(resumable bool) string {
	if resumable {
		return "continue incomplete stage from persisted workflow run snapshot"
	}
	return "continue output requires a paused graph workflow run with an incomplete next stage; retry or cancel this run"
}

func workflowRunBudgetApprovalActionReason(resumable bool) string {
	if resumable {
		return "approve hard workflow budget and resume from the persisted workflow run snapshot"
	}
	return "budget approval requires a graph workflow run paused at a hard budget boundary; retry or cancel this run"
}

func workflowRunModelEscalationActionReason(resumable bool, detail string) string {
	if resumable {
		return "rerun the blocked stage with its configured escalation model and resume this workflow run"
	}
	if strings.TrimSpace(detail) != "" {
		return detail
	}
	return "model escalation requires a blocked graph workflow stage with escalation provider or model params; retry or route remediation"
}

func decodeWorkflowRunActionRequest(r *http.Request) (workflowRunActionRequest, error) {
	if r == nil || r.Body == nil {
		return workflowRunActionRequest{}, nil
	}
	var req workflowRunActionRequest
	err := json.NewDecoder(r.Body).Decode(&req)
	if err == nil || errors.Is(err, io.EOF) {
		return req, nil
	}
	return workflowRunActionRequest{}, err
}

func (s *Server) workflowRunApprovalResumable(run session.WorkflowRunSnapshot) bool {
	if s == nil || s.runtime == nil || strings.TrimSpace(run.Name) == "" || strings.TrimSpace(run.NextStage) == "" {
		return false
	}
	if workflowRunBuiltInApprovalResumable(run) {
		return true
	}
	doc, err := s.runtime.WorkflowRunner().LoadWorkflowGraphDocument(run.Name)
	if err != nil {
		return false
	}
	for _, stage := range doc.Stages {
		if !workflowRunStageEqual(stage.Name, run.NextStage) {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(stage.NodeType)) {
		case "checkpoint", "manual_approval", "approval_gate":
			return true
		default:
			return stage.Approval
		}
	}
	return false
}

func (s *Server) workflowRunToolApprovalResumable(run session.WorkflowRunSnapshot) bool {
	if s == nil || s.runtime == nil {
		return false
	}
	return s.runtime.WorkflowRunner().CanResumeToolApproval(run)
}

func (s *Server) workflowRunRememberToolApprovalAvailable(run session.WorkflowRunSnapshot, callID string, resumable bool, fallbackReason string) (bool, string) {
	if !resumable {
		return false, fallbackReason
	}
	if s == nil || s.runtime == nil {
		return false, fallbackReason
	}
	var (
		ok     bool
		reason string
	)
	if strings.TrimSpace(callID) != "" {
		ok, reason = s.runtime.CanRememberPendingToolApproval(callID)
	} else {
		ok, reason = s.runtime.WorkflowRunner().CanRememberToolApproval(run)
	}
	if !ok && strings.Contains(reason, "pending approval not found") {
		ok, reason = s.runtime.WorkflowRunner().CanRememberToolApproval(run)
	}
	if !ok {
		if strings.TrimSpace(reason) == "" {
			reason = fallbackReason
		}
		return false, reason
	}
	return true, "approves this tool call and auto-approves later matching tool calls within the same workflow stage"
}

func (s *Server) workflowRunToolApprovalAllowed(run session.WorkflowRunSnapshot, callID string, fallbackReason string) (bool, string) {
	if s == nil || s.runtime == nil {
		return false, fallbackReason
	}
	var (
		ok     bool
		reason string
	)
	if strings.TrimSpace(callID) != "" {
		ok, reason = s.runtime.CanApprovePendingToolApproval(callID)
	} else {
		ok, reason = s.runtime.WorkflowRunner().CanApproveToolApproval(run)
	}
	if !ok && strings.Contains(reason, "pending approval not found") {
		ok, reason = s.runtime.WorkflowRunner().CanApproveToolApproval(run)
	}
	if !ok {
		if strings.TrimSpace(reason) == "" {
			reason = fallbackReason
		}
		return false, reason
	}
	return true, fallbackReason
}

func (s *Server) workflowRunSubWorkflowResumable(run session.WorkflowRunSnapshot) bool {
	if s == nil || s.runtime == nil || !strings.EqualFold(strings.TrimSpace(run.Status), "awaiting_sub_workflow") {
		return false
	}
	childRunID := strings.TrimSpace(run.PendingSubWorkflowRunID)
	if childRunID == "" {
		return false
	}
	childRun, ok := workflowRunByID(s.sessionSnapshotWithApprovalRisk().WorkflowRuns, childRunID)
	if !ok {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(childRun.Status), "completed")
}

func (s *Server) workflowRunContinueOutputResumable(run session.WorkflowRunSnapshot) (bool, string) {
	if s == nil || s.runtime == nil {
		return false, workflowRunContinueOutputActionReason(false)
	}
	status := strings.ToLower(strings.TrimSpace(run.Status))
	if status != "paused_need_more_budget" {
		return false, workflowRunContinueOutputActionReason(false)
	}
	if strings.TrimSpace(run.ID) == "" || strings.TrimSpace(run.Name) == "" || strings.TrimSpace(run.NextStage) == "" {
		return false, workflowRunContinueOutputActionReason(false)
	}
	if !workflowRunHasIncompleteStage(run, run.NextStage) {
		return false, "continue output requires the next stage to have a persisted incomplete result"
	}
	if err := s.runtime.WorkflowRunner().CanContinueOutput(run); err != nil {
		return false, err.Error()
	}
	return true, workflowRunContinueOutputActionReason(true)
}

func (s *Server) workflowRunBudgetApprovalResumable(run session.WorkflowRunSnapshot) (bool, string) {
	if s == nil || s.runtime == nil {
		return false, workflowRunBudgetApprovalActionReason(false)
	}
	if !strings.EqualFold(strings.TrimSpace(run.Status), "awaiting_budget_approval") {
		return false, workflowRunBudgetApprovalActionReason(false)
	}
	if strings.TrimSpace(run.ID) == "" || strings.TrimSpace(run.Name) == "" || strings.TrimSpace(run.NextStage) == "" {
		return false, workflowRunBudgetApprovalActionReason(false)
	}
	if err := s.runtime.WorkflowRunner().CanApproveBudget(run); err != nil {
		return false, err.Error()
	}
	return true, workflowRunBudgetApprovalActionReason(true)
}

func (s *Server) workflowRunModelEscalationResumable(run session.WorkflowRunSnapshot) (bool, string) {
	if s == nil || s.runtime == nil {
		return false, workflowRunModelEscalationActionReason(false, "")
	}
	if err := s.runtime.WorkflowRunner().CanEscalateModel(run); err != nil {
		return false, workflowRunModelEscalationActionReason(false, err.Error())
	}
	return true, workflowRunModelEscalationActionReason(true, "")
}

func workflowRunHasIncompleteStage(run session.WorkflowRunSnapshot, stageName string) bool {
	for _, stage := range run.CompletedStages {
		if !workflowRunStageEqual(stage.Stage, stageName) {
			continue
		}
		if stage.Result.Incomplete || strings.EqualFold(strings.TrimSpace(stage.Status), "incomplete") {
			return true
		}
		if strings.EqualFold(strings.TrimSpace(stage.Metadata["incomplete"]), "true") {
			return true
		}
	}
	return false
}

func workflowRunBuiltInApprovalResumable(run session.WorkflowRunSnapshot) bool {
	switch strings.ToLower(strings.TrimSpace(run.Name)) {
	case "plan-fix-audit":
		if !workflowRunStageEqual(run.NextStage, "fix") {
			return false
		}
		for _, stage := range run.CompletedStages {
			if workflowRunStageEqual(stage.Stage, "plan") && strings.TrimSpace(stage.Result.Output) != "" {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func (s *Server) workflowRunPendingApprovalCallID(run session.WorkflowRunSnapshot) (string, bool) {
	callID := strings.TrimSpace(run.PendingCallID)
	if callID != "" {
		if _, ok := s.runtime.PendingApproval(callID); ok {
			return callID, true
		}
		return callID, false
	}
	for _, pending := range s.runtime.PendingApprovalSummaries() {
		if workflowRunMatchesPendingSummary(run, pending) {
			callID = strings.TrimSpace(pending.CallID)
			if callID == "" {
				return "", false
			}
			if _, ok := s.runtime.PendingApproval(callID); ok {
				return callID, true
			}
			return callID, false
		}
	}
	return "", false
}

func workflowRunMatchesPendingSummary(run session.WorkflowRunSnapshot, pending session.PendingApprovalSnapshot) bool {
	if strings.TrimSpace(pending.CallID) != "" && strings.TrimSpace(run.PendingCallID) != "" {
		return strings.TrimSpace(pending.CallID) == strings.TrimSpace(run.PendingCallID)
	}
	if strings.TrimSpace(pending.WorkflowName) != "" && !strings.EqualFold(strings.TrimSpace(pending.WorkflowName), strings.TrimSpace(run.Name)) {
		return false
	}
	if strings.TrimSpace(pending.Request) != "" && strings.TrimSpace(run.Request) != "" && strings.TrimSpace(pending.Request) != strings.TrimSpace(run.Request) {
		return false
	}
	if strings.TrimSpace(pending.Stage) != "" && strings.TrimSpace(run.NextStage) != "" {
		return workflowRunStageEqual(pending.Stage, run.NextStage)
	}
	return strings.TrimSpace(pending.WorkflowName) != ""
}

func (s *Server) handleWorkflowRunAction(w http.ResponseWriter, r *http.Request, run session.WorkflowRunSnapshot, parts []string) {
	switch {
	case len(parts) == 2 && parts[1] == "cancel":
		s.handleWorkflowRunCancel(w, run)
	case len(parts) == 2 && parts[1] == "approve":
		s.handleWorkflowRunApproval(w, r, run, false)
	case len(parts) == 3 && parts[1] == "approve" && parts[2] == "stream":
		s.handleWorkflowRunApproval(w, r, run, true)
	case len(parts) == 2 && parts[1] == "approve-tool":
		s.handleWorkflowRunToolApproval(w, r, run, false, true, false)
	case len(parts) == 3 && parts[1] == "approve-tool" && parts[2] == "stream":
		s.handleWorkflowRunToolApproval(w, r, run, true, true, false)
	case len(parts) == 2 && parts[1] == "deny-tool":
		s.handleWorkflowRunToolApproval(w, r, run, false, false, false)
	case len(parts) == 3 && parts[1] == "deny-tool" && parts[2] == "stream":
		s.handleWorkflowRunToolApproval(w, r, run, true, false, false)
	case len(parts) == 2 && parts[1] == "approve-tools":
		s.handleWorkflowRunToolApproval(w, r, run, false, true, true)
	case len(parts) == 3 && parts[1] == "approve-tools" && parts[2] == "stream":
		s.handleWorkflowRunToolApproval(w, r, run, true, true, true)
	case len(parts) == 2 && parts[1] == "retry":
		s.handleWorkflowRunRetry(w, r, run, false)
	case len(parts) == 3 && parts[1] == "retry" && parts[2] == "stream":
		s.handleWorkflowRunRetry(w, r, run, true)
	case len(parts) == 2 && parts[1] == "input":
		s.handleWorkflowRunInput(w, r, run, false)
	case len(parts) == 3 && parts[1] == "input" && parts[2] == "stream":
		s.handleWorkflowRunInput(w, r, run, true)
	case len(parts) == 2 && parts[1] == "resume-sub-workflow":
		s.handleWorkflowRunSubWorkflowResume(w, r, run, false)
	case len(parts) == 3 && parts[1] == "resume-sub-workflow" && parts[2] == "stream":
		s.handleWorkflowRunSubWorkflowResume(w, r, run, true)
	case len(parts) == 2 && parts[1] == "continue-output":
		s.handleWorkflowRunContinueOutput(w, r, run, false)
	case len(parts) == 3 && parts[1] == "continue-output" && parts[2] == "stream":
		s.handleWorkflowRunContinueOutput(w, r, run, true)
	case len(parts) == 2 && parts[1] == "approve-budget":
		s.handleWorkflowRunBudgetApproval(w, r, run, false)
	case len(parts) == 3 && parts[1] == "approve-budget" && parts[2] == "stream":
		s.handleWorkflowRunBudgetApproval(w, r, run, true)
	case len(parts) == 2 && parts[1] == "escalate-model":
		s.handleWorkflowRunModelEscalation(w, r, run, false)
	case len(parts) == 3 && parts[1] == "escalate-model" && parts[2] == "stream":
		s.handleWorkflowRunModelEscalation(w, r, run, true)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleWorkflowRunCancel(w http.ResponseWriter, run session.WorkflowRunSnapshot) {
	if strings.TrimSpace(run.ID) == "" {
		http.Error(w, "workflow run not found", http.StatusNotFound)
		return
	}
	cancel, ok := s.workflowRunCancelFunc(run.ID)
	if ok {
		updated, _ := s.runtime.RequestWorkflowRunCancel(run.ID, "workflow run cancellation requested")
		cancel()
		writeJSON(w, updated)
		return
	}
	if workflowRunStatusCancellable(run.Status) {
		updated, _ := s.runtime.CancelWorkflowRunAndApprovals(run.ID, "workflow run cancelled while paused")
		writeJSON(w, updated)
		return
	}
	http.Error(w, "workflow run is not active or paused", http.StatusConflict)
}

func (s *Server) handleWorkflowRunEventsStream(w http.ResponseWriter, r *http.Request, runID string) {
	writer, ok := newSSEWriter(w)
	if !ok {
		return
	}
	if err := writer.writeRetry(durableRunSSERetryMillis); err != nil {
		return
	}
	query := workflowRunQueryFromRequest(r)
	lastSeq := query.Since
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	heartbeat := time.NewTicker(durableRunSSEHeartbeatInterval)
	defer heartbeat.Stop()

	for {
		run, exists := workflowRunByID(s.sessionSnapshotWithApprovalRisk().WorkflowRuns, runID)
		if !exists {
			_ = writer.writeNamedJSON("workflow_run_error", map[string]string{"error": "workflow run not found"})
			return
		}
		query.Since = lastSeq
		events := workflowRunEventsForQuery(run.Events, query)
		for _, event := range events {
			if event.Seq > lastSeq {
				lastSeq = event.Seq
			}
			if err := writer.writeWorkflowRunEvent(event); err != nil {
				return
			}
		}
		if workflowRunStatusStreamComplete(run.Status) {
			pending := s.sessionSnapshotWithApprovalRisk().PendingApprovals
			_ = writer.writeNamedJSON("workflow_run_snapshot", s.workflowRunWithApprovalRisk(run, pending))
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if err := writer.writeComment("goflow-heartbeat"); err != nil {
				return
			}
		case <-ticker.C:
		}
	}
}

func (s *Server) handleWorkflowRunApproval(w http.ResponseWriter, r *http.Request, run session.WorkflowRunSnapshot, stream bool) {
	if !strings.EqualFold(strings.TrimSpace(run.Status), "awaiting_approval") {
		http.Error(w, "workflow run is not awaiting approval", http.StatusConflict)
		return
	}
	if !s.workflowRunApprovalResumable(run) {
		http.Error(w, "workflow approval cannot be resumed durably; retry or cancel this run", http.StatusConflict)
		return
	}
	req, err := decodeWorkflowRunActionRequest(r)
	if err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.Background && !stream {
		if !s.ensureWorkspaceConfirmedForJSON(w, "workflow approval resume runs workspace-scoped stages") {
			return
		}
		accepted, err := s.startWorkflowRunBackgroundAction(run.ID, run.Name, "approve", func(ctx context.Context) (agent.WorkflowResult, error) {
			return s.resumeWorkflowApprovalWithCancelLocked(ctx, run.ID, nil)
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(accepted)
		return
	}
	if stream {
		if !s.ensureWorkspaceConfirmedForWorkflowStream(w) {
			return
		}
		writer, ok := newSSEWriter(w)
		if !ok {
			return
		}
		result, err := s.resumeWorkflowApprovalWithCancel(r.Context(), run.ID, writer.write)
		if err != nil {
			_ = writer.write(schema.StreamEvent{Type: schema.StreamEventError, Content: err.Error(), IsError: true})
			return
		}
		_ = writer.writeWorkflowResult(result)
		return
	}
	if !s.ensureWorkspaceConfirmedForJSON(w, "workflow approval resume runs workspace-scoped stages") {
		return
	}
	result, err := s.resumeWorkflowApprovalWithCancel(r.Context(), run.ID, nil)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, context.Canceled) {
			status = http.StatusConflict
		}
		http.Error(w, err.Error(), status)
		return
	}
	writeJSON(w, result)
}

func (s *Server) handleWorkflowRunToolApproval(w http.ResponseWriter, r *http.Request, run session.WorkflowRunSnapshot, stream, approve, remember bool) {
	if !strings.EqualFold(strings.TrimSpace(run.Status), "awaiting_tool_approval") {
		http.Error(w, "workflow run is not awaiting tool approval", http.StatusConflict)
		return
	}
	if strings.TrimSpace(run.Name) == "" {
		http.Error(w, "workflow run cannot approve tools without workflow name", http.StatusBadRequest)
		return
	}
	if !s.workflowRunToolApprovalResumable(run) {
		http.Error(w, "workflow tool approval context is not available in this process; retry or cancel this run", http.StatusConflict)
		return
	}
	req, err := decodeWorkflowRunActionRequest(r)
	if err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.Background && !stream {
		if !s.ensureWorkspaceConfirmedForJSON(w, "workflow tool approval resume runs workspace-scoped stages") {
			return
		}
		accepted, err := s.startWorkflowRunBackgroundAction(run.ID, run.Name, workflowRunToolApprovalActionName(approve, remember), func(ctx context.Context) (agent.WorkflowResult, error) {
			return s.resumeWorkflowToolApprovalWithCancelLocked(ctx, run.ID, approve, remember, nil)
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(accepted)
		return
	}
	if stream {
		if !s.ensureWorkspaceConfirmedForWorkflowStream(w) {
			return
		}
		writer, ok := newSSEWriter(w)
		if !ok {
			return
		}
		result, err := s.resumeWorkflowToolApprovalWithCancel(r.Context(), run.ID, approve, remember, writer.write)
		if err != nil {
			_ = writer.write(schema.StreamEvent{Type: schema.StreamEventError, Content: err.Error(), IsError: true})
			return
		}
		_ = writer.writeWorkflowResult(result)
		return
	}
	if !s.ensureWorkspaceConfirmedForJSON(w, "workflow tool approval resume runs workspace-scoped stages") {
		return
	}
	result, err := s.resumeWorkflowToolApprovalWithCancel(r.Context(), run.ID, approve, remember, nil)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, context.Canceled) {
			status = http.StatusConflict
		}
		http.Error(w, err.Error(), status)
		return
	}
	writeJSON(w, result)
}

func (s *Server) handleWorkflowRunRetry(w http.ResponseWriter, r *http.Request, run session.WorkflowRunSnapshot, stream bool) {
	if strings.TrimSpace(run.Name) == "" || strings.TrimSpace(run.Request) == "" {
		http.Error(w, "workflow run cannot be retried without name and request", http.StatusBadRequest)
		return
	}
	if workflowRunStatusActive(run.Status) {
		activeStatus := strings.ToLower(strings.TrimSpace(run.Status))
		if activeStatus != "paused_need_more_budget" && activeStatus != "awaiting_budget_approval" {
			http.Error(w, "workflow run is still active", http.StatusConflict)
			return
		}
	}
	req, err := decodeWorkflowRunActionRequest(r)
	if err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.Background && !stream {
		if !s.ensureWorkspaceConfirmedForJSON(w, "workflow retry runs workspace-scoped stages") {
			return
		}
		accepted, err := s.startWorkflowBackground(run.Name, run.Request, run.ID, true, "retry")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(accepted)
		return
	}
	if stream {
		if !s.ensureWorkspaceConfirmedForWorkflowStream(w) {
			return
		}
		writer, ok := newSSEWriter(w)
		if !ok {
			return
		}
		result, err := s.runWorkflowWithCancel(r.Context(), run.Name, run.Request, run.ID, true, writer.write)
		if err != nil {
			_ = writer.write(schema.StreamEvent{Type: schema.StreamEventError, Content: err.Error(), IsError: true})
			return
		}
		_ = writer.writeWorkflowResult(result)
		return
	}
	if !s.ensureWorkspaceConfirmedForJSON(w, "workflow retry runs workspace-scoped stages") {
		return
	}
	result, err := s.runWorkflowWithCancel(r.Context(), run.Name, run.Request, run.ID, true, nil)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, context.Canceled) {
			status = http.StatusConflict
		}
		http.Error(w, err.Error(), status)
		return
	}
	writeJSON(w, result)
}

type workflowRunInputRequest struct {
	Inputs     map[string]any `json:"inputs"`
	Background bool           `json:"background,omitempty"`
}

func (s *Server) handleWorkflowRunInput(w http.ResponseWriter, r *http.Request, run session.WorkflowRunSnapshot, stream bool) {
	if !strings.EqualFold(strings.TrimSpace(run.Status), "awaiting_input") {
		http.Error(w, "workflow run is not awaiting input", http.StatusConflict)
		return
	}
	var req workflowRunInputRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.Background && !stream {
		if !s.ensureWorkspaceConfirmedForJSON(w, "workflow input resume runs workspace-scoped stages") {
			return
		}
		inputs := normalizeWorkflowRunInputValues(req.Inputs)
		inputValues := normalizeWorkflowRunTypedInputValues(req.Inputs)
		accepted, err := s.startWorkflowRunBackgroundAction(run.ID, run.Name, "input", func(ctx context.Context) (agent.WorkflowResult, error) {
			return s.resumeWorkflowInputWithCancelLocked(ctx, run.ID, inputs, inputValues, nil)
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(accepted)
		return
	}
	if stream {
		if !s.ensureWorkspaceConfirmedForWorkflowStream(w) {
			return
		}
		writer, ok := newSSEWriter(w)
		if !ok {
			return
		}
		inputs := normalizeWorkflowRunInputValues(req.Inputs)
		inputValues := normalizeWorkflowRunTypedInputValues(req.Inputs)
		result, err := s.resumeWorkflowInputWithCancel(r.Context(), run.ID, inputs, inputValues, writer.write)
		if err != nil {
			_ = writer.write(schema.StreamEvent{Type: schema.StreamEventError, Content: err.Error(), IsError: true})
			return
		}
		_ = writer.writeWorkflowResult(result)
		return
	}
	if !s.ensureWorkspaceConfirmedForJSON(w, "workflow input resume runs workspace-scoped stages") {
		return
	}
	inputs := normalizeWorkflowRunInputValues(req.Inputs)
	inputValues := normalizeWorkflowRunTypedInputValues(req.Inputs)
	result, err := s.resumeWorkflowInputWithCancel(r.Context(), run.ID, inputs, inputValues, nil)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, context.Canceled) {
			status = http.StatusConflict
		}
		http.Error(w, err.Error(), status)
		return
	}
	writeJSON(w, result)
}

func (s *Server) handleWorkflowRunSubWorkflowResume(w http.ResponseWriter, r *http.Request, run session.WorkflowRunSnapshot, stream bool) {
	if !strings.EqualFold(strings.TrimSpace(run.Status), "awaiting_sub_workflow") {
		http.Error(w, "workflow run is not awaiting a sub-workflow", http.StatusConflict)
		return
	}
	if !s.workflowRunSubWorkflowResumable(run) {
		http.Error(w, "nested sub-workflow is not completed yet", http.StatusConflict)
		return
	}
	req, err := decodeWorkflowRunActionRequest(r)
	if err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.Background && !stream {
		if !s.ensureWorkspaceConfirmedForJSON(w, "sub-workflow resume runs workspace-scoped stages") {
			return
		}
		accepted, err := s.startWorkflowRunBackgroundAction(run.ID, run.Name, "resume-sub-workflow", func(ctx context.Context) (agent.WorkflowResult, error) {
			return s.resumeWorkflowSubWorkflowWithCancelLocked(ctx, run.ID, nil)
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(accepted)
		return
	}
	if stream {
		if !s.ensureWorkspaceConfirmedForWorkflowStream(w) {
			return
		}
		writer, ok := newSSEWriter(w)
		if !ok {
			return
		}
		result, err := s.resumeWorkflowSubWorkflowWithCancel(r.Context(), run.ID, writer.write)
		if err != nil {
			_ = writer.write(schema.StreamEvent{Type: schema.StreamEventError, Content: err.Error(), IsError: true})
			return
		}
		_ = writer.writeWorkflowResult(result)
		return
	}
	if !s.ensureWorkspaceConfirmedForJSON(w, "sub-workflow resume runs workspace-scoped stages") {
		return
	}
	result, err := s.resumeWorkflowSubWorkflowWithCancel(r.Context(), run.ID, nil)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, context.Canceled) {
			status = http.StatusConflict
		}
		http.Error(w, err.Error(), status)
		return
	}
	writeJSON(w, result)
}

func (s *Server) handleWorkflowRunContinueOutput(w http.ResponseWriter, r *http.Request, run session.WorkflowRunSnapshot, stream bool) {
	status := strings.ToLower(strings.TrimSpace(run.Status))
	if status != "paused_need_more_budget" {
		http.Error(w, "workflow run is not paused for more output budget", http.StatusConflict)
		return
	}
	resumable, reason := s.workflowRunContinueOutputResumable(run)
	if !resumable {
		http.Error(w, reason, http.StatusConflict)
		return
	}
	req, err := decodeWorkflowRunActionRequest(r)
	if err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.Background && !stream {
		if !s.ensureWorkspaceConfirmedForJSON(w, "workflow output continuation runs workspace-scoped stages") {
			return
		}
		accepted, err := s.startWorkflowRunBackgroundAction(run.ID, run.Name, "continue-output", func(ctx context.Context) (agent.WorkflowResult, error) {
			return s.resumeWorkflowContinueOutputWithCancelLocked(ctx, run.ID, nil)
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(accepted)
		return
	}
	if stream {
		if !s.ensureWorkspaceConfirmedForWorkflowStream(w) {
			return
		}
		writer, ok := newSSEWriter(w)
		if !ok {
			return
		}
		result, err := s.resumeWorkflowContinueOutputWithCancel(r.Context(), run.ID, writer.write)
		if err != nil {
			_ = writer.write(schema.StreamEvent{Type: schema.StreamEventError, Content: err.Error(), IsError: true})
			return
		}
		_ = writer.writeWorkflowResult(result)
		return
	}
	if !s.ensureWorkspaceConfirmedForJSON(w, "workflow output continuation runs workspace-scoped stages") {
		return
	}
	result, err := s.resumeWorkflowContinueOutputWithCancel(r.Context(), run.ID, nil)
	if err != nil {
		statusCode := http.StatusBadRequest
		if errors.Is(err, context.Canceled) {
			statusCode = http.StatusConflict
		}
		http.Error(w, err.Error(), statusCode)
		return
	}
	writeJSON(w, result)
}

func (s *Server) handleWorkflowRunModelEscalation(w http.ResponseWriter, r *http.Request, run session.WorkflowRunSnapshot, stream bool) {
	resumable, reason := s.workflowRunModelEscalationResumable(run)
	if !resumable {
		http.Error(w, reason, http.StatusConflict)
		return
	}
	req, err := decodeWorkflowRunActionRequest(r)
	if err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.Background && !stream {
		if !s.ensureWorkspaceConfirmedForJSON(w, "workflow model escalation reruns workspace-scoped stages") {
			return
		}
		accepted, err := s.startWorkflowRunBackgroundAction(run.ID, run.Name, "escalate-model", func(ctx context.Context) (agent.WorkflowResult, error) {
			return s.resumeWorkflowEscalateModelWithCancelLocked(ctx, run.ID, nil)
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(accepted)
		return
	}
	if stream {
		if !s.ensureWorkspaceConfirmedForWorkflowStream(w) {
			return
		}
		writer, ok := newSSEWriter(w)
		if !ok {
			return
		}
		result, err := s.resumeWorkflowEscalateModelWithCancel(r.Context(), run.ID, writer.write)
		if err != nil {
			_ = writer.write(schema.StreamEvent{Type: schema.StreamEventError, Content: err.Error(), IsError: true})
			return
		}
		_ = writer.writeWorkflowResult(result)
		return
	}
	if !s.ensureWorkspaceConfirmedForJSON(w, "workflow model escalation reruns workspace-scoped stages") {
		return
	}
	result, err := s.resumeWorkflowEscalateModelWithCancel(r.Context(), run.ID, nil)
	if err != nil {
		statusCode := http.StatusBadRequest
		if errors.Is(err, context.Canceled) {
			statusCode = http.StatusConflict
		}
		http.Error(w, err.Error(), statusCode)
		return
	}
	writeJSON(w, result)
}

func (s *Server) handleWorkflowRunBudgetApproval(w http.ResponseWriter, r *http.Request, run session.WorkflowRunSnapshot, stream bool) {
	if !strings.EqualFold(strings.TrimSpace(run.Status), "awaiting_budget_approval") {
		http.Error(w, "workflow run is not awaiting budget approval", http.StatusConflict)
		return
	}
	resumable, reason := s.workflowRunBudgetApprovalResumable(run)
	if !resumable {
		http.Error(w, reason, http.StatusConflict)
		return
	}
	req, err := decodeWorkflowRunActionRequest(r)
	if err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.Background && !stream {
		if !s.ensureWorkspaceConfirmedForJSON(w, "workflow budget approval resume runs workspace-scoped stages") {
			return
		}
		accepted, err := s.startWorkflowRunBackgroundAction(run.ID, run.Name, "approve-budget", func(ctx context.Context) (agent.WorkflowResult, error) {
			return s.resumeWorkflowApproveBudgetWithCancelLocked(ctx, run.ID, nil)
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(accepted)
		return
	}
	if stream {
		if !s.ensureWorkspaceConfirmedForWorkflowStream(w) {
			return
		}
		writer, ok := newSSEWriter(w)
		if !ok {
			return
		}
		result, err := s.resumeWorkflowApproveBudgetWithCancel(r.Context(), run.ID, writer.write)
		if err != nil {
			_ = writer.write(schema.StreamEvent{Type: schema.StreamEventError, Content: err.Error(), IsError: true})
			return
		}
		_ = writer.writeWorkflowResult(result)
		return
	}
	if !s.ensureWorkspaceConfirmedForJSON(w, "workflow budget approval resume runs workspace-scoped stages") {
		return
	}
	result, err := s.resumeWorkflowApproveBudgetWithCancel(r.Context(), run.ID, nil)
	if err != nil {
		statusCode := http.StatusBadRequest
		if errors.Is(err, context.Canceled) {
			statusCode = http.StatusConflict
		}
		http.Error(w, err.Error(), statusCode)
		return
	}
	writeJSON(w, result)
}

func normalizeWorkflowRunInputValues(values map[string]any) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		switch typed := value.(type) {
		case nil:
			out[key] = ""
		case string:
			out[key] = typed
		case bool:
			out[key] = strconv.FormatBool(typed)
		case float64:
			out[key] = strconv.FormatFloat(typed, 'f', -1, 64)
		default:
			data, err := json.Marshal(typed)
			if err != nil {
				out[key] = fmt.Sprint(typed)
				continue
			}
			out[key] = string(data)
		}
	}
	return out
}

func normalizeWorkflowRunTypedInputValues(values map[string]any) map[string]any {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		out[key] = copyWorkflowRunInputValue(value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func copyWorkflowRunInputValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = copyWorkflowRunInputValue(item)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = copyWorkflowRunInputValue(item)
		}
		return out
	default:
		return typed
	}
}

func (s *Server) runWorkflowWithCancel(ctx context.Context, workflowName, input, retryOf string, approve bool, handler func(event schema.StreamEvent) error) (agent.WorkflowResult, error) {
	s.workflowExecMu.Lock()
	defer s.workflowExecMu.Unlock()
	return s.runWorkflowWithCancelHook(ctx, workflowName, input, retryOf, approve, handler, nil)
}

func (s *Server) runWorkflowWithCancelHook(ctx context.Context, workflowName, input, retryOf string, approve bool, handler func(event schema.StreamEvent) error, onStart func(string)) (agent.WorkflowResult, error) {
	ctx, cancel := context.WithCancel(ctx)
	runID := ""
	handler = s.streamHandlerWithApprovalRisk(handler)
	ctx = agent.WithWorkflowRunStartHook(ctx, func(id string) {
		runID = id
		s.registerActiveWorkflowRun(id, cancel)
		if onStart != nil {
			onStart(id)
		}
	})
	if strings.TrimSpace(retryOf) != "" {
		ctx = agent.WithWorkflowRunRetryOf(ctx, retryOf)
	}
	defer func() {
		if strings.TrimSpace(runID) != "" {
			s.unregisterActiveWorkflowRun(runID)
		}
		cancel()
	}()
	return s.runtime.WorkflowRunner().Run(ctx, workflowName, input, approve, handler)
}

func (s *Server) resumeWorkflowInputWithCancel(ctx context.Context, runID string, inputs map[string]string, inputValues map[string]any, handler func(event schema.StreamEvent) error) (agent.WorkflowResult, error) {
	s.workflowExecMu.Lock()
	defer s.workflowExecMu.Unlock()
	return s.resumeWorkflowInputWithCancelLocked(ctx, runID, inputs, inputValues, handler)
}

func (s *Server) resumeWorkflowInputWithCancelLocked(ctx context.Context, runID string, inputs map[string]string, inputValues map[string]any, handler func(event schema.StreamEvent) error) (agent.WorkflowResult, error) {
	ctx, cancel := context.WithCancel(ctx)
	s.registerActiveWorkflowRun(runID, cancel)
	defer func() {
		s.unregisterActiveWorkflowRun(runID)
		cancel()
	}()
	if len(inputValues) > 0 {
		return s.runtime.WorkflowRunner().ResumeInputValues(ctx, runID, inputValues, s.streamHandlerWithApprovalRisk(handler))
	}
	return s.runtime.WorkflowRunner().ResumeInput(ctx, runID, inputs, s.streamHandlerWithApprovalRisk(handler))
}

func (s *Server) resumeWorkflowApprovalWithCancel(ctx context.Context, runID string, handler func(event schema.StreamEvent) error) (agent.WorkflowResult, error) {
	s.workflowExecMu.Lock()
	defer s.workflowExecMu.Unlock()
	return s.resumeWorkflowApprovalWithCancelLocked(ctx, runID, handler)
}

func (s *Server) resumeWorkflowApprovalWithCancelLocked(ctx context.Context, runID string, handler func(event schema.StreamEvent) error) (agent.WorkflowResult, error) {
	ctx, cancel := context.WithCancel(ctx)
	s.registerActiveWorkflowRun(runID, cancel)
	defer func() {
		s.unregisterActiveWorkflowRun(runID)
		cancel()
	}()
	return s.runtime.WorkflowRunner().ResumeApproval(ctx, runID, s.streamHandlerWithApprovalRisk(handler))
}

func (s *Server) resumeWorkflowSubWorkflowWithCancel(ctx context.Context, runID string, handler func(event schema.StreamEvent) error) (agent.WorkflowResult, error) {
	s.workflowExecMu.Lock()
	defer s.workflowExecMu.Unlock()
	return s.resumeWorkflowSubWorkflowWithCancelLocked(ctx, runID, handler)
}

func (s *Server) resumeWorkflowSubWorkflowWithCancelLocked(ctx context.Context, runID string, handler func(event schema.StreamEvent) error) (agent.WorkflowResult, error) {
	ctx, cancel := context.WithCancel(ctx)
	s.registerActiveWorkflowRun(runID, cancel)
	defer func() {
		s.unregisterActiveWorkflowRun(runID)
		cancel()
	}()
	return s.runtime.WorkflowRunner().ResumeSubWorkflow(ctx, runID, s.streamHandlerWithApprovalRisk(handler))
}

func (s *Server) resumeWorkflowContinueOutputWithCancel(ctx context.Context, runID string, handler func(event schema.StreamEvent) error) (agent.WorkflowResult, error) {
	s.workflowExecMu.Lock()
	defer s.workflowExecMu.Unlock()
	return s.resumeWorkflowContinueOutputWithCancelLocked(ctx, runID, handler)
}

func (s *Server) resumeWorkflowContinueOutputWithCancelLocked(ctx context.Context, runID string, handler func(event schema.StreamEvent) error) (agent.WorkflowResult, error) {
	ctx, cancel := context.WithCancel(ctx)
	s.registerActiveWorkflowRun(runID, cancel)
	defer func() {
		s.unregisterActiveWorkflowRun(runID)
		cancel()
	}()
	return s.runtime.WorkflowRunner().ContinueOutput(ctx, runID, s.streamHandlerWithApprovalRisk(handler))
}

func (s *Server) resumeWorkflowEscalateModelWithCancel(ctx context.Context, runID string, handler func(event schema.StreamEvent) error) (agent.WorkflowResult, error) {
	s.workflowExecMu.Lock()
	defer s.workflowExecMu.Unlock()
	return s.resumeWorkflowEscalateModelWithCancelLocked(ctx, runID, handler)
}

func (s *Server) resumeWorkflowEscalateModelWithCancelLocked(ctx context.Context, runID string, handler func(event schema.StreamEvent) error) (agent.WorkflowResult, error) {
	ctx, cancel := context.WithCancel(ctx)
	s.registerActiveWorkflowRun(runID, cancel)
	defer func() {
		s.unregisterActiveWorkflowRun(runID)
		cancel()
	}()
	return s.runtime.WorkflowRunner().EscalateModel(ctx, runID, s.streamHandlerWithApprovalRisk(handler))
}

func (s *Server) resumeWorkflowApproveBudgetWithCancel(ctx context.Context, runID string, handler func(event schema.StreamEvent) error) (agent.WorkflowResult, error) {
	s.workflowExecMu.Lock()
	defer s.workflowExecMu.Unlock()
	return s.resumeWorkflowApproveBudgetWithCancelLocked(ctx, runID, handler)
}

func (s *Server) resumeWorkflowApproveBudgetWithCancelLocked(ctx context.Context, runID string, handler func(event schema.StreamEvent) error) (agent.WorkflowResult, error) {
	ctx, cancel := context.WithCancel(ctx)
	s.registerActiveWorkflowRun(runID, cancel)
	defer func() {
		s.unregisterActiveWorkflowRun(runID)
		cancel()
	}()
	return s.runtime.WorkflowRunner().ApproveBudget(ctx, runID, s.streamHandlerWithApprovalRisk(handler))
}

func (s *Server) resumeWorkflowToolApprovalWithCancel(ctx context.Context, runID string, approve, remember bool, handler func(event schema.StreamEvent) error) (agent.WorkflowResult, error) {
	s.workflowExecMu.Lock()
	defer s.workflowExecMu.Unlock()
	return s.resumeWorkflowToolApprovalWithCancelLocked(ctx, runID, approve, remember, handler)
}

func (s *Server) resumeWorkflowToolApprovalWithCancelLocked(ctx context.Context, runID string, approve, remember bool, handler func(event schema.StreamEvent) error) (agent.WorkflowResult, error) {
	ctx, cancel := context.WithCancel(ctx)
	s.registerActiveWorkflowRun(runID, cancel)
	defer func() {
		s.unregisterActiveWorkflowRun(runID)
		cancel()
	}()
	return s.runtime.WorkflowRunner().ResumeToolApproval(ctx, runID, approve, remember, s.streamHandlerWithApprovalRisk(handler))
}

func (s *Server) startWorkflowBackground(workflowName, input, retryOf string, approve bool, action string) (workflowRunBackgroundResponse, error) {
	if !s.workflowExecMu.TryLock() {
		return workflowRunBackgroundResponse{}, fmt.Errorf("another workflow run is already active")
	}
	started := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		defer s.workflowExecMu.Unlock()
		_, err := s.runWorkflowWithCancelHook(context.Background(), workflowName, input, retryOf, approve, nil, func(runID string) {
			select {
			case started <- runID:
			default:
			}
		})
		done <- err
	}()

	select {
	case runID := <-started:
		return s.workflowRunBackgroundResponse(runID, workflowName, action, retryOf), nil
	case err := <-done:
		if err == nil {
			return workflowRunBackgroundResponse{}, fmt.Errorf("workflow completed before run id was allocated")
		}
		return workflowRunBackgroundResponse{}, err
	case <-time.After(5 * time.Second):
		return workflowRunBackgroundResponse{}, fmt.Errorf("workflow run did not allocate a run id in time")
	}
}

func (s *Server) startWorkflowRunBackgroundAction(runID, workflowName, action string, run func(context.Context) (agent.WorkflowResult, error)) (workflowRunBackgroundResponse, error) {
	if !s.workflowExecMu.TryLock() {
		return workflowRunBackgroundResponse{}, fmt.Errorf("another workflow run is already active")
	}
	go func() {
		defer s.workflowExecMu.Unlock()
		if _, err := run(context.Background()); err != nil {
			s.runtime.AppendWorkflowRunEvent(runID, session.WorkflowRunEventSnapshot{
				Type:           string(schema.StreamEventError),
				Content:        err.Error(),
				IsError:        true,
				WorkflowName:   workflowName,
				WorkflowStatus: "action_failed",
			})
		}
	}()
	return s.workflowRunBackgroundResponse(runID, workflowName, action, ""), nil
}

func (s *Server) workflowRunBackgroundResponse(runID, workflowName, action, retryOf string) workflowRunBackgroundResponse {
	resp := workflowRunBackgroundResponse{
		RunID:     strings.TrimSpace(runID),
		Name:      strings.TrimSpace(workflowName),
		Status:    "running",
		Action:    strings.TrimSpace(action),
		RetryOf:   strings.TrimSpace(retryOf),
		EventsURL: workflowRunEventsStreamPath(runID),
	}
	if run, ok := workflowRunByID(s.sessionSnapshotWithApprovalRisk().WorkflowRuns, runID); ok {
		resp.Name = run.Name
		resp.Status = run.Status
		resp.Run = &run
	}
	return resp
}

func (s *Server) registerActiveWorkflowRun(runID string, cancel context.CancelFunc) {
	if strings.TrimSpace(runID) == "" || cancel == nil {
		return
	}
	s.activeWorkflowMu.Lock()
	defer s.activeWorkflowMu.Unlock()
	if s.activeWorkflowRuns == nil {
		s.activeWorkflowRuns = make(map[string]context.CancelFunc)
	}
	s.activeWorkflowRuns[runID] = cancel
}

func (s *Server) unregisterActiveWorkflowRun(runID string) {
	if strings.TrimSpace(runID) == "" {
		return
	}
	s.activeWorkflowMu.Lock()
	defer s.activeWorkflowMu.Unlock()
	delete(s.activeWorkflowRuns, runID)
}

func (s *Server) workflowRunCancelFunc(runID string) (context.CancelFunc, bool) {
	if strings.TrimSpace(runID) == "" {
		return nil, false
	}
	s.activeWorkflowMu.Lock()
	defer s.activeWorkflowMu.Unlock()
	cancel, ok := s.activeWorkflowRuns[runID]
	return cancel, ok
}

type workflowRunStageDetail struct {
	RunID     string                             `json:"run_id"`
	Stage     string                             `json:"stage"`
	Status    string                             `json:"status,omitempty"`
	Snapshot  *session.WorkflowRunStageSnapshot  `json:"snapshot,omitempty"`
	Events    []session.WorkflowRunEventSnapshot `json:"events,omitempty"`
	Artifacts []session.WorkflowRunArtifact      `json:"artifacts,omitempty"`
}

func workflowRunByID(runs []session.WorkflowRunSnapshot, id string) (session.WorkflowRunSnapshot, bool) {
	for _, run := range runs {
		if run.ID == id {
			return run, true
		}
	}
	return session.WorkflowRunSnapshot{}, false
}

func workflowRunStageDetailFor(run session.WorkflowRunSnapshot, stage string) (workflowRunStageDetail, bool) {
	stage = strings.TrimSpace(stage)
	events := workflowRunEventsForStage(run.Events, stage)
	artifacts := workflowRunArtifactsForStage(run.Artifacts, stage)
	detail := workflowRunStageDetail{
		RunID:     run.ID,
		Stage:     stage,
		Events:    events,
		Artifacts: artifacts,
	}
	for _, completed := range run.CompletedStages {
		if workflowRunStageEqual(completed.Stage, stage) {
			stageSnapshot := completed
			detail.Stage = completed.Stage
			detail.Status = completed.Status
			detail.Snapshot = &stageSnapshot
			return detail, true
		}
	}
	if workflowRunStageEqual(run.NextStage, stage) {
		detail.Stage = run.NextStage
		detail.Status = run.Status
		return detail, true
	}
	return detail, len(events) > 0 || len(artifacts) > 0
}

func (s *Server) workflowRunStageDetailFor(run session.WorkflowRunSnapshot, stage string) (workflowRunStageDetail, bool) {
	detail, ok := workflowRunStageDetailFor(run, stage)
	if !ok || detail.Snapshot == nil {
		return detail, ok
	}
	hydrated := s.runtime.HydrateWorkflowRunStages([]session.WorkflowRunStageSnapshot{*detail.Snapshot})
	if len(hydrated) == 0 {
		return detail, ok
	}
	stageSnapshot := hydrated[0]
	detail.Snapshot = &stageSnapshot
	return detail, ok
}

func workflowRunQueryFromRequest(r *http.Request) workflowRunQuery {
	if r == nil || r.URL == nil {
		return workflowRunQuery{}
	}
	values := r.URL.Query()
	limit, _ := strconv.Atoi(strings.TrimSpace(values.Get("limit")))
	if limit < 0 {
		limit = 0
	}
	if limit > 500 {
		limit = 500
	}
	since, _ := strconv.Atoi(strings.TrimSpace(firstWorkflowRunQueryValue(values.Get("since"), values.Get("cursor"), values.Get("after"))))
	if since < 0 {
		since = 0
	}
	if since == 0 {
		since, _ = strconv.Atoi(strings.TrimSpace(r.Header.Get("Last-Event-ID")))
		if since < 0 {
			since = 0
		}
	}
	stage := strings.TrimSpace(firstWorkflowRunQueryValue(values.Get("stage"), values.Get("focus_stage"), values.Get("current_stage")))
	if stage == "" {
		stage = workflowRunStageFromAnchor(values.Get("anchor"))
	}
	query := workflowRunQuery{
		Stage:           stage,
		ItemKind:        normalizeWorkflowRunQueryToken(firstWorkflowRunQueryValue(values.Get("kind"), values.Get("item_kind"))),
		EventType:       normalizeWorkflowRunQueryToken(firstWorkflowRunQueryValue(values.Get("event_type"), values.Get("type"))),
		ArtifactKind:    normalizeWorkflowRunQueryToken(firstWorkflowRunQueryValue(values.Get("artifact_kind"), values.Get("artifact_type"))),
		Status:          normalizeWorkflowRunQueryToken(values.Get("status")),
		AgentID:         strings.TrimSpace(values.Get("agent_id")),
		ToolName:        strings.TrimSpace(values.Get("tool_name")),
		Query:           strings.TrimSpace(firstWorkflowRunQueryValue(values.Get("q"), values.Get("query"), values.Get("search"))),
		Limit:           limit,
		Since:           since,
		ErrorsOnly:      workflowRunQueryBool(values.Get("errors_only")) || workflowRunQueryBool(values.Get("error")),
		NeedsActionOnly: workflowRunQueryBool(values.Get("needs_action")) || workflowRunQueryBool(values.Get("pending")),
		SuspendedOnly:   workflowRunQueryBool(values.Get("suspended")),
		IncludeContent:  workflowRunQueryBool(values.Get("include_content")) || workflowRunQueryBool(values.Get("content")),
	}
	return query
}

func workflowRunStageFromAnchor(anchor string) string {
	anchor = strings.TrimSpace(anchor)
	if anchor == "" {
		return ""
	}
	for _, prefix := range []string{"stage:", "stage/"} {
		if strings.HasPrefix(strings.ToLower(anchor), prefix) {
			return strings.TrimSpace(anchor[len(prefix):])
		}
	}
	return ""
}

func workflowRunQueryHasFilters(query workflowRunQuery) bool {
	return strings.TrimSpace(query.Stage) != "" ||
		query.ItemKind != "" ||
		query.EventType != "" ||
		query.ArtifactKind != "" ||
		query.Status != "" ||
		strings.TrimSpace(query.AgentID) != "" ||
		strings.TrimSpace(query.ToolName) != "" ||
		strings.TrimSpace(query.Query) != "" ||
		query.Limit > 0 ||
		query.Since > 0 ||
		query.ErrorsOnly ||
		query.NeedsActionOnly ||
		query.SuspendedOnly
}

func workflowRunArtifactQueryFromRequest(r *http.Request) workflowRunQuery {
	query := workflowRunQueryFromRequest(r)
	if query.ArtifactKind == "" && query.ItemKind != "" && !workflowRunQueryItemKind(query.ItemKind) {
		query.ArtifactKind = query.ItemKind
		query.ItemKind = ""
	}
	return query
}

func (q workflowRunQuery) withStage(stage string) workflowRunQuery {
	q.Stage = strings.TrimSpace(stage)
	return q
}

func firstWorkflowRunQueryValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func workflowRunQueryBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func normalizeWorkflowRunQueryToken(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func workflowRunQueryItemKind(value string) bool {
	switch normalizeWorkflowRunQueryToken(value) {
	case "event", "events", "stage", "stages", "artifact", "artifacts":
		return true
	default:
		return false
	}
}

func workflowRunEventsForStage(events []session.WorkflowRunEventSnapshot, stage string) []session.WorkflowRunEventSnapshot {
	return workflowRunEventsForQuery(events, workflowRunQuery{Stage: strings.TrimSpace(stage)})
}

func workflowRunArtifactsForStage(artifacts []session.WorkflowRunArtifact, stage string) []session.WorkflowRunArtifact {
	return workflowRunArtifactsForQuery(artifacts, workflowRunQuery{Stage: strings.TrimSpace(stage)})
}

func workflowRunEventsForQuery(events []session.WorkflowRunEventSnapshot, query workflowRunQuery) []session.WorkflowRunEventSnapshot {
	if query.ItemKind != "" && query.ItemKind != "event" && query.ItemKind != "events" {
		return nil
	}
	filtered := make([]session.WorkflowRunEventSnapshot, 0, len(events))
	for _, event := range events {
		if !workflowRunEventMatchesQuery(event, query) {
			continue
		}
		filtered = append(filtered, event)
		if query.Limit > 0 && len(filtered) >= query.Limit {
			break
		}
	}
	return filtered
}

func (s *Server) workflowRunEventsForQuery(events []session.WorkflowRunEventSnapshot, query workflowRunQuery) []session.WorkflowRunEventSnapshot {
	filtered := workflowRunEventsForQuery(events, query)
	if len(filtered) == 0 || !query.IncludeContent {
		return filtered
	}
	return s.runtime.HydrateWorkflowRunEvents(filtered)
}

func workflowRunArtifactsForQuery(artifacts []session.WorkflowRunArtifact, query workflowRunQuery) []session.WorkflowRunArtifact {
	if query.ItemKind != "" && query.ItemKind != "artifact" && query.ItemKind != "artifacts" {
		return nil
	}
	filtered := make([]session.WorkflowRunArtifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		if !workflowRunArtifactMatchesQuery(artifact, query) {
			continue
		}
		if artifact.ArtifactRef == "" {
			artifact.ArtifactRef = firstWorkflowRunQueryValue(artifact.Metadata["artifact_ref"], artifact.Metadata["ref"], artifact.Metadata["refs"])
		}
		if artifact.Hash == "" {
			artifact.Hash = firstWorkflowRunQueryValue(artifact.Metadata["hash"], runArtifactHashFromRef(artifact.ArtifactRef))
		}
		if !query.IncludeContent {
			artifact.Content = ""
		}
		filtered = append(filtered, artifact)
		if query.Limit > 0 && len(filtered) >= query.Limit {
			break
		}
	}
	return filtered
}

func (s *Server) workflowRunArtifactsForQuery(artifacts []session.WorkflowRunArtifact, query workflowRunQuery) []session.WorkflowRunArtifact {
	filtered := workflowRunArtifactsForQuery(artifacts, query)
	if len(filtered) == 0 || !query.IncludeContent {
		return filtered
	}
	out := make([]session.WorkflowRunArtifact, len(filtered))
	for i, artifact := range filtered {
		out[i] = s.hydrateWorkflowRunArtifact(artifact)
	}
	return out
}

func (s *Server) hydrateWorkflowRunArtifact(artifact session.WorkflowRunArtifact) session.WorkflowRunArtifact {
	if strings.TrimSpace(artifact.Content) != "" {
		if artifact.ContentBytes == 0 {
			artifact.ContentBytes = len([]byte(artifact.Content))
		}
		if artifact.Size == 0 {
			artifact.Size = artifact.ContentBytes
		}
		return artifact
	}
	if s == nil || s.runtime == nil {
		return artifact
	}
	if artifact.ArtifactRef == "" {
		artifact.ArtifactRef = firstWorkflowRunQueryValue(artifact.Metadata["artifact_ref"], artifact.Metadata["ref"], artifact.Metadata["refs"])
	}
	if artifact.Hash == "" {
		artifact.Hash = firstWorkflowRunQueryValue(artifact.Metadata["hash"], runArtifactHashFromRef(artifact.ArtifactRef))
	}
	for _, ref := range []string{
		artifact.ArtifactRef,
		artifact.Hash,
		artifact.Metadata["artifact_ref"],
		artifact.Metadata["hash"],
		artifact.Metadata["ref"],
		artifact.Metadata["refs"],
	} {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		object, ok, err := s.runtime.ArtifactObject(ref)
		if err != nil || !ok {
			continue
		}
		artifact.Content = object.Content
		artifact.ArtifactRef = firstWorkflowRunQueryValue(artifact.ArtifactRef, object.Ref)
		artifact.Hash = firstWorkflowRunQueryValue(artifact.Hash, object.Hash)
		artifact.Mime = firstWorkflowRunQueryValue(artifact.Mime, object.Mime)
		artifact.Size = firstPositiveInt(artifact.Size, object.Size)
		artifact.ContentBytes = firstPositiveInt(artifact.ContentBytes, object.Size, len([]byte(object.Content)))
		artifact.StoredBytes = firstPositiveInt(artifact.StoredBytes, int(object.StoredBytes))
		artifact.Externalized = true
		if strings.TrimSpace(artifact.Summary) == "" {
			artifact.Summary = object.Summary
		}
		artifact.Metadata = mergeRunArtifactMetadata(artifact.Metadata, map[string]string{
			"artifact_ref":  artifact.ArtifactRef,
			"hash":          artifact.Hash,
			"mime":          artifact.Mime,
			"size":          workflowRunIntString(artifact.Size),
			"content_bytes": workflowRunIntString(artifact.ContentBytes),
			"stored_bytes":  workflowRunIntString(artifact.StoredBytes),
			"externalized":  "true",
		})
		return artifact
	}
	return artifact
}

func workflowRunStagesForQuery(stages []session.WorkflowRunStageSnapshot, query workflowRunQuery) []session.WorkflowRunStageSnapshot {
	if query.ItemKind != "" && query.ItemKind != "stage" && query.ItemKind != "stages" {
		return nil
	}
	filtered := make([]session.WorkflowRunStageSnapshot, 0, len(stages))
	for _, stage := range stages {
		if !workflowRunStageMatchesQuery(stage, query) {
			continue
		}
		if !query.IncludeContent {
			stage.Result.Output = ""
			stage.Result.ToolResults = nil
			stage.Artifacts = workflowRunArtifactsForQuery(stage.Artifacts, query)
		}
		filtered = append(filtered, stage)
		if query.Limit > 0 && len(filtered) >= query.Limit {
			break
		}
	}
	return filtered
}

func (s *Server) workflowRunStagesForQuery(stages []session.WorkflowRunStageSnapshot, query workflowRunQuery) []session.WorkflowRunStageSnapshot {
	filtered := workflowRunStagesForQuery(stages, query)
	if len(filtered) == 0 {
		return filtered
	}
	if query.IncludeContent {
		filtered = s.runtime.HydrateWorkflowRunStages(filtered)
	}
	if !query.IncludeContent {
		return filtered
	}
	out := make([]session.WorkflowRunStageSnapshot, len(filtered))
	for i, stage := range filtered {
		stage.Artifacts = s.workflowRunArtifactsForQuery(stage.Artifacts, query)
		out[i] = stage
	}
	return out
}

func (s *Server) workflowRunTimeline(run session.WorkflowRunSnapshot, query workflowRunQuery) []workflowRunTimelineItem {
	withoutLimit := query
	withoutLimit.Limit = 0
	items := make([]workflowRunTimelineItem, 0, len(run.Events)+len(run.CompletedStages)+len(run.Artifacts))
	for _, event := range s.workflowRunEventsForQuery(run.Events, withoutLimit) {
		items = append(items, workflowRunTimelineItem{
			At:                        event.At,
			Kind:                      "event",
			Type:                      event.Type,
			Stage:                     event.Stage,
			AgentID:                   event.AgentID,
			ToolName:                  event.ToolName,
			ToolCallID:                event.ToolCallID,
			Summary:                   workflowRunTimelineEventSummary(event),
			Content:                   workflowRunTimelineContent(event.Content, query),
			Reason:                    event.Reason,
			Severity:                  event.Severity,
			BudgetScope:               event.BudgetScope,
			BudgetReason:              event.BudgetReason,
			BudgetMetric:              event.BudgetMetric,
			BudgetUsed:                event.BudgetUsed,
			BudgetSoftLimit:           event.BudgetSoftLimit,
			BudgetHardLimit:           event.BudgetHardLimit,
			BudgetRemaining:           event.BudgetRemaining,
			BudgetTotalTokens:         event.BudgetTotalTokens,
			BudgetLLMCalls:            event.BudgetLLMCalls,
			BudgetEstimatedInputCost:  event.BudgetEstimatedInputCost,
			BudgetEstimatedOutputCost: event.BudgetEstimatedOutputCost,
			BudgetEstimatedTotalCost:  event.BudgetEstimatedTotalCost,
			BudgetCostCurrency:        event.BudgetCostCurrency,
			BudgetPricingSource:       event.BudgetPricingSource,
			ContractCheck:             event.ContractCheck,
			SourceRef:                 event.SourceRef,
			StopReason:                event.StopReason,
			ContinuationCount:         event.ContinuationCount,
			Incomplete:                event.Incomplete,
			PromptTokens:              event.PromptTokens,
			OutputTokens:              event.OutputTokens,
			CachedTokens:              event.CachedTokens,
			IsError:                   event.IsError,
			NeedsAction:               event.NeedsAction || event.PendingApproval || event.Incomplete,
			Suspended:                 event.Suspended,
		})
	}
	for _, stage := range s.workflowRunStagesForQuery(run.CompletedStages, withoutLimit) {
		items = append(items, workflowRunTimelineItem{
			At:                        firstWorkflowRunQueryValue(stage.CompletedAt, stage.StartedAt),
			Kind:                      "stage",
			Type:                      stage.NodeType,
			Stage:                     stage.Stage,
			Status:                    stage.Status,
			AgentID:                   stage.AgentID,
			ToolName:                  stage.Tool,
			Title:                     stage.Skill,
			Summary:                   stage.Summary,
			Content:                   workflowRunTimelineContent(stage.Result.Output, query),
			Reason:                    workflowRunStageReason(stage),
			Severity:                  workflowRunStageSeverity(stage),
			BudgetScope:               workflowRunStageBudgetScope(stage),
			BudgetReason:              workflowRunStageBudgetReason(stage),
			BudgetMetric:              workflowRunStageBudgetMetric(stage),
			BudgetUsed:                workflowRunStageBudgetUsed(stage),
			BudgetSoftLimit:           workflowRunStageBudgetSoftLimit(stage),
			BudgetHardLimit:           workflowRunStageBudgetHardLimit(stage),
			BudgetRemaining:           workflowRunStageBudgetRemaining(stage),
			BudgetTotalTokens:         workflowRunStageBudgetTotalTokens(stage),
			BudgetLLMCalls:            workflowRunStageBudgetLLMCalls(stage),
			BudgetEstimatedInputCost:  stage.BudgetEstimatedInputCost,
			BudgetEstimatedOutputCost: stage.BudgetEstimatedOutputCost,
			BudgetEstimatedTotalCost:  stage.BudgetEstimatedTotalCost,
			BudgetCostCurrency:        stage.BudgetCostCurrency,
			BudgetPricingSource:       stage.BudgetPricingSource,
			ContractCheck:             workflowRunStageMetadataValue(stage, "contract_check", "contract.check"),
			SourceRef:                 workflowRunStageMetadataValue(stage, "source_ref", "source.ref"),
			StopReason:                workflowRunStageStopReason(stage),
			ContinuationCount:         workflowRunStageContinuationCount(stage),
			Incomplete:                workflowRunStageIncomplete(stage),
			IsError:                   workflowRunStageIsError(stage),
			NeedsAction:               workflowRunStageNeedsAction(stage),
		})
	}
	for _, artifact := range s.workflowRunArtifactsForQuery(run.Artifacts, withoutLimit) {
		items = append(items, workflowRunTimelineItem{
			Kind:       "artifact",
			Type:       artifact.Kind,
			Stage:      artifact.Stage,
			ToolName:   artifact.ToolName,
			ToolCallID: artifact.ToolCallID,
			ArtifactID: artifact.ID,
			Title:      artifact.Title,
			Summary:    artifact.Summary,
			Content:    workflowRunTimelineContent(artifact.Content, query),
			Reason:     artifact.Metadata["reason"],
			Severity:   artifact.Metadata["severity"],
			SourceRef:  firstWorkflowRunQueryValue(artifact.Metadata["source_ref"], artifact.ArtifactRef),
			IsError:    artifact.IsError,
		})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].At == "" && items[j].At != "" {
			return false
		}
		if items[i].At != "" && items[j].At == "" {
			return true
		}
		if items[i].At == items[j].At {
			return items[i].Kind < items[j].Kind
		}
		return items[i].At < items[j].At
	})
	if query.Limit > 0 && len(items) > query.Limit {
		return append([]workflowRunTimelineItem(nil), items[:query.Limit]...)
	}
	return items
}

func workflowRunEventMatchesQuery(event session.WorkflowRunEventSnapshot, query workflowRunQuery) bool {
	if query.Since > 0 && event.Seq <= query.Since {
		return false
	}
	if query.Stage != "" && !workflowRunStageEqual(event.Stage, query.Stage) {
		return false
	}
	if query.EventType != "" && normalizeWorkflowRunQueryToken(event.Type) != query.EventType {
		return false
	}
	if query.AgentID != "" && !strings.EqualFold(strings.TrimSpace(event.AgentID), query.AgentID) {
		return false
	}
	if query.ToolName != "" && !strings.EqualFold(strings.TrimSpace(event.ToolName), query.ToolName) {
		return false
	}
	if query.ErrorsOnly && !event.IsError {
		return false
	}
	if query.NeedsActionOnly && !event.NeedsAction && !event.PendingApproval && !event.Suspended && !event.Incomplete {
		return false
	}
	if query.SuspendedOnly && !event.Suspended {
		return false
	}
	if query.Query != "" && !workflowRunSearchMatch(query.Query, event.Stage, event.Type, event.Content, event.ToolName, event.ToolCallID, event.TaskStage, event.WorkflowStatus, event.NextStage, event.Reason, event.Severity, event.BudgetScope, event.BudgetMetric, event.ContractCheck, event.SourceRef, event.StopReason) {
		return false
	}
	return true
}

func workflowRunArtifactMatchesQuery(artifact session.WorkflowRunArtifact, query workflowRunQuery) bool {
	if query.Stage != "" && !workflowRunStageEqual(artifact.Stage, query.Stage) {
		return false
	}
	if query.ArtifactKind != "" && normalizeWorkflowRunQueryToken(artifact.Kind) != query.ArtifactKind {
		return false
	}
	if query.ToolName != "" && !strings.EqualFold(strings.TrimSpace(artifact.ToolName), query.ToolName) {
		return false
	}
	if query.ErrorsOnly && !artifact.IsError {
		return false
	}
	if query.NeedsActionOnly || query.SuspendedOnly {
		return false
	}
	if query.Query != "" && !workflowRunSearchMatch(query.Query, artifact.Stage, artifact.Kind, artifact.Title, artifact.Summary, artifact.Content, artifact.ToolName, artifact.ToolCallID) {
		return false
	}
	return true
}

func workflowRunStageMatchesQuery(stage session.WorkflowRunStageSnapshot, query workflowRunQuery) bool {
	if query.Stage != "" && !workflowRunStageEqual(stage.Stage, query.Stage) {
		return false
	}
	if query.Status != "" && normalizeWorkflowRunQueryToken(stage.Status) != query.Status {
		return false
	}
	if query.EventType != "" || query.ArtifactKind != "" || query.SuspendedOnly {
		return false
	}
	if query.NeedsActionOnly && !workflowRunStageNeedsAction(stage) {
		return false
	}
	if query.AgentID != "" && !strings.EqualFold(strings.TrimSpace(stage.AgentID), query.AgentID) {
		return false
	}
	if query.ToolName != "" && !strings.EqualFold(strings.TrimSpace(stage.Tool), query.ToolName) {
		return false
	}
	if query.ErrorsOnly && !workflowRunStageIsError(stage) {
		return false
	}
	if query.Query != "" && !workflowRunSearchMatch(query.Query, stage.Stage, stage.AgentID, stage.NodeType, stage.Skill, stage.Tool, stage.Status, stage.Summary, stage.Result.Output, stage.Result.IncompleteReason, workflowRunStageReason(stage), workflowRunStageBudgetScope(stage), workflowRunStageBudgetMetric(stage), workflowRunStageStopReason(stage), workflowRunStageMetadataValue(stage, "contract_check", "contract.check"), workflowRunStageMetadataValue(stage, "source_ref", "source.ref")) {
		return false
	}
	return true
}

func workflowRunStageIsError(stage session.WorkflowRunStageSnapshot) bool {
	switch normalizeWorkflowRunQueryToken(stage.Status) {
	case "failed", "error", "denied", "blocked", "cancelled", "canceled", "incomplete":
		return true
	}
	if workflowRunStageIncomplete(stage) {
		return true
	}
	for _, result := range stage.Result.ToolResults {
		if result.IsError {
			return true
		}
	}
	return false
}

func workflowRunStageNeedsAction(stage session.WorkflowRunStageSnapshot) bool {
	if workflowRunStageIncomplete(stage) {
		return true
	}
	switch normalizeWorkflowRunQueryToken(stage.Status) {
	case "paused_need_more_budget", "awaiting_budget_approval", "awaiting_input", "awaiting_approval", "awaiting_tool_approval", "blocked":
		return true
	default:
		return false
	}
}

func workflowRunStageIncomplete(stage session.WorkflowRunStageSnapshot) bool {
	if stage.Result.Incomplete {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(stage.Status), "incomplete") {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(stage.Metadata["incomplete"]), "true")
}

func workflowRunStageReason(stage session.WorkflowRunStageSnapshot) string {
	return firstWorkflowRunQueryValue(
		stage.Metadata["reason"],
		stage.Metadata["incomplete_reason"],
		stage.Result.IncompleteReason,
	)
}

func workflowRunStageSeverity(stage session.WorkflowRunStageSnapshot) string {
	severity := firstWorkflowRunQueryValue(stage.Metadata["severity"], stage.Metadata["level"])
	if severity != "" {
		return severity
	}
	if workflowRunStageNeedsAction(stage) {
		return "warning"
	}
	return ""
}

func workflowRunStageBudgetScope(stage session.WorkflowRunStageSnapshot) string {
	if strings.TrimSpace(stage.BudgetScope) != "" {
		return strings.TrimSpace(stage.BudgetScope)
	}
	scope := firstWorkflowRunQueryValue(stage.Metadata["budget_scope"], stage.Metadata["budget.scope"])
	if scope != "" {
		return scope
	}
	switch workflowRunStageStopReason(stage) {
	case schema.StopReasonMaxTokens, schema.StopReasonLength:
		return "output"
	case schema.StopReasonIterationBudget:
		return "iteration"
	default:
		return ""
	}
}

func workflowRunStageBudgetReason(stage session.WorkflowRunStageSnapshot) string {
	if strings.TrimSpace(stage.BudgetReason) != "" {
		return strings.TrimSpace(stage.BudgetReason)
	}
	return workflowRunStageMetadataValue(stage, "budget_reason", "budget.reason")
}

func workflowRunStageBudgetMetric(stage session.WorkflowRunStageSnapshot) string {
	if strings.TrimSpace(stage.BudgetMetric) != "" {
		return strings.TrimSpace(stage.BudgetMetric)
	}
	return workflowRunStageMetadataValue(stage, "budget_metric", "budget.metric")
}

func workflowRunStageBudgetUsed(stage session.WorkflowRunStageSnapshot) int {
	if stage.BudgetUsed > 0 {
		return stage.BudgetUsed
	}
	return workflowRunStageMetadataInt(stage, "budget_used")
}

func workflowRunStageBudgetSoftLimit(stage session.WorkflowRunStageSnapshot) int {
	if stage.BudgetSoftLimit > 0 {
		return stage.BudgetSoftLimit
	}
	return workflowRunStageMetadataInt(stage, "budget_soft_limit")
}

func workflowRunStageBudgetHardLimit(stage session.WorkflowRunStageSnapshot) int {
	if stage.BudgetHardLimit > 0 {
		return stage.BudgetHardLimit
	}
	return workflowRunStageMetadataInt(stage, "budget_hard_limit")
}

func workflowRunStageBudgetRemaining(stage session.WorkflowRunStageSnapshot) int {
	if stage.BudgetRemaining > 0 {
		return stage.BudgetRemaining
	}
	return workflowRunStageMetadataInt(stage, "budget_remaining")
}

func workflowRunStageBudgetTotalTokens(stage session.WorkflowRunStageSnapshot) int {
	if stage.BudgetTotalTokens > 0 {
		return stage.BudgetTotalTokens
	}
	if stage.BudgetPromptTokens+stage.BudgetOutputTokens > 0 {
		return stage.BudgetPromptTokens + stage.BudgetOutputTokens
	}
	return workflowRunStageMetadataInt(stage, "budget_total_tokens")
}

func workflowRunStageBudgetLLMCalls(stage session.WorkflowRunStageSnapshot) int {
	if stage.BudgetLLMCalls > 0 {
		return stage.BudgetLLMCalls
	}
	return workflowRunStageMetadataInt(stage, "budget_llm_calls")
}

func workflowRunStageStopReason(stage session.WorkflowRunStageSnapshot) string {
	return firstWorkflowRunQueryValue(stage.Metadata["stop_reason"], stage.Metadata["stop.reason"], stage.Result.StopReason)
}

func workflowRunStageContinuationCount(stage session.WorkflowRunStageSnapshot) int {
	if stage.Result.ContinuationCount > 0 {
		return stage.Result.ContinuationCount
	}
	for _, key := range []string{"continuation_count", "continuation.count"} {
		value := strings.TrimSpace(stage.Metadata[key])
		if value == "" {
			continue
		}
		count, err := strconv.Atoi(value)
		if err == nil && count > 0 {
			return count
		}
	}
	return 0
}

func workflowRunStageMetadataValue(stage session.WorkflowRunStageSnapshot, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(stage.Metadata[key]); value != "" {
			return value
		}
	}
	return ""
}

func workflowRunStageMetadataInt(stage session.WorkflowRunStageSnapshot, keys ...string) int {
	for _, key := range keys {
		value := strings.TrimSpace(stage.Metadata[key])
		if value == "" {
			continue
		}
		n, err := strconv.Atoi(value)
		if err == nil {
			return n
		}
	}
	return 0
}

func workflowRunTimelineEventSummary(event session.WorkflowRunEventSnapshot) string {
	if strings.TrimSpace(event.Content) != "" {
		return event.Content
	}
	if strings.TrimSpace(event.ArgumentsSummary) != "" {
		return event.ArgumentsSummary
	}
	if strings.TrimSpace(event.TaskStage) != "" {
		return event.TaskStage
	}
	return event.Type
}

func workflowRunTimelineContent(content string, query workflowRunQuery) string {
	if !query.IncludeContent {
		return ""
	}
	return content
}

func workflowRunSearchMatch(query string, values ...string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return true
	}
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
}

func workflowRunStageEqual(left, right string) bool {
	return strings.EqualFold(strings.TrimSpace(left), strings.TrimSpace(right))
}

func workflowRunStatusActive(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running", "cancelling", "paused_need_more_budget", "awaiting_budget_approval":
		return true
	default:
		return false
	}
}

func workflowRunStatusRetryable(status string) bool {
	status = strings.ToLower(strings.TrimSpace(status))
	return !workflowRunStatusActive(status) || status == "paused_need_more_budget" || status == "awaiting_budget_approval"
}

func workflowRunRetryActionReason(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "paused_need_more_budget", "awaiting_budget_approval":
		return "workflow paused because model output or budget was incomplete; retry starts a new run from the saved request"
	default:
		return ""
	}
}

func workflowRunStatusCancellable(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running", "cancelling", "awaiting_approval", "awaiting_tool_approval", "awaiting_input", "awaiting_sub_workflow", "paused_need_more_budget", "awaiting_budget_approval":
		return true
	default:
		return false
	}
}

func workflowRunStatusStreamComplete(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running", "cancelling":
		return false
	default:
		return true
	}
}
