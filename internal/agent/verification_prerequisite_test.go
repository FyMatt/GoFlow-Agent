package agent

import (
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestVerificationCannotPassAfterUnappliedImplementation(t *testing.T) {
	failed := schema.ToolResult{ToolName: "apply_patch", IsError: true, Content: "expected 1 occurrence(s) but found 0; no changes were written"}
	for _, stageName := range []string{"verify", "recovery-verify"} {
		source := "implement"
		if stageName == "recovery-verify" {
			source = "repair"
		}
		stage := workflowGraphStage{Name: stageName}
		result := workflowGraphStageResult(stage, schema.AgentResult{Output: "Verification Result: PASS; existing smoke tests pass"}, nil, nil, 1)
		applyWorkflowVerificationPrerequisite(stage, []WorkflowStageResult{{Stage: WorkflowStage(source), Result: schema.AgentResult{ToolResults: []schema.ToolResult{failed}}}}, &result)
		if result.Status != "warning" || result.Output.Decision != "blocked" || len(result.Result.Verification) != 1 || result.Result.Verification[0].Status != "failed" || !strings.HasPrefix(result.Output.Variables["verification"], "验证前提未满足") {
			t.Fatalf("old smoke tests must not override a zero-write implementation failure: %#v", result)
		}
	}
}

func TestVerificationAcceptsRecoveredWriteAndReadOnlyTasks(t *testing.T) {
	for _, tools := range [][]schema.ToolResult{
		{{ToolName: "read_file", Content: "existing project"}},
		{{ToolName: "apply_patch", IsError: true, Content: "no changes were written"}, {ToolName: "write_file", Content: `{"relative_path":"static/style.css"}`}},
	} {
		stage := workflowGraphStage{Name: "verify"}
		result := workflowGraphStageResult(stage, schema.AgentResult{Output: "Verification Result: pass"}, nil, nil, 1)
		applyWorkflowVerificationPrerequisite(stage, []WorkflowStageResult{{Stage: "implement", Result: schema.AgentResult{ToolResults: tools}}}, &result)
		if result.Status != "completed" || len(result.Result.Verification) != 0 {
			t.Fatalf("read-only tasks and recovered writes must not be blocked: %#v", result)
		}
	}
}
