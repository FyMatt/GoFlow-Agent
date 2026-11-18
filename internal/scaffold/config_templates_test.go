package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderAgentAndProviderConfigTemplatesUseEmbeddedDefaults(t *testing.T) {
	agent, err := RenderAgentConfigTemplate("", "researcher")
	if err != nil {
		t.Fatalf("RenderAgentConfigTemplate: %v", err)
	}
	if !strings.Contains(agent, "researcher:") || !strings.Contains(agent, "allowed_tool_kinds: [read]") || !strings.Contains(agent, "allowed_tools") {
		t.Fatalf("unexpected agent template:\n%s", agent)
	}

	provider, err := RenderProviderConfigTemplate("", "deepseek")
	if err != nil {
		t.Fatalf("RenderProviderConfigTemplate: %v", err)
	}
	if !strings.Contains(provider, "deepseek:") || !strings.Contains(provider, "api_key: ${DEEPSEEK_API_KEY}") {
		t.Fatalf("unexpected provider template:\n%s", provider)
	}
}

func TestRenderConfigTemplatesUseRuntimeOverrides(t *testing.T) {
	root := t.TempDir()
	agentDir := filepath.Join(root, "templates", "config", "agents")
	providerDir := filepath.Join(root, "templates", "config", "providers")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatalf("mkdir agent template dir: %v", err)
	}
	if err := os.MkdirAll(providerDir, 0o755); err != nil {
		t.Fatalf("mkdir provider template dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "default.yaml.tmpl"), []byte(`{{.Name}}:
  provider: custom
  mode: fix
  tool_policy: confirm
  allowed_tool_kinds: [read, write]
  max_iterations: 3
`), 0o644); err != nil {
		t.Fatalf("write agent template: %v", err)
	}
	if err := os.WriteFile(filepath.Join(providerDir, "default.yaml.tmpl"), []byte(`{{.Name}}:
  provider: custom-compatible
  base_url: https://custom.example/v1
  api_key: ${{"{"}}{{.EnvAPIKey}}{{"}"}}
  model: custom-model
  timeout: 30s
  retry_count: 1
`), 0o644); err != nil {
		t.Fatalf("write provider template: %v", err)
	}

	agent, err := RenderAgentConfigTemplate(root, "writer")
	if err != nil {
		t.Fatalf("RenderAgentConfigTemplate override: %v", err)
	}
	if !strings.Contains(agent, "allowed_tool_kinds: [read, write]") || !strings.Contains(agent, "mode: fix") {
		t.Fatalf("expected runtime agent override, got:\n%s", agent)
	}

	provider, err := RenderProviderConfigTemplate(root, "local-ai")
	if err != nil {
		t.Fatalf("RenderProviderConfigTemplate override: %v", err)
	}
	if !strings.Contains(provider, "provider: custom-compatible") || !strings.Contains(provider, "api_key: ${LOCAL_AI_API_KEY}") {
		t.Fatalf("expected runtime provider override, got:\n%s", provider)
	}
}
