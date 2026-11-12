package agent

import (
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestPlanRefreshDoesNotResetCompletedSlices(t *testing.T) {
	output := "```goflow-change-slices\n" + `[{"id":"first","title":"first","mode":"write","target_files":["a.go"],"verification_commands":["go test ./..."]},{"id":"second","title":"second","mode":"write","depends_on":["first"],"target_files":["b.go"],"verification_commands":["go test ./..."]}]` + "\n```"
	slices := compileWorkflowChangeSlices(output, nil)
	slices[0].Status = "implemented"
	slices[0].ContractHash = "approved-from-full-evidence"
	slices[0].ChangedFiles = []string{"a.go"}
	slices[1].Status = "running"
	state := session.New(4)
	id := state.StartWorkflowRun("delivery", "continue the remaining slice")
	if err := state.UpdateWorkflowRunChangeSlicesDurable(id, slices); err != nil {
		t.Fatal(err)
	}
	runner := &WorkflowRunner{runtime: &Runtime{session: state}, runID: id}
	graph := workflowGraph{Stages: []workflowGraphStage{{Name: "plan", Params: map[string]string{"change_slice_policy": "required"}}}}
	runner.refreshWorkflowPlanChangeSlices(graph, []WorkflowStageResult{{Stage: "plan", Status: "completed", Result: schema.AgentResult{Output: output}}})
	run, _ := state.WorkflowRun(id)
	if run.ChangeSlices[0].Status != "implemented" || run.ChangeSlices[1].Status != "running" || len(run.ChangeSlices[0].ChangedFiles) != 1 {
		t.Fatalf("continuation reset durable progress: %#v", run.ChangeSlices)
	}
}

func TestPlanRefreshAcceptsAnActuallyChangedPlan(t *testing.T) {
	oldPlan := "```goflow-change-slices\n" + `[{"id":"change","title":"change","mode":"write","target_files":["a.go"],"verification_commands":["go test ./..."]}]` + "\n```"
	newPlan := "```goflow-change-slices\n" + `[{"id":"change","title":"change","mode":"write","target_files":["b.go"],"verification_commands":["go test ./..."]}]` + "\n```"
	state := session.New(4)
	id := state.StartWorkflowRun("delivery", "apply corrected scope")
	if err := state.UpdateWorkflowRunChangeSlicesDurable(id, compileWorkflowChangeSlices(oldPlan, nil)); err != nil {
		t.Fatal(err)
	}
	runner := &WorkflowRunner{runtime: &Runtime{session: state}, runID: id}
	graph := workflowGraph{Stages: []workflowGraphStage{{Name: "plan", Params: map[string]string{"change_slice_policy": "required"}}}}
	runner.refreshWorkflowPlanChangeSlices(graph, []WorkflowStageResult{{Stage: "plan", Status: "completed", Result: schema.AgentResult{Output: newPlan}}})
	run, _ := state.WorkflowRun(id)
	if run.ChangeSlices[0].TargetFiles[0] != "b.go" {
		t.Fatalf("corrected plan was ignored: %#v", run.ChangeSlices)
	}
}

func TestPlanRefreshAcceptsCorrectedWritePlanAfterLegacyReadOnlyPlan(t *testing.T) {
	read := "```goflow-change-slices\n" + `[{"id":"discover","title":"discover","mode":"read","target_files":["a.go"],"verification_commands":["git status --short"]}]` + "\n```"
	write := "```goflow-change-slices\n" + `[{"id":"change","title":"change","mode":"write","target_files":["a.go"],"verification_commands":["go test ./..."]}]` + "\n```"
	state := session.New(4)
	id := state.StartWorkflowRun("delivery", "apply corrected plan")
	if err := state.UpdateWorkflowRunChangeSlicesDurable(id, compileWorkflowChangeSlices(read, nil)); err != nil {
		t.Fatal(err)
	}
	runner := &WorkflowRunner{runtime: &Runtime{session: state}, runID: id}
	graph := workflowGraph{Stages: []workflowGraphStage{{Name: "plan", Params: map[string]string{"change_slice_policy": "required"}}}}
	runner.refreshWorkflowPlanChangeSlices(graph, []WorkflowStageResult{{Stage: "plan", Status: "completed", Result: schema.AgentResult{Output: write}}})
	run, _ := state.WorkflowRun(id)
	if len(run.ChangeSlices) != 1 || run.ChangeSlices[0].ID != "change" || run.ChangeSlices[0].ExecutionMode != "write" {
		t.Fatalf("corrected write plan remained stuck on old read-only scope: %#v", run.ChangeSlices)
	}
}
