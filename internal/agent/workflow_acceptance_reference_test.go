package agent

import (
	"testing"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestWorkflowAcceptanceResultReferencePresence(t *testing.T) {
	for _, test := range []struct {
		name, output, ref string
		present           bool
	}{
		{"missing", `{}`, "result.alert_required", false},
		{"missing nested", `{"evidence":{}}`, "result.evidence.path", false},
		{"null", `{"alert_required":null}`, "result.alert_required", false},
		{"false is present", `{"alert_required":false}`, "result.alert_required", true},
		{"zero is present", `{"count":0}`, "result.count", true},
		{"empty string", `{"detail":""}`, "result.detail", false},
		{"empty array", `{"evidence":[]}`, "result.evidence", true},
		{"nested", `{"evidence":{"path":"input/log.txt"}}`, "result.evidence.path", true},
		{"raw output", "完整报告", "result.output", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			present := true
			stage := workflowGraphStage{AcceptanceCriteria: []workflowGraphAcceptanceCriterion{{Name: "field", Ref: test.ref, Exists: &present}}}
			result := WorkflowStageResult{Result: schema.AgentResult{Output: test.output}}
			checks := evaluateWorkflowGraphAcceptanceCriteria(stage, result)
			if len(checks) != 1 || (checks[0].Status == "passed") != test.present {
				t.Fatalf("presence=%t, got %#v", test.present, checks)
			}
			absent := false
			stage.AcceptanceCriteria[0].Exists = &absent
			checks = evaluateWorkflowGraphAcceptanceCriteria(stage, result)
			if (checks[0].Status == "passed") == test.present {
				t.Fatalf("absence check is inconsistent: %#v", checks)
			}
		})
	}
}

func TestWorkflowAcceptanceMappedMissingReferenceIsAbsent(t *testing.T) {
	stage := workflowGraphStage{Outputs: map[string]string{"missing": "result.missing", "literal": "'result.missing'"}}
	result := WorkflowStageResult{Result: schema.AgentResult{Output: `{}`}}
	result.Output = buildWorkflowStageOutput(stage, result.Result)
	if _, found := workflowGraphAcceptanceActual(stage, result, "outputs.missing"); found {
		t.Fatal("unresolved mapped reference was treated as evidence")
	}
	if value, found := workflowGraphAcceptanceActual(stage, result, "outputs.literal"); !found || value != "result.missing" {
		t.Fatalf("explicit literal changed: %q, %t", value, found)
	}
}
