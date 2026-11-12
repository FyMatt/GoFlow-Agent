package session

import (
	"testing"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestIncompleteCheckpointDoesNotBlockRemainingSlices(t *testing.T) {
	planned := []WorkflowChangeSliceSnapshot{{ID: "first", ExecutionMode: "write", TargetFiles: []string{"a.go"}, ContractHash: "a", Status: "planned"}, {ID: "second", ExecutionMode: "write", TargetFiles: []string{"b.go"}, ContractHash: "b", Status: "planned"}}
	current := copyWorkflowChangeSlices(planned)
	current[0].Status = "implemented"
	current[0].ChangedFiles = []string{"a.go"}
	stages := []WorkflowRunStageSnapshot{{Stage: "plan", Status: "completed", ChangeSlices: planned}, {Stage: "implement", Status: "incomplete", Result: schema.AgentResult{Incomplete: true, Verification: []schema.Verification{{Kind: "tests", Status: "failed", Detail: "repair still in progress"}}}}}
	merged := mergeWorkflowRunChangeSlices(current, stages)
	if merged[0].Status != "implemented" || merged[1].Status != "planned" {
		t.Fatalf("an incomplete checkpoint prevented continuing the repair: %#v", merged)
	}
	stages[1].Status = "failed"
	if failed := mergeWorkflowRunChangeSlices(current, stages); failed[0].Status != "blocked" {
		t.Fatal("an explicit terminal failure must still block")
	}
}

func TestWorkflowChangeSlicesAdvanceOnlyWithPassingStageEvidence(t *testing.T) {
	planned := []WorkflowChangeSliceSnapshot{{
		ID: "storage", Title: "Storage", TargetFiles: []string{"store.go"},
		VerificationCommands: []string{"go test ./..."}, ContractHash: "contract", Status: "planned",
	}}
	stages := []WorkflowRunStageSnapshot{{Stage: "plan", Status: "completed", ChangeSlices: planned}}
	merged := mergeWorkflowRunChangeSlices(nil, stages)
	if len(merged) != 1 || merged[0].Status != "planned" {
		t.Fatalf("unexpected planned state: %#v", merged)
	}

	stages = append(stages, WorkflowRunStageSnapshot{
		Stage: "implement", Status: "completed",
		Result: schema.AgentResult{Changes: []schema.Change{{Files: []string{"store.go"}}}},
	})
	merged = mergeWorkflowRunChangeSlices(merged, stages)
	if merged[0].Status != "implemented" {
		t.Fatalf("expected implemented status, got %#v", merged)
	}

	stages = append(stages, WorkflowRunStageSnapshot{
		Stage: "verify", Status: "completed",
		Result: schema.AgentResult{Verification: []schema.Verification{{Kind: "test", Status: "failed", Detail: "regression failed"}}},
	})
	merged = mergeWorkflowRunChangeSlices(merged, stages)
	if merged[0].Status != "blocked" {
		t.Fatalf("failed verification must block the slice, got %#v", merged)
	}
}

func TestWorkflowChangeSlicesRecoverRunningAsPending(t *testing.T) {
	recovered := recoverWorkflowChangeSlices([]WorkflowChangeSliceSnapshot{
		{ID: "running", Status: "running", BlockedReason: "uncertain"},
		{ID: "done", Status: "implemented"},
	})
	if recovered[0].Status != "pending" || recovered[0].BlockedReason != "" {
		t.Fatalf("running slice was not made retryable: %#v", recovered)
	}
	if recovered[1].Status != "implemented" {
		t.Fatalf("completed slice must remain completed: %#v", recovered)
	}
}

func TestWorkflowChangeSliceDirectoryTargetCompletesOnNestedWrite(t *testing.T) {
	state := New(3)
	runID := state.StartWorkflowRun("complex-project-delivery", "migrate config")
	planned := []WorkflowChangeSliceSnapshot{{
		ID: "config", Title: "Config", TargetFiles: []string{"internal/config"},
		VerificationCommands: []string{"go test ./..."}, ContractHash: "contract", Status: "running",
	}}
	if err := state.UpdateWorkflowRunChangeSlicesDurable(runID, planned); err != nil {
		t.Fatalf("persist slices: %v", err)
	}
	if err := state.MarkWorkflowRunChangeSliceFilesDurable(runID, "implement", "config", []string{"internal/config/config.go"}); err != nil {
		t.Fatalf("record nested write: %v", err)
	}
	run, ok := state.WorkflowRun(runID)
	if !ok || run.ChangeSlices[0].Status != "implemented" {
		t.Fatalf("nested write should complete directory target: %#v", run.ChangeSlices)
	}
}

func TestWorkflowRunPersistsCompiledChangeSlices(t *testing.T) {
	state := New(3)
	runID := state.StartWorkflowRun("complex-project-delivery", "migrate storage")
	planned := []WorkflowChangeSliceSnapshot{{
		ID: "storage", Title: "Storage", InputHashes: map[string]string{"store.go": "abc"},
		TargetFiles: []string{"store.go"}, VerificationCommands: []string{"go test ./..."},
		ContractHash: "contract", Status: "planned",
	}}
	state.CompleteWorkflowRun(runID, "awaiting_approval", "planned", "approve-plan", "approve", nil, []WorkflowRunStageSnapshot{{
		Stage: "plan", Status: "completed", ChangeSlices: planned,
	}})
	run, ok := state.WorkflowRun(runID)
	if !ok || len(run.ChangeSlices) != 1 || run.ChangeSlices[0].InputHashes["store.go"] != "abc" {
		t.Fatalf("compiled change slices were not persisted: %#v", run)
	}
	run.ChangeSlices[0].InputHashes["store.go"] = "mutated"
	again, _ := state.WorkflowRun(runID)
	if again.ChangeSlices[0].InputHashes["store.go"] != "abc" {
		t.Fatalf("workflow snapshot must deep-copy change slices: %#v", again.ChangeSlices)
	}
}
