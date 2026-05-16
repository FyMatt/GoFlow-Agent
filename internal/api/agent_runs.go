package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/agent"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func (s *Server) handleAgentRunCollection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	runs := s.sessionSnapshotWithApprovalRisk().AgentRuns
	query := agentRunCollectionQueryFromRequest(r)
	if !agentRunCollectionWantsEnvelope(r, query) {
		writeJSON(w, runs)
		return
	}
	writeJSON(w, agentRunCollectionResponseFor(runs, query))
}

func (s *Server) handleAgentRunItem(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/agent-runs/"), "/"), "/")
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		http.NotFound(w, r)
		return
	}
	run, ok := agentRunByID(s.sessionSnapshotWithApprovalRisk().AgentRuns, parts[0])
	if !ok {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodPost {
		s.handleAgentRunAction(w, r, run, parts)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	switch {
	case len(parts) == 1:
		if workflowRunQueryBool(firstWorkflowRunQueryValue(r.URL.Query().Get("summary"), r.URL.Query().Get("compact"))) {
			writeJSON(w, summarizeAgentRunForAPI(run))
			return
		}
		writeJSON(w, s.runtime.HydrateAgentRun(run))
	case len(parts) == 2 && parts[1] == "events":
		writeJSON(w, s.agentRunEventsForQuery(run.Events, agentRunQueryFromRequest(r)))
	case len(parts) == 2 && parts[1] == "timeline":
		writeJSON(w, s.agentRunTimeline(run, agentRunQueryFromRequest(r)))
	case len(parts) == 2 && parts[1] == "context":
		writeJSON(w, agentRunContext(run))
	case len(parts) == 2 && parts[1] == "artifacts":
		writeJSON(w, s.agentRunArtifactsResponse(run, workflowRunArtifactQueryFromRequest(r)))
	case len(parts) == 3 && parts[1] == "events" && parts[2] == "stream":
		s.handleAgentRunEventsStream(w, r, run.ID)
	case len(parts) == 2 && parts[1] == "replay":
		writeJSON(w, s.agentRunReplay(run, agentRunQueryFromRequest(r)))
	case len(parts) == 2 && parts[1] == "diffs":
		writeJSON(w, s.agentRunDiffResponse(run, agentRunQueryFromRequest(r)))
	case len(parts) == 2 && parts[1] == "export":
		s.handleAgentRunExport(w, r, run)
	case len(parts) == 2 && parts[1] == "actions":
		actions := s.agentRunActions(run)
		if runActionsWantsEnvelope(r) {
			writeJSON(w, agentRunActionDiscoveryFor(run, actions))
			return
		}
		writeJSON(w, actions)
	default:
		http.NotFound(w, r)
	}
}

type agentRunBackgroundResponse struct {
	RunID       string                    `json:"run_id"`
	Status      string                    `json:"status,omitempty"`
	Action      string                    `json:"action,omitempty"`
	RetryOf     string                    `json:"retry_of,omitempty"`
	EventsURL   string                    `json:"events_url,omitempty"`
	TimelineURL string                    `json:"timeline_url,omitempty"`
	ReplayURL   string                    `json:"replay_url,omitempty"`
	ActionsURL  string                    `json:"actions_url,omitempty"`
	DiffsURL    string                    `json:"diffs_url,omitempty"`
	CancelURL   string                    `json:"cancel_url,omitempty"`
	Run         *session.AgentRunSnapshot `json:"run,omitempty"`
}

type agentRunReplay struct {
	Run      session.AgentRunSnapshot        `json:"run"`
	Events   []session.AgentRunEventSnapshot `json:"events,omitempty"`
	Timeline []agentRunTimelineItem          `json:"timeline,omitempty"`
	Diffs    []runDiffItem                   `json:"diffs,omitempty"`
	Actions  []agentRunAction                `json:"actions,omitempty"`
	Filters  agentRunQuery                   `json:"filters,omitempty"`
}

type agentRunTimelineItem struct {
	At          string `json:"at,omitempty"`
	Kind        string `json:"kind"`
	Type        string `json:"type,omitempty"`
	Status      string `json:"status,omitempty"`
	AgentID     string `json:"agent_id,omitempty"`
	Mode        string `json:"mode,omitempty"`
	ToolName    string `json:"tool_name,omitempty"`
	ToolCallID  string `json:"tool_call_id,omitempty"`
	Title       string `json:"title,omitempty"`
	Summary     string `json:"summary,omitempty"`
	Content     string `json:"content,omitempty"`
	IsError     bool   `json:"is_error,omitempty"`
	NeedsAction bool   `json:"needs_action,omitempty"`
	Suspended   bool   `json:"suspended,omitempty"`
}

type agentRunAction struct {
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

type runActionDiscoveryMeta struct {
	Schema                    string `json:"schema"`
	SchemaVersion             int    `json:"schema_version"`
	MinSupportedSchemaVersion int    `json:"min_supported_schema_version"`
}

type runActionCounts struct {
	Total       int `json:"total"`
	Available   int `json:"available"`
	Unavailable int `json:"unavailable"`
	Destructive int `json:"destructive"`
	NeedsBody   int `json:"needs_body"`
	WithRisk    int `json:"with_risk"`
}

type agentRunActionDiscovery struct {
	Meta          runActionDiscoveryMeta `json:"meta"`
	RunID         string                 `json:"run_id"`
	Status        string                 `json:"status,omitempty"`
	NeedsAction   bool                   `json:"needs_action"`
	EventsPath    string                 `json:"events_path,omitempty"`
	TimelinePath  string                 `json:"timeline_path,omitempty"`
	ReplayPath    string                 `json:"replay_path,omitempty"`
	ActionsPath   string                 `json:"actions_path,omitempty"`
	Recommended   string                 `json:"recommended_action,omitempty"`
	Counts        runActionCounts        `json:"counts"`
	Actions       []agentRunAction       `json:"actions"`
	SupportsRetry bool                   `json:"supports_retry"`
}

type agentRunActionRequest struct {
	Background bool `json:"background,omitempty"`
}

type agentRunQuery struct {
	EventType       string `json:"event_type,omitempty"`
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

type agentRunCollectionResponse struct {
	Runs    []session.AgentRunSnapshot  `json:"runs"`
	Filters agentRunCollectionQuery     `json:"filters,omitempty"`
	Counts  agentRunCollectionRunCounts `json:"counts"`
}

type agentRunCollectionQuery struct {
	AgentID         string `json:"agent_id,omitempty"`
	Mode            string `json:"mode,omitempty"`
	Status          string `json:"status,omitempty"`
	ToolName        string `json:"tool_name,omitempty"`
	Query           string `json:"query,omitempty"`
	RetryOf         string `json:"retry_of,omitempty"`
	Limit           int    `json:"limit,omitempty"`
	ActiveOnly      bool   `json:"active_only,omitempty"`
	TerminalOnly    bool   `json:"terminal_only,omitempty"`
	NeedsActionOnly bool   `json:"needs_action_only,omitempty"`
	ErrorsOnly      bool   `json:"errors_only,omitempty"`
	Summary         bool   `json:"summary,omitempty"`
}

type agentRunCollectionRunCounts struct {
	Total       int `json:"total"`
	Matched     int `json:"matched"`
	Returned    int `json:"returned"`
	Active      int `json:"active"`
	Terminal    int `json:"terminal"`
	NeedsAction int `json:"needs_action"`
	Errors      int `json:"errors"`
}

func (s *Server) agentRunReplay(run session.AgentRunSnapshot, query agentRunQuery) agentRunReplay {
	if query.IncludeContent {
		run = s.runtime.HydrateAgentRun(run)
	}
	return agentRunReplay{
		Run:      run,
		Events:   s.agentRunEventsForQuery(run.Events, query),
		Timeline: s.agentRunTimeline(run, query),
		Diffs:    s.agentRunDiffs(run, query),
		Actions:  s.agentRunActions(run),
		Filters:  query,
	}
}

func agentRunCollectionResponseFor(runs []session.AgentRunSnapshot, query agentRunCollectionQuery) agentRunCollectionResponse {
	counts := agentRunCollectionRunCounts{Total: len(runs)}
	matched := make([]session.AgentRunSnapshot, 0, len(runs))
	for _, run := range runs {
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
		if !agentRunCollectionRunMatches(run, query) {
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
		matched = summarizeAgentRunsForAPI(matched)
	}
	return agentRunCollectionResponse{Runs: matched, Filters: query, Counts: counts}
}

func agentRunCollectionQueryFromRequest(r *http.Request) agentRunCollectionQuery {
	if r == nil || r.URL == nil {
		return agentRunCollectionQuery{}
	}
	values := r.URL.Query()
	limit, _ := strconv.Atoi(strings.TrimSpace(values.Get("limit")))
	if limit < 0 {
		limit = 0
	}
	if limit > 500 {
		limit = 500
	}
	return agentRunCollectionQuery{
		AgentID:         strings.TrimSpace(firstWorkflowRunQueryValue(values.Get("agent_id"), values.Get("agent"))),
		Mode:            strings.TrimSpace(values.Get("mode")),
		Status:          normalizeWorkflowRunQueryToken(values.Get("status")),
		ToolName:        strings.TrimSpace(firstWorkflowRunQueryValue(values.Get("tool_name"), values.Get("tool"))),
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

func agentRunCollectionWantsEnvelope(r *http.Request, query agentRunCollectionQuery) bool {
	if r == nil || r.URL == nil {
		return false
	}
	values := r.URL.Query()
	if workflowRunQueryBool(firstWorkflowRunQueryValue(values.Get("envelope"), values.Get("summary"), values.Get("counts"))) {
		return true
	}
	query.Summary = false
	return query != (agentRunCollectionQuery{})
}

func agentRunCollectionRunMatches(run session.AgentRunSnapshot, query agentRunCollectionQuery) bool {
	if query.AgentID != "" && !strings.EqualFold(strings.TrimSpace(run.AgentID), query.AgentID) {
		return false
	}
	if query.Mode != "" && !strings.EqualFold(strings.TrimSpace(run.Mode), query.Mode) {
		return false
	}
	if query.Status != "" && normalizeWorkflowRunQueryToken(run.Status) != query.Status {
		return false
	}
	if query.ToolName != "" && !agentRunCollectionRunHasTool(run, query.ToolName) {
		return false
	}
	if query.RetryOf != "" && !strings.EqualFold(strings.TrimSpace(run.RetryOf), query.RetryOf) {
		return false
	}
	if query.ActiveOnly && !agentRunCollectionRunActive(run) {
		return false
	}
	if query.TerminalOnly && !agentRunCollectionRunTerminal(run) {
		return false
	}
	if query.NeedsActionOnly && !agentRunCollectionRunNeedsAction(run) {
		return false
	}
	if query.ErrorsOnly && !agentRunCollectionRunHasError(run) {
		return false
	}
	if query.Query != "" && !agentRunCollectionRunSearchMatch(run, query.Query) {
		return false
	}
	return true
}

func agentRunCollectionRunActive(run session.AgentRunSnapshot) bool {
	switch normalizeWorkflowRunQueryToken(run.Status) {
	case "running", "cancelling", "awaiting_tool_approval":
		return true
	default:
		return false
	}
}

func agentRunCollectionRunTerminal(run session.AgentRunSnapshot) bool {
	switch normalizeWorkflowRunQueryToken(run.Status) {
	case "completed", "failed", "cancelled", "canceled", "denied":
		return true
	default:
		return false
	}
}

func agentRunCollectionRunNeedsAction(run session.AgentRunSnapshot) bool {
	if strings.EqualFold(strings.TrimSpace(run.Status), "awaiting_tool_approval") {
		return true
	}
	if strings.TrimSpace(run.PendingCallID) != "" || len(run.PendingApprovals) > 0 {
		return true
	}
	for _, event := range run.Events {
		if event.NeedsAction || event.Suspended {
			return true
		}
	}
	return false
}

func agentRunCollectionRunHasError(run session.AgentRunSnapshot) bool {
	switch normalizeWorkflowRunQueryToken(run.Status) {
	case "failed", "denied":
		return true
	}
	if strings.TrimSpace(run.Error) != "" {
		return true
	}
	for _, event := range run.Events {
		if event.IsError {
			return true
		}
	}
	if run.Result != nil {
		for _, result := range run.Result.ToolResults {
			if result.IsError || result.Denied {
				return true
			}
		}
	}
	return false
}

func agentRunCollectionRunHasTool(run session.AgentRunSnapshot, toolName string) bool {
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(run.PendingTool), toolName) {
		return true
	}
	for _, event := range run.Events {
		if strings.EqualFold(strings.TrimSpace(event.ToolName), toolName) {
			return true
		}
	}
	if run.Result != nil {
		for _, result := range run.Result.ToolResults {
			if strings.EqualFold(strings.TrimSpace(result.ToolName), toolName) {
				return true
			}
		}
	}
	return false
}

func agentRunCollectionRunSearchMatch(run session.AgentRunSnapshot, query string) bool {
	if workflowRunSearchMatch(query, run.ID, run.Status, run.Request, run.Output, run.Error, run.AgentID, run.Mode, run.PendingTool, run.PendingCallID) {
		return true
	}
	for _, event := range run.Events {
		if agentRunEventContains(event, query) {
			return true
		}
	}
	if run.Result != nil {
		if workflowRunSearchMatch(query, run.Result.Output, run.Result.AgentID, run.Result.Mode) {
			return true
		}
		for _, result := range run.Result.ToolResults {
			if workflowRunSearchMatch(query, result.ToolName, result.CallID, result.Content) {
				return true
			}
		}
		if run.Result.MatchedSkill != nil && workflowRunSearchMatch(query, run.Result.MatchedSkill.Name, run.Result.MatchedSkill.Description) {
			return true
		}
	}
	return false
}

func (s *Server) handleAgentRunExport(w http.ResponseWriter, r *http.Request, run session.AgentRunSnapshot) {
	query := agentRunQueryFromRequest(r)
	replay := s.agentRunReplay(run, query)
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
		w.Header().Set("Content-Disposition", agentRunExportDisposition(run, "json"))
		_, _ = w.Write(append(data, '\n'))
	case "md", "markdown":
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Header().Set("Content-Disposition", agentRunExportDisposition(run, "md"))
		_, _ = w.Write([]byte(renderAgentRunMarkdownExport(replay, query)))
	default:
		http.Error(w, "unsupported agent run export format: "+format, http.StatusBadRequest)
	}
}

func agentRunExportDisposition(run session.AgentRunSnapshot, ext string) string {
	name := normalizeWorkflowRunExportName(firstWorkflowRunQueryValue(run.AgentID, "agent-run"))
	if name == "" {
		name = "agent-run"
	}
	id := normalizeWorkflowRunExportName(run.ID)
	if id != "" {
		name += "-" + id
	}
	return fmt.Sprintf(`attachment; filename="%s.%s"`, name, ext)
}

func renderAgentRunMarkdownExport(replay agentRunReplay, query agentRunQuery) string {
	run := replay.Run
	var builder strings.Builder
	builder.WriteString("# Agent Run Report\n\n")
	workflowRunMarkdownKV(&builder, "Run ID", run.ID)
	workflowRunMarkdownKV(&builder, "Status", run.Status)
	workflowRunMarkdownKV(&builder, "Agent", run.AgentID)
	workflowRunMarkdownKV(&builder, "Mode", run.Mode)
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
	if strings.TrimSpace(run.Output) != "" {
		builder.WriteString("\n## Output\n\n")
		builder.WriteString(workflowRunMarkdownCode(run.Output, "text"))
	}
	if strings.TrimSpace(run.Error) != "" {
		builder.WriteString("\n## Error\n\n")
		builder.WriteString(workflowRunMarkdownCode(run.Error, "text"))
	}
	if prompt, output, cached := agentRunTokenTotals(run); prompt > 0 || output > 0 || cached > 0 {
		builder.WriteString("\n## Token Usage\n\n")
		workflowRunMarkdownKV(&builder, "Prompt Tokens", strconv.Itoa(prompt))
		workflowRunMarkdownKV(&builder, "Output Tokens", strconv.Itoa(output))
		workflowRunMarkdownKV(&builder, "Cached Tokens", strconv.Itoa(cached))
		workflowRunMarkdownKV(&builder, "Total Tokens", strconv.Itoa(prompt+output))
	}
	if run.Result != nil {
		if run.Result.MatchedSkill != nil && strings.TrimSpace(run.Result.MatchedSkill.Name) != "" {
			builder.WriteString("\n## Matched Skill\n\n")
			workflowRunMarkdownKV(&builder, "Name", run.Result.MatchedSkill.Name)
			workflowRunMarkdownKV(&builder, "Description", run.Result.MatchedSkill.Description)
		}
		if len(run.Result.ToolResults) > 0 {
			builder.WriteString("\n## Tool Results\n\n")
			for _, result := range run.Result.ToolResults {
				builder.WriteString("### ")
				builder.WriteString(workflowRunMarkdownInline(firstWorkflowRunQueryValue(result.ToolName, "tool")))
				builder.WriteString("\n\n")
				workflowRunMarkdownKV(&builder, "Call ID", result.CallID)
				workflowRunMarkdownKV(&builder, "Error", strconv.FormatBool(result.IsError))
				if result.Denied {
					workflowRunMarkdownKV(&builder, "Denied", "true")
				}
				if result.Suspended {
					workflowRunMarkdownKV(&builder, "Suspended", "true")
				}
				if query.IncludeContent && strings.TrimSpace(result.Content) != "" {
					builder.WriteString("\n")
					builder.WriteString(workflowRunMarkdownCode(result.Content, "text"))
				}
				builder.WriteString("\n")
			}
		}
		agentRunMarkdownJSONSection(&builder, "Structured Sections", run.Result.Structured)
		agentRunMarkdownJSONSection(&builder, "Findings", run.Result.Findings)
		agentRunMarkdownJSONSection(&builder, "Changes", run.Result.Changes)
		agentRunMarkdownJSONSection(&builder, "Verification", run.Result.Verification)
		agentRunMarkdownJSONSection(&builder, "Audit Trail", run.Result.AuditTrail)
	}
	if len(replay.Events) > 0 {
		builder.WriteString("\n## Events\n\n")
		for _, event := range replay.Events {
			title := firstWorkflowRunQueryValue(event.Type, "event")
			builder.WriteString("### ")
			builder.WriteString(workflowRunMarkdownInline(title))
			if event.Seq > 0 {
				builder.WriteString(" #")
				builder.WriteString(strconv.Itoa(event.Seq))
			}
			builder.WriteString("\n\n")
			workflowRunMarkdownKV(&builder, "At", event.At)
			workflowRunMarkdownKV(&builder, "Agent", event.AgentID)
			workflowRunMarkdownKV(&builder, "Mode", event.Mode)
			workflowRunMarkdownKV(&builder, "Tool", event.ToolName)
			workflowRunMarkdownKV(&builder, "Tool Call", event.ToolCallID)
			if event.IsError {
				workflowRunMarkdownKV(&builder, "Error", "true")
			}
			if event.NeedsAction || event.Suspended {
				workflowRunMarkdownKV(&builder, "Needs Action", "true")
			}
			content := strings.TrimSpace(firstWorkflowRunQueryValue(event.Content, event.ArgumentsSummary, event.TaskStage))
			if content != "" && query.IncludeContent {
				builder.WriteString("\n")
				builder.WriteString(workflowRunMarkdownCode(content, "text"))
			}
			builder.WriteString("\n")
		}
	}
	if len(replay.Timeline) > 0 {
		builder.WriteString("\n## Timeline\n\n")
		for _, item := range replay.Timeline {
			builder.WriteString("- ")
			builder.WriteString(workflowRunMarkdownInline(firstWorkflowRunQueryValue(item.At, "unknown time")))
			builder.WriteString(" ")
			builder.WriteString(workflowRunMarkdownInline(firstWorkflowRunQueryValue(item.Kind, "item")))
			if strings.TrimSpace(item.Type) != "" {
				builder.WriteString("/")
				builder.WriteString(workflowRunMarkdownInline(item.Type))
			}
			if strings.TrimSpace(item.Summary) != "" {
				builder.WriteString(": ")
				builder.WriteString(workflowRunMarkdownInline(item.Summary))
			}
			builder.WriteString("\n")
		}
	}
	if query != (agentRunQuery{}) {
		builder.WriteString("\n## Filters\n\n")
		if data, err := json.MarshalIndent(query, "", "  "); err == nil {
			builder.WriteString(workflowRunMarkdownCode(string(data), "json"))
		}
	}
	return builder.String()
}

func agentRunTokenTotals(run session.AgentRunSnapshot) (int, int, int) {
	var prompt, output, cached int
	for _, event := range run.Events {
		prompt += event.PromptTokens
		output += event.OutputTokens
		cached += event.CachedTokens
	}
	if prompt == 0 && output == 0 && cached == 0 && run.Result != nil {
		for _, audit := range run.Result.AuditTrail {
			prompt += audit.PromptTokens
			output += audit.OutputTokens
			cached += audit.CachedTokens
		}
	}
	return prompt, output, cached
}

func agentRunMarkdownJSONSection(builder *strings.Builder, title string, value any) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil || string(data) == "null" || string(data) == "[]" || string(data) == "{}" {
		return
	}
	builder.WriteString("\n## ")
	builder.WriteString(workflowRunMarkdownInline(title))
	builder.WriteString("\n\n")
	builder.WriteString(workflowRunMarkdownCode(string(data), "json"))
}

func (s *Server) agentRunActions(run session.AgentRunSnapshot) []agentRunAction {
	id := strings.TrimSpace(run.ID)
	if id == "" {
		return nil
	}
	eventsPath := agentRunEventsStreamPath(id)
	pendingRisk := s.agentRunPendingApprovalRisk(run)
	actions := make([]agentRunAction, 0, 5)
	status := strings.ToLower(strings.TrimSpace(run.Status))
	if agentRunStatusCancellable(status) {
		actions = append(actions, agentRunAction{
			Name:        "cancel",
			Label:       "Cancel",
			Method:      http.MethodPost,
			Path:        "/api/agent-runs/" + id + "/cancel",
			EventsPath:  eventsPath,
			Available:   true,
			Durable:     true,
			Background:  true,
			Destructive: true,
		})
	}
	if strings.EqualFold(status, "awaiting_tool_approval") {
		callID := s.agentRunPendingApprovalCallID(run)
		resumable := s.agentRunToolApprovalResumable(callID)
		resumeReason := agentRunToolApprovalActionReason(resumable)
		approveAvailable := resumable
		approveReason := resumeReason
		if approveAvailable {
			approveAvailable, approveReason = s.agentRunToolApprovalAllowed(callID, approveReason)
		}
		rememberAvailable, rememberReason := s.agentRunRememberToolApprovalAvailable(callID, approveAvailable, approveReason)
		if len(run.PendingApprovals) > 1 {
			allResumable := s.agentRunToolApprovalsResumable(run)
			allResumeReason := agentRunToolApprovalActionReason(allResumable)
			approveAllAvailable := allResumable
			approveAllReason := allResumeReason
			if approveAllAvailable {
				approveAllAvailable, approveAllReason = s.agentRunToolApprovalsAllowed(run, approveAllReason)
			}
			rememberAllAvailable, rememberAllReason := s.agentRunRememberToolApprovalsAvailable(run, approveAllAvailable, approveAllReason)
			for _, item := range []struct {
				name      string
				label     string
				available bool
				reason    string
			}{
				{name: "approve_all_tools", label: "Approve all tools", available: approveAllAvailable, reason: approveAllReason},
				{name: "approve_remember_all_tools", label: "Approve and remember all tools", available: rememberAllAvailable, reason: rememberAllReason},
				{name: "deny_all_tools", label: "Deny all tools", available: allResumable, reason: allResumeReason},
			} {
				actions = append(actions, agentRunAction{
					Name:       item.name,
					Label:      item.label,
					Method:     http.MethodPost,
					Path:       agentRunActionPath(id, item.name),
					StreamPath: agentRunActionStreamPath(id, item.name),
					EventsPath: eventsPath,
					Available:  item.available,
					Durable:    item.available,
					Background: true,
					Reason:     item.reason,
					Risk:       pendingRisk,
				})
			}
		}
		for _, item := range []struct {
			name      string
			label     string
			available bool
			reason    string
		}{
			{name: "approve_tool", label: "Approve tool", available: approveAvailable, reason: approveReason},
			{name: "approve_remember_tool", label: "Approve and remember tool", available: rememberAvailable, reason: rememberReason},
			{name: "deny_tool", label: "Deny tool", available: resumable, reason: resumeReason},
		} {
			actions = append(actions, agentRunAction{
				Name:       item.name,
				Label:      item.label,
				Method:     http.MethodPost,
				Path:       agentRunActionPath(id, item.name),
				StreamPath: agentRunActionStreamPath(id, item.name),
				EventsPath: eventsPath,
				Available:  item.available,
				Durable:    item.available,
				Background: true,
				Reason:     item.reason,
				Risk:       pendingRisk,
			})
		}
	}
	if !agentRunStatusActive(status) && strings.TrimSpace(run.Request) != "" {
		actions = append(actions, agentRunAction{
			Name:       "retry",
			Label:      "Retry",
			Method:     http.MethodPost,
			Path:       "/api/agent-runs/" + id + "/retry",
			EventsPath: eventsPath,
			Available:  true,
			Durable:    true,
			Background: true,
		})
	}
	return normalizeAgentRunActions(actions)
}

func normalizeAgentRunActions(actions []agentRunAction) []agentRunAction {
	if len(actions) == 0 {
		return actions
	}
	out := make([]agentRunAction, len(actions))
	for i, action := range actions {
		out[i] = action
		out[i].Kind = agentRunActionKind(action.Name)
		out[i].SupportsBackground = action.Background || strings.TrimSpace(action.EventsPath) != ""
		out[i].SupportsStream = strings.TrimSpace(action.StreamPath) != ""
		out[i].AcceptsBody = agentRunActionAcceptsBody(action.Name)
		out[i].RequiresBody = false
		if out[i].AcceptsBody {
			out[i].BodySchema = agentRunActionBodySchema(action.Name)
		}
		if out[i].Risk != nil {
			risk := copySchemaToolRiskProfile(*out[i].Risk)
			out[i].Risk = &risk
		}
	}
	return out
}

func agentRunActionKind(name string) string {
	switch strings.TrimSpace(name) {
	case "cancel":
		return "lifecycle"
	case "retry":
		return "retry"
	case "approve_tool", "approve_remember_tool", "approve_all_tools", "approve_remember_all_tools", "deny_tool", "deny_all_tools":
		return "tool_approval"
	default:
		return "action"
	}
}

func agentRunActionAcceptsBody(name string) bool {
	switch strings.TrimSpace(name) {
	case "retry", "approve_tool", "approve_remember_tool", "approve_all_tools", "approve_remember_all_tools", "deny_tool", "deny_all_tools":
		return true
	default:
		return false
	}
}

func agentRunActionBodySchema(name string) map[string]any {
	schema := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"background": map[string]any{
				"type":        "boolean",
				"description": "Run the action in the background and reconnect through events_path.",
				"default":     true,
			},
		},
	}
	return schema
}

func runActionsWantsEnvelope(r *http.Request) bool {
	if r == nil || r.URL == nil {
		return false
	}
	values := r.URL.Query()
	return workflowRunQueryBool(values.Get("envelope")) ||
		workflowRunQueryBool(values.Get("meta")) ||
		workflowRunQueryBool(values.Get("metadata"))
}

func runActionDiscoveryMetaForActions() runActionDiscoveryMeta {
	return runActionDiscoveryMeta{
		Schema:                    "goflow.run_actions",
		SchemaVersion:             1,
		MinSupportedSchemaVersion: 1,
	}
}

func agentRunActionDiscoveryFor(run session.AgentRunSnapshot, actions []agentRunAction) agentRunActionDiscovery {
	id := strings.TrimSpace(run.ID)
	return agentRunActionDiscovery{
		Meta:          runActionDiscoveryMetaForActions(),
		RunID:         id,
		Status:        strings.TrimSpace(run.Status),
		NeedsAction:   agentRunCollectionRunNeedsAction(run),
		EventsPath:    agentRunEventsStreamPath(id),
		TimelinePath:  "/api/agent-runs/" + id + "/timeline",
		ReplayPath:    "/api/agent-runs/" + id + "/replay",
		ActionsPath:   "/api/agent-runs/" + id + "/actions",
		Recommended:   recommendedAgentRunAction(actions),
		Counts:        agentRunActionCounts(actions),
		Actions:       actions,
		SupportsRetry: agentRunActionExists(actions, "retry"),
	}
}

func agentRunActionExists(actions []agentRunAction, name string) bool {
	for _, action := range actions {
		if action.Name == name {
			return true
		}
	}
	return false
}

func agentRunActionCounts(actions []agentRunAction) runActionCounts {
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

func recommendedAgentRunAction(actions []agentRunAction) string {
	for _, name := range []string{"approve_tool", "approve_all_tools", "deny_tool", "retry", "cancel"} {
		for _, action := range actions {
			if action.Name == name && action.Available {
				return name
			}
		}
	}
	return ""
}

func (s *Server) agentRunPendingApprovalRisk(run session.AgentRunSnapshot) *schema.ToolRiskProfile {
	for _, pending := range run.PendingApprovals {
		if pending.Risk != nil {
			risk := copySchemaToolRiskProfile(*pending.Risk)
			return &risk
		}
		if strings.TrimSpace(pending.ToolName) != "" {
			return s.toolRiskProfileForName(pending.ToolName)
		}
	}
	if strings.TrimSpace(run.PendingTool) != "" {
		return s.toolRiskProfileForName(run.PendingTool)
	}
	return nil
}

func (s *Server) handleAgentRunAction(w http.ResponseWriter, r *http.Request, run session.AgentRunSnapshot, parts []string) {
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	action := normalizeAgentRunActionPath(parts[1])
	stream := len(parts) == 3 && parts[2] == "stream"
	switch action {
	case "cancel":
		s.handleAgentRunCancel(w, run)
	case "retry":
		s.handleAgentRunRetry(w, r, run)
	case "approve_tool", "approve_remember_tool", "deny_tool", "approve_all_tools", "approve_remember_all_tools", "deny_all_tools":
		s.handleAgentRunToolApproval(w, r, run, action, stream)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleAgentRunCancel(w http.ResponseWriter, run session.AgentRunSnapshot) {
	if !agentRunStatusCancellable(run.Status) {
		http.Error(w, "agent run is not active or paused", http.StatusConflict)
		return
	}
	if cancel, ok := s.agentRunCancelFunc(run.ID); ok {
		updated, _ := s.runtime.RequestAgentRunCancel(run.ID, "agent run cancellation requested")
		cancel()
		writeJSON(w, updated)
		return
	}
	updated, ok := s.runtime.CancelAgentRunAndApprovals(run.ID, "agent run cancelled while paused")
	if !ok {
		http.Error(w, "agent run not found", http.StatusNotFound)
		return
	}
	writeJSON(w, updated)
}

func (s *Server) handleAgentRunRetry(w http.ResponseWriter, r *http.Request, run session.AgentRunSnapshot) {
	if strings.TrimSpace(run.Request) == "" {
		http.Error(w, "agent run cannot be retried without request", http.StatusBadRequest)
		return
	}
	if agentRunStatusActive(run.Status) {
		http.Error(w, "agent run is still active", http.StatusConflict)
		return
	}
	if !s.ensureWorkspaceConfirmedForRun(w, run.Request) {
		return
	}
	var req agentRunActionRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	accepted, err := s.startAgentBackgroundWithRetry(run.Request, run.ID, "retry")
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	_ = req
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(accepted)
}

func (s *Server) handleAgentRunToolApproval(w http.ResponseWriter, r *http.Request, run session.AgentRunSnapshot, action string, stream bool) {
	if !strings.EqualFold(strings.TrimSpace(run.Status), "awaiting_tool_approval") {
		http.Error(w, "agent run is not awaiting tool approval", http.StatusConflict)
		return
	}
	callID := s.agentRunPendingApprovalCallID(run)
	if callID == "" {
		http.Error(w, "agent run has no pending tool approval call id", http.StatusConflict)
		return
	}
	if agentRunToolApprovalAllAction(action) {
		if !s.agentRunToolApprovalsResumable(run) {
			http.Error(w, agentRunToolApprovalActionReason(false), http.StatusConflict)
			return
		}
	} else if !s.agentRunToolApprovalResumable(callID) {
		http.Error(w, agentRunToolApprovalActionReason(false), http.StatusConflict)
		return
	}
	if stream {
		writer, ok := newSSEWriter(w)
		if !ok {
			return
		}
		if err := s.runAgentRunToolApprovalAction(r.Context(), run.ID, callID, action, writer.write); err != nil {
			_ = writer.write(schema.StreamEvent{Type: schema.StreamEventError, Content: err.Error(), IsError: true})
		}
		return
	}
	var req agentRunActionRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	if req.Background {
		accepted, err := s.startAgentRunBackgroundAction(run.ID, action, func(ctx context.Context) error {
			return s.runAgentRunToolApprovalActionLocked(ctx, run.ID, callID, action, nil)
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
	if err := s.runAgentRunToolApprovalAction(r.Context(), run.ID, callID, action, nil); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if updated, ok := s.runtime.AgentRun(run.ID); ok {
		writeJSON(w, s.agentRunWithApprovalRisk(updated))
		return
	}
	http.NotFound(w, r)
}

func (s *Server) runAgentRunToolApprovalAction(ctx context.Context, runID, callID, action string, handler func(schema.StreamEvent) error) error {
	s.agentExecMu.Lock()
	defer s.agentExecMu.Unlock()
	return s.runAgentRunToolApprovalActionLocked(ctx, runID, callID, action, handler)
}

func (s *Server) runAgentRunToolApprovalActionLocked(ctx context.Context, runID, callID, action string, handler func(schema.StreamEvent) error) error {
	ctx = agent.WithAgentRunID(ctx, runID)
	record := s.recordAgentRunEvents(runID, handler)
	if agentRunToolApprovalAllAction(action) {
		return s.runAgentRunToolApprovalAllAction(ctx, runID, action, record)
	}
	if !s.agentRunToolApprovalResumable(callID) {
		return fmt.Errorf("%s", agentRunToolApprovalActionReason(false))
	}
	var (
		result schema.ToolResult
		err    error
	)
	switch action {
	case "approve_tool":
		result, err = s.runtime.ApproveToolCall(ctx, callID)
	case "approve_remember_tool":
		result, err = s.runtime.ApproveToolCallAndRemember(ctx, callID)
	case "deny_tool":
		result, err = s.runtime.DenyToolCall(callID)
	default:
		return fmt.Errorf("unknown agent run tool approval action")
	}
	if err != nil {
		s.runtime.FailAgentRun(runID, err.Error())
		return err
	}
	if err := record(schema.StreamEvent{Type: schema.StreamEventToolResult, ToolName: result.ToolName, ToolCallID: result.CallID, Content: result.Content, IsError: result.IsError, Suspended: result.Suspended}); err != nil {
		return err
	}
	resumeResult, resumed, err := s.runtime.ResumeApprovedOrdinaryToolCall(ctx, callID, record)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			s.runtime.CancelAgentRun(runID, "agent run cancelled")
			return err
		}
		s.runtime.FailAgentRun(runID, err.Error())
		return err
	}
	if resumed {
		_ = record(schema.StreamEvent{Type: schema.StreamEventFinalMessage, Content: resumeResult.Output, AgentID: resumeResult.AgentID, Mode: resumeResult.Mode})
		s.runtime.CompleteAgentRun(runID, agentRunStatusForResult(resumeResult), resumeResult)
	}
	return nil
}

func (s *Server) runAgentRunToolApprovalAllAction(ctx context.Context, runID, action string, record func(schema.StreamEvent) error) error {
	run, ok := s.runtime.AgentRun(runID)
	if !ok {
		return fmt.Errorf("agent run not found: %s", runID)
	}
	if len(run.PendingApprovals) == 0 && strings.TrimSpace(run.PendingCallID) != "" {
		run.PendingApprovals = []session.PendingApprovalSnapshot{{CallID: run.PendingCallID, ToolName: run.PendingTool, AgentID: run.PendingAgentID, AgentRunID: runID}}
	}
	if len(run.PendingApprovals) == 0 {
		return fmt.Errorf("agent run has no pending tool approvals")
	}
	callIDs := s.agentRunPendingApprovalCallIDs(run)
	if !s.runtime.OrdinaryToolApprovalsResumable(callIDs) {
		return fmt.Errorf("%s", agentRunToolApprovalActionReason(false))
	}
	var lastCallID string
	for _, pending := range run.PendingApprovals {
		callID := strings.TrimSpace(pending.CallID)
		if callID == "" {
			continue
		}
		lastCallID = callID
		var (
			result schema.ToolResult
			err    error
		)
		switch action {
		case "approve_all_tools":
			result, err = s.runtime.ApproveToolCall(ctx, callID)
		case "approve_remember_all_tools":
			result, err = s.runtime.ApproveToolCallAndRemember(ctx, callID)
		case "deny_all_tools":
			result, err = s.runtime.DenyToolCall(callID)
		default:
			return fmt.Errorf("unknown agent run tool approval action")
		}
		if err != nil {
			s.runtime.FailAgentRun(runID, err.Error())
			return err
		}
		if err := record(schema.StreamEvent{Type: schema.StreamEventToolResult, ToolName: result.ToolName, ToolCallID: result.CallID, Content: result.Content, IsError: result.IsError, Suspended: result.Suspended}); err != nil {
			return err
		}
	}
	if strings.TrimSpace(lastCallID) == "" {
		return fmt.Errorf("agent run has no pending tool approval call id")
	}
	resumeResult, resumed, err := s.runtime.ResumeApprovedOrdinaryToolCall(ctx, lastCallID, record)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			s.runtime.CancelAgentRun(runID, "agent run cancelled")
			return err
		}
		s.runtime.FailAgentRun(runID, err.Error())
		return err
	}
	if resumed {
		_ = record(schema.StreamEvent{Type: schema.StreamEventFinalMessage, Content: resumeResult.Output, AgentID: resumeResult.AgentID, Mode: resumeResult.Mode})
		s.runtime.CompleteAgentRun(runID, agentRunStatusForResult(resumeResult), resumeResult)
	}
	return nil
}

func (s *Server) handleAgentRunEventsStream(w http.ResponseWriter, r *http.Request, runID string) {
	writer, ok := newSSEWriter(w)
	if !ok {
		return
	}
	if err := writer.writeRetry(durableRunSSERetryMillis); err != nil {
		return
	}
	query := agentRunQueryFromRequest(r)
	lastSeq := query.Since
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	heartbeat := time.NewTicker(durableRunSSEHeartbeatInterval)
	defer heartbeat.Stop()

	for {
		run, exists := agentRunByID(s.sessionSnapshotWithApprovalRisk().AgentRuns, runID)
		if !exists {
			_ = writer.writeNamedJSON("agent_run_error", map[string]string{"error": "agent run not found"})
			return
		}
		query.Since = lastSeq
		events := agentRunEventsForQuery(run.Events, query)
		for _, event := range events {
			if event.Seq > lastSeq {
				lastSeq = event.Seq
			}
			if err := writer.writeAgentRunEvent(event); err != nil {
				return
			}
		}
		if agentRunStatusStreamComplete(run.Status) {
			_ = writer.writeNamedJSON("agent_run_snapshot", s.agentRunWithApprovalRisk(run))
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

func (s *Server) startAgentBackground(input, action string) (agentRunBackgroundResponse, error) {
	return s.startAgentBackgroundWithRetry(input, "", action)
}

func (s *Server) startAgentBackgroundWithRetry(input, retryOf, action string) (agentRunBackgroundResponse, error) {
	if !s.agentExecMu.TryLock() {
		return agentRunBackgroundResponse{}, fmt.Errorf("another agent run is already active")
	}
	runID := s.runtime.StartAgentRunWithOptions(input, session.AgentRunStartOptions{RetryOf: retryOf})
	if strings.TrimSpace(runID) == "" {
		s.agentExecMu.Unlock()
		return agentRunBackgroundResponse{}, fmt.Errorf("agent run could not be started")
	}
	ctx, cancel := context.WithCancel(context.Background())
	ctx = agent.WithAgentRunID(ctx, runID)
	s.registerActiveAgentRun(runID, cancel)
	go func() {
		defer s.agentExecMu.Unlock()
		defer s.unregisterActiveAgentRun(runID)
		defer cancel()
		record := s.recordAgentRunEvents(runID, nil)
		expanded, err := s.expandAtReferences(ctx, input, record)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				s.runtime.CancelAgentRun(runID, "agent run cancelled")
				return
			}
			s.runtime.FailAgentRun(runID, err.Error())
			return
		}
		result, err := s.runtime.RunStream(ctx, expanded, record)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				s.runtime.CancelAgentRun(runID, "agent run cancelled")
				return
			}
			s.runtime.FailAgentRun(runID, err.Error())
			return
		}
		s.runtime.CompleteAgentRun(runID, agentRunStatusForResult(result), result)
	}()
	return s.agentRunBackgroundResponse(runID, action, retryOf), nil
}

func (s *Server) startAgentRunBackgroundAction(runID, action string, run func(context.Context) error) (agentRunBackgroundResponse, error) {
	if !s.agentExecMu.TryLock() {
		return agentRunBackgroundResponse{}, fmt.Errorf("another agent run is already active")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.registerActiveAgentRun(runID, cancel)
	go func() {
		defer s.agentExecMu.Unlock()
		defer s.unregisterActiveAgentRun(runID)
		defer cancel()
		if err := run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			s.runtime.AppendAgentRunEvent(runID, session.AgentRunEventSnapshot{
				Type:    string(schema.StreamEventError),
				Content: err.Error(),
				IsError: true,
			})
		}
	}()
	return s.agentRunBackgroundResponse(runID, action, ""), nil
}

func (s *Server) agentRunBackgroundResponse(runID, action, retryOf string) agentRunBackgroundResponse {
	base := "/api/agent-runs/" + strings.TrimSpace(runID)
	resp := agentRunBackgroundResponse{
		RunID:       strings.TrimSpace(runID),
		Status:      "running",
		Action:      strings.TrimSpace(action),
		RetryOf:     strings.TrimSpace(retryOf),
		EventsURL:   base + "/events/stream",
		TimelineURL: base + "/timeline",
		ReplayURL:   base + "/replay",
		ActionsURL:  base + "/actions",
		DiffsURL:    base + "/diffs",
		CancelURL:   base + "/cancel",
	}
	if run, ok := s.runtime.AgentRun(runID); ok {
		run = s.agentRunWithApprovalRisk(run)
		resp.Status = run.Status
		resp.Run = &run
	}
	return resp
}

func (s *Server) recordAgentRunEvents(runID string, handler func(event schema.StreamEvent) error) func(event schema.StreamEvent) error {
	if strings.TrimSpace(runID) == "" {
		return handler
	}
	return func(event schema.StreamEvent) error {
		event = s.streamEventWithRisk(event)
		if strings.TrimSpace(event.RunID) == "" {
			event.RunID = runID
		}
		s.runtime.AppendAgentRunEvent(runID, agentRunEventSnapshot(event))
		if handler == nil {
			return nil
		}
		return handler(event)
	}
}

func agentRunEventSnapshot(event schema.StreamEvent) session.AgentRunEventSnapshot {
	return session.AgentRunEventSnapshot{
		Type:             string(event.Type),
		Content:          event.Content,
		ToolName:         event.ToolName,
		ToolCallID:       event.ToolCallID,
		ArgumentsSummary: event.ArgumentsSummary,
		AgentID:          event.AgentID,
		Mode:             event.Mode,
		IsError:          event.IsError,
		NeedsAction:      event.NeedsAction,
		Suspended:        event.Suspended,
		TaskStage:        event.TaskStage,
		PromptTokens:     event.PromptTokens,
		OutputTokens:     event.OutputTokens,
		CachedTokens:     event.CachedTokens,
		PromptBudget:     event.PromptBudget,
		Risk:             event.Risk,
	}
}

func (s *Server) registerActiveAgentRun(runID string, cancel context.CancelFunc) {
	if strings.TrimSpace(runID) == "" || cancel == nil {
		return
	}
	s.activeAgentMu.Lock()
	defer s.activeAgentMu.Unlock()
	if s.activeAgentRuns == nil {
		s.activeAgentRuns = make(map[string]context.CancelFunc)
	}
	s.activeAgentRuns[runID] = cancel
}

func (s *Server) unregisterActiveAgentRun(runID string) {
	if strings.TrimSpace(runID) == "" {
		return
	}
	s.activeAgentMu.Lock()
	defer s.activeAgentMu.Unlock()
	delete(s.activeAgentRuns, runID)
}

func (s *Server) agentRunCancelFunc(runID string) (context.CancelFunc, bool) {
	if strings.TrimSpace(runID) == "" {
		return nil, false
	}
	s.activeAgentMu.Lock()
	defer s.activeAgentMu.Unlock()
	cancel, ok := s.activeAgentRuns[runID]
	return cancel, ok
}

func agentRunByID(runs []session.AgentRunSnapshot, id string) (session.AgentRunSnapshot, bool) {
	for _, run := range runs {
		if run.ID == id {
			return run, true
		}
	}
	return session.AgentRunSnapshot{}, false
}

func agentRunEventsStreamPath(runID string) string {
	return "/api/agent-runs/" + strings.TrimSpace(runID) + "/events/stream"
}

func agentRunActionPath(runID, action string) string {
	return "/api/agent-runs/" + strings.TrimSpace(runID) + "/" + agentRunActionPathSegment(action)
}

func agentRunActionStreamPath(runID, action string) string {
	return agentRunActionPath(runID, action) + "/stream"
}

func agentRunActionPathSegment(action string) string {
	switch strings.TrimSpace(action) {
	case "approve_tool":
		return "approve-tool"
	case "approve_remember_tool":
		return "approve-remember-tool"
	case "deny_tool":
		return "deny-tool"
	case "approve_all_tools":
		return "approve-tools"
	case "approve_remember_all_tools":
		return "approve-remember-tools"
	case "deny_all_tools":
		return "deny-tools"
	default:
		return strings.TrimSpace(action)
	}
}

func normalizeAgentRunActionPath(action string) string {
	switch strings.TrimSpace(action) {
	case "approve-tool":
		return "approve_tool"
	case "approve-remember-tool":
		return "approve_remember_tool"
	case "deny-tool":
		return "deny_tool"
	case "approve-tools":
		return "approve_all_tools"
	case "approve-remember-tools":
		return "approve_remember_all_tools"
	case "deny-tools":
		return "deny_all_tools"
	default:
		return strings.TrimSpace(action)
	}
}

func agentRunStatusForResult(result schema.AgentResult) string {
	for _, toolResult := range result.ToolResults {
		if toolResult.Suspended {
			return "awaiting_tool_approval"
		}
		if toolResult.Denied {
			return "denied"
		}
	}
	return "completed"
}

func agentRunToolApprovalAllAction(action string) bool {
	switch strings.TrimSpace(action) {
	case "approve_all_tools", "approve_remember_all_tools", "deny_all_tools":
		return true
	default:
		return false
	}
}

func (s *Server) agentRunPendingApprovalCallID(run session.AgentRunSnapshot) string {
	callID := strings.TrimSpace(run.PendingCallID)
	if callID != "" {
		return callID
	}
	pending := s.agentRunPendingApprovalCallIDs(run)
	if len(pending) == 0 {
		return ""
	}
	return pending[len(pending)-1]
}

func (s *Server) agentRunPendingApprovalCallIDs(run session.AgentRunSnapshot) []string {
	ids := make([]string, 0, len(run.PendingApprovals)+1)
	seen := make(map[string]struct{}, len(run.PendingApprovals)+1)
	for _, pending := range run.PendingApprovals {
		callID := strings.TrimSpace(pending.CallID)
		if callID == "" {
			continue
		}
		if _, ok := seen[callID]; ok {
			continue
		}
		seen[callID] = struct{}{}
		ids = append(ids, callID)
	}
	callID := strings.TrimSpace(run.PendingCallID)
	if callID != "" {
		if _, ok := seen[callID]; !ok {
			ids = append(ids, callID)
		}
	}
	return ids
}

func (s *Server) agentRunToolApprovalResumable(callID string) bool {
	if s == nil || s.runtime == nil || strings.TrimSpace(callID) == "" {
		return false
	}
	if _, ok := s.runtime.PendingApproval(callID); !ok {
		return false
	}
	return s.runtime.OrdinaryToolApprovalResumable(callID)
}

func (s *Server) agentRunToolApprovalsResumable(run session.AgentRunSnapshot) bool {
	if s == nil || s.runtime == nil {
		return false
	}
	callIDs := s.agentRunPendingApprovalCallIDs(run)
	if len(callIDs) == 0 {
		return false
	}
	for _, callID := range callIDs {
		if _, ok := s.runtime.PendingApproval(callID); !ok {
			return false
		}
	}
	return s.runtime.OrdinaryToolApprovalsResumable(callIDs)
}

func (s *Server) agentRunRememberToolApprovalAvailable(callID string, resumable bool, fallbackReason string) (bool, string) {
	if !resumable {
		return false, fallbackReason
	}
	if s == nil || s.runtime == nil {
		return false, fallbackReason
	}
	ok, reason := s.runtime.CanRememberPendingToolApproval(callID)
	if !ok {
		if strings.TrimSpace(reason) == "" {
			reason = fallbackReason
		}
		return false, reason
	}
	return true, "ordinary agent tool approval can be remembered for this session"
}

func (s *Server) agentRunToolApprovalAllowed(callID string, fallbackReason string) (bool, string) {
	if s == nil || s.runtime == nil {
		return false, fallbackReason
	}
	ok, reason := s.runtime.CanApprovePendingToolApproval(callID)
	if !ok {
		if strings.TrimSpace(reason) == "" {
			reason = fallbackReason
		}
		return false, reason
	}
	return true, fallbackReason
}

func (s *Server) agentRunRememberToolApprovalsAvailable(run session.AgentRunSnapshot, resumable bool, fallbackReason string) (bool, string) {
	if !resumable {
		return false, fallbackReason
	}
	if s == nil || s.runtime == nil {
		return false, fallbackReason
	}
	ok, reason := s.runtime.CanRememberPendingToolApprovals(s.agentRunPendingApprovalCallIDs(run))
	if !ok {
		if strings.TrimSpace(reason) == "" {
			reason = fallbackReason
		}
		return false, reason
	}
	return true, "ordinary agent tool approvals can be remembered for this session"
}

func (s *Server) agentRunToolApprovalsAllowed(run session.AgentRunSnapshot, fallbackReason string) (bool, string) {
	if s == nil || s.runtime == nil {
		return false, fallbackReason
	}
	ok, reason := s.runtime.CanApprovePendingToolApprovals(s.agentRunPendingApprovalCallIDs(run))
	if !ok {
		if strings.TrimSpace(reason) == "" {
			reason = fallbackReason
		}
		return false, reason
	}
	return true, fallbackReason
}

func agentRunToolApprovalActionReason(resumable bool) string {
	if resumable {
		return "ordinary agent tool approval can resume from the persisted agent run snapshot"
	}
	return "ordinary agent tool approval context is not available yet or is missing from the persisted agent run snapshot; retry or cancel the agent run"
}

func agentRunStatusActive(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running", "cancelling":
		return true
	default:
		return false
	}
}

func agentRunStatusCancellable(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running", "cancelling", "awaiting_tool_approval":
		return true
	default:
		return false
	}
}

func agentRunStatusStreamComplete(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running", "cancelling":
		return false
	default:
		return true
	}
}

func agentRunQueryFromRequest(r *http.Request) agentRunQuery {
	if r == nil || r.URL == nil {
		return agentRunQuery{}
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
	return agentRunQuery{
		EventType:       normalizeWorkflowRunQueryToken(firstWorkflowRunQueryValue(values.Get("event_type"), values.Get("type"))),
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
}

func agentRunEventsForQuery(events []session.AgentRunEventSnapshot, query agentRunQuery) []session.AgentRunEventSnapshot {
	filtered := make([]session.AgentRunEventSnapshot, 0, len(events))
	for _, event := range events {
		if !agentRunEventMatchesQuery(event, query) {
			continue
		}
		filtered = append(filtered, event)
		if query.Limit > 0 && len(filtered) >= query.Limit {
			break
		}
	}
	return filtered
}

func (s *Server) agentRunEventsForQuery(events []session.AgentRunEventSnapshot, query agentRunQuery) []session.AgentRunEventSnapshot {
	filtered := agentRunEventsForQuery(events, query)
	if len(filtered) == 0 || !query.IncludeContent {
		return filtered
	}
	return s.runtime.HydrateAgentRunEvents(filtered)
}

func agentRunTimeline(run session.AgentRunSnapshot, query agentRunQuery) []agentRunTimelineItem {
	withoutLimit := query
	withoutLimit.Limit = 0
	events := agentRunEventsForQuery(run.Events, withoutLimit)
	items := make([]agentRunTimelineItem, 0, len(events)+1)
	for _, event := range events {
		items = append(items, agentRunTimelineItem{
			At:          event.At,
			Kind:        "event",
			Type:        event.Type,
			AgentID:     event.AgentID,
			Mode:        event.Mode,
			ToolName:    event.ToolName,
			ToolCallID:  event.ToolCallID,
			Summary:     agentRunTimelineEventSummary(event),
			Content:     agentRunTimelineContent(event.Content, query),
			IsError:     event.IsError,
			NeedsAction: event.NeedsAction || event.Suspended,
			Suspended:   event.Suspended,
		})
	}
	if strings.TrimSpace(run.Status) != "" {
		items = append(items, agentRunTimelineItem{
			At:      firstWorkflowRunQueryValue(run.CompletedAt, run.UpdatedAt, run.StartedAt),
			Kind:    "run",
			Type:    "agent_run",
			Status:  run.Status,
			AgentID: run.AgentID,
			Mode:    run.Mode,
			Title:   run.ID,
			Summary: agentRunSnapshotSummary(run),
			Content: agentRunTimelineContent(run.Output, query),
			IsError: strings.EqualFold(run.Status, "failed"),
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
		return append([]agentRunTimelineItem(nil), items[:query.Limit]...)
	}
	return items
}

func (s *Server) agentRunTimeline(run session.AgentRunSnapshot, query agentRunQuery) []agentRunTimelineItem {
	if query.IncludeContent {
		run = s.runtime.HydrateAgentRun(run)
	}
	return agentRunTimeline(run, query)
}

func agentRunEventMatchesQuery(event session.AgentRunEventSnapshot, query agentRunQuery) bool {
	if query.Since > 0 && event.Seq <= query.Since {
		return false
	}
	if query.EventType != "" && normalizeWorkflowRunQueryToken(event.Type) != query.EventType {
		return false
	}
	if query.AgentID != "" && !strings.EqualFold(strings.TrimSpace(event.AgentID), strings.TrimSpace(query.AgentID)) {
		return false
	}
	if query.ToolName != "" && !strings.EqualFold(strings.TrimSpace(event.ToolName), strings.TrimSpace(query.ToolName)) {
		return false
	}
	if query.ErrorsOnly && !event.IsError {
		return false
	}
	if query.NeedsActionOnly && !event.NeedsAction && !event.Suspended {
		return false
	}
	if query.SuspendedOnly && !event.Suspended {
		return false
	}
	if query.Query != "" && !agentRunEventContains(event, query.Query) {
		return false
	}
	return true
}

func agentRunTimelineEventSummary(event session.AgentRunEventSnapshot) string {
	if strings.TrimSpace(event.Content) != "" {
		return event.Content
	}
	if strings.TrimSpace(event.ArgumentsSummary) != "" {
		return event.ArgumentsSummary
	}
	if strings.TrimSpace(event.TaskStage) != "" {
		return event.TaskStage
	}
	if strings.TrimSpace(event.ToolName) != "" {
		return event.ToolName
	}
	return event.Type
}

func agentRunTimelineContent(content string, query agentRunQuery) string {
	if !query.IncludeContent {
		return ""
	}
	return content
}

func agentRunSnapshotSummary(run session.AgentRunSnapshot) string {
	if strings.TrimSpace(run.Error) != "" {
		return run.Error
	}
	if strings.TrimSpace(run.Output) != "" {
		return run.Output
	}
	if strings.TrimSpace(run.PendingTool) != "" {
		return "waiting for approval: " + strings.TrimSpace(run.PendingTool)
	}
	return run.Status
}

func agentRunEventContains(event session.AgentRunEventSnapshot, query string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return true
	}
	values := []string{
		event.Type,
		event.Content,
		event.ToolName,
		event.ToolCallID,
		event.ArgumentsSummary,
		event.AgentID,
		event.Mode,
		event.TaskStage,
	}
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
}
