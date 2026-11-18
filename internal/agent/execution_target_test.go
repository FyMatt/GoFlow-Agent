package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestNormalizeOptionalWriteFileSHA(t *testing.T) {
	placeholder := schema.ToolCall{Name: "file_tools/write_file", Arguments: json.RawMessage(`{"path":"report.md","content":"done","expected_sha256":"e0d5e4b0f5e2f1a0b9c8d7e6f5a4b3c2d1e0f0..."}`)}
	normalized := normalizeOptionalToolArguments(placeholder)
	if strings.Contains(string(normalized.Arguments), "expected_sha256") {
		t.Fatalf("invalid optional SHA placeholder must be removed, got %s", normalized.Arguments)
	}

	validSHA := strings.Repeat("a", 64)
	valid := schema.ToolCall{Name: "write_file", Arguments: json.RawMessage(`{"path":"report.md","content":"done","expected_sha256":"` + validSHA + `"}`)}
	kept := normalizeOptionalToolArguments(valid)
	if !strings.Contains(string(kept.Arguments), validSHA) {
		t.Fatalf("valid concurrency SHA must be preserved, got %s", kept.Arguments)
	}
}

func TestExecutionContextBindsHostToolToSelectedRemoteTarget(t *testing.T) {
	tool := schema.Tool{
		Name: "ssh_tools/ssh_run_command", Server: "ssh_tools", Kind: "exec",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"target_id":{"type":"string"},"command":{"type":"string"}},"required":["target_id","command"],"additionalProperties":false}`),
	}
	execCtx := newExecutionContext("operations", config.AgentProfile{AllowedToolKinds: []config.ToolKind{config.ToolKindExec}, ToolPolicy: config.ToolPolicyAllow}, []schema.Tool{tool}, nil, t.TempDir())
	execCtx.TargetID = "edge-a"
	execCtx.MCPServers = []config.MCPServerRef{{Name: "ssh_tools", TargetScope: "host"}}

	valid := schema.ToolCall{Name: tool.Name, Arguments: json.RawMessage(`{"target_id":"edge-a","command":"uptime"}`)}
	if _, err := execCtx.validateToolCall(valid); err != nil {
		t.Fatalf("expected matching target to pass: %v", err)
	}
	_, err := execCtx.validateToolCall(schema.ToolCall{Name: tool.Name, Arguments: json.RawMessage(`{"target_id":"edge-b","command":"uptime"}`)})
	if err == nil || !strings.Contains(err.Error(), "task Target") {
		t.Fatalf("expected mismatched Target rejection, got %v", err)
	}
	_, err = execCtx.validateToolCall(schema.ToolCall{Name: tool.Name, Arguments: json.RawMessage(`{"command":"uptime"}`)})
	if err == nil {
		t.Fatal("expected missing target_id rejection")
	}
}

func TestExecutionContextRejectsRemoteTargetArgumentOnLocalTask(t *testing.T) {
	tool := schema.Tool{
		Name: "ssh_tools/ssh_host_key_scan", Server: "ssh_tools", Kind: "network",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"target_id":{"type":"string"}},"additionalProperties":false}`),
	}
	execCtx := newExecutionContext("operations", config.AgentProfile{AllowedToolKinds: []config.ToolKind{config.ToolKindNetwork}, ToolPolicy: config.ToolPolicyAllow}, []schema.Tool{tool}, nil, t.TempDir())
	execCtx.MCPServers = []config.MCPServerRef{{Name: "ssh_tools", TargetScope: "host"}}
	_, err := execCtx.validateToolCall(schema.ToolCall{Name: tool.Name, Arguments: json.RawMessage(`{"target_id":"edge-a"}`)})
	if err == nil || !strings.Contains(err.Error(), `task Target "local"`) {
		t.Fatalf("expected local Target binding rejection, got %v", err)
	}
	if _, err := execCtx.validateToolCall(schema.ToolCall{Name: tool.Name, Arguments: json.RawMessage(`{}`)}); err != nil {
		t.Fatalf("local task should allow an explicitly authorized first-time host-key scan without target_id: %v", err)
	}
}
