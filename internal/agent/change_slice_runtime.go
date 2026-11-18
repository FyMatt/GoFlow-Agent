package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/policy"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/internal/slicescheduler"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

// validateChangeSliceToolCall is the runtime hard gate for approved workspace
// slices. The model may describe a dependency order, but it cannot bypass it
// by emitting an out-of-order read or write tool call.
func (e ExecutionContext) validateChangeSliceToolCall(call schema.ToolCall, tool schema.Tool) error {
	if !e.ChangeSliceEnforcement || !workspaceScopedExecutionTool(call, tool) {
		return nil
	}
	kind := policy.KindForTool(tool)
	if kind != config.ToolKindRead && kind != config.ToolKindWrite {
		return nil
	}
	paths := e.changeSliceToolPaths(call)
	if len(paths) == 0 {
		if kind == config.ToolKindRead {
			return nil
		}
		return fmt.Errorf("write tool %s has no workspace-relative target path for the approved change slices", tool.Name)
	}
	if kind == config.ToolKindWrite && changeSliceDeliveryArtifactPaths(paths) {
		return nil
	}
	slices := e.changeSliceState()
	if len(slices) == 0 {
		return fmt.Errorf("approved change slice plan is not available before %s tool %s", kind, tool.Name)
	}
	item, err := findChangeSliceForPaths(slices, paths, string(kind))
	if kind == config.ToolKindRead && err != nil {
		// Reading a file that is only a target of a write slice is allowed as
		// preparation, but the write slice must already be dependency-ready.
		item, err = findChangeSliceForPaths(slices, paths, slicescheduler.ModeWrite)
		if err != nil {
			return nil
		}
	}
	if err != nil && kind == config.ToolKindWrite {
		item, err = findReadyWriteChangeSlice(slices)
	}
	if err != nil {
		return err
	}
	status := strings.ToLower(strings.TrimSpace(item.Status))
	if kind == config.ToolKindRead {
		// Reads are side-effect free. A blocked or not-yet-ready slice must
		// never block the evidence needed to repair it: in real runs a blocked
		// read-only discovery slice rejected every read in the repair stage, so
		// the stage produced no changes and the whole delivery failed.
		switch status {
		case slicescheduler.StatusBlocked, slicescheduler.StatusPending, slicescheduler.StatusPlanned, "":
			return nil
		}
	}
	if kind == config.ToolKindRead && normalizeChangeSliceMode(item.ExecutionMode) == slicescheduler.ModeWrite {
		// Reading a target of a write slice is preparation, not execution. It
		// must neither start that slice nor trip its dependency order: blocking
		// the read made every dependent slice unreachable in real runs, because
		// the model could not inspect the files it had to change later.
		return nil
	}
	switch status {
	case slicescheduler.StatusImplemented, slicescheduler.StatusVerified:
		if kind == config.ToolKindRead {
			return nil
		}
		if status == slicescheduler.StatusVerified {
			return fmt.Errorf("change slice %s is already complete; refusing to replay write tool %s", item.ID, tool.Name)
		}
		// Implemented-but-not-verified writes may still fail public tests.
		// Allow follow-up patches in the same delivery instead of locking the
		// first incomplete write in place.
		return nil
	case slicescheduler.StatusRunning:
		if kind == config.ToolKindRead && normalizeChangeSliceMode(item.ExecutionMode) == slicescheduler.ModeWrite {
			return nil
		}
		return nil
	case slicescheduler.StatusPending, slicescheduler.StatusPlanned, "":
		plan, err := slicescheduler.New(slices)
		if err != nil {
			return fmt.Errorf("validate approved change slice plan: %w", err)
		}
		started, err := plan.Start(item.ID)
		if err != nil {
			// A dependency that is blocked but already carries real changes must
			// not deadlock the delivery: measured runs retried the same blocked
			// slice 29 times because its dependency never became ready.
			if relaxed, ok := relaxBlockedChangeSliceDependencies(slices); ok {
				if relaxedPlan, planErr := slicescheduler.New(relaxed); planErr == nil {
					if startedRelaxed, startErr := relaxedPlan.Start(item.ID); startErr == nil {
						if e.Session != nil && strings.TrimSpace(e.WorkflowRunID) != "" {
							if persistErr := e.Session.ReplaceWorkflowRunChangeSlicesDurable(e.WorkflowRunID, startedRelaxed.Slices); persistErr == nil {
								e.Session.AppendWorkflowRunEvent(e.WorkflowRunID, session.WorkflowRunEventSnapshot{
									Type:           "change_slice_state",
									Stage:          e.WorkflowStage,
									Content:        fmt.Sprintf("change slice %s started after treating blocked dependencies with changes as done", item.ID),
									WorkflowName:   e.WorkflowName,
									WorkflowStatus: "running",
									NextStage:      e.WorkflowStage,
									Reason:         "change_slice_dependency_relaxed",
									SourceRef:      item.ID,
								})
								return nil
							}
						}
					}
				}
			}
			// The plan's dependency order is a guess made before any code was
			// read. Give the model two chances to comply, then let the write go
			// through: measured runs retried the same out-of-order slice 29 times
			// and burned ~40% of the run's calls. The verify stage still gates
			// correctness.
			attempts := changeSliceNotReadyAttempts(e.WorkflowRunID, item.ID)
			recordChangeSliceNotReadyAttempt(e.WorkflowRunID, item.ID)
			if attempts >= 2 {
				if relaxed, relaxErr := startChangeSliceOutOfOrder(slices, item.ID); relaxErr == nil {
					if e.Session != nil && strings.TrimSpace(e.WorkflowRunID) != "" {
						if persistErr := e.Session.ReplaceWorkflowRunChangeSlicesDurable(e.WorkflowRunID, relaxed); persistErr == nil {
							e.Session.AppendWorkflowRunEvent(e.WorkflowRunID, session.WorkflowRunEventSnapshot{
								Type:           "change_slice_state",
								Stage:          e.WorkflowStage,
								Content:        fmt.Sprintf("change slice %s started out of dependency order: %v", item.ID, err),
								WorkflowName:   e.WorkflowName,
								WorkflowStatus: "running",
								NextStage:      e.WorkflowStage,
								Reason:         "change_slice_started_out_of_order",
								SourceRef:      item.ID,
							})
							return nil
						}
					}
				}
			}
			return fmt.Errorf("change slice %s is not ready: %w; available now: %s", item.ID, err, readyChangeSliceSummary(slices))
		}
		if e.Session == nil || strings.TrimSpace(e.WorkflowRunID) == "" {
			return fmt.Errorf("change slice %s cannot be made durable without a workflow run", item.ID)
		}
		if err := e.Session.ReplaceWorkflowRunChangeSlicesDurable(e.WorkflowRunID, started.Slices); err != nil {
			return fmt.Errorf("persist change slice %s start: %w", item.ID, err)
		}
		e.Session.AppendWorkflowRunEvent(e.WorkflowRunID, session.WorkflowRunEventSnapshot{
			Type:           "change_slice_state",
			Stage:          e.WorkflowStage,
			Content:        fmt.Sprintf("change slice %s started", item.ID),
			WorkflowName:   e.WorkflowName,
			WorkflowStatus: "running",
			NextStage:      e.WorkflowStage,
			Reason:         "change_slice_started",
			SourceRef:      item.ID,
		})
		return nil
	case slicescheduler.StatusBlocked:
		if kind == config.ToolKindWrite {
			// A blocked slice rejected every write in the stage that owned it,
			// so the model kept retrying until the tool-operation cap (200) and
			// the whole delivery failed. An explicit write attempt is the
			// retry: reopen the slice and let the quality gate judge the result.
			if err := e.reopenBlockedChangeSlice(item.ID); err != nil {
				return err
			}
			return nil
		}
		return fmt.Errorf("change slice %s is blocked: %s", item.ID, item.BlockedReason)
	default:
		return fmt.Errorf("change slice %s has unsupported status %q", item.ID, item.Status)
	}
}

// reopenBlockedChangeSlice turns a blocked slice back into running so the write
// that is being attempted can proceed.
func (e ExecutionContext) reopenBlockedChangeSlice(id string) error {
	if e.Session == nil || strings.TrimSpace(e.WorkflowRunID) == "" {
		return fmt.Errorf("change slice %s is blocked and cannot be reopened without a workflow run", id)
	}
	run, ok := e.Session.WorkflowRun(e.WorkflowRunID)
	if !ok || len(run.ChangeSlices) == 0 {
		return fmt.Errorf("change slice %s is blocked and its workflow state is unavailable", id)
	}
	for _, slice := range run.ChangeSlices {
		if slice.ID != id {
			continue
		}
		if strings.Contains(strings.ToLower(slice.BlockedReason), "model-call budget") {
			// Reopening here would let a write attempt reset the slice budget and
			// turn the guard into an endless block/reopen cycle.
			return fmt.Errorf("change slice %s is blocked by its model-call budget; stop writing this slice and let the repair stage handle it", id)
		}
		break
	}
	slices := append([]session.WorkflowChangeSliceSnapshot(nil), run.ChangeSlices...)
	found := false
	for i := range slices {
		if slices[i].ID != id {
			continue
		}
		slices[i].Status = slicescheduler.StatusRunning
		slices[i].BlockedReason = ""
		found = true
	}
	if !found {
		return fmt.Errorf("change slice %s is blocked and missing from the run state", id)
	}
	if err := e.Session.ReplaceWorkflowRunChangeSlicesDurable(e.WorkflowRunID, slices); err != nil {
		return fmt.Errorf("reopen blocked change slice %s: %w", id, err)
	}
	e.Session.AppendWorkflowRunEvent(e.WorkflowRunID, session.WorkflowRunEventSnapshot{
		Type:           "change_slice_state",
		Stage:          e.WorkflowStage,
		Content:        fmt.Sprintf("change slice %s reopened by a write attempt", id),
		WorkflowName:   e.WorkflowName,
		WorkflowStatus: "running",
		NextStage:      e.WorkflowStage,
		Reason:         "change_slice_reopened_for_write",
		SourceRef:      id,
	})
	return nil
}

var changeSliceNotReadyMu sync.Mutex
var changeSliceNotReadyCounts = map[string]int{}

func changeSliceNotReadyAttempts(runID, sliceID string) int {
	changeSliceNotReadyMu.Lock()
	defer changeSliceNotReadyMu.Unlock()
	return changeSliceNotReadyCounts[strings.TrimSpace(runID)+"/"+strings.TrimSpace(sliceID)]
}

func recordChangeSliceNotReadyAttempt(runID, sliceID string) {
	changeSliceNotReadyMu.Lock()
	defer changeSliceNotReadyMu.Unlock()
	changeSliceNotReadyCounts[strings.TrimSpace(runID)+"/"+strings.TrimSpace(sliceID)]++
}

// startChangeSliceOutOfOrder marks the target slice running even though its
// declared dependencies are incomplete. The write itself is the evidence that
// the model needs this slice now; the verify stage checks correctness.
func startChangeSliceOutOfOrder(slices []session.WorkflowChangeSliceSnapshot, id string) ([]session.WorkflowChangeSliceSnapshot, error) {
	out := append([]session.WorkflowChangeSliceSnapshot(nil), slices...)
	for i := range out {
		if out[i].ID != id {
			continue
		}
		out[i].Status = slicescheduler.StatusRunning
		out[i].BlockedReason = ""
		return out, nil
	}
	return nil, fmt.Errorf("change slice %s is missing from the run state", id)
}

// relaxBlockedChangeSliceDependencies marks blocked slices that already changed
// files as implemented, so a dependent slice can start instead of deadlocking
// on a dependency that can no longer progress.
func relaxBlockedChangeSliceDependencies(slices []session.WorkflowChangeSliceSnapshot) ([]session.WorkflowChangeSliceSnapshot, bool) {
	out := append([]session.WorkflowChangeSliceSnapshot(nil), slices...)
	changed := false
	for i := range out {
		if strings.EqualFold(strings.TrimSpace(out[i].Status), slicescheduler.StatusBlocked) && len(out[i].ChangedFiles) > 0 {
			out[i].Status = slicescheduler.StatusImplemented
			out[i].BlockedReason = ""
			changed = true
		}
	}
	return out, changed
}

// readyChangeSliceSummary lists the slices a write may target right now, with
// their files, so a rejected write can pivot instead of retrying.
func readyChangeSliceSummary(slices []session.WorkflowChangeSliceSnapshot) string {
	done := make(map[string]bool, len(slices))
	for _, slice := range slices {
		switch strings.ToLower(strings.TrimSpace(slice.Status)) {
		case slicescheduler.StatusImplemented, slicescheduler.StatusVerified:
			done[slice.ID] = true
		}
	}
	parts := make([]string, 0, len(slices))
	for _, slice := range slices {
		status := strings.ToLower(strings.TrimSpace(slice.Status))
		if status == slicescheduler.StatusBlocked || status == slicescheduler.StatusImplemented || status == slicescheduler.StatusVerified {
			continue
		}
		ready := true
		for _, dep := range slice.DependsOn {
			if !done[strings.TrimSpace(dep)] {
				ready = false
				break
			}
		}
		if !ready {
			continue
		}
		targets := ""
		if len(slice.TargetFiles) > 0 {
			targets = " (" + strings.Join(slice.TargetFiles, ", ") + ")"
		}
		parts = append(parts, slice.ID+targets)
	}
	if len(parts) == 0 {
		return "no slice is currently writable; report the blocker instead of retrying"
	}
	return strings.Join(parts, "; ")
}

func (e ExecutionContext) changeSliceState() []session.WorkflowChangeSliceSnapshot {
	if e.Session != nil && strings.TrimSpace(e.WorkflowRunID) != "" {
		if run, ok := e.Session.WorkflowRun(e.WorkflowRunID); ok && len(run.ChangeSlices) > 0 {
			return run.ChangeSlices
		}
	}
	return append([]session.WorkflowChangeSliceSnapshot(nil), e.ChangeSlices...)
}

func (e ExecutionContext) changeSliceToolPaths(call schema.ToolCall) []string {
	var args map[string]json.RawMessage
	if len(call.Arguments) == 0 || json.Unmarshal(call.Arguments, &args) != nil {
		return nil
	}
	paths := make([]string, 0, 2)
	for _, key := range []string{"path", "source", "destination", "relative_path"} {
		var value string
		if json.Unmarshal(args[key], &value) != nil {
			continue
		}
		value = e.normalizeChangeSliceToolPath(value)
		if value == "" {
			continue
		}
		seen := false
		for _, existing := range paths {
			if existing == value {
				seen = true
				break
			}
		}
		if !seen {
			paths = append(paths, value)
		}
	}
	return paths
}

func (e ExecutionContext) normalizeChangeSliceToolPath(path string) string {
	path = e.relativizeWorkspacePath(path)
	if path == "" || path == "." || path == ".." || filepath.IsAbs(path) || strings.HasPrefix(path, "../") {
		return ""
	}
	return path
}

func (e ExecutionContext) relativizeWorkspacePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	cleaned := filepath.Clean(path)
	if !filepath.IsAbs(cleaned) {
		return filepath.ToSlash(cleaned)
	}
	for _, root := range []string{e.ToolWorkspaceRoot, e.WorkspaceRoot} {
		root = filepath.Clean(strings.TrimSpace(root))
		if root == "" {
			continue
		}
		rel, err := filepath.Rel(root, cleaned)
		if err != nil {
			continue
		}
		rel = filepath.ToSlash(rel)
		if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
			continue
		}
		return rel
	}
	return ""
}

func (e ExecutionContext) relativizeWorkspaceToolPaths(call schema.ToolCall) schema.ToolCall {
	if len(call.Arguments) == 0 {
		return call
	}
	var args map[string]json.RawMessage
	if json.Unmarshal(call.Arguments, &args) != nil {
		return call
	}
	changed := false
	for _, key := range []string{"path", "source", "destination"} {
		var value string
		if json.Unmarshal(args[key], &value) != nil {
			continue
		}
		relative := e.relativizeWorkspacePath(value)
		if relative == "" || relative == value {
			continue
		}
		encoded, err := json.Marshal(relative)
		if err != nil {
			continue
		}
		args[key] = encoded
		changed = true
	}
	if !changed {
		return call
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		return call
	}
	call.Arguments = encoded
	return call
}

func (e ExecutionContext) prepareToolCall(call schema.ToolCall) schema.ToolCall {
	return e.relativizeWorkspaceToolPaths(normalizeOptionalToolArguments(call))
}

func (e ExecutionContext) rejectExistingAcceptanceTestRewrite(call schema.ToolCall, tool schema.Tool) error {
	if policy.KindForTool(tool) != config.ToolKindWrite {
		return nil
	}
	root := strings.TrimSpace(e.ToolWorkspaceRoot)
	if root == "" {
		root = strings.TrimSpace(e.WorkspaceRoot)
	}
	if root == "" {
		return nil
	}
	for _, rel := range e.changeSliceToolPaths(call) {
		if !isExistingAcceptanceTestPath(rel) {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			continue
		}
		return fmt.Errorf("existing test %s encodes acceptance; preserve its assertions and change the implementation, or add requested regression coverage in a new test file instead of rewriting this file", rel)
	}
	return nil
}

func isExistingAcceptanceTestPath(rel string) bool {
	base := strings.ToLower(filepath.Base(rel))
	switch {
	case strings.HasSuffix(base, "_test.go"):
		return true
	case strings.HasSuffix(base, ".test.js"), strings.HasSuffix(base, ".spec.js"), strings.HasSuffix(base, ".test.mjs"), strings.HasSuffix(base, ".spec.mjs"):
		return true
	default:
		return false
	}
}

func changeSliceDeliveryArtifactPaths(paths []string) bool {
	if len(paths) == 0 {
		return false
	}
	for _, path := range paths {
		rel := filepath.ToSlash(strings.TrimSpace(path))
		rel = strings.TrimPrefix(rel, "./")
		switch {
		case rel == "deliverables", strings.HasPrefix(rel, "deliverables/"):
		case rel == "docs", strings.HasPrefix(rel, "docs/"):
		default:
			return false
		}
	}
	return true
}

func findChangeSliceForPaths(slices []session.WorkflowChangeSliceSnapshot, paths []string, mode string) (session.WorkflowChangeSliceSnapshot, error) {
	candidates := make(map[string]session.WorkflowChangeSliceSnapshot)
	mode = normalizeChangeSliceMode(mode)
	for _, path := range paths {
		matches := make(map[string]session.WorkflowChangeSliceSnapshot)
		for index := range slices {
			if normalizeChangeSliceMode(slices[index].ExecutionMode) != mode {
				continue
			}
			for _, target := range slices[index].TargetFiles {
				if session.ChangeSliceTargetCovers(target, path) {
					matches[slices[index].ID] = slices[index]
					break
				}
			}
		}
		if len(matches) == 0 {
			return session.WorkflowChangeSliceSnapshot{}, fmt.Errorf("%s target %s is outside the approved change slices", mode, path)
		}
		if len(candidates) == 0 {
			candidates = matches
			continue
		}
		for id := range candidates {
			if _, ok := matches[id]; !ok {
				delete(candidates, id)
			}
		}
	}
	if len(candidates) == 0 {
		return session.WorkflowChangeSliceSnapshot{}, fmt.Errorf("%s tool targets do not belong to one approved change slice", mode)
	}
	for _, item := range candidates {
		if strings.EqualFold(strings.TrimSpace(item.Status), slicescheduler.StatusRunning) {
			return item, nil
		}
	}
	plan, err := slicescheduler.New(slices)
	if err == nil {
		if batches, batchErr := plan.ReadyBatches(); batchErr == nil {
			for _, batch := range batches {
				if batch.Mode != mode {
					continue
				}
				for _, id := range batch.IDs {
					if item, ok := candidates[id]; ok {
						return item, nil
					}
				}
			}
		}
	}
	for _, item := range candidates {
		return item, nil
	}
	return session.WorkflowChangeSliceSnapshot{}, fmt.Errorf("approved change slice could not be selected")
}

func findReadyWriteChangeSlice(slices []session.WorkflowChangeSliceSnapshot) (session.WorkflowChangeSliceSnapshot, error) {
	for _, item := range slices {
		if normalizeChangeSliceMode(item.ExecutionMode) != slicescheduler.ModeWrite {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(item.Status), slicescheduler.StatusRunning) {
			return item, nil
		}
	}
	plan, err := slicescheduler.New(slices)
	if err != nil {
		return session.WorkflowChangeSliceSnapshot{}, err
	}
	batches, err := plan.ReadyBatches()
	if err != nil {
		return session.WorkflowChangeSliceSnapshot{}, err
	}
	byID := make(map[string]session.WorkflowChangeSliceSnapshot, len(slices))
	for _, item := range slices {
		byID[item.ID] = item
	}
	for _, batch := range batches {
		if batch.Mode != slicescheduler.ModeWrite {
			continue
		}
		for _, id := range batch.IDs {
			if item, ok := byID[id]; ok {
				return item, nil
			}
		}
	}
	return session.WorkflowChangeSliceSnapshot{}, fmt.Errorf("write target is outside the approved change slices")
}

func (e ExecutionContext) recordChangeSliceToolSuccess(call schema.ToolCall, tool schema.Tool, result schema.ToolResult) {
	if !e.ChangeSliceEnforcement || e.Session == nil || strings.TrimSpace(e.WorkflowRunID) == "" || result.IsError || result.Denied || result.Suspended || !workspaceScopedExecutionTool(call, tool) {
		return
	}
	kind := policy.KindForTool(tool)
	if kind != config.ToolKindRead && kind != config.ToolKindWrite {
		return
	}
	paths := e.changeSliceToolPaths(call)
	if len(paths) == 0 {
		return
	}
	item, err := findChangeSliceForPaths(e.changeSliceState(), paths, string(kind))
	if err != nil && kind == config.ToolKindWrite {
		item, err = findReadyWriteChangeSlice(e.changeSliceState())
	}
	if err != nil || item.ID == "" {
		return
	}
	if kind == config.ToolKindRead && normalizeChangeSliceMode(item.ExecutionMode) == slicescheduler.ModeWrite {
		return
	}
	_ = e.Session.MarkWorkflowRunChangeSliceFilesDurable(e.WorkflowRunID, e.WorkflowStage, item.ID, paths)
	e.Session.AppendWorkflowRunEvent(e.WorkflowRunID, session.WorkflowRunEventSnapshot{
		Type:           "change_slice_state",
		Stage:          e.WorkflowStage,
		Content:        fmt.Sprintf("change slice %s observed successful %s for %s", item.ID, kind, strings.Join(paths, ", ")),
		WorkflowName:   e.WorkflowName,
		WorkflowStatus: "running",
		NextStage:      e.WorkflowStage,
		Reason:         "change_slice_observed",
		SourceRef:      item.ID,
	})
}

func (e ExecutionContext) recordChangeSliceToolFailure(call schema.ToolCall, tool schema.Tool, result schema.ToolResult) {
	if !e.ChangeSliceEnforcement || e.Session == nil || strings.TrimSpace(e.WorkflowRunID) == "" || !result.IsError || result.Denied || result.Suspended || !workspaceScopedExecutionTool(call, tool) {
		return
	}
	kind := policy.KindForTool(tool)
	if kind != config.ToolKindRead && kind != config.ToolKindWrite {
		return
	}
	paths := e.changeSliceToolPaths(call)
	if len(paths) == 0 {
		return
	}
	item, err := findChangeSliceForPaths(e.changeSliceState(), paths, string(kind))
	if err != nil || item.ID == "" {
		return
	}
	blockReason := ""
	if kind == config.ToolKindWrite {
		failure := strings.TrimSpace(result.Content)
		if len(failure) > 2000 {
			failure = failure[:2000]
		}
		if failure == "" {
			failure = fmt.Sprintf("%s tool %s failed", kind, tool.Name)
		}
		// A failed write is a failed attempt, not a blocked delivery: usage
		// errors such as a missing expected_sha256 must stay retryable. The
		// retry budget is bounded per slice and stage, because unbounded retries
		// turned into a 733-call loop that never wrote anything.
		if !changeSliceWriteFailureBlocks(result) {
			const maxRetryableWriteFailures = 3
			retries := 0
			if run, ok := e.Session.WorkflowRun(e.WorkflowRunID); ok {
				for _, event := range run.Events {
					if event.Reason == "change_slice_retryable_write_failure" && event.SourceRef == item.ID && event.Stage == e.WorkflowStage {
						retries++
					}
				}
			}
			if retries < maxRetryableWriteFailures {
				e.Session.AppendWorkflowRunEvent(e.WorkflowRunID, session.WorkflowRunEventSnapshot{
					Type:           "change_slice_state",
					Stage:          e.WorkflowStage,
					Content:        fmt.Sprintf("change slice %s retained after failed write (%d/%d): %s", item.ID, retries+1, maxRetryableWriteFailures, failure),
					WorkflowName:   e.WorkflowName,
					WorkflowStatus: "running",
					NextStage:      e.WorkflowStage,
					Reason:         "change_slice_retryable_write_failure",
					SourceRef:      item.ID,
				})
				return
			}
			blockReason = fmt.Sprintf("write kept failing %d times in this stage; last error: %s", retries, failure)
		}
	}
	reason := blockReason
	if reason == "" {
		reason = strings.TrimSpace(result.Content)
	}
	if reason == "" {
		reason = fmt.Sprintf("%s tool %s failed", kind, tool.Name)
	}
	if err := e.Session.BlockWorkflowRunChangeSliceDurable(e.WorkflowRunID, e.WorkflowStage, item.ID, reason); err != nil {
		return
	}
	e.Session.AppendWorkflowRunEvent(e.WorkflowRunID, session.WorkflowRunEventSnapshot{
		Type:           "change_slice_state",
		Stage:          e.WorkflowStage,
		Content:        fmt.Sprintf("change slice %s blocked after failed %s", item.ID, kind),
		WorkflowName:   e.WorkflowName,
		WorkflowStatus: "blocked",
		NextStage:      e.WorkflowStage,
		Reason:         "change_slice_failed",
		SourceRef:      item.ID,
	})
}

// maxModelCallsPerChangeSlice bounds how many model calls one change slice may
// consume. A real run spent 315 of 320 calls on a single stuck slice, so the
// per-slice budget is the control that keeps one slice from absorbing the run.
const maxModelCallsPerChangeSlice = 45

// enforceChangeSliceCallBudget blocks a running slice that exceeds its model
// call budget, leaving the reason (and the calls it consumed) in durable state
// so repair can target the slice instead of the whole stage.
// enforceChangeSliceCallBudget blocks a slice that exceeded its model-call
// budget and reports whether the stage should stop: continuing after every
// write slice is blocked only burns calls, which is how one run reached 141.
func (e ExecutionContext) enforceChangeSliceCallBudget() bool {
	if !e.ChangeSliceEnforcement || e.Session == nil || strings.TrimSpace(e.WorkflowRunID) == "" {
		return false
	}
	run, ok := e.Session.WorkflowRun(e.WorkflowRunID)
	if !ok {
		return false
	}
	records := session.WorkflowUsageRecords(run)
	// Count calls made in this stage only. The slice scheduler rewrites slice
	// snapshots from its own copy every turn, so a stored baseline is erased;
	// the usage ledger is append-only and therefore a stable clock.
	usedInStage := 0
	for _, record := range records {
		if strings.EqualFold(strings.TrimSpace(record.Stage), e.WorkflowStage) {
			usedInStage++
		}
	}
	if usedInStage == 0 {
		return false
	}
	doneInStage := 0
	for _, slice := range run.ChangeSlices {
		switch strings.ToLower(strings.TrimSpace(slice.Status)) {
		case "implemented", "verified":
			doneInStage++
		}
	}
	allowance := maxModelCallsPerChangeSlice * (doneInStage + 1)
	if usedInStage <= allowance {
		return false
	}
	for _, slice := range run.ChangeSlices {
		if !strings.EqualFold(strings.TrimSpace(slice.Status), "running") {
			continue
		}
		reason := fmt.Sprintf("stage %s exceeded its model-call budget: %d calls with only %d slice(s) finished (allowance %d)", e.WorkflowStage, usedInStage, doneInStage, allowance)
		if err := e.Session.BlockWorkflowRunChangeSliceDurable(e.WorkflowRunID, e.WorkflowStage, slice.ID, reason); err != nil {
			continue
		}
		e.Session.AppendWorkflowRunEvent(e.WorkflowRunID, session.WorkflowRunEventSnapshot{
			Type:           "change_slice_state",
			Stage:          e.WorkflowStage,
			Content:        fmt.Sprintf("change slice %s blocked after exceeding model-call budget: %s", slice.ID, reason),
			WorkflowName:   e.WorkflowName,
			WorkflowStatus: "blocked",
			NextStage:      e.WorkflowStage,
			Reason:         "change_slice_budget_exceeded",
			SourceRef:      slice.ID,
		})
	}
	// Stop the stage once no write slice can make progress. Re-read the run:
	// the snapshot above predates the blocks applied in this call.
	if refreshed, ok := e.Session.WorkflowRun(e.WorkflowRunID); ok {
		run = refreshed
	}
	for _, slice := range run.ChangeSlices {
		status := strings.ToLower(strings.TrimSpace(slice.Status))
		if status == "running" || status == "planned" || status == "pending" {
			return false
		}
	}
	return true
}

// changeSliceWriteFailureBlocks reports failures that must stop the slice: a
// denied or out-of-boundary write will not succeed on retry. Every other write
// failure is a usage or conflict error the model can correct.
func changeSliceWriteFailureBlocks(result schema.ToolResult) bool {
	if result.Denied {
		return true
	}
	message := strings.ToLower(strings.TrimSpace(result.Content))
	for _, marker := range []string{
		"permission denied",
		"access denied",
		"not allowed",
		"outside the workspace",
		"symbolic link",
		"read-only",
		"forbidden",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func retryableChangeSliceWriteFailure(tool schema.Tool, result schema.ToolResult) bool {
	name := strings.ToLower(strings.TrimSpace(tool.Name))
	if slash := strings.LastIndex(name, "/"); slash >= 0 {
		name = name[slash+1:]
	}
	switch name {
	case "apply_patch", "replace_text", "write_file", "move_path":
	default:
		return false
	}
	message := strings.ToLower(strings.TrimSpace(result.Content))
	if strings.Contains(message, "file changed since it was read") && strings.Contains(message, "expected_sha256 does not match") {
		return true
	}
	return (name == "apply_patch" || name == "replace_text") &&
		strings.Contains(message, "no changes were written") &&
		strings.Contains(message, "expected") &&
		strings.Contains(message, "found")
}
