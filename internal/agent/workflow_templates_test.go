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
	if len(rows) < 17 {
		t.Fatalf("expected embedded workflow templates, got %#v", rows)
	}
	required := map[string]bool{
		"complex-project-delivery":       false,
		"engineering-parallel-delivery":  false,
		"task-decomposition-plan":        false,
		"multi-domain-intake-router":     false,
		"agent-framework-extension":      false,
		"plan-implement-audit":           false,
		"plan-fix-audit":                 false,
		"software-quality-gate":          false,
		"web-research-risk":              false,
		"security-audit-evidence-gate":   false,
		"authorized-red-team-validation": false,
		"parallel-research-review":       false,
		"binary-triage":                  false,
		"docs-review-publish":            false,
		"operations-runbook":             false,
		"customer-support-triage":        false,
		"human-input-security-review":    false,
		"software-team-review-gate":      false,
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

func TestBuiltInWorkflowTemplatesValidateAndMeetDeliveryQualityBar(t *testing.T) {
	runtimeRef := newWorkflowGraphRuntime(t, t.TempDir(), workflowGraphTestSkills(), &stubRuntimeMCP{}, workflowGraphTestClients())
	runner := runtimeRef.WorkflowRunner()
	for _, row := range runner.WorkflowTemplates() {
		template, ok := runner.WorkflowTemplate(row.Name)
		if !ok {
			t.Fatalf("expected workflow template %s", row.Name)
		}
		if strings.TrimSpace(template.Title) == "" || strings.TrimSpace(template.Description) == "" || strings.TrimSpace(template.Category) == "" || len(template.Tags) == 0 {
			t.Fatalf("workflow template %s is missing user-facing summary metadata: %#v", row.Name, template.WorkflowTemplateSummary)
		}
		if template.Stages != len(template.Graph.Stages) {
			t.Fatalf("workflow template %s stage count mismatch: summary=%d graph=%d", row.Name, template.Stages, len(template.Graph.Stages))
		}
		validation := runner.ValidateWorkflowGraphDocument(row.Name, template.Graph)
		if !validation.Valid {
			t.Fatalf("workflow template %s should validate, got %#v", row.Name, validation.Issues)
		}
		if !workflowTemplateQualityHasTerminalEnd(template.Graph) {
			t.Fatalf("workflow template %s must include an explicit end node", row.Name)
		}
		if !workflowTemplateQualityHasControlGate(template.Graph) {
			t.Fatalf("workflow template %s must include a quality gate or policy gate", row.Name)
		}
		if !workflowTemplateQualityHasAcceptance(template.Graph) {
			t.Fatalf("workflow template %s must include acceptance criteria", row.Name)
		}
		if !workflowTemplateQualityHasEvidenceArtifact(template.Graph) {
			t.Fatalf("workflow template %s must emit at least one evidence/report artifact", row.Name)
		}
		if !workflowTemplateQualityHasFinalUserOutput(template.Graph) {
			t.Fatalf("workflow template %s must include a final report, handoff, publish, or materialize stage", row.Name)
		}
	}
}

func TestBuiltInSoftwareWorkflowTemplatesUseEngineeringTokenControls(t *testing.T) {
	runtimeRef := newWorkflowGraphRuntime(t, t.TempDir(), workflowGraphTestSkills(), &stubRuntimeMCP{}, workflowGraphTestClients())
	runner := runtimeRef.WorkflowRunner()
	for _, name := range []string{"engineering-parallel-delivery", "complex-project-delivery", "plan-implement-audit", "plan-fix-audit"} {
		template, ok := runner.WorkflowTemplate(name)
		if !ok {
			t.Fatalf("expected workflow template %s", name)
		}
		if !workflowTemplateHasPromptBudget(template.Graph) {
			t.Fatalf("workflow template %s must declare stage prompt budgets", name)
		}
		if !workflowTemplateHasWorkerContract(template.Graph) {
			t.Fatalf("workflow template %s must use structured worker contracts", name)
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

func workflowTemplateQualityHasTerminalEnd(doc WorkflowGraphDocument) bool {
	for _, stage := range doc.Stages {
		if strings.EqualFold(strings.TrimSpace(stage.NodeType), "end") {
			return true
		}
	}
	return false
}

func workflowTemplateQualityHasControlGate(doc WorkflowGraphDocument) bool {
	for _, stage := range doc.Stages {
		switch strings.ToLower(strings.TrimSpace(stage.NodeType)) {
		case "quality_gate", "quality_guard", "quality-guard", "policy_guard", "guard":
			return true
		}
	}
	return false
}

func workflowTemplateQualityHasAcceptance(doc WorkflowGraphDocument) bool {
	for _, stage := range doc.Stages {
		if len(stage.AcceptanceCriteria) > 0 {
			return true
		}
	}
	return false
}

func workflowTemplateQualityHasEvidenceArtifact(doc WorkflowGraphDocument) bool {
	for _, stage := range doc.Stages {
		for _, artifact := range stage.Artifacts {
			kind := strings.ToLower(strings.TrimSpace(artifact.Kind))
			name := strings.ToLower(strings.TrimSpace(artifact.Name))
			switch kind {
			case "report", "verification", "evidence", "audit", "plan", "acceptance", "test", "tests", "diff", "patch", "change_report", "requirements":
				return true
			}
			if strings.Contains(name, "report") || strings.Contains(name, "handoff") || strings.Contains(name, "review") || strings.Contains(name, "plan") {
				return true
			}
		}
	}
	return false
}

func workflowTemplateQualityHasFinalUserOutput(doc WorkflowGraphDocument) bool {
	for _, stage := range doc.Stages {
		name := strings.ToLower(strings.TrimSpace(stage.Name))
		if strings.Contains(name, "report") || strings.Contains(name, "handoff") || strings.Contains(name, "publish") || strings.Contains(name, "materialize") || strings.Contains(name, "summary") {
			return true
		}
		for key := range stage.Outputs {
			output := strings.ToLower(strings.TrimSpace(key))
			if strings.Contains(output, "report") || strings.Contains(output, "handoff") || strings.Contains(output, "summary") {
				return true
			}
		}
	}
	return false
}

func workflowTemplateHasPromptBudget(doc WorkflowGraphDocument) bool {
	for _, stage := range doc.Stages {
		if stage.Context.PromptMaxTokens > 0 && stage.Context.RequestMaxTokens > 0 && stage.Context.InputsMaxTokens > 0 && stage.Context.ParametersMaxTokens > 0 {
			return true
		}
	}
	return false
}

func workflowTemplateHasWorkerContract(doc WorkflowGraphDocument) bool {
	for _, stage := range doc.Stages {
		if strings.EqualFold(strings.TrimSpace(stage.Params["worker_contract"]), "engineering_v1") {
			return true
		}
	}
	return false
}
