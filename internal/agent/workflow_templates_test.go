package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
)

func TestBuiltInWorkflowTemplatesLoadFromEmbeddedYAML(t *testing.T) {
	rows := (&WorkflowRunner{}).WorkflowTemplates()
	if len(rows) < 14 {
		t.Fatalf("expected embedded workflow templates, got %#v", rows)
	}
	required := map[string]bool{
		"complex-project-delivery":     false,
		"task-decomposition-plan":      false,
		"multi-domain-intake-router":   false,
		"agent-framework-extension":    false,
		"plan-fix-audit":               false,
		"software-quality-gate":        false,
		"web-research-risk":            false,
		"security-audit-evidence-gate": false,
		"parallel-research-review":     false,
		"binary-triage":                false,
		"docs-review-publish":          false,
		"operations-runbook":           false,
		"customer-support-triage":      false,
		"human-input-security-review":  false,
		"software-team-review-gate":    false,
	}
	for _, row := range rows {
		if _, ok := required[row.Name]; ok {
			required[row.Name] = true
			if row.Source != "built_in" || row.Stages == 0 || row.Kind != workflowTemplateResourceKind || row.Version != workflowTemplateResourceVersion {
				t.Fatalf("unexpected embedded workflow template summary for %s: %#v", row.Name, row)
			}
		}
	}
	for name, found := range required {
		if !found {
			t.Fatalf("missing embedded workflow template %s in %#v", name, rows)
		}
	}
}

func TestBuiltInWorkflowTemplateDetailIsCloned(t *testing.T) {
	first, ok := (&WorkflowRunner{}).WorkflowTemplate("plan-fix-audit")
	if !ok {
		t.Fatal("expected plan-fix-audit template")
	}
	if len(first.Graph.Stages) < 3 || first.Graph.Stages[1].Name != "intake" || first.Graph.Stages[2].Name != "plan" {
		t.Fatalf("unexpected plan-fix-audit stages: %#v", first.Graph.Stages)
	}
	first.Graph.Stages[1].Name = "mutated"
	first.Graph.Stages[2].Outputs["plan"] = "mutated"

	second, ok := (&WorkflowRunner{}).WorkflowTemplate("plan-fix-audit")
	if !ok {
		t.Fatal("expected plan-fix-audit template after mutation")
	}
	if second.Graph.Stages[1].Name != "intake" || second.Graph.Stages[2].Name != "plan" || second.Graph.Stages[2].Outputs["plan"] == "mutated" {
		t.Fatalf("expected cloned embedded workflow template, got %#v", second)
	}
}

func TestRuntimeWorkflowTemplateOverridesEmbeddedBuiltIn(t *testing.T) {
	runtimeHome := t.TempDir()
	dir := filepath.Join(runtimeHome, "templates", "workflows")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir workflow template dir: %v", err)
	}
	custom := `
kind: goflow.workflow_template_resource
version: 2
min_supported_version: 1
name: plan-fix-audit
title: Custom Plan Template
category: custom
graph:
  name: plan-fix-audit
  stages:
    - name: plan
      node_type: agent
      agent: planner
      skill: execution-plan
`
	if err := os.WriteFile(filepath.Join(dir, "plan-fix-audit.yaml"), []byte(strings.TrimSpace(custom)+"\n"), 0o644); err != nil {
		t.Fatalf("write custom workflow template: %v", err)
	}
	runner := NewWorkflowRunner(&Runtime{cfg: &config.Config{RuntimeHome: runtimeHome}})
	template, ok := runner.WorkflowTemplate("plan-fix-audit")
	if !ok {
		t.Fatal("expected runtime workflow template")
	}
	if !template.Custom || template.Source != "custom" || template.Title != "Custom Plan Template" || len(template.Graph.Stages) != 1 {
		t.Fatalf("expected custom template to override embedded template, got %#v", template)
	}
}

func TestRuntimeWorkflowTemplateLoadsLegacyNestedSummary(t *testing.T) {
	runtimeHome := t.TempDir()
	dir := filepath.Join(runtimeHome, "templates", "workflows")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir workflow template dir: %v", err)
	}
	legacy := `
workflowtemplatesummary:
  kind: goflow.workflow_template_resource
  version: 2
  min_supported_version: 1
  name: legacy-plan-template
  title: Legacy Plan Template
  category: legacy
  tags: [legacy, plan]
graph:
  name: legacy-plan-template
  stages:
    - name: plan
      node_type: agent
      agent: planner
      skill: execution-plan
`
	if err := os.WriteFile(filepath.Join(dir, "legacy-plan-template.yaml"), []byte(strings.TrimSpace(legacy)+"\n"), 0o644); err != nil {
		t.Fatalf("write legacy workflow template: %v", err)
	}
	runner := NewWorkflowRunner(&Runtime{cfg: &config.Config{RuntimeHome: runtimeHome}})
	template, ok := runner.WorkflowTemplate("legacy-plan-template")
	if !ok {
		t.Fatal("expected legacy workflow template")
	}
	if template.Title != "Legacy Plan Template" || template.Category != "legacy" || len(template.Tags) != 2 {
		t.Fatalf("expected legacy nested summary to load, got %#v", template)
	}
}
