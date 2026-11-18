package session

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestDurableSnapshotWaitsForWorkflowPersistence(t *testing.T) {
	state := New(8)
	state.workflowPersistMu.Lock()
	returned := make(chan Snapshot, 1)
	go func() {
		returned <- state.DurableSnapshot()
	}()

	select {
	case <-returned:
		state.workflowPersistMu.Unlock()
		t.Fatal("durable snapshot returned before workflow persistence completed")
	case <-time.After(50 * time.Millisecond):
	}

	state.workflowPersistMu.Unlock()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("durable snapshot did not return after workflow persistence completed")
	}
}

func TestReconcileInterruptedWorkflowRunsPersistsRetryableState(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".goflow", "session.json")
	state := New(8)
	state.SetPersistPath(path)

	runningID := state.StartWorkflowRun("delivery", "finish the task")
	state.AppendWorkflowRunEvent(runningID, WorkflowRunEventSnapshot{
		Type:           string(schema.StreamEventTaskStage),
		Stage:          "implement",
		Content:        "implementation started",
		WorkflowStatus: "running",
		NextStage:      "implement",
	})
	waitingID := state.StartWorkflowRun("intake", "collect target")
	state.CompleteWorkflowRun(waitingID, "awaiting_input", "target required", "collect", "provide target", []schema.WorkflowInputField{{Name: "target", Required: true}}, nil)
	state.AppendWorkflowRunEvent(waitingID, WorkflowRunEventSnapshot{
		Type:           "workflow_input_required",
		Stage:          "collect",
		Content:        "target required",
		WorkflowStatus: "awaiting_input",
		NextStage:      "collect",
		NeedsAction:    true,
		Suspended:      true,
	})
	pausedID := state.StartWorkflowRun("long-delivery", "continue after restart")
	state.CompleteWorkflowRun(pausedID, "paused_need_more_budget", "implementation paused", "implement", "continue implementation", nil, []WorkflowRunStageSnapshot{{
		Stage:    "implement",
		Status:   "incomplete",
		Result:   schema.AgentResult{Incomplete: true, IncompleteReason: "tool iteration budget reached", ToolResults: []schema.ToolResult{{CallID: "read-1", ToolName: "read_file", Content: "preserved"}}},
		Metadata: map[string]string{"incomplete": "true", "incomplete_reason": "tool iteration budget reached"},
	}})
	state.SetWorkflow(WorkflowSnapshot{RunID: pausedID, Name: "long-delivery", Status: "running", NextStage: "implement"})
	state.UpdateWorkflowRunState(state.Snapshot().Workflow)
	if err := state.PersistConfigured(); err != nil {
		t.Fatalf("persist simulated process-owned pause: %v", err)
	}

	restarted := New(8)
	restarted.SetPersistPath(path)
	if err := restarted.Load(path); err != nil {
		t.Fatalf("load persisted workflow runs: %v", err)
	}
	count, err := restarted.ReconcileInterruptedWorkflowRuns()
	if err != nil {
		t.Fatalf("reconcile interrupted workflow runs: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected active work to interrupt and incomplete work to restore, got %d", count)
	}
	running, ok := restarted.WorkflowRun(runningID)
	if !ok || running.Status != "interrupted" || running.NextStage != "implement" {
		t.Fatalf("expected retryable interrupted run at implement, got %#v", running)
	}
	last := running.Events[len(running.Events)-1]
	if last.Type != "workflow_interrupted" || last.Reason != "process_restart" || !last.NeedsAction || !last.Suspended {
		t.Fatalf("expected explicit restart interruption event, got %#v", last)
	}
	waiting, ok := restarted.WorkflowRun(waitingID)
	if !ok || waiting.Status != "awaiting_input" || len(waiting.PendingFields) != 1 {
		t.Fatalf("expected durable input gate to remain resumable, got %#v", waiting)
	}
	paused, ok := restarted.WorkflowRun(pausedID)
	if !ok || paused.Status != "paused_need_more_budget" || paused.NextStage != "implement" || paused.CompletedAt != "" {
		t.Fatalf("expected incomplete stage to remain resumable across restart, got %#v", paused)
	}
	if last := paused.Events[len(paused.Events)-1]; last.Type != "workflow_pause_restored" || last.Reason != "process_restart_recovery" {
		t.Fatalf("expected explicit pause restoration event, got %#v", last)
	}

	reloaded := New(8)
	if err := reloaded.Load(path); err != nil {
		t.Fatalf("reload reconciled session: %v", err)
	}
	persisted, ok := reloaded.WorkflowRun(runningID)
	if !ok || persisted.Status != "interrupted" || persisted.Events[len(persisted.Events)-1].Type != "workflow_interrupted" {
		t.Fatalf("expected reconciled state on disk, got %#v", persisted)
	}
}

func TestReconcileRestartInterruptedIncompleteWorkflowBackToPause(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".goflow", "session.json")
	state := New(8)
	state.SetPersistPath(path)
	runID := state.StartWorkflowRun("long-delivery", "continue saved work")
	state.CompleteWorkflowRun(runID, "interrupted", "workflow interrupted by process restart; retry from the saved request", "implement", "", nil, []WorkflowRunStageSnapshot{{
		Stage:    "implement",
		Status:   "incomplete",
		Result:   schema.AgentResult{Incomplete: true, IncompleteReason: "provider response was interrupted", ToolResults: []schema.ToolResult{{CallID: "read-1", ToolName: "read_file", Content: "preserved"}}},
		Metadata: map[string]string{"incomplete": "true", "incomplete_reason": "provider response was interrupted"},
	}})
	state.AppendWorkflowRunEvent(runID, WorkflowRunEventSnapshot{Type: "workflow_interrupted", Stage: "implement", Reason: "process_restart", WorkflowStatus: "interrupted", NextStage: "implement"})

	restarted := New(8)
	restarted.SetPersistPath(path)
	if err := restarted.Load(path); err != nil {
		t.Fatalf("load interrupted workflow: %v", err)
	}
	count, err := restarted.ReconcileInterruptedWorkflowRuns()
	if err != nil {
		t.Fatalf("reconcile interrupted workflow: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected one restart-interrupted pause to recover, got %d", count)
	}
	recovered, ok := restarted.WorkflowRun(runID)
	if !ok || recovered.Status != "paused_need_more_budget" || recovered.NextStage != "implement" || recovered.CompletedAt != "" {
		t.Fatalf("expected restart-interrupted incomplete stage to become resumable, got %#v", recovered)
	}
}
