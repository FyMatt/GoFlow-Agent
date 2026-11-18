package session

import (
	"fmt"
	"strings"
	"testing"
)

func TestWorkflowToolOperationLimitFailsClosedWithoutEviction(t *testing.T) {
	state := New(4)
	runID := state.StartWorkflowRun("delivery", "apply changes")
	for i := 0; i < maxWorkflowToolOperations; i++ {
		key := fmt.Sprintf("operation-%03d", i)
		_, created, err := state.ReserveWorkflowToolOperation(WorkflowToolOperationSnapshot{
			Key:           key,
			RunID:         runID,
			Workflow:      "delivery",
			Stage:         "implement",
			ToolName:      "file_tools/apply_patch",
			ArgumentsHash: fmt.Sprintf("arguments-%03d", i),
		})
		if err != nil || !created {
			t.Fatalf("reserve operation %d: created=%v err=%v", i, created, err)
		}
	}
	_, created, err := state.ReserveWorkflowToolOperation(WorkflowToolOperationSnapshot{
		Key:           "operation-over-limit",
		RunID:         runID,
		Workflow:      "delivery",
		Stage:         "implement",
		ToolName:      "file_tools/apply_patch",
		ArgumentsHash: "arguments-over-limit",
	})
	if err == nil || created || !strings.Contains(err.Error(), "operation limit reached") {
		t.Fatalf("expected fail-closed limit error, created=%v err=%v", created, err)
	}
	run, ok := state.WorkflowRun(runID)
	if !ok || len(run.ToolOperations) != maxWorkflowToolOperations {
		t.Fatalf("expected all prior operations to remain, got %d", len(run.ToolOperations))
	}
	if run.ToolOperations[0].Key != "operation-000" {
		t.Fatalf("oldest idempotency record was evicted: %#v", run.ToolOperations[0])
	}
}
