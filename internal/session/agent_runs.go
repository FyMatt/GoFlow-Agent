package session

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

const (
	maxAgentRuns      = 30
	maxAgentRunEvents = 300
	maxAgentRunText   = maxWorkflowRunText
)

// AgentRunStartOptions carries metadata for a new ordinary agent run.
type AgentRunStartOptions struct {
	RetryOf string
}

// AgentRunResumeContextSnapshot stores enough ordinary agent state to resume a
// paused tool-approval turn after the HTTP request or process has gone away.
type AgentRunResumeContextSnapshot struct {
	AgentID                      string              `json:"agent_id,omitempty"`
	Mode                         string              `json:"mode,omitempty"`
	SystemPrompt                 string              `json:"system_prompt,omitempty"`
	MatchedSkill                 *schema.Skill       `json:"matched_skill,omitempty"`
	Messages                     []schema.Message    `json:"messages,omitempty"`
	MessagesArtifactRef          string              `json:"messages_artifact_ref,omitempty"`
	MessagesHash                 string              `json:"messages_hash,omitempty"`
	MessagesCount                int                 `json:"messages_count,omitempty"`
	MessagesBytes                int                 `json:"messages_bytes,omitempty"`
	MessagesStoredBytes          int                 `json:"messages_stored_bytes,omitempty"`
	MessagesExternalized         bool                `json:"messages_externalized,omitempty"`
	SuspendedCalls               []schema.ToolCall   `json:"suspended_calls,omitempty"`
	SuspendedCallsArtifactRef    string              `json:"suspended_calls_artifact_ref,omitempty"`
	SuspendedCallsHash           string              `json:"suspended_calls_hash,omitempty"`
	SuspendedCallCount           int                 `json:"suspended_calls_count,omitempty"`
	SuspendedCallsBytes          int                 `json:"suspended_calls_bytes,omitempty"`
	SuspendedCallsStoredBytes    int                 `json:"suspended_calls_stored_bytes,omitempty"`
	SuspendedCallsExternalized   bool                `json:"suspended_calls_externalized,omitempty"`
	CollectedResults             []schema.ToolResult `json:"collected_results,omitempty"`
	CollectedResultsArtifactRef  string              `json:"collected_results_artifact_ref,omitempty"`
	CollectedResultsHash         string              `json:"collected_results_hash,omitempty"`
	CollectedResultCount         int                 `json:"collected_results_count,omitempty"`
	CollectedResultsBytes        int                 `json:"collected_results_bytes,omitempty"`
	CollectedResultsStoredBytes  int                 `json:"collected_results_stored_bytes,omitempty"`
	CollectedResultsExternalized bool                `json:"collected_results_externalized,omitempty"`
}

// AgentRunSnapshot is a durable, replayable view of one ordinary agent turn.
type AgentRunSnapshot struct {
	ID               string                         `json:"id"`
	Status           string                         `json:"status,omitempty"`
	Request          string                         `json:"request,omitempty"`
	Output           string                         `json:"output,omitempty"`
	Error            string                         `json:"error,omitempty"`
	AgentID          string                         `json:"agent_id,omitempty"`
	Mode             string                         `json:"mode,omitempty"`
	StartedAt        string                         `json:"started_at,omitempty"`
	UpdatedAt        string                         `json:"updated_at,omitempty"`
	CompletedAt      string                         `json:"completed_at,omitempty"`
	CancelledAt      string                         `json:"cancelled_at,omitempty"`
	RetryOf          string                         `json:"retry_of,omitempty"`
	Attempt          int                            `json:"attempt,omitempty"`
	PendingCallID    string                         `json:"pending_call_id,omitempty"`
	PendingTool      string                         `json:"pending_tool_name,omitempty"`
	PendingAgentID   string                         `json:"pending_agent_id,omitempty"`
	PendingApprovals []PendingApprovalSnapshot      `json:"pending_approvals,omitempty"`
	ResumeContext    *AgentRunResumeContextSnapshot `json:"resume_context,omitempty"`
	Result           *schema.AgentResult            `json:"result,omitempty"`
	Events           []AgentRunEventSnapshot        `json:"events,omitempty"`
	Artifacts        []AgentRunArtifactSnapshot     `json:"artifacts,omitempty"`
	EventsCount      int                            `json:"events_count,omitempty"`
	ArtifactsCount   int                            `json:"artifacts_count,omitempty"`
	DiffsCount       int                            `json:"diffs_count,omitempty"`
}

// AgentRunEventSnapshot captures a replayable ordinary agent stream/status event.
type AgentRunEventSnapshot struct {
	Seq                 int                     `json:"seq,omitempty"`
	At                  string                  `json:"at,omitempty"`
	Type                string                  `json:"type,omitempty"`
	Content             string                  `json:"content,omitempty"`
	ContentArtifactRef  string                  `json:"content_artifact_ref,omitempty"`
	ContentHash         string                  `json:"content_hash,omitempty"`
	ContentBytes        int                     `json:"content_bytes,omitempty"`
	ContentStoredBytes  int                     `json:"content_stored_bytes,omitempty"`
	ContentExternalized bool                    `json:"content_externalized,omitempty"`
	ToolName            string                  `json:"tool_name,omitempty"`
	ToolCallID          string                  `json:"tool_call_id,omitempty"`
	ArgumentsSummary    string                  `json:"arguments_summary,omitempty"`
	AgentID             string                  `json:"agent_id,omitempty"`
	Mode                string                  `json:"mode,omitempty"`
	IsError             bool                    `json:"is_error,omitempty"`
	NeedsAction         bool                    `json:"needs_action,omitempty"`
	Suspended           bool                    `json:"suspended,omitempty"`
	TaskStage           string                  `json:"task_stage,omitempty"`
	PromptTokens        int                     `json:"prompt_tokens,omitempty"`
	OutputTokens        int                     `json:"output_tokens,omitempty"`
	CachedTokens        int                     `json:"cached_tokens,omitempty"`
	PromptBudget        *schema.PromptBudget    `json:"prompt_budget,omitempty"`
	Risk                *schema.ToolRiskProfile `json:"risk,omitempty"`
}

// StartAgentRun creates a bounded, session-persisted ordinary agent run record.
func (s *State) StartAgentRun(request string) string {
	return s.StartAgentRunWithOptions(request, AgentRunStartOptions{})
}

// StartAgentRunWithOptions creates a bounded ordinary agent run record.
func (s *State) StartAgentRunWithOptions(request string, opts AgentRunStartOptions) string {
	if s == nil {
		return ""
	}
	now := time.Now().UTC()
	timestamp := workflowRunTimestamp(now)
	s.mu.Lock()
	defer s.mu.Unlock()

	retryOf := strings.TrimSpace(opts.RetryOf)
	attempt := 1
	if retryOf != "" {
		attempt = 2
		if index := s.agentRunIndexLocked(retryOf); index >= 0 && s.agentRuns[index].Attempt > 0 {
			attempt = s.agentRuns[index].Attempt + 1
		}
	}
	id := uniqueAgentRunID(s.agentRuns, now)
	run := AgentRunSnapshot{
		ID:        id,
		Status:    "running",
		Request:   trimAgentRunText(request),
		StartedAt: timestamp,
		UpdatedAt: timestamp,
		RetryOf:   retryOf,
		Attempt:   attempt,
		Events: []AgentRunEventSnapshot{{
			Seq:     1,
			At:      timestamp,
			Type:    "agent_run_started",
			Content: agentRunStartContent(retryOf),
		}},
	}
	for i := range run.Events {
		run.Events[i] = s.normalizeAgentRunEventLocked(id, run.Events[i])
	}
	s.agentRuns = append([]AgentRunSnapshot{run}, s.agentRuns...)
	if len(s.agentRuns) > maxAgentRuns {
		s.agentRuns = append([]AgentRunSnapshot(nil), s.agentRuns[:maxAgentRuns]...)
	}
	return id
}

// RequestAgentRunCancel marks a running ordinary agent run as cancellation-requested.
func (s *State) RequestAgentRunCancel(runID, reason string) (AgentRunSnapshot, bool) {
	if s == nil || strings.TrimSpace(runID) == "" {
		return AgentRunSnapshot{}, false
	}
	now := workflowRunTimestamp(time.Now().UTC())
	s.mu.Lock()
	defer s.mu.Unlock()
	index := s.agentRunIndexLocked(runID)
	if index < 0 {
		return AgentRunSnapshot{}, false
	}
	run := s.agentRuns[index]
	if !isTerminalAgentRunStatus(run.Status) {
		run.Status = "cancelling"
	}
	run.UpdatedAt = now
	run.Events = appendAgentRunEventLocked(run.Events, s.normalizeAgentRunEventLocked(run.ID, AgentRunEventSnapshot{
		At:      now,
		Type:    "agent_run_cancel_requested",
		Content: trimAgentRunText(reason),
		AgentID: run.AgentID,
		Mode:    run.Mode,
	}))
	s.agentRuns[index] = run
	return copyAgentRunSnapshot(run), true
}

// CancelAgentRun marks an ordinary agent run as cancelled.
func (s *State) CancelAgentRun(runID, reason string) (AgentRunSnapshot, bool) {
	if s == nil || strings.TrimSpace(runID) == "" {
		return AgentRunSnapshot{}, false
	}
	now := workflowRunTimestamp(time.Now().UTC())
	s.mu.Lock()
	defer s.mu.Unlock()
	index := s.agentRunIndexLocked(runID)
	if index < 0 {
		return AgentRunSnapshot{}, false
	}
	run := s.agentRuns[index]
	run.Status = "cancelled"
	run.UpdatedAt = now
	run.CompletedAt = now
	run.CancelledAt = now
	run.PendingCallID = ""
	run.PendingTool = ""
	run.PendingAgentID = ""
	run.PendingApprovals = nil
	run.ResumeContext = nil
	if strings.TrimSpace(reason) != "" {
		run.Output = trimAgentRunText(reason)
	}
	run.Events = appendAgentRunEventLocked(run.Events, s.normalizeAgentRunEventLocked(run.ID, AgentRunEventSnapshot{
		At:      now,
		Type:    "agent_run_cancelled",
		Content: trimAgentRunText(reason),
		AgentID: run.AgentID,
		Mode:    run.Mode,
	}))
	s.agentRuns[index] = run
	return copyAgentRunSnapshot(run), true
}

// CompleteAgentRun records the final or suspended result returned by an ordinary agent run.
func (s *State) CompleteAgentRun(runID, status string, result schema.AgentResult) (AgentRunSnapshot, bool) {
	if s == nil || strings.TrimSpace(runID) == "" {
		return AgentRunSnapshot{}, false
	}
	now := workflowRunTimestamp(time.Now().UTC())
	s.mu.Lock()
	defer s.mu.Unlock()
	index := s.agentRunIndexLocked(runID)
	if index < 0 {
		return AgentRunSnapshot{}, false
	}
	run := s.agentRuns[index]
	status = strings.TrimSpace(status)
	if status == "" {
		status = "completed"
	}
	run.Status = status
	run.UpdatedAt = now
	if isTerminalAgentRunStatus(status) {
		run.CompletedAt = now
	}
	if strings.EqualFold(status, "cancelled") {
		run.CancelledAt = now
	}
	run.Output = trimAgentRunText(result.Output)
	run.AgentID = strings.TrimSpace(result.AgentID)
	run.Mode = strings.TrimSpace(result.Mode)
	copied := copyAgentResult(result)
	run.Result = &copied
	run.Artifacts = s.buildAgentRunArtifactsLocked(run, now)
	if !strings.EqualFold(status, "awaiting_tool_approval") {
		run.PendingCallID = ""
		run.PendingTool = ""
		run.PendingAgentID = ""
		run.PendingApprovals = nil
		run.ResumeContext = nil
	}
	eventType := "agent_run_completed"
	if !isTerminalAgentRunStatus(status) {
		eventType = "agent_run_paused"
	}
	run.Events = appendAgentRunEventLocked(run.Events, s.normalizeAgentRunEventLocked(run.ID, AgentRunEventSnapshot{
		At:      now,
		Type:    eventType,
		Content: trimAgentRunText(result.Output),
		AgentID: run.AgentID,
		Mode:    run.Mode,
	}))
	s.agentRuns[index] = run
	return copyAgentRunSnapshot(run), true
}

// FailAgentRun marks an ordinary agent run as failed.
func (s *State) FailAgentRun(runID, message string) (AgentRunSnapshot, bool) {
	if s == nil || strings.TrimSpace(runID) == "" {
		return AgentRunSnapshot{}, false
	}
	now := workflowRunTimestamp(time.Now().UTC())
	s.mu.Lock()
	defer s.mu.Unlock()
	index := s.agentRunIndexLocked(runID)
	if index < 0 {
		return AgentRunSnapshot{}, false
	}
	run := s.agentRuns[index]
	run.Status = "failed"
	run.Error = trimAgentRunText(message)
	run.UpdatedAt = now
	run.CompletedAt = now
	run.ResumeContext = nil
	run.Artifacts = s.buildAgentRunArtifactsLocked(run, now)
	run.Events = appendAgentRunEventLocked(run.Events, s.normalizeAgentRunEventLocked(run.ID, AgentRunEventSnapshot{
		At:      now,
		Type:    string(schema.StreamEventError),
		Content: trimAgentRunText(message),
		AgentID: run.AgentID,
		Mode:    run.Mode,
		IsError: true,
	}))
	s.agentRuns[index] = run
	return copyAgentRunSnapshot(run), true
}

// AppendAgentRunEvent adds a bounded replay event to an existing ordinary agent run record.
func (s *State) AppendAgentRunEvent(runID string, event AgentRunEventSnapshot) {
	if s == nil || strings.TrimSpace(runID) == "" {
		return
	}
	now := workflowRunTimestamp(time.Now().UTC())
	if strings.TrimSpace(event.At) == "" {
		event.At = now
	}
	event.Type = strings.TrimSpace(event.Type)
	event.ArgumentsSummary = trimAgentRunText(event.ArgumentsSummary)
	s.mu.Lock()
	defer s.mu.Unlock()
	index := s.agentRunIndexLocked(runID)
	if index < 0 {
		return
	}
	run := s.agentRuns[index]
	if strings.TrimSpace(event.AgentID) != "" {
		run.AgentID = strings.TrimSpace(event.AgentID)
	}
	if strings.TrimSpace(event.Mode) != "" {
		run.Mode = strings.TrimSpace(event.Mode)
	}
	if event.Suspended || strings.EqualFold(event.Type, string(schema.StreamEventApproval)) {
		run.Status = "awaiting_tool_approval"
		run.PendingCallID = strings.TrimSpace(event.ToolCallID)
		run.PendingTool = strings.TrimSpace(event.ToolName)
		run.PendingAgentID = strings.TrimSpace(event.AgentID)
	}
	event = s.normalizeAgentRunEventLocked(runID, event)
	run.Events = appendAgentRunEventLocked(run.Events, event)
	run.UpdatedAt = now
	s.agentRuns[index] = run
}

// AppendAgentRunPendingApproval records a pending tool approval on an ordinary agent run.
func (s *State) AppendAgentRunPendingApproval(runID string, approval PendingApprovalSnapshot) {
	if s == nil || strings.TrimSpace(runID) == "" || strings.TrimSpace(approval.CallID) == "" {
		return
	}
	approval.AgentRunID = strings.TrimSpace(runID)
	s.mu.Lock()
	defer s.mu.Unlock()
	approval = s.normalizePendingApprovalLocked(approval)
	index := s.agentRunIndexLocked(runID)
	if index < 0 {
		return
	}
	run := s.agentRuns[index]
	run.Status = "awaiting_tool_approval"
	run.PendingCallID = strings.TrimSpace(approval.CallID)
	run.PendingTool = strings.TrimSpace(approval.ToolName)
	run.PendingAgentID = strings.TrimSpace(approval.AgentID)
	replaced := false
	for i := range run.PendingApprovals {
		if run.PendingApprovals[i].CallID == approval.CallID {
			run.PendingApprovals[i] = approval
			replaced = true
			break
		}
	}
	if !replaced {
		run.PendingApprovals = append(run.PendingApprovals, approval)
	}
	run.UpdatedAt = workflowRunTimestamp(time.Now().UTC())
	s.agentRuns[index] = run
}

// SetAgentRunResumeContext stores the durable ordinary tool-loop resume state
// for a paused agent run.
func (s *State) SetAgentRunResumeContext(runID string, context AgentRunResumeContextSnapshot) (AgentRunSnapshot, bool) {
	if s == nil || strings.TrimSpace(runID) == "" {
		return AgentRunSnapshot{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	index := s.agentRunIndexLocked(runID)
	if index < 0 {
		return AgentRunSnapshot{}, false
	}
	run := s.agentRuns[index]
	copied := s.normalizeAgentRunResumeContextLocked(runID, context)
	run.ResumeContext = &copied
	run.UpdatedAt = workflowRunTimestamp(time.Now().UTC())
	s.agentRuns[index] = run
	return copyAgentRunSnapshot(run), true
}

// ClearAgentRunPendingApproval removes one pending approval from an ordinary agent run.
func (s *State) ClearAgentRunPendingApproval(runID, callID string) {
	if s == nil || strings.TrimSpace(runID) == "" || strings.TrimSpace(callID) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	index := s.agentRunIndexLocked(runID)
	if index < 0 {
		return
	}
	run := s.agentRuns[index]
	filtered := make([]PendingApprovalSnapshot, 0, len(run.PendingApprovals))
	for _, item := range run.PendingApprovals {
		if item.CallID == callID {
			continue
		}
		filtered = append(filtered, item)
	}
	run.PendingApprovals = filtered
	if len(run.PendingApprovals) > 0 {
		last := run.PendingApprovals[len(run.PendingApprovals)-1]
		run.PendingCallID = strings.TrimSpace(last.CallID)
		run.PendingTool = strings.TrimSpace(last.ToolName)
		run.PendingAgentID = strings.TrimSpace(last.AgentID)
	} else {
		run.PendingCallID = ""
		run.PendingTool = ""
		run.PendingAgentID = ""
	}
	run.UpdatedAt = workflowRunTimestamp(time.Now().UTC())
	s.agentRuns[index] = run
}

// AgentRuns returns a newest-first copy of recent ordinary agent runs.
func (s *State) AgentRuns() []AgentRunSnapshot {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return copyAgentRunSnapshots(s.agentRuns)
}

// AgentRun returns one ordinary agent run by id.
func (s *State) AgentRun(id string) (AgentRunSnapshot, bool) {
	if s == nil || strings.TrimSpace(id) == "" {
		return AgentRunSnapshot{}, false
	}
	s.mu.RLock()
	for _, run := range s.agentRuns {
		if run.ID == id {
			copied := copyAgentRunSnapshot(run)
			s.mu.RUnlock()
			return s.HydrateAgentRun(copied), true
		}
	}
	s.mu.RUnlock()
	return AgentRunSnapshot{}, false
}

func (s *State) agentRunIndexLocked(id string) int {
	id = strings.TrimSpace(id)
	for i := range s.agentRuns {
		if s.agentRuns[i].ID == id {
			return i
		}
	}
	return -1
}

func uniqueAgentRunID(runs []AgentRunSnapshot, now time.Time) string {
	for suffix := 0; ; suffix++ {
		id := agentRunID(now, suffix)
		found := false
		for _, run := range runs {
			if run.ID == id {
				found = true
				break
			}
		}
		if !found {
			return id
		}
	}
}

func agentRunID(now time.Time, suffix int) string {
	if suffix > 0 {
		return fmt.Sprintf("run-agent-%d-%d", now.UnixNano(), suffix)
	}
	return fmt.Sprintf("run-agent-%d", now.UnixNano())
}

func agentRunStartContent(retryOf string) string {
	if strings.TrimSpace(retryOf) == "" {
		return "agent run started"
	}
	return "agent retry started from " + strings.TrimSpace(retryOf)
}

func isTerminalAgentRunStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "failed", "cancelled", "canceled", "denied":
		return true
	default:
		return false
	}
}

func trimAgentRunText(value string) string {
	if len(value) <= maxAgentRunText {
		return value
	}
	return value[:maxAgentRunText]
}

func copyAgentRunSnapshots(runs []AgentRunSnapshot) []AgentRunSnapshot {
	if len(runs) == 0 {
		return nil
	}
	copied := make([]AgentRunSnapshot, len(runs))
	for i, run := range runs {
		copied[i] = copyAgentRunSnapshot(run)
	}
	return copied
}

func copyAgentRunSnapshot(run AgentRunSnapshot) AgentRunSnapshot {
	if run.Result != nil {
		copied := copyAgentResult(*run.Result)
		run.Result = &copied
	}
	run.Artifacts = copyAgentRunArtifacts(run.Artifacts)
	if len(run.Events) > 0 {
		run.Events = append([]AgentRunEventSnapshot(nil), run.Events...)
		for i := range run.Events {
			if run.Events[i].Risk != nil {
				risk := copyToolRiskProfile(*run.Events[i].Risk)
				run.Events[i].Risk = &risk
			}
		}
		assignAgentRunEventSeqs(run.Events)
	}
	if len(run.PendingApprovals) > 0 {
		run.PendingApprovals = copyPendingApprovalSnapshots(run.PendingApprovals)
	}
	if run.ResumeContext != nil {
		copied := copyAgentRunResumeContext(*run.ResumeContext)
		run.ResumeContext = &copied
	}
	return run
}

func (s *State) buildAgentRunArtifactsLocked(run AgentRunSnapshot, now string) []AgentRunArtifactSnapshot {
	artifacts := deriveAgentRunArtifacts(run, now)
	if len(artifacts) == 0 {
		return nil
	}
	out := make([]AgentRunArtifactSnapshot, 0, len(artifacts))
	for _, artifact := range artifacts {
		if strings.TrimSpace(artifact.Content) == "" && strings.TrimSpace(artifact.ArtifactRef) == "" && strings.TrimSpace(artifact.Hash) == "" {
			continue
		}
		out = append(out, normalizeAgentRunArtifactLocked(s, artifact, now))
	}
	return out
}

func deriveAgentRunArtifacts(run AgentRunSnapshot, createdAt string) []AgentRunArtifactSnapshot {
	items := make([]AgentRunArtifactSnapshot, 0)
	runID := strings.TrimSpace(run.ID)
	agentID := strings.TrimSpace(run.AgentID)
	mode := strings.TrimSpace(run.Mode)
	if run.Result != nil {
		agentID = firstAgentRunArtifactValue(agentID, run.Result.AgentID)
		mode = firstAgentRunArtifactValue(mode, run.Result.Mode)
	}
	if strings.TrimSpace(run.Output) != "" {
		items = append(items, AgentRunArtifactSnapshot{
			ID:        agentRunArtifactID("final-output"),
			Kind:      "output",
			Title:     "Final output",
			Summary:   trimAgentRunText(run.Output),
			Content:   run.Output,
			Mime:      "text/markdown",
			CreatedAt: createdAt,
			AgentID:   agentID,
			Mode:      mode,
			Metadata:  agentRunArtifactMetadata(runID, "agent_output", nil),
		})
	}
	if strings.TrimSpace(run.Error) != "" {
		items = append(items, AgentRunArtifactSnapshot{
			ID:        agentRunArtifactID("error"),
			Kind:      "error",
			Title:     "Run error",
			Summary:   trimAgentRunText(run.Error),
			Content:   run.Error,
			Mime:      "text/plain",
			CreatedAt: createdAt,
			AgentID:   agentID,
			Mode:      mode,
			Metadata:  agentRunArtifactMetadata(runID, "agent_error", map[string]string{"is_error": "true"}),
		})
	}
	if run.Result == nil {
		return items
	}
	result := *run.Result
	for i, toolResult := range result.ToolResults {
		title := firstAgentRunArtifactValue(toolResult.ToolName, toolResult.CallID, "Tool result")
		metadata := agentRunArtifactMetadata(runID, "tool_result", map[string]string{
			"is_error":  boolString(toolResult.IsError),
			"denied":    boolString(toolResult.Denied),
			"suspended": boolString(toolResult.Suspended),
		})
		metadata = mergeAgentRunArtifactMetadata(metadata, agentRunDiffMetadata(toolResult.ToolName, toolResult.Content))
		items = append(items, AgentRunArtifactSnapshot{
			ID:         agentRunArtifactIndexedID("tool-result", i+1, toolResult.CallID, toolResult.ToolName),
			Kind:       "tool_result",
			Title:      title,
			Summary:    trimAgentRunText(toolResult.Content),
			Content:    toolResult.Content,
			Mime:       "text/plain",
			CreatedAt:  createdAt,
			ToolName:   toolResult.ToolName,
			ToolCallID: toolResult.CallID,
			AgentID:    agentID,
			Mode:       mode,
			Metadata:   metadata,
		})
	}
	for i, section := range result.Structured {
		content := agentRunArtifactJSON(section)
		summary := firstAgentRunArtifactValue(section.Summary, strings.Join(section.Items, "\n"), content)
		items = append(items, AgentRunArtifactSnapshot{
			ID:        agentRunArtifactIndexedID("structured", i+1, section.Title, section.Kind),
			Kind:      firstAgentRunArtifactValue(section.Kind, "structured"),
			Title:     firstAgentRunArtifactValue(section.Title, section.Kind, "Structured output"),
			Summary:   trimAgentRunText(summary),
			Content:   content,
			Mime:      "application/json",
			CreatedAt: createdAt,
			AgentID:   agentID,
			Mode:      mode,
			Metadata:  agentRunArtifactMetadata(runID, "structured", nil),
		})
	}
	for i, finding := range result.Findings {
		items = append(items, AgentRunArtifactSnapshot{
			ID:        agentRunArtifactIndexedID("finding", i+1, finding.Summary, finding.Severity),
			Kind:      "finding",
			Title:     firstAgentRunArtifactValue(finding.Severity, "Finding"),
			Summary:   trimAgentRunText(finding.Summary),
			Content:   agentRunArtifactJSON(finding),
			Mime:      "application/json",
			CreatedAt: createdAt,
			AgentID:   agentID,
			Mode:      mode,
			Metadata: agentRunArtifactMetadata(runID, "finding", map[string]string{
				"severity": finding.Severity,
				"fixed":    boolString(finding.Fixed),
				"files":    strings.Join(finding.Files, ", "),
			}),
		})
	}
	for i, change := range result.Changes {
		items = append(items, AgentRunArtifactSnapshot{
			ID:        agentRunArtifactIndexedID("change", i+1, change.Summary),
			Kind:      "change",
			Title:     "Change",
			Summary:   trimAgentRunText(change.Summary),
			Content:   agentRunArtifactJSON(change),
			Mime:      "application/json",
			CreatedAt: createdAt,
			AgentID:   agentID,
			Mode:      mode,
			Metadata: agentRunArtifactMetadata(runID, "change", map[string]string{
				"files": strings.Join(change.Files, ", "),
			}),
		})
	}
	for i, verification := range result.Verification {
		failed := strings.EqualFold(verification.Status, "failed") || strings.EqualFold(verification.Status, "fail")
		items = append(items, AgentRunArtifactSnapshot{
			ID:        agentRunArtifactIndexedID("verification", i+1, verification.Kind, verification.Status),
			Kind:      "verification",
			Title:     firstAgentRunArtifactValue(verification.Kind, "Verification"),
			Summary:   trimAgentRunText(firstAgentRunArtifactValue(verification.Detail, verification.Status)),
			Content:   agentRunArtifactJSON(verification),
			Mime:      "application/json",
			CreatedAt: createdAt,
			AgentID:   agentID,
			Mode:      mode,
			Metadata: agentRunArtifactMetadata(runID, "verification", map[string]string{
				"status":   verification.Status,
				"is_error": boolString(failed),
			}),
		})
	}
	for i, audit := range result.AuditTrail {
		isError := strings.Contains(strings.ToLower(audit.Outcome), "error") || strings.Contains(strings.ToLower(audit.Outcome), "fail")
		items = append(items, AgentRunArtifactSnapshot{
			ID:        agentRunArtifactIndexedID("audit", i+1, audit.Type, audit.ToolName, audit.Outcome),
			Kind:      "audit",
			Title:     firstAgentRunArtifactValue(audit.Type, "Audit entry"),
			Summary:   trimAgentRunText(firstAgentRunArtifactValue(audit.Detail, audit.Outcome)),
			Content:   agentRunArtifactJSON(audit),
			Mime:      "application/json",
			CreatedAt: createdAt,
			ToolName:  audit.ToolName,
			AgentID:   firstAgentRunArtifactValue(audit.AgentID, agentID),
			Mode:      mode,
			Metadata: agentRunArtifactMetadata(runID, "audit", map[string]string{
				"outcome":     audit.Outcome,
				"skill_name":  audit.SkillName,
				"duration_ms": workflowArtifactIntString(int(audit.DurationMs)),
				"is_error":    boolString(isError),
			}),
		})
	}
	return items
}

func normalizeAgentRunArtifactLocked(state *State, artifact AgentRunArtifactSnapshot, createdAt string) AgentRunArtifactSnapshot {
	artifact.ID = sanitizeSessionArtifactID(artifact.ID)
	if artifact.ID == "" {
		artifact.ID = sanitizeSessionArtifactID(strings.Join([]string{artifact.Kind, artifact.ToolName, artifact.ToolCallID}, "-"))
	}
	if artifact.ID == "" {
		artifact.ID = "agent-artifact"
	}
	artifact.Ref = "goflow://session-artifacts/" + artifact.ID
	if strings.TrimSpace(artifact.CreatedAt) == "" {
		artifact.CreatedAt = createdAt
	}
	artifact.Kind = fallbackSessionArtifactValue(artifact.Kind, "artifact")
	artifact.Title = fallbackSessionArtifactValue(artifact.Title, fallbackSessionArtifactValue(artifact.ToolName, "Artifact"))
	artifact.Mime = fallbackSessionArtifactValue(artifact.Mime, agentRunArtifactMime(artifact.Kind))
	artifact.ContentBytes = len([]byte(artifact.Content))
	if strings.TrimSpace(artifact.Summary) == "" {
		artifact.Summary = artifact.Content
	}
	artifact.Summary = trimSessionArtifactBytes(artifact.Summary, maxSessionArtifactSummaryBytes)
	artifact.Metadata = copyStringMapForArtifact(artifact.Metadata)
	if state != nil && state.artifactStore != nil && strings.TrimSpace(artifact.Content) != "" {
		artifact = state.externalizeArtifactLocked(artifact)
	} else {
		artifact.StoredBytes = len([]byte(artifact.Content))
	}
	return normalizeAgentRunArtifactReferenceMetadata(artifact)
}

func normalizeAgentRunArtifactReferenceMetadata(artifact AgentRunArtifactSnapshot) AgentRunArtifactSnapshot {
	if strings.TrimSpace(artifact.ArtifactRef) == "" {
		artifact.ArtifactRef = firstAgentRunArtifactValue(artifact.Metadata["artifact_ref"], artifact.Metadata["ref"])
	}
	if strings.TrimSpace(artifact.Hash) == "" {
		artifact.Hash = firstAgentRunArtifactValue(artifact.Metadata["hash"], workflowArtifactHashFromRef(artifact.ArtifactRef))
	}
	size := firstAgentRunArtifactInt(artifact.ContentBytes, len([]byte(artifact.Content)), len([]byte(artifact.Summary)))
	artifact.Metadata = copyStringMapForArtifact(artifact.Metadata)
	if artifact.Metadata == nil {
		artifact.Metadata = map[string]string{}
	}
	workflowArtifactSetMetadata(artifact.Metadata, "artifact_ref", artifact.ArtifactRef)
	workflowArtifactSetMetadata(artifact.Metadata, "hash", artifact.Hash)
	workflowArtifactSetMetadata(artifact.Metadata, "mime", artifact.Mime)
	workflowArtifactSetMetadata(artifact.Metadata, "size", workflowArtifactIntString(size))
	workflowArtifactSetMetadata(artifact.Metadata, "content_bytes", workflowArtifactIntString(artifact.ContentBytes))
	workflowArtifactSetMetadata(artifact.Metadata, "stored_bytes", workflowArtifactIntString(artifact.StoredBytes))
	if strings.TrimSpace(artifact.ArtifactRef) != "" || strings.TrimSpace(artifact.Hash) != "" {
		workflowArtifactSetMetadata(artifact.Metadata, "externalized", "true")
	}
	if len(artifact.Metadata) == 0 {
		artifact.Metadata = nil
	}
	return artifact
}

func agentRunArtifactMetadata(runID, source string, extra map[string]string) map[string]string {
	metadata := map[string]string{
		"agent_run_id": strings.TrimSpace(runID),
		"source":       strings.TrimSpace(source),
	}
	for key, value := range extra {
		if strings.TrimSpace(value) == "" {
			continue
		}
		metadata[key] = value
	}
	return copyStringMapForArtifact(metadata)
}

func mergeAgentRunArtifactMetadata(base, extra map[string]string) map[string]string {
	if len(base) == 0 && len(extra) == 0 {
		return nil
	}
	out := copyStringMapForArtifact(base)
	if out == nil {
		out = map[string]string{}
	}
	for key, value := range extra {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			continue
		}
		out[key] = value
	}
	return copyStringMapForArtifact(out)
}

func agentRunDiffMetadata(toolName, content string) map[string]string {
	if !agentRunDiffToolName(toolName) || strings.TrimSpace(content) == "" {
		return nil
	}
	var payload map[string]any
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil || len(payload) == 0 {
		return nil
	}
	metadata := map[string]string{}
	agentRunDiffSetMetadata(metadata, "path", firstAgentRunArtifactValue(agentRunDiffString(payload, "relative_path"), agentRunDiffString(payload, "path")))
	agentRunDiffSetMetadata(metadata, "status", agentRunDiffString(payload, "status"))
	agentRunDiffSetMetadata(metadata, "line_summary", firstAgentRunArtifactValue(agentRunDiffString(payload, "line_summary"), agentRunDiffString(payload, "summary")))
	agentRunDiffSetMetadata(metadata, "old_range", agentRunDiffString(payload, "old_range"))
	agentRunDiffSetMetadata(metadata, "new_range", agentRunDiffString(payload, "new_range"))
	agentRunDiffSetMetadata(metadata, "added_lines", agentRunDiffIntString(payload, "added_lines"))
	agentRunDiffSetMetadata(metadata, "deleted_lines", agentRunDiffIntString(payload, "deleted_lines"))
	agentRunDiffSetMetadata(metadata, "bytes_written", agentRunDiffIntString(payload, "bytes_written"))
	agentRunDiffSetMetadata(metadata, "old_line_count", agentRunDiffIntString(payload, "old_line_count"))
	agentRunDiffSetMetadata(metadata, "new_line_count", agentRunDiffIntString(payload, "new_line_count"))
	if len(metadata) == 0 {
		return nil
	}
	return metadata
}

func agentRunDiffToolName(toolName string) bool {
	name := strings.ToLower(strings.TrimSpace(toolName))
	if index := strings.LastIndex(name, "/"); index >= 0 {
		name = name[index+1:]
	}
	switch name {
	case "write_file", "edit_file", "delete_file":
		return true
	default:
		return false
	}
}

func agentRunDiffString(payload map[string]any, key string) string {
	value, _ := payload[key].(string)
	return strings.TrimSpace(value)
}

func agentRunDiffIntString(payload map[string]any, key string) string {
	switch value := payload[key].(type) {
	case int:
		if value != 0 {
			return fmt.Sprintf("%d", value)
		}
	case float64:
		if value != 0 {
			return fmt.Sprintf("%d", int(value))
		}
	case json.Number:
		if parsed, err := value.Int64(); err == nil && parsed != 0 {
			return fmt.Sprintf("%d", parsed)
		}
	}
	return ""
}

func agentRunDiffSetMetadata(metadata map[string]string, key, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	metadata[key] = value
}

func agentRunArtifactID(kind string) string {
	return "agent-" + sanitizeSessionArtifactID(kind)
}

func agentRunArtifactIndexedID(prefix string, index int, values ...string) string {
	parts := []string{"agent", sanitizeSessionArtifactID(prefix)}
	for _, value := range values {
		value = sanitizeSessionArtifactID(value)
		if value != "" {
			parts = append(parts, value)
			break
		}
	}
	if index > 0 {
		parts = append(parts, fmt.Sprintf("%d", index))
	}
	return strings.Trim(strings.Join(parts, "-"), "-")
}

func agentRunArtifactJSON(value any) string {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return ""
	}
	return string(data)
}

func agentRunArtifactMime(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "structured", "finding", "change", "verification", "audit":
		return "application/json"
	case "output", "report", "summary":
		return "text/markdown"
	default:
		return "text/plain"
	}
}

func firstAgentRunArtifactValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstAgentRunArtifactInt(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func copyAgentRunArtifacts(artifacts []AgentRunArtifactSnapshot) []AgentRunArtifactSnapshot {
	return copySessionArtifacts(artifacts)
}

func copyAgentRunResumeContext(context AgentRunResumeContextSnapshot) AgentRunResumeContextSnapshot {
	if context.MatchedSkill != nil {
		copied := copyAgentRunSkill(*context.MatchedSkill)
		context.MatchedSkill = &copied
	}
	context.Messages = copyAgentRunMessages(context.Messages)
	context.SuspendedCalls = copyAgentRunToolCalls(context.SuspendedCalls)
	context.CollectedResults = append([]schema.ToolResult(nil), context.CollectedResults...)
	return context
}

func copyAgentRunMessages(messages []schema.Message) []schema.Message {
	if len(messages) == 0 {
		return nil
	}
	copied := make([]schema.Message, len(messages))
	for i, message := range messages {
		copied[i] = message
		if message.ToolCall != nil {
			call := copyAgentRunToolCall(*message.ToolCall)
			copied[i].ToolCall = &call
		}
		copied[i].ToolCalls = copyAgentRunToolCalls(message.ToolCalls)
	}
	return copied
}

func copyAgentRunToolCalls(calls []schema.ToolCall) []schema.ToolCall {
	if len(calls) == 0 {
		return nil
	}
	copied := make([]schema.ToolCall, len(calls))
	for i, call := range calls {
		copied[i] = copyAgentRunToolCall(call)
	}
	return copied
}

func copyAgentRunToolCall(call schema.ToolCall) schema.ToolCall {
	call.Arguments = append([]byte(nil), call.Arguments...)
	return call
}

func copyAgentRunSkill(skill schema.Skill) schema.Skill {
	skill.Tools = append([]schema.SkillTool(nil), skill.Tools...)
	skill.Params = append([]schema.SkillParam(nil), skill.Params...)
	skill.Resources = append([]schema.SkillResource(nil), skill.Resources...)
	skill.AllowedToolKinds = append([]string(nil), skill.AllowedToolKinds...)
	skill.NextSkills = append([]string(nil), skill.NextSkills...)
	if len(skill.Metadata) > 0 {
		copied := make(map[string]string, len(skill.Metadata))
		for key, value := range skill.Metadata {
			copied[key] = value
		}
		skill.Metadata = copied
	}
	return skill
}

func appendBoundedAgentRunEvents(events []AgentRunEventSnapshot, event AgentRunEventSnapshot) []AgentRunEventSnapshot {
	events = append(events, event)
	if len(events) <= maxAgentRunEvents {
		return events
	}
	return append([]AgentRunEventSnapshot(nil), events[len(events)-maxAgentRunEvents:]...)
}

func appendAgentRunEventLocked(events []AgentRunEventSnapshot, event AgentRunEventSnapshot) []AgentRunEventSnapshot {
	if event.Seq <= 0 {
		event.Seq = nextAgentRunEventSeq(events)
	}
	return appendBoundedAgentRunEvents(events, event)
}

func nextAgentRunEventSeq(events []AgentRunEventSnapshot) int {
	maxSeq := 0
	for index, event := range events {
		seq := event.Seq
		if seq <= 0 {
			seq = index + 1
		}
		if seq > maxSeq {
			maxSeq = seq
		}
	}
	return maxSeq + 1
}

func assignAgentRunEventSeqs(events []AgentRunEventSnapshot) {
	next := 1
	for index := range events {
		if events[index].Seq > 0 {
			if events[index].Seq >= next {
				next = events[index].Seq + 1
			}
			continue
		}
		events[index].Seq = next
		next++
	}
}
