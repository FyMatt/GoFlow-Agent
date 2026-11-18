package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestAgentTaskApprovalScopePersistsAndFailsClosedAcrossBoundaries(t *testing.T) {
	workspace := t.TempDir()
	state := session.New(8)
	sessionPath := filepath.Join(t.TempDir(), "session.json")
	state.SetPersistPath(sessionPath)
	runID := state.StartAgentRunWithOptions("update demo", session.AgentRunStartOptions{AgentID: "software-engineer", TargetID: session.LocalTargetID})
	mcp := &stubApprovalMCP{result: schema.ToolResult{Content: "ok"}}
	runtimeRef := &Runtime{
		cfg: &config.Config{
			WorkspaceRoot: workspace,
			ToolRiskPolicy: config.ToolRiskPolicyConfig{
				RequireApprovalForUnsandboxedRiskyTools: true,
			},
			MCP: []config.MCPServerRef{{Name: "file_tools", Isolation: "process_group"}},
		},
		mcp:       mcp,
		session:   state,
		approvals: newApprovalStore(),
	}
	call := schema.ToolCall{ID: "call-write", Name: "write_file", Arguments: json.RawMessage(`{"path":"demo.txt","content":"ok"}`)}
	tool := schema.Tool{Name: "write_file", Server: "file_tools", Kind: string(config.ToolKindWrite)}
	runtimeRef.approvals.Add(pendingApproval{call: call, tool: tool, agent: "software-engineer", agentRunID: runID})

	if available, reason := runtimeRef.CanApproveAgentTaskScope(call.ID); !available || reason == "" {
		t.Fatalf("expected bounded task approval to be available, available=%t reason=%q", available, reason)
	}
	result, err := runtimeRef.ApproveToolCallForAgentTask(context.Background(), call.ID)
	if err != nil {
		t.Fatalf("ApproveToolCallForAgentTask: %v", err)
	}
	if result.IsError || mcp.calls != 1 {
		t.Fatalf("expected current call to execute once, result=%#v calls=%d", result, mcp.calls)
	}
	run, ok := state.AgentRun(runID)
	if !ok || run.TaskApproval == nil || !run.TaskApproval.AllowWorkspaceWrites || !run.TaskApproval.AllowValidationCommands {
		t.Fatalf("expected durable task approval on active run, got %#v ok=%t", run, ok)
	}

	assertApplied := func(name string, mutate func(*ExecutionContext), want bool) {
		t.Helper()
		execCtx := newExecutionContext("software-engineer", config.AgentProfile{}, []schema.Tool{tool}, nil, workspace)
		execCtx.AgentRunID = runID
		execCtx.TargetID = session.LocalTargetID
		if mutate != nil {
			mutate(&execCtx)
		}
		runtimeRef.applyAgentTaskApprovalScope(&execCtx)
		if execCtx.ApprovedTaskScope != want {
			t.Fatalf("%s: ApprovedTaskScope=%t want %t", name, execCtx.ApprovedTaskScope, want)
		}
	}
	assertApplied("matching run", nil, true)
	assertApplied("different agent", func(ctx *ExecutionContext) { ctx.AgentID = "chat" }, false)
	assertApplied("different workspace", func(ctx *ExecutionContext) { ctx.WorkspaceRoot = t.TempDir() }, false)
	assertApplied("remote target", func(ctx *ExecutionContext) { ctx.TargetID = "remote-1" }, false)
	assertApplied("workflow context", func(ctx *ExecutionContext) { ctx.WorkflowName = "workflow" }, false)

	loaded := session.New(8)
	loaded.SetPersistPath(sessionPath)
	if err := loaded.Load(sessionPath); err != nil {
		t.Fatalf("Load persisted task approval: %v", err)
	}
	restored := &Runtime{cfg: runtimeRef.cfg, session: loaded}
	restoredCtx := newExecutionContext("software-engineer", config.AgentProfile{}, []schema.Tool{tool}, nil, workspace)
	restoredCtx.AgentRunID = runID
	restored.applyAgentTaskApprovalScope(&restoredCtx)
	if !restoredCtx.ApprovedTaskScope {
		t.Fatal("expected matching active task approval to restore after restart")
	}

	loaded.CompleteAgentRun(runID, "completed", schema.AgentResult{Output: "done", AgentID: "software-engineer"})
	terminalCtx := newExecutionContext("software-engineer", config.AgentProfile{}, []schema.Tool{tool}, nil, workspace)
	terminalCtx.AgentRunID = runID
	restored.applyAgentTaskApprovalScope(&terminalCtx)
	if terminalCtx.ApprovedTaskScope {
		t.Fatal("terminal task approval must be inactive")
	}
}

func TestAgentTaskApprovalScopeRejectsRemoteAndDestructiveGrantCalls(t *testing.T) {
	state := session.New(8)
	remoteRunID := state.StartAgentRunWithOptions("remote task", session.AgentRunStartOptions{AgentID: "software-engineer", TargetID: "remote-1"})
	runtimeRef := &Runtime{cfg: &config.Config{WorkspaceRoot: t.TempDir()}, session: state, approvals: newApprovalStore()}
	writeTool := schema.Tool{Name: "write_file", Server: "file_tools", Kind: string(config.ToolKindWrite)}
	remoteCall := schema.ToolCall{ID: "remote-write", Name: "write_file", Arguments: json.RawMessage(`{"path":"demo.txt","content":"ok"}`)}
	runtimeRef.approvals.Add(pendingApproval{call: remoteCall, tool: writeTool, agent: "software-engineer", agentRunID: remoteRunID})
	if available, _ := runtimeRef.CanApproveAgentTaskScope(remoteCall.ID); available {
		t.Fatal("remote target must not be eligible for local task scope")
	}

	localRunID := state.StartAgentRunWithOptions("delete task", session.AgentRunStartOptions{AgentID: "software-engineer", TargetID: session.LocalTargetID})
	deleteCall := schema.ToolCall{ID: "delete", Name: "delete_file", Arguments: json.RawMessage(`{"path":"demo.txt"}`)}
	deleteTool := schema.Tool{Name: "delete_file", Server: "file_tools", Kind: string(config.ToolKindWrite)}
	runtimeRef.approvals.Add(pendingApproval{call: deleteCall, tool: deleteTool, agent: "software-engineer", agentRunID: localRunID})
	if available, _ := runtimeRef.CanApproveAgentTaskScope(deleteCall.ID); available {
		t.Fatal("destructive file operation must not establish task scope")
	}
}

func TestAgentTaskApprovalScopeRestoresPendingToolMetadata(t *testing.T) {
	state := session.New(8)
	runID := state.StartAgentRunWithOptions("update demo", session.AgentRunStartOptions{AgentID: "software-engineer", TargetID: session.LocalTargetID})
	state.AppendAgentRunPendingApproval(runID, session.PendingApprovalSnapshot{
		CallID:     "call-write",
		ToolName:   "write_file",
		ToolServer: "file_tools",
		ToolKind:   string(config.ToolKindWrite),
		AgentID:    "software-engineer",
		Arguments:  `{"path":"demo.txt","content":"ok"}`,
		AgentRunID: runID,
	})
	runtimeRef := &Runtime{
		cfg:       &config.Config{WorkspaceRoot: t.TempDir()},
		session:   state,
		approvals: newApprovalStore(),
	}

	runtimeRef.RestoreAgentRunApproval(runID)
	available, reason := runtimeRef.CanApproveAgentTaskScope("call-write")
	if !available {
		t.Fatalf("expected restored file tool to retain task-scope eligibility, reason=%q", reason)
	}
	pending, ok := runtimeRef.PendingApproval("call-write")
	if !ok || pending.tool.Server != "file_tools" || pending.tool.Kind != string(config.ToolKindWrite) {
		t.Fatalf("expected restored tool metadata, got %#v", pending.tool)
	}
}

func TestAgentRunTerminalStatePersistsImmediately(t *testing.T) {
	sessionPath := filepath.Join(t.TempDir(), "session.json")
	state := session.New(8)
	state.SetPersistPath(sessionPath)
	runtimeRef := &Runtime{session: state}
	runID, err := runtimeRef.StartAgentRunWithOptionsDurable("finish durably", session.AgentRunStartOptions{AgentID: "software-engineer"})
	if err != nil {
		t.Fatalf("StartAgentRunWithOptionsDurable: %v", err)
	}
	completed, ok := runtimeRef.CompleteAgentRun(runID, "completed", schema.AgentResult{
		Output:  "verified result",
		AgentID: "software-engineer",
		Mode:    "fix",
	})
	if !ok || completed.Status != "completed" {
		t.Fatalf("expected completed run, got %#v", completed)
	}

	restored := session.New(8)
	if err := restored.Load(sessionPath); err != nil {
		t.Fatalf("Load: %v", err)
	}
	persisted, ok := restored.AgentRun(runID)
	if !ok || persisted.Status != "completed" || persisted.Output != "verified result" || persisted.CompletedAt == "" {
		t.Fatalf("expected terminal state on disk, got %#v", persisted)
	}
}
