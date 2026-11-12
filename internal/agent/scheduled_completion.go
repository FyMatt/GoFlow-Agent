package agent

import (
	"fmt"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

// A model's final message is not completion of every approved write slice.
// Checkpoint only slices with actual writes recorded by the execution guard.
func (w *WorkflowRunner) holdIncompleteChangeSlices(graph workflowGraph, stage workflowGraphStage, result *schema.AgentResult) error {
	if !workflowGraphStageUsesChangeSliceScheduler(graph, stage) || result.Incomplete {
		return nil
	}
	if w == nil || w.runtime == nil || w.runtime.session == nil {
		return fmt.Errorf("approved change slice state is unavailable")
	}
	run, ok := w.runtime.session.WorkflowRun(w.runID)
	if !ok || len(run.ChangeSlices) == 0 {
		return fmt.Errorf("approved change slice state is unavailable")
	}
	changed := false
	for i := range run.ChangeSlices {
		item := &run.ChangeSlices[i]
		if item.ExecutionMode != "write" {
			continue
		}
		status := strings.ToLower(strings.TrimSpace(item.Status))
		if status == "running" && len(item.ChangedFiles) > 0 {
			item.Status = "implemented"
			changed = true
		}
	}
	changed = completeObservedFormattingSlices(run.ChangeSlices, result.ToolResults, stage.Name) || changed
	var pending []string
	for _, item := range run.ChangeSlices {
		if item.ExecutionMode == "write" && item.Status != "implemented" && item.Status != "verified" {
			pending = append(pending, item.ID)
		}
	}
	if changed {
		if err := w.runtime.session.ReplaceWorkflowRunChangeSlicesDurable(w.runID, run.ChangeSlices); err != nil {
			return err
		}
	}
	if len(pending) > 0 {
		result.Incomplete = true
		result.StopReason = schema.StopReasonStop
		result.IncompleteReason = "仍有已批准的变更分片未实施：" + strings.Join(pending, ", ") + "。保留现有改动，继续当前实施阶段，完成剩余分片后再进入验证。"
	}
	return nil
}
