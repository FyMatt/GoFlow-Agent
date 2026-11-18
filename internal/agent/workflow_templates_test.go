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
		"repository-readiness-review":    false,
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

func TestSecurityAuditEvidenceGateRetriesEmptyAuditAndWriteStages(t *testing.T) {
	template, ok := (&WorkflowRunner{}).WorkflowTemplate("security-audit-evidence-gate")
	if !ok {
		t.Fatal("expected security-audit-evidence-gate template")
	}
	required := map[string]int{"audit": 3, "remediate": 3, "repair": 2, "recovery-verify": 2}
	seen := map[string]int{}
	for _, stage := range template.Graph.Stages {
		if want, ok := required[stage.Name]; ok {
			seen[stage.Name] = stage.Retry.MaxAttempts
			if stage.Retry.MaxAttempts < want {
				t.Fatalf("stage %s retry max_attempts=%d, want at least %d", stage.Name, stage.Retry.MaxAttempts, want)
			}
		}
	}
	for name := range required {
		if _, ok := seen[name]; !ok {
			t.Fatalf("missing required retry stage %s", name)
		}
	}
}

func TestLegacySecurityReviewTemplateAliasesToEvidenceGate(t *testing.T) {
	template, ok := (&WorkflowRunner{}).WorkflowTemplate("human-input-security-review")
	if !ok {
		t.Fatal("expected legacy security review alias to remain loadable")
	}
	if template.Name != "security-audit-evidence-gate" || template.Graph.Name != "security-audit-evidence-gate" {
		t.Fatalf("legacy security review alias should resolve to the evidence-gated workflow, got %#v", template)
	}
	for _, summary := range (&WorkflowRunner{}).WorkflowTemplates() {
		if summary.Name == "human-input-security-review" {
			t.Fatal("legacy security review alias must not remain visible in the template list")
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
	for _, name := range []string{"complex-project-delivery", "plan-implement-audit", "plan-fix-audit"} {
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
		allowsRaceFallback := false
		for _, stage := range template.Graph.Stages {
			if stage.NodeType == "quality_gate" && strings.Contains(stage.Params["allowed_infrastructure_failures"], "go_race_unavailable") {
				allowsRaceFallback = true
				break
			}
		}
		if !allowsRaceFallback {
			t.Fatalf("workflow template %s must allow only the verified go-race infrastructure fallback", name)
		}
	}
}

func TestSoftwareReviewUsesReadOnlyEvidenceAndRetainsRepairFiles(t *testing.T) {
	template, ok := (&WorkflowRunner{}).WorkflowTemplate("plan-implement-audit")
	if !ok {
		t.Fatal("missing plan-implement-audit template")
	}
	for _, stage := range template.Graph.Stages {
		switch stage.Name {
		case "audit", "recovery-audit":
			if stage.Params["tools"] != "file_tools/read_file,file_tools/read_file_range" || stage.Params["max_read_calls"] != "3" {
				t.Fatalf("%s must inspect bounded evidence without rerunning pytest: %#v", stage.Name, stage.Params)
			}
		case "verify", "recovery-verify":
			if !strings.Contains(stage.Params["completion_policy"], "original requirement") {
				t.Fatalf("%s must verify the original request, not only existing smoke tests", stage.Name)
			}
		case "final-report":
			if stage.Input["repair_changed_files"] != "stages.repair.outputs.changed_files" {
				t.Fatalf("final report must receive actual repaired paths: %#v", stage.Input)
			}
		}
	}
}

func TestComplexProjectDeliveryAcceptsStructuredEngineeringOutputs(t *testing.T) {
	template, ok := (&WorkflowRunner{}).WorkflowTemplate("complex-project-delivery")
	if !ok {
		t.Fatal("expected complex-project-delivery template")
	}
	byName := make(map[string]WorkflowGraphStageDocument, len(template.Graph.Stages))
	for _, stage := range template.Graph.Stages {
		byName[stage.Name] = stage
	}
	for _, name := range []string{"implement", "repair"} {
		stage, exists := byName[name]
		if !exists {
			t.Fatalf("expected %s stage", name)
		}
		if !strings.EqualFold(strings.TrimSpace(stage.Params["worker_contract"]), "engineering_v1") {
			t.Fatalf("%s must use engineering_v1, got %#v", name, stage.Params)
		}
		refs := make([]string, 0, len(stage.AcceptanceCriteria))
		for _, criterion := range stage.AcceptanceCriteria {
			if strings.Contains(criterion.Ref, "变更") {
				t.Fatalf("%s must not require Chinese title literals, got %#v", name, criterion)
			}
			refs = append(refs, criterion.Ref)
		}
		joined := strings.Join(refs, " ")
		for _, required := range []string{"result.summary", "result.changed_files", "result.verification"} {
			if !strings.Contains(joined, required) {
				t.Fatalf("%s missing %s acceptance, got %#v", name, required, refs)
			}
		}
	}
}

func TestSecurityAuditTemplateGatesTheDeliveredPassiveReport(t *testing.T) {
	runner := &WorkflowRunner{}
	template, ok := runner.WorkflowTemplate("security-audit-evidence-gate")
	if !ok {
		t.Fatal("expected security-audit-evidence-gate template")
	}
	stages := make(map[string]WorkflowGraphStageDocument, len(template.Graph.Stages))
	for _, stage := range template.Graph.Stages {
		stages[stage.Name] = stage
	}
	audit := stages["audit"]
	if len(audit.Next) != 1 || audit.Next[0] != "approve-remediation" {
		t.Fatalf("security audit must flow through approval, got %#v", audit.Next)
	}
	if len(audit.AcceptanceCriteria) != 3 {
		t.Fatalf("security audit must retain three evidence boundaries, got %#v", audit.AcceptanceCriteria)
	}
	for _, criterion := range audit.AcceptanceCriteria {
		if criterion.Ref != "result.output" {
			t.Fatalf("security acceptance must validate the structured audit output, got %#v", criterion)
		}
	}
	quality := stages["quality"]
	// A warning means the evidence is acceptable with caveats: it must move the
	// delivery forward instead of forcing a repair round that has nothing to fix.
	if quality.Routes["fail"] != "repair" || quality.Routes["warning"] != "final-report" || quality.Routes["pass"] != "final-report" {
		t.Fatalf("security quality warning must forward into the delivery closure, got %#v", quality.Routes)
	}
	for _, required := range []string{"approve-remediation", "remediate", "verify", "review", "repair", "recovery-verify", "final-report"} {
		if _, ok := stages[required]; !ok {
			t.Fatalf("generic security workflow must contain stage %q", required)
		}
	}
	for _, issue := range runner.ValidateWorkflowGraphStartup(template.Graph.Name, template.Graph, false) {
		if issue.Level == "error" && issue.Stage == "remediate" {
			t.Fatalf("security remediation must carry a complete live execution contract: %#v", issue)
		}
	}
}

func TestSecurityFixTemplateGatesTheToolReadDelivery(t *testing.T) {
	if _, ok := (&WorkflowRunner{}).WorkflowTemplate("security-fix-plan"); ok {
		t.Fatal("fixed-vector security-fix-plan template must be removed; use security-audit-evidence-gate")
	}
}

func TestUnauthorizedRedTeamBranchGatesTheToolReadBlockReport(t *testing.T) {
	template, ok := (&WorkflowRunner{}).WorkflowTemplate("authorized-red-team-validation")
	if !ok {
		t.Fatal("expected authorized-red-team-validation template")
	}
	for _, stage := range template.Graph.Stages {
		if stage.Name != "blocked-report" {
			continue
		}
		if len(stage.AcceptanceCriteria) != 1 || stage.AcceptanceCriteria[0].Ref != "result.tool_results" {
			t.Fatalf("blocked red-team report must validate the tool-read delivery, got %#v", stage.AcceptanceCriteria)
		}
		return
	}
	t.Fatal("authorized-red-team-validation blocked-report stage not found")
}

func TestResearchTemplatesGateToolReadDeliveries(t *testing.T) {
	for _, name := range []string{"web-research-risk", "parallel-research-review"} {
		template, ok := (&WorkflowRunner{}).WorkflowTemplate(name)
		if !ok {
			t.Fatalf("expected %s template", name)
		}
		var report WorkflowGraphStageDocument
		var quality WorkflowGraphStageDocument
		found := false
		qualityFound := false
		for _, stage := range template.Graph.Stages {
			if stage.Name == "report" {
				report = stage
				found = true
			}
			if stage.Name == "quality" {
				quality = stage
				qualityFound = true
			}
		}
		if !found {
			t.Fatalf("%s report stage not found", name)
		}
		if !strings.Contains(report.Params["tools"], "file_tools/read_file") {
			t.Fatalf("%s report must read back every delivery, got %q", name, report.Params["tools"])
		}
		if len(report.AcceptanceCriteria) != 2 {
			t.Fatalf("%s report must retain two delivery criteria, got %#v", name, report.AcceptanceCriteria)
		}
		for _, criterion := range report.AcceptanceCriteria {
			if criterion.Ref != "result.tool_results" {
				t.Fatalf("%s acceptance must validate tool-read delivery content, got %#v", name, criterion)
			}
		}
		if len(report.Artifacts) != 1 || report.Artifacts[0].Ref != "result.tool_results" {
			t.Fatalf("%s final artifact must bind to tool evidence, got %#v", name, report.Artifacts)
		}
		if len(report.Next) != 1 || report.Next[0] != "quality" {
			t.Fatalf("%s must grade the tool-read delivery after report generation, got %#v", name, report.Next)
		}
		if !qualityFound {
			t.Fatalf("%s quality stage not found", name)
		}
		if !strings.Contains(quality.Params["stages"], "report") {
			t.Fatalf("%s quality gate must include the final report stage, got %q", name, quality.Params["stages"])
		}
		for _, route := range []string{"pass", "warning", "fail"} {
			if quality.Routes[route] != "end" {
				t.Fatalf("%s quality route %s must end after grading the delivery, got %#v", name, route, quality.Routes)
			}
		}
	}
}

func TestDocsReviewTemplateUsesOfficeAwareValidationAndFinalRepairGate(t *testing.T) {
	template, ok := (&WorkflowRunner{}).WorkflowTemplate("docs-review-publish")
	if !ok {
		t.Fatal("expected docs-review-publish template")
	}
	stages := make(map[string]WorkflowGraphStageDocument, len(template.Graph.Stages))
	for _, stage := range template.Graph.Stages {
		stages[stage.Name] = stage
	}
	for _, name := range []string{"validate", "repair"} {
		stage, ok := stages[name]
		if !ok {
			t.Fatalf("expected %s stage", name)
		}
		if strings.Contains(stage.Params["tools"], "file_tools/read_file") {
			t.Fatalf("%s must not expose text reads for Office binaries, got %q", name, stage.Params["tools"])
		}
		if !strings.Contains(stage.Params["tools"], "office_tools/validate_office_document") {
			t.Fatalf("%s must retain Office-aware validation, got %q", name, stage.Params["tools"])
		}
	}
	repair := stages["repair"]
	if len(repair.Next) != 1 || repair.Next[0] != "final-quality" {
		t.Fatalf("repair must flow through final-quality, got %#v", repair.Next)
	}
	finalQuality, ok := stages["final-quality"]
	if !ok || finalQuality.NodeType != "quality_gate" {
		t.Fatalf("expected final repair quality gate, got %#v", finalQuality)
	}
	if finalQuality.Params["required_tools"] != "office_tools/validate_office_document" || finalQuality.Params["verification_mode"] != "tool" || finalQuality.Routes["pass"] != "delivery" {
		t.Fatalf("unexpected final repair quality contract: %#v", finalQuality)
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

func TestRunnableWorkflowGraphFallsBackToTemplateAndPrefersSavedOverride(t *testing.T) {
	runtimeRef := newWorkflowGraphRuntime(t, t.TempDir(), workflowGraphTestSkills(), &stubRuntimeMCP{}, workflowGraphTestClients())
	runner := runtimeRef.WorkflowRunner()

	embedded, err := runner.LoadRunnableWorkflowGraphDocument("complex-project-delivery")
	if err != nil {
		t.Fatalf("load embedded runnable workflow: %v", err)
	}
	if embedded.Name != "complex-project-delivery" || len(embedded.Stages) != 15 {
		t.Fatalf("unexpected embedded runnable workflow: %#v", embedded)
	}

	embedded.Description = "saved workflow override"
	if err := runner.SaveWorkflowGraphDocument(embedded.Name, embedded); err != nil {
		t.Fatalf("save workflow override: %v", err)
	}
	loaded, err := runner.LoadRunnableWorkflowGraphDocument(embedded.Name)
	if err != nil {
		t.Fatalf("load saved workflow override: %v", err)
	}
	if loaded.Description != "saved workflow override" {
		t.Fatalf("expected saved graph to override embedded template, got %#v", loaded)
	}
}

func TestRunnableMultiDomainWorkflowUsesCompleteBuiltInTemplate(t *testing.T) {
	runtimeRef := newWorkflowGraphRuntime(t, t.TempDir(), workflowGraphTestSkills(), &stubRuntimeMCP{}, workflowGraphTestClients())
	runner := runtimeRef.WorkflowRunner()

	doc, err := runner.LoadRunnableWorkflowGraphDocument("multi-domain-intake-router")
	if err != nil {
		t.Fatalf("load built-in multi-domain workflow: %v", err)
	}
	if doc.Name != "multi-domain-intake-router" || len(doc.Stages) != 24 {
		t.Fatalf("unexpected runnable multi-domain workflow: %#v", doc)
	}
	for _, stageName := range []string{"research-worker", "docs-worker", "office-delivery-route", "office-delivery"} {
		if !workflowGraphHasStage(doc, stageName) {
			t.Fatalf("runnable multi-domain workflow is missing %q", stageName)
		}
	}
}

func TestWorkflowExecutorsIncludeRunnableBuiltInTemplate(t *testing.T) {
	runtimeRef := newWorkflowGraphRuntime(t, t.TempDir(), workflowGraphTestSkills(), &stubRuntimeMCP{}, workflowGraphTestClients())
	for _, item := range runtimeRef.WorkflowRunner().WorkflowExecutors() {
		if item.Name != "complex-project-delivery" {
			continue
		}
		if item.Source != "built_in" || !item.Valid || item.Editable || item.Stages != 15 || !strings.Contains(item.Detail, "bundled runnable workflow") {
			t.Fatalf("unexpected runnable built-in workflow entry: %#v", item)
		}
		return
	}
	t.Fatal("complex-project-delivery runnable workflow is missing")
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
