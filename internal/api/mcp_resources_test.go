package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/agent"
	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/interfaces"
	"github.com/FyMatt/GoFlow-Agent/internal/runtime"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestMCPResourceCRUDAndExportPreservesSimpleConfiguration(t *testing.T) {
	server := NewServer(newAPITestRuntime(t))
	body := `{
		"name":"docs-server",
		"target_scope":"host",
		"transport":"stdio",
		"command":"node",
		"args":["server.js","--workspace","demo repo"],
		"workdir":".",
		"env_allowlist":["PATH","DOCS_TOKEN"],
		"enabled":true,
		"optional":true,
		"timeout":"45s"
	}`

	put := httptest.NewRecorder()
	server.ServeHTTP(put, httptest.NewRequest(http.MethodPut, "/api/resources/mcp/docs-server", strings.NewReader(body)))
	if put.Code != http.StatusOK {
		t.Fatalf("save MCP resource: status=%d body=%s", put.Code, put.Body.String())
	}
	var saved mcpResourceDocument
	if err := json.Unmarshal(put.Body.Bytes(), &saved); err != nil {
		t.Fatalf("decode saved MCP resource: %v", err)
	}
	if saved.Name != "docs-server" || saved.TargetScope != "host" || saved.Timeout != "45s" || !saved.Optional || len(saved.Args) != 3 || saved.Args[1] != "--workspace" || saved.Args[2] != "demo repo" {
		t.Fatalf("unexpected saved MCP resource: %+v", saved)
	}

	path := filepath.Join(server.runtime.RuntimeHome(), "configs", "mcp_servers", "docs-server.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved MCP config: %v", err)
	}
	if !strings.Contains(string(data), "target_scope: host") || !strings.Contains(string(data), "timeout: 45s") || !strings.Contains(string(data), "optional: true") || !strings.Contains(string(data), "- --workspace") {
		t.Fatalf("saved MCP config lost simple fields: %s", data)
	}
	if !strings.Contains(string(data), "isolation: auto") {
		t.Fatalf("third-party MCP config must use automatic strong-sandbox resolution: %s", data)
	}

	get := httptest.NewRecorder()
	server.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/api/resources/mcp/docs-server", nil))
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), `"target_scope":"host"`) || !strings.Contains(get.Body.String(), `"optional":true`) || !strings.Contains(get.Body.String(), `"apply_state":"restart_required"`) {
		t.Fatalf("get MCP resource: status=%d body=%s", get.Code, get.Body.String())
	}

	export := httptest.NewRecorder()
	server.ServeHTTP(export, httptest.NewRequest(http.MethodGet, "/api/resources/mcp/docs-server/export?format=claude", nil))
	if export.Code != http.StatusOK || !strings.Contains(export.Body.String(), `"command": "node"`) || !strings.Contains(export.Body.String(), `"DOCS_TOKEN": "${DOCS_TOKEN}"`) {
		t.Fatalf("Claude export did not preserve safe references: status=%d body=%s", export.Code, export.Body.String())
	}
	if strings.Contains(export.Body.String(), "secret-value") {
		t.Fatal("Claude export must not contain environment secret values")
	}

	remove := httptest.NewRecorder()
	server.ServeHTTP(remove, httptest.NewRequest(http.MethodDelete, "/api/resources/mcp/docs-server", nil))
	if remove.Code != http.StatusNoContent {
		t.Fatalf("delete MCP resource: status=%d body=%s", remove.Code, remove.Body.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected MCP config to be deleted, stat error=%v", err)
	}
}

func TestMCPResourceCodexAndClaudeImport(t *testing.T) {
	server := NewServer(newAPITestRuntime(t))
	codex := `
[mcp_servers.repo]
command = "python"
args = ["server.py", "--port", "9000"]
cwd = "."
env = { REPO_TOKEN = "do-not-save" }
tool_timeout_sec = 17
`
	importCodex := httptest.NewRecorder()
	server.ServeHTTP(importCodex, httptest.NewRequest(http.MethodPost, "/api/resources/mcp/import?format=codex", strings.NewReader(codex)))
	if importCodex.Code != http.StatusOK || !strings.Contains(importCodex.Body.String(), `"name":"repo"`) || !strings.Contains(importCodex.Body.String(), "environment values were replaced") {
		t.Fatalf("Codex MCP import: status=%d body=%s", importCodex.Code, importCodex.Body.String())
	}

	claude := `{"mcpServers":{"browser":{"type":"stdio","command":"npx","args":["browser-mcp"],"env":{"BROWSER_TOKEN":"hidden"}}}}`
	importClaude := httptest.NewRecorder()
	server.ServeHTTP(importClaude, httptest.NewRequest(http.MethodPost, "/api/resources/mcp/import?format=claude", strings.NewReader(claude)))
	if importClaude.Code != http.StatusOK || !strings.Contains(importClaude.Body.String(), `"name":"browser"`) || !strings.Contains(importClaude.Body.String(), "environment values were replaced") {
		t.Fatalf("Claude MCP import: status=%d body=%s", importClaude.Code, importClaude.Body.String())
	}

	list := httptest.NewRecorder()
	server.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/resources/mcp", nil))
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"name":"repo"`) || !strings.Contains(list.Body.String(), `"name":"browser"`) {
		t.Fatalf("MCP list after imports: status=%d body=%s", list.Code, list.Body.String())
	}
}

func TestMCPResourceRejectsHTTPTransport(t *testing.T) {
	server := NewServer(newAPITestRuntime(t))
	request := httptest.NewRequest(http.MethodPut, "/api/resources/mcp/remote", strings.NewReader(`{"transport":"sse","command":"https://example.test/mcp","enabled":true}`))
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "not available yet") {
		t.Fatalf("expected HTTP MCP transport rejection: status=%d body=%s", response.Code, response.Body.String())
	}

	codexHTTP := "[mcp_servers.remote]\nurl = \"https://example.test/mcp\"\n"
	importResponse := httptest.NewRecorder()
	server.ServeHTTP(importResponse, httptest.NewRequest(http.MethodPost, "/api/resources/mcp/import?format=codex", strings.NewReader(codexHTTP)))
	if importResponse.Code != http.StatusBadRequest || !strings.Contains(importResponse.Body.String(), "imports stdio servers only") {
		t.Fatalf("expected Codex HTTP MCP rejection: status=%d body=%s", importResponse.Code, importResponse.Body.String())
	}
}

func TestMCPResourceRejectsUnsupportedTargetScope(t *testing.T) {
	server := NewServer(newAPITestRuntime(t))
	request := httptest.NewRequest(http.MethodPut, "/api/resources/mcp/remote", strings.NewReader(`{"target_scope":"remote","transport":"stdio","command":"node","enabled":true}`))
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "target_scope") {
		t.Fatalf("expected unsupported target scope rejection: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestMCPResourceTestChecksAvailabilityWithoutSaving(t *testing.T) {
	server := NewServer(newAPITestRuntime(t))
	command, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}
	body, err := json.Marshal(mcpResourceDocument{
		Name:      "availability-check",
		Transport: "stdio",
		Command:   command,
		WorkDir:   ".",
		Enabled:   true,
		Timeout:   "30s",
	})
	if err != nil {
		t.Fatalf("marshal MCP test request: %v", err)
	}

	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/resources/mcp/availability-check/test", strings.NewReader(string(body))))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"ready":true`) || !strings.Contains(response.Body.String(), `"status":"ready"`) {
		t.Fatalf("MCP availability test: status=%d body=%s", response.Code, response.Body.String())
	}
	path := filepath.Join(server.runtime.RuntimeHome(), "configs", "mcp_servers", "availability-check.yaml")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("availability test must not save configuration, stat error=%v", err)
	}
}

func TestMCPResourceTestRejectsMissingCommand(t *testing.T) {
	server := NewServer(newAPITestRuntime(t))
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/resources/mcp/missing/test", strings.NewReader(`{
		"transport":"stdio",
		"command":"goflow-command-that-does-not-exist",
		"workdir":".",
		"enabled":true,
		"timeout":"30s"
	}`)))
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), `"ready":false`) || !strings.Contains(response.Body.String(), `"status":"unavailable"`) {
		t.Fatalf("expected missing MCP command to fail availability check: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestMCPCatalogReportsToolCountAndEditability(t *testing.T) {
	runtimeRef := newAPITestRuntimeWithStateHomeResponsesAndMCP(t, nil, "", nil, []config.MCPServerRef{{
		Name:    "file_tools",
		Command: "go",
		Enabled: true,
		Timeout: 30 * time.Second,
		WorkDir: ".",
	}})
	server := NewServer(runtimeRef)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/resources?kind=mcp", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("MCP catalog: status=%d body=%s", response.Code, response.Body.String())
	}
	var catalog resourceCatalogResponse
	if err := json.Unmarshal(response.Body.Bytes(), &catalog); err != nil {
		t.Fatalf("decode MCP catalog: %v", err)
	}
	if len(catalog.Items) != 1 || catalog.Items[0].Name != "file_tools" || catalog.Items[0].Title != "文件操作连接" || !strings.Contains(catalog.Items[0].Description, "可审计 Diff") || catalog.Items[0].ToolCount != 3 || catalog.Items[0].Editable || !catalog.Items[0].Builtin {
		t.Fatalf("unexpected MCP catalog metadata: %+v", catalog.Items)
	}
}

func TestBuiltinMCPResourceIsReadOnlyWithSeparateDataRoot(t *testing.T) {
	runtimeHome := t.TempDir()
	dataRoot := t.TempDir()
	configDir := filepath.Join(runtimeHome, "configs", "mcp_servers")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("create built-in MCP directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "builtin-tools.yaml"), []byte(`mcp_servers:
  - name: builtin_tools
    command: go
    enabled: true
    timeout: 30s
    workdir: .
`), 0o644); err != nil {
		t.Fatalf("write built-in MCP module: %v", err)
	}
	cfg := &config.Config{
		RuntimeHome:   runtimeHome,
		DataRoot:      dataRoot,
		WorkspaceRoot: t.TempDir(),
		Audit:         config.AuditConfig{},
		Session:       config.SessionConfig{MaxHistory: 8},
		DefaultAgent:  "chat",
		Providers: map[string]config.LLMConfig{
			"primary": {Provider: "openai-compatible", BaseURL: "http://localhost:9999/v1", Model: "test-model"},
		},
		Agents: map[string]config.AgentProfile{
			"chat": {Name: "Chat", Provider: "primary", Mode: "chat", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead}, ToolPolicy: config.ToolPolicyConfirm},
		},
		MCP: []config.MCPServerRef{{Name: "builtin_tools", Command: "go", Enabled: true, Timeout: 30 * time.Second, WorkDir: "."}},
	}
	clients := map[string]interfaces.LLMClient{"primary": &apiTestLLM{responses: []schema.ChatResponse{}}}
	runtimeRef, err := agent.NewRuntime(cfg, clients, apiTestSkillManager{}, &apiTestMCP{toolNames: []string{"builtin_tools/read"}}, session.New(8), runtime.NewAuditLogger(false, false))
	if err != nil {
		t.Fatalf("create runtime with separate data root: %v", err)
	}
	server := NewServer(runtimeRef)

	get := httptest.NewRecorder()
	server.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/api/resources/mcp/builtin_tools", nil))
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), `"builtin":true`) || !strings.Contains(get.Body.String(), `"read_only":true`) || !strings.Contains(get.Body.String(), `"tool_count":1`) {
		t.Fatalf("built-in MCP detail metadata: status=%d body=%s", get.Code, get.Body.String())
	}

	put := httptest.NewRecorder()
	server.ServeHTTP(put, httptest.NewRequest(http.MethodPut, "/api/resources/mcp/builtin_tools", strings.NewReader(`{"command":"node","enabled":true}`)))
	if put.Code != http.StatusForbidden || !strings.Contains(put.Body.String(), "read-only") {
		t.Fatalf("built-in MCP update should be forbidden: status=%d body=%s", put.Code, put.Body.String())
	}
	remove := httptest.NewRecorder()
	server.ServeHTTP(remove, httptest.NewRequest(http.MethodDelete, "/api/resources/mcp/builtin_tools", nil))
	if remove.Code != http.StatusForbidden || !strings.Contains(remove.Body.String(), "read-only") {
		t.Fatalf("built-in MCP delete should be forbidden: status=%d body=%s", remove.Code, remove.Body.String())
	}
}
