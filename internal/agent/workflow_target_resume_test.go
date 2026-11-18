package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/interfaces"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestWorkflowInputResumeKeepsRemoteTargetBinding(t *testing.T) {
	for _, requested := range []string{"edge-a", "edge-b"} {
		t.Run(requested, func(t *testing.T) {
			home := t.TempDir()
			writeWorkflowGraphYAML(t, home, "remote-input", `
name: remote-input
stages:
  - name: scope
    node_type: input_gate
    params:
      manual: "true"
      fields: scope
      required: scope
    next: [inspect]
  - name: inspect
    agent: quality-engineer
    skill: code-audit
`)
			tool := schema.Tool{Name: "ssh_run_command", Server: "ssh_tools", Kind: "exec",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"target_id":{"type":"string"},"command":{"type":"string"}},"required":["target_id","command"]}`)}
			mcp := &stubRuntimeMCP{tools: []schema.Tool{tool}, result: schema.ToolResult{Content: `{"exit_code":0,"stdout":"uid=1000"}`}}
			model := &workflowRecordingLLMClient{responses: []schema.ChatResponse{
				{ToolCalls: []schema.ToolCall{{ID: "ssh-1", Name: "ssh_tools/ssh_run_command", Arguments: json.RawMessage(`{"target_id":"` + requested + `","command":"id"}`)}}},
				{Message: schema.Message{Content: "Inspection complete."}},
			}}
			runtime := newWorkflowGraphRuntime(t, home, workflowGraphTestSkills(), mcp, map[string]interfaces.LLMClient{"quality-engineer": model})
			runtime.cfg.MCP = append(runtime.cfg.MCP, config.MCPServerRef{Name: "ssh_tools", TargetScope: "host"})
			paused, err := runtime.WorkflowRunner().Run(WithExecutionTarget(t.Context(), "edge-a"), "remote-input", "Inspect the authorized host", true, nil)
			if err != nil || paused.Status != "awaiting_input" {
				t.Fatalf("expected manual input gate, got %s, %v", paused.Status, err)
			}
			result, err := runtime.WorkflowRunner().ResumeInput(context.Background(), paused.RunID, map[string]string{"scope": "Read-only authorized host"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			wantCalls := 0
			if requested == "edge-a" {
				wantCalls = 1
			}
			if mcp.calls != wantCalls {
				t.Fatalf("saved target edge-a, requested %s: got %d MCP calls, want %d; result=%#v", requested, mcp.calls, wantCalls, result)
			}
			if got := runtime.SessionSnapshot().Workflow.TargetID; got != "edge-a" {
				t.Fatalf("workflow lost saved target after input: %q", got)
			}
		})
	}
}
