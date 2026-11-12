package agent

import (
	"encoding/json"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestRunCommandApprovalIsBoundToCommandRisk(t *testing.T) {
	tool := schema.Tool{Name: "run_command", Server: "command_tools", Kind: string(config.ToolKindExec)}
	execCtx := newExecutionContext("fixer", config.AgentProfile{ToolPolicy: config.ToolPolicyConfirm}, []schema.Tool{tool}, nil, t.TempDir())

	inspection := commandToolCall(t, "git", []string{"status", "--short"})
	rememberExecutionApproval(t, &execCtx, inspection, tool)
	if execCtx.requiresApproval(inspection, tool) {
		t.Fatal("remembered safe inspection should not require another approval")
	}
	changedInspection := commandToolCall(t, "git", []string{"diff", "--stat"})
	if !execCtx.requiresApproval(changedInspection, tool) {
		t.Fatal("remembered git status must not authorize another safe command")
	}

	destructive := commandToolCall(t, "git", []string{"reset", "--hard"})
	if !execCtx.requiresApproval(destructive, tool) {
		t.Fatal("destructive command must require approval even when run_command was remembered")
	}
	if execCtx.canRememberCallApproval(destructive, tool) {
		t.Fatal("destructive command approval must remain one-shot")
	}
}

func TestApprovedPlanScopeAllowsOnlyBoundedLocalWriteAndExec(t *testing.T) {
	writeTool := schema.Tool{Name: "write_file", Server: "file_tools", Kind: string(config.ToolKindWrite)}
	deleteTool := schema.Tool{Name: "delete_file", Server: "file_tools", Kind: string(config.ToolKindWrite)}
	hostWriteTool := schema.Tool{Name: "set_managed_json_value", Server: "host_tools", Kind: string(config.ToolKindWrite)}
	commandTool := schema.Tool{Name: "run_command", Server: "command_tools", Kind: string(config.ToolKindExec)}
	execCtx := newExecutionContext("fixer", config.AgentProfile{ToolPolicy: config.ToolPolicyConfirm}, []schema.Tool{writeTool, deleteTool, hostWriteTool, commandTool}, nil, t.TempDir())
	execCtx.RiskPolicy = config.ToolRiskPolicyConfig{RequireApprovalForUnsandboxedRiskyTools: true}
	execCtx.WorkflowName = workflowNameComplexProjectDelivery
	execCtx.WorkflowStage = "implement"
	execCtx.ApprovedPlanScope = true

	write := schema.ToolCall{ID: "write-1", Name: "write_file", Arguments: json.RawMessage(`{"path":"demo.txt","content":"ok"}`)}
	if execCtx.requiresApproval(write, writeTool) {
		t.Fatal("approved local implementation scope should not prompt again for a bounded workspace write")
	}
	deleteCall := schema.ToolCall{ID: "delete-1", Name: "delete_file", Arguments: json.RawMessage(`{"path":"demo.txt"}`)}
	if !execCtx.requiresApproval(deleteCall, deleteTool) {
		t.Fatal("approved local plan scope must not authorize deletion")
	}
	hostWrite := schema.ToolCall{ID: "host-1", Name: "set_managed_json_value", Arguments: json.RawMessage(`{"target_id":"local"}`)}
	if !execCtx.requiresApproval(hostWrite, hostWriteTool) {
		t.Fatal("approved local plan scope must not authorize host changes")
	}
	inspection := commandToolCall(t, "go", []string{"test", "./..."})
	if execCtx.requiresApproval(inspection, commandTool) {
		t.Fatal("approved local implementation scope should not prompt again for a non-destructive verification command")
	}
	format := commandToolCall(t, "go", []string{"fmt", "./..."})
	if execCtx.requiresApproval(format, commandTool) {
		t.Fatal("approved local implementation scope should authorize the planned workspace formatter")
	}
	formatCheck := commandToolCall(t, "gofmt", []string{"-l", "./..."})
	if execCtx.requiresApproval(formatCheck, commandTool) {
		t.Fatal("approved local implementation scope should authorize a read-only format check")
	}
	unknown := commandToolCall(t, "custom-tool", []string{"run"})
	if !execCtx.requiresApproval(unknown, commandTool) {
		t.Fatal("approved local plan scope must not authorize an unknown executable")
	}
	shell := commandToolCall(t, "powershell", []string{"-Command", "Remove-Item demo.txt"})
	if !execCtx.requiresApproval(shell, commandTool) {
		t.Fatal("approved local plan scope must not authorize shell execution")
	}
	destructive := commandToolCall(t, "git", []string{"reset", "--hard"})
	if !execCtx.requiresApproval(destructive, commandTool) {
		t.Fatal("destructive commands must remain one-shot approvals inside an approved plan scope")
	}

	execCtx.TargetID = "remote-server"
	if !execCtx.requiresApproval(write, writeTool) {
		t.Fatal("approved local plan scope must not authorize a remote target")
	}
}

func TestRunPermissionModesControlApprovalWithoutBypassingToolPolicy(t *testing.T) {
	writeTool := schema.Tool{Name: "write_file", Server: "file_tools", Kind: string(config.ToolKindWrite)}
	deleteTool := schema.Tool{Name: "delete_file", Server: "file_tools", Kind: string(config.ToolKindWrite)}
	commandTool := schema.Tool{Name: "run_command", Server: "command_tools", Kind: string(config.ToolKindExec)}
	networkTool := schema.Tool{Name: "fetch_url", Server: "web_tools", Kind: string(config.ToolKindNetwork)}
	execCtx := newExecutionContext("fixer", config.AgentProfile{ToolPolicy: config.ToolPolicyConfirm}, []schema.Tool{writeTool, deleteTool, commandTool, networkTool}, nil, t.TempDir())
	execCtx.RiskPolicy = config.ToolRiskPolicyConfig{RequireApprovalForUnsandboxedRiskyTools: true}
	write := schema.ToolCall{ID: "write-1", Name: "write_file", Arguments: json.RawMessage(`{"path":"demo.txt","content":"ok"}`)}
	remove := schema.ToolCall{ID: "delete-1", Name: "delete_file", Arguments: json.RawMessage(`{"path":"demo.txt"}`)}
	testCall := commandToolCall(t, "go", []string{"test", "./..."})
	unknown := commandToolCall(t, "custom-tool", []string{"run"})
	network := schema.ToolCall{ID: "network-1", Name: "fetch_url", Arguments: json.RawMessage(`{"url":"https://example.test"}`)}

	execCtx.RunPermissionMode = RunPermissionWorkspace
	if execCtx.requiresApproval(write, writeTool) || execCtx.requiresApproval(testCall, commandTool) {
		t.Fatal("workspace permission should allow bounded workspace writes and validation commands")
	}
	if !execCtx.requiresApproval(remove, deleteTool) || !execCtx.requiresApproval(unknown, commandTool) || !execCtx.requiresApproval(network, networkTool) {
		t.Fatal("workspace permission must keep deletion, unknown commands, and network tools approval-gated")
	}

	execCtx.RunPermissionMode = RunPermissionFull
	for _, item := range []struct {
		call schema.ToolCall
		tool schema.Tool
	}{{remove, deleteTool}, {unknown, commandTool}, {network, networkTool}} {
		if execCtx.requiresApproval(item.call, item.tool) {
			t.Fatalf("full permission should not prompt for configured tool %s", item.tool.Name)
		}
	}
}

func TestApprovedAgentTaskScopeAllowsOnlyBoundedWorkspaceWork(t *testing.T) {
	writeTool := schema.Tool{Name: "write_file", Server: "file_tools", Kind: string(config.ToolKindWrite)}
	deleteTool := schema.Tool{Name: "delete_file", Server: "file_tools", Kind: string(config.ToolKindWrite)}
	hostWriteTool := schema.Tool{Name: "set_managed_json_value", Server: "host_tools", Kind: string(config.ToolKindWrite)}
	commandTool := schema.Tool{Name: "run_command", Server: "command_tools", Kind: string(config.ToolKindExec)}
	networkTool := schema.Tool{Name: "fetch_url", Server: "web_tools", Kind: string(config.ToolKindNetwork)}
	execCtx := newExecutionContext("software-engineer", config.AgentProfile{ToolPolicy: config.ToolPolicyConfirm}, []schema.Tool{writeTool, deleteTool, hostWriteTool, commandTool, networkTool}, nil, t.TempDir())
	execCtx.AgentRunID = "agent-run-1"
	execCtx.ApprovedTaskScope = true
	execCtx.RiskPolicy = config.ToolRiskPolicyConfig{RequireApprovalForUnsandboxedRiskyTools: true}

	write := schema.ToolCall{ID: "write-1", Name: "write_file", Arguments: json.RawMessage(`{"path":"demo.txt","content":"ok"}`)}
	if execCtx.requiresApproval(write, writeTool) {
		t.Fatal("task scope should allow a bounded file_tools write")
	}
	if execCtx.requiresApproval(commandToolCall(t, "go", []string{"test", "./..."}), commandTool) {
		t.Fatal("task scope should allow a classified test command")
	}
	if execCtx.requiresApproval(commandToolCall(t, "gofmt", []string{"-w", "."}), commandTool) {
		t.Fatal("task scope should allow a classified formatter")
	}

	delete := schema.ToolCall{ID: "delete-1", Name: "delete_file", Arguments: json.RawMessage(`{"path":"demo.txt"}`)}
	if !execCtx.requiresApproval(delete, deleteTool) {
		t.Fatal("task scope must not authorize deletion")
	}
	hostWrite := schema.ToolCall{ID: "host-1", Name: "set_managed_json_value", Arguments: json.RawMessage(`{"target_id":"local"}`)}
	if !execCtx.requiresApproval(hostWrite, hostWriteTool) {
		t.Fatal("task scope must not authorize host changes")
	}
	network := schema.ToolCall{ID: "network-1", Name: "fetch_url", Arguments: json.RawMessage(`{"url":"https://example.com"}`)}
	if !execCtx.requiresApproval(network, networkTool) {
		t.Fatal("task scope must not authorize network tools")
	}
	for name, call := range map[string]schema.ToolCall{
		"dependency install":  commandToolCall(t, "go", []string{"mod", "tidy"}),
		"destructive command": commandToolCall(t, "git", []string{"reset", "--hard"}),
		"unknown command":     commandToolCall(t, "custom-tool", []string{"run"}),
		"shell command":       commandToolCall(t, "powershell", []string{"-Command", "Get-ChildItem"}),
	} {
		if !execCtx.requiresApproval(call, commandTool) {
			t.Fatalf("task scope must not authorize %s", name)
		}
	}

	execCtx.TargetID = "remote-1"
	if !execCtx.requiresApproval(write, writeTool) {
		t.Fatal("task scope must not cross execution targets")
	}
	execCtx.TargetID = "local"
	execCtx.WorkflowName = "custom-workflow"
	if !execCtx.requiresApproval(write, writeTool) {
		t.Fatal("ordinary task scope must not leak into a workflow")
	}
}

func commandToolCall(t *testing.T, program string, args []string) schema.ToolCall {
	t.Helper()
	arguments, err := json.Marshal(map[string]any{"program": program, "args": args})
	if err != nil {
		t.Fatal(err)
	}
	return schema.ToolCall{ID: "call-1", Name: "run_command", Arguments: arguments}
}
