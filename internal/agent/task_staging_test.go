package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/internal/taskstaging"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestRuntimeTaskStagingLifecycle(t *testing.T) {
	workspace := t.TempDir()
	dataRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "demo.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	state := session.New(4)
	runID := state.StartAgentRun("update demo")
	runtimeRef := &Runtime{
		cfg:     &config.Config{WorkspaceRoot: workspace, RuntimeHome: dataRoot, DataRoot: dataRoot},
		session: state,
	}
	t.Cleanup(func() { _ = runtimeRef.ReleaseTaskStaging(runID) })
	task, err := runtimeRef.prepareTaskStaging(runID)
	if err != nil {
		t.Fatalf("prepareTaskStaging: %v", err)
	}
	if !strings.HasPrefix(filepath.Clean(task.Root), filepath.Join(filepath.Clean(dataRoot), "task-staging")) {
		t.Fatalf("unexpected task staging root: %s", task.Root)
	}
	if err := os.WriteFile(filepath.Join(task.Workspace, "demo.txt"), []byte("after\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest, result, err := runtimeRef.applyTaskStaging(runID)
	if err != nil {
		t.Fatalf("applyTaskStaging: %v", err)
	}
	if len(manifest.Changes) != 1 || manifest.Changes[0].Path != "demo.txt" {
		t.Fatalf("unexpected task manifest: %#v", manifest)
	}
	data, err := os.ReadFile(filepath.Join(workspace, "demo.txt"))
	if err != nil || string(data) != "after\n" {
		t.Fatalf("unexpected applied workspace file: %q err=%v", data, err)
	}
	if err := taskstaging.Restore(result.RecoveryPath); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	data, err = os.ReadFile(filepath.Join(workspace, "demo.txt"))
	if err != nil || string(data) != "before\n" {
		t.Fatalf("unexpected restored workspace file: %q err=%v", data, err)
	}
}

func TestRuntimeTaskStagingMovesOutsideWorkspaceDataRoot(t *testing.T) {
	workspace := t.TempDir()
	state := session.New(4)
	runID := state.StartWorkflowRun("test", "stage safely")
	runtimeRef := &Runtime{
		cfg: &config.Config{
			WorkspaceRoot: workspace,
			RuntimeHome:   workspace,
			DataRoot:      filepath.Join(workspace, ".goflow-data"),
		},
		session: state,
	}
	t.Cleanup(func() { _ = runtimeRef.ReleaseTaskStaging(runID) })
	task, err := runtimeRef.prepareTaskStaging(runID)
	if err != nil {
		t.Fatalf("prepareTaskStaging: %v", err)
	}
	if pathWithin(workspace, task.Root) {
		t.Fatalf("task staging must not recursively live inside the workspace: %s", task.Root)
	}
}

func TestRuntimeTaskStagingRejectsUnknownRun(t *testing.T) {
	runtimeRef := &Runtime{cfg: &config.Config{WorkspaceRoot: t.TempDir(), DataRoot: t.TempDir()}, session: session.New(4)}
	if _, err := runtimeRef.prepareTaskStaging("missing-run"); err == nil || !strings.Contains(err.Error(), "run not found") {
		t.Fatalf("expected unknown run rejection, got %v", err)
	}
}

func TestExecutorRoutesWorkspaceToolsToTaskStaging(t *testing.T) {
	workspace := t.TempDir()
	dataRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "demo.txt"), []byte("real\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	state := session.New(4)
	runID := state.StartAgentRun("update demo")
	mcp := &stubRuntimeMCP{result: schema.ToolResult{Content: "ok"}}
	runtimeRef := &Runtime{
		cfg:                &config.Config{WorkspaceRoot: workspace, RuntimeHome: dataRoot, DataRoot: dataRoot},
		session:            state,
		mcp:                mcp,
		approvals:          newApprovalStore(),
		taskStagingEnabled: true,
	}
	t.Cleanup(func() { _ = runtimeRef.ReleaseTaskStaging(runID) })
	writeTool := schema.Tool{
		Name: "write_file", Server: "file_tools", Kind: string(config.ToolKindWrite),
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`),
	}
	readTool := schema.Tool{
		Name: "read_file", Server: "file_tools", Kind: string(config.ToolKindRead),
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`),
	}
	execCtx := newExecutionContext("fixer", config.AgentProfile{ToolPolicy: config.ToolPolicyAllow, AllowedToolKinds: []config.ToolKind{config.ToolKindRead, config.ToolKindWrite}}, []schema.Tool{writeTool, readTool}, nil, workspace)
	execCtx.AgentRunID = runID
	execCtx.Session = state
	results, err := NewExecutor(mcp).RunToolCalls(context.Background(), execCtx, []schema.ToolCall{
		{ID: "write", Name: "file_tools/write_file", Arguments: json.RawMessage(`{"path":"demo.txt","content":"staged\n"}`)},
		{ID: "read", Name: "file_tools/read_file", Arguments: json.RawMessage(`{"path":"demo.txt"}`)},
	}, runtimeRef, nil)
	if err != nil {
		t.Fatalf("RunToolCalls: %v", err)
	}
	if len(results) != 2 || mcp.calls != 2 {
		t.Fatalf("unexpected scoped execution results: calls=%d results=%#v", mcp.calls, results)
	}
	task := runtimeRef.taskStaging[runID]
	if task == nil || filepath.Clean(mcp.workspace) != filepath.Clean(task.Workspace) {
		t.Fatalf("workspace tools were not routed to task staging: task=%#v workspace=%q", task, mcp.workspace)
	}
	if filepath.Clean(mcp.taskScope.Root) != filepath.Clean(workspace) || mcp.taskScope.Input != "" || mcp.taskScope.Dependencies != "" || mcp.taskScope.RunID != runID {
		t.Fatalf("task workspace scope was not propagated: task=%#v scope=%#v", task, mcp.taskScope)
	}
	data, err := os.ReadFile(filepath.Join(workspace, "demo.txt"))
	if err != nil || string(data) != "real\n" {
		t.Fatalf("stub tool must not mutate the live project while scope routing is tested: %q err=%v", data, err)
	}
}

func TestAgentReadRestoresExistingTaskStagingAfterRuntimeRestart(t *testing.T) {
	workspace := t.TempDir()
	dataRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "demo.txt"), []byte("live\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	state := session.New(4)
	runID := state.StartAgentRun("continue staged work")
	cfg := &config.Config{WorkspaceRoot: workspace, RuntimeHome: dataRoot, DataRoot: dataRoot}
	beforeRestart := &Runtime{cfg: cfg, session: state, taskStagingEnabled: true}
	task, err := beforeRestart.prepareTaskStaging(runID)
	if err != nil {
		t.Fatalf("prepareTaskStaging: %v", err)
	}
	if err := os.WriteFile(filepath.Join(task.Workspace, "demo.txt"), []byte("staged\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mcp := &stubRuntimeMCP{result: schema.ToolResult{Content: "ok"}}
	afterRestart := &Runtime{
		cfg:                cfg,
		session:            state,
		mcp:                mcp,
		approvals:          newApprovalStore(),
		taskStagingEnabled: true,
	}
	t.Cleanup(func() { _ = afterRestart.ReleaseTaskStaging(runID) })
	readTool := schema.Tool{
		Name: "read_file", Server: "file_tools", Kind: string(config.ToolKindRead),
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`),
	}
	execCtx := newExecutionContext("fixer", config.AgentProfile{ToolPolicy: config.ToolPolicyAllow, AllowedToolKinds: []config.ToolKind{config.ToolKindRead}}, []schema.Tool{readTool}, nil, workspace)
	execCtx.AgentRunID = runID
	execCtx.Session = state
	results, err := NewExecutor(mcp).RunToolCalls(context.Background(), execCtx, []schema.ToolCall{{
		ID: "read", Name: "file_tools/read_file", Arguments: json.RawMessage(`{"path":"demo.txt"}`),
	}}, afterRestart, nil)
	if err != nil || len(results) != 1 || results[0].IsError {
		t.Fatalf("continued read failed: results=%#v err=%v", results, err)
	}
	if filepath.Clean(mcp.workspace) != filepath.Clean(task.Workspace) {
		t.Fatalf("continued Agent read used %q instead of staged workspace %q", mcp.workspace, task.Workspace)
	}
}

func TestExecutorRefusesTaskWorkspaceFallback(t *testing.T) {
	workspace := t.TempDir()
	dataRoot := t.TempDir()
	state := session.New(4)
	runID := state.StartAgentRun("update demo")
	mcp := &stubApprovalMCP{result: schema.ToolResult{Content: "should not run"}}
	runtimeRef := &Runtime{
		cfg:                &config.Config{WorkspaceRoot: workspace, RuntimeHome: dataRoot, DataRoot: dataRoot},
		session:            state,
		mcp:                mcp,
		approvals:          newApprovalStore(),
		taskStagingEnabled: true,
	}
	t.Cleanup(func() { _ = runtimeRef.ReleaseTaskStaging(runID) })
	tool := schema.Tool{
		Name: "write_file", Server: "file_tools", Kind: string(config.ToolKindWrite),
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`),
	}
	execCtx := newExecutionContext("fixer", config.AgentProfile{ToolPolicy: config.ToolPolicyAllow, AllowedToolKinds: []config.ToolKind{config.ToolKindWrite}}, []schema.Tool{tool}, nil, workspace)
	execCtx.AgentRunID = runID
	execCtx.Session = state
	results, err := NewExecutor(mcp).RunToolCalls(context.Background(), execCtx, []schema.ToolCall{{
		ID: "write", Name: "file_tools/write_file", Arguments: json.RawMessage(`{"path":"demo.txt","content":"staged\n"}`),
	}}, runtimeRef, nil)
	if err != nil {
		t.Fatalf("RunToolCalls: %v", err)
	}
	if mcp.calls != 0 || len(results) != 1 || !results[0].IsError || !strings.Contains(results[0].Content, "refusing to fall back") {
		t.Fatalf("expected closed workspace-scope failure, calls=%d results=%#v", mcp.calls, results)
	}
}

func TestWorkflowReadToolsPrepareTaskStagingBeforeCommands(t *testing.T) {
	workspace := t.TempDir()
	dataRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "demo.txt"), []byte("real\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	state := session.New(4)
	runID := state.StartWorkflowRun("delivery", "implement from first read")
	mcp := &stubRuntimeMCP{result: schema.ToolResult{Content: "ok"}}
	runtimeRef := &Runtime{
		cfg:                &config.Config{WorkspaceRoot: workspace, RuntimeHome: dataRoot, DataRoot: dataRoot},
		session:            state,
		mcp:                mcp,
		approvals:          newApprovalStore(),
		taskStagingEnabled: true,
	}
	t.Cleanup(func() { _ = runtimeRef.ReleaseTaskStaging(runID) })
	readTool := schema.Tool{
		Name: "read_file", Server: "file_tools", Kind: string(config.ToolKindRead),
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`),
	}
	execCtx := newExecutionContext("fixer", config.AgentProfile{ToolPolicy: config.ToolPolicyAllow, AllowedToolKinds: []config.ToolKind{config.ToolKindRead, config.ToolKindExec}}, []schema.Tool{readTool}, nil, workspace)
	execCtx.WorkflowRunID = runID
	execCtx.Session = state
	results, err := NewExecutor(mcp).RunToolCalls(context.Background(), execCtx, []schema.ToolCall{
		{ID: "read", Name: "file_tools/read_file", Arguments: json.RawMessage(`{"path":"demo.txt"}`)},
	}, runtimeRef, nil)
	if err != nil {
		t.Fatalf("RunToolCalls: %v", err)
	}
	if len(results) != 1 || results[0].IsError {
		t.Fatalf("unexpected workflow read result: %#v", results)
	}
	task := runtimeRef.taskStaging[runID]
	if task == nil || filepath.Clean(mcp.workspace) != filepath.Clean(task.Workspace) {
		t.Fatalf("workflow read must bind file tools to task staging before commands, task=%#v workspace=%q", task, mcp.workspace)
	}
}

func TestExecutorRoutesThirdPartyReadToolToTaskStaging(t *testing.T) {
	workspace := t.TempDir()
	dataRoot := t.TempDir()
	state := session.New(4)
	runID := state.StartAgentRun("read through third-party MCP")
	mcp := &stubRuntimeMCP{result: schema.ToolResult{Content: "ok"}}
	runtimeRef := &Runtime{
		cfg:                &config.Config{WorkspaceRoot: workspace, RuntimeHome: dataRoot, DataRoot: dataRoot},
		session:            state,
		mcp:                mcp,
		approvals:          newApprovalStore(),
		taskStagingEnabled: true,
	}
	tool := schema.Tool{
		Name: "inspect", Server: "third_party_helper", Kind: string(config.ToolKindRead),
		InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	}
	execCtx := newExecutionContext("chat", config.AgentProfile{ToolPolicy: config.ToolPolicyAllow, AllowedToolKinds: []config.ToolKind{config.ToolKindRead}}, []schema.Tool{tool}, nil, workspace)
	execCtx.AgentRunID = runID
	execCtx.Session = state
	results, err := NewExecutor(mcp).RunToolCalls(context.Background(), execCtx, []schema.ToolCall{{
		ID: "inspect", Name: "third_party_helper/inspect", Arguments: json.RawMessage(`{}`),
	}}, runtimeRef, nil)
	if err != nil {
		t.Fatalf("RunToolCalls: %v", err)
	}
	if len(results) != 1 || results[0].IsError {
		t.Fatalf("unexpected third-party scoped result: %#v", results)
	}
	task := runtimeRef.taskStaging[runID]
	if task == nil || filepath.Clean(mcp.taskScope.Root) != filepath.Clean(workspace) || filepath.Clean(mcp.taskScope.Workspace) != filepath.Clean(task.Workspace) || mcp.taskScope.Input != "" {
		t.Fatalf("third-party read tool bypassed task staging: task=%#v scope=%#v", task, mcp.taskScope)
	}
}

func TestWorkspaceScopedExecutionToolKeepsStructuredRemoteToolsOutOfTaskCopy(t *testing.T) {
	for _, server := range []string{"host_tools", "network_tools", "web_tools", "ssh_tools", "notification_tools"} {
		tool := schema.Tool{Name: "inspect", Server: server}
		if workspaceScopedExecutionTool(schema.ToolCall{Name: server + "/inspect"}, tool) {
			t.Fatalf("structured remote tool %s should use its capability boundary", server)
		}
		if thirdPartyExecutionTool(schema.ToolCall{Name: server + "/inspect"}, tool) {
			t.Fatalf("bundled connector %s must retain its own capability boundary", server)
		}
	}
	tool := schema.Tool{Name: "inspect", Server: "third_party_helper"}
	if !workspaceScopedExecutionTool(schema.ToolCall{Name: "third_party_helper/inspect"}, tool) {
		t.Fatal("third-party MCP tool should require task staging")
	}
}

func TestTerminalWorkflowReleasesUnchangedTaskWorkspace(t *testing.T) {
	workspace := t.TempDir()
	dataRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("unchanged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	state := session.New(4)
	runID := state.StartWorkflowRun("skill-chain", "inspect only")
	runtimeRef := &Runtime{
		cfg:                &config.Config{WorkspaceRoot: workspace, RuntimeHome: dataRoot, DataRoot: dataRoot},
		session:            state,
		approvals:          newApprovalStore(),
		taskStagingEnabled: true,
	}
	task, err := runtimeRef.prepareTaskStaging(runID)
	if err != nil {
		t.Fatal(err)
	}
	runtimeRef.releaseUnchangedTaskStaging(runID, "completed")
	if _, err := os.Stat(task.Workspace); err != nil {
		t.Fatalf("live workspace must remain available, err=%v", err)
	}
	if _, err := os.Stat(task.Input); !os.IsNotExist(err) {
		t.Fatalf("unchanged recovery snapshot should be removed, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(task.Metadata, "baseline.json")); err != nil {
		t.Fatalf("bounded audit metadata must remain: %v", err)
	}
}

func TestTaskStagingManifestRefreshesUnappliedWorkspace(t *testing.T) {
	workspace := t.TempDir()
	dataRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "one.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	state := session.New(4)
	runID := state.StartWorkflowRun("skill-chain", "edit files")
	runtimeRef := &Runtime{
		cfg:                &config.Config{WorkspaceRoot: workspace, RuntimeHome: dataRoot, DataRoot: dataRoot},
		session:            state,
		approvals:          newApprovalStore(),
		taskStagingEnabled: true,
	}
	task, err := runtimeRef.prepareTaskStaging(runID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtimeRef.ReleaseTaskStaging(runID) })
	if err := os.WriteFile(filepath.Join(task.Workspace, "one.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := task.BuildManifest(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(task.Workspace, "two.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest, _, exists, err := runtimeRef.TaskStagingManifest(runID)
	if err != nil || !exists || len(manifest.Changes) != 2 {
		t.Fatalf("unapplied manifest must refresh from the current task workspace: exists=%v err=%v changes=%#v", exists, err, manifest.Changes)
	}
}
