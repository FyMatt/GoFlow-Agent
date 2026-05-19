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
		Tools:       []string{"file_tools/read_file", "network_tools/device_discovery_plan"},
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
		"- acme-helper",
		"generated_agent: acme-agent",
		"materialized: true",
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

func TestRenderMaterializedAgentSkillTeamAndWorkflowInheritBuiltinTools(t *testing.T) {
	preset := KitPreset{
		Name:        "operations-runbook",
		Title:       "Operations Kit",
		Description: "Plan safe operations changes.",
		Category:    "operations",
		Tags:        []string{"operations"},
		Providers:   []string{"primary"},
		Tools: []string{
			"file_tools/read_file",
			"network_tools/device_discovery_plan",
			"network_tools/device_command_plan",
			"network_tools/device_config_dry_run",
		},
	}
	names := MaterializedKitNames{
		Kit:              "ops-kit",
		Agent:            "ops-agent",
		Skill:            "ops-skill",
		Tool:             "ops-helper",
		Workflow:         "ops-workflow",
		WorkflowTemplate: "ops-template",
		TeamTemplate:     "ops-team",
		PolicyRule:       "ops-gate",
	}
	rendered := map[string]string{}
	var err error
	rendered["agent"], err = RenderMaterializedAgentConfig("", preset, names)
	if err != nil {
		t.Fatalf("RenderMaterializedAgentConfig: %v", err)
	}
	rendered["skill"], err = RenderMaterializedSkillMarkdown("", preset, names)
	if err != nil {
		t.Fatalf("RenderMaterializedSkillMarkdown: %v", err)
	}
	rendered["team"], err = RenderMaterializedTeamTemplateYAML("", preset, names)
	if err != nil {
		t.Fatalf("RenderMaterializedTeamTemplateYAML: %v", err)
	}
	rendered["workflow"], err = RenderMaterializedWorkflowTemplateYAML("", preset, names)
	if err != nil {
		t.Fatalf("RenderMaterializedWorkflowTemplateYAML: %v", err)
	}
	for label, content := range rendered {
		for _, want := range []string{
			"file_tools/read_file",
			"network_tools are planning and policy-check tools only",
		} {
			if !strings.Contains(content, want) {
				t.Fatalf("%s should contain %q, got:\n%s", label, want, content)
			}
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

func TestRenderMaterializedWorkflowTemplateUsesModelAndContextBudgets(t *testing.T) {
	content, err := RenderMaterializedWorkflowTemplateYAML("", KitPreset{
		Name:      "software-engineering",
		Title:     "Software Kit",
		Category:  "software",
		Tags:      []string{"coding"},
		Providers: []string{"backup"},
	}, MaterializedKitNames{
		Agent:            "acme-agent",
		Skill:            "acme-skill",
		WorkflowTemplate: "acme-template",
		TeamTemplate:     "acme-team",
		PolicyRule:       "acme-gate",
	})
	if err != nil {
		t.Fatalf("RenderMaterializedWorkflowTemplateYAML: %v", err)
	}
	for _, want := range []string{
		"name: acme-template",
		"provider: backup",
		"max_tokens: 1200",
		"context:",
		"large_file_contents",
		"token_policy:",
		"activation",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected rendered workflow template to contain %q, got:\n%s", want, content)
		}
	}
}
