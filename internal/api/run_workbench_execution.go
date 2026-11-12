package api

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

const (
	runWorkbenchSliceLimit       = 12
	runWorkbenchSliceListLimit   = 8
	runWorkbenchCheckLimit       = 12
	runWorkbenchArtifactLimit    = 8
	runWorkbenchBlockerLimit     = 6
	runWorkbenchShortTextLimit   = 240
	runWorkbenchRequestTextLimit = 800
)

type runWorkbenchExecution struct {
	Contract         runWorkbenchContract          `json:"contract"`
	Outcome          runWorkbenchOutcome           `json:"outcome"`
	Previews         []runPreview                  `json:"previews,omitempty"`
	CurrentSlice     *runWorkbenchChangeSlice      `json:"current_slice,omitempty"`
	ChangeSlices     []runWorkbenchChangeSlice     `json:"change_slices,omitempty"`
	Blockers         []string                      `json:"blockers,omitempty"`
	Verification     []runWorkbenchVerification    `json:"verification,omitempty"`
	Artifacts        []runWorkbenchArtifactSummary `json:"artifacts,omitempty"`
	OmittedSlices    int                           `json:"omitted_slices,omitempty"`
	OmittedChecks    int                           `json:"omitted_verification,omitempty"`
	OmittedArtifacts int                           `json:"omitted_artifacts,omitempty"`
}

type runWorkbenchOutcome struct {
	Conclusion     string   `json:"conclusion"`
	QualityScore   int      `json:"quality_score"`
	DurationMs     int64    `json:"duration_ms,omitempty"`
	ModelCalls     int      `json:"model_calls,omitempty"`
	ToolCalls      int      `json:"tool_calls,omitempty"`
	ChangeCount    int      `json:"change_count,omitempty"`
	ArtifactCount  int      `json:"artifact_count,omitempty"`
	ChangedFiles   []string `json:"changed_files,omitempty"`
	Unverified     []string `json:"unverified,omitempty"`
	RemainingRisks []string `json:"remaining_risks,omitempty"`
}

type runWorkbenchContract struct {
	Request      string `json:"request,omitempty"`
	Workflow     string `json:"workflow,omitempty"`
	Status       string `json:"status,omitempty"`
	CurrentStage string `json:"current_stage,omitempty"`
	TargetID     string `json:"target_id,omitempty"`
}

type runWorkbenchChangeSlice struct {
	ID                   string   `json:"id"`
	Title                string   `json:"title"`
	ExecutionMode        string   `json:"execution_mode,omitempty"`
	Status               string   `json:"status"`
	DependsOn            []string `json:"depends_on,omitempty"`
	TargetFiles          []string `json:"target_files,omitempty"`
	ChangedFiles         []string `json:"changed_files,omitempty"`
	VerificationCommands []string `json:"verification_commands,omitempty"`
	EvidenceStages       []string `json:"evidence_stages,omitempty"`
	BlockedReason        string   `json:"blocked_reason,omitempty"`
	Attempt              int      `json:"attempt,omitempty"`
}

type runWorkbenchVerification struct {
	Stage  string `json:"stage,omitempty"`
	Kind   string `json:"kind,omitempty"`
	Name   string `json:"name,omitempty"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type runWorkbenchArtifactSummary struct {
	ID          string `json:"id"`
	Stage       string `json:"stage,omitempty"`
	Kind        string `json:"kind,omitempty"`
	Title       string `json:"title,omitempty"`
	Summary     string `json:"summary,omitempty"`
	ArtifactRef string `json:"artifact_ref,omitempty"`
	Mime        string `json:"mime,omitempty"`
	Size        int    `json:"size,omitempty"`
}

func workflowRunWorkbenchExecution(run session.WorkflowRunSnapshot, delivery runDeliveryManifest) *runWorkbenchExecution {
	var processResults []schema.ToolResult
	for _, stage := range run.CompletedStages {
		processResults = append(processResults, stage.Result.ToolResults...)
	}
	for _, event := range run.Events {
		if event.Type == "tool_result" {
			processResults = append(processResults, schema.ToolResult{ToolName: event.ToolName, Content: event.Content})
		}
	}
	execution := &runWorkbenchExecution{
		Previews: runPreviews(processResults),
		Contract: runWorkbenchContract{
			Request:      boundedWorkbenchText(run.Request, runWorkbenchRequestTextLimit),
			Workflow:     boundedWorkbenchText(run.Name, runWorkbenchShortTextLimit),
			Status:       strings.TrimSpace(run.Status),
			CurrentStage: boundedWorkbenchText(run.NextStage, runWorkbenchShortTextLimit),
			TargetID:     boundedWorkbenchText(run.TargetID, runWorkbenchShortTextLimit),
		},
		Outcome: summarizeWorkbenchOutcome(delivery),
	}
	for _, item := range run.ChangeSlices {
		if len(execution.ChangeSlices) >= runWorkbenchSliceLimit {
			execution.OmittedSlices++
			continue
		}
		slice := summarizeWorkbenchChangeSlice(item)
		execution.ChangeSlices = append(execution.ChangeSlices, slice)
		if execution.CurrentSlice == nil && workbenchSliceIsCurrent(slice.Status) {
			copy := slice
			execution.CurrentSlice = &copy
		}
		if slice.BlockedReason != "" {
			execution.Blockers = appendBoundedUnique(execution.Blockers, slice.BlockedReason, runWorkbenchBlockerLimit)
		}
	}
	if execution.CurrentSlice == nil {
		for i := range execution.ChangeSlices {
			if !workbenchSliceIsComplete(execution.ChangeSlices[i].Status) {
				copy := execution.ChangeSlices[i]
				execution.CurrentSlice = &copy
				break
			}
		}
	}
	if execution.CurrentSlice == nil && len(execution.ChangeSlices) > 0 {
		copy := execution.ChangeSlices[len(execution.ChangeSlices)-1]
		execution.CurrentSlice = &copy
	}
	if strings.TrimSpace(run.ApprovalPrompt) != "" {
		execution.Blockers = appendBoundedUnique(execution.Blockers, boundedWorkbenchText(run.ApprovalPrompt, runWorkbenchShortTextLimit), runWorkbenchBlockerLimit)
	}
	checks := workbenchVerificationChecks(delivery)
	for _, check := range checks {
		if len(execution.Verification) >= runWorkbenchCheckLimit {
			execution.OmittedChecks++
			continue
		}
		execution.Verification = append(execution.Verification, runWorkbenchVerification{
			Stage:  boundedWorkbenchText(check.Stage, runWorkbenchShortTextLimit),
			Kind:   boundedWorkbenchText(check.Kind, runWorkbenchShortTextLimit),
			Name:   boundedWorkbenchText(workbenchCheckName(check), runWorkbenchShortTextLimit),
			Status: strings.TrimSpace(check.Status),
			Detail: boundedWorkbenchText(workbenchCheckDetail(check), runWorkbenchShortTextLimit),
		})
	}
	for _, artifact := range workbenchArtifacts(delivery.Artifacts) {
		if len(execution.Artifacts) >= runWorkbenchArtifactLimit {
			execution.OmittedArtifacts++
			continue
		}
		execution.Artifacts = append(execution.Artifacts, runWorkbenchArtifactSummary{
			ID:          boundedWorkbenchText(artifact.ID, runWorkbenchShortTextLimit),
			Stage:       boundedWorkbenchText(artifact.Stage, runWorkbenchShortTextLimit),
			Kind:        boundedWorkbenchText(artifact.Kind, runWorkbenchShortTextLimit),
			Title:       boundedWorkbenchText(artifact.Title, runWorkbenchShortTextLimit),
			Summary:     boundedWorkbenchText(artifact.Summary, runWorkbenchShortTextLimit),
			ArtifactRef: boundedWorkbenchText(artifact.ArtifactRef, runWorkbenchRequestTextLimit),
			Mime:        boundedWorkbenchText(artifact.Mime, runWorkbenchShortTextLimit),
			Size:        artifact.Size,
		})
	}
	return execution
}

// agentRunWorkbenchExecution exposes the same bounded evidence summary as a
// workflow run. Ordinary agents do not have workflow change slices, but they
// still produce a durable contract, verification, blockers, and artifacts.
func agentRunWorkbenchExecution(run session.AgentRunSnapshot, delivery runDeliveryManifest) *runWorkbenchExecution {
	var processResults []schema.ToolResult
	if run.Result != nil {
		processResults = append(processResults, run.Result.ToolResults...)
	}
	for _, event := range run.Events {
		if event.Type == "tool_result" {
			processResults = append(processResults, schema.ToolResult{ToolName: event.ToolName, Content: event.Content})
		}
	}
	execution := &runWorkbenchExecution{
		Previews: runPreviews(processResults),
		Contract: runWorkbenchContract{
			Request:  boundedWorkbenchText(run.Request, runWorkbenchRequestTextLimit),
			Workflow: boundedWorkbenchText(run.AgentID, runWorkbenchShortTextLimit),
			Status:   strings.TrimSpace(run.Status),
			TargetID: boundedWorkbenchText(run.TargetID, runWorkbenchShortTextLimit),
		},
		Outcome: summarizeWorkbenchOutcome(delivery),
	}
	if strings.TrimSpace(run.Error) != "" {
		execution.Blockers = appendBoundedUnique(execution.Blockers, run.Error, runWorkbenchBlockerLimit)
	}
	for _, approval := range run.PendingApprovals {
		label := firstWorkflowRunQueryValue(approval.ToolName, approval.ToolServer, approval.Stage, "等待审批")
		execution.Blockers = appendBoundedUnique(execution.Blockers, boundedWorkbenchText(label, runWorkbenchShortTextLimit), runWorkbenchBlockerLimit)
	}
	checks := workbenchVerificationChecks(delivery)
	for _, check := range checks {
		if len(execution.Verification) >= runWorkbenchCheckLimit {
			execution.OmittedChecks++
			continue
		}
		execution.Verification = append(execution.Verification, runWorkbenchVerification{
			Stage:  boundedWorkbenchText(check.Stage, runWorkbenchShortTextLimit),
			Kind:   boundedWorkbenchText(check.Kind, runWorkbenchShortTextLimit),
			Name:   boundedWorkbenchText(workbenchCheckName(check), runWorkbenchShortTextLimit),
			Status: strings.TrimSpace(check.Status),
			Detail: boundedWorkbenchText(workbenchCheckDetail(check), runWorkbenchShortTextLimit),
		})
	}
	for _, artifact := range workbenchArtifacts(delivery.Artifacts) {
		if len(execution.Artifacts) >= runWorkbenchArtifactLimit {
			execution.OmittedArtifacts++
			continue
		}
		execution.Artifacts = append(execution.Artifacts, runWorkbenchArtifactSummary{
			ID:          boundedWorkbenchText(artifact.ID, runWorkbenchShortTextLimit),
			Stage:       boundedWorkbenchText(artifact.Stage, runWorkbenchShortTextLimit),
			Kind:        boundedWorkbenchText(artifact.Kind, runWorkbenchShortTextLimit),
			Title:       boundedWorkbenchText(artifact.Title, runWorkbenchShortTextLimit),
			Summary:     boundedWorkbenchText(artifact.Summary, runWorkbenchShortTextLimit),
			ArtifactRef: boundedWorkbenchText(artifact.ArtifactRef, runWorkbenchRequestTextLimit),
			Mime:        boundedWorkbenchText(artifact.Mime, runWorkbenchShortTextLimit),
			Size:        artifact.Size,
		})
	}
	return execution
}

func summarizeWorkbenchOutcome(delivery runDeliveryManifest) runWorkbenchOutcome {
	changedFiles := make([]string, 0, min(len(delivery.Changes), runWorkbenchSliceListLimit))
	for _, change := range delivery.Changes {
		if change.IsError || !workbenchChangeStatus(change.Status) {
			continue
		}
		path := boundedWorkbenchText(change.Path, runWorkbenchShortTextLimit)
		if path == "" {
			continue
		}
		duplicate := false
		for _, existing := range changedFiles {
			if existing == path {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		changedFiles = append(changedFiles, path)
		if len(changedFiles) >= runWorkbenchSliceListLimit {
			break
		}
	}
	return runWorkbenchOutcome{
		Conclusion:     strings.TrimSpace(delivery.Conclusion),
		QualityScore:   delivery.Quality.Score,
		DurationMs:     delivery.Metrics.DurationMs,
		ModelCalls:     delivery.Metrics.LLMCalls,
		ToolCalls:      delivery.Metrics.ToolCalls,
		ChangeCount:    len(changedFiles),
		ArtifactCount:  delivery.Metrics.Artifacts,
		ChangedFiles:   changedFiles,
		Unverified:     boundedWorkbenchStrings(delivery.Unverified, 4),
		RemainingRisks: boundedWorkbenchStrings(delivery.RemainingRisks, 4),
	}
}

func workbenchVerificationChecks(delivery runDeliveryManifest) []runDeliveryCheck {
	candidates := append([]runDeliveryCheck(nil), delivery.Acceptance...)
	for _, check := range delivery.Verification {
		kind := normalizeWorkflowRunQueryToken(check.Kind)
		status := normalizeWorkflowRunQueryToken(check.Status)
		if kind == "response" || kind == "plan" {
			continue
		}
		if kind == "tool" && status == "passed" {
			continue
		}
		candidates = append(candidates, check)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return workbenchCheckRank(candidates[i]) < workbenchCheckRank(candidates[j])
	})
	out := make([]runDeliveryCheck, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, check := range candidates {
		key := strings.Join([]string{check.Stage, check.Kind, check.Name, check.Status, check.Detail}, "\x00")
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, check)
	}
	return out
}

func workbenchCheckRank(check runDeliveryCheck) int {
	if normalizeWorkflowRunQueryToken(check.Kind) == "verifier" {
		return 0
	}
	switch normalizeWorkflowRunQueryToken(check.Status) {
	case "failed", "failure", "error", "blocked", "denied":
		return 1
	case "passed", "success", "succeeded", "verified", "completed":
		return 3
	default:
		return 2
	}
}

func workbenchArtifacts(items []runDeliveryArtifact) []runDeliveryArtifact {
	out := make([]runDeliveryArtifact, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		kind := normalizeWorkflowRunQueryToken(item.Kind)
		includeError := item.IsError && kind != "tool_result" && kind != "audit"
		if includeError || kind == "" || kind == "output" || kind == "verification" || kind == "diff" || kind == "report" || kind == "document" || kind == "test" || kind == "evidence" {
			key := strings.Join([]string{kind, item.Title, item.Summary}, "\x00")
			if kind == "verification" {
				key = strings.Join([]string{kind, item.Title}, "\x00")
			}
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, item)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return workbenchArtifactRank(out[i]) < workbenchArtifactRank(out[j])
	})
	return out
}

func workbenchChangeStatus(status string) bool {
	switch normalizeWorkflowRunQueryToken(status) {
	case "added", "created", "modified", "changed", "deleted", "removed", "renamed", "moved":
		return true
	default:
		return false
	}
}

func workbenchCheckDetail(check runDeliveryCheck) string {
	detail := strings.TrimSpace(firstWorkflowRunQueryValue(check.Detail, check.Actual))
	separator := strings.Index(detail, ": {")
	if separator <= 0 {
		return detail
	}
	toolName := strings.TrimSpace(detail[:separator])
	var payload map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(detail[separator+2:])), &payload); err != nil {
		if toolName == "run_command" {
			return workbenchTruncatedCommandDetail(detail, check.Status)
		}
		return detail
	}
	if toolName != "run_command" {
		return firstWorkflowRunQueryValue(workbenchPayloadString(payload, "content"), workbenchPayloadString(payload, "recommendation"), detail)
	}
	parts := []string{toolName}
	program := strings.TrimSpace(fmt.Sprint(payload["program"]))
	args := make([]string, 0)
	if values, ok := payload["args"].([]any); ok {
		for _, value := range values {
			args = append(args, fmt.Sprint(value))
		}
	}
	command := strings.TrimSpace(strings.Join(append([]string{program}, args...), " "))
	if command != "" && command != "<nil>" {
		parts = append(parts, command)
	}
	if exit, ok := payload["exit_code"]; ok {
		parts = append(parts, "exit "+fmt.Sprint(exit))
	}
	if duration, ok := payload["duration_ms"]; ok {
		parts = append(parts, fmt.Sprint(duration)+" ms")
	}
	message := firstWorkflowRunQueryValue(workbenchPayloadString(payload, "recommendation"), workbenchPayloadString(payload, "stderr"), workbenchPayloadString(payload, "stdout"))
	if message != "" {
		parts = append(parts, strings.Join(strings.Fields(message), " "))
	}
	return strings.Join(parts, " · ")
}

func workbenchTruncatedCommandDetail(detail, status string) string {
	parts := []string{"run_command"}
	argsStart := strings.Index(detail, `"args":[`)
	if argsStart >= 0 {
		arrayStart := argsStart + len(`"args":`)
		if relativeEnd := strings.Index(detail[arrayStart:], "]"); relativeEnd > 0 {
			var args []string
			if json.Unmarshal([]byte(detail[arrayStart:arrayStart+relativeEnd+1]), &args) == nil && len(args) > 0 {
				program := ""
				if strings.HasPrefix(args[0], "-") {
					program = "python"
				}
				parts = append(parts, strings.TrimSpace(strings.Join(append([]string{program}, args...), " ")))
			}
		}
	}
	if status != "" {
		parts = append(parts, strings.TrimSpace(status))
	}
	return strings.Join(parts, " · ")
}

func workbenchCheckName(check runDeliveryCheck) string {
	if normalizeWorkflowRunQueryToken(check.Kind) == "tool" {
		if separator := strings.Index(check.Detail, ":"); separator > 0 {
			return strings.TrimSpace(check.Detail[:separator])
		}
	}
	return firstWorkflowRunQueryValue(check.Name, check.Kind, "verification")
}

func workbenchPayloadString(payload map[string]any, key string) string {
	value, exists := payload[key]
	if !exists || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func workbenchArtifactRank(item runDeliveryArtifact) int {
	if normalizeWorkflowRunQueryToken(item.Kind) == "verification" {
		return 0
	}
	if normalizeWorkflowRunQueryToken(item.Kind) == "output" {
		return 1
	}
	if item.IsError {
		return 2
	}
	return 3
}

func summarizeWorkbenchChangeSlice(item session.WorkflowChangeSliceSnapshot) runWorkbenchChangeSlice {
	return runWorkbenchChangeSlice{
		ID:                   boundedWorkbenchText(item.ID, runWorkbenchShortTextLimit),
		Title:                boundedWorkbenchText(item.Title, runWorkbenchShortTextLimit),
		ExecutionMode:        boundedWorkbenchText(item.ExecutionMode, runWorkbenchShortTextLimit),
		Status:               strings.TrimSpace(item.Status),
		DependsOn:            boundedWorkbenchStrings(item.DependsOn, runWorkbenchSliceListLimit),
		TargetFiles:          boundedWorkbenchStrings(item.TargetFiles, runWorkbenchSliceListLimit),
		ChangedFiles:         boundedWorkbenchStrings(item.ChangedFiles, runWorkbenchSliceListLimit),
		VerificationCommands: boundedWorkbenchStrings(item.VerificationCommands, runWorkbenchSliceListLimit),
		EvidenceStages:       boundedWorkbenchStrings(item.EvidenceStages, runWorkbenchSliceListLimit),
		BlockedReason:        boundedWorkbenchText(item.BlockedReason, runWorkbenchShortTextLimit),
		Attempt:              item.Attempt,
	}
}

func workbenchSliceIsCurrent(status string) bool {
	switch normalizeWorkflowRunQueryToken(status) {
	case "running", "in_progress", "active", "blocked", "awaiting_approval", "awaiting_input":
		return true
	default:
		return false
	}
}

func workbenchSliceIsComplete(status string) bool {
	switch normalizeWorkflowRunQueryToken(status) {
	case "completed", "succeeded", "verified", "skipped":
		return true
	default:
		return false
	}
}

func boundedWorkbenchStrings(values []string, limit int) []string {
	out := make([]string, 0, min(len(values), limit))
	for _, value := range values {
		value = boundedWorkbenchText(value, runWorkbenchShortTextLimit)
		if value == "" {
			continue
		}
		out = append(out, value)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func boundedWorkbenchText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if value == "" || limit <= 0 || utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return strings.TrimSpace(string(runes[:limit-1])) + "…"
}

func appendBoundedUnique(values []string, value string, limit int) []string {
	value = strings.TrimSpace(value)
	if value == "" || len(values) >= limit {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func workflowWorkbenchFinalOutput(run session.WorkflowRunSnapshot) string {
	if output := boundedWorkbenchText(run.Summary, 64*1024); output != "" {
		return output
	}
	for index := len(run.CompletedStages) - 1; index >= 0; index-- {
		if output := boundedWorkbenchText(run.CompletedStages[index].Result.Output, 64*1024); output != "" {
			return output
		}
	}
	return ""
}
