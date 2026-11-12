package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/executionfingerprint"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

type idempotencyMCP struct {
	tools  []schema.Tool
	calls  int
	result schema.ToolResult
	err    error
	onCall func()
}

func (m *idempotencyMCP) ListTools(context.Context) ([]schema.Tool, error) {
	return m.tools, nil
}

func (m *idempotencyMCP) RefreshTools(context.Context) ([]schema.Tool, error) {
	return m.tools, nil
}

func (m *idempotencyMCP) HealthStatus(context.Context) map[string]string {
	return map[string]string{"test": "healthy"}
}

func (m *idempotencyMCP) ToolNames() []string {
	names := make([]string, 0, len(m.tools))
	for _, tool := range m.tools {
		names = append(names, tool.Name)
	}
	return names
}

func (m *idempotencyMCP) CallTool(context.Context, string, []byte) (schema.ToolResult, error) {
	m.calls++
	if m.onCall != nil {
		m.onCall()
	}
	if m.err != nil {
		return schema.ToolResult{}, m.err
	}
	return m.result, nil
}

func TestOrdinaryAgentReusesDuplicateReadsUntilWorkspaceMutation(t *testing.T) {
	state := session.New(4)
	runID := state.StartAgentRun("inspect then update")
	readTool := schema.Tool{Name: "file_tools/read_file", Server: "file_tools", Kind: "read", InputSchema: []byte(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`)}
	writeTool := schema.Tool{Name: "file_tools/write_file", Server: "file_tools", Kind: "write", InputSchema: []byte(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`)}
	mcp := &idempotencyMCP{tools: []schema.Tool{readTool, writeTool}, result: schema.ToolResult{Content: "saved result"}}
	execCtx := newExecutionContext("software-engineer", config.AgentProfile{
		AllowedToolKinds: []config.ToolKind{config.ToolKindRead, config.ToolKindWrite},
		ToolPolicy:       config.ToolPolicyAllow,
	}, []schema.Tool{readTool, writeTool}, nil, t.TempDir())
	execCtx.AgentRunID = runID
	execCtx.Session = state
	if err := os.WriteFile(filepath.Join(execCtx.WorkspaceRoot, "app.py"), []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	executor := NewExecutor(mcp)

	read := func(id string) schema.ToolCall {
		return schema.ToolCall{ID: id, Name: readTool.Name, Arguments: []byte(`{"path":"app.py"}`)}
	}
	first, err := executor.RunToolCalls(context.Background(), execCtx, []schema.ToolCall{read("read-1")}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := executor.RunToolCalls(context.Background(), execCtx, []schema.ToolCall{read("read-2")}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if mcp.calls != 1 || len(first) != 1 || len(second) != 1 || second[0].CallID != "read-2" || second[0].Summary != "reused unchanged read result" {
		t.Fatalf("expected second read to reuse cached evidence: calls=%d first=%#v second=%#v", mcp.calls, first, second)
	}

	write := schema.ToolCall{ID: "write-1", Name: writeTool.Name, Arguments: []byte(`{"path":"app.py","content":"changed"}`)}
	if _, err := executor.RunToolCalls(context.Background(), execCtx, []schema.ToolCall{write}, nil, nil); err != nil {
		t.Fatal(err)
	}
	third, err := executor.RunToolCalls(context.Background(), execCtx, []schema.ToolCall{read("read-3")}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if mcp.calls != 3 || len(third) != 1 || third[0].Summary == "reused unchanged read result" {
		t.Fatalf("successful write must invalidate read cache: calls=%d third=%#v", mcp.calls, third)
	}
}

func TestOrdinaryAgentDoesNotReuseUnchangedFailedValidation(t *testing.T) {
	state := session.New(4)
	runID := state.StartAgentRun("run tests")
	tool := reusableValidationTestTool()
	result := schema.ToolResult{Content: `{"exit_code":1,"workspace_changed":false,"stderr":"No module named pytest"}`, IsError: true}
	mcp := &idempotencyMCP{tools: []schema.Tool{tool}, result: result}
	execCtx := newExecutionContext("software-engineer", config.AgentProfile{
		AllowedToolKinds: []config.ToolKind{config.ToolKindExec},
		ToolPolicy:       config.ToolPolicyAllow,
	}, []schema.Tool{tool}, nil, t.TempDir())
	execCtx.AgentRunID = runID
	execCtx.Session = state
	executor := NewExecutor(mcp)
	call := func(id string) schema.ToolCall {
		return schema.ToolCall{ID: id, Name: tool.Name, Arguments: []byte(`{"program":"python","args":["-m","pytest","-q"],"reuse_if_unchanged":true}`)}
	}
	first, err := executor.RunToolCalls(context.Background(), execCtx, []schema.ToolCall{call("test-1")}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := executor.RunToolCalls(context.Background(), execCtx, []schema.ToolCall{call("test-2")}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if mcp.calls != 2 || len(first) != 1 || len(second) != 1 || !second[0].IsError || second[0].Summary == "reused unchanged validation result" {
		t.Fatalf("failed validation must execute again: calls=%d first=%#v second=%#v", mcp.calls, first, second)
	}
}

func TestOrdinaryAgentNeverCachesLiveExternalReads(t *testing.T) {
	for _, tool := range []schema.Tool{
		{Name: "web_tools/browser_snapshot", Server: "web_tools", Kind: "read"},
		{Name: "web_tools/http_get", Server: "web_tools", Kind: "read"},
		{Name: "external/read_file", Server: "external", Kind: "read"},
	} {
		t.Run(tool.Name, func(t *testing.T) {
			state := session.New(4)
			execCtx := idempotencyExecutionContext(state, "", tool, false)
			execCtx.AgentRunID = state.StartAgentRun("verify live state")
			mcp := &idempotencyMCP{result: schema.ToolResult{Content: "first live state"}}
			call := schema.ToolCall{ID: "first", Name: tool.Name, Arguments: []byte(`{}`)}
			if _, _, err := executeToolCallWithIdempotency(context.Background(), mcp, execCtx, call, tool); err != nil {
				t.Fatal(err)
			}
			mcp.result.Content = "second live state"
			call.ID = "second"
			result, replayed, err := executeToolCallWithIdempotency(context.Background(), mcp, execCtx, call, tool)
			if err != nil || replayed || mcp.calls != 2 || result.Content != "second live state" {
				t.Fatalf("external state must be read live: result=%#v replayed=%v calls=%d err=%v", result, replayed, mcp.calls, err)
			}
		})
	}
}

func TestRetryableSideEffectUsesOneDurableOperationForEquivalentArguments(t *testing.T) {
	state := session.New(4)
	runID := state.StartWorkflowRun("delivery", "apply change")
	tool := idempotencyTestTool("write")
	mcp := &idempotencyMCP{tools: []schema.Tool{tool}, result: schema.ToolResult{Content: "changed"}}
	execCtx := idempotencyExecutionContext(state, runID, tool, true)

	first := schema.ToolCall{ID: "call-1", Name: tool.Name, Arguments: []byte(`{"path":"a.txt","content":"hello"}`)}
	second := schema.ToolCall{ID: "call-2", Name: tool.Name, Arguments: []byte(`{ "content": "hello", "path": "a.txt" }`)}
	firstResults, err := NewExecutor(mcp).RunToolCalls(context.Background(), execCtx, []schema.ToolCall{first}, nil, nil)
	if err != nil {
		t.Fatalf("first RunToolCalls: %v", err)
	}
	secondResults, err := NewExecutor(mcp).RunToolCalls(context.Background(), execCtx, []schema.ToolCall{second}, nil, nil)
	if err != nil {
		t.Fatalf("second RunToolCalls: %v", err)
	}
	if mcp.calls != 1 {
		t.Fatalf("expected one MCP side effect, got %d", mcp.calls)
	}
	if len(firstResults) != 1 || firstResults[0].CallID != "call-1" || firstResults[0].Content != "changed" {
		t.Fatalf("unexpected first result: %#v", firstResults)
	}
	if len(secondResults) != 1 || secondResults[0].CallID != "call-2" || secondResults[0].Content != "changed" {
		t.Fatalf("expected replay with current call id, got %#v", secondResults)
	}
	run, ok := state.WorkflowRun(runID)
	if !ok || len(run.ToolOperations) != 1 || run.ToolOperations[0].Status != session.WorkflowToolOperationCompleted {
		t.Fatalf("expected one completed operation, got %#v", run.ToolOperations)
	}
}

func TestRetryableSideEffectExecutesAgainAfterApprovalPlaceholder(t *testing.T) {
	state := session.New(4)
	runID := state.StartWorkflowRun("docs", "produce")
	tool := idempotencyTestTool("write")
	mcp := &idempotencyMCP{tools: []schema.Tool{tool}, result: schema.ToolResult{Content: "requires approval", IsError: true, Suspended: true}}
	execCtx := idempotencyExecutionContext(state, runID, tool, true)
	call := schema.ToolCall{ID: "call-1", Name: tool.Name, Arguments: []byte(`{"path":"deliverables/report.docx"}`)}
	first, err := NewExecutor(mcp).RunToolCalls(context.Background(), execCtx, []schema.ToolCall{call}, nil, nil)
	if err != nil {
		t.Fatalf("first RunToolCalls: %v", err)
	}
	if len(first) != 1 || !first[0].Suspended {
		t.Fatalf("expected suspended approval placeholder, got %#v", first)
	}
	mcp.result = schema.ToolResult{Content: "filled"}
	call.ID = "call-2"
	second, err := NewExecutor(mcp).RunToolCalls(context.Background(), execCtx, []schema.ToolCall{call}, nil, nil)
	if err != nil {
		t.Fatalf("second RunToolCalls: %v", err)
	}
	if mcp.calls != 2 {
		t.Fatalf("approval placeholder must not consume the durable write slot, got %d calls", mcp.calls)
	}
	if len(second) != 1 || second[0].Suspended || second[0].Content != "filled" {
		t.Fatalf("expected real write after approval, got %#v", second)
	}
}

func TestRetryableSideEffectReplaysAfterSessionReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	state := session.New(4)
	state.SetPersistPath(path)
	runID := state.StartWorkflowRun("delivery", "apply change")
	tool := idempotencyTestTool("write")
	call := schema.ToolCall{ID: "call-before-restart", Name: tool.Name, Arguments: []byte(`{"path":"a.txt"}`)}
	firstMCP := &idempotencyMCP{tools: []schema.Tool{tool}, result: schema.ToolResult{Content: "changed"}}
	if _, err := NewExecutor(firstMCP).RunToolCalls(context.Background(), idempotencyExecutionContext(state, runID, tool, true), []schema.ToolCall{call}, nil, nil); err != nil {
		t.Fatalf("first RunToolCalls: %v", err)
	}

	reloaded := session.New(4)
	reloaded.SetPersistPath(path)
	if err := reloaded.Load(path); err != nil {
		t.Fatalf("Load: %v", err)
	}
	secondMCP := &idempotencyMCP{tools: []schema.Tool{tool}, result: schema.ToolResult{Content: "must not run"}}
	call.ID = "call-after-restart"
	results, err := NewExecutor(secondMCP).RunToolCalls(context.Background(), idempotencyExecutionContext(reloaded, runID, tool, true), []schema.ToolCall{call}, nil, nil)
	if err != nil {
		t.Fatalf("replayed RunToolCalls: %v", err)
	}
	if secondMCP.calls != 0 {
		t.Fatalf("expected no MCP call after reload, got %d", secondMCP.calls)
	}
	if len(results) != 1 || results[0].CallID != "call-after-restart" || results[0].Content != "changed" {
		t.Fatalf("unexpected reloaded replay result: %#v", results)
	}
}

func TestRetryableSideEffectRefusesUncertainStartedOperation(t *testing.T) {
	state := session.New(4)
	runID := state.StartWorkflowRun("delivery", "apply change")
	tool := idempotencyTestTool("write")
	call := schema.ToolCall{ID: "call-1", Name: tool.Name, Arguments: []byte(`{"path":"a.txt"}`)}
	execCtx := idempotencyExecutionContext(state, runID, tool, true)
	operation, err := workflowToolOperationForCall(execCtx, call, tool)
	if err != nil {
		t.Fatalf("workflowToolOperationForCall: %v", err)
	}
	if _, created, err := state.ReserveWorkflowToolOperation(operation); err != nil || !created {
		t.Fatalf("ReserveWorkflowToolOperation: created=%v err=%v", created, err)
	}
	mcp := &idempotencyMCP{tools: []schema.Tool{tool}}
	_, err = NewExecutor(mcp).RunToolCalls(context.Background(), execCtx, []schema.ToolCall{call}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "outcome is uncertain") {
		t.Fatalf("expected uncertain operation error, got %v", err)
	}
	if mcp.calls != 0 {
		t.Fatalf("uncertain operation must not call MCP, got %d", mcp.calls)
	}
}

func TestToolOperationLedgerSkipsReadOnlyAndNonRetryableCalls(t *testing.T) {
	tests := []struct {
		name    string
		kind    string
		enabled bool
	}{
		{name: "read only", kind: "read", enabled: true},
		{name: "non retryable", kind: "write", enabled: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := session.New(4)
			runID := state.StartWorkflowRun("delivery", "inspect")
			tool := idempotencyTestTool(test.kind)
			mcp := &idempotencyMCP{tools: []schema.Tool{tool}, result: schema.ToolResult{Content: "ok"}}
			call := schema.ToolCall{ID: "call-1", Name: tool.Name, Arguments: []byte(`{"path":"a.txt"}`)}
			if _, err := NewExecutor(mcp).RunToolCalls(context.Background(), idempotencyExecutionContext(state, runID, tool, test.enabled), []schema.ToolCall{call}, nil, nil); err != nil {
				t.Fatalf("RunToolCalls: %v", err)
			}
			run, _ := state.WorkflowRun(runID)
			if len(run.ToolOperations) != 0 {
				t.Fatalf("expected no tool operations, got %#v", run.ToolOperations)
			}
			if mcp.calls != 1 {
				t.Fatalf("expected one ordinary MCP call, got %d", mcp.calls)
			}
		})
	}
}

func TestManagedProcessCallsNeverReplayStaleLifecycleResults(t *testing.T) {
	for _, name := range []string{"start_process", "process_status", "stop_process"} {
		t.Run(name, func(t *testing.T) {
			state := session.New(4)
			runID := state.StartWorkflowRun("delivery", "verify live service")
			kind := "exec"
			if name == "process_status" {
				kind = "read"
			}
			tool := schema.Tool{Name: "command_tools/" + name, Server: "command_tools", Kind: kind}
			execCtx := idempotencyExecutionContext(state, runID, tool, true)
			call := schema.ToolCall{ID: "first", Name: tool.Name, Arguments: json.RawMessage(`{"process_id":"service"}`)}
			// Simulate a completed operation persisted by the previous service.
			operation, err := workflowToolOperationForCall(execCtx, call, tool)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := state.ReserveWorkflowToolOperation(operation); err != nil {
				t.Fatal(err)
			}
			if _, err := state.CompleteWorkflowToolOperation(runID, operation.Key, schema.ToolResult{Content: `{"running":true,"pid":42}`}); err != nil {
				t.Fatal(err)
			}
			mcp := &idempotencyMCP{result: schema.ToolResult{Content: `{"running":true,"pid":123}`}}
			if _, replay, err := executeToolCallWithIdempotency(context.Background(), mcp, execCtx, call, tool); err != nil || replay {
				t.Fatalf("first lifecycle call: replay=%v err=%v", replay, err)
			}
			mcp.result = schema.ToolResult{Content: `{"running":false,"pid":456}`}
			call.ID = "second"
			result, replay, err := executeToolCallWithIdempotency(context.Background(), mcp, execCtx, call, tool)
			if err != nil || replay || mcp.calls != 2 || !strings.Contains(result.Content, "456") {
				t.Fatalf("lifecycle must query the process manager: result=%#v replay=%v calls=%d err=%v", result, replay, mcp.calls, err)
			}
			if ordinaryAgentToolCacheable(call, tool) {
				t.Fatal("ordinary agent must not cache live process state")
			}
		})
	}
}

func TestRetryableSideEffectCachesMCPErrorResult(t *testing.T) {
	state := session.New(4)
	runID := state.StartWorkflowRun("delivery", "apply change")
	tool := idempotencyTestTool("write")
	mcp := &idempotencyMCP{tools: []schema.Tool{tool}, err: errors.New("backend failed")}
	execCtx := idempotencyExecutionContext(state, runID, tool, true)
	call := schema.ToolCall{ID: "call-1", Name: tool.Name, Arguments: []byte(`{"path":"a.txt"}`)}

	first, err := NewExecutor(mcp).RunToolCalls(context.Background(), execCtx, []schema.ToolCall{call}, nil, nil)
	if err != nil {
		t.Fatalf("first RunToolCalls: %v", err)
	}
	call.ID = "call-2"
	second, err := NewExecutor(mcp).RunToolCalls(context.Background(), execCtx, []schema.ToolCall{call}, nil, nil)
	if err != nil {
		t.Fatalf("second RunToolCalls: %v", err)
	}
	if mcp.calls != 1 || len(first) != 1 || len(second) != 1 || !first[0].IsError || !second[0].IsError || second[0].CallID != "call-2" {
		t.Fatalf("expected one cached MCP error, calls=%d first=%#v second=%#v", mcp.calls, first, second)
	}
}

func TestExactPatchNoWriteFailureDoesNotPoisonDurableRetry(t *testing.T) {
	state := session.New(4)
	runID := state.StartWorkflowRun("delivery", "apply change")
	tool := idempotencyTestTool("write")
	tool.Name = "file_tools/apply_patch"
	tool.Server = "file_tools"
	mcp := &idempotencyMCP{tools: []schema.Tool{tool}, result: schema.ToolResult{
		IsError: true, Content: `{"relative_path":"a.txt","error":"edit 1 expected 1 occurrence(s) but found 0; no changes were written"}`,
	}}
	execCtx := idempotencyExecutionContext(state, runID, tool, true)
	call := schema.ToolCall{ID: "call-1", Name: tool.Name, Arguments: []byte(`{"path":"a.txt"}`)}
	if _, _, err := executeToolCallWithIdempotency(context.Background(), mcp, execCtx, call, tool); err != nil {
		t.Fatal(err)
	}
	mcp.result = schema.ToolResult{Content: `{"relative_path":"a.txt","new_sha256":"changed"}`}
	call.ID = "call-2"
	result, replayed, err := executeToolCallWithIdempotency(context.Background(), mcp, execCtx, call, tool)
	if err != nil || result.IsError || replayed || mcp.calls != 2 {
		t.Fatalf("known no-write failure must allow fresh execution: result=%#v replayed=%v calls=%d err=%v", result, replayed, mcp.calls, err)
	}
}

func TestApprovedRetryableSideEffectUsesSameDurableOperation(t *testing.T) {
	state := session.New(4)
	runID := state.StartWorkflowRun("delivery", "apply change")
	tool := idempotencyTestTool("write")
	mcp := &idempotencyMCP{tools: []schema.Tool{tool}, result: schema.ToolResult{Content: "changed"}}
	runtimeRef := &Runtime{
		cfg:       &config.Config{},
		mcp:       mcp,
		session:   state,
		approvals: newApprovalStore(),
	}
	execCtx := idempotencyExecutionContext(state, runID, tool, true)
	execCtx.Profile.ToolPolicy = config.ToolPolicyConfirm
	firstCall := schema.ToolCall{ID: "approved-call-1", Name: tool.Name, Arguments: []byte(`{"path":"a.txt"}`)}

	results, err := NewExecutor(mcp).RunToolCalls(context.Background(), execCtx, []schema.ToolCall{firstCall}, runtimeRef, nil)
	if err != nil {
		t.Fatalf("queue approval: %v", err)
	}
	if len(results) != 1 || !results[0].Suspended {
		t.Fatalf("expected suspended approval result, got %#v", results)
	}
	pending, ok := runtimeRef.PendingApproval(firstCall.ID)
	if !ok || !pending.toolOperations.Enabled || pending.toolOperations.RunID != runID || pending.toolOperations.Stage != "implement" {
		t.Fatalf("expected retry operation scope on pending approval, got %#v", pending.toolOperations)
	}
	firstResult, err := runtimeRef.ApproveToolCall(context.Background(), firstCall.ID)
	if err != nil {
		t.Fatalf("first ApproveToolCall: %v", err)
	}

	secondCall := firstCall
	secondCall.ID = "approved-call-2"
	runtimeRef.queueApprovalForAgentRunWithToolOperations(secondCall, tool, "worker", "", pending.toolOperations)
	secondResult, err := runtimeRef.ApproveToolCall(context.Background(), secondCall.ID)
	if err != nil {
		t.Fatalf("second ApproveToolCall: %v", err)
	}
	if mcp.calls != 1 {
		t.Fatalf("expected one approved MCP side effect, got %d", mcp.calls)
	}
	if firstResult.CallID != firstCall.ID || secondResult.CallID != secondCall.ID || secondResult.Content != "changed" {
		t.Fatalf("unexpected approved results: first=%#v second=%#v", firstResult, secondResult)
	}
}

func TestRetryableSideEffectDoesNotRunWhenReservationCannotPersist(t *testing.T) {
	state := session.New(4)
	runID := state.StartWorkflowRun("delivery", "apply change")
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, []byte("block"), 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	state.SetPersistPath(filepath.Join(blocker, "session.json"))
	tool := idempotencyTestTool("write")
	mcp := &idempotencyMCP{tools: []schema.Tool{tool}}
	call := schema.ToolCall{ID: "call-1", Name: tool.Name, Arguments: []byte(`{"path":"a.txt"}`)}

	_, err := NewExecutor(mcp).RunToolCalls(context.Background(), idempotencyExecutionContext(state, runID, tool, true), []schema.ToolCall{call}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "persist workflow tool operation start") {
		t.Fatalf("expected reservation persistence error, got %v", err)
	}
	if mcp.calls != 0 {
		t.Fatalf("MCP must not run before reservation is durable, got %d calls", mcp.calls)
	}
}

func TestRetryableSideEffectBecomesUncertainWhenCompletionCannotPersist(t *testing.T) {
	root := t.TempDir()
	validPath := filepath.Join(root, "session.json")
	state := session.New(4)
	state.SetPersistPath(validPath)
	runID := state.StartWorkflowRun("delivery", "apply change")
	blocker := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(blocker, []byte("block"), 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	tool := idempotencyTestTool("write")
	mcp := &idempotencyMCP{
		tools:  []schema.Tool{tool},
		result: schema.ToolResult{Content: "changed"},
		onCall: func() { state.SetPersistPath(filepath.Join(blocker, "session.json")) },
	}
	call := schema.ToolCall{ID: "call-1", Name: tool.Name, Arguments: []byte(`{"path":"a.txt"}`)}

	_, err := NewExecutor(mcp).RunToolCalls(context.Background(), idempotencyExecutionContext(state, runID, tool, true), []schema.ToolCall{call}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "result is not durable") {
		t.Fatalf("expected completion persistence error, got %v", err)
	}
	if mcp.calls != 1 {
		t.Fatalf("expected exactly one MCP side effect, got %d", mcp.calls)
	}
	run, _ := state.WorkflowRun(runID)
	if len(run.ToolOperations) != 1 || run.ToolOperations[0].Status != session.WorkflowToolOperationStarted {
		t.Fatalf("expected uncertain started operation after persistence failure, got %#v", run.ToolOperations)
	}
	mcp.onCall = nil
	_, err = NewExecutor(mcp).RunToolCalls(context.Background(), idempotencyExecutionContext(state, runID, tool, true), []schema.ToolCall{call}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "outcome is uncertain") || mcp.calls != 1 {
		t.Fatalf("expected retry to remain blocked, calls=%d err=%v", mcp.calls, err)
	}
}

func TestReusableValidationRunsOnceAcrossStagesWhenWorkspaceIsUnchanged(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	state := session.New(4)
	runID := state.StartWorkflowRun("delivery", "verify project")
	tool := reusableValidationTestTool()
	mcp := &idempotencyMCP{tools: []schema.Tool{tool}, result: successfulValidationResult(t, root)}
	call := reusableValidationCall("validation-1")
	firstContext := reusableValidationExecutionContext(state, runID, root, "test", tool)
	if _, err := NewExecutor(mcp).RunToolCalls(context.Background(), firstContext, []schema.ToolCall{call}, nil, nil); err != nil {
		t.Fatalf("first validation: %v", err)
	}
	call.ID = "validation-2"
	secondContext := reusableValidationExecutionContext(state, runID, root, "quality-gate", tool)
	results, err := NewExecutor(mcp).RunToolCalls(context.Background(), secondContext, []schema.ToolCall{call}, nil, nil)
	if err != nil {
		t.Fatalf("second validation: %v", err)
	}
	if mcp.calls != 1 || len(results) != 1 || results[0].CallID != "validation-2" {
		t.Fatalf("expected one execution and a current-id replay, calls=%d results=%#v", mcp.calls, results)
	}
	run, _ := state.WorkflowRun(runID)
	if len(run.ToolOperations) != 1 || !run.ToolOperations[0].Reusable || run.ToolOperations[0].WorkspaceHash == "" || run.ToolOperations[0].Stage != "test" {
		t.Fatalf("unexpected reusable validation ledger: %#v", run.ToolOperations)
	}
}

func TestReadOnlyFileCallReplaysWhenWorkspaceIsUnchanged(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	state := session.New(4)
	runID := state.StartWorkflowRun("delivery", "inspect project")
	tool := schema.Tool{
		Name:        "file_tools/read_file",
		Kind:        string(config.ToolKindRead),
		InputSchema: []byte(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`),
	}
	mcp := &idempotencyMCP{tools: []schema.Tool{tool}, result: schema.ToolResult{Content: `{"path":"main.go","content":"package main\n"}`}}
	first := schema.ToolCall{ID: "read-1", Name: tool.Name, Arguments: []byte(`{"path":"main.go"}`)}
	second := schema.ToolCall{ID: "read-2", Name: tool.Name, Arguments: []byte(`{"path":"main.go"}`)}
	execCtx := idempotencyExecutionContext(state, runID, tool, true)
	execCtx.WorkspaceRoot = root
	for _, call := range []schema.ToolCall{first, second} {
		if _, err := NewExecutor(mcp).RunToolCalls(context.Background(), execCtx, []schema.ToolCall{call}, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	if mcp.calls != 1 {
		t.Fatalf("expected one read and one durable replay, got %d calls", mcp.calls)
	}
	run, _ := state.WorkflowRun(runID)
	if len(run.ToolOperations) != 1 || !run.ToolOperations[0].Reusable {
		t.Fatalf("expected reusable read operation, got %#v", run.ToolOperations)
	}
}

func TestValidationCommandReplaysWithoutModelReuseFlag(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	state := session.New(4)
	runID := state.StartWorkflowRun("delivery", "verify project")
	tool := reusableValidationTestTool()
	mcp := &idempotencyMCP{tools: []schema.Tool{tool}, result: successfulValidationResult(t, root)}
	call := schema.ToolCall{ID: "validation-1", Name: tool.Name, Arguments: []byte(`{"program":"go","args":["test","./..."]}`)}
	if _, err := NewExecutor(mcp).RunToolCalls(context.Background(), reusableValidationExecutionContext(state, runID, root, "test", tool), []schema.ToolCall{call}, nil, nil); err != nil {
		t.Fatal(err)
	}
	call.ID = "validation-2"
	if _, err := NewExecutor(mcp).RunToolCalls(context.Background(), reusableValidationExecutionContext(state, runID, root, "quality-gate", tool), []schema.ToolCall{call}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if mcp.calls != 1 {
		t.Fatalf("expected unchanged validation to replay, got %d calls", mcp.calls)
	}
}

func TestReusableValidationRerunsAfterWorkspaceContentChanges(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	state := session.New(4)
	runID := state.StartWorkflowRun("delivery", "verify project")
	tool := reusableValidationTestTool()
	mcp := &idempotencyMCP{tools: []schema.Tool{tool}, result: successfulValidationResult(t, root)}
	call := reusableValidationCall("validation-1")
	if _, err := NewExecutor(mcp).RunToolCalls(context.Background(), reusableValidationExecutionContext(state, runID, root, "test", tool), []schema.ToolCall{call}, nil, nil); err != nil {
		t.Fatalf("first validation: %v", err)
	}
	if err := os.WriteFile(path, []byte("package main\n\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mcp.result = successfulValidationResult(t, root)
	call.ID = "validation-2"
	if _, err := NewExecutor(mcp).RunToolCalls(context.Background(), reusableValidationExecutionContext(state, runID, root, "quality-gate", tool), []schema.ToolCall{call}, nil, nil); err != nil {
		t.Fatalf("second validation: %v", err)
	}
	if mcp.calls != 2 {
		t.Fatalf("workspace change must force re-execution, got %d calls", mcp.calls)
	}
	run, _ := state.WorkflowRun(runID)
	if len(run.ToolOperations) != 2 || run.ToolOperations[0].WorkspaceHash == run.ToolOperations[1].WorkspaceHash {
		t.Fatalf("expected two workspace-bound validation records: %#v", run.ToolOperations)
	}
}

func TestReusableValidationFailureIsNeverReplayed(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	state := session.New(4)
	runID := state.StartWorkflowRun("delivery", "verify project")
	tool := reusableValidationTestTool()
	hash := workspaceHashForTest(t, root)
	failureContent, _ := json.Marshal(map[string]any{
		"exit_code":                         1,
		"workspace_fingerprint_before_hash": hash,
		"workspace_fingerprint_after_hash":  hash,
	})
	mcp := &idempotencyMCP{tools: []schema.Tool{tool}, result: schema.ToolResult{Content: string(failureContent)}}
	call := reusableValidationCall("validation-1")
	firstContext := reusableValidationExecutionContext(state, runID, root, "test", tool)
	if _, err := NewExecutor(mcp).RunToolCalls(context.Background(), firstContext, []schema.ToolCall{call}, nil, nil); err != nil {
		t.Fatalf("first failed validation: %v", err)
	}
	run, _ := state.WorkflowRun(runID)
	if len(run.ToolOperations) != 1 || run.ToolOperations[0].Status != session.WorkflowToolOperationFailed {
		t.Fatalf("expected durable failed validation, got %#v", run.ToolOperations)
	}
	call.ID = "validation-2"
	secondContext := reusableValidationExecutionContext(state, runID, root, "quality-gate", tool)
	if _, err := NewExecutor(mcp).RunToolCalls(context.Background(), secondContext, []schema.ToolCall{call}, nil, nil); err != nil {
		t.Fatalf("second failed validation: %v", err)
	}
	if mcp.calls != 2 {
		t.Fatalf("failed validation must execute again, got %d calls", mcp.calls)
	}
	run, _ = state.WorkflowRun(runID)
	if len(run.ToolOperations) != 1 || run.ToolOperations[0].Status != session.WorkflowToolOperationFailed || run.ToolOperations[0].Stage != "quality-gate" {
		t.Fatalf("expected failed record to be retried in the current stage, got %#v", run.ToolOperations)
	}
}

func TestApprovalRequiredCommandIgnoresValidationReuseRequest(t *testing.T) {
	root := t.TempDir()
	state := session.New(4)
	runID := state.StartWorkflowRun("delivery", "format project")
	tool := reusableValidationTestTool()
	call := schema.ToolCall{ID: "format-1", Name: "run_command", Arguments: []byte(`{"program":"gofmt","args":["-w","."],"reuse_if_unchanged":true}`)}
	first, err := workflowToolOperationForCall(reusableValidationExecutionContext(state, runID, root, "implement", tool), call, tool)
	if err != nil {
		t.Fatal(err)
	}
	second, err := workflowToolOperationForCall(reusableValidationExecutionContext(state, runID, root, "quality-gate", tool), call, tool)
	if err != nil {
		t.Fatal(err)
	}
	if first.Reusable || second.Reusable || first.WorkspaceHash != "" || first.Key == second.Key {
		t.Fatalf("mutating formatter must remain stage-bound: first=%#v second=%#v", first, second)
	}
}

func idempotencyTestTool(kind string) schema.Tool {
	return schema.Tool{
		Name:        "file_tools/apply_patch",
		Kind:        kind,
		InputSchema: []byte(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path"],"additionalProperties":false}`),
	}
}

func idempotencyExecutionContext(state *session.State, runID string, tool schema.Tool, enabled bool) ExecutionContext {
	execCtx := newExecutionContext("worker", config.AgentProfile{
		AllowedToolKinds: []config.ToolKind{config.ToolKindRead, config.ToolKindWrite},
		ToolPolicy:       config.ToolPolicyAllow,
	}, []schema.Tool{tool}, nil, "")
	execCtx.Session = state
	execCtx.WorkflowRunID = runID
	execCtx.WorkflowName = "delivery"
	execCtx.WorkflowStage = "implement"
	execCtx.IdempotentToolCalls = enabled
	return execCtx
}

func reusableValidationTestTool() schema.Tool {
	return schema.Tool{
		Name:        "run_command",
		Server:      "command_tools",
		Kind:        "exec",
		InputSchema: []byte(`{"type":"object","properties":{"program":{"type":"string"},"args":{"type":"array","items":{"type":"string"}},"reuse_if_unchanged":{"type":"boolean"}},"required":["program"],"additionalProperties":false}`),
	}
}

func reusableValidationCall(id string) schema.ToolCall {
	return schema.ToolCall{ID: id, Name: "run_command", Arguments: []byte(`{"program":"go","args":["test","./..."],"reuse_if_unchanged":true}`)}
}

func reusableValidationExecutionContext(state *session.State, runID, root, stage string, tool schema.Tool) ExecutionContext {
	execCtx := newExecutionContext("worker", config.AgentProfile{
		AllowedToolKinds: []config.ToolKind{config.ToolKindExec},
		ToolPolicy:       config.ToolPolicyAllow,
	}, []schema.Tool{tool}, nil, root)
	execCtx.Session = state
	execCtx.WorkflowRunID = runID
	execCtx.WorkflowName = "delivery"
	execCtx.WorkflowStage = stage
	execCtx.IdempotentToolCalls = true
	return execCtx
}

func workspaceHashForTest(t *testing.T, root string) string {
	t.Helper()
	snapshot, err := executionfingerprint.Workspace(root)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot.Hash
}

func successfulValidationResult(t *testing.T, root string) schema.ToolResult {
	t.Helper()
	hash := workspaceHashForTest(t, root)
	content, err := json.Marshal(map[string]any{
		"exit_code":                         0,
		"workspace_fingerprint_before_hash": hash,
		"workspace_fingerprint_after_hash":  hash,
	})
	if err != nil {
		t.Fatal(err)
	}
	return schema.ToolResult{Content: string(content)}
}
