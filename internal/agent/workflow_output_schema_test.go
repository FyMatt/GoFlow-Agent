package agent

import (
	"testing"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestWorkflowOutputSchemaRejectsInvalidValuesBeforeRouting(t *testing.T) {
	stage := workflowGraphStage{Name: "observe", Params: map[string]string{
		"output_schema_json": `{"type":"object","required":["alert_required","severity"],"properties":{"alert_required":{"type":"boolean"},"severity":{"type":"string","enum":["info","warning"]}}}`,
	}}
	for _, test := range []struct {
		output string
		valid  bool
	}{
		{`{"alert_required":true,"severity":"warning"}`, true},
		{`{"alert_required":false,"severity":"info"}`, true},
		{`{"alert_required":null,"severity":"info"}`, false},
		{`{"alert_required":"false","severity":"info"}`, false},
		{`{"alert_required":false,"severity":"safe"}`, false},
		{`{"severity":"info"}`, false},
	} {
		result := WorkflowStageResult{Stage: "observe", Result: schema.AgentResult{Output: test.output}}
		result.Output = buildWorkflowStageOutput(stage, result.Result)
		failure := workflowGraphStageContractFailure(stage, result)
		if failure.Failed == test.valid || (!test.valid && !failure.Required) {
			t.Fatalf("output %s valid=%t, failure=%#v", test.output, test.valid, failure)
		}
	}
}

func TestWorkflowOutputSchemaMustBeJSONObject(t *testing.T) {
	for _, value := range []string{"[", "[]", "null", `"object"`} {
		stage := workflowGraphStage{Name: "observe", Params: map[string]string{"output_schema_json": value}}
		if err := validateWorkflowGraphStageCapabilities("custom", stage); err == nil {
			t.Fatalf("invalid schema accepted: %s", value)
		}
	}
}
