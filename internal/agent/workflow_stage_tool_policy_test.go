package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/commandpolicy"
	"github.com/FyMatt/GoFlow-Agent/internal/interfaces"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestWorkflowStageStopsAdvertisingExhaustedReadTools(t *testing.T) {
	home := t.TempDir()
	writeWorkflowGraphYAML(t, home, "bounded-audit", `
name: bounded-audit
stages:
  - name: audit
    agent: auditor
    skill: code-audit
    params:
      max_read_calls: "1"
`)
	model := &workflowRecordingLLMClient{responses: []schema.ChatResponse{
		{ToolCalls: []schema.ToolCall{{ID: "read-once", Name: "read_file", Arguments: json.RawMessage(`{"path":"main.go"}`)}}},
		{Message: schema.Message{Content: "Audit complete using the observed source."}},
	}}
	mcp := &stubRuntimeMCP{tools: []schema.Tool{{Name: "read_file", Server: "file_tools", Kind: "read"}}, result: schema.ToolResult{Content: "package main"}}
	runtime := newWorkflowGraphRuntime(t, home, workflowGraphTestSkills(), mcp, map[string]interfaces.LLMClient{"auditor": model})
	result, err := runtime.WorkflowRunner().Run(t.Context(), "bounded-audit", "Review main.go", true, nil)
	if err != nil || result.Status != "completed" {
		t.Fatalf("audit failed: status=%s err=%v", result.Status, err)
	}
	if len(model.requests) != 2 {
		t.Fatalf("expected a read followed by a final report, got %d requests", len(model.requests))
	}
	if len(model.requests[0].Tools) != 1 || len(model.requests[1].Tools) != 0 || mcp.calls != 1 {
		t.Fatalf("exhausted tools still advertised: initial=%d remaining=%d calls=%d", len(model.requests[0].Tools), len(model.requests[1].Tools), mcp.calls)
	}
}

func TestWorkflowStageToolPolicyBlocksUnapprovedCommandClassBeforeExecution(t *testing.T) {
	execCtx := ExecutionContext{Tools: []schema.Tool{{Name: "run_command", Server: "command_tools", Kind: "exec"}}}
	calls := []schema.ToolCall{
		{ID: "inspect", Name: "command_tools/run_command", Arguments: json.RawMessage(`{"program":"find","args":["."]}`)},
		{ID: "test", Name: "command_tools/run_command", Arguments: json.RawMessage(`{"program":"go","args":["test","./..."]}`)},
	}
	allowed, blocked := enforceWorkflowStageToolPolicy(calls, nil, execCtx, workflowStageToolPolicy{
		MaxCommandCalls:       2,
		AllowedCommandClasses: []commandpolicy.Class{commandpolicy.ClassTest},
	})
	if len(allowed) != 1 || allowed[0].ID != "test" {
		t.Fatalf("expected only the test command to execute, got %#v", allowed)
	}
	if result, ok := blocked["inspect"]; !ok || !result.Denied || !result.IsError {
		t.Fatalf("expected inspection command to be denied before execution, got %#v", blocked)
	}
}

func TestWorkflowStageToolPolicyBlocksInvalidGoFmtBeforeExecution(t *testing.T) {
	execCtx := ExecutionContext{Tools: []schema.Tool{{Name: "run_command", Server: "command_tools", Kind: "exec"}}}
	calls := []schema.ToolCall{{
		ID: "invalid-format", Name: "command_tools/run_command", Arguments: json.RawMessage(`{"program":"go","args":["fmt","-l","."]}`),
	}}
	allowed, blocked := enforceWorkflowStageToolPolicy(calls, nil, execCtx, workflowStageToolPolicy{
		MaxCommandCalls:       1,
		AllowedCommandClasses: []commandpolicy.Class{commandpolicy.ClassFormat},
	})
	if len(allowed) != 0 || !strings.Contains(blocked["invalid-format"].Content, "use gofmt -l .") {
		t.Fatalf("expected invalid go fmt to be blocked with guidance, allowed=%#v blocked=%#v", allowed, blocked)
	}
}

func TestWorkflowStageToolPolicyCapsCommandAndReadCalls(t *testing.T) {
	execCtx := ExecutionContext{Tools: []schema.Tool{
		{Name: "run_command", Server: "command_tools", Kind: "exec"},
		{Name: "read_file", Server: "file_tools", Kind: "read"},
	}}
	completed := []schema.ToolResult{
		{CallID: "previous-command", ToolName: "run_command", Content: "ok"},
		{CallID: "previous-read", ToolName: "read_file", Content: "ok"},
	}
	calls := []schema.ToolCall{
		{ID: "command", Name: "command_tools/run_command", Arguments: json.RawMessage(`{"program":"go","args":["vet","./..."]}`)},
		{ID: "read", Name: "file_tools/read_file", Arguments: json.RawMessage(`{"path":"a.go"}`)},
	}
	allowed, blocked := enforceWorkflowStageToolPolicy(calls, completed, execCtx, workflowStageToolPolicy{
		MaxCommandCalls:       1,
		MaxReadCalls:          1,
		AllowedCommandClasses: []commandpolicy.Class{commandpolicy.ClassLint},
	})
	if len(allowed) != 0 {
		t.Fatalf("expected both limits to block execution, got %#v", allowed)
	}
	if len(blocked) != 2 || !blocked["command"].Denied || !blocked["read"].Denied {
		t.Fatalf("expected command and read calls to be denied, got %#v", blocked)
	}
}

func TestMergeWorkflowStageToolResultsPreservesCallOrder(t *testing.T) {
	calls := []schema.ToolCall{{ID: "one"}, {ID: "two"}, {ID: "three"}}
	executed := []schema.ToolResult{{CallID: "one", Content: "1"}, {CallID: "three", Content: "3"}}
	blocked := map[string]schema.ToolResult{"two": {CallID: "two", Content: "2", Denied: true}}
	merged := mergeWorkflowStageToolResults(calls, executed, blocked)
	if len(merged) != 3 || merged[0].CallID != "one" || merged[1].CallID != "two" || merged[2].CallID != "three" {
		t.Fatalf("unexpected merged order: %#v", merged)
	}
}
