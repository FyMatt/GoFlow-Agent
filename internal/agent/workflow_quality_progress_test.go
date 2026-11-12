package agent

import (
	"testing"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestQualityFailureEventKeepsContinuingWorkflowRunning(t *testing.T) {
	for _, test := range []struct {
		name, status, want string
		halt               bool
	}{
		{"continue to report", "completed", "running", false},
		{"continue to repair", "completed", "running", false},
		{"halt", "blocked", "blocked", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var events []schema.StreamEvent
			var runner WorkflowRunner
			runner.emitWorkflowGraphQualityGateFailureEvent(workflowGraph{Name: "custom"}, workflowGraphStage{Name: "quality"}, WorkflowStageResult{}, workflowGraphControlDecision{Kind: "quality_gate", Passed: false, Status: test.status, Halt: test.halt}, func(event schema.StreamEvent) error {
				events = append(events, event)
				return nil
			})
			if len(events) != 1 || events[0].WorkflowStatus != test.want || events[0].NeedsAction != test.halt {
				t.Fatalf("unexpected workflow lifecycle event: %#v", events)
			}
		})
	}
}
