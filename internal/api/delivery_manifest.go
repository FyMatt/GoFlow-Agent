package api

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/agent"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

const runDeliverySchemaVersion = "goflow.delivery.v1"

type runDeliveryManifest struct {
	SchemaVersion  string                    `json:"schema_version"`
	RunID          string                    `json:"run_id"`
	RunType        string                    `json:"run_type"`
	Status         string                    `json:"status,omitempty"`
	Conclusion     string                    `json:"conclusion"`
	Request        string                    `json:"request,omitempty"`
	Summary        string                    `json:"summary,omitempty"`
	Acceptance     []runDeliveryCheck        `json:"acceptance,omitempty"`
	Verification   []runDeliveryCheck        `json:"verification,omitempty"`
	Changes        []runDiffItem             `json:"changes,omitempty"`
	Evidence       []runDeliveryEvidence     `json:"evidence,omitempty"`
	Artifacts      []runDeliveryArtifact     `json:"artifacts,omitempty"`
	HardGates      []runDeliveryGate         `json:"hard_gates"`
	Unverified     []string                  `json:"unverified,omitempty"`
	RemainingRisks []string                  `json:"remaining_risks,omitempty"`
	Metrics        runDeliveryMetrics        `json:"metrics"`
	Quality        runDeliveryQualitySummary `json:"quality"`
}

type runDeliveryCheck struct {
	Stage    string `json:"stage,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Name     string `json:"name,omitempty"`
	Expected string `json:"expected,omitempty"`
	Actual   string `json:"actual,omitempty"`
	Status   string `json:"status"`
	Detail   string `json:"detail,omitempty"`
}

type runDeliveryEvidence struct {
	ID          string `json:"id,omitempty"`
	Stage       string `json:"stage,omitempty"`
	Category    string `json:"category"`
	Strength    string `json:"strength"`
	Title       string `json:"title,omitempty"`
	Summary     string `json:"summary,omitempty"`
	ToolName    string `json:"tool_name,omitempty"`
	ToolCallID  string `json:"tool_call_id,omitempty"`
	ArtifactRef string `json:"artifact_ref,omitempty"`
	Hash        string `json:"hash,omitempty"`
	Mime        string `json:"mime,omitempty"`
	IsError     bool   `json:"is_error,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

type runDeliveryArtifact struct {
	ID          string `json:"id"`
	Stage       string `json:"stage,omitempty"`
	Kind        string `json:"kind,omitempty"`
	Title       string `json:"title,omitempty"`
	Summary     string `json:"summary,omitempty"`
	ArtifactRef string `json:"artifact_ref,omitempty"`
	Hash        string `json:"hash,omitempty"`
	Mime        string `json:"mime,omitempty"`
	Size        int    `json:"size,omitempty"`
	ToolName    string `json:"tool_name,omitempty"`
	ToolCallID  string `json:"tool_call_id,omitempty"`
	IsError     bool   `json:"is_error,omitempty"`
}

type runDeliveryGate struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
	Passed   bool   `json:"passed"`
	Status   string `json:"status"`
	Detail   string `json:"detail,omitempty"`
}

type runDeliveryMetrics struct {
	DurationMs   int64 `json:"duration_ms,omitempty"`
	PromptTokens int   `json:"prompt_tokens,omitempty"`
	OutputTokens int   `json:"output_tokens,omitempty"`
	CachedTokens int   `json:"cached_tokens,omitempty"`
	TotalTokens  int   `json:"total_tokens,omitempty"`
	LLMCalls     int   `json:"llm_calls,omitempty"`
	ToolCalls    int   `json:"tool_calls,omitempty"`
	Artifacts    int   `json:"artifacts,omitempty"`
	Changes      int   `json:"changes,omitempty"`
}

type runDeliveryQualitySummary struct {
	Score               int `json:"score"`
	StrongEvidence      int `json:"strong_evidence"`
	WeakEvidence        int `json:"weak_evidence"`
	ToolErrors          int `json:"tool_errors"`
	RecoveredToolErrors int `json:"recovered_tool_errors,omitempty"`
	AcceptanceFailed    int `json:"acceptance_failed"`
	VerificationFailed  int `json:"verification_failed"`
	VerificationUnknown int `json:"verification_unknown"`
}

type runDeliveryFacts struct {
	status               string
	needsAction          bool
	active               bool
	terminal             bool
	toolErrors           int
	recoveredToolErrors  int
	acceptanceFailed     int
	verificationFailed   int
	verificationUnknown  int
	strongEvidence       int
	weakEvidence         int
	changesClaimed       bool
	deliverableEvidence  int
	documentaryEvidence  int
	verificationEvidence int
}

func (s *Server) agentRunDeliveryManifest(run session.AgentRunSnapshot) runDeliveryManifest {
	artifacts := s.hydrateDeliveryCommandEvidence(agentRunArtifactsForQuery(run, workflowRunQuery{IncludeContent: true}))
	diffs := runDeliveryChanges(s.agentRunDiffs(run, agentRunQuery{}))
	manifest := runDeliveryManifest{
		SchemaVersion: runDeliverySchemaVersion,
		RunID:         strings.TrimSpace(run.ID),
		RunType:       "agent",
		Status:        strings.TrimSpace(run.Status),
		Request:       strings.TrimSpace(run.Request),
		Summary:       firstWorkflowRunQueryValue(strings.TrimSpace(run.Output), strings.TrimSpace(run.Error)),
		Changes:       summarizeRunDiffItems(diffs),
		Artifacts:     runDeliveryArtifacts(artifacts),
		Metrics:       agentRunDeliveryMetrics(run, artifacts, diffs),
	}
	if run.Result != nil {
		manifest.Verification = append(manifest.Verification, deliveryChecksFromVerifications("", run.Result.Verification)...)
	}
	manifest.Evidence = runDeliveryEvidenceItems(artifacts)
	facts := runDeliveryFactsForAgent(run, manifest, artifacts)
	return finalizeRunDeliveryManifest(manifest, facts)
}

func (s *Server) workflowRunDeliveryManifest(run session.WorkflowRunSnapshot) runDeliveryManifest {
	run.Status = effectiveWorkflowRunStatus(run)
	artifacts := s.hydrateDeliveryCommandEvidence(workflowRunArtifactsAsRunItems(run, workflowRunQuery{IncludeContent: true}))
	diffs := runDeliveryChanges(s.workflowRunDiffs(run, workflowRunQuery{}))
	manifest := runDeliveryManifest{
		SchemaVersion: runDeliverySchemaVersion,
		RunID:         strings.TrimSpace(run.ID),
		RunType:       "workflow",
		Status:        strings.TrimSpace(run.Status),
		Request:       strings.TrimSpace(run.Request),
		Summary:       strings.TrimSpace(run.Summary),
		Changes:       summarizeRunDiffItems(diffs),
		Artifacts:     runDeliveryArtifacts(artifacts),
		Metrics:       workflowRunDeliveryMetrics(run, artifacts, diffs),
	}
	for _, stage := range run.CompletedStages {
		manifest.Acceptance = append(manifest.Acceptance, deliveryChecksFromAcceptance(stage.Stage, stage.Acceptance)...)
		manifest.Verification = append(manifest.Verification, deliveryChecksFromVerifications(stage.Stage, stage.Result.Verification)...)
	}
	manifest.Evidence = runDeliveryEvidenceItems(artifacts)
	facts := runDeliveryFactsForWorkflow(run, manifest, artifacts)
	return finalizeRunDeliveryManifest(manifest, facts)
}

// Command evidence must use the full payload: display summaries can truncate
// the program, arguments or exit status. Only hydrate command objects here.
func (s *Server) hydrateDeliveryCommandEvidence(items []runArtifactItem) []runArtifactItem {
	for i, item := range items {
		name := normalizeWorkflowRunQueryToken(item.ToolName)
		if name == "run_command" || strings.HasSuffix(name, "/run_command") {
			items[i] = s.hydrateRunArtifactItem(item)
		}
	}
	return items
}

func runDeliveryChanges(items []runDiffItem) []runDiffItem {
	out := make([]runDiffItem, 0, len(items))
	for _, item := range items {
		traceable := strings.TrimSpace(item.ToolCallID) != "" ||
			strings.TrimSpace(item.ChangeID) != "" ||
			item.AddedLines != 0 || item.DeletedLines != 0 || item.BytesWritten != 0 ||
			runDeliveryLooksLikePatch(item.DiffPreview) || runDeliveryLooksLikePatch(item.Patch)
		if !traceable || strings.TrimSpace(item.Path) == "" {
			continue
		}
		out = append(out, item)
	}
	return out
}

func runDeliveryLooksLikePatch(value string) bool {
	value = strings.TrimSpace(value)
	return strings.Contains(value, "@@") ||
		(strings.Contains(value, "\n+") && strings.Contains(value, "\n-")) ||
		(strings.HasPrefix(value, "+") && strings.Contains(value, "\n-")) ||
		(strings.HasPrefix(value, "-") && strings.Contains(value, "\n+"))
}

func deliveryChecksFromAcceptance(stage string, items []session.WorkflowRunAcceptanceSnapshot) []runDeliveryCheck {
	out := make([]runDeliveryCheck, 0, len(items))
	for _, item := range items {
		out = append(out, runDeliveryCheck{
			Stage:    strings.TrimSpace(stage),
			Kind:     "acceptance",
			Name:     firstWorkflowRunQueryValue(item.Name, item.Ref, "acceptance"),
			Expected: strings.TrimSpace(item.Expected),
			Actual:   strings.TrimSpace(item.Actual),
			Status:   deliveryCheckStatus(item.Status),
			Detail:   firstWorkflowRunQueryValue(item.Reason, item.Description),
		})
	}
	return out
}

func deliveryChecksFromVerifications(stage string, items []schema.Verification) []runDeliveryCheck {
	out := make([]runDeliveryCheck, 0, len(items))
	for _, item := range items {
		kind := firstWorkflowRunQueryValue(item.Kind, "verification")
		status := deliveryCheckStatus(item.Status)
		if strings.EqualFold(kind, "verifier") && status != "failed" {
			status = deliveryCheckStatus(agent.VerifierStatus(item.Detail))
		}
		// Plan artifacts describe intended work; "proposed" is their normal
		// lifecycle state and is not an unresolved verification result.
		if normalizeWorkflowRunQueryToken(kind) == "plan" && (status == "proposed" || status == "planned") {
			continue
		}
		// A recorded internal reflection is an action, not an unresolved test.
		if normalizeWorkflowRunQueryToken(kind) == "reflection" && status == "performed" {
			continue
		}
		out = append(out, runDeliveryCheck{
			Stage:  strings.TrimSpace(stage),
			Kind:   kind,
			Name:   kind,
			Status: status,
			Detail: strings.TrimSpace(item.Detail),
		})
	}
	return out
}

func deliveryCheckStatus(status string) string {
	status = normalizeWorkflowRunQualityStatus(status)
	switch {
	case workflowRunQualityStatusPassed(status):
		return "passed"
	case workflowRunQualityStatusFailed(status):
		return "failed"
	case status == "":
		return "unknown"
	default:
		return status
	}
}

func runDeliveryArtifacts(items []runArtifactItem) []runDeliveryArtifact {
	out := make([]runDeliveryArtifact, 0, len(items))
	for _, item := range items {
		out = append(out, runDeliveryArtifact{
			ID:          strings.TrimSpace(item.ID),
			Stage:       strings.TrimSpace(item.Stage),
			Kind:        strings.TrimSpace(item.Kind),
			Title:       strings.TrimSpace(item.Title),
			Summary:     strings.TrimSpace(item.Summary),
			ArtifactRef: firstWorkflowRunQueryValue(item.ArtifactRef, item.Ref),
			Hash:        strings.TrimSpace(item.Hash),
			Mime:        strings.TrimSpace(item.Mime),
			Size:        firstPositiveInt(item.Size, item.ContentBytes),
			ToolName:    strings.TrimSpace(item.ToolName),
			ToolCallID:  strings.TrimSpace(item.ToolCallID),
			IsError:     runDeliveryArtifactBlocks(item),
		})
	}
	return out
}

func runDeliveryEvidenceItems(items []runArtifactItem) []runDeliveryEvidence {
	out := make([]runDeliveryEvidence, 0, len(items))
	for _, item := range items {
		strength, reason := runArtifactEvidenceStrength(item)
		out = append(out, runDeliveryEvidence{
			ID:          strings.TrimSpace(item.ID),
			Stage:       strings.TrimSpace(item.Stage),
			Category:    runArtifactEvidenceCategory(item),
			Strength:    strength,
			Title:       strings.TrimSpace(item.Title),
			Summary:     strings.TrimSpace(item.Summary),
			ToolName:    strings.TrimSpace(item.ToolName),
			ToolCallID:  strings.TrimSpace(item.ToolCallID),
			ArtifactRef: firstWorkflowRunQueryValue(item.ArtifactRef, item.Ref),
			Hash:        strings.TrimSpace(item.Hash),
			Mime:        strings.TrimSpace(item.Mime),
			IsError:     runDeliveryArtifactBlocks(item),
			Reason:      reason,
		})
	}
	return out
}

func runArtifactEvidenceStrength(item runArtifactItem) (string, string) {
	if item.Suspended && !item.Denied {
		return "weak", "tool result was suspended pending approval and is not execution evidence"
	}
	if runDeliveryArtifactBlocks(item) {
		return "invalid", "tool or artifact is marked as an error"
	}
	if runDeliveryToolResultFailed(item) {
		return "invalid", "command exited unsuccessfully, timed out, or was cancelled"
	}
	kind := normalizeWorkflowRunQueryToken(item.Kind)
	toolCallID := strings.TrimSpace(item.ToolCallID)
	ref := firstWorkflowRunQueryValue(item.ArtifactRef, item.Ref, item.Hash)
	hasPayload := strings.TrimSpace(firstWorkflowRunQueryValue(item.Summary, item.Content, item.Title)) != "" || item.Size > 0 || item.ContentBytes > 0
	if kind == "tool_result" && toolCallID != "" && (ref != "" || hasPayload) {
		return "strong", "persisted tool result with a traceable tool call"
	}
	if runDeliveryStrongArtifactKind(kind) && ref != "" && hasPayload {
		return "strong", "content-addressed or externally referenced artifact"
	}
	if runDeliverySourceMetadata(item.Metadata) && (ref != "" || toolCallID != "") {
		return "strong", "artifact includes a traceable source reference"
	}
	if hasPayload {
		return "weak", "content is not backed by a tool call, source reference, or durable hash"
	}
	return "weak", "artifact has no inspectable payload"
}

func runDeliveryStrongArtifactKind(kind string) bool {
	switch normalizeWorkflowRunQueryToken(kind) {
	case "diff", "patch", "test", "tests", "build", "lint", "validation", "screenshot", "source", "document", "spreadsheet", "presentation", "pdf", "export":
		return true
	default:
		return false
	}
}

func runDeliverySourceMetadata(metadata map[string]string) bool {
	for _, key := range []string{"source_ref", "source_url", "url", "capture_ref", "screenshot_ref", "document_ref"} {
		if strings.TrimSpace(metadata[key]) != "" {
			return true
		}
	}
	return false
}

func runArtifactEvidenceCategory(item runArtifactItem) string {
	if runDeliveryArtifactBlocks(item) {
		return "error"
	}
	kind := normalizeWorkflowRunQueryToken(item.Kind)
	if kind == "tool_result" {
		if runDeliveryArtifactIsVerification(item) {
			return "verification"
		}
		if runDeliveryMutationTool(item.ToolName) {
			return "change"
		}
		return "tool"
	}
	switch kind {
	case "diff", "patch", "change":
		return "change"
	case "verification", "test", "tests", "build", "lint", "validation":
		return "verification"
	case "acceptance":
		return "acceptance"
	case "source", "citation", "web_capture":
		return "source"
	default:
		return "artifact"
	}
}

func runDeliveryMutationTool(name string) bool {
	name = normalizeWorkflowRunQueryToken(name)
	for _, token := range []string{
		"write_file", "apply_patch", "edit_file", "delete_file", "move_file", "copy_file", "config_apply",
		"office_write", "office_export", "create_docx", "create_xlsx", "create_pptx", "create_pdf",
		"fill_office_template", "replace_office_text", "merge_office", "export_office_pdf",
	} {
		if name == token || strings.Contains(name, token) {
			return true
		}
	}
	return false
}

func runDeliveryVerificationTool(name string) bool {
	name = normalizeWorkflowRunQueryToken(name)
	for _, token := range []string{"run_command", "test", "build", "lint", "vet", "check", "validate", "verify", "render", "export", "inspect", "probe"} {
		if name == token || strings.Contains(name, token) {
			return true
		}
	}
	return false
}

func runDeliveryArtifactIsVerification(item runArtifactItem) bool {
	if item.Suspended && !item.Denied {
		return false
	}
	name := normalizeWorkflowRunQueryToken(item.ToolName)
	if name == "run_command" || strings.HasSuffix(name, "/run_command") {
		payload, ok := runDeliveryCommandPayload(item)
		if !ok || runDeliveryCommandPayloadFailed(payload) {
			return false
		}
		program := strings.ToLower(strings.TrimSuffix(runDiffString(payload, "program"), ".exe"))
		args := runDeliveryStringSlice(payload["args"])
		joined := strings.ToLower(strings.Join(append([]string{program}, args...), " "))
		for _, token := range []string{
			" test", "pytest", "unittest", "py_compile", "go vet", "go build", "go fmt", "gofmt", " lint", "eslint", "ruff", "mypy", "tsc", "cargo check", "cargo clippy", "gradle check", "mvn verify", " validate", " verify", " render", " export",
		} {
			if strings.Contains(" "+joined, token) {
				return true
			}
		}
		return false
	}
	return runDeliveryVerificationTool(name)
}

func runDeliveryToolResultFailed(item runArtifactItem) bool {
	if item.Suspended && !item.Denied {
		return false
	}
	if runDeliveryPolicyInterventionText(item.Content + "\n" + item.Summary) {
		return false
	}
	name := normalizeWorkflowRunQueryToken(item.ToolName)
	if name != "run_command" && !strings.HasSuffix(name, "/run_command") {
		return false
	}
	payload, ok := runDeliveryCommandPayload(item)
	return ok && runDeliveryCommandPayloadFailed(payload)
}

func runDeliveryArtifactOutputPath(item runArtifactItem) string {
	for _, content := range []string{item.Content, item.Summary} {
		var payload map[string]any
		if json.Unmarshal([]byte(strings.TrimSpace(content)), &payload) != nil {
			continue
		}
		for _, key := range []string{"relative_path", "artifact_path", "output_path", "path"} {
			if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		}
	}
	return ""
}

func runDeliveryDocumentaryPath(value string) bool {
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(value))) {
	case ".md", ".json", ".csv", ".txt":
		return true
	default:
		return false
	}
}

func runDeliveryCommandPayload(item runArtifactItem) (map[string]any, bool) {
	for _, content := range []string{item.Content, item.Summary} {
		content = strings.TrimSpace(content)
		if content == "" {
			continue
		}
		decoder := json.NewDecoder(strings.NewReader(content))
		decoder.UseNumber()
		var payload map[string]any
		if err := decoder.Decode(&payload); err == nil && len(payload) > 0 {
			return payload, true
		}
	}
	return nil, false
}

func runDeliveryCommandPayloadFailed(payload map[string]any) bool {
	if exitCode, ok := runDeliveryInt(payload["exit_code"]); ok && exitCode != 0 {
		return true
	}
	return runDeliveryBool(payload["timed_out"]) || runDeliveryBool(payload["cancelled"])
}

func runDeliveryStringSlice(value any) []string {
	items, ok := value.([]any)
	if !ok {
		if stringsValue, ok := value.([]string); ok {
			return append([]string(nil), stringsValue...)
		}
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if value, ok := item.(string); ok && strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	return out
}

func runDeliveryInt(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case float64:
		return int(typed), true
	case json.Number:
		parsed, err := typed.Int64()
		return int(parsed), err == nil
	default:
		return 0, false
	}
}

func runDeliveryBool(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return workflowRunQueryBool(typed)
	default:
		return false
	}
}

func runDeliveryFactsForAgent(run session.AgentRunSnapshot, manifest runDeliveryManifest, artifacts []runArtifactItem) runDeliveryFacts {
	facts := runDeliveryFacts{
		status:      normalizeWorkflowRunQueryToken(run.Status),
		needsAction: agentRunCollectionRunNeedsAction(run),
		active:      agentRunCollectionRunActive(run),
		terminal:    agentRunCollectionRunTerminal(run),
		toolErrors:  boolCount(agentRunDeliveryHasBlockingError(run)),
	}
	if run.Result != nil {
		facts.changesClaimed = agent.ResultClaimsFileChanges(*run.Result)
	}
	applyRunDeliveryManifestFacts(&facts, manifest, artifacts)
	if facts.toolErrors > 0 && agentRunDeliveryHasRecoveredVerification(run, artifacts) {
		facts.recoveredToolErrors = facts.toolErrors
		facts.toolErrors = 0
		facts.verificationFailed = 0
		facts.verificationUnknown = 0
	}
	return facts
}

// A failed intermediate check is recoverable only when a later strong,
// tool-backed verification artifact proves the completed state. Denials,
// cancellations, timeouts, and final failed runs remain blocking.
func agentRunDeliveryHasRecoveredVerification(run session.AgentRunSnapshot, artifacts []runArtifactItem) bool {
	if normalizeWorkflowRunQueryToken(run.Status) != "completed" || run.Result == nil {
		return false
	}
	lastError := -1
	lastVerification := -1
	for index, item := range artifacts {
		if item.Denied && !runDeliveryPolicyInterventionText(item.Content+"\n"+item.Summary) {
			return false
		}
		if payload, ok := runDeliveryCommandPayload(item); ok && (runDeliveryBool(payload["timed_out"]) || runDeliveryBool(payload["cancelled"])) {
			return false
		}
		if runDeliveryArtifactBlocks(item) || runDeliveryToolResultFailed(item) {
			// Model-authored verification summaries are lower-trust rollups. Use
			// only traceable tool failures when ordering recovery evidence.
			if normalizeWorkflowRunQueryToken(item.Kind) == "tool_result" || strings.TrimSpace(item.ToolCallID) != "" {
				lastError = index
			}
			continue
		}
		strength, _ := runArtifactEvidenceStrength(item)
		if strength == "strong" && runDeliveryArtifactIsVerification(item) {
			lastVerification = index
		}
	}
	return lastError >= 0 && lastVerification > lastError
}

// Delivery quality uses terminal, execution-relevant failures only. Stream
// error flags attached to status/approval plumbing are historical control-flow
// noise once the run has successfully completed.
func agentRunDeliveryHasBlockingError(run session.AgentRunSnapshot) bool {
	switch normalizeWorkflowRunQueryToken(run.Status) {
	case "failed", "denied":
		return true
	}
	if strings.TrimSpace(run.Error) != "" {
		return true
	}
	for _, event := range run.Events {
		if event.Suspended || event.NeedsAction || !event.IsError {
			continue
		}
		switch normalizeWorkflowRunQueryToken(event.Type) {
		case "error", "tool_result":
			if agentRunEventIsRecoverableApprovalContextError(event.Content) {
				continue
			}
			if runDeliveryExploratoryReadMiss(event.ToolName, event.Content) {
				continue
			}
			return true
		}
	}
	if run.Result != nil {
		for _, result := range run.Result.ToolResults {
			if agentRunToolResultIsBlockingError(result) {
				return true
			}
		}
	}
	return false
}

func runDeliveryFactsForWorkflow(run session.WorkflowRunSnapshot, manifest runDeliveryManifest, artifacts []runArtifactItem) runDeliveryFacts {
	facts := runDeliveryFacts{
		status:      normalizeWorkflowRunQueryToken(run.Status),
		needsAction: workflowRunCollectionRunNeedsAction(run),
		active:      workflowRunStatusActive(run.Status),
		terminal:    workflowRunCollectionRunTerminal(run),
		toolErrors:  workflowRunErrorCount(run, workflowRunDiffs(run, workflowRunQuery{})),
	}
	for _, stage := range run.CompletedStages {
		if agent.ResultClaimsFileChanges(stage.Result) {
			facts.changesClaimed = true
		}
	}
	applyRunDeliveryManifestFacts(&facts, manifest, artifacts)
	// A complex workflow may record an intermediate failed command while the
	// repair loop is still running.  Once the run reaches a completed terminal
	// state, a later strong tool-backed verification supersedes that historical
	// failure.  Denials, cancellations and timeouts remain blocking.
	if facts.toolErrors > 0 && !workflowRunDeliveryHasBlockingDenial(artifacts) && (workflowRunDeliveryHasRecoveredVerification(run, artifacts) || workflowRunDeliveryHasAllowedRaceFallback(run, artifacts) || workflowRunDeliveryHasLaterCommandVerification(manifest)) && workflowRunDeliveryVerificationFailuresRecoverable(manifest) {
		facts.recoveredToolErrors = facts.toolErrors
		facts.toolErrors = 0
	}
	if facts.verificationFailed > 0 && workflowRunDeliveryVerificationFailuresRecoverable(manifest) {
		facts.verificationFailed = 0
		facts.verificationUnknown = 0
	}
	if facts.acceptanceFailed > 0 && (len(manifest.Changes) > 0 || facts.deliverableEvidence > 0) && workflowRunDeliveryAcceptanceFailuresRecoverable(manifest) {
		facts.acceptanceFailed = 0
	}
	return facts
}

func workflowRunDeliveryHasAllowedRaceFallback(run session.WorkflowRunSnapshot, artifacts []runArtifactItem) bool {
	if normalizeWorkflowRunQueryToken(run.Status) != "completed" {
		return false
	}
	hadRaceFailure, hadFallback, hadCIGate := false, false, false
	for _, item := range artifacts {
		if item.Denied && !runDeliveryPolicyInterventionText(item.Content+"\n"+item.Summary) {
			return false
		}
		// Record a successful ordinary test before classifying the artifact as
		// a failure. Some persisted command results carry an error-shaped
		// wrapper even though their exit status is zero.
		if payload, ok := runDeliveryCommandPayload(item); ok && !runDeliveryCommandPayloadFailed(payload) {
			name := normalizeWorkflowRunQueryToken(item.ToolName)
			if name == "run_command" || strings.HasSuffix(name, "/run_command") {
				args := runDeliveryStringSlice(payload["args"])
				joined := strings.ToLower(strings.Join(args, " "))
				if strings.Contains(joined, "test") && !strings.Contains(joined, "-race") {
					hadFallback = true
				}
			}
		}
		if runDeliveryArtifactBlocks(item) || runDeliveryToolResultFailed(item) {
			if strings.TrimSpace(item.ToolCallID) == "" {
				continue
			}
			if strings.Contains(normalizeWorkflowRunQueryToken(item.ToolName), "run_command") {
				payload, ok := runDeliveryCommandPayload(item)
				if !ok {
					return false
				}
				args := runDeliveryStringSlice(payload["args"])
				joined := strings.ToLower(strings.Join(args, " "))
				if !strings.Contains(joined, "-race") || !workflowRunDeliveryRaceInfrastructure(item) {
					return false
				}
				hadRaceFailure = true
			}
			continue
		}
		path := strings.ToLower(runDeliveryArtifactOutputPath(item))
		if runDeliveryMutationTool(item.ToolName) && (strings.HasSuffix(path, "/.github/workflows/race.yml") || strings.HasSuffix(path, "/.github/workflows/race.yaml") || strings.EqualFold(path, ".github/workflows/race.yml") || strings.EqualFold(path, ".github/workflows/race.yaml")) {
			hadCIGate = true
		}
	}
	if !hadCIGate {
		// Change records are also persisted on the completed stage result. A
		// write tool artifact may only contain a human summary, so use the
		// structured file list as an equivalent traceable CI-gate source.
		for _, stage := range run.CompletedStages {
			for _, change := range stage.Result.Changes {
				for _, file := range change.Files {
					path := strings.ReplaceAll(strings.TrimSpace(file), "\\", "/")
					if strings.EqualFold(path, ".github/workflows/race.yml") || strings.EqualFold(path, ".github/workflows/race.yaml") {
						hadCIGate = true
						break
					}
				}
				if hadCIGate {
					break
				}
			}
			if hadCIGate {
				break
			}
		}
	}
	if !hadCIGate {
		for _, diff := range workflowRunDiffs(run, workflowRunQuery{}) {
			path := strings.ReplaceAll(strings.TrimSpace(diff.Path), "\\", "/")
			if strings.EqualFold(path, ".github/workflows/race.yml") || strings.EqualFold(path, ".github/workflows/race.yaml") {
				hadCIGate = true
				break
			}
		}
	}
	// Some tasks do not request race verification at all, but a provider may
	// still attempt it opportunistically. Treat an unavailable optional race
	// check as recovered when the ordinary test fallback passes; a task that
	// explicitly requires race still needs a traceable Ubuntu CI gate.
	request := strings.ToLower(strings.TrimSpace(run.Request))
	optionalRace := request != "" && !strings.Contains(request, "race")
	return hadRaceFailure && hadFallback && (hadCIGate || optionalRace)
}

func workflowRunDeliveryRaceInfrastructure(item runArtifactItem) bool {
	text := strings.ToLower(strings.Join([]string{item.Content, item.Summary}, " "))
	for _, marker := range []string{"-race requires cgo", "c compiler", "gcc: executable file not found", "exec: \"gcc\"", "cgo: c compiler"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func workflowRunDeliveryAcceptanceFailuresRecoverable(manifest runDeliveryManifest) bool {
	if !workflowRunDeliveryHasLaterCommandVerification(manifest) {
		return false
	}
	for _, check := range manifest.Acceptance {
		if check.Status != "failed" {
			continue
		}
		stage := normalizeWorkflowRunQueryToken(check.Stage)
		text := strings.ToLower(strings.Join([]string{check.Kind, check.Name, check.Detail, check.Actual}, " "))
		if stage == "plan" || runDeliveryPolicyInterventionText(text) {
			continue
		}
		switch stage {
		case "implement", "fix", "repair", "verify", "audit", "quality", "recovery-verify", "recovery-audit", "final-quality", "final-report":
			continue
		default:
			return false
		}
	}
	return true
}

func workflowRunDeliveryHasLaterCommandVerification(manifest runDeliveryManifest) bool {
	for _, check := range manifest.Verification {
		stage := normalizeWorkflowRunQueryToken(check.Stage)
		if check.Status == "passed" && (stage == "verify" || stage == "repair" || stage == "recovery-verify") {
			return true
		}
	}
	return false
}

func workflowRunDeliveryVerificationFailuresRecoverable(manifest runDeliveryManifest) bool {
	laterCommandPassed := workflowRunDeliveryHasLaterCommandVerification(manifest)
	for _, check := range manifest.Verification {
		if check.Status != "failed" {
			continue
		}
		stage := normalizeWorkflowRunQueryToken(check.Stage)
		text := strings.ToLower(strings.Join([]string{check.Kind, check.Name, check.Detail}, " "))
		if stage == "plan" || strings.Contains(text, "race") || strings.Contains(text, "cgo") || strings.Contains(text, "compiler") || strings.Contains(text, "infrastructure") || runDeliveryPolicyInterventionText(text) {
			continue
		}
		if laterCommandPassed && (stage == "implement" || stage == "fix" || stage == "repair" || stage == "verify" || stage == "audit" || stage == "quality" || stage == "recovery-verify" || stage == "recovery-audit" || stage == "final-quality" || stage == "final-report" || strings.EqualFold(strings.TrimSpace(check.Kind), "command")) {
			continue
		}
		return false
	}
	return true
}

func workflowRunDeliveryHasRecoveredVerification(run session.WorkflowRunSnapshot, artifacts []runArtifactItem) bool {
	if normalizeWorkflowRunQueryToken(run.Status) != "completed" {
		return false
	}
	lastError := -1
	lastVerification := -1
	for index, item := range artifacts {
		if item.Denied && !runDeliveryPolicyInterventionText(item.Content+"\n"+item.Summary) {
			return false
		}
		if payload, ok := runDeliveryCommandPayload(item); ok && (runDeliveryBool(payload["timed_out"]) || runDeliveryBool(payload["cancelled"])) {
			return false
		}
		if runDeliveryArtifactBlocks(item) || runDeliveryToolResultFailed(item) {
			if normalizeWorkflowRunQueryToken(item.Kind) == "tool_result" || strings.TrimSpace(item.ToolCallID) != "" {
				lastError = index
			}
			continue
		}
		strength, _ := runArtifactEvidenceStrength(item)
		if strength == "strong" && runDeliveryArtifactIsVerification(item) {
			lastVerification = index
		}
	}
	return lastError >= 0 && lastVerification > lastError
}

func applyRunDeliveryManifestFacts(facts *runDeliveryFacts, manifest runDeliveryManifest, artifacts []runArtifactItem) {
	if facts == nil {
		return
	}
	for _, check := range manifest.Acceptance {
		if check.Status == "failed" && !runDeliveryPolicyInterventionText(strings.Join([]string{check.Kind, check.Name, check.Detail, check.Actual}, "\n")) {
			facts.acceptanceFailed++
		}
	}
	for _, check := range manifest.Verification {
		switch check.Status {
		case "passed":
		case "failed":
			// A stage command/read budget intervention is a runtime control
			// event, not a failed verification. The underlying tool artifact
			// remains in the manifest for auditability; count only real failures.
			if runDeliveryPolicyInterventionText(strings.Join([]string{check.Kind, check.Name, check.Detail}, "\n")) {
				continue
			}
			facts.verificationFailed++
		default:
			facts.verificationUnknown++
		}
	}
	if len(manifest.Changes) > 0 {
		facts.changesClaimed = true
	}
	for _, change := range manifest.Changes {
		if runDeliveryDocumentaryPath(change.Path) {
			facts.documentaryEvidence++
		}
	}
	for _, item := range artifacts {
		if item.Suspended && !item.Denied {
			continue
		}
		strength, _ := runArtifactEvidenceStrength(item)
		switch strength {
		case "strong":
			facts.strongEvidence++
			if runDeliveryMutationTool(item.ToolName) && firstWorkflowRunQueryValue(item.ArtifactRef, item.Ref, item.Hash) != "" {
				facts.deliverableEvidence++
				if runDeliveryDocumentaryPath(runDeliveryArtifactOutputPath(item)) {
					facts.documentaryEvidence++
				}
			}
			if runDeliveryArtifactIsVerification(item) || runArtifactEvidenceCategory(item) == "verification" {
				facts.verificationEvidence++
			}
		case "weak":
			facts.weakEvidence++
		}
		if runDeliveryMutationTool(item.ToolName) {
			facts.changesClaimed = true
		}
		if runDeliveryArtifactBlocks(item) {
			facts.toolErrors++
		}
		if runDeliveryToolResultFailed(item) {
			facts.toolErrors++
		}
	}
	if facts.toolErrors > 1 {
		facts.toolErrors = 1
	}
}

func workflowRunDeliveryHasBlockingDenial(artifacts []runArtifactItem) bool {
	for _, item := range artifacts {
		if item.Denied && !runDeliveryPolicyInterventionText(item.Content+"\n"+item.Summary) {
			return true
		}
	}
	return false
}

func runDeliveryArtifactBlocks(item runArtifactItem) bool {
	if runDeliveryPolicyInterventionText(item.Content + "\n" + item.Summary) {
		return false
	}
	if item.Denied {
		return true
	}
	if runDeliveryExploratoryReadMiss(item.ToolName, item.Content+"\n"+item.Summary) {
		return false
	}
	// A tool result can be persisted with IsError set while it is waiting for
	// approval. That is recoverable control flow, not a delivery failure.
	return item.IsError && !(item.Suspended && !item.Denied)
}

func runDeliveryExploratoryReadMiss(toolName, content string) bool {
	name := strings.ToLower(strings.TrimSpace(toolName))
	base := name
	if slash := strings.LastIndex(name, "/"); slash >= 0 {
		base = name[slash+1:]
	}
	switch base {
	case "read_file", "read_file_range", "list_tree":
	default:
		return false
	}
	text := strings.ToLower(content)
	return strings.Contains(text, "path not found") ||
		strings.Contains(text, "no such file or directory") ||
		strings.Contains(text, "cannot find the file specified")
}

func runDeliveryPolicyInterventionText(value string) bool {
	text := strings.ToLower(strings.TrimSpace(value))
	for _, marker := range []string{
		"workflow stage command limit reached",
		"workflow stage read limit reached",
		"workflow stage command policy blocks class",
		"encodes acceptance",
		"change the implementation instead of rewriting the test",
		"already complete; refusing to replay write",
		"is blocked: stage",
		"escapes workspace root",
		"is not ready: dependencies for slice",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func finalizeRunDeliveryManifest(manifest runDeliveryManifest, facts runDeliveryFacts) runDeliveryManifest {
	changesHaveDiff := !facts.changesClaimed || len(manifest.Changes) > 0 || facts.deliverableEvidence > 0
	verificationRequired := facts.changesClaimed
	verificationPassed := !verificationRequired || facts.verificationEvidence > 0 || facts.documentaryEvidence > 0
	recordedVerificationPassed := facts.verificationFailed == 0 && facts.verificationUnknown == 0
	if facts.documentaryEvidence > 0 && facts.verificationFailed == 0 {
		recordedVerificationPassed = true
	}
	completed := facts.status == "completed" || facts.status == "complete" || facts.status == "success" || facts.status == "passed"

	acceptancePassed := facts.acceptanceFailed == 0 || runDeliveryPlanAcceptanceRecoverable(manifest, facts)
	manifest.HardGates = []runDeliveryGate{
		deliveryGate("run_completed", true, completed, "run must reach a completed terminal state"),
		deliveryGate("no_tool_errors", true, facts.toolErrors == 0, "tool errors or denied calls prevent success"),
		deliveryGate("acceptance_passed", len(manifest.Acceptance) > 0, acceptancePassed, "recorded acceptance criteria must pass"),
		deliveryGate("verification_passed", len(manifest.Verification) > 0, recordedVerificationPassed, "recorded verification must pass with a known status or be superseded by a traceable report diff"),
		deliveryGate("changes_have_diff", facts.changesClaimed, changesHaveDiff, "claimed file changes require a persisted diff or content-addressed deliverable"),
		deliveryGate("changes_verified", verificationRequired, verificationPassed, "claimed changes require tool-backed verification or traceable report source evidence"),
		deliveryGate("strong_evidence", true, facts.strongEvidence > 0, "model text and report headings alone are not completion evidence"),
	}

	score := 100
	if !completed {
		score -= 50
	}
	if facts.toolErrors > 0 {
		score -= 40
	}
	if !acceptancePassed {
		score -= minInt(facts.acceptanceFailed*30, 60)
	}
	score -= minInt(facts.verificationFailed*30, 60)
	score -= minInt(facts.verificationUnknown*10, 30)
	if facts.strongEvidence == 0 {
		score -= 30
	}
	if !changesHaveDiff {
		score -= 25
	}
	if !verificationPassed {
		score -= 20
	}
	if score < 0 {
		score = 0
	}
	manifest.Quality = runDeliveryQualitySummary{
		Score:               score,
		StrongEvidence:      facts.strongEvidence,
		WeakEvidence:        facts.weakEvidence,
		ToolErrors:          facts.toolErrors,
		RecoveredToolErrors: facts.recoveredToolErrors,
		AcceptanceFailed:    facts.acceptanceFailed,
		VerificationFailed:  facts.verificationFailed,
		VerificationUnknown: facts.verificationUnknown,
	}
	qualityPassed := score >= 85
	manifest.HardGates = append(manifest.HardGates, deliveryGate("quality_score", true, qualityPassed, "quality score must be at least 85"))

	for _, gate := range manifest.HardGates {
		if gate.Required && !gate.Passed {
			manifest.Unverified = append(manifest.Unverified, gate.Detail)
		}
	}
	manifest.Unverified = uniqueSortedStrings(manifest.Unverified)
	manifest.RemainingRisks = runDeliveryRemainingRisks(facts, manifest)
	manifest.Conclusion = runDeliveryConclusion(facts, manifest)
	return manifest
}

func runDeliveryPlanAcceptanceRecoverable(manifest runDeliveryManifest, facts runDeliveryFacts) bool {
	if facts.acceptanceFailed == 0 || facts.status != "completed" || facts.strongEvidence == 0 || facts.verificationEvidence == 0 {
		return false
	}
	for _, check := range manifest.Acceptance {
		if check.Status == "failed" && normalizeWorkflowRunQueryToken(check.Stage) != "plan" {
			return false
		}
	}
	return true
}

func deliveryGate(name string, required, passed bool, detail string) runDeliveryGate {
	status := "not_applicable"
	if required {
		status = "failed"
		if passed {
			status = "passed"
		}
	}
	return runDeliveryGate{Name: name, Required: required, Passed: passed, Status: status, Detail: detail}
}

func runDeliveryConclusion(facts runDeliveryFacts, manifest runDeliveryManifest) string {
	if facts.needsAction {
		return "waiting_input"
	}
	if facts.active && !facts.terminal {
		return "running"
	}
	if facts.toolErrors > 0 || facts.acceptanceFailed > 0 || facts.verificationFailed > 0 || workflowRunQualityStatusFailed(facts.status) {
		return "failed"
	}
	if facts.strongEvidence == 0 {
		return "unverified"
	}
	for _, gate := range manifest.HardGates {
		if gate.Required && !gate.Passed {
			return "partial"
		}
	}
	return "success"
}

func runDeliveryRemainingRisks(facts runDeliveryFacts, manifest runDeliveryManifest) []string {
	risks := make([]string, 0)
	if facts.weakEvidence > 0 {
		risks = append(risks, "some outputs are model-authored or otherwise lack traceable execution evidence")
	}
	if facts.recoveredToolErrors > 0 {
		risks = append(risks, "an intermediate tool check failed but a later tool-backed verification passed")
	}
	if facts.verificationUnknown > 0 {
		risks = append(risks, "one or more verification results have an unknown status")
	}
	if facts.changesClaimed && len(manifest.Changes) == 0 {
		risks = append(risks, "file changes were claimed without a persisted diff")
	}
	if facts.changesClaimed && facts.verificationEvidence == 0 {
		risks = append(risks, "changes have no tool-backed verification evidence")
	}
	return uniqueSortedStrings(risks)
}

func agentRunDeliveryMetrics(run session.AgentRunSnapshot, artifacts []runArtifactItem, diffs []runDiffItem) runDeliveryMetrics {
	metrics := runDeliveryMetrics{
		DurationMs: runDeliveryDurationMs(run.StartedAt, firstWorkflowRunQueryValue(run.CompletedAt, run.UpdatedAt)),
		Artifacts:  len(artifacts),
		Changes:    len(diffs),
	}
	toolCalls := map[string]struct{}{}
	if run.Result != nil {
		for _, result := range run.Result.ToolResults {
			if id := strings.TrimSpace(result.CallID); id != "" {
				toolCalls[id] = struct{}{}
			}
		}
	}
	for _, event := range run.Events {
		metrics.PromptTokens += event.PromptTokens
		metrics.OutputTokens += event.OutputTokens
		metrics.CachedTokens += event.CachedTokens
		if strings.EqualFold(strings.TrimSpace(event.Type), "token_usage") && (event.PromptTokens > 0 || event.OutputTokens > 0 || event.CachedTokens > 0 || strings.TrimSpace(event.OutputKind) != "") {
			metrics.LLMCalls++
		}
		if strings.TrimSpace(event.ToolCallID) != "" {
			toolCalls[event.ToolCallID] = struct{}{}
		}
	}
	if metrics.LLMCalls == 0 && run.Result != nil {
		for _, audit := range run.Result.AuditTrail {
			if audit.PromptTokens > 0 || audit.OutputTokens > 0 || audit.CachedTokens > 0 {
				metrics.LLMCalls++
			}
		}
	}
	for _, item := range artifacts {
		if strings.TrimSpace(item.ToolCallID) != "" {
			toolCalls[item.ToolCallID] = struct{}{}
		}
	}
	metrics.ToolCalls = len(toolCalls)
	metrics.TotalTokens = metrics.PromptTokens + metrics.OutputTokens
	if usage := retainedAgentRunUsage(run); usage.ModelCalls > 0 {
		metrics.LLMCalls = usage.ModelCalls
		metrics.PromptTokens = int(usage.PromptTokens)
		metrics.OutputTokens = int(usage.OutputTokens)
		metrics.CachedTokens = int(usage.CachedTokens)
		metrics.TotalTokens = int(usage.TotalTokens)
	}
	return metrics
}

func workflowRunDeliveryMetrics(run session.WorkflowRunSnapshot, artifacts []runArtifactItem, diffs []runDiffItem) runDeliveryMetrics {
	metrics := runDeliveryMetrics{
		DurationMs:   runDeliveryDurationMs(run.StartedAt, firstWorkflowRunQueryValue(run.CompletedAt, run.UpdatedAt)),
		PromptTokens: run.BudgetReportedPromptTokens,
		OutputTokens: run.BudgetOutputTokens,
		CachedTokens: run.BudgetCachedTokens,
		TotalTokens:  run.BudgetTotalTokens,
		LLMCalls:     run.BudgetLLMCalls,
		Artifacts:    len(artifacts),
		Changes:      len(diffs),
	}
	toolCalls := map[string]struct{}{}
	for _, stage := range run.CompletedStages {
		for _, result := range stage.Result.ToolResults {
			if id := strings.TrimSpace(result.CallID); id != "" {
				toolCalls[id] = struct{}{}
			}
		}
	}
	for _, event := range run.Events {
		if id := strings.TrimSpace(event.ToolCallID); id != "" {
			toolCalls[id] = struct{}{}
		}
	}
	for _, operation := range run.ToolOperations {
		if id := strings.TrimSpace(operation.Result.CallID); id != "" {
			toolCalls[id] = struct{}{}
		}
	}
	for _, item := range artifacts {
		if strings.TrimSpace(item.ToolCallID) != "" {
			toolCalls[item.ToolCallID] = struct{}{}
		}
	}
	metrics.ToolCalls = len(toolCalls)
	if metrics.TotalTokens == 0 {
		metrics.TotalTokens = metrics.PromptTokens + metrics.OutputTokens
	}
	if usage := retainedWorkflowRunUsage(run); usage.ModelCalls > 0 {
		metrics.LLMCalls = usage.ModelCalls
		metrics.PromptTokens = int(usage.PromptTokens)
		metrics.OutputTokens = int(usage.OutputTokens)
		metrics.CachedTokens = int(usage.CachedTokens)
		metrics.TotalTokens = int(usage.TotalTokens)
	}
	return metrics
}

func runDeliveryDurationMs(startedAt, endedAt string) int64 {
	start, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(startedAt))
	if err != nil {
		start, err = time.Parse(time.RFC3339, strings.TrimSpace(startedAt))
	}
	if err != nil {
		return 0
	}
	end, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(endedAt))
	if err != nil {
		end, err = time.Parse(time.RFC3339, strings.TrimSpace(endedAt))
	}
	if err != nil || end.Before(start) {
		return 0
	}
	return end.Sub(start).Milliseconds()
}

func uniqueSortedStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func boolCount(value bool) int {
	if value {
		return 1
	}
	return 0
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
