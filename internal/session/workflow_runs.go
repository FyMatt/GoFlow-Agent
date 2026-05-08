package session

import (
	"fmt"
	"strings"
	"time"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

const (
	maxWorkflowRuns      = 20
	maxWorkflowRunEvents = 200
	maxWorkflowRunText   = 8192
)

// WorkflowRunStartOptions carries metadata for a new workflow run.
type WorkflowRunStartOptions struct {
	RetryOf string
}

// StartWorkflowRun creates a bounded, session-persisted workflow run record.
func (s *State) StartWorkflowRun(name, request string) string {
	return s.StartWorkflowRunWithOptions(name, request, WorkflowRunStartOptions{})
}

// StartWorkflowRunWithOptions creates a bounded run record with retry metadata.
func (s *State) StartWorkflowRunWithOptions(name, request string, opts WorkflowRunStartOptions) string {
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
		if index := s.workflowRunIndexLocked(retryOf); index >= 0 && s.workflowRuns[index].Attempt > 0 {
			attempt = s.workflowRuns[index].Attempt + 1
		}
	}
	id := uniqueWorkflowRunID(s.workflowRuns, name, now)
	run := WorkflowRunSnapshot{
		ID:        id,
		Name:      strings.TrimSpace(name),
		Status:    "running",
		Request:   trimWorkflowRunText(request),
		StartedAt: timestamp,
		UpdatedAt: timestamp,
		RetryOf:   retryOf,
		Attempt:   attempt,
		Events: []WorkflowRunEventSnapshot{{
			Seq:            1,
			At:             timestamp,
			Type:           "workflow_started",
			Content:        workflowRunStartContent(retryOf),
			WorkflowName:   strings.TrimSpace(name),
			WorkflowStatus: "running",
		}},
	}
	s.workflowRuns = append([]WorkflowRunSnapshot{run}, s.workflowRuns...)
	if len(s.workflowRuns) > maxWorkflowRuns {
		s.workflowRuns = append([]WorkflowRunSnapshot(nil), s.workflowRuns[:maxWorkflowRuns]...)
	}
	s.workflow = WorkflowSnapshot{
		RunID:   id,
		Name:    run.Name,
		Status:  run.Status,
		Request: run.Request,
	}
	return id
}

// StartWorkflowRunFromSnapshot attaches a run id to an already-suspended workflow snapshot.
func (s *State) StartWorkflowRunFromSnapshot(snapshot WorkflowSnapshot) string {
	if s == nil {
		return ""
	}
	if strings.TrimSpace(snapshot.RunID) != "" {
		return strings.TrimSpace(snapshot.RunID)
	}
	now := time.Now().UTC()
	timestamp := workflowRunTimestamp(now)
	s.mu.Lock()
	defer s.mu.Unlock()

	id := uniqueWorkflowRunID(s.workflowRuns, snapshot.Name, now)
	snapshot.RunID = id
	if strings.TrimSpace(snapshot.Status) == "" {
		snapshot.Status = "running"
	}
	run := WorkflowRunSnapshot{
		ID:                       id,
		Name:                     strings.TrimSpace(snapshot.Name),
		Status:                   strings.TrimSpace(snapshot.Status),
		Request:                  trimWorkflowRunText(snapshot.Request),
		StartedAt:                timestamp,
		UpdatedAt:                timestamp,
		Attempt:                  1,
		NextStage:                strings.TrimSpace(snapshot.NextStage),
		Summary:                  trimWorkflowRunText(snapshot.Summary),
		ApprovalPrompt:           trimWorkflowRunText(snapshot.LastApproval),
		PendingCallID:            strings.TrimSpace(snapshot.PendingCallID),
		PendingToolName:          strings.TrimSpace(snapshot.PendingToolName),
		PendingAgentID:           strings.TrimSpace(snapshot.PendingAgentID),
		PendingArgs:              trimWorkflowRunText(snapshot.PendingArguments),
		PendingSubWorkflowName:   strings.TrimSpace(snapshot.PendingSubWorkflowName),
		PendingSubWorkflowRunID:  strings.TrimSpace(snapshot.PendingSubWorkflowRunID),
		PendingSubWorkflowStatus: strings.TrimSpace(snapshot.PendingSubWorkflowStatus),
	}
	s.workflowRuns = append([]WorkflowRunSnapshot{run}, s.workflowRuns...)
	if len(s.workflowRuns) > maxWorkflowRuns {
		s.workflowRuns = append([]WorkflowRunSnapshot(nil), s.workflowRuns[:maxWorkflowRuns]...)
	}
	s.workflow = snapshot
	return id
}

// RequestWorkflowRunCancel marks a running workflow as cancellation-requested.
func (s *State) RequestWorkflowRunCancel(runID, reason string) (WorkflowRunSnapshot, bool) {
	if s == nil || strings.TrimSpace(runID) == "" {
		return WorkflowRunSnapshot{}, false
	}
	now := workflowRunTimestamp(time.Now().UTC())
	s.mu.Lock()
	defer s.mu.Unlock()
	index := s.workflowRunIndexLocked(runID)
	if index < 0 {
		return WorkflowRunSnapshot{}, false
	}
	run := s.workflowRuns[index]
	if !isTerminalWorkflowStatus(run.Status) {
		run.Status = "cancelling"
	}
	run.UpdatedAt = now
	run.Events = appendWorkflowRunEventLocked(run.Events, WorkflowRunEventSnapshot{
		At:             now,
		Type:           "workflow_cancel_requested",
		Content:        trimWorkflowRunText(reason),
		WorkflowName:   run.Name,
		WorkflowStatus: run.Status,
		NextStage:      run.NextStage,
	})
	s.workflowRuns[index] = run
	if s.workflow.RunID == run.ID {
		s.workflow.Status = run.Status
		s.workflow.Summary = trimWorkflowRunText(reason)
	}
	return copyWorkflowRunSnapshot(run), true
}

// CancelWorkflowRun marks a workflow run as cancelled.
func (s *State) CancelWorkflowRun(runID, reason string) (WorkflowRunSnapshot, bool) {
	if s == nil || strings.TrimSpace(runID) == "" {
		return WorkflowRunSnapshot{}, false
	}
	now := workflowRunTimestamp(time.Now().UTC())
	s.mu.Lock()
	defer s.mu.Unlock()
	index := s.workflowRunIndexLocked(runID)
	if index < 0 {
		return WorkflowRunSnapshot{}, false
	}
	run := s.workflowRuns[index]
	run.Status = "cancelled"
	run.UpdatedAt = now
	run.CompletedAt = now
	run.CancelledAt = now
	run.PendingCallID = ""
	run.PendingToolName = ""
	run.PendingAgentID = ""
	run.PendingArgs = ""
	run.PendingSubWorkflowName = ""
	run.PendingSubWorkflowRunID = ""
	run.PendingSubWorkflowStatus = ""
	run.ApprovalPrompt = ""
	run.PendingFields = nil
	if strings.TrimSpace(reason) != "" {
		run.Summary = trimWorkflowRunText(reason)
	}
	run.Events = appendWorkflowRunEventLocked(run.Events, WorkflowRunEventSnapshot{
		At:             now,
		Type:           "workflow_cancelled",
		Content:        trimWorkflowRunText(reason),
		WorkflowName:   run.Name,
		WorkflowStatus: run.Status,
		NextStage:      run.NextStage,
	})
	s.workflowRuns[index] = run
	if s.workflow.RunID == run.ID {
		s.workflow.Status = run.Status
		s.workflow.Summary = run.Summary
		s.workflow.NextStage = run.NextStage
		s.workflow.LastApproval = ""
		s.workflow.PendingCallID = ""
		s.workflow.PendingToolName = ""
		s.workflow.PendingAgentID = ""
		s.workflow.PendingArguments = ""
		s.workflow.PendingSubWorkflowName = ""
		s.workflow.PendingSubWorkflowRunID = ""
		s.workflow.PendingSubWorkflowStatus = ""
	}
	return copyWorkflowRunSnapshot(run), true
}

// UpdateWorkflowRunState merges the current workflow snapshot into its run record.
func (s *State) UpdateWorkflowRunState(snapshot WorkflowSnapshot) {
	if s == nil || strings.TrimSpace(snapshot.RunID) == "" {
		return
	}
	now := workflowRunTimestamp(time.Now().UTC())
	s.mu.Lock()
	defer s.mu.Unlock()
	index := s.workflowRunIndexLocked(snapshot.RunID)
	if index < 0 {
		s.workflowRuns = append([]WorkflowRunSnapshot{{
			ID:        strings.TrimSpace(snapshot.RunID),
			Name:      strings.TrimSpace(snapshot.Name),
			Status:    strings.TrimSpace(snapshot.Status),
			Request:   trimWorkflowRunText(snapshot.Request),
			StartedAt: now,
			UpdatedAt: now,
		}}, s.workflowRuns...)
		if len(s.workflowRuns) > maxWorkflowRuns {
			s.workflowRuns = append([]WorkflowRunSnapshot(nil), s.workflowRuns[:maxWorkflowRuns]...)
		}
		index = 0
	}
	run := s.workflowRuns[index]
	if strings.TrimSpace(snapshot.Name) != "" {
		run.Name = strings.TrimSpace(snapshot.Name)
	}
	if strings.TrimSpace(snapshot.Status) != "" {
		run.Status = strings.TrimSpace(snapshot.Status)
	}
	if strings.TrimSpace(snapshot.Request) != "" {
		run.Request = trimWorkflowRunText(snapshot.Request)
	}
	run.UpdatedAt = now
	run.NextStage = strings.TrimSpace(snapshot.NextStage)
	run.Summary = trimWorkflowRunText(snapshot.Summary)
	run.ApprovalPrompt = trimWorkflowRunText(snapshot.LastApproval)
	run.PendingCallID = strings.TrimSpace(snapshot.PendingCallID)
	run.PendingToolName = strings.TrimSpace(snapshot.PendingToolName)
	run.PendingAgentID = strings.TrimSpace(snapshot.PendingAgentID)
	run.PendingArgs = trimWorkflowRunText(snapshot.PendingArguments)
	run.PendingSubWorkflowName = strings.TrimSpace(snapshot.PendingSubWorkflowName)
	run.PendingSubWorkflowRunID = strings.TrimSpace(snapshot.PendingSubWorkflowRunID)
	run.PendingSubWorkflowStatus = strings.TrimSpace(snapshot.PendingSubWorkflowStatus)
	if isTerminalWorkflowStatus(run.Status) && strings.TrimSpace(run.CompletedAt) == "" {
		run.CompletedAt = now
	}
	if strings.EqualFold(run.Status, "cancelled") && strings.TrimSpace(run.CancelledAt) == "" {
		run.CancelledAt = now
	}
	s.workflowRuns[index] = run
}

// CompleteWorkflowRun records the final or suspended result returned by a workflow.
func (s *State) CompleteWorkflowRun(runID, status, summary, nextStage, approvalPrompt string, pendingFields []schema.WorkflowInputField, stages []WorkflowRunStageSnapshot) {
	if s == nil || strings.TrimSpace(runID) == "" {
		return
	}
	now := workflowRunTimestamp(time.Now().UTC())
	s.mu.Lock()
	defer s.mu.Unlock()
	index := s.workflowRunIndexLocked(runID)
	if index < 0 {
		return
	}
	run := s.workflowRuns[index]
	if strings.TrimSpace(status) != "" {
		run.Status = strings.TrimSpace(status)
	}
	run.UpdatedAt = now
	if isTerminalWorkflowStatus(run.Status) {
		run.CompletedAt = now
	}
	if strings.EqualFold(run.Status, "cancelled") {
		run.CancelledAt = now
	}
	if strings.TrimSpace(summary) != "" {
		run.Summary = trimWorkflowRunText(summary)
	}
	run.NextStage = strings.TrimSpace(nextStage)
	run.ApprovalPrompt = trimWorkflowRunText(approvalPrompt)
	if strings.EqualFold(run.Status, "awaiting_input") {
		run.PendingFields = copyWorkflowInputFields(pendingFields)
	} else {
		run.PendingFields = nil
	}
	if !strings.EqualFold(run.Status, "awaiting_sub_workflow") {
		run.PendingSubWorkflowName = ""
		run.PendingSubWorkflowRunID = ""
		run.PendingSubWorkflowStatus = ""
	}
	if len(stages) > 0 {
		run.CompletedStages = enrichWorkflowRunStages(run, stages, now)
		run.Artifacts = workflowRunArtifacts(run.CompletedStages)
	}
	s.mergeWorkflowSchemaFromRunLocked(run)
	s.workflowRuns[index] = run
}

// AppendWorkflowRunEvent adds a bounded replay event to an existing run record.
func (s *State) AppendWorkflowRunEvent(runID string, event WorkflowRunEventSnapshot) {
	if s == nil || strings.TrimSpace(runID) == "" {
		return
	}
	now := workflowRunTimestamp(time.Now().UTC())
	if strings.TrimSpace(event.At) == "" {
		event.At = now
	}
	event.Stage = strings.TrimSpace(event.Stage)
	event.Content = trimWorkflowRunText(event.Content)
	event.ArgumentsSummary = trimWorkflowRunText(event.ArgumentsSummary)
	s.mu.Lock()
	defer s.mu.Unlock()
	index := s.workflowRunIndexLocked(runID)
	if index < 0 {
		return
	}
	run := s.workflowRuns[index]
	run.Events = appendWorkflowRunEventLocked(run.Events, event)
	run.UpdatedAt = now
	s.workflowRuns[index] = run
}

// WorkflowRuns returns a newest-first copy of recent workflow runs.
func (s *State) WorkflowRuns() []WorkflowRunSnapshot {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return copyWorkflowRunSnapshots(s.workflowRuns)
}

// WorkflowRun returns one workflow run by id.
func (s *State) WorkflowRun(id string) (WorkflowRunSnapshot, bool) {
	if s == nil || strings.TrimSpace(id) == "" {
		return WorkflowRunSnapshot{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, run := range s.workflowRuns {
		if run.ID == id {
			return copyWorkflowRunSnapshot(run), true
		}
	}
	return WorkflowRunSnapshot{}, false
}

func (s *State) workflowRunIndexLocked(id string) int {
	id = strings.TrimSpace(id)
	for i := range s.workflowRuns {
		if s.workflowRuns[i].ID == id {
			return i
		}
	}
	return -1
}

func uniqueWorkflowRunID(runs []WorkflowRunSnapshot, name string, now time.Time) string {
	for suffix := 0; ; suffix++ {
		id := workflowRunID(name, now, suffix)
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

func workflowRunID(name string, now time.Time, suffix int) string {
	slug := workflowRunSlug(name)
	if suffix > 0 {
		return fmt.Sprintf("wf-%s-%d-%d", slug, now.UnixNano(), suffix)
	}
	return fmt.Sprintf("wf-%s-%d", slug, now.UnixNano())
}

func workflowRunStartContent(retryOf string) string {
	if strings.TrimSpace(retryOf) == "" {
		return "workflow run started"
	}
	return "workflow retry started from " + strings.TrimSpace(retryOf)
}

func workflowRunSlug(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	lastDash := false
	for _, r := range name {
		allowed := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if allowed {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if (r == '-' || r == '_') && !lastDash {
			b.WriteByte('-')
			lastDash = true
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		slug = "workflow"
	}
	if len(slug) > 48 {
		slug = strings.Trim(slug[:48], "-")
	}
	if slug == "" {
		return "workflow"
	}
	return slug
}

func workflowRunTimestamp(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func isTerminalWorkflowStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "denied", "failed", "blocked", "cancelled", "canceled":
		return true
	default:
		return false
	}
}

func workflowRunActive(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running", "cancelling", "awaiting_sub_workflow":
		return true
	default:
		return false
	}
}

func trimWorkflowRunText(value string) string {
	if len(value) <= maxWorkflowRunText {
		return value
	}
	return value[:maxWorkflowRunText]
}

func copyWorkflowRunSnapshots(runs []WorkflowRunSnapshot) []WorkflowRunSnapshot {
	if len(runs) == 0 {
		return nil
	}
	copied := make([]WorkflowRunSnapshot, len(runs))
	for i, run := range runs {
		copied[i] = copyWorkflowRunSnapshot(run)
	}
	return copied
}

func copyWorkflowRunSnapshot(run WorkflowRunSnapshot) WorkflowRunSnapshot {
	if run.PendingToolRisk != nil {
		risk := copyToolRiskProfile(*run.PendingToolRisk)
		run.PendingToolRisk = &risk
	}
	run.CompletedStages = copyWorkflowRunStageSnapshots(run.CompletedStages)
	if len(run.Events) > 0 {
		run.Events = append([]WorkflowRunEventSnapshot(nil), run.Events...)
		for i := range run.Events {
			if run.Events[i].Risk != nil {
				risk := copyToolRiskProfile(*run.Events[i].Risk)
				run.Events[i].Risk = &risk
			}
		}
		assignWorkflowRunEventSeqs(run.Events)
	}
	run.PendingFields = copyWorkflowInputFields(run.PendingFields)
	run.Artifacts = copyWorkflowRunArtifacts(run.Artifacts)
	return run
}

func copyWorkflowInputFields(fields []schema.WorkflowInputField) []schema.WorkflowInputField {
	if len(fields) == 0 {
		return nil
	}
	copied := make([]schema.WorkflowInputField, len(fields))
	for i, field := range fields {
		copied[i] = field
		if len(field.Options) > 0 {
			copied[i].Options = append([]string(nil), field.Options...)
		}
	}
	return copied
}

func appendBoundedWorkflowRunEvents(events []WorkflowRunEventSnapshot, event WorkflowRunEventSnapshot) []WorkflowRunEventSnapshot {
	events = append(events, event)
	if len(events) <= maxWorkflowRunEvents {
		return events
	}
	return append([]WorkflowRunEventSnapshot(nil), events[len(events)-maxWorkflowRunEvents:]...)
}

func appendWorkflowRunEventLocked(events []WorkflowRunEventSnapshot, event WorkflowRunEventSnapshot) []WorkflowRunEventSnapshot {
	if event.Seq <= 0 {
		event.Seq = nextWorkflowRunEventSeq(events)
	}
	return appendBoundedWorkflowRunEvents(events, event)
}

func nextWorkflowRunEventSeq(events []WorkflowRunEventSnapshot) int {
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

func assignWorkflowRunEventSeqs(events []WorkflowRunEventSnapshot) {
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

func copyWorkflowRunStageSnapshots(stages []WorkflowRunStageSnapshot) []WorkflowRunStageSnapshot {
	if len(stages) == 0 {
		return nil
	}
	copied := make([]WorkflowRunStageSnapshot, len(stages))
	for i, stage := range stages {
		copied[i] = stage
		copied[i].Inputs = copyWorkflowRunStringMap(stage.Inputs)
		copied[i].InputValues = copyWorkflowRunAnyMap(stage.InputValues)
		copied[i].Outputs = copyWorkflowRunStringMap(stage.Outputs)
		copied[i].OutputValues = copyWorkflowRunAnyMap(stage.OutputValues)
		copied[i].Metadata = copyWorkflowRunStringMap(stage.Metadata)
		copied[i].Result = copyAgentResult(stage.Result)
		copied[i].Artifacts = copyWorkflowRunArtifacts(stage.Artifacts)
		copied[i].Acceptance = copyWorkflowRunAcceptance(stage.Acceptance)
	}
	return copied
}

func copyWorkflowRunAcceptance(items []WorkflowRunAcceptanceSnapshot) []WorkflowRunAcceptanceSnapshot {
	if len(items) == 0 {
		return nil
	}
	return append([]WorkflowRunAcceptanceSnapshot(nil), items...)
}

func copyWorkflowRunAnyMap(values map[string]any) map[string]any {
	if len(values) == 0 {
		return nil
	}
	copied := make(map[string]any, len(values))
	for key, value := range values {
		copied[key] = copyWorkflowRunAnyValue(value)
	}
	return copied
}

func copyWorkflowRunAnyValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return copyWorkflowRunAnyMap(typed)
	case []any:
		copied := make([]any, len(typed))
		for i, item := range typed {
			copied[i] = copyWorkflowRunAnyValue(item)
		}
		return copied
	case []string:
		return append([]string(nil), typed...)
	case map[string]string:
		return copyWorkflowRunStringMap(typed)
	default:
		return typed
	}
}

func enrichWorkflowRunStages(run WorkflowRunSnapshot, stages []WorkflowRunStageSnapshot, completedAt string) []WorkflowRunStageSnapshot {
	copied := copyWorkflowRunStageSnapshots(stages)
	previous := make(map[string]WorkflowRunStageSnapshot, len(run.CompletedStages))
	for _, stage := range run.CompletedStages {
		previous[strings.ToLower(strings.TrimSpace(stage.Stage))] = stage
	}
	for i := range copied {
		stage := &copied[i]
		key := strings.ToLower(strings.TrimSpace(stage.Stage))
		old := previous[key]
		if strings.TrimSpace(stage.Status) == "" {
			stage.Status = "completed"
		}
		if strings.TrimSpace(stage.StartedAt) == "" {
			stage.StartedAt = old.StartedAt
		}
		if strings.TrimSpace(stage.StartedAt) == "" {
			stage.StartedAt = firstWorkflowRunStageEventAt(run.Events, stage.Stage)
		}
		if strings.TrimSpace(stage.StartedAt) == "" {
			stage.StartedAt = run.StartedAt
		}
		if strings.TrimSpace(stage.CompletedAt) == "" {
			stage.CompletedAt = completedAt
		}
		if strings.TrimSpace(stage.Summary) == "" {
			stage.Summary = trimWorkflowRunText(stage.Result.Output)
		}
		stage.Artifacts = buildWorkflowRunStageArtifacts(*stage)
	}
	return copied
}

func firstWorkflowRunStageEventAt(events []WorkflowRunEventSnapshot, stage string) string {
	stage = strings.ToLower(strings.TrimSpace(stage))
	for _, event := range events {
		if strings.ToLower(strings.TrimSpace(event.Stage)) == stage && strings.TrimSpace(event.At) != "" {
			return event.At
		}
	}
	return ""
}

func workflowRunArtifacts(stages []WorkflowRunStageSnapshot) []WorkflowRunArtifact {
	var artifacts []WorkflowRunArtifact
	for _, stage := range stages {
		artifacts = append(artifacts, copyWorkflowRunArtifacts(stage.Artifacts)...)
	}
	return artifacts
}

func buildWorkflowRunStageArtifacts(stage WorkflowRunStageSnapshot) []WorkflowRunArtifact {
	stageName := strings.TrimSpace(stage.Stage)
	artifacts := declaredWorkflowRunStageArtifacts(stageName, stage.Artifacts)
	if strings.TrimSpace(stage.Result.Output) != "" {
		artifacts = append(artifacts, WorkflowRunArtifact{
			ID:      workflowArtifactID(stageName, "output", len(artifacts)+1),
			Stage:   stageName,
			Kind:    "output",
			Title:   "Stage output",
			Summary: trimWorkflowRunText(stage.Result.Output),
			Content: trimWorkflowRunText(stage.Result.Output),
		})
	}
	for _, result := range stage.Result.ToolResults {
		title := strings.TrimSpace(result.ToolName)
		if title == "" {
			title = "Tool result"
		}
		artifacts = append(artifacts, WorkflowRunArtifact{
			ID:         workflowArtifactID(stageName, "tool-result", len(artifacts)+1),
			Stage:      stageName,
			Kind:       "tool_result",
			Title:      title,
			Summary:    trimWorkflowRunText(result.Content),
			Content:    trimWorkflowRunText(result.Content),
			ToolName:   result.ToolName,
			ToolCallID: result.CallID,
			IsError:    result.IsError || result.Denied,
			Metadata: workflowArtifactMetadata(map[string]string{
				"denied":    boolString(result.Denied),
				"suspended": boolString(result.Suspended),
			}),
		})
	}
	for _, section := range stage.Result.Structured {
		content := section.Summary
		if strings.TrimSpace(content) == "" && len(section.Items) > 0 {
			content = strings.Join(section.Items, "\n")
		}
		if strings.TrimSpace(content) == "" {
			continue
		}
		title := strings.TrimSpace(section.Title)
		if title == "" {
			title = "Structured output"
		}
		artifacts = append(artifacts, WorkflowRunArtifact{
			ID:      workflowArtifactID(stageName, "structured", len(artifacts)+1),
			Stage:   stageName,
			Kind:    "structured",
			Title:   title,
			Summary: trimWorkflowRunText(content),
			Content: trimWorkflowRunText(content),
			Metadata: workflowArtifactMetadata(map[string]string{
				"section_kind": section.Kind,
			}),
		})
	}
	for _, finding := range stage.Result.Findings {
		content := strings.TrimSpace(finding.Summary)
		if strings.TrimSpace(finding.Remediation) != "" {
			content = strings.TrimSpace(content + "\n" + finding.Remediation)
		}
		if content == "" {
			continue
		}
		artifacts = append(artifacts, WorkflowRunArtifact{
			ID:      workflowArtifactID(stageName, "finding", len(artifacts)+1),
			Stage:   stageName,
			Kind:    "finding",
			Title:   fallbackWorkflowArtifactTitle(finding.Summary, "Finding"),
			Summary: trimWorkflowRunText(finding.Summary),
			Content: trimWorkflowRunText(content),
			Metadata: workflowArtifactMetadata(map[string]string{
				"severity": finding.Severity,
				"files":    strings.Join(finding.Files, ","),
				"fixed":    boolString(finding.Fixed),
			}),
		})
	}
	for _, change := range stage.Result.Changes {
		if strings.TrimSpace(change.Summary) == "" {
			continue
		}
		artifacts = append(artifacts, WorkflowRunArtifact{
			ID:      workflowArtifactID(stageName, "change", len(artifacts)+1),
			Stage:   stageName,
			Kind:    "change",
			Title:   "Change",
			Summary: trimWorkflowRunText(change.Summary),
			Content: trimWorkflowRunText(change.Summary),
			Metadata: workflowArtifactMetadata(map[string]string{
				"files": strings.Join(change.Files, ","),
			}),
		})
	}
	for _, verification := range stage.Result.Verification {
		content := strings.TrimSpace(verification.Detail)
		if content == "" {
			content = strings.TrimSpace(verification.Kind + " " + verification.Status)
		}
		if content == "" {
			continue
		}
		artifacts = append(artifacts, WorkflowRunArtifact{
			ID:      workflowArtifactID(stageName, "verification", len(artifacts)+1),
			Stage:   stageName,
			Kind:    "verification",
			Title:   fallbackWorkflowArtifactTitle(verification.Kind, "Verification"),
			Summary: trimWorkflowRunText(content),
			Content: trimWorkflowRunText(content),
			Metadata: workflowArtifactMetadata(map[string]string{
				"status": verification.Status,
			}),
		})
	}
	for _, acceptance := range stage.Acceptance {
		content := strings.TrimSpace(acceptance.Reason)
		if content == "" {
			content = strings.TrimSpace(acceptance.Actual)
		}
		if content == "" {
			continue
		}
		artifacts = append(artifacts, WorkflowRunArtifact{
			ID:      workflowArtifactID(stageName, "acceptance", len(artifacts)+1),
			Stage:   stageName,
			Kind:    "acceptance",
			Title:   fallbackWorkflowArtifactTitle(acceptance.Name, "Acceptance"),
			Summary: trimWorkflowRunText(content),
			Content: trimWorkflowRunText(content),
			Metadata: workflowArtifactMetadata(map[string]string{
				"description": acceptance.Description,
				"ref":         acceptance.Ref,
				"expected":    acceptance.Expected,
				"status":      acceptance.Status,
			}),
		})
	}
	return enrichWorkflowRunStageArtifacts(stage, artifacts)
}

func declaredWorkflowRunStageArtifacts(stageName string, artifacts []WorkflowRunArtifact) []WorkflowRunArtifact {
	if len(artifacts) == 0 {
		return nil
	}
	out := make([]WorkflowRunArtifact, 0, len(artifacts))
	for index, artifact := range artifacts {
		if !strings.EqualFold(strings.TrimSpace(artifact.Metadata["declared"]), "true") {
			continue
		}
		if strings.TrimSpace(artifact.Stage) == "" {
			artifact.Stage = stageName
		}
		if strings.TrimSpace(artifact.Kind) == "" {
			artifact.Kind = "artifact"
		}
		if strings.TrimSpace(artifact.ID) == "" {
			artifact.ID = workflowArtifactID(stageName, artifact.Kind, index+1)
		}
		if artifact.Metadata == nil {
			artifact.Metadata = map[string]string{"declared": "true"}
		} else {
			artifact.Metadata = copyWorkflowRunStringMap(artifact.Metadata)
			artifact.Metadata["declared"] = "true"
		}
		artifact.Summary = trimWorkflowRunText(artifact.Summary)
		artifact.Content = trimWorkflowRunText(artifact.Content)
		out = append(out, artifact)
	}
	return out
}

func enrichWorkflowRunStageArtifacts(stage WorkflowRunStageSnapshot, artifacts []WorkflowRunArtifact) []WorkflowRunArtifact {
	if len(artifacts) == 0 {
		return nil
	}
	out := make([]WorkflowRunArtifact, len(artifacts))
	for i, artifact := range artifacts {
		artifact.Metadata = copyWorkflowRunStringMap(artifact.Metadata)
		if artifact.Metadata == nil {
			artifact.Metadata = map[string]string{}
		}
		workflowArtifactSetMetadata(artifact.Metadata, "producer_stage", firstWorkflowArtifactValue(artifact.Stage, stage.Stage))
		workflowArtifactSetMetadata(artifact.Metadata, "producer_agent", stage.AgentID)
		workflowArtifactSetMetadata(artifact.Metadata, "producer_node_type", stage.NodeType)
		workflowArtifactSetMetadata(artifact.Metadata, "producer_skill", stage.Skill)
		workflowArtifactSetMetadata(artifact.Metadata, "producer_tool", firstWorkflowArtifactValue(artifact.ToolName, stage.Tool))
		workflowArtifactSetMetadata(artifact.Metadata, "source_tool", firstWorkflowArtifactValue(artifact.ToolName, stage.Tool))
		workflowArtifactSetMetadata(artifact.Metadata, "evidence_category", workflowArtifactEvidenceCategory(artifact))
		workflowArtifactSetMetadata(artifact.Metadata, "validation_status", workflowArtifactValidationStatus(artifact))
		if files := firstWorkflowArtifactValue(artifact.Metadata["files"], artifact.Metadata["related_files"]); files != "" {
			workflowArtifactSetMetadata(artifact.Metadata, "related_files", files)
		}
		if ref := firstWorkflowArtifactValue(artifact.Metadata["ref"], artifact.Metadata["refs"]); ref != "" {
			workflowArtifactSetMetadata(artifact.Metadata, "refs", ref)
		}
		if len(artifact.Metadata) == 0 {
			artifact.Metadata = nil
		}
		out[i] = artifact
	}
	return out
}

func workflowArtifactSetMetadata(metadata map[string]string, key, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	if strings.TrimSpace(metadata[key]) != "" {
		return
	}
	metadata[key] = value
}

func workflowArtifactEvidenceCategory(artifact WorkflowRunArtifact) string {
	switch strings.ToLower(strings.TrimSpace(artifact.Kind)) {
	case "acceptance":
		return "acceptance"
	case "verification":
		return "verification"
	case "finding":
		return "finding"
	case "change", "diff", "patch":
		return "change"
	case "tool_result":
		return "tool"
	case "output", "structured", "report", "artifact", "evidence":
		return "evidence"
	default:
		if artifact.IsError {
			return "error"
		}
		return "artifact"
	}
}

func workflowArtifactValidationStatus(artifact WorkflowRunArtifact) string {
	if artifact.IsError {
		return "failed"
	}
	if status := strings.ToLower(strings.TrimSpace(artifact.Metadata["status"])); status != "" {
		return status
	}
	if fixed := strings.ToLower(strings.TrimSpace(artifact.Metadata["fixed"])); fixed == "true" {
		return "fixed"
	}
	switch strings.ToLower(strings.TrimSpace(artifact.Kind)) {
	case "acceptance", "verification":
		return "unknown"
	default:
		return "recorded"
	}
}

func firstWorkflowArtifactValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func workflowArtifactID(stage, kind string, index int) string {
	stage = workflowRunSlug(stage)
	kind = workflowRunSlug(kind)
	return fmt.Sprintf("%s-%s-%d", stage, kind, index)
}

func fallbackWorkflowArtifactTitle(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	if len(value) > 80 {
		return value[:80]
	}
	return value
}

func workflowArtifactMetadata(values map[string]string) map[string]string {
	metadata := make(map[string]string, len(values))
	for key, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		metadata[key] = value
	}
	if len(metadata) == 0 {
		return nil
	}
	return metadata
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return ""
}

func copyWorkflowRunArtifacts(artifacts []WorkflowRunArtifact) []WorkflowRunArtifact {
	if len(artifacts) == 0 {
		return nil
	}
	copied := make([]WorkflowRunArtifact, len(artifacts))
	for i, artifact := range artifacts {
		copied[i] = artifact
		if len(artifact.Metadata) > 0 {
			copied[i].Metadata = make(map[string]string, len(artifact.Metadata))
			for key, value := range artifact.Metadata {
				copied[i].Metadata[key] = value
			}
		}
	}
	return copied
}

func copyWorkflowRunStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	copied := make(map[string]string, len(values))
	for key, value := range values {
		copied[key] = value
	}
	return copied
}

func copyAgentResult(result schema.AgentResult) schema.AgentResult {
	result.ToolResults = append([]schema.ToolResult(nil), result.ToolResults...)
	result.Structured = append([]schema.StructuredSection(nil), result.Structured...)
	result.AuditTrail = append([]schema.AuditEntry(nil), result.AuditTrail...)
	result.Findings = append([]schema.Finding(nil), result.Findings...)
	result.Changes = append([]schema.Change(nil), result.Changes...)
	result.Verification = append([]schema.Verification(nil), result.Verification...)
	return result
}
