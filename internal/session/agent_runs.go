package session

import (
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
	AgentID          string              `json:"agent_id,omitempty"`
	Mode             string              `json:"mode,omitempty"`
	SystemPrompt     string              `json:"system_prompt,omitempty"`
	MatchedSkill     *schema.Skill       `json:"matched_skill,omitempty"`
	Messages         []schema.Message    `json:"messages,omitempty"`
	SuspendedCalls   []schema.ToolCall   `json:"suspended_calls,omitempty"`
	CollectedResults []schema.ToolResult `json:"collected_results,omitempty"`
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
}

// AgentRunEventSnapshot captures a replayable ordinary agent stream/status event.
type AgentRunEventSnapshot struct {
	Seq              int                     `json:"seq,omitempty"`
	At               string                  `json:"at,omitempty"`
	Type             string                  `json:"type,omitempty"`
	Content          string                  `json:"content,omitempty"`
	ToolName         string                  `json:"tool_name,omitempty"`
	ToolCallID       string                  `json:"tool_call_id,omitempty"`
	ArgumentsSummary string                  `json:"arguments_summary,omitempty"`
	AgentID          string                  `json:"agent_id,omitempty"`
	Mode             string                  `json:"mode,omitempty"`
	IsError          bool                    `json:"is_error,omitempty"`
	NeedsAction      bool                    `json:"needs_action,omitempty"`
	Suspended        bool                    `json:"suspended,omitempty"`
	TaskStage        string                  `json:"task_stage,omitempty"`
	PromptTokens     int                     `json:"prompt_tokens,omitempty"`
	OutputTokens     int                     `json:"output_tokens,omitempty"`
	CachedTokens     int                     `json:"cached_tokens,omitempty"`
	PromptBudget     *schema.PromptBudget    `json:"prompt_budget,omitempty"`
	Risk             *schema.ToolRiskProfile `json:"risk,omitempty"`
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
	run.Events = appendAgentRunEventLocked(run.Events, AgentRunEventSnapshot{
		At:      now,
		Type:    "agent_run_cancel_requested",
		Content: trimAgentRunText(reason),
		AgentID: run.AgentID,
		Mode:    run.Mode,
	})
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
	run.Events = appendAgentRunEventLocked(run.Events, AgentRunEventSnapshot{
		At:      now,
		Type:    "agent_run_cancelled",
		Content: trimAgentRunText(reason),
		AgentID: run.AgentID,
		Mode:    run.Mode,
	})
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
	run.Events = appendAgentRunEventLocked(run.Events, AgentRunEventSnapshot{
		At:      now,
		Type:    eventType,
		Content: trimAgentRunText(result.Output),
		AgentID: run.AgentID,
		Mode:    run.Mode,
	})
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
	run.Events = appendAgentRunEventLocked(run.Events, AgentRunEventSnapshot{
		At:      now,
		Type:    string(schema.StreamEventError),
		Content: trimAgentRunText(message),
		AgentID: run.AgentID,
		Mode:    run.Mode,
		IsError: true,
	})
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
	event.Content = trimAgentRunText(event.Content)
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
	copied := copyAgentRunResumeContext(context)
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
	defer s.mu.RUnlock()
	for _, run := range s.agentRuns {
		if run.ID == id {
			return copyAgentRunSnapshot(run), true
		}
	}
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
