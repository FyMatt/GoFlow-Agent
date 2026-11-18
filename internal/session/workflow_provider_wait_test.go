package session

import (
	"path/filepath"
	"testing"
)

func TestRestartPreservesProviderWaitWithoutPartialOutput(t *testing.T) {
	for _, pendingTool := range []bool{false, true} {
		state := New(8)
		path := filepath.Join(t.TempDir(), "session.json")
		state.SetPersistPath(path)
		id := state.StartWorkflowRun("custom", "finish the task")
		state.CompleteWorkflowRun(id, "running", "implementation completed", "verify", "", nil,
			[]WorkflowRunStageSnapshot{{Stage: "implement", Status: "completed"}})
		state.AppendWorkflowRunEvent(id, WorkflowRunEventSnapshot{Type: "status", Stage: "verify", Reason: "provider_response_connected"})
		if pendingTool {
			_, _, err := state.ReserveWorkflowToolOperation(WorkflowToolOperationSnapshot{
				Key: "pending", RunID: id, Stage: "verify", ToolName: "run_command", ArgumentsHash: "args",
			})
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := state.PersistConfigured(); err != nil {
			t.Fatal(err)
		}
		restored := New(8)
		restored.SetPersistPath(path)
		if err := restored.Load(path); err != nil {
			t.Fatal(err)
		}
		if _, err := restored.ReconcileInterruptedWorkflowRuns(); err != nil {
			t.Fatal(err)
		}
		run, _ := restored.WorkflowRun(id)
		if pendingTool {
			if run.Status != "interrupted" || len(run.CompletedStages) != 1 {
				t.Fatal("uncertain tool operation was converted into a replayable provider wait")
			}
		} else if run.Status != "paused_need_more_budget" || run.NextStage != "verify" || len(run.CompletedStages) != 2 ||
			run.CompletedStages[1].Result.Output != "" || run.CompletedAt != "" {
			t.Fatal("provider wait was lost or invented model output")
		}
	}
}
