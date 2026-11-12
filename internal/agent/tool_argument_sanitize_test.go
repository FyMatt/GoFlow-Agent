package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func sanitizerTestTool(kind, schemaJSON string) schema.Tool {
	return schema.Tool{Name: "read_file", Server: "file_tools", Kind: kind, InputSchema: json.RawMessage(schemaJSON)}
}

func sanitizerTestCall(args map[string]any) schema.ToolCall {
	encoded, _ := json.Marshal(args)
	return schema.ToolCall{ID: "c1", Name: "read_file", Arguments: encoded}
}

func TestSanitizeDropsUnknownFieldsOnReadTool(t *testing.T) {
	tool := sanitizerTestTool("read", `{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)
	call := sanitizerTestCall(map[string]any{"path": "a.go", "target_id": "edge-b"})
	sanitized := sanitizeReadOnlyToolArguments(tool, call)
	var arguments map[string]any
	if err := json.Unmarshal(sanitized.Arguments, &arguments); err != nil {
		t.Fatalf("decode arguments: %v", err)
	}
	if _, ok := arguments["target_id"]; ok {
		t.Fatalf("read-only call must drop undeclared fields, got %v", arguments)
	}
	if arguments["path"] != "a.go" {
		t.Fatalf("declared field must survive, got %v", arguments)
	}
}

func TestSanitizeKeepsUnknownFieldsOnWriteTool(t *testing.T) {
	tool := sanitizerTestTool("write", `{"type":"object","properties":{"path":{"type":"string"}}}`)
	call := sanitizerTestCall(map[string]any{"path": "a.go", "extra": "x"})
	sanitized := sanitizeReadOnlyToolArguments(tool, call)
	if !strings.Contains(string(sanitized.Arguments), `"extra"`) {
		t.Fatalf("write tool arguments must not be rewritten, got %s", sanitized.Arguments)
	}
}

func TestSanitizeKeepsArgumentsWhenSchemaHasNoProperties(t *testing.T) {
	tool := sanitizerTestTool("read", `{"type":"object"}`)
	call := sanitizerTestCall(map[string]any{"path": "a.go"})
	sanitized := sanitizeReadOnlyToolArguments(tool, call)
	if string(sanitized.Arguments) != string(call.Arguments) {
		t.Fatalf("missing schema properties must leave arguments untouched")
	}
}
