package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
)

func TestBuiltInTeamTemplatesLoadFromEmbeddedYAML(t *testing.T) {
	rows := TeamTemplates()
	if len(rows) < 8 {
		t.Fatalf("expected embedded team templates, got %#v", rows)
	}
	required := map[string]bool{
		"software-task-team":       false,
		"framework-extension-team": false,
		"audit-security-team":      false,
		"web-research-team":        false,
		"binary-triage-team":       false,
		"documentation-team":       false,
		"operations-runbook-team":  false,
		"customer-support-team":    false,
	}
	for _, row := range rows {
		if _, ok := required[row.Name]; ok {
			required[row.Name] = true
			if row.Source != "built_in" || row.Roles == 0 || row.Kind != teamTemplateResourceKind || row.Version != teamTemplateResourceVersion {
				t.Fatalf("unexpected embedded team template summary for %s: %#v", row.Name, row)
			}
		}
	}
	for name, found := range required {
		if !found {
			t.Fatalf("missing embedded team template %s in %#v", name, rows)
		}
	}
}

func TestBuiltInTeamTemplateDetailIsCloned(t *testing.T) {
	first, ok := LoadTeamTemplate("web-research-team")
	if !ok {
		t.Fatal("expected web research team template")
	}
	if len(first.RoleTemplates) == 0 || first.RoleTemplates[0].Name != "asset-collector" {
		t.Fatalf("unexpected web research team roles: %#v", first.RoleTemplates)
	}
	first.RoleTemplates[0].Name = "mutated"
	first.RoleTemplates[0].Responsibilities[0] = "mutated"

	second, ok := LoadTeamTemplate("web-research-team")
	if !ok {
		t.Fatal("expected web research team template after mutation")
	}
	if second.RoleTemplates[0].Name != "asset-collector" || second.RoleTemplates[0].Responsibilities[0] == "mutated" {
		t.Fatalf("expected cloned embedded team template, got %#v", second)
	}
}

func TestRuntimeTeamTemplateOverridesEmbeddedBuiltIn(t *testing.T) {
	runtimeHome := t.TempDir()
	dir := filepath.Join(runtimeHome, "templates", "teams")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir team template dir: %v", err)
	}
	custom := `
kind: goflow.team_template_resource
version: 2
min_supported_version: 1
name: web-research-team
title: Custom Web Research Team
category: custom
tags: [custom]
role_templates:
  - name: lead
    agent: planner
    skill: execution-plan
handoffs: []
blackboard_templates: []
output_contract: [custom_report]
`
	if err := os.WriteFile(filepath.Join(dir, "web-research-team.yaml"), []byte(strings.TrimSpace(custom)+"\n"), 0o644); err != nil {
		t.Fatalf("write custom team template: %v", err)
	}
	runner := NewWorkflowRunner(&Runtime{cfg: &config.Config{RuntimeHome: runtimeHome}})
	template, ok := runner.TeamTemplate("web-research-team")
	if !ok {
		t.Fatal("expected runtime team template")
	}
	if !template.Custom || template.Source != "custom" || template.Title != "Custom Web Research Team" || len(template.RoleTemplates) != 1 {
		t.Fatalf("expected custom template to override embedded template, got %#v", template)
	}
}

func TestRuntimeTeamTemplateLoadsLegacyNestedSummary(t *testing.T) {
	runtimeHome := t.TempDir()
	dir := filepath.Join(runtimeHome, "templates", "teams")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir team template dir: %v", err)
	}
	legacy := `
teamtemplatesummary:
  kind: goflow.team_template_resource
  version: 2
  min_supported_version: 1
  name: legacy-review-team
  title: Legacy Review Team
  category: legacy
  tags: [legacy, review]
role_templates:
  - name: reviewer
    agent: auditor
    skill: code-audit
handoffs: []
blackboard_templates: []
output_contract: [review]
`
	if err := os.WriteFile(filepath.Join(dir, "legacy-review-team.yaml"), []byte(strings.TrimSpace(legacy)+"\n"), 0o644); err != nil {
		t.Fatalf("write legacy team template: %v", err)
	}
	runner := NewWorkflowRunner(&Runtime{cfg: &config.Config{RuntimeHome: runtimeHome}})
	template, ok := runner.TeamTemplate("legacy-review-team")
	if !ok {
		t.Fatal("expected legacy team template")
	}
	if template.Title != "Legacy Review Team" || template.Category != "legacy" || len(template.Tags) != 2 {
		t.Fatalf("expected legacy nested summary to load, got %#v", template)
	}
}
