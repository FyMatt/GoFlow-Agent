package agent

import (
	"testing"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestStageDecisionHonorsDeclaredConclusionWhenOnlyRaceIsUnavailable(t *testing.T) {
	output := WorkflowStageOutput{
		RawOutput: "总体结论：通过\n\n## Recovery Verification Result\n普通测试与格式检查通过；本机 race 因 CGO 未启用延期。",
		ToolResults: []schema.ToolResult{
			{CallID: "tests", ToolName: "run_command", Content: `{"program":"go","args":["test","./..."],"exit_code":0}`},
			{CallID: "race", ToolName: "run_command", IsError: true, Content: `{"program":"go","args":["test","-race","./..."],"exit_code":2,"stderr":"go: -race requires cgo"}`},
		},
	}
	if got := workflowStageCanonicalDecision(&output); got != "passed" {
		t.Fatalf("an approved race deferral must not make the declared 通过 a failure, got %q", got)
	}
}

func TestStageDecisionKeepsClaimHonestWhenARealCommandFailed(t *testing.T) {
	output := WorkflowStageOutput{
		RawOutput: "总体结论：通过\n\n## Static Analysis\n格式检查声称通过。",
		ToolResults: []schema.ToolResult{
			{CallID: "fmt", ToolName: "run_command", IsError: true, Content: `{"program":"gofmt","args":["-l","."],"exit_code":0,"stdout":"main.go\n"}`},
		},
	}
	if got := workflowStageCanonicalDecision(&output); got != "failed" {
		t.Fatalf("prose must not override a failed command, got %q", got)
	}
}

func TestStageDecisionUsesDeclaredFailureConclusion(t *testing.T) {
	output := WorkflowStageOutput{
		RawOutput: "总体结论：未通过\n\n## Verification Result\n格式检查未通过。",
		ToolResults: []schema.ToolResult{
			{CallID: "tests", ToolName: "run_command", Content: `{"program":"go","args":["test","./..."],"exit_code":0}`},
		},
	}
	if got := workflowStageCanonicalDecision(&output); got != "failed" {
		t.Fatalf("a declared 未通过 conclusion must stay failed, got %q", got)
	}
}

func TestStageDecisionStillFallsBackToFailedToolResultsWithoutConclusion(t *testing.T) {
	output := WorkflowStageOutput{
		RawOutput:   "## Result\n没有结论行。",
		ToolResults: []schema.ToolResult{{CallID: "tests", ToolName: "run_command", IsError: true, Content: `{"program":"go","args":["test","./..."],"exit_code":1}`}},
	}
	if got := workflowStageCanonicalDecision(&output); got != "failed" {
		t.Fatalf("uncaptioned failing evidence must stay failed, got %q", got)
	}
}
