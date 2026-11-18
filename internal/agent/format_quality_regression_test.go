package agent

import (
	"testing"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestQualityGateDoesNotWaiveAggregateFailureBecauseReportMentionsRace(t *testing.T) {
	stage := workflowGraphStage{Params: map[string]string{"allowed_infrastructure_failures": "go_race_unavailable", "failure_output_markers": "总体结论：未通过"}}
	result := WorkflowStageResult{Stage: "verify", Status: "completed", Result: schema.AgentResult{
		Output:       "总体结论：未通过\n格式检查发现未格式化文件；race按环境限制延期。",
		Verification: []schema.Verification{{Kind: "response", Status: "failed", Detail: "验证未完全通过：格式检查发现未格式化文件；go test -race ./... 因缺少CGO未启动"}},
		ToolResults: []schema.ToolResult{
			{CallID: "fallback", ToolName: "run_command", Content: `{"program":"go","args":["test","./..."],"exit_code":0}`},
			{CallID: "race", ToolName: "run_command", IsError: true, Content: `{"program":"go","args":["test","-race","./..."],"exit_code":2,"stderr":"go: -race requires cgo"}`},
		},
	}}
	if got := evaluateWorkflowGraphQualityGate(stage, []WorkflowStageResult{result}); got.Passed || len(got.Failures) == 0 {
		t.Fatalf("race fallback must not waive the whole failed report: %#v", got)
	}
}

func TestRaceOnlyAggregateToolFailureDoesNotRejectAllowedFallback(t *testing.T) {
	stage := workflowGraphStage{Params: map[string]string{"allowed_infrastructure_failures": "go_race_unavailable", "failure_output_markers": "总体结论：未通过"}}
	result := WorkflowStageResult{Stage: "verify", Status: "completed", Result: schema.AgentResult{
		Output:       "总体结论：通过\n本机race未通过环境启动检查，按允许范围延期；普通测试通过。",
		Verification: []schema.Verification{{Kind: "response", Status: "failed", Detail: "aggregate tool status includes deferred race"}},
		ToolResults: []schema.ToolResult{
			{CallID: "fallback", ToolName: "run_command", Content: `{"program":"go","args":["test","./..."],"exit_code":0}`},
			{CallID: "race", ToolName: "run_command", IsError: true, Content: `{"program":"go","args":["test","-race","./..."],"exit_code":2,"stderr":"go: -race requires cgo"}`},
		},
	}}
	if got := evaluateWorkflowGraphQualityGate(stage, []WorkflowStageResult{result}); !got.Passed || got.InfrastructureLimits != 1 {
		t.Fatalf("synthetic aggregate failure rejected an allowed infrastructure exception: %#v", got)
	}
}

func TestOverallVerdictUsesFirstLineNotQuotedHistory(t *testing.T) {
	for _, test := range []struct {
		output string
		passed bool
	}{
		{"总体结论：通过\n本机race未通过环境启动检查，按允许范围延期。\n此前总体结论：未通过，现已修复。", true},
		{"总体结论：未通过\n旧报告曾经写总体结论：通过，但本次发现缺陷。", false},
	} {
		verify := workflowGraphStage{Name: "verify", AcceptanceCriteria: []workflowGraphAcceptanceCriterion{{Name: "overall", Ref: "result.first_line", Equals: "总体结论：通过"}}}
		result := workflowGraphStageResult(verify, schema.AgentResult{Output: test.output, ToolResults: []schema.ToolResult{{CallID: "tests", ToolName: "run_command", Content: `{"program":"go","args":["test","./..."],"exit_code":0}`}}}, nil, nil, 1)
		quality := evaluateWorkflowGraphQualityGate(workflowGraphStage{Params: map[string]string{"require_acceptance": "true"}}, []WorkflowStageResult{result})
		if quality.Passed != test.passed {
			t.Fatalf("historical text changed the current verdict: %#v", quality)
		}
	}
}

func TestGofmtListOutputIsFailedQualityEvenWithZeroExitCode(t *testing.T) {
	for _, test := range []struct {
		args, stdout string
		failed       bool
	}{
		{`["-l","."]`, `"internal/api/status_test.go\n"`, true},
		{`["-l","."]`, `""`, false},
		{`["main.go"]`, `"package main\n"`, false},
	} {
		result := schema.ToolResult{CallID: "format", ToolName: "command_tools/run_command", Content: `{"program":"gofmt","args":` + test.args + `,"exit_code":0,"stdout":` + test.stdout + `}`}
		if got := workflowGraphQualityToolResultFailed(result); got != test.failed {
			t.Fatalf("gofmt %s failed=%v, want %v", test.args, got, test.failed)
		}
	}
}
