package agent

import (
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/interfaces"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestRequiredChangeSlicePromptPreservesPermissionModes(t *testing.T) {
	stage := workflowGraphStage{Name: "plan", Params: map[string]string{
		"change_slice_policy": "required", "output_contract": strings.Repeat("long resource prose ", 300),
	}, Context: workflowGraphStageContext{PromptMaxTokens: 600, ParametersMaxTokens: 30}}
	prompt := buildWorkflowGraphStagePrompt(workflowGraph{Name: "unknown-domain"}, stage, "Plan the requested change", nil)
	for _, required := range []string{"goflow-change-slices", "read", "write", "target_files", "verification_commands"} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("required machine contract %q was lost under budgeting", required)
		}
	}
	stage.Params["change_slice_policy"] = "off"
	if prompt := buildWorkflowGraphStagePrompt(workflowGraph{Name: "unknown-domain"}, stage, "Plan", nil); strings.Contains(prompt, "goflow-change-slices") {
		t.Fatal("disabled capability injected a change-slice contract")
	}
}

func TestInvalidChangeSliceModeHasActionableDiagnostic(t *testing.T) {
	stage := workflowGraphStage{Name: "plan", Params: map[string]string{"change_slice_policy": "required"}}
	var runner WorkflowRunner
	output := "```goflow-change-slices\n" + `[{"id":"cache","title":"Implement cache","mode":"implementation","depends_on":[],"input_files":[],"target_files":["pkg/cache.go"],"verification_commands":["go test ./..."]}]` + "\n```"
	err := runner.workflowGraphStageCompletionError(workflowGraph{Name: "custom"}, stage, schema.AgentResult{Output: output})
	if err == nil || !strings.Contains(err.Error(), "implementation") || !strings.Contains(err.Error(), "read") || !strings.Contains(err.Error(), "write") {
		t.Fatalf("invalid permission mode was not explained: %v", err)
	}
	if slices := compileWorkflowChangeSlices(output, nil); len(slices) != 0 {
		t.Fatal("unknown mode must not silently become writable")
	}
}

func TestRequiredChangeSliceRetryRepairsModelContractOnly(t *testing.T) {
	home := t.TempDir()
	writeWorkflowGraphYAML(t, home, "contract-retry", `
name: contract-retry
stages:
  - name: plan
    agent: planner
    skill: execution-plan
    params:
      change_slice_policy: required
    retry:
      max_attempts: 2
    next: [end]
  - name: end
    node_type: end
`)
	bad := "```goflow-change-slices\n" + `[{"id":"change","title":"Requested change","mode":"implementation","depends_on":[],"input_files":[],"target_files":["new.go"],"verification_commands":["go test ./..."]}]` + "\n```"
	good := strings.Replace(bad, `"implementation"`, `"write"`, 1)
	model := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: bad}}, {Message: schema.Message{Content: good}}}}
	runtime := newWorkflowGraphRuntime(t, home, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{"planner": model})
	result, err := runtime.WorkflowRunner().Run(t.Context(), "contract-retry", "Plan a new file", true, nil)
	if err != nil || result.Status != "completed" || len(model.requests) != 2 {
		t.Fatalf("bounded contract repair failed: status=%s calls=%d error=%v", result.Status, len(model.requests), err)
	}
	var retryMessages strings.Builder
	for _, message := range model.requests[1].Messages {
		retryMessages.WriteString(message.Content)
	}
	if !strings.Contains(retryMessages.String(), `mode="implementation"`) {
		t.Fatal("retry did not carry the actual validation failure")
	}
}
