package session

import (
	"fmt"
	"strconv"
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
	for i := range run.Events {
		run.Events[i] = s.normalizeWorkflowRunEventLocked(id, run.Name, run.Events[i])
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
	snapshot = s.normalizeWorkflowSnapshotLocked(snapshot)

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
		PendingArgsSummary:       trimWorkflowRunText(snapshot.PendingArgumentsSummary),
		PendingArgsArtifactRef:   strings.TrimSpace(snapshot.PendingArgumentsArtifactRef),
		PendingArgsHash:          strings.TrimSpace(snapshot.PendingArgumentsHash),
		PendingArgsBytes:         snapshot.PendingArgumentsBytes,
		PendingArgsStoredBytes:   snapshot.PendingArgumentsStoredBytes,
		PendingArgsExternalized:  snapshot.PendingArgumentsExternalized,
		PendingResponseMessage:   schema.CopyMessage(snapshot.PendingResponseMessage),
		PendingSubWorkflowName:   strings.TrimSpace(snapshot.PendingSubWorkflowName),
		PendingSubWorkflowRunID:  strings.TrimSpace(snapshot.PendingSubWorkflowRunID),
		PendingSubWorkflowStatus: strings.TrimSpace(snapshot.PendingSubWorkflowStatus),
	}
	run = normalizeWorkflowRunPendingArgumentsLocked(s.artifactStore, run)
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
	run.Events = appendWorkflowRunEventLocked(run.Events, s.normalizeWorkflowRunEventLocked(run.ID, run.Name, WorkflowRunEventSnapshot{
		At:             now,
		Type:           "workflow_cancel_requested",
		Content:        trimWorkflowRunText(reason),
		WorkflowName:   run.Name,
		WorkflowStatus: run.Status,
		NextStage:      run.NextStage,
	}))
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
	run.PendingArgsSummary = ""
	run.PendingArgsArtifactRef = ""
	run.PendingArgsHash = ""
	run.PendingArgsBytes = 0
	run.PendingArgsStoredBytes = 0
	run.PendingArgsExternalized = false
	run.PendingResponseMessage = schema.Message{}
	run.PendingSubWorkflowName = ""
	run.PendingSubWorkflowRunID = ""
	run.PendingSubWorkflowStatus = ""
	run.ApprovalPrompt = ""
	run.PendingFields = nil
	if strings.TrimSpace(reason) != "" {
		run.Summary = trimWorkflowRunText(reason)
	}
	run.Events = appendWorkflowRunEventLocked(run.Events, s.normalizeWorkflowRunEventLocked(run.ID, run.Name, WorkflowRunEventSnapshot{
		At:             now,
		Type:           "workflow_cancelled",
		Content:        trimWorkflowRunText(reason),
		WorkflowName:   run.Name,
		WorkflowStatus: run.Status,
		NextStage:      run.NextStage,
	}))
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
		s.workflow.PendingArgumentsSummary = ""
		s.workflow.PendingArgumentsArtifactRef = ""
		s.workflow.PendingArgumentsHash = ""
		s.workflow.PendingArgumentsBytes = 0
		s.workflow.PendingArgumentsStoredBytes = 0
		s.workflow.PendingArgumentsExternalized = false
		s.workflow.PendingResponseMessage = schema.Message{}
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
	snapshot = s.normalizeWorkflowSnapshotLocked(snapshot)
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
	run.PendingArgsSummary = trimWorkflowRunText(snapshot.PendingArgumentsSummary)
	run.PendingArgsArtifactRef = strings.TrimSpace(snapshot.PendingArgumentsArtifactRef)
	run.PendingArgsHash = strings.TrimSpace(snapshot.PendingArgumentsHash)
	run.PendingArgsBytes = snapshot.PendingArgumentsBytes
	run.PendingArgsStoredBytes = snapshot.PendingArgumentsStoredBytes
	run.PendingArgsExternalized = snapshot.PendingArgumentsExternalized
	run.PendingResponseMessage = schema.CopyMessage(snapshot.PendingResponseMessage)
	run.PendingSubWorkflowName = strings.TrimSpace(snapshot.PendingSubWorkflowName)
	run.PendingSubWorkflowRunID = strings.TrimSpace(snapshot.PendingSubWorkflowRunID)
	run.PendingSubWorkflowStatus = strings.TrimSpace(snapshot.PendingSubWorkflowStatus)
	run = normalizeWorkflowRunPendingArgumentsLocked(s.artifactStore, run)
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
	run = updateWorkflowRunBudgetSummary(run)
	if !strings.EqualFold(run.Status, "awaiting_tool_approval") {
		run.PendingCallID = ""
		run.PendingToolName = ""
		run.PendingAgentID = ""
		run = clearWorkflowRunPendingArguments(run)
		run.PendingResponseMessage = schema.Message{}
	}
	if !strings.EqualFold(run.Status, "awaiting_sub_workflow") {
		run.PendingSubWorkflowName = ""
		run.PendingSubWorkflowRunID = ""
		run.PendingSubWorkflowStatus = ""
	}
	if len(stages) > 0 {
		run.CompletedStages = enrichWorkflowRunStages(run, stages, now)
		run.CompletedStages = enrichWorkflowRunStageBudgets(run.CompletedStages, run.Events)
		run.CompletedStages = s.normalizeWorkflowRunStagePayloadsLocked(run.ID, run.Name, run.CompletedStages)
		run.CompletedStages = s.externalizeWorkflowRunStageArtifactsLocked(run.CompletedStages, now)
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
	event.ArgumentsSummary = trimWorkflowRunText(event.ArgumentsSummary)
	s.mu.Lock()
	defer s.mu.Unlock()
	index := s.workflowRunIndexLocked(runID)
	if index < 0 {
		return
	}
	run := s.workflowRuns[index]
	event = s.normalizeWorkflowRunEventLocked(runID, run.Name, event)
	run.Events = appendWorkflowRunEventLocked(run.Events, event)
	run.UpdatedAt = now
	run = updateWorkflowRunBudgetSummary(run)
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
	for _, run := range s.workflowRuns {
		if run.ID == id {
			copied := copyWorkflowRunSnapshot(run)
			s.mu.RUnlock()
			return s.HydrateWorkflowRun(copied), true
		}
	}
	s.mu.RUnlock()
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
	case "running", "cancelling", "awaiting_sub_workflow", "paused_need_more_budget", "awaiting_budget_approval":
		return true
	default:
		return false
	}
}

func updateWorkflowRunBudgetSummary(run WorkflowRunSnapshot) WorkflowRunSnapshot {
	var estimatedPromptTokens, reportedPromptTokens, outputTokens, cachedTokens, promptBudgetCalls, reportedCalls, continuations int
	var cumulativeEstimatedPromptTokens, cumulativeReportedPromptTokens, cumulativeOutputTokens, cumulativeCachedTokens, cumulativeLLMCalls, cumulativeContinuations int
	var netPromptTokens, grossPromptTokens, savedTokens, memorySavedTokens, historySavedTokens, artifactSavedTokens, skillSavedTokens, toolSchemaSavedTokens int
	var cumulativeNetPromptTokens, cumulativeGrossPromptTokens, cumulativeSavedTokens, cumulativeMemorySavedTokens, cumulativeHistorySavedTokens, cumulativeArtifactSavedTokens, cumulativeSkillSavedTokens, cumulativeToolSchemaSavedTokens int
	var estimatedInputCost, estimatedOutputCost, cumulativeEstimatedInputCost, cumulativeEstimatedOutputCost, cumulativeEstimatedTotalCost float64
	var costCurrency, pricingSource string
	promptCostFromBudget := false
	for _, event := range run.Events {
		switch strings.ToLower(strings.TrimSpace(event.Type)) {
		case "prompt_budget":
			if event.PromptBudget != nil {
				estimatedPromptTokens += event.PromptBudget.EstimatedPromptTokens
				estimatedInputCost += event.PromptBudget.EstimatedInputCost
				if event.PromptBudget.EstimatedInputCost > 0 {
					promptCostFromBudget = true
				}
				if strings.TrimSpace(costCurrency) == "" {
					costCurrency = event.PromptBudget.CostCurrency
				}
				if strings.TrimSpace(pricingSource) == "" {
					pricingSource = event.PromptBudget.PricingSource
				}
				attribution := promptBudgetTokenAttribution(*event.PromptBudget)
				netPromptTokens += attribution.NetPromptTokens
				grossPromptTokens += attribution.GrossPromptTokens
				savedTokens += attribution.SavedTokens
				memorySavedTokens += attribution.MemorySavedTokens
				historySavedTokens += attribution.HistorySavedTokens
				artifactSavedTokens += attribution.ArtifactSavedTokens
				skillSavedTokens += attribution.SkillSavedTokens
				toolSchemaSavedTokens += attribution.ToolSchemaSavedTokens
			} else if event.BudgetEstimatedPromptTokens > 0 {
				if event.BudgetEstimatedPromptTokens > estimatedPromptTokens {
					estimatedPromptTokens = event.BudgetEstimatedPromptTokens
				}
			}
			promptBudgetCalls++
		case "token_usage":
			reportedPromptTokens += maxInt(event.PromptTokens, event.BudgetReportedPromptTokens)
			outputTokens += maxInt(event.OutputTokens, event.BudgetOutputTokens)
			cachedTokens += maxInt(event.CachedTokens, event.BudgetCachedTokens)
			if !promptCostFromBudget {
				estimatedInputCost += event.BudgetEstimatedInputCost
			}
			estimatedOutputCost += event.BudgetEstimatedOutputCost
			if strings.TrimSpace(costCurrency) == "" {
				costCurrency = event.BudgetCostCurrency
			}
			if strings.TrimSpace(pricingSource) == "" {
				pricingSource = event.BudgetPricingSource
			}
			reportedCalls++
		}
		if event.BudgetEstimatedPromptTokens > 0 {
			cumulativeEstimatedPromptTokens = maxInt(cumulativeEstimatedPromptTokens, event.BudgetEstimatedPromptTokens)
		}
		if event.BudgetNetPromptTokens > 0 {
			cumulativeNetPromptTokens = maxInt(cumulativeNetPromptTokens, event.BudgetNetPromptTokens)
		}
		if event.BudgetGrossPromptTokens > 0 {
			cumulativeGrossPromptTokens = maxInt(cumulativeGrossPromptTokens, event.BudgetGrossPromptTokens)
		}
		if event.BudgetSavedTokens > 0 {
			cumulativeSavedTokens = maxInt(cumulativeSavedTokens, event.BudgetSavedTokens)
		}
		if event.BudgetMemorySavedTokens > 0 {
			cumulativeMemorySavedTokens = maxInt(cumulativeMemorySavedTokens, event.BudgetMemorySavedTokens)
		}
		if event.BudgetHistorySavedTokens > 0 {
			cumulativeHistorySavedTokens = maxInt(cumulativeHistorySavedTokens, event.BudgetHistorySavedTokens)
		}
		if event.BudgetArtifactSavedTokens > 0 {
			cumulativeArtifactSavedTokens = maxInt(cumulativeArtifactSavedTokens, event.BudgetArtifactSavedTokens)
		}
		if event.BudgetSkillSavedTokens > 0 {
			cumulativeSkillSavedTokens = maxInt(cumulativeSkillSavedTokens, event.BudgetSkillSavedTokens)
		}
		if event.BudgetToolSchemaSavedTokens > 0 {
			cumulativeToolSchemaSavedTokens = maxInt(cumulativeToolSchemaSavedTokens, event.BudgetToolSchemaSavedTokens)
		}
		if event.BudgetReportedPromptTokens > 0 {
			cumulativeReportedPromptTokens = maxInt(cumulativeReportedPromptTokens, event.BudgetReportedPromptTokens)
		}
		if event.BudgetOutputTokens > 0 {
			cumulativeOutputTokens = maxInt(cumulativeOutputTokens, event.BudgetOutputTokens)
		}
		if event.BudgetCachedTokens > 0 {
			cumulativeCachedTokens = maxInt(cumulativeCachedTokens, event.BudgetCachedTokens)
		}
		if event.BudgetLLMCalls > 0 {
			cumulativeLLMCalls = maxInt(cumulativeLLMCalls, event.BudgetLLMCalls)
		}
		if event.BudgetContinuations > 0 {
			cumulativeContinuations = maxInt(cumulativeContinuations, event.BudgetContinuations)
		}
		if event.BudgetEstimatedInputCost > 0 {
			cumulativeEstimatedInputCost = maxFloat64(cumulativeEstimatedInputCost, event.BudgetEstimatedInputCost)
		}
		if event.BudgetEstimatedOutputCost > 0 {
			cumulativeEstimatedOutputCost = maxFloat64(cumulativeEstimatedOutputCost, event.BudgetEstimatedOutputCost)
		}
		if event.BudgetEstimatedTotalCost > 0 {
			cumulativeEstimatedTotalCost = maxFloat64(cumulativeEstimatedTotalCost, event.BudgetEstimatedTotalCost)
		}
		if strings.TrimSpace(costCurrency) == "" {
			costCurrency = event.BudgetCostCurrency
		}
		if strings.TrimSpace(pricingSource) == "" {
			pricingSource = event.BudgetPricingSource
		}
		if strings.EqualFold(strings.TrimSpace(event.Reason), "continuation_started") {
			continuations++
		}
	}
	estimatedPromptTokens = maxInt(estimatedPromptTokens, cumulativeEstimatedPromptTokens)
	netPromptTokens = maxInt(netPromptTokens, cumulativeNetPromptTokens)
	grossPromptTokens = maxInt(grossPromptTokens, cumulativeGrossPromptTokens)
	savedTokens = maxInt(savedTokens, cumulativeSavedTokens)
	memorySavedTokens = maxInt(memorySavedTokens, cumulativeMemorySavedTokens)
	historySavedTokens = maxInt(historySavedTokens, cumulativeHistorySavedTokens)
	artifactSavedTokens = maxInt(artifactSavedTokens, cumulativeArtifactSavedTokens)
	skillSavedTokens = maxInt(skillSavedTokens, cumulativeSkillSavedTokens)
	toolSchemaSavedTokens = maxInt(toolSchemaSavedTokens, cumulativeToolSchemaSavedTokens)
	if netPromptTokens == 0 {
		netPromptTokens = estimatedPromptTokens
	}
	if savedTokens == 0 {
		savedTokens = memorySavedTokens + historySavedTokens + artifactSavedTokens + skillSavedTokens + toolSchemaSavedTokens
	}
	if grossPromptTokens == 0 && (netPromptTokens > 0 || savedTokens > 0) {
		grossPromptTokens = netPromptTokens + savedTokens
	}
	reportedPromptTokens = maxInt(reportedPromptTokens, cumulativeReportedPromptTokens)
	outputTokens = maxInt(outputTokens, cumulativeOutputTokens)
	cachedTokens = maxInt(cachedTokens, cumulativeCachedTokens)
	estimatedInputCost = maxFloat64(estimatedInputCost, cumulativeEstimatedInputCost)
	estimatedOutputCost = maxFloat64(estimatedOutputCost, cumulativeEstimatedOutputCost)
	estimatedTotalCost := estimatedInputCost + estimatedOutputCost
	estimatedTotalCost = maxFloat64(estimatedTotalCost, cumulativeEstimatedTotalCost)
	llmCalls := promptBudgetCalls
	if llmCalls == 0 {
		llmCalls = reportedCalls
	}
	llmCalls = maxInt(llmCalls, cumulativeLLMCalls)
	continuations = maxInt(continuations, cumulativeContinuations)
	promptTokens := reportedPromptTokens
	if estimatedPromptTokens > promptTokens {
		promptTokens = estimatedPromptTokens
	}
	run.BudgetPromptTokens = promptTokens
	run.BudgetEstimatedPromptTokens = estimatedPromptTokens
	run.BudgetNetPromptTokens = netPromptTokens
	run.BudgetGrossPromptTokens = grossPromptTokens
	run.BudgetSavedTokens = savedTokens
	run.BudgetMemorySavedTokens = memorySavedTokens
	run.BudgetHistorySavedTokens = historySavedTokens
	run.BudgetArtifactSavedTokens = artifactSavedTokens
	run.BudgetSkillSavedTokens = skillSavedTokens
	run.BudgetToolSchemaSavedTokens = toolSchemaSavedTokens
	run.BudgetReportedPromptTokens = reportedPromptTokens
	run.BudgetOutputTokens = outputTokens
	run.BudgetCachedTokens = cachedTokens
	run.BudgetTotalTokens = promptTokens + outputTokens
	run.BudgetLLMCalls = llmCalls
	run.BudgetContinuations = continuations
	run.BudgetEstimatedInputCost = estimatedInputCost
	run.BudgetEstimatedOutputCost = estimatedOutputCost
	run.BudgetEstimatedTotalCost = estimatedTotalCost
	run.BudgetCostCurrency = strings.TrimSpace(costCurrency)
	run.BudgetPricingSource = strings.TrimSpace(pricingSource)
	run.BudgetScope = ""
	run.BudgetReason = ""
	run.BudgetMetric = ""
	run.BudgetUsed = 0
	run.BudgetSoftLimit = 0
	run.BudgetHardLimit = 0
	run.BudgetRemaining = 0
	for i := len(run.Events) - 1; i >= 0; i-- {
		event := run.Events[i]
		if workflowRunEventHasBudgetDiagnostic(event) && run.BudgetMetric == "" && run.BudgetSoftLimit == 0 && run.BudgetHardLimit == 0 {
			run.BudgetMetric = strings.TrimSpace(event.BudgetMetric)
			run.BudgetUsed = event.BudgetUsed
			run.BudgetSoftLimit = event.BudgetSoftLimit
			run.BudgetHardLimit = event.BudgetHardLimit
			run.BudgetRemaining = event.BudgetRemaining
		}
		if strings.TrimSpace(event.BudgetScope) == "" && !strings.EqualFold(strings.TrimSpace(event.Reason), "budget_hard_limit_hit") {
			continue
		}
		if strings.TrimSpace(event.BudgetScope) != "" {
			run.BudgetScope = strings.TrimSpace(event.BudgetScope)
		}
		if strings.TrimSpace(event.BudgetReason) != "" {
			run.BudgetReason = strings.TrimSpace(event.BudgetReason)
		} else if strings.TrimSpace(event.Reason) != "" {
			run.BudgetReason = strings.TrimSpace(event.Reason)
		}
		break
	}
	return run
}

func workflowRunEventHasBudgetDiagnostic(event WorkflowRunEventSnapshot) bool {
	return strings.TrimSpace(event.BudgetMetric) != "" ||
		event.BudgetUsed > 0 ||
		event.BudgetSoftLimit > 0 ||
		event.BudgetHardLimit > 0 ||
		event.BudgetRemaining > 0
}

type promptBudgetAttribution struct {
	NetPromptTokens       int
	GrossPromptTokens     int
	SavedTokens           int
	MemorySavedTokens     int
	HistorySavedTokens    int
	ArtifactSavedTokens   int
	SkillSavedTokens      int
	ToolSchemaSavedTokens int
}

func promptBudgetTokenAttribution(budget schema.PromptBudget) promptBudgetAttribution {
	attribution := promptBudgetAttribution{
		NetPromptTokens:       budget.EstimatedPromptTokens,
		MemorySavedTokens:     budget.MemoryEstimatedSavedTokens,
		HistorySavedTokens:    budget.HistoryEstimatedSavedTokens,
		ArtifactSavedTokens:   budget.ArtifactOmittedTokens,
		SkillSavedTokens:      budget.SkillOmittedTokens,
		ToolSchemaSavedTokens: budget.ToolSchemaEstimatedSavedTokens,
	}
	attribution.SavedTokens = attribution.MemorySavedTokens +
		attribution.HistorySavedTokens +
		attribution.ArtifactSavedTokens +
		attribution.SkillSavedTokens +
		attribution.ToolSchemaSavedTokens
	attribution.GrossPromptTokens = attribution.NetPromptTokens + attribution.SavedTokens
	return attribution
}

type workflowRunStageBudgetUsage struct {
	Scope                 string
	Reason                string
	Metric                string
	Used                  int
	SoftLimit             int
	HardLimit             int
	Remaining             int
	EstimatedPromptTokens int
	NetPromptTokens       int
	GrossPromptTokens     int
	SavedTokens           int
	MemorySavedTokens     int
	HistorySavedTokens    int
	ArtifactSavedTokens   int
	SkillSavedTokens      int
	ToolSchemaSavedTokens int
	ReportedPromptTokens  int
	OutputTokens          int
	CachedTokens          int
	LLMCalls              int
	Continuations         int
	EstimatedInputCost    float64
	EstimatedOutputCost   float64
	EstimatedTotalCost    float64
	CostCurrency          string
	PricingSource         string
	PromptCostFromBudget  bool
}

func workflowRunStageBudgetUsageByStage(events []WorkflowRunEventSnapshot) map[string]workflowRunStageBudgetUsage {
	if len(events) == 0 {
		return nil
	}
	usages := make(map[string]workflowRunStageBudgetUsage)
	for _, event := range events {
		stage := strings.ToLower(strings.TrimSpace(event.Stage))
		if stage == "" {
			continue
		}
		usage := usages[stage]
		switch strings.ToLower(strings.TrimSpace(event.Type)) {
		case "prompt_budget":
			if event.PromptBudget != nil {
				usage.EstimatedPromptTokens += event.PromptBudget.EstimatedPromptTokens
				usage.EstimatedInputCost += event.PromptBudget.EstimatedInputCost
				if event.PromptBudget.EstimatedInputCost > 0 {
					usage.PromptCostFromBudget = true
				}
				if strings.TrimSpace(usage.CostCurrency) == "" {
					usage.CostCurrency = event.PromptBudget.CostCurrency
				}
				if strings.TrimSpace(usage.PricingSource) == "" {
					usage.PricingSource = event.PromptBudget.PricingSource
				}
				attribution := promptBudgetTokenAttribution(*event.PromptBudget)
				usage.NetPromptTokens += attribution.NetPromptTokens
				usage.GrossPromptTokens += attribution.GrossPromptTokens
				usage.SavedTokens += attribution.SavedTokens
				usage.MemorySavedTokens += attribution.MemorySavedTokens
				usage.HistorySavedTokens += attribution.HistorySavedTokens
				usage.ArtifactSavedTokens += attribution.ArtifactSavedTokens
				usage.SkillSavedTokens += attribution.SkillSavedTokens
				usage.ToolSchemaSavedTokens += attribution.ToolSchemaSavedTokens
			} else if event.BudgetEstimatedPromptTokens > 0 {
				usage.EstimatedPromptTokens = maxInt(usage.EstimatedPromptTokens, event.BudgetEstimatedPromptTokens)
			} else if event.PromptTokens > 0 {
				usage.EstimatedPromptTokens += event.PromptTokens
			}
			usage.LLMCalls++
		case "token_usage":
			usage.ReportedPromptTokens += maxInt(event.PromptTokens, event.BudgetReportedPromptTokens)
			usage.OutputTokens += maxInt(event.OutputTokens, event.BudgetOutputTokens)
			usage.CachedTokens += maxInt(event.CachedTokens, event.BudgetCachedTokens)
			if !usage.PromptCostFromBudget {
				usage.EstimatedInputCost += event.BudgetEstimatedInputCost
			}
			usage.EstimatedOutputCost += event.BudgetEstimatedOutputCost
			if strings.TrimSpace(usage.CostCurrency) == "" {
				usage.CostCurrency = event.BudgetCostCurrency
			}
			if strings.TrimSpace(usage.PricingSource) == "" {
				usage.PricingSource = event.BudgetPricingSource
			}
			if usage.LLMCalls == 0 {
				usage.LLMCalls = 1
			}
		}
		if event.BudgetEstimatedPromptTokens > 0 {
			usage.EstimatedPromptTokens = maxInt(usage.EstimatedPromptTokens, event.BudgetEstimatedPromptTokens)
		}
		if event.BudgetNetPromptTokens > 0 {
			usage.NetPromptTokens = maxInt(usage.NetPromptTokens, event.BudgetNetPromptTokens)
		}
		if event.BudgetGrossPromptTokens > 0 {
			usage.GrossPromptTokens = maxInt(usage.GrossPromptTokens, event.BudgetGrossPromptTokens)
		}
		if event.BudgetSavedTokens > 0 {
			usage.SavedTokens = maxInt(usage.SavedTokens, event.BudgetSavedTokens)
		}
		if event.BudgetMemorySavedTokens > 0 {
			usage.MemorySavedTokens = maxInt(usage.MemorySavedTokens, event.BudgetMemorySavedTokens)
		}
		if event.BudgetHistorySavedTokens > 0 {
			usage.HistorySavedTokens = maxInt(usage.HistorySavedTokens, event.BudgetHistorySavedTokens)
		}
		if event.BudgetArtifactSavedTokens > 0 {
			usage.ArtifactSavedTokens = maxInt(usage.ArtifactSavedTokens, event.BudgetArtifactSavedTokens)
		}
		if event.BudgetSkillSavedTokens > 0 {
			usage.SkillSavedTokens = maxInt(usage.SkillSavedTokens, event.BudgetSkillSavedTokens)
		}
		if event.BudgetToolSchemaSavedTokens > 0 {
			usage.ToolSchemaSavedTokens = maxInt(usage.ToolSchemaSavedTokens, event.BudgetToolSchemaSavedTokens)
		}
		if event.BudgetReportedPromptTokens > 0 {
			usage.ReportedPromptTokens = maxInt(usage.ReportedPromptTokens, event.BudgetReportedPromptTokens)
		}
		if event.BudgetOutputTokens > 0 {
			usage.OutputTokens = maxInt(usage.OutputTokens, event.BudgetOutputTokens)
		}
		if event.BudgetCachedTokens > 0 {
			usage.CachedTokens = maxInt(usage.CachedTokens, event.BudgetCachedTokens)
		}
		if event.BudgetLLMCalls > 0 {
			usage.LLMCalls = maxInt(usage.LLMCalls, event.BudgetLLMCalls)
		}
		if event.BudgetContinuations > 0 {
			usage.Continuations = maxInt(usage.Continuations, event.BudgetContinuations)
		}
		if event.BudgetEstimatedInputCost > 0 {
			usage.EstimatedInputCost = maxFloat64(usage.EstimatedInputCost, event.BudgetEstimatedInputCost)
		}
		if event.BudgetEstimatedOutputCost > 0 {
			usage.EstimatedOutputCost = maxFloat64(usage.EstimatedOutputCost, event.BudgetEstimatedOutputCost)
		}
		if event.BudgetEstimatedTotalCost > 0 {
			usage.EstimatedTotalCost = maxFloat64(usage.EstimatedTotalCost, event.BudgetEstimatedTotalCost)
		}
		if strings.TrimSpace(usage.CostCurrency) == "" {
			usage.CostCurrency = event.BudgetCostCurrency
		}
		if strings.TrimSpace(usage.PricingSource) == "" {
			usage.PricingSource = event.BudgetPricingSource
		}
		if strings.EqualFold(strings.TrimSpace(event.Reason), "continuation_started") {
			usage.Continuations++
		}
		if strings.TrimSpace(event.BudgetScope) != "" {
			usage.Scope = strings.TrimSpace(event.BudgetScope)
		}
		if strings.TrimSpace(event.BudgetReason) != "" {
			usage.Reason = strings.TrimSpace(event.BudgetReason)
		} else if strings.TrimSpace(event.Reason) != "" {
			usage.Reason = strings.TrimSpace(event.Reason)
		}
		if workflowRunEventHasBudgetDiagnostic(event) {
			usage.Metric = strings.TrimSpace(event.BudgetMetric)
			usage.Used = event.BudgetUsed
			usage.SoftLimit = event.BudgetSoftLimit
			usage.HardLimit = event.BudgetHardLimit
			usage.Remaining = event.BudgetRemaining
		}
		usages[stage] = usage
	}
	if len(usages) == 0 {
		return nil
	}
	return usages
}

func applyWorkflowRunStageBudgetUsage(stage *WorkflowRunStageSnapshot, usage workflowRunStageBudgetUsage) {
	if stage == nil {
		return
	}
	promptTokens := usage.ReportedPromptTokens
	if usage.EstimatedPromptTokens > promptTokens {
		promptTokens = usage.EstimatedPromptTokens
	}
	if usage.NetPromptTokens == 0 {
		usage.NetPromptTokens = usage.EstimatedPromptTokens
	}
	if usage.SavedTokens == 0 {
		usage.SavedTokens = usage.MemorySavedTokens + usage.HistorySavedTokens + usage.ArtifactSavedTokens + usage.SkillSavedTokens + usage.ToolSchemaSavedTokens
	}
	if usage.GrossPromptTokens == 0 && (usage.NetPromptTokens > 0 || usage.SavedTokens > 0) {
		usage.GrossPromptTokens = usage.NetPromptTokens + usage.SavedTokens
	}
	if usage.EstimatedTotalCost == 0 && (usage.EstimatedInputCost > 0 || usage.EstimatedOutputCost > 0) {
		usage.EstimatedTotalCost = usage.EstimatedInputCost + usage.EstimatedOutputCost
	}
	if strings.TrimSpace(stage.BudgetScope) == "" {
		stage.BudgetScope = usage.Scope
	}
	if strings.TrimSpace(stage.BudgetReason) == "" {
		stage.BudgetReason = usage.Reason
	}
	if strings.TrimSpace(stage.BudgetMetric) == "" {
		stage.BudgetMetric = usage.Metric
	}
	if workflowRunStageBudgetUsageHasDiagnostic(usage) {
		stage.BudgetUsed = usage.Used
		stage.BudgetSoftLimit = usage.SoftLimit
		stage.BudgetHardLimit = usage.HardLimit
		stage.BudgetRemaining = usage.Remaining
	}
	stage.BudgetPromptTokens = maxInt(stage.BudgetPromptTokens, promptTokens)
	stage.BudgetEstimatedPromptTokens = maxInt(stage.BudgetEstimatedPromptTokens, usage.EstimatedPromptTokens)
	stage.BudgetNetPromptTokens = maxInt(stage.BudgetNetPromptTokens, usage.NetPromptTokens)
	stage.BudgetGrossPromptTokens = maxInt(stage.BudgetGrossPromptTokens, usage.GrossPromptTokens)
	stage.BudgetSavedTokens = maxInt(stage.BudgetSavedTokens, usage.SavedTokens)
	stage.BudgetMemorySavedTokens = maxInt(stage.BudgetMemorySavedTokens, usage.MemorySavedTokens)
	stage.BudgetHistorySavedTokens = maxInt(stage.BudgetHistorySavedTokens, usage.HistorySavedTokens)
	stage.BudgetArtifactSavedTokens = maxInt(stage.BudgetArtifactSavedTokens, usage.ArtifactSavedTokens)
	stage.BudgetSkillSavedTokens = maxInt(stage.BudgetSkillSavedTokens, usage.SkillSavedTokens)
	stage.BudgetToolSchemaSavedTokens = maxInt(stage.BudgetToolSchemaSavedTokens, usage.ToolSchemaSavedTokens)
	stage.BudgetReportedPromptTokens = maxInt(stage.BudgetReportedPromptTokens, usage.ReportedPromptTokens)
	stage.BudgetOutputTokens = maxInt(stage.BudgetOutputTokens, usage.OutputTokens)
	stage.BudgetCachedTokens = maxInt(stage.BudgetCachedTokens, usage.CachedTokens)
	stage.BudgetLLMCalls = maxInt(stage.BudgetLLMCalls, usage.LLMCalls)
	stage.BudgetContinuations = maxInt(stage.BudgetContinuations, usage.Continuations)
	stage.BudgetTotalTokens = maxInt(stage.BudgetTotalTokens, stage.BudgetPromptTokens+stage.BudgetOutputTokens)
	stage.BudgetEstimatedInputCost = maxFloat64(stage.BudgetEstimatedInputCost, usage.EstimatedInputCost)
	stage.BudgetEstimatedOutputCost = maxFloat64(stage.BudgetEstimatedOutputCost, usage.EstimatedOutputCost)
	stage.BudgetEstimatedTotalCost = maxFloat64(stage.BudgetEstimatedTotalCost, usage.EstimatedTotalCost)
	if strings.TrimSpace(stage.BudgetCostCurrency) == "" {
		stage.BudgetCostCurrency = strings.TrimSpace(usage.CostCurrency)
	}
	if strings.TrimSpace(stage.BudgetPricingSource) == "" {
		stage.BudgetPricingSource = strings.TrimSpace(usage.PricingSource)
	}
	workflowRunStageBudgetMetadata(stage)
}

func workflowRunStageBudgetUsageHasDiagnostic(usage workflowRunStageBudgetUsage) bool {
	return strings.TrimSpace(usage.Metric) != "" ||
		usage.Used > 0 ||
		usage.SoftLimit > 0 ||
		usage.HardLimit > 0 ||
		usage.Remaining > 0
}

func workflowRunStageBudgetMetadata(stage *WorkflowRunStageSnapshot) {
	if stage == nil {
		return
	}
	if strings.TrimSpace(stage.BudgetScope) == "" &&
		strings.TrimSpace(stage.BudgetReason) == "" &&
		stage.BudgetPromptTokens == 0 &&
		stage.BudgetEstimatedPromptTokens == 0 &&
		stage.BudgetNetPromptTokens == 0 &&
		stage.BudgetGrossPromptTokens == 0 &&
		stage.BudgetSavedTokens == 0 &&
		stage.BudgetMemorySavedTokens == 0 &&
		stage.BudgetHistorySavedTokens == 0 &&
		stage.BudgetArtifactSavedTokens == 0 &&
		stage.BudgetSkillSavedTokens == 0 &&
		stage.BudgetToolSchemaSavedTokens == 0 &&
		stage.BudgetReportedPromptTokens == 0 &&
		stage.BudgetOutputTokens == 0 &&
		stage.BudgetCachedTokens == 0 &&
		stage.BudgetTotalTokens == 0 &&
		stage.BudgetLLMCalls == 0 &&
		stage.BudgetContinuations == 0 &&
		stage.BudgetEstimatedInputCost == 0 &&
		stage.BudgetEstimatedOutputCost == 0 &&
		stage.BudgetEstimatedTotalCost == 0 &&
		strings.TrimSpace(stage.BudgetCostCurrency) == "" &&
		strings.TrimSpace(stage.BudgetPricingSource) == "" &&
		strings.TrimSpace(stage.BudgetMetric) == "" &&
		stage.BudgetUsed == 0 &&
		stage.BudgetSoftLimit == 0 &&
		stage.BudgetHardLimit == 0 &&
		stage.BudgetRemaining == 0 {
		return
	}
	if stage.Metadata == nil {
		stage.Metadata = map[string]string{}
	}
	setString := func(key, value string) {
		if strings.TrimSpace(value) != "" {
			stage.Metadata[key] = strings.TrimSpace(value)
		}
	}
	setInt := func(key string, value int) {
		if value > 0 {
			stage.Metadata[key] = strconv.Itoa(value)
		}
	}
	setString("budget_scope", stage.BudgetScope)
	setString("budget_reason", stage.BudgetReason)
	setString("budget_metric", stage.BudgetMetric)
	setInt("budget_used", stage.BudgetUsed)
	setInt("budget_soft_limit", stage.BudgetSoftLimit)
	setInt("budget_hard_limit", stage.BudgetHardLimit)
	setInt("budget_remaining", stage.BudgetRemaining)
	setInt("budget_prompt_tokens", stage.BudgetPromptTokens)
	setInt("budget_estimated_prompt_tokens", stage.BudgetEstimatedPromptTokens)
	setInt("budget_net_prompt_tokens", stage.BudgetNetPromptTokens)
	setInt("budget_gross_prompt_tokens", stage.BudgetGrossPromptTokens)
	setInt("budget_saved_tokens", stage.BudgetSavedTokens)
	setInt("budget_memory_saved_tokens", stage.BudgetMemorySavedTokens)
	setInt("budget_history_saved_tokens", stage.BudgetHistorySavedTokens)
	setInt("budget_artifact_saved_tokens", stage.BudgetArtifactSavedTokens)
	setInt("budget_skill_saved_tokens", stage.BudgetSkillSavedTokens)
	setInt("budget_tool_schema_saved_tokens", stage.BudgetToolSchemaSavedTokens)
	setInt("budget_reported_prompt_tokens", stage.BudgetReportedPromptTokens)
	setInt("budget_output_tokens", stage.BudgetOutputTokens)
	setInt("budget_cached_tokens", stage.BudgetCachedTokens)
	setInt("budget_total_tokens", stage.BudgetTotalTokens)
	setInt("budget_llm_calls", stage.BudgetLLMCalls)
	setInt("budget_continuations", stage.BudgetContinuations)
	setFloat := func(key string, value float64) {
		if value > 0 {
			stage.Metadata[key] = strconv.FormatFloat(value, 'f', -1, 64)
		}
	}
	setFloat("budget_estimated_input_cost", stage.BudgetEstimatedInputCost)
	setFloat("budget_estimated_output_cost", stage.BudgetEstimatedOutputCost)
	setFloat("budget_estimated_total_cost", stage.BudgetEstimatedTotalCost)
	setString("budget_cost_currency", stage.BudgetCostCurrency)
	setString("budget_pricing_source", stage.BudgetPricingSource)
}

func firstWorkflowRunNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func maxFloat64(left, right float64) float64 {
	if left > right {
		return left
	}
	return right
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
	run.PendingResponseMessage = schema.CopyMessage(run.PendingResponseMessage)
	run.CompletedStages = copyWorkflowRunStageSnapshots(run.CompletedStages)
	run = updateWorkflowRunBudgetSummary(run)
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

func enrichWorkflowRunStageBudgets(stages []WorkflowRunStageSnapshot, events []WorkflowRunEventSnapshot) []WorkflowRunStageSnapshot {
	if len(stages) == 0 || len(events) == 0 {
		return stages
	}
	usages := workflowRunStageBudgetUsageByStage(events)
	if len(usages) == 0 {
		return stages
	}
	out := copyWorkflowRunStageSnapshots(stages)
	for i := range out {
		stage := strings.ToLower(strings.TrimSpace(out[i].Stage))
		if stage == "" {
			continue
		}
		usage, ok := usages[stage]
		if !ok {
			continue
		}
		applyWorkflowRunStageBudgetUsage(&out[i], usage)
	}
	return out
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

func (s *State) externalizeWorkflowRunStageArtifactsLocked(stages []WorkflowRunStageSnapshot, completedAt string) []WorkflowRunStageSnapshot {
	if len(stages) == 0 || s == nil || s.artifactStore == nil {
		return stages
	}
	for i := range stages {
		if len(stages[i].Artifacts) == 0 {
			continue
		}
		stages[i].Artifacts = s.externalizeWorkflowRunArtifactsLocked(stages[i].Artifacts, completedAt)
	}
	return stages
}

func (s *State) externalizeWorkflowRunArtifactsLocked(artifacts []WorkflowRunArtifact, createdAt string) []WorkflowRunArtifact {
	if len(artifacts) == 0 || s == nil || s.artifactStore == nil {
		return artifacts
	}
	out := make([]WorkflowRunArtifact, len(artifacts))
	for i, artifact := range artifacts {
		out[i] = s.externalizeWorkflowRunArtifactLocked(artifact, createdAt)
	}
	return out
}

func (s *State) externalizeWorkflowRunArtifactLocked(artifact WorkflowRunArtifact, createdAt string) WorkflowRunArtifact {
	if s == nil || s.artifactStore == nil || strings.TrimSpace(artifact.Content) == "" {
		return normalizeWorkflowRunArtifactReferenceMetadata(artifact)
	}
	mime := firstWorkflowArtifactValue(artifact.Mime, artifact.Metadata["mime"], workflowRunArtifactMime(artifact.Kind))
	metadata := copyWorkflowRunStringMap(artifact.Metadata)
	object, deduplicated, err := s.artifactStore.Put(ArtifactObject{
		CreatedAt: createdAt,
		Mime:      mime,
		Summary:   artifact.Summary,
		Content:   artifact.Content,
		Kind:      artifact.Kind,
		Title:     artifact.Title,
		Metadata:  metadata,
	})
	if err != nil {
		return normalizeWorkflowRunArtifactReferenceMetadata(artifact)
	}
	artifact.ArtifactRef = object.Ref
	artifact.Hash = object.Hash
	artifact.Mime = object.Mime
	artifact.Size = object.Size
	artifact.ContentBytes = object.Size
	artifact.StoredBytes = int(object.StoredBytes)
	artifact.Deduplicated = deduplicated
	artifact.Externalized = true
	artifact.Content = ""
	if strings.TrimSpace(artifact.Summary) == "" {
		artifact.Summary = object.Summary
	}
	return normalizeWorkflowRunArtifactReferenceMetadata(artifact)
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
			Content: stage.Result.Output,
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
			Content:    result.Content,
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
			Content: content,
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
			Content: content,
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
			Content: change.Summary,
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
			Content: content,
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
			Content: content,
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
		artifact = normalizeWorkflowRunArtifactReferenceMetadata(artifact)
		out[i] = artifact
	}
	return out
}

func normalizeWorkflowRunArtifactReferenceMetadata(artifact WorkflowRunArtifact) WorkflowRunArtifact {
	if strings.TrimSpace(artifact.Mime) == "" {
		artifact.Mime = firstWorkflowArtifactValue(artifact.Metadata["mime"], workflowRunArtifactMime(artifact.Kind))
	}
	if strings.TrimSpace(artifact.ArtifactRef) == "" {
		artifact.ArtifactRef = firstWorkflowArtifactValue(artifact.Metadata["artifact_ref"])
	}
	if strings.TrimSpace(artifact.Hash) == "" {
		artifact.Hash = firstWorkflowArtifactValue(artifact.Metadata["hash"], workflowArtifactHashFromRef(artifact.ArtifactRef))
	}
	if artifact.ContentBytes == 0 && strings.TrimSpace(artifact.Content) != "" {
		artifact.ContentBytes = len([]byte(artifact.Content))
	}
	if artifact.Size == 0 {
		artifact.Size = artifact.ContentBytes
	}
	artifact.Externalized = artifact.Externalized || strings.TrimSpace(artifact.ArtifactRef) != "" || strings.TrimSpace(artifact.Hash) != ""
	if strings.TrimSpace(artifact.ArtifactRef) == "" && strings.TrimSpace(artifact.Hash) == "" && strings.TrimSpace(artifact.Mime) == "" && artifact.Size == 0 && artifact.ContentBytes == 0 && artifact.StoredBytes == 0 && !artifact.Externalized {
		return artifact
	}
	if artifact.Metadata == nil {
		artifact.Metadata = map[string]string{}
	} else {
		artifact.Metadata = copyWorkflowRunStringMap(artifact.Metadata)
	}
	workflowArtifactSetMetadata(artifact.Metadata, "artifact_ref", artifact.ArtifactRef)
	workflowArtifactSetMetadata(artifact.Metadata, "hash", artifact.Hash)
	workflowArtifactSetMetadata(artifact.Metadata, "mime", artifact.Mime)
	workflowArtifactSetMetadata(artifact.Metadata, "size", workflowArtifactIntString(artifact.Size))
	workflowArtifactSetMetadata(artifact.Metadata, "content_bytes", workflowArtifactIntString(artifact.ContentBytes))
	workflowArtifactSetMetadata(artifact.Metadata, "stored_bytes", workflowArtifactIntString(artifact.StoredBytes))
	if artifact.Externalized {
		workflowArtifactSetMetadata(artifact.Metadata, "externalized", "true")
	}
	if len(artifact.Metadata) == 0 {
		artifact.Metadata = nil
	}
	return artifact
}

func workflowRunArtifactMime(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "structured", "finding", "change", "verification", "acceptance", "audit":
		return "application/json"
	case "output", "report", "summary":
		return "text/markdown"
	default:
		return "text/plain"
	}
}

func workflowArtifactHashFromRef(ref string) string {
	ref = strings.TrimSpace(ref)
	if strings.HasPrefix(ref, "sha256:") {
		return strings.TrimPrefix(ref, "sha256:")
	}
	return ""
}

func workflowArtifactIntString(value int) string {
	if value <= 0 {
		return ""
	}
	return fmt.Sprintf("%d", value)
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
	result.ResponseMessage = schema.CopyMessage(result.ResponseMessage)
	return result
}
