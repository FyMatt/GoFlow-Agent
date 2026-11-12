package agent

import (
	"strconv"
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/interfaces"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestWorkflowStageRetryKeepsCompletedWriteObservation(t *testing.T) {
	for _, attempts := range []int{1, 2} {
		t.Run(strconv.Itoa(attempts)+"_attempts", func(t *testing.T) {
			model := &workflowRecordingLLMClient{responses: []schema.ChatResponse{
				{ToolCalls: []schema.ToolCall{{ID: "write-once", Name: "write_file", Arguments: []byte(`{"path":"main.go","content":"package main\n"}`)}}},
				{Message: schema.Message{Content: "The file was written, but the response was interrupted."}, StopReason: schema.StopReasonError},
				{Message: schema.Message{Content: "The requested file change is complete."}},
			}}
			mcp := &stubRuntimeMCP{
				tools:  []schema.Tool{{Name: "write_file", Server: "file_tools", Kind: "write"}},
				result: schema.ToolResult{Content: `{"relative_path":"main.go","before_sha256":"` + strings.Repeat("a", 64) + `","after_sha256":"` + strings.Repeat("b", 64) + `","added_lines":1}`},
			}
			runtime := newWorkflowGraphRuntime(t, t.TempDir(), workflowGraphTestSkills(), mcp, map[string]interfaces.LLMClient{"fixer": model})
			agent, _ := runtime.agentRunner("fixer")
			agent.profile.ToolPolicy = config.ToolPolicyAllow
			workflow := runtime.WorkflowRunner()
			workflow.runID = runtime.session.StartWorkflowRun("retry-evidence", "Write main.go once")
			stage := workflowGraphStage{Name: "implement", Agent: "fixer", Skill: "code-writing", Params: map[string]string{"require_workspace_mutation": "true"}, Retry: workflowGraphRetry{MaxAttempts: attempts}}
			skill, err := workflow.graphStageSkill(stage)
			if err != nil {
				t.Fatal(err)
			}
			result, actualAttempts, err := workflow.runWorkflowGraphExecutableStage(t.Context(), workflowGraph{Name: "retry-evidence"}, stage, "Write main.go exactly once, then report the actual result.", skill, nil)
			if (err != nil) != (attempts == 1) || result.Incomplete || actualAttempts != attempts {
				t.Fatalf("unexpected retry outcome: attempts=%d incomplete=%v err=%v", actualAttempts, result.Incomplete, err)
			}
			if mcp.calls != 1 || len(result.ToolResults) != 1 || result.ToolResults[0].CallID != "write-once" {
				t.Fatalf("expected one original write without replay: calls=%d results=%#v", mcp.calls, result.ToolResults)
			}
		})
	}
}
