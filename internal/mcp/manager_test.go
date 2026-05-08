package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

const validToolSchema = `{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`

func TestFindToolClientRejectsAmbiguousShortNames(t *testing.T) {
	manager := &Manager{
		toolIndex: map[string]*Client{
			"alpha/read_file": {name: "alpha"},
			"beta/read_file":  {name: "beta"},
		},
		ambiguousShortNames: map[string][]string{
			"read_file": {"alpha/read_file", "beta/read_file"},
		},
		cachedTools: []schema.Tool{
			{Name: "read_file", Server: "alpha"},
			{Name: "read_file", Server: "beta"},
		},
	}

	_, _, err := manager.findToolClient(context.Background(), "read_file")
	if err == nil || !strings.Contains(err.Error(), "ambiguous tool name") || !strings.Contains(err.Error(), "alpha/read_file") {
		t.Fatalf("expected ambiguous short-name error, got %v", err)
	}

	client, toolName, err := manager.findToolClient(context.Background(), "alpha/read_file")
	if err != nil {
		t.Fatalf("expected qualified tool to resolve: %v", err)
	}
	if client.name != "alpha" || toolName != "read_file" {
		t.Fatalf("unexpected qualified resolution: client=%#v tool=%q", client, toolName)
	}
}

func TestUniqueSortedNonEmpty(t *testing.T) {
	values := uniqueSortedNonEmpty([]string{"beta/read", "", "alpha/read", "beta/read"})
	if len(values) != 2 || values[0] != "alpha/read" || values[1] != "beta/read" {
		t.Fatalf("unexpected unique values: %#v", values)
	}
}

func TestValidateDiscoveredToolRequiresStrictMetadata(t *testing.T) {
	valid := schema.Tool{Name: "read_file", Kind: "read", InputSchema: json.RawMessage(validToolSchema)}
	if err := validateDiscoveredTool(valid); err != nil {
		t.Fatalf("expected valid tool metadata: %v", err)
	}

	tests := []struct {
		name string
		tool schema.Tool
		want string
	}{
		{
			name: "empty name",
			tool: schema.Tool{Kind: "read", InputSchema: json.RawMessage(validToolSchema)},
			want: "tool name is required",
		},
		{
			name: "unsafe name",
			tool: schema.Tool{Name: "read/file", Kind: "read", InputSchema: json.RawMessage(validToolSchema)},
			want: "must match",
		},
		{
			name: "missing kind",
			tool: schema.Tool{Name: "read_file", InputSchema: json.RawMessage(validToolSchema)},
			want: "tool kind is required",
		},
		{
			name: "invalid kind",
			tool: schema.Tool{Name: "read_file", Kind: "browser", InputSchema: json.RawMessage(validToolSchema)},
			want: "tool kind",
		},
		{
			name: "missing schema",
			tool: schema.Tool{Name: "read_file", Kind: "read"},
			want: "input_schema is required",
		},
		{
			name: "invalid schema json",
			tool: schema.Tool{Name: "read_file", Kind: "read", InputSchema: json.RawMessage(`{"type":"object"`)},
			want: "decode input_schema",
		},
		{
			name: "schema with trailing json",
			tool: schema.Tool{Name: "read_file", Kind: "read", InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false} {}`)},
			want: "unexpected trailing data",
		},
		{
			name: "non object schema",
			tool: schema.Tool{Name: "read_file", Kind: "read", InputSchema: json.RawMessage(`{"type":"string","properties":{},"additionalProperties":false}`)},
			want: "type must be object",
		},
		{
			name: "missing properties",
			tool: schema.Tool{Name: "read_file", Kind: "read", InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`)},
			want: "must declare properties",
		},
		{
			name: "open additional properties",
			tool: schema.Tool{Name: "read_file", Kind: "read", InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":true}`)},
			want: "additionalProperties=false",
		},
		{
			name: "required not declared",
			tool: schema.Tool{Name: "read_file", Kind: "read", InputSchema: json.RawMessage(`{"type":"object","properties":{},"required":["path"],"additionalProperties":false}`)},
			want: "required field",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateDiscoveredTool(tt.tool)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected %q error, got %v", tt.want, err)
			}
		})
	}
}

func TestRefreshToolsRejectsDuplicateToolNamesFromSameServer(t *testing.T) {
	manager := newHelperMCPManager(t, "helper", []schema.Tool{
		{Name: "read_file", Description: "Read one.", Kind: "read", InputSchema: json.RawMessage(validToolSchema)},
		{Name: "read_file", Description: "Read two.", Kind: "read", InputSchema: json.RawMessage(validToolSchema)},
	})
	defer stopManagerClients(manager)

	_, err := manager.RefreshTools(context.Background())
	if err == nil || !strings.Contains(err.Error(), "advertised duplicate tool") || !strings.Contains(err.Error(), "read_file") {
		t.Fatalf("expected duplicate tool-name discovery error, got %v", err)
	}
}

func TestRefreshToolsRejectsInvalidDiscoveredToolMetadata(t *testing.T) {
	manager := newHelperMCPManager(t, "bad", []schema.Tool{{
		Name:        "read_file",
		Description: "Read a file.",
		Kind:        "",
		InputSchema: json.RawMessage(validToolSchema),
	}})
	defer stopManagerClients(manager)

	_, err := manager.RefreshTools(context.Background())
	if err == nil || !strings.Contains(err.Error(), "advertised invalid tool") || !strings.Contains(err.Error(), "tool kind is required") {
		t.Fatalf("expected invalid tool metadata error, got %v", err)
	}
}

func TestRefreshToolsIndexesValidDiscoveredTools(t *testing.T) {
	manager := newHelperMCPManager(t, "helper", []schema.Tool{{
		Name:        "read_file",
		Description: "Read a file.",
		Kind:        "read",
		InputSchema: json.RawMessage(validToolSchema),
	}})
	defer stopManagerClients(manager)

	tools, err := manager.RefreshTools(context.Background())
	if err != nil {
		t.Fatalf("refresh tools: %v", err)
	}
	if len(tools) != 1 || tools[0].Server != "helper" {
		t.Fatalf("unexpected tools: %#v", tools)
	}
	client, toolName, err := manager.findToolClient(context.Background(), "helper/read_file")
	if err != nil {
		t.Fatalf("expected qualified tool resolution: %v", err)
	}
	if client.name != "helper" || toolName != "read_file" {
		t.Fatalf("unexpected resolution: client=%s tool=%s", client.name, toolName)
	}
}

func TestRefreshToolsKeepsAmbiguousShortNamesOutOfIndex(t *testing.T) {
	manager := newHelperMCPManagerWithServers(t, []string{"alpha", "beta"}, []schema.Tool{{
		Name:        "read_file",
		Description: "Read a file.",
		Kind:        "read",
		InputSchema: json.RawMessage(validToolSchema),
	}})
	defer stopManagerClients(manager)

	if _, err := manager.RefreshTools(context.Background()); err != nil {
		t.Fatalf("refresh tools: %v", err)
	}
	if _, _, err := manager.findToolClient(context.Background(), "read_file"); err == nil || !strings.Contains(err.Error(), "ambiguous tool name") || !strings.Contains(err.Error(), "alpha/read_file") || !strings.Contains(err.Error(), "beta/read_file") {
		t.Fatalf("expected ambiguous short-name routing error, got %v", err)
	}
	client, toolName, err := manager.findToolClient(context.Background(), "beta/read_file")
	if err != nil {
		t.Fatalf("expected qualified tool to resolve: %v", err)
	}
	if client.name != "beta" || toolName != "read_file" {
		t.Fatalf("unexpected qualified resolution: client=%s tool=%s", client.name, toolName)
	}
}

func TestManagerCloseClearsClientsAndIndexes(t *testing.T) {
	manager := &Manager{
		clients: map[string]*Client{
			"file_tools": {name: "file_tools"},
		},
		toolIndex: map[string]*Client{
			"file_tools/read_file": {name: "file_tools"},
		},
		ambiguousShortNames: map[string][]string{
			"read_file": {"alpha/read_file", "beta/read_file"},
		},
		cachedTools: []schema.Tool{{Name: "read_file", Server: "file_tools"}},
	}

	if err := manager.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if names := manager.ToolNames(); len(names) != 0 {
		t.Fatalf("expected tool names to be cleared, got %#v", names)
	}
	status := manager.HealthStatus(context.Background())
	if status["none"] != "disabled" || len(status) != 1 {
		t.Fatalf("expected disabled health after close, got %#v", status)
	}
}

func newHelperMCPManager(t *testing.T, name string, tools []schema.Tool) *Manager {
	return newHelperMCPManagerWithServers(t, []string{name}, tools)
}

func newHelperMCPManagerWithServers(t *testing.T, names []string, tools []schema.Tool) *Manager {
	t.Helper()
	payload, err := json.Marshal(tools)
	if err != nil {
		t.Fatalf("marshal helper tools: %v", err)
	}
	t.Setenv("GOFLOW_TEST_MCP_HELPER", "1")
	t.Setenv("GOFLOW_TEST_MCP_TOOLS", string(payload))

	servers := make([]config.MCPServerRef, 0, len(names))
	for _, name := range names {
		servers = append(servers, config.MCPServerRef{
			Name:             name,
			Command:          os.Args[0],
			Args:             []string{"-test.run=TestMCPManagerHelperProcess"},
			Enabled:          true,
			Timeout:          5 * time.Second,
			WorkDir:          ".",
			EnvAllowlist:     []string{"GOFLOW_TEST_MCP_HELPER", "GOFLOW_TEST_MCP_TOOLS"},
			AllowedCommands:  []string{os.Args[0]},
			MaxRequestBytes:  64 * 1024,
			MaxResponseBytes: 2 * 1024 * 1024,
		})
	}
	manager := NewManager(servers)
	return manager
}

func stopManagerClients(manager *Manager) {
	for _, client := range manager.clients {
		client.mu.Lock()
		_ = client.stopProcessLocked()
		client.mu.Unlock()
	}
}

func TestMCPManagerHelperProcess(t *testing.T) {
	if os.Getenv("GOFLOW_TEST_MCP_HELPER") != "1" {
		return
	}
	var tools []schema.Tool
	if err := json.Unmarshal([]byte(os.Getenv("GOFLOW_TEST_MCP_TOOLS")), &tools); err != nil {
		os.Exit(2)
	}
	type helperResponse struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Result  any    `json:"result,omitempty"`
		Error   any    `json:"error,omitempty"`
	}
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var request rpcRequest
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			_ = encoder.Encode(helperResponse{JSONRPC: "2.0", ID: request.ID, Error: map[string]any{"code": -32700, "message": err.Error()}})
			continue
		}
		switch request.Method {
		case "tools/list":
			_ = encoder.Encode(helperResponse{JSONRPC: "2.0", ID: request.ID, Result: map[string]any{"tools": tools}})
		default:
			_ = encoder.Encode(helperResponse{JSONRPC: "2.0", ID: request.ID, Error: map[string]any{"code": -32601, "message": "method not found"}})
		}
	}
	os.Exit(0)
}
