package agent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestRequiredPlanMatchesReachableMutationStages(t *testing.T) {
	for _, test := range []struct {
		mode                          string
		mutation, reachable, rejected bool
	}{{"read", true, true, true}, {"write", true, true, false}, {"read", false, true, false}, {"read", true, false, false}} {
		stage := workflowGraphStage{Name: "outline", NodeType: "agent", Params: map[string]string{"change_slice_policy": "required", "require_workspace_mutation": "false"}, Next: []string{"confirm"}}
		graph := workflowGraph{Name: "custom", Stages: []workflowGraphStage{stage, {Name: "confirm", Next: []string{"work"}}, {Name: "work", NodeType: "agent", Params: map[string]string{"require_workspace_mutation": fmt.Sprint(test.mutation)}}}}
		if !test.reachable {
			graph.Stages[1].Next = nil
		}
		result := schema.AgentResult{Output: "```goflow-change-slices\n" + fmt.Sprintf(`[{"id":"change","title":"requested work","mode":%q,"target_files":["a.go"],"verification_commands":["git status --short"]}]`, test.mode) + "\n```"}
		err := (&WorkflowRunner{}).workflowGraphStageCompletionError(graph, stage, result)
		if (err != nil) != test.rejected || err != nil && !strings.Contains(err.Error(), "no writable change slices") {
			t.Fatalf("mode=%s mutation=%t reachable=%t: %v", test.mode, test.mutation, test.reachable, err)
		}
	}
}

func TestLegacyReadOnlyPlanReturnsToPlanningWithoutDroppingEvidence(t *testing.T) {
	graph := workflowGraph{Stages: []workflowGraphStage{{Name: "outline", Params: map[string]string{"change_slice_policy": "required"}, Next: []string{"work"}}, {Name: "work", Params: map[string]string{"require_workspace_mutation": "true"}}}}
	completed := []WorkflowStageResult{{Stage: "outline", Status: "completed", Result: schema.AgentResult{ToolResults: []schema.ToolResult{{CallID: "observed-source"}}}}, {Stage: "work", Result: schema.AgentResult{Incomplete: true}}}
	index, plan, before, ok := workflowGraphContinuationReplan(graph, completed, completed[1], []session.WorkflowChangeSliceSnapshot{{ID: "discover", ExecutionMode: "read"}})
	if !ok || index != 0 || len(before) != 0 || !plan.Result.Incomplete || plan.Result.ToolResults[0].CallID != "observed-source" {
		t.Fatalf("legacy read-only plan was not safely rebuilt: %#v", plan)
	}
}
