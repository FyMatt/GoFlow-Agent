package agent

import (
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/interfaces"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestChangeSliceContractRetryKeepsSuccessfulReadEvidence(t *testing.T) {
	home := t.TempDir()
	writeWorkflowGraphYAML(t, home, "planning-retry-evidence", `
name: planning-retry-evidence
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
	bad := "goflow-change-slices\n" + `[{"id":"fix","title":"Fix code","mode":"implementation","depends_on":[],"input_files":["main.go"],"target_files":["main.go"],"verification_commands":["go test ./..."]}]`
	good := strings.Replace(bad, `"implementation"`, `"write"`, 1)
	model := &workflowRecordingLLMClient{responses: []schema.ChatResponse{
		{ToolCalls: []schema.ToolCall{{ID: "observed-once", Name: "read_file", Arguments: []byte(`{"path":"main.go"}`)}}},
		{Message: schema.Message{Content: bad}},
		{Message: schema.Message{Content: good}},
	}}
	mcp := &stubRuntimeMCP{tools: []schema.Tool{{Name: "read_file", Server: "file_tools", Kind: "read"}}, result: schema.ToolResult{Content: `{"relative_path":"main.go","sha256":"` + strings.Repeat("a", 64) + `"}`}}
	runtime := newWorkflowGraphRuntime(t, home, workflowGraphTestSkills(), mcp, map[string]interfaces.LLMClient{"planner": model})
	result, err := runtime.WorkflowRunner().Run(t.Context(), "planning-retry-evidence", "Fix the observed main.go", true, nil)
	if err != nil || result.Status != "completed" {
		t.Fatalf("format correction lost already collected evidence: status=%s error=%v", result.Status, err)
	}
	plan := workflowGraphStageResultByName(t, result.CompletedStages, "plan")
	if mcp.calls != 1 || len(plan.Result.ToolResults) != 1 || plan.Result.ToolResults[0].CallID != "observed-once" {
		t.Fatalf("read was repeated or falsely duplicated: calls=%d results=%#v", mcp.calls, plan.Result.ToolResults)
	}
	run, _ := runtime.session.WorkflowRun(result.RunID)
	readEvents := 0
	for _, event := range run.Events {
		if event.Type == "tool_result" && event.ToolCallID == "observed-once" {
			readEvents++
		}
	}
	if readEvents != 1 {
		t.Fatalf("planning retry duplicated observed read events: %d", readEvents)
	}
}
