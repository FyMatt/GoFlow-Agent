package session

import "testing"

func TestResumedWorkflowClearsOldCompletionTime(t *testing.T) {
	state := New(8)
	id := state.StartWorkflowRun("custom", "complete the task")
	state.UpdateWorkflowRunState(WorkflowSnapshot{RunID: id, Name: "custom", Status: "blocked"})
	before, _ := state.WorkflowRun(id)
	if before.CompletedAt == "" {
		t.Fatal("terminal run has no completion timestamp")
	}
	state.UpdateWorkflowRunState(WorkflowSnapshot{RunID: id, Name: "custom", Status: "running", NextStage: "repair"})
	active, _ := state.WorkflowRun(id)
	if active.CompletedAt != "" || active.StartedAt != before.StartedAt {
		t.Fatal("continued run retains a terminal timestamp or restarts its original clock")
	}
	state.UpdateWorkflowRunState(WorkflowSnapshot{RunID: id, Name: "custom", Status: "completed"})
	after, _ := state.WorkflowRun(id)
	if after.CompletedAt == "" || after.StartedAt != before.StartedAt {
		t.Fatal("continued completion is missing its new end time")
	}
}
