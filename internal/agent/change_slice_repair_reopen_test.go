package agent

import (
	"encoding/json"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

// A failed implementation stage blocks every unverified slice. The repair stage
// must be able to reopen exactly those slices, otherwise its writes are rejected
// and the delivery can never be fixed.
func TestRepairStageReopensBlockedChangeSlicesBeforeWriting(t *testing.T) {
	state := session.New(4)
	runID := state.StartWorkflowRun("security-audit-evidence-gate", "repair a blocked delivery")
	slices := []session.WorkflowChangeSliceSnapshot{
		{ID: "remediate-redaction", ExecutionMode: "write", TargetFiles: []string{"internal/audit/redact.go"}, Status: "blocked", BlockedReason: "stage remediate failed before this slice was verified"},
	}
	if err := state.UpdateWorkflowRunChangeSlicesDurable(runID, slices); err != nil {
		t.Fatalf("persist slices: %v", err)
	}
	runner := NewWorkflowRunner(&Runtime{session: state})
	runner.runID = runID

	writeTool := schema.Tool{Name: "file_tools/apply_patch", Server: "file_tools", Kind: "write", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)}
	execCtx := ExecutionContext{
		Profile:                config.AgentProfile{AllowedToolKinds: []config.ToolKind{config.ToolKindWrite}, ToolPolicy: config.ToolPolicyAllow},
		Tools:                  []schema.Tool{writeTool},
		Session:                state,
		WorkflowRunID:          runID,
		WorkflowName:           "security-audit-evidence-gate",
		WorkflowStage:          "repair",
		ChangeSliceEnforcement: true,
	}
	call := schema.ToolCall{Name: writeTool.Name, Arguments: []byte(`{"path":"internal/audit/redact.go"}`)}
	// A write attempt is the retry: it reopens the blocked slice instead of
	// rejecting the call and letting the stage loop until its tool cap.
	if _, err := execCtx.validateToolCall(call); err != nil {
		t.Fatalf("a blocked slice must accept the write that reopens it: %v", err)
	}
	run, ok := state.WorkflowRun(runID)
	if !ok || len(run.ChangeSlices) != 1 {
		t.Fatalf("slice state missing: %#v", run)
	}
	if run.ChangeSlices[0].Status != "running" || run.ChangeSlices[0].BlockedReason != "" {
		t.Fatalf("blocked slice was not reopened by the write attempt: %#v", run.ChangeSlices[0])
	}

	runner.reopenBlockedChangeSlices("repair")
	if _, err := execCtx.validateToolCall(call); err != nil {
		t.Fatalf("the repair stage must be able to write a reopened slice: %v", err)
	}
}
