package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderMaterializedKitYAMLUsesEmbeddedTemplate(t *testing.T) {
	content, err := RenderMaterializedKitYAML("", KitPreset{
		Name:        "software-engineering",
		Title:       "Software Kit",
		Description: "Plan and ship code changes.",
		Category:    "software",
		Tags:        []string{"coding"},
		Providers:   []string{"primary"},
	}, MaterializedKitNames{
		Kit:              "acme-kit",
		Agent:            "acme-agent",
		Skill:            "acme-skill",
		Tool:             "acme-helper",
		Workflow:         "acme-workflow",
		WorkflowTemplate: "acme-template",
		TeamTemplate:     "acme-team",
		PolicyRule:       "acme-gate",
	})
	if err != nil {
		t.Fatalf("RenderMaterializedKitYAML: %v", err)
	}
	for _, want := range []string{
		"name: acme-kit",
		"generated_agent: acme-agent",
		"recommended_workflow: acme-workflow",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected rendered kit to contain %q, got:\n%s", want, content)
		}
	}
}

func TestRenderMaterializedAgentConfigUsesRuntimeOverride(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "templates", "kits", "materialized", "generic")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create template dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent.yaml.tmpl"), []byte(`{{.Names.Agent}}:
  name: Runtime Override Agent
  description: rendered from runtime home
  provider: {{.PrimaryProvider}}
  mode: chat
  tool_policy: confirm
  allowed_tool_kinds: [read]
  allowed_tools:
    - {{.Names.Tool}}/read_text
  max_iterations: 2
  system_prompt: runtime override
`), 0o644); err != nil {
		t.Fatalf("write override template: %v", err)
	}
	content, err := RenderMaterializedAgentConfig(root, KitPreset{
		Name:      "software-engineering",
		Title:     "Software Kit",
		Providers: []string{"backup"},
	}, MaterializedKitNames{
		Agent: "acme-agent",
		Tool:  "acme-helper",
	})
	if err != nil {
		t.Fatalf("RenderMaterializedAgentConfig: %v", err)
	}
	for _, want := range []string{
		"name: Runtime Override Agent",
		"provider: backup",
		"- acme-helper/read_text",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected runtime override to contain %q, got:\n%s", want, content)
		}
	}
}

func TestRenderMaterializedToolConfigUsesEmbeddedTemplate(t *testing.T) {
	root := t.TempDir()
	content, err := RenderMaterializedToolConfig(root, KitPreset{
		Name: "software-engineering",
	}, MaterializedKitNames{
		Kit:  "acme-kit",
		Tool: "acme-helper",
	})
	if err != nil {
		t.Fatalf("RenderMaterializedToolConfig: %v", err)
	}
	for _, want := range []string{
		"name: acme-helper",
		"args:",
		"- /goflow-tools/acme-helper.py",
		"tool_source: " + filepath.ToSlash(filepath.Join(root, "mcp_servers", "acme-helper.py")),
		"tool_target: /goflow-tools/acme-helper.py",
		"isolation: container",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected tool config to contain %q, got:\n%s", want, content)
		}
	}
}

func TestRenderMaterializedToolConfigUsesRuntimeOverride(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "templates", "kits", "materialized", "generic")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create template dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tool-config.yaml.tmpl"), []byte(`mcp_servers:
  - name: {{.Names.Tool}}
    args: [{{.Tool.Target}}]
    isolation_options:
      tool_source: {{.Tool.Source}}
`), 0o644); err != nil {
		t.Fatalf("write override template: %v", err)
	}
	content, err := RenderMaterializedToolConfig(root, KitPreset{Name: "software-engineering"}, MaterializedKitNames{
		Kit:  "acme-kit",
		Tool: "acme-helper",
	})
	if err != nil {
		t.Fatalf("RenderMaterializedToolConfig override: %v", err)
	}
	for _, want := range []string{
		"name: acme-helper",
		"args: [/goflow-tools/acme-helper.py]",
		"tool_source: " + filepath.ToSlash(filepath.Join(root, "mcp_servers", "acme-helper.py")),
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected runtime override to contain %q, got:\n%s", want, content)
		}
	}
}
