package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderPythonMCPToolTemplatesUseEmbeddedDefaults(t *testing.T) {
	code, err := RenderPythonMCPToolCode("", "safe-helper")
	if err != nil {
		t.Fatalf("RenderPythonMCPToolCode: %v", err)
	}
	for _, want := range []string{"GOFLOW_WORKSPACE_ROOT", "tools/list", "tools/call", "write_text", "resolves outside workspace root"} {
		if !strings.Contains(code, want) {
			t.Fatalf("expected code to contain %q", want)
		}
	}
	config, err := RenderPythonMCPServerConfig("", "safe-helper")
	if err != nil {
		t.Fatalf("RenderPythonMCPServerConfig: %v", err)
	}
	if !strings.Contains(config, "safe_helper") || !strings.Contains(config, "./mcp_servers/safe-helper.py") {
		t.Fatalf("unexpected config template:\n%s", config)
	}
}

func TestRenderBinaryAnalysisPythonMCPToolTemplateUsesEmbeddedDefault(t *testing.T) {
	code, err := RenderBinaryAnalysisPythonMCPToolCode("", "binary-helper")
	if err != nil {
		t.Fatalf("RenderBinaryAnalysisPythonMCPToolCode: %v", err)
	}
	for _, want := range []string{"GOFLOW_WORKSPACE_ROOT", "binary_file_info", "binary_strings", "hex_preview", "MAX_BINARY_BYTES", "resolves outside workspace root"} {
		if !strings.Contains(code, want) {
			t.Fatalf("expected binary analysis code to contain %q", want)
		}
	}
	if !strings.Contains(code, `SERVER_NAME = "binary_helper"`) {
		t.Fatalf("expected normalized server name, got:\n%s", code)
	}
}

func TestRenderPythonMCPToolTemplatesUseRuntimeOverrides(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "templates", "tools", "python")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir template dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "server.py.tmpl"), []byte(`SERVER_NAME = "{{.ServerName}}"
TOOL_NAME = "{{.Name}}"
`), 0o644); err != nil {
		t.Fatalf("write server template: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml.tmpl"), []byte(`mcp_servers:
  - name: {{.ServerName}}
    args: [./mcp_servers/{{.Name}}.py]
`), 0o644); err != nil {
		t.Fatalf("write config template: %v", err)
	}
	code, err := RenderPythonMCPToolCode(root, "safe-helper")
	if err != nil {
		t.Fatalf("RenderPythonMCPToolCode override: %v", err)
	}
	if !strings.Contains(code, `SERVER_NAME = "safe_helper"`) || !strings.Contains(code, `TOOL_NAME = "safe-helper"`) {
		t.Fatalf("expected runtime code override, got:\n%s", code)
	}
	config, err := RenderPythonMCPServerConfig(root, "safe-helper")
	if err != nil {
		t.Fatalf("RenderPythonMCPServerConfig override: %v", err)
	}
	if !strings.Contains(config, "safe_helper") || !strings.Contains(config, "safe-helper.py") {
		t.Fatalf("expected runtime config override, got:\n%s", config)
	}
}

func TestRenderBinaryAnalysisPythonMCPToolTemplateUsesRuntimeOverride(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "templates", "tools", "python")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir template dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "binary-analysis-server.py.tmpl"), []byte(`SERVER_NAME = "{{.ServerName}}"
TOOL_NAME = "{{.Name}}"
BINARY = True
`), 0o644); err != nil {
		t.Fatalf("write binary analysis template: %v", err)
	}
	code, err := RenderBinaryAnalysisPythonMCPToolCode(root, "binary-helper")
	if err != nil {
		t.Fatalf("RenderBinaryAnalysisPythonMCPToolCode override: %v", err)
	}
	for _, want := range []string{`SERVER_NAME = "binary_helper"`, `TOOL_NAME = "binary-helper"`, "BINARY = True"} {
		if !strings.Contains(code, want) {
			t.Fatalf("expected runtime binary override to contain %q, got:\n%s", want, code)
		}
	}
}
