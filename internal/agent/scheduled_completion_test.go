package agent

import (
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestScheduledStageCannotCompleteAfterOnlyOneSlice(t *testing.T) {
	state := session.New(4)
	id := state.StartWorkflowRun("delivery", "finish both changes")
	if err := state.UpdateWorkflowRunChangeSlicesDurable(id, []session.WorkflowChangeSliceSnapshot{
		{ID: "first", ExecutionMode: "write", TargetFiles: []string{"a.go"}, Status: "running", ChangedFiles: []string{"a.go"}, ContractHash: "first"},
		{ID: "second", ExecutionMode: "write", TargetFiles: []string{"b.go"}, Status: "planned", DependsOn: []string{"first"}, ContractHash: "second"},
	}); err != nil {
		t.Fatal(err)
	}
	runner := &WorkflowRunner{runtime: &Runtime{session: state}, runID: id}
	stage := workflowGraphStage{Name: "implement", Approval: true, Params: map[string]string{"use_change_slice_scheduler": "true", "approved_plan_scope": "true"}}
	result := schema.AgentResult{Output: "first slice done, continue later", Changes: []schema.Change{{Files: []string{"b.go"}}}}
	if err := runner.holdIncompleteChangeSlices(workflowGraph{}, stage, &result); err != nil {
		t.Fatal(err)
	}
	if !result.Incomplete || !strings.Contains(result.IncompleteReason, "second") {
		t.Fatalf("partial implementation incorrectly completed: %#v", result)
	}
	run, _ := state.WorkflowRun(id)
	if run.ChangeSlices[0].Status != "implemented" || run.ChangeSlices[1].Status != "planned" {
		t.Fatalf("checkpoint trusted model claims instead of observed writes: %#v", run.ChangeSlices)
	}
	if err := state.MarkWorkflowRunChangeSliceFilesDurable(id, "implement", "second", []string{"b.go"}); err != nil {
		t.Fatal(err)
	}
	complete := schema.AgentResult{Output: "both changes implemented"}
	if err := runner.holdIncompleteChangeSlices(workflowGraph{}, stage, &complete); err != nil || complete.Incomplete {
		t.Fatalf("observed completion did not advance: %#v, %v", complete, err)
	}
}

func TestSuccessfulFormattingDoesNotRequireAFileDiff(t *testing.T) {
	slices := []session.WorkflowChangeSliceSnapshot{{ID: "format", ExecutionMode: "write", Status: "planned", VerificationCommands: []string{"gofmt -w a.go", "go test ./..."}}}
	checkOnly := schema.ToolResult{CallID: "check", ToolName: "command_tools/run_command", Content: `{"program":"gofmt","args":["-l","a.go"],"exit_code":0,"stdout":""}`}
	if completeObservedFormattingSlices(slices, []schema.ToolResult{checkOnly}, "implement") {
		t.Fatal("a read-only formatting check completed a write action")
	}
	stdoutOnly := schema.ToolResult{CallID: "stdout", ToolName: "command_tools/run_command", Content: `{"program":"gofmt","args":["a.go"],"exit_code":0,"stdout":"package a"}`}
	stdoutPlan := []session.WorkflowChangeSliceSnapshot{{ID: "print", ExecutionMode: "write", Status: "planned", VerificationCommands: []string{"gofmt a.go"}}}
	if completeObservedFormattingSlices(stdoutPlan, []schema.ToolResult{stdoutOnly}, "implement") {
		t.Fatal("printing formatted source must not complete a write action")
	}
	write := schema.ToolResult{CallID: "format", ToolName: "command_tools/run_command", Content: `{"program":"gofmt","args":["-w","a.go"],"exit_code":0,"workspace_changed":false}`}
	if !completeObservedFormattingSlices(slices, []schema.ToolResult{write}, "implement") || slices[0].Status != "implemented" {
		t.Fatalf("an observed successful formatter was forced to invent a diff: %#v", slices)
	}
}

func TestLegacyPartialImplementationResumesBeforeVerification(t *testing.T) {
	graph := workflowGraph{Stages: []workflowGraphStage{{Name: "plan"}, {Name: "implement", Approval: true, Params: map[string]string{"use_change_slice_scheduler": "true", "approved_plan_scope": "true"}}, {Name: "verify"}}}
	completed := []WorkflowStageResult{{Stage: "plan"}, {Stage: "implement", Result: schema.AgentResult{ToolResults: []schema.ToolResult{{CallID: "existing-write"}}}}, {Stage: "verify", Result: schema.AgentResult{Incomplete: true}}}
	index, stage, prior, restored := workflowGraphContinuationRemediation(graph, completed, completed[2], []session.WorkflowChangeSliceSnapshot{{ID: "remaining", ExecutionMode: "write", Status: "planned"}})
	if !restored || index != 1 || len(prior) != 1 || !stage.Result.Incomplete || stage.Result.ToolResults[0].CallID != "existing-write" {
		t.Fatalf("legacy continuation must retain writes and return to unfinished implementation: %#v", stage)
	}
}
