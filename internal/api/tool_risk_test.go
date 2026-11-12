package api

import (
	"encoding/json"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestCommandToolRiskRecognizesEnforcedWorkspaceCWD(t *testing.T) {
	tool := schema.Tool{
		Name:   "run_command",
		Server: "command_tools",
		Kind:   "exec",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{"cwd":{"type":"string","description":"Working directory inside the active workspace."}}
		}`),
	}
	if !toolSchemaHasWorkspacePathInputs(tool.InputSchema) {
		t.Fatal("expected cwd schema to be recognized as workspace-scoped input")
	}
	if !toolRiskWorkspaceScopeLikelyEnforced(tool) {
		t.Fatal("expected built-in command_tools cwd enforcement to be recognized")
	}
}
