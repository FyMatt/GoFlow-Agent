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
	LocalTargetID     = "local"
)

// AgentRunStartOptions carries metadata for a new ordinary agent run.
type AgentRunStartOptions struct {
	RetryOf         string
	AgentID         string
	TargetID        string
	ScheduleID      string
	ScheduleJobID   string
	ResourcePins    []ResourcePin
	Attachments     []schema.AttachmentRef
	ReasoningEffort string
	PermissionMode  string
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

// AgentTaskApprovalScopeSnapshot records the bounded local capabilities an
// operator granted to one ordinary agent run. The scope is valid only while
// that exact run is active and its agent, workspace, and target still match.
type AgentTaskApprovalScopeSnapshot struct {
	Version                 int    `json:"version,omitempty"`
	AgentID                 string `json:"agent_id,omitempty"`
	WorkspaceRoot           string `json:"workspace_root,omitempty"`
	TargetID                string `json:"target_id,omitempty"`
	AllowWorkspaceWrites    bool   `json:"allow_workspace_writes,omitempty"`
	AllowValidationCommands bool   `json:"allow_validation_commands,omitempty"`
	GrantedByCallID         string `json:"granted_by_call_id,omitempty"`
	GrantedAt               string `json:"granted_at,omitempty"`
}

// AgentRunSnapshot is a durable, replayable view of one ordinary agent turn.
type AgentRunSnapshot struct {
	ID               string                          `json:"id"`
	Status           string                          `json:"status,omitempty"`
	Request          string                          `json:"request,omitempty"`
	Output           string                          `json:"output,omitempty"`
	Error            string                          `json:"error,omitempty"`
	AgentID          string                          `json:"agent_id,omitempty"`
	TargetID         string                          `json:"target_id,omitempty"`
	Mode             string                          `json:"mode,omitempty"`
	StartedAt        string                          `json:"started_at,omitempty"`
	UpdatedAt        string                          `json:"updated_at,omitempty"`
	CompletedAt      string                          `json:"completed_at,omitempty"`
	CancelledAt      string                          `json:"cancelled_at,omitempty"`
	RetryOf          string                          `json:"retry_of,omitempty"`
	Attempt          int                             `json:"attempt,omitempty"`
	ScheduleID       string                          `json:"schedule_id,omitempty"`
	ScheduleJobID    string                          `json:"schedule_job_id,omitempty"`
	ResourcePins     []ResourcePin                   `json:"resource_pins,omitempty"`
	Attachments      []schema.AttachmentRef          `json:"attachments,omitempty"`
	ReasoningEffort  string                          `json:"reasoning_effort,omitempty"`
	PermissionMode   string                          `json:"permission_mode,omitempty"`
	PendingCallID    string                          `json:"pending_call_id,omitempty"`
	PendingTool      string                          `json:"pending_tool_name,omitempty"`
	PendingAgentID   string                          `json:"pending_agent_id,omitempty"`
	PendingApprovals []PendingApprovalSnapshot       `json:"pending_approvals,omitempty"`
	ResumeContext    *AgentRunResumeContextSnapshot  `json:"resume_context,omitempty"`
	TaskApproval     *AgentTaskApprovalScopeSnapshot `json:"task_approval,omitempty"`
	Result           *schema.AgentResult             `json:"result,omitempty"`
	Events           []AgentRunEventSnapshot         `json:"events,omitempty"`
	UsageRecords     []UsageRecord                   `json:"usage_records,omitempty"`
	Artifacts        []AgentRunArtifactSnapshot      `json:"artifacts,omitempty"`
	EventsCount      int                             `json:"events_count,omitempty"`
	ArtifactsCount   int                             `json:"artifacts_count,omitempty"`
	DiffsCount       int                             `json:"diffs_count,omitempty"`
}

// SetAgentRunTaskApproval stores a bounded task authorization on an active
// ordinary run. Callers remain responsible for validating the workspace and
// tool capability before constructing the scope.
func (s *State) SetAgentRunTaskApproval(runID string, scope AgentTaskApprovalScopeSnapshot) (AgentRunSnapshot, bool) {
	if s == nil || strings.TrimSpace(runID) == "" {
		return AgentRunSnapshot{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	index := s.agentRunIndexLocked(runID)
	if index < 0 || isTerminalAgentRunStatus(s.agentRuns[index].Status) {
		return AgentRunSnapshot{}, false
	}
	scope.Version = 1
	scope.AgentID = strings.TrimSpace(scope.AgentID)
	scope.WorkspaceRoot = strings.TrimSpace(scope.WorkspaceRoot)
	scope.TargetID = normalizeRunTargetID(scope.TargetID)
	scope.GrantedByCallID = strings.TrimSpace(scope.GrantedByCallID)
	if strings.TrimSpace(scope.GrantedAt) == "" {
		scope.GrantedAt = workflowRunTimestamp(time.Now().UTC())
	}
	run := s.agentRuns[index]
	run.TaskApproval = &scope
	run.UpdatedAt = workflowRunTimestamp(time.Now().UTC())
	s.agentRuns[index] = run
	return copyAgentRunSnapshot(run), true
}

// ClearAgentRunTaskApproval removes a task authorization that could not be
// durably committed. Terminal runs keep their historical grant for auditing,
// but execution code never treats a terminal grant as active.
func (s *State) ClearAgentRunTaskApproval(runID string) {
	if s == nil || strings.TrimSpace(runID) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	index := s.agentRunIndexLocked(runID)
	if index < 0 {
		return
	}
	run := s.agentRuns[index]
	run.TaskApproval = nil
	run.UpdatedAt = workflowRunTimestamp(time.Now().UTC())
	s.agentRuns[index] = run
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
	DurationMs          int64                   `json:"duration_ms,omitempty"`
	ToolResultSummary   string                  `json:"tool_result_summary,omitempty"`
	ArtifactRef         string                  `json:"artifact_ref,omitempty"`
	ArtifactHash        string                  `json:"artifact_hash,omitempty"`
	ToolResultBytes     int                     `json:"tool_result_content_bytes,omitempty"`
	ToolResultStored    int                     `json:"tool_result_stored_bytes,omitempty"`
	ToolResultTruncated bool                    `json:"tool_result_truncated,omitempty"`
	ArtifactRetention   string                  `json:"artifact_retention,omitempty"`
	Redaction           string                  `json:"redaction,omitempty"`
	ArgumentsSummary    string                  `json:"arguments_summary,omitempty"`
	ArgumentsHash       string                  `json:"arguments_hash,omitempty"`
	AgentID             string                  `json:"agent_id,omitempty"`
	Mode                string                  `json:"mode,omitempty"`
	IsError             bool                    `json:"is_error,omitempty"`
	NeedsAction         bool                    `json:"needs_action,omitempty"`
	Suspended           bool                    `json:"suspended,omitempty"`
	TaskStage           string                  `json:"task_stage,omitempty"`
	PromptTokens        int                     `json:"prompt_tokens,omitempty"`
	OutputTokens        int                     `json:"output_tokens,omitempty"`
	CachedTokens        int                     `json:"cached_tokens,omitempty"`
	OutputKind          string                  `json:"output_kind,omitempty"`
	CacheHitRate        float64                 `json:"cache_hit_rate,omitempty"`
	Reason              string                  `json:"reason,omitempty"`
	Severity            string                  `json:"severity,omitempty"`
	BudgetScope         string                  `json:"budget_scope,omitempty"`
	ContractCheck       string                  `json:"contract_check,omitempty"`
	SourceRef           string                  `json:"source_ref,omitempty"`
	StopReason          string                  `json:"stop_reason,omitempty"`
	ContinuationCount   int                     `json:"continuation_count,omitempty"`
	Incomplete          bool                    `json:"incomplete,omitempty"`
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
		ID:              id,
		Status:          "running",
		Request:         trimAgentRunText(request),
		AgentID:         strings.TrimSpace(opts.AgentID),
		TargetID:        normalizeRunTargetID(opts.TargetID),
		StartedAt:       timestamp,
		UpdatedAt:       timestamp,
		RetryOf:         retryOf,
		Attempt:         attempt,
		ScheduleID:      strings.TrimSpace(opts.ScheduleID),
		ScheduleJobID:   strings.TrimSpace(opts.ScheduleJobID),
		ResourcePins:    CopyResourcePins(opts.ResourcePins),
		Attachments:     append([]schema.AttachmentRef(nil), opts.Attachments...),
		ReasoningEffort: strings.TrimSpace(opts.ReasoningEffort),
		PermissionMode:  strings.TrimSpace(opts.PermissionMode),
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

// StartAgentRunWithOptionsDurable persists a new ordinary run before exposing
// its id to a background executor.
func (s *State) StartAgentRunWithOptionsDurable(request string, opts AgentRunStartOptions) (string, error) {
	if s == nil {
		return "", nil
	}
	s.workflowPersistMu.Lock()
	defer s.workflowPersistMu.Unlock()
	id := s.StartAgentRunWithOptions(request, opts)
	if id == "" {
		return "", nil
	}
	return id, s.PersistConfigured()
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

// RequestAgentRunCancelDurable keeps durable API readers behind the persisted
// cancellation transition. This prevents shutdown from observing the new
// in-memory state while session.json still contains the previous state.
func (s *State) RequestAgentRunCancelDurable(runID, reason string) (AgentRunSnapshot, bool, error) {
	if s == nil {
		return AgentRunSnapshot{}, false, nil
	}
	s.workflowPersistMu.Lock()
	defer s.workflowPersistMu.Unlock()
	run, ok := s.RequestAgentRunCancel(runID, reason)
	if !ok {
		return run, false, nil
	}
	return run, true, s.PersistConfigured()
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

// CancelAgentRunDurable persists cancellation before durable API readers can
// observe the terminal state.
func (s *State) CancelAgentRunDurable(runID, reason string) (AgentRunSnapshot, bool, error) {
	if s == nil {
		return AgentRunSnapshot{}, false, nil
	}
	s.workflowPersistMu.Lock()
	defer s.workflowPersistMu.Unlock()
	run, ok := s.CancelAgentRun(runID, reason)
	if !ok {
		return run, false, nil
	}
	return run, true, s.PersistConfigured()
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
	if !strings.EqualFold(status, "awaiting_tool_approval") && !strings.EqualFold(status, "paused_need_more_budget") {
		run.PendingCallID = ""
		run.PendingTool = ""
		run.PendingAgentID = ""
		run.PendingApprovals = nil
		run.ResumeContext = nil
	}
	run.Artifacts = s.buildAgentRunArtifactsLocked(run, now)
	eventType := "agent_run_completed"
	if !isTerminalAgentRunStatus(status) {
		eventType = "agent_run_paused"
	}
	run.Events = appendAgentRunEventLocked(run.Events, s.normalizeAgentRunEventLocked(run.ID, AgentRunEventSnapshot{
		At:                now,
		Type:              eventType,
		Content:           trimAgentRunText(result.Output),
		AgentID:           run.AgentID,
		Mode:              run.Mode,
		NeedsAction:       result.Incomplete,
		Reason:            result.IncompleteReason,
		Severity:          agentRunResultSeverity(result),
		BudgetScope:       agentRunResultBudgetScope(result),
		StopReason:        result.StopReason,
		ContinuationCount: result.ContinuationCount,
		Incomplete:        result.Incomplete,
	}))
	s.agentRuns[index] = run
	return copyAgentRunSnapshot(run), true
}

// CompleteAgentRunDurable persists completion or suspension before durable API
// readers can observe the transition.
func (s *State) CompleteAgentRunDurable(runID, status string, result schema.AgentResult) (AgentRunSnapshot, bool, error) {
	if s == nil {
		return AgentRunSnapshot{}, false, nil
	}
	s.workflowPersistMu.Lock()
	defer s.workflowPersistMu.Unlock()
	run, ok := s.CompleteAgentRun(runID, status, result)
	if !ok {
		return run, false, nil
	}
	return run, true, s.PersistConfigured()
}

func (s *State) ResumeAgentRunDurable(runID, reason string) (AgentRunSnapshot, bool, error) {
	if s == nil || strings.TrimSpace(runID) == "" {
		return AgentRunSnapshot{}, false, nil
	}
	s.workflowPersistMu.Lock()
	defer s.workflowPersistMu.Unlock()
	now := workflowRunTimestamp(time.Now().UTC())
	s.mu.Lock()
	index := s.agentRunIndexLocked(runID)
	if index < 0 || !strings.EqualFold(strings.TrimSpace(s.agentRuns[index].Status), "paused_need_more_budget") {
		s.mu.Unlock()
		return AgentRunSnapshot{}, false, nil
	}
	run := s.agentRuns[index]
	run.Status = "running"
	run.UpdatedAt = now
	run.CompletedAt = ""
	run.Events = appendAgentRunEventLocked(run.Events, s.normalizeAgentRunEventLocked(run.ID, AgentRunEventSnapshot{
		At:          now,
		Type:        "agent_run_continuation_started",
		Content:     strings.TrimSpace(reason),
		NeedsAction: false,
		Reason:      "continue_output",
	}))
	s.agentRuns[index] = run
	result := copyAgentRunSnapshot(run)
	s.mu.Unlock()
	return result, true, s.PersistConfigured()
}

// ReconcileInterruptedAgentRuns converts process-owned ordinary Agent work
// into a durable state after restart. Runs with saved conversation state can
// continue in place; runs without it must be retried from the original request.
func (s *State) ReconcileInterruptedAgentRuns() (int, error) {
	if s == nil {
		return 0, nil
	}
	s.workflowPersistMu.Lock()
	defer s.workflowPersistMu.Unlock()
	now := workflowRunTimestamp(time.Now().UTC())
	count := 0
	s.mu.Lock()
	for i := range s.agentRuns {
		run := s.agentRuns[i]
		if !strings.EqualFold(strings.TrimSpace(run.Status), "running") {
			continue
		}
		resumable := run.ResumeContext != nil &&
			(run.ResumeContext.MessagesCount > 0 || len(run.ResumeContext.Messages) > 0) &&
			(run.ResumeContext.CollectedResultCount > 0 || len(run.ResumeContext.CollectedResults) > 0)
		content := "智能体运行因进程重启中断；请从原请求重新运行"
		eventType := "agent_run_failed"
		if resumable {
			run.Status = "paused_need_more_budget"
			run.CompletedAt = ""
			content = "智能体运行因进程重启中断；已保留对话和工具结果，可继续生成"
			eventType = "agent_run_paused"
		} else {
			run.Status = "failed"
			run.CompletedAt = now
			run.Error = content
			run.ResumeContext = nil
		}
		run.UpdatedAt = now
		run.Events = appendAgentRunEventLocked(run.Events, s.normalizeAgentRunEventLocked(run.ID, AgentRunEventSnapshot{
			At:          now,
			Type:        eventType,
			Content:     content,
			AgentID:     run.AgentID,
			Mode:        run.Mode,
			IsError:     !resumable,
			NeedsAction: true,
			Suspended:   resumable,
			Reason:      "process_restart",
			Severity:    "warning",
			Incomplete:  true,
		}))
		s.agentRuns[i] = run
		count++
	}
	s.mu.Unlock()
	if count == 0 {
		return 0, nil
	}
	return count, s.PersistConfigured()
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

// FailAgentRunDurable persists failure before durable API readers can observe
// the terminal state.
func (s *State) FailAgentRunDurable(runID, message string) (AgentRunSnapshot, bool, error) {
	if s == nil {
		return AgentRunSnapshot{}, false, nil
	}
	s.workflowPersistMu.Lock()
	defer s.workflowPersistMu.Unlock()
	run, ok := s.FailAgentRun(runID, message)
	if !ok {
		return run, false, nil
	}
	return run, true, s.PersistConfigured()
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
	// Approval events are recorded immediately, but the actionable state is
	// published by SetAgentRunResumeContext after the suspended conversation
	// has been persisted. Exposing it here creates a retry/approval race.
	event = s.normalizeAgentRunEventLocked(runID, event)
	if strings.EqualFold(event.Type, "token_usage") {
		if run.UsageRecords == nil {
			run.UsageRecords = append([]UsageRecord(nil), AgentUsageRecords(run)...)
		}
		if event.Seq == 0 {
			event.Seq = nextAgentRunEventSeq(run.Events)
		}
		agentID := event.AgentID
		if strings.TrimSpace(agentID) == "" {
			agentID = run.AgentID
		}
		record := UsageRecord{RunID: run.ID, RunType: "agent", Seq: event.Seq, At: event.At, AgentID: agentID, Stage: event.TaskStage, PromptTokens: event.PromptTokens, OutputTokens: event.OutputTokens, CachedTokens: event.CachedTokens}
		run.UsageRecords = append(run.UsageRecords, record)
		s.usageRecords = append(s.usageRecords, record)
	}
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
	if isTerminalAgentRunStatus(run.Status) {
		return
	}
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
	if isTerminalAgentRunStatus(run.Status) {
		return copyAgentRunSnapshot(run), false
	}
	copied := s.normalizeAgentRunResumeContextLocked(runID, context)
	run.ResumeContext = &copied
	if len(copied.SuspendedCalls) > 0 {
		run.Status = "awaiting_tool_approval"
	}
	run.UpdatedAt = workflowRunTimestamp(time.Now().UTC())
	s.agentRuns[index] = run
	return copyAgentRunSnapshot(run), true
}

// MarkAgentRunApprovalResuming hides the resolved approval while its saved
// conversation is continuing. The resume context stays durable until the run
// either publishes a new approval or reaches a terminal state.
func (s *State) MarkAgentRunApprovalResuming(runID string) {
	if s == nil || strings.TrimSpace(runID) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	index := s.agentRunIndexLocked(runID)
	if index < 0 {
		return
	}
	run := s.agentRuns[index]
	if isTerminalAgentRunStatus(run.Status) {
		return
	}
	run.Status = "running"
	run.PendingCallID = ""
	run.PendingTool = ""
	run.PendingAgentID = ""
	run.PendingApprovals = nil
	run.UpdatedAt = workflowRunTimestamp(time.Now().UTC())
	s.agentRuns[index] = run
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
	runs := copyAgentRunSnapshots(s.agentRuns)
	s.mu.RUnlock()
	for i := range runs {
		runs[i] = normalizeAgentRunApprovalState(s.HydrateAgentRun(runs[i]))
	}
	return runs
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
			return normalizeAgentRunApprovalState(s.HydrateAgentRun(copied)), true
		}
	}
	s.mu.RUnlock()
	return AgentRunSnapshot{}, false
}

// normalizeAgentRunApprovalState repairs snapshots written between persisting
// the ordinary resume context and persisting the matching approval metadata.
// Suspended calls are the durable source of truth: retrying them as a new run
// could duplicate workspace changes made earlier in the same turn.
func normalizeAgentRunApprovalState(run AgentRunSnapshot) AgentRunSnapshot {
	if run.ResumeContext == nil || len(run.ResumeContext.SuspendedCalls) == 0 || !strings.EqualFold(strings.TrimSpace(run.Status), "awaiting_tool_approval") {
		return run
	}
	agentID := strings.TrimSpace(run.ResumeContext.AgentID)
	if agentID == "" {
		agentID = strings.TrimSpace(run.AgentID)
	}
	existing := make(map[string]struct{}, len(run.PendingApprovals))
	for _, approval := range run.PendingApprovals {
		if callID := strings.TrimSpace(approval.CallID); callID != "" {
			existing[callID] = struct{}{}
		}
	}
	for _, call := range run.ResumeContext.SuspendedCalls {
		callID := strings.TrimSpace(call.ID)
		if callID == "" {
			continue
		}
		if _, ok := existing[callID]; !ok {
			run.PendingApprovals = append(run.PendingApprovals, PendingApprovalSnapshot{
				CallID:     callID,
				ToolName:   strings.TrimSpace(call.Name),
				AgentID:    agentID,
				Arguments:  string(call.Arguments),
				AgentRunID: strings.TrimSpace(run.ID),
			})
			existing[callID] = struct{}{}
		}
		run.PendingCallID = callID
		run.PendingTool = strings.TrimSpace(call.Name)
		run.PendingAgentID = agentID
	}
	if strings.TrimSpace(run.PendingCallID) != "" {
		run.Status = "awaiting_tool_approval"
	}
	return run
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

func agentRunResultSeverity(result schema.AgentResult) string {
	if result.Incomplete {
		return "warning"
	}
	return ""
}

func agentRunResultBudgetScope(result schema.AgentResult) string {
	if result.Incomplete {
		return "output"
	}
	return ""
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
	run.UsageRecords = append([]UsageRecord(nil), AgentUsageRecords(run)...)
	run.TargetID = normalizeRunTargetID(run.TargetID)
	run.ResourcePins = CopyResourcePins(run.ResourcePins)
	run.Attachments = append([]schema.AttachmentRef(nil), run.Attachments...)
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
	if run.TaskApproval != nil {
		copied := *run.TaskApproval
		run.TaskApproval = &copied
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
			Title:     "最终输出",
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
			Title:     "运行错误",
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
		if !publishToolResultArtifact(toolResult) {
			continue
		}
		title := firstAgentRunArtifactValue(toolResult.ToolName, toolResult.CallID, "工具结果")
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
		if !publishStructuredArtifact(section.Kind) {
			continue
		}
		content := agentRunArtifactJSON(section)
		summary := firstAgentRunArtifactValue(section.Summary, strings.Join(section.Items, "\n"), content)
		items = append(items, AgentRunArtifactSnapshot{
			ID:        agentRunArtifactIndexedID("structured", i+1, section.Title, section.Kind),
			Kind:      firstAgentRunArtifactValue(section.Kind, "structured"),
			Title:     firstAgentRunArtifactValue(section.Title, section.Kind, "结构化输出"),
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
			Title:     firstAgentRunArtifactValue(finding.Severity, "问题发现"),
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
			Title:     "变更",
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
		if !publishVerificationArtifact(verification.Kind) {
			continue
		}
		failed := strings.EqualFold(verification.Status, "failed") || strings.EqualFold(verification.Status, "fail")
		items = append(items, AgentRunArtifactSnapshot{
			ID:        agentRunArtifactIndexedID("verification", i+1, verification.Kind, verification.Status),
			Kind:      "verification",
			Title:     firstAgentRunArtifactValue(verification.Kind, "验证"),
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
		if !publishAuditArtifact(audit.Outcome) {
			continue
		}
		isError := true
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
	return schema.CopyMessages(messages)
}

func copyAgentRunToolCalls(calls []schema.ToolCall) []schema.ToolCall {
	return schema.CopyToolCalls(calls)
}

func copyAgentRunToolCall(call schema.ToolCall) schema.ToolCall {
	return schema.CopyToolCall(call)
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
