package agent

import (
	"testing"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

// A read that guesses a wrong path and fails is an exploration mistake the
// model corrects itself. It is not evidence about the delivery, so it must not
// fail an otherwise verified quality gate.
func TestQualityGateDoesNotFailDeliveryForExplorationReadErrors(t *testing.T) {
	stage := workflowGraphStage{Name: "quality", Params: map[string]string{"verification_mode": "command"}}
	result := WorkflowStageResult{Stage: "review", Status: "completed", Result: schema.AgentResult{
		Output: "审查完成：四个阻断项均有补丁映射。",
		ToolResults: []schema.ToolResult{
			{CallID: "wrong-path", ToolName: "file_tools/read_file", IsError: true, Content: "read_file path not found: docs/security-report.md"},
			{CallID: "tests", ToolName: "run_command", Content: `{"program":"go","args":["test","./..."],"exit_code":0}`},
		},
	}}
	got := evaluateWorkflowGraphQualityGate(stage, []WorkflowStageResult{result})
	if !got.Passed {
		t.Fatalf("an exploration read error must not fail the gate: %#v", got.Failures)
	}
	if got.ExplorationErrors != 1 {
		t.Fatalf("expected one exploration error to be recorded, got %d", got.ExplorationErrors)
	}
	if got.ToolErrors != 0 {
		t.Fatalf("exploration errors must not be counted as delivery tool errors, got %d", got.ToolErrors)
	}
}

// A failed patch followed by a successful write to the same file is recovery,
// not a delivery error: retries legitimately carry different patch text.
func TestQualityGateTreatsALaterWriteToTheSamePathAsRecovery(t *testing.T) {
	stage := workflowGraphStage{Name: "final-quality", Params: map[string]string{"verification_mode": "command"}}
	result := WorkflowStageResult{Stage: "repair", Status: "completed", Result: schema.AgentResult{
		Output: "返修完成",
		ToolResults: []schema.ToolResult{
			{CallID: "patch-fail", ToolName: "file_tools/apply_patch", IsError: true, Content: `{"error":"edit 1 expected 1 occurrence(s) but found 0; no changes were written","relative_path":"internal/rollout/rollout.go"}`},
			{CallID: "write-ok", ToolName: "file_tools/write_file", Content: `{"relative_path":"internal/rollout/rollout.go","bytes":120,"workspace_changed":true}`},
			{CallID: "tests", ToolName: "run_command", Content: `{"program":"go","args":["test","./..."],"exit_code":0}`},
		},
	}}
	got := evaluateWorkflowGraphQualityGate(stage, []WorkflowStageResult{result})
	if !got.Passed {
		t.Fatalf("a later successful write must supersede the failed patch: %#v", got.Failures)
	}
	if got.ToolErrors != 0 || got.RecoveredToolErrors != 1 {
		t.Fatalf("unexpected counters: tool=%d recovered=%d", got.ToolErrors, got.RecoveredToolErrors)
	}
}

// A failed write with no later write to that path is still a delivery error.
func TestQualityGateKeepsUnrecoveredWriteFailures(t *testing.T) {
	stage := workflowGraphStage{Name: "final-quality", Params: map[string]string{"verification_mode": "command"}}
	result := WorkflowStageResult{Stage: "repair", Status: "completed", Result: schema.AgentResult{
		Output: "返修未完成",
		ToolResults: []schema.ToolResult{
			{CallID: "patch-fail", ToolName: "file_tools/apply_patch", IsError: true, Content: `{"error":"edit 1 expected 1 occurrence(s) but found 0; no changes were written","relative_path":"internal/rollout/rollout.go"}`},
			{CallID: "tests", ToolName: "run_command", Content: `{"program":"go","args":["test","./..."],"exit_code":0}`},
		},
	}}
	got := evaluateWorkflowGraphQualityGate(stage, []WorkflowStageResult{result})
	if got.Passed || got.ToolErrors != 1 {
		t.Fatalf("an unrecovered write failure must still fail the gate: %#v", got)
	}
}

func TestQualityGateStillFailsForSubstantiveToolErrors(t *testing.T) {
	stage := workflowGraphStage{Name: "quality", Params: map[string]string{"verification_mode": "command"}}
	result := WorkflowStageResult{Stage: "repair", Status: "completed", Result: schema.AgentResult{
		Output: "返修完成",
		ToolResults: []schema.ToolResult{
			{CallID: "patch", ToolName: "file_tools/apply_patch", IsError: true, Content: "edit 1 expected 1 occurrence(s) but found 0; no changes were written"},
			{CallID: "tests", ToolName: "run_command", Content: `{"program":"go","args":["test","./..."],"exit_code":0}`},
		},
	}}
	got := evaluateWorkflowGraphQualityGate(stage, []WorkflowStageResult{result})
	if got.Passed {
		t.Fatal("a failed write must still fail the gate")
	}
	if got.ToolErrors != 1 || got.ExplorationErrors != 0 {
		t.Fatalf("unexpected counters: tool=%d exploration=%d", got.ToolErrors, got.ExplorationErrors)
	}
}
